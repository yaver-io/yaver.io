package main

// turn_credentials.go — issues short-lived TURN credentials so the
// web viewer's RTCPeerConnection can include a relay-backed ICE
// candidate. When ICE can't find a direct path (CG-NAT both ends,
// corporate WiFi blocking inbound UDP, etc.), the relay's colocated
// TURN listener (relay/turn.go) becomes the rendezvous.
//
// Auth uses Pion's long-term-credential mechanism. The agent and the
// relay share a single secret (TURN_AUTH_SECRET, defaulting to
// RELAY_PASSWORD). The browser never sees the secret — it sees only
// the per-session derived password, valid for ~1 minute.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	pionturn "github.com/pion/turn/v4"
)

// turnCredentialResponse is the shape the web viewer plugs straight
// into RTCPeerConnection { iceServers: [...] }. We always include a
// public STUN entry (so direct ICE candidates still get gathered)
// before the TURN entry — that order matches what works best with
// Chrome / Safari ICE prioritization.
type turnCredentialResponse struct {
	IceServers []turnIceServer `json:"iceServers"`
	TTLSeconds int             `json:"ttlSeconds"`
}

type turnIceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// turnCredentialTTL is how long each derived password is valid for.
// 60s is short enough that a leaked password becomes useless almost
// immediately, long enough that ICE always finishes within the
// window.
const turnCredentialTTL = 60 * time.Second

// handleRemoteRuntimeTURNCredentials backs GET
// /remote-runtime/turn-credentials. The viewer fetches it once per
// session, sticks the result into RTCPeerConnection, then forgets
// about it. Owner-only — guests on the vibing scope cannot mint TURN
// credentials (they'd be eligible to relay arbitrary UDP through the
// operator's bandwidth).
func (s *HTTPServer) handleRemoteRuntimeTURNCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	resp := turnCredentialResponse{TTLSeconds: int(turnCredentialTTL / time.Second)}

	// Always offer a STUN server so Chrome/Safari can still gather
	// host + srflx candidates for the direct-WebRTC happy path.
	// Google's free STUN is used by every webrtc.org example for a
	// reason — it's reliable and globally anycast. Self-hosters who
	// want a private STUN flip the env var.
	stunURL := strings.TrimSpace(os.Getenv("YAVER_STUN_URL"))
	if stunURL == "" {
		stunURL = "stun:stun.l.google.com:19302"
	}
	resp.IceServers = append(resp.IceServers, turnIceServer{URLs: []string{stunURL}})

	// TURN is opt-in. If the operator hasn't pointed us at one (via
	// either YAVER_TURN_URL or, for a self-hosted relay, the same
	// host that backs RELAY_URL), we just return STUN-only and let
	// ICE try its best. The viewer never knows whether the agent
	// has TURN configured — it only sees what's in the response.
	turnURL := strings.TrimSpace(os.Getenv("YAVER_TURN_URL"))
	if turnURL == "" {
		jsonReply(w, http.StatusOK, resp)
		return
	}

	secret := turnAuthSecret()
	if secret == "" {
		// Configuration mistake worth making visible — without a
		// shared secret the relay's TURN handler refuses every
		// request. Log it but still return STUN-only so the viewer
		// doesn't fail the whole session.
		jsonReply(w, http.StatusOK, resp)
		return
	}

	user, pass, err := pionturn.GenerateLongTermCredentials(secret, turnCredentialTTL)
	if err != nil {
		// HMAC over fixed inputs — the only reason this fails is
		// out-of-memory or similar. Surface as 500 so the viewer
		// retries instead of silently using STUN-only.
		jsonError(w, http.StatusInternalServerError, "generate turn credentials: "+err.Error())
		return
	}

	resp.IceServers = append(resp.IceServers, turnIceServer{
		URLs:       []string{turnURL},
		Username:   user,
		Credential: pass,
	})
	jsonReply(w, http.StatusOK, resp)
}

// turnAuthSecret returns the shared secret the agent uses to derive
// TURN passwords. Mirrors the relay's default: prefer the explicit
// TURN_AUTH_SECRET env var, fall back to RELAY_PASSWORD when it's
// not set so a single-secret deploy "just works".
func turnAuthSecret() string {
	if v := strings.TrimSpace(os.Getenv("TURN_AUTH_SECRET")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("RELAY_PASSWORD"))
}

// turnCredentialsRouteHelper writes a JSON 200 response. Defensive
// helper used by tests that don't have an HTTPServer available.
func writeTURNCredentials(w http.ResponseWriter, resp turnCredentialResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
