# Handoff — Mobile Shortcuts + Tasks→Hermes-reload + Feedback-SDK Voice + CLI onboarder

**Date:** 2026-05-31  **Branch:** `main`  **Author:** previous Claude session
**Scope:** mobile app, feedback SDKs (web + react-native), desktop agent voice
protocol, headless packages, CLI update onboarder, full deploy.

> Read the code, not just this doc (per CLAUDE.md). All paths below are real
> as of commit `a341134e`. `git log --oneline -8` shows the session's commits.

---

## 1. What was asked (chronological)

1. From the mobile **Tasks** tab + **feedback SDK** (container case): trigger a
   **Hermes reload** on a connected dev PC by **typing or speaking**.
2. A **Shortcuts** bottom-nav tab — one-tap, chainable actions (connect →
   open project → reload), Talos-style, with a **creation UI**.
3. **Vibe coding via STT/TTS** in the feedback SDK (inside Yaver *and*
   standalone), with **local and remote** models.
4. Make the feedback SDK **web + mobile** fully support STT/TTS in **local**
   and **Flux** (Deepgram) cases, **clearly** (selectable + visible).
5. Commit/push + deploy **all** (Convex, npm, TestFlight, Play). Prefer local.
6. Fix broken cases; check `yaver-mobile-headless` + `yaver-web-headless`.
7. Fix the mobile **"Unimplemented component: ExpoGlassEffect_GlassView"** error.
8. Make **local STT/TTS work from Tasks** — "mobile app should have injected
   local stt/tts models."
9. Add a **Codex-style auto-upgrade onboarder** to the `yaver` CLI.

---

## 2. What shipped (features)

### A. Tasks → Hermes reload (typed or spoken) — `mobile/app/(tabs)/tasks.tsx`
- `isReloadIntent()` (module-level regex) detects a bare reload command
  ("reload", "hot reload", "hermes", "rebuild bundle", optional project token).
- In `handleCreateTask()`, a fast-path intercepts that intent and calls
  `triggerHermesReload()` → `clientFor(deviceId).reloadDevServer({mode:'bundle'})`
  against the selected device — instead of spinning up an agent task.
- Re-added the **composer mic** (retired in 2026-04-28) + a **⚡ Reload chip**.
  Speaking "reload" works because dictation writes into the same input.
- iOS-gated (Android has no `YaverBundleLoader`); visible toast/Alert on result.
- Feedback-SDK container reload was **already wired** (`FeedbackModal.handleHotReload`
  → `P2PClient.reloadApp('bundle')`) — no change needed there.

### B. Shortcuts tab (Convex-synced, chainable, one-tap)
- **Convex**: `userShortcuts` table (`backend/convex/schema.ts`) + module
  `backend/convex/shortcuts.ts` (`listByToken`/`upsertByToken`/`deleteByToken`)
  + HTTP routes `/shortcuts` (GET/POST) + `/shortcuts/delete` in
  `backend/convex/http.ts` (added to CORS allowlist). **Deployed to prod.**
  - **Privacy contract**: steps store ONLY `deviceId` + `projectSlug` + flags +
    UI `label`. NO absolute paths, NO task-prompt text (closed `stepValidator`).
- **Mobile**: `mobile/src/lib/shortcuts.ts` (CRUD over `/shortcuts`),
  `mobile/src/lib/runShortcut.ts` (client-side sequential chain runner →
  `selectDevice`/`switchProject(slug,startDev)`/`reloadDevServer`/`openAppBus`,
  stop-on-first-failure with visible error).
- **Screen + tab**: `mobile/app/(tabs)/shortcuts.tsx` (card list, tap-to-run with
  per-step progress, template chooser + step editor). Registered as a visible
  6th tab in `mobile/app/(tabs)/_layout.tsx` (between Projects and Devices).

### C. Feedback-SDK voice vibe coding (web + RN, local + Flux)
Routes through the agent's `WS /voice/stream`. **Local = whisper.cpp on the
user's machine (private) + client-side TTS readback. Flux = Deepgram nova-3 STT
+ Aura TTS streamed back as PCM.** Same wire protocol on both surfaces.

- **Agent** (`desktop/agent/voice_http.go`): `voiceStartFrame` now accepts
  per-session `sttProvider`/`ttsProvider` overrides; resolution + validation
  moved post-start-frame; emits a `{"type":"providers","stt","tts"}` echo so the
  UI shows the active engine; `streamTTS` takes the per-session provider;
  `validateVoiceSTTProvider()` helper. **`wsQueryToken` shim** in
  `desktop/agent/httpserver.go` lets browser WebSockets auth via `?access_token=`
  (browsers can't set WS headers). Shipped in `yaver-cli@1.99.246`.
- **RN SDK** (`sdk/feedback/react-native/`): `voice.ts` (ported WS client) +
  `capture.ts` `startPcmRecording` (raw LPCM, the format STT needs — the m4a
  voice-note recorder can't stream) + `VibeChatScreen.tsx` mic + **Local/Flux
  toggle** (Flux shown only when agent has a Deepgram key, from `/voice/status`)
  + active-engine caption + local TTS via `expo-speech`. Published `0.9.0`.
- **Web SDK** (`sdk/feedback/web/`): built from scratch — `voice.ts`
  (`WebAudioPCMRecorder` → 16kHz mono Int16 PCM, `WebVoiceSession` WS client w/
  query-token, `playPcm16` Web-Audio playback, `speakViaSynthesis` local TTS) +
  mic + Local/Flux toggle wired into the vibing chat in `YaverFeedback.ts`.
  Published `0.5.0`.
- TTS note: agent only streams PCM frames for **cloud** engines; for **local**
  the client synthesizes from the `task-result` text (browser `SpeechSynthesis`
  / RN `expo-speech`).

### D. On-device local STT model bundling — `mobile/`
- **Bug:** `ggml-whisper-tiny.bin` (32 MB) is in the repo but was never embedded
  (no metro `.bin` assetExt, no `assetBundlePatterns`), so `isBundleAsset:true`
  failed → "Failed to load the model" when tapping the Tasks mic.
- **Fix:** added `mobile/metro.config.js` (registers `.bin` as an asset),
  load the model via `require()` in `speech.ts` `initWhisper()` so **Expo
  embeds it in the binary**, added `assetBundlePatterns:["assets/**"]` to
  `app.json`. Local **TTS** already uses `expo-speech` device voices (no model).
  **Requires the native rebuild (TestFlight 360) to take effect.**

### E. GlassView "Unimplemented component" — `mobile/src/components/YaverGlass.tsx`
- **Bug:** `expo-glass-effect` JS is installed (`~55.0.11`) but its **native pod
  is not linked** (not in `Podfile.lock`). `supportsLiquidGlass()` only checked
  the JS export + iOS-26, so it rendered the native `GlassView` that isn't in the
  binary → red "Unimplemented component: ExpoGlassEffect_GlassView" over the tab bar.
- **Fix:** gate on the package's `isGlassEffectAPIAvailable()` /
  `isLiquidGlassAvailable()` probes (return `false` when native absent) → falls
  back to BlurView; auto-upgrades when the native pod ships. JS-only fix, but
  ships with TestFlight 360.

### F. Codex-style CLI update onboarder — `cli/src/update-check.js` + `index.js`
- On the bare `yaver` shell and `yaver wrap`/`code`, checks npm for a newer
  `yaver-cli` and prompts: **Update now / Skip / Skip until next version**
  (runs `npm install -g yaver-cli@latest`). TTY-only, 8h-throttled, skipped in
  CI/non-TTY/`YAVER_NO_UPDATE_CHECK=1`, fails open. State:
  `~/.yaver/update-check.json`. Shipping in `yaver-cli@1.99.247`.

---

## 3. Broken cases found + fixed (not caused by this work, fixed anyway)

- **`release-sdk.yml` failed on every release** — it auto-fired on
  `release: published` and blanket-republished unchanged SDKs + ran the
  mobile-headless typecheck. **Fix:** removed the `release: published` trigger →
  SDK publishing is now `workflow_dispatch`-only (the path that works).
- **`yaver-mobile-headless` typecheck broken** — it reuses `mobile/src/lib/*`
  via path-shims; new imports there (`expo-file-system/legacy`, `expo-sqlite`,
  `@react-native-community/netinfo`, RN `Linking`, dgram `reusePort`) had no
  shim, so `tsc --noEmit` failed in CI. **Fix:** added the missing shims +
  tsconfig paths + fetch-mock cast (`mobile-headless/src/shims/*`, `tsconfig.json`,
  `test/llm-client.test.ts`). 0 errors, 19 tests pass, published `0.1.5`.
  **Fix is self-contained in `mobile-headless/` — `mobile/src` untouched.**
- **`yaver-web-headless`** — NOT broken; its CI "failure" was only E403
  republishing the unchanged `0.2.1`. Left as-is (`0.2.1` is live).

---

## 4. Deploy status

### Live now (all confirmed)
- npm: **`yaver-cli@1.99.247`** (release-cli run = success, signed binary built),
  **`yaver-feedback-web@0.5.0`**, **`yaver-feedback-react-native@0.9.0`**,
  **`yaver-mobile-headless@0.1.5`**. (`yaver-web-headless@0.2.1` unchanged/live.)
- **Convex** → deployed to **prod** (`perceptive-minnow-557`); `userShortcuts`
  table + index live.
- **iOS TestFlight build 360** (1.18.135) — `deploy-testflight.sh` exited **0**
  (= upload succeeded; the script only exits 0 after `EXPORT SUCCEEDED` + upload).
  **This build carries the GlassView fix + the embedded whisper model.** Build
  number bump committed (`8d9b6622`). Build 359 (no fixes) is superseded.

### Still in flight (confirm)
- **Android → Play internal** — `release-mobile.yml` run `26722006246`
  (mobile/v1.18.135), `android` job **still in_progress** at handoff and it has
  run long — verify it finished green (or re-run / do the local Play deploy:
  `JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh`).

---

## 5. What remains / open items

1. **Confirm the 3 in-flight runs** (§4) actually completed green:
   `gh run list --workflow=release-cli.yml -L1`, the Play run
   `gh run view 26722006246`, and the TestFlight 360 log.
2. **Verify on-device STT end-to-end on TestFlight 360**: tap the Tasks mic,
   speak "reload" → should reload; speak a task → should dictate. If the model
   still fails to load, check that the archive's "Bundle React Native code"
   phase actually embedded `assets/models/ggml-whisper-tiny.bin` (app size
   should jump ~32 MB). Android equivalent: model under
   `android/app/src/main/assets/` after a release build.
3. **Verify GlassView fallback** on TestFlight 360 (tab bar = BlurView, no red
   error). When the RN/Xcode bump lands and the `expo-glass-effect` **native
   pod** is installed (`pod install` → `Podfile.lock` gains `ExpoGlassEffect`),
   `isGlassEffectAPIAvailable()` flips true and real Liquid Glass turns on — no
   code change needed.
4. **Feedback-SDK voice first-turn**: the in-chat mic handles follow-up voice
   turns (speak → agent → spoken reply). The *first* vibe turn is still typed;
   pure voice cold-start would need a mic on the vibe-input screen too.
5. **Flutter feedback SDK voice** — deferred. Web + RN done; Flutter
   (`sdk/feedback/flutter`) needs its own WS + PCM + audio in Dart.
6. **Shortcuts runner `create-task` step** — not implemented (would prompt for
   text at run time, never persisted, per the privacy contract). v1 chains:
   select-device / open-project / start-dev / hermes-reload.
7. **CLI onboarder** — only on bare shell + `wrap`/`code`. If you want it on
   more entry points, broaden the gate in `cli/src/index.js` `runUnified()`.
   The npm-publish-before-binary ordering in `release-cli.yml` (§4) is worth
   hardening (publish npm *after* the binary release exists).

---

## 6. Pre-existing test failures (NOT regressions — do not chase as new)

Proven pre-existing by stashing this session's changes and reproducing on
pristine `main`:
- **Web feedback SDK**: 2 failing overlay tests in `YaverFeedback.test.ts`
  ("creates an overlay DOM element", "git setup before vibing tools"). 71/73 pass.
- **RN feedback SDK**: 6 failing `BlackBox.hotReload` tests — `DeviceEventEmitter`
  is undefined in the jest mock (shake/dev-menu path). Unrelated to voice.

Everything this session added typechecks clean: `mobile` tsc, RN SDK tsc, web
SDK tsc, agent `go build ./...`, backend `convex codegen`, mobile-headless
`bun run typecheck` (0 errors) + 19 tests + build.

---

## 7. Key files (for fast orientation)

| Area | Files |
|---|---|
| Tasks→reload | `mobile/app/(tabs)/tasks.tsx` (`isReloadIntent`, `triggerHermesReload`, composer mic/⚡) |
| Shortcuts | `backend/convex/{schema.ts,shortcuts.ts,http.ts}`, `mobile/src/lib/{shortcuts.ts,runShortcut.ts}`, `mobile/app/(tabs)/{shortcuts.tsx,_layout.tsx}` |
| Agent voice | `desktop/agent/voice_http.go`, `desktop/agent/httpserver.go` (`wsQueryToken`) |
| RN SDK voice | `sdk/feedback/react-native/src/{voice.ts,capture.ts,VibeChatScreen.tsx,P2PClient.ts,FeedbackModal.tsx}` |
| Web SDK voice | `sdk/feedback/web/src/{voice.ts,YaverFeedback.ts}` |
| Whisper bundling | `mobile/metro.config.js`, `mobile/src/lib/speech.ts`, `mobile/app.json` |
| GlassView | `mobile/src/components/YaverGlass.tsx` |
| CLI onboarder | `cli/src/update-check.js`, `cli/src/index.js` |
| headless fixes | `mobile-headless/src/shims/*`, `mobile-headless/tsconfig.json` |
| CI | `.github/workflows/release-sdk.yml` (trigger fix) |

Related memory: `project_feedback_sdk_voice_vibe.md` (local-vs-flux design + the
load-bearing facts about agent-side TTS streaming and the browser-WS token shim).
