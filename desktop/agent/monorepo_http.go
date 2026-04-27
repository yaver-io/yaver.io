package main

// HTTP surface for monorepo detection: GET /projects/monorepo?dir=<path>.
// Returns a Monorepo JSON describing the requested directory's framework
// composition. Owner-auth — guests are blocked because the response includes
// absolute paths.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleMonorepoDetect serves GET /projects/monorepo[?dir=<path>][&maxDepth=<n>].
// Defaults dir to the agent's current work directory when omitted, which
// matches what `/projects` already does for the per-project pipeline.
func (s *HTTPServer) handleMonorepoDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		// dir = s.workDir (workDir field removed)
	}
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid dir: " + err.Error()})
		return
	}

	// Light bound — the detector itself defaults to 6 when 0 is passed.
	maxDepth := 0
	if md := r.URL.Query().Get("maxDepth"); md != "" {
		if n, err := parseSmallInt(md, 1, 12); err == nil {
			maxDepth = n
		}
	}

	mr, err := DetectMonorepo(abs, DetectOpts{MaxDepth: maxDepth})
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(mr)
}

// parseSmallInt parses a positive int within [lo, hi]. Returns an error on
// out-of-range or non-numeric input.
func parseSmallInt(s string, lo, hi int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &smallIntErr{}
		}
		n = n*10 + int(c-'0')
		if n > hi {
			return 0, &smallIntErr{}
		}
	}
	if n < lo {
		return 0, &smallIntErr{}
	}
	return n, nil
}

type smallIntErr struct{}

func (e *smallIntErr) Error() string { return "out of range" }
