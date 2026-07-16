# Talos + Yaver Playwright Handoff

## Purpose

This handoff is for implementing Talos-driven third-party app development and
testing through Yaver, using a selected Yaver machine as the remote execution
box. For Apple-surface work, prefer the user-owned Mac mini profile in
`docs/mac-mini-remote-worker.md` over a Linux/Hetzner box because it carries
Xcode, simulator runtimes, and the Codex defaults expected by local development.

The goal is not to make Talos learn every Yaver HTTP route. Talos should treat
Yaver as the execution plane and call one stable control surface:

```json
{
  "machine": "primary-or-device-id",
  "verb": "playwright_run",
  "payload": {
    "dir": "/workspace/talos",
    "root": "/workspace/talos/yaver-tests",
    "profile": "talos-admin",
    "trace": true,
    "video": true,
    "concurrency": 1
  }
}
```

Yaver runs the browser/dev/build/test work on the selected machine. Talos owns
the product workflow, result interpretation, and long-term records.

## Verified Current State

Do not rely on older docs blindly. The code currently shows:

- `ops` is the right cross-machine surface.
  - MCP tool definition: `desktop/agent/mcp_tools.go`
  - MCP dispatch case: `desktop/agent/httpserver.go`, case `"ops"`
  - Router: `desktop/agent/ops.go`
  - Supports `machine: "local" | "primary" | "auto" | deviceId/alias`.

- `playwright_run` is the first-class remote Playwright runner for Talos/Yaver.
  - Registered in `desktop/agent/ops_testkit.go`
  - Runs `yaver-tests/*.test.yaml`
  - Forces `target: web` specs to `target: web-playwright` at runtime
  - Supports `headed`, `trace`, `video`, `profile`, `storageState`, `env`,
    `only`, and `concurrency`.
  - Returns a job id, then `project_test_report` returns feature reports and
    artifact paths.

- `project_test_run` remains the generic remote-capable web test runner.
  - Registered in `desktop/agent/ops_testkit.go`
  - Runs `yaver-tests/*.test.yaml`
  - Returns a job id, then `project_test_report` returns feature reports and
    artifact paths.

- `testkit_run` is local MCP only.
  - Handler: `desktop/agent/testkit_mcp.go`
  - Useful inside one machine, but not the Talos remote-control boundary.

- Playwright exists inside the embedded testkit, but only as a generated Node
  sidecar for specs with `target: web-playwright`.
  - Target enum: `desktop/agent/testkit/spec.go`
  - Driver: `desktop/agent/testkit/driver_playwright.go`
  - It requires `node`, npm package `playwright`, and installed Chromium.

- Default web automation is still chromedp.
  - `desktop/agent/testkit/driver_chromecdp.go`
  - Browser/ghost interactive automation also uses chromedp:
    `desktop/agent/browser_interactive.go`, `desktop/agent/ops_ghost_web.go`

- Codex MCP setup is already implemented.
  - `desktop/agent/mcp-setup.go`
  - `setupCodex` uses `codex mcp add yaver -- yaver mcp`.
  - Older docs saying Codex setup is missing are stale.

## Architecture Decision

Use this layering:

1. Talos calls Yaver `ops`.
2. Yaver resolves the target machine.
3. Yaver executes on the Hetzner box or another selected machine.
4. Yaver returns structured results and local artifacts.
5. Talos stores the business/product records, summaries, and decisions.

Do not build Talos directly against:

- `/dev/*`
- `/testkit/*`
- `/browser/*`
- direct relay URLs
- individual MCP tools except `ops`, `ops_plan`, and `ops_verbs`

Those can change. `ops` is the intended stable facade.

## Primary Workflows

### 1. Talos Web App Regression

Talos repo contains:

```text
yaver-tests/
  smoke.test.yaml
  auth.test.yaml
  order-flow.test.yaml
```

Talos asks Yaver:

```json
{
  "machine": "primary",
  "verb": "playwright_run",
  "payload": {
    "dir": "/workspace/talos",
    "root": "/workspace/talos/yaver-tests",
    "profile": "talos-admin",
    "trace": true,
    "env": {
      "TALOS_SESSION_TOKEN": "..."
    },
    "video": true,
    "concurrency": 1
  }
}
```

Then poll:

```json
{
  "machine": "primary",
  "verb": "project_test_report",
  "payload": {
    "jobId": "<job-id>"
  }
}
```

### 2. Playwright-Specific Specs

Use `playwright_run` when Talos wants the whole web suite to execute through
Playwright. Existing `target: web` specs are converted to `target:
web-playwright` for that run only.

Use `target: web-playwright` in a YAML spec when the spec should always require
Playwright, even through generic `project_test_run`.

Example:

```yaml
name: talos smoke
target: web-playwright
url: http://127.0.0.1:3000
viewport:
  width: 1280
  height: 800
steps:
  - goto: /
  - assert.text: Talos
```

Current limitation: this is not a persistent Playwright service. It generates a
temporary Node script per run, launches Chromium, runs steps, and exits.

### 3. Third-Party React Native / Expo Apps

For third-party RN apps, never load them through WebView. The repo rule is to
use Hermes bundle push into the Yaver mobile container.

Relevant code/docs:

- `desktop/agent/devserver_http.go`
- `desktop/agent/ops_reload.go`
- `cli/src/{analyzer,bundler,discovery,transport}.js`
- `CLAUDE.md`, Hermes-push section

Use Yaver for:

- dependency prep
- dev server start
- Hermes build
- reload/push to paired phone

Hetzner is good for Linux-side builds and web tests. It is not good for iOS
native builds that require Xcode or a USB-attached iPhone.

## Hetzner Role

The Hetzner machine should be:

- owner-authenticated with Yaver
- reachable as `primary` or a stable alias/device id
- configured with Node, Playwright, Chromium, package managers, and repo clone
  credentials
- used for repeatable web/dev/test loops

It should not be used for:

- iOS native builds requiring Xcode
- TestFlight upload
- USB-device install flows
- aggressive third-party scraping from a datacenter IP

Respect the repo’s policy in `CLAUDE.md`: stop on 403/429/451, do not rotate IPs
to bypass blocks, prefer official APIs or user-owned/residential devices for
sensitive collection.

## Implementation Tasks

### P0: Toolchain Readiness

Implemented ops verb:

```text
playwright_status
```

It should report:

- `node` path/version
- whether `playwright` npm package is resolvable
- whether Chromium is installed
- Playwright cache path if found
- suggested fix commands

This should run on any `machine` through `ops`.

Example:

```json
{
  "machine": "primary",
  "verb": "playwright_status",
  "payload": {
    "dir": "/workspace/talos"
  }
}
```

Repair is also first-class:

```json
{
  "machine": "primary",
  "verb": "playwright_repair",
  "payload": {
    "include": ["node", "playwright", "ffmpeg"]
  }
}
```

`playwright_repair` reuses the existing async dependency installer and returns a
Studio job id. Poll `studio_job_status`, then call `playwright_status` again.

### P1: First-Class Playwright Run Verb

Implemented ops verb:

```text
playwright_run
```

Initial payload:

```json
{
  "dir": "/workspace/talos",
  "root": "/workspace/talos/yaver-tests",
  "only": "smoke",
  "headed": false,
  "trace": true,
  "video": true,
  "profile": "talos-admin",
  "devCommand": "npm run dev",
  "waitURL": "http://127.0.0.1:3000",
  "devTimeoutSec": 60,
  "env": {}
}
```

Current implementation:

- Reuses existing `testkit` and `studioJobs.startTestkitRun`.
- Forces `target: web` specs to `target: web-playwright` at runtime, so Talos
  does not need to rewrite every existing web YAML spec.
- Returns a Studio job snapshot; poll `studio_job_status`, then call
  `project_test_report`.
- Keeps artifact/report logic shared with `project_test_run`.
- Supports `headed`, `trace`, `profile`, explicit `storageState`,
  `devCommand`, `waitURL`, `devTimeoutSec`, and `keepDevServer`.
- When `devCommand` is provided, Yaver starts the app server in the project
  directory, streams its logs into the Studio job, waits for `waitURL` (or the
  first spec URL), runs the suite, and stops the process group by default.

### P1B: Native Playwright Project Runner

Implemented ops verb:

```text
playwright_native_run
```

Use this when the target app already has `playwright.config.*` and native
Playwright `.spec.ts` / `.spec.js` tests. It runs `npx playwright test` in the
project directory instead of translating Yaver YAML specs.

Example:

```json
{
  "machine": "primary",
  "verb": "playwright_native_run",
  "payload": {
    "dir": "/workspace/talos",
    "config": "playwright.config.ts",
    "project": "chromium",
    "grep": "checkout",
    "workers": 1,
    "trace": "retain-on-failure",
    "devCommand": "npm run dev",
    "waitURL": "http://127.0.0.1:3000",
    "env": {
      "E2E_BASE_URL": "http://127.0.0.1:3000"
    }
  }
}
```

Current implementation:

- Runs through a Studio job and returns a job id.
- Defaults to Playwright JSON reporter and writes artifacts under
  `~/.yaver/playwright-native/<jobId>/`.
- Exposes the result through `project_test_report` so Talos can use the same
  report polling/fetching path as `playwright_run`.
- Captures `run.log`, `results.json`, trace/video/screenshot/html/xml files
  found in the artifact directory.
- Supports the same `devCommand` / `waitURL` app startup lifecycle.

### P2: Persistent Browser Profiles

Basic storage-state/profile support is implemented for `playwright_run`:

```text
~/.yaver/playwright-storage/<profile>.json
```

Requirements:

- Profiles are local to the machine.
- Never sync cookies/tokens to Convex.
- Allow Talos to reference a profile name, not raw cookie content.
- `profile` maps to `~/.yaver/playwright-storage/<profile>.json`.
- The Playwright sidecar loads storage state if the file exists and writes it
  back after the run.
- `playwright_profiles` lists saved profile files on the selected machine.
- `playwright_profile_delete` deletes a saved profile file on the selected
  machine.
- `playwright_profile_auth` opens a headed Playwright browser on the selected
  machine, lets the user complete sign-in, then saves storage state to the named
  profile. Use `successURL` when the app has a reliable post-login URL; otherwise
  the job saves state after `timeoutSec`.

Examples:

```json
{
  "machine": "primary",
  "verb": "playwright_profile_auth",
  "payload": {
    "dir": "/workspace/talos",
    "url": "https://talos.example.com/login",
    "successURL": "/dashboard",
    "profile": "talos-admin",
    "timeoutSec": 180
  }
}
```

```json
{
  "machine": "primary",
  "verb": "playwright_profiles",
  "payload": {}
}
```

```json
{
  "machine": "primary",
  "verb": "playwright_profile_delete",
  "payload": {
    "profile": "talos-admin"
  }
}
```

### P3: Talos Contract

Talos should store only structured summaries:

- test run id
- project/repo id
- machine alias/device id
- status
- feature pass/fail counts
- artifact references
- failure summary
- timestamp

Do not store raw screenshots, raw tokens, local absolute paths, or secrets in
Talos Convex unless explicitly designed and privacy-reviewed.

### P4: Artifact Fetching

Current `project_test_report` includes both backwards-compatible per-feature
paths and a normalized `artifacts` array. Artifact refs include `kind`, `path`,
`name`, `mimeType`, `bytes`, `feature`, and `step` where applicable.

If Talos needs to fetch an artifact, use the Playwright-named scoped verb:

```json
{
  "machine": "primary",
  "verb": "playwright_artifact",
  "payload": {
    "jobId": "<job-id>",
    "path": "<tracePath-or-screenshot-or-clip-from-report>"
  }
}
```

`project_test_artifact` remains available for the generic testkit path. Both
verbs only serve files referenced by the stored report; they do not allow
arbitrary filesystem reads.

Check:

- `desktop/agent/ops_testkit.go`
- verb `project_test_artifact`
- verb `playwright_artifact`

### P5: Run Inventory and Cleanup

Implemented Playwright management verbs:

```text
playwright_artifacts
playwright_runs
playwright_gc
playwright_profile_auth_finish
playwright_profile_auth_cancel
```

Use cases:

- `playwright_artifacts {jobId}` returns the normalized artifact index without
  base64 payloads.
- `playwright_runs {limit}` lists local run/auth artifact directories under:
  - `~/.yaver/playwright-native`
  - `~/.yaver/playwright-auth`
  - `~/.yaver/testkit`
- `playwright_gc {olderThanHours, dryRun}` deletes old artifact directories from
  those known roots only. Default is `dryRun: true`.
- `playwright_profile_auth_finish {jobId}` lets mobile/Talos save storage state
  immediately after the user completes 2FA in the headed browser.
- `playwright_profile_auth_cancel {jobId}` cancels the headed auth job.

## Redroid / Chromedp / Mobile-Web Wiring Audit

This is the current split in code:

- **Chromedp web path exists and is UI-wired.**
  - Backend: `desktop/agent/ops_testkit.go` registers `project_test_run`,
    `project_test_report`, `project_test_artifact`, and `project_test_grow`.
  - Runner: `desktop/agent/testkit/runner.go` dispatches `target: web` to
    chromedp.
  - Driver: `desktop/agent/testkit/driver_chromecdp.go` provides the lower-level
    CDP backend for browser automation/snapshotting.
  - Web UI: `web/components/dashboard/WebTestsPanel.tsx` calls
    `project_test_run`, polls `studio_job_status`, then calls
    `project_test_report` and `project_test_artifact`.
  - Mobile UI: `mobile/src/lib/testkitClient.ts` and `mobile/app/project-tests.tsx`
    mirror the same generic testkit verbs.

- **Playwright backend exists and basic web/mobile UI wiring is implemented.**
  - Backend verbs now include `playwright_status`, `playwright_repair`,
    `playwright_run`, `playwright_native_run`, `playwright_profile_auth`,
    profile list/delete/finish/cancel, artifact index/fetch, runs list, and GC.
  - YAML path: `playwright_run` forces `target: web` specs to
    `target: web-playwright`.
  - Native path: `playwright_native_run` runs `npx playwright test`.
  - Web UI: `web/components/dashboard/WebTestsPanel.tsx` now has a run-mode
    selector for chromedp YAML, Playwright YAML, and native Playwright; exposes
    Playwright readiness/repair, profile selection, dev command/wait URL, native
    project/grep, trace capture, and normalized artifact refs.
  - Mobile UI: `mobile/src/lib/testkitClient.ts` exposes the Playwright ops
    verbs, and `mobile/app/project-tests.tsx` now mirrors the same run modes and
    controls for remote PC execution.
  - Web/mobile now expose headed `playwright_profile_auth` start/finish/cancel,
    run inventory, and GC dry-run controls.
  - Web/mobile now expose trace zip inspection via `playwright_trace_inspect`,
    including parsed Playwright action timeline entries.
  - Still missing UI polish: pixel-perfect parity with the upstream Playwright
    trace viewer, and first-class HTML/JSON report navigation beyond the basic
    artifact open/download flows.

- **Redroid path exists and is separately UI-wired.**
  - Backend: `desktop/agent/ops_qa.go` registers `qa_base_build`, `qa_base_up`,
    `qa_base_list`, `qa_run`, `qa_report`, and `qa_base_gc`.
  - QA jobs: `desktop/agent/qa_jobs.go` resolves local/SSH Redroid surfaces,
    warm base images, ephemeral test accounts, and LLM-backed flow execution.
  - Testkit mobile target: `desktop/agent/testkit/runner_mobile.go` supports
    `target: android-redroid` using the same mobile step vocabulary as Android
    emulator/device.
  - Web UI: `web/components/dashboard/QAPanel.tsx` calls `qa_run`,
    `studio_job_status`, and `qa_report`.
  - Mobile UI: `mobile/src/lib/qaClient.ts` and `mobile/app/qa.tsx` mirror
    Redroid QA ops.

- **Redroid + Playwright now have a combined Talos orchestration verb.**
  - Redroid validates Android app behavior through app UI flows.
  - Playwright validates web/RN-web/browser behavior through Chromium.
  - `talos_quality_run` executes a browser child run (`project_test_run`,
    `playwright_run`, or `playwright_native_run`) plus optional `qa_run`, then
    `talos_quality_report` returns one combined web + Android/Redroid result
    with dependency preflight status.
  - Web/mobile project test screens now expose a Full Quality action with
    optional Redroid package/APK/base fields.

- **Mobile/web artifact surfaces now show normalized refs, but rich viewing is
  still basic.**
  - Existing web/mobile test panels still fetch individual clips/screenshots
    through `project_test_artifact` for chromedp runs.
  - Backend now exposes normalized `artifacts[]` and `playwright_artifacts`.
  - Web/mobile now render normalized artifact refs for Playwright runs and fetch
    scoped artifacts through `playwright_artifact`.
  - Web now opens images/videos/text/html inline, inspects trace zip contents,
    and falls back to download for zip/binary artifacts. Mobile lists refs,
    inspects trace zip metadata, and keeps screenshot/video playback for feature
    evidence.

Practical next frontend/backend integration tasks:

1. Add upstream-style Playwright trace viewer parity for screenshots/snapshots.
2. Add a dedicated combined-quality dashboard history view on top of
   `talos_quality_report` and `playwright_runs`.
3. Add persistent saved Full Quality runs beyond in-memory report lookup.

## Safety Constraints

- Never put Hetzner IPs, hostnames, tokens, Apple keys, npm tokens, or relay
  passwords in tracked files.
- Do not commit or push without explicit user permission.
- Do not use WebView for third-party RN apps.
- Do not use Hetzner/datacenter IPs for sustained third-party scraping.
- Stop on blocks: 403, 429, 451, CAPTCHA walls, geo/IP denials.
- Use `access_policy_check` / collection planning for external data collection
  flows.
- Keep secrets local to the execution machine or Yaver vault.

## Suggested Codex Starting Points

Read these first:

```text
CLAUDE.md
desktop/agent/ops.go
desktop/agent/ops_testkit.go
desktop/agent/testkit/driver_playwright.go
desktop/agent/testkit/spec.go
desktop/agent/mcp_tools.go
desktop/agent/httpserver.go
desktop/agent/mcp-setup.go
docs/hetzner-shared-owner-runbook.md
```

Then verify current routes/tool names with `rg` before editing.

Useful searches:

```bash
rg -n 'Name:\s*"project_test_run"|Name:\s*"project_test_report"|Name:\s*"project_test_artifact"' desktop/agent
rg -n 'TargetWebPlaywright|web-playwright|runPlaywrightSpec' desktop/agent/testkit
rg -n 'case "ops"|case "ops_verbs"|dispatchOps' desktop/agent
rg -n 'setupCodex|autoSetupMCP' desktop/agent/mcp-setup.go
```

## Acceptance Criteria

Minimum useful version:

- From Codex through Yaver MCP, call `ops(machine:"primary", verb:"playwright_run", ...)`.
- Test run executes on the Hetzner box.
- Playwright-target specs either run or return a precise missing-dependency
  message.
- `project_test_report` returns pass/fail feature data.
- Artifacts are retrievable through a scoped verb, not arbitrary file reads.

Better version:

- `playwright_status` exists. **Implemented.**
- `playwright_run` exists. **Implemented.**
- Native Playwright project execution exists. **Implemented via
  `playwright_native_run`.**
- Persistent Playwright storage-state profile names work. **Implemented for
  Playwright storageState and headed profile-auth jobs.**
- Profile inventory/delete verbs exist. **Implemented.**
- Dev server lifecycle exists for app-under-test startup. **Implemented via
  `devCommand`/`waitURL`.**
- Trace/video artifacts are visible to Talos. **Implemented for report paths and
  normalized artifact refs plus scoped fetch.**
- Hetzner readiness can be checked and repaired without SSH. **Implemented via
  `playwright_status` and `playwright_repair`.**

## Implementation Status

Implemented in this pass:

- `playwright_status` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_repair` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_run` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_native_run` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_profile_auth` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_profiles` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_profile_delete` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_artifact` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_artifacts` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_runs` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_gc` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_profile_auth_finish` and `playwright_profile_auth_cancel` ops verbs
- `forcePlaywright` runtime conversion for web specs
- Playwright `headed`, `trace`, `profile`, `storageState`, `devCommand`, and
  `waitURL` payload fields
- Playwright storage-state load/save in `desktop/agent/testkit/driver_playwright.go`
- Playwright headed profile-auth storage-state job
- Native Playwright project runner with JSON-result parsing and artifact scan
- Playwright run inventory and known-root artifact cleanup
- Playwright trace path reporting, normalized artifact refs, and scoped artifact fetch
- Mobile testkit client methods for Playwright status/repair/YAML/native/profile/artifact/run-GC verbs
- Web dashboard Playwright mode selector, readiness/repair/profile controls,
  native-run fields, dev server fields, trace toggle, and artifact refs
- Mobile project-tests Playwright mode selector, readiness/repair/profile
  controls, native-run fields, dev server fields, trace toggle, and artifact refs
- `talos_quality_run` / `talos_quality_report` combined Playwright + Redroid
  orchestration verbs in `desktop/agent/ops_quality.go`
- Dependency preflight aggregation in `talos_quality_report`
- `playwright_trace_inspect` scoped trace zip metadata/listing/timeline verb
- Web/mobile Full Quality actions with optional Redroid package/APK/base fields
- Web/mobile Full Quality missing-preflight dependency repair action
- Web/mobile headed profile-auth start/finish/cancel controls
- Web/mobile Playwright run inventory and GC dry-run controls
- Web artifact drawer with inline image/video/text/html handling, trace zip
  inspection/timeline, and binary download fallback
- Mobile trace artifact metadata/listing/timeline inspection
- Focused test coverage in `desktop/agent/testkit/driver_playwright_test.go`
- Focused root-package helper/report coverage in
  `desktop/agent/ops_testkit_playwright_test.go`

Verification:

```bash
cd desktop/agent && go test -count=1 ./testkit
cd desktop/agent && go test -c -o /tmp/yaver-agent-playwright.test .
cd desktop/agent && /tmp/yaver-agent-playwright.test -test.run 'Test(ForceWebSpecsToPlaywright|PlaywrightProfilesListAndDelete|PlaywrightDeleteProfileRejectsOutsidePath|PlaywrightDevHelpers|WaitForPlaywrightURL|WaitForPlaywrightURLSeesEarlyProcessExit|BuildPlaywrightProfileAuthScript|PlaywrightRunsAndGCDryRun|BuildPlaywrightNativeCommand|BuildPlaywrightNativeReportFromJSON|BuildTestkitReportIncludesPlaywrightTraceArtifact)$' -test.v -test.timeout 20s
```

These passed locally. A broader root-package `go test -run ... .` invocation was
slow/hung in this repo during this pass, so the root package was verified with a
compiled test binary and focused Playwright tests.

## Non-Goals For This Pass

- Replacing chromedp globally with Playwright.
- Making Talos call raw Yaver HTTP routes directly.
- Building an IP-rotation or scraping system.
- Solving iOS native builds on Hetzner.
- Moving raw browser/session artifacts into Convex.
