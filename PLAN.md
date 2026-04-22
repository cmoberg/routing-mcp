# Implementation Plan: Go Client for FRR mgmtd Native Protocol

## Build & Test Environment

### Container topology

```text
┌─────────────────────────────────────────────┐
│ Docker Desktop (Linux VM, x86_64)           │
│                                             │
│  ┌──────────────┐    named volume           │
│  │  frr         │ ──► /run/frr/  ◄──────┐  │
│  │  (mgmtd,     │    mgmtd_fe.sock       │  │
│  │   zebra,     │                        │  │
│  │   staticd,   │                  ┌─────┴──┤
│  │   ripd)      │                  │  test  │
│  └──────────────┘                  │  (go   │
│                                    │  test) │
│                                    └────────┤
└─────────────────────────────────────────────┘
```

The macOS host cannot directly reach the Unix socket (it lives in the Docker Desktop VM), so integration tests always run inside the `test` container. Unit tests have no FRR dependency and run natively on macOS via `go test`.

### Files to create

```text
routing-mcp/
  docker/
    frr/
      daemons          # enable mgmtd, zebra, staticd, ripd
      frr.conf         # minimal bootstrap config
    Dockerfile.test    # golang image + test runner
  docker-compose.yml
  Makefile
```

### docker/frr/daemons

```ini
zebra=yes
bgpd=no
ospfd=no
ospf6d=no
ripd=yes
ripngd=no
isisd=no
eigrpd=no
nhrpd=no
pimd=no
pim6d=no
ldpd=no
babeld=no
sharpd=no
pbrd=no
bfdd=no
fabricd=no
vrrpd=no
pathd=no
mgmtd=yes
staticd=yes
```

### docker/frr/frr.conf

```text
frr version 10.2
frr defaults traditional
hostname frr-test
log syslog informational
service integrated-vtysh-config
!
ip route 10.99.0.0/24 Null0
!
```

(The static route gives us something to read back in Step 9.)

### docker-compose.yml

```yaml
services:
  frr:
    image: frrouting/frr:10.2
    hostname: frr-test
    privileged: true
    volumes:
      - frr-run:/run/frr
      - ./docker/frr/daemons:/etc/frr/daemons:ro
      - ./docker/frr/frr.conf:/etc/frr/frr.conf:ro
    healthcheck:
      test: ["CMD", "test", "-S", "/run/frr/mgmtd_fe.sock"]
      interval: 2s
      timeout: 5s
      retries: 15
      start_period: 5s

  test:
    build:
      context: .
      dockerfile: docker/Dockerfile.test
    volumes:
      - frr-run:/run/frr
      - .:/workspace
    working_dir: /workspace
    environment:
      - FRR_SOCK=/run/frr/mgmtd_fe.sock
    depends_on:
      frr:
        condition: service_healthy

volumes:
  frr-run:
```

### docker/Dockerfile.test

```dockerfile
FROM golang:1.23-alpine
RUN apk add --no-cache bash
WORKDIR /workspace
# Dependencies cached in a separate layer
COPY go.mod go.sum ./
RUN go mod download
CMD ["go", "test", "-v", "-tags", "integration", "./..."]
```

### Makefile

```makefile
.PHONY: up down test test-unit test-integration shell logs

# Start FRR container in background
up:
	docker compose up -d frr
	docker compose run --rm test go build ./...

# Stop and remove everything including the socket volume
down:
	docker compose down -v

# Fast: unit tests run natively on macOS, no FRR needed
test-unit:
	go test ./... -run 'Unit|unit' -v

# Slow: integration tests run inside the test container
test-integration: up
	docker compose run --rm test go test -v -tags integration -timeout 30s ./...

# Both
test: test-unit test-integration

# Interactive shell in test container for debugging
shell: up
	docker compose run --rm test bash

# Tail FRR logs
logs:
	docker compose logs -f frr

# Run a single test by name: make run-test T=TestGetData
run-test: up
	docker compose run --rm test go test -v -tags integration -run $(T) ./...
```

### Test tagging convention

Unit tests (no FRR): no build tag, name prefix `TestUnit`.
Integration tests (require FRR socket): `//go:build integration` tag at top of `_integration_test.go` files. The `FRR_SOCK` env var carries the socket path into the test binary.

### Dev iteration loop

```bash
# First time:
make up          # pulls frrouting/frr:10.2, starts mgmtd (~30s)

# Inner loop:
make test-unit   # native macOS, ~1s
make test-integration  # container, ~5s once frr is up

# Debugging:
make shell       # bash inside test container, full go toolchain
make logs        # watch FRR daemon output
make run-test T=TestSessionCreate   # run a single test

# Clean slate:
make down && make up
```

---

## Step-by-Step Implementation Plan

### Step 0 — Verify frame byte order

**Task**: Read `frr/lib/mgmt_msg.c`, locate the function that writes the `mgmt_msg_hdr` to the socket, and confirm whether `marker` and `len` are written in host (little-endian) or network (big-endian) byte order.

**Decision point**: If `stream_putl()` is used → big-endian. If the struct is written directly with `write()`/`stream_put()` → native/little-endian. Expected: little-endian (Unix socket between local processes).

**Success criteria**:

- The byte order is identified from source, not assumed.
- A comment in `pkg/frrmgmt/frame.go` cites the file and function name as evidence.
- A `TestUnitFrameMarkerBytes` test encodes a frame and asserts the exact first 4 bytes match the expected wire encoding (e.g. `[]byte{0x01, 0x23, 0x23, 0x23}` for little-endian native marker version 1).

---

### Step 1 — Project scaffold

**Task**: Initialize Go module, create the package directory tree, add a `go.sum`, confirm the build is clean.

```text
routing-mcp/
  go.mod                    module routing-mcp, go 1.23
  pkg/
    frrmgmt/
      frame.go              stub
      msg.go                stub
      encode.go             stub
      conn.go               stub
      dispatch.go           stub
      session.go            stub
      client.go             stub
  cmd/
    routing-mcp/
      main.go               package main, func main() {}
```

No external dependencies yet. `encoding/binary`, `net`, `sync`, `context` are all stdlib.

**Success criteria**:

- `go build ./...` exits 0 with no output.
- `go vet ./...` exits 0.
- `go test ./...` exits 0 (no tests yet, but no compilation errors).

---

### Step 2 — Frame layer (`frame.go`)

**Task**: Implement read/write of the outer wire frame.

Wire format (two 4-byte fields before payload):

```text
[marker uint32][len uint32][payload: len bytes]
```

- `marker` = `0x23232300 | 1` (version byte 1 = native)
- `len` = byte count of payload (the message struct + variable data)
- Byte order determined in Step 0

```go
const (
    markerPrefix  = uint32(0x23232300)
    versionNative = uint8(1)
    maxFrameSize  = 256 * 1024
)

func ReadFrame(r io.Reader) (payload []byte, err error)
func WriteFrame(w io.Writer, payload []byte) error
```

`ReadFrame` must:

1. Read 8 bytes.
2. Validate `marker & 0xFFFFFF00 == 0x23232300`.
3. Extract version from low byte.
4. Reject version ≠ 1.
5. Read exactly `len` more bytes.
6. Reject `len > maxFrameSize`.

**Success criteria**:

- `TestUnitFrameRoundTrip`: encode then decode a payload, get identical bytes.
- `TestUnitFrameMarkerBytes`: asserts exact wire bytes of marker (verifies byte order from Step 0).
- `TestUnitFrameBadMarker`: `ReadFrame` returns a non-nil error when marker bytes are wrong.
- `TestUnitFrameOversized`: `ReadFrame` returns an error when `len > maxFrameSize`.
- `TestUnitFrameTruncated`: `ReadFrame` on an `io.Reader` that closes mid-payload returns an error (not a panic).
- `TestUnitFrameEmptyPayload`: zero-length payload encodes and decodes without error.

---

### Step 3 — Message type definitions (`msg.go`)

**Task**: Define all PUBLIC message constants and fixed-field Go structs. Variable data is not included in the structs — it is passed separately as `[]byte` and appended after serializing the struct.

Every fixed struct must serialize to exactly 32 bytes (24-byte header + 8 bytes of fixed fields, matching the C `_Static_assert` that `sizeof(msg) == offsetof(msg, variable_field)`).

```go
// MsgHeader is 24 bytes — 3 × uint64-aligned words.
type MsgHeader struct {
    Code    uint16
    Resv    uint16
    VSplit  uint32
    ReferID uint64
    ReqID   uint64
}
```

All fixed structs embed `MsgHeader` as the first field and pad their specific fields to 8 bytes total:

| Struct | Specific fields | Pad |
| --- | --- | --- |
| `SessionReqFixed` | `NotifyFormat uint8` | `[7]byte` |
| `SessionReplyFixed` | `Created uint8` | `[7]byte` |
| `GetDataFixed` | `ResultType, Flags, Defaults, Datastore uint8` | `[4]byte` |
| `TreeDataFixed` | `PartialError int8, ResultType, More uint8` | `[5]byte` |
| `EditFixed` | `RequestType, Flags, Datastore, Operation uint8` | `[4]byte` |
| `EditReplyFixed` | `Changed, Created uint8` | `[6]byte` |
| `LockFixed` | `Datastore, Lock uint8` | `[6]byte` |
| `LockReplyFixed` | `Datastore, Lock uint8` | `[6]byte` |
| `CommitFixed` | `Source, Target, Action, Unlock uint8` | `[4]byte` |
| `CommitReplyFixed` | `Source, Target, Action, Unlock uint8` | `[4]byte` |
| `NotifySelectFixed` | `Replace, GetOnly, Subscribing uint8` | `[5]byte` |
| `NotifyDataFixed` | `ResultType, Op uint8` | `[6]byte` |
| `ErrorFixed` | `Error int16` | `[6]byte` |

**Success criteria**:

- `TestUnitMsgHeaderSize`: `binary.Size(MsgHeader{}) == 24`.
- `TestUnitAllFixedStructSizes`: for every fixed struct, `binary.Size(T{}) == 32`.
- `TestUnitConstantValues`: spot-check that Go constants match the C header values (e.g. `CodeGetData == 3`, `FormatJSON == 2`, `EditOpMerge == 2`, `CommitApply == 0`, `DatastoreCandidate == 2`).
- No test should use a numeric literal for a constant — this forces the test to encode the mapping explicitly.

---

### Step 4 — Variable-data encoding (`encode.go`)

**Task**: Implement helpers for the two variable-data patterns used in PUBLIC messages.

**Pattern A — single NUL-terminated string** (xpath in `GET_DATA`, client name in `SESSION_REQ`):

```go
func AppendString(s string) []byte           // s + "\x00"
```

**Pattern B — xpath split + secondary data** (`vsplit` pattern in `EDIT`, `NOTIFY`, `EDIT_REPLY`):

```go
// Encode: returns vsplit value and combined payload bytes.
// vsplit = len(xpath)+1, payload = xpath\x00 + data
func EncodeXpathData(xpath string, data []byte) (vsplit uint32, payload []byte)

// Decode: splits payload at vsplit into xpath string and secondary data.
func DecodeXpathData(vsplit uint32, payload []byte) (xpath string, data []byte, err error)
```

**Pattern C — NUL-separated string list** (`NOTIFY_SELECT.selectors`):

```go
func EncodeNulStrings(strs []string) []byte   // each str + "\x00"
func DecodeNulStrings(b []byte) []string       // split on "\x00", drop empty trailing
```

**Success criteria**:

- `TestUnitEncodeXpathDataRoundTrip`: encode then decode returns identical xpath and data.
- `TestUnitEncodeXpathDataVsplit`: the returned `vsplit` equals `len(xpath)+1` exactly.
- `TestUnitEncodeXpathDataEmptyData`: encoding with `nil` data and decoding returns `xpath` and `nil` data.
- `TestUnitDecodeXpathDataCorrupt`: `vsplit` pointing past the end of payload returns an error.
- `TestUnitNulStringsRoundTrip`: encode then decode returns identical slice for 0, 1, and 5 strings.
- `TestUnitNulStringsEmptyInput`: `DecodeNulStrings(nil)` returns an empty (not nil) slice.

---

### Step 5 — Connection layer (`conn.go`)

**Task**: Implement Unix socket connection with automatic reconnect.

```go
type Conn struct { /* unexported */ }

// Dial connects to the mgmtd_fe socket at sockPath.
// Returns immediately; if the socket is not yet present it retries in the background.
func Dial(ctx context.Context, sockPath string) *Conn

// Send writes a fully-encoded payload (fixed struct + variable data) as one frame.
func (c *Conn) Send(payload []byte) error

// Frames returns a channel that delivers incoming raw payloads.
// Closed when ctx is cancelled.
func (c *Conn) Frames() <-chan []byte
```

Reconnect strategy: 100ms → 200ms → 400ms → ... → 5s cap, with ±10% jitter. On reconnect the `Frames()` channel keeps delivering; the session layer above is responsible for noticing the disconnect and re-establishing the session.

**Decision point**: Should `Send` block until the socket is connected, or return an error immediately if disconnected? Decision: return `ErrNotConnected` immediately — the caller (session layer) handles reconnect.

**Success criteria**:

- `TestUnitConnReconnectBackoff`: using a fake clock, verify the retry delays follow the capped exponential sequence.
- `TestIntegrationConnDial`: `Dial` against the live FRR socket path succeeds within 2s.
- `TestIntegrationConnFrameRoundTrip`: send a raw payload, receive it back (requires a loopback echo — or validated indirectly via Step 7 session test).
- `TestIntegrationConnReconnect`: close the FRR container, wait, restart it; verify `Frames()` resumes delivering messages without the test needing to call `Dial` again.

---

### Step 6 — Request/response dispatch (`dispatch.go`)

**Task**: Correlate `req_id`s between outgoing requests and incoming replies. Deliver async `NOTIFY` messages to a fan-out channel.

```go
type Dispatcher struct { /* unexported */ }

func NewDispatcher(frames <-chan []byte) *Dispatcher

// NextReqID returns a unique, monotonically increasing request ID.
func (d *Dispatcher) NextReqID() uint64

// Expect registers a pending request. ch will receive all reply frames
// for req_id until either the reply arrives (multi=false) or all
// fragments arrive (multi=true, last fragment has more=0).
func (d *Dispatcher) Expect(reqID uint64, multi bool) <-chan []byte

// Cancel removes a pending request (used on context cancellation).
func (d *Dispatcher) Cancel(reqID uint64)

// Notifications returns a channel that receives all NOTIFY frames.
func (d *Dispatcher) Notifications() <-chan []byte
```

The internal read goroutine decodes the `MsgHeader` from each raw payload to get `Code` and `ReqID`, then routes:

- `Code == CodeNotify` → broadcast to notifications channel.
- Any other code with a matching pending `reqID` → send to that pending channel.
- Unmatched reply (no pending entry) → log and discard.

**Success criteria**:

- `TestUnitDispatchSingleReply`: register a pending req_id, push a matching frame, receive it on the channel.
- `TestUnitDispatchTwoInflight`: two simultaneous pending req_ids each receive exactly their own reply.
- `TestUnitDispatchNotify`: a frame with `CodeNotify` goes to `Notifications()` not to any pending channel.
- `TestUnitDispatchUnmatched`: a frame with no pending entry does not block or panic.
- `TestUnitDispatchCancel`: after `Cancel(reqID)`, a late arriving reply is discarded cleanly.
- `TestUnitDispatchMultiFragment`: three frames with `more=1,1,0` all arrive on the same pending channel; channel is not closed until `more=0`.

---

### Step 7 — Session management (`session.go`)

**Task**: Implement the SESSION_REQ / SESSION_REPLY handshake. The session owns the `session_id` used as `refer_id` on all subsequent requests.

```go
type Session struct { /* unexported */ }

// New sends SESSION_REQ and waits for SESSION_REPLY.
// clientName appears in `show mgmt clients` in vtysh.
func New(ctx context.Context, conn *Conn, d *Dispatcher, clientName string) (*Session, error)

// ID returns the session_id assigned by mgmtd.
func (s *Session) ID() uint64

// Close sends SESSION_REQ with refer_id=sessionID to delete the session.
func (s *Session) Close(ctx context.Context) error
```

Wire encoding for SESSION_REQ (create):

- `MsgHeader.Code = CodeSessionReq`
- `MsgHeader.ReferID = 0` (create)
- `MsgHeader.ReqID = <client_id>` (arbitrary uint64, use 1)
- `SessionReqFixed.NotifyFormat = FormatJSON`
- Variable data: `clientName + "\x00"`

On SESSION_REPLY: store `reply.ReferID` as `sessionID`. Verify `reply.Created == 1`.

**Success criteria**:

- `TestIntegrationSessionCreate`: `New` returns a `*Session` with a non-zero `ID()`.
- `TestIntegrationSessionClose`: `Close` on a valid session returns nil error.
- `TestIntegrationSessionTwoSessions`: two independent sessions created sequentially both get distinct non-zero IDs.
- `TestIntegrationSessionCloseIdempotent`: closing an already-closed session returns an error (not a panic).
- `TestIntegrationSessionContextCancel`: cancelling the context during `New` returns `context.Canceled` within 1s.

---

### Step 8 — Public operations API (`client.go`)

**Task**: Implement the six operations the MCP layer will call. All operations are synchronous from the caller's perspective (block on context, return error or result).

```go
type Client struct { /* unexported */ }

func NewClient(sess *Session, d *Dispatcher, conn *Conn) *Client

// GetData queries config or state data for an xpath.
// flags: GET_DATA_FLAG_CONFIG (0x02), GET_DATA_FLAG_STATE (0x01), or both.
// datastore: DatastoreRunning, DatastoreCandidate, DatastoreOperational.
// Returns JSON bytes (assembles multi-fragment TREE_DATA internally).
func (c *Client) GetData(ctx context.Context, xpath string, datastore, flags uint8) ([]byte, error)

// Lock acquires an exclusive lock on a datastore.
func (c *Client) Lock(ctx context.Context, datastore uint8) error

// Unlock releases a previously acquired lock.
func (c *Client) Unlock(ctx context.Context, datastore uint8) error

// Edit stages a config change in the candidate datastore.
// op: EditOpCreate, EditOpMerge, EditOpReplace, EditOpDelete, EditOpRemove.
// data: JSON-encoded YANG tree for the node at xpath (nil for delete ops).
func (c *Client) Edit(ctx context.Context, xpath string, op uint8, data []byte) (*EditResult, error)

type EditResult struct {
    Changed bool
    Created bool
    XPath   string  // canonical xpath of created node
}

// Commit applies the candidate to running. action: CommitApply, CommitValidate, CommitAbort.
// If unlock is true, mgmtd releases the candidate lock on success.
func (c *Client) Commit(ctx context.Context, action uint8, unlock bool) error

// EditAndCommit is Lock → Edit → Commit(CommitApply, unlock=true).
// The lock is released even if Edit or Commit fails.
func (c *Client) EditAndCommit(ctx context.Context, xpath string, op uint8, data []byte) (*EditResult, error)

// Subscribe registers notification selectors. Subsequent NOTIFY frames
// are delivered on the returned channel. replace=true clears prior selectors.
func (c *Client) Subscribe(ctx context.Context, xpaths []string, replace bool) (<-chan Notification, error)

type Notification struct {
    Op         uint8  // NOTIFY_OP_*
    XPath      string
    Data       []byte // JSON
}

// RPC executes a YANG RPC or action.
// input: JSON-encoded `input` container (may be nil).
func (c *Client) RPC(ctx context.Context, xpath string, input []byte) ([]byte, error)
```

**Success criteria**:

- `TestIntegrationGetDataRunningConfig`: `GetData("/", DatastoreRunning, FlagConfig)` returns bytes that unmarshal as valid JSON with no error.
- `TestIntegrationGetDataOperState`: `GetData("/frr-staticd:lib", DatastoreOperational, FlagState)` returns valid JSON containing the `10.99.0.0/24` static route from `frr.conf`.
- `TestIntegrationGetDataUnknownXpath`: a syntactically invalid xpath returns an error (not a hang).
- `TestIntegrationEditAndCommit`: add a static route via `EditAndCommit`, then `GetData` confirms it is present.
- `TestIntegrationEditAndCommitDelete`: delete the route added above, then `GetData` confirms it is absent.
- `TestIntegrationLockConflict`: two clients both lock the same datastore — the second `Lock` call returns an error.
- `TestIntegrationCommitValidate`: `Commit(CommitValidate)` with a valid candidate succeeds; running config is unchanged.
- `TestIntegrationSubscribeNotify`: `Subscribe` then `EditAndCommit` a route change; the notification channel receives a `NOTIFY_OP_DS_PATCH` or `NOTIFY_OP_NOTIFICATION` within 5s.
- `TestIntegrationContextCancelUnblocks`: cancelling the context during a `GetData` in flight returns within 500ms.

---

### Step 9 — End-to-end integration test (`client_e2e_test.go`)

**Task**: One test that exercises the complete client lifecycle as a realistic sequence, mirroring what the MCP layer will do. Run via `make test-integration`.

Sequence:

1. `Dial` the socket.
2. `NewDispatcher`.
3. `New` session named `"routing-mcp-test"`.
4. `GetData("/", Running, FlagConfig)` — print byte count.
5. `GetData("/frr-staticd:lib", Operational, FlagState)` — assert `10.99.0.0/24` present.
6. `Subscribe(["/frr-staticd:lib"], replace=true)`.
7. `EditAndCommit` to add `10.99.1.0/24 Null0`.
8. Assert notification arrives on subscription channel within 5s.
9. `GetData` confirms `10.99.1.0/24` is now present.
10. `EditAndCommit` with `EditOpDelete` to remove `10.99.1.0/24`.
11. `GetData` confirms it is absent.
12. `Close` session.

**Success criteria**:

- `TestIntegrationE2E` passes with `-timeout 30s` and zero test failures.
- `go test -v` output shows each step as a named sub-test (`t.Run`).
- The test is idempotent: run twice in a row without `make down`, both pass.

---

### Step 10 — MCP layer (`pkg/mcp/`)

**Task**: Wrap the `Client` operations as MCP tools using an MCP server library. Expose the FRR YANG schemas as MCP resources.

Tools:

| Tool name | Client call | Input schema |
| --- | --- | --- |
| `get_config` | `GetData(xpath, Running, FlagConfig)` | `{xpath: string}` |
| `get_state` | `GetData(xpath, Operational, FlagState)` | `{xpath: string}` |
| `set_config` | `EditAndCommit(xpath, Merge, data)` | `{xpath: string, data: object}` |
| `delete_config` | `EditAndCommit(xpath, Delete, nil)` | `{xpath: string}` |
| `validate_config` | `Lock → Edit → Commit(Validate) → Unlock` | `{xpath: string, data: object}` |
| `get_notifications` | drain `Subscribe` channel (poll) | `{xpaths: []string, max: int}` |
| `run_rpc` | `RPC(xpath, input)` | `{xpath: string, input: object}` |

Resources:

- `frr://yang/{module}` → contents of `frr/yang/{module}.yang` served as text.
- `frr://yang/index` → list of available YANG modules.

**Success criteria**:

- MCP server starts (`cmd/routing-mcp/main.go`) and the tool list JSON contains all 7 tool names.
- `get_config` called with xpath `/` returns a non-empty JSON string.
- `set_config` called with a new static route, followed by `get_config`, shows the route.
- `get_notifications` called after a config change returns at least one notification object.
- `frr://yang/index` resource lists at least the `frr-staticd` and `frr-ripd` modules.
- All tools return a structured error (not a panic) when mgmtd is unreachable.

---

## Decision Point Tracker

| # | Question | Decision | Rationale |
| --- | --- | --- | --- |
| 0 | Byte order | Verify in `mgmt_msg.c` before writing decoder | Only ~20 lines to check; wrong byte order means silent garbled data |
| 2 | Max frame size cap | 256 KB | 4× the stated 64 KB max; protects against corrupted length fields |
| 5 | `Send` when disconnected | Return `ErrNotConnected` immediately | Session layer knows to reconnect; blocking would deadlock callers |
| 5 | Auto-reconnect + re-session | Reconnect transparent; re-session is caller responsibility | Session IDs are invalidated on disconnect; caller must re-subscribe too |
| 6 | Streaming TREE_DATA assembly | Buffer internally, return single `[]byte` | Simpler API; 64 KB max means full result fits in memory |
| 8 | `EditAndCommit` convenience | Yes, keep primitives too | MCP tools need atomicity; direct callers may need finer control |
| 9 | Test environment | `frrouting/frr:10.2` via Docker Compose | Building from source takes 20+ min; 10.2 protocol is compatible |
| 10 | MCP notification delivery | Polling `get_notifications` tool | MCP has no standard push; revisit if SSE support lands in the MCP SDK |
