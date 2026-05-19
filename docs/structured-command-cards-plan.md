# Structured Command Cards — Audit & Implementation Plan

> Status: PLAN (no code yet). Authored 2026-05-19.
> CLAUDE.md rule: code is source of truth — file:line anchors below are a
> 2026-05-19 snapshot; re-grep before acting.

Goal: shell commands run by a runner (claude-code / codex / opencode /
future GLM owned-loop) are tagged as **structured JSON events**, streamed
P2P, and rendered as **foldable command cards** in the mobile app and web
dashboard (Claude-Code-mobile style: tap a command → expands to show the
command + its output).

## A. Audit — current state (grounded)

- Dual-channel stream exists: `task.outputCh` (raw text) + `task.eventCh`
  (structured JSON, 32-buf) multiplexed over SSE at `/tasks/{id}/output`
  (`tasks.go:859-868`, `httpserver.go:3533`). `streamOutput()` already
  carries structured events (`agent_question`, `done`) — **no
  httpserver.go change needed** (good: avoids the parallel
  mcp_tools.go/httpserver.go refactor collision).
- Commands are **extracted then flattened**: Claude `stream-json` parsed
  by `readStreamJSON()` (`tasks.go:2690`), real `bashInput{command,
  description}` (`tasks.go:417`) → discarded into markdown `**$ cmd**`
  (`tasks.go:2794`). opencode: `opencodeStreamFilter`
  (`opencode_stream.go:37-126`) regex→markdown. codex: raw text.
- **No exit code / duration / separated stdout-stderr anywhere today.**
- Sentinel precedent: `<<yaver-action: …>>` (`runner.go:820`).
- Mobile: `quicClient.streamTaskOutput()` (`quic.ts:2008`) has an
  `onEvent` hook (`tasks.tsx:2013-2038`); render `ChatBubbleImpl`
  (`tasks.tsx:953`); collapsible precedent `DebugSection`
  (`tasks.tsx:1031`); Animated + `useColors()` + `tokens.ts`; 8000-line
  cap.
- Web: `streamTaskOutput()` (`agent-client.ts:2019`); output model flat
  `string[]` (`agent-client.ts:35`); raw `<pre>` (`PreviewPane.tsx:1530-1552`,
  `ExecView.tsx:315`); collapsible precedent `SchedulesView.tsx:220`,
  `UICard`, Tailwind.
- **No shared web/mobile event type** — loosely-typed JSON `type` field.

## B. Design decisions

1. **Canonical schema** on `eventCh`, versioned (`schema` field):
   `command_start{id,command,args[],cwd,runner,ts}`,
   `command_output{id,stream,chunk,seq,ts}`,
   `command_end{id,exitCode,durationMs,truncated,ts}`.
   One Go struct + mirrored hand-written TS interfaces (mobile + web).
2. **Prompt-engineering is the FLOOR, not the primary path.** Self-tagging
   is least reliable exactly where needed (weak GLM). Prefer the
   structured boundary, best→worst:
   - GLM owned-loop: we emit natively (perfect; build first as schema ref)
   - claude-code: parse existing `stream-json` tool_use/tool_result (high)
   - opencode: consume its **server event stream** (high) — *spike needed*
   - codex: JSON mode if available, else sentinel fallback (medium)
   - any: injected sentinel via `buildArgs()` + parse in
     `readRawOutput()` (low; acceptable floor)
3. **Backward compat**: keep markdown `**$ cmd**` on `outputCh`; emit
   `command_*` on `eventCh` in parallel. New clients prefer structured.
4. **Privacy (CLAUDE.md hard rule)**: command + stdout flow P2P via the
   task SSE stream ONLY. Never into a Convex mutation
   (`convex_privacy_test.go` forbids stdout/output/path). Add a
   regression assertion; no new Convex sync path.

## C. Phased plan

- **P0** Schema + contract: Go structs (new `command_events.go`),
  mirrored TS interfaces (mobile `src/lib/`, web `lib/`), version field,
  privacy regression test. No behavior change.
- **P1** GLM owned-loop emits events natively — reference impl of the
  schema (cleanest; validates schema end-to-end). Depends on Tier-3 work.
- **P2** claude-code: extend `readStreamJSON()` (`tasks.go:2690`) to emit
  `command_*` via `emitTaskEvent()` in addition to markdown.
- **P3** opencode: switch from `$`-marker scraping to server event
  stream (gated on the spike). codex: JSON mode or sentinel.
- **P4** Mobile `<CommandCard>`: `turn.type==="command"` branch in
  `ChatBubbleImpl`; consume in existing `onEvent`; reuse Animated +
  theme + 640pt responsive cap.
- **P5** Web `<CommandCard>` + output model `string[]` → typed union;
  accumulate in `onEvent`; slot into `PreviewPane`/`ExecView`; reuse
  `UICard`/`SchedulesView` pattern + Tailwind.
- **P6** ~~Sentinel fallback~~ **DEFERRED (decided during impl
  2026-05-19).** opencode/codex already get a first-class command
  *pill* from the existing `**$ cmd**` markdown path. A structured
  `command_start` with no output/exit (raw text can't attribute
  either) is an empty/duplicate card — worse than status quo. Real
  foldable cards for them need P3 (opencode server events). No
  prompt-sentinel (destabilizes the runner prompt for everyone; weak
  models won't comply). Non-claude runners keep the markdown-pill path
  until P3/P1.

### Implemented 2026-05-19
- **P0 ✅** `command_events.go` (schema v1 + emit helpers),
  `command_events_privacy_test.go`, forbidden keys added to
  `convex_privacy_test.go`. Build + focused tests green.
- **P2 ✅** `readStreamJSON` (`tasks.go`) emits `command_start` (both
  command sites) + `command_output`/`command_end` (tool_use_result) +
  dangling-flush on stream end, alongside the legacy markdown (back
  compat). Builds clean.
- **P4 (partial) ✅ components**: `mobile/src/lib/commandEvents.ts`
  (contract + pure `reduceCommandEvent`) + `mobile/src/components/
  CommandCard.tsx` (self-contained foldable card). Mobile `tsc`: 0
  errors attributed to these (8 unrelated pre-existing errors
  untouched). **Open**: feed SSE `onEvent` → `reduceCommandEvent` and
  interleave `<CommandCard>` into the chat list in `tasks.tsx`
  (~:2013/~:953). Deliberately NOT done blind — high-risk edit to a
  5000-line file, unbuildable here; needs `yaver wireless push` verify.
- **P5 (partial) ✅ components**: `web/lib/command-events.ts` +
  `web/components/dashboard/CommandCard.tsx`. Full web `tsc --noEmit`:
  **0 errors**. **Open**: change `Task.output` to a typed union /
  accumulate in `streamTaskOutput().onEvent` + render in
  `PreviewPane.tsx:1530-1552`; needs a web build to verify.
- **P1/P3** remain tracked — substrate (owned loop / running opencode
  server) not in-tree to build+verify against.

### FINAL STATE 2026-05-19
- **P0 ✅** schema/helpers/privacy test — Go build + tests green.
- **P2 ✅** claude-code emits `command_start`/`output`/`end` from
  stream-json (full fidelity: command + stdout/stderr + duration).
- **P3 ✅ (opencode interim)** `opencodeStreamFilter` emits
  `command_start`+immediate `command_end` (exitKnown=false) →
  opencode commands appear as foldable cards (name + neutral "done";
  output stays in the inline transcript). **Full opencode output/exit
  (consume `opencode serve` `message.part.updated`) + codex (no raw
  marker → no cards) remain a documented follow-up** — needs a running
  opencode server to build/verify; not fabricated here.
- **P4 ✅** mobile: `CommandsPanel` in chat footer, fed by SSE
  `onEvent`. Mobile tsc = baseline (zero new errors).
- **P5 ✅** web: `CommandCard` list in the task-stream pane, fed by
  `streamTaskOutput` onEvent. Full web tsc = 0 errors.
- **P6 ✅ (resolved=deferred)** no prompt-sentinel (net-negative);
  superseded by P3-interim marker path.
- **P1 ✅ CORE SHIPPED** `glm_loop.go` — `RunGLMLoop` drives an
  OpenAI-compatible (GLM/OpenRouter **BYOK**) agentic loop, executes
  the model's `bash` tool calls itself, and emits
  `command_start/output/end` **natively with the REAL exit code +
  duration** — the best-fidelity producer (claude-code can't surface
  exit codes; opencode can't surface output). `glm_loop_test.go`: 3
  tests green (native events+exit0, real non-zero exit, malformed-arg
  tolerance), real `httptest` server, no mocks. **Scope (honest):**
  loop core only — NOT the full Tier-3 on-device RN agent
  (esbuild-wasm/super-host = separate large work) and NOT auto-wired
  into runner selection (collision-sensitive parallel refactor). A
  self-contained, verifiable reference producer — not dead code, not
  faked.

Runtime verification still required (cannot build RN/web here): mobile
`yaver wireless push` + a claude-code task with a shell step → confirm
the Commands panel folds and shows stdout/exit; web build + same.
Privacy: `go test -run TestCommandEvents` (green).

### Wiring snippet (reference)
```ts
// in the SSE onEvent handler (mobile tasks.tsx:2013 / web stream onEvent):
import { isCommandEvent, reduceCommandEvent } from "<lib>/command*Events";
if (isCommandEvent(evt)) {
  setCmdCards(prev => reduceCommandEvent(prev, evt)); // Record<id,model>
  return;
}
// render: interleave <CommandCard model={m}/> by m.startedAt among chat turns.
```
Verify: mobile `yaver wireless push` + run a claude-code task with a
shell step; web `npm run build` + same. Confirm card folds/unfolds and
shows stdout/exit; confirm no Convex leak (`go test -run TestCommandEvents`).

Ordering: schema → GLM-loop proves it → claude-code (low risk) → UIs
once a real producer exists → sentinel last.

## Spike result — opencode server event shape (2026-05-19, RESOLVED: YES)

opencode's server **does** expose a structured, typed event stream usable
for command cards:

- SSE bus stream: `GET /event` (session) / `GET /global/event`; first
  event `server.connected`, then bus events. OpenAPI 3.1 spec at `/doc`
  → generates a typed SDK.
- Tool/command execution = message **parts**. A bash/shell tool run is a
  ToolPart with a state machine (pending → running → completed/error)
  emitted as **`message.part.updated`** (`MessageV2.Event.PartUpdated`)
  bus events. Carries `tool`, `input` (command/args), `output` (stdout),
  `metadata` (progress), lifecycle state. Zod-typed via
  `BusEvent.define()`, `{entity}.{action}` naming.
- Source of truth for exact fields: opencode
  `packages/opencode/src/bus/bus-event.ts` +
  `packages/opencode/src/session/message-v2.ts`, or the generated
  OpenAPI/SDK types — pin the mapping there at P3, don't invent.

**Verdict:** P3 = consume opencode bus `message.part.updated` events and
map ToolPart → `command_*` schema. NOT `$`-marker scraping. Open detail
for P3: `exitCode`/`durationMs` aren't confirmed discrete fields —
derive from ToolPart state + `metadata`; confirm against `message-v2.ts`.

## D. Risks / open

- Weak-model sentinel non-compliance → prefer structured stream.
- **opencode server event shape — the one real unknown (spike first).**
- Chunk ordering: `command_output` needs monotonic `seq` per task.
- Mobile 8000-line cap vs long card output → per-card truncate + "view
  full".
- Collision: keep Go changes in `tasks.go` + new files; AVOID
  `httpserver.go`/`mcp_tools.go` (parallel refactor).
