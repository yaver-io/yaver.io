---
doer: codex
---

<!-- Single seat. claude is not authed on the mini, and seats in front matter
     are binding — naming an unauthed master fails the run at iteration 1
     rather than falling back. codex runs this solo. -->

# The four that scope killed — one run, one scope wide enough to hold them

## Read this first: why these four are here

On 2026-07-17 these four tasks ran as four separate autoruns on the mini. All
four died the same way, on iteration 1, with the same finish reason:

```
Finish reason: scope violation
Iterations run: 1
Verified commits kept: 0
```

**None of them failed at coding.** Each one wrote its changes, and each one had
those changes stashed and thrown away because it touched a file its `--scope`
did not list. The runner was right and the scope was wrong. Verbatim from the
four progress files:

| Task | Files it was killed for touching |
|---|---|
| `autorun-digest-query` | `autorun_cmd.go`, `autorun_ops.go`, `httpserver.go`, `mcp_tools.go`, `mobile/app/autoruns.tsx`, `web/.../AutorunsView.tsx`, `web/lib/agentStatus.ts` |
| `glm-remove-runner` | 12 × `desktop/agent/*.go`, `backend/convex/schema.ts`, 8 × `mobile/**`, 3 × `web/**` |
| `ci-one-bus` | `ci_runner.go`, `ci_view.go`, `ops_ci_native.go`, `ops_pipeline.go`, `pipeline.go`, `httpserver.go`, `mcp_tools.go` |
| `ci-review-gate` | `code_review_gate.go`, `deploy_pipeline.go`, `project_config_surface.go`, `web/.../ProjectDetailView.tsx`, `web/lib/agent-client.ts` |

Every one of those files is a file the task genuinely had to edit. Three of the
four are explicitly cross-surface tasks — and this repo's own rule is that a fix
is not done until it exists on every surface (CLAUDE.md, "Cross-surface parity").
A scope that stops at `desktop/agent/**` cannot express that. So the scope for
this run is the union of what all four actually reached for, and it is passed on
the command line, not here.

**Do not narrow the scope to "be safe".** A scope violation here does not
protect anything; it deletes finished work. If you need a file that is genuinely
outside the scope, stop and say so in the progress file instead of working
around it.

## The work: four objectives, in this order

The full brief for each lives in its own task file, already on main. **Read the
one you are working on before you touch anything** — each contains verified
grep-backed findings, and at least two contain a "false friends" table that will
save you an hour of chasing the wrong symbol.

1. **`tasks/glm-remove-runner.md`** — retire `glm` as a runner. It sets
   `Command: "claude"`, which drives the subscription-OAuth CLI with a z.ai API
   key, and fails with a fake "OAuth session expired". GLM stays available via
   opencode's `zai-coding-plan` provider. End state:
   `supportedRunnerIDs = {claude, codex, opencode}`. **Smallest and fully
   settled — do it first.**

2. **`tasks/autorun-digest-query.md`** — add `autorun_digest`. `autorun_status`
   returned 56 KB / 368 lines on the mini and blew the MCP output limit, so the
   simplest question ("is the run finished?") had no readable answer. The
   inventory is ~40 bytes per session. Must be a projection of the existing
   session list, not a second source of truth.

3. **`tasks/ci-one-bus.md`** — four CI systems on opposite buses (`ops`-only,
   HTTP-only, MCP-only), so no surface can drive CI. Put them on one bus. Also
   stop reporting skipped jobs as passed — an unsupported `uses:` currently
   reads as green.

4. **`tasks/ci-review-gate.md`** — the optional, **off-by-default** code-review
   gate, plus the per-project settings surface it needs. Net-new: there is
   nothing to flip off yet. `app_review_check`, `managed_quality_run` and
   `talos_quality_run` are false friends — the latter two compute a verdict that
   nothing consumes. **Do this after #3**; it needs the CI surface to exist.

## How to pick this iteration's work

1. Read `docs/handoff/merged-remaining-progress.md` if it exists — it is the
   record of what previous iterations finished.
2. Take the **lowest-numbered objective that is not yet complete**. Finish it
   before starting the next one. Do not interleave.
3. Commit that objective's work on its own, with a message naming which
   objective it is.
4. Append what you finished, and what you deliberately left, to the handoff file.

## Done means

All four objectives complete. Say `DONE` only when every one of them is, and
only when the gate passes on the current tree.

If you find an objective is already complete on main (some of this work may have
landed since 2026-07-17 — check before rewriting it), record that in the handoff
file and move to the next one. Re-doing landed work is worse than skipping it.

## The gate

`scripts/gate-merged-remaining.sh` — `gofmt` over `desktop/agent`, then
`go build ./...` there. It is deliberately outside the scope of this run: you
cannot edit the thing that judges you. It does **not** typecheck the TypeScript
surfaces, so when you touch `web/**` or `mobile/**`, read what you wrote — the
gate will not catch it for you.
