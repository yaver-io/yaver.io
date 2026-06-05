package main

// mesh_http.go — owner-authed P2P control surface for Yaver Mesh, mirroring the
// companion route style (s.auth, JSON in/out). The daemon owns the long-lived
// WireGuard device, so `yaver mesh up/down/status` from the CLI drive these
// routes rather than touching the TUN directly.

import (
	"encoding/json"
	"net/http"
	"time"
)

func (s *HTTPServer) registerMeshRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mesh/up", s.auth(s.handleMeshUp))
	mux.HandleFunc("/mesh/down", s.auth(s.handleMeshDown))
	mux.HandleFunc("/mesh/status", s.auth(s.handleMeshStatus))
}

// handleMeshUp opts the device into the mesh: ensures a keypair (private half in
// the vault), registers the public key + endpoints with the control plane,
// persists the opt-in, and brings up the data plane. Control-plane registration
// succeeding while the data plane fails (e.g. no privilege) is reported as a
// warning, not a hard error — peers can still see this node.
func (s *HTTPServer) handleMeshUp(w http.ResponseWriter, r *http.Request) {
	cfg, err := LoadConfig()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	if cfg.AuthToken == "" || cfg.ConvexSiteURL == "" || cfg.DeviceID == "" {
		jsonError(w, http.StatusBadRequest, "not signed in")
		return
	}

	kp, err := meshLoadOrCreateKeyPair()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "keys: "+err.Error())
		return
	}
	endpoints := meshLocalEndpoints()
	assigned, err := meshRegisterJoin(cfg, kp.PublicKey, endpoints)
	if err != nil {
		jsonError(w, http.StatusBadGateway, "control plane: "+err.Error())
		return
	}

	if cfg.Mesh == nil {
		cfg.Mesh = &MeshConfig{}
	}
	cfg.Mesh.Enabled = true
	cfg.Mesh.PublicKey = kp.PublicKey
	cfg.Mesh.MeshIPv4 = assigned.MeshIPv4
	cfg.Mesh.MeshIPv6 = assigned.MeshIPv6
	cfg.Mesh.LastJoinedAt = time.Now().Unix()
	if err := SaveConfig(cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	// Bring up the data plane.
	s.meshMu.Lock()
	mgr, err := s.ensureMeshManagerLocked(cfg.DeviceID)
	s.meshMu.Unlock()
	resp := map[string]interface{}{
		"meshIPv4":  assigned.MeshIPv4,
		"publicKey": kp.PublicKey,
		"endpoints": endpoints,
	}
	if err != nil {
		resp["dataPlaneWarning"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if err := mgr.Start(); err != nil {
		// Control plane is registered; surface the data-plane reason (commonly
		// "elevated privilege required") without failing the whole call.
		resp["dataPlaneWarning"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	s.startMeshDesiredLoop(cfg.DeviceID)
	resp["dataPlane"] = mgr.Status()
	writeJSON(w, http.StatusOK, resp)
}

// handleMeshDown tears the data plane down and marks the node offline in the
// control plane. The vault keypair is kept so re-joining reuses the same IP.
func (s *HTTPServer) handleMeshDown(w http.ResponseWriter, r *http.Request) {
	cfg, err := LoadConfig()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	s.meshMu.Lock()
	if s.meshMgr != nil {
		_ = s.meshMgr.Stop()
		s.meshMgr = nil // drop so a later /mesh/up rebuilds with fresh config
	}
	s.meshMu.Unlock()

	if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" && cfg.DeviceID != "" {
		_, _ = meshConvexCall(cfg, "mutation", "mesh:leaveMesh", map[string]interface{}{
			"deviceId": cfg.DeviceID,
		})
	}
	if cfg.Mesh != nil {
		cfg.Mesh.Enabled = false
		_ = SaveConfig(cfg)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleMeshStatus reports the persisted opt-in state plus the live data-plane
// snapshot (interface, self-IP, per-peer handshake counters).
func (s *HTTPServer) handleMeshStatus(w http.ResponseWriter, r *http.Request) {
	cfg, _ := LoadConfig()
	out := map[string]interface{}{}
	if cfg != nil && cfg.Mesh != nil {
		out["enabled"] = cfg.Mesh.Enabled
		out["meshIPv4"] = cfg.Mesh.MeshIPv4
		out["publicKey"] = cfg.Mesh.PublicKey
	} else {
		out["enabled"] = false
	}
	s.meshMu.Lock()
	mgr := s.meshMgr
	s.meshMu.Unlock()
	if mgr != nil {
		out["dataPlane"] = mgr.Status()
	}
	writeJSON(w, http.StatusOK, out)
}

// meshJoinResult is the assigned-address shape returned by mesh:joinMesh.
type meshJoinResult struct {
	MeshIPv4 string `json:"meshIPv4"`
	MeshIPv6 string `json:"meshIPv6"`
}

// meshRegisterJoin posts mesh:joinMesh and returns the assigned overlay IPs.
func meshRegisterJoin(cfg *Config, publicKey string, endpoints []string) (meshJoinResult, error) {
	var out meshJoinResult
	args := map[string]interface{}{
		"deviceId":    cfg.DeviceID,
		"wgPublicKey": publicKey,
		"endpoints":   endpoints,
	}
	if cfg.Mesh != nil {
		if len(cfg.Mesh.AdvertisedRoutes) > 0 {
			args["advertisedRoutes"] = cfg.Mesh.AdvertisedRoutes
		}
		if cfg.Mesh.ExitNode {
			// An exit node advertises the default route so opted-in peers route
			// their traffic through it.
			args["isExitNode"] = true
			args["advertisedRoutes"] = appendUnique(cfg.Mesh.AdvertisedRoutes, "0.0.0.0/0")
		}
	}
	raw, err := meshConvexCall(cfg, "mutation", "mesh:joinMesh", args)
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(raw, &out)
	return out, nil
}
