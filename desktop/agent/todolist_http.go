package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleTodoList handles POST /todolist (add) and GET /todolist (list).
func (s *HTTPServer) handleTodoList(w http.ResponseWriter, r *http.Request) {
	if s.todolistMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "todolist not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		items := s.todolistMgr.ListItems()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"items": items,
			"count": s.todolistMgr.Count(),
		})

	case http.MethodPost:
		// Multipart upload: metadata (JSON) + screenshots + audio
		r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100 MB
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart: " + err.Error()})
			return
		}

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
				name := fh.Filename
				if name == "" {
					name = key
				}
				files[name] = data
			}
		}

		// Capture blackbox snapshot if available
		blackboxSnap := ""
		if s.blackboxMgr != nil {
			// Try to get context from metadata's deviceInfo.platform
			var meta struct {
				DeviceInfo DeviceFBInfo `json:"deviceInfo"`
			}
			json.Unmarshal([]byte(metadataStr), &meta)
			if meta.DeviceInfo.Platform != "" {
				for _, sess := range s.blackboxMgr.ListSessions() {
					if sess["platform"] == meta.DeviceInfo.Platform {
						if session := s.blackboxMgr.GetSession(sess["deviceId"].(string)); session != nil {
							blackboxSnap = session.GenerateBlackBoxContext(50)
						}
						break
					}
				}
			}
		}

		item, err := s.todolistMgr.AddItem(json.RawMessage(metadataStr), files, blackboxSnap)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"id":    item.ID,
			"count": s.todolistMgr.Count(),
		})

	case http.MethodDelete:
		// Clear all items
		count := s.todolistMgr.ClearAll()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"cleared": count,
		})

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleTodoListAutoConsume handles POST /todolist/auto-consume — toggle auto-consume mode.
// When enabled, items are implemented immediately as they arrive (parallel with testing).
func (s *HTTPServer) handleTodoListAutoConsume(w http.ResponseWriter, r *http.Request) {
	if s.todolistMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "todolist not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"enabled": s.todolistMgr.IsAutoConsume(),
		})
	case http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		s.todolistMgr.SetAutoConsume(req.Enabled, func(item *TodoItem) {
			s.autoConsumeItem(item)
		})

		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"enabled": req.Enabled,
		})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// autoConsumeItem implements a single item in the background (called by auto-consume).
func (s *HTTPServer) autoConsumeItem(item *TodoItem) {
	if s.taskMgr == nil || s.todolistMgr == nil {
		return
	}

	prompt := s.todolistMgr.GenerateSingleFixPrompt(item)
	if s.autopilot.IsEnabled() {
		prompt += autopilotContext()
	}

	var images []ImageAttachment
	for _, ssPath := range item.Screenshots {
		data, err := os.ReadFile(ssPath)
		if err == nil {
			images = append(images, ImageAttachment{
				Base64:   base64.StdEncoding.EncodeToString(data),
				MimeType: mimeTypeFromPath(ssPath),
				Filename: filepath.Base(ssPath),
			})
		}
	}

	task, err := s.taskMgr.CreateTask(prompt, "", "", "todolist", "", "", images)
	if err != nil {
		s.todolistMgr.MarkFailed([]string{item.ID})
		return
	}

	s.todolistMgr.MarkImplementing([]string{item.ID}, task.ID)
	if !s.autopilot.IsEnabled() {
		go s.watchTodoTaskCompletion(task.ID, []string{item.ID})
	}
}

// handleTodoListCount handles GET /todolist/count.
func (s *HTTPServer) handleTodoListCount(w http.ResponseWriter, r *http.Request) {
	if s.todolistMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "todolist not available"})
		return
	}
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"count": s.todolistMgr.Count(),
	})
}

// handleTodoListImplementAll handles POST /todolist/implement-all.
func (s *HTTPServer) handleTodoListImplementAll(w http.ResponseWriter, r *http.Request) {
	if s.todolistMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "todolist not available"})
		return
	}
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	pending := s.todolistMgr.PendingItems()
	if len(pending) == 0 {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"itemCount": 0,
			"message":   "no pending items",
		})
		return
	}

	// Generate combined prompt
	prompt := s.todolistMgr.GenerateBatchFixPrompt(pending)

	// Collect screenshot paths for image attachments
	var images []ImageAttachment
	for _, item := range pending {
		for _, ssPath := range item.Screenshots {
			data, err := os.ReadFile(ssPath)
			if err == nil {
				images = append(images, ImageAttachment{
					Base64:   base64.StdEncoding.EncodeToString(data),
					MimeType: mimeTypeFromPath(ssPath),
					Filename: filepath.Base(ssPath),
				})
			}
		}
	}

	// Create a single task
	if s.taskMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "task manager not available"})
		return
	}

	task, err := s.taskMgr.CreateTask(prompt, "", "", "todolist", "", "", images)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Mark all items as implementing
	ids := make([]string, len(pending))
	for i, item := range pending {
		ids[i] = item.ID
	}
	s.todolistMgr.MarkImplementing(ids, task.ID)

	// Watch task completion in background
	go s.watchTodoTaskCompletion(task.ID, ids)

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"taskId":    task.ID,
		"itemCount": len(pending),
	})
}

// handleTodoListByID handles /todolist/{id}[/implement].
func (s *HTTPServer) handleTodoListByID(w http.ResponseWriter, r *http.Request) {
	if s.todolistMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "todolist not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/todolist/")
	parts := strings.SplitN(path, "/", 2)
	itemID := parts[0]

	if itemID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing item ID"})
		return
	}

	// Sub-route: implement
	if len(parts) > 1 && parts[1] == "implement" {
		s.handleTodoListImplementOne(w, r, itemID)
		return
	}

	item, ok := s.todolistMgr.GetItem(itemID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "item not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := s.todolistMgr.RemoveItem(itemID); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"count": s.todolistMgr.Count(),
		})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleTodoListImplementOne implements a single item.
func (s *HTTPServer) handleTodoListImplementOne(w http.ResponseWriter, r *http.Request, itemID string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	item, ok := s.todolistMgr.GetItem(itemID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "item not found"})
		return
	}
	if item.Status != TodoStatusPending {
		jsonReply(w, http.StatusConflict, map[string]string{"error": "item is not pending"})
		return
	}

	prompt := s.todolistMgr.GenerateSingleFixPrompt(item)

	var images []ImageAttachment
	for _, ssPath := range item.Screenshots {
		data, err := os.ReadFile(ssPath)
		if err == nil {
			images = append(images, ImageAttachment{
				Base64:   base64.StdEncoding.EncodeToString(data),
				MimeType: mimeTypeFromPath(ssPath),
				Filename: filepath.Base(ssPath),
			})
		}
	}

	if s.taskMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "task manager not available"})
		return
	}

	task, err := s.taskMgr.CreateTask(prompt, "", "", "todolist", "", "", images)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.todolistMgr.MarkImplementing([]string{itemID}, task.ID)
	go s.watchTodoTaskCompletion(task.ID, []string{itemID})

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
	})
}

// watchTodoTaskCompletion polls task status and updates todo items when done.
func (s *HTTPServer) watchTodoTaskCompletion(taskID string, itemIDs []string) {
	if s.taskMgr == nil {
		return
	}

	for i := 0; i < 720; i++ { // up to 1 hour (5s * 720)
		task, ok := s.taskMgr.GetTask(taskID)
		if !ok || task == nil {
			return
		}
		switch task.Status {
		case TaskStatusFinished:
			s.todolistMgr.MarkDone(itemIDs)
			return
		case TaskStatusFailed, TaskStatusStopped:
			s.todolistMgr.MarkFailed(itemIDs)
			return
		}
		time.Sleep(5 * time.Second)
	}
	// Timed out — mark as failed
	s.todolistMgr.MarkFailed(itemIDs)
}

// handleTodoListClassify handles POST /todolist/classify — auto-classifies a chat message.
// The user just writes naturally and the system determines if it's a todo item,
// continuation, or immediate action. Returns the classification and acts on it.
func (s *HTTPServer) handleTodoListClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Message    string `json:"message"`
		Source     string `json:"source"`               // "sdk" or "mobile"
		AutoAct    bool   `json:"autoAct"`              // if true, automatically queue/execute based on classification
		DeviceInfo *DeviceFBInfo `json:"deviceInfo,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Message == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}

	// Get existing items for continuation detection
	var existingItems []*TodoItem
	if s.todolistMgr != nil {
		existingItems = s.todolistMgr.PendingItems()
	}

	result := ClassifyMessage(req.Message, existingItems)

	resp := map[string]interface{}{
		"intent":      result.Intent,
		"description": result.Description,
		"isImmediate": result.IsImmediate,
		"confidence":  result.Confidence,
	}
	if result.TodoID != "" {
		resp["todoId"] = result.TodoID
	}

	// Auto-act: perform the classified action
	if req.AutoAct {
		switch result.Intent {
		case IntentTodo:
			if s.todolistMgr != nil {
				source := req.Source
				if source == "" {
					source = "chat"
				}
				meta := map[string]interface{}{
					"description": req.Message,
					"source":      source,
				}
				if req.DeviceInfo != nil {
					meta["deviceInfo"] = req.DeviceInfo
				}
				metaJSON, _ := json.Marshal(meta)

				// Get blackbox snap
				blackboxSnap := ""
				if s.blackboxMgr != nil && req.DeviceInfo != nil && req.DeviceInfo.Platform != "" {
					for _, sess := range s.blackboxMgr.ListSessions() {
						if sess["platform"] == req.DeviceInfo.Platform {
							if session := s.blackboxMgr.GetSession(sess["deviceId"].(string)); session != nil {
								blackboxSnap = session.GenerateBlackBoxContext(50)
							}
							break
						}
					}
				}

				item, err := s.todolistMgr.AddItem(json.RawMessage(metaJSON), nil, blackboxSnap)
				if err == nil {
					resp["todoItemId"] = item.ID
					resp["todoCount"] = s.todolistMgr.Count()
					resp["acted"] = true
				}
			}

		case IntentContinuation:
			if s.todolistMgr != nil && result.TodoID != "" {
				// Append to existing item's description
				if item, ok := s.todolistMgr.GetItem(result.TodoID); ok {
					item.Description = item.Description + "\n" + req.Message
					s.todolistMgr.saveItem(item)
					resp["todoItemId"] = result.TodoID
					resp["acted"] = true
				}
			}

		case IntentAction:
			if s.taskMgr != nil {
				source := req.Source
				if source == "" {
					source = "chat"
				}
				task, err := s.taskMgr.CreateTask(req.Message, "", "", source, "", "", nil)
				if err == nil {
					resp["taskId"] = task.ID
					resp["acted"] = true
				}
			}
		}
	}

	// Add project context
	if s.taskMgr != nil {
		project := DetectProjectInfo(s.taskMgr.workDir)
		resp["project"] = project.Name
	}

	jsonReply(w, http.StatusOK, resp)
}

// mimeTypeFromPath returns a MIME type based on file extension.
func mimeTypeFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// markLinkedTodos marks todolist items linked to a task as done or failed.
func (s *HTTPServer) markLinkedTodos(taskID string, success bool) {
	if s.todolistMgr == nil {
		return
	}
	items := s.todolistMgr.ListItems()
	var ids []string
	for _, item := range items {
		if item.TaskID == taskID && item.Status == TodoStatusImplementing {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if success {
		s.todolistMgr.MarkDone(ids)
	} else {
		s.todolistMgr.MarkFailed(ids)
	}
}
