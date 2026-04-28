package main

// runner_http.go — HTTP surface for the unified Runner (RUNNER_DEV.md).
//
// Routes (all owner-auth in Phase 1; guest scope tiers added in
// guest_scope.go for read-only + manual-trigger operations):
//
//   GET  /runner/jobs                          List declared jobs
//   POST /runner/jobs                          Create / upsert a job (RunnerJob body)
//   GET  /runner/jobs/{name}                   One job
//   DELETE /runner/jobs/{name}                 Delete a job
//   POST /runner/jobs/{name}/trigger           Manual trigger (synchronous; returns RunnerRun)
//   POST /runner/jobs/{name}/pause             Pause / resume scheduling for a job
//   GET  /runner/jobs/{name}/runs              History for one job
//   GET  /runner/runs                          Cross-job run history
//   GET  /runner/runs/{id}                     One run (with OutputTail)
//   GET  /runner/runs/{id}/log                 Stream the full on-disk log as text/plain
//   GET  /runner/pools                         Capability tags this agent advertises
//
// Concurrency: every triggering path acquires from runnerLimiter
// (owner cap = 8, guest cap = 2). Read-only paths are not limited.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// ensureRunnerStore lazy-allocates the RunnerStore on first use. Same
// pattern as ensureDeployHistory so a test that constructs an
// HTTPServer literal does not need to wire it manually.
func (s *HTTPServer) ensureRunnerStore() *RunnerStore {
	if s.runnerStore == nil {
		s.runnerStore = NewRunnerStore(500)
	}
	return s.runnerStore
}

func (s *HTTPServer) ensureRunnerLimiter() *runnerLimiter {
	if s.runnerLimiter == nil {
		s.runnerLimiter = newRunnerLimiter()
	}
	return s.runnerLimiter
}

// runnerGuestFilter extracts the guest's userID from the request when
// the auth middleware tagged the request as a guest. Owner requests
// return "" — meaning "no filter, see everything."
func runnerGuestFilter(r *http.Request) string {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		return r.Header.Get("X-Yaver-GuestUserID")
	}
	return ""
}

// handleRunnerJobs dispatches GET (list) and POST (upsert) on
// /runner/jobs.
func (s *HTTPServer) handleRunnerJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pool := strings.TrimSpace(r.URL.Query().Get("pool"))
		jobs := s.ensureRunnerStore().ListJobs(pool)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"jobs":  jobs,
			"count": len(jobs),
		})
	case http.MethodPost:
		// Guests are not allowed to create jobs in Phase 1 — only
		// owner can author specs (the trigger endpoint is the guest
		// surface). The auth middleware handles this for the
		// `feedback-only` tier; the explicit check here covers the
		// `runner-submit` future scope.
		if r.Header.Get("X-Yaver-Guest") == "true" {
			jsonReply(w, http.StatusForbidden, map[string]string{
				"error": "guests cannot author runner jobs — owner only",
			})
			return
		}
		var job RunnerJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		stored, err := s.ensureRunnerStore().AddJob(job)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "job": stored})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleRunnerJobByID dispatches /runner/jobs/{name} variants:
// GET, DELETE, POST .../trigger, POST .../pause, GET .../runs.
func (s *HTTPServer) handleRunnerJobByID(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/runner/jobs/")
	tail = strings.TrimSuffix(tail, "/")
	if tail == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing job name"})
		return
	}
	parts := strings.SplitN(tail, "/", 2)
	name := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch sub {
	case "":
		s.handleRunnerJobOne(w, r, name)
	case "trigger":
		s.handleRunnerJobTrigger(w, r, name)
	case "pause":
		s.handleRunnerJobPause(w, r, name)
	case "runs":
		s.handleRunnerJobRuns(w, r, name)
	default:
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource: " + sub})
	}
}

// handleRunnerJobOne — GET / DELETE on /runner/jobs/{name}.
func (s *HTTPServer) handleRunnerJobOne(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		j, ok := s.ensureRunnerStore().GetJob(name)
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "job not found"})
			return
		}
		jsonReply(w, http.StatusOK, j)
	case http.MethodDelete:
		if r.Header.Get("X-Yaver-Guest") == "true" {
			jsonReply(w, http.StatusForbidden, map[string]string{"error": "guests cannot delete runner jobs"})
			return
		}
		if err := s.ensureRunnerStore().RemoveJob(name); err != nil {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleRunnerJobTrigger — POST /runner/jobs/{name}/trigger.
// Synchronous in Phase 1 (the run completes before the response
// returns). Phase 3 promotes this to async-with-streamId via the
// existing /streams/<name> SSE infra.
func (s *HTTPServer) handleRunnerJobTrigger(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	store := s.ensureRunnerStore()
	job, ok := store.GetJob(name)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	caps := LocalCapabilities()
	if !PoolMatches(job.Pool, caps) {
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"error":        "this agent does not match the job's pool",
			"required":     job.Pool,
			"capabilities": caps,
		})
		return
	}

	isGuest := r.Header.Get("X-Yaver-Guest") == "true"
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	if isGuest {
		// Guests can only trigger projects within their allowedProjects.
		if s.guestConfigMgr != nil && job.Project != "" && !s.guestConfigMgr.GuestCanAccessProject(guestUID, job.Project) {
			jsonReply(w, http.StatusForbidden, map[string]string{
				"error": "guest is not authorised for this project",
			})
			return
		}
	}

	// Acquire a concurrency slot.
	limiter := s.ensureRunnerLimiter()
	limiterKey := "owner"
	maxRuns := runnerLimits.Owner
	if isGuest {
		limiterKey = "guest:" + guestUID
		maxRuns = runnerLimits.Guest
	}
	if !limiter.tryAcquire(limiterKey, maxRuns) {
		jsonReply(w, http.StatusTooManyRequests, map[string]interface{}{
			"error": "runner concurrency cap reached — wait for an in-flight run to finish",
			"cap":   maxRuns,
		})
		return
	}
	defer limiter.release(limiterKey)

	// Phase 1 only honours shell jobs; future kinds register here.
	if job.Kind != RunnerJobShell {
		jsonReply(w, http.StatusNotImplemented, map[string]interface{}{
			"error": "runner kind not yet supported on this agent",
			"kind":  string(job.Kind),
			"hint":  "Phase 1 ships shell only — see RUNNER_DEV.md for the build plan",
		})
		return
	}

	triggeredBy := "owner"
	if isGuest {
		triggeredBy = guestUID
	}
	final, err := runJobShell(r.Context(), store, job, triggeredBy, isGuest, s.vaultStore)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": "run failed: " + err.Error(),
		})
		return
	}
	// Strip server-side absolute paths before returning to clients.
	final.LogPath = ""
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":  final.OK,
		"run": final,
	})
}

// handleRunnerJobPause — POST /runner/jobs/{name}/pause [body: {"paused": bool}].
func (s *HTTPServer) handleRunnerJobPause(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "guests cannot pause runner jobs"})
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	body.Paused = true // default if no body
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.ensureRunnerStore().SetPaused(name, body.Paused); err != nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]bool{"ok": true, "paused": body.Paused})
}

// handleRunnerJobRuns — GET /runner/jobs/{name}/runs[?limit=N].
func (s *HTTPServer) handleRunnerJobRuns(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	limit := runnerLimitFromQuery(r, 50, 500)
	guestFilter := runnerGuestFilter(r)
	runs := s.ensureRunnerStore().ListRuns(name, guestFilter, limit)
	for i := range runs {
		runs[i].OutputTail = ""
		runs[i].LogPath = ""
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"runs":  runs,
		"count": len(runs),
	})
}

// handleRunnerRuns — GET /runner/runs[?limit=N].
func (s *HTTPServer) handleRunnerRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	limit := runnerLimitFromQuery(r, 50, 500)
	guestFilter := runnerGuestFilter(r)
	runs := s.ensureRunnerStore().ListRuns("", guestFilter, limit)
	for i := range runs {
		runs[i].OutputTail = ""
		runs[i].LogPath = ""
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"runs":  runs,
		"count": len(runs),
	})
}

// handleRunnerRunByID — GET /runner/runs/{id} or
// GET /runner/runs/{id}/log.
func (s *HTTPServer) handleRunnerRunByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/runner/runs/")
	tail = strings.TrimSuffix(tail, "/")
	if tail == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing run id"})
		return
	}
	parts := strings.SplitN(tail, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	guestFilter := runnerGuestFilter(r)
	run, ok := s.ensureRunnerStore().GetRun(id, guestFilter)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}

	switch sub {
	case "":
		// Strip absolute path before returning.
		run.LogPath = ""
		jsonReply(w, http.StatusOK, run)
	case "log":
		s.streamRunnerRunLog(w, run)
	default:
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource: " + sub})
	}
}

// streamRunnerRunLog sends the on-disk full log as text/plain. Falls
// back to the in-memory tail when the disk log is unavailable.
func (s *HTTPServer) streamRunnerRunLog(w http.ResponseWriter, run RunnerRun) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	logPath := s.ensureRunnerStore().LogPathFor(run.ID)
	if logPath == "" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(run.OutputTail))
		return
	}
	f, err := openDeployLog(logPath)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(run.OutputTail))
		return
	}
	defer f.Close()
	w.WriteHeader(http.StatusOK)
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

// handleRunnerPools — GET /runner/pools. Returns this agent's
// capability label list. Multi-machine roll-up (every device's caps)
// arrives in Phase 5; Phase 1 is local-only.
func (s *HTTPServer) handleRunnerPools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	caps := LocalCapabilities()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"pools":  caps,
		"local":  true,
		"device": s.deviceID,
	})
}

// runnerLimitFromQuery parses ?limit=N within bounds. Same shape as
// the deploy/runs endpoint; kept duplicated to avoid an awkward
// shared helper that one of the two might want to evolve later.
func runnerLimitFromQuery(r *http.Request, def, max int) int {
	v := strings.TrimSpace(r.URL.Query().Get("limit"))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
