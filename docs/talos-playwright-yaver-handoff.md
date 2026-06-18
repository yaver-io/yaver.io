# Talos + Yaver Playwright Handoff

## Purpose

This handoff is for implementing Talos-driven third-party app development and
testing through Yaver, using a Hetzner machine as the remote execution box.

The goal is not to make Talos learn every Yaver HTTP route. Talos should treat
Yaver as the execution plane and call one stable control surface:

```json
{
  "machine": "primary-or-device-id",
  "verb": "project_test_run",
  "payload": {
    "dir": "/workspace/talos",
    "root": "/workspace/talos/yaver-tests",
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

- `project_test_run` is the remote-capable web test runner.
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
  "verb": "project_test_run",
  "payload": {
    "dir": "/workspace/talos",
    "root": "/workspace/talos/yaver-tests",
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

Use `target: web-playwright` in a YAML spec when Playwright-specific behavior is
needed.

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
  "storageState": "talos-admin",
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
- Supports `headed`, `trace`, `profile`, and explicit `storageState`.

### P2: Persistent Browser Profiles

Basic storage-state/profile support is implemented for `playwright_run`:

```text
~/.yaver/browser-profiles/<profile>/
~/.yaver/playwright-storage/<profile>.json
```

Requirements:

- Profiles are local to the machine.
- Never sync cookies/tokens to Convex.
- Allow Talos to reference a profile name, not raw cookie content.
- `profile` maps to `~/.yaver/playwright-storage/<profile>.json`.
- The Playwright sidecar loads storage state if the file exists and writes it
  back after the run.
- The full human-in-the-loop auth path using existing browser interactive
  endpoints is still a follow-up.

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

Current `project_test_report` can reference screenshots and clips. If Talos
needs to fetch an artifact, prefer an ops verb or existing `project_test_artifact`
rather than opening arbitrary files.

Check:

- `desktop/agent/ops_testkit.go`
- verb `project_test_artifact`

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

- From Codex through Yaver MCP, call `ops(machine:"primary", verb:"project_test_run", ...)`.
- Test run executes on the Hetzner box.
- Playwright-target specs either run or return a precise missing-dependency
  message.
- `project_test_report` returns pass/fail feature data.
- Artifacts are retrievable through a scoped verb, not arbitrary file reads.

Better version:

- `playwright_status` exists. **Implemented.**
- `playwright_run` exists. **Implemented.**
- Persistent Playwright storage-state profile names work. **Implemented for
  Playwright storageState; interactive login handoff still follow-up.**
- Trace/video artifacts are visible to Talos.
- Hetzner readiness can be checked and repaired without SSH.

## Implementation Status

Implemented in this pass:

- `playwright_status` ops verb in `desktop/agent/ops_testkit.go`
- `playwright_run` ops verb in `desktop/agent/ops_testkit.go`
- `forcePlaywright` runtime conversion for web specs
- Playwright `headed`, `trace`, `profile`, and `storageState` payload fields
- Playwright storage-state load/save in `desktop/agent/testkit/driver_playwright.go`
- Focused test coverage in `desktop/agent/testkit/driver_playwright_test.go`

Verification:

```bash
cd desktop/agent && go test -count=1 ./testkit
```

This passed locally.

Current repo blocker:

```bash
cd desktop/agent && go test -count=1 -run 'Test.*(Testkit|Playwright|Ops|Studio)' .
```

The root package currently fails to parse because of unrelated existing syntax
errors in `desktop/agent/deploy_cmd.go` and `desktop/agent/ops_box.go`. Those
must be cleaned up before root-package tests can validate the new ops verbs.

## Non-Goals For This Pass

- Replacing chromedp globally with Playwright.
- Making Talos call raw Yaver HTTP routes directly.
- Building an IP-rotation or scraping system.
- Solving iOS native builds on Hetzner.
- Moving raw browser/session artifacts into Convex.
