# Yaver in the car — CarPlay and Android Auto

Handoff brief. Everything marked VERIFIED was checked against the App Store
Connect API, Apple's SDKs, or the sources and build output in this repo on
2026-07-10. Anything uncertain is labelled UNVERIFIED.

Companion docs: `docs/yaver-tvos-surface.md`, `docs/yaver-watch-surface.md`.

---

## 0. The one-paragraph version

The two car platforms are **not symmetric and should not be planned together.**
Android Auto is real, compiled, and shipping in the APK today. It posts genuine
MessagingStyle / CarExtender notifications, drains head-unit voice replies back
into JS, and Auto replies now drive `/runner/session/turn` so the user can keep
talking to the live Codex/Claude session from the car. The phone car voice loop
also publishes spoken status back into the same conversation. CarPlay is still
**blocked by Apple entitlement / provisioning**, but the compiled Swift scene is
now the voice-control template rather than a disabled placeholder. Neither
platform lets you display a terminal, and neither should. Both are voice pipes.

---

## 1. The hard blocker: CarPlay is not entitled (VERIFIED)

Queried live from the App Store Connect API:

```
io.yaver.mobile: 4 capabilities; CarPlay -> NONE
```

And the entitlements file itself carries no CarPlay key:

```
$ plutil -p mobile/ios/Yaver/Yaver.entitlements
  aps-environment, com.apple.developer.applesignin,
  com.apple.developer.associated-domains, …          (no com.apple.developer.carplay-*)
```

**Consequences, in order of importance:**

1. Without `com.apple.developer.carplay-voice-based-conversation`, the CarPlay
   scene will not load on a real head unit. Nothing you write changes this.
2. **Do not add the entitlement key speculatively.** Declaring an entitlement your
   provisioning profile does not carry makes the archive fail to sign — it will
   break the working iPhone build. `scripts/deploy-carplay.sh` already knows: it
   warns and hard-fails `--upload`.
3. Getting it is an **application to Apple** (`developer.apple.com/contact/carplay`),
   under the audio/communication category. There is a queue. File it independently
   of any code work; nothing shortens it.

Until it is granted, CarPlay work is speculative and untestable on hardware.

---

## 2. What exists — CarPlay

### 2.1 Compiled voice-control delegate (VERIFIED)

`Info.plist` wires the scene to `$(PRODUCT_MODULE_NAME).YaverCarPlaySceneDelegate`,
which resolves to the **compiled** overlay:

```
mobile/ios/Yaver/YaverCarPlaySceneDelegate.swift      CPVoiceControlTemplate
mobile/ios/Yaver/YaverCarPlaySceneDelegate.swift      ready/listening/working/speaking states
```

The tracked reference copy is the same shape:

```
mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift:6    private var voiceTemplate: CPVoiceControlTemplate?
mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift:39   CPVoiceControlTemplate(voiceControlStates: [ready, listening, working, speaking])
```

Four voice states is the right template shape. It still cannot be tested on real
hardware until §1 clears, and it does not yet bridge a native CarPlay action into
the JS car voice loop.

### 2.2 What CarPlay allows at all

CarPlay is **template-only**. You get Apple's templates (`CPListTemplate`,
`CPVoiceControlTemplate`, `CPAlertTemplate`, …). There is no custom drawing, no
scrolling text view, no terminal. Driver-distraction rules mean you will never
render a pane on a head unit. **Plan for voice in, voice out, and at most a short
list to pick from.**

That constraint is a good fit for `/runner/session/turn`: `awaitingChoice` +
`options[]` maps onto a list template; everything else is speech.

---

## 3. What exists — Android Auto (and it's further along than it looks)

### 3.1 It is a *messaging* app, not a car app (VERIFIED)

```xml
<!-- mobile/android/app/src/main/res/xml/automotive_app_desc.xml -->
<automotiveApp>
    <uses name="notification" />
</automotiveApp>
```

Only `notification`. There is no `androidx.car.app` / `CarAppService` anywhere;
`withAndroidAutoMessaging.js:46` says so explicitly — *"no CarAppService, no
androidx.car.app — that heavier IoT/EV surface is a…"*.

So Yaver appears to Android Auto as a **messaging app**: Auto reads notifications
aloud and offers a voice reply. That is the entire surface.

### 3.2 It IS wired and compiled (VERIFIED — earlier notes claiming otherwise were wrong)

The module's own header says *"REFERENCE IMPLEMENTATION. NOT yet wired into the
Android build by default"*, which is true only when the plugin is inactive. It is
active:

```
mobile/app.json plugins → withAndroidAutoMessaging: REGISTERED
mobile/plugins/withAndroidAutoMessaging.js:38   copies native-androidauto/android/*.kt into the generated app
mobile/android/app/build/tmp/kotlin-classes/release/io/yaver/mobile/car/YaverCarMessagingModule.class
mobile/android/app/build/tmp/kotlin-classes/release/io/yaver/mobile/car/YaverCarReplyReceiver.class
```

Compiled classes exist in the release build. **Android Auto messaging ships in
the APK today.**

### 3.3 What it does at runtime

`YaverCarMessagingModule.kt` posts a `NotificationCompat.MessagingStyle` with a
`CarExtender.UnreadConversation` and a `RemoteInput` reply action — the exact
shape Android Auto reads aloud and offers a voice reply for. The reply fires
`YaverCarReplyReceiver` (declared at `AndroidManifest.xml:88`), which re-emits the
captured text to JS as a `yaverCarReply` device event. If React Native is not
active yet, the receiver stores the reply in native SharedPreferences and
`subscribeCarReplies()` drains it when the car screen mounts.

`mobile/src/lib/carReplyDispatch.ts` receives it and routes through the
driving-safe JS car pipeline: risky commands require an explicit confirm, car
surface intents go through `/ops`, and coding replies from Android Auto drive
`/runner/session/turn` through `quicClient.runnerSessionTurn()`. The phone car
voice screen also posts normal spoken turn results back through
`presentCarConversation()`, so Android Auto has a live conversation even before
the first RemoteInput reply.

---

## 4. The agent contract

### 4.1 Live-session path: `/runner/session/turn` (shipped 2026-07-10)

`desktop/agent/runner_session_turn.go`. One call: a sentence in, a live session
driven, pane state back.

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
  "pane": "…plain text, ANSI already stripped…" }
```

Four guards, each learned by breaking a real box — **do not remove them**, and in
a car they matter more than anywhere else, because the driver cannot look:

- A **prompt** into a pane showing a menu returns `409` + `options[]`. Its
  submitting Enter would pick whatever option is highlighted. A prompt sent while
  codex showed `1. Update now` ran `npm install` and killed the session.
- A **choice** when no menu is showing returns `409`. tmux types the digit into
  the composer as literal text, silently prefixing the next prompt
  (`"2reply with exactly …"`).
- A menu digit is sent **without Enter**. The digit confirms by itself; a trailing
  Enter answers the *next* modal blind — and claude's next modal renumbers `1` to
  `No, exit`. It quit.
- Menu state is read only after the pane stops redrawing, because a modal painting
  200 ms late reads as "no menu".

**Menus chain and renumber. Never assume option 1 means yes.** A car surface must
speak the options aloud and take a spoken answer; it must never guess.

### 4.2 Do not use `/watch/turn` here

`watch_http.go:136 dispatchWatchTranscript` **creates a new task**. A car user
saying *"keep developing this"* means the session already running on their box,
not a fresh task. Same reasoning as the watch doc, §4.2.

### 4.3 Sessions survive restarts now (shipped 1.99.280)

`KillMode=process` in the agent's systemd units. Previously any agent restart —
upgrade, crash, reboot — destroyed every runner session, because the tmux server
lived in the unit's cgroup. Verified: codex pid identical either side of a
restart. A car client can assume yesterday's session still exists.

---

## 5. The loop, per platform

**Android Auto** (works today for live-session replies):

```
Phone car voice turn or Auto reads the notification aloud
   → user speaks a command/reply → RemoteInput → YaverCarReplyReceiver
   → yaverCarReply event → carReplyDispatch.ts
   → risk gate / car surface intent / POST /runner/session/turn
   ← awaitingChoice ? speak options : speak one-sentence pane summary
   → post the reply back as another MessagingStyle notification (Auto speaks it)
```

The whole surface is a conversation thread. The phone push-to-talk screen can
still create/poll a Yaver task; Android Auto replies prefer the live-session
branch so "keep developing this" continues the already-running pane.

**CarPlay** (after the entitlement lands):

```
CPVoiceControlTemplate: ready → listening → working → speaking
   → POST /runner/session/turn
   ← awaitingChoice ? CPListTemplate(options) : speak the summary
```

`mobile/ios/Yaver/YaverCarPlaySceneDelegate.swift` already has the four states.
When Apple grants the entitlement, add the matching entitlement/profile and then
bridge the native scene into the JS car voice entry point.

---

## 6. What has been built (2026-07-10) vs. what does not exist

**Built:**
- **Android Auto replies drive `/runner/session/turn`.** `carReplyDispatch.ts`
  now has a session path: when `sessionTurn` is provided, `handleCarReply`
  drives the LIVE coding session instead of spawning a new task. The car voice
  screen (`car-voice-coding.tsx`) wires `quicClient.runnerSessionTurn` as the
  transport. `awaitingChoice` is handled: the options are spoken as a question,
  the driver's spoken number is mapped to a choice digit, and menus chain
  (§7 build order #2).
- **CarPlay compiles `CPVoiceControlTemplate`.** The compiled delegate in
  `mobile/ios/Yaver/YaverCarPlaySceneDelegate.swift` now uses the four-state
  voice template (ready/listening/working/speaking), not the disabled label.
  Still untestable on hardware until the entitlement lands (§1).
- **Android Auto replies survive a dead JS bridge.**
  `YaverCarMessagingModule.consumePendingReplies` drains head-unit replies
  captured while the car voice screen wasn't mounted. No more dropped spoken
  commands.

**Still does not exist:**
- **CarPlay entitlement.** §1. Everything else on that platform is downstream.
- **CarPlay native-to-JS bridge.** The Swift voice template exists, but there is
  no entitled hardware path yet and no native action wired to `carVoiceEntryBus`.
- **No `androidx.car.app`.** Deliberate. A real Car App (maps/EV/IoT templates)
  is a much heavier surface and unnecessary for a voice pipe.

---

## 7. Build order

1. **File the CarPlay entitlement request today.** It is the only item with an
   external clock, and it blocks nothing else.
2. **Verify Android Auto with DHU.** The notification, `RemoteInput`, receiver,
   native pending queue, JS dispatch, and `/runner/session/turn` transport all
   exist and are compiled. Confirm the Desktop Head Unit reads the Yaver message
   aloud and that replies continue the live runner session.
3. **Harden live-session selection.** Today `runnerSessionTurn` lets the agent
   pick the only live session or return a spoken error. Add a driver-safe picker
   if multiple runner sessions are active.
4. **Handle `awaitingChoice` as a spoken question** in both platforms. Map "yes",
   "the first one", "one" → `choice: "1"`. Never infer.
5. **CarPlay**, when entitled: add the entitlement key at the *same time* as the
   profile refresh, then wire the native template to the JS car voice entry.

---

## 8. Risks

- **Adding the CarPlay entitlement early breaks the iPhone build.** Signing fails
  when the profile lacks it. `scripts/deploy-carplay.sh` guards this; do not
  bypass it.
- **Transport.** The car surfaces run inside the phone app, so they inherit its
  connectivity (QUIC/relay) and do **not** hit the LAN-only wall the Apple TV does
  (`docs/yaver-tvos-surface.md` §1.7). This is an advantage: a car can reach a
  remote box today, the TV cannot.
- **Driver distraction.** Neither platform will ever show a terminal. Anything
  that requires reading is the wrong design. If the answer cannot be spoken in one
  sentence, the correct behaviour is to say so and defer.
- **Destructive work.** The watch has `ConfirmView` and `watch_risk.go` for a
  reason. A car is worse: the user is driving. Gate writes and deploys behind an
  explicit spoken confirmation, and default to refusing.
- **`yaverCarReply` is emitted from the app's private native receiver, and the
  receiver is `exported=false`.** Keep it that way; do not add an external
  broadcast action for car replies.
