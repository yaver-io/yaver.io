package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// host_share_reaper.go — "removable allocation" teardown for host-share /
// operator-fleet tenants.
//
// Background (the gap this closes): creating a tenant slice works and
// revoking NEW requests is fast (~guestTokenCacheTTL), but nothing ever
// tore a slice DOWN — the per-session workspace persisted forever, and a
// revoked tenant's running processes (their coding agent / dev servers)
// kept holding CPU/RAM/ports/secrets until they happened to idle out (a
// busy process never does). For an operator fleet serving strangers that
// is both a privacy leak (their code stays on our disk; the next tenant
// could see residue) and a safety hole.
//
// The reaper runs on the agent's existing 10s refresh loop. It asks Convex
// for the set of host-share sessions still ACTIVE on THIS device, then:
//   1. hard-kills any live terminal session whose hostShareID is no longer
//      active (close(true) → process.Kill()), and
//   2. deletes any on-disk workspace whose session is no longer active.
// Combined with the server-side cron that flips expired/idle sessions to a
// terminal status, a revoked or expired allocation is fully removed within
// one reap cycle.

type hostShareSessionRow struct {
	SessionID    string `json:"sessionId"`
	Status       string `json:"status"`
	HostDeviceID string `json:"hostDeviceId"`
}

// FetchActiveHostShareSessionIDs returns the set of host-share session ids
// that are active for the caller (host) AND bound to deviceID. The caller's
// token is the host/operator token (s.token). Sessions for the host's other
// devices are filtered out so the reaper never touches another box's slices.
func FetchActiveHostShareSessionIDs(baseURL, token, deviceID string) (map[string]bool, error) {
	url := strings.TrimRight(baseURL, "/") + "/host-share/sessions?role=host"
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create host-share sessions request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share sessions request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("host-share sessions: %s", string(body))
	}
	var result struct {
		Sessions []hostShareSessionRow `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share sessions: %w", err)
	}
	dev := strings.TrimSpace(deviceID)
	active := make(map[string]bool, len(result.Sessions))
	for _, row := range result.Sessions {
		if row.Status != "active" {
			continue
		}
		// listSessions already filters status=active by index, but be
		// defensive. Only keep sessions hosted on THIS device (empty
		// hostDeviceId = legacy/unspecified → keep, can't prove otherwise).
		if dev != "" && strings.TrimSpace(row.HostDeviceID) != "" && row.HostDeviceID != dev {
			continue
		}
		if id := strings.TrimSpace(row.SessionID); id != "" {
			active[id] = true
		}
	}
	return active, nil
}

// killHostShareSession hard-kills every live terminal session bound to the
// given host-share sessionID. Returns the count killed. Safe to call for an
// unknown id (no-op).
func (s *HTTPServer) killHostShareSession(hostShareID string) int {
	hostShareID = strings.TrimSpace(hostShareID)
	if hostShareID == "" {
		return 0
	}
	var victims []*terminalSession
	s.terminalSessions.Range(func(_, v any) bool {
		if ts, ok := v.(*terminalSession); ok && ts.hostShareID == hostShareID {
			victims = append(victims, ts)
		}
		return true
	})
	for _, ts := range victims {
		ts.close(true)
	}
	return len(victims)
}

// reapHostShareSessions is one teardown pass: given the set of still-active
// host-share session ids, hard-kill live terminals and delete workspaces for
// everything else. A nil active set is a no-op (an errored fetch passes nil →
// we skip rather than nuke live tenants).
func (s *HTTPServer) reapHostShareSessions(active map[string]bool) {
	if active == nil {
		return
	}
	// 1. Kill live terminals whose host-share session is no longer active.
	var revoked []string
	s.terminalSessions.Range(func(_, v any) bool {
		ts, ok := v.(*terminalSession)
		if !ok {
			return true
		}
		id := strings.TrimSpace(ts.hostShareID)
		if id == "" {
			return true // not a host-share terminal — leave it alone
		}
		if !active[id] {
			revoked = append(revoked, id)
		}
		return true
	})
	for _, id := range revoked {
		if killed := s.killHostShareSession(id); killed > 0 {
			log.Printf("[HOSTSHARE-REAP] killed %d terminal(s) for revoked session %s", killed, id)
		}
	}

	// 2. Wipe on-disk workspaces whose session is no longer active.
	if s.hostShareWorkspaceMgr != nil {
		keep := make(map[string]bool, len(active))
		for id := range active {
			keep[s.hostShareWorkspaceMgr.SanitizeSessionID(id)] = true
		}
		if removed, err := s.hostShareWorkspaceMgr.ReapExcept(keep); err != nil {
			log.Printf("[HOSTSHARE-REAP] workspace scrub: %v", err)
		} else if len(removed) > 0 {
			log.Printf("[HOSTSHARE-REAP] wiped %d stale workspace(s): %s", len(removed), strings.Join(removed, ", "))
		}
	}
}

// hasLocalHostShareState reports whether this box currently has anything to
// reap (a live host-share terminal or any workspace dir on disk). Lets the
// refresh loop skip the network call entirely on normal single-owner boxes.
func (s *HTTPServer) hasLocalHostShareState() bool {
	has := false
	s.terminalSessions.Range(func(_, v any) bool {
		if ts, ok := v.(*terminalSession); ok && strings.TrimSpace(ts.hostShareID) != "" {
			has = true
			return false
		}
		return true
	})
	if has {
		return true
	}
	if s.hostShareWorkspaceMgr != nil {
		s.hostShareWorkspaceMgr.mu.Lock()
		names, _ := s.hostShareWorkspaceMgr.listSessionDirsLocked()
		s.hostShareWorkspaceMgr.mu.Unlock()
		if len(names) > 0 {
			return true
		}
	}
	return false
}

// runHostShareReap performs one reap cycle if there's anything to reap.
// Called from the refreshGuestList ticker. In operator mode it always runs
// (so a fleet box scrubs promptly even between tenants).
func (s *HTTPServer) runHostShareReap() {
	if !s.operatorMode && !s.hasLocalHostShareState() {
		return
	}
	active, err := FetchActiveHostShareSessionIDs(s.convexURL, s.token, s.deviceID)
	if err != nil {
		// Fail safe: do NOT wipe on a fetch error (could be transient).
		log.Printf("[HOSTSHARE-REAP] skip (fetch active sessions: %v)", err)
		return
	}
	s.reapHostShareSessions(active)
}
