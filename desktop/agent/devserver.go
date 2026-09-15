package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ─── DevServer Interface ───────────────────────────────────────────────

// DevServer is the interface all dev server implementations must satisfy.
// Each framework (Expo, Flutter, Vite, Next.js, etc.) implements this.
type DevServer interface {
	// Name returns the framework identifier ("expo", "flutter", "vite", "nextjs", "custom").
	Name() string
	// Detect returns true if the working directory contains this framework's project.
	Detect(workDir string) bool
	// Start launches the dev server process. Blocks until server is ready or error.
	Start(ctx context.Context, opts DevServerOpts) error
	// Stop terminates the dev server process.
	Stop() error
	// Port returns the local port the dev server is listening on.
	Port() int
	// BundleURL returns the relative URL path the phone should load.
	// For Expo: "/dev/index.bundle?platform=ios&dev=true"
	// For Vite: "/dev/"
	BundleURL(platform string) string
	// SupportsHotReload returns true if the framework can reload without rebuild.
	SupportsHotReload() bool
	// Reload triggers a hot reload if supported.
	Reload() error
	// PreStart sets the name/port/workDir before async Start (for immediate Status).
	PreStart(name string, port int, workDir string)
	// Status returns the current state.
	Status() DevServerStatus
	// Kind classifies the dev server as web, mobile, or hybrid.
	// See devserver_kind.go.
	Kind() DevServerKind
}

// DevServerOpts configures a dev server launch.
type DevServerOpts struct {
	WorkDir  string
	Port     int               // override default port (0 = framework default)
	Platform string            // "ios", "android", "web"
	Target   DevServerTarget   // selected Yaver preview target, if any
	Env      map[string]string // extra environment variables
	Args     []string          // extra args passed to the dev server command
}

// DevServerStatus is the JSON-serializable status of a dev server.
type DevServerStatus struct {
	Framework       string        `json:"framework"`
	Kind            DevServerKind `json:"kind,omitempty"`
	Running         bool          `json:"running"`
	Serving         bool          `json:"serving"`
	ServingLabel    string        `json:"servingLabel,omitempty"`
	StopActionLabel string        `json:"stopActionLabel,omitempty"`
	Building        bool          `json:"building,omitempty"` // true during native compilation (expo run:ios, etc.)
	Port            int           `json:"port"`
	BundleURL       string        `json:"bundleUrl"`
	// Resources this dev server holds — the port it ACTUALLY bound, plus any
	// device it claimed. Same VibeResourceView shape as /vibe/sessions, so every
	// client surface renders "flutter on :9100 · iPhone 15" with one formatter
	// instead of each screen inventing its own.
	//
	// This is the client's answer to "which port am I on?": the preview always
	// loads through the /dev/ proxy, so a substituted port needs no client change
	// — but the user staring at a log line that says 9103 deserves to be told why.
	Resources []VibeResourceView `json:"resources,omitempty"`
	// PortSubstituted is true when the framework's canonical port was taken and
	// Yaver bound a different one.
	PortSubstituted bool `json:"portSubstituted,omitempty"`
	// PreferredPort is what the framework would have used by default. Only set
	// when it differs from Port.
	PreferredPort int `json:"preferredPort,omitempty"`
	// VibeSessionID ties this dev server to the co-vibe session holding it, so a
	// client can fetch the roster for the project it is looking at.
	VibeSessionID string `json:"vibeSessionId,omitempty"`
	DirectURL     string `json:"directUrl,omitempty"`
	DeepLink      string `json:"deepLink,omitempty"`
	DevMode       string `json:"devMode,omitempty"` // "dev-client", "web", "expo-go", "" (for non-Expo)
	StartedAt     string `json:"startedAt,omitempty"`
	Error         string `json:"error,omitempty"`
	// RecentLogs gives polling transports the same bounded tail carried by
	// /dev/events snapshots. The public relay's WebSocket fallback cannot
	// proxy streaming responses, so SDK Dogfood would otherwise show a dead
	// Browser Logs panel whenever QUIC is unavailable.
	RecentLogs []string `json:"recentLogs,omitempty"`
	// CapabilityGap mirrors the SSE error frame's Gap for clients that poll
	// status instead of holding the stream open. DevPreview gates its
	// /dev/events subscription on running||building, so on a hard start
	// failure the stream is closed exactly when the gap is emitted — the
	// route would be invisible on that surface without this field.
	CapabilityGap     *CapabilityGap `json:"capabilityGap,omitempty"`
	PreviewHealth     *PreviewHealth `json:"previewHealth,omitempty"`
	PID               int            `json:"pid,omitempty"`
	WorkDir           string         `json:"workDir,omitempty"`
	HotReload         bool           `json:"hotReload"`
	TargetDeviceID    string         `json:"targetDeviceId,omitempty"`
	TargetDeviceName  string         `json:"targetDeviceName,omitempty"`
	TargetDeviceClass string         `json:"targetDeviceClass,omitempty"`
	IOSInstallMethod  string         `json:"iosInstallMethod,omitempty"`
	IOSInstallReason  string         `json:"iosInstallReason,omitempty"`
	// WebPort is non-zero when a sibling Expo Web preview is running
	// alongside Metro (--dev-client). Browser iframe routes through
	// /dev-web/* to this port while /dev/index.bundle?platform=...
	// continues to hit Metro on `Port`. Zero means "no web sibling
	// running"; the Web Reload tab shows a "Start Web Preview" CTA in
	// that state. Only populated for the Expo framework — other
	// frameworks either serve web directly (Vite, Next) or are mobile-
	// only (Flutter mobile, Swift, Kotlin).
	WebPort int `json:"webPort,omitempty"`
}

type PreviewHealth struct {
	State               string   `json:"state"` // starting | healthy | needs_project_fix | infrastructure_gap | disconnected | unknown
	CanOfferProjectFix  bool     `json:"canOfferProjectFix"`
	Severity            string   `json:"severity,omitempty"` // info | warn | error
	Reason              string   `json:"reason,omitempty"`
	SignalSource        string   `json:"signalSource,omitempty"`
	RelevantLogLines    []string `json:"relevantLogLines,omitempty"`
	HasDeterministicFix bool     `json:"hasDeterministicFix,omitempty"`
	// PaintSignal advertises an OPERATIONAL wire capability, not merely an
	// agent version: HTML served through /dev/, /dev-web/ and the static web
	// bundle contains the in-frame probe that posts yaver-rendered to its
	// embedder. Older agents omit this field. RN-web must not place a permanent
	// opaque cover over their cross-origin iframe while waiting for a message
	// those agents cannot emit.
	PaintSignal string `json:"paintSignal,omitempty"` // "in_frame_v1"
}

// DevServerEvent is pushed via SSE on /dev/events.
//
// Type taxonomy (the "Yaver Protocol v1 lite" living on the existing SSE
// channel — full envelope is a follow-up):
//
//	"phase"     — a discrete state transition for a topic
//	"progress"  — a percentage update for the current phase
//	"snapshot"  — the agent's current full state, emitted every 5s
//	              even when otherwise quiet (so the consumer can render
//	              from the latest snapshot and never feel "stuck")
//	"log"       — a single stdout/stderr line
//	"heartbeat" — agent is alive (kept for backwards-compat with
//	              v1.99.<=66 consumers that don't grok snapshots)
//	"ready"|"reload"|"error"|"stopped"|"file_changed"|"web-preview-starting"|
//	"starting"  — legacy event types (still emitted)
//
// Topic taxonomy:
//
//	"dev/start"      — main dev-server lifecycle (Metro/Expo/Vite/etc)
//	"webview/build"  — Expo Web sibling
//	"hermes/compile" — hermesc on the agent (per /dev/build-native)
//	"bundle/push"    — yaver-cli pushing to phone
type DevServerEvent struct {
	Type      string `json:"type"`
	Framework string `json:"framework"`
	BundleURL string `json:"bundleUrl,omitempty"`
	DeepLink  string `json:"deepLink,omitempty"`
	Message   string `json:"message,omitempty"`
	LogLine   string `json:"logLine,omitempty"` // single build output line (type="log")
	Timestamp string `json:"timestamp"`

	// type="resources": what this session now holds — the port actually bound and
	// any claimed device. Pushed the moment it is decided, so a live client learns
	// its port from the stream instead of discovering it on the next poll (or
	// worse, assuming the framework default and being wrong).
	// (Port is declared once, below, and reused by both heartbeat and resources —
	// two fields for one fact is how a client ends up reading the wrong one.)
	Resources       []VibeResourceView `json:"resources,omitempty"`
	PreferredPort   int                `json:"preferredPort,omitempty"`
	PortSubstituted bool               `json:"portSubstituted,omitempty"`

	// Heartbeat-only fields (type="heartbeat"). Emitted every 5s by
	// DevServerManager.heartbeatLoop while a dev server is running.
	// The point: Metro/Expo are quiet between bundle requests, so the
	// CONSOLE used to render "0 events, last: no events yet" forever
	// even though the box was perfectly healthy.
	Pid        int    `json:"pid,omitempty"`        // OS pid of the dev server process
	PidAlive   bool   `json:"pidAlive,omitempty"`   // true if pid responds to signal-0
	UptimeSec  int    `json:"uptimeSec,omitempty"`  // since baseDevServer.startedAt
	Port       int    `json:"port,omitempty"`       // dev server's bound port
	WorkDir    string `json:"workDir,omitempty"`    // project absolute path
	Surface    string `json:"surface,omitempty"`    // "hot-reload" | "web-reload"
	IdleSec    int    `json:"idleSec,omitempty"`    // seconds since last non-heartbeat event
	BeatNumber int    `json:"beatNumber,omitempty"` // monotonically increasing beat counter

	// Yaver Protocol v1 fields (type="phase" | "progress" | "snapshot").
	// All are omitempty so legacy event shapes still serialize cleanly.
	Topic       string  `json:"topic,omitempty"`       // "dev/start" | "webview/build" | "hermes/compile" | "bundle/push" | "webview/transport"
	Phase       string  `json:"phase,omitempty"`       // see file header
	PrevPhase   string  `json:"prevPhase,omitempty"`   // for transition validation in consumer
	Pct         float32 `json:"pct,omitempty"`         // 0..100, REAL number from compiler output
	Done        int64   `json:"done,omitempty"`        // e.g. 1247 modules / served bytes (int64 to fit multi-MB bundles)
	Total       int64   `json:"total,omitempty"`       // e.g. 2390 modules / total bytes
	Unit        string  `json:"unit,omitempty"`        // "modules" | "bytes" | "files" | "tasks"
	CurrentFile string  `json:"currentFile,omitempty"` // e.g. "node_modules/expo-router/build/Route.js"
	ProgressSrc string  `json:"progressSrc,omitempty"` // "exact" | "heuristic" | "unknown"
	EtaMs       int64   `json:"etaMs,omitempty"`       // estimated remaining millis (only when ProgressSrc=="exact")
	// Caller is the X-Yaver-Caller of the surface that originated the
	// triggering request. Threaded onto every event under that lifecycle
	// so the dashboard CONSOLE can attribute phases (`[mobile-app/1.18.15]
	// hermes/compile 73%` vs `[web-dashboard/1.1.83] webview/transport
	// streaming 24%`). Empty for heartbeat / log events.
	Caller string `json:"caller,omitempty"`

	// Snapshot-only fields (type="snapshot"). Lets a late or
	// reconnecting consumer rebuild full UI state from one event
	// instead of replaying the entire history.
	Snapshot *DevServerSnapshot `json:"snapshot,omitempty"`

	// Gap is set on type="error" when the start failed because this machine
	// is missing a capability — and it carries the ROUTE to the fix
	// (method + path + stream), not a sentence about it.
	//
	// This one field closes the case no 412 can ever catch: mgr.Start returns
	// BEFORE the process is spawned (see "Launch start in background" below),
	// so a start that dies on `exec flutter: executable file not found` has
	// already answered 200 OK. The failure is ASYNCHRONOUS by construction,
	// and /dev/events is the channel every preview surface is already
	// subscribed to. Produced by the one detector (capability_gap.go) so a
	// new gap needs no new client code.
	Gap *CapabilityGap `json:"gap,omitempty"`
}

// DevServerSnapshot is a complete picture of every active topic + the
// last known progress + the most recent log lines. Consumer renders
// from this and never feels "stuck" because a fresh one arrives every
// 5s regardless of whether anything happened.
type DevServerSnapshot struct {
	GeneratedAt   string            `json:"generatedAt"`
	Running       bool              `json:"running"`
	Framework     string            `json:"framework,omitempty"`
	Surface       string            `json:"surface,omitempty"`
	Port          int               `json:"port,omitempty"`
	WebPort       int               `json:"webPort,omitempty"`
	WorkDir       string            `json:"workDir,omitempty"`
	UptimeSec     int               `json:"uptimeSec,omitempty"`
	Pid           int               `json:"pid,omitempty"`
	PidAlive      bool              `json:"pidAlive,omitempty"`
	IdleSec       int               `json:"idleSec,omitempty"`
	Phases        map[string]string `json:"phases,omitempty"`   // topic → current phase
	Progress      *ProgressSnapshot `json:"progress,omitempty"` // most recent active progress
	WebProgress   *ProgressSnapshot `json:"webProgress,omitempty"`
	RecentLogs    []string          `json:"recentLogs,omitempty"` // last 8 stdout/stderr lines
	PreviewHealth *PreviewHealth    `json:"previewHealth,omitempty"`
	BeatNumber    int               `json:"beatNumber,omitempty"`
}

// ─── DevServer Registry ────────────────────────────────────────────────

var (
	devServerRegistry   []DevServer
	devServerRegistryMu sync.Mutex
)

func registerDevServer(ds DevServer) {
	devServerRegistryMu.Lock()
	defer devServerRegistryMu.Unlock()
	devServerRegistry = append(devServerRegistry, ds)
}

// detectDevServer auto-detects the framework for a given directory.
func detectDevServer(workDir string) DevServer {
	devServerRegistryMu.Lock()
	defer devServerRegistryMu.Unlock()
	for _, ds := range devServerRegistry {
		if ds.Detect(workDir) {
			return ds
		}
	}
	return nil
}

// getDevServerByName returns a registered dev server by framework name.
func getDevServerByName(name string) DevServer {
	devServerRegistryMu.Lock()
	defer devServerRegistryMu.Unlock()
	for _, ds := range devServerRegistry {
		if ds.Name() == name {
			return ds
		}
	}
	return nil
}

func init() {
	registerDevServer(&ExpoDevServer{})
	registerDevServer(&ReactNativeDevServer{})
	registerDevServer(&FlutterDevServer{})
	registerDevServer(&ViteDevServer{})
	registerDevServer(&NextDevServer{})
	// Tokamak/SwiftWasm. Registered LAST because its Detect() is the most
	// expensive (it samples Swift sources) and the cheapest detectors should
	// short-circuit first — a JS project must not pay for a Swift scan.
	registerDevServer(&SwiftWasmDevServer{})
}

// ─── DevServerManager ──────────────────────────────────────────────────

// DevServerManager manages the active dev server session and event subscribers.
type DevServerManager struct {
	mu     sync.RWMutex
	active *devServerSession
	subs   []chan DevServerEvent
	subsMu sync.Mutex
	target DevServerTarget

	// OwnerUserID labels this manager's port reservations so the owner can answer
	// "what is on :8083?". Empty uses the current workload attribution.
	OwnerUserID string

	// VibeSessionID + the brokered-port facts, so /dev/status can tell the client
	// exactly which port it got and whether that differs from the framework's
	// default. Written under m.mu in Start.
	VibeSessionID   string
	preferredPort   int
	portSubstituted bool

	// history is a ring buffer of recent events. Subscribe() replays
	// it into a new channel before adding to subs so a late SSE
	// subscriber (e.g. the dashboard arriving after Metro has already
	// printed its banner) still sees what just happened. Capped at
	// devEventHistoryMax to bound memory.
	history []DevServerEvent

	// Agent's externally reachable URL (for Metro proxy URL).
	// Set by the HTTP server after relay connection is established.
	// Examples: "http://192.168.1.10:18080", "https://public.yaver.io/d/abc123"
	AgentURL string

	// bundleMetaJSON stores the last validated bundle's metadata JSON.
	// Set by handleBuildNativeBundle, read by handleServeNativeBundle.
	bundleMetaJSON string
	// nativeBundleState tracks the most recent build-native artifacts so
	// /dev/native-bundle and /dev/native-assets keep working even when no
	// Metro dev server is active. Persisted to ~/.yaver/native-bundles.json.
	nativeBundleState NativeBundleState

	// Heartbeat state. heartbeatLoop ticks every 5s and emits a
	// "heartbeat" DevServerEvent with the live process state so the
	// CONSOLE / Webview pane can render real liveness instead of an
	// empty stream when Metro is quiet between bundle requests.
	heartbeatStop chan struct{}
	beatCounter   int
	lastNonBeatAt time.Time

	// Per-topic progress trackers. Set when the dev server's spawn
	// path attaches them; cleared on Stop. Used by the snapshot
	// ticker to embed real progress in every snapshot — and by the
	// tracker itself to emit "phase" / "progress" events.
	devTracker       *progressTracker // topic="dev/start"
	webTracker       *progressTracker // topic="webview/build" (Expo Web sibling)
	hermesTracker    *progressTracker // topic="hermes/compile" (build-native)
	transportTracker *webTransport    // topic="webview/transport" (per-bundle delivery lifecycle)
	recentLogTail    []string         // last 8 stdout/stderr lines for snapshots
	recentLogMu      sync.Mutex
	// webBundleInfo records the most recent web target build so the
	// /dev/web-bundle/* handler knows which directory to serve from.
	// Set by build_web.go on completion; cleared on Stop.
	webBundleInfo   WebBundleInfo
	webBundleInfoMu sync.RWMutex
}

// WebBundleInfo describes the currently-served web bundle (target =
// web-js-bundle or web-hermes-wasm). The /dev/web-bundle handler reads
// it to pick the right on-disk dir to serve.
type WebBundleInfo struct {
	Target    string `json:"target"`            // "web-js-bundle" | "web-hermes-wasm"
	BuildDir  string `json:"buildDir"`          // absolute path on host
	WorkDir   string `json:"workDir,omitempty"` // source repo root — used by serve-time freshness check + auto-rebuild
	IndexFile string `json:"indexFile"`         // typically "index.html"
	Size      int64  `json:"size"`              // total bundle bytes
	FileCount int    `json:"fileCount"`         // file count for js-bundle target
	BuiltAt   string `json:"builtAt"`           // RFC3339 build completion timestamp
	// HeadCommit is the git HEAD hash the export actually contains
	// (recorded AFTER the pre-build pull). The serve-time freshness
	// guard compares commit identity against this; the BuiltAt
	// wall-clock comparison alone false-staled fresh bundles when the
	// pre-build `git pull --rebase` re-stamped committer times
	// (double-build incident, 2026-07-26). Empty on bundles built by
	// older agents — those fall back to the timestamp comparison.
	HeadCommit string `json:"headCommit,omitempty"`
	Caller     string `json:"caller"` // X-Yaver-Caller of the build trigger
}

// NativeBundleInfo describes one compiled Hermes/native build artifact set.
// The build-specific URL returned by /dev/build-native carries BuildID so
// later fetches do not depend on whichever project is currently "active".
type NativeBundleInfo struct {
	BuildID      string `json:"buildId"`
	WorkDir      string `json:"workDir"`
	BuildDir     string `json:"buildDir"`
	BundlePath   string `json:"bundlePath"`
	AssetsDir    string `json:"assetsDir,omitempty"`
	Platform     string `json:"platform"`
	ModuleName   string `json:"moduleName"`
	BuiltAt      string `json:"builtAt"`
	MetadataJSON string `json:"metadataJson,omitempty"`
}

type NativeBundleState struct {
	LatestBuildID string             `json:"latestBuildId"`
	Bundles       []NativeBundleInfo `json:"bundles"`
}

// webBundleInfoFile is where SetWebBundleInfo persists its struct so a
// later agent process (after auto-update / reboot / systemd restart)
// can keep serving the on-disk bundle without losing 5 MB of asset
// requests to a 503 because the in-memory pointer was empty.
func webBundleInfoFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".yaver", "web-bundle-info.json")
}

func nativeBundleStateFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".yaver", "native-bundles.json")
}

// SetWebBundleInfo registers a freshly-built web bundle with the
// manager. Subsequent /dev/web-bundle/* requests are routed there.
// Persists the struct to ~/.yaver/web-bundle-info.json so an agent
// restart still serves the bundle that's already on disk.
func (m *DevServerManager) SetWebBundleInfo(info WebBundleInfo) {
	m.webBundleInfoMu.Lock()
	m.webBundleInfo = info
	m.webBundleInfoMu.Unlock()
	path := webBundleInfoFile()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if data, err := json.Marshal(info); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

// GetWebBundleInfo returns the current web bundle info. If the in-memory
// struct is empty (e.g. the agent restarted), tries to rehydrate from
// the persisted ~/.yaver/web-bundle-info.json — if that file points at
// a still-existing BuildDir, the manager re-adopts it so the iframe's
// asset requests don't 404 / 503.
func (m *DevServerManager) GetWebBundleInfo() WebBundleInfo {
	m.webBundleInfoMu.RLock()
	cur := m.webBundleInfo
	m.webBundleInfoMu.RUnlock()
	if cur.BuildDir != "" {
		return cur
	}
	path := webBundleInfoFile()
	if path == "" {
		return cur
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cur
	}
	var persisted WebBundleInfo
	if err := json.Unmarshal(data, &persisted); err != nil || persisted.BuildDir == "" {
		return cur
	}
	if st, err := os.Stat(persisted.BuildDir); err != nil || !st.IsDir() {
		return cur
	}
	m.webBundleInfoMu.Lock()
	if m.webBundleInfo.BuildDir == "" {
		m.webBundleInfo = persisted
	}
	out := m.webBundleInfo
	m.webBundleInfoMu.Unlock()
	return out
}

// SetWebTransport registers a freshly-created transport tracker for the
// current bundle. Last-write-wins: starting a new build replaces the
// tracker since the dashboard preview pane is single-track. Idempotent
// across multiple identical sets.
func (m *DevServerManager) SetWebTransport(t *webTransport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transportTracker = t
}

// GetWebTransport returns the current transport tracker (nil-safe; the
// tracker's own methods all no-op on a nil receiver).
func (m *DevServerManager) GetWebTransport() *webTransport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transportTracker
}

// devEventHistoryMax bounds DevServerManager.history. 200 lines covers
// Metro's startup banner + a comfortable margin of bundling/log output
// without keeping unbounded state for the long-running session.
const devEventHistoryMax = 200

type devServerSession struct {
	server DevServer
	proxy  *httputil.ReverseProxy
	ctx    context.Context
	cancel context.CancelFunc
	target DevServerTarget
	// releasePort returns this session's brokered port to the pool. Always
	// non-nil (a no-op when nothing was reserved) so callers never nil-check.
	// A leaked reservation shrinks the machine's usable range until restart,
	// which on a box meant to host many parallel previews is a real cost.
	releasePort func()
	// failed is true when ds.Start returned an error; we keep the
	// session around so Status() still reports the failure. A
	// subsequent Start() on the same framework clears it.
	failed bool
	// gap is the structured capability gap behind `failed`, when the failure
	// was one (missing toolchain). Held on the session, not globally, so a
	// new Start on another project cannot inherit a stale route.
	gap *CapabilityGap
}

type DevServerTarget struct {
	DeviceID    string
	DeviceName  string
	DeviceClass string
}

// NewDevServerManager creates a new manager.
func NewDevServerManager() *DevServerManager {
	return &DevServerManager{}
}

// Start launches a dev server for the given framework in the given directory.
// For fast frameworks (Vite, Next.js), blocks until ready.
// For slow frameworks (Flutter, Expo), launches async and returns immediately.
func (m *DevServerManager) Start(framework, workDir, platform string, port int, target DevServerTarget) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing session
	if m.active != nil {
		m.active.server.Stop()
		m.active.cancel()
		m.releasePortLocked()
		m.active = nil
	}

	// Drop replay history so a freshly-started dev server does not
	// hand its first subscriber the previous run's banner lines.
	// Live subs (rare — usually the SSE was closed when the previous
	// session stopped) keep their channels.
	m.subsMu.Lock()
	m.history = nil
	m.subsMu.Unlock()

	if isEmptyDevServerTarget(target) {
		target = m.target
	} else {
		m.target = target
	}

	var ds DevServer
	if framework != "" {
		if resolvedDS, resolvedDir, ok, err := resolveExplicitFrameworkWorkDir(framework, workDir); err != nil {
			return err
		} else if ok {
			ds = resolvedDS
			workDir = resolvedDir
			if canonical := devServerNameForDetectedFramework(framework); canonical != "" {
				framework = canonical
			}
		}
		if ds == nil {
			ds = getDevServerByName(framework)
		}
		if ds == nil {
			// Name lookup failed (name set at Start time) — fall back to auto-detection
			ds = detectDevServer(workDir)
		}
		if ds == nil {
			return fmt.Errorf("unknown framework: %s", framework)
		}
	} else {
		ds = detectDevServer(workDir)
		if ds == nil {
			// Monorepo fallback — when no marker is at the root, look for sub-projects.
			// Picks the first runnable dev server (Vite > Next > Expo > RN > Flutter)
			// and points workDir at that sub-project. If nothing runnable exists, the
			// returned error lists the apps so the user can pick one.
			fallbackDS, fallbackDir, err := monorepoFallbackDevServer(workDir)
			if err != nil {
				return err
			}
			ds = fallbackDS
			workDir = fallbackDir
		}
	}

	// Pre-set name/port/workDir so Status() returns meaningful data immediately
	// (before the async Start goroutine sets them again inside Start())
	frameworkName := framework
	if frameworkName == "" {
		// Derive name from the detected dev server type
		switch ds.(type) {
		case *ExpoDevServer:
			frameworkName = "expo"
		case *ReactNativeDevServer:
			frameworkName = "react-native"
		case *FlutterDevServer:
			frameworkName = "flutter"
		case *ViteDevServer:
			frameworkName = "vite"
		case *NextDevServer:
			frameworkName = "nextjs"
		}
	}
	defaultPort := port
	if defaultPort == 0 {
		switch frameworkName {
		case "expo", "react-native":
			defaultPort = 8081
		case "flutter":
			defaultPort = 9100
		case "vite":
			defaultPort = 5173
		case "nextjs":
			defaultPort = 3000
		}
	}
	// Reserve a port nobody else holds — Yaver session or otherwise. Done HERE,
	// once, for every framework: each lane used to hard-code its canonical port
	// (Metro 8081, Expo Web 19006, Flutter 9100, Vite 5173, Next 3000) and then
	// treat "something answers on it" as readiness, which on a busy machine
	// means a foreign listener (an orphan from another project, or on one real
	// box a four-day-old freeswitch on :8081) gets reported as a healthy dev
	// server. See devserver_ports.go for the full account.
	// One owner tag for the claim AND the roll-up. They used to differ (claim →
	// path tag, report → session tag), so a session's own port did not appear in
	// its resources — and PruneEmpty then deleted the session as "empty" while its
	// dev server was running. Verified live on the mini before this fix: the
	// roster was blank while /dev/status happily reported a vibeSessionId.
	claimOwner := m.claimOwnerTagFor(workDir)
	brokeredPort, portSubstituted, releasePort := AcquireDevPort(
		frameworkName, claimOwner, defaultPort)
	m.preferredPort = defaultPort
	m.portSubstituted = portSubstituted
	if portSubstituted {
		log.Printf("[dev] %s: :%d was unavailable, binding :%d instead (%s)",
			frameworkName, defaultPort, brokeredPort, workDir)
	}
	ds.PreStart(frameworkName, brokeredPort, workDir)
	// Tell every live client which port this session got, immediately. A client
	// that has to infer the port from the framework default is a client that is
	// wrong exactly when it matters (a substituted port), and a poll-only answer
	// arrives seconds late.
	m.emit(DevServerEvent{
		Type:            "resources",
		Framework:       frameworkName,
		Port:            brokeredPort,
		PreferredPort:   defaultPort,
		PortSubstituted: portSubstituted,
		Resources:       resourcesForOwner(claimOwner),
		Message: func() string {
			if portSubstituted {
				return fmt.Sprintf("Serving on :%d — :%d was already in use on this machine", brokeredPort, defaultPort)
			}
			return fmt.Sprintf("Serving on :%d", brokeredPort)
		}(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Snapshot the native fingerprint so /dev/reload can tell later whether a
	// JS-only hot reload is actually enough. Cheap (stat + hash of <30 files).
	if workDir != "" {
		SetNativeBaseline(workDir, ComputeNativeFingerprint(workDir))
	}

	// Inject SSE log emitter into the dev server so build output streams to mobile.
	if setter, ok := ds.(interface{ SetEmitFn(func(DevServerEvent)) }); ok {
		setter.SetEmitFn(m.emit)
	}

	// Wire the structured-progress trackers + recent-log recorder so
	// every stdout line gets parsed for "Bundling 67% (1247/2390)"
	// shapes and surfaced as real "progress" events. The Expo Web
	// sibling tracker is created lazily by StartWebPreview when the
	// dashboard's Web App tab fires its auto-spawn.
	surface := "hot-reload"
	if platform == "web" {
		surface = "web-reload"
	}
	devTracker := newProgressTracker(m.emit, frameworkName, "dev/start", surface)
	m.devTracker = devTracker
	m.webTracker = nil // reset; StartWebPreview will create when needed
	m.recentLogTail = nil
	if setter, ok := ds.(interface {
		SetTrackers(main, web *progressTracker)
	}); ok {
		setter.SetTrackers(devTracker, nil)
	}
	if setter, ok := ds.(interface{ SetRecordLogFn(func(string)) }); ok {
		setter.SetRecordLogFn(m.recordRecentLog)
	}
	// Initial phase event: queued. The next stdout line that matches
	// "Starting Metro Bundler" or similar pushes us to metro_bundling,
	// then "Waiting on http://..." pushes us to listening, etc.
	devTracker.transitionPhase("queued")

	log.Printf("[dev] Starting %s dev server in %s", frameworkName, workDir)

	ctx, cancel := context.WithCancel(context.Background())
	opts := DevServerOpts{
		WorkDir: workDir,
		// The brokered port, NOT the caller's raw `port` (which may be 0 and
		// would send each framework back to its hard-coded canonical default,
		// defeating the reservation above).
		Port:     brokeredPort,
		Platform: platform,
		Target:   target,
	}

	// Pass the agent's reachable URL so Metro can tell dev clients to connect
	// through the relay instead of hardcoding the local IP.
	if m.AgentURL != "" {
		opts.Env = map[string]string{
			"YAVER_AGENT_URL": m.AgentURL,
		}
	}

	// Set up the session immediately so Status() returns "starting"
	m.active = &devServerSession{
		server:      ds,
		ctx:         ctx,
		cancel:      cancel,
		target:      target,
		releasePort: releasePort,
	}

	// Emit starting event
	m.emit(DevServerEvent{
		Type:      "starting",
		Framework: frameworkName,
		Message:   fmt.Sprintf("Starting %s dev server...", ds.Name()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Heartbeat loop — emits one event every 5s so the CONSOLE never
	// looks dead. Stopped by Stop() / next Start().
	m.startHeartbeatLocked()

	// Launch start in background — don't block the HTTP response
	go func() {
		if err := ds.Start(ctx, opts); err != nil {
			// Distinguish a deliberate cancellation (project switch,
			// /dev/stop, Hermes-bytecode mode tearing down Metro after
			// the bundle was written) from a real start failure. The
			// parent ctx is context.Background() with explicit cancel,
			// so ctx.Err() == context.Canceled iff something on our
			// side called cancel(). In that case the mobile UI was
			// previously rendering "Start failed: exec npx: context
			// canceled" as a red banner even though the underlying
			// build/load succeeded — a false-positive after Hermes
			// reload. Treat as a clean stop instead.
			if ctx.Err() == context.Canceled {
				log.Printf("[dev] %s start canceled (deliberate stop / project switch): %v", ds.Name(), err)
				m.mu.Lock()
				if m.active != nil && m.active.server == ds {
					m.active.failed = false
				}
				m.mu.Unlock()
				m.emit(DevServerEvent{
					Type:      "stopped",
					Framework: ds.Name(),
					Message:   fmt.Sprintf("%s dev server stopped before becoming ready.", ds.Name()),
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}
			log.Printf("[dev] %s failed to start: %v", ds.Name(), err)
			// Keep the session around so /dev/status still reports
			// something the mobile client can render as a failure
			// (red banner + View Logs + Retry) instead of silently
			// disappearing into "no dev server running".
			// Name the fix, don't just forward the dump. `No file or variants
			// found for asset: .env.` is a complete diagnosis to whoever wrote the
			// pubspec and total noise to whoever is holding the phone.
			annotated := annotateDevStartError(ds.Name(), opts.WorkDir, err)
			if setter, ok := ds.(interface{ SetError(string) }); ok {
				setter.SetError(annotated)
			}
			// Tell the custodian, not just the client that happened to be
			// watching. A failure only a returned error knows about is invisible
			// to every OTHER surface, and invisible to the automatic-repair lane
			// — which is how the same npm/Metro/simulator failure gets rediscovered
			// by hand every time. The playbook answers instantly when it
			// recognises the text; anything it does not becomes evidence for a
			// runner. See custodian_playbook.go.
			ReportFailureToCustodian("dev-start", filepath.Base(opts.WorkDir), annotated)
			// The one detector. `annotated` already contains the raw spawn
			// error, so a missing toolchain is recognised here even though
			// nothing synchronous ever saw it. nil for every other failure
			// shape (compile error, port clash, missing pubspec asset) — a
			// bogus gap would send the user to install what they already have.
			gap := DetectCapabilityGap(CapabilityGapContext{
				Framework: ds.Name(),
				WorkDir:   opts.WorkDir,
				Err:       annotated,
			})
			m.mu.Lock()
			if m.active != nil && m.active.server == ds {
				m.active.cancel()
				m.active.failed = true
				m.active.gap = gap
				// The process is dead; holding its port would shrink the pool
				// for every other project on the machine.
				m.releasePortLocked()
			}
			m.mu.Unlock()
			m.emit(DevServerEvent{
				Type:      "error",
				Framework: ds.Name(),
				Message:   fmt.Sprintf("Failed to start %s: %s", ds.Name(), annotated),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Gap:       gap,
			})
			return
		}

		// Create reverse proxy to the dev server
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", ds.Port()))
		proxy := newDevServerReverseProxy(target)
		// Rewrite the served HTML's <base href> so root-absolute asset paths
		// resolve THROUGH this /dev/ proxy.
		//
		// The bug this fixes (2026-07-24, e-mobile): the browser lane serves the
		// dev server under /dev/, but Flutter's index.html ships `<base href="/">`.
		// A relative `<script src="flutter.js">` therefore resolves against the
		// base, i.e. to /flutter.js at the AGENT ROOT — not /dev/flutter.js — so
		// flutter.js (and splash images, main.dart.js, canvaskit) all 404, the
		// engine never boots, `flutter-view` never appears, and the mobile
		// overlay waits forever on a page that can never render. Measured through
		// the proxy: 404 /flutter.js, 404 /splash/img/light-1x.png. Direct on
		// :9100 it works, which is why a direct-URL test missed it.
		//
		// Rewriting the base to /dev/ makes every relative asset resolve to
		// /dev/<asset>, which the proxy forwards to the dev server correctly.
		// Applies to any web framework whose index roots its assets (Flutter is
		// the one that bites; Expo/Vite/Next use their own base handling and are
		// unaffected because their assets are already served relative or already
		// carry the right base).
		proxy.ModifyResponse = rewriteDevIndexBaseHref
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			// The framework's dev server (127.0.0.1:port) refused the request.
			// For a web-lane framework this is almost always "still compiling":
			// flutter run -d web-server and expo web take 10-60s to bind their
			// port on a cold start. Log it clearly for troubleshooting, and
			// return a STRUCTURED 503 "starting" (not a bare 502 "unavailable")
			// with Retry-After so the mobile WebView shows a loader and retries
			// instead of painting a dead error page. See DevPreview.tsx.
			log.Printf("[dev:proxy] %s target http://127.0.0.1:%d unreachable for %q: %v — signalling 'starting' (first compile can take ~1 min)",
				ds.Name(), ds.Port(), r.URL.Path, err)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "2")
			w.Header().Set("X-Yaver-DevServer", "starting")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"starting","framework":%q,"port":%d,"message":%q}`,
				ds.Name(), ds.Port(),
				ds.Name()+" dev server is still starting — the first web compile can take up to a minute")
		}

		m.mu.Lock()
		if m.active != nil && m.active.server == ds {
			m.active.proxy = proxy
		}
		m.mu.Unlock()

		log.Printf("[dev] %s ready on port %d", ds.Name(), ds.Port())

		// BROWSER LANE for Expo: Metro alone serves no web page, so a preview
		// pointed at /dev/ renders nothing. Start the `expo start --web` sibling
		// automatically — the user asked for a browser preview, which is a request
		// for a web server, not for Metro.
		//
		// This was previously only reachable via an explicit
		// POST /dev/web-preview/start that no client called, so the Expo browser
		// lane could never render. Idempotent (StartWebPreview returns the existing
		// port), and failure is reported rather than swallowed: a browser lane with
		// no web server must not look healthy.
		if platform == "web" {
			if _, isExpo := ds.(*ExpoDevServer); isExpo {
				if webPort, werr := m.StartWebPreview(); werr != nil {
					log.Printf("[dev:expo] browser lane requested but the web sibling failed to start: %v", werr)
					m.emit(DevServerEvent{
						Type:      "error",
						Framework: ds.Name(),
						Message: "Metro is running but the web preview server could not start, so there is " +
							"nothing to render in a browser: " + werr.Error(),
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
				} else {
					log.Printf("[dev:expo] browser lane: expo --web sibling on :%d (served at /dev-web/)", webPort)
				}
			}
		}

		m.emit(DevServerEvent{
			Type:      "ready",
			Framework: ds.Name(),
			BundleURL: ds.BundleURL(platform),
			Message:   fmt.Sprintf("%s dev server ready", ds.Name()),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}()

	return nil
}

// Stop stops the active dev server.
func (m *DevServerManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return fmt.Errorf("no dev server running")
	}

	name := m.active.server.Name()
	if st := m.active.server.Status(); st.WorkDir != "" {
		ClearNativeBaseline(st.WorkDir)
	}
	m.stopHeartbeatLocked()
	m.active.server.Stop()
	m.active.cancel()
	m.releasePortLocked()
	m.active = nil
	m.devTracker = nil
	m.webTracker = nil
	m.hermesTracker = nil
	m.recentLogMu.Lock()
	m.recentLogTail = nil
	m.recentLogMu.Unlock()

	// Drop replay history so a freshly-connecting consumer (mobile,
	// web dashboard) doesn't see stale phase/progress/snapshot
	// events from the session that just ended. The single "stopped"
	// event below is enough for them to know the channel is idle.
	m.history = nil

	m.emit(DevServerEvent{
		Type:      "stopped",
		Framework: name,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	log.Printf("[dev] %s stopped", name)
	return nil
}

// Reload triggers a hot reload on the active dev server.
func (m *DevServerManager) Reload() error {
	return m.ReloadMode("fast")
}

// modeReloadableDevServer is implemented by dev servers that distinguish
// a fast reload from a full one (today: Flutter, where fast = "r" hot
// reload and full = "R" hot restart). Servers without the interface get
// their plain Reload() for either mode.
type modeReloadableDevServer interface {
	ReloadWithMode(mode string) error
}

// ReloadMode triggers a reload on the active dev server with an explicit
// mode: "fast" (default — the framework's cheapest refresh) or "full"
// (framework-level restart of the app state where supported; NEVER a
// cache clear or process cold-start).
func (m *DevServerManager) ReloadMode(mode string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == nil {
		return fmt.Errorf("no dev server running")
	}

	if !m.active.server.SupportsHotReload() {
		return fmt.Errorf("%s does not support hot reload", m.active.server.Name())
	}

	if mr, ok := m.active.server.(modeReloadableDevServer); ok {
		if err := mr.ReloadWithMode(mode); err != nil {
			return err
		}
	} else if err := m.active.server.Reload(); err != nil {
		return err
	}

	msg := "Hot reload triggered"
	if mode == "full" {
		msg = "Full reload triggered"
	}
	m.emit(DevServerEvent{
		Type:      "reload",
		Framework: m.active.server.Name(),
		BundleURL: m.active.server.BundleURL(""),
		Message:   msg,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

// Status returns the current dev server status.
func (m *DevServerManager) Status() *DevServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == nil {
		return nil
	}

	s := m.active.server.Status()
	s.Kind = m.active.server.Kind()
	// Fill the URL the client should load from the ONE method that every
	// framework already implements correctly, instead of trusting each
	// Status() to remember.
	//
	// It didn't: only ExpoDevServer.Status() set BundleURL, so /dev/status for
	// Flutter / Vite / Next / React Native / SwiftWasm answered
	// `"serving":true,"bundleUrl":""` — the inventory saying yes while the
	// operation says nothing. The phone gates its WebView on bundleUrl, so it
	// sat on "Waiting for the dev server to report its address…" forever with a
	// perfectly healthy dev server on the other end (seen 2026-07-25 against a
	// Mac mini running `flutter run -d web-server` on :9100).
	//
	// Deriving it here means a NEW framework cannot reintroduce the bug by
	// omission — the only way to get it wrong is to return the wrong path from
	// BundleURL, which is the method whose whole job that is.
	if s.BundleURL == "" {
		s.BundleURL = m.active.server.BundleURL("")
	}
	// A session that exists but hasn't bound yet is LAUNCHING — say so.
	//
	// It used to answer `running:false, building:absent`, which every client
	// reads as "there is no dev server here" (mobile's isActiveDevServerStatus
	// requires running||building). For the 30s–3min a cold Flutter/Vite web
	// compile takes, the phone therefore: showed "Waiting for the dev server to
	// report its address…" with no elapsed time, AND never opened the /dev/events
	// log stream (it is gated on the same predicate) — so the one screen the user
	// stares at during the longest wait was the one screen with no information,
	// while the agent was streaming the whole log tail every 5s. Reported
	// 2026-07-25 on build 469 against a Mac mini.
	if !s.Running && !m.active.failed && s.Error == "" {
		s.Building = true
		if s.ServingLabel == "" {
			s.ServingLabel = fmt.Sprintf("Starting %s preview…", m.active.server.Name())
		}
	}
	// The route, on the polled channel too — see the field's comment.
	s.CapabilityGap = m.active.gap
	m.recentLogMu.Lock()
	recentLogs := append([]string(nil), m.recentLogTail...)
	m.recentLogMu.Unlock()
	s.RecentLogs = recentLogs
	s.PreviewHealth = previewHealthFromAgentSignals(s, recentLogs)
	// One shape for "what does this session hold", shared with /vibe/sessions.
	s.Resources = resourcesForOwner(m.resourceOwnerTag())
	s.VibeSessionID = m.VibeSessionID
	if m.portSubstituted && m.preferredPort > 0 && m.preferredPort != s.Port {
		s.PortSubstituted = true
		s.PreferredPort = m.preferredPort
	}
	s.TargetDeviceID = m.active.target.DeviceID
	s.TargetDeviceName = m.active.target.DeviceName
	s.TargetDeviceClass = m.active.target.DeviceClass
	return &s
}

// RecomputePreviewHealth re-derives previewHealth after a handler mutated the
// status it was computed from (the /dev/status dead-web-sibling probe sets
// Error AFTER Status() ran). A health verdict sitting next to an Error it
// never saw is a contradiction on the wire.
func (m *DevServerManager) RecomputePreviewHealth(status *DevServerStatus) {
	if status == nil {
		return
	}
	m.recentLogMu.Lock()
	recentLogs := append([]string(nil), m.recentLogTail...)
	m.recentLogMu.Unlock()
	status.PreviewHealth = previewHealthFromAgentSignals(*status, recentLogs)
}

func previewHealthFromAgentSignals(status DevServerStatus, recentLogs []string) (health *PreviewHealth) {
	// Capability negotiation belongs on the status payload the preview already
	// polls. Inferring this from a version recreated the exact v1.99.418 failure:
	// the client required an in-frame signal that the installed binary did not
	// contain, while the dashboard (which does not gate on it) rendered fine.
	defer func() {
		if health != nil {
			health.PaintSignal = "in_frame_v1"
		}
	}()
	if status.CapabilityGap != nil {
		return &PreviewHealth{
			State:               "infrastructure_gap",
			CanOfferProjectFix:  false,
			Severity:            "warn",
			Reason:              "The agent detected a deterministic machine/setup gap with its own repair route.",
			SignalSource:        "capability_gap",
			HasDeterministicFix: true,
		}
	}
	if strings.TrimSpace(status.Error) != "" {
		if statusErrorNeedsProjectFix(status.Error) {
			lines := compileErrorLines([]string{status.Error})
			return &PreviewHealth{
				State:              "needs_project_fix",
				CanOfferProjectFix: true,
				Severity:           "error",
				Reason:             status.Error,
				SignalSource:       "dev_status_error",
				RelevantLogLines:   lines,
			}
		}
		return &PreviewHealth{
			State:              "unknown",
			CanOfferProjectFix: false,
			Severity:           "warn",
			Reason:             status.Error,
			SignalSource:       "dev_status_error",
		}
	}
	if lines := previewProjectFailureLines(recentLogs); len(lines) > 0 {
		return &PreviewHealth{
			State:              "needs_project_fix",
			CanOfferProjectFix: true,
			Severity:           "error",
			Reason:             strings.Join(lines, "\n"),
			SignalSource:       "recent_logs",
			RelevantLogLines:   lines,
		}
	}
	if status.Building || previewLogsShowProgress(recentLogs) {
		return &PreviewHealth{
			State:              "starting",
			CanOfferProjectFix: false,
			Severity:           "info",
			Reason:             "The dev server is starting or compiling and has not reported a project failure.",
			SignalSource:       "agent_progress",
		}
	}
	if status.Running || status.Serving || previewLogsShowHealthy(recentLogs) {
		return &PreviewHealth{
			State:              "healthy",
			CanOfferProjectFix: false,
			Severity:           "info",
			Reason:             "The dev server is running and no project failure is active.",
			SignalSource:       "agent_status",
		}
	}
	return &PreviewHealth{
		State:              "unknown",
		CanOfferProjectFix: false,
		Severity:           "info",
		Reason:             "No project failure signal is active.",
		SignalSource:       "agent_status",
	}
}

func statusErrorNeedsProjectFix(msg string) bool {
	if strings.TrimSpace(msg) == "" {
		return false
	}
	for _, line := range strings.Split(msg, "\n") {
		if devBuildFailureLine(line) {
			return true
		}
	}
	l := strings.ToLower(msg)
	return strings.Contains(l, "no file or variants found for asset") ||
		strings.Contains(l, "cannot find module") ||
		strings.Contains(l, "unable to resolve module") ||
		strings.Contains(l, "syntaxerror") ||
		strings.Contains(l, "error ts")
}

func previewProjectFailureLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	// Order matters: a failure line followed by a LATER recovery line
	// ("Recompile complete", "Compiled successfully") means the app builds
	// again — the stale failure text must not keep needs_project_fix alive
	// after the fix (possibly made by the Fix-in-Yaver runner itself) landed.
	lastFailure, lastRecovery := -1, -1
	for i, line := range lines {
		if devBuildFailureLine(line) {
			lastFailure = i
		}
		if devBuildRecoveryLine(line) {
			lastRecovery = i
		}
	}
	if lastFailure < 0 || lastRecovery > lastFailure {
		return nil
	}
	return compileErrorLines(lines)
}

func previewLogsShowProgress(lines []string) bool {
	for _, line := range lines {
		l := strings.ToLower(strings.TrimSpace(line))
		if l == "" {
			continue
		}
		if l == "queued" || l == "starting" || l == "building" || strings.HasPrefix(l, "$ flutter ") ||
			strings.HasPrefix(l, "$ npm ") || strings.HasPrefix(l, "$ npx ") ||
			strings.HasPrefix(l, "$ yarn ") || strings.HasPrefix(l, "$ pnpm ") ||
			strings.Contains(l, "compiling ") || strings.Contains(l, "waiting for connection") ||
			strings.Contains(l, "logs for your project will appear") {
			return true
		}
	}
	return false
}

func previewLogsShowHealthy(lines []string) bool {
	for _, line := range lines {
		l := strings.ToLower(strings.TrimSpace(line))
		if l == "" {
			continue
		}
		if strings.Contains(l, "ready") || strings.Contains(l, "bundled") ||
			strings.Contains(l, "compiled") || strings.Contains(l, "listening") ||
			strings.Contains(l, "serving on") || strings.Contains(l, "running") {
			return true
		}
	}
	return false
}

// resourceOwnerTag is how this manager labels everything it claims. When a vibe
// session is attached the tag is the session's, so ports and devices roll up to
// the same roster entry the user sees; otherwise it falls back to the project
// path, which is still enough to attribute a stray port.
func (m *DevServerManager) resourceOwnerTag() string {
	if m.active != nil {
		return m.claimOwnerTagFor(m.active.server.Status().WorkDir)
	}
	return m.claimOwnerTagFor("")
}

// claimOwnerTagFor is THE owner tag for anything this manager claims: the vibe
// session when there is one, else a path-derived label. Single function so a
// claim and its later lookup can never be computed differently.
func (m *DevServerManager) claimOwnerTagFor(workDir string) string {
	if m.VibeSessionID != "" {
		return vibeOwnerTag(m.VibeSessionID)
	}
	return devPortOwner(m.OwnerUserID, workDir)
}

// releasePortLocked returns the active session's brokered port to the pool.
// Caller must hold m.mu. Safe to call repeatedly and with no active session.
func (m *DevServerManager) releasePortLocked() {
	if m.active == nil || m.active.releasePort == nil {
		return
	}
	m.active.releasePort()
	m.active.releasePort = nil
}

// PreferredTarget returns the persisted dev preview target.
func (m *DevServerManager) PreferredTarget() DevServerTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active != nil && !isEmptyDevServerTarget(m.active.target) {
		return m.active.target
	}
	return m.target
}

// SetPreferredTarget updates the persisted dev preview target.
func (m *DevServerManager) SetPreferredTarget(target DevServerTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.target = target
	if m.active != nil {
		m.active.target = target
	}
}

func isEmptyDevServerTarget(target DevServerTarget) bool {
	return target.DeviceID == "" && target.DeviceName == "" && target.DeviceClass == ""
}

// IsRunning returns true if a dev server is active.
func (m *DevServerManager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active != nil
}

// Proxy returns the reverse proxy for the active dev server, or nil.
func (m *DevServerManager) Proxy() *httputil.ReverseProxy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	return m.active.proxy
}

// DevServerPort returns the local port of the active dev server.
func (m *DevServerManager) DevServerPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return 0
	}
	return m.active.server.Port()
}

// WebPreviewPort returns the local port of the sibling Expo Web
// process if one is running, or 0. Only meaningful when the active
// dev server is an Expo framework; other dev servers serve web
// directly (Vite / Next) and don't use the sibling pattern.
func (m *DevServerManager) WebPreviewPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return 0
	}
	if expo, ok := m.active.server.(*ExpoDevServer); ok {
		return expo.WebPort()
	}
	return 0
}

// BrowserRoutePort returns the process that owns browser DOCUMENT routes after
// the injected router bootstrap exposes the guest app at its logical root.
//
// A preview first arrives through /dev-web/ (Expo sibling) or /dev/ (the main
// web server). Client routers then use logical URLs such as / and /settings.
// Those requests must keep reaching the same browser process: routing them to
// the agent mux is the exact first-paint-then-404 failure observed in Dogfood
// on 2026-09-05. Expo dev-client mode is deliberately fail-closed when its web
// sibling is gone; falling back to Metro would return a different surface.
func (m *DevServerManager) BrowserRoutePort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.active.server == nil {
		return 0
	}
	if expo, ok := m.active.server.(*ExpoDevServer); ok {
		return expo.WebPort()
	}
	status := m.active.server.Status()
	if !status.Running || !status.Serving {
		return 0
	}
	return m.active.server.Port()
}

// StartWebPreview starts a sibling Expo Web process alongside Metro.
// Returns the web port on success, 0 + error otherwise. Only valid
// when the active dev server is an ExpoDevServer — Vite / Next / etc.
// already serve browser preview through their primary port.
func (m *DevServerManager) StartWebPreview() (int, error) {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil {
		return 0, fmt.Errorf("no dev server running")
	}
	expo, ok := active.server.(*ExpoDevServer)
	if !ok {
		return 0, fmt.Errorf("web preview sibling is only supported for Expo — active framework is %s", active.server.Name())
	}

	// Spin up the structured-progress tracker for the web sibling
	// BEFORE the spawn so the very first stdout line gets parsed.
	webTracker := newProgressTracker(m.emit, "expo-web", "webview/build", "web-reload")
	webTracker.transitionPhase("queued")
	m.mu.Lock()
	m.webTracker = webTracker
	m.mu.Unlock()
	if setter, ok := active.server.(interface {
		SetTrackers(main, web *progressTracker)
	}); ok {
		setter.SetTrackers(m.devTracker, webTracker)
	}

	port, err := expo.StartWebPreview(active.ctx, expo.Status().WorkDir)
	if err != nil {
		webTracker.transitionPhase("error")
		return 0, err
	}
	// Phase events for the consumer's clarity: queued → preparing
	// (npm verify) → web_bundling (Metro web pass) → listening (port
	// open) → ready (HTML servable). Most transitions happen inside
	// the tracker's regex when stdout fires; we explicitly bump
	// "preparing" here as the agent's own marker for "we kicked it".
	webTracker.transitionPhase("preparing")

	m.emit(DevServerEvent{
		Type:      "web-preview-starting",
		Framework: "expo-web",
		Message:   fmt.Sprintf("Expo Web preview starting on :%d", port),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return port, nil
}

// StopWebPreview terminates the Expo Web sibling if running. Metro
// is left alone.
func (m *DevServerManager) StopWebPreview() error {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil {
		return nil
	}
	if expo, ok := active.server.(*ExpoDevServer); ok {
		return expo.StopWebPreview()
	}
	return nil
}

// Subscribe returns a channel that receives dev server events. Any
// recent events buffered in m.history (capped at devEventHistoryMax)
// are replayed into the new channel before it is added to the
// subscriber set, so a late subscriber (the dashboard finishing its
// SSE handshake after Metro has already printed its banner) still
// sees what it missed. Held under subsMu so a concurrent emit() can
// not interleave a partially-replayed view with a new live event.
func (m *DevServerManager) Subscribe() chan DevServerEvent {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	ch := make(chan DevServerEvent, 16+len(m.history))
	for _, ev := range m.history {
		ch <- ev
	}
	m.subs = append(m.subs, ch)
	return ch
}

// SubscribeFresh is Subscribe without the history replay. Use for
// short-lived consumers (mobile feedback overlay reload chip) that
// only care about events emitted from this moment forward. The
// default Subscribe replays up to 200 buffered events so dashboards
// don't lose context across reconnects, which is the wrong shape
// for a "watch this one reload finish" UX — the user saw the prior
// reload's hbc_cache_lookup → ready cycle replay PLUS the new live
// one and got "Hot reload triggered" twice in the transcript.
func (m *DevServerManager) SubscribeFresh() chan DevServerEvent {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	ch := make(chan DevServerEvent, 16)
	m.subs = append(m.subs, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (m *DevServerManager) Unsubscribe(ch chan DevServerEvent) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for i, s := range m.subs {
		if s == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// SetBundleMetadata stores validated bundle metadata JSON for the native-bundle endpoint.
func (m *DevServerManager) SetBundleMetadata(metaJSON string) {
	m.mu.Lock()
	m.bundleMetaJSON = metaJSON
	m.mu.Unlock()
}

// GetBundleMetadata returns the last stored bundle metadata JSON.
func (m *DevServerManager) GetBundleMetadata() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bundleMetaJSON
}

func trimNativeBundlesLocked(state NativeBundleState) NativeBundleState {
	const maxNativeBundles = 4
	if len(state.Bundles) <= maxNativeBundles {
		return state
	}
	keep := state.Bundles[len(state.Bundles)-maxNativeBundles:]
	state.Bundles = append([]NativeBundleInfo(nil), keep...)
	return state
}

func persistNativeBundleState(state NativeBundleState) {
	path := nativeBundleStateFile()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if data, err := json.Marshal(state); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

func isUsableNativeBundle(info NativeBundleInfo) bool {
	if info.BuildID == "" || info.BundlePath == "" {
		return false
	}
	if st, err := os.Stat(info.BundlePath); err != nil || st.IsDir() {
		return false
	}
	if info.AssetsDir != "" {
		if st, err := os.Stat(info.AssetsDir); err != nil || !st.IsDir() {
			info.AssetsDir = ""
		}
	}
	return true
}

func loadPersistedNativeBundleState() NativeBundleState {
	path := nativeBundleStateFile()
	if path == "" {
		return NativeBundleState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NativeBundleState{}
	}
	var persisted NativeBundleState
	if err := json.Unmarshal(data, &persisted); err != nil {
		return NativeBundleState{}
	}
	filtered := make([]NativeBundleInfo, 0, len(persisted.Bundles))
	for _, info := range persisted.Bundles {
		if isUsableNativeBundle(info) {
			filtered = append(filtered, info)
		}
	}
	persisted.Bundles = filtered
	foundLatest := false
	for _, info := range persisted.Bundles {
		if info.BuildID == persisted.LatestBuildID {
			foundLatest = true
			break
		}
	}
	if !foundLatest && len(persisted.Bundles) > 0 {
		persisted.LatestBuildID = persisted.Bundles[len(persisted.Bundles)-1].BuildID
	}
	if len(persisted.Bundles) == 0 {
		persisted.LatestBuildID = ""
	}
	return trimNativeBundlesLocked(persisted)
}

// SetNativeBundleInfo stores a completed native bundle build so the returned
// bundle/assets URLs remain valid even when no project is actively serving
// Metro. The state is persisted to ~/.yaver/native-bundles.json.
func (m *DevServerManager) SetNativeBundleInfo(info NativeBundleInfo) {
	if info.BuildID == "" || info.BundlePath == "" {
		return
	}
	m.mu.Lock()
	state := m.nativeBundleState
	replaced := false
	for i := range state.Bundles {
		if state.Bundles[i].BuildID == info.BuildID {
			state.Bundles[i] = info
			replaced = true
			break
		}
	}
	if !replaced {
		state.Bundles = append(state.Bundles, info)
	}
	state.LatestBuildID = info.BuildID
	state = trimNativeBundlesLocked(state)
	m.nativeBundleState = state
	m.mu.Unlock()
	persistNativeBundleState(state)
}

// GetNativeBundleInfo resolves a build-specific native bundle when buildID is
// provided, otherwise it returns the latest successfully built bundle.
func (m *DevServerManager) GetNativeBundleInfo(buildID string) NativeBundleInfo {
	m.mu.RLock()
	state := m.nativeBundleState
	m.mu.RUnlock()
	if len(state.Bundles) == 0 {
		state = loadPersistedNativeBundleState()
		if len(state.Bundles) > 0 {
			m.mu.Lock()
			if len(m.nativeBundleState.Bundles) == 0 {
				m.nativeBundleState = state
			}
			state = m.nativeBundleState
			m.mu.Unlock()
		}
	}
	resolveID := buildID
	if resolveID == "" {
		resolveID = state.LatestBuildID
	}
	for _, info := range state.Bundles {
		if info.BuildID == resolveID && isUsableNativeBundle(info) {
			return info
		}
	}
	return NativeBundleInfo{}
}

// EmitLog emits a "log" event with the given line to all SSE subscribers.
func (m *DevServerManager) EmitLog(line string) {
	m.emit(DevServerEvent{
		Type:      "log",
		LogLine:   line,
		Message:   line, // mirror into Message so SSE consumers that read .message see it too
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// EmitCapabilityGap puts a NAMED, ROUTED capability gap on /dev/events.
//
// THE PROBLEM THIS SOLVES. The gap object reaches every surface today only
// from the dev-SERVER lane: the /dev/start 412 body, this SSE stream's start
// failure, and /dev/status. Sibling lanes that refuse for exactly the same
// reason — /dev/build-native's "missing required tools on this machine",
// most of all — answer an HTTP body that no shipped client decorates, because
// each client's transport only lifts `capabilityGap` off the /dev/start 412
// (mobile/src/lib/quic.ts, web/lib/agent-client.ts). A correct gap in a body
// nobody parses is a signal with no consumer, which the audit's rule 11 counts
// as not shipped.
//
// /dev/events is the channel every preview surface is ALREADY subscribed to,
// and all four renderers already call capabilityGapFromDevEvent on each frame
// (apps.tsx, DevPreview.tsx, PreviewPane.tsx, RuntimeLabView.tsx). Emitting
// here therefore needs no client change to land on all of them.
//
// Transient by design: an SSE frame cannot go stale the way a /dev/status
// field can, so a gap raised by a failed build cannot keep accusing a machine
// that has since been fixed.
func (m *DevServerManager) EmitCapabilityGap(framework, message string, gap *CapabilityGap) {
	if gap == nil {
		return
	}
	if strings.TrimSpace(message) == "" {
		message = gap.Summary
	}
	m.emit(DevServerEvent{
		Type:      "error",
		Framework: framework,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Gap:       gap,
	})
}

// EmitReloadDone emits an explicit terminal event that
// /dev/reload-app SSE consumers (feedback-overlay reload chip)
// can use to clear their progress spinner without waiting on a
// safety timeout. The shape is fixed by contract — type must stay
// "reload_done" for the iOS summarizer to detect it.
//
// `bundleURL` is the signed relative path the agent broadcast on
// /blackbox/command-stream so the in-Yaver feedback overlay (native
// iOS) can swap the running guest bridge directly via
// YaverBundleLoader instead of waiting on the JS-side BlackBox
// listener — which is dead the moment the bridge swapped to a guest
// (the previous Yaver-side listener died with the host bridge, and
// the guest's yaver-feedback-react-native SDK is suppressed when
// IS_HOST_MODE=true). Without this, reload_bundle was a tree-falls-
// in-the-forest event: the agent broadcast it, no one listened, the
// underlying app stayed at the pre-vibe version even after the
// transcript said ✓ Reloaded.
func (m *DevServerManager) EmitReloadDone(projectPath, deviceID, bundleURL string) {
	m.emit(DevServerEvent{
		Type:      "reload_done",
		Topic:     "reload-app",
		Phase:     "done",
		Message:   "Reload complete",
		BundleURL: bundleURL,
		WorkDir:   projectPath,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *DevServerManager) EmitStopResult(result map[string]interface{}) {
	if m == nil {
		return
	}
	message, _ := result["message"].(string)
	workDir, _ := result["workDir"].(string)
	m.emit(DevServerEvent{
		Type:      "stopped",
		Topic:     "dev/stop",
		Phase:     "done",
		Message:   message,
		WorkDir:   workDir,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *DevServerManager) emit(event DevServerEvent) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	m.history = append(m.history, event)
	if len(m.history) > devEventHistoryMax {
		m.history = m.history[len(m.history)-devEventHistoryMax:]
	}
	if event.Type != "heartbeat" {
		m.lastNonBeatAt = time.Now()
	}
	for _, ch := range m.subs {
		select {
		case ch <- event:
		default:
			// Drop if subscriber is slow
		}
	}
}

// startHeartbeatLocked must be called with m.mu held. Spins up the
// goroutine that pulses an event onto /dev/events every 5s with the
// real process state of the active dev server. Idempotent — replaces
// any existing heartbeat loop on the manager.
func (m *DevServerManager) startHeartbeatLocked() {
	if m.heartbeatStop != nil {
		close(m.heartbeatStop)
	}
	stop := make(chan struct{})
	m.heartbeatStop = stop
	m.beatCounter = 0
	m.lastNonBeatAt = time.Now()
	go m.heartbeatLoop(stop)
}

func (m *DevServerManager) stopHeartbeatLocked() {
	if m.heartbeatStop != nil {
		close(m.heartbeatStop)
		m.heartbeatStop = nil
	}
}

// heartbeatLoop emits a "heartbeat" DevServerEvent every 5 seconds
// while a dev server is running. The event carries real, agent-side
// process state — pid alive (signal-0 probe), uptime, port, idle
// seconds since the last log/event — so the dashboard CONSOLE strip
// can prove liveness instead of going silent between Metro bundle
// requests. Five seconds is small enough to feel live without
// flooding the SSE channel; the web UI throttles its render budget
// to one beat-line per ~5s so the strip stays readable.
//
// Two events fire per tick: a legacy "heartbeat" and a new "snapshot".
// The snapshot is the consumer's source of truth — even if every
// progress/log delta were dropped, the next snapshot 5s later would
// fully restore the UI. The heartbeat is kept for backwards-compat
// with consumers that don't yet handle snapshots.
func (m *DevServerManager) heartbeatLoop(stop <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	// Fire one beat almost immediately so the CONSOLE never sits at
	// "events: 0" longer than it has to.
	first := time.NewTimer(750 * time.Millisecond)
	defer first.Stop()
	for {
		select {
		case <-stop:
			return
		case <-first.C:
			m.emitHeartbeat()
			m.emitSnapshot()
		case <-t.C:
			m.emitHeartbeat()
			m.emitSnapshot()
		}
	}
}

func (m *DevServerManager) emitHeartbeat() {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil {
		return
	}
	st := active.server.Status()
	pid := 0
	pidAlive := false
	if base, ok := active.server.(interface{ Pid() int }); ok {
		pid = base.Pid()
		if pid > 0 {
			if proc, err := os.FindProcess(pid); err == nil {
				// signal 0 — kernel-level "is the process alive" probe
				pidAlive = proc.Signal(syscall.Signal(0)) == nil
			}
		}
	}
	uptime := 0
	if base, ok := active.server.(interface{ StartedAt() time.Time }); ok {
		if started := base.StartedAt(); !started.IsZero() {
			uptime = int(time.Since(started).Seconds())
		}
	}
	m.subsMu.Lock()
	m.beatCounter++
	beatNum := m.beatCounter
	idle := 0
	if !m.lastNonBeatAt.IsZero() {
		idle = int(time.Since(m.lastNonBeatAt).Seconds())
	}
	m.subsMu.Unlock()

	m.emit(DevServerEvent{
		Type:       "heartbeat",
		Framework:  active.server.Name(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Pid:        pid,
		PidAlive:   pidAlive,
		UptimeSec:  uptime,
		Port:       st.Port,
		WorkDir:    st.WorkDir,
		IdleSec:    idle,
		BeatNumber: beatNum,
	})
}

// emitSnapshot is the single source of truth for the UI. Every 5s,
// regardless of activity, the agent emits a full picture of every
// running stream + the last known progress + the most recent log
// tail. A reconnecting consumer reads one snapshot and is fully
// caught up — no replay storm needed. A user staring at a slow
// compile gets a fresh snapshot every 5s with current_file and
// pct, so they always have something to look at.
func (m *DevServerManager) emitSnapshot() {
	m.mu.RLock()
	active := m.active
	devT := m.devTracker
	webT := m.webTracker
	hermesT := m.hermesTracker
	m.mu.RUnlock()

	// Build phases map
	phases := map[string]string{}
	var devProgress, webProgress *ProgressSnapshot
	if devT != nil {
		ps := devT.Snapshot()
		phases["dev/start"] = ps.Phase
		if ps.Phase != "" {
			devProgress = &ps
		}
	}
	if webT != nil {
		ps := webT.Snapshot()
		phases["webview/build"] = ps.Phase
		if ps.Phase != "" {
			webProgress = &ps
		}
	}
	if hermesT != nil {
		ps := hermesT.Snapshot()
		phases["hermes/compile"] = ps.Phase
	}

	snap := &DevServerSnapshot{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Phases:      phases,
		Progress:    devProgress,
		WebProgress: webProgress,
	}

	if active != nil {
		st := active.server.Status()
		snap.Running = st.Running
		snap.Framework = active.server.Name()
		snap.Port = st.Port
		snap.WebPort = st.WebPort
		snap.WorkDir = st.WorkDir
		if base, ok := active.server.(interface{ Pid() int }); ok {
			snap.Pid = base.Pid()
			if snap.Pid > 0 {
				if proc, err := os.FindProcess(snap.Pid); err == nil {
					snap.PidAlive = proc.Signal(syscall.Signal(0)) == nil
				}
			}
		}
		if base, ok := active.server.(interface{ StartedAt() time.Time }); ok {
			if started := base.StartedAt(); !started.IsZero() {
				snap.UptimeSec = int(time.Since(started).Seconds())
			}
		}
	}

	m.subsMu.Lock()
	snap.BeatNumber = m.beatCounter
	if !m.lastNonBeatAt.IsZero() {
		snap.IdleSec = int(time.Since(m.lastNonBeatAt).Seconds())
	}
	m.subsMu.Unlock()

	m.recentLogMu.Lock()
	if len(m.recentLogTail) > 0 {
		// Copy to avoid mutation aliasing through SSE serialization.
		snap.RecentLogs = append([]string{}, m.recentLogTail...)
	}
	m.recentLogMu.Unlock()
	if active != nil {
		st := active.server.Status()
		st.CapabilityGap = active.gap
		snap.PreviewHealth = previewHealthFromAgentSignals(st, snap.RecentLogs)
	}

	m.emit(DevServerEvent{
		Type:      "snapshot",
		Framework: snap.Framework,
		Timestamp: snap.GeneratedAt,
		Snapshot:  snap,
	})
}

// recordRecentLog appends to a small ring buffer of recent stdout/stderr
// lines. Snapshot embeds the last 8 so a fresh subscriber gets context
// without replaying the full history.
func (m *DevServerManager) recordRecentLog(line string) {
	if line == "" {
		return
	}
	m.recentLogMu.Lock()
	m.recentLogTail = append(m.recentLogTail, line)
	if len(m.recentLogTail) > devRecentLogMax {
		m.recentLogTail = m.recentLogTail[len(m.recentLogTail)-devRecentLogMax:]
	}
	tail := append([]string(nil), m.recentLogTail...)
	m.recentLogMu.Unlock()

	// A dev server can be perfectly HEALTHY and still be serving an app that
	// cannot compile. Flutter's web-server keeps listening and answers
	// index.html, so readiness passes, the proxy returns 200, and the phone shows
	// a black screen forever. The only statement of the truth is in the output:
	//
	//   ../font_awesome_flutter-10.12.0/lib/src/icon_data.dart:104:36: Error: The
	//   class 'IconData' can't be extended outside of its library …
	//   Failed to compile application.
	//
	// Observed on a real project 2026-07-25, after two Yaver-side bugs had been
	// fixed and the preview STILL showed nothing. Promote it to a first-class
	// failure with the offending lines attached, so the surface the user is
	// looking at says "your app failed to compile: <reason>" instead of nothing.
	if devBuildFailureLine(line) {
		detail := "The app failed to compile — the dev server is running but has nothing to serve:\n" + strings.Join(compileErrorLines(tail), "\n")
		// A DETAIL STRING IS NOT A ROUTE. This event used to carry the compiler's
		// words and nothing else: no code to switch on, no button to press, so
		// every surface rendered a wall of text and the user retyped the error
		// into the chat by hand. A compile error is the one failure class Yaver
		// has no command for, which is exactly when escalating to a coding agent
		// is the right answer rather than the expensive one.
		gap := compileFailureGap(m.frameworkNameForEvents(), detail, collectInstalledRunnerIDs())
		m.emit(DevServerEvent{
			Type:      "error",
			Framework: m.frameworkNameForEvents(),
			Message:   detail,
			Gap:       gap,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		// An EVENT alone is not enough. Events reach whoever is subscribed at
		// that instant; every client that connects afterwards polls /dev/status
		// and was told `running: true, error: none` — which is how a Flutter
		// preview sat blank while the agent reported perfect health (2026-07-25,
		// font_awesome_flutter vs a newer Flutter SDK: `The class IconData
		// can't be extended outside of its library`). Persist it so the truth
		// survives the moment it was discovered.
		m.mu.RLock()
		active := m.active
		m.mu.RUnlock()
		if active != nil {
			if setter, ok := active.server.(interface{ SetCompileError(string) }); ok {
				setter.SetCompileError(detail)
			}
		}
		// The semi-deterministic lane gets it too: a compile failure in a
		// dependency is exactly the shape the playbook + runner escalation exist
		// for, and it must not depend on a human noticing a log line.
		ReportFailureToCustodian("dev-compile", m.frameworkNameForEvents(), detail)
	} else if devBuildRecoveryLine(line) {
		// The inverse transition: the app builds again. Without this clear the
		// persisted error above outlives the failure it described, and
		// previewHealth keeps offering a project fix over a WORKING preview —
		// including immediately after the Fix-in-Yaver runner repaired the
		// project (the feature would defeat its own acceptance criterion).
		m.mu.RLock()
		active := m.active
		m.mu.RUnlock()
		if active != nil {
			if setter, ok := active.server.(interface{ SetCompileError(string) }); ok {
				setter.SetCompileError("")
			}
		}
	}
}

// devRecentLogMax bounds the replayed tail. Big enough to carry a Dart/TS
// compile error with its context lines (the useful part is 5–10 lines), small
// enough that a snapshot frame stays cheap on a relay.
const devRecentLogMax = 24

func (m *DevServerManager) frameworkNameForEvents() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active != nil {
		return m.active.server.Name()
	}
	return ""
}

// ─── Base Dev Server (shared logic) ────────────────────────────────────

// baseDevServer provides shared process management for dev servers.
type baseDevServer struct {
	name      string
	port      int
	cmd       *exec.Cmd
	running   bool
	startedAt time.Time
	workDir   string
	err       string
	mu        sync.Mutex
	emitFn    func(DevServerEvent) // set by DevServerManager to stream log lines via SSE
	// tracker (and optional webTracker for the Expo Web sibling)
	// receive every stdout/stderr line for structured-progress
	// extraction. Both are nullable; falling back to plain log emission
	// when nil keeps tests + non-Expo dev servers working unchanged.
	tracker     *progressTracker
	webTracker  *progressTracker
	recordLogFn func(string) // appended to manager's recent-log ring buffer for snapshots
}

// SetTrackers wires the two progress trackers (main dev server and
// optional Expo Web sibling) into the spawn pipeline so each output
// line is parsed for real progress.
func (b *baseDevServer) SetTrackers(main, web *progressTracker) {
	b.tracker = main
	b.webTracker = web
}

// SetRecordLogFn lets the manager capture stdout/stderr for the
// snapshot's recent-log tail.
func (b *baseDevServer) SetRecordLogFn(fn func(string)) { b.recordLogFn = fn }

func (b *baseDevServer) Name() string                      { return b.name }
func (b *baseDevServer) Port() int                         { return b.port }
func (b *baseDevServer) SetEmitFn(fn func(DevServerEvent)) { b.emitFn = fn }

// Pid + StartedAt are read by the heartbeat loop to fill in real
// process state on the heartbeat event. Both are guarded by b.mu so
// concurrent Stop() during the heartbeat tick can't race.
func (b *baseDevServer) Pid() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd != nil && b.cmd.Process != nil {
		return b.cmd.Process.Pid
	}
	return 0
}
func (b *baseDevServer) StartedAt() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.startedAt
}

// SetError records a human-readable failure reason on the dev server
// so Status() returns it even after the manager clears b.running.
func (b *baseDevServer) SetError(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = msg
	b.running = false
}

// SetCompileError records a BUILD failure while the process keeps serving.
//
// Distinct from SetError on purpose: `running` stays true because the statement
// being made is different. SetError means "this dev server is not up".
// SetCompileError means "it is up, listening and answering — and the app it is
// supposed to serve cannot be built". Collapsing the two would either hide a
// live server or claim a dead one, and the user's screen needs the third
// answer: running, reachable, and serving nothing, with the reason attached.
func (b *baseDevServer) SetCompileError(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = msg
}

// PreStart sets the name, port, and workDir before the async Start goroutine.
// This ensures Status() returns meaningful data immediately.
func (b *baseDevServer) PreStart(name string, port int, workDir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.name = name
	if port > 0 {
		b.port = port
	}
	b.workDir = workDir
	// A NEW start must not report the PREVIOUS session's failure. b.err was
	// only cleared deep in the readiness loop (after the port answered), so
	// every /dev/status poll between /dev/start and readiness served the old
	// session's error — observed live 2026-07-26 as a one-poll flash of
	// 'the browser preview exited' while a fresh sfmg start was building.
	b.err = ""
}

func (b *baseDevServer) Status() DevServerStatus {
	b.mu.Lock()
	defer b.mu.Unlock()

	// PROBE THE PROCESS, do not trust the flag.
	//
	// `b.running` is an in-memory boolean set at spawn and cleared by the
	// bookkeeping goroutine on a clean exit. When the process dies in a way
	// that goroutine does not survive — an OOM kill, a SIGKILL — the flag stays
	// true forever and /info reports a dev server that is not there.
	//
	// Measured on ubuntu-4gb, 2026-08-03. /info said:
	//     running=true serving=true port=8081 pid=11999
	// while `ss -lntp` showed NOTHING listening on 8081 and pid 11999 did not
	// exist. The kernel had been OOM-killing 5-6 GB `git` processes on that box.
	// The visionOS arc trusted it, aimed the capture browser at :8081 and got
	// `net::ERR_CONNECTION_REFUSED` — a confusing error instead of "the dev
	// server is not running".
	//
	// The liveness check already existed (`PidAlive`, signal-0, devserver.go's
	// heartbeat snapshot) and the field that everyone actually reads never
	// consulted it: a producer with no consumer, again.
	//
	// signal(0) asks the kernel "does this pid exist and may I signal it"
	// without delivering anything, so this is cheap enough for a status call.
	running := b.running
	if running && b.cmd != nil && b.cmd.Process != nil {
		if err := b.cmd.Process.Signal(syscall.Signal(0)); err != nil {
			running = false
		}
	}

	s := DevServerStatus{
		Framework: b.name,
		Running:   running,
		Serving:   running,
		Port:      b.port,
		HotReload: true,
		WorkDir:   b.workDir,
		Error:     b.err,
	}
	// Say WHY it is not serving, rather than letting a surface render an empty
	// panel next to a silent `serving:false`.
	if b.running && !running && s.Error == "" {
		s.Error = fmt.Sprintf("the %s dev server process (pid %d) is gone — it exited or was killed (check for an OOM kill). Start it again.",
			b.name, b.cmd.Process.Pid)
	}
	if running {
		s.StartedAt = b.startedAt.UTC().Format(time.RFC3339)
		s.ServingLabel = fmt.Sprintf("Serving %s preview", b.name)
		s.StopActionLabel = "Stop Serving"
	}
	if b.cmd != nil && b.cmd.Process != nil {
		s.PID = b.cmd.Process.Pid
	}
	return s
}

func (b *baseDevServer) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd != nil && b.cmd.Process != nil {
		// Dev servers are usually invoked as `sh -c "vite ..."` or
		// `npm run dev` which forks a node child (and esbuild
		// grandchildren). Sending SIGINT only to the shell PID exits
		// the shell but orphans node, leaving the dev port bound and
		// blocking the next /dev/start. Kill the whole process group
		// (created via setProcGroup in startProcess) so all descendants
		// die together.
		pid := b.cmd.Process.Pid
		if err := killProcessGroup(pid, "INT"); err != nil {
			// Group kill might fail if the leader already exited;
			// fall back to per-process signal so we still try.
			b.cmd.Process.Signal(os.Interrupt)
		}
		done := make(chan error, 1)
		go func() { done <- b.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// Escalate to SIGKILL on the whole group, then on the
			// leader, so we don't leak vite/esbuild after a hung
			// graceful-stop window.
			killProcessGroup(pid, "KILL")
			b.cmd.Process.Kill()
		}
	}
	b.running = false
	return nil
}

// emitDevCommand puts the exact operation at the top of the same raw log lane
// that carries npm/Metro/Flutter output. A child process can take tens of
// seconds to print its first byte; without this banner the UI cannot
// distinguish a real compile from a dead start.
func emitDevCommand(emit func(DevServerEvent), framework, name string, args []string, workDir string) {
	if emit == nil {
		return
	}
	command := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
	emit(DevServerEvent{
		Type:      "log",
		Framework: framework,
		LogLine:   fmt.Sprintf("$ %s   (in %s)", command, workDir),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// startProcess launches a command and waits for readiness by polling a URL.
func (b *baseDevServer) startProcess(ctx context.Context, name string, args []string, workDir string, env []string, readyURL string) error {
	b.mu.Lock()
	b.workDir = workDir
	b.mu.Unlock()

	// Resolve via the runtime dirs at SPAWN time — exec.Command alone uses
	// the agent's boot-time PATH and misses agent-installed toolchains.
	cmd, err := newRuntimeCommandContext(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("prepare %s command: %w", name, err)
	}
	cmd.Dir = workDir
	// augmentEnv prepends ~/.yaver/runtimes/node/bin to PATH so
	// `npx` / `node` invocations resolve to the agent-managed Node
	// runtime on a fresh Linux box that never had system Node.
	// applyMetroCacheEnv pins TMPDIR to the persistent per-project
	// cache dir so Metro/Expo transform caches survive restarts and
	// /tmp cleanups (see devserver_metro_cache.go).
	cmd.Env = append(applyMetroCacheEnv(augmentEnv(nil), workDir), env...)
	// Put the dev server in its own process group so Stop() can take down
	// all child processes (vite/next fork node + esbuild; killing only the
	// shell PID leaks them and the dev port stays bound until reboot).
	setProcGroup(cmd)

	// Pipe output to log with [dev] prefix, stream to SSE subscribers,
	// AND feed the structured-progress trackers so they can extract
	// pct + current_file from Metro/Expo/webpack output.
	logWriter := &devLogWriter{prefix: fmt.Sprintf("[dev:%s]", b.name)}
	emitFn := b.emitFn
	framework := b.name
	tracker := b.tracker
	webTracker := b.webTracker
	recordLogFn := b.recordLogFn
	logWriter.onLogLine = func(line string) {
		if recordLogFn != nil {
			recordLogFn(line)
		}
		if tracker != nil {
			tracker.FeedLine(line)
		}
		if webTracker != nil {
			webTracker.FeedLine(line)
		}
		if emitFn != nil {
			emitFn(DevServerEvent{
				Type:      "log",
				Framework: framework,
				LogLine:   line,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	emitDevCommand(b.emitFn, b.name, name, args, workDir)

	// Keep stdin open (Flutter needs it for "r" hot reload)
	stdinPipe, _ := cmd.StdinPipe()
	_ = stdinPipe // kept open for Reload()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	// Write it down BEFORE anything else can go wrong: setProcGroup means this
	// child survives us, so a record written late is a record that isn't there
	// when the next agent needs to reap it. See devserver_child_registry.go.
	RecordDevChild(devChildRecord{
		PID: cmd.Process.Pid, Port: b.port, Kind: b.name,
		Match: fmt.Sprintf("%s,%d", name, b.port), WorkDir: workDir,
	})

	b.mu.Lock()
	b.cmd = cmd
	b.startedAt = time.Now()
	b.mu.Unlock()

	// Signal subprocess exit. If the command dies before readyURL
	// responds, we want to abort the readiness loop immediately and
	// bubble up the tail of its output so the user sees a real error
	// instead of a 120 s "did not become ready" spinner.
	exitCh := make(chan error, 1)
	childPID := cmd.Process.Pid
	go func() {
		err := cmd.Wait()
		ForgetDevChild(childPID) // exited on its own — nothing to reap next start
		exitCh <- err
	}()

	// Wait for dev server to become ready (poll health/readiness)
	deadline := time.After(120 * time.Second) // Expo web first build can take 2+ min
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case waitErr := <-exitCh:
			tail := logWriter.Tail(12)
			if waitErr != nil {
				return fmt.Errorf("%s exited before becoming ready: %v\n%s", name, waitErr, tail)
			}
			return fmt.Errorf("%s exited before becoming ready\n%s", name, tail)
		case <-deadline:
			tail := logWriter.Tail(12)
			if tail != "" {
				return fmt.Errorf("%s did not become ready within 120s\n%s", name, tail)
			}
			return fmt.Errorf("%s did not become ready within 120s", name)
		case <-ticker.C:
			// Terminal by nature — name it now rather than spend the whole
			// readiness deadline and report a blank timeout.
			if tail := logWriter.Tail(12); portBindFailure(tail) {
				return fmt.Errorf("%s could not bind port %d — something else is already listening on it. "+
					"Stop that process (lsof -nP -iTCP:%d) or start the preview again to get a free port.\n%s",
					name, b.port, b.port, tail)
			}
			resp, err := devReadinessHTTPClient.Get(readyURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					// Somebody is listening — but it has to be US. A foreign
					// process on this port answers identically, which is how a
					// four-day-old freeswitch on :8081 would have been reported
					// as a healthy Metro. If our process has already exited,
					// that response was never ours.
					select {
					case waitErr := <-exitCh:
						tail := logWriter.Tail(12)
						return fmt.Errorf("%s exited during startup while port %d answered — "+
							"the port is owned by another process, not this dev server: %v\n%s",
							name, b.port, waitErr, tail)
					default:
					}
					b.mu.Lock()
					b.running = true
					// Clear any failure from an EARLIER start attempt. Without
					// this, /dev/status answers `running:true` next to a fatal
					// `npm install failed: ENOENT` from hours ago — observed on
					// the Mac mini 2026-07-25, where the stale string made a
					// healthy Next.js server read as broken to every client (and
					// to me, mid-debug). An error that is no longer true is the
					// same defect as a success that was never true.
					b.err = ""
					b.mu.Unlock()
					return nil
				}
			}
		}
	}
}

// devLogWriter writes dev server output to the agent log with a prefix.
// Also captures output for post-hoc inspection (e.g., checking for "Build Succeeded").
// When onLogLine is set, each output line is also emitted as a "log" SSE event.
type devLogWriter struct {
	prefix    string
	buf       []byte
	history   []string
	onLogLine func(line string) // callback to emit log events to SSE subscribers
}

// Contains returns true if any logged line contains the given substring.
func (w *devLogWriter) Contains(substr string) bool {
	for _, line := range w.history {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// Tail returns the last n non-empty log lines joined with newlines.
// Used when a subprocess dies before readiness so the surfaced error
// includes the actual stderr output instead of a blank "did not
// become ready".
func (w *devLogWriter) Tail(n int) string {
	if n <= 0 || len(w.history) == 0 {
		return ""
	}
	start := len(w.history) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(w.history[start:], "\n")
}

func (w *devLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		// Break on \r as well as \n.
		//
		// Flutter/Dart render compile progress as an in-place spinner
		// terminated by CARRIAGE RETURN, not newline. Splitting only on \n
		// meant that during the entire first web compile — the exact minutes
		// the user is staring at a spinner — no line ever "completed", so no
		// log event was emitted and the phone showed "waiting for the first
		// output from the box" while the box was working hard and saying
		// plenty. Treating \r as a line boundary surfaces that progress.
		idx := bytes.IndexAny(w.buf, "\r\n")
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			log.Printf("%s %s", w.prefix, line)
			w.history = append(w.history, trimmed)
			if w.onLogLine != nil {
				w.onLogLine(trimmed)
			}
		}
	}
	return len(p), nil
}

// ─── Expo Dev Server ───────────────────────────────────────────────────

type ExpoDevServer struct {
	baseDevServer
	devMode  string // "dev-client", "web", "expo-go"
	building bool   // true during native compilation (expo run:ios)

	// Sibling Expo Web process for the browser iframe on the Web Reload
	// tab. Runs *alongside* Metro (--dev-client) on a different port so
	// the Hermes bundle path (/dev/index.bundle?platform=ios|android)
	// keeps flowing to Metro untouched. Empty when the user hasn't
	// started a web preview. webMu guards both webCmd and webPort.
	webMu   sync.Mutex
	webCmd  *exec.Cmd
	webPort int
	webCtx  context.Context
}

func (e *ExpoDevServer) Name() string { return "expo" }

func (e *ExpoDevServer) Detect(workDir string) bool {
	pkg := filepath.Join(workDir, "package.json")
	data, err := os.ReadFile(pkg)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "\"expo\"")
}

func (e *ExpoDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	e.name = "expo"
	e.port = opts.Port
	if e.port == 0 {
		e.port = 8081
	}
	// If the default port is taken, find a free one
	if isPortInUse(e.port) {
		for p := e.port + 1; p < e.port+20; p++ {
			if !isPortInUse(p) {
				log.Printf("[dev:expo] Port %d in use, using %d instead", e.port, p)
				e.port = p
				break
			}
		}
	}

	// Install deps if needed — honor the project's package manager
	// (yarn / pnpm / bun / npm) instead of hardcoding npm, and surface
	// missing-runtime errors with an actionable next step the phone
	// can render ("Install Node" → POST /install/node).
	if err := ensureNodeDepsStreamed(ctx, opts.WorkDir, e.emitFn, e.name); err != nil {
		return err
	}

	// Run yaver.config.js if it exists (generates SDK config)
	configScript := filepath.Join(opts.WorkDir, "yaver.config.js")
	if _, err := os.Stat(configScript); err == nil {
		log.Printf("[dev] Running yaver.config.js...")
		gen := exec.CommandContext(ctx, "node", "yaver.config.js")
		gen.Dir = opts.WorkDir
		gen.Stdout = os.Stdout
		gen.Stderr = os.Stderr
		gen.Run() // best-effort
	}

	// Always native dev-client mode — Yaver is a debugger tool.
	// The native app runs separately on the phone with full hardware access.
	// Yaver mobile shows controls (Reload/Stop), never opens a WebView for this.
	hasNativeProject := fileExists(filepath.Join(opts.WorkDir, "ios", "Podfile")) ||
		fileExists(filepath.Join(opts.WorkDir, "android", "build.gradle"))

	// Web preview compiles JS through react-native-web — it does NOT
	// need native ios/android scaffolding. Skip prebuild for web,
	// otherwise we run a 30+ second android scaffold on every Expo
	// Web start AND failed prebuilds (e.g. missing Java, sfmg's
	// gitignored android/) bubble up as a confusing
	// "expo prebuild failed: exit status 1" error in the dashboard
	// even though `expo start --web` would have worked fine.
	needsPrebuild := !hasNativeProject && opts.Platform != "web"

	if needsPrebuild {
		// No native dirs — run expo prebuild first. Pick the platform
		// that actually builds on this OS: macOS can do iOS, Linux/WSL
		// only really has Android. Falling back to ios on Linux used
		// to silently waste time generating Xcode metadata that this
		// box can never compile.
		prebuildPlatform := "ios"
		if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			prebuildPlatform = "android"
		}
		log.Printf("[dev:expo] No native project — running expo prebuild --platform %s...", prebuildPlatform)
		prebuild := exec.CommandContext(ctx, "npx", "expo", "prebuild", "--platform", prebuildPlatform)
		prebuild.Dir = opts.WorkDir
		prebuild.Env = augmentEnv(nil)
		prebuild.Stdout = &devLogWriter{prefix: "[dev:expo:prebuild]"}
		prebuild.Stderr = &devLogWriter{prefix: "[dev:expo:prebuild]"}
		if err := prebuild.Run(); err != nil {
			return fmt.Errorf("expo prebuild failed: %w", err)
		}
	}

	if opts.Platform == "web" {
		log.Printf("[dev:expo] Starting Expo web preview (port %d)", e.port)
		e.devMode = "web"
		args := []string{"expo", "start",
			"--web",
			"--port", fmt.Sprintf("%d", e.port),
			"--host", "lan",
		}
		readyURL := fmt.Sprintf("http://127.0.0.1:%d", e.port)
		return e.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
	}

	// HERMES-FIRST FLOW: never run `expo run:ios` from dev server start.
	// Just start Metro with --host lan. The phone uses `/dev/build-native`
	// to compile a Hermes bundle and load it inside Yaver's container.
	// No native dev client install needed — super-host handles everything.
	log.Printf("[dev:expo] Starting Metro (port %d, Hermes-push mode)", e.port)
	e.devMode = "dev-client"
	args := []string{"expo", "start",
		"--dev-client",
		"--port", fmt.Sprintf("%d", e.port),
		"--host", "lan",
	}
	readyURL := fmt.Sprintf("http://127.0.0.1:%d", e.port)
	return e.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

// detectIOSDevice finds a connected iOS device (USB or wireless).
// Skips the Mac itself, simulators, and headers. Returns iPhone/iPad UDID.
func detectIOSDevice(ctx context.Context) string {
	out, err := xctraceListDevicesCommand(ctx).Output()
	if err != nil {
		return ""
	}
	inSimulators := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Track section
		if line == "== Simulators ==" {
			inSimulators = true
			continue
		}
		if line == "== Devices ==" {
			inSimulators = false
			continue
		}
		if inSimulators || line == "" || strings.HasPrefix(line, "==") {
			continue
		}
		// Skip MacBook/Mac entries — we want iPhone/iPad only
		if strings.Contains(line, "MacBook") || strings.Contains(line, "Mac ") ||
			strings.Contains(line, "iMac") || strings.Contains(line, "Mac Pro") ||
			strings.Contains(line, "Mac mini") || strings.Contains(line, "Mac Studio") {
			continue
		}
		// Must have a version number in parens (e.g. "(18.3.1)") to be a real device
		if !strings.Contains(line, ".") {
			continue
		}
		// Extract UDID from last parentheses
		if idx := strings.LastIndex(line, "("); idx > 0 {
			udid := strings.TrimSuffix(line[idx+1:], ")")
			if len(udid) > 20 && !strings.Contains(udid, " ") && !strings.Contains(udid, ".") {
				return udid
			}
		}
	}
	return ""
}

// yaverBuildsDir returns the directory for build markers.
func yaverBuildsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "builds")
}

// expoDepsHash returns a hash of package.json + Podfile.lock content.
// Changes when dependencies are added/removed/updated, triggering a rebuild.
func expoDepsHash(workDir string) string {
	h := sha256.New()
	for _, name := range []string{"package.json", filepath.Join("ios", "Podfile.lock")} {
		data, err := os.ReadFile(filepath.Join(workDir, name))
		if err == nil {
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (e *ExpoDevServer) BundleURL(platform string) string {
	// In dev-client mode (super-host), return the platform-specific Metro bundle path
	// so the Yaver app can load it natively via the secondary RCTBridge
	if e.devMode == "dev-client" && (platform == "ios" || platform == "android") {
		return fmt.Sprintf("/dev/index.bundle?platform=%s&dev=true&minify=false", platform)
	}
	return "/dev/"
}

func (e *ExpoDevServer) SupportsHotReload() bool { return true }

func (e *ExpoDevServer) Status() DevServerStatus {
	s := e.baseDevServer.Status()
	s.DevMode = e.devMode
	s.BundleURL = "/dev/"
	e.mu.Lock()
	s.Building = e.building
	e.mu.Unlock()
	if e.devMode == "dev-client" {
		// Metro URL for same-network dev client connections
		s.DeepLink = fmt.Sprintf("exp://%s:%d", getLocalIP(), e.port)
	}
	// Expose sibling Expo Web port when it's running. Doesn't touch
	// any existing field — BundleURL, DevMode, Port all stay pointed
	// at Metro. Clients that want the browser preview read WebPort
	// separately and route through /dev-web/*.
	e.webMu.Lock()
	s.WebPort = e.webPort
	// When the Expo Web sibling is up, THAT is what a browser client must load —
	// so say so in bundleUrl instead of leaving it pointed at Metro.
	//
	// The old contract was "clients that want the browser preview read WebPort
	// separately and route through /dev-web/*". No client ever did: mobile and web
	// both load bundleUrl, so an Expo browser preview fetched Metro's root, which
	// serves no page — a blank screen with a healthy status behind it. Telling the
	// client what to load beats documenting what it should have inferred.
	//
	// Hermes/native is unaffected: that lane uses /dev/build-native and the signed
	// native-bundle URLs, never bundleUrl.
	if e.webPort > 0 {
		s.BundleURL = "/dev-web/"
	}
	e.webMu.Unlock()
	if s.WebPort == 0 {
		// Direct browser lane: the main process IS the web server, so its port
		// is the browser-preview port. BundleURL stays /dev/ — the proxy to the
		// main port, with the base-href rewrite. See WebPort().
		if wp := e.WebPort(); wp > 0 {
			s.WebPort = wp
		}
	}
	return s
}

// StartWebPreview spawns an `expo start --web` sibling process on a
// free port alongside the running Metro dev-client. Idempotent —
// returns nil with no side effects if a web preview is already
// running. The Metro process (`e.cmd`) is never touched.
//
// Caller MUST verify `e.running == true` (Metro started) before
// calling; otherwise Expo Web is pointless on its own and the parent
// DevServerManager has no way to route /dev-web/* for us.
func (e *ExpoDevServer) StartWebPreview(parent context.Context, workDir string) (int, error) {
	// Direct browser lane: the MAIN process is already `expo start --web` and
	// serves the web target itself. Spawning a sibling here would be a second
	// full web compile of the same project — the manager auto-calls this for
	// every platform="web" start, and on a 4 GB box the redundant sibling
	// fights the real one for RAM. The main port is the web preview.
	if e.devMode == "web" {
		e.mu.Lock()
		running, port := e.running, e.port
		e.mu.Unlock()
		if running && port > 0 {
			return port, nil
		}
	}
	e.webMu.Lock()
	if e.webCmd != nil && e.webCmd.Process != nil && e.webPort > 0 {
		port := e.webPort
		pid := e.webCmd.Process.Pid
		alive := isProcessAlive(pid)
		listening := isPortInUse(port)
		if alive && listening {
			e.webMu.Unlock()
			return port, nil
		}
		log.Printf("[dev:expo] stale web preview handle pid=%d port=%d alive=%v listening=%v — starting a fresh expo --web sibling",
			pid, port, alive, listening)
		e.webCmd = nil
		e.webPort = 0
		e.webCtx = nil
	}
	if e.webCtx != nil {
		e.webCtx = nil
	}
	if e.webPort > 0 && !isPortInUse(e.webPort) {
		log.Printf("[dev:expo] stale web preview port %d had no listener — clearing before restart", e.webPort)
		e.webPort = 0
	}
	e.webMu.Unlock()

	// Pick a free port >=19006 (Expo Web's historical default).
	// Scanning avoids colliding with Metro on 8081/8082 or with a
	// previous Expo Web that's still in TIME_WAIT.
	port := 19006
	for p := port; p < port+50; p++ {
		if !isPortInUse(p) {
			port = p
			break
		}
	}
	if isPortInUse(port) {
		return 0, fmt.Errorf("no free port near 19006 for expo --web")
	}

	ctx, cancel := context.WithCancel(parent)
	args := []string{"expo", "start",
		"--web",
		"--port", fmt.Sprintf("%d", port),
		"--host", "lan",
	}
	// Isolate this Expo's cache so it doesn't fight Metro over .expo/
	// bundler state. Two concurrent `expo start` invocations on the
	// same project without separate cache dirs occasionally race on
	// watchman manifest writes; dedicated dirs eliminate the risk.
	//
	// The isolation comes from a DEDICATED SUBDIR of the persistent
	// per-project cache — not from a throwaway temp dir, which started
	// this lane with a cold Metro cache on every single open (part of
	// the ~87 s "web ui" incident; see devserver_metro_cache.go).
	cacheDir, cacheEphemeral := "", false
	if base := metroCacheDir(workDir); base != "" {
		cacheDir = filepath.Join(base, "expo-web")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			cacheDir = ""
		}
	}
	if cacheDir == "" {
		cacheDir, _ = os.MkdirTemp("", "yaver-expo-web-*")
		cacheEphemeral = true
	}
	cmd, err := newRuntimeCommandContext(ctx, "npx", args...)
	if err != nil {
		cancel()
		if cacheEphemeral {
			os.RemoveAll(cacheDir)
		}
		return 0, fmt.Errorf("prepare expo --web command: %w", err)
	}
	cmd.Dir = workDir
	extraEnv := []string{
		fmt.Sprintf("EXPO_METRO_CACHE_DIR=%s", cacheDir),
		// Don't open a browser tab on the remote machine.
		"BROWSER=none",
		"CI=1",
	}
	cmd.Env = append(augmentEnv(nil), extraEnv...)
	// Same group-kill rationale as baseDevServer.startProcess: the npx
	// shell forks node + metro children — without Setpgid, StopWebPreview
	// only reaps the shell and leaves the metro process bound to its port.
	setProcGroup(cmd)

	logWriter := &devLogWriter{prefix: "[dev:expo:web]"}
	if e.emitFn != nil {
		emitFn := e.emitFn
		logWriter.onLogLine = func(line string) {
			emitFn(DevServerEvent{
				Type:      "log",
				Framework: "expo-web",
				LogLine:   line,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	emitDevCommand(e.emitFn, "expo-web", "npx", args, workDir)

	if err := cmd.Start(); err != nil {
		cancel()
		if cacheEphemeral {
			os.RemoveAll(cacheDir)
		}
		return 0, fmt.Errorf("expo --web failed to start: %w", err)
	}

	// Survives the agent (own process group) — so write it down now, not later.
	RecordDevChild(devChildRecord{
		PID: cmd.Process.Pid, Port: port, Kind: "expo-web",
		Match: fmt.Sprintf("expo start,--web,%d", port), WorkDir: workDir,
	})

	e.webMu.Lock()
	e.webCmd = cmd
	e.webPort = port
	e.webCtx = ctx
	e.webMu.Unlock()

	// Reap the child and clean up state when it exits on its own.
	go func() {
		cmd.Wait()
		ForgetDevChild(cmd.Process.Pid)
		e.webMu.Lock()
		if e.webCmd == cmd {
			e.webCmd = nil
			e.webPort = 0
			e.webCtx = nil
		}
		e.webMu.Unlock()
		cancel()
		if cacheEphemeral {
			os.RemoveAll(cacheDir)
		}
		if e.emitFn != nil {
			e.emitFn(DevServerEvent{
				Type:      "stopped",
				Framework: "expo-web",
				Message:   "Expo Web preview stopped",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}()

	return port, nil
}

// StopWebPreview terminates the sibling Expo Web process if running.
// Safe to call when nothing is running. Metro (`e.cmd`) is untouched.
func (e *ExpoDevServer) StopWebPreview() error {
	e.webMu.Lock()
	cmd := e.webCmd
	e.webMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if err := killProcessGroup(pid, "INT"); err != nil {
		cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		killProcessGroup(pid, "KILL")
		cmd.Process.Kill()
	}
	e.webMu.Lock()
	e.webCmd = nil
	e.webPort = 0
	e.webCtx = nil
	e.webMu.Unlock()
	return nil
}

// WebPort returns the port serving the browser preview: the sibling Expo Web
// process when one runs, the MAIN port when the main process itself is the web
// server (devMode "web", i.e. `expo start --web` started directly), and 0 only
// when nothing can serve a browser. The direct lane has no sibling by design —
// leaving this 0 there made /dev/status read devMode "web" + webPort 0 as "the
// preview exited" for every healthy direct preview (observed live against
// agent 1.99.371: expo serving 200 on :8082 while status said exited), and
// left /dev-web/ with nothing to route to.
func (e *ExpoDevServer) WebPort() int {
	e.webMu.Lock()
	sibling := e.webPort
	e.webMu.Unlock()
	if sibling > 0 {
		return sibling
	}
	if e.devMode == "web" {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.running && e.port > 0 {
			return e.port
		}
	}
	return 0
}

// Stop overrides baseDevServer.Stop to also terminate any sibling
// Expo Web process. Metro is stopped first (the primary surface), then
// the web preview — ordering doesn't really matter, but Metro going
// first matches user expectation when they click "Stop Serving".
func (e *ExpoDevServer) Stop() error {
	_ = e.StopWebPreview()
	return e.baseDevServer.Stop()
}

// ExpoDeepLink returns the exp:// URL for the dev client.
func (e *ExpoDevServer) ExpoDeepLink(agentHost string) string {
	return fmt.Sprintf("exp://%s:%d", agentHost, e.port)
}

// isPortInUse checks if a TCP port is already bound.
// isPortInUse delegates to portBusy — the ONE port-availability check (see
// devport_allocator.go). It used to be its own single wildcard bind, which misses
// a loopback-only listener (Go sets SO_REUSEADDR, so the wildcard bind succeeds
// next to one) — and the agent's proxy dials loopback, so that listener would have
// received the traffic. Two checks that disagree is how the Expo Web sibling could
// pick a port something else already answered.
func isPortInUse(port int) bool { return portBusy(port) }

func (e *ExpoDevServer) Reload() error {
	// Metro auto-reloads on file change; this is a manual force.
	// --host lan --dev-client mode makes /reload flaky on 127.0.0.1
	// (Metro binds to LAN IP, or the endpoint is gone in newer Metro).
	// Best-effort HTTP here; the caller (handleDevServerReload) also
	// broadcasts a `reload` command over the blackbox channel, which
	// is the path that actually reloads mobile clients. Return nil
	// either way so a Metro HTTP hiccup doesn't abort the real path.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, httpErr := client.Get(fmt.Sprintf("http://127.0.0.1:%d/reload", e.port))
	if httpErr != nil {
		log.Printf("[dev:expo] /reload HTTP unreachable (soft-fail, broadcast will still fire): %v", httpErr)
		return nil
	}
	resp.Body.Close()
	return nil
}

// ─── React Native (bare) Dev Server ────────────────────────────────────

// ReactNativeDevServer handles bare React Native projects (without Expo).
// Uses `npx react-native start` / Expo web fallback for browser-style preview
// surfaces only. The first-class mobile path remains Hermes bundle reload
// inside Yaver, not a WebView.
type ReactNativeDevServer struct {
	baseDevServer
	// metroOnly is set when the Expo-web attempt failed and we fell back to plain
	// `react-native start`. Metro serves no HTML, so the browser lane must not
	// claim it can render this project.
	metroOnly bool
}

func (rn *ReactNativeDevServer) Name() string { return "react-native" }

func (rn *ReactNativeDevServer) Detect(workDir string) bool {
	pkg := filepath.Join(workDir, "package.json")
	data, err := os.ReadFile(pkg)
	if err != nil {
		return false
	}
	content := string(data)
	// Has react-native but NOT expo (Expo is handled by ExpoDevServer)
	return strings.Contains(content, `"react-native"`) && !strings.Contains(content, `"expo"`)
}

func (rn *ReactNativeDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	rn.name = "react-native"
	rn.port = opts.Port
	if rn.port == 0 {
		rn.port = 8081
	}

	// Install deps if needed — honor project package manager and
	// surface missing-runtime errors with an actionable next step.
	if err := ensureNodeDepsStreamed(ctx, opts.WorkDir, rn.emitFn, rn.name); err != nil {
		return err
	}

	// Try npx expo start --web first (works if expo CLI is available, even for bare RN)
	// Fall back to npx react-native start if expo isn't available
	args := []string{"expo", "start",
		"--web",
		"--port", fmt.Sprintf("%d", rn.port),
		"--host", "lan",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d", rn.port)
	err := rn.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
	if err != nil {
		// Fallback: plain Metro. NOTE what this costs — Metro is a BUNDLER, not a
		// web server: it serves no HTML at `/`. A browser preview pointed here
		// renders nothing, forever, with a healthy-looking status behind it (the
		// same shape as the Expo-browser-lane bug). Record it so BundleURL can be
		// honest instead of promising a page that does not exist.
		log.Printf("[dev] Expo CLI not available, falling back to Metro bundler — this project has NO web target, so the browser lane cannot render it")
		rn.mu.Lock()
		rn.metroOnly = true
		rn.mu.Unlock()
		args = []string{"react-native", "start",
			"--port", fmt.Sprintf("%d", rn.port),
			"--host", "lan",
		}
		return rn.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
	}
	return nil
}

func (rn *ReactNativeDevServer) BundleURL(platform string) string {
	// Bare Metro has no HTML to serve. Returning "/dev/" anyway told every client
	// "there is a page here" and produced a blank browser preview; an empty
	// bundleUrl makes the client say "no web target" instead of rendering nothing.
	rn.mu.Lock()
	metroOnly := rn.metroOnly
	rn.mu.Unlock()
	if metroOnly {
		return ""
	}
	return "/dev/"
}

// Status adds the reason a bare-RN project cannot be previewed in a browser, so
// the surface the user is looking at can say it out loud.
func (rn *ReactNativeDevServer) Status() DevServerStatus {
	s := rn.baseDevServer.Status()
	rn.mu.Lock()
	metroOnly := rn.metroOnly
	rn.mu.Unlock()
	if metroOnly {
		s.BundleURL = ""
		if s.Error == "" {
			s.Error = "This project runs on bare Metro (no Expo CLI, no web target), so there is no " +
				"page to show in a browser. Use the Hermes lane for a real device, or add " +
				"react-native-web + expo CLI to get a browser build."
		}
	}
	return s
}

func (rn *ReactNativeDevServer) SupportsHotReload() bool { return true }

func (rn *ReactNativeDevServer) Reload() error {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/reload", rn.port))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ─── Flutter Dev Server ────────────────────────────────────────────────

type FlutterDevServer struct {
	baseDevServer
	stdinPipe *stdinWriter
}

func (f *FlutterDevServer) Name() string { return "flutter" }

// stdinWriter wraps an io.WriteCloser for sending commands to the Flutter process.
type stdinWriter struct {
	w interface{ Write([]byte) (int, error) }
}

func (f *FlutterDevServer) Detect(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, "pubspec.yaml"))
	return err == nil
}

func (f *FlutterDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	f.name = "flutter"
	f.port = opts.Port
	if f.port == 0 {
		f.port = 9100
	}

	deviceID := opts.Platform
	preferredPlatform := ""
	switch deviceID {
	case "ios", "android":
		preferredPlatform = deviceID
		deviceID = ""
	case "web", "chrome", "web-server":
		// BROWSER LANE: always serve `-d web-server`, never a detected device.
		//
		// The old code called detectFlutterMobileDevice even for an explicit web
		// request and used whatever it found — so with a paired iPhone connected,
		// the browser lane ran Flutter NATIVELY on the phone (a full iOS build)
		// and never bound the web port. The preview then 503'd forever
		// ("dev server unavailable" / "still starting"). A web request means web.
		log.Printf("[dev:flutter] browser lane requested (%q) — serving -d web-server on :%d, skipping mobile-device detection", deviceID, f.port)
		deviceID = "web-server"
	}
	if deviceID == "" {
		// NO PLATFORM ASKED FOR → WEB. This default is inverted from what it used
		// to be, and the inversion is the fix.
		//
		// It used to detect a mobile device and run Flutter NATIVELY on it. That
		// is right for someone typing `yaver dev` at a terminal and wrong for
		// every preview surface, because a native Flutter run produces nothing
		// the requesting surface can display: Flutter is DevServerKindWeb and can
		// never load into the Yaver container (Hermes is RN-only). So the phone
		// asked for a preview, the agent quietly started a native iOS build, and
		// the card read `mode · native install · target · this device · Failed to
		// compile application.` — a lane the user never chose, failing for
		// reasons that had nothing to do with what they wanted (2026-07-25, the
		// e-mobile Flutter recording).
		//
		// The default must be the lane that can ALWAYS be shown. Native Flutter
		// hot reload is still available — it just has to be asked for by name
		// (platform=ios / platform=android), which is exactly the request that
		// carries the intent to use it.
		log.Printf("[dev:flutter] no platform requested — serving -d web-server on :%d (the only Flutter lane a preview surface can display; pass platform=ios/android for native hot reload)", f.port)
		deviceID = "web-server"
	}
	if preferredPlatform != "" {
		// Explicit ios/android: honour it, and say so when there is nothing to
		// run on rather than silently substituting a different lane.
		detected := detectFlutterMobileDevice(ctx, preferredPlatform, opts.Target)
		if detected == "" {
			return fmt.Errorf("no %s device or simulator is available for native Flutter hot reload on this machine — "+
				"boot one, or use the browser preview (platform=web), which needs no device", preferredPlatform)
		}
		deviceID = detected
	}

	args := []string{"run", "-d", deviceID}

	// Web-server needs port config; native devices don't
	if deviceID == "web-server" || deviceID == "chrome" {
		// A Flutter project can only be served with `-d web-server` if it has
		// web support (a web/ dir). A mobile-only project created without it has
		// none — `flutter run -d web-server` then errors and the browser-lane
		// preview shows "dev server unavailable" forever (this is exactly what
		// demo/mobile/todo-flutter and e-mobile hit: no web/ dir). Add web
		// support idempotently first so the browser lane actually serves.
		if _, statErr := os.Stat(filepath.Join(opts.WorkDir, "web")); os.IsNotExist(statErr) {
			log.Printf("[dev:flutter] %s has no web/ dir — enabling web support (flutter create --platforms web .)", opts.WorkDir)
			cre := exec.CommandContext(ctx, resolveSpawnPath("flutter"), "create", "--platforms", "web", ".")
			cre.Dir = opts.WorkDir
			if out, cerr := cre.CombinedOutput(); cerr != nil {
				log.Printf("[dev:flutter] flutter create --platforms web failed: %v — %.300s", cerr, string(out))
			} else {
				log.Printf("[dev:flutter] web support added to %s", opts.WorkDir)
			}
		}
		// Port choice is brokered centrally (devserver_ports.go) before Start is
		// called, so by here f.port is already a port no other Yaver session
		// holds. Readiness below still verifies the listener is OURS — a
		// reservation stops Yaver colliding with itself, not the whole machine.
		args = append(args, "--web-port", fmt.Sprintf("%d", f.port), "--web-hostname", "0.0.0.0")
	}

	log.Printf("[dev:flutter] Starting on device: %s (workDir=%s, port=%d)", deviceID, opts.WorkDir, f.port)

	if deviceID == "web-server" || deviceID == "chrome" {
		// Web mode — wait for HTTP readiness
		readyURL := fmt.Sprintf("http://127.0.0.1:%d/", f.port)
		return f.startProcessWithStdin(ctx, "flutter", args, opts.WorkDir, nil, readyURL)
	}

	// Native mode — no HTTP readiness check, just wait for "is available" in output
	return f.startNativeProcess(ctx, "flutter", args, opts.WorkDir)
}

func normalizeDeviceName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		"’", "",
		"'", "",
		"`", "",
		"“", "",
		"”", "",
		"\"", "",
		"(", " ",
		")", " ",
		"-", " ",
		"_", " ",
	)
	value = replacer.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func flutterDeviceMatchesTarget(deviceName string, target DevServerTarget) bool {
	if strings.TrimSpace(target.DeviceName) == "" {
		return false
	}
	deviceNorm := normalizeDeviceName(deviceName)
	targetNorm := normalizeDeviceName(target.DeviceName)
	return deviceNorm != "" && targetNorm != "" &&
		(strings.Contains(deviceNorm, targetNorm) || strings.Contains(targetNorm, deviceNorm))
}

// detectFlutterMobileDevice runs `flutter devices --machine` and returns a mobile device ID.
// If preferredPlatform is "ios" or "android", it prefers that class first.
// If a Yaver preview target is selected, it tries to match by device name first.
func detectFlutterMobileDevice(ctx context.Context, preferredPlatform string, target DevServerTarget) string {
	out, err := exec.CommandContext(ctx, resolveSpawnPath("flutter"), "devices", "--machine").Output()
	if err != nil {
		return ""
	}

	var devices []struct {
		Name           string `json:"name"`
		ID             string `json:"id"`
		TargetPlatform string `json:"targetPlatform"`
	}
	if err := json.Unmarshal(out, &devices); err != nil {
		return ""
	}

	matchesPreferred := func(target string) bool {
		switch preferredPlatform {
		case "ios":
			return target == "ios"
		case "android":
			return strings.HasPrefix(target, "android")
		default:
			return false
		}
	}

	isMobile := func(target string) bool {
		return target == "ios" || strings.HasPrefix(target, "android")
	}

	if target.DeviceName != "" {
		for _, d := range devices {
			if !isMobile(d.TargetPlatform) {
				continue
			}
			if preferredPlatform != "" && !matchesPreferred(d.TargetPlatform) {
				continue
			}
			if flutterDeviceMatchesTarget(d.Name, target) {
				log.Printf("[dev:flutter] Matched selected Yaver target %q to Flutter device %s (%s) [%s]", target.DeviceName, d.Name, d.ID, d.TargetPlatform)
				return d.ID
			}
		}
	}

	if preferredPlatform != "" {
		for _, d := range devices {
			if matchesPreferred(d.TargetPlatform) {
				log.Printf("[dev:flutter] Found preferred mobile device: %s (%s) [%s]", d.Name, d.ID, d.TargetPlatform)
				return d.ID
			}
		}
	}

	// Otherwise prefer iOS, then Android — skip desktop/web.
	for _, d := range devices {
		if isMobile(d.TargetPlatform) {
			log.Printf("[dev:flutter] Found mobile device: %s (%s) [%s]", d.Name, d.ID, d.TargetPlatform)
			return d.ID
		}
	}
	return ""
}

// startNativeProcess starts a native Flutter process (no HTTP readiness — watches stdout for "ready" signals).
func (f *FlutterDevServer) startNativeProcess(ctx context.Context, name string, args []string, workDir string) error {
	f.mu.Lock()
	f.workDir = workDir
	f.mu.Unlock()

	cmd := exec.CommandContext(ctx, resolveSpawnPath(name), args...)
	cmd.Dir = workDir
	cmd.Env = augmentEnv(nil)

	// Create stdin pipe for hot reload ("r") and hot restart ("R")
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	f.stdinPipe = &stdinWriter{w: pipe}

	// Capture stdout to detect when app is ready + log output
	logWriter := &devLogWriter{prefix: "[dev:flutter]"}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	f.mu.Lock()
	f.cmd = cmd
	f.startedAt = time.Now()
	// Mark as running immediately for native — the app will build and deploy
	f.running = true
	f.mu.Unlock()

	log.Printf("[dev:flutter] Native process started (PID %d) — building and deploying to device...", cmd.Process.Pid)
	return nil
}

// startProcessWithStdin is like startProcess but saves the stdin pipe for hot reload.
func (f *FlutterDevServer) startProcessWithStdin(ctx context.Context, name string, args []string, workDir string, env []string, readyURL string) error {
	f.mu.Lock()
	f.workDir = workDir
	f.mu.Unlock()

	cmd := exec.CommandContext(ctx, resolveSpawnPath(name), args...)
	cmd.Dir = workDir
	// augmentEnv prepends ~/.yaver/runtimes/node/bin to PATH so
	// `npx` / `node` invocations resolve to the agent-managed Node
	// runtime on a fresh Linux box that never had system Node.
	// applyMetroCacheEnv pins TMPDIR to the persistent per-project
	// cache dir so Metro/Expo transform caches survive restarts and
	// /tmp cleanups (see devserver_metro_cache.go).
	cmd.Env = append(applyMetroCacheEnv(augmentEnv(nil), workDir), env...)
	// Put the dev server in its own process group so Stop() can take down
	// all child processes (vite/next fork node + esbuild; killing only the
	// shell PID leaks them and the dev port stays bound until reboot).
	setProcGroup(cmd)

	// Feed Flutter's stdout into the progress tracker (SUMMARIZED phase events:
	// pub get → compiling → launching → serving) AND stream the raw lines as
	// "log" events.
	//
	// Originally this path deliberately emitted NO per-line log, on the theory
	// the phone wanted a summary not a firehose. In practice that left the
	// preview overlay showing "waiting for the first output from the box" for
	// the entire 60-90s first compile — the box was working hard and saying
	// nothing, which reads as hung (reported from TestFlight on e-mobile). A
	// user watching a slow compile needs to SEE it compiling. So we stream the
	// lines, lightly throttled below so a burst does not flood the SSE channel.
	logWriter := &devLogWriter{prefix: fmt.Sprintf("[dev:%s]", f.name)}
	{
		tracker := f.tracker
		recordLogFn := f.recordLogFn
		emitFn := f.emitFn
		framework := f.name
		var lastEmit time.Time
		logWriter.onLogLine = func(line string) {
			if recordLogFn != nil {
				recordLogFn(line)
			}
			if tracker != nil {
				tracker.FeedLine(line)
			}
			if emitFn == nil {
				return
			}
			// Throttle to ~4/s: Flutter's compile spinner (now surfaced via the
			// \r handling in devLogWriter.Write) can update very fast, and the
			// user wants "it's alive", not every frame. Always let a
			// newline-terminated substantive line through even inside the
			// window so real phase messages are never dropped.
			now := time.Now()
			if now.Sub(lastEmit) < 250*time.Millisecond {
				return
			}
			lastEmit = now
			emitFn(DevServerEvent{
				Type:      "log",
				Framework: framework,
				LogLine:   line,
				Timestamp: now.UTC().Format(time.RFC3339),
			})
		}
	}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter

	// Announce the exact command AND directory before the first compiler byte.
	emitDevCommand(f.emitFn, f.name, name, args, workDir)

	// Create stdin pipe and save it for Reload()
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	f.stdinPipe = &stdinWriter{w: pipe}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	f.mu.Lock()
	f.cmd = cmd
	f.startedAt = time.Now()
	f.mu.Unlock()

	// Abort the instant the process dies (e.g. a Flutter compile error such as a
	// missing pubspec asset) instead of blocking the whole 180s, and bubble up
	// the output tail so the user sees the REAL reason, not a blank timeout.
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

	// Wait for dev server to become ready
	deadline := time.After(180 * time.Second) // Flutter web first build can take 3+ min
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case waitErr := <-exitCh:
			tail := logWriter.Tail(25)
			if waitErr != nil {
				return fmt.Errorf("%s exited before becoming ready: %v\n%s", name, waitErr, tail)
			}
			return fmt.Errorf("%s exited before becoming ready\n%s", name, tail)
		case <-deadline:
			tail := logWriter.Tail(25)
			if tail != "" {
				return fmt.Errorf("%s did not become ready within 180s\n%s", name, tail)
			}
			return fmt.Errorf("%s did not become ready within 180s", name)
		case <-ticker.C:
			// A bind failure is terminal — report it by name instead of
			// letting the 180s deadline turn a five-word cause into a
			// blank timeout. (Flutter keeps the process alive for a
			// moment after printing this, so waiting on exitCh alone is
			// not enough.)
			if tail := logWriter.Tail(25); portBindFailure(tail) {
				return fmt.Errorf("%s could not bind port %d — something else is already listening on it. "+
					"Stop that process (lsof -nP -iTCP:%d) or start the preview again to get a free port.\n%s",
					name, f.port, f.port, tail)
			}
			resp, err := devReadinessHTTPClient.Get(readyURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					// Somebody is listening — but it must be US. A foreign
					// process on this port answers identically, which is how a
					// dead session got reported as `serving:true` while the user
					// was shown another project's app. If our process has
					// already exited, that 200 was not ours.
					select {
					case waitErr := <-exitCh:
						tail := logWriter.Tail(25)
						return fmt.Errorf("%s exited during startup while port %d answered — "+
							"the port is owned by another process, not this preview: %v\n%s",
							name, f.port, waitErr, tail)
					default:
					}
					f.mu.Lock()
					f.running = true
					f.mu.Unlock()
					return nil
				}
			}
		}
	}
}

func (f *FlutterDevServer) BundleURL(platform string) string {
	return "/dev/"
}

func (f *FlutterDevServer) SupportsHotReload() bool { return true }

func (f *FlutterDevServer) Reload() error {
	return f.ReloadWithMode("fast")
}

// ReloadWithMode maps the fast/full reload contract onto Flutter's
// stdin protocol: fast = "r" (hot reload, preserves app state), full =
// "R" (hot restart, resets app state). Neither clears any cache — full
// means restart, not cold-start.
func (f *FlutterDevServer) ReloadWithMode(mode string) error {
	if f.stdinPipe != nil && f.stdinPipe.w != nil {
		key := "r\n"
		if mode == "full" {
			key = "R\n"
		}
		_, err := f.stdinPipe.w.Write([]byte(key))
		return err
	}
	return fmt.Errorf("flutter process stdin not available")
}

// ─── Vite Dev Server ───────────────────────────────────────────────────

type ViteDevServer struct {
	baseDevServer
}

func (v *ViteDevServer) Name() string { return "vite" }

func (v *ViteDevServer) Detect(workDir string) bool {
	for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			return true
		}
	}
	return false
}

func (v *ViteDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	v.name = "vite"
	v.port = opts.Port
	if v.port == 0 {
		v.port = 5173
	}

	if err := ensureNodeDepsStreamed(ctx, opts.WorkDir, v.emitFn, v.name); err != nil {
		return err
	}

	// Vite's --host doesn't accept Expo's "lan" keyword — pass 0.0.0.0
	// to bind on every interface (LAN + loopback) so the relay tunnel
	// + LAN preview both reach it. The yaver agent fronts /dev/* so
	// browser-side access is via the tunnelled endpoint either way.
	args := []string{"vite",
		"--port", fmt.Sprintf("%d", v.port),
		"--host", "0.0.0.0",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/", v.port)
	return v.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

func (v *ViteDevServer) BundleURL(platform string) string { return "/dev/" }
func (v *ViteDevServer) SupportsHotReload() bool          { return true }
func (v *ViteDevServer) Reload() error                    { return nil } // Vite auto-reloads

// ─── Next.js Dev Server ────────────────────────────────────────────────

type NextDevServer struct {
	baseDevServer
}

func (n *NextDevServer) Name() string { return "nextjs" }

func (n *NextDevServer) Detect(workDir string) bool {
	for _, name := range []string{"next.config.ts", "next.config.js", "next.config.mjs", "next.config.cjs"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			return true
		}
	}
	// A Next.js app does NOT need a next.config file — `create-next-app` with
	// default settings ships none. Requiring one classified such a project as
	// generic "react", which has no dev server at all, so its browser lane
	// answered 404 through the proxy and its WebRTC lane said "react projects use
	// webview, not WebRTC" — two dead ends for an ordinary Next app
	// (yaver-todo-web, 2026-07-25). The package manifest is the authority: a
	// `next` dependency or a `next dev` script IS a Next project.
	return packageJSONDeclaresNext(workDir)
}

// packageJSONDeclaresNext reports whether package.json names next as a dependency
// or drives it from a script.
func packageJSONDeclaresNext(workDir string) bool {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	if _, ok := pkg.Dependencies["next"]; ok {
		return true
	}
	if _, ok := pkg.DevDependencies["next"]; ok {
		return true
	}
	for _, script := range pkg.Scripts {
		fields := strings.Fields(script)
		for i, f := range fields {
			// "next dev", "npx next dev", "cross-env … next dev" — the token
			// pair is what matters, not the prefix.
			if f == "next" && i+1 < len(fields) && (fields[i+1] == "dev" || fields[i+1] == "start") {
				return true
			}
		}
	}
	return false
}

func (n *NextDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	n.name = "nextjs"
	n.port = opts.Port
	if n.port == 0 {
		n.port = 3000
	}

	if err := ensureNodeDepsStreamed(ctx, opts.WorkDir, n.emitFn, n.name); err != nil {
		return err
	}

	args := []string{"next", "dev",
		"--port", fmt.Sprintf("%d", n.port),
		"--hostname", "0.0.0.0",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/", n.port)
	return n.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

func (n *NextDevServer) BundleURL(platform string) string { return "/dev/" }
func (n *NextDevServer) SupportsHotReload() bool          { return true }
func (n *NextDevServer) Reload() error                    { return nil } // Next.js auto-reloads
