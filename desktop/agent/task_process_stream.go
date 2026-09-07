package main

import (
	"io"
	"log"
	"strings"
)

type runnerProcessChunk struct {
	Stream string
	Data   []byte
}

// emitRunnerProcessChunk is the process-I/O boundary shared by Claude Code,
// Codex and OpenCode CLI tasks. Raw bytes stay local/P2P in the terminal lane;
// the typed event preserves which OS pipe produced them so a GUI does not have
// to infer stderr from colour or prose. It reuses the existing bounded raw
// channel instead of duplicating every chunk into the event channel.
func (tm *TaskManager) emitRunnerProcessChunk(task *Task, stream string, chunk []byte) {
	if task == nil || len(chunk) == 0 {
		return
	}
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream != "stderr" && stream != "pty" {
		stream = "stdout"
	}
	tm.retainRunnerProcessChunk(task, stream, chunk)
}

// readRunnerProcessStream drains the non-semantic pipe of a structured runner.
// Claude's NDJSON stdout must remain isolated for readStreamJSON, but its
// process stderr is still user-visible evidence. Previously it was printed only
// to the daemon log, so a phone could show a silent spinner while the desktop
// process had already named the failure.
func (tm *TaskManager) readRunnerProcessStream(task *Task, stream string, r io.Reader) {
	if task == nil || r == nil {
		return
	}
	buf := make([]byte, 8*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			log.Printf("[task %s %s] %s", task.ID, stream, truncate(strings.TrimSpace(string(chunk)), 300))
			tm.emitRunnerProcessChunk(task, stream, chunk)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[task %s] %s read error: %v", task.ID, stream, err)
			}
			return
		}
	}
}
