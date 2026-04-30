package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProjectExecutionMode string

const (
	ExecutionModeRNHermes     ProjectExecutionMode = "rn-hermes"
	ExecutionModeWebWebview   ProjectExecutionMode = "web-webview"
	ExecutionModeNativeWebRTC ProjectExecutionMode = "native-webrtc"
	ExecutionModeUnsupported  ProjectExecutionMode = "unsupported"
)

type RemoteRuntimeTarget struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Platform         string `json:"platform"`
	RuntimeHostClass string `json:"runtimeHostClass,omitempty"`
	Enabled          bool   `json:"enabled"`
	Reason           string `json:"reason,omitempty"`
	HostOS           string `json:"hostOs,omitempty"`
	RequiredCLI      string `json:"requiredCli,omitempty"`
}

type RemoteRuntimeCapabilities struct {
	WorkDir                 string                `json:"workDir"`
	Framework               string                `json:"framework"`
	ExecutionMode           ProjectExecutionMode  `json:"executionMode"`
	PrimarySurface          string                `json:"primarySurface"`
	RemoteRuntimeEligible   bool                  `json:"remoteRuntimeEligible"`
	FeedbackSDKCompatible   bool                  `json:"feedbackSdkCompatible"`
	FeedbackSDKNote         string                `json:"feedbackSdkNote,omitempty"`
	FeedbackControlProtocol string                `json:"feedbackControlProtocol,omitempty"`
	SupportedTransports     []string              `json:"supportedTransports,omitempty"`
	CurrentHostClass        string                `json:"currentHostClass,omitempty"`
	Targets                 []RemoteRuntimeTarget `json:"targets"`
}

type RemoteRuntimeSession struct {
	ID               string               `json:"id"`
	WorkDir          string               `json:"workDir"`
	Framework        string               `json:"framework"`
	ExecutionMode    ProjectExecutionMode `json:"executionMode"`
	TargetID         string               `json:"targetId"`
	TargetLabel      string               `json:"targetLabel"`
	Platform         string               `json:"platform,omitempty"`
	DeviceID         string               `json:"deviceId,omitempty"`
	RuntimeHostClass string               `json:"runtimeHostClass,omitempty"`
	TransportMode    string               `json:"transportMode,omitempty"`
	FrameTransport   string               `json:"frameTransport,omitempty"`
	Status           string               `json:"status"`
	LastCommand      string               `json:"lastCommand,omitempty"`
	CreatedAt        string               `json:"createdAt"`
	UpdatedAt        string               `json:"updatedAt"`
	Note             string               `json:"note,omitempty"`
}

type RemoteRuntimeManager struct {
	mu       sync.RWMutex
	sessions map[string]RemoteRuntimeSession
	live     map[string]*remoteRuntimeLiveState
}

func NewRemoteRuntimeManager() *RemoteRuntimeManager {
	return &RemoteRuntimeManager{
		sessions: map[string]RemoteRuntimeSession{},
		live:     map[string]*remoteRuntimeLiveState{},
	}
}

func executionModeForFramework(framework string) ProjectExecutionMode {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		return ExecutionModeRNHermes
	case "next", "nextjs", "vite", "react":
		return ExecutionModeWebWebview
	case "swift", "kotlin":
		return ExecutionModeNativeWebRTC
	default:
		return ExecutionModeUnsupported
	}
}

func primarySurfaceForFramework(framework string) string {
	switch executionModeForFramework(framework) {
	case ExecutionModeRNHermes:
		return "hermes"
	case ExecutionModeWebWebview:
		return "webview"
	case ExecutionModeNativeWebRTC:
		return "webrtc"
	default:
		return "none"
	}
}

func detectRuntimeHostClass() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos-ios"
	case "linux":
		return "linux-android"
	default:
		return runtime.GOOS
	}
}

func runtimeHostClassForAndroid() string {
	if runtime.GOOS == "darwin" {
		return "macos-android"
	}
	if runtime.GOOS == "linux" {
		return "linux-android"
	}
	return runtime.GOOS + "-android"
}

func remoteRuntimeCapabilitiesForProject(workDir, framework string) RemoteRuntimeCapabilities {
	mode := executionModeForFramework(framework)
	caps := RemoteRuntimeCapabilities{
		WorkDir:                 strings.TrimSpace(workDir),
		Framework:               strings.TrimSpace(framework),
		ExecutionMode:           mode,
		PrimarySurface:          primarySurfaceForFramework(framework),
		RemoteRuntimeEligible:   mode == ExecutionModeNativeWebRTC,
		FeedbackSDKCompatible:   mode == ExecutionModeNativeWebRTC,
		FeedbackSDKNote:         "Remote runtime is intended to coexist with Yaver Feedback SDK instrumentation in native apps; session transport and feedback transport remain separate.",
		FeedbackControlProtocol: "remote-runtime-feedback-v1",
		SupportedTransports:     []string{"direct-webrtc", "relay-jpeg-poll"},
		CurrentHostClass:        detectRuntimeHostClass(),
	}
	if !caps.RemoteRuntimeEligible {
		return caps
	}
	switch mode {
	case ExecutionModeNativeWebRTC:
		switch strings.ToLower(strings.TrimSpace(framework)) {
		case "swift":
			caps.Targets = []RemoteRuntimeTarget{probeIOSSimulatorTarget()}
		case "kotlin":
			caps.Targets = []RemoteRuntimeTarget{probeAndroidEmulatorTarget()}
		}
	}
	return caps
}

func probeIOSSimulatorTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               "ios-simulator",
		Label:            "iOS Simulator over WebRTC",
		Platform:         "ios",
		RuntimeHostClass: "macos-ios",
		HostOS:           runtime.GOOS,
		RequiredCLI:      "xcrun simctl",
	}
	if runtime.GOOS != "darwin" {
		target.Enabled = false
		target.Reason = "Requires a macOS host with Xcode installed."
		return target
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		target.Enabled = false
		target.Reason = "xcrun not found. Install Xcode command line tools or Xcode."
		return target
	}
	if out, err := exec.Command("xcode-select", "-p").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		target.Enabled = false
		target.Reason = "Xcode path unavailable. Run xcode-select or install Xcode."
		return target
	}
	target.Enabled = true
	return target
}

func probeAndroidEmulatorTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               "android-emulator",
		Label:            "Android Emulator over WebRTC",
		Platform:         "android",
		RuntimeHostClass: runtimeHostClassForAndroid(),
		HostOS:           runtime.GOOS,
		RequiredCLI:      "adb + emulator",
	}
	if findAndroidToolPath("adb") == "" {
		target.Enabled = false
		target.Reason = "adb not found. Install Android platform-tools."
		return target
	}
	if findAndroidToolPath("emulator") == "" {
		target.Enabled = false
		target.Reason = "Android emulator binary not found."
		return target
	}
	target.Enabled = true
	return target
}

func (m *RemoteRuntimeManager) List() []RemoteRuntimeSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RemoteRuntimeSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func (m *RemoteRuntimeManager) Get(id string) (RemoteRuntimeSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[strings.TrimSpace(id)]
	return session, ok
}

func (m *RemoteRuntimeManager) getLive(id string) (*remoteRuntimeLiveState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	live, ok := m.live[strings.TrimSpace(id)]
	return live, ok
}

func (m *RemoteRuntimeManager) Update(id string, mutate func(*RemoteRuntimeSession)) (RemoteRuntimeSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[strings.TrimSpace(id)]
	if !ok {
		return RemoteRuntimeSession{}, false
	}
	mutate(&session)
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.sessions[session.ID] = session
	return session, true
}

func (m *RemoteRuntimeManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, strings.TrimSpace(id))
	delete(m.live, strings.TrimSpace(id))
}

func (m *RemoteRuntimeManager) Create(workDir, framework, targetID, transportMode string) (RemoteRuntimeSession, error) {
	caps := remoteRuntimeCapabilitiesForProject(workDir, framework)
	if !caps.RemoteRuntimeEligible {
		return RemoteRuntimeSession{}, fmt.Errorf("%s projects use %s, not WebRTC remote runtime", framework, caps.PrimarySurface)
	}
	var selected *RemoteRuntimeTarget
	for i := range caps.Targets {
		if caps.Targets[i].ID == targetID {
			selected = &caps.Targets[i]
			break
		}
	}
	if selected == nil {
		return RemoteRuntimeSession{}, fmt.Errorf("unknown remote runtime target %q", targetID)
	}
	if !selected.Enabled {
		return RemoteRuntimeSession{}, fmt.Errorf("%s", selected.Reason)
	}
	transportMode = strings.TrimSpace(transportMode)
	if transportMode == "" {
		transportMode = "direct-webrtc"
	}
	validTransport := false
	for _, candidate := range caps.SupportedTransports {
		if candidate == transportMode {
			validTransport = true
			break
		}
	}
	if !validTransport {
		return RemoteRuntimeSession{}, fmt.Errorf("unsupported transport mode %q", transportMode)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	frameTransport := "webrtc-datachannel-jpeg-v1"
	note := "Remote runtime session created. Waiting for simulator or emulator attach."
	if transportMode == "relay-jpeg-poll" {
		frameTransport = "relay-jpeg-poll-v1"
		note = "Remote runtime session created in relay mode. Frames will be fetched over Yaver relay-compatible HTTP."
	}
	session := RemoteRuntimeSession{
		ID:               fmt.Sprintf("rr_%d", time.Now().UTC().UnixNano()),
		WorkDir:          strings.TrimSpace(workDir),
		Framework:        strings.TrimSpace(framework),
		ExecutionMode:    caps.ExecutionMode,
		TargetID:         selected.ID,
		TargetLabel:      selected.Label,
		Platform:         selected.Platform,
		RuntimeHostClass: selected.RuntimeHostClass,
		TransportMode:    transportMode,
		FrameTransport:   frameTransport,
		Status:           "control-ready",
		CreatedAt:        now,
		UpdatedAt:        now,
		Note:             note,
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.live[session.ID] = &remoteRuntimeLiveState{sessionID: session.ID, targetID: selected.ID, platform: selected.Platform}
	m.mu.Unlock()
	return session, nil
}

func (s *HTTPServer) ensureRemoteRuntimeManager() *RemoteRuntimeManager {
	if s.remoteRuntimeMgr == nil {
		s.remoteRuntimeMgr = NewRemoteRuntimeManager()
	}
	return s.remoteRuntimeMgr
}

func (s *HTTPServer) handleRemoteRuntimeCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	workDir := strings.TrimSpace(r.URL.Query().Get("workDir"))
	framework := strings.TrimSpace(r.URL.Query().Get("framework"))
	if workDir == "" || framework == "" {
		jsonError(w, http.StatusBadRequest, "workDir and framework are required")
		return
	}
	jsonReply(w, http.StatusOK, remoteRuntimeCapabilitiesForProject(workDir, framework))
}

func (s *HTTPServer) handleRemoteRuntimeSessions(w http.ResponseWriter, r *http.Request) {
	mgr := s.ensureRemoteRuntimeManager()
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"sessions": mgr.List()})
	case http.MethodPost:
		var req struct {
			WorkDir   string `json:"workDir"`
			Framework string `json:"framework"`
			TargetID  string `json:"targetId"`
			Transport string `json:"transportMode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		session, err := mgr.Create(req.WorkDir, req.Framework, req.TargetID, req.Transport)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		session, err = mgr.Attach(session.ID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, session)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleRemoteRuntimeSessionCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	mgr := s.ensureRemoteRuntimeManager()
	sessionID := strings.TrimPrefix(r.URL.Path, "/remote-runtime/sessions/")
	sessionID = strings.TrimSuffix(sessionID, "/command")
	sessionID = strings.Trim(sessionID, "/")
	if sessionID == "" {
		jsonError(w, http.StatusBadRequest, "missing session id")
		return
	}
	session, ok := mgr.Get(sessionID)
	if !ok {
		jsonError(w, http.StatusNotFound, "remote runtime session not found")
		return
	}
	var req struct {
		Command string `json:"command"`
		Source  string `json:"source,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		jsonError(w, http.StatusBadRequest, "missing command")
		return
	}
	switch strings.TrimSpace(req.Command) {
	case "launch-feedback":
		source := strings.TrimSpace(req.Source)
		if source == "" {
			source = "unknown"
		}
		updated, _ := mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			current.Status = "feedback-pending"
			current.LastCommand = "launch-feedback"
			current.Note = fmt.Sprintf("Feedback launch requested from %s. Wire this command to the active remote viewer once the media/control bridge lands.", source)
		})
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"sessionId": session.ID,
			"command":   "launch-feedback",
			"protocol":  "remote-runtime-feedback-v1",
			"status":    "accepted",
			"source":    source,
			"session":   updated,
			"note":      updated.Note,
		})
	default:
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("unsupported command %q", req.Command))
	}
}
