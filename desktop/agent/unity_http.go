package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type unityBuildManifest struct {
	ExecutablePath string `json:"executablePath"`
	OutputPath     string `json:"outputPath"`
	BuildTarget    string `json:"buildTarget"`
	ExecuteMethod  string `json:"executeMethod"`
}

type unityRunResponse struct {
	OK             bool     `json:"ok"`
	Status         string   `json:"status,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	ProjectPath    string   `json:"projectPath,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	BuildTarget    string   `json:"buildTarget,omitempty"`
	ExecuteMethod  string   `json:"executeMethod,omitempty"`
	OutputPath     string   `json:"outputPath,omitempty"`
	ExecutablePath string   `json:"executablePath,omitempty"`
	LogPath        string   `json:"logPath,omitempty"`
	ResultsPath    string   `json:"resultsPath,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	Artifacts      []string `json:"artifacts,omitempty"`
	NextAction     string   `json:"nextAction,omitempty"`
	Command        []string `json:"command,omitempty"`
}

var unityLaunchRegistry struct {
	mu       sync.Mutex
	byBinary map[string]int
}

var unityRunHistory struct {
	mu    sync.Mutex
	items []unityRunResponse
}

func (s *HTTPServer) handleUnityTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	if s.isolatedGuestDevMutationBlocked(w, r, "unity tests") {
		return
	}
	var req struct {
		ProjectName string `json:"projectName"`
		ProjectPath string `json:"projectPath"`
		TestMode    string `json:"testMode"`
		TestFilter  string `json:"testFilter"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	projectPath, err := s.resolveUnityProjectPath(r, req.ProjectName, req.ProjectPath)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	unityPath, _ := detectUnityEditor()
	if unityPath == "" {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "Unity Editor not detected on this machine"})
		return
	}
	testMode := normalizeUnityTestMode(req.TestMode)
	buildDir := filepath.Join(projectPath, ".yaver-build", "unity")
	_ = os.MkdirAll(buildDir, 0o755)
	logPath := filepath.Join(buildDir, "unity-tests.log")
	resultsPath := filepath.Join(buildDir, "unity-tests.xml")

	args := []string{
		"-batchmode",
		"-accept-apiupdate",
		"-projectPath", projectPath,
		"-runTests",
		"-testPlatform", testMode,
		"-testResults", resultsPath,
		"-logFile", logPath,
		"-quit",
	}
	if filter := strings.TrimSpace(req.TestFilter); filter != "" {
		args = append(args, "-testFilter", filter)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, unityPath, args...)
	logW := &devLogWriter{prefix: "[unity:test]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	cmd.Stdout = logW
	cmd.Stderr = logW
	err = cmd.Run()
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, unityRunResponse{
			OK:          false,
			Status:      "failed",
			Stage:       "test",
			ProjectPath: projectPath,
			Mode:        testMode,
			LogPath:     logPath,
			ResultsPath: resultsPath,
			Summary:     fmt.Sprintf("Unity %s tests failed: %v", testMode, err),
			Artifacts:   compactArtifacts(logPath, resultsPath),
			NextAction:  "Review the Unity test log and fix the failing tests before rebuilding.",
			Command:     append([]string{unityPath}, args...),
		})
		recordUnityRun(unityRunResponse{
			OK:          false,
			Status:      "failed",
			Stage:       "test",
			ProjectPath: projectPath,
			Mode:        testMode,
			LogPath:     logPath,
			ResultsPath: resultsPath,
			Summary:     fmt.Sprintf("Unity %s tests failed: %v", testMode, err),
			Artifacts:   compactArtifacts(logPath, resultsPath),
			NextAction:  "Review the Unity test log and fix the failing tests before rebuilding.",
			Command:     append([]string{unityPath}, args...),
		})
		return
	}

	resp := unityRunResponse{
		OK:          true,
		Status:      "passed",
		Stage:       "test",
		ProjectPath: projectPath,
		Mode:        testMode,
		LogPath:     logPath,
		ResultsPath: resultsPath,
		Summary:     fmt.Sprintf("Unity %s tests completed.", testMode),
		Artifacts:   compactArtifacts(logPath, resultsPath),
		NextAction:  "Proceed to a build or relaunch if the objective requires runtime verification.",
		Command:     append([]string{unityPath}, args...),
	}
	recordUnityRun(resp)
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleUnityBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	if s.isolatedGuestDevMutationBlocked(w, r, "unity build") {
		return
	}
	var req struct {
		ProjectName   string   `json:"projectName"`
		ProjectPath   string   `json:"projectPath"`
		BuildTarget   string   `json:"buildTarget"`
		ExecuteMethod string   `json:"executeMethod"`
		OutputPath    string   `json:"outputPath"`
		Args          []string `json:"args"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	resp, status, err := s.runUnityBuild(r, req.ProjectName, req.ProjectPath, req.BuildTarget, req.ExecuteMethod, req.OutputPath, req.Args)
	if err != nil {
		jsonReply(w, status, map[string]string{"error": err.Error()})
		return
	}
	recordUnityRun(resp)
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleUnityRelaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	if s.isolatedGuestDevMutationBlocked(w, r, "unity relaunch") {
		return
	}
	var req struct {
		ProjectName       string   `json:"projectName"`
		ProjectPath       string   `json:"projectPath"`
		ExecutablePath    string   `json:"executablePath"`
		Args              []string `json:"args"`
		BuildBeforeLaunch bool     `json:"buildBeforeLaunch"`
		BuildTarget       string   `json:"buildTarget"`
		ExecuteMethod     string   `json:"executeMethod"`
		OutputPath        string   `json:"outputPath"`
		BuildArgs         []string `json:"buildArgs"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	projectPath, err := s.resolveUnityProjectPath(r, req.ProjectName, req.ProjectPath)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.BuildBeforeLaunch {
		if _, _, err := s.runUnityBuild(r, req.ProjectName, projectPath, req.BuildTarget, req.ExecuteMethod, req.OutputPath, req.BuildArgs); err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	executablePath := strings.TrimSpace(req.ExecutablePath)
	if executablePath == "" && strings.TrimSpace(req.OutputPath) != "" {
		executablePath = strings.TrimSpace(req.OutputPath)
	}
	if executablePath == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "executablePath or outputPath required for relaunch"})
		return
	}
	if !filepath.IsAbs(executablePath) {
		executablePath = filepath.Join(projectPath, executablePath)
	}
	if !pathExists(executablePath) {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("executable not found: %s", executablePath)})
		return
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(executablePath, 0o755)
	}
	stopTrackedUnityProcess(executablePath)
	cmd := exec.Command(executablePath, req.Args...)
	cmd.Dir = filepath.Dir(executablePath)
	if err := cmd.Start(); err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("launch failed: %v", err)})
		return
	}
	trackUnityProcess(executablePath, cmd.Process.Pid)
	resp := unityRunResponse{
		OK:             true,
		Status:         "passed",
		Stage:          "relaunch",
		ProjectPath:    projectPath,
		ExecutablePath: executablePath,
		Summary:        fmt.Sprintf("Launched Unity player (%d).", cmd.Process.Pid),
		Artifacts:      compactArtifacts(executablePath),
		NextAction:     "Verify the running player and capture feedback, screenshots, or smoke-run results.",
		Command:        append([]string{executablePath}, req.Args...),
	}
	recordUnityRun(resp)
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleUnityRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}
	jsonReply(w, http.StatusOK, listUnityRuns())
}

func (s *HTTPServer) runUnityBuild(r *http.Request, projectName, projectPath, buildTarget, executeMethod, outputPath string, extraArgs []string) (unityRunResponse, int, error) {
	projectPath, err := s.resolveUnityProjectPath(r, projectName, projectPath)
	if err != nil {
		return unityRunResponse{}, http.StatusBadRequest, err
	}
	unityPath, _ := detectUnityEditor()
	if unityPath == "" {
		return unityRunResponse{}, http.StatusServiceUnavailable, fmt.Errorf("Unity Editor not detected on this machine")
	}
	if strings.TrimSpace(executeMethod) == "" {
		return unityRunResponse{}, http.StatusBadRequest, fmt.Errorf("executeMethod required for Unity builds")
	}
	buildDir := filepath.Join(projectPath, ".yaver-build", "unity")
	_ = os.MkdirAll(buildDir, 0o755)
	logPath := filepath.Join(buildDir, "unity-build.log")
	manifestPath := filepath.Join(buildDir, "unity-build-result.json")
	if strings.TrimSpace(outputPath) == "" {
		outputPath = filepath.Join(buildDir, "build")
	}
	args := []string{
		"-batchmode",
		"-accept-apiupdate",
		"-projectPath", projectPath,
		"-quit",
		"-logFile", logPath,
	}
	if target := strings.TrimSpace(buildTarget); target != "" {
		args = append(args, "-buildTarget", target)
	}
	args = append(args, "-executeMethod", executeMethod)
	if trimmed := strings.TrimSpace(outputPath); trimmed != "" {
		args = append(args, "-yaverBuildOutput", trimmed)
	}
	args = append(args, "-yaverBuildManifest", manifestPath)
	args = append(args, extraArgs...)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, unityPath, args...)
	logW := &devLogWriter{prefix: "[unity:build]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	cmd.Stdout = logW
	cmd.Stderr = logW
	if err := cmd.Run(); err != nil {
		return unityRunResponse{}, http.StatusInternalServerError, fmt.Errorf("Unity build failed: %v", err)
	}
	manifest := readUnityBuildManifest(manifestPath)
	resolvedOutputPath := resolveUnityOutputPath(outputPath, manifest)
	resolvedExecutablePath := resolveUnityExecutablePath(projectPath, outputPath, manifest)
	return unityRunResponse{
		OK:             true,
		Status:         "passed",
		Stage:          "build",
		ProjectPath:    projectPath,
		BuildTarget:    strings.TrimSpace(buildTarget),
		ExecuteMethod:  executeMethod,
		OutputPath:     resolvedOutputPath,
		ExecutablePath: resolvedExecutablePath,
		LogPath:        logPath,
		Summary:        "Unity build completed.",
		Artifacts:      compactArtifacts(logPath, manifestPath, resolvedOutputPath, resolvedExecutablePath),
		NextAction:     nextUnityBuildAction(resolvedExecutablePath),
		Command:        append([]string{unityPath}, args...),
	}, http.StatusOK, nil
}

func compactArtifacts(paths ...string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func recordUnityRun(item unityRunResponse) {
	unityRunHistory.mu.Lock()
	defer unityRunHistory.mu.Unlock()
	unityRunHistory.items = append([]unityRunResponse{item}, unityRunHistory.items...)
	if len(unityRunHistory.items) > 50 {
		unityRunHistory.items = unityRunHistory.items[:50]
	}
}

func listUnityRuns() []unityRunResponse {
	unityRunHistory.mu.Lock()
	defer unityRunHistory.mu.Unlock()
	out := make([]unityRunResponse, len(unityRunHistory.items))
	copy(out, unityRunHistory.items)
	return out
}

func readUnityBuildManifest(path string) *unityBuildManifest {
	if strings.TrimSpace(path) == "" || !pathExists(path) {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var manifest unityBuildManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil
	}
	return &manifest
}

func resolveUnityOutputPath(outputPath string, manifest *unityBuildManifest) string {
	if manifest != nil && strings.TrimSpace(manifest.OutputPath) != "" {
		return strings.TrimSpace(manifest.OutputPath)
	}
	return strings.TrimSpace(outputPath)
}

func resolveUnityExecutablePath(projectPath, outputPath string, manifest *unityBuildManifest) string {
	if manifest != nil && strings.TrimSpace(manifest.ExecutablePath) != "" {
		path := strings.TrimSpace(manifest.ExecutablePath)
		if filepath.IsAbs(path) {
			return path
		}
		if strings.TrimSpace(projectPath) != "" {
			return filepath.Join(projectPath, path)
		}
		return path
	}
	trimmed := strings.TrimSpace(outputPath)
	if trimmed == "" {
		return ""
	}
	if filepath.Ext(trimmed) != "" || strings.HasSuffix(trimmed, ".app") {
		if filepath.IsAbs(trimmed) {
			return trimmed
		}
		if strings.TrimSpace(projectPath) != "" {
			return filepath.Join(projectPath, trimmed)
		}
		return trimmed
	}
	return ""
}

func nextUnityBuildAction(executablePath string) string {
	if strings.TrimSpace(executablePath) != "" {
		return "Relaunch the built player or inspect the build output on disk."
	}
	return "Inspect the build output on disk and set UnityDesktopExecutablePath for relaunch support."
}

func (s *HTTPServer) resolveUnityProjectPath(r *http.Request, projectName, fallbackPath string) (string, error) {
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID != "" {
		resolved, err := s.guestResolveDevWorkDir(r, projectName, fallbackPath)
		if err != nil {
			return "", err
		}
		if !hasUnityProjectFiles(resolved) {
			return "", fmt.Errorf("%s is not a Unity project", resolved)
		}
		return resolved, nil
	}
	if strings.TrimSpace(fallbackPath) != "" {
		if !hasUnityProjectFiles(fallbackPath) {
			return "", fmt.Errorf("%s is not a Unity project", fallbackPath)
		}
		return fallbackPath, nil
	}
	if mp := findMobileProjectByName(projectName); mp != nil && mp.Framework == "unity" && strings.TrimSpace(mp.Path) != "" {
		return mp.Path, nil
	}
	return "", fmt.Errorf("no Unity project resolved — pass projectPath or a detected Unity projectName")
}

func normalizeUnityTestMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "edit", "editor", "editmode":
		return "EditMode"
	case "play", "playmode":
		return "PlayMode"
	case "all":
		return "All"
	default:
		return "EditMode"
	}
}

func trackUnityProcess(executablePath string, pid int) {
	unityLaunchRegistry.mu.Lock()
	defer unityLaunchRegistry.mu.Unlock()
	if unityLaunchRegistry.byBinary == nil {
		unityLaunchRegistry.byBinary = map[string]int{}
	}
	unityLaunchRegistry.byBinary[filepath.Clean(executablePath)] = pid
}

func stopTrackedUnityProcess(executablePath string) {
	unityLaunchRegistry.mu.Lock()
	defer unityLaunchRegistry.mu.Unlock()
	if unityLaunchRegistry.byBinary == nil {
		return
	}
	key := filepath.Clean(executablePath)
	pid, ok := unityLaunchRegistry.byBinary[key]
	if !ok || pid <= 0 {
		return
	}
	if proc, err := os.FindProcess(pid); err == nil && proc != nil {
		if runtime.GOOS == "windows" {
			_ = proc.Kill()
		} else {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	delete(unityLaunchRegistry.byBinary, key)
}
