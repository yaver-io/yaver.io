package main

// vibe_preview_clip_fix.go — close the clip → fix feedback loop.
//
// Today: a developer records a vibe-preview clip (sim or browser), watches
// it back in glass-workspace / web /workspace / mobile VibePreviewModal,
// sees the bug. There is NO direct path from "I see the bug" to "agent
// fixes it" — they have to manually copy the clip, file a feedback report
// from the SDK, and call /feedback/{id}/fix. Three steps.
//
// This endpoint makes it one step. POST /vibing/preview/clip/{id}/fix
// with a short user comment ("login button doesn't respond on tap"):
//   1. Look up the clip record.
//   2. Synthesise a FeedbackReport carrying the clip's MP4 path + a
//      timeline event with the user's comment.
//   3. Register it via FeedbackManager (so it appears in /feedback list
//      and inherits the existing review / change-set machinery).
//   4. If autoFix=true (default), immediately call GenerateFixPrompt +
//      create a fix task — same machinery as /feedback/{id}/fix.
//
// Returns { ok, feedbackId, taskId? }. The UI surfaces both:
// "filed as feedback X" and (if auto-fixed) "fix task Y started".

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type vibeClipFixRequest struct {
	// Comment: the user's short description of the bug they saw in the
	// clip. Becomes a timeline annotation on the feedback report and
	// the lead-in line of the generated fix prompt.
	Comment string `json:"comment"`
	// AutoFix: when true (default) the endpoint chains straight into
	// task creation. Set false to file a feedback report without
	// kicking off a fix — useful when the user just wants to capture
	// the bug for later triage.
	AutoFix *bool `json:"autoFix,omitempty"`
	// Project: optional override. Defaults to the clip's project name.
	Project string `json:"project,omitempty"`
}

type vibeClipFixResponse struct {
	OK         bool   `json:"ok"`
	FeedbackID string `json:"feedbackId"`
	TaskID     string `json:"taskId,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

// handleVibePreviewClipFix — POST /vibing/preview/clip/{id}/fix
func (s *HTTPServer) handleVibePreviewClipFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil || s.feedbackMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview or feedback manager not initialised")
		return
	}
	// Path: /vibing/preview/clip/<id>/fix — strip the prefix and the
	// /fix suffix. Validate the id shape to block path traversal even
	// though FeedbackManager.ReceiveFeedback already sanitises uploads.
	clipID := strings.TrimPrefix(r.URL.Path, "/vibing/preview/clip/")
	clipID = strings.TrimSuffix(clipID, "/fix")
	clipID = strings.Trim(clipID, "/ ")
	if clipID == "" || strings.ContainsAny(clipID, "/\\.") {
		jsonError(w, http.StatusBadRequest, "invalid clip id")
		return
	}
	rec := s.vibePreviewMgr.ClipByID(clipID)
	if rec == nil {
		jsonError(w, http.StatusNotFound, "clip not found")
		return
	}
	if rec.Status != "ready" {
		jsonError(w, http.StatusConflict, "clip not ready (status: "+rec.Status+")")
		return
	}

	var req vibeClipFixRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	autoFix := true
	if req.AutoFix != nil {
		autoFix = *req.AutoFix
	}
	comment := strings.TrimSpace(req.Comment)
	if comment == "" {
		comment = "Fix the bug visible in this vibe-preview clip."
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		projectName = rec.Project
	}

	// Read the MP4 bytes so ReceiveFeedback can persist them under the
	// new report's directory. The clip on disk stays where it is — we
	// COPY rather than reference, so later GC of the clip ringbuffer
	// doesn't break the feedback report.
	videoBytes, err := os.ReadFile(rec.Path)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "read clip artifact: "+err.Error())
		return
	}

	// Synthesise the metadata. Source="vibe-clip" tells consumers this
	// report was generated from a screen recording rather than via the
	// regular feedback SDK.
	meta := map[string]interface{}{
		"source":     "vibe-clip",
		"createdAt":  time.Now().UTC().Format(time.RFC3339),
		"transcript": comment,
		"timeline": []map[string]interface{}{
			{
				"time": 0.0,
				"type": "annotation",
				"text": comment,
			},
		},
		"deviceInfo": map[string]interface{}{
			"platform": clipSourceToPlatform(rec.Source),
			"model":    "vibe-preview",
			"appName":  projectName,
		},
		"project": map[string]interface{}{
			"projectName": projectName,
			"appName":     projectName,
			"surface":     clipSourceToSurface(rec.Source),
		},
	}
	metaJSON, _ := json.Marshal(meta)
	files := map[string][]byte{
		"clip.mp4": videoBytes,
	}
	report, err := s.feedbackMgr.ReceiveFeedback(metaJSON, files)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "register feedback: "+err.Error())
		return
	}

	resp := vibeClipFixResponse{OK: true, FeedbackID: report.ID}

	if autoFix {
		// Chain to the existing fix-task pipeline so we don't reinvent
		// the change-set + Docker-isolation + work-dir-resolution rules.
		// We replay them here inline to avoid a recursive HTTP call.
		prompt, err := s.feedbackMgr.GenerateFixPrompt(report.ID)
		if err != nil {
			// Feedback recorded successfully; the fix step failed.
			// Return 200 with the feedback id so the UI can still link
			// to the report. The user can click "fix this" again later.
			resp.Hint = "feedback filed but fix-prompt synthesis failed: " + err.Error()
			jsonReply(w, http.StatusOK, resp)
			return
		}
		if s.taskMgr != nil {
			opts := TaskCreateOptions{}
			// Use the agent's own work directory — clip-fix is owner-
			// initiated, not guest-initiated, so no Docker isolation
			// is required.
			opts.WorkDir = s.taskMgr.workDir
			if deferral, deferred, derr := s.deferIngressTaskToCloudWorkspace(r.Context(), "vibe-clip-fix", "vibe", "", opts.WorkDir); deferred {
				if deferral != nil {
					resp.TaskID = deferral.PendingTaskID
				}
				if derr != nil {
					resp.Hint = "feedback filed; Cloud Workspace selected but handoff is not ready: " + derr.Error()
				} else {
					resp.Hint = fmt.Sprintf("filed feedback %s; Cloud Workspace handoff %s queued for clip fix", report.ID, resp.TaskID)
				}
				jsonReply(w, http.StatusOK, resp)
				return
			}
			task, terr := s.taskMgr.CreateTaskWithOptions(prompt, "", "", "vibe-clip-fix", "", "", nil, opts)
			if terr != nil {
				resp.Hint = "fix task creation failed: " + terr.Error()
			} else {
				resp.TaskID = task.ID
				resp.Hint = fmt.Sprintf("filed feedback %s, fix task %s started", report.ID, task.ID)
			}
		} else {
			resp.Hint = "feedback filed; no taskMgr to start a fix task"
		}
	} else {
		resp.Hint = fmt.Sprintf("filed feedback %s (autoFix disabled)", report.ID)
	}

	jsonReply(w, http.StatusOK, resp)
}

// clipSourceToPlatform maps the recording source to the FeedbackReport's
// DeviceInfo.Platform — used downstream by the feedback-fix pipeline to
// pick a Black Box context (iOS / Android session).
func clipSourceToPlatform(source string) string {
	switch source {
	case "sim-ios":
		return "ios"
	case "sim-android":
		return "android"
	case "browser":
		return "web"
	case "phone":
		return "phone"
	default:
		return "unknown"
	}
}

func clipSourceToSurface(source string) string {
	if source == "browser" {
		return "web"
	}
	return "mobile"
}
