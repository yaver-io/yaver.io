# Yaver autorun progress
## 2026-07-19T13:43:56Z

Iteration 1: SCOPE FAILED. Runner changes were removed from the worktree and preserved in a diagnostic git stash.

at least one --scope is required; autorun will not run without an explicit allowlist

## 2026-07-19T13:43:56Z

autorun: final autorun commit for DEPLOY-AND-HARDEN (scope violation)

This is the final autorun commit for task DEPLOY-AND-HARDEN. No further autorun commits will follow for this run.

Finish reason: scope violation
Iterations run: 1
Verified commits kept: 0
Runner: claude
Gate: cd desktop/agent && go build ./...
Machine at finish: disk 12.0 GB free, RAM 8.0 GB, 8 CPUs, load 2.69 (0.34/core)

The run ended on an error. Its code changes were not kept; they are preserved in a diagnostic git stash:
iteration 1 violated scope; changes were preserved in a diagnostic git stash: at least one --scope is required; autorun will not run without an explicit allowlist

