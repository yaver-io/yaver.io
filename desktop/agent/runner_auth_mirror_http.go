package main

// runner_auth_mirror_http.go — HTTP edges for the token-mirror flow.
//
//   POST /runner/auth/mirror/request   — orchestration entry (glass / phone / web ask "push my Mac's token to box X")
//   POST /runner/auth/mirror/accept    — target receives plaintext from source (owner-auth gated, NOT authSDK)
//   GET  /runner/auth/ledger           — sanitized metadata for the mobile + /spatial UIs
//   POST /runner/auth/ledger/revoke    — remove a ledger entry (does NOT delete the runner's credential file)
//
// Glass clients never POST plaintext — they only POST `mirror/request`
// (no secret value crosses the glass surface). The source-agent
// performs the actual file read + push.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleRunnerAuthMirrorRequest accepts a request to mirror a token
// from a source device to a target device. For Phase 1 we only
// support local-source mirror — the caller's machine reads its own
// credentials and writes them locally OR forwards to a peer agent.
// Cross-device fan-out (Mac → cloud-1) lives behind the same handler
// once the QUIC peer-proxy is wired (Phase 2).
func (s *HTTPServer) handleRunnerAuthMirrorRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req MirrorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	runner := normalizeRunnerAuthName(req.Runner)
	if runner != "claude" && runner != "codex" {
		jsonError(w, http.StatusBadRequest, "runner must be claude or codex")
		return
	}
	cred, err := ReadLocalRunnerCredential(runner)
	if err != nil {
		// No local credential — the caller should fall back to the
		// phone-relay device-auth flow (existing
		// runner_auth_browser_http.go). We emit a structured signal
		// so glass clients can speak the right next step.
		jsonReply(w, http.StatusOK, map[string]any{
			"ok":          false,
			"runner":      runner,
			"reason":      "no_local_credential",
			"nextAction":  "phone_relay_device_auth",
			"message":     fmt.Sprintf("no signed-in %s on this machine — start phone-relay flow from Yaver mobile", runner),
		})
		return
	}
	// Local accept path: write to our own credentials.json directly
	// (no peer round trip needed when target == local). This is the
	// common path during early dev / single-box use.
	payload := MirrorAcceptPayload{
		Runner:          runner,
		CredentialsFile: base64StdEncode(cred.FileBytes),
		SourceHost:      hostnameOrUnknown(),
		ExpiresAtTS:     timeToMilli(cred.ExpiresAt),
	}
	result, accErr := AcceptMirrorPayload(r.Context(), payload)
	if accErr != nil {
		jsonError(w, http.StatusInternalServerError, "accept: "+accErr.Error())
		return
	}
	// Surface a "completed" blackbox event so any paired glass / phone
	// listener stops nagging the user about reauth.
	if s.blackboxMgr != nil {
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "runner_auth_completed",
			Data: map[string]interface{}{
				"runner":    runner,
				"tokenHash": result.TokenHash,
				"source":    result.SourceHost,
				"path":      result.WrittenTo,
			},
		})
	}
	jsonReply(w, http.StatusOK, result)
}

// handleRunnerAuthMirrorAccept receives an inbound credentials.json
// from a peer agent (the source). Validates the payload + writes to
// disk + appends a ledger entry.
//
// Owner auth only — SDK tokens cannot push runner credentials.
// Existing s.auth() middleware enforces this since SDK tokens are
// distinguished at the auth layer; here we just guard against a
// guest having somehow reached the route by checking
// X-Yaver-GuestUserID.
func (s *HTTPServer) handleRunnerAuthMirrorAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if r.Header.Get("X-Yaver-GuestUserID") != "" {
		jsonError(w, http.StatusForbidden, "mirror accept is owner-only")
		return
	}
	var payload MirrorAcceptPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := AcceptMirrorPayload(ctx, payload)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "accept: "+err.Error())
		return
	}
	if s.blackboxMgr != nil {
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "runner_auth_completed",
			Data: map[string]interface{}{
				"runner":    result.Runner,
				"tokenHash": result.TokenHash,
				"source":    result.SourceHost,
				"path":      result.WrittenTo,
			},
		})
	}
	jsonReply(w, http.StatusOK, result)
}

// handleRunnerAuthLedger returns the metadata ledger. Never includes
// plaintext. Safe to expose to any owner-authenticated surface.
func (s *HTTPServer) handleRunnerAuthLedger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	entries := LoadRunnerTokenLedger()
	out := make([]RunnerTokenLedgerEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.PublicView())
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"entries": out,
	})
}

// handleRunnerAuthLedgerRevoke removes a ledger entry by hash. Does
// NOT delete the runner's actual credential file — call the existing
// /runner/auth/import endpoint with an empty payload if you want to
// also wipe the file. This split lets a user revoke confidence in a
// token while keeping it around for rollback.
func (s *HTTPServer) handleRunnerAuthLedgerRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Runner    string `json:"runner"`
		TokenHash string `json:"tokenHash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Runner == "" || body.TokenHash == "" {
		jsonError(w, http.StatusBadRequest, "runner + tokenHash required")
		return
	}
	entries, err := RevokeRunnerTokenByHash(body.Runner, body.TokenHash)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "revoke: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"entries": entries,
	})
}

func base64StdEncode(b []byte) string {
	// re-export here so mirror_http doesn't pull encoding/base64 into
	// its imports just for one call; the mirror.go file already has
	// the heavy dep.
	return mirrorBase64Encode(b)
}

func hostnameOrUnknown() string {
	if h, err := osHostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}
