# Yaver on the wrist — watchOS and Wear OS

Handoff brief. Everything marked VERIFIED was checked against the running system,
Apple's watchOS 26.2 SDK, or the sources in this repo on 2026-07-10. Anything
uncertain is labelled UNVERIFIED — check it before you rely on it.

Companion docs: `docs/yaver-tvos-surface.md`, `docs/yaver-car-surface.md`.

---

## 0. The one-paragraph version

Both watch apps **exist and are honestly labelled scaffolds**. Both already do
voice-in over the platform's own dictation. Neither speaks back. Neither can hold
a terminal, and neither should try. The agent side of the loop shipped on
2026-07-10 (`POST /runner/session/turn`), and it is a better target than the
older `/watch/turn`, which spawns a *new task* instead of driving the session the
user already has running. The largest gap is not code: **nothing ships either
app.**

---

## 1. Platform constraints

### 1.1 watchOS CAN record audio. tvOS cannot. (VERIFIED)

```
$WATCH_SDK/.../AVAudioSession.h:120  recordPermission          … watchos(4.0, 10.0)  API_UNAVAILABLE(macos, tvos)
$WATCH_SDK/.../AVAudioSession.h:130  requestRecordPermission:  … watchos(4.0, 10.0)  API_UNAVAILABLE(macos, tvos)
```

This is the key asymmetry with the Apple TV. A watchOS app **may open the
microphone** (migrate to `AVAudioApplication`, the newer spelling). So unlike
tvOS, raw audio capture is on the table.

### 1.2 …but watchOS has no on-device speech recognition (VERIFIED)

```
$WATCH_SDK/System/Library/Frameworks/Speech.framework → No such file or directory
```

No `SFSpeechRecognizer`. So there are exactly two ways to turn wrist speech into
text:

- **System dictation** (what the app does today): present a text-input
  controller, let watchOS transcribe. Free, Siri-quality, no permissions.
- **Record and ship the audio** to something that can transcribe — the agent
  already runs whisper (`/voice/*` endpoints). This is the only path to
  continuous or hands-free capture, and it costs battery and bandwidth.

### 1.3 watchOS has no WebKit (VERIFIED)

```
$WATCH_SDK/System/Library/Frameworks/WebKit.framework → No such file or directory
```

No terminal, no web view, same as tvOS. `mobile/app/shell.tsx:32` (xterm.js in a
WebView) cannot be ported. It also does not need to be — see §1.5.

### 1.4 TTS is available on watchOS and unused (VERIFIED)

```
$WATCH_SDK/.../AVFAudio.framework/Headers/AVSpeechSynthesis.h   → present
$ grep -rn AVSpeechSynthesizer watch/                            → no matches
```

`AVSpeechSynthesizer` exists. The watch app currently answers with **one line of
text plus a haptic**. Speaking the reply is a small, high-value addition.

### 1.5 The pane is plain text (VERIFIED)

`tmux capture-pane -p` strips escape sequences. `/runner/session/turn` returns a
`pane` field that renders in a plain `Text`. No terminal emulator is needed on
any surface — but a watch should show a *summary*, not a pane. See §4.

### 1.6 Wear OS uses the system recognizer (VERIFIED)

```
wear/app/src/main/kotlin/io/yaver/wear/Dictation.kt:5   import android.speech.RecognizerIntent
wear/.../Dictation.kt:30                                Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
```

Android's `RecognizerIntent` is the equivalent of watchOS system dictation, and
Wear OS routes it to the watch's own UI. No `SpeechRecognizer` service, no
`TextToSpeech` anywhere in `wear/` — **so Wear OS also does not speak back.**

### 1.7 App Intents / Siri (UNVERIFIED)

`AppIntents.framework` and `Intents.framework` are both present in the watchOS
SDK, and unlike tvOS, watchOS **does** have Shortcuts. A custom App Intent is
therefore plausibly Siri-invocable ("Hey Siri, tell Yaver to…"). This was not
tested. Verify before promising it. The dictation path needs none of it.

---

## 2. What exists — Apple Watch (`watch/`)

`watch/YaverWatch.xcodeproj`, bundle `io.yaver.mobile.watch`,
`WKCompanionAppBundleIdentifier = io.yaver.mobile`. Its own README:
**"Status: scaffold (2026-06-17)"**. Roughly 1,300 lines of real SwiftUI — no
`TODO`/`fatalError` stubs, but not shipped.

| File | What it does |
|---|---|
| `Views/RootView.swift:7` | The whole interaction: `tap mic → Dictation.dictate() → store.sendTranscript(text)` |
| `Views/RootView.swift:62-78` | The record button, `Label("Speak", systemImage: "mic.fill")` |
| `Views/ConfirmView.swift` | Confirm/cancel a `confirm-needed` reply. The watch never auto-decides |
| `Views/SignInView.swift` | Standalone-only device-code sign-in; `TextField` for LAN host/IP (`:137`) |
| `Views/SettingsView.swift` | Pair status, "use without phone" opt-in |
| `WatchProtocol.swift:3` | **Single source of truth** for the v1 wire message |
| `PhoneSession.swift:77` | Default transport: `session.sendMessage(envelope, replyHandler:)`, envelope key `yaverWatch` |
| `AgentClient.swift:39` | Fallback transport: `POST http://<box>:<port>/watch/turn` + bearer |
| `Complications.swift:17` | Watch-face quick actions. The Widget Extension target is a **follow-up, not built** |

Two transports, one protocol:

- **Default — phone-paired.** Watch → iPhone over WatchConnectivity; the phone
  runs the `carVoiceCoding` loop. The watch stores no token, no host, no state.
  Phone side is real and mounted: `mobile/src/lib/watchEntry.ts`,
  `watchBridge.ts`, `WatchBridgeHost.tsx` (mounted at `mobile/app/_layout.tsx:168`),
  with headless tests (`watchEntry.test.mts`).
- **Fallback — standalone LAN.** Opt-in in Settings; the only mode where the
  watch holds a token.

## 3. What exists — Wear OS (`wear/`)

A real Kotlin app, standalone-capable
(`wear/app/src/main/AndroidManifest.xml:38 com.google.android.wearable.standalone`).

```
wear/app/src/main/kotlin/io/yaver/wear/
  Dictation.kt      RecognizerIntent.ACTION_RECOGNIZE_SPEECH
  PhoneBridge.kt    Wear Data Layer (MessageClient) → phone
  AgentClient.kt    OkHttp → POST /watch/turn (standalone LAN)
  WatchProtocol.kt  mirrors WatchProtocol.swift
  Backend.kt        device-code auth
```

Phone side: `mobile/native-wear/android/YaverWearListenerService.kt` receives the
Data Layer message on path `/yaver/watch/turn` and forwards it to JS.

**It is compiled** (`io/yaver/mobile/wear/YaverWearListenerService.class` exists
in the build output) and declared at `AndroidManifest.xml:89`.

Known hole, honestly commented at `YaverWearListenerService.kt:9`: if the phone
app's process is dead, the message is dropped. The correct fix is a
`HeadlessJsTaskService`; it is a `TODO`. So today an inbound turn requires the
phone app to have been opened at least once that process lifetime.

---

## 4. The agent contract — and which endpoint to use

### 4.1 `/watch/turn` — what the watches call today

`desktop/agent/watch_http.go`, route registered at `httpserver.go:868`.
Owner-auth, same bearer as every other client.

```
POST /watch/turn   {v, kind: "transcript"|"confirm"|"intent", text|token+reply|intent}
GET  /watch/result?taskId=…
```

Non-blocking by design — a wrist must never hold a 15-minute HTTP request open.
`dispatchWatchTranscript` (`watch_http.go:136`) **creates a task** and returns a
`taskId` to poll. Confirm is stateless: the `token` is base64 of the transcript.
Risk gate, read-code guard, intent expansion and one-sentence summarisation live
in `watch_risk.go`.

### 4.2 `/runner/session/turn` — what they should call (shipped 2026-07-10)

`desktop/agent/runner_session_turn.go`. This is the difference between *"start a
new task"* and *"keep developing in the session I already have running on
ubuntu"* — which is the actual product.

```jsonc
// request
{ "runner": "codex",        // or "session": "yaver-codex", or neither if only one is live
  "text":   "keep developing this",   // a prompt   (mutually exclusive with choice)
  "choice": "2",                      // a menu answer
  "waitMs": 6000 }

// response
{ "ok": true, "session": "yaver-codex", "runner": "codex",
  "sent": "prompt",
  "awaitingChoice": false,
  "options": ["1. Yes, I trust this folder", "2. No, exit"],
  "pane": "…plain text…" }
```

Four guards, each learned by breaking a real box — **do not remove them**:

- A **prompt** into a pane showing a menu returns `409` + `options[]`. Its
  submitting Enter would pick whatever is highlighted. A prompt sent while codex
  showed `1. Update now` ran `npm install` and killed the session.
- A **choice** when no menu is showing returns `409`. tmux types the digit into
  the composer as literal text, silently prefixing the next prompt
  (`"2reply with exactly …"`).
- A menu digit is sent **without Enter**. The digit confirms by itself; a trailing
  Enter answers the *next* modal blind — and claude's next modal renumbers `1` to
  `No, exit`. It quit.
- Menu state is read only after the pane stops redrawing, because a modal
  painting 200 ms late reads as "no menu".

**Menus chain and renumber. Never assume option 1 means yes.** On a watch this is
not a safety rail, it is the interaction model: `awaitingChoice` + `options[]`
becomes a two-button screen, which is exactly what `ConfirmView.swift` already is.

### 4.3 Sessions survive restarts now (shipped 1.99.280)

The tmux server holding every runner lived in `yaver.service`'s cgroup, so
systemd's default `KillMode=control-group` destroyed all sessions on any restart.
Fixed with `KillMode=process`. Verified: codex pid identical either side of a
restart, and it survives a binary swap. A watch can now safely assume the session
it spoke to yesterday still exists.

---

## 5. The loop, on both watches

```
system dictation (RecognizerIntent / watchOS text input)
   → POST /runner/session/turn {runner:"codex", text:"…"}
   ← {pane, awaitingChoice, options[]}
   → if awaitingChoice: two-button screen (ConfirmView already looks like this)
       → POST {choice:"2"}   (loop; menus chain)
     else: summarise `pane` to one sentence, show it, speak it, haptic
```

No terminal. No WebView. No custom recognizer. The watch shows a sentence and a
haptic, exactly as `RootView.swift` already does — it just needs to be pointed at
the session endpoint and given a voice.

---

## 6. What has been built (2026-07-10)

The gaps below were all VERIFIED on 2026-07-10 and have since been resolved.
Each bullet links to the commit that landed it.

- **Both watches speak.** `AVSpeechSynthesizer` (watchOS) and `TextToSpeech`
  (Wear) are now wired in. `watch/YaverWatch/Speech.swift` and
  `wear/.../Speech.kt` mirror `Haptics.swift`/`Haptics.kt` — `forReply(reply)`
  speaks the `spoken` field on every terminal reply, interrupts in-flight
  speech, and stays silent on `working` (transient state shouldn't talk over
  the user). This was the single highest-value addition (§8 build order #2).
- **Both watches call `/runner/session/turn`.** `watch/YaverWatch/SessionClient.swift`
  and `wear/.../SessionClient.kt` drive the LIVE coding session instead of
  spawning a new task. Maps `awaitingChoice` → `confirmNeeded`, `pane` →
  `summary` (with client-side summarization mirroring `watch_risk.go`), and
  auto-maps spoken numbers to `{choice}` when a menu is showing (§8 #1).
- **`withWatchBridge.js` is registered in `mobile/app.json`.** `expo prebuild`
  now injects the native WCSession/Wear Data Layer bridge on every prebuild
  (§8 #3). The watchOS companion target must still be embedded in the iOS app
  separately (one-time Xcode step — see `watch/README.md`).
- **Wear inbound turns survive a dead phone process.**
  `YaverWearBridgeModule.storePendingTurn` persists turns in SharedPreferences
  when the RN context is dead; `consumePendingTurns` drains them when the JS
  bridge mounts. Mirrors the car surface's `consumePendingReplies` pattern
  (§8 #4).

## 7. What still does not exist

- **No complication widget extension** on Apple Watch.

---

## 7. The good news about Apple Watch distribution

`WKCompanionAppBundleIdentifier = io.yaver.mobile` makes it a **companion app**:
it ships *embedded inside the iPhone build*. No separate App Store record, no
separate review, no separate version. Bundle IDs `io.yaver.watch` and
`io.yaver.mobile.watch` are already registered with Apple (VERIFIED via the App
Store Connect API).

So the watch is **not blocked by Apple.** It is blocked by us: register
`withWatchBridge.js` in `app.json`, embed the watch target, and it rides the next
TestFlight upload.

---

## 8. Build order

1. **Point both watches at `/runner/session/turn`.** The wire protocol
   (`WatchProtocol.swift` / `.kt`) already carries `transcript` and `confirm`;
   map `confirm` → `choice`, and `awaitingChoice` → `ConfirmView`. No new agent
   code.
2. **Give both watches a voice.** `AVSpeechSynthesizer` / `TextToSpeech` on the
   one-sentence summary. Highest value per line of code on this surface.
3. **Ship the Apple Watch app**: register `withWatchBridge.js`, embed the target,
   let it ride the iPhone build.
4. **Fix Wear's dead-process activation** (`HeadlessJsTaskService`).
5. Then, if wanted: continuous capture on watchOS (§1.1) streaming audio to the
   agent's whisper, for true hands-free.

---

## 9. Risks

- **`/watch/turn` and `/runner/session/turn` will diverge.** Decide now whether
  `/watch/turn` becomes a thin adapter over the session endpoint (recommended) or
  stays a separate task-spawning path. Two paths means two sets of guards, and
  the guards in §4.2 were paid for in dead sessions.
- **The standalone LAN mode stores a token on the wrist.** It is opt-in
  (`SettingsView`), and it is the only mode that does. Keep it that way.
- **Wear standalone reaches the box over plain LAN HTTP.** Same transport wall
  the Apple TV hits: a remote agent over the public internet needs a real
  certificate or a relay. See `docs/yaver-tvos-surface.md` §5.
- **The watch never auto-decides.** `ConfirmView` exists because a wrist confirms
  destructive work. `watch_risk.go` gates it. Keep both.
