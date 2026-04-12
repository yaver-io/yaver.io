package main

import (
	"encoding/json"
	"net/http"
)

var globalSwitchEngine = NewSwitchEngine()

// ---------- MCP handlers ----------

func mcpSwitchTargets() interface{} {
	return map[string]interface{}{"targets": SwitchTargets()}
}

func mcpSwitchPlan(dir, targetID string, dryRun bool) interface{} {
	s, err := globalSwitchEngine.Plan(dir, targetID, dryRun)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := globalSwitchEngine.Persist(s); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return s
}

func mcpSwitchRun(dir, id string) interface{} {
	s, err := globalSwitchEngine.Load(dir, id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := globalSwitchEngine.Run(s); err != nil {
		return map[string]interface{}{"error": err.Error(), "state": s}
	}
	return s
}

func mcpSwitchRollback(dir, id string) interface{} {
	s, err := globalSwitchEngine.Rollback(dir, id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return s
}

func mcpSwitchHistory(dir string) interface{} {
	h, err := globalSwitchEngine.History(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"switches": h}
}

func mcpSwitchCleanup(dir string) interface{} {
	out, err := globalSwitchEngine.Cleanup(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"output": out}
}

// ---------- HTTP handlers ----------

func (s *HTTPServer) handleSwitchTargets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpSwitchTargets())
}

type switchPlanBody struct {
	Target string `json:"target"`
	DryRun bool   `json:"dryRun"`
}

func (s *HTTPServer) handleSwitchPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b switchPlanBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpSwitchPlan(s.dirParam(r), b.Target, b.DryRun))
}

type switchIDBody struct {
	ID string `json:"id"`
}

func (s *HTTPServer) handleSwitchRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b switchIDBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpSwitchRun(s.dirParam(r), b.ID))
}

func (s *HTTPServer) handleSwitchRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b switchIDBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpSwitchRollback(s.dirParam(r), b.ID))
}

func (s *HTTPServer) handleSwitchHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpSwitchHistory(s.dirParam(r)))
}

func (s *HTTPServer) handleSwitchCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	writeJSON(w, http.StatusOK, mcpSwitchCleanup(s.dirParam(r)))
}
