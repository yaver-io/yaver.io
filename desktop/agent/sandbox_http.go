package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ContainerSandboxSummary struct {
	OK                  bool     `json:"ok"`
	EnabledMode         string   `json:"enabledMode,omitempty"`
	Docker              bool     `json:"docker"`
	DockerPath          string   `json:"dockerPath,omitempty"`
	ImageReady          bool     `json:"imageReady"`
	ImageName           string   `json:"imageName,omitempty"`
	GPUAvailable        bool     `json:"gpuAvailable,omitempty"`
	ContainerizeGuests  bool     `json:"containerizeGuests"`
	ContainerizeHost    bool     `json:"containerizeHost"`
	NetworkMode         string   `json:"networkMode,omitempty"`
	ReadOnly            bool     `json:"readOnly,omitempty"`
	CPULimit            string   `json:"cpuLimit,omitempty"`
	MemoryLimit         string   `json:"memoryLimit,omitempty"`
	ExtraMounts         []string `json:"extraMounts,omitempty"`
	RecommendedMode     string   `json:"recommendedMode,omitempty"`
	RecommendedReason   string   `json:"recommendedReason,omitempty"`
	QuickstartAvailable bool     `json:"quickstartAvailable"`
}

func (s *HTTPServer) sandboxSummary() ContainerSandboxSummary {
	result := ContainerSandboxSummary{
		OK:                  true,
		ContainerizeGuests:  s.containerizeGuests,
		ContainerizeHost:    s.containerizeHost,
		QuickstartAvailable: true,
	}

	switch {
	case s.containerizeHost:
		result.EnabledMode = "host"
	case s.containerizeGuests:
		result.EnabledMode = "guests"
	default:
		result.EnabledMode = "off"
	}

	if s.taskMgr != nil {
		result.NetworkMode = s.taskMgr.ContainerNetwork
		result.ReadOnly = s.taskMgr.ContainerReadOnly
		result.CPULimit = s.taskMgr.ContainerCPU
		result.MemoryLimit = s.taskMgr.ContainerMemory
		result.ExtraMounts = append([]string{}, s.taskMgr.ContainerMounts...)
	}
	if result.NetworkMode == "" {
		result.NetworkMode = "host"
	}

	if s.containerRunner != nil {
		status := s.containerRunner.Status()
		result.Docker = status.Available
		result.DockerPath = status.DockerPath
		result.ImageReady = status.ImageReady
		result.ImageName = status.ImageName
		result.GPUAvailable = s.containerRunner.IsGPUAvailable()
	} else {
		result.Docker = false
		result.ImageReady = false
	}

	result.RecommendedMode = "guests"
	result.RecommendedReason = "shared infra should isolate guest-triggered tasks without changing your own hot-reload flow"
	if !result.ContainerizeGuests && !result.ContainerizeHost {
		result.RecommendedMode = "guests"
	}
	return result
}

func (s *HTTPServer) persistSandboxConfig() {
	cfg, err := LoadConfig()
	if err != nil {
		return
	}
	cfg.ContainerizeGuests = s.containerizeGuests
	cfg.ContainerizeHost = s.containerizeHost
	if s.taskMgr != nil {
		cfg.ContainerCPU = s.taskMgr.ContainerCPU
		cfg.ContainerMemory = s.taskMgr.ContainerMemory
		cfg.ContainerNetwork = s.taskMgr.ContainerNetwork
		cfg.ContainerReadOnly = s.taskMgr.ContainerReadOnly
		cfg.ContainerMounts = append([]string{}, s.taskMgr.ContainerMounts...)
	}
	_ = SaveConfig(cfg)
}

func (s *HTTPServer) ensureContainerRunner() error {
	if s.containerRunner != nil {
		return nil
	}
	cr := NewContainerRunner()
	if !cr.IsAvailable() {
		return fmt.Errorf("Docker not available")
	}
	s.containerRunner = cr
	return nil
}

func (s *HTTPServer) startSandboxBuild() error {
	if err := s.ensureContainerRunner(); err != nil {
		return err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := s.containerRunner.BuildImage(ctx); err != nil {
			_ = err
		}
	}()
	return nil
}

func (s *HTTPServer) applySandboxQuickstart(mode string, buildImage bool) (ContainerSandboxSummary, string, error) {
	if err := s.ensureContainerRunner(); err != nil {
		return ContainerSandboxSummary{}, "", err
	}
	switch mode {
	case "", "guests":
		s.containerizeGuests = true
		s.containerizeHost = false
	case "host":
		s.containerizeGuests = false
		s.containerizeHost = true
	default:
		return ContainerSandboxSummary{}, "", fmt.Errorf("mode must be 'guests' or 'host'")
	}
	if s.taskMgr != nil {
		s.taskMgr.ContainerRunner = s.containerRunner
		s.taskMgr.ContainerizeGuests = s.containerizeGuests
		s.taskMgr.ContainerizeHost = s.containerizeHost
		if s.taskMgr.ContainerNetwork == "" {
			s.taskMgr.ContainerNetwork = "host"
		}
		if !s.taskMgr.ContainerReadOnly {
			s.taskMgr.ContainerReadOnly = true
		}
	}
	s.persistSandboxConfig()
	message := "Containerization enabled."
	if buildImage && !s.containerRunner.IsImageReady() {
		_ = s.startSandboxBuild()
		message = "Containerization enabled and sandbox image build started."
	}
	return s.sandboxSummary(), message, nil
}

// handleSandboxStatus returns container sandbox status (GET /sandbox/status).
func (s *HTTPServer) handleSandboxStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	result := s.sandboxSummary()
	jsonReply(w, http.StatusOK, result)
}

// handleSandboxConfig updates container sandbox settings (POST /sandbox/config).
func (s *HTTPServer) handleSandboxConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		ContainerizeGuests *bool    `json:"containerizeGuests,omitempty"`
		ContainerizeHost   *bool    `json:"containerizeHost,omitempty"`
		CPULimit           string   `json:"cpuLimit,omitempty"`
		MemoryLimit        string   `json:"memoryLimit,omitempty"`
		NetworkMode        string   `json:"networkMode,omitempty"`
		ReadOnly           *bool    `json:"readOnly,omitempty"`
		ExtraMounts        []string `json:"extraMounts,omitempty"`
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

	if err := s.ensureContainerRunner(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.taskMgr != nil {
		s.taskMgr.ContainerRunner = s.containerRunner
		if s.taskMgr.ContainerNetwork == "" {
			s.taskMgr.ContainerNetwork = "host"
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
	if body.ExtraMounts != nil && s.taskMgr != nil {
		s.taskMgr.ContainerMounts = append([]string{}, body.ExtraMounts...)
	}

	s.persistSandboxConfig()

	jsonReply(w, http.StatusOK, s.sandboxSummary())
}

// handleSandboxBuild triggers building the sandbox Docker image (POST /sandbox/build).
func (s *HTTPServer) handleSandboxBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	if err := s.startSandboxBuild(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"message": "Sandbox image build started. Poll GET /sandbox/status to check progress.",
	})
}

func (s *HTTPServer) handleSandboxQuickstart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Mode       string `json:"mode,omitempty"`
		BuildImage *bool  `json:"buildImage,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	buildImage := true
	if body.BuildImage != nil {
		buildImage = *body.BuildImage
	}
	summary, message, err := s.applySandboxQuickstart(body.Mode, buildImage)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"message": message,
		"sandbox": summary,
	})
}
