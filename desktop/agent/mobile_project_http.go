package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type mobileProjectMCPRequest struct {
	Directory string `json:"directory"`
	Framework string `json:"framework,omitempty"`
	Platform  string `json:"platform,omitempty"`
}

func (s *HTTPServer) mobileProjectStatusPayload(directory string) (map[string]any, error) {
	if strings.TrimSpace(directory) == "" {
		if s.taskMgr == nil {
			return nil, fmt.Errorf("directory is required")
		}
		directory = s.taskMgr.workDir
	}
	raw, _ := json.Marshal(mobileProjectStatus(directory))
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *HTTPServer) mobileProjectPreparePayload(directory string) (map[string]any, error) {
	if strings.TrimSpace(directory) == "" {
		if s.taskMgr == nil {
			return nil, fmt.Errorf("directory is required")
		}
		directory = s.taskMgr.workDir
	}
	manifest, err := readProjectPackageManifest(directory)
	if err != nil {
		return nil, fmt.Errorf("package.json missing or invalid: %w", err)
	}
	prep := detectProjectPreparation(directory, manifest)
	if len(prep.MissingTools) == 0 && prep.NeedsDependencyInstall && prep.CanAutoInstallDependencies {
		if err := installProjectDependencies(directory, prep); err != nil {
			return nil, fmt.Errorf("dependency install failed: %w", err)
		}
	}
	return s.mobileProjectStatusPayload(directory)
}

func (s *HTTPServer) mobileProjectBuildPayload(directory, framework, platform string) (map[string]any, error) {
	if strings.TrimSpace(directory) == "" {
		if s.taskMgr == nil {
			return nil, fmt.Errorf("directory is required")
		}
		directory = s.taskMgr.workDir
	}
	if strings.TrimSpace(platform) == "" {
		platform = "ios"
	}
	return s.buildNativeBundleForProject(directory, framework, platform)
}

func (s *HTTPServer) handleMobileProjectStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req mobileProjectMCPRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := s.mobileProjectStatusPayload(req.Directory)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}

func (s *HTTPServer) handleMobileProjectPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req mobileProjectMCPRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := s.mobileProjectPreparePayload(req.Directory)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}

func (s *HTTPServer) handleMobileProjectBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req mobileProjectMCPRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := s.mobileProjectBuildPayload(req.Directory, req.Framework, req.Platform)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}

// handleMobileHermesDoctor is the receiving half of remote Hermes diagnosis.
//
// REMOTE_WORKER.md §A ships the dev loop first, and this was the one Layer 1
// verb with nowhere to land: mobile_hermes_doctor could not be proxied because
// no route existed to proxy TO. Adding the device_id flag alone would have
// advertised a capability that then 404s — worse than not offering it.
//
// The diagnosis is pure inspection of a checkout: which native modules the
// project needs, and whether the Yaver container can host them. That is
// intrinsically a question about the machine holding the code, which is exactly
// why it has to be answerable remotely — the laptop asking is usually not the
// box with the project on it.
func (s *HTTPServer) handleMobileHermesDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req mobileHermesDoctorInput
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Directory) == "" && s.taskMgr != nil {
		// Same default as the local MCP path: an omitted directory means "the
		// project this agent is working in", which on a remote worker is the
		// whole point of asking it rather than asking here.
		req.Directory = s.taskMgr.workDir
	}
	jsonReply(w, http.StatusOK, mobileHermesDoctor(req))
}
