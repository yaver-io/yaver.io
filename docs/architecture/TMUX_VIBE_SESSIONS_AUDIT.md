# Tmux Vibe Sessions — Every Agent Session Is A Task

Status: implementation audit, 2026-07-22. **Code is source of truth** — every
claim below was grepped or probed against a live box on the day of writing.
Re-verify before acting; other threads move these constants.

Extends `TMUX_AUTORUN_MOBILE_TASKS.md` (2026-07-21), which covered
*Yaver-adopted* tmux sessions as tasks. This document covers the harder case
the user actually has open right now: **tmux sessions Yaver did not create,
running agents Yaver did not start, several of them inside a single window as
separate panes.**

## Product directive (from the user, verbatim intent)

1. *"It's gonna be tmux attach from the Yaver mobile app basically."*
2. *"We can have a discover-session API with the Go agent and mobile app."*
3. **"Don't pollute mobile UI / web UI. Make plumbing with tasks."**
4. **"Each session will be treated as a Task in the Yaver ecosystem UI surface."**

(3) and (4) are the load-bearing constraints and they govern every design
decision below. **No new screens. No new tabs. No new dashboard panels.** A
coding agent running in a tmux pane is a **Task** — it appears in the Tasks
list, opens in the Task detail, takes follow-up input through the composer
already there, and reaches a real terminal through the `/shell` screen that
already exists. The work is almost entirely in the Go agent; the client work is
plumbing, not surface.

---

## 1. The two layouts that must both work

The user's machine runs several coding agents at once — a `claude`, a `codex`,
an `opencode` — laid out one of two ways:

- **Layout A — session per agent.** `tmux new -s claude`, `tmux new -s codex`, …
- **Layout B — one session, many panes.** One session, one window, split into N
  panes, a different agent in each. *(This is the layout in the user's second
  screenshot, and it is the layout the current code cannot see.)*

Both, and every mix. Live probe of the box on 2026-07-22 — Layout B, today:

```
session 0 ($0), 1 window, 2 panes
  %37  pane_pid 27653  active    title "⠂ Discuss arbitrage and BYO options…"
  %50  pane_pid 86525            title "⠐ Claude Code"
```

Under the current code this machine reports **one** agent.

---

## 2. What already exists

Far more than a green-field reading suggests. This is an extension job.

### 2.1 Go agent — discovery and adoption (`desktop/agent/tmux.go`, 991 lines)

| Capability | Symbol | Notes |
|---|---|---|
| List sessions | `TmuxManager.ListTmuxSessions` `tmux.go:276` | `tmux list-sessions -F …` |
| Classify vs Yaver | same | `adopted` / `forked-by-yaver` / `unrelated` |
| Identify the agent | `detectAgentType` `tmux.go:819` | pane PID → `ps -o command=`, then **direct children only** via `pgrep -P` |
| Known agents | `knownAgentBinaries` `tmux.go:58` | `claude`, `codex`, `opencode` — exactly the three named |
| **Adopt as a Task** | `AdoptSession` `tmux.go:327` | `Task{Source:"tmux-adopted", IsAdopted:true, TmuxSession, TmuxPaneID, …}` + poll goroutine |
| Detach, don't kill | `DetachSession` `tmux.go:399` | task → stopped, tmux lives |
| Send input | `SendTmuxInput` `tmux.go:522` | `send-keys -l --`, **250 ms beat** (`tmuxSubmitDelay` `tmux.go:453`), then `Enter` |
| Menu guard | `tmuxPaneAwaitingChoice` `tmux.go:496` | ≥2 numbered options in last 12 lines ⇒ refuse a prompt |
| Bounded output poll | `pollTmuxOutput` `tmux.go:585` | 500 ms tick, `diffCapture` → task output channel |
| Alt-screen aware capture | `capturePane`/`paneCaptureSignal` `tmux.go:888,909` | tries normal + alternate, keeps the higher-signal one |
| Re-adopt after restart | `ReAdoptOnStartup` `tmux.go:669` | |

**The session→Task bridge the directive asks for already exists.** `AdoptSession`
is exactly "treat this tmux session as a Task", and `Task` already carries
`TmuxSession`, `TmuxSessionID`, `TmuxWindowIndex`, `TmuxWindowName`,
`TmuxPaneIndex`, `TmuxPaneID`, `IsAdopted`. The remaining work is to make it
**pane-scoped, automatic, and status-bearing** — not to invent a new model.

The **menu-guard reasoning in `SendTmuxInput` is load-bearing product knowledge**
and must survive any refactor verbatim: a prompt typed while codex showed
`› 1. Update now` selected it, codex self-updated, exited, and took the tmux
session with it. A bare digit confirms *by itself* — appending Enter answers the
*next* modal sight-unseen, and claude's next modal has `No, exit` as option 1.

### 2.2 Go agent — HTTP surface

```
GET  /tmux/sessions   httpserver.go:804
POST /tmux/adopt      httpserver.go:805   {session}
POST /tmux/detach     httpserver.go:806   {taskId}
POST /tmux/input      httpserver.go:807   {taskId, input}
WS   /ws/terminal     httpserver.go:1270  handleTerminalWS
WS   /ws/runner       httpserver.go:1273  handleRunnerPTYWS
GET  /runner/sessions httpserver.go:1275  — no client calls it
POST /runner/session/turn httpserver.go:1278 — the screenless vibe path
SSE  /tasks/:id/output — what Task detail already renders
```

All `/tmux/*` behind `s.auth` — **owner-only**, absent from
`hostShareAllowedPrefixes` (`httpserver.go:1717`). Correct posture; typing into a
pane is arbitrary code execution. MCP mirrors exist (`tmux_list_sessions`,
`tmux_adopt_session`, `tmux_detach_session`, `tmux_send_input`,
`httpserver.go:7528-7640`, plus `tmux_close_sessions` in `mcp_tmux_close.go`);
`tmux_send_input` already proxies to a remote device (`httpserver.go:7632`).

### 2.3 Go agent — the PTY seam (`runner_pty.go`, 596 lines)

`WS /ws/runner` **spawns** a runner inside `tmux new-session -A -s <name> <cmd>`
(`runner_pty.go:135-140`). `-A` = attach-if-present, so a dropped phone
reconnects into the same TUI. Frame protocol is **identical to `/ws/terminal`**
(`terminal_session.go`): binary both ways, text in for
`{"resize":{cols,rows}}` / `{"type":"terminate_session"}`. Zero chrome by
default — status bar off, re-asserted 8× over 1.2 s to win the initial-render
race (`runner_pty.go:167-180`). Owner-only (`runner_pty.go:40`). Resume via
`?session_id=` (`runner_pty.go:52`).

**`RunnerPTYSession.Confirmed` (`runner_pty.go:367`) is the most important prior
art in this codebase for §4.** Its comment records the incident: a tmux session
named `yaver-codex` whose runner has exited is a *plain shell*, and a "prompt"
typed into a shell is a **command**, submitted with Enter. A dictated turn aimed
at such a session ran the text and came back `zsh: command not found`. Sessions
routinely outlive their runner (systemd `KillMode=process` keeps the tmux server
up across agent restarts), so this is the normal end state, not a corner case.
The lesson — **classify by observing the process, never by trusting the name** —
is the whole basis of the status model.

### 2.4 Go agent — the screenless seam (`runner_session_turn.go`)

`POST /runner/session/turn {session?, runner?, text?, choice?, waitMs?}` — one
call that types into a live session and reads the pane back. Built for
watch/car/TV, explicitly *not* `/ws/runner` ("that endpoint hands you a raw pane
and assumes a terminal emulator on the other end — right for the phone's xterm,
useless on a wrist", `runner_session_turn.go:12`). Already encodes the three menu
hazards. Extend to pane targeting; do not duplicate.

### 2.5 Mobile — the Task surface is already built

This is why the directive is achievable with near-zero UI work.

| Piece | Path | State |
|---|---|---|
| Tmux session list + adopt + detach | `mobile/app/(tabs)/tasks.tsx:3817-3860` | `handleOpenTmuxSessions`, `handleAdoptTmuxSession` (adopt → Task → opens Task detail), `handleDetachTmuxSession` |
| Task detail live output | `tasks.tsx:2238` → `quicClient.streamTaskOutput` | SSE `/tasks/:id/output`, chat-style modal |
| Follow-up into an adopted task | `quic.ts:4326` `sendTmuxInput` → `POST /tmux/input` | already bypasses runner fork/resume |
| Client lib | `mobile/src/lib/quic.ts:4280-4326` | `listTmuxSessions`, `adoptTmuxSession`, `detachTmuxSession`, `sendTmuxInput` |
| **Real terminal** | `mobile/app/shell.tsx` | xterm.js in WebView (`XtermView.tsx` + `xtermBridge.ts`); `PTYTarget` kinds `shell`/`runner`/`tmux` at `:38-44` |
| **Deep link to a session's PTY** | `shell.tsx:94-99` | `/shell?session=<tmux>` — already used by `autoruns.tsx:216` "Open terminal" |
| Screenless turn | `quic.ts:9224-9257` | `POST /runner/session/turn`, incl. relay form |

So "attach from the phone" has a **built UI path already**: Task → "Open
terminal" → `/shell?session=…`. See GAP 2 for why it does not work.

### 2.6 Web — deliberately out of scope

`web/lib/agent-client.ts` has **no** `/tmux/*` methods at all; the dashboard
never calls `/tmux/sessions`. The only web tmux UI is `/spatial`
(`web/app/spatial/page.tsx:95,163,180` + `TmuxPane.tsx`), which attaches by
*typing* `tmux a -t <name>\r` into a `/ws/terminal` shell (`TmuxPane.tsx:17-22`).
Web has `@xterm/xterm@^6.0.0`; mobile has `@xterm/xterm@^5.5.0`.

Per the directive, **web gets nothing new**. If Tasks on web later render the
same Task objects, pane-backed tasks appear there for free — that is the point
of doing this in the Task model.

---

## 3. The gaps

### GAP 1 — Pane blindness: the data model stops at the session *(P0)*

`TmuxSession` (`tmux.go:22-38`) carries **one** `AgentType`, **one** `MainPID`,
**one** `PaneID`, **one** `PanePreview`, all from
`getActivePaneIdentity(sessionName)` (`tmux.go:783`) — which enumerates panes
only to pick the **active** one (first as fallback).

On Layout B:

- `/tmux/sessions` reports **one** agent. The other two do not exist to Yaver,
  so they can never become Tasks.
- `PanePreview` is the active pane's tail — `capturePane(sessionName, …)`
  (`tmux.go:888`) passes a *session* as `-t`, which tmux resolves to the active
  pane of the active window.
- `SendTmuxInput` → `sendTmuxLine(sessionName, …)` (`tmux.go:468`) targets
  `-t <session>` ⇒ **input always lands in whichever pane is active.** Following
  up on "the codex task" can type into the claude pane, and the response still
  says "sent".
- `tmuxPaneAwaitingChoice(sessionName)` (`tmux.go:496`) inspects the *active*
  pane, so **the menu guard protects the wrong pane.** A menu open in a
  non-active pane is invisible: the guard returns "not awaiting" and the prompt
  goes in — the exact codex-self-update failure the guard exists to prevent.

That last point is the compound danger: **GAP 1 silently disarms the safety
built in §2.1.** Same class as the `Confirmed` incident — we report success
while the bytes went somewhere else.

### GAP 2 — "Open terminal on a tmux session" is dead at the server *(P0, shipped bug)*

The user's headline ask has a UI path already wired, and it fails 100% of the
time.

`mobile/app/shell.tsx:52-56`, target kind `tmux`, builds:

```ts
q.set("name", target.sessionName);
return `${ws}/ws/runner?${q.toString()}`;   // NOTE: no `runner` param
```

`handleRunnerPTYWS` (`runner_pty.go:59-64`) opens with:

```go
runnerID := normalizeRunnerID(q.Get("runner"))
if runnerID == "" || !IsSupportedRunner(runnerID) {
    runnerPTYFail(conn, "unsupported runner \"\" — expected one of: …")
    return
}
```

`normalizeRunnerID("")` returns `""` (`runner_auth.go:376-388`, `default:` just
lowercases/trims), so the guard trips. **Every `/shell?session=<name>` deep link
— the "Open terminal" button on every autorun card (`autoruns.tsx:216`) and the
only route to an arbitrary tmux session from the phone — dies with
`unsupported runner ""`.**

And even with a `runner=` param, `/ws/runner` can only **spawn**: every path
builds `tmux new-session -A -s <name>` around `rc.Command`. There is no branch
that attaches to a session that already exists with something else in it. The
`-A` flag would attach if the name matched — but you cannot get there without
naming a runner, and naming a runner is a lie about a foreign session.

This is a textbook false green: the inventory (a button, a route, a handler)
says yes; the operation says no.

### GAP 3 — No vibing status *(P0)*

Nothing computes *working / waiting-on-me / idle / dead* for an agent pane. The
signals all exist but are used defensively and privately:

- `tmuxPaneAwaitingChoice` — only to *refuse* input inside `SendTmuxInput`. Never surfaced.
- `diffCapture` (`tmux.go:937`) — only to feed the task output channel; its "did anything change" answer is discarded.
- `paneCaptureSignal` (`tmux.go:909`) — only to pick normal vs alt screen.
- `RunnerPTYSession.Confirmed` — the only honest liveness bit, session-scoped and CLI-facing.

Note `/vibing/*` in the tree is **Vibe Preview** (app screenshots/clips,
`vibe_preview*.go`; mobile `apps.tsx:2233-2340`). Unrelated. Do not overload it.

A live probe found a strong unused signal — **`pane_title`**:

```
%37  ⠂ Discuss arbitrage and BYO options for Claude products
%50  ⠐ Claude Code
```

Claude Code writes its current activity into the terminal title with a braille
spinner while working. `pane_current_path` gives the project directory. Both are
one `tmux list-panes -a -F` away. §4.3 rates how much to trust them.

### GAP 4 — `detectAgentType` is one level deep *(P1)*

`detectAgentType` (`tmux.go:819`) checks the PID then **direct children only**.
Probed live: pane PID 27653 is `-zsh`, child 27772 is
`claude --dangerously-skip-permissions` → matches at depth 1. But
`zsh → npm exec → claude`, `zsh → wrapper → codex`, or anything under a
`direnv`/`mise` shim resolves at depth 2 and returns `""` — reported as **no
agent**, so it never becomes a Task.

Second-order: `pane_current_command` for a live claude is **`2.1.216`** — claude
renames its own process to its version string. Any classifier keying on
`pane_current_command` (as `listRunnerPTYSessions` does, `runner_pty.go:467`)
will not see "claude". `matchAgentCommand` (`tmux.go:838`) works only because
`ps -o command=` preserves argv[0]. Fragile; deserves a comment where it lives.

### GAP 5 — Absolute paths enter scope, and they are Convex-forbidden *(P1)*

Pane-level Tasks want `pane_current_path` — that is how a Task says *which
project* it is vibing. It is an absolute path and leaks the home-dir username.
It is on the forbidden list enforced by `convex_privacy_test.go`, whose
`fieldsWeForbidInAnyConvexPayload` scan looks for `/Users/`, `/home/`, `/root/`.
`TMUX_AUTORUN_MOBILE_TASKS.md` already forbids working-directory paths in
Convex. The field must be **P2P-only**, and the test matrix must prove it.

Note the related live constraint: `userProjects` rows are slug-only, no absolute
paths. A pane-backed Task that syncs must follow the same rule.

### GAP 6 — Task lifecycle for panes is unspecified *(P1)*

Making every agent pane a Task raises questions `AdoptSession` never had to
answer, because adoption was manual and session-scoped:

- **Identity.** A Task must map 1:1 to a pane across restarts. `PaneID` (`%37`)
  is stable for the pane's life but is reused after the pane dies. Session
  *name* is user-renameable. The key should be `session_id + pane_id` plus the
  pane PID as a tiebreak, and re-adoption must verify the process, not the id.
- **Auto-adopt or not.** Auto-adopting every agent pane means the Tasks list
  fills with things the user did not ask Yaver to manage — the "don't pollute"
  directive cuts both ways. Recommended: auto-adopt panes with a **confirmed**
  agent process; leave `no-agent` shells discoverable but unadopted.
- **Death.** `pollTmuxOutput` already marks a task finished when the session
  disappears (`tmux.go:596`). Pane-level needs the same for a pane closing while
  its session lives.
- **Duplicates.** `AdoptSession` refuses a second adoption of the same session
  (`tmux.go:336`). Pane-level needs the equivalent, keyed on the pane.
- **Bounded growth.** A user with many panes over many days accumulates finished
  Tasks. Needs a retention answer.

---

## 4. The status model

Governing rule from CLAUDE.md: **probe the real capability, never the proxy.**
"The inventory says yes, the operation says no" is exactly the failure mode — a
session named `yaver-codex` with no codex in it; a pane that looks idle because
its agent is blocked on a modal off-screen.

### 4.1 The unit is the pane

```go
// VibePane is one agent seat: a single tmux pane and everything we can honestly
// say about it. PaneID ("%37") is the stable handle — names and indexes move.
type VibePane struct {
    PaneID      string // "%37" — the ONLY safe target for send-keys/capture
    SessionName string
    SessionID   string // "$0"
    WindowIndex string
    WindowName  string
    PaneIndex   string
    Active      bool

    Agent          string // "claude" | "codex" | "opencode" | "shell" | ""
    AgentConfirmed bool   // a real process was OBSERVED — runner_pty.go:367
    PID            int

    Status       string // see 4.2
    StatusReason string // human sentence carrying the WHY (CLAUDE.md)
    Title        string // pane_title — the agent's own activity line
    IdleMs       int64  // since output last changed

    CurrentPath string // P2P ONLY — absolute path, NEVER to Convex (GAP 5)
    Preview     string // bounded tail, capture-pane -t <PaneID>

    TaskID string // the Yaver Task this pane is adopted as
}
```

### 4.2 Status values and what each is allowed to mean

| Status | Determined by | Meaning in the Task list |
|---|---|---|
| `working` | content changed within the sample window, **or** title carries a spinner glyph | It's vibing. Leave it. |
| `awaiting-input` | `tmuxPaneAwaitingChoice(<paneID>)` ≥2 numbered options | **It needs you.** Answer with a digit. |
| `idle` | agent confirmed, no change for > `vibeIdleThreshold` | Done, or waiting for a prompt. |
| `no-agent` | no agent process in the pane's tree | **Typing here runs a SHELL COMMAND.** |
| `dead` | `pane_dead=1` | Pane's process exited. |
| `unknown` | probe deadline exceeded | Say so; never guess. |

`no-agent` is the `Confirmed` incident rendered as product: the Task composer
must refuse to send a prompt into such a pane, name the reason, and offer to
start an agent instead.

`awaiting-input` must carry the parsed options in the payload so a watch or car
can read them aloud and answer with a digit via `/runner/session/turn {choice}`.

These map onto the existing `TaskStatus` vocabulary rather than replacing it —
`working` ≈ running, `awaiting-input` is the new one worth surfacing, and it is
the single most useful thing a Tasks list can tell a user with three agents up.

### 4.3 Trust ladder — the cheap signals are the lying ones

1. **Process tree** (`AgentConfirmed`) — the operation itself. Fix GAP 4 first:
   walk the tree to bounded depth (3), not direct children.
2. **Menu detection on the pane** — deterministic text match, pane-targeted.
3. **Output delta** — `diffCapture` vs the previous sample. Agent-agnostic;
   works for agents Yaver has never heard of.
4. **`pane_title`** — richest and *least* portable. Claude Code sets it; whether
   codex and opencode do is **unverified and must be probed on a real box before
   shipping**. A bonus label and a tiebreak for `working` — never the sole basis
   for a status.

### 4.4 Bounding — advisory work must never block

CLAUDE.md: *advisory work must never sit in the critical path of the operation
it annotates … carry a wall-clock deadline and degrade to empty rather than
block.* Every input here is an `exec.Command` fork, so this is not theoretical.

- **One** `tmux list-panes -a -F …` for the whole machine is the spine (single
  fork, cheap). Everything else is enrichment.
- Per-pane enrichment (`ps`, `pgrep`, `capture-pane`) runs under a **wall-clock
  deadline for the whole request**. On expiry return what is known with
  `Status:"unknown"`. A depth limit is not a bound — only the deadline is.
- Short TTL cache keyed by pane id, so a polling Tasks list does not fork `ps`
  per pane per second.

---

## 5. Attach — the tmux constraint and the two modes

### 5.1 The constraint

You attach to a **session**, not a pane. Landing on a specific pane means
`select-window` + `select-pane` — and the active pane within a window is
**window state shared by every client**, so a naive "focus the codex pane"
**moves the desktop user's focus too.** That is hostile on the user's own
machine, and it is Layout B.

Sizing is the second trap. Probed on this box: tmux 3.5a with
`window-size latest` and `aggressive-resize on` — the most-recently-active
client wins, so **a phone attaching resizes the desktop's window to phone
dimensions.** Any attach path must pin its sizing policy explicitly rather than
inherit it.

### 5.2 Mode A — Mirror *(default; pane-precise, non-invasive)*

Stream the pane; do not attach to it.

- **Out:** `capture-pane -e -p -t %<paneID>` on a tick, diffed — the
  `pollTmuxOutput` loop that already exists, upgraded from session-target to
  pane-target and from stripped to escape-preserving (`-e` keeps colors).
- **In:** `send-keys -t %<paneID>` through the existing menu guard with the
  existing 250 ms submit beat.

No client attaches ⇒ **no resize war, no focus stealing**; works per-pane in
Layout B; multiple viewers; degrades to a still frame on a bad link. Cost: not a
real TTY — mouse reporting and some alt-screen edges are approximations. For
reading an agent TUI and typing prompts, the right trade.

Mode A is also what makes the Task plumbing free: the mirror feeds the same
`/tasks/:id/output` SSE the Task detail already renders (`tasks.tsx:2238`).

### 5.3 Mode B — True attach *(explicit opt-in)*

A real PTY running a real tmux client, reusing `newTerminalSessionFromPTY`
(`terminal_session.go:72`) and the `/ws/terminal` frame protocol — so
`shell.tsx` renders it with no client change.

- **Layout A** → `tmux new-session -d -t <target> -s yaver-phone-<id>` puts the
  phone in the same **session group**: shares windows, keeps its **own current
  window**, killed on disconnect leaving the original untouched. Clean path.
- **Layout B** → a grouped session does not buy independent *pane* focus, so
  true attach necessarily shares focus. Mode B must say so before starting, and
  default to Mode A.
- Pin sizing deliberately; a watch-only client attaches read-only (`-r`).

### 5.4 Fixing GAP 2 concretely

`handleRunnerPTYWS` needs an attach branch **before** the runner guard: when
`?name=` names an existing tmux session and `?runner=` is absent, attach instead
of spawn. That single change makes every already-shipped "Open terminal" button
work. The mobile deep link then extends from `?session=` to `?session=&pane=`
for Layout B — one param, no new screen.

---

## 6. Proposed API

Owner-only, all of it; the new routes join `/tmux/*` in the
`hostShareAllowedPrefixes` exclusion.

```
GET  /tmux/sessions                    # EXTEND: each session gains panes:[VibePane]
                                       # existing mobile/spatial callers keep parsing
POST /tmux/adopt   {session, pane?}    # EXTEND: adopt ONE pane as a Task
POST /tmux/input   {taskId, input, allowShell?}
                                       # EXTEND: route by the Task's PaneID, not session
WS   /ws/runner?name=<session>         # FIX GAP 2: attach when no runner= given
```

**`allowShell` (added during implementation, 2026-07-22).** Refusing every
`no-agent` pane outright would have broken a real shipped capability: adopting a
plain tmux session and sending it shell commands, which three existing tests
covered and which the spatial web UI relies on. The refusal is therefore the
DEFAULT rather than an absolute — `allowShell: true` is the explicit "I mean to
run a command" opt-in, and the refusal message names the flag so the capability
stays discoverable. Prompt-shaped callers (the mobile composer, dictation, the
screenless turn) simply never set it, so dictated text cannot reach a shell by
accident. MCP mirrors it as `allow_shell`.

**Extend, do not add.** Every route above is already called by shipped clients;
a new `/vibe/*` namespace would mean new client code, which the directive
forbids. `/vibing/*` in particular is taken by Vibe Preview and must not be
overloaded.

MCP parity (CLAUDE.md: a diagnosis only the CLI can see does not exist for a
user on their phone): `tmux_list_sessions` returns panes; `tmux_adopt_session`
and `tmux_send_input` take an optional `pane`.

---

## 7. Client work — the whole of it

Deliberately tiny, per the directive.

**Mobile** (`mobile/src/lib/quic.ts`, `mobile/app/(tabs)/tasks.tsx`,
`mobile/app/shell.tsx`):

1. `TmuxSession` type gains `panes: VibePane[]`; `adoptTmuxSession` gains an
   optional `pane`. *(lib only)*
2. The existing tmux-session modal (`tasks.tsx:3817`) lists panes under each
   session instead of one row per session. Same modal, same component.
3. Task cards show the pane's `status` + `statusReason` — reusing the existing
   status pill, not a new element.
4. Composer refuses on `no-agent` with the reason. *(guard, not UI)*
5. `/shell?session=…&pane=…` — one extra param in the existing deep link.

**Web:** nothing. Pane-backed Tasks appear in any Task surface for free.

**Watch / Wear / TV / car:** status + `/runner/session/turn` only. No PTY on a
wrist — already this codebase's stated position (`runner_session_turn.go:12`).

RN surfaces (mobile, tablet, car, glass) share the lib, so (1) and (4)
propagate for free; native surfaces need no port because they get no new
capability. That satisfies cross-surface parity honestly rather than by
shipping a terminal to a watch.

---

## 8. Test matrix

Go (`desktop/agent`, real tmux, no mocks — the house pattern):

- Layout B fixture: one session, 3 panes, 3 distinct agents → 3 discovered, each
  with its own agent/status/preview, each adoptable as its own Task.
- Pane-targeted input lands in the **named** pane, provably not the active one.
- Menu guard fires for a menu in a **non-active** pane (the GAP 1 compound).
- `no-agent` pane refuses a prompt with the shell-command reason.
- Deep process tree (`zsh → npm exec → claude`) resolves at depth 2 (GAP 4).
- **GAP 2 regression:** `/ws/runner?name=<existing>` with no `runner=` attaches
  rather than failing `unsupported runner ""`.
- Deadline: a pane whose `ps` hangs yields `unknown` and the request still
  returns inside the bound.
- Pane dies while its session lives → its Task finishes.
- No tmux server → empty list, 200, not an error.
- **Privacy:** `currentPath` never reaches a Convex payload
  (`convex_privacy_test.go` extension, GAP 5).

Scope discipline: **never** run a broad `go test ./...` in `desktop/agent` — it
hits the real `~/.yaver` and can sign the machine out. Targeted `-run` only.

---

## 9. Incident-hardening — what this must leave behind

Per the standing rule that every incident leaves the product harder than it
found it:

- A **doctor probe** answering "why can't I see my agents?" in ten seconds: tmux
  resolvable (`DiscoverBinary("tmux")` — note the launchd minimal-`$PATH`
  history at `tmux.go:76-101`), server running, panes enumerable, per-pane
  classification **with its confidence bit**.
- A probe that **attempts the attach**, not one that reports the route exists.
  GAP 2 is precisely a check that would have been GREEN while the feature was
  100% broken.
- **Reasons in the payload.** `StatusReason` names the specific fix, never
  "check your configuration".
- `AgentConfirmed` surfaced, not swallowed — an unconfirmed pane must look
  different from a confirmed one, because typing into the two does entirely
  different things.

---

## 10. Open questions to resolve before implementation

1. **Do codex and opencode set `pane_title`?** Claude Code does (probed). If they
   don't, §4.3's tiebreak is claude-only and output-delta carries the others.
   **Probe on a real box; do not assume.**
2. **Auto-adopt policy** (GAP 6): every confirmed agent pane, or only on tap?
   This is the sharpest edge of "don't pollute" and is a product call.
3. **Idle threshold.** Agents pause mid-think; too tight and Tasks flap between
   `working` and `idle`. Needs measurement, not a guess.
4. **Retention** for finished pane-Tasks.
5. **`@xterm/xterm` 5.5 (mobile) vs 6.0 (web)** — pre-existing drift; not
   blocking, since no new terminal component is being built.
