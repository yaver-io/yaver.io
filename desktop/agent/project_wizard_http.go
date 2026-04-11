package main

// project_wizard_http.go — HTTP surface for the project wizard.
// Same state machine that `yaver new` drives on the terminal,
// now reachable by the mobile app, the web dashboard, the
// desktop Electron app, and MCP clients.

import (
	"encoding/json"
	"net/http"
)

func (s *HTTPServer) handleWizardStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use POST or GET")
		return
	}
	sess, q := StartWizard()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"session":  sess,
		"question": q,
	})
}

func (s *HTTPServer) handleWizardAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		SessionID  string `json:"sessionId"`
		QuestionID string `json:"questionId"`
		Answer     string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	q, err := AnswerWizard(body.SessionID, body.QuestionID, body.Answer)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess := GetWizard(body.SessionID)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"session":  sess,
		"question": q,
	})
}

func (s *HTTPServer) handleWizardGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
		ParentDir string `json:"parentDir,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	res, err := GenerateProject(body.SessionID, body.ParentDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, res)
}

func (s *HTTPServer) handleWizardSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	id := r.URL.Query().Get("id")
	sess := GetWizard(id)
	if sess == nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"session":  sess,
		"question": nextQuestion(sess),
	})
}

func (s *HTTPServer) handleWizardQuestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"questions": wizardQuestions,
	})
}
