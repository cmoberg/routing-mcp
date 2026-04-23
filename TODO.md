# TODO — routing-mcp improvements

The 10-step implementation plan is complete. All integration tests pass against FRR 10.x. What follows are suggested next steps roughly ordered by impact.

## High priority

### Fix tool xpath examples for FRR 10.x

The tool descriptions in `pkg/mcp/server.go` still reference `/frr-staticd:lib`, which was the FRR 9.x path. In FRR 10.x staticd augments `frr-routing:routing`, so the canonical path is `/frr-routing:routing/control-plane-protocols/control-plane-protocol/.../frr-staticd:staticd`. Update the description strings for `get_config`, `get_state`, and `set_config` to use accurate, tested example xpaths.

### Claude Desktop / MCP client integration guide

Add a `docs/claude-desktop.md` (or a section in the README) showing how to wire routing-mcp into a Claude Desktop config. The main challenge is socket access: the `mgmtd_fe.sock` lives inside the Docker Desktop VM and is not directly reachable from the macOS host. Options:

- `socat TCP-LISTEN:... UNIX-CLIENT:/run/frr/mgmtd_fe.sock` bridge running inside the container, with the MCP binary connecting over TCP
- Run the MCP binary itself inside a container that shares the `frr-run` volume
- Use an SSH tunnel if the FRR host is a remote Linux box

### Auto-reconnect + re-session on disconnect

`Conn` already reconnects transparently, but the `Session` ID is invalidated on disconnect. The `Client` currently surfaces the broken-session error to the caller. Add a reconnect-and-rehandshake path inside `Client` so MCP tools remain usable after a transient mgmtd restart without requiring a full server restart. The re-subscription (for `get_notifications`) also needs to be replayed on re-session.

## Medium priority

### CI pipeline

Add a GitHub Actions workflow that runs `make test-integration` on every push. The `frrouting/frr:10.2` image is ~200 MB and can be cached via `docker/build-push-action` layer caching. The workflow needs Docker Compose v2 and the `frr-run` socket volume setup.

### Push-based notification delivery

Decision Point #10 chose polling because MCP had no standard push at the time. If the MCP SDK gains SSE/streaming support, replace the `get_notifications` polling tool with a resource subscription or a streaming tool response. Evaluate `github.com/mark3labs/mcp-go` for relevant new features.

### `validate_config` resilience

`handleValidateConfig` currently holds both the candidate and running locks for the duration of validation. If the validation call takes long or the client disconnects, locks are held until mgmtd detects the session is gone. Investigate whether FRR will ever expose a validate-only flag analogous to `EDIT_FLAG_IMPLICIT_COMMIT` (currently only `EDIT_FLAG_IMPLICIT_LOCK = 0x01` and `EDIT_FLAG_IMPLICIT_COMMIT = 0x02` are defined in `mgmt_msg_native.h`). In the meantime, add a short timeout inside `handleValidateConfig`.

### Structured error responses

Tool handlers currently return `mcp.NewToolResultError(err.Error())` with a flat string. Consider returning a JSON object `{"error": "...", "code": N}` so callers can distinguish mgmtd error codes (e.g. EBUSY for lock conflicts, EINVAL for bad xpath) from transport errors.

## Low priority / future

### Multi-VRF support

The current tools operate on the default VRF. FRR YANG paths include VRF context (e.g. `/frr-vrf:lib/vrf[name='mgmt']`). Add optional `vrf` parameters to the config tools, or document the VRF-aware xpaths callers should use.

### Support for additional daemons

As FRR converts more daemons to mgmtd (bgpd, ospfd, and isisd are the large ones still on the old vtysh model), update the tool descriptions and add integration test fixtures for those daemons. No code changes should be needed — the tools are xpath-agnostic.

### Authentication / access control for the HTTP transport

`ServeHTTP` currently binds with no authentication. If the SSE endpoint is exposed beyond localhost, add at minimum a shared-secret bearer token check in the HTTP handler. The stdio transport (`ServeStdio`) is inherently process-scoped and does not need this.

### Batch operations tool

Add a `batch_config` tool that accepts a list of `{xpath, op, data}` objects and applies them inside a single Lock → Edit... → Commit sequence. Useful for multi-step configuration changes that must be atomic.
