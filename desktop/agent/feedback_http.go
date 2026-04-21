package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxFeedbackUploadSize = 500 << 20 // 500 MB

// handleFeedbackStream handles POST /feedback/stream — live feedback streaming.
// The SDK streams screen chunks, voice, and events in real-time.
// The agent processes them incrementally and can respond with fixes.
func (s *HTTPServer) handleFeedbackStream(w http.ResponseWriter, r *http.Request) {
	if s.feedbackMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "feedback not available"})
		return
	}

	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// SSE response for bidirectional communication
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Read incoming events from the SDK (chunked JSON lines)
	decoder := json.NewDecoder(r.Body)
	for decoder.More() {
		var event struct {
			Type string `json:"type"` // "voice", "screenshot", "annotation", "end"
			Text string `json:"text,omitempty"`
			Data string `json:"data,omitempty"` // base64 encoded image/audio chunk
		}
		if err := decoder.Decode(&event); err != nil {
			break
		}

		switch event.Type {
		case "voice":
			// Forward voice transcript to AI agent as incremental prompt
			if s.taskMgr != nil && event.Text != "" {
				// Send as SSE back to SDK: agent is processing
				fmt.Fprintf(w, "data: {\"type\":\"processing\",\"text\":\"Analyzing: %s\"}\n\n", event.Text)
				flusher.Flush()
			}
		case "screenshot":
			fmt.Fprintf(w, "data: {\"type\":\"received\",\"text\":\"Screenshot received\"}\n\n")
			flusher.Flush()
		case "annotation":
			fmt.Fprintf(w, "data: {\"type\":\"received\",\"text\":\"Note: %s\"}\n\n", event.Text)
			flusher.Flush()
		case "end":
			fmt.Fprintf(w, "data: {\"type\":\"done\",\"text\":\"Feedback session complete\"}\n\n")
			flusher.Flush()
			return
		}
	}
}

// handleFeedback handles POST /feedback (upload) and GET /feedback (list).
func (s *HTTPServer) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if s.feedbackMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "feedback not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		reports := s.feedbackMgr.ListFeedback()
		jsonReply(w, http.StatusOK, reports)

	case http.MethodPost:
		// Multipart upload: metadata (JSON) + video + audio + screenshots
		r.Body = http.MaxBytesReader(w, r.Body, maxFeedbackUploadSize)
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB in memory
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart: " + err.Error()})
			return
		}

		// Extract metadata
		metadataStr := r.FormValue("metadata")
		if metadataStr == "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing metadata field"})
			return
		}

		// Extract files
		files := make(map[string][]byte)
		for key, fileHeaders := range r.MultipartForm.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}
				// Use the form field key or original filename
				name := fh.Filename
				if name == "" {
					name = key
				}
				files[name] = data
			}
		}

		report, err := s.feedbackMgr.ReceiveFeedback(json.RawMessage(metadataStr), files)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, report)

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleFeedbackByID handles /feedback/{id}[/video|/screenshot/name|/fix].
func (s *HTTPServer) handleFeedbackByID(w http.ResponseWriter, r *http.Request) {
	if s.feedbackMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "feedback not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/feedback/")
	parts := strings.SplitN(path, "/", 3)
	feedbackID := parts[0]

	if feedbackID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing feedback ID"})
		return
	}

	// Sub-routes
	if len(parts) > 1 {
		switch parts[1] {
		case "video":
			s.serveFeedbackFile(w, r, feedbackID, "video")
			return
		case "screenshot":
			name := ""
			if len(parts) > 2 {
				name = parts[2]
			}
			s.serveFeedbackFile(w, r, feedbackID, name)
			return
		case "fix":
			s.handleFeedbackFix(w, r, feedbackID)
			return
		case "transcript":
			s.handleFeedbackTranscript(w, r, feedbackID)
			return
		}
	}

	// Default: GET report or DELETE
	report, ok := s.feedbackMgr.GetFeedback(feedbackID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "feedback not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, report)
	case http.MethodDelete:
		if err := s.feedbackMgr.DeleteFeedback(feedbackID); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// serveFeedbackFile serves a file from a feedback report.
func (s *HTTPServer) serveFeedbackFile(w http.ResponseWriter, r *http.Request, feedbackID, fileHint string) {
	report, ok := s.feedbackMgr.GetFeedback(feedbackID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "feedback not found"})
		return
	}

	var filePath string
	if fileHint == "video" {
		filePath = report.VideoPath
	} else if fileHint != "" {
		// Screenshot by name
		for _, s := range report.Screenshots {
			if strings.HasSuffix(s, fileHint) {
				filePath = s
				break
			}
		}
	}

	if filePath == "" {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}

	http.ServeFile(w, r, filePath)
}

// handleFeedbackFix creates a task from feedback.
func (s *HTTPServer) handleFeedbackFix(w http.ResponseWriter, r *http.Request, feedbackID string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	prompt, err := s.feedbackMgr.GenerateFixPrompt(feedbackID)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Inject black box context if available for this device
	if s.blackboxMgr != nil {
		report, ok := s.feedbackMgr.GetFeedback(feedbackID)
		if ok && report.DeviceInfo.Platform != "" {
			// Try to find a matching black box session
			for _, sess := range s.blackboxMgr.ListSessions() {
				if sess["platform"] == report.DeviceInfo.Platform {
					if session := s.blackboxMgr.GetSession(sess["deviceId"].(string)); session != nil {
						bbCtx := session.GenerateBlackBoxContext(100)
						if bbCtx != "" {
							prompt = bbCtx + "\n" + prompt
						}
					}
					break
				}
			}
		}
	}

	// Create a task with the generated prompt
	if s.taskMgr != nil {
		// If this fix was triggered by a guest (e.g. an end-user of the host's
		// app via the Feedback SDK), propagate the guest policy into the task.
		// Feedback-only guests always force Docker isolation — the prompt is
		// synthesized from user-controlled feedback content, so prompt-injection
		// would otherwise give a malicious end-user arbitrary code execution
		// against the dev machine's filesystem + network.
		opts := TaskCreateOptions{}
		guestUID := r.Header.Get("X-Yaver-GuestUserID")
		if guestUID != "" && s.guestConfigMgr != nil {
			guestCfg := s.guestConfigMgr.GetConfig(guestUID)

			// Project-scope gate: if the host narrowed this guest to specific
			// projects, reject fixes for feedback from any project the guest
			// doesn't own. We identify the project by the SDK-reported app
			// name on the feedback report (DeviceInfo.AppName) and match it
			// against the guest's allowedProjects list, case-insensitive.
			//
			// Missing app name + restricted guest == reject: a guest who was
			// pinned to Project A must not be able to trigger fixes on
			// untagged reports that could be from any project.
			if report, ok := s.feedbackMgr.GetFeedback(feedbackID); ok {
				projectName := report.DeviceInfo.AppName
				if !s.guestConfigMgr.GuestCanAccessProject(guestUID, projectName) {
					jsonReply(w, http.StatusForbidden, map[string]string{
						"error": "this guest is scoped to specific projects; the feedback's project is not in the allowed list",
					})
					return
				}
				// Pin the task to the resolved project directory so the AI
				// agent operates inside the right repo instead of whatever
				// the agent's current workdir happens to be.
				if projectName != "" {
					if mp := findMobileProjectByName(projectName); mp != nil && mp.Path != "" {
						opts.WorkDir = mp.Path
					}
				}
			}

			forceIsolation := s.guestConfigMgr.IsFeedbackOnly(guestUID) || guestRequireIsolation(guestCfg)
			if forceIsolation && (s.containerRunner == nil || !s.containerRunner.IsAvailable()) {
				jsonReply(w, http.StatusServiceUnavailable, map[string]string{
					"error": "feedback-only guests require Docker isolation for fix tasks, but Docker is not available on this host",
				})
				return
			}
			opts.GuestUserID = guestUID
			opts.GuestRequireIsolation = forceIsolation
			opts.GuestUseHostAPIKeys = guestUseHostAPIKeys(guestCfg)
			opts.GuestAllowGuestProvidedKeys = guestCfg == nil || guestCfg.AllowGuestProvidedAPIKeys == nil || *guestCfg.AllowGuestProvidedAPIKeys
			if guestCfg != nil {
				opts.GuestCPULimitPercent = guestCfg.CPULimitPercent
				opts.GuestRAMLimitMB = guestCfg.RAMLimitMB
			}
			// Stay inside the project directory — same prefix the /tasks handler
			// applies to direct guest-authored task creation.
			prompt = guestPromptPrefix(s.taskMgr.workDir, guestCfg) + prompt
		}
		task, err := s.taskMgr.CreateTaskWithOptions(prompt, "", "", "feedback", "", "", nil, opts)
		if err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"taskId": task.ID,
			"prompt": prompt,
		})
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"prompt": prompt,
	})
}

// handleFeedbackTranscript saves a voice transcript.
func (s *HTTPServer) handleFeedbackTranscript(w http.ResponseWriter, r *http.Request, feedbackID string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Transcript string `json:"transcript"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if err := s.feedbackMgr.SaveTranscript(feedbackID, req.Transcript); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}
