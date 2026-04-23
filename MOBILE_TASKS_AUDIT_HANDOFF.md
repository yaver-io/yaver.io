# Mobile Tasks Audit Handoff

This document summarizes the recent work around Yaver publish runners, runner readiness/error handling, and the mobile Tasks UI so another agent can audit the current implementation and tighten anything that still needs work.

## Original User Context

The work in this handoff was driven by a sequence of direct product-building prompts. The initial request was to architect and immediately implement a Yaver-internal GitHub-runner-style publishing system so developers can use their own hardware for releases instead of paying GitHub/GitLab CI for routine publish jobs.

The first user prompt was effectively:

> inside yaver we should make such github runner thing for developers to use their own hardware to make publishes through npm flutter dev pypi etc. all to not pay to github/gitlab for ci lets immediately architect it and implement it and make it to have mobile/web/mcp etc driven config (token etc.) settings make architecture now first but in yaver we can use github for now for ease

The user then clarified and extended the request through the following follow-up prompts, in order:

1. `same for mobile deploys if platform is capable like testflight/google play internal test etc. etc.`
2. `if user is ok with github fallback`
3. `ok but wire it with mobile app etc. etc. as well mention about it in developer portal readme.md as well also lets make all publishers be able to get secrets from github config etc. as well and lets run all publishers (npm pypi dart ) but not mobiles now to test`
4. `make wire to mcp of yaver etc as well`
5. `can we use uplaoder of yaver like dogfood also we need to mcp submitter uploader ci in both yaver and in github so yaver should first yaver's stack to upload/register and if fails fallsback to gh btw`
6. `/Users/kivanccakmak/Downloads/ScreenRecording_04-20-2026 01-33-59_1.MP4  in yaver /Users/kivanccakmak/Desktop/Screen Recording 2026-04-20 at 01.33.46.mov  if none of agents is detected at all go agent should smartly return that no agent detcted. if agents deteclted (like claude code / codex / ollama etc.) it should say those agents are available but non is logged in etc.`
7. `ok commit push etc. also try to deploy npm with yavers runner by using yaver's mcp etc.`
8. `/Users/kivanccakmak/Downloads/ScreenRecording_04-20-2026 01-43-29_1.MP4  /Users/kivanccakmak/Desktop/Screen Recording 2026-04-20 at 01.43.04.mov  this worked but two problems first is we need to see  logs since we will market it in landing video etc according to that. second is when we go back to yaver from third party app it should have live tasks session with priorly connected machine that it had hermes bundle from already connected why to tap user extra one more time. lastly in tasks ui dont show expo build etc. in tasks it just tasks. user can go to expo bundle from hot reload`
9. `but make sure to not break md stream from logs from go agent through mobile app etc. as well`
10. `and try to make mobile app's tasks ui similar like claude remote as much as possible make some animations etc. but stay lean etc. as well`
11. `maybe streamer may propose some animation etc. for mobile to show with that box`
12. `maybe some texts like baked cooked etc. but not just steal directly from claude code`
13. `also working compiling searching etc.`
14. `lets make for unavoidable error cases as well like no agent is authenticated etc. as well agents are not available. agents available but non is authenticated or like non is runnable etc.`

This handoff should be read as the implementation response to that prompt sequence, not as a greenfield proposal.

## Video References Provided By User

The user explicitly provided these local recordings as context for UX issues and desired behavior:

- `/Users/kivanccakmak/Downloads/ScreenRecording_04-20-2026 01-33-59_1.MP4`
- `/Users/kivanccakmak/Desktop/Screen Recording 2026-04-20 at 01.33.46.mov`
- `/Users/kivanccakmak/Downloads/ScreenRecording_04-20-2026 01-43-29_1.MP4`
- `/Users/kivanccakmak/Desktop/Screen Recording 2026-04-20 at 01.43.04.mov`

These paths should be treated as audit/demo references for:

- agent-detection messaging quality
- task/session continuity after returning from third-party apps
- logs visibility in the mobile Tasks experience
- overall Tasks UI presentation quality for marketing/demo footage

## Scope

The work covered three broad areas:

1. Yaver-first publish runner architecture with MCP/mobile/web wiring.
2. Runner detection/readiness classification so the product can distinguish:
   - no agents installed
   - agents installed but not authenticated
   - agents installed but not runnable
3. Mobile Tasks UI improvements:
   - better logs visibility
   - auto-resume/reconnect when returning from third-party apps
   - generic task labels instead of build-tool-heavy labels
   - leaner Claude-Remote-like visual direction
   - animated Yaver-specific task phase chips like `searching`, `cooking`, `compiling`

## Publish Runner Architecture

### Goal

Make Yaver use developer-owned hardware first for package/store publishing, dogfood Yaver’s own upload/register path, and only fall back to GitHub when explicitly allowed and requested.

### Main backend file

- [desktop/agent/publish.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/publish.go)

### What was added

- `PublishConfig`, `PublishTarget`, `PublishFallback`, `PublishRun`, `PublishManager`
- project config file path:
  - `.yaver/publish.yaml`
- local-first publish execution for:
  - `npm`
  - `pypi`
  - `pubdev`
  - `testflight`
  - `playstore`
- explicit fallback semantics:
  - local/Yaver path runs first
  - if local execution fails and fallback is allowed, dispatch GitHub workflow
- Yaver dogfood uploader path:
  - artifacts are archived via Yaver blob storage before relying on external fallback
- support fields on publish targets:
  - `uploader`
  - `submitter`
  - `prepareCommand`
  - `artifactGlobs`
  - `envFromVault`
  - `envFromGitHub`

### Notable publish behavior

- `npm` publish commands were changed from plain `npm publish` to:
  - `PKG_FILE="$(npm pack | tail -n 1)" && npm publish "$PKG_FILE" --access public`
- reason:
  - Yaver needs a concrete tarball artifact to archive/upload/register through its own stack
- mobile publish path registers artifacts with `BuildManager`
- shell publish path tries to archive produced artifacts using `artifactGlobs`

### Config file

- [.yaver/publish.yaml](/Users/kivanccakmak/Workspace/yaver.io/.yaver/publish.yaml)

Current checked-in config covers:

- npm:
  - `npm-cli`
  - `npm-sdk-errors-js`
  - `npm-sdk-js`
  - `npm-feedback-react-native`
  - `npm-feedback-web`
- pypi:
  - `pypi-sdk-python`
- pub.dev:
  - `pubdev-feedback-flutter`
  - `pubdev-sdk-flutter`
- mobile:
  - `testflight-mobile`
  - `playstore-mobile`

### HTTP surface

Mounted in [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go):

- `GET/POST /publish/config`
- `POST /publish/run`
- `GET /publish/runs`
- `GET /publish/runs/:id`

### CLI surface

Mounted in [desktop/agent/main.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/main.go):

- `yaver publish init`
- `yaver publish config`
- `yaver publish run`
- `yaver publish list`
- `yaver publish status`

### MCP surface

Declared in [desktop/agent/mcp_tools.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/mcp_tools.go) and wired in [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go):

- `publish_config_get`
- `publish_run`
- `publish_submit`
- `publish_upload`
- `publish_ci_dispatch`
- `publish_list`
- `publish_status`

Intent:

- MCP clients can think in terms of “run”, “submit”, “upload”, or “CI dispatch”
- all aliases still hit the same local-first Yaver publish pipeline

### GitHub fallback workflow

- [.github/workflows/yaver-publish.yml](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/yaver-publish.yml)

Also scaffoldable via:

- [desktop/agent/ci_add_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/ci_add_cmd.go)
- `yaver ci add publish-runner`

### Secret resolution

Implemented in `resolvePublishEnv()` in `publish.go`.

Resolution order is effectively:

1. explicit `env`
2. vault lookup
3. direct env var on machine
4. GitHub-injected env var mapping

### Vault crash fix

Problem encountered:

- original vault access path could terminate the daemon if the derived passphrase didn’t match the actual vault state

Fix:

- added non-fatal `optionalVaultToken()` usage for publish flows
- publish runs now continue instead of crashing the entire daemon

## Publish Wiring in Web and Mobile

### Web

Files:

- [web/lib/agent-client.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/agent-client.ts)
- [web/components/dashboard/BuildsView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/BuildsView.tsx)
- [web/app/docs/developers/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/docs/developers/page.tsx)

Added:

- fetch/save publish config
- start publish
- list publish runs
- get publish status
- publish UI in dashboard builds view
- docs updates for local-first publish and Yaver-first uploader path

### Mobile publish/build wiring

Files:

- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)
- [mobile/app/(tabs)/builds.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/builds.tsx)

Added:

- publish config fetch/save methods
- publish run/list/status client methods
- publish section in mobile Builds tab
- GitHub fallback toggle in mobile

## Runner Detection and Readiness Classification

### Problem

Previously Yaver could collapse different failure cases into something too vague, especially around runner detection.

Desired distinction:

- no agents installed at all
- agents exist but none are authenticated
- agents exist but none are runnable

### Main backend changes

Files:

- [desktop/agent/main.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/main.go)
- [desktop/agent/runner_auth.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/runner_auth.go)

### What changed

During `yaver serve` runner auto-detection:

- detected agents are now split into:
  - `available`
  - `unavailable`
- if no binaries are detected:
  - log “No AI agent found”
- if binaries are detected but none are ready:
  - log that agents were detected but none are ready/authenticated/configured
- if some are ready and some are not:
  - prompt only with ready ones
  - also list installed-but-not-ready ones separately

### Current runtime readiness sources

From `runner_auth.go`:

- Claude:
  - env vars
  - auth token env
  - Claude credential file / keychain assumptions
- Codex:
  - `OPENAI_API_KEY`
  - `~/.codex/auth.json` or `CODEX_HOME/auth.json`
  - vault-backed API key
- OpenCode:
  - auth/config file heuristics
  - blocks unsupported Anthropic OAuth wrapper case

### Mobile data model update

File:

- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)

Added fields so mobile can reason about readiness instead of only installation:

- `RunnerInfo.authConfigured`
- `RunnerInfo.authSource`
- `RunnerInfo.warning`
- `RunnerInfo.error`
- `AgentStatus.runner.authConfigured`
- `AgentStatus.runner.authSource`
- `AgentStatus.runner.warning`

## Mobile Tasks UI Changes

### Files changed

- [mobile/app/(tabs)/tasks.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/tasks.tsx)
- [mobile/src/context/DeviceContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/context/DeviceContext.tsx)

### Important constraint preserved

The Markdown task stream from the Go agent was intentionally not changed.

What remained untouched:

- chat/task stream rendering path
- Markdown rendering of assistant content
- existing stream-driven task output behavior

The new logs surface is separate and plain-text.

### 1. Logs visibility

Problem:

- user wants visible logs for demos/landing videos and for practical debugging

Changes in `tasks.tsx`:

- added `Logs` action in the main task action row
- added `Logs` button in task detail header
- logs modal title changes to `Live Logs` when a task is selected
- logs modal shows:
  - selected task output/result text first
  - connection logs after that
- copy action now copies both task log text and connection logs

This is intentionally separate from Markdown chat rendering.

### 2. Foreground auto-resume after returning from third-party app

Problem:

- after returning from Expo / third-party app to Yaver, the app should not require another manual device tap if the session already exists

Changes in `DeviceContext.tsx`:

- added `AppState` listener
- when app becomes active again:
  - if there is an `activeDevice`
  - and the user did not explicitly disconnect
  - and connection is not already healthy
  - trigger reconnect automatically

Current behavior:

- use existing reconnect loop if one is already in progress
- otherwise trigger reconnect immediately

### 3. Generic task labels instead of build-system branding

Problem:

- Tasks surface should say “tasks”, not “Expo build” etc.
- hot reload/build-specific flows belong conceptually elsewhere

Changes:

- added `normalizeTaskTitle()`
- patterns like Expo/Xcode/Gradle/Flutter/RN build are collapsed to:
  - `Build`
  - `Hot Reload`

Used in:

- task cards
- fallback user message in task chat builder
- task detail header
- log modal task section title

### 4. Claude-Remote-like lean UI direction

Goal:

- keep it lean and mobile-first
- feel closer to a remote coding client, less like a dense dashboard
- avoid copying Claude branding/microcopy directly

Changes in `tasks.tsx`:

- task cards:
  - animated entrance
  - softer card silhouette
  - calmer metadata layout
  - runner label/time moved to the side
- status chrome:
  - running pulse dot
  - pill-based status/metas
- overall spacing:
  - rounder cards and sheets
  - less hard border-heavy dashboard look
- chat sheet:
  - cleaner hierarchy
  - more refined bubble geometry
- FAB:
  - darker neutral floating button instead of bright utility look

### 5. Yaver-specific animated phase chip

User wanted something similar in spirit to a live remote coding client, but not stolen directly from Claude.

Implemented:

- `deriveTaskPhases()`
- `PhaseChip`

Current derived microcopy:

- `searching`
- `mapping`
- `cooking`
- `compiling`
- `checking`
- `shipping`
- fallback: `working`

Important:

- these are inferred from actual task title/output/result text
- not fake static progress
- the chip cross-fades between detected phases for running/queued tasks

Appears in:

- task card for running tasks
- task detail header for running tasks

### 6. Unavoidable error states in mobile banner

Problem:

- mobile banner previously could only say something like runner installed/not installed
- not enough to distinguish real user-facing failure cases

Added in `tasks.tsx`:

- `deriveRunnerBannerState()`

Current banner cases:

- `No agents available`
- `Agents available, none authenticated`
- `Agents available, none runnable`
- `<runner> not installed`
- `<runner> needs sign-in`
- `<runner> blocked`
- `<runner> ready`

This uses:

- `availableRunners` from `/agent/runners`
- `agentStatus` from `/agent/status`

## Validation Performed

### Passed

- `go test -run 'TestDetectRunnerRuntimeStatus|TestDoesNotExist' .` in `desktop/agent`
- `go test -run TestDoesNotExist .` in `desktop/agent`
- `npm exec -- tsc -p tsconfig.json --noEmit` in `web`
- `npm exec -- tsc -p tsconfig.json --noEmit` in `mobile`

### MCP publish test performed

Tested through actual MCP HTTP endpoint, not just raw CLI:

- called `publish_submit` for `npm-cli`
- MCP created a real publish run and hit the local Yaver publish pipeline

Observed result:

- local path executed
- npm publish failed with `E404`
- GitHub fallback did not dispatch because `github-token` was missing from Yaver vault

### Local publish smoke run summary

Observed machine-specific failures:

- npm targets:
  - local path runs
  - npm publish returns `E404`
- pypi:
  - `python` missing on machine
- pub.dev:
  - versions already exist
- GitHub fallback:
  - blocked due to missing `github-token` in vault

## Known Risks / Audit Points

### Publish

1. Confirm artifact archival timing is correct for every target kind.
2. Check whether artifact archive should happen:
   - before publish
   - after publish
   - or both
3. Verify npm `pack -> publish tarball` is acceptable for all packages here.
4. Confirm GitHub fallback should trigger on all local execution failures or only selected classes.
5. Check whether publish runs should expose streamable live logs, not just final status snapshots.

### Mobile Tasks UI

1. `deriveTaskPhases()` is heuristic.
   - audit whether the text matching is too broad or too naive
2. visual pass may still be incomplete.
   - there was no simulator/screenshot verification in this handoff
3. task card motion uses `Animated`.
   - audit whether it causes any list perf issue on lower-end devices
4. `normalizeTaskTitle()` is intentionally aggressive.
   - audit whether it over-normalizes task titles that should remain specific
5. logs modal currently mixes:
   - task live output
   - connection logs
   audit whether these should be tabbed/separated more explicitly

### Foreground reconnect

1. AppState reconnect is intentionally simple.
2. Audit whether it can race with:
   - manual reconnect
   - network-change reconnect
   - auth recovery flow
3. Confirm there is no edge case where user intentionally wants disconnected-but-not-flagged state.

### Runner banner classification

1. Current logic uses:
   - installed
   - authConfigured
   - error
2. Audit whether “runnable” should be based on a stronger signal than `!error`.
3. Confirm wording/tone is right for users.

## Current Uncommitted Mobile Follow-Up

At the time of writing this handoff, the newer mobile Tasks UI / banner-state work is not yet described as committed here by hash.

Files currently involved in that follow-up:

- [mobile/app/(tabs)/tasks.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/tasks.tsx)
- [mobile/src/context/DeviceContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/context/DeviceContext.tsx)
- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)

## Suggested Audit Checklist

1. Read `desktop/agent/publish.go` end-to-end for publish fallback and artifact archival semantics.
2. Verify MCP aliases and HTTP wiring in `httpserver.go` and `mcp_tools.go`.
3. Check `.yaver/publish.yaml` against actual intended release ownership and secret names.
4. Audit `main.go` runner auto-detect UX for:
   - zero installed
   - installed but unauthenticated
   - mixed ready/unready
5. Audit `mobile/app/(tabs)/tasks.tsx` for:
   - visual quality
   - animation smoothness
   - phase chip heuristics
   - unavoidable-error messaging
   - logs UX
6. Audit `DeviceContext.tsx` foreground reconnect behavior for race conditions.
7. Confirm Markdown task streaming was not regressed.
