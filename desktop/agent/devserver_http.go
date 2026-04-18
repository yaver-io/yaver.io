package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

type nativeBuildStatus struct {
	State         string `json:"state"`
	Platform      string `json:"platform,omitempty"`
	LastBuiltAt   string `json:"lastBuiltAt,omitempty"`
	LastFailedAt  string `json:"lastFailedAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	BundleSize    int64  `json:"bundleSize,omitempty"`
	BundlePath    string `json:"bundlePath,omitempty"`
	ModuleName    string `json:"moduleName,omitempty"`
	HermesVersion int    `json:"hermesVersion,omitempty"`
}

type projectPreparationStatus struct {
	PackageManager             string   `json:"packageManager,omitempty"`
	DependenciesInstalled      bool     `json:"dependenciesInstalled"`
	NeedsDependencyInstall     bool     `json:"needsDependencyInstall"`
	CanAutoInstallDependencies bool     `json:"canAutoInstallDependencies"`
	MissingTools               []string `json:"missingTools,omitempty"`
	HermesCompiler             string   `json:"hermesCompiler,omitempty"`
	HermesCompilerError        string   `json:"hermesCompilerError,omitempty"`
}

type projectPackageManifest struct {
	Main             string                 `json:"main"`
	PackageManager   string                 `json:"packageManager"`
	Dependencies     map[string]string      `json:"dependencies"`
	PeerDependencies map[string]string      `json:"peerDependencies"`
	DevDependencies  map[string]string      `json:"devDependencies"`
	ReactNative      map[string]interface{} `json:"reactNative"`
}

func nativeBuildStatusPath(workDir string) string {
	return filepath.Join(workDir, ".yaver-build", "status.json")
}

func readNativeBuildStatus(workDir string) nativeBuildStatus {
	status := nativeBuildStatus{State: "needs_build"}

	buildDir := filepath.Join(workDir, ".yaver-build")
	bundlePath := filepath.Join(buildDir, "main.jsbundle")
	if info, err := os.Stat(bundlePath); err == nil && !info.IsDir() {
		status.State = "ready"
		status.BundlePath = bundlePath
		status.BundleSize = info.Size()
		status.LastBuiltAt = info.ModTime().UTC().Format(time.RFC3339)
	}

	data, err := os.ReadFile(nativeBuildStatusPath(workDir))
	if err != nil {
		return status
	}
	var stored nativeBuildStatus
	if json.Unmarshal(data, &stored) != nil {
		return status
	}
	if stored.State != "" {
		status.State = stored.State
	}
	if stored.Platform != "" {
		status.Platform = stored.Platform
	}
	if stored.LastBuiltAt != "" {
		status.LastBuiltAt = stored.LastBuiltAt
	}
	if stored.LastFailedAt != "" {
		status.LastFailedAt = stored.LastFailedAt
	}
	if stored.LastError != "" {
		status.LastError = stored.LastError
	}
	if stored.BundleSize > 0 {
		status.BundleSize = stored.BundleSize
	}
	if stored.BundlePath != "" {
		status.BundlePath = stored.BundlePath
	}
	if stored.ModuleName != "" {
		status.ModuleName = stored.ModuleName
	}
	if stored.HermesVersion > 0 {
		status.HermesVersion = stored.HermesVersion
	}

	if _, err := os.Stat(bundlePath); err != nil {
		if status.State == "ready" {
			status.State = "needs_build"
		}
		status.BundlePath = ""
		status.BundleSize = 0
		status.LastBuiltAt = ""
	}

	return status
}

func writeNativeBuildStatus(workDir string, status nativeBuildStatus) {
	buildDir := filepath.Join(workDir, ".yaver-build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(nativeBuildStatusPath(workDir), data, 0o644)
}

func buildStateGuidance(state nativeBuildStatus) string {
	switch state.State {
	case "ready":
		if state.LastBuiltAt != "" {
			return fmt.Sprintf("Hermes bundle already compiled on this machine (%s). Open in Yaver should load it immediately, and Rebuild Hermes is available if you changed source.", state.LastBuiltAt)
		}
		return "Hermes bundle already compiled on this machine. Open in Yaver should load it immediately."
	case "building":
		return "Hermes bundle build is currently running on the agent."
	case "build_failed":
		if state.LastError != "" {
			return fmt.Sprintf("Last Hermes build failed: %s", state.LastError)
		}
		return "Last Hermes build failed. Rebuild Hermes after fixing dependencies or bundler errors."
	default:
		return "This project is still source-only on this machine. Compile Hermes once, then Open in Yaver on the phone."
	}
}

func detectProjectPackageManager(workDir string, manifest *projectPackageManifest) string {
	if manifest != nil {
		pm := strings.TrimSpace(manifest.PackageManager)
		if pm != "" {
			if idx := strings.Index(pm, "@"); idx > 0 {
				pm = pm[:idx]
			}
			switch pm {
			case "npm", "yarn", "pnpm", "bun":
				return pm
			}
		}
	}
	switch {
	case projectFileExists(filepath.Join(workDir, "pnpm-lock.yaml")):
		return "pnpm"
	case projectFileExists(filepath.Join(workDir, "yarn.lock")):
		return "yarn"
	case projectFileExists(filepath.Join(workDir, "bun.lock")), projectFileExists(filepath.Join(workDir, "bun.lockb")):
		return "bun"
	default:
		return "npm"
	}
}

func projectFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func detectProjectPreparation(workDir string, manifest *projectPackageManifest) projectPreparationStatus {
	prep := projectPreparationStatus{
		PackageManager: detectProjectPackageManager(workDir, manifest),
	}

	nodeExists := commandExists("node")
	npmExists := commandExists("npm")
	npxExists := commandExists("npx")
	yarnExists := commandExists("yarn")
	pnpmExists := commandExists("pnpm")
	bunExists := commandExists("bun")
	bunxExists := commandExists("bunx")

	if !nodeExists {
		prep.MissingTools = append(prep.MissingTools, "node")
	}
	switch prep.PackageManager {
	case "npm":
		if !npmExists {
			prep.MissingTools = append(prep.MissingTools, "npm")
		}
		if !npxExists {
			prep.MissingTools = append(prep.MissingTools, "npx")
		}
	case "yarn":
		if !yarnExists {
			prep.MissingTools = append(prep.MissingTools, "yarn")
		}
		if !npxExists {
			prep.MissingTools = append(prep.MissingTools, "npx")
		}
	case "pnpm":
		if !pnpmExists {
			prep.MissingTools = append(prep.MissingTools, "pnpm")
		}
		if !npxExists {
			prep.MissingTools = append(prep.MissingTools, "npx")
		}
	case "bun":
		if !bunExists {
			prep.MissingTools = append(prep.MissingTools, "bun")
		}
		if !bunxExists {
			prep.MissingTools = append(prep.MissingTools, "bunx")
		}
	}

	prep.DependenciesInstalled = projectFileExists(filepath.Join(workDir, "node_modules", ".yarn-integrity")) || projectFileExists(filepath.Join(workDir, "node_modules", ".modules.yaml"))
	if !prep.DependenciesInstalled {
		if stat, err := os.Stat(filepath.Join(workDir, "node_modules")); err == nil && stat.IsDir() {
			prep.DependenciesInstalled = true
		}
	}
	prep.NeedsDependencyInstall = !prep.DependenciesInstalled

	if prep.NeedsDependencyInstall {
		switch prep.PackageManager {
		case "npm":
			prep.CanAutoInstallDependencies = nodeExists && npmExists
		case "yarn":
			prep.CanAutoInstallDependencies = nodeExists && yarnExists
		case "pnpm":
			prep.CanAutoInstallDependencies = nodeExists && pnpmExists
		case "bun":
			prep.CanAutoInstallDependencies = bunExists
		}
	}

	if _, err := GetEmbeddedHermesc(); err == nil {
		prep.HermesCompiler = "embedded"
	} else if path := findProjectHermesc(workDir); path != "" {
		prep.HermesCompiler = "project"
	} else {
		info := detectHermesRuntimeInfo(workDir)
		if info.HermesRef == "" {
			prep.HermesCompiler = "missing"
			prep.HermesCompilerError = "Hermes compiler could not be resolved from the embedded runtime or the project dependencies yet."
		} else if err := ensureHermescBuildDeps(); err == nil {
			prep.HermesCompiler = "buildable"
		} else {
			prep.HermesCompiler = "missing"
			prep.HermesCompilerError = err.Error()
		}
	}

	return prep
}

func installProjectDependencies(workDir string, prep projectPreparationStatus) error {
	var cmd *exec.Cmd
	switch prep.PackageManager {
	case "yarn":
		cmd = exec.Command("yarn", "install")
	case "pnpm":
		cmd = exec.Command("pnpm", "install")
	case "bun":
		cmd = exec.Command("bun", "install")
	default:
		cmd = exec.Command("npm", "install", "--legacy-peer-deps")
	}
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func readProjectPackageManifest(workDir string) (*projectPackageManifest, error) {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return nil, err
	}
	var manifest projectPackageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func mobileProjectStatus(workDir string) map[string]interface{} {
	manifest, err := readProjectPackageManifest(workDir)
	if err != nil {
		return map[string]interface{}{
			"workDir":    workDir,
			"ok":         false,
			"error":      fmt.Sprintf("package.json not found or invalid: %v", err),
			"buildState": "needs_build",
		}
	}
	prep := detectProjectPreparation(workDir, manifest)
	buildState := readNativeBuildStatus(workDir)
	return map[string]interface{}{
		"workDir":                    workDir,
		"ok":                         len(prep.MissingTools) == 0,
		"packageManager":             prep.PackageManager,
		"dependenciesInstalled":      prep.DependenciesInstalled,
		"needsDependencyInstall":     prep.NeedsDependencyInstall,
		"canAutoInstallDependencies": prep.CanAutoInstallDependencies,
		"missingTools":               prep.MissingTools,
		"hermesCompiler":             prep.HermesCompiler,
		"hermesCompilerError":        prep.HermesCompilerError,
		"buildState":                 buildState.State,
		"lastBuildAt":                buildState.LastBuiltAt,
		"lastBuildFailedAt":          buildState.LastFailedAt,
		"lastBuildError":             buildState.LastError,
		"compiledBundleSize":         buildState.BundleSize,
		"compiledModuleName":         buildState.ModuleName,
	}
}

func (s *HTTPServer) ensureDevServerManager() *DevServerManager {
	if s.devServerMgr == nil {
		s.devServerMgr = NewDevServerManager()
	}
	return s.devServerMgr
}

func (s *HTTPServer) ensureDevServerForProject(workDir, framework, platform string) error {
	mgr := s.ensureDevServerManager()
	if status := mgr.Status(); status != nil && status.Running && status.WorkDir == workDir {
		return nil
	}
	if err := mgr.Start(framework, workDir, platform, 0, DevServerTarget{}); err != nil {
		return err
	}
	for i := 0; i < 60; i++ {
		time.Sleep(1 * time.Second)
		status := mgr.Status()
		if status != nil && status.Running && status.WorkDir == workDir {
			return nil
		}
		if status == nil {
			continue
		}
		if status.Error != "" {
			return fmt.Errorf(status.Error)
		}
	}
	return fmt.Errorf("dev server did not become ready in time")
}

func (s *HTTPServer) buildNativeBundleForProject(workDir, framework, platform string) (map[string]interface{}, error) {
	if platform == "" {
		platform = "ios"
	}
	if err := s.ensureDevServerForProject(workDir, framework, platform); err != nil {
		return nil, err
	}

	body := bytes.NewBufferString(fmt.Sprintf(`{"platform":%q}`, platform))
	req := httptest.NewRequest(http.MethodPost, "/dev/build-native", body)
	rec := httptest.NewRecorder()
	s.handleBuildNativeBundle(rec, req)

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		if rec.Code >= 400 {
			return nil, fmt.Errorf(strings.TrimSpace(rec.Body.String()))
		}
		return nil, err
	}
	if rec.Code >= 400 {
		if msg, _ := result["error"].(string); msg != "" {
			return nil, fmt.Errorf(msg)
		}
		return nil, fmt.Errorf("native build failed with status %d", rec.Code)
	}
	return result, nil
}

func bundleCommand(packageManager, framework, platform, entryFile, bundlePath, assetsDir string) *exec.Cmd {
	switch framework {
	case "expo":
		switch packageManager {
		case "yarn":
			return exec.Command("yarn", "expo", "export:embed", "--platform", platform, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		case "pnpm":
			return exec.Command("pnpm", "exec", "expo", "export:embed", "--platform", platform, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		case "bun":
			return exec.Command("bunx", "expo", "export:embed", "--platform", platform, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		default:
			return exec.Command("npx", "expo", "export:embed", "--platform", platform, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		}
	default:
		switch packageManager {
		case "yarn":
			return exec.Command("yarn", "react-native", "bundle", "--platform", platform, "--entry-file", entryFile, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		case "pnpm":
			return exec.Command("pnpm", "exec", "react-native", "bundle", "--platform", platform, "--entry-file", entryFile, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		case "bun":
			return exec.Command("bunx", "react-native", "bundle", "--platform", platform, "--entry-file", entryFile, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		default:
			return exec.Command("npx", "react-native", "bundle", "--platform", platform, "--entry-file", entryFile, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true", "--reset-cache")
		}
	}
}

// handleDevServerStatus returns the current dev server status.
// GET /dev/status
func (s *HTTPServer) handleDevServerStatus(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	resolvedIOSMethod, resolvedIOSReason := resolveIOSInstallMethodWithReason(s.iosInstallMethod)
	target := s.devServerMgr.PreferredTarget()
	status := s.devServerMgr.Status()
	if status == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"running":           false,
			"targetDeviceId":    target.DeviceID,
			"targetDeviceName":  target.DeviceName,
			"targetDeviceClass": target.DeviceClass,
			"iosInstallMethod":  resolvedIOSMethod,
			"iosInstallReason":  resolvedIOSReason,
		})
		return
	}

	status.IOSInstallMethod = resolvedIOSMethod
	status.IOSInstallReason = resolvedIOSReason
	jsonReply(w, http.StatusOK, status)
}

// handleDevServerTarget gets or updates the preferred dev preview target.
// GET /dev/target
// POST /dev/target { "targetDeviceId": "...", "targetDeviceName": "...", "targetDeviceClass": "..." }
func (s *HTTPServer) handleDevServerTarget(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		target := s.devServerMgr.PreferredTarget()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"targetDeviceId":    target.DeviceID,
			"targetDeviceName":  target.DeviceName,
			"targetDeviceClass": target.DeviceClass,
		})
	case http.MethodPost:
		var req struct {
			TargetDeviceID    string `json:"targetDeviceId"`
			TargetDeviceName  string `json:"targetDeviceName"`
			TargetDeviceClass string `json:"targetDeviceClass"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		s.devServerMgr.SetPreferredTarget(DevServerTarget{
			DeviceID:    req.TargetDeviceID,
			DeviceName:  req.TargetDeviceName,
			DeviceClass: req.TargetDeviceClass,
		})

		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":                true,
			"targetDeviceId":    req.TargetDeviceID,
			"targetDeviceName":  req.TargetDeviceName,
			"targetDeviceClass": req.TargetDeviceClass,
		})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
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
		Framework         string `json:"framework"` // "expo", "flutter", "vite", "nextjs", "" (auto-detect)
		WorkDir           string `json:"workDir"`
		Platform          string `json:"platform"` // "ios", "android", "web"
		Port              int    `json:"port"`
		Rebuild           bool   `json:"rebuild"` // force rebuild (clear build marker)
		TargetDeviceID    string `json:"targetDeviceId"`
		TargetDeviceName  string `json:"targetDeviceName"`
		TargetDeviceClass string `json:"targetDeviceClass"`
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

	if req.TargetDeviceID != "" || req.TargetDeviceName != "" || req.TargetDeviceClass != "" {
		s.devServerMgr.SetPreferredTarget(DevServerTarget{
			DeviceID:    req.TargetDeviceID,
			DeviceName:  req.TargetDeviceName,
			DeviceClass: req.TargetDeviceClass,
		})
	}

	// Clear build marker if rebuild requested
	if req.Rebuild && req.WorkDir != "" {
		projectHash := strings.ReplaceAll(filepath.Base(req.WorkDir), " ", "_")
		marker := filepath.Join(yaverBuildsDir(), projectHash+".built")
		os.Remove(marker)
		log.Printf("[dev] Cleared build marker for %s (rebuild requested)", projectHash)
	}

	if err := s.devServerMgr.Start(req.Framework, req.WorkDir, req.Platform, req.Port, DevServerTarget{
		DeviceID:    req.TargetDeviceID,
		DeviceName:  req.TargetDeviceName,
		DeviceClass: req.TargetDeviceClass,
	}); err != nil {
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
		if sent := s.sendPreviewWorkerReloadCommand(); sent {
			log.Printf("[dev] Sent targeted preview reload command to preview worker")
		} else {
			s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
				Command: "reload",
			})
			log.Printf("[dev] Broadcast reload command to connected SDK devices")
		}
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
		if sent := s.sendPreviewWorkerReloadCommand(); sent {
			log.Printf("[dev] Reload-app (dev mode): sent targeted preview reload to preview worker")
		} else {
			s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
				Command: "reload",
			})
			log.Printf("[dev] Reload-app (dev mode): broadcast reload to SDK devices")
		}
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "mode": "dev"})

	case "bundle":
		// Native bundle: rebuild and tell SDK devices to fetch new bundle
		// First trigger the build (reuse build-native logic)
		s.handleBuildNativeBundle(w, r)
		// After build completes (handleBuildNativeBundle writes response),
		// push reload_bundle command
		cmd := BlackBoxCommand{
			Command: "reload_bundle",
			Data: map[string]interface{}{
				"bundleUrl": "/dev/native-bundle",
				"assetsUrl": "/dev/native-assets",
			},
		}
		if sent := s.sendCommandToPreviewTarget(cmd); sent {
			log.Printf("[dev] Reload-app (bundle mode): sent targeted reload_bundle to preview worker")
		} else {
			s.blackboxMgr.BroadcastCommand(cmd)
			log.Printf("[dev] Reload-app (bundle mode): broadcast reload_bundle to SDK devices")
		}
		// Note: response already written by handleBuildNativeBundle

	default:
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid mode, use 'dev' or 'bundle'"})
	}
}

func (s *HTTPServer) sendCommandToPreviewTarget(cmd BlackBoxCommand) bool {
	if s.blackboxMgr == nil || s.devServerMgr == nil {
		return false
	}
	target := s.devServerMgr.PreferredTarget()
	if target.DeviceID == "" {
		return false
	}
	return s.blackboxMgr.SendCommandToDevice(target.DeviceID, cmd)
}

func (s *HTTPServer) sendPreviewWorkerReloadCommand() bool {
	return s.sendCommandToPreviewTarget(BlackBoxCommand{
		Command: "reload_bundle",
		Data: map[string]interface{}{
			"bundleUrl":  "/dev/native-bundle",
			"assetsUrl":  "/dev/native-assets",
			"moduleName": "main",
		},
	})
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
	writeNativeBuildStatus(workDir, nativeBuildStatus{
		State:    "building",
		Platform: req.Platform,
	})

	os.MkdirAll(buildDir, 0o755)

	manifest, manifestErr := readProjectPackageManifest(workDir)
	if manifestErr != nil {
		writeNativeBuildStatus(workDir, nativeBuildStatus{
			State:        "build_failed",
			Platform:     req.Platform,
			LastFailedAt: time.Now().UTC().Format(time.RFC3339),
			LastError:    manifestErr.Error(),
		})
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("package.json missing or invalid: %v", manifestErr)})
		return
	}
	prep := detectProjectPreparation(workDir, manifest)
	if len(prep.MissingTools) > 0 {
		errMsg := fmt.Sprintf("missing required tools on this machine: %s", strings.Join(prep.MissingTools, ", "))
		writeNativeBuildStatus(workDir, nativeBuildStatus{
			State:        "build_failed",
			Platform:     req.Platform,
			LastFailedAt: time.Now().UTC().Format(time.RFC3339),
			LastError:    errMsg,
		})
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
		return
	}

	if prep.NeedsDependencyInstall {
		if !prep.CanAutoInstallDependencies {
			errMsg := fmt.Sprintf("dependencies are missing and cannot be auto-installed with %s on this machine", prep.PackageManager)
			writeNativeBuildStatus(workDir, nativeBuildStatus{
				State:        "build_failed",
				Platform:     req.Platform,
				LastFailedAt: time.Now().UTC().Format(time.RFC3339),
				LastError:    errMsg,
			})
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
			return
		}
		s.devServerMgr.EmitLog(fmt.Sprintf("Installing dependencies with %s...", prep.PackageManager))
		if err := installProjectDependencies(workDir, prep); err != nil {
			errMsg := fmt.Sprintf("dependency install failed: %v", err)
			writeNativeBuildStatus(workDir, nativeBuildStatus{
				State:        "build_failed",
				Platform:     req.Platform,
				LastFailedAt: time.Now().UTC().Format(time.RFC3339),
				LastError:    errMsg,
			})
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
			return
		}
		prep = detectProjectPreparation(workDir, manifest)
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
	assetsDir := filepath.Join(buildDir, "assets")
	var cmd *exec.Cmd
	if isExpo {
		s.devServerMgr.EmitLog(fmt.Sprintf("Bundling with Expo for %s...", req.Platform))
		cmd = bundleCommand(prep.PackageManager, "expo", req.Platform, "", bundlePath, assetsDir)
	} else {
		// Find entry file: package.json "main" → fallback candidates
		entryFile := "index.js"
		if manifest.Main != "" {
			entryFile = manifest.Main
		}
		// Check for expo-router entry
		if strings.Contains(string(pkgData), `"expo-router"`) {
			candidate := "node_modules/expo-router/entry.js"
			if _, err := os.Stat(filepath.Join(workDir, candidate)); err == nil {
				entryFile = candidate
			}
		}
		s.devServerMgr.EmitLog(fmt.Sprintf("Bundling %s for %s...", entryFile, req.Platform))
		cmd = bundleCommand(prep.PackageManager, "react-native", req.Platform, entryFile, bundlePath, assetsDir)
	}

	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "NODE_ENV=production")

	log.Printf("[super-host] bundling with command: %v", cmd.Args)
	logW := &devLogWriter{prefix: "[super-host]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		s.devServerMgr.EmitLog(fmt.Sprintf("Bundle failed: %v", err))
		writeNativeBuildStatus(workDir, nativeBuildStatus{
			State:        "build_failed",
			Platform:     req.Platform,
			LastFailedAt: time.Now().UTC().Format(time.RFC3339),
			LastError:    fmt.Sprintf("bundle failed: %v", err),
		})
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("bundle failed: %v. Check agent logs for details.", err)})
		return
	}

	// ── Hermes compile ──
	s.devServerMgr.EmitLog("Compiling Hermes bytecode...")

	info := detectHermesRuntimeInfo(workDir)
	hermescPath, hermescErr := resolveHermesc(workDir)
	if hermescErr != nil {
		log.Printf("[super-host] hermesc resolve failed: %v", hermescErr)
	}

	if hermescPath == "" {
		log.Printf("[super-host] hermesc not found — cannot produce Hermes bundle")
		s.devServerMgr.EmitLog("hermesc not found — cannot produce Hermes bundle")
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
	expectedBCVersion := 0
	if hermescPath != "" {
		if bc, err := hermescBytecodeVersion(hermescPath); err == nil {
			expectedBCVersion = bc
		}
	}
	meta, valErr := ValidateHBC(bundlePath, expectedBCVersion)
	if valErr != nil {
		errMsg := fmt.Sprintf("Bundle validation failed: %v", valErr)
		log.Printf("[super-host] %s", errMsg)
		s.devServerMgr.EmitLog(errMsg)
		writeNativeBuildStatus(workDir, nativeBuildStatus{
			State:        "build_failed",
			Platform:     req.Platform,
			LastFailedAt: time.Now().UTC().Format(time.RFC3339),
			LastError:    errMsg,
		})
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": errMsg,
			"code":  "BUNDLE_VALIDATION_FAILED",
		})
		return
	}
	meta.ModuleName = moduleName
	meta.TargetPlatform = req.Platform
	meta.BuilderPlatform = runtime.GOOS
	meta.BuilderArch = runtime.GOARCH
	meta.ReactNativeVersion = info.ReactNativeVersion
	meta.ExpoSDKVersion = info.ExpoSDKVersion
	meta.HermesRef = info.HermesRef
	log.Printf("[super-host] bundle validated: %d bytes, MD5=%s, BC%d, module=%s",
		meta.Size, meta.MD5, meta.HermesBCVersion, meta.ModuleName)

	// Store metadata for the /dev/native-bundle endpoint to attach as header
	s.devServerMgr.SetBundleMetadata(meta.JSON())

	// ── Check for assets ──
	assetsDir = filepath.Join(buildDir, "assets")
	hasAssets := false
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(assetsDir)
		hasAssets = len(entries) > 0
	}
	log.Printf("[super-host] hasAssets=%v", hasAssets)

	s.devServerMgr.EmitLog(fmt.Sprintf("Bundle ready: %d KB, MD5 verified, BC%d, module: %s",
		meta.Size/1024, meta.HermesBCVersion, moduleName))
	writeNativeBuildStatus(workDir, nativeBuildStatus{
		State:         "ready",
		Platform:      req.Platform,
		LastBuiltAt:   time.Now().UTC().Format(time.RFC3339),
		BundleSize:    meta.Size,
		BundlePath:    bundlePath,
		ModuleName:    moduleName,
		HermesVersion: meta.HermesBCVersion,
	})

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
	return findProjectHermesc(workDir)
}

func hermescSearchRoots(workDir string) []string {
	var roots []string
	seen := map[string]struct{}{}
	cur := filepath.Clean(workDir)
	for {
		if _, ok := seen[cur]; !ok {
			roots = append(roots, cur)
			seen[cur] = struct{}{}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return roots
}

func hermescCandidates(root string) []string {
	if runtime.GOOS == "linux" {
		return []string{
			filepath.Join(root, "node_modules", "react-native", "sdks", "hermesc", "linux64-bin", "hermesc"),
			filepath.Join(root, "node_modules", "hermes-engine", "linux64-bin", "hermesc"),
			filepath.Join(root, "node_modules", "react-native", "sdks", "hermesc", "osx-bin", "hermesc"),
			filepath.Join(root, "node_modules", "hermes-engine", "osx-bin", "hermesc"),
		}
	}
	return []string{
		filepath.Join(root, "node_modules", "react-native", "sdks", "hermesc", "osx-bin", "hermesc"),
		filepath.Join(root, "node_modules", "hermes-engine", "osx-bin", "hermesc"),
		filepath.Join(root, "node_modules", "react-native", "sdks", "hermesc", "linux64-bin", "hermesc"),
		filepath.Join(root, "node_modules", "hermes-engine", "linux64-bin", "hermesc"),
	}
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
		WorkDir          string   `json:"workDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	workDir := strings.TrimSpace(req.WorkDir)
	// Fall back to the current dev-server/task context for older callers.
	if workDir == "" {
		if s.devServerMgr != nil {
			if status := s.devServerMgr.Status(); status != nil {
				workDir = status.WorkDir
			}
		}
		if workDir == "" && s.taskMgr != nil {
			workDir = s.taskMgr.workDir
		}
	}
	if workDir == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"compatible":       true,
			"missingModules":   []string{},
			"availableModules": []string{},
			"warnings":         []string{},
			"needsYaverCLI":    false,
			"needsFeedbackSDK": false,
			"buildState":       "needs_build",
			"recommendedFlow":  "agent-open-in-yaver",
			"guidance":         "No project selected yet. Open in Yaver does not require yaver-cli or the Feedback SDK.",
		})
		return
	}

	pkgPath := workDir + "/package.json"
	if _, err := os.ReadFile(pkgPath); err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"compatible":       true,
			"missingModules":   []string{},
			"availableModules": []string{},
			"warnings":         []string{fmt.Sprintf("package.json not found at %s", pkgPath)},
			"needsYaverCLI":    false,
			"needsFeedbackSDK": false,
			"buildState":       "needs_build",
			"recommendedFlow":  "agent-open-in-yaver",
			"guidance":         "Open in Yaver does not require yaver-cli or the Feedback SDK, but compatibility could not be checked because package.json was not found.",
		})
		return
	}

	pkg, err := readProjectPackageManifest(workDir)
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"compatible":       true,
			"missingModules":   []string{},
			"availableModules": []string{},
			"warnings":         []string{"package.json could not be parsed for compatibility analysis"},
			"needsYaverCLI":    false,
			"needsFeedbackSDK": false,
			"buildState":       "needs_build",
			"recommendedFlow":  "agent-open-in-yaver",
			"guidance":         "Open in Yaver does not require yaver-cli or the Feedback SDK. Compatibility analysis failed, so missing native modules may still block runtime behavior.",
		})
		return
	}

	manifest, manifestErr := loadSDKManifest()
	buildState := readNativeBuildStatus(workDir)
	prep := detectProjectPreparation(workDir, pkg)

	available := make(map[string]bool)
	for _, m := range req.AvailableModules {
		available[m] = true
	}

	allDeps := map[string]string{}
	for name, version := range pkg.PeerDependencies {
		allDeps[name] = version
	}
	for name, version := range pkg.Dependencies {
		allDeps[name] = version
	}

	var missing []string
	var present []string
	var warnings []string
	var errors []string

	projectRN := cleanCompatVersion(allDeps["react-native"])
	sdkRN := ""
	if manifest != nil {
		sdkRN = cleanCompatVersion(manifest.ReactNative)
		if projectRN != "" && sdkRN != "" {
			projSemver := toSemver(projectRN)
			sdkSemver := toSemver(sdkRN)
			if projSemver != "" && sdkSemver != "" {
				if semver.Major(projSemver) != semver.Major(sdkSemver) {
					errors = append(errors, fmt.Sprintf("React Native major version mismatch: project %s, yaver %s.", projectRN, sdkRN))
				} else if semver.MajorMinor(projSemver) != semver.MajorMinor(sdkSemver) {
					warnings = append(warnings, fmt.Sprintf("React Native %s vs yaver %s. Minor version differs and may or may not work.", projectRN, sdkRN))
				}
			}
		}
		if newArch, _ := pkg.ReactNative["newArchEnabled"].(bool); newArch && !manifest.Arch.NewArch {
			errors = append(errors, "Project uses New Architecture but the Yaver app manifest reports classic bridge only.")
		}
	} else if manifestErr != nil {
		warnings = append(warnings, fmt.Sprintf("sdk-manifest.json unavailable: %v", manifestErr))
	}

	for dep := range allDeps {
		if dep == "react" || dep == "react-native" || dep == "expo" {
			continue
		}
		if pkg.DevDependencies != nil && pkg.DevDependencies[dep] != "" && pkg.Dependencies[dep] == "" {
			continue
		}
		if isPureJSPackage(dep) || isFalsePositiveNative(dep) {
			continue
		}
		if looksLikeNativeModule(dep) {
			if available[dep] {
				present = append(present, dep)
				if manifest != nil {
					if sdkVersion, ok := manifest.NativeModules[dep]; ok {
						localVersion := cleanCompatVersion(allDeps[dep])
						localSemver := toSemver(localVersion)
						sdkSemver := toSemver(cleanCompatVersion(sdkVersion))
						if localSemver != "" && sdkSemver != "" && semver.Major(localSemver) != semver.Major(sdkSemver) {
							warnings = append(warnings, fmt.Sprintf("%s major version differs: project %s, yaver %s.", dep, localVersion, sdkVersion))
						}
					}
				}
			} else {
				missing = append(missing, dep)
				errors = append(errors, fmt.Sprintf("%s requires native code but is not present in the Yaver app.", dep))
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(present)

	needsFeedbackSDK := false
	needsYaverCLI := false
	recommendedFlow := "agent-open-in-yaver"
	guidance := "Open in Yaver should work from Linux, WSL, macOS, or a remote host. No project injection is required. yaver-cli is optional for direct CLI push/watch workflows. Add the Feedback SDK only for in-app bug reports or remote reload inside your own app."
	if len(prep.MissingTools) > 0 {
		errors = append(errors, fmt.Sprintf("Missing local build tools: %s.", strings.Join(prep.MissingTools, ", ")))
	}
	if prep.NeedsDependencyInstall {
		if prep.CanAutoInstallDependencies {
			warnings = append(warnings, fmt.Sprintf("Dependencies are not installed yet. Yaver can install them automatically with %s on first build.", prep.PackageManager))
		} else {
			errors = append(errors, fmt.Sprintf("Dependencies are not installed and Yaver cannot auto-install them with %s on this machine.", prep.PackageManager))
		}
	}
	if prep.HermesCompiler == "missing" && prep.HermesCompilerError != "" {
		errors = append(errors, prep.HermesCompilerError)
	}
	if len(errors) > 0 {
		needsYaverCLI = true
		guidance = strings.Join(errors, " ") + " yaver-cli is not required for the agent flow, but its compatibility check/watch workflow may help. The Feedback SDK is still optional."
	} else if len(warnings) > 0 {
		guidance = warnings[0] + " Open in Yaver should still be the default path. yaver-cli remains optional."
	} else {
		guidance = buildStateGuidance(buildState)
	}
	if len(errors) == 0 && buildState.State == "build_failed" && buildState.LastError != "" && !strings.Contains(guidance, buildState.LastError) {
		guidance = buildStateGuidance(buildState)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"compatible":                 len(errors) == 0,
		"missingModules":             missing,
		"availableModules":           present,
		"warnings":                   warnings,
		"errors":                     errors,
		"projectReactNative":         projectRN,
		"sdkReactNative":             sdkRN,
		"needsYaverCLI":              needsYaverCLI,
		"needsFeedbackSDK":           needsFeedbackSDK,
		"recommendedFlow":            recommendedFlow,
		"guidance":                   guidance,
		"buildState":                 buildState.State,
		"canBuildInYaver":            len(errors) == 0,
		"lastBuildAt":                buildState.LastBuiltAt,
		"lastBuildFailedAt":          buildState.LastFailedAt,
		"lastBuildError":             buildState.LastError,
		"compiledBundleSize":         buildState.BundleSize,
		"compiledModuleName":         buildState.ModuleName,
		"packageManager":             prep.PackageManager,
		"dependenciesInstalled":      prep.DependenciesInstalled,
		"needsDependencyInstall":     prep.NeedsDependencyInstall,
		"canAutoInstallDependencies": prep.CanAutoInstallDependencies,
		"missingLocalTools":          prep.MissingTools,
		"hermesCompiler":             prep.HermesCompiler,
		"hermesCompilerError":        prep.HermesCompilerError,
	})
}

type sdkManifestCompat struct {
	ReactNative    string            `json:"reactNative"`
	SupportedRange string            `json:"supportedRNRange"`
	NativeModules  map[string]string `json:"nativeModules"`
	Arch           struct {
		NewArch bool `json:"newArch"`
	} `json:"arch"`
}

func loadSDKManifest() (*sdkManifestCompat, error) {
	candidates := []string{
		"mobile/sdk-manifest.json",
		"sdk-manifest.json",
		filepath.Join("..", "..", "mobile", "sdk-manifest.json"),
		filepath.Join("..", "..", "cli", "sdk-manifest.json"),
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var manifest sdkManifestCompat
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("parse %s: %w", candidate, err)
		}
		return &manifest, nil
	}
	return nil, fmt.Errorf("sdk-manifest.json not found")
}

func cleanCompatVersion(v string) string {
	return strings.TrimLeft(strings.TrimSpace(v), "^~<>= ")
}

func toSemver(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "v") {
		if semver.IsValid(v) {
			return v
		}
		v = strings.TrimPrefix(v, "v")
	}
	parts := strings.Split(v, ".")
	switch len(parts) {
	case 1:
		v = v + ".0.0"
	case 2:
		v = v + ".0"
	}
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return ""
}

func isPureJSPackage(name string) bool {
	switch name {
	case "axios", "lodash", "moment", "date-fns", "uuid", "dayjs",
		"zustand", "jotai", "redux", "@reduxjs/toolkit", "mobx", "mobx-react",
		"react-query", "@tanstack/react-query", "formik", "yup", "zod", "react-hook-form",
		"i18next", "react-i18next", "nativewind", "twrnc", "styled-components", "@emotion/native",
		"swr", "immer", "@react-navigation/native", "@react-navigation/stack",
		"@react-navigation/bottom-tabs", "@react-navigation/drawer", "@react-navigation/native-stack",
		"@react-navigation/material-top-tabs", "react-native-web", "react-dom",
		"@expo/metro-runtime", "expo-router", "expo-splash-screen", "expo-status-bar":
		return true
	default:
		return false
	}
}

func isFalsePositiveNative(name string) bool {
	switch name {
	case "react-native-paper", "react-native-elements", "react-native-size-matters",
		"react-native-responsive-screen", "react-native-toast-message",
		"react-native-responsive-fontsize", "react-native-iphone-x-helper",
		"react-native-status-bar-height", "react-native-markdown-display":
		return true
	default:
		return false
	}
}

func looksLikeNativeModule(name string) bool {
	return strings.HasPrefix(name, "react-native-") ||
		strings.HasPrefix(name, "@react-native-") ||
		strings.HasPrefix(name, "@react-native/") ||
		strings.HasPrefix(name, "rn") ||
		strings.HasPrefix(name, "expo-") ||
		strings.HasPrefix(name, "@shopify/react-native-")
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
