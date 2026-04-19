package main

import (
	"encoding/json"
	"net/http"
)

func (s *HTTPServer) handleConversationImportPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body ConversationImportRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.WorkDir == "" {
		body.WorkDir = "."
	}
	out, err := AnalyzeConversationImport(body)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
