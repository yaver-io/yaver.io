# Go Yaver Mobile Discovery

This note summarizes the current Go-agent mobile-project discovery path, what I changed in this session, what is verified working, what is still weak, and how the new test is implemented so another agent can audit or extend it.

## Goal

The intended behavior is:

- The mobile app's Hot Reload tab should show mobile-capable projects found by the Go agent.
- Detection should work for projects inside a larger repo like `~/Workspace/yaver.io`, not only standalone repos.
- Projects should carry mobile-framework information such as:
  - `expo`
  - `react-native`
  - `flutter`
  - `swift`
  - `kotlin`
- The mobile app should use that mobile-aware discovery surface, not the generic repo list.

## What I Changed

### 1. Mobile app Hot Reload tab now uses the mobile scanner

Before this change, the mobile Hot Reload tab in [mobile/app/(tabs)/apps.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/apps.tsx) was reading:

- `quicClient.listProjectsDetailed()`
- which hits the generic Go endpoint: `/projects`

That generic endpoint is based on repo discovery, not the dedicated mobile-framework scanner.

I changed the Hot Reload tab to read:

- `quicClient.listMobileProjectsDetailed()`
- which hits the Go endpoint: `/projects/mobile`

Related code:

- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)
- [mobile/app/(tabs)/apps.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/apps.tsx)

### 2. Mobile app refresh now refreshes the mobile scan

The Hot Reload tab's "Rediscover" button now calls:

- `quicClient.refreshMobileProjects()`

instead of:

- `quicClient.refreshProjects()`

This means the UI is refreshing the same scanner it is rendering.

### 3. Project open/tap resolution is now path-aware

The Hot Reload tab used to resolve project taps and `open_app` bus commands only by project name.

That is fragile because scanner labels can be repo-style names like:

- `sfmg / mobile`
- `yaver / mobile`
- `carrotbet / web`

I changed the Hot Reload screen so:

- tapping a row passes the full project object
- action lookup prefers `GET /projects/actions?path=...`
- string-based `open_app` still works via fuzzy match against:
  - displayed name
  - path leaf
  - full path

This is implemented in:

- [mobile/app/(tabs)/apps.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/apps.tsx)
- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)

## Go Agent Discovery Path

The relevant Go-side scanner is in:

- [desktop/agent/mobile_projects.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/mobile_projects.go)

Important functions:

- `scanMobileProjects()`
- `detectMonorepoLineage()`
- `hasProjectGitContext()`
- `parseAppName()`
- `handleMobileProjects()`

Important behavior:

- Walks discovery roots from `projectDiscoveryRoots()`
- Scans nested directories for framework markers
- Detects:
  - `pubspec.yaml` -> `flutter`
  - `package.json` with Expo -> `expo`
  - `package.json` with RN deps -> `react-native`
  - `Package.swift` -> `swift`
  - Android Gradle/manifest markers -> `kotlin`
  - Unity markers -> `unity`
- Requires some nearby project/git/workspace context via `hasProjectGitContext()`
- Builds richer mobile project metadata:
  - `Name`
  - `Path`
  - `Framework`
  - `WebCapable`
  - `MobileCapable`
  - `ExecutionMode`
  - `PrimarySurface`
  - `MonorepoRoot`
  - `MonorepoApp`

The HTTP endpoint used by the mobile app is:

- `GET /projects/mobile`

wired in:

- [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go)

## What Is Working

### Verified by Go tests

I added a new test in:

- [desktop/agent/mobile_projects_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/mobile_projects_test.go)

New test:

- `TestScanMobileProjects_DiscoversNestedFrameworksInsideYaverRepo`

What it simulates:

- `HOME=<tmp>`
- repo at `~/Workspace/yaver.io`
- nested mobile-capable apps inside that repo:
  - `mobile` -> Expo
  - `apps/todo-rn` -> React Native
  - `tests/fixtures/native-flutter-app` -> Flutter
  - `tests/fixtures/native-ios-swift` -> Swift
  - `tests/fixtures/native-android-kotlin` -> Kotlin

What it asserts:

- `scanMobileProjects()` finds each nested app
- each discovered project has the expected framework
- each is marked `MobileCapable=true`

### Verified test commands

Run from:

```bash
cd /Users/kivanccakmak/Workspace/yaver.io/desktop/agent
```

Commands I ran:

```bash
go test ./... -run 'TestScanMobileProjects_DiscoversNestedFrameworksInsideYaverRepo'
go test ./... -run 'Test(ProjectBundleIDMatches|DetectMonorepoLineage|DisplayProjectName|RepoRootForProject|HasProjectGitContext|ScanMobileProjects_)'
```

Both passed.

### Existing supporting tests that already help this area

Also relevant in [desktop/agent/mobile_projects_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/mobile_projects_test.go):

- `TestDetectMonorepoLineageRecognisesAppsLayout`
- `TestDetectMonorepoLineageRecognisesMobileLayout`
- `TestDetectMonorepoLineageRecognisesYaverWorkspace`
- `TestDetectMonorepoLineageYaverIoDogfoodLayout`
- `TestHasProjectGitContext_WalksDeepFixtureAncestors`
- `TestHasProjectGitContext_AcceptsWorkspaceChildWithoutGit`

Also relevant in [desktop/agent/classify_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/classify_test.go):

- `TestDetectFramework_ScopeAwareness`

## What Is Not Fully Verified Yet

### 1. No end-to-end mobile UI test for the Hot Reload list change

I changed the mobile app to use `/projects/mobile`, but I did not run a full mobile UI or headless test proving:

- the Hot Reload tab visually renders the new scanner output
- tags appear correctly for all frameworks
- tapping each row opens the right action sheet

The Go side is verified; the mobile UI path is not end-to-end verified yet.

### 2. `open_app` flow is improved but not fully integration-tested after this change

The matching logic now supports path-aware resolution and fuzzy fallback, but I did not add a specific UI/integration test for:

- `yaver insert sfmg`
- mobile receives `open_app`
- Hot Reload tab matches the intended row from `/projects/mobile`

That should be audited.

### 3. Generic `/projects` vs mobile `/projects/mobile` drift still exists

There are now two discovery surfaces with different intent:

- `/projects`
- `/projects/mobile`

This is fine if intentional, but an auditor should decide whether:

- Hot Reload should exclusively trust `/projects/mobile`
- `/projects` should be extended to carry the same mobile-framework richness
- or both should be normalized behind one internal projection

### 4. TypeScript workspace still has unrelated pre-existing errors

I ran:

```bash
cd /Users/kivanccakmak/Workspace/yaver.io/mobile
npx tsc --noEmit
```

It failed, but on unrelated pre-existing issues:

- `app/phone-project/code/[slug].tsx`
- `src/lib/phoneSandboxFsExpo.ts`

These are not from the mobile discovery change, but they still block a clean TS pass.

### 5. No live agent/manual verification against the real repo state in this session

I did not run the actual agent and inspect `/projects/mobile` live against the current real workspace contents. I verified by unit/integration-style Go tests only.

## Exact Files Changed In This Session

- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)
- [mobile/app/(tabs)/apps.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/apps.tsx)
- [desktop/agent/mobile_projects_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/mobile_projects_test.go)

Note:

- [mobile/ios/Yaver/Info.plist](/Users/kivanccakmak/Workspace/yaver.io/mobile/ios/Yaver/Info.plist) was already dirty in the worktree and was not modified by me for this task.

## Recommended Audit Checklist

For Claude Code or another auditor, I would check:

1. Confirm the product intent:
   - Hot Reload must use `/projects/mobile`, not `/projects`
   - `swift`, `kotlin`, `flutter`, `expo`, `react-native` all belong in the same mobile list

2. Verify live agent output:
   - run the agent
   - hit `GET /projects/mobile`
   - confirm real `yaver.io` nested apps are discovered with correct frameworks

3. Add an HTTP-level Go test for `/projects/mobile`:
   - not just `scanMobileProjects()`
   - verify returned JSON includes expected framework/path fields

4. Add a mobile/headless test for the Hot Reload tab:
   - list is populated from `/projects/mobile`
   - Rediscover hits `POST /projects/mobile`
   - row tap uses path-aware actions

5. Audit `open_app` matching:
   - exact label
   - repo-style label
   - path leaf
   - duplicate-name edge cases

6. Review whether `/projects` and `/projects/mobile` should be unified or kept separate by design.

## Short Summary

Implemented:

- mobile Hot Reload now reads the Go agent's mobile-framework scanner
- refresh uses the same scanner
- project action resolution is path-aware
- added Go test proving nested mobile frameworks inside `~/Workspace/yaver.io` are discovered correctly

Verified:

- targeted Go tests pass for nested Expo/RN/Flutter/Swift/Kotlin discovery

Not yet verified:

- full end-to-end mobile UI flow
- live `/projects/mobile` against the real running agent in this session
- full `open_app` integration after the data-source switch
