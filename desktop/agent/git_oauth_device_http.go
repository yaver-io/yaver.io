package main

// git_oauth_device_http.go — HTTP handlers for the GitHub/GitLab Device
// Flow. Wire-compatible with /peer/<id>/git/provider/oauth/* via the
// existing peer-proxy, so mobile/web/CLI can drive a Device Flow on any
// owned remote box without inventing a new transport.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleGitProviderOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Host     string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		jsonError(w, http.StatusBadRequest, "provider is required (github|gitlab)")
		return
	}

	sess, err := startGitOAuthDevice(r.Context(), provider, strings.TrimSpace(req.Host))
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	jsonReply(w, http.StatusOK, map[string]any{
		"ok":               true,
		"session_id":       sess.ID,
		"provider":         sess.Provider,
		"host":             sess.Host,
		"user_code":        sess.UserCode,
		"verification_uri": sess.VerificationURI,
		"interval":         sess.Interval,
		"expires_at":       sess.ExpiresAt.Unix(),
		"byo_client":       sess.BYOClient,
	})
}

func (s *HTTPServer) handleGitProviderOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		jsonError(w, http.StatusBadRequest, "missing session query param")
		return
	}
	sess, ok := getGitOAuthSession(sessionID)
	if !ok {
		jsonReply(w, http.StatusOK, map[string]any{
			"ok":    false,
			"state": "unknown",
			"error": "session not found (may have been reaped)",
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":               true,
		"session_id":       sess.ID,
		"provider":         sess.Provider,
		"host":             sess.Host,
		"user_code":        sess.UserCode,
		"verification_uri": sess.VerificationURI,
		"interval":         sess.Interval,
		"expires_at":       sess.ExpiresAt.Unix(),
		"state":            sess.State,
		"username":         sess.Username,
		"error":            sess.Error,
		"byo_client":       sess.BYOClient,
	})
}
