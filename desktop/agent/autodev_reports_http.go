package main

// autodev_reports_http.go — HTTP surface for the autodev per-run
// JSON reports produced by autodev_cmd.go. All endpoints are
// authenticated via the existing auth() middleware and served
// over the agent's normal P2P/relay path, so mobile, desktop
// Electron, and the web dashboard all read the same data without
// any Convex roundtrip.
//
// GET  /autodev/reports             — list every saved report (summary)
// GET  /autodev/reports?name=X      — one report in full (kicks + deploy)
// POST /autodev/reports/revert      — body {name, commit_shas:[...]}
//                                     runs `git revert --no-edit` for each
//                                     SHA in the loop's work dir, then push.
//
// Handler registration lives in httpserver.go (mux.HandleFunc),
// same pattern as /auth/pair/*.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleAutodevReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	name := r.URL.Query().Get("name")
	if name != "" {
		rep, err := LoadAutodevReport(name)
		if err != nil {
			jsonError(w, http.StatusNotFound, "no report for "+name)
			return
		}
		jsonReply(w, http.StatusOK, rep)
		return
	}
	reports, err := ListAutodevReports()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"reports": reports,
	})
}

func (s *HTTPServer) handleAutodevRevert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Name       string   `json:"name"`
		CommitSHAs []string `json:"commit_shas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" || len(body.CommitSHAs) == 0 {
		jsonError(w, http.StatusBadRequest, "name and commit_shas required")
		return
	}
	// Reject anything that doesn't look like a git SHA to avoid
	// passing shell metacharacters or unrelated refs to git revert.
	for _, sha := range body.CommitSHAs {
		if !isProbableGitSHA(sha) {
			jsonError(w, http.StatusBadRequest, "not a git SHA: "+sha)
			return
		}
	}
	if err := RevertAutodevCommits(body.Name, body.CommitSHAs); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"reverted": body.CommitSHAs,
	})
}

func isProbableGitSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	s = strings.ToLower(s)
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
