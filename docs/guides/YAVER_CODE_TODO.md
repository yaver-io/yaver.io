# YAVER Code TODO

This document is the implementation backlog and architecture plan for `yaver code`.

It is intentionally opinionated. The goal is not to preserve a minimal CLI forever. The goal is to make `yaver code` the canonical terminal-native coding surface for Yaver:

- local coding
- remote coding on attached machines
- runner auth
- repo/workdir selection
- dev/build/reload/deploy loops
- session continuation
- multi-runner delegation
- optional auto-orchestration

This file is a planning document, not source of truth. The code in `desktop/agent/*.go` remains authoritative.

## Product Goal

`yaver code` should feel like a native coding terminal, not a thin wrapper over `/tasks`.

That means:

- the user can stay inside one terminal session for long periods
- the terminal always shows where they are, which runner is active, and what repo is targeted
- local commands are deterministic and slash-first
- remote headless machines are first-class, including runner auth
- existing sessions can be continued, switched, forked, and delegated without losing the parent thread
- multiple underlying runners can cooperate while Yaver remains the canonical session owner

The target UX is closer to Codex/Claude Code/Cursor terminal ergonomics than to a generic chat REPL.

## Current State

The current codebase already has the beginnings of the right shape:

- deterministic control-plane commands in `desktop/agent/code_control.go`
- interactive terminal entry points in `desktop/agent/attach.go`, `desktop/agent/client.go`, and `desktop/agent/terminal_ui.go`
- remote attach / device selection in `desktop/agent/code_cmd.go`
- task/session ownership in `desktop/agent/tasks.go`
- remote browser auth flows for Claude/Codex in `desktop/agent/runner_auth_browser_http.go`
- open-code config APIs already exposed through `/runner/opencode/config`
- graph / mesh execution primitives in `desktop/agent/agent_mode.go`

Recent work in the tree already pushes toward this plan:

- slash palette and slash command parsing
- prompt footer showing active runner/model/workdir
- pasted file/image/video path detection
- remote headless browser-auth control from terminal
- session listing and continuation
- continuation with runner/model override
- compact-context structs for future fork/delegate flows
- orchestration mode placeholder: `manual|auto`

## Non-Goals

These should not happen:

- turning every final user answer into compressed caveman-speak by default
- replacing underlying runner-native output with a generic Yaver paraphrase
- hiding task boundaries so aggressively that debugging becomes impossible
- inventing a second session system separate from `Task` ownership
- making auto-orchestration mandatory
- allowing compression to mutate code, diffs, stack traces, file paths, versions, env vars, or auth tokens

## Core Model

The core design should be:

1. Yaver owns the canonical terminal session.
2. Underlying runners are execution engines, not the source of truth.
3. Every visible session maps to a parent `Task`.
4. Child work is represented as child tasks or graph nodes, not opaque subprocesses.
5. Delegation passes compacted context, not full transcript replay.
6. The terminal shows a single coherent conversation with overlays for child activity.

This is the key distinction from “just let the user run Codex and separately run OpenCode.”

## Mental Model

There are three layers:

1. Control Plane
Commands like attach, auth, set/get, repo, sessions, continue, fork, deploy.

2. Session Plane
Parent task ownership, child task relationships, compact context, continuation, output streaming.

3. Orchestration Plane
Optional policy deciding whether to keep work in the active runner, continue with a different runner, or fork a subtask into another runner.

## Terminal UX

### Prompt Footer

The terminal should always show a compact status line before the prompt.

Examples:

- `gpt-5.4 default · ~/Workspace/yaver.io`
- `claude default · ~/Workspace/sfmg`
- `openai/gpt-5.4 · ~/Workspace/mobile-app`

Later expansions:

- attach target: `gpt-5.4 default · ~/Workspace/yaver.io · @mac-mini`
- orchestration mode: `gpt-5.4 default · ~/Workspace/yaver.io · auto`
- child overlay active: `gpt-5.4 default · ~/Workspace/yaver.io · fork:opencode`

### Local Commands

All major local commands should have canonical slash forms:

- `/attach pc`
- `/detach pc`
- `/auth claude`
- `/auth codex`
- `/sessions`
- `/continue`
- `/fork`
- `/set agent`
- `/set model`
- `/set orchestration`
- `/repo list`
- `/repo clone`
- `/dev reload`
- `/deploy frontend`

Plain aliases may continue to exist, but docs and help should bias toward slash forms.

### Input vs Output Rendering

The terminal must clearly separate:

- user-entered text
- local control-plane output
- parent-runner output
- child-runner overlay output
- final result lines

Recommended rendering model:

- user input: cyan `⟩ ...`
- local control plane: dim neutral status lines
- active parent runner: raw/native
- child runner overlay: prefixed tag like `[fork opencode task-1234]`
- final summaries: short, plain, direct

Yaver should preserve native ANSI/color/diff output from runners whenever possible.

## Session Model

### Parent Task

Every `yaver code` session should map to a parent `Task`.

The parent task stores:

- task ID
- session ID from underlying runner when applicable
- runner/model
- workdir/repo context
- conversation turns
- current output
- recent result

### Continue

Continue should remain the canonical “same session, same task” path.

Command:

`yaver code continue <task-id> [--agent <runner>] [--model <model>] <message>`

Semantics:

- same `Task` ID
- same conversation turns
- same session if runner permits and runner does not change
- session reset if runner changes
- model override applied for the next run

### Fork

Fork should be “spawn child work from parent context.”

Command:

`yaver code fork <task-id> --agent <runner> [--model <model>] <message>`

Semantics:

- parent task remains canonical
- child task gets compacted context from parent
- child task has its own runner/model/session
- child work streams into terminal as overlay
- child completion may optionally:
  - only display result
  - append summarized result into parent turns
  - queue a parent continuation

The first version should keep this manual and explicit.

## Compact Context

The compact-context layer is critical. Without it, multi-runner orchestration becomes too expensive and too messy.

Compacted context must include:

- parent task ID
- parent session ID if any
- current runner/model
- workdir
- original task title
- current user intent
- last few turns
- latest useful result
- relevant attachment paths

It must exclude:

- long raw logs
- entire transcript history
- repetitive prior summaries
- huge code dumps already on disk

Rules:

- preserve technical tokens exactly
- preserve file paths exactly
- preserve error strings exactly if they matter
- preserve code blocks verbatim if included
- otherwise compress aggressively

## Remote / Headless Auth

This is critical for headless boxes.

Required flow:

1. User attaches to remote box with `yaver code attach pc ...`
2. User runs `/auth claude` or `/auth codex`
3. Yaver starts remote `/runner-auth/browser/start`
4. Terminal prints:
   - runner
   - session ID
   - URL
   - code if present
5. For Claude:
   - user opens URL
   - completes sign-in elsewhere
   - pastes returned token/code into terminal
   - Yaver sends it via `/runner-auth/browser/submit-code`
6. Yaver polls status until ready/failed/cancelled

This already exists in partial form and should become a polished first-class flow.

## OpenCode Provider Surface

OpenCode needs a first-class config/auth surface in `yaver code`, not only in `runner-auth`.

Need:

- `yaver code auth opencode`
- `yaver code set provider openai`
- `yaver code set provider anthropic`
- `yaver code set provider glm`
- `yaver code set provider zai`
- `yaver code set plan-model ...`
- `yaver code set build-model ...`
- `yaver code get provider`
- `yaver code get plan-model`
- `yaver code get build-model`

For API-key-backed providers, Yaver can store them through the vault-backed runner auth layer.

For auto mode, these provider capabilities should be visible to the policy engine.

## Child Overlay Streaming

Forked child tasks should stream back into the same terminal.

Minimal first implementation:

- parent terminal stays active
- child emits overlay-prefixed chunks
- overlay lines include runner and child task ID
- child completion emits a short summary

Later:

- toggle overlay visibility
- jump into child session
- promote child task to active view
- merge child summary into parent turns

## Graph / Mesh Integration

There are two multi-runner forms:

1. Local session-level fork
2. Distributed graph/mesh execution

These should not be separate product worlds.

The session-level fork model can later be implemented as a tiny graph:

- parent node: active terminal session
- child node: delegated opencode task
- optional review node: claude/codex verification

This means long-term `fork` should reuse graph machinery where possible.

## Suggested Commands

### Stable now / near-term

- `yaver code sessions`
- `yaver code continue <task-id> [--agent ...] [--model ...] <message>`
- `yaver code fork <task-id> --agent <runner> [--model ...] <message>`
- `yaver code auth claude|codex`
- `yaver code auth status <session-id>`
- `yaver code auth submit <session-id> <code>`
- `yaver code auth cancel <session-id>`
- `yaver code set orchestration manual|auto`
- `yaver code get orchestration`

### Medium-term

- `yaver code fork <task-id> --agent opencode --mode overlay "fix the navbar spacing"`
- `yaver code child list`
- `yaver code child show <task-id>`
- `yaver code child adopt <task-id>`
- `yaver code child merge <task-id>`
- `yaver code auth opencode`

### Longer-term

- `yaver code auto on`
- `yaver code auto policy`
- `yaver code auto budget`
- `yaver code auto simulate "implement checkout page"`

## Implementation Phases

### Phase 1: Finish Session UX

- polish bottom prompt/footer
- keep active runner/model/workdir always visible
- make session IDs more visible in interactive mode
- improve `sessions` output ordering and formatting

### Phase 2: First-Class Fork

- implement interactive `/fork`
- create real child tasks from compacted parent context
- stream child overlays into active terminal
- support explicit runner/model selection

### Phase 3: OpenCode Provider Surface

- expose provider settings through `yaver code`
- wire GLM/ZAI/OpenAI/Anthropic keys through vault-backed control paths
- surface readiness in `get agent` / `status`

### Phase 4 & 5 — DROPPED 2026-04-28

The original plan had two more phases:

- **Phase 4: Graph Unification** — represent forked child tasks as
  graph nodes; reuse placement / streaming / summaries; support remote
  child execution on attached or pooled machines.
- **Phase 5: Auto-Orchestration Policy** — automatic multi-runner
  routing based on cost / capability / load.

Both were cut as part of the lean stack pass (commit `7c3d826e`
"lean stack cut: kill voice + Phase 4/5"). The earlier "caveman"
compression idea bundled into Phase 4 was also dropped — it risked
silently degrading visible answers. The auto-orchestration policy
never made it past a stub.

If multi-runner auto-routing is reintroduced later, build it on top
of the existing fork primitive (Phase 2) — don't resurrect
`code_orchestrator_policy.go`. If graph unification comes back, it
should sit alongside the active session model, not replace it.

## Engineering Constraints

- preserve native runner output whenever possible
- avoid second session stores
- keep parent task canonical
- do not silently switch repos/workdirs
- do not silently use remote machines unless attach/work-mode says so
- keep auth codes/tokens in memory only
- make auto mode inspectable and reversible

## Testing

Need tests for:

- `fork` arg parsing
- compact context generation
- child prompt rendering
- remote auth start/status/submit/cancel wrappers
- continue with runner override
- fork with child runner override
- prompt footer rendering
- attachment detection with spaced file paths

Need higher-level integration tests for:

- attached remote Claude auth flow
- attached remote Codex device-auth flow
- continue same task with different runner
- fork child opencode task and observe streamed overlay

## Immediate Next Work

1. Finish `fork` end-to-end in interactive mode with overlay streaming.
2. Expose OpenCode provider configuration through `yaver code`.
3. Make child task compact summaries appendable back to parent session.

## Definition of Done

`yaver code` is "done enough" for the next stage when all of this is true:

- a user can attach to a headless machine and sign Claude/Codex in from terminal
- a user can list/continue/fork sessions without leaving `yaver code`
- a user can delegate a bounded task to OpenCode from the same terminal
- Yaver can stream delegated child output back into the parent terminal
- the user still feels like they are using one coherent coding terminal, not juggling three tools manually
