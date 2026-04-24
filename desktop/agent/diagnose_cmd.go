package main

// diagnose_cmd.go — CLI + HTTP + MCP wrappers for RunDiagnose.
//
// Usage:
//   yaver diagnose                         # streaming text
//   yaver diagnose --json                  # newline-delimited JSON
//   yaver diagnose --only=binary-paths,... # narrow
//   yaver diagnose --skip=runtime-deps     # subtract
//   yaver diagnose --fix                   # opt-in auto-fix for safe checks
//
// HTTP surface (owner-auth):
//   GET /diagnose/stream                   # SSE events
//   POST /diagnose                         # { only?, skip?, fix? } → final report
//
// Mobile + web subscribe to /diagnose/stream via EventSource /
// react-native-sse.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runDiagnoseCLI is the `yaver diagnose` subcommand entry.
func runDiagnoseCLI(args []string) {
	fs := flag.NewFlagSet("diagnose", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Emit newline-delimited JSON events")
	only := fs.String("only", "", "Comma-separated list of checks to run (overrides the default set)")
	skip := fs.String("skip", "", "Comma-separated checks to skip")
	fix := fs.Bool("fix", false, "Apply safe auto-fixes (symlink /usr/bin/yaver when multiple binaries exist, ...)")
	fs.Parse(args)

	opts := DiagnoseOptions{
		Fix: *fix,
	}
	if *only != "" {
		opts.Only = diagSplitCSV(*only)
	}
	if *skip != "" {
		opts.Skip = diagSplitCSV(*skip)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	emit := func(ev DiagEvent) {
		if *jsonOut {
			WriteDiagnoseJSONLines(os.Stdout, ev)
			return
		}
		renderDiagEventText(os.Stdout, ev)
	}

	report := RunDiagnose(ctx, opts, emit)
	if !*jsonOut {
		fmt.Printf("\nSummary: %d ok · %d info · %d warning · %d failure\n", report.OK, report.Info, report.Warnings, report.Failures)
	}
	if report.Failures > 0 {
		os.Exit(2)
	}
}

func renderDiagEventText(w *os.File, ev DiagEvent) {
	switch ev.Type {
	case "start":
		fmt.Fprintln(w, "yaver diagnose —", ev.Timestamp)
	case "check_start":
		fmt.Fprintf(w, "\n[%s]\n", ev.Check)
	case "finding":
		glyph := "·"
		switch ev.Severity {
		case DiagOK:
			glyph = "✓"
		case DiagWarning:
			glyph = "!"
		case DiagFailure:
			glyph = "✗"
		case DiagInfo:
			glyph = "·"
		}
		fmt.Fprintf(w, "  %s %s\n", glyph, ev.Message)
	case "check_end":
		// no-op; the next check_start prints its own header
	case "done":
		// printed by the caller (it has access to the report)
	}
}

func diagSplitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ─── HTTP ──────────────────────────────────────────────────────────

// handleDiagnose (POST /diagnose) runs the diagnostic set in-process
// and returns the final report. JSON request body:
//
//	{ "only": [...], "skip": [...], "fix": bool }
func (s *HTTPServer) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Only []string `json:"only,omitempty"`
		Skip []string `json:"skip,omitempty"`
		Fix  bool     `json:"fix,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	events := make([]DiagEvent, 0, 64)
	report := RunDiagnose(ctx, DiagnoseOptions{
		Only:  req.Only,
		Skip:  req.Skip,
		Fix:   req.Fix,
		Agent: s,
	}, func(ev DiagEvent) {
		events = append(events, ev)
	})

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     report.Failures == 0,
		"report": report,
		"events": events,
	})
}

// handleDiagnoseStream (GET /diagnose/stream) streams events as
// Server-Sent Events. Mobile + web UIs subscribe via EventSource.
func (s *HTTPServer) handleDiagnoseStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	q := r.URL.Query()
	opts := DiagnoseOptions{
		Fix:   q.Get("fix") == "1" || q.Get("fix") == "true",
		Agent: s,
	}
	if only := q.Get("only"); only != "" {
		opts.Only = diagSplitCSV(only)
	}
	if skip := q.Get("skip"); skip != "" {
		opts.Skip = diagSplitCSV(skip)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	RunDiagnose(ctx, opts, func(ev DiagEvent) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	})
}
