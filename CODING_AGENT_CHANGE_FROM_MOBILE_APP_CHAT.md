# Coding Agent Change From Mobile App Chat

Date: 2026-05-02

## Goal

Allow users to change the coding agent from an active chat on:

- Yaver mobile app
- Yaver web UI

Supported first-class agents:

- `claude`
- `codex`
- `opencode`

For `opencode`, also support:

- agent mode selection: `default`, `build`, `plan`, plus custom OpenCode agents discovered from the target machine
- provider-aware setup from UI, including `GLM_API_KEY` / Z.ai, OpenAI, Anthropic, OpenRouter, Ollama-compatible backends, and other existing OpenCode providers

The switch must be compatible across:

- mobile UI
- web UI
- Go agent HTTP API
- Go agent CLI / `yaver code`
- MCP tools
- existing task/session persistence

## Current State

### What already works

- New tasks can be started with explicit `runner`, `model`, and `mode`.
  - Web uses `createTask(... runner, model, mode ...)` in [web/components/dashboard/VibeCodingView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/VibeCodingView.tsx:462)
  - Mobile uses `POST /tasks` with `runner`, `model`, and `mode` in [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts:1346)
  - The Go agent already accepts `mode` in task creation in [desktop/agent/code_control.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/code_control.go:894)

- OpenCode mode already exists on current task creation surfaces.
  - Web passes `mode` only for `opencode` in [web/components/dashboard/VibeCodingView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/VibeCodingView.tsx:474)
  - Mobile passes `selectedOpenCodeMode` for `opencode` in `Tasks` screen

- Runner auth/status/setup machinery already exists.
  - `runner_auth_status`, `runner_auth_setup`, `runner_auth_browser_*`
  - OpenCode config APIs already exist, including `defaultAgent`, `buildModel`, `planModel`, full `Agents[]`, provider list, diagnostics

- Session transfer / export / import / handoff primitives already exist.
  - Transfer bundle includes task turns, result text, runner, model, and session metadata in [desktop/agent/transfer.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/transfer.go:23)
  - `yaver code fork` already exists for creating a child task on another runner in [desktop/agent/code_control.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/code_control.go:759)

### What does not work today

- Runtime switching inside an active chat does not exist.
  - Web continue path sends only `{ input }` to `/tasks/{id}/continue` in [web/lib/agent-client.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/agent-client.ts:1340)
  - Web chat continue action does not pass `runner`, `model`, or `mode` in [web/components/dashboard/VibeCodingView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/VibeCodingView.tsx:486)
  - Mobile continue path also only continues the same task/session; it does not switch agent

- Current `continue` semantics are bound to the existing runner session.
  - `/tasks/{id}/continue` is routed as a plain continuation in [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go:3330)

## Product Decision

Do not mutate the live runner session in place when the user changes agent.

Instead:

1. keep the existing task/session immutable
2. create a new child task using the newly selected runner/model/mode
3. carry over recent context from the current chat into that new child task
4. visually show that the conversation has branched to a new agent

This matches the existing Yaver mental model better than trying to hot-swap a live Claude/Codex/OpenCode process mid-session.

## Why Fork Instead Of In-Place Switching

### In-place switching is risky

- `continueTask` is runner-session specific
- different runners keep different session files and resume semantics
- Claude/Codex/OpenCode do not share a session format
- changing runner mid-task could corrupt assumptions around:
  - session ID
  - resume support
  - output rendering
  - analytics
  - transfer/export/import

### Forking is already aligned with the codebase

- Yaver already has a notion of child tasks and runner forks
- transfer bundles already serialize turns and result text
- `yaver code fork` is the closest current primitive

## UX Definition

### User action

From an active chat in mobile or web:

1. user opens `Agent & Model`
2. user chooses a different runner/model/mode
3. user presses `Switch agent`

### Result

Yaver creates a new child task and keeps the old task visible as the parent.

The new agent receives:

- current project/workdir context
- machine/deploy/git context already used in the composer
- a bounded recent excerpt from the existing chat
- a short system note explaining that this is a continuation from another agent
- the user’s new message

### UI notice

The UI should politely state that only the recent part of the chat is forwarded, not the entire transcript.

Recommended copy:

`Switching to Codex will start a new child chat. Yaver will include the most recent part of this conversation as context so the new agent can pick up where you left off.`

Recommended advanced detail line:

`For speed and token safety, Yaver sends roughly the last X words plus the latest task summary, not the entire chat history.`

## Context Carry-Over Design

### Principle

Do not forward the entire chat by default.

Forward:

- latest task title
- latest task status / runner / model
- latest project path and project name
- last assistant result tail if available
- last N turns, clipped by a word budget
- the user’s explicit new prompt

### Proposed defaults

- `recent_context_word_budget = 1200`
- `assistant_result_tail_word_budget = 400`
- `max_turns_considered = 8`

These should be server-owned defaults, not UI-only constants.

### Context block format

The new child task prompt should prepend a bounded structured handoff block:

```text
[Conversation Handoff]
Previous task: <task-id>
Previous runner: <runner>
Previous model: <model>
Project: <project-name>
Work dir: <workdir>

Recent chat context follows. This is a clipped excerpt, not the full transcript.

User: ...
Assistant: ...
User: ...
Assistant: ...

Latest assistant tail:
...

[New User Request]
...
```

### Why words, not characters

- easier to explain in UI
- more stable across languages than raw char count
- close enough for budgeting without tokenizer dependency in UI

### Server-side ownership

The clipping and prompt assembly must happen in the Go agent, not only in mobile/web.

Reason:

- MCP / CLI / future surfaces should get identical behavior
- avoids drift between web and mobile
- keeps context policy centralized

## API Plan

### New endpoint

Add a new Go-agent endpoint:

`POST /tasks/{id}/fork`

Body:

```json
{
  "runner": "codex",
  "model": "gpt-5.4",
  "mode": "",
  "input": "Continue this work, but focus on the failing API tests.",
  "contextWords": 1200
}
```

Response:

```json
{
  "ok": true,
  "taskId": "child-task-id",
  "runnerId": "codex",
  "status": "running",
  "parentTaskId": "old-task-id",
  "relationship": "forked-by-yaver"
}
```

### Behavior

The endpoint should:

1. load parent task
2. extract recent turns / tail / metadata
3. compose bounded handoff prompt
4. create a new task with requested `runner`, `model`, `mode`
5. mark parent/child relationship in task metadata

### Compatibility

- keep existing `/tasks/{id}/continue` unchanged
- keep existing `/tasks` creation unchanged
- add `fork` as a new additive endpoint

### Reuse

Internally, this should reuse as much as possible from:

- existing `code fork` logic
- transfer bundle turn extraction logic

## Task Model Changes

Additive only.

Extend task metadata with optional fields:

- `parentTaskId`
- `forkReason` = `runner-switch`
- `forkedFromRunner`
- `forkedFromModel`
- `forkedFromMode`
- `contextWordsUsed`

Do not change existing task IDs, session IDs, or runner semantics.

## OpenCode-Specific Plan

### Runner

Top-level runner remains `opencode`.

### Mode

Use `mode` to mean OpenCode agent mode:

- `""` or omitted = OpenCode `defaultAgent`
- `build`
- `plan`
- custom agent name from `OpenCodeConfigSummary.Agents`

### Provider setup

When the user selects `opencode` and it is not ready:

- show `Configure OpenCode`
- show provider-aware setup
- include GLM/Z.ai as a first-class option

### GLM support

Allow the UI to:

- submit `GLM_API_KEY`
- create/update provider `glm`
- set the correct base URL
- optionally set top-level model / plan model / build model

Recommended default GLM preset:

- provider ID: `glm`
- provider name: `Z.ai (Zhipu)`
- base URL: `https://open.bigmodel.cn/api/paas/v4`

## Mobile Plan

### Surfaces

1. active chat in `mobile/app/(tabs)/tasks.tsx`
2. new-task flows that should share the same agent picker

### Changes

1. keep current new-task `runner/model/mode` behavior
2. add `forkTask(...)` client call
3. when user changes runner during an active chat:
   - if same runner/model/mode, keep using `continueTask`
   - if different, use `forkTask`
4. show parent/child relation in chat UI
5. show context-carry notice before confirming the switch

### New mobile client methods

In `mobile/src/lib/quic.ts` add:

- `forkTask(taskId, { runner, model, mode, input, contextWords? })`

### Mobile persistence

Extend per-device runner preferences to store:

- `runnerId`
- `model`
- `mode`

for `opencode`, `mode` is meaningful
for other runners, `mode` is empty/ignored

## Web Plan

### Surfaces

Primary surface:

- `web/components/dashboard/VibeCodingView.tsx`

### Changes

1. keep current new-task creation path
2. replace runtime-switch behavior from plain `continueTask` to:
   - `continueTask` when runner/model/mode unchanged
   - `forkTask` when changed
3. add a switch confirmation notice
4. display parent/child branch UI in the chat/task list

### Important current gap

Web already has runner/model/mode selection in the composer, but `continueChatTask()` still calls plain continuation in [web/components/dashboard/VibeCodingView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/VibeCodingView.tsx:486). This is the exact behavior to change.

## UI Copy Plan

### Switch confirmation

Use polite, explicit copy:

`This will start a new child chat with <Agent>. Yaver will include the most recent part of this conversation as context so the new agent can continue smoothly.`

### Detail line

`To keep things fast and relevant, Yaver sends a recent excerpt and latest task summary instead of the full chat history.`

### Optional expandable detail

`Included context: last ~1200 words, recent turns, and the latest task summary.`

## MCP / CLI Compatibility

### MCP

Add additive tool support for fork-by-runner-switch:

- either expose `task_fork`
- or route to existing session/task operation machinery

The semantics must match UI behavior:

- fork current task
- pass bounded recent context
- start child runner with explicit runner/model/mode

### CLI

Keep `yaver code fork` semantics aligned.

If `mode` support is missing in the current fork path, extend it additively so:

`yaver code fork <task-id> --agent opencode --model <provider/model> --mode plan <message>`

matches web/mobile behavior.

## Analytics / Audit

Track:

- `agent_switch_requested`
- `agent_switch_completed`
- `agent_switch_failed`
- source surface: `mobile` | `web` | `mcp` | `cli`
- parent runner/model/mode
- child runner/model/mode
- context word count used

This helps verify:

- users actually use runtime switching
- context carry is sufficient
- no silent regressions in branch creation

## Rollout Order

### Phase 1

- Add server-side `fork task with recent context` primitive
- Add tests

### Phase 2

- Wire web runtime switch

### Phase 3

- Wire mobile runtime switch

### Phase 4

- Add MCP/CLI parity

### Phase 5

- Refine UI copy and context budget tuning

## Testing Plan

### Go agent tests

1. fork endpoint creates child task with requested runner/model/mode
2. recent context is clipped to configured budget
3. parent task remains unchanged
4. OpenCode mode is forwarded correctly
5. empty mode falls back to OpenCode default agent

### Web tests

1. continuing with same runner calls `/continue`
2. changing runner calls `/fork`
3. change from `claude` to `codex` shows context notice
4. change to `opencode/build` forwards `mode=build`

### Mobile tests

1. same runner/model/mode continues existing task
2. changed runner forks child task
3. OpenCode custom agent mode is persisted and reused
4. GLM setup path updates OpenCode readiness

## Non-Goals

- hot-swapping a live runner process in place
- forwarding the full conversation transcript by default
- inventing mobile-only or web-only switch semantics
- treating OpenCode `build` / `plan` as separate top-level runners

## Summary

The safest compatible design is:

1. keep existing tasks and sessions immutable
2. introduce an additive fork endpoint
3. use recent bounded chat context as handoff input
4. make web and mobile use the same runtime-switch semantics
5. keep `claude`, `codex`, and `opencode` as the only first-class UI agents
6. keep OpenCode mode and provider setup fully OpenCode-aware, including GLM API key flow
