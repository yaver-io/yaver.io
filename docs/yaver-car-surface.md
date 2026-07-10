# Yaver in the car — CarPlay and Android Auto

Handoff brief. Everything marked VERIFIED was checked against the App Store
Connect API, Apple's SDKs, or the sources and build output in this repo on
2026-07-10. Anything uncertain is labelled UNVERIFIED.

Companion docs: `docs/yaver-tvos-surface.md`, `docs/yaver-watch-surface.md`.

---

## 0. The one-paragraph version

The two car platforms are **not symmetric and should not be planned together.**
Android Auto is real, compiled, and shipping in the APK today — it just relays
voice replies into JS rather than into a coding session. CarPlay is **blocked by
Apple**: the entitlement was never granted, and the template that actually
compiles is a single disabled label. Neither platform lets you display a
terminal, and neither should. Both are voice pipes. The agent side of the loop
already exists (`POST /runner/session/turn`).

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

### 2.1 Two delegates that disagree (VERIFIED)

`Info.plist` wires the scene to `$(PRODUCT_MODULE_NAME).YaverCarPlaySceneDelegate`,
which resolves to the **compiled** overlay:

```
mobile/ios/Yaver/YaverCarPlaySceneDelegate.swift:31   item.isEnabled = false
mobile/ios/Yaver/YaverCarPlaySceneDelegate.swift:33   return CPListTemplate(title: "Yaver", sections: [section])
```

A single `CPListTemplate` holding one **disabled** `CPListItem` that reads
*"Voice runtime ready. Use the iPhone voice loop for dictation and confirmation."*
It is a placeholder. It drives nothing.

The good version is tracked but **not compiled**:

```
mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift:6    private var voiceTemplate: CPVoiceControlTemplate?
mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift:39   CPVoiceControlTemplate(voiceControlStates: [ready, listening, working, speaking])
```

Four voice states. This is the right shape. Swapping it in is cheap; it just
cannot be *tested* until §1 clears.

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
captured text to JS as a `yaverCarReply` device event.

`mobile/src/lib/carReplyDispatch.ts` receives it. **What it does with it is the
open question** — today it dispatches into the JS coding pipeline, not into a
live tmux session.

---

## 4. The agent contract

### 4.1 Use `/runner/session/turn` (shipped 2026-07-10)

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

**Android Auto** (works today, needs only the dispatch changed):

```
Auto reads the notification aloud
   → user speaks a reply → RemoteInput → YaverCarReplyReceiver
   → yaverCarReply event → carReplyDispatch.ts
   → POST /runner/session/turn {runner, text}
   ← {pane, awaitingChoice, options[]}
   → post the reply back as another MessagingStyle notification (Auto speaks it)
```

The whole surface is a conversation thread. `awaitingChoice` becomes a spoken
question; the user answers with a number or a word you map to one.

**CarPlay** (after the entitlement lands):

```
CPVoiceControlTemplate: ready → listening → working → speaking
   → POST /runner/session/turn
   ← awaitingChoice ? CPListTemplate(options) : speak the summary
```

`mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift` already has the four
states. Swap it into the build when Apple grants the entitlement.

---

## 6. What does not exist

- **CarPlay entitlement.** §1. Everything else on that platform is downstream.
- **The good CarPlay delegate is not the one that compiles.** `Info.plist` points
  at the disabled-label version.
- **Neither car surface drives a live session.** Android Auto relays a reply into
  JS; CarPlay relays nothing.
- **No summarisation for speech.** `pane` is a screenful of text. A car needs one
  sentence. `watch_risk.go` already has one-sentence summarisation — reuse it
  rather than writing a second one.
- **No `androidx.car.app`.** Deliberate. A real Car App (maps/EV/IoT templates)
  is a much heavier surface and unnecessary for a voice pipe.

---

## 7. Build order

1. **File the CarPlay entitlement request today.** It is the only item with an
   external clock, and it blocks nothing else.
2. **Android Auto: point `carReplyDispatch.ts` at `/runner/session/turn`.** The
   notification, the `RemoteInput`, the receiver, and the JS event all exist and
   are compiled. This is the shortest path to vibing from a car, and it needs no
   permission from anyone.
3. **Reuse `watch_risk.go`'s summariser** so the car speaks one sentence, not a
   pane.
4. **Handle `awaitingChoice` as a spoken question** in both platforms. Map "yes",
   "the first one", "one" → `choice: "1"`. Never infer.
5. **CarPlay**, when entitled: swap in `mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift`
   and add the entitlement key at the *same time* as the profile refresh, never
   before.

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
- **`yaverCarReply` is a device event with no auth context.** Confirm that a
  spoken reply cannot be spoofed by another app posting the same event before
  wiring it to a session that can run code. (UNVERIFIED — check.)
