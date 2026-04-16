package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type browserSession struct {
	PathPrefix string
	ExpiresAt  time.Time
}

func newBrowserSessionToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *HTTPServer) issueBrowserSession(pathPrefix string, ttl time.Duration) (string, time.Time, error) {
	token, err := newBrowserSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(ttl)
	s.browserSessions.Store(token, browserSession{
		PathPrefix: pathPrefix,
		ExpiresAt:  expiresAt,
	})
	return token, expiresAt, nil
}

func (s *HTTPServer) validateBrowserSession(token, path string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	v, ok := s.browserSessions.Load(token)
	if !ok {
		return false
	}
	session := v.(browserSession)
	if time.Now().After(session.ExpiresAt) {
		s.browserSessions.Delete(token)
		return false
	}
	return strings.HasPrefix(path, session.PathPrefix)
}

func (s *HTTPServer) pruneBrowserSessions() {
	now := time.Now()
	s.browserSessions.Range(func(key, value interface{}) bool {
		session, ok := value.(browserSession)
		if !ok || now.After(session.ExpiresAt) {
			s.browserSessions.Delete(key)
		}
		return true
	})
}

func (s *HTTPServer) handleBrowserSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		PathPrefix string `json:"pathPrefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !isAllowedBrowserSessionPath(body.PathPrefix) {
		jsonError(w, http.StatusBadRequest, "unsupported browser session path")
		return
	}

	s.pruneBrowserSessions()
	token, expiresAt, err := s.issueBrowserSession(body.PathPrefix, 2*time.Minute)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to issue browser session")
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"token":     token,
		"expiresAt": expiresAt.UTC().Format(time.RFC3339),
	})
}

func isAllowedBrowserSessionPath(path string) bool {
	switch {
	case path == "/ws/metrics":
		return true
	case path == "/ws/logs":
		return true
	case path == "/ws/terminal":
		return true
	case strings.HasPrefix(path, "/proxy/"):
		return true
	default:
		return false
	}
}
