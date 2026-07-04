package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
		s.handleWhatsAppTask(w, req)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported whatsapp action")
	}
}

func (s *HTTPServer) handleWhatsAppTask(w http.ResponseWriter, req whatsappCommandRequest) {
	if s == nil || s.taskMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "task manager unavailable")
		return
	}
	title := req.CommandText
	if req.ProjectSlug != "" {
		title = "WhatsApp request for " + req.ProjectSlug + ": " + title
	}
	task, err := s.taskMgr.CreateTaskWithOptions(
		title,
		"",
		"",
		"whatsapp",
		"",
		"",
		nil,
		TaskCreateOptions{InitialUserPrompt: req.CommandText},
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
