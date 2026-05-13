# Feedback SDK Audit

Last updated: 2026-05-13

This is the canonical handoff note for Feedback SDK work spanning both:

- `yaver.io`
- sibling app repo `../sfmg`

This document is for Claude Code or any other agent that needs to inspect both repos, compare the standalone SDK path against the Yaver host path, and continue fixing bugs if it finds them.

Code is the source of truth. Re-check the files named here before acting.

## Goal

Understand and verify two distinct flows:

1. `sfmg` running **inside Yaver** as a hosted Hermes guest
2. `sfmg` running **standalone** with embedded `yaver-feedback-react-native`

The hosted-in-Yaver path is reported to be working.

The standalone SDK path was audited here, one targeted fix was applied in `sfmg`, and runtime verification still needs to happen.

## Executive summary

### What appears healthy

- Yaver's current agent-side reload path is materially better than older docs suggest.
- The hosted `sfmg inside Yaver` path is intentionally separate from the standalone SDK path.
- `sfmg` already suppresses its own feedback widget when running inside Yaver host.
- `sfmg` has the Expo plugin installed for standalone native reload support.
- The installed `sfmg` SDK package already includes several important patched behaviors.

### What was actually fixed

In `../sfmg`:

- cached `agentUrl` is no longer trusted without a live auth token
- `BlackBox.start()` is no longer allowed to start with URL-only / no auth
- sign-out now clears cached `agentUrl` as well as token

In `yaver.io` SDK source:

- standalone machine picker no longer hard-blocks an online machine just because a direct LAN `/health` probe failed
- online + direct-probe-failed now renders as a warning-state hint instead of a false fatal offline state

### What is still unresolved

- no end-to-end runtime verification was run after the fix
- `sfmg` still lives on published `yaver-feedback-react-native@^0.8.6`
- `yaver.io` repo SDK source is already at `0.8.13`
- `sfmg` still carries `patch-package` drift via `yaver-feedback-react-native+0.8.4.patch`

## Repo map

### Yaver repo

Root: `/Users/kivanccakmak/Workspace/yaver.io`

Important files:

- SDK source: `sdk/feedback/react-native/*`
- agent reload handling: `desktop/agent/devserver_http.go`
- feedback-to-vibing auto-reload glue: `desktop/agent/feedback_to_vibe.go`
- mobile host reload UI: `mobile/app/(tabs)/hotreload.tsx`
- mobile host feedback overlay: `mobile/src/components/FeedbackOverlay.tsx`

### SFMG repo

Root: `/Users/kivanccakmak/Workspace/sfmg`

Important files:

- standalone SDK bootstrap: `src/components/YaverFeedbackWidget.tsx`
- standalone settings/auth glue: `src/app/settings.tsx`
- host suppression: `src/app/_layout.tsx`
- package pinning: `package.json`
- Expo plugin config: `app.json`
- patch-package patch: `patches/yaver-feedback-react-native+0.8.4.patch`

## Two products, not one

Treat these as separate products with shared pieces.

### A. Hosted guest inside Yaver

This means:

- Yaver mobile app is running
- `sfmg` is loaded as a Hermes guest inside the Yaver container
- Yaver owns the overlay, reload controls, bridge swap, and host-native modules

Important evidence:

- `sfmg` does not mount its standalone widget inside Yaver host:
  - `../sfmg/src/app/_layout.tsx:785`

Implication:

- success in this mode does **not** prove standalone SDK mode is healthy

### B. Standalone SFMG with embedded Feedback SDK

This means:

- `sfmg` is its own app process
- `yaver-feedback-react-native` is mounted inside that app
- standalone auth, discovery, reload, and BlackBox SSE all matter

Implication:

- this mode has more moving parts than the hosted guest path

## Current Yaver-side state

### SDK source in `yaver.io`

Current repo SDK version:

- `sdk/feedback/react-native/package.json:3` -> `0.8.13`

Important current-source behaviors:

- BlackBox command handling for `reload`, `reload_bundle`, and `status`
- safer startup behavior around report launch
- native bundle loading via:
  - `YaverBundleLoader` in host mode
  - `YaverHotReload` in standalone mode

Relevant source files:

- `sdk/feedback/react-native/src/YaverFeedback.ts`
- `sdk/feedback/react-native/src/P2PClient.ts`
- `sdk/feedback/react-native/src/FeedbackModal.tsx`
- `sdk/feedback/react-native/src/BlackBox.ts`
- `sdk/feedback/react-native/app.plugin.js`

### Agent reload path in `yaver.io`

Current authoritative handler:

- `desktop/agent/devserver_http.go:1947`

What it does in bundle mode:

1. accepts `POST /dev/reload-app`
2. resolves project hints
3. rebuilds Hermes bundle through the agent build path
4. returns the real build response to caller
5. skips `reload_bundle` broadcast if build failed
6. emits status/progress for the device UI
7. broadcasts or targets `reload_bundle` only after successful build

This is important because older archived notes described weaker behavior.

### Feedback-to-vibing auto-reload in `yaver.io`

Agent glue:

- `desktop/agent/feedback_to_vibe.go`

What it appears to do:

- reshapes feedback-origin tasks into vibing-style tasks
- can auto-trigger bundle rebuild + reload after successful vibing tasks when prompt intent asks for reload

This is relevant for comparing "manual reload works" versus "fix flow auto-reload works".

## Current SFMG-side state

### Package and plugin state

In `../sfmg`:

- dependency pin:
  - `package.json:42` -> `yaver-feedback-react-native@^0.8.6`
- `patch-package` postinstall:
  - `package.json:73`
- Expo plugin enabled:
  - `app.json:52`

### Installed package reality

The currently installed `node_modules` in `sfmg` appears to already include important patched behavior such as:

- `reportLaunchInFlight`
- `yaverFeedback:reportLaunch`
- quick-action icon launch guarding

That means:

- the local app is not a pure stock `0.8.6`
- but the repo still expresses itself as `0.8.6` + patch-package

### Patch drift risk

Patch file:

- `../sfmg/patches/yaver-feedback-react-native+0.8.4.patch`

Concerns:

- filename says `0.8.4`
- installed package is `0.8.6`
- repo SDK source is `0.8.13`

This may still work today, but it is drift and should be treated as suspicious until verified cleanly.

## What was fixed in `sfmg`

Files changed:

- `../sfmg/src/components/YaverFeedbackWidget.tsx`
- `../sfmg/src/app/settings.tsx`

### 1. Cached agent URL now requires auth

Before:

- `sfmg` would reuse `sfmg.yaverAgentUrl` even when `sfmg.yaverToken` was absent

Risk:

- stale target reuse
- reconnecting toward the wrong machine
- reopening old unauthenticated churn behavior

After:

- cached URL is only used when token exists
- otherwise fallback is discovery

### 2. BlackBox SSE startup now requires both URL and auth

Before:

- `BlackBox.start()` was gated on `agentUrl` only

Risk:

- command stream startup with stale or missing auth

After:

- startup requires both `agentUrl` and `authToken`

### 3. Sign-out now clears both token and cached target

Before:

- sign-out removed token only

After:

- sign-out removes token and cached `agentUrl`

## What we have

Across both repos, we have:

- Yaver host path that is reportedly working for `sfmg`
- standalone SDK bootstrap inside `sfmg`
- native plugin wiring for standalone reload
- agent-side bundle rebuild + guarded `reload_bundle` broadcast
- feedback-to-vibing integration on the Yaver side
- a targeted SFMG fix for stale-target / missing-auth startup behavior
- a targeted Yaver SDK picker fix for false-negative direct reachability

## What was fixed in `yaver.io`

File changed:

- `sdk/feedback/react-native/src/MachinePickerScreen.tsx`

### Direct probe was too strict for standalone SDK selection

Before:

- the standalone picker did a direct unauthenticated `http://host:port/health` probe
- if that probe failed, tapping the machine was blocked even when the backend still considered the machine online
- the UI also rendered that state as a hard red "agent not responding" failure

Why this was wrong:

- the rest of Yaver can still reach a healthy machine through the selected-device discovery path and relay fallback
- a direct LAN probe failing does not mean the machine is unusable
- this is especially visible when the same machine can already serve `sfmg` from inside Yaver, but the standalone SDK picker still refuses to select it

After:

- direct-probe failure only hard-blocks selection when the machine is also offline in backend state
- online + direct-probe-failed is now treated as a warning state
- selection can continue so the SDK can use the proper discovery/reconnect path

## What we do not have yet

- a fresh runtime verification of standalone `sfmg` after the fix
- confidence that `sfmg` should remain on `0.8.6` + patch-package
- a clean statement of whether the patch file is still necessary, fully necessary, or stale
- a documented migration path from patched published SDK to current source

## Main concerns for Claude Code

1. Do not collapse hosted-guest mode and standalone SDK mode into one mental model.
2. Re-check both repos before editing:
   - `yaver.io` SDK/agent/mobile host code
   - `sfmg` wrapper/config/patch state
3. Verify the standalone runtime first, before broad refactors.
4. Audit whether `sfmg` should:
   - stay on published `0.8.6`
   - regenerate its patch cleanly
   - or move closer to the in-repo SDK source at `0.8.13`
5. Compare standalone SDK status surfaces against Yaver mobile's own native panes, especially:
   - machine reachability semantics
   - runner auth visibility
   - "machine online but coding agent not ready" messaging
6. If runtime bugs still exist, determine whether the bug belongs in:
   - Yaver agent
   - Yaver SDK source
   - Yaver mobile host app
   - or SFMG wrapper/config

## Mobile app reference vs standalone SDK

Use Yaver mobile's own native feedback and agents panes as the reference implementation for operator UX.

### What Yaver mobile already does

Yaver mobile's native feedback pane:

- runs a runner-auth preflight against `/runner-auth/status`
- checks whether any coding runner is actually authenticated
- rewrites the subtitle into a CTA when no coding agent is signed in
- can route the user directly into the native Agents pane

Relevant code:

- `mobile/ios/Yaver/YaverFeedbackPane.swift`
  - `runRunnerAuthPreflight()`
  - `markSubtitleNoAgent(...)`
  - `refreshAgentChipLabel()`

Yaver mobile also has a dedicated native agents pane:

- `mobile/ios/Yaver/YaverAgentsPane.swift`

That pane already exposes per-runner state for:

- Claude Code
- Codex
- OpenCode

including install/auth state and remote sign-in flows.

### What the standalone SDK currently does

Standalone SDK currently has:

- a machine picker with direct reachability hints
- a selected-machine card in `FeedbackModal`
- remote sign-in buttons for Codex and Claude
- a browser-auth modal flow
- machine re-selection without leaving the host app
- standalone Yaver account sign-in inside the host app

Relevant code:

- `sdk/feedback/react-native/src/AuthOverlay.tsx`
- `sdk/feedback/react-native/src/LoginScreen.tsx`
- `sdk/feedback/react-native/src/MachinePickerScreen.tsx`
- `sdk/feedback/react-native/src/FeedbackModal.tsx`
- `sdk/feedback/react-native/src/YaverFeedback.ts`

### Important conclusion: standalone SDK already supports remote agent auth

This matters for the `sfmg` direct-use case:

- the user does **not** need to be inside the Yaver mobile container app
- the user does **not** need to bounce to the Yaver mobile app to authenticate Codex or Claude on the selected remote machine

What already exists in the standalone SDK:

1. Yaver account auth inside the host app
   - login modal is mounted by `AuthOverlay`
   - supports Apple / Google / GitHub / GitLab / Microsoft / Email

2. Remote machine selection inside the host app
   - machine picker is mounted by `AuthOverlay`
   - selected device is persisted and used for later discovery

3. Remote runner auth inside the host app
   - `FeedbackModal` exposes `Remote sign-in` buttons for:
     - Codex
     - Claude
   - these call:
     - `YaverFeedback.startRunnerBrowserAuth(...)`
     - `YaverFeedback.getRunnerBrowserAuthStatus(...)`
     - `YaverFeedback.submitRunnerBrowserAuthCode(...)`
   - which proxy to the agent over the selected machine path

So the direct `sfmg + yaver-feedback-react-native` path already covers:

- sign in to Yaver
- select remote machine
- authenticate remote Codex or Claude
- run feedback / vibing / reload against that selected machine

That is a real standalone workflow, independent of Yaver mobile host mode.

Relevant code:

- `sdk/feedback/react-native/src/MachinePickerScreen.tsx`
- `sdk/feedback/react-native/src/FeedbackModal.tsx`

### The gap

The standalone SDK does not yet match Yaver mobile's ambient status visibility.

It is weaker at:

- showing current runner auth state before the user hits Send
- distinguishing "machine reachable" from "coding agent ready"
- presenting machine status and runner status as one coherent operational surface

In practice:

- Yaver mobile already tells the user whether the coding agent layer is configured
- standalone SDK mostly tells the user whether a machine exists and offers sign-in actions
- standalone SDK can perform remote auth, but does not summarize auth/install/default state as clearly before action time

### What standalone SDK has vs does not have for agent selection/auth

#### It has

- Yaver user login inside SDK
- machine picker inside SDK
- selected-machine card in `FeedbackModal`
- Codex remote browser-auth flow
- Claude remote browser-auth flow
- runner-down messaging on the selected-machine card
- relay-capable runner-auth transport through the selected machine

#### It does not yet have

- a persistent per-runner status summary equivalent to Yaver mobile's agents pane
- an explicit `/runner-auth/status` summary strip in the standalone feedback modal
- "installed / not installed / signed in / not signed in" rows shown by default
- default runner selection UI equivalent to mobile's `CodingAgentsSection`
- default model selection UI equivalent to mobile's per-device model picker
- an OpenCode auth/config flow comparable to Yaver mobile's native agents pane
- an obvious "which runner will my feedback use right now?" summary that matches mobile parity

### Comparison against Yaver mobile RN and native surfaces

Yaver mobile has two reference levels:

1. Native host panes
   - `YaverFeedbackPane.swift`
   - `YaverAgentsPane.swift`

2. RN device/settings surfaces
   - `mobile/src/components/DeviceDetailsModal.tsx`
   - `mobile/app/(tabs)/settings.tsx`

Compared with those, standalone SDK is missing:

- per-runner install/auth rows
- saved default runner display and editing
- saved default model display and editing
- a first-class OpenCode setup surface
- stronger preflight messaging before the user taps Vibing / Screenshot & Fix / Hot Reload

### Suggested parity target

For the direct `sfmg` use case, the ideal standalone SDK experience should let a user:

1. sign in to Yaver from inside `sfmg`
2. pick their remote machine from inside `sfmg`
3. see whether Codex / Claude / OpenCode are:
   - installed
   - authenticated
   - selected as default
4. authenticate the runner from inside `sfmg` if needed
5. immediately use:
   - Hot Reload
   - Vibing
   - Screenshot & Fix

without needing the Yaver mobile app as a separate operational console

### Recommended comparison target for Claude Code

Claude Code should compare:

1. `mobile/ios/Yaver/YaverFeedbackPane.swift`
2. `mobile/ios/Yaver/YaverAgentsPane.swift`
3. `sdk/feedback/react-native/src/FeedbackModal.tsx`
4. `sdk/feedback/react-native/src/MachinePickerScreen.tsx`

Focus questions:

- Should standalone SDK add a lightweight `/runner-auth/status` preflight like Yaver mobile?
- Should standalone SDK show a compact "Codex ready / Claude needs auth" strip instead of only generic remote sign-in buttons?
- Should the selected-machine card surface runner-auth and runner-down state more explicitly?
- Should standalone SDK add default-runner/default-model controls similar to `CodingAgentsSection`?
- Should standalone SDK grow an OpenCode config/auth surface for full parity with Yaver mobile?

## Recommended work plan for Claude Code

### Step 1. Reconfirm code assumptions

Check:

- `yaver.io/sdk/feedback/react-native/*`
- `yaver.io/desktop/agent/devserver_http.go`
- `yaver.io/desktop/agent/feedback_to_vibe.go`
- `../sfmg/src/components/YaverFeedbackWidget.tsx`
- `../sfmg/src/app/settings.tsx`
- `../sfmg/package.json`
- `../sfmg/app.json`
- `../sfmg/patches/yaver-feedback-react-native+0.8.4.patch`

### Step 2. Verify standalone SFMG behavior

Run these on standalone `sfmg`, not hosted inside Yaver:

1. Clean session
   - clear `sfmg.yaverToken`
   - clear `sfmg.yaverAgentUrl`
   - relaunch

2. Sign-in flow
   - sign in from SFMG Settings
   - verify no repeated launch/open-loop behavior

3. Discovery flow
   - no cached URL
   - verify correct machine selection
   - switch machines and verify it does not keep the old target
   - verify an online machine with failed direct probe is still selectable

4. Manual reload flow
   - trigger SDK Hot Reload
   - verify agent rebuild starts
   - verify status updates surface
   - verify `reload_bundle` lands
   - verify app actually reloads

5. Vibing/fix flow
   - send one fix request from the SDK
   - let task complete
   - verify whether auto-reload after fix works

6. Sign-out flow
   - sign out
   - verify token and cached URL are both gone

### Step 3. Only then decide on structural changes

If runtime is clean:

- consider leaving architecture alone and just reducing patch drift

If runtime is not clean:

- localize the bug to repo:
  - Yaver agent
  - Yaver SDK
  - Yaver host app
  - SFMG wrapper

### Step 4. Cleanup follow-up if time allows

Potential follow-up tasks:

- regenerate or rename the `sfmg` patch file against the actual SDK version
- compare installed `sfmg/node_modules/yaver-feedback-react-native` against `yaver.io/sdk/feedback/react-native`
- decide whether `sfmg` should vendor fewer wrapper behaviors and trust upstream SDK more

## Practical conclusion

The hosted `sfmg inside Yaver` path and the standalone `sfmg + Feedback SDK` path are close cousins, not the same system.

The fix applied here reduced one real class of standalone failure in `sfmg`, but it did not prove the standalone system end-to-end.

The next best action is runtime verification across both repos, with bugs fixed in the repo that actually owns the fault.
