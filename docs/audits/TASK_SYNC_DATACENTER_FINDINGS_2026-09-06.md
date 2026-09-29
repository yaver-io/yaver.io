# Task Sync and Datacenter Findings — 2026-09-06

## Live Ubuntu evidence

- A local delete-all removed 18 stale tasks.
- One tmux session containing three real Codex panes became three distinct
  Yaver Tasks. Discovery reported `gpt-5.6-sol` and the `high`, `medium`, and
  `low` reasoning levels.
- Exiting one pane changed exactly its corresponding Task to `stopped`.
- A real OpenCode session reported `deepseek/deepseek-flash`; reasoning was
  omitted because that runner did not provide it.
- A guarded disposable Task was observed as `running` in both the local ledger
  and production snapshot, then `stopped` in both, then absent from both after
  its exact delete. The active controller Task remained `running` throughout.
- A second browser-scoped lifecycle created two exact disposable Tasks,
  observed `running`, explicitly changed them to `completed`, and observed
  deletion in both stores after the RN-web bulk action.

## Defects found and fixed

- `ListTasks` omitted `model`, `reasoning`, `goal`, and `deviceName` even
  though task detail contained them. The list contract now preserves that
  authoritative metadata.
- Convex task-snapshot publishing had reached `failCount: 177` with HTTP 404.
  The agent incorrectly sent an opaque Yaver bearer to the `.site`
  `/api/mutation` path, which does not exist there; the `.cloud` mutation API
  also rejects that bearer as Convex function auth.
- The fix is a first-class authenticated `POST /task-snapshots` route. It
  validates the Yaver session, enforces owned-device writes, and stores one
  bounded prompt-free snapshot per device.
- RN-web restored an explicit device preference but never placed that device
  in its auto-connect ordering. A cold context could therefore connect to a
  different recent/primary device while the saved explicit pick only blocked
  later focus repair. The explicit pick now enters the ladder first, and the
  cold-reopen contract test asserts that it precedes the connection cache.

## Privacy and cost contract

The client payload contains only `deviceId`, `observedAt`, and at most 200
lifecycle entries. Each entry contains `taskId`, optional `yaverSessionId`,
`status`, optional closed-enum `hostKind`, and `updatedAt`. The stored document
also has the server-derived `userId` required for tenant ownership/indexing;
it is not accepted as client metadata. Convex receives no prompts, output,
source, paths, project, title, runner, model, or reasoning. The GET response's
device name/online/heartbeat fields are an owned-device read-time join and are
not stored in the Task snapshot document.

The local agent remains authoritative. Lifecycle changes publish
event-driven snapshots, with a two-hour freshness floor. An idle coordinator
does not write merely to renew its role.

The final live read-back contained eight entries. The active controller was
`running` in both the local ledger and its owning-device Convex snapshot. Every
entry was within the 200-row bound, carried only `taskId`, optional
`yaverSessionId`, `status`, optional `hostKind`, and `updatedAt`, and contained
none of the forbidden Task metadata. Every present `hostKind` belonged to the
code-defined `terminal_tmux | desktop_gui | runner_process` enum.

## Datacenter alignment

This work matches `docs/architecture/YAVER_DATACENTER.md` from `origin/main`:

- Task identity is device-scoped and can be composed by `FabricConnection`.
- Any surface may attach through an owned device.
- A worker continues locally without Convex; Convex is discovery and fallback,
  not execution.
- A thin Mac may remain the controller while Linux performs build, test, or
  browser work.
- Gateway retries must retain one task identity and must not duplicate the
  task.

## Cross-surface Datacenter verification roadmap

The Go agent owns the authoritative local Task lifecycle. Convex carries only
the bounded prompt-free roaming snapshot described above. Every surface
consumes the same Task identity and lifecycle contract; no client recreates a
parallel tmux-session or coding-agent-session model.

Verification is layered and ordered:

1. CLI/MCP and direct agent HTTP probes prove discovery, create, reattach,
   continue, reconcile, stop, and delete without a UI.
2. Browser automation is the first closed-loop gate because it is the lightest
   real surface available on an Ubuntu worker.
3. Android native surfaces follow on Ubuntu through Redroid or an emulator.
4. Apple-only native surfaces require an Xcode-capable macOS worker. Linux
   contract tests may validate their shared schema, but must never be reported
   as tvOS, watchOS, CarPlay, iOS, or visionOS UI proof.

Every matrix row must pass the same five assertions; a platform-specific test
is not green if it omits one:

1. **Presentation:** Codex, OpenCode, and other runner sessions appear as
   ordinary Tasks with concise local model/reasoning metadata where available.
   No surface gets an attach/adopt-session card, session inventory, or parallel
   session concept.
2. **Identity and privacy:** one device-scoped Task identity survives every
   surface handoff. Convex receives only the bounded prompt-free identity,
   status, host-kind, and timestamp snapshot; never presentation or private
   Task content.
3. **Lifecycle parity:** create, reattach, continue, stop, and exact delete use
   the shared Task verbs and do not duplicate or silently replace the Task.
4. **Reconciliation:** refresh, reconnect, scrolling/virtualization, and local
   runner exit converge to the authoritative local state and remove stale UI
   and Convex rows.
5. **Closed loop:** the named surface test must assert pixels or native named UI
   and then prove local/Convex convergence. A shared contract fixture may prove
   a native-only transport/schema gate, but it is not native UI proof.

| Surface | Required closed-loop test, including all five shared assertions | Status |
|---|---|---|
| Go agent + CLI/MCP foundation | Go HTTP/MCP integration fixture discovers distinct real runner panes, then creates, reattaches, continues, stops, and exactly deletes one disposable Task while polling the prompt-free Convex snapshot and reconciliation result. | Proven headlessly; permanent foundation gate |
| Web dashboard | Playwright drives the real dashboard in Chromium with named selectors and pixel assertions, performs the full lifecycle, refreshes, and proves no stale local/Convex row. | Pending dashboard-specific proof |
| RN-web phone | Playwright opens the real RN-web app in `devices["iPhone 15 Pro"]`, asserts the complete descriptor, Task-only presentation, HTTP/SSE lifecycle, refresh/scroll reconciliation, and scoped bulk delete. Resized desktop Chromium is forbidden. | Green on phone |
| RN-web tablet | Playwright opens the real RN-web app with one checked-in full named tablet descriptor, asserts all descriptor properties and the same Task lifecycle/reconciliation contract. Resized desktop Chromium is forbidden. | Pending tablet proof |
| Electron / desktop GUI | Playwright's Electron support or the maintained desktop harness launches the real packaged/development GUI, asserts Task pixels and the complete lifecycle against the same local agent and Convex identity. A dashboard tab is not a substitute. | Pending |
| Android phone/tablet native | UIAutomator/Maestro/instrumentation drives the real RN Android app in a named emulator or device, checks Task-only presentation and the complete lifecycle plus restart/reconnect reconciliation. | Pending native gate |
| iOS/iPadOS native | XCUITest drives the real RN app on a named simulator/device and checks the complete lifecycle, app-relaunch reconciliation, and local/Convex identity continuity. | Pending macOS gate |
| Android TV | UIAutomator drives the real TV app in a named Android TV emulator/device using remote-focus semantics; it verifies one concise Task, continue/stop/delete, refresh, and the shared snapshot. | Pending |
| tvOS | XCUITest drives the real tvOS app in a named simulator/device using focus-engine semantics and verifies the same concise Task lifecycle and reconciliation. | Pending macOS gate |
| Wear OS | UIAutomator drives the real Wear OS app in a named round/square emulator/device; one Task and one safe primary action remain visible while the full lifecycle is verified through the shared fixture. | Pending |
| watchOS | XCUITest drives the real watch app in a named simulator/device; one Task and one safe primary action remain visible while the full lifecycle is verified through the shared fixture. | Pending macOS gate |
| Android Auto | The Desktop Head Unit or a real compatible head unit drives the production car surface with native UI/voice assertions for create/continue/stop and a companion exact-delete fixture, followed by reconnect reconciliation. | Pending |
| CarPlay | XCUITest drives the real CarPlay simulator/head unit with native template and voice assertions for the same lifecycle and reconnect reconciliation. | Pending macOS gate |
| visionOS / glass / AR-VR | XCUITest on a named visionOS simulator/device, or the platform's real spatial UI harness, asserts quiet Task presentation and the complete lifecycle/reconciliation contract. Shared fixtures may gate schema/transport until hardware exists, but cannot be reported as spatial UI proof. | Pending platform gate |

The same tests also cover offline continuity (the local Task keeps running while
Convex is unavailable), the two-hour idle write floor, and a visible named
failure with one route to its fix instead of a spinner. Browser-capable rows use
real browser automation. Native-only rows use native simulators/devices/UI
harnesses or are explicitly limited to shared contract-fixture evidence; a
resized desktop Chromium window never counts as native proof.

### Transport parity

- Browser and RN-web use authenticated HTTP for requests and SSE for streams.
  They do not gain raw QUIC, weaker CORS, or tokens in URLs.
- Native clients may use LAN, relay, or QUIC where the platform supports it.
  A QUIC extension is added only after an observed capability gap.
- HTTP/SSE and native transport implementations share the same Task endpoints,
  status enums, reconnect cursors, stop/delete semantics, and auth boundary.
- Any separate platform transport implementation requires a parity test for
  reconnect, refresh, stop/delete, and stale-session reconciliation.

### Ubuntu execution profile

The Ubuntu worker performs source changes, Go/backend checks, RN-web serving,
Chromium/Playwright, and Android Redroid/emulator tests. Work is sequential:
one build, server, browser, or emulator phase at a time. Browser automation is
the first priority; Redroid starts only after the browser lane is green and
resource headroom has been measured. Native Apple verification is scheduled
onto an owned macOS capability when required rather than consuming the thin
client or being simulated dishonestly on Linux.

## Remaining gaps

- The Datacenter `WorkloadSpec`, `Job`, `Attempt`, lease/fencing, and
  source/artifact protocols are not implemented by this patch.
- Multi-node failover and idempotency remain open.
- Dashboard-specific, Electron, Android/native, tablet, and Apple-platform
  closed loops remain pending as shown above.
- Deployment was explicitly out of scope and was not run.

## Final RN-web browser result

- The real mobile app ran under Chromium with
  `devices["iPhone 15 Pro"]`: 393×659 CSS viewport, device scale factor 3,
  mobile user agent, and touch enabled. The dashboard was not substituted.
- The bearer was injected only into
  `localStorage["yaver.secure.yaver_auth_token"]`; it was never put in a URL,
  trace, screenshot artifact, or log. The ephemeral context also carried the
  non-secret, user-scoped owning-device preference so the normal cold-start
  auto-connect path was exercised.
- The controller card visibly rendered `gpt-5.6-sol` and its recorded reasoning
  level; an in-memory pixel capture of that exact label was non-empty. Runner
  sessions rendered simply as Tasks, with no attach/adopt/session inventory on
  the overview.
- Opening the controller issued the standard authenticated Task output SSE.
  The Completed view showed only two disposable fixtures; Select all and Delete
  removed exactly those two through single-Task owner deletes. The controller
  stayed running.
- A scroll plus fresh page mount did not resurrect deleted rows. Local Task
  state and the production prompt-free snapshot both converged to absence.
- `mobile-task-discovery-live.spec.ts` passed with one Playwright worker in
  52.9 seconds. No trace, video, or persistent screenshot was written.

## Validation result

- Focused Go metadata, snapshot, discovery, reconciliation, and runner-default
  tests passed with `GOMAXPROCS=2`, `-p 1`, and no test cache.
- The complete `desktop/agent` Go package passed sequentially after making the
  OpenCode catalog tests independent of the host CLI and making capability-gap
  route tests independent of live disk headroom. The remedy-route source guard
  now recognizes the typed `route` response field.
- The sticky-device cold-reopen contract, Task snapshot merge contract, and
  backend Task snapshot schema/route tests passed.
- The Playwright spec compiled/listed successfully before its one-worker live
  run. The real RN-web app and Expo server were stopped through the agent after
  the browser result; no browser or app server was left running.

These findings are foundational evidence for **DC-004**, **DC-600**,
**DC-1107**, and **DC-1110**. They do not complete those backlog items.
