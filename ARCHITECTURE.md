# ARCHITECTURE.md

routing-mcp is a Go MCP (Model Context Protocol) server that exposes FRRouting configuration and operational state to AI agents. It connects to FRR's `mgmtd` daemon over a Unix socket using the native binary protocol, translates those operations into MCP tools, and serves them over stdio or HTTP/SSE.

---

## Package layout

```text
pkg/frrmgmt/
  frame.go      Wire frame encode/decode (outer envelope)
  msg.go        All message type constants and fixed-size structs
  encode.go     Variable-data helpers (xpath+data encoding, NUL strings)
  errors.go     Sentinel errors
  conn.go       Unix socket connection with reconnect loop
  dispatch.go   ReqID → pending-channel routing; NOTIFY fan-out
  session.go    SESSION_REQ/REPLY handshake
  client.go     Public API: GetData, Lock, Edit, Commit, EditAndCommit, Subscribe, RPC

pkg/mcp/
  server.go     MCP tool and resource handlers wrapping frrmgmt.Client

cmd/routing-mcp/
  main.go       Binary: flags, wiring, stdio/HTTP transport selection
```

No CGo. No dependency on libfrr or libyang. The protocol is implemented purely in Go using `encoding/binary`.

---

## Layer architecture

```text
┌─────────────────────────────────────────┐
│              MCP transport              │  stdio or HTTP/SSE (mcp-go)
├─────────────────────────────────────────┤
│           pkg/mcp/server.go             │  7 tools, 2 resource types
├─────────────────────────────────────────┤
│           frrmgmt.Client                │  synchronous operation API
├─────────────────────────────────────────┤
│   Session    │    Dispatcher            │  handshake │ req→reply routing
├─────────────────────────────────────────┤
│              frrmgmt.Conn               │  Unix socket + reconnect
├─────────────────────────────────────────┤
│    frame.go / msg.go / encode.go        │  wire encoding (pure functions)
└─────────────────────────────────────────┘
              ↕  Unix socket
         mgmtd_fe.sock  (FRR mgmtd)
```

---

## Wire protocol

### Outer frame (`frame.go`)

Every message is wrapped in an 8-byte envelope:

```text
[marker uint32][total_len uint32][payload: total_len-8 bytes]
```

`marker = 0x23232301` — the low byte is the protocol version (1 = native). All fields are **little-endian** (confirmed in `frr/lib/mgmt_msg.c:357-366`; structs are written with `memcpy`, not `htonl`). `total_len` includes the 8-byte header itself, matching the FRR convention. The cap on incoming payload is 256 KB (4× the documented 64 KB limit) to bound memory use from corrupt frames.

### Message structure (`msg.go`)

Every payload starts with a 24-byte `MsgHeader`:

```go
type MsgHeader struct {
    Code    uint16  // message type
    Resv    uint16  // always 0
    VSplit  uint32  // split point in variable data (see below)
    ReferID uint64  // session_id on all FE→BE messages
    ReqID   uint64  // correlates request to reply
}
```

Every fixed-size message struct is exactly **32 bytes**: 24-byte header + 8 bytes of type-specific fields (plus padding). This mirrors the C `_Static_assert(sizeof(msg) == offsetof(msg, variable_field))` in `mgmt_msg_native.h`. Variable data is appended after the fixed struct, never embedded in it.

### Variable-data patterns (`encode.go`)

Three patterns cover all public message types:

| Pattern | Usage | Encoding |
| --- | --- | --- |
| NUL-terminated string | `GET_DATA` xpath, `SESSION_REQ` client name | `str + "\x00"` |
| xpath split | `EDIT`, `NOTIFY`, `EDIT_REPLY` | `xpath\x00 + data`; `VSplit = len(xpath)+1` |
| NUL-separated list | `NOTIFY_SELECT` selectors | `str1\x00str2\x00...` |

The `VSplit` field in `MsgHeader` encodes where the xpath ends and the secondary data begins, allowing a single `[]byte` to carry both without an intermediate allocation.

---

## Connection layer (`conn.go`)

`Conn` wraps a Unix socket with an automatic reconnect loop. `Dial` returns immediately and connects in the background; callers poll `IsConnected()` or wait for the first frame.

**Reconnect**: exponential backoff from 100ms to 5s, capped, with ±10% jitter. On reconnect the `Frames()` channel keeps delivering without interruption. The `Session` and `Dispatcher` above are *not* reset automatically — the caller is responsible for detecting the disconnect (via a failed `Send` returning `ErrNotConnected`) and re-establishing the session.

**Concurrency**: two mutexes on `Conn`. `connMu` guards the `sock` pointer for concurrent `Send` and reconnect. `writeMu` serialises concurrent `Send` calls so frames are not interleaved.

`Send` returns `ErrNotConnected` immediately when `sock == nil` rather than blocking. Blocking would deadlock callers that hold context-linked goroutines. The session layer above handles the error.

---

## Dispatcher (`dispatch.go`)

The dispatcher is the concurrency hub. It runs a single `dispatchLoop` goroutine reading from `Conn.Frames()` and routes each payload by inspecting the first two bytes (`Code`) and bytes 16–24 (`ReqID`):

- `Code == CodeNotify` → drop into the `notifyCh` channel (buffered 64). If the buffer is full the notification is silently dropped.
- All other codes → look up `reqID` in the pending map and deliver to that request's channel.

### Pending request lifecycle

`Expect(ctx, reqID, multi)` registers a `pending` struct before the `Send` call, preventing a race where the reply arrives before the registration. The struct holds:

- `ch chan []byte` — receives incoming frames (buffered 16)
- `multi bool` — whether to expect multiple frames (TREE_DATA streaming)
- `done chan struct{}` — closed when the entry leaves the map

For `multi=true` (used only by `GetData`), the dispatcher reads the `More` flag at offset 26 inside `TreeDataFixed`. Frames with `more=1` are forwarded and the entry is kept in the map; the final `more=0` frame closes both `ch` and `done` and removes the entry. For `multi=false`, a single frame closes both channels immediately.

`Cancel(reqID)` removes the entry under the mutex and closes `done`. The watching goroutine (started inside `Expect`) calls `Cancel` when `ctx` is cancelled, so the calling goroutine's `select` will also see `<-ctx.Done()`. Only `dispatchLoop` ever closes `ch`; `Cancel` never does. This prevents double-close panics when a frame arrives after cancellation.

---

## Session (`session.go`)

The handshake is a single request/reply pair:

1. Send `SESSION_REQ` with `ReferID=0` (create) and `NotifyFormat=FormatJSON`.
2. `Expect` is called **before** `Send` to prevent the race where the reply arrives before the channel is registered.
3. `SESSION_REPLY` carries the assigned `session_id` in `ReferID`. All subsequent messages set `MsgHeader.ReferID = session_id`.

`Close` sends `SESSION_REQ` with `ReferID=session_id` (non-zero = delete) as fire-and-forget — no reply channel is registered because the socket close that follows will cause mgmtd to reap the session regardless.

Data format is locked to `FormatJSON` at session creation. There is no per-request format negotiation.

---

## Client (`client.go`)

`Client` holds a `*Session`, `*Dispatcher`, and `*Conn` and exposes eight synchronous methods. All block on `context.Context` and return typed errors.

### GetData

The only method using `multi=true` dispatch. It sends `GET_DATA` and loops on the pending channel, accumulating JSON bytes from `TREE_DATA` frames until the channel closes (`more=0` or ctx cancelled). Multi-fragment assembly is transparent to callers.

### EditAndCommit

Atomic config change: `Lock(Candidate)` → `Edit(Candidate)` → `Commit(Apply, unlock=true)`. If `Edit` fails, `Unlock` is attempted (error ignored) before returning. The `Commit(unlock=true)` path releases the lock inside mgmtd atomically with the apply, avoiding a separate unlock round-trip on the success path.

`Lock`, `Edit`, and `Commit` are also exposed individually for callers that need finer control (e.g. `validate_config`).

### Subscribe

`Subscribe` sends `NOTIFY_SELECT` (fire-and-forget, no reply expected) and starts a goroutine that forwards `Notification` structs from `Dispatcher.Notifications()` to the returned channel. The goroutine exits when `ctx` is cancelled, closing the caller's channel. Multiple `Subscribe` calls share the same underlying `notifyCh` from the dispatcher — every subscriber sees every notification.

### checkError / roundtrip

`checkError` inspects the first two bytes of any reply for `CodeError` (0) and returns a descriptive error including the error code and optional text message from the variable payload. `roundtrip` packages the common `Expect → Send → select` pattern for single-reply operations.

---

## MCP server (`pkg/mcp/server.go`)

`Server` wraps `*frrmgmt.Client` and registers 7 tools and up to 2 resource types with the `mark3labs/mcp-go` library.

### Tools

| Tool | frrmgmt call | Notes |
| --- | --- | --- |
| `get_config` | `GetData(xpath, Running, FlagConfig)` | |
| `get_state` | `GetData(xpath, Operational, FlagState)` | |
| `set_config` | `EditAndCommit(xpath, Merge, data)` | |
| `delete_config` | `EditAndCommit(xpath, Delete, nil)` | |
| `validate_config` | `Lock → Edit → Commit(Validate) → defer Commit(Abort, unlock)` | lock is always released |
| `get_notifications` | drain internal buffer | polling |
| `run_rpc` | `RPC(xpath, input)` | |

### Notification buffering

`NewServer` calls `Subscribe(ctx, ["/"], replace=true)` at startup, subscribing to all paths. The background `collectNotifications` goroutine reads from the returned channel and appends to `notifBuf` (capacity 1000, oldest-first eviction). `get_notifications` drains up to `max_count` entries atomically under a mutex. This polling model matches the MCP spec (no standard push mechanism) and decouples mgmtd notification timing from MCP client polling cadence.

### validate_config lock safety

The `defer s.client.Commit(context.Background(), CommitAbort, true)` is registered *after* `Lock` succeeds. If `Lock` fails, there is nothing to clean up. If `Lock` succeeds but `Edit` or the validate `Commit` fails, the deferred abort always runs, releasing the lock. The separate background context prevents a cancelled request context from blocking the cleanup.

### YANG resources

The `frr://yang/index` and `frr://yang/{module}` resources are only registered when `--yang-dir` points to an accessible directory. `handleYangModule` rejects module names containing `/` or `\` before constructing the file path.

---

## Concurrency model summary

| Goroutine | Owner | Lifetime |
| --- | --- | --- |
| `connectLoop` | `Conn` | until `Conn.Close()` |
| `readLoop` | `Conn` (per connection) | until socket error |
| `dispatchLoop` | `Dispatcher` | until `Conn.Frames()` closes |
| ctx-cancel watcher | `Dispatcher.Expect` (per request) | until `p.done` closes |
| `collectNotifications` | `Server` | until subscription ctx cancels |
| per-`Subscribe` fan-out | `Client.Subscribe` (per caller) | until caller ctx cancels |

All shared state is protected by mutexes or channels. `Conn.framesCh` is the single delivery channel from `readLoop` to `dispatchLoop`; buffered at 64 to absorb brief bursts without blocking the read loop.

---

## FRR daemon scope

Not all FRR daemons are mgmtd-aware. This client can only manage daemons that have been converted to the mgmtd backend model:

- **Full config + state**: `zebra`, `staticd`, `ripd`, `ripngd`, and several `lib/` modules (routemap, filter, interface, VRF, keychain)
- **State / limited config**: `bfdd`, `pathd`, `pbrd`, `pimd`
- **Not supported** (old vtysh model only): `bgpd`, `ospfd`, `isisd`, `ldpd`

The test container enables `mgmtd`, `zebra`, `staticd`, and `ripd`. Integration tests use `10.99.0.0/24 Null0` (seeded in `docker/frr/frr.conf`) as a known-good fixture.

---

## Protocol source references

| File | Purpose |
| --- | --- |
| `frr/lib/mgmt_msg_native.h` | All public message structs and constants; `_Static_assert` lines pin exact sizes |
| `frr/lib/mgmt_msg.h` | Outer wire frame: `mgmt_msg_hdr` and `MGMT_MSG_MARKER_*` |
| `frr/lib/mgmt_msg.c` | Confirmed byte order (little-endian via `memcpy`, lines 357-366) |
| `frr/lib/mgmt_fe_client.c` | Reference C client implementation |
| `frr/doc/developer/mgmtd-dev.rst` | Architecture narrative and datastore semantics |
