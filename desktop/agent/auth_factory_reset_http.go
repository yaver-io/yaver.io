// HTTP endpoint that lets a verified Convex device-owner factory-
// reset the agent's local auth state without first having a valid
// owner-bearer for THIS agent. Why: the bug we're fixing is exactly
// "agent's local auth_token belongs to a different user" — the
// dashboard's owner bearer would be 403'd by the regular auth()
// middleware, so we can't gate this endpoint on it. Instead we
// re-verify the caller's identity against Convex directly:
//
//   1. Convex GET /auth/me with caller's bearer → caller's userId
//   2. Convex GET /devices/list with caller's bearer → list of
//      devices Convex says this user owns / has access to
//   3. accept the reset only if THIS agent's deviceId is in that
//      list AND the caller's accessScope == "owner". Guests cannot
//      factory-reset; only the owner can.
//
// This works regardless of what the agent's local auth_token says —
// Convex is the trust anchor for who-owns-what. Once verified, we
// spawn the same `yaver auth factory-reset --headless --skip-npm`
// the CLI uses, which clears /home/yaver/.yaver/{auth_token,
// device_id, runner-tokens}, then exits and lets systemd restart
// the agent in bootstrap mode for a fresh pair.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleAuthFactoryReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	// Bearer either via header or ?token= query param. Some web/mobile
	// flows can't set custom headers (iframe, EventSource fallback).
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer == "" {
		bearer = r.URL.Query().Get("token")
	}
	if bearer == "" {
		jsonReply(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer"})
		return
	}

	cfg, cfgErr := LoadConfig()
	if cfgErr != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("could not read agent config: %v", cfgErr),
		})
		return
	}
	if cfg.ConvexSiteURL == "" {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent has no Convex URL configured — cannot verify ownership",
		})
		return
	}
	if cfg.DeviceID == "" {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent has no device_id — already in bootstrap mode? just re-pair",
		})
		return
	}

	// Step 1: who is the caller, per Convex? We don't trust the
	// agent's local view — it's the thing being reset.
	devices, err := listDevices(cfg.ConvexSiteURL, bearer)
	if err != nil {
		// Most common cause: bearer is invalid/expired against Convex.
		jsonReply(w, http.StatusUnauthorized, map[string]string{
			"error": fmt.Sprintf("Convex rejected the bearer: %v", err),
		})
		return
	}

	// Step 2: is THIS device in the caller's owner-scope list?
	// "owner" — caller paired the box (or Convex transferred it).
	// "shared-scoped" / "shared-legacy" — caller is a guest. Guests
	// can't factory-reset; that's a host privilege.
	var match *DeviceInfo
	for i := range devices {
		if devices[i].DeviceID == cfg.DeviceID {
			match = &devices[i]
			break
		}
	}
	if match == nil {
		jsonReply(w, http.StatusForbidden, map[string]string{
			"error": "Convex does not list this device under your account — you don't own it and aren't a guest",
		})
		return
	}
	if match.IsGuest || (match.AccessScope != "" && match.AccessScope != "owner") {
		jsonReply(w, http.StatusForbidden, map[string]string{
			"error": "guests cannot factory-reset auth — only the device owner can. Ask the host (" + match.HostName + ") to reset.",
		})
		return
	}

	// Step 3: ownership confirmed. Spawn the factory-reset in
	// headless mode. The agent will exit; systemd / launchd
	// restarts it in bootstrap mode within seconds.
	log.Printf("[auth] /auth/factory-reset accepted from owner (deviceId=%s) — spawning factory reset", cfg.DeviceID)
	go func() {
		// Tiny delay so the HTTP response actually makes it back to
		// the caller before we exit. spawnAuthFactoryReset detaches
		// + the agent process exits as part of the reset; without
		// the delay the client sees a connection-reset instead of
		// the JSON ack.
		_ = json.NewEncoder // ref so import isn't dropped if we ever simplify
		if err := spawnAuthFactoryReset(true); err != nil {
			log.Printf("[auth] spawnAuthFactoryReset failed: %v", err)
		}
	}()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":            true,
		"message":       "factory reset triggered. The agent will exit and restart in bootstrap mode within a few seconds. Re-pair from the dashboard.",
		"deviceId":      cfg.DeviceID,
		"verifiedOwner": match.Name,
	})
}
