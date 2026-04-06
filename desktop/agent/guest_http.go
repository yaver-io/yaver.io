package main

import (
	"encoding/json"
	"net/http"
)

// handleGuestList returns the host's guest list (GET /guests).
func (s *HTTPServer) handleGuestList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	guests, err := FetchGuestList(s.convexURL, s.token)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch guest list: "+err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"guests": guests,
	})
}

// handleGuestInvite invites a guest by email (POST /guests/invite).
func (s *HTTPServer) handleGuestInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		jsonError(w, http.StatusBadRequest, "email is required")
		return
	}

	if err := InviteGuest(s.convexURL, s.token, body.Email); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Refresh guest list immediately
	if ids, err := FetchGuestUserIds(s.convexURL, s.token); err == nil {
		s.guestUserIDsMu.Lock()
		s.guestUserIDs = ids
		s.guestUserIDsMu.Unlock()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Invitation sent to " + body.Email,
	})
}

// handleGuestRevoke revokes guest access (POST /guests/revoke).
func (s *HTTPServer) handleGuestRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		jsonError(w, http.StatusBadRequest, "email is required")
		return
	}

	if err := RevokeGuest(s.convexURL, s.token, body.Email); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Refresh guest list immediately
	if ids, err := FetchGuestUserIds(s.convexURL, s.token); err == nil {
		s.guestUserIDsMu.Lock()
		s.guestUserIDs = ids
		s.guestUserIDsMu.Unlock()
	}

	// Clear token cache so revoked guests are rejected immediately
	s.tokenCache.Range(func(key, value interface{}) bool {
		info := value.(*cachedTokenInfo)
		if info.userID != s.ownerUserID && !info.isSdk {
			s.tokenCache.Delete(key)
		}
		return true
	})

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Guest access revoked for " + body.Email,
	})
}
