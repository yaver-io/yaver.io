# Yaver Feedback SDK — Kotlin / Android

Shake-to-report feedback for **native Android apps**, matching the contract the
RN, Flutter and Web SDKs already implement: capture context, POST it to the
Yaver agent at `/feedback`, and let the agent turn it into a work item.

```kotlin
// Application.onCreate
if (BuildConfig.DEBUG) {
    YaverFeedback.init(
        context = this,
        config = FeedbackConfig(
            agentUrl = "http://192.168.1.100:18080",
            authToken = BuildConfig.YAVER_SDK_TOKEN,
        ),
    )
}
```

That is the whole integration. Shake the device and a report is captured and
sent.

---

## Three ways to adopt this — the SDK stands alone

You do **not** need the Yaver app to use this. The SDK is a standalone product,
and "drop it in and point it at an agent" is a legitimate end state, not a
stepping stone.

| Path | What you run | What you get |
|---|---|---|
| **1. SDK only** | your app + the Yaver agent on your own machine | shake-to-report, screenshots, timeline, crash capture — filed straight into your dev loop. **No Yaver app, no Cloud Workspace, no account beyond an SDK token.** |
| **2. SDK + remote runtime** | your app in a simulator/Redroid on a box, streamed | the above, plus reviewing the app from your phone over WebRTC |
| **3. Full Yaver** | Cloud Workspace + agent + Yaver app | the above, plus the coding loop |

Path 1 is deliberately supported and deliberately small: one `init` call, one
token, one HTTP POST. If it only worked as part of a larger product it would be
a lock-in mechanism rather than an SDK.

---

## "Inside Yaver" means two different things

This distinction decides the whole design, and blurring it is how the SDK ends
up either silent or fighting the host.

| Sense | Mechanism | Which apps |
|---|---|---|
| **Inside the Yaver CONTAINER** | Hermes bytecode bundle loaded **in-process** by the mobile app | **React Native only** |
| **Inside a Yaver SESSION** | app runs in a **simulator / Redroid on the Cloud Workspace**, streamed to the phone over **WebRTC** | **Kotlin and Swift** — this is how native apps run inside Yaver |

A Kotlin app is never in the first sense — the container loads Hermes bytecode,
which is RN-only, so there is no mechanism by which a Kotlin app is loaded into
it. It is very much in the second.

**That is why this SDK has no suppression logic.** The RN SDK must check
`YaverInfo.isYaver` and disable its own shake handler, because in the container
the *container* owns shake (Reload / Back to Yaver) and two overlays would
fight. Adding the same check here "for symmetry" would be strictly wrong — it
would disable feedback in the one case it is needed.

### How shake reaches the app in a streamed session

```
phone shake
  → `shake` session command over the WebRTC events channel
  → box injects a HARDWARE shake into the simulator
      (adb sensor for Redroid, simctl for iOS)
  → the app's OWN SDK — this one, live in the simulator — fires its overlay
  → the overlay streams back to the phone as video
```

The phone's exit affordance lives in **native viewer chrome outside the video**,
so it and the in-sim overlay can never collide. See
`docs/architecture/yaver-preview-mode-and-self-development.md`.

So this SDK owns shake in both contexts that exist for a native app:

| Context | Who owns shake |
|---|---|
| **Streamed inside a Yaver session** (simulator / Redroid) | **this SDK**, firing in the sim |
| **Standalone** (Play, sideload, `yaver wire push`) | **this SDK** |

**Until this SDK existed, native Android had no in-app feedback at all** — the
loop was viewer-triggered only, via a `launch-feedback` control message pushed
down the WebRTC events channel. That still works and is the fallback when the
SDK is absent; this makes the in-app path available too.

---

## What it captures

- Device + OS + app version (`DeviceFBInfo`)
- A screenshot of the current window
- Optional user note
- Timeline events you add via `YaverFeedback.mark(...)`
- Recent unhandled exceptions, if `captureErrors` is on

It does **not** capture video or audio. The RN SDK does that through platform
APIs Yaver already ships in its container; for a standalone native app that
would mean requesting screen-record and microphone permissions, which is a much
larger ask than a bug report warrants. Add them deliberately, not by default.

---

## Configuration

| Field | Default | Notes |
|---|---|---|
| `agentUrl` | — | required — the Yaver agent, usually `:18080` |
| `authToken` | — | required — an SDK token, **never** a session token |
| `shakeEnabled` | `true` | set false to trigger only via `YaverFeedback.open()` |
| `shakeThresholdG` | `2.7` | acceleration in g before a shake registers |
| `captureScreenshot` | `true` | |
| `captureErrors` | `true` | installs an uncaught-exception handler that **chains** to the existing one |

### Security

`authToken` must be an **SDK token**, not a user session token. SDK tokens are
scoped to feedback submission; a session token would give an app shipped to
third parties the user's full agent authority. Keep it out of source control and
out of release builds — the guard above (`BuildConfig.DEBUG`) is the intended
shape.

---

## Files

| File | Purpose |
|---|---|
| `YaverFeedback.kt` | public API, lifecycle, submission |
| `ShakeDetector.kt` | accelerometer shake, debounced |
| `FeedbackTypes.kt` | wire types matching `desktop/agent/feedback.go` |

The wire format is `FeedbackReport` in `desktop/agent/feedback.go`. If that
struct changes, this SDK and the Swift one change with it — the field names are
the contract.
