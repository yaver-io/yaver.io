# Host-Share Connectivity and Transport Robustness Report

Date: 2026-04-22

Status: implementation audit for the host-share / borrowed-runner / guest-bus path

## Purpose

This document captures:

- what Yaver host-share is trying to accomplish
- what connectivity infrastructure exists today
- what already works
- what is incomplete or fragile
- what is security-sensitive or vulnerable
- what must change before this feature can be considered robust across LAN, Tailscale, Cloudflare Tunnel, relay, and mixed VPN environments

This report is specifically about the model where:

- the **host** already has Yaver, infra, Claude/Codex/Aider, and the stronger technical knowledge
- the **guest** has only the Yaver Go agent
- the **guest agent acts as the bus**
- the **host agent does the heavy execution**
- host-side coding sessions may need to read and write guest-side repo data over Yaver transport

## Product Goal

The intended outcome is:

1. The host creates a host-share invite from Yaver.
2. The guest redeems the code with only the Go agent installed.
3. The host can lend:
   - host infra
   - host coding tools
   - or both
4. The host-side coding agent can operate while using the guest agent as a remote repo/data bus.
5. The system should work across:
   - same LAN
   - Tailscale
   - Cloudflare Tunnel
   - self-hosted relay
   - platform free relay
   - generic VPN situations where the transport path is still valid
6. The guest should not need deep networking or OS knowledge.

The key product rule is:

> the guest connects to a session, not to a transport; Yaver should choose the transport automatically and keep the session alive.

## What Exists Today

### Control plane

The host-share control plane now exists:

- invite creation, preview, join, revoke, list, sessions
- session access resolution for host side
- peer access resolution for guest side
- session activity touching / idle tracking

Relevant files:

- [backend/convex/hostShare.ts](/Users/kivanccakmak/Workspace/yaver.io/backend/convex/hostShare.ts:1)
- [backend/convex/http.ts](/Users/kivanccakmak/Workspace/yaver.io/backend/convex/http.ts:3261)
- [desktop/agent/auth.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth.go:1015)

### Host runtime admission

Host-share sessions can reach a narrow runtime surface on the host:

- `/info`
- `/agent/status`
- `/agent/runners`
- `/ws/terminal`
- borrowed workspace status endpoints

Relevant files:

- [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go:974)
- [desktop/agent/console_terminal.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/console_terminal.go:33)

### Borrowed workspace

The host now has a per-session borrowed workspace manager:

- per-session root
- per-session repo dir
- local bootstrap from a host directory
- workspace metadata persisted on disk

Relevant files:

- [desktop/agent/host_share_workspace.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_workspace.go:13)
- [desktop/agent/host_share_workspace_http.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_workspace_http.go:1)

### Guest-bus repo surface

The guest agent now exposes a narrow host-share repo bus:

- list roots
- list directories
- read files
- raw file fetch
- write text files
- create directories

Relevant files:

- [desktop/agent/files_browser.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/files_browser.go:48)
- [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go:316)

### Host-share bus workflow

The host CLI can now:

- inspect guest roots
- read guest files
- write guest files
- pull a guest root into the borrowed workspace
- push borrowed workspace text files back to the guest root

Relevant files:

- [desktop/agent/host_share_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_cmd.go:409)
- [desktop/agent/host_share_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_cmd.go:682)

### Guest-owned repo attach/sync workflow

The cleaner guest-facing borrowed-workspace flow now exists:

- `yaver host-share attach-repo --session <id> --path <repo>`
- `yaver host-share sync-repo --session <id> --to-host`
- `yaver host-share sync-repo --session <id> --from-host`

This means the guest no longer has to manually drive host-side `guest-pull` and
`guest-push` commands just to seed or refresh a borrowed workspace.

Relevant files:

- [desktop/agent/host_share_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_cmd.go:878)
- [desktop/agent/host_share_workspace_http.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_workspace_http.go:1)

### Transport readiness audit

The host-share prep flow already audits:

- same-LAN likelihood
- Tailscale connectivity
- Cloudflare Tunnel config presence
- custom relay config
- backend relay availability
- derived transport order

Relevant files:

- [desktop/agent/host_share_prepare.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_prepare.go:332)

## What Connectivity Infrastructure Exists Today

### 1. Same LAN

Yaver already knows how to advertise local reachability and local IP candidates for pairing.

Relevant files:

- [desktop/agent/auth_pair.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_pair.go:150)
- [desktop/agent/pair_url.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/pair_url.go:102)

### 2. Tailscale

Yaver can detect a locally running Tailscale and extract Tailnet IP candidates.

Relevant files:

- [desktop/agent/tailscale.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/tailscale.go:39)

### 3. Relay

Yaver has a real application-layer relay system:

- agent keeps outbound QUIC tunnel(s)
- clients can reach devices through `/d/{deviceId}/...`
- relay architecture supports direct-first and relay fallback at the platform level
- multi-relay is documented

Relevant files:

- [relay/README.md](/Users/kivanccakmak/Workspace/yaver.io/relay/README.md:1)
- [desktop/agent/agent_mesh_remote.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/agent_mesh_remote.go:16)

### 4. Cloudflare Tunnel / public HTTPS endpoints

Yaver supports configuring Cloudflare tunnel endpoints in config, has a setup
wizard, and now advertises those endpoints as `PublicEndpoints` on device
heartbeats so remote resolution can actually use them.

Relevant files:

- [desktop/agent/config.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/config.go:141)
- [desktop/agent/tunnel_cf_wizard.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/tunnel_cf_wizard.go:44)
- [desktop/agent/auth.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth.go:1300)
- [desktop/agent/agent_mesh_remote.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/agent_mesh_remote.go:334)

### 5. Generic VPN

There is no explicit generic VPN integration layer. If a VPN makes a host reachable through its advertised address, Yaver may work over it, but this is incidental rather than designed behavior.

## What Is Working

### Working today

- host-share invite/session control plane
- hard lease lifetime and idle timeout on host-share sessions
- explicit host-side session termination
- terminal-first host-share admission
- session-scoped borrowed workspace
- symmetric guest-bus auth
- host read/write to guest repo through the guest agent
- basic bus-backed pull/push workflow for text files
- guest-driven attach/sync workflow for guest-owned repos
- host readiness audit for transport classes
- public HTTPS endpoint routing, including Cloudflare-configured endpoints advertised by the host

### Working conditionally

- same-LAN access when the chosen address is truly reachable
- Tailscale access when the current host/device address selection happens to line up with the Tailnet path
- relay access when a valid relay URL is selected and reachable

## What Is Not Working Yet

### 1. Runtime transport is materially better, but still not fully session-aware

The current runtime path now does more than the older report assumed:

- builds a candidate set from direct addresses, advertised public endpoints, and relays
- orders those candidates
- probes and demotes recent failures
- caches last-good bases
- falls back across candidates inside `doRemoteAgentRequest`

Relevant files:

- [desktop/agent/agent_mesh_remote.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/agent_mesh_remote.go:234)
- [desktop/agent/agent_mesh_remote_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/agent_mesh_remote_test.go:104)

What is still missing is a more explicit session-level transport memory and
reconnect policy. The current behavior is request-level robust, but not yet a
full borrowed-session transport manager.

### 2. Cloudflare is now in the live path, but only through advertised public endpoints

Cloudflare-configured URLs now participate in runtime resolution when the host
advertises them via `PublicEndpoints`.

That means Cloudflare Tunnel is no longer “audit only.” It is usable for
host-share and remote proxying when:

- the host has a configured tunnel URL
- that URL is included in heartbeats
- the guest resolves the host device through normal device listing

The remaining limitation is that runtime selection is based on advertised public
endpoints, not on a separate dedicated Cloudflare-only resolution layer.

### 3. Same-LAN and Tailscale are first-class candidates now

Direct candidate generation already includes:

- `QuicHost`
- discovered `LocalIps`
- `.local` hostnames

That means same-LAN and Tailscale are part of the live candidate ladder rather
than merely advisory prep outputs.

The remaining fragility is prioritization and adaptation after network changes,
not total absence from runtime routing.

### 4. Relay failover exists, but robustness still depends on candidate health quality

The remote request layer does iterate through candidate bases and can fall back
to later relay/direct options. That is materially better than a single-choice
relay path.

The remaining issue is not “no failover”; it is that failover quality depends on
probe freshness, candidate ordering, and transport churn during long sessions.

### 5. Pairing transport hints and runtime transport selection are not unified

Pairing URLs use one transport candidate system in [desktop/agent/auth_pair.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_pair.go:150).

Host-share prep derives another order in [desktop/agent/host_share_prepare.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_prepare.go:432).

Runtime proxying uses a third, thinner decision path in [desktop/agent/agent_mesh_remote.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/agent_mesh_remote.go:16).

That split is a structural source of bugs.

### 6. Guest push is still text-only

`guest-push` currently skips non-UTF-8 files because the write endpoint accepts text content, not raw bytes.

That is acceptable as a temporary bridge, but not enough for real repositories with:

- images
- binary fixtures
- generated artifacts
- lockfiles or encoded files that are not valid UTF-8

## Fragility Analysis

## A. Transport fragility

### Request-level routing is stronger than session-level routing

One stale address no longer necessarily fails the whole request because the
runtime can step through more than one candidate. That said, the robustness is
still request-scoped more than session-scoped.

### No strong session transport stickiness

Host-share sessions should remember:

- last-good transport
- last-good origin
- last-good latency class

That does not exist today.

Without that:

- terminal behavior can flap between paths
- bus read/write latency can vary unpredictably
- reconnect behavior is more brittle after sleep/wake or network changes

### Limited handling for network transitions

Examples:

- Wi‑Fi to Ethernet
- office LAN to home Wi‑Fi
- Tailscale temporarily disconnected
- Cloudflare tunnel restarted
- relay endpoint degraded

The relay infrastructure itself is resilient, but host-share runtime selection is not yet resilient enough to react smoothly.

## B. Guest-bus fragility

### No binary-safe write path

This weakens the usefulness of host-side coding agents on guest repos.

### Pull/push is explicit, not transparently mounted

This is not necessarily wrong, but it means the host-side coding tools are not yet using a first-class remote filesystem adapter. That leaves more room for stale workspace state and operator confusion.

### Partial repo semantics

There is no full model yet for:

- delete propagation
- chmod / executable bit handling
- symlink handling
- conflict detection
- large file streaming
- resumable file transfer

## C. Operational fragility

### Readiness audit is advisory, not enforcing

The prep report in [desktop/agent/host_share_prepare.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/host_share_prepare.go:332) is useful, but nothing yet forces invite creation or live session handling to respect a verified working transport.

### Repo discovery is still a prerequisite

The guest-facing `attach-repo` flow only works for repo roots that Yaver has
already discovered locally. If the guest has not refreshed project discovery,
attach will fail even though transport and auth are fine.

That is not a security issue, but it is an operational sharp edge that docs and
UI should make explicit.

## Vulnerability and Security Analysis

This section is about product and implementation risk, not only code exploits.

### 1. Overexposed trust if transport routing is wrong

Host-share is intentionally narrow. If transport resolution falls back to an unexpected origin or stale endpoint, the caller may:

- fail closed
- or worse, hit the wrong device/origin assumptions

Current code still depends heavily on device ID lookup and trusted relay origin checks, which is good, but runtime origin selection needs to stay explicit and audited.

### 2. Host-share uses powerful host capabilities

Even with narrow endpoints, host-share gives real reach into:

- terminal
- runner inventory
- borrowed workspace
- guest repo bus

That means transport/auth mistakes matter more than ordinary read-only status surfaces.

### 3. Cloudflare tunnel auth integration is present but narrow

Cloudflare Access client credentials are now attached when the chosen base
matches a configured tunnel origin, which is the core requirement for protected
HTTPS tunnel use.

What is still missing is richer tunnel-specific health and policy behavior, not
basic header support.

### 4. Relay trust is origin-sensitive

Relay password checks in [desktop/agent/agent_mesh_remote.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/agent_mesh_remote.go:107) are good, but robustness work must preserve the current rule that relay origins are trusted only when present in config/platform data.

This must not regress into:

- arbitrary `/d/{deviceId}` origin acceptance
- insecure HTTP relay except loopback/dev
- silent downgrade to untrusted origin

### 5. Guest file writes are narrow but still sensitive

The guest-side write surface is constrained to allowed roots and active host-share sessions, which is correct, but it is still a write primitive into guest repos.

That means long-term hardening should include:

- explicit audit log entries for file writes
- optional write confirmation modes for novice guests
- clearer root binding visibility
- deletion support only when a stronger safety model is present

## Scenario Assessment

### Same LAN

Current state: supported and reasonably usable, but not fully polished for long
session churn.

### Tailscale

Current state: supported as part of the direct/private candidate ladder, with
the same remaining caveats as LAN around long-session stickiness.

### Cloudflare Tunnel

Current state: supported through advertised public HTTPS endpoints. Viable for
host-share runtime when the host advertises the configured tunnel URL.

### Self-hosted relay

Current state: usable with real candidate fallback, but still not a full
session-managed transport.

### Free relay

Current state: usable with the same request-level fallback behavior and the same
session-level limitations.

### Generic VPN

Current state: incidental support only.

Why:

- no explicit VPN-aware candidate model
- may work if the selected address happens to be reachable

## Required Outcome Before We Can Claim Robustness

Host-share should only be called robust when all of the following are true:

1. A single runtime transport resolver is used by:
   - pairing
   - host-share prep
   - remote device proxying
   - terminal attach
   - guest-bus calls

2. The resolver builds candidate sets for:
   - same-LAN
   - Tailscale
   - Cloudflare Tunnel
   - custom relay
   - free relay

3. The runtime path:
   - probes candidates
   - caches last-good path
   - retries on transport failure
   - fails over between relays
   - handles wake/sleep/network changes

4. Guest-bus write path becomes binary-safe.

5. There is stronger transport-aware and host-share attach/sync test coverage.

## Recommended Implementation Plan

### Phase 1: transport resolver

Add a shared resolver that returns ordered candidates for a target device:

- direct LAN candidates
- Tailscale candidates
- Cloudflare tunnel candidates
- relay candidates

Suggested entry points:

- `ResolveRemoteAgentCandidates(deviceID string)`
- `DialRemoteAgentWithFallback(...)`

### Phase 2: runtime retries and stickiness

Add:

- last-good transport cache per device
- latency-aware preference
- failover on timeout / network error / 5xx

### Phase 3: Cloudflare first-class runtime support

Use configured `CloudflareTunnelConfig` values from [desktop/agent/config.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/config.go:141) as true remote candidates.

### Phase 4: guest-bus hardening

Add:

- binary-safe file write endpoint
- optional delete endpoint with policy
- checksum support
- resumable transfer for large files

### Phase 5: tests

Add coverage for:

- direct LAN success, relay fallback
- Tailscale preferred over relay
- Cloudflare tunnel usable when direct is unavailable
- first relay down, second relay succeeds
- stale last-good path replaced automatically
- guest-bus binary roundtrip

## Bottom Line

Yaver host-share is already far enough along that the product direction is real:

- the lease/control plane exists
- the borrowed workspace exists
- the guest-bus path exists
- the host can already drive guest repo access through the guest agent

But the transport layer is still not strong enough to honestly claim:

> works robustly in all scenarios: LAN, Tailscale, Cloudflare Tunnel, relay, free relay, VPN

The current state is:

- **architecturally viable**
- **partially implemented**
- **transport-fragile**
- **not yet production-robust**

The primary blocker is not the host-share model itself. It is the lack of a unified runtime transport resolver with fallback, probing, stickiness, and Cloudflare integration.
