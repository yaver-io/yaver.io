# Yaver tvOS — native remote-runtime dashboard

> Status: **Apple TV App Store staged** (2026-07-05). Decision:
> `docs/yaver-tvos-fork-adr.md` (Option B — native SwiftUI, **no `react-native-tvos` fork**).
> This target builds and uploads through `scripts/deploy-tvos.sh`.

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
  The tvOS app calls `info`, `status`, `runner`, `voice`, `reload`, plus the existing
  `appletv_*` / `capture_*` verbs.

No new backend, no new agent code. The agent already serves every verb this app calls.

## Scope (lean-back runtime control — by design)

Shipped slice = the surfaces that are genuinely a 10-foot experience:

1. **Runtime control room** — machine status, dev-server status, Claude/Codex agent sessions,
   STT/TTS readiness, QR-based OAuth handoff, and hot-reload/Hermes-push controls
   (`info`, `status`, `runner`, `runner_auth`, `voice`, `reload`).
2. **Apple TV remote** — D-pad / transport / now-playing card (`appletv_*` verbs).
3. **Capture / now-playing** view of the home capture card (`capture_*`).

Dense code authoring, raw logs, and text editing are intentionally **not** on tvOS. The Apple TV
is the wall display/control surface while coding continues from MacBook terminal, Claude Code,
Codex, phone, or web. tvOS can trigger reloads and show whether the remote runtime is alive.

## QR auth handoff

Apple TV follows the same sign-in pattern users expect from streaming apps:

- Yaver account/runtime auth shows a QR plus a short code. The QR targets
  `https://yaver.io/auth/device?code=...`; the signed-in Yaver phone app opens
  the approver via Universal Links/App Links and authorizes the TV or remote
  machine.
- Claude Code and Codex auth is started on the selected runtime through
  `runner_auth browser_start`. tvOS renders the returned provider URL as a QR;
  the phone opens the system browser and completes OAuth/device-code handling.

The TV never asks for passwords, provider tokens, API keys, or long codes with
the Siri Remote. Watch, car, Android TV, and Android Auto should use the same
phone-mediated handoff shape.

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

Submission mirrors the iOS path (App Store Connect, same team/API key). The current staged
tvOS version has build `4`, export compliance set, and a 1920x1080 Apple TV screenshot uploaded.

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
| `Views/RuntimeDashboardView.swift` | Runtime control room: status, Claude/Codex sessions, voice, QR OAuth, reload, Apple surface readiness. |
| `Views/AppleTVRemoteView.swift` | D-pad / transport / now-playing. |
