# Unity Unified Yaver

## Purpose

This document is the unified audit and architecture reference for Yaver's Unity direction as of April 23, 2026.

It answers four questions:

1. What do we already have?
2. What do we not have yet?
3. What should Unity support mean inside Yaver?
4. What should we build next?

The goal is to make Unity a real Yaver lane for:

- solo developers
- small game studios
- publisher/tester workflows
- self-hosted home machines
- rented VPS / GPU runners
- optional managed cloud later

This document covers both:

- Unity mobile
- Unity desktop

because the product should stay unified even if the runtime constraints differ.

## Core Product Thesis

Yaver for Unity should not be framed around Hermes, React Native internals, or universal runtime code injection.

It should be framed around:

- fast iteration
- in-app feedback
- self-hosted build/reload/relaunch loop
- AI-assisted fix loop
- remote continuation on owned machines
- phone as control surface

The right thesis is:

`Unity app with Yaver SDK -> self-hosted Yaver agent -> optional relay / remote runner -> feedback, vibing, reload/relaunch, summary, verification`

That works for solo developers and scales up to teams.

## What We Have Today

### 1. Unity package scaffold

We already have a Unity Package Manager package under:

- [sdk/feedback/unity/package.json](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/package.json:1)
- [sdk/feedback/unity/README.md](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/README.md:1)

This is the correct delivery format. In Unity terms, this is effectively the plugin/package.

### 2. Runtime SDK primitives

Current runtime files:

- [YaverFeedback.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverFeedback.cs:1)
- [YaverFeedbackTypes.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverFeedbackTypes.cs:1)
- [YaverP2PClient.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverP2PClient.cs:1)
- [YaverBlackBox.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverBlackBox.cs:1)
- [YaverRuntime.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverRuntime.cs:1)
- [YaverAuth.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverAuth.cs:1)
- [YaverOverlay.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverOverlay.cs:1)
- [YaverDiscovery.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverDiscovery.cs:1)
- [YaverSseDownloadHandler.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Runtime/YaverSseDownloadHandler.cs:1)

Current capabilities already implemented:

- package-level Unity SDK
- config model
- agent discovery
- OAuth kickoff + deep-link callback token consumption
- email login/signup
- stored token reuse
- cloud-backed agent discovery via `/devices/list`
- screenshot capture
- Unity log capture
- scene/lifecycle black-box capture
- feedback upload
- feedback -> fix trigger
- vibing request
- reload/redeploy request
- in-app overlay
- command stream listener
- crash auto-reporting path

### 3. Current reload semantics

Unity reload is already modeled as a strategy rather than a single hardcoded action.

Existing strategies effectively include:

- `content`
- `custom`
- `scene`
- `remote`
- `redeploy`
- `relaunch`

That is the correct shape. The product already acknowledges that Unity reload is a ladder.

### 4. Desktop-aware direction has started

The Unity config now also has fields for:

- `RuntimeProfile`
- `DeploymentMode`
- `TeamName`
- `RunnerName`
- `RunnerUrl`

This is the right start for:

- solo local machine
- self-hosted remote runner
- team/studio routing

### 5. Overlay exists

The Unity SDK already has an in-app overlay via `OnGUI`.

Current overlay capabilities:

- auth
- connect
- screenshot
- feedback send
- fix trigger
- vibing
- reload request
- activity log

This is enough for a first dev/tester surface.

### 6. Unity CLI integration exists

The desktop agent already detects Unity projects and exposes Unity in the CLI path.

Key behavior already added:

- `yaver doctor` detects Unity
- `yaver sdk add feedback --platform unity`
- Unity project detection via `ProjectSettings/ProjectVersion.txt` and `Packages/manifest.json`

So Unity is not just a side doc; it is already entering the product surface.

### 7. GitHub CI path exists

We already added Unity package test wiring:

- dedicated workflow:
  - [.github/workflows/unity-sdk-tests.yml](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/unity-sdk-tests.yml:1)
- integrated CI job:
  - [.github/workflows/ci.yml](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/ci.yml:1)
- package tests:
  - [sdk/feedback/unity/Tests/Editor/Yaver.Feedback.Tests.asmdef](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Tests/Editor/Yaver.Feedback.Tests.asmdef:1)
  - [sdk/feedback/unity/Tests/Editor/YaverFeedbackConfigTests.cs](/Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/unity/Tests/Editor/YaverFeedbackConfigTests.cs:1)

This means local Unity install is not required for basic package verification.

### 8. Session completion / remote continuation groundwork exists

The agent side already has session-complete / handoff semantics for:

- continue on this machine
- continue on another owned machine

That is important for Unity because vibing is not just about editing code. It also needs:

- test runs
- build runs
- relaunch / verify
- summary back to phone

The current handoff/session-complete work is a strong base for that.

## What We Do Not Have Yet

### 1. Real Unity sample project verification

We have scaffolding and test-app structure, but not a fully verified Unity editor import/build loop in this environment.

Missing:

- verified sample project import
- compile proof across a selected Unity version
- smoke run proof
- package install proof for both mobile and desktop targets

### 2. True content adapters

The SDK has hooks for `content` reload, but not the actual adapters.

Missing adapters:

- Addressables refresh
- Remote Config refresh
- JSON gameplay config refresh
- asset-bundle refresh
- save-state/session-preserving scene reload

These are critical because content refresh is the closest honest Unity equivalent to fast reload.

### 3. Desktop relaunch implementation

We have the architectural direction, but not the full agent+SDK path for:

- stop running desktop player
- rebuild if needed
- relaunch player with args
- restore context
- send results back to phone

Desktop Unity becomes materially stronger once this exists.

### 4. Video/replay capture

We have screenshots and logs, but not proper short replay/video capture yet.

This is a major gap for:

- hypercasual
- casual
- gameplay-heavy QA
- remote triage

### 5. Better overlay UX

The current `OnGUI` overlay is useful but still rough.

Missing:

- production-quality mobile layout
- desktop resizable panel mode
- keyboard-friendly desktop mode
- searchable logs in overlay
- richer activity/task status
- better controller-safe visibility behavior

### 6. Studio/team routing

We have config fields for team/runner awareness, but not the real multi-tenant/team flow yet.

Missing:

- team/project binding
- runner selection policy
- shared box auth/access control in Unity-side flows
- solo-first defaults with optional team escalation

### 7. Strong vibing summary protocol

We do not yet have a first-class schema for:

- current task
- changed files
- tests run
- tests passed/failed
- build result
- relaunch result
- residual risk
- next step

This matters because the phone should show useful summaries, not raw logs.

### 8. Streaming / remote viewport

We do not yet have:

- screenshot streaming
- live preview transport
- VNC/RDP/WebRTC integration path
- "watch while the remote machine keeps working" UX

This is optional for V1, but important for the longer-term Unity story.

### 9. Crash artifact enrichment

We capture managed-side crash context, but we do not yet have a full platform-native symbol/minidump strategy for desktop.

Missing for desktop-grade workflows:

- Windows PDB-aware crash enrichment
- macOS dSYM-aware enrichment
- Linux symbol-aware enrichment
- crash artifact upload policy

### 10. Unity-specific build/test helpers in agent

We do not yet have a complete Unity-specific agent layer for:

- running EditMode tests
- running PlayMode tests
- building desktop players
- building Android/iOS from known project path
- launching smoke scenes
- detecting failures and surfacing them in a structured summary

## What Unity Support Should Mean

Unity support in Yaver should mean:

### For solo developers

- install one Unity package
- run one self-hosted Yaver agent
- use phone or in-app overlay to:
  - send feedback
  - trigger vibing
  - reload/relaunch
  - read summaries
- avoid mandatory cloud

### For teams/studios

- same Unity package
- same SDK shape
- same overlay/feedback primitives
- but optionally point to:
  - home machine
  - office workstation
  - Hetzner/GPU VPS
  - private remote runner with local LLM

That means there should not be two different Unity products. It should be one product with:

- solo-first defaults
- team/rented-runner escalation path

## Architectural Modes

Yaver for Unity should be designed in three modes.

### Mode A: Solo Self-Hosted

Default path.

Components:

- Unity package in project
- Yaver agent on the developer's machine
- optional relay/Tailscale/tunnel
- phone as control surface

This should be the simplest and cheapest path.

### Mode B: Remote Owned Runner

For users who want to sleep while the agent keeps going.

Components:

- Unity package in project
- local or remote handoff
- session continues on Hetzner/home box/shared workstation
- test/build/relaunch keeps running there

This mode is already aligned with the existing session-complete/handoff work.

### Mode C: Studio Shared Runner

For teams reducing SaaS AI spend by running a strong local model or controlled infra.

Components:

- same Unity package
- team auth/project selection
- controlled runner box
- local LLM / self-hosted model
- shared artifact storage if desired

This should be an extension of the same architecture, not a fork.

## Mobile vs Desktop

The SDK should stay unified, but product promises must differ.

### Unity Mobile

Good promises:

- in-app feedback
- screenshots/logs/crash context
- remote config/content refresh
- scene restart
- rebuild/redeploy

Bad promises:

- universal live code injection
- Hermes-equivalent runtime patching

### Unity Desktop

Good promises:

- in-app feedback
- stronger overlay/debug console
- screenshots/logs/crash context
- content refresh
- scene restart
- relaunch current build
- remote build + smoke run + summary

Desktop is a stronger fit than mobile for:

- relaunch loops
- remote view/stream
- richer overlay
- longer unattended vibing sessions

## Plugin or SDK?

For Unity this should be both, in the normal Unity sense:

- technically: a Unity SDK
- delivery form: a Unity package/plugin via UPM

So the answer is:

- yes, it is a Unity plugin/package
- yes, it is also the Yaver feedback SDK for Unity

The correct shipping artifact remains:

- `io.yaver.feedback.unity`

## Reload Architecture

This is the most important technical framing.

Unity "reload" should be a ladder:

### 1. Content Refresh

Preferred whenever possible.

Targets:

- Addressables
- Remote Config
- JSON data
- balance tables
- level definitions
- tutorial flow data

### 2. Scene Restart

Safe default when content refresh is unavailable.

### 3. Relaunch Current Build

Best desktop path.

The agent should:

- rebuild if needed
- launch the player again
- restore context
- capture proof

### 4. Redeploy

Best mobile fallback.

The agent should:

- rebuild
- reinstall or republish dev build
- notify SDK/user

### 5. Custom Patch Path

Only for projects intentionally built around it.

Examples:

- Mono dev patch path
- embedded scripting runtime
- special project-specific hot patch system

This should be opt-in only.

## Vibing Architecture

Unity vibing should be modeled as a long-running project-aware workflow.

### Minimum contract

When a Unity vibing task runs, the agent should aim to:

1. understand the repo
2. inspect the latest feedback/crash context
3. change code/assets/config
4. add or update tests where appropriate
5. run relevant checks
6. rebuild/relaunch/redeploy if needed
7. summarize outcome back to phone
8. continue until objective is complete or blocked

### Why this matters

For Unity, "vibe coding" without verification is weak.

So the prompt contract should emphasize:

- tests are important
- build verification matters
- smoke run evidence matters
- summary must be explicit

This is especially important for unattended sessions on remote owned machines.

## Phone Experience

The phone should not try to become the Unity Editor.

It should become:

- command center
- status viewer
- summary viewer
- optional remote viewport

Ideal phone cards:

- active task
- latest summary
- changed files
- test results
- build result
- relaunch status
- last screenshot
- watch live preview
- keep going / stop / handoff

## Team and Runner Architecture

This should remain compatible with solo users.

### Solo default

- no team required
- no cloud required
- no dedicated runner required
- local machine is enough

### Studio extension

- optional team name/project binding
- optional dedicated runner
- optional private model on rented VPS/GPU
- optional controlled AI cost structure

This is a real product advantage. Teams can reduce external LLM spend by running stronger local/self-hosted models on their own hardware, while still using Yaver's SDK and control loop.

## CI and Verification

The no-local-Unity path matters.

Current stance should be:

- develop the package locally as code
- use GitHub Actions + GameCI for Unity package tests
- later add owned-runner smoke tests for real Unity projects

Already wired:

- dedicated Unity workflow
- CI integration
- package tests

Still needed:

- richer package tests
- sample project import/build validation
- owned-runner smoke builds

## Market Fit Summary

Yaver's Unity angle is strongest where current tools stop at bug capture and web dashboards.

Validated by the market:

- in-app bug reporting
- screenshots
- logs
- sessions/snapshots
- desktop and mobile Unity tooling

Where Yaver can be different:

- self-hosted first
- free local mode
- one SDK for solo + studio
- direct feedback -> AI fix loop
- remote session continuation on owned machines
- phone as control surface
- unified mobile/desktop philosophy

## Recommended Build Order

### Phase 1: Make current SDK real

1. verify sample Unity project import/build
2. add richer package tests
3. stabilize overlay and config model
4. document install and first-run clearly

### Phase 2: Make reload honest and useful

1. implement content adapters
2. implement desktop relaunch path
3. implement mobile redeploy path better
4. add project-level custom reload handler docs and examples

### Phase 3: Make vibing operational

1. structured task summary schema
2. Unity-specific build/test helpers in agent
3. relaunch/redeploy result reporting
4. summary cards for phone

### Phase 4: Make feedback richer

1. short replay/video capture
2. desktop crash artifact enrichment
3. searchable logs and session attachments

### Phase 5: Make remote supervision richer

1. screenshot streaming
2. optional live viewport
3. remote-runner selection and handoff UX

## Final Assessment

Unity is a viable Yaver lane.

Not because Yaver can become Hermes for Unity.

But because Yaver can give Unity developers something they actually need:

- feedback inside the game
- fast iteration across mobile and desktop
- self-hosted control
- remote continuation
- AI-assisted fix loops
- verification and summary

The current repo already contains meaningful groundwork for this.

What is missing is not the core thesis. What is missing is the next round of implementation that turns the scaffolding into a verified, opinionated Unity workflow.
