package main

// HTTP surface for Wake-on-LAN.
//
// This exists because a magic packet is link-local. A watch on cellular, a
// CarPlay head unit, a headset, or the web dashboard cannot wake anything
// themselves no matter how the UI is wired — the packet has to originate on
// the sleeping box's own LAN. So the client picks a target and asks an agent
// that is already awake on that subnet to do the shouting.
//
//	GET  /wake/macs  -> this host's own MACs, so it can be registered as a
//	                    wake target without anyone hand-typing a MAC.
//	POST /wake       -> {"mac": "..."} broadcast a magic packet from here.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleWakeMACs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	macs := localMACAddrs()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"macs": macs,
	})
}

func (s *HTTPServer) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var req struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.MAC) == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "mac is required"})
		return
	}

	res := sendWakeOnLAN(req.MAC)
	// A failed wake is a legitimate answer, not a server fault: the caller
	// asked a host that isn't on the target's LAN, or gave a bad MAC. 200 with
	// ok:false lets the UI say which, instead of showing a generic error.
	jsonReply(w, http.StatusOK, res)
}
