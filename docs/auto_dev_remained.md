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
- **Release-train TestFlight gating** — new `LoopShip.ReleaseTrain`
  block (`{n, paused, target}`) plus `LoopState.GreenRunSinceLastDeploy`
  counter. `releaseTrainAllowsDeploy` in `loop_exec.go` gates
  `phaseDeploy` on N consecutive green kicks AND
  `TestflightToday < MaxTestFlightPerDay` AND `!Paused`. Successful
  deploy bumps `TestflightToday` and zeroes the green counter;
  stuck / failed / needs_human resets it too.
- **Codex runner** — `phaseThink` now dispatches to `spawnCodex`
  when `think.runner: codex`. Shared `buildLoopPrompt` helper means
  Claude and Codex see byte-identical prompts, so `think.fallback:
  [claude, codex]` can cycle between them on rate-limit errors
  without the prompt changing shape.
- **Auto Test mode** — new `LoopMode = "auto-test"` in
  `loop_autotest.go`. `runAutoTestLoop` walks `yaver-tests/` via
  `testkit.DiscoverSpecs` + `testkit.Run`, wraps the first failing
  spec as a synthetic `HeuristicReport`, hands it to `phaseThink`,
  re-runs until green / stuck / max kicks. Spec field `test:
  {root, retry_flake, headful}`. Tested end-to-end in the agent
  suite.
- **Session-limits tracker** — new `loop_session.go`. Before every
  kick, `pickRunnerWithinLimits` checks the current runner's
  wall-clock usage inside its provider window (from
  `defaultProviderLimits`). Over the soft cap → swap in the first
  runner from `think.fallback` that still has headroom, or yield
  with `status=budget_hit`. After every kick,
  `recordKickUsage` charges the runner that ran. File is
  atomically written to `~/.yaver/loops/<name>/session_usage.json`
  so scheduler subprocesses persist their usage.
- **Aider runner** — `phaseThink` dispatches to `spawnAider` on
  `think.runner: aider`. Same JSON contract and shared
  `buildLoopPrompt` as Claude and Codex; uses `--yes-always
  --no-git --message` for autonomous one-shot runs so our
  phaseCommit stays the single commit path.
- **Ollama runner** — `spawnOllama` POSTs to the local ollama
  daemon via `OLLAMA_HOST` (default `http://127.0.0.1:11434`).
  Runner string is `ollama:<model>` (default `qwen2.5-coder`).
  Ollama can classify failures but can't edit files, so a
  successful analysis returns `status=needs_human` with the
  rationale — valuable in a fallback chain for "at least tell me
  what went wrong" when the primary is rate-limited.

## Gaps, ordered by value

## All tracked Auto Dev gaps are closed.

Every item from the original handoff is landed. The remaining
work is:

- Replacing the ollama runner's "classify-only" response with a
  real tool-using local runner (Cline, Roo, etc) once one ships
  a stable subprocess driver.
- Surfacing session-limits / release-train state on the mobile
  Auto Dev tab so devs can see "2h 10m of 5h Claude window used
  today" without opening the terminal.
- Whatever the dev asks for in the next iteration.

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
