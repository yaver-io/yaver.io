package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── Chained Tasks ──────────────────────────────────────────────────

// handleChainCreate creates a chain of tasks that execute sequentially.
// POST /chain
func (s *HTTPServer) handleChainCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var body struct {
		Tasks              []ChainedTaskInput `json:"tasks"`
		Model              string             `json:"model,omitempty"`
		Runner             string             `json:"runner,omitempty"`
		Source             string             `json:"source,omitempty"`
		PlacementKind      string             `json:"placementKind,omitempty"`
		ForceCloud         bool               `json:"forceCloud,omitempty"`
		ForceRelaySource   bool               `json:"forceRelaySource,omitempty"`
		AllowLocalFallback bool               `json:"allowLocalFallback,omitempty"`
		AutoRetry          bool               `json:"autoRetry,omitempty"`
		SpeechContext      *struct {
			InputFromSpeech bool   `json:"inputFromSpeech,omitempty"`
			STTProvider     string `json:"sttProvider,omitempty"`
			TTSEnabled      bool   `json:"ttsEnabled,omitempty"`
			TTSProvider     string `json:"ttsProvider,omitempty"`
			TTSMode         bool   `json:"ttsMode,omitempty"`
		} `json:"speechContext,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Tasks) == 0 {
		jsonError(w, http.StatusBadRequest, "tasks array is required and must not be empty")
		return
	}
	for i, t := range body.Tasks {
		if t.Title == "" {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("task %d: title is required", i))
			return
		}
	}

	source := body.Source
	if source == "" {
		source = r.Header.Get("X-Yaver-Source")
	}
	if source == "" {
		source = "mobile"
	}

	// Fold the client's speech context / TTS-mode setting into a viewport
	// passed INTO chain creation, so even task 0 (started synchronously)
	// gets the prompt hint. Text-only shaping; see formatViewportHint.
	var vp *TaskViewport
	if body.SpeechContext != nil {
		vp = &TaskViewport{
			Voice:       body.SpeechContext.InputFromSpeech,
			STTEnabled:  body.SpeechContext.InputFromSpeech || body.SpeechContext.STTProvider != "",
			TTSEnabled:  body.SpeechContext.TTSEnabled,
			TTSMode:     body.SpeechContext.TTSMode,
			STTProvider: body.SpeechContext.STTProvider,
			TTSProvider: body.SpeechContext.TTSProvider,
		}
	}
	vp = mergeClientVoiceHints(r, vp, source)

	chainTitle := strings.TrimSpace(body.Tasks[0].Title)
	if len(body.Tasks) > 1 {
		chainTitle = fmt.Sprintf("%s (+%d chained)", chainTitle, len(body.Tasks)-1)
	}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		KindHint:         firstNonEmpty(strings.TrimSpace(body.PlacementKind), "chain"),
		Title:            chainTitle,
		Description:      body.Tasks[0].Description,
		Source:           source,
		Runner:           body.Runner,
		WorkDir:          s.taskMgr.workDir,
		TargetDeviceID:   s.deviceID,
		ForceCloud:       body.ForceCloud,
		ForceRelaySource: body.ForceRelaySource,
	})
	if previewPlacement, perr := s.previewTaskPlacement(r.Context(), meta); perr != nil {
		log.Printf("[placement] chain preview skipped before task create: %v", perr)
	} else if !body.AllowLocalFallback && shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		pendingTaskID := newPendingCloudTaskID()
		recordedPlacement := previewPlacement
		if placement, rerr := s.recordTaskPlacement(r.Context(), pendingTaskID, meta); rerr != nil {
			log.Printf("[placement] chain pending record skipped for %s: %v", pendingTaskID, rerr)
		} else if placement != nil {
			recordedPlacement = placement
		}
		var activation map[string]any
		if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
			if result, aerr := s.activateTaskPlacement(r.Context(), recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
				activation = activationMapFromError(aerr)
				log.Printf("[placement] chain activation skipped for %s: %v", pendingTaskID, aerr)
			} else {
				activation = result
			}
		}
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"ok":            false,
			"action":        "cloud_workspace_required",
			"pendingTaskId": pendingTaskID,
			"placement":     recordedPlacement,
			"activation":    activation,
			"reason":        "placement selected a Cloud Workspace for this chain; keep the chain client-side and retry against the assigned workspace when it is ready",
		})
		return
	}

	created, err := s.taskMgr.CreateChainedTasks(body.Tasks, body.Model, source, body.Runner, body.AutoRetry, vp)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create chain: %v", err))
		return
	}

	var taskIDs []string
	for _, t := range created {
		taskIDs = append(taskIDs, t.ID)
	}

	log.Printf("[HTTP] Chain created with %d tasks: %v", len(created), taskIDs)
	jsonReply(w, http.StatusCreated, map[string]interface{}{
		"ok":      true,
		"chainId": created[0].ChainID,
		"tasks":   taskIDs,
		"count":   len(created),
	})
}

// handleChainStatus returns the status of all tasks in a chain.
// GET /chain/{chainId}
func (s *HTTPServer) handleChainStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	chainID := strings.TrimPrefix(r.URL.Path, "/chain/")
	if chainID == "" {
		jsonError(w, http.StatusBadRequest, "chain ID required")
		return
	}

	chain := s.taskMgr.GetChainStatus(chainID)
	if len(chain) == 0 {
		jsonError(w, http.StatusNotFound, "chain not found")
		return
	}

	// Calculate overall chain status
	overall := "queued"
	allDone := true
	anyFailed := false
	anyRunning := false
	for _, t := range chain {
		if t.Status == TaskStatusRunning {
			anyRunning = true
			allDone = false
		} else if t.Status == TaskStatusQueued {
			allDone = false
		} else if t.Status == TaskStatusFailed {
			anyFailed = true
		}
	}
	if allDone && anyFailed {
		overall = "failed"
	} else if allDone {
		overall = "completed"
	} else if anyRunning {
		overall = "running"
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"chainId": chainID,
		"status":  overall,
		"tasks":   chain,
	})
}

// ── Deploy (Ship It) ───────────────────────────────────────────────

// DeployTarget represents a detected deploy target for the project.
type DeployTarget struct {
	ID      string `json:"id"`      // e.g. "cloudflare", "testflight", "playstore"
	Name    string `json:"name"`    // human-readable name
	Command string `json:"command"` // shell command to run
}

// handleDeploy detects project type and runs the appropriate deploy command.
// GET /deploy — returns available deploy targets
// POST /deploy — triggers a deploy
func (s *HTTPServer) handleDeploy(w http.ResponseWriter, r *http.Request) {
	workDir := s.taskMgr.workDir

	if r.Method == http.MethodGet {
		// Return available deploy targets
		targets := detectDeployTargets(workDir)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"targets": targets,
			"workDir": workDir,
		})
		return
	}

	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
		return
	}

	var body struct {
		Target string `json:"target"` // deploy target ID; empty = auto-detect
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// Allow empty body for auto-detect
		body.Target = ""
	}

	targets := detectDeployTargets(workDir)
	if len(targets) == 0 {
		jsonError(w, http.StatusBadRequest, "no deploy targets detected for this project")
		return
	}

	// Find the target
	var target *DeployTarget
	if body.Target == "" {
		// Auto-select first target
		target = &targets[0]
	} else {
		for i := range targets {
			if targets[i].ID == body.Target {
				target = &targets[i]
				break
			}
		}
	}
	if target == nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("unknown deploy target: %s", body.Target))
		return
	}

	// Create a task for the deploy
	deployTitle := fmt.Sprintf("Deploy to %s", target.Name)
	taskOpts := TaskCreateOptions{WorkDir: workDir}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		KindHint:       "deploy",
		Title:          deployTitle,
		CustomCommand:  target.Command,
		Source:         "mobile",
		WorkDir:        workDir,
		TargetDeviceID: s.deviceID,
	})
	if previewPlacement, perr := s.previewTaskPlacement(r.Context(), meta); perr != nil {
		log.Printf("[placement] deploy preview skipped before task create: %v", perr)
	} else if shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		pendingTaskID := newPendingCloudTaskID()
		recordedPlacement := previewPlacement
		if placement, rerr := s.recordTaskPlacement(r.Context(), pendingTaskID, meta); rerr != nil {
			log.Printf("[placement] deploy pending record skipped for %s: %v", pendingTaskID, rerr)
		} else if placement != nil {
			recordedPlacement = placement
		}
		var activation map[string]any
		if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
			if result, aerr := s.activateTaskPlacement(r.Context(), recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
				activation = activationMapFromError(aerr)
				log.Printf("[placement] deploy activation skipped for %s: %v", pendingTaskID, aerr)
			} else {
				activation = result
			}
		}
		bodyJSON, _ := json.Marshal(map[string]any{
			"title":         deployTitle,
			"source":        "mobile",
			"workDir":       workDir,
			"customCommand": target.Command,
			"placementKind": meta.Kind,
		})
		cloudErr := &CloudWorkspaceRequiredError{
			PendingTaskID: pendingTaskID,
			Placement:     recordedPlacement,
			Activation:    activation,
			Reason:        "placement selected a Cloud Workspace for this deploy task",
		}
		authHeader := "Bearer " + strings.TrimSpace(s.token)
		if _, remoteTask, herr := createTaskOnCloudWorkspace(r.Context(), cloudErr, authHeader, bodyJSON, 20*time.Second); herr == nil && remoteTask != nil {
			targetDeviceID := ""
			if recordedPlacement != nil {
				targetDeviceID = recordedPlacement.TargetDeviceID
			}
			log.Printf("[HTTP] Deploy triggered on Cloud Workspace: %s (task %s)", target.Name, remoteTask.TaskID)
			jsonReply(w, http.StatusAccepted, map[string]interface{}{
				"ok":             true,
				"mode":           "cloud_workspace",
				"taskId":         remoteTask.TaskID,
				"status":         remoteTask.Status,
				"pendingTaskId":  pendingTaskID,
				"targetDeviceId": targetDeviceID,
				"placement":      recordedPlacement,
				"target":         target.Name,
			})
			return
		} else {
			reason := "Cloud Workspace is waking or needs attention before this deploy can run."
			if herr != nil {
				reason = herr.Error()
			}
			jsonReply(w, http.StatusConflict, map[string]interface{}{
				"ok":            false,
				"action":        "cloud_workspace_required",
				"pendingTaskId": pendingTaskID,
				"placement":     recordedPlacement,
				"activation":    activation,
				"reason":        reason,
			})
			return
		}
	} else if previewPlacement != nil {
		taskOpts.Placement = previewPlacement
	}
	task, err := s.taskMgr.CreateTaskWithOptions(deployTitle, "", "", "mobile", "", target.Command, nil, taskOpts)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("deploy failed: %v", err))
		return
	}

	log.Printf("[HTTP] Deploy triggered: %s (task %s)", target.Name, task.ID)
	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"target": target.Name,
	})
}

// detectDeployTargets checks what deploy commands are available for this project.
func detectDeployTargets(workDir string) []DeployTarget {
	var targets []DeployTarget

	// Web: Cloudflare Workers / Vercel
	if dirHasCloudflareDeployConfig(workDir) {
		targets = append(targets, DeployTarget{
			ID:      "cloudflare",
			Name:    "Cloudflare Workers",
			Command: "npm run deploy",
		})
	} else if dirHasVercelDeployConfig(workDir) {
		targets = append(targets, DeployTarget{
			ID:      "vercel",
			Name:    "Vercel",
			Command: "npx vercel --prod --yes",
		})
	} else if fileExists(filepath.Join(workDir, "netlify.toml")) {
		targets = append(targets, DeployTarget{
			ID:      "netlify",
			Name:    "Netlify",
			Command: "npx netlify deploy --prod",
		})
	}

	// iOS: TestFlight
	if fileExists(filepath.Join(workDir, "ios")) {
		scriptPath := findDeployScript(workDir, "deploy-testflight.sh")
		if scriptPath != "" {
			targets = append(targets, DeployTarget{
				ID:      "testflight",
				Name:    "TestFlight",
				Command: scriptPath,
			})
		}
	}

	// Android: Google Play
	if fileExists(filepath.Join(workDir, "android")) {
		scriptPath := findDeployScript(workDir, "deploy-playstore.sh")
		if scriptPath != "" {
			targets = append(targets, DeployTarget{
				ID:      "playstore",
				Name:    "Google Play",
				Command: scriptPath,
			})
		}
	}

	// Convex backend
	if fileExists(filepath.Join(workDir, "convex")) {
		targets = append(targets, DeployTarget{
			ID:      "convex",
			Name:    "Convex",
			Command: "cd " + workDir + " && npx convex deploy --yes",
		})
	}

	// Supabase
	if fileExists(filepath.Join(workDir, "supabase")) {
		targets = append(targets, DeployTarget{
			ID:      "supabase",
			Name:    "Supabase",
			Command: "cd " + workDir + " && npx supabase db push",
		})
	}

	// Firebase
	if fileExists(filepath.Join(workDir, "firebase.json")) {
		targets = append(targets, DeployTarget{
			ID:      "firebase",
			Name:    "Firebase",
			Command: "cd " + workDir + " && npx firebase deploy",
		})
	}

	// Docker Compose
	if fileExists(filepath.Join(workDir, "docker-compose.yml")) || fileExists(filepath.Join(workDir, "docker-compose.yaml")) {
		targets = append(targets, DeployTarget{
			ID:      "docker",
			Name:    "Docker Compose",
			Command: "cd " + workDir + " && docker compose up -d --build",
		})
	}

	// Fly.io
	if fileExists(filepath.Join(workDir, "fly.toml")) {
		targets = append(targets, DeployTarget{
			ID:      "fly",
			Name:    "Fly.io",
			Command: "cd " + workDir + " && fly deploy",
		})
	}

	// Railway
	if fileExists(filepath.Join(workDir, "railway.json")) || fileExists(filepath.Join(workDir, "railway.toml")) {
		targets = append(targets, DeployTarget{
			ID:      "railway",
			Name:    "Railway",
			Command: "cd " + workDir + " && railway up",
		})
	}

	return targets
}

// findDeployScript looks for a deploy script in the project's scripts/ dir or parent.
func findDeployScript(workDir, scriptName string) string {
	// Check scripts/ directory
	path := filepath.Join(workDir, "scripts", scriptName)
	if fileExists(path) {
		return path
	}
	// Check parent's scripts/ directory (monorepo)
	parent := filepath.Dir(workDir)
	path = filepath.Join(parent, "scripts", scriptName)
	if fileExists(path) {
		return path
	}
	// Check if the script is in PATH
	if _, err := exec.LookPath(scriptName); err == nil {
		return scriptName
	}
	return ""
}

// ── Task Summary ───────────────────────────────────────────────────

// handleSummary returns a digest of task activity.
// GET /summary — returns summary for last 24 hours (or ?hours=N)
func (s *HTTPServer) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := fmt.Sscanf(h, "%d", &hours); n == 1 && err == nil && hours > 0 {
			// use parsed value
		} else {
			hours = 24
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	summary := s.taskMgr.GetSummary(since)
	text := s.taskMgr.GenerateSummaryText(since)

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"summary": summary,
		"text":    text,
	})
}
