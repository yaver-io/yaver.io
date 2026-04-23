package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func rejectGuestManagementCall(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonError(w, http.StatusForbidden, "guests cannot manage host sharing settings")
		return true
	}
	return false
}

// handleGuestList returns the host's guest list (GET /guests).
func (s *HTTPServer) handleGuestList(w http.ResponseWriter, r *http.Request) {
	if rejectGuestManagementCall(w, r) {
		return
	}
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

// handleGuestInvite invites a guest by email or public user id (POST /guests/invite).
func (s *HTTPServer) handleGuestInvite(w http.ResponseWriter, r *http.Request) {
	if rejectGuestManagementCall(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		Email           string   `json:"email"`
		UserID          string   `json:"userId"`
		DeviceIDs       []string `json:"deviceIds"`
		Scope           string   `json:"scope"`
		AllowedProjects []string `json:"allowedProjects"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.Email) == "" && strings.TrimSpace(body.UserID) == "" {
		jsonError(w, http.StatusBadRequest, "email or userId is required")
		return
	}
	if body.Scope != "" && body.Scope != GuestScopeFull && body.Scope != GuestScopeFeedbackOnly && body.Scope != GuestScopeSDKProject {
		jsonError(w, http.StatusBadRequest, "scope must be 'full', 'feedback-only', or 'sdk-project'")
		return
	}

	result, err := InviteGuestWith(s.convexURL, s.token, InviteGuestOpts{
		Email:             strings.TrimSpace(body.Email),
		UserID:            strings.TrimSpace(body.UserID),
		ProposedDeviceIDs: body.DeviceIDs,
		Scope:             strings.TrimSpace(body.Scope),
		AllowedProjects:   cleanProjectList(body.AllowedProjects),
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Refresh guest list immediately
	if ids, err := FetchGuestUserIds(s.convexURL, s.token, s.deviceID); err == nil {
		s.guestUserIDsMu.Lock()
		s.guestUserIDs = ids
		s.guestUserIDsMu.Unlock()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":              true,
		"message":         "Invitation sent",
		"inviteCode":      result.InviteCode,
		"guestRegistered": result.GuestRegistered,
		"scope":           result.Scope,
	})
}

// handleGuestRevoke revokes guest access (POST /guests/revoke).
func (s *HTTPServer) handleGuestRevoke(w http.ResponseWriter, r *http.Request) {
	if rejectGuestManagementCall(w, r) {
		return
	}
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
	if ids, err := FetchGuestUserIds(s.convexURL, s.token, s.deviceID); err == nil {
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
