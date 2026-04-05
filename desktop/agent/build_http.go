package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// resolveClientPlatform adjusts build platform based on client device OS.
// If platform is a build system name without target (e.g., "flutter", "rn", "expo"),
// it resolves to the correct target for the client's platform.
func resolveClientPlatform(platform, clientOS string) string {
	isIOS := clientOS == "ios"

	// Map build system names to platform-specific targets
	switch platform {
	case "flutter":
		if isIOS {
			return "flutter-ipa"
		}
		return "flutter-apk"
	case "gradle":
		return "gradle-apk" // Gradle is Android-only
	case "xcode":
		return "xcode-ipa" // Xcode is iOS-only
	case "rn":
		if isIOS {
			return "rn-ios"
		}
		return "rn-android"
	case "expo":
		if isIOS {
			return "expo-ios"
		}
		return "expo-android"
	}
	return platform // already specific
}

// handleBuilds handles POST /builds (start) and GET /builds (list).
func (s *HTTPServer) handleBuilds(w http.ResponseWriter, r *http.Request) {
	if s.buildMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "builds not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		builds := s.buildMgr.ListBuilds()
		jsonReply(w, http.StatusOK, builds)

	case http.MethodPost:
		var req struct {
			Platform        string   `json:"platform"`
			WorkDir         string   `json:"workDir"`
			Args            []string `json:"args"`
			ArtifactPath    string   `json:"artifactPath"`    // for register
			InstallOnDevice bool     `json:"installOnDevice"` // direct device install (LAN only)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		// Platform-aware: if client sends X-Client-Platform and platform ends with "-auto",
		// resolve to the right platform for the client's OS
		clientPlatform := r.Header.Get("X-Client-Platform")
		if clientPlatform != "" && req.Platform != "" {
			req.Platform = resolveClientPlatform(req.Platform, clientPlatform)
		}

		// Auto-upgrade to device install: if iOS + direct LAN + installOnDevice requested
		if req.InstallOnDevice {
			if !isDirectConnection(r) {
				jsonReply(w, http.StatusBadRequest, map[string]string{"error": "direct device install requires LAN connection — use TestFlight for relay connections"})
				return
			}
			// Override platform to xcode-device-install if not already
			if req.Platform != string(PlatformXcodeDeviceInstall) {
				req.Platform = string(PlatformXcodeDeviceInstall)
			}
		}

		build, err := s.buildMgr.StartBuild(BuildPlatform(req.Platform), req.WorkDir, req.Args, req.InstallOnDevice)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, build)

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleBuildRegister handles POST /builds/register (register pre-built artifact).
func (s *HTTPServer) handleBuildRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.buildMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "builds not available"})
		return
	}

	var req struct {
		ArtifactPath string `json:"artifactPath"`
		Platform     string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.ArtifactPath == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing artifactPath"})
		return
	}

	platform := BuildPlatform(req.Platform)
	if platform == "" {
		platform = guessPlatformFromFile(req.ArtifactPath)
	}

	build, err := s.buildMgr.RegisterArtifact(req.ArtifactPath, platform)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, build)
}

// handleBuildByID handles GET /builds/{id}, DELETE /builds/{id}.
func (s *HTTPServer) handleBuildByID(w http.ResponseWriter, r *http.Request) {
	if s.buildMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "builds not available"})
		return
	}

	// Extract build ID from path: /builds/{id}[/artifact|/stream]
	path := strings.TrimPrefix(r.URL.Path, "/builds/")
	parts := strings.SplitN(path, "/", 2)
	buildID := parts[0]

	if buildID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing build ID"})
		return
	}

	// Sub-routes
	if len(parts) > 1 {
		switch parts[1] {
		case "artifact":
			s.handleBuildArtifact(w, r, buildID)
			return
		case "stream":
			s.handleBuildStream(w, r, buildID)
			return
		}
	}

	build, ok := s.buildMgr.GetBuild(buildID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "build not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, build)
	case http.MethodDelete:
		if err := s.buildMgr.CancelBuild(buildID); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleBuildArtifact serves the build artifact binary with Range support.
func (s *HTTPServer) handleBuildArtifact(w http.ResponseWriter, r *http.Request, buildID string) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	build, ok := s.buildMgr.GetBuild(buildID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "build not found"})
		return
	}

	if build.ArtifactPath == "" {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "no artifact available"})
		return
	}

	// Set SHA256 header for client-side verification
	if build.ArtifactHash != "" {
		w.Header().Set("X-Content-SHA256", build.ArtifactHash)
	}

	// http.ServeFile handles Range requests, Content-Length, Content-Type automatically
	http.ServeFile(w, r, build.ArtifactPath)
}

// handleBuildStream proxies to the exec session's SSE stream.
func (s *HTTPServer) handleBuildStream(w http.ResponseWriter, r *http.Request, buildID string) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	build, ok := s.buildMgr.GetBuild(buildID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "build not found"})
		return
	}

	if build.ExecID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "build has no exec session"})
		return
	}

	// Redirect internally to the exec stream endpoint
	r.URL.Path = "/exec/" + build.ExecID + "/stream"
	s.handleExecByID(w, r)
}
