# Yaver Unity Case for Mobile Hypercasual

## Executive Summary

This document scopes a realistic Unity expansion for Yaver as of April 23, 2026.

The conclusion is:

- Unity is a valid Yaver expansion for mobile game developers, especially hypercasual and hybrid-casual teams.
- The strongest wedge is not "Hermes-like universal hot reload."
- The strongest wedge is a self-hosted, free-by-default device feedback and build/install loop:
  - in-game bug capture
  - short gameplay replay
  - logs and exceptions
  - scene / level / build metadata
  - remote build, install, and verify from the phone
- Managed cloud remains an optional paid layer, not a prerequisite.

That stays aligned with Yaver's current philosophy:

`phone / control surface -> your own machine -> optional managed cloud`

## Why Unity Fits Yaver

Unity mobile teams already accept the core Yaver operating model:

- a local or owned machine does heavy work
- builds happen on developer-controlled hardware
- iOS often requires a Mac build host
- Android builds are larger and slower than JS bundle pushes
- device-only bugs are common and costly to repro

That means Yaver does not need to retrain the market into a new operational model. It only needs to compress the loop.

For a solo hypercasual developer or a small studio, the pain usually looks like this:

1. Ship a prototype to a phone.
2. A tester or publisher contact finds a bug.
3. The report arrives as chat text, a loose screenshot, or a vague screen recording.
4. Logs are elsewhere.
5. Device details are missing.
6. The developer rebuilds locally, tries to reproduce, then sends another APK/IPA.
7. The loop repeats.

Yaver can reduce that to:

1. Tester triggers the in-game Yaver SDK.
2. SDK captures screenshot, short replay, logs, errors, device/build metadata.
3. Report goes directly to the Yaver agent running on the developer's own machine.
4. Developer or agent rebuilds, installs, and verifies.
5. Phone stays the control surface.

## Why Hypercasual Specifically Is a Good Entry Point

Hypercasual and adjacent mobile genres reward iteration speed more than broad feature depth.

Typical needs:

- very fast prototype validation
- frequent device testing
- publisher and QA feedback loops
- remote tuning of difficulty, pacing, ad timing, and progression values
- high sensitivity to UX friction in the first 30-120 seconds of play

Those teams care deeply about:

- finding bugs on real phones
- getting exact repro context
- testing changes quickly
- reducing manual coordination overhead

They care less about a universal runtime injection story than React Native developers do.

Unity itself frames hypercasual around rapid testing and iteration:

- Unity: "How to Scale Hit Hyper-Casual Games"
  - https://unity.com/resources/growing-hyper-casual-games
- Unity Learn: "How to ship a hit hyper-casual game"
  - https://learn.unity.com/tutorial/how-to-ship-a-hit-hyper-casual-game

Market data still shows hypercasual matters as a traffic source and install engine:

- Liftoff 2025 Casual Gaming Apps Report
  - https://www.liftoff.ai/2025-casual-gaming-apps-report/

## Technical Reality: Feedback Yes, Hermes-Equivalent No

The main technical boundary is straightforward:

- Feedback SDK for Unity mobile: yes
- Self-hosted build/install loop for Unity mobile: yes
- Hermes-style runtime code injection for arbitrary Unity mobile builds: no

### Why React Native Can Do More

Yaver's current strongest reload loop is built around Hermes bundles and dev-server semantics. In the React Native SDK and agent, reload is modeled around:

- a dev reload path
- a bundle rebuild path
- a command channel back into the app

Relevant files:

- [sdk/feedback/react-native/src/P2PClient.ts](sdk/feedback/react-native/src/P2PClient.ts)
- [sdk/feedback/react-native/src/BlackBox.ts](sdk/feedback/react-native/src/BlackBox.ts)
- [sdk/feedback/react-native/src/types.ts](sdk/feedback/react-native/src/types.ts)

That works because the runtime contract is JS-first.

### Why Unity Is Different

Unity mobile usually ships through either:

- Mono: JIT runtime on supported platforms
- IL2CPP: ahead-of-time conversion from IL to C++ to native binary

Unity documentation:

- Mono overview
  - https://docs.unity3d.com/kr/6000.0/Manual/scripting-backends-mono.html
- IL2CPP overview
  - https://docs.unity3d.com/cn/2023.1/Manual/IL2CPP.html
- Managed stripping / linker behavior
  - https://docs.unity3d.com/ja/2018.3/Manual/ManagedCodeStripping.html

Implications:

- iOS builds are effectively IL2CPP/AOT in practice.
- Many production Android builds also use IL2CPP.
- There is no universal "swap logic bundle at runtime" surface comparable to Hermes.
- Reflection-heavy or dynamic patching strategies fight stripping, AOT, platform restrictions, and store expectations.

So Yaver must not market Unity support as:

- "Open in Yaver"
- "Hermes-like hot reload"
- "Universal runtime injection"

That would be technically dishonest.

## What Unity Support Should Mean in Yaver

Unity support should mean:

- self-hosted feedback SDK
- self-hosted black box event stream
- self-hosted build and install orchestration
- optional content/config flush where the game architecture supports it
- optional managed cloud runners later

### First-Class Unity Capabilities

1. In-game feedback capture
2. Short gameplay replay
3. Screenshot capture
4. Log and exception upload
5. Device model / OS / app version / build number capture
6. Scene and game-state metadata capture
7. Command channel from agent to runtime
8. Build/install/test actions from the phone

### Second-Class or Optional Unity Capabilities

1. Content-only reload
2. Remote config push
3. Addressables refresh
4. Optional integration with third-party Unity hot patch tools

### Explicit Non-Promise

Yaver should not promise runtime code injection for arbitrary Unity mobile projects.

## Competitive Landscape

There is already a proven Unity market for "capture context inside the game":

- Unity User Reporting / Diagnostics
  - https://docs.unity.com/en-us/cloud-diagnostics/user-reporting/about-user-reporting
  - https://docs.unity.com/en-us/cloud-diagnostics/migration
- Backtrace for Unity
  - https://docs.saucelabs.com/error-reporting/platform-integrations/unity/setup/
- Bugsee Unity SDK
  - https://docs.bugsee.com/sdk/unity/installation/
  - https://docs.bugsee.com/sdk/unity/manual/
- BetaHub Unity support
  - https://betahub.io/docs/features/
- Jahro Unity QA workflow
  - https://jahro.io/features/collaboration

What the market already validates:

- screenshots matter
- short replay/video matters
- Unity logs and device metadata matter
- in-game bug reporting matters

What is still relatively open:

- self-hosted by default
- phone as remote control surface
- one-step path from report to local AI-assisted fix loop
- local-first, free-by-default workflow with optional managed cloud later

## Product Positioning

### Good Positioning

"Yaver gives Unity mobile teams a self-hosted device feedback and build loop."

"Capture gameplay bugs on real phones, route them to your own machine, and rebuild or verify from anywhere."

"Free on your own machine. Managed cloud optional."

### Bad Positioning

"Unity hot reload like Hermes."

"Inject code into any Unity mobile build at runtime."

"Open Unity apps inside Yaver."

## Why Self-Hosted by Default Is an Advantage

Unity developers often prefer:

- no mandatory cloud build vendor
- no forced gameplay/video upload to a third-party SaaS
- no lock-in around asset-heavy workflows
- predictable costs

So the self-hosted default is not just philosophically aligned; it is a market wedge.

### Free Layer

- local agent on the developer machine
- local report intake
- local build/install loop
- local storage for replay and feedback bundles

### Managed Cloud Layer

Paid later for:

- remote build runners
- shared artifact hosting
- shared QA dashboards
- cloud relay/storage
- organization-wide AI triage
- cross-team access controls

This is a clean layering model because local-first still provides real value on its own.

## Technical Product Shape

## Unity SDK Scope

The Unity SDK should be packaged as a Unity Package Manager package under `sdk/feedback/unity`.

Core components:

- `YaverFeedback`
- `YaverBlackBox`
- `YaverDiscovery`
- `YaverP2PClient`
- command stream listener
- optional helper MonoBehaviour host

### Data to Capture

- screenshots
- errors / exceptions
- `Application.logMessageReceived`
- scene name
- runtime platform
- device model
- OS version
- app version
- optional custom state metadata from the game

### Upload Flow

- multipart POST to the local/owned Yaver agent
- metadata JSON + replay + screenshots + optional attachments

### Command Flow

- SSE command stream from the agent
- `reload`
- `reload_bundle`
- custom game-side commands later

For Unity, `reload` means:

- reload content or scene if the game implements it
- or trigger a rebuild/install flow at the agent layer

It does not mean universal code swapping.

## Recommended Go-To-Market Sequence

### Phase 1: Unity Feedback SDK

Ship:

- screenshot capture
- logs
- exceptions
- scene/build/device metadata
- black box buffer
- agent upload

Primary audience:

- solo mobile Unity devs
- hypercasual prototype teams
- publisher playtest loops

### Phase 2: Build/Install Companion

Ship:

- Android build/install flows
- iOS build/install orchestration where the host is a Mac
- "build current branch" from phone
- artifact retrieval and status

### Phase 3: Content/Config Loop

Ship:

- remote config push
- Addressables/content refresh hooks
- balance/level tuning workflows
- optional integrations for studios with custom patch systems

## Risks

1. Over-positioning around reload
2. Assuming all Unity teams want AI auto-fix loops on day one
3. Underestimating Android/iOS build pipeline diversity
4. Treating hypercasual as if it wants the same runtime ergonomics as React Native

## Recommendation

Proceed with Unity support, but keep the contract honest:

- feedback SDK: yes
- self-hosted build/install loop: yes
- hypercasual/mobile fit: yes
- managed cloud optional: yes
- universal Hermes-equivalent runtime injection: no

That is still a strong Yaver story, and it is one the product can actually deliver.
