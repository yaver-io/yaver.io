# Progress dump — 2026-07-15 (voice · false-logout · machine auto-connect)

Session handoff. Three independent work streams. All code committed + pushed to
`main`; Convex backend deployed to prod. **No mobile build was run** (parallel
threads own the build/deploy). Mobile UI/auth changes are typecheck-clean but
**not yet device-verified** — they only prove out on a real build.

---

## 1. Hands-free voice in CarPlay (+ all surfaces)

### Issue
In CarPlay you connect to a box (Mac mini), speak, and it says "I didn't
understand you."

### Root cause (4 audits)
- STT text extraction already worked; the fallback line is pure TS in
  `car-voice-coding.tsx`, fired only when `transcribe()` **threw or returned
  empty**.
- The real gap was **"when to submit"** — on the phone you tap submit; in the
  car you can't, and there was **no logic** to decide, from the speech alone,
  that a thought is finished. Drivers pause to think, so a fixed silence timer
  would cut them off.
- Capture failed on the car route: the recorder set no `.voiceChat` mode / no
  Bluetooth input, so over CarPlay's BT-HFP route the mic captured silence.

### What changed
A shared, surface-agnostic **`VoiceConversationCore`** (`mobile/src/lib/voice/`):
`listen → semantic endpoint → interceptors → risk gate → dispatch → speak →
auto-resume → barge-in`. Pure TS, tsx-tested.
- **Semantic endpointing**: timing endpointer (`endpointer.ts`) only decides
  *when to ask*; an on-device `llama.rn` judge (`completenessJudge.ts`,
  GBNF-forced `{complete,wantsAnswer}`) decides *the answer*. Free/offline, with
  heuristic fast-path + silence fallback when no model.
- **Runner-only (no Flux)**: commits one complete instruction to the live
  claude/codex session via `runnerSessionTurn`, reusing `carSessionTurn` readback.
- **Barge-in**: `interrupt()` + `speech.ts::stopSpeaking` (retains TTS handle);
  v1 tap-interrupt, v2 native AEC module.
- Adapters: `whisperCapture` (streaming STT + CarPlay `AVAudioSession`),
  `deviceTts`, `runnerChannel`, `localJudge`, `interceptors`. Factory
  `createVoiceCore` + hook `useHandsFreeVoice`. Car wired in
  `app/car-voice-coding.tsx` (autostart → hands-free; big button = start/stop/
  interrupt, no PTT-submit). Glass wired by a parallel thread.

### Status
Committed (`1d39482ee` + earlier). 64 voice tests + 35 reused-lib tests green;
`tsc` clean. **Device-verify pending** (needs a build + real CarPlay route).
Doc: `docs/architecture/VOICE_CONVERSATION.md`.

### Follow-ups
Native echo-cancelling barge-in module; wire `localJudge.resolveModel` to the
bundled GGUF when model-install ships; locale plumbing (en hard-set); migrate
phone/watch/TV/web onto the core; retire legacy PTT `runTurnFromUri`.

---

## 2. False logout — "sometimes kicked to login screen" (P0)

### Issue
The app sometimes bounces a signed-in user to the login screen, on a session
that is supposed to be 1-year.

### Root cause
Mobile sent `X-Yaver-Rotate-Token: 1` on **every** `/auth/refresh`
(`mobile/src/lib/auth.ts::doRefreshToken`), so the server rotated the token A→B
with only a **2-minute grace** (`backend/convex/auth.ts` `ROTATION_GRACE_MS`).
The new token B rides the refresh RESPONSE; a phone loses that response
routinely (cellular blip, the 5s abort, or iOS suspending before the
fire-and-forget `saveToken`). Client stays on A → 2 min later the 30s
`/devices/list` poll gets 401 → `notifyAuthFailure` → `clearToken` → `index.tsx`
guard flips → Redirect to /login. Rotation was built for the Go agent (atomic
persistence), never a mobile requirement.

### What changed (commit `d9b2f5d4a`)
- `mobile/src/lib/auth.ts`: **dropped the rotate header** — extend-only, never
  strands (future builds).
- `backend/convex/auth.ts`: `ROTATION_GRACE_MS` **2 min → 7 days** so
  already-installed apps (which still rotate until updated) tolerate a missed
  rotation response and re-learn the rotation on any later refresh.
- Parallel thread mirrored extend-only/no-rotate to **web** (`web/lib/use-auth.ts`)
  and **tvOS** (`Backend.swift`/`YaverStore.swift`; tvOS previously had NO
  refresh → 1yr token hard-expired = the stuck-login). Those files are in the
  working tree, owned by that thread.

### Status
Mobile + backend committed + pushed. **Backend DEPLOYED to prod Convex**
(perceptive-minnow-557) — this alone stops the false logout for existing installs
with **no app update**. Mobile no-rotate reaches users on the next build.

---

## 3. Machine selection — auto-connect UX

### Issue
On the Tasks tab it shows "No machine selected / Choose machine" even though it
*could* connect (tapping Choose just auto-connected). The transient discovery/
connect states read as a hard "you must pick" wall.

### What changed (commit `ee62bd37c`)
Rule: **primary if online → else secondary if it's online and primary isn't →
else show the list.**
- `DeviceContext`: rewrote the auto-connect effect to be **sequential + narrated
  + cancellable** (was parallel best-reachable). Only sticky/primary/secondary
  auto-connect; if none reachable, drop to the list (no scary error). New state
  `autoConnecting` / `autoConnectTarget` / `cancelAutoConnect`.
- `RemoteBoxBanner`: shows "**Connecting · Primary · <box>**" + a **Cancel** chip
  instead of "No machine selected" during a sweep.
- `NoMachineEmpty`: narrated "**Primary (Mac mini) is online — connecting…**"
  state + an **inline tappable machine list** (online-first, Primary/Secondary
  tags) instead of a bare "Choose machine" button.
- `mobile/src/lib/autoConnectStatus.ts`: one shared status vocabulary so every
  RN surface (mobile/tablet/glass/car) reads identically.

### Status
Committed + pushed; `tsc` clean. **Device-verify pending.** Web + watch/TV native
would need the same narration mirrored for full cross-surface parity (follow-up).

---

## Commits / deploy state
- `1d39482ee` voice: shared hands-free module + glass wiring
- `d9b2f5d4a` auth: stop mobile rotation + backend grace 7d (false-logout)
- `ee62bd37c` devices: narrated primary/secondary auto-connect + inline picker
- **Convex prod: DEPLOYED** (grace fix live). Web/tvOS logout mirrors: uncommitted,
  owned by a parallel thread. **No mobile build run** — deliberately deferred.

## Environment note
This Mac's disk is tight (~15 GiB free after clearing `~/.gradle` caches +
talos `.next`); a TestFlight archive wants ≥20 GiB. Whoever builds should free
~5 GiB more first (talos `node_modules` / Docker) or use `yaver wireless push`
(lighter, matches the "iterate wireless not TestFlight" preference).
