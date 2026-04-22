# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

A standalone Go MCP (Model Context Protocol) server that exposes FRR (Free Range Routing) configuration and operational state to AI agents. It connects to FRR's `mgmtd` daemon over a Unix socket using the native binary protocol, wraps those operations as MCP tools, and serves them to any MCP-compatible client.

The `frr/` subdirectory is a **read-only reference clone** of FRR 10.7.0-dev. Do not modify files under `frr/`. Read them to understand the protocol.

## Build and test commands

```bash
# Unit tests — pure Go, no FRR required, runs natively on macOS
go test ./... -run 'Unit|unit' -v

# Start FRR container (required for integration tests)
make up

# Integration tests — run inside Docker, require FRR socket
make test-integration

# Run a single integration test
make run-test T=TestIntegrationSessionCreate

# Both unit and integration
make test

# Interactive shell inside the test container
make shell

# Tail FRR daemon logs
make logs

# Stop and remove all containers and the socket volume
make down
```

Integration test files use the build tag `//go:build integration` and the `FRR_SOCK` environment variable for the socket path. Unit tests have no build tag and no external dependencies.

## Protocol reference — the files that matter

All protocol implementation decisions derive from these FRR source files:

| File | Purpose |
| --- | --- |
| `frr/lib/mgmt_msg_native.h` | **Primary spec.** All PUBLIC message structs, constants, and encoding rules. The `_Static_assert` lines pin exact struct sizes. |
| `frr/lib/mgmt_msg.h` | Outer wire frame: `struct mgmt_msg_hdr { uint32_t marker; uint32_t len; }` and the `MGMT_MSG_MARKER_*` constants. |
| `frr/lib/mgmt_defines.h` | Socket paths (`mgmtd_fe.sock`), max lengths, datastore enum. |
| `frr/lib/mgmt_fe_client.c` | Reference C client — the implementation we are porting to Go. |
| `frr/doc/developer/mgmtd-dev.rst` | Architecture narrative, datastore semantics, message flow diagrams, conversion status per daemon. |
| `frr/yang/*.yang` | YANG data models; the xpaths used in Get/Edit calls must be valid against these. |
| `frr/grpc/frr-northbound.proto` | gRPC interface — not what we implement, but shows the same operations in a more readable schema. |

## Wire protocol summary

**Outer frame** (8 bytes before every message):

```text
[marker uint32][len uint32]
```

`marker = 0x23232300 | 1` (version byte 1 = native protocol). `len` = payload byte count. Byte order: verify in `frr/lib/mgmt_msg.c` before assuming — expected to be little-endian (host byte order, Unix socket between local processes).

**Message header** (first 24 bytes of every payload, matches C `struct mgmt_msg_header`):

```go
type MsgHeader struct {
    Code    uint16  // MGMT_MSG_CODE_*
    Resv    uint16  // always 0
    VSplit  uint32  // split point in variable data (xpath NUL-len for xpath+data messages)
    ReferID uint64  // session_id on all FE messages
    ReqID   uint64  // correlates request → reply
}
```

All fixed message structs are exactly 32 bytes (header + 8 bytes of fields + padding). The C `_Static_assert(sizeof(msg) == offsetof(msg, variable_field))` enforces this.

**Variable data patterns** used in PUBLIC messages:

- Single NUL-terminated string: `xpath + "\x00"` (GET_DATA, SESSION_REQ client_name)
- xpath split: `xpath\x00 + treeData`, with `VSplit = len(xpath)+1` (EDIT, NOTIFY, EDIT_REPLY)
- NUL-separated list: `str1\x00str2\x00` (NOTIFY_SELECT selectors)

**Session lifecycle**: `SESSION_REQ` (refer_id=0) → receive `SESSION_REPLY` (refer_id = assigned session_id) → use session_id as ReferID on all subsequent messages → `SESSION_REQ` (refer_id=session_id) to close.

**Config change flow**: `LOCK(candidate)` → `EDIT(candidate)` → `COMMIT(validate then apply)` → lock released. `unlock=1` in the COMMIT message releases the lock automatically.

## Go package architecture

```text
pkg/
  frrmgmt/
    frame.go      ReadFrame / WriteFrame — outer marker+length envelope
    msg.go        All PUBLIC message type constants and fixed-field structs
    encode.go     Variable-data helpers: EncodeXpathData, DecodeNulStrings, etc.
    conn.go       Unix socket dial, reconnect with exponential backoff, read loop
    dispatch.go   req_id → pending reply routing; NOTIFY fan-out channel
    session.go    SESSION_REQ/REPLY handshake; owns session_id
    client.go     Public API: GetData, Lock, Unlock, Edit, Commit, EditAndCommit, Subscribe, RPC
pkg/
  mcp/            MCP tool wrappers over client.go operations
cmd/
  routing-mcp/
    main.go
```

No CGo. No dependency on libfrr or libyang. The framing and struct layouts are implemented directly in Go using `encoding/binary`.

## Daemon conversion status (scope constraint)

Not all FRR daemons are mgmtd-aware. Only converted daemons can be managed through this client:

- **Fully converted** (config + state): `zebra`, `staticd`, `ripd`, `ripngd`, and several `lib/` modules (routemap, filter, interface, VRF, keychain)
- **Backend only** (state, limited config): `bfdd`, `pathd`, `pbrd`, `pimd`
- **Not converted** (old vtysh model only): `bgpd`, `ospfd`, `isisd`, `ldpd` — the major routing protocols

The test FRR container enables `mgmtd`, `zebra`, `staticd`, and `ripd`. Integration tests use the static route `10.99.0.0/24 Null0` seeded in `docker/frr/frr.conf` as a known-good fixture.

## Key design decisions

See `PLAN.md` for the full rationale. The short version:

- Data format: always JSON (`FormatJSON = 2`) — no libyang dependency
- Streaming `TREE_DATA` (more=1 fragments): assembled internally before returning to callers
- MCP notification delivery: polling `get_notifications` tool (MCP has no standard push)
- `EditAndCommit` is a convenience wrapper that does Lock → Edit → Commit(unlock=true) atomically

## Reference documents in this repo

- `RESEARCH.md` — prior art survey: existing MCP servers for network management, relevant IETF drafts (MCP for network management, A2A for network management), interfaces (gNMI, BGP-LS, BMP, GoBGP gRPC), academic work
- `PLAN.md` — step-by-step implementation plan with testable success criteria per step and the full container setup specification
