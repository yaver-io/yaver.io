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
