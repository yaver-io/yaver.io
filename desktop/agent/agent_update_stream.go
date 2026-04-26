package main

import (
	"fmt"
	"log"
	"sync/atomic"
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
// short and machine-friendly ("download", "extract", "replace",
// "restart", "ready", "error") so the UI can drive a progress bar
// off them — the human-readable detail goes in `text`.
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
