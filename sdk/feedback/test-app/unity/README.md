# Yaver Unity Test App

This is a minimal Unity consumer scaffold for the `io.yaver.feedback.unity` package.

It is intentionally small:

- package consumption example
- runtime bootstrap
- simple in-game feedback actions
- remote vibing and reload request demo hooks
- sample Editor build methods for desktop iteration

It is not a verified Unity project export yet. The purpose is to make the SDK integration path concrete while the package is still being hardened.

## Intended flow

1. Open this folder as a Unity project.
2. Confirm the local package path in `Packages/manifest.json` is valid on your machine.
3. Add the `YaverBootstrap` component to a scene object.
4. Wire a few UI buttons to `YaverFeedbackDemo`.
5. Point it at a local or remote Yaver agent.

## What this sample demonstrates

- initialize the SDK
- subscribe to command handling
- capture a screenshot
- upload feedback
- start a vibing task
- request reload/redeploy
- provide Unity `-executeMethod` targets via `YaverBuildTools`
- provide a simple content refresh hook via `YaverContentReloadDemo`
- provide a simple JSON gameplay-config flow via `YaverGameConfigApplier`

## Sample desktop build methods

The sample includes:

- `YaverBuildTools.BuildWindows64`
- `YaverBuildTools.BuildMacOS`
- `YaverBuildTools.BuildLinux64`
- `YaverBuildTools.BuildAndroid`

These are intended to be called by the Yaver agent through the new Unity build endpoint using `-executeMethod`.

## Sample content/config flow

The sample also includes:

- `YaverContentRefreshHandler`
- `YaverContentReloadDemo`
- `YaverGameConfig`
- `YaverGameConfigApplier`

That gives you a minimal remote-tunable loop:

1. agent sends a content refresh command with a URL
2. Unity downloads the payload
3. sample code parses it as JSON game config
4. new values are applied locally

## CI shape

This repo now has a Unity sample CI workflow that validates the sample project in GitHub Actions:

- EditMode tests for the sample project
- PlayMode tests for the sample project
- desktop build: `StandaloneWindows64`
- mobile build: `Android`

That gives Yaver a real Unity CI lane for both desktop and mobile-oriented paths without requiring a local Unity install on the SDK author's machine.

## Important note

For Unity mobile projects, reload should be interpreted as:

- restart scene
- refresh config/content
- rebuild/redeploy through the Yaver agent

not universal runtime code injection.
