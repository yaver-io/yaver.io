# Auto Dev — what's left (session handoff)

Pick-up notes for the next session. This is a focused checklist, not
a full design doc — the design lives in
`docs/roadmap_ci_solo_developer_lower_costs.md` M8.

Last session ended with worktree isolation landed (see "Done since
last handoff" below).

## Current state (what's already wired)

- **`yaver loop` CLI** (`desktop/agent/loop_cmd.go`, `desktop/agent/loop_exec.go`)
  - `add / list / run / stop / pause / resume / status / remove`
  - `prompt set / show / clear / pick` for Auto Dev inline prompts
  - `ideas show / kick`
  - Modes: `auto-fix`, `fix`, `develop` (multi-kick), `ideas`
- **Phase execution** (`loop_exec.go`):
  - Phase 1 HTTP readiness probe (only when playtest enabled)
  - Phase 2 chromedp headless Chrome heuristic scan (blank screen,
    console errors, `undefined` in UI, 11 Turkish diacritic probes)
  - Phase 3 `claude --print --permission-mode acceptEdits --add-dir`
    subprocess with prompt + heuristic report via stdin
  - Phase 4 green gate (typecheck via `node_modules/.bin/tsc`)
  - Phase 5 git add/commit/push with spec `commit_prefix`
- **Multi-kick develop loop** (`runDevelopLoop`): threads `next_step`
  forward as nudge, terminates on done / stuck / needs_human /
  budget_hit / stopped / max_kicks_per_run (default 10 per run).
- **Budget enforcement**: `LoopState.CommitsToday/PatchesToday/
  TestflightToday/BudgetDayKey`, rolled on UTC day change, checked
  before every kick. No AI spawn when budget is exhausted.
- **Inline prompts**: `LoopState.PromptInline` runtime override +
  `LoopSpec.Think.PromptInline` spec field; precedence is
  runtime > spec-inline > file; `wrapInlinePrompt` injects the JSON
  contract for terse mobile-written prompts.
- **Ideas mode**: self-contained pipeline (not a vibing wrapper).
  `gatherIdeasContext` reads git log + README + TODO.md +
  `.yaver/product.md`; `buildIdeasPrompt` renders the strict JSON
  contract; claude spawn writes `~/.yaver/loops/<name>/ideas.json`.
- **Prompt pick**: `yaver loop prompt pick <dev-loop> <idea-id>
  [--ideas-from <src>] [--run]` reads source loop's ideas.json,
  finds idea by ID, stashes its `.prompt` as the target loop's
  inline prompt.
- **Scheduler integration**: `loop add` POSTs a `ScheduledTask` to
  the running daemon's `/schedules` endpoint so loops auto-tick.
  `loop stop / remove` DELETE the schedule.
- **Safety rails** (runtime, not just docs):
  - Per-loop worktree isolation — kicks run inside
    `~/.yaver/loops/<name>/worktree` (detached HEAD off
    `ship.branch`), never touching the dev's main tree
  - STOP kill-file watchdog cancels iteration context
  - Green-gate failure triggers `git reset --hard preSHA &&
    git clean -fd` inside the worktree
  - `schedule.timeout` honored as context deadline

## Done since last handoff

- **Worktree isolation** — `runSingleKick` and `runIdeasKick` now run
  inside `~/.yaver/loops/<name>/worktree`, created with `--detach` off
  `ship.branch`. `ensureWorktree` refreshes the worktree to the
  branch tip before every kick; `removeWorktree` (called from
  `loop remove`) prunes it. `phaseCommit` pushes via `HEAD:<branch>`
  so the detached head still lands on `main`. The dirty-tree bail is
  gone — Auto Dev no longer refuses to run against active repos.
  Dead code `deriveWorkDir` / `gitIsDirty` removed.
- **Concurrency guard for loops.json** — `loadLoops` / `saveLoops`
  serialize through `loopsFileMu`; `saveLoops` writes via
  `loops.json.tmp` + `os.Rename` so readers never see a
  half-written file. New `withLoops(fn)` helper wraps
  read-modify-write sequences and is used by the new HTTP handlers.
- **Mobile `/autodev/*` HTTP endpoints** — new `autodev_http.go`
  exposes `GET /autodev/loops`, `POST /autodev/loops/<name>/run`,
  `/stop`, `GET /ideas`, `POST /prompt`, `POST /prompt/pick`.
  Shared helper `kickLoopOnce` runs one iteration without CLI side
  effects; `pickIdeaPrompt` is the lookup path both the CLI and
  the HTTP handler use. `quicClient.autodev*` methods in
  `mobile/src/lib/quic.ts` + `mobile/app/(tabs)/autodev.tsx` now
  load real data (loops, prompts mirrored from inline prompts,
  ideas from the first ideas-mode loop).

## Gaps, ordered by value

### 1. Auto Test mode
User explicitly asked for "auto test things". Today there's no
`auto-test` mode. Likely shape: a loop mode that runs the existing
yaver-test-sdk specs (`yaver-tests/*.test.yaml`) and, on failure,
asks claude to fix the failing spec or the code under test and
re-runs. The existing `testkit/autonomous.go` has scaffolding
hooks (`FixRequest`, `FixResult`, `FixHandler`) that were never
wired — this is the right place to bolt Auto Dev onto.

Sketch:
- New `LoopMode = "auto-test"` in `loop_cmd.go`
- New `runAutoTestLoop(ctx, l, saveState)` in `loop_exec.go` that:
  1. Runs `yaver test <spec>` via the existing test runner
  2. On red, builds a `FixRequest` and passes it through a claude
     subprocess (same pattern as `runSingleKick` phase 3)
  3. Re-runs the spec, loops until green or stuck budget / max kicks
- Wire the `testkit.FixHandler` seam so the autonomous loop is the
  implementation the test runner calls on failure
- New `.loop.yaml` spec: `mode: auto-test`, `test.specs: [...]`,
  `test.retry_flake: 2`

### 2. Session-limits tracker runtime
`think.respect_session_limits` field is parsed but nothing enforces
it. Claude Code's 5h rolling window shared with interactive use
needs to be tracked so the loop yields during active hours.

Sketch:
- Per-provider in-memory counter keyed by provider name
- Tick counter on each kick: record tokens spent (parse claude's
  stdout for its own cost reporting, or estimate from prompt+response
  length)
- Soft cap: default 80% of `ProviderLimits.SessionWindow` tokens
- Before each kick in `runDevelopLoop`, check
  `providerUsage[runner] > soft_cap * session_budget` — if yes,
  either fall back to the next runner in `think.fallback` or
  terminate with `budget_hit`
- Persist the counter to `~/.yaver/loops/<name>/session_usage.json`

### 3. Release-train TestFlight gating
`Budget.MaxTestFlightPerDay` is parsed and hard-capped at 10, but
nothing actually checks it before running a deploy. The doc's
"release train" (deploy to TF only when N consecutive green
iterations have passed + daily budget not exhausted) is unwired.

Sketch:
- New `LoopState.GreenRunSinceLastDeploy int`
- In `phaseDeploy`, if the deploy command contains `testflight` or
  `altool` or matches `ship.deploy_target == "testflight"`:
  - Check `GreenRunSinceLastDeploy >= spec.Ship.ReleaseTrainN`
    (default 3)
  - Check `LoopState.TestflightToday < Budget.MaxTestFlightPerDay`
  - Check `LoopState.Spec.Ship.ReleaseTrainPaused` flag
  - If all pass, run the deploy, bump `TestflightToday`, reset
    `GreenRunSinceLastDeploy`
  - Else, skip deploy and log the reason
- Expose a `ship.release_train: {N: 3, paused: false, target: "testflight"}`
  block in the spec

### 4. codex / aider / ollama runner support
Only `claude-code` is wired in `phaseThink`. Stubs return a clear
error (not a silent fake). Adding codex / aider is ~50 lines each:
new `spawnCodex(...)`, same JSON contract parsing. Ollama is
slightly bigger because it needs a local HTTP client to the ollama
daemon instead of a subprocess.

This unblocks fallback chain execution — once multiple runners
exist, `think.fallback` can actually cycle between them on
rate-limit failures.

## Quick verification commands

```bash
# Build + deploy the local binary
cd ~/Workspace/yaver.io/desktop/agent && go build -o /tmp/yaver-test . && \
  cp /tmp/yaver-test /opt/homebrew/bin/yaver

# Smoke test the Auto Dev loop end-to-end
cd /tmp/loop-e2e  # scratch repo from the previous session
yaver loop list
yaver loop prompt show e2e-autofix
yaver loop ideas show e2e-ideas | head -30
yaver loop run e2e-autofix      # develop mode, kicks until done
yaver loop run e2e-ideas        # ideas mode, writes ideas.json
yaver loop prompt pick e2e-autofix <idea-id> --ideas-from e2e-ideas --run
```

## Known quirks

- **macOS `/opt/homebrew/bin/yaver` hangs** when the daemon is doing
  startup probes. Use `/tmp/yaver-test` directly (same binary) for
  fast iteration; only `cp` to the install path after a green run.
- **Parallel Claude session** was committing to `desktop/agent/exec.go`
  and `desktop/agent/testkit/*.go` during the session. Build errors
  in `testkit/` may be pre-existing from that work — try a clean
  `go build .` first; if it fails on testkit files that loop_exec.go
  does not touch, rebase / pull first.
- **Scheduler registration is best-effort** — if the daemon is
  offline, `loop add` prints a warning but the spec is still
  registered locally. Iterations run via manual `yaver loop run`
  until the daemon comes back.
- **Worktree first run** does a full `git worktree add` which can
  take a few seconds on large repos. Subsequent kicks just reset
  + fetch, so they're fast. The worktree lives at
  `~/.yaver/loops/<name>/worktree` and is torn down by
  `yaver loop remove`.

## Relevant commits (github/main)

- `bbd5ecaf` Auto Dev: purpose-built ideas generator + prompt pick CLI
- `61edf434` Auto Develop: multi-kick develop loop, budget, inline prompts, ideas mode
- `0b5d9659` Wire loop phase execution end-to-end (auto-fix + develop on web)
- `ea4fbd6d` M8: autonomous test → fix → deploy loops (doc + agent scaffold + mobile tab)

## Files to know

- `desktop/agent/loop_cmd.go` — CLI + state + subcommand dispatch
- `desktop/agent/loop_exec.go` — phase execution, multi-kick, ideas
- `desktop/agent/vibing.go` — separate interactive widget, leave alone
- `desktop/agent/autopilot.go` — separate batch todo runner, leave alone
- `desktop/agent/testkit/autonomous.go` — scaffolding hooks for #3
- `mobile/app/(tabs)/autodev.tsx` — mobile shell, needs #2 to come alive
- `docs/roadmap_ci_solo_developer_lower_costs.md` — M8 design reference
