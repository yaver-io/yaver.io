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

## Completion "highlight reel" video — demonstrate what was achieved

When an autorun session completes (or on `--summary-video`), produce a short highlight
video — like a football-match highlights reel — that SHOWS/DEMONSTRATES what was achieved
in the PRODUCT end-to-end, not just a text summary. This is how a sleeping developer wakes
up and instantly sees "here's what got built and here it is working."

How:
- For each achieved change ("goal"), drive the actual product surface end-to-end and
  screen-record it, using a dedicated **TEST ACCOUNT** (never the user's real creds).
  Reuse yaver's existing capture stack — pick per surface:
  - web: browser/selenium + `browser_screenshot`/screen record
  - mobile: iOS simulator / redroid + `simulator_screenshot` / screen record
  - agent/CLI: `screenlog`/`record` (the local screen black box) or `vibe_preview` clips
  - TV: `appletv`/`capture`
- Capture ONE short clip per achieved "goal", each showing the feature actually working.
- Stitch with ffmpeg into a highlights reel: title card ("Autorun session · N commits ·
  M features"), per-clip captions ("Built X · verified by <gate>"), an outro summary, and
  (optional) a TTS voice-over narrating each highlight (reuse the voice/TTS stack).
- Output `docs/handoff/<task>-highlights.mp4` (+ thumbnail), linked from the progress MD.
  Compress (`ffmpeg -crf 32 -vf scale=720 -an`) or store the mp4 outside the repo and link
  it, to respect the web/repo size guards.

Test account + safety:
- The demo runs under a config-provided demo/test account (env or vault), isolated and safe
  to show. The reel must NEVER expose real user data, tokens, or secrets — respect the
  Convex privacy contract; scrub anything sensitive from frames.

Implementation sketch: `desktop/agent/autorun_summary.go` — (1) map each kept commit to a
demonstrable product surface, (2) run a scripted demo of it under the test account with
capture on, (3) assemble the reel via ffmpeg, (4) write the mp4 + update the progress MD.
Gate it behind `--summary-video` (default on for `--machine`/overnight runs).

## MCP exposure (drivable from the phone's Claude app / any MCP client)
All of autorun is exposed as MCP tools via the yaver `ops` grand-tool + `mcp_tools.go`
(follow the existing `ops`/first-class-tool patterns like circuit_plot, appletv_now_playing):
- `autorun_start {task, runner=auto, interval, machine, gate, push, summaryVideo}` → start a loop
- `autorun_status {machine}` → loop state + progress-MD tail + link/stream URL of latest highlights
- `autorun_stop {machine}`
- `autorun_summary_video {task, machine}` → (re)generate the highlight reel on demand
The video is returned as a first-class artifact / stream URL (below), so an MCP client
(phone Claude app, another agent) can kick a run, watch progress, and view the highlights.

## Watch the highlights from the MOBILE APP — streamed from the remote device
The developer must WATCH the highlights video IN the Yaver mobile app, streamed from
whatever remote box ran the autorun (Mac mini / cloud), not just find an mp4 on disk.
Reuse yaver's existing capture/stream + transport stack (the SAME layer as the iOS/redroid
video feature — one streaming layer, reused):
- The agent serves the highlights mp4 over its HTTP surface (range-request / HLS, e.g.
  `/autorun/<id>/highlights.mp4`), reachable via relay/direct — the transport the app
  already uses. `autorun_status` / the MCP tool returns that stream URL.
- OR live-stream the demo run as it's produced via the existing MJPEG/WebRTC capture
  (`/capture/stream`, `remote_runtime` WebRTC) so the app watches it being made.
- Mobile: an "Autorun" surface lists sessions + plays the highlights inline (native video
  player over the relay/direct URL) with the per-goal captions.

## Task QUEUE + MCP signalling (feed a running autorun more tasks, hands-free)
Autorun maintains a durable task QUEUE, not a single task. Via the yaver MCP you SIGNAL
new, additive tasks into a running loop's queue — from the phone or any MCP client, while
the developer is asleep or away, without touching a terminal:
- `autorun_enqueue {machine, task}`  → append a task (md text or file ref) to the queue
- `autorun_queue {machine}`          → list queued / active / done tasks (+ each one's
                                        progress-MD tail and highlights-video stream URL)
- `autorun_dequeue {machine, id}`    → cancel/remove a queued task
The loop DRAINS the queue in order: after finishing (or converging on) the current task, it
picks the next queued task and continues — a closed loop across a growing backlog. Each task
keeps its own progress MD + completion highlights video. The MCP is the signalling channel,
the queue is the durable intent, the remote loop is the executor. This is the core of
"becoming a real yaver": remote autorun that a developer keeps feeding by intent (MCP
signals), that runs + tests + demonstrates on its own on the remote box, and reports back
(progress MD + highlight reel streamed to the mobile app) — independent of the dev's laptop.

## Autonomy defaults (autorun / MCP) — ALWAYS skip-permissions
- Runners in autorun AND via the MCP ALWAYS run with permissions auto-approved
  (opencode/claude `--dangerously-skip-permissions`, codex full-auto). There is NO human
  approver in the loop by design. Safety comes from the GATE (build/test before keep), the
  scope allowlist, and the forbidden-command list — NOT from per-tool prompts.
  (Tonight's no-op bug: opencode ran WITHOUT skip-permissions, so its tools were never
  approved and it silently did nothing every kick.)
- The MCP remote QUEUE accepts free-text developer prompts — exactly like the messages a
  developer fires from the phone — via `autorun_enqueue {task}`. Each prompt becomes a task
  the loop drains. The phone/MCP is the developer's signalling channel to the remote box;
  the queue is durable intent; the loop executes with skip-permissions.

## Deep analysis — NEVER-BLOCK: keeping an autonomous runner from stalling on questions

Failure mode: an autonomous coding runner, mid-task, poses a question ("Which approach: A)
quick B) thorough?", "Should I also update X?") and stops, waiting for a human who isn't
there — wasting a kick, or picking a lazy default. Two complementary defenses, BOTH in yaver:

### 1. Prompt engineering (necessary, not sufficient)
- Do NOT tell the model "you are in autorun / unattended mode." Counter-intuitively this can
  make some models MORE cautious/hedgy (they surface caveats + ask permission). Frame it as a
  normal senior-engineer task instead. (Confirmed tonight: dropped the "you are in autonomous
  mode" header for exactly this reason.)
- Positive framing: "Do not ask questions. When a choice arises, choose the MOST CORRECT,
  most thorough option and implement it fully; treat any 'recommended' option as the answer;
  note rejected alternatives in the commit message." This biases toward the RIGHT option (the
  developer's stated rule: "option one = the true/correct, usually-more-effort one"), not
  merely the first.
- Why not sufficient: models still occasionally ask at genuine forks, and a CLI/tool itself
  can emit a y/n prompt that prompt text cannot intercept.

### 2. Hard-coded business logic in yaver (the robust layer)
yaver's autorun/runner layer detects an awaiting-input signal and auto-answers it so the
runner never blocks — runner-agnostic:
- Tool/permission prompts: never let them appear — always drive runners with the
  full-auto/bypass flags (codex `--dangerously-bypass-approvals-and-sandbox`, claude/opencode
  `--dangerously-skip-permissions`). This alone killed tonight's silent no-op (opencode had
  NO flag → its tools were never approved → it did nothing).
- Model-level questions in the output: classify — an interrogative directed at the user +
  an enumerated option set ("1) … 2) …", "A/B", "Which…?", "Should I…?", "recommended:").
- Answer deterministically: prefer the option explicitly marked "recommended"; else the
  most-thorough/correct one (heuristic: the most-complete option, the one implying the fuller
  implementation); else option 1. Inject the answer as the next turn ("Proceed with the
  recommended / most-thorough option; do not ask again; implement it fully."), continuing the
  SAME session so the runner resumes with no wasted kick.
- Escalate rarely: if a question is genuinely unsafe to auto-answer (destructive, ambiguous
  scope, a secret is needed), do NOT guess — record it in the progress MD as a "needs-human"
  item and continue other safe work. Auto-answer is for IMPLEMENTATION choices, never for
  irreversible or scope-expanding decisions.
- Ships as a small `autorun_answer` classifier+injector in the loop (sibling of `--runner
  auto`), so ANY runner is kept unblocked by the same yaver logic. Also exposed via MCP.

Net: prompt-engineer the model to not ask + prefer the correct option; back it with yaver
hard-coded detect-and-auto-answer so a stray question never stalls the remote loop.

## Multi-session management (one remote yaver hosts MANY autorun loops)
A single yaver agent on a remote box manages MULTIPLE concurrent autorun loops, each in its
own persistent session, tracked by a stable session/runner id — yaver is AWARE of its runner
sessions:
- Each `autorun_start` (and each queued task that spawns a loop) gets a RUNNER ID + a session
  id = a tmux/screen session name, registered with the agent (in-memory + a durable registry
  under ~/.yaver).
- `autorun_list` / `autorun_status {id}` enumerate the live loops (id, task, runner, current
  iteration, last commit, progress-MD tail, highlights stream URL) by reading tmux/screen
  session state. `autorun_stop {id}` / `autorun_attach {id}` target one specifically.
- tmux PREFERRED (attach / observe / scrollback / send-keys to inject auto-answers); screen
  is the fallback when tmux is absent (tonight's case — the mini has screen, not tmux;
  installing tmux unlocks attach/observe + the send-keys auto-answer path). The agent brokers
  attach/observe so the phone/MCP can watch or steer ANY session live.
- Result: the remote box is a multi-tenant autorun HOST — several tasks/queues developed in
  parallel, each isolated in its own tmux session, all managed + observable through one yaver
  agent + its MCP. Runner id + session id are the handles for everything (status, stop,
  attach, auto-answer injection, highlights).
