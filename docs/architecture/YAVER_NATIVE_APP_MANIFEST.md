# Yaver Native App Manifest

`yaver.app.yaml` and `yaver.game.yaml` are the source-of-truth manifests for apps
that run inside the Yaver runtime without copying their source code into
`yaver.io`.

The manifest is intentionally separate from Apple `Info.plist`, Android
`AndroidManifest.xml`, Expo `app.json`, and store listings:

- the third-party app manifest declares what the app requires;
- the Yaver host binary declares what the host can provide;
- the catalog/review scanner compares both before a Yaver catalog release.

Private development, private deploys, and leaving Yaver do not require catalog
publishing. Catalog release is the point where Yaver can require source/package
review, reproducible build evidence, billing compliance, and native capability
audit.

## File Names

Yaver scans in this order:

1. `yaver.app.yaml`
2. `yaver.game.yaml`
3. `yaver.app.yml`
4. `yaver.game.yml`
5. `yaver.app.json`
6. `yaver.game.json`

The JSON files remain supported for existing apps. YAML is preferred for new
Yaver-native apps because it is easier for developers and agents to edit.

## Minimal Shape

```yaml
schemaVersion: 1
kind: game
id: game_sfmg
slug: sfmg
title: SFMG
owner: yaver

runtime:
  kind: yaver-strategy-game
  platformPositioning: strategy-games-first
  eventLogRequired: true

auth:
  provider: yaver-oauth
  requiredInYaverBuild: true
  standaloneAuthAllowedOutsideYaver: true
  requiredScopes:
    - openid
    - profile
    - yaver.apps.run
    - yaver.apps.events.write
    - yaver.ai.invoke
    - yaver.games.play
    - yaver.games.save

surfaces:
  - web
  - ios
  - android
  - tablet
  - tvos
  - android-tv

native:
  bundleMode: hosted-yaver-runtime
  host:
    requiresYaverOAuth: true
    requiredSurfaces: [ios, android, tablet, tvos, android-tv, web]
  apple:
    infoPlist:
      requiredKeys:
        - NSLocalNetworkUsageDescription
        - NSMicrophoneUsageDescription
      usageDescriptions:
        NSLocalNetworkUsageDescription: Yaver connects this game to the user's Yaver runtime and nearby development machines.
        NSMicrophoneUsageDescription: Yaver can pass voice commands to strategy games when the user enables voice input.
  android:
    permissions:
      - android.permission.INTERNET
      - android.permission.ACCESS_NETWORK_STATE
```

## Info.plist Boundary

Yaver-hosted apps must not mutate iOS or tvOS `Info.plist` dynamically. Apple
reviews the host binary and its declared capabilities, not a remote game
manifest downloaded later.

For hosted apps, `native.apple.infoPlist` means:

- "this app requires the Yaver host to already include these keys";
- "catalog review must block release if the host cannot satisfy them";
- "usage descriptions must be accurate for the Yaver host experience."

If an app needs its own bundle identifier, display name, URL scheme ownership,
entitlements, push topic, Apple Sign In service ID, or store listing, that is a
separate native binary / white-label release, not a hosted Yaver catalog app.

## iOS / Watch Remote OAuth

iPhone, iPad, Apple TV, Apple Watch, Android TV, Wear OS, car, and XR surfaces
must not pass raw Yaver bearer tokens around as a shortcut.

The approved remote-auth pattern is device-code OAuth:

1. The remote Yaver runtime starts auth or recovery:
   - CLI: `yaver auth --headless`
   - mobile recovery: `recoverAgent(..., "device-code")`
   - tvOS/watch standalone: `POST /auth/device-code`
2. The surface displays or forwards `https://yaver.io/auth/device?code=...`.
3. The signed-in phone opens that URL through universal links or Safari and
   approves the code.
4. The remote runtime polls and stores its own token locally.

Apple Watch and Wear OS are companion surfaces by default. They hold no token
while paired to the phone. Standalone watch mode can use device-code auth only
after explicit user opt-in, and then the token stays on the watch/box path that
requested it.

## Current Host Declaration

The Yaver iOS and tvOS plists now declare:

- `YaverNativeAppManifestSchemaVersion = 1`
- `YaverNativeOAuthProvider = yaver-oauth`
- `YaverNativeRuntimeKind = hosted-yaver-runtime`
- `YaverNativeRuntimeSurfaces = [...]`

These keys are for internal scanning and diagnostics. Store-facing metadata
still belongs to the Yaver app unless Yaver builds a separate app binary.

## SFMG

SFMG is integrated through its own `yaver.game.yaml` in the SFMG repo. Its source
is not copied into `yaver.io`. Kivanc and Serhat can use Yaver for sandbox,
remote runner, private deploy, and review preparation without being forced to
publish in the Yaver catalog.
