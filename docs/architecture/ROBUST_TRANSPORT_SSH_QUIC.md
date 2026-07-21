# Robust phone↔agent transport — SSH / reverse-SSH / QUIC-over-relay

> **Companion to [`SECURE_FRICTIONLESS_TRANSPORT_SETUP.md`](./SECURE_FRICTIONLESS_TRANSPORT_SETUP.md)**,
> which is the **canonical target design** (key hierarchy, signed request
> envelopes, relay tiers, reverse-SSH forced-command, transport selector). This
> doc does NOT redesign that — it is the **incident-grounded record** of the
> 2026-07-21 "always fails to connect" failure and the **immediate Road A
> hardening** that implements a slice of that plan's **Phase 3 (relay
> device-signature/robustness)** using the *existing* QUIC relay, so users stop
> losing their connection today. Where the two docs touch, the canonical one wins;
> update it if code moves.
>
> **Concrete alignment already shipped:** the canonical doc's Reverse-SSH → Relay
> Behavior rule — *"close old tunnel generation when a new one for the same
> machine becomes live"* — is exactly the fix landed in `relay/server.go`
> (same-authenticated-user eviction on registration collision). It kills the
> up-to-120s "deviceId already registered" black hole for the *current* QUIC
> relay, and is the same generation-replacement semantics the reverse-SSH design
> prescribes.

Deep evaluation (2026-07-21), grounded in the real failure logs from a phone that
"always fails to connect" to the mac-mini (`Mobiles-Mac-mini`, deviceId `229aeb03`).

## 0. What actually failed (from the logs, not theory)

Phone connection log, 09:51–09:54:
- **All direct legs are unroutable from the phone**: `lan-heartbeat 192.168.1.105`,
  `lan-tailscale 100.89.x`, `lan-convex-ip …` → *every* one "unroutable (no route
  from this phone to that address right now)". The phone is off-LAN and **not on
  the tailnet** (or iOS ATS blocks cleartext to 100.64/10 — see
  `ios_ats_blocks_tailnet_and_mesh`). So **relay is the only path**.
- **Relay is a single public relay** (`public-free` / `public.yaver.io` /
  `46.224.110.38:4433`) with **no redundancy** ("all 1 relay(s) failed").
- Relay password was initially **missing** ("sign in again to fetch it") then
  self-healed one second later.
- The relay tunnel came up (09:51:16 "agent answered via relay"), held ~2.5 min,
  then **timed out at 09:54:02 and never recovered** in the window.

Mini agent (relay client) log:
- `[RELAY 46.224.110.38:4433] Connection lost after 0s: registration rejected:
  deviceId already registered` → `Reconnecting in 57.6s…`. **This is the
  deadlock**: when the tunnel drops and the mini redials, the relay still holds
  the *stale* registration for the same deviceId and rejects the new one, so the
  mini is unreachable for up to ~57 s even though its watchdog wants to redial.
- **Yaver Mesh is force-disabled**: `utun4 (100.89.155.25)` (Tailscale) overlaps
  Mesh's `100.64.0.0/10`, so Mesh stays off to avoid fighting the VPN. That
  removes the lean P2P path entirely, leaving only the flaky relay.

**Net:** the phone depends on a *single, custom, flap-prone* relay tunnel whose
reconnection deadlocks on "already registered". Everything else (direct, tailnet,
mesh) is unavailable in the phone-off-LAN case, which is the *common* case.

## 1. The design question

The user's proposal: **signal over Convex/relay, then pick a real transport** —
- both sides on Tailscale/VPN → plain SSH (direct),
- otherwise → reverse-SSH held open at the relay (with Yaver Mesh),
- the phone speaks SSH to the go agent over QUIC to summarize status, etc.,
- **fully secure**.

The instinct is correct: **stop depending on a bespoke always-managed relay data
tunnel; use a battle-tested keep-alive/reconnect discipline and a signaled,
per-situation transport.** But the details matter, especially two hard constraints.

## 2. Two hard constraints that shape the answer

1. **iOS is not an SSH host.** There is no system `ssh` on iOS, and a React-Native
   app embedding libssh2 is a heavy, awkward dependency (key storage, PTY,
   channels) for what is really *"call the agent's HTTP API"*. The agent already
   exposes everything the phone needs as HTTP (`/tasks`, `/tasks/{id}/continue`,
   `/projects/mobile`, `/info`, …). Wrapping those in an SSH exec channel is
   redundant. **Conclusion: the phone should keep HTTP-over-QUIC, not become an
   SSH client.** SSH's value is on the *server-reachability* leg, not the phone
   API leg.
2. **iOS ATS blocks cleartext to 100.64/10.** Even when the phone *is* on the
   tailnet, ATS refuses `http://100.x`. The agent's tailnet path must be **TLS**
   (the agent's `:18443` self-signed HTTPS with cert pinning), not plain HTTP.

So "both sides on Tailscale → plain SSH" is really "**phone on tailnet → direct
HTTPS-over-QUIC to the agent's `:18443`**", and it only helps when the phone is
actually on the tailnet — which, per the logs, is exactly *not* the failing case.

## 3. Where SSH genuinely helps: the server-reachability leg

The problem is not the phone's API dialect — it's that **the mini has no reliable
always-on path from the outside**. This is the textbook **reverse-tunnel-through-a-
bastion** problem, and SSH's `autossh -R` is the canonical, hardened solution:

- The mini **dials out** to a bastion and holds a **reverse tunnel** (`-R`), so a
  phone (via the bastion) reaches the mini through NAT.
- `ServerAliveInterval`/`ServerAliveCountMax` + `ExitOnForwardFailure` +
  `autossh` give **sub-second-detected, self-healing** tunnels — precisely the
  discipline our custom relay client lacks (it deadlocks 57 s on "already
  registered").

Our **current QUIC relay already *is* this pattern** (mini dials relay, holds a
tunnel, relay forwards phone→mini). The gap is **robustness**, not architecture.
So there are two credible roads:

### Road A — Harden the existing QUIC relay to autossh-grade (recommended first)
Keep QUIC-over-relay; give it SSH's reconnection discipline:
1. **Fix the "already registered" deadlock** — relay evicts the stale
   registration on a new dial from the same deviceId+auth (**last-writer-wins**),
   OR the mini sends an explicit `deregister` before redial. Removes the 57 s
   black hole. *(Highest-leverage single fix.)*
2. **Aggressive keepalive + instant redial** — QUIC PING every ~10 s, and on a
   dead tunnel redial in <1 s with jittered backoff, not 57 s. Mirror
   `ServerAliveInterval=10, ServerAliveCountMax=3`.
3. **Relay redundancy** — ≥2 relays (regions), race them; never "all 1 relay
   failed". Phone and agent both hold the tunnel to the *primary* and fail over.
4. **Health-verified registration** — the relay only advertises a device as
   reachable while its tunnel answers a PING, so the phone never gets
   "online but no transport answered".

This is ~2 days of work, reuses the existing transport the app already speaks,
needs **no iOS SSH client**, and directly kills every failure in §0.

### Road B — Reverse-SSH bastion as an *alternative* relay backend
Run an SSH bastion alongside (or as) the relay. The mini holds `autossh -R`; the
relay's HTTP proxy forwards `public.yaver.io/d/<deviceId>/…` into the reverse
tunnel. The phone is unchanged (still HTTP-over-QUIC to the proxy URL).
- **Pros:** inherits SSH's decades-hardened keepalive/reconnect/multiplexing for
  free; `autossh` is boringly reliable; ops can debug with standard tools.
- **Cons:** adds an SSH bastion to operate; per-device SSH keys to mint/rotate/
  revoke; the reverse tunnel must stay **E2E-opaque to the bastion** (see §4) to
  keep the "relay never sees task data" contract; and it duplicates what Road A's
  hardened QUIC already gives us. Best considered if Road A's custom client keeps
  proving fragile.

### The Tailscale fast-path (independent of A/B)
When the **phone is on the tailnet**, prefer **direct HTTPS-over-QUIC to the
agent `:18443`** (cert-pinned) — lowest latency, no relay, no bastion. This is the
"both sides have Tailscale → go direct" case, done over TLS (not cleartext, not
SSH) to satisfy ATS. Gate it on a real reachability probe (the current code
already races these legs; it just needs the TLS/ATS-safe leg to actually succeed).

## 4. Security (must hold for any road)

- **End-to-end, relay/bastion is pass-through.** The relay/bastion must never be
  able to read phone↔agent traffic — it forwards ciphertext. This preserves the
  Convex privacy contract ("relay never stores task data"). With SSH-in-the-
  middle this means the *payload* is still the agent's TLS/authenticated channel,
  i.e. SSH tunnel carrying TLS, or the QUIC tunnel already being E2E to the agent.
- **Per-device credentials, revocable, least-privilege.** No shared secrets, no
  password auth. If SSH: per-device keypairs + a **forced command** that exposes
  *only* the agent API forward, never a shell (`command="…",no-pty,
  no-agent-forwarding,permitopen="127.0.0.1:18080"`). Revoke a device = drop its
  key.
- **Auth binding.** The tunnel registration is bound to the same auth token /
  device identity the agent already uses; a rotated token invalidates the tunnel
  (the logs already show "Auth token rotated since last attempt — using the
  current one").
- **No open relay / anti-pivot.** Same as today (`egress_proxy.go` anti-pivot,
  RFC1918-blocked), the bastion forwards only to the registered device, never an
  arbitrary host.

## 4a. Multi-tenancy: why raw reverse-SSH is *wrong* for the shared public relay

Key question: *on the shared `public-free` relay, would one user's reverse-SSH
tunnel block others?* **Yes — raw reverse SSH is multi-tenant-hostile here, and
in exactly the way we're already being bitten.**

- **Reverse SSH binds a bastion port per tunnel.** `ssh -R 2222:localhost:18080`
  makes the bastion listen on `2222`. Two devices both wanting the "obvious" port
  collide; the bastion must **allocate and track a distinct port per device**,
  hand it back out-of-band, and firewall it. A **stale bound port blocks the
  reconnect** with "port already in use" — which is *literally the SSH-layer
  version of the `deviceId already registered` deadlock we see today*. So
  reverse-SSH doesn't remove our worst bug; it re-creates it as a port lease.
- **The QUIC relay does NOT bind per device.** Every device multiplexes over the
  **same** QUIC listener (`:4433`); routing is application-layer by **deviceId**
  (`public.yaver.io/d/<deviceId>/…`). A device's presence is a *registration
  keyed by deviceId*, not a port. Thousands of users/devices coexist on one
  listener; one device's tunnel cannot starve another's. **This is the correct
  multi-tenant design for a shared relay**, and it's why the fix is to make that
  registration robust (last-writer-wins eviction), not to swap in per-port SSH.
- **Therefore:** reverse-SSH (Road B) only makes sense for a **single-tenant /
  self-hosted** relay (one user, one box — port collisions are a non-issue), or
  if you rebuild deviceId-multiplexing on top of SSH (dynamic `-R 0` + a routing
  layer), which is just re-implementing the QUIC relay with more moving parts.
  For the shared `public-free` relay, **Road A (hardened QUIC relay) is strictly
  better** and reverse-SSH would be a multi-tenancy regression.

## 4b. Native iOS SSH — a persistent background CONTROL channel

User intent (2026-07-21): **keep the existing QUIC relay (and keep hardening it),
and ADD a native-SSH / reverse-SSH channel as a *redundant, out-of-band control
channel*** — always-available, secure, alive in the background. It is the
datacenter "out-of-band management" pattern: when the primary data path (QUIC
relay) breaks, the SSH channel is *still up*, so Yaver can **agentically
self-heal connectivity** — run the **remote runner / Yaver MCP over the SSH
channel to diagnose and fix the transport problem itself** — instead of the user
staring at "can't connect". It also carries **tasks** (multiplexed control), and
is the resilient liveness/eventing plane the way Yaver Mesh would be (Mesh is off
here because Tailscale occupies `100.64/10`). Strictly **additive**: the data
plane still rides the best transport; SSH is the redundant control/recovery plane.

**The headline use case — agentic connectivity self-heal:** primary path drops →
SSH control channel is still alive → Yaver runs `runner`/MCP verbs *over SSH* on
the box (`doctor transport`, `doctor relay`, re-register relay, restart tunnel,
inspect the agent) → fixes the data path → user's session resumes. The box is
never truly unreachable as long as ONE channel survives.

**Scope + coordination (least privilege):** the SSH self-heal loop may touch
**only** (a) the **remote box** (its own agent: restart tunnel, re-register,
`doctor`, pick another relay candidate) and (b) the **mobile app** (re-arm its
transport, refresh creds). It must **never** touch third-party infra. For fixes
the box *cannot* make itself, it reports **agentically to Convex** ("relay X looks
wedged / I re-registered / switch me to relay Y"), and **Convex** — the control
plane — is what talks to the **free relay / Relay Pro** to force-evict a stale
registration, rotate a relay credential, or move the device to another relay/tier.
This preserves the split: **agent = machine authority, Convex = control plane,
relay = data path**; the out-of-band channel just lets the agent *reason and act*
on connectivity even while the data path is down, and lets Convex broker the
relay-side half.

### Why a dedicated control channel earns its keep
- The HTTP-over-QUIC data path is request/response; on a flap it dies *silently*
  (the exact bug in §0/mobile). A persistent SSH channel with `ServerAliveInterval`
  gives **sub-10s liveness truth** and a **warm path** — so recovery is "the data
  path re-attaches to a box we already know is up," not a cold rediscovery race.
- It carries **control only**: `health`, status summary, task-done events,
  path-recovery signaling — tiny, latency-tolerant. **No bulk data, no media**
  over it (see §C: never tunnel WebRTC media through TCP SSH).

### iOS background reality (the honest constraint)
iOS suspends apps; a raw SSH socket will **not** stay open 24/7. So "background
persistent" is achieved as **logical** persistence, not a literally-open socket:
- **Foreground + short background grace:** the SSH channel is live with keepalive.
- **Suspended:** rely on **APNs silent/background push to wake** the app and
  re-establish the channel when there's something to deliver (task finished, box
  state changed) or when the user returns. The device registers for push at pair
  time; the agent (via control plane) sends a silent push to trigger reconnect.
- Net UX: to the user it feels always-connected (open app → already warm; event
  happens → push wakes it), within Apple's rules — no misleading "always-on"
  claim, no battery-hostile socket-thrash.

### Security model (purely secure — non-negotiable)
- **Key in the Secure Enclave.** The iOS device SSH private key is generated in
  and **never leaves** the Secure Enclave (P-256; or an Ed25519 key sealed in the
  Keychain with `kSecAttrAccessibleWhenUnlockedThisDeviceOnly` where SE curve
  limits apply). No key material in JS, in a backup, or on the relay.
- **Forced-command on the agent, no shell.** The phone's public key is installed
  as a `# yaver-managed` entry:
  `command="yaver ssh-session --session <id>",no-pty,no-agent-forwarding,`
  `no-user-rc,permitopen="127.0.0.1:18080"`. The channel can invoke **only** the
  Yaver control verbs — never a shell, never arbitrary forwarding. Raw shell stays
  an explicit owner-only debug escape hatch with an audit event.
- **Bound to Yaver identity.** SSH auth is gated by the device certificate +
  account session; the signed request envelope (§ canonical doc) still wraps every
  verb *inside* the channel — SSH is the pipe, Yaver identity is the authority.
- **E2E, relay/mesh pass-through.** The channel is end-to-end phone↔agent; the
  relay/bastion forwards ciphertext only, authorizes nothing, logs metadata only.
- **Per-device, instantly revocable.** Revoke device → remove its `yaver-managed`
  key + close its live channel + invalidate its session capability. Never touch
  non-Yaver keys.
- **Least privilege + limits.** Idle timeout, max lifetime, byte caps; control
  lane is priority but bounded so it can't be abused as a data tunnel.

### Completely invisible to the user
The user **feels and knows nothing about SSH** — no keys to manage, no config, no
`authorized_keys`, no "SSH" in the normal UI. It is auto-provisioned at pair time
(agent mints its own `# yaver-managed` key, installs its own forced-command entry),
runs silently as the out-of-band channel, and self-heals in the background. The
only place SSH is ever named is the **advanced/debug** transport panel
(`Path: … / out-of-band: healthy`) — never the normie flow. "It just works, and
it's secure" is the entire user-facing contract.

### "Yaver Mesh" is the SSH-wrapping abstraction
Framing (2026-07-21): **Yaver Mesh = the layer that wraps well-known SSH**. It is
not a bespoke protocol — it is the wrapper that, from a **Convex "hello" signaling
handshake** (who am I, what overlays do I have, is the peer reachable), picks:
- **shared overlay present** (Tailscale / VPN / Yaver's own WireGuard overlay) →
  **wrap native direct SSH** to the peer's overlay address, and
- **behind NAT / no shared infra on one or both sides** → **reverse SSH** (box
  dials out, held open at relay/Relay-Pro).
So the whole out-of-band channel *is* "Yaver Mesh wrapping SSH": one abstraction,
two flavors, chosen by signaling — the user never picks. (This unifies the older
WireGuard-overlay "Mesh" and the SSH transport under one selection rule.)

### Which SSH: native-direct vs reverse — decided by signaling
At init, **Convex signaling** determines reachability, then picks the SSH flavor:
- **Both sides on a shared overlay (Tailscale/VPN/Mesh) → native direct SSH** to
  the box's overlay address (`:22` or the agent's SSH port). Lowest latency, E2E,
  no bastion. This is the "both have Tailscale → go direct" case.
- **Not on a shared overlay → reverse SSH via the relay/bastion.** The box holds
  an outbound reverse tunnel (autossh-grade keepalive, generation-replacement);
  the phone reaches the forced-command endpoint through it. Works through NAT/CGNAT
  with no inbound ports.
The channel identity, forced-command, and Secure-Enclave key are **identical**
across both flavors — only the reachability leg differs, chosen by the signaled
path decision (same selector that picks LAN/VPN/relay for the data plane).

### Convex cost — cheap by construction (do not inflate the bill)
The SSH out-of-band channel is **P2P (phone↔box)**; its bytes never traverse
Convex, so it adds **no** Convex function load. Convex is touched only at:
- **pair/setup** — issue device cert + tunnel intent (one-time, per pairing),
- **rare self-heal events** — the box reports "relay wedged / re-registered /
  switch relay" only when a real fault is detected+acted on, **debounced** (one
  summary per fault, not per retry). No per-heartbeat writes, no polling loops.
Keepalive/liveness lives in the SSH channel (`ServerAliveInterval`), **not** in
Convex. This keeps the resilience layer off the metered plane — consistent with
the existing Convex-cost discipline.

**No high-frequency loops — even during troubleshooting.** The agentic self-heal
is **single-shot with bounded exponential backoff and a hard attempt cap**, and is
**event-driven** (triggered by an actual detected fault, not a timer). A relay
re-register is tried once, then backed off (seconds → tens of seconds → give up +
surface to the user), never a tight retry loop hammering Convex/relay/the box.
This is both a cost rule (metered Convex/relay) and an anti-abuse rule (never let
a datacenter box hammer infra). Troubleshooting that can't converge in a few
bounded attempts escalates to the user, it does not spin.

### Native module shape (build)
- `YaverSSHControl` TurboModule wrapping **libssh2** (or SwiftNIO SSH): generate/
  load SE key, connect (over the selected reachability leg — mesh/relay/direct),
  keepalive, `exec` forced-command verbs, one multiplexed channel for
  status+events. Exposed to RN as `sshControl.connect()/health()/onEvent()`.
- **Selector integration:** add `kind: "ssh-control"` to `MachineTransport`. It is
  a *liveness + eventing + recovery-trigger* transport, not the data path. When
  the data path errors, the selector checks the SSH channel: **alive → box is up,
  re-attach the data path immediately**; **also dead → full rediscovery**. This is
  what makes loss recovery *seamless*.
- **Mesh relation:** this control channel works **even when Yaver Mesh is off**
  (as it is here). When Mesh IS up, the SSH channel can ride the mesh overlay; when
  it isn't, it rides relay/direct. Same secure control plane, transport-agnostic.

### Task model it plugs into (existing)
A task runs in its own **tmux** session (`yaver-<task>`); a **yaver session**
wraps the remote **runner** (claude/codex/opencode). The SSH channel's `run-task`/
`attach-tmux`/`stop-task` verbs drive exactly these — SSH is another way to reach
the *same* tmux/runner, not a parallel task system. So "tasks over SSH" = the
forced-command verbs attaching to the existing tmux/runner session.

### Build order — grounded in existing code (after connectivity+UI perfected, before deploy)
Existing to build ON (do not duplicate): SSH resolution LAN→Tailscale→mesh→device
lives in `desktop/agent/ssh_resolve_lan.go` / `ssh_resolve_mesh_test.go` /
`ssh_targets.go`; bootstrap in `ssh_bootstrap.go`; `yaver ssh` in `launch_ssh.go`.
There is **no** forced-command verb server, no reverse-SSH-via-relay tunnel, no
mobile `MachineTransport` selector yet — those are the new work.

1. **Agent forced-command server** (`yaver ssh-session --session <id>`): a Go verb
   dispatcher exposing ONLY `health/run-task/attach-tmux/stop-task/list-projects/
   open-port/status` over the SSH channel's stdio, wrapping the same TaskManager +
   tmux the HTTP path uses. Owner-only raw shell behind an explicit flag + audit.
   Testable in Go without iOS.
2. **`# yaver-managed` key lifecycle** (Go): generate/install/rotate/revoke the
   device forced-command key in `authorized_keys`, never touching unknown keys
   (mirror the SSH-key-safety tests the canonical doc calls for).
3. **Reverse-SSH-via-relay backend** (agent dials relay/bastion, autossh-grade
   keepalive + generation-replacement) for the not-on-tailnet case; native-direct
   SSH over the resolved overlay for the on-tailnet case (reuse `ssh_resolve_*`).
4. **iOS `YaverSSHControl` TurboModule** (SE key, libssh2/SwiftNIO-SSH, keepalive,
   exec) — the native client. + APNs silent-push wake for background reconnect.
5. **Mobile `MachineTransport` selector**: add `ssh-control`/`ssh-task` kinds; use
   the channel for liveness truth + seamless data-path re-attach + agentic
   self-heal trigger (bounded, event-driven — never a loop).
6. **Doctor** `yaver doctor ssh-control`: proves the key can SIGN + the forced
   command answers `health` (real capability, not "key exists").

## 4c. Cross-surface parity (all surfaces, per CLAUDE.md)

The transport is **one behavior, propagated by two families** (same rule as the
rest of Yaver):

- **RN surfaces share the JS transport** — **mobile, tablet, car
  (`app/car-voice-coding.tsx`), glass/AR-VR (`app/glass-*.tsx`)** all consume the
  same `quic.ts` + `DeviceContext`. The connectivity fixes (follow-up timeout,
  conn-gate, tap timeouts) and the future `MachineTransport` selector +
  `ssh-control`/`ssh-task` kinds reach **all of them for free** — verify nothing
  is gated to one screen. **Android RN** is in this family too (shares the JS).
- **Native surfaces port explicitly** — **tvOS** (`tvos/YaverTV/AgentClient.swift`),
  **watchOS** (`watch/…/SessionClient.swift`), **Wear OS** (`wear/…/*.kt`). They
  do NOT inherit RN transport code; the same connection semantics + status
  vocabulary must be ported. For the *native SSH control channel* specifically:
  the **native iOS module** is the reference; **Android** gets a Kotlin
  equivalent; **watchOS/tvOS** are companion-first (rely on the paired phone's
  channel and the relay) rather than each embedding libssh2 — an SSH stack on a
  watch is not worth it, so those surfaces get the **hardened-relay + selector
  status**, and reach the box via the phone/companion when needed.
- **Web UI is relay-only** — the browser cannot open raw sockets/SSH (`web/lib/`).
  It benefits from the **server-side relay hardening** (the `already registered`
  eviction) automatically and shows the same transport status vocabulary, but has
  **no** out-of-band SSH channel by design; its resilience *is* the hardened relay.

So: one hardened-relay + one selector contract + one status vocabulary across
every surface; the native SSH out-of-band channel is phone/desktop-first (+ Wear
OS/Android native later), with watch/tv/web riding the companion + hardened relay.

## 4c-2. Two planes: Mesh-VPN (data) + SSH (control) — harden both
Yaver Mesh also has its **own VPN-like WireGuard overlay** — that is the **DATA
plane**: WebRTC media, Hermes bundle push, dev-server preview, bulk transfer ride
it (or direct/relay) when peers can join the overlay. The **SSH / reverse-SSH
channel is the CONTROL plane**: liveness, status, tasks, and self-heal. They are
complementary — the Mesh-VPN moves bulk/real-time data (never tunnel media over
TCP SSH, §C), the SSH channel is the out-of-band redundant control/recovery link
that survives when the data plane is down. **Harden both**: the overlay with ACLs
(same-user default, guest only for shared targets, no open subnet routing, route-
conflict doctor before bring-up), the SSH channel with §4d. Same multi-tenant
security bar applies to both — a tenant's overlay/tunnel never reaches another
tenant's peer.

### MCP self-heal over the redundant SSH channel
Yaver MCP connectivity problems are fixed *using the SSH channel itself*: when the
data plane (QUIC relay / Mesh-VPN) is down, MCP verbs route over the surviving SSH
control channel to run the **remote runner's diagnostics + repair** —
`doctor-transport`, `repair-relay`, re-register/restart tunnel — agentically,
bounded (no tight loops), then restore the data plane. So an MCP call that would
otherwise fail "can't reach the box" instead reaches it out-of-band and self-heals.
This is why `doctor-transport` + `repair-relay` are in the forced-command
whitelist (`sshSessionRoute`): the recovery verbs must be reachable precisely when
everything else isn't.

## 4d. Threat: a hostile relay user must NOT reach another user's box/phone

This is the paramount property, and it must hold **even though the code is open
source** (an attacker reads everything — security rests on **keys, not secret
request shapes**). Concretely, a free- or Pro-relay user (who therefore has relay
access and the source) must never get into anyone else's box or phone. Layered
defense, each layer sufficient on its own:

1. **Box authenticates the CLIENT's device key — public-key ONLY.** The embedded
   SSH server (`ssh_control_server.go`) has **no password / no keyboard-
   interactive** auth and accepts a connection **only** if the presented key is in
   that box's own `# yaver-managed` set (`authorizedManagedKeysChecker`, re-read
   per connection). An attacker can send bytes to the box's SSH port but **cannot
   authenticate** without the owner's device private key — which lives in the
   phone's **Secure Enclave** (never extractable) or 0600 local storage, and which
   the **relay never sees**. Reading the source does not yield a key.
2. **The relay is pass-through and access-graph-scoped.** It forwards **ciphertext**
   only, authorizes nothing, holds no device keys. It bridges a connection **only
   between the same owner/access-graph** (Convex says who may reach whom — same
   rule as the `already registered` eviction that validates userID). So a hostile
   tenant's bytes are not even routed to a stranger's box. A *fully compromised*
   relay still can't get in — layer 1 stops it (it has no key).
3. **Forced-command cage.** Even a valid device key can ONLY run the whitelisted
   verbs (`sshSessionRoute`) — **never a shell, pty, port-forward, or subsystem**
   (all refused in `handleSession`). Worst case for a stolen *owner* key is
   running Yaver verbs on that owner's OWN box, not a foothold on the OS.
4. **Host-key pinning (client side).** The client pins the box's persistent host
   key (`ensureSSHControlHostKey`), so a malicious relay can't MITM by swapping in
   its own box. (Test uses InsecureIgnoreHostKey; the native client MUST pin.)
5. **Instant revocation.** Revoke a device → its `# yaver-managed` key is removed
   and the per-connection check refuses it on the next handshake; live channel
   closed. A lost phone loses access immediately.
6. **Least privilege + no lateral movement.** No agent-forwarding (`no-agent-
   forwarding`) so a foothold can't hop onward; RFC1918-blocked anti-pivot on the
   relay stays in force; the channel touches only the box + the app, never third-
   party infra.

**Free vs Pro is NOT a security boundary** (per the canonical doc): both tiers use
the identical key/signature protocol and the identical box-side public-key auth.
Pro buys reliability/capacity, never a weaker auth path — a Pro relay is treated
as exactly as hostile as the free one. The box trusts **its owner's device keys**,
full stop — not the relay, not the tier, not Convex alone.

## 4e. Cloud Workspace VM compatibility (the normie path)

The dominant normie shape is **phone + a Yaver Cloud Workspace VM** (BYO
claude/codex or Yaver inference) — no Mac, no LAN. The transport must be
first-class for that, and it maps cleanly onto the same design:

- **Reachability:** a cloud VM and a phone share **no overlay** and the VM is
  behind the provider's NAT/firewall → `chooseSSHTransport(false)` →
  **reverse-SSH via relay** is the default path (the VM dials out, holds the
  tunnel; the phone reaches the forced-command endpoint through it). No inbound
  ports, no security-group holes to open.
- **Frictionless keys, provisioning-time:** the VM's device key + its
  `# yaver-managed` forced-command entry are **injected at provision** (Yaver-
  managed, scoped to the workspace's user, revocable without touching anything
  personal — the VM has no personal keys). Zero user action. Private material is
  injected from the control plane's secret store, **never baked into an image**
  (open-source images must be clean — §"No private data").
- **Scale-to-zero honored (Hetzner = metered).** The reverse tunnel must NOT be a
  reason to keep the VM alive: when the workspace has no active session it
  **sleeps/deletes** per provider semantics (state on a volume/snapshot), which
  closes the tunnel; on **wake** the VM re-establishes it via the bounded
  supervisor (`superviseReverseTunnel`) and Convex re-signals presence. A tunnel
  is not a heartbeat that burns compute — idle → tunnel down → VM parked.
- **Same security bar.** Multi-tenant isolation (§4d) applies identically: many
  users' workspaces share the relay; each VM authenticates only its owner's
  device keys; the relay bridges only within the workspace's access graph. A
  compromised relay cannot enter a stranger's workspace VM.
- **MCP self-heal fits the VM too:** when a workspace's data path flaps, the
  redundant SSH control channel runs the VM's runner diagnostics
  (`doctor-transport`, `repair-relay`) to recover — the same out-of-band self-heal,
  now on rented compute.

So the Cloud Workspace VM is just "the box behind NAT" case of the general design:
reverse-SSH-via-relay by default, provisioning-time managed keys, scale-to-zero
respected, identical multi-tenant security.

## 5. Recommendation

1. **Now (fixes the reported pain):** Road A — harden the QUIC relay to
   autossh-grade. In order of leverage: (a) **evict "already registered"**
   (last-writer-wins), (b) **keepalive + <1 s redial**, (c) **≥2 relays**,
   (d) **health-verified reachability**. Plus the **phone-on-tailnet → direct
   `:18443` HTTPS** fast-path. No iOS SSH client, reuses the current transport.
2. **In parallel (mobile UX, so failures aren't silent):** the app must treat
   "have credentials" ≠ "connected": fail fast (timeouts on
   `continueTask`/`forkTask`/`getProjectActions`), **surface** the drop, gate Send
   on real connection, and render **cached projects at-a-glance** instead of a
   scan-blocked empty state. (Separate change set; see the connectivity fix
   commit.)
3. **Later / if A stays fragile:** Road B — a reverse-SSH bastion backend, E2E-
   opaque, per-device forced-command keys. Adopt only if the hardened custom
   client can't hit the reliability bar; SSH's maturity is the fallback insurance,
   not the first move.

**Bottom line:** the user is right that a persistent, self-healing reverse tunnel
+ signaled path selection is the answer — but on iOS the phone stays an
HTTP-over-QUIC client; SSH's *discipline* (autossh keepalive/redial, no
"already-registered" black hole) is what we adopt, first by hardening the QUIC
relay (Road A), with reverse-SSH (Road B) as the proven fallback. Security is
E2E-opaque relay + per-device revocable least-privilege throughout.
