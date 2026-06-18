# Handoff — Device-Pairing Activation Onboarding (2026-06-01)

Branch: `feat/device-pairing-onboarding` (created off `main`).
Decisions locked by kivanc: **soft nudge + prominent skip**, **build all three phases**.

> Why this exists: a parallel session's Bash/Read output started corrupting mid-build
> (greps repeating one line 300×, full-file reads injecting unrelated text, scrambled
> line numbers). Rather than edit the working beacon/pairing code blind, the audit +
> plan are captured here so a clean session executes it safely. **Re-grep every anchor
> below before editing — verify line numbers, they may have drifted.**

## Problem (from prod Convex data, 2026-05-31)

Last 4 organic signups — Leslie (Manager @ Y Combinator, 2026-05-21), Aris, Ahmere,
John — each: mobile OAuth → survey complete → **never paired a device**. The April
cohort that activated (Serhat, Henry, Harshit, Batıkan) came via kivanc's network and
already knew the CLI. Funnel leak is exactly: `mobile sign-in → [WALL] → pairing`.
Net-new organic activations across that wall: **0**.

Leslie's timeline confirms it was NOT an auth failure: authIdentities + session both
created at 19:35:13 (56 ms apart), survey done 19:35:44, `emailVerified:true`, zero
securityEvents. Clean signup, dropped at the pairing step. Git log 05-18→05-24 shows
no auth/sign-in regression (all managed-cloud/billing/vault work).

## Root cause (confirmed in code)

1. Post-survey jumps straight to Tasks — no pairing step. `mobile/app/index.tsx:14-18`
   and `mobile/app/survey.tsx` `completeSurveyFlow` (`router.replace("/(tabs)/tasks")`).
2. Tasks zero-device empty state leads with **"Open Mobile Sandbox"** (primary button,
   routes to `/phone-projects`) and demotes pairing to two lines of gray inline text
   (`npm install -g yaver-cli` / `yaver auth`). `mobile/app/(tabs)/tasks.tsx:3880-3912`.
3. Real pairing UI (`SetupInstructions`, LAN auto-discovery/adopt) lives in the Devices
   tab — which was moved OUT of the bottom bar into More. New users never see it.
   `mobile/app/(tabs)/devices.tsx:842-898`, `mobile/app/(tabs)/_layout.tsx:~340`.
4. Plumbing already works, just undiscoverable: `desktop/agent/beacon.go`,
   `mobile/src/lib/beacon.ts`, `desktop/agent/auth_owner_claim.go`,
   `desktop/agent/auth_pair.go`, `web/app/pair/page.tsx`, `mobile/src/lib/pairDevice.ts`.
5. `yaver auth` succeeds silently — auto-starts agent (LaunchAgent/systemd) but never
   says "paired, check your phone"; `yaver serve` not mentioned in first-run copy.

## Confirmed API surface (re-verify before use)

`mobile/src/lib/beacon.ts` — singleton `beaconListener`:
- `setUserId(userId: string)` — computes SHA-256 fingerprint (match agent).
- `setKnownDevices(deviceIds: string[])` — whitelist from Convex device list.
- `start()` / `stop()` — bind/unbind UDP 19837.
- `subscribe(cb: (d: DiscoveredDevice) => void): () => void` — returns unsubscribe.
- `getDevices(): DiscoveredDevice[]`.
- `DiscoveredDevice { deviceId, host, port, name, needsAuth, bootstrapPasskey?,
  devicePublicKey?, lastSeen }`. `needsAuth===true` ⇒ bootstrap box ⇒ show Adopt.

`mobile/src/lib/pairDevice.ts` — (grep exact names; corruption hid them) expect
`normalizeTargetUrl`, `normalizeCode`, `fetchPairInfo(targetUrl)`,
`submitPair({url, code, token, convexSiteUrl, userId})`, `parsePairUrl`.

Adopt / owner-claim: lives in `mobile/app/(tabs)/devices.tsx` (bootstrap "NEEDS AUTH"
section, ~lines 1254-1302) — POSTs bearer to agent `/auth/pair/owner-claim`. **Extract
this into a shared `src/lib/adoptDevice.ts` so the onboarding screen reuses it instead
of duplicating the token-submit call.** (Security-sensitive: sends user token to a LAN
device — copy the exact existing logic, don't re-invent.)

Auth context: `mobile/src/context/AuthContext.tsx` — `useAuth()` gives
`{ user, token, surveyCompleted, markSurveyCompleted, ... }`. `user.id` is the userId
for `beaconListener.setUserId`.

Backend: device registry `backend/convex/schema.ts` devices table (lines ~226-351,
`needsAuth` flag ~330); heartbeat handler `backend/convex/http.ts` `/devices/heartbeat`
(~1643-1700) → mutation `backend/convex/devices.ts::heartbeat` (~593-670). First device
auto-set primary in `devices.ts::registerDevice` (~445-457).

Web onboarding gate: `web/lib/onboarding.ts::hasRegisteredMachine`; callback routes
0-device users to `/survey` (`web/app/.../callback/page.tsx:53-58`) — dead-end to fix.

## Build plan

### Phase 1 — mobile-only, addresses 100% of measured leak (lowest risk)

1. **New file `mobile/app/onboarding-pair.tsx`** — post-survey screen, soft nudge:
   - On mount: `beaconListener.setUserId(user.id)`; fetch Convex device list →
     `setKnownDevices`; `subscribe(...)`; `start()`. Cleanup unsub + `stop()` on unmount.
   - State machine: "Waiting for your computer…" (spinner) → on discovery render the
     device card; if `needsAuth` show **Adopt** (call shared `adoptDevice`); on a
     same-user already-authed beacon, treat as paired.
   - Lead CTA: large copyable `npm install -g yaver-cli && yaver auth` (expo-clipboard;
     check it's a dep). Add "keeps running after sign-in (auto-starts); or `yaver serve`."
   - On first device appears (beacon OR Convex list grows) → success state → CTA
     "Start coding" → `router.replace("/(tabs)/tasks")`.
   - **Prominent "Skip for now"** (secondary, always visible) → `/phone-projects`
     (Mobile Sandbox). Persist a `pairing_skipped` flag (SecureStore/AsyncStorage) so it
     doesn't reappear every launch.
2. **Route into it** — `index.tsx:14-18` and `survey.tsx` completeSurveyFlow: when
   authed + survey done + **zero devices + not skipped**, `router.replace("/onboarding-pair")`
   instead of `/(tabs)/tasks`. (Need a cheap device-count check; reuse the same Convex
   list call. If the check is async/slow, default to tasks and let Phase-3 banner cover.)
3. **Flip Tasks empty state** `tasks.tsx:3880-3912` — make **"Pair your computer"** the
   primary button (→ `/onboarding-pair`); demote "Open Mobile Sandbox" to secondary.
   Keep the copyable command lines.

### Phase 2 — close the loop

4. **First-device push** — in heartbeat handler (`backend/convex/http.ts` /
   `devices.ts::heartbeat`), detect the user's *first ever* online device (no prior
   `lastHeartbeat` on any of their devices) → enqueue a push "✅ your computer is paired".
   Reuse existing push infra (grep `expo push` / notification sender). Privacy: no paths,
   follow `convex_privacy_test.go` forbidden-field rules.
5. **CLI success output** — after `yaver auth` succeeds (`desktop/agent/auth*.go`, find
   the post-token-save success path), print: "Paired as <email>. Agent running — open the
   Yaver app, it'll appear in seconds. (Runs on login; `yaver serve` to foreground.)"
   Runtime-derive email/user; no hardcoded names (see feedback_yaver_is_for_everyone).
6. **Web dashboard empty state** — replace the 0-device `/survey` dead-end with a guided
   setup (platform-aware `npm install -g yaver-cli` + `yaver auth`, mirror mobile copy).
   Use inline SVG icons only (no icon libs — feedback_no_lucide_use_inline_svg).

### Phase 3 — lower the bar + measure

7. **"Pair via QR"** in onboarding — start a pair session and render a QR for the
   `yaver.io/pair?...` URL leaning on `web/app/pair` + `pairDevice.ts::parsePairUrl`.
   Check for an existing QR component before adding `react-native-qrcode-svg`.
8. **Persistent banner** — "1 step left: pair a device" shown app-wide until first device
   paired, then collapse. Drive off the same device-count/skip state.
9. **Funnel instrumentation** — events: survey_done → pair_onboarding_seen →
   beacon_discovered → adopt_attempted → first_heartbeat. Use existing analytics
   (`mcp__yaver__analytics_*` / whatever web/mobile already use). P2P-friendly; no PII.

## Constraints (from CLAUDE.md + memory)
- Never commit/push without explicit permission. Verify `git branch` before any commit.
- Never WebView for RN; pairing stays on beacon/owner-claim/QR native paths.
- iOS overlay files in `mobile/ios/` are force-tracked — don't let prebuild wipe them.
- Mobile-only fixes can ship via `yaver wireless push`; TestFlight only on explicit ask.
- Ship tests/fixtures alongside; prefer new files over large edits; don't break working
  pairing code (other sessions may touch mobile — collision risk).
- Typecheck before declaring done: `cd mobile && npx tsc --noEmit` (or repo's tsc task);
  `cd backend && npx convex` typegen; `cd desktop/agent && go build ./...`.

## Status at handoff (updated mid-Phase-1, 2026-06-01)

Branch `feat/device-pairing-onboarding`. Decisions locked: **soft nudge + prominent
skip**, **build all three phases**. node_modules is NOT installed in this checkout
(expected `MISSING`); `expo-clipboard` resolves once installed — it's statically
imported by `src/components/RunnerAuthModal.tsx` / `DeviceDetailsModal.tsx`, so match
that `import * as Clipboard from "expo-clipboard"` pattern.

### PHASE 1 COMPLETE (typechecks clean; only pre-existing tasks.tsx:916 `ready` error
remains — another session's runner code, outside all our diff hunks). Files:
- `mobile/src/lib/pairDevice.ts` — `adoptBootstrapDevice()` + `AdoptResult` (line ~255).
- `mobile/app/onboarding-pair.tsx` — NEW post-survey screen (beacon discovery + adopt +
  copyable command + live "waiting" state + success state + prominent "Skip for now"
  → AsyncStorage `pairing_skipped_<uid>` → Tasks). Exports `pairingSkippedKey(userId)`.
- `mobile/app/survey.tsx:160` — finishSurvey now `router.replace("/onboarding-pair")`.
- `mobile/app/(tabs)/tasks.tsx:3918-3962` — empty state flipped: PRIMARY = "Pair your
  computer" → /onboarding-pair; SECONDARY = "Open Mobile Sandbox"; "Refresh devices" now
  a muted text link. Styles `discoverRefreshLink`/`discoverRefreshText` added (~5824).
NOT YET: launch-routing in index.tsx does NOT yet honor pairingSkippedKey / device count
(a returning user who skipped still lands on tasks via index.tsx because surveyCompleted
is true → tasks; that's fine — they only see onboarding-pair via the survey path or the
Tasks "Pair your computer" button. Re-showing on every cold start was intentionally NOT
done to keep it a soft nudge.)
Verify on device: from REPO ROOT `yaver wireless push` (mobile-only; no TestFlight).

### (earlier) DONE
1. ✅ `mobile/src/lib/pairDevice.ts` — added `import type { DiscoveredDevice } from "./beacon"`
   at top, and appended `adoptBootstrapDevice(dev, token, userId?): Promise<AdoptResult>`
   + `AdoptResult` interface at end of file. This wraps the exact fetchPairInfo+submitPair
   sequence the Devices tab uses. PURELY ADDITIVE — devices.tsx untouched (avoids collision
   with other sessions). VERIFY it's there: `grep -n adoptBootstrapDevice src/lib/pairDevice.ts`.

### VERIFIED API anchors (re-grep to confirm line numbers, they drift)
- Beacon singleton `beaconListener` (`mobile/src/lib/beacon.ts:242`):
  `async setUserId(userId)`, `setKnownDevices(ids[])` (slices to first 8 chars),
  `start()`, `stop()`, `onDiscovered(cb): ()=>void` (NOT `subscribe`),
  `getDevices()`, `getBootstrapDevices()`. `DiscoveredDevice {deviceId,ip,port,name,
  lastSeen,hwid?,needsAuth?,bootstrapPasskey?,devicePublicKey?}`. Bootstrap beacons
  (`needsAuth===true`) bypass fingerprint+known checks. NOTE setUserId is async.
- `useDevices()` (`mobile/src/context/DeviceContext.tsx:25`) returns `{ devices: Device[],
  refreshDevices: ()=>Promise<void>, activeDevice, connectionStatus, primaryDeviceId,
  isLoading, ... }`. `Device` (`src/lib/api.ts:102`): `{id, deviceId?, name, platform?,
  isOnline?, status?, lastSeen?, ...}`. Use `d.deviceId ?? d.id` for short-id slicing.
- `useAuth()` (`AuthContext.tsx:327`): `{ user, token, surveyCompleted,
  markSurveyCompleted, ... }`; `user.id` = userId for `setUserId`, `user.email` for copy.
- Colors via `useColors()` (`src/context/ThemeContext.tsx`): keys `accent, bg, bgCard,
  bgElevated, textPrimary, textSecondary, textMuted, border, borderSubtle, error, success`.
- Survey finish: `mobile/app/survey.tsx:157` `router.replace("/(tabs)/tasks")` (inside
  `finishSurvey`, also called by "Skip for now" at line ~847).
- Launch routing: `mobile/app/index.tsx:19-23` (Redirect: survey if !surveyCompleted else tasks).
- Tasks zero-device empty state: `mobile/app/(tabs)/tasks.tsx:3887-3953`. Primary button
  currently "Open Mobile Sandbox" (`taskRouter.navigate("/phone-projects")`, line 3906);
  pairing is muted text steps 3917-3936; "Refresh Devices" secondary 3938-3952.
  Styles: `s.discoverPrimaryBtn`, `s.discoverSecondaryBtn`, `s.discoverBtnText`,
  `s.discoverSteps/Step/StepDot/StepNum/StepTitle/StepDesc`, `s.discoverDivider`,
  `s.discoverSectionLabel`, `s.discoverHelper`.

### TODO — remaining Phase 1 (in order)
2. CREATE `mobile/app/onboarding-pair.tsx` (new file, expo-router auto-registers it):
   - `useAuth()` + `useDevices()` + `useColors()`. `SafeAreaView` like survey.tsx.
   - On mount (useEffect): `await beaconListener.setUserId(user.id)`;
     `beaconListener.setKnownDevices(devices.map(d=>(d.deviceId??d.id)))`;
     `const unsub = beaconListener.onDiscovered(d => setDiscovered(prev merge by deviceId))`;
     `beaconListener.start()`. Cleanup: `unsub(); beaconListener.stop()`.
     Also `setInterval(refreshDevices, 5000)` so a box that registers via relay/Convex
     (not LAN) still flips us to success; clear interval on unmount.
   - SUCCESS condition: `devices.length > 0` (Convex list grew) → show "✅ Your computer
     is connected" + primary button "Start coding" → `router.replace("/(tabs)/tasks")`.
   - WAITING state: spinner + "Waiting for your computer…". Lead CTA = copyable command
     box `npm install -g yaver-cli && yaver auth` (use Clipboard.setStringAsync, toast/Alert
     on copy). Helper line: "Keeps running after sign-in (auto-starts). Or `yaver serve`."
   - DISCOVERED bootstrap device(s) (`discovered.filter(d=>d.needsAuth)`): render card per
     device with **Adopt** button → `adoptBootstrapDevice(dev, token, user.id)` (import from
     ../src/lib/pairDevice). On `ok` → Alert "Paired" + `refreshDevices()`. On
     `needsManualPasskey` → Alert telling them to use More → Pair a device. Else Alert error.
   - **Prominent "Skip for now"** (always visible, secondary style) → set AsyncStorage flag
     `pairing_skipped_<userId>` = "1" → `router.replace("/(tabs)/tasks")`.
3. EDIT `mobile/app/survey.tsx:157`: change `router.replace("/(tabs)/tasks")` →
   `router.replace("/onboarding-pair")`. (Brand-new user definitionally has 0 devices, so
   no async count check needed on this path. Leave the skip-for-now caller going through
   finishSurvey too — it'll also land on onboarding-pair, which is fine.)
4. EDIT Tasks empty state `tasks.tsx:3904-3936`: make **"Pair your computer"** the primary
   accent button → `taskRouter.navigate("/onboarding-pair")`; demote "Open Mobile Sandbox"
   to the secondary (outline) button; keep the install steps as supporting text. Don't
   delete the sandbox path, just reorder/restyle.
5. TYPECHECK: `cd mobile && npx tsc --noEmit` (needs `npm install --legacy-peer-deps`
   first since node_modules absent). Fix any type errors (Device.id vs deviceId, async
   setUserId await, Clipboard import).
6. Do NOT commit/push without explicit permission. When ready to verify on device:
   `cd <repo> && yaver wireless push` (from REPO ROOT —
   walks into ./mobile). Mobile-only change so wireless push is allowed; NO TestFlight.

### Phases 2 & 3 — see plan sections above (not started). Tracked in task list.

### KEY LEARNING — tooling corruption this session
Bash/Read/Edit intermittently returned scrambled output (repeated lines, contradictory
"success+fail" on one Edit, truncated mid-line, stale-file warnings). ALWAYS verify each
edit with a fresh `sed -n`/`grep` afterward rather than trusting the tool result. The
pairDevice.ts import edit reported both success AND failure but actually applied — confirmed
via sed. If edits seem not to apply, re-grep before retrying to avoid double-applying.
