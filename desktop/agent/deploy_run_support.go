package main

// deploy_run_support.go — supporting pieces for /deploy/ship:
//
//   1. deployLimiter — per-user in-flight cap so a misbehaving guest
//      can't kick off dozens of parallel xcodebuild runs. Owner calls
//      use the empty-string key and get a higher cap.
//   2. HTTP handlers for /deploy/runs (list) and /deploy/runs/{id}
//      (detail). Guest-aware: a guest only sees their own runs.
//   3. ensureDeployHistory / ensureDeployLimiter — lazy init so a
//      server built in a test doesn't have to remember to allocate
//      them.

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

// openDeployLog is a tiny wrapper for os.Open — split out so tests
// can stub file access if they ever need to.
func openDeployLog(path string) (*os.File, error) { return os.Open(path) }

// deployShipLimits is the default concurrency cap table.
var deployShipLimits = struct {
	Owner int
	Guest int
}{Owner: 8, Guest: 2}

// deployLimiter tracks per-caller in-flight deploys. "" is the owner.
type deployLimiter struct {
	mu       sync.Mutex
	inFlight map[string]int
}

func newDeployLimiter() *deployLimiter {
	return &deployLimiter{inFlight: map[string]int{}}
}

// tryAcquire atomically increments the counter for key if under max.
// Returns true on success; false means the caller is at their cap.
func (l *deployLimiter) tryAcquire(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] >= max {
		return false
	}
	l.inFlight[key]++
	return true
}

// release decrements. Safe to call if acquire wasn't called (no-op
// when already at zero — the defer pattern benefits).
func (l *deployLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] > 0 {
		l.inFlight[key]--
	}
}

// ensureDeployHistory returns s.deployHistory, creating it on first
// call. Guards every handler path so tests that build an HTTPServer
// literal don't need to initialise it manually.
func (s *HTTPServer) ensureDeployHistory() *DeployHistory {
	if s.deployHistory == nil {
		s.deployHistory = NewDeployHistory(100)
	}
	return s.deployHistory
}

func (s *HTTPServer) ensureDeployLimiter() *deployLimiter {
	if s.deployLimiter == nil {
		s.deployLimiter = newDeployLimiter()
	}
	return s.deployLimiter
}

// guestFilterForRequest returns ("guestUID") when the caller is a
// guest and ("") when the caller is the owner. Used by the history
// endpoints to hide other users' runs.
func guestFilterForRequest(r *http.Request) string {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		return r.Header.Get("X-Yaver-GuestUserID")
	}
	return ""
}

// handleDeployRuns: GET /deploy/runs[?limit=N]
//
// Response: { "runs": [ DeployRun, ... ] }
//
// Guests only see runs they themselves initiated. Owner sees all.
func (s *HTTPServer) handleDeployRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	guestFilter := guestFilterForRequest(r)
	runs := s.ensureDeployHistory().List(limit, guestFilter)
	// Elide OutputTail from list responses — the detail endpoint
	// gives you that. Keeps a `list` cheap and prevents accidental
	// 8 KB × N blow-ups.
	for i := range runs {
		runs[i].OutputTail = ""
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"runs":  runs,
		"count": len(runs),
	})
}

// handleDeployRunDetail: GET /deploy/runs/{id} or /deploy/runs/{id}/output
//
// Returns a single DeployRun including its OutputTail, or (when the
// path ends in /output) streams the full on-disk log as text/plain.
// Guests get 404 for runs they didn't initiate (indistinguishable
// from absent, deliberately).
func (s *HTTPServer) handleDeployRunDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/deploy/runs/")
	id := path
	wantOutput := false
	if idx := strings.Index(path, "/"); idx >= 0 {
		id = path[:idx]
		suffix := strings.TrimPrefix(path[idx:], "/")
		if suffix == "output" {
			wantOutput = true
		} else {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": "unknown sub-resource: " + suffix,
			})
			return
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing run id"})
		return
	}
	guestFilter := guestFilterForRequest(r)
	run, ok := s.ensureDeployHistory().Get(id, guestFilter)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	if !wantOutput {
		// Don't leak the on-disk path to clients — it contains $HOME
		// which is the user's identity. The log_bytes count is enough
		// for a UI to decide whether to offer "download full log".
		run.LogPath = ""
		jsonReply(w, http.StatusOK, run)
		return
	}
	// Stream the full log. Large file, text/plain, no buffering.
	s.streamDeployRunOutput(w, r, run)
}

// streamDeployRunOutput sends the on-disk full output log for a run
// as text/plain. When the run's log is absent (disk persistence was
// off, or the run pre-dated this feature) we fall back to the 8 KB
// in-memory tail so callers always get something useful.
func (s *HTTPServer) streamDeployRunOutput(w http.ResponseWriter, r *http.Request, run DeployRun) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	if run.LogPath == "" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(run.OutputTail))
		return
	}
	f, err := openDeployLog(run.LogPath)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(run.OutputTail))
		return
	}
	defer f.Close()
	w.WriteHeader(http.StatusOK)
	// Chunked streaming so a 50 MB log doesn't buffer in memory.
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
