package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// handleSandboxStatus returns container sandbox status (GET /sandbox/status).
func (s *HTTPServer) handleSandboxStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	result := map[string]interface{}{
		"ok":                 true,
		"containerizeGuests": s.containerizeGuests,
		"containerizeHost":   s.containerizeHost,
	}

	if s.taskMgr != nil {
		result["networkMode"] = s.taskMgr.ContainerNetwork
		result["readOnly"] = s.taskMgr.ContainerReadOnly
		if s.taskMgr.ContainerCPU != "" {
			result["cpuLimit"] = s.taskMgr.ContainerCPU
		}
		if s.taskMgr.ContainerMemory != "" {
			result["memoryLimit"] = s.taskMgr.ContainerMemory
		}
	}

	if s.containerRunner != nil {
		status := s.containerRunner.Status()
		result["docker"] = status.Available
		result["imageReady"] = status.ImageReady
		result["imageName"] = status.ImageName
		result["gpuAvailable"] = s.containerRunner.IsGPUAvailable()
	} else {
		result["docker"] = false
		result["imageReady"] = false
	}

	jsonReply(w, http.StatusOK, result)
}

// handleSandboxConfig updates container sandbox settings (POST /sandbox/config).
func (s *HTTPServer) handleSandboxConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		ContainerizeGuests *bool  `json:"containerizeGuests,omitempty"`
		ContainerizeHost   *bool  `json:"containerizeHost,omitempty"`
		CPULimit           string `json:"cpuLimit,omitempty"`
		MemoryLimit        string `json:"memoryLimit,omitempty"`
		NetworkMode        string `json:"networkMode,omitempty"`
		ReadOnly           *bool  `json:"readOnly,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate network mode if provided
	if body.NetworkMode != "" {
		switch body.NetworkMode {
		case "host", "bridge", "none":
			// valid
		default:
			jsonError(w, http.StatusBadRequest, "networkMode must be 'host', 'bridge', or 'none'")
			return
		}
	}

	// Initialize container runner if not already done
	if s.containerRunner == nil {
		cr := NewContainerRunner()
		if cr.IsAvailable() {
			s.containerRunner = cr
		} else {
			jsonError(w, http.StatusBadRequest, "Docker not available")
			return
		}
	}

	if body.ContainerizeGuests != nil {
		s.containerizeGuests = *body.ContainerizeGuests
		if s.taskMgr != nil {
			s.taskMgr.ContainerizeGuests = *body.ContainerizeGuests
			s.taskMgr.ContainerRunner = s.containerRunner
		}
	}
	if body.ContainerizeHost != nil {
		s.containerizeHost = *body.ContainerizeHost
		if s.taskMgr != nil {
			s.taskMgr.ContainerizeHost = *body.ContainerizeHost
			s.taskMgr.ContainerRunner = s.containerRunner
		}
	}
	if body.CPULimit != "" && s.taskMgr != nil {
		s.taskMgr.ContainerCPU = body.CPULimit
	}
	if body.MemoryLimit != "" && s.taskMgr != nil {
		s.taskMgr.ContainerMemory = body.MemoryLimit
	}
	if body.NetworkMode != "" && s.taskMgr != nil {
		s.taskMgr.ContainerNetwork = body.NetworkMode
	}
	if body.ReadOnly != nil && s.taskMgr != nil {
		s.taskMgr.ContainerReadOnly = *body.ReadOnly
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"containerizeGuests": s.containerizeGuests,
		"containerizeHost":   s.containerizeHost,
		"networkMode":        s.taskMgr.ContainerNetwork,
		"readOnly":           s.taskMgr.ContainerReadOnly,
	})
}

// handleSandboxBuild triggers building the sandbox Docker image (POST /sandbox/build).
func (s *HTTPServer) handleSandboxBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	if s.containerRunner == nil {
		cr := NewContainerRunner()
		if !cr.IsAvailable() {
			jsonError(w, http.StatusBadRequest, "Docker not available")
			return
		}
		s.containerRunner = cr
	}

	// Build in background (takes minutes)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := s.containerRunner.BuildImage(ctx); err != nil {
			// Log error — client can poll /sandbox/status to check
			_ = err
		}
	}()

	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"message": "Sandbox image build started. Poll GET /sandbox/status to check progress.",
	})
}
