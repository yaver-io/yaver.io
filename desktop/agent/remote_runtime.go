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
	// RemoteBuilders is the list of paired builders the dashboard
	// can dispatch to. Populated for Swift / iOS sessions on
	// non-darwin hosts; empty everywhere else. Each entry is the
	// public-safe subset of the on-disk registry (no token).
	RemoteBuilders []RemoteBuilderSummary `json:"remoteBuilders,omitempty"`
}

// RemoteBuilderSummary is the public-safe view of a paired builder.
// Tokens, hostnames, and any other infra-sensitive field stay on
// disk per the privacy contract (see CLAUDE.md `convex_privacy_test`
// forbidden keys: `remoteBuilderHostname`, `remoteBuilderTunnelToken`).
type RemoteBuilderSummary struct {
	Alias     string   `json:"alias"`
	URL       string   `json:"url"`
	Platforms []string `json:"platforms"`
	Default   bool     `json:"default,omitempty"`
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
	// RemoteBuilderId is the alias (NOT the URL or token) of the
	// builder this session is dispatched to. Set when a Linux dev
	// box forwards a Swift session to a paired Mac via the Phase-5
	// proxy. Empty for local sessions. URL + token are private to
	// the agent's on-disk registry and never appear in any payload
	// returned by the agent.
	RemoteBuilderId string `json:"remoteBuilderId,omitempty"`
	// DeviceDims carries the booted device's logical resolution +
	// rotation so the web viewer can scale pointer coordinates back
	// to device space. Populated on Attach by ProbeDeviceDims; updated
	// whenever a rotation event fires. Pointer-typed because not every
	// session exposes dims (relay-jpeg-poll mode doesn't need them).
	DeviceDims *DeviceDims `json:"deviceDims,omitempty"`
}

type RemoteRuntimeManager struct {
	mu       sync.RWMutex
	sessions map[string]RemoteRuntimeSession
	live     map[string]*remoteRuntimeLiveState
	// proxied maps a local session ID to the dispatch record for a
	// session served by a paired Mac builder. Phase-5 closer: HTTP
	// handlers consult this before touching the local manager and
	// forward when a mapping exists. Local-only sessions stay nil.
	proxied map[string]*proxiedSession
}

func NewRemoteRuntimeManager() *RemoteRuntimeManager {
	return &RemoteRuntimeManager{
		sessions: map[string]RemoteRuntimeSession{},
		live:     map[string]*remoteRuntimeLiveState{},
		proxied:  map[string]*proxiedSession{},
	}
}

func executionModeForFramework(framework string) ProjectExecutionMode {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		// Hermes hot-reload only — never WebRTC. RN apps load as guest
		// bundles into the Yaver mobile super-host. See
		// docs/native-webrtc-web-streaming.md §13.
		return ExecutionModeRNHermes
	case "next", "nextjs", "vite", "react":
		return ExecutionModeWebWebview
	case "swift", "kotlin", "flutter":
		// Flutter joins Swift + Kotlin in the WebRTC family because
		// it doesn't fit the Hermes guest-bundle model — its UI runs
		// on Skia/Impeller in its own process. The web dashboard's
		// RemoteRuntimeViewer streams the running emulator/simulator.
		return ExecutionModeNativeWebRTC
	case "browser":
		// "PC UI in glasses" surface — a headless Chromium tab on the
		// agent host streamed to a spatial headset / web client over
		// the same WebRTC pipeline that ships the native simulators.
		// The target is browser-window; capture is JPEG-DC for now.
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
			// Simulator first (default on a Mac); physical iPhone
			// second (real-hardware fidelity, or the only iOS path
			// when no sim is usable). Capability-probed.
			caps.Targets = []RemoteRuntimeTarget{
				probeIOSSimulatorTarget(),
				probeIOSDeviceTarget(),
			}
		case "kotlin":
			// Emulator first (default where the host can run it),
			// physical device second (the only path on a host with no
			// emulator binary — e.g. linux/arm64). Capability-probed,
			// never host-name-gated.
			caps.Targets = []RemoteRuntimeTarget{
				probeAndroidEmulatorTarget(),
				probeAndroidDeviceTarget(),
			}
		case "flutter":
			// Flutter projects compile to the same booted simulators
			// or emulators as their native counterparts. Expose every
			// surface so the user can pick — `flutter build apk` for
			// the Android side, `flutter build ios` for iOS. The
			// session's build dispatch is identical to native; only
			// the build command differs (handled in native_build.go).
			// android-device is the fallback when no local emulator
			// binary exists (linux/arm64); sim/emu stay first-class
			// wherever the host supports them.
			caps.Targets = []RemoteRuntimeTarget{
				probeAndroidEmulatorTarget(),
				probeAndroidDeviceTarget(),
				probeIOSSimulatorTarget(),
				probeIOSDeviceTarget(),
			}
		case "browser":
			// One target: a headless Chromium tab on the agent host.
			// Same JPEG-DC transport as android/ios. Useful entry
			// points (Gmail tab, docs, generic URL) are layered on
			// top by ops_glass_pc.go verbs.
			caps.Targets = []RemoteRuntimeTarget{
				probeBrowserWindowTarget(),
			}
		}
	}

	// Surface paired builders for any framework whose iOS target
	// can't run locally (i.e. anything Swift / Flutter on a
	// non-darwin host). The dashboard uses this to show "Open via
	// mac-rack-1" instead of the generic disabled-target message.
	caps.RemoteBuilders = collectIOSBuilderSummaries()
	return caps
}

// collectIOSBuilderSummaries reads the local registry and returns
// the iOS-capable builders. Errors are swallowed: a missing or
// corrupt file means "no builders paired", which is the right
// thing to advertise.
func collectIOSBuilderSummaries() []RemoteBuilderSummary {
	reg, err := LoadBuilders()
	if err != nil || reg == nil {
		return nil
	}
	var out []RemoteBuilderSummary
	for _, alias := range reg.SortedAliases() {
		entry := reg.Builders[alias]
		if !platformsContain(entry.Platforms, "ios") {
			continue
		}
		out = append(out, RemoteBuilderSummary{
			Alias:     entry.Alias,
			URL:       entry.URL,
			Platforms: entry.Platforms,
			Default:   reg.Default == entry.Alias,
		})
	}
	return out
}

func platformsContain(list []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, p := range list {
		if strings.ToLower(strings.TrimSpace(p)) == want {
			return true
		}
	}
	return false
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
		if !androidEmulatorHostSupported() {
			target.Reason = "Google ships no Android emulator binary for " +
				runtime.GOOS + "/" + runtime.GOARCH + ". Stream from a physical " +
				"device (`yaver wire`) or a macOS / x86-64-Linux host."
		} else {
			target.Reason = "Android emulator binary not found. Run `yaver install remote-runtime`."
		}
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
	id = strings.TrimSpace(id)
	delete(m.sessions, id)
	delete(m.live, id)
	delete(m.proxied, id)
}

func (m *RemoteRuntimeManager) Create(workDir, framework, targetID, transportMode string) (RemoteRuntimeSession, error) {
	// Phase-5 closer: when this host can't run the requested target
	// natively (e.g. Linux + Swift/iOS) and a paired Mac builder is
	// configured, dispatch the create call to the builder. Every
	// follow-up HTTP call (offer / control / frame / delete) is
	// forwarded by `proxiedFor()` checks at the handler level. RTP
	// media flows direct viewer↔builder once SDP is exchanged — the
	// Linux box never decodes or re-encodes a single byte.
	if entry, _ := pickBuilderForFramework(framework, targetID); entry != nil {
		return m.dispatchCreateToBuilder(*entry, workDir, framework, targetID, transportMode)
	}

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
		// Proxied sessions are already booted on the builder — the
		// Mac handles its own simctl boot + dims probe. The local
		// Attach() would fail trying to look up live state we don't
		// keep for proxied IDs.
		if proxy := mgr.proxiedFor(session.ID); proxy == nil {
			session, err = mgr.Attach(session.ID)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
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
