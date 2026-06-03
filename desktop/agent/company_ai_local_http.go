package main

// company_ai_local_http.go — offline resolver route for air-gapped runtimes.
//
//   POST /company-ai/resolve-local
//   body: {workKind, requestedRunner?, requestedModel?, requestedProvider?, requestedDeviceId?}
//
// Resolves against the local policy file (LoadLocalCompanyAIPolicy) with NO
// Convex call, so an egress-restricted box can still gate runner / provider /
// approvals / dataPolicy. Returns the same shape as /company-ai/resolve, with
// source="local-airgap". No secrets.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleCompanyAIResolveLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		WorkKind          string `json:"workKind"`
		RequestedRunner   string `json:"requestedRunner"`
		RequestedModel    string `json:"requestedModel"`
		RequestedProvider string `json:"requestedProvider"`
		RequestedDeviceID string `json:"requestedDeviceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.WorkKind) == "" {
		jsonError(w, http.StatusBadRequest, "workKind is required")
		return
	}
	pol, err := LoadLocalCompanyAIPolicy()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "read local policy: "+err.Error())
		return
	}
	if pol == nil {
		jsonError(w, http.StatusNotFound, "no local company AI policy configured on this runtime")
		return
	}
	jsonReply(w, http.StatusOK, ResolveCompanyAILocal(pol, body.WorkKind, body.RequestedRunner, body.RequestedModel, body.RequestedProvider, body.RequestedDeviceID))
}
