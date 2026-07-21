# Tmux Autorun As Mobile Tasks

Status: implementation audit, 2026-07-21. Code is source of truth; re-grep
before changing behavior.

## Product Contract

An autorun in `tmux` should appear in the mobile Tasks UI as a normal task with
one extra fact: it is backed by a live tmux pane. The user should be able to:

- see the task in the task list;
- see the machine/device running it;
- see the runner when detectable (`claude`, `codex`, `opencode`, or `shell`);
- see the tmux session name/id and active window/pane id;
- open the task and read bounded recent output;
- send a follow-up, menu choice, or confirmation into the pane;
- detach monitoring without killing the tmux session.

Mobile must not become a raw terminal manager. It should show enough identity to
make the task trustworthy and debuggable, not expose every tmux knob.

## Current Code Paths

Go agent:

- `desktop/agent/tmux.go` discovers sessions, adopts a session as a `Task`,
  polls bounded pane output, sends input, and detaches without killing tmux.
- `desktop/agent/httpserver.go` exposes `/tmux/sessions`, `/tmux/adopt`,
  `/tmux/input`, and `/tmux/detach`.
- `desktop/agent/tasks.go` exposes `TaskInfo` through `/tasks` and
  `/tasks/{id}`.

Mobile:

- `mobile/src/lib/quic.ts` has `TmuxSession` and `Task` fields.
- `mobile/app/(tabs)/tasks.tsx` lists tmux sessions, adopts them, shows adopted
  tasks, sends follow-up input through `/tmux/input`, and detaches.

Convex:

- `backend/convex/taskDispatchIntents.ts` stores prompt-free dispatch metadata
  only. It should link local pending work to the eventual task id, but it must
  not store prompts, pane output, paths, shell commands, or secrets.

## Metadata Contract

The Go agent can expose this safe metadata to mobile:

```json
{
  "taskId": "abc123",
  "source": "tmux-adopted",
  "runnerId": "claude",
  "tmuxSession": "yaver-multicloud-claude",
  "tmuxSessionId": "$6",
  "tmuxWindowIndex": "0",
  "tmuxWindowName": "zsh",
  "tmuxPaneIndex": "0",
  "tmuxPaneId": "%17",
  "isAdopted": true
}
```

Allowed in task/session list:

- ids, names, counts, timestamps, relationship labels;
- bounded pane preview;
- runner label inferred from process tree;
- adopted task id.

Not allowed in Convex or list metadata:

- full command lines;
- environment variables;
- working directory paths;
- prompts;
- stdout beyond bounded P2P task output;
- API keys or OAuth tokens;
- provider credentials.

## Signalling Model

Use three layers:

1. Go agent P2P HTTP/QUIC: authoritative live task and tmux control.
2. Mobile local cache: recent tasks/output for offline display.
3. Convex dispatch intent: prompt-free coordination while a machine wakes or a
   remote task is being handed off.

Do not route tmux pane output through Convex. If another device needs to know an
autorun exists while the machine is asleep or waking, store only
`localTaskId`, `taskId`, `targetDeviceId`, `cloudMachineId`, `requestedRunner`,
`status`, `reason`, and a short blocked-action label.

## Required Mobile Behavior

- Task cards show a `tmux` pill with session name or session id.
- Tmux session modal shows session id plus active window/pane id.
- Opening a task preserves `tmuxSession`, `tmuxSessionId`, `tmuxPaneId`, and
  `isAdopted`; detail fetches must not drop fields present in list fetches.
- Follow-up on adopted tasks bypasses runner fork/resume and uses `/tmux/input`.
- Detach marks the Yaver task stopped but leaves the tmux session alive.
- If `/tmux/sessions` is unavailable, show a soft empty/error state; do not hide
  already-known adopted tasks.

## Required Go Agent Behavior

- `tmux` must be auto-installed where unambiguous and non-interactive.
- `/tmux/sessions` must return an empty list, not a hard failure, when no tmux
  server is running.
- Session adoption must be idempotent from the user's perspective: an already
  adopted session should reveal its task id instead of creating a duplicate.
- Pane polling must be bounded.
- Menu-choice detection must block accidental prompt submission into numbered
  Claude/Codex menus.
- Re-adopt running tmux-backed tasks on agent restart if the session still
  exists.

## Cloud Workspace Fit

Cloud Workspace machines should start with tmux available because every remote
runner seat and autorun depends on it. For normies, this is invisible: they see
tasks and status, not tmux management. For advanced users, tmux identity is the
debug handle: `session`, `session_id`, `window`, `pane`.

## Test Matrix

- Go: parse session metadata, list sessions, adopt/detach, send input, re-adopt
  on restart, HTTP endpoints, no-tmux empty response.
- Mobile: task list maps tmux identity fields, task detail preserves them,
  adopted follow-up uses `/tmux/input`, modal renders session/window/pane ids.
- Convex/mobile dispatch: pending Cloud Workspace task links to eventual task id
  without storing prompt/output/path data.
