package main

// support_http.go — HTTP surface for remote-support sessions.
//
// Owner-only (behind s.auth()):
//   POST /support/start   — open a new window, returns code + token
//   POST /support/stop    — revoke the active session
//   GET  /support/status  — full state incl. code (for host's UI)
//
// Unauthenticated + rate-limited (behind s.rateLimit()):
//   GET  /support/info    — is a session active? metadata only
//   POST /support/redeem  — exchange a 6-char code for a bearer token
//
// The unauth endpoints are how a second party (guest phone, web
// dashboard, another yaver agent) bootstraps into the session without
// needing an owner token first. The code itself is the secret — same
// model as /auth/pair/submit.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *HTTPServer) handleSupportStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		TTL   string `json:"ttl"`
		Label string `json:"label"`
		Shell bool   `json:"shell"` // opt-in for /exec /ws/terminal /browser/* — the "TeamViewer remote help" UX
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	ttl := defaultSupportTTL
	if strings.TrimSpace(body.TTL) != "" {
		if d, err := time.ParseDuration(body.TTL); err == nil && d > 0 {
			ttl = d
		}
	}
	sess := StartSupportSession(SupportStartOptions{
		Label: body.Label,
		TTL:   ttl,
		Shell: body.Shell,
	})
	jsonReply(w, http.StatusOK, supportSessionPayload(sess, s.deviceID, true))
}

func (s *HTTPServer) handleSupportStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"stopped": StopSupportSession(),
	})
}

func (s *HTTPServer) handleSupportStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	sess := activeSupportSnapshot()
	if sess == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"active": false})
		return
	}
	jsonReply(w, http.StatusOK, supportSessionPayload(sess, s.deviceID, true))
}

// handleSupportInfo is UNAUTHENTICATED. Returns only non-secret
// metadata so a prospective guest can confirm a session is actually
// open before they ship a redeem POST.
func (s *HTTPServer) handleSupportInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	sess := activeSupportSnapshot()
	if sess == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"active": false})
		return
	}
	// Never leak the code or token from an unauth endpoint.
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"active":    true,
		"host":      sess.Hostname,
		"label":     sess.Label,
		"deviceId":  s.deviceID,
		"expiresAt": sess.ExpiresAt.UTC().Format(time.RFC3339),
		"allowed":   sess.AllowedPrefixes,
	})
}

// handleSupportRedeem is UNAUTHENTICATED. The code is the secret.
// Rate limited via the s.rateLimit wrapper on registration. Codes are
// reusable within the TTL so a single invite can be opened from a
// phone and a laptop simultaneously.
func (s *HTTPServer) handleSupportRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		code = strings.TrimSpace(r.URL.Query().Get("code"))
	}
	if code == "" {
		jsonError(w, http.StatusBadRequest, "code required")
		return
	}
	sess := supportSessionRedeem(code)
	if sess == nil {
		jsonError(w, http.StatusForbidden, "invalid or expired support code")
		return
	}
	jsonReply(w, http.StatusOK, supportSessionPayload(sess, s.deviceID, true))
}

// supportSessionPayload is the shared JSON shape. includeSecrets
// is true for owner-only status + redeem (caller already proved they
// know the code); false for the public /support/info probe.
func supportSessionPayload(sess *supportSession, deviceID string, includeSecrets bool) map[string]interface{} {
	out := map[string]interface{}{
		"ok":         true,
		"active":     true,
		"host":       sess.Hostname,
		"deviceId":   deviceID,
		"label":      sess.Label,
		"createdAt":  sess.CreatedAt.UTC().Format(time.RFC3339),
		"expiresAt":  sess.ExpiresAt.UTC().Format(time.RFC3339),
		"ttlSeconds": int(time.Until(sess.ExpiresAt).Seconds()),
		"allowed":    sess.AllowedPrefixes,
		"shell":      sess.Shell,
	}
	if includeSecrets {
		out["code"] = sess.Code
		out["token"] = sess.Token
	}
	return out
}
