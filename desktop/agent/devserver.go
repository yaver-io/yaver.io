package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
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
	Framework         string        `json:"framework"`
	Kind              DevServerKind `json:"kind,omitempty"`
	Running           bool          `json:"running"`
	Serving           bool          `json:"serving"`
	ServingLabel      string        `json:"servingLabel,omitempty"`
	StopActionLabel   string        `json:"stopActionLabel,omitempty"`
	Building          bool          `json:"building,omitempty"` // true during native compilation (expo run:ios, etc.)
	Port              int           `json:"port"`
	BundleURL         string        `json:"bundleUrl"`
	DirectURL         string        `json:"directUrl,omitempty"`
	DeepLink          string        `json:"deepLink,omitempty"`
	DevMode           string        `json:"devMode,omitempty"` // "dev-client", "web", "expo-go", "" (for non-Expo)
	StartedAt         string        `json:"startedAt,omitempty"`
	Error             string        `json:"error,omitempty"`
	PID               int           `json:"pid,omitempty"`
	WorkDir           string        `json:"workDir,omitempty"`
	HotReload         bool          `json:"hotReload"`
	TargetDeviceID    string        `json:"targetDeviceId,omitempty"`
	TargetDeviceName  string        `json:"targetDeviceName,omitempty"`
	TargetDeviceClass string        `json:"targetDeviceClass,omitempty"`
	IOSInstallMethod  string        `json:"iosInstallMethod,omitempty"`
	IOSInstallReason  string        `json:"iosInstallReason,omitempty"`
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
}

// DevServerSnapshot is a complete picture of every active topic + the
// last known progress + the most recent log lines. Consumer renders
// from this and never feels "stuck" because a fresh one arrives every
// 5s regardless of whether anything happened.
type DevServerSnapshot struct {
	GeneratedAt string            `json:"generatedAt"`
	Running     bool              `json:"running"`
	Framework   string            `json:"framework,omitempty"`
	Surface     string            `json:"surface,omitempty"`
	Port        int               `json:"port,omitempty"`
	WebPort     int               `json:"webPort,omitempty"`
	WorkDir     string            `json:"workDir,omitempty"`
	UptimeSec   int               `json:"uptimeSec,omitempty"`
	Pid         int               `json:"pid,omitempty"`
	PidAlive    bool              `json:"pidAlive,omitempty"`
	IdleSec     int               `json:"idleSec,omitempty"`
	Phases      map[string]string `json:"phases,omitempty"`   // topic → current phase
	Progress    *ProgressSnapshot `json:"progress,omitempty"` // most recent active progress
	WebProgress *ProgressSnapshot `json:"webProgress,omitempty"`
	RecentLogs  []string          `json:"recentLogs,omitempty"` // last 8 stdout/stderr lines
	BeatNumber  int               `json:"beatNumber,omitempty"`
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
}

// ─── DevServerManager ──────────────────────────────────────────────────

// DevServerManager manages the active dev server session and event subscribers.
type DevServerManager struct {
	mu     sync.RWMutex
	active *devServerSession
	subs   []chan DevServerEvent
	subsMu sync.Mutex
	target DevServerTarget

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
	Target    string `json:"target"`    // "web-js-bundle" | "web-hermes-wasm"
	BuildDir  string `json:"buildDir"`  // absolute path on host
	IndexFile string `json:"indexFile"` // typically "index.html"
	Size      int64  `json:"size"`      // total bundle bytes
	FileCount int    `json:"fileCount"` // file count for js-bundle target
	BuiltAt   string `json:"builtAt"`   // RFC3339 build completion timestamp
	Caller    string `json:"caller"`    // X-Yaver-Caller of the build trigger
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
	// failed is true when ds.Start returned an error; we keep the
	// session around so Status() still reports the failure. A
	// subsequent Start() on the same framework clears it.
	failed bool
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
		ds = getDevServerByName(framework)
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
	ds.PreStart(frameworkName, defaultPort, workDir)

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
		WorkDir:  workDir,
		Port:     port,
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
		server: ds,
		ctx:    ctx,
		cancel: cancel,
		target: target,
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
			if setter, ok := ds.(interface{ SetError(string) }); ok {
				setter.SetError(err.Error())
			}
			m.mu.Lock()
			if m.active != nil && m.active.server == ds {
				m.active.cancel()
				m.active.failed = true
			}
			m.mu.Unlock()
			m.emit(DevServerEvent{
				Type:      "error",
				Framework: ds.Name(),
				Message:   fmt.Sprintf("Failed to start %s: %v", ds.Name(), err),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
			return
		}

		// Create reverse proxy to the dev server
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", ds.Port()))
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "dev server unavailable", http.StatusBadGateway)
		}

		m.mu.Lock()
		if m.active != nil && m.active.server == ds {
			m.active.proxy = proxy
		}
		m.mu.Unlock()

		log.Printf("[dev] %s ready on port %d", ds.Name(), ds.Port())

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
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == nil {
		return fmt.Errorf("no dev server running")
	}

	if !m.active.server.SupportsHotReload() {
		return fmt.Errorf("%s does not support hot reload", m.active.server.Name())
	}

	if err := m.active.server.Reload(); err != nil {
		return err
	}

	m.emit(DevServerEvent{
		Type:      "reload",
		Framework: m.active.server.Name(),
		BundleURL: m.active.server.BundleURL(""),
		Message:   "Hot reload triggered",
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
	s.TargetDeviceID = m.active.target.DeviceID
	s.TargetDeviceName = m.active.target.DeviceName
	s.TargetDeviceClass = m.active.target.DeviceClass
	return &s
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
	defer m.recentLogMu.Unlock()
	const max = 8
	m.recentLogTail = append(m.recentLogTail, line)
	if len(m.recentLogTail) > max {
		m.recentLogTail = m.recentLogTail[len(m.recentLogTail)-max:]
	}
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
}

func (b *baseDevServer) Status() DevServerStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := DevServerStatus{
		Framework: b.name,
		Running:   b.running,
		Serving:   b.running,
		Port:      b.port,
		HotReload: true,
		WorkDir:   b.workDir,
		Error:     b.err,
	}
	if b.running {
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

// startProcess launches a command and waits for readiness by polling a URL.
func (b *baseDevServer) startProcess(ctx context.Context, name string, args []string, workDir string, env []string, readyURL string) error {
	b.mu.Lock()
	b.workDir = workDir
	b.mu.Unlock()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	// augmentEnv prepends ~/.yaver/runtimes/node/bin to PATH so
	// `npx` / `node` invocations resolve to the agent-managed Node
	// runtime on a fresh Linux box that never had system Node.
	cmd.Env = append(augmentEnv(nil), env...)
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

	// Keep stdin open (Flutter needs it for "r" hot reload)
	stdinPipe, _ := cmd.StdinPipe()
	_ = stdinPipe // kept open for Reload()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	b.mu.Lock()
	b.cmd = cmd
	b.startedAt = time.Now()
	b.mu.Unlock()

	// Signal subprocess exit. If the command dies before readyURL
	// responds, we want to abort the readiness loop immediately and
	// bubble up the tail of its output so the user sees a real error
	// instead of a 120 s "did not become ready" spinner.
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

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
			resp, err := http.Get(readyURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					b.mu.Lock()
					b.running = true
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
		idx := bytes.IndexByte(w.buf, '\n')
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
	out, err := exec.CommandContext(ctx, "xcrun", "xctrace", "list", "devices").Output()
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
	e.webMu.Unlock()
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
	e.webMu.Lock()
	if e.webCmd != nil && e.webCmd.Process != nil && e.webPort > 0 {
		port := e.webPort
		e.webMu.Unlock()
		return port, nil
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
	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = workDir
	// Isolate this Expo's cache so it doesn't fight Metro over .expo/
	// bundler state. Two concurrent `expo start` invocations on the
	// same project without separate cache dirs occasionally race on
	// watchman manifest writes; dedicated dirs eliminate the risk.
	cacheDir, _ := os.MkdirTemp("", "yaver-expo-web-*")
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

	if err := cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(cacheDir)
		return 0, fmt.Errorf("expo --web failed to start: %w", err)
	}

	e.webMu.Lock()
	e.webCmd = cmd
	e.webPort = port
	e.webCtx = ctx
	e.webMu.Unlock()

	// Reap the child and clean up state when it exits on its own.
	go func() {
		cmd.Wait()
		e.webMu.Lock()
		if e.webCmd == cmd {
			e.webCmd = nil
			e.webPort = 0
			e.webCtx = nil
		}
		e.webMu.Unlock()
		cancel()
		os.RemoveAll(cacheDir)
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

// WebPort returns the port the sibling Expo Web process is serving on,
// or 0 when no web preview is active. Used by the HTTP proxy to route
// /dev-web/* independently of Metro's Port().
func (e *ExpoDevServer) WebPort() int {
	e.webMu.Lock()
	defer e.webMu.Unlock()
	return e.webPort
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
func isPortInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

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
		// Fallback: use Metro bundler directly
		log.Printf("[dev] Expo CLI not available, falling back to Metro bundler")
		args = []string{"react-native", "start",
			"--port", fmt.Sprintf("%d", rn.port),
			"--host", "lan",
		}
		return rn.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
	}
	return nil
}

func (rn *ReactNativeDevServer) BundleURL(platform string) string {
	return "/dev/"
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

	// Find a real mobile device (iOS/Android) for native hot reload.
	// Flutter is a mobile framework — run natively, not as web.
	deviceID := opts.Platform
	preferredPlatform := ""
	switch deviceID {
	case "ios", "android":
		preferredPlatform = deviceID
		deviceID = ""
	}
	if deviceID == "" || deviceID == "web" || deviceID == "chrome" || deviceID == "web-server" {
		detected := detectFlutterMobileDevice(ctx, preferredPlatform, opts.Target)
		if detected != "" {
			deviceID = detected
		} else {
			// No mobile device found — fall back to web-server
			log.Printf("[dev:flutter] No mobile device found, falling back to web-server")
			deviceID = "web-server"
		}
	}

	args := []string{"run", "-d", deviceID}

	// Web-server needs port config; native devices don't
	if deviceID == "web-server" || deviceID == "chrome" {
		args = append(args, "--web-port", fmt.Sprintf("%d", f.port), "--web-hostname", "0.0.0.0")
	}

	log.Printf("[dev:flutter] Starting on device: %s", deviceID)

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
	out, err := exec.CommandContext(ctx, "flutter", "devices", "--machine").Output()
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

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

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

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	// augmentEnv prepends ~/.yaver/runtimes/node/bin to PATH so
	// `npx` / `node` invocations resolve to the agent-managed Node
	// runtime on a fresh Linux box that never had system Node.
	cmd.Env = append(augmentEnv(nil), env...)
	// Put the dev server in its own process group so Stop() can take down
	// all child processes (vite/next fork node + esbuild; killing only the
	// shell PID leaks them and the dev port stays bound until reboot).
	setProcGroup(cmd)

	logWriter := &devLogWriter{prefix: fmt.Sprintf("[dev:%s]", f.name)}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter

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

	// Wait for dev server to become ready
	deadline := time.After(180 * time.Second) // Flutter web first build can take 3+ min
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("%s did not become ready within 180s", name)
		case <-ticker.C:
			resp, err := http.Get(readyURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
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
	// Flutter hot reload via stdin "r"
	if f.stdinPipe != nil && f.stdinPipe.w != nil {
		_, err := f.stdinPipe.w.Write([]byte("r\n"))
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
	for _, name := range []string{"next.config.ts", "next.config.js", "next.config.mjs"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			return true
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
