# Yaver autorun progress

## 2026-07-16T01:29:00Z

The first local autorun increment (CLI loop, runner auto-selection, scope enforcement,
gate-owned signed commits, diagnostic rollback stashes, and unit tests) was added to
`main` in commit `bdbe63437` while this run was in progress. That commit was produced by
a concurrent runner and also contains unrelated n2n P4 work.

Audit findings and actions from this run:

- Corrected the runner prompt to use the design's normal senior-engineer framing instead
  of announcing unattended/auto mode.
- Made the autorun Codex adapter use
  `--dangerously-bypass-approvals-and-sandbox`; Claude, opencode, and GLM already use
  their permission-bypass arguments.
- Gate, runner, and scope failures now return the worktree to a clean state with
  `git stash push --include-untracked`, retaining the failed diff under a diagnostic
  stash name instead of deleting it.
- The committed runner-argument test incorrectly assumes the prompt is always the final
  CLI argument. A local correction checks for the prompt as one intact argument instead.

Verification is currently blocked by host disk exhaustion. The required command was run
with `/opt/homebrew/bin/go` (Go is installed but absent from this shell's PATH): the first
full suite compiled, then reported several pre-existing/concurrent failures plus the bad
autorun assertion above. After correcting that assertion, the targeted test/build retry
failed in the linker with `no space left on device`; the data volume had only 279 MiB
available. No cache or artifact was deleted because this run explicitly forbids deletion.
The local Go test correction must not be committed until both required Go gates pass.

Next safe increment after disk capacity is restored:

1. Run `cd desktop/agent && /opt/homebrew/bin/go build ./... && /opt/homebrew/bin/go test ./...`.
2. If green, commit the local `autorun_test.go` correction separately.
3. Add first-class autorun MCP/ops start, status, stop, and summary-video verbs; do not
   touch `remote_runtime*` or streaming files owned by the n2n/video runner.

## 2026-07-16T09:00:00Z

This run audited the existing loop and began the next MCP/ops increment. A correct MCP
surface needs an asynchronous session manager: calling the current synchronous
`executeAutorun` directly from an MCP handler would block the request and make status and
stop unusable. The attempted increment therefore added an in-process, cancellation-aware
session registry and owner-only `autorun_start`, `autorun_status`, and `autorun_stop` ops
verbs, with progress-tail reporting and tests. It was reverted before commit because the
mandatory full Go gate could not compile the package.

The blocker is concurrent, out-of-scope work currently present in the shared worktree:
untracked `desktop/agent/runner_keeper.go` and `runner_keeper_mcp.go`, plus overlapping
edits in `httpserver.go` and `mcp_tools.go`. The Go compiler reports `shortHash` declared
in both `runner_keeper.go` and existing `tunnel_cf_wizard.go`. Those files belong to the
concurrent runner and were left untouched. `github/main` is still at `f1ded4c1a`, so
fetching upstream does not provide a resolution yet.

Next safe increment after the concurrent runner finishes and the worktree builds:

1. Re-run the required baseline gate: `cd desktop/agent && /opt/homebrew/bin/go build ./... && /opt/homebrew/bin/go test ./...`.
2. Add the asynchronous autorun session registry in `autorun_ops.go`; expose owner-only
   `autorun_start`, `autorun_status`, and `autorun_stop` through the existing `ops` MCP
   gateway so remote `machine` routing is reused without touching transport code.
3. Require `task`, `gate`, and at least one scope in the start payload; return immediately
   with a stable session ID; make stop cancellation-aware; include the progress-MD tail in
   status. Add registration, safety-boundary, lifecycle, and unknown-session tests.
4. Do not expose specialist MCP tool names unless their dispatch can be wired within the
   allowed files; a listed-but-undispatchable tool is worse than ops-only discovery.

### Gate follow-up

The concurrent P7/P8 runner finished while this handoff was being written and committed
the attempted `autorun_ops.go` / test files along with its own work. Its duplicate
`shortHash` compile error was resolved. The mandatory gate was then rerun with the explicit
Homebrew Go PATH. `go build ./...` passed; `go test ./...` failed, so this ancestry was not
pushed as gate-verified.

Observed test failures included the known stale autorun assertion
`TestAutorunRunnerArgsAlwaysAutoApproves` (expects `--full-auto`; actual Codex autorun args
correctly use the stronger `--dangerously-bypass-approvals-and-sandbox`) plus unrelated
timeouts/surface failures including `TestInfoEndpoint`,
`TestAgentAuthConvexValidationPath`, and
`TestWebReload_DevStartFallbackSurfaceGating`. The full suite ran for roughly 9.5 minutes.
The next run must correct the autorun assertion within scope, then rerun both full gates;
it must still withhold the autorun commit if any test remains red.

## 2026-07-16T05:44:00Z

This run retried the smallest safe Go increment: correcting
`TestAutorunRunnerArgsAlwaysAutoApproves` to expect Codex autorun's actual
`--dangerously-bypass-approvals-and-sandbox` argument instead of the replaced general
runner `--full-auto` argument. The mandatory gate was run exactly as required with the
Homebrew Go installation added to `PATH`.

- `go build ./...` passed.
- `go test ./...` failed after 555.773 seconds.
- The corrected autorun test passed; it was not among the failures.
- Remaining failures were outside autorun scope: `TestInfoEndpoint` and
  `TestAgentAuthConvexValidationPath` timed out awaiting `/info` headers, and
  `TestWebReload_DevStartFallbackSurfaceGating` expected HTTP 400 but received the
  existing HTTP 404 `workDir not found` response.

Per the task's gate rule, the autorun test correction was reverted and no Go change was
kept. The unrelated existing `web/package-lock.json` modification remains untouched.
The next run should retry the same minimal assertion correction only after the full Go
suite's out-of-scope failures are fixed on `main`; then proceed with the MCP lifecycle
increment. In particular, audit the current in-process session context lifetime before
calling the MCP surface complete: an autorun loop must outlive the request context that
started it while remaining explicitly stoppable.

## 2026-07-16T06:06:00Z

This run implemented and focused-tested the smallest correct MCP lifecycle repair, then
reverted every Go change because the mandatory full gate remained red.

- `autorun_start` currently derives its asynchronous loop context directly from the MCP
  request context. The attempted repair used `context.WithoutCancel` plus a manager-owned
  cancel function, so the loop retained tracing values, outlived request completion, and
  remained explicitly stoppable. Its regression test passed.
- The stale Codex assertion was corrected to expect the actual, stronger
  `--dangerously-bypass-approvals-and-sandbox` argument. Its focused test passed.
- `go build ./...` passed.
- `go test ./...` failed after 496.142 seconds on the same three out-of-scope baseline
  failures: `TestInfoEndpoint` and `TestAgentAuthConvexValidationPath` timed out awaiting
  `/info` headers; `TestWebReload_DevStartFallbackSurfaceGating` expected HTTP 400 but got
  the existing HTTP 404 `workDir not found` response.

Per the gate rule, neither the lifecycle repair nor the assertion correction remains in
the worktree. The unrelated `web/package-lock.json` modification remains untouched. The
next safe run should repeat these two small repairs after `main` has a green full Go suite,
then proceed to the durable task queue (`autorun_enqueue`, `autorun_queue`, and
`autorun_dequeue`). Queue work must not be layered onto the known request-lifetime bug.

## 2026-07-16T03:50:42Z

This run re-read the design, project guide, handoff, recent history, and live autorun
implementation, then retried the smallest prerequisite repair before queue work.

- Changed MCP-started sessions to derive from `context.WithoutCancel(requestContext)` and
  a manager-owned cancel function. The regression test proved request cancellation no
  longer stopped the loop, request values remained available, and `autorun_stop` could
  still cancel it explicitly.
- Corrected the stale Codex permission assertion to require
  `--dangerously-bypass-approvals-and-sandbox`.
- Both focused autorun tests passed.
- The required `go build ./...` passed.
- The required `go test ./...` failed after 421.007 seconds on the same three
  out-of-scope baseline failures: `TestInfoEndpoint` and
  `TestAgentAuthConvexValidationPath` timed out awaiting `/info` headers, and
  `TestWebReload_DevStartFallbackSurfaceGating` expected HTTP 400 but received the
  existing HTTP 404 `workDir not found` response.

Per the gate rule, all Go changes were reverted. The unrelated existing
`web/package-lock.json` modification remains untouched. The next safe run remains the
lifecycle repair plus Codex assertion after the full Go baseline is green; only then add
the durable queue. No queue implementation should build on the request-lifetime bug.
