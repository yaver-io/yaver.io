# Desktop Unity Yaver Feedback SDK Audit

## Executive Summary

This document evaluates Yaver for Unity desktop games as of April 23, 2026.

The conclusion is:

- Unity desktop is a strong fit for Yaver.
- It is technically easier than Unity mobile for feedback, remote control, QA tooling, and fast iteration.
- The primary product should still be a Unity SDK embedded in the developer's project, not a generic injector for arbitrary shipped games.
- Remote iteration can feel fast enough for casual and mid-core desktop teams through:
  - in-game feedback capture
  - in-game debug overlay
  - logs / screenshots / crash context
  - runtime commands and content refresh
  - remote build / relaunch / verify loops
- Managed cloud remains optional. The core path remains:

`game build -> Yaver SDK in project -> self-hosted machine or owned remote machine -> optional relay`

## High-Level Verdict

Desktop Unity is more favorable than mobile for Yaver for five reasons:

1. Desktop supports more flexible scripting backends and debugging workflows.
2. Relaunching a desktop build is cheaper and more reliable than reinstalling a mobile build.
3. Desktop overlays and debug consoles are easier to host and use.
4. Build artifacts, symbols, logs, and crash dumps are easier to gather on owned infrastructure.
5. Steam / itch / direct-download testing workflows naturally fit self-hosted Yaver.

So if the question is:

"Can Yaver work for Unity desktop games with roughly the same philosophy as React Native fast iteration?"

The answer is:

- yes in workflow terms
- no in exact runtime mechanism terms

The value is the loop, not Hermes itself.

## Product Fit

Best-fit segments:

- indie desktop Unity games
- casual PC games
- premium single-player titles in active QA
- closed alpha / beta branches
- Steam demo pipelines
- small studios with a shared build machine
- solo developers who want remote control from phone or laptop while away

Especially good fits:

- puzzle
- simulation-lite
- cozy / casual
- management
- roguelite prototypes
- controller-driven couch games

These teams often need:

- quick tester feedback
- screenshots with exact logs
- easy remote repro
- fast relaunch after a fix
- low operational overhead

That lines up directly with Yaver.

## What "Desktop Unity Support" Should Mean

For desktop Unity, Yaver should mean:

- Unity package inside the developer's project
- in-game overlay and command surface
- screenshots and optional local replay capture
- Unity log capture
- crash and exception capture
- scene / game-state metadata
- remote commands from a self-hosted Yaver agent
- content refresh where the game supports it
- build / relaunch / verify orchestration
- optional long-running remote agent session completion on another machine

It should not mean:

- universal binary patching for arbitrary shipped Unity desktop games
- guaranteed live code injection into every build
- a promise that all projects will hot reload gameplay logic without setup

## Technical Reality

### Why Desktop Is Easier Than Mobile

Unity's scripting back-end docs currently state that Mono is supported on:

- Windows x86/x64/Arm64
- macOS x64/Arm64
- Linux x64

and IL2CPP is supported on all platforms, with Mono the default on platforms that support both.

That matters because Mono-friendly desktop projects have a wider iteration surface than IL2CPP-first mobile projects. Inference from Unity's docs: desktop teams can more reasonably choose between:

- faster iteration and simpler managed tooling with Mono
- stronger production parity with IL2CPP

Source:

- Unity manual, scripting back ends overview:
  - https://docs.unity3d.com/ja/current/Manual/scripting-backends-intro.html

### What Yaver Can Do Reliably

For developer-controlled Unity desktop projects, Yaver can reliably provide:

1. Embedded feedback SDK
2. In-game overlay
3. Log streaming and filtering
4. Screenshot capture
5. Crash / exception capture
6. Scene and lifecycle breadcrumbs
7. Runtime command channel
8. Content/config refresh hooks
9. Remote rebuild + relaunch from another machine

Desktop also gives Yaver extra leverage:

- standalone players accept command-line arguments
- headless and batch execution exist for automation
- Windows desktop players can even be embedded under a parent window handle in some flows

Sources:

- Unity standalone player command-line arguments:
  - https://docs.unity3d.com/2019.4/Documentation/Manual/PlayerCommandLineArguments.html
- Unity command-line arguments overview:
  - https://docs.unity3d.com/kr/6000.0/Manual/CommandLineArguments.html

### What Yaver Can Sometimes Do

These are possible for some desktop projects, but should be treated as opt-in capabilities:

1. Managed-domain code refresh in Mono development builds
2. Plugin-based command extensions
3. Asset bundle or Addressables refresh
4. ScriptableObject / JSON / economy / level-data swap without full rebuild
5. Fast relaunch with preserved session context

This is the right place for Yaver's "reload" story on desktop.

### What Yaver Should Not Promise

Do not promise:

- universal live patching of Unity desktop gameplay code
- reliable hot code injection into IL2CPP builds
- generic attach-to-any-game post-build injection as the main product

Those are possible only in narrower architectures.

## Two Support Modes

Yaver should define two distinct desktop Unity modes.

### Mode 1: Embedded SDK Mode

This is the default and preferred mode.

The game developer:

- installs the Unity package
- initializes `YaverFeedback`
- optionally enables overlay/auth/content refresh handlers
- builds normally

Advantages:

- technically clean
- predictable support surface
- no reverse engineering or binary patching
- works with self-hosted philosophy
- easy to hand to a game developer friend

This is the sellable product.

### Mode 2: External Injection / Wrapper Mode

This is optional and secondary.

Possible variants:

- launcher process that starts the Unity build with Yaver-controlled arguments
- native shell around a Unity desktop player
- Mono/plugin loader based instrumentation in development builds
- mod-loader style instrumentation for consenting developer builds

This can be useful for internal tooling, but it should not be the main contract because:

- it is backend-specific
- IL2CPP weakens it significantly
- anti-cheat / distribution / platform policies can complicate it
- it is much less predictable than project-level SDK integration

## Reload Architecture

Desktop Unity should use a reload ladder, just like mobile, but with stronger desktop options.

### Level 1: Content Refresh

Fastest and most reliable.

Examples:

- reload JSON config
- refresh Addressables
- refresh remote config
- swap level packs
- reload localization tables
- retune balance values

This should be Yaver's preferred "hot reload" story for casual desktop games.

### Level 2: Scene Restart

Safe default.

If content refresh is not available, restart the active scene or jump to a debug scene with preserved context.

### Level 3: Relaunch Current Build

Desktop makes this much better than mobile.

The Yaver agent can:

- stop the current player
- rebuild if needed
- relaunch with command-line args
- restore last scene / session token / replay marker

This often feels good enough in practice.

### Level 4: Managed Patch Path

Only where the project explicitly supports it.

Examples:

- Mono dev builds
- custom script host
- plugin-based interpreted gameplay systems

This should be optional, not a default promise.

## Overlay Architecture

Desktop Unity is a very good place for a Yaver overlay.

The overlay should expose:

- sign-in / project selection
- connect to best agent
- screenshot
- annotate feedback
- attach black-box markers
- run custom debug commands
- start vibing
- request reload
- request relaunch/redeploy
- inspect recent logs
- inspect last crash / fix task state

Desktop-specific improvements beyond the current mobile-style overlay:

- resizable window
- keyboard-first mode
- controller-safe minimized mode
- detachable floating console panel
- searchable logs
- snapshot gallery
- debug command palette

This is also commercially validated. Jahro explicitly positions an in-game console, commands, logs, snapshots, and a web console for Unity 2021.3+ on iOS, Android, and desktop.

Sources:

- Jahro docs overview:
  - https://jahro.io/docs
- Jahro Unity plugin overview:
  - https://jahro.io/docs/unity/overview

## Feedback and Crash Architecture

The correct desktop Unity data model is:

### Local Runtime Capture

- Unity logs
- handled exceptions
- unhandled exceptions
- scene route
- session markers
- screenshot paths
- optional short replay/video
- platform metadata
- build metadata
- git sha / branch if available at build time

### Agent Intake

- receive structured bundle
- store artifacts locally by default
- trigger AI triage or fix tasks
- map report to project/repo/build
- keep session alive on local or remote machine

### Optional Cloud Layer

- remote relay
- artifact mirroring
- team dashboard
- issue triage history
- org auth / access control

Desktop games benefit from stronger native crash artifact flows too. Backtrace explicitly positions Unity support across Android, PC, and consoles, with minidumps on Windows, macOS, and Linux, and offers hosted or on-prem deployment. That validates both the desktop need and the self-hosted angle.

Source:

- Backtrace for Unity:
  - https://backtrace.io/unity

## Vibing Architecture

For desktop Unity, "vibing" should mean:

1. capture feedback or crash
2. ship context to the owned Yaver agent
3. let the agent inspect repo, logs, and recent session artifacts
4. let the session continue locally or on a remote owned machine
5. require tests/checks before completion when code changed

This aligns well with the handoff/session-complete work already in Yaver:

- continue on this machine until finished
- or continue on Hetzner / another owned machine while the developer sleeps

Desktop Unity is a good fit for this because the remote machine can:

- run editor scripts
- build desktop players
- launch smoke runs
- collect logs
- keep iterating longer than a human-attended session

## Self-Hosted Topology

Recommended topology:

### Local-First

- Unity game contains Yaver SDK
- SDK talks to a local Yaver agent on the developer machine
- optional access via Tailscale / relay / tunnel

### Away-From-Desk

- developer launches build on home machine or Hetzner box
- runs the game remotely or ships a dev artifact
- from inside the game overlay, presses:
  - send feedback
  - start vibing
  - reload/relaunch
- Yaver agent performs work on owned hardware

### Remote Continuation

- local machine can hand the session to a remote owned machine
- remote machine continues until done
- task completion requires tests/checks where feasible

This is one of Yaver's strongest differentiators versus SaaS-only tools.

## Market Landscape

The current market already validates much of the problem.

### Jahro

Signals:

- in-game debug console
- logs in build
- screenshots
- commands
- snapshots
- desktop support

Sources:

- https://jahro.io/docs
- https://jahro.io/docs/unity/overview
- https://jahro.io/docs/unity/logs

### Backtrace

Signals:

- Unity desktop crash and exception capture
- minidumps across Windows/macOS/Linux
- hosted or on-prem deployment

Source:

- https://backtrace.io/unity

### BetaHub

Signals:

- game-focused feedback workflow
- Unity plugin
- screenshots and video
- community / tester oriented pipeline

Sources:

- https://betahub.io/docs/
- https://betahub.io/docs/features/
- https://betahub.io/docs/integrations/game-engines/

### Bugsee

Signals:

- Unity SDK with crash and log capture
- relaunch options in SDK configuration
- proves the demand for runtime feedback tooling in Unity

Source:

- https://docs.bugsee.com/sdk/unity/configuration/

## Where Yaver Can Be Different

What the market already has:

- crash capture
- screenshots
- logs
- in-game console
- shareable sessions

What Yaver can own:

- self-hosted by default
- free local mode first
- direct report-to-agent workflow
- AI-assisted repo-aware fix loop
- remote session continuation on owned machines
- unified mobile + desktop + React Native philosophy
- reload ladder integrated with a self-hosted build/relaunch agent

That is a different product shape from pure bug-reporting SaaS.

## Recommended Architecture

### SDK Layer

Unity package in project:

- `YaverFeedback`
- `YaverOverlay`
- `YaverBlackBox`
- `YaverAuth`
- `YaverRuntime`
- `YaverP2PClient`

### Host Integration Layer

Project-specific hooks:

- `ContentRefreshRequested`
- `ReloadRequested`
- `ReloadBundleRequested`
- custom debug commands
- scene restore hook
- session resume hook

### Agent Layer

Self-hosted `yaver-go` agent:

- intake feedback bundles
- store artifacts
- trigger fix tasks
- run desktop build scripts
- relaunch players
- stream progress back
- hand off to remote owned machines

### Cloud Layer

Optional later:

- relay
- auth
- shared dashboards
- organization controls
- hosted artifact storage

## CI and Test Strategy

If the goal is low local setup, GitHub CI is the correct default.

Practical approach:

- Unity package tests via GitHub Actions
- GameCI container runner
- no local Unity install required for day-to-day SDK work
- desktop smoke builds later on owned runners if needed

This repo now has a package-level GitHub Actions workflow for Unity SDK tests. That covers the early "I am not a gamedev, I do not want local setup" use case well enough.

Current external references:

- GameCI GitHub Actions getting started:
  - https://game.ci/docs/github/getting-started/
- GameCI Unity test runner package mode:
  - https://game.ci/docs/github/test-runner/
- Unity Build Automation:
  - https://docs.unity.com/en-us/build-automation/run-builds

## Recommendation

Yaver should actively support desktop Unity.

But the contract should be:

"Install the Yaver Unity SDK in your game. Use your own machine or owned remote machine for feedback, vibing, reload, relaunch, and verification."

Not:

"Yaver can inject itself into any Unity desktop game and hot reload everything."

That distinction keeps the product technically honest and still commercially strong.

## Final Assessment

Desktop Unity is a good Yaver expansion.

It is:

- technically feasible
- more favorable than mobile for iteration
- compatible with self-hosted local-first philosophy
- aligned with casual and indie desktop workflows
- commercially differentiated if Yaver stays focused on:
  - embedded SDK
  - self-hosted agent
  - remote continuation
  - fast feedback-to-fix loop

If Yaver executes well, desktop Unity can be a real product lane, not just an edge-case SDK.
