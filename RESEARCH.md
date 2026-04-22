# Research: Exposing IP Routing Protocol State to AI Agents

## Context

This document captures prior art and standards work relevant to building an MCP server that exposes IP routing protocol configuration and state to AI agents.

---

## Existing MCP Servers

### gNMIBuddy (jillesca / Cisco)

- Retrieves structured network state via gNMI + OpenConfig YANG models
- Exposes routing tables, interface state, MPLS, topology queries
- Architecture: collectors → processors → MCP layer, producing LLM-friendly output
- Targets vendor hardware (Cisco, Juniper, Nokia, Arista); no open-source daemon support
- Listed on Cisco Code Exchange and Glama MCP registry
- <https://github.com/jillesca/gNMIBuddy>

### NetClaw (automateyournetwork)

- Full autonomous network engineering agent built on Claude + MCP (44 integrations, 97 skills)
- Implements a native BGP-4 (RFC 4271) and OSPFv3 (RFC 5340) speaker, peering with real routers over GRE tunnels
- Supports OSPF LSDB queries, BGP RIB queries, route injection/withdrawal, LOCAL_PREF adjustment, OSPF cost manipulation
- More of a full agent than a composable MCP server
- <https://github.com/automateyournetwork/netclaw>

### Cisco NSO MCP Server

- Wraps Cisco NSO (Network Services Orchestrator) via RESTCONF
- Exposes multi-vendor network configurations including BGP and OSPF as MCP tools
- Two implementations: official (NSO-developer) and community (dbono711)
- <https://github.com/NSO-developer/cisco-nso-mcp-server>

### Itential MCP Server

- Commercial platform with 56 production-ready MCP servers across 11 infrastructure categories
- Covers NETCONF, RESTCONF, SSH/CLI; includes BGP troubleshooting workflows
- <https://github.com/itential/itential-mcp>

### Ze (ExaBGP author)

- Ground-up Go rewrite combining a programmable network stack (BGP, FIB management) with a built-in MCP server
- Very early-stage but architecturally interesting as an integrated approach
- Referenced via ExaBGP GitHub wiki

---

## Key Interfaces and Data Models

### gNMI + OpenConfig YANG (best for real-time state from vendor hardware)

- gNMI (gRPC Network Management Interface) supports Get, Set, Subscribe RPCs against YANG paths
- Provides structured, vendor-normalized routing data at scale
- Supported by Cisco (IOS-XR, NX-OS), Juniper (Junos), Nokia (SR Linux, SR OS), Arista (EOS)
- OpenConfig BGP YANG: <https://github.com/openconfig/public/blob/master/release/models/bgp/openconfig-bgp.yang>
- gNMI spec: <https://openconfig.net/docs/gnmi/gnmi-specification/>

### YANG Models (dominant machine-readable routing data model)

- RFC 9129: YANG Data Model for OSPF
- RFC 9067: YANG Data Model for Routing Policy (BGP, OSPF, IS-IS)
- RFC 9552: BGP-LS (BGP Link State distribution)
- OpenConfig maintains vendor-neutral YANG models for BGP, routing policy, IS-IS

### BGP-LS (RFC 7752 / RFC 9552) — topology exposure to controllers

- Redistributes IGP topology (OSPF/IS-IS link state, TE attributes) northbound via BGP
- Gives a controller a full network graph view without running the IGP itself
- RFC 9815 (July 2025) adds BGP-LS SPF routing
- Recommended by practitioners for AI agents needing topology data (read-only, non-intrusive)

### BMP — BGP Monitoring Protocol (RFC 7854)

- Passive monitoring feed of all BGP sessions on a router (all RIBs, not just active routes)
- OpenBMP: main open-source collector, stores data in PostgreSQL, streams via Kafka
- Good for analytics agents; no BMP → MCP bridge currently exists

### NETCONF / RESTCONF

- NETCONF (RFC 6241) and HTTP variant RESTCONF (RFC 8040): YANG-based config and state access
- Mature, widely deployed; used by most existing MCP servers (NSO, Itential)
- Better for config management than high-frequency state streaming

### GoBGP gRPC API

- Cleanest programmatic BGP interface for open-source BGP
- Full gRPC API with protobuf, rich route management
- Best API for agent integration in the open-source space
- <https://github.com/osrg/gobgp>

### FRRouting (FRR)

- Most feature-complete open-source routing stack (BGP, OSPF, IS-IS, MPLS, BFD, PBR)
- API: VTYSH CLI with JSON output; internal Northbound C API with YANG models
- No native gRPC; gNMI transport is partial
- JSON output from `show` commands is parseable but not streaming

---

## Academic and Industry Work

### Confucius (Meta, SIGCOMM 2025)

- Multi-agent LLM framework for intent-driven network management at hyperscaler scale
- Models workflows as DAGs with specialized agents per subtask
- Integrates RAG for large context (routing tables, IP address space)
- Interfaces with relational databases containing BGP configurations
- Enforces human approval for sensitive changes
- Most credible large-scale production deployment of LLM-driven network automation
- <https://dl.acm.org/doi/10.1145/3718958.3750537>

### NetKeeper (USENIX ATC 2025)

- Autonomous network configuration update system (BGP, OSPF, link attributes)
- LLM translates operator intent into a DSL driving three specialized agents
- 99.6% policy consistency across dynamic network configurations
- <https://www.usenix.org/conference/atc25/presentation/wan>

### NetConfEval (CoNEXT 2024)

- Benchmarks LLMs on OSPF, BGP, RIP configuration tasks using Kathará emulator
- Key finding: decomposing tasks (separate conflict detection from config translation) improves accuracy
- <https://dejankostic.com/documents/publications/netconfeval-conext24.pdf>

### PeeringLLM-Bench (AIEC 2025)

- Benchmark for BGP configuration generation from natural language policy descriptions
- Covers multi-peer topologies, policy-driven routing, vendor-specific syntax
- <https://dl.acm.org/doi/full/10.1145/3763400.3763451>

---

## IETF Drafts

### draft-zeng-opsawg-applicability-mcp-a2a-00 — "When NETCONF Is Not Enough"

Argues NETCONF has five fundamental architectural gaps (not implementation bugs):

| Gap | Root cause | Proposed fix |
| --- | --- | --- |
| AI intent translation | XML rigidity, no function catalog | MCP (`/tools/list`, JSON-Schema) |
| Rapid DevOps iteration | Slow YANG revision cycles | MCP hot-swappable tool registration |
| Cross-domain long workflows | Single-RPC model, no task state | A2A persistent Task state machine |
| Multi-agent consensus | No agent coordination primitives | A2A AgentCards + consensus scoring |
| Large artifact delivery | No streaming/blob support | A2A Artifact URLs |

MCP scenarios: natural-language intent translation and hot-swap tool registration without firmware updates.

A2A scenarios: a 5-city MTU migration with `pending → working → completed/failed/cancelled` task lifecycle and human-in-the-loop approval gates. A DDoS response example shows autonomous multi-agent consensus via policy scoring.

Security section and coexistence model (Sections 5.1–5.5) are marked TBD — early-stage draft.

<https://www.ietf.org/archive/id/draft-zeng-opsawg-applicability-mcp-a2a-00.txt>

---

### draft-yang-nmrg-mcp-nm-02 — "MCP for Network Management"

Proposes MCP as a bridging/adapter layer between AI agents and existing network management interfaces (NETCONF, gNMI, vendor CLIs).

Four deployment patterns:

1. **Device-to-device**: MCP client/server on separate devices for protocol troubleshooting
2. **Controller pulls external data**: Controller's MCP client queries third-party systems (threat feeds, inventory)
3. **Standalone MCP server as adapter**: Translates MCP tool calls into NETCONF/gNMI — the most common pattern
4. **Gateway manages devices**: MCP client on a gateway/controller coordinates multiple network elements

Key ideas: tool encapsulation (wrap vendor commands into standardized MCP tools), intent translation via LLM, closed-loop automation.

Security concerns: prompt injection, tool poisoning/shadowing, missing authentication/authorization in MCP itself, context window exhaustion with many connections, stateful SSE complicating REST integration.

Open issues: device crashes mid-execution, timeout handling, human verification of irreversible operations.

[https://www.ietf.org/archive/id/draft-yang-nmrg-mcp-nm-02.txt](https://www.ietf.org/archive/id/draft-yang-nmrg-mcp-nm-02.txt)

---

### draft-yang-nmrg-a2a-nm-02 — "A2A Protocol for Network Management"

Proposes A2A protocol applied to a three-tier hierarchical agent architecture:

- **Service layer**: Service AI agents coordinate high-level user intent
- **Network layer**: Domain controllers (BGP domain, QoS domain, etc.)
- **Element layer**: Device-level agents for local execution

Key innovation: embedding **YANG-structured data inside A2A messages** to eliminate ambiguity. Natural language intent can be misinterpreted and cause outages; YANG provides machine-interpretable payloads aligned with NETCONF/RESTCONF pipelines.

Event-driven extension: agents subscribe to pub/sub topics (e.g., Kafka) for real-time fault response, supplementing A2A's default request-response model.

Concerns: A2A's HTTPS/TLS deemed inadequate for high-speed agent exchanges at scale; agent lifecycle management, idempotency, and cross-domain coordination unresolved.

[https://www.ietf.org/archive/id/draft-yang-nmrg-a2a-nm-02.txt](https://www.ietf.org/archive/id/draft-yang-nmrg-a2a-nm-02.txt)

---

## Emerging Architecture Consensus

These three IETF drafts (and the broader literature) converge on a layered model:

| Layer | Protocol | Role |
| --- | --- | --- |
| Tool discovery & invocation | MCP | AI agent ↔ network APIs |
| Agent coordination | A2A | Agent ↔ agent, long-running workflows, human approval |
| Structured data | YANG | Unambiguous payloads inside MCP/A2A messages |
| Config/state transport | NETCONF / gNMI | Unchanged underlying protocols |

---

## Gaps — What routing-mcp Could Fill

1. **No unified MCP server for open-source routing daemons** — gNMIBuddy targets vendor hardware; NetClaw is a full agent, not a composable server. A clean MCP server over FRR JSON / GoBGP gRPC does not exist.

2. **Shallow protocol coverage** — existing tools focus on BGP. OSPF LSDB, IS-IS, MPLS label tables, and segment routing state are underserved.

3. **Principled read-write tools** — typed MCP tools (`announce_prefix`, `adjust_ospf_cost`, `withdraw_route`) mapped directly to routing daemon APIs don't exist as a composable server.

4. **No BMP → MCP bridge** — no tool exposes OpenBMP's streaming BGP data as subscribable MCP resources.

5. **IETF alignment opportunity** — OPSAWG drafts propose routers themselves becoming MCP servers. Designing routing-mcp against that architecture now positions it well.
