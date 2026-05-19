package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// handlePhoneShare (POST /phone/projects/share) mints a join code for
// a project so a friend can preview it. Body: {slug, ttlMinutes?}.
// The mobile "Share with a friend" button is a thin caller of this.
func (s *HTTPServer) handlePhoneShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug       string `json:"slug"`
		TTLMinutes int    `json:"ttlMinutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Slug == "" {
		jsonError(w, http.StatusBadRequest, "slug required")
		return
	}
	ttl := time.Duration(body.TTLMinutes) * time.Minute
	sh, err := CreatePhoneShare(body.Slug, ttl)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// handlePhoneJoin (GET /phone/projects/join?code=XXXX) resolves a code
// to {slug, name, hostedConvexUrl, bundleUrl}. The friend's client
// then fetches bundleUrl (the .zip), Hermes-loads it, and points it at
// hostedConvexUrl — sharing the host's live backend with zero config.
func (s *HTTPServer) handlePhoneJoin(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		jsonError(w, http.StatusBadRequest, "code required")
		return
	}
	sh, err := ResolvePhoneShare(code)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrPhoneShareNotFound) {
			status = http.StatusNotFound
		}
		jsonError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sh)
}
