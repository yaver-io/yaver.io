# Verified Task Architecture

Status: implementation contract. Code remains authoritative.

## Product invariant

A runner saying that work is complete is a claim. A verified task contains
evidence collected by the Yaver agent through the operation that matters to the
user. For browser work, that operation is a real Playwright run against the
task's project. Git changes, terminal output, and a demo video remain useful
evidence, but none of them imply that the requested behaviour works.

Verification is one consumer of a broader first-class Browser Automation
runtime. Once a developer configures a browser target, coding agents use the
same named session for exploration, reproduction, screenshots, recording, and
acceptance checks. An existing project-owned browser spec under
`yaver-tests/*.test.yaml` remains the deterministic verification format. The
runner may add or update that spec as part of the task, then call
`yaver_verify_task`. The tool blocks until the run has a terminal result and
returns structured pass/fail evidence to the same runner turn. A failed
verification therefore remains inside the coding loop instead of being
flattened into a green task-completion message.

## Existing code used by this design

- `desktop/agent/tasks.go` owns persisted task state, task events, and the wire
  `TaskInfo` projection.
- `desktop/agent/ops_testkit.go` already executes `yaver-tests` through the
  Playwright lane, stores a `testkitReport`, and records screenshots/traces as
  scoped local artifacts.
- `desktop/agent/browser.go`, `browser_video.go`, and the `browser_*` MCP tools
  already provide persistent Chromium/CDP sessions and MP4 recording.
- `desktop/agent/mcp_selenium.go` plus `desktop/agent/testkit/driver_firefox.go`
  and `driver_safari.go` provide W3C WebDriver primitives. At present the MCP
  session manager is Chrome-only even though Firefox and Safari drivers exist;
  the unified runtime closes that wiring gap.
- `desktop/agent/studio_jobs.go` supplies bounded asynchronous job state.
- `desktop/agent/mcp_tools.go` and `desktop/agent/httpserver.go` expose runner
  tools. The existing `yaver_request_render` forwarding pattern proves how a
  stdio runner child safely calls its sibling daemon with `YAVER_TASK_ID`.
- `desktop/agent/task_proof.go` records video and Git evidence. Verification is
  task evidence even when video proof is disabled, so its lifecycle is stored
  directly on the task rather than gated by `VideoEnabled`.
- `mobile/app/(tabs)/tasks.tsx` and the web task views already consume task
  detail plus structured SSE events. They must display agent-owned verification
  state; they must never run their own competing verification state machine.

## Ownership boundary

```text
runner                         Yaver agent                         clients
  |                                 |                                |
  | yaver_verify_task(request)      |                                |
  |-------------------------------->| validate task + project         |
  |                                 | persist running attempt         |
  |                                 | emit task_verification          |---->
  |                                 | start existing Playwright job   |
  |                                 | wait with wall-clock deadline   |
  |                                 | collect independent report      |
  |                                 | persist terminal evidence       |
  |                                 | emit task_verification          |---->
  |<--------------------------------| structured pass/fail report     |
  | fix and retry, or report done   |                                |
```

The agent is the sole writer of verification state. Clients can observe it and
may request a retry through an authenticated task route, but they do not infer
pass/fail, inspect arbitrary local paths, or assemble evidence themselves.

## First-class Browser Automation runtime

Browser automation is an agent subsystem, not a test-only helper and not a
client feature. It exposes a single logical tool family independent of the
configured engine:

```text
browser_targets
browser_open
browser_navigate
browser_snapshot
browser_click
browser_type
browser_select
browser_scroll
browser_wait
browser_wait_navigation
browser_extract_text
browser_extract_attribute
browser_screenshot
browser_close
yaver_verify_task
```

Existing tool names remain compatible. Internally, `browser_open` resolves a
configured target and returns both a stable `session_id` and the engine that
actually launched. Every later action dispatches by session ownership, so an
agent never has to switch from `browser_*` to a separate `selenium_*` grammar
merely because the developer chose Firefox or Safari.

### Browser target configuration

A project may commit non-secret defaults in `.yaver/browser.yaml`; developer
and machine overrides live under the owner-only Yaver config directory. Secret
profile contents/cookies remain in the existing local profile stores.

```yaml
version: 1
default: desktop-chrome
targets:
  desktop-chrome:
    engine: chrome
    driver: cdp
    profile: development
    headful: false
  firefox-linux:
    engine: firefox
    driver: webdriver
  safari-mac:
    engine: safari
    driver: webdriver
    headful: true
matrix:
  pull-request: [desktop-chrome, firefox-linux]
  release: [desktop-chrome, firefox-linux, safari-mac]
```

Resolution is capability based:

| Declared target | Real operation | Machine constraint |
| --- | --- | --- |
| `chrome/cdp` | Chrome or Chromium through CDP | Windows, Linux, macOS |
| `firefox/webdriver` | Firefox through geckodriver | Windows, Linux, macOS |
| `safari/webdriver` | Actual Safari through `safaridriver` | macOS only, remote automation enabled |

WebKit is never labelled Safari. Actual Safari proof is produced only by a
successful safaridriver session on macOS. Likewise Chromium coverage is not
labelled Google Chrome unless that binary was actually launched.

The initial unified-session implementation accepts Chrome, Firefox, and real
Safari. Edge and Playwright WebKit remain explicit follow-on engines; config
validation rejects them today instead of claiming coverage that did not run.

`browser_targets` reports configured intent and explicitly does not call it
ready. `browser_open` probes the real launch/session handshake and returns the
named failure when a configured target is unavailable. Configuration alone
never makes a target green.

### Agent usage policy

The coding prompt tells the runner to use Browser Automation whenever the task
depends on user-visible browser behaviour, external web-console state, or a web
reproduction. The runner should:

1. inspect capabilities and select the configured project target/matrix;
2. open or reuse a task-scoped session;
3. record when the user requested proof/demo or when the task originated from
   visual feedback;
4. operate through named DOM selectors and snapshots, never coordinates when a
   DOM is available;
5. stop on CAPTCHA, 2FA, consent, payment, legal declarations, or an access
   block and request human takeover;
6. close the session, finalizing video, before reporting completion;
7. run deterministic configured verification before claiming the behaviour is
   fixed.

Browser automation may assist platform submissions, but a browser agent cannot
silently accept agreements, change pricing, publish production releases, or
submit an app for review. Those remain typed, human-confirmed mutations.

## Automation run and artifact contract

Every browser session attached to a task has an `AutomationRun` record:

```json
{
  "id": "bar_...",
  "taskId": "...",
  "target": "safari-mac",
  "engine": "safari",
  "driver": "webdriver",
  "browserName": "Safari",
  "browserVersion": "...",
  "platform": "darwin/arm64",
  "status": "opening|running|finalizing|passed|failed|closed",
  "startedAt": "...",
  "finishedAt": "...",
  "artifacts": [
    {
      "id": "...",
      "kind": "video",
      "mimeType": "video/mp4",
      "status": "ready",
      "streamPath": "/tasks/<task>/automation/<run>/artifacts/<artifact>"
    }
  ]
}
```

Artifacts are agent-scoped descriptors, never raw filesystem paths:

- `video/mp4`: the complete recorded browser run, Range-enabled;
- `image/png` or `image/jpeg`: screenshots and poster;
- `application/zip`: trace, when supported;
- `application/json`: bounded structured report/HAR metadata;
- `text/plain`: bounded, redacted console summary.

CDP can capture frames directly. WebDriver sessions use periodic W3C
screenshots fed through the same bounded ffmpeg encoder, so Chrome, Edge,
Firefox, and real Safari all produce the same MP4 artifact contract. Recording
failure is named on the run and cannot silently become a successful proof.

Artifacts remain local by default. Authenticated routes support byte ranges so
mobile, web, desktop, TV, and spatial clients can stream instead of downloading
an entire clip. Optional durable sharing uses the existing owner-controlled
artifact store and records that fact explicitly.

## Persisted task contract

`Task.Verification` and `TaskInfo.Verification` contain the latest attempt:

```json
{
  "kind": "playwright-spec",
  "status": "running|passed|failed",
  "attempt": 2,
  "jobId": "studio-...",
  "project": "checkout",
  "feature": "mobile checkout",
  "total": 1,
  "passed": 1,
  "failed": 0,
  "durationMs": 1842,
  "checks": [
    { "name": "mobile checkout", "status": "pass" }
  ],
  "artifacts": [
    { "kind": "screenshot", "path": "...", "name": "..." }
  ],
  "startedAt": "...",
  "finishedAt": "...",
  "failureCode": "",
  "failureReason": ""
}
```

Only report-referenced artifact paths are retained. Existing artifact-serving
verbs remain the byte-access boundary; a client never receives arbitrary file
contents from the task DTO.

State transitions are monotonic within one attempt:

```text
absent -> running -> passed
                  -> failed
```

A retry increments `attempt` and replaces the latest result. Concurrent runs
for one task are rejected with `verification_in_progress`; they are never
coalesced because two browser runs could observe different application state.

## Runner tool contract

`yaver_verify_task` accepts:

- `feature`: optional exact `Feature` name; empty runs all discovered specs.
- `project`: optional report label.
- `profile`: optional named Playwright storage-state profile.
- `trace`: capture a Playwright trace; defaults true.
- `dev_command`: optional command that starts the task project's dev server.
- `wait_url`: readiness URL for that command.
- `timeout_sec`: 30–1800 seconds, default 600.

The project directory is never accepted from the runner request. It comes from
the authenticated task's effective work directory, preventing a compromised or
confused runner from using this tool as a browser-test oracle over another
project. The tool accepts no environment map or secret values. Authenticated
profiles are referenced by name and resolved by the existing profile boundary.

The tool returns `passed: true` only when a report exists, at least one feature
ran, and the report contains zero failures. Startup failure, missing specs,
dependency failure, timeout, an empty report, and failed features are distinct
named failures.

## Completion contract

Verification does not itself complete or promote a task. The runner still calls
`yaver_report_complete` after interpreting the evidence. This separation keeps
the following behaviours explicit:

- A passing browser spec may cover only part of a larger request.
- A failed check should return to the same runner turn for repair.
- Backend-only work may complete without browser verification.
- A user can explicitly request implementation without verification.

When a task has requested verification, `yaver_report_complete` must refuse a
review transition while the latest verification is `running` or `failed`. This
prevents the inventory-says-yes failure where the runner asks for verification,
sees it fail, and nevertheless marks the task verified.

## Event contract

Every transition emits:

```json
{
  "type": "task_verification",
  "taskId": "...",
  "verification": { "...latest persisted state..." }
}
```

The persisted task projection is authoritative for reconnects. SSE is only the
low-latency delivery path. Clients should replace their latest verification
object when this event arrives and refresh task detail after reconnect.

## Client contract

Clients render one compact result near task completion:

- running: `Verifying in browser… 12s`
- passed: `Verified · 3 checks passed`
- failed: the named failed feature or infrastructure cause, plus `Retry`

Expanded evidence may show feature results and request artifacts through the
existing scoped artifact verb. Clients must not expose raw local paths as
clickable links. The agent response and task event use the same vocabulary so
mobile and web cannot drift on pass/fail meaning.

### Cross-surface projection

All surfaces consume the same task verification and automation-run DTOs:

| Surface | Projection |
| --- | --- |
| Mobile/tablet | Inline verification card, poster, native MP4 player, feature results, retry/handoff |
| Web | Same evidence plus trace/report download and interactive co-browse |
| Electron desktop | Web projection through the local authenticated agent |
| tvOS/Android TV | Large poster/video playback, concise result, handoff QR/deep link |
| Xbox | Controller-first machine/task list, authoritative result, authenticated MP4 playback over the owner relay |
| PlayStation | Same controller-first result/playback contract; executable remains blocked on Sony SDK, title IDs, hardware, and certification |
| Steam/Steam Deck | Electron/web projection, controller navigation, authenticated MP4 playback; retail Deck closed loop remains a release gate |
| visionOS/AR/VR | Spatial video/result panel; browser control only when the surface supplies safe named input |
| Watch/Wear OS | Pass/fail, elapsed time, poster thumbnail when practical, `Open on phone`; no full browser controls |
| Car/CarPlay/Android Auto | Spoken one-sentence state and phone handoff; never video or browser controls while driving |
| Glass | Glanceable status/poster and voice handoff; full evidence opens on phone |
| CLI/MCP | Full structured JSON and authenticated artifact paths |

Full surfaces render video only when the artifact reports `status: ready` and
`mimeType: video/mp4`. Constrained surfaces never invent a second media format.
If the agent is unreachable, they keep the last known result but label the
artifact unavailable rather than showing an endless loading state.

No surface may claim `verified` by parsing runner prose, a commit, a video, or a
browser process being alive.

“All surfaces” does not erase platform truth. PlayStation has no public
executable in this repository because Sony's SDK is partner-gated. Its checked
product contract pins the same DTO, authenticated media, controller, privacy,
and certification requirements, while `claimsPlayStationSupport` stays false
until those external gates and a hardware closed loop are complete.

## Failure routing

| Failure | Code | Route |
| --- | --- | --- |
| No task project directory | `verification_no_project` | Attach/select a project and retry |
| Another attempt is active | `verification_in_progress` | Wait for the current attempt |
| No specs discovered | `verification_no_specs` | Add a `yaver-tests/*.test.yaml` spec |
| Browser dependencies missing | `verification_start_failed` | Existing `playwright_status` / `playwright_repair` verbs |
| Deadline exceeded | `verification_timeout` | Inspect job progress, narrow the feature, retry |
| Feature failed | `verification_failed` | Return feature error and artifacts to the runner |
| Report missing/empty | `verification_no_report` | Treat as agent/test-harness failure, never success |

## Security and privacy

- The task bearer and `YAVER_TASK_ID` bind the runner call to an existing task.
- The daemon chooses the working directory from task state.
- Verification artifacts stay on the user's machine and use existing scoped
  artifact readers.
- No browser profile contents, tokens, commands, console output, or local paths
  are synced to Convex by this feature.
- Headed profile creation and 2FA remain human-in-the-loop operations.
- The tool does not deploy, publish, submit, or mutate an external platform.

## Tests and negative controls

Required automated coverage:

1. The MCP tool is listed and dispatched.
2. A runner call without `YAVER_TASK_ID` fails.
3. The task work directory wins; request JSON cannot select another directory.
4. A passing report persists `passed` and emits a terminal event.
5. A failed report persists `failed` and returns the failing feature.
6. A missing report and a zero-feature report cannot become green.
7. A second concurrent request is rejected.
8. `yaver_report_complete` refuses an outstanding or failed requested check.
9. Task persistence/reload retains the latest verification.
10. Breaking a passing fixture causes the integration test to fail; restoring
    the fixture makes it pass again.
11. Configured Chrome, Firefox, and Safari targets dispatch to their declared
    engines; unsupported OS/engine pairs return a named capability failure.
12. WebKit results are never labelled Safari.
13. Recording a WebDriver session produces a valid, Range-streamable MP4.
14. Closing a session finalizes its run and attaches the clip to the task.
15. Every surface adapter maps the same `passed|failed|running` values and uses
    the agent-provided artifact URL; parity tests fail when one drifts.

## Follow-on release verification

Release runs should consume this same evidence type rather than invent another
status model. A later release record can require a passing task verification,
then append build, signature, upload, processing, and store-review evidence.
Final store submission remains an explicit human-confirmed mutation.
