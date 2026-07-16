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
