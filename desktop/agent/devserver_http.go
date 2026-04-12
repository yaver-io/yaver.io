package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// handleDevServerStatus returns the current dev server status.
// GET /dev/status
func (s *HTTPServer) handleDevServerStatus(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	status := s.devServerMgr.Status()
	if status == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"running": false,
		})
		return
	}

	jsonReply(w, http.StatusOK, status)
}

// handleDevServerStart starts a dev server.
// POST /dev/start { "framework": "expo", "workDir": "/path", "platform": "ios", "port": 8081 }
func (s *HTTPServer) handleDevServerStart(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Framework string `json:"framework"` // "expo", "flutter", "vite", "nextjs", "" (auto-detect)
		WorkDir   string `json:"workDir"`
		Platform  string `json:"platform"` // "ios", "android", "web"
		Port      int    `json:"port"`
		Rebuild   bool   `json:"rebuild"`  // force rebuild (clear build marker)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.WorkDir == "" {
		if s.taskMgr != nil {
			req.WorkDir = s.taskMgr.workDir
		}
	}

	// Clear build marker if rebuild requested
	if req.Rebuild && req.WorkDir != "" {
		projectHash := strings.ReplaceAll(filepath.Base(req.WorkDir), " ", "_")
		marker := filepath.Join(yaverBuildsDir(), projectHash+".built")
		os.Remove(marker)
		log.Printf("[dev] Cleared build marker for %s (rebuild requested)", projectHash)
	}

	if err := s.devServerMgr.Start(req.Framework, req.WorkDir, req.Platform, req.Port); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Return immediately — server starts in background, mobile polls /dev/status
	status := s.devServerMgr.Status()
	if status == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"running":   false,
			"framework": req.Framework,
			"workDir":   req.WorkDir,
			"starting":  true,
		})
		return
	}

	jsonReply(w, http.StatusOK, status)
}

// handleDevServerStop stops the active dev server.
// POST /dev/stop
func (s *HTTPServer) handleDevServerStop(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if err := s.devServerMgr.Stop(); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleDevServerReload triggers a hot reload on the active dev server.
// POST /dev/reload
func (s *HTTPServer) handleDevServerReload(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if err := s.devServerMgr.Reload(); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Emit control signal for hot reload
	if s.taskMgr != nil {
		s.taskMgr.BroadcastControlSignal(`{"yaver_control":"hot_reload"}`)
	}

	// Push reload command to all connected SDK devices (third-party apps with Feedback SDK)
	if s.blackboxMgr != nil {
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "reload",
		})
		log.Printf("[dev] Broadcast reload command to connected SDK devices")
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleReloadApp triggers a reload of the third-party app running inside the Yaver container.
// For dev server mode: pushes a "reload" command to SDK devices.
// For native bundle mode: rebuilds the bundle and pushes "reload_bundle" with the bundle URL.
// POST /dev/reload-app { "mode": "dev" | "bundle" }
func (s *HTTPServer) handleReloadApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Mode string `json:"mode"` // "dev" (hot reload) or "bundle" (rebuild + push)
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Mode == "" {
		req.Mode = "dev"
	}

	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no SDK devices connected"})
		return
	}

	switch req.Mode {
	case "dev":
		// Hot reload: tell SDK devices to reload from dev server
		if s.devServerMgr != nil {
			s.devServerMgr.Reload()
		}
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "reload",
		})
		log.Printf("[dev] Reload-app (dev mode): broadcast reload to SDK devices")
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "mode": "dev"})

	case "bundle":
		// Native bundle: rebuild and tell SDK devices to fetch new bundle
		// First trigger the build (reuse build-native logic)
		s.handleBuildNativeBundle(w, r)
		// After build completes (handleBuildNativeBundle writes response),
		// push reload_bundle command
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "reload_bundle",
			Data: map[string]interface{}{
				"bundleUrl": "/dev/native-bundle",
				"assetsUrl": "/dev/native-assets",
			},
		})
		log.Printf("[dev] Reload-app (bundle mode): broadcast reload_bundle to SDK devices")
		// Note: response already written by handleBuildNativeBundle

	default:
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid mode, use 'dev' or 'bundle'"})
	}
}

// handleDevServerEvents streams dev server events via SSE.
// GET /dev/events
func (s *HTTPServer) handleDevServerEvents(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.devServerMgr.Subscribe()
	defer s.devServerMgr.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleDevServerProxy reverse-proxies requests to the local dev server.
// /dev/* → http://127.0.0.1:{devServerPort}/*
// Supports both HTTP and WebSocket (needed for Metro HMR hot reload).
func (s *HTTPServer) handleDevServerProxy(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	proxy := s.devServerMgr.Proxy()
	if proxy == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no dev server running"})
		return
	}

	// Strip the /dev prefix before forwarding
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/dev")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}

	// WebSocket upgrade — Metro uses WS for HMR (/hot) and debugger (/debugger-proxy)
	if isWebSocketUpgrade(r) {
		port := s.devServerMgr.DevServerPort()
		s.proxyWebSocket(w, r, fmt.Sprintf("127.0.0.1:%d", port))
		return
	}

	proxy.ServeHTTP(w, r)
}

// handleBuildNativeBundle builds a production Hermes bytecode bundle for the active project.
// POST /dev/build-native { "platform": "ios" }
// Returns { "status": "ok", "bundleUrl": "/dev/native-bundle", "moduleName": "main" } on success.
func (s *HTTPServer) handleBuildNativeBundle(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	status := s.devServerMgr.Status()
	if status == nil || status.WorkDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no active dev server — start one first"})
		return
	}

	var req struct {
		Platform string `json:"platform"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Platform == "" {
		req.Platform = "ios"
	}

	workDir := status.WorkDir
	buildDir := filepath.Join(workDir, ".yaver-build")
	bundlePath := filepath.Join(buildDir, "main.jsbundle")

	log.Printf("[super-host] build-native called: platform=%s workDir=%s", req.Platform, workDir)
	s.devServerMgr.EmitLog("Building native bundle...")

	os.MkdirAll(buildDir, 0o755)

	// ── Check node_modules ──
	if _, err := os.Stat(filepath.Join(workDir, "node_modules")); os.IsNotExist(err) {
		s.devServerMgr.EmitLog("Installing dependencies...")
		install := exec.Command("npm", "install", "--legacy-peer-deps")
		install.Dir = workDir
		if err := install.Run(); err != nil {
			install = exec.Command("yarn", "install")
			install.Dir = workDir
			install.Run()
		}
	}

	// ── Detect project type ──
	pkgData, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
	appJsonData, _ := os.ReadFile(filepath.Join(workDir, "app.json"))

	// Expo detection: check package.json deps + app.json
	isExpo := false
	if strings.Contains(string(pkgData), `"expo"`) {
		// Verify it's actually the expo framework, not just expo-* packages
		if _, err := os.Stat(filepath.Join(workDir, "node_modules", "expo", "AppEntry.js")); err == nil {
			isExpo = true
		} else if strings.Contains(string(appJsonData), `"expo"`) {
			isExpo = true
		}
	}

	// ── Bundle ──
	var bundleCmd string
	if isExpo {
		s.devServerMgr.EmitLog(fmt.Sprintf("Bundling with Expo for %s...", req.Platform))
		bundleCmd = fmt.Sprintf("npx expo export:embed --platform %s --bundle-output %s --assets-dest %s --dev false --minify true --reset-cache",
			req.Platform, bundlePath, filepath.Join(buildDir, "assets"))
	} else {
		// Find entry file: package.json "main" → fallback candidates
		entryFile := "index.js"
		var pkg struct {
			Main string `json:"main"`
		}
		json.Unmarshal(pkgData, &pkg)
		if pkg.Main != "" {
			entryFile = pkg.Main
		}
		// Check for expo-router entry
		if strings.Contains(string(pkgData), `"expo-router"`) {
			candidate := "node_modules/expo-router/entry.js"
			if _, err := os.Stat(filepath.Join(workDir, candidate)); err == nil {
				entryFile = candidate
			}
		}
		s.devServerMgr.EmitLog(fmt.Sprintf("Bundling %s for %s...", entryFile, req.Platform))
		bundleCmd = fmt.Sprintf("npx react-native bundle --platform %s --entry-file %s --bundle-output %s --assets-dest %s --dev false --minify true --reset-cache",
			req.Platform, entryFile, bundlePath, filepath.Join(buildDir, "assets"))
	}

	cmd := exec.Command("sh", "-c", bundleCmd)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "NODE_ENV=production")

	log.Printf("[super-host] bundling with command: %s", bundleCmd)
	logW := &devLogWriter{prefix: "[super-host]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		s.devServerMgr.EmitLog(fmt.Sprintf("Bundle failed: %v", err))
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("bundle failed: %v. Check agent logs for details.", err)})
		return
	}

	// ── Hermes compile ──
	s.devServerMgr.EmitLog("Compiling Hermes bytecode...")

	// Use embedded hermesc (matches Yaver app's exact Hermes version) — never user's local one
	hermescPath, hermescErr := GetEmbeddedHermesc()
	if hermescErr != nil {
		log.Printf("[super-host] embedded hermesc not available: %v — falling back to project hermesc", hermescErr)
		hermescPath = findHermesc(workDir)
	}

	if hermescPath == "" {
		log.Printf("[super-host] hermesc not found — serving plain JS bundle")
		s.devServerMgr.EmitLog("hermesc not found — serving plain JS bundle (slower but works)")
	} else {
		log.Printf("[super-host] using hermesc at: %s", hermescPath)
		tmpPath := bundlePath + ".tmp"
		os.Rename(bundlePath, tmpPath)

		hermesCmd := exec.Command(hermescPath, "-emit-binary", "-out", bundlePath, "-O", tmpPath)
		hermesCmd.Dir = workDir
		hermesLogW := &devLogWriter{prefix: "[super-host:hermesc]"}
		hermesCmd.Stdout = hermesLogW
		hermesCmd.Stderr = hermesLogW

		if err := hermesCmd.Run(); err != nil {
			os.Rename(tmpPath, bundlePath)
			log.Printf("[super-host] hermesc failed: %v — using plain JS", err)
			s.devServerMgr.EmitLog(fmt.Sprintf("hermesc failed, using plain JS: %v", err))
		} else {
			os.Remove(tmpPath)
			log.Printf("[super-host] hermesc compile complete")
		}
	}

	// ── Detect module name from bundle ──
	moduleName := detectModuleName(bundlePath, workDir)
	log.Printf("[super-host] detected module name: %s", moduleName)

	// ── Validate bundle integrity (magic, BC version, size, MD5) ──
	// expectedBCVersion 96 = Yaver's embedded Hermes from RN 0.81.5
	meta, valErr := ValidateHBC(bundlePath, 96)
	if valErr != nil {
		errMsg := fmt.Sprintf("Bundle validation failed: %v", valErr)
		log.Printf("[super-host] %s", errMsg)
		s.devServerMgr.EmitLog(errMsg)
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": errMsg,
			"code":  "BUNDLE_VALIDATION_FAILED",
		})
		return
	}
	meta.ModuleName = moduleName
	log.Printf("[super-host] bundle validated: %d bytes, MD5=%s, BC%d, module=%s",
		meta.Size, meta.MD5, meta.HermesBCVersion, meta.ModuleName)

	// Store metadata for the /dev/native-bundle endpoint to attach as header
	s.devServerMgr.SetBundleMetadata(meta.JSON())

	// ── Check for assets ──
	assetsDir := filepath.Join(buildDir, "assets")
	hasAssets := false
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(assetsDir)
		hasAssets = len(entries) > 0
	}
	log.Printf("[super-host] hasAssets=%v", hasAssets)

	s.devServerMgr.EmitLog(fmt.Sprintf("Bundle ready: %d KB, MD5 verified, BC%d, module: %s",
		meta.Size/1024, meta.HermesBCVersion, moduleName))

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"bundleUrl":  "/dev/native-bundle",
		"assetsUrl":  "/dev/native-assets",
		"size":       meta.Size,
		"md5":        meta.MD5,
		"bcVersion":  meta.HermesBCVersion,
		"platform":   req.Platform,
		"moduleName": moduleName,
		"hasAssets":  hasAssets,
	})
}

// handleServeNativeBundle serves the compiled native bundle file.
// GET /dev/native-bundle
func (s *HTTPServer) handleServeNativeBundle(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		http.Error(w, "no dev server", http.StatusServiceUnavailable)
		return
	}

	status := s.devServerMgr.Status()
	if status == nil || status.WorkDir == "" {
		http.Error(w, "no active project", http.StatusBadRequest)
		return
	}

	bundlePath := filepath.Join(status.WorkDir, ".yaver-build", "main.jsbundle")
	if _, err := os.Stat(bundlePath); err != nil {
		http.Error(w, "no native bundle — call POST /dev/build-native first", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=main.jsbundle")

	// Attach bundle metadata header for integrity validation on phone
	if s.devServerMgr != nil {
		if metaJSON := s.devServerMgr.GetBundleMetadata(); metaJSON != "" {
			w.Header().Set("X-Yaver-Bundle-Metadata", metaJSON)
		}
	}

	http.ServeFile(w, r, bundlePath)
}

// detectModuleName scans the JS bundle for AppRegistry.registerComponent('Name', ...)
// and falls back to app.json "name" field, then "main".
func detectModuleName(bundlePath, workDir string) string {
	if bundleData, err := os.ReadFile(bundlePath); err == nil {
		bundleStr := string(bundleData)
		// Search for AppRegistry.registerComponent('X', ...) — works in both minified and non-minified
		idx := strings.Index(bundleStr, "registerComponent")
		if idx > 0 {
			rest := bundleStr[idx:]
			for i := 0; i < len(rest) && i < 200; i++ {
				if rest[i] == '"' || rest[i] == '\'' {
					quote := rest[i]
					end := strings.IndexByte(rest[i+1:], quote)
					if end > 0 && end < 100 {
						detected := rest[i+1 : i+1+end]
						if detected != "" && !strings.ContainsAny(detected, " \t\n{}()[]") {
							return detected
						}
					}
					break
				}
			}
		}
	}

	// Fallback: check app.json
	appJsonPath := filepath.Join(workDir, "app.json")
	if data, err := os.ReadFile(appJsonPath); err == nil {
		var appJson struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &appJson) == nil && appJson.Name != "" {
			return appJson.Name
		}
	}

	return "main"
}

// handleServeNativeAssets serves the assets directory as a zip archive.
// GET /dev/native-assets
func (s *HTTPServer) handleServeNativeAssets(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		http.Error(w, "no dev server", http.StatusServiceUnavailable)
		return
	}

	status := s.devServerMgr.Status()
	if status == nil || status.WorkDir == "" {
		http.Error(w, "no active project", http.StatusBadRequest)
		return
	}

	assetsDir := filepath.Join(status.WorkDir, ".yaver-build", "assets")
	if _, err := os.Stat(assetsDir); err != nil {
		http.Error(w, "no assets — call POST /dev/build-native first", http.StatusNotFound)
		return
	}

	// Stream zip directly to response
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=assets.zip")

	zipW := zip.NewWriter(w)
	defer zipW.Close()

	filepath.Walk(assetsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(assetsDir, path)
		// Use Store (no compression) so the phone can extract without zlib
		header := &zip.FileHeader{
			Name:   relPath,
			Method: zip.Store,
		}
		f, err := zipW.CreateHeader(header)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	})
}

// findHermesc looks for hermesc in the project's react-native installation.
func findHermesc(workDir string) string {
	candidates := []string{
		filepath.Join(workDir, "node_modules", "react-native", "sdks", "hermesc", "osx-bin", "hermesc"),
		filepath.Join(workDir, "node_modules", "react-native", "sdks", "hermesc", "linux64-bin", "hermesc"),
		filepath.Join(workDir, "node_modules", "hermes-engine", "osx-bin", "hermesc"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			os.Chmod(c, 0o755)
			return c
		}
	}
	return ""
}

// handleDevServerBuilds lists or clears build markers.
// GET /dev/builds — list all build markers
// DELETE /dev/builds?project=BentoApp — clear a specific build marker
// DELETE /dev/builds — clear all build markers
func (s *HTTPServer) handleDevServerBuilds(w http.ResponseWriter, r *http.Request) {
	buildsDir := yaverBuildsDir()

	if r.Method == http.MethodDelete {
		project := r.URL.Query().Get("project")
		if project != "" {
			marker := filepath.Join(buildsDir, project+".built")
			os.Remove(marker)
			jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "cleared": project})
		} else {
			// Clear all
			entries, _ := os.ReadDir(buildsDir)
			count := 0
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".built") {
					os.Remove(filepath.Join(buildsDir, e.Name()))
					count++
				}
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "cleared": count})
		}
		return
	}

	// GET — list
	entries, _ := os.ReadDir(buildsDir)
	var builds []map[string]string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".built") {
			name := strings.TrimSuffix(e.Name(), ".built")
			info, _ := e.Info()
			builtAt := ""
			if info != nil {
				builtAt = info.ModTime().UTC().Format("2006-01-02 15:04:05")
			}
			builds = append(builds, map[string]string{"project": name, "builtAt": builtAt})
		}
	}
	if builds == nil {
		builds = []map[string]string{}
	}
	jsonReply(w, http.StatusOK, builds)
}

// handleDevServerCompatibility checks if the user's project is compatible with
// the Yaver super-host (i.e., all required native modules are available).
// POST /dev/compatibility { "availableModules": ["expo-camera", ...] }
func (s *HTTPServer) handleDevServerCompatibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	var req struct {
		AvailableModules []string `json:"availableModules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Get the work dir from dev server or task manager
	workDir := ""
	if s.devServerMgr != nil {
		if status := s.devServerMgr.Status(); status != nil {
			workDir = status.WorkDir
		}
	}
	if workDir == "" && s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}
	if workDir == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"compatible": true, "missingModules": []string{},
		})
		return
	}

	// Read user project's package.json to find native dependencies
	pkgPath := workDir + "/package.json"
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"compatible": true, "missingModules": []string{},
		})
		return
	}

	available := make(map[string]bool)
	for _, m := range req.AvailableModules {
		available[m] = true
	}

	// Check which native deps the user project needs that Yaver doesn't have
	var missing []string
	nativeDeps := []string{
		"expo-camera", "expo-location", "expo-sensors", "expo-haptics",
		"expo-brightness", "expo-battery", "expo-device", "expo-constants",
		"expo-barcode-scanner", "expo-notifications", "expo-file-system",
		"expo-asset", "expo-font", "expo-clipboard", "expo-linking",
		"expo-secure-store", "expo-av", "expo-image-picker", "expo-speech",
		"expo-web-browser", "expo-apple-authentication",
		"react-native-maps", "react-native-ble-plx",
		"react-native-reanimated", "react-native-gesture-handler",
		"react-native-screens", "react-native-safe-area-context",
		"react-native-webview", "@react-native-async-storage/async-storage",
		"@react-native-community/netinfo",
	}

	content := string(data)
	for _, dep := range nativeDeps {
		// If the user's project uses this dep but Yaver doesn't have it
		if strings.Contains(content, `"`+dep+`"`) && !available[dep] {
			missing = append(missing, dep)
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"compatible":     len(missing) == 0,
		"missingModules": missing,
	})
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// proxyWebSocket tunnels a WebSocket connection to the target.
func (s *HTTPServer) proxyWebSocket(w http.ResponseWriter, r *http.Request, target string) {
	// Connect to the backend
	backendConn, err := net.Dial("tcp", target)
	if err != nil {
		http.Error(w, "backend unavailable", http.StatusBadGateway)
		return
	}
	defer backendConn.Close()

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket not supported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Forward the original HTTP upgrade request to backend
	if err := r.Write(backendConn); err != nil {
		return
	}

	// Flush any buffered data from the client
	if clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		clientBuf.Read(buffered)
		backendConn.Write(buffered)
	}

	// Bidirectional copy
	done := make(chan struct{}, 2)
	go func() { io.Copy(clientConn, backendConn); done <- struct{}{} }()
	go func() { io.Copy(backendConn, clientConn); done <- struct{}{} }()
	<-done
}
