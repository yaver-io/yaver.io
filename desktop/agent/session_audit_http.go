package main

import (
	"encoding/json"
	"net/http"
)

// handleSessionAudit handles session audit endpoints.
// GET  /session-audit         — list all audit-generated todos
// POST /session-audit         — trigger audit of a specific task { "taskId": "..." }
// POST /session-audit/all     — trigger audit of all completed tasks
func (s *HTTPServer) handleSessionAudit(w http.ResponseWriter, r *http.Request) {
	if s.sessionAuditor == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "session audit not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List all todo items sourced from session audits
		if s.todolistMgr == nil {
			jsonReply(w, http.StatusOK, map[string]interface{}{"items": []interface{}{}})
			return
		}
		items := s.todolistMgr.ListItemsFull()
		var auditItems []interface{}
		for _, item := range items {
			if item.Source == "session-audit" {
				auditItems = append(auditItems, item)
			}
		}
		if auditItems == nil {
			auditItems = []interface{}{}
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"items": auditItems})

	case http.MethodPost:
		var req struct {
			TaskID string `json:"taskId"`
		}
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&req)
		}
		if req.TaskID == "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "taskId required"})
			return
		}
		items, err := s.sessionAuditor.AuditTaskNow(req.TaskID)
		if err != nil {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"taskId":      req.TaskID,
			"missedItems": items,
			"count":       len(items),
		})

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
	}
}

// handleSessionAuditAll triggers audit of all completed tasks.
// POST /session-audit/all
func (s *HTTPServer) handleSessionAuditAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.sessionAuditor == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "session audit not available"})
		return
	}

	// Run synchronously — audit all completed tasks
	tasks := s.taskMgr.ListTasks()
	totalMissed := 0
	audited := 0

	for _, info := range tasks {
		if info.Status != "completed" {
			continue
		}
		items, err := s.sessionAuditor.AuditTaskNow(info.ID)
		if err == nil {
			totalMissed += len(items)
			audited++
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"tasksAudited": audited,
		"missedItems":  totalMissed,
	})
}

// ListItemsFull returns full todo items (not summaries).
func (tm *TodoListManager) ListItemsFull() []TodoItem {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var items []TodoItem
	for _, item := range tm.items {
		items = append(items, *item)
	}
	return items
}
