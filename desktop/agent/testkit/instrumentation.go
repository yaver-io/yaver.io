package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chromedp/cdproto/log"
	cdpnetwork "github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Browser instrumentation that paid SaaS tools charge for.
//
// This file collects the stuff Playwright / Cypress / Percy / Sentry
// bundle into their reports:
//
//   - Console errors + warnings (JS logger feed)
//   - Page errors (unhandled exceptions)
//   - Network requests (URL, method, status, timing, size)
//   - Performance timings (DOMContentLoaded, load, LCP, CLS)
//
// Everything is collected over chromedp's CDP subscription hooks —
// no Node runtime, no external instrumentation layer, no token you
// pay for. The runner turns these on per-spec based on the spec's
// `capture:` block and dumps the results to the normal artifacts
// directory at the end of the run so the mobile / desktop / web UI
// can render them inline with the failure.

// CaptureConfig turns collection on/off. The runner pulls this off
// the Spec during execution.
type CaptureConfig struct {
	ConsoleErrors  bool `yaml:"console_errors,omitempty"`
	NetworkRequests bool `yaml:"network,omitempty"`
	Performance    bool `yaml:"performance,omitempty"`
	Accessibility  bool `yaml:"accessibility,omitempty"`
}

// InstrumentationState is the runner-scoped collection bucket. One
// per spec run; written to disk on completion.
type InstrumentationState struct {
	mu        sync.Mutex
	Console   []ConsoleEvent   `json:"console"`
	PageErrs  []string         `json:"page_errors"`
	Requests  []NetworkEvent   `json:"network_requests"`
	Perf      PerformanceMetrics `json:"performance"`
}

// ConsoleEvent captures one console.log/warn/error line from the page.
type ConsoleEvent struct {
	Level   string    `json:"level"`
	Message string    `json:"message"`
	URL     string    `json:"url,omitempty"`
	Line    int       `json:"line,omitempty"`
	At      time.Time `json:"at"`
}

// NetworkEvent is a minimal request/response record.
type NetworkEvent struct {
	RequestID string    `json:"request_id"`
	Method    string    `json:"method"`
	URL       string    `json:"url"`
	Status    int       `json:"status"`
	StatusText string   `json:"status_text,omitempty"`
	Type      string    `json:"type"`
	SizeBytes int64     `json:"size_bytes"`
	DurationMS int64    `json:"duration_ms"`
	StartedAt time.Time `json:"started_at"`
}

// PerformanceMetrics captures Core Web Vitals-ish numbers from the
// browser's performance API.
type PerformanceMetrics struct {
	DOMContentLoadedMS int64 `json:"dom_content_loaded_ms"`
	LoadMS             int64 `json:"load_ms"`
	FirstPaintMS       int64 `json:"first_paint_ms"`
	FirstContentfulPaintMS int64 `json:"first_contentful_paint_ms"`
	// LCP/CLS require a PerformanceObserver, so we only capture them
	// when the instrumentation ran long enough for the browser to
	// report them.
	LargestContentfulPaintMS int64 `json:"largest_contentful_paint_ms,omitempty"`
	CumulativeLayoutShift    float64 `json:"cumulative_layout_shift,omitempty"`
}

// InstallInstrumentation wires chromedp listeners into a browser
// context so the state buckets fill up as the spec runs. Safe to
// call even if some capture flags are off — each listener checks
// its own flag before recording.
//
// The runner calls this once right after the browser context is
// created, then calls FinalizeInstrumentation at the end of the
// spec to pull the performance metrics out of the page.
func InstallInstrumentation(ctx context.Context, cfg CaptureConfig) *InstrumentationState {
	state := &InstrumentationState{}
	startTimes := map[cdpnetwork.RequestID]time.Time{}

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			if !cfg.ConsoleErrors {
				return
			}
			// Only surface warn/error — info/debug is too noisy.
			level := string(e.Type)
			if level != "warning" && level != "error" && level != "assert" {
				return
			}
			msg := ""
			for _, a := range e.Args {
				if a.Value != nil {
					msg += string(a.Value) + " "
				} else if a.Description != "" {
					msg += a.Description + " "
				}
			}
			state.mu.Lock()
			state.Console = append(state.Console, ConsoleEvent{
				Level:   level,
				Message: msg,
				At:      time.Now(),
			})
			state.mu.Unlock()

		case *runtime.EventExceptionThrown:
			if !cfg.ConsoleErrors {
				return
			}
			state.mu.Lock()
			msg := "(unknown)"
			if e.ExceptionDetails != nil {
				msg = e.ExceptionDetails.Text
				if e.ExceptionDetails.Exception != nil && e.ExceptionDetails.Exception.Description != "" {
					msg = e.ExceptionDetails.Exception.Description
				}
			}
			state.PageErrs = append(state.PageErrs, msg)
			state.mu.Unlock()

		case *log.EventEntryAdded:
			// Browser-level (security, deprecation) log entries.
			if !cfg.ConsoleErrors {
				return
			}
			entry := e.Entry
			if entry == nil || entry.Level != log.LevelError {
				return
			}
			state.mu.Lock()
			state.Console = append(state.Console, ConsoleEvent{
				Level:   string(entry.Level),
				Message: entry.Text,
				URL:     entry.URL,
				Line:    int(entry.LineNumber),
				At:      time.Now(),
			})
			state.mu.Unlock()

		case *cdpnetwork.EventRequestWillBeSent:
			if !cfg.NetworkRequests {
				return
			}
			startTimes[e.RequestID] = time.Now()
			state.mu.Lock()
			state.Requests = append(state.Requests, NetworkEvent{
				RequestID: string(e.RequestID),
				Method:    e.Request.Method,
				URL:       e.Request.URL,
				Type:      string(e.Type),
				StartedAt: time.Now(),
			})
			state.mu.Unlock()

		case *cdpnetwork.EventResponseReceived:
			if !cfg.NetworkRequests {
				return
			}
			state.mu.Lock()
			for i := range state.Requests {
				if state.Requests[i].RequestID != string(e.RequestID) {
					continue
				}
				state.Requests[i].Status = int(e.Response.Status)
				state.Requests[i].StatusText = e.Response.StatusText
				if started, ok := startTimes[e.RequestID]; ok {
					state.Requests[i].DurationMS = time.Since(started).Milliseconds()
				}
				break
			}
			state.mu.Unlock()

		case *cdpnetwork.EventLoadingFinished:
			if !cfg.NetworkRequests {
				return
			}
			state.mu.Lock()
			for i := range state.Requests {
				if state.Requests[i].RequestID != string(e.RequestID) {
					continue
				}
				state.Requests[i].SizeBytes = int64(e.EncodedDataLength)
				if started, ok := startTimes[e.RequestID]; ok && state.Requests[i].DurationMS == 0 {
					state.Requests[i].DurationMS = time.Since(started).Milliseconds()
				}
				break
			}
			state.mu.Unlock()
		}
	})

	// Enable the CDP domains we just subscribed to. Each one is
	// idempotent and cheap.
	_ = chromedp.Run(ctx,
		runtime.Enable(),
		log.Enable(),
		cdpnetwork.Enable(),
	)

	return state
}

// FinalizeInstrumentation pulls the page performance API values out
// of the tab at the end of the run. Called once per spec after the
// last step fires.
func FinalizeInstrumentation(ctx context.Context, state *InstrumentationState, cfg CaptureConfig) {
	if state == nil || !cfg.Performance {
		return
	}
	var raw string
	err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
		try {
			const t = performance.timing || {};
			const navStart = t.navigationStart || 0;
			const paints = performance.getEntriesByType ? performance.getEntriesByType('paint') : [];
			const lcpEntries = performance.getEntriesByType ? performance.getEntriesByType('largest-contentful-paint') : [];
			const first = (n) => {
				const p = paints.find((p) => p.name === n);
				return p ? Math.round(p.startTime) : 0;
			};
			return JSON.stringify({
				dcl: t.domContentLoadedEventEnd ? (t.domContentLoadedEventEnd - navStart) : 0,
				load: t.loadEventEnd ? (t.loadEventEnd - navStart) : 0,
				firstPaint: first('first-paint'),
				firstContentfulPaint: first('first-contentful-paint'),
				lcp: lcpEntries.length ? Math.round(lcpEntries[lcpEntries.length-1].startTime) : 0,
			});
		} catch (e) {
			return JSON.stringify({error: String(e)});
		}
	})()`, &raw))
	if err != nil {
		return
	}
	var parsed struct {
		DCL   int64 `json:"dcl"`
		Load  int64 `json:"load"`
		FP    int64 `json:"firstPaint"`
		FCP   int64 `json:"firstContentfulPaint"`
		LCP   int64 `json:"lcp"`
	}
	_ = json.Unmarshal([]byte(raw), &parsed)
	state.mu.Lock()
	state.Perf.DOMContentLoadedMS = parsed.DCL
	state.Perf.LoadMS = parsed.Load
	state.Perf.FirstPaintMS = parsed.FP
	state.Perf.FirstContentfulPaintMS = parsed.FCP
	state.Perf.LargestContentfulPaintMS = parsed.LCP
	state.mu.Unlock()
}

// WriteInstrumentation serializes the state as JSON under the spec's
// artifact directory so the mobile / desktop / web UI can render it.
func WriteInstrumentation(state *InstrumentationState, artifactDir string) (string, error) {
	if state == nil {
		return "", nil
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(artifactDir, "instrumentation.json")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	state.mu.Lock()
	defer state.mu.Unlock()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		return path, err
	}
	return path, nil
}

// ConsoleErrorCount returns how many page errors + warning/error
// console events were recorded. Used by the runner to auto-fail a
// spec when `capture.console_errors` is set and any error fires.
func (s *InstrumentationState) ConsoleErrorCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.PageErrs)
	for _, c := range s.Console {
		if c.Level == "error" || c.Level == "assert" {
			n++
		}
	}
	return n
}

// SummaryLine produces a one-line human string for TTY reporters:
// "3 console errors, 12 requests (1 4xx), LCP 450ms"
func (s *InstrumentationState) SummaryLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	errs := 0
	for _, c := range s.Console {
		if c.Level == "error" || c.Level == "assert" {
			errs++
		}
	}
	errs += len(s.PageErrs)
	n4xx := 0
	for _, r := range s.Requests {
		if r.Status >= 400 && r.Status < 500 {
			n4xx++
		}
	}
	parts := []string{}
	if errs > 0 {
		parts = append(parts, fmt.Sprintf("%d console errors", errs))
	}
	if len(s.Requests) > 0 {
		line := fmt.Sprintf("%d requests", len(s.Requests))
		if n4xx > 0 {
			line += fmt.Sprintf(" (%d 4xx)", n4xx)
		}
		parts = append(parts, line)
	}
	if s.Perf.LargestContentfulPaintMS > 0 {
		parts = append(parts, fmt.Sprintf("LCP %dms", s.Perf.LargestContentfulPaintMS))
	}
	return joinComma(parts)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
