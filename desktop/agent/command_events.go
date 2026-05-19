package main

import "time"

// Structured shell-command events for foldable command cards in the
// mobile app + web dashboard.
//
// These ride the existing per-task structured channel (Task.eventCh →
// emitTaskEvent → the SSE writer in streamOutput). Old clients that only
// understand {type:"output"}/{type:"done"} ignore unknown event types,
// so this is backwards-compatible — see the Task.eventCh doc comment.
//
// PRIVACY (CLAUDE.md hard rule): command + cwd + stdout/stderr flow P2P
// over the task SSE stream ONLY. They MUST NEVER be placed in a Convex
// mutation payload — convex_privacy_test.go forbids command/cwd/stdout/
// stderr/path. command_events_privacy_test.go pins this.
//
// Wire contract is mirrored, by hand, in:
//   mobile/src/lib/commandEvents.ts
//   web/lib/command-events.ts
// Keep all three in lockstep; bump CommandEventSchema on any breaking
// change so clients can gate.
const CommandEventSchema = 1

// nowMillis is a seam for tests.
var nowMillis = func() int64 { return time.Now().UnixMilli() }

// emitCommandStart announces a shell command a runner is about to run.
// id must be stable for the lifetime of this command so command_output
// / command_end can be correlated to it client-side.
func emitCommandStart(task *Task, id, command string, args []string, cwd, runner string) {
	if task == nil || id == "" || command == "" {
		return
	}
	if args == nil {
		args = []string{}
	}
	emitTaskEvent(task, map[string]interface{}{
		"type":    "command_start",
		"schema":  CommandEventSchema,
		"id":      id,
		"command": command,
		"args":    args,
		"cwd":     cwd,
		"runner":  runner,
		"ts":      nowMillis(),
	})
}

// emitCommandOutput streams a chunk of a command's output. stream is
// "stdout" or "stderr". seq is a per-command monotonic counter so the
// client can order chunks even if SSE delivery races.
func emitCommandOutput(task *Task, id, stream, chunk string, seq int) {
	if task == nil || id == "" || chunk == "" {
		return
	}
	if stream != "stderr" {
		stream = "stdout"
	}
	emitTaskEvent(task, map[string]interface{}{
		"type":   "command_output",
		"schema": CommandEventSchema,
		"id":     id,
		"stream": stream,
		"chunk":  chunk,
		"seq":    seq,
		"ts":     nowMillis(),
	})
}

// emitCommandEnd closes a command. exitKnown=false means the runner did
// not surface an exit status (e.g. claude-code stream-json tool results
// carry no exit code) — the client then renders a neutral "done" badge
// instead of success/fail. durationMs<=0 is treated as unknown.
func emitCommandEnd(task *Task, id string, exitCode int, exitKnown bool, durationMs int64, truncated bool) {
	if task == nil || id == "" {
		return
	}
	ev := map[string]interface{}{
		"type":      "command_end",
		"schema":    CommandEventSchema,
		"id":        id,
		"truncated": truncated,
		"ts":        nowMillis(),
	}
	if exitKnown {
		ev["exitCode"] = exitCode
	}
	if durationMs > 0 {
		ev["durationMs"] = durationMs
	}
	emitTaskEvent(task, ev)
}
