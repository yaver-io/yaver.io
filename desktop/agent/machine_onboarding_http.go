package main

import (
	"encoding/json"
	"net/http"
)

func (s *HTTPServer) handleMachineOnboardingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	status := collectMachineOnboardingStatus()
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":        true,
		"providers": status.Providers,
	})
}

func (s *HTTPServer) handleMachineOnboardingApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req machineOnboardingApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if result, err := applyMachineOnboardingLocal(req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
	} else {
		jsonReply(w, http.StatusOK, result)
	}
}

func (s *HTTPServer) handleMachineOnboardingRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req machineOnboardingRemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if result, err := applyMachineOnboardingRemoveLocal(req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
	} else {
		jsonReply(w, http.StatusOK, result)
	}
}
