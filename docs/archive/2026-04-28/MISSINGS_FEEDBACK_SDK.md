# MISSINGS — `yaver-feedback-react-native` vs. Yaver Mobile App

Audit of 2026-04-20 against SDK `0.6.1` and mobile app `1.18.5` (versionCode 180).

The SDK's original charter is narrow — capture shake-triggered feedback and
ship it to the agent. In practice users expect it to feel like a pocket
version of the Yaver mobile app because both are the same face of the same
agent. Every gap below is where that illusion breaks.

---

## 0. User-visible bugs seen in the 2026-04-20 11:59 recording

| # | Symptom | Root cause | Fix sketch |
|---|---|---|---|
| B1 | **Hot Reload button does nothing** when tapped inside the feedback modal | `FeedbackModal.handleReload` posts to `/dev/reload-app`, which broadcasts a `reload` command **through `/blackbox/command-stream` SSE** to subscribers. The host app almost never starts `BlackBox.start()` (YaverFeedback wires the handler but nothing opens the SSE channel), so the broadcast lands on zero subscribers and silently noops. Mobile uses `/dev/reload` — a direct dev-server push. | Switch SDK to `POST /dev/reload`; fall back to `/dev/reload-app` only when the former returns "no dev server running". Auto-open BlackBox SSE on init so remote reloads from the mobile app still reach the SDK. |
| B2 | **No progress UI** while a reload is in flight | `FeedbackModal` flips a local `isReloading` bool on POST start and off on POST return. No SSE, no log tail, no "Building…/Running/Ready" states. Mobile subscribes to `/dev/events` and surfaces `building`, `log`, `ready`, `reload`, `error`. | Subscribe to `/dev/events` while the reload dialog is open; show last log line + phase (`Building → Running → Reloaded`). Reuse BlackBox's SSE reader with the URL generalised. |
| B3 | **Same device listed 3×** in the Machine Picker (`Kvancs-MacBook-Air.local`) | `auth.ts::listReachableDevices` returns whatever Convex emits. Convex can have multiple rows per machine (re-install / re-pair / hwid rotation). Mobile runs a three-pass merge (`collapseAliasDevices` → identity key → alias key → endpoint key). SDK has **no** merge. | Port `collapseAliasDevices` + helpers from `mobile/src/context/DeviceContext.tsx:193-413` into `auth.ts::listReachableDevices`. |
| B4 | **Online machine not green** even though the agent is running | `MachinePickerScreen` uses only Convex's `isOnline` bool + a 60 s heartbeat-age gate. Mobile merges four signals: Convex heartbeat (90 s threshold), LAN UDP beacon, primary-relay presence override, and "I'm actively connected right now". Any one of these sets `online=true`; SDK gets none of the three extras. | Raise the stale threshold to 90 s to match mobile; wire `applyRelayPresence` against the primary relay's `/presence?ids=…`; optionally listen for the UDP beacon (`react-native-udp`) when the SDK is running on the same LAN. |

Fixing **B1 → B4** is the top priority and will resolve everything visible
in the recording.

---

## 1. Hot-reload wiring — detail

### SDK path (today)
- `FeedbackModal.tsx:222-248` → `POST /dev/reload-app` `{mode: "dev"}`
- `P2PClient.ts:225-241` → same endpoint
- `FloatingButton.tsx:318-331` → `POST /tasks` with the prompt *"Hot reload the app…"* (spawns a Claude task — takes minutes, not seconds)

### Mobile path
- `quic.ts:3748-3755` → `POST /dev/reload` (no body)
- `DevPreview.tsx:207-223` + `hotreload.tsx:250-252` call that helper

### Agent handlers
- `devserver_http.go:751-823` `handleDevServerReload` — drives the dev-server manager, emits `reload` on `/dev/events`, then broadcasts to BlackBox sessions
- `devserver_http.go:884-945` `handleReloadApp` — returns **503** if `blackboxMgr` is nil, otherwise broadcasts a `reload` command and calls `devServerMgr.Reload()` but drops its return value

**Required:** make the SDK's primary path `/dev/reload`, same as mobile.
`/dev/reload-app` is still useful as a *remote* trigger from the mobile app
to an SDK-instrumented third-party app — but not as the SDK's own
"reload-now" button.

---

## 2. Reload progress surface

Mobile's SSE handling (`DevPreview.tsx:60-115`, `hotreload.tsx:160-179`)
reads `/dev/events` and bucketizes events:

| Event | Meaning | Mobile UI |
|---|---|---|
| `starting` | dev-server boot | banner tinted yellow |
| `building` | Metro/expo compile underway | "Building…" + last log line |
| `log` | stdout line | live tail (last ~40 lines) |
| `ready` | bundle served | green dot, dismiss spinner |
| `reload` | HMR push fired | force reload card, bump WebView key |
| `error` | failed | red banner with message |
| `stopped` | dev-server killed | banner hides |

SDK has **none** of this. Adding it is ~80 lines on top of `BlackBox`'s
existing SSE reader.

---

## 3. Device picker duplication + green-dot computation

### Mobile merge chain (`DeviceContext.tsx:193-413`)
1. **`deviceIdentityKey`** — prefer `hwid:<hwid>` → `pub:<publicKey>` → `guest:<hostScope>:<id>` → `host:<os>:<normalized-name>` → `id:<id>` → `name:<name>`. Strong identity wins over name.
2. **`deviceAliasKey`** — OS + normalised hostname. Collapses `Kvancs-MacBook-Air` and `Kvancs-MacBook-Air.local`. On conflicts, `pickActiveOverStaleNeedsAuth` chooses online > stale-needs-auth.
3. **`deviceEndpointKey`** — host + port final pass.
4. **`mergeDeviceEntries`** — online/local wins, newest `lastSeen` wins, name from the more-online side.

### Mobile online chain
- `HEARTBEAT_STALE_MS = 90_000` (`DeviceContext.tsx:117`)
- `isActivelyConnected` override (line 586)
- LAN beacon override (`beacon.ts` + `DeviceContext.tsx:1007-1037`)
- Relay presence override (`applyRelayPresence` + `/presence?ids=…`, DeviceContext.tsx:66-91)
- Backend stale gate (`convex/devices.ts:23-27`)

### SDK today
- No merge. 3 Convex rows → 3 UI rows.
- Single signal (`isOnline`) + 60-second stale gate (`MachinePickerScreen.tsx:73-80`).
- No beacon, no relay presence, no active-connection feedback.

---

## 4. Feature parity matrix

Legend: ✅ present · ⚠ partial · ❌ missing.

| Feature | Mobile location | SDK |
|---|---|---|
| Apple native sign-in | `src/lib/auth.ts` | ✅ `auth.ts:221-267` |
| Google / GitHub / GitLab / Microsoft in-app OAuth | `app/auth/*` | ✅ `auth.ts:289-325` (strictNativeAuth in 0.6.1 forces ephemeral) |
| Email sign-in with 2FA/TOTP | mobile web flow | ⚠ SDK throws on `requires2fa` (`auth.ts:360-366`) |
| Session-token persistence | Keychain / SecureStore | ⚠ AsyncStorage only |
| Device list | `/devices/list` → Convex | ✅ `auth.ts:398-415` |
| **Device merge / dedup** | `collapseAliasDevices` + helpers | ❌ — **B3** |
| **Online status (4-signal merge)** | beacon + heartbeat + relay + active-conn | ❌ — **B4** |
| LAN UDP beacon discovery | `src/lib/beacon.ts` | ❌ Discovery.ts does a crude `fetch` sweep of 24 hardcoded 192.168 IPs |
| Convex-IP discovery | `DeviceContext` | ✅ `Discovery.ts:105-162` |
| Relay HTTP fallback | `quic.ts` | ✅ `Discovery.ts:169-217` |
| Auto-connect single device | `DeviceContext.tsx:1350-1379` | ❌ |
| Primary device preference (Convex `userSettings.primaryDeviceId`) | mobile reads/writes | ❌ (SDK's `preferredDeviceId` is local-only) |
| Network-change reconnect | `DeviceContext.tsx:1382-1402` | ❌ |
| App-state-change resume | `DeviceContext.tsx:1406-1426` | ❌ |
| Dev-server start/stop/status | `/dev/start · /stop · /status` | ❌ |
| **Hot reload (JS/dev-server)** | `/dev/reload` | ⚠ — **B1** |
| **Hot reload progress** | `/dev/events` SSE | ❌ — **B2** |
| Hot reload (native Hermes push) | `/dev/build-native` → `localhost:8347/bundle` | ⚠ SDK can only *receive* a reload-bundle command, never initiate |
| Remote reload from mobile → SDK | `/blackbox/command-stream` | ✅ `BlackBox.connectSSE` (must be `.start()`-ed) |
| TestFlight / Play Store build | `/builds` | ⚠ SDK routes through `/tasks` (Claude prompt) instead of the direct endpoint |
| Task CRUD | `/tasks/*` full surface | ⚠ SDK only `POST /tasks` + poll `GET /tasks/{id}`. No stop/continue/output/stop-all. |
| Agent / model picker | mobile Tasks tab | ❌ |
| Voice input capture | `FeedbackModal` | ✅ |
| Voice transcription | `/voice/transcribe` | ⚠ available in `P2PClient`, not auto-called after recording |
| Voice provider discovery | `/voice/status` | ⚠ present, unused |
| Agent status & runners | `/agent/status` + `/agent/runners` | ❌ |
| Projects browser | `/projects` | ❌ |
| Guest invite / accept / revoke | `DeviceContext` guest flow | ⚠ SDK can *use* a shared machine, cannot invite/revoke |
| BlackBox event streaming | n/a in mobile | ✅ |
| Feedback capture (screenshot, video, voice, error buffer) | n/a in mobile | ✅ |
| TLS fingerprint / requireTLS | `quicClient.tlsFingerprint` | ⚠ `FeedbackConfig` fields declared, not consumed anywhere in `fetch`/`P2PClient` |
| SDK-token rotation | n/a | ✅ `P2PClient.rotateToken` |
| Support sessions (TeamViewer-style) | CLI + web | ❌ |
| Exec / tmux / files / vault | `/exec`, `/tmux/*`, `/files/*`, `/vault/*` | ❌ (by design — feedback SDK shouldn't have a shell) |

---

## 5. Agent endpoints used

### SDK touches
`/health · /feedback · /feedback/stream · /tasks · /tasks/{id} (GET) · /builds · /voice/status · /voice/transcribe · /dev/reload-app · /test-app/* · /sdk/token/rotate · /flags/eval · /releases/* · /analytics/ingest · /blackbox/command-stream · /blackbox/events`

### Mobile-only endpoints the user has repeatedly hit while iterating
- `/dev/start · /dev/stop · /dev/status · /dev/reload · /dev/events · /dev/build-native · /dev/native-fingerprint · /dev/target · /dev/compatibility`
- `/agent/status · /agent/runners · /agent/capabilities · /agent/runner/restart · /agent/runner/switch`
- `/projects · /projects/refresh · /projects/switch · /projects/actions · /projects/mobile`
- `/tasks/{id}/stop · /tasks/{id}/continue · /tasks/{id}/output · /tasks/stop-all`
- `/streams · /streams/{name}` (SSE)
- `/mobile-workers/preview-session{,/command}`

Everything under `/exec`, `/tmux/*`, `/files/*`, `/vault/*`, `/autodev/*`,
`/vibing/*`, `/git/*`, `/quality/*`, `/healthmon/*` is **intentionally
out of scope** for a feedback SDK — those are remote-control surfaces.

---

## 6. Prioritised fix order

Cluster A — visible bugs from the recording (merge first):

1. **B1 · Hot-reload endpoint swap.** `FeedbackModal.handleReload` + `P2PClient.reloadApp` → `POST /dev/reload`; fall back to `/dev/reload-app` on 400/"no dev server". Also auto-`BlackBox.start()` on init so remote reloads from the mobile app still reach the SDK's SSE subscriber.
2. **B3 · Port `collapseAliasDevices` into `listReachableDevices`.** Direct copy + adapt of `DeviceContext.tsx:193-413`. Unit test with fixture containing 3 Convex rows for one hwid + one old host-only row.
3. **B4 · Widen online detection.** 60 → 90 s stale threshold; add `applyRelayPresence` against `platformConfig.primaryRelay`; optional UDP beacon listener behind a `react-native-udp` peer dep flag.
4. **B2 · `/dev/events` SSE subscription + progress UI.** Generalise `BlackBox.connectSSE` into a reusable helper; render last log line + phase badge in `FeedbackModal` while `isReloading`.

Cluster B — quick wins once Cluster A lands:

5. Auto-connect when a single device is online; honour Convex `userSettings.primaryDeviceId`.
6. Email 2FA flow (mirror web SDK's `sdk-callback` + TOTP page path).
7. Wire `tlsFingerprint` / `requireTLS` through every fetch call. Currently dead config.
8. `FloatingButton.handleReload` — route through `/dev/reload`, not `/tasks`.

Cluster C — real feature parity:

9. Dev-server control (`startDevServer`, `stopDevServer`, `getDevServerStatus`) exposed in a minimal in-app console.
10. Build-to-device: `POST /dev/build-native` → download Hermes bundle → POST to `localhost:8347/bundle`.
11. Network-change / app-state-change reconnect (mirror `DeviceContext.tsx:1382-1426`).
12. Device remove / detach (`/devices/remove` + local detached-ids list).
13. Agent status / runners list on the machine picker (per-runner health, not a single `runnerDown` bool).
14. Guest invite/accept/revoke UI.
15. Voice auto-transcription after `stopAudioRecording`.
16. Move token storage to Keychain / SecureStore.

Cluster D — deferred (intentionally not in SDK scope):

`/exec`, `/tmux/*`, `/files/*`, `/vault/*`, `/autodev/*`, `/vibing/*`,
`/git/*`, `/quality/*`, `/healthmon/*`, support sessions. These are for
the mobile app and CLI; SDK stays feedback-focused.

---

## 7. Files to touch when implementing Cluster A

| Bug | File(s) | Lines |
|---|---|---|
| B1 | `sdk/feedback/react-native/src/FeedbackModal.tsx` | 222-248 (handleReload) |
|    | `sdk/feedback/react-native/src/P2PClient.ts` | 225-241 (reloadApp) |
|    | `sdk/feedback/react-native/src/YaverFeedback.ts` | 132-164 (auto-start BlackBox) |
| B2 | `sdk/feedback/react-native/src/FeedbackModal.tsx` | 222-248 (add SSE subscribe while reloading) |
|    | `sdk/feedback/react-native/src/BlackBox.ts` | 366-441 (extract generic SSE helper) |
| B3 | `sdk/feedback/react-native/src/auth.ts` | 398-415 (listReachableDevices) — port dedup |
|    | `sdk/feedback/react-native/src/MachinePickerScreen.tsx` | 47-80 (consume merged list) |
| B4 | `sdk/feedback/react-native/src/MachinePickerScreen.tsx` | 73-80 (stale threshold + relay presence) |
|    | new helper: `sdk/feedback/react-native/src/presence.ts` | — (fetch `/presence?ids=…`) |

Reference sources on the mobile side:
- `mobile/src/context/DeviceContext.tsx` lines 66-91, 117, 193-413, 561-654
- `mobile/src/lib/beacon.ts`
- `mobile/src/lib/quic.ts` lines 1035-1083, 3748-3755
- `mobile/src/components/DevPreview.tsx` lines 60-115, 207-223
- `mobile/app/(tabs)/hotreload.tsx` lines 160-252

---

## 8. FeedbackModal actions — end-to-end audit (2026-04-20 addendum)

The recording only showed the shake → login flow. Tracing every remaining
button in the modal from UI → `P2PClient` → agent handler surfaced a
larger pile of issues. Each row is pinned to file:line for both sides.

### Per-feature verdicts

| Feature | UI (SDK) | Client call | Agent endpoint | Agent handler | Verdict | Blocker |
|---|---|---|---|---|---|---|
| Mode · **Live** | `FeedbackModal.tsx:45,98-111,139-200` | `P2PClient.streamFeedback` (`P2PClient.ts:119-137`) | `POST /feedback/stream` per-event | `feedback_http.go:16-72` (single-connection JSON stream decoder) | ❌ | SDK sends one POST per event; agent's decoder expects a single long-lived connection. Only first event per POST is seen. Event types `audio` / `voice_command` are not in the agent's switch; payload `data: {path,duration}` is a local device URI with no file bytes. Result: screenshots and audio **never reach the agent** in Live. |
| Mode · **Narrated** | `FeedbackModal.tsx:350-362` | — | — | — | ❌ | **Not implemented.** The button toggles state; no code branches on `narrated`. Behaves identically to Batch. |
| Mode · **Batch** | `FeedbackModal.tsx:250-309` | `upload.ts::uploadFeedback` | `POST /feedback` (multipart) | `feedback_http.go:75-133` → `FeedbackManager.ReceiveFeedback` (`feedback.go:135-183`) | ✅ | — |
| **Take Screenshot** | `FeedbackModal.tsx:82-118,404-406` | `capture.ts::captureScreenshot` (`react-native-view-shot`) | Batch → `POST /feedback`; Live → `POST /feedback/stream` | Batch → reads part bytes; Live → just acks | ⚠ | Works in Batch. In Live only the local file path is sent — no image bytes are uploaded. Hard-fails if host app doesn't install `react-native-view-shot`. |
| **Voice Note** | `FeedbackModal.tsx:120-172,408-416` | `capture.ts::startAudioRecording / stopAudioRecording` (`react-native-audio-recorder-player`) | Batch → `POST /feedback` (`audio` multipart field); Live → `POST /feedback/stream` `type:'audio'` | Batch handler detects `.m4a/.aac/.wav`; Live has **no `audio` case** → silently dropped | ⚠ | Works in Batch only. No auto-transcription — `handleToggleAudio` never calls `P2PClient.transcribeVoice`. |
| **"Not streaming" / "Streaming" indicator** | `FeedbackModal.tsx:431-437` | `BlackBox.isStreaming` getter (`BlackBox.ts:146-148`) | — | — | ❌ | Reads the wrong boolean. `isStreaming` flips on `BlackBox.start()` call, not on SSE health. Correct signal is `BlackBox.isCommandChannelConnected` (`BlackBox.ts:357-359`). Also not reactive — even the right getter needs a state push so the modal re-renders on disconnect. Compound bug: `YaverFeedback.init()` never auto-calls `BlackBox.start()`, so most hosts show "Not streaming" forever. |
| **Send Report** | `FeedbackModal.tsx:250-309,466-477` | `upload.ts::uploadFeedback` | `POST /feedback` | `feedback_http.go:75-133` | ✅ | Works, but `FeedbackBundle.errors[]` is serialised inside the `metadata` field and the agent unmarshals into `FeedbackReport`, which doesn't declare `errors`. Captured errors may be silently dropped on the server. Verify the struct. |
| **Hot Reload** | `FeedbackModal.tsx:222-248,421-429` | inline `fetch` to `/dev/reload-app` (not `P2PClient.reloadApp`) | `POST /dev/reload-app` | `devserver_http.go:884-945` | ❌ | Two independent failures: (1) the endpoint broadcasts through BlackBox SSE to zero subscribers (see §1 B1); (2) `/dev/reload-app` has **no matching entry in `scopePathPrefixes`** — `authSDK` denies every SDK-minted token. Works with owner/CLI/paired tokens only. |
| **Shake → manual modal** | `YaverFeedback.ts:136-145` → `FeedbackModal` via `DeviceEventEmitter` | `startReport` | Opens modal | — | ✅ | Auto-suppressed inside Yaver super-host (`ShakeDetector.ts:55`) — by design. |
| **Shake → `reportingOnly` auto-report** | `YaverFeedback.ts:605-664` | `sendAutoReport` → `uploadFeedback` | `POST /feedback` | `feedback_http.go:75-133` | ✅ | Server-side `/feedback` never auto-triggers a fix; `reportingOnly` is a pure client-side flag (matches docs). No retry queue — failed shake uploads are lost silently. |
| **Vibing** | (none) | (none) | `/vibing`, `/vibing/execute`, `/vibing/surprise` | `httpserver.go:548-550` under `s.auth` | ❌ | No SDK client surface exists. Even if added, all `/vibing*` routes are owner-only (`s.auth`), not `authSDK`. Today: mobile-app-only. |
| **`P2PClient.transcribeVoice`** (latent, not wired to any button) | `P2PClient.ts:188-215` — multipart `audio` field | `POST /voice/transcribe` | `voice_http.go:70-151` — `io.ReadAll(r.Body)` treats body as raw audio, ignores multipart | — | ❌ | Wire-format mismatch. Agent reads raw body, first ~200 bytes are MIME boundary → STT sees garbage. Logs successful byte count. |
| **BlackBox `/blackbox/events` POST** | `BlackBox.ts:475-499` | `blackbox_http.go:187-221` | `authSDK` + `blackbox` scope | ✅ | — |
| **BlackBox `/blackbox/command-stream` SSE** | `BlackBox.ts:366-441` | `blackbox_http.go:123-182` | `authSDK` + `blackbox` | ✅ | Reconnects every 5 s. This is the correct signal for the streaming indicator. |
| **`YaverFeedback.track`** | → `BlackBox.track` | `POST /blackbox/events` | `authSDK` + `blackbox` | ✅ | — |
| **`YaverFeedback.getFlag` / `getFlags`** | `P2PClient.flagsEvaluate(One)` (`P2PClient.ts:319-341`) | `GET /flags/eval` | `flags.go:483-487`, route `authSDK` at `httpserver.go:239` | ❌ | **No `/flags` prefix in `scopePathPrefixes`.** Any SDK token → 403 "SDK token scope does not allow this endpoint." |
| **`YaverFeedback.checkUpdate`** | `P2PClient.releasesLatest` | `GET /releases/latest` | Route under `s.auth` (owner-only) at `httpserver.go:226` | ❌ | Route gated on owner token — SDK tokens rejected regardless of scope. |
| **`P2PClient.releasesDownload`** | — | `GET /releases/bundle` | Route under `s.auth` at `httpserver.go:227` | ❌ | Same as above. |
| **`P2PClient.analyticsIngest`** | — | `POST /analytics/ingest` at `httpserver.go:237` with `authSDK` | — | ❌ | `authSDK` accepts, but no `/analytics` prefix in `scopePathPrefixes` → 403. |
| **`P2PClient.startTestSession` / `stop` / `getTestSession`** | `/test-app/start · /stop · /status` | `authSDK` + `testapp` scope | — | ⚠ | Scope prefix exists, but **`testapp` is not in `DEFAULT_SDK_SCOPES`**. Fails with default-minted tokens; requires `yaver sdk-token create --scopes ...,testapp`. |
| **`P2PClient.rotateToken`** | `POST /sdk/token/rotate` | route registration **not found** under authSDK with matching scope | — | ⚠ | No `/sdk` entry in `scopePathPrefixes`. If the route runs under `authSDK`, SDK tokens are 403. Needs explicit verification. |

### 8a. Root-cause clusters uncovered

The audit exposes five systemic problems that multiply the apparent
bug count:

**C1 · `scopePathPrefixes` in `httpserver.go:951-959` is incomplete.** SDK
tokens carry scopes; the agent checks the request path against the
scope's prefix list. Missing prefixes: `/dev/`, `/flags/`,
`/analytics/`, `/releases/`, `/sdk/`. Every `P2PClient` method
touching those returns 403 with a cryptic message.

**C2 · `/releases/*` is mounted under `s.auth` (owner-only) in
`httpserver.go:226-227`.** `checkUpdate` and `releasesDownload` are
impossible from the SDK regardless of scope. Either move them to
`authSDK` + add a `releases` scope, or drop the methods from
`P2PClient`.

**C3 · `DEFAULT_SDK_SCOPES` = `[feedback, blackbox, voice, builds]`** —
`testapp` exists as a prefix but isn't included. `yaver sdk-token
create` users don't know to add it. Either widen the defaults or
print a clear guide.

**C4 · `/feedback/stream` wire contract is underspecified.** SDK issues
one POST per event with a JSON body; agent treats the body as a
stream-of-JSON-objects and writes an SSE response that nobody
reads. Live and Narrated modes are dead without a rewrite of one
side. Recommended: SDK opens a single long-lived POST (chunked) or
a WebSocket; agent keeps the existing decoder; event schema
matches what the agent accepts (`voice`, `screenshot`, `annotation`,
`end`) and for screenshot/audio the SDK *uploads bytes* (follow-up
multipart or chunked binary frame) rather than a local path.

**C5 · `voice/transcribe` wire mismatch.** SDK sends multipart,
agent reads raw body. Pick one — agent should `ParseMultipartForm`
+ `FormFile("audio")` to match the SDK; or SDK should send the raw
audio with `Content-Type: audio/wav`. Same fix pattern as C4: align
the contract on one side only.

### 8b. Fix plan additions (append to §6 cluster order)

Cluster **A′** — extend the visible-bug cluster with broken-auth fixes:

A5. **Add missing scope prefixes + default scopes** (agent side,
`httpserver.go:951-959` and the default list in
`backend/convex/auth.ts:1314` / `desktop/agent/sdk_token.go:32`).
Minimum: `dev: ["/dev/"]`, `flags: ["/flags/"]`,
`analytics: ["/analytics/"]`, `sdk: ["/sdk/"]`, plus widen default
to include `dev`, `flags`, `analytics`, `testapp`, `sdk`.

A6. **Move `/releases/latest` + `/releases/bundle` to `authSDK`** with
a new `releases: ["/releases/"]` scope entry. Or drop
`releasesLatest/releasesDownload` from `P2PClient` if not
supported.

A7. **Fix the streaming indicator**: swap `BlackBox.isStreaming` →
`BlackBox.isCommandChannelConnected` in `FeedbackModal.tsx:431-437`,
add a state subscription so the pill re-renders on disconnect,
and have `YaverFeedback.init()` auto-call `BlackBox.start()` when
`enabled` is true.

A8. **Document or remove Narrated mode.** Short term: remove the
button. Long term: implement a session recorder with periodic
flush and a `reportingOnly`-aware auto-finish on shake.

Cluster **B′** — wire-contract fixes:

B9. **`/feedback/stream` contract realignment.** Options: (a) SDK
collects events in-memory and flushes a single chunked POST on
`end`; (b) single persistent POST with `application/x-ndjson`
containing event lines; (c) WebSocket. Pick (a) for minimum
churn. Event schema: `voice | screenshot | annotation | end` with
binary payloads sent in a follow-up multipart `/feedback` POST
keyed by report ID.

B10. **`/voice/transcribe` contract realignment.** Agent-side fix:
`ParseMultipartForm(32<<20)` + `FormFile("audio")`. One-line
change that matches every existing caller.

B11. **Screenshot bytes in Live mode.** When Live is rewritten
(B9), the screenshot event should include inline base64 bytes
(small) or reference a follow-up `/feedback/asset?reportId=X&id=Y`
upload (large).

Cluster **C′** — new functional gaps:

C12. **`errors[]` lost on agent.** Extend `FeedbackReport` struct
to include `errors []CapturedError`; update metadata unmarshal to
copy them from the bundle metadata JSON.

C13. **Vibing access policy.** Decide: (a) keep mobile-only and
remove SDK comments that imply it's available; or (b) add
`vibing: ["/vibing"]` scope, move `/vibing*` routes to `authSDK`,
add `P2PClient.vibingExecute`.

### 8c. Updated summary of what actually works today with a default SDK token

✅ `POST /feedback` (batch upload · Send Report · shake auto-report)
✅ `POST /blackbox/events`
✅ `GET  /blackbox/command-stream` (SSE for remote reload command)
✅ `GET  /voice/status`
✅ `GET  /builds`, `POST /builds`
✅ `GET  /health`
⚠ `POST /voice/transcribe` — 200 OK but garbage text due to multipart/raw mismatch
⚠ `POST /feedback/stream` — 200 OK but event types mismatch and no bytes uploaded
❌ `POST /dev/reload-app` — 403 (missing scope prefix)
❌ `GET  /flags/eval` — 403 (missing scope prefix)
❌ `POST /analytics/ingest` — 403 (missing scope prefix)
❌ `GET  /releases/latest`, `/releases/bundle` — 401 (route is owner-only)
❌ `/test-app/*` — 403 unless token minted with non-default `testapp` scope
❌ `/sdk/token/rotate` — likely 403 (unverified)
❌ `/vibing*` — 401 (owner-only by design)

### 8d. Recommended implementation order

Before touching any SDK TypeScript, land the three agent-side config
changes — they unlock four "❌" rows without a single client diff:

1. Add missing `scopePathPrefixes` entries (`dev`, `flags`,
   `analytics`, `sdk`, `releases`).
2. Add the missing scopes to `DEFAULT_SDK_SCOPES` (widen or print
   a deprecation-style warning that tells users to re-mint).
3. Move `/releases/*` to `authSDK` + new `releases` scope.

Then do Cluster A (B1-B4) + Cluster A′ (A5-A8) on the SDK side:

4. Hot-reload endpoint swap + SSE progress subscription.
5. `listReachableDevices` dedup port.
6. Online-detection widening.
7. Streaming indicator fix + auto-`BlackBox.start()`.
8. Remove or implement Narrated mode.

Then Cluster B′ (B9-B11) contract realignments — one commit each,
behind a minor version bump (0.7.0 since the `/feedback/stream`
schema is changing).

Then Cluster C′ for the long-tail (errors[] persistence, vibing
policy decision, etc.).

---

Last updated: 2026-04-20. Re-run this audit after Cluster A + A′ land
and bump the verdicts table.
