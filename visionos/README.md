# Yaver visionOS — native spatial runtime dashboard

> Status: **native app scaffolded** (2026-07-14). Mirrors the tvOS decision
> (`docs/yaver-tvos-fork-adr.md`): native SwiftUI, **no `react-native-visionos`
> fork**. Builds and uploads through `scripts/deploy-visionos.sh`.

## Why this is separate from `mobile/`

Same reasoning that produced the tvOS app, and it applies harder here. Stock
React Native + Expo cannot target visionOS; the `react-native-visionos` fork
would tax every future `expo prebuild` and every native overlay in
`mobile/ios/` — for a surface that is 90% touch-first UI you cannot drive with
eyes and pinch. So Vision Pro is a **small standalone SwiftUI app** that talks
to a Yaver agent over the **same** surfaces everything else uses.

There is a second, cheaper path Apple gives you for free: **Vision Pro runs
compatible iPad apps unmodified**, and apps are opted in by default. That gets
"Yaver on Vision Pro" with zero code — but it is an iPad window floating in
space, not a spatial app. `scripts/deploy-visionos.sh` supports both: it falls
back to analyzing/shipping the compatible iOS artifact when no native project
exists, and builds this one when it does.

## The client layer is SHARED with tvOS, not copied

`project.yml` pulls these straight out of `../tvos/YaverTV/`:

- `Backend.swift` — Convex base URLs + RFC 8628 device-code flow
- `Models.swift` — `BoxTarget`, `AgentInfo`, `AgentStatus`, `RunnerSessions`
- `AgentClient.swift` — `POST http://<box>:18080/ops` with a bearer token
- `YaverStore.swift` — token + box persistence
- `SessionClient.swift`, `YaverNativeCatalog.swift`

They are pure Foundation/SwiftUI with no platform-specific imports. Copying them
into a second app is how two surfaces silently drift apart; sharing them means a
new `ops` verb reaches TV and Vision Pro at once. **No new backend, no new agent
code** — the agent already serves every verb this app calls.

## Auth — the headset never asks for a password

Same device-code handoff as the TV: the headset shows a QR + short code, an
already-signed-in phone approves it. Typing a password into a floating keyboard
with your eyes is exactly as bad as it sounds.

## Scope (lean-back control room — by design)

1. **Machine panel** — host, platform, agent version, CPU.
2. **Runtime panel** — auth validity, task counts, dev-server state.
3. **Coding agents** — active runner sessions.
4. **Hot reload** — trigger a reload on the selected box.

Dense code authoring, raw logs, and text editing are intentionally **not** here,
for the same reason they are not on tvOS. A headset is a poor place to read a
stack trace. Vision Pro is the wall display / control surface while the real work
continues on a machine.

## Build

```bash
# generate the project (it is gitignored — edit project.yml, not the .xcodeproj)
cd visionos && xcodegen generate

# build only
./scripts/deploy-visionos.sh

# archive + upload to App Store Connect
$(yaver vault env --project mobile)   # or: source ~/.appstoreconnect/yaver.env
VISIONOS_MARKETING_VERSION=1.0.0 VISIONOS_BUILD_NUMBER=1 \
  ./scripts/deploy-visionos.sh --upload
```

Requires the visionOS platform component (`xcodebuild -downloadPlatform visionOS`
— ~7 GB). Having the SDK listed in `xcodebuild -showsdks` is **not** enough; the
platform itself must be installed or every build dies with "visionOS is not
installed".

## Signing

Automatic (`-allowProvisioningUpdates`), deliberately. `deploy-tvos.sh` pins its
profile **by name** with manual signing, and that broke the instant CarPlay was
enabled on the App ID — turning on any capability marks **every existing profile
INVALID**, on every platform. Automatic signing regenerates instead of dying.

Bundle ID is `io.yaver.mobile`, shared with the iPhone and TV apps for Universal
Purchase.
