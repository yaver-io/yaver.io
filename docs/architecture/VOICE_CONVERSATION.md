# Voice Conversation Engine — hands-free STT/TTS across every surface

Status: **core shipped (client-side), device-verification pending** · 2026-07-15

This is the runtime reference for the shared hands-free voice loop — the
"Claude-app voice mode" experience — used by CarPlay first and every other
surface (phone, watch, TV, web, glass, VR) next. Read it before touching
`mobile/src/lib/voice/*`, `speech.ts`'s TTS/realtime paths, or any surface's
voice entry point.

> Docs drift; code is truth (see `CLAUDE.md`). Where this disagrees with
> `mobile/src/lib/voice/`, the code wins — fix the doc in the same change.

## The problem this solves

The bug report: in Apple CarPlay you connect to a box (e.g. the Mac mini),
speak, and it says *"I didn't understand you."* Four parallel audits established:

1. **STT text extraction already works** (on-device whisper, English). The
   fallback string is pure TS in `car-voice-coding.tsx` — fired when
   `transcribe()` **threw or returned empty**, not when the words were wrong.
2. **The real gap is "when to submit."** On the phone the user taps a submit
   button. In the car they can't. There was **no business logic** to decide,
   from the speech alone, that a thought is finished — and a driver **pauses to
   think mid-instruction**, so a fixed silence timer would cut them off.
3. **Capture failed on the car audio route.** The recorder set no
   `.voiceChat` mode and no Bluetooth input option, so over CarPlay's BT-HFP
   route the mic captured silence → empty transcript → the fallback line.
4. **Massive duplication.** The same STT→dispatch→TTS loop was re-implemented
   for car, watch, TV (twice, once in Swift), the local helper, and web spatial;
   the one-sentence summarizer existed 3× in 2 languages; recording setup was
   copied 5×.

## The design

One **surface-agnostic core** owns all the business logic; surfaces plug in four
thin adapters. Pure TS — no React, expo, whisper, network, or `Date.now()` — so
it unit-tests under `npx tsx`.

```
        ┌──────────────── VoiceConversationCore (pure TS, shared) ───────────────┐
        │  listen → semantic endpoint → interceptors → risk gate → dispatch →      │
        │  speak → auto-resume → barge-in       (conversationCore.ts)              │
        └────────────────────────────────────────────────────────────────────────┘
             ▲ AudioCaptureAdapter   ▲ TtsAdapter   ▲ AgentChannelAdapter   ▲ CompletenessJudge
        whisper.rn realtime      expo-speech /     runnerSessionTurn        on-device llama.rn
        + CarPlay AVAudioSession  cloud TTS         (claude/codex)           (free) + heuristic
             ▲
   surfaces: car · phone · watch · tv · web · glass · vr   (UI shells only, via createVoiceCore)
```

### The loop (conversationCore.ts)

```
listen ──▶ timing endpointer proposes an end-of-utterance (WHEN to ask)
       ──▶ JUDGE: semantically complete? ── no ─▶ accumulate fragment, keep listening
                    │ yes
       ──▶ interceptors (machine-switch / surface intents) ── handled ─▶ speak ─▶ listen
                    │ pass
       ──▶ risk gate ── risky ─▶ spoken confirm handshake ─▶ (yes) dispatch / (no) cancel
                    │ safe
       ──▶ DISPATCH one complete instruction to the runner (claude/codex)
       ──▶ SPEAK the one-sentence reply ──▶ auto-resume listening (hands-free)
```

### Semantic endpointing — the crux

Two stages, so we get semantics without paying for an LLM call on every tick:

- **Timing trigger** (`endpointer.ts`, `UtteranceEndpointer`): whisper re-emits
  its best transcript each slice; while the user talks it keeps changing, when
  they stop it goes stable. "Stable for `silenceMs`" only decides **when to ask
  the judge** — plus a no-speech timeout and a hard max-utterance cap (safety
  nets). It is **not** the decision.
- **Semantic judge** (`completenessJudge.ts`): decides — from the words/verbs —
  whether the thought is a **finished, actionable instruction** and whether the
  user **expects an answer now**. Heuristic fast-path (trailing conjunction →
  keep listening; imperative/question → complete) answers the obvious cases; the
  ambiguous ones go to an **on-device `llama.rn` model**, GBNF-constrained to
  `{complete, wantsAnswer}`. Runs **free, offline** — the only judge placement
  that avoids a second bill (see below).

This is what lets a driver say *"add a login button… and…"*, pause to think, and
finish *"…that logs in with Google"* without being cut off — the fragments
accumulate until the judge says complete.

### Runner channel — no Flux, no double bill

The complete instruction is committed to the **live runner session** the user
already has running (claude code / codex) via `POST /runner/session/turn`,
reusing `carSessionTurn.ts` for choice-parsing + one-sentence pane readback.
Key constraint (runner audit): tmux `send-keys` submits with an unconditional
Enter, so **the CLI can't judge completeness non-committally** — hence the judge
runs **before** the commit, client-side. The runner is the user's own paid
subscription; the on-device STT + judge are free. No cloud STT/LLM in the
default path = no "two payments."

### Barge-in

`core.interrupt()` cuts off TTS (`speech.ts::stopSpeaking` now retains the
playback handle) and drops back to listening. **v1**: interrupt is triggered by
the surface's control (tap-to-interrupt) — wired to the big button. **v2**: true
open-mic barge-in needs a native echo-cancelling capture path (the whisper.rn
`AudioQueue` gets no AEC); the `.voiceChat` session the capture adapter now sets
is the groundwork. The core is already barge-in-ready; only the trigger is v1.

## Files

| File | Role |
|---|---|
| `voice/types.ts` | The four adapter seams + state types |
| `voice/endpointer.ts` | Timing trigger (transcript-stability) — WHEN to ask |
| `voice/completenessJudge.ts` | Semantic "is it complete / wants answer" — the decision |
| `voice/conversationCore.ts` | The full hands-free state machine |
| `voice/scheduler.ts` | Real + `FakeTime` clock/scheduler (deterministic tests) |
| `voice/createVoiceCore.ts` | Factory every surface calls |
| `voice/useHandsFreeVoice.ts` | The one React seam (surfaces use this) |
| `voice/adapters/whisperCapture.ts` | Streaming STT + CarPlay AVAudioSession |
| `voice/adapters/deviceTts.ts` | TTS + barge-in stop |
| `voice/adapters/runnerChannel.ts` | Commit to the live runner session |
| `voice/adapters/localJudge.ts` | On-device model wiring (graceful degrade) |
| `voice/adapters/interceptors.ts` | machine-switch / surface-intent / risk |

## Verification

- **Pure logic (here, now):** `npx tsx src/lib/voice/{endpointer,completenessJudge,conversationCore}.test.ts`
  — 64 assertions (timing, accumulation, menu choice, risk confirm, interceptor,
  barge-in, idle-park, virtual-time loop). Reused libs
  (`carSessionTurn`/`carVoiceConfirm`/`carMachineSwitch`/`carSurfaceIntent`) —
  35 assertions, still green. `tsc --noEmit` clean project-wide.
- **On-device (wire-push, in a car):** the audio-session + capture fix and the
  end-to-end loop can only be confirmed on a real CarPlay route. From repo root:
  `yaver wireless push` (or `yaver wire push`), connect CarPlay, speak.

## Surfaces built on the core

- **Car / glass** — `mobile/app/car-voice-coding.tsx` (surface `"car"` / `"glass"`).
- **Phone (Vibe)** — `mobile/app/vibe.tsx` (surface `"phone"`), launched from the
  Tasks-tab mic FAB. Adds the phone-only `loadAppInterceptor` (*"load me the app
  with Hermes"* → load a guest app into the container) and a summarized,
  checklist-style turn UI. See **`VIBE_FROM_TASKS.md`** for the full "talk to
  build, load the app, keep vibing" design (incl. the autorun / protected-build /
  voice-deploy roadmap).

## Roadmap

- **v2 native barge-in:** an `AVAudioEngine.setVoiceProcessingEnabled` capture
  module scoped per-turn (satisfies CarPlay criterion 2) feeding AEC-clean audio
  to both STT and a VAD onset detector → `core.interrupt()`.
- **On-device model install:** wire `localJudge.ts::resolveModel` to the bundled
  GGUF path once the model-install path ships; until then the judge runs
  heuristic + silence fallback (still fully functional).
- **Locale:** `en` is hard-set today; plumb a user locale (tr-TR, …) through
  `createVoiceCore({locale})` — the seam exists end to end.
- **Collapse duplication:** migrate phone/watch/TV/web/glass onto
  `createVoiceCore`; retire `car-voice-coding.tsx`'s legacy PTT turn path
  (`runTurnFromUri`/`dispatchTurn`) once hands-free is device-verified; unify the
  Swift/Kotlin/Go summarizers behind the shared readback.
- **Go-side (optional):** per-turn "live car-voice channel" preamble into the
  runner, timestamped fragments, incremental token→TTS for a truer realtime feel.
```
