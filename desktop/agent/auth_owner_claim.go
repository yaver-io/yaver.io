// One-click pair flow for boxes already in bootstrap mode and
// reachable through the relay. The dashboard / mobile app calls
// POST /auth/pair/owner-claim with the user's Yaver bearer; we
// verify ownership against Convex (NOT against any local agent
// state — there is none worth trusting in bootstrap mode) and
// then submit the bearer into the active pairing session
// internally. No passkey copy-paste, no URL composition, no
// expiry races.
//
// Trust model:
//   1. Caller reached us through the relay or LAN (path-level
//      gate from the relay's per-user `__rp=` validation).
//   2. Convex GET /devices/list with the bearer lists THIS
//      device with accessScope=="owner" — same check the
//      factory-reset endpoint uses.
//   3. We splice the bearer into the active pair session as if
//      a regular /auth/pair/submit had landed. Once accepted,
//      the bootstrap loop saves it + re-execs `yaver serve`.
//
// Guests cannot owner-claim a host's box (accessScope must be
// "owner"). A box without an active pair session 409s; the
// caller should have already started one.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func (bs *bootstrapHTTPServer) handleOwnerClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer == "" {
		bearer = r.URL.Query().Get("token")
	}
	if bearer == "" {
		var body struct{ Token string `json:"token"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		bearer = body.Token
	}
	if bearer == "" {
		jsonReply(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer"})
		return
	}

	cfg, cfgErr := LoadConfig()
	if cfgErr != nil || cfg == nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("agent config unreadable: %v", cfgErr),
		})
		return
	}
	if cfg.ConvexSiteURL == "" {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent has no Convex URL — cannot verify ownership",
		})
		return
	}
	if cfg.DeviceID == "" {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent has no device_id (truly fresh box). Pair via the URL flow first.",
		})
		return
	}

	// Verify the bearer is the owner of THIS device per Convex.
	// Same lookup as the factory-reset endpoint — the trust anchor
	// is Convex /devices/list, not anything on the box.
	devices, err := listDevices(cfg.ConvexSiteURL, bearer)
	if err != nil {
		jsonReply(w, http.StatusUnauthorized, map[string]string{
			"error": fmt.Sprintf("Convex rejected the bearer: %v", err),
		})
		return
	}
	var match *DeviceInfo
	for i := range devices {
		if devices[i].DeviceID == cfg.DeviceID {
			match = &devices[i]
			break
		}
	}
	if match == nil {
		jsonReply(w, http.StatusForbidden, map[string]string{
			"error": "Convex does not list this device under your account",
		})
		return
	}
	if match.IsGuest || (match.AccessScope != "" && match.AccessScope != "owner") {
		jsonReply(w, http.StatusForbidden, map[string]string{
			"error": "guests cannot owner-claim a box — only the host (" + match.HostName + ") can re-pair it",
		})
		return
	}

	// Splice into the active pair session. activePairingSnapshot()
	// returns nil if no session is open — caller should have started
	// one (the agent does this automatically every 10min).
	sess := activePairingSnapshot()
	if sess == nil {
		jsonReply(w, http.StatusConflict, map[string]string{
			"error": "no active pair session — the agent is rotating one; retry in a few seconds",
		})
		return
	}

	// Build a synthetic /auth/pair/submit request and dispatch it
	// through the same handler so the on-success persist + re-exec
	// path is identical to a manual claim. This keeps the surface
	// area small and avoids divergence between "URL pair" and
	// "owner-claim pair".
	body, _ := json.Marshal(map[string]interface{}{
		"token":         bearer,
		"convexSiteUrl": cfg.ConvexSiteURL,
		"userId":        match.DeviceID, // any non-empty placeholder; submit handler doesn't enforce
	})
	innerReq, _ := http.NewRequest("POST", "/auth/pair/submit?code="+sess.Code, strings.NewReader(string(body)))
	innerReq.Header.Set("Content-Type", "application/json")
	rec := newCapturingResponseWriter()
	bs.handlePairSubmit(rec, innerReq)
	// Forward the inner response 1:1 (status + body) so the caller
	// sees the same shape they'd get from the manual flow.
	for k, vv := range rec.Header() {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Status())
	_, _ = w.Write(rec.Body())

	if rec.Status() < 300 {
		hostname := match.HostName
		if hostname == "" {
			hostname = match.HostEmail
		}
		if hostname == "" {
			hostname = "(unknown)"
		}
		log.Printf("[auth] /auth/pair/owner-claim: paired %s as owner %s",
			cfg.DeviceID[:8], hostname)
	}
}
