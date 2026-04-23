# YAVER_BINARY_TOOL_MERGING

## Goal

Make Yaver feel like it has one install story instead of several unrelated ones.

The user expectation behind this audit is valid:

- `npm install -g yaver-cli` should feel like a real umbrella entry point.
- Feedback SDK setup should not feel disconnected from the Yaver binary.
- Flutter, Python, web, React Native, and future SDK users should have one obvious place to start.

The important constraint is that "single point" should mean:

- one canonical entry command for machine/bootstrap setup
- one canonical Yaver command for project/tool orchestration
- not "force every ecosystem to install through npm"

For Flutter and Python especially, package install still needs to happen through their native package managers. The unification should happen at the Yaver command layer, not by collapsing everything into one npm tarball.

## Executive Summary

`npm install -g yaver-cli` already does more than the repo copy suggests. It currently:

- installs a `yaver` command
- bootstraps the Go agent binary on first use
- runs a best-effort postinstall bootstrap for `yaver install mobile`

That means the agent/mobile bootstrap is already partially merged.

What is not merged today:

- Feedback SDK installation remains package-by-package:
  - React Native: `npm install yaver-feedback-react-native`
  - Flutter: `flutter pub add yaver_feedback`
  - Web: `npm install @yaver/feedback-web`
- Programmatic SDKs remain package-by-package:
  - JS: `npm install yaver-sdk`
  - Python: `pip install yaver`
  - Flutter/Dart: `flutter pub add yaver`
  - Go: `go get ...`
- Docs still present these as separate product surfaces instead of one Yaver-managed flow.

Conclusion:

- Yes, the install story should be unified further.
- No, the correct solution is not "put every SDK inside `npm install -g yaver-cli`".
- The right design is: make `yaver` the single orchestrator for all SDK/tool installation, while still delegating final dependency writes to the host ecosystem.

## Current State Audit

### 1. `yaver-cli` is already a bootstrap umbrella

Evidence:

- [cli/package.json](/Users/kivanccakmak/Workspace/yaver.io/cli/package.json) exposes `yaver`, `yaver-push`, `yaver-mcp`, and `yaver-cli`.
- [cli/src/postinstall.js](/Users/kivanccakmak/Workspace/yaver.io/cli/src/postinstall.js) does a global-install bootstrap:
  - prefetches the agent binary
  - runs `yaver install mobile`
- [web/public/llms.txt](/Users/kivanccakmak/Workspace/yaver.io/web/public/llms.txt) already treats `npm install -g yaver-cli` as the canonical install on any OS with Node.
- [README.md](/Users/kivanccakmak/Workspace/yaver.io/README.md) also positions `npm install -g yaver-cli` as the fastest start and a unified bootstrap.

Implication:

- The repo already moved toward "single entry" for machine bootstrap.
- The remaining gap is mostly SDK/project install orchestration and documentation consistency.

### 2. The repo currently has two different "single entry" concepts

There are two centers of gravity:

- `npm install -g yaver-cli`
  - machine bootstrap
  - agent bootstrap
  - RN push/mobile tooling bootstrap
- `yaver install <tool>`
  - dependency installer/catalog for tools and runtimes
  - examples: `node`, `mobile`, `cloudflared`, `docker`, `android-sdk`, `wda`

Evidence:

- [desktop/agent/install_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/install_cmd.go)
- [desktop/agent/install_registry.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/install_registry.go)

Implication:

- This is not inherently bad.
- But product/docs should present them as one layered flow:
  1. install `yaver`
  2. use `yaver` to install/configure everything else

Right now that layered story is not stated consistently.

### 3. Feedback SDK install is still fragmented by platform

Current install surfaces:

- React Native:
  - package: `yaver-feedback-react-native`
  - docs: [sdk/feedback/react-native/README.md](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/react-native/README.md)
- Flutter:
  - package: `yaver_feedback`
  - docs: [sdk/feedback/flutter/README.md](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/flutter/README.md)
- Web:
  - package: `@yaver/feedback-web`
  - docs/UI: [web/app/docs/feedback-sdk/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/docs/feedback-sdk/page.tsx)

Existing partial abstraction:

- `yaver expo setup` already installs `yaver-feedback-react-native` and patches Expo config when possible.
- Evidence: [desktop/agent/expo_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/expo_cmd.go)

Implication:

- There is already precedent for "Yaver command installs and wires SDK into project".
- That pattern should become the general model, not stay Expo-only.

### 4. Programmatic SDKs are also fragmented by ecosystem

Current install surfaces:

- JS: `yaver-sdk`
  - [sdk/js/package.json](/Users/kivanccakmak/Workspace/yaver.io/sdk/js/package.json)
- Python: `yaver`
  - [sdk/python/pyproject.toml](/Users/kivanccakmak/Workspace/yaver.io/sdk/python/pyproject.toml)
- Flutter/Dart core SDK: `yaver`
  - [sdk/flutter/pubspec.yaml](/Users/kivanccakmak/Workspace/yaver.io/sdk/flutter/pubspec.yaml)
- Go: module import path under `sdk/go/yaver`

Implication:

- Different ecosystems already require different native install commands.
- Unification must be command-driven and docs-driven, not package-name-driven.

### 5. Docs still frame these as separate products

Examples:

- [README.md](/Users/kivanccakmak/Workspace/yaver.io/README.md) separates:
  - desktop agent
  - npm bootstrap
  - feedback SDK
  - programmatic SDKs
- [web/app/docs/feedback-sdk/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/docs/feedback-sdk/page.tsx) presents direct package installs, not a Yaver command.
- [desktop/agent/feedback_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/feedback_cmd.go) only manages feedback reports, not feedback SDK setup.

Implication:

- Even if implementation is partly unified, the product narrative is still fragmented.
- This is why the install story feels split.

## Can Feedback SDK Be "Merged Into" `npm install -g yaver-cli`?

### Short answer

Not literally in the packaging sense, and it should not be.

### Why not

Feedback SDKs are embedded project dependencies, not machine-global utilities.

Examples:

- React Native SDK must be installed into the app's `package.json`.
- Flutter SDK must be written into the app's `pubspec.yaml`.
- Web SDK must be installed into the app's web project dependencies.

Installing them globally with npm would not make the host project depend on them, would not lock versions properly, and would not update project config files.

### What should happen instead

`npm install -g yaver-cli` should install the umbrella `yaver` command, and that command should be able to do:

- `yaver sdk add feedback`
- `yaver sdk add feedback --platform react-native`
- `yaver sdk add feedback --platform flutter`
- `yaver sdk add core --platform python`
- `yaver sdk add core --platform web`

Internally, `yaver` should:

- detect project type
- pick the native package manager
- install the right package into the project
- patch config files when needed
- print next steps

That gives a true single point without fighting ecosystem norms.

## Recommended Product Model

### Canonical install story

Use this model everywhere:

1. Install Yaver itself
   - preferred: `npm install -g yaver-cli`
   - fallback: `curl ... install.sh` / PowerShell / brew / apt / scoop
2. Use `yaver` for everything else
   - machine/runtime tools: `yaver install <tool>`
   - app/project SDK wiring: `yaver sdk add ...`
   - framework helpers: `yaver expo setup`, later generalized

### The two layers should be explicit

Layer 1: machine bootstrap

- install the `yaver` command
- install/prefetch agent binary
- install local runtimes/toolchains where Yaver owns them

Layer 2: project bootstrap

- install Yaver SDKs into the current repo/app
- patch app config files
- create `.env.yaver` or equivalent
- print/apply integration snippets

This keeps the system coherent.

## Recommended CLI Additions

### 1. Add a general `yaver sdk` command family

Recommended commands:

```bash
yaver sdk add feedback
yaver sdk add feedback --platform react-native
yaver sdk add feedback --platform flutter
yaver sdk add feedback --platform web

yaver sdk add core
yaver sdk add core --platform js
yaver sdk add core --platform python
yaver sdk add core --platform flutter

yaver sdk doctor
yaver sdk list
```

Responsibilities:

- auto-detect project type
- choose correct package manager
- install package into project
- patch config
- verify integration

### 2. Expand existing Expo precedent into a generic project setup layer

Current precedent:

- `yaver expo setup` installs RN feedback SDK and patches Expo config.

Recommended direction:

```bash
yaver app setup
yaver app setup feedback
yaver app setup feedback --platform react-native
yaver app setup feedback --platform flutter
```

Either command family is fine. The important part is one discoverable place.

### 3. Add project-native writers instead of global bundling

Per ecosystem:

- React Native / Web:
  - run `npm`, `yarn`, or `pnpm`
  - update `package.json`
- Flutter:
  - run `flutter pub add`
  - patch `pubspec.yaml` only when needed
- Python:
  - support `pip`, `uv add`, and possibly `poetry add` later
  - do not force plain `pip install` if a locked project manager is detected
- Go:
  - print import path and optionally patch sample bootstrap

### 4. Add setup subcommands under `yaver feedback`

Current `yaver feedback` is post-capture oriented:

- `list`
- `show`
- `fix`
- `delete`

Recommended additions:

```bash
yaver feedback setup
yaver feedback setup react-native
yaver feedback setup flutter
yaver feedback setup web
```

This is also reasonable because "feedback" is already a product noun inside the CLI.

## Concrete Gaps To Fix

### Gap 1. Naming inconsistency across docs

Today the user sees:

- `yaver-cli`
- `yaver`
- `yaver-feedback-react-native`
- `@yaver/feedback-web`
- `yaver_feedback`
- `yaver-sdk`
- `yaver` on PyPI
- `yaver` on pub.dev for core SDK

Recommendation:

- Keep package names if needed for ecosystem reasons.
- Standardize docs around one sentence:
  - "Install Yaver once, then use `yaver` to add the right SDK to your project."

### Gap 2. No generic feedback installer in the binary

Current state:

- Expo has one.
- Everything else is manual.

Recommendation:

- build feedback setup into the binary as first-class behavior
- start with:
  - Expo / React Native
  - Flutter
  - Web

### Gap 3. No generic programmatic SDK installer/orchestrator

Recommendation:

- add `yaver sdk add core`
- support JS, Python, Flutter first

### Gap 4. Machine bootstrap and project bootstrap are mixed in messaging

Recommendation:

- docs should separate:
  - "install Yaver on this machine"
  - "add Yaver to this app"

This removes confusion without losing the single-point story.

## Proposed End-State UX

### Machine bootstrap

```bash
npm install -g yaver-cli
yaver auth
```

### Add feedback to a React Native or Expo app

```bash
cd my-app
yaver feedback setup
```

Expected behavior:

- detects Expo or RN
- installs `yaver-feedback-react-native`
- adds plugin/config where needed
- prints integration snippet
- optionally runs a verify pass

### Add feedback to a Flutter app

```bash
cd my-flutter-app
yaver feedback setup
```

Expected behavior:

- detects Flutter
- runs `flutter pub add yaver_feedback`
- patches or prints root-widget integration instructions

### Add core SDK to a Python project

```bash
cd my-python-app
yaver sdk add core
```

Expected behavior:

- detects Python project
- prefers `uv add yaver` when `uv` project
- falls back to `pip install yaver`

### Add web SDK

```bash
cd my-web-app
yaver sdk add feedback
```

Expected behavior:

- detects web/js project
- installs `@yaver/feedback-web`

## Implementation Plan

### Phase 1. Docs and contract cleanup

Low risk, high value.

- Update README/install pages/docs to define:
  - `npm install -g yaver-cli` as the umbrella machine bootstrap
  - `yaver` as the single follow-up command surface
- Stop presenting feedback/core SDKs as detached first-touch install paths.
- Keep direct package commands in "manual/advanced" sections only.

### Phase 2. CLI orchestration for feedback SDKs

Add one of:

- `yaver feedback setup <platform?>`
- or `yaver sdk add feedback <platform?>`

Start with:

- React Native / Expo
- Flutter
- Web

### Phase 3. CLI orchestration for core SDKs

Add:

- `yaver sdk add core`
- project detection for JS/Python/Flutter

### Phase 4. Unify "tool install" and "SDK install" output

Make help text and docs consistently say:

- `yaver install ...` for machine tools
- `yaver sdk add ...` or `yaver feedback setup ...` for project dependencies

## What Should Not Be Done

### 1. Do not make Flutter/Python developers depend on npm for SDK embedding

Bad outcome:

- global npm install becomes mandatory even when project ecosystem is Python or Flutter
- project dependency state becomes invisible to native tooling

### 2. Do not globally install embedded SDK packages and call that "merged"

That would not:

- update the app's dependency manifest
- pin versions correctly
- integrate config changes

### 3. Do not hide package-manager side effects completely

The command can orchestrate everything, but it should still print what it is doing:

- package installed
- files changed
- next steps

## Recommended Decision

Adopt this principle:

> Yaver should have one canonical command surface, not one physical package artifact.

Concrete product decision:

- keep `npm install -g yaver-cli` as the preferred umbrella machine install
- keep other OS-native install methods as alternates that still end in the same `yaver` binary
- make `yaver` the single entry point for all SDK/tool setup
- implement first-class project setup commands for feedback/core SDKs

## Immediate Follow-Up Work

1. Add a new binary command for SDK setup:
   - preferred: `yaver sdk add ...`
   - acceptable: `yaver feedback setup ...` first, then generalize
2. Reuse logic from [desktop/agent/expo_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/expo_cmd.go) as the initial pattern.
3. Update docs so every install page starts with:
   - install `yaver`
   - use `yaver` to wire your app
4. Reserve direct package-manager instructions for manual fallback sections.

## Final Verdict

Yes, Yaver should move to a single developer install point.

The correct shape is:

- `npm install -g yaver-cli` (or equivalent OS-native install) gives you `yaver`
- `yaver` becomes the single setup/orchestration command for:
  - agent bootstrap
  - machine tools
  - feedback SDK wiring
  - programmatic SDK wiring

The merge should happen at the command/workflow layer, not by stuffing every SDK into the global npm package.
