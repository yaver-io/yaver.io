# Yaver native build fixtures

Real, minimal mobile apps used by `desktop/agent/native_build_fixtures_test.go`
to verify that yaver's native build pipeline (`yaver iosNative` / `yaver
androidNative` / `yaver flutter`, the matching `/builds` POST aliases, and the
`native_build` MCP tool) can build and push real apps over LAN.

| Fixture | Source | Stack | Build target |
|---------|--------|-------|--------------|
| `native-android-kotlin/` | Pure Kotlin Activity (no Compose) | Gradle 8 + AGP 8.2 + Kotlin 1.9 + JDK 17 | `gradle assembleDebug` → debug APK |
| `native-ios-swift/`      | SwiftUI iOS app                  | Xcode 15 + Swift 5.9 + iOS 15+         | `xcodebuild` → .app + xcrun devicectl install |
| `native-flutter-app/`    | Flutter / Dart                   | Flutter SDK + Dart 3                   | `flutter build apk --debug` → APK |

All three apps implement the same UX: **login screen with hardcoded credentials
(`admin` / `admin`) → dashboard greeting the signed-in user**. The
authentication helper is unit-tested in each fixture's native test framework
(JUnit / XCTest / `flutter test`).

## When to use these

1. After changing anything in `native_build.go`, `builds.go::resolveBuildCommand`,
   or the `/builds` POST handler — run `go test -run TestFixture ./desktop/agent/`
   on a host with the relevant toolchains. The tests skip themselves cleanly
   when (e.g.) Xcode isn't installed.
2. Before claiming a release supports `iosNative` / `androidNative` / `flutter`
   end-to-end. The fixtures are the proof.
3. When debugging a customer report ("yaver iosNative doesn't build my app"):
   reproduce against the fixture first to isolate yaver bugs from app bugs.

## LAN device push

```sh
# Connect a device or boot an emulator first
YAVER_TEST_LAN_DEVICE=android go test -run TestFixtureLANPush ./desktop/agent/
YAVER_TEST_LAN_DEVICE=ios     APPLE_TEAM_ID=XXXXXXXX go test -run TestFixtureLANPush ./desktop/agent/
```

The test will:
1. Build the fixture for the requested platform
2. `adb install -r` (Android) or `xcrun devicectl install` (iOS) the artifact
3. Verify `installStatus=installed` on the resulting `Build` record

Without `YAVER_TEST_LAN_DEVICE` set, the LAN push test is a no-op skip.

## React Native + Hermes is intentionally NOT here

These fixtures cover the **native** build path. The RN/Hermes push path is
covered separately by `desktop/agent/bundlecheck.go` + `mobile/sdk-manifest.json`
+ `cli/` (the `yaver-cli` npm package). Don't add an RN fixture here.
