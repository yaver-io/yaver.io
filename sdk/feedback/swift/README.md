# Yaver Feedback SDK — Swift / iOS

Shake-to-report feedback for **native iOS apps**, matching the contract the RN,
Flutter, Web and Kotlin SDKs implement: capture context, POST it to the Yaver
agent at `/feedback`, let the agent turn it into a work item.

```swift
// AppDelegate / App.init
#if DEBUG
YaverFeedback.initialize(
    FeedbackConfig(
        agentURL: "http://192.168.1.100:18080",
        authToken: ProcessInfo.processInfo.environment["YAVER_SDK_TOKEN"] ?? ""
    )
)
#endif
```

UIKit only delivers the shake gesture to the first responder, so either use
`YaverShakeWindow` as your window class, or post `.yaverDeviceDidShake`
yourself.

---

## Three ways to adopt this — the SDK stands alone

You do **not** need the Yaver app.

| Path | What you run | What you get |
|---|---|---|
| **1. SDK only** | your app + the Yaver agent on your own machine | shake-to-report, screenshot, timeline, crash capture. **No Yaver app, no Cloud Workspace.** Reload stays your existing Xcode loop. |
| **2. SDK + remote runtime** | your app in an **iOS simulator on a Mac host**, streamed over WebRTC | the above, plus reviewing from your phone — and the rebuild happens on the host, not your laptop |
| **3. Full Yaver** | plus the coding agent | the above, plus the agent making the edits |

> ⚠️ **Path 2 needs a Mac host.** An iOS simulator requires macOS, so it cannot
> run on a Linux Cloud Workspace. Android's equivalent (Redroid) does run there
> today; **iOS remote runtime does not exist yet.** See
> `docs/architecture/native-reload-and-sdk-webrtc-audit.md` §6.

---

## "Inside Yaver" means two different things

| Sense | Mechanism | Which apps |
|---|---|---|
| Inside the Yaver **container** | Hermes bytecode loaded **in-process** | **React Native only** |
| Inside a Yaver **session** | app in a **simulator**, streamed over **WebRTC** | **Swift and Kotlin** |

A Swift app is never in the first sense — the container loads Hermes bytecode,
which is RN-only. **That is why this SDK has no suppression logic.** The RN SDK
must check `YaverInfo.isYaver` and disable its own shake handler, because there
the container owns shake and two overlays would fight. Adding the same check
here would disable feedback in the one case it is needed.

### How shake reaches the app in a streamed session

```
phone shake
  → `shake` session command over the WebRTC events channel
  → host injects a hardware shake with simctl
  → THIS SDK fires its overlay inside the simulator
  → the overlay streams back to the phone as video
```

The phone's exit affordance lives in native viewer chrome **outside** the video,
so it and the in-sim overlay cannot collide.

---

## Why there is no WebRTC view in this SDK

Deliberate. This SDK is **zero-dependency** — one `init`, one token, one POST.
Embedding `libwebrtc` would add tens of MB and a notorious source of version
conflicts to every app that only wanted shake-to-report, and it would put the
previewed content and the escape hatch back in one process.

The viewer is the Yaver app's job. If you want the live loop, the SDK offers a
deep link into it when installed. Full reasoning:
`docs/architecture/native-reload-and-sdk-webrtc-audit.md`.

---

## Security

`authToken` must be an **SDK token**, not a user session token. SDK tokens are
scoped to feedback submission; a session token in an app shipped to third
parties would hand them the user's full agent authority. Keep it out of source
control and out of release builds — the `#if DEBUG` above is the intended shape.

---

## Wire format

`FeedbackReport` in `desktop/agent/feedback.go`. **The field names are the
contract** — renaming one here fails silently at runtime rather than at build
time. If that struct changes, this SDK and the Kotlin one change with it.
