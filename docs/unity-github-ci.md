# Unity Feedback SDK GitHub CI

This is the simplest way to test the Yaver Unity SDK without installing Unity locally.

## What runs in this repo

There are two Unity GitHub Actions workflows:

- `Unity SDK Tests`
  - validates the Unity package metadata
  - runs package `EditMode` tests for `sdk/feedback/unity`

- `Unity Sample CI`
  - runs sample project `EditMode` tests
  - runs sample project `PlayMode` tests
  - builds the sample project for:
    - `StandaloneWindows64`
    - `StandaloneLinux64`
    - `Android`
    - `WebGL`

Together, that gives you:

- package-level feedback SDK checks
- sample integration checks
- desktop build coverage on Windows and Linux
- mobile-oriented Android build coverage
- browser-oriented WebGL build coverage
- uploaded code coverage reports for package, EditMode, and PlayMode tests

## What you need in GitHub

You need a Unity license secret in the repository:

- `UNITY_LICENSE`

The current workflows fail early if that secret is missing.

## Recommended repo secrets

Minimum:

- `UNITY_LICENSE`

Optional later, if you split the workflow or move to different licensing flows:

- `UNITY_EMAIL`
- `UNITY_PASSWORD`
- `UNITY_SERIAL`

For the current repo, `UNITY_LICENSE` is the one that matters.

## When workflows run

`Unity SDK Tests` runs on:

- pushes to `main`
- pull requests touching:
  - `sdk/feedback/unity/**`
  - `scripts/validate-unity-package.mjs`
  - `.github/workflows/unity-sdk-tests.yml`

`Unity Sample CI` runs on:

- pushes to `main`
- pull requests touching:
  - `sdk/feedback/unity/**`
  - `sdk/feedback/test-app/unity/**`
  - `scripts/validate-unity-package.mjs`
  - `.github/workflows/unity-sample-ci.yml`

Both workflows also support manual `workflow_dispatch`.

## What to look at after a run

Check these first:

- step summary
- uploaded test artifacts
- uploaded sample build artifacts

Useful artifact groups:

- `unity-sdk-test-artifacts`
- `unity-sdk-coverage`
- `unity-sample-test-artifacts`
- `unity-sample-editmode-coverage`
- `unity-sample-playmode-test-artifacts`
- `unity-sample-playmode-coverage`
- `unity-sample-desktop-windows64`
- `unity-sample-desktop-linux64`
- `unity-sample-mobile-android`
- `unity-sample-webgl`

## What this CI does not cover yet

Not covered in GitHub-hosted Linux by default:

- iOS build validation
- macOS standalone build validation
- device install/run verification
- hardware-specific rendering/performance checks

Those should be added later with either:

- self-hosted macOS runners
- Unity Build Automation
- Yaver-managed remote runners

## Why this is enough for now

For the current Yaver Unity lane, the important thing is proving:

- the package stays importable
- the feedback SDK code keeps compiling
- core sample integration still works
- desktop/mobile-oriented build paths do not silently rot

That is the right CI bar before handing the SDK to a Unity developer friend.
