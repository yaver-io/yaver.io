# Connectivity & Mesh Audit — 2026-07-19

Why the phone cannot hold a connection to the Mac mini, on or off Tailscale,
and what Yaver Mesh is missing before it can be trusted the way a tailnet is.

Scope: the mobile connect ladder, the agent/relay leg, the Convex device
registry, and the WireGuard overlay. Every claim below is anchored to code read
on `main` at `b695a2a8c`. Where a doc and the code disagreed, the doc is noted
as the bug.

**Hard constraint carried through every recommendation:** the Mac mini is
reachable *only* over Tailscale. No fix may remove, shadow, or race a route in
`100.64.0.0/10` on that box. Section 4 is where this constraint is currently
violated.

---

## 0. The one-paragraph version

There is no single connectivity bug. There are **five independent faults**, and
each one alone is enough to produce "connecting… disconnected". They compound:
the stale-IP fault makes the probe storm worse, the probe storm trips the
relay's abuse limiter, and the limiter produces an auth error that the recovery
code misroutes into a no-op. Transport itself is mostly fine — when the relay is
healthy the phone reaches the mini in ~1s. The failure is that *nothing in the
ladder can tell a structurally impossible path from a transiently failing one*,
so it retries all of them, forever, at full rate.

| # | Fault | Where | Severity |
|---|---|---|---|
| 1 | Stale LAN IPs resurrected by a read-path union | `backend/convex/devices.ts:364` | **P0** — root of the storm |
| 2 | Impossible legs never negative-cached | `mobile/src/lib/directProbeFailure.ts:42` | **P0** |
| 3 | Relay auth conflates dead-token with bad-password | `backend/convex/userSettings.ts:712` | **P0** |
| 4 | Mesh can shadow Tailscale on 2 of 3 bring-up paths | `desktop/agent/mesh_http.go:27` | **P0 — safety** |
| 5 | `isConnected` is an unvalidated in-memory flag | `mobile/src/lib/quic.ts:1358` | P1 — the UI lie |

---

## 1. Fault 1 — stale LAN IPs are manufactured at query time

The log shows the phone racing nine dead addresses every attempt:
`192.168.111.{38,11,8}`, `192.168.1.105`, `172.20.10.{9,4}` (an iPhone hotspot
range), `10.0.0.{40,45}`, and `172.17.0.1` — a **Docker bridge**.

The instinct is to blame the agent or the heartbeat. Both are innocent:

- `getLocalIPs` (`desktop/agent/main.go:9763-9829`) enumerates only interfaces
  that are **currently UP**, drops loopback/link-local/public, and explicitly
  filters Docker bridges at `main.go:9820`. The agent can never report an IP
  from a network it has left.
- The heartbeat mutation **replaces**, it does not merge —
  `backend/convex/devices.ts:1110-1115`, whose comment already anticipates this
  exact bug: *"a delta-merge would strand stale addresses on the record
  forever."*

The accumulation happens on the **read** path. `collapseListedDevices`
(`devices.ts:396-443`) runs three collapse passes — identity, alias, endpoint —
and every one calls `mergeListedDevices`, which unions with no recency filter,
no cap, no TTL:

```ts
// backend/convex/devices.ts:364
localIps: [...new Set([...(a.localIps || []), ...(b.localIps || [])].filter(Boolean))],
```

So: one live Mac-mini row plus N stale duplicate rows for the same box collapse
by alias (`platform:hostname`, `devices.ts:428`) and the phone receives the
**union of every address any of those rows ever last-reported**. A dead row
never heartbeats, therefore never gets its `localIps` replaced, therefore
donates its frozen snapshot forever. That is also how a Docker bridge IP the
agent stopped emitting builds ago comes back from the dead.

Duplicate rows are a known, already-instrumented condition —
`devices.ts:806-834` is an entire helper about "duplicate rows for this
hardwareId".

**Fix direction:** stamp each IP with the `lastSeen` of the row that reported
it and drop anything older than a short window at merge time; cap the set; never
union across a row that is offline. The agent and the mutation need no change.

### 1b. Why this one fault causes the storm

`sameDeviceList` (`mobile/src/lib/deviceListEquality.ts:119`) compares `lanIps`
element-wise to decide whether the device array is unchanged. `lastSeen` was
deliberately excluded from that comparison to stop a 30s re-render metronome —
but `lanIps` was not, and `lanIps` is precisely the field the server-side union
makes unstable. **Any churn in the merged array re-identifies the list and
restarts every effect keyed on it**, which is the documented probe-storm
amplifier at `DeviceContext.tsx:1352-1369`.

---

## 2. Fault 2 — the ladder cannot learn that a path is impossible

`lan-tailscale 100.89.155.25:18080 failed — Network request failed` fails
*instantly*, not on timeout. That is the signature of an unroutable destination:
the phone is off the tailnet, so there is no route to `100.x` at all.

`describeDirectProbeFailure` (`mobile/src/lib/directProbeFailure.ts:26-42`)
classifies `blocked` only for ATS `-1022` cleartext refusals. A bare
`Network request failed` falls through every branch and returns the raw string
at `:42`. Consequences:

- It is **not** classified as `blocked`, so it is indistinguishable from a
  transient failure.
- There is **no negative cache**. The same four `100.x` legs are re-raced on
  every attempt, forever.
- The only gate on racing tailnet candidates is a *user preference*
  (`quic.ts:6137`, `allowTailnet`), and with no prefs configured
  `transportPolicy()` returns all-true (`quic.ts:6067-6076`). **Nothing ever
  asks the phone whether Tailscale is actually up.** The desktop resolver has
  exactly this gate (`localMeshUp()`, `main.go:8480`); the phone has no
  equivalent.

The in-code comment at `quic.ts:6053-6056` justifies letting it ride because
"the cost is bounded by the parallel race budget". That is true for wall-clock
and **false for socket pressure**: `quic.ts:6150` pushes every private candidate
twice (adding port 18080), so nine stale IPs become up to eighteen simultaneous
sockets, competing with the relay probe on iOS's ~6-connections-per-host pool.

Worse, the *gate in front of* connect is not bounded at all.
`probeMobileDeviceStatus` (`mobile/src/lib/deviceStatus.ts:213-278`) does a bare
`Promise.any` over every relay plus two legs per lanIp with **no phase
deadline** — it settles only when every leg rejects. `raceDirectCandidates` has
a 2800ms wall (`quic.ts:6204`); the probe that gates it does not.

**Fix direction:** classify instant-fail as `unroutable`; negative-cache it per
(network, candidate) keyed on the phone's current L3 identity; gate tailnet
candidates on observed tailnet membership rather than a preference; give
`probeMobileDeviceStatus` the same phase deadline the race already has; stop
double-pushing port 18080.

### 2b. A security consequence worth its own line

The session bearer token is attached to every private-IP direct leg
(`deviceStatus.ts:262-263` via `isCredentialSafeBase`) — including `172.17.0.1`
and stale `192.168.111.x`. On a network where some *other* host now occupies
that address, the phone sprays the user's bearer at it in cleartext. Staleness
is not just a latency bug here.

---

## 3. Fault 3 — "relay password missing" is usually not a password problem

Three different relay errors appear in the log and they have three different
causes.

**3a. `relay password missing — sign in again to fetch it.`** This is the
*client sending no password at all* (`relay/server.go:1616`), not a wrong one.
`fetchRelayServers` (`DeviceContext.tsx:2328-2339`) installs a **bare,
password-less** relay set when platform servers exist but no
`settingsRelayPassword` was fetched, and `withFreeRelayFallback`
(`DeviceContext.tsx:501-511`) injects `public-free` with `password: undefined`.

This then trips `relay/server.go:1621-1626`, which rate-limits invalid-auth
**per client IP** — and `DeviceContext.tsx:2251-2255` documents that this bans
the entire NAT. The uncapped concurrent ladders (fault 5b) are exactly the shape
that trips it.

**3b. The register-path conflation — the likely reason the mini goes dark.**
For `action === "register"`, `backend/convex/userSettings.ts:712-715` requires a
valid **session token** on top of the password:

```ts
if (action === "register") {
  if (!args.tokenHash) return null;
  const session = await validateSessionInternal(ctx, args.tokenHash);
  if (!session || session.user._id !== match.userId) return null;
```

So an expired agent session token returns the *identical* failure as a wrong
password. The relay reports both as `"invalid relay credentials (password or
session token)"` (`relay/server.go:955`). That string matches
`looksLikeStaleRelayPassword` (`main.go:11195-11204`) on `"password"` +
`"invalid"`, so the agent refetches the password, gets back the *same perfectly
valid* password, finds `fresh != password` false — and **does not even retry**.
It falls into backoff and loops forever on a token problem while logging a
password problem.

The mobile auto-heal cannot rescue this either: `repairRelayPassword` returns
`{ok:false, reason:"unauthorized"}` when the session is dead
(`userSettings.ts:629-632`).

**Fix direction:** return distinct error codes for bad-password vs
dead-token from `validateRelayPassword`; route the token case to
re-authentication (`yaver auth fix`), not to a password refetch; surface it as
"this box's session expired" rather than a relay error.

**3c. `device not connected to relay`** (`relay/server.go:1705-1713`) is the
refuse-on-collision window. `relay/server.go:962-997` rejects a second
registration for a `deviceID` that already has a tunnel until the old conn's
context fires — up to `MaxIdleTimeout` = 120s. `relay/tunnel_liveness.go:21-23`
states the failure mode outright: *"a zombie BLOCKS ITS OWN REPLACEMENT"*, and
its header comment describes **this exact box**:

> Observed 2026-07-14 on an always-on Mac mini — registered for over an hour,
> every request from the phone timing out, both ends reporting a healthy
> tunnel. It came back the instant the agent restarted.

The mitigations are real and worth crediting: `runRelayTunnel`
(`main.go:11064-11172`) is an infinite redial with 1s→60s backoff and jitter; a
zombie watchdog forces redial after 90s (`main.go:10754-10789`); the relay
probes each tunnel with a real `GET /health` every 30s and evicts after two
failures (`relay/tunnel_liveness.go:52-81`). A dropped relay connection does
**not** stay dropped until restart. But none of that closes the ~2-minute
self-blocking window.

---

## 4. Fault 4 — Yaver Mesh can shadow Tailscale. This is the safety finding.

### 4a. The range overlap is real and the code already admits it

`desktop/agent/mesh/device.go:56` — `MeshSubnetCIDR = "100.96.0.0/12"`, which is
**inside** `TailscaleCGNATCIDR = "100.64.0.0/10"` (`:62`). The comment block at
`:33-55` is an explicit retraction of the old claim that it was outside.

**The correction never propagated.** `backend/convex/mesh.ts:35` — the allocator
that actually hands out addresses — still carries the false claim
*"Deliberately OUTSIDE Tailscale's 100.64.0.0/10"*. Per the repo's own rule,
that doc-in-code is the bug and should be fixed in the same change.

### 4b. What mesh does to the routing table

Exactly one route, ever. macOS (`mesh/netconfig_darwin.go:48-54`):

```go
runCmd("route", "-q", "-n", "add", "-inet", "-net", meshCIDR, "-interface", name)
```

**Teardown is safe.** There is no `route delete`, no `ip route del`, no
interface destroy anywhere in `desktop/agent/mesh/` — verified across every
`runCmd` call site. Tailscale's route is never removed by Yaver. macOS DNS
interference is scoped to `/etc/resolver/mesh` and cleaned by
`CleanStaleMeshArtifacts` (`mesh/netconfig.go:44`).

**Shadowing is not safe.** `/12` is a longer prefix than `/10`, so BSD
longest-prefix-match makes mesh win for `100.96.0.0–100.111.255.255` — one
sixteenth of the tailnet. Any Tailscale peer whose address lands in that band
becomes unreachable while mesh is up. Not a full outage; a silent partial one.
Tailscale's address assignment is not partitioned to avoid that band.

### 4c. The guard exists and is wired to one of three paths

`mesh_http.go:181` does the right thing:

```go
if conflict, cErr := mesh.SubnetRouteConflict(ifaceName); cErr == nil && conflict != nil {
    return conflict.Reason()
}
```

Grep for `SubnetRouteConflict` across the repo returns **exactly this one
non-test call site**. Therefore:

| Bring-up path | Guarded? | Reachable from |
|---|---|---|
| `autoEnableMesh` — default-on boot | ✅ yes (`mesh_http.go:158`) | agent start |
| `handleMeshUp` — `POST /mesh/up` | ❌ **no** (`mesh_http.go:27-89`) | CLI **and the phone** |
| `meshConvergeDesired` | ❌ **no** (`mesh_agent.go:366-369`) | console toggle, every 30s |

So boot on the mini is safe — with Tailscale up, `SubnetRouteConflict` finds
`utun4` in 100.64-space and mesh never starts. But **one tap of "enable mesh on
all machines" from the phone** (`mobile/src/lib/meshControl.ts`,
`mobile/app/(tabs)/mesh.tsx`) installs `100.96/12` on the mini regardless of
Tailscale, and a console-driven desired-state change re-enters `Start()` with no
check even if boot correctly deferred.

**Assessment against the hard constraint:** the "never disrupt the mini's
Tailscale" invariant holds on the automatic path and is broken on the two
manual/remote paths. Remote access itself very likely survives — the mini's own
tailnet address and its outbound sessions are unaffected unless that address sits
in 100.96–100.111 — but that is **address-assignment luck, not a guarantee from
the code**. Routing the guard into all three paths is the single highest-value
mesh change and must land before anything else touches mesh.

### 4d. Mesh has no mobile data plane at all

`mobile/src/lib/yaverMesh.ts` resolves to `{ supported: false }` until a native
extension ships. The native sources exist (`mobile/native-mesh/ios/…`,
`…/android/…`) and the config plugin exists (`mobile/plugins/withMeshTunnel.js`)
— but **`withMeshTunnel` is absent from the `plugins` array in
`mobile/app.json:107-181`**. The phone has no mesh data plane in any shipped
build.

Yet `quic.ts:6135` still builds `lan-mesh` candidates and gates them on the
*Tailscale* preference flag (`:6137`), with no local-mesh-up check. Every
phone→box attempt races a 100.96 candidate that cannot possibly succeed — fault
2, again.

### 4e. Stability gap vs Tailscale

The user's bar is "as stable as regular Tailscale, with or without Tailscale
underneath". Measured against that:

| Mechanism | Tailscale | Yaver Mesh |
|---|---|---|
| Relay fallback | DERP, ~30 regions, sub-second failover | single relay, loopback shim, **20s grace** (`manager.go:75`) |
| Endpoint discovery | continuous netmap + disco, many candidates/peer | **one-shot STUN at join, never refreshed** |
| NAT traversal | port prediction, simultaneous open, UPnP/PMP/PCP | STUN mapped-address only, **assumes port preservation** (`stun.go:36`) |
| Keepalive | adaptive | fixed 25s (`mesh_agent.go:94`) — fine |
| Route conflict | refuses overlapping routes | guard exists, **1 of 3 paths** |
| MTU | probes and clamps | **hardcoded 1420** (`device.go:24`), no probing |
| Link change / sleep-wake | `netmon`, full re-STUN on wake | **nothing** |
| Control plane | long-poll push | 20s/30s polls |

The **single largest stability defect is the absent periodic endpoint refresh.**
`meshRegisterJoin` is called from only three places, and the one in
`mesh_agent.go:371` sits inside an `if !changed { return }` guard at `:353`. A
node that roams Wi-Fi→cellular keeps advertising a dead public endpoint
indefinitely; peers dial it until the 20s DERP fallover drags them onto the
relay, and only if `derp != nil`.

Second: `reconcileLoop` (`manager.go:290-330`) never re-invokes
`ConfigureNetwork` and never verifies the interface still holds its address or
that the route still exists. `m.running` is set once and cleared only by an
explicit `Stop()`. There is no data-plane liveness probe; `Status()` reports
handshake counters that nothing consumes to trigger repair.

Third, directly relevant to "with Tailscale underlying it": MTU is fixed at 1420
with no path-MTU probing or clamping. **Nested inside another tunnel — exactly
the mesh-over-Tailscale case — this produces silent large-packet loss with no
code path that notices.**

### 4f. Mesh: implemented vs aspirational

Real and wired: the WireGuard data plane, Convex peer reconcile, endpoint-roam
preservation (`manager.go:101`), DERP fallover, inbound ACLs with fail-closed
boot, `.mesh` MagicDNS, the overlay HTTP listener, and rung 2.5 of the desktop
connect ladder (`main.go:8479-8486`).

Not functional despite appearances: **subnet routes and exit node**. They are
advertised, validated, filtered, tested, and pushed into WireGuard `AllowedIPs`
— but `ConfigureNetwork` only ever routes `MeshSubnetCIDR`, and `AllowedIPs` is
cryptokey routing, which decides *which peer decrypts a packet that already
reached the TUN*. Nothing steers host traffic. The `--tailscale` bridge
(`mesh_cmd.go:577`) is aspirational end-to-end. Also non-functional: macOS
exit-node NAT, Windows forwarding, Linux MagicDNS, BSD entirely.

A related UI divergence: `web/components/dashboard/NetworkView.tsx:67` offers a
one-click bridge advertising the **raw /10**, which every peer silently drops via
`filterAdvertisedRoutes` (`mesh_agent.go:146`). The CLI gets this right
(`mesh_cmd.go:67` advertises the split pair `100.64/11` + `100.112/12`).
`web/lib/transport.ts:73` also labels mesh addresses as `"tailscale"` — the
operator diagnosing an outage is told the wrong transport is in play.

---

## 5. Fault 5 — the UI asserts things it does not know

This is the same lesson the 2026-07-18 incident already recorded, recurring.

**5a. `[connect-resume] Already connected` while every leg fails.**
`DeviceContext.tsx:1453-1464` short-circuits on `client.isConnected`, which is a
pure in-memory flag (`quic.ts:1358-1360`) set once after a single `/health` 200
and **never revalidated before the resume claim**. It can be stale by 15–40s:
the heartbeat runs every 15s and requires two consecutive failures before
erroring (`quic.ts:6830-6842`), and poll failures are deliberately silent
(`quic.ts:6765-6768`). Compounding it, the failing legs in the log may belong to
a *different* `QuicClient` — `appLog` output from the race carries no device id.

**5b. Unbounded concurrent ladders.** The pool is explicitly uncapped
(`connectionManager.ts:24-27`). Each `QuicClient` owns its own backoff with no
global scheduler and no shared budget (`quic.ts:6674`, `:6707-6712`). The
pool-warmer (`DeviceContext.tsx:3750-3786`) starts a ladder for every online
device and re-fires on `connectedDeviceIds` churn that it causes itself. A
Wi-Fi/VPN flap resets *every* client's backoff to the bottom rung
(`quic.ts:6652-6671`).

**5c. The repair rung is missing from the path that needs it most.**

| Path | Repair rung? |
|---|---|
| Manual switch modal (`RemoteBoxPickerModal.tsx:627`) | ✅ |
| Auto-connect sweep (`DeviceContext.tsx:3606-3614`) | ✅ |
| **`quic.ts scheduleReconnect` → `attemptConnect`** | ❌ **none** |
| web | ❌ none (still) |

During a reconnect ladder `lastError` is set to `Reconnecting (n/max)...`
(`DeviceContext.tsx:1999`), which matches **none** of the strings the self-heal
effect greps for (`:2437-2440`) — so the self-heal can never fire mid-ladder,
which is exactly when it is needed.

---

## 6. Ordered remediation

Ordered by (root-cause depth × blast radius), not by effort.

1. **Route `SubnetRouteConflict` into `handleMeshUp` and
   `meshConvergeDesired`.** Safety first — this is the only finding that can
   cost remote access to the mini. Fix `backend/convex/mesh.ts:35`'s false
   comment in the same change.
2. **Stop the read-path union of `localIps`** (`devices.ts:364`): recency-stamp,
   cap, never union from offline rows. Kills fault 1 and, with it, the
   `sameDeviceList` churn that drives the storm.
3. **Negative-cache unroutable legs** and gate tailnet/mesh candidates on
   observed membership rather than a preference. Classify instant-fail as
   `unroutable` in `directProbeFailure.ts`.
4. **Split relay auth errors**: bad-password vs dead-session-token, end to end
   (`userSettings.ts:712` → `relay/server.go:955` → `main.go:11195`). Route the
   token case to re-auth.
5. **Bound `probeMobileDeviceStatus`** with the phase deadline the race already
   has; stop double-pushing port 18080.
6. **Revalidate before claiming connected**; add the repair rung to
   `scheduleReconnect`; make `lastError` during a ladder carry the underlying
   cause instead of masking it.
7. **Mesh stability**: periodic endpoint re-registration (largest single win),
   data-plane liveness in `reconcileLoop`, link-change/wake hooks, MTU probing —
   the last one is required before mesh-over-Tailscale can work at all.
8. **Register `withMeshTunnel` in `app.json`** or stop building `lan-mesh`
   candidates on the phone. Today it is neither.

## 7. What would have told us this in ten seconds

Per the standing rule, the diagnosis goes where the agent already looks:

- A `doctor` probe that **attempts** a relay register and reports which of
  {password, session-token, collision} failed — the current error text cannot
  distinguish them, and that ambiguity cost this whole investigation.
- A `doctor` probe that reads the host routing table and reports any
  longer-prefix route shadowing an active `100.64/10` — the mesh/Tailscale
  conflict is invisible until a peer goes unreachable.
- Per-candidate age in the connection log: `lan-heartbeat 192.168.111.38
  (last reported 14d ago)` makes fault 1 self-evident from the phone.
- A device-row duplicate count surfaced in the UI; the helper already computes
  it (`devices.ts:806-834`) and only logs it.

---

*Audited 2026-07-19 against `b695a2a8c`. Findings are code-anchored; if a line
reference has drifted, trust the code and correct this file in the same change.*
