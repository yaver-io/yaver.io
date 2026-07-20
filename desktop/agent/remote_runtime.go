package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/testkit"
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
	// Surface is the n2n picker badge — phone|tablet|watch|tv|vision|browser.
	// Additive: JSON clients ignore unknown fields. Populated for every
	// Apple-runtime target (P0 fan-out). Older Android targets omit it.
	Surface string `json:"surface,omitempty"`
}

type RemoteRuntimeCapabilities struct {
	WorkDir                 string                `json:"workDir"`
	Framework               string                `json:"framework"`
	ExecutionMode           ProjectExecutionMode  `json:"executionMode"`
	PrimarySurface          string                `json:"primarySurface"`
	RemoteRuntimeEligible   bool                  `json:"remoteRuntimeEligible"`
	FeedbackSDKCompatible   bool                  `json:"feedbackSdkCompatible"`
	FeedbackSDKNote         string                `json:"feedbackSdkNote,omitempty"`
	FeedbackSurface         string                `json:"feedbackSurface,omitempty"` // "viewer-overlay" (RN sim stream) | "in-app-sdk" (native)
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

// frameworkStreamsRNViaSimulator reports whether an RN/Expo project can ALSO be
// run in a real simulator/emulator and streamed over WebRTC — the alternative to
// Hermes push. Hermes stays the PRIMARY surface for RN (fast bytecode reload into
// the Yaver container); this path exists for when you want the guest app running
// standalone in a booted simulator — e.g. to exercise native modules the Yaver
// host lacks (the expo-gl class), or to test the app's OWN Yaver Feedback SDK
// (react-native), which the Hermes container deliberately suppresses. In a real
// simulator that SDK is live, so shake-to-feedback works natively.
func frameworkStreamsRNViaSimulator(framework string) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		return true
	}
	return false
}

func remoteRuntimeCapabilitiesForProject(workDir, framework string) RemoteRuntimeCapabilities {
	mode := executionModeForFramework(framework)
	rnSim := frameworkStreamsRNViaSimulator(framework)
	// Eligibility is decoupled from the PRIMARY execution mode: a native app is
	// WebRTC-primary, while an RN app is Hermes-primary but ALSO simulator-
	// streamable. Both are remote-runtime eligible; PrimarySurface records which
	// is the default so the UI can present Hermes first and WebRTC as "also".
	eligible := mode == ExecutionModeNativeWebRTC || rnSim
	caps := RemoteRuntimeCapabilities{
		WorkDir:               strings.TrimSpace(workDir),
		Framework:             strings.TrimSpace(framework),
		ExecutionMode:         mode,
		PrimarySurface:        primarySurfaceForFramework(framework),
		RemoteRuntimeEligible: eligible,
		// The RN app in a booted simulator carries its OWN feedback SDK, which is
		// live there (unlike the suppressed-in-container Hermes path), so shake +
		// feedback work natively. For native apps the note is unchanged.
		FeedbackSDKCompatible: mode == ExecutionModeNativeWebRTC || rnSim,
		FeedbackSDKNote: func() string {
			if rnSim {
				return "Feedback flows client→server: the phone owns shake detection (it already has ShakeDetector), and in a WebRTC session a shake sends the `shake` session command to the remote box, which injects a hardware shake into the simulator (simctl for iOS, adb sensor for Android). The guest app's OWN Yaver Feedback SDK — live in the real simulator — then fires its overlay inside the sim, and that overlay streams back to the phone over the same WebRTC video. Yaver can also push a launch-feedback control message down the events channel to trigger it directly."
			}
			return "Remote runtime is intended to coexist with Yaver Feedback SDK instrumentation in native apps; session transport and feedback transport remain separate."
		}(),
		// FeedbackSurface tells the client HOW feedback is captured for this
		// session: "viewer-overlay" = the phone draws the feedback UI over the
		// WebRTC video (RN sim streaming); "in-app-sdk" = the running app carries
		// its own SDK (native). The mobile RemoteRuntimeViewer switches on this.
		FeedbackSurface: func() string {
			if rnSim {
				return "client-shake-remote-sim"
			}
			return "in-app-sdk"
		}(),
		FeedbackControlProtocol: "remote-runtime-feedback-v1",
		SupportedTransports:     []string{"direct-webrtc", "relay-jpeg-poll"},
		CurrentHostClass:        detectRuntimeHostClass(),
	}
	if !caps.RemoteRuntimeEligible {
		return caps
	}
	// RN/Expo streams into the same iOS simulators / Android emulators the native
	// path uses; only the BUILD command differs (expo run:ios / run:android,
	// resolved in native_build.go). Offer both platforms, capability-probed.
	if rnSim {
		caps.Targets = []RemoteRuntimeTarget{
			probeIOSSimulatorTarget(appleRuntimeFamiliesForCaps()),
			probeAndroidEmulatorTarget(),
			probeAndroidDeviceTarget(),
			probeIOSDeviceTarget(),
		}
		caps.RemoteBuilders = collectIOSBuilderSummaries()
		return caps
	}
	// One probe per capabilities call — five `simctl list runtimes` shells
	// would slow the picker load. Empty map on non-darwin hosts is fine:
	// each Apple probe short-circuits on the macOS check first.
	appleFams := appleRuntimeFamiliesForCaps()
	switch mode {
	case ExecutionModeNativeWebRTC:
		switch strings.ToLower(strings.TrimSpace(framework)) {
		case "swift":
			// iPhone default; then iPad/watchOS/tvOS/visionOS sims (each
			// gated on its runtime being installed); physical iPhone last.
			caps.Targets = []RemoteRuntimeTarget{
				probeIOSSimulatorTarget(appleFams),
				probeIPadSimulatorTarget(appleFams),
				probeWatchOSSimulatorTarget(appleFams),
				probeTVOSSimulatorTarget(appleFams),
				probeVisionOSSimulatorTarget(appleFams),
				probeIOSDeviceTarget(),
			}
		case "kotlin":
			// Emulator first (default where the host can run it),
			// physical device second (the only path on a host with no
			// emulator binary — e.g. linux/arm64). Capability-probed,
			// never host-name-gated. P6 adds Wear/TV/XR/Auto surface
			// variants (all adb-based, differ only in AVD).
			caps.Targets = []RemoteRuntimeTarget{
				probeAndroidEmulatorTarget(),
				probeAndroidWearTarget(),
				probeAndroidTVTarget(),
				probeAndroidXRTarget(),
				probeAndroidAutoTarget(),
				probeRedroidTarget(),
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
				probeAndroidWearTarget(),
				probeAndroidTVTarget(),
				probeAndroidXRTarget(),
				probeAndroidAutoTarget(),
				probeRedroidTarget(),
				probeAndroidDeviceTarget(),
				probeIOSSimulatorTarget(appleFams),
				probeIPadSimulatorTarget(appleFams),
				probeWatchOSSimulatorTarget(appleFams),
				probeTVOSSimulatorTarget(appleFams),
				probeVisionOSSimulatorTarget(appleFams),
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
		case "desktop":
			// The host's own screen, not a project runtime — this is the
			// "drive my actual PC from a phone/headset" path. Unlike every
			// other arm here it is framework-independent; "desktop" is a
			// pseudo-framework so the existing picker plumbing can reach it
			// without a parallel capabilities endpoint.
			caps.Targets = []RemoteRuntimeTarget{
				probeDesktopScreenTarget(),
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

// appleRuntimeFamiliesForCaps is the seam a capabilities call uses to
// know which Apple runtime families are installed (`iOS`, `watchOS`,
// `tvOS`, `visionOS`). Cached per call because we build all five Apple
// probes from one map; overridable by tests via
// setAppleRuntimeFamiliesForTest to avoid shelling to simctl.
var appleRuntimeFamiliesForCaps = func() map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	fams, _ := testkit.InstalledRuntimeFamilies(ctx)
	return fams
}

// setAppleRuntimeFamiliesForTest swaps the appleRuntimeFamiliesForCaps
// callback and returns a cleanup that restores the original. Used by
// tests to drive the five Apple probes deterministically without
// shelling to `xcrun simctl list runtimes`.
func setAppleRuntimeFamiliesForTest(fams map[string]bool) func() {
	prev := appleRuntimeFamiliesForCaps
	appleRuntimeFamiliesForCaps = func() map[string]bool {
		copy := map[string]bool{}
		for k, v := range fams {
			copy[k] = v
		}
		return copy
	}
	return func() { appleRuntimeFamiliesForCaps = prev }
}

// probeAppleSimTarget is the shared core for every Apple-runtime sim
// probe. It applies the darwin / xcrun / xcode-select gate and then,
// if all host prereqs pass, the per-runtime-family install gate. The
// caller supplies id/surface/label/family so each thin probe is a
// two-liner.
func probeAppleSimTarget(id, surface, label, family string, families map[string]bool) RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               id,
		Label:            label,
		Platform:         "ios",
		Surface:          surface,
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
	if family != "" && !families[family] {
		target.Enabled = false
		target.Reason = family + " runtime not installed. Open Xcode > Settings > Components and install it."
		return target
	}
	target.Enabled = true
	return target
}

func probeIOSSimulatorTarget(families map[string]bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("ios-simulator", "phone", "iPhone Simulator over WebRTC", "iOS", families)
}

func probeIPadSimulatorTarget(families map[string]bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("ipados-simulator", "tablet", "iPad Simulator over WebRTC", "iOS", families)
}

func probeWatchOSSimulatorTarget(families map[string]bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("watchos-simulator", "watch", "Apple Watch Simulator over WebRTC", "watchOS", families)
}

func probeTVOSSimulatorTarget(families map[string]bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("tvos-simulator", "tv", "Apple TV Simulator over WebRTC", "tvOS", families)
}

func probeVisionOSSimulatorTarget(families map[string]bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("visionos-simulator", "vision", "Apple Vision Pro Simulator over WebRTC", "visionOS", families)
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

// launchAppOnRuntimeTarget dispatches a `launch-app` session command to
// the target-appropriate driver (simctl for iOS/iPadOS/watch/tv/vision,
// adb for android). New in P1: adds the second useful command on top of
// the legacy `launch-feedback`. Kept dispatcher-local (not a
// runtimeTarget method) so browser/redroid/stream targets don't have to
// implement a no-op.
func launchAppOnRuntimeTarget(ctx context.Context, session RemoteRuntimeSession, bundleID string) error {
	switch session.TargetID {
	case "ios-simulator", "ipados-simulator", "watchos-simulator", "tvos-simulator", "visionos-simulator":
		return (&testkit.IOSSimDriver{BundleID: bundleID}).Launch(ctx, session.DeviceID)
	case "android-emulator", "android-device", "android-wear", "android-tv", "android-xr", "android-auto", remoteRuntimeRedroidTargetID:
		return (&testkit.AndroidEmuDriver{Package: bundleID}).Launch(ctx, session.DeviceID)
	case desktopScreenTargetID:
		// bundleID carries an application NAME here ("Safari", "AutoCAD"),
		// not a bundle identifier — desktop launchers resolve by name.
		return launchDesktopApp(ctx, bundleID)
	}
	return fmt.Errorf("launch-app is not supported for target %q", session.TargetID)
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
	if framework == "" {
		jsonError(w, http.StatusBadRequest, "framework is required")
		return
	}
	// The desktop target streams the machine itself, not a project, so it has
	// no workDir. Every other framework still requires one — a missing workDir
	// there is a client bug we want surfaced, not defaulted away.
	if workDir == "" && !strings.EqualFold(framework, "desktop") {
		jsonError(w, http.StatusBadRequest, "workDir is required for project runtimes")
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
		Command  string `json:"command"`
		Source   string `json:"source,omitempty"`
		BundleID string `json:"bundleId,omitempty"`
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
	case "boot":
		// Idempotent re-attach — useful when the picker created a
		// session but the caller wants a fresh device id (a proxied
		// session may have been dispatched to a builder, in which case
		// the local Attach is a no-op).
		attached, attachErr := mgr.Attach(session.ID)
		if attachErr != nil {
			jsonError(w, http.StatusBadRequest, attachErr.Error())
			return
		}
		updated, _ := mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			current.LastCommand = "boot"
			current.Note = "Session (re)attached; device booted."
		})
		if attached.DeviceID != "" && updated.DeviceID == "" {
			// The Attach return carries the freshly resolved device id;
			// mgr.Update may not see it if the manager already stamped
			// it on the session earlier. Merge the two views.
			updated.DeviceID = attached.DeviceID
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"sessionId": session.ID,
			"command":   "boot",
			"deviceId":  updated.DeviceID,
			"session":   updated,
		})
	case "launch-app":
		bundleID := strings.TrimSpace(req.BundleID)
		if bundleID == "" {
			jsonError(w, http.StatusBadRequest, "launch-app requires bundleId")
			return
		}
		if session.DeviceID == "" {
			jsonError(w, http.StatusBadRequest, "session has no device; run boot first")
			return
		}
		if err := launchAppOnRuntimeTarget(r.Context(), session, bundleID); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, _ := mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			current.LastCommand = "launch-app"
			current.Note = fmt.Sprintf("Launched %s on %s.", bundleID, session.TargetID)
		})
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"sessionId": session.ID,
			"command":   "launch-app",
			"bundleId":  bundleID,
			"session":   updated,
		})
	case "launch-feedback":
		source := strings.TrimSpace(req.Source)
		if source == "" {
			source = "unknown"
		}
		if live, ok := mgr.getLive(session.ID); ok {
			live.sendEventJSON(map[string]any{
				"type":      "feedback-launch-request",
				"protocol":  "remote-runtime-feedback-v1",
				"sessionId": session.ID,
				"source":    source,
				"ts":        time.Now().UTC().Format(time.RFC3339Nano),
			})
		}
		updated, _ := mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			current.Status = "feedback-pending"
			current.LastCommand = "launch-feedback"
			current.Note = fmt.Sprintf("Feedback launch requested from %s.", source)
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
	case "shake":
		// Client→server feedback trigger. The viewer (phone shake or a web
		// "Shake" button — works with or without the Yaver mobile app) sends
		// this; the agent injects a hardware shake into the remote simulator so
		// the guest app's OWN Yaver Feedback SDK — live and standalone in the
		// real sim — fires its overlay, which streams back over the same WebRTC
		// video. Also emits a feedback-launch-request on the events channel so a
		// viewer-side overlay (or an SDK subscribed to it) can trigger even if the
		// hardware-shake injection is a no-op on this host.
		source := strings.TrimSpace(req.Source)
		if source == "" {
			source = "viewer-shake"
		}
		injErr := injectSimulatorShake(r.Context(), session)
		if live, ok := mgr.getLive(session.ID); ok {
			live.sendEventJSON(map[string]any{
				"type":      "feedback-launch-request",
				"protocol":  "remote-runtime-feedback-v1",
				"sessionId": session.ID,
				"source":    source,
				"trigger":   "shake",
				"ts":        time.Now().UTC().Format(time.RFC3339Nano),
			})
		}
		note := "Shake injected into the simulator; the guest app's feedback SDK should open."
		if injErr != nil {
			note = "Hardware-shake injection unavailable on this host (" + injErr.Error() + "); sent feedback-launch-request instead."
		}
		updated, _ := mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			current.Status = "feedback-pending"
			current.LastCommand = "shake"
			current.Note = note
		})
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":             true,
			"sessionId":      session.ID,
			"command":        "shake",
			"source":         source,
			"injected":       injErr == nil,
			"injectionError": errString(injErr),
			"session":        updated,
			"note":           note,
		})
	default:
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("unsupported command %q", req.Command))
	}
}

// injectSimulatorShake sends a hardware shake gesture to the session's booted
// simulator/emulator so the guest app's motion-based shake detector (the Yaver
// Feedback SDK's accelerometer path) fires. Best-effort and platform-specific:
//   - iOS simulator: the Simulator app's Device ▸ Shake menu, driven via
//     osascript (there is no `simctl shake` verb).
//   - Android emulator: an accelerometer burst via `adb emu sensor set`.
//
// Returns an error the caller degrades on (it still emits a launch-feedback
// event), never a panic — an unsupported host is a normal, expected outcome.
func injectSimulatorShake(ctx context.Context, session RemoteRuntimeSession) error {
	switch session.TargetID {
	case "ios-simulator", "ipados-simulator", "watchos-simulator", "tvos-simulator", "visionos-simulator":
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("iOS simulator shake needs macOS")
		}
		script := `tell application "Simulator" to activate
tell application "System Events" to tell process "Simulator" to click menu item "Shake" of menu "Device" of menu bar 1`
		out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
		if err != nil {
			return fmt.Errorf("osascript shake failed: %s", strings.TrimSpace(string(out)))
		}
		return nil
	case "android-emulator", "android-device", "android-wear", "android-tv", "android-xr", "android-auto", remoteRuntimeRedroidTargetID:
		dev := strings.TrimSpace(session.DeviceID)
		if dev == "" {
			return fmt.Errorf("android session has no device id")
		}
		// A short accelerometer burst: a hard jolt then rest. `adb -s <emu>
		// emu sensor set acceleration x:y:z` injects raw accelerometer values;
		// alternating peaks cross the SDK's 1.8g shake threshold.
		for _, v := range []string{"20:20:20", "-20:-20:-20", "20:20:20", "0:9.8:0"} {
			if out, err := exec.CommandContext(ctx, "adb", "-s", dev, "emu", "sensor", "set", "acceleration", v).CombinedOutput(); err != nil {
				return fmt.Errorf("adb emu sensor failed: %s", strings.TrimSpace(string(out)))
			}
		}
		return nil
	}
	return fmt.Errorf("shake injection not supported for target %q", session.TargetID)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
