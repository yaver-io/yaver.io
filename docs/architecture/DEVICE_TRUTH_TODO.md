# Device Truth — remaining work (handoff)

**For:** a fresh thread picking this up. You do not need the original session.
**Companion:** `docs/architecture/DEVICE_TRUTH.md` — the full audit, the model,
and findings F1–F31. Read §1, §2 and §6 of it before writing code. This file is
only *what is left*.

**Done already** — commit `f9e2835f2`, do not redo:
F1, F2, F3, F4, F14, F15, F20, F21 (web only). Phase 1 of DEVICE_TRUTH.md §5.

⚠️ **Line numbers in DEVICE_TRUTH.md were accurate on 2026-07-18 and have
already drifted** — Phase 1 edited `web/lib/device-lifecycle.ts`,
`probe-backoff.ts`, `use-devices.ts`, `app/dashboard/page.tsx`, and
`components/dashboard/DevicesView.tsx`. **Re-grep for the symbol, never trust the
line number.** Everything outside `web/` is untouched and its refs should still
land.

---

## The one rule

> **Never state a capability you have not verified. State the evidence you have,
> and how old it is.**

A Convex heartbeat proves the agent made an **outbound** call within 15 minutes.
It proves nothing about whether *this client* can reach it. Every remaining task
below is an instance of that distinction not being made somewhere.

---

## Use the existing model — do not invent a second one

`web/lib/device-lifecycle.ts` is the web canon. Its shape is what the other
surfaces should mirror (in their own language — no shared build exists):

```ts
type BrowserReachState = "reachable" | "claimed" | "unreachable" | "offline";

deriveDeviceLifecycleState(device)      // is the AGENT alive?
deriveBrowserReach(device, lastFailure) // can WE get to it?
deviceStatusLabel(lifecycle, reach)     // the one honest status string
deviceCtaLabel(lifecycle, reach)        // { label, confident, title }
canBrowserActOnDevice(lifecycle, reach) // may we attempt at all?
```

The invariant, asserted in `web/lib/device-lifecycle.test.ts`: **`confident ⇒
verified`.** If you add a surface or a CTA, add it to that test's `cases` array.

`mobile/src/lib/deviceStatus.ts` is the mobile canon and is in some ways better
than web's — `probeMobileDeviceStatus` returns machine-readable failure codes
(`relay-credentials-missing`, `no-transport`) instead of prose. Prefer its
approach when adding probes.

---

## Task 1 — render the bit we already compute (highest value, do this first)

**Nothing new needs to be built. The honest signal already ships on every
heartbeat and every listing surface throws it away.**

```go
// desktop/agent/auth.go  (grep: relayConnected)
payload["relayConnected"] = relayDataPathUsable()
```

It is gated on whether the tunnel can still *carry* a request, it is in
`backend/convex/schema.ts` (grep `relayConnected`), the schema comment states its
purpose verbatim, and it already reaches the Go CLI struct (`main.go`, grep
`RelayConnected`). Only `machine_doctor` reads it.

**Do:** render `online · no relay path` when `IsOnline && !RelayConnected` in
- `yaver devices` — grep `runDevices`, the `STATUS` column
- `yaver status` — grep `runStatus`, the machine table
- `yaver primary` show/pick — grep `runPrimaryShow`, `runPrimaryPick`
- MCP `yaver_devices` — grep `yaver_devices` in `httpserver.go`
- MCP `agent_machine_inventory` — grep `agent_machine_inventory`

**Acceptance:** a box with a fresh heartbeat and a dead relay tunnel prints a
status distinguishable from a fully healthy one, on all five. No protocol change,
no new field, no migration.

**Related, same pass:**
- **F17** — MCP `yaver_ping` (grep `yaver_ping` in `httpserver.go` /
  `mcp_tools.go`) takes **no arguments** and echoes the **local** agent process.
  An LLM reading the name believes it verified a remote device. Give it a target
  and probe it, or rename it to `yaver_self_check`.
- **F23** — `HEARTBEAT_STALE_MS` is 15 min, hand-duplicated in
  `backend/convex/devices.ts`, `mobile/src/_core/constants.ts`,
  `web/lib/use-devices.ts`. Three comments and one live computation still assume
  **5 min**: grep `5 \* time.Minute` in `ops_machine_doctor.go` (it sets
  `Fresh: false` for devices the backend calls online), plus stale comments in
  `primary_cmd.go` and `remote_status_cmd.go`.

---

## Task 2 — web failure UX (Phase 2 of DEVICE_TRUTH.md §5)

The connect path is correct-ish and unbearable. All refs in `web/`.

**F9 — the good error copy exists and is unused.** `web/lib/connection-error.ts`
classifies 14 reasons with `label` / `detail` / `suggestedAction`, including the
exact "your QUIC tunnel isn't established, restart with `yaver serve`" sentence.
The workspace connect panel (grep `connectError` in `app/dashboard/page.tsx`)
hand-rolls a worse classification, so relay 401 and relay 502 render an identical
headline. `ConnectivityView.tsx` already uses it correctly — copy that.
*Acceptance:* 401, 502, timeout, and mixed-content each produce a different
headline and a different suggested action.

**F8 — no cancel, up to ~3.5 min.** Timeouts are sequential (8s × relays, 8s ×
tunnels, 5s direct — grep `attemptConnect` in `lib/agent-client.ts`), plus
`ownerClaimDevice` at 12s **per target** which can run *before* the first probe,
plus a full second pass. During `connecting` the UI renders a spinner and the
hardcoded string "Trying relay servers"; `Retry`/`Back` exist only in the error
branch. *Acceptance:* a Cancel button during `connecting`; the panel names the
path being tried; aborting actually stops the in-flight fetch (needs an
`AbortSignal` threaded through — `fetchWithTimeout` currently owns a private
controller).

**F10 — the reconnect ladder flickers unprompted.** 8 attempts over ~4-5 min
(grep `scheduleReconnect`), each flipping state back to `"connecting"` with no
user action, so the panel oscillates. Attempt 9 stops permanently and silently.
Non-first failures swallow the throw (`if (isFirstAttempt) throw err;`), so the
on-screen diagnostics are frozen at the first failure while the real ones change
underneath. *Acceptance:* "retrying in 8s · attempt 3 of 8", live diagnostics,
and an explicit "gave up — Retry" terminal state.

**F7 — `RemoteDesktopModal` still has the bug the shell modal fixed.** No
`failed` state, no stall timeout, no `connectionState` subscription, and an
unconditional confident green "Connect & open desktop". Its header comment claims
it "mirrors WebShellModal's connection gating" — **delete that comment as part of
the fix.** Port `WebShellModal.tsx`'s `failed` state, `CONNECT_STALL_MS`,
`useAgentConnectionState`, `useBrowserReach`, and diagnostics list verbatim.

**F11 — `TerminalView` dead-black-rectangle.** `await
agentClient.terminalWsUrl(cwd)` sits in an un-`try`'d async IIFE; a throw during
session-token issuance (401/403/502, or "not connected") kills it before
`new WebSocket`, no setter runs, `status` stays `"connecting"`, and the error
overlay never renders. Everything after the socket opens is fine. *Acceptance:*
wrap it; render the failure with the same overlay `onclose` uses.

**F18 — silent no-op buttons.** `ProjectsView.tsx` (grep `reloadDevServer`) and
two call sites in `VibeCodingView.tsx` fire `void agentClient.reloadDevServer(…)`
with no `.catch` and no gate. Clicking reload on an unreachable box does nothing
and says nothing.

**F29 — Remote Desktop shows green for a stream with no frames.** The status dot
tracks *URL minting*, not frame arrival, and there is no frame watchdog (the
terminal has a 60s one — copy it). `/rd/status` is fetched but `supported` and
`allowRemoteControl` are never rendered, so a box reporting "control not allowed"
still gets the full control UI.

---

## Task 3 — native surfaces (Phase 4)

These share no code with web or RN. Each needs its own port.

**tvOS — worst offender, do first.**
- **F5.** `tvos/YaverTV/MachineRegistry.swift` → `RegisteredDevice` is
  `Decodable` and does **not declare `needsAuth`, `peerState`, or
  `lastTunnelEvent`**, so they are silently dropped from `/devices/list`. A
  signed-out box renders green **"Online"** and is one tap from guaranteed
  failure. Add the fields, then handle them.
- **F6.** `MachinePickerView.swift` → `connect()` does
  `firstReachable(...) ?? candidates.first` — when every probe fails it selects
  an address it just proved dead. This is the magara bug in Swift. Fail with a
  reason instead.
- **F25.** The liveness predicate is written out **three times**
  (`MachinePickerView.swift` ×2, `YaverStore.swift` ×1). Collapse to one, the way
  `web/lib/device-lifecycle.ts` did.
- Also: `nowMs` is captured once at load, so a picker left open renders "Online"
  against an hour-old clock; there is no refresh affordance outside the error
  state; and no last-seen age is ever shown.
- Note tvOS's `BoxLifecycle.swift` **already** classifies auth-expiry correctly
  during a wake — reuse it for the list rather than writing new logic.

**Wear OS — F16.** `wear/.../BoxLifecycle.kt`: in phone-paired mode with no box
URL it sets `READY` after 90s and re-fires the pending turn with **zero**
reachability confirmation (the comment concedes this). Both Swift surfaces refuse
to advance without a real 200. Match them. Note Wear's `probeHealth` is the
**strictest** classifier in the repo — the problem is only that it is called from
`startWake` and nowhere else.

**watchOS.** No device list exists; a single hand-typed host, with reachability
discovered only after a turn fails. Whether it *should* have a list is a product
decision (DEVICE_TRUTH.md §7.3) — ask before building one.

---

## Task 4 — the RN parity lie (Phase 4)

CLAUDE.md says RN surfaces share `DeviceContext`/`AuthContext` so a fix reaches
mobile, tablet, car, and glass for free. **Grep says otherwise:**
`car-voice-coding.tsx`, `glass-terminal.tsx`, and `glass-workspace.tsx` import
**nothing** from `mobile/src/lib/deviceStatus.ts`. They render a raw
`d.online ? "online" : "offline"` ternary and tap straight through with no probe.
`TaskTargetWizard.tsx` is the same.

**Do:** extract the honest row logic that currently lives *inside*
`RemoteBoxPickerModal.tsx` — `staleOnline`, `noRelayPath`, `needsSignIn`,
`Down · last seen …` — up into `deviceStatus.ts`, then adopt it in
`car-voice-coding.tsx`, `glass-terminal.tsx`, and `TaskTargetWizard.tsx`. That
extraction is what makes "shared code ⇒ shared fix" true instead of aspirational.

**F24, quick win, same area:** `car-voice-coding.tsx` → `isDeviceAsleep` reads
`d.isOnline`, but the `Device` type declares `online`; the mismatch is suppressed
by an `as any`. So `undefined === false` is always false, the LAN branch never
fires, and an offline WoL-capable box shows "offline" instead of "asleep · tap to
wake" with the Wake button never rendering. `DeviceContext.tsx` already does it
right — grep `d.isOnline ?? d.online ?? false` and copy.

**Do NOT "fix" this by hiding devices.** `mobile/src/lib/devicePicker.ts` ranks
rather than filters, deliberately, because hiding offline+needsAuth boxes is what
made a rebooted Mac mini vanish from the picker. Its comment names the incident.
Honest labels, never omission.

---

## Task 5 — trust and polish (Phase 5)

**F22 — the dashboard can be a year stale with no indication.** `/dashboard` is
statically prerendered and served `cache-control: s-maxage=31536000`. The HTML
shell references hashed chunks that still exist, so a warm cache serves an
arbitrarily old dashboard forever. There is no build-version check anywhere.
*Observed live:* v1.1.159 rendering after 1.1.162 shipped — i.e. **every fix in
this document can be invisible to a user with a warm cache.** Add a build-id
check + "a new version is available, reload" prompt. Consider whether
`/dashboard` should be prerendered at all.

**F19 — guest scopes are never read.** `full` / `feedback-only` / `sdk-project`
exist in `web/lib/guests.ts`, but `DevicesView` gates on the coarse boolean
`!device.isGuest`. Shell, "Coding agent…", Open SSH, and Copy SSH command have
**no guest check at all** — a `feedback-only` guest is offered a PTY and an SSH
command on the host's box. Whether the daemon rejects it is beside the point; the
surface promises it.

**F30 — a queued agent update is never confirmed.** `requestedVersion` from
`requestAgentUpdateViaConvex` is discarded and has no consumer in the web UI, so
"never asked" and "asked three days ago, nothing happened" look identical. Add a
"update pending since …" state.

**F26 — managed chips read two feeds for one fact.** `⏸ PAUSED` comes from
`/subscription`, `🌙 ASLEEP` from the devices feed, and the `/subscription` poll
**stops permanently** once nothing is pending (no `else` branch reschedules). A
box paused from another tab shows stale chips forever. One feed, one fact.

**F27 — up to 14 chips on one card.** A paused box says "⏸ Paused" + "🌙 Asleep"
+ "Offline" (one fact, three ways) and offers "▶ Wake" **and** "▶ Resume box" —
two buttons firing the identical POST. A BYO box prints "Self-hosted" + "BYO", in
code whose comment claims that duplication was already fixed.

**F28 — `(— unavailable)` on Git projects** collapses "probe failed", "in backoff,
didn't ask", and "offline, never asked". Its explanatory `BackoffHint` renders
only when a classified error exists — i.e. never in the backoff case that causes
it.

**F31 — jargon as user-facing status.** Phase 1 fixed "Bootstrap" → "Needs
pairing" and "Yaver Auth Expired" → "Signed out". Still to go: "Dedicated
Session", "Bootstrap (reclaim rotating)", "Bus saw this machine recently",
"Private LAN", "WSL2 NAT", raw `shared-scoped`/`shared-legacy`, and
`yaver auth factory-reset` offered as **first-run** advice. Rewrite table is in
DEVICE_TRUTH.md §6.

---

## Ground rules for whoever does this

From CLAUDE.md, and all four were learned the hard way *in this repo, this week*:

1. **Many sessions run concurrently.** Work on a branch, `git commit -- <paths>`
   always (never `-a`, never `add -A`), then rebase and merge.
2. **`git diff <file>` before committing it.** Pathspec commits stop foreign
   *files* being swept in, but **not another session's edits inside a file you
   are also editing**. That is how `main` was broken on 2026-07-18: a commit
   picked up a sibling's `import WakeProgress` whose component was untracked.
3. **`npm run build` in `web/`, never `tsc --noEmit` alone.** tsc resolves
   untracked files CI cannot see; that produced a false green and a wasted
   release run the same day.
4. **Committed work is immutable.** If something here is wrong, land a new commit
   that fixes it — never revert or `checkout --` someone else's work.

Deploys are metered. Batch these into as few `release-web.yml` runs as the work
allows; do not deploy to check something.

## Verification expected

Each task lands with a check that fails before and passes after:
- Pure derivations → extend `web/lib/device-lifecycle.test.ts` (13 tests today).
  New CTA or surface ⇒ add it to the `confident ⇒ verified` case list.
- Cross-surface work → assert **one** fixture device shape (fresh heartbeat +
  every transport dead — magara's) through every surface's derivation.
  Divergence is the bug.
- UI work → drive the real dashboard against an unreachable device and confirm no
  element claims verified reachability. Phase 1 was unit-verified but **the
  end-to-end click against a live unreachable box was never driven** — that gap
  is still open and worth closing early.
