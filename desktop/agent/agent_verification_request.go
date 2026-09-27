package main

// agent_verification_request.go makes browser verification a first-class
// runner operation. The runner asks; the daemon binds the run to the task's
// project, executes the existing Playwright-backed yaver-tests suite, and
// returns independently collected evidence to the same turn.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type TaskVerificationCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // pass|fail
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

type TaskVerificationArtifact struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	// Path is persisted locally so the authenticated artifact reader can find
	// it after restart. taskVerificationForWire removes it from every DTO.
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

type TaskVerification struct {
	Kind          string                     `json:"kind"`
	Status        string                     `json:"status"` // running|passed|failed
	Attempt       int                        `json:"attempt"`
	JobID         string                     `json:"jobId,omitempty"`
	Project       string                     `json:"project,omitempty"`
	Feature       string                     `json:"feature,omitempty"`
	Total         int                        `json:"total,omitempty"`
	Passed        int                        `json:"passed,omitempty"`
	Failed        int                        `json:"failed,omitempty"`
	DurationMs    int64                      `json:"durationMs,omitempty"`
	Checks        []TaskVerificationCheck    `json:"checks,omitempty"`
	Artifacts     []TaskVerificationArtifact `json:"artifacts,omitempty"`
	VideoClipID   string                     `json:"videoClipId,omitempty"`
	VideoURL      string                     `json:"videoUrl,omitempty"`
	PosterURL     string                     `json:"posterUrl,omitempty"`
	StartedAt     time.Time                  `json:"startedAt"`
	FinishedAt    *time.Time                 `json:"finishedAt,omitempty"`
	FailureCode   string                     `json:"failureCode,omitempty"`
	FailureReason string                     `json:"failureReason,omitempty"`
}

type taskVerificationRequest struct {
	Feature    string `json:"feature,omitempty"`
	Project    string `json:"project,omitempty"`
	Profile    string `json:"profile,omitempty"`
	Trace      *bool  `json:"trace,omitempty"`
	Video      *bool  `json:"video,omitempty"`
	DevCommand string `json:"dev_command,omitempty"`
	WaitURL    string `json:"wait_url,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

func taskVerificationForWire(in *TaskVerification) *TaskVerification {
	if in == nil {
		return nil
	}
	out := *in
	out.Checks = append([]TaskVerificationCheck(nil), in.Checks...)
	out.Artifacts = append([]TaskVerificationArtifact(nil), in.Artifacts...)
	for i := range out.Artifacts {
		out.Artifacts[i].Path = ""
	}
	return &out
}

func (tm *TaskManager) beginTaskVerification(taskID string, req taskVerificationRequest) (*Task, *TaskVerification, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task := tm.tasks[taskID]
	if task == nil || task.DeletedAt != nil {
		return nil, nil, fmt.Errorf("task not found")
	}
	if task.Status == TaskStatusFailed || task.Status == TaskStatusStopped {
		return nil, nil, fmt.Errorf("task is not active")
	}
	if task.Verification != nil && task.Verification.Status == "running" {
		return nil, nil, fmt.Errorf("verification_in_progress: attempt %d is still running", task.Verification.Attempt)
	}
	attempt := 1
	if task.Verification != nil {
		attempt = task.Verification.Attempt + 1
	}
	v := &TaskVerification{
		Kind: "playwright-spec", Status: "running", Attempt: attempt,
		Project: strings.TrimSpace(req.Project), Feature: strings.TrimSpace(req.Feature),
		StartedAt: time.Now().UTC(),
	}
	if v.Project == "" {
		v.Project = task.ProjectName
	}
	task.Verification = v
	tm.persistAsync()
	return task, taskVerificationForWire(v), nil
}

func (tm *TaskManager) updateTaskVerification(taskID string, mutate func(*TaskVerification)) (*Task, *TaskVerification) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task := tm.tasks[taskID]
	if task == nil || task.Verification == nil {
		return task, nil
	}
	mutate(task.Verification)
	tm.persistAsync()
	return task, taskVerificationForWire(task.Verification)
}

func (tm *TaskManager) taskVerificationArtifact(taskID string, index int) (TaskVerificationArtifact, bool) {
	if tm == nil || index < 0 {
		return TaskVerificationArtifact{}, false
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	task := tm.tasks[taskID]
	if task == nil || task.DeletedAt != nil || task.Verification == nil || index >= len(task.Verification.Artifacts) {
		return TaskVerificationArtifact{}, false
	}
	return task.Verification.Artifacts[index], true
}

func (tm *TaskManager) taskVerificationSnapshot(taskID string) *TaskVerification {
	if tm == nil {
		return nil
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	task := tm.tasks[taskID]
	if task == nil || task.DeletedAt != nil {
		return nil
	}
	return taskVerificationForWire(task.Verification)
}

func emitTaskVerification(task *Task, verification *TaskVerification) {
	if task == nil || verification == nil {
		return
	}
	emitTaskEvent(task, map[string]interface{}{
		"type": "task_verification", "taskId": task.ID,
		"verification": verification,
	})
}

func (s *HTTPServer) requestTaskVerification(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req taskVerificationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil && err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.TimeoutSec == 0 {
		req.TimeoutSec = 600
	}
	if req.TimeoutSec < 30 || req.TimeoutSec > 1800 {
		jsonError(w, http.StatusBadRequest, "timeout_sec must be between 30 and 1800")
		return
	}
	task, started, err := s.taskMgr.beginTaskVerification(taskID, req)
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	emitTaskVerification(task, started)

	workDir := strings.TrimSpace(task.WorkDir)
	if workDir == "" {
		s.finishTaskVerificationFailure(taskID, "verification_no_project", "task has no project working directory")
		jsonError(w, http.StatusUnprocessableEntity, "verification_no_project: attach a project and retry")
		return
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		s.finishTaskVerificationFailure(taskID, "verification_no_project", "project working directory is invalid")
		jsonError(w, http.StatusUnprocessableEntity, "verification_no_project: project working directory is invalid")
		return
	}
	trace := true
	if req.Trace != nil {
		trace = *req.Trace
	}
	video := true
	if req.Video != nil {
		video = *req.Video
	}
	runReq := testkitRunRequest{
		Project: started.Project, Dir: absWorkDir, Only: strings.TrimSpace(req.Feature),
		Concurrency: 1, Trace: trace, Profile: strings.TrimSpace(req.Profile),
		ForcePlaywright: true, DevCommand: strings.TrimSpace(req.DevCommand),
		WaitURL: strings.TrimSpace(req.WaitURL), Video: &video,
	}
	if runReq.Profile != "" {
		statePath, resolveErr := playwrightStorageStatePath(runReq.Profile)
		if resolveErr != nil {
			s.finishTaskVerificationFailure(taskID, "verification_bad_profile", resolveErr.Error())
			jsonError(w, http.StatusBadRequest, "verification_bad_profile: "+resolveErr.Error())
			return
		}
		runReq.StorageState = statePath
	}
	job, err := studioJobs.startTestkitRun(runReq)
	if err != nil {
		code := "verification_start_failed"
		if strings.Contains(strings.ToLower(err.Error()), "specs dir not found") {
			code = "verification_no_specs"
		}
		s.finishTaskVerificationFailure(taskID, code, err.Error())
		jsonError(w, http.StatusUnprocessableEntity, code+": "+err.Error())
		return
	}
	task, running := s.taskMgr.updateTaskVerification(taskID, func(v *TaskVerification) { v.JobID = job.ID })
	emitTaskVerification(task, running)

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		job.mu.Lock()
		state, jobErr := job.State, job.Error
		job.mu.Unlock()
		switch state {
		case studioCompleted:
			s.finishTaskVerificationReport(w, taskID, job.ID)
			return
		case studioFailed:
			if strings.TrimSpace(jobErr) == "" {
				jobErr = "browser verification job failed"
			}
			s.finishTaskVerificationFailure(taskID, "verification_start_failed", jobErr)
			jsonError(w, http.StatusUnprocessableEntity, "verification_start_failed: "+jobErr)
			return
		}
		select {
		case <-ctx.Done():
			s.finishTaskVerificationFailure(taskID, "verification_timeout", fmt.Sprintf("browser verification exceeded %ds", req.TimeoutSec))
			jsonError(w, http.StatusGatewayTimeout, "verification_timeout: browser verification did not finish before the deadline")
			return
		case <-ticker.C:
		}
	}
}

func (s *HTTPServer) finishTaskVerificationFailure(taskID, code, reason string) {
	now := time.Now().UTC()
	task, v := s.taskMgr.updateTaskVerification(taskID, func(v *TaskVerification) {
		v.Status, v.FailureCode, v.FailureReason, v.FinishedAt = "failed", code, strings.TrimSpace(reason), &now
	})
	emitTaskVerification(task, v)
}

func (s *HTTPServer) finishTaskVerificationReport(w http.ResponseWriter, taskID, jobID string) {
	report := getTestkitReport(jobID)
	if report == nil || report.Total == 0 {
		s.finishTaskVerificationFailure(taskID, "verification_no_report", "browser run completed without a non-empty report")
		jsonError(w, http.StatusUnprocessableEntity, "verification_no_report: browser run produced no checks")
		return
	}
	checks := make([]TaskVerificationCheck, 0, len(report.Features))
	for _, feature := range report.Features {
		checks = append(checks, TaskVerificationCheck{
			Name: feature.Name, Status: feature.Status, Error: feature.Error, DurationMs: feature.DurationMs,
		})
	}
	artifacts := make([]TaskVerificationArtifact, 0, len(report.Artifacts))
	for i, artifact := range report.Artifacts {
		artifacts = append(artifacts, TaskVerificationArtifact{
			Kind: artifact.Kind, Name: artifact.Name, MimeType: artifact.Mime,
			Bytes: artifact.Bytes, Path: artifact.Path,
			URL: fmt.Sprintf("/tasks/%s/verification/artifacts/%d", taskID, i),
		})
	}
	status, code, reason := "passed", "", ""
	if report.Failed > 0 {
		status, code = "failed", "verification_failed"
		for _, check := range checks {
			if check.Status == "fail" {
				reason = check.Name
				if check.Error != "" {
					reason += ": " + check.Error
				}
				break
			}
		}
	}
	clipID := ""
	if report.ReelPath != "" {
		var clipErr error
		clipID, clipErr = importVerificationClip(report.ReelPath, taskID)
		if clipErr != nil {
			status, code, reason = "failed", "verification_artifact_failed", clipErr.Error()
		}
	}
	now := time.Now().UTC()
	task, verification := s.taskMgr.updateTaskVerification(taskID, func(v *TaskVerification) {
		v.Status, v.Total, v.Passed, v.Failed = status, report.Total, report.Passed, report.Failed
		v.DurationMs, v.Checks, v.Artifacts = report.DurationMs, checks, artifacts
		v.FailureCode, v.FailureReason, v.FinishedAt = code, reason, &now
		if clipID != "" {
			v.VideoClipID = clipID
			v.VideoURL = "/vibing/preview/clip/" + clipID
			v.PosterURL = v.VideoURL + "/poster"
		}
	})
	if clipID != "" {
		writeTaskClipMarker(taskID, clipID)
		s.taskMgr.SetTaskVideoState(taskID, clipID, "ready")
	}
	emitTaskVerification(task, verification)
	statusCode := http.StatusOK
	if status == "failed" {
		statusCode = http.StatusUnprocessableEntity
	}
	jsonReply(w, statusCode, map[string]interface{}{
		"ok": status == "passed", "passed": status == "passed", "taskId": taskID,
		"verification": verification,
	})
}

func (s *HTTPServer) serveTaskVerificationArtifact(w http.ResponseWriter, r *http.Request, taskID, indexText string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	index, err := strconv.Atoi(strings.TrimSpace(indexText))
	if err != nil || index < 0 {
		jsonError(w, http.StatusBadRequest, "invalid artifact index")
		return
	}
	artifact, ok := s.taskMgr.taskVerificationArtifact(taskID, index)
	if !ok {
		jsonError(w, http.StatusNotFound, "verification artifact not found")
		return
	}
	path := strings.TrimSpace(artifact.Path)
	if path == "" {
		jsonError(w, http.StatusNotFound, "verification artifact is unavailable")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		jsonError(w, http.StatusNotFound, "verification artifact is missing on disk")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		jsonError(w, http.StatusNotFound, "verification artifact is unavailable")
		return
	}
	if artifact.MimeType != "" {
		w.Header().Set("Content-Type", artifact.MimeType)
	}
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	name := artifact.Name
	if name == "" {
		name = filepath.Base(path)
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func importVerificationClip(sourcePath, taskID string) (string, error) {
	st, err := os.Stat(sourcePath)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return "", fmt.Errorf("verification video is unavailable")
	}
	if st.Size() > 512<<20 {
		return "", fmt.Errorf("verification video exceeds the 512 MiB artifact limit")
	}
	clipID := newClipID()
	dir := filepath.Join(vibePreviewRoot(), "clips", sanitizeBranchName("task-"+taskID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(dir, clipID+".mp4")
	in, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, 512<<20))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return "", closeErr
	}
	poster := strings.TrimSuffix(destination, ".mp4") + ".poster.jpg"
	_ = extractClipPoster(destination, poster)
	return clipID, nil
}

func forwardYaverVerifyTask(rawArgs json.RawMessage) interface{} {
	var args taskVerificationRequest
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	if args.TimeoutSec == 0 {
		args.TimeoutSec = 600
	}
	if args.TimeoutSec < 30 || args.TimeoutSec > 1800 {
		return mcpToolError("timeout_sec must be between 30 and 1800")
	}
	taskID := strings.TrimSpace(os.Getenv("YAVER_TASK_ID"))
	if taskID == "" {
		return mcpToolError("yaver_verify_task is only available inside a Yaver task (YAVER_TASK_ID not set)")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return mcpToolError("yaver_verify_task: not authenticated (run `yaver auth`)")
	}
	body, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(args.TimeoutSec+30)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, localAgentBaseURL()+"/tasks/"+taskID+"/verify", bytes.NewReader(body))
	if err != nil {
		return mcpToolError("build verification request: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: time.Duration(args.TimeoutSec+30) * time.Second}).Do(req)
	if err != nil {
		return mcpToolError(fmt.Sprintf("forward verification request to daemon: %v", err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var payload map[string]interface{}
	if json.Unmarshal(raw, &payload) != nil {
		return mcpToolError(fmt.Sprintf("daemon returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(raw))
		if e, _ := payload["error"].(string); e != "" {
			message = e
		}
		return mcpToolError(message)
	}
	return mcpToolJSON(payload)
}
