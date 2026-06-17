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

// isNativeAlias returns true for the friendly native names (iosNative, androidNative,
// flutter) — these need resolveNativePlatform with a target before reaching the build
// pipeline.
func isNativeAlias(p string) bool {
	switch p {
	case NativeIOS, "ios-native",
		NativeAndroid, "android-native",
		NativeFlutter:
		return true
	}
	return false
}

// isIOSBoundPlatform returns true for any platform whose artifact targets iOS — so the
// existing iOS-only auto-upgrade-to-Hermes-or-Xcode-install logic only fires for iOS.
// Android (gradle-*) and Flutter device-install (flutter-device-install / flutter-apk)
// must NOT be rerouted into the iOS install path.
func isIOSBoundPlatform(p string) bool {
	switch BuildPlatform(p) {
	case PlatformXcodeIPA, PlatformXcodeBuild, PlatformXcodeDeviceInstall,
		PlatformRNIOS, PlatformExpoIOS, PlatformHermesBundlePush, PlatformFlutterIPA:
		return true
	}
	switch p {
	case "ios", "iosNative", "ios-native":
		return true
	}
	return false
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
			Target          string   `json:"target"` // device | simulator | testflight | playstore | local
			WorkDir         string   `json:"workDir"`
			Args            []string `json:"args"`
			ArtifactPath    string   `json:"artifactPath"`    // for register
			InstallOnDevice bool     `json:"installOnDevice"` // direct device install (LAN only)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		// Friendly native aliases (iosNative / androidNative / flutter) resolve via
		// resolveNativePlatform so mobile/web/MCP can POST one shape regardless of host.
		if isNativeAlias(req.Platform) {
			resolved, err := resolveNativePlatform(req.Platform, req.Target)
			if err != nil {
				jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			req.Platform = string(resolved)
			// device/simulator targets imply install-on-device unless caller already said so
			if req.Target == "device" || req.Target == "simulator" || req.Target == "emulator" || req.Target == "emu" || req.Target == "sim" {
				req.InstallOnDevice = true
			}
		}

		// Platform-aware: if client sends X-Client-Platform and platform ends with "-auto",
		// resolve to the right platform for the client's OS
		clientPlatform := r.Header.Get("X-Client-Platform")
		if clientPlatform != "" && req.Platform != "" {
			req.Platform = resolveClientPlatform(req.Platform, clientPlatform)
		}

		// Auto-upgrade to device install — but ONLY for iOS-bound platforms. Android
		// (gradle-*) and Flutter (flutter-*) already pick the right device-install
		// constant via resolveNativePlatform, so don't clobber them here.
		if req.InstallOnDevice && isIOSBoundPlatform(req.Platform) {
			if !isDirectConnection(r) {
				jsonReply(w, http.StatusBadRequest, map[string]string{"error": "direct device install requires LAN connection — use TestFlight for relay connections"})
				return
			}

			// Decide install method based on platform capabilities and user preference
			method := resolveIOSInstallMethod(s.iosInstallMethod)
			if method == IOSInstallNative {
				// macOS + Xcode: xcodebuild + xcrun devicectl
				if req.Platform != string(PlatformXcodeDeviceInstall) {
					req.Platform = string(PlatformXcodeDeviceInstall)
				}
			} else {
				// Non-macOS or no Xcode: Hermes bytecode bundle push to super-host
				req.Platform = string(PlatformHermesBundlePush)
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
		case "log":
			s.handleBuildLog(w, r, buildID)
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

// handleBuildLog returns the build's captured exec output (stdout/stderr) so
// `yaver build status` can show a log tail. It reads the build's own exec
// session directly rather than proxying to the auth-gated /exec/{id} endpoint,
// keeping the build surface self-contained and auth-free for local callers.
func (s *HTTPServer) handleBuildLog(w http.ResponseWriter, r *http.Request, buildID string) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	build, ok := s.buildMgr.GetBuild(buildID)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "build not found"})
		return
	}

	out := map[string]interface{}{"id": build.ID, "status": string(build.Status)}
	if s.execMgr != nil && build.ExecID != "" {
		if sess, ok := s.execMgr.GetExec(build.ExecID); ok {
			out["exec"] = sess.Snapshot()
		}
	}
	jsonReply(w, http.StatusOK, out)
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
