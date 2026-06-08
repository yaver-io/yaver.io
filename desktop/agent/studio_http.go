package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yaver-io/agent/studio"
)

// studio_http.go — agent HTTP surface for the store-asset Studio, consumed by
// the mobile app + web dashboard (and usable by third-party owners on a
// Yaver-managed-cloud box where the agent runs next to the capture surface).
// See docs/yaver-store-asset-studio.md.
//
// POST /studio/permission-prose : analyze an app's permission usage and return
// the Play Console justification prose + demo-video shot-list. Fast + offline
// (no device) — this is what the Studio UI shows immediately; the actual video
// recording is driven by the capture layer (studio/redroid.go) on a runner.

type studioProseRequest struct {
	Permission string `json:"permission"`
	Path       string `json:"path"`     // project dir to scan (default: agent work dir)
	Manifest   string `json:"manifest"` // explicit AndroidManifest.xml path (optional)
	App        string `json:"app"`      // display name
	What       string `json:"what"`     // one clause: what the service does
}

type studioProseResponse struct {
	Permission  string   `json:"permission"`
	Platform    string   `json:"platform"`
	FGSType     string   `json:"fgsType,omitempty"`
	Service     string   `json:"service,omitempty"`
	Subtype     string   `json:"subtype,omitempty"`
	Trigger     string   `json:"trigger,omitempty"`
	Declared    bool     `json:"declared"`
	TaskOther   string   `json:"taskOther"`
	Description string   `json:"description"`
	ShotList    []string `json:"shotList"`
	Warnings    []string `json:"warnings,omitempty"`
	Markdown    string   `json:"markdown"`
	AllFGSPerms []string `json:"allForegroundServicePermissions,omitempty"`
}

func (s *HTTPServer) handleStudioPermissionProse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req studioProseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Permission) == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "permission required"})
		return
	}

	root := strings.TrimSpace(req.Path)
	if root == "" && s.taskMgr != nil {
		root = s.taskMgr.workDir
	}
	manifestPath := strings.TrimSpace(req.Manifest)
	if manifestPath == "" {
		manifestPath = findAndroidManifest(root)
	}
	if manifestPath == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "could not find AndroidManifest.xml under " + root + " — pass manifest"})
		return
	}

	facts, err := studio.AnalyzeAndroidManifest(manifestPath, req.Permission)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	facts.TriggerHint = studio.FindTrigger(root, facts)

	appName := strings.TrimSpace(req.App)
	if appName == "" {
		appName = "The app"
	}
	j := studio.GenerateJustification(facts, appName, req.What)

	resp := studioProseResponse{
		Permission:  facts.Permission,
		Platform:    facts.Platform,
		FGSType:     facts.FGSType,
		Subtype:     facts.SpecialUseSubtype,
		Trigger:     facts.TriggerHint,
		Declared:    facts.Declared,
		TaskOther:   j.TaskOther,
		Description: j.Description,
		ShotList:    j.ShotList,
		Warnings:    j.Warnings,
		Markdown:    j.Markdown(facts.Permission),
		AllFGSPerms: facts.AllFGSPermissions,
	}
	if facts.Service != nil {
		resp.Service = facts.Service.Name
	}
	jsonReply(w, http.StatusOK, resp)
}

// POST /studio/jobs — start an async permission-video capture job (agentic LLM
// or UI triggers it); returns the job id + initial status to poll.
func (s *HTTPServer) handleStudioJobStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req studioPermissionJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Path) == "" && s.taskMgr != nil {
		req.Path = s.taskMgr.workDir
	}
	job, err := studioJobs.startPermissionVideo(req)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusAccepted, job.snapshot())
}

// GET /studio/jobs            — list jobs
// GET /studio/jobs/<id>       — one job's live status
func (s *HTTPServer) handleStudioJobStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/studio/jobs/")
	id = strings.Trim(id, "/")
	if id == "" {
		jsonReply(w, http.StatusOK, map[string]any{"jobs": studioJobs.list()})
		return
	}
	job := studioJobs.get(id)
	if job == nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "no such job"})
		return
	}
	jsonReply(w, http.StatusOK, job.snapshot())
}
