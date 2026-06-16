# Yaver tvOS — thin native SwiftUI app

> Status: **scaffold** (2026-06-17). Decision: `docs/yaver-tvos-fork-adr.md` (Option B —
> native SwiftUI, **no `react-native-tvos` fork**). Roadmap: `docs/yaver-tv-car-deployment-roadmap.md`
> M-TV2. This directory is source-only: it is **not** wired into any build pipeline yet
> (mirrors `mobile/plugins/withAndroidTV.js` being unregistered before activation).

## Why this is separate from `mobile/`

Stock React Native + Expo cannot target tvOS, and the `react-native-tvos` fork would tax every
future `expo prebuild` / native overlay in `mobile/ios/` for a surface that is 90% touch-first
UI a Siri Remote can't drive. So Apple TV is a **small standalone SwiftUI app** that talks to a
Yaver agent over the **same** surfaces everything else uses:

- **Auth:** RFC 8628 device-code flow against Convex (`POST /auth/device-code`,
  `GET /auth/device-code/poll`) — identical to `mobile/src/lib/tvSignIn.ts` and `yaver auth`.
  The TV shows a QR + short code; an already-signed-in phone approves it.
- **Control:** `POST http://<box>:18080/ops` with `{ "verb": ..., "payload": ..., "machine": "local" }`
  and `Authorization: Bearer <session-token>` — identical to `mobile/src/lib/appletvClient.ts`.
  All the `appletv_*` / `capture_*` / `stream_*` verbs are already callable.

No new backend, no new agent code. The agent already serves every verb this app calls.

## Scope (lean-back only — by design)

Shipped slice = the surfaces that are genuinely a 10-foot experience:

1. **Apple TV remote** — D-pad / transport / now-playing card (`appletv_*` verbs).
2. **Capture / now-playing** view of the home capture card (`capture_*`).
3. **Device + agent status**.

Code authoring, the agentic UI, forms, tabs — intentionally **not** here. They are phone/web
surfaces; a remote can't drive them. (Documented as out-of-scope, not "missing".)

## Transport note

This scaffold targets a box reachable on the **same LAN** (the common case: an Apple TV and a
Raspberry Pi running `yaver serve` on the home network). Enter or discover the box host once;
the bearer token from device-code auth authorizes `/ops`. Relay (QUIC) fallback for off-LAN
boxes is a documented follow-up — the mobile app's `quicClient.callOpsOnDevice` is the
reference; tvOS would need a Swift QUIC client or a relay HTTP shim.

## Creating the Xcode target (one-time)

The repo intentionally does **not** check in an `.xcodeproj` (generated, churny). To build:

1. Xcode → New Project → **tvOS App**, SwiftUI lifecycle, name `YaverTV`,
   bundle id `io.yaver.tv`, team `5SJZ4KA39A` (same as the iOS app).
2. Delete the generated `ContentView.swift` / `App.swift`; add every file under
   `tvos/YaverTV/` to the target (drag the folder in, "Create groups").
3. Set `Info.plist` to the one in `tvos/YaverTV/Info.plist` (or merge its keys).
4. Build & run on the tvOS Simulator or a real Apple TV.
5. Sign-in: scan the QR with the Yaver phone app, approve — the TV gets a 1-year session.

Submission later mirrors the iOS path (App Store Connect, same team/API key). A legit
remote-control app passes review with Siri Remote support + tvOS HIG focus behavior.

## File map

| File | Role |
|---|---|
| `YaverTVApp.swift` | `@main` App; injects `YaverStore`. |
| `Backend.swift` | Convex origin + device-code auth (create + poll). |
| `AgentClient.swift` | `POST /ops` to a box over LAN HTTP, Bearer auth. |
| `Models.swift` | `Codable` for now-playing, capture status, devices. |
| `YaverStore.swift` | `@MainActor ObservableObject` — session token, selected box, persistence. |
| `Views/SignInView.swift` | QR + short code, polls until approved. |
| `Views/DashboardView.swift` | Lean-back tile launcher. |
| `Views/AppleTVRemoteView.swift` | D-pad / transport / now-playing. |
