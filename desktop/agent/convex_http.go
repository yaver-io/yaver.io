package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
)

// dirParam returns the ?directory=... query param, falling back to the agent's cwd.
func (s *HTTPServer) dirParam(r *http.Request) string {
	if d := r.URL.Query().Get("directory"); d != "" {
		return d
	}
	if d := r.URL.Query().Get("dir"); d != "" {
		return d
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func (s *HTTPServer) handleConvexStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpConvexLocalStatus(s.dirParam(r)))
}

func (s *HTTPServer) handleConvexTables(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpConvexTables(s.dirParam(r)))
}

func (s *HTTPServer) handleConvexBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	writeJSON(w, http.StatusOK, mcpConvexBrowse(s.dirParam(r), q.Get("table"), q.Get("cursor"), limit))
}

type convexCallBody struct {
	Function string                 `json:"function"`
	Args     map[string]interface{} `json:"args"`
}

func (s *HTTPServer) decodeCall(r *http.Request) convexCallBody {
	var b convexCallBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	return b
}

func argsToJSON(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	b, _ := json.Marshal(args)
	return string(b)
}

func (s *HTTPServer) handleConvexQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	b := s.decodeCall(r)
	writeJSON(w, http.StatusOK, mcpConvexAdminQuery(s.dirParam(r), b.Function, argsToJSON(b.Args)))
}

func (s *HTTPServer) handleConvexMutate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	b := s.decodeCall(r)
	writeJSON(w, http.StatusOK, mcpConvexAdminMutate(s.dirParam(r), b.Function, argsToJSON(b.Args)))
}

func (s *HTTPServer) handleConvexAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	b := s.decodeCall(r)
	writeJSON(w, http.StatusOK, mcpConvexAdminAction(s.dirParam(r), b.Function, argsToJSON(b.Args)))
}

func (s *HTTPServer) handleConvexSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpConvexSchema(s.dirParam(r)))
}

func (s *HTTPServer) handleConvexExport(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpConvexExport(s.dirParam(r)))
}

func (s *HTTPServer) handleConvexInstallHelper(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	writeJSON(w, http.StatusOK, mcpConvexInstallHelper(s.dirParam(r)))
}
