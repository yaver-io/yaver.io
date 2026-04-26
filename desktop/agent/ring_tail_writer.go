package main

import (
	"strings"
	"sync"
)

// ringTailWriter is an io.Writer that buffers the last N lines of
// whatever was written to it. Used to capture the tail of a long
// subprocess so an HTTP handler can include the failing output in
// its error response without having to re-read the agent log file.
//
// Concurrency: safe for one writer goroutine plus arbitrary readers
// via lines(); subprocess stdout + stderr go through io.MultiWriter
// which serializes, so the single-writer assumption holds in
// practice.
type ringTailWriter struct {
	mu      sync.Mutex
	max     int
	buf     []string
	partial strings.Builder
}

func newRingTailWriter(maxLines int) *ringTailWriter {
	if maxLines <= 0 {
		maxLines = 100
	}
	return &ringTailWriter{max: maxLines}
}

func (w *ringTailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			line := strings.TrimRight(w.partial.String(), "\r")
			w.buf = append(w.buf, line)
			if len(w.buf) > w.max {
				w.buf = w.buf[len(w.buf)-w.max:]
			}
			w.partial.Reset()
			continue
		}
		w.partial.WriteByte(b)
	}
	return len(p), nil
}

// lines returns a snapshot copy of the captured tail. Any in-flight
// partial line (no newline yet) is included as the last entry.
func (w *ringTailWriter) lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.buf)+1)
	out = append(out, w.buf...)
	if w.partial.Len() > 0 {
		out = append(out, strings.TrimRight(w.partial.String(), "\r"))
	}
	return out
}
