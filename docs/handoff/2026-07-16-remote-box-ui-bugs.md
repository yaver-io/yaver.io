# Remote Box / device-picker UI bugs — 2026-07-16

Triaged live from device screenshots. Each item has **severity**, **where**
(file:line), **symptom**, **root cause**, and a **fix**. Copy any block into a
Codex task as-is.

Key diagnostic fact used throughout: the Mac mini's agent was probed directly
over Tailscale (`http://100.89.155.25:18080/health`) and reports
`{"lifecycleState":"ready-to-connect","usable":true,"version":"1.99.302"}` — so
the boxes are healthy and signed in. **The failures below are phone-side
(transport + rendering), not the boxes.**

---

## 1. 🔴 Every box shows "No reachable transport (tried relay + direct)"

- **Where:** phone connect path — `mobile/src/context/DeviceContext.tsx`
  (relay resolution `fetchRelayServers`/`resolveRelayServers`),
  `mobile/src/lib/quic.ts`, `mobile/src/lib/connectionCache.ts`.
- **Symptom:** Mac mini, magara, simkab, ubuntu — **all** boxes, including ones
  tagged **LIVE** — show "No reachable transport (tried relay + direct)". The
  phone cannot reach anything.
- **Root cause (most likely):** the phone holds **no usable relay password**, so
  every relay request 401s and no tunnel is ever established. This is the exact
  class root-caused in `docs/handoff-connectivity-2026-07-13.md` §1.1
  ("password-less relay fallback → 401 forever"). The agent-side + no-fallback
  fix landed on `main` (`e179367d7`) but only reaches the phone via a **build** —
  the on-device build likely predates it. Secondary factor: agent QUIC `:4433`
  is closed/filtered even over Tailscale, so the direct path can't substitute.
- **Fix:**
  1. Verify on the phone whether `/settings` returns a `relayPassword`; if it's
     empty, that's the bug — never fall back to the password-less `/config` list
     (assert with `scripts/test-relay-path.mjs`).
  2. Ship a fresh mobile build carrying the `main` relay fixes.
  3. When the phone genuinely has no relay credential, the row must say
     **"Sign in again to fetch relay credentials"**, not a generic "no transport".
- **Note:** boxes on the same LAN as the phone should also try mDNS/direct; a
  box marked `LAN-only (no relay path)` (simkab, ubuntu) is honest — those have
  no relay tunnel and are only reachable on-LAN.

## 2. 🔴 Managed box wakes but agent is signed out → dead-end "Try wake again"

- **Where:** `mobile/src/components/RemoteBoxPickerModal.tsx:194`
  (`{failed ? "Try wake again" : "⏻ Wake"}`).
- **Symptom:** `mn777j15.cloud.yaver.io` wake fails with *"The box came back up but
  its Yaver agent is signed out, so it could never register. Parked again to stop
  the meter — re-authorize it, then wake."* The only action is **Try wake again**,
  which re-parks and fails again — an infinite loop with no way out.
- **Root cause:** the failure is a **yaver-level auth** problem (agent signed
  out), but the UI offers only a wake retry. There is no in-app path to
  re-authorize a remote box.
- **Fix:** when the wake-failure reason is "signed out / could never register"
  (or the box's `/health` `lifecycleState` is `yaver-auth-expired`/`bootstrap`),
  replace/augment "Try wake again" with **"Sign this machine in"** that launches
  the **remote OAuth (device-code) flow** for that box (the same
  `backend/convex/deviceCode.ts` flow as `yaver auth --headless`), then wakes.
  This is the user's explicit ask: *"if the issue is yaver-level OAuth, guide the
  UI into Yaver remote OAuth for the Mac mini etc."*

## 3. 🟠 "Couldn't switch" screen is generic — doesn't branch on lifecycle

- **Where:** switch/ping flow — `mobile/src/context/DeviceContext.tsx`,
  `mobile/src/lib/deviceStatus.ts` (`probeMobileDeviceStatus`), the "Switching"
  screen that renders "Couldn't reach … no transport answered".
- **Symptom:** the same "no transport" text regardless of *why* it failed.
- **Root cause:** the agent already reports `lifecycleState` in `/health`
  (`ready-to-connect` / `bootstrap` / `yaver-auth-expired`), but the switch
  failure UI ignores it.
- **Fix:** on switch failure, probe `/health` and branch:
  - `yaver-auth-expired` → "This machine needs to sign in" → remote OAuth (see #2).
  - `bootstrap` → "Reclaim this machine" (owner-claim / pair).
  - `ready-to-connect` (this case) → "Signed in, but no live relay tunnel —
    restart its agent or check its network." **Not** an OAuth prompt.

## 4. 🟠 Wake progress bar starts ~85% full ("Agent online…") while still booting

- **Where:** `mobile/src/lib/wakeMachineCore.ts:52-62` (`PHASE_META` percents),
  phase derivation in `mobile/src/lib/wakeMachine.ts:167-199`,
  `mobile/src/components/WakeProgress.tsx`.
- **Symptom:** the WAKING bar is ~85% full almost immediately with "Agent
  online…", when the box is really still booting.
- **Root cause:** two things —
  (a) `PHASE_META` jumps `booting:52 → registering:80 → online:94`, so as soon as
  the phase advances past booting the bar leaps to 80–94%;
  (b) the phase is derived from a **premature "online" heartbeat** — a managed box
  heartbeats `online` the instant the agent process starts, before it's actually
  reachable/usable ("reachable ≠ usable"), so the derivation lands on
  `registering`/`online` (80–94%) while the box is genuinely still in `booting`.
- **Fix:**
  1. Do **not** map a bare `online` heartbeat to the `online`/`registering`
     phase until the agent is actually **reachable** (a successful probe), not
     just heartbeating. Keep it in `booting` (with in-phase creep) until then.
  2. Consider flattening the early curve (e.g. booting ≈ 40, registering ≈ 65)
     so booting — the ~8-min long pole (`PHASE_TYPICAL_MS.booting = 480_000`) —
     doesn't visually complete in seconds.

## 5. 🟠 No timer / ETA during wake — "user just waits"

- **Where:** `mobile/src/components/WakeProgress.tsx` (+ `elapsedInPhaseMs` and
  `PHASE_TYPICAL_MS` already exist in `wakeMachineCore.ts`).
- **Symptom:** the wake bar shows no elapsed time or ETA; the user stares at it
  with no sense of how long is left (booting alone is ~8 min).
- **Fix:** surface an **elapsed timer** ("Waking · 1:24") and a soft ETA from
  `PHASE_TYPICAL_MS` ("~8 min for a big disk"). The data is already computed
  (`elapsedInPhaseMs`, `stallHint`) — it just isn't shown as a clock.

## 6. 🟡 Box state flickers between "Online · tap to select" and "STALE · may be unreachable"

- **Where:** row state derivation in `mobile/src/components/RemoteBoxPickerModal.tsx`
  (`staleOnline` / `reachableNow` / `device.online` around :786-806) +
  `mobile/src/lib/deviceStatus.ts`.
- **Symptom:** the *same* box (e.g. Mac mini, magara) shows "Online · tap to
  select" in one refresh and "Last seen 2m ago · may be unreachable · STALE"
  seconds later — the badges (LIVE/STALE) and the subtitle disagree and jump.
- **Root cause:** the row mixes a Convex `online` heartbeat flag, a heartbeat
  freshness window, and a live probe result, and different refreshes weight them
  differently, so the derived label oscillates.
- **Fix:** derive one canonical row state (prefer the live probe when present,
  else heartbeat-freshness) and debounce it so a single missed poll doesn't flip
  LIVE→STALE. Align the corner badge (LIVE/STALE) with the subtitle so they never
  contradict.

## 7. 🟡 Remove Hermes-reload prerequisite text from the device list

- **Where:** `mobile/src/components/RemoteBoxPickerModal.tsx:913-921`
  ("Checking Hermes reload prerequisites…" / "Hermes reload ready" /
  "Hermes reload prerequisites missing").
- **Symptom:** every box row carries "Hermes reload prerequisites missing" — noise
  in a picker whose job is "pick a machine to connect."
- **Fix (user request):** don't surface Hermes status in the device/box list at
  all. Move it to the Hot-Reload tab / project context where it's relevant.

## 8. 🟡 Secondary device has no badge (Primary does)

- **Where:** `mobile/src/components/RemoteBoxPickerModal.tsx:807` (`roleLabel`)
  — sort already knows `secondaryDeviceId` (:227), the label only renders Primary.
- **Symptom:** the Primary box shows a "Primary" pill; the secondary box shows
  nothing.
- **Fix (user request):** render a **"Secondary"** badge (same styling as
  Primary) when `device.id === secondaryDeviceId`. Only when a secondary is set.

## 9. 🟢 "Last seen last seen 2m ago" — duplicated words — ✅ FIXED

- **Where:** `mobile/src/components/RemoteBoxPickerModal.tsx:799`.
- **Cause:** template prepended `"Last seen "` to `lastSeenLabel()`, which already
  returns `"last seen 2m ago"`.
- **Fix (done):** capitalize the label instead of prefixing —
  `${lastSeenLabel(ts).replace(/^last seen/, "Last seen")} · may be unreachable`.

## 10. 🟢 `·` shown literally in "Yaver Mesh" subtitle — ✅ FIXED

- **Where:** `mobile/app/(tabs)/more.tsx:3278`.
- **Cause:** `·` written as **bare JSX text**; JSX does not process `\u`
  escapes in text nodes, so it rendered the literal characters. (Line 1828's
  `·` is inside a `{}` JS string and renders correctly — left untouched.)
- **Fix (done):** replaced with the real `·` character in the JSX text.

## 11. 🟢 More menu — surface core/bootstrapped features better

- **Where:** `mobile/app/(tabs)/more.tsx`.
- **Ask (user):** make the More menu lead with the bootstrapped **core** features
  (Devices, Pair, Connection/Network, Hot Reload, MCP) rather than burying them
  under a long flat list; the "CORE" section exists but scrolls far down.
- **Fix:** promote the CORE group to the top (after START HERE), group the rest
  (AV/Home, Publish/Stores, Dev-tools) under clear headers, and trim one-liner
  subtitles so they don't truncate.

---

## Quick-wins already applied (uncommitted, working tree)
- #9 last-seen duplication — `RemoteBoxPickerModal.tsx:799`
- #10 `·` literal — `more.tsx:3278`

## Suggested Codex split
- **Codex A (rendering/strings, low risk):** #7, #8, #11 (+ verify #9/#10).
- **Codex B (wake UX):** #4, #5 (WakeProgress timer + honest curve).
- **Codex C (connectivity/auth, higher risk, needs a build):** #1, #2, #3, #6.
