# Desktop runner streaming contract

Yaver Desktop is a GUI around processes Yaver owns. It is not an extractor for
another vendor's already-open desktop chat window.

## What Yaver can wrap

For a Yaver task, the Go agent launches Claude Code, Codex, or OpenCode in the
selected project and owns the process transport:

- ACP uses the runner adapter's JSON-RPC stdin/stdout and emits semantic task
  events.
- CLI mode keeps stdout and stderr separate at the OS-pipe boundary. Both are
  copied into the bounded raw terminal lane; live `raw` SSE frames name their
  `stream` (`stdout`, `stderr`, or `pty`). Claude's NDJSON stdout is parsed into
  semantic presentation and command events while process stderr remains visible
  under Runner details.
- An adopted tmux pane is a PTY attachment. Its bytes are visible and input can
  be sent to the pane, but Yaver does not label terminal redraws as a trustworthy
  assistant answer.

GUI text input is task input, not an indefinitely-open pipe. A follow-up goes to
`POST /tasks/{id}/continue` (or the source-gated Vibing mirror) and resumes the
same runner conversation. Typed controls use `/control`. One-shot Claude/Codex
CLI processes deliberately receive a closed stdin because Claude can wait
forever on a pipe; interactive keystrokes belong to the PTY/WebSocket lane.

The task launcher uses the exact executable path exercised by readiness checks.
On Windows, `.cmd`/`.bat` npm shims are routed through `cmd.exe`; native `.exe`
runners remain direct. The same constructor is used for first turns and resumed
turns, preventing a green readiness check followed by a spawn failure.

## What an already-open vendor GUI exposes

Claude Desktop or a Codex desktop UI can call Yaver MCP tools. That lets it
create a Yaver task whose output Yaver owns and streams. It does not grant Yaver
access to the vendor app's private conversation model.

Remote Desktop/WebRTC can stream that window's pixels and send consented input;
accessibility can identify controls where the OS permits it. Those are visual
and input attachments, not semantic stdout/stderr. A surface must never present
OCR, accessibility text, or terminal capture as a lossless vendor chat stream.

## Mobile and SDK path

Owner surfaces stream `GET /tasks/{id}/output`. Feedback/Dogfood SDK tokens use
`GET /vibing/task/{id}/output`; the handler first verifies the immutable task
source is feedback/vibing, then delegates to the identical SSE implementation.
The stream includes presentation snapshots, output, raw process bytes, command
events, render requests, and terminal status.

React Native uses `XMLHttpRequest.onprogress` because Hermes fetch can buffer an
SSE response until EOF. Browser runtimes use `ReadableStream`; interrupted lanes
fall back to the same source-gated task detail endpoint with bounded backoff.

Render intent is the `yaver_request_render` MCP call. It emits
`runtime_render_requested`; the client still decides whether to show **Render
updates** or execute one opt-in auto-render after coding settles. Phrase matching
remains compatibility only.

## Resource and privacy bounds

- Groomed transcript tail: 1 MiB per task.
- Raw stdout/stderr/PTY replay tail: 512 KiB per task.
- Live groomed queue: 128 chunks; live raw queue: 64 chunks. Slow consumers use
  retained snapshots/reconnect rather than growing RAM with runner output.
- Mobile SDK runner-details buffer: 64 KiB.
- Task prompts and process output stay local/P2P. Task lifecycle developer-log
  calls are not sent to Convex, and this streaming path adds no Convex writes.

## Source locations

- Process launch: `desktop/agent/command_shell.go`, `desktop/agent/tasks.go`
- Pipe/PTY stream identity: `desktop/agent/task_process_stream.go`,
  `desktop/agent/tmux.go`
- SSE and source gate: `desktop/agent/httpserver.go`, `desktop/agent/vibing.go`
- Typed render request: `desktop/agent/agent_render_request.go`
- React Native transport: `sdk/feedback/react-native/src/P2PClient.ts`
