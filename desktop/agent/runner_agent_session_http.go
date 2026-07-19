package main

// runner_agent_session_http.go — HTTP surface for the
// AgentSessionManager (RUNNER_DEV.md Phase 2).
//
//   GET    /runner/agent/sessions                Live + recent sessions
//   POST   /runner/agent/sessions                Start a new session (AgentSessionStartOpts)
//   GET    /runner/agent/sessions/{id}           One session (with refreshed task state)
//   DELETE /runner/agent/sessions/{id}           Delete (does not stop running task)
//   POST   /runner/agent/sessions/{id}/message   Append a follow-up user message
//   POST   /runner/agent/sessions/{id}/cancel    Stop the in-flight task
//
// Phase 2 is owner-only; agent sessions can write code and call any
// runner the owner has configured, so a guest tier here would mean
// "give me arbitrary code execution on someone else's box." That
// gate stays closed until we have proper per-session sandboxing.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) ensureAgentSessionManager() *AgentSessionManager {
	if s.agentSessionMgr == nil && s.taskMgr != nil {
		s.agentSessionMgr = NewAgentSessionManager(s.taskMgr)
		s.agentSessionMgr.SetPlacementConfig(TaskIngressPlacementConfig{
			ConvexURL:     s.convexURL,
			Token:         s.token,
			LocalDeviceID: s.deviceID,
			WorkDir:       s.taskMgr.workDir,
		})
	}
	return s.agentSessionMgr
}

func (s *HTTPServer) handleRunnerAgentSessions(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "agent sessions are owner-only in Phase 2"})
		return
	}
	mgr := s.ensureAgentSessionManager()
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent sessions unavailable — TaskManager not wired",
		})
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions := mgr.List("")
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"sessions": sessions,
			"count":    len(sessions),
		})
	case http.MethodPost:
		var opts AgentSessionStartOpts
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		sess, err := mgr.Create(opts, s.ownerUserID)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *HTTPServer) handleRunnerAgentSessionByID(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "agent sessions are owner-only in Phase 2"})
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/runner/agent/sessions/")
	tail = strings.TrimSuffix(tail, "/")
	if tail == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}
	parts := strings.SplitN(tail, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	mgr := s.ensureAgentSessionManager()
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent sessions unavailable",
		})
		return
	}
	switch sub {
	case "":
		s.handleAgentSessionOne(w, r, mgr, id)
	case "message":
		s.handleAgentSessionMessage(w, r, mgr, id)
	case "cancel":
		s.handleAgentSessionCancel(w, r, mgr, id)
	default:
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource: " + sub})
	}
}

func (s *HTTPServer) handleAgentSessionOne(w http.ResponseWriter, r *http.Request, mgr *AgentSessionManager, id string) {
	switch r.Method {
	case http.MethodGet:
		sess, ok := mgr.Get(id, "")
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		jsonReply(w, http.StatusOK, sess)
	case http.MethodDelete:
		if err := mgr.Delete(id, ""); err != nil {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *HTTPServer) handleAgentSessionMessage(w http.ResponseWriter, r *http.Request, mgr *AgentSessionManager, id string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	sess, err := mgr.Message(id, body.Text, "")
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
}

func (s *HTTPServer) handleAgentSessionCancel(w http.ResponseWriter, r *http.Request, mgr *AgentSessionManager, id string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := mgr.Cancel(id, ""); err != nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]bool{"ok": true, "cancelled": true})
}
