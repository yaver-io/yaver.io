package main

import (
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// agentUpdateStreamRef is set by HTTPServer at boot to a LogStream
// named "agent-update". checkAutoUpdate / runForcedAgentUpdate publish
// progress lines to it via emitAgentUpdate(...) so the dashboard +
// mobile can subscribe to /streams/agent-update and watch the
// download / extract / replace / restart phases live.
//
// Why a package-level pointer rather than threading the stream
// through every call site: checkAutoUpdate is invoked from many
// places (boot path, periodic ticker, /agent/update POST, manual CLI
// invocations) and adding a parameter to all of them is churn for
// no benefit. The atomic pointer is set once in NewHTTPServer and
// never mutated afterwards.
var agentUpdateStreamRef atomic.Pointer[LogStream]

func setAgentUpdateStream(s *LogStream) {
	agentUpdateStreamRef.Store(s)
}

// emitAgentUpdate publishes a progress line both to the daemon log
// (so terminal viewers / journalctl tails still see the same info)
// and to the SSE stream the dashboard reads. Phase strings are kept
// short and machine-friendly ("queued", "fetch_release", "check",
// "download", "extract", "replace", "restart", "ready", "error") so
// the UI can drive a progress bar off them — the human-readable
// detail goes in `text`.
func emitAgentUpdate(phase, format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	log.Printf("[auto-update:%s] %s", phase, text)
	if s := agentUpdateStreamRef.Load(); s != nil {
		s.AppendEvent(map[string]interface{}{
			"type":  "progress",
			"phase": phase,
			"text":  text,
		})
	}
}

// emitAgentUpdateProgress is the byte-counted variant — same SSE
// stream, but carries `bytes` and `total` so the dashboard can
// render a real progress bar. `total` is -1 when the upstream
// didn't send a Content-Length header.
func emitAgentUpdateProgress(phase string, bytes, total int64, text string) {
	if s := agentUpdateStreamRef.Load(); s != nil {
		s.AppendEvent(map[string]interface{}{
			"type":  "progress",
			"phase": phase,
			"text":  text,
			"bytes": bytes,
			"total": total,
		})
	}
}

// agentUpdateProgressReader wraps an io.Reader and periodically
// emits download progress events. Throttled at ~200ms or every 5%
// to avoid spamming the SSE stream on a fast link. The final
// event (with bytes == total when total is known) is fired by
// flush() — call it from a defer in the caller so a download
// that fails midway still gets a "stalled at 67%" final reading.
type agentUpdateProgressReader struct {
	r            io.Reader
	total        int64
	phase        string
	mu           sync.Mutex
	read         int64
	lastEmitAt   time.Time
	lastEmitPct  int
	flushed      bool
}

func newAgentUpdateProgressReader(r io.Reader, total int64, phase string) *agentUpdateProgressReader {
	return &agentUpdateProgressReader{r: r, total: total, phase: phase}
}

func (p *agentUpdateProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.mu.Lock()
		p.read += int64(n)
		shouldEmit := false
		now := time.Now()
		if p.lastEmitAt.IsZero() || now.Sub(p.lastEmitAt) >= 200*time.Millisecond {
			shouldEmit = true
		}
		if p.total > 0 {
			pct := int((p.read * 100) / p.total)
			if pct >= p.lastEmitPct+5 {
				shouldEmit = true
			}
			if pct >= 100 {
				shouldEmit = true
			}
		}
		read := p.read
		total := p.total
		if shouldEmit {
			p.lastEmitAt = now
			if total > 0 {
				p.lastEmitPct = int((read * 100) / total)
			}
		}
		p.mu.Unlock()
		if shouldEmit {
			emitAgentUpdateProgress(p.phase, read, total, formatBytesProgress(read, total))
		}
	}
	return n, err
}

// flush forces a final progress event so the dashboard always sees
// the terminal byte count, even if the read loop exited mid-stream
// with an error. Idempotent — calling flush twice is harmless.
func (p *agentUpdateProgressReader) flush() {
	p.mu.Lock()
	if p.flushed {
		p.mu.Unlock()
		return
	}
	p.flushed = true
	read := p.read
	total := p.total
	p.mu.Unlock()
	emitAgentUpdateProgress(p.phase, read, total, formatBytesProgress(read, total))
}

func formatBytesProgress(read, total int64) string {
	if total > 0 {
		return fmt.Sprintf("Downloaded %s / %s (%d%%)", humanBytes(read), humanBytes(total), int((read*100)/total))
	}
	return fmt.Sprintf("Downloaded %s", humanBytes(read))
}
