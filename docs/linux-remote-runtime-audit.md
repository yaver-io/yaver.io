# Linux Remote Runtime Audit

Date: 2026-04-30

## Scope

This audit covers Yaver's `Remote Runtime` feature for native Swift/Kotlin projects when the developer's main remote box is Linux, with special focus on the iOS-native case.

The current product split is:

- React Native projects: `Hermes`
- Web projects: `WebView`
- Native Swift/Kotlin projects: `Remote Runtime`

`Remote Runtime` is not a Hermes replacement. It is a second execution lane that streams and controls a simulator or emulator running on a host machine.

## Executive Summary

For Android-native projects, a Linux remote box is a valid primary host.

For iOS-native Swift projects, a Linux remote box is not a valid primary simulator host because Apple does not provide the iOS Simulator on Linux. That is a hard platform constraint, not a missing Yaver feature.

So the correct architecture for iOS-native remote runtime is:

- Linux can host:
  - agent control plane
  - coding runners
  - repo checkout
  - signaling
  - relay-aware frame/control routing
  - logs and orchestration
- macOS must host:
  - iOS Simulator
  - `xcrun simctl`
  - Xcode toolchain
  - simulator screenshot / input automation
  - eventual build/install/launch for Swift-native apps

## What Linux Can Do

Linux remote boxes are good at:

- running the Go agent
- holding monorepo state
- running Codex / Claude / build helpers
- hosting Android Emulator
- hosting relay-compatible remote runtime frame endpoints
- acting as the session registry and control broker
- forwarding Yaver Feedback SDK commands

Linux remote boxes are not good at:

- running the iOS Simulator
- running `xcodebuild`
- using `xcrun simctl`
- launching Swift-native iOS apps in Apple's simulator runtime

## iOS Native on Linux

### Hard Constraint

The iOS Simulator requires macOS and Xcode. A Linux machine cannot be the actual runtime host for `ios-simulator`.

This means a Linux-only implementation for Swift-native remote runtime can never be complete if the requirement is:

- boot an iOS Simulator
- install a Swift app
- launch it
- stream its screen
- send taps/text/home-like control

### What Is Still Possible

A Linux-first architecture is still possible if Yaver separates:

1. control/orchestration host
2. runtime host

Then:

- Linux host owns:
  - project scanning
  - task execution
  - relay-aware session creation
  - permissions
  - session metadata
  - feedback routing
- Mac host owns:
  - simulator process
  - app install/launch
  - screenshot capture
  - input injection

In that architecture, the Linux box is still useful, but it is no longer the actual iOS runtime machine.

## Viable Architectures

### Option A: Split Host Model

- Linux agent is the main developer machine.
- A paired Mac worker is registered as an iOS runtime node.
- Yaver schedules `ios-simulator` sessions onto the Mac worker.
- Linux stays the main coding/control machine.

Pros:

- preserves Linux as the main remote box
- realistic for Swift-native development
- clean separation of concerns

Cons:

- requires multi-host session scheduling
- repo sync between Linux and Mac must be solved
- build artifacts or source tree must be mirrored

### Option B: Mac Runtime Host Only for iOS

- Android-native remote runtime uses Linux directly.
- Swift-native remote runtime bypasses Linux for execution and uses a Mac-hosted agent/runtime lane.

Pros:

- simplest operationally
- least ambiguity

Cons:

- iOS and Android behave differently operationally
- developer may need to understand two host classes

### Option C: Linux Agent Plus Remote Build Artifact Push to Mac

- Linux does code work and prepares source/build instructions.
- Mac receives source or artifacts.
- Mac builds and runs the app.
- Mac streams frames back through Yaver relay-compatible transport.

Pros:

- keeps heavy coding on Linux
- keeps simulator on Mac where it belongs

Cons:

- artifact synchronization complexity
- signing/build cache duplication
- more fragile when source and toolchains drift

## Recommended Product Position

Yaver should state this explicitly:

- `Android Emulator`: Linux or macOS host supported
- `iOS Simulator`: macOS host required

Do not market Linux remote boxes as supporting Swift-native simulator runtime directly. That will create false expectations and support noise.

## Relay Impact

Yaver relay helps with:

- signaling
- auth
- session discovery
- feedback commands
- control POSTs
- frame polling fallback over authenticated HTTP

Yaver relay does not remove the need for a macOS host for iOS Simulator.

Relay solves network reachability. It does not solve platform availability.

## Current Yaver Implementation Direction

The current remote runtime slice in the repo supports two transport modes:

- `direct-webrtc`
  - WebRTC data-channel JPEG frames
  - best for direct / Tailscale / reachable hosts
- `relay-jpeg-poll`
  - authenticated HTTP frame polling
  - works through Yaver relay paths
  - intended as the cross-network fallback

That transport split is correct for Linux-hosted Android and for Mac-hosted iOS. It still does not allow Linux to become the iOS simulator machine.

## iOS Native Implementation Plan for a Linux-Primary Developer

### Phase 1

- Keep Linux as primary coding/control host.
- Add explicit runtime-host targeting in remote runtime sessions.
- Introduce `runtimeHostClass` such as:
  - `linux-android`
  - `macos-ios`

### Phase 2

- Register a Mac runtime node in Yaver.
- Teach the Linux-side control plane to schedule Swift-native sessions onto the Mac runtime node.
- Mirror project source or send build context to the Mac node.

### Phase 3

- Move iOS Simulator attach, screenshot, and tap/text control fully onto the Mac runtime node.
- Keep relay-compatible frame transport available for internet use.

### Phase 4

- Add launch/build integration for Swift projects:
  - scheme detection
  - destination resolution
  - simulator boot selection
  - install / launch / relaunch hooks

## What Should Not Be Done

- Do not attempt to emulate iOS Simulator on Linux.
- Do not pretend a Linux box alone can run Swift-native remote runtime end to end.
- Do not couple iOS-native remote runtime success to the presence of Yaver relay.

## Recommendation

The recommended production architecture is:

- Linux remote box:
  - primary coding/control/orchestration node
  - Android runtime host
- Mac runtime node:
  - iOS simulator/build/runtime host
- Yaver relay:
  - signaling
  - auth
  - control plane
  - relay-frame fallback transport
  - later optionally TURN-like media relay

That is the correct way to support Swift-native development when the developer's main remote box is Linux.
