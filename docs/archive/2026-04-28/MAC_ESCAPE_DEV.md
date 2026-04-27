# Mac Escape Dev

## Goal

Make Yaver the default way for a React Native / Expo developer to work on Linux, WSL, or a remote dev box and still hot reload on a real iPhone.

The product promise is simple:

- You do not need a Mac for your daily iPhone dev loop.
- You may still need a Mac once to get the Yaver iOS app onto the phone.
- After that, your code can live on Linux, WSL, or a remote machine.
- Yaver agent runs near the code, builds Metro + Hermes there, and pushes the app into the Yaver iPhone container.

This is not "full native iOS development without Apple." It is "real iPhone React Native iteration without living inside a Mac workflow."

## Why This Matters

There is a real user here:

- Windows developer using WSL for all real work
- Linux laptop user who does not want a Mac as primary machine
- Remote dev user on a Hetzner box, home server, or cloud VM
- Team with one shared Mac for bootstrap/signing but not for daily development

Today that user usually gets stuck at one of these points:

- They can write RN code anywhere, but iPhone testing drags them back to a Mac
- Expo Go is not enough for their module set
- Remote dev environments are great until "test on real iPhone" enters the picture
- Existing iOS tooling assumes Xcode is central to the workflow

Yaver can make the iPhone part feel like Android already feels for many developers: a real device at the edge of a remote workflow, not the center of the workflow.

## Current Reality In Repo

The core architecture already supports most of this:

- `React Native / Expo` is the only framework with full push-to-device container support
- Hermes bytecode can be built on the host and pushed to the phone
- iPhone can load the guest app inside Yaver's super-host container
- Relay / LAN / Tailscale already give us "phone talks to remote agent"
- iOS install method already has `auto | native | bundle`
- `auto` resolves to `native` on macOS + Xcode, and `bundle` otherwise

Relevant references:

- `README.md` documents `iOS + cellular / relay` as Hermes HBC push into Yaver
- `CLAUDE.md` documents that `/dev/start` must stay Metro-only and must not fall back to `expo run:ios`
- `desktop/agent/device_install.go` defines `IOSInstallAuto` as native on macOS + Xcode, bundle otherwise
- `desktop/agent/mcp_tools.go` exposes install method control through MCP

So the platform is already close. The missing work is mostly productization, defaults, onboarding, and making the flow explicit instead of incidental.

## User Promise

For the target user, the experience should read like this:

1. Install Yaver mobile app on iPhone.
2. Pair phone with a Yaver agent running on Linux, WSL, or a remote box.
3. Point Yaver at an Expo / React Native project.
4. Tap `Open App`.
5. Yaver starts Metro on the host, bundles JS, compiles Hermes, pushes to phone, and loads it inside the Yaver app.
6. Edit code on Linux/WSL/remote machine.
7. Fast refresh or bundle reload lands on the real iPhone.

That is the story. Any fallback that silently reintroduces `expo run:ios`, `xcodebuild`, or "please use a simulator" breaks the product.

## Hard Boundary

We should be precise about what this does and does not mean.

Supported:

- Real iPhone testing for React Native / Expo guest apps inside Yaver
- Hermes bundle push from Linux, WSL, macOS, or remote machine
- Remote iteration over LAN, relay, or Tailscale
- Mobile-triggered reloads and AI-assisted fix loops

Not supported:

- Building arbitrary native iOS binaries on Linux or WSL
- Replacing Xcode for code signing, provisioning, or App Store submission
- Running custom native iOS modules unless they are already inside the Yaver host app or dev client path

The correct message is not "no Mac needed ever." The correct message is "no Mac needed for the daily JS iteration loop."

## Product Positioning

This should become a first-class message on the site and in docs:

- "Develop on Linux. Preview on real iPhone."
- "WSL to iPhone hot reload."
- "Remote React Native dev on a real device."
- "Use your iPhone like a thin client for your remote dev box."

The strongest framing is not anti-Mac. It is anti-Mac-dependence for day-to-day iteration.

## What To Do First

### 1. Make The Workflow Explicit In Docs

Ship a dedicated doc set for this flow:

- Linux / WSL -> iPhone quickstart
- Remote box -> iPhone quickstart
- "What still requires a Mac?" FAQ
- Troubleshooting: relay, Metro reachability, Hermes mismatch, unsupported native modules

README should state clearly:

- macOS is required for native iOS builds
- macOS is not required for Hermes push into the Yaver iPhone app
- Linux/WSL users are a primary supported path for RN/Expo hot reload

### 2. Make `bundle` The Obvious Path For Non-macOS

The product should be biased toward success:

- Non-macOS hosts should clearly show `iOS install method: bundle`
- UI copy should explain why: "native iOS install requires Xcode; Yaver will use Hermes push instead"
- Any flows that infer "iPhone means native build" need to be audited

### 3. Remove Dangerous Fallbacks

We already know the main failure mode:

- an agent or prompt falls back to `expo run:ios`
- a direct-build branch sneaks back in
- the user gets simulator behavior or a native build requirement they were trying to avoid

This needs explicit guardrails in:

- prompts
- mobile app open-app routing
- agent `/dev/start` handling
- docs and examples

### 4. Create A Simple Onboarding Funnel

The onboarding for this use case should be:

1. Install iPhone app
2. Pair with remote agent
3. Select project
4. Confirm framework is Expo/RN
5. Choose `Bundle to Yaver` mode
6. Tap `Open App`

If possible, skip any language about Xcode, device UDID, provisioning, or native install unless the user explicitly asks for native iOS builds.

## Engineering Workstreams

### A. Documentation

- Add a Linux/WSL/iPhone guide to `README.md`
- Add a remote-dev/iPhone guide to `README.md`
- Add troubleshooting table for relay/Hermes/Metro/native-module mismatch
- Add FAQ: "Do I need a Mac?" with a precise answer

### B. UX Copy

- Audit mobile labels like `Open App`, `Hot Reload`, `Build Native`, `Direct Build`
- For iPhone on non-macOS, show "Loads inside Yaver via Hermes bundle"
- Avoid language that implies Xcode is about to run

### C. Agent Defaults

- Confirm non-macOS defaults stay on `bundle`
- Confirm `auto` never picks a path that implies native iOS build outside macOS
- Add defensive logging when a request is redirected from native to bundle
- Add a clear reason in status output

### D. Prompt Safety

- Audit prompts sent to Claude Code / Codex / loop flows
- Ban `expo run:ios`, `xcodebuild`, `gradlew`, and similar commands in this workflow
- Make `/dev/start` and `/dev/build-native` the canonical path

### E. Compatibility Messaging

- Detect unsupported native modules earlier
- Explain whether the app can run inside Yaver's host container
- Provide a clean failure mode instead of "build succeeded but app behavior is broken"

## Concrete Repo Changes

These are the highest-value near-term edits:

1. `README.md`
Add a dedicated section for `Linux/WSL -> iPhone (Hermes push)` and `Remote dev box -> iPhone`.

2. `mobile/app/(tabs)/apps.tsx`
Audit open-app routing and ensure non-macOS / remote flows are clearly bundle-first in both behavior and copy.

3. `mobile/src/components/DevPreview.tsx`
Surface bundle-mode messaging more explicitly. This already hints that Hermes push is the default fast path.

4. `desktop/agent/device_install.go`
Keep the native-vs-bundle resolution simple, visible, and logged.

5. `desktop/agent/devserver.go`
Ensure the Metro/Hermes path is the primary and well-explained route for this workflow.

6. Prompt templates and fallback text
Audit anywhere the system might reintroduce `expo run:ios`.

## Success Criteria

We should consider this use case "real" when a developer can do the following without reading internal docs:

- Run Yaver agent on Linux, WSL, or remote host
- Pair an iPhone through Yaver mobile
- Open an Expo / RN app on the phone
- Change JS code and see it refresh on the iPhone
- Understand why the app runs inside Yaver rather than as a separately installed native app
- Understand in one sentence what still requires macOS

## Risks

### Native Module Expectations

Some developers will expect any RN app to work. That is only true when the needed native modules are present in the Yaver host container or compatible dev-client path.

We need better compatibility communication, otherwise the Mac-escape promise will feel fake.

### Terminology Confusion

Users may confuse:

- Yaver mobile app
- Yaver agent
- Yaver CLI
- native iOS install
- bundle push into container

Docs and UI need one consistent vocabulary.

### Overpromising "No Mac"

If we market this sloppily, users will read "iPhone development without Mac" as "compile, sign, archive, and ship iOS apps without macOS." That is false and avoidable.

## Recommended Messaging

Use this style:

- "Use Linux or WSL for daily React Native development. Hot reload on your real iPhone through Yaver."
- "Yaver pushes Hermes bundles to the iPhone app, so your daily dev loop does not depend on Xcode."
- "You still need macOS for native iOS builds and App Store shipping."

Avoid this style:

- "Build iPhone apps without a Mac"
- "Replace Xcode"
- "Native iOS development from Linux"

## Next Moves

Recommended order:

1. Update `README.md` with a dedicated Linux/WSL/iPhone section.
2. Audit UI copy for bundle-first non-macOS flows.
3. Audit prompts and fallbacks to eliminate `expo run:ios` regressions.
4. Add a compatibility explainer for native modules inside the Yaver host app.
5. Turn this into landing-page copy and a short demo video.

## Short Version

This is a real wedge:

- React Native / Expo developer
- code on Linux / WSL / remote box
- real iPhone in hand
- no Mac in the daily loop

Yaver already has most of the underlying plumbing. The job now is to make the workflow intentional, obvious, safe, and marketable.
