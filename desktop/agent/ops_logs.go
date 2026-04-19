package main

// ops_logs.go — verb "logs": return the snapshot + live-tail contract
// for a named log stream. Agents get:
//   - stream (string): the stream name ready to subscribe to via
//     /streams/<name> SSE for live frames.
//   - snapshot ([]string): most-recent ring-buffer lines available
//     immediately so the agent can contextualise before it bothers
//     subscribing.
//
// Named streams are populated by long-running ops (autodev, install,
// build, deploy, anything using teeStdoutToStream). An agent that
// wants a generic "last N log lines" uses this verb; an agent that
// wants per-provider logs (docker, vercel, convex, k8s, fly, ...) keeps
// using the domain-specific MCP tools.

import (
	"encoding/json"
)

type opsLogsPayload struct {
	// Op: "list" (all stream names) | "snapshot" (ring buffer for one) |
	// "subscribe" (return stream id for SSE follow-up). Defaults to snapshot.
	Op string `json:"op,omitempty"`
	// Stream: required for snapshot + subscribe.
	Stream string `json:"stream,omitempty"`
	// TailLines: cap the snapshot at this many lines. 0 = all buffered.
	TailLines int `json:"tailLines,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "logs",
		Description: "Read a named in-memory log stream. op=list (streams), op=snapshot (recent lines), op=subscribe (streamId for SSE follow-up). Streams are populated by long-running verbs (build/deploy/install/autodev).",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"op":        map[string]interface{}{"type": "string", "enum": []string{"list", "snapshot", "subscribe"}, "default": "snapshot"},
				"stream":    map[string]interface{}{"type": "string"},
				"tailLines": map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsLogsHandler,
		Streaming:  true,
		AllowGuest: false, // logs can contain owner-only secrets/PII
	})
}

func opsLogsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsLogsPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	op := p.Op
	if op == "" {
		op = "snapshot"
	}
	if c.Server == nil || c.Server.streams == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "log stream registry not initialised"}
	}

	// snapshotFor returns the buffered history for a named stream by
	// briefly subscribing + canceling. This avoids exposing the internal
	// list.List in the registry API and keeps the stream's ring-buffer
	// implementation opaque to the ops layer.
	snapshotFor := func(name string) []string {
		stream := c.Server.streams.Get(name)
		_, hist, cancel := stream.Subscribe()
		cancel()
		return hist
	}

	switch op {
	case "list":
		names := c.Server.streams.Names()
		return OpsResult{OK: true, Initial: map[string]interface{}{"count": len(names), "streams": names}}

	case "snapshot":
		if p.Stream == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "stream name required"}
		}
		snap := snapshotFor(p.Stream)
		if p.TailLines > 0 && len(snap) > p.TailLines {
			snap = snap[len(snap)-p.TailLines:]
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"stream":    p.Stream,
			"lines":     snap,
			"lineCount": len(snap),
		}}

	case "subscribe":
		if p.Stream == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "stream name required"}
		}
		// Agents subscribe via existing /streams/<name> SSE endpoint.
		// This verb hands back the stream id + the initial snapshot
		// so the agent doesn't have to do a separate GET first.
		snap := snapshotFor(p.Stream)
		return OpsResult{
			OK:       true,
			StreamID: p.Stream,
			Initial: map[string]interface{}{
				"stream":        p.Stream,
				"sseUrl":        "/streams/" + p.Stream,
				"initialLines":  snap,
				"subscribeHint": "follow the sseUrl with the same owner token; each line event is one log line",
			},
		}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + op}
	}
}
