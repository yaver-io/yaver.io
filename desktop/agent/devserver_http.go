package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// activeBuildRegistry tracks in-flight /dev/build-native builds so /dev/stop
// can cancel them. Keyed by workDir+platform so concurrent builds for
// different projects don't collide. Calling the handle's cancel kills the
// bundler subprocess via exec.CommandContext and unblocks the request.
type activeBuildHandle struct {
	cancel context.CancelFunc
}

var (
	activeBuildsMu sync.Mutex
	activeBuilds   = map[string]*activeBuildHandle{}
)

func activeBuildKey(workDir, platform string) string {
	return strings.TrimSpace(workDir) + "\x00" + strings.TrimSpace(platform)
}

// registerActiveBuild stores cancel so /dev/stop can call it. Returns a
// release func to remove the entry on normal completion. If a previous build
// for the same (workDir, platform) is still registered (e.g. mobile retried
// before the prior request returned), it gets cancelled first.
func registerActiveBuild(workDir, platform string, cancel context.CancelFunc) func() {
	h := &activeBuildHandle{cancel: cancel}
	key := activeBuildKey(workDir, platform)
	activeBuildsMu.Lock()
	if prev, ok := activeBuilds[key]; ok && prev != nil && prev.cancel != nil {
		prev.cancel()
	}
	activeBuilds[key] = h
	activeBuildsMu.Unlock()
	return func() {
		activeBuildsMu.Lock()
		if cur, ok := activeBuilds[key]; ok && cur == h {
			delete(activeBuilds, key)
		}
		activeBuildsMu.Unlock()
	}
}

// cancelAllActiveBuilds cancels every registered build and clears the
// registry. Returns the number of builds cancelled.
func cancelAllActiveBuilds() int {
	activeBuildsMu.Lock()
	defer activeBuildsMu.Unlock()
	n := 0
	for k, h := range activeBuilds {
		if h != nil && h.cancel != nil {
			h.cancel()
			n++
		}
		delete(activeBuilds, k)
	}
	return n
}

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
	ConsumerKey   string `json:"consumerKey,omitempty"`
	ConsumerLabel string `json:"consumerLabel,omitempty"`

	// Git fingerprint captured AFTER the last successful Hermes bundle
	// build. Used by build_cache_git.go::checkGitStateBuildCache to
	// short-circuit POST /dev/build-native when nothing the bundle
	// would actually pick up has changed since the cached build.
	//
	// LastBuiltGitSHA: HEAD SHA at build time. Empty if not in a git
	// repo or git was unreachable. Empty disables git-state caching.
	//
	// LastBuiltSourceTreeSHA: sha256 over the dirty bundle-relevant
	// files at build time, "" when the working tree was clean. We
	// re-derive the same hash on the next request and compare; mismatch
	// means uncommitted changes touched something we'd ship.
	//
	// LastBuiltGitHasDirty: convenience flag so we can tell "tree was
	// clean then" apart from "tree had no bundle-relevant dirt". Without
	// it we'd treat "" as ambiguous on the read side.
	LastBuiltGitSHA        string `json:"lastBuiltGitSha,omitempty"`
	LastBuiltSourceTreeSHA string `json:"lastBuiltSourceTreeSha,omitempty"`
	LastBuiltGitHasDirty   bool   `json:"lastBuiltGitHasDirty,omitempty"`
}

type nativeBuildConsumerContract struct {
	Platform        string
	AppVersion      string
	AppBuild        string
	SDKVersion      string
	HermesBCVersion int
}

type nativeBuildCacheDecision struct {
	Valid   bool
	Message string
	Label   string
}

type projectPreparationStatus struct {
	PackageManager             string   `json:"packageManager,omitempty"`
	PackageManagerSpec         string   `json:"-"`
	DependenciesInstalled      bool     `json:"dependenciesInstalled"`
	NeedsDependencyInstall     bool     `json:"needsDependencyInstall"`
	CanAutoInstallDependencies bool     `json:"canAutoInstallDependencies"`
	MissingTools               []string `json:"missingTools,omitempty"`
	HermesCompiler             string   `json:"hermesCompiler,omitempty"`
	HermesCompilerError        string   `json:"hermesCompilerError,omitempty"`
	// Monorepo-awareness. When the project is a member of an
	// npm/yarn/pnpm workspaces monorepo, WorkspaceRoot is the absolute
	// path to the directory whose package.json declares the workspaces
	// array — and that's where `npm install` (etc.) MUST run for the
	// workspace symlinks (carrotbet/node_modules/@backgammon/* →
	// packages/...) to materialise. Running install in the leaf package
	// (mobile/) only populates mobile/node_modules and leaves the
	// workspace deps unresolvable, which then surfaces during bundle
	// build as "Unable to resolve module @scope/foo from <leaf>/X.tsx".
	// Empty when the project isn't part of a workspaces tree.
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
}

type projectPackageManifest struct {
	Main             string                 `json:"main"`
	PackageManager   string                 `json:"packageManager"`
	Dependencies     map[string]string      `json:"dependencies"`
	PeerDependencies map[string]string      `json:"peerDependencies"`
	DevDependencies  map[string]string      `json:"devDependencies"`
	ReactNative      map[string]interface{} `json:"reactNative"`
}

func devOperationID(kind, projectPath, deviceID string) string {
	projectPath = strings.TrimSpace(projectPath)
	deviceID = strings.TrimSpace(deviceID)
	if projectPath == "" {
		projectPath = "unknown"
	}
	if deviceID == "" {
		deviceID = "default"
	}
	projectPath = strings.ReplaceAll(projectPath, string(filepath.Separator), "_")
	projectPath = strings.ReplaceAll(projectPath, " ", "_")
	return fmt.Sprintf("%s:%s:%s", kind, projectPath, deviceID)
}

func (s *HTTPServer) upsertDevOperation(kind, status, phase, message, projectPath, deviceID string, progress float64, metadata map[string]interface{}, incidentIDs ...string) OperationState {
	op := GlobalOperationStore().Upsert(OperationState{
		ID:          devOperationID(kind, projectPath, deviceID),
		Kind:        kind,
		Status:      status,
		Phase:       phase,
		Message:     message,
		Progress:    progress,
		DeviceID:    strings.TrimSpace(deviceID),
		ProjectPath: strings.TrimSpace(projectPath),
		IncidentIDs: devCompactStrings(incidentIDs),
		Metadata:    metadata,
	})
	return op
}

func (s *HTTPServer) appendDevIncident(kind, code, title, userMessage, technicalInfo, suggestedAction, projectPath, deviceID, target string, severity IncidentSeverity, recoverable bool, logsAvailable bool, logRefs []string, metadata map[string]interface{}, operationID string) IncidentEvent {
	return GlobalIncidentStore().Append(IncidentEvent{
		Timestamp:       time.Now().UnixMilli(),
		Severity:        severity,
		Category:        kind,
		Code:            code,
		Source:          "devserver",
		Title:           title,
		UserMessage:     userMessage,
		TechnicalInfo:   technicalInfo,
		SuggestedAction: suggestedAction,
		OperationID:     operationID,
		DeviceID:        strings.TrimSpace(deviceID),
		ProjectPath:     strings.TrimSpace(projectPath),
		Target:          strings.TrimSpace(target),
		LogsAvailable:   logsAvailable,
		LogRefs:         logRefs,
		Recoverable:     recoverable,
		Metadata:        metadata,
	})
}

func devCompactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *HTTPServer) syncPreviewWorkerIncident(projectPath string, target DevServerTarget, delivered bool) {
	if s.blackboxMgr == nil || strings.TrimSpace(target.DeviceID) == "" {
		return
	}
	key := IncidentKey{
		Category:    "reload",
		Code:        ReasonReloadPreviewWorkerOffline,
		DeviceID:    strings.TrimSpace(target.DeviceID),
		ProjectPath: strings.TrimSpace(projectPath),
		Target:      "preview-worker",
	}
	if delivered {
		GlobalIncidentStore().ResolveOpenByKey(key, "Preview worker command delivery recovered.")
		return
	}
	GlobalIncidentStore().UpsertOpen(key, IncidentEvent{
		Timestamp:       time.Now().UnixMilli(),
		Severity:        IncidentSeverityError,
		Category:        "reload",
		Code:            ReasonReloadPreviewWorkerOffline,
		Source:          "devserver",
		Title:           "Preview worker is offline",
		UserMessage:     "The selected preview worker is not currently connected, so reload could not be delivered directly to that device.",
		SuggestedAction: "Reconnect the selected preview worker or switch preview target before reloading again.",
		DeviceID:        strings.TrimSpace(target.DeviceID),
		ProjectPath:     strings.TrimSpace(projectPath),
		Target:          "preview-worker",
		LogsAvailable:   true,
		LogRefs:         []string{"blackbox:device:" + strings.TrimSpace(target.DeviceID), "stream:dev-events"},
		Recoverable:     true,
		Metadata: map[string]interface{}{
			"targetDeviceName":  strings.TrimSpace(target.DeviceName),
			"targetDeviceClass": strings.TrimSpace(target.DeviceClass),
		},
	})
}

func (s *HTTPServer) guestAllowedDevWorkDir(guestUID, workDir string) bool {
	if guestUID == "" || s.guestConfigMgr == nil {
		return true
	}
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return s.guestConfigMgr.GuestCanAccessProject(guestUID, "")
	}
	if s.guestConfigMgr.GuestCanAccessProject(guestUID, filepath.Base(workDir)) {
		return true
	}
	if mp := findMobileProjectByPath(workDir); mp != nil && s.guestConfigMgr.GuestCanAccessProject(guestUID, mp.Name) {
		return true
	}
	return false
}

func (s *HTTPServer) guestResolveDevWorkDir(r *http.Request, projectName, fallbackWorkDir string) (string, error) {
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID == "" {
		return fallbackWorkDir, nil
	}
	projectName = strings.TrimSpace(projectName)
	if projectName != "" {
		if !s.guestConfigMgr.GuestCanAccessProject(guestUID, projectName) {
			return "", fmt.Errorf("project %q is not in the allowed project list %v", projectName, s.guestConfigMgr.AllowedProjects(guestUID))
		}
		return resolveGuestTaskProjectPath(projectName)
	}
	if allowed := s.guestConfigMgr.AllowedProjects(guestUID); len(allowed) > 0 {
		return "", fmt.Errorf("this guest is scoped to projects %v; projectName is required", allowed)
	}
	if strings.TrimSpace(fallbackWorkDir) != "" {
		return fallbackWorkDir, nil
	}
	if s.taskMgr != nil {
		return s.taskMgr.workDir, nil
	}
	return "", nil
}

func (s *HTTPServer) requireGuestAccessToActiveDevServer(w http.ResponseWriter, r *http.Request) bool {
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID == "" || s.devServerMgr == nil {
		return true
	}
	status := s.devServerMgr.Status()
	if status == nil || strings.TrimSpace(status.WorkDir) == "" {
		return true
	}
	if s.guestAllowedDevWorkDir(guestUID, status.WorkDir) {
		return true
	}
	jsonReply(w, http.StatusForbidden, map[string]string{"error": "guest cannot access the active dev server project"})
	return false
}

func (s *HTTPServer) isolatedGuestDevMutationBlocked(w http.ResponseWriter, r *http.Request, capability string) bool {
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID == "" || s.guestConfigMgr == nil {
		return false
	}
	cfg := s.guestConfigMgr.GetConfig(guestUID)
	if !guestRequireIsolation(cfg) {
		return false
	}
	jsonReply(w, http.StatusForbidden, map[string]string{
		"error": fmt.Sprintf("guest is configured to require isolation; %s must go through a brokered host action", capability),
	})
	return true
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
	if stored.ConsumerKey != "" {
		status.ConsumerKey = stored.ConsumerKey
	}
	if stored.ConsumerLabel != "" {
		status.ConsumerLabel = stored.ConsumerLabel
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

func trimBuildValue(value string) string {
	return strings.TrimSpace(value)
}

func nativeBuildConsumerKey(contract nativeBuildConsumerContract) string {
	parts := []string{
		"platform=" + trimBuildValue(contract.Platform),
		"version=" + trimBuildValue(contract.AppVersion),
		"build=" + trimBuildValue(contract.AppBuild),
		"sdk=" + trimBuildValue(contract.SDKVersion),
		"bc=" + strconv.Itoa(contract.HermesBCVersion),
	}
	return strings.Join(parts, "|")
}

func nativeBuildConsumerLabel(contract nativeBuildConsumerContract) string {
	label := trimBuildValue(contract.AppVersion)
	if label == "" {
		label = "unknown"
	}
	if build := trimBuildValue(contract.AppBuild); build != "" {
		label += " (" + build + ")"
	}
	if sdk := trimBuildValue(contract.SDKVersion); sdk != "" {
		label += ", SDK " + sdk
	}
	if contract.HermesBCVersion > 0 {
		label += fmt.Sprintf(", BC%d", contract.HermesBCVersion)
	}
	if platform := trimBuildValue(contract.Platform); platform != "" {
		label += " on " + platform
	}
	return label
}

func nativeBuildCacheDecisionForConsumer(status nativeBuildStatus, contract nativeBuildConsumerContract) nativeBuildCacheDecision {
	currentKey := nativeBuildConsumerKey(contract)
	currentLabel := nativeBuildConsumerLabel(contract)
	if strings.TrimSpace(currentKey) == "" || currentKey == "platform=|version=|build=|sdk=|bc=0" {
		return nativeBuildCacheDecision{
			Valid:   false,
			Label:   currentLabel,
			Message: "No mobile consumer contract was provided. Clearing stale build output before bundling.",
		}
	}
	if strings.TrimSpace(status.ConsumerKey) == "" {
		return nativeBuildCacheDecision{
			Valid:   false,
			Label:   currentLabel,
			Message: "No previous consumer contract was recorded. Clearing stale build output before bundling.",
		}
	}
	if status.ConsumerKey != currentKey {
		prev := strings.TrimSpace(status.ConsumerLabel)
		if prev == "" {
			prev = "another Yaver build"
		}
		return nativeBuildCacheDecision{
			Valid:   false,
			Label:   currentLabel,
			Message: fmt.Sprintf("Clearing bundle cache to match this Yaver build. Previous output was built for %s.", prev),
		}
	}
	return nativeBuildCacheDecision{
		Valid:   true,
		Label:   currentLabel,
		Message: fmt.Sprintf("Bundle cache matches this Yaver build (%s). Reusing compatible cache before rebuilding output.", currentLabel),
	}
}

func prepareNativeBuildOutput(buildDir, workDir string, contract nativeBuildConsumerContract) (nativeBuildCacheDecision, error) {
	status := readNativeBuildStatus(workDir)
	decision := nativeBuildCacheDecisionForConsumer(status, contract)
	if !decision.Valid {
		if err := os.RemoveAll(buildDir); err != nil {
			return decision, fmt.Errorf("clear build dir: %w", err)
		}
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return decision, err
	}
	// Even when the consumer matches, rebuild from clean output paths so
	// removed assets from a previous build cannot leak into the next zip.
	_ = os.Remove(filepath.Join(buildDir, "main.jsbundle"))
	_ = os.Remove(filepath.Join(buildDir, "status.json"))
	_ = os.RemoveAll(filepath.Join(buildDir, "assets"))
	return decision, nil
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

// findWorkspaceRoot walks up from workDir looking for a package.json
// that declares a non-empty `workspaces` array. Returns the absolute
// path of the workspace root (the directory containing that
// package.json), or "" if no enclosing workspace is found.
//
// Stops at the filesystem root or after 10 levels — far more than any
// realistic monorepo nesting and a guard against pathological symlink
// loops. Returns "" for workDir itself if it's already the workspace
// root *unless* its package.json sub-includes itself (npm allows this
// — the root can list "." in workspaces) — in that case we still
// return workDir so the install path collapses cleanly.
//
// `workspaces` may be a JSON array OR an object `{"packages": [...]}`
// (yarn classic). We accept both.
func findWorkspaceRoot(workDir string) string {
	dir, err := filepath.Abs(workDir)
	if err != nil {
		return ""
	}
	for i := 0; i < 10; i++ {
		pkgPath := filepath.Join(dir, "package.json")
		if data, err := os.ReadFile(pkgPath); err == nil {
			if hasWorkspaces(data) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

func hasWorkspaces(pkgJSON []byte) bool {
	var probe struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(pkgJSON, &probe); err != nil {
		return false
	}
	raw := bytes.TrimSpace(probe.Workspaces)
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	// Array form: workspaces: ["apps/*", "packages/*", "mobile"]
	if raw[0] == '[' {
		var arr []string
		if err := json.Unmarshal(raw, &arr); err != nil {
			return false
		}
		return len(arr) > 0
	}
	// Object form (yarn classic): workspaces: {"packages": [...]}
	if raw[0] == '{' {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return false
		}
		return len(obj.Packages) > 0
	}
	return false
}

func commandExists(name string) bool {
	_, err := lookPathWithRuntimes(name)
	return err == nil
}

func detectProjectPreparation(workDir string, manifest *projectPackageManifest) projectPreparationStatus {
	prep := projectPreparationStatus{
		PackageManager: detectProjectPackageManager(workDir, manifest),
	}
	if manifest != nil {
		prep.PackageManagerSpec = strings.TrimSpace(manifest.PackageManager)
	}

	nodeExists := commandExists("node")
	npmExists := commandExists("npm")
	npxExists := commandExists("npx")
	yarnExists := commandExists("yarn")
	pnpmExists := commandExists("pnpm")
	bunExists := commandExists("bun")
	bunxExists := commandExists("bunx")
	corepackExists := commandExists("corepack")

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
			if !canBootstrapPackageManager(prep.PackageManager, npmExists, corepackExists) {
				prep.MissingTools = append(prep.MissingTools, "yarn")
			}
		}
		if !npxExists {
			prep.MissingTools = append(prep.MissingTools, "npx")
		}
	case "pnpm":
		if !pnpmExists {
			if !canBootstrapPackageManager(prep.PackageManager, npmExists, corepackExists) {
				prep.MissingTools = append(prep.MissingTools, "pnpm")
			}
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
	// Monorepo handling. If the project lives inside a workspaces tree,
	// the workspace ROOT is the source of truth for "are deps installed"
	// — that's where workspace symlinks (e.g.
	// carrotbet/node_modules/@scope/foo → packages/foo) live, and
	// running `npm install` only inside the leaf package never creates
	// them. So if the root has no node_modules, install is needed
	// regardless of what the leaf looks like; we mark NeedsInstall
	// true and route the install to the workspace root in
	// installProjectDependenciesTo. carrotbet's "bundle 500" was
	// exactly this: mobile/node_modules existed (from a leaf-only
	// install), the root never got installed, Metro's parent walk
	// found no @backgammon/*, bundle errored — but the prep check
	// above said "looks fine" because it only checked the leaf.
	prep.WorkspaceRoot = findWorkspaceRoot(workDir)
	if prep.WorkspaceRoot != "" && prep.WorkspaceRoot != workDir {
		rootStat, err := os.Stat(filepath.Join(prep.WorkspaceRoot, "node_modules"))
		if err != nil || !rootStat.IsDir() {
			prep.DependenciesInstalled = false
		}
	}
	prep.NeedsDependencyInstall = !prep.DependenciesInstalled

	if prep.NeedsDependencyInstall {
		switch prep.PackageManager {
		case "npm":
			prep.CanAutoInstallDependencies = nodeExists && npmExists
		case "yarn":
			prep.CanAutoInstallDependencies = nodeExists && (yarnExists || canBootstrapPackageManager(prep.PackageManager, npmExists, corepackExists))
		case "pnpm":
			prep.CanAutoInstallDependencies = nodeExists && (pnpmExists || canBootstrapPackageManager(prep.PackageManager, npmExists, corepackExists))
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

func canBootstrapPackageManager(packageManager string, npmExists, corepackExists bool) bool {
	switch packageManager {
	case "yarn", "pnpm":
		return corepackExists || npmExists
	default:
		return false
	}
}

func defaultPackageManagerInstallSpec(packageManager string) string {
	switch packageManager {
	case "yarn":
		return "yarn@1.22.22"
	case "pnpm":
		return "pnpm@latest"
	default:
		return packageManager
	}
}

func ensureProjectPackageManager(prep projectPreparationStatus, extraOut io.Writer) error {
	if prep.PackageManager == "" || commandExists(prep.PackageManager) {
		return nil
	}

	run := func(cmd *exec.Cmd) error {
		cmd.Env = augmentEnv(nil)
		if extraOut != nil {
			cmd.Stdout = io.MultiWriter(os.Stdout, extraOut)
			cmd.Stderr = io.MultiWriter(os.Stderr, extraOut)
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
		return cmd.Run()
	}

	if commandExists("corepack") {
		if err := run(exec.Command("corepack", "enable")); err != nil {
			return fmt.Errorf("corepack enable failed: %w", err)
		}
		spec := prep.PackageManagerSpec
		if strings.TrimSpace(spec) == "" {
			spec = defaultPackageManagerInstallSpec(prep.PackageManager)
		}
		if err := run(exec.Command("corepack", "prepare", spec, "--activate")); err != nil {
			return fmt.Errorf("corepack prepare %s failed: %w", spec, err)
		}
	} else if commandExists("npm") {
		spec := defaultPackageManagerInstallSpec(prep.PackageManager)
		if err := run(exec.Command("npm", "install", "-g", spec)); err != nil {
			return fmt.Errorf("npm install -g %s failed: %w", spec, err)
		}
	} else {
		return fmt.Errorf("%s is missing and neither corepack nor npm is available to install it", prep.PackageManager)
	}

	if !commandExists(prep.PackageManager) {
		return fmt.Errorf("%s still not found after bootstrap", prep.PackageManager)
	}
	return nil
}

// canInstallMissingTool reports whether every entry in missing maps to
// an integration the phone can trigger via POST /install/<tool>. Used
// by the pre-flight to decide whether to advertise a one-tap install.
func canInstallMissingTool(missing []string) bool {
	if len(missing) == 0 {
		return false
	}
	for _, t := range missing {
		switch t {
		case "node", "npm", "npx":
			// All three come from the agent-managed Node runtime.
		default:
			return false
		}
	}
	return true
}

// installEndpointForTool returns the /install/<tool> path the phone
// should POST to in order to provision every missing tool. Picks
// `mobile` whenever any of node/npm/npx is missing — the agent-managed
// mobile install ships the Node runtime those tools come from and
// verifies the embedded hermesc reload path too.
func installEndpointForTool(missing []string) string {
	for _, t := range missing {
		if t == "node" || t == "npm" || t == "npx" {
			return "/install/mobile"
		}
	}
	return ""
}

func installProjectDependencies(workDir string, prep projectPreparationStatus) error {
	return installProjectDependenciesTo(workDir, prep, nil)
}

// installProjectDependenciesTo runs the project's package-manager
// install with output also tee'd to extraOut. Callers pass a
// devLogWriter whose onLogLine emits SSE "log" events so the mobile
// Hot Reload card sees every npm/yarn line live. Pass nil to fall
// back to stdout-only (matches the pre-streaming behaviour).
//
// Monorepo routing: when prep.WorkspaceRoot is set and differs from
// workDir, install runs at the workspace root. That's required for
// workspace-linked deps (e.g. mobile depends on @backgammon/game-engine
// at version "*", which only resolves once the root install
// materialises carrotbet/node_modules/@backgammon/game-engine as a
// symlink into packages/game-engine). Running install in the leaf
// only would silently leave those symlinks missing and the next
// bundle build would fail with "Unable to resolve module
// @backgammon/game-engine from <leaf>/X.tsx".
func installProjectDependenciesTo(workDir string, prep projectPreparationStatus, extraOut io.Writer) error {
	if err := ensureProjectPackageManager(prep, extraOut); err != nil {
		return err
	}

	installDir := workDir
	if prep.WorkspaceRoot != "" {
		installDir = prep.WorkspaceRoot
	}

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
	cmd.Dir = installDir
	// Pick up the agent-managed Node runtime (~/.yaver/runtimes/node/bin)
	// when system Node is missing, so a fresh Linux box can install
	// project deps after a phone-driven /install/mobile.
	cmd.Env = augmentEnv(nil)
	if extraOut != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, extraOut)
		cmd.Stderr = io.MultiWriter(os.Stderr, extraOut)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
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
			return errors.New(status.Error)
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
			return nil, errors.New(strings.TrimSpace(rec.Body.String()))
		}
		return nil, err
	}
	if rec.Code >= 400 {
		if msg, _ := result["error"].(string); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("native build failed with status %d", rec.Code)
	}
	return result, nil
}

// bundleCommand returns the framework-appropriate Metro/Expo invocation as
// a context-aware *exec.Cmd. Callers MUST supply a context with a timeout
// (e.g. context.WithTimeout) — Metro and Expo can hang silently on a broken
// project (missing node_modules, infinite resolver loop, stuck on stdin
// prompts), so a hard wall-clock cap is the only way to surface failure to
// the mobile app instead of leaving the HTTP request blocked indefinitely.
//
// resetCache controls whether `--reset-cache` is appended. Pass true on the
// first build of a session or after a project change; pass false on incremental
// rebuilds to save 30-60s and avoid blowing past Metro's heap on small boxes
// (the cache rebuild is the heaviest memory step in a Metro bundle).
func bundleCommand(ctx context.Context, packageManager, framework, platform, entryFile, bundlePath, assetsDir string, resetCache bool) *exec.Cmd {
	expoArgs := []string{"expo", "export:embed", "--platform", platform, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true"}
	rnArgs := []string{"react-native", "bundle", "--platform", platform, "--entry-file", entryFile, "--bundle-output", bundlePath, "--assets-dest", assetsDir, "--dev", "false", "--minify", "true"}
	if resetCache {
		expoArgs = append(expoArgs, "--reset-cache")
		rnArgs = append(rnArgs, "--reset-cache")
	}

	pmRun := func(pm string) (string, []string) {
		switch pm {
		case "yarn":
			return "yarn", nil
		case "pnpm":
			return "pnpm", []string{"exec"}
		case "bun":
			return "bunx", nil
		default:
			return "npx", nil
		}
	}

	switch framework {
	case "expo":
		bin, prefix := pmRun(packageManager)
		return exec.CommandContext(ctx, bin, append(prefix, expoArgs...)...)
	default:
		bin, prefix := pmRun(packageManager)
		return exec.CommandContext(ctx, bin, append(prefix, rnArgs...)...)
	}
}

// cacheLabel renders the warm/cold tag we paste into progress messages
// so users can see why a build is going to be slow this iteration.
func cacheLabel(resetCache bool) string {
	if resetCache {
		return " (cold)"
	}
	return " (warm cache)"
}

// shouldResetMetroCache returns true when no warm Metro cache exists for
// the project, or when the cache is older than 24h (Metro bugs accumulate
// in stale caches). On a clean checkout or the first build after a yarn
// install, the cache is empty and Metro will rebuild it implicitly even
// without the flag — passing --reset-cache then is wasted memory.
func shouldResetMetroCache(workDir string) bool {
	candidates := []string{
		filepath.Join(workDir, "node_modules", ".cache", "metro"),
		filepath.Join(workDir, "node_modules", ".cache", "babel-loader"),
	}
	freshest := time.Time{}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil {
			if info.ModTime().After(freshest) {
				freshest = info.ModTime()
			}
		}
	}
	if freshest.IsZero() {
		return true // no cache → no need to force-reset, but doesn't hurt
	}
	return time.Since(freshest) > 24*time.Hour
}

// Wall-clock caps for the build pipeline. Metro/Expo bundles for a clean
// project complete in <2 min on a workstation; 8 min covers a large monorepo
// with cold caches. Hermes compile is much faster — 3 min is generous.
// These caps exist to surface a hung subprocess as a real failure to the
// mobile app instead of leaving /dev/build-native blocked indefinitely.
const (
	bundleBuildTimeout   = 8 * time.Minute
	hermesCompileTimeout = 3 * time.Minute
)

// handleDevServerStatus returns the current dev server status.
// GET /dev/status
func (s *HTTPServer) handleDevServerStatus(w http.ResponseWriter, r *http.Request) {
	mgr := s.devMgrForRequest(r)
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	resolvedIOSMethod, resolvedIOSReason := resolveIOSInstallMethodWithReason(s.iosInstallMethod)
	target := mgr.PreferredTarget()
	status := mgr.Status()
	if status == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"running":           false,
			"serving":           false,
			"servingLabel":      "Not serving a preview",
			"stopActionLabel":   "Stop Serving",
			"targetDeviceId":    target.DeviceID,
			"targetDeviceName":  target.DeviceName,
			"targetDeviceClass": target.DeviceClass,
			"iosInstallMethod":  resolvedIOSMethod,
			"iosInstallReason":  resolvedIOSReason,
		})
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
		return
	}

	status.IOSInstallMethod = resolvedIOSMethod
	status.IOSInstallReason = resolvedIOSReason
	if status.Port > 0 {
		if ip := strings.TrimSpace(getLocalIP()); ip != "" && ip != "0.0.0.0" {
			status.DirectURL = fmt.Sprintf("http://%s:%d", ip, status.Port)
		}
	}
	jsonReply(w, http.StatusOK, status)
}

func (s *HTTPServer) stopServingPreviewResult() map[string]interface{} {
	// An explicit user Stop should always (a) cancel in-flight Hermes
	// builds, (b) tear down the dev server, (c) clear outstanding
	// build/reload incidents so the UI doesn't keep showing a stale
	// "current blocker" pill, and (d) verify the server is really
	// down before returning.
	cancelledBuilds := cancelAllActiveBuilds()

	if s.devServerMgr == nil {
		return map[string]interface{}{
			"ok":                false,
			"stoppedServing":    false,
			"previouslyServing": false,
			"buildsCancelled":   cancelledBuilds,
			"message":           "Dev server manager not available.",
		}
	}

	status := s.devServerMgr.Status()
	if status == nil || !status.Running {
		// Even if no dev server is running, the user clicked Stop —
		// resolve any open build incidents so the UI returns to a
		// clean idle state.
		s.resolveOpenDevIncidents("", "")
		return map[string]interface{}{
			"ok":                true,
			"stoppedServing":    false,
			"previouslyServing": false,
			"buildsCancelled":   cancelledBuilds,
			"message":           "Nothing is being served right now.",
			"verified":          true,
		}
	}

	result := map[string]interface{}{
		"ok":                true,
		"stoppedServing":    true,
		"previouslyServing": true,
		"framework":         status.Framework,
		"kind":              status.Kind,
		"workDir":           status.WorkDir,
		"buildsCancelled":   cancelledBuilds,
		"message":           "Stopped serving the active preview.",
	}
	stoppedWorkDir := status.WorkDir
	if err := s.devServerMgr.Stop(); err != nil {
		result["ok"] = false
		result["stoppedServing"] = false
		result["message"] = err.Error()
		return result
	}

	// Verify the manager really has no active server. Stop() is
	// SIGINT → 5s wait → SIGKILL inside baseDevServer.Stop, so this
	// loop is effectively waiting for the post-kill state flip; in
	// practice it returns immediately. Bounded so a misbehaving
	// subprocess can't pin the request open forever.
	deadline := time.Now().Add(7 * time.Second)
	verified := false
	for time.Now().Before(deadline) {
		st := s.devServerMgr.Status()
		if st == nil || !st.Running {
			verified = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	result["verified"] = verified
	if !verified {
		result["ok"] = false
		result["message"] = "Dev server failed to stop within 7s — subprocess may still be running. The agent issued SIGINT and SIGKILL."
	}

	// Clear any "Bundle validation failed" / "Bundle rebuild failed"
	// incidents so the mobile + web UIs don't keep showing a stale red
	// pill after the user has Stop'd. Pass empty (workDir, target) for
	// the wildcard sweep — user-initiated Stop is "clean slate" intent,
	// not "clear only this project". stoppedWorkDir is preserved here
	// in case a future call site wants to do scoped resolution instead.
	_ = stoppedWorkDir
	s.resolveOpenDevIncidents("", "")

	return result
}

// resolveOpenDevIncidents clears every open category=build /
// category=reload incident with a recognised dev-server reason code.
// If projectPath/target is non-empty the resolve is project-scoped via
// ResolveOpenByKey (used by automatic post-build success). If both are
// empty (the case for a user-initiated /dev/stop) we walk the full open
// list and resolve every matching entry regardless of which project /
// target it was opened against — explicit Stop = clean slate.
func (s *HTTPServer) resolveOpenDevIncidents(projectPath, target string) {
	store := GlobalIncidentStore()
	if store == nil {
		return
	}
	codes := map[string]string{
		ReasonBuildNativeFailed:           "build",
		ReasonBuildHermesFailed:           "build",
		ReasonReloadDevServerUnavailable:  "reload",
		ReasonReloadNativeRebuildRequired: "reload",
		ReasonReloadPreviewWorkerOffline:  "reload",
	}

	scoped := strings.TrimSpace(projectPath) != "" || strings.TrimSpace(target) != ""
	if scoped {
		for code, category := range codes {
			store.ResolveOpenByKey(IncidentKey{
				Category:    category,
				Code:        code,
				ProjectPath: strings.TrimSpace(projectPath),
				Target:      strings.TrimSpace(target),
			}, "Cleared by user Stop.")
		}
		return
	}

	for _, ev := range store.List(IncidentFilter{IncludeResolved: false}) {
		if _, ok := codes[ev.Code]; !ok {
			continue
		}
		if ev.Resolved {
			continue
		}
		store.Resolve(ev.ID, "Cleared by user Stop.")
	}
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
		if !s.requireGuestAccessToActiveDevServer(w, r) {
			return
		}
		target := s.devServerMgr.PreferredTarget()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"targetDeviceId":    target.DeviceID,
			"targetDeviceName":  target.DeviceName,
			"targetDeviceClass": target.DeviceClass,
		})
	case http.MethodPost:
		if s.isolatedGuestDevMutationBlocked(w, r, "dev target changes") {
			return
		}
		if !s.requireGuestAccessToActiveDevServer(w, r) {
			return
		}
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
	mgr := s.devMgrForRequest(r)
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.isolatedGuestDevMutationBlocked(w, r, "dev server start") {
		return
	}

	var req struct {
		Framework         string `json:"framework"` // "expo", "flutter", "vite", "nextjs", "" (auto-detect)
		WorkDir           string `json:"workDir"`
		ProjectName       string `json:"projectName,omitempty"`
		App               string `json:"app,omitempty"`     // workspace app name (monorepo path)
		Surface           string `json:"surface,omitempty"` // "web-reload" or "hot-reload" — kind gate
		Root              string `json:"root,omitempty"`    // workspace root override (for monorepo lookup)
		Platform          string `json:"platform"`          // "ios", "android", "web"
		Port              int    `json:"port"`
		Rebuild           bool   `json:"rebuild"` // force rebuild (clear build marker)
		TargetDeviceID    string `json:"targetDeviceId"`
		TargetDeviceName  string `json:"targetDeviceName"`
		TargetDeviceClass string `json:"targetDeviceClass"`
		// Caller identity. "web-ui" lets the agent pivot a mobile
		// project to the static-bundle path instead of returning the
		// legacy "mobile-only" 400 (which the iframe can't recover
		// from). "mobile" pins to the Hermes/native path. Empty =
		// legacy CLI behaviour.
		Caller string `json:"caller,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Monorepo-friendly: resolve `app` to workDir + framework via the
	// workspace manifest. Explicit workDir always wins. Surface gating
	// prevents the Web Reload tab from accidentally starting Metro.
	if strings.TrimSpace(req.App) != "" {
		root := strings.TrimSpace(req.Root)
		if root == "" {
			root = resolveWorkspaceRoot(r, s)
		}
		m, _, err := loadWorkspaceManifestForHTTP(root)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("workspace manifest: %s", err.Error()),
			})
			return
		}
		var matched *WorkspaceApp
		for i := range m.Apps {
			if m.Apps[i].Name == req.App {
				matched = &m.Apps[i]
				break
			}
		}
		if matched == nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("app %q not in workspace manifest", req.App),
			})
			return
		}
		kind := StackToDevServerKind(matched.Stack)
		if kind == "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("app %q (stack=%q) has no dev server", matched.Name, matched.Stack),
			})
			return
		}
		if req.Surface == "web-reload" && kind == DevServerKindMobile {
			if strings.TrimSpace(req.Caller) == "web-ui" {
				// Caller=web-ui knows how to render a static bundle — tell
				// it to do that instead of a hard 400. UI polls
				// /dev/web-bundle/info to know whether to kick off a build.
				info := s.devServerMgr.GetWebBundleInfo()
				jsonReply(w, http.StatusOK, map[string]interface{}{
					"ok":          true,
					"mode":        "static-bundle",
					"bundleUrl":   signedWebBundleURL(w),
					"bundleReady": info.BuildDir != "" && info.FileCount > 0,
					"bundleHint":  "POST /dev/build-native target=web-js-bundle",
					"appName":     matched.Name,
					"kind":        string(kind),
				})
				return
			}
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("app %q is mobile-only; not available in Web Reload", matched.Name),
			})
			return
		}
		if req.Surface == "hot-reload" && kind == DevServerKindWeb {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("app %q is web-only; not available in Hot Reload", matched.Name),
			})
			return
		}
		if req.WorkDir == "" {
			req.WorkDir = appAbsPath(root, m, matched)
		}
		if req.Framework == "" {
			req.Framework = StackToFramework(matched.Stack)
		}
	}

	resolvedWorkDir, err := s.guestResolveDevWorkDir(r, req.ProjectName, req.WorkDir)
	if err != nil {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	req.WorkDir = resolvedWorkDir

	// Surface gate for the no-workspace-manifest path. The branch above
	// only fires when the caller named an `App` from yaver.workspace.yaml.
	// Without a manifest the dashboard's project-fallback picker can ask
	// us to start any project on any surface; we re-check the framework
	// here so a Web Reload click can't accidentally launch Metro for an
	// Expo / RN project (which the iframe can't render anyway).
	if strings.TrimSpace(req.App) == "" && strings.TrimSpace(req.Surface) != "" {
		var kind DevServerKind
		if framework := strings.TrimSpace(req.Framework); framework != "" {
			kind = FrameworkToDevServerKind(framework)
		}
		if kind == "" && req.WorkDir != "" {
			if ds := detectDevServer(req.WorkDir); ds != nil {
				kind = ds.Kind()
			}
		}
		switch req.Surface {
		case "web-reload":
			if kind == DevServerKindMobile {
				if strings.TrimSpace(req.Caller) == "web-ui" {
					// Web UI knows how to render a static bundle — tell it
					// to use the bundle path instead of returning 400. UI
					// polls /dev/web-bundle/info, kicks off a build via
					// POST /dev/build-native target=web-js-bundle when
					// needed, then loads /dev/web-bundle/ in the iframe.
					info := s.devServerMgr.GetWebBundleInfo()
					jsonReply(w, http.StatusOK, map[string]interface{}{
						"ok":          true,
						"mode":        "static-bundle",
						"bundleUrl":   signedWebBundleURL(w),
						"bundleReady": info.BuildDir != "" && info.FileCount > 0,
						"bundleHint":  "POST /dev/build-native target=web-js-bundle",
						"projectName": req.ProjectName,
						"workDir":     req.WorkDir,
						"kind":        string(kind),
					})
					return
				}
				jsonReply(w, http.StatusBadRequest, map[string]string{
					"error": "Project is mobile-only (Metro/RN); use Hot Reload + Yaver app instead of Web Reload.",
					"kind":  string(kind),
				})
				return
			}
		case "hot-reload":
			if kind == DevServerKindWeb {
				jsonReply(w, http.StatusBadRequest, map[string]string{
					"error": "Project is web-only; use Web Reload instead of Hot Reload.",
					"kind":  string(kind),
				})
				return
			}
		}
	}

	if req.TargetDeviceID != "" || req.TargetDeviceName != "" || req.TargetDeviceClass != "" {
		mgr.SetPreferredTarget(DevServerTarget{
			DeviceID:    req.TargetDeviceID,
			DeviceName:  req.TargetDeviceName,
			DeviceClass: req.TargetDeviceClass,
		})
	}

	// In multi-user mode the user has been allocated a dedicated
	// (Metro, ExpoWeb) port pair; apply it when the caller did not
	// pin an explicit port. Owner / single-user keeps the canonical
	// 8081 default.
	if req.Port == 0 {
		if pair := s.devPortsForRequest(r); pair.MetroPort != 0 && pair.MetroPort != 8081 {
			req.Port = pair.MetroPort
		}
	}

	// Clear build marker if rebuild requested
	if req.Rebuild && req.WorkDir != "" {
		projectHash := strings.ReplaceAll(filepath.Base(req.WorkDir), " ", "_")
		marker := filepath.Join(yaverBuildsDir(), projectHash+".built")
		os.Remove(marker)
		log.Printf("[dev] Cleared build marker for %s (rebuild requested)", projectHash)
	}

	// Pre-flight: if the project directory is set and the box is
	// missing required runtimes (e.g. Node), refuse with a 412 +
	// structured payload so the phone can offer a one-tap install
	// instead of surfacing a raw "executable not found" error after
	// a long timeout. The mobile client knows to render this shape.
	if req.WorkDir != "" {
		if manifest, err := readProjectPackageManifest(req.WorkDir); err == nil {
			prep := detectProjectPreparation(req.WorkDir, manifest)
			// Relax the pre-flight when only Node itself is missing —
			// the dev-server Start goroutine now auto-installs Node via
			// ensureNodeDepsStreamed (Task 4) and streams progress to
			// /dev/events SSE. We only 412-reject when some *other*
			// toolchain is missing (bun / pnpm / yarn / hermesc, etc.)
			// that we can't fix ourselves.
			if len(prep.MissingTools) > 0 && !isOnlyNodeMissing(prep.MissingTools) {
				installable := canInstallMissingTool(prep.MissingTools)
				jsonReply(w, http.StatusPreconditionFailed, map[string]interface{}{
					"error":           fmt.Sprintf("Cannot start dev server: %s missing on this machine.", strings.Join(prep.MissingTools, ", ")),
					"missingTools":    prep.MissingTools,
					"packageManager":  prep.PackageManager,
					"hermesCompiler":  prep.HermesCompiler,
					"installEndpoint": installEndpointForTool(prep.MissingTools),
					"installable":     installable,
					"helpHint":        "POST /install/node from the phone (sudo-free, ~/.yaver/runtimes/node) and retry.",
				})
				return
			}
		}
	}
	if strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")) == "" {
		s.maybePullBeforeHotReloadBuild(req.WorkDir)
	}

	if err := mgr.Start(req.Framework, req.WorkDir, req.Platform, req.Port, DevServerTarget{
		DeviceID:    req.TargetDeviceID,
		DeviceName:  req.TargetDeviceName,
		DeviceClass: req.TargetDeviceClass,
	}); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Web Reload tab kicked us — auto-spawn the Expo Web sibling so the
	// browser iframe has an HTML target, while the primary Metro process
	// keeps serving Hermes bundles to the mobile app on the canonical
	// port. Without this, a Web Reload click would either lock Metro
	// into --web mode (breaking Hot Reload for any other user) or leave
	// the iframe pointing at a Metro endpoint that returns nothing
	// renderable. Best-effort: errors here become structured logs but
	// don't fail the /dev/start response — the dashboard polls
	// /dev/status and surfaces missing webPort with a "Start Web
	// Preview" CTA already.
	if strings.EqualFold(strings.TrimSpace(req.Surface), "web-reload") {
		framework := strings.TrimSpace(req.Framework)
		if framework == "" && req.WorkDir != "" {
			if ds := detectDevServer(req.WorkDir); ds != nil {
				framework = ds.Name()
			}
		}
		if strings.EqualFold(framework, "expo") || strings.EqualFold(framework, "react-native") {
			go func() {
				// Wait briefly for Metro to bind its port before spawning
				// the sibling — concurrent expo starts on the same project
				// occasionally race on watchman manifest writes.
				time.Sleep(2 * time.Second)
				if _, err := mgr.StartWebPreview(); err != nil {
					log.Printf("[dev] auto web-preview start (surface=web-reload) failed: %v", err)
				}
			}()
		}
	}

	// Return immediately — server starts in background, mobile polls /dev/status
	status := mgr.Status()
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
	mgr := s.devMgrForRequest(r)
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	_ = mgr // routed below via stopServingPreviewResult; kept for parity
	if s.isolatedGuestDevMutationBlocked(w, r, "dev server stop") {
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
		return
	}

	result := s.stopServingPreviewResult()
	if ok, _ := result["ok"].(bool); !ok {
		jsonReply(w, http.StatusBadRequest, result)
		return
	}
	jsonReply(w, http.StatusOK, result)
}

// handleDevServerReload triggers a hot reload on the active dev server.
// POST /dev/reload
//
// Also computes a native-change delta against the fingerprint captured at
// dev-server start. If native-only files (app.json splash, Podfile,
// AndroidManifest.xml, …) changed, the Hermes bundle push will silently lie
// about the outcome — we return nativeChangesDetected=true + the list of
// files so the client can surface "rebuild required" instead of pretending
// the reload worked. We still do the JS reload; the mobile UX decides what
// to show the user.
func (s *HTTPServer) handleDevServerReload(w http.ResponseWriter, r *http.Request) {
	target := DevServerTarget{}
	if s.devServerMgr != nil {
		target = s.devServerMgr.PreferredTarget()
	}
	projectPath := ""
	if s.devServerMgr != nil {
		if st := s.devServerMgr.Status(); st != nil {
			projectPath = strings.TrimSpace(st.WorkDir)
		}
	}
	reloadOp := s.upsertDevOperation("reload", "running", "request", "Preparing reload…", projectPath, target.DeviceID, 0.05, map[string]interface{}{
		"mode": "dev",
	})
	if s.devServerMgr == nil {
		incident := s.appendDevIncident("reload", ReasonReloadDevServerUnavailable, "Dev server unavailable", "The agent cannot reload because no active dev server is available.", "dev server manager not available", "Start or reconnect the dev server before reloading.", projectPath, target.DeviceID, "dev-server", IncidentSeverityError, true, false, nil, nil, reloadOp.ID)
		s.upsertDevOperation("reload", "failed", "error", incident.UserMessage, projectPath, target.DeviceID, 1, nil, incident.ID)
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
		return
	}

	// Compute the native change delta BEFORE triggering the reload so we
	// capture exactly the state the user is asking us to reload against.
	var nativeChanges []NativeFingerprintChange
	var changeClass string
	if st := s.devServerMgr.Status(); st != nil && st.WorkDir != "" {
		if baseline, ok := GetNativeBaseline(st.WorkDir); ok {
			current := ComputeNativeFingerprint(st.WorkDir)
			delta := DiffNativeFingerprints(baseline, current)
			nativeChanges = delta.Changed
			if len(nativeChanges) > 0 {
				changeClass = "native_rebuild_required"
			} else {
				changeClass = "js_only"
			}
		}
	}

	// Metro's HTTP /reload endpoint is flaky in --host lan --dev-client mode
	// (connection-refuses on 127.0.0.1). That's fine — the actual mobile
	// reload path is the blackbox BroadcastCommand below, which doesn't care
	// whether Metro's HTTP layer responded. Log + keep going.
	if err := s.devServerMgr.Reload(); err != nil {
		log.Printf("[dev] dev server Reload() soft-failed (continuing to broadcast): %v", err)
	}

	// Emit control signal for hot reload
	if s.taskMgr != nil {
		s.taskMgr.BroadcastControlSignal(`{"yaver_control":"hot_reload"}`)
	}

	// Push reload command to all connected SDK devices (third-party apps with Feedback SDK).
	// If we detected native changes, send the device a distinct command so the super-host
	// can show "rebuild required" instead of pretending the JS reload fixed everything.
	if s.blackboxMgr != nil {
		if len(nativeChanges) > 0 {
			paths := make([]string, 0, len(nativeChanges))
			reasons := make([]string, 0, len(nativeChanges))
			for _, c := range nativeChanges {
				paths = append(paths, c.Path)
				reasons = append(reasons, c.Reason)
			}
			cmd := BlackBoxCommand{
				Command: "native_rebuild_required",
				Data: map[string]interface{}{
					"changedPaths":   paths,
					"changedReasons": reasons,
				},
			}
			if sent := s.sendCommandToPreviewTarget(cmd); sent {
				s.syncPreviewWorkerIncident(projectPath, target, true)
				log.Printf("[dev] reload: native change detected (%d files) — sent native_rebuild_required to preview worker", len(nativeChanges))
			} else {
				s.syncPreviewWorkerIncident(projectPath, target, strings.TrimSpace(target.DeviceID) == "")
				s.blackboxMgr.BroadcastCommand(cmd)
				log.Printf("[dev] reload: native change detected (%d files) — broadcast native_rebuild_required", len(nativeChanges))
			}
		} else if sent := s.sendPreviewWorkerReloadCommand(); sent {
			s.syncPreviewWorkerIncident(projectPath, target, true)
			log.Printf("[dev] Sent targeted preview reload command to preview worker")
		} else {
			s.syncPreviewWorkerIncident(projectPath, target, strings.TrimSpace(target.DeviceID) == "")
			s.blackboxMgr.BroadcastCommand(BlackBoxCommand{Command: "reload"})
			log.Printf("[dev] Broadcast reload command to connected SDK devices")
		}
	}

	incidentIDs := []string{}
	if len(nativeChanges) > 0 {
		paths := make([]string, 0, len(nativeChanges))
		reasons := make([]string, 0, len(nativeChanges))
		for _, change := range nativeChanges {
			paths = append(paths, change.Path)
			reasons = append(reasons, change.Reason)
		}
		incident := GlobalIncidentStore().UpsertOpen(IncidentKey{
			Category:    "reload",
			Code:        ReasonReloadNativeRebuildRequired,
			DeviceID:    strings.TrimSpace(target.DeviceID),
			ProjectPath: strings.TrimSpace(projectPath),
			Target:      "mobile-hermes",
		}, IncidentEvent{
			Timestamp:       time.Now().UnixMilli(),
			Severity:        IncidentSeverityWarn,
			Category:        "reload",
			Code:            ReasonReloadNativeRebuildRequired,
			Source:          "devserver",
			Title:           "Native rebuild required",
			UserMessage:     "Hot reload was accepted, but native files changed and a rebuild is required before the app can fully match the filesystem state.",
			SuggestedAction: "Run a native rebuild or use bundle reload so Hermes picks up native-side changes.",
			OperationID:     reloadOp.ID,
			DeviceID:        strings.TrimSpace(target.DeviceID),
			ProjectPath:     strings.TrimSpace(projectPath),
			Target:          "mobile-hermes",
			LogsAvailable:   true,
			LogRefs:         []string{"stream:dev-events"},
			Recoverable:     true,
			Metadata: map[string]interface{}{
				"changedPaths":   paths,
				"changedReasons": reasons,
				"changeClass":    changeClass,
			},
		})
		incidentIDs = append(incidentIDs, incident.ID)
	} else {
		GlobalIncidentStore().ResolveOpenByKey(IncidentKey{
			Category:    "reload",
			Code:        ReasonReloadNativeRebuildRequired,
			DeviceID:    strings.TrimSpace(target.DeviceID),
			ProjectPath: strings.TrimSpace(projectPath),
			Target:      "mobile-hermes",
		}, "Reload no longer requires a native rebuild.")
	}
	s.upsertDevOperation("reload", "completed", changeClassOrDefault(changeClass), "Reload command dispatched.", projectPath, target.DeviceID, 1, map[string]interface{}{
		"mode":                  "dev",
		"nativeChangesDetected": len(nativeChanges) > 0,
		"changeClass":           changeClass,
	}, incidentIDs...)

	resp := map[string]interface{}{
		"ok":                    true,
		"nativeChangesDetected": len(nativeChanges) > 0,
		"nativeChanges":         nativeChanges,
		"changeClass":           changeClass, // "js_only" | "native_rebuild_required" | "" (no baseline)
	}
	jsonReply(w, http.StatusOK, resp)
}

// handleNativeFingerprintGet reports the current native fingerprint + delta
// vs. baseline without triggering any reload. Mobile / CLI can poll this to
// show a "rebuild required" indicator passively.
// GET /dev/native-fingerprint
func (s *HTTPServer) handleNativeFingerprintGet(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
		return
	}
	st := s.devServerMgr.Status()
	if st == nil || st.WorkDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no active dev server"})
		return
	}
	current := ComputeNativeFingerprint(st.WorkDir)
	resp := map[string]interface{}{
		"workDir":       st.WorkDir,
		"current":       current,
		"hasBaseline":   false,
		"changedFiles":  []NativeFingerprintChange{},
		"nativeChanges": false,
	}
	if baseline, ok := GetNativeBaseline(st.WorkDir); ok {
		delta := DiffNativeFingerprints(baseline, current)
		resp["hasBaseline"] = true
		resp["baseline"] = baseline
		resp["changedFiles"] = delta.Changed
		resp["nativeChanges"] = len(delta.Changed) > 0
	}
	jsonReply(w, http.StatusOK, resp)
}

// handleNativeFingerprintRefresh resets the baseline to the current state.
// Intended to be called right after a successful native rebuild, so the next
// /dev/reload no longer says "rebuild required".
// POST /dev/native-fingerprint/refresh
func (s *HTTPServer) handleNativeFingerprintRefresh(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
		return
	}
	st := s.devServerMgr.Status()
	if st == nil || st.WorkDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no active dev server"})
		return
	}
	fp := ComputeNativeFingerprint(st.WorkDir)
	SetNativeBaseline(st.WorkDir, fp)
	log.Printf("[dev] native fingerprint baseline refreshed for %s", st.WorkDir)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"baseline": fp,
	})
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
		// Optional project hints for the bundle path. Without these,
		// the agent has to fall back to the currently-active dev
		// server, which is empty for any TestFlight-installed app.
		// projectName matches MobileProject.Name from the mobile
		// scan; projectPath is an explicit absolute filesystem path;
		// bundleId is the iOS / Android application ID, intended
		// for a future scan-side index. Forwarded verbatim into the
		// re-issued /dev/build-native body below.
		ProjectName string `json:"projectName"`
		ProjectPath string `json:"projectPath"`
		BundleID    string `json:"bundleId"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Mode == "" {
		req.Mode = "dev"
	}
	if req.Mode != "dev" && s.isolatedGuestDevMutationBlocked(w, r, "native reload/bundle build") {
		return
	}

	if s.blackboxMgr == nil {
		target := DevServerTarget{}
		if s.devServerMgr != nil {
			target = s.devServerMgr.PreferredTarget()
		}
		projectPath := strings.TrimSpace(req.ProjectPath)
		reloadOp := s.upsertDevOperation("reload_app", "failed", "error", "No SDK devices are connected to receive reload commands.", projectPath, target.DeviceID, 1, map[string]interface{}{"mode": req.Mode})
		incident := s.appendDevIncident("reload", ReasonReloadPreviewWorkerOffline, "No connected SDK devices", "The agent has no connected SDK device to receive the reload command.", "blackbox manager unavailable", "Connect the app or preview worker before trying remote reload.", projectPath, target.DeviceID, "preview-worker", IncidentSeverityError, true, false, nil, nil, reloadOp.ID)
		s.upsertDevOperation("reload_app", "failed", "error", incident.UserMessage, projectPath, target.DeviceID, 1, map[string]interface{}{"mode": req.Mode}, incident.ID)
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no SDK devices connected"})
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
		return
	}

	switch req.Mode {
	case "dev":
		target := s.devServerMgr.PreferredTarget()
		projectPath := strings.TrimSpace(req.ProjectPath)
		if projectPath == "" {
			if st := s.devServerMgr.Status(); st != nil {
				projectPath = strings.TrimSpace(st.WorkDir)
			}
		}
		s.upsertDevOperation("reload_app", "running", "dispatch", "Dispatching reload to connected app…", projectPath, target.DeviceID, 0.4, map[string]interface{}{"mode": "dev"})
		// Hot reload: tell SDK devices to reload from dev server
		if s.devServerMgr != nil {
			s.devServerMgr.Reload()
		}
		if sent := s.sendPreviewWorkerReloadCommand(); sent {
			s.syncPreviewWorkerIncident(projectPath, target, true)
			log.Printf("[dev] Reload-app (dev mode): sent targeted preview reload to preview worker")
		} else {
			s.syncPreviewWorkerIncident(projectPath, target, strings.TrimSpace(target.DeviceID) == "")
			s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
				Command: "reload",
			})
			log.Printf("[dev] Reload-app (dev mode): broadcast reload to SDK devices")
		}
		s.upsertDevOperation("reload_app", "completed", "done", "Reload command sent to app.", projectPath, target.DeviceID, 1, map[string]interface{}{"mode": "dev"})
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "mode": "dev"})

	case "bundle":
		target := s.devServerMgr.PreferredTarget()
		projectPath := strings.TrimSpace(req.ProjectPath)
		if projectPath == "" {
			if st := s.devServerMgr.Status(); st != nil {
				projectPath = strings.TrimSpace(st.WorkDir)
			}
		}
		s.emitBuildProgress("Preparing Yaver reload: stop guest, rebuild Hermes, restart…", "prepare")
		reloadOp := s.upsertDevOperation("reload_app", "running", "prepare", "Preparing Yaver reload: stop guest, rebuild Hermes bundle, then restart in Yaver.", projectPath, target.DeviceID, phaseProgress("prepare"), map[string]interface{}{"mode": "bundle"})
		// Native bundle: rebuild and tell SDK devices to fetch new bundle.
		// Capture handleBuildNativeBundle's response so we can detect a
		// failed build (no active dev server, dependency install failed,
		// hermes crashed, …) and skip the reload_bundle broadcast — the
		// SDK would otherwise fetch /dev/native-bundle, get nothing or a
		// stale file, and SIGABRT inside Hermes when the native bundle
		// loader tries to swap a corrupt bridge. That's the SFMG crash
		// users saw before this fix.
		//
		// r.Body was already drained by the json decode above, so we
		// can't just pass `r` through. Build a fresh request body with
		// the project hints so handleBuildNativeBundle sees them.
		buildBody, _ := json.Marshal(map[string]string{
			"platform":    "ios",
			"projectName": req.ProjectName,
			"projectPath": req.ProjectPath,
			"bundleId":    req.BundleID,
		})
		buildReq, _ := http.NewRequest("POST", "/dev/build-native", bytes.NewReader(buildBody))
		buildReq.Header.Set("Content-Type", "application/json")
		rec := newCapturingResponseWriter()
		s.handleBuildNativeBundle(rec, buildReq)
		// Forward the build's response (success OR failure) to the
		// caller so the SDK sees the real status + body.
		for k, vv := range rec.Header() {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Status())
		w.Write(rec.Body())
		if rec.Status() >= 400 {
			// Build never produced a fresh bundle. DON'T broadcast
			// reload_bundle — the SDK would fetch /dev/native-bundle,
			// get a stale or missing file, and SIGABRT inside the
			// Hermes bridge swap. Emit a status command so the modal
			// can flip from "Compiling…" → the real failure.
			msg := extractErrorMessage(rec.Body())
			if msg == "" {
				msg = fmt.Sprintf("build failed (status %d)", rec.Status())
			}
			s.emitBuildProgress(msg, "error")
			incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "Bundle rebuild failed", "The agent could not build a fresh bundle for reload.", msg, "Inspect the build logs and fix the host-side build issue before retrying.", projectPath, target.DeviceID, "mobile-hermes", IncidentSeverityError, true, true, []string{"stream:dev-events"}, map[string]interface{}{"mode": "bundle", "status": rec.Status()}, reloadOp.ID)
			s.upsertDevOperation("reload_app", "failed", "error", incident.UserMessage, projectPath, target.DeviceID, 1, map[string]interface{}{"mode": "bundle"}, incident.ID)
			log.Printf("[dev] Reload-app (bundle mode): build failed (%d), skipping reload_bundle broadcast", rec.Status())
			return
		}
		// Build succeeded. Tell SDKs to fetch the fresh bundle.
		s.emitBuildProgress("Sending fresh bundle + restart command to phone…", "push")
		s.upsertDevOperation("reload_app", "running", "restart", "Bundle ready. Sending restart command so the phone can swap the guest bridge in Yaver.", projectPath, target.DeviceID, phaseProgress("restart"), map[string]interface{}{"mode": "bundle"})
		bundleURL, assetsURL := signedNativeBundleURLs(s)
		cmd := BlackBoxCommand{
			Command: "reload_bundle",
			Data: map[string]interface{}{
				"bundleUrl": bundleURL,
				"assetsUrl": assetsURL,
			},
		}
		if sent := s.sendCommandToPreviewTarget(cmd); sent {
			s.syncPreviewWorkerIncident(projectPath, target, true)
			log.Printf("[dev] Reload-app (bundle mode): sent targeted reload_bundle to preview worker")
		} else {
			s.syncPreviewWorkerIncident(projectPath, target, strings.TrimSpace(target.DeviceID) == "")
			s.blackboxMgr.BroadcastCommand(cmd)
			log.Printf("[dev] Reload-app (bundle mode): broadcast reload_bundle to SDK devices")
		}
		s.upsertDevOperation("reload_app", "completed", "push", "Fresh bundle sent. The phone is restarting the guest inside Yaver.", projectPath, target.DeviceID, 1, map[string]interface{}{"mode": "bundle"})
		// Explicit terminal event on /dev/events so SSE consumers
		// (feedback-overlay reload chip) can clear their progress
		// spinner without waiting on a 90s safety timeout. Without
		// this, the only "we're done" signal lived on
		// /blackbox/command-stream (phase=push with progress=0.98)
		// which the overlay isn't subscribed to — the build had
		// finished and the bundle had broadcast successfully but
		// the spinner kept going until the hard timeout fired.
		if s.devServerMgr != nil {
			s.devServerMgr.EmitLog("Reload complete — bundle broadcast")
			s.devServerMgr.EmitReloadDone(projectPath, target.DeviceID, bundleURL)
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
	bundleURL, assetsURL := signedNativeBundleURLs(s)
	return s.sendCommandToPreviewTarget(BlackBoxCommand{
		Command: "reload_bundle",
		Data: map[string]interface{}{
			"bundleUrl":  bundleURL,
			"assetsUrl":  assetsURL,
			"moduleName": "main",
		},
	})
}

// handleDevServerEvents streams dev server events via SSE.
// GET /dev/events
func (s *HTTPServer) handleDevServerEvents(w http.ResponseWriter, r *http.Request) {
	mgr := s.devMgrForRequest(r)
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if !s.requireGuestAccessToActiveDevServer(w, r) {
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
	// X-Accel-Buffering: no tells nginx + Cloudflare to NOT buffer
	// the response. Without this Cloudflare's edge holds the bytes
	// until either a buffer fills or 30s passes — the symptom users
	// hit is "agent: connected, sse: error, err: HTTP 502" because
	// CF kills upstream as stalled before the first event arrives.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Flush an initial keepalive immediately — Cloudflare's TTFB
	// timeout is shorter than our 15-s ticker, so without an initial
	// byte the edge declares the upstream stalled before the first
	// real event ever arrives. SSE comments are ignored by the
	// browser but they get the response chunked-encoding rolling.
	fmt.Fprintf(w, ":hello %d\n\n", time.Now().Unix())
	flusher.Flush()

	// `?fresh=1` skips the history replay so callers that only care
	// about events emitted AFTER they subscribed (mobile feedback
	// overlay reload chip) don't have to filter the replayed prior
	// reload cycle out client-side. The dashboard CONSOLE keeps
	// replay (default) so it doesn't lose context across reconnects.
	fresh := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("fresh")), "1") ||
		strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("fresh")), "true")
	var ch chan DevServerEvent
	if fresh {
		ch = mgr.SubscribeFresh()
	} else {
		ch = mgr.Subscribe()
	}
	defer mgr.Unsubscribe(ch)

	// Periodic keepalive comments. Without these, an idle Metro that
	// emits no log lines for 30+ s results in zero bytes flowing
	// through the relay/Cloudflare edge, which kills the upstream
	// connection as "stalled" and returns 502 to the browser. SSE
	// comments (lines starting with `:`) are ignored by clients but
	// keep the byte stream alive end-to-end.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ":keep-alive %d\n\n", time.Now().Unix())
			flusher.Flush()
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

// handleDevWebProxy reverse-proxies requests to the sibling Expo Web
// process when one is running. /dev-web/* → http://127.0.0.1:{webPort}/*
// Completely independent of /dev/*, which continues to point at Metro
// so the Hermes bundle URL (/dev/index.bundle?platform=ios|android)
// keeps working while the browser iframe renders Expo Web's HTML.
func (s *HTTPServer) handleDevWebProxy(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	port := s.devServerMgr.WebPreviewPort()
	if port == 0 {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no Expo Web preview running — POST /dev/web-preview/start"})
		return
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Critical for Expo SDK 54+: Expo's CorsMiddleware rejects any
	// Origin that isn't localhost / 127.0.0.1 with HTTP 403
	// "Unauthorized request from <origin>". Through the relay our
	// iframe sends `Origin: https://yaver.io`, expo says no, and the
	// HMR scripts + asset fetches all 403 — the iframe paints blank
	// or fails to register the dev client. Rewrite the Origin (and
	// Referer for parity) to the local target before forwarding so
	// expo's CORS check sees its own loopback and lets the request
	// through. Other forwarded headers stay intact.
	originalDirector := proxy.Director
	localOrigin := fmt.Sprintf("http://127.0.0.1:%d", port)
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("Origin", localOrigin)
		if req.Header.Get("Referer") != "" {
			req.Header.Set("Referer", localOrigin+"/")
		}
		req.Host = req.URL.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "expo web unavailable", http.StatusBadGateway)
	}
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/dev-web")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	// Expo Web uses WebSockets for HMR on the same port as HTTP.
	if isWebSocketUpgrade(r) {
		s.proxyWebSocket(w, r, fmt.Sprintf("127.0.0.1:%d", port))
		return
	}
	proxy.ServeHTTP(w, r)
}

// handleDevWebPreviewStart spawns an Expo Web sibling alongside Metro.
// Owner-only; guests never spawn subprocesses.
// POST /dev/web-preview/start → { ok, port, webUrl }
func (s *HTTPServer) handleDevWebPreviewStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	port, err := s.devServerMgr.StartWebPreview()
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"port":   port,
		"webUrl": "/dev-web/",
	})
}

// handleDevWebPreviewStop terminates the Expo Web sibling. Metro is
// left alone so Hermes push keeps working.
// POST /dev/web-preview/stop → { ok }
func (s *HTTPServer) handleDevWebPreviewStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if err := s.devServerMgr.StopWebPreview(); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
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

// extractErrorMessage pulls the `{"error": "..."}` field from a JSON
// body, falling back to the raw body string. Used by handleReloadApp
// to surface a readable build-failure message to the SDK.
func extractErrorMessage(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
		return parsed.Error
	}
	return strings.TrimSpace(string(body))
}

func changeClassOrDefault(changeClass string) string {
	if strings.TrimSpace(changeClass) == "" {
		return "done"
	}
	return changeClass
}

// emitBuildProgress reports one step of a native-bundle build to every
// listener simultaneously:
//
//   - `/dev/events` (Yaver mobile app's progress banner, via EmitLog)
//   - `/blackbox/command-stream` (Feedback SDK's status toast, via
//     BroadcastCommand with `{command: "status", data: {message, phase, progress}}`)
//   - native-build-status JSON on disk (debugging / yaver status)
//
// Callers pass a user-facing message (shown verbatim in the SDK's toast)
// plus a short machine-readable phase label (`build`, `bundle`,
// `assets`, `push`, `done`, `error`) the SDK can switch on.
func (s *HTTPServer) emitBuildProgress(message, phase string) {
	s.emitBuildProgressWithPercent(message, phase, phaseProgress(phase))
}

// emitBuildProgressWithPercent reports one step PLUS a 0..1 fraction so
// the Feedback SDK / Yaver mobile app can render a progress bar like
// the Yaver mobile DevPreview already does (`buildProgress` state).
// `percent` should be monotonically non-decreasing over a single build.
func (s *HTTPServer) emitBuildProgressWithPercent(message, phase string, percent float64) {
	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}
	if s.devServerMgr != nil {
		s.devServerMgr.EmitLog(message)
	}
	if s.blackboxMgr != nil {
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "status",
			Data: map[string]interface{}{
				"message":  message,
				"phase":    phase,
				"progress": percent,
			},
		})
	}
}

// phaseProgress maps a build phase label to a coarse 0..1 fraction so
// the Feedback SDK's progress bar advances even when the phase doesn't
// supply an explicit percent. Same shape the mobile app's
// DevPreview.tsx uses for native builds.
func phaseProgress(phase string) float64 {
	switch phase {
	case "prepare":
		return 0.03
	case "build":
		return 0.05
	case "install":
		return 0.10
	case "bundle":
		return 0.45
	case "hermes":
		return 0.70
	case "compat":
		return 0.82
	case "assets":
		return 0.85
	case "ready":
		return 0.95
	case "push":
		return 0.98
	case "restart":
		return 0.99
	case "done":
		return 1.0
	case "error":
		return 1.0
	default:
		return 0
	}
}

// handleBuildNativeBundle builds a production Hermes bytecode bundle for the active project.
// POST /dev/build-native { "platform": "ios" }
// Returns { "status": "ok", "bundleUrl": "/dev/native-bundle", "moduleName": "main" } on success.
func (s *HTTPServer) handleBuildNativeBundle(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if s.isolatedGuestDevMutationBlocked(w, r, "native bundle build") {
		return
	}

	var req struct {
		Platform string `json:"platform"`
		// projectName / projectPath let a Feedback-SDK caller (e.g.
		// SFMG installed via TestFlight, no `yaver dev start` ever
		// run) tell the agent which mobile project to build. Without
		// this hint the agent would have to fall back to whatever
		// dev server is "active" — usually nothing for a TestFlight
		// app — and 400 with "no active dev server". This is the
		// fix for the SFMG Hot Reload crash where the agent
		// broadcast reload_bundle on a never-built bundle.
		ProjectName string `json:"projectName"`
		ProjectPath string `json:"projectPath"`
		BundleID    string `json:"bundleId"`
		// Target controls compile output:
		//   "" / "mobile-hermes"   — Metro bundle + hermesc → HBC
		//                            served via /dev/native-bundle
		//                            (default — mobile super-host load)
		//   "web-js-bundle"        — `expo export -p web` → static web bundle
		//                            served via /dev/web-bundle
		//                            (default for caller=web-dashboard/*)
		//   "web-hermes-wasm"      — Metro bundle (web platform) + hermesc → HBC
		//                            served alongside hermes.wasm runner
		//                            via /dev/web-bundle
		//                            (experimental — same HBC executes in
		//                             browser via Hermes WASM build)
		// Platform field still picks ios|android for mobile-hermes;
		// it's ignored when target is web-*.
		Target string `json:"target"`
		// Caller compat baseline — only consumed by web targets.
		// Web UI sends `clientVersion: "web/1.1.96"` plus the React
		// range it knows how to render (today: a single major).
		// Agent's preflight uses these to fail fast when the
		// project's installed deps drift outside what the caller
		// supports. Mirrors mobile's HBC-vs-manifest validation.
		ClientVersion            string          `json:"clientVersion,omitempty"`
		ExpectReact              string          `json:"expectReact,omitempty"`
		ExpectReactDom           string          `json:"expectReactDom,omitempty"`
		AllowUnsafeNativeModules bool            `json:"allowUnsafeNativeModules,omitempty"`
		ConsumerVersion          string          `json:"consumerVersion,omitempty"`
		ConsumerBuild            string          `json:"consumerBuild,omitempty"`
		ConsumerSDKVersion       string          `json:"consumerSdkVersion,omitempty"`
		ConsumerHermesBCVersion  int             `json:"consumerHermesBCVersion,omitempty"`
		ConsumerCurrentFamilyID  string          `json:"consumerCurrentRuntimeFamilyId,omitempty"`
		ConsumerDefaultFamilyID  string          `json:"consumerDefaultRuntimeFamilyId,omitempty"`
		ConsumerRuntimeFamilies  []RuntimeFamily `json:"consumerRuntimeFamilies,omitempty"`
		// AutoAlignRuntime asks the agent to rewrite the project's
		// package.json overrides to match the closest compiledIn host
		// runtime family (and run npm install once) before bundling. This
		// fixes the "React 19.2.5 vs host 19.1.0" failure mode that
		// otherwise turns into a 409 RUNTIME_FAMILY_MISMATCH at the load
		// step. Default true — pass false to opt out for projects whose
		// owner manages overrides themselves.
		AutoAlignRuntime *bool `json:"autoAlignRuntime,omitempty"`
		// Debug: when true, hermesc compiles with -Og (opts suitable
		// for debugging) + -g (full debug info for backtraces) + emits
		// a source map sidecar (.map) alongside the .jsbundle. The
		// resulting bundle is ~2-3x larger and a few % slower at
		// runtime, but: (a) Hermes crash backtraces include source
		// file/line so the captured EXC_BAD_ACCESS dumps from
		// YaverGuestCrashReporter point at actual JS code instead of
		// hermes::vm internals; (b) the source map lets the symbolicator
		// turn minified function names back into their source forms.
		// Off by default — production loads stay fast.
		Debug bool `json:"debug,omitempty"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	caller := extractCaller(r)

	// Resolve the build target. Explicit `target` field always wins.
	// Otherwise default by caller surface so existing mobile callers
	// (no `target` field) keep their current behavior, and web-dashboard
	// callers automatically get the web JS bundle path.
	buildTarget := strings.TrimSpace(req.Target)
	if buildTarget == "" {
		if strings.HasPrefix(caller, "web-dashboard/") {
			buildTarget = "web-js-bundle"
		} else {
			buildTarget = "mobile-hermes"
		}
	}
	switch buildTarget {
	case "mobile-hermes", "web-js-bundle", "web-hermes-wasm":
		// ok
	default:
		jsonReply(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unknown target %q — must be mobile-hermes, web-js-bundle, or web-hermes-wasm", buildTarget),
		})
		return
	}

	if buildTarget == "mobile-hermes" {
		if req.Platform == "" {
			req.Platform = "ios"
		}
		if req.Platform != "ios" && req.Platform != "android" {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("platform %q invalid for mobile-hermes — must be ios or android", req.Platform),
			})
			return
		}
	} else {
		// Web targets always compile for the web platform; ignore any
		// stale `platform: ios` left over from a mobile-app caller's
		// boilerplate body.
		req.Platform = "web"
	}
	log.Printf("[build-native] caller=%s target=%s platform=%s project=%s path=%s",
		caller, buildTarget, req.Platform, req.ProjectName, req.ProjectPath)

	// Resolve workDir in priority order:
	//   1. explicit projectPath in body
	//   2. projectName looked up in mobile-projects scan / workspace manifest
	//   3. bundleId looked up in the cached mobile-project scan
	//   4. devServerMgr's currently-active project (legacy path)
	var workDir string
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID != "" {
		if strings.TrimSpace(req.ProjectName) != "" {
			resolved, err := s.guestResolveDevWorkDir(r, req.ProjectName, "")
			if err != nil {
				jsonReply(w, http.StatusForbidden, map[string]string{"error": err.Error()})
				return
			}
			workDir = resolved
		} else if strings.TrimSpace(req.ProjectPath) != "" {
			workDir = strings.TrimSpace(req.ProjectPath)
			if !s.guestAllowedDevWorkDir(guestUID, workDir) {
				jsonReply(w, http.StatusForbidden, map[string]string{"error": "guest cannot access this project path"})
				return
			}
		}
	} else if req.ProjectPath != "" || req.ProjectName != "" {
		if ref, err := resolveProjectRef(req.ProjectName, req.ProjectPath); err == nil {
			workDir = ref.Path
			if strings.TrimSpace(req.ProjectName) == "" {
				req.ProjectName = ref.Name
			}
		} else if req.ProjectName != "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("no mobile project named %q on this machine — check `yaver projects mobile`", req.ProjectName),
			})
			return
		}
	}
	if workDir == "" && strings.TrimSpace(req.BundleID) != "" {
		if mp := findMobileProjectByBundleID(req.BundleID); mp != nil && strings.TrimSpace(mp.Path) != "" {
			workDir = strings.TrimSpace(mp.Path)
			if strings.TrimSpace(req.ProjectName) == "" {
				req.ProjectName = strings.TrimSpace(mp.Name)
			}
		} else if req.ProjectPath != "" || req.ProjectName != "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("bundle id %q did not match any scanned mobile project on this machine", req.BundleID),
			})
			return
		}
	}
	if workDir == "" {
		status := s.devServerMgr.Status()
		if status == nil || status.WorkDir == "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no active dev server — start one first OR pass projectName / projectPath / bundleId in the body"})
			return
		}
		workDir = status.WorkDir
	}
	if guestUID == "" {
		s.maybePullBeforeHotReloadBuild(workDir)
	}
	// Pick output dir per target so concurrent mobile+web builds for
	// the same project don't trash each other's bundles.
	var buildDir string
	switch buildTarget {
	case "web-js-bundle":
		buildDir = filepath.Join(workDir, ".yaver-build-web")
	case "web-hermes-wasm":
		buildDir = filepath.Join(workDir, ".yaver-build-web-hermes")
	default:
		buildDir = filepath.Join(workDir, ".yaver-build")
	}
	bundlePath := filepath.Join(buildDir, "main.jsbundle")

	// Web targets short-circuit out to their own builders. Everything
	// below this point is the existing mobile-hermes pipeline; keeping
	// the diff that small means we cannot regress mobile.
	if buildTarget == "web-js-bundle" || buildTarget == "web-hermes-wasm" {
		s.handleBuildWebTarget(w, r, buildWebRequest{
			Target:         buildTarget,
			Caller:         caller,
			WorkDir:        workDir,
			BuildDir:       buildDir,
			ProjectName:    req.ProjectName,
			ClientVersion:  strings.TrimSpace(req.ClientVersion),
			ExpectReact:    strings.TrimSpace(req.ExpectReact),
			ExpectReactDom: strings.TrimSpace(req.ExpectReactDom),
		})
		return
	}

	target := DevServerTarget{}
	if s.devServerMgr != nil {
		target = s.devServerMgr.PreferredTarget()
	}
	buildOp := s.upsertDevOperation("build_native", "running", "build", "Preparing native bundle build…", workDir, target.DeviceID, 0.02, map[string]interface{}{
		"platform": req.Platform,
	})

	log.Printf("[super-host] build-native called: platform=%s workDir=%s", req.Platform, workDir)
	consumer := nativeBuildConsumerContract{
		Platform:        req.Platform,
		AppVersion:      req.ConsumerVersion,
		AppBuild:        req.ConsumerBuild,
		SDKVersion:      req.ConsumerSDKVersion,
		HermesBCVersion: req.ConsumerHermesBCVersion,
	}

	// Git-state cache short-circuit (gated on YAVER_BUILD_CACHE=1 so we
	// can disable in seconds if it ever serves a stale bundle). Skipped
	// when buildDir doesn't yet have a prior bundle, when the consumer
	// contract differs from the cached build (mobile updated), or when
	// git-relevant source files have changed since the last build.
	//
	// Must run BEFORE writeNativeBuildStatus(state="building") below,
	// which would clobber LastBuiltGitSHA / LastBuiltSourceTreeSHA on
	// the first call after agent restart.
	if cached := tryServeCachedNativeBundle(s, w, workDir, buildDir, bundlePath, consumer, req.Platform, target, buildOp); cached {
		return
	}

	s.emitBuildProgress("Checking cache validity for this Yaver build...", "prepare")
	s.upsertDevOperation("build_native", "running", "prepare", "Checking cache validity for this Yaver build…", workDir, target.DeviceID, phaseProgress("prepare"), map[string]interface{}{"platform": req.Platform})
	writeNativeBuildStatus(workDir, nativeBuildStatus{
		State:         "building",
		Platform:      req.Platform,
		ConsumerKey:   nativeBuildConsumerKey(consumer),
		ConsumerLabel: nativeBuildConsumerLabel(consumer),
	})
	cacheDecision, prepErr := prepareNativeBuildOutput(buildDir, workDir, consumer)
	if prepErr != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("prepare build output: %v", prepErr)})
		return
	}
	log.Printf("[build-native] cache-valid=%v consumer=%q message=%s", cacheDecision.Valid, nativeBuildConsumerKey(consumer), cacheDecision.Message)
	s.emitBuildProgress(cacheDecision.Message, "prepare")
	s.upsertDevOperation("build_native", "running", "prepare", cacheDecision.Message, workDir, target.DeviceID, phaseProgress("prepare"), map[string]interface{}{
		"platform":      req.Platform,
		"cacheValid":    cacheDecision.Valid,
		"consumerKey":   nativeBuildConsumerKey(consumer),
		"consumerLabel": cacheDecision.Label,
	})
	s.emitBuildProgress("Building native bundle...", "build")
	s.upsertDevOperation("build_native", "running", "build", "Building native bundle…", workDir, target.DeviceID, phaseProgress("build"), map[string]interface{}{"platform": req.Platform, "cacheValid": cacheDecision.Valid})

	manifest, manifestErr := readProjectPackageManifest(workDir)
	if manifestErr != nil {
		incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "package.json missing or invalid", "The agent cannot build a native bundle because the project manifest is missing or invalid.", manifestErr.Error(), "Restore a valid package.json and try the build again.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
		s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
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
		incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "Required build tools are missing", "The machine is missing required tools for native bundle build.", errMsg, "Install the missing tools on the host and retry the build.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, map[string]interface{}{"missingTools": prep.MissingTools}, buildOp.ID)
		s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
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
			incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "Dependencies are missing", "The machine is missing project dependencies and cannot auto-install them for this build.", errMsg, "Install the project dependencies on the host before retrying.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
			s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
			writeNativeBuildStatus(workDir, nativeBuildStatus{
				State:        "build_failed",
				Platform:     req.Platform,
				LastFailedAt: time.Now().UTC().Format(time.RFC3339),
				LastError:    errMsg,
			})
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
			return
		}
		// Surface the actual install location in the progress message so
		// the user can tell from the mobile Hot Reload card that we're
		// installing at the workspace root (not just the leaf). For a
		// monorepo this is exactly the bit that distinguishes a
		// successful auto-bootstrap from a leaf-only install that
		// silently misses workspace symlinks.
		installAt := workDir
		if prep.WorkspaceRoot != "" {
			installAt = prep.WorkspaceRoot
		}
		progressMsg := fmt.Sprintf("Installing dependencies with %s in %s…", prep.PackageManager, installAt)
		if installAt != workDir {
			progressMsg = fmt.Sprintf("Installing workspace dependencies with %s at %s (monorepo root) — required so workspace-linked deps resolve before bundling.", prep.PackageManager, installAt)
		}
		s.emitBuildProgress(progressMsg, "install")
		s.upsertDevOperation("build_native", "running", "install", progressMsg, workDir, target.DeviceID, phaseProgress("install"), map[string]interface{}{
			"platform":      req.Platform,
			"installAt":     installAt,
			"workspaceRoot": prep.WorkspaceRoot,
		})
		// Tee install stdout/stderr through devLogWriter so every
		// npm/yarn/pnpm line surfaces as an SSE "log" event on
		// /dev/events — the mobile Hot Reload card and web preview
		// both already render those lines, so the user sees the
		// install happen live instead of staring at "Installing
		// dependencies…" for two minutes with nothing changing.
		installLogger := &devLogWriter{prefix: "[install]"}
		if s.devServerMgr != nil {
			installLogger.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
		}
		if err := installProjectDependenciesTo(workDir, prep, installLogger); err != nil {
			errMsg := fmt.Sprintf("dependency install failed: %v", err)
			incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "Dependency install failed", "The agent could not install the project dependencies required for bundle build.", errMsg, "Fix the dependency install failure on the host and retry.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
			s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
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

	// ── Runtime family selection preflight ──
	// The host advertises the runtime families it can safely execute.
	// The agent fingerprints the guest app and selects the closest host
	// family before bundle/compile so logs and UI can explain the target
	// contract upfront, even when the final load is blocked later.
	var compatReport *CompatReport
	if report, err := BuildNativeModuleCompatReportWithFamilies(workDir, req.ConsumerRuntimeFamilies); err == nil {
		compatReport = report
		if report.RuntimeFamily != nil {
			sel := report.RuntimeFamily
			guestSummary := fmt.Sprintf(
				"Expo %s / RN %s / React %s",
				fallbackRuntimeValue(sel.Guest.ExpoVersion, "?"),
				fallbackRuntimeValue(sel.Guest.ReactNativeVersion, "?"),
				fallbackRuntimeValue(sel.Guest.ReactVersion, "?"),
			)
			selectedLabel := strings.TrimSpace(sel.Selected.Label)
			if selectedLabel == "" {
				selectedLabel = sel.Selected.ID
			}
			metaMsg := fmt.Sprintf(
				"Runtime family %s: guest Expo %s / RN %s / React %s -> host %s",
				sel.MatchKind,
				fallbackRuntimeValue(sel.Guest.ExpoVersion, "?"),
				fallbackRuntimeValue(sel.Guest.ReactNativeVersion, "?"),
				fallbackRuntimeValue(sel.Guest.ReactVersion, "?"),
				sel.Selected.ID,
			)
			currentFamilyID := strings.TrimSpace(req.ConsumerCurrentFamilyID)
			defaultFamilyID := strings.TrimSpace(req.ConsumerDefaultFamilyID)
			switch {
			case currentFamilyID != "" && currentFamilyID != sel.Selected.ID:
				metaMsg += fmt.Sprintf(" (switching host family from %s)", currentFamilyID)
			case currentFamilyID == "" && defaultFamilyID != "" && defaultFamilyID != sel.Selected.ID:
				metaMsg += fmt.Sprintf(" (default host family is %s)", defaultFamilyID)
			}
			s.devServerMgr.EmitLog("Guest runtime: " + guestSummary)
			if sel.ExactMatch {
				log.Printf("[super-host] runtime-family match: %s", sel.Reason)
				s.devServerMgr.EmitLog("Host runtime matched: " + selectedLabel)
				s.emitBuildProgress("Host runtime matched: "+selectedLabel, "prepare")
				if currentFamilyID != "" && currentFamilyID != sel.Selected.ID {
					s.devServerMgr.EmitLog("Switching host runtime from " + currentFamilyID + " to " + sel.Selected.ID)
				}
			} else {
				log.Printf("[super-host] runtime-family closest: %s | supported=%s", sel.Reason, sel.SupportedHint)
				s.devServerMgr.EmitLog("Host runtime selected: " + selectedLabel)
				if strings.TrimSpace(sel.Reason) != "" {
					s.devServerMgr.EmitLog("Why: " + sel.Reason)
				}
				if strings.TrimSpace(sel.SupportedHint) != "" {
					s.devServerMgr.EmitLog("Host supports: " + sel.SupportedHint)
				}
				s.emitBuildProgress("Host runtime selected: "+selectedLabel, "prepare")
			}
			s.devServerMgr.EmitLog("Compiling against host runtime contract: " + selectedLabel)
			s.emitBuildProgress("Compiling for host runtime: "+selectedLabel, "build")
			s.upsertDevOperation("build_native", "running", "prepare", metaMsg, workDir, target.DeviceID, phaseProgress("prepare"), map[string]interface{}{
				"platform":                       req.Platform,
				"runtimeFamilySelection":         sel,
				"consumerCurrentRuntimeFamilyId": currentFamilyID,
				"consumerDefaultRuntimeFamilyId": defaultFamilyID,
			})
		}
	} else {
		log.Printf("[super-host] runtime-family probe skipped: %v", err)
	}

	// ── Auto-align project runtime to a compiledIn host family ──
	// When the project's React/RN/Expo doesn't already match a compiledIn
	// host family, rewrite package.json `overrides` and run `npm install`
	// once so the bundle is ABI-compatible with the device. Idempotent on
	// subsequent builds. Default behavior; caller passes
	// `autoAlignRuntime: false` to opt out.
	autoAlign := true
	if req.AutoAlignRuntime != nil {
		autoAlign = *req.AutoAlignRuntime
	}
	if compatReport != nil && autoAlign {
		candidateFamilies := req.ConsumerRuntimeFamilies
		if len(candidateFamilies) == 0 {
			if hostFams, herr := HostRuntimeFamilies(); herr == nil {
				candidateFamilies = hostFams
			}
		}
		chosen, skipReason := pickCompiledInRuntimeFamily(compatReport.GuestRuntime, candidateFamilies)
		if chosen == nil {
			if skipReason != "" {
				log.Printf("[runtime-align] skipped: %s", skipReason)
			}
		} else {
			alignCtx, alignCancel := context.WithTimeout(r.Context(), 4*time.Minute)
			alignReport := alignProjectRuntimeIfNeeded(alignCtx, workDir, chosen, compatReport.GuestRuntime, true)
			alignCancel()
			if alignReport.Error != "" {
				log.Printf("[runtime-align] error: %s", alignReport.Error)
				s.devServerMgr.EmitLog("Runtime auto-align failed: " + alignReport.Error)
			} else if alignReport.Applied {
				log.Printf("[runtime-align] applied target=%s overrides=%v", alignReport.TargetFamilyID, alignReport.OverridesAfter)
				s.devServerMgr.EmitLog(fmt.Sprintf(
					"Aligned project runtime to host family %s (npm install %dms)",
					alignReport.TargetFamilyID, alignReport.NPMInstallMs,
				))
				// Re-run the compat probe so subsequent stages see the
				// post-align fingerprint, not the pre-align one.
				if rebuilt, rerr := BuildNativeModuleCompatReportWithFamilies(workDir, req.ConsumerRuntimeFamilies); rerr == nil {
					compatReport = rebuilt
				}
			} else if strings.TrimSpace(alignReport.SkippedReason) != "" {
				log.Printf("[runtime-align] skipped: %s", alignReport.SkippedReason)
			}
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
	// Wall-clock cap so a hung Metro/Expo subprocess (broken project, infinite
	// resolver loop, missing native dep) doesn't keep /dev/build-native
	// blocked. On expiry exec.CommandContext kills the bundler and cmd.Run
	// returns; we detect ctx.Err() == DeadlineExceeded and surface a
	// dedicated build_failed status to the mobile DevPreview / hot reload UI.
	bundleCtx, bundleCancel := context.WithTimeout(r.Context(), bundleBuildTimeout)
	defer bundleCancel()
	// Register so /dev/stop can interrupt a hung Metro/expo subprocess
	// without waiting for bundleBuildTimeout (8m default) to expire.
	releaseActiveBuild := registerActiveBuild(workDir, req.Platform, bundleCancel)
	defer releaseActiveBuild()
	assetsDir := filepath.Join(buildDir, "assets")
	// Decide whether to nuke Metro's cache. --reset-cache forces a full
	// haste-map rebuild which is the heaviest memory step in the whole
	// bundle (peaks 4-6 GB on a SFMG-sized RN project). On a small box
	// it OOM-kills node. Skip it on incremental rebuilds — Metro
	// invalidates entries when source files change anyway.
	resetCache := shouldResetMetroCache(workDir)
	var cmd *exec.Cmd
	if isExpo {
		msg := fmt.Sprintf("Bundling with Expo for %s%s...", req.Platform, cacheLabel(resetCache))
		s.emitBuildProgress(msg, "bundle")
		s.upsertDevOperation("build_native", "running", "bundle", msg, workDir, target.DeviceID, phaseProgress("bundle"), map[string]interface{}{"platform": req.Platform, "framework": "expo", "resetCache": resetCache})
		cmd = bundleCommand(bundleCtx, prep.PackageManager, "expo", req.Platform, "", bundlePath, assetsDir, resetCache)
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
		msg := fmt.Sprintf("Bundling %s for %s%s...", entryFile, req.Platform, cacheLabel(resetCache))
		s.emitBuildProgress(msg, "bundle")
		s.upsertDevOperation("build_native", "running", "bundle", msg, workDir, target.DeviceID, phaseProgress("bundle"), map[string]interface{}{"platform": req.Platform, "framework": "react-native", "entryFile": entryFile, "resetCache": resetCache})
		cmd = bundleCommand(bundleCtx, prep.PackageManager, "react-native", req.Platform, entryFile, bundlePath, assetsDir, resetCache)
	}

	cmd.Dir = workDir
	// augmentEnv prepends the agent-managed Node bin so a fresh
	// Linux box (no system Node) still bundles after /install/node.
	// NODE_OPTIONS caps node's heap so a runaway Metro doesn't OOM-kill
	// itself (and the agent) on small boxes. 5120 MB is generous for a
	// large RN bundle and well below the 7-8 GB physical RAM where the
	// Linux OOM-killer starts reaping. Without this cap, node will try
	// to grow its heap to the full machine size and SIGKILL itself.
	cmd.Env = append(augmentEnv(nil), "NODE_ENV=production", "NODE_OPTIONS=--max-old-space-size=5120")

	log.Printf("[super-host] bundling with command: %v", cmd.Args)
	logW := &devLogWriter{prefix: "[super-host]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	// Capture the bundler's tail so the /dev/build-native response can
	// surface the actual failure reason inline. Without this the
	// caller only saw "Check agent logs for details." which is useless
	// for a remote dev box where the agent log isn't tailable from
	// the dashboard.
	tail := newRingTailWriter(120)
	cmd.Stdout = io.MultiWriter(logW, tail)
	cmd.Stderr = io.MultiWriter(logW, tail)

	if err := cmd.Run(); err != nil {
		tailLines := tail.lines()
		// Timeout takes precedence over the raw exec error — when the
		// bundler hangs, exec.CommandContext kills it and cmd.Run returns
		// "signal: killed", which is misleading. Surface "timed out" so the
		// mobile app shows a useful message and the user knows to look at
		// project state (broken node_modules, infinite resolver loop, etc.)
		timedOut := bundleCtx.Err() == context.DeadlineExceeded
		errMsg := fmt.Sprintf("bundle failed: %v", err)
		summary := "JavaScript bundle build failed"
		userMsg := "The agent failed while producing the JavaScript bundle used for Hermes compilation."
		hint := "Output above is the last 120 lines of bundler stdout/stderr — usually contains a 'Cannot find module' / 'Unable to resolve' / 'JavaScript heap out of memory' line that points at the real cause."
		if timedOut {
			errMsg = fmt.Sprintf("bundle timed out after %s — the bundler was killed", bundleBuildTimeout)
			summary = "JavaScript bundle build timed out"
			userMsg = fmt.Sprintf("The bundler did not finish in %s. The subprocess was killed so the build endpoint can return an answer.", bundleBuildTimeout)
			hint = "Common causes: missing node_modules (run `npm install` in the project), Metro stuck on a circular import, or `npm install` running inside the bundler waiting for the network. The tail above shows what the bundler was doing when it was killed."
		}
		s.devServerMgr.EmitLog(errMsg)
		incident := s.appendDevIncident("build", ReasonBuildNativeFailed, summary, userMsg, err.Error(), "Inspect the bundler output and fix the project build error before retrying.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
		s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
		writeNativeBuildStatus(workDir, nativeBuildStatus{
			State:        "build_failed",
			Platform:     req.Platform,
			LastFailedAt: time.Now().UTC().Format(time.RFC3339),
			LastError:    errMsg,
		})
		statusCode := http.StatusInternalServerError
		if timedOut {
			statusCode = http.StatusGatewayTimeout
		}
		jsonReply(w, statusCode, map[string]interface{}{
			"error":    errMsg,
			"phase":    "bundle",
			"command":  cmd.Args,
			"workDir":  workDir,
			"output":   strings.Join(tailLines, "\n"),
			"timedOut": timedOut,
			"helpHint": hint,
		})
		return
	}

	if err := injectGuestSafePrelude(bundlePath); err != nil {
		errMsg := fmt.Sprintf("guest-safe prelude injection failed: %v", err)
		s.devServerMgr.EmitLog(errMsg)
		incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "Guest safety prelude failed", "Yaver could not prepare its guest-safety runtime shim before Hermes compilation.", errMsg, "Inspect the generated bundle path and retry the build after fixing host file permissions or disk state.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
		s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
		writeNativeBuildStatus(workDir, nativeBuildStatus{
			State:        "build_failed",
			Platform:     req.Platform,
			LastFailedAt: time.Now().UTC().Format(time.RFC3339),
			LastError:    errMsg,
		})
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": errMsg,
			"code":  "GUEST_SAFE_PRELUDE_FAILED",
		})
		return
	}
	s.devServerMgr.EmitLog("Injected Yaver guest-safe runtime shims for ExpoHaptics and RNCNetInfo.")

	// ── Hermes compile ──
	s.emitBuildProgress("Compiling Hermes bytecode...", "hermes")
	s.upsertDevOperation("build_native", "running", "hermes", "Compiling Hermes bytecode…", workDir, target.DeviceID, phaseProgress("hermes"), map[string]interface{}{"platform": req.Platform})

	// Yaver Protocol v1: emit a structured `topic=hermes/compile`
	// progress event so the dashboard CONSOLE can render the bytecode
	// compile as its own phase row instead of just an inline stdout line.
	// We don't have per-byte progress from hermesc (its stdout is
	// silent on success), so this is a coarse two-phase signal:
	// hermesc_compiling → ready (or → error). Real progress would
	// require parsing hermesc's `--progress` output, which the upstream
	// binary doesn't ship today.
	if mgr := s.devServerMgr; mgr != nil {
		if mgr.hermesTracker == nil {
			frameworkName := "expo"
			if st := mgr.Status(); st != nil && st.Framework != "" {
				frameworkName = st.Framework
			}
			mgr.hermesTracker = newProgressTracker(mgr.emit, frameworkName, "hermes/compile", "build-native")
		}
		mgr.hermesTracker.transitionPhase("hermesc_compiling")
	}

	info := detectHermesRuntimeInfo(workDir)
	hermescPath, hermescErr := resolveHermesc(workDir)
	if hermescErr != nil {
		log.Printf("[super-host] hermesc resolve failed: %v", hermescErr)
	}

	if hermescPath == "" {
		log.Printf("[super-host] hermesc not found — cannot produce Hermes bundle")
		s.devServerMgr.EmitLog("hermesc not found — cannot produce Hermes bundle")
		incident := s.appendDevIncident("build", ReasonBuildHermesFailed, "Hermes compiler not found", "The machine could not find `hermesc`, so it cannot produce the Hermes bytecode bundle expected by the app.", hermescErrString(hermescErr), "Install the Hermes compiler on the host or use a machine with a valid React Native toolchain.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
		s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
	} else {
		log.Printf("[super-host] using hermesc at: %s", hermescPath)

		// ── Layer 0: HBC content-hash cache (secondary-reload optimization) ──
		// If the JS bundle Metro just produced is byte-identical to one we
		// previously compiled, skip hermesc entirely and serve the cached
		// HBC. Cache failures fall through silently. See
		// docs/hermes-secondary-reload-optimization.md §13.1 for the
		// safety analysis. The lookup emits cache_lookup → cache_hit /
		// cache_miss / cache_corrupt phase events on the hermesTracker so
		// the mobile UI sees what happened (the "always stream even on
		// fallback" guarantee).
		hbcCache := prepareDevHBCCacheLookup(bundlePath, hermescPath, req.Debug, s.devServerMgr)
		if hbcCache.Hit() {
			// Cache hit — bundlePath now contains the validated cached
			// HBC; skip the hermesc invocation entirely. The
			// hermesTracker has already been transitioned to `ready`
			// inside prepareDevHBCCacheLookup, mirroring the success
			// path below.
			GlobalIncidentStore().ResolveOpenByKey(IncidentKey{
				Category:    "build",
				Code:        ReasonBuildHermesFailed,
				DeviceID:    strings.TrimSpace(target.DeviceID),
				ProjectPath: strings.TrimSpace(workDir),
				Target:      strings.TrimSpace(req.Platform),
			}, "Hermes bundle served from cache.")
			s.devServerMgr.EmitLog("Hermes bundle served from cache (no compile needed)")
			log.Printf("[super-host] hermes cache hit — skipped hermesc")
		} else {
			tmpPath := bundlePath + ".tmp"
			os.Rename(bundlePath, tmpPath)

			// Wall-clock cap on hermesc compile. hermesc is fast (<30s for a
			// large RN bundle) but a corrupt input or an OOM-killed retry can
			// hang it; without this the /dev/build-native HTTP request would
			// stay open indefinitely after a successful Metro bundle.
			hermesCtx, hermesCancel := context.WithTimeout(r.Context(), hermesCompileTimeout)
			// Re-register under the hermesc cancel so /dev/stop can also
			// interrupt the compile phase, not just the Metro bundle phase.
			releaseHermesBuild := registerActiveBuild(workDir, req.Platform, hermesCancel)
			// Default: -O (heavy optimization) — production-grade bundle.
			// Debug=true: -Og + -g + -output-source-map — keeps Hermes
			// runtime backtraces symbolicatable. See req.Debug docs above.
			hermesArgs := []string{"-emit-binary", "-out", bundlePath}
			if req.Debug {
				hermesArgs = append(hermesArgs, "-Og", "-g", "-output-source-map")
				log.Printf("[super-host] hermesc DEBUG mode: emitting -Og -g + source map")
				s.devServerMgr.EmitLog("Hermes debug build (source maps + line info)")
			} else {
				hermesArgs = append(hermesArgs, "-O")
			}
			hermesArgs = append(hermesArgs, tmpPath)
			hermesCmd := exec.CommandContext(hermesCtx, hermescPath, hermesArgs...)
			hermesCmd.Dir = workDir
			hermesLogW := &devLogWriter{prefix: "[super-host:hermesc]"}
			hermesCmd.Stdout = hermesLogW
			hermesCmd.Stderr = hermesLogW

			hermesErr := hermesCmd.Run()
			hermesCancel()
			releaseHermesBuild()
			if err := hermesErr; err != nil {
				if hermesCtx.Err() == context.DeadlineExceeded {
					err = fmt.Errorf("hermesc timed out after %s (subprocess killed)", hermesCompileTimeout)
				}
				os.Rename(tmpPath, bundlePath)
				log.Printf("[super-host] hermesc failed: %v — using plain JS", err)
				s.devServerMgr.EmitLog(fmt.Sprintf("hermesc failed, using plain JS: %v", err))
				incident := s.appendDevIncident("build", ReasonBuildHermesFailed, "Hermes compilation failed", "Hermes bytecode compilation failed on the host.", err.Error(), "Inspect the Hermes compiler output and retry after fixing the host-side build issue.", workDir, target.DeviceID, req.Platform, IncidentSeverityWarn, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
				s.upsertDevOperation("build_native", "running", "hermes", "Hermes compilation failed; falling back to plain JS bundle.", workDir, target.DeviceID, 0.8, map[string]interface{}{"platform": req.Platform}, incident.ID)
				if mgr := s.devServerMgr; mgr != nil && mgr.hermesTracker != nil {
					mgr.hermesTracker.transitionPhase("error")
				}
				// Compile failed → don't poison the cache.
			} else {
				os.Remove(tmpPath)
				log.Printf("[super-host] hermesc compile complete")
				GlobalIncidentStore().ResolveOpenByKey(IncidentKey{
					Category:    "build",
					Code:        ReasonBuildHermesFailed,
					DeviceID:    strings.TrimSpace(target.DeviceID),
					ProjectPath: strings.TrimSpace(workDir),
					Target:      strings.TrimSpace(req.Platform),
				}, "Hermes compilation recovered.")
				if mgr := s.devServerMgr; mgr != nil && mgr.hermesTracker != nil {
					mgr.hermesTracker.transitionPhase("ready")
				}
				// Compile succeeded — file the bytecode under the
				// cache key prepared at lookup time. Failures are
				// logged inside the helper and never propagate.
				hbcCache.CommitOnSuccess(bundlePath)
			}
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
		incident := s.appendDevIncident("build", ReasonBuildNativeFailed, "Bundle validation failed", "The bundle was built, but the final native bundle failed validation.", errMsg, "Inspect the generated bundle and rebuild after fixing the underlying build problem.", workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, nil, buildOp.ID)
		s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{"platform": req.Platform}, incident.ID)
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

	// A successful build clears any prior native-build / native-reload
	// incidents for the same (deviceID, projectPath, target). Six different
	// paths above can open a ReasonBuildNativeFailed incident (manifest
	// missing, tools missing, deps missing, deps install failed, bundler
	// error, validation failed); without this, a stale failure pill sticks
	// in the mobile/web UI even after subsequent builds succeed.
	resolveKey := IncidentKey{
		Category:    "build",
		Code:        ReasonBuildNativeFailed,
		DeviceID:    strings.TrimSpace(target.DeviceID),
		ProjectPath: strings.TrimSpace(workDir),
		Target:      strings.TrimSpace(req.Platform),
	}
	GlobalIncidentStore().ResolveOpenByKey(resolveKey, "Native bundle build recovered.")

	// ── Native-module compatibility handshake ──
	// The bundle compiled cleanly, but that only proves Hermes is happy.
	// What it does not prove is that every TurboModule the project calls
	// at runtime is actually registered in Yaver's super-host. SFMG
	// shipping `react-native-record-screen` against a Yaver build that
	// doesn't know that selector is exactly the path that produced the
	// 1.18.22-build-246 NSException-into-JSError crash. We compare the
	// project's package.json deps against the host's embedded
	// sdk-manifest.json and surface mismatches in the build response;
	// the phone shows an actionable banner instead of crashing in JS.
	var compatIncompatible []string
	var compatMatched []string
	var compatIgnored []string
	var compatVersionMismatches []NativeModuleMismatch
	var compatReactVersionMismatch *VersionMismatch
	var compatExpoVersionMismatch *VersionMismatch
	var compatRNVersionMismatch *VersionMismatch
	var compatHostRN string
	if report := compatReport; report != nil {
		s.emitBuildProgress("Checking host compatibility...", "compat")
		s.upsertDevOperation("build_native", "running", "compat", "Checking whether Yaver can safely restart this bundle…", workDir, target.DeviceID, phaseProgress("compat"), map[string]interface{}{"platform": req.Platform})
		compatIncompatible = report.Incompatible
		compatMatched = report.Matched
		compatIgnored = report.Ignored
		compatVersionMismatches = append([]NativeModuleMismatch(nil), report.VersionMismatches...)
		compatReactVersionMismatch = report.ReactVersionMismatch
		compatExpoVersionMismatch = report.ExpoVersionMismatch
		compatRNVersionMismatch = report.RNVersionMismatch
		compatHostRN = report.HostRN
		meta.HostSDKVersion = report.HostSDKVersion
		meta.HostExpoVersion = strings.TrimSpace(report.HostExpo)
		meta.HostReactNativeVersion = strings.TrimSpace(report.HostRN)
		meta.HostReactVersion = strings.TrimSpace(report.HostReact)
		meta.GuestRuntime = &report.GuestRuntime
		meta.RuntimeFamilySelection = report.RuntimeFamily
		if report.RuntimeFamily != nil {
			meta.HostRuntimeFamilies = append([]RuntimeFamily(nil), report.RuntimeFamily.Supported...)
		}
		if report.ReactVersionMismatch != nil {
			meta.ProjectReactVersion = report.ReactVersionMismatch.ProjectVersion
		}
		if report.ExpoVersionMismatch != nil {
			meta.ProjectExpoVersion = report.ExpoVersionMismatch.ProjectVersion
		}
		if report.RNVersionMismatch != nil {
			meta.ReactNativeVersion = report.RNVersionMismatch.ProjectVersion
		}
		meta.ExpoVersionMismatch = report.ExpoVersionMismatch
		meta.ReactNativeVersionMismatch = report.RNVersionMismatch
		meta.ReactVersionMismatch = report.ReactVersionMismatch
		meta.SupportedRNRange = report.SupportedRNRange
		meta.IncompatibleNativeModules = append([]string(nil), compatIncompatible...)
		meta.NativeModuleVersionMismatches = append([]NativeModuleMismatch(nil), compatVersionMismatches...)
		if len(compatIgnored) > 0 {
			msg := fmt.Sprintf("Ignoring host-optional SDK package(s): %s", strings.Join(compatIgnored, ", "))
			log.Printf("[super-host] native-module compat: ignored (%v)", compatIgnored)
			s.devServerMgr.EmitLog(msg)
		}
		if len(compatVersionMismatches) > 0 {
			names := make([]string, 0, len(compatVersionMismatches))
			for _, mismatch := range compatVersionMismatches {
				names = append(names, fmt.Sprintf("%s (%s vs %s)", mismatch.Name, mismatch.ProjectVersion, mismatch.HostVersion))
			}
			log.Printf("[super-host] native-module compat: %d version mismatch(es) (%v)",
				len(compatVersionMismatches), names)
			s.devServerMgr.EmitLog(fmt.Sprintf(
				"⚠ Native module version drift at a likely-breaking boundary: %s.",
				strings.Join(names, ", "),
			))
		}
		if compatReactVersionMismatch != nil {
			log.Printf("[super-host] react compat: project=%s host=%s (%s)",
				compatReactVersionMismatch.ProjectVersion, compatReactVersionMismatch.HostVersion, compatReactVersionMismatch.Reason)
		}
		if compatExpoVersionMismatch != nil {
			log.Printf("[super-host] expo compat: project=%s host=%s (%s)",
				compatExpoVersionMismatch.ProjectVersion, compatExpoVersionMismatch.HostVersion, compatExpoVersionMismatch.Reason)
		}
		if compatRNVersionMismatch != nil {
			log.Printf("[super-host] react-native compat: project=%s host=%s (%s)",
				compatRNVersionMismatch.ProjectVersion, compatRNVersionMismatch.HostVersion, compatRNVersionMismatch.Reason)
		}
		if len(compatIncompatible) > 0 {
			log.Printf("[super-host] native-module compat: %d incompatible (%v)",
				len(compatIncompatible), compatIncompatible)
			s.devServerMgr.EmitLog(fmt.Sprintf(
				"⚠ %d native module(s) declared in this project are NOT in Yaver's super-host: %s. "+
					"They will throw at runtime if called.",
				len(compatIncompatible), strings.Join(compatIncompatible, ", "),
			))
		}
		if len(compatIncompatible) > 0 || len(compatVersionMismatches) > 0 || compatReactVersionMismatch != nil || compatExpoVersionMismatch != nil || compatRNVersionMismatch != nil {
			if !req.AllowUnsafeNativeModules {
				metaJSON := meta.JSON()
				s.devServerMgr.SetBundleMetadata(metaJSON)
				errMsg := "Blocked native Hermes load due to host compatibility mismatch."
				helpHint := "Align the project's native-module versions with Yaver's host, add missing modules to Yaver, or guard unsupported call sites before retrying."
				title := "Compatibility blocked"
				userMsg := "The bundle compiled, but Yaver blocked restart because the project's native runtime contract does not match the mobile host."
				respCode := "NATIVE_MODULE_INCOMPATIBLE"
				payload := map[string]interface{}{
					"platform":                      req.Platform,
					"incompatibleNativeModules":     compatIncompatible,
					"matchedNativeModules":          compatMatched,
					"ignoredNativeModules":          compatIgnored,
					"nativeModuleVersionMismatches": compatVersionMismatches,
					"expoVersionMismatch":           compatExpoVersionMismatch,
					"reactNativeVersionMismatch":    compatRNVersionMismatch,
					"reactVersionMismatch":          compatReactVersionMismatch,
					"blocked":                       true,
					"guestRuntime":                  report.GuestRuntime,
					"runtimeFamilySelection":        report.RuntimeFamily,
				}
				if len(compatIncompatible) > 0 {
					errMsg = fmt.Sprintf(
						"Blocked native Hermes load: this project declares %d native module(s) Yaver does not register: %s",
						len(compatIncompatible), strings.Join(compatIncompatible, ", "),
					)
					title = "Incompatible native modules"
					userMsg = "The bundle compiled, but it would crash at runtime because the project declares native modules missing from Yaver's mobile host."
				} else if len(compatVersionMismatches) > 0 {
					respCode = "NATIVE_MODULE_VERSION_MISMATCH"
					parts := make([]string, 0, len(compatVersionMismatches))
					for _, mismatch := range compatVersionMismatches {
						parts = append(parts, fmt.Sprintf("%s project %s vs host %s", mismatch.Name, mismatch.ProjectVersion, mismatch.HostVersion))
					}
					errMsg = fmt.Sprintf(
						"Blocked native Hermes load: host-native module version drift at a likely-breaking boundary: %s",
						strings.Join(parts, ", "),
					)
				} else {
					respCode = "RUNTIME_FAMILY_MISMATCH"
					parts := make([]string, 0, 3)
					if compatExpoVersionMismatch != nil {
						parts = append(parts, fmt.Sprintf("expo project %s vs host %s", compatExpoVersionMismatch.ProjectVersion, compatExpoVersionMismatch.HostVersion))
					}
					if compatRNVersionMismatch != nil {
						parts = append(parts, fmt.Sprintf("react-native project %s vs host %s", compatRNVersionMismatch.ProjectVersion, compatRNVersionMismatch.HostVersion))
					}
					if compatReactVersionMismatch != nil {
						parts = append(parts, fmt.Sprintf("react project %s vs host %s", compatReactVersionMismatch.ProjectVersion, compatReactVersionMismatch.HostVersion))
					}
					errMsg = fmt.Sprintf(
						"Blocked native Hermes load: framework/runtime drift between guest and Yaver host: %s",
						strings.Join(parts, ", "),
					)
					title = "Runtime family mismatch"
					userMsg = "The bundle compiled, but Yaver blocked restart because the guest app does not match the selected mobile host runtime family."
					helpHint = "Use one of Yaver's supported host runtime families, or align the project's Expo, React Native, and React versions to the nearest family before retrying."
					if report.RuntimeFamily != nil {
						errMsg = fmt.Sprintf("%s. Closest host family: %s. Host supports: %s",
							errMsg, report.RuntimeFamily.Selected.Label, report.RuntimeFamily.SupportedHint)
						helpHint = fmt.Sprintf("Closest host family: %s. Host supports: %s",
							report.RuntimeFamily.Selected.Label, report.RuntimeFamily.SupportedHint)
					}
				}
				incident := s.appendDevIncident("build", ReasonBuildNativeFailed, title, userMsg, errMsg, helpHint, workDir, target.DeviceID, req.Platform, IncidentSeverityError, true, true, []string{"stream:dev-events"}, payload, buildOp.ID)
				s.upsertDevOperation("build_native", "failed", "error", incident.UserMessage, workDir, target.DeviceID, 1, map[string]interface{}{
					"platform":                      req.Platform,
					"incompatibleNativeModules":     compatIncompatible,
					"matchedNativeModules":          compatMatched,
					"ignoredNativeModules":          compatIgnored,
					"nativeModuleVersionMismatches": compatVersionMismatches,
					"expoVersionMismatch":           compatExpoVersionMismatch,
					"reactNativeVersionMismatch":    compatRNVersionMismatch,
					"reactVersionMismatch":          compatReactVersionMismatch,
					"blocked":                       true,
					"guestRuntime":                  report.GuestRuntime,
					"runtimeFamilySelection":        report.RuntimeFamily,
				}, incident.ID)
				jsonReply(w, http.StatusConflict, map[string]interface{}{
					"status":                        "blocked",
					"code":                          respCode,
					"error":                         errMsg,
					"helpHint":                      helpHint,
					"platform":                      req.Platform,
					"moduleName":                    moduleName,
					"incompatibleNativeModules":     compatIncompatible,
					"matchedNativeModules":          compatMatched,
					"ignoredNativeModules":          compatIgnored,
					"nativeModuleVersionMismatches": compatVersionMismatches,
					"expoVersionMismatch":           compatExpoVersionMismatch,
					"reactNativeVersionMismatch":    compatRNVersionMismatch,
					"reactVersionMismatch":          compatReactVersionMismatch,
					"hostSdkVersion":                report.HostSDKVersion,
					"hostExpoVersion":               report.HostExpo,
					"hostReactNative":               report.HostRN,
					"hostReactVersion":              report.HostReact,
					"supportedRNRange":              report.SupportedRNRange,
					"guestRuntime":                  report.GuestRuntime,
					"runtimeFamilySelection":        report.RuntimeFamily,
					"hostRuntimeFamilies":           meta.HostRuntimeFamilies,
					"bcVersion":                     meta.HermesBCVersion,
					"md5":                           meta.MD5,
					"size":                          meta.Size,
				})
				return
			}
		}
	} else {
		log.Printf("[super-host] native-module compat probe skipped: no report")
	}

	// ── Check for assets ──
	assetsDir = filepath.Join(buildDir, "assets")
	hasAssets := false
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(assetsDir)
		hasAssets = len(entries) > 0
	}
	log.Printf("[super-host] hasAssets=%v", hasAssets)

	s.devServerMgr.SetBundleMetadata(meta.JSON())
	buildID := fmt.Sprintf("%s-%d", meta.MD5, time.Now().UTC().UnixNano())
	// C-4: mint signed URLs for the bundle + assets. The native bundle
	// fetch happens from the phone over the relay so we cannot rely on
	// owner-bearer auth — but we can require the URL itself to carry an
	// HMAC tied to (buildID, kind, exp). 30 min covers a slow phone
	// + retry pattern; the buildID rotates on every rebuild so a
	// captured URL for build N is dead the moment build N+1 ships.
	bundleSig, sigErr := signDevBundleURL(buildID, "native", 30*time.Minute)
	if sigErr != nil {
		http.Error(w, "failed to sign bundle URL: "+sigErr.Error(), http.StatusInternalServerError)
		return
	}
	assetsSig, _ := signDevBundleURL(buildID, "assets", 30*time.Minute)
	bundleURL := "/dev/native-bundle?" + bundleSig
	assetsURL := "/dev/native-assets?" + assetsSig
	s.devServerMgr.SetNativeBundleInfo(NativeBundleInfo{
		BuildID:      buildID,
		WorkDir:      workDir,
		BuildDir:     buildDir,
		BundlePath:   bundlePath,
		AssetsDir:    assetsDir,
		Platform:     req.Platform,
		ModuleName:   moduleName,
		BuiltAt:      time.Now().UTC().Format(time.RFC3339),
		MetadataJSON: meta.JSON(),
	})
	if meta.RuntimeFamilySelection != nil {
		selectedLabel := strings.TrimSpace(meta.RuntimeFamilySelection.Selected.Label)
		if selectedLabel == "" {
			selectedLabel = meta.RuntimeFamilySelection.Selected.ID
		}
		s.emitBuildProgress("Bundle ready for host runtime: "+selectedLabel, "ready")
	}
	s.emitBuildProgress(fmt.Sprintf("Bundle ready: %d KB, BC%d, module: %s",
		meta.Size/1024, meta.HermesBCVersion, moduleName), "ready")
	s.upsertDevOperation("build_native", "completed", "ready", fmt.Sprintf("Bundle ready: %d KB, BC%d, module: %s", meta.Size/1024, meta.HermesBCVersion, moduleName), workDir, target.DeviceID, phaseProgress("done"), map[string]interface{}{
		"platform":    req.Platform,
		"moduleName":  moduleName,
		"size":        meta.Size,
		"bcVersion":   meta.HermesBCVersion,
		"hasAssets":   hasAssets,
		"builderOS":   runtime.GOOS,
		"builderArch": runtime.GOARCH,
		"reactNative": info.ReactNativeVersion,
		"expoSDK":     info.ExpoSDKVersion,
		"hermesRef":   info.HermesRef,
	})
	// Snapshot git state at build time so the next request can decide
	// whether the cache is reusable. checkGitStateBuildCache (gated on
	// YAVER_BUILD_CACHE=1) reads these back to skip Metro+hermesc when
	// nothing the bundle would pick up has changed.
	gitSHA, _ := runGit(workDir, "rev-parse", "HEAD")
	porcelain, _ := runGit(workDir, "status", "--porcelain")
	gitDirty := strings.TrimSpace(porcelain) != ""
	dirtySHA := ""
	if gitDirty {
		dirtySHA = hashDirtyBundleFiles(workDir, porcelain)
	}

	writeNativeBuildStatus(workDir, nativeBuildStatus{
		State:                  "ready",
		Platform:               req.Platform,
		LastBuiltAt:            time.Now().UTC().Format(time.RFC3339),
		BundleSize:             meta.Size,
		BundlePath:             bundlePath,
		ModuleName:             moduleName,
		HermesVersion:          meta.HermesBCVersion,
		ConsumerKey:            nativeBuildConsumerKey(consumer),
		ConsumerLabel:          nativeBuildConsumerLabel(consumer),
		LastBuiltGitSHA:        gitSHA,
		LastBuiltSourceTreeSHA: dirtySHA,
		LastBuiltGitHasDirty:   gitDirty,
	})

	// Capture the just-built project so subsequent /vibing/execute calls
	// from inside the loaded guest (which only know `prompt` in 1.18.34
	// — projectName/bundleId aren't passed) can fall back to "the
	// project we just pushed to your phone." See
	// resolveVibingProjectForRequest's last-resort branch.
	s.lastNativeBundleMu.Lock()
	s.lastNativeBundleProjectPath = workDir
	s.lastNativeBundleProjectName = strings.TrimSpace(req.ProjectName)
	if s.lastNativeBundleProjectName == "" {
		s.lastNativeBundleProjectName = DetectProjectInfo(workDir).Name
	}
	s.lastNativeBundleMu.Unlock()

	resp := map[string]interface{}{
		"status":                        "ok",
		"bundleUrl":                     bundleURL,
		"assetsUrl":                     assetsURL,
		"buildId":                       buildID,
		"size":                          meta.Size,
		"md5":                           meta.MD5,
		"bcVersion":                     meta.HermesBCVersion,
		"platform":                      req.Platform,
		"moduleName":                    moduleName,
		"hasAssets":                     hasAssets,
		"cacheValid":                    cacheDecision.Valid,
		"cacheMessage":                  cacheDecision.Message,
		"hostSdkVersion":                meta.HostSDKVersion,
		"hostExpoVersion":               meta.HostExpoVersion,
		"hostReactNative":               compatHostRN,
		"hostReactVersion":              meta.HostReactVersion,
		"supportedRNRange":              meta.SupportedRNRange,
		"guestRuntime":                  meta.GuestRuntime,
		"runtimeFamilySelection":        meta.RuntimeFamilySelection,
		"hostRuntimeFamilies":           meta.HostRuntimeFamilies,
		"incompatibleNativeModules":     compatIncompatible,
		"matchedNativeModules":          compatMatched,
		"ignoredNativeModules":          compatIgnored,
		"nativeModuleVersionMismatches": compatVersionMismatches,
		"expoVersionMismatch":           compatExpoVersionMismatch,
		"reactNativeVersionMismatch":    compatRNVersionMismatch,
		"reactVersionMismatch":          compatReactVersionMismatch,
		"allowUnsafeNativeModules":      req.AllowUnsafeNativeModules,
	}
	jsonReply(w, http.StatusOK, resp)

	// Persist the response next to the bundle so a future cache hit
	// can re-emit the same payload (md5, runtime family, host versions,
	// etc.) without re-running hermesc. Best-effort — failure here
	// only means the next build won't hit the cache, which we'd recover
	// from on the build after that.
	respJSON, jerr := json.Marshal(resp)
	if jerr != nil {
		log.Printf("[build-cache] marshal response for sidecar failed: %v", jerr)
	} else if err := writeNativeBuildMetaSidecar(buildDir, nativeBuildMetaSidecar{
		Response:     respJSON,
		MetadataJSON: meta.JSON(),
		BundlePath:   bundlePath,
		ModuleName:   moduleName,
		Platform:     req.Platform,
		HasAssets:    hasAssets,
		BundleSize:   meta.Size,
		MD5:          meta.MD5,
		BuiltAt:      time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		log.Printf("[build-cache] writeNativeBuildMetaSidecar failed (cache hits will miss until next build): %v", err)
	}
}

func hermescErrString(err error) string {
	if err == nil {
		return "hermes compiler was not found"
	}
	return err.Error()
}

// handleServeNativeBundle serves the compiled native bundle file.
// GET /dev/native-bundle?build=<id>&exp=<unix>&sig=<hex>
//
// C-4 fix: previously unauth — anyone scanning a public IP while a
// dev session was active could pull the compiled Hermes bundle (full
// transpiled source). Now the URL must carry an HMAC signature minted
// by the owner via signDevBundleURL, scoped to a specific build ID
// and expiring within minutes. The same agent-local secret used for
// /blobs/public is reused; a new secret would force a new persisted
// file on disk and the threat profile is identical.
func (s *HTTPServer) handleServeNativeBundle(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		http.Error(w, "no dev server", http.StatusServiceUnavailable)
		return
	}

	build := strings.TrimSpace(r.URL.Query().Get("build"))
	if err := verifyDevBundleSig(build, "native", r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	info := s.devServerMgr.GetNativeBundleInfo(build)
	bundlePath := info.BundlePath
	if bundlePath == "" {
		status := s.devServerMgr.Status()
		if status != nil && status.WorkDir != "" {
			bundlePath = filepath.Join(status.WorkDir, ".yaver-build", "main.jsbundle")
		}
	}
	if bundlePath == "" {
		http.Error(w, "no native bundle — call POST /dev/build-native first", http.StatusNotFound)
		return
	}
	if _, err := os.Stat(bundlePath); err != nil {
		http.Error(w, "no native bundle — call POST /dev/build-native first", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=main.jsbundle")

	// Attach bundle metadata header for integrity validation on phone
	if metaJSON := info.MetadataJSON; metaJSON != "" {
		w.Header().Set("X-Yaver-Bundle-Metadata", metaJSON)
	} else if s.devServerMgr != nil {
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
// GET /dev/native-assets?build=<id>&exp=<unix>&sig=<hex>
//
// Same C-4 protection as handleServeNativeBundle: the bundle's sibling
// assets must not leak to unauthenticated scanners either.
func (s *HTTPServer) handleServeNativeAssets(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		http.Error(w, "no dev server", http.StatusServiceUnavailable)
		return
	}

	build := strings.TrimSpace(r.URL.Query().Get("build"))
	if err := verifyDevBundleSig(build, "assets", r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	info := s.devServerMgr.GetNativeBundleInfo(build)
	assetsDir := info.AssetsDir
	if assetsDir == "" {
		status := s.devServerMgr.Status()
		if status != nil && status.WorkDir != "" {
			assetsDir = filepath.Join(status.WorkDir, ".yaver-build", "assets")
		}
	}
	if assetsDir == "" {
		http.Error(w, "no assets — call POST /dev/build-native first", http.StatusNotFound)
		return
	}
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
		ProjectName      string   `json:"projectName,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	workDir := strings.TrimSpace(req.WorkDir)
	if guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")); guestUID != "" {
		resolved, err := s.guestResolveDevWorkDir(r, req.ProjectName, "")
		if err != nil {
			jsonReply(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		workDir = resolved
	}
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
		"@expo/metro-runtime", "expo-router", "expo-splash-screen", "expo-status-bar",
		"yaver-feedback-react-native",
		"three", "@react-three/fiber", "@react-three/drei":
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
