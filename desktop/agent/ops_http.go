package main

// ops_http.go — HTTP surfaces for the unified verb API.
//
//   POST /ops        — dispatch a single verb on this (or a routed) machine
//   POST /ops/plan   — resolve machine/project/access plan without executing
//   GET  /ops/verbs  — list every registered verb with its payload schema
//
// Both routes are owner-authed at registration time (auth() middleware in
// registerRoutes). Non-owner callers (guests / support bearers) hit the
// dispatcher through the same path but with a different caller role
// flag, which lets each verb decide whether to honour the call.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// opsCallIsRemote reports whether this /ops call originates from another machine
// — relay-bridged (X-Yaver-Via-Relay), proxied by another agent
// (X-Yaver-Proxied-By), or a non-loopback peer. A same-machine owner MCP call is
// loopback + unproxied.
func opsCallIsRemote(r *http.Request) bool {
	if isRelayBridged(r) {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-Yaver-Proxied-By")) != "" {
		return true
	}
	return !isLoopbackAddr(r.RemoteAddr)
}

// opsVerbIsLocalOnlySecret lists verbs that read/write LOCAL secrets and must
// never run for a caller on another machine (REMOTE_WORKER.md: "secrets never
// cross machines"). SECURITY (audit 2026-07-13): the client-side layer4Tools
// denylist keyed on tool names that don't exist at runtime — ops verbs proxy as
// "ops:<verb>", so ops:secrets / ops:env / ops:runner_auth slipped through and a
// same-user remote worker could exfiltrate the owner's vault/env plaintext. This
// holder-side gate cannot be bypassed by a hostile box.
func opsVerbIsLocalOnlySecret(verb string) bool {
	switch strings.TrimSpace(verb) {
	case "secrets", "env", "runner_auth":
		return true
	}
	return false
}

func (s *HTTPServer) handleOps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req OpsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if opsVerbIsLocalOnlySecret(req.Verb) && opsCallIsRemote(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OpsResult{
			OK:    false,
			Code:  "local_only",
			Error: "verb is local-only; secrets never cross machines",
		})
		return
	}
	// Derive caller role from the middleware-set headers. Auth is
	// already enforced upstream — this is only about telling the
	// dispatcher whether to honour guest-scoped verbs.
	caller := "owner"
	if r.Header.Get("X-Yaver-Support") == "true" {
		caller = "support"
	} else if r.Header.Get("X-Yaver-HostShare") == "true" {
		caller = "host-share"
	} else if r.Header.Get("X-Yaver-Guest") == "true" {
		caller = "guest"
	}

	octx := OpsContext{
		Ctx:            r.Context(),
		Server:         s,
		RequestHeaders: r.Header.Clone(),
		ActorUserID:    strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")),
		Caller:         caller,
		Scope:          strings.TrimSpace(r.Header.Get("X-Yaver-GuestScope")),
	}
	out := dispatchOps(octx, req)

	w.Header().Set("Content-Type", "application/json")
	// Even typed errors (unknown_verb, unauthorized, bad_payload) are
	// returned as HTTP 200 with `ok:false, code, error`. Agents treat
	// the structured body as authoritative; HTTP 4xx/5xx is reserved
	// for transport-level failures (malformed JSON, method wrong).
	_ = json.NewEncoder(w).Encode(out)
}

func (s *HTTPServer) handleOpsPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req OpsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	caller := "owner"
	if r.Header.Get("X-Yaver-Support") == "true" {
		caller = "support"
	} else if r.Header.Get("X-Yaver-HostShare") == "true" {
		caller = "host-share"
	} else if r.Header.Get("X-Yaver-Guest") == "true" {
		caller = "guest"
	}
	octx := OpsContext{
		Ctx:            r.Context(),
		Server:         s,
		RequestHeaders: r.Header.Clone(),
		ActorUserID:    strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")),
		Caller:         caller,
		Scope:          strings.TrimSpace(r.Header.Get("X-Yaver-GuestScope")),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildOpsExecutionPlan(octx, req))
}

func (s *HTTPServer) handleOpsVerbs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	verbs := listOpsVerbs()
	out := make([]map[string]interface{}, 0, len(verbs))
	for _, v := range verbs {
		out = append(out, map[string]interface{}{
			"name":        v.Name,
			"description": v.Description,
			"streaming":   v.Streaming,
			"allowGuest":  v.AllowGuest,
			"payload":     v.Schema,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"count": len(out),
		"verbs": out,
	})
}
