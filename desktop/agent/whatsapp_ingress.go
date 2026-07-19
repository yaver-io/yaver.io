package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

type whatsappCommandRequest struct {
	Source      string `json:"source"`
	Action      string `json:"action"`
	CommandText string `json:"commandText"`
	ProjectSlug string `json:"projectSlug,omitempty"`
}

func (s *HTTPServer) handleWhatsAppCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	secret := strings.TrimSpace(os.Getenv("YAVER_WHATSAPP_INGRESS_SECRET"))
	if secret == "" {
		jsonError(w, http.StatusServiceUnavailable, "whatsapp ingress is not enabled on this agent")
		return
	}
	got := strings.TrimSpace(r.Header.Get("X-Yaver-WhatsApp-Secret"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		jsonError(w, http.StatusUnauthorized, "invalid whatsapp ingress secret")
		return
	}
	var req whatsappCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Action = strings.TrimSpace(strings.ToLower(req.Action))
	req.CommandText = strings.TrimSpace(req.CommandText)
	req.ProjectSlug = strings.TrimSpace(req.ProjectSlug)
	if req.CommandText == "" && req.Action != "status" && req.Action != "reload" && req.Action != "build_reload" {
		jsonError(w, http.StatusBadRequest, "commandText required")
		return
	}

	switch req.Action {
	case "status":
		tasks := 0
		running := 0
		if s != nil && s.taskMgr != nil {
			for _, t := range s.taskMgr.ListTasks() {
				tasks++
				if t.Status == TaskStatusQueued || t.Status == TaskStatusRunning {
					running++
				}
			}
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "tasks": tasks, "running": running})
	case "reload", "build_reload":
		s.handleWhatsAppReload(w, r, req)
	case "task", "":
		s.handleWhatsAppTask(w, r, req)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported whatsapp action")
	}
}

func (s *HTTPServer) handleWhatsAppTask(w http.ResponseWriter, r *http.Request, req whatsappCommandRequest) {
	if s == nil || s.taskMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "task manager unavailable")
		return
	}
	title := req.CommandText
	if req.ProjectSlug != "" {
		title = "WhatsApp request for " + req.ProjectSlug + ": " + title
	}
	taskOpts := TaskCreateOptions{InitialUserPrompt: req.CommandText}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		Title:          title,
		Source:         "whatsapp",
		ProjectName:    req.ProjectSlug,
		WorkDir:        s.taskMgr.workDir,
		TargetDeviceID: s.deviceID,
	})
	if previewPlacement, perr := s.previewTaskPlacement(r.Context(), meta); perr != nil {
		log.Printf("[placement] WhatsApp preview skipped before task create: %v", perr)
	} else if shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		pendingTaskID := newPendingCloudTaskID()
		recordedPlacement := previewPlacement
		if placement, rerr := s.recordTaskPlacement(r.Context(), pendingTaskID, meta); rerr != nil {
			log.Printf("[placement] WhatsApp pending record skipped for %s: %v", pendingTaskID, rerr)
		} else if placement != nil {
			recordedPlacement = placement
		}
		var activation map[string]any
		if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
			if result, aerr := s.activateTaskPlacement(r.Context(), recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
				activation = activationMapFromError(aerr)
				log.Printf("[placement] WhatsApp activation skipped for %s: %v", pendingTaskID, aerr)
			} else {
				activation = result
			}
		}
		bodyJSON, _ := json.Marshal(map[string]any{
			"title":             title,
			"source":            "whatsapp",
			"projectName":       req.ProjectSlug,
			"userPrompt":        req.CommandText,
			"initialUserPrompt": req.CommandText,
			"placementKind":     meta.Kind,
		})
		cloudErr := &CloudWorkspaceRequiredError{
			PendingTaskID: pendingTaskID,
			Placement:     recordedPlacement,
			Activation:    activation,
			Reason:        "placement selected a Cloud Workspace for this WhatsApp task",
		}
		authHeader := "Bearer " + strings.TrimSpace(s.token)
		if _, remoteTask, herr := createTaskOnCloudWorkspace(r.Context(), cloudErr, authHeader, bodyJSON, 20*time.Second); herr == nil && remoteTask != nil {
			targetDeviceID := ""
			if recordedPlacement != nil {
				targetDeviceID = recordedPlacement.TargetDeviceID
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":             true,
				"mode":           "cloud_workspace",
				"taskId":         remoteTask.TaskID,
				"status":         remoteTask.Status,
				"pendingTaskId":  pendingTaskID,
				"targetDeviceId": targetDeviceID,
				"placement":      recordedPlacement,
			})
			return
		} else {
			reason := "Cloud Workspace is waking or needs attention before this WhatsApp task can run."
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
	task, err := s.taskMgr.CreateTaskWithOptions(
		title,
		"",
		"",
		"whatsapp",
		"",
		"",
		nil,
		taskOpts,
	)
	if err != nil {
		if task == nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"status": task.Status,
	})
}

func (s *HTTPServer) handleWhatsAppReload(w http.ResponseWriter, original *http.Request, req whatsappCommandRequest) {
	if s == nil {
		jsonError(w, http.StatusServiceUnavailable, "server unavailable")
		return
	}
	mode := "dev"
	if req.Action == "build_reload" {
		mode = "bundle"
	}
	body, _ := json.Marshal(map[string]string{
		"mode":        mode,
		"projectName": req.ProjectSlug,
	})
	inner, _ := http.NewRequestWithContext(original.Context(), http.MethodPost, "/dev/reload-app", bytes.NewReader(body))
	inner.Header.Set("Content-Type", "application/json")
	// The internal handler only needs guest headers for guest restrictions;
	// WhatsApp ingress is already authenticated by the shared backend secret.
	rec := httptest.NewRecorder()
	s.handleReloadApp(rec, inner)
	for k, vals := range rec.Header() {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if rec.Code == 0 {
		rec.Code = http.StatusOK
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
