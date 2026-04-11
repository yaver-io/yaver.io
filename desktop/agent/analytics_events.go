package main

// analytics_events.go — A1 — BlackBox track() ingest channel.
//
// The Feedback SDK already streams logs / errors / navigation /
// network / render events to the agent as BlackBoxEvent records.
// This file adds a fourth lane: arbitrary `track(event, props)`
// business events that the dev explicitly emits from their app
// code. Think "purchase_completed", "signup_started", "upsell_shown".
//
// Design rules:
//
//  1. Zero dashboards. The agent is not a PostHog / Mixpanel
//     replacement — it's an ingest + export tunnel so the dev
//     owns the data. Export paths:
//        - `/analytics/events?since=<ts>&limit=N` JSON tail
//        - `/analytics/events.csv` full CSV dump
//     and that's it. Any real analytics happen in whatever
//     downstream tool the dev points at the CSV or webhook.
//  2. All writes are local to ~/.yaver/analytics/events.jsonl.
//     Newline-delimited JSON so grep / jq / tail -f all work.
//  3. Ring bound: after 10k events we rotate events.jsonl to
//     events.jsonl.old. Two files max. Solo dev shipping a
//     moderate app for months fits comfortably.
//  4. Ingestion happens from the same BlackBox PushEvent path —
//     events of type "track" funnel into AnalyticsAppend here
//     instead of (or in addition to) the per-device ring.

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TrackEvent is one business-event record as it lives on disk.
type TrackEvent struct {
	Name      string            `json:"name"`
	DeviceID  string            `json:"deviceId,omitempty"`
	Timestamp int64             `json:"timestamp"`
	Route     string            `json:"route,omitempty"`
	Props     map[string]string `json:"props,omitempty"`
}

var (
	analyticsMu    sync.Mutex
	analyticsCache []TrackEvent // last 1000 for fast /analytics/events tail reads
	analyticsMax   = 10000
)

func analyticsPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "analytics")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "events.jsonl"), nil
}

// AnalyticsAppend writes one track event to the jsonl ledger and
// updates the in-memory tail cache. Safe to call from the
// BlackBox push goroutine.
func AnalyticsAppend(ev TrackEvent) {
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	p, err := analyticsPath()
	if err != nil {
		return
	}
	analyticsMu.Lock()
	defer analyticsMu.Unlock()

	// Rotate if the file is over analyticsMax lines. Use file
	// size heuristic — 10k typical events ~1MB. Rotate at 4MB.
	if info, serr := os.Stat(p); serr == nil && info.Size() > 4*1024*1024 {
		_ = os.Rename(p, p+".old")
	}
	f, oerr := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if oerr != nil {
		return
	}
	defer f.Close()
	if data, jerr := json.Marshal(ev); jerr == nil {
		f.Write(data)
		f.Write([]byte{'\n'})
	}
	analyticsCache = append(analyticsCache, ev)
	if len(analyticsCache) > 1000 {
		analyticsCache = analyticsCache[len(analyticsCache)-1000:]
	}
}

// analyticsTail returns the most recent events, filtered by
// since (unix-ms) and capped at limit.
func analyticsTail(since int64, limit int) []TrackEvent {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	analyticsMu.Lock()
	defer analyticsMu.Unlock()
	out := make([]TrackEvent, 0, limit)
	for i := len(analyticsCache) - 1; i >= 0; i-- {
		ev := analyticsCache[i]
		if ev.Timestamp < since {
			break
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// handleAnalyticsEvents serves the JSON tail endpoint.
func (s *HTTPServer) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events := analyticsTail(since, limit)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"events": events,
	})
}

// handleAnalyticsCSV streams the full on-disk ledger as CSV for
// import into PostHog / Mixpanel / a spreadsheet. Off-the-record
// analysis only — yaver deliberately ships no dashboards.
func (s *HTTPServer) handleAnalyticsCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	p, err := analyticsPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f, oerr := os.Open(p)
	if oerr != nil {
		if os.IsNotExist(oerr) {
			w.Header().Set("Content-Type", "text/csv")
			w.Write([]byte("name,deviceId,timestamp,route,props\n"))
			return
		}
		http.Error(w, oerr.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="yaver-events.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{"name", "deviceId", "timestamp", "route", "props"})

	dec := json.NewDecoder(f)
	for dec.More() {
		var ev TrackEvent
		if err := dec.Decode(&ev); err != nil {
			continue
		}
		propsJSON, _ := json.Marshal(ev.Props)
		cw.Write([]string{
			ev.Name,
			ev.DeviceID,
			strconv.FormatInt(ev.Timestamp, 10),
			ev.Route,
			string(propsJSON),
		})
	}
}

// handleAnalyticsIngest accepts a POST from the SDK directly.
// Parallel path to the BlackBox funnel — useful for surfaces
// that don't run a long-lived SSE stream (web, server-to-server).
func (s *HTTPServer) handleAnalyticsIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Name     string            `json:"name"`
		DeviceID string            `json:"deviceId,omitempty"`
		Route    string            `json:"route,omitempty"`
		Props    map[string]string `json:"props,omitempty"`
		Timestamp int64            `json:"timestamp,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		jsonError(w, http.StatusBadRequest, "name required")
		return
	}
	AnalyticsAppend(TrackEvent{
		Name:      body.Name,
		DeviceID:  body.DeviceID,
		Route:     body.Route,
		Props:     body.Props,
		Timestamp: body.Timestamp,
	})
	jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true})
}
