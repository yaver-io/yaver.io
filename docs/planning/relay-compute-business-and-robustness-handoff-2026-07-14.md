# Yaver Relay, Compute, Pricing, and Robustness Handoff

Date: 2026-07-14

This document is a coordination handoff for parallel coding-agent sessions. It captures the product and architecture decisions from the July 14, 2026 planning session, audits the current codebase at a high level, and breaks the implementation into independent workstreams.

As always in this repo: this Markdown is context, not authority. Before relying on any claim below, grep the code and open the named files. Code wins.

## Review Addendum (2026-07-14, second pass)

Three changes to the original draft. If you read nothing else, read these.

1. **Do NOT put the relay data plane behind Cloudflare's orange cloud — the
   WebSocket tunnel included.** `/d/*` proxies arbitrary agent paths, which means
   MJPEG (`capture.go:351`) and multi-MB Hermes bundles; and the WSS tunnel
   carries *every* proxied byte for a device. Marking `/d/*` `no-cache` does not
   help: Cloudflare's Free/Pro/Business terms restrict what you **serve through**
   them, not what you cache. Enforcement takes the whole `yaver.io` zone with it.
   Split by hostname: **control plane orange, data plane grey.**
2. **W0 (multi-relay failover) is new and is the highest-robustness item here —
   and it is a CONFIG task, not a code task.** Today one relay reboot
   disconnects the entire fleet, and no CDN can fix it (the relay is stateful,
   so a stateless proxy cannot route a deviceId to the origin holding its
   socket). But the agent ALREADY opens a tunnel to every configured relay and
   clients ALREADY probe every relay — so this is: stand up a second €3.80/mo
   box, add it to `platformConfig`, done. **No agent release required.**
3. **Cloudflare saves ~€0.** You are not egress-bound (20 TB included; MJPEG at
   ~2 GB/h ≈ 10,000 hours). Adopt Cloudflare for free WAF/DDoS on the control
   plane, never as a cost lever and never as a paid tier.

## Executive Decision

Yaver should be local-first and open-core:

- Keep the agent, CLI, self-hosted relay, self-hosted worker, and protocol surface open.
- Monetize managed robustness: priority relay, managed compute, team quota pools, dedicated relay/worker, hosted scheduler, billing, quotas, and operations automation.
- Do not sell unlimited relay or unlimited compute.
- Sell capped, predictable plans because normal users are uncomfortable with uncapped usage and surprise bills.
- Keep self-hosting first-class because it is the trust funnel and the wedge against pure SaaS competitors.

The short product sentence:

> Yaver is the local-first infrastructure layer for Claude Code, Codex, Cursor, and mobile development: bring your own machines for free, or pay Yaver for robust relay, managed compute, team pools, and no surprise bills.

## Current Code Audit

### Relay

Relevant files:

- `relay/server.go`
- `relay/tunnel.go`
- `relay/protocol.go`
- `relay/bandwidth.go`
- `relay/abuse_guard.go`
- `relay/deploy/nginx-relay.conf`
- `desktop/agent/main.go`
- `desktop/agent/relay_health.go`
- `mobile/src/lib/quic.ts`

What exists:

- QUIC relay server and agent tunnel already exist.
- HTTP proxy path is `/d/{deviceId}/...`.
- Relay auth supports shared password and Convex-backed per-user validation.
- Device signature auth is partially present for `/d/` proxy requests.
- Abuse guard exists for HTTP, `/d/`, bus, admin, invalid auth, and QUIC registration.
- Bandwidth manager exists with defaults around free and paid device limits.
- Liveness watchdogs and zombie tunnel detection were **added on 2026-07-14**
  (`relay/tunnel_liveness.go`). They did NOT exist before that, and the gap was
  not theoretical: an always-on Mac mini sat registered-but-not-forwarding for
  over an hour while BOTH ends reported a healthy tunnel, every phone request
  timed out, and it recovered only when the agent was restarted. `OpenStreamSync`
  succeeds against a dead peer (QUIC opens the stream locally, no round-trip), so
  nothing in the request path ever noticed. Worse, the zombie **blocked its own
  replacement** — re-registration is refused as a duplicate deviceId while the
  stale connection's context never fires. Do not read this line as "always
  covered".
- Nginx config already supports SSE and WebSocket pass-through on the HTTP side.

Gaps:

- Agent-to-relay tunnel is QUIC/UDP:4433 ONLY, with no TCP path at all. On a
  UDP-blocked network (corporate wifi, hotels, some carriers) the agent cannot
  reach the relay, and there is nothing to fall back to. The fix is a WSS tunnel
  on TCP/443 terminating **directly on the relay** — see W1. Cloudflare is not
  required for it and must not carry it (see Cloudflare Relay Strategy).
- **There is exactly ONE relay, and it is stateful** (`tunnels map[deviceID]`).
  If it reboots, every phone loses every machine — there is no LAN or VPN path
  from LTE. No CDN or load balancer can fix this, because a stateless proxy
  cannot route a deviceId to the origin holding its socket. See **W0**, which is
  the highest-value robustness item in this document.
- Bandwidth limits are device-centric and not yet a full owner/team quota-pool model.
- Traffic classes are not first-class across relay accounting: `control`, `terminal`, `hermes`, `artifact`, `media`, `expose`.
- Convex validation is in the auth path. It should stay cached and low-frequency; do not move byte/frame metering into Convex.

### Compute and Managed Cloud

Relevant files:

- `desktop/agent/cloud.go`
- `desktop/agent/cloud_byo_provision.go`
- `desktop/agent/cloud_deploy.go`
- `desktop/agent/cloud_broker.go`
- `desktop/agent/cloud_capacity.go`
- `desktop/agent/cloud_stopstart.go`
- `desktop/agent/container_runner.go`
- `desktop/agent/remote_builder.go`
- `desktop/agent/Dockerfile.yaver-cloud`
- `backend/convex/cloudMachines.ts`
- `backend/convex/cloudLifecycle.ts`
- `backend/convex/managedMeter.ts`
- `backend/convex/schema.ts`
- `backend/convex/subscriptions.ts`
- `backend/convex/runnerUsage.ts`

What exists:

- BYO Hetzner provisioning exists and explicitly uses the user's provider token. Yaver's wallet is not involved there.
- Managed cloud machine model exists in Convex.
- Managed cloud bootstrap/cloud-init exists.
- Prepaid wallet exists: `prepaidCredits`.
- SKU-specific compute ledger exists: `creditUsage`.
- Generic managed resource meter exists: `managedUsage`.
- Included-hour allowance exists.
- Container runner exists for Docker-based task execution and caches common package managers.
- Remote builder registry exists and intentionally keeps builder hostnames/tokens local, not in Convex.

Gaps:

- There is no clean compute backend abstraction that can target local Docker, BYO agent, managed VM, dedicated VM, and future Kubernetes Jobs uniformly.
- Managed compute should not run through Convex actions. Convex should only hold entitlement, quota summaries, and low-frequency usage ledger rows.
- Shared compute pool scheduling is not yet a separate service.
- Job-level cost enforcement needs explicit limits: wall time, CPU seconds, RAM, disk, artifact size, network, and concurrency.
- Mac/iOS compute should be treated as a later premium add-on, not bundled early.

### Billing and Product Surface

Relevant files:

- `desktop/agent/mcp_billing.go`
- `desktop/agent/mcp_paid_gate.go`
- `backend/convex/plans.ts`
- `backend/convex/subscriptions.ts`
- `backend/convex/cloudLifecycle.ts`
- `backend/convex/managedMeter.ts`
- `backend/convex/schema.ts`
- `docs/yaver-mcp-billing.md`
- `docs/yaver-normie-concierge-fair-metering.md`
- `docs/yaver-managed-cloud-ci-absorption.md`

What exists:

- Buyer-side billing MCP tools exist but are hidden for launch by `mcp_paid_gate.go`.
- Subscription rows and LemonSqueezy webhook logic exist.
- Wallet and top-up primitives exist.
- Managed usage meter is designed around provider COGS plus markup.
- Per-user managed service opt-in exists conceptually through `userSettings.managedServices`.

Gaps:

- Need plan packaging that users can understand without fear of surprise bills.
- Need hard cap behavior and messaging.
- Need owner/team-only paid entitlement enforcement for relay and compute.
- Guest access must never silently consume unlimited paid quota.

### Convex Cost Boundary

Convex is useful for identity, devices, teams, subscriptions, quotas, wallet, and low-frequency ledgers.

Convex must not be the hot path for:

- Hermes bundles.
- Relay byte streams.
- Remote runtime frames.
- Build logs at high volume.
- Per-frame/per-chunk metering.
- Artifact storage.
- Long-running compute execution.

Current official Convex pricing/limits checked on 2026-07-14:

- Function calls: 1M included on Starter, then about $2.20/M; Professional includes 25M, then about $2/M.
- Action compute: Starter includes 20 GB-hours, then about $0.33/GB-hour; Professional includes 250 GB-hours, then about $0.30/GB-hour.
- Database I/O and data egress are metered.
- Actions have execution and response-size limits that make them wrong for build/runtime streams.

Architecture rule:

```text
Relay/worker counts locally -> aggregate -> flush summarized rows to Convex.
```

Do not write one Convex row per log line, frame, stream chunk, or request.

## Product and Pricing Model

Use capped packages, not uncapped usage.

Initial suggested packages:

| Plan | Price | Relay | Compute | Target |
|---|---:|---:|---:|---|
| Free | $0 | 3-5 GB/mo shared | local/self-hosted only | trial, text/control |
| Pro | $12/mo | 100 GB/mo priority | 20 Linux compute hours | solo Claude Code/Codex/Hermes user |
| Pro Plus | $19/mo | 300 GB/mo priority | 50 Linux compute hours | heavy mobile dev |
| Team | $39/mo + seats | 1 TB pooled | 100+ Linux compute hours pooled | teams |
| Dedicated | $79+/mo | private relay | private worker | heavy/private/predictable |

Hard rules:

- Overage is disabled by default.
- Optional overage, if added, must be prepaid wallet only.
- After cap, control/auth/status keeps working.
- After cap, Hermes/artifacts/media/expose/managed compute pause or throttle.
- Guests default to free/control-only unless the owner explicitly grants a small allowance.

Traffic classes:

- `control`: auth, health, status, settings, diagnostics.
- `terminal`: command text and shell I/O.
- `hermes`: Hermes bundles, RN reload artifacts.
- `artifact`: APKs, zips, build outputs.
- `media`: remote runtime frames/video-like traffic.
- `expose`: public URL/subdomain traffic.

Owner/team quota pool model:

```text
paid privileges attach to ownerUserId or teamId
actorUserId determines whether caller is owner, team member, or guest
poolId is charged by traffic class
per-device and per-actor fairness caps prevent one machine/guest from burning the pool
```

## Cloudflare Relay Strategy

**REVISED 2026-07-14 (second review). The original version of this section
contradicted itself and would have put Yaver's video traffic through
Cloudflare's CDN. Read the correction before implementing.**

Cloudflare should provide:

- DNS.
- TLS.
- WAF and HTTP DDoS shielding **on the control plane**.
- Edge cache only for public static/config endpoints.

Cloudflare must NOT carry:

- Private video/artifact traffic. **This is not a caching rule. It is a
  ToS rule.**
- Custom UDP for QUIC (needs Spectrum/Enterprise — not the near-term cost model).

### Correction 1 — `no-cache` does not make `/d/*` legal

The first draft put `relay.yaver.io` on the orange cloud and listed `/d/*` as
merely *no-cache*. That does not work, for a reason that has nothing to do with
caching:

- `/d/` proxies **arbitrary agent paths** (`relay/server.go:1122` →
  `handleProxy`).
- The agent serves `multipart/x-mixed-replace` MJPEG on `/capture/stream`
  (`desktop/agent/capture.go:351`) and `/rd/stream`, plus multi-MB Hermes
  bundles.
- Cloudflare's Free/Pro/Business terms restrict **serving** streaming video and
  disproportionate large files not hosted on Stream/R2. `no-cache` changes what
  is *stored at the edge*; it does not change what is *served through* it.

Enforcement would not just kill the relay — it takes the whole `yaver.io` zone
with it: web, docs, download hub. Verify the current Service-Specific Terms
before committing money; this analysis is from 2026-07-13/14.

### Correction 2 — the WebSocket TUNNEL is the data plane, not a side channel

The first draft also put the tunnel at `wss://relay.yaver.io/agent/tunnel`,
orange-cloud. **That smuggles the video through Cloudflare regardless of any
`/d/*` cache rule**, because every proxied byte for a device — MJPEG frames
included — rides that one tunnel. Fixing the `/d/*` rule alone does not save
you. The tunnel must be grey-cloud too.

### Correction 3 — WebSocket is needed because UDP gets blocked, not because Cloudflare exists

The original text said "Cloudflare can proxy WebSocket, **therefore** the relay
needs a WebSocket fallback." That is a non-sequitur, and it is load-bearing: it
implies the fallback is a Cloudflare feature that could be dropped with
Cloudflare. It cannot. The agent leg is **QUIC/UDP:4433 only**
(`relay/server.go` → `net.ListenUDP`) with no TCP path at all, so on any
UDP-blocked network (corporate wifi, hotels, some carriers) the agent simply
cannot reach the relay.

**WSS on TCP/443 terminating directly on the relay solves that completely, with
no Cloudflare in the path.** Cloudflare is orthogonal to this fix.

### Corrected transport ladder

```text
1. LAN direct
2. Tailscale / WireGuard / direct HTTPS
3. Yaver QUIC relay            (UDP 4433, grey-cloud)
4. Yaver WebSocket relay       (TCP 443,  grey-cloud)  <- defeats UDP blocking
5. (no Cloudflare tunnel — see project_transport_no_cloudflare_tunnel)
```

### Corrected DNS shape — split the planes by hostname

```text
relay.yaver.io      ORANGE  control plane only: /relay/validate, /bus/*,
                            /presence, /config. Small JSON. Gets WAF + DDoS.
data.yaver.io       GREY    data plane: /d/* proxy AND the WSS agent tunnel.
                            MJPEG, Hermes bundles, artifacts. Direct to origin.
quic-relay.yaver.io GREY    UDP 4433 QUIC tunnel.
*.dev.yaver.io      GREY    expose/preview. NOTE: this zone DOES NOT EXIST today
                            (NXDOMAIN, verified 2026-07-14) while the relay is
                            started with --expose-domain=dev.yaver.io, so every
                            device advertises a publicUrl nothing can resolve.
                            Create the zone or unset the flag.
```

The point of the split: the control plane is the actual attack surface (auth,
registration, pub/sub) and it is tiny, so it gets Cloudflare's free WAF/DDoS at
zero ToS risk. The data plane is bulky and video-shaped, so it goes straight to
the origin — which also dodges Cloudflare's 100s timeout and idle resets, both
hostile to long-lived MJPEG and SSE.

### Agent tunnel behaviour

```text
agent opens QUIC tunnel to quic-relay.yaver.io:4433   (fast path)
if UDP is blocked/unhealthy, agent opens WSS tunnel to
    wss://data.yaver.io/agent/tunnel/ws                (grey-cloud, TCP 443)
relay prefers QUIC when it is delivering; WSS otherwise
ping/pong heartbeat on both (idle-timeout resilience at any reverse proxy)
```

### Cost reality — Cloudflare saves ~€0

State it plainly so nobody adopts Cloudflare for a saving that does not exist:

- Relay = one Hetzner `cax11`: **~€3.80/mo with 20 TB egress included**; overage
  ~€1/TB. MJPEG at ~2 GB/h means 20 TB ≈ **10,000 streaming hours** on one box.
  You are nowhere near egress-bound.
- Cloudflare's value here is **WAF/DDoS, not money**.
- **A second relay for failover costs €3.80/mo. Cloudflare Load Balancer costs
  ~$5/mo and cannot work here anyway** (see W0 — the relay is stateful). The
  cheaper option is also the only one that works.
- Paying Cloudflare *more* does not buy the right to stream video: the
  restriction applies to Free, Pro **and** Business. Only Stream/Spectrum/
  Enterprise would, at multiples of a whole Hetzner box per customer. So
  "Cloudflare for paid users" is either a ToS violation or a margin wipeout.
  **Cloudflare is a $0 hardening layer, never a product tier.**

## Source Strategy

Recommended: open-core.

Keep open:

- CLI.
- Desktop agent.
- Self-hosted relay.
- Self-hosted worker.
- Protocol docs.
- Basic Claude Code/Codex/Cursor integration.

Keep proprietary/closed:

- Managed scheduler.
- Billing/quota service.
- Cloudflare edge config.
- Hosted dashboard internals where needed.
- Managed worker images and ops automation.
- Abuse detection heuristics.
- Provider orchestration for Yaver-owned infrastructure.
- Premium team controls if they are mostly hosted-service value.

Do not fully close source. Yaver's trust story depends on users being able to inspect and self-host the core.

## Protection Rules

Cost protection:

- No unlimited plans.
- No implicit overage.
- Prepaid wallet only for overage.
- Auto-stop/auto-park managed compute when quota or wallet cannot cover the next interval.
- Keep control path alive after cap.

Abuse protection:

- No open relay.
- Rate limit by IP, user, device, pool, and class.
- Block public proxy behavior.
- Block RFC1918/pivoting in shared compute by default.
- No Docker socket in shared jobs.
- No privileged containers in shared jobs.
- Kill long jobs.
- Limit artifacts.
- Detect crypto mining, scanning, spam, and high-volume media abuse.

Legal/commercial protection:

- Free has no SLA.
- Paid has capped usage, not unlimited.
- Yaver may throttle or suspend abusive relay/compute use.
- User is responsible for code/content they run.
- Dedicated/private relay terms are separate from shared relay terms.

## Independent Workstreams

These are intended for parallel sessions. Each workstream should grep and verify current code before editing.

### W0: Multi-Relay Failover — DEFERRED. Do not build this yet.

**Status 2026-07-14 (third pass): DEFERRED, deliberately. Do not stand up a
second relay.** I recommended this an hour ago and I was wrong; the reasoning is
worth keeping because it is the sort of mistake that is easy to repeat.

Two reasons it is the wrong next move:

1. **A second relay does not fix the failure we actually have.** The observed
   outage was a ZOMBIE TUNNEL, not a dead box — `public.yaver.io` answered 401
   (alive, reachable) throughout, while the mini's tunnel had stopped forwarding.
   The agent tunnels to every configured relay, so with two relays it would have
   held two QUIC/UDP tunnels **from the same host, over the same NAT, on the same
   path**. Whatever killed tunnel A almost certainly kills tunnel B in the same
   instant. Two copies of a failing transport is still a failing transport.
2. **There is no traffic.** Redundancy and capacity work insure against load and
   hardware failure. We have neither users nor an observed box-level outage. This
   is an insurance premium on a car nobody drives — and it is not free: two boxes
   means two to patch, monitor, version-match, and keep relay-password-synced.

**The correct generalisation: we need TRANSPORT diversity, not BOX diversity.**
One relay reachable two ways (QUIC/UDP **and** WSS/TCP-443) survives the failure
we have actually seen. Two relays reachable one way do not. W1 is therefore the
real redundancy work, and it is free.

Revisit W0 only when one of these becomes true:

- A relay box actually goes down (we have never observed this), or
- We sell an SLA where a single reboot is unacceptable.

When that day comes, the good news (verified in code, and locked by
`relay_multihome_test.go`) is that **it is a config task, not a code task** — see
below. Nothing needs to be built in advance.

#### Why it will be cheap when we do want it

Today: **one relay box. If it reboots, every phone loses every machine.** There
is no LAN path and no VPN path from a phone on LTE — the relay is the only route.

Why Cloudflare cannot fix this (and why the "just put a load balancer in front"
instinct fails):

```go
relay/server.go:80   tunnels map[string]*agentTunnel   // stateful, in ONE process
relay/server.go:867  s.tunnels[reg.DeviceID] = tunnel
```

A device's tunnel lives in **one relay process's memory**. Put a stateless LB
(Cloudflare or otherwise) in front of two relays and a client request for a
device tunnelled to relay A that lands on relay B finds no tunnel and 502s.
**A stateless CDN cannot route by deviceId to the stateful origin holding the
socket.** This is an application-layer routing problem; buy no product for it.

**CORRECTED after reading the code (do not skip this — it changes the work by an
order of magnitude).** An earlier revision of this section proposed a new
`relayHost` heartbeat field and per-device relay routing. **That is not needed.
The code already supports N relays:**

- `relayManager.applyRelayServers` (`desktop/agent/main.go`) builds a `desired`
  set from **every configured relay** and starts a tunnel to **each one** —
  `for addr, pw := range desired { go runRelayTunnel(...) }`. An agent registers
  with **all** relays it knows about, not one.
- `buildRemoteAgentCandidates` (`desktop/agent/agent_mesh_remote.go:362`) emits
  `<relay.HttpURL>/d/<deviceID>` for **every** relay in the list, so clients
  already try all of them and (since the per-leg-deadline fix) race them
  honestly.

So a device is reachable through **any** relay it is tunnelled to, and a client
will find it. **Multi-relay failover is therefore a CONFIG task, not a code
task.** The reason it does not work today is simply that `platformConfig` lists
**one relay** — which is why the agent logs `Using 1 cached relay server(s)`.

Implementation outline (mostly ops):

1. Stand up a second relay (`cax11`, ~€3.80/mo, different region/AZ from the
   first). It must be always-on; relays cannot be scale-to-zero.
2. Add it to `platformConfig` relay servers. Agents pick it up on the next config
   refresh and open a second tunnel automatically. **No agent release required.**
3. Verify: `yaver ops machine_doctor --payload='{"device":"<id>"}'` should now
   show a `reachable` leg per relay. Kill relay A; the box must stay reachable
   via relay B with no user action.
4. Watch out: the relay is stateful per-process, so this works ONLY because the
   agent registers with both. If a future change makes the agent pick a single
   "best" relay, this property silently dies — add a test that asserts an agent
   with two configured relays opens two tunnels.

Optional optimisation (NOT required for HA): publish which relay is actually
carrying the device so clients can skip probing the others. Worth it only if the
probe fan-out becomes measurably slow; with the per-leg deadlines it is already
acceptable. Do not build it before the second relay exists.

Interaction with W1 and the zombie work: eviction (`relay/tunnel_liveness.go`)
makes a dead tunnel *detectable*; W0 makes it *survivable*. Ship eviction first
(it fixes the outage class), then W0 (it removes the SPOF).

Deliverable:

- One relay reboot no longer disconnects the entire fleet.
- Adding relay capacity is horizontal, not a migration.
- Zero Cloudflare dependency; €3.80/mo per unit of redundancy.

### W1: Relay WebSocket Fallback

Goal: give the agent a TCP/443 tunnel so UDP-blocked networks still work.

**Note the corrected framing (see Cloudflare Relay Strategy, Correction 3):
this exists because the agent leg is QUIC/UDP-only and UDP gets blocked — NOT
because Cloudflare proxies WebSocket. Terminate it grey-cloud, directly on the
relay. Routing the tunnel through Cloudflare would push every proxied byte —
MJPEG frames included — through their CDN.**

Primary files:

- `relay/server.go`
- `relay/tunnel.go`
- `relay/protocol.go`
- `relay/deploy/nginx-relay.conf`
- `desktop/agent/main.go`
- `desktop/agent/config.go`
- tests under `relay/*_test.go` and `desktop/agent/*relay*_test.go`

Implementation outline:

1. Add relay HTTP endpoint such as `/agent/tunnel/ws`.
2. Authenticate registration with the same `RegisterMsg` fields as QUIC.
3. Introduce a tunnel interface internally so proxy code can use QUIC or WebSocket transport.
4. Preserve current QUIC behavior.
5. Agent attempts QUIC first and WebSocket fallback second, or maintains both where cheap.
6. Add ping/pong heartbeat for Cloudflare idle timeout resilience.
7. Update nginx config to forward the new endpoint with WebSocket upgrade headers and no buffering.
8. Add tests for registration, duplicate registration, proxying one request, and fallback when QUIC is unavailable.

Deliverable:

- QUIC remains the fast path.
- WebSocket works through normal HTTPS reverse proxy and Cloudflare.
- Existing mobile/web `/d/{deviceId}/...` clients do not need an immediate protocol change.

### W2: Relay Quota Pools and Traffic Classes

Goal: make relay metering product-safe without using Convex as the hot path.

Primary files:

- `relay/bandwidth.go`
- `relay/server.go`
- `relay/abuse_guard.go`
- `backend/convex/userSettings.ts`
- `backend/convex/schema.ts`
- `backend/convex/managedMeter.ts`

Implementation outline:

1. Add traffic-class classifier for relay paths.
2. Introduce `ownerUserId`, `actorUserId`, `teamId`, `poolId`, `role`, `plan`, `trafficClass` entitlement shape.
3. Extend Convex `/relay/validate` to return entitlement metadata, not just OK/userId.
4. Cache entitlement at relay for short TTL.
5. Meter bytes locally by pool + class.
6. Enforce free/pro/team/dedicated class caps locally.
7. Add periodic aggregate flush endpoint or internal action for summarized usage rows.

Deliverable:

- Control path survives quota exhaustion.
- Hermes/artifacts/media/expose can be throttled or paused independently.
- Guests do not consume paid privileges unless explicitly allowed.

### W3: Compute Backend Abstraction

Goal: make compute backend-pluggable before introducing Kubernetes.

Primary files:

- `desktop/agent/container_runner.go`
- `desktop/agent/cloud_byo_provision.go`
- `desktop/agent/cloud.go`
- `desktop/agent/remote_builder.go`
- new files likely under `desktop/agent/compute_*.go`

Suggested interface:

```go
type ComputeBackend interface {
    StartJob(ctx context.Context, spec JobSpec) (JobHandle, error)
    StopJob(ctx context.Context, jobID string) error
    StreamLogs(ctx context.Context, jobID string) (io.Reader, error)
    Usage(ctx context.Context, jobID string) (JobUsage, error)
}
```

Backends:

- local Docker.
- BYO Yaver agent.
- managed VM.
- dedicated worker.
- future Kubernetes Jobs.

Deliverable:

- Existing sandbox/container runner still works.
- New code can choose backend without knowing if it is local, BYO, managed, or future k8s.

### W4: Compute Quotas and Job Limits

Goal: make managed compute safe to sell.

Primary files:

- `desktop/agent/container_runner.go`
- `desktop/agent/sandbox*.go`
- `backend/convex/cloudLifecycle.ts`
- `backend/convex/managedMeter.ts`
- `backend/convex/schema.ts`

Implementation outline:

1. Define job limit spec: wall time, CPU seconds, memory MB, disk GB, network GB, artifact bytes.
2. Enforce limits in local/container backend.
3. Emit job-end aggregate usage.
4. Debit included hours or prepaid wallet only from aggregate usage.
5. Ensure jobs cannot start if entitlement cannot cover them.

Deliverable:

- Managed compute cannot run unbounded.
- Usage rows are aggregate counters only.

### W5: Product Plan and Billing Surfacing

Goal: expose capped plans clearly.

Primary files:

- `backend/convex/plans.ts`
- `backend/convex/subscriptions.ts`
- `desktop/agent/mcp_billing.go`
- `desktop/agent/mcp_paid_gate.go`
- web billing/pricing files

Implementation outline:

1. Add Free/Pro/Pro Plus/Team/Dedicated plan constants.
2. Map plan to relay quota and compute hours.
3. Keep overage disabled by default.
4. Update billing status to show remaining relay GB and compute hours.
5. Keep mobile purchase-free for App Store policy; web/CLI owns checkout.

Deliverable:

- Users see capped allowances.
- No surprise-bill language.
- Paid MCP tools can be unhidden only when plan implementation is safe.

### W6: Cloudflare Deployment Hardening

Goal: make relay v2 robust behind Cloudflare without pretending Cloudflare is a custom UDP CDN.

Primary files:

- `relay/deploy/nginx-relay.conf`
- `scripts/setup-relay-cf-security.sh`
- `scripts/provision-relay.sh`
- `scripts/install-relay.sh`
- docs under `docs/`

Implementation outline:

1. Add Cloudflare no-cache rules documentation and script support.
2. Ensure WebSocket endpoint is proxied correctly.
3. Keep QUIC hostname DNS-only.
4. Add `/config` cache recommendations.
5. Add health checks for HTTP/WebSocket path separately from QUIC.

Deliverable:

- Operators can deploy robust relay with Cloudflare HTTPS/WebSocket protection.
- QUIC path remains direct and optional.

### W7: Documentation and Terms

Goal: make users and future agents understand the model.

Primary files:

- `relay/README.md`
- `docs/architecture/AI_ARCH.md`
- `docs/architecture/REMOTE_WORKER.md`
- `docs/yaver-mcp-billing.md`
- new docs as needed

Implementation outline:

1. Document relay transport ladder.
2. Document self-hosted vs managed.
3. Document quota behavior after cap.
4. Document open-core source strategy.
5. Document abuse and acceptable use boundaries.

Deliverable:

- Consistent docs that match the implementation.

## Priority Order

Do these first:

1. W1 Relay WebSocket fallback.
2. W2 traffic classes and local quota seams.
3. W6 Cloudflare deployment hardening.

Then:

4. W3 compute backend abstraction.
5. W4 compute quota/job limits.
6. W5 billing plan surfacing.
7. W7 docs/terms cleanup.

Reason: relay robustness is the immediate product reliability gap. Compute monetization is already partially scaffolded, but should not be sold until quota and job limits are hard.

## Test Expectations

Relay work:

```bash
cd relay && go test ./...
cd desktop/agent && go test ./... -run 'Relay|Tunnel|WebSocket|Proxy|Bandwidth'
```

Compute work:

```bash
cd desktop/agent && go test ./... -run 'Container|Sandbox|Cloud|Billing|Meter|Compute'
```

Convex work:

```bash
cd backend && npx convex codegen
cd backend && npm test -- --runInBand
```

Use the actual repo test commands if these drift. Grep package scripts first.

## Do Not Do

- Do not deploy, publish, push, tag, or commit without explicit permission.
- Do not move relay byte/frame/log traffic through Convex.
- Do not sell or implement unlimited paid usage.
- Do not put Cloudflare orange-cloud in front of the QUIC UDP hostname unless using Spectrum intentionally.
- Do not implement shared managed compute with privileged containers.
- Do not put provider tokens, relay hostnames, customer IPs, or secrets in tracked files.
- Do not break self-hosted relay/worker paths while adding managed features.

