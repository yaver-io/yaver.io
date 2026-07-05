# CarPlay entitlement request — voice runtime

> Status: submitted direction (2026-07-05). Companion:
> `docs/yaver-car-voice-coding.md`, `docs/yaver-tv-car-deployment-roadmap.md`.
> Without a granted entitlement the native CarPlay scene must stay disabled.

## Which entitlement, in what order

1. **`com.apple.developer.carplay-voice-based-conversation`** — lead with this. The real Yaver
   car use case is voice-first: a user launches Yaver in CarPlay, speaks a remote-runtime request,
   Yaver dispatches it to the selected machine, and Yaver speaks back a short status. Coding and
   Talos work are important examples, but the product category is broader than coding.
2. `com.apple.developer.carplay-communication` — later only if Apple steers us there. Yaver is
   conversational, but it is not primarily a human messaging or VoIP app and should not claim that
   category first.
3. `com.apple.developer.carplay-charging` / `com.apple.developer.carplay-driving-task` — separate
   future requests for EV charging or driving-specific utility surfaces. Do not mix these into the
   voice-runtime request.

## Submitted request direction (voice-based conversational)

> **App name:** Yaver IO
>
> **CarPlay app type:** Voice-Based Conversational
>
> **Tell us about your app:** Yaver IO lets users control their own computers and remote runtimes
> from iPhone. The CarPlay use case is a voice-first runtime assistant for safe hands-free use while
> driving: the user launches Yaver from CarPlay, speaks a short request such as "start the Talos
> build", "check the failing task", or "run the next step on my Mac", and Yaver sends that request
> to the user's paired machine. Yaver then speaks a concise status result back to the driver. Source
> code, logs, and task data stay on the user's own machine; Yaver's servers are used only for
> account, pairing, and relay/discovery.
>
> **What specific CarPlay features do you plan to implement?** We plan to implement the
> voice-based conversational CarPlay surface only. On launch, the primary modality is voice. The
> CarPlay UI will expose a minimal voice control screen to start/stop one active voice turn, show
> listening/working/speaking state, and return audio-only responses. Yaver will not display code,
> diffs, logs, long text, images, web views, or arbitrary project UI in CarPlay. It will only hold
> an audio session while actively listening or speaking. Risky commands such as deploy, push,
> delete, reset, or production changes require an explicit confirmation before dispatch. Requests
> to read code or diffs aloud are refused while driving and directed back to the phone when parked.

## Technical checklist before/after the grant
- [x] Add `CPTemplateApplicationScene` to the iOS app's `Info.plist` scene manifest.
- [x] Add the granted entitlement to the app's `.entitlements`.
- [x] Implement the voice-based conversational CarPlay scene using Apple's approved voice control
      surface for this category; keep Yaver's project/file UI out of CarPlay.
- [ ] Wire the scene to `mobile/src/lib/carVoiceEntry.ts` / `mobile/src/lib/carVoiceCoding.ts`.
- [ ] Test in the iOS Simulator's CarPlay environment with a development entitlement first.
- [ ] No text entry while in motion; all actions are voice-driven and spoken status readback only.

## Realistic timeline
- Entitlement decision: days to a couple of weeks after submission.
- **The Android side (Android Auto charging + messaging) has no entitlement gate and ships
  first** — don't block the EV car work on Apple.
