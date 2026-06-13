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
