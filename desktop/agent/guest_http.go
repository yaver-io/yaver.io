package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

// filterTestableProjects is the pure core of GET /guest/testable: given the
// host's discovered projects and a tester's grant, it returns only the projects
// the tester may run (allowedProjects; empty = all), tagging each with whether a
// dev server is currently live for it and whether the tester may vibe it. Kept
// pure (no scan / no *HTTPServer) so it is deterministically unit-testable.
func filterTestableProjects(projects []MobileProject, mgr *GuestConfigManager, guestUID, activeWorkDir string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0)
	if mgr == nil || strings.TrimSpace(guestUID) == "" {
		return out
	}
	canVibe := mgr.GuestCanVibe(guestUID)
	for _, mp := range projects {
		if !mgr.GuestCanAccessProject(guestUID, mp.Name) {
			continue
		}
		devActive := activeWorkDir != "" &&
			(strings.EqualFold(filepath.Base(activeWorkDir), mp.Name) ||
				filepath.Clean(activeWorkDir) == filepath.Clean(mp.Path))
		out = append(out, map[string]interface{}{
			"name":            mp.Name,
			"framework":       mp.Framework,
			"canVibe":         canVibe,
			"devServerActive": devActive,
		})
	}
	return out
}

// handleGuestTestableProjects (GET /guest/testable) lets an invited tester
// discover which of the host's projects they're allowed to run — the input to
// the mobile "test my friend's app" screen. Only projects in the tester's
// allowedProjects are returned (empty = all discovered projects). A non-guest
// caller (the owner) gets an empty list; the owner uses /projects.
func (s *HTTPServer) handleGuestTestableProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID == "" || s.guestConfigMgr == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "projects": []interface{}{}})
		return
	}
	activeWorkDir := ""
	if s.devServerMgr != nil {
		if st := s.devServerMgr.Status(); st != nil {
			activeWorkDir = strings.TrimSpace(st.WorkDir)
		}
	}
	projects := filterTestableProjects(scanMobileProjects(), s.guestConfigMgr, guestUID, activeWorkDir)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":              true,
		"projects":        projects,
		"allowedProjects": s.guestConfigMgr.AllowedProjects(guestUID),
	})
}

// funnyVibeCommitMessage builds the default commit line for a tester's saved
// vibe. Keep it light — this is a friend improving a friend's app, not a
// corporate changelog. Picked deterministically from the sha-free inputs so
// there's no rand dependency.
func funnyVibeCommitMessage(friend, project string) string {
	if strings.TrimSpace(friend) == "" {
		friend = "a friend"
	}
	if strings.TrimSpace(project) == "" {
		project = "the app"
	}
	quips := []string{
		"✨ %s vibed on %s (blame them, not me)",
		"🎸 %s improved %s via Yaver — ship it",
		"🪄 %s sprinkled some vibes on %s",
		"🚀 %s couldn't resist tweaking %s",
	}
	// Deterministic pick: length of friend+project mod len(quips).
	idx := (len(friend) + len(project)) % len(quips)
	return fmt.Sprintf(quips[idx], friend, project)
}

// handleGuestVibeSave (POST /guest/vibe-save) commits a canVibe tester's changes
// straight onto the project's current branch, attributed to the friend, with a
// funny message, then best-effort pushes. Project-gated (guestResolveDevWorkDir)
// and canVibe-gated — a tester who wasn't opted into vibe can't reach it.
func (s *HTTPServer) handleGuestVibeSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID == "" || s.guestConfigMgr == nil {
		jsonError(w, http.StatusForbidden, "guests only")
		return
	}
	if !s.guestConfigMgr.GuestCanVibe(guestUID) {
		jsonError(w, http.StatusForbidden, "vibe is not enabled for this tester")
		return
	}
	var req struct {
		ProjectName string `json:"projectName"`
		Message     string `json:"message"`
	}
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	// Reuses the /dev project gate: resolves + enforces allowedProjects.
	workDir, err := s.guestResolveDevWorkDir(r, req.ProjectName, "")
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	cfg := s.guestConfigMgr.GetConfig(guestUID)
	friend := "a friend"
	email := "tester@yaver.io"
	if cfg != nil {
		if strings.TrimSpace(cfg.GuestName) != "" {
			friend = cfg.GuestName
		}
		if strings.TrimSpace(cfg.GuestEmail) != "" {
			email = cfg.GuestEmail
		}
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		msg = funnyVibeCommitMessage(friend, filepath.Base(workDir))
	}
	sha, pushed, err := commitAndPushGuestVibe(workDir, friend, email, msg)
	if err == errNothingToCommit {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "committed": false, "message": "nothing to save — no changes yet"})
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"committed": true,
		"sha":       sha,
		"pushed":    pushed,
		"message":   msg,
	})
}

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
		CanVibe         bool     `json:"canVibe"`
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
	// canVibe (AI-improve) is only meaningful for the tester tier. Reject the
	// nonsensical combo up front so the host gets a clear error rather than a
	// silently-ignored flag.
	if body.CanVibe && strings.TrimSpace(body.Scope) != GuestScopeSDKProject {
		jsonError(w, http.StatusBadRequest, "canVibe requires scope 'sdk-project'")
		return
	}

	result, err := InviteGuestWith(s.convexURL, s.token, InviteGuestOpts{
		Email:             strings.TrimSpace(body.Email),
		UserID:            strings.TrimSpace(body.UserID),
		ProposedDeviceIDs: body.DeviceIDs,
		Scope:             strings.TrimSpace(body.Scope),
		AllowedProjects:   cleanProjectList(body.AllowedProjects),
		CanVibe:           body.CanVibe,
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
