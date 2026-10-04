package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	// Additive: JSON clients ignore unknown fields. Populated for simulator,
	// emulator, physical-device, browser, and desktop runtime targets.
	Surface        string                 `json:"surface,omitempty"`
	DisplaySurface string                 `json:"displaySurface,omitempty"`
	Viewport       *RemoteRuntimeViewport `json:"viewport,omitempty"`
	Checks         []RemoteRuntimeCheck   `json:"checks,omitempty"`
	RoleHint       string                 `json:"roleHint,omitempty"`
}

type RemoteRuntimeViewport struct {
	Label  string `json:"label,omitempty"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type RemoteRuntimeCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

type RemoteRuntimeCapabilities struct {
	WorkDir                 string                `json:"workDir"`
	Framework               string                `json:"framework"`
	ExecutionMode           ProjectExecutionMode  `json:"executionMode"`
	PrimarySurface          string                `json:"primarySurface"`
	RemoteRuntimeEligible   bool                  `json:"remoteRuntimeEligible"`
	FeedbackSDKCompatible   bool                  `json:"feedbackSdkCompatible"`
	FeedbackSDKNote         string                `json:"feedbackSdkNote,omitempty"`
	FeedbackSurface         string                `json:"feedbackSurface,omitempty"` // "client-shake-remote-sim" (RN sim stream) | "in-app-sdk" (browser/flutter/native)
	FeedbackControlProtocol string                `json:"feedbackControlProtocol,omitempty"`
	SupportedTransports     []string              `json:"supportedTransports,omitempty"`
	CurrentHostClass        string                `json:"currentHostClass,omitempty"`
	Targets                 []RemoteRuntimeTarget `json:"targets"`
	Cached                  bool                  `json:"cached,omitempty"`
	CachedAt                string                `json:"cachedAt,omitempty"`
	ProbeDurationMs         int64                 `json:"probeDurationMs,omitempty"`
	// RemoteBuilders is the list of paired builders the dashboard
	// can dispatch to. Populated for Swift / iOS sessions on
	// non-darwin hosts; empty everywhere else. Each entry is the
	// public-safe subset of the on-disk registry (no token).
	RemoteBuilders []RemoteBuilderSummary `json:"remoteBuilders,omitempty"`
}

type remoteRuntimeCapabilitiesCacheEntry struct {
	caps     RemoteRuntimeCapabilities
	cachedAt time.Time
}

var (
	remoteRuntimeCapabilitiesCacheMu sync.Mutex
	remoteRuntimeCapabilitiesCache   = map[string]remoteRuntimeCapabilitiesCacheEntry{}
)

const remoteRuntimeCapabilitiesCacheTTL = 2 * time.Minute

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
	ID            string               `json:"id"`
	WorkDir       string               `json:"workDir"`
	Framework     string               `json:"framework"`
	ExecutionMode ProjectExecutionMode `json:"executionMode"`
	TargetID      string               `json:"targetId"`
	TargetLabel   string               `json:"targetLabel"`
	Platform      string               `json:"platform,omitempty"`
	DeviceID      string               `json:"deviceId,omitempty"`
	// RequestedRealDeviceID is opaque agent-local routing state. It selects a
	// registered physical device without exposing its ADB serial to Convex.
	RequestedRealDeviceID string                 `json:"-"`
	RuntimeHostClass      string                 `json:"runtimeHostClass,omitempty"`
	DisplaySurface        string                 `json:"displaySurface,omitempty"`
	Viewport              *RemoteRuntimeViewport `json:"viewport,omitempty"`
	TransportMode         string                 `json:"transportMode,omitempty"`
	FrameTransport        string                 `json:"frameTransport,omitempty"`
	Status                string                 `json:"status"`
	LastCommand           string                 `json:"lastCommand,omitempty"`
	// TextInputFocused is measured after a pointer tap on browser-window.
	// Remote viewers cannot receive the page's native keyboard request, so this
	// signal lets them open their own text-entry surface only for real editable
	// elements rather than guessing from pixels or opening on every button.
	TextInputFocused bool   `json:"textInputFocused,omitempty"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
	Note             string `json:"note,omitempty"`
	// ViewerCount is the number of live viewers/participants attached to
	// this session (webrtc peers + fresh frame-pollers). Stamped from live
	// state on List/Get/Create responses; 0 for proxied builder sessions
	// where the builder holds the real viewers.
	ViewerCount int `json:"viewerCount,omitempty"`
	// StartedBy is the stable clientId of the surface that created the
	// session (e.g. "tvos-<uuid>"), so the roster can say "Kivan · phone".
	StartedBy string `json:"startedBy,omitempty"`
	// SourceSurface is the normalized X-Yaver-Surface of the creator
	// ("mobile" | "tvos" | "web" | "desktop" | ...).
	SourceSurface string `json:"sourceSurface,omitempty"`
	// RemoteBuilderId is the alias (NOT the URL or token) of the
	// builder this session is dispatched to. Set when a Linux dev
	// box forwards a Swift session to a paired Mac via the Phase-5
	// proxy. Empty for local sessions. URL + token are private to
	// the agent's on-disk registry and never appear in any payload
	// returned by the agent.
	RemoteBuilderId string `json:"remoteBuilderId,omitempty"`
	// Runner is the remote-side coding runner (claude / codex / opencode / …)
	// that services feedback→prompt→patch for this session. Like tasks, it
	// defaults to the box's primary runner but the viewer can change it mid-
	// session via the same picker UX. Empty means "use the primary".
	Runner string `json:"runner,omitempty"`
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
	// capabilitiesForCreate is an instance-local test seam. Production leaves
	// it nil and probes the real host operation; attribution tests can exercise
	// the session contract without pretending a Linux box has Xcode.
	capabilitiesForCreate func(workDir, framework string) RemoteRuntimeCapabilities

	// devManager is the source of the URL a browser-window session
	// should navigate to on Create. Wired explicitly at construction
	// (SetDevServerManager) so the runtime manager doesn't reach through
	// a package-level global — nil is fine for surfaces that never
	// create browser-window sessions. See attachAndNavigateBrowserWindow.
	//
	// Held as an INTERFACE, not the concrete *DevServerManager, so tests
	// can inject a stub that reports IsRunning/DevServerPort/WebPreviewPort
	// without spinning up Metro/Vite/Flutter for real. The concrete
	// *DevServerManager satisfies this interface by construction.
	devManager devServerInfo

	// browserNav is the browser-window attach+navigate seam Create uses
	// for browser-window targets. Nil in production — the real
	// browserWindowTarget{} is used. Tests inject a recorder to assert
	// the URL Create picked without launching real Chrome.
	browserNav browserWindowNavigator

	// iceServerProvider is wired by the authenticated HTTPServer and returns
	// bounded, short-lived managed TURN credentials. Tests and non-server
	// callers leave it nil and retain the deterministic local STUN/TURN config.
	iceServerProvider rtcICEServerProvider
	// vibingControl handles reliable, versioned DOM-selection messages arriving
	// on the events DataChannel. HTTP remains the fallback for surfaces without
	// WebRTC data channels (today: tvOS snapshot polling and watches).
	vibingControl vibingWebRTCControlHandler
}

// devServerInfo is the narrow read-only view attachAndNavigateBrowserWindow
// needs from the DevServerManager. Split into an interface so a test can
// provide a stub without constructing the full manager (which would need a
// real dev-server subprocess to be interesting).
type devServerInfo interface {
	IsRunning() bool
	Status() *DevServerStatus
	DevServerPort() int
	WebPreviewPort() int
}

// browserWindowNavigator is the two-verb subset of runtimeTarget that Create
// uses when it needs to open a browser-window session on the project's dev
// server. Split from the fuller interface so tests can supply a recorder that
// captures the URL without spawning real Chrome (Attach on the real target
// launches chromedp headlessly).
type browserWindowNavigator interface {
	Attach(ctx context.Context) (deviceID string, err error)
	Navigate(ctx context.Context, deviceID, url string) error
}

type browserWindowViewportNavigator interface {
	AttachViewport(ctx context.Context, width, height int) (deviceID string, err error)
}

func NewRemoteRuntimeManager() *RemoteRuntimeManager {
	return &RemoteRuntimeManager{
		sessions: map[string]RemoteRuntimeSession{},
		live:     map[string]*remoteRuntimeLiveState{},
		proxied:  map[string]*proxiedSession{},
	}
}

// SetDevServerManager wires the manager that owns the running dev server.
// Called by the HTTP server once both managers are constructed. A nil dev
// manager is legal (browser-window sessions then always fall back to the
// "no dev server" reason string) but not what production wires.
func (m *RemoteRuntimeManager) SetDevServerManager(dsm devServerInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devManager = dsm
}

// SetBrowserNavigator swaps the browser-window attach/navigate seam. Tests
// only — production leaves it nil and gets the real browserWindowTarget.
func (m *RemoteRuntimeManager) SetBrowserNavigator(nav browserWindowNavigator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.browserNav = nav
}

func (m *RemoteRuntimeManager) SetICEServerProvider(provider rtcICEServerProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.iceServerProvider = provider
}

func (m *RemoteRuntimeManager) SetVibingWebRTCControlHandler(handler vibingWebRTCControlHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vibingControl = handler
}

func executionModeForFramework(framework string) ProjectExecutionMode {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		// Hermes hot-reload only — never WebRTC. RN apps load as guest
		// bundles into the Yaver mobile super-host. See
		// docs/native-webrtc-web-streaming.md §13.
		return ExecutionModeRNHermes
	case "next", "nextjs", "vite", "react", "firebase", "supabase", "convex", "yaver-serverless":
		return ExecutionModeWebWebview
	case "swift", "kotlin", "flutter":
		// Flutter joins Swift + Kotlin in the WebRTC family because
		// it doesn't fit the Hermes guest-bundle model — its UI runs
		// on Skia/Impeller in its own process. The web dashboard's
		// RemoteRuntimeViewer streams the running emulator/simulator.
		return ExecutionModeNativeWebRTC
	case "unity":
		// Unity is treated as a generic remote-PC view: no Unity SDK, no
		// editor bridge — the built player runs on the box's display and
		// the desktop-screen target streams that display over the same
		// WebRTC/JPEG pipeline that ships every other native runtime.
		// The player is launched via /unity/relaunch; the box must have a
		// real display for the ffmpeg grab (same constraint as "desktop").
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
// StreamingSurface is how a guest app is run+streamed for the WebRTC vibe loop.
type StreamingSurface string

const (
	// SurfaceBrowser — RN-Web / Flutter-Web in a headless Chrome tab, streamed via
	// CDP screencast. Fastest + lightest (sub-second HMR, no emulator, no Xcode,
	// runs on any Linux). Loses native modules; perfect for UI vibing.
	SurfaceBrowser StreamingSurface = "browser"
	// SurfaceEmulator — Android emulator (KVM on Linux) / redroid container. Full
	// native Android; adb screenrecord H.264. Cross-platform + Kotlin.
	SurfaceEmulator StreamingSurface = "emulator"
	// SurfaceSimulator — Apple sim (iOS/watch/tv/vision/CarPlay) captured via
	// ScreenCaptureKit. Heaviest; needs a healthy Mac. Apple-native only.
	SurfaceSimulator StreamingSurface = "simulator"
)

// defaultStreamingSurface is Yaver's PRAGMATIC default per framework — fastest
// path that still shows the real app. The user can override it in settings
// (streamingSurfaceOverride), but these are what Yaver picks unasked:
//
//	RN / Flutter → browser   (fastest; RN-Web / Flutter-Web)
//	Kotlin       → emulator  (native Android, no browser option)
//	Swift/Apple  → simulator (native Apple, Xcode required)
//	web frameworks → browser (they ARE web)
func defaultStreamingSurface(framework string) StreamingSurface {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native", "flutter", "next", "nextjs", "vite", "react", "browser":
		return SurfaceBrowser
	case "kotlin":
		return SurfaceEmulator
	case "swift":
		return SurfaceSimulator
	}
	return SurfaceBrowser
}

// streamingSurfaceOptions lists every surface a framework CAN use, default first,
// so the UI shows the Yaver default prominently and the alternatives in settings.
func streamingSurfaceOptions(framework string) []StreamingSurface {
	fw := strings.ToLower(strings.TrimSpace(framework))
	switch fw {
	case "expo", "react-native":
		// browser (fast) → Android emulator (native) → iOS sim (native Apple)
		return []StreamingSurface{SurfaceBrowser, SurfaceEmulator, SurfaceSimulator}
	case "flutter":
		return []StreamingSurface{SurfaceBrowser, SurfaceEmulator, SurfaceSimulator}
	case "kotlin":
		return []StreamingSurface{SurfaceEmulator}
	case "swift":
		return []StreamingSurface{SurfaceSimulator}
	case "next", "nextjs", "vite", "react", "browser":
		return []StreamingSurface{SurfaceBrowser}
	}
	return []StreamingSurface{SurfaceBrowser}
}

// resolveStreamingSurface applies a user override if it's valid for the
// framework, else falls back to the Yaver default. Empty override → default.
func resolveStreamingSurface(framework, override string) StreamingSurface {
	want := StreamingSurface(strings.ToLower(strings.TrimSpace(override)))
	if want != "" {
		for _, opt := range streamingSurfaceOptions(framework) {
			if opt == want {
				return want
			}
		}
	}
	return defaultStreamingSurface(framework)
}

// webDevServerCommand returns the command to start a framework's WEB build dev
// server, for the browser streaming surface (the pragmatic RN/Flutter default).
// The agent starts this, points a headless-Chrome browser session at
// http://127.0.0.1:<port>, and streams the tab via CDP screencast — sub-second
// HMR, no emulator, no Xcode. Testable contract; the shell-out is trivial.
func webDevServerCommand(framework string, port int) ([]string, error) {
	p := fmt.Sprintf("%d", port)
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		return []string{"npx", "expo", "start", "--web", "--port", p}, nil
	case "flutter":
		return []string{"flutter", "run", "-d", "web-server", "--web-port", p, "--web-hostname", "127.0.0.1"}, nil
	case "next", "nextjs":
		return []string{"npx", "next", "dev", "-p", p}, nil
	case "vite", "react":
		return []string{"npx", "vite", "--port", p, "--host", "127.0.0.1"}, nil
	}
	return nil, fmt.Errorf("no web dev server command for framework %q", framework)
}

func frameworkStreamsRNViaSimulator(framework string) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		return true
	}
	return false
}

type runtimeProjectSurfaceSet map[WireSurface]bool

func runtimeProjectSurfaces(workDir, framework string) runtimeProjectSurfaceSet {
	out := runtimeProjectSurfaceSet{}
	if strings.TrimSpace(workDir) == "" {
		return out
	}
	stack := strings.ToLower(strings.TrimSpace(framework))
	switch stack {
	case "swift":
		stack = "native-ios"
	case "kotlin":
		stack = "native-android"
	}
	for _, surface := range DetectProjectSurfaces(workDir, stack) {
		out[surface] = true
	}
	return out
}

func runtimeProjectHasMobileAppleSurface(framework string, surfaces runtimeProjectSurfaceSet) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native", "flutter", "swift":
		return true
	default:
		return surfaces[SurfaceIOS]
	}
}

func runtimeProjectHasMobileAndroidSurface(framework string, surfaces runtimeProjectSurfaceSet) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native", "flutter", "kotlin":
		return true
	default:
		return surfaces[SurfaceAndroid]
	}
}

func projectHasAnyPathMarker(root string, markers ...string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	for _, marker := range markers {
		p := filepath.Join(root, filepath.FromSlash(marker))
		if st, err := os.Stat(p); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return true
		}
	}
	return false
}

func appendAppleMobileRuntimeTargets(targets []RemoteRuntimeTarget, families map[string]bool, familiesKnown bool) []RemoteRuntimeTarget {
	return append(targets,
		probeIOSSimulatorTarget(families, familiesKnown),
		probeIPadSimulatorTarget(families, familiesKnown),
		probeIOSDeviceTarget(),
	)
}

func appendAndroidMobileRuntimeTargets(targets []RemoteRuntimeTarget, includeRedroid bool) []RemoteRuntimeTarget {
	targets = append(targets, probeAndroidEmulatorTarget())
	if includeRedroid {
		targets = append(targets, probeRedroidTarget())
	}
	return append(targets, probeAndroidDeviceTarget())
}

func appendDeclaredSpecialRuntimeTargets(
	targets []RemoteRuntimeTarget,
	workDir string,
	surfaces runtimeProjectSurfaceSet,
	families map[string]bool,
	familiesKnown bool,
) []RemoteRuntimeTarget {
	allSurfaces := strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_REMOTE_RUNTIME_ALL_SURFACES")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_REMOTE_RUNTIME_ALL_SURFACES")), "true")
	// Manifest-derived Android surface markers (leanback/XR/Automotive in
	// AndroidManifest.xml) gate the emulator lanes the same way the directory
	// path markers do — a repo is not "an Android TV app" because a folder is
	// named android-tv any more than because it isn't. One scan for all three
	// gates, not three scans.
	androidMarkers := androidManifestMarkers(workDir)
	if surfaces[SurfaceWatchOS] {
		targets = append(targets, probeWatchOSSimulatorTarget(families, familiesKnown))
	}
	if surfaces[SurfaceWearOS] || allSurfaces {
		targets = append(targets, probeAndroidWearTarget())
	}
	if surfaces[SurfaceTVOS] {
		targets = append(targets, probeTVOSSimulatorTarget(families, familiesKnown))
	}
	if allSurfaces || projectHasAnyPathMarker(workDir, "android-tv", "androidtv", "tv/android", "tv/android-tv") || androidMarkers["tv"] {
		targets = append(targets, probeAndroidTVTarget())
	}
	if surfaces[SurfaceVisionOS] {
		targets = append(targets, probeVisionOSSimulatorTarget(families, familiesKnown))
	}
	if allSurfaces || projectHasAnyPathMarker(workDir, "android-xr", "xr/android", "vision/android") || androidMarkers["xr"] {
		targets = append(targets, probeAndroidXRTarget())
	}
	if allSurfaces || projectHasAnyPathMarker(workDir, "android-auto", "automotive", "car/android", "car/android-auto") || androidMarkers["auto"] {
		targets = append(targets, probeAndroidAutoTarget())
	}
	if allSurfaces {
		targets = append(targets, probeRedroidTarget())
	}
	return targets
}

func remoteRuntimeCapabilitiesForProject(workDir, framework string) RemoteRuntimeCapabilities {
	mode := executionModeForFramework(framework)
	rnSim := frameworkStreamsRNViaSimulator(framework)
	webBrowser := mode == ExecutionModeWebWebview
	projectSurfaces := runtimeProjectSurfaces(workDir, framework)
	// Eligibility is decoupled from the PRIMARY execution mode: a native app is
	// WebRTC-primary, while an RN app is Hermes-primary but ALSO simulator-
	// streamable. Both are remote-runtime eligible; PrimarySurface records which
	// is the default so the UI can present Hermes first and WebRTC as "also".
	// WebView is the primary surface for ordinary web clients, but TV/vision/
	// watch clients do not have an in-process browser. The same project is still
	// eligible for the browser-window capture target over WebRTC. PrimarySurface
	// remains webview so this additive lane does not change web/mobile routing.
	eligible := mode == ExecutionModeNativeWebRTC || rnSim || webBrowser
	caps := RemoteRuntimeCapabilities{
		WorkDir:               strings.TrimSpace(workDir),
		Framework:             strings.TrimSpace(framework),
		ExecutionMode:         mode,
		PrimarySurface:        primarySurfaceForFramework(framework),
		RemoteRuntimeEligible: eligible,
		// The RN app in a booted simulator carries its OWN feedback SDK, which is
		// live there (unlike the suppressed-in-container Hermes path), so shake +
		// feedback work natively. For native apps the note is unchanged.
		FeedbackSDKCompatible: mode == ExecutionModeNativeWebRTC || rnSim || webBrowser,
		FeedbackSDKNote: func() string {
			if rnSim {
				return "Feedback flows client→server: the phone owns shake detection (it already has ShakeDetector), and in a WebRTC session a shake sends the `shake` session command to the remote box, which injects a hardware shake into the simulator (simctl for iOS, adb sensor for Android). The project app's own Yaver Feedback SDK — live in the real simulator — then fires its overlay inside the sim, and that overlay streams back to the phone over the same WebRTC video. Yaver can also push a launch-feedback control message down the events channel to trigger it directly."
			}
			return "Remote runtime is intended to coexist with Yaver Feedback SDK instrumentation in native apps; session transport and feedback transport remain separate."
		}(),
		// FeedbackSurface tells the client HOW feedback is captured for this
		// session: "client-shake-remote-sim" = the viewer sends a shake/control
		// event into a real simulator, where the guest app's own SDK opens;
		// "in-app-sdk" = the browser/native app carries its own SDK directly.
		// Keep this on the shared FeedbackTransport constants so runtime
		// capabilities, preview planning and the SDK examples cannot drift into
		// three spellings for the same transport.
		FeedbackSurface: func() string {
			if rnSim {
				return FeedbackClientShakeRemoteSim
			}
			return string(FeedbackInAppSDK)
		}(),
		FeedbackControlProtocol: "remote-runtime-feedback-v1",
		SupportedTransports:     []string{"direct-webrtc", "relay-jpeg-poll"},
		CurrentHostClass:        detectRuntimeHostClass(),
		// Keep the wire shape stable. A nil Go slice serializes as null, which
		// strict native clients correctly reject for an array field and used to
		// turn an ordinary capability gap into "the data is missing".
		Targets: []RemoteRuntimeTarget{},
	}
	if webBrowser {
		caps.Targets = []RemoteRuntimeTarget{browserWindowTargetForFramework(framework)}
		return caps
	}
	if !caps.RemoteRuntimeEligible {
		return caps
	}
	if rnSim {
		rnAppleFams, rnAppleFamsKnown := appleRuntimeFamiliesForCaps()
		caps.Targets = []RemoteRuntimeTarget{
			// RN's WEB target, streamed from a headless tab on the box.
			//
			// It leads because it is the cheapest lane by an order of
			// magnitude — no simulator or emulator boot, ~0 vCPU against 1-2 —
			// and because it is the only one here where the IN-APP feedback
			// SDK applies: the app is genuinely running rather than being a
			// video of one, so feedback carries real state instead of a
			// viewer-triggered control message.
			//
			browserWindowTargetForFramework(framework),
		}
		if runtimeProjectHasMobileAppleSurface(framework, projectSurfaces) {
			caps.Targets = appendAppleMobileRuntimeTargets(caps.Targets, rnAppleFams, rnAppleFamsKnown)
		}
		if runtimeProjectHasMobileAndroidSurface(framework, projectSurfaces) {
			caps.Targets = appendAndroidMobileRuntimeTargets(caps.Targets, false)
		}
		caps.Targets = appendDeclaredSpecialRuntimeTargets(caps.Targets, workDir, projectSurfaces, rnAppleFams, rnAppleFamsKnown)
		caps.RemoteBuilders = collectIOSBuilderSummaries()
		return caps
	}
	// One probe per capabilities call — five `simctl list runtimes` shells
	// would slow the picker load. Empty map on non-darwin hosts is fine:
	// each Apple probe short-circuits on the macOS check first.
	appleFams, appleFamsKnown := appleRuntimeFamiliesForCaps()
	switch mode {
	case ExecutionModeNativeWebRTC:
		switch strings.ToLower(strings.TrimSpace(framework)) {
		case "swift":
			// "Swift" is four runtimes wearing one label, and only ONE of them
			// needs a Mac. A SwiftWasm/Tokamak project compiles to wasm and
			// renders in a browser, so it streams over WebRTC from a LINUX
			// workspace through the headless-Chrome target — no simulator, no
			// Mac. Before this, every Swift project was routed to the Apple
			// arm below, every target answered "Requires a macOS host with
			// Xcode installed", and browser-window (RuntimeHostClass "any",
			// gated only on a Chrome binary) was never offered to it. The
			// capability existed and the routing could not reach it.
			//
			// Detected from the project, not the label — a label cannot tell
			// UIKit from JavaScriptKit, and guessing wrong either strands a
			// Linux user or renders a blank tab for a UIKit app.
			if swiftKind := DetectSwiftProject(workDir).Kind; swiftKind == SwiftKindTokamak {
				caps.Targets = []RemoteRuntimeTarget{
					probeBrowserWindowTarget(),
					// Simulators still listed after it: on a Mac the user may
					// legitimately prefer to see it in a real iOS shell.
					probeIOSSimulatorTarget(appleFams, appleFamsKnown),
					probeIPadSimulatorTarget(appleFams, appleFamsKnown),
					probeIOSDeviceTarget(),
				}
				break
			}
			caps.Targets = appendAppleMobileRuntimeTargets(caps.Targets, appleFams, appleFamsKnown)
			caps.Targets = appendDeclaredSpecialRuntimeTargets(caps.Targets, workDir, projectSurfaces, appleFams, appleFamsKnown)
		case "kotlin":
			// Emulator first (default where the host can run it),
			// physical device second (the only path on a host with no
			// emulator binary — e.g. linux/arm64). Specialized Wear/TV/XR/Auto
			// targets require code markers in the project; host inventory alone
			// must not advertise a surface this app does not build.
			caps.Targets = appendAndroidMobileRuntimeTargets(caps.Targets, true)
			caps.Targets = appendDeclaredSpecialRuntimeTargets(caps.Targets, workDir, projectSurfaces, appleFams, appleFamsKnown)
		case "flutter":
			caps.Targets = []RemoteRuntimeTarget{
				// Flutter runs as a WEB dev server on the box
				// (devserver_kind.go classes it DevServerKindWeb), so the
				// lightest honest preview is the app itself in a browser —
				// no emulator boot, no Redroid, and the in-app yaver_feedback
				// SDK (pub.dev) works because the app is real rather than a
				// video of one.
				browserWindowTargetForFramework(framework),
			}
			if runtimeProjectHasMobileAndroidSurface(framework, projectSurfaces) {
				caps.Targets = appendAndroidMobileRuntimeTargets(caps.Targets, true)
			}
			if runtimeProjectHasMobileAppleSurface(framework, projectSurfaces) {
				caps.Targets = appendAppleMobileRuntimeTargets(caps.Targets, appleFams, appleFamsKnown)
			}
			caps.Targets = appendDeclaredSpecialRuntimeTargets(caps.Targets, workDir, projectSurfaces, appleFams, appleFamsKnown)
		case "browser":
			// One target: a headless Chromium tab on the agent host.
			// Same JPEG-DC transport as android/ios. Useful entry
			// points (Gmail tab, docs, generic URL) are layered on
			// top by ops_glass_pc.go verbs.
			caps.Targets = []RemoteRuntimeTarget{
				probeBrowserWindowTarget(),
			}
			// Manifest-declared sibling surfaces (TV/watch/vision/wear
			// apps in yaver.workspace.yaml) come AFTER the browser tab:
			// a web-first monorepo is still a monorepo, and probing it
			// with framework=browser used to return ONLY browser-window
			// while the host had tvOS/watchOS/visionOS runtimes
			// installed (2026-08-13: "targets: 1 primary, 0
			// advanced/unavailable" in the Vibing console with a Mac
			// render box). Each entry is surface/path-marker gated, so
			// a pure web project gains nothing extra.
			caps.Targets = appendDeclaredSpecialRuntimeTargets(caps.Targets, workDir, projectSurfaces, appleFams, appleFamsKnown)
		case "desktop":
			// The host's own screen, not a project runtime — this is the
			// "drive my actual PC from a phone/headset" path. Unlike every
			// other arm here it is framework-independent; "desktop" is a
			// pseudo-framework so the existing picker plumbing can reach it
			// without a parallel capabilities endpoint.
			caps.Targets = []RemoteRuntimeTarget{
				probeDesktopScreenTarget(),
			}
		case "unity":
			// Generic remote-PC view: the built Unity player runs on the
			// box's display; the desktop-screen ffmpeg grab streams it over
			// the same WebRTC/JPEG pipeline. No Unity SDK, no editor bridge
			// — the same "yet another remote PC" lane as "desktop".
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

func browserWindowTargetForFramework(framework string) RemoteRuntimeTarget {
	target := probeBrowserWindowTarget()
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native", "flutter":
		target.DisplaySurface = "mobile-web"
		target.Viewport = &RemoteRuntimeViewport{Label: "Mobile", Width: 393, Height: 852}
	}
	return target
}

func remoteRuntimeCapabilitiesForProjectCached(workDir, framework string, refresh bool) RemoteRuntimeCapabilities {
	key := remoteRuntimeCapabilitiesCacheKey(workDir, framework)
	now := time.Now()
	if !refresh {
		remoteRuntimeCapabilitiesCacheMu.Lock()
		if entry, ok := remoteRuntimeCapabilitiesCache[key]; ok && now.Sub(entry.cachedAt) < remoteRuntimeCapabilitiesCacheTTL {
			caps := cloneRemoteRuntimeCapabilities(entry.caps)
			caps.Cached = true
			caps.CachedAt = entry.cachedAt.UTC().Format(time.RFC3339)
			remoteRuntimeCapabilitiesCacheMu.Unlock()
			return caps
		}
		remoteRuntimeCapabilitiesCacheMu.Unlock()
	}

	start := time.Now()
	caps := remoteRuntimeCapabilitiesForProject(workDir, framework)
	caps.ProbeDurationMs = time.Since(start).Milliseconds()
	caps.Cached = false
	caps.CachedAt = now.UTC().Format(time.RFC3339)

	remoteRuntimeCapabilitiesCacheMu.Lock()
	remoteRuntimeCapabilitiesCache[key] = remoteRuntimeCapabilitiesCacheEntry{
		caps:     cloneRemoteRuntimeCapabilities(caps),
		cachedAt: now,
	}
	remoteRuntimeCapabilitiesCacheMu.Unlock()
	return caps
}

func remoteRuntimeCapabilitiesCacheKey(workDir, framework string) string {
	wd := strings.TrimSpace(workDir)
	if abs, err := filepath.Abs(wd); err == nil {
		wd = abs
	}
	return strings.ToLower(strings.TrimSpace(framework)) + "\x00" + filepath.Clean(wd)
}

func cloneRemoteRuntimeCapabilities(in RemoteRuntimeCapabilities) RemoteRuntimeCapabilities {
	out := in
	out.SupportedTransports = append([]string(nil), in.SupportedTransports...)
	out.Targets = append([]RemoteRuntimeTarget(nil), in.Targets...)
	for i := range out.Targets {
		out.Targets[i].Viewport = cloneRemoteRuntimeViewport(in.Targets[i].Viewport)
		out.Targets[i].Checks = append([]RemoteRuntimeCheck(nil), in.Targets[i].Checks...)
	}
	out.RemoteBuilders = append([]RemoteBuilderSummary(nil), in.RemoteBuilders...)
	return out
}

func cloneRemoteRuntimeViewport(in *RemoteRuntimeViewport) *RemoteRuntimeViewport {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func resetRemoteRuntimeCapabilitiesCacheForTest() {
	remoteRuntimeCapabilitiesCacheMu.Lock()
	defer remoteRuntimeCapabilitiesCacheMu.Unlock()
	remoteRuntimeCapabilitiesCache = map[string]remoteRuntimeCapabilitiesCacheEntry{}
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
// appleRuntimeFamiliesForCaps answers "which Apple simulator runtimes does this
// machine have?" — and, crucially, whether the answer is KNOWN.
//
// It used to allow `xcrun simctl list runtimes` 4 seconds and treat a timeout as
// "nothing installed". On a real Mac mini (2026-07-25) `xcrun simctl help` alone
// took 17 SECONDS, so every Apple target reported "iOS runtime not installed.
// Open Xcode > Settings > Components and install it." — while iOS 26.4 was
// installed AND a device was booted on it. The product told the user to install
// something they had, and the Swift/RN WebRTC lane looked unavailable on a machine
// that could run it.
//
// Now: read the runtime bundles off disk first (instant, cannot hang on a busy
// CoreSimulator), fall back to simctl with a realistic timeout, and report
// "unknown" separately from "absent".
var appleRuntimeFamiliesForCaps = func() (map[string]bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return testkit.InstalledRuntimeFamiliesDetermined(ctx)
}

// setAppleRuntimeFamiliesForTest swaps the appleRuntimeFamiliesForCaps
// callback and returns a cleanup that restores the original. Used by
// tests to drive the five Apple probes deterministically without
// shelling to `xcrun simctl list runtimes`.
func setAppleRuntimeFamiliesForTest(fams map[string]bool) func() {
	prev := appleRuntimeFamiliesForCaps
	appleRuntimeFamiliesForCaps = func() (map[string]bool, bool) {
		copy := map[string]bool{}
		for k, v := range fams {
			copy[k] = v
		}
		return copy, true
	}
	return func() { appleRuntimeFamiliesForCaps = prev }
}

var appleSimulatorDeviceAvailableForCaps = func(deviceType string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := testkit.RankSimulators(ctx, deviceType); err != nil {
		if ok, createReason := testkit.SimulatorTypeCreatable(ctx, deviceType); ok {
			return true, ""
		} else if strings.TrimSpace(createReason) != "" {
			return false, err.Error() + "; auto-create unavailable: " + createReason
		}
		return false, err.Error()
	}
	return true, ""
}

func setAppleSimulatorDevicesForTest(devices map[string]bool) func() {
	prev := appleSimulatorDeviceAvailableForCaps
	appleSimulatorDeviceAvailableForCaps = func(deviceType string) (bool, string) {
		if devices[deviceType] {
			return true, ""
		}
		return false, fmt.Sprintf("no available simulator matching %q", deviceType)
	}
	return func() { appleSimulatorDeviceAvailableForCaps = prev }
}

// probeAppleSimTarget is the shared core for every Apple-runtime sim
// probe. It applies the darwin / xcrun / xcode-select gate and then,
// if all host prereqs pass, the per-runtime-family install gate. The
// caller supplies id/surface/label/family so each thin probe is a
// two-liner.
func probeAppleSimTarget(id, surface, label, family, deviceType string, families map[string]bool, familiesKnown bool) RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               id,
		Label:            label,
		Platform:         "ios",
		Surface:          surface,
		RuntimeHostClass: "macos-ios",
		HostOS:           runtime.GOOS,
		RequiredCLI:      "xcrun simctl",
		// Per-platform pixel size — the viewer must respect each surface's
		// physical dimensions, not a hardcoded phone aspect. Mirrors the
		// androidTargetDisplay table (2026-08-12 audit: a tvOS session
		// rendered in a 9/19.5 phone frame because Apple sim targets carried
		// no viewport at all). Set even when the target is disabled so the
		// viewer can still lay out correctly.
		Viewport: appleTargetViewport(surface),
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
		if !familiesKnown {
			// Do NOT claim it is missing when we could not find out. This exact
			// conflation sent a user to Xcode > Components for a runtime that was
			// already installed, because simctl was slow (17s on the box in
			// question) rather than because anything was absent.
			target.Reason = family + " runtime could not be confirmed: no runtime bundle on disk and " +
				"`xcrun simctl` did not answer in time (it can take 15s+ when CoreSimulator is busy). " +
				"Retry, or run `xcrun simctl list runtimes` on the host to see the real state."
			return target
		}
		target.Reason = family + " runtime not installed. Open Xcode > Settings > Components and install it."
		return target
	}
	if ok, reason := appleSimulatorDeviceAvailableForCaps(deviceType); !ok {
		target.Enabled = false
		target.Reason = reason + ". Create or install a matching simulator in Xcode > Window > Devices and Simulators."
		return target
	}
	target.Enabled = true
	return target
}

func probeIOSSimulatorTarget(families map[string]bool, familiesKnown bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("ios-simulator", "phone", "iPhone Simulator over WebRTC", "iOS", "iPhone", families, familiesKnown)
}

func probeIPadSimulatorTarget(families map[string]bool, familiesKnown bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("ipados-simulator", "tablet", "iPad Simulator over WebRTC", "iOS", "iPad", families, familiesKnown)
}

func probeWatchOSSimulatorTarget(families map[string]bool, familiesKnown bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("watchos-simulator", "watch", "Apple Watch Simulator over WebRTC", "watchOS", "Apple Watch", families, familiesKnown)
}

func probeTVOSSimulatorTarget(families map[string]bool, familiesKnown bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("tvos-simulator", "tv", "Apple TV Simulator over WebRTC", "tvOS", "Apple TV", families, familiesKnown)
}

func probeVisionOSSimulatorTarget(families map[string]bool, familiesKnown bool) RemoteRuntimeTarget {
	return probeAppleSimTarget("visionos-simulator", "vision", "Apple Vision Pro Simulator over WebRTC", "visionOS", "Apple Vision", families, familiesKnown)
}

// appleTargetViewport returns the physical pixel size of an Apple simulator
// surface. Physical pixels, not points: a TV frame is 3840×2160 regardless of
// the points the tvOS UI lays out in. The viewer uses this to size its media
// element so a couch/test session shows the platform's real aspect, not a
// phone-shaped fallback (2026-08-12 audit).
func appleTargetViewport(surface string) *RemoteRuntimeViewport {
	switch surface {
	case "tv":
		return &RemoteRuntimeViewport{Label: "Apple TV", Width: 3840, Height: 2160}
	case "tablet":
		return &RemoteRuntimeViewport{Label: "iPad", Width: 1620, Height: 2160}
	case "watch":
		return &RemoteRuntimeViewport{Label: "Apple Watch", Width: 396, Height: 484}
	case "vision":
		return &RemoteRuntimeViewport{Label: "Vision Pro", Width: 1920, Height: 1080}
	default: // phone
		return &RemoteRuntimeViewport{Label: "iPhone", Width: 393, Height: 852}
	}
}

func probeAndroidEmulatorTarget() RemoteRuntimeTarget {
	display, viewport := androidTargetDisplay("phone")
	target := RemoteRuntimeTarget{
		ID:               "android-emulator",
		Label:            "Android Emulator over WebRTC",
		Surface:          "phone",
		Platform:         "android",
		RuntimeHostClass: runtimeHostClassForAndroid(),
		HostOS:           runtime.GOOS,
		RequiredCLI:      "adb + emulator",
		DisplaySurface:   display,
		Viewport:         viewport,
	}
	if findAndroidToolPath("adb") == "" {
		target.Enabled = false
		target.Reason = "adb not found. Install Android platform-tools."
		target.Checks = androidRuntimeChecks(false, false, "", false, "Not probed because adb is missing.")
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
		target.Checks = androidRuntimeChecks(true, false, "", false, "Not probed because the emulator binary is missing.")
		return target
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ok, reason := testkit.AndroidAnyAVDUsable(ctx); !ok {
		target.Enabled = false
		target.Reason = reason
		target.Checks = androidRuntimeChecks(true, true, "", false, reason)
		return target
	}
	target.Enabled = true
	target.Checks = androidRuntimeChecks(true, true, "", true, "")
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
	return m.CreateWith(workDir, framework, targetID, transportMode, remoteRuntimeCreator{})
}

// remoteRuntimeCreator identifies who is starting a session, so the
// shared-session roster can attribute it ("Kivan · phone started this").
type remoteRuntimeCreator struct {
	ClientID     string
	Surface      string
	RealDeviceID string
}

// sourceSurfaceForCreator keeps the DTO honest for legacy (anonymous)
// creates: normalizeSurface("") is "unknown", but a session started with no
// creator should carry NO source surface — empty means "unspecified", which
// is exactly what a legacy client means. A real creator always has a
// normalized surface (even "unknown" when the header named something odd).
func sourceSurfaceForCreator(creator remoteRuntimeCreator) string {
	if strings.TrimSpace(creator.ClientID) == "" && strings.TrimSpace(creator.Surface) == "" {
		return ""
	}
	return string(normalizeSurface(creator.Surface))
}

func (m *RemoteRuntimeManager) CreateWith(workDir, framework, targetID, transportMode string, creator remoteRuntimeCreator) (RemoteRuntimeSession, error) {
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

	capabilityProbe := m.capabilitiesForCreate
	if capabilityProbe == nil {
		capabilityProbe = remoteRuntimeCapabilitiesForProject
	}
	caps := capabilityProbe(workDir, framework)
	if !caps.RemoteRuntimeEligible {
		// Name the way FORWARD, not just the refusal.
		//
		// "monorepo projects use none, not WebRTC remote runtime" is accurate and
		// leaves the user nowhere: a monorepo is a container, and the runnable
		// things are inside it (talos/mobile, talos/cloud, …). Same for a repo
		// whose lane is simply a different one. Measured on the mini 2026-07-25,
		// where talos and yaver-todo-web each dead-ended on a sentence that
		// described the problem and not the next step.
		if subs := runnableSubProjects(workDir); len(subs) > 0 {
			return RemoteRuntimeSession{}, fmt.Errorf(
				"%s is a container, not a runnable app — its WebRTC lane lives in the sub-projects. "+
					"Pick one of: %s", filepath.Base(workDir), strings.Join(subs, ", "))
		}
		return RemoteRuntimeSession{}, fmt.Errorf(
			"%s projects use %s, not the WebRTC remote runtime — open it with %s instead",
			framework, caps.PrimarySurface, caps.PrimarySurface)
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
	requestedRealDeviceID := strings.TrimSpace(creator.RealDeviceID)
	if requestedRealDeviceID != "" && selected.ID != remoteRuntimeAndroidDeviceTargetID {
		return RemoteRuntimeSession{}, fmt.Errorf("realDeviceId is only valid with targetId=%s", remoteRuntimeAndroidDeviceTargetID)
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
		ID:                    fmt.Sprintf("rr_%d", time.Now().UTC().UnixNano()),
		WorkDir:               strings.TrimSpace(workDir),
		Framework:             strings.TrimSpace(framework),
		ExecutionMode:         caps.ExecutionMode,
		TargetID:              selected.ID,
		TargetLabel:           selected.Label,
		Platform:              selected.Platform,
		RequestedRealDeviceID: requestedRealDeviceID,
		RuntimeHostClass:      selected.RuntimeHostClass,
		DisplaySurface:        selected.DisplaySurface,
		Viewport:              cloneRemoteRuntimeViewport(selected.Viewport),
		TransportMode:         transportMode,
		FrameTransport:        frameTransport,
		Status:                "control-ready",
		StartedBy:             strings.TrimSpace(creator.ClientID),
		SourceSurface:         sourceSurfaceForCreator(creator),
		CreatedAt:             now,
		UpdatedAt:             now,
		Note:                  note,
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	live := &remoteRuntimeLiveState{sessionID: session.ID, targetID: selected.ID, platform: selected.Platform}
	if strings.TrimSpace(creator.ClientID) != "" {
		live.viewers = map[string]*remoteRuntimeViewer{
			strings.TrimSpace(creator.ClientID): {
				ID:      strings.TrimSpace(creator.ClientID),
				Surface: session.SourceSurface,
				Kind:    "webrtc", // creator holds the capture; roster shows them first
			},
		}
	}
	m.live[session.ID] = live
	m.mu.Unlock()

	// browser-window is the ONLY target where the session is content-less by
	// default: chromedp opens about:blank. Every other target boots into an
	// OS/app that shows something on its own. Wire the project's dev-server URL
	// here so the caller doesn't get a session that streams a blank page — the
	// bug this fix exists for. See BROWSER_VIBING_AUDIT.md §5.
	if selected.ID == "browser-window" {
		session = m.attachAndNavigateBrowserWindow(session, workDir, framework)
	}
	return session, nil
}

// resolveDevServerURL picks the URL a browser-window session should navigate
// to for a given (workDir, framework). Returns (url, "") when we have one and
// ("", reason) when the caller should carry a "start your dev server first"
// note back to the viewer instead of pointing at a dead port.
//
// The port choice matters: Expo/RN's Metro serves bytecode, not the web app —
// the web bundle lives on a SIBLING process started by StartWebPreview
// (WebPreviewPort). Every other web dev server serves the app on its primary
// port (DevServerPort). See the comment on WebPreviewPort in devserver.go.
func (m *RemoteRuntimeManager) resolveDevServerURL(workDir, framework string) (string, string) {
	m.mu.RLock()
	dsm := m.devManager
	m.mu.RUnlock()
	if dsm == nil {
		return "", "no dev-server manager wired for this agent — start a dev server first (yaver dev)"
	}
	if !dsm.IsRunning() {
		return "", "no dev server is running — start one first (yaver dev) so the browser has something to load"
	}
	// A dev server for a DIFFERENT project would render the wrong app. Refuse
	// rather than showing content that isn't yours; the user's next click
	// (Start Dev Server) resolves it.
	if st := dsm.Status(); st != nil {
		if wantDir := strings.TrimSpace(workDir); wantDir != "" &&
			!samePath(strings.TrimSpace(st.WorkDir), wantDir) {
			return "", fmt.Sprintf("a dev server is running for %s, not this project — restart it for %s", st.WorkDir, wantDir)
		}
	}
	fw := strings.ToLower(strings.TrimSpace(framework))
	var port int
	switch fw {
	case "expo", "react-native":
		port = dsm.WebPreviewPort()
		if port == 0 {
			return "", "Expo web preview isn't running — start it so RN-Web can load in the browser (Metro serves bytecode, not the web app)"
		}
	default:
		port = dsm.DevServerPort()
		if port == 0 {
			return "", "the dev server is running but has no listening port yet — retry once it finishes starting"
		}
	}
	// 127.0.0.1 rather than localhost: on some hosts localhost resolves to ::1
	// first and headless Chrome retries slow the first paint noticeably.
	return fmt.Sprintf("http://127.0.0.1:%d", port), ""
}

// samePath compares two absolute paths after cleaning + case-folding on macOS,
// which is case-insensitive on the default APFS. Empty strings are not equal.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

// attachAndNavigateBrowserWindow opens the headless browser and points it at
// the project's dev-server URL. Runs after Create has stored the session so
// its result mutations flow through m.Update, which the manager already uses
// for status/note updates from Attach and command handlers.
//
// If no URL is available (no dev server, wrong project, port not up yet), the
// session is left at about:blank with a Note explaining why. A blank frame with
// no explanation was the bug being fixed; a blank frame WITH an explanation is
// acceptable — the viewer can act on "start a dev server first" in a way it
// cannot on silence.
func (m *RemoteRuntimeManager) attachAndNavigateBrowserWindow(session RemoteRuntimeSession, workDir, framework string) RemoteRuntimeSession {
	url, reason := m.resolveDevServerURL(workDir, framework)
	if url == "" {
		updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
			s.Status = "waiting-for-dev-server"
			s.Note = "Browser window will open blank: " + reason
		})
		return updated
	}
	m.mu.RLock()
	nav := m.browserNav
	m.mu.RUnlock()
	if nav == nil {
		nav = browserWindowTarget{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var deviceID string
	var err error
	if selectedViewport := session.Viewport; selectedViewport != nil {
		if viewportNav, ok := nav.(browserWindowViewportNavigator); ok {
			deviceID, err = viewportNav.AttachViewport(ctx, selectedViewport.Width, selectedViewport.Height)
		} else {
			deviceID, err = nav.Attach(ctx)
		}
	} else {
		deviceID, err = nav.Attach(ctx)
	}
	if err != nil {
		updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
			s.Status = "attach-failed"
			s.Note = fmt.Sprintf("Browser attach failed before navigate to %s: %v", url, err)
		})
		return updated
	}
	// Record the deviceID on live state so the HTTP handler's later Attach()
	// call short-circuits on the DeviceID != "" guard — we already have the
	// window open, and re-Attach would open a second one.
	if live, ok := m.getLive(session.ID); ok {
		live.mu.Lock()
		live.deviceID = deviceID
		live.mu.Unlock()
		browserPool.setEventSink(deviceID, func(ev map[string]any) {
			live.sendEventJSON(ev)
		})
	}
	if err := nav.Navigate(ctx, deviceID, url); err != nil {
		updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
			s.DeviceID = deviceID
			s.Status = "navigate-failed"
			s.Note = fmt.Sprintf("Browser opened but navigate to %s failed: %v", url, err)
		})
		return updated
	}
	updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
		s.DeviceID = deviceID
		s.Status = "control-ready"
		s.Note = fmt.Sprintf("Browser opened. Navigated to %s.", url)
	})
	return updated
}

func (m *RemoteRuntimeManager) ensureBrowserWindowNavigated(session RemoteRuntimeSession, live *remoteRuntimeLiveState) RemoteRuntimeSession {
	deviceID := strings.TrimSpace(session.DeviceID)
	if session.TargetID != "browser-window" || deviceID == "" {
		return session
	}
	browserPool.setEventSink(deviceID, func(ev map[string]any) {
		live.sendEventJSON(ev)
	})
	if current, ok := browserPool.currentURL(deviceID); ok && strings.TrimSpace(current) != "" && current != "about:blank" {
		return session
	}
	url, reason := m.resolveDevServerURL(session.WorkDir, session.Framework)
	if url == "" {
		updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
			s.Status = "waiting-for-dev-server"
			s.Note = "Browser window is open but blank: " + reason
		})
		return updated
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m.mu.RLock()
	nav := m.browserNav
	m.mu.RUnlock()
	if nav == nil {
		nav = browserWindowTarget{}
	}
	if err := nav.Navigate(ctx, deviceID, url); err != nil {
		updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
			s.Status = "navigate-failed"
			s.Note = fmt.Sprintf("Browser opened but navigate to %s failed: %v", url, err)
		})
		return updated
	}
	updated, _ := m.Update(session.ID, func(s *RemoteRuntimeSession) {
		s.Status = "control-ready"
		s.Note = fmt.Sprintf("Browser opened. Navigated to %s.", url)
	})
	return updated
}

// iosSimBuildArgs returns the xcodebuild invocation that builds an RN/Expo iOS
// app for a SIMULATOR. Deliberately expo-CLI-independent — we drive xcodebuild +
// simctl directly, both first-party Apple tools, so nothing hinges on a
// third-party CLI's licensing or version churn.
//
// The destination is the GENERIC `platform=iOS Simulator`, never a specific
// device udid: on Xcode 26.4 `-destination id=<udid>` fails to enumerate a
// simctl-booted device ("Unable to find a destination matching …"), but the
// generic destination resolves and produces a Debug-iphonesimulator .app we then
// `simctl install` onto the exact booted device. Debug config keeps the app on
// Metro so a code patch Fast-Refreshes sub-second — the whole point of streaming
// a sim. CODE_SIGNING_ALLOWED=NO because simulator builds don't need signing.
//
// Split out for unit-testing: the arg vector is the contract.
//
// ARCHS is pinned to the HOST's native simulator arch (arm64 on Apple Silicon,
// x86_64 on Intel). The generic destination otherwise builds BOTH slices, and on
// an Apple Silicon Mac the x86_64 sim slice fails to compile (some pods — fmt,
// etc. — don't build x86_64 under this toolchain), which is a real cold-build
// failure the hardware test caught. The booted sim runs the host arch anyway, so
// a single-arch build is both correct and faster.
func iosSimBuildArgs(workspace, scheme, derivedData, arch string) []string {
	return []string{
		"xcodebuild",
		"-workspace", workspace,
		"-scheme", scheme,
		"-configuration", "Debug",
		"-destination", "generic/platform=iOS Simulator",
		"-derivedDataPath", derivedData,
		"ARCHS=" + arch,
		"ONLY_ACTIVE_ARCH=NO",
		"CODE_SIGNING_ALLOWED=NO",
		"build",
	}
}

// hostSimulatorArch maps the Go host arch to the xcodebuild ARCHS value for the
// native simulator slice.
func hostSimulatorArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return "arm64"
}

// discoverIOSWorkspaceScheme finds the .xcworkspace under <workDir>/ios and the
// scheme to build. For an Expo/RN prebuild the scheme name equals the workspace
// basename (e.g. Talos.xcworkspace → scheme "Talos"); we return that and let
// xcodebuild validate it.
func discoverIOSWorkspaceScheme(workDir string) (workspace, scheme string, err error) {
	iosDir := filepath.Join(workDir, "ios")
	entries, err := os.ReadDir(iosDir)
	if err != nil {
		return "", "", fmt.Errorf("no ios/ dir in %s: %w", workDir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".xcworkspace") {
			ws := filepath.Join(iosDir, e.Name())
			return ws, strings.TrimSuffix(e.Name(), ".xcworkspace"), nil
		}
	}
	return "", "", fmt.Errorf("no .xcworkspace under %s/ios (run prebuild first)", workDir)
}

// iosProjectPresent reports whether workDir already has a buildable iOS project
// (an .xcworkspace or .xcodeproj under ios/). Used to make scaffolding idempotent.
func iosProjectPresent(workDir string) bool {
	iosDir := filepath.Join(workDir, "ios")
	entries, err := os.ReadDir(iosDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".xcworkspace") || strings.HasSuffix(e.Name(), ".xcodeproj") {
			return true
		}
	}
	return false
}

// ensureWebPlatformScaffold generates the web/ platform a guest needs for the
// browser streaming surface (the pragmatic RN/Flutter default) when it's missing
// — like ios/, it's generated and typically not committed. Idempotent. Currently
// framework-specific for Flutter (`flutter create . --platforms=web`); Expo/RN
// serves web straight from Metro once react-native-web + react-dom are installed
// (an `npm install` concern, not a scaffold), so no-op there. Encoded from the
// 2026-07-21 bring-up where todo-flutter shipped with no web/ and `flutter run -d
// web-server` would have dead-ended.
func ensureWebPlatformScaffold(ctx context.Context, workDir, framework string, emit func(string)) error {
	if emit == nil {
		emit = func(string) {}
	}
	fw := strings.ToLower(strings.TrimSpace(framework))
	if fw == "flutter" || fileExists(filepath.Join(workDir, "pubspec.yaml")) {
		if fileExists(filepath.Join(workDir, "web", "index.html")) {
			return nil
		}
		emit("no web/ platform — scaffolding with `flutter create . --platforms=web`")
		cmd := exec.CommandContext(ctx, "flutter", "create", ".", "--platforms=web")
		cmd.Dir = workDir
		hardenBuildProcessGroup(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("flutter create (web scaffold) failed: %s", tailStr(string(out), 400))
		}
	}
	return nil
}

// hardenBuildProcessGroup makes a build command own its process group and, on
// context cancel, SIGKILL the WHOLE descendant tree. xcodebuild/gradle/flutter/
// expo-prebuild fork heavy children (clang, swift-frontend, impellerc, dart2js,
// node) that a plain ctx-cancel would orphan. On 2026-07-21 an abandoned build's
// orphaned tree kept running for a long time, starved this 8GB box, and OOM-
// killed (-9) a later shader compile. Group-killing on cancel closes that class.
func hardenBuildProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	setProcGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = killProcessGroup(cmd.Process.Pid, "KILL")
		return cmd.Process.Kill()
	}
	// Give the group a moment to die after cancel before Wait returns; without
	// this a slow-dying child can outlive the parent's Wait.
	cmd.WaitDelay = 5 * time.Second
}

// excludeFromSpotlight drops a `.metadata_never_index` marker into dir so macOS
// Spotlight (mds/mds_stores) never indexes it. Build-output trees churn
// constantly and indexing them is pure waste that can pin a core — a real
// resource drain observed on the mini (2026-07-21). Best-effort, no sudo, no-op
// off darwin and on any error.
func excludeFromSpotlight(dir string) {
	if runtime.GOOS != "darwin" || strings.TrimSpace(dir) == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, ".metadata_never_index"), nil, 0o644)
}

// ensureIOSScaffold generates the ios/ project a guest app needs to build for the
// simulator when it is missing — the platform folder is generated, not committed,
// so a fresh clone frequently lacks it. Idempotent (returns immediately if a
// project is already there). Framework-aware:
//   - Expo/RN  → `npx expo prebuild --platform ios --no-install`
//   - Flutter  → `flutter create . --platforms=ios`
//   - Swift w/ project.yml (XcodeGen) → `xcodegen generate`
//
// Every branch is a real 2026-07-21 bring-up incident: todo-flutter shipped with
// no ios/ ("Expected ios/Runner.xcodeproj … missing"), todo-swift shipped only a
// project.yml. Encoded so any model — not just this session — self-heals instead
// of dead-ending.
func ensureIOSScaffold(ctx context.Context, workDir, framework string, emit func(string)) error {
	if iosProjectPresent(workDir) {
		return nil
	}
	if emit == nil {
		emit = func(string) {}
	}
	fw := strings.ToLower(strings.TrimSpace(framework))

	// Flutter: ios/ generated by `flutter create`.
	if fw == "flutter" || fileExists(filepath.Join(workDir, "pubspec.yaml")) {
		emit("no ios/ project — scaffolding with `flutter create . --platforms=ios`")
		cmd := exec.CommandContext(ctx, "flutter", "create", ".", "--platforms=ios")
		cmd.Dir = workDir
		hardenBuildProcessGroup(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("flutter create (ios scaffold) failed: %s", tailStr(string(out), 400))
		}
		return nil
	}

	// Swift XcodeGen project: a project.yml with no generated .xcodeproj.
	if fileExists(filepath.Join(workDir, "project.yml")) {
		if _, err := exec.LookPath("xcodegen"); err != nil {
			return fmt.Errorf("project.yml present but no .xcodeproj and xcodegen is not installed (brew install xcodegen)")
		}
		emit("no .xcodeproj — generating from project.yml with `xcodegen generate`")
		cmd := exec.CommandContext(ctx, "xcodegen", "generate")
		cmd.Dir = workDir
		hardenBuildProcessGroup(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("xcodegen generate failed: %s", tailStr(string(out), 400))
		}
		return nil
	}

	// Expo / React Native: ios/ generated by prebuild.
	emit("no ios/ project — scaffolding with `npx expo prebuild --platform ios --no-install`")
	cmd := exec.CommandContext(ctx, "npx", "expo", "prebuild", "--platform", "ios", "--no-install")
	cmd.Dir = workDir
	hardenBuildProcessGroup(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("expo prebuild (ios scaffold) failed: %s", tailStr(string(out), 400))
	}
	return nil
}

// isRNGuestRuntimeTarget reports whether a target can run a debug RN/Expo guest
// app with Metro Fast Refresh. Apple simulator families use xcodebuild+simctl;
// Android emulator/redroid/physical devices use Gradle+adb.
func isRNSimulatorTarget(targetID string) bool {
	switch targetID {
	case "ios-simulator", "ipados-simulator", "watchos-simulator", "tvos-simulator", "visionos-simulator",
		"android-emulator", "android-device", "android-wear", "android-tv", "android-xr", "android-auto", remoteRuntimeRedroidTargetID:
		return true
	}
	return false
}

func rnGuestBuildPlatform(targetID string) string {
	switch targetID {
	case "android-emulator", "android-device", "android-wear", "android-tv", "android-xr", "android-auto", remoteRuntimeRedroidTargetID:
		return "android"
	case "tvos-simulator":
		return "tvos"
	default:
		return "ios"
	}
}

// buildAndLaunchRNInSimulator dispatches the guest RN build to the platform path:
// Apple sims → xcodebuild+simctl (macOS); Android emulator/redroid → gradle+adb
// (runs on Linux too, which is the Cloud-Workspace normie case — an iPhone client
// streaming a Linux-hosted redroid Android). First-party tools only, no expo CLI.
func (s *HTTPServer) buildAndLaunchRNInSimulator(ctx context.Context, session RemoteRuntimeSession, workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return fmt.Errorf("RN simulator run needs a workDir")
	}
	// Self-heal breadcrumb: emit the WebRTC toolchain gaps up front so even a
	// weak model driving this sees exactly what to fix (or calls webrtc_doctor
	// heal). Fast inventory only (no simctl timing on the hot path).
	if s != nil && s.devServerMgr != nil {
		for _, line := range remediationSummary(probeWebRTCDeps(ctx, true)) {
			s.devServerMgr.EmitLog("[webrtc-doctor] " + line)
		}
	}
	switch rnGuestBuildPlatform(session.TargetID) {
	case "android":
		return s.buildAndLaunchRNAndroid(ctx, session, workDir)
	case "tvos":
		return s.buildAndLaunchTVOSApp(ctx, session, workDir)
	}
	return s.buildAndLaunchRNiOS(ctx, session, workDir)
}

// buildAndLaunchTVOSApp is the native Dogfood lane for Apple TV. A tvOS
// viewer must run Yaver's actual SwiftUI tvOS target on the render Mac, not an
// iPhone RN build and not a browser-window substitute. The session has already
// claimed and booted the exact simulator UDID, so build/install/launch stays on
// that device and the existing WebRTC control lease owns all input.
func (s *HTTPServer) buildAndLaunchTVOSApp(ctx context.Context, session RemoteRuntimeSession, workDir string) error {
	udid := strings.TrimSpace(session.DeviceID)
	if udid == "" {
		return fmt.Errorf("tvOS session has no booted simulator device id")
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("tvOS simulator builds need macOS with Xcode")
	}
	workDir = filepath.Clean(strings.TrimSpace(workDir))
	project := filepath.Join(workDir, "YaverTV.xcodeproj")
	if _, err := os.Stat(project); err != nil {
		if _, specErr := os.Stat(filepath.Join(workDir, "project.yml")); specErr != nil {
			return fmt.Errorf("no YaverTV.xcodeproj or project.yml in %s", workDir)
		}
		if _, lookErr := exec.LookPath("xcodegen"); lookErr != nil {
			return fmt.Errorf("YaverTV.xcodeproj is not generated and xcodegen is missing; install it with `brew install xcodegen`, then retry")
		}
		generate := exec.CommandContext(ctx, "xcodegen", "generate")
		generate.Dir = workDir
		hardenBuildProcessGroup(generate)
		if out, genErr := generate.CombinedOutput(); genErr != nil {
			return fmt.Errorf("xcodegen generate failed: %s", tailStr(string(out), 600))
		}
	}

	derivedData := filepath.Join(os.TempDir(), "yaver-tvos-dogfood")
	excludeFromSpotlight(derivedData)
	emit := func(message string) {
		if s != nil && s.devServerMgr != nil {
			s.devServerMgr.EmitLog("[tvos-dogfood] " + message)
		}
	}
	emit("building YaverTV for the claimed Apple TV Simulator — Debug, WebRTC control enabled")
	args := tvOSSimulatorBuildArgs(project, udid, derivedData)
	build := exec.CommandContext(ctx, "xcodebuild", args...)
	build.Dir = workDir
	hardenBuildProcessGroup(build)
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		return fmt.Errorf("tvOS simulator build failed: %s", tailStr(string(out), 800))
	}

	appPath := filepath.Join(derivedData, "Build", "Products", "Debug-appletvsimulator", "Yaver.app")
	if _, err := os.Stat(appPath); err != nil {
		return fmt.Errorf("tvOS build succeeded but %s was not produced", appPath)
	}
	emit("installing Yaver.app into the claimed Apple TV Simulator")
	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "install", udid, appPath).CombinedOutput(); err != nil {
		return fmt.Errorf("simctl install tvOS app failed: %s", tailStr(string(out), 600))
	}
	bundleID, err := bundleIDFromApp(appPath)
	if err != nil {
		return fmt.Errorf("read tvOS bundle id: %w", err)
	}
	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "launch", udid, bundleID).CombinedOutput(); err != nil {
		return fmt.Errorf("simctl launch %s failed: %s", bundleID, tailStr(string(out), 600))
	}
	emit("launched " + bundleID + " in Apple TV Simulator; WebRTC stream and full remote control are live")
	return nil
}

func tvOSSimulatorBuildArgs(project, udid, derivedData string) []string {
	return []string{
		"-project", project,
		"-scheme", "YaverTV",
		"-configuration", "Debug",
		"-sdk", "appletvsimulator",
		"-destination", "id=" + udid,
		"-derivedDataPath", derivedData,
		"-jobs", "2",
		// Both packages are pinned public GitHub dependencies. Asking SwiftPM
		// to consult the login keychain can block a headless agent forever in
		// SecItemCopyMatching even though no credential is needed. The netrc
		// provider is non-interactive; public fetch still works, and a private
		// dependency fails with a named auth error instead of a hidden prompt.
		"-packageAuthorizationProvider", "netrc",
		"-onlyUsePackageVersionsFromResolvedFile",
		"CODE_SIGNING_ALLOWED=NO",
		"build",
	}
}

// buildAndLaunchRNiOS builds the RN project into the session's booted Apple
// simulator and launches it in dev mode via first-party Apple tools only
// (xcodebuild + simctl, no expo CLI): build for the generic simulator
// destination, find the produced .app, `simctl install` it onto the exact booted
// device, read its bundle id, and `simctl launch`. Long running (a cold build is
// minutes); the caller runs it off the request path and streams progress.
func (s *HTTPServer) buildAndLaunchRNiOS(ctx context.Context, session RemoteRuntimeSession, workDir string) error {
	udid := strings.TrimSpace(session.DeviceID)
	if udid == "" {
		return fmt.Errorf("session has no booted simulator device id")
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("iOS simulator builds need macOS")
	}

	// Self-heal: a freshly-cloned Expo/RN guest often has no ios/ project at all
	// (it's generated by prebuild, and .gitignore'd). discoverIOSWorkspaceScheme
	// would dead-end at "run prebuild first" — so run it. Idempotent: if ios/ with
	// an .xcworkspace already exists this returns immediately. Encoded here because
	// a real clone of a third-party app (2026-07-21 bring-up) hit exactly this and
	// a weak model would have no idea it needed to prebuild.
	scaffoldEmit := func(msg string) {
		if s != nil && s.devServerMgr != nil {
			s.devServerMgr.EmitLog("[rn-sim] " + msg)
		}
	}
	if err := ensureIOSScaffold(ctx, workDir, session.Framework, scaffoldEmit); err != nil {
		return err
	}

	workspace, scheme, err := discoverIOSWorkspaceScheme(workDir)
	if err != nil {
		return err
	}
	derivedData := filepath.Join(os.TempDir(), "yaver-rnsim-"+scheme)
	// Resource-aware: keep Spotlight (mds_stores) from indexing the churning
	// DerivedData tree. On the mini a post-build reindex pinned mds_stores at
	// ~237% CPU and starved the very build/stream we were timing (2026-07-21).
	excludeFromSpotlight(derivedData)
	emit := func(msg string) {
		if s != nil && s.devServerMgr != nil {
			s.devServerMgr.EmitLog("[rn-sim] " + msg)
		}
	}
	emit(fmt.Sprintf("building %s (scheme %s) for %s — Debug, Metro Fast Refresh", basename(workDir), scheme, session.TargetLabel))

	args := iosSimBuildArgs(workspace, scheme, derivedData, hostSimulatorArch())
	build := exec.CommandContext(ctx, args[0], args[1:]...)
	build.Dir = workDir
	hardenBuildProcessGroup(build)
	if out, err := build.CombinedOutput(); err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 600 {
			tail = tail[len(tail)-600:]
		}
		emit("xcodebuild failed: " + tail)
		return fmt.Errorf("xcodebuild simulator build failed: %w", err)
	}

	// Find the built .app in the simulator products dir.
	productsDir := filepath.Join(derivedData, "Build", "Products", "Debug-iphonesimulator")
	appPath, err := findFirstDotApp(productsDir)
	if err != nil {
		return fmt.Errorf("locate built .app: %w", err)
	}
	emit("built " + filepath.Base(appPath) + "; installing into the simulator")

	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "install", udid, appPath).CombinedOutput(); err != nil {
		return fmt.Errorf("simctl install failed: %s", strings.TrimSpace(string(out)))
	}
	bundleID, err := bundleIDFromApp(appPath)
	if err != nil {
		return fmt.Errorf("read bundle id: %w", err)
	}

	// Metro MUST be running before we launch, or a Debug build shows the RN red
	// screen "No script URL provided … unsanitizedScriptURLString = (null)" — it
	// has no JS bundle to load. This is the dev server that also gives Fast
	// Refresh, so start it here (idempotent; reuses a running one for this
	// workDir). Observed exactly this red screen on the mini before wiring it.
	emit("starting Metro so the app can load its JS bundle (and Fast-Refresh)…")
	if err := s.ensureDevServerForProject(workDir, session.Framework, "ios"); err != nil {
		emit("warning: Metro did not start (" + err.Error() + ") — the app will show the RN 'No script URL' screen until it does")
	}

	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "launch", udid, bundleID).CombinedOutput(); err != nil {
		return fmt.Errorf("simctl launch %s failed: %s", bundleID, strings.TrimSpace(string(out)))
	}
	emit("launched " + bundleID + " — running against Metro in the simulator, streaming; edit code and Fast Refresh applies it live")
	return nil
}

// findFirstDotApp returns the first *.app bundle directly under dir.
func findFirstDotApp(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".app") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no .app in %s", dir)
}

// bundleIDFromApp reads CFBundleIdentifier from a built .app's Info.plist.
func bundleIDFromApp(appPath string) (string, error) {
	out, err := exec.Command("defaults", "read", filepath.Join(appPath, "Info"), "CFBundleIdentifier").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// androidGradleAssembleArgs is the first-party (Android SDK, no expo CLI) build
// of a debug APK. Debug keeps the app on Metro so a code patch Fast-Refreshes
// live. Runs on Linux too — which is the Cloud Workspace path where a redroid
// Android is streamed to an Apple client.
func androidGradleAssembleArgs() []string {
	return []string{"./gradlew", ":app:assembleDebug"}
}

// buildAndLaunchRNAndroid builds a debug APK with gradle and installs+launches it
// on the session's adb-reachable device — an Android emulator (macOS/local) OR a
// redroid container (Linux Cloud Workspace). Both are just an adb serial once
// connected, so this one path serves the "Apple client, Linux server" case.
func androidMetroReverseArgs(serial string) []string {
	return []string{"-s", strings.TrimSpace(serial), "reverse", "tcp:8081", "tcp:8081"}
}

func (s *HTTPServer) buildAndLaunchRNAndroid(ctx context.Context, session RemoteRuntimeSession, workDir string) error {
	serial := strings.TrimSpace(session.DeviceID)
	if serial == "" {
		return fmt.Errorf("android session has no attached adb device serial")
	}
	adbPath, err := resolveAndroidTool("adb")
	if err != nil {
		return fmt.Errorf("adb is unavailable; run `yaver install remote-runtime`: %w", err)
	}
	androidDir := filepath.Join(workDir, "android")
	if _, err := os.Stat(filepath.Join(androidDir, "gradlew")); err != nil {
		return fmt.Errorf("no android/gradlew in %s (run prebuild first): %w", workDir, err)
	}
	emit := func(msg string) {
		if s != nil && s.devServerMgr != nil {
			s.devServerMgr.EmitLog("[rn-sim/android] " + msg)
		}
	}
	emit(fmt.Sprintf("gradle assembleDebug for %s → %s (Metro Fast Refresh)", basename(workDir), session.TargetLabel))

	args := androidGradleAssembleArgs()
	build := exec.CommandContext(ctx, args[0], args[1:]...)
	build.Dir = androidDir
	build.Env = append(os.Environ(), "GRADLE_OPTS=-Dorg.gradle.daemon=false")
	hardenBuildProcessGroup(build)
	if out, err := build.CombinedOutput(); err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 600 {
			tail = tail[len(tail)-600:]
		}
		emit("gradle failed: " + tail)
		return fmt.Errorf("gradle assembleDebug failed: %w", err)
	}
	apk, err := findFirstDebugAPK(androidDir)
	if err != nil {
		return fmt.Errorf("locate debug apk: %w", err)
	}
	emit("built " + filepath.Base(apk) + "; adb install onto " + serial)
	if out, err := exec.CommandContext(ctx, adbPath, "-s", serial, "install", "-r", apk).CombinedOutput(); err != nil {
		return fmt.Errorf("adb install failed: %s", strings.TrimSpace(string(out)))
	}
	// A remote physical device reaches Metro through its edge host, not through
	// the thin client. This is idempotent for emulators/redroid and essential
	// for a USB-connected phone or tablet running a debug RN build.
	if out, err := exec.CommandContext(ctx, adbPath, androidMetroReverseArgs(serial)...).CombinedOutput(); err != nil {
		return fmt.Errorf("adb reverse tcp:8081 failed: %s", strings.TrimSpace(string(out)))
	}
	pkg, activity := readAndroidLaunchInfo(apk)
	if pkg == "" {
		return fmt.Errorf("could not read package name from %s", apk)
	}
	comp := pkg + "/" + activity
	if activity == "" {
		comp = pkg
	}
	if out, err := exec.CommandContext(ctx, adbPath, "-s", serial, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1").CombinedOutput(); err != nil {
		// monkey-launch failed; try an explicit component start.
		if out2, err2 := exec.CommandContext(ctx, adbPath, "-s", serial, "shell", "am", "start", "-n", comp).CombinedOutput(); err2 != nil {
			return fmt.Errorf("adb launch failed: %s / %s", strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	emit("launched " + pkg + " — running on the Android device, streaming; edit code and Metro Fast Refresh applies it live")
	return nil
}

func (s *HTTPServer) buildAndLaunchNativeRuntimeProject(ctx context.Context, session RemoteRuntimeSession) error {
	switch strings.ToLower(strings.TrimSpace(session.Framework)) {
	case "swift":
		return s.buildAndLaunchSwiftRuntimeProject(ctx, session)
	case "kotlin":
		return s.buildAndLaunchKotlinRuntimeProject(ctx, session)
	default:
		return fmt.Errorf("native runtime launch does not support framework %q", session.Framework)
	}
}

func discoverNativeIOSContainer(workDir string) (flag, container, scheme string, err error) {
	for _, dir := range []string{workDir, filepath.Join(workDir, "ios")} {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			continue
		}
		for _, suffix := range []string{".xcworkspace", ".xcodeproj"} {
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), suffix) {
					continue
				}
				kind := "-project"
				if suffix == ".xcworkspace" {
					kind = "-workspace"
				}
				return kind, filepath.Join(dir, entry.Name()), strings.TrimSuffix(entry.Name(), suffix), nil
			}
		}
	}
	return "", "", "", fmt.Errorf("no .xcworkspace or .xcodeproj found in %s or its ios directory", workDir)
}

func (s *HTTPServer) buildAndLaunchSwiftRuntimeProject(ctx context.Context, session RemoteRuntimeSession) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("Swift iOS simulator launch needs the paired macOS runtime host")
	}
	workDir := filepath.Clean(strings.TrimSpace(session.WorkDir))
	if workDir == "." || strings.TrimSpace(session.DeviceID) == "" {
		return fmt.Errorf("Swift runtime session is missing its checkout or simulator id")
	}
	flag, container, scheme, err := discoverNativeIOSContainer(workDir)
	if err != nil {
		if scaffoldErr := ensureIOSScaffold(ctx, workDir, "swift", func(line string) {
			if s.devServerMgr != nil {
				s.devServerMgr.EmitLog("[native-shortcut/ios] " + line)
			}
		}); scaffoldErr != nil {
			return scaffoldErr
		}
		flag, container, scheme, err = discoverNativeIOSContainer(workDir)
		if err != nil {
			return err
		}
	}
	derivedData := filepath.Join(os.TempDir(), "yaver-native-shortcut-"+browserShortcutSlug(scheme))
	excludeFromSpotlight(derivedData)
	args := []string{
		flag, container, "-scheme", scheme, "-configuration", "Debug",
		"-destination", "generic/platform=iOS Simulator", "-derivedDataPath", derivedData,
		"-jobs", "2", "ARCHS=" + hostSimulatorArch(), "ONLY_ACTIVE_ARCH=NO",
		"CODE_SIGNING_ALLOWED=NO", "build",
	}
	if s.devServerMgr != nil {
		s.devServerMgr.EmitLog("[native-shortcut/ios] building " + scheme + " for iOS Simulator (2 jobs)")
	}
	build := exec.CommandContext(ctx, "xcodebuild", args...)
	build.Dir = workDir
	hardenBuildProcessGroup(build)
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		return fmt.Errorf("xcodebuild simulator build failed: %s", tailStr(string(out), 900))
	}
	appPath, err := findFirstDotApp(filepath.Join(derivedData, "Build", "Products", "Debug-iphonesimulator"))
	if err != nil {
		return fmt.Errorf("locate built iOS app: %w", err)
	}
	udid := strings.TrimSpace(session.DeviceID)
	if out, installErr := exec.CommandContext(ctx, "xcrun", "simctl", "install", udid, appPath).CombinedOutput(); installErr != nil {
		return fmt.Errorf("simctl install failed: %s", tailStr(string(out), 700))
	}
	bundleID, err := bundleIDFromApp(appPath)
	if err != nil {
		return fmt.Errorf("read built app bundle id: %w", err)
	}
	if out, launchErr := exec.CommandContext(ctx, "xcrun", "simctl", "launch", udid, bundleID).CombinedOutput(); launchErr != nil {
		return fmt.Errorf("simctl launch failed: %s", tailStr(string(out), 700))
	}
	if s.devServerMgr != nil {
		s.devServerMgr.EmitLog("[native-shortcut/ios] launched " + bundleID + "; WebRTC viewer is live")
	}
	return nil
}

func (s *HTTPServer) buildAndLaunchKotlinRuntimeProject(ctx context.Context, session RemoteRuntimeSession) error {
	workDir := filepath.Clean(strings.TrimSpace(session.WorkDir))
	serial := strings.TrimSpace(session.DeviceID)
	if workDir == "." || serial == "" {
		return fmt.Errorf("Kotlin runtime session is missing its checkout or emulator id")
	}
	androidDir := workDir
	if _, err := os.Stat(filepath.Join(androidDir, "gradlew")); err != nil {
		androidDir = filepath.Join(workDir, "android")
	}
	if _, err := os.Stat(filepath.Join(androidDir, "gradlew")); err != nil {
		return fmt.Errorf("no gradlew found in %s or its android directory", workDir)
	}
	if s.devServerMgr != nil {
		s.devServerMgr.EmitLog("[native-shortcut/android] assembling debug app with a 2 GiB heap and 2 workers")
	}
	build := exec.CommandContext(ctx, "./gradlew", "--no-daemon", "--max-workers=2", "-Dorg.gradle.jvmargs="+nativeShortcutGradleDaemonJVMArgs, ":app:assembleDebug")
	build.Dir = androidDir
	build.Env = nativeShortcutGradleEnv(augmentEnv(nil))
	hardenBuildProcessGroup(build)
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		return fmt.Errorf("Gradle debug build failed: %s", tailStr(string(out), 900))
	}
	apk, err := findFirstDebugAPK(androidDir)
	if err != nil {
		return fmt.Errorf("locate debug APK: %w", err)
	}
	if out, installErr := exec.CommandContext(ctx, "adb", "-s", serial, "install", "-r", "-t", apk).CombinedOutput(); installErr != nil {
		return fmt.Errorf("adb install failed: %s", tailStr(string(out), 700))
	}
	pkg, activity := readAndroidLaunchInfo(apk)
	if pkg == "" {
		return fmt.Errorf("could not read the Android application id from %s", apk)
	}
	if out, launchErr := exec.CommandContext(ctx, "adb", "-s", serial, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1").CombinedOutput(); launchErr != nil {
		component := pkg
		if activity != "" {
			component += "/" + activity
		}
		if out2, explicitErr := exec.CommandContext(ctx, "adb", "-s", serial, "shell", "am", "start", "-n", component).CombinedOutput(); explicitErr != nil {
			return fmt.Errorf("adb launch failed: %s / %s", tailStr(string(out), 350), tailStr(string(out2), 350))
		}
	}
	if s.devServerMgr != nil {
		s.devServerMgr.EmitLog("[native-shortcut/android] launched " + pkg + "; WebRTC viewer is live")
	}
	return nil
}

const nativeShortcutGradleDaemonJVMArgs = "-Xmx2g -XX:MaxMetaspaceSize=512m"

func nativeShortcutGradleEnv(base []string) []string {
	// --no-daemon still launches a single-use build daemon. Budget the small
	// Gradle client separately from that daemon so they cannot each reserve
	// 2 GiB and push a 4 GiB Ubuntu box into the OOM killer. withEnvOverride
	// also removes an ambient duplicate whose winner would otherwise depend on
	// libc/JVM environment parsing.
	return withEnvOverride(base, "GRADLE_OPTS=-Xmx512m -XX:MaxMetaspaceSize=256m")
}

// findFirstDebugAPK returns the first debug APK gradle produced.
func findFirstDebugAPK(androidDir string) (string, error) {
	dir := filepath.Join(androidDir, "app", "build", "outputs", "apk", "debug")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".apk") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no .apk in %s", dir)
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
		// Exclusivity needs a way to be LOST, or the machine looks full while
		// idle: a closed tab / slept phone / timed-out test leaves a session
		// holding a simulator with nobody watching. See remote_runtime_reaper.go.
		//
		// The sweep runs as a CUSTODIAN WARDEN rather than its own goroutine, so
		// what it does reaches the user's screen instead of only a launchd log —
		// that visibility is the whole reason custodian.go exists. Same cadence,
		// same ReapAbandonedSessions, one feed.
		AttachRuntimeWarden(s.remoteRuntimeMgr)
	}
	// Keep the dev-server pointer fresh even after lazy allocation — the
	// devServerMgr on HTTPServer is itself lazy for some code paths, so a
	// nil-check-and-attach on every access covers a browser-window Create
	// that happens after the dev-server manager was created.
	if s.remoteRuntimeMgr.devManager == nil && s.devServerMgr != nil {
		s.remoteRuntimeMgr.SetDevServerManager(s.devServerMgr)
	}
	s.remoteRuntimeMgr.SetICEServerProvider(s.iceServersForHTTPServer)
	s.remoteRuntimeMgr.SetVibingWebRTCControlHandler(s.handleVibingWebRTCControl)
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
	refresh := r.URL.Query().Get("refresh") == "1" || strings.EqualFold(r.URL.Query().Get("refresh"), "true")
	jsonReply(w, http.StatusOK, remoteRuntimeCapabilitiesForProjectCached(workDir, framework, refresh))
}

// remoteRuntimeCreateError replies to a session-create/attach failure, carrying
// the typed CapabilityGap when the failure has one.
//
// Degrades to exactly the previous behaviour for every other error, so nothing
// that already worked changes shape.
func remoteRuntimeCreateError(w http.ResponseWriter, err error) {
	var bw *BrowserWindowLaunchError
	if errors.As(err, &bw) && bw.Gap != nil {
		jsonErrorWithGap(w, http.StatusBadRequest, bw.Error(), bw.Gap)
		return
	}
	jsonError(w, http.StatusBadRequest, err.Error())
}

func (s *HTTPServer) handleRemoteRuntimeSessions(w http.ResponseWriter, r *http.Request) {
	mgr := s.ensureRemoteRuntimeManager()
	switch r.Method {
	case http.MethodGet:
		// Roster: list live sessions, optionally filtered by the
		// project (workDir) or device a client is attached to, so a
		// surface can show "live sessions on this box" instead of
		// forcing its own capture. Each entry carries viewerCount.
		project := strings.TrimSpace(r.URL.Query().Get("project"))
		device := strings.TrimSpace(r.URL.Query().Get("device"))
		sessions := mgr.List()
		out := make([]RemoteRuntimeSession, 0, len(sessions))
		for _, sess := range sessions {
			if project != "" && !strings.Contains(sess.WorkDir, project) && filepath.Base(sess.WorkDir) != project {
				continue
			}
			if device != "" && sess.DeviceID != device {
				continue
			}
			out = append(out, mgr.stampViewerCount(sess))
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"sessions": out})
	case http.MethodPost:
		var req struct {
			WorkDir      string `json:"workDir"`
			Framework    string `json:"framework"`
			TargetID     string `json:"targetId"`
			RealDeviceID string `json:"realDeviceId,omitempty"`
			Transport    string `json:"transportMode"`
			Runner       string `json:"runner,omitempty"`
			// ClientID + Surface attribute WHO created the session, so the
			// shared-session roster can say "Kivan · phone started this" and a
			// returning surface can find the room it began. Mirrors the offer
			// path's attribution fields (remote_runtime_webrtc.go:1244-1246).
			ClientID string `json:"clientId,omitempty"`
			Surface  string `json:"surface,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		// Fall back to the X-Yaver-Surface header when the body omits it, so
		// every existing client (mobile, web, TV, watch) attributes the creator
		// without a client bump. The header is advisory metadata, never auth.
		bodySurface := strings.TrimSpace(req.Surface)
		if bodySurface == "" {
			bodySurface = r.Header.Get(surfaceHeader)
		}
		creator := remoteRuntimeCreator{
			ClientID:     strings.TrimSpace(req.ClientID),
			Surface:      string(normalizeSurface(bodySurface)),
			RealDeviceID: strings.TrimSpace(req.RealDeviceID),
		}
		session, err := mgr.CreateWith(req.WorkDir, req.Framework, req.TargetID, req.Transport, creator)
		if err == nil && strings.TrimSpace(req.Runner) != "" {
			// Optional runner override at create; empty defaults to the box's
			// primary (resolved when a feedback fix task is dispatched).
			session, _ = mgr.Update(session.ID, func(cur *RemoteRuntimeSession) { cur.Runner = strings.TrimSpace(req.Runner) })
		}
		if err != nil {
			// CARRY THE GAP. browserWindowLaunchError types the five
			// browser_window.* reasons and attaches a remedy that differs per
			// code — install a browser / install the UNCONFINED build / end the
			// process holding the profile / fix permissions / read the launch
			// output. Replying with err.Error() alone put the code back inside a
			// sentence, which is how all five ended up unread in the first place.
			remoteRuntimeCreateError(w, err)
			return
		}
		// Proxied sessions are already booted on the builder — the
		// Mac handles its own simctl boot + dims probe. The local
		// Attach() would fail trying to look up live state we don't
		// keep for proxied IDs.
		if proxy := mgr.proxiedFor(session.ID); proxy == nil {
			session, err = mgr.Attach(session.ID)
			if err != nil {
				// Attach launches the browser too, so the same typed refusal can
				// arrive here. One helper for both paths: a route that only one of
				// two entry points carries is the drift this codebase pays for.
				remoteRuntimeCreateError(w, err)
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
		WorkDir  string `json:"workDir,omitempty"`
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
	case "run-guest":
		// Build the RN/Expo guest app into the booted sim and launch it in dev
		// mode (Metro + Fast Refresh). A cold build is minutes, so it runs OFF the
		// request path in a goroutine and streams progress to the dev-server log;
		// the response returns immediately with status "building". The viewer polls
		// the session / watches the stream for readiness.
		if session.DeviceID == "" {
			jsonError(w, http.StatusBadRequest, "session has no device; run boot first")
			return
		}
		workDir := strings.TrimSpace(session.WorkDir)
		if workDir == "" {
			workDir = strings.TrimSpace(req.WorkDir)
		}
		if workDir == "" {
			jsonError(w, http.StatusBadRequest, "run-guest needs a workDir (the RN project root)")
			return
		}
		if !isRNSimulatorTarget(session.TargetID) {
			jsonError(w, http.StatusBadRequest, runGuestUnsupportedReason(session.TargetID))
			return
		}
		// In-flight guard: web Runtime Lab and the mobile Tasks tab both
		// auto-fire run-guest on every runtime_render_requested event, and a
		// runner transcript can emit render markers on many consecutive
		// output chunks. A cold build is minutes; without this claim each
		// chunk would spawn ANOTHER concurrent xcodebuild/gradle goroutine
		// on the same workDir. The claim is atomic (inside Update, under the
		// manager lock) and cannot wedge: the build goroutine below always
		// flips status off "building" within its 20-minute context, and
		// sessions are in-memory so a crash clears the state anyway.
		alreadyBuilding := false
		mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			if current.Status == "building" && current.LastCommand == "run-guest" {
				alreadyBuilding = true
				return
			}
			current.Status = "building"
			current.LastCommand = "run-guest"
			current.Note = "Building the project app in the simulator (dev mode, Fast Refresh)…"
		})
		if alreadyBuilding {
			current, _ := mgr.Get(session.ID)
			jsonReply(w, http.StatusAccepted, map[string]interface{}{
				"ok":        true,
				"sessionId": session.ID,
				"command":   "run-guest",
				"status":    "building",
				"deduped":   true,
				"note":      "A project build is already in flight for this session; not starting another.",
				"session":   current,
			})
			return
		}
		go func() {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Minute)
			defer cancel()
			err := s.buildAndLaunchRNInSimulator(bctx, session, workDir)
			s.ensureRemoteRuntimeManager().Update(session.ID, func(current *RemoteRuntimeSession) {
				if err != nil {
					current.Status = "build-failed"
					current.Note = "Project build failed: " + err.Error()
					return
				}
				current.Status = "running"
				current.Note = "Project app running in the simulator; Metro Fast Refresh live. Streaming."
			})
		}()
		updated, _ := mgr.Update(session.ID, func(_ *RemoteRuntimeSession) {})
		jsonReply(w, http.StatusAccepted, map[string]interface{}{
			"ok":        true,
			"sessionId": session.ID,
			"command":   "run-guest",
			"status":    "building",
			"session":   updated,
		})
	case "run-project":
		// Browser-installed native shortcuts are launchers, not native-to-web
		// transpilers. Build the pinned Swift/Kotlin checkout into the already
		// claimed simulator/emulator, launch it there, and let the existing
		// WebRTC peer carry pixels + input back to the phone.
		if session.DeviceID == "" {
			jsonError(w, http.StatusBadRequest, "session has no simulator or emulator device; run boot first")
			return
		}
		framework := strings.ToLower(strings.TrimSpace(session.Framework))
		if framework != "swift" && framework != "kotlin" {
			jsonError(w, http.StatusBadRequest, "run-project is reserved for native Swift and Kotlin runtime shortcuts")
			return
		}
		alreadyBuilding := false
		mgr.Update(session.ID, func(current *RemoteRuntimeSession) {
			if current.Status == "building" && current.LastCommand == "run-project" {
				alreadyBuilding = true
				return
			}
			current.Status = "building"
			current.LastCommand = "run-project"
			current.Note = "Building and launching the native project in its remote runtime…"
		})
		if !alreadyBuilding {
			go func() {
				buildCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Minute)
				defer cancel()
				err := s.buildAndLaunchNativeRuntimeProject(buildCtx, session)
				s.ensureRemoteRuntimeManager().Update(session.ID, func(current *RemoteRuntimeSession) {
					if err != nil {
						current.Status = "build-failed"
						current.Note = "Native project launch failed: " + err.Error()
						return
					}
					current.Status = "running"
					current.Note = "Native project is running in the simulator/emulator and streaming."
				})
			}()
		}
		updated, _ := mgr.Get(session.ID)
		jsonReply(w, http.StatusAccepted, map[string]interface{}{
			"ok": true, "sessionId": session.ID, "command": "run-project",
			"status": "building", "deduped": alreadyBuilding, "session": updated,
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
		note := "Shake injected into the simulator; the project app's feedback SDK should open."
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

func runGuestUnsupportedReason(targetID string) string {
	switch targetID {
	case remoteRuntimeIOSDeviceTargetID:
		return "run-guest for ios-device needs the physical-device native path: build for iphoneos, install with devicectl, launch with devicectl, then stream/control through WebDriverAgent. Use iOS Simulator for closed-loop Fast Refresh until that path is enabled."
	case "browser-window":
		return "run-guest is for native runtime targets; browser-window uses the browser dev-server lane."
	case desktopScreenTargetID:
		return "run-guest is for mobile runtime targets; desktop-screen streams the host desktop/app directly."
	default:
		return fmt.Sprintf("run-guest not supported for target %q", targetID)
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

// runnableSubProjects lists the immediate sub-directories of a container repo that
// are themselves startable projects, so a refusal can point at them instead of
// leaving the user at a dead end.
//
// Deliberately ONE level deep and capped: the point is to hand the user a short,
// clickable set of next steps, not to walk a monorepo (which on a big tree is both
// slow and unreadable). Sorted for a stable message.
func runnableSubProjects(workDir string) []string {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
			continue
		}
		sub := filepath.Join(workDir, e.Name())
		for _, marker := range []string{"package.json", "pubspec.yaml", "Package.swift", "build.gradle", "build.gradle.kts"} {
			if _, err := os.Stat(filepath.Join(sub, marker)); err == nil {
				out = append(out, e.Name())
				break
			}
		}
	}
	sort.Strings(out)
	const maxNamed = 8
	if len(out) > maxNamed {
		out = append(out[:maxNamed], fmt.Sprintf("… and %d more", len(out)-maxNamed))
	}
	return out
}
