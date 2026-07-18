# Device Truth — what "online" means, and what every surface may claim

**Status:** audit + proposed model. Nothing here is implemented yet except where
marked ✅. Written 2026-07-18 against `main` @ 20420b30b.

**Why this file exists.** The Devices list is the first screen a Yaver user sees
after sign-in. Today it can tell a user a machine is "Ready to Connect" when
every possible path to that machine is dead, offer three buttons that cannot
succeed, and then hang for three and a half minutes with no cancel button. This
document establishes what each surface is *allowed to claim*, and what evidence
it must hold to claim it.

The rule this document exists to enforce:

> **Never state a capability you have not verified. State the evidence you have,
> and how old it is.**

---

## 1. The source of truth, precisely

`online` is a stored boolean AND a freshness check, resolved server-side:

```ts
// backend/convex/devices.ts:176-180
function deriveIsOnline(d: { isOnline: boolean; lastHeartbeat: number }): boolean {
  if (!d.isOnline) return false;
  return (Date.now() - d.lastHeartbeat) < HEARTBEAT_STALE_MS;
}
```

`HEARTBEAT_STALE_MS = 900_000` — **15 minutes** (`devices.ts:112`).

### What `online: true` actually proves

> The agent was able to make an **outbound** call to Convex at some point in the
> last 15 minutes.

That is all. It says nothing about whether *you* — from this browser, phone,
watch, or TV — can reach it. The two are different questions and the entire
class of bugs in this document comes from conflating them.

### How stale `online: true` can be

| Contributor | Worst case | Source |
|---|---|---|
| Staleness window | 15 min | `devices.ts:112` |
| Presence-write coalescing — a presence-only beat inside the bucket is **skipped entirely**, so `lastHeartbeat` lags a *healthy* box | +8 min | `devices.ts:1170-1196`, `HEARTBEAT_WRITE_BUCKET_MS` @ `:123` |
| Idle heartbeat cadence (10 min idle / 2 min with runners) — one missed idle beat leaves 5 min of margin | — | `desktop/agent/main.go:9898-9905` |
| `markOffline` runs on graceful shutdown **only** — SIGKILL, power cut, lid close, wifi drop never downgrade the flag | — | `devices.ts:2052`, and `devices.ts:93-96` says so outright |

**A relay tunnel-up event alone sets `isOnline: true` and back-dates
`lastHeartbeat`** (`devices.ts:1434` `presenceUpdate`, patch at `:1470`), with
zero agent participation. If that tunnel dies without a clean disconnect and the
agent is also gone, the device reads online for 15 minutes on the strength of a
TCP connect that no longer exists.

### Three ways `online: true` is honest and still useless

1. **Pure lag** — the box died 30 seconds ago.
2. **Outbound-only NAT** — cellular, CGNAT, hotel wifi. Heartbeats fine, no LAN
   path, no relay tunnel, no inbound port. Permanently online, permanently
   unreachable.
3. **Dead tunnel** — the agent talks to Convex but its QUIC tunnel to the relay
   is down, so the browser's only path 502s. **This is the `magara` case.**

### ⚠️ The honest bit already exists and nobody renders it

`relayConnected` is computed by the agent on **every** heartbeat, specifically
gated on whether the tunnel can still *carry* a request:

```go
// desktop/agent/auth.go:1775
payload["relayConnected"] = relayDataPathUsable()
```

It is in the schema (`backend/convex/schema.ts:412`) and the schema comment
states its purpose verbatim — so clients can show *"online · no relay path"*
instead of *"online"* that 502s. It reaches the Go CLI struct
(`main.go:9316 RelayConnected`).

**Every listing surface drops it.** The only consumer is `machine_doctor`
(`ops_machine_doctor.go`). Five CLI/MCP surfaces could print `online · no relay
path` **today, with no protocol change.**

This is the single highest-leverage finding in this document.

---

## 2. The model

Replace the binary `online | offline` with four states plus an age. Every
surface renders from this and nothing else.

| State | Meaning | Evidence required | CTA |
|---|---|---|---|
| `reachable` | We completed a request to the agent | Successful probe, with timestamp | **Open Workspace** (confident) |
| `claimed` | Convex says alive; *we* have not proven a path | Fresh heartbeat, no probe result | **Connect** (neutral) — never "Open" |
| `unreachable` | We tried and failed | Failed probe, with reason + timestamp | **Retry** + reason + fix |
| `offline` | No recent heartbeat | Stale/absent heartbeat | **Try anyway** (diagnostic) |

Two invariants:

- **`claimed` is not `reachable`.** Absence of evidence is not evidence of
  reachability. This is the bug that survived three fix attempts: web's
  `deriveBrowserReach` downgrades only on *positive failure evidence*, so an
  unprobed dead box defaults to "Ready to Connect".
- **Every state carries an age**, and the age is rendered. "Reachable · checked
  8s ago" is a true statement; "Ready to Connect" is a promise.

### Why not just probe everything?

Because probing every card on every render hammers relays and burns the user's
own bandwidth, and this repo's rules forbid loud loops against shared infra. The
model resolves the tension: **probe lazily, but never let an unprobed device
render as verified.** `claimed` is the honest resting state and costs nothing.

---

## 3. Conformance matrix

What each surface claims today vs. what it has verified.

| Surface | Reads | Probes? | Separates alive from reachable? | Can show false "ready"? |
|---|---|---|---|---|
| **web** Devices card | heartbeat + local probe registry | only when card **expanded** | partly ✅ (`deriveBrowserReach`) | **YES** — default when unprobed |
| **mobile** `RemoteBoxPickerModal` | heartbeat + live probe | ✅ before commit | ✅ **best on any surface** | no |
| **mobile** devices tab | heartbeat + probe | ✅ | ✅ | no |
| **mobile** `TaskTargetWizard` | `device.online` | no | ✗ | **YES** |
| **CarPlay** `car-voice-coding` | `device.online` | no | ✗ | **YES** |
| **glass / AR-VR** `glass-terminal` | `device.online` | no | ✗ | **YES** |
| **tvOS** `MachinePickerView` | `isOnline` + `lastHeartbeat` | on connect only, result **discarded on failure** | ✗ | **YES** |
| **watchOS** | single typed host | reactive, after failure | ✗ (no list exists) | n/a |
| **Wear OS** | single `boxUrl` | reactive; **auto-READY after 90s unconfirmed** | ✗ (no list exists) | n/a |
| **CLI** `yaver devices` / `status` | `IsOnline` | probes, but **only for the RUNNERS column** | ✗ | **YES** |
| **CLI** `ping` / `primary ping` / `primary status` | probe | ✅ | ✅ | no |
| **MCP** `yaver_devices` | `IsOnline` | no | ✗ | **YES** |
| **MCP** `agent_machine_inventory` | `IsOnline` | no | ✗ | **YES** |
| **MCP** `machine_doctor` | both | ✅ | ✅ **reference implementation** | no |
| **MCP** `primary_status` | both | ✅ | ✅ | no |

**The good implementations already exist** — `machine_doctor`,
`primary_status`, `yaver primary status`, and mobile's `RemoteBoxPickerModal`.
This is not a design problem. It is a propagation problem.

---

## 4. Findings

### P0 — actively misleads on the first screen

**F1. `use-devices.ts` has no loading state and no error state.**
Returns `{ devices, refreshDevices }` only (`:169-172`); failures are swallowed
(`:503` `if (!res.ok) return;`, `:631-633` bare `catch`). So `devices === []`
means *any* of: loading, network down, backend 500, token rejected, or truly
zero devices — and all five render **"No devices registered. Install the Yaver
CLI and run `yaver auth`."** A user whose backend is erroring is told their
machines do not exist and instructed to reinstall. 30s poll ⇒ up to 30s of it.

**F2. "Ready to Connect" is the default, not a conclusion.**
`deriveBrowserReach` (`web/lib/device-lifecycle.ts:139-157`) downgrades only on
evidence; the `/info` probe that produces evidence mounts **only when a card is
expanded** (`DevicesView.tsx:4736`). A collapsed card on a dead box shows blue
"Ready to Connect" + indigo "Open Workspace" forever.

**F3. The unreachable downgrade flaps back.** ⚠️ *introduced by me, this session*
Failure window is 90s (`device-lifecycle.ts:110`); probe backoff caps at 120s
(`probe-backoff.ts:74`). From failure #6 the record expires **before** the next
probe re-records it, so a box that failed six consecutive times cycles back to
"Ready to Connect". The comment at `:106-109` claims the window was chosen to
prevent exactly this. The arithmetic does not hold. **Window must exceed
`MAX_DELAY_MS`.**

**F4. `displayDevices` manufactures freshness.**
`page.tsx:1868-1893` force-overwrites `probeState:"ok"`, `online:true`,
`lastSeen: new Date()` for the connecting device, and `workspaceLive` includes
`"connecting"`. During every background reconnect tick a *failing* box is
stamped online and just-seen, overriding real probe data.

**F5. tvOS drops `needsAuth` at the decoder.**
`RegisteredDevice` (`MachineRegistry.swift:20-43`) is `Decodable` and does not
declare `needsAuth`, `peerState`, or `lastTunnelEvent` — silently discarded from
the API response. A signed-out box renders green **"Online"** and is one tap
from guaranteed failure.

**F6. tvOS connects to an address it just proved dead.**
```swift
// MachinePickerView.swift:176-186
let host = await MachineRegistry.firstReachable(...) ?? candidates.first
```
When every probe fails it selects the first candidate anyway. The `magara` bug,
verbatim, in Swift.

**F7. `RemoteDesktopModal` reproduces the shell-modal hang.**
No `failed` state, no stall timeout, no `connectionState` subscription, and an
unconditional confident green "Connect & open desktop" (`:117-122`). A failed
connect says "Connecting…" forever. Its own header comment (`:5-6`) claims it
"mirrors WebShellModal's connection gating." It does not.

### P1 — false hope and dead ends

**F8. No cancel, up to ~3.5 min.** Sequential timeouts (8s × relays, 8s ×
tunnels, 5s direct), plus `ownerClaimDevice` at 12s **per target** which can run
*before* the first probe, plus a full second pass. During `connecting` the UI
renders only a spinner and the hardcoded string "Trying relay servers"
(`page.tsx:2432`). `Retry`/`Back` exist only in the error branch — unreachable
while it hangs.

**F9. The good error copy exists and is not used.**
`web/lib/connection-error.ts` classifies 14 reasons with
label/detail/suggestedAction, including the exact "your QUIC tunnel isn't
established, restart with `yaver serve`" sentence. The workspace connect panel
does not import it; it hand-rolls a worse classification, so relay 401 and relay
502 render an identical headline. `ConnectivityView` one tab away uses it
correctly.

**F10. Background reconnect makes the UI flicker unprompted.** 8 attempts over
~4-5 min, each flipping state to `"connecting"` with no user action. Attempt 9
stops permanently and silently. Non-first failures swallow the throw
(`agent-client.ts:3822`), so the on-screen diagnostics are frozen at the *first*
failure while the real ones change underneath.

**F11. `TerminalView` dead-black-rectangle.** `await agentClient.terminalWsUrl()`
sits in an un-`try`'d async IIFE (`:94-203`); a throw during session-token
issuance kills it before `new WebSocket`, no setter runs, `status` stays
`"connecting"`, the error overlay never renders. Everything after the socket
opens is handled well.

**F12. Runner chips are green on nothing.** `classify` maps `status: ""` →
`health: "ready"` (`DevicesView.tsx:456-460`); shared/guest runners are inserted
with `{}` and classify ready. Never gated on reachability. An offline box shows
"Preferred · claude ✓ ready" and an enabled Test button.

**F13. Transport chips assert untested paths.** TAILSCALE / PRIVATE LAN /
PUBLIC IP / PUBLIC ENDPOINT are regex over Convex metadata with **zero network
I/O** (`web/lib/transport.ts:111-228`). PUBLIC ENDPOINT does not apply the
`isUsablePublicEndpoint` filter the probes themselves use to skip known-dead
`{uuid}.yaver.io` subdomains. PRIVATE LAN advertises an `http://` URL that is
mixed-content-blocked before it leaves the browser.

**F14. Refresh refreshes one of six data sources.** One `GET /devices/list`. Does
not reset probe backoff, clear failures, re-probe, or refresh managed-machine
state; swallows all errors, shows no spinner. Clicking it with an expired token
is visually identical to success. **There is no "as of" timestamp anywhere on
the page.**

**F15. Attention-needed devices sort last.** The only sort key is `online`
(`use-devices.ts:622`), and every device needing action (`needsAuth`, expired,
bootstrap, asleep) is `online === false`. Order is also **frozen** once
membership stabilizes (`:604-615`), so a device that breaks keeps its old
position. Sidebar slices to 10 (`page.tsx:2206`) — a broken box at #11 is
invisible in both places.

**F16. Wear OS advances to READY unconfirmed.** In phone-paired mode with no box
URL, after 90s it sets READY and re-fires the pending turn with zero reachability
confirmation (`BoxLifecycle.kt:193-199`). Both Swift surfaces refuse to advance
without a real 200.

**F17. MCP `yaver_ping` probes nothing remote.** It echoes the **local** agent
process and its schema takes no arguments (`mcp_tools.go:1003-1009`,
`httpserver.go:7207`). An LLM reading the name reasonably believes it verified a
remote device.

**F18. Silent no-op buttons.** `ProjectsView.tsx:300`,
`VibeCodingView.tsx:1518,1997` fire `void agentClient.reloadDevServer(...)` — no
`.catch`, no gate. Clicking reload on an unreachable box does nothing, silently.

**F19. Guest scopes are never read.** `full` / `feedback-only` / `sdk-project`
exist but `DevicesView` gates on the coarse `!device.isGuest`. Shell, "Coding
agent…", Open SSH, Copy SSH have **no guest check at all** — a `feedback-only`
guest is offered a PTY and an SSH command on the host's box.

**F20. Survey redirect fires on the pre-fetch state.** `page.tsx:931` keys on
`devices.length === 0`, which is also the initial value — an existing user can be
bounced to `/survey` before the first fetch lands.

**F21. Hidden-devices dead end.** If every remaining device is dormant, the list
takes the empty branch, but the "N hidden / Show all" recovery renders only in
the *non-empty* branch (`DevicesView.tsx:3065-3074`). The user sees "No devices
registered" with no way back.

**F22. Stale UI with no staleness signal.** `/dashboard` is statically
prerendered and served `cache-control: s-maxage=31536000` — a **one-year**
shared-cache TTL. The HTML shell references hashed chunks that still exist, so a
warm cache serves an arbitrarily old dashboard. There is no build-version check
anywhere. *(Observed live: v1.1.159 rendering after 1.1.162 shipped.)*

### P2 — correctness and consistency

- **F23.** `HEARTBEAT_STALE_MS` is 15 min but duplicated by hand in three places
  (`devices.ts:112`, `mobile/_core/constants.ts`, `web/lib/use-devices.ts`), and
  three comments plus one live computation still assume **5 min** —
  `primary_cmd.go:816`, `remote_status_cmd.go:729`, and
  `ops_machine_doctor.go:336` (`h.Fresh = age < 5*time.Minute`), which reports
  `Fresh: false` for devices the backend calls online.
- **F24.** CarPlay `isDeviceAsleep` reads `d.isOnline`; the type declares
  `online`. Suppressed by `as any` (`car-voice-coding.tsx:152`), so
  `undefined === false` is always false — the Wake button never renders for
  non-managed boxes. `DeviceContext.tsx:1243` already does it right with
  `d.isOnline ?? d.online ?? false`.
- **F25.** Three verbatim copies of the tvOS liveness predicate
  (`MachinePickerView.swift:117`, `:329`, `YaverStore.swift:152`); two of
  `isDormantUnreachableDevice` (`page.tsx:193`, `DevicesView.tsx:361`).
- **F26.** Managed chips read two different feeds for one fact — `⏸ PAUSED` from
  `/subscription`, `🌙 ASLEEP` from the devices feed — and the `/subscription`
  poll **stops permanently** once nothing is pending (`:2585`, no `else`). A box
  paused elsewhere shows stale chips forever; Refresh does not touch them.
- **F27.** Up to **14 chips** on one card. A paused box says "⏸ Paused" +
  "🌙 Asleep" + "Offline" (one fact, three ways) and offers "▶ Wake" **and**
  "▶ Resume box" — two buttons, one identical POST. A BYO box prints
  "Self-hosted" + "BYO", in code whose comment claims that was fixed.
- **F28.** `(— unavailable)` on Git projects collapses "probe failed", "in
  backoff, didn't ask", and "offline, never asked". Its explanatory
  `BackoffHint` renders only when a classified error exists — i.e. **never in
  the backoff case that causes it**.
- **F29.** Remote Desktop's green dot tracks *URL minting*, not frames — a stream
  that delivers nothing shows green over black, with no frame watchdog (the
  terminal has one). `/rd/status` `supported` and `allowRemoteControl` are
  fetched and never rendered, so a box reporting "control not allowed" still gets
  the full control UI.
- **F30.** A queued update is never confirmed. `requestedVersion` from Convex is
  discarded and has no consumer in the web UI; "never asked" and "asked three
  days ago, nothing happened" look identical.
- **F31.** Jargon as user-facing status: "Dedicated Session", "Bootstrap",
  "Yaver Auth Expired", "Bootstrap (reclaim rotating)", "Bus saw this machine
  recently", "Private LAN", "WSL2 NAT", raw `shared-scoped`, and
  `yaver auth factory-reset` offered as **first-run** advice (`page.tsx:2638`).

---

## 5. Fix plan

Ordered so each phase is shippable and verifiable alone.

### Phase 1 — stop lying (web, no new infra)
1. F1: add `loading` / `error` to `use-devices.ts`; render three distinct states.
2. F3: raise the failure window above `MAX_DELAY_MS` (120s → window 180s), or
   derive one from the other so they cannot drift.
3. F4: delete the `online/probeState/lastSeen` overwrite in `displayDevices`.
4. F2: introduce `claimed` as distinct from `reachable`; unprobed ⇒ `claimed` ⇒
   neutral **"Connect"**, never indigo "Open Workspace".
5. F14: Refresh re-probes and resets backoff; add a spinner, an error, and an
   **"as of HH:MM:SS"** stamp.
6. F15: sort attention-needed to the top; unfreeze on state change.

### Phase 2 — failure UX (web)
7. F9: route the connect panel through `connection-error.ts`.
8. F8: add Cancel during `connecting`; show which path is being tried.
9. F10: surface the backoff ladder honestly ("retrying in 8s · attempt 3 of 8")
   and say when it has given up.
10. F7: port the shell modal's `failed` state to `RemoteDesktopModal`; delete the
    comment that claims it is already there.
11. F11: wrap the `terminalWsUrl` await; render the failure.
12. F18: gate or `.catch` the three reload buttons.

### Phase 3 — render the bit we already have (all surfaces)
13. F-relay: render `relayConnected` as **"online · no relay path"** in
    `yaver devices`, `yaver status`, `yaver primary` show/pick, MCP
    `yaver_devices`, MCP `agent_machine_inventory`. No protocol change needed.
14. F17: make MCP `yaver_ping` take a target and probe it, or rename it.
15. F23: one shared `HEARTBEAT_STALE_MS`; fix the three 5-min assumptions.

### Phase 4 — cross-surface parity
16. F5/F6: tvOS — decode `needsAuth`/`peerState`/`lastTunnelEvent`; never select
    a failed candidate; one shared liveness predicate; add refresh; unfreeze the
    clock.
17. F16: Wear — require a real 200 before READY.
18. Extract mobile's `RemoteBoxPickerModal` row logic (`staleOnline`,
    `noRelayPath`, `needsSignIn`, `Down · last seen`) **into `deviceStatus.ts`**,
    then adopt it in car, glass, and `TaskTargetWizard`. This is what makes
    "shared code ⇒ shared fix" actually true.
19. F24: fix the `isOnline`/`online` mismatch in the car path.

### Phase 5 — trust and polish
20. F22: build-version check + "a new version is available, reload" prompt.
21. F19: read guest scope; hide or disable CTAs the scope forbids.
22. F20/F21: fix the survey redirect; make "Show all" reachable from empty.
23. F27/F31: collapse redundant chips; rewrite jargon per §6.

---

## 6. Copy rules

| Don't | Do | Why |
|---|---|---|
| "Ready to Connect" (unverified) | "Last seen 2m ago · not verified from here" | States evidence, not a promise |
| "Online" | "Online · no relay path" when `relayConnected === false` | The bit already exists |
| "Open Workspace" (unverified) | "Connect" | "Open" implies it will open |
| "Could not reach agent (direct, tunnel, or relay)" | "Relay tunnel is down — restart the agent with `yaver serve`" | `connection-error.ts` already writes this |
| "(— unavailable)" | "Couldn't check (retrying in 42s)" / "Not checked — device offline" | Distinguish the three |
| "Bootstrap" | "Needs pairing" | Internal enum |
| "Yaver Auth Expired" | "Signed out — run `yaver auth` on the box" | Say the fix |
| "Bus saw this machine recently" | "Seen 4m ago, but no working connection" | "Bus" is an internal |
| "No devices registered" (on error) | "Couldn't load your devices — retry" | Never blame the user for our 500 |
| `yaver auth factory-reset` as first-run advice | `yaver auth` | Destructive command, wrong audience |

---

## 7. Decisions needed

1. **Probe budget.** Auto-probe collapsed cards on the Devices tab, or keep
   `claimed` as the resting state and probe only on expand/hover/click? Probing
   N cards every 30s is real relay load. *Recommendation: keep `claimed`, probe
   on expand and on Refresh, never on a timer.*
2. **`claimed` CTA wording** — "Connect" vs "Connect (unverified)". *Recommendation:
   plain "Connect"; put the evidence in the status line, not the button.*
3. **Do watchOS and Wear OS get real device lists?** Today they hold one typed
   host. That is a product decision, not a bug fix.
4. **Is `relayConnected === false` enough to render `unreachable`**, or only to
   demote `claimed`? It proves the *relay* path is dead, not LAN/tunnel.
   *Recommendation: demote, don't condemn — a LAN-local user may still reach it.*

---

## 8. Verification

Per the repo rule that markdown drifts and code is the truth, each fix lands with
a check that fails before and passes after:

- **F1/F2/F3/F15** — pure functions; unit-test `deriveBrowserReach`,
  `deviceStatusLabel`, `canBrowserActOnDevice`, and the sort against fixture
  device shapes including `magara`'s (fresh heartbeat + failing probe).
- **F3 specifically** — an assertion that the failure window **exceeds**
  `MAX_DELAY_MS`, so the two constants cannot drift apart again.
- **F4/F14** — drive the real dashboard against a device with a dead tunnel and
  confirm no card claims verified reachability.
- **Cross-surface** — one fixture device shape, asserted through every surface's
  derivation: mobile, web, tvOS, Wear, CLI, MCP. Divergence is the bug.
- **Web builds** — `npm run build` in `web/`, never `tsc --noEmit` alone; tsc
  resolves untracked files CI cannot see and has already produced one false green
  this week.

---

## 9. Provenance

Audited 2026-07-18 across seven parallel passes: web device-card claims, web
connect path, web shell/RD/update modals, first-run and empty states, native
tvOS/watchOS/WearOS, CLI + MCP + the Convex heartbeat definition, and the
React-Native family (mobile/tablet/car/glass). Every `file:line` above was read,
not inferred. Line numbers drift — re-grep before acting on any of them, and if
this file disagrees with the code, **the file is the bug**.
