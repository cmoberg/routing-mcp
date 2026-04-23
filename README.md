# routing-mcp

An MCP (Model Context Protocol) server that exposes [FRRouting](https://frrouting.org/) configuration and operational state to AI agents. It connects to FRR's `mgmtd` daemon over a Unix socket and surfaces routing data as MCP tools.

## Status

Under active development.

## Requirements

- FRR 10.x with `mgmtd` enabled
- Linux x86_64 or arm64

## Quick start (Docker)

```yaml
# docker-compose.yml
services:
  frr:
    image: frrouting/frr:10.2
    privileged: true
    volumes:
      - frr-run:/run/frr

  routing-mcp:
    image: ghcr.io/cmoberg/routing-mcp:latest
    volumes:
      - frr-run:/run/frr
    ports:
      - "3000:3000"

volumes:
  frr-run:
```

## Install (bare metal)

```bash
curl -L https://github.com/cmoberg/routing-mcp/releases/latest/download/routing-mcp_linux_amd64.tar.gz | tar xz
sudo install routing-mcp /usr/local/bin/

# Run alongside FRR
routing-mcp --socket /run/frr/mgmtd_fe.sock --transport http --port 3000
```

## MCP tools

| Tool | Description |
| --- | --- |
| `get_config` | Read configuration from the running datastore |
| `get_state` | Read operational state |
| `set_config` | Apply a configuration change (edit + commit) |
| `delete_config` | Remove a configuration node |
| `validate_config` | Validate a change without applying it |
| `get_notifications` | Poll for datastore change notifications |
| `run_rpc` | Execute a YANG RPC |

All tools accept an `xpath` parameter using standard YANG XPath syntax. Data is returned as JSON using FRR's YANG data models (`frr/yang/*.yang`).

## Supported daemons

Only daemons converted to the mgmtd northbound API are accessible:

- **Full support**: `zebra`, `staticd`, `ripd`, `ripngd`
- **State only**: `bfdd`, `pathd`, `pbrd`, `pimd`
- **Not yet supported**: `bgpd`, `ospfd`, `isisd`, `ldpd`

## Development

```bash
# Unit tests (no FRR required)
make test-unit

# Integration tests (starts FRR in Docker)
make test-integration

# Interactive shell in test container
make shell
```

The `frr/` directory is a local reference clone of the FRR source — it is not tracked in this repo. Clone it separately if needed:

```bash
git clone https://github.com/FRRouting/frr frr
```

## Background

- [RESEARCH.md](RESEARCH.md) — prior art survey covering existing MCP servers for network management, relevant IETF drafts, and interface options
- [ARCHITECTURE.md](ARCHITECTURE.md) — current design, protocol details, and package layout

## License

MIT
