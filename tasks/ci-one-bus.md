---
doer: codex
---

<!-- Single seat. claude is not authed on the mini; seats in front matter are
     binding, so naming an unauthed master fails the run at iteration 1. -->

# CI on one bus — every CI verb reachable from every surface, and stop passing jobs we didn't run

## Why

Yaver has **four** CI systems. They work. They are exposed on **opposite buses**,
so no single surface can drive CI, and no user can tell they exist.

| # | System | Config | Where it runs | Reachable from |
|---|---|---|---|---|
| 1 | `ci_selfhosted_runner.go` | none (registers the box) | GitHub/GitLab dispatches jobs **to** you | **`ops` only** (`ops_ci.go`, 9 verbs) |
| 2 | `ci_runner.go` | `.yaver/ci.yaml` | local, `docker run` per step | **HTTP only** (`/ci/run`,`/ci/list`,`/ci/config`) |
| 3 | `pipeline.go` (~2600 lines) | the repo's REAL `.github/workflows/*.yml` + `.gitlab-ci.yml` | local, native Go `act`-alike | **MCP only** (no ops verb) |
| 4 | `gh_run`/`glab_run`/`gitlab_pipelines`/`github_ci_status` | — | remote (the forge) | MCP |

Verified 2026-07-17, not from docs:
- `grep -rn 'Name: *"pipeline' desktop/agent/` → **zero hits**. System 3 has no ops verb.
- `grep -n 'Name: *"ci_' desktop/agent/ops_ci.go` → 9 verbs, **none** in `mcp_tools.go`.
- System 2's only non-HTTP caller is the deploy gate at `deploy_pipeline.go:133-150`.

**The user-visible consequence:** an agent on MCP can run your workflows locally
but cannot register a runner or read the savings. A web/mobile surface on `ops`
can register a runner but cannot run a workflow. The CLI can do neither
consistently. Four good systems, no product.

**This task does NOT collapse the three execution models into one.** That was
considered and explicitly rejected by the owner — each earns its place:

- **Model 1** (self-hosted runner) = perfect fidelity. It *is* the real GitHub
  runner, so every third-party action works. Cost: still depends on the forge.
- **Model 2** (`.yaver/ci.yaml`) = forge-agnostic, portable, no YAML dialect.
- **Model 3** (`pipeline.go`) = zero migration; runs the CI you already have.

Keep all three. **Unify the bus and the vocabulary, not the engines.**

## Part 1 — one bus: every CI verb on both `ops` and MCP

`ops_ci.go:3-6` already states the intended pattern:

> Self-registering ops verbs so the web/mobile/CLI drive it via callOps without
> any central-router edit (mirrors `ops_git.go`).

Finish it in both directions. **Do not duplicate logic** — the second bus is a
thin adapter over the same handler. One implementation, two doors.

**1a. Give System 3 (`pipeline.go`) ops verbs.** New `ops_pipeline.go`, mirroring
`ops_ci.go`'s self-registering `init()` shape:

```
ops pipeline_run      { machine?, file?, job?, dryRun? }
ops pipeline_list     { machine? }
ops pipeline_status   { machine? }
ops pipeline_stop     { machine? }
ops pipeline_hardware { machine? }
```

Owner-only (`AllowGuest: false`) — it executes arbitrary `run:` steps from a
workflow file. `pipeline_run` is long-running: set `Streaming: true` like
`ops_deploy.go:46-67` does, and return a streamId rather than blocking.

**`machine` comes free** — `ops.go:262-313` proxies any verb to any device. That
single field is the entire "run my CI on the Mac mini / the Hetzner box" story,
and it is why ops is the right second bus. Do not build a transport.

**1b. Give System 1 (`ops_ci.go`) MCP tools.** All 9 verbs
(`ci_runner_register/list/remove/status`, `ci_workflow_scaffold`,
`ci_workflow_targets`, `ci_jail_setup/status/teardown`) get entries in
`mcp_tools.go` + a dispatch arm in `httpserver.go`'s `callTool` switch. They stay
owner-only; the MCP door must not widen the guest scope the ops door enforces.

**1c. Give System 2 (`ci_runner.go`) both.** It is HTTP-only today — invisible to
agents and to `ops`. `ci_config_get/set`, `ci_run`, `ci_runs`. Name them so they
never read as System 1: System 1 is `ci_runner_*` (the runner), System 2 is
`ci_*` (the native pipeline). If that collision is too tight, rename System 2's
verbs `yci_*` — but **decide and write the decision into the verb descriptions**.
Two verb families that both sound like "run CI" and disagree is the exact bug
this task exists to kill.

**1d. `PipelineConfig` has no read/write verb at all.** `GetConfig`/`SetConfig`
(`pipeline.go:433/441`) exist and nothing calls them; users must hand-edit
`~/.yaver/pipeline-config.json`. Wire them into 1a.

## Part 2 — stop reporting green for work we did not do

**This is the load-bearing part. Do it even if Part 1 slips.**

`pipeline.go:1604-1606`:

```go
// Unknown action — skip with warning
step.Output = fmt.Sprintf("warning: action %q is not supported locally — step skipped", uses)
step.Status = "skipped"
return step
```

and `pipeline.go:1598-1600` for local composite actions:

```go
step.Output = fmt.Sprintf("composite action %s found but execution not fully implemented", uses)
step.Status = "skipped"
```

A `skipped` step does not fail its job. So a workflow that is 60% third-party
actions **reports passed while having run almost nothing**. A user who trusts
that ships a broken build believing local CI was green.

This violates the house law directly: **visible failure > silent retry**. A
silent skip is worse than a silent retry — a retry eventually surfaces; a false
green never does.

Handled today (verify before trusting this list — read `pipeline.go:1365-1608`):
`actions/checkout` (no-op, correct), `actions/setup-{node,go,python,java}`
(**only checks the tool exists — does NOT install the requested version**;
a matrix over node 18/20/22 silently tests whatever node is on PATH, three
times), `actions/cache`, `actions/{upload,download}-artifact`,
`docker/login-action`, `docker/build-push-action`, `peaceiris/actions-gh-pages`.
Everything else → skipped-and-passing.

**Required behavior:**

1. Add `PipelineStep.Unsupported bool` (or a `status: "unsupported"` distinct
   from `"skipped"` — `skipped` is a legitimate `if:` outcome and must keep
   meaning that). Never let the two share a code.
2. A job containing an unsupported step **does not pass**. Terminal status:
   `unsupported` (not `failed` — the user's CI isn't broken, *ours* is
   incomplete, and the distinction is what makes the report actionable).
   `PipelineResult.Status` gains the same value.
3. `PipelineResult` carries `UnsupportedActions []string` (deduped) so every
   surface can say **exactly** which actions forced the verdict.
4. **`pipeline_run` gains `allowUnsupported: bool` (default `false`).** When
   true, the old skip-and-pass behavior returns, `Status` stays `passed`, and
   the result is stamped `Degraded: true` + the same `UnsupportedActions` list.
   Opt-in, never inferred, and a degraded pass must be visibly degraded
   everywhere it is rendered.
5. `pipeline_list` (`pipeline.go:527`) should **pre-flight**: parse each workflow
   and report `unsupportedActions` per file *before* the user runs it. Knowing
   "this workflow is 4 actions we can't run" costs a parse, not 20 minutes.
6. The `setup-*` version gap is a real lie of the same family. **In scope: report
   it.** If `setup-node@v4` asks for `node-version: 20` and PATH node is 22,
   that step is `unsupported`, not passed. Actually installing the version
   (fnm/nvm/asdf) is **out of scope** — write it under "Needs verification" in
   the progress file, don't guess a version-manager integration.

**Do not** try to implement the third-party action ecosystem. The point is
honesty about the boundary, not moving it. Model 1 exists precisely for users
who need 100% fidelity, and Part 2's report is what should point them there:
when `pipeline_run` returns `unsupported`, the message names `ci_runner_register`
as the fix. That is the two systems finally cooperating instead of competing.

## Part 3 — one vocabulary across four engines

Each system invented its own result type. Four names for "a CI run":

- `PipelineResult` / `PipelineJob` / `PipelineStep` (`pipeline.go:243-305`)
- `CIRun` / `CIStepRun` (`ci_runner.go:35`)
- `CIRunResult` (`ci_selfhosted_runner.go`, the one the savings ledger books)
- `RunnerRun` (`runner.go` — **not wired to any bus; do not extend it**)

**Do NOT unify the structs.** That is a large, breaking refactor across four
subsystems, and it is not what makes the product work. Instead add one **read
model** the surfaces consume — new file `ci_view.go`:

```go
// CIRunView is the ONE shape every surface renders, whatever engine produced it.
type CIRunView struct {
	ID       string
	Engine   string // "self-hosted" | "native" | "local-workflow"
	Provider string // "github" | "gitlab" | "" (native)
	Name     string
	Status   string // running|passed|failed|unsupported|cancelled
	Degraded bool   // passed only because allowUnsupported was set
	Started  time.Time
	Duration time.Duration
	Steps    []CIStepView
	Unsupported []string
	SavedCents  int // 0 when not applicable — see tasks/ci-savings-everywhere.md
	Machine     string
}
```

Plus `func (r PipelineResult) View() CIRunView` and friends. Three small,
mechanical adapters. Additive — nothing existing changes shape, so nothing
existing breaks.

Then `ops ci_runs { machine?, engine? }` returns `[]CIRunView` merged across all
three local engines. **That single verb is what lets one UI show all of CI**, and
it is the whole deliverable of `tasks/ci-surfaces.md`. Ship it here.

## Prior art — read before inventing

- **`ops_ci.go`** — copy its `init()` + `registerOpsVerb(opsVerbSpec{...})` shape
  verbatim for `ops_pipeline.go`. It is the reference implementation and it
  already documents why (`:3-6`).
- **`ops.go:181` `dispatchOps`**, remote branch `:262-313` — `machine` routing is
  done. Read `:193-201`: guests are gated **before** routing and
  `machine=primary|auto` is forced to `local` for guests (`:227-230`). A new
  streaming verb must not regress that ordering; it is a fixed confused-deputy.
- **`ops_deploy.go:46-67`** — the model for a long-running streaming ops verb,
  including the shell-metachar + workDir hardening at `:88-101`.
- **`deploy_pipeline.go:133-150`** — System 2's existing deploy gate. Whatever you
  do to `ci_runner.go`'s signature, this caller must keep compiling.
- **`ci.go:16-22`** — `CIProvider`/`CIGitHub`/`CIGitLab`, the one type all four
  systems already share. Reuse it; do not add a fifth provider enum.
- **`pipeline.go:192-220` `PipelineConfig`** — persisted at
  `~/.yaver/pipeline-config.json`. Note `ReportToCloud` + `ReportStatus`
  (`pipeline.go:1966`) are implemented and **uncalled**. Don't wire them in this
  task; don't delete them either. Note it in the progress file.

### Two traps that will cost you an hour each

1. **`pipeline_cmd.go:10` `runPipeline`** (CLI `yaver pipeline`, `main.go:637`)
   has **nothing to do with `PipelineRunner`**. It's a build→deploy chain
   (`POST /builds` → wait → deploy). Pure name collision. Do not "fix" it, do not
   route it into your new verbs, and do not let `ops pipeline_run` and
   `yaver pipeline` mean different things without saying so in both descriptions.
2. **`runner.go`'s `Job`/`Pool`** reads exactly like the cross-machine build queue
   you'd want for this task. It is a Phase-1 skeleton: `Pool` is never used for
   dispatch (list-filter only), and `RunnerScheduleCron/Interval/Webhook`
   (`runner.go:82-85`) are **declared and never read anywhere**. Do not extend it.
   Do not delete it. `ops` + `machine` already gives you cross-machine dispatch.

## DO NOT BUILD. DO NOT RUN TESTS.

Owner's instruction: **do the coding, commit, push to main. That is all.**

No `go build`, no `go test`, no `tsc`, no gradle/xcodebuild — not even to check.
This box runs several autoruns at once and a Go build cache is what filled its
disk to 1.1 GB free before (`reclaimAutorunDisk` exists for that).

So **nothing verifies your edits.** Edit conservatively; if a change needs a
compiler to know whether it is right, write it under "Needs verification" in the
progress file instead of guessing.

**NEVER** run a bare `go test ./...` in `desktop/agent` — `TestAuthLogout` hits
the real `~/.yaver` and signs the owner out.

## Done means

- Every CI verb across all three local engines is callable from **both** `ops`
  and MCP, with one implementation behind two doors and no guest-scope widening.
- `ops pipeline_run --machine=<id>` runs this repo's real workflows on another
  box and returns a streamId.
- **An unsupported action can no longer produce a passing job.** `unsupported` is
  a distinct terminal status from `skipped` and from `failed`;
  `allowUnsupported: true` is the only way back to the old behavior and it stamps
  `Degraded`.
- `pipeline_list` says which actions a workflow uses that we can't run, before
  it's run.
- An `unsupported` result names `ci_runner_register` as the 100%-fidelity path.
- `ops ci_runs` returns one `[]CIRunView` merged across all three engines.
- The System 1 / System 2 verb-naming decision is written down in the verb
  descriptions themselves, not just in this file.
