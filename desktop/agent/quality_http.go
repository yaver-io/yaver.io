package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleQualityDetect handles GET /quality/detect — detect available quality checks.
func (s *HTTPServer) handleQualityDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	workDir := r.URL.Query().Get("workDir")
	if workDir == "" && s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	checks := DetectQualityChecks(workDir)
	jsonReply(w, http.StatusOK, checks)
}

// handleQualityRun handles POST /quality/run — run a single quality check.
func (s *HTTPServer) handleQualityRun(w http.ResponseWriter, r *http.Request) {
	if s.qualityMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "quality checks not available"})
		return
	}
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	var req struct {
		Type    string `json:"type"`
		WorkDir string `json:"workDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	checkType := QualityCheckType(req.Type)
	if checkType != QualityLint && checkType != QualityTypeCheck && checkType != QualityFormat && checkType != QualityTest {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid type: use lint, typecheck, format, or test"})
		return
	}

	result, err := s.qualityMgr.RunQualityCheck(checkType, req.WorkDir)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	jsonReply(w, http.StatusOK, result)
}

// handleQualityRunAll handles POST /quality/run-all — run all available quality checks.
func (s *HTTPServer) handleQualityRunAll(w http.ResponseWriter, r *http.Request) {
	if s.qualityMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "quality checks not available"})
		return
	}
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	var req struct {
		WorkDir string `json:"workDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	results, err := s.qualityMgr.RunAllQualityChecks(req.WorkDir)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	jsonReply(w, http.StatusOK, results)
}

// handleQualityResults handles GET /quality/results — list all quality results.
func (s *HTTPServer) handleQualityResults(w http.ResponseWriter, r *http.Request) {
	if s.qualityMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "quality checks not available"})
		return
	}
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	results := s.qualityMgr.ListResults()
	jsonReply(w, http.StatusOK, results)
}

// handleQualityResultByID handles GET /quality/results/{id} and /quality/results/{id}/stream.
func (s *HTTPServer) handleQualityResultByID(w http.ResponseWriter, r *http.Request) {
	if s.qualityMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "quality checks not available"})
		return
	}
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/quality/results/")
	parts := strings.SplitN(path, "/", 2)
	resultID := parts[0]

	if resultID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing result ID"})
		return
	}

	// Sub-route: stream
	if len(parts) > 1 && parts[1] == "stream" {
		result, ok := s.qualityMgr.GetResult(resultID)
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "result not found"})
			return
		}
		if result.ExecID != "" {
			r.URL.Path = "/exec/" + result.ExecID + "/stream"
			s.handleExecByID(w, r)
			return
		}
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no exec session"})
		return
	}

	result, ok := s.qualityMgr.GetResult(resultID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "result not found"})
		return
	}

	jsonReply(w, http.StatusOK, result)
}
