# Yaver watchOS — the thinnest voice terminal ("Yaver on the wrist")

> Status: **scaffold** (2026-06-17). Design: `docs/yaver-smartwatch-voice-terminal.md`.
> Decision (mirrors the tvOS ADR `docs/yaver-tvos-fork-adr.md`): native SwiftUI,
> **no `react-native-tvos` fork**, **not** an Expo config plugin. This directory
> is source-only: it is **not** wired into any build pipeline yet (same posture
> as `tvos/` and `mobile/plugins/withAndroidTV.js` before activation).

## What the watch is (and isn't)

The watch **owns nothing and shows nothing complex.** Voice in, **one short
sentence + a haptic** out. It renders no React Native, holds no agent, runs no
chromedp, and never shows code or a diff. Everything real — the Yaver runtime,
Claude/Codex sessions, redroid, chromedp/playwright, the personal-agent-gateway
CRUD over your credentialed apps — runs on the **remote runner** (self-hosted box
or managed cloud), exactly as it does for the car surface.

It is **async by design**: dispatch → "On it" haptic → background → wake on
completion. It never blocks the wrist on a remote task.

## Why this is separate from `mobile/`

watchOS does **not** run React Native, and ~90% of the RN app is touch-first UI
a wrist can't drive. Forking `react-native-tvos` would tax every future
`expo prebuild` for a surface that shares almost nothing with the phone app. So
the watch is a **small standalone SwiftUI app** following the `tvos/` precedent
exactly: a reproducible XcodeGen project (`watch/project.yml` →
`xcodegen generate`), bundle `io.yaver.mobile.watch`, team `5SJZ4KA39A`. Keeping it out
of the mobile prebuild entirely is the lower-friction path
(`docs/yaver-smartwatch-voice-terminal.md` §6).

## Transport — phone-paired first

| Mode | How | When | Status |
|---|---|---|---|
| **A. Phone-paired (DEFAULT)** | `WCSession.sendMessage` → the iPhone Yaver app runs the real `carVoiceCoding` loop and replies | Phone in pocket — the normal case | shipped in this scaffold |
| **B. Standalone LAN** | watch POSTs `/watch/turn` to `http://<box>:18080` with `Authorization: Bearer <token>` | Phone left at home, watch on Wi-Fi | scaffolded (`AgentClient.swift`), behind the "use without phone" opt-in |
| **C. Standalone over the internet** | QUIC relay + push-wake | Off-LAN | documented follow-up only — not in this scaffold |

In the **default** topology the watch holds **nothing sensitive**: no token, no
box host, no task state. The phone is the brain-of-record; the watch only ever
holds a `taskId` reference. Standalone (B/C) is an explicit opt-in (Settings →
"Use without phone") and is the *only* place the watch keeps a session token.

## Wire protocol v1 — `WatchProtocol.swift` is the single source of truth

Both transports exchange the **same** JSON messages. In WCSession they travel as
a dictionary `{"yaverWatch": "<json-string>"}`; in standalone mode the JSON is
the HTTP request/response body of `POST /watch/turn`.

### Watch → Phone / Agent

| `kind` | JSON | Meaning |
|---|---|---|
| `transcript` | `{"v":1,"kind":"transcript","text":"<spoken command>"}` | A spoken command |
| `confirm` | `{"v":1,"kind":"confirm","token":"<token>","reply":"confirm"\|"cancel"}` | Answer to a confirm-needed prompt |
| `intent` | `{"v":1,"kind":"intent","intent":"run-tests"\|"deploy"\|"status"}` | A complication quick-action |

### Phone / Agent → Watch

| `kind` | JSON | Meaning |
|---|---|---|
| `ack` | `{"v":1,"kind":"ack","spoken":"On it."}` | Accepted, fire-and-forget |
| `confirm-needed` | `{"v":1,"kind":"confirm-needed","token":"<token>","prompt":"That looks like a deploy command — confirm?"}` | Needs confirm/cancel first |
| `working` | `{"v":1,"kind":"working","taskId":"<id>","spoken":"Working…"}` | Long task started; wrist will be woken |
| `summary` | `{"v":1,"kind":"summary","taskId":"<id>","status":"completed","spoken":"Done. Tests pass."}` | Terminal, one sentence |
| `error` | `{"v":1,"kind":"error","spoken":"I couldn't reach your box."}` | Couldn't do it |
| `handoff` | `{"v":1,"kind":"handoff","target":"phone","spoken":"Sent it to your phone."}` | Sent to a bigger screen |

**The watch never decides what needs confirmation.** Every write/deploy/delete
verb is confirm-gated by the *phone* (or agent, in standalone): it replies
`confirm-needed` with a token + prompt, the watch shows `ConfirmView`, and the
user's choice goes back as a `confirm` message carrying the **same token**.

The phone→watch completion wake (a long task finishing while the watch app isn't
frontmost) arrives via `WCSession transferUserInfo` and is folded into the same
reduce path as a direct reply (`PhoneSession.lastPushedReply` → `WatchStore.absorb`).
The watch can't background-poll itself (`docs/yaver-smartwatch-voice-terminal.md`
§8), so the phone does the polling and pushes the summary.

## Standalone auth (mode B/C only)

Identical to `tvos/YaverTV/Backend.swift` — RFC 8628 device-code flow against
Convex:

```
POST /auth/device-code                      -> { userCode, deviceCode, expiresAt }
GET  /auth/device-code/poll?device_code=... -> { status, token? }
```

`SignInView.swift` shows a short code (primary; the QR is hard to scan on a
wrist) + a small QR, polls until an already-signed-in phone approves, then
collects the LAN box host. No new backend — the same contract `yaver auth` and
the tvOS app use.

## OAuth handoff for Yaver / Claude / Codex

The watch never opens provider OAuth itself. In phone-paired mode it asks the
iPhone to do the work; in standalone mode it shows/speaks a short code and
pushes a phone handoff. The phone then uses the same existing screens as mobile
and TV:

- **Yaver machine auth:** `https://yaver.io/auth/device?code=ABCD-1234` opens
  `app/approve-device.tsx` through Universal Links on iOS and App Links on
  Android.
- **Claude Code / Codex runtime auth:** `https://yaver.io/runner-auth/browser`
  or `yaver://runner-auth/browser` opens the mobile runner-auth browser flow.

For watchOS and Wear OS, the wearable only displays one sentence such as
"Approve Codex on your phone." The phone handles browser cookies, biometrics,
clipboard, provider redirects, and the final token write to the selected
runtime. That keeps OAuth off the tiny screen and avoids storing provider
credentials on the watch.

## Creating the Xcode target (one-time)

The repo intentionally does **not** check in an `.xcodeproj` (generated, churny).
Either run XcodeGen against `project.yml` or create the target by hand:

1. `cd watch && xcodegen generate` (preferred), **or** Xcode → New Project →
   **watchOS App**, SwiftUI lifecycle, name `YaverWatch`, bundle id
   `io.yaver.mobile.watch`, team `5SJZ4KA39A`, deployment target watchOS 10.0.
2. Delete any generated `ContentView.swift` / `App.swift`; add every file under
   `watch/YaverWatch/` to the target ("Create groups").
3. Set `Info.plist` to `watch/YaverWatch/Info.plist` (or merge its keys —
   it adds `NSMicrophoneUsageDescription`, `NSSpeechRecognitionUsageDescription`,
   `NSAllowsLocalNetworking`, and `WKCompanionAppBundleIdentifier=io.yaver.mobile`
   so watchOS can install it as the Yaver iPhone app's companion).
4. Complications: the quick-actions in `Complications.swift` need a separate
   **Widget Extension** target to appear on a watch face — that wiring is a
   follow-up. The intent + deep-link contract (`yaverwatch://intent/<name>`) is
   pinned now and the app already handles it via `.onOpenURL`.
5. Build & run on the watchOS Simulator or a paired Apple Watch.

Submission rides the iOS app's record (a watchOS app is an iOS companion);
budget the extra review/signing overhead (`docs/yaver-smartwatch-voice-terminal.md`
§8). Phone-paired mode needs no separate sign-in — the watch piggybacks the
phone's session.

## File map

| File | Role |
|---|---|
| `project.yml` | XcodeGen spec (platform watchOS, `TARGETED_DEVICE_FAMILY 4`, deploy target 10.0). |
| `YaverWatch/YaverWatchApp.swift` | `@main` App; injects `WatchStore`, activates WCSession, handles complication deep links. |
| `YaverWatch/WatchStore.swift` | `@MainActor ObservableObject` — transport routing, reply→(line+haptic) reduce, standalone creds, persistence. |
| `YaverWatch/WatchProtocol.swift` | **Single source of truth** for the v1 wire protocol (`Codable` request/reply + JSON envelope codec). |
| `YaverWatch/PhoneSession.swift` | `WCSession` wrapper — `sendTranscript`/`sendConfirm`/`sendIntent`, reachability, background completion wake. |
| `YaverWatch/AgentClient.swift` | Standalone `POST /watch/turn` over LAN HTTP, Bearer auth (mirrors `tvos` `AgentClient`). |
| `YaverWatch/Backend.swift` | Convex origin + device-code auth for standalone mode; `BoxTarget`. |
| `YaverWatch/Dictation.swift` | Watch dictation (`presentTextInputController`) → transcript; phone/AirPods STT preferred. |
| `YaverWatch/Haptics.swift` | `WKInterfaceDevice` haptics; maps reply kinds to tap/success/failure. |
| `YaverWatch/Complications.swift` | Quick-action intents + deep-link contract + minimal WidgetKit scaffold. |
| `YaverWatch/Info.plist` | Mic/speech usage strings + local-networking ATS exception. |
| `YaverWatch/Views/RootView.swift` | Raise-to-record → dictate → send → one big line + haptic; async working state. |
| `YaverWatch/Views/ConfirmView.swift` | Confirm/cancel for a `confirm-needed` prompt. |
| `YaverWatch/Views/SignInView.swift` | Standalone-only QR + short code device-code sign-in + box host entry. |
| `YaverWatch/Views/SettingsView.swift` | Pair status + the "use without phone" opt-in. |
