# Yaver Autorun — runner-agnostic, timer-based closed-loop kicker/tester

Product goal: a first-class yaver feature that autonomously drives a coding runner
to develop + test a task on a timer, closed-loop, until done — and KEEPS WORKING
after the developer closes their laptop. Runner-agnostic. Task-MD-driven. Runs on
the developer's LOCAL machine OR a REMOTE machine.

## Why (from the field, 2026-07-16 ~03:35)
Tonight this was a bash stopgap on the Mac mini (`~/run-video-runner.sh` in a detached
`screen`: `while: git pull; opencode run --model glm-4.7 "$(cat task.md)"; sleep 300`).
It should be NATIVE: robust, resumable, observable, safe, runner-agnostic.

## Auto-mode prompt engineering (the runner's system framing every kick)
The loop must prime the runner to act autonomously: "You are in auto mode; do NOT ask
questions; read the codebase first; think like a senior developer; choose the proper
correct implementation with no shortcuts; keep developing so you never block; small
verified increments." (See `~/yaver-*-task.md` for the canonical wording.)

## Interface
`yaver autorun --task <task.md> [--runner auto|claude|codex|opencode|glm]
   [--interval 5m] [--max-iters N] [--machine <alias>] [--gate "<cmd>"] [--push] [--scope <glob>...]`
- `--task`: session/task-context MD (goal, scope, hard rules, auto-mode framing). THE driver.
- `--runner auto`: pick the first AUTHED runner (adapter layer). Runner-agnostic.
- `--machine`: run the loop ON a remote box via the agent (persist there); default local.
- `--gate`: build/test cmd that MUST pass before a commit is kept.
- `--push`: push only gate-verified commits.

## Loop (each iteration = one "kick")
1. `git pull --ff-only`.
2. Feed the task MD + CURRENT-STATE (recent git log, last-run summary from the progress
   MD, gate status) to the runner, headless.
3. Runner reads/edits/tests; run `--gate`; commit (+push if `--push`) ONLY on gate pass.
4. Append an iteration summary to `docs/handoff/<task>-progress.md` — the session-info
   context the NEXT kick reads. THIS md IS the continuity mechanism (focus: the task /
   session-info md file).
5. Sleep `--interval`. Stop on `--max-iters`, a `DONE` marker in the progress MD, or
   repeated no-op (converged).

## Runner-agnostic adapter (headless invocation)
- claude:   `claude -p "<ctx>" --dangerously-skip-permissions`
- codex:    `codex exec "<ctx>"`
- opencode: `opencode run --model <m> "<ctx>"`
- glm:      opencode + Z.AI (`zai-coding-plan/glm-*`) OR claude with z.ai base-url/token.
`--runner auto` detects an authed runner (reuse runner-auth status / creds probe) in a
configured preference order. REAL constraint seen tonight: claude OAuth expired, codex
absent, so opencode+GLM(z.ai) was the only working path — the auto-selector MUST fall
through gracefully like this.

## Local vs remote (the persistence layer — key product insight)
"Yaver should have a layer that keeps remote sessions/executions working after the
developer's PC closes." `yaver autorun --machine <box>` IS that layer: the loop lives ON
the remote box (agent-hosted PTY / tmux / screen persist), driven by the task MD,
independent of the laptop. The box's agent is already a launchd/systemd daemon; the loop
rides it. The phone or a new session attaches to observe/steer.

## Safety (non-negotiable)
- Gate-before-keep: never keep un-verified code (revert the working change if gate fails).
- Scope allowlist from the task MD; forbid rm -rf / git clean / reset --hard / rebase / deploy / tags / force-push.
- No-op-safe: if no clearly-safe step, append to the progress MD + sleep (don't churn tokens).
- Observable + killable: `yaver autorun status` tails the loop + progress MD; `yaver autorun stop`.

## Where to build it
`desktop/agent/` — a new `autorun*.go` (loop + runner adapters + gate + progress-MD writer),
a CLI/ops verb, reusing the existing runner-auth + `yaver code --attach` remote path.
Tonight's prototype (`~/run-video-runner.sh`, `~/yaver-video-task.md`) is the reference.
