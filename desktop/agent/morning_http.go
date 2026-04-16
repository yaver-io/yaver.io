package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// morning_http.go — owner-only HTTP for the morning match report and
// its recording byte-range streamer. Same surface for local dashboard,
// mobile (via relay), and yaver-to-yaver (also via relay, since the
// relay just proxies /d/{deviceId}/...).
//
// Not in guestAllowedPrefixes. Guests see nothing here.

// ── Wiring ────────────────────────────────────────────────────────────

// RegisterMorningRoutes attaches every /morning/* and /recordings/*
// handler to mux, guarded by the owner auth middleware provided by s.
// Called from the main HTTP server setup.
func (s *HTTPServer) RegisterMorningRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/morning/runs", s.auth(s.handleMorningRuns))
	mux.HandleFunc("/morning/runs/", s.auth(s.handleMorningRunByPath))
	mux.HandleFunc("/morning/drivers", s.auth(s.handleMorningDrivers))
	mux.HandleFunc("/recordings/", s.auth(s.handleRecordingByPath))
}

// ── morningCtx lazy accessors ─────────────────────────────────────────

// morningStore returns the attached MorningStore, creating a default
// one if none was wired. Ensures the server works out-of-the-box.
func (s *HTTPServer) morningStore() *MorningStore {
	s.morningMu.Lock()
	defer s.morningMu.Unlock()
	if s.morningStoreRef == nil {
		s.morningStoreRef = DefaultMorningStore()
	}
	return s.morningStoreRef
}

func (s *HTTPServer) recordingManager() *RecordingManager {
	s.morningMu.Lock()
	defer s.morningMu.Unlock()
	if s.recordingMgrRef == nil {
		s.recordingMgrRef = DefaultRecordingManager()
	}
	return s.recordingMgrRef
}

// ── /morning/runs and /morning/runs/{id}/... ──────────────────────────

func (s *HTTPServer) handleMorningRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	runs := s.morningStore().List(limit)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"runs": runs,
	})
}

func (s *HTTPServer) handleMorningRunByPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/morning/runs/")
	path = strings.Trim(path, "/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "run id required")
		return
	}
	parts := strings.Split(path, "/")
	runID := parts[0]

	// /morning/runs/{id}            → GET the run
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		summary, ok := s.morningStore().Load(runID)
		if !ok {
			jsonError(w, http.StatusNotFound, "run not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "run": summary})
		return
	}

	// /morning/runs/{id}/tasks/{tid}[/rollback]
	if len(parts) >= 3 && parts[1] == "tasks" {
		taskID := parts[2]
		action := ""
		if len(parts) >= 4 {
			action = parts[3]
		}
		switch action {
		case "":
			if r.Method != http.MethodGet {
				jsonError(w, http.StatusMethodNotAllowed, "use GET")
				return
			}
			summary, ok := s.morningStore().Load(runID)
			if !ok {
				jsonError(w, http.StatusNotFound, "run not found")
				return
			}
			for _, t := range summary.Tasks {
				if t.TaskID == taskID {
					jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "task": t})
					return
				}
			}
			jsonError(w, http.StatusNotFound, "task not found")
			return
		case "rollback":
			if r.Method != http.MethodPost {
				jsonError(w, http.StatusMethodNotAllowed, "use POST")
				return
			}
			s.handleMorningRollback(w, r, runID, taskID)
			return
		default:
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
	}

	jsonError(w, http.StatusNotFound, "not found")
}

// handleMorningRollback runs `git revert --no-edit` for every commit in
// TaskHighlight.CommitSHAs, in reverse order, against TaskHighlight.WorkDir
// (falling back to the run's WorkDir). Any conflict aborts the revert
// cleanly and surfaces the error — we never try to "smart merge".
func (s *HTTPServer) handleMorningRollback(w http.ResponseWriter, r *http.Request, runID, taskID string) {
	summary, ok := s.morningStore().Load(runID)
	if !ok {
		jsonError(w, http.StatusNotFound, "run not found")
		return
	}
	var task *TaskHighlight
	for i := range summary.Tasks {
		if summary.Tasks[i].TaskID == taskID {
			task = &summary.Tasks[i]
			break
		}
	}
	if task == nil {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Status == TaskStatusHighlightRolledBack {
		jsonError(w, http.StatusConflict, "task already rolled back")
		return
	}
	if len(task.CommitSHAs) == 0 {
		jsonError(w, http.StatusBadRequest, "task has no recorded commits to revert")
		return
	}
	workDir := task.WorkDir
	if workDir == "" {
		workDir = summary.WorkDir
	}
	if workDir == "" {
		jsonError(w, http.StatusBadRequest, "task has no recorded workDir")
		return
	}
	revertSHA, err := gitRevertCommits(r.Context(), workDir, task.CommitSHAs)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.morningStore().MarkRollback(runID, taskID, revertSHA)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"revertSha": revertSHA,
		"run":       updated,
	})
}

// gitRevertCommits reverts shas in reverse chronological order (newest
// first), creating a single revert commit per original commit. On any
// conflict it runs `git revert --abort` and returns the error verbatim
// so the caller can show it in the UI. Returns the SHA of the final
// revert commit on success.
func gitRevertCommits(ctx context.Context, workDir string, shas []string) (string, error) {
	if workDir == "" {
		return "", fmt.Errorf("empty workDir")
	}
	for _, sha := range shas {
		cmd := exec.CommandContext(ctx, "git", "-C", workDir, "revert", "--no-edit", sha)
		if out, err := cmd.CombinedOutput(); err != nil {
			// Abort in case git left a half-done revert in progress.
			_ = exec.Command("git", "-C", workDir, "revert", "--abort").Run()
			return "", fmt.Errorf("revert %s: %v (%s)", sha, err, strings.TrimSpace(string(out)))
		}
	}
	head := GitHeadSHA(workDir)
	if head == "" {
		return "", fmt.Errorf("reverts succeeded but could not read new HEAD")
	}
	return head, nil
}

// ── /recordings/{runId}/{taskId}/video.mp4 ────────────────────────────

func (s *HTTPServer) handleRecordingByPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/recordings/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	runID := sanitizeMorningID(parts[0])
	taskID := sanitizeMorningID(parts[1])
	leaf := parts[2]
	if len(parts) > 3 || (leaf != "video.mp4" && leaf != "thumb.jpg") {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}

	// Resolve + confine the path to the recordings root. Even though
	// sanitizeMorningID filters dangerous input, we re-validate after
	// filepath.Join to defend against driver-root misconfiguration.
	root := s.recordingManager().root
	full := filepath.Join(root, runID, taskID, leaf)
	absRoot, _ := filepath.Abs(root)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) && absFull != absRoot {
		jsonError(w, http.StatusBadRequest, "invalid path")
		return
	}

	if r.Method == http.MethodDelete {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonError(w, http.StatusMethodNotAllowed, "use GET/HEAD")
		return
	}

	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			jsonError(w, http.StatusNotFound, "recording not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// ServeContent handles Accept-Ranges, Content-Range, 206, HEAD, and
	// ETag/If-Modified-Since automatically. Mobile expo-video and web
	// <video> both rely on this to seek without downloading the whole
	// file — critical for the match-report card swipe experience.
	w.Header().Set("Content-Type", contentTypeFor(leaf))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, leaf, info.ModTime(), f)
	_ = time.Time{} // keep the "time" import useful without a direct call
}

func contentTypeFor(leaf string) string {
	switch {
	case strings.HasSuffix(leaf, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(leaf, ".jpg"), strings.HasSuffix(leaf, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(leaf, ".png"):
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// ── /morning/drivers (doctor-adjacent) ────────────────────────────────

func (s *HTTPServer) handleMorningDrivers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"platform": platformDescription(),
		"drivers":  s.recordingManager().Drivers(),
	})
}

// ── ugly-but-useful: ensure encoding/json stays imported even if the
// file ever loses its jsonReply use. jsonReply is already referenced
// above, but go vet/imports can be touchy; this block keeps the
// import obvious if someone strips usages later.
var _ = json.Marshal
var _ = io.Copy
