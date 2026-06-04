package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yaver-io/agent/ghost"
)

var currentLocalAgentPort atomic.Int64

// HTTPServer serves the V1 HTTP API for mobile clients over Tailscale.
type HTTPServer struct {
	port           int
	token          string
	ownerUserID    string
	deviceID       string
	convexURL      string
	hostname       string
	taskMgr        *TaskManager
	execMgr        *ExecManager
	scheduler      *Scheduler
	companion      *CompanionEngine // companion-compute engine (yaver.companion.yaml)
	analytics      *Analytics
	aclMgr         *ACLManager
	emailMgr       *EmailManager
	notifyMgr      *NotificationManager
	vaultStore     *VaultStore
	buildMgr       *BuildManager
	tunnelMgr      *TunnelManager
	testMgr        *TestManager
	feedbackMgr    *FeedbackManager
	designRefMgr   *DesignReferenceManager
	blackboxMgr    *BlackBoxManager
	devServerMgr   *DevServerManager
	todolistMgr    *TodoListManager
	sessionAuditor *SessionAuditor
	guestConfigMgr *GuestConfigManager
	// Deploy history (in-memory ring buffer of recent /deploy/ship runs)
	// and per-caller concurrency limiter. Both are always live — lazy
	// allocation happens on first use via the ensureDeploy* helpers.
	deployHistory      *DeployHistory
	deployLimiter      *deployLimiter
	runnerStore        *RunnerStore         // unified Runner abstraction (RUNNER_DEV.md Phase 1)
	runnerLimiter      *runnerLimiter       // per-caller in-flight cap (mirror of deployLimiter)
	sandboxMgr         *SandboxManager      // long-lived Docker sandboxes (RUNNER_DEV.md Phase 2)
	agentSessionMgr    *AgentSessionManager // Devin-shape coding agent sessions (RUNNER_DEV.md Phase 2)
	containerRunner    *ContainerRunner     // nil if Docker not available
	containerizeGuests bool                 // run guest tasks in containers
	containerizeHost   bool                 // run host tasks in containers
	browserMgr         *BrowserManager      // nil until first browser_open
	pipelineRunner     *PipelineRunner      // nil until first pipeline_run
	analyticsMgr       *AnalyticsManager    // nil until first analytics_start
	authDevMgr         *AuthDevManager      // nil until first auth_dev_start
	mailDevMgr         *MailDevManager      // nil until first mail_dev_start
	exposeMgr          *ExposeManager       // nil until first expose_start
	relayExposeMgr     *RelayExposeManager  // relay-based subdomain expose (set when relay connected)
	stripeDevMgr       *StripeDevManager    // nil until first stripe_listen
	uptimeMonitor      *UptimeMonitor       // nil until first monitor_add
	modelMgr           *ModelManager        // nil until first models_*
	lemonMgr           *LemonSqueezyManager // nil until first lemonsqueezy_*
	servicesMgr        *ServicesManager     // nil until first services_*
	proxyMgr           *ProxyManager        // nil until first proxy_*
	dnsMgr             *DNSManager          // nil until first dns_*
	storageMgr         *StorageManager      // nil until first storage_*
	mockServer         *MockServer          // nil until first mock_*
	preCheckMgr        *PreCheckManager     // nil until first check_*
	perfMgr            *PerfManager         // nil until first perf_lighthouse
	dbLifecycleMgr     *DBLifecycleManager  // nil until first db_migrate
	previewMgr         *PreviewManager      // nil until first preview_*
	vibePreviewMgr     *VibePreviewManager  // nil until first /vibing/preview/start
	oauthWizardMgr     *OAuthWizardManager  // nil until first auth_oauth_*
	cloudDeployMgr     *CloudDeployManager  // nil until first cloud_*
	migrateMgr         *MigrateManager      // nil until first migrate_*
	remoteMgr          *RemoteManager       // nil until first remote_*
	scaleMgr           *ScaleManager        // nil until first scale_*
	pocketBaseMgr      *PocketBaseManager   // nil until first backend_*
	platformMgr        *PlatformManager     // nil until first platform_*
	domainMgr          *DomainManager       // nil until first domain_*
	siteMgr            *SiteManager         // nil until first site_*
	formMgr            *FormManager         // nil until first form_*
	seoMgr             *SEOManager          // nil until first seo_*
	cmsMgr             *CMSManager          // nil until first cms_*
	templateMgr        *TemplateManager     // nil until first template_*
	multiUserMgr       *MultiUserManager    // nil in single-user mode
	server             *http.Server
	tlsServer          *http.Server
	onShutdown         func() // called when mobile requests agent shutdown

	// lastNativeBundleProject{Path,Name} captures the most recent
	// successful /dev/build-native compile so a follow-up
	// /vibing/execute call from the loaded guest (which doesn't know
	// its own project name when only a `prompt` is sent — see
	// FeedbackOverlay.handleSend in 1.18.34) can fall back to "the
	// project we just pushed to your phone."
	// Without this, the resolver would fall through to taskMgr.workDir
	// (typically /root or the agent's cwd), and the fix would
	// run in the wrong directory.
	lastNativeBundleProjectPath string
	lastNativeBundleProjectName string
	lastNativeBundleMu          sync.Mutex

	iosInstallMethod string // "auto", "native", "bundle" — resolved at startup

	// GUI ghost (UI-automation slave). Opt-in via --ghost / config.GhostEnabled.
	// The engine is created lazily on first ghost verb so non-ghost agents pay
	// nothing. See ops_ghost.go.
	ghostEnabled bool
	ghostEngine  *ghost.Engine
	ghostOnce    sync.Once
	ghostErr     error

	// Test app sessions
	testAppSession       sync.Map // sessionID -> *TestAppSession
	activeTestAppSession sync.Map // "current" -> *TestAppSession

	// Cache validated tokens (token -> cachedTokenInfo) to avoid repeated Convex calls
	tokenCache sync.Map

	// IP allowlist — if non-empty, only these CIDRs can access the agent
	allowedCIDRs []*net.IPNet
	// Extra CIDRs that are only admitted when the request carries a
	// valid bearer token (owner, guest, or SDK). Used to open the
	// network-layer gate for authenticated traffic from relay /
	// Tailscale / Cloudflare while keeping anonymous traffic
	// restricted to the main allowedCIDRs set.
	allowedGuestCIDRs []*net.IPNet

	// Track seen IPs per token prefix for new-device notifications
	seenIPs sync.Map // "tokenPrefix_IP" -> true

	// Short-lived browser-scoped session tokens for websocket and iframe flows.
	browserSessions  sync.Map
	terminalSessions sync.Map

	// Guest access: cached list of approved guest userIds (refreshed every 60s)
	guestUserIDs   []string
	guestUserIDsMu sync.RWMutex

	// TLS config for HTTPS on LAN
	tlsPort        int
	tlsCert        tls.Certificate
	tlsFingerprint string

	// Auth status — set by heartbeat loop when token expires
	authExpired atomic.Bool
	// True while a manual update request is running. Prevents duplicate
	// self-update attempts from web/mobile clients.
	agentUpdateRunning atomic.Bool

	// Autopilot (auto-driving) mode
	autopilot *AutopilotManager

	// Quality gates (lint + typecheck + format)
	qualityMgr *QualityManager

	// Health monitor (production URL pinging)
	healthMon        *HealthMonitor
	agentGraphMgr    *AgentGraphManager
	publishMgr       *PublishManager
	remoteRuntimeMgr *RemoteRuntimeManager

	// Named log streams for fan-out of long-running CLI ops
	// to mobile + web subscribers.
	streams *LogStreamRegistry

	hostShareWorkspaceMgr *HostShareWorkspaceManager

	// Lets handlers that change reportable state (e.g. runner auth just
	// completed via /runner-auth/browser/start) cut in front of the 30 s
	// heartbeat ticker. Without this, a successful remote codex/claude
	// sign-in takes up to 30 s to reach Convex — and the web pill stays
	// "sign in" until the next tick, making it look like nothing happened.
	// Buffer of 1 so concurrent kicks coalesce without the sender blocking.
	heartbeatKick chan struct{}
}

// NewHTTPServer creates a new HTTP server bound to the given port.
func NewHTTPServer(port int, token, ownerUserID, deviceID, convexURL, hostname string, taskMgr *TaskManager) *HTTPServer {
	currentLocalAgentPort.Store(int64(port))
	var hostShareWorkspaceMgr *HostShareWorkspaceManager
	if mgr, err := NewHostShareWorkspaceManager(); err == nil {
		hostShareWorkspaceMgr = mgr
	}
	s := &HTTPServer{
		port:                  port,
		token:                 token,
		ownerUserID:           ownerUserID,
		deviceID:              deviceID,
		convexURL:             convexURL,
		hostname:              hostname,
		taskMgr:               taskMgr,
		streams:               NewLogStreamRegistry(),
		hostShareWorkspaceMgr: hostShareWorkspaceMgr,
		heartbeatKick:         make(chan struct{}, 1),
	}
	// Expose the agent-update progress stream to the package-level
	// emitAgentUpdate helper so checkAutoUpdate / runForcedAgentUpdate
	// (free functions, no HTTPServer reference) can publish to it.
	setAgentUpdateStream(s.streams.Get("agent-update"))
	return s
}

// TriggerHeartbeat nudges the heartbeat loop to send an extra beat now, out
// of band with the regular 30 s ticker. Non-blocking: if a kick is already
// pending, this call is a no-op. Safe to call from any goroutine.
func (s *HTTPServer) TriggerHeartbeat() {
	if s == nil || s.heartbeatKick == nil {
		return
	}
	select {
	case s.heartbeatKick <- struct{}{}:
	default:
	}
}

// Start starts the HTTP server and blocks until the context is cancelled.
func (s *HTTPServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Wire this server into the dev-bundle bearer fallback so unauth
	// /dev/native-bundle / /dev/web-bundle requests carrying a known
	// SDK token can be admitted (matches authSDK's behaviour).
	SetDevBundleServerRef(s)

	// Public
	mux.HandleFunc("/health", s.handleHealth)

	// Authenticated
	mux.HandleFunc("/tasks", s.auth(s.handleTasks))
	mux.HandleFunc("/tasks/", s.auth(s.handleTaskByID))
	mux.HandleFunc("/chain", s.auth(s.handleChainCreate))
	mux.HandleFunc("/chain/", s.auth(s.handleChainStatus))
	mux.HandleFunc("/deploy", s.auth(s.handleDeploy))
	mux.HandleFunc("/summary", s.auth(s.handleSummary))
	mux.HandleFunc("/info", s.auth(s.handleInfo))
	mux.HandleFunc("/hardware/refresh", s.auth(s.handleHardwareRefresh))
	mux.HandleFunc("/self-check", s.auth(s.handleSelfCheck))
	mux.HandleFunc("/bus/status", s.auth(s.handleBusStatus))
	mux.HandleFunc("/bus/retained", s.auth(s.handleBusRetained))
	mux.HandleFunc("/bus/events", s.auth(s.handleBusEvents))
	mux.HandleFunc("/bus/publish", s.auth(s.handleBusPublish))
	mux.HandleFunc("/agent/status", s.auth(s.handleAgentStatus))
	mux.HandleFunc("/agent/capabilities", s.auth(s.handleAgentCapabilities))
	// Multi-source yaver-binary reconcile (apt/brew/npm/manual/auto-update).
	// Owner-only; never exposed to guests or SDK tokens. See self_heal.go.
	mux.HandleFunc("/agent/self-heal", s.auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handleSelfHealReport(w, r)
		} else {
			handleSelfHealApply(w, r)
		}
	}))
	mux.HandleFunc("/agent/graphs", s.auth(s.handleAgentGraphs))
	mux.HandleFunc("/agent/graphs/", s.auth(s.handleAgentGraphByID))
	mux.HandleFunc("/agent/runners", s.authSDKOrGuest(s.handleRunners))
	mux.HandleFunc("/agent/runners/test", s.auth(s.handleRunnerTest))
	mux.HandleFunc("/runner-auth/status", s.auth(s.handleRunnerAuthStatus))
	mux.HandleFunc("/runner-auth/set", s.auth(s.handleRunnerAuthSet))
	// Same-Convex-user SSH bootstrap: append the caller's pubkey to
	// ~/.ssh/authorized_keys so `yaver ssh primary` from a freshly
	// signed-in box works without ssh-copy-id. See auth_ssh_http.go.
	mux.HandleFunc("/auth/ssh/authorized-keys", s.auth(s.handleSSHAuthorizedKeys))
	mux.HandleFunc("/runner-auth/setup", s.authSDK(s.handleRunnerAuthSetup))
	mux.HandleFunc("/runner/opencode/config", s.auth(s.handleOpenCodeConfig))
	// Browser/device-auth sub-family is also reachable by SDK tokens that
	// carry the "runner-auth" scope — lets the embedded Feedback SDK on
	// carrotbytes.xyz / an RN host trigger `codex login --device-auth`
	// (verification URL + one-time code) without forcing the end-user to
	// also log in to yaver.io. The api-key setter stays owner-only via
	// the other /runner-auth/* endpoints registered near the top.
	mux.HandleFunc("/runner-auth/browser/start", s.authSDK(s.handleRunnerBrowserAuthStart))
	mux.HandleFunc("/runner-auth/browser/status", s.authSDK(s.handleRunnerBrowserAuthStatus))
	mux.HandleFunc("/runner-auth/browser/cancel", s.authSDK(s.handleRunnerBrowserAuthCancel))
	mux.HandleFunc("/runner-auth/browser/submit-code", s.authSDK(s.handleRunnerBrowserAuthSubmitCode))
	// Subscription-token transfer between user-owned devices. See the
	// handler — yaver is a single-user wrapper, so the user's existing
	// local Claude / Codex token gets *copied* to a remote box rather
	// than re-OAuthing per box. Avoids the SSH-launched-daemon Keychain
	// quagmire entirely.
	mux.HandleFunc("/runner-auth/credentials/import", s.authSDK(s.handleRunnerAuthCredentialsImport))
	mux.HandleFunc("/runner-provider/preflight", s.authSDK(s.handleRunnerProviderPreflight))
	mux.HandleFunc("/company-ai/resolve-local", s.authSDK(s.handleCompanyAIResolveLocal))
	mux.HandleFunc("/machine/onboarding/status", s.auth(s.handleMachineOnboardingStatus))
	mux.HandleFunc("/machine/onboarding/apply", s.auth(s.handleMachineOnboardingApply))
	mux.HandleFunc("/machine/onboarding/remove", s.auth(s.handleMachineOnboardingRemove))
	mux.HandleFunc("/agent/env-profile", s.auth(s.handleEnvironmentProfile))
	mux.HandleFunc("/agent/env-profile/apply", s.auth(s.handleEnvironmentProfileApply))
	mux.HandleFunc("/agent/toolchain-sync/profile", s.auth(s.handleEnvironmentProfile))
	mux.HandleFunc("/agent/toolchain-sync/apply", s.auth(s.handleEnvironmentProfileApply))
	mux.HandleFunc("/agent/toolchain-sync/git-credentials", s.auth(s.handleToolchainGitCredentials))
	mux.HandleFunc("/agent/dev-configs/bundle", s.auth(s.handleDevConfigBundle))
	mux.HandleFunc("/agent/dev-configs/apply", s.auth(s.handleDevConfigApply))
	mux.HandleFunc("/dev-environments/clone/plan", s.auth(s.handleDevEnvironmentClonePlan))
	mux.HandleFunc("/dev-environments/clone/start", s.auth(s.handleDevEnvironmentCloneStart))
	mux.HandleFunc("/dev-environments/clone/status", s.auth(s.handleDevEnvironmentCloneStatus))
	mux.HandleFunc("/code/config", s.auth(s.handleCodeConfig))
	mux.HandleFunc("/code/status", s.auth(s.handleCodeStatus))
	mux.HandleFunc("/code/attach", s.auth(s.handleCodeAttach))
	mux.HandleFunc("/code/detach", s.auth(s.handleCodeDetach))
	mux.HandleFunc("/code/repos", s.auth(s.handleCodeRepos))
	mux.HandleFunc("/code/repo", s.auth(s.handleCodeRepo))
	mux.HandleFunc("/project/kind", s.auth(s.handleProjectKind))
	mux.HandleFunc("/code/dev", s.auth(s.handleCodeDev))
	mux.HandleFunc("/code/deploy", s.auth(s.handleCodeDeploy))

	mux.HandleFunc("/agent/runner/restart", s.auth(s.handleRunnerRestart))
	mux.HandleFunc("/agent/runner/switch", s.authSDKOrGuest(s.handleRunnerSwitch))
	mux.HandleFunc("/agent/update", s.auth(s.handleAgentUpdate))
	mux.HandleFunc("/agent/shutdown", s.auth(s.handleShutdown))
	mux.HandleFunc("/machine/remove", s.auth(s.handleMachineRemove))
	mux.HandleFunc("/infra/summary", s.auth(s.handleInfraSummary))
	mux.HandleFunc("/infra/services/action", s.auth(s.handleInfraServiceAction))
	mux.HandleFunc("/infra/power", s.auth(s.handleInfraPower))
	mux.HandleFunc("/agent/clean", s.auth(s.handleClean))
	mux.HandleFunc("/agent/doctor", s.auth(s.handleDoctor))
	mux.HandleFunc("/agent/tools", s.auth(s.handleTools))
	mux.HandleFunc("/schedules", s.auth(s.handleSchedules))
	mux.HandleFunc("/schedules/", s.auth(s.handleScheduleByID))
	mux.HandleFunc("/streams", s.auth(s.handleStreams))
	mux.HandleFunc("/streams/", s.auth(s.handleStreamByName))
	mux.HandleFunc("/autoideas/start", s.auth(s.handleAutoIdeasStart))
	mux.HandleFunc("/autoideas/file", s.auth(s.handleAutoIdeasFile))
	mux.HandleFunc("/autoinit/start", s.auth(s.handleAutoInitStart))
	mux.HandleFunc("/autoinit/status", s.auth(s.handleAutoInitStatus))
	mux.HandleFunc("/releases/list", s.auth(s.handleReleaseList))
	mux.HandleFunc("/releases/latest", s.auth(s.handleReleaseLatest))
	mux.HandleFunc("/releases/bundle", s.auth(s.handleReleaseBundle))
	mux.HandleFunc("/incidents", s.auth(s.handleIncidents))
	mux.HandleFunc("/incidents/stream", s.auth(s.handleIncidentsStream))
	mux.HandleFunc("/incidents/summary", s.auth(s.handleIncidentsSummary))
	mux.HandleFunc("/incidents/", s.auth(s.handleIncidentByID))
	mux.HandleFunc("/operations", s.auth(s.handleOperations))
	mux.HandleFunc("/operations/stream", s.auth(s.handleOperationsStream))
	mux.HandleFunc("/capabilities/snapshot", s.auth(s.handleCapabilitiesSnapshot))
	mux.HandleFunc("/errors", s.auth(s.handleErrors))
	mux.HandleFunc("/errors/stats", s.auth(s.handleErrorsStats))
	mux.HandleFunc("/errors/detail", s.auth(s.handleErrorsDetail))
	mux.HandleFunc("/errors/resolve", s.auth(s.handleErrorsResolve))
	mux.HandleFunc("/errors/reopen", s.auth(s.handleErrorsReopen))
	mux.HandleFunc("/monitors", s.auth(s.handleMonitors))
	mux.HandleFunc("/monitors/", s.auth(s.handleMonitorAction))
	mux.HandleFunc("/analytics/events", s.auth(s.handleAnalyticsEvents))
	mux.HandleFunc("/analytics/events.csv", s.auth(s.handleAnalyticsCSV))
	mux.HandleFunc("/analytics/ingest", s.authSDK(s.handleAnalyticsIngest))
	mux.HandleFunc("/flags", s.auth(s.handleFlags))
	mux.HandleFunc("/flags/eval", s.authSDK(s.handleFlagsEval))
	mux.HandleFunc("/flags/override", s.auth(s.handleFlagOverride))
	mux.HandleFunc("/flags/delete", s.auth(s.handleFlagDelete))
	mux.HandleFunc("/logs/search", s.auth(s.handleLogsSearch))
	mux.HandleFunc("/sourcemaps", s.auth(s.handleSourceMaps))
	mux.HandleFunc("/env", s.auth(s.handleEnvList))
	mux.HandleFunc("/env/get", s.authSDK(s.handleEnvGet))
	mux.HandleFunc("/sync/", s.auth(s.handleSync))
	mux.HandleFunc("/statuspage", s.auth(s.handleStatusPage))
	mux.HandleFunc("/email/send", s.auth(s.handleEmailSend))
	mux.HandleFunc("/email/sent", s.auth(s.handleEmailSent))
	mux.HandleFunc("/blobs", s.auth(s.handleBlobs))
	mux.HandleFunc("/blobs/", s.auth(s.handleBlobs))
	mux.HandleFunc("/blobs/public", s.handleBlobPublic)
	mux.HandleFunc("/changelog", s.auth(s.handleChangelog))
	mux.HandleFunc("/changelog.html", s.handleChangelogHTML)
	mux.HandleFunc("/changelog.atom", s.handleChangelogAtom)
	// /apikeys is wrapped in the token-bucket limiter because POST
	// creates a new SDK token (network round-trip to Convex) and
	// DELETE sweeps the token cache. Both are expensive and creation
	// could be brute-forced by a compromised owner token to flood
	// the Convex sdkTokens table.
	mux.HandleFunc("/apikeys", s.rateLimit(s.auth(s.handleAPIKeys)))
	mux.HandleFunc("/apm", s.auth(s.handleAPM))
	mux.HandleFunc("/pubsub/publish", s.authSDK(s.handlePubSubPublish))
	mux.HandleFunc("/pubsub/subscribe", s.authSDK(s.handlePubSubSubscribe))
	mux.HandleFunc("/pubsub/topics", s.auth(s.handlePubSubTopics))
	mux.HandleFunc("/search", s.auth(s.handleSearch))
	mux.HandleFunc("/search/", s.auth(s.handleSearch))
	mux.HandleFunc("/feedback-board", s.auth(s.handleFeedbackBoard))
	mux.HandleFunc("/feedback-board/", s.auth(s.handleFeedbackBoard))
	mux.HandleFunc("/feedback-board/public", s.authSDK(s.handleFeedbackBoardPublic))
	// Auth pair endpoints are intentionally UNAUTHENTICATED —
	// the pairing code (10-min window, single use) is the secret.
	// They still go through the generic rate limiter so brute-
	// force attempts on the 6-char code are throttled hard.
	mux.HandleFunc("/auth/pair/info", s.rateLimit(s.handlePairInfo))
	mux.HandleFunc("/auth/pair/session", s.rateLimit(s.handlePairSession))
	mux.HandleFunc("/auth/pair/submit", s.rateLimit(s.handlePairSubmit))
	mux.HandleFunc("/auth/pair/encrypted", s.rateLimit(s.handlePairEncrypted))
	// Remote-support sessions (TeamViewer-style, in-memory, TTL'd).
	// Owner-only control plane:
	// Grand MCP: unified verb-based ops API. See ops.go.
	// /ops           — POST {machine, verb, payload} -> {ok, streamId?, initial?, error?, code?}
	// /ops/plan      — POST {machine, verb, payload} -> execution plan without side effects
	// /ops/verbs     — GET list of registered verbs + their payload schemas
	mux.HandleFunc("/ops", s.auth(s.handleOps))
	mux.HandleFunc("/ops/plan", s.auth(s.handleOpsPlan))
	// Remote-view (RustDesk/AnyDesk/VNC) management — first-class in the agent.
	mux.HandleFunc("/remoteview/providers", s.auth(s.handleRemoteViewProviders))
	mux.HandleFunc("/remoteview/status", s.auth(s.handleRemoteViewStatus))
	mux.HandleFunc("/remoteview/connect", s.auth(s.handleRemoteViewConnect))
	mux.HandleFunc("/remoteview/disconnect", s.auth(s.handleRemoteViewDisconnect))
	// Live ghost screen stream (Bambu-camera style) — proxied by the Talos web UI.
	mux.HandleFunc("/ghost/stream", s.auth(s.handleGhostStream))
	mux.HandleFunc("/ghost/frame.jpg", s.auth(s.handleGhostFrame))
	mux.HandleFunc("/ops/verbs", s.auth(s.handleOpsVerbs))
	mux.HandleFunc("/support/start", s.auth(s.handleSupportStart))
	mux.HandleFunc("/support/stop", s.auth(s.handleSupportStop))
	mux.HandleFunc("/support/status", s.auth(s.handleSupportStatus))
	// Unauth probe + redeem — code is the secret, same model as
	// /auth/pair/submit. Rate-limited to throttle brute force.
	mux.HandleFunc("/support/info", s.rateLimit(s.handleSupportInfo))
	mux.HandleFunc("/support/redeem", s.rateLimit(s.handleSupportRedeem))
	mux.HandleFunc("/auth/browser-session", s.auth(s.handleBrowserSession))
	mux.HandleFunc("/machine/health", s.auth(s.handleMachineHealth))
	mux.HandleFunc("/machine/peers", s.auth(s.handlePeerHealth))
	mux.HandleFunc("/machine/peers/recover", s.auth(s.handlePeerRecover))
	mux.HandleFunc("/tunnel/forward/", s.auth(s.handleTunnelForward))
	mux.HandleFunc("/machine/tailscale", s.auth(s.handleTailscaleStatus))
	// Unauthenticated recovery endpoint — bootstrap-secret gated,
	// rate limited. Intentionally NOT wrapped in auth() because
	// the whole point is to bring a locked-out agent back online.
	mux.HandleFunc("/auth/recover", s.handleAuthRecover)
	mux.HandleFunc("/auth/recover/session", s.handleAuthRecoverSession)
	// /auth/reload-from-disk: loopback-only nudge that lets an
	// out-of-process `yaver auth` (or anything else that writes a
	// fresh token to ~/.yaver/config.json) tell the running daemon
	// to re-read disk + validate + clear authExpired immediately
	// instead of waiting up to 5 min for the next heartbeat tick.
	// The peer-IP loopback gate lives inside the handler.
	mux.HandleFunc("/auth/reload-from-disk", s.handleAuthReloadFromDisk)
	// /auth/factory-reset verifies caller's identity via Convex
	// round-trip inside the handler (auth_factory_reset_http.go),
	// NOT via the regular auth() middleware — the bug it fixes is
	// exactly "agent has someone else's auth_token", which makes
	// auth() reject the legitimate-owner bearer with 403. Convex
	// is the trust anchor for who-owns-this-device. Rate-limited
	// so a malicious bearer can't reset-flood the agent.
	mux.HandleFunc("/auth/factory-reset", s.rateLimit(s.handleAuthFactoryReset))
	// Cheap, unauth'd "am I signed in?" probe — used by `yaver status`,
	// the mobile app's connection list, and health dashboards. Returns
	// the authExpired flag set by the heartbeat loop after a failed
	// refresh, so callers can tell "just offline" apart from "needs
	// re-auth." Public because it leaks nothing secret and the
	// bootstrap-beacon already publishes needs-auth state in the clear.
	mux.HandleFunc("/auth/status", s.handleAuthStatus)
	// File browser (read-only, scoped to discovered projects)
	mux.HandleFunc("/files/roots", s.auth(s.handleFilesRoots))
	mux.HandleFunc("/files/list", s.auth(s.handleFilesList))
	mux.HandleFunc("/files/read", s.auth(s.handleFilesRead))
	mux.HandleFunc("/files/raw", s.auth(s.handleFilesRaw))
	mux.HandleFunc("/host-share/fs/write", s.auth(s.handleHostShareFSWrite))
	mux.HandleFunc("/host-share/fs/mkdir", s.auth(s.handleHostShareFSMkdir))
	mux.HandleFunc("/host-share/fs/delete", s.auth(s.handleHostShareFSDelete))
	mux.HandleFunc("/shared-storage/profiles", s.auth(s.handleSharedStorageProfiles))
	mux.HandleFunc("/shared-storage/profile/delete", s.auth(s.handleSharedStorageDelete))
	mux.HandleFunc("/shared-storage/list", s.auth(s.handleSharedStorageList))
	mux.HandleFunc("/shared-storage/read", s.auth(s.handleSharedStorageRead))
	mux.HandleFunc("/shared-storage/raw", s.auth(s.handleSharedStorageRaw))
	mux.HandleFunc("/shared-storage/search", s.auth(s.handleSharedStorageSearch))
	// Project wizard (fullstack generator) — drives the same
	// state machine as `yaver new` over HTTP so the mobile app,
	// the web dashboard and MCP clients all share it.
	mux.HandleFunc("/project/wizard/start", s.auth(s.handleWizardStart))
	mux.HandleFunc("/project/wizard/answer", s.auth(s.handleWizardAnswer))
	mux.HandleFunc("/project/wizard/generate", s.auth(s.handleWizardGenerate))
	mux.HandleFunc("/project/wizard/session", s.auth(s.handleWizardSession))
	mux.HandleFunc("/project/wizard/questions", s.auth(s.handleWizardQuestions))
	mux.HandleFunc("/imports/conversation/plan", s.auth(s.handleConversationImportPlan))

	// Forms — public submit endpoint, owner-managed CRUD
	mux.HandleFunc("/forms", s.auth(s.handleForms))
	mux.HandleFunc("/forms/", s.handleFormsRouter)

	// Newsletter — public subscribe/confirm/unsub, owner broadcast
	mux.HandleFunc("/newsletter/subscribe", s.handleNewsletterSubscribe)
	mux.HandleFunc("/newsletter/confirm", s.handleNewsletterConfirm)
	mux.HandleFunc("/newsletter/unsubscribe", s.handleNewsletterUnsubscribe)
	mux.HandleFunc("/newsletter/subscribers", s.auth(s.handleNewsletterSubscribers))
	mux.HandleFunc("/newsletter/campaigns", s.auth(s.handleNewsletterCampaigns))
	mux.HandleFunc("/newsletter/campaigns/", s.auth(s.handleNewsletterSend))
	mux.HandleFunc("/newsletter/compose", s.auth(s.handleNewsletterCompose))

	// Job queue — persistent background jobs with retries/DLQ
	mux.HandleFunc("/jobs", s.auth(s.handleJobs))
	mux.HandleFunc("/jobs/enqueue", s.auth(s.handleJobsEnqueue))
	mux.HandleFunc("/jobs/", s.auth(s.handleJobAction))

	// Image optimizer — on-demand resize + reencode + disk cache
	mux.HandleFunc("/img", s.auth(s.handleImgOptimize))

	// PDF generation — HTML or URL → PDF via embedded Chromium
	mux.HandleFunc("/pdf/render", s.auth(s.handlePDFRender))

	// Self-hosted OAuth provider — discovery, authorize, token, jwks
	// All unauthenticated because OAuth is its own auth system.
	mux.HandleFunc("/oauth/.well-known/openid-configuration", s.handleOauthDiscovery)
	mux.HandleFunc("/oauth/authorize", s.handleOauthAuthorize)
	mux.HandleFunc("/oauth/login", s.handleOauthLogin)
	mux.HandleFunc("/oauth/token", s.handleOauthToken)
	mux.HandleFunc("/oauth/userinfo", s.handleOauthUserinfo)
	mux.HandleFunc("/oauth/jwks", s.handleOauthJWKS)
	// CRUD for registered clients + users — owner-only
	mux.HandleFunc("/oauth/clients", s.auth(s.handleOauthClients))
	mux.HandleFunc("/oauth/users", s.auth(s.handleOauthUsers))

	// Mail — Gmail / O365 inbox fetch + AI draft + SMTP send
	mux.HandleFunc("/mail/inbox", s.auth(s.handleMailInbox))
	mux.HandleFunc("/mail/draft", s.auth(s.handleMailDraft))
	mux.HandleFunc("/mail/send", s.auth(s.handleMailSend))
	// OAuth onboarding — callback is unauthenticated (the OAuth
	// provider redirects into it), everything else is owner-only.
	mux.HandleFunc("/mail/onboard/start", s.auth(s.handleMailOnboardStart))
	mux.HandleFunc("/mail/onboard/status", s.auth(s.handleMailOnboardStatus))
	mux.HandleFunc("/mail/onboard/callback", s.handleMailOnboardCallback)
	mux.HandleFunc("/mail/config", s.auth(s.handleMailConfig))

	// URL shortener — public /s/:code redirect, owner CRUD on /shortener
	mux.HandleFunc("/s/", s.handleShortRedirect)
	mux.HandleFunc("/shortener", s.auth(s.handleShortener))
	mux.HandleFunc("/shortener/clicks", s.auth(s.handleShortClicks))

	// Waitlist with referral leaderboard — join + leaderboard public
	mux.HandleFunc("/waitlist/join", s.handleWaitlistJoin)
	mux.HandleFunc("/waitlist/leaderboard", s.handleWaitlistLeaderboard)
	mux.HandleFunc("/waitlist", s.auth(s.handleWaitlist))

	// Docs site — public /docs/*, owner config on /docs/config
	mux.HandleFunc("/docs", s.handleDocsSite)
	mux.HandleFunc("/docs/", s.handleDocsSite)

	// Meetings / Calendly-lite — public /meet/:slug, owner /meetings
	mux.HandleFunc("/meetings", s.auth(s.handleMeetings))
	mux.HandleFunc("/meet/", s.handleMeetPage)
	mux.HandleFunc("/bookings", s.auth(s.handleBookings))

	// A/B experiments on top of flags
	mux.HandleFunc("/ab/experiments", s.auth(s.handleABExperiments))
	mux.HandleFunc("/ab/assign", s.handleABAssign)
	mux.HandleFunc("/ab/events", s.handleABEvents)
	mux.HandleFunc("/ab/results", s.auth(s.handleABResults))

	// Clips — screen recording + sharing (share links are public).
	// Replaces Loom / Tella / Vidyard for the solo dev.
	mux.HandleFunc("/clips/start", s.auth(s.handleClipStart))
	mux.HandleFunc("/clips/stop", s.auth(s.handleClipStop))
	mux.HandleFunc("/clips/list", s.auth(s.handleClipList))
	mux.HandleFunc("/clips/upload/", s.auth(s.handleClipUpload))
	mux.HandleFunc("/clips/merge/", s.auth(s.handleClipMerge))
	mux.HandleFunc("/clips/private/", s.auth(s.handleClipPrivateDetail))
	mux.HandleFunc("/clips/", s.handleClipDetail)

	// Affiliate tracking (extends the shortener with commissions)
	mux.HandleFunc("/affiliates", s.auth(s.handleAffiliates))
	mux.HandleFunc("/affiliates/", s.auth(s.handleAffiliateSub))

	// Invoices + Stripe / LemonSqueezy integration
	mux.HandleFunc("/customers", s.auth(s.handleCustomers))
	mux.HandleFunc("/invoices", s.auth(s.handleInvoices))
	mux.HandleFunc("/invoices/", s.auth(s.handleInvoiceSub))
	// Webhook endpoints — public (signature verification pending)
	mux.HandleFunc("/webhooks/stripe", s.handleStripeWebhook)
	mux.HandleFunc("/webhooks/lemonsqueezy", s.handleLemonWebhook)

	// Asciinema-lite terminal recording
	mux.HandleFunc("/asciinema", s.auth(s.handleAsciinemaList))
	mux.HandleFunc("/asciinema/import", s.auth(s.handleAsciinemaImport))
	mux.HandleFunc("/asciinema/start", s.auth(s.handleAsciinemaStart))
	mux.HandleFunc("/asciinema/stop", s.auth(s.handleAsciinemaStop))
	mux.HandleFunc("/asciinema/", s.handleAsciinemaDetail)

	// Live chat widget — public visitor side, owner side gated
	mux.HandleFunc("/chat/messages", s.handleChatMessageIngest)
	mux.HandleFunc("/chat/stream", s.handleChatStream)
	mux.HandleFunc("/chat/conversations", s.auth(s.handleChatConversations))
	mux.HandleFunc("/chat/reply", s.auth(s.handleChatReply))
	mux.HandleFunc("/chat/widget.js", s.handleChatWidgetJS)

	// Copilot-lite — local Ollama autocomplete (replaces Copilot/Cursor)
	mux.HandleFunc("/copilot/complete", s.auth(s.handleCopilotComplete))
	mux.HandleFunc("/copilot/models", s.auth(s.handleCopilotModels))

	// Analytics UI (PostHog / Plausible-lite)
	mux.HandleFunc("/analytics/views", s.handleAnalyticsViewsJS)
	mux.HandleFunc("/analytics/top", s.auth(s.handleAnalyticsTop))
	mux.HandleFunc("/analytics/funnel", s.auth(s.handleAnalyticsFunnel))
	mux.HandleFunc("/analytics/retention", s.auth(s.handleAnalyticsRetention))
	mux.HandleFunc("/analytics/summary", s.auth(s.handleAnalyticsSummary))

	// Mail classifier learning loop (mark-as-bulk / mark-as-personal)
	mux.HandleFunc("/mail/mark", s.auth(s.handleMailMark))
	mux.HandleFunc("/mail/learning", s.auth(s.handleMailLearning))

	mux.HandleFunc("/analytics", s.auth(s.handleAnalytics))
	mux.HandleFunc("/session/list", s.auth(s.handleSessionList))
	mux.HandleFunc("/session/export", s.auth(s.handleSessionExport))
	mux.HandleFunc("/session/import", s.auth(s.handleSessionImport))
	mux.HandleFunc("/tmux/sessions", s.auth(s.handleTmuxSessions))
	mux.HandleFunc("/tmux/adopt", s.auth(s.handleTmuxAdopt))
	mux.HandleFunc("/tmux/detach", s.auth(s.handleTmuxDetach))
	mux.HandleFunc("/tmux/input", s.auth(s.handleTmuxInput))

	// Notifications
	mux.HandleFunc("/notifications/config", s.auth(s.handleNotificationsConfig))
	mux.HandleFunc("/notifications/test", s.auth(s.handleNotificationsTest))

	// Voice — hands-free agent loop (revived 2026-05-27, see
	// project_voice_glasses_revival_2026_05_27.md). /voice/stream is a
	// WebSocket; authSDK gates it so paired mobile + Feedback-SDK
	// clients can drive voice without round-tripping the auth broker.
	mux.HandleFunc("/voice/status", s.authSDK(s.handleVoiceStatus))
	// Browser WebSocket clients (the web feedback SDK) can't set request
	// headers, so wsQueryToken promotes ?access_token=<bearer> into the
	// Authorization header before authSDK validates it. RN/CLI clients
	// keep sending the header and are unaffected.
	mux.HandleFunc("/voice/stream", s.wsQueryToken(s.authSDK(s.handleVoiceStream)))
	// /voice/config — POST to set provider + API keys from mobile
	// Settings. Owner-auth gated (NOT authSDK) because the body
	// carries plaintext API keys.
	mux.HandleFunc("/voice/config", s.auth(s.handleVoiceConfigSet))

	// Hermes runtime — bundle validation + headless execution. Gated by
	// owner auth (NOT authSDK) because exec is privileged. Used by the
	// voice-launch verb to smoke-test bundles before fan-out.
	mux.HandleFunc("/hermes/validate", s.auth(s.handleHermesValidate))
	mux.HandleFunc("/hermes/run", s.auth(s.handleHermesRun))
	mux.HandleFunc("/hermes/smoke", s.auth(s.handleHermesSmoke))

	// Runner-auth mirror — token-mirror for glass OAuth flow. See
	// project_glass_oauth_mirror_2026_05_27 memory. All routes are
	// owner-auth gated (NOT authSDK) because plaintext credentials
	// travel through mirror/accept; SDK tokens cannot push runner auth.
	mux.HandleFunc("/runner/auth/mirror/request", s.auth(s.handleRunnerAuthMirrorRequest))
	mux.HandleFunc("/runner/auth/mirror/accept", s.auth(s.handleRunnerAuthMirrorAccept))
	mux.HandleFunc("/runner/auth/ledger", s.auth(s.handleRunnerAuthLedger))
	mux.HandleFunc("/runner/auth/ledger/revoke", s.auth(s.handleRunnerAuthLedgerRevoke))

	// Webhooks (public — uses webhook secret instead of auth)
	mux.HandleFunc("/webhooks/trigger", s.handleWebhookTrigger)

	// Exec (remote command execution)
	mux.HandleFunc("/exec", s.auth(s.handleExec))
	mux.HandleFunc("/exec/", s.auth(s.handleExecByID))

	// Tunnels (TCP port tunneling for hot reload)
	mux.HandleFunc("/tunnels", s.auth(s.handleTunnels))
	mux.HandleFunc("/tunnels/", s.auth(s.handleTunnelByID))

	// Tests (automated test sessions — legacy "spawn an external runner" path)
	mux.HandleFunc("/tests", s.auth(s.handleTests))
	mux.HandleFunc("/tests/", s.auth(s.handleTestByID))

	// yaver-test-sdk: embedded local-CI runner (Chromium via CDP, no
	// external Playwright/Selenium needed). Mobile app uses these to
	// list specs, kick off runs, and read history over the existing P2P
	// transport. Nothing here ever talks to Convex.
	mux.HandleFunc("/testkit/specs", s.auth(s.handleTestkitListSpecs))
	mux.HandleFunc("/testkit/run", s.auth(s.handleTestkitRun))
	mux.HandleFunc("/testkit/history", s.auth(s.handleTestkitHistory))
	mux.HandleFunc("/testkit/flake", s.auth(s.handleTestkitFlake))
	mux.HandleFunc("/testkit/notifications", s.auth(s.handleTestkitNotifications))
	mux.HandleFunc("/testkit/markers", s.auth(s.handleTestkitMarkers))
	mux.HandleFunc("/testkit/artifact", s.auth(s.handleTestkitArtifact))
	mux.HandleFunc("/testkit/frames", s.auth(s.handleTestkitFrames))
	mux.HandleFunc("/testkit/devices", s.auth(s.handleTestkitDevices))
	mux.HandleFunc("/testkit/integrations", s.auth(s.handleTestkitIntegrations))
	mux.HandleFunc("/testkit/autofix", s.auth(s.handleTestkitAutoFix))
	mux.HandleFunc("/testkit/autofix/", s.auth(s.handleTestkitAutoFixAction))

	// Auto Test: autonomous phase-1 web flow driver. Thin wrappers over
	// the ops verb so mobile, web, and CLI share one local-only path.
	mux.HandleFunc("/autotest/start", s.auth(s.handleAutotestStart))
	mux.HandleFunc("/autotest/status", s.auth(s.handleAutotestStatus))
	mux.HandleFunc("/autotest/stop", s.auth(s.handleAutotestStop))
	mux.HandleFunc("/autotest/results", s.auth(s.handleAutotestResults))
	mux.HandleFunc("/autotest/results/", s.auth(s.handleAutotestResults))
	mux.HandleFunc("/autotest/approve", s.auth(s.handleAutotestApprove))
	mux.HandleFunc("/autotest/suite", s.auth(s.handleAutotestSuite))

	// Feedback (visual bug reports from device testing) — SDK-accessible
	mux.HandleFunc("/feedback", s.authSDK(s.handleFeedback))
	mux.HandleFunc("/feedback/stream", s.authSDK(s.handleFeedbackStream))
	mux.HandleFunc("/feedback/", s.authSDK(s.handleFeedbackByID))
	// On-device App Store screenshot upload (Engine 2) — SDK-accessible.
	mux.HandleFunc("/shots/upload", s.authSDK(s.handleShotsUpload))

	// Design references (web UI captures from the browser extension) —
	// distinct store, same auth tier as feedback so SDK tokens can list
	// references when feeding them as context to runners.
	mux.HandleFunc("/design-references", s.authSDK(s.handleDesignReferences))
	mux.HandleFunc("/design-references/", s.authSDK(s.handleDesignReferenceByID))

	// Test app (autonomous testing from Feedback SDK) — SDK-accessible
	mux.HandleFunc("/test-app/start", s.authSDK(s.handleTestAppStart))
	mux.HandleFunc("/test-app/stop", s.authSDK(s.handleTestAppStop))
	mux.HandleFunc("/test-app/status", s.authSDK(s.handleTestAppStatus))

	// Black box (flight-recorder streaming from device SDKs) — SDK-accessible
	mux.HandleFunc("/blackbox/stream", s.authSDK(s.handleBlackBoxStream))
	mux.HandleFunc("/blackbox/command-stream", s.authSDK(s.handleBlackBoxCommandStream))
	mux.HandleFunc("/blackbox/events", s.authSDK(s.handleBlackBoxEvents))
	mux.HandleFunc("/blackbox/logs", s.authSDK(s.handleBlackBoxLogs))
	mux.HandleFunc("/blackbox/subscribe", s.authSDK(s.handleBlackBoxSubscribe))
	mux.HandleFunc("/blackbox/context", s.authSDK(s.handleBlackBoxContext))

	// Glass HUD push surface — POST /glass/hud emits typed views on
	// the existing /blackbox/command-stream so MentraOS clients can
	// render terminal_tail, email_subjects, notification, voice_overlay
	// without polling. Owner-only — same posture as the rest of the
	// agent's broadcast channels.
	mux.HandleFunc("/glass/hud", s.auth(s.handleGlassHUDPush))

	// IMAP summary surface — HUD-sized inbox snapshot. Read-only; the
	// HUD wall renders 4×{from, subject} lines and an "as of" stamp.
	// Compose goes through the spatial /glass_pc browser quad, not
	// this endpoint.
	mux.HandleFunc("/imap/inbox", s.auth(s.handleIMAPInbox))

	// Mobile-app session enumeration + remote-trigger control plane.
	// Owner-only — guests must not be able to push apps onto a phone.
	mux.HandleFunc("/mobile/sessions", s.auth(s.handleMobileSessions))
	mux.HandleFunc("/mobile/insert", s.auth(s.handleMobileInsert))

	// Dev server (reverse proxy to local Metro/Vite/Flutter dev server)
	mux.HandleFunc("/dev/status", s.authSDKOrGuest(s.handleDevServerStatus))
	mux.HandleFunc("/dev/target", s.authSDKOrGuest(s.handleDevServerTarget))
	mux.HandleFunc("/dev/start", s.auth(s.handleDevServerStart))
	mux.HandleFunc("/dev/stop", s.auth(s.handleDevServerStop))
	mux.HandleFunc("/dev/reload", s.authSDKOrGuest(s.handleDevServerReload))
	mux.HandleFunc("/dev/reload-app", s.authSDKOrGuest(s.handleReloadApp))
	mux.HandleFunc("/dev/native-fingerprint", s.authSDKOrGuest(s.handleNativeFingerprintGet))
	mux.HandleFunc("/dev/native-fingerprint/refresh", s.authSDKOrGuest(s.handleNativeFingerprintRefresh))
	mux.HandleFunc("/dev/events", s.authSDKOrGuest(s.handleDevServerEvents))
	mux.HandleFunc("/dev/compatibility", s.authSDKOrGuest(s.handleDevServerCompatibility))
	mux.HandleFunc("/dev/builds", s.auth(s.handleDevServerBuilds))
	mux.HandleFunc("/dev/build-native", s.authSDKOrGuest(s.handleBuildNativeBundle))
	mux.HandleFunc("/dev/native-bundle", s.handleServeNativeBundle) // No auth — serves compiled bundle
	mux.HandleFunc("/dev/native-assets", s.handleServeNativeAssets) // No auth — serves compiled assets
	// Web build target outputs (target=web-js-bundle / web-hermes-wasm).
	// Registered before the catch-all /dev/ proxy so they don't get
	// shadowed by the dev-server reverse proxy.
	mux.HandleFunc("/dev/web-bundle/", s.handleServeWebBundle)              // No auth — serves built web bundle (static files)
	mux.HandleFunc("/dev/hermes-wasm-runtime", s.handleServeHermesWasm)     // No auth — serves hermes.wasm for the runner page
	mux.HandleFunc("/dev/web-bundle/info", s.auth(s.handleWebBundleInfo))   // Owner — returns metadata about the current bundle
	mux.HandleFunc("/dev/web-bundle/ack", s.auth(s.handleWebBundleAck))     // Owner — iframe reports successful load
	mux.HandleFunc("/dev/web-bundle/error", s.auth(s.handleWebBundleError)) // Owner — iframe reports JS error during init
	mux.HandleFunc("/dev/", s.handleDevServerProxy)                         // No auth — serves proxied dev content for browser/webview preview surfaces
	// Parallel Expo Web: sibling preview process so the Web Reload tab
	// can render RN apps in a browser iframe without killing Metro's
	// dev-client (which serves Hermes bundles to the phone via /dev/*).
	mux.HandleFunc("/dev/web-preview/start", s.auth(s.handleDevWebPreviewStart))
	mux.HandleFunc("/dev/web-preview/stop", s.auth(s.handleDevWebPreviewStop))
	mux.HandleFunc("/dev-web/", s.handleDevWebProxy) // No auth — matches /dev/ convention for browser iframe previews
	// Monorepo workspace manifest (declarative yaver.workspace.yaml)
	mux.HandleFunc("/workspace", s.auth(s.handleWorkspace))
	mux.HandleFunc("/workspace/apps", s.auth(s.handleWorkspaceApps))
	// Diagnose — one-shot self-check (CLI, HTTP, MCP, mobile, web)
	mux.HandleFunc("/diagnose", s.auth(s.handleDiagnose))
	mux.HandleFunc("/diagnose/stream", s.auth(s.handleDiagnoseStream))
	mux.HandleFunc("/unity/test", s.authSDKOrGuest(s.handleUnityTest))
	mux.HandleFunc("/unity/build", s.authSDKOrGuest(s.handleUnityBuild))
	mux.HandleFunc("/unity/relaunch", s.authSDKOrGuest(s.handleUnityRelaunch))
	mux.HandleFunc("/unity/runs", s.auth(s.handleUnityRuns))
	mux.HandleFunc("/mobile-workers/preview-session", s.authSDK(s.handleMobileWorkerPreviewSession))
	mux.HandleFunc("/mobile-workers/preview-session/command", s.authSDK(s.handleMobileWorkerPreviewCommand))

	// Relay-based expose (subdomain routing via QUIC relay)
	mux.HandleFunc("/expose/start", s.auth(s.handleRelayExposeStart))
	mux.HandleFunc("/expose/stop", s.auth(s.handleRelayExposeStop))
	mux.HandleFunc("/expose/list", s.auth(s.handleRelayExposeList))

	// Browser automation (AI-driven browser control)
	mux.HandleFunc("/browser/sessions", s.auth(s.handleBrowserSessions))
	mux.HandleFunc("/browser/sessions/", s.auth(s.handleBrowserSessionByID))
	mux.HandleFunc("/browser/events", s.auth(s.handleBrowserEvents))
	mux.HandleFunc("/browser/events/", s.auth(s.handleBrowserEvents))

	// Projects (discovery + workdir switching + actions)
	mux.HandleFunc("/projects", s.auth(s.handleProjects))
	mux.HandleFunc("/projects/refresh", s.auth(s.handleProjectsRefresh))
	mux.HandleFunc("/projects/mobile", s.auth(s.handleMobileProjects))
	mux.HandleFunc("/projects/web", s.auth(s.handleProjectsByCapability))
	mux.HandleFunc("/projects/all", s.auth(s.handleProjectsByCapability))
	mux.HandleFunc("/remote-runtime/capabilities", s.auth(s.handleRemoteRuntimeCapabilities))
	mux.HandleFunc("/remote-runtime/sessions", s.auth(s.handleRemoteRuntimeSessions))
	mux.HandleFunc("/remote-runtime/sessions/", s.auth(s.handleRemoteRuntimeSessionRoute))
	// Monorepo detection — desktop/agent/monorepo_detect.go
	mux.HandleFunc("/projects/monorepo", s.auth(s.handleMonorepoDetect))
	mux.HandleFunc("/projects/switch", s.auth(s.handleProjectSwitch))
	mux.HandleFunc("/projects/actions", s.auth(s.handleProjectActions))
	mux.HandleFunc("/publish/config", s.auth(s.handlePublishConfig))
	mux.HandleFunc("/publish/run", s.auth(s.handlePublishRun))
	mux.HandleFunc("/publish/runs", s.auth(s.handlePublishRuns))
	mux.HandleFunc("/publish/runs/", s.auth(s.handlePublishRunByID))
	mux.HandleFunc("/vibing", s.authSDKOrGuest(s.handleVibing))
	mux.HandleFunc("/vibing/eligibility", s.authSDKOrGuest(s.handleVibingEligibility))
	mux.HandleFunc("/vibing/commit", s.authSDKOrGuest(s.handleVibingCommit))
	mux.HandleFunc("/vibing/deploy", s.authSDKOrGuest(s.handleVibingDeploy))
	mux.HandleFunc("/vibing/execute", s.authSDKOrGuest(s.handleVibingExecute))
	// SDK-accessible read-back + continue for vibing tasks. /tasks/{id}
	// itself requires owner-auth, so without these endpoints the
	// Feedback SDK chat surface couldn't poll its own task once
	// /vibing/execute returned. Source-gated to "vibing" tasks only.
	mux.HandleFunc("/vibing/task/", s.authSDKOrGuest(s.handleVibingTaskByID))
	mux.HandleFunc("/vibing/surprise", s.authSDKOrGuest(s.handleVibingSurprise))
	// Vibe Preview — live screenshot stream of a remote dev server, viewed
	// from the mobile app while vibe-coding (docs/vibe-preview-streaming.md).
	// /status + /snapshot are read-ish and inherit guest-vibing scope; the
	// mutating endpoints stay owner-only.
	mux.HandleFunc("/vibing/preview/start", s.auth(s.handleVibePreviewStart))
	mux.HandleFunc("/vibing/preview/stop", s.auth(s.handleVibePreviewStop))
	mux.HandleFunc("/vibing/preview/status", s.authSDKOrGuest(s.handleVibePreviewStatus))
	mux.HandleFunc("/vibing/preview/snapshot", s.authSDKOrGuest(s.handleVibePreviewSnapshot))
	mux.HandleFunc("/vibing/preview/events", s.authSDKOrGuest(s.handleVibePreviewEvents))
	mux.HandleFunc("/vibing/preview/frames/", s.authSDKOrGuest(s.handleVibePreviewFrame))
	mux.HandleFunc("/vibing/preview/clip/start", s.auth(s.handleVibePreviewClipStart))
	mux.HandleFunc("/vibing/preview/clip/stop", s.auth(s.handleVibePreviewClipStop))
	mux.HandleFunc("/vibing/preview/clips", s.authSDKOrGuest(s.handleVibePreviewClips))
	mux.HandleFunc("/vibing/preview/clip/", s.authSDKOrGuest(s.handleVibePreviewClip))
	mux.HandleFunc("/vibing/preview/summaries", s.authSDKOrGuest(s.handleVibePreviewSummaries))
	mux.HandleFunc("/vibing/preview/clip/upload", s.auth(s.handleVibePreviewClipUpload))
	mux.HandleFunc("/vibing/project/remote", s.auth(s.handleProjectRemote))

	// Recovery: central catalog of fix prompts routed to the wrapped AI agent.
	mux.HandleFunc("/recover", s.auth(s.handleRecover))

	// Todo list (queued bug reports for batch implementation) — SDK-accessible for add/list/count
	mux.HandleFunc("/todolist", s.authSDK(s.handleTodoList))
	mux.HandleFunc("/todolist/count", s.authSDK(s.handleTodoListCount))
	mux.HandleFunc("/todolist/classify", s.authSDK(s.handleTodoListClassify))
	mux.HandleFunc("/todolist/auto-consume", s.auth(s.handleTodoListAutoConsume))
	mux.HandleFunc("/todolist/implement-all", s.auth(s.handleTodoListImplementAll))
	mux.HandleFunc("/todolist/", s.authSDK(s.handleTodoListByID))

	// Session audit (missed items detector) — accessible from mobile
	mux.HandleFunc("/session-audit", s.auth(s.handleSessionAudit))
	mux.HandleFunc("/session-audit/all", s.auth(s.handleSessionAuditAll))
	mux.HandleFunc("/autopilot", s.auth(s.handleAutopilot))

	// Quality gates (lint + typecheck + format + test)
	mux.HandleFunc("/quality/detect", s.auth(s.handleQualityDetect))
	mux.HandleFunc("/quality/run", s.auth(s.handleQualityRun))
	mux.HandleFunc("/quality/run-all", s.auth(s.handleQualityRunAll))
	mux.HandleFunc("/quality/results", s.auth(s.handleQualityResults))
	mux.HandleFunc("/quality/results/", s.auth(s.handleQualityResultByID))

	// Health monitor (production URL pinging)
	mux.HandleFunc("/healthmon", s.auth(s.handleHealthMon))
	mux.HandleFunc("/healthmon/", s.auth(s.handleHealthMonByID))

	// Git operations (read-only + safe writes)
	mux.HandleFunc("/git/status", s.auth(s.handleGitStatus))
	mux.HandleFunc("/git/log", s.auth(s.handleGitLog))
	mux.HandleFunc("/git/diff", s.auth(s.handleGitDiff))
	mux.HandleFunc("/git/branches", s.auth(s.handleGitBranches))
	mux.HandleFunc("/git/stash", s.auth(s.handleGitStash))
	mux.HandleFunc("/git/stash-pop", s.auth(s.handleGitStashPop))
	mux.HandleFunc("/git/checkout", s.auth(s.handleGitCheckout))
	mux.HandleFunc("/git/commit", s.auth(s.handleGitCommit))
	mux.HandleFunc("/git/commit-push", s.auth(s.handleGitCommitPush))
	mux.HandleFunc("/git/push", s.auth(s.handleGitPush))
	mux.HandleFunc("/git/pull", s.auth(s.handleGitPull))
	mux.HandleFunc("/git/revert", s.auth(s.handleGitRevert))

	// Repo sync (clone/pull repos, manage git credentials — P2P only)
	mux.HandleFunc("/repos/clone", s.auth(s.handleRepoCloneWithMetadata))
	mux.HandleFunc("/repos/pull", s.auth(s.handleRepoPull))
	mux.HandleFunc("/repos/list", s.auth(s.handleRepoList))
	mux.HandleFunc("/repos/delete", s.auth(s.handleRepoDelete))
	mux.HandleFunc("/repos/credentials", s.auth(s.handleRepoCredentials))
	mux.HandleFunc("/repos/credentials/", s.auth(s.handleRepoCredentialByHost))

	// Phone-driven dependency installer — `yaver install <tool>` over HTTP.
	// Output streams to /streams/install:<tool>. Owner-auth only (runs
	// shell commands and downloads runtimes into ~/.yaver/runtimes).
	mux.HandleFunc("/install/", s.auth(s.handleInstall))
	mux.HandleFunc("/install", s.auth(s.handleInstall))

	// Cross-machine peer forwarder. Any owner-auth'd request to
	// /peer/<deviceId>/<anything> is re-signed and forwarded to the
	// named agent via the same relay transport the MCP proxy uses.
	// Lets the mobile app and web dashboard install onto / inspect
	// a paired peer without having to rebind the connection.
	mux.HandleFunc("/peer/", s.auth(s.handlePeerProxy))

	// Git provider (GitHub/GitLab — auto-detect from dev machine's existing credentials)
	mux.HandleFunc("/git/provider/detect", s.auth(s.handleGitProviderAutoDetect))
	mux.HandleFunc("/git/provider/setup", s.auth(s.handleGitProviderSetup))
	mux.HandleFunc("/git/provider/status", s.auth(s.handleGitProviderStatus))
	mux.HandleFunc("/git/provider/repos", s.auth(s.handleGitProviderRepos))
	// Device Flow (RFC 8628) — start a GitHub/GitLab device-code
	// authorization on the agent and poll until the user approves it
	// in any browser. Returns the user_code + verification_uri the
	// caller should display, plus a session_id to poll. Token never
	// reaches Convex; persistence shape matches /git/provider/setup.
	// Specific routes registered before the catch-all /git/provider/
	// remove handler below so they take precedence.
	mux.HandleFunc("/git/provider/oauth/start", s.auth(s.handleGitProviderOAuthStart))
	mux.HandleFunc("/git/provider/oauth/status", s.auth(s.handleGitProviderOAuthStatus))
	// New repo creation — used by the mobile sandbox wizard's
	// "Configure now" git step. Owner-only (uses the user's stored
	// PAT to call the provider API on their behalf + commit a
	// yaver.workspace.yaml). Not opened to SDK / guest tokens.
	mux.HandleFunc("/git/provider/repo/create", s.auth(s.handleGitProviderRepoCreate))
	// Deploy-token onboarding (Convex / Cloudflare / npm / PyPI /
	// TestFlight / Play). Vault-backed; values never sync to Convex.
	// Owner-only — guests can't enumerate or save deploy secrets.
	mux.HandleFunc("/deploy/tokens/catalogue", s.auth(s.handleDeployTokensCatalogue))
	mux.HandleFunc("/deploy/tokens/status", s.auth(s.handleDeployTokensStatus))
	mux.HandleFunc("/deploy/tokens/verify", s.auth(s.handleDeployTokensVerify))
	mux.HandleFunc("/deploy/tokens/save", s.auth(s.handleDeployTokensSave))
	mux.HandleFunc("/git/provider/", s.auth(s.handleGitProviderRemove))
	// Find an existing clone of a remote URL anywhere under the
	// agent's project-discovery roots. The Feedback SDK's git-setup
	// wizard calls this before issuing /repos/clone so a user who
	// already cloned the repo manually doesn't end up with a
	// duplicate clone in a fresh location.
	mux.HandleFunc("/git/find-repo", s.auth(s.handleGitFindRepo))

	// Multi-user management (shared machines)
	mux.HandleFunc("/users", s.auth(s.handleMultiUserList))
	mux.HandleFunc("/users/me", s.auth(s.handleMultiUserMe))
	mux.HandleFunc("/users/", s.auth(s.handleMultiUserRemove))
	mux.HandleFunc("/sessions", s.auth(s.handleMultiUserSessions))

	// Container sandbox management
	mux.HandleFunc("/sandbox/status", s.auth(s.handleSandboxStatus))
	mux.HandleFunc("/sandbox/config", s.auth(s.handleSandboxConfig))
	mux.HandleFunc("/sandbox/build", s.auth(s.handleSandboxBuild))
	mux.HandleFunc("/sandbox/quickstart", s.auth(s.handleSandboxQuickstart))

	// Convex local backend — dashboard proxy endpoints
	mux.HandleFunc("/convex/status", s.auth(s.handleConvexStatus))
	mux.HandleFunc("/convex/tables", s.auth(s.handleConvexTables))
	mux.HandleFunc("/convex/browse", s.auth(s.handleConvexBrowse))
	mux.HandleFunc("/convex/query", s.auth(s.handleConvexQuery))
	mux.HandleFunc("/convex/mutate", s.auth(s.handleConvexMutate))
	mux.HandleFunc("/convex/action", s.auth(s.handleConvexAction))
	mux.HandleFunc("/convex/schema", s.auth(s.handleConvexSchema))
	mux.HandleFunc("/convex/export", s.auth(s.handleConvexExport))
	mux.HandleFunc("/convex/install-helper", s.auth(s.handleConvexInstallHelper))

	// Universal backend dashboard (Convex, Supabase, Postgres, PocketBase, Appwrite, SQLite)
	mux.HandleFunc("/backend/status", s.auth(s.handleBackendStatus))
	mux.HandleFunc("/backend/tables", s.auth(s.handleDBTables))
	mux.HandleFunc("/backend/browse", s.auth(s.handleDBBrowse))
	mux.HandleFunc("/backend/query", s.auth(s.handleDBQuery))
	mux.HandleFunc("/backend/insert", s.auth(s.handleDBInsert))
	mux.HandleFunc("/backend/update", s.auth(s.handleDBUpdate))
	mux.HandleFunc("/backend/delete", s.auth(s.handleDBDelete))

	// Cloud emulators (AWS/GCP/Azure local dev)
	mux.HandleFunc("/cloud/emu/start", s.auth(s.handleCloudEmuStart))
	mux.HandleFunc("/cloud/emu/stop", s.auth(s.handleCloudEmuStop))
	mux.HandleFunc("/cloud/emu/status", s.auth(s.handleCloudEmuStatus))
	mux.HandleFunc("/cloud/emu/config", s.auth(s.handleCloudEmuConfig))

	// Switch engine: change backend/host with snapshots + 7-day rollback
	mux.HandleFunc("/switch/targets", s.auth(s.handleSwitchTargets))
	mux.HandleFunc("/switch/plan", s.auth(s.handleSwitchPlan))
	mux.HandleFunc("/switch/run", s.auth(s.handleSwitchRun))
	mux.HandleFunc("/switch/rollback", s.auth(s.handleSwitchRollback))
	mux.HandleFunc("/switch/history", s.auth(s.handleSwitchHistory))
	mux.HandleFunc("/switch/cleanup", s.auth(s.handleSwitchCleanup))

	// Cloud accounts (encrypted provider credentials)
	mux.HandleFunc("/accounts", s.auth(s.handleAccountList))
	mux.HandleFunc("/accounts/connect", s.auth(s.handleAccountConnect))
	mux.HandleFunc("/accounts/disconnect", s.auth(s.handleAccountDisconnect))
	mux.HandleFunc("/accounts/status", s.auth(s.handleAccountStatus))

	// Studio proxy (mobile-access Drizzle/Supabase/Convex/PocketBase dashboards)
	mux.HandleFunc("/studios", s.auth(s.handleStudioList))
	mux.HandleFunc("/proxy/", s.auth(s.handleStudioProxy))

	// Switch cost comparator
	mux.HandleFunc("/switch/cost", s.auth(s.handleSwitchCost))

	// Logs streaming (SSE) + schema viewer
	mux.HandleFunc("/logs/stream", s.auth(s.handleLogsStream))
	mux.HandleFunc("/backend/schema", s.auth(s.handleSchemaView))
	mux.HandleFunc("/storage/list", s.auth(s.handleStorageList))
	mux.HandleFunc("/jobs/list", s.auth(s.handleJobsList))

	// Yaver Console: Docker engine, live metrics, web terminal, catalog
	mux.HandleFunc("/console/containers", s.auth(s.handleConsoleContainers))
	mux.HandleFunc("/console/containers/action", s.auth(s.handleConsoleContainerAction))
	mux.HandleFunc("/console/containers/stats", s.auth(s.handleConsoleContainerStats))
	mux.HandleFunc("/console/images", s.auth(s.handleConsoleImages))
	mux.HandleFunc("/console/volumes", s.auth(s.handleConsoleVolumes))
	mux.HandleFunc("/console/prune", s.auth(s.handleConsolePrune))
	mux.HandleFunc("/console/metrics", s.auth(s.handleMetricsSnapshot))
	mux.HandleFunc("/console/catalog", s.auth(s.handleCatalogList))
	mux.HandleFunc("/console/catalog/install", s.auth(s.handleCatalogInstall))
	mux.HandleFunc("/ws/metrics", s.auth(s.handleMetricsStream))
	mux.HandleFunc("/ws/logs", s.auth(s.handleContainerLogsStream))
	mux.HandleFunc("/ws/terminal", s.auth(s.handleTerminalWS))
	mux.HandleFunc("/console/machines", s.auth(s.handleConsoleMachines))

	// Deploy pipeline
	mux.HandleFunc("/deploy/run", s.auth(s.handleDeployRun))
	mux.HandleFunc("/deploy/list", s.auth(s.handleDeployList))
	mux.HandleFunc("/deploy/rollback", s.auth(s.handleDeployRollback))
	mux.HandleFunc("/deploy/config", s.auth(s.handleDeployConfig))
	mux.HandleFunc("/deploy/webhook", s.handleDeployWebhook)
	mux.HandleFunc("/deploy/preview", s.auth(s.handleDeployPreview))

	// Backups
	mux.HandleFunc("/backups/create", s.auth(s.handleBackupCreate))
	mux.HandleFunc("/backups/list", s.auth(s.handleBackupList))
	mux.HandleFunc("/backups/restore", s.auth(s.handleBackupRestore))
	mux.HandleFunc("/backups/delete", s.auth(s.handleBackupDelete))
	mux.HandleFunc("/backups/sync", s.auth(s.handleBackupSync))
	mux.HandleFunc("/backups/auto", s.auth(s.handleBackupAuto))

	// Domains + SSL (Caddy)
	mux.HandleFunc("/domains/list", s.auth(s.handleDomainList))
	mux.HandleFunc("/domains/add", s.auth(s.handleDomainAdd))
	mux.HandleFunc("/domains/remove", s.auth(s.handleDomainRemove))

	// Log search — see line 186 for the primary registration (handleLogsSearch)
	mux.HandleFunc("/logs/index/start", s.auth(s.handleLogIndexStart))

	// Error tracking. /errors/ingest accepts pushes from the
	// Feedback SDK + 3rd-party app crash hooks, so it goes
	// through authSDK (accepts owner tokens, paired tokens, and
	// scoped SDK tokens). H-7 fix: previously unauth — disk-fill
	// + host-existence-leak vector for any unauthenticated scanner.
	mux.HandleFunc("/errors/ingest", s.rateLimit(s.authSDK(s.handleErrorIngest)))
	mux.HandleFunc("/errors/groups", s.auth(s.handleErrorGroups))
	mux.HandleFunc("/errors/instances", s.auth(s.handleErrorInstances))
	// /errors/resolve registered at line 175 (handleErrorsResolve) — this duplicate removed

	// Environment clone
	mux.HandleFunc("/env/clone", s.auth(s.handleCloneEnvironment))

	// Cron management
	mux.HandleFunc("/cron/create", s.auth(s.handleCronCreate))
	mux.HandleFunc("/cron/delete", s.auth(s.handleCronDelete))

	// Uptime alerts
	mux.HandleFunc("/uptime/list", s.auth(s.handleUptimeList))
	mux.HandleFunc("/uptime/add", s.auth(s.handleUptimeAdd))
	mux.HandleFunc("/uptime/remove", s.auth(s.handleUptimeRemove))

	// Object storage (S3/MinIO/R2 compatible)
	mux.HandleFunc("/objects/list", s.auth(s.handleObjectList))
	mux.HandleFunc("/objects/upload", s.auth(s.handleObjectUpload))
	mux.HandleFunc("/objects/delete", s.auth(s.handleObjectDelete))

	// Staging environment creation
	mux.HandleFunc("/staging/create", s.auth(s.handleStagingCreate))

	// Queue inspector (ElasticMQ / SQS)
	mux.HandleFunc("/queues/list", s.auth(s.handleQueueList))
	mux.HandleFunc("/queues/purge", s.auth(s.handleQueuePurge))

	// Secret rotation
	mux.HandleFunc("/secrets/rotate", s.auth(s.handleSecretRotate))

	// Declarative project manifest (`.yaver/project.yaml`)
	mux.HandleFunc("/project/runtime", s.auth(s.handleProjectRuntime))
	mux.HandleFunc("/project/runtime/apply", s.auth(s.handleProjectRuntimeApply))
	mux.HandleFunc("/manifest/get", s.auth(s.handleManifestGet))
	mux.HandleFunc("/manifest/set", s.auth(s.handleManifestSet))
	mux.HandleFunc("/manifest/apply", s.auth(s.handleManifestApply))
	mux.HandleFunc("/manifest/diff", s.auth(s.handleManifestDiff))

	// Phone-first mini backend (in-app SQLite projects, portable to switch-engine targets)
	s.registerPhoneRoutes(mux)

	// Companion compute (yaver.companion.yaml — crons + workers for serverless projects)
	s.registerCompanionRoutes(mux)

	// DNS helpers — Cloudflare first (others later). Used by the phone-first
	// "Custom domain" flow to CNAME <sub>.<zone> to cloud.yaver.io in one tap.
	s.registerDNSRoutes(mux)

	// Escape routes — curated "I'm on X, get me to Y" list over the existing
	// SwitchEngine. Trust-signal surface, not the headline feature: reassures
	// vibe coders there's no lock-in without stealing attention from the
	// phone-first wedge. See phone_escape.go.
	s.registerEscapeRoutes(mux)

	// Public data API — /data/<slug>/<table>[/<id>] with per-project API
	// tokens + CORS. The surface a third-party RN/web app's runtime hits
	// from the end-user's device. See phone_data_http.go + phone_tokens.go.
	s.registerPhoneDataRoutes(mux)

	// Embedded SPA (when console_static is populated by the build)
	s.mountConsoleEmbed(mux)

	// Audit log + PITR + multi-region HA
	mux.HandleFunc("/audit/list", s.auth(s.handleAuditList))
	mux.HandleFunc("/pitr/setup", s.auth(s.handlePITRSetup))
	mux.HandleFunc("/pitr/restore", s.auth(s.handlePITRRestore))
	mux.HandleFunc("/multiregion/deploy", s.auth(s.handleMultiRegionDeploy))
	mux.HandleFunc("/provider/rotate", s.auth(s.handleProviderRotate))
	mux.HandleFunc("/replication/setup", s.auth(s.handleReplicaSetup))
	mux.HandleFunc("/multiregion/orchestrate", s.auth(s.handleMultiRegionOrchestrate))

	// Cross-machine aggregation
	mux.HandleFunc("/aggregated/logs", s.auth(s.handleAggregatedLogs))
	mux.HandleFunc("/aggregated/errors", s.auth(s.handleAggregatedErrors))
	mux.HandleFunc("/aggregated/audit", s.auth(s.handleAggregatedAudit))
	mux.HandleFunc("/aggregated/uptime", s.auth(s.handleAggregatedUptime))
	mux.HandleFunc("/aggregated/deploys", s.auth(s.handleAggregatedDeploys))

	// CI runner
	mux.HandleFunc("/ci/run", s.auth(s.handleCIRun))
	mux.HandleFunc("/ci/list", s.auth(s.handleCIList))
	mux.HandleFunc("/ci/config", s.auth(s.handleCIConfig))

	// Metrics history + threshold alerts
	mux.HandleFunc("/console/metrics/history", s.auth(s.handleMetricsHistory))
	mux.HandleFunc("/alerts/list", s.auth(s.handleAlertList))
	mux.HandleFunc("/alerts/add", s.auth(s.handleAlertAdd))
	mux.HandleFunc("/alerts/remove", s.auth(s.handleAlertRemove))

	// Backup encryption toggle (encrypts new backups with the master key)
	mux.HandleFunc("/backups/encryption", s.auth(s.handleBackupEncryption))

	// Project environments (local/staging/production switcher)
	mux.HandleFunc("/project/env/list", s.auth(s.handleProjectEnvList))
	mux.HandleFunc("/project/env/switch", s.auth(s.handleProjectEnvSwitch))
	mux.HandleFunc("/project/env/save", s.auth(s.handleProjectEnvSave))
	mux.HandleFunc("/project/env/load", s.auth(s.handleProjectEnvLoad))
	mux.HandleFunc("/project/env/delete", s.auth(s.handleProjectEnvDelete))

	// Overview home summary
	mux.HandleFunc("/overview/summary", s.auth(s.handleOverviewSummary))
	mux.HandleFunc("/sync/status", s.auth(s.handleSyncStatus))

	// Per-project dashboard manager (Convex/Supabase/Drizzle/PocketBase per project)
	mux.HandleFunc("/dashboard/start", s.auth(s.handleDashboardStart))
	mux.HandleFunc("/dashboard/stop", s.auth(s.handleDashboardStop))
	mux.HandleFunc("/dashboard/list", s.auth(s.handleDashboardList))
	mux.HandleFunc("/dashboard/", s.auth(s.handleDashboardProxy))

	// Mailpit viewer (native mobile + web — no iframe)
	mux.HandleFunc("/mail/list", s.auth(s.handleMailpitList))
	mux.HandleFunc("/mail/message", s.auth(s.handleMailpitMessage))
	mux.HandleFunc("/mail/delete", s.auth(s.handleMailpitDelete))

	// Guest access management (host invites guests to use their agent)
	mux.HandleFunc("/guests", s.auth(s.handleGuestList))
	mux.HandleFunc("/guests/invite", s.auth(s.handleGuestInvite))
	mux.HandleFunc("/guests/revoke", s.auth(s.handleGuestRevoke))
	mux.HandleFunc("/guests/config", s.auth(s.handleGuestConfig))
	mux.HandleFunc("/guests/usage", s.auth(s.handleGuestUsage))
	mux.HandleFunc("/host-share/workspace/status", s.auth(s.handleHostShareWorkspaceStatus))
	mux.HandleFunc("/host-share/workspace/bootstrap", s.auth(s.handleHostShareWorkspaceBootstrap))
	mux.HandleFunc("/host-share/workspace/attach-repo", s.auth(s.handleHostShareWorkspaceAttachRepo))
	mux.HandleFunc("/host-share/workspace/pull-from-guest", s.auth(s.handleHostShareWorkspacePullFromGuest))
	mux.HandleFunc("/host-share/workspace/push-to-guest", s.auth(s.handleHostShareWorkspacePushToGuest))

	// Agent context (repo switching)
	mux.HandleFunc("/agent/workdir", s.auth(s.handleAgentWorkdir))
	mux.HandleFunc("/agent/context", s.auth(s.handleAgentContext))

	// Builds (remote build & artifact transfer) — SDK-accessible
	mux.HandleFunc("/builds", s.authSDK(s.handleBuilds))
	mux.HandleFunc("/builds/register", s.authSDK(s.handleBuildRegister))
	mux.HandleFunc("/builds/", s.authSDK(s.handleBuildByID))

	// Vault (P2P encrypted key sync)
	// /vault/* is rate-limited on top of auth: the value payload
	// of /vault/get is the most sensitive single response the agent
	// can produce, so we make it prohibitively expensive to walk
	// the namespace by hammering names.
	// Build toolchain preflight + deploy-script generator. Owner full
	// access; guests with scope=full or scope=deploy can hit the
	// preflight and generator. /deploy/ship actually executes the
	// generated script on the host and streams stdout/stderr; it is
	// the shared-machine deploy surface, gated by allowedProjects in
	// the handler itself.
	mux.HandleFunc("/doctor/build", s.auth(s.handleDoctorBuild))
	mux.HandleFunc("/deploy/templates", s.auth(s.handleDeployTemplates))
	mux.HandleFunc("/deploy/capabilities", s.auth(s.handleDeployCapabilities))
	mux.HandleFunc("/deploy/generate", s.auth(s.handleDeployGenerate))
	mux.HandleFunc("/fleet/deploy-options", s.auth(s.handleFleetDeployOptions))
	mux.HandleFunc("/deploy/ship", s.auth(s.handleDeployShip))
	mux.HandleFunc("/deploy/runs", s.auth(s.handleDeployRuns))
	mux.HandleFunc("/deploy/runs/", s.auth(s.handleDeployRunDetail))
	mux.HandleFunc("/deploy/diagnose", s.auth(s.handleDeployDiagnose))

	// Cable + WiFi phone discovery — surfaces the same data as
	// `yaver wire detect` / `yaver wireless detect` so the web dashboard
	// and mobile app can list reachable phones per agent. Owner-auth only;
	// nothing is persisted to Convex (privacy contract: device serials and
	// IPs stay local to the agent).
	mux.HandleFunc("/wire/devices", s.auth(s.handleWireDevices))
	mux.HandleFunc("/wireless/devices", s.auth(s.handleWirelessDevices))

	// Unified Runner surface (RUNNER_DEV.md Phase 1 + Phase 2). Owner-auth on
	// every path; guest tiers (RunnerView / RunnerSubmit) defined in
	// guest_scope.go control which subset a guest can reach. Sandboxes
	// and agent sessions are owner-only in Phase 2.
	mux.HandleFunc("/runner/jobs", s.auth(s.handleRunnerJobs))
	mux.HandleFunc("/runner/jobs/", s.auth(s.handleRunnerJobByID))
	mux.HandleFunc("/runner/runs", s.auth(s.handleRunnerRuns))
	mux.HandleFunc("/runner/runs/", s.auth(s.handleRunnerRunByID))
	mux.HandleFunc("/runner/pools", s.auth(s.handleRunnerPools))
	mux.HandleFunc("/runner/sandboxes", s.auth(s.handleRunnerSandboxes))
	mux.HandleFunc("/runner/sandboxes/", s.auth(s.handleRunnerSandboxByID))
	mux.HandleFunc("/runner/agent/sessions", s.auth(s.handleRunnerAgentSessions))
	mux.HandleFunc("/runner/agent/sessions/", s.auth(s.handleRunnerAgentSessionByID))

	mux.HandleFunc("/vault/list", s.rateLimit(s.auth(s.handleVaultList)))
	mux.HandleFunc("/vault/get", s.rateLimit(s.auth(s.handleVaultGet)))
	mux.HandleFunc("/vault/set", s.rateLimit(s.auth(s.handleVaultSet)))
	mux.HandleFunc("/vault/delete", s.rateLimit(s.auth(s.handleVaultDelete)))
	mux.HandleFunc("/vault/env", s.rateLimit(s.auth(s.handleVaultEnv)))
	// Peer-to-peer sync (owner-auth; never cross-user).
	mux.HandleFunc("/vault/digest", s.rateLimit(s.auth(s.handleVaultDigest)))
	mux.HandleFunc("/vault/sync", s.rateLimit(s.auth(s.handleVaultSync)))
	mux.HandleFunc("/vault/push", s.rateLimit(s.auth(s.handleVaultPush)))
	mux.HandleFunc("/vault/peer-sync", s.rateLimit(s.auth(s.handleVaultPeerSync)))

	// Yaver-agent (mobile-embedded control-plane LLM) provider config —
	// stored in vault under project "yaver-agent". GET returns the
	// metadata without the API key value; POST writes any subset.
	mux.HandleFunc("/yaver-agent/config", s.rateLimit(s.auth(s.handleYaverAgentConfig)))
	// Aggregated device + runner audit consumed by the mobile-embedded
	// yaver-agent so it can answer "what's the state of this box?" in
	// a single round trip.
	mux.HandleFunc("/yaver-agent/audit", s.auth(s.handleYaverAgentDeviceAudit))

	// MCP (Model Context Protocol) endpoint — JSON-RPC 2.0 over HTTP
	mux.HandleFunc("/mcp", s.auth(s.handleMCP))

	handler := s.ipAllowlist(withCORS(mux))

	s.server = &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", s.port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(shutdownCtx)
		if s.tlsServer != nil {
			s.tlsServer.Shutdown(shutdownCtx)
		}
	}()

	// Start TLS server alongside HTTP if configured
	if s.tlsPort > 0 && s.tlsFingerprint != "" {
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{s.tlsCert},
			MinVersion:   tls.VersionTLS12,
		}
		s.tlsServer = &http.Server{
			Addr:              fmt.Sprintf("0.0.0.0:%d", s.tlsPort),
			Handler:           handler,
			TLSConfig:         tlsCfg,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			fpPreview := s.tlsFingerprint
			if len(fpPreview) > 16 {
				fpPreview = fpPreview[:16] + "..."
			}
			log.Printf("HTTPS server listening on 0.0.0.0:%d (fingerprint: %s)", s.tlsPort, fpPreview)
			if err := s.tlsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("[TLS] HTTPS server error: %v", err)
			}
		}()
	}

	// Start guest list refresh goroutine (polls Convex every 60s)
	go s.refreshGuestList(ctx)

	// Proactively probe runner-CLI tokens every 6h so a Claude Code /
	// Codex token rotation surfaces in the dashboard's [SIGN IN]
	// badge BEFORE the user kicks off a task and watches it die at
	// iteration 1. Reuses the existing /runner-auth/status pipeline
	// — collectRunnerAuthStatusRows() runs the same lightweight
	// CLI checks the dashboard polls, and syncRunnerAuthIncidents()
	// folds any "authConfigured flipped false" results into the dev
	// incidents Convex stream the mobile app already consumes.
	go s.runnerAuthHealthLoop(ctx)

	log.Printf("HTTP server listening on 0.0.0.0:%d", s.port)
	if len(s.allowedCIDRs) > 0 {
		cidrs := make([]string, len(s.allowedCIDRs))
		for i, c := range s.allowedCIDRs {
			cidrs[i] = c.String()
		}
		log.Printf("IP allowlist: %s", strings.Join(cidrs, ", "))
	}
	err := s.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------
// Cached token info
// ---------------------------------------------------------------------------

type cachedTokenInfo struct {
	userID               string
	isSdk                bool
	scopes               []string
	allowedCIDRs         []string
	delegatedGuestUserID string
	delegatedGuestScope  string
	sourceSurface        string
	targetDeviceID       string
	allowedProjects      []string
	hostShare            *HostShareAccessInfo
	// storedAt records when the cache entry was minted. The auth() middleware
	// uses this to force a Convex re-validation of any non-owner, non-SDK,
	// non-paired token (i.e. guest tokens) older than guestTokenCacheTTL —
	// so host-side revocations show up within a handful of seconds, even if
	// the 10s refreshGuestList loop is the only channel.
	storedAt time.Time
}

// guestTokenCacheTTL is how long a guest's token can live in the validation
// cache before we re-check with Convex. Keep short so host revocations are
// effectively immediate.
const guestTokenCacheTTL = 15 * time.Second

var hostShareAllowedPrefixes = []string{
	"/info",
	"/agent/status",
	"/agent/runners",
	"/ops",
	"/ops/plan",
	"/ws/terminal",
	"/host-share/workspace/status",
	"/host-share/workspace/attach-repo",
	"/host-share/workspace/pull-from-guest",
	"/host-share/workspace/push-to-guest",
	"/files/roots",
	"/files/list",
	"/files/read",
	"/files/raw",
	"/host-share/fs/write",
	"/host-share/fs/mkdir",
	"/host-share/fs/delete",
}

// Scope-to-path mapping: which URL paths each scope grants access to.
var scopePathPrefixes = map[string][]string{
	"feedback":     {"/feedback"},
	"blackbox":     {"/blackbox/"},
	"builds":       {"/builds"},
	"testapp":      {"/test-app/"},
	"health":       {"/health"},
	"todolist":     {"/todolist"},
	"guest-reload": {"/dev/reload", "/dev/reload-app", "/dev/status", "/dev/target", "/dev/events", "/dev/compatibility", "/unity/test", "/unity/build", "/unity/relaunch"},
	"guest-vibing": {"/vibing"},
	// runner-auth: lets the embedded Feedback SDK inspect runner state
	// and complete either browser-style auth (codex / claude) or
	// token-based setup (opencode) without a separate full-session UI.
	"runner-auth": {"/runner-auth/browser/start", "/runner-auth/browser/status", "/runner-auth/browser/cancel", "/runner-auth/status", "/runner-auth/setup", "/agent/runners", "/agent/runner/switch"},
}

func pathAllowedByScopes(path string, scopes []string) bool {
	for _, scope := range scopes {
		prefixes, ok := scopePathPrefixes[scope]
		if !ok {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
}

func (s *HTTPServer) applyDelegatedGuestSDKHeaders(w http.ResponseWriter, r *http.Request, info *cachedTokenInfo) bool {
	if info == nil || strings.TrimSpace(info.delegatedGuestUserID) == "" {
		return true
	}
	if info.targetDeviceID != "" && strings.TrimSpace(info.targetDeviceID) != strings.TrimSpace(s.deviceID) {
		jsonError(w, http.StatusForbidden, "SDK token is not valid for this device")
		return false
	}
	// Same defensive strip as allowGuest: an SDK token caller can attach
	// X-Yaver-GuestScope: full or X-Yaver-GuestAllowedProjects: every-app
	// to their request; we must NOT honor those. Re-stamp every value
	// from the cached token info, falling back to safe defaults.
	stripGuestRequestHeaders(r)
	r.Header.Set("X-Yaver-Guest", "true")
	r.Header.Set("X-Yaver-GuestUserID", info.delegatedGuestUserID)
	scope := info.delegatedGuestScope
	if strings.TrimSpace(scope) == "" {
		// Default to the safer scope when the token didn't pin one.
		// Pre-fix this fell through to the legacy "full" default,
		// silently elevating SDK callers that lacked explicit scope.
		scope = GuestScopeFeedbackOnly
	}
	r.Header.Set("X-Yaver-GuestScope", scope)
	if info.sourceSurface != "" {
		r.Header.Set("X-Yaver-SourceSurface", info.sourceSurface)
	}
	if len(info.allowedProjects) > 0 {
		r.Header.Set("X-Yaver-GuestAllowedProjects", strings.Join(info.allowedProjects, ","))
	}
	return true
}

func isHostShareAllowedPath(path string, access *HostShareAccessInfo) bool {
	if access == nil {
		return false
	}
	if strings.HasPrefix(path, "/ws/terminal") && !access.Policy.AllowTerminal {
		return false
	}
	for _, prefix := range hostShareAllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func hostShareAllowedProjectsFromHeader(r *http.Request) []string {
	raw := strings.TrimSpace(r.Header.Get("X-Yaver-HostShareAllowedProjects"))
	if raw == "" {
		return nil
	}
	return cleanProjectList(strings.Split(raw, ","))
}

func hostShareCanAccessProject(r *http.Request, projectPath string) bool {
	allowed := hostShareAllowedProjectsFromHeader(r)
	if len(allowed) == 0 {
		return true
	}
	base := strings.TrimSpace(filepath.Base(projectPath))
	for _, p := range allowed {
		if strings.EqualFold(strings.TrimSpace(p), base) || strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(projectPath)) {
			return true
		}
	}
	return false
}

// clientIP extracts the remote IP from the request (strips port).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipMatchesCIDRs checks if an IP is within any of the given CIDRs.
func ipMatchesCIDRs(ipStr string, cidrs []*net.IPNet) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// parseCIDRs parses a list of CIDR strings (also accepts plain IPs).
func parseCIDRs(strs []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, s := range strs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// If no /, treat as single IP
		if !strings.Contains(s, "/") {
			if strings.Contains(s, ":") {
				s += "/128"
			} else {
				s += "/32"
			}
		}
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			log.Printf("[IP] Warning: invalid CIDR %q: %v", s, err)
			continue
		}
		nets = append(nets, cidr)
	}
	return nets
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// ipAllowlist rejects requests from IPs not in the allowlist.
// If the allowlist is empty, all IPs are allowed.
//
// Two tiers:
//   - allowedCIDRs is the baseline and applies to every request
//     (including anonymous probes to /health).
//   - allowedGuestCIDRs, when set, widens the gate but ONLY for
//     requests that carry a bearer token. The token itself is
//     validated later by auth()/authSDK() — here we just verify a
//     bearer is present so an unauthenticated scan can't walk the
//     agent. This lets a host keep "owner traffic on LAN only"
//     (small allowedCIDRs) while still permitting guests who arrive
//     over relay, Tailscale, or Cloudflare tunnel (broad
//     allowedGuestCIDRs, typically 0.0.0.0/0). Loopback is always
//     admitted so relay/cloudflared sidecars terminating on 127.0.0.1
//     never trip the gate.
func (s *HTTPServer) ipAllowlist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.allowedCIDRs) == 0 && len(s.allowedGuestCIDRs) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r)
		// Loopback always admitted — relay tunnel client and cloudflared
		// both deliver proxied requests over 127.0.0.1/::1, and blocking
		// those would defeat the point of having tunnels.
		if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
			next.ServeHTTP(w, r)
			return
		}
		if len(s.allowedCIDRs) > 0 && ipMatchesCIDRs(ip, s.allowedCIDRs) {
			next.ServeHTTP(w, r)
			return
		}
		if len(s.allowedGuestCIDRs) > 0 && ipMatchesCIDRs(ip, s.allowedGuestCIDRs) {
			// Widened gate applies only to bearer-carrying requests.
			// The bearer is NOT validated here — auth()/authSDK() does
			// that. We just need proof the caller is claiming an
			// identity, which turns this from "public ingress" into
			// "authenticated ingress."
			if hasBearer(r) {
				next.ServeHTTP(w, r)
				return
			}
		}
		log.Printf("[IP] %s %s — blocked IP %s (not in allowlist)", r.Method, r.URL.Path, ip)
		jsonError(w, http.StatusForbidden, "IP not allowed")
	})
}

// hasBearer reports whether the request carries an Authorization
// header with a Bearer scheme. Does not validate the token.
func hasBearer(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if h == "" {
		return false
	}
	return strings.HasPrefix(h, "Bearer ") || strings.HasPrefix(h, "bearer ")
}

// auth is for full-access endpoints (tasks, exec, agent commands, vault, etc.).
// Accepts the agent's own token and CLI session tokens. Rejects SDK tokens.
// authOrLocalhost allows requests from localhost without auth (for Claude Code tasks running locally).
func (s *HTTPServer) authOrLocalhost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow localhost without auth
		remoteIP := r.RemoteAddr
		if idx := strings.LastIndex(remoteIP, ":"); idx >= 0 {
			remoteIP = remoteIP[:idx]
		}
		remoteIP = strings.Trim(remoteIP, "[]")
		if remoteIP == "127.0.0.1" || remoteIP == "::1" || remoteIP == "localhost" {
			next(w, r)
			return
		}
		// Otherwise require auth
		s.auth(next)(w, r)
	}
}

// Guest-path allow-lists live in guest_scope.go, keyed off the grant's
// `scope` field so a feedback-only end-user cannot reach the
// tasks/vibing/dev/projects/builds surface that a "full" teammate can.
// Per-scope prefixes + helpers: guest_scope.go::isGuestAllowedPathForScope.

func (s *HTTPServer) isApprovedGuest(userID string) bool {
	s.guestUserIDsMu.RLock()
	defer s.guestUserIDsMu.RUnlock()
	for _, gid := range s.guestUserIDs {
		if gid == userID {
			return true
		}
	}
	return false
}

// refreshGuestList periodically fetches the approved guest list and configs from Convex.
// Uses a short interval (10s) so host-side revocations propagate quickly —
// combined with per-request cache TTL (see cachedTokenInfo) the end-to-end
// cutoff is a handful of seconds even if the mobile app never talks to this agent.
func (s *HTTPServer) refreshGuestList(ctx context.Context) {
	prevGuests := map[string]bool{}
	fetchOnce := func() {
		if ids, err := FetchGuestUserIds(s.convexURL, s.token, s.deviceID); err == nil {
			s.guestUserIDsMu.Lock()
			s.guestUserIDs = ids
			s.guestUserIDsMu.Unlock()
			if len(ids) > 0 {
				log.Printf("[GUESTS] Loaded %d approved guest(s)", len(ids))
			}
			// Detect removals and flush affected token-cache entries so sessions
			// already in flight get kicked on the next request.
			newSet := map[string]bool{}
			for _, id := range ids {
				newSet[id] = true
			}
			for prev := range prevGuests {
				if !newSet[prev] {
					s.tokenCache.Range(func(key, value interface{}) bool {
						info := value.(*cachedTokenInfo)
						if info.userID == prev {
							s.tokenCache.Delete(key)
						}
						return true
					})
				}
			}
			prevGuests = newSet
		}
		if s.guestConfigMgr != nil {
			if cfgs, err := FetchGuestConfigs(s.convexURL, s.token); err == nil {
				s.guestConfigMgr.UpdateConfigs(cfgs)
			}
		}
	}
	fetchOnce()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// Flush usage less often than the guest-list check — usage is write-heavy.
	usageTicker := time.NewTicker(60 * time.Second)
	defer usageTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetchOnce()
		case <-usageTicker.C:
			if s.guestConfigMgr != nil {
				s.guestConfigMgr.FlushUsage(s.convexURL, s.token)
			}
		}
	}
}

// allowGuest checks if a non-owner userId is an approved guest and the path is
// allowed under this guest's scope. Returns true if the request was handled
// (allowed or rejected), false if the token is not a guest and the caller
// should fall through.
//
// For feedback-only guests we also stamp `X-Yaver-GuestScope: feedback-only`
// on the forwarded request so downstream handlers (notably /info and any
// task-spawn path) can apply the extra redaction + force-containerize rules
// without a second manager lookup.
func (s *HTTPServer) allowGuest(w http.ResponseWriter, r *http.Request, uid string, next http.HandlerFunc) bool {
	if !s.isApprovedGuest(uid) {
		return false
	}
	scope := guestScopeDefaultLegacy
	if s.guestConfigMgr != nil {
		scope = s.guestConfigMgr.GetScope(uid)
	}
	if !isGuestAllowedPathForScope(r.URL.Path, scope) {
		jsonError(w, http.StatusForbidden, "guests cannot access this endpoint")
		return true
	}
	// Check guest config limits (usage mode, daily limit, schedule)
	if s.guestConfigMgr != nil {
		if denied := s.guestConfigMgr.CheckAccess(uid); denied != nil {
			jsonError(w, http.StatusForbidden, denied.Reason)
			return true
		}
	}
	// CRITICAL: strip every X-Yaver-Guest* header the caller might have
	// pre-stamped on their inbound request. Downstream handlers
	// (ops_execution_plan, /info redaction, project gate) trust these
	// headers as if they were set by us, so a guest spoofing
	// X-Yaver-GuestAllowedProjects (H-13) or X-Yaver-GuestScope (M-1)
	// can broaden their own scope. Always re-stamp from server-resolved
	// state below — never honor inbound values.
	stripGuestRequestHeaders(r)
	r.Header.Set("X-Yaver-Guest", "true")
	r.Header.Set("X-Yaver-GuestUserID", uid)
	r.Header.Set("X-Yaver-GuestScope", scope)
	if s.guestConfigMgr != nil {
		if allowed := cleanProjectList(s.guestConfigMgr.AllowedProjects(uid)); len(allowed) > 0 {
			r.Header.Set("X-Yaver-GuestAllowedProjects", strings.Join(allowed, ","))
		}
	}
	next(w, r)
	return true
}

// stripGuestRequestHeaders deletes every X-Yaver-Guest* header from an
// inbound request before downstream code reads them. The middleware
// will re-stamp the legitimate values from server-resolved state.
// Listed explicitly (rather than wildcard-deleting) so a future header
// not yet in the audit doesn't silently bypass.
func stripGuestRequestHeaders(r *http.Request) {
	for _, name := range []string{
		"X-Yaver-Guest",
		"X-Yaver-GuestUserID",
		"X-Yaver-GuestScope",
		"X-Yaver-GuestAllowedProjects",
		"X-Yaver-GuestAllowedRunners",
		"X-Yaver-SdkAllowedRunners",
		"X-Yaver-HostShareAllowedRunners",
		"X-Yaver-AllowedTools",
		"X-Yaver-RedactPII",
		"X-Yaver-Support",
		"X-Yaver-Proxied-By",
		"X-Yaver-Proxied-Tool",
	} {
		r.Header.Del(name)
	}
}

func (s *HTTPServer) resolveHostShareAccess(guestUserID string) *HostShareAccessInfo {
	token := s.currentAuthToken()
	if strings.TrimSpace(guestUserID) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(s.convexURL) == "" {
		return nil
	}
	access, err := FetchHostShareAccess(s.convexURL, token, guestUserID, s.deviceID)
	if err != nil {
		log.Printf("[HOST-SHARE] access lookup failed for %s: %v", guestUserID, err)
		return nil
	}
	return access
}

func (s *HTTPServer) resolveHostSharePeerAccess(hostUserID string) *HostShareAccessInfo {
	token := s.currentAuthToken()
	if strings.TrimSpace(hostUserID) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(s.convexURL) == "" {
		return nil
	}
	access, err := FetchHostSharePeerAccess(s.convexURL, token, hostUserID, s.deviceID)
	if err != nil {
		log.Printf("[HOST-SHARE] peer access lookup failed for %s: %v", hostUserID, err)
		return nil
	}
	return access
}

func (s *HTTPServer) currentAuthToken() string {
	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		if token := strings.TrimSpace(cfg.AuthToken); token != "" {
			return token
		}
	}
	return strings.TrimSpace(s.token)
}

func (s *HTTPServer) allowHostShare(w http.ResponseWriter, r *http.Request, uid string, access *HostShareAccessInfo, next http.HandlerFunc) bool {
	if access == nil {
		return false
	}
	if !isHostShareAllowedPath(r.URL.Path, access) {
		jsonError(w, http.StatusForbidden, "host-share session cannot access this endpoint")
		return true
	}
	r.Header.Set("X-Yaver-HostShare", "true")
	r.Header.Set("X-Yaver-HostShareGuestUserID", uid)
	r.Header.Set("X-Yaver-HostShareSessionID", access.SessionID)
	r.Header.Set("X-Yaver-HostShareGuestDeviceID", access.GuestDeviceID)
	r.Header.Set("X-Yaver-HostShareToolingPreset", access.Policy.ToolingPreset)
	r.Header.Set("X-Yaver-HostShareResourcePreset", access.Policy.ResourcePreset)
	r.Header.Set("X-Yaver-HostShareAllowedProjects", strings.Join(access.Policy.AllowedProjects, ","))
	r.Header.Set("X-Yaver-HostShareAllowedRunners", strings.Join(access.Policy.AllowedRunners, ","))
	r.Header.Set("X-Yaver-HostShareAllowInfra", fmt.Sprintf("%t", access.Policy.AllowInfra))
	r.Header.Set("X-Yaver-HostShareAllowTerminal", fmt.Sprintf("%t", access.Policy.AllowTerminal))
	r.Header.Set("X-Yaver-HostShareAllowTunnel", fmt.Sprintf("%t", access.Policy.AllowTunnel))
	r.Header.Set("X-Yaver-HostShareUseHostAgentTools", fmt.Sprintf("%t", access.Policy.UseHostAgentTools))
	r.Header.Set("X-Yaver-HostShareUseHostInfra", fmt.Sprintf("%t", access.Policy.UseHostInfra))
	next(w, r)
	return true
}

func (s *HTTPServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionToken := r.URL.Query().Get("browser_session"); sessionToken != "" {
			if s.validateBrowserSession(sessionToken, r.URL.Path) {
				next(w, r)
				return
			}
			jsonError(w, http.StatusUnauthorized, "invalid browser session")
			return
		}

		// WebSocket clients (mobile Terminal, browser) cannot set custom
		// headers, so they pass the bearer as ?token=<jwt>. Promote it
		// into the Authorization header before the regular Bearer path
		// so every downstream check (owner / support / paired / Convex)
		// works unchanged.
		if r.Header.Get("Authorization") == "" {
			if qt := r.URL.Query().Get("token"); qt != "" {
				r.Header.Set("Authorization", "Bearer "+qt)
			}
		}

		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			log.Printf("[AUTH] %s %s — missing Authorization header", r.Method, r.URL.Path)
			jsonError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Fast path: exact match with the agent's own token
		if secretEqual(token, s.token) {
			next(w, r)
			return
		}

		// Fast path: active support-session bearer. Narrow allowlist,
		// no Convex round-trip, revoked instantly by `yaver support
		// stop` / TTL expiry. A support token that misses the
		// allowlist falls through to the normal owner/guest path so
		// the caller gets the same 401/403 any unrecognized token
		// does — we don't want to special-case the rejection.
		if strings.HasPrefix(token, "yv_supp_") && supportTokenValidFor(token, r.URL.Path) {
			r.Header.Set("X-Yaver-Support", "true")
			next(w, r)
			return
		}

		// Second fast path: paired tokens (multi-user pairing).
		// A startup that bought one Mac Studio can have 5 phones
		// paired with it — each gets the same scope as the primary
		// owner WITHOUT depending on Convex round-tripping. This
		// lives *before* the Convex check so lag / outage on the
		// auth broker can't lock out paired users.
		if IsPairedToken(token) {
			TouchPairedToken(token)
			// If multi-user mode is on, resolve the paired token
			// to its own userId via Convex (best-effort; the cache
			// makes this cheap after the first hit) so the
			// MultiUserManager routes the request to an isolated
			// workspace. Otherwise just hand the request through
			// as the owner.
			if cached, ok := s.tokenCache.Load(token); ok {
				info := cached.(*cachedTokenInfo)
				r = withPairedUser(r, info.userID)
				next(w, r)
				return
			}
			// First paired request — hydrate the cache in the
			// background, never block the request. If Convex is
			// reachable we'll know which isolated user slot to
			// route to on the next call; for this one we treat
			// the paired token as owner-equivalent.
			go func() {
				if uid, err := ValidateTokenUser(s.convexURL, token); err == nil {
					s.tokenCache.Store(token, &cachedTokenInfo{userID: uid, isSdk: false})
				}
			}()
			next(w, r)
			return
		}

		// Check token cache. Guest entries have a TTL so host-side
		// revocations are honored within guestTokenCacheTTL even if
		// the revoke doesn't travel through this agent's own API.
		if cached, ok := s.tokenCache.Load(token); ok {
			info := cached.(*cachedTokenInfo)
			if info.isSdk {
				jsonError(w, http.StatusForbidden, "SDK tokens cannot access this endpoint")
				return
			}
			stale := info.userID != s.ownerUserID &&
				!info.storedAt.IsZero() &&
				time.Since(info.storedAt) > guestTokenCacheTTL
			if stale {
				s.tokenCache.Delete(token)
			} else {
				if info.userID == s.ownerUserID {
					next(w, r)
					return
				}
				if s.allowGuest(w, r, info.userID, next) {
					return
				}
				if s.allowHostShare(w, r, info.userID, info.hostShare, next) {
					return
				}
				jsonError(w, http.StatusForbidden, "token belongs to a different user")
				return
			}
		}

		// Owner's own current token — recognize it WITHOUT a Convex
		// round-trip, including when the in-memory fast path (s.token)
		// above has drifted from the on-disk token across a restart /
		// self-upgrade / rotation window. tokenIsOwner re-reads disk, so
		// the owner's local operations (builds, exec/build-log streaming,
		// tasks) work online OR offline rather than bouncing off a Convex
		// validation. Sits after the cache check so guest tokens still
		// resolve through their cached entry / host-share path first.
		if s.tokenIsOwner(token) {
			next(w, r)
			return
		}

		// Validate session token via Convex
		log.Printf("[AUTH] %s %s — validating token against Convex...", r.Method, r.URL.Path)
		uid, err := ValidateTokenUser(s.convexURL, token)
		if err != nil {
			log.Printf("[AUTH] %s %s — token validation failed: %v", r.Method, r.URL.Path, err)
			jsonError(w, http.StatusForbidden, "invalid token")
			return
		}
		info := &cachedTokenInfo{userID: uid, isSdk: false, storedAt: time.Now()}
		if uid != s.ownerUserID {
			info.hostShare = s.resolveHostShareAccess(uid)
			if info.hostShare == nil {
				info.hostShare = s.resolveHostSharePeerAccess(uid)
			}
		}
		cacheToken := true
		if uid != s.ownerUserID && info.hostShare == nil {
			// Do not cache a negative non-owner authorization result.
			// A guest can be granted access moments later via a freshly
			// accepted host-share invite, and caching the denial makes the
			// first post-join request fail until the guest token TTL elapses.
			cacheToken = false
		}
		if cacheToken {
			s.tokenCache.Store(token, info)
		}

		if uid != s.ownerUserID {
			if s.allowGuest(w, r, uid, next) {
				return
			}
			if s.allowHostShare(w, r, uid, info.hostShare, next) {
				return
			}
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}
		next(w, r)
	}
}

// withPairedUser attaches the resolved userId for a paired
// token to the request context so downstream handlers
// (tasks.go, exec.go, etc) can route to an isolated multi-user
// workspace via s.multiUserMgr.GetOrCreateSession(userID).
func withPairedUser(r *http.Request, userID string) *http.Request {
	if userID == "" {
		return r
	}
	ctx := r.Context()
	ctx = contextWithPairedUser(ctx, userID)
	return r.WithContext(ctx)
}

// authSDK is for SDK-accessible endpoints (feedback, blackbox, voice, builds).
// Accepts all token types: agent's own, CLI session, and SDK tokens (with scope check).
// wsQueryToken lets browser WebSocket clients authenticate via a query
// param, since the browser WebSocket constructor cannot set request
// headers. If no Authorization header is present, it promotes
// ?access_token=<bearer> (or ?token=) into the header so the downstream
// auth middleware validates it unchanged. A real header always wins.
func (s *HTTPServer) wsQueryToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			tok := r.URL.Query().Get("access_token")
			if tok == "" {
				tok = r.URL.Query().Get("token")
			}
			if tok != "" {
				r.Header.Set("Authorization", "Bearer "+tok)
			}
		}
		next(w, r)
	}
}

// tokenIsOwner reports whether token is the agent owner's own session token.
// It checks the in-memory copy (s.token) first, then re-reads config from
// disk. The disk fallback is load-bearing: on a freshly (re)started or
// self-upgraded daemon the in-memory s.token can lag the on-disk token (boot
// before the heartbeat settles, a rotation, or an out-of-process `yaver auth`
// write), while the CLI on this same machine sends the current on-disk token.
// Recognizing the owner here — with no network — is what keeps local
// operations (native builds, exec/log streaming) working online OR offline
// instead of bouncing the owner's own token off a Convex round-trip that the
// in-memory fast path just missed. Empty tokens never match (secretEqual
// guards it), so an unauthenticated daemon grants nothing.
func (s *HTTPServer) tokenIsOwner(token string) bool {
	if secretEqual(token, s.token) {
		return true
	}
	if cfg, err := LoadConfig(); err == nil && secretEqual(token, cfg.AuthToken) {
		return true
	}
	return false
}

func (s *HTTPServer) authSDK(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			jsonError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Fast path: agent's own token (full access)
		if secretEqual(token, s.token) {
			next(w, r)
			return
		}

		// Multi-user paired tokens — same treatment as the
		// full auth() middleware. Paired tokens get SDK-scope
		// access automatically since they come from a user
		// who's signed in with a full session on another
		// device. Lives before the Convex / SDK checks so a
		// paired phone can drive feedback / blackbox /
		// voice / builds without round-tripping the auth
		// broker.
		if IsPairedToken(token) {
			TouchPairedToken(token)
			if cached, ok := s.tokenCache.Load(token); ok {
				info := cached.(*cachedTokenInfo)
				r = withPairedUser(r, info.userID)
			}
			next(w, r)
			return
		}

		// Check cache
		if cached, ok := s.tokenCache.Load(token); ok {
			info := cached.(*cachedTokenInfo)
			if info.userID != s.ownerUserID {
				jsonError(w, http.StatusForbidden, "token belongs to a different user")
				return
			}
			if info.isSdk {
				// Check scope
				if !pathAllowedByScopes(r.URL.Path, info.scopes) {
					jsonError(w, http.StatusForbidden, "SDK token scope does not allow this endpoint")
					return
				}
				// Check IP binding
				if len(info.allowedCIDRs) > 0 {
					cidrs := parseCIDRs(info.allowedCIDRs)
					if !ipMatchesCIDRs(clientIP(r), cidrs) {
						jsonError(w, http.StatusForbidden, "SDK token not allowed from this IP")
						return
					}
				}
				// Track new device IPs
				s.trackNewIP(token, r)
				if !s.applyDelegatedGuestSDKHeaders(w, r, info) {
					return
				}
			}
			next(w, r)
			return
		}

		// Owner's own current token — recognize it WITHOUT a Convex
		// round-trip. The in-memory fast path (s.token) above can miss
		// when s.token has drifted from the on-disk token across a
		// restart / self-upgrade / rotation window, while the CLI on
		// this same machine sends the current on-disk token. Bouncing
		// the owner's own token off Convex in that window is exactly
		// what made offline-and-even-online local builds fail with
		// "invalid token". tokenIsOwner re-reads the on-disk token, so
		// this holds with no network at all. Sits after the cache check
		// so guests still hit their cached entry first.
		if s.tokenIsOwner(token) {
			next(w, r)
			return
		}

		// Try session token first
		uid, err := ValidateTokenUser(s.convexURL, token)
		if err == nil {
			s.tokenCache.Store(token, &cachedTokenInfo{userID: uid, isSdk: false})
			if uid != s.ownerUserID {
				jsonError(w, http.StatusForbidden, "token belongs to a different user")
				return
			}
			next(w, r)
			return
		}

		// Try SDK token
		sdkInfo, sdkErr := ValidateSdkTokenFull(s.convexURL, token)
		if sdkErr != nil {
			log.Printf("[AUTH] %s %s — all token validation failed", r.Method, r.URL.Path)
			jsonError(w, http.StatusForbidden, "invalid token")
			return
		}

		info := &cachedTokenInfo{
			userID:               sdkInfo.UserID,
			isSdk:                true,
			scopes:               sdkInfo.Scopes,
			allowedCIDRs:         sdkInfo.AllowedCIDRs,
			delegatedGuestUserID: sdkInfo.DelegatedGuestUserID,
			delegatedGuestScope:  sdkInfo.DelegatedGuestScope,
			sourceSurface:        sdkInfo.SourceSurface,
			targetDeviceID:       sdkInfo.TargetDeviceID,
			allowedProjects:      sdkInfo.AllowedProjects,
		}
		s.tokenCache.Store(token, info)

		if sdkInfo.UserID != s.ownerUserID {
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}

		// Check scope
		if !pathAllowedByScopes(r.URL.Path, sdkInfo.Scopes) {
			jsonError(w, http.StatusForbidden, "SDK token scope does not allow this endpoint")
			return
		}
		stampSdkRunnerScope(r, sdkInfo.Scopes)
		stampMcpToolScope(r, sdkInfo.Scopes)
		stampSdkDataPolicy(r, sdkInfo.Scopes)

		// Check IP binding
		if len(sdkInfo.AllowedCIDRs) > 0 {
			cidrs := parseCIDRs(sdkInfo.AllowedCIDRs)
			if !ipMatchesCIDRs(clientIP(r), cidrs) {
				jsonError(w, http.StatusForbidden, "SDK token not allowed from this IP")
				return
			}
		}

		// Track new device IPs
		s.trackNewIP(token, r)
		if !s.applyDelegatedGuestSDKHeaders(w, r, info) {
			return
		}

		next(w, r)
	}
}

// authSDKOrGuest is for endpoints that must remain reachable by SDK tokens
// but should also allow full-scope guest session tokens. Guests still go
// through the regular allowGuest scope gate; SDK tokens keep their existing
// scope/IP checks.
func (s *HTTPServer) authSDKOrGuest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			jsonError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		if secretEqual(token, s.token) {
			next(w, r)
			return
		}

		if IsPairedToken(token) {
			TouchPairedToken(token)
			if cached, ok := s.tokenCache.Load(token); ok {
				info := cached.(*cachedTokenInfo)
				r = withPairedUser(r, info.userID)
			}
			next(w, r)
			return
		}

		if cached, ok := s.tokenCache.Load(token); ok {
			info := cached.(*cachedTokenInfo)
			if info.isSdk {
				if !pathAllowedByScopes(r.URL.Path, info.scopes) {
					jsonError(w, http.StatusForbidden, "SDK token scope does not allow this endpoint")
					return
				}
				if len(info.allowedCIDRs) > 0 {
					cidrs := parseCIDRs(info.allowedCIDRs)
					if !ipMatchesCIDRs(clientIP(r), cidrs) {
						jsonError(w, http.StatusForbidden, "SDK token not allowed from this IP")
						return
					}
				}
				s.trackNewIP(token, r)
				if !s.applyDelegatedGuestSDKHeaders(w, r, info) {
					return
				}
				next(w, r)
				return
			}
			if info.userID == s.ownerUserID {
				next(w, r)
				return
			}
			if s.allowGuest(w, r, info.userID, next) {
				return
			}
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}

		uid, err := ValidateTokenUser(s.convexURL, token)
		if err == nil {
			s.tokenCache.Store(token, &cachedTokenInfo{userID: uid, isSdk: false, storedAt: time.Now()})
			if uid == s.ownerUserID {
				next(w, r)
				return
			}
			if s.allowGuest(w, r, uid, next) {
				return
			}
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}

		sdkInfo, sdkErr := ValidateSdkTokenFull(s.convexURL, token)
		if sdkErr != nil {
			log.Printf("[AUTH] %s %s — all token validation failed", r.Method, r.URL.Path)
			jsonError(w, http.StatusForbidden, "invalid token")
			return
		}

		info := &cachedTokenInfo{
			userID:               sdkInfo.UserID,
			isSdk:                true,
			scopes:               sdkInfo.Scopes,
			allowedCIDRs:         sdkInfo.AllowedCIDRs,
			delegatedGuestUserID: sdkInfo.DelegatedGuestUserID,
			delegatedGuestScope:  sdkInfo.DelegatedGuestScope,
			sourceSurface:        sdkInfo.SourceSurface,
			targetDeviceID:       sdkInfo.TargetDeviceID,
			allowedProjects:      sdkInfo.AllowedProjects,
		}
		s.tokenCache.Store(token, info)

		if info.userID != s.ownerUserID {
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}
		if !pathAllowedByScopes(r.URL.Path, info.scopes) {
			jsonError(w, http.StatusForbidden, "SDK token scope does not allow this endpoint")
			return
		}
		stampSdkRunnerScope(r, info.scopes)
		stampMcpToolScope(r, info.scopes)
		stampSdkDataPolicy(r, info.scopes)
		if len(info.allowedCIDRs) > 0 {
			cidrs := parseCIDRs(info.allowedCIDRs)
			if !ipMatchesCIDRs(clientIP(r), cidrs) {
				jsonError(w, http.StatusForbidden, "SDK token not allowed from this IP")
				return
			}
		}
		s.trackNewIP(token, r)
		if !s.applyDelegatedGuestSDKHeaders(w, r, info) {
			return
		}
		next(w, r)
	}
}

// trackNewIP records the first time an SDK token is used from a new IP.
func (s *HTTPServer) trackNewIP(token string, r *http.Request) {
	ip := clientIP(r)
	prefix := token
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	seenKey := prefix + "_" + ip
	if _, loaded := s.seenIPs.LoadOrStore(seenKey, true); !loaded {
		log.Printf("[SECURITY] New IP %s for SDK token %s...", ip, prefix)
		go ReportSecurityEvent(s.convexURL, s.token, "new_ip", map[string]interface{}{
			"ip":          ip,
			"tokenPrefix": prefix,
			"path":        r.URL.Path,
		})
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Yaver-Caller, X-Relay-Password")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	lifecycle := s.lifecycleInfo()
	resp := map[string]interface{}{
		"ok":             true,
		"hostname":       s.hostname,
		"version":        version,
		"lifecycleState": lifecycle.State,
		"lifecycle":      lifecycle,
	}
	if s.tlsFingerprint != "" {
		resp["tlsFingerprint"] = s.tlsFingerprint
		resp["tlsPort"] = s.tlsPort
	}
	if s.authExpired.Load() {
		resp["authExpired"] = true
	}
	jsonReply(w, http.StatusOK, resp)
}

// handleSelfCheck exposes the TaskSupervisor registry: every in-process
// ticker that registered with SupervisedGo, its last tick, last error,
// health state, and any stalled-for-longer-than-threshold flags.
//
// Returns 200 even when unhealthy — the caller is expected to check
// `unhealthy` / individual task states. A smoke script can `jq` the
// payload and exit-code on findings.
func (s *HTTPServer) handleSelfCheck(w http.ResponseWriter, r *http.Request) {
	sup := supervisor()
	snap := sup.Snapshot()
	unhealthy := 0
	for _, t := range snap {
		if t.HealthState == "stalled" || t.HealthState == "error" {
			unhealthy++
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"tasks":     snap,
		"unhealthy": unhealthy,
		"count":     len(snap),
	})
}

func (s *HTTPServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	lifecycle := s.lifecycleInfo()
	// Surface the per-device runner pref from Convex when set, so
	// `yaver primary status` (which reads info.runner.id) reflects the
	// dashboard's "primary runner" choice without restarting the agent.
	// Falls through to tm.runner — which itself was resolved from the
	// global userSettings.runnerId at boot — when no per-device pref
	// exists.
	runnerID := s.taskMgr.runner.RunnerID
	runnerName := s.taskMgr.runner.Name
	runnerModel := s.taskMgr.runner.Model
	runnerMode := s.taskMgr.runner.Mode
	runnerProvider := ""
	if pref := resolvePrimaryRunnerPrefForSelf(r.Context(), s); pref.RunnerID != "" {
		runnerID = pref.RunnerID
		if cfg, ok := builtinRunners[runnerID]; ok {
			runnerName = cfg.Name
		} else {
			runnerName = runnerID
		}
		if pref.Model != "" {
			runnerModel = pref.Model
		}
		if pref.Mode != "" {
			runnerMode = pref.Mode
		}
		if pref.Provider != "" {
			runnerProvider = pref.Provider
		}
	}
	// Agent's runtime OS user — single source of truth for "who do I
	// SSH as to land in the same authorized_keys this agent writes to?"
	// Resolved from /etc/passwd via os/user; falls back to a numeric
	// uid string if the lookup fails (rare, but possible inside very
	// stripped containers). Home dir surfaced too so the CLI can
	// double-check before pushing keys.
	osUserName, osHome := agentRuntimeUserInfo()
	info := map[string]interface{}{
		"ok":             true,
		"hostname":       hostname,
		"version":        version,
		"workDir":        s.taskMgr.workDir,
		"hwid":           HardwareID(),
		"hardware":       cachedHardwareProfile(),
		"lifecycleState": lifecycle.State,
		"lifecycle":      lifecycle,
		"osUser":         osUserName,
		"homeDir":        osHome,
		"runner": map[string]interface{}{
			"id":       runnerID,
			"name":     runnerName,
			"model":    runnerModel,
			"mode":     runnerMode,
			"provider": runnerProvider,
		},
	}

	// Project metadata
	project := DetectProjectInfo(s.taskMgr.workDir)
	info["project"] = project

	// gh + glab CLI posture. Mobile + web surfaces use this to render
	// "GitHub CLI ready" / "GitLab CLI not authenticated" indicators
	// without each surface re-probing. Cached at boot + refreshed by
	// `yaver install` after a successful gh/glab install.
	info["gitProviderCLIs"] = DetectGitProviderCLIs()

	// Dev server status (for hot-reload awareness)
	if s.devServerMgr != nil {
		if devStatus := s.devServerMgr.Status(); devStatus != nil {
			info["devServer"] = devStatus
		}
	}
	info["sandbox"] = s.sandboxSummary()

	// Todo list count + stats
	if s.todolistMgr != nil {
		items := s.todolistMgr.ListItems()
		pending, implementing, done, failed := 0, 0, 0, 0
		for _, item := range items {
			switch item.Status {
			case TodoStatusPending:
				pending++
			case TodoStatusImplementing:
				implementing++
			case TodoStatusDone:
				done++
			case TodoStatusFailed:
				failed++
			}
		}
		info["todoCount"] = pending
		info["todoTotal"] = len(items)
		info["todoDone"] = done
		info["todoFailed"] = failed
		info["todoImplementing"] = implementing
		info["autoConsume"] = s.todolistMgr.IsAutoConsume()
	}
	if lifecycle.State == AgentLifecycleAuthExpired {
		info["authExpired"] = true
	}

	// Session task stats
	if s.taskMgr != nil {
		tasks := s.taskMgr.ListTasks()
		taskTotal := len(tasks)
		taskDone := 0
		taskRunning := 0
		taskFailed := 0
		for _, t := range tasks {
			switch t.Status {
			case TaskStatusFinished:
				taskDone++
			case TaskStatusRunning:
				taskRunning++
			case TaskStatusFailed:
				taskFailed++
			}
		}
		info["taskStats"] = map[string]int{
			"total":   taskTotal,
			"done":    taskDone,
			"running": taskRunning,
			"failed":  taskFailed,
		}
	}

	// Auto-start / systemd status
	autoStart := map[string]interface{}{"configured": false}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if isDarwinLaunchDaemonInstalled() {
			autoStart["configured"] = true
			autoStart["type"] = "launchd-daemon"
			autoStart["scope"] = "boot"
		} else if _, err := os.Stat(filepath.Join(home, darwinLaunchAgentPath)); err == nil {
			autoStart["configured"] = true
			autoStart["type"] = "launchd-agent"
			autoStart["scope"] = "login"
		}
	case "linux":
		if isWSL() {
			if isWSLWindowsScheduledTaskInstalled() {
				autoStart["configured"] = true
				autoStart["type"] = "wsl-schtasks"
				autoStart["scope"] = "windows-login"
			} else if isWSLAutoStartInstalled() {
				autoStart["configured"] = true
				autoStart["type"] = "wsl-startup"
				autoStart["scope"] = "shell-login"
			}
		} else if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "yaver.service")); err == nil {
			autoStart["configured"] = true
			autoStart["type"] = "systemd"
			autoStart["scope"] = "boot"
		}
	default:
		// Windows implementation reports scheduled task elsewhere via runtime defaults.
	}
	info["autoStart"] = autoStart

	// Non-owner callers (any guest tier, host-share peer, SDK token,
	// support session) get a redacted /info. We still need to answer
	// "is the agent alive + which feedback endpoints are available"
	// (the SDK probes this on startup), but we strip everything that leaks
	// the dev-machine's project layout, task activity, auto-start config,
	// hardware id, workDir, or hostname. A malicious end-user of a host's
	// app should not learn what projects the dev is working on or how
	// many tasks are in flight.
	//
	// Pre-fix (H-17): only feedback-only guests got the redaction. Full
	// guests, host-share peers, SDK callers all saw the absolute workDir
	// (which contains /Users/<owner-username>/), the hostname, the hwid,
	// the runner config, project list, and task stats. Privacy contract
	// in CLAUDE.md forbids absolute paths and usernames flowing off-machine.
	if isNonOwnerInfoCaller(r) {
		redacted := map[string]interface{}{
			"ok":      info["ok"],
			"version": info["version"],
		}
		// Sandbox flag is safe — guest tasks get force-containerized,
		// so the SDK can show "running sandboxed" without surfacing host config.
		if sb, ok := info["sandbox"].(map[string]interface{}); ok {
			redacted["sandbox"] = map[string]interface{}{
				"available": sb["available"],
			}
		}
		jsonReply(w, http.StatusOK, redacted)
		return
	}

	jsonReply(w, http.StatusOK, info)
}

// isNonOwnerInfoCaller reports whether the request is from anyone other
// than the agent's owner. Used to gate /info redaction. Returns true for
// any guest scope (feedback-only / full / deploy / sdk-project), any
// support-session bearer, and any host-share peer.
func isNonOwnerInfoCaller(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Header.Get("X-Yaver-Guest") == "true" {
		return true
	}
	if r.Header.Get("X-Yaver-Support") == "true" {
		return true
	}
	if r.Header.Get("X-Yaver-HostShare") == "true" {
		return true
	}
	return false
}

// handleHardwareRefresh re-runs hardware detection (bypassing the in-process
// cache + the 24h heartbeat gate) and kicks the heartbeat loop so the fresh
// profile lands in Convex within ~1s. Mobile + web call this when a device's
// hardwareProfile row is missing or stale (e.g. the agent was upgraded from a
// pre-detection build) so users don't have to wait the regular 24h refresh.
// POST /hardware/refresh
func (s *HTTPServer) handleHardwareRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	profile := forceRefreshHardwareProfile()
	s.TriggerHeartbeat()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"hardware": profile,
	})
}

// handleProjectsRefresh forces a re-scan of projects on the machine.
// POST /projects/refresh
func (s *HTTPServer) handleProjectsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	go discoverProjects()
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "discovery started"})
}

// handleProjects lists discovered projects on this machine.
func (s *HTTPServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	projects := listDiscoveredProjects()
	if len(projects) == 0 {
		log.Printf("[projects] Discovery cache empty; rescanning local roots now")
		discoverProjects()
		projects = listDiscoveredProjects()
	}
	type projectResp struct {
		Name           string   `json:"name"`
		Path           string   `json:"path"`
		Branch         string   `json:"branch,omitempty"`
		Framework      string   `json:"framework,omitempty"`
		ExecutionMode  string   `json:"executionMode,omitempty"`
		PrimarySurface string   `json:"primarySurface,omitempty"`
		GitRemote      string   `json:"gitRemote,omitempty"`
		Tags           []string `json:"tags,omitempty"`
		IsMonorepo     bool     `json:"isMonorepo,omitempty"`
		Subframeworks  []string `json:"subframeworks,omitempty"`
	}
	// Filter out non-deployable projects: dotfiles, editor configs, vim plugins, etc.
	// Only show things a solo dev would actually deploy: apps, websites, backends, APIs.
	skipPrefixes := []string{".", "_"}
	skipNames := map[string]bool{
		// System / OS dirs
		"node_modules": true, "vendor": true, "dist": true, "build": true,
		"Library": true, "Applications": true, "Music": true, "Movies": true,
		"Pictures": true, "Documents": true, "Public": true, "Downloads": true,
		"Desktop": true, "Trash": true,
		// Editor / shell configs
		"vim": true, "nvim": true, "neovim": true, "emacs": true,
		"tmux": true, "zsh": true, "bash": true, "fish": true,
		"oh-my-zsh": true, "powerlevel10k": true, "spacemacs": true,
		// Vim plugin managers / bundles
		"bundle": true, "plugged": true, "pack": true,
	}
	// Skip paths containing these segments (vim bundles, dotfile managers, etc.)
	skipPathSegments := []string{
		"/vim/bundle/", "/vim/plugged/", "/vim/pack/",
		"/.vim/", "/.config/nvim/", "/.tmux/", "/.oh-my-zsh/",
		"/.local/share/", "/.cache/", "/.cargo/",
		"/homebrew/", "/Cellar/", "/Caskroom/",
	}

	result := make([]projectResp, 0, len(projects))
	for _, p := range projects {
		name := filepath.Base(p.Path)
		// Skip dotfiles and config dirs
		skip := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(name, prefix) {
				skip = true
				break
			}
		}
		if skip || skipNames[name] {
			continue
		}
		// Skip paths inside editor/config directories
		for _, seg := range skipPathSegments {
			if strings.Contains(p.Path, seg) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Only show projects that look deployable
		// Check root AND immediate subdirs for build system files (monorepo support)
		hasDeployable := false
		dirsToCheck := []string{p.Path}
		if subs, err := os.ReadDir(p.Path); err == nil {
			for _, sub := range subs {
				if sub.IsDir() && !strings.HasPrefix(sub.Name(), ".") && sub.Name() != "node_modules" {
					dirsToCheck = append(dirsToCheck, filepath.Join(p.Path, sub.Name()))
				}
			}
		}
		for _, dir := range dirsToCheck {
			if fileExists(filepath.Join(dir, "package.json")) || fileExists(filepath.Join(dir, "pubspec.yaml")) ||
				fileExists(filepath.Join(dir, "go.mod")) || fileExists(filepath.Join(dir, "Cargo.toml")) ||
				fileExists(filepath.Join(dir, "Dockerfile")) || fileExists(filepath.Join(dir, "docker-compose.yml")) ||
				fileExists(filepath.Join(dir, "docker-compose.yaml")) || fileExists(filepath.Join(dir, "pyproject.toml")) ||
				fileExists(filepath.Join(dir, "requirements.txt")) || fileExists(filepath.Join(dir, "Makefile")) ||
				fileExists(filepath.Join(dir, "Package.swift")) || hasExtInDir(dir, ".xcodeproj") || isKotlinAndroidProject(dir) {
				hasDeployable = true
				break
			}
		}
		if !hasDeployable {
			continue
		}
		info := DetectProjectInfo(p.Path)
		// Derive tags from actions — covers monorepos (mobile/ + web/ + backend/)
		actions := DetectProjectActions(p.Path)
		tagSet := map[string]bool{}
		for _, a := range actions {
			if a.Framework != "" {
				tagSet[a.Framework] = true
			}
			if a.Platform != "" {
				tagSet[a.Platform] = true
			}
			// Infer high-level tags
			switch a.Type {
			case "dev-server":
				if a.Framework == "expo" || a.Framework == "flutter" {
					tagSet["mobile"] = true
				} else {
					tagSet["web"] = true
				}
			case "deploy":
				if a.Platform == "testflight" || a.Platform == "playstore" {
					tagSet["mobile"] = true
				}
			}
		}
		// Also add language-level tags
		for _, t := range DetectProjectTags(p.Path) {
			tagSet[t] = true
		}
		tags := make([]string, 0, len(tagSet))
		for t := range tagSet {
			tags = append(tags, t)
		}
		// Monorepo fallback — if the root has no single framework marker but
		// has multiple framework-bearing subdirs (carrotbet, yaver.io, etc),
		// label it "monorepo" + surface the constituent frameworks so the
		// dashboard shows MONOREPO instead of "?".
		framework := info.Framework
		isMonorepo := false
		var subframeworks []string
		if framework == "" {
			if mfw, subs := monorepoSummaryForDir(p.Path); mfw != "" {
				framework = mfw
				isMonorepo = true
				subframeworks = subs
				for _, s := range subs {
					tagSet[s] = true
				}
				tags = tags[:0]
				for t := range tagSet {
					tags = append(tags, t)
				}
			}
		}

		result = append(result, projectResp{
			Name:           name,
			Path:           p.Path,
			Branch:         p.Branch,
			Framework:      framework,
			ExecutionMode:  string(executionModeForFramework(framework)),
			PrimarySurface: primarySurfaceForFramework(framework),
			GitRemote:      info.GitRemote,
			Tags:           tags,
			IsMonorepo:     isMonorepo,
			Subframeworks:  subframeworks,
		})
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"projects":   result,
		"currentDir": s.taskMgr.workDir,
		"discovery":  currentProjectDiscoverySnapshot(),
	})
}

// handleProjectSwitch changes the agent's working directory + optionally starts dev server.
func (s *HTTPServer) handleProjectSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Query    string `json:"query"`    // fuzzy project name
		Path     string `json:"path"`     // or explicit path
		StartDev bool   `json:"startDev"` // also start dev server
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	projectPath := req.Path
	if projectPath == "" && req.Query != "" {
		found, err := findProject(req.Query)
		if err != nil {
			jsonError(w, http.StatusNotFound, fmt.Sprintf("project not found: %v", err))
			return
		}
		projectPath = found
	}

	if projectPath == "" {
		jsonError(w, http.StatusBadRequest, "query or path required")
		return
	}

	// Switch workdir
	s.taskMgr.mu.Lock()
	s.taskMgr.workDir = projectPath
	s.taskMgr.mu.Unlock()
	log.Printf("[projects] Switched to %s", projectPath)

	resp := map[string]interface{}{
		"ok":      true,
		"path":    projectPath,
		"project": DetectProjectInfo(projectPath),
	}

	// Optionally start dev server
	if req.StartDev && s.devServerMgr != nil {
		if status := s.devServerMgr.Status(); status != nil {
			// Already running — stop first
			s.devServerMgr.Stop()
		}
		framework := DetectProjectInfo(projectPath).Framework
		if err := s.devServerMgr.Start(framework, projectPath, "", 0, DevServerTarget{}); err != nil {
			resp["devServerError"] = err.Error()
		} else {
			resp["devServer"] = s.devServerMgr.Status()
		}
	}

	jsonReply(w, http.StatusOK, resp)
}

// handleProjectActions returns available actions for a project (deploy targets, dev servers, builds).
func (s *HTTPServer) handleProjectActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	path := r.URL.Query().Get("path")
	query := r.URL.Query().Get("query")

	if path == "" && query != "" {
		found, err := findProject(query)
		if err != nil {
			jsonError(w, http.StatusNotFound, "project not found: "+err.Error())
			return
		}
		path = found
	}
	if path == "" {
		path = s.taskMgr.workDir
	}

	actions := DetectProjectActions(path)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"project": filepath.Base(path),
		"path":    path,
		"actions": actions,
	})
}

func (s *HTTPServer) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	status := s.taskMgr.GetAgentStatus()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"status":  status,
		"sandbox": s.sandboxSummary(),
	})
}

type runnerModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Source      string `json:"source,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
}

type runnerInfoRow struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	Command                string            `json:"command"`
	Installed              bool              `json:"installed"`
	Ready                  bool              `json:"ready"`
	AuthConfigured         bool              `json:"authConfigured,omitempty"`
	AuthSource             string            `json:"authSource,omitempty"`
	Warning                string            `json:"warning,omitempty"`
	Error                  string            `json:"error,omitempty"`
	IsDefault              bool              `json:"isDefault"`
	SupportsBrowserAuth    bool              `json:"supportsBrowserAuth,omitempty"`
	SupportsModelSelection bool              `json:"supportsModelSelection,omitempty"`
	ModelSource            string            `json:"modelSource,omitempty"`
	Models                 []runnerModelInfo `json:"models"`
	// Version is the first line of `<bin> --version` (e.g. "Claude Code
	// 2.1.126" / "codex-cli 0.122.0" / "1.4.0"). Surfaced in the CLI
	// `yaver primary status` runners table and the mobile UI.
	Version string `json:"version,omitempty"`
}

func fallbackRunnerModels(runnerID string) []runnerModelInfo {
	switch normalizeRunnerID(runnerID) {
	case "claude":
		return []runnerModelInfo{
			{ID: "claude-opus-4-7", Name: "Claude Opus 4.7", Source: "builtin", IsDefault: false},
			{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", Source: "builtin", IsDefault: false},
			{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", Source: "builtin", IsDefault: true},
			{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Source: "builtin", IsDefault: false},
			{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", Source: "builtin", IsDefault: false},
		}
	case "codex":
		return []runnerModelInfo{
			// Default to the Codex-native model: general gpt-5.x require
			// API billing and error on a ChatGPT-subscription Codex login
			// ("model is not supported when using Codex with a ChatGPT
			// account"). gpt-5.3-codex is the one that works there.
			{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", Source: "builtin", IsDefault: true},
			{ID: "gpt-5.5", Name: "GPT-5.5", Source: "builtin", IsDefault: false},
			{ID: "gpt-5.5-pro", Name: "GPT-5.5 Pro", Source: "builtin", IsDefault: false},
			{ID: "gpt-5.4", Name: "GPT-5.4", Source: "builtin", IsDefault: false},
			{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini", Source: "builtin", IsDefault: false},
		}
	default:
		return nil
	}
}

// handleRunnerRestart checks if the runner is healthy and clears the runnerDown flag.
// Mobile can call this to "restart" the runner after all retries were exhausted.
// handleRunners returns all available runners with their install status and models.
func (s *HTTPServer) handleRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	// Build models index by runner
	modelsByRunner := make(map[string][]runnerModelInfo)
	modelSourceByRunner := make(map[string]string)
	for _, m := range GetCachedModels() {
		modelsByRunner[m.RunnerID] = append(modelsByRunner[m.RunnerID], runnerModelInfo{
			ID:          m.ModelID,
			Name:        m.Name,
			Description: m.Description,
			Source:      "backend",
			IsDefault:   m.IsDefault,
		})
		modelSourceByRunner[m.RunnerID] = "backend"
	}
	if len(modelsByRunner["opencode"]) == 0 {
		if cfg, err := loadOpenCodeConfigSummary(); err == nil {
			for _, model := range cfg.Models {
				modelsByRunner["opencode"] = append(modelsByRunner["opencode"], runnerModelInfo{
					ID:        model.ID,
					Name:      model.Name,
					Provider:  model.Provider,
					Source:    model.Source,
					IsDefault: model.IsDefault,
				})
			}
			if len(modelsByRunner["opencode"]) > 0 {
				modelSourceByRunner["opencode"] = "opencode-config"
			}
		}
	}
	for _, runnerID := range []string{"claude", "codex", "opencode"} {
		if len(modelsByRunner[runnerID]) == 0 {
			if fallback := fallbackRunnerModels(runnerID); len(fallback) > 0 {
				modelsByRunner[runnerID] = fallback
				modelSourceByRunner[runnerID] = "builtin"
			}
		}
	}

	var runners []runnerInfoRow
	seenIDs := make(map[string]bool)
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	allowedRunnerSet := map[string]bool{}
	filterGuestRunners := false
	if guestUID != "" && s.guestConfigMgr != nil {
		if cfg := s.guestConfigMgr.GetConfig(guestUID); cfg != nil && len(cfg.AllowedRunners) > 0 {
			filterGuestRunners = true
			for _, id := range cfg.AllowedRunners {
				allowedRunnerSet[normalizeRunnerID(id)] = true
			}
		}
	}

	// Add default runner first, then others sorted by ID
	defaultID := s.taskMgr.runner.RunnerID
	addRunner := func(r RunnerConfig) {
		if filterGuestRunners && !allowedRunnerSet[normalizeRunnerID(r.RunnerID)] {
			return
		}
		if seenIDs[r.RunnerID] {
			return
		}
		path, err := osexec.LookPath(r.Command)
		status := DetectRunnerRuntimeStatus(r, s.taskMgr.workDir)
		models := modelsByRunner[r.RunnerID]
		// Probe `<bin> --version` so the CLI / mobile UI can show what
		// build the agent is wrapping. verifyRunnerBinarySignature
		// (5min cached) re-uses the same call for its sigOK check, so
		// the cost is one fork per runner per 5 minutes.
		version := ""
		if err == nil {
			if _, ver := verifyRunnerBinarySignature(r.RunnerID, path); ver != "" {
				version = ver
			}
		}
		runners = append(runners, runnerInfoRow{
			ID:                     r.RunnerID,
			Name:                   r.Name,
			Command:                r.Command,
			Installed:              err == nil,
			Ready:                  status.Ready,
			AuthConfigured:         status.AuthConfigured,
			AuthSource:             status.AuthSource,
			Warning:                status.Warning,
			Error:                  status.Error,
			IsDefault:              r.RunnerID == defaultID,
			SupportsBrowserAuth:    normalizeRunnerID(r.RunnerID) == "claude" || normalizeRunnerID(r.RunnerID) == "codex",
			SupportsModelSelection: len(models) > 0,
			ModelSource:            modelSourceByRunner[r.RunnerID],
			Models:                 models,
			Version:                version,
		})
		seenIDs[r.RunnerID] = true
	}

	// Default runner first
	if r, ok := builtinRunners[defaultID]; ok {
		addRunner(r)
	}
	// Then rest in stable order
	for _, id := range []string{"claude", "codex", "opencode"} {
		if r, ok := builtinRunners[id]; ok {
			addRunner(r)
		}
	}
	// Any remaining runners from Convex
	for _, r := range builtinRunners {
		addRunner(r)
	}

	// Include the active runner if it's custom (not in builtinRunners)
	if !seenIDs[s.taskMgr.runner.RunnerID] && (!filterGuestRunners || allowedRunnerSet[normalizeRunnerID(s.taskMgr.runner.RunnerID)]) {
		models := modelsByRunner[s.taskMgr.runner.RunnerID]
		runners = append(runners, runnerInfoRow{
			ID:                     s.taskMgr.runner.RunnerID,
			Name:                   s.taskMgr.runner.Name,
			Command:                s.taskMgr.runner.Command,
			Installed:              true,
			Ready:                  true,
			IsDefault:              true,
			SupportsBrowserAuth:    normalizeRunnerID(s.taskMgr.runner.RunnerID) == "claude" || normalizeRunnerID(s.taskMgr.runner.RunnerID) == "codex",
			SupportsModelSelection: len(models) > 0,
			ModelSource:            modelSourceByRunner[s.taskMgr.runner.RunnerID],
			Models:                 models,
		})
	}

	responseDefault := s.taskMgr.runner.RunnerID
	if filterGuestRunners && !allowedRunnerSet[normalizeRunnerID(responseDefault)] {
		responseDefault = ""
		if len(runners) > 0 {
			responseDefault = runners[0].ID
			for i := range runners {
				runners[i].IsDefault = runners[i].ID == responseDefault
			}
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"runners": runners,
		"default": responseDefault,
	})
}

func (s *HTTPServer) handleRunnerRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	// Check runner health
	if err := s.taskMgr.CheckRunner(); err != nil {
		jsonError(w, http.StatusServiceUnavailable, fmt.Sprintf("runner not available: %v", err))
		return
	}

	// Clear runnerDown flag in Convex
	if s.taskMgr.ConvexURL != "" {
		go func() {
			_ = SetRunnerDown(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID, false)
			_ = ReportDeviceEvent(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID, "restart", "manual restart from mobile")
		}()
	}

	log.Printf("[HTTP] Runner restart triggered from mobile — runner is healthy")
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Runner is healthy, runnerDown flag cleared",
	})
}

// handleRunnerSwitch switches the active runner. Validates the binary exists first.
// parseRunnerAllowCSV parses a server-stamped CSV runner allowlist header into
// a normalized set. Empty/blank → nil, meaning "this layer imposes no runner
// constraint".
func parseRunnerAllowCSV(csv string) map[string]bool {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(csv, ",") {
		if p := normalizeRunnerID(strings.TrimSpace(part)); p != "" {
			out[p] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// runnerDeniedByScopeHeaders enforces the runner allowlists carried by
// server-stamped scope headers — host-share policy (X-Yaver-HostShareAllowedRunners)
// and SDK-token runner scope (X-Yaver-SdkAllowedRunners). Jointly inclusive: the
// requested runner must satisfy EVERY present allowlist; an absent/empty header
// imposes no constraint and never forces a choice. Guest allowlists are enforced
// separately via GuestConfigManager.CheckRequestedRunner. Returns a denial reason
// or nil when allowed.
func runnerDeniedByScopeHeaders(r *http.Request, requestedRunnerID, defaultRunnerID string) *AccessDeniedReason {
	runner := normalizeRunnerID(strings.TrimSpace(requestedRunnerID))
	if runner == "" {
		runner = normalizeRunnerID(strings.TrimSpace(defaultRunnerID))
	}
	if runner == "" {
		return nil
	}
	for _, header := range []string{"X-Yaver-HostShareAllowedRunners", "X-Yaver-SdkAllowedRunners"} {
		allowed := parseRunnerAllowCSV(r.Header.Get(header))
		if allowed == nil {
			continue // layer doesn't constrain runners
		}
		if !allowed[runner] {
			return &AccessDeniedReason{Denied: true, Reason: fmt.Sprintf("runner %q is not permitted by policy (allowed: %s)", runner, r.Header.Get(header))}
		}
	}
	return nil
}

// stampSdkRunnerScope translates an SDK token's `runners:<csv>` scope into the
// server-controlled X-Yaver-SdkAllowedRunners header that runner enforcement
// reads. It always deletes any inbound value first (never trust a client-set
// header) and only re-stamps from the validated token scopes. No-op when the
// token carries no runner scope.
func stampSdkRunnerScope(r *http.Request, scopes []string) {
	r.Header.Del("X-Yaver-SdkAllowedRunners")
	for _, scope := range scopes {
		if rest, ok := strings.CutPrefix(scope, "runners:"); ok {
			if rest = strings.TrimSpace(rest); rest != "" {
				r.Header.Set("X-Yaver-SdkAllowedRunners", rest)
			}
			return
		}
	}
}

// stampSdkDataPolicy translates an SDK token's `policy:redactPII` scope into the
// server-controlled X-Yaver-RedactPII header that the task-create path reads to
// enforce the company dataPolicy redaction on the runtime. Always deletes any
// inbound value first (never trust a client header) and only re-stamps from the
// validated token scopes. Enforces the company privacy control authoritatively
// regardless of which surface created the task.
func stampSdkDataPolicy(r *http.Request, scopes []string) {
	r.Header.Del("X-Yaver-RedactPII")
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == "policy:redactPII" {
			r.Header.Set("X-Yaver-RedactPII", "1")
			return
		}
	}
}

func (s *HTTPServer) handleRunnerSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var body struct {
		RunnerID string `json:"runnerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.RunnerID == "" {
		jsonError(w, http.StatusBadRequest, "runnerId is required")
		return
	}

	runnerID := normalizeRunnerID(body.RunnerID)
	newRunner, known := builtinRunners[runnerID]
	if !known {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("unknown runner: %s", body.RunnerID))
		return
	}

	// Enforce runner allowlists carried by the caller's scope. Switching the
	// runner mutates global task-manager state, so a scoped caller (guest,
	// host-share, or SDK-token runner scope) may only switch to a runner their
	// policy permits. Jointly inclusive — each present layer must allow it.
	if s.guestConfigMgr != nil {
		if guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")); guestUID != "" {
			if denied := s.guestConfigMgr.CheckRequestedRunner(guestUID, runnerID, s.taskMgr.runner.RunnerID); denied != nil {
				jsonError(w, http.StatusForbidden, denied.Reason)
				return
			}
		}
	}
	if denied := runnerDeniedByScopeHeaders(r, runnerID, s.taskMgr.runner.RunnerID); denied != nil {
		jsonError(w, http.StatusForbidden, denied.Reason)
		return
	}

	// Check if binary exists on this machine
	path, err := osexec.LookPath(newRunner.Command)
	if err != nil {
		log.Printf("[HTTP] Runner switch failed: %s not found on machine", newRunner.Command)
		jsonError(w, http.StatusNotFound, fmt.Sprintf("%s is not installed on this machine", newRunner.Command))
		return
	}

	// Update the task manager's runner
	s.taskMgr.mu.Lock()
	s.taskMgr.runner = newRunner
	s.taskMgr.mu.Unlock()

	log.Printf("[HTTP] Runner switched to %s (%s) at %s", newRunner.Name, runnerID, path)

	// Also save to Convex user settings (non-blocking)
	if s.taskMgr.ConvexURL != "" {
		go func() {
			payload, _ := json.Marshal(map[string]string{"runnerId": runnerID})
			req, err := newBearerRequest("POST", s.taskMgr.ConvexURL+"/settings", s.taskMgr.AuthToken, bytes.NewReader(payload))
			if err == nil {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"runner":   newRunner.Name,
		"runnerId": runnerID,
		"path":     path,
	})
}

// handleShutdown gracefully shuts down the yaver agent. Called from mobile.
func (s *HTTPServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	log.Printf("[HTTP] Shutdown requested from mobile")

	// Stop all running tasks first
	stopped := s.taskMgr.StopAllTasks()
	log.Printf("[HTTP] Stopped %d tasks before shutdown", stopped)

	// Report event to Convex
	if s.taskMgr.ConvexURL != "" {
		go func() {
			_ = ReportDeviceEvent(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID, "stopped", "shutdown from mobile")
			_ = MarkOffline(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID)
		}()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Agent shutting down",
		"stopped": stopped,
	})

	// Trigger shutdown after response is sent
	if s.onShutdown != nil {
		go func() {
			time.Sleep(500 * time.Millisecond) // let response flush
			s.onShutdown()
		}()
	}
}

// handleClean removes old tasks, images, and logs. Called from mobile settings.
func (s *HTTPServer) handleClean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Days int  `json:"days"`
		All  bool `json:"all"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Days == 0 {
		body.Days = 30
	}

	result := performClean(body.Days, body.All, false)
	log.Printf("[HTTP] Clean: removed %d tasks, %d image dirs, freed %s", result.TasksRemoved, result.ImagesRemoved, formatBytes(result.BytesFreed))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"result": result,
	})
}

// handleTasks handles GET /tasks (list) and POST /tasks (create).
func (s *HTTPServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTasks(w, r)
	case http.MethodPost:
		s.createTask(w, r)
	case http.MethodDelete:
		count := s.taskMgr.DeleteAllTasks()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "deleted": count})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.taskMgr.ListTasks()
	for i := range tasks {
		s.enrichTaskInfoVideo(&tasks[i], r)
	}

	// Support ?limit=N to reduce payload size for web dashboard polling
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(tasks) {
			tasks = tasks[:limit]
		}
	}

	resp := map[string]interface{}{
		"ok":    true,
		"tasks": tasks,
	}
	// Include project context so mobile can display project chips
	project := DetectProjectInfo(s.taskMgr.workDir)
	resp["project"] = project
	// Include todo stats
	if s.todolistMgr != nil {
		resp["todoCount"] = s.todolistMgr.Count()
		resp["todoTotal"] = len(s.todolistMgr.ListItems())
	}
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) taskInfoFromTask(task *Task, r *http.Request) TaskInfo {
	s.taskMgr.mu.RLock()
	defer s.taskMgr.mu.RUnlock()
	output := task.Output
	if len(output) > 10000 {
		output = output[len(output)-10000:]
	}
	hostname, _ := os.Hostname()
	info := TaskInfo{
		ID:          task.ID,
		Title:       task.Title,
		Description: task.Description,
		Status:      task.Status,
		RunnerID:    task.RunnerID,
		// Echo the model + deviceName so mobile UIs can render the
		// task's authoritative target instead of inferring from the
		// focused-device picker state.
		Model:          task.Model,
		DeviceName:     hostname,
		SessionID:      task.SessionID,
		Output:         output,
		ResultText:     task.ResultText,
		CostUSD:        task.CostUSD,
		Turns:          task.Turns,
		Source:         task.Source,
		TmuxSession:    task.TmuxSession,
		IsAdopted:      task.IsAdopted,
		CreatedAt:      task.CreatedAt,
		StartedAt:      task.StartedAt,
		FinishedAt:     task.FinishedAt,
		ChainID:        task.ChainID,
		ChainOrder:     task.ChainOrder,
		AutoRetry:      task.AutoRetry,
		AutoRetryCount: task.AutoRetryCount,
		AutoRetryMax:   task.AutoRetryMax,
		VideoEnabled:   task.VideoEnabled,
		VideoSource:    task.VideoSource,
		VideoClipID:    task.VideoClipID,
		VideoStatus:    task.VideoStatus,
		AskFreely:      task.AskFreely,
	}
	s.enrichTaskInfoVideo(&info, r)
	return info
}

func (s *HTTPServer) enrichTaskInfoVideo(info *TaskInfo, r *http.Request) {
	if info == nil {
		return
	}
	if strings.TrimSpace(info.VideoClipID) == "" {
		return
	}
	if s.vibePreviewMgr != nil {
		if clip := s.vibePreviewMgr.ClipByID(info.VideoClipID); clip != nil {
			if strings.TrimSpace(clip.Status) != "" {
				info.VideoStatus = strings.TrimSpace(clip.Status)
			}
		}
	}
	base := ""
	if r != nil {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		} else if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
			scheme = proto
		}
		host := strings.TrimSpace(r.Host)
		if host != "" {
			base = scheme + "://" + host
		}
	}
	path := "/vibing/preview/clip/" + urlQueryEscape(strings.TrimSpace(info.VideoClipID))
	posterPath := path + "/poster"
	info.VideoClipURL = path
	info.VideoPosterURL = posterPath
	if base != "" {
		info.VideoClipURL = base + path
		info.VideoPosterURL = base + posterPath
	}
}

func (s *HTTPServer) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string             `json:"title"`
		Description   string             `json:"description"`
		UserPrompt    string             `json:"userPrompt,omitempty"`
		Model         string             `json:"model"`
		Runner        string             `json:"runner"`         // runner ID: "claude", "codex", "opencode" — empty uses default
		Mode          string             `json:"mode,omitempty"` // runner-specific subcommand: opencode "build" / "plan" / custom agent
		CustomCommand string             `json:"customCommand"`  // arbitrary command — runs via sh -c
		ProjectName   string             `json:"projectName,omitempty"`
		BundleID      string             `json:"bundleId,omitempty"` // mobile-app bundle id (e.g. io.example.sfmg) — used to resolve project for feedback-source tasks
		Source        string             `json:"source"`             // client type: "mobile", "desktop-app", "web", "cli"
		Verbosity     *int               `json:"verbosity,omitempty"`
		Images        []ImageAttachment  `json:"images,omitempty"`
		WorkDir       string             `json:"workDir,omitempty"`
		SliceContract *TaskSliceContract `json:"sliceContract,omitempty"`
		// Video summary toggle. When videoEnabled is true, after the
		// task finishes the agent auto-records a short MP4 of the
		// running result via vibe-preview. videoSource picks the
		// recorder (browser/sim-ios/sim-android/phone); empty =
		// auto-detect from the task's workDir.
		VideoEnabled bool   `json:"videoEnabled,omitempty"`
		VideoSource  string `json:"videoSource,omitempty"`
		// AskFreely opts the task OUT of yaver's no-questions
		// preamble + soft-question fallback so the runner may emit
		// clarifying questions in prose. Default false.
		AskFreely bool `json:"askFreely,omitempty"`
		// AskMode runs the task as a grounded deep question-answer (repo
		// analysis + file:line cites, escalate-on-breadth, explain-first
		// with a confirm gate before acting) instead of a work run. Set by
		// `yaver ask`, the yaver_ask MCP tool, and the Ask toggle on the
		// web/mobile console. Default false.
		AskMode bool `json:"askMode,omitempty"`
		// Viewport — surface + pane geometry hints. Optional. When
		// present, the prompt wrapper appends a one-line display
		// context line so Claude tunes response shape (terse on HUD,
		// markdown on desktop, voice-budgeted when TTS will read it).
		Viewport *TaskViewport `json:"viewport,omitempty"`
		// SpeechContext — the STT/TTS state of the client creating this
		// task. Mobile already sends this; the agent folds it into the
		// viewport so the prompt wrapper knows whether output will be
		// spoken (TTS) and whether the user can reply by voice (STT).
		// Provider names are hints only — API keys live in the vault and
		// flow P2P, never in this payload.
		SpeechContext *struct {
			InputFromSpeech bool   `json:"inputFromSpeech,omitempty"`
			STTProvider     string `json:"sttProvider,omitempty"`
			TTSEnabled      bool   `json:"ttsEnabled,omitempty"`
			TTSProvider     string `json:"ttsProvider,omitempty"`
			TTSMode         bool   `json:"ttsMode,omitempty"` // user "run tasks in TTS mode" setting
		} `json:"speechContext,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	source := body.Source
	if source == "" {
		// Fall back to header, then default
		source = r.Header.Get("X-Yaver-Source")
	}
	if source == "" && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Yaver-Session-Mode")), "terminal") {
		source = terminalLocalTaskSource
	}
	if source == "" {
		source = "mobile"
	}

	// Check guest restrictions before creating task
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	var guestCfg *GuestConfig
	guestWorkDir := ""
	// Feedback-only guests ALWAYS require isolation — the scope is designed
	// for untrusted end-users, so their prompts go through a container even
	// when the host hasn't globally enabled containerize_guests.
	forceIsolation := false
	if guestUID != "" && s.guestConfigMgr != nil {
		guestCfg = s.guestConfigMgr.GetConfig(guestUID)
		// Check runner restriction
		if denied := s.guestConfigMgr.CheckRequestedRunner(guestUID, body.Runner, s.taskMgr.runner.RunnerID); denied != nil {
			jsonError(w, http.StatusForbidden, denied.Reason)
			return
		}
		// Block custom commands for guests (direct shell access)
		if body.CustomCommand != "" {
			jsonError(w, http.StatusForbidden, "guests cannot run custom commands")
			return
		}
		forceIsolation = guestRequireIsolation(guestCfg) || s.guestConfigMgr.IsFeedbackOnly(guestUID)
		if forceIsolation {
			if s.containerRunner == nil || !s.containerRunner.IsAvailable() {
				jsonError(w, http.StatusServiceUnavailable, "guest is configured to require Docker isolation, but Docker is not available on this host")
				return
			}
		}
		// Project-scope gate: direct guest tasks must carry a project identity
		// so the host can resolve the exact allowed repo path server-side.
		// We never trust guest-supplied workDir paths.
		guestProjectName := strings.TrimSpace(body.ProjectName)
		allowedProjects := s.guestConfigMgr.AllowedProjects(guestUID)
		if len(allowedProjects) > 0 && guestProjectName == "" {
			jsonError(w, http.StatusForbidden, fmt.Sprintf("this guest is scoped to projects %v; projectName is required", allowedProjects))
			return
		}
		if guestProjectName != "" {
			if !s.guestConfigMgr.GuestCanAccessProject(guestUID, guestProjectName) {
				jsonError(w, http.StatusForbidden, fmt.Sprintf("project %q is not in the allowed project list %v", guestProjectName, allowedProjects))
				return
			}
			resolvedWorkDir, err := resolveGuestTaskProjectPath(guestProjectName)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			guestWorkDir = resolvedWorkDir
		}
	}

	// Enforce host-share / SDK-token runner scope (guest runners were already
	// checked in the guest block above). Jointly inclusive with any other layer.
	if denied := runnerDeniedByScopeHeaders(r, body.Runner, s.taskMgr.runner.RunnerID); denied != nil {
		jsonError(w, http.StatusForbidden, denied.Reason)
		return
	}

	// For guest tasks, prepend security context to the prompt so the AI agent
	// stays within the project directory and doesn't access sensitive files.
	title := body.Title
	if guestUID != "" {
		promptWorkDir := s.taskMgr.workDir
		if guestWorkDir != "" {
			promptWorkDir = guestWorkDir
		}
		title = guestPromptPrefix(promptWorkDir, guestCfg) + title
	}

	// Feedback-source tasks (FeedbackOverlay typed message after a guest
	// shake, SDK modal "Fix" button, etc.) get reshaped into the same
	// vibing pipeline /vibing/execute uses: project resolved from
	// projectName/bundleId/last-loaded-guest, vibing context prefixed,
	// runner picked by readiness. Without this, those tasks ran as
	// generic one-shot prompts with no project context, no commit, no
	// reload — the user shook, typed "fix this bug", saw "done", and
	// the loaded sfmg guest bundle still showed the broken version.
	// See feedback_to_vibe.go.
	bodyWorkDir := body.WorkDir
	if guestUID != "" {
		bodyWorkDir = guestWorkDir
	}
	s.vibingifyFeedbackTaskBody(r, source, &title, &body.ProjectName, &bodyWorkDir, &body.Runner, &body.Model, body.BundleID)
	if guestUID == "" {
		body.WorkDir = bodyWorkDir
	}

	// Guests must not be able to redirect the task cwd or inject their own
	// slice contract — those fields are owner-only mesh orchestration hints
	// and could otherwise override the guest prompt prefix that keeps the
	// AI agent inside the host's workdir.
	taskOpts := TaskCreateOptions{
		WorkDir:           body.WorkDir,
		InitialUserPrompt: body.UserPrompt,
		SliceContract:     body.SliceContract,
	}
	if guestUID != "" {
		// Strip owner-only fields. If the host resolved a guest project,
		// keep that server-approved workDir.
		taskOpts.WorkDir = guestWorkDir
		taskOpts.SliceContract = nil
		// Snapshot guest policy into the task BEFORE it starts, so runtime
		// guards (autoSwitchProject, container gating, API-key filtering)
		// see GuestUserID atomically. Setting these after the call is a
		// race: startProcess runs synchronously and could pre-observe the
		// task as owner-authenticated.
		taskOpts.GuestUserID = guestUID
		taskOpts.GuestUseHostAPIKeys = guestUseHostAPIKeys(guestCfg)
		taskOpts.GuestRequireIsolation = forceIsolation
		taskOpts.GuestAllowGuestProvidedKeys = guestCfg == nil || guestCfg.AllowGuestProvidedAPIKeys == nil || *guestCfg.AllowGuestProvidedAPIKeys
		if guestCfg != nil {
			taskOpts.GuestCPULimitPercent = guestCfg.CPULimitPercent
			taskOpts.GuestRAMLimitMB = guestCfg.RAMLimitMB
		}
	}
	if mounts, err := sharedStorageContainerMountsForTask(taskOpts.GuestUserID, s.guestConfigMgr); err == nil {
		taskOpts.GuestSharedStorageMounts = mounts
	}
	taskOpts.Mode = strings.TrimSpace(body.Mode)
	// Video summary toggle propagates from request → task → OnTaskDone
	// hook, where MaybeRecordTaskSummary fires the vibe-preview clip
	// recorder when status flips to completed.
	taskOpts.VideoEnabled = body.VideoEnabled
	taskOpts.VideoSource = strings.TrimSpace(body.VideoSource)
	// Guests can never opt out of the no-questions preamble — they
	// shouldn't be able to elicit free-form clarifying prose from the
	// runner (the host's review of the task assumes the preamble is
	// in force).
	if guestUID == "" {
		taskOpts.AskFreely = body.AskFreely
		// Ask mode is owner-only too: a guest shouldn't be able to flip a
		// scoped work task into a free-roaming repo-analysis run.
		taskOpts.AskMode = body.AskMode
		// Console auto-detect: when a console surface (the attach terminal,
		// web console, mobile code-mode) sends a plain natural-language
		// QUESTION with no yaver verb/command in it, treat it as an ask
		// case — deep grounded analysis, explain-first — even though the
		// caller didn't set askMode. The user types "how do I test STT/TTS"
		// and gets a real answer instead of a work run. High-precision
		// classifier (detectAskIntent) so genuine work instructions are
		// left alone. Explicit askFreely opts out.
		if !taskOpts.AskMode && !taskOpts.AskFreely && isConsoleAskSource(source) && detectAskIntent(body.Title) {
			taskOpts.AskMode = true
			log.Printf("[ask] auto-detected question intent on %s console task; routing to ask mode", source)
		}
	}

	var verbosityCtx *TaskVerbosity
	if body.Verbosity != nil {
		verbosityCtx = &TaskVerbosity{Verbosity: body.Verbosity}
	}
	// Fold the client's surface + STT/TTS state into the viewport so the
	// prompt wrapper can shape output (voice-friendly + budgeted when TTS
	// is on, the whole reply led by a spoken summary in TTS mode, a spoken
	// closing question when the user can reply by voice). Computed BEFORE
	// CreateTaskWithOptions and passed via taskOpts.Viewport so startProcess
	// — which assembles the prompt synchronously inside that call — sees it
	// (setting task.TaskViewport afterward is a race).
	// Precedence: explicit body.Viewport fields, then speechContext body,
	// then X-Yaver-* headers, then source. CLI/no-hint stays plain text.
	vp := body.Viewport
	if body.SpeechContext != nil {
		if vp == nil {
			vp = &TaskViewport{}
		}
		vp.Voice = vp.Voice || body.SpeechContext.InputFromSpeech
		vp.STTEnabled = vp.STTEnabled || body.SpeechContext.InputFromSpeech || body.SpeechContext.STTProvider != ""
		vp.TTSEnabled = vp.TTSEnabled || body.SpeechContext.TTSEnabled
		vp.TTSMode = vp.TTSMode || body.SpeechContext.TTSMode
		if vp.STTProvider == "" {
			vp.STTProvider = body.SpeechContext.STTProvider
		}
		if vp.TTSProvider == "" {
			vp.TTSProvider = body.SpeechContext.TTSProvider
		}
	}
	vp = mergeClientVoiceHints(r, vp, source)
	taskOpts.Viewport = vp

	// Company dataPolicy.redactPII enforcement: the header is server-stamped
	// from the validated SDK token's `policy:redactPII` scope (and stripped on
	// ingress), so this cannot be forged or disabled by a client. When set, the
	// runtime scrubs PII/secrets from the prompt before the runner sees them.
	taskOpts.RedactPII = r.Header.Get("X-Yaver-RedactPII") == "1"

	task, err := s.taskMgr.CreateTaskWithOptions(title, body.Description, body.Model, source, body.Runner, body.CustomCommand, body.Images, taskOpts, verbosityCtx)
	if err != nil {
		// Preflight failures (workDir not writable, runner not authed,
		// Linux sandbox kernel prereqs missing, …) come back here with
		// a non-nil err AND a task object whose Status is already
		// TaskStatusFailed and whose Output carries the readable
		// reason. Surface those as a 201 with status=failed so the
		// chat UI renders a normal failed bubble in the conversation
		// instead of a transient banner — the user reads the chown
		// command (or whatever the fix is) right where they're typing,
		// chowns, retries, done.
		//
		// Pure validation errors (missing title, ID collision, etc.)
		// have task == nil and continue to surface as 500 like before.
		if task == nil || task.Status != TaskStatusFailed || task.Output == "" {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create task: %v", err))
			return
		}
		log.Printf("[HTTP] Task %s preflight-failed: %v (surfacing as failed bubble)", task.ID, err)
	}

	log.Printf("[HTTP] Task created: %s — %s (status: %s, model: %s, runner: %s)", task.ID, task.Title, task.Status, body.Model, task.RunnerID)
	projectWorkDir := s.taskMgr.workDir
	if task.WorkDir != "" {
		projectWorkDir = task.WorkDir
	}
	project := DetectProjectInfo(projectWorkDir)
	hostname, _ := os.Hostname()
	resp := map[string]interface{}{
		"ok":         true,
		"taskId":     task.ID,
		"status":     task.Status,
		"runnerId":   task.RunnerID,
		"model":      task.Model,
		"deviceName": hostname,
		"project":    project.Name,
	}
	log.Printf("[HTTP] Sending create response for task %s", task.ID)
	jsonReply(w, http.StatusCreated, resp)
	log.Printf("[HTTP] Response sent for task %s", task.ID)
}

func resolveGuestTaskProjectPath(projectName string) (string, error) {
	ref, err := resolveProjectRef(projectName, "")
	if err != nil {
		return "", fmt.Errorf("could not resolve project %q: %w", strings.TrimSpace(projectName), err)
	}
	return ref.Path, nil
}

// handleTaskByID routes /tasks/{id}, /tasks/{id}/output, /tasks/{id}/stop, /tasks/{id}/continue
func (s *HTTPServer) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	// Parse path: /tasks/{id}[/action]
	path := strings.TrimPrefix(r.URL.Path, "/tasks/")
	parts := strings.SplitN(path, "/", 2)
	taskID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if taskID == "" {
		jsonError(w, http.StatusBadRequest, "task ID required")
		return
	}

	if taskID == "stop-all" {
		s.handleStopAll(w, r)
		return
	}
	if taskID == "delete-all" {
		s.handleDeleteAll(w, r)
		return
	}

	switch action {
	case "":
		if r.Method == http.MethodDelete {
			s.deleteTask(w, r, taskID)
		} else {
			s.getTask(w, r, taskID)
		}
	case "output":
		s.streamOutput(w, r, taskID)
	case "stop":
		s.stopTask(w, r, taskID)
	case "exit":
		s.exitTask(w, r, taskID)
	case "continue":
		s.continueTask(w, r, taskID)
	case "complete":
		s.completeTask(w, r, taskID)
	case "fork":
		// Runtime agent switch: keep parent immutable, spawn child with
		// new runner/model/mode + bounded recent-context handoff. See
		// task_fork.go and CODING_AGENT_CHANGE_FROM_MOBILE_APP_CHAT.md.
		s.handleTaskFork(w, r, taskID)
	case "question":
		// POST: stdio MCP child registers a yaver_ask_user call,
		// blocks until the user answers via /tasks/{id}/answer.
		// GET: late-joining UI fetches the currently-pending question
		// (if any) without re-subscribing to SSE.
		s.handleTaskQuestion(w, r, taskID)
	case "answer":
		// POST {questionId, answer}: mobile / web / CLI delivers the
		// human's answer. Resolves the parked /question handler so the
		// runner's MCP tool call returns.
		s.handleTaskAnswer(w, r, taskID)
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}

func (s *HTTPServer) getTask(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[HTTP] GET task %s", id)
	task, ok := s.taskMgr.GetTask(id)
	if !ok {
		log.Printf("[HTTP] Task %s not found", id)
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}

	info := s.taskInfoFromTask(task, r)

	log.Printf("[HTTP] Task %s status=%s output_len=%d", id, info.Status, len(info.Output))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"task": info,
	})
}

// streamOutput streams task output as Server-Sent Events (SSE).
func (s *HTTPServer) streamOutput(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[HTTP] SSE stream requested for task %s", id)
	task, ok := s.taskMgr.GetTask(id)
	if !ok {
		log.Printf("[HTTP] SSE task %s not found", id)
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	// First send any existing output as initial data.
	s.taskMgr.mu.RLock()
	existingOutput := task.Output
	currentStatus := task.Status
	s.taskMgr.mu.RUnlock()

	if existingOutput != "" {
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
			"type": "output",
			"text": existingOutput,
		}))
		flusher.Flush()
	}

	// If already finished, send done event and return.
	if currentStatus == TaskStatusFinished || currentStatus == TaskStatusReview || currentStatus == TaskStatusFailed || currentStatus == TaskStatusStopped {
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
			"type":   "done",
			"status": currentStatus,
		}))
		flusher.Flush()
		return
	}

	// If a question is already pending for this task (the agent
	// asked while no client was subscribed), replay it immediately
	// on subscribe so a late-joining mobile/web client doesn't sit
	// blank waiting for the next event.
	if pending, ok := globalQuestionRegistry.Pending(id); ok {
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
			"type":     "agent_question",
			"question": pending,
		}))
		flusher.Flush()
	}

	// Stream live output from the channel.
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-task.eventCh:
			if ev == nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", jsonString(ev))
			flusher.Flush()
		case text, ok := <-task.outputCh:
			if !ok {
				// Channel closed — the stdout reader is done, but the task's
				// process waiter may still be racing to flip Status from
				// running/queued -> completed/failed/stopped. Wait briefly for
				// doneCh so the terminal SSE event reflects the real final
				// state instead of leaving mobile/web stuck animating
				// "running" until the next poll.
				select {
				case <-task.doneCh:
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				s.taskMgr.mu.RLock()
				finalStatus := task.Status
				s.taskMgr.mu.RUnlock()
				fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
					"type":   "done",
					"status": finalStatus,
				}))
				flusher.Flush()
				return
			}
			// Cap per-chunk size before shipping to mobile / web. A
			// runaway codex stdout (npm install logs, bytecode dumps,
			// 4 GB tarball pulls) used to ship verbatim and froze the
			// mobile transcript view's main thread when it tried to
			// render the accumulated buffer. We protect every consumer
			// at the source: anything above maxStreamChunkBytes gets
			// truncated with a readable marker. The full stream is
			// still preserved in task.Output for the Logs view.
			const maxStreamChunkBytes = 4096
			if len(text) > maxStreamChunkBytes {
				keep := maxStreamChunkBytes
				text = text[:keep] + "\n…[chunk trimmed: " +
					fmt.Sprintf("%d", len(text)-keep) +
					" bytes more in Logs]…\n"
			}
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
				"type": "output",
				"text": text,
			}))
			flusher.Flush()
		}
	}
}

func (s *HTTPServer) stopTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	if err := s.taskMgr.StopTask(id); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	log.Printf("[HTTP] Task stopped: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": id,
		"status": TaskStatusStopped,
	})
}

func (s *HTTPServer) completeTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	if err := s.taskMgr.CompleteTask(id); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[HTTP] Task completed by user: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": id,
		"status": TaskStatusFinished,
	})
}

func (s *HTTPServer) exitTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	if err := s.taskMgr.GracefulStopTask(id); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	log.Printf("[HTTP] Task gracefully exited: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": id,
		"status": TaskStatusStopped,
	})
}

func (s *HTTPServer) deleteTask(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.taskMgr.DeleteTask(id); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[HTTP] Task deleted: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleStopAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	count := s.taskMgr.StopAllTasks()
	log.Printf("[HTTP] Stopped all tasks: %d", count)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"stopped": count,
	})
}

func (s *HTTPServer) handleDeleteAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	count := s.taskMgr.DeleteAllTasks()
	log.Printf("[HTTP] Deleted all tasks: %d", count)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"deleted": count,
	})
}

func (s *HTTPServer) continueTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var body struct {
		Input  string            `json:"input"`
		Images []ImageAttachment `json:"images,omitempty"`
		Runner string            `json:"runner,omitempty"`
		Model  string            `json:"model,omitempty"`
		Mode   string            `json:"mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Input == "" {
		jsonError(w, http.StatusBadRequest, "input is required")
		return
	}

	task, err := s.taskMgr.ResumeTaskWithOptions(id, body.Input, body.Images, TaskResumeOptions{
		RunnerID: body.Runner,
		Model:    body.Model,
		Mode:     body.Mode,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("resume failed: %v", err))
		return
	}

	log.Printf("[HTTP] Task resumed: %s (session=%s)", id, task.SessionID)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"status": task.Status,
	})
}

// ---------------------------------------------------------------------------
// Doctor & Tools handlers
// ---------------------------------------------------------------------------

// handleDoctor runs system diagnostics and returns results as JSON.
func (s *HTTPServer) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	type checkResult struct {
		Name    string `json:"name"`
		Status  string `json:"status"` // "pass", "warn", "fail"
		Detail  string `json:"detail"`
		Section string `json:"section"`
	}

	var checks []checkResult

	addCheck := func(section, name, status, detail string) {
		checks = append(checks, checkResult{Name: name, Status: status, Detail: detail, Section: section})
	}

	// Config
	cfg, err := LoadConfig()
	if err != nil {
		addCheck("config", "Config file", "fail", fmt.Sprintf("Error: %v", err))
	} else {
		p, _ := ConfigPath()
		addCheck("config", "Config file", "pass", p)
	}
	addCheck("config", "Version", "pass", version)

	// Auth
	if cfg == nil || cfg.AuthToken == "" {
		addCheck("auth", "Auth token", "fail", "Not signed in")
	} else {
		addCheck("auth", "Auth token", "pass", "Present")
		if cfg.DeviceID != "" {
			addCheck("auth", "Device ID", "pass", cfg.DeviceID[:8]+"...")
		} else {
			addCheck("auth", "Device ID", "fail", "Not set")
		}
	}

	// Agent
	agentPID, agentRunning := isAgentRunning()
	if agentRunning {
		addCheck("agent", "Agent process", "pass", fmt.Sprintf("Running (PID %d)", agentPID))
	} else {
		addCheck("agent", "Agent process", "warn", "Not running")
	}

	if tmuxAvailable() {
		addCheck("agent", "Tmux", "pass", "available")
	} else {
		addCheck("agent", "Tmux", "warn", "not installed")
	}

	// HTTP server
	statusClient := &http.Client{Timeout: 3 * time.Second}
	if resp, err := statusClient.Get("http://127.0.0.1:18080/health"); err == nil {
		resp.Body.Close()
		addCheck("agent", "HTTP server", "pass", "Listening on :18080")
	} else {
		addCheck("agent", "HTTP server", "warn", "Not reachable on port 18080")
	}

	// AI Runners — yaver's three first-class runners.
	runners := []struct{ id, name, cmd, install string }{
		{"claude", "Claude Code", "claude", "npm install -g @anthropic-ai/claude-code"},
		{"codex", "OpenAI Codex", "codex", "npm install -g @openai/codex"},
		{"opencode", "opencode", "opencode", "curl -fsSL https://opencode.ai/install | bash"},
	}
	for _, runner := range runners {
		p, err := osexec.LookPath(runner.cmd)
		if err != nil {
			addCheck("runners", runner.name, "warn", "Not installed — "+runner.install)
			continue
		}
		// Cap each runner --version probe at 800ms. Some runners
		// (claude, codex) open a network socket on first run and
		// would otherwise stall the whole /agent/doctor response
		// past the caller's read timeout.
		probeCtx, cancel := context.WithTimeout(r.Context(), 800*time.Millisecond)
		out, verr := osexec.CommandContext(probeCtx, runner.cmd, "--version").CombinedOutput()
		cancel()
		if verr == nil {
			ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			if len(ver) > 60 {
				ver = ver[:60]
			}
			addCheck("runners", runner.name, "pass", fmt.Sprintf("%s (%s)", p, ver))
		} else {
			addCheck("runners", runner.name, "pass", p)
		}
	}

	onboarding := collectMachineOnboardingStatus()
	for _, provider := range onboarding.Providers {
		status := machineOnboardingDoctorLevel(provider)
		switch status {
		case "pass":
			addCheck("onboarding", provider.Name, "pass", machineOnboardingDoctorDetail(provider))
		case "warn":
			addCheck("onboarding", provider.Name, "warn", machineOnboardingDoctorDetail(provider))
		default:
			addCheck("onboarding", provider.Name, "fail", machineOnboardingDoctorDetail(provider))
		}
	}

	// Relay servers
	if cfg != nil && len(cfg.RelayServers) > 0 {
		relayClient := &http.Client{Timeout: 5 * time.Second}
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			start := time.Now()
			resp, err := relayClient.Get(rs.HttpURL + "/health")
			rtt := time.Since(start)
			if err != nil {
				addCheck("relay", "Relay: "+label, "fail", "Unreachable")
			} else {
				resp.Body.Close()
				addCheck("relay", "Relay: "+label, "pass", fmt.Sprintf("OK (%dms)", rtt.Milliseconds()))
			}
		}
	} else {
		addCheck("relay", "Relay servers", "warn", "None configured")
	}

	// Network
	ip := getLocalIP()
	if ip != "" {
		addCheck("network", "Local IP", "pass", ip)
	} else {
		addCheck("network", "Local IP", "warn", "Could not determine")
	}
	recoveryPosture := computeRecoveryTransportPosture(cfg)
	if recoveryPosture.HasPrivateTransport {
		addCheck("network", "Remote recovery transport", "pass", recoveryPosture.Summary)
	} else {
		addCheck("network", "Remote recovery transport", "warn", recoveryPosture.Summary)
	}
	if recoveryPosture.PublicDirectRecoveryClosed {
		addCheck("network", "Public recovery exposure", "pass", "Direct public HTTP recovery is blocked by the agent")
	} else {
		addCheck("network", "Public recovery exposure", "warn", "Direct public HTTP recovery is enabled (default). Set require-private-recovery=true to lock /auth/recover to private paths.")
	}

	// Hermes / Super-host
	nodePath, nodeVersion := detectManagedOrSystemNode()
	if nodePath != "" {
		addCheck("hermes", "Node.js runtime", "pass", fmt.Sprintf("%s (%s)", nodePath, nodeVersion))
	} else {
		addCheck("hermes", "Node.js runtime", "fail", "not installed — run `yaver install mobile`")
	}
	hermesReady := false
	if summary, err := embeddedHermescSummary(); err == nil {
		addCheck("hermes", "Embedded hermesc", "pass", summary)
		hermesReady = true
	} else {
		addCheck("hermes", "Embedded hermesc", "fail", err.Error())
	}
	if nodePath != "" && hermesReady {
		addCheck("hermes", "Hermes reload path", "pass", "ready for React Native / Expo bundle reload into Yaver mobile")
	} else {
		addCheck("hermes", "Hermes reload path", "warn", "run `yaver install mobile` to provision the Node runtime and verify hermesc")
	}

	if unityPath, unityVer := detectUnityEditor(); unityPath != "" {
		if unityVer != "" && unityVer != "unknown" {
			addCheck("unity", "Unity Editor", "pass", fmt.Sprintf("%s (%s)", unityPath, unityVer))
		} else {
			addCheck("unity", "Unity Editor", "pass", unityPath)
		}
	} else {
		addCheck("unity", "Unity Editor", "warn", "not detected")
	}

	unityProjects := []string{}
	for _, p := range scanMobileProjects() {
		if p.Framework != "unity" {
			continue
		}
		label := p.Name
		if p.SDKVersion != "" {
			label += " (" + p.SDKVersion + ")"
		}
		unityProjects = append(unityProjects, label)
	}
	if len(unityProjects) > 0 {
		addCheck("unity", "Unity projects", "pass", strings.Join(unityProjects, ", "))
		addCheck("unity", "Unity fast iteration path", "pass", "feedback SDK + remote vibing + content refresh/scene reload/redeploy workflow available")
	} else {
		addCheck("unity", "Unity projects", "warn", "no Unity projects detected in discovery roots")
		addCheck("unity", "Unity fast iteration path", "warn", "run `yaver sdk add feedback --platform unity` inside a Unity project")
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"checks": checks,
	})
}

// handleTools scans for installed AI tools and returns their info.
func (s *HTTPServer) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	type toolInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Command   string `json:"command"`
		Installed bool   `json:"installed"`
		Path      string `json:"path,omitempty"`
		Version   string `json:"version,omitempty"`
		Install   string `json:"installCmd"`
	}

	tools := []struct{ id, name, cmd, install string }{
		{"claude", "Claude Code", "claude", "npm install -g @anthropic-ai/claude-code"},
		{"codex", "OpenAI Codex", "codex", "npm install -g @openai/codex"},
		{"opencode", "opencode", "opencode", "curl -fsSL https://opencode.ai/install | bash"},
	}

	var result []toolInfo
	for _, t := range tools {
		ti := toolInfo{ID: t.id, Name: t.name, Command: t.cmd, Install: t.install}
		p, err := osexec.LookPath(t.cmd)
		if err == nil {
			ti.Installed = true
			ti.Path = p
			out, verr := osexec.Command(t.cmd, "--version").CombinedOutput()
			if verr == nil {
				ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
				if len(ver) > 60 {
					ver = ver[:60]
				}
				ti.Version = ver
			}
		}
		result = append(result, ti)
	}

	// Also check supporting tools
	type supportTool struct {
		Name      string `json:"name"`
		Command   string `json:"command"`
		Installed bool   `json:"installed"`
		Purpose   string `json:"purpose"`
	}
	var support []supportTool
	supportChecks := []struct{ name, cmd, purpose string }{
		{"tmux", "tmux", "Session management"},
		{"Node.js", "node", "JS toolchain"},
		{"Python", "python3", "Python toolchain"},
		{"Go", "go", "Go toolchain"},
		{"Git", "git", "Version control"},
		{"sox", "sox", "Audio recording"},
		{"ffmpeg", "ffmpeg", "Media processing"},
		{"whisper", "whisper-cpp", "On-device STT"},
		{"Docker", "docker", "Container runtime"},
		{"cloudflared", "cloudflared", "Cloudflare Tunnel"},
	}
	for _, s := range supportChecks {
		st := supportTool{Name: s.name, Command: s.cmd, Purpose: s.purpose}
		if _, err := osexec.LookPath(s.cmd); err == nil {
			st.Installed = true
		}
		support = append(support, st)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"tools":   result,
		"support": support,
	})
}

// ---------------------------------------------------------------------------
// Scheduler handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		jsonError(w, http.StatusServiceUnavailable, "scheduler not enabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		schedules := s.scheduler.ListSchedules()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "schedules": schedules})
	case http.MethodPost:
		var st ScheduledTask
		if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.scheduler.AddSchedule(&st); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "schedule": st})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleScheduleByID(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		jsonError(w, http.StatusServiceUnavailable, "scheduler not enabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "":
		if r.Method == http.MethodDelete {
			// Fast existence check is synchronous, but the actual
			// remove + persist runs in a goroutine so the client
			// never blocks on disk I/O or scheduler lock contention.
			// `unscheduleLoopViaDaemon` on the CLI side is best-
			// effort — it just wants a quick thumbs-up.
			if _, ok := s.scheduler.GetSchedule(id); !ok {
				jsonError(w, http.StatusNotFound, "schedule not found")
				return
			}
			go func(id string) {
				if err := s.scheduler.RemoveSchedule(id); err != nil {
					log.Printf("[scheduler] async remove %s: %v", id, err)
				}
			}(id)
			jsonReply(w, http.StatusAccepted, map[string]interface{}{"ok": true, "queued": true})
		} else {
			st, ok := s.scheduler.GetSchedule(id)
			if !ok {
				jsonError(w, http.StatusNotFound, "schedule not found")
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "schedule": st})
		}
	case "pause":
		if err := s.scheduler.PauseSchedule(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "resume":
		if err := s.scheduler.ResumeSchedule(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "run-now", "run":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		if err := s.scheduler.RunScheduleNow(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusAccepted, map[string]interface{}{"ok": true, "queued": true})
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}

// ---------------------------------------------------------------------------
// Analytics handler
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if s.analytics == nil {
		jsonError(w, http.StatusServiceUnavailable, "analytics not available")
		return
	}
	stats := s.analytics.GetStats()
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "analytics": stats})
}

// ---------------------------------------------------------------------------
// Session transfer handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleSessionList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	sessions := ListTransferableSessions(s.taskMgr)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "sessions": sessions})
}

func (s *HTTPServer) handleSessionExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		TaskID           string `json:"taskId"`
		IncludeWorkspace bool   `json:"includeWorkspace"`
		WorkspaceMode    string `json:"workspaceMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.TaskID == "" {
		jsonError(w, http.StatusBadRequest, "taskId is required")
		return
	}
	bundle, err := ExportSession(s.taskMgr, body.TaskID, ExportOptions{
		IncludeWorkspace: body.IncludeWorkspace,
		WorkspaceMode:    body.WorkspaceMode,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[HTTP] Session exported: task=%s agent=%s turns=%d", body.TaskID, bundle.AgentType, len(bundle.Task.Turns))
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "bundle": bundle})
}

func (s *HTTPServer) handleSessionImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Bundle         TransferBundle `json:"bundle"`
		WorkDir        string         `json:"workDir,omitempty"`
		ResumeOnImport bool           `json:"resumeOnImport"`
		GitClone       bool           `json:"gitClone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	taskID, warnings, err := ImportSession(s.taskMgr, &body.Bundle, ImportOptions{
		WorkDir:        body.WorkDir,
		ResumeOnImport: body.ResumeOnImport,
		GitClone:       body.GitClone,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[HTTP] Session imported: task=%s warnings=%d", taskID, len(warnings))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"taskId":   taskID,
		"warnings": warnings,
	})
}

// ---------------------------------------------------------------------------
// Notifications handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleNotificationsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, _ := LoadConfig()
		if cfg != nil && cfg.Notifications != nil {
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": cfg.Notifications})
		} else {
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": NotificationConfig{}})
		}
	case http.MethodPost:
		var notifConfig NotificationConfig
		if err := json.NewDecoder(r.Body).Decode(&notifConfig); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		cfg, _ := LoadConfig()
		if cfg == nil {
			cfg = &Config{}
		}
		cfg.Notifications = &notifConfig
		if err := SaveConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notifyMgr != nil {
			s.notifyMgr.UpdateConfig(&notifConfig)
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleNotificationsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Channel string `json:"channel"` // "telegram", "discord", "slack", or "" for all
	}
	json.NewDecoder(r.Body).Decode(&body)
	if s.notifyMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "notifications not available")
		return
	}
	result := s.notifyMgr.TestNotification(body.Channel)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "result": result})
}

// ---------------------------------------------------------------------------
// Webhook trigger (public — uses webhook secret)
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	// Validate webhook secret
	secret := r.Header.Get("X-Webhook-Secret")
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.WebhookSecret == "" {
		jsonError(w, http.StatusServiceUnavailable, "webhook secret not configured — set via: yaver config set webhook-secret <secret>")
		return
	}
	if !secretEqual(secret, cfg.WebhookSecret) {
		jsonError(w, http.StatusUnauthorized, "invalid webhook secret")
		return
	}

	var body struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Runner      string `json:"runner,omitempty"`
		Model       string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	task, err := s.taskMgr.CreateTask(body.Title, body.Description, body.Model, "webhook", body.Runner, "", nil, nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[HTTP] Webhook task created: %s — %s", task.ID, body.Title)
	jsonReply(w, http.StatusCreated, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"status": task.Status,
	})
}

// ---------------------------------------------------------------------------
// Exec handlers (remote command execution)
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleExec(w http.ResponseWriter, r *http.Request) {
	if s.execMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "exec is not enabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions := s.execMgr.ListExecs()
		execs := make([]map[string]interface{}, 0, len(sessions))
		for _, sess := range sessions {
			execs = append(execs, sess.Snapshot())
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "execs": execs})
	case http.MethodPost:
		var body struct {
			Command string            `json:"command"`
			WorkDir string            `json:"workDir,omitempty"`
			Shell   string            `json:"shell,omitempty"`
			Timeout int               `json:"timeout,omitempty"`
			Env     map[string]string `json:"env,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.Command == "" {
			jsonError(w, http.StatusBadRequest, "command is required")
			return
		}
		sess, err := s.execMgr.StartExec(body.Command, body.WorkDir, body.Shell, body.Env, body.Timeout)
		if err != nil {
			code := http.StatusInternalServerError
			if strings.Contains(err.Error(), "blocked") {
				code = http.StatusBadRequest
			} else if strings.Contains(err.Error(), "too many") {
				code = http.StatusTooManyRequests
			}
			jsonError(w, code, err.Error())
			return
		}
		log.Printf("[HTTP] Exec started: %s — %s (pid=%d)", sess.ID, body.Command, sess.PID)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "execId": sess.ID, "pid": sess.PID})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleExecByID(w http.ResponseWriter, r *http.Request) {
	if s.execMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "exec is not enabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/exec/")
	parts := strings.SplitN(path, "/", 2)
	execID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if execID == "" {
		jsonError(w, http.StatusBadRequest, "exec ID required")
		return
	}

	switch action {
	case "":
		if r.Method == http.MethodDelete {
			if err := s.execMgr.KillExec(execID); err != nil {
				jsonError(w, http.StatusNotFound, err.Error())
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		} else {
			sess, ok := s.execMgr.GetExec(execID)
			if !ok {
				jsonError(w, http.StatusNotFound, "exec session not found")
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "exec": sess.Snapshot()})
		}
	case "input":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.execMgr.SendInput(execID, body.Input); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "signal":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var body struct {
			Signal string `json:"signal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.execMgr.SignalExec(execID, body.Signal); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "stream":
		s.streamExecOutput(w, r, execID)
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}

func (s *HTTPServer) streamExecOutput(w http.ResponseWriter, r *http.Request, execID string) {
	ch, err := s.execMgr.Subscribe(execID)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for evt := range ch {
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func jsonReply(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	payload := map[string]interface{}{
		"ok":    false,
		"error": msg,
	}
	// Cloud-tenant mode: when the agent is deployed as a managed Yaver Cloud
	// tenant (YAVER_CLOUD_TENANT=1 in its env), enrich 401/403 responses
	// with a hint + checkout URL so mobile / CLI / web clients can guide
	// the caller to complete checkout instead of showing a raw auth error.
	// Apple-compliance: the mobile app is expected to display this hint
	// but NOT auto-open checkoutUrl — that stays on web.
	if (status == http.StatusUnauthorized || status == http.StatusForbidden) && os.Getenv("YAVER_CLOUD_TENANT") == "1" {
		payload["hint"] = "Yaver Cloud tenant — push requires a valid Yaver session or an explicit override token"
		if u := os.Getenv("YAVER_CLOUD_CHECKOUT_URL"); u != "" {
			payload["checkoutUrl"] = u
		}
	}
	jsonReply(w, status, payload)
}

func jsonString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------------------
// MCP (Model Context Protocol) — JSON-RPC 2.0 over HTTP
// Allows AI agents like Claude to use Yaver as an MCP server.
// ---------------------------------------------------------------------------

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpToolMatchesPatterns reports whether toolName satisfies any glob pattern.
// "*" matches everything; "prefix_*" / "prefix*" match by prefix; otherwise an
// exact match is required. An empty list matches nothing (the caller decides
// whether that means "unconstrained" or "deny all").
func mcpToolMatchesPatterns(toolName string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
			continue
		case p == "*":
			return true
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(toolName, strings.TrimSuffix(p, "*")) {
				return true
			}
		case p == toolName:
			return true
		}
	}
	return false
}

// mcpToolDeniedByScope enforces the per-role MCP tool allowlist carried by the
// server-stamped X-Yaver-AllowedTools header (derived from a token's
// tools:<patterns> scope, itself projected from companyAIOptions
// toolPolicyByRole). Semantics:
//   - header absent        → no constraint (e.g. owner); allowed.
//   - header == "(none)"   → deny all tools (e.g. viewer role).
//   - header == "a_*,b_x"  → allowed only if the tool matches a pattern.
//
// Returns a denial reason or nil.
func mcpToolDeniedByScope(r *http.Request, toolName string) *AccessDeniedReason {
	header := strings.TrimSpace(r.Header.Get("X-Yaver-AllowedTools"))
	if header == "" {
		return nil
	}
	if header == "(none)" {
		return &AccessDeniedReason{Denied: true, Reason: fmt.Sprintf("tool %q is not permitted: this role may not call MCP tools", toolName)}
	}
	if mcpToolMatchesPatterns(toolName, strings.Split(header, ",")) {
		return nil
	}
	return &AccessDeniedReason{Denied: true, Reason: fmt.Sprintf("tool %q is not permitted by policy (allowed: %s)", toolName, header)}
}

// stampMcpToolScope translates a token's tools:<patterns> scope into the
// server-controlled X-Yaver-AllowedTools header that MCP dispatch enforces. The
// inbound value is always stripped first (never trust a client header). No-op
// when the token carries no tools scope.
func stampMcpToolScope(r *http.Request, scopes []string) {
	r.Header.Del("X-Yaver-AllowedTools")
	for _, scope := range scopes {
		if rest, ok := strings.CutPrefix(scope, "tools:"); ok {
			if rest = strings.TrimSpace(rest); rest != "" {
				r.Header.Set("X-Yaver-AllowedTools", rest)
			}
			return
		}
	}
}

func (s *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mcpResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &mcpError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	var resp mcpResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "yaver",
				"version": version,
			},
			// Shown to the calling LLM on every session start. Mirrors
			// the "Read This First" section of CLAUDE.md + AGENTS.md so
			// clients that never open those files still hear the rule.
			"instructions": mcpInstructions(),
		}

	case "tools/list":
		resp.Result = s.getMCPToolsList()

	case "tools/call":
		// Enforce the per-role MCP tool allowlist (companyAIOptions
		// toolPolicyByRole, carried as the token's tools:<patterns> scope).
		// Owner calls carry no scope header and are unconstrained.
		var tc struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &tc)
		if denied := mcpToolDeniedByScope(r, tc.Name); denied != nil {
			resp.Result = mcpToolError(denied.Reason)
		} else {
			resp.Result = s.handleMCPToolCallWithAddr(req.Params, r.RemoteAddr)
		}

	case "notifications/initialized":
		// Client notification, no response needed but we return empty result
		resp.Result = map[string]interface{}{}

	default:
		resp.Error = &mcpError{Code: -32601, Message: "Method not found: " + req.Method}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (s *HTTPServer) handleMCPToolCall(params json.RawMessage) interface{} {
	return s.handleMCPToolCallWithAddr(params, "")
}

// handleMCPToolCallWithAddr is the same as handleMCPToolCall but threads
// the HTTP client's TCP peer address through to handlers that need it
// (currently only session_handoff, which uses it to look up the calling
// process's PID for cooperative termination).
func (s *HTTPServer) handleMCPToolCallWithAddr(params json.RawMessage, clientAddr string) interface{} {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": "Invalid tool call parameters"},
			},
			"isError": true,
		}
	}

	switch call.Name {
	case "create_task":
		var args struct {
			Prompt    string `json:"prompt"`
			Verbosity *int   `json:"verbosity"`
			Runner    string `json:"runner"`
			Model     string `json:"model"`
			// Mode is the runner-specific subcommand selector. Currently
			// honored by opencode where it maps to `--agent <mode>` —
			// e.g. "build" / "plan" / any custom agent the user has
			// defined in their opencode.json. Other runners ignore it.
			Mode string `json:"mode"`
			// VideoEnabled triggers the post-completion vibe-preview clip
			// recorder. VideoSource overrides the auto-detected recorder.
			VideoEnabled bool   `json:"video_enabled"`
			VideoSource  string `json:"video_source"`
			// AskFreely opts the new task out of yaver's no-questions
			// preamble + soft-question fallback (default false).
			AskFreely bool `json:"ask_freely"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Prompt == "" {
			return mcpToolError("prompt is required")
		}
		var vc *TaskVerbosity
		if args.Verbosity != nil {
			vc = &TaskVerbosity{Verbosity: args.Verbosity}
		}
		taskOpts := TaskCreateOptions{
			Mode:         strings.TrimSpace(args.Mode),
			VideoEnabled: args.VideoEnabled,
			VideoSource:  strings.TrimSpace(args.VideoSource),
			AskFreely:    args.AskFreely,
		}
		task, err := s.taskMgr.CreateTaskWithOptions(args.Prompt, "", strings.TrimSpace(args.Model), "mcp", strings.TrimSpace(args.Runner), "", nil, taskOpts, vc)
		if err != nil {
			return mcpToolError(fmt.Sprintf("failed to create task: %v", err))
		}
		log.Printf("[MCP] Task created: %s (video=%v)", task.ID, args.VideoEnabled)
		return mcpToolJSON(map[string]interface{}{
			"ok":   true,
			"task": s.taskInfoFromTask(task, nil),
		})

	case "yaver_ask":
		var args struct {
			Question string `json:"question"`
			Runner   string `json:"runner"`
			Model    string `json:"model"`
			WorkDir  string `json:"work_dir"`
			Depth    string `json:"depth"`
		}
		json.Unmarshal(call.Arguments, &args)
		question := strings.TrimSpace(args.Question)
		if question == "" {
			return mcpToolError("question is required")
		}
		// Depth resolution: explicit deep/single wins; auto/empty escalates to
		// the multi-agent graph only when the question reads as broad.
		depth := strings.ToLower(strings.TrimSpace(args.Depth))
		goDeep := depth == "deep"
		if depth == "" || depth == "auto" {
			goDeep = detectAskBreadth(question)
		}
		if goDeep && s.agentGraphMgr != nil {
			workDir := strings.TrimSpace(args.WorkDir)
			if workDir == "" {
				workDir = s.taskMgr.workDir
			}
			run, err := s.agentGraphMgr.CreateRun(AgentGraphCreateRequest{
				Name:     "ask",
				WorkDir:  workDir,
				Prompt:   question,
				Template: "ask",
				Runner:   strings.TrimSpace(args.Runner),
				Model:    strings.TrimSpace(args.Model),
			})
			if err != nil {
				return mcpToolError(fmt.Sprintf("failed to start ask graph: %v", err))
			}
			log.Printf("[MCP] Ask graph started: %s (broad question, %d nodes)", run.ID, len(run.Nodes))
			return mcpToolJSON(map[string]interface{}{
				"ok":    true,
				"mode":  "deep",
				"graph": run,
			})
		}
		taskOpts := TaskCreateOptions{
			AskMode: true,
			WorkDir: strings.TrimSpace(args.WorkDir),
		}
		task, err := s.taskMgr.CreateTaskWithOptions(question, "", strings.TrimSpace(args.Model), "ask", strings.TrimSpace(args.Runner), "", nil, taskOpts)
		if err != nil {
			return mcpToolError(fmt.Sprintf("failed to create ask task: %v", err))
		}
		log.Printf("[MCP] Ask task created: %s", task.ID)
		return mcpToolJSON(map[string]interface{}{
			"ok":   true,
			"mode": "single",
			"task": s.taskInfoFromTask(task, nil),
		})

	case "list_tasks":
		tasks := s.taskMgr.ListTasks()
		for i := range tasks {
			s.enrichTaskInfoVideo(&tasks[i], nil)
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "tasks": tasks})

	case "get_task":
		var args struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		task, ok := s.taskMgr.GetTask(args.TaskID)
		if !ok {
			return mcpToolError("task not found: " + args.TaskID)
		}
		return mcpToolJSON(map[string]interface{}{
			"ok":   true,
			"task": s.taskInfoFromTask(task, nil),
		})

	case "stop_task":
		var args struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if err := s.taskMgr.StopTask(args.TaskID); err != nil {
			return mcpToolError(err.Error())
		}
		log.Printf("[MCP] Task stopped: %s", args.TaskID)
		return mcpToolResult("Task stopped: " + args.TaskID)

	case "continue_task":
		var args struct {
			TaskID string `json:"task_id"`
			Input  string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		task, err := s.taskMgr.ResumeTask(args.TaskID, args.Input, nil)
		if err != nil {
			return mcpToolError(fmt.Sprintf("resume failed: %v", err))
		}
		log.Printf("[MCP] Task resumed: %s (session=%s)", args.TaskID, task.SessionID)
		return mcpToolResult(fmt.Sprintf("Task resumed. Task ID: %s", task.ID))

	case "yaver_ask_user":
		// Two-mode handler:
		//   - HTTP MCP (daemon): we ARE the daemon, so register
		//     directly with globalQuestionRegistry and block on the
		//     answer channel.
		//   - stdio MCP (child of runner): we have a sibling daemon
		//     listening on 127.0.0.1:18080 — POST to its
		//     /tasks/{id}/question and let it long-poll for the
		//     answer. forwardYaverAskUser() handles both.
		return forwardYaverAskUser(call.Arguments)

	case "wire_detect":
		// List USB-attached phones on the agent's host. See mcp_wire_tools.go.
		out, err := mcpWireDetect()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "wire_push":
		// Build + install a self-contained native binary onto a USB-attached
		// phone. Long-running; captures output to ~/.yaver/logs/wire-push-*.log
		// and returns the log path + tail. See mcp_wire_tools.go.
		var args mcpWirePushArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return mcpToolError("bad args: " + err.Error())
		}
		out, err := mcpWirePush(args)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "wireless_detect":
		// Paired + visible-unpaired across iOS and Android.
		// See mcp_wireless_tools.go.
		out, err := mcpWirelessDetect()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "wireless_setup_android":
		// Polls mDNS for the pairing service, runs adb pair + auto-connect.
		// Caller supplies the 6-digit code (typically collected via
		// yaver_ask_user). See mcp_wireless_tools.go.
		var args mcpWirelessSetupAndroidArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return mcpToolError("bad args: " + err.Error())
		}
		out, err := mcpWirelessSetupAndroid(args)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "wireless_pair_android":
		var args mcpWirelessPairAndroidArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return mcpToolError("bad args: " + err.Error())
		}
		out, err := mcpWirelessPairAndroid(args)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "wireless_connect_android":
		var args mcpWirelessConnectAndroidArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return mcpToolError("bad args: " + err.Error())
		}
		out, err := mcpWirelessConnectAndroid(args)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "wireless_push":
		// Same as wire_push but routes through the wireless device picker.
		// See mcp_wireless_tools.go.
		var args mcpWirelessPushArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return mcpToolError("bad args: " + err.Error())
		}
		out, err := mcpWirelessPush(args)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)

	case "fork_task":
		// Runtime agent switch over MCP. Same shape as POST /tasks/{id}/fork.
		// Returns a structured object so calling AI agents can chain follow-ups
		// to the new child task.
		var args struct {
			TaskID       string `json:"task_id"`
			Runner       string `json:"runner"`
			Model        string `json:"model"`
			Mode         string `json:"mode"`
			Input        string `json:"input"`
			ContextWords int    `json:"context_words"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		parent, ok := s.taskMgr.GetTask(args.TaskID)
		if !ok || parent == nil {
			return mcpToolError("parent task not found: " + args.TaskID)
		}
		runner := normalizeRunnerID(strings.TrimSpace(args.Runner))
		if runner == "" {
			return mcpToolError("runner is required (claude, codex, or opencode)")
		}
		input := strings.TrimSpace(args.Input)
		if input == "" {
			return mcpToolError("input is required")
		}
		if !isSupportedForkRunner(runner) {
			return mcpToolError("unsupported runner: " + runner)
		}
		req := taskForkRequest{
			Runner:       runner,
			Model:        strings.TrimSpace(args.Model),
			Mode:         strings.TrimSpace(args.Mode),
			Input:        input,
			ContextWords: args.ContextWords,
		}
		ctxWords := req.ContextWords
		if ctxWords <= 0 {
			ctxWords = defaultForkContextWords
		}
		if ctxWords < minForkContextWords {
			ctxWords = minForkContextWords
		}
		if ctxWords > maxForkContextWords {
			ctxWords = maxForkContextWords
		}
		handoff := buildForkHandoffPrompt(parent, req, ctxWords)
		taskOpts := TaskCreateOptions{
			WorkDir:           parent.WorkDir,
			InitialUserPrompt: req.Input,
			Mode:              req.Mode,
		}
		if parent.GuestUserID != "" {
			taskOpts.GuestUserID = parent.GuestUserID
			taskOpts.GuestUseHostAPIKeys = parent.GuestUseHostAPIKeys
			taskOpts.GuestRequireIsolation = parent.GuestRequireIsolation
			taskOpts.GuestAllowGuestProvidedKeys = parent.GuestAllowGuestProvidedKeys
		}
		child, err := s.taskMgr.CreateTaskWithOptions(handoff, "forked from "+parent.ID+" (runner switch)", req.Model, "runner-switch-fork", req.Runner, "", nil, taskOpts)
		if err != nil {
			return mcpToolError("create child task: " + err.Error())
		}
		log.Printf("[MCP] Task forked: parent=%s child=%s runner=%s", parent.ID, child.ID, child.RunnerID)
		return mcpToolJSON(map[string]interface{}{
			"taskId":           child.ID,
			"runnerId":         child.RunnerID,
			"parentTaskId":     parent.ID,
			"relationship":     "forked-by-yaver",
			"contextWordsUsed": countWords(handoff),
		})

	case "get_info":
		hostname, _ := os.Hostname()
		return mcpToolResult(fmt.Sprintf("Hostname: %s\nVersion: %s\nWork Dir: %s", hostname, version, s.taskMgr.workDir))

	case "yaver_auth_factory_reset":
		var args struct {
			Headless bool `json:"headless"`
		}
		json.Unmarshal(call.Arguments, &args)
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := spawnAuthFactoryReset(args.Headless); err != nil {
				log.Printf("[MCP] auth factory-reset failed to launch: %v", err)
			}
		}()
		mode := "browser"
		if args.Headless {
			mode = "device-code"
		}
		return mcpToolResult(fmt.Sprintf("Starting Yaver auth factory-reset in the background (%s mode). This clears local auth state, refreshes the canonical backend URL, and reopens sign-in.", mode))

	case "web_search":
		var args struct {
			Query    string `json:"query"`
			Provider string `json:"provider"`
			Limit    int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Query == "" {
			return mcpToolError("query is required")
		}
		resp, err := RunWebSearch(args.Query, args.Provider, args.Limit)
		if err != nil {
			return mcpToolError(fmt.Sprintf("web_search: %v", err))
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Web search via %s for %q (%d results):\n\n", resp.Provider, resp.Query, len(resp.Results)))
		for i, r := range resp.Results {
			sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, r.Title, r.URL))
			if r.Snippet != "" {
				sb.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(sb.String())

	// --- Runner Management ---
	case "list_runners":
		var sb strings.Builder
		sb.WriteString("Available runners:\n")
		defaultID := s.taskMgr.runner.RunnerID
		for id, r := range builtinRunners {
			_, err := osexec.LookPath(r.Command)
			installed := "not installed"
			if err == nil {
				installed = "installed"
			}
			def := ""
			if id == defaultID {
				def = " (active)"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s [%s]%s\n", id, r.Name, installed, def))
		}
		return mcpToolResult(sb.String())

	case "switch_runner":
		var args struct {
			RunnerID string `json:"runner_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.RunnerID == "" {
			return mcpToolError("runner_id is required")
		}
		r, ok := builtinRunners[args.RunnerID]
		if !ok {
			return mcpToolError(fmt.Sprintf("unknown runner: %s", args.RunnerID))
		}
		if _, err := osexec.LookPath(r.Command); err != nil {
			return mcpToolError(fmt.Sprintf("%s is not installed on this machine", r.Command))
		}
		s.taskMgr.mu.Lock()
		s.taskMgr.runner = r
		s.taskMgr.mu.Unlock()
		log.Printf("[MCP] Runner switched to %s", args.RunnerID)
		return mcpToolResult(fmt.Sprintf("Runner switched to %s (%s)", r.Name, args.RunnerID))

	case "agent_machine_inventory":
		machines := listAllMachines(context.Background())
		if len(machines) == 0 {
			return mcpToolResult("No machines found.")
		}
		var sb strings.Builder
		for _, m := range machines {
			status := "offline"
			if m.IsOnline {
				status = "online"
			}
			scope := "remote"
			if m.IsLocal {
				scope = "local"
			}
			if m.IsShared {
				scope = "shared"
			}
			sb.WriteString(fmt.Sprintf("- %s (%s) [%s, %s]", m.Name, m.DeviceID, scope, status))
			if m.IsShared {
				hostLabel := firstNonEmpty(m.HostName, m.HostEmail, "unknown host")
				sb.WriteString(fmt.Sprintf(" shared_from=%s", hostLabel))
				if strings.TrimSpace(m.PriorityMode) != "" {
					sb.WriteString(fmt.Sprintf(" priority=%s", m.PriorityMode))
				}
				if m.UseHostAPIKeys {
					sb.WriteString(" host_api_keys=yes")
				} else {
					sb.WriteString(" host_api_keys=no")
				}
				if strings.TrimSpace(m.AccessScope) != "" {
					sb.WriteString(fmt.Sprintf(" access=%s", m.AccessScope))
				}
			}
			if m.Provider != "" {
				sb.WriteString(fmt.Sprintf(" provider=%s", m.Provider))
			}
			if m.Capabilities != nil {
				sb.WriteString(fmt.Sprintf(" slots=%d", m.Capabilities.MaxTaskSlots))
				var ready []string
				for _, runner := range m.Capabilities.Runners {
					if runner.Ready {
						ready = append(ready, runner.ID)
					}
				}
				if len(ready) > 0 {
					sb.WriteString(fmt.Sprintf(" runners=%s", strings.Join(ready, ",")))
				}
				if m.Capabilities.Profile != nil {
					if m.Capabilities.Profile.Summary != "" {
						sb.WriteString(fmt.Sprintf("\n  summary: %s", m.Capabilities.Profile.Summary))
					}
					if len(m.Capabilities.Profile.Signatures) > 0 {
						sb.WriteString(fmt.Sprintf("\n  signatures: %s", strings.Join(m.Capabilities.Profile.Signatures, ", ")))
					}
					if len(m.Capabilities.Profile.PreferredFor) > 0 {
						sb.WriteString(fmt.Sprintf("\n  preferred_for: %s", strings.Join(m.Capabilities.Profile.PreferredFor, ", ")))
					}
				}
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(strings.TrimSpace(sb.String()))

	case "agent_graph_start":
		if s.agentGraphMgr == nil {
			return mcpToolError("agent graphs unavailable")
		}
		var args struct {
			Name            string                 `json:"name"`
			WorkDir         string                 `json:"work_dir"`
			Prompt          string                 `json:"prompt"`
			Template        string                 `json:"template"`
			Runner          string                 `json:"runner"`
			Model           string                 `json:"model"`
			MaxParallel     int                    `json:"max_parallel"`
			PreferredDevice string                 `json:"preferred_device"`
			AllowedDevices  []string               `json:"allowed_devices"`
			AllowedRunners  []string               `json:"allowed_runners"`
			Nodes           []mcpAgentGraphNodeArg `json:"nodes"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Prompt) == "" {
			return mcpToolError("prompt is required")
		}
		workDir := strings.TrimSpace(args.WorkDir)
		if workDir == "" {
			workDir = s.taskMgr.workDir
		}
		nodes, err := buildAgentGraphNodesFromMCP(args.Nodes)
		if err != nil {
			return mcpToolError(err.Error())
		}
		req := AgentGraphCreateRequest{
			Name:            args.Name,
			WorkDir:         workDir,
			Prompt:          args.Prompt,
			Template:        args.Template,
			Runner:          args.Runner,
			Model:           args.Model,
			MaxParallel:     args.MaxParallel,
			PreferredDevice: args.PreferredDevice,
			AllowedDevices:  args.AllowedDevices,
			AllowedRunners:  args.AllowedRunners,
			Nodes:           nodes,
		}
		run, err := s.agentGraphMgr.CreateRun(req)
		if err != nil {
			return mcpToolError(fmt.Sprintf("start agent graph: %v", err))
		}
		var pool string
		if len(args.AllowedDevices) > 0 {
			pool = strings.Join(args.AllowedDevices, ", ")
		} else if args.PreferredDevice != "" {
			pool = args.PreferredDevice
		} else {
			pool = "auto"
		}
		return mcpToolResult(fmt.Sprintf("Agent graph started.\nGraph ID: %s\nName: %s\nMachine pool: %s\nNodes: %d", run.ID, run.Name, pool, len(run.Nodes)))

	case "agent_graph_list":
		if s.agentGraphMgr == nil {
			return mcpToolError("agent graphs unavailable")
		}
		runs := s.agentGraphMgr.ListRuns()
		if len(runs) == 0 {
			return mcpToolResult("No agent graphs yet.")
		}
		var sb strings.Builder
		for _, run := range runs {
			sb.WriteString(fmt.Sprintf("- %s [%s] %s nodes=%d parallel=%d\n", run.ID, run.Status, run.Name, len(run.Nodes), run.MaxParallel))
			for _, node := range run.Nodes {
				sb.WriteString(fmt.Sprintf("  • %s [%s]", node.Spec.Title, node.Status))
				if node.Placement != nil {
					sb.WriteString(fmt.Sprintf(" @ %s", node.Placement.DeviceNameOrID()))
					if node.Placement.Runner != "" {
						sb.WriteString(fmt.Sprintf(" via %s", node.Placement.Runner))
					}
				}
				sb.WriteString("\n")
			}
		}
		return mcpToolResult(strings.TrimSpace(sb.String()))

	case "agent_graph_show":
		if s.agentGraphMgr == nil {
			return mcpToolError("agent graphs unavailable")
		}
		var args struct {
			GraphID string `json:"graph_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.GraphID) == "" {
			return mcpToolError("graph_id is required")
		}
		run, ok := s.agentGraphMgr.GetRun(args.GraphID)
		if !ok {
			return mcpToolError("graph not found: " + args.GraphID)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Graph %s [%s]\n", run.Name, run.Status))
		sb.WriteString(fmt.Sprintf("ID: %s\nWorkDir: %s\nParallel: %d\n", run.ID, run.WorkDir, run.MaxParallel))
		if run.Summary != "" {
			sb.WriteString("Summary: " + run.Summary + "\n")
		}
		sb.WriteString("\nNodes:\n")
		for _, node := range run.Nodes {
			sb.WriteString(fmt.Sprintf("- %s (%s) [%s]\n", node.Spec.Title, node.Spec.Kind, node.Status))
			if len(node.Spec.ResourceModes) > 0 {
				sb.WriteString(fmt.Sprintf("  resources: %s\n", strings.Join(node.Spec.ResourceModes, ", ")))
			}
			if node.Spec.PriorDevice != "" || node.Spec.PriorRunner != "" {
				sb.WriteString(fmt.Sprintf("  prior: device=%s runner=%s\n", firstNonEmpty(node.Spec.PriorDevice, "-"), firstNonEmpty(node.Spec.PriorRunner, "-")))
			}
			if node.Placement != nil {
				sb.WriteString(fmt.Sprintf("  placement: %s", node.Placement.DeviceNameOrID()))
				if node.Placement.Runner != "" {
					sb.WriteString(fmt.Sprintf(" via %s", node.Placement.Runner))
				}
				sb.WriteString("\n")
				if node.Placement.Reason != "" {
					sb.WriteString(fmt.Sprintf("  reason: %s\n", node.Placement.Reason))
				}
			}
			if node.Summary != "" {
				sb.WriteString(fmt.Sprintf("  summary: %s\n", node.Summary))
			}
			if node.Error != "" {
				sb.WriteString(fmt.Sprintf("  error: %s\n", node.Error))
			}
		}
		return mcpToolResult(strings.TrimSpace(sb.String()))

	case "agent_graph_stop":
		if s.agentGraphMgr == nil {
			return mcpToolError("agent graphs unavailable")
		}
		var args struct {
			GraphID string `json:"graph_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.GraphID) == "" {
			return mcpToolError("graph_id is required")
		}
		if err := s.agentGraphMgr.StopRun(args.GraphID); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Stopped agent graph: " + args.GraphID)

	case "code_mesh_start":
		if s.agentGraphMgr == nil {
			return mcpToolError("agent graphs unavailable")
		}
		var args struct {
			Name           string                 `json:"name"`
			WorkDir        string                 `json:"work_dir"`
			Prompt         string                 `json:"prompt"`
			MaxParallel    int                    `json:"max_parallel"`
			AllowedDevices []string               `json:"allowed_devices"`
			AllowedRunners []string               `json:"allowed_runners"`
			Nodes          []mcpAgentGraphNodeArg `json:"nodes"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Prompt) == "" {
			return mcpToolError("prompt is required")
		}
		workDir := strings.TrimSpace(args.WorkDir)
		if workDir == "" {
			workDir = s.taskMgr.workDir
		}
		maxParallel := args.MaxParallel
		if maxParallel <= 0 {
			maxParallel = 2
		}
		nodes, err := buildAgentGraphNodesFromMCP(args.Nodes)
		if err != nil {
			return mcpToolError(err.Error())
		}
		req := AgentGraphCreateRequest{
			Name:           args.Name,
			WorkDir:        workDir,
			Prompt:         args.Prompt,
			Template:       "full",
			MaxParallel:    maxParallel,
			AllowedDevices: args.AllowedDevices,
			AllowedRunners: args.AllowedRunners,
			Nodes:          nodes,
		}
		run, err := s.agentGraphMgr.CreateRun(req)
		if err != nil {
			return mcpToolError(fmt.Sprintf("start yaver code: %v", err))
		}
		pool := "auto"
		if len(args.AllowedDevices) > 0 {
			pool = strings.Join(args.AllowedDevices, ", ")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("yaver code (mesh) started — graph %s\n", run.ID))
		sb.WriteString(fmt.Sprintf("Name: %s   Machine pool: %s   Parallel: %d\n", run.Name, pool, run.MaxParallel))
		sb.WriteString("Nodes:\n")
		for _, node := range run.Nodes {
			placement := "auto"
			if node.Placement != nil {
				placement = node.Placement.DeviceNameOrID()
				if node.Placement.Runner != "" {
					placement = placement + " / " + node.Placement.Runner
				}
			}
			sb.WriteString(fmt.Sprintf("  • %s [%s] @ %s\n", node.Spec.Title, node.Status, placement))
		}
		sb.WriteString("\nUse agent_graph_show with graph_id=" + run.ID + " to follow progress.")
		return mcpToolResult(strings.TrimSpace(sb.String()))

	case "totp_status":
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
			return mcpToolError("not signed in — run `yaver auth` first")
		}
		var out struct {
			Enabled                bool `json:"enabled"`
			RecoveryCodesRemaining int  `json:"recoveryCodesRemaining"`
		}
		if err := twoFactorConvexCall(cfg, http.MethodGet, "/auth/totp/status", nil, &out); err != nil {
			return mcpToolError(err.Error())
		}
		if out.Enabled {
			return mcpToolResult(fmt.Sprintf("2FA: enabled (%d recovery codes remaining)", out.RecoveryCodesRemaining))
		}
		return mcpToolResult("2FA: not enabled")

	case "totp_enable_begin":
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
			return mcpToolError("not signed in — run `yaver auth` first")
		}
		var setup struct {
			Secret     string `json:"secret"`
			OtpAuthURL string `json:"otpAuthUrl"`
		}
		if err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/setup", nil, &setup); err != nil {
			return mcpToolError(err.Error())
		}
		body := map[string]interface{}{
			"secret":         setup.Secret,
			"secretReadable": groupTwoFactorSecret(setup.Secret),
			"otpauthUrl":     setup.OtpAuthURL,
			"instructions":   "Scan the otpauth:// URL with Microsoft Authenticator, Google Authenticator, 1Password, or any TOTP app. Then call totp_enable_confirm with a 6-digit code to finish enrollment.",
		}
		return mcpToolJSON(body)

	case "totp_enable_confirm":
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
			return mcpToolError("not signed in — run `yaver auth` first")
		}
		var args struct {
			Code string `json:"code"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Code) == "" {
			return mcpToolError("code is required")
		}
		var out struct {
			RecoveryCodes []string `json:"recoveryCodes"`
		}
		if err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/enable", map[string]string{"code": strings.TrimSpace(args.Code)}, &out); err != nil {
			return mcpToolError(err.Error())
		}
		body := map[string]interface{}{
			"ok":            true,
			"recoveryCodes": out.RecoveryCodes,
			"instructions":  "2FA is now enabled. Show these recovery codes to the user ONCE and ask them to save them somewhere safe — each works once if they lose access to their authenticator.",
		}
		return mcpToolJSON(body)

	case "totp_disable":
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
			return mcpToolError("not signed in — run `yaver auth` first")
		}
		var args struct {
			Code string `json:"code"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Code) == "" {
			return mcpToolError("code is required")
		}
		var out struct {
			OK bool `json:"ok"`
		}
		if err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/disable", map[string]string{"code": strings.TrimSpace(args.Code)}, &out); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("2FA disabled")

	// --- System & Config ---
	case "get_system_info":
		status := s.taskMgr.GetAgentStatus()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Hostname: %s\n", status.System.Hostname))
		sb.WriteString(fmt.Sprintf("OS: %s/%s\n", status.System.OS, status.System.Arch))
		if status.System.MemoryMB > 0 {
			sb.WriteString(fmt.Sprintf("Memory: %d MB\n", status.System.MemoryMB))
		}
		sb.WriteString(fmt.Sprintf("Runner: %s (%s) — %s\n", status.Runner.Name, status.Runner.ID, func() string {
			if status.Runner.Installed {
				return "installed"
			}
			return "not installed"
		}()))
		sb.WriteString(fmt.Sprintf("Running tasks: %d / %d total\n", status.RunningTasks, status.TotalTasks))
		sb.WriteString(fmt.Sprintf("Work dir: %s\n", s.taskMgr.workDir))
		sb.WriteString(fmt.Sprintf("Version: %s\n", version))
		return mcpToolResult(sb.String())

	case "get_config":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		// Redact sensitive fields
		safeCfg := map[string]interface{}{
			"auto_start":       cfg.AutoStart,
			"auto_update":      cfg.AutoUpdate,
			"relay_count":      len(cfg.RelayServers),
			"acl_peers":        len(cfg.ACLPeers),
			"email_configured": cfg.Email != nil && cfg.Email.Provider != "",
		}
		if cfg.Sandbox != nil {
			safeCfg["sandbox"] = map[string]interface{}{
				"enabled":    cfg.Sandbox.Enabled,
				"allow_sudo": cfg.Sandbox.AllowSudo,
			}
		} else {
			safeCfg["sandbox"] = "default (enabled, no sudo)"
		}
		data, _ := json.MarshalIndent(safeCfg, "", "  ")
		return mcpToolResult(string(data))

	case "set_work_dir":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Path == "" {
			return mcpToolError("path is required")
		}
		info, err := os.Stat(args.Path)
		if err != nil {
			return mcpToolError(fmt.Sprintf("path not accessible: %v", err))
		}
		if !info.IsDir() {
			return mcpToolError("path is not a directory")
		}
		if err := ValidateWorkDir(args.Path, s.taskMgr.Sandbox); err != nil {
			return mcpToolError(err.Error())
		}
		s.taskMgr.mu.Lock()
		s.taskMgr.workDir = args.Path
		s.taskMgr.mu.Unlock()
		log.Printf("[MCP] Work dir changed to %s", args.Path)
		return mcpToolResult(fmt.Sprintf("Working directory changed to: %s", args.Path))

	case "get_ios_install_method":
		resolved, reason := resolveIOSInstallMethodWithReason(s.iosInstallMethod)
		return mcpToolResult(fmt.Sprintf("iOS install method: %s\nResolved: %s\nReason: %s\nPlatform: %s\nXcode available: %v",
			s.iosInstallMethod, resolved, reason, runtime.GOOS, canDoNativeInstall()))

	case "set_ios_install_method":
		var args struct {
			Method  string `json:"method"`
			Persist *bool  `json:"persist"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Method != IOSInstallAuto && args.Method != IOSInstallNative && args.Method != IOSInstallBundle {
			return mcpToolError("method must be auto, native, or bundle")
		}
		s.iosInstallMethod = args.Method
		resolved, reason := resolveIOSInstallMethodWithReason(args.Method)
		log.Printf("[MCP] iOS install method set to %s (resolved: %s, reason: %s)", args.Method, resolved, reason)

		// Persist to config by default
		shouldPersist := args.Persist == nil || *args.Persist
		if shouldPersist {
			cfg, err := LoadConfig()
			if err == nil {
				cfg.IOSInstallMethod = args.Method
				SaveConfig(cfg)
			}
		}
		return mcpToolResult(fmt.Sprintf("iOS install method set to: %s (resolved: %s)\nReason: %s", args.Method, resolved, reason))

	case "list_projects":
		fp, err := projectsFilePath()
		if err != nil {
			return mcpToolError(fmt.Sprintf("projects file: %v", err))
		}
		data, err := os.ReadFile(fp)
		if err != nil {
			if os.IsNotExist(err) {
				return mcpToolResult("No projects discovered yet. Run 'yaver discover' to scan.")
			}
			return mcpToolError(fmt.Sprintf("read projects: %v", err))
		}
		content := string(data)
		if len(content) > 5000 {
			content = content[:5000] + "\n... (truncated)"
		}
		return mcpToolResult(content)

	case "publish_config_get":
		var args struct {
			Dir string `json:"dir"`
		}
		json.Unmarshal(call.Arguments, &args)
		dir := strings.TrimSpace(args.Dir)
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		cfg, exists, err := loadOrScaffoldPublishConfig(dir)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{
			"ok":     true,
			"exists": exists,
			"path":   publishConfigPath(dir),
			"config": cfg,
		})

	case "publish_run", "publish_submit", "publish_upload", "publish_ci_dispatch":
		if s.publishMgr == nil {
			return mcpToolError("publish manager unavailable")
		}
		var args struct {
			Dir                 string `json:"dir"`
			Target              string `json:"target"`
			AllowGitHubFallback bool   `json:"allow_github_fallback"`
		}
		json.Unmarshal(call.Arguments, &args)
		dir := strings.TrimSpace(args.Dir)
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		run, err := s.publishMgr.StartRun(dir, args.Target, args.AllowGitHubFallback)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "run": run})

	case "publish_list":
		if s.publishMgr == nil {
			return mcpToolError("publish manager unavailable")
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "runs": s.publishMgr.ListRuns()})

	case "publish_status":
		if s.publishMgr == nil {
			return mcpToolError("publish manager unavailable")
		}
		var args struct {
			RunID string `json:"run_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.RunID) == "" {
			return mcpToolError("run_id is required")
		}
		run, ok := s.publishMgr.GetRun(args.RunID)
		if !ok {
			return mcpToolError("publish run not found: " + args.RunID)
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "run": run})

	// --- Relay Management ---
	case "get_relay_config":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		if len(cfg.RelayServers) == 0 {
			return mcpToolResult("No relay servers configured. Use add_relay_server to add one.")
		}
		var sb strings.Builder
		for _, rs := range cfg.RelayServers {
			sb.WriteString(fmt.Sprintf("- [%s] %s", rs.ID, rs.QuicAddr))
			if rs.Label != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", rs.Label))
			}
			if rs.Region != "" {
				sb.WriteString(fmt.Sprintf(" region=%s", rs.Region))
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(sb.String())

	case "add_relay_server":
		var args struct {
			QuicAddr string `json:"quic_addr"`
			HttpURL  string `json:"http_url"`
			Password string `json:"password"`
			Label    string `json:"label"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.QuicAddr == "" {
			return mcpToolError("quic_addr is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		newRelay := RelayServerConfig{
			ID:       fmt.Sprintf("relay-%d", len(cfg.RelayServers)+1),
			QuicAddr: args.QuicAddr,
			HttpURL:  args.HttpURL,
			Password: args.Password,
			Label:    args.Label,
		}
		cfg.RelayServers = append(cfg.RelayServers, newRelay)
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		log.Printf("[MCP] Relay server added: %s", args.QuicAddr)
		return mcpToolResult(fmt.Sprintf("Relay server added: %s (ID: %s). Restart agent to connect.", args.QuicAddr, newRelay.ID))

	case "remove_relay_server":
		var args struct {
			RelayID string `json:"relay_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.RelayID == "" {
			return mcpToolError("relay_id is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		found := false
		var remaining []RelayServerConfig
		for _, rs := range cfg.RelayServers {
			if rs.ID == args.RelayID {
				found = true
				continue
			}
			remaining = append(remaining, rs)
		}
		if !found {
			return mcpToolError("relay server not found: " + args.RelayID)
		}
		cfg.RelayServers = remaining
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Relay server %s removed. Restart agent to apply.", args.RelayID))

	// --- Filesystem ---
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Path == "" {
			return mcpToolError("path is required")
		}
		filePath := s.resolveFilePath(args.Path)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return mcpToolError(fmt.Sprintf("read file: %v", err))
		}
		content := string(data)
		if len(content) > 100*1024 {
			content = content[:100*1024] + "\n... (truncated at 100KB)"
		}
		return mcpToolResult(content)

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Path == "" || args.Content == "" {
			return mcpToolError("path and content are required")
		}
		filePath := s.resolveFilePath(args.Path)
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return mcpToolError(fmt.Sprintf("create directory: %v", err))
		}
		if err := os.WriteFile(filePath, []byte(args.Content), 0644); err != nil {
			return mcpToolError(fmt.Sprintf("write file: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("File written: %s (%d bytes)", filePath, len(args.Content)))

	case "list_directory":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		dirPath := s.taskMgr.workDir
		if args.Path != "" {
			dirPath = s.resolveFilePath(args.Path)
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list directory: %v", err))
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Directory: %s\n\n", dirPath))
		for _, e := range entries {
			info, _ := e.Info()
			if info != nil {
				if info.IsDir() {
					sb.WriteString(fmt.Sprintf("  %s/\n", e.Name()))
				} else {
					sb.WriteString(fmt.Sprintf("  %s (%d bytes)\n", e.Name(), info.Size()))
				}
			}
		}
		return mcpToolResult(sb.String())

	case "search_files":
		var args struct {
			Pattern    string `json:"pattern"`
			Directory  string `json:"directory"`
			MaxResults int    `json:"max_results"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Pattern == "" {
			return mcpToolError("pattern is required")
		}
		dir := args.Directory
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		return mcpToolResult(searchFiles(dir, args.Pattern, args.MaxResults))

	case "search_content":
		var args struct {
			Query      string `json:"query"`
			Directory  string `json:"directory"`
			MaxResults int    `json:"max_results"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Query == "" {
			return mcpToolError("query is required")
		}
		dir := args.Directory
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		return mcpToolResult(searchFileContent(dir, args.Query, args.MaxResults))

	case "screenshot":
		img, err := captureScreen()
		if err != nil {
			return mcpToolError(err.Error())
		}
		// Return as text with base64 — MCP clients can render images
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "image", "data": img, "mimeType": "image/png"},
			},
		}

	case "system_info":
		return mcpToolResult(getSystemInfo())

	case "git_info":
		var args struct {
			Operation string `json:"operation"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Operation == "" {
			return mcpToolError("operation is required (status, diff, log, branch, remote)")
		}
		dir := args.Directory
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		return mcpToolResult(gitInfo(dir, args.Operation))

	// --- Email ---
	case "email_list_inbox":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			Folder string `json:"folder"`
			Search string `json:"search"`
			Limit  int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Limit <= 0 {
			args.Limit = 20
		}
		if args.Folder == "" {
			args.Folder = "inbox"
		}
		emails, err := s.emailMgr.ListInbox(args.Folder, args.Search, args.Limit)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list inbox: %v", err))
		}
		if len(emails) == 0 {
			return mcpToolResult("No emails found.")
		}
		data, _ := json.MarshalIndent(emails, "", "  ")
		return mcpToolResult(string(data))

	case "email_get":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			EmailID string `json:"email_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.EmailID == "" {
			return mcpToolError("email_id is required")
		}
		email, err := s.emailMgr.GetEmail(args.EmailID)
		if err != nil {
			return mcpToolError(fmt.Sprintf("get email: %v", err))
		}
		data, _ := json.MarshalIndent(email, "", "  ")
		return mcpToolResult(string(data))

	case "email_send":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
			Body    string `json:"body"`
			CC      string `json:"cc"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.To == "" || args.Subject == "" || args.Body == "" {
			return mcpToolError("to, subject, and body are required")
		}
		if err := s.emailMgr.SendEmail(args.To, args.Subject, args.Body, args.CC); err != nil {
			return mcpToolError(fmt.Sprintf("send email: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Email sent to %s: %s", args.To, args.Subject))

	case "email_sync":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		count, err := s.emailMgr.SyncEmails()
		if err != nil {
			return mcpToolError(fmt.Sprintf("sync failed: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Synced %d emails to local database.", count))

	case "email_search":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Query == "" {
			return mcpToolError("query is required")
		}
		if args.Limit <= 0 {
			args.Limit = 20
		}
		emails, err := s.emailMgr.SearchEmails(args.Query, args.Limit)
		if err != nil {
			return mcpToolError(fmt.Sprintf("search: %v", err))
		}
		if len(emails) == 0 {
			return mcpToolResult("No emails found matching query.")
		}
		data, _ := json.MarshalIndent(emails, "", "  ")
		return mcpToolResult(string(data))

	// --- ACL (Agent Communication Layer) ---
	case "acl_list_peers":
		if s.aclMgr == nil {
			return mcpToolResult("ACL not initialized. No peers configured.")
		}
		peers := s.aclMgr.ListPeers()
		if len(peers) == 0 {
			return mcpToolResult("No MCP peers connected. Use acl_add_peer to connect to another MCP server.")
		}
		data, _ := json.MarshalIndent(peers, "", "  ")
		return mcpToolResult(string(data))

	case "acl_add_peer":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Auth string `json:"auth"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Name == "" || args.URL == "" {
			return mcpToolError("name and url are required")
		}
		peer := ACLPeerConfig{
			ID:   strings.ToLower(strings.ReplaceAll(args.Name, " ", "-")),
			Name: args.Name,
			URL:  args.URL,
			Type: "http",
			Auth: args.Auth,
		}
		if err := s.aclMgr.AddPeer(peer); err != nil {
			return mcpToolError(fmt.Sprintf("add peer: %v", err))
		}
		// Persist to config
		cfg, _ := LoadConfig()
		if cfg != nil {
			cfg.ACLPeers = append(cfg.ACLPeers, peer)
			SaveConfig(cfg)
		}
		return mcpToolResult(fmt.Sprintf("Connected to MCP peer: %s (%s)", args.Name, args.URL))

	case "acl_remove_peer":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			PeerID string `json:"peer_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.PeerID == "" {
			return mcpToolError("peer_id is required")
		}
		if err := s.aclMgr.RemovePeer(args.PeerID); err != nil {
			return mcpToolError(err.Error())
		}
		// Persist removal to config
		cfg, _ := LoadConfig()
		if cfg != nil {
			var remaining []ACLPeerConfig
			for _, p := range cfg.ACLPeers {
				if p.ID != args.PeerID {
					remaining = append(remaining, p)
				}
			}
			cfg.ACLPeers = remaining
			SaveConfig(cfg)
		}
		return mcpToolResult(fmt.Sprintf("Disconnected from peer: %s", args.PeerID))

	case "acl_list_peer_tools":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			PeerID string `json:"peer_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.PeerID == "" {
			return mcpToolError("peer_id is required")
		}
		tools, err := s.aclMgr.ListPeerTools(args.PeerID)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list tools: %v", err))
		}
		data, _ := json.MarshalIndent(tools, "", "  ")
		return mcpToolResult(string(data))

	case "acl_call_peer_tool":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			PeerID    string          `json:"peer_id"`
			ToolName  string          `json:"tool_name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.PeerID == "" || args.ToolName == "" {
			return mcpToolError("peer_id and tool_name are required")
		}
		result, err := s.aclMgr.CallPeerTool(args.PeerID, args.ToolName, args.Arguments)
		if err != nil {
			return mcpToolError(fmt.Sprintf("call tool: %v", err))
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcpToolResult(string(data))

	case "acl_health":
		if s.aclMgr == nil {
			return mcpToolResult("ACL not initialized. No peers configured.")
		}
		health := s.aclMgr.HealthCheck()
		var sb strings.Builder
		for id, ok := range health {
			status := "healthy"
			if !ok {
				status = "unreachable"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s\n", id, status))
		}
		return mcpToolResult(sb.String())

	// --- Tmux Session Management ---
	case "tmux_list_sessions":
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolResult("Tmux is not available on this machine. Install tmux to use session adoption.")
		}
		sessions, err := tmuxMgr.ListTmuxSessions()
		if err != nil {
			return mcpToolError(fmt.Sprintf("list sessions: %v", err))
		}
		if len(sessions) == 0 {
			return mcpToolResult("No tmux sessions found.")
		}
		var sb strings.Builder
		sb.WriteString("Tmux sessions:\n")
		for _, s := range sessions {
			agent := s.AgentType
			if agent == "" {
				agent = "shell"
			}
			sb.WriteString(fmt.Sprintf("- %s [%s] %s, %d window(s)", s.Name, s.Relationship, agent, s.Windows))
			if s.TaskID != "" {
				sb.WriteString(fmt.Sprintf(", task=%s", s.TaskID))
			}
			if s.Attached {
				sb.WriteString(" (attached)")
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(sb.String())

	case "tmux_adopt_session":
		var args struct {
			SessionName string `json:"session_name"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionName == "" {
			return mcpToolError("session_name is required")
		}
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolError("tmux is not available on this machine")
		}
		task, err := tmuxMgr.AdoptSession(args.SessionName)
		if err != nil {
			return mcpToolError(fmt.Sprintf("adopt failed: %v", err))
		}
		log.Printf("[MCP] Adopted tmux session %q as task %s", args.SessionName, task.ID)
		return mcpToolResult(fmt.Sprintf("Adopted tmux session %q as task %s.\nStatus: %s\nRunner: %s", args.SessionName, task.ID, task.Status, task.RunnerID))

	case "tmux_detach_session":
		var args struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" {
			return mcpToolError("task_id is required")
		}
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolError("tmux is not available on this machine")
		}
		if err := tmuxMgr.DetachSession(args.TaskID); err != nil {
			return mcpToolError(fmt.Sprintf("detach failed: %v", err))
		}
		log.Printf("[MCP] Detached tmux session (task %s)", args.TaskID)
		return mcpToolResult(fmt.Sprintf("Detached task %s. The tmux session continues running.", args.TaskID))

	case "tmux_send_input":
		var args struct {
			TaskID string `json:"task_id"`
			Input  string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" {
			return mcpToolError("task_id is required")
		}
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolError("tmux is not available on this machine")
		}
		if err := tmuxMgr.SendTmuxInput(args.TaskID, args.Input); err != nil {
			return mcpToolError(fmt.Sprintf("send input failed: %v", err))
		}
		return mcpToolResult("Input sent to tmux session.")

	// --- Diagnostics & Status ---
	case "yaver_doctor":
		return s.mcpDoctor()

	case "yaver_status":
		return s.mcpStatus()

	case "yaver_devices":
		cfg, err := LoadConfig()
		if err != nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
			return mcpToolError("Not signed in. Run 'yaver auth' first.")
		}
		devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list devices: %v", err))
		}
		if len(devices) == 0 {
			return mcpToolResult("No devices registered. Run 'yaver serve' on your dev machine to register it.")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%-10s  %-22s  %-8s  %-8s  %-22s  %s\n",
			"ID", "NAME", "PLATFORM", "STATUS", "ACCESS", "ADDRESS"))
		for _, d := range devices {
			status := "offline"
			if d.IsOnline {
				status = "online"
			}
			id := d.DeviceID
			if len(id) > 8 {
				id = id[:8] + "..."
			}
			access := "OWN"
			if d.IsGuest {
				label := "SHARED"
				if d.HostName != "" {
					label = "SHARED:" + d.HostName
				} else if d.HostEmail != "" {
					label = "SHARED:" + d.HostEmail
				}
				if len(label) > 22 {
					label = label[:21] + "…"
				}
				access = label
			}
			sb.WriteString(fmt.Sprintf("%-10s  %-22s  %-8s  %-8s  %-22s  %s:%d\n",
				id, d.Name, d.Platform, status, access, d.QuicHost, d.QuicPort))
		}
		return mcpToolResult(sb.String())

	case "yaver_logs":
		var args struct {
			Lines int `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Lines <= 0 {
			args.Lines = 50
		}
		if args.Lines > 500 {
			args.Lines = 500
		}
		lp := logFilePath()
		if lp == "" {
			return mcpToolError("Could not determine log file path")
		}
		out, err := osexec.Command("tail", "-n", fmt.Sprintf("%d", args.Lines), lp).CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "No such file") {
				return mcpToolResult("No logs found. Start the agent with 'yaver serve'.")
			}
			return mcpToolError(fmt.Sprintf("read logs: %v: %s", err, string(out)))
		}
		return mcpToolResult(string(out))

	case "yaver_clear_logs":
		lp := logFilePath()
		if lp == "" {
			return mcpToolError("Could not determine log file path")
		}
		if err := os.Truncate(lp, 0); err != nil {
			if os.IsNotExist(err) {
				return mcpToolResult("No log file to clear.")
			}
			return mcpToolError(fmt.Sprintf("clear logs: %v", err))
		}
		log.Printf("[MCP] Logs cleared")
		return mcpToolResult("Agent logs cleared.")

	case "yaver_help":
		var args struct {
			Topic string `json:"topic"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolResult(yaverHelpText(args.Topic))

	case "yaver_ping":
		hostname, _ := os.Hostname()
		return mcpToolResult(fmt.Sprintf("Pong! Agent is alive.\nHostname: %s\nVersion: %s\nWork Dir: %s", hostname, version, s.taskMgr.workDir))

	case "agent_shutdown":
		var args struct {
			Confirm bool `json:"confirm"`
		}
		json.Unmarshal(call.Arguments, &args)
		if !args.Confirm {
			return mcpToolError("You must pass confirm: true to shut down the agent.")
		}
		log.Printf("[MCP] Shutdown requested")
		// Trigger shutdown after returning the response
		go func() {
			time.Sleep(500 * time.Millisecond)
			if s.onShutdown != nil {
				s.onShutdown()
			}
		}()
		return mcpToolResult("Agent shutdown initiated. All running tasks will be stopped.")

	case "infra_summary":
		return mcpToolJSON(s.infraSummary(context.Background()))

	case "infra_service_action":
		var args struct {
			Scope  string `json:"scope"`
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		switch strings.TrimSpace(args.Scope) {
		case "dev":
			workDir := "."
			if s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" {
				workDir = s.taskMgr.workDir
			}
			sm := NewServicesManager(workDir)
			switch strings.TrimSpace(args.Action) {
			case "start":
				msg, err := sm.Start(args.Name)
				if err != nil {
					return mcpToolError(err.Error())
				}
				return mcpToolJSON(map[string]interface{}{"ok": true, "output": msg})
			case "stop":
				msg, err := sm.Stop(args.Name)
				if err != nil {
					return mcpToolError(err.Error())
				}
				return mcpToolJSON(map[string]interface{}{"ok": true, "output": msg})
			case "restart":
				if _, err := sm.Stop(args.Name); err != nil {
					return mcpToolError(err.Error())
				}
				msg, err := sm.Start(args.Name)
				if err != nil {
					return mcpToolError(err.Error())
				}
				return mcpToolJSON(map[string]interface{}{"ok": true, "output": msg})
			case "status":
				statuses, err := sm.Status()
				if err != nil {
					return mcpToolError(err.Error())
				}
				for _, status := range statuses {
					if status.Name == args.Name {
						return mcpToolJSON(status)
					}
				}
				return mcpToolError("service not found")
			default:
				return mcpToolError("unsupported dev service action")
			}
		case "system":
			if strings.TrimSpace(args.Action) == "status" {
				return mcpToolJSON(mcpServiceStatus(args.Name))
			}
			return mcpToolJSON(mcpServiceAction(args.Name, args.Action))
		default:
			return mcpToolError("scope must be dev or system")
		}

	case "infra_power":
		var args struct {
			Action  string `json:"action"`
			Confirm bool   `json:"confirm"`
		}
		json.Unmarshal(call.Arguments, &args)
		if !args.Confirm {
			return mcpToolError("confirm must be true")
		}
		switch strings.TrimSpace(args.Action) {
		case "agent_shutdown":
			log.Printf("[MCP] Infra shutdown requested")
			go func() {
				time.Sleep(500 * time.Millisecond)
				if s.onShutdown != nil {
					s.onShutdown()
				}
			}()
			return mcpToolJSON(map[string]interface{}{"ok": true, "action": args.Action})
		case "host_reboot":
			command, err := infraHostReboot()
			if err != nil {
				return mcpToolError(err.Error())
			}
			return mcpToolJSON(map[string]interface{}{"ok": true, "action": args.Action, "command": command})
		default:
			return mcpToolError("unsupported power action")
		}

	case "machine_remove":
		var args struct {
			Confirm bool   `json:"confirm"`
			Phrase  string `json:"phrase"`
		}
		json.Unmarshal(call.Arguments, &args)
		if !args.Confirm {
			return mcpToolError("confirm must be true")
		}
		if !machineRemovalPhraseValid(args.Phrase) {
			return mcpToolError(fmt.Sprintf("phrase must equal %q", machineRemovalPhrase))
		}
		streamName := fmt.Sprintf("machine-remove:%d", time.Now().UnixNano())
		progress := newMachineRemoveStreamProgress(s, streamName)
		schedulePermanentMachineRemoval(s.onShutdown, progress)
		return mcpToolJSON(map[string]interface{}{
			"ok":          true,
			"action":      "machine_remove",
			"phase":       "scheduled",
			"stream":      streamName,
			"manualSteps": machineRemovalManualSteps(),
		})

	// --- Config Management ---
	case "config_set":
		var args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Key == "" || args.Value == "" {
			return mcpToolError("key and value are required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		switch args.Key {
		case "auto-start":
			cfg.AutoStart = args.Value == "true" || args.Value == "1" || args.Value == "yes"
			if err := SaveConfig(cfg); err != nil {
				return mcpToolError(fmt.Sprintf("save config: %v", err))
			}
			return mcpToolResult(fmt.Sprintf("auto-start set to %v", cfg.AutoStart))
		case "auto-update":
			cfg.AutoUpdate = args.Value == "true" || args.Value == "1" || args.Value == "yes"
			if err := SaveConfig(cfg); err != nil {
				return mcpToolError(fmt.Sprintf("save config: %v", err))
			}
			return mcpToolResult(fmt.Sprintf("auto-update set to %v", cfg.AutoUpdate))
		case "headless-keep-awake":
			enabled := args.Value == "true" || args.Value == "1" || args.Value == "yes"
			cfg.HeadlessKeepAwake = &enabled
			if err := SaveConfig(cfg); err != nil {
				return mcpToolError(fmt.Sprintf("save config: %v", err))
			}
			return mcpToolResult(fmt.Sprintf("headless-keep-awake set to %v", enabled))
		case "require-private-recovery":
			enabled := args.Value == "true" || args.Value == "1" || args.Value == "yes"
			cfg.RequirePrivateRecoveryTransport = enabled
			if err := SaveConfig(cfg); err != nil {
				return mcpToolError(fmt.Sprintf("save config: %v", err))
			}
			if enabled {
				return mcpToolResult("require-private-recovery enabled; /auth/recover now rejects direct public HTTP")
			}
			return mcpToolResult("require-private-recovery disabled; /auth/recover is back to default open mode")
		default:
			return mcpToolError(fmt.Sprintf("Unknown config key: %s. Supported: auto-start, auto-update, headless-keep-awake, require-private-recovery", args.Key))
		}

	case "relay_test":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		var urls []string
		if args.URL != "" {
			urls = []string{strings.TrimRight(args.URL, "/")}
		} else {
			cfg, err := LoadConfig()
			if err != nil {
				return mcpToolError(fmt.Sprintf("load config: %v", err))
			}
			for _, rs := range cfg.RelayServers {
				urls = append(urls, rs.HttpURL)
			}
			if len(urls) == 0 {
				return mcpToolResult("No relay servers configured. Use add_relay_server or pass a URL.")
			}
		}
		client := &http.Client{Timeout: 10 * time.Second}
		var sb strings.Builder
		for _, u := range urls {
			start := time.Now()
			resp, err := client.Get(u + "/health")
			rtt := time.Since(start)
			if err != nil {
				sb.WriteString(fmt.Sprintf("FAIL  %s  error: %v\n", u, err))
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				sb.WriteString(fmt.Sprintf("OK    %s  %dms\n", u, rtt.Milliseconds()))
			} else {
				sb.WriteString(fmt.Sprintf("FAIL  %s  status: %d\n", u, resp.StatusCode))
			}
		}
		return mcpToolResult(sb.String())

	case "relay_set_password":
		var args struct {
			Password string `json:"password"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Password == "" {
			return mcpToolError("password is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		cfg.RelayPassword = args.Password
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		signalRunningAgent()
		log.Printf("[MCP] Relay password set")
		return mcpToolResult("Relay password saved. Agent notified.")

	case "relay_clear_password":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		if cfg.RelayPassword == "" {
			return mcpToolResult("No relay password was set.")
		}
		cfg.RelayPassword = ""
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		signalRunningAgent()
		log.Printf("[MCP] Relay password cleared")
		return mcpToolResult("Relay password cleared. Agent notified.")

	// --- Tunnel Management ---
	case "tunnel_list":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		if len(cfg.CloudflareTunnels) == 0 {
			return mcpToolResult("No Cloudflare Tunnels configured.\nAdd one with: yaver tunnel add <url>")
		}
		var sb strings.Builder
		sb.WriteString("Cloudflare Tunnels:\n")
		for _, t := range cfg.CloudflareTunnels {
			cfAccess := "no"
			if t.CFAccessClientId != "" {
				cfAccess = "yes"
			}
			label := t.Label
			if label == "" {
				label = "-"
			}
			sb.WriteString(fmt.Sprintf("- %s  %s  (CF Access: %s, label: %s)\n", t.ID, t.URL, cfAccess, label))
		}
		return mcpToolResult(sb.String())

	case "tunnel_add":
		var args struct {
			URL            string `json:"url"`
			CFClientId     string `json:"cf_client_id"`
			CFClientSecret string `json:"cf_client_secret"`
			Label          string `json:"label"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.URL == "" {
			return mcpToolError("url is required")
		}
		rawURL := strings.TrimRight(args.URL, "/")
		id := fmt.Sprintf("%x", func() uint32 {
			var h uint32
			for _, c := range rawURL {
				h = h*31 + uint32(c)
			}
			return h
		}())
		if len(id) > 8 {
			id = id[:8]
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		for _, t := range cfg.CloudflareTunnels {
			if t.URL == rawURL {
				return mcpToolError(fmt.Sprintf("Tunnel already configured: %s (id: %s)", rawURL, t.ID))
			}
		}
		tunnel := CloudflareTunnelConfig{
			ID:                   id,
			URL:                  rawURL,
			CFAccessClientId:     args.CFClientId,
			CFAccessClientSecret: args.CFClientSecret,
			Label:                args.Label,
			Priority:             len(cfg.CloudflareTunnels) + 1,
		}
		cfg.CloudflareTunnels = append(cfg.CloudflareTunnels, tunnel)
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		log.Printf("[MCP] Added Cloudflare Tunnel: %s", rawURL)
		return mcpToolResult(fmt.Sprintf("Added Cloudflare Tunnel:\n  ID: %s\n  URL: %s\n  CF Access: %v", id, rawURL, args.CFClientId != ""))

	case "tunnel_remove":
		var args struct {
			TunnelID string `json:"tunnel_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TunnelID == "" {
			return mcpToolError("tunnel_id is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		found := false
		var remaining []CloudflareTunnelConfig
		for _, t := range cfg.CloudflareTunnels {
			if t.ID == args.TunnelID || t.URL == args.TunnelID {
				found = true
				log.Printf("[MCP] Removed Cloudflare Tunnel: %s (%s)", t.URL, t.ID)
			} else {
				remaining = append(remaining, t)
			}
		}
		if !found {
			return mcpToolError(fmt.Sprintf("Tunnel not found: %s", args.TunnelID))
		}
		cfg.CloudflareTunnels = remaining
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Removed tunnel: %s", args.TunnelID))

	case "tunnel_test":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		var tunnels []CloudflareTunnelConfig
		if args.URL != "" {
			tunnels = []CloudflareTunnelConfig{{URL: strings.TrimRight(args.URL, "/")}}
		} else {
			cfg, err := LoadConfig()
			if err != nil {
				return mcpToolError(fmt.Sprintf("load config: %v", err))
			}
			tunnels = cfg.CloudflareTunnels
			if len(tunnels) == 0 {
				return mcpToolResult("No tunnels configured. Pass a URL or add with tunnel_add.")
			}
		}
		client := &http.Client{Timeout: 10 * time.Second}
		var sb strings.Builder
		for _, t := range tunnels {
			req, _ := http.NewRequest("GET", t.URL+"/health", nil)
			if t.CFAccessClientId != "" {
				req.Header.Set("CF-Access-Client-Id", t.CFAccessClientId)
				req.Header.Set("CF-Access-Client-Secret", t.CFAccessClientSecret)
			}
			start := time.Now()
			resp, err := client.Do(req)
			rtt := time.Since(start)
			if err != nil {
				sb.WriteString(fmt.Sprintf("FAIL  %s  error: %v\n", t.URL, err))
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				sb.WriteString(fmt.Sprintf("OK    %s  %dms\n", t.URL, rtt.Milliseconds()))
			} else {
				sb.WriteString(fmt.Sprintf("FAIL  %s  status: %d\n", t.URL, resp.StatusCode))
			}
		}
		return mcpToolResult(sb.String())

	// --- Session Transfer ---
	case "session_list":
		sessions := ListTransferableSessions(s.taskMgr)
		if len(sessions) == 0 {
			return mcpToolResult("No transferable sessions found.")
		}
		var sb strings.Builder
		sb.WriteString("Transferable sessions:\n\n")
		for _, sess := range sessions {
			resumable := ""
			if sess.Resumable {
				resumable = " [resumable]"
			}
			sb.WriteString(fmt.Sprintf("- %s (%s) — \"%s\" [%s, %d turns]%s\n",
				sess.TaskID, sess.AgentType, sess.Title, sess.Status, sess.Turns, resumable))
			if sess.GitRemote != "" {
				sb.WriteString(fmt.Sprintf("  Git: %s @ %s\n", sess.GitRemote, sess.GitBranch))
			}
		}
		return mcpToolResult(sb.String())

	case "session_export":
		var args struct {
			TaskID           string `json:"task_id"`
			IncludeWorkspace bool   `json:"include_workspace"`
			WorkspaceMode    string `json:"workspace_mode"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" {
			return mcpToolError("task_id is required")
		}
		bundle, err := ExportSession(s.taskMgr, args.TaskID, ExportOptions{
			IncludeWorkspace: args.IncludeWorkspace,
			WorkspaceMode:    args.WorkspaceMode,
		})
		if err != nil {
			return mcpToolError(err.Error())
		}
		bundleJSON, _ := json.MarshalIndent(bundle, "", "  ")
		return mcpToolResult(fmt.Sprintf("Session exported (%d bytes, %d turns, agent=%s).\n\nBundle JSON:\n%s",
			len(bundleJSON), len(bundle.Task.Turns), bundle.AgentType, string(bundleJSON)))

	case "session_import":
		var args struct {
			BundleJSON string `json:"bundle_json"`
			WorkDir    string `json:"work_dir"`
			GitClone   bool   `json:"git_clone"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.BundleJSON == "" {
			return mcpToolError("bundle_json is required")
		}
		var bundle TransferBundle
		if err := json.Unmarshal([]byte(args.BundleJSON), &bundle); err != nil {
			return mcpToolError(fmt.Sprintf("invalid bundle JSON: %v", err))
		}
		taskID, warnings, err := ImportSession(s.taskMgr, &bundle, ImportOptions{
			WorkDir:  args.WorkDir,
			GitClone: args.GitClone,
		})
		if err != nil {
			return mcpToolError(err.Error())
		}
		result := fmt.Sprintf("Session imported! Task ID: %s", taskID)
		if len(warnings) > 0 {
			result += "\n\nWarnings:\n"
			for _, w := range warnings {
				result += "- " + w + "\n"
			}
		}
		return mcpToolResult(result)

	case "session_transfer":
		var args struct {
			TaskID           string `json:"task_id"`
			TargetDevice     string `json:"target_device"`
			IncludeWorkspace bool   `json:"include_workspace"`
			WorkspaceMode    string `json:"workspace_mode"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" || args.TargetDevice == "" {
			return mcpToolError("task_id and target_device are required")
		}
		// Export
		bundle, err := ExportSession(s.taskMgr, args.TaskID, ExportOptions{
			IncludeWorkspace: args.IncludeWorkspace,
			WorkspaceMode:    args.WorkspaceMode,
		})
		if err != nil {
			return mcpToolError(fmt.Sprintf("export failed: %v", err))
		}
		// Find target device
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		targetURL := resolveDeviceURL(cfg, args.TargetDevice, true)
		// Send to target
		importBody, _ := json.Marshal(map[string]interface{}{
			"bundle":   bundle,
			"gitClone": args.WorkspaceMode == "git",
		})
		req, _ := http.NewRequest("POST", targetURL+"/session/import", bytes.NewReader(importBody))
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return mcpToolError(fmt.Sprintf("transfer failed: %v", err))
		}
		defer resp.Body.Close()
		var importResp struct {
			OK       bool     `json:"ok"`
			TaskID   string   `json:"taskId"`
			Warnings []string `json:"warnings"`
			Error    string   `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&importResp)
		if !importResp.OK {
			return mcpToolError(fmt.Sprintf("import failed on target: %s", importResp.Error))
		}
		result := fmt.Sprintf("Session transferred successfully!\nSource: %s (task %s)\nTarget: %s (task %s)",
			s.hostname, args.TaskID, args.TargetDevice, importResp.TaskID)
		if len(importResp.Warnings) > 0 {
			result += "\n\nWarnings:\n"
			for _, w := range importResp.Warnings {
				result += "- " + w + "\n"
			}
		}
		log.Printf("[MCP] Session transferred: %s -> %s (task %s -> %s)", s.hostname, args.TargetDevice, args.TaskID, importResp.TaskID)
		return mcpToolResult(result)

	// --- Exec ---
	case "exec_command":
		var args struct {
			Command string `json:"command"`
			WorkDir string `json:"work_dir"`
			Timeout int    `json:"timeout"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Command == "" {
			return mcpToolError("command is required")
		}
		if s.execMgr == nil {
			return mcpToolError("exec is not enabled on this agent")
		}
		sess, err := s.execMgr.StartExec(args.Command, args.WorkDir, "", nil, args.Timeout)
		if err != nil {
			return mcpToolError(err.Error())
		}
		// Wait for completion (up to timeout)
		select {
		case <-sess.doneCh:
		case <-time.After(time.Duration(args.Timeout) * time.Second):
			if args.Timeout == 0 {
				<-sess.doneCh // wait forever if no timeout
			}
		}
		snapshot := sess.Snapshot()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Exit code: %v\n", snapshot["exitCode"]))
		if stdout, ok := snapshot["stdout"].(string); ok && stdout != "" {
			sb.WriteString("\n--- stdout ---\n")
			sb.WriteString(stdout)
		}
		if stderr, ok := snapshot["stderr"].(string); ok && stderr != "" {
			sb.WriteString("\n--- stderr ---\n")
			sb.WriteString(stderr)
		}
		return mcpToolResult(sb.String())

	case "schedule_task":
		var args struct {
			Title          string `json:"title"`
			RunAt          string `json:"run_at"`
			RepeatInterval int    `json:"repeat_interval"`
			Cron           string `json:"cron"`
			MaxRuns        int    `json:"max_runs"`
			Runner         string `json:"runner"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Title == "" {
			return mcpToolError("title is required")
		}
		if s.scheduler == nil {
			return mcpToolError("scheduler not available")
		}
		st := &ScheduledTask{
			Title:          args.Title,
			RunAt:          args.RunAt,
			RepeatInterval: args.RepeatInterval,
			Cron:           args.Cron,
			MaxRuns:        args.MaxRuns,
			Runner:         args.Runner,
		}
		if err := s.scheduler.AddSchedule(st); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Scheduled! ID: %s, Next run: %s", st.ID, st.NextRunAt))

	case "list_schedules":
		if s.scheduler == nil {
			return mcpToolError("scheduler not available")
		}
		schedules := s.scheduler.ListSchedules()
		if len(schedules) == 0 {
			return mcpToolResult("No scheduled tasks.")
		}
		var sb strings.Builder
		for _, st := range schedules {
			sb.WriteString(fmt.Sprintf("- %s [%s] \"%s\" next:%s runs:%d\n",
				st.ID, st.Status, st.Title, st.NextRunAt, st.RunCount))
		}
		return mcpToolResult(sb.String())

	case "cancel_schedule":
		var args struct {
			ScheduleID string `json:"schedule_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.scheduler == nil {
			return mcpToolError("scheduler not available")
		}
		if err := s.scheduler.RemoveSchedule(args.ScheduleID); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Schedule cancelled: " + args.ScheduleID)

	case "routine_create", "routine_list", "routine_get", "routine_delete",
		"routine_pause", "routine_resume", "routine_run_now", "routine_update":
		// Verb-mode routines route to a single dispatcher in
		// routines_mcp.go so future routine_* tools land there
		// rather than growing this switch.
		return s.handleRoutineMCP(call.Name, call.Arguments)

	case "notify":
		var args struct {
			Message string `json:"message"`
			Channel string `json:"channel"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Message == "" {
			return mcpToolError("message is required")
		}
		if s.notifyMgr == nil {
			return mcpToolError("notifications not configured")
		}
		result := s.notifyMgr.TestNotification(args.Channel)
		// Actually send the custom message
		if args.Channel == "" {
			s.notifyMgr.sendAll(args.Message)
		} else {
			switch strings.ToLower(args.Channel) {
			case "telegram":
				s.notifyMgr.sendTelegram(args.Message)
			case "discord":
				s.notifyMgr.sendDiscord(args.Message)
			case "slack":
				s.notifyMgr.sendSlack(args.Message)
			case "teams":
				s.notifyMgr.sendTeams(args.Message)
			}
		}
		return mcpToolResult("Notification sent: " + result)

	case "integrations_list":
		cfg, _ := LoadConfig()
		if cfg == nil || cfg.Notifications == nil {
			return mcpToolResult("No integrations configured.\n\nAvailable channels: telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email\n\nUse 'integrations_set' to configure.")
		}
		nc := cfg.Notifications
		var sb strings.Builder
		sb.WriteString("Configured integrations:\n\n")
		if nc.Telegram != nil {
			sb.WriteString(fmt.Sprintf("- Telegram: %s (chatId: %s)\n", boolStr(nc.Telegram.Enabled), nc.Telegram.ChatID))
		}
		if nc.Discord != nil {
			sb.WriteString(fmt.Sprintf("- Discord: %s\n", boolStr(nc.Discord.Enabled)))
		}
		if nc.Slack != nil {
			sb.WriteString(fmt.Sprintf("- Slack: %s\n", boolStr(nc.Slack.Enabled)))
		}
		if nc.Teams != nil {
			sb.WriteString(fmt.Sprintf("- Teams: %s\n", boolStr(nc.Teams.Enabled)))
		}
		if nc.Linear != nil {
			sb.WriteString(fmt.Sprintf("- Linear: %s (team: %s)\n", boolStr(nc.Linear.Enabled), nc.Linear.TeamID))
		}
		if nc.Jira != nil {
			sb.WriteString(fmt.Sprintf("- Jira: %s (project: %s)\n", boolStr(nc.Jira.Enabled), nc.Jira.ProjectKey))
		}
		if nc.PagerDuty != nil {
			sb.WriteString(fmt.Sprintf("- PagerDuty: %s (failOnly: %v)\n", boolStr(nc.PagerDuty.Enabled), nc.PagerDuty.OnFailOnly))
		}
		if nc.Opsgenie != nil {
			sb.WriteString(fmt.Sprintf("- Opsgenie: %s (failOnly: %v)\n", boolStr(nc.Opsgenie.Enabled), nc.Opsgenie.OnFailOnly))
		}
		if nc.Email != nil {
			sb.WriteString(fmt.Sprintf("- Email: %s (to: %s)\n", boolStr(nc.Email.Enabled), nc.Email.To))
		}
		if sb.Len() == len("Configured integrations:\n\n") {
			sb.WriteString("(none configured)\n")
		}
		sb.WriteString("\nAvailable: telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email")
		return mcpToolResult(sb.String())

	case "integrations_set":
		var args struct {
			Channel string          `json:"channel"`
			Config  json.RawMessage `json:"config"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Channel == "" {
			return mcpToolError("channel is required (telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email)")
		}
		cfg, _ := LoadConfig()
		if cfg == nil {
			cfg = &Config{}
		}
		if cfg.Notifications == nil {
			cfg.Notifications = &NotificationConfig{}
		}
		nc := cfg.Notifications
		ch := strings.ToLower(args.Channel)
		switch ch {
		case "telegram":
			var c TelegramConfig
			json.Unmarshal(args.Config, &c)
			nc.Telegram = &c
		case "discord":
			var c DiscordConfig
			json.Unmarshal(args.Config, &c)
			nc.Discord = &c
		case "slack":
			var c SlackConfig
			json.Unmarshal(args.Config, &c)
			nc.Slack = &c
		case "teams":
			var c TeamsConfig
			json.Unmarshal(args.Config, &c)
			nc.Teams = &c
		case "linear":
			var c LinearConfig
			json.Unmarshal(args.Config, &c)
			nc.Linear = &c
		case "jira":
			var c JiraConfig
			json.Unmarshal(args.Config, &c)
			nc.Jira = &c
		case "pagerduty":
			var c PagerDutyConfig
			json.Unmarshal(args.Config, &c)
			nc.PagerDuty = &c
		case "opsgenie":
			var c OpsgenieConfig
			json.Unmarshal(args.Config, &c)
			nc.Opsgenie = &c
		case "email":
			var c EmailNotifyConfig
			json.Unmarshal(args.Config, &c)
			nc.Email = &c
		default:
			return mcpToolError("unknown channel: " + ch)
		}
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError("failed to save config: " + err.Error())
		}
		if s.notifyMgr != nil {
			s.notifyMgr.UpdateConfig(nc)
		}
		return mcpToolResult(fmt.Sprintf("Integration '%s' configured and saved.", ch))

	case "integrations_test":
		var args struct {
			Channel string `json:"channel"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.notifyMgr == nil {
			return mcpToolError("notifications not configured")
		}
		result := s.notifyMgr.TestNotification(args.Channel)
		return mcpToolResult(result)

	// --- Docker ---
	case "docker_ps":
		return mcpToolJSON(mcpDockerPS())
	case "docker_logs":
		var args struct {
			Container string `json:"container"`
			Tail      int    `json:"tail"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDockerLogs(args.Container, args.Tail))
	case "docker_exec":
		var args struct {
			Container string `json:"container"`
			Command   string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDockerExec(args.Container, args.Command))
	case "docker_images":
		return mcpToolJSON(mcpDockerImages())
	case "docker_compose":
		var args struct {
			Action    string `json:"action"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDockerCompose(args.Action, args.Directory))

	// --- Test Runner ---
	case "run_tests":
		var args struct {
			Command   string `json:"command"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRunTests(args.Command, args.Directory))

	// --- HTTP Client ---
	case "http_request":
		var args struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Method == "" {
			args.Method = "GET"
		}
		return mcpToolJSON(mcpHTTPRequest(args.Method, args.URL, args.Headers, args.Body))

	// --- Log Tail ---
	case "tail_logs":
		var args struct {
			Path  string `json:"path"`
			Lines int    `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTailLogs(args.Path, args.Lines))

	// --- Clipboard ---
	case "clipboard_read":
		return mcpToolJSON(mcpClipboardRead())
	case "clipboard_write":
		var args struct {
			Content string `json:"content"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpClipboardWrite(args.Content))

	// --- Process Management ---
	case "process_list":
		var args struct {
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpProcessList(args.Filter))
	case "process_kill":
		var args struct {
			PID    int    `json:"pid"`
			Signal string `json:"signal"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpProcessKill(args.PID, args.Signal))
	case "port_check":
		var args struct {
			Port int `json:"port"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPortCheck(args.Port))

	// --- Code Quality ---
	case "lint":
		var args struct {
			Directory string `json:"directory"`
			Tool      string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpLint(args.Directory, args.Tool))
	case "format_code":
		var args struct {
			Directory string `json:"directory"`
			Tool      string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpFormat(args.Directory, args.Tool))
	case "type_check":
		var args struct {
			Directory string `json:"directory"`
			Tool      string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTypeCheck(args.Directory, args.Tool))

	// --- Package Dependencies ---
	case "deps_outdated":
		var args struct {
			Directory string `json:"directory"`
			Manager   string `json:"manager"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDepsOutdated(args.Directory, args.Manager))
	case "deps_audit":
		var args struct {
			Directory string `json:"directory"`
			Manager   string `json:"manager"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDepsAudit(args.Directory, args.Manager))
	case "deps_list":
		var args struct {
			Directory string `json:"directory"`
			Manager   string `json:"manager"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDepsList(args.Directory, args.Manager))
	case "mobile_project_status":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Directory) == "" {
			args.Directory = s.taskMgr.workDir
		}
		return mcpToolJSON(mobileProjectStatus(args.Directory))
	case "mobile_project_prepare":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Directory) == "" {
			args.Directory = s.taskMgr.workDir
		}
		manifest, err := readProjectPackageManifest(args.Directory)
		if err != nil {
			return mcpToolError(fmt.Sprintf("package.json missing or invalid: %v", err))
		}
		prep := detectProjectPreparation(args.Directory, manifest)
		if len(prep.MissingTools) > 0 {
			return mcpToolJSON(mobileProjectStatus(args.Directory))
		}
		if prep.NeedsDependencyInstall {
			if !prep.CanAutoInstallDependencies {
				return mcpToolJSON(mobileProjectStatus(args.Directory))
			}
			if err := installProjectDependencies(args.Directory, prep); err != nil {
				return mcpToolError(fmt.Sprintf("dependency install failed: %v", err))
			}
		}
		return mcpToolJSON(mobileProjectStatus(args.Directory))
	case "mobile_hermes_doctor":
		var args mobileHermesDoctorInput
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Directory) == "" {
			args.Directory = s.taskMgr.workDir
		}
		return mcpToolJSON(mobileHermesDoctor(args))
	case "mobile_project_build":
		var args struct {
			Directory string `json:"directory"`
			Framework string `json:"framework"`
			Platform  string `json:"platform"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Directory) == "" {
			args.Directory = s.taskMgr.workDir
		}
		if strings.TrimSpace(args.Platform) == "" {
			args.Platform = "ios"
		}
		result, err := s.buildNativeBundleForProject(args.Directory, args.Framework, args.Platform)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "mobile_hermes_reload":
		var args mobileHermesReloadArgs
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpMobileHermesReload(args))
	case "device_broadcast_command":
		var args deviceBroadcastCommandArgs
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(s.mcpDeviceBroadcastCommand(args))

	// --- GitHub ---
	case "github_prs":
		var args struct {
			Directory string `json:"directory"`
			State     string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubPRs(args.Directory, args.State))
	case "github_issues":
		var args struct {
			Directory string `json:"directory"`
			State     string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubIssues(args.Directory, args.State))
	case "github_ci_status":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubCIStatus(args.Directory))

	// --- Database ---
	case "db_query":
		var args struct {
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
			Query  string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDBQuery(args.Driver, args.DSN, args.Query))
	case "db_schema":
		var args struct {
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDBSchemas(args.Driver, args.DSN))

	// --- Network Diagnostics ---
	case "dns_lookup":
		var args struct {
			Host string `json:"host"`
			Type string `json:"type"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDNSLookup(args.Host, args.Type))
	case "ping":
		var args struct {
			Host  string `json:"host"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPing(args.Host, args.Count))
	case "ssl_check":
		var args struct {
			Host string `json:"host"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSSLCheck(args.Host))
	case "http_timing":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHTTPTiming(args.URL))

	// --- Data Tools ---
	case "base64":
		var args struct {
			Action string `json:"action"`
			Input  string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpBase64(args.Action, args.Input))
	case "hash":
		var args struct {
			Input     string `json:"input"`
			Algorithm string `json:"algorithm"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHash(args.Algorithm, args.Input))
	case "uuid":
		return mcpToolJSON(mcpUUID())
	case "jq":
		var args struct {
			Expression string `json:"expression"`
			Input      string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpJQ(args.Expression, args.Input))
	case "regex_test":
		var args struct {
			Pattern string `json:"pattern"`
			Input   string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRegexTest(args.Pattern, args.Input))

	// --- Archive ---
	case "archive_create":
		var args struct {
			Source string `json:"source"`
			Output string `json:"output"`
			Format string `json:"format"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpArchiveCreate(args.Format, args.Source, args.Output))
	case "archive_extract":
		var args struct {
			Path        string `json:"path"`
			Destination string `json:"destination"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpArchiveExtract(args.Path, args.Destination))

	// --- System Services ---
	case "service_status":
		var args struct {
			Name string `json:"name"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpServiceStatus(args.Name))
	case "service_action":
		var args struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpServiceAction(args.Name, args.Action))
	case "service_list":
		return mcpToolJSON(mcpServiceList())

	// --- Benchmark ---
	case "benchmark":
		var args struct {
			Command   string `json:"command"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpBenchmark(args.Command, args.Directory))

	// --- Diff ---
	case "diff":
		var args struct {
			PathA string `json:"path_a"`
			PathB string `json:"path_b"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDiff(args.PathA, args.PathB))

	// --- Environment ---
	case "env_list":
		var args struct {
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEnvList(args.Filter))
	case "env_read":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEnvRead(args.Path))

	// --- Crontab ---
	case "crontab":
		var args struct {
			Action string `json:"action"`
			Entry  string `json:"entry"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCrontab(args.Action, args.Entry))

	// --- Cloud CLI ---
	case "cloud_cli":
		var args struct {
			Provider string   `json:"provider"`
			Args     []string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCloudCmd(args.Provider, args.Args))

	// --- Home Assistant ---
	case "ha_states":
		var args struct {
			Filter string `json:"filter"`
			URL    string `json:"url"`
			Token  string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Token == "" {
			cfg, _ := LoadConfig()
			if cfg.HAToken != "" {
				args.Token = cfg.HAToken
			}
			if cfg.HAURL != "" && args.URL == "" {
				args.URL = cfg.HAURL
			}
		}
		return mcpToolJSON(mcpHAStates(args.URL, args.Token, args.Filter))
	case "ha_service":
		var args struct {
			Domain  string                 `json:"domain"`
			Service string                 `json:"service"`
			Data    map[string]interface{} `json:"data"`
			URL     string                 `json:"url"`
			Token   string                 `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Token == "" {
			cfg, _ := LoadConfig()
			if cfg.HAToken != "" {
				args.Token = cfg.HAToken
			}
			if cfg.HAURL != "" && args.URL == "" {
				args.URL = cfg.HAURL
			}
		}
		return mcpToolJSON(mcpHAService(args.URL, args.Token, args.Domain, args.Service, args.Data))
	case "ha_toggle":
		var args struct {
			EntityID string `json:"entity_id"`
			URL      string `json:"url"`
			Token    string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Token == "" {
			cfg, _ := LoadConfig()
			if cfg.HAToken != "" {
				args.Token = cfg.HAToken
			}
			if cfg.HAURL != "" && args.URL == "" {
				args.URL = cfg.HAURL
			}
		}
		return mcpToolJSON(mcpHAToggle(args.URL, args.Token, args.EntityID))
	case "mqtt_publish":
		var args struct {
			Topic   string `json:"topic"`
			Message string `json:"message"`
			Broker  string `json:"broker"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpMQTTPublish(args.Broker, args.Topic, args.Message))

	// --- Desktop Control ---
	case "desktop_notify":
		var args struct {
			Title   string `json:"title"`
			Message string `json:"message"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDesktopNotify(args.Title, args.Message))
	case "open_url":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpOpenURL(args.URL))
	case "volume":
		var args struct {
			Action string `json:"action"`
			Level  int    `json:"level"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpVolume(args.Action, args.Level))
	case "screen_lock":
		return mcpToolJSON(mcpScreenLock())
	case "say":
		var args struct {
			Text string `json:"text"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSay(args.Text))
	case "brightness":
		var args struct {
			Action string `json:"action"`
			Level  int    `json:"level"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpBrightness(args.Action, args.Level))

	// --- Music ---
	case "music":
		var args struct {
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpMusicControl(args.Action))

	// --- Weather ---
	case "weather":
		var args struct {
			Location string `json:"location"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWeather(args.Location))

	// --- System Extras ---
	case "battery":
		return mcpToolJSON(mcpBattery())
	case "disk_usage":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDiskUsage(args.Path))
	case "wifi_info":
		return mcpToolJSON(mcpWiFiInfo())
	case "public_ip":
		return mcpToolJSON(mcpPublicIP())
	case "uptime":
		return mcpToolJSON(mcpUptime())
	case "speed_test":
		return mcpToolJSON(mcpSpeedTest())
	case "site_check":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSiteCheck(args.URL))

	// --- Utilities ---
	case "password_gen":
		var args struct {
			Length    int  `json:"length"`
			NoSymbols bool `json:"no_symbols"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPasswordGen(args.Length, args.NoSymbols))
	case "qr_code":
		var args struct {
			Text string `json:"text"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpQRCode(args.Text))
	case "timer":
		var args struct {
			Seconds int    `json:"seconds"`
			Label   string `json:"label"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTimer(args.Seconds, args.Label))
	case "calculate":
		var args struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCalculate(args.Expression))
	case "world_clock":
		var args struct {
			Timezones []string `json:"timezones"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWorldClock(args.Timezones))
	case "countdown":
		var args struct {
			Date string `json:"date"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCountdown(args.Date))
	case "convert_units":
		var args struct {
			Value float64 `json:"value"`
			From  string  `json:"from"`
			To    string  `json:"to"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpConvertUnits(args.Value, args.From, args.To))

	// --- Philips Hue ---
	case "hue_lights":
		var args struct {
			BridgeIP string `json:"bridge_ip"`
			APIKey   string `json:"api_key"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHueLights(args.BridgeIP, args.APIKey))
	case "hue_control":
		var args struct {
			BridgeIP   string `json:"bridge_ip"`
			APIKey     string `json:"api_key"`
			LightID    string `json:"light_id"`
			Action     string `json:"action"`
			Brightness int    `json:"brightness"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHueControl(args.BridgeIP, args.APIKey, args.LightID, args.Action, args.Brightness))
	case "hue_scenes":
		var args struct {
			BridgeIP string `json:"bridge_ip"`
			APIKey   string `json:"api_key"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHueScenes(args.BridgeIP, args.APIKey))

	// --- Shelly ---
	case "shelly_status":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpShellyStatus(args.IP))
	case "shelly_control":
		var args struct {
			IP      string `json:"ip"`
			Action  string `json:"action"`
			Channel int    `json:"channel"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpShellyControl(args.IP, args.Channel, args.Action))
	case "shelly_power":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpShellyPower(args.IP))

	// --- Elgato Key Light ---
	case "elgato_status":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpElgatoStatus(args.IP))
	case "elgato_control":
		var args struct {
			IP          string `json:"ip"`
			On          bool   `json:"on"`
			Brightness  int    `json:"brightness"`
			Temperature int    `json:"temperature"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpElgatoControl(args.IP, args.On, args.Brightness, args.Temperature))

	// --- Nanoleaf ---
	case "nanoleaf":
		var args struct {
			IP         string `json:"ip"`
			Token      string `json:"token"`
			Action     string `json:"action"`
			Brightness int    `json:"brightness"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNanoleafControl(args.IP, args.Token, args.Action, args.Brightness))

	// --- Tasmota ---
	case "tasmota":
		var args struct {
			IP      string `json:"ip"`
			Command string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTasmotaControl(args.IP, args.Command))

	// --- Govee ---
	case "govee_devices":
		var args struct {
			APIKey string `json:"api_key"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGoveeDevices(args.APIKey))
	case "govee_control":
		var args struct {
			APIKey     string         `json:"api_key"`
			Device     string         `json:"device"`
			Model      string         `json:"model"`
			Action     string         `json:"action"`
			Brightness int            `json:"brightness"`
			Color      map[string]int `json:"color"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGoveeControl(args.APIKey, args.Device, args.Model, args.Action, args.Brightness, args.Color))

	// --- Wake on LAN ---
	case "wake_on_lan":
		var args struct {
			MAC string `json:"mac"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWakeOnLAN(args.MAC))

	// --- Apple Shortcuts ---
	case "run_shortcut":
		var args struct {
			Name  string `json:"name"`
			Input string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRunShortcut(args.Name, args.Input))
	case "list_shortcuts":
		return mcpToolJSON(mcpListShortcuts())

	// --- ADB ---
	case "adb_devices":
		return mcpToolJSON(mcpADBDevices())
	case "adb_command":
		var args struct {
			Command string `json:"command"`
			Device  string `json:"device"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpADBCommand(args.Device, args.Command))
	case "adb_screenshot":
		var args struct {
			Device string `json:"device"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpADBScreenshot(args.Device))

	// --- Sonos ---
	case "sonos_discover":
		return mcpToolJSON(mcpSonosDiscover())
	case "sonos_control":
		var args struct {
			IP     string `json:"ip"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSonosControl(args.IP, args.Action))

	// --- Productivity & Sharing ---
	case "standup":
		var args struct {
			Directory string `json:"directory"`
			Days      int    `json:"days"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpStandup(args.Directory, args.Days))
	case "create_gist":
		var args struct {
			Content     string `json:"content"`
			Filename    string `json:"filename"`
			Description string `json:"description"`
			Public      bool   `json:"public"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCreateGist(args.Filename, args.Content, args.Description, args.Public))
	case "changelog":
		var args struct {
			Directory string `json:"directory"`
			From      string `json:"from"`
			To        string `json:"to"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpChangelog(args.Directory, args.From, args.To))
	case "commit_message":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCommitMessage(args.Directory))
	case "gitignore":
		var args struct {
			Languages []string `json:"languages"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitignore(args.Languages))
	case "license":
		var args struct {
			Type   string `json:"type"`
			Author string `json:"author"`
			Year   int    `json:"year"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpLicense(args.Type, args.Author, args.Year))
	case "color":
		var args struct {
			Input string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpColor(args.Input))
	case "figlet":
		var args struct {
			Text string `json:"text"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpFiglet(args.Text))
	case "lorem_ipsum":
		var args struct {
			Paragraphs int `json:"paragraphs"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpLoremIpsum(args.Paragraphs))
	case "tldr":
		var args struct {
			Command string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTldr(args.Command))
	case "github_badge":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubBadge(args.Directory))
	case "invite":
		var args struct {
			Method    string `json:"method"`
			Recipient string `json:"recipient"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpInvite(args.Method, args.Recipient))
	case "git_stats":
		var args struct {
			Directory string `json:"directory"`
			Days      int    `json:"days"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitStats(args.Directory, args.Days))

	// --- Location & Lifestyle ---
	case "ev_charging":
		var args struct {
			Lat           float64 `json:"lat"`
			Lon           float64 `json:"lon"`
			Radius        int     `json:"radius"`
			ConnectorType string  `json:"connector_type"`
			Network       string  `json:"network"`
			Country       string  `json:"country"`
			MinPowerKW    int     `json:"min_power_kw"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEVCharging(args.Lat, args.Lon, args.Radius, args.ConnectorType, args.Network, args.Country, args.MinPowerKW))
	case "ev_networks":
		var args struct {
			Country string `json:"country"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEVNetworks(args.Country))
	case "ev_connector_types":
		return mcpToolJSON(mcpEVConnectorTypes())
	case "nobetci_eczane":
		var args struct {
			City     string `json:"city"`
			District string `json:"district"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNobetciEczane(args.City, args.District))
	case "eczane_nearby":
		var args struct {
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			Radius int     `json:"radius"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEczaneSearch(args.Lat, args.Lon, args.Radius))
	case "places_search":
		var args struct {
			Query string  `json:"query"`
			Lat   float64 `json:"lat"`
			Lon   float64 `json:"lon"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPlacesSearch(args.Query, args.Lat, args.Lon))
	case "restaurants":
		var args struct {
			Lat     float64 `json:"lat"`
			Lon     float64 `json:"lon"`
			Radius  int     `json:"radius"`
			Cuisine string  `json:"cuisine"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRestaurants(args.Lat, args.Lon, args.Radius, args.Cuisine))
	case "hotels":
		var args struct {
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			Radius int     `json:"radius"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHotels(args.Lat, args.Lon, args.Radius))
	case "geocode":
		var args struct {
			Address string `json:"address"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGeocode(args.Address))
	case "directions":
		var args struct {
			FromLat float64 `json:"from_lat"`
			FromLon float64 `json:"from_lon"`
			ToLat   float64 `json:"to_lat"`
			ToLon   float64 `json:"to_lon"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDirections(args.FromLat, args.FromLon, args.ToLat, args.ToLon))
	case "news":
		var args struct {
			Source string `json:"source"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNews(args.Source))
	case "stock_price":
		var args struct {
			Symbol string `json:"symbol"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpStockPrice(args.Symbol))
	case "translate":
		var args struct {
			Text   string `json:"text"`
			From   string `json:"from"`
			To     string `json:"to"`
			APIURL string `json:"api_url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTranslate(args.Text, args.From, args.To, args.APIURL))

	// --- Daily Dev Tools ---
	case "crypto_price":
		var args struct {
			Coins []string `json:"coins"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCryptoPrice(args.Coins))
	case "currency_exchange":
		var args struct {
			Amount float64 `json:"amount"`
			From   string  `json:"from"`
			To     string  `json:"to"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCurrencyExchange(args.Amount, args.From, args.To))
	case "npm_info":
		var args struct {
			Package string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNPMInfo(args.Package))
	case "github_trending":
		var args struct {
			Language string `json:"language"`
			Since    string `json:"since"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubTrending(args.Language, args.Since))
	case "jwt_decode":
		var args struct {
			Token string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpJWTDecode(args.Token))
	case "epoch":
		var args struct {
			Input string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEpoch(args.Input))
	case "cron_explain":
		var args struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCronExplain(args.Expression))
	case "http_status":
		var args struct {
			Code int `json:"code"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHTTPStatusLookup(args.Code))
	case "whois":
		var args struct {
			Domain string `json:"domain"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWhois(args.Domain))
	case "ip_geo":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpIPGeo(args.IP))
	case "subnet_calc":
		var args struct {
			CIDR string `json:"cidr"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSubnet(args.CIDR))
	case "fake_data":
		var args struct {
			Type  string `json:"type"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpFakeData(args.Type, args.Count))
	case "domain_check":
		var args struct {
			Domain string `json:"domain"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDomainCheck(args.Domain))

	// --- Kubernetes ---
	case "k8s_pods":
		var a struct {
			NS  string `json:"namespace"`
			Ctx string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sPods(a.NS, a.Ctx))
	case "k8s_logs":
		var a struct {
			Pod       string `json:"pod"`
			NS        string `json:"namespace"`
			Ctx       string `json:"context"`
			Container string `json:"container"`
			Tail      int    `json:"tail"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sLogs(a.Pod, a.NS, a.Ctx, a.Container, a.Tail))
	case "k8s_describe":
		var a struct {
			Resource string `json:"resource"`
			Name     string `json:"name"`
			NS       string `json:"namespace"`
			Ctx      string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sDescribe(a.Resource, a.Name, a.NS, a.Ctx))
	case "k8s_get":
		var a struct {
			Resource string `json:"resource"`
			NS       string `json:"namespace"`
			Ctx      string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sGet(a.Resource, a.NS, a.Ctx))
	case "k8s_apply":
		var a struct {
			File string `json:"file"`
			NS   string `json:"namespace"`
			Ctx  string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sApply(a.File, a.NS, a.Ctx))
	case "k8s_exec":
		var a struct {
			Pod       string `json:"pod"`
			Command   string `json:"command"`
			NS        string `json:"namespace"`
			Ctx       string `json:"context"`
			Container string `json:"container"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sExec(a.Pod, a.NS, a.Ctx, a.Command, a.Container))
	case "k8s_contexts":
		return mcpToolJSON(mcpK8sContexts())
	case "k8s_namespaces":
		var a struct {
			Ctx string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sNamespaces(a.Ctx))
	case "k8s_top":
		var a struct {
			Resource string `json:"resource"`
			NS       string `json:"namespace"`
			Ctx      string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		if a.Resource == "nodes" {
			return mcpToolJSON(mcpK8sTopNodes(a.Ctx))
		}
		return mcpToolJSON(mcpK8sTopPods(a.NS, a.Ctx))
	case "k8s_events":
		var a struct {
			NS  string `json:"namespace"`
			Ctx string `json:"context"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sEvents(a.NS, a.Ctx))

	// --- Terraform ---
	case "tf_plan":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformPlan(a.Dir))
	case "tf_apply":
		var a struct {
			Dir  string `json:"directory"`
			Auto bool   `json:"auto_approve"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformApply(a.Dir, a.Auto))
	case "tf_state":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformState(a.Dir))
	case "tf_output":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformOutput(a.Dir))
	case "tf_init":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformInit(a.Dir))
	case "tf_validate":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformValidate(a.Dir))

	// --- Serverless ---
	case "lambda_list":
		return mcpToolJSON(mcpLambdaList())
	case "lambda_invoke":
		var a struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLambdaInvoke(a.Name, a.Payload))
	case "lambda_logs":
		var a struct {
			Name    string `json:"name"`
			Minutes int    `json:"minutes"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLambdaLogs(a.Name, a.Minutes))

	// --- Vercel ---
	case "vercel_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVercelStatus(a.Dir))
	case "vercel_logs":
		var a struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVercelLogs(a.URL))
	case "vercel_env":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVercelEnv(a.Dir))

	// --- Netlify ---
	case "netlify_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNetlifyStatus(a.Dir))

	// --- Sentry ---
	case "sentry_issues":
		var a struct {
			Org     string `json:"org"`
			Project string `json:"project"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSentryIssues(a.Org, a.Project))

	// --- Linear ---
	case "linear_issues":
		var a struct {
			APIKey string `json:"api_key"`
			Team   string `json:"team"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLinearIssues(a.APIKey, a.Team))

	// --- Notion ---
	case "notion_search":
		var a struct {
			APIKey string `json:"api_key"`
			Query  string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNotionSearch(a.APIKey, a.Query))

	// --- 1Password ---
	case "op_get":
		var a struct {
			Item  string `json:"item"`
			Vault string `json:"vault"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpOnePasswordGet(a.Item, a.Vault))
	case "op_list":
		var a struct {
			Vault string `json:"vault"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpOnePasswordList(a.Vault))

	// --- Raycast ---
	case "raycast":
		var a struct {
			Command string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRaycastTrigger(a.Command))

	// --- App Store / iOS ---
	case "appstore_status":
		var a struct {
			BundleID string `json:"bundle_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAppStoreStatus(a.BundleID))
	case "testflight_builds":
		var a struct {
			BundleID string `json:"bundle_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAppStoreTestFlight(a.BundleID))
	case "xcode_build":
		var a struct {
			Dir    string `json:"directory"`
			Scheme string `json:"scheme"`
			Dest   string `json:"destination"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpXcodeBuild(a.Dir, a.Scheme, a.Dest))
	case "xcode_test":
		var a struct {
			Dir    string `json:"directory"`
			Scheme string `json:"scheme"`
			Dest   string `json:"destination"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpXcodeTest(a.Dir, a.Scheme, a.Dest))
	case "simulators":
		return mcpToolJSON(mcpSimulators())
	case "simulator_boot":
		var a struct {
			Device string `json:"device"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSimulatorBoot(a.Device))
	case "simulator_screenshot":
		var a struct {
			Device string `json:"device"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSimulatorScreenshot(a.Device))

	// --- Google Play / Android ---
	case "playstore_status":
		var a struct {
			Package string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPlayStoreStatus(a.Package))
	case "playstore_track":
		var a struct {
			Package string `json:"package"`
			Track   string `json:"track"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPlayStoreTrack(a.Package, a.Track))
	case "gradle_build":
		var a struct {
			Dir  string `json:"directory"`
			Task string `json:"task"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGradleBuild(a.Dir, a.Task))
	case "gradle_test":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGradleTest(a.Dir))
	case "android_lint":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAndroidLint(a.Dir))
	case "emulators":
		return mcpToolJSON(mcpEmulators())

	// --- Firebase ---
	case "firebase_projects":
		return mcpToolJSON(mcpFirebaseProjects())
	case "firebase_deploy":
		var a struct {
			Dir  string `json:"directory"`
			Only string `json:"only"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFirebaseDeploy(a.Dir, a.Only))
	case "firebase_crashlytics":
		var a struct {
			ProjectID string `json:"project_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFirebaseCrashlytics(a.ProjectID))

	// --- React Native / Expo ---
	case "expo_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpExpoStatus(a.Dir))
	case "eas_build":
		var a struct {
			Dir      string `json:"directory"`
			Platform string `json:"platform"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpExpoBuild(a.Dir, a.Platform))
	case "eas_submit":
		var a struct {
			Dir      string `json:"directory"`
			Platform string `json:"platform"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpEASSubmit(a.Dir, a.Platform))

	// --- Flutter ---
	case "flutter_doctor":
		return mcpToolJSON(mcpFlutterDoctor())
	case "flutter_build":
		var a struct {
			Dir      string `json:"directory"`
			Platform string `json:"platform"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlutterBuild(a.Dir, a.Platform))
	case "flutter_test":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlutterTest(a.Dir))

	// --- CocoaPods ---
	case "pod_install":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPodInstall(a.Dir))
	case "pod_outdated":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPodOutdated(a.Dir))

	// --- App Review ---
	case "app_review_check":
		var a struct {
			Platform string `json:"platform"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAppReviewCheck(a.Platform))

	// --- Package Registries ---
	case "dockerhub_search":
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerHubSearch(a.Query, a.Limit))
	case "dockerhub_tags":
		var a struct {
			Image string `json:"image"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerHubTags(a.Image, a.Limit))
	case "pypi_info":
		var a struct {
			Pkg string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPyPIInfo(a.Pkg))
	case "pypi_versions":
		var a struct {
			Pkg string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPyPIVersions(a.Pkg))
	case "npm_search":
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNPMSearch(a.Query, a.Limit))
	case "npm_versions":
		var a struct {
			Pkg string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNPMVersions(a.Pkg))
	case "crates_info":
		var a struct {
			Crate string `json:"crate"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCratesInfo(a.Crate))
	case "crates_search":
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCratesSearch(a.Query, a.Limit))
	case "go_module_info":
		var a struct {
			Module string `json:"module"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModuleInfo(a.Module))
	case "go_module_versions":
		var a struct {
			Module string `json:"module"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModuleVersions(a.Module))
	case "pubdev_info":
		var a struct {
			Pkg string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPubDevInfo(a.Pkg))
	case "pubdev_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPubDevSearch(a.Query))
	case "brew_info":
		var a struct {
			Formula string `json:"formula"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBrewInfo(a.Formula))
	case "brew_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBrewSearch(a.Query))
	case "gem_info":
		var a struct {
			Gem string `json:"gem"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGemInfo(a.Gem))
	case "gem_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGemSearch(a.Query))
	case "maven_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMavenSearch(a.Query))
	case "nuget_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNuGetSearch(a.Query))
	case "apt_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAptSearch(a.Query))
	case "apt_show":
		var a struct {
			Pkg string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAptShow(a.Pkg))
	case "pip_show":
		var a struct {
			Pkg string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPipShow(a.Pkg))
	case "pip_list":
		return mcpToolJSON(mcpPipList())
	case "cargo_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoSearch(a.Query))
	case "pkg_install":
		var a struct {
			Manager string `json:"manager"`
			Pkg     string `json:"package"`
			Global  bool   `json:"global"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPkgInstall(a.Manager, a.Pkg, a.Global))

	// --- Supabase ---
	case "supabase_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSupabaseStatus(a.Dir))
	case "supabase_db":
		var a struct {
			Dir   string `json:"directory"`
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSupabaseDB(a.Dir, a.Query))
	case "supabase_migrations":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSupabaseMigrations(a.Dir))
	case "supabase_functions":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSupabaseFunctions(a.Dir))
	case "supabase_deploy":
		var a struct {
			Dir      string `json:"directory"`
			Function string `json:"function"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSupabaseDeploy(a.Dir, a.Function))
	// --- Convex ---
	case "convex_deploy":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexDeploy(a.Dir))
	case "convex_logs":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexLogs(a.Dir))
	case "convex_run":
		var a struct {
			Dir      string `json:"directory"`
			Function string `json:"function"`
			Args     string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexRun(a.Dir, a.Function, a.Args))
	case "convex_local_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexLocalStatus(a.Dir))
	case "convex_tables":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexTables(a.Dir))
	case "convex_browse":
		var a struct {
			Dir    string `json:"directory"`
			Table  string `json:"table"`
			Cursor string `json:"cursor"`
			Limit  int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexBrowse(a.Dir, a.Table, a.Cursor, a.Limit))
	case "convex_query":
		var a struct {
			Dir      string `json:"directory"`
			Function string `json:"function"`
			Args     string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexAdminQuery(a.Dir, a.Function, a.Args))
	case "convex_mutate":
		var a struct {
			Dir      string `json:"directory"`
			Function string `json:"function"`
			Args     string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexAdminMutate(a.Dir, a.Function, a.Args))
	case "convex_action":
		var a struct {
			Dir      string `json:"directory"`
			Function string `json:"function"`
			Args     string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexAdminAction(a.Dir, a.Function, a.Args))
	case "convex_schema":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexSchema(a.Dir))
	case "convex_export":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexExport(a.Dir))
	case "convex_install_helper":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpConvexInstallHelper(a.Dir))
	// --- Universal backend ---
	case "backend_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBackendStatus(a.Dir))
	case "data_tables":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDataTables(a.Dir))
	case "data_browse":
		var a struct {
			Dir    string `json:"directory"`
			Table  string `json:"table"`
			Cursor string `json:"cursor"`
			Limit  int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDataBrowse(a.Dir, a.Table, a.Cursor, a.Limit))
	case "data_query":
		var a struct {
			Dir   string `json:"directory"`
			Query string `json:"query"`
			Args  string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDataQuery(a.Dir, a.Query, a.Args))
	case "data_insert":
		var a struct {
			Dir   string `json:"directory"`
			Table string `json:"table"`
			Doc   string `json:"doc"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDataInsert(a.Dir, a.Table, a.Doc))
	case "data_update":
		var a struct {
			Dir    string `json:"directory"`
			Table  string `json:"table"`
			ID     string `json:"id"`
			Fields string `json:"fields"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDataUpdate(a.Dir, a.Table, a.ID, a.Fields))
	case "data_delete":
		var a struct {
			Dir   string `json:"directory"`
			Table string `json:"table"`
			ID    string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDataDelete(a.Dir, a.Table, a.ID))
	// --- Cloud emulators ---
	case "cloud_emu_start":
		var a struct {
			Dir      string `json:"directory"`
			Provider string `json:"provider"`
			Services string `json:"services"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloudEmuStart(a.Dir, a.Provider, splitCSV(a.Services)))
	case "cloud_emu_stop":
		var a struct {
			Dir      string `json:"directory"`
			Provider string `json:"provider"`
			Services string `json:"services"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloudEmuStop(a.Dir, a.Provider, splitCSV(a.Services)))
	case "cloud_emu_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloudEmuStatus(a.Dir))
	case "cloud_emu_config":
		var a struct {
			Provider string `json:"provider"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloudEmuConfig(a.Provider))
	// --- Switch engine ---
	case "switch_targets":
		return mcpToolJSON(mcpSwitchTargets())
	case "switch_plan":
		var a struct {
			Dir    string `json:"directory"`
			Target string `json:"target"`
			DryRun bool   `json:"dryRun"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSwitchPlan(a.Dir, a.Target, a.DryRun))
	case "switch_run":
		var a struct {
			Dir string `json:"directory"`
			ID  string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSwitchRun(a.Dir, a.ID))
	case "switch_rollback":
		var a struct {
			Dir string `json:"directory"`
			ID  string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSwitchRollback(a.Dir, a.ID))
	case "switch_history":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSwitchHistory(a.Dir))
	case "switch_cleanup":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSwitchCleanup(a.Dir))
	case "project_runtime":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpProjectRuntime(a.Dir))
	case "project_runtime_apply":
		var a ProjectRuntimeApplyRequest
		var wrapper struct {
			Directory string `json:"directory"`
			ProjectRuntimeApplyRequest
		}
		json.Unmarshal(call.Arguments, &wrapper)
		a = wrapper.ProjectRuntimeApplyRequest
		return mcpToolJSON(mcpProjectRuntimeApply(wrapper.Directory, a))
	// --- Accounts manager ---
	case "account_list":
		return mcpToolJSON(mcpAccountList())
	case "account_connect":
		var a struct {
			Provider string `json:"provider"`
			Label    string `json:"label"`
			Fields   string `json:"fields"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAccountConnect(a.Provider, a.Label, a.Fields))
	case "account_disconnect":
		var a struct {
			Provider string `json:"provider"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAccountDisconnect(a.Provider))
	case "account_status":
		var a struct {
			Provider string `json:"provider"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAccountStatus(a.Provider))
	// --- Yaver sign-in (headless OAuth) ---
	case "yaver_auth_status":
		return mcpToolJSON(authStatusSnapshot())
	case "yaver_auth_start":
		var a struct {
			ConvexURL string `json:"convex_url"`
		}
		json.Unmarshal(call.Arguments, &a)
		result, err := authStartDeviceCode(context.Background(), a.ConvexURL)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_poll":
		var a struct {
			DeviceCode string `json:"device_code"`
			ConvexURL  string `json:"convex_url"`
		}
		json.Unmarshal(call.Arguments, &a)
		result, err := authPollDeviceCode(context.Background(), a.ConvexURL, a.DeviceCode)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_wait":
		var a struct {
			DeviceCode          string `json:"device_code"`
			ConvexURL           string `json:"convex_url"`
			TimeoutSeconds      int    `json:"timeout_seconds"`
			PollIntervalSeconds int    `json:"poll_interval_seconds"`
		}
		json.Unmarshal(call.Arguments, &a)
		// Clamp timeout to protect callers from their own footguns — some
		// MCP clients abort at 2min, others wait much longer. 300s is a
		// hard ceiling; callers wanting longer should loop yaver_auth_poll.
		if a.TimeoutSeconds > 300 {
			a.TimeoutSeconds = 300
		}
		result, err := authWaitDeviceCode(context.Background(), a.ConvexURL, a.DeviceCode, a.TimeoutSeconds, a.PollIntervalSeconds)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_logout":
		result, err := authLogout()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_lazy_setup":
		var a struct {
			WaitSeconds int `json:"wait_seconds"`
		}
		json.Unmarshal(call.Arguments, &a)
		if a.WaitSeconds > 180 {
			a.WaitSeconds = 180
		}
		result, err := yaverLazySetup(context.Background(), a.WaitSeconds)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_list_identities":
		result, err := authListIdentities(context.Background())
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_link_start":
		var a struct {
			Provider string `json:"provider"`
		}
		json.Unmarshal(call.Arguments, &a)
		result, err := authLinkStart(context.Background(), a.Provider)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_link_wait":
		var a struct {
			Provider        string `json:"provider"`
			TimeoutSec      int    `json:"timeout_seconds"`
			PollIntervalSec int    `json:"poll_interval_seconds"`
		}
		json.Unmarshal(call.Arguments, &a)
		if a.TimeoutSec > 300 {
			a.TimeoutSec = 300
		}
		result, err := authLinkWait(context.Background(), a.Provider, a.TimeoutSec, a.PollIntervalSec)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "recovery_transport_status":
		return mcpToolJSON(mcpRecoveryTransportStatus())
	case "recovery_target_status":
		var a struct {
			TargetURL             string `json:"target_url"`
			RelayPassword         string `json:"relay_password"`
			AllowPublicDirectHTTP bool   `json:"allow_public_direct_http"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRecoveryTargetStatus(a.TargetURL, a.RelayPassword, a.AllowPublicDirectHTTP))
	case "recovery_target_start":
		var a struct {
			TargetURL             string `json:"target_url"`
			Mode                  string `json:"mode"`
			BootstrapSecret       string `json:"bootstrap_secret"`
			BearerToken           string `json:"bearer_token"`
			RelayPassword         string `json:"relay_password"`
			AllowPublicDirectHTTP bool   `json:"allow_public_direct_http"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRecoveryTargetStart(a.TargetURL, a.Mode, a.BootstrapSecret, a.BearerToken, a.RelayPassword, a.AllowPublicDirectHTTP))
	case "recovery_target_wait":
		var a struct {
			TargetURL             string `json:"target_url"`
			RecoveryID            string `json:"recovery_id"`
			WaitToken             string `json:"wait_token"`
			RelayPassword         string `json:"relay_password"`
			AllowPublicDirectHTTP bool   `json:"allow_public_direct_http"`
			TimeoutSeconds        int    `json:"timeout_seconds"`
			PollIntervalSeconds   int    `json:"poll_interval_seconds"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRecoveryTargetWait(a.TargetURL, a.RecoveryID, a.WaitToken, a.RelayPassword, a.TimeoutSeconds, a.PollIntervalSeconds, a.AllowPublicDirectHTTP))
	case "device_reauth_status":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDeviceReauthStatus(a.DeviceID))
	case "device_reauth_start":
		var a struct {
			DeviceID        string `json:"device_id"`
			Mode            string `json:"mode"`
			BootstrapSecret string `json:"bootstrap_secret"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDeviceReauthStart(a.DeviceID, a.Mode, a.BootstrapSecret))
	case "device_reauth_wait":
		var a struct {
			RecoveryID          string `json:"recovery_id"`
			WaitToken           string `json:"wait_token"`
			DeviceID            string `json:"device_id"`
			TimeoutSeconds      int    `json:"timeout_seconds"`
			PollIntervalSeconds int    `json:"poll_interval_seconds"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDeviceReauthWait(a.DeviceID, a.RecoveryID, a.WaitToken, a.TimeoutSeconds, a.PollIntervalSeconds))
	case "runner_auth_status":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerAuthStatus(a.DeviceID))
	case "runner_auth_set":
		var a struct {
			DeviceID             string `json:"device_id"`
			Runner               string `json:"runner"`
			OpenAIAPIKey         string `json:"openai_api_key"`
			AnthropicAPIKey      string `json:"anthropic_api_key"`
			AnthropicAuthToken   string `json:"anthropic_auth_token"`
			ClaudeCodeOAuthToken string `json:"claude_code_oauth_token"`
			GLMAPIKey            string `json:"glm_api_key"`
			ZAIAPIKey            string `json:"zai_api_key"`
			Notes                string `json:"notes"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerAuthSet(
			a.DeviceID,
			a.Runner,
			a.OpenAIAPIKey,
			a.AnthropicAPIKey,
			a.AnthropicAuthToken,
			a.ClaudeCodeOAuthToken,
			a.GLMAPIKey,
			a.ZAIAPIKey,
			a.Notes,
		))
	case "opencode_config_get":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.DeviceID) != "" {
			status, body, err := proxyToDevice(context.Background(), "opencode_config_get", strings.TrimSpace(a.DeviceID), http.MethodGet, "/runner/opencode/config", nil)
			if err != nil {
				return mcpToolError(fmt.Sprintf("opencode_config_get: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("opencode_config_get: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		cfg, err := loadOpenCodeConfigSummary()
		if err != nil {
			return mcpToolError(fmt.Sprintf("opencode_config_get: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "config": cfg})

	case "code_config_get":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.DeviceID) != "" {
			status, body, err := proxyToDevice(context.Background(), "code_config_get", strings.TrimSpace(a.DeviceID), http.MethodGet, "/code/config", nil)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_config_get: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_config_get: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		summary, err := buildCodeConfigSummary()
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_config_get: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "code": summary})

	case "code_config_set":
		var a struct {
			DeviceID               string `json:"device_id"`
			Runner                 string `json:"runner"`
			Model                  string `json:"model"`
			Provider               string `json:"provider"`
			BaseURL                string `json:"base_url"`
			OrchestrationMode      string `json:"orchestration_mode"`
			CompressionMode        string `json:"compression_mode"`
			HandoffCompressionMode string `json:"handoff_compression_mode"`
			MemoryCompressionMode  string `json:"memory_compression_mode"`
			WorkMode               string `json:"work_mode"`
			AttachedDeviceID       string `json:"attached_device_id"`
			AttachedDeviceName     string `json:"attached_device_name"`
			RepoPath               string `json:"repo_path"`
			RepoRemote             *bool  `json:"repo_remote"`
			BYOKProvider           string `json:"byok_provider"`
			BYOKAPIKey             string `json:"byok_api_key"`
			SmallModel             string `json:"small_model"`
			PlanModel              string `json:"plan_model"`
			BuildModel             string `json:"build_model"`
		}
		json.Unmarshal(call.Arguments, &a)
		payload := map[string]interface{}{}
		if v := strings.TrimSpace(a.Runner); v != "" {
			payload["runner"] = v
		}
		if v := strings.TrimSpace(a.Model); v != "" {
			payload["model"] = v
		}
		if v := strings.TrimSpace(a.Provider); v != "" {
			payload["provider"] = v
		}
		if v := strings.TrimSpace(a.BaseURL); v != "" {
			payload["baseUrl"] = v
		}
		if v := strings.TrimSpace(a.WorkMode); v != "" {
			payload["workMode"] = v
		}
		if v := strings.TrimSpace(a.AttachedDeviceID); v != "" {
			payload["attachedDeviceId"] = v
		}
		if v := strings.TrimSpace(a.AttachedDeviceName); v != "" {
			payload["attachedDeviceName"] = v
		}
		if v := strings.TrimSpace(a.RepoPath); v != "" {
			payload["repoPath"] = v
		}
		if a.RepoRemote != nil {
			payload["repoRemote"] = *a.RepoRemote
		}
		if v := strings.TrimSpace(a.BYOKProvider); v != "" {
			payload["byokProvider"] = v
		}
		if v := strings.TrimSpace(a.BYOKAPIKey); v != "" {
			payload["byokApiKey"] = v
		}
		if v := strings.TrimSpace(a.SmallModel); v != "" {
			payload["smallModel"] = v
		}
		if v := strings.TrimSpace(a.PlanModel); v != "" {
			payload["planModel"] = v
		}
		if v := strings.TrimSpace(a.BuildModel); v != "" {
			payload["buildModel"] = v
		}
		if strings.TrimSpace(a.DeviceID) != "" {
			raw, _ := json.Marshal(payload)
			status, body, err := proxyToDevice(context.Background(), "code_config_set", strings.TrimSpace(a.DeviceID), http.MethodPost, "/code/config", raw)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_config_set: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_config_set: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		summary, err := applyCodeConfigPatch(codeConfigPatchRequest{
			Runner:                 stringPtr(a.Runner),
			Model:                  stringPtr(a.Model),
			Provider:               stringPtr(a.Provider),
			BaseURL:                stringPtr(a.BaseURL),
			OrchestrationMode:      stringPtr(a.OrchestrationMode),
			CompressionMode:        stringPtr(a.CompressionMode),
			HandoffCompressionMode: stringPtr(a.HandoffCompressionMode),
			MemoryCompressionMode:  stringPtr(a.MemoryCompressionMode),
			WorkMode:               stringPtr(a.WorkMode),
			AttachedDeviceID:       stringPtr(a.AttachedDeviceID),
			AttachedDeviceName:     stringPtr(a.AttachedDeviceName),
			RepoPath:               stringPtr(a.RepoPath),
			RepoRemote:             a.RepoRemote,
			BYOKProvider:           stringPtr(a.BYOKProvider),
			BYOKAPIKey:             stringPtr(a.BYOKAPIKey),
			SmallModel:             stringPtr(a.SmallModel),
			PlanModel:              stringPtr(a.PlanModel),
			BuildModel:             stringPtr(a.BuildModel),
		})
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_config_set: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "code": summary})

	case "code_status":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.DeviceID) != "" {
			status, body, err := proxyToDevice(context.Background(), "code_status", strings.TrimSpace(a.DeviceID), http.MethodGet, "/code/status", nil)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_status: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_status: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		out, err := buildCodeStatusPayload()
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_status: %v", err))
		}
		return mcpToolJSON(out)

	case "code_attach":
		var a struct {
			DeviceID string `json:"device_id"`
			Target   string `json:"target"`
			Username string `json:"username"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.Target) == "" {
			return mcpToolError("target is required")
		}
		payload := map[string]interface{}{"target": strings.TrimSpace(a.Target)}
		if v := strings.TrimSpace(a.Username); v != "" {
			payload["username"] = v
		}
		if strings.TrimSpace(a.DeviceID) != "" {
			raw, _ := json.Marshal(payload)
			status, body, err := proxyToDevice(context.Background(), "code_attach", strings.TrimSpace(a.DeviceID), http.MethodPost, "/code/attach", raw)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_attach: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_attach: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		out, err := applyCodeAttach(a.Target, a.Username)
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_attach: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "result": out})

	case "code_detach":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.DeviceID) != "" {
			status, body, err := proxyToDevice(context.Background(), "code_detach", strings.TrimSpace(a.DeviceID), http.MethodPost, "/code/detach", []byte(`{}`))
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_detach: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_detach: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		out, err := applyCodeDetach()
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_detach: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "code": out})

	case "code_repos":
		var a struct {
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.DeviceID) != "" {
			status, body, err := proxyToDevice(context.Background(), "code_repos", strings.TrimSpace(a.DeviceID), http.MethodGet, "/code/repos", nil)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_repos: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_repos: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		repos, err := listCodeReposStructured()
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_repos: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "repos": repos})

	case "code_repo_set":
		var a struct {
			DeviceID string `json:"device_id"`
			Query    string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.Query) == "" {
			return mcpToolError("query is required")
		}
		payload := map[string]interface{}{"query": strings.TrimSpace(a.Query)}
		if strings.TrimSpace(a.DeviceID) != "" {
			raw, _ := json.Marshal(payload)
			status, body, err := proxyToDevice(context.Background(), "code_repo_set", strings.TrimSpace(a.DeviceID), http.MethodPost, "/code/repo", raw)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_repo_set: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_repo_set: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		out, err := setCodeRepoStructured(a.Query)
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_repo_set: %v", err))
		}
		return mcpToolJSON(out)

	case "code_dev":
		var a struct {
			DeviceID string `json:"device_id"`
			Action   string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.Action) == "" {
			return mcpToolError("action is required")
		}
		payload := map[string]interface{}{"action": strings.TrimSpace(a.Action)}
		if strings.TrimSpace(a.DeviceID) != "" {
			raw, _ := json.Marshal(payload)
			status, body, err := proxyToDevice(context.Background(), "code_dev", strings.TrimSpace(a.DeviceID), http.MethodPost, "/code/dev", raw)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_dev: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_dev: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		out, err := runCodeDevActionStructured(a.Action)
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_dev: %v", err))
		}
		return mcpToolJSON(out)

	case "code_deploy":
		var a struct {
			DeviceID   string   `json:"device_id"`
			App        string   `json:"app"`
			Surface    string   `json:"surface"`
			Targets    []string `json:"targets"`
			RepoQuery  string   `json:"repo_query"`
			RepoPath   string   `json:"repo_path"`
			Machine    string   `json:"machine"`
			Distribute bool     `json:"distribute"`
			CIProvider string   `json:"ci_provider"`
			CIRepo     string   `json:"ci_repo"`
			Workflow   string   `json:"workflow"`
			Branch     string   `json:"branch"`
			Tag        string   `json:"tag"`
			File       string   `json:"file"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.Surface) == "" && len(a.Targets) == 0 {
			return mcpToolError("surface or targets is required")
		}
		payload := map[string]interface{}{}
		if v := strings.TrimSpace(a.App); v != "" {
			payload["app"] = v
		}
		if v := strings.TrimSpace(a.Surface); v != "" {
			payload["surface"] = v
		}
		if len(a.Targets) > 0 {
			payload["targets"] = a.Targets
		}
		if v := strings.TrimSpace(a.RepoQuery); v != "" {
			payload["repoQuery"] = v
		}
		if v := strings.TrimSpace(a.RepoPath); v != "" {
			payload["repoPath"] = v
		}
		if v := strings.TrimSpace(a.Machine); v != "" {
			payload["machine"] = v
		}
		if a.Distribute {
			payload["distribute"] = true
		}
		if v := strings.TrimSpace(a.CIProvider); v != "" {
			payload["ciProvider"] = v
		}
		if v := strings.TrimSpace(a.CIRepo); v != "" {
			payload["ciRepo"] = v
		}
		if v := strings.TrimSpace(a.Workflow); v != "" {
			payload["workflow"] = v
		}
		if v := strings.TrimSpace(a.Branch); v != "" {
			payload["branch"] = v
		}
		if v := strings.TrimSpace(a.Tag); v != "" {
			payload["tag"] = v
		}
		if v := strings.TrimSpace(a.File); v != "" {
			payload["file"] = v
		}
		if strings.TrimSpace(a.DeviceID) != "" {
			raw, _ := json.Marshal(payload)
			status, body, err := proxyToDevice(context.Background(), "code_deploy", strings.TrimSpace(a.DeviceID), http.MethodPost, "/code/deploy", raw)
			if err != nil {
				return mcpToolError(fmt.Sprintf("code_deploy: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("code_deploy: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		out, err := runCodeDeployRequestStructured(CodeDeployRequest{
			App:        a.App,
			Surface:    a.Surface,
			Targets:    a.Targets,
			RepoQuery:  a.RepoQuery,
			RepoPath:   a.RepoPath,
			Machine:    a.Machine,
			Distribute: a.Distribute,
			CIProvider: a.CIProvider,
			CIRepo:     a.CIRepo,
			Workflow:   a.Workflow,
			Branch:     a.Branch,
			Tag:        a.Tag,
			File:       a.File,
		})
		if err != nil {
			return mcpToolError(fmt.Sprintf("code_deploy: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "result": out})

	case "opencode_config_set":
		var a struct {
			DeviceID     string                  `json:"device_id"`
			DefaultAgent *string                 `json:"default_agent"`
			Model        *string                 `json:"model"`
			SmallModel   *string                 `json:"small_model"`
			BuildModel   *string                 `json:"build_model"`
			PlanModel    *string                 `json:"plan_model"`
			Providers    []openCodeProviderPatch `json:"providers"`
		}
		json.Unmarshal(call.Arguments, &a)
		patch := openCodeConfigPatch{
			DefaultAgent: a.DefaultAgent,
			Model:        a.Model,
			SmallModel:   a.SmallModel,
			BuildModel:   a.BuildModel,
			PlanModel:    a.PlanModel,
			Providers:    a.Providers,
		}
		if strings.TrimSpace(a.DeviceID) != "" {
			payload, _ := json.Marshal(patch)
			status, body, err := proxyToDevice(context.Background(), "opencode_config_set", strings.TrimSpace(a.DeviceID), http.MethodPost, "/runner/opencode/config", payload)
			if err != nil {
				return mcpToolError(fmt.Sprintf("opencode_config_set: %v", err))
			}
			if status >= 300 {
				return mcpToolError(fmt.Sprintf("opencode_config_set: remote returned %d: %s", status, string(body)))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		cfg, err := applyOpenCodeConfigPatch(patch)
		if err != nil {
			return mcpToolError(fmt.Sprintf("opencode_config_set: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "config": cfg})

	case "runner_auth_setup":
		var a struct {
			DeviceID             string `json:"device_id"`
			Runner               string `json:"runner"`
			OpenAIAPIKey         string `json:"openai_api_key"`
			AnthropicAPIKey      string `json:"anthropic_api_key"`
			AnthropicAuthToken   string `json:"anthropic_auth_token"`
			ClaudeCodeOAuthToken string `json:"claude_code_oauth_token"`
			GLMAPIKey            string `json:"glm_api_key"`
			ZAIAPIKey            string `json:"zai_api_key"`
			Notes                string `json:"notes"`
			InstallIfMissing     *bool  `json:"install_if_missing"`
			CodexLogin           *bool  `json:"codex_login"`
			SetupMCP             *bool  `json:"setup_mcp"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerAuthSetup(a.DeviceID, runnerAuthSetupRequest{
			Runner:               a.Runner,
			OpenAIAPIKey:         a.OpenAIAPIKey,
			AnthropicAPIKey:      a.AnthropicAPIKey,
			AnthropicAuthToken:   a.AnthropicAuthToken,
			ClaudeCodeOAuthToken: a.ClaudeCodeOAuthToken,
			GLMAPIKey:            a.GLMAPIKey,
			ZAIAPIKey:            a.ZAIAPIKey,
			Notes:                a.Notes,
			InstallIfMissing:     a.InstallIfMissing,
			CodexLogin:           a.CodexLogin,
			SetupMCP:             a.SetupMCP,
		}))
	case "runner_auth_browser_start":
		var a struct {
			DeviceID string `json:"device_id"`
			Runner   string `json:"runner"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerBrowserAuthStart(a.DeviceID, a.Runner))
	case "runner_auth_browser_status":
		var a struct {
			DeviceID  string `json:"device_id"`
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerBrowserAuthStatus(a.DeviceID, a.SessionID))
	case "runner_auth_browser_submit_code":
		var a struct {
			DeviceID  string `json:"device_id"`
			SessionID string `json:"session_id"`
			Code      string `json:"code"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerBrowserAuthSubmitCode(a.DeviceID, a.SessionID, a.Code))
	case "runner_auth_browser_cancel":
		var a struct {
			DeviceID  string `json:"device_id"`
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerBrowserAuthCancel(a.DeviceID, a.SessionID))
	case "runner_auth_credentials_import":
		var a struct {
			DeviceID        string `json:"device_id"`
			Runner          string `json:"runner"`
			CredentialsJSON string `json:"credentials_json"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRunnerAuthCredentialsImport(a.DeviceID, a.Runner, a.CredentialsJSON))
	case "machine_onboarding_status":
		var a struct {
			DeviceID  string   `json:"device_id"`
			DeviceIDs []string `json:"device_ids"`
		}
		json.Unmarshal(call.Arguments, &a)
		if len(a.DeviceIDs) > 0 {
			return mcpToolJSON(mcpMachineOnboardingStatusMulti(a.DeviceIDs))
		}
		return mcpToolJSON(mcpMachineOnboardingStatus(a.DeviceID))
	case "machine_onboarding_apply":
		var a struct {
			DeviceID     string   `json:"device_id"`
			DeviceIDs    []string `json:"device_ids"`
			OpenAIAPIKey string   `json:"openai_api_key"`
			GitHubToken  string   `json:"github_token"`
			GitLabToken  string   `json:"gitlab_token"`
			GitLabHost   string   `json:"gitlab_host"`
			ApplyClone   *bool    `json:"apply_clone"`
			ApplyCIToken *bool    `json:"apply_ci_token"`
			Notes        string   `json:"notes"`
		}
		json.Unmarshal(call.Arguments, &a)
		req := machineOnboardingApplyRequest{
			OpenAIAPIKey: a.OpenAIAPIKey,
			GitHubToken:  a.GitHubToken,
			GitLabToken:  a.GitLabToken,
			GitLabHost:   a.GitLabHost,
			ApplyClone:   a.ApplyClone,
			ApplyCIToken: a.ApplyCIToken,
			Notes:        a.Notes,
		}
		if len(a.DeviceIDs) > 0 {
			return mcpToolJSON(mcpMachineOnboardingApplyMulti(a.DeviceIDs, req))
		}
		return mcpToolJSON(mcpMachineOnboardingApply(a.DeviceID, machineOnboardingApplyRequest{
			OpenAIAPIKey: req.OpenAIAPIKey,
			GitHubToken:  req.GitHubToken,
			GitLabToken:  req.GitLabToken,
			GitLabHost:   req.GitLabHost,
			ApplyClone:   req.ApplyClone,
			ApplyCIToken: req.ApplyCIToken,
			Notes:        req.Notes,
		}))
	case "machine_onboarding_remove":
		var a struct {
			DeviceID      string   `json:"device_id"`
			DeviceIDs     []string `json:"device_ids"`
			Providers     []string `json:"providers"`
			GitLabHost    string   `json:"gitlab_host"`
			RemoveClone   *bool    `json:"remove_clone"`
			RemoveCIToken *bool    `json:"remove_ci_token"`
		}
		json.Unmarshal(call.Arguments, &a)
		req := machineOnboardingRemoveRequest{
			Providers:     a.Providers,
			GitLabHost:    a.GitLabHost,
			RemoveClone:   a.RemoveClone,
			RemoveCIToken: a.RemoveCIToken,
		}
		if len(a.DeviceIDs) > 0 {
			return mcpToolJSON(mcpMachineOnboardingRemoveMulti(a.DeviceIDs, req))
		}
		return mcpToolJSON(mcpMachineOnboardingRemove(a.DeviceID, req))
	case "git_push_creds":
		var a gitPushCredsMCPArgs
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitPushCreds(a))
	case "git_oauth_start":
		var a struct {
			Provider string `json:"provider"`
			Host     string `json:"host"`
			DeviceID string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.DeviceID) != "" {
			payload, _ := json.Marshal(map[string]string{"provider": a.Provider, "host": a.Host})
			status, body, err := proxyToDevice(context.Background(), "git_oauth_start", strings.TrimSpace(a.DeviceID), http.MethodPost, "/git/provider/oauth/start", payload)
			if err != nil {
				return mcpToolError(err.Error())
			}
			if status/100 != 2 {
				return mcpToolError(string(body))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		sess, err := startGitOAuthDevice(context.Background(), a.Provider, a.Host)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]any{
			"session_id":       sess.ID,
			"provider":         sess.Provider,
			"host":             sess.Host,
			"user_code":        sess.UserCode,
			"verification_uri": sess.VerificationURI,
			"interval":         sess.Interval,
			"expires_at":       sess.ExpiresAt.Unix(),
			"byo_client":       sess.BYOClient,
		})
	case "git_oauth_status":
		var a struct {
			SessionID string `json:"session_id"`
			DeviceID  string `json:"device_id"`
		}
		json.Unmarshal(call.Arguments, &a)
		if strings.TrimSpace(a.SessionID) == "" {
			return mcpToolError("session_id is required")
		}
		if strings.TrimSpace(a.DeviceID) != "" {
			path := "/git/provider/oauth/status?session=" + url.QueryEscape(a.SessionID)
			status, body, err := proxyToDevice(context.Background(), "git_oauth_status", strings.TrimSpace(a.DeviceID), http.MethodGet, path, nil)
			if err != nil {
				return mcpToolError(err.Error())
			}
			if status/100 != 2 {
				return mcpToolError(string(body))
			}
			return mcpToolJSON(json.RawMessage(body))
		}
		sess, ok := getGitOAuthSession(a.SessionID)
		if !ok {
			return mcpToolJSON(map[string]any{"state": "unknown", "error": "session not found"})
		}
		return mcpToolJSON(map[string]any{
			"session_id":       sess.ID,
			"provider":         sess.Provider,
			"host":             sess.Host,
			"user_code":        sess.UserCode,
			"verification_uri": sess.VerificationURI,
			"interval":         sess.Interval,
			"expires_at":       sess.ExpiresAt.Unix(),
			"state":            sess.State,
			"username":         sess.Username,
			"error":            sess.Error,
			"byo_client":       sess.BYOClient,
		})
	case "yaver_auth_unlink":
		var a struct {
			Provider string `json:"provider"`
			TOTPCode string `json:"totp_code"`
		}
		json.Unmarshal(call.Arguments, &a)
		result, err := authUnlink(context.Background(), a.Provider, a.TOTPCode)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_merge_start":
		var a struct {
			TOTPCode string `json:"totp_code"`
		}
		json.Unmarshal(call.Arguments, &a)
		result, err := authMergeStart(context.Background(), a.TOTPCode)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "yaver_auth_merge_wait":
		var a struct {
			MergeToken      string `json:"merge_token"`
			TimeoutSec      int    `json:"timeout_seconds"`
			PollIntervalSec int    `json:"poll_interval_seconds"`
		}
		json.Unmarshal(call.Arguments, &a)
		if a.TimeoutSec > 600 {
			a.TimeoutSec = 600
		}
		result, err := authMergeWait(context.Background(), a.MergeToken, a.TimeoutSec, a.PollIntervalSec)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	// --- Cloud provisioning ---
	case "cloud_provision":
		var a struct {
			Host string `json:"host"`
			Name string `json:"name"`
			Opts string `json:"opts"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloudProvision(a.Host, a.Name, a.Opts))
	case "cloud_destroy":
		var a struct {
			Host string `json:"host"`
			ID   string `json:"id"`
			Opts string `json:"opts"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloudDestroy(a.Host, a.ID, a.Opts))
	case "studio_list":
		return mcpToolJSON(mcpStudioList())
	case "switch_cost":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSwitchCost(a.Dir))
	case "init_project":
		var a struct {
			Opts string `json:"opts"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpInitProject(a.Opts))
	case "backend_schema":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSchemaView(a.Dir))
	case "storage_list":
		var a struct {
			Dir    string `json:"directory"`
			Bucket string `json:"bucket"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpStorageList(a.Dir, a.Bucket))
	case "shared_storage_profiles":
		return mcpToolJSON(mcpSharedStorageProfiles())
	case "shared_storage_upsert":
		var a struct {
			Profile string `json:"profile"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSharedStorageUpsert(a.Profile))
	case "shared_storage_delete":
		var a struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSharedStorageDelete(a.ID))
	case "shared_storage_list":
		var a struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSharedStorageList(a.ID, a.Path))
	case "shared_storage_search":
		var a struct {
			ID    string `json:"id"`
			Query string `json:"query"`
			Path  string `json:"path"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSharedStorageSearch(a.ID, a.Query, a.Path, a.Limit))
	case "cron_list":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpJobsList(a.Dir))
	case "console_machines":
		return mcpToolJSON(mcpConsoleMachines())
	case "deploy_run":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDeployRun(a.Dir))
	case "deploy_list":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDeployList(a.Dir))
	case "deploy_rollback":
		var a struct {
			Dir string `json:"directory"`
			ID  string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDeployRollback(a.Dir, a.ID))
	case "clone_env":
		var a struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Subset int    `json:"subsetRows"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCloneEnv(a.Source, a.Target, a.Subset))
	case "log_search":
		var a struct {
			Q        string `json:"q"`
			Services string `json:"services"`
			Limit    int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLogSearch(a.Q, a.Services, a.Limit))
	// --- Cloudflare ---
	case "cf_workers":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCFWorkers(a.Dir))
	case "cf_deploy":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCFDeploy(a.Dir))
	case "cf_pages":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCFPages(a.Dir))
	case "cf_r2":
		var a struct {
			Action string `json:"action"`
			Bucket string `json:"bucket"`
			Key    string `json:"key"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCFR2(a.Action, a.Bucket, a.Key))
	case "cf_d1":
		var a struct {
			Action string `json:"action"`
			DB     string `json:"database"`
			Query  string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCFD1(a.Action, a.DB, a.Query))
	case "cf_kv":
		var a struct {
			Action string `json:"action"`
			NS     string `json:"namespace"`
			Key    string `json:"key"`
			Value  string `json:"value"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCFKV(a.Action, a.NS, a.Key, a.Value))
	// --- GitLab ---
	case "gitlab_mrs":
		var a struct {
			Dir   string `json:"directory"`
			State string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLabMRs(a.Dir, a.State))
	case "gitlab_issues":
		var a struct {
			Dir   string `json:"directory"`
			State string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLabIssues(a.Dir, a.State))
	case "gitlab_pipelines":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLabPipelines(a.Dir))
	case "gitlab_ci":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLabCI(a.Dir))
	// --- GitHub extras ---
	case "github_repo_info":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitHubRepoInfo(a.Dir))
	case "github_releases":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitHubReleases(a.Dir))
	case "github_stars":
		var a struct {
			Repo string `json:"repo"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitHubStargazers(a.Repo))
	// --- gh / glab generic + write-op ---
	case "gh_run":
		var a struct {
			Args []string `json:"args"`
			Dir  string   `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGhRun(a.Dir, a.Args))
	case "glab_run":
		var a struct {
			Args []string `json:"args"`
			Dir  string   `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGlabRun(a.Dir, a.Args))
	case "github_pr_create":
		var a struct {
			Dir   string `json:"directory"`
			Title string `json:"title"`
			Body  string `json:"body"`
			Base  string `json:"base"`
			Head  string `json:"head"`
			Draft bool   `json:"draft"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitHubPRCreate(a.Dir, a.Title, a.Body, a.Base, a.Head, a.Draft))
	case "github_issue_create":
		var a struct {
			Dir    string   `json:"directory"`
			Title  string   `json:"title"`
			Body   string   `json:"body"`
			Labels []string `json:"labels"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitHubIssueCreate(a.Dir, a.Title, a.Body, a.Labels))
	case "github_workflow_run":
		var a struct {
			Dir      string            `json:"directory"`
			Workflow string            `json:"workflow"`
			Ref      string            `json:"ref"`
			Inputs   map[string]string `json:"inputs"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitHubWorkflowRun(a.Dir, a.Workflow, a.Ref, a.Inputs))
	case "gitlab_mr_create":
		var a struct {
			Dir          string `json:"directory"`
			Title        string `json:"title"`
			Description  string `json:"description"`
			SourceBranch string `json:"sourceBranch"`
			TargetBranch string `json:"targetBranch"`
			Draft        bool   `json:"draft"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLabMRCreate(a.Dir, a.Title, a.Description, a.SourceBranch, a.TargetBranch, a.Draft))
	case "gitlab_issue_create":
		var a struct {
			Dir         string   `json:"directory"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Labels      []string `json:"labels"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLabIssueCreate(a.Dir, a.Title, a.Description, a.Labels))
	// --- PlanetScale ---
	case "pscale_branches":
		var a struct {
			DB string `json:"database"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPlanetScaleBranches(a.DB))
	case "pscale_deploy":
		var a struct {
			DB     string `json:"database"`
			Branch string `json:"branch"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPlanetScaleDeploy(a.DB, a.Branch))
	// --- Prisma ---
	case "prisma_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPrismaStatus(a.Dir))
	case "prisma_generate":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPrismaGenerate(a.Dir))
	case "prisma_push":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPrismaPush(a.Dir))
	// --- Drizzle ---
	case "drizzle_push":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDrizzlePush(a.Dir))
	case "drizzle_generate":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDrizzleGenerate(a.Dir))
	// --- Fly.io ---
	case "fly_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlyStatus(a.Dir))
	case "fly_deploy":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlyDeploy(a.Dir))
	case "fly_logs":
		var a struct {
			App string `json:"app"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlyLogs(a.App))
	// --- Railway ---
	case "railway_status":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRailwayStatus(a.Dir))
	case "railway_deploy":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRailwayDeploy(a.Dir))

	// --- Docker Extended ---
	case "docker_prune":
		var a struct {
			What string `json:"what"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerPrune(a.What))
	case "docker_disk_usage":
		return mcpToolJSON(mcpDockerDiskUsage())
	case "docker_networks":
		return mcpToolJSON(mcpDockerNetworks())
	case "docker_volumes":
		return mcpToolJSON(mcpDockerVolumes())
	case "docker_inspect":
		var a struct {
			Target string `json:"target"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerInspect(a.Target))
	case "docker_stats":
		return mcpToolJSON(mcpDockerStats())
	case "docker_build":
		var a struct {
			Dir        string `json:"directory"`
			Tag        string `json:"tag"`
			Dockerfile string `json:"dockerfile"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerBuild(a.Dir, a.Tag, a.Dockerfile))
	case "docker_pull":
		var a struct {
			Image string `json:"image"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerPull(a.Image))
	case "docker_push":
		var a struct {
			Image string `json:"image"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerPush(a.Image))
	case "docker_stop":
		var a struct {
			Container string `json:"container"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerStop(a.Container))
	case "docker_start":
		var a struct {
			Container string `json:"container"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerStart(a.Container))
	case "docker_restart":
		var a struct {
			Container string `json:"container"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerRestart(a.Container))
	case "docker_rm":
		var a struct {
			Container string `json:"container"`
			Force     bool   `json:"force"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerRm(a.Container, a.Force))
	case "docker_rmi":
		var a struct {
			Image string `json:"image"`
			Force bool   `json:"force"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerRmi(a.Image, a.Force))
	case "docker_top":
		var a struct {
			Container string `json:"container"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerTop(a.Container))
	case "docker_port":
		var a struct {
			Container string `json:"container"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerPort(a.Container))
	case "docker_cp":
		var a struct {
			Src string `json:"source"`
			Dst string `json:"destination"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerCp(a.Src, a.Dst))
	case "docker_history":
		var a struct {
			Image string `json:"image"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerHistory(a.Image))

	// --- Git Extended ---
	case "git_stash":
		var a struct {
			Action  string `json:"action"`
			Message string `json:"message"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitStash(a.Action, a.Message))
	case "git_blame_file":
		var a struct {
			File  string `json:"file"`
			Lines string `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitBlame(a.File, a.Lines))
	case "git_log_advanced":
		var a struct {
			Dir    string `json:"directory"`
			Author string `json:"author"`
			Since  string `json:"since"`
			Until  string `json:"until"`
			Path   string `json:"path"`
			Count  int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitLogAdvanced(a.Dir, a.Author, a.Since, a.Until, a.Path, a.Count))
	case "git_branches":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitBranches(a.Dir))
	case "git_tags":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitTags(a.Dir))
	case "git_remotes":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitRemotes(a.Dir))
	case "git_reflog":
		var a struct {
			Dir   string `json:"directory"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitReflog(a.Dir, a.Count))
	case "git_shortlog":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGitShortlog(a.Dir))

	// --- Helm ---
	case "helm_list":
		var a struct {
			NS string `json:"namespace"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHelmList(a.NS))
	case "helm_status":
		var a struct {
			Release string `json:"release"`
			NS      string `json:"namespace"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHelmStatus(a.Release, a.NS))
	case "helm_values":
		var a struct {
			Release string `json:"release"`
			NS      string `json:"namespace"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHelmValues(a.Release, a.NS))
	case "helm_search":
		var a struct {
			Query string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHelmSearch(a.Query))
	case "helm_repos":
		return mcpToolJSON(mcpHelmRepos())
	case "helm_history":
		var a struct {
			Release string `json:"release"`
			NS      string `json:"namespace"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHelmHistory(a.Release, a.NS))

	// --- System Extended ---
	case "free_memory":
		return mcpToolJSON(mcpFreeMemory())
	case "listen_ports":
		return mcpToolJSON(mcpListenPorts())
	case "find_large_files":
		var a struct {
			Dir    string `json:"directory"`
			SizeMB int    `json:"size_mb"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFindLargeFiles(a.Dir, a.SizeMB))
	case "tree_dir":
		var a struct {
			Dir   string `json:"directory"`
			Depth int    `json:"depth"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTreeDir(a.Dir, a.Depth))
	case "lines_of_code":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLinesOfCode(a.Dir))

	// --- Network & Packet Capture ---
	case "tcpdump":
		var a struct {
			Iface  string `json:"interface"`
			Count  int    `json:"count"`
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTcpdump(a.Iface, a.Count, a.Filter))
	case "tcpdump_http":
		var a struct {
			Iface string `json:"interface"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTcpdumpHTTP(a.Iface, a.Count))
	case "tcpdump_dns":
		var a struct {
			Iface string `json:"interface"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTcpdumpDNS(a.Iface, a.Count))
	case "tshark":
		var a struct {
			Iface  string `json:"interface"`
			Count  int    `json:"count"`
			Filter string `json:"filter"`
			Fields string `json:"fields"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTshark(a.Iface, a.Count, a.Filter, a.Fields))
	case "pcap_analyze":
		var a struct {
			File   string `json:"file"`
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPcapAnalyze(a.File, a.Filter))
	case "pcap_stats":
		var a struct {
			File string `json:"file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPcapStats(a.File))
	case "netcat":
		var a struct {
			Host string `json:"host"`
			Port int    `json:"port"`
			Data string `json:"data"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNetcat(a.Host, a.Port, a.Data))
	case "port_scan":
		var a struct {
			Host  string `json:"host"`
			Ports string `json:"ports"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPortScan(a.Host, a.Ports))
	case "arp_table":
		return mcpToolJSON(mcpArpTable())
	case "arp_scan":
		var a struct {
			Subnet string `json:"subnet"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpArpScan(a.Subnet))
	case "nmap_scan":
		var a struct {
			Target string `json:"target"`
			Type   string `json:"type"`
			Ports  string `json:"ports"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNmapScan(a.Target, a.Type, a.Ports))
	case "traceroute_host":
		var a struct {
			Host    string `json:"host"`
			MaxHops int    `json:"max_hops"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTraceroute(a.Host, a.MaxHops))
	case "mtr_report":
		var a struct {
			Host  string `json:"host"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMtr(a.Host, a.Count))
	case "network_interfaces":
		return mcpToolJSON(mcpNetworkInterfaces())
	case "ip_route":
		return mcpToolJSON(mcpIPRoute())
	case "network_connections":
		var a struct {
			State string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNetworkConnections(a.State))
	case "bandwidth_test":
		var a struct {
			Host string `json:"host"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBandwidthTest(a.Host))
	case "curl_timings":
		var a struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCurlTimings(a.URL))

	// --- Linux System ---
	case "dmesg":
		var a struct {
			Level string `json:"level"`
			Lines int    `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDmesg(a.Level, a.Lines))
	case "lsmod":
		return mcpToolJSON(mcpLsmod())
	case "modinfo":
		var a struct {
			Module string `json:"module"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpModinfo(a.Module))
	case "insmod":
		var a struct {
			Module string `json:"module"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpInsmod(a.Module))
	case "rmmod":
		var a struct {
			Module string `json:"module"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRmmod(a.Module))
	case "uname":
		return mcpToolJSON(mcpUname())
	case "sysctl":
		var a struct {
			Key string `json:"key"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSysctl(a.Key))
	case "top_snapshot":
		return mcpToolJSON(mcpTopSnapshot())
	case "ps_aux":
		var a struct {
			Sort   string `json:"sort"`
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPsAux(a.Sort, a.Filter))
	case "ps_tree":
		return mcpToolJSON(mcpPsTree())
	case "load_average":
		return mcpToolJSON(mcpLoadAverage())
	case "vmstat":
		var a struct {
			Count int `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVmstat(a.Count))
	case "swap_info":
		return mcpToolJSON(mcpSwap())
	case "df":
		var a struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDf(a.Path))
	case "du":
		var a struct {
			Path  string `json:"path"`
			Depth int    `json:"depth"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDu(a.Path, a.Depth))
	case "lsblk":
		return mcpToolJSON(mcpLsblk())
	case "fdisk_list":
		return mcpToolJSON(mcpFdisk())
	case "mounts":
		return mcpToolJSON(mcpMounts())
	case "iostat":
		return mcpToolJSON(mcpIostat())
	case "tree":
		var a struct {
			Path     string `json:"path"`
			Depth    int    `json:"depth"`
			All      bool   `json:"all"`
			DirsOnly bool   `json:"dirs_only"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTree(a.Path, a.Depth, a.All, a.DirsOnly))
	case "cpu_info":
		return mcpToolJSON(mcpCpuInfo())
	case "lspci":
		return mcpToolJSON(mcpLspci())
	case "lsusb":
		return mcpToolJSON(mcpLsusb())
	case "sensors":
		return mcpToolJSON(mcpSensors())
	case "ufw":
		var a struct {
			Action string `json:"action"`
			Rule   string `json:"rule"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpUfw(a.Action, a.Rule))
	case "iptables_list":
		return mcpToolJSON(mcpIptables())
	case "who_is_logged_in":
		return mcpToolJSON(mcpWho())
	case "last_logins":
		var a struct {
			Count int `json:"count"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLastLogins(a.Count))
	case "timedate_info":
		return mcpToolJSON(mcpTimeDateInfo())
	case "hostname_info":
		return mcpToolJSON(mcpHostnameInfo())

	// --- Compilers & Language Suites ---
	case "make_targets":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMakeTargets(a.Dir))
	case "make_run":
		var a struct {
			Dir    string `json:"directory"`
			Target string `json:"target"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMakeRun(a.Dir, a.Target))
	case "make_clean":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMakeClean(a.Dir))
	case "cmake_configure":
		var a struct {
			Dir      string `json:"directory"`
			BuildDir string `json:"build_dir"`
			Gen      string `json:"generator"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCMakeConfigure(a.Dir, a.BuildDir, a.Gen))
	case "cmake_build":
		var a struct {
			Dir      string `json:"directory"`
			BuildDir string `json:"build_dir"`
			Parallel int    `json:"parallel"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCMakeBuild(a.Dir, a.BuildDir, a.Parallel))
	case "cmake_test":
		var a struct {
			Dir      string `json:"directory"`
			BuildDir string `json:"build_dir"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCMakeTest(a.Dir, a.BuildDir))
	case "cmake_install":
		var a struct {
			Dir      string `json:"directory"`
			BuildDir string `json:"build_dir"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCMakeInstall(a.Dir, a.BuildDir))
	case "gcc_compile":
		var a struct {
			File   string   `json:"file"`
			Output string   `json:"output"`
			Flags  []string `json:"flags"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGCCCompile(a.File, a.Output, a.Flags))
	case "clang_compile":
		var a struct {
			File   string   `json:"file"`
			Output string   `json:"output"`
			Flags  []string `json:"flags"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpClangCompile(a.File, a.Output, a.Flags))
	case "clang_tidy_check":
		var a struct {
			File string `json:"file"`
			Dir  string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpClangTidy(a.File, a.Dir))
	case "clang_format_file":
		var a struct {
			File    string `json:"file"`
			InPlace bool   `json:"in_place"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpClangFormat(a.File, a.InPlace))
	case "objdump":
		var a struct {
			File string `json:"file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLLVMObjdump(a.File))
	case "binary_size":
		var a struct {
			File string `json:"file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLLVMSize(a.File))
	case "nm_symbols":
		var a struct {
			File string `json:"file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLLVMNM(a.File))
	case "compiler_version":
		var a struct {
			Compiler string `json:"compiler"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCompilerVersion(a.Compiler))
	// Cargo (Rust)
	case "cargo_build":
		var a struct {
			Dir     string `json:"directory"`
			Release bool   `json:"release"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoBuild(a.Dir, a.Release))
	case "cargo_test_suite":
		var a struct {
			Dir      string `json:"directory"`
			TestName string `json:"test_name"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoTest(a.Dir, a.TestName))
	case "cargo_clippy":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoClippy(a.Dir))
	case "cargo_fmt":
		var a struct {
			Dir   string `json:"directory"`
			Check bool   `json:"check"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoFmt(a.Dir, a.Check))
	case "cargo_doc":
		var a struct {
			Dir  string `json:"directory"`
			Open bool   `json:"open"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoDoc(a.Dir, a.Open))
	case "cargo_bench_suite":
		var a struct {
			Dir   string `json:"directory"`
			Bench string `json:"bench"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoBench(a.Dir, a.Bench))
	case "cargo_tree_deps":
		var a struct {
			Dir   string `json:"directory"`
			Depth int    `json:"depth"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoTree(a.Dir, a.Depth))
	case "cargo_update_deps":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoUpdate(a.Dir))
	case "cargo_audit_deps":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoAudit(a.Dir))
	case "cargo_check_only":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoCheck(a.Dir))
	case "cargo_clean":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoClean(a.Dir))
	case "cargo_add_crate":
		var a struct {
			Dir   string `json:"directory"`
			Crate string `json:"crate"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoAdd(a.Dir, a.Crate))
	case "cargo_remove_crate":
		var a struct {
			Dir   string `json:"directory"`
			Crate string `json:"crate"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoRemove(a.Dir, a.Crate))
	// Go
	case "go_build":
		var a struct {
			Dir    string `json:"directory"`
			Output string `json:"output"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoBuild(a.Dir, a.Output))
	case "go_test_suite":
		var a struct {
			Dir     string `json:"directory"`
			Verbose bool   `json:"verbose"`
			Race    bool   `json:"race"`
			Cover   bool   `json:"cover"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoTest(a.Dir, a.Verbose, a.Race, a.Cover))
	case "go_vet_check":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoVet(a.Dir))
	case "go_mod_tidy":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModTidy(a.Dir))
	case "go_mod_graph":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModGraph(a.Dir))
	case "go_mod_why":
		var a struct {
			Dir    string `json:"directory"`
			Module string `json:"module"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModWhy(a.Dir, a.Module))
	case "go_generate":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoGenerate(a.Dir))
	case "go_fmt_check":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoFmt(a.Dir))
	case "go_staticcheck":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoStaticcheck(a.Dir))
	case "go_vulncheck":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoVulncheck(a.Dir))
	// Python
	case "pytest_suite":
		var a struct {
			Dir      string `json:"directory"`
			Verbose  bool   `json:"verbose"`
			Coverage bool   `json:"coverage"`
			Marker   string `json:"marker"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPytest(a.Dir, a.Verbose, a.Coverage, a.Marker))
	case "ruff_suite":
		var a struct {
			Dir    string `json:"directory"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRuff(a.Dir, a.Action))
	case "mypy_check":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMypy(a.Dir))
	case "black_format":
		var a struct {
			Dir   string `json:"directory"`
			Check bool   `json:"check"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBlack(a.Dir, a.Check))
	case "pip_compile":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPipCompile(a.Dir))
	case "uv_install":
		var a struct {
			Dir string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpUVInstall(a.Dir))
	// Node.js/TypeScript
	case "npm_run_script":
		var a struct {
			Dir    string `json:"directory"`
			Script string `json:"script"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNPMRun(a.Dir, a.Script))
	case "tsc_check":
		var a struct {
			Dir    string `json:"directory"`
			NoEmit bool   `json:"no_emit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTSC(a.Dir, a.NoEmit))
	case "eslint_check":
		var a struct {
			Dir string `json:"directory"`
			Fix bool   `json:"fix"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpESLint(a.Dir, a.Fix))
	case "prettier_check":
		var a struct {
			Dir   string `json:"directory"`
			Check bool   `json:"check"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPrettier(a.Dir, a.Check))
	case "biome_suite":
		var a struct {
			Dir    string `json:"directory"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBiome(a.Dir, a.Action))

	// --- Static Analysis ---
	case "cppcheck":
		var a struct {
			Dir       string `json:"directory"`
			File      string `json:"file"`
			Severity  string `json:"severity"`
			EnableAll bool   `json:"enable_all"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCppcheck(a.Dir, a.File, a.Severity, a.EnableAll))
	case "shellcheck":
		var a struct {
			File     string `json:"file"`
			Shell    string `json:"shell"`
			Severity string `json:"severity"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpShellcheck(a.File, a.Shell, a.Severity))
	case "hadolint":
		var a struct {
			File              string   `json:"file"`
			TrustedRegistries []string `json:"trusted_registries"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHadolint(a.File, a.TrustedRegistries))
	case "semgrep":
		var a struct {
			Dir        string `json:"directory"`
			Config     string `json:"config"`
			AutoConfig bool   `json:"auto_config"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSemgrep(a.Dir, a.Config, a.AutoConfig))
	case "sonarscanner":
		var a struct {
			Dir        string `json:"directory"`
			ProjectKey string `json:"project_key"`
			HostURL    string `json:"host_url"`
			Token      string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSonarScanner(a.Dir, a.ProjectKey, a.HostURL, a.Token))
	case "bandit":
		var a struct {
			Dir      string `json:"directory"`
			File     string `json:"file"`
			Severity string `json:"severity"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBandit(a.Dir, a.File, a.Severity))
	case "gosec":
		var a struct {
			Dir    string `json:"directory"`
			NoFail bool   `json:"no_fail"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGosec(a.Dir, a.NoFail))
	case "brakeman":
		var a struct {
			Dir        string `json:"directory"`
			Confidence int    `json:"confidence"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBrakeman(a.Dir, a.Confidence))
	case "safety_check":
		var a struct {
			Dir  string `json:"directory"`
			File string `json:"file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSafetyCheck(a.Dir, a.File))
	case "trivy_fs_scan":
		var a struct {
			Dir      string `json:"directory"`
			Severity string `json:"severity"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTrivyFSScan(a.Dir, a.Severity))
	// --- Profiling & Debugging ---
	case "valgrind_memcheck":
		var a struct {
			Binary string   `json:"binary"`
			Args   []string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpValgrindMemcheck(a.Binary, a.Args))
	case "valgrind_callgrind":
		var a struct {
			Binary     string   `json:"binary"`
			Args       []string `json:"args"`
			OutputFile string   `json:"output_file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpValgrindCallgrind(a.Binary, a.Args, a.OutputFile))
	case "valgrind_massif":
		var a struct {
			Binary     string   `json:"binary"`
			Args       []string `json:"args"`
			OutputFile string   `json:"output_file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpValgrindMassif(a.Binary, a.Args, a.OutputFile))
	case "gdb_backtrace":
		var a struct {
			PID    int    `json:"pid"`
			Binary string `json:"binary"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGDBBacktrace(a.PID, a.Binary))
	case "lldb_backtrace":
		var a struct {
			PID    int    `json:"pid"`
			Binary string `json:"binary"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLLDBBacktrace(a.PID, a.Binary))
	case "strace_trace":
		var a struct {
			PID           int      `json:"pid"`
			Binary        string   `json:"binary"`
			SyscallFilter string   `json:"syscall_filter"`
			Args          []string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpStraceTrace(a.PID, a.Binary, a.SyscallFilter, a.Args))
	case "ltrace_trace":
		var a struct {
			PID    int      `json:"pid"`
			Binary string   `json:"binary"`
			Args   []string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLtraceTrace(a.PID, a.Binary, a.Args))
	case "perf_record":
		var a struct {
			Binary     string   `json:"binary"`
			Args       []string `json:"args"`
			Duration   int      `json:"duration"`
			OutputFile string   `json:"output_file"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPerfRecord(a.Binary, a.Args, a.Duration, a.OutputFile))
	case "perf_stat":
		var a struct {
			Binary string   `json:"binary"`
			Args   []string `json:"args"`
			Events string   `json:"events"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPerfStat(a.Binary, a.Args, a.Events))
	case "go_pprof_cpu":
		var a struct {
			Dir      string `json:"directory"`
			Duration int    `json:"duration"`
			Binary   string `json:"binary"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoPprofCPU(a.Dir, a.Duration, a.Binary))
	case "go_pprof_heap":
		var a struct {
			Dir string `json:"directory"`
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoPprofHeap(a.Dir, a.URL))
	case "heaptrack":
		var a struct {
			Binary string   `json:"binary"`
			Args   []string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpHeaptrack(a.Binary, a.Args))
	// --- Code Metrics ---
	case "cyclomatic_complexity":
		var a struct {
			Dir      string `json:"directory"`
			Language string `json:"language"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCyclomaticComplexity(a.Dir, a.Language))
	case "lizard":
		var a struct {
			Dir       string `json:"directory"`
			Threshold int    `json:"threshold"`
			Languages string `json:"languages"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLizard(a.Dir, a.Threshold, a.Languages))
	case "loc_count":
		var a struct {
			Dir  string `json:"directory"`
			Tool string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLOCCount(a.Dir, a.Tool))

	// --- System Logs & Debugging ---
	case "journalctl":
		var a struct {
			Unit     string `json:"unit"`
			Priority string `json:"priority"`
			Lines    int    `json:"lines"`
			Boot     bool   `json:"boot"`
			Since    string `json:"since"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpJournalctl(a.Unit, a.Priority, a.Lines, a.Boot, a.Since))
	case "journalctl_errors":
		return mcpToolJSON(mcpJournalctlErrors())
	case "journalctl_disk_usage":
		return mcpToolJSON(mcpJournalctlDiskUsage())
	case "systemctl":
		var a struct {
			Action string `json:"action"`
			Unit   string `json:"unit"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSystemctl(a.Action, a.Unit))
	case "gdb_attach":
		var a struct {
			PID      int    `json:"pid"`
			Commands string `json:"commands"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGDBAttach(a.PID, a.Commands))
	case "gdb_core_dump":
		var a struct {
			Binary string `json:"binary"`
			Core   string `json:"corefile"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGDBCoreDump(a.Binary, a.Core))
	case "lldb_attach":
		var a struct {
			PID      int    `json:"pid"`
			Commands string `json:"commands"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLLDBAttach(a.PID, a.Commands))
	case "coredump_list":
		return mcpToolJSON(mcpCoredumpList())
	case "coredump_info":
		var a struct {
			PID string `json:"pid"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCoredumpInfo(a.PID))
	case "syslog":
		var a struct {
			File   string `json:"file"`
			Lines  int    `json:"lines"`
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSyslog(a.File, a.Lines, a.Filter))
	case "auth_log":
		var a struct {
			Lines int `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAuthLog(a.Lines))

	// --- Guest Access ---
	case "guest_invite":
		var args struct {
			Email     string   `json:"email"`
			UserID    string   `json:"user_id"`
			Scope     string   `json:"scope"`
			DeviceIDs []string `json:"device_ids"`
			Projects  []string `json:"projects"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.Email) == "" && strings.TrimSpace(args.UserID) == "" {
			return mcpToolError("email or user_id is required")
		}
		if args.Scope != "" && args.Scope != GuestScopeFull && args.Scope != GuestScopeFeedbackOnly && args.Scope != GuestScopeSDKProject {
			return mcpToolError("scope must be 'full', 'feedback-only', or 'sdk-project'")
		}
		invResult, err := InviteGuestWith(s.convexURL, s.token, InviteGuestOpts{
			Email:             args.Email,
			UserID:            args.UserID,
			ProposedDeviceIDs: args.DeviceIDs,
			Scope:             args.Scope,
			AllowedProjects:   args.Projects,
		})
		if err != nil {
			return mcpToolError(err.Error())
		}
		// Refresh guest list
		if ids, err := FetchGuestUserIds(s.convexURL, s.token, s.deviceID); err == nil {
			s.guestUserIDsMu.Lock()
			s.guestUserIDs = ids
			s.guestUserIDsMu.Unlock()
		}
		scopeShown := invResult.Scope
		if scopeShown == "" {
			scopeShown = args.Scope
		}
		if scopeShown == "" {
			scopeShown = GuestScopeFeedbackOnly
		}
		target := strings.TrimSpace(args.Email)
		if target == "" {
			target = "user_id:" + strings.TrimSpace(args.UserID)
		}
		msg := fmt.Sprintf("Invitation sent to %s.\nInvite code: %s\nScope: %s\n", target, invResult.InviteCode, scopeShown)
		if scopeShown == GuestScopeFeedbackOnly {
			msg += "Hardened end-user tier — no /tasks, /vibing, /dev, /projects; /info redacted; fix-triggered tasks force-containerized. Re-invite with scope='full' for teammates.\n"
		} else if scopeShown == GuestScopeSDKProject {
			msg += "Project-scoped Feedback SDK tier — delegated guest access is limited to the selected projects and device slice.\n"
		} else {
			msg += "Full teammate tier — tasks, vibing, dev-server proxy, builds, projects, plus the feedback/voice surface.\n"
		}
		if ids := cleanProjectList(args.DeviceIDs); len(ids) > 0 {
			msg += fmt.Sprintf("Machine narrowing: %s.\n", strings.Join(ids, ", "))
		}
		if projs := cleanProjectList(args.Projects); len(projs) > 0 {
			msg += fmt.Sprintf("Project narrowing: %s (this guest cannot see/fix feedback or run tasks outside these projects).\n", strings.Join(projs, ", "))
		}
		if invResult.GuestRegistered {
			msg += "This email is already registered — they'll see the invitation in their Yaver app."
		} else {
			msg += "This email is not yet registered. Share the invite code — they can accept with any OAuth method after signing up."
		}
		msg += "\nExpires in 2 days."
		return mcpToolResult(msg)

	case "guest_list":
		guests, err := FetchGuestList(s.convexURL, s.token)
		if err != nil {
			return mcpToolError("failed to fetch guests: " + err.Error())
		}
		if len(guests) == 0 {
			return mcpToolResult("No guests. Use guest_invite to invite someone.")
		}
		var sb strings.Builder
		sb.WriteString("Guests:\n")
		for _, g := range guests {
			name := g.FullName
			if name == "" {
				name = "(not yet signed up)"
			}
			sb.WriteString(fmt.Sprintf("- %s [%s] %s\n", g.Email, g.Status, name))
		}
		return mcpToolResult(sb.String())

	case "guest_revoke":
		var args struct {
			Email string `json:"email"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Email == "" {
			return mcpToolError("email is required")
		}
		if err := RevokeGuest(s.convexURL, s.token, args.Email); err != nil {
			return mcpToolError(err.Error())
		}
		// Refresh guest list
		if ids, err := FetchGuestUserIds(s.convexURL, s.token, s.deviceID); err == nil {
			s.guestUserIDsMu.Lock()
			s.guestUserIDs = ids
			s.guestUserIDsMu.Unlock()
		}
		// Clear token cache for non-owner users
		s.tokenCache.Range(func(key, value interface{}) bool {
			info := value.(*cachedTokenInfo)
			if info.userID != s.ownerUserID && !info.isSdk {
				s.tokenCache.Delete(key)
			}
			return true
		})
		return mcpToolResult(fmt.Sprintf("Guest access revoked for %s", args.Email))

	case "company_ai_resolve":
		var args struct {
			TeamID            string `json:"teamId"`
			WorkKind          string `json:"workKind"`
			RequestedRunner   string `json:"requestedRunner"`
			RequestedModel    string `json:"requestedModel"`
			RequestedProvider string `json:"requestedProvider"`
			RequestedDeviceID string `json:"requestedDeviceId"`
		}
		json.Unmarshal(call.Arguments, &args)
		if strings.TrimSpace(args.TeamID) == "" || strings.TrimSpace(args.WorkKind) == "" {
			return mcpToolError("teamId and workKind are required")
		}
		payload := map[string]interface{}{
			"teamId":   args.TeamID,
			"workKind": args.WorkKind,
			"source":   "mcp",
		}
		if args.RequestedRunner != "" {
			payload["requestedRunner"] = args.RequestedRunner
		}
		if args.RequestedModel != "" {
			payload["requestedModel"] = args.RequestedModel
		}
		if args.RequestedProvider != "" {
			payload["requestedProvider"] = args.RequestedProvider
		}
		if args.RequestedDeviceID != "" {
			payload["requestedDeviceId"] = args.RequestedDeviceID
		}
		data, err := resolveCompanyAIWithFallback(s.convexURL, s.token, payload)
		if err != nil {
			return mcpToolError("company_ai_resolve failed: " + err.Error())
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") != nil {
			return mcpToolResult(string(data))
		}
		return mcpToolResult(pretty.String())

	// --- Grand MCP: ops (unified verb-based API) ---
	case "ops":
		var req OpsRequest
		if err := json.Unmarshal(call.Arguments, &req); err != nil {
			return mcpToolError("invalid ops request: " + err.Error())
		}
		// Callers that reach the MCP dispatch have already cleared
		// the owner-auth boundary upstream (auth() in /mcp). Guests
		// and support sessions use their own scoped routes, not /mcp,
		// so we can safely assume owner here. The dispatcher itself
		// enforces per-verb policy via AllowGuest on non-owner paths
		// when /ops HTTP is called directly.
		octx := OpsContext{Ctx: context.Background(), Server: s, Caller: "owner"}
		out := dispatchOps(octx, req)
		body, _ := json.MarshalIndent(out, "", "  ")
		return mcpToolResult(string(body))

	case "ops_plan":
		var req OpsRequest
		if err := json.Unmarshal(call.Arguments, &req); err != nil {
			return mcpToolError("invalid ops request: " + err.Error())
		}
		octx := OpsContext{Ctx: context.Background(), Server: s, Caller: "owner"}
		body, _ := json.MarshalIndent(buildOpsExecutionPlan(octx, req), "", "  ")
		return mcpToolResult(string(body))

	// --- SDK-token MCP ---
	case "sdk_token_create":
		var args struct {
			Label        string   `json:"label"`
			Scopes       []string `json:"scopes"`
			AllowedCIDRs []string `json:"allowedCIDRs"`
			ExpiresInMs  int64    `json:"expiresInMs"`
		}
		json.Unmarshal(call.Arguments, &args)
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || cfg.AuthToken == "" {
			return mcpToolError("not signed in — run 'yaver auth' on the agent first")
		}
		opts := SdkTokenCreateOpts{
			Label:        args.Label,
			Scopes:       args.Scopes,
			AllowedCIDRs: args.AllowedCIDRs,
			ExpiresInMs:  args.ExpiresInMs,
		}
		tok, err := CreateSdkToken(s.convexURL, cfg.AuthToken, opts)
		if err != nil {
			return mcpToolError(err.Error())
		}
		// Raw token returned once — standard sdk-token contract.
		out := map[string]interface{}{
			"ok":    true,
			"token": tok,
			"hint":  "store this token now — it cannot be retrieved again. Use it as Authorization: Bearer <token> on scoped SDK endpoints.",
			"label": args.Label,
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		return mcpToolResult(string(body))

	// --- Feedback SDK (MCP) ---
	case "feedback_list":
		var args struct {
			Limit int `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		// Reuse the same HTTP surface the CLI hits.
		body, _, _ := s.feedbackHttpMCP("GET", "/feedback", nil)
		if args.Limit > 0 {
			// Best-effort truncation; Feedback list is small in practice.
			var list []interface{}
			if err := json.Unmarshal(body, &list); err == nil && len(list) > args.Limit {
				trimmed, _ := json.MarshalIndent(list[:args.Limit], "", "  ")
				return mcpToolResult(string(trimmed))
			}
		}
		return mcpToolResult(string(body))

	case "feedback_show":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.ID == "" {
			return mcpToolError("id is required")
		}
		body, status, _ := s.feedbackHttpMCP("GET", "/feedback/"+args.ID, nil)
		if status == 404 {
			return mcpToolError("feedback not found: " + args.ID)
		}
		return mcpToolResult(string(body))

	case "feedback_fix":
		var args struct {
			ID     string `json:"id"`
			Runner string `json:"runner"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.ID == "" {
			return mcpToolError("id is required")
		}
		payload := map[string]interface{}{}
		if args.Runner != "" {
			payload["runner"] = args.Runner
		}
		body, _, _ := s.feedbackHttpMCP("POST", "/feedback/"+args.ID+"/fix", payload)
		return mcpToolResult(string(body))

	case "feedback_delete":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.ID == "" {
			return mcpToolError("id is required")
		}
		_, status, _ := s.feedbackHttpMCP("DELETE", "/feedback/"+args.ID, nil)
		if status >= 400 {
			return mcpToolError(fmt.Sprintf("delete failed: HTTP %d", status))
		}
		return mcpToolResult(fmt.Sprintf("Feedback %s deleted.", args.ID))

	// --- Source maps (MCP) ---
	case "sourcemaps_list":
		store := GlobalSourceMapStore()
		out := store.List()
		body, _ := json.MarshalIndent(out, "", "  ")
		return mcpToolResult(string(body))

	case "sourcemaps_delete":
		var args struct {
			App     string `json:"app"`
			Version string `json:"version"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.App == "" || args.Version == "" {
			return mcpToolError("app and version are required")
		}
		if err := GlobalSourceMapStore().Delete(args.App, args.Version); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Source map %s@%s deleted.", args.App, args.Version))

	// --- Managed / self-hosted toggle (per subsystem) ---
	case "managed_get":
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || cfg.AuthToken == "" {
			return mcpToolError("not signed in")
		}
		body, err := fetchManagedSettings(context.Background(), s.convexURL, cfg.AuthToken)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(string(body))

	case "managed_set":
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || cfg.AuthToken == "" {
			return mcpToolError("not signed in")
		}
		var args struct {
			Subsystem string          `json:"subsystem"`
			Managed   json.RawMessage `json:"managed"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Subsystem == "" {
			return mcpToolError("subsystem is required")
		}
		if err := setManagedSubsystem(context.Background(), s.convexURL, cfg.AuthToken, args.Subsystem, args.Managed); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("managed.%s updated", args.Subsystem))

	// --- Monorepo workspace manifest ---
	case "workspace_init":
		var args opsWorkspacePayload
		json.Unmarshal(call.Arguments, &args)
		args.Op = "init"
		p, _ := json.Marshal(args)
		r := opsWorkspaceHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "workspace_list":
		var args opsWorkspacePayload
		json.Unmarshal(call.Arguments, &args)
		args.Op = "list"
		p, _ := json.Marshal(args)
		r := opsWorkspaceHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "workspace_status":
		var args opsWorkspacePayload
		json.Unmarshal(call.Arguments, &args)
		args.Op = "status"
		p, _ := json.Marshal(args)
		r := opsWorkspaceHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "workspace_scaffold":
		var args opsWorkspacePayload
		json.Unmarshal(call.Arguments, &args)
		args.Op = "scaffold"
		p, _ := json.Marshal(args)
		r := opsWorkspaceHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "workspace_web_apps":
		var args struct {
			Root string `json:"root,omitempty"`
			Kind string `json:"kind,omitempty"`
		}
		json.Unmarshal(call.Arguments, &args)
		root := strings.TrimSpace(args.Root)
		if root == "" && s.taskMgr != nil {
			root = s.taskMgr.workDir
		}
		m, _, err := loadWorkspaceManifestForHTTP(root)
		if err != nil {
			return mcpToolError(err.Error())
		}
		views := buildAppViews(root, m)
		kindFilter := strings.TrimSpace(args.Kind)
		if kindFilter == "" {
			kindFilter = "web"
		}
		wanted := map[DevServerKind]bool{}
		for _, k := range strings.Split(kindFilter, ",") {
			wanted[DevServerKind(strings.TrimSpace(k))] = true
		}
		out := make([]*WorkspaceAppView, 0, len(views))
		for _, v := range views {
			if wanted[v.Kind] {
				out = append(out, v)
			}
		}
		body, _ := json.MarshalIndent(map[string]interface{}{"ok": true, "root": root, "apps": out}, "", "  ")
		return mcpToolResult(string(body))

	case "web_preview_start":
		var args opsWebPreviewPayload
		json.Unmarshal(call.Arguments, &args)
		args.Action = "start"
		p, _ := json.Marshal(args)
		r := opsWebPreviewHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "web_preview_reload":
		p, _ := json.Marshal(opsWebPreviewPayload{Action: "reload"})
		r := opsWebPreviewHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "web_preview_stop":
		p, _ := json.Marshal(opsWebPreviewPayload{Action: "stop"})
		r := opsWebPreviewHandler(OpsContext{Server: s, Caller: "owner"}, p)
		body, _ := json.MarshalIndent(r, "", "  ")
		return mcpToolResult(string(body))

	case "preview_stop_serving":
		body, _ := json.MarshalIndent(s.stopServingPreviewResult(), "", "  ")
		return mcpToolResult(string(body))

	case "project_context":
		var args struct {
			WorkDir string `json:"workDir,omitempty"`
		}
		json.Unmarshal(call.Arguments, &args)
		workDir := strings.TrimSpace(args.WorkDir)
		if workDir == "" && s.taskMgr != nil {
			workDir = s.taskMgr.workDir
		}
		body, _ := json.MarshalIndent(projectContextFiles(workDir), "", "  ")
		return mcpToolResult(string(body))

	case "diagnose":
		var args struct {
			Only []string `json:"only,omitempty"`
			Skip []string `json:"skip,omitempty"`
			Fix  bool     `json:"fix,omitempty"`
		}
		json.Unmarshal(call.Arguments, &args)
		events := make([]DiagEvent, 0, 64)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		report := RunDiagnose(ctx, DiagnoseOptions{
			Only:  args.Only,
			Skip:  args.Skip,
			Fix:   args.Fix,
			Agent: s,
		}, func(ev DiagEvent) {
			events = append(events, ev)
		})
		body, _ := json.MarshalIndent(map[string]interface{}{
			"ok":     report.Failures == 0,
			"report": report,
			"events": events,
		}, "", "  ")
		return mcpToolResult(string(body))

	case "sourcemaps_resolve":
		var args struct {
			App     string `json:"app"`
			Version string `json:"version"`
			Line    int    `json:"line"`
			Column  int    `json:"column"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.App == "" || args.Version == "" {
			return mcpToolError("app and version are required")
		}
		src, line, col, name, ok := GlobalSourceMapStore().Resolve(args.App, args.Version, args.Line, args.Column)
		if !ok {
			return mcpToolError(fmt.Sprintf("no source map for %s@%s (or frame unresolvable)", args.App, args.Version))
		}
		out := map[string]interface{}{
			"source": src,
			"line":   line,
			"column": col,
			"name":   name,
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		return mcpToolResult(string(body))

	case "ops_verbs":
		verbs := listOpsVerbs()
		out := make([]map[string]interface{}, 0, len(verbs))
		for _, v := range verbs {
			out = append(out, map[string]interface{}{
				"name":        v.Name,
				"description": v.Description,
				"streaming":   v.Streaming,
				"allowGuest":  v.AllowGuest,
				"payload":     v.Schema,
			})
		}
		body, _ := json.MarshalIndent(map[string]interface{}{"verbs": out, "count": len(out)}, "", "  ")
		return mcpToolResult(string(body))

	// --- Primary device preference ---
	case "device_primary_get":
		ctx := context.Background()
		current, err := primaryGetCurrent(ctx, s.token, s.convexURL)
		if err != nil {
			return mcpToolError("failed to read settings: " + err.Error())
		}
		devices, err := primaryListDevices(ctx, s.token, s.convexURL)
		if err != nil {
			return mcpToolError("failed to list devices: " + err.Error())
		}
		if current == "" {
			return mcpToolResult(fmt.Sprintf("No primary device set. %d device(s) registered — multi-device users must pick manually when connecting.", len(devices)))
		}
		name := "(unknown)"
		for _, d := range devices {
			if d.DeviceID == current {
				name = d.Name
				break
			}
		}
		return mcpToolResult(fmt.Sprintf("Primary device: %s (deviceId=%s)", name, current))

	case "device_primary_set":
		var args struct {
			DeviceID string `json:"deviceId"`
			Clear    bool   `json:"clear"`
		}
		json.Unmarshal(call.Arguments, &args)
		ctx := context.Background()
		if args.Clear {
			if err := primarySaveRaw(ctx, s.token, s.convexURL, "", true); err != nil {
				return mcpToolError(err.Error())
			}
			return mcpToolResult("Primary device cleared.")
		}
		target := strings.TrimSpace(args.DeviceID)
		if target == "" {
			return mcpToolError("deviceId or clear=true is required")
		}
		devices, err := primaryListDevices(ctx, s.token, s.convexURL)
		if err != nil {
			return mcpToolError("failed to list devices: " + err.Error())
		}
		var matches []primaryDevice
		for _, d := range devices {
			if d.DeviceID == target {
				matches = []primaryDevice{d}
				break
			}
			if strings.HasPrefix(d.DeviceID, target) {
				matches = append(matches, d)
			}
		}
		if len(matches) == 0 {
			return mcpToolError(fmt.Sprintf("No device matches %q — run device_primary_get to list available devices", target))
		}
		if len(matches) > 1 {
			return mcpToolError(fmt.Sprintf("%q matches %d devices — use a longer prefix", target, len(matches)))
		}
		chosen := matches[0]
		if chosen.IsGuest {
			return mcpToolError("shared (guest) devices cannot be marked primary — the host can revoke access at any time")
		}
		if err := primarySaveRaw(ctx, s.token, s.convexURL, chosen.DeviceID, false); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Primary device set to %s (%s).", chosen.Name, chosen.DeviceID))

	// --- Primary device sugar (resolve "primary" → deviceId, then act) ---
	case "primary_auth":
		var a struct {
			Runner string `json:"runner"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPrimaryAuth(a.Runner))

	case "primary_status":
		return mcpToolJSON(mcpPrimaryStatus())

	case "primary_ping":
		return mcpToolJSON(mcpPrimaryPing())

	case "primary_projects":
		var a struct {
			MobileOnly bool `json:"mobile_only"`
		}
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPrimaryProjects(a.MobileOnly))

	// --- Remote Support Sessions ---
	case "support_start":
		var args struct {
			TTL   string `json:"ttl"`
			Label string `json:"label"`
			Shell bool   `json:"shell"`
		}
		json.Unmarshal(call.Arguments, &args)
		ttl := defaultSupportTTL
		if strings.TrimSpace(args.TTL) != "" {
			if d, err := time.ParseDuration(args.TTL); err == nil && d > 0 {
				ttl = d
			}
		}
		sess := StartSupportSession(SupportStartOptions{
			Label: args.Label,
			TTL:   ttl,
			Shell: args.Shell,
		})
		return mcpToolJSON(supportSessionPayload(sess, s.deviceID, true))

	case "support_status":
		sess := activeSupportSnapshot()
		if sess == nil {
			return mcpToolJSON(map[string]interface{}{"active": false})
		}
		return mcpToolJSON(supportSessionPayload(sess, s.deviceID, true))

	case "support_stop":
		stopped := StopSupportSession()
		return mcpToolJSON(map[string]interface{}{"ok": true, "stopped": stopped})

	case "guest_config":
		var args struct {
			Email             string   `json:"email"`
			DailyLimit        *int     `json:"daily_limit"`
			UsageMode         string   `json:"usage_mode"`
			AllowedRunners    []string `json:"allowed_runners"`
			ResourcePreset    string   `json:"resource_preset"`
			UseHostAPIKeys    *bool    `json:"use_host_api_keys"`
			AllowGuestAPIKeys *bool    `json:"allow_guest_api_keys"`
			AllowDesktop      *bool    `json:"allow_desktop_control"`
			AllowBrowser      *bool    `json:"allow_browser_control"`
			AllowTunnel       *bool    `json:"allow_tunnel_forward"`
			RequireIsolation  *bool    `json:"require_isolation"`
			CPULimitPercent   *int     `json:"cpu_limit_percent"`
			RAMLimitMB        *int     `json:"ram_limit_mb"`
			PriorityMode      string   `json:"priority_mode"`
		}
		json.Unmarshal(call.Arguments, &args)

		if args.Email == "" {
			// List all configs
			configs, err := FetchGuestConfigs(s.convexURL, s.token)
			if err != nil {
				return mcpToolError("failed to fetch configs: " + err.Error())
			}
			if len(configs) == 0 {
				return mcpToolResult("No guest configs. Guests use default settings (unlimited).")
			}
			var sb strings.Builder
			sb.WriteString("Guest Configs:\n")
			for _, c := range configs {
				mode := c.UsageMode
				if mode == "" {
					mode = "always"
				}
				limit := "unlimited"
				if c.DailyTokenLimit != nil && *c.DailyTokenLimit > 0 {
					limit = fmt.Sprintf("%ds/day", *c.DailyTokenLimit)
				}
				runners := "all"
				if len(c.AllowedRunners) > 0 {
					runners = strings.Join(c.AllowedRunners, ",")
				}
				hostKeys := "inherit"
				if c.UseHostAPIKeys != nil {
					hostKeys = fmt.Sprintf("%v", *c.UseHostAPIKeys)
				}
				preset := guestResourcePreset(&c)
				guestKeys := "inherit"
				if c.AllowGuestProvidedAPIKeys != nil {
					guestKeys = fmt.Sprintf("%v", *c.AllowGuestProvidedAPIKeys)
				}
				desktop := fmt.Sprintf("%v", guestAllowDesktopControl(&c))
				tunnels := fmt.Sprintf("%v", guestAllowTunnelForward(&c))
				isolation := "false"
				if c.RequireIsolation != nil && *c.RequireIsolation {
					isolation = "true"
				}
				sb.WriteString(fmt.Sprintf("- %s (%s): mode=%s limit=%s runners=%s preset=%s host_keys=%s guest_keys=%s desktop=%s tunnels=%s isolation=%s\n",
					c.GuestEmail, c.GuestName, mode, limit, runners, preset, hostKeys, guestKeys, desktop, tunnels, isolation))
			}
			return mcpToolResult(sb.String())
		}

		// If no update fields, just show this guest's config
		isUpdate := args.DailyLimit != nil || args.UsageMode != "" || args.AllowedRunners != nil ||
			args.ResourcePreset != "" || args.UseHostAPIKeys != nil || args.AllowGuestAPIKeys != nil ||
			args.AllowDesktop != nil || args.AllowBrowser != nil || args.AllowTunnel != nil || args.RequireIsolation != nil ||
			args.CPULimitPercent != nil || args.RAMLimitMB != nil || args.PriorityMode != ""
		if !isUpdate {
			configs, err := FetchGuestConfigs(s.convexURL, s.token)
			if err != nil {
				return mcpToolError("failed to fetch config: " + err.Error())
			}
			for _, c := range configs {
				if c.GuestEmail == args.Email {
					mode := c.UsageMode
					if mode == "" {
						mode = "always"
					}
					limit := "unlimited"
					if c.DailyTokenLimit != nil && *c.DailyTokenLimit > 0 {
						limit = fmt.Sprintf("%d seconds/day", *c.DailyTokenLimit)
					}
					runners := "all"
					if len(c.AllowedRunners) > 0 {
						runners = strings.Join(c.AllowedRunners, ", ")
					}
					hostKeys := "inherit"
					if c.UseHostAPIKeys != nil {
						hostKeys = fmt.Sprintf("%v", *c.UseHostAPIKeys)
					}
					preset := guestResourcePreset(&c)
					guestKeys := "inherit"
					if c.AllowGuestProvidedAPIKeys != nil {
						guestKeys = fmt.Sprintf("%v", *c.AllowGuestProvidedAPIKeys)
					}
					desktop := fmt.Sprintf("%v", guestAllowDesktopControl(&c))
					browser := fmt.Sprintf("%v", guestAllowBrowserControl(&c))
					tunnels := fmt.Sprintf("%v", guestAllowTunnelForward(&c))
					isolation := "false"
					if c.RequireIsolation != nil && *c.RequireIsolation {
						isolation = "true"
					}
					cpuCap := "unset"
					if c.CPULimitPercent != nil {
						cpuCap = fmt.Sprintf("%d%%", *c.CPULimitPercent)
					}
					ramCap := "unset"
					if c.RAMLimitMB != nil {
						ramCap = fmt.Sprintf("%d MB", *c.RAMLimitMB)
					}
					priority := c.PriorityMode
					if priority == "" {
						priority = "default"
					}
					return mcpToolResult(fmt.Sprintf("Config for %s (%s):\n  Mode: %s\n  Daily limit: %s\n  Runners: %s\n  Resource preset: %s\n  Host API keys: %s\n  Guest API keys: %s\n  Desktop control: %s\n  Browser control: %s\n  Tunnel forward: %s\n  Docker isolation: %s\n  CPU cap: %s\n  RAM cap: %s\n  Priority: %s",
						c.GuestEmail, c.GuestName, mode, limit, runners, preset, hostKeys, guestKeys, desktop, browser, tunnels, isolation, cpuCap, ramCap, priority))
				}
			}
			return mcpToolResult(fmt.Sprintf("No config found for %s", args.Email))
		}

		// Update config
		payload := map[string]interface{}{"email": args.Email}
		if args.DailyLimit != nil {
			payload["dailyTokenLimit"] = *args.DailyLimit
		}
		if args.UsageMode != "" {
			payload["usageMode"] = args.UsageMode
		}
		if args.AllowedRunners != nil {
			payload["allowedRunners"] = args.AllowedRunners
		}
		if args.ResourcePreset != "" {
			payload["resourcePreset"] = args.ResourcePreset
		}
		if args.UseHostAPIKeys != nil {
			payload["useHostApiKeys"] = *args.UseHostAPIKeys
		}
		if args.AllowGuestAPIKeys != nil {
			payload["allowGuestProvidedApiKeys"] = *args.AllowGuestAPIKeys
		}
		if args.AllowDesktop != nil {
			payload["allowDesktopControl"] = *args.AllowDesktop
		}
		if args.AllowBrowser != nil {
			payload["allowBrowserControl"] = *args.AllowBrowser
		}
		if args.AllowTunnel != nil {
			payload["allowTunnelForward"] = *args.AllowTunnel
		}
		if args.RequireIsolation != nil {
			payload["requireIsolation"] = *args.RequireIsolation
		}
		if args.CPULimitPercent != nil {
			payload["cpuLimitPercent"] = *args.CPULimitPercent
		}
		if args.RAMLimitMB != nil {
			payload["ramLimitMb"] = *args.RAMLimitMB
		}
		if args.PriorityMode != "" {
			payload["priorityMode"] = args.PriorityMode
		}
		if err := UpdateGuestConfig(s.convexURL, s.token, payload); err != nil {
			return mcpToolError(err.Error())
		}
		// Refresh cached configs
		if s.guestConfigMgr != nil {
			if cfgs, err := FetchGuestConfigs(s.convexURL, s.token); err == nil {
				s.guestConfigMgr.UpdateConfigs(cfgs)
			}
		}
		return mcpToolResult(fmt.Sprintf("Config updated for %s", args.Email))

	case "sandbox_status":
		return mcpToolJSON(s.sandboxSummary())

	case "sandbox_config":
		var args struct {
			ContainerizeGuests *bool  `json:"containerize_guests"`
			ContainerizeHost   *bool  `json:"containerize_host"`
			CPULimit           string `json:"cpu_limit"`
			MemoryLimit        string `json:"memory_limit"`
			NetworkMode        string `json:"network_mode"`
			ReadOnly           *bool  `json:"read_only"`
		}
		json.Unmarshal(call.Arguments, &args)

		// Validate network mode
		if args.NetworkMode != "" {
			switch args.NetworkMode {
			case "host", "bridge", "none":
			default:
				return mcpToolError("network_mode must be 'host', 'bridge', or 'none'")
			}
		}

		if err := s.ensureContainerRunner(); err != nil {
			return mcpToolError(err.Error() + " — install Docker first")
		}
		if args.ContainerizeGuests != nil {
			s.containerizeGuests = *args.ContainerizeGuests
			if s.taskMgr != nil {
				s.taskMgr.ContainerizeGuests = *args.ContainerizeGuests
				s.taskMgr.ContainerRunner = s.containerRunner
			}
		}
		if args.ContainerizeHost != nil {
			s.containerizeHost = *args.ContainerizeHost
			if s.taskMgr != nil {
				s.taskMgr.ContainerizeHost = *args.ContainerizeHost
				s.taskMgr.ContainerRunner = s.containerRunner
			}
		}
		if args.CPULimit != "" && s.taskMgr != nil {
			s.taskMgr.ContainerCPU = args.CPULimit
		}
		if args.MemoryLimit != "" && s.taskMgr != nil {
			s.taskMgr.ContainerMemory = args.MemoryLimit
		}
		if args.NetworkMode != "" && s.taskMgr != nil {
			s.taskMgr.ContainerNetwork = args.NetworkMode
		}
		if args.ReadOnly != nil && s.taskMgr != nil {
			s.taskMgr.ContainerReadOnly = *args.ReadOnly
		}

		if s.taskMgr != nil {
			s.taskMgr.ContainerRunner = s.containerRunner
			if s.taskMgr.ContainerNetwork == "" {
				s.taskMgr.ContainerNetwork = "host"
			}
		}
		s.persistSandboxConfig()

		return mcpToolJSON(s.sandboxSummary())

	case "sandbox_quickstart":
		var args struct {
			Mode       string `json:"mode"`
			BuildImage *bool  `json:"build_image"`
		}
		json.Unmarshal(call.Arguments, &args)
		buildImage := true
		if args.BuildImage != nil {
			buildImage = *args.BuildImage
		}
		summary, message, err := s.applySandboxQuickstart(args.Mode, buildImage)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{
			"ok":      true,
			"message": message,
			"sandbox": summary,
		})

	case "guest_usage":
		var args struct {
			Date string `json:"date"`
		}
		json.Unmarshal(call.Arguments, &args)
		usage, err := FetchGuestUsage(s.convexURL, s.token, args.Date)
		if err != nil {
			return mcpToolError("failed to fetch usage: " + err.Error())
		}
		if len(usage) == 0 {
			date := args.Date
			if date == "" {
				date = "today"
			}
			return mcpToolResult(fmt.Sprintf("No usage for %s.", date))
		}
		var sb strings.Builder
		sb.WriteString("Guest Usage:\n")
		for _, u := range usage {
			sb.WriteString(fmt.Sprintf("- %s (%s): %.0f seconds on %s\n",
				u.GuestEmail, u.GuestName, u.SecondsUsed, u.Date))
		}
		return mcpToolResult(sb.String())

	case "forgot_password":
		var args struct {
			Email string `json:"email"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Email == "" {
			return mcpToolError("email is required")
		}
		if err := RequestPasswordReset(s.convexURL, args.Email); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("If an account exists for that email, a password reset link has been sent. The link expires in 1 hour.")

	case "change_password":
		var args struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.CurrentPassword == "" || args.NewPassword == "" {
			return mcpToolError("current_password and new_password are required")
		}
		if len(args.NewPassword) < 8 {
			return mcpToolError("new password must be at least 8 characters")
		}
		if err := ChangePassword(s.convexURL, s.token, args.CurrentPassword, args.NewPassword); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Password changed successfully.")

	// --- yaver-test-sdk: local CI runner ---
	case "testkit_list_specs":
		return s.mcpTestkitListSpecs(call.Arguments)
	case "testkit_run":
		return s.mcpTestkitRun(call.Arguments)
	case "testkit_last_failure":
		return s.mcpTestkitLastFailure(call.Arguments)
	case "testkit_flake_report":
		return s.mcpTestkitFlakeReport(call.Arguments)
	case "testkit_self_heal_selector":
		return s.mcpTestkitSelfHealSelector(call.Arguments)

	// --- Monitor (errors / flags / releases / uptime / analytics) ---
	case "error_list":
		return s.mcpErrorList(call.Arguments)
	case "error_resolve":
		return s.mcpErrorResolve(call.Arguments)
	case "flag_list":
		return s.mcpFlagList()
	case "flag_set":
		return s.mcpFlagSet(call.Arguments)
	case "flag_evaluate":
		return s.mcpFlagEvaluate(call.Arguments)
	case "release_list":
		return s.mcpReleaseList(call.Arguments)
	case "release_rollout":
		return s.mcpReleaseRollout(call.Arguments)
	case "release_rollback":
		return s.mcpReleaseRollback(call.Arguments)
	case "monitor_list":
		return s.mcpMonitorList()
	case "monitor_add":
		return s.mcpMonitorAdd(call.Arguments)
	case "monitor_remove":
		return s.mcpMonitorRemove(call.Arguments)
	case "analytics_events":
		return s.mcpAnalyticsEvents(call.Arguments)

	// --- Project wizard (fullstack generator) ---
	case "project_wizard_start":
		sess, q := StartWizard()
		return mcpToolJSON(map[string]interface{}{
			"sessionId": sess.ID,
			"question":  q,
			"note":      "Call project_wizard_answer for each question, then project_wizard_generate.",
		})
	case "project_wizard_answer":
		var args struct {
			SessionID  string `json:"sessionId"`
			QuestionID string `json:"questionId"`
			Answer     string `json:"answer"`
		}
		json.Unmarshal(call.Arguments, &args)
		q, err := AnswerWizard(args.SessionID, args.QuestionID, args.Answer)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{
			"question": q,
			"session":  GetWizard(args.SessionID),
		})
	case "project_wizard_generate":
		var args struct {
			SessionID string `json:"sessionId"`
			ParentDir string `json:"parentDir"`
		}
		json.Unmarshal(call.Arguments, &args)
		res, err := GenerateProject(args.SessionID, args.ParentDir)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(res)
	case "project_new_quick":
		return s.mcpProjectNewQuick(call.Arguments)
	case "project_self_host_create":
		return s.mcpProjectNewQuick(call.Arguments)

	case "yaver_onboard":
		return mcpToolResult(yaverOnboardChecklist())
	case "yaver_self_host_onboarding":
		var args yaverSelfHostOnboardingArgs
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpYaverSelfHostOnboarding(args))
	case "yaver_managed_cloud_onboarding":
		var args yaverManagedCloudOnboardingArgs
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpYaverManagedCloudOnboarding(args))

	// --- Forms ---
	case "form_list":
		forms, _ := loadForms()
		return mcpToolJSON(map[string]interface{}{"forms": forms})
	case "form_create":
		var f Form
		json.Unmarshal(call.Arguments, &f)
		if f.Name == "" {
			return mcpToolError("name required")
		}
		f.ID = randomFormID()
		f.CreatedAt = time.Now().UTC()
		forms, _ := loadForms()
		forms = append(forms, f)
		if err := saveForms(forms); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"form": f, "submitUrl": "/forms/" + f.ID + "/submit"})
	case "form_submissions":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		subs, _ := readSubmissions(args.ID, 100)
		return mcpToolJSON(map[string]interface{}{"submissions": subs})
	case "form_delete":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		forms, _ := loadForms()
		out := forms[:0]
		for _, f := range forms {
			if f.ID != args.ID {
				out = append(out, f)
			}
		}
		_ = saveForms(out)
		return mcpToolResult("deleted")

	// --- Newsletter ---
	case "newsletter_subscribers":
		subs := loadSubscribers()
		return mcpToolJSON(map[string]interface{}{
			"subscribers": subs,
			"counts": map[string]int{
				"total":        len(subs),
				"confirmed":    countByStatus(subs, "confirmed"),
				"pending":      countByStatus(subs, "pending"),
				"unsubscribed": countByStatus(subs, "unsubscribed"),
			},
		})
	case "newsletter_create":
		var c Campaign
		json.Unmarshal(call.Arguments, &c)
		if c.Subject == "" {
			return mcpToolError("subject required")
		}
		c.ID = randomFormID()
		c.Status = "draft"
		c.CreatedAt = time.Now().UTC()
		_ = saveCampaigns(append(loadCampaigns(), c))
		return mcpToolJSON(map[string]interface{}{"campaign": c})
	case "newsletter_send":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		camps := loadCampaigns()
		found := false
		for i := range camps {
			if camps[i].ID == args.ID && camps[i].Status == "draft" {
				camps[i].Status = "sending"
				_ = saveCampaigns(camps)
				go broadcastCampaign(args.ID)
				found = true
				break
			}
		}
		if !found {
			return mcpToolError("campaign not found or already sent")
		}
		return mcpToolResult("broadcast started")
	case "newsletter_compose_from_git":
		var opts ComposeNewsletterOptions
		json.Unmarshal(call.Arguments, &opts)
		act, err := CollectGitActivity(opts)
		if err != nil {
			return mcpToolError(err.Error())
		}
		subject, draft := BuildNewsletterDraft(act, opts.Subject)
		result := map[string]interface{}{"subject": subject, "draft": draft, "activity": act}
		if opts.Execute {
			prompt := BuildComposePrompt(act, draft, opts.Instructions)
			if polished, err := runMailDraftInline(opts.Runner, prompt); err == nil {
				result["draft"] = polished
				draft = polished
			}
		}
		if opts.SaveDraft {
			camp := Campaign{ID: randomFormID(), Subject: subject, Body: draft, Status: "draft", CreatedAt: time.Now().UTC()}
			_ = saveCampaigns(append(loadCampaigns(), camp))
			result["campaignId"] = camp.ID
		}
		return mcpToolJSON(result)

	// --- Jobs ---
	case "jobs_list":
		queue, _ := listJobs("queue")
		dlq, _ := listJobs("dlq")
		return mcpToolJSON(map[string]interface{}{"queue": queue, "dlq": dlq})
	case "jobs_enqueue":
		var args struct {
			Handler     string          `json:"handler"`
			Payload     json.RawMessage `json:"payload"`
			DelaySec    int             `json:"delaySec"`
			MaxAttempts int             `json:"maxAttempts"`
			BackoffSec  int             `json:"backoffSec"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Handler == "" {
			return mcpToolError("handler required")
		}
		opts := []JobOption{}
		if args.DelaySec > 0 {
			opts = append(opts, WithDelay(time.Duration(args.DelaySec)*time.Second))
		}
		if args.MaxAttempts > 0 {
			opts = append(opts, WithMaxAttempts(args.MaxAttempts))
		}
		if args.BackoffSec > 0 {
			opts = append(opts, WithBackoffSec(args.BackoffSec))
		}
		j, err := EnqueueJob(args.Handler, json.RawMessage(args.Payload), opts...)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"job": j})
	case "jobs_retry":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		dlq, _ := listJobs("dlq")
		for _, j := range dlq {
			if j.ID == args.ID {
				j.Attempts = 0
				j.LastError = ""
				j.RunAt = time.Now().UTC()
				_ = removeJob("dlq", args.ID)
				_ = writeJob("queue", &j)
				return mcpToolResult("requeued")
			}
		}
		return mcpToolError("job not in dlq")
	case "jobs_cancel":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		_ = removeJob("queue", args.ID)
		return mcpToolResult("cancelled")

	// --- Image + PDF ---
	case "img_optimize":
		var args struct {
			Src  string `json:"src"`
			Root string `json:"root"`
			W    int    `json:"w"`
			H    int    `json:"h"`
			Fmt  string `json:"fmt"`
			Q    int    `json:"q"`
		}
		json.Unmarshal(call.Arguments, &args)
		u := fmt.Sprintf("/img?src=%s", args.Src)
		if args.Root != "" {
			u += "&root=" + args.Root
		}
		if args.W > 0 {
			u += fmt.Sprintf("&w=%d", args.W)
		}
		if args.H > 0 {
			u += fmt.Sprintf("&h=%d", args.H)
		}
		if args.Fmt != "" {
			u += "&fmt=" + args.Fmt
		}
		if args.Q > 0 {
			u += fmt.Sprintf("&q=%d", args.Q)
		}
		return mcpToolJSON(map[string]interface{}{"url": u})
	case "pdf_render":
		var opts PDFRenderOptions
		json.Unmarshal(call.Arguments, &opts)
		pdf, err := RenderPDF(opts)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{
			"size":   len(pdf),
			"base64": base64.StdEncoding.EncodeToString(pdf),
		})

	// --- OAuth provider admin ---
	case "oauth_client_list":
		return mcpToolJSON(map[string]interface{}{"clients": loadOauthClients()})
	case "oauth_client_create":
		var args struct {
			Name         string   `json:"name"`
			RedirectUris []string `json:"redirectUris"`
			Scopes       []string `json:"scopes"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Name == "" || len(args.RedirectUris) == 0 {
			return mcpToolError("name + redirectUris required")
		}
		secret := randomFormID() + randomFormID() + randomFormID()
		hashed, _, _ := hashPassword(secret)
		client := OAuthClient{
			ID: randomFormID(), Secret: hashed, Name: args.Name,
			RedirectURIs: args.RedirectUris, Scopes: args.Scopes,
			CreatedAt: time.Now().UTC(),
		}
		oauthMu.Lock()
		oauthClients = append(loadOauthClients(), client)
		oauthMu.Unlock()
		_ = saveOauthClients()
		return mcpToolJSON(map[string]interface{}{
			"client_id": client.ID, "client_secret": secret,
			"note": "Secret is shown ONCE — save it now.",
		})
	case "oauth_user_list":
		users := loadOauthUsers()
		out := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			out = append(out, map[string]interface{}{"id": u.ID, "email": u.Email, "name": u.Name})
		}
		return mcpToolJSON(map[string]interface{}{"users": out})
	case "oauth_user_create":
		var args struct {
			Email, Name, Password string
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Email == "" || args.Password == "" {
			return mcpToolError("email + password required")
		}
		h, salt, _ := hashPassword(args.Password)
		u := OAuthUser{ID: randomFormID(), Email: strings.ToLower(args.Email), Name: args.Name, Hash: h, Salt: salt, CreatedAt: time.Now().UTC()}
		oauthMu.Lock()
		oauthUsers = append(loadOauthUsers(), u)
		oauthMu.Unlock()
		_ = saveOauthUsers()
		return mcpToolResult("user created")

	// --- Mail ---
	case "mail_inbox":
		var opts MailFetchOptions
		json.Unmarshal(call.Arguments, &opts)
		msgs, err := FetchMail(opts)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"messages": msgs, "counts": countByClassification(msgs)})
	case "mail_draft":
		var args struct {
			ID           string `json:"id"`
			Provider     string `json:"provider"`
			Instructions string `json:"instructions"`
			Execute      bool   `json:"execute"`
			Runner       string `json:"runner"`
		}
		json.Unmarshal(call.Arguments, &args)
		all, err := FetchMail(MailFetchOptions{Provider: args.Provider, Folder: "inbox", Limit: 50})
		if err != nil {
			return mcpToolError(err.Error())
		}
		var target MailMessage
		var thread []MailMessage
		for _, m := range all {
			if m.ID == args.ID {
				target = m
			}
		}
		if target.ID == "" {
			return mcpToolError("message not found in recent window")
		}
		for _, m := range all {
			if m.ThreadID == target.ThreadID {
				thread = append(thread, m)
			}
		}
		sent, _ := FetchMail(MailFetchOptions{Provider: args.Provider, Folder: "sent", Limit: 10})
		prompt := BuildDraftPrompt(target, thread, sent, args.Instructions)
		result := map[string]interface{}{"target": target, "prompt": prompt}
		if args.Execute {
			if reply, err := runMailDraftInline(args.Runner, prompt); err == nil {
				result["draft"] = reply
			} else {
				result["error"] = err.Error()
			}
		}
		return mcpToolJSON(result)

	// --- Shortener ---
	case "short_list":
		return mcpToolJSON(map[string]interface{}{"links": loadShortLinks()})
	case "short_create":
		var args struct{ URL, Code, Label string }
		json.Unmarshal(call.Arguments, &args)
		if args.URL == "" {
			return mcpToolError("url required")
		}
		if args.Code == "" {
			args.Code = randomShortCode()
		}
		links := loadShortLinks()
		for _, l := range links {
			if l.Code == args.Code {
				return mcpToolError("code taken")
			}
		}
		link := ShortLink{Code: args.Code, URL: args.URL, Label: args.Label, CreatedAt: time.Now().UTC()}
		shortMu.Lock()
		shortLinks = append(links, link)
		_ = saveShortLinks()
		shortMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"link": link, "publicUrl": "/s/" + link.Code})
	case "short_clicks":
		var args struct{ Code string }
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(map[string]interface{}{"code": args.Code})
	case "short_delete":
		var args struct{ Code string }
		json.Unmarshal(call.Arguments, &args)
		links := loadShortLinks()
		out := links[:0]
		for _, l := range links {
			if l.Code != args.Code {
				out = append(out, l)
			}
		}
		shortMu.Lock()
		shortLinks = out
		_ = saveShortLinks()
		shortMu.Unlock()
		return mcpToolResult("deleted")

	// --- Waitlist ---
	case "waitlist_list":
		list := loadWaitlist()
		return mcpToolJSON(map[string]interface{}{"entries": list, "total": len(list)})
	case "waitlist_leaderboard":
		list := loadWaitlist()
		top := make([]WaitlistEntry, 0, 10)
		for _, e := range list {
			if e.Invited > 0 {
				top = append(top, e)
			}
		}
		sort.Slice(top, func(i, j int) bool { return top[i].Invited > top[j].Invited })
		if len(top) > 10 {
			top = top[:10]
		}
		return mcpToolJSON(map[string]interface{}{"leaderboard": top})
	case "waitlist_delete":
		var args struct{ Email string }
		json.Unmarshal(call.Arguments, &args)
		list := loadWaitlist()
		out := list[:0]
		for _, e := range list {
			if e.Email != args.Email {
				out = append(out, e)
			}
		}
		waitlistMu.Lock()
		waitlistCache = out
		_ = saveWaitlist()
		waitlistMu.Unlock()
		return mcpToolResult("deleted")

	// --- Docs site ---
	case "docs_config":
		var cfg DocsConfig
		json.Unmarshal(call.Arguments, &cfg)
		if cfg.Path != "" {
			_ = saveDocsConfig(&cfg)
			scanDocs()
		}
		return mcpToolJSON(map[string]interface{}{"config": loadDocsConfig(), "tree": docsTree})
	case "docs_list":
		if docsIndex == nil {
			scanDocs()
		}
		return mcpToolJSON(map[string]interface{}{"tree": docsTree, "config": loadDocsConfig()})
	case "docs_search":
		var args struct{ Q string }
		json.Unmarshal(call.Arguments, &args)
		if docsIndex == nil {
			scanDocs()
		}
		q := strings.ToLower(args.Q)
		hits := []map[string]interface{}{}
		for slug, path := range docsIndex {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if strings.Contains(strings.ToLower(string(data)), q) {
				hits = append(hits, map[string]interface{}{"slug": slug, "title": prettyTitle(slug, path)})
			}
		}
		return mcpToolJSON(map[string]interface{}{"hits": hits})

	// --- Studio modules (clips, chat, A/B, invoices, affiliates, asciinema) ---

	case "ab_experiment_create":
		var e Experiment
		json.Unmarshal(call.Arguments, &e)
		if e.Key == "" || len(e.Variants) == 0 {
			return mcpToolError("key and variants required")
		}
		if e.StartedAt.IsZero() {
			e.StartedAt = time.Now().UTC()
		}
		exps := loadExperiments()
		found := false
		for i := range exps {
			if exps[i].Key == e.Key {
				exps[i] = e
				found = true
				break
			}
		}
		if !found {
			exps = append(exps, e)
		}
		abMu.Lock()
		abExperiments = exps
		_ = saveExperiments()
		abMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"experiment": e})
	case "ab_experiment_list":
		return mcpToolJSON(map[string]interface{}{"experiments": loadExperiments()})
	case "ab_assign":
		var args struct {
			Key, UserID string
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Key == "" || args.UserID == "" {
			return mcpToolError("key and userId required")
		}
		var exp *Experiment
		for i, e := range loadExperiments() {
			if e.Key == args.Key {
				exp = &abExperiments[i]
				break
			}
		}
		if exp == nil || !exp.StoppedAt.IsZero() {
			return mcpToolJSON(map[string]interface{}{"variant": "", "running": false})
		}
		variant := AssignVariant(exp, args.UserID)
		go func() {
			_ = appendABEvent(ABEvent{Key: args.Key, Variant: variant, UserID: args.UserID, Kind: "exposure", At: time.Now().UTC()})
		}()
		return mcpToolJSON(map[string]interface{}{"variant": variant, "running": true})
	case "ab_event":
		var e ABEvent
		json.Unmarshal(call.Arguments, &e)
		if e.Key == "" || e.Kind == "" {
			return mcpToolError("key and kind required")
		}
		e.At = time.Now().UTC()
		if err := appendABEvent(e); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("ok")
	case "ab_results":
		var args struct{ Key string }
		json.Unmarshal(call.Arguments, &args)
		if args.Key == "" {
			return mcpToolError("key required")
		}
		p, _ := abEventsFile()
		data, err := os.ReadFile(p)
		if err != nil {
			return mcpToolJSON(map[string]interface{}{"results": map[string]interface{}{}})
		}
		type bucket struct {
			Exposures, Conversions int
		}
		results := map[string]*bucket{}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			var ev ABEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			if ev.Key != args.Key {
				continue
			}
			b := results[ev.Variant]
			if b == nil {
				b = &bucket{}
				results[ev.Variant] = b
			}
			switch ev.Kind {
			case "exposure":
				b.Exposures++
			case "conversion":
				b.Conversions++
			}
		}
		out := map[string]interface{}{}
		for v, b := range results {
			rate := 0.0
			if b.Exposures > 0 {
				rate = float64(b.Conversions) / float64(b.Exposures)
			}
			out[v] = map[string]interface{}{"exposures": b.Exposures, "conversions": b.Conversions, "conversionRate": rate}
		}
		return mcpToolJSON(map[string]interface{}{"results": out})

	case "clip_start":
		var body struct {
			Title, Description string
		}
		json.Unmarshal(call.Arguments, &body)
		clipMu.Lock()
		if clipActive != nil {
			clipMu.Unlock()
			return mcpToolError("a recording is already running — call clip_stop first")
		}
		clipMu.Unlock()
		session := &ClipSession{
			ID:          "clip-" + randomFormID(),
			Title:       body.Title,
			Description: body.Description,
			StartedAt:   time.Now().UTC(),
			Streams:     []ClipStream{{Kind: "agent-screen", File: "agent-screen.mp4", Mime: "video/mp4"}},
		}
		if err := saveClipSession(session); err != nil {
			return mcpToolError(err.Error())
		}
		cmd, err := startAgentCapture(session)
		if err != nil {
			return mcpToolError(err.Error())
		}
		clipMu.Lock()
		clipActive = &activeSession{session: session, cmd: cmd, stopCh: make(chan struct{})}
		clipMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"session": session, "shareUrl": "/clips/" + session.ID})
	case "clip_stop":
		clipMu.Lock()
		active := clipActive
		clipActive = nil
		clipMu.Unlock()
		if active == nil {
			return mcpToolError("no active recording")
		}
		_ = active.cmd.Process.Signal(os.Interrupt)
		_ = active.cmd.Wait()
		active.session.StoppedAt = time.Now().UTC()
		active.session.DurationSec = int(active.session.StoppedAt.Sub(active.session.StartedAt).Seconds())
		for i := range active.session.Streams {
			if active.session.Streams[i].Kind == "agent-screen" {
				p, _ := sessionDir(active.session.ID)
				if info, err := os.Stat(filepath.Join(p, active.session.Streams[i].File)); err == nil {
					active.session.Streams[i].Bytes = info.Size()
				}
				active.session.Streams[i].Uploaded = true
			}
		}
		_ = saveClipSession(active.session)
		return mcpToolJSON(map[string]interface{}{"session": active.session})
	case "clip_list":
		sessions, _ := listClipSessions()
		return mcpToolJSON(map[string]interface{}{"sessions": sessions})

	case "chat_conversations":
		dir, _ := chatDir()
		entries, _ := os.ReadDir(dir)
		out := []map[string]interface{}{}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			vid := strings.TrimSuffix(e.Name(), ".jsonl")
			msgs, _ := readChatMessages(vid, 5)
			last := ""
			if len(msgs) > 0 {
				last = msgs[len(msgs)-1].Text
			}
			out = append(out, map[string]interface{}{"vid": vid, "last": last, "count": len(msgs)})
		}
		return mcpToolJSON(map[string]interface{}{"conversations": out})
	case "chat_history":
		var args struct {
			VID   string `json:"vid"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.VID == "" {
			return mcpToolError("vid required")
		}
		if args.Limit == 0 {
			args.Limit = 100
		}
		msgs, _ := readChatMessages(sanitizeVID(args.VID), args.Limit)
		return mcpToolJSON(map[string]interface{}{"messages": msgs})
	case "chat_reply":
		var args struct{ VID, Text string }
		json.Unmarshal(call.Arguments, &args)
		if args.VID == "" || args.Text == "" {
			return mcpToolError("vid and text required")
		}
		m := ChatMessage{ID: randomFormID(), VID: sanitizeVID(args.VID), From: "owner", Text: args.Text, At: time.Now().UTC()}
		_ = appendChatMessage(m)
		publishChatMessage(m)
		return mcpToolResult("sent")

	case "customer_create":
		var c Customer
		json.Unmarshal(call.Arguments, &c)
		if c.Name == "" || c.Email == "" {
			return mcpToolError("name and email required")
		}
		c.ID = randomFormID()
		invMu.Lock()
		customerCache = append(loadCustomers(), c)
		_ = saveCustomers()
		invMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"customer": c})
	case "customer_list":
		return mcpToolJSON(map[string]interface{}{"customers": loadCustomers()})
	case "invoice_create":
		var body struct {
			CustomerID string     `json:"customerId"`
			Currency   string     `json:"currency"`
			DueAt      string     `json:"dueAt"`
			TaxPercent float64    `json:"taxPercent"`
			Notes      string     `json:"notes"`
			LineItems  []LineItem `json:"lineItems"`
		}
		json.Unmarshal(call.Arguments, &body)
		if body.CustomerID == "" || len(body.LineItems) == 0 {
			return mcpToolError("customerId and lineItems required")
		}
		if body.Currency == "" {
			body.Currency = "USD"
		}
		inv := Invoice{
			ID: randomFormID(), Number: nextInvoiceNumber(),
			CustomerID: body.CustomerID, IssuedAt: time.Now().UTC(),
			Currency: body.Currency, LineItems: body.LineItems,
			Status: "draft", Notes: body.Notes,
		}
		if body.DueAt != "" {
			if t, err := time.Parse("2006-01-02", body.DueAt); err == nil {
				inv.DueAt = t
			}
		}
		for i := range inv.LineItems {
			if inv.LineItems[i].Total == 0 {
				inv.LineItems[i].Total = inv.LineItems[i].Quantity * inv.LineItems[i].UnitPrice
			}
			inv.Subtotal += inv.LineItems[i].Total
		}
		if body.TaxPercent > 0 {
			inv.Tax = inv.Subtotal * body.TaxPercent / 100
		}
		inv.Total = inv.Subtotal + inv.Tax
		invMu.Lock()
		invoiceCache = append(loadInvoices(), inv)
		_ = saveInvoices()
		invMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"invoice": inv})
	case "invoice_list":
		return mcpToolJSON(map[string]interface{}{"invoices": loadInvoices()})
	case "invoice_render_pdf":
		var args struct{ ID string }
		json.Unmarshal(call.Arguments, &args)
		inv, cust := findInvoiceAndCustomer(args.ID)
		if inv == nil || cust == nil {
			return mcpToolError("invoice or customer not found")
		}
		pdf, err := RenderPDF(PDFRenderOptions{HTML: renderInvoiceHTML(inv, cust), Format: "A4", PrintBackground: true})
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"size": len(pdf), "base64": base64.StdEncoding.EncodeToString(pdf)})
	case "invoice_payment_link":
		var args struct {
			ID, Provider, APIKey, ReturnURL string
		}
		json.Unmarshal(call.Arguments, &args)
		inv, _ := findInvoiceAndCustomer(args.ID)
		if inv == nil {
			return mcpToolError("invoice not found")
		}
		link, err := createPaymentLink(args.Provider, args.APIKey, inv, args.ReturnURL)
		if err != nil {
			return mcpToolError(err.Error())
		}
		invMu.Lock()
		inv.PaymentLink = link
		inv.PaymentSource = args.Provider
		_ = saveInvoices()
		invMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"paymentLink": link})
	case "invoice_send":
		var args struct{ ID string }
		json.Unmarshal(call.Arguments, &args)
		inv, cust := findInvoiceAndCustomer(args.ID)
		if inv == nil || cust == nil {
			return mcpToolError("invoice or customer not found")
		}
		body := fmt.Sprintf("Your invoice %s for %s %.2f is ready.\n", inv.Number, inv.Currency, inv.Total)
		if inv.PaymentLink != "" {
			body += "Pay now: " + inv.PaymentLink + "\n"
		}
		_, err := SendTransactionalEmail(SendEmailRequest{
			To: []string{cust.Email}, Subject: fmt.Sprintf("Invoice %s", inv.Number), Body: body,
		})
		if err != nil {
			return mcpToolError(err.Error())
		}
		invMu.Lock()
		inv.Status = "sent"
		_ = saveInvoices()
		invMu.Unlock()
		return mcpToolResult("sent")

	case "affiliate_create":
		var a Affiliate
		json.Unmarshal(call.Arguments, &a)
		if a.Email == "" {
			return mcpToolError("email required")
		}
		if a.Code == "" {
			a.Code = randomShortCode()
		}
		if a.ID == "" {
			a.ID = randomFormID()
		}
		if a.CommissionPercent <= 0 {
			a.CommissionPercent = 20
		}
		a.CreatedAt = time.Now().UTC()
		affMu.Lock()
		affCache = append(loadAffiliates(), a)
		_ = saveAffiliates()
		affMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"affiliate": a, "referralUrl": "?ref=" + a.Code})
	case "affiliate_list":
		return mcpToolJSON(map[string]interface{}{"affiliates": loadAffiliates()})
	case "affiliate_conversion":
		var args struct {
			ID, Currency, SourceRef string
			Amount                  float64
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Amount <= 0 {
			return mcpToolError("amount required")
		}
		list := loadAffiliates()
		var aff *Affiliate
		for i := range list {
			if list[i].ID == args.ID || list[i].Code == args.ID {
				aff = &affCache[i]
				break
			}
		}
		if aff == nil {
			return mcpToolError("affiliate not found")
		}
		if args.Currency == "" {
			args.Currency = "USD"
		}
		commission := args.Amount * aff.CommissionPercent / 100
		conv := Conversion{AffiliateID: aff.ID, Amount: args.Amount, Currency: args.Currency, Commission: commission, SourceRef: args.SourceRef, At: time.Now().UTC()}
		_ = appendConversionRow(conv)
		affMu.Lock()
		aff.TotalOwed += commission
		_ = saveAffiliates()
		affMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"conversion": conv})
	case "affiliate_payout":
		var p Payout
		json.Unmarshal(call.Arguments, &p)
		if p.Amount <= 0 {
			return mcpToolError("amount required")
		}
		list := loadAffiliates()
		var aff *Affiliate
		for i := range list {
			if list[i].ID == p.AffiliateID || list[i].Code == p.AffiliateID {
				aff = &affCache[i]
				break
			}
		}
		if aff == nil {
			return mcpToolError("affiliate not found")
		}
		p.At = time.Now().UTC()
		affMu.Lock()
		aff.TotalOwed -= p.Amount
		aff.TotalPaid += p.Amount
		_ = saveAffiliates()
		affMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"payout": p, "affiliate": aff})

	case "cast_list":
		return mcpToolJSON(map[string]interface{}{"casts": loadCasts()})
	case "cast_start":
		var body struct{ Title, Command string }
		json.Unmarshal(call.Arguments, &body)
		if body.Command == "" {
			body.Command = os.Getenv("SHELL")
			if body.Command == "" {
				body.Command = "/bin/bash"
			}
		}
		if _, err := osexec.LookPath("asciinema"); err != nil {
			return mcpToolError("asciinema not installed — brew install asciinema")
		}
		castMu.Lock()
		if activeCast != nil {
			castMu.Unlock()
			return mcpToolError("a recording is already running")
		}
		id := "cast-" + randomFormID()
		dir, _ := asciinemaDir()
		file := filepath.Join(dir, id+".cast")
		cmd := osexec.Command("asciinema", "rec", "-q", "--title", body.Title, "-c", body.Command, file)
		if err := cmd.Start(); err != nil {
			castMu.Unlock()
			return mcpToolError(err.Error())
		}
		activeCast = cmd
		activeCastInfo = &AsciiCast{ID: id, Title: body.Title, File: id + ".cast", CreatedAt: time.Now().UTC()}
		castMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"id": id})
	case "cast_stop":
		castMu.Lock()
		defer castMu.Unlock()
		if activeCast == nil || activeCastInfo == nil {
			return mcpToolError("no active recording")
		}
		_ = activeCast.Process.Signal(os.Interrupt)
		_ = activeCast.Wait()
		cast := *activeCastInfo
		castIndex = append(loadCasts(), cast)
		_ = saveCasts()
		activeCast = nil
		activeCastInfo = nil
		return mcpToolJSON(map[string]interface{}{"cast": cast})

	case "copilot_complete":
		var req CopilotRequest
		json.Unmarshal(call.Arguments, &req)
		if req.Prefix == "" {
			return mcpToolError("prefix required")
		}
		res, err := CompleteOnce(req)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(res)
	case "copilot_models":
		cmd := osexec.Command("ollama", "list")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return mcpToolJSON(map[string]interface{}{"models": []string{}, "error": err.Error()})
		}
		models := []string{}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "NAME") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				models = append(models, fields[0])
			}
		}
		return mcpToolJSON(map[string]interface{}{"models": models, "default": defaultCopilotModel})

	// --- Meetings ---
	case "meeting_create":
		var e EventType
		json.Unmarshal(call.Arguments, &e)
		if e.Slug == "" || e.Title == "" {
			return mcpToolError("slug and title required")
		}
		if e.DurationMin <= 0 {
			e.DurationMin = 30
		}
		e.CreatedAt = time.Now().UTC()
		meetMu.Lock()
		eventTypes = append(loadMeetings(), e)
		_ = saveMeetings()
		meetMu.Unlock()
		return mcpToolJSON(map[string]interface{}{"eventType": e, "publicUrl": "/meet/" + e.Slug})
	case "meeting_list":
		return mcpToolJSON(map[string]interface{}{"eventTypes": loadMeetings()})
	case "meeting_bookings":
		return mcpToolJSON(map[string]interface{}{"bookings": loadBookings()})

	case "autoinit_start":
		var spec AutoInitStart
		_ = json.Unmarshal(call.Arguments, &spec)
		if spec.WorkDir == "" {
			return mcpToolError("work_dir required")
		}
		if _, err := os.Stat(spec.WorkDir); err != nil {
			return mcpToolError("work_dir does not exist: " + spec.WorkDir)
		}
		project := spec.Project
		if project == "" {
			project = filepath.Base(spec.WorkDir)
		}
		exe, err := os.Executable()
		if err != nil {
			return mcpToolError("find yaver binary: " + err.Error())
		}
		args := []string{"autoinit", project}
		if spec.Prompt != "" {
			args = append(args, "--prompt", spec.Prompt)
		}
		if spec.Engine != "" {
			args = append(args, "--engine", spec.Engine)
		}
		if spec.Output != "" {
			args = append(args, "--output", spec.Output)
		}
		if spec.Force {
			args = append(args, "--force")
		}
		cmd := osexec.Command(exe, args...)
		cmd.Dir = spec.WorkDir
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return mcpToolError("spawn autoinit: " + err.Error())
		}
		go func() { _ = cmd.Wait() }()
		return mcpToolJSON(map[string]interface{}{
			"ok":          true,
			"loop_name":   project + "-autoinit",
			"stream_name": "autodev:" + project + "-autoinit",
			"output":      autoinitOutputPath(spec),
			"work_dir":    spec.WorkDir,
		})

	case "autoinit_status":
		var args struct {
			WorkDir string `json:"work_dir"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		if args.WorkDir == "" {
			return mcpToolError("work_dir required")
		}
		return mcpToolJSON(computeAutoInitStatus(args.WorkDir))

	case "autoideas_start":
		var spec AutoIdeasStart
		_ = json.Unmarshal(call.Arguments, &spec)
		if spec.WorkDir == "" {
			return mcpToolError("work_dir required")
		}
		if _, err := os.Stat(spec.WorkDir); err != nil {
			return mcpToolError("work_dir does not exist: " + spec.WorkDir)
		}
		project := spec.Project
		if project == "" {
			project = filepath.Base(spec.WorkDir)
		}
		exe, err := os.Executable()
		if err != nil {
			return mcpToolError("find yaver binary: " + err.Error())
		}
		args := autoIdeasBuildArgs(project, spec)
		cmd := osexec.Command(exe, append([]string{"autoideas"}, args...)...)
		cmd.Dir = spec.WorkDir
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return mcpToolError("spawn autoideas: " + err.Error())
		}
		go func() { _ = cmd.Wait() }()
		return mcpToolJSON(map[string]interface{}{
			"ok":          true,
			"loop_name":   project + "-autoideas",
			"stream_name": "autodev:" + project + "-autoideas",
			"output":      autoIdeasOutputPath(spec),
			"work_dir":    spec.WorkDir,
		})

	case "autoideas_file":
		var args struct {
			WorkDir string `json:"work_dir"`
			Output  string `json:"output"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		if args.WorkDir == "" {
			return mcpToolError("work_dir required")
		}
		// Reuse the HTTP handler by synthesising a request — keeps
		// the parsing logic in one place.
		req, _ := http.NewRequest("GET",
			fmt.Sprintf("/autoideas/file?work_dir=%s&output=%s", args.WorkDir, args.Output),
			nil)
		rec := newCapturingResponseWriter()
		s.handleAutoIdeasFile(rec, req)
		var payload interface{}
		_ = json.Unmarshal(rec.Body(), &payload)
		return mcpToolJSON(payload)

	// --- Browser Automation ---
	case "browser_open":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available. Ensure Chrome/Chromium is installed.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Headful   bool   `json:"headful"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			args.SessionID = fmt.Sprintf("browser-%d", time.Now().UnixMilli()%100000)
		}
		if err := s.browserMgr.OpenSession(args.SessionID, args.Headful); err != nil {
			return mcpToolError(fmt.Sprintf("browser_open: %v", err))
		}
		return mcpToolJSON(map[string]interface{}{
			"session_id": args.SessionID,
			"headful":    args.Headful,
			"status":     "open",
			"message":    "Browser session opened. Use browser_navigate to go to a URL.",
		})

	case "browser_close":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			return mcpToolError("session_id is required")
		}
		if err := s.browserMgr.CloseSession(args.SessionID); err != nil {
			return mcpToolError(fmt.Sprintf("browser_close: %v", err))
		}
		return mcpToolResult("Browser session closed.")

	case "browser_sessions":
		if s.browserMgr == nil {
			return mcpToolJSON(map[string]interface{}{"sessions": []interface{}{}})
		}
		return mcpToolJSON(map[string]interface{}{
			"sessions": s.browserMgr.ListSessions(),
		})

	case "browser_navigate":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			URL       string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.URL == "" {
			return mcpToolError("session_id and url are required")
		}
		result, err := s.browserMgr.Navigate(args.SessionID, args.URL)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_navigate: %v", err))
		}
		return mcpBrowserResult(result, fmt.Sprintf("Navigated to %s — title: %s", result.URL, result.Title))

	case "browser_click":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.Selector == "" {
			return mcpToolError("session_id and selector are required")
		}
		result, err := s.browserMgr.Click(args.SessionID, args.Selector)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_click: %v", err))
		}
		return mcpBrowserResult(result, fmt.Sprintf("Clicked %q — now at %s", args.Selector, result.URL))

	case "browser_type":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
			Text      string `json:"text"`
			Clear     bool   `json:"clear"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.Selector == "" || args.Text == "" {
			return mcpToolError("session_id, selector, and text are required")
		}
		result, err := s.browserMgr.Type(args.SessionID, args.Selector, args.Text, args.Clear)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_type: %v", err))
		}
		return mcpBrowserResult(result, fmt.Sprintf("Typed into %q", args.Selector))

	case "browser_select":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
			Value     string `json:"value"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.Selector == "" || args.Value == "" {
			return mcpToolError("session_id, selector, and value are required")
		}
		result, err := s.browserMgr.Select(args.SessionID, args.Selector, args.Value)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_select: %v", err))
		}
		return mcpBrowserResult(result, fmt.Sprintf("Selected %q in %q", args.Value, args.Selector))

	case "browser_scroll":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			X         int    `json:"x"`
			Y         int    `json:"y"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			return mcpToolError("session_id is required")
		}
		if args.Y == 0 && args.X == 0 {
			args.Y = 300
		}
		result, err := s.browserMgr.Scroll(args.SessionID, args.X, args.Y)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_scroll: %v", err))
		}
		return mcpBrowserResult(result, fmt.Sprintf("Scrolled by (%d, %d)", args.X, args.Y))

	case "browser_wait":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
			TimeoutMs int    `json:"timeout_ms"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.Selector == "" {
			return mcpToolError("session_id and selector are required")
		}
		if err := s.browserMgr.WaitFor(args.SessionID, args.Selector, args.TimeoutMs); err != nil {
			return mcpToolError(fmt.Sprintf("browser_wait: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Element %q is now visible.", args.Selector))

	case "browser_wait_navigation":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			TimeoutMs int    `json:"timeout_ms"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			return mcpToolError("session_id is required")
		}
		if err := s.browserMgr.WaitForNavigation(args.SessionID, args.TimeoutMs); err != nil {
			return mcpToolError(fmt.Sprintf("browser_wait_navigation: %v", err))
		}
		return mcpToolResult("Navigation completed.")

	case "browser_screenshot":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			return mcpToolError("session_id is required")
		}
		result, err := s.browserMgr.Screenshot(args.SessionID)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_screenshot: %v", err))
		}
		return mcpBrowserResult(result, fmt.Sprintf("Screenshot captured — %s", result.URL))

	case "browser_extract_text":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			return mcpToolError("session_id is required")
		}
		text, err := s.browserMgr.ExtractText(args.SessionID, args.Selector)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_extract_text: %v", err))
		}
		return mcpToolResult(text)

	case "browser_extract_attribute":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
			Attribute string `json:"attribute"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.Selector == "" || args.Attribute == "" {
			return mcpToolError("session_id, selector, and attribute are required")
		}
		value, err := s.browserMgr.ExtractAttribute(args.SessionID, args.Selector, args.Attribute)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_extract_attribute: %v", err))
		}
		return mcpToolResult(value)

	case "browser_get_dom":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" {
			return mcpToolError("session_id is required")
		}
		htmlContent, err := s.browserMgr.GetDOM(args.SessionID)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_get_dom: %v", err))
		}
		return mcpToolResult(htmlContent)

	case "browser_evaluate":
		if s.browserMgr == nil {
			return mcpToolError("Browser automation not available.")
		}
		var args struct {
			SessionID  string `json:"session_id"`
			JavaScript string `json:"javascript"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionID == "" || args.JavaScript == "" {
			return mcpToolError("session_id and javascript are required")
		}
		evalResult, err := s.browserMgr.Evaluate(args.SessionID, args.JavaScript)
		if err != nil {
			return mcpToolError(fmt.Sprintf("browser_evaluate: %v", err))
		}
		data, _ := json.Marshal(evalResult)
		return mcpToolResult(string(data))

	// --- Vibe Preview ---
	// Schemas live in mcp_tools.go (vibePreviewTools); these are the
	// dispatchers that turn a JSON-RPC tools/call into a manager call.

	case "vibe_preview_start":
		if s.vibePreviewMgr == nil {
			return mcpToolError("vibe-preview manager not initialised")
		}
		var args struct {
			Project   string `json:"project"`
			TargetURL string `json:"target_url"`
			Mode      string `json:"mode"`
			Profile   string `json:"profile"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		sess, err := s.vibePreviewMgr.Start(VibePreviewStartOpts{
			Project:   args.Project,
			TargetURL: args.TargetURL,
			Mode:      VibePreviewMode(args.Mode),
			Profile:   args.Profile,
		})
		if err != nil {
			return mcpToolError("vibe_preview_start: " + err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"session": sess})

	case "vibe_preview_stop":
		if s.vibePreviewMgr == nil {
			return mcpToolError("vibe-preview manager not initialised")
		}
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		if err := s.vibePreviewMgr.Stop(args.Project); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"ok": true})

	case "vibe_preview_status":
		if s.vibePreviewMgr == nil {
			return mcpToolJSON(map[string]interface{}{"sessions": []interface{}{}})
		}
		return mcpToolJSON(map[string]interface{}{"sessions": s.vibePreviewMgr.Status()})

	case "vibe_preview_snapshot":
		if s.vibePreviewMgr == nil {
			return mcpToolError("vibe-preview manager not initialised")
		}
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		rec, err := s.vibePreviewMgr.Snapshot(args.Project)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{
			"seq":  rec.Seq,
			"hash": rec.Hash,
			"size": len(rec.Bytes),
		})

	case "vibe_preview_clip_record":
		if s.vibePreviewMgr == nil {
			return mcpToolError("vibe-preview manager not initialised")
		}
		var args struct {
			Project        string `json:"project"`
			Source         string `json:"source"`
			DurationMaxSec int    `json:"duration_max_sec"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		rec, err := s.vibePreviewMgr.StartClip(VibeClipStartOpts{
			Project:        args.Project,
			Source:         VibeClipSource(args.Source),
			DurationMaxSec: args.DurationMaxSec,
		})
		if err != nil {
			return mcpToolError("vibe_preview_clip_record: " + err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"clip": rec})

	case "vibe_preview_clips":
		if s.vibePreviewMgr == nil {
			return mcpToolJSON(map[string]interface{}{"clips": []interface{}{}})
		}
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(map[string]interface{}{"clips": s.vibePreviewMgr.ListClips(args.Project)})

	case "vibe_preview_summaries":
		if s.vibePreviewMgr == nil {
			return mcpToolJSON(map[string]interface{}{"summaries": []interface{}{}})
		}
		var args struct {
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(map[string]interface{}{
			"summaries": s.vibePreviewMgr.ListSummaries(args.Project, args.Limit),
		})

	// --- Pipeline ---
	case "pipeline_run":
		var args struct {
			File   string `json:"file"`
			Job    string `json:"job"`
			DryRun bool   `json:"dry_run"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.pipelineRunner == nil {
			s.pipelineRunner = NewPipelineRunner()
		}
		result, err := s.pipelineRunner.Run(args.File, args.Job, args.DryRun)
		if err != nil {
			return mcpToolError(fmt.Sprintf("pipeline_run: %v", err))
		}
		return mcpToolJSON(result)
	case "pipeline_status":
		if s.pipelineRunner == nil {
			return mcpToolJSON(map[string]interface{}{"running": false})
		}
		return mcpToolJSON(s.pipelineRunner.Status())
	case "pipeline_list":
		var args struct {
			Dir string `json:"dir"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Dir == "" {
			args.Dir = s.taskMgr.workDir
		}
		if s.pipelineRunner == nil {
			s.pipelineRunner = NewPipelineRunner()
		}
		list, err := s.pipelineRunner.List(args.Dir)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(list)
	case "pipeline_stop":
		if s.pipelineRunner != nil {
			s.pipelineRunner.Stop()
		}
		return mcpToolResult("Pipeline cancelled.")
	case "pipeline_cancel_cloud":
		var args struct {
			Provider string `json:"provider"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.pipelineRunner == nil {
			s.pipelineRunner = NewPipelineRunner()
		}
		if err := s.pipelineRunner.CancelCloudCI(args.Provider); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Cloud CI cancelled (%s).", args.Provider))
	case "pipeline_hardware":
		if s.pipelineRunner == nil {
			s.pipelineRunner = NewPipelineRunner()
		}
		return mcpToolJSON(DetectHardware())

	// --- Analytics (self-hosted) ---
	case "analytics_start":
		var args struct {
			Engine string `json:"engine"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.analyticsMgr == nil {
			s.analyticsMgr = NewAnalyticsManager()
		}
		if err := s.analyticsMgr.Start(args.Engine); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Analytics started. Check analytics_status for URL.")
	case "analytics_stop":
		if s.analyticsMgr == nil {
			return mcpToolResult("Analytics not running.")
		}
		if err := s.analyticsMgr.Stop(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Analytics stopped.")
	case "analytics_status":
		if s.analyticsMgr == nil {
			s.analyticsMgr = NewAnalyticsManager()
		}
		st, err := s.analyticsMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(st)
	case "analytics_selfhost_events":
		var args struct {
			Event    string `json:"event"`
			PersonID string `json:"person_id"`
			Last     string `json:"last"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.analyticsMgr == nil {
			return mcpToolError("Analytics not started. Run analytics_start first.")
		}
		events, err := s.analyticsMgr.Events(args.Event, args.PersonID, args.Last)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(events)
	case "analytics_dashboard":
		if s.analyticsMgr == nil {
			return mcpToolError("Analytics not started.")
		}
		dash, err := s.analyticsMgr.Dashboard()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(dash)
	case "analytics_setup":
		var args struct {
			Framework string `json:"framework"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.analyticsMgr == nil {
			s.analyticsMgr = NewAnalyticsManager()
		}
		return mcpToolResult(s.analyticsMgr.Setup(args.Framework))

	// --- Auth dev server ---
	case "auth_dev_start":
		var args struct {
			Engine string `json:"engine"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.authDevMgr == nil {
			s.authDevMgr = NewAuthDevManager()
		}
		if err := s.authDevMgr.Start(args.Engine); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Auth server started. Check auth_dev_status for URL.")
	case "auth_dev_stop":
		if s.authDevMgr == nil {
			return mcpToolResult("Auth server not running.")
		}
		if err := s.authDevMgr.Stop(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Auth server stopped.")
	case "auth_dev_status":
		if s.authDevMgr == nil {
			s.authDevMgr = NewAuthDevManager()
		}
		st, err := s.authDevMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(st)
	case "auth_dev_users":
		var args struct {
			Action   string `json:"action"`
			Email    string `json:"email"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.authDevMgr == nil {
			return mcpToolError("Auth server not started.")
		}
		result, err := s.authDevMgr.Users(args.Action, args.Email, args.Password, args.Role)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)
	case "auth_dev_setup":
		var args struct {
			Framework string `json:"framework"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.authDevMgr == nil {
			s.authDevMgr = NewAuthDevManager()
		}
		return mcpToolResult(s.authDevMgr.Setup(args.Framework))
	case "auth_dev_tokens":
		var args struct {
			Action string `json:"action"`
			Email  string `json:"email"`
			Token  string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.authDevMgr == nil {
			s.authDevMgr = NewAuthDevManager()
		}
		result, err := s.authDevMgr.Tokens(args.Action, args.Email, args.Token)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	// --- Mail dev ---
	case "mail_dev_start":
		if s.mailDevMgr == nil {
			s.mailDevMgr = NewMailDevManager()
		}
		if err := s.mailDevMgr.Start(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Mail server started. SMTP: localhost:1025, Web UI: http://localhost:8025")
	case "mail_dev_stop":
		if s.mailDevMgr == nil {
			return mcpToolResult("Mail server not running.")
		}
		if err := s.mailDevMgr.Stop(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Mail server stopped.")
	case "mail_dev_status":
		if s.mailDevMgr == nil {
			s.mailDevMgr = NewMailDevManager()
		}
		st, err := s.mailDevMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(st)
	case "mail_dev_inbox":
		var args struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
			Limit   int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.mailDevMgr == nil {
			return mcpToolError("Mail server not started.")
		}
		msgs, err := s.mailDevMgr.Inbox(args.To, args.Subject, args.Limit)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(msgs)
	case "mail_dev_read":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.mailDevMgr == nil {
			return mcpToolError("Mail server not started.")
		}
		msg, err := s.mailDevMgr.Read(args.ID)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(msg)
	case "mail_dev_clear":
		if s.mailDevMgr == nil {
			return mcpToolError("Mail server not started.")
		}
		if err := s.mailDevMgr.Clear(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("All emails cleared.")
	case "mail_dev_config":
		if s.mailDevMgr == nil {
			s.mailDevMgr = NewMailDevManager()
		}
		return mcpToolJSON(s.mailDevMgr.Config())

	// --- Expose ---
	case "expose_start":
		var args struct {
			Port      int    `json:"port"`
			Subdomain string `json:"subdomain"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Port == 0 {
			return mcpToolError("port is required")
		}
		if s.exposeMgr == nil {
			s.exposeMgr = NewExposeManager()
		}
		tunnel, err := s.exposeMgr.Start(args.Port, args.Subdomain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(tunnel)
	case "expose_stop":
		var args struct {
			Port int `json:"port"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.exposeMgr == nil {
			return mcpToolResult("No active tunnels.")
		}
		if args.Port == 0 {
			s.exposeMgr.StopAll()
		} else {
			s.exposeMgr.Stop(args.Port)
		}
		return mcpToolResult("Tunnel stopped.")
	case "expose_list":
		if s.exposeMgr == nil {
			return mcpToolJSON([]interface{}{})
		}
		return mcpToolJSON(s.exposeMgr.List())

	// --- Stripe ---
	case "stripe_listen":
		var args struct {
			Port   int      `json:"port"`
			Path   string   `json:"path"`
			Events []string `json:"events"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.stripeDevMgr == nil {
			s.stripeDevMgr = NewStripeDevManager()
		}
		if err := s.stripeDevMgr.Listen(args.Port, args.Path, args.Events); err != nil {
			return mcpToolError(err.Error())
		}
		listenSt, listenErr := s.stripeDevMgr.Status()
		if listenErr != nil {
			return mcpToolResult("Stripe listener started.")
		}
		return mcpToolJSON(listenSt)
	case "stripe_stop":
		if s.stripeDevMgr == nil {
			return mcpToolResult("Stripe listener not running.")
		}
		if err := s.stripeDevMgr.Stop(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Stripe listener stopped.")
	case "stripe_trigger":
		var args struct {
			Event string `json:"event"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.stripeDevMgr == nil {
			s.stripeDevMgr = NewStripeDevManager()
		}
		out, err := s.stripeDevMgr.Trigger(args.Event)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(out)
	case "stripe_status":
		if s.stripeDevMgr == nil {
			s.stripeDevMgr = NewStripeDevManager()
		}
		st, err := s.stripeDevMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(st)

	// --- Uptime Monitor ---
	case "uptime_monitor_add":
		var args struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			IntervalSec    int    `json:"interval_sec"`
			ExpectedStatus int    `json:"expected_status"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.uptimeMonitor == nil {
			s.uptimeMonitor = NewUptimeMonitor()
			s.uptimeMonitor.Start()
		}
		if err := s.uptimeMonitor.Add(args.Name, args.URL, args.IntervalSec, args.ExpectedStatus); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Monitoring %s (%s) every %ds.", args.Name, args.URL, args.IntervalSec))
	case "uptime_monitor_remove":
		var args struct {
			Name string `json:"name"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.uptimeMonitor == nil {
			return mcpToolError("No monitors configured.")
		}
		if err := s.uptimeMonitor.Remove(args.Name); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Removed monitor %q.", args.Name))
	case "uptime_monitor_list":
		if s.uptimeMonitor == nil {
			return mcpToolJSON([]interface{}{})
		}
		return mcpToolJSON(s.uptimeMonitor.List())
	case "uptime_monitor_status":
		if s.uptimeMonitor == nil {
			return mcpToolJSON(map[string]interface{}{"totalMonitors": 0})
		}
		return mcpToolJSON(s.uptimeMonitor.Status())
	case "uptime_monitor_history":
		var args struct {
			Name  string `json:"name"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.uptimeMonitor == nil {
			return mcpToolError("No monitors configured.")
		}
		hist, err := s.uptimeMonitor.History(args.Name, args.Limit)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(hist)

	// --- Models ---
	case "models_list":
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		models, err := s.modelMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(models)
	case "models_pull":
		var args struct {
			Name string `json:"name"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		progress := make(chan string, 64)
		var pullErr error
		go func() {
			pullErr = s.modelMgr.Pull(args.Name, progress)
		}()
		var lastMsg string
		for msg := range progress {
			lastMsg = msg
		}
		if pullErr != nil {
			return mcpToolError(pullErr.Error())
		}
		return mcpToolResult(fmt.Sprintf("Pulled %s. %s", args.Name, lastMsg))
	case "models_remove":
		var args struct {
			Name string `json:"name"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		if err := s.modelMgr.Remove(args.Name); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Removed %s.", args.Name))
	case "models_run":
		var args struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			System string `json:"system"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		resp, err := s.modelMgr.Run(args.Model, args.Prompt, args.System)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(resp)
	case "models_serve":
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		if err := s.modelMgr.Serve(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Ollama server running.")
	case "models_ps":
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		running, err := s.modelMgr.PS()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(running)
	case "models_recommend":
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		recs, err := s.modelMgr.Recommend()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(recs)
	case "models_status":
		if s.modelMgr == nil {
			s.modelMgr = NewModelManager()
		}
		st, err := s.modelMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(st)

	// --- Lemon Squeezy ---
	case "lemonsqueezy_status":
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		st, err := s.lemonMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(st)
	case "lemonsqueezy_products":
		var args struct {
			Limit int `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		products, err := s.lemonMgr.Products(args.Limit)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(products)
	case "lemonsqueezy_orders":
		var args struct {
			Limit int    `json:"limit"`
			Email string `json:"email"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		orders, err := s.lemonMgr.Orders(args.Limit, args.Email)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(orders)
	case "lemonsqueezy_subscriptions":
		var args struct {
			Limit  int    `json:"limit"`
			Status string `json:"status"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		subs, err := s.lemonMgr.Subscriptions(args.Limit, args.Status)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(subs)
	case "lemonsqueezy_customers":
		var args struct {
			Limit int    `json:"limit"`
			Email string `json:"email"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		customers, err := s.lemonMgr.Customers(args.Limit, args.Email)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(customers)
	case "lemonsqueezy_revenue":
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		rev, err := s.lemonMgr.Revenue()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(rev)
	case "lemonsqueezy_discounts":
		var args struct {
			Limit int `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		discounts, err := s.lemonMgr.Discounts(args.Limit)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(discounts)
	case "lemonsqueezy_create_discount":
		var args struct {
			Name       string `json:"name"`
			Code       string `json:"code"`
			Amount     int    `json:"amount"`
			AmountType string `json:"amount_type"`
			ProductID  string `json:"product_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		disc, err := s.lemonMgr.CreateDiscount(args.Name, args.Code, args.Amount, args.AmountType, args.ProductID)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(disc)
	case "lemonsqueezy_webhook_listen":
		var args struct {
			Port int    `json:"port"`
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		if err := s.lemonMgr.WebhookListen(args.Port, args.Path); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Lemon Squeezy webhook listener started on port %d.", args.Port))
	case "lemonsqueezy_webhook_stop":
		if s.lemonMgr == nil {
			return mcpToolResult("Webhook listener not running.")
		}
		if err := s.lemonMgr.WebhookStop(); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Webhook listener stopped.")
	case "lemonsqueezy_setup":
		if s.lemonMgr == nil {
			s.lemonMgr = NewLemonSqueezyManager()
		}
		return mcpToolResult(s.lemonMgr.Setup())

	default:
		// Dev environment clone — orchestrates toolchain sync, repo clone, and coding-agent readiness.
		if handled, result := dispatchDevEnvironmentCloneMCP(s, call.Name, call.Arguments); handled {
			return result
		}
		// Phone-first mini backend (desktop/agent/phone_backend.go)
		if handled, result := dispatchPhoneMCP(s, call.Name, call.Arguments); handled {
			return result
		}
		// Companion compute (desktop/agent/companion.go)
		if handled, result := dispatchCompanionMCP(s, call.Name, call.Arguments); handled {
			return result
		}
		// Native build & deploy — iosNative / androidNative / flutter (mcp_native_build.go)
		if handled, result := dispatchNativeBuildMCP(s, call.Name, call.Arguments); handled {
			return result
		}
		// Monorepo detection — mcp_monorepo.go
		if handled, result := dispatchMonorepoMCP(s, call.Name, call.Arguments); handled {
			return result
		}
		// DNS / SSL provisioning — dns_mcp.go
		if handled, result := dispatchDnsMCP(s, call.Name, call.Arguments); handled {
			return result
		}
		// Try workspace tools (services, proxy, dns, storage, mock, check, perf, db, preview, oauth, cloud, migrate, remote, scale, backend, platform, domain, site, form, seo, cms, template)
		if result := s.handleWorkspaceMCPTool(call); result != nil {
			return result
		}
		return mcpToolError("unknown tool: " + call.Name)
	}
}

type mcpAgentGraphNodeArg struct {
	ID                       string   `json:"id"`
	Title                    string   `json:"title"`
	Kind                     string   `json:"kind"`
	Prompt                   string   `json:"prompt"`
	DependsOn                []string `json:"depends_on"`
	DependsOnCompat          []string `json:"dependsOn"`
	Runner                   string   `json:"runner"`
	Model                    string   `json:"model"`
	Engine                   string   `json:"engine"`
	WorkDir                  string   `json:"work_dir"`
	WorkDirCompat            string   `json:"workDir"`
	Project                  string   `json:"project"`
	Target                   string   `json:"target"`
	Load                     string   `json:"load"`
	Hours                    string   `json:"hours"`
	MaxIterations            int      `json:"max_iterations"`
	MaxIterationsCompat      int      `json:"maxIterations"`
	NoAutotest               bool     `json:"no_autotest"`
	NoAutotestCompat         bool     `json:"noAutotest"`
	PreferredDevice          string   `json:"preferred_device"`
	PreferredDeviceCompat    string   `json:"preferredDevice"`
	AllowedDevices           []string `json:"allowed_devices"`
	AllowedDevicesCompat     []string `json:"allowedDevices"`
	AllowedRunners           []string `json:"allowed_runners"`
	AllowedRunnersCompat     []string `json:"allowedRunners"`
	PriorDevice              string   `json:"prior_device"`
	PriorDeviceCompat        string   `json:"priorDevice"`
	PriorRunner              string   `json:"prior_runner"`
	PriorRunnerCompat        string   `json:"priorRunner"`
	StickyDevice             bool     `json:"sticky_device"`
	StickyDeviceCompat       bool     `json:"stickyDevice"`
	StickyRunner             bool     `json:"sticky_runner"`
	StickyRunnerCompat       bool     `json:"stickyRunner"`
	ResourceModes            []string `json:"resource_modes"`
	ResourceModesCompat      []string `json:"resourceModes"`
	PreferredVideoMode       string   `json:"preferred_video_mode"`
	PreferredVideoModeCompat string   `json:"preferredVideoMode"`
	Toughness                float64  `json:"toughness"`
	DesignPoints             float64  `json:"design_points"`
	DesignPointsCompat       float64  `json:"designPoints"`
	BuildPoints              float64  `json:"build_points"`
	BuildPointsCompat        float64  `json:"buildPoints"`
	VerifyPoints             float64  `json:"verify_points"`
	VerifyPointsCompat       float64  `json:"verifyPoints"`
}

func buildAgentGraphNodesFromMCP(args []mcpAgentGraphNodeArg) ([]AgentGraphNodeSpec, error) {
	if len(args) == 0 {
		return nil, nil
	}
	nodes := make([]AgentGraphNodeSpec, 0, len(args))
	for _, arg := range args {
		kind := AgentNodeKind(strings.ToLower(strings.TrimSpace(arg.Kind)))
		if kind == "" {
			kind = AgentNodeChat
		}
		switch kind {
		case AgentNodeChat, AgentNodeAutoIdeas:
		default:
			return nil, fmt.Errorf("invalid graph node kind %q", arg.Kind)
		}
		node := AgentGraphNodeSpec{
			ID:                 arg.ID,
			Title:              arg.Title,
			Kind:               kind,
			Prompt:             arg.Prompt,
			DependsOn:          firstNonEmptySlice(arg.DependsOn, arg.DependsOnCompat),
			Runner:             arg.Runner,
			Model:              arg.Model,
			Engine:             arg.Engine,
			WorkDir:            firstNonEmpty(arg.WorkDir, arg.WorkDirCompat),
			Project:            arg.Project,
			Target:             arg.Target,
			Load:               arg.Load,
			Hours:              arg.Hours,
			MaxIterations:      firstPositiveInt(arg.MaxIterations, arg.MaxIterationsCompat),
			NoAutotest:         arg.NoAutotest || arg.NoAutotestCompat,
			PreferredDevice:    firstNonEmpty(arg.PreferredDevice, arg.PreferredDeviceCompat),
			AllowedDevices:     firstNonEmptySlice(arg.AllowedDevices, arg.AllowedDevicesCompat),
			AllowedRunners:     firstNonEmptySlice(arg.AllowedRunners, arg.AllowedRunnersCompat),
			PriorDevice:        firstNonEmpty(arg.PriorDevice, arg.PriorDeviceCompat),
			PriorRunner:        firstNonEmpty(arg.PriorRunner, arg.PriorRunnerCompat),
			StickyDevice:       arg.StickyDevice || arg.StickyDeviceCompat,
			StickyRunner:       arg.StickyRunner || arg.StickyRunnerCompat,
			ResourceModes:      firstNonEmptySlice(arg.ResourceModes, arg.ResourceModesCompat),
			PreferredVideoMode: firstNonEmpty(arg.PreferredVideoMode, arg.PreferredVideoModeCompat),
			Toughness:          arg.Toughness,
			DesignPoints:       firstPositiveFloat(arg.DesignPoints, arg.DesignPointsCompat),
			BuildPoints:        firstPositiveFloat(arg.BuildPoints, arg.BuildPointsCompat),
			VerifyPoints:       firstPositiveFloat(arg.VerifyPoints, arg.VerifyPointsCompat),
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// mcpDoctor runs a doctor-like health check and returns results as text.
func (s *HTTPServer) mcpDoctor() interface{} {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Yaver Doctor (v%s)\n\n", version))

	ok, warn, fail := 0, 0, 0
	check := func(name, status, detail string) {
		icon := "✓"
		switch status {
		case "warn":
			icon = "!"
			warn++
		case "fail":
			icon = "✗"
			fail++
		default:
			ok++
		}
		sb.WriteString(fmt.Sprintf("  %-30s %s %s\n", name, icon, detail))
	}

	// Config
	sb.WriteString("── Configuration ──\n")
	cfg, err := LoadConfig()
	if err != nil {
		check("Config file", "fail", fmt.Sprintf("Error: %v", err))
	} else {
		p, _ := ConfigPath()
		check("Config file", "ok", p)
	}

	// Auth
	sb.WriteString("\n── Authentication ──\n")
	if cfg == nil || cfg.AuthToken == "" {
		check("Auth token", "fail", "Not signed in — run 'yaver auth'")
	} else {
		check("Auth token", "ok", "Present")
		if cfg.DeviceID != "" {
			check("Device ID", "ok", cfg.DeviceID[:8]+"...")
		} else {
			check("Device ID", "fail", "Not set — run 'yaver serve'")
		}
		if cfg.ConvexSiteURL != "" {
			check("Backend", "ok", cfg.ConvexSiteURL)
		} else {
			check("Backend", "fail", "Not configured")
		}
	}

	// Agent
	sb.WriteString("\n── Agent ──\n")
	agentPID, agentRunning := isAgentRunning()
	if agentRunning {
		check("Agent process", "ok", fmt.Sprintf("Running (PID %d)", agentPID))
	} else {
		check("Agent process", "warn", "Not running — start with 'yaver serve'")
	}

	if tmuxAvailable() {
		check("Tmux", "ok", "available")
	} else {
		check("Tmux", "warn", "not installed — session adoption requires tmux")
	}

	// Tasks
	status := s.taskMgr.GetAgentStatus()
	check("Tasks", "ok", fmt.Sprintf("%d running, %d total", status.RunningTasks, status.TotalTasks))

	// Runners
	sb.WriteString("\n── AI Runners ──\n")
	runners := []struct{ id, name, cmd string }{
		{"claude", "Claude Code", "claude"},
		{"codex", "OpenAI Codex", "codex"},
		{"opencode", "opencode", "opencode"},
	}
	for _, r := range runners {
		path, err := osexec.LookPath(r.cmd)
		if err != nil {
			check(r.name, "warn", "Not installed")
		} else {
			runnerCfg := GetRunnerConfig(r.id)
			out, verr := osexec.Command(r.cmd, "--version").CombinedOutput()
			ver := ""
			if verr == nil {
				ver = strings.TrimSpace(strings.Split(string(out), "\n")[0])
				if len(ver) > 60 {
					ver = ver[:60]
				}
			}
			level, detail := runnerDoctorDetail(runnerCfg, s.taskMgr.workDir, path, ver)
			check(r.name, level, detail)
		}
	}

	sb.WriteString("\n── Machine Onboarding ──\n")
	for _, provider := range collectMachineOnboardingStatus().Providers {
		check(provider.Name, machineOnboardingDoctorLevel(provider), machineOnboardingDoctorDetail(provider))
	}

	// Relay
	sb.WriteString("\n── Relay Servers ──\n")
	if cfg != nil && len(cfg.RelayServers) > 0 {
		client := &http.Client{Timeout: 5 * time.Second}
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			start := time.Now()
			resp, err := client.Get(rs.HttpURL + "/health")
			rtt := time.Since(start)
			if err != nil {
				check("Relay: "+label, "fail", "Unreachable")
			} else {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					check("Relay: "+label, "ok", fmt.Sprintf("OK (%dms)", rtt.Milliseconds()))
				} else {
					check("Relay: "+label, "fail", fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
		}
	} else {
		check("Relay servers", "warn", "None configured")
	}

	// Tunnels
	if cfg != nil && len(cfg.CloudflareTunnels) > 0 {
		sb.WriteString("\n── Cloudflare Tunnels ──\n")
		for _, t := range cfg.CloudflareTunnels {
			label := t.Label
			if label == "" {
				label = t.ID
			}
			cf := ""
			if t.CFAccessClientId != "" {
				cf = " (CF Access)"
			}
			check("Tunnel: "+label, "ok", t.URL+cf)
		}
	}

	// Network
	sb.WriteString("\n── Network ──\n")
	ip := getLocalIP()
	if ip != "" && ip != "0.0.0.0" {
		check("Local IP", "ok", ip)
	} else {
		check("Local IP", "warn", "Could not determine")
	}

	sb.WriteString(fmt.Sprintf("\nSummary: %d passed, %d warnings, %d failures\n", ok, warn, fail))
	return mcpToolResult(sb.String())
}

// mcpStatus returns auth/agent/relay status information.
func (s *HTTPServer) mcpStatus() interface{} {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Yaver v%s\n\n", version))

	cfg, err := LoadConfig()
	if err != nil {
		return mcpToolError(fmt.Sprintf("load config: %v", err))
	}

	// Agent
	agentPID, running := isAgentRunning()
	if running {
		sb.WriteString(fmt.Sprintf("Agent: running (PID %d)\n", agentPID))
	} else {
		sb.WriteString("Agent: stopped\n")
	}

	// Auth
	if cfg.AuthToken != "" {
		sb.WriteString("Auth: signed in\n")
		if cfg.DeviceID != "" {
			sb.WriteString(fmt.Sprintf("Device: %s\n", cfg.DeviceID[:8]+"..."))
		}
	} else {
		sb.WriteString("Auth: not signed in\n")
	}

	// Runner
	s.taskMgr.mu.RLock()
	runner := s.taskMgr.runner
	s.taskMgr.mu.RUnlock()
	sb.WriteString(fmt.Sprintf("Runner: %s (%s)\n", runner.Name, runner.RunnerID))

	// Work dir
	sb.WriteString(fmt.Sprintf("Work dir: %s\n", s.taskMgr.workDir))

	// Relay
	if len(cfg.RelayServers) > 0 {
		sb.WriteString(fmt.Sprintf("\nRelay servers: %d configured\n", len(cfg.RelayServers)))
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			pw := "no password"
			if rs.Password != "" || cfg.RelayPassword != "" {
				pw = "password set"
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s (%s)\n", label, rs.HttpURL, pw))
		}
	} else {
		sb.WriteString("\nRelay servers: none configured\n")
	}

	// Tunnels
	if len(cfg.CloudflareTunnels) > 0 {
		sb.WriteString(fmt.Sprintf("\nCloudflare Tunnels: %d configured\n", len(cfg.CloudflareTunnels)))
		for _, t := range cfg.CloudflareTunnels {
			label := t.Label
			if label == "" {
				label = t.ID
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", label, t.URL))
		}
	}

	// Tasks
	status := s.taskMgr.GetAgentStatus()
	sb.WriteString(fmt.Sprintf("\nTasks: %d running, %d total\n", status.RunningTasks, status.TotalTasks))

	return mcpToolResult(sb.String())
}

// yaverHelpText returns help documentation for the given topic.
// mcpProjectNewQuick is the one-shot "skip the wizard" path:
// take a name + slug + description + flags and materialise a
// monorepo scaffold in a single MCP call. Under the hood it
// starts a wizard session, prefills every answer, and calls
// GenerateProject. Keeps the composition surface simple so a
// remote AI agent doesn't need to round-trip 20 questions.
func (s *HTTPServer) mcpProjectNewQuick(raw json.RawMessage) interface{} {
	var args struct {
		Name           string `json:"name"`
		Slug           string `json:"slug"`
		Description    string `json:"description"`
		Tagline        string `json:"tagline"`
		AppTemplate    string `json:"appTemplate"`
		AudienceType   string `json:"audienceType"`
		Problem        string `json:"problem"`
		UniqueAngle    string `json:"uniqueAngle"`
		Monetization   string `json:"monetization"`
		LaunchTimeline string `json:"launchTimeline"`
		Domain         string `json:"domain"`
		PrimaryColor   string `json:"primaryColor"`
		SecondaryColor string `json:"secondaryColor"`
		AccentColor    string `json:"accentColor"`
		SurfaceColor   string `json:"surfaceColor"`
		IncludeWeb     *bool  `json:"includeWeb"`
		IncludeLanding *bool  `json:"includeLanding"`
		IncludeMobile  *bool  `json:"includeMobile"`
		IncludeBackend *bool  `json:"includeBackend"`
		WebHost        string `json:"webHost"`
		Backend        string `json:"backend"`
		OauthApple     *bool  `json:"oauthApple"`
		OauthGoogle    *bool  `json:"oauthGoogle"`
		OauthMicrosoft *bool  `json:"oauthMicrosoft"`
		IosBundleID    string `json:"iosBundleId"`
		AndroidPackage string `json:"androidPackage"`
		GitProvider    string `json:"gitProvider"`
		GitVisibility  string `json:"gitVisibility"`
		GitOrg         string `json:"gitOrg"`
		ParentDir      string `json:"parentDir"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	if args.Name == "" || args.Slug == "" || args.Description == "" {
		return mcpToolError("name, slug, and description are required")
	}

	sess, _ := StartWizard()
	pref := func(k, v string) {
		if v != "" {
			_, _ = AnswerWizard(sess.ID, k, v)
		}
	}
	boolStr := func(p *bool, dflt bool) string {
		v := dflt
		if p != nil {
			v = *p
		}
		if v {
			return "true"
		}
		return "false"
	}

	pref("app_name", args.Name)
	pref("slug", args.Slug)
	pref("description", args.Description)
	pref("tagline", args.Tagline)
	pref("app_template", firstNonEmpty(args.AppTemplate, "saas-dashboard"))
	pref("audience_type", firstNonEmpty(args.AudienceType, "consumers"))
	pref("problem_statement", firstNonEmpty(args.Problem, args.Description))
	pref("unique_angle", args.UniqueAngle)
	pref("monetization", firstNonEmpty(args.Monetization, "free"))
	pref("launch_timeline", firstNonEmpty(args.LaunchTimeline, "1-2-weeks"))
	pref("success_metric", "daily-active-users")
	pref("distribution_channel", "word-of-mouth")
	pref("supported_languages", "English")
	pref("domain", args.Domain)
	pref("primary_color", firstNonEmpty(args.PrimaryColor, "#4F46E5"))
	pref("secondary_color", firstNonEmpty(args.SecondaryColor, "#0EA5E9"))
	pref("accent_color", firstNonEmpty(args.AccentColor, "#F59E0B"))
	pref("surface_color", firstNonEmpty(args.SurfaceColor, "#111827"))
	pref("tone", "system")
	pref("include_web", boolStr(args.IncludeWeb, true))
	pref("include_landing", boolStr(args.IncludeLanding, true))
	pref("include_mobile", boolStr(args.IncludeMobile, true))
	pref("include_backend", boolStr(args.IncludeBackend, true))
	pref("web_framework", "nextjs")
	pref("web_host", firstNonEmpty(args.WebHost, "cloudflare"))
	pref("backend", firstNonEmpty(args.Backend, "convex"))
	pref("mobile_stack", "expo-rn")
	pref("mobile_nav_style", "bottom-tabs")
	pref("mobile_nav_count", "4")
	pref("design_source", "prompt-only")
	pref("oauth_apple", boolStr(args.OauthApple, true))
	pref("oauth_google", boolStr(args.OauthGoogle, true))
	pref("oauth_microsoft", boolStr(args.OauthMicrosoft, false))
	pref("oauth_email", "true")
	pref("payments", "stripe")
	pref("ios_bundle_id", firstNonEmpty(args.IosBundleID, defaultAppIdentifier(args.Slug)))
	pref("android_package", firstNonEmpty(args.AndroidPackage, defaultAppIdentifier(args.Slug)))
	pref("apple_team_id", "")
	pref("play_service_account", "")
	pref("cloudflare_zone", "")
	pref("git_provider", firstNonEmpty(args.GitProvider, "none"))
	pref("git_visibility", firstNonEmpty(args.GitVisibility, "private"))
	pref("git_org", args.GitOrg)
	pref("git_repo_name", args.Slug)
	pref("confirm", "true")
	finishWizardWithDefaults(sess)

	res, err := GenerateProject(sess.ID, args.ParentDir)
	if err != nil {
		return mcpToolError(err.Error())
	}
	res.NextSteps = append([]string{
		"Self-hosted first: `cd " + res.Directory + " && npm install && ./scripts/dev.sh`.",
		"Web UI: `apps/web` is a Next.js Cloudflare app; mobile: `apps/mobile` is Expo React Native with iOS + Android identifiers; backend: `backend/convex` is local Convex and cloud-deployable.",
		"From Claude Code or Codex, keep working through MCP: run `mobile_project_prepare` on `apps/mobile`, then `mobile_project_build` for Yaver phone testing.",
		"Managed cloud is the upgrade path, not the first requirement: call `yaver_managed_cloud_onboarding` only when the user wants an hourly Yaver cloud machine.",
	}, res.NextSteps...)
	res.YaverOnboarding = map[string]interface{}{
		"firstCapture": "self-hosted",
		"stack": map[string]interface{}{
			"repo":    "monorepo",
			"web":     "apps/web Next.js on Cloudflare",
			"landing": "apps/landing static Cloudflare Pages",
			"mobile":  "apps/mobile Expo React Native for iOS and Android",
			"backend": "backend/convex local dev and hosted Convex deploy",
			"shared":  "packages/shared TypeScript",
		},
		"mcpLoop": []string{
			"project_self_host_create creates the repo.",
			"mobile_project_prepare installs mobile dependencies.",
			"mobile_project_build builds the Hermes bundle for the Yaver phone app.",
			"deploy_run can ship Cloudflare, Convex, TestFlight, or Play later.",
		},
		"managedCloudUpsell": map[string]interface{}{
			"when":                        "Use after the self-hosted repo exists and the user wants an always-on hourly machine for vibing.",
			"tool":                        "yaver_managed_cloud_onboarding",
			"requiresExplicitCostConsent": true,
		},
	}
	return mcpToolJSON(res)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultAppIdentifier(slug string) string {
	segment := strings.ToLower(strings.TrimSpace(slug))
	var b strings.Builder
	for _, r := range segment {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	segment = b.String()
	if segment == "" {
		segment = "app"
	}
	if segment[0] >= '0' && segment[0] <= '9' {
		segment = "app" + segment
	}
	return "com.myco." + segment
}

func firstNonEmptySlice[T any](vals ...[]T) []T {
	for _, v := range vals {
		if len(v) > 0 {
			return append([]T{}, v...)
		}
	}
	return nil
}

// yaverOnboardChecklist inspects the current agent state and
// returns an ordered list of "still to do" items for a fresh
// install. The idea: an AI agent (Claude Desktop, Codex, etc.)
// can call yaver_onboard at the start of a session and then
// walk the user through the gaps with specific CLI commands.
func yaverOnboardChecklist() string {
	var b strings.Builder
	b.WriteString("Yaver onboarding checklist\n")
	b.WriteString("==========================\n\n")

	cfg, _ := LoadConfig()
	mark := func(done bool, label, hint string) {
		if done {
			b.WriteString("  [x] " + label + "\n")
		} else {
			b.WriteString("  [ ] " + label + "\n")
			if hint != "" {
				b.WriteString("       → " + hint + "\n")
			}
		}
	}

	// 1. Auth
	authed := cfg != nil && cfg.AuthToken != ""
	mark(authed, "Sign in", "yaver auth   (opens browser)")

	// 2. Device registered
	deviceOK := cfg != nil && cfg.DeviceID != ""
	mark(deviceOK, "Device registered", "yaver serve   (also starts the HTTP + QUIC servers)")

	// 3. Bootstrap secret (for remote /auth/recover)
	bootOK := cfg != nil && cfg.BootstrapSecretHash != ""
	mark(bootOK, "Bootstrap recovery secret set", "yaver config bootstrap-secret <passphrase>  (store in password manager)")

	// 4. Public transport
	transport := "none"
	if cfg != nil {
		switch {
		case len(cfg.CloudflareTunnels) > 0:
			transport = "cloudflare-tunnel"
		case len(cfg.RelayServers) > 0:
			transport = "relay"
		}
	}
	mark(transport != "none", "Public reachable transport ("+transport+")", "yaver tunnel cloudflare wizard   or   yaver relay add <url>")

	// 5. Email — for notifications, forms, newsletter
	emailOK := cfg != nil && cfg.Email != nil && (cfg.Email.SMTPHost != "" || cfg.Email.GoogleRefreshToken != "")
	mark(emailOK, "Email provider wired (needed for forms/newsletter/mail)", "yaver email setup   (or use mail_onboard_start from mobile)")

	// 6. Runner installed — yaver's three first-class runners.
	runnerFound := ""
	for _, r := range []string{"claude", "codex", "opencode"} {
		if _, err := osexecLookPath(r); err == nil {
			runnerFound = r
			break
		}
	}
	mark(runnerFound != "", "AI runner installed ("+runnerFound+")", "npm i -g @anthropic-ai/claude-code   or `yaver install codex` / `yaver install opencode`")

	// 7. Auto-start
	autoStartReady := false
	autoStartRemedy := "yaver config set auto-start true"
	autoStartLabel := "Agent auto-starts on boot"
	switch runtime.GOOS {
	case "darwin":
		autoStartReady = isDarwinLaunchDaemonInstalled()
		if !autoStartReady {
			autoStartLabel = "Agent auto-starts before login (macOS headless)"
			autoStartRemedy = "sudo yaver serve --install-launchd-daemon"
		}
	case "linux":
		if isWSL() {
			autoStartReady = isWSLWindowsScheduledTaskInstalled()
			autoStartLabel = "Agent restarts after Windows login (WSL)"
			autoStartRemedy = "yaver serve --install-systemd   or install Windows Task Scheduler wrapper"
		} else {
			autoStartReady = isAutoStartInstalled()
		}
	default:
		autoStartReady = cfg != nil && cfg.AutoStart
	}
	mark(autoStartReady, autoStartLabel, autoStartRemedy)

	// 8. Auto-update
	autoUpdate := cfg != nil && cfg.AutoUpdate
	mark(autoUpdate, "Auto-update enabled", "yaver config set auto-update true")

	b.WriteString("\nNext suggested action: ")
	switch {
	case !authed:
		b.WriteString("run `yaver auth`")
	case !deviceOK:
		b.WriteString("run `yaver serve` in one terminal")
	case !bootOK:
		b.WriteString("pick a passphrase and run `yaver config bootstrap-secret <passphrase>`")
	case transport == "none":
		b.WriteString("wire a public transport with `yaver tunnel cloudflare wizard`")
	case !emailOK:
		b.WriteString("connect Gmail/O365 (POST /mail/onboard/start from the mobile app, or `yaver email setup`)")
	case runnerFound == "":
		b.WriteString("install an AI runner — claude-code / codex / opencode")
	default:
		b.WriteString("you're set up — call `yaver_help` with topic=solo-stack to see what's possible")
	}
	b.WriteString("\n")
	return b.String()
}

func yaverHelpText(topic string) string {
	switch strings.ToLower(topic) {
	case "tmux":
		return `Tmux Session Adoption
═══════════════════

Yaver can discover and adopt existing tmux sessions, making them visible and
controllable from the mobile app. This is useful when you start an AI agent
(Claude Code, Codex, opencode) in tmux and want to monitor/interact with
it from your phone.

How it works:
1. Start a tmux session: tmux new -s my-agent
2. Run an AI agent inside it (e.g., claude, codex, opencode)
3. Yaver detects it: yaver tmux list (or tmux_list_sessions MCP tool)
4. Adopt it: yaver tmux adopt my-agent (or tmux_adopt_session MCP tool)
5. The session now appears as a task in the mobile app
6. You can send input from mobile — it goes to tmux via send-keys
7. Output is polled every 500ms and streamed to mobile

MCP Tools:
- tmux_list_sessions: List all sessions with agent detection
- tmux_adopt_session: Adopt a session as a Yaver task
- tmux_detach_session: Stop monitoring (session keeps running)
- tmux_send_input: Send keyboard input to an adopted session

Agent detection: Yaver inspects the process tree in each pane to identify
yaver's three first-class runners (claude, codex, opencode).`

	case "relay":
		return `Relay Servers
═════════════

Relay servers enable NAT traversal — your mobile can reach your dev machine
even when it's behind a firewall or on a different network.

How it works:
- Desktop agent connects outbound to relay via QUIC tunnel on startup
- Mobile makes short-lived HTTP requests to relay
- Relay is pass-through — no data stored
- Password-protected for security

Setup:
  yaver relay add https://relay.example.com --password secret --label "My Relay"
  yaver relay test   # Test connectivity
  yaver relay list   # View configured relays

MCP Tools: get_relay_config, add_relay_server, remove_relay_server, relay_test,
relay_set_password, relay_clear_password

Self-hosting: cd relay && RELAY_PASSWORD=secret docker compose up -d`

	case "tunnel":
		return `Cloudflare Tunnels
══════════════════

Cloudflare Tunnel creates a secure HTTPS path from Cloudflare's edge to your
machine. No port forwarding, works through any firewall.

Setup:
  1. Install cloudflared: brew install cloudflared
  2. Create a tunnel: cloudflared tunnel create yaver
  3. Route traffic: cloudflared tunnel route dns yaver yaver.example.com
  4. Run tunnel: cloudflared tunnel --url http://localhost:18080 run yaver
  5. Add to Yaver: yaver tunnel add https://yaver.example.com

MCP Tools: tunnel_list, tunnel_add, tunnel_remove, tunnel_test

For CF Access (zero-trust):
  yaver tunnel add https://yaver.example.com --cf-client-id ID --cf-client-secret SECRET`

	case "mobile":
		return `Mobile App
══════════

The Yaver mobile app (iOS/Android) lets you control AI coding agents from your phone.

Features:
- Create tasks: send prompts to Claude Code, Codex, Aider, etc.
- Live streaming: see agent output in real-time
- Follow-up: send additional instructions to running tasks
- Tmux adoption: discover and control pre-existing tmux sessions
- Multi-device: connect to any registered machine
- Connection modes: LAN (direct), relay (NAT traversal), Cloudflare tunnel

Connection priority:
  1. LAN beacon (UDP broadcast, ~5ms) — same WiFi
  2. Convex IP (direct HTTP, ~5ms) — known IP
  3. QUIC relay (proxied, ~50ms) — roaming/NAT
  4. Cloudflare tunnel — zero-trust

Network changes (WiFi ↔ cellular) are handled silently.`

	case "mcp":
		return `MCP (Model Context Protocol)
════════════════════════════

Yaver exposes an MCP server so AI agents can interact with it programmatically.

Start MCP server:
  yaver mcp              # stdio mode (for Claude Code, etc.)
  yaver mcp --http 8080  # HTTP mode (for remote tools)

Available tool categories:
- Tasks: create_task, list_tasks, get_task, stop_task, continue_task
- Runners: list_runners, switch_runner
- System: get_info, get_system_info, get_config, set_work_dir, list_projects
- Publish: publish_config_get, publish_run, publish_submit, publish_upload, publish_ci_dispatch, publish_list, publish_status
- Files: read_file, write_file, list_directory, search_files
- Relay: get_relay_config, add_relay_server, remove_relay_server, relay_test
- Tunnels: tunnel_list, tunnel_add, tunnel_remove, tunnel_test
- Tmux: tmux_list_sessions, tmux_adopt_session, tmux_detach_session, tmux_send_input
- Email: email_list_inbox, email_get, email_send, email_sync, email_search
- ACL: acl_list_peers, acl_add_peer, acl_remove_peer, acl_call_peer_tool
- Diagnostics: yaver_doctor, yaver_status, yaver_devices, yaver_logs, yaver_ping
- Config: config_set, relay_set_password, relay_clear_password

Use yaver_help with a topic for details on any category.`

	case "runners":
		return `AI Runners
══════════

Yaver supports multiple AI coding agents. You can switch between them per-task
or set a default.

First-class runners (the only runners Yaver supports natively):
- claude: Claude Code (default) — npm i -g @anthropic-ai/claude-code
- codex: OpenAI Codex — npm i -g @openai/codex
- opencode: opencode (BYOK any provider — Anthropic / OpenAI / OpenRouter / Ollama / GLM / ZAI / …) — curl -fsSL https://opencode.ai/install | bash

Custom runners:
  yaver set-runner custom "my-tool --auto {prompt}"

MCP Tools: list_runners, switch_runner

The runner is also selectable per-task from the mobile app.`

	case "tasks":
		return `Task Management
═══════════════

Tasks are the core abstraction — each task is an AI agent session.

Lifecycle: queued → running → completed/failed/stopped

From mobile: tap + to create, tap task to view, input bar for follow-ups
From MCP: create_task, list_tasks, get_task, stop_task, continue_task
From CLI: yaver attach (interactive REPL)

Adopted tmux sessions also appear as tasks with source="tmux-adopted".
They support input via tmux send-keys and output via pane polling.

Tasks are persisted to ~/.yaver/tasks.json and survive agent restarts.
Adopted tasks are automatically re-adopted if the tmux session still exists.`

	case "auth":
		return `Authentication
══════════════

Yaver uses OAuth via the web app for authentication.

  yaver auth          # Opens browser for sign-in (Apple/GitHub/Google/Microsoft)
  yaver auth --headless  # Device code flow for SSH/headless servers
  yaver signout       # Clear credentials
  yaver status        # Check auth status

The auth flow:
1. CLI opens https://yaver.io/auth?client=desktop
2. User signs in via Apple/GitHub/Google/Microsoft
3. Web redirects to http://127.0.0.1:19836/callback?token=<token>
4. CLI saves token to ~/.config/yaver/config.json

The token is used for all API calls and is refreshed automatically.`

	// --- Self-hosted replacements (zero monthly cost) ---

	case "forms":
		return `Forms (replaces Formspree / Basin / Getform ~$29/mo)
════════════════════════════════════════════════════

Self-hosted HTML form ingestion with honeypot, rate limiting, and
SMTP notification. Runs entirely on the dev's own machine.

Endpoints:
  POST /forms                        owner — create form
  GET  /forms                        owner — list forms
  POST /forms/:id/submit             public — honeypot + rate-limited
  GET  /forms/:id/submissions        owner — tail submissions
  DELETE /forms?id=:id               owner — delete

Create a form:
  curl -X POST $AGENT/forms -H "Authorization: Bearer $TOKEN" \
    -d '{"name":"Contact","notifyEmail":"me@example.com","honeypotField":"website","rateLimitPerHour":60}'

Point your landing page <form action="..."> at /forms/:id/submit.

MCP tools: form_create, form_list, form_submissions, form_delete`

	case "newsletter":
		return `Newsletter (replaces ConvertKit / Mailchimp / Buttondown ~$49/mo)
════════════════════════════════════════════════════════════════

HMAC-tokened double opt-in with broadcast via the existing SMTP relay.
Plus compose-from-git: the agent walks git log + gh/glab to draft the
weekly recap for you.

Public:
  POST /newsletter/subscribe                email signup
  GET  /newsletter/confirm?token=...        confirm subscription
  GET  /newsletter/unsubscribe?token=...    one-click unsub

Owner:
  GET  /newsletter/subscribers              list + counts
  GET  /newsletter/campaigns                list drafts + sent
  POST /newsletter/campaigns                create draft
  POST /newsletter/campaigns/:id/send       broadcast
  POST /newsletter/compose                  compose-from-git
                                            { repo, sinceDays, includePrs,
                                              includeIssues, saveDraft,
                                              execute, runner }

MCP tools: newsletter_subscribers, newsletter_create, newsletter_send,
newsletter_compose_from_git`

	case "jobs", "queue":
		return `Job queue (replaces Inngest / Trigger.dev / BullMQ ~$0-500/mo)
══════════════════════════════════════════════════════════════

File-backed persistent queue with retry/backoff/DLQ. Built-in
handlers for newsletter.send, form.notify, pdf.render. Register
your own with RegisterJobHandler() for custom side-effects.

  POST /jobs/enqueue  { handler, payload, delaySec?, maxAttempts?, backoffSec? }
  GET  /jobs          list queue + dlq
  POST /jobs/:id/retry
  POST /jobs/:id/cancel

MCP tools: jobs_list, jobs_enqueue, jobs_retry, jobs_cancel`

	case "image", "img":
		return `Image optimizer (replaces Cloudinary / Imgix ~$99+/mo)
══════════════════════════════════════════════════════

Resize + reencode on-demand. Pure-Go, no CGo, disk-cached.

  GET /img?src=<path>&root=<id>&w=&h=&fmt=&q=

Serve via the agent's public tunnel and link directly from your
landing page:
  <img src="https://yaver.me.com/img?src=hero.png&w=1200&fmt=webp&q=75">

MCP tools: img_optimize, img_cache_clear`

	case "pdf":
		return `PDF generation (replaces DocRaptor ~$15-300/mo)
═══════════════════════════════════════════════

HTML → PDF via the embedded Chromium (same one the test SDK uses).

  POST /pdf/render
    { "html": "<h1>Invoice</h1>", "format": "A4", "landscape": false,
      "printBackground": true, "marginTop": "1cm" }
  → application/pdf

You can also pass a URL instead of html:
  { "url": "https://yaver.me.com/invoices/123" }

MCP tools: pdf_render`

	case "oauth":
		return `OAuth provider (replaces Dex / Authelia / Keycloak-lite)
════════════════════════════════════════════════════════

Self-hosted OIDC so projects generated by yaver new can point their
auth at your own agent instead of Convex / Google / etc.

Public:
  GET  /oauth/.well-known/openid-configuration
  GET  /oauth/authorize                     — login form
  POST /oauth/login                         — email+password
  POST /oauth/token                         — code → JWT
  GET  /oauth/userinfo
  GET  /oauth/jwks                          — RS256 public key

Owner:
  POST /oauth/clients                       — register a client
                                              (secret shown ONCE)
  POST /oauth/users                         — create a user

Passwords are scrypt-hashed (N=32768, r=8, p=1) — brute-force
painful, single-user logins stay fast.

MCP tools: oauth_client_list, oauth_client_create, oauth_user_list,
oauth_user_create`

	case "mail":
		return `Mail (replaces ConvertKit inbox / Superhuman ~$30/mo)
═════════════════════════════════════════════════════

Gmail + Microsoft Graph (O365) triage + AI-boosted replies. All via
the dev's own OAuth tokens — nothing touches Convex.

  GET  /mail/inbox?provider=&limit=&onlyPersonal=true
  POST /mail/draft        { id, instructions, execute: true }
                          execute=true pipes the prompt into the
                          configured runner (Claude/Codex/Ollama)
                          and returns the draft text inline.
  POST /mail/send
  POST /mail/onboard/start { provider: "gmail" | "o365" }
  GET  /mail/onboard/callback / /status

Classifier beats Gmail Promotions: thread replies, List-Unsubscribe,
Precedence=bulk, Auto-Submitted, marketing keywords, sender domain
history → personal / transactional / marketing / bulk buckets.

CLI: yaver mail inbox | draft | send | connect
MCP tools: email_list_inbox, email_get, email_send, email_search,
mail_draft, mail_classify`

	case "shortener", "short":
		return `URL shortener (replaces Bitly / Rebrandly / Dub.co ~$29/mo)
═══════════════════════════════════════════════════════════

  POST /shortener { url, code?, label? }   owner — create
  GET  /s/:code                            public — 302 + click log
  GET  /s/:code/json                       public — JSON API
  GET  /shortener                          owner — list + counts
  GET  /shortener/clicks?code=             owner — last 500 clicks

Click rows are append-only JSONL — cheap to tail, rotatable by mv.

MCP tools: short_create, short_list, short_clicks, short_delete`

	case "waitlist":
		return `Waitlist (replaces Prefinery / Earlybird / Viral Loops ~$49/mo)
═══════════════════════════════════════════════════════════════

Public signup with referral codes + leaderboard.

  POST /waitlist/join { email, ref, name, source }
                       → { slot, code, shareUrl }
  GET  /waitlist/leaderboard                (redacted — no emails)
  GET  /waitlist                            owner — full list
  DELETE /waitlist?email=

Referral credit auto-increments when join includes ?ref=CODE.
Broadcast via the newsletter tool.

MCP tools: waitlist_list, waitlist_delete, waitlist_leaderboard`

	case "docs":
		return `Docs site (replaces Mintlify / Gitbook / Readme.com ~$20+/mo)
═════════════════════════════════════════════════════════════

Serve a markdown folder as a static docs site with sidebar + search.

  POST /docs/config { path, title, theme, logoUrl }
  GET  /docs                        — index
  GET  /docs/<slug>                 — page
  GET  /docs/_search?q=...          — substring search
  GET  /docs/_json                  — sidebar tree

Write markdown in your repo, point /docs/config at the folder, done.
Zero asset deps (inline CSS, pure-Go renderer).

MCP tools: docs_config, docs_list, docs_search`

	case "meetings", "meet", "calendar":
		return `Meetings (replaces Calendly / Cal.com / SavvyCal ~$12-24/mo)
════════════════════════════════════════════════════════════

Public booking page with Google Calendar + Microsoft Teams integration.
Reuses the existing Gmail OAuth + Azure tenant credentials — the dev
authorises once and gets real Meet/Teams links auto-generated.

  POST /meetings                owner — define event type
  GET  /meet/:slug              public — HTML booking page
  POST /meet/:slug              public — book a slot
  GET  /bookings                owner — list confirmed

Event type:
  { slug, title, durationMin, provider: "google"|"o365",
    hosting: "meet"|"teams"|"none",
    availability: [{ weekday, startTime, endTime, timezone }],
    bufferMin, daysAhead }

MCP tools: meeting_create, meeting_list, meeting_bookings`

	case "wizard", "new-project", "project":
		return `Project wizard (replaces create-react-app / turbo gen ~free+your time)
═════════════════════════════════════════════════════════════════════

Monorepo scaffold: Convex + Next.js on Cloudflare + Expo RN + native
builds (xcodebuild + gradle, no EAS). Auto git init + gh/glab repo
create + initial push.

  yaver new                         interactive wizard
  POST /project/wizard/start        start a session
  POST /project/wizard/answer       submit an answer
  POST /project/wizard/generate     materialise the scaffold

Layout: apps/{web,landing,mobile}/, packages/shared/, backend/convex/

Fields: app_name, description, slug, domain, colors, include_web /
mobile / backend / landing (all opt-in), git_provider + visibility.

MCP tools: project_wizard_start, project_wizard_answer,
project_wizard_generate`

	case "solo-stack", "stack", "costs", "savings":
		return `Solo dev stack — every feature replaces a paid SaaS
════════════════════════════════════════════════════

Your Mac mini + Claude Code / Codex / Ollama subscription replace:

  Sentry / Datadog errors       → error_list, apm, blackbox  ($50-300/mo)
  LaunchDarkly flags            → flag_*                      ($20-500/mo)
  ConvertKit newsletter         → newsletter_*                ($29-79/mo)
  Formspree forms               → form_*                      ($24/mo)
  Cloudinary images             → img_optimize                ($99+/mo)
  DocRaptor PDF                 → pdf_render                  ($15-300/mo)
  Calendly meetings             → meeting_*                   ($12/mo)
  Bitly short URLs              → short_*                     ($29/mo)
  Prefinery waitlist            → waitlist_*                  ($49/mo)
  Mintlify docs                 → docs_*                      ($20+/mo)
  Auth0 / Clerk                 → oauth_*                     ($25+/mo)
  Algolia search                → search                      ($100+/mo)
  Better Uptime / Healthchecks  → monitor_*                   ($18/mo)
  Dex / Authelia                → oauth provider              (free)
  Inngest / Trigger.dev         → jobs_*                      ($20-500/mo)
  Prefinery affiliate tracking  → waitlist referrals          ($49/mo)
  Statuspage.io                 → statuspage                  ($29/mo)
  PagerDuty-lite                → notify + monitors           ($21/mo)
  Papertrail logs               → logs                        ($7/mo)
  Vault / Doppler               → vault                       ($25/mo)
  Cron-job.org                  → schedule_task               ($2/mo)
  ngrok / bore                  → tunnels + relay             ($8/mo)
  Expo EAS Build                → local xcodebuild + gradle   ($29/mo)
  BackBlaze / Tarsnap           → backup + encrypted          ($5/mo)

Running total replaced: roughly $600-2500/mo → $0 on your Mac mini.

Use yaver_help with any of these topics for setup:
  forms, newsletter, jobs, image, pdf, oauth, mail, shortener,
  waitlist, docs, meetings, wizard`

	default:
		return `Yaver — your Mac mini runs every SaaS you were paying for
══════════════════════════════════════════════════════════

Yaver turns your powerful dev machine into the self-hosted
replacement for the solo-dev SaaS stack. Works with any AI coding
agent you're already paying for (Claude Code, Codex, Aider, Ollama).

Available help topics — use yaver_help({ topic: "..." }):

  Foundation        tasks, mcp, runners, auth, mobile, relay, tunnel, tmux
  Developer         wizard, tests, builds, sessions, git
  SaaS replacements forms, newsletter, jobs, image, pdf, oauth, mail,
                    shortener, waitlist, docs, meetings
  Overview          solo-stack  ← shows what each feature replaces and
                                   how much you save

Quick start:
  1. Install: npm install -g yaver-cli
  2. Sign in: yaver auth
  3. yaver init    ← first-run wizard, walks you through everything
  4. yaver new     ← generate a fullstack monorepo for your next app

Run yaver doctor any time to audit what's configured vs missing.

CLI commands: auth, serve, status, devices, init, new, doctor, tmux,
relay, tunnel, config, set-runner, mcp, email, mail, acl, logs, ping,
attach, connect`
	}
}

// resolveFilePath resolves a path relative to the work directory.
func (s *HTTPServer) resolveFilePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(s.taskMgr.workDir, path)
}

func boolStr(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func mcpToolJSON(data interface{}) interface{} {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcpToolError("json marshal error: " + err.Error())
	}
	return mcpToolResult(string(out))
}

func mcpToolResult(text string) interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
	}
}

func mcpToolError(text string) interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
		"isError": true,
	}
}

// mcpBrowserResult returns a text message + screenshot image for browser automation tools.
func mcpBrowserResult(result *BrowserActionResult, message string) interface{} {
	content := []map[string]interface{}{
		{"type": "text", "text": message},
	}
	if result.ScreenshotB64 != "" {
		content = append(content, map[string]interface{}{
			"type":     "image",
			"data":     result.ScreenshotB64,
			"mimeType": "image/png",
		})
	}
	return map[string]interface{}{
		"content": content,
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------------------
// tmux session management endpoints
// ---------------------------------------------------------------------------

// GET /tmux/sessions — list all tmux sessions with relationship info
func (s *HTTPServer) handleTmuxSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": []TmuxSession{}})
		return
	}
	sessions, err := tmuxMgr.ListTmuxSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if sessions == nil {
		sessions = []TmuxSession{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": sessions})
}

// POST /tmux/adopt — adopt an existing tmux session as a yaver task
func (s *HTTPServer) handleTmuxAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tmux not available"})
		return
	}
	var body struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Session == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session name"})
		return
	}
	task, err := tmuxMgr.AdoptSession(body.Session)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"taskId":  task.ID,
		"session": body.Session,
	})
}

// POST /tmux/detach — detach an adopted tmux session (stop monitoring, keep session alive)
func (s *HTTPServer) handleTmuxDetach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tmux not available"})
		return
	}
	var body struct {
		TaskID string `json:"taskId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing taskId"})
		return
	}
	if err := tmuxMgr.DetachSession(body.TaskID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "detached"})
}

// POST /tmux/input — send keyboard input to an adopted tmux session
func (s *HTTPServer) handleTmuxInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tmux not available"})
		return
	}
	var body struct {
		TaskID string `json:"taskId"`
		Input  string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing taskId or input"})
		return
	}
	if err := tmuxMgr.SendTmuxInput(body.TaskID, body.Input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
