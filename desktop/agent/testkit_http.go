package main

// HTTP handlers that expose the embedded yaver-test-sdk runner to the
// mobile app over the existing authenticated transport. Everything
// here is local-first: results live on the dev's machine, the mobile
// app pulls them via P2P, and no Convex / no central server is in the
// path.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// testkitState holds the runner's mutable state shared across HTTP
// requests. There's at most one active run at a time per agent — solo
// dev rarely wants two suites stomping on each other's Chromium
// instances. The mobile app polls /testkit/run for status.
type testkitState struct {
	mu        sync.Mutex
	root      string // last spec root the user pointed at, e.g. ./yaver-tests
	running   bool
	startedAt time.Time
	lastSuite *testkit.Suite
}

var testkitGlobal testkitState

// resolveSpecRoot maps a request payload (or default) to an absolute
// directory under the agent's working dir. Empty body → ./yaver-tests.
func resolveSpecRoot(reqRoot string) (string, error) {
	root := reqRoot
	if root == "" {
		root = "yaver-tests"
	}
	return filepath.Abs(root)
}

// handleTestkitListSpecs returns the parsed list of specs the runner
// would execute, so the mobile app can show "10 specs found" before
// the user kicks anything off.
func (s *HTTPServer) handleTestkitListSpecs(w http.ResponseWriter, r *http.Request) {
	root, err := resolveSpecRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	specs, err := testkit.DiscoverSpecs(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	type specView struct {
		Name      string         `json:"name"`
		Path      string         `json:"path"`
		Target    testkit.Target `json:"target"`
		URL       string         `json:"url,omitempty"`
		StepCount int            `json:"step_count"`
	}
	out := struct {
		Root  string     `json:"root"`
		Specs []specView `json:"specs"`
	}{
		Root: root,
	}
	for _, sp := range specs {
		out.Specs = append(out.Specs, specView{
			Name:      sp.Name,
			Path:      sp.Path,
			Target:    sp.Target,
			URL:       sp.URL,
			StepCount: len(sp.Steps),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleTestkitRun is GET (status) or POST (start a new run).
//
//   - GET  /testkit/run                → current state + last suite
//   - POST /testkit/run                → start a run, returns 202
//
// Request body for POST:
//
//	{
//	  "root":            "./yaver-tests",
//	  "concurrency":     2,
//	  "retries":         1,
//	  "headful":         false,
//	  "update_snapshots":false,
//	  "ac_power_only":   true
//	}
func (s *HTTPServer) handleTestkitRun(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		testkitGlobal.mu.Lock()
		state := struct {
			Running   bool           `json:"running"`
			Root      string         `json:"root"`
			StartedAt time.Time      `json:"started_at,omitempty"`
			LastSuite *testkit.Suite `json:"last_suite,omitempty"`
		}{
			Running:   testkitGlobal.running,
			Root:      testkitGlobal.root,
			StartedAt: testkitGlobal.startedAt,
			LastSuite: testkitGlobal.lastSuite,
		}
		testkitGlobal.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
		return

	case http.MethodPost:
		var body struct {
			Root            string  `json:"root"`
			Concurrency     int     `json:"concurrency"`
			Retries         int     `json:"retries"`
			Headful         bool    `json:"headful"`
			UpdateSnapshots bool    `json:"update_snapshots"`
			ACPowerOnly     bool    `json:"ac_power_only"`
			MaxLoad         float64 `json:"max_load"`
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf("bad json: %v", err), http.StatusBadRequest)
				return
			}
		}
		root, err := resolveSpecRoot(body.Root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		specs, err := testkit.DiscoverSpecs(root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if len(specs) == 0 {
			http.Error(w, "no specs found", http.StatusNotFound)
			return
		}

		testkitGlobal.mu.Lock()
		if testkitGlobal.running {
			testkitGlobal.mu.Unlock()
			http.Error(w, "another run is already in progress", http.StatusConflict)
			return
		}
		testkitGlobal.running = true
		testkitGlobal.startedAt = time.Now()
		testkitGlobal.root = root
		testkitGlobal.mu.Unlock()

		// Hardware-aware: if the dev asked us to skip on battery and
		// they're on battery, refuse cleanly so the mobile app shows a
		// "skipped: on battery" message instead of doing nothing.
		if body.ACPowerOnly || body.MaxLoad > 0 {
			hs := testkit.SnapshotHost()
			if ok, why := testkit.ShouldRun(hs, body.ACPowerOnly, body.MaxLoad); !ok {
				testkitGlobal.mu.Lock()
				testkitGlobal.running = false
				testkitGlobal.mu.Unlock()
				http.Error(w, "skipped: "+why, http.StatusServiceUnavailable)
				return
			}
		}

		opts := testkit.RunOptions{
			Headful:      body.Headful,
			VerboseLog:   false,
			FlakeRetries: body.Retries,
		}
		if body.UpdateSnapshots {
			opts.Snapshot.Mode = testkit.SnapshotModeUpdate
		}
		conc := body.Concurrency
		if conc < 1 {
			conc = 1
		}

		// Kick the run off in a goroutine; the mobile app polls /run.
		go func() {
			// Background context — long-running. Bound to ~30 min so
			// nothing leaks if a spec hangs.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			suite := testkit.RunSuite(ctx, specs, opts, conc)
			// Append to local history (P2P only — never sent anywhere).
			hist := &testkit.History{Path: testkit.HistoryPathFor(root)}
			_ = hist.AppendSuite(suite, "", "", runtime.GOOS)
			// Failure-only notifications: write into the local center
			// so the mobile app sees them next poll, and fire the
			// optional webhook for users who configured one.
			nc := testkit.NewNotificationCenter(testkit.NotificationsPathFor(root), 100)
			testkit.PublishSuiteFailures(nc, suite, "", "")
			testkitGlobal.mu.Lock()
			testkitGlobal.lastSuite = suite
			testkitGlobal.running = false
			testkitGlobal.mu.Unlock()
		}()

		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"started":     true,
			"specs":       len(specs),
			"concurrency": conc,
			"root":        root,
		})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// handleTestkitHistory returns the most recent N entries from the
// project's local .history.jsonl. Mobile "Runs" tab uses this.
func (s *HTTPServer) handleTestkitHistory(w http.ResponseWriter, r *http.Request) {
	root, err := resolveSpecRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hist := &testkit.History{Path: testkit.HistoryPathFor(root)}
	entries, err := hist.Tail(50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": entries,
	})
}

// handleTestkitNotifications returns the local notification stream
// (failure-only). Mobile app polls this every few seconds. Source of
// truth is `<spec>/.yaver-test-results/notifications.jsonl` — fully
// local, no third-party push provider involved.
func (s *HTTPServer) handleTestkitNotifications(w http.ResponseWriter, r *http.Request) {
	root, err := resolveSpecRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nc := testkit.NewNotificationCenter(testkit.NotificationsPathFor(root), 100)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"notifications": nc.List(50),
	})
}

// handleTestkitArtifact serves a screenshot, trace, or video frame
// from the on-disk artifact tree. The path query param must resolve
// to a file inside `<root>/.yaver-test-results/` or
// `<root>/snapshots/`; anything else returns 404 to prevent the
// mobile app (or anything else with a token) from reading arbitrary
// files. We never serve from outside the project's spec dir.
func (s *HTTPServer) handleTestkitArtifact(w http.ResponseWriter, r *http.Request) {
	root, err := resolveSpecRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	// Resolve. The on-disk artifact paths the runner records are
	// already absolute, but we accept either form.
	target := rel
	if !filepath.IsAbs(rel) {
		target = filepath.Join(root, rel)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	// Path containment check — abs must be inside one of the allowed
	// subtrees of the spec root.
	allowed := false
	for _, base := range []string{
		filepath.Join(root, ".yaver-test-results"),
		filepath.Join(root, "snapshots"),
	} {
		if strings.HasPrefix(abs+string(filepath.Separator), base+string(filepath.Separator)) || abs == base {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, abs)
}

// handleTestkitMarkers exposes the local pass markers so the mobile
// app's "Local CI" tab can show "✓ this SHA already passed locally."
func (s *HTTPServer) handleTestkitMarkers(w http.ResponseWriter, r *http.Request) {
	root, err := resolveSpecRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	markers, err := testkit.LatestPassMarkers(root, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"markers": markers,
	})
}

// handleTestkitFlake returns the per-spec failure ratios over the last
// 100 runs. Solo dev's "which test is being annoying right now" view.
func (s *HTTPServer) handleTestkitFlake(w http.ResponseWriter, r *http.Request) {
	root, err := resolveSpecRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hist := &testkit.History{Path: testkit.HistoryPathFor(root)}
	stats, err := hist.FlakeReport(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"stats": stats,
	})
}
