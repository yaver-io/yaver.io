package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// handleTests handles POST /tests (start) and GET /tests (list).
func (s *HTTPServer) handleTests(w http.ResponseWriter, r *http.Request) {
	if s.testMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "tests not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		sessions := s.testMgr.ListTests()
		jsonReply(w, http.StatusOK, sessions)

	case http.MethodPost:
		var req struct {
			Framework string `json:"framework"`
			Command   string `json:"command"`
			WorkDir   string `json:"workDir"`
			TestType  string `json:"testType"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		ts, err := s.testMgr.StartTest(req.Framework, req.Command, req.WorkDir, req.TestType)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, ts)

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleTestByID handles GET /tests/{id} and sub-routes.
func (s *HTTPServer) handleTestByID(w http.ResponseWriter, r *http.Request) {
	if s.testMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "tests not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/tests/")
	parts := strings.SplitN(path, "/", 2)
	testID := parts[0]

	if testID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing test ID"})
		return
	}

	// Sub-route: stream
	if len(parts) > 1 && parts[1] == "stream" {
		ts, ok := s.testMgr.GetTest(testID)
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "test not found"})
			return
		}
		if ts.ExecID != "" {
			r.URL.Path = "/exec/" + ts.ExecID + "/stream"
			s.handleExecByID(w, r)
			return
		}
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no exec session"})
		return
	}

	ts, ok := s.testMgr.GetTest(testID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "test not found"})
		return
	}

	jsonReply(w, http.StatusOK, ts)
}

// handleAgentWorkdir handles POST /agent/workdir to change working directory at runtime.
func (s *HTTPServer) handleAgentWorkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		WorkDir string `json:"workDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.WorkDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	// Verify directory exists
	if fi, err := os.Stat(req.WorkDir); err != nil || !fi.IsDir() {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "directory not found: " + req.WorkDir})
		return
	}

	// Update task manager and exec manager work directories
	if s.taskMgr != nil {
		s.taskMgr.workDir = req.WorkDir
	}
	if s.execMgr != nil {
		s.execMgr.workDir = req.WorkDir
	}
	if s.buildMgr != nil {
		s.buildMgr.workDir = req.WorkDir
	}
	if s.testMgr != nil {
		s.testMgr.workDir = req.WorkDir
	}
	if s.qualityMgr != nil {
		s.qualityMgr.workDir = req.WorkDir
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "workDir": req.WorkDir})
}

// handleAgentContext returns current project context.
func (s *HTTPServer) handleAgentContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	workDir := ""
	if s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}

	ctx := map[string]interface{}{
		"workDir": workDir,
	}

	// Get git info if in a git repo
	if workDir != "" {
		if branch, err := runCmdDir(workDir, "git", "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			ctx["branch"] = strings.TrimSpace(branch)
		}
		if _, err := runCmdDir(workDir, "git", "ls-files"); err == nil {
			// Detect languages
			framework, _, _ := DetectTestFramework(workDir)
			if framework != "" {
				ctx["testFramework"] = framework
			}
		}
	}

	jsonReply(w, http.StatusOK, ctx)
}
