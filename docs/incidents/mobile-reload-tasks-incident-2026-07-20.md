# Mobile Tasks / Reload Incident Dump — 2026-07-20

This is a working dump from the 2026-07-20 iPhone screenshots/video around
Tasks, Follow Up, Hot Reload, Hermes build, and connection loss. It intentionally
redacts user email, machine IDs, relay hostnames, Convex hostnames, and IPs
because the repository is public.

## Observed Failures

1. New-task composer layout overflow:
   - Screenshot time: around 22:49.
   - The `Send` button overflows outside the black composer well while the iOS
     keyboard is open.
   - The footer has four circular actions plus a large `Send` pill. On narrow
     iPhone width, `composerFooterRight` has fixed-size children plus `gap: 10`
     and `sendButtonLarge.minWidth = 120`, so the row can exceed the composer
     width.

2. Follow-up composer is second-class compared with New task:
   - Screenshot time: around 22:48.
   - Follow-up still uses the older `modalButtons` layout: large `Cancel`,
     `+`, mic, and `Send` squeezed into one row.
   - User intent: second composer should behave like first composer. If first
     task was text/STT, follow-up should preserve the same priority/path and
     have the same STT affordance/layout instead of a smaller, drifted UI.

3. STT inconsistency:
   - New task has the richer composer footer and mic path.
   - Follow-up calls `startRecording("followup")`, but UI and affordance are not
     equivalent to new task. This makes "first is STT and second should be STT"
     feel broken even though both paths eventually call the same helper.
   - Shared code: `mobile/app/(tabs)/tasks.tsx` has one `startRecording(target)`
     function with `recordingTargetRef` and writes into either `newTaskText` or
     `followUpText`.

4. Hermes compatibility false block:
   - Screenshot times: around 22:54-22:58.
   - Dialog says `Compatibility Blocked` and `Missing in Yaver: expo-gl`.
   - Agent card says the bundle compiled, but Yaver blocked restart because the
     project declares native modules missing from the mobile host.
   - Talos mobile does declare `expo-gl`, `expo-three`, and `three`.
   - Talos call sites found locally:
     - `/Users/.../Workspace/talos/mobile/src/screens/more/Cell3D.tsx`
     - `/Users/.../Workspace/talos/mobile/src/screens/more/spatial/SpatialBackdrop.tsx`
   - Both call sites guard `require("expo-gl")`, `require("expo-three")`, and
     `require("three")` in a try/catch and render fallback UI if unavailable.
   - Root cause: the old gate treated "module declared but not registered in
     Yaver host" as "would crash", even when the app already guarded the import.
     That is an inventory check standing in for the real operation.

5. Hot Reload / remote side update expectation:
   - User expectation: Hermes reload should make the remote side pull from main
     or otherwise update before building.
   - Current code has a pre-build pull path:
     - `desktop/agent/devserver_http.go` calls
       `s.maybePullBeforeHotReloadBuild(workDir)` in `handleBuildNativeBundle`
       for non-guest callers.
     - `desktop/agent/devserver_pull.go` delegates ambiguous cases to a coding
       agent.
     - `desktop/agent/devserver_pull_fast.go` has a synchronous git rules table:
       clean + behind runs `git pull --ff-only`; dirty + active agent can
       autostash/rebase; dirty + no agent skips.
   - Local checkout note: this repo's remote is named `github`, not `origin`.
     A naive `git fetch origin main` fails with "origin does not appear to be a
     git repository"; `git fetch github main` succeeds.

6. Connection loss / reconnect loop:
   - Screenshot/log time: around 23:00.
   - Tasks banner shows `Connecting`, `reconnect 1/5`, `Stop`, `View Logs`.
   - Connection logs show:
     - direct LAN candidates timing out or being negative-cached as unroutable;
     - relay attempts sometimes timing out;
     - relay sometimes refusing because relay password/session is missing or
       the device is not connected to relay;
     - later relay attempts can succeed.
   - Root-cause class: there are multiple connection truths in play: Convex
     device list/heartbeat, direct LAN reachability, Tailscale reachability,
     relay tunnel presence, and relay credentials. The UI can show a selected
     primary machine while the transport has no current path.
   - Existing relevant code:
     - `mobile/src/context/DeviceContext.tsx` refreshes devices every 30s and
       now preserves device array identity when unchanged to avoid restarting
       connection effects.
     - `mobile/src/lib/quic.ts` schedules reconnect with capped backoff and has
       one relay credential repair hook.
     - `mobile/src/lib/directProbeFailure.ts` formats unroutable/timeout probe
       failures.
     - `mobile/src/lib/unroutableCache.ts` negative-caches impossible direct
       candidates until network changes.

## Current Code State

Working branch: `tasks-active-bulk-and-reload-align`.

Worktree was clean at first inspection, then `desktop/agent/devserver_http.go`
became locally modified by an existing/concurrent change. I did not create or
revert that change.

The local diff in `desktop/agent/devserver_http.go` already changes the native
module gate:

- Missing modules are logged as "warning, not fatal".
- Fatal blocking is limited to version mismatches and framework/runtime drift.
- The code comment names the exact Talos `expo-gl` incident.

This means the `expo-gl` dialog shown in the screenshots is from an older app /
agent slice or from a device not running this local change yet. Shipping the
current agent/mobile pair should stop that specific false block, assuming there
is no separate load-time validator on the phone re-blocking missing modules.

## Concrete Root Causes

### A. Composer footer has unbounded fixed width

File: `mobile/app/(tabs)/tasks.tsx`.

Relevant styles:

- `composerFooterRight`: row + `gap: 10`.
- `composerActionButton`: fixed `48 x 48`.
- `sendButtonLarge`: `minWidth: 120`, `paddingHorizontal: 24`.

Worst case on iPhone:

- 3 icon buttons = 144 px
- gaps between right-row children = 30 px
- send min width = 120 px
- left add button = 48 px
- footer horizontal padding = 16 px
- parent shell padding = 16 px

That already consumes about 374 px before safe margins. On a 393 px-wide
viewport with modal padding, it cannot fit reliably.

Fix direction:

- Make the new-task footer responsive:
  - keep the left add button fixed;
  - let the right side shrink with `minWidth: 0`;
  - cap `sendButtonLarge` using `flexShrink: 1` and lower min width on narrow
    screens;
  - or wrap the `Send` button onto a second row when width is narrow.
- Add a small pure layout test or Maestro screenshot flow for iPhone width with
  keyboard visible.

### B. Follow-up uses a different composer contract

File: `mobile/app/(tabs)/tasks.tsx`.

The new-task composer has `composerShell`, `composerFooter`, `composerActionButton`,
`sendButtonLarge`, and extra quick actions. The expanded follow-up composer uses
`modalButtons`, `cancelButton`, ad hoc 44 px buttons, and `submitButton`.

Fix direction:

- Extract a shared `TaskComposerSurface` component used by both New task and
  Follow Up.
- Inputs:
  - `kind: "task" | "followup"`
  - `text`, `setText`
  - `images`, `setImages`
  - `isSubmitting`, `isTranscribing`, `isRecording`
  - `onPickImage`, `onStartStopRecording`, `onSend`
  - optional target/runner picker slot
- New task and follow-up should share:
  - STT button behavior;
  - disabled state;
  - overflow behavior;
  - attachment strip;
  - `Send` button sizing;
  - haptics.

### C. STT target state is global, not composer-scoped

File: `mobile/app/(tabs)/tasks.tsx`.

`recordingTargetRef` is a global mutable ref. This is workable but fragile when
modals are dismissed, follow-up collapses, or user switches between task and
follow-up while recording/transcribing.

Fix direction:

- Keep one shared recorder implementation, but expose it through the shared
  composer component.
- When closing a composer while recording, stop/cancel intentionally and clear
  `recordingTargetRef`.
- Ensure follow-up starts recording only after the expanded composer is mounted,
  mirroring `openCreateTaskDictating`.

### D. Missing-native-module gate confused absence with crash

Files:

- `desktop/agent/native_modules_compat.go`
- `desktop/agent/devserver_http.go`
- `mobile/src/lib/nativeBuild.ts`

The old behavior:

- `BuildNativeModuleCompatReportWith` marks `expo-gl` as incompatible because
  it is in Talos `package.json` but absent from Yaver `sdk-manifest.json`.
- `handleBuildNativeBundle` returned `409 NATIVE_MODULE_INCOMPATIBLE`.
- Mobile `nativeBuildFailureTitle()` maps that to `Compatibility Blocked`.

Correct behavior:

- Missing host modules should be warnings unless Yaver can prove the module will
  be imported/called unguarded.
- Version mismatches of modules that the host does register should stay fatal.
- Runtime family mismatches should stay fatal.

Current local change already implements this backend direction in
`desktop/agent/devserver_http.go`.

Follow-up hardening:

- Add a Go regression test with a temp Expo app declaring `expo-gl` and assert
  `/dev/build-native` no longer returns `NATIVE_MODULE_INCOMPATIBLE` solely for
  missing modules.
- Add a doctor/report test that says "missing module warning" not "would crash".
- Mobile copy should not say "would crash" for missing-only reports.

### E. Pre-build pull exists but is easy to misunderstand

Files:

- `desktop/agent/devserver_pull.go`
- `desktop/agent/devserver_pull_fast.go`
- `desktop/agent/devserver_http.go`

The pull path is wired into `/dev/build-native`, but it is conservative:

- no git worktree: skip;
- no upstream: skip;
- clean and behind: `git pull --ff-only`;
- dirty and active coding agent: rebase/autostash;
- dirty and no active coding agent: skip;
- ambiguous state: delegate to coding agent.

What the UI should surface:

- show a build log line for the pull decision every time;
- show "pulled", "already up to date", or "skipped because dirty/no upstream";
- never let the user believe Hermes reload ignored main when it intentionally
  skipped for safety.

### F. Connection recovery has several false-green risks

Files:

- `mobile/src/context/DeviceContext.tsx`
- `mobile/src/lib/quic.ts`
- `mobile/src/lib/connectionCache.ts`
- `mobile/src/lib/unroutableCache.ts`
- `mobile/src/components/RemoteBoxBanner.tsx`

The pasted log shows the app can discover devices successfully but still have
no working transport. That is valid, but the product needs to state it plainly.

Fix direction:

- Banner should distinguish:
  - device listed/heartbeat fresh;
  - direct unreachable;
  - relay tunnel absent;
  - relay auth stale;
  - relay timed out;
  - recovered via relay.
- Add one self-heal step for relay auth shaped failures already exists in
  `quic.ts`; verify it fires for both "relay password missing" and "device not
  connected to relay" cases only when appropriate.
- Avoid retry storms:
  - preserve device array identity (already present);
  - ensure reconnect timers are per focused device and not restarted by the 30s
    device refresh tick;
  - expose "last successful path" and "current failure path" in the banner/logs.

## Proposed Work Order

1. Keep/finish the current backend change that makes missing native modules
   warning-only.
2. Add regression tests around the missing-only `expo-gl` case.
3. Extract shared task/follow-up composer UI and fix footer wrapping.
4. Make follow-up STT lifecycle match new-task dictation behavior.
5. Tighten mobile failure copy:
   - missing module warning: "This feature may be unavailable if the app calls
     it."
   - runtime/version mismatch: "Blocked".
6. Add connection diagnostic summarizer for the banner:
   - "LAN unreachable; relay tunnel absent"
   - "Relay auth stale; repairing"
   - "Relay timed out; retrying"
   - "Recovered via relay"
7. Run verification:
   - Go: targeted agent tests around native compatibility and pre-build pull.
   - Mobile TypeScript: pure tests for composer/layout helpers if extracted.
   - Maestro: `mobile/maestro/followup-visible.yaml`.
   - Manual: iPhone narrow viewport with keyboard open for New task and Follow Up.

## Verification Commands

Run from repo root:

```bash
cd desktop/agent
go test -count=1 -run 'TestBuildNativeModuleCompatReport|TestBuildNative|TestOpsReload|TestMobileHermes' .
```

```bash
cd mobile
npx tsc --noEmit
```

```bash
maestro test mobile/maestro/followup-visible.yaml
```

## Notes From This Investigation

- `git fetch origin main` is wrong for this checkout; remote is `github`.
- `git fetch github main` succeeded.
- `desktop/agent/devserver_http.go` has a local modification that appears to
  address the Hermes `expo-gl` false block. Do not overwrite it.
- No mobile UI fix has been applied in this dump yet.
