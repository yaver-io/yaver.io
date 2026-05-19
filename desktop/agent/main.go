package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	mathRand "math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	osexec "os/exec"
	osuser "os/user"

	"github.com/google/uuid"
	"github.com/quic-go/quic-go"
	"golang.org/x/mod/semver"
	"golang.org/x/term"
)

const version = "1.99.200"

// Default hosted Convex instance (public endpoint). Override with --convex-url flag or convex_site_url in config.json.
const defaultConvexSiteURL = "https://perceptive-minnow-557.eu-west-1.convex.site"

func augmentAgentPATH() {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return
	}
	current := strings.TrimSpace(os.Getenv("PATH"))
	seen := map[string]struct{}{}
	for _, part := range strings.Split(current, ":") {
		part = strings.TrimSpace(part)
		if part != "" {
			seen[part] = struct{}{}
		}
	}

	candidates := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}
	if pyDirs, _ := filepath.Glob(filepath.Join(home, "Library", "Python", "*", "bin")); len(pyDirs) > 0 {
		candidates = append(candidates, pyDirs...)
	}

	var prepend []string
	for _, dir := range candidates {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			prepend = append(prepend, dir)
			seen[dir] = struct{}{}
		}
	}
	if len(prepend) == 0 {
		return
	}
	parts := append(prepend, strings.Split(current, ":")...)
	var cleaned []string
	seen = map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		cleaned = append(cleaned, part)
	}
	_ = os.Setenv("PATH", strings.Join(cleaned, ":"))
}

func relayInfosFromConfig(servers []RelayServerConfig) ([]RelayServerInfo, map[string]string) {
	var relayServers []RelayServerInfo
	passwords := make(map[string]string)
	for _, rs := range servers {
		if strings.TrimSpace(rs.QuicAddr) == "" {
			continue
		}
		relayServers = append(relayServers, RelayServerInfo{
			ID:       rs.ID,
			QuicAddr: rs.QuicAddr,
			HttpURL:  rs.HttpURL,
			Region:   rs.Region,
			Priority: rs.Priority,
		})
		if rs.Password != "" {
			passwords[rs.QuicAddr] = rs.Password
		}
	}
	return relayServers, passwords
}

func relayConfigMatches(a, b []RelayServerConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cacheResolvedRelayConfig(cfg *Config, servers []RelayServerInfo, globalPassword string, perRelayPasswords map[string]string) {
	if cfg == nil || len(cfg.RelayServers) > 0 || len(servers) == 0 {
		return
	}
	cached := make([]RelayServerConfig, 0, len(servers))
	for _, rs := range servers {
		if strings.TrimSpace(rs.QuicAddr) == "" {
			continue
		}
		cached = append(cached, RelayServerConfig{
			ID:       rs.ID,
			QuicAddr: rs.QuicAddr,
			HttpURL:  rs.HttpURL,
			Password: perRelayPasswords[rs.QuicAddr],
			Region:   rs.Region,
			Priority: rs.Priority,
		})
	}
	if relayConfigMatches(cfg.CachedRelayServers, cached) && cfg.CachedRelayPassword == globalPassword {
		return
	}
	cfg.CachedRelayServers = cached
	cfg.CachedRelayPassword = globalPassword
	if err := SaveConfig(cfg); err != nil {
		log.Printf("Warning: could not cache relay config: %v", err)
	}
}

func normalizeRelayHTTPURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// persistRotatedAuthToken writes a freshly-rotated bearer token back
// to ~/.yaver/config.json. SaveConfig is already atomic (tmp +
// rename + fsync), so a crash mid-write can't corrupt the file, and
// the old token remains valid on the server until the daily refresh
// ticks — either outcome (success or interrupted write) leaves the
// agent authenticated.
//
// The cfg passed here must be a FRESHLY loaded config. We re-read it
// here to avoid stomping on other mutations (relay cache updates,
// runner changes, device ID rotations) that may have happened on a
// different goroutine since the caller obtained its cfg pointer.
func persistRotatedAuthToken(cfg *Config, newToken string) error {
	if strings.TrimSpace(newToken) == "" {
		return nil
	}
	fresh, err := LoadConfig()
	if err != nil || fresh == nil {
		// Fall back to the caller's cfg — better than silently losing
		// the rotation; any concurrent writer will just resync on its
		// next SaveConfig.
		fresh = cfg
	}
	if fresh.AuthToken == newToken {
		return nil
	}
	// SetAuthToken stashes the prev token + rekeys vault.enc so the
	// new token can read existing entries. Without this, every
	// /auth/refresh ?rotate=1 cycle locks the vault.
	return SetAuthToken(fresh, newToken)
}

// tryOpenAgentVault is the agent boot mirror of openVaultE — same
// three-tier resolution (manual passphrase → current auth-token →
// previous auth-token) but stamps the deviceID into writes so sync
// attribution stays correct, and silently auto-rekeys + clears
// PreviousAuthToken on a successful previous-token unlock.
//
// Called from BOTH early boot (before any token validation /
// refresh) AND late boot (after the HTTPServer materialises). The
// early call is what makes rotations during boot recoverable: with
// the runtime store seeded under T_disk, every subsequent
// SetAuthToken can rekey the in-memory store + disk in lockstep so
// the vault key chain never lags the token chain.
func tryOpenAgentVault(cfg *Config, vaultPassFlag string) (*VaultStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if pass := strings.TrimSpace(vaultPassFlag); pass != "" {
		return NewVaultStoreWithDevice(pass, cfg.DeviceID)
	}
	if pass := strings.TrimSpace(os.Getenv("YAVER_VAULT_PASSPHRASE")); pass != "" {
		return NewVaultStoreWithDevice(pass, cfg.DeviceID)
	}
	if strings.TrimSpace(cfg.AuthToken) != "" {
		currentPass := DerivePassphraseFromToken(cfg.AuthToken)
		if vs, err := NewVaultStoreWithDevice(currentPass, cfg.DeviceID); err == nil {
			return vs, nil
		} else if !strings.Contains(err.Error(), "wrong passphrase") {
			return nil, err
		}
	}
	if strings.TrimSpace(cfg.PreviousAuthToken) != "" && strings.TrimSpace(cfg.AuthToken) != "" {
		prevPass := DerivePassphraseFromToken(cfg.PreviousAuthToken)
		vsPrev, err := NewVaultStoreWithDevice(prevPass, cfg.DeviceID)
		if err != nil {
			return nil, err
		}
		currentPass := DerivePassphraseFromToken(cfg.AuthToken)
		if rkErr := vsPrev.RekeyTo(currentPass); rkErr != nil {
			log.Printf("Warning: vault rekey on boot failed: %v — runtime store still usable; will retry on next rotation.", rkErr)
			return vsPrev, nil
		}
		cfg.PreviousAuthToken = ""
		if sErr := SaveConfig(cfg); sErr != nil {
			log.Printf("Warning: clear PreviousAuthToken after boot rekey failed: %v", sErr)
		}
		log.Printf("Vault auto-rekeyed on boot using previous auth token.")
		return vsPrev, nil
	}
	return nil, fmt.Errorf("vault still locked — no fallback token available")
}

func relayHTTPURLsMatch(a, b string) bool {
	a = normalizeRelayHTTPURL(a)
	b = normalizeRelayHTTPURL(b)
	return a != "" && b != "" && a == b
}

// synthRelayServerInfoFromURL parses a user-set relay HTTP URL into a
// RelayServerInfo when the URL isn't in platformConfig (Phase 2D —
// the user's managed-cloud box doubles as their own relay; per-user,
// not global, so it never appears in /config). The yaver-cloud image
// always publishes the bundled yaver-relay on QUIC 4433/UDP, so we
// can derive the QuicAddr deterministically from the URL's host.
// Returns ok=false for unparseable input — the caller falls back to
// the platform list. Independent of the URL's port (the QUIC port is
// a separate protocol on UDP; the HTTP URL is just for matching/display).
func synthRelayServerInfoFromURL(rawURL string) (RelayServerInfo, bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u == nil {
		return RelayServerInfo{}, false
	}
	host := u.Hostname()
	if host == "" {
		return RelayServerInfo{}, false
	}
	return RelayServerInfo{
		ID:       "user-managed-" + host,
		HttpURL:  rawURL,
		QuicAddr: host + ":4433",
		Region:   "user",
		// Priority 0 ⇒ tops the sorted list when the relay manager
		// orders by priority; doesn't change selection for managers
		// that broadcast to all relays, just informational ordering.
		Priority: 0,
	}, true
}

// restoreUserCwdFromNpmWrapper undoes the cwd-clobbering done by the npm
// wrapper's `go run .` dev fallback (cli/src/agent-runtime.js sets cwd to
// desktop/agent so the Go toolchain can find the module). The wrapper
// passes the user's real cwd via YAVER_USER_CWD; we chdir back to it
// before any cwd-sensitive command (wire push, wireless push, code, …)
// runs, otherwise every project-resolution defaults to desktop/agent.
func restoreUserCwdFromNpmWrapper() {
	cwd := strings.TrimSpace(os.Getenv("YAVER_USER_CWD"))
	if cwd == "" {
		return
	}
	if err := os.Chdir(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "yaver: warning — could not restore user cwd %q: %v\n", cwd, err)
	}
	os.Unsetenv("YAVER_USER_CWD")
}

func main() {
	augmentAgentPATH()
	logYaverBinaryDriftWarnings()
	restoreUserCwdFromNpmWrapper()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := os.Args[1]
	switch cmd {
	case "auth":
		runAuth(os.Args[2:])
	case "signout", "logout":
		runSignout()
	case "connect":
		runConnect(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "register-url":
		runRegisterURLCmd(os.Args[2:])
	case "push":
		runPushBridge(os.Args[2:])
	case "permissions":
		runMacOSPermissions(os.Args[2:])
	case "logs":
		runLogs(os.Args[2:])
	case "stop":
		runStop()
	case "clear-logs":
		runClearLogs()
	case "restart":
		runRestart(os.Args[2:])
	case "shutdown":
		runShutdown()
	case "ping":
		// New-style reachability ping when invoked with a positional hint
		// and no legacy flags: `yaver ping <alias|deviceId|name>`.
		// Legacy QUIC ping (-c, --device, --relay) keeps its old behavior.
		runPingDispatch(os.Args[2:])
	case "attach":
		runAttach(os.Args[2:])
	case "code":
		runCode(os.Args[2:])
	case "agent":
		runAgentMode(os.Args[2:])
	case "status":
		runStatus()
	case "devices":
		runDevices(os.Args[2:])
	case "alias":
		runAlias(os.Args[2:])
	case "ssh":
		runSSHWrap(os.Args[2:])
	case "config":
		runConfig(os.Args[2:])
	case "auto-update":
		runAutoUpdate(os.Args[2:])
	case "relay":
		runRelay(os.Args[2:])
	case "tunnel":
		runTunnel(os.Args[2:])
	case "set-runner":
		runSetRunner(os.Args[2:])
	case "mcp":
		runMCP(os.Args[2:])
	case "email":
		runEmail(os.Args[2:])
	case "acl":
		runACL(os.Args[2:])
	case "discover":
		discoverProjects()
		fp, _ := projectsFilePath()
		fmt.Printf("Project discovery complete: %s\n", fp)
	case "purge", "reset", "factory-reset":
		runPurge()
	case "wipe":
		runWipe(os.Args[2:])
	case "uninstall":
		runUninstall()
	case "tmux":
		runTmux(os.Args[2:])
	case "mobile-test":
		runMobileTest(os.Args[2:])
	case "exec":
		runExec(os.Args[2:])
	case "session":
		runSession(os.Args[2:])
	case "vault":
		runVault(os.Args[2:])
	case "runner":
		runRunner(os.Args[2:])
	case "runner-auth":
		runRunnerAuth(os.Args[2:])
	case "build":
		runBuild(os.Args[2:])
	case "iosNative", "ios-native":
		runNativeIOS(os.Args[2:])
	case "androidNative", "android-native":
		runNativeAndroid(os.Args[2:])
	case "flutter":
		runNativeFlutter(os.Args[2:])
	case "publish":
		runPublish(os.Args[2:])
	case "release":
		runRelease(os.Args[2:])
	case "monitor":
		runMonitor(os.Args[2:])
	case "flags":
		runFlags(os.Args[2:])
	case "analytics":
		runAnalytics(os.Args[2:])
	case "sourcemaps", "source-maps":
		runSourceMaps(os.Args[2:])
	case "env":
		runEnv(os.Args[2:])
	case "status-page", "statuspage":
		runStatusPage(os.Args[2:])
	case "blob", "blobs":
		runBlob(os.Args[2:])
	case "changelog":
		runChangelog(os.Args[2:])
	case "cron":
		runCron(os.Args[2:])
	case "apikey", "apikeys":
		runAPIKey(os.Args[2:])
	case "backup":
		runBackup(os.Args[2:])
	case "machine":
		runMachine(os.Args[2:])
	case "debug":
		runDebug(os.Args[2:])
	case "expo":
		runExpo(os.Args[2:])
	case "deploy":
		runDeploy(os.Args[2:])
	case "test":
		runTest(os.Args[2:])
	case "autotest":
		runAutotest(os.Args[2:])
	case "dev":
		runDev(os.Args[2:])
	case "vibe":
		runVibe(os.Args[2:])
	case "repo":
		runRepo(os.Args[2:])
	case "git":
		// `yaver git push-creds <device|alias> [...]` — forward locally
		// detected GitHub/GitLab tokens to one or more owned remote
		// machines via the same /machine/onboarding/apply flow the
		// dashboard uses. See git_push_creds_cmd.go.
		runGitCLI(os.Args[2:])
	case "pipeline":
		runPipeline(os.Args[2:])
	case "feedback":
		runFeedback(os.Args[2:])
	case "sdk":
		runSDK(os.Args[2:])
	case "ci":
		runCI(os.Args[2:])
	case "voice":
		runVoice(os.Args[2:])
	case "clean":
		runClean(os.Args[2:])
	case "cloud":
		runCloud(os.Args[2:])
	case "guests":
		runGuests(os.Args[2:])
	case "host-share":
		runHostShare(os.Args[2:])
	case "primary":
		runPrimary(os.Args[2:])
	case "secondary":
		runSecondary(os.Args[2:])
	case "ops":
		runOps(os.Args[2:])
	case "workspace":
		runWorkspace(os.Args[2:])
	case "diagnose":
		runDiagnoseCLI(os.Args[2:])
	case "managed":
		runManaged(os.Args[2:])
	case "2fa", "totp":
		runTwoFactor(os.Args[2:])
	case "sandbox":
		runSandbox(os.Args[2:])
	case "sdk-token":
		runSdkToken(os.Args[2:])
	case "forgot-password":
		runForgotPassword(os.Args[2:])
	case "change-password":
		runChangePassword(os.Args[2:])
	case "install":
		runInstall(os.Args[2:])
	// `yaver swift` and `yaver builder` dispatchers temporarily disabled —
	// the swift_cmd.go / remote_builder_cmd.go implementations are still
	// in flight in a parallel session and aren't on main yet. The CLI
	// release pipeline broke because main.go was referring to symbols
	// that don't exist in the committed tree. Re-add these cases when
	// swift_cmd.go + remote_builder_cmd.go land.
	case "update", "self-update", "upgrade":
		runManualUpdate()
	case "self":
		// `yaver self heal [--apply ...]` reconciles every yaver
		// binary on the box (apt, brew, npm, ~/.yaver/bin/<v>/, manual)
		// to a single canonical version. See self_heal.go.
		if len(os.Args) > 2 && os.Args[2] == "heal" {
			runSelfHealCommand(os.Args[3:])
			return
		}
		fmt.Println("usage: yaver self heal [--apply] [--include-managed] [--self-update] [--json]")
		os.Exit(2)
	case "doctor":
		// `yaver doctor build [--target=X]` is a focused preflight for
		// deploy toolchains. `yaver doctor webrtc [--install] [--json]`
		// probes the native-WebRTC remote-runtime stack — adb, xcrun,
		// the in-tree H.264 extractor, paired Mac builders. Everything
		// else falls through to the legacy wide-scan runDoctor().
		if len(os.Args) > 2 && os.Args[2] == "build" {
			runDoctorBuild(os.Args[3:])
			return
		}
		// `yaver doctor webrtc` lives in doctor_webrtc.go (parallel session,
		// not yet committed). Restore this branch when that file lands.
		runDoctor()
	case "init", "setup":
		runInit(os.Args[2:])
	case "new", "project-new", "project-wizard":
		runNew(os.Args[2:])
	case "autoideas":
		runAutoIdeas(os.Args[2:])
	case "autoinit":
		runAutoInit(os.Args[2:])
	case "stream":
		runStream(os.Args[2:])
	case "mail":
		runMail(os.Args[2:])
	case "copilot":
		runCopilot(os.Args[2:])
	case "expose":
		runExposeCmd(os.Args[2:])
	case "completion":
		runCompletion(os.Args[2:])
	case "support":
		runSupport(os.Args[2:])
	case "ui":
		runUI(os.Args[2:])
	case "phone":
		runPhone(os.Args[2:])
	case "wire":
		runWire(os.Args[2:])
	case "wireless":
		runWireless(os.Args[2:])
	case "android":
		runAndroid(os.Args[2:])
	case "remote":
		runRemote(os.Args[2:])
	case "insert":
		runRemoteInsert(os.Args[2:])
	case "monorepo":
		runMonorepo(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	case "version", "--version", "-v":
		fmt.Printf("yaver %s\n", version)
		checkLatestVersion()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Yaver — your AI coding agent, on your phone

Usage:
  yaver push        Push-to-device helper for existing RN/Expo projects
  yaver push ios    Discover iOS app in this repo, build IPA, upload to TestFlight
  yaver push android  Discover Android app in this repo, build AAB, upload to Play internal testing
  yaver auth        Sign in and start agent (opens browser)
  yaver auth status [--show-token]  Show who you are signed in as (gh/glab style)
  yaver signout     Sign out and clear credentials
  yaver connect     Connect to your dev machine
  yaver ping        Ping a device (direct or via relay)
  yaver stop        Stop the running agent
  yaver restart     Restart the agent
  yaver code        Terminal-first coding UX (interactive by default, mesh with --mesh)
  yaver attach      Interactive terminal — see tasks, type prompts (like Claude Code)
  yaver agent       Dependency-aware agent graph runner (chat + autoideas)
  yaver serve       Start the agent manually (advanced)
  yaver permissions Open the one-time macOS permission checklist again
  yaver logs        Show agent logs
  yaver clear-logs  Clear agent log file
  yaver self heal [--apply] [--include-managed] [--self-update]  Reconcile multi-source yaver installs
  yaver config      Show current configuration
  yaver config set <key> <value>  Set a config value (auto-start, auto-update, headless-keep-awake)
  yaver relay add <url> [--password <pass>] [--label <name>]  Add a relay server
  yaver relay list   List configured relay servers
  yaver relay remove <id-or-url>  Remove a relay server
  yaver relay test [url]  Test relay server health
  yaver relay set-password <pass>  Set default relay password
  yaver relay clear-password  Remove default relay password
  yaver tunnel add <url> [--cf-client-id <id>] [--cf-client-secret <secret>] [--label <name>]
  yaver tunnel list   List configured Cloudflare Tunnels
  yaver tunnel remove <id-or-url>  Remove a tunnel
  yaver tunnel test [url]  Test tunnel connectivity
  yaver tunnel setup  Show Cloudflare Tunnel setup guide
  yaver set-runner  Set default AI agent (also settable from mobile app, per task)
  yaver tmux list   List all tmux sessions (with agent detection)
  yaver tmux adopt <session>  Adopt an existing tmux session as a Yaver task
  yaver tmux detach <task-id>  Stop monitoring an adopted session (session keeps running)
  yaver mcp         Start MCP server (local stdio or network HTTP)
  yaver mcp setup <client>  Auto-configure MCP for Claude Code, Codex, or opencode
  yaver email       Email connector setup and management (Office 365 / Gmail)
  yaver acl         Agent Communication Layer — connect to other MCP servers
  yaver status      Show auth, relay, and connection status
  yaver devices [remove <device-id>]  List your registered devices or remove one
  yaver alias [set|rm|list] ...  Manage per-user device aliases (used by yaver ssh and the dashboard)
  yaver ssh <device|alias> [ssh args...]  Wrap OpenSSH; resolves device IP via Tailscale or Convex
  yaver exec        Execute a command on a remote device (like SSH)
  yaver session     Transfer AI agent sessions between machines
  yaver vault add <name> [--category <cat>] [--value <val>]  Add a secret to the vault
  yaver vault list   List all vault entries
  yaver vault get <name>  Get a vault entry value
  yaver vault delete <name>  Delete a vault entry
  yaver vault export  Export vault as plaintext JSON
  yaver vault import <file>  Import entries from JSON
  yaver build flutter apk [--dir <path>]  Build Flutter APK
  yaver build ios [repo-or-project-dir]   Discover iOS app in repo and build IPA
  yaver build android [repo-or-project-dir]  Discover Android app in repo and build AAB
  yaver build gradle apk [--dir <path>]   Build Android APK via Gradle
  yaver build xcode ipa [--scheme <name>]  Build iOS IPA via Xcode
  yaver build rn android [--dir <path>]   Build React Native Android
  yaver build list       List all builds
  yaver build status <id> Show build details
  yaver build register <file>  Register pre-built artifact
  yaver publish init [--dir <path>]  Scaffold .yaver/publish.yaml from the repo
  yaver publish run [--target <id>] [--allow-github-fallback]  Publish through local hardware first
  yaver expo setup [--dir <path>]      Inject Feedback SDK into Expo project
  yaver expo start [--dir <path>]     Start Expo Metro + P2P tunnel for hot reload
  yaver expo build android [--eas]    Build via Expo (--eas for cloud, no Mac needed)
  yaver expo build ios [--eas]        Build iOS via EAS Build (no Mac needed)
  yaver expo status                   Show Expo session + tunnel status
  yaver debug flutter [--dir <path>]  Start Flutter debug with hot reload tunnel
  yaver debug rn [--dir <path>]       Start React Native/Metro debug
  yaver debug --port <N>              Expose any TCP port for remote access
  yaver deploy --file <path>          Register artifact for P2P transfer
  yaver deploy --ci github --workflow <file.yml>  Trigger GitHub Actions
  yaver deploy --ci gitlab --repo <id>  Trigger GitLab CI pipeline
  yaver test unit [--dir <path>]  Auto-detect and run unit tests
  yaver test flutter [--dir <path>]  Run Flutter tests
  yaver test android [--dir <path>]  Run Android tests
  yaver test ios [--dir <path>]  Run iOS tests on simulator
  yaver test e2e [--dir <path>]  Run E2E tests (Playwright/Cypress/Maestro)
  yaver repo list      List discovered projects
  yaver repo auth status   Show Git provider / credential / vault status
  yaver repo auth setup <github|gitlab> [--token <pat>]  Save clone + CI auth together
  yaver repo switch <name>  Switch working directory to a project
  yaver repo refresh   Re-run project discovery
  yaver workspace merge <repo-or-path>...  Merge split repos into a Yaver monorepo with preserved history
  yaver workspace status   Show monorepo app status from yaver.workspace.yaml
  yaver pipeline --test --deploy <target>  Build → test → deploy in one command
  yaver loop add <spec.yaml>   Register an auto-dev loop from a .loop.yaml
  yaver loop list              List registered loops and their status
  yaver loop run <name>        Run one iteration of a loop (blocking)
  yaver loop stop <name>       Stop a running loop immediately
  yaver loop pause <name>      Pause a loop until resumed
  yaver loop resume <name>     Resume a paused loop
  yaver loop status <name>     Show detailed status for one loop
  yaver voice setup [--provider <name>]  Set up a voice provider (personaplex, openai)
  yaver voice serve      Start voice inference server (PersonaPlex)
  yaver voice status     Show voice provider status
  yaver voice test       Record & transcribe a test clip
  yaver voice providers  List available voice providers
  yaver feedback list   List visual bug reports from device testing
  yaver feedback show <id>  Show feedback details + transcript
  yaver feedback fix <id>   Create AI task from feedback report
  yaver sdk add <core|feedback>  Inject the Yaver SDK into this project
  yaver ci add <hermes|feedback|push-to-device|publish-runner>  Scaffold a GitHub Actions workflow
  yaver ci list             List available CI targets
  yaver cloud buy      Open the hosted cloud checkout flow
  yaver cloud create   Start the cloud flow and wait for the machine
  yaver cloud status   Show cloud machine status
  yaver cloud ssh      SSH into your cloud machine
  yaver cloud destroy  Tear down your cloud machine
  yaver guests invite <email>  Invite someone to use your machine (max 5 guests)
  yaver guests list            List your guests and their status
  yaver guests remove <email>  Revoke guest access
  yaver host-share prepare     Audit this machine for future host-backed guest coding
  yaver host-share create      Create a host-backed coding invite code/link
  yaver host-share join <code> Join a host-backed coding lease
  yaver host-share list        List host-share invites or sessions
  yaver host-share workspace-status --session <id>   Show local borrowed workspace
  yaver host-share workspace-bootstrap --session <id> --source-dir <path>  Seed workspace
  yaver host-share attach-repo --session <id> [--path <repo>]  Attach a guest repo to a borrowed workspace
  yaver host-share sync-repo --session <id> --to-host|--from-host  Sync an attached repo
  yaver host-share guest-roots --device <id>         List guest repo roots via host-share bus
  yaver host-share guest-read --device <id> --root <id> --path <file>  Read guest file
  yaver host-share guest-write --device <id> --root <id> --path <file> --content <txt>  Write guest file
  yaver host-share guest-pull --session <id> --device <id> [--root <id>]  Mirror guest repo into workspace
  yaver host-share guest-push --session <id> [--device <id>] [--root <id>]  Push workspace back to guest repo
  yaver host-share end <id>    End an active host-share session immediately
  yaver host-share revoke <code>  Revoke a host-share invite
  yaver host-share status      Show the saved host-share capability manifest
  yaver 2fa status             Show whether two-factor auth is enabled
  yaver 2fa enable             Enroll a TOTP authenticator app (optional)
  yaver 2fa disable            Remove two-factor auth from your account
  yaver record start <run> <task>       Start recording for a task
  yaver record stop  <run> <task>       Finalize a recording
  yaver record drivers                  Show which recording drivers work here
  yaver expose --port <N> [--subdomain <name>]  Expose a local port via yaver.io subdomain
  yaver expose list            List active expose entries
  yaver expose stop [subdomain]  Stop exposing a subdomain (or all)
  yaver clean       Remove old tasks, images, and logs (default: older than 30 days)
  yaver purge       Factory reset — remove all local data (auth, sessions, tasks, logs)
  yaver reset       Alias for purge
  yaver uninstall   Remove config, certs, and stop the agent
  yaver auth factory-reset   Re-sign in from a clean auth state and refresh npm CLI if available
  yaver completion <bash|zsh|fish>  Generate shell completions
  yaver doctor      Diagnose issues
  yaver help        Show this help message
  yaver version     Print version

Flags for auth:
  --headless        Use device code flow (for SSH/headless servers, auto-detected)
  --token           Provide token directly (skip browser)

Flags for serve:
  --debug           Run in foreground with verbose logging
  --port            HTTP server port (default 18080)
  --quic-port       QUIC server port (default 4433)
  --no-relay        Disable relay tunnels (direct connections only)
  --wait-for-session Wait for other Claude Code sessions to finish before starting tasks
  --dummy           Use dummy runner (fake responses for network testing)
  --work-dir        Working directory for tasks (default .)

Flags for connect:
  --host            Agent host (auto-discovers if not set)
  --port            Agent QUIC port (default 4433)
  --device          Device ID to connect to
  --relay           Connect through relay server (default: true)
  --direct          Connect directly via QUIC (skip relay)
  --relay-server    Relay server URL (auto-fetched from Convex if not set)

Examples:
  yaver set-runner claude           Use Claude Code (default)
  yaver set-runner codex            Use OpenAI Codex
  yaver set-runner aider            Use Aider
  yaver set-runner custom "my-ai --auto {prompt}"   Use a custom command
  yaver set-runner                  List available runners
  yaver runner-auth status [--target <deviceId>]   Show runner auth readiness
  yaver runner-auth set <runner> [flags]           Save runner/provider auth into the Yaver vault
  yaver runner-auth setup <runner> [flags]         Install + auth + MCP-wire Codex/Claude on this machine or a remote Yaver device
  (Agent is also selectable per task from the mobile app)
  yaver config set auto-start true  Start Yaver on login
  yaver config set auto-update true Check for updates on startup
  yaver config set headless-keep-awake true  Block sleep while yaver serve runs

Flags for exec:
  --device          Device ID or hostname prefix (auto-discovers if not set)
  --work-dir        Working directory on remote machine
  --timeout         Command timeout in seconds (default: 300)
  --relay           Force relay connection
  --direct          Force direct connection

Run 'yaver <command> -h' for command-specific options.
`)
}

func runPushBridge(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "ios":
			runNativeReleasePush(NativeIOS, args[1:])
			return
		case "android":
			runNativeReleasePush(NativeAndroid, args[1:])
			return
		}
	}

	npmPath, err := osexec.LookPath("npm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "yaver push requires npm because it bundles an existing React Native / Expo project.")
		fmt.Fprintln(os.Stderr, "Install Node.js/npm, then rerun `yaver push ...`.")
		os.Exit(1)
	}

	packageRef := fmt.Sprintf("yaver-cli@%s", version)
	argv := append([]string{"exec", "--yes", "--package", packageRef, "--", "yaver", "push"}, args...)
	cmd := osexec.Command(npmPath, argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	if wd, werr := os.Getwd(); werr == nil && strings.TrimSpace(wd) != "" {
		cmd.Dir = wd
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*osexec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "failed to run push bridge via npm: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// auth — sign in via browser OAuth (like claude auth)
// ---------------------------------------------------------------------------

func runAuth(args []string) {
	// Run the WSL2 network tuner before auth so the first `yaver
	// serve` (often invoked automatically right after auth) already
	// has UDP buffers large enough for the QUIC relay. Noop elsewhere.
	maybeRunWSL2NetworkTuning()

	// Subcommand dispatch: `yaver auth pair` / `yaver auth send`
	// handle the QR-based P2P token forwarding flow for cases
	// where another yaver machine is already signed in and we
	// want to skip the OAuth roundtrip entirely. The remaining
	// args path below handles the normal browser / device-code
	// flows for fresh logins.
	if len(args) > 0 {
		switch args[0] {
		case "pair":
			runAuthPair(args[1:])
			return
		case "send":
			runAuthSend(args[1:])
			return
		case "status":
			runAuthStatus(args[1:])
			return
		case "factory-reset", "reset", "repair":
			runAuthFactoryReset(args[1:])
			return
		}
	}

	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	convexURL := fs.String("convex-url", defaultConvexSiteURL, "Convex site URL")
	token := fs.String("token", "", "Provide token directly (skip browser)")
	email := fs.String("email", "", "Email for direct email/password auth")
	password := fs.String("password", "", "Password for direct email/password auth")
	fullName := fs.String("name", "", "Full name for email/password signup")
	signup := fs.Bool("signup", false, "Create an email/password account instead of logging in")
	headless := fs.Bool("headless", false, "Use device code flow (for headless/SSH servers)")
	backgroundWait := fs.Bool("background-wait", false, "Internal: continue polling a pending headless sign-in in the background")
	fs.Parse(args)

	if *backgroundWait {
		if err := runPendingAuthBackgroundWaiter(*convexURL); err != nil {
			fmt.Fprintf(os.Stderr, "background auth waiter: %v\n", err)
		}
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Check if already logged in
	if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		if err := ValidateToken(cfg.ConvexSiteURL, cfg.AuthToken); err == nil {
			fmt.Println("Already signed in.")
			fmt.Println()
			startServeIfStopped()
			return
		}
		// Token expired, continue to re-auth
		fmt.Println("Session expired. Re-authenticating...")
	}

	if *token != "" {
		if err := finalizeAuthConfig(cfg, *convexURL, *token, true, false); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if strings.TrimSpace(*email) != "" || strings.TrimSpace(*password) != "" || *signup {
		if strings.TrimSpace(*email) == "" || strings.TrimSpace(*password) == "" {
			fmt.Fprintln(os.Stderr, "Error: --email and --password are required for direct email/password auth")
			os.Exit(1)
		}
		var authToken string
		var authErr error
		if *signup {
			name := strings.TrimSpace(*fullName)
			if name == "" {
				name = strings.TrimSpace(strings.Split(strings.TrimSpace(*email), "@")[0])
			}
			authToken, authErr = SignupWithEmail(*convexURL, name, strings.TrimSpace(*email), *password)
		} else {
			authToken, authErr = LoginWithEmail(*convexURL, strings.TrimSpace(*email), *password)
		}
		if authErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", authErr)
			os.Exit(1)
		}
		if err := finalizeAuthConfig(cfg, *convexURL, authToken, true, false); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Device code flow for headless machines (SSH, no display)
	if *headless || isHeadless() {
		if *headless {
			fmt.Println("Using device code flow (--headless)...")
		} else {
			fmt.Println("Headless environment detected. Using device code flow...")
		}

		t, err := runDeviceCodeAuth(*convexURL)
		if err != nil {
			// Resumable: a round-timeout (agentic bash tool gave up
			// before the human finished signing in). Exit 0 with a
			// structured hint so the caller re-invokes this same
			// command — pending-auth is still on disk so we'll keep
			// polling the same URL. NOW we spawn the background
			// waiter (was eagerly spawned earlier, which raced the
			// foreground for the token).
			if errors.Is(err, errResumable) {
				ensurePendingAuthBackgroundWaiter(*convexURL)
				fmt.Println()
				fmt.Println("Sign-in still pending — same URL is valid.")
				fmt.Println("  Re-run: yaver auth")
				fmt.Println("  (resumes the existing flow; the human does NOT need to sign in again)")
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := finalizeAuthConfig(cfg, *convexURL, t, true, true); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Browser-based OAuth — opens yaver.io auth page with provider choice
	fmt.Println("Opening browser to sign in...")
	fmt.Println()

	authPageURL := "https://yaver.io/auth?client=desktop"
	fmt.Printf("If your browser doesn't open, visit:\n  %s\n\n", authPageURL)

	// Start local callback server — try multiple addresses for compatibility
	callbackToken := make(chan string, 1)

	callbackHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("  Callback received: %s %s\n", r.Method, r.URL.String())
		t := r.URL.Query().Get("token")
		if t != "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body style="background:#0f1117;color:#fff;font-family:system-ui;display:flex;align-items:center;justify-content:center;height:100vh;flex-direction:column">
				<h2 style="margin-bottom:8px">Signed in!</h2>
				<p style="color:#9ca3af">You can close this tab and return to your terminal.</p>
			</body></html>`)
			callbackToken <- t
		} else {
			http.Error(w, "Missing token", 400)
		}
	})

	// Listen on both 127.0.0.1 and localhost for maximum compatibility
	srv1 := &http.Server{Addr: "127.0.0.1:19836", Handler: callbackHandler}
	srv2 := &http.Server{Addr: "localhost:19836", Handler: callbackHandler}

	listenErr := make(chan error, 1)
	go func() { listenErr <- srv1.ListenAndServe() }()

	// Give first server a moment to start.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-listenErr:
		fmt.Fprintf(os.Stderr, "Error: could not start callback server on 127.0.0.1:19836: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is another 'yaver auth' running?")
		os.Exit(1)
	default:
	}

	// Also try localhost (ignore errors — 127.0.0.1 may already cover it)
	go func() { srv2.ListenAndServe() }()

	openBrowser(authPageURL)

	fmt.Println("Waiting for authentication...")

	select {
	case t := <-callbackToken:
		// Give the browser time to receive the HTML response before shutting down servers.
		time.Sleep(500 * time.Millisecond)
		srv1.Close()
		srv2.Close()
		fmt.Printf("  Token received (%d chars)\n", len(t))
		cfg.ConvexSiteURL = *convexURL
		// Retry validation against Convex with the candidate token
		// before we commit anything to disk — session may not be
		// fully written through yet on the backend.
		var validationErr error
		for attempt := 0; attempt < 8; attempt++ {
			if attempt > 0 {
				delay := time.Duration(attempt) * time.Second
				fmt.Printf("  Retrying validation (attempt %d/8, wait %s)...\n", attempt+1, delay)
				time.Sleep(delay)
			}
			validationErr = ValidateToken(cfg.ConvexSiteURL, t)
			if validationErr == nil {
				break
			}
			fmt.Printf("  Validation attempt %d failed: %v\n", attempt+1, validationErr)
		}
		if validationErr != nil {
			fmt.Fprintf(os.Stderr, "Error: token validation failed after retries: %v\n", validationErr)
			fmt.Fprintln(os.Stderr, "The token was received but could not be validated against Convex.")
			fmt.Fprintln(os.Stderr, "Try again with: yaver auth factory-reset")
			os.Exit(1)
		}
		if cfg.DeviceID == "" {
			cfg.DeviceID = uuid.New().String()
		}
		// Clear any manually configured relay — use per-user relay from backend
		cfg.RelayServers = nil
		cfg.RelayPassword = ""
		if err := SetAuthToken(cfg, t); err != nil {
			log.Fatalf("save config: %v", err)
		}
		fmt.Println()
		fmt.Println("Signed in successfully.")
		fmt.Println("  Free relay: public.yaver.io (included, no setup needed)")
		fmt.Println()
		startServeIfStopped()
		autoSetupMCP()

	case <-time.After(5 * time.Minute):
		srv1.Close()
		srv2.Close()
		fmt.Fprintln(os.Stderr, "Authentication timed out.")
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// auth status — gh / glab style summary of the local auth state
// ---------------------------------------------------------------------------

func runAuthStatus(args []string) {
	fs := flag.NewFlagSet("auth status", flag.ExitOnError)
	showToken := fs.Bool("show-token", false, "Display the auth token instead of masking it")
	fs.Parse(args)

	const host = "yaver.io"
	fmt.Println(host)

	cfg, err := LoadConfig()
	cfgPath, _ := ConfigPath()
	if cfgPath != "" {
		if home, herr := os.UserHomeDir(); herr == nil && strings.HasPrefix(cfgPath, home) {
			cfgPath = "~" + strings.TrimPrefix(cfgPath, home)
		}
	}

	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		// Mid-device-code-flow is a meaningful "in progress" state — surface
		// it explicitly so an orchestrating agent can poll for completion.
		if pend, _ := loadPendingAuth(); pend != nil &&
			pend.ExpiresAt > time.Now().UnixMilli() &&
			strings.TrimSpace(pend.DeviceCode) != "" {
			fmt.Printf("  \033[33m●\033[0m Sign-in pending on %s\n", host)
			fmt.Printf("  - URL: %s\n", pend.URL)
			fmt.Printf("  - Code: %s\n", pend.UserCode)
			fmt.Printf("  - Expires in: %s\n", humanRoundDuration(time.Until(time.UnixMilli(pend.ExpiresAt))))
			fmt.Println()
			fmt.Println("Sign in on your phone, then run `yaver auth` again to finalize.")
			os.Exit(1)
		}
		fmt.Printf("  \033[31mX\033[0m Not logged in to %s\n", host)
		fmt.Println()
		fmt.Println("To sign in, run: yaver auth")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	statusClient := &http.Client{Timeout: 5 * time.Second}
	info, validateErr := func() (*UserInfo, error) {
		req, reqErr := newBearerRequest("GET", convexURL+"/auth/validate", cfg.AuthToken, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		resp, respErr := statusClient.Do(req)
		if respErr != nil {
			return nil, respErr
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("validate token failed (status %d)", resp.StatusCode)
		}
		var result struct {
			User UserInfo `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}
		return &result.User, nil
	}()

	tokenDisplay := maskAuthToken(cfg.AuthToken)
	if *showToken {
		tokenDisplay = cfg.AuthToken
	}

	if validateErr != nil {
		// Distinguish "session expired" (server said 401) from "couldn't reach
		// server" (network/dns/down). gh prints both as warnings, but we know
		// the difference and the recovery path is different.
		if strings.Contains(validateErr.Error(), "status 401") || strings.Contains(validateErr.Error(), "status 403") {
			fmt.Printf("  \033[31mX\033[0m Session expired for %s (%s)\n", host, cfgPath)
			fmt.Printf("  - Backend: %s\n", convexURL)
			if cfg.DeviceID != "" {
				fmt.Printf("  - Device ID: %s\n", cfg.DeviceID)
			}
			fmt.Printf("  - Token: %s\n", tokenDisplay)
			fmt.Println()
			fmt.Println("To re-authenticate, run: yaver auth")
			os.Exit(1)
		}
		fmt.Printf("  \033[33m●\033[0m Token present but %s is unreachable (%v)\n", host, validateErr)
		fmt.Printf("  - Backend: %s\n", convexURL)
		if cfg.DeviceID != "" {
			fmt.Printf("  - Device ID: %s\n", cfg.DeviceID)
		}
		fmt.Printf("  - Token: %s\n", tokenDisplay)
		os.Exit(1)
	}

	identity := info.Email
	if info.Provider != "" {
		identity = fmt.Sprintf("%s (%s)", info.Email, info.Provider)
	}
	fmt.Printf("  \033[32m✓\033[0m Logged in to %s as %s (%s)\n", host, identity, cfgPath)
	if info.FullName != "" && info.FullName != info.Email {
		fmt.Printf("  - Name: %s\n", info.FullName)
	}
	fmt.Printf("  - Backend: %s\n", convexURL)
	if cfg.DeviceID != "" {
		fmt.Printf("  - Device ID: %s\n", cfg.DeviceID)
	}
	fmt.Printf("  - Token: %s\n", tokenDisplay)
}

// maskAuthToken returns a redacted form of an auth token suitable for display.
// Keeps any well-known prefix (e.g. "yvr_") followed by asterisks the same
// length as the rest of the token, mirroring `gh auth status` style.
func maskAuthToken(tok string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return ""
	}
	if i := strings.IndexByte(tok, '_'); i > 0 && i < 8 {
		return tok[:i+1] + strings.Repeat("*", len(tok)-i-1)
	}
	return strings.Repeat("*", len(tok))
}

// ---------------------------------------------------------------------------
// signout — clear credentials
// ---------------------------------------------------------------------------

func runSignout() {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Println("Not signed in.")
		return
	}

	// Mark device offline and report event before clearing credentials
	if cfg.DeviceID != "" && cfg.ConvexSiteURL != "" {
		_ = ReportDeviceEvent(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID, "stopped", "signout")
		if err := MarkOffline(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); err != nil {
			fmt.Printf("Warning: could not mark device offline: %v\n", err)
		}
	}

	// Stop running agent if any (it will lose auth after signout)
	if pid, running := isAgentRunning(); running {
		if proc, err := os.FindProcess(pid); err == nil {
			terminateProcess(proc)
		}
	}

	cfg.AuthToken = ""
	// Drop the vault-recovery breadcrumb on explicit sign-out — leaving
	// it would let any future re-auth on the same machine silently
	// inherit the previous user's vault.
	cfg.PreviousAuthToken = ""
	// Keep DeviceID so the bootstrap agent can re-register with Convex
	// + relay with a stable identity. The mobile app and web UI show
	// the device in the list with a "NEEDS AUTH" badge and auto-pair
	// using encrypted token push.
	if err := SaveConfigClearingAuth(cfg); err != nil {
		log.Fatalf("save config: %v", err)
	}
	fmt.Println("Signed out.")
}

// ---------------------------------------------------------------------------
// clean — remove old tasks, images, and logs
// ---------------------------------------------------------------------------

func runClean(args []string) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	days := fs.Int("days", 30, "Remove tasks older than N days")
	all := fs.Bool("all", false, "Remove all completed/stopped/failed tasks regardless of age")
	dryRun := fs.Bool("dry-run", false, "Show what would be removed without deleting")
	fs.Parse(args)

	result := performClean(*days, *all, *dryRun)

	if *dryRun {
		fmt.Println("Dry run — no changes made:")
	}
	fmt.Printf("  Tasks removed:  %d\n", result.TasksRemoved)
	fmt.Printf("  Image dirs:     %d\n", result.ImagesRemoved)
	fmt.Printf("  Logs cleared:   %v\n", result.LogsCleared)
	fmt.Printf("  Space freed:    %s\n", formatBytes(result.BytesFreed))
}

// purge — wipe all local data (auth, sessions, tasks, projects, certs, logs)
// ---------------------------------------------------------------------------

func runPurge() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot find home dir: %v", err)
	}
	yaverDir := filepath.Join(home, ".yaver")

	// Check if directory exists
	if _, err := os.Stat(yaverDir); os.IsNotExist(err) {
		fmt.Println("Nothing to purge — ~/.yaver does not exist.")
		return
	}

	// List what will be removed
	fmt.Println("This will remove ALL local Yaver data:")
	fmt.Println()
	entries, _ := os.ReadDir(yaverDir)
	for _, e := range entries {
		info, _ := e.Info()
		if info != nil && info.IsDir() {
			fmt.Printf("  %s/\n", e.Name())
		} else {
			fmt.Printf("  %s\n", e.Name())
		}
	}
	fmt.Println()
	fmt.Print("Are you sure? (y/N): ")

	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Aborted.")
		return
	}

	if err := os.RemoveAll(yaverDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Purged. All local data removed from ~/.yaver/")
	fmt.Println("Run 'yaver auth' to sign in again.")
}

// ---------------------------------------------------------------------------
// connect — connect to a remote agent interactively
// ---------------------------------------------------------------------------

func runPing(args []string) {
	fs := flag.NewFlagSet("ping", flag.ExitOnError)
	deviceID := fs.String("device", "", "Device ID to ping")
	useRelay := fs.Bool("relay", true, "Ping through relay server (default: true)")
	relayURL := fs.String("relay-server", "", "Relay server URL (auto-fetched if not set)")
	count := fs.Int("c", 5, "Number of pings")
	fs.Parse(args)

	cfg := mustLoadAuthConfig()

	// Auto-discover device
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing devices: %v\n", err)
		os.Exit(1)
	}
	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "No devices found.")
		os.Exit(1)
	}

	var target *DeviceInfo
	for i := range devices {
		if *deviceID != "" && devices[i].DeviceID == *deviceID {
			target = &devices[i]
			break
		}
		if *deviceID == "" && devices[i].IsOnline {
			target = &devices[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintln(os.Stderr, "No matching online device.")
		os.Exit(1)
	}

	authHeader := "Bearer " + cfg.AuthToken
	client := &http.Client{Timeout: 10 * time.Second}

	// Determine base URL
	var baseURL string
	var mode string

	if *useRelay || *relayURL != "" {
		if *relayURL == "" {
			relays, err := FetchRelayServers(cfg.ConvexSiteURL)
			if err != nil || len(relays) == 0 {
				fmt.Fprintln(os.Stderr, "No relay servers available.")
				os.Exit(1)
			}
			*relayURL = relays[0].HttpURL
		}
		baseURL = fmt.Sprintf("%s/d/%s", strings.TrimRight(*relayURL, "/"), target.DeviceID)
		mode = "relay"
	} else {
		baseURL = fmt.Sprintf("http://%s:%d", target.QuicHost, target.QuicPort)
		mode = "direct"
	}

	fmt.Printf("PING %s (%s) via %s\n", target.Name, target.DeviceID[:8], mode)

	var totalMs float64
	var minMs, maxMs float64
	success := 0
	minMs = 999999

	for i := 0; i < *count; i++ {
		start := time.Now()
		req, _ := http.NewRequest("GET", baseURL+"/health", nil)
		req.Header.Set("Authorization", authHeader)
		resp, err := client.Do(req)
		rtt := time.Since(start)
		rttMs := float64(rtt.Microseconds()) / 1000.0

		if err != nil {
			fmt.Printf("ping %d: error — %v\n", i+1, err)
		} else {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				fmt.Printf("pong from %s: time=%.1fms via=%s\n", target.Name, rttMs, mode)
				totalMs += rttMs
				if rttMs < minMs {
					minMs = rttMs
				}
				if rttMs > maxMs {
					maxMs = rttMs
				}
				success++
			} else {
				fmt.Printf("ping %d: HTTP %d\n", i+1, resp.StatusCode)
			}
		}

		if i < *count-1 {
			time.Sleep(1 * time.Second)
		}
	}

	fmt.Printf("\n--- %s ping statistics ---\n", target.Name)
	fmt.Printf("%d packets transmitted, %d received, %.0f%% loss\n",
		*count, success, float64(*count-success)/float64(*count)*100)
	if success > 0 {
		fmt.Printf("rtt min/avg/max = %.1f/%.1f/%.1f ms\n",
			minMs, totalMs/float64(success), maxMs)
	}
}

func runConnect(args []string) {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	host := fs.String("host", "", "Agent host (auto-discovers if not set)")
	port := fs.Int("port", 4433, "Agent QUIC port")
	deviceID := fs.String("device", "", "Device ID to connect to")
	useRelay := fs.Bool("relay", true, "Connect through relay server (default: true)")
	direct := fs.Bool("direct", false, "Connect directly via QUIC (skip relay)")
	relayURL := fs.String("relay-server", "", "Relay server URL (e.g. https://connect.yaver.io). Auto-fetched if not set")
	fs.Parse(args)

	// --direct overrides --relay
	if *direct {
		*useRelay = false
	}

	cfg := mustLoadAuthConfig()

	// Auto-discover device
	var targetDeviceID string
	if *host == "" || *useRelay {
		devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing devices: %v\n", err)
			os.Exit(1)
		}

		if len(devices) == 0 {
			fmt.Fprintln(os.Stderr, "No devices found. Make sure your agent is running on your dev machine.")
			os.Exit(1)
		}

		var target *DeviceInfo
		for i := range devices {
			if *deviceID != "" && devices[i].DeviceID == *deviceID {
				target = &devices[i]
				break
			}
			if *deviceID == "" && devices[i].IsOnline {
				target = &devices[i]
				break
			}
		}

		if target == nil {
			fmt.Fprintln(os.Stderr, "No matching online device. Your devices:")
			for _, d := range devices {
				status := "offline"
				if d.IsOnline {
					status = "online"
				}
				fmt.Fprintf(os.Stderr, "  %s  %-20s  %-8s  %s:%d\n", d.DeviceID[:8], d.Name, status, d.QuicHost, d.QuicPort)
			}
			os.Exit(1)
		}

		if *host == "" {
			*host = target.QuicHost
			*port = target.QuicPort
		}
		targetDeviceID = target.DeviceID
		fmt.Printf("Connecting to %s (%s)...\n", target.Name, target.DeviceID[:8])
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		cancel()
	}()

	if *useRelay || *relayURL != "" {
		// Connect via relay HTTP proxy
		if *relayURL == "" {
			// Auto-fetch relay servers from Convex
			relays, err := FetchRelayServers(cfg.ConvexSiteURL)
			if err != nil || len(relays) == 0 {
				fmt.Fprintln(os.Stderr, "No relay servers available. Check your Convex config.")
				os.Exit(1)
			}
			*relayURL = relays[0].HttpURL
			fmt.Printf("Using relay: %s (%s)\n", relays[0].ID, relays[0].Region)
		}

		if targetDeviceID == "" {
			fmt.Fprintln(os.Stderr, "Device ID required for relay connection. Use --device flag.")
			os.Exit(1)
		}

		baseURL := fmt.Sprintf("%s/d/%s", strings.TrimRight(*relayURL, "/"), targetDeviceID)
		if err := RunClientHTTP(ctx, baseURL, cfg.AuthToken, TerminalClientOptions{Source: terminalRemoteTaskSource}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := RunClient(ctx, *host, *port, cfg.AuthToken, TerminalClientOptions{Source: terminalRemoteTaskSource}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

// ---------------------------------------------------------------------------
// serve — run the QUIC agent server
// ---------------------------------------------------------------------------

// pidFilePath returns the path to the PID file.
func pidFilePath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agent.pid")
}

func shouldTrackPrimaryAgent(httpPort int) bool {
	return httpPort == 18080
}

type localAgentHealthInfo struct {
	OK          bool   `json:"ok"`
	Hostname    string `json:"hostname"`
	Version     string `json:"version"`
	AuthExpired bool   `json:"authExpired"`
}

type localAgentInfo struct {
	OK       bool   `json:"ok"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
	WorkDir  string `json:"workDir"`
	Project  string `json:"project"`
}

func probeLocalAgentHealthInfo(port int) *localAgentHealthInfo {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	// STREAMING DEBUG
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var out localAgentHealthInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	if !out.OK {
		return nil
	}
	return &out
}

func probeAuthedLocalAgentInfo(port int, authToken string) *localAgentInfo {
	if strings.TrimSpace(authToken) == "" {
		return nil
	}
	req, err := newBearerRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/info", port), authToken, nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	// STREAMING DEBUG
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var out localAgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	if !out.OK {
		return nil
	}
	return &out
}

func refreshExistingLocalAgent(authToken string, httpPort int, workDir string) {
	if strings.TrimSpace(authToken) == "" {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	if strings.TrimSpace(workDir) != "" {
		body, _ := json.Marshal(map[string]string{"workDir": workDir})
		req, err := newBearerRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/agent/workdir", httpPort), authToken, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	req, err := newBearerRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/projects/refresh", httpPort), authToken, nil)
	if err != nil {
		return
	}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// logFilePath returns the path to the log file.
func logFilePath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agent.log")
}

func finalizeAuthConfig(cfg *Config, convexURL, token string, printSuccess, printHeadlessSteps bool) error {
	cfg.ConvexSiteURL = convexURL
	if err := ValidateToken(cfg.ConvexSiteURL, token); err != nil {
		return fmt.Errorf("token validation failed: %w", err)
	}
	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.New().String()
	}
	// Clear any manually configured relay — use per-user relay from backend
	cfg.RelayServers = nil
	cfg.RelayPassword = ""
	applyDefaultHeadlessKeepAwake(cfg)
	if err := SetAuthToken(cfg, token); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	// If a daemon is already running locally, nudge it to re-read the
	// fresh token from disk + clear its in-memory authExpired flag.
	// Without this hop, the running daemon keeps serving /info with
	// authExpired=true until the next 5-min heartbeat tick — making
	// `yaver primary status` race the heartbeat right after a successful
	// `yaver auth`. Loopback-only endpoint; best-effort.
	nudgeRunningDaemonToReloadAuth()
	// First-auth ergonomics: if the user has no primary device set
	// yet, mark THIS box as primary. Subsequent `yaver primary status` /
	// `yaver primary auth` then have a target without an explicit
	// `yaver primary set` step. Skipped when a primary already exists
	// — never silently overwrite the user's choice.
	maybeSetSelfAsPrimaryAfterAuth(cfg)
	if printSuccess {
		fmt.Println("Signed in successfully.")
		fmt.Println("  Free relay: public.yaver.io (included, no setup needed)")
		if shouldEnableHeadlessKeepAwake(cfg) {
			fmt.Println("  Headless keep-awake: enabled while `yaver serve` is running")
		}
		fmt.Println()
		// Browser-OAuth callers don't go through printHeadlessNextSteps,
		// so add a short next-steps line so a fresh user knows what to
		// run. Headless flow already prints its own (richer) variant.
		if !printHeadlessSteps {
			fmt.Println("Next:")
			fmt.Println("  yaver primary       see your devices, pick a primary")
			fmt.Println("  yaver code          terminal UI for AI-driven dev on this machine")
			fmt.Println("  yaver ssh primary   SSH to your primary (auto-bootstraps keys)")
			fmt.Println()
		}
	}
	// Register reboot persistence (systemd user unit on Linux, launchd
	// LaunchAgent on macOS, Scheduled Task on Windows) before forking
	// the agent. Done HERE — not just in the headless steps — so a
	// browser-OAuth sign-in on a real desktop also gets "yaver starts
	// after reboot" without an extra command. Idempotent: skipped if
	// the unit/plist/task already exists, and opt-out via
	// YAVER_NO_AUTO_START=1.
	maybeRegisterAutoStartAfterAuth()
	startServeIfStopped()
	autoSetupMCP()
	maybeRunHostSharePrepareOnboarding("auth")
	if printHeadlessSteps {
		printHeadlessNextSteps()
	}
	return nil
}

// maybeRegisterAutoStartAfterAuth runs ensureAutoStart in the shared
// auth-success path. Prints a one-line "Reboot persistence: …" hint
// so the user knows what happened, plus a WSL-specific note pointing
// at the differences from a real Linux box. Idempotent + opt-out via
// YAVER_NO_AUTO_START=1.
func maybeRegisterAutoStartAfterAuth() {
	if isAutoStartInstalled() {
		return
	}
	if envTruthy(os.Getenv("YAVER_NO_AUTO_START")) {
		return
	}
	exePath, err := os.Executable()
	if err != nil || exePath == "" {
		return
	}
	workDir, _ := os.Getwd()
	msg := ensureAutoStart(exePath, workDir)
	if msg == "" {
		// Either WSL couldn't write the helper, or runtime.GOOS is
		// outside the supported set (BSDs, etc). Warn the user so
		// they know reboot persistence is on them — much louder than
		// the previous silent skip.
		warnAutoStartUnavailable()
		return
	}
	fmt.Println("Reboot persistence:")
	fmt.Println("  " + msg)
	if isWSL() {
		// CLAUDE.md ships docs/wsl2-relay-troubleshooting.md for
		// related WSL gotchas; surface that hook so users debugging
		// "agent didn't come back after Windows reboot" find it.
		fmt.Println("  WSL note: shell-profile hook starts yaver when you open a WSL")
		fmt.Println("  shell. For start-on-Windows-boot, the helper also tries to")
		fmt.Println("  drop a wrapper into your Windows Startup folder; if that fails")
		fmt.Println("  (e.g. /mnt/c not mounted, no $USER), add a Task Scheduler")
		fmt.Println("  entry that runs `wsl.exe -d <distro> -u <user> bash -lc")
		fmt.Println("  '~/.yaver/wsl-autostart.sh'`. See docs/wsl2-relay-troubleshooting.md.")
		fmt.Println("  Windows host note: WSL cannot block Windows sleep. For unattended")
		fmt.Println("  remote use, disable Windows sleep, run Tailscale on Windows itself,")
		os.Stdout.WriteString("  and prefer mirrored networking in `%USERPROFILE%\\\\.wslconfig`.\n")
	} else if runtime.GOOS == "darwin" && !isDarwinLaunchDaemonInstalled() {
		fmt.Println("  macOS note: the default LaunchAgent starts after login only.")
		fmt.Println("  For a real headless Mac mini that must come back before login, run:")
		fmt.Println("    sudo yaver serve --install-launchd-daemon")
	}
	fmt.Println("  (opt out any time: `yaver config set auto-start false`)")
	fmt.Println()
}

// warnAutoStartUnavailable prints a clear, actionable warning when
// reboot persistence couldn't be registered — so a user on WSL
// without /mnt/c or on a niche OS doesn't quietly end up with a
// box that won't come back after a reboot.
func warnAutoStartUnavailable() {
	switch {
	case isWSL():
		fmt.Println("Reboot persistence: NOT registered (WSL helper write failed).")
		fmt.Println("  This usually means /mnt/c isn't mounted or $USER is empty.")
		fmt.Println("  Either run yaver from a tmux/screen session inside WSL, or")
		fmt.Println("  enable systemd in WSL2 (`/etc/wsl.conf` → `[boot] systemd=true`,")
		fmt.Println("  then `wsl --shutdown`) and re-run `yaver serve --install-systemd`.")
		fmt.Println("  Background: docs/wsl2-relay-troubleshooting.md.")
		fmt.Println()
	case runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows":
		fmt.Printf("Reboot persistence: NOT registered (%s isn't a supported auto-start target).\n", runtime.GOOS)
		fmt.Println("  Run `yaver serve` from your shell startup or a process supervisor.")
		fmt.Println()
	}
}

// printHeadlessNextSteps runs at the end of a successful headless
// `yaver auth`. Surfaces the Yaver mobile app install (required for
// the P2P client end) and the bootstrap-secret nudge for remote
// re-auth. Reboot persistence is now registered earlier in
// finalizeAuthConfig (via maybeRegisterAutoStartAfterAuth) so it
// applies to BOTH browser-OAuth and headless sign-ins; this function
// no longer touches systemd/launchd directly.
func printHeadlessNextSteps() {
	fmt.Println("Next — on your phone:")
	fmt.Println("  Open the Yaver app and sign in with the same account you just used.")
	fmt.Println("    iPhone:            https://apps.apple.com/us/app/yaver-io/id6760467669")
	fmt.Println("    Android (Play):    https://play.google.com/store/apps/details?id=io.yaver.mobile")
	fmt.Println("  This machine will appear in its device list automatically.")
	fmt.Println()

	// Bootstrap-secret nudge still needs a human decision — where to store it.
	bootstrapSet := false
	if cfg, _ := LoadConfig(); cfg != nil && strings.TrimSpace(cfg.BootstrapSecretHash) != "" {
		bootstrapSet = true
	}
	if !bootstrapSet {
		fmt.Println("Optional: to let the mobile app remotely re-auth this box if the token")
		fmt.Println("ever expires while you're away from it, run: yaver init")
		fmt.Println()
	}
	if shouldEnableHeadlessKeepAwake(cfgOrEmpty()) {
		fmt.Println("While the agent is running, Yaver will ask the OS not to sleep this box.")
		fmt.Println("Opt out: `yaver config set headless-keep-awake false`")
		fmt.Println()
	} else if isWSL() {
		fmt.Println("WSL note: Yaver cannot block Windows sleep from inside WSL.")
		fmt.Println("Use the WSL startup helper plus Windows power settings / Tailscale service.")
		fmt.Println()
	}
	if runtime.GOOS == "darwin" && !isDarwinLaunchDaemonInstalled() {
		fmt.Println("For a real headless macOS box, install the LaunchDaemon once:")
		fmt.Println("  sudo yaver serve --install-launchd-daemon")
		fmt.Println()
	}
}

func cfgOrEmpty() *Config {
	cfg, _ := LoadConfig()
	if cfg != nil {
		return cfg
	}
	return &Config{}
}

// startServeIfStopped forks `yaver serve` in the background if the
// agent isn't already running. Called at the end of a successful
// auth / pair flow so the user doesn't have to remember the extra
// step. Best-effort: prints a diagnostic to stderr on failure but
// never aborts the caller — the user can always run `yaver serve`
// manually.
func startServeIfStopped() {
	if probeLocalAgentHealthInfo(18080) != nil {
		fmt.Println("Agent is already running. No need to run `yaver serve` again.")
		return
	}
	if _, running := isAgentRunning(); running {
		fmt.Println("Agent is already running. No need to run `yaver serve` again.")
		return
	}
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "auto-start: cannot find yaver binary: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run `yaver serve` manually.")
		return
	}
	fmt.Println("Starting Yaver agent automatically...")
	cmd := osexec.Command(execPath, "serve")
	// Inherit stdio so the user sees the "Restarting Yaver agent" /
	// "Starting Yaver agent…" line the server prints before it
	// forks into the background. Once the parent serve process
	// returns, we're done.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "auto-start: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run `yaver serve` manually to start the agent.")
	}
}

// nudgeRunningDaemonToReloadAuth POSTs /auth/reload-from-disk on the local
// daemon (if one is running). The handler reads ~/.yaver/config.json fresh
// and applies the new token to in-memory state via applyRecoveredAuthToken,
// clearing authExpired and triggering a heartbeat. Best-effort: a missing
// daemon, network blip, or non-2xx response is logged at debug-level and
// ignored — the heartbeat loop's currentToken() closure will catch up on
// its next tick (~5 min) regardless. The whole call is bounded at 3 s so a
// hung daemon never wedges `yaver auth`.
func nudgeRunningDaemonToReloadAuth() {
	if probeLocalAgentHealthInfo(18080) == nil {
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18080/auth/reload-from-disk", nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Not a hard failure — the daemon will catch up on its next
		// heartbeat. Worst case the user sees one stale `primary status`
		// before the 5-min tick recovers state.
		return
	}
}

// maybeSetSelfAsPrimaryAfterAuth marks this device as the user's primary
// when no primary is set yet. Called from finalizeAuthConfig after a
// successful `yaver auth`. Idempotent + non-destructive: a non-empty
// primaryDeviceId in userSettings is left alone, even if it points at a
// different device. Best-effort with a tight timeout — a Convex hiccup
// here never blocks the auth flow.
func maybeSetSelfAsPrimaryAfterAuth(cfg *Config) {
	if cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.DeviceID) == "" {
		return
	}
	convexURL := strings.TrimSpace(cfg.ConvexSiteURL)
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current, err := primaryGetCurrent(ctx, cfg.AuthToken, convexURL)
	if err != nil {
		// Convex unreachable / misconfigured. Silent — primary can be
		// set later via `yaver primary set self`.
		return
	}
	if strings.TrimSpace(current) != "" {
		return
	}
	if err := primarySaveRaw(ctx, cfg.AuthToken, convexURL, cfg.DeviceID, false); err != nil {
		return
	}
	fmt.Println("  Primary device: set to this machine (first device — change with `yaver primary set <other>`)")
}

// isAgentRunning checks if the agent process is alive.
func isAgentRunning() (int, bool) {
	pidFile := pidFilePath()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, false
	}
	if !isProcessAlive(pid) {
		os.Remove(pidFile)
		return 0, false
	}
	return pid, true
}

// installSystemdService creates and enables a systemd user service for yaver serve.
func installSystemdService() {
	if isWSL() {
		fmt.Println("WSL detected.")
		fmt.Println("Systemd user service install is not available in WSL.")
		fmt.Println("Installing the Yaver WSL startup helper instead.")
		exePath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		workDir, _ := os.Getwd()
		msg, err := installAutoStartWSL(exePath, workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		fmt.Println(msg)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Find yaver binary path
	yaverBin, err := os.Executable()
	if err != nil {
		yaverBin = "yaver" // fallback to PATH
	}

	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	servicePath := filepath.Join(serviceDir, "yaver.service")

	unit := fmt.Sprintf(`[Unit]
Description=Yaver Agent — AI coding from your phone
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s serve --debug --work-dir %s
Restart=on-failure
RestartSec=5
WorkingDirectory=%s
Environment=HOME=%s

[Install]
WantedBy=default.target
`, yaverBin, home, home, home)

	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating systemd dir: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(servicePath, []byte(unit), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing service file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created: %s\n", servicePath)
	fmt.Println()

	// Try to enable and start
	cmds := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "yaver"},
		{"systemctl", "--user", "start", "yaver"},
	}
	for _, c := range cmds {
		cmd := osexec.Command(c[0], c[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("Note: '%s' failed (systemd may not be available on this OS).\n", strings.Join(c, " "))
			fmt.Println("You can manually enable with:")
			fmt.Printf("  systemctl --user daemon-reload\n")
			fmt.Printf("  systemctl --user enable --now yaver\n")
			return
		}
	}

	// Enable linger so the user-mode systemd manager keeps running
	// after the user logs out (or never logs in at all — headless
	// servers, SSH-only boxes). Without this the agent dies on
	// session end and `yaver primary status` reports "offline" the
	// next time someone connects. Best-effort: linger usually needs
	// root to enable for OTHER users, but a user can always enable
	// it for themselves; failure surfaces as a clear hint instead of
	// a silent regression.
	user := strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		if u, err := osuser.Current(); err == nil {
			user = strings.TrimSpace(u.Username)
		}
	}
	lingerOK := false
	if user != "" {
		lingerCmd := osexec.Command("loginctl", "enable-linger", user)
		if err := lingerCmd.Run(); err == nil {
			lingerOK = true
		}
	}

	fmt.Println()
	fmt.Println("Yaver agent installed as systemd user service.")
	fmt.Println("  Status:  systemctl --user status yaver")
	fmt.Println("  Logs:    journalctl --user -u yaver -f")
	fmt.Println("  Stop:    systemctl --user stop yaver")
	fmt.Println("  Disable: systemctl --user disable yaver")
	fmt.Println()
	if lingerOK {
		fmt.Println("Linger enabled — the agent survives logout and reboots automatically.")
	} else if user != "" {
		fmt.Printf("Could not enable linger automatically. Run once to keep the agent up after logout:\n  sudo loginctl enable-linger %s\n", user)
	}
	fmt.Println("Auth token is persisted in ~/.yaver/config.json (run 'yaver auth' once).")
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	httpPort := fs.Int("port", 18080, "HTTP server port")
	quicPort := fs.Int("quic-port", 4433, "QUIC server port (legacy)")
	workDir := fs.String("work-dir", ".", "Working directory for tasks")
	noQUIC := fs.Bool("no-quic", false, "Disable QUIC server (HTTP only)")
	noRelay := fs.Bool("no-relay", false, "Disable relay tunnel (direct only)")
	waitForSession := fs.Bool("wait-for-session", false, "Wait for other Claude Code sessions to finish before starting tasks")
	debug := fs.Bool("debug", false, "Run in foreground with verbose logging")
	dummy := fs.Bool("dummy", false, "Use dummy runner (fake responses for network testing)")
	relayPassword := fs.String("relay-password", "", "Password for relay server authentication")
	vaultPass := fs.String("vault-passphrase", "", "Custom vault passphrase (default: derived from auth token)")
	multiUser := fs.Bool("multi-user", false, "Enable multi-user mode (shared machines)")
	teamID := fs.String("team", "", "Restrict access to team members (requires --multi-user)")
	maxUsers := fs.Int("max-users", 0, "Max concurrent users in multi-user mode (0 = unlimited)")
	allowIPs := fs.String("allow-ips", "", "IP allowlist: comma-separated CIDRs (e.g. 192.168.1.0/24). Applies to every request, including anonymous probes.")
	allowGuestIPs := fs.String("allow-guest-ips", "", "Extra CIDRs admitted only when the request carries a bearer token. Use when --allow-ips is LAN-scoped but guests arrive over relay/Tailscale/Cloudflare (e.g. --allow-guest-ips=0.0.0.0/0,::/0).")
	recoveryPolicy := fs.String("recovery-policy", "", "Recovery ingress policy: open (default) or private. 'private' blocks /auth/recover on direct public HTTP and allows only LAN/loopback, Tailscale, private relay, or HTTPS Cloudflare Tunnel.")
	tlsPort := fs.Int("tls-port", 18443, "HTTPS server port (0 to disable)")
	noTLS := fs.Bool("no-tls", false, "Disable HTTPS server")
	installSystemd := fs.Bool("install-systemd", false, "Install and enable systemd user service, then exit")
	installLaunchdDaemon := fs.Bool("install-launchd-daemon", false, "Install a macOS LaunchDaemon for boot-before-login headless start, then exit")
	noAutopilot := fs.Bool("no-autopilot", false, "Disable auto-driving mode (enabled by default)")
	iosInstall := fs.String("ios-install", "", "iOS install method: auto (default), native (xcodebuild+xcrun), bundle (Hermes push)")
	containerizeGuests := fs.Bool("containerize-guests", false, "Run guest tasks inside Docker containers (requires yaver-sandbox image)")
	containerizeHost := fs.Bool("containerize-host", false, "Run host tasks inside Docker containers (requires yaver-sandbox image)")
	// Phase 5 — opt this agent in as a remote-mac builder for paired
	// Linux dev boxes. Empty (default) means "not a builder", which
	// is the correct posture for almost every host. Typical Mac
	// usage: `yaver serve --builder-platforms=ios` so a paired Linux
	// box can dispatch Swift / iOS Simulator sessions here.
	builderPlatformsArg := fs.String("builder-platforms", "", "Advertise this agent as a builder for the listed platforms (e.g. ios,macos). Empty = not a builder.")
	containerNetwork := fs.String("container-network", "", "Container network mode: host (default), bridge, none")
	containerReadOnly := fs.Bool("container-read-only", false, "Read-only container root filesystem (only /workspace and /tmp writable)")
	fs.Parse(args)

	// Install systemd service and exit
	if *installSystemd {
		installSystemdService()
		return
	}
	if *installLaunchdDaemon {
		installLaunchdDaemonService()
		return
	}

	// Builder role advertisement is process-local — set once here,
	// read on every /info request. SetBuilderPlatforms lives in
	// remote_builder.go (parallel session, not yet on main). Re-enable
	// the call once that file is committed. Until then we just consume
	// the flag value to avoid an "unused variable" failure.
	_ = builderPlatformsArg

	if *workDir == "." {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatalf("get working directory: %v", err)
		}
		*workDir = wd
	}

	maybeRunMacOSPermissionOnboarding("serve")
	maybeRunHostSharePrepareOnboarding("serve")
	maybeRunWSL2NetworkTuning()

	cfgForReuse, _ := LoadConfig()
	authTokenForReuse := ""
	if cfgForReuse != nil {
		authTokenForReuse = cfgForReuse.AuthToken
	}

	// Primary local agent port should be reused instead of blindly forking
	// another daemon that collides on the same control-plane sockets.
	if !*debug && shouldTrackPrimaryAgent(*httpPort) {
		if health := probeLocalAgentHealthInfo(*httpPort); health != nil {
			refreshExistingLocalAgent(authTokenForReuse, *httpPort, *workDir)
			fmt.Printf("Yaver agent already running on :%d", *httpPort)
			if health.Version != "" {
				fmt.Printf(" (v%s)", health.Version)
			}
			fmt.Println(". Reusing the live process.")
			return
		}
		if pid, running := isAgentRunning(); running {
			fmt.Printf("Restarting stale Yaver agent (stopping PID %d)...\n", pid)
			runStop()
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Auto-register as a system service (LaunchAgent / systemd
	// user unit / Windows scheduled task) on the very first
	// `yaver serve`. Done BEFORE the bootstrap branch so that a
	// brand-new install with no token still gets the always-up
	// guarantee — on the next reboot the OS launches us in
	// bootstrap mode automatically and the phone can pair us
	// without anyone touching the box.
	if execPath, execErr := os.Executable(); execErr == nil {
		if msg := ensureAutoStart(execPath, *workDir); msg != "" {
			fmt.Printf("  %s\n", msg)
		}
	}

	// Bootstrap mode: if we have no token yet, don't exit with
	// "Not signed in" — run a minimal pairing HTTP server so the
	// user can push a token from their phone. See auth_bootstrap.go.
	//
	// In non-debug mode we fork the bootstrap server into the
	// background just like the authed serve branch does, so the
	// user can close the terminal and the box stays reachable.
	bootstrapCfg, bootstrapErr := LoadConfig()
	if needsBootstrap(bootstrapCfg, bootstrapErr) {
		if !*debug {
			if forkBootstrapToBackground(*httpPort, *workDir) {
				return
			}
			// Fall through to foreground bootstrap if fork fails.
		}
		runBootstrapServe(*httpPort)
		return
	}

	cfg := mustLoadAuthConfig()
	switch strings.ToLower(strings.TrimSpace(*recoveryPolicy)) {
	case "":
	case "open":
		cfg.RequirePrivateRecoveryTransport = false
	case "private":
		cfg.RequirePrivateRecoveryTransport = true
	default:
		log.Fatalf("invalid --recovery-policy=%q (expected open or private)", *recoveryPolicy)
	}

	// Open the encrypted vault NOW, before anything that can rotate
	// cfg.AuthToken (RefreshToken below, RegisterDevice in
	// post-bootstrap, MCP authLogin, etc.). The on-disk vault is
	// keyed off whichever token was current the LAST time the vault
	// was successfully written; if we let the token rotate first,
	// the disk encryption key (T_disk) stops matching cfg.AuthToken
	// (T_new) and we lose the only token chain that could have
	// recovered the vault. Loading early lands the runtime store +
	// rekeyVaultBetweenTokens fix at the right moment: every later
	// SetAuthToken in this process can rekey the live store in place.
	if vs, err := tryOpenAgentVault(cfg, *vaultPass); err != nil {
		log.Printf("Warning: vault unavailable on early boot: %v — will retry after token validation", err)
	} else {
		setRuntimeVaultStore(vs)
		log.Printf("Vault unlocked early (%d entries) — runtime store now tracks rotations.", len(vs.List("*")))
	}

	// Check for auto-update before forking
	checkAutoUpdate(cfg)

	// Multi-source install reconciler: report-only at startup. If the
	// box has a stale apt/brew/npm `yaver` shadowing the auto-updated
	// one in ~/.yaver/bin/<v>/, log a drift warning so the operator
	// knows about it without us silently rewriting their package
	// manager's files. See self_heal.go.
	runSelfHealOnStartup()

	// Validate token before forking — try refresh if expired, but never exit
	if _, err := ValidateTokenUser(cfg.ConvexSiteURL, cfg.AuthToken); err != nil {
		// Try refreshing the token first — the backend may rotate the
		// token as part of refresh; if so we must persist the new one.
		if newToken, refreshErr := RefreshToken(cfg.ConvexSiteURL, cfg.AuthToken); refreshErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: token validation failed (%v). The agent will start but the device may appear offline.\n", err)
			fmt.Fprintf(os.Stderr, "Run 'yaver auth' to re-authenticate. The agent will NOT sign you out automatically.\n")
			// Continue anyway — the heartbeat loop will keep retrying and the user can re-auth
		} else {
			if err := persistRotatedAuthToken(cfg, newToken); err != nil {
				log.Printf("[auth] (warn) could not persist rotated token: %v", err)
			}
			fmt.Println("Token refreshed successfully.")
		}
	}

	// If not debug mode, fork into background
	if !*debug {
		// Re-exec ourselves with an internal flag
		execPath, err := os.Executable()
		if err != nil {
			log.Fatalf("cannot find executable: %v", err)
		}

		logFile := logFilePath()
		lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("cannot open log file: %v", err)
		}

		// Build args for the child process
		childArgs := []string{"serve", "--debug"}
		childArgs = append(childArgs, fmt.Sprintf("--port=%d", *httpPort))
		childArgs = append(childArgs, fmt.Sprintf("--quic-port=%d", *quicPort))
		childArgs = append(childArgs, fmt.Sprintf("--work-dir=%s", *workDir))
		if *noQUIC {
			childArgs = append(childArgs, "--no-quic")
		}
		if *noRelay {
			childArgs = append(childArgs, "--no-relay")
		}
		if *waitForSession {
			childArgs = append(childArgs, "--wait-for-session")
		}
		if *dummy {
			childArgs = append(childArgs, "--dummy")
		}
		if *relayPassword != "" {
			childArgs = append(childArgs, fmt.Sprintf("--relay-password=%s", *relayPassword))
		}
		if *vaultPass != "" {
			childArgs = append(childArgs, fmt.Sprintf("--vault-passphrase=%s", *vaultPass))
		}
		if *allowIPs != "" {
			childArgs = append(childArgs, fmt.Sprintf("--allow-ips=%s", *allowIPs))
		}
		if *allowGuestIPs != "" {
			childArgs = append(childArgs, fmt.Sprintf("--allow-guest-ips=%s", *allowGuestIPs))
		}
		if strings.TrimSpace(*recoveryPolicy) != "" {
			childArgs = append(childArgs, fmt.Sprintf("--recovery-policy=%s", strings.TrimSpace(*recoveryPolicy)))
		}
		if *noTLS {
			childArgs = append(childArgs, "--no-tls")
		} else {
			childArgs = append(childArgs, fmt.Sprintf("--tls-port=%d", *tlsPort))
		}
		if *noAutopilot {
			childArgs = append(childArgs, "--no-autopilot")
		}
		if *iosInstall != "" {
			childArgs = append(childArgs, fmt.Sprintf("--ios-install=%s", *iosInstall))
		}
		if *containerizeGuests {
			childArgs = append(childArgs, "--containerize-guests")
		}
		if *containerizeHost {
			childArgs = append(childArgs, "--containerize-host")
		}
		if *containerNetwork != "" {
			childArgs = append(childArgs, fmt.Sprintf("--container-network=%s", *containerNetwork))
		}
		if *containerReadOnly {
			childArgs = append(childArgs, "--container-read-only")
		}

		cmd := osexec.Command(execPath, childArgs...)
		cmd.Stdout = lf
		cmd.Stderr = lf
		cmd.Dir = *workDir
		// Detach from parent (platform-specific)
		detachProcess(cmd)

		if err := cmd.Start(); err != nil {
			log.Fatalf("failed to start agent: %v", err)
		}

		// Write PID file
		if shouldTrackPrimaryAgent(*httpPort) {
			if err := os.WriteFile(pidFilePath(), []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
				log.Printf("warning: could not write PID file: %v", err)
			}
		}

		lf.Close()

		fmt.Printf("Yaver agent started (PID %d).\n", cmd.Process.Pid)
		fmt.Println()

		// Auto-register as system service (launchd/systemd/schtasks)
		if msg := ensureAutoStart(execPath, *workDir); msg != "" {
			fmt.Printf("  %s\n", msg)
		}

		// Auto-configure MCP for detected editors
		autoSetupMCP()

		fmt.Println("  yaver logs      View agent logs")
		fmt.Println("  yaver stop      Stop the agent")
		fmt.Println("  yaver status    Check agent status")
		return
	}

	// Debug mode: run in foreground with full logging
	log.Println("Yaver agent starting...")
	stopKeepAwake := startHeadlessKeepAwake(cfg)
	if stopKeepAwake != nil {
		defer stopKeepAwake()
	}

	// Note: we no longer kill other Claude processes on startup.
	// Users may have active Claude Code sessions we shouldn't disrupt.

	log.Printf("  Work dir: %s", *workDir)
	log.Printf("  HTTP port: %d", *httpPort)
	if !*noQUIC {
		log.Printf("  QUIC port: %d", *quicPort)
	}

	// Probe gh + glab once at boot. Used by /info, the runner-task
	// preamble (so Claude/Codex see "gh + glab installed and authed"
	// without having to discover it themselves), and MCP wrappers
	// (gh_run, glab_run, github_pr_create, …) for fast install/auth
	// preflight. Cached for 10 minutes; install_cmd refreshes after
	// successful install. Logged here so the boot banner shows the
	// posture every time, matching how `yaver doctor` reports it.
	for name, cli := range DetectGitProviderCLIs() {
		switch {
		case !cli.Available:
			log.Printf("  %s CLI: not on PATH — install with `yaver install %s`", name, name)
		case !cli.Authed:
			log.Printf("  %s CLI: %s (%s) — NOT authenticated; run `%s auth login`", name, cli.Path, cli.Version, name)
		default:
			who := cli.AuthUser
			if who == "" {
				who = "(user unknown)"
			}
			log.Printf("  %s CLI: %s (%s) — authed as %s on %s", name, cli.Path, cli.Version, who, cli.AuthHost)
		}
	}

	// Ensure stable device ID
	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.New().String()
		log.Printf("Generated device ID: %s", cfg.DeviceID)
	}
	if err := SaveConfig(cfg); err != nil {
		log.Fatalf("save config: %v", err)
	}

	// Get owner userId and email for multi-token auth and dev logging
	var ownerUserID, ownerEmail string
	offlineMode := false
	ownerInfo, err := ValidateTokenInfo(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		log.Printf("Warning: token validation failed (%v) — starting in offline mode", err)
		log.Printf("Run 'yaver auth' to re-authenticate. Local features (dev server, tasks) still work.")
		offlineMode = true
		ownerUserID = "offline"
		ownerEmail = "offline"
	} else {
		ownerUserID = ownerInfo.UserID
		ownerEmail = ownerInfo.Email
		log.Printf("Token validated. Owner: %s (%s)", ownerUserID, ownerEmail)
	}

	// Register device
	hostname, _ := os.Hostname()
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	localIP := getLocalIP()

	// Load or generate device X25519 keypair for encrypted pairing.
	deviceKeys, keysErr := LoadOrGenerateKeys()
	if keysErr != nil {
		log.Printf("Warning: could not load device keys: %v — encrypted pairing will be unavailable", keysErr)
	}
	var devicePubKey string
	if deviceKeys != nil {
		devicePubKey = deviceKeys.PublicKeyBase64()
	}

	if !offlineMode {
		publicEndpoints := publicEndpointsWithAutoIP(cfg, *httpPort)
		recoveryPosture := computeRecoveryTransportPosture(cfg)
		if len(publicEndpoints) > 0 {
			log.Printf("[reachability] publishing %d publicEndpoint(s): %v", len(publicEndpoints), publicEndpoints)
		} else {
			log.Printf("[reachability] no publicEndpoints — only LAN beacon + relay-assigned URL will be reachable")
		}
		log.Printf("Registering device %s (%s) at %s:%d...", hostname, cfg.DeviceID, localIP, *httpPort)
		if rotatedToken, err := RegisterDevice(cfg.ConvexSiteURL, RegisterDeviceRequest{
			Token:           cfg.AuthToken,
			DeviceID:        cfg.DeviceID,
			Name:            hostname,
			Platform:        platform,
			PublicKey:       devicePubKey,
			QuicHost:        localIP,
			QuicPort:        *httpPort,
			PublicEndpoints: publicEndpoints,
			HardwareID:      HardwareID(),
			HardwareProfile: cachedHardwareProfile(),
			RecoveryPosture: &recoveryPosture,
			AgentVersion:    version,
		}); err != nil {
			if strings.Contains(err.Error(), "belongs to another user") {
				log.Printf("Device ID conflict — generating new device ID")
				cfg.DeviceID = uuid.New().String()
				if saveErr := SaveConfig(cfg); saveErr != nil {
					log.Fatalf("save config after device ID reset: %v", saveErr)
				}
				if rotatedToken2, err2 := RegisterDevice(cfg.ConvexSiteURL, RegisterDeviceRequest{
					Token:           cfg.AuthToken,
					DeviceID:        cfg.DeviceID,
					Name:            hostname,
					Platform:        platform,
					PublicKey:       devicePubKey,
					QuicHost:        localIP,
					QuicPort:        *httpPort,
					PublicEndpoints: publicEndpoints,
					HardwareID:      HardwareID(),
					HardwareProfile: cachedHardwareProfile(),
					RecoveryPosture: &recoveryPosture,
					AgentVersion:    version,
				}); err2 != nil {
					log.Printf("Warning: device registration failed: %v", err2)
					offlineMode = true
				} else if rotatedToken2 != "" && rotatedToken2 != cfg.AuthToken {
					if saveErr := SetAuthToken(cfg, rotatedToken2); saveErr != nil {
						log.Printf("Warning: could not persist dedicated device session: %v", saveErr)
					}
				}
			} else {
				log.Printf("Warning: device registration failed: %v", err)
				offlineMode = true
			}
		} else if rotatedToken != "" && rotatedToken != cfg.AuthToken {
			if saveErr := SetAuthToken(cfg, rotatedToken); saveErr != nil {
				log.Printf("Warning: could not persist dedicated device session: %v", saveErr)
			}
		}
		if !offlineMode {
			log.Println("Device registered.")
		}
	} else {
		log.Printf("Skipping device registration (offline mode)")
	}

	// Fetch platform config (relay servers, runners, models) from Convex
	var relayServers []RelayServerInfo
	// relayPasswords maps relay QuicAddr to password for per-relay auth
	relayPasswords := make(map[string]string)

	// Determine relay password: --relay-password flag > config.relay_password
	effectiveRelayPassword := *relayPassword
	if effectiveRelayPassword == "" {
		if cfg.RelayPassword != "" {
			effectiveRelayPassword = cfg.RelayPassword
		} else {
			effectiveRelayPassword = cfg.CachedRelayPassword
		}
	}

	if !*noRelay && len(cfg.RelayServers) > 0 {
		// Use relay servers from config.json (highest priority)
		log.Printf("Using %d relay server(s) from config.json:", len(cfg.RelayServers))
		relayServers, relayPasswords = relayInfosFromConfig(cfg.RelayServers)
		for _, rs := range relayServers {
			log.Printf("  [%s] %s (%s)", rs.ID, rs.QuicAddr, rs.Region)
		}
	} else if !*noRelay && len(cfg.CachedRelayServers) > 0 {
		relayServers, relayPasswords = relayInfosFromConfig(cfg.CachedRelayServers)
		if len(relayServers) == 0 {
			log.Printf("Ignoring %d cached relay server(s) without QUIC addresses", len(cfg.CachedRelayServers))
		} else {
			log.Printf("Using %d cached relay server(s):", len(relayServers))
			for _, rs := range relayServers {
				log.Printf("  [%s] %s (%s)", rs.ID, rs.QuicAddr, rs.Region)
			}
		}
	}

	// Fetch user settings from Convex (relay config, runner, etc.)
	userSettings, userSettingsErr := FetchUserSettings(cfg.ConvexSiteURL, cfg.AuthToken)
	if userSettingsErr != nil {
		log.Printf("Warning: could not fetch user settings: %v", userSettingsErr)
	} else if userSettings.RelayPassword != "" {
		effectiveRelayPassword = userSettings.RelayPassword
	}

	// Fetch platform config first so we can match user's relayUrl to a full relay entry
	platformCfg, platformErr := FetchPlatformConfig(cfg.ConvexSiteURL)
	if platformErr != nil {
		log.Printf("Warning: could not fetch platform config: %v", platformErr)
	}

	// Build relay server list: config.json > user settings matched with platform > platform config
	if !*noRelay && len(relayServers) == 0 && userSettingsErr == nil && userSettings.RelayUrl != "" && platformErr == nil {
		// Match user's relayUrl against platform config to get full relay info (incl. QUIC address)
		for _, rs := range platformCfg.RelayServers {
			if relayHTTPURLsMatch(rs.HttpURL, userSettings.RelayUrl) {
				relayServers = append(relayServers, rs)
				log.Printf("Using relay from user settings: %s (QUIC: %s)", rs.HttpURL, rs.QuicAddr)
				break
			}
		}
		if len(relayServers) == 0 {
			// Phase 2D — the user's managed-cloud box doubles as their
			// own relay (cloudMachines.provision Phase 2C wires
			// userSettings → the box's hostname). It's per-user, never
			// in platformConfig. Synth a RelayServerInfo from the URL
			// + pair with userSettings.RelayPassword so this device
			// prefers the user's own relay over the shared free list.
			// On synth failure (garbage URL) we still fall back to the
			// platform list below — never worse than today.
			if synth, ok := synthRelayServerInfoFromURL(userSettings.RelayUrl); ok {
				relayServers = append(relayServers, synth)
				if userSettings.RelayPassword != "" {
					relayPasswords[synth.QuicAddr] = userSettings.RelayPassword
				}
				log.Printf("Using user's managed relay (synthesised from settings): %s → QUIC %s", synth.HttpURL, synth.QuicAddr)
			} else {
				log.Printf("User relay setting %s could not be parsed; falling back to platform relays", userSettings.RelayUrl)
			}
		}
	}

	if platformErr == nil {
		// Populate relay servers from platform config if not already set
		if !*noRelay && len(relayServers) == 0 {
			relayServers = platformCfg.RelayServers
			if len(relayServers) > 0 {
				log.Printf("Found %d relay server(s) from Convex:", len(relayServers))
				for _, rs := range relayServers {
					log.Printf("  [%s] %s (%s)", rs.ID, rs.QuicAddr, rs.Region)
				}
			} else {
				log.Println("No relay servers configured.")
			}
		}
		// Populate runners from Convex (overrides hardcoded builtinRunners)
		if len(platformCfg.Runners) > 0 {
			log.Printf("Loaded %d runner(s) from Convex", len(platformCfg.Runners))
			LoadRunnersFromBackend(platformCfg.Runners)
		}
		// Cache models for the /agent/runners endpoint
		if len(platformCfg.Models) > 0 {
			log.Printf("Loaded %d model(s) from Convex", len(platformCfg.Models))
			LoadModelsFromBackend(platformCfg.Models)
		}
	}
	if !*noRelay && len(relayServers) > 0 {
		cacheResolvedRelayConfig(cfg, relayServers, effectiveRelayPassword, relayPasswords)
	}

	// Write PID file (for debug mode too, so stop/status work)
	if shouldTrackPrimaryAgent(*httpPort) {
		if err := os.WriteFile(pidFilePath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
			log.Printf("warning: could not write PID file: %v", err)
		}
	}

	// Resolve runner config (fetch user settings, fall back to auto-detect)
	runner := resolveRunner(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID)

	// If no runner was explicitly set by user, auto-detect available agents
	if runner.AutoDetected {
		// Scan all known agents to find what's installed
		agentSearch := []struct{ id, cmd, name string }{
			{"claude", "claude", "Claude Code"},
			{"codex", "codex", "OpenAI Codex"},
			{"aider", "aider", "Aider"},
			{"ollama", "ollama", "Ollama"},
			{"opencode", "opencode", "OpenCode"},
			{"goose", "goose", "Goose"},
			{"amp", "amp", "Amp"},
			{"continue", "continue", "Continue"},
		}

		type detectedAgent struct {
			id, cmd, name, path string
			status              RunnerRuntimeStatus
		}
		var available []detectedAgent
		var unavailable []detectedAgent
		for _, a := range agentSearch {
			var agentPath string
			if p, err := osexec.LookPath(a.cmd); err == nil {
				agentPath = p
			} else if p := findInExpandedPath(a.cmd); p != "" {
				agentPath = p
			}
			if agentPath != "" {
				status := DetectRunnerRuntimeStatus(GetRunnerConfig(a.id), *workDir)
				detected := detectedAgent{id: a.id, cmd: a.cmd, name: a.name, path: agentPath, status: status}
				if status.Ready {
					available = append(available, detected)
				} else {
					unavailable = append(unavailable, detected)
				}
			}
		}

		if len(available) == 0 && len(unavailable) == 0 {
			log.Printf("WARNING: No AI agent found. Install one to run tasks.")
			log.Printf("  Claude Code: https://docs.anthropic.com/en/docs/claude-code")
			log.Printf("  OpenAI Codex: https://github.com/openai/codex")
			log.Printf("  Aider: https://aider.chat")
			log.Printf("  Ollama: https://ollama.com")
			log.Printf("  Or set a custom command: yaver set-runner custom \"your-command {prompt}\"")
			log.Printf("Agent will start but tasks will fail until an AI agent is available.")
		} else if len(available) == 0 {
			log.Printf("WARNING: AI agents are installed, but none are ready to run tasks yet.")
			for _, a := range unavailable {
				msg := strings.TrimSpace(a.status.Error)
				if msg == "" {
					msg = "installed, but not ready"
				}
				log.Printf("  %s (%s): %s", a.name, a.path, msg)
			}
			log.Printf("Available agents were detected, but none appears to be logged in or configured yet.")
			log.Printf("Agent will start but tasks will fail until one runner is authenticated/configured.")
		} else if len(available) == 1 {
			// Only one agent found — use it automatically
			a := available[0]
			log.Printf("Runner: auto-detected %s at %s", a.name, a.path)
			if r, err := fetchRunner(&http.Client{Timeout: 5 * time.Second}, cfg.ConvexSiteURL, a.id); err == nil {
				runner = r
			}
		} else {
			// Multiple agents found — ask user to pick (interactive, like Vercel)
			fmt.Println()
			fmt.Println("Multiple AI agents detected on your machine:")
			fmt.Println()
			for i, a := range available {
				fmt.Printf("  %d. %s  (%s)\n", i+1, a.name, a.path)
			}
			if len(unavailable) > 0 {
				fmt.Println()
				fmt.Println("Installed but not ready:")
				fmt.Println()
				for _, a := range unavailable {
					msg := strings.TrimSpace(a.status.Error)
					if msg == "" {
						msg = "installed, but not ready"
					}
					fmt.Printf("  - %s  (%s) — %s\n", a.name, a.path, msg)
				}
			}
			fmt.Println()
			fmt.Printf("Select your default agent [1-%d]: ", len(available))

			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			choice := 0
			if n, err := fmt.Sscanf(input, "%d", &choice); n == 1 && err == nil && choice >= 1 && choice <= len(available) {
				a := available[choice-1]
				log.Printf("Runner: selected %s at %s", a.name, a.path)
				if r, err := fetchRunner(&http.Client{Timeout: 5 * time.Second}, cfg.ConvexSiteURL, a.id); err == nil {
					runner = r
				}
				// Save choice to Convex so it persists
				go func() {
					payload := map[string]string{"runnerId": a.id}
					body, _ := json.Marshal(payload)
					req, _ := http.NewRequest("POST", cfg.ConvexSiteURL+"/settings", bytes.NewReader(body))
					req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
					req.Header.Set("Content-Type", "application/json")
					resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
					if err == nil {
						resp.Body.Close()
						log.Printf("Runner preference saved to account: %s", a.id)
					}
				}()
			} else {
				// Invalid input — use first detected
				a := available[0]
				log.Printf("Runner: defaulting to %s at %s", a.name, a.path)
				if r, err := fetchRunner(&http.Client{Timeout: 5 * time.Second}, cfg.ConvexSiteURL, a.id); err == nil {
					runner = r
				}
			}
		}
	}
	log.Printf("Runner: %s (command=%s, mode=%s)", runner.Name, runner.Command, runner.OutputMode)

	// Refresh machine-wide project discovery on every serve start so the
	// agent sees repos across home/workspace roots regardless of which repo
	// the user launched `yaver serve` from.
	log.Printf("Refreshing local projects cache (stored in ~/.yaver/PROJECTS.md, never uploaded)...")
	go discoverProjects()

	// Scan mobile projects + pre-build dev clients for Expo/RN (background)
	go PrewarmMobileProjects()

	// Clean old session files (>7 days)
	go cleanOldSessions()

	// Task store and manager
	taskStore, err := NewTaskStore()
	if err != nil {
		log.Fatalf("failed to create task store: %v", err)
	}
	taskMgr := NewTaskManager(*workDir, taskStore, runner)
	taskMgr.WaitForSlot = *waitForSession
	taskMgr.DummyMode = *dummy
	if *dummy {
		log.Println("DUMMY MODE enabled — tasks will return fake responses (no real runner)")
	}
	taskMgr.ConvexURL = cfg.ConvexSiteURL
	taskMgr.AuthToken = cfg.AuthToken
	taskMgr.DeviceID = cfg.DeviceID
	taskMgr.OwnerEmail = ownerEmail

	// Configure sandbox — defaults to enabled with secure settings
	if cfg.Sandbox != nil {
		taskMgr.Sandbox = *cfg.Sandbox
	} else {
		taskMgr.Sandbox = DefaultSandboxConfig()
	}
	log.Printf("Sandbox: enabled=%v, allow_sudo=%v", taskMgr.Sandbox.Enabled, taskMgr.Sandbox.AllowSudo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize tmux manager if tmux is available
	taskMgr.TmuxMgr = NewTmuxManager(taskMgr)
	if taskMgr.TmuxMgr != nil {
		log.Println("Tmux: available — session adoption enabled")
		taskMgr.TmuxMgr.ReAdoptOnStartup()
	} else {
		log.Println("Tmux: not available — session adoption disabled")
	}

	// heartbeatLoop is started after httpServer is created (needs authExpired flag)
	go metricsLoop(ctx, cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID)

	// Periodic auto-update check (every 6 hours when idle)
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if cfg.AutoUpdate && taskMgr.GetRunningTaskCount() == 0 {
					log.Println("[auto-update] Periodic check (agent idle)...")
					checkAutoUpdate(cfg)
				}
			}
		}
	}()

	// Warm up the runner — fork Claude at startup to establish a session
	go taskMgr.WarmUp()

	// Initialize ACL manager for MCP peer communication
	var aclMgr *ACLManager
	if len(cfg.ACLPeers) > 0 {
		aclMgr = NewACLManager(cfg.ACLPeers)
		log.Printf("ACL: %d peer(s) configured", len(cfg.ACLPeers))
	} else {
		aclMgr = NewACLManager(nil)
	}

	// Initialize email manager if configured
	var emailMgr *EmailManager
	if cfg.Email != nil && cfg.Email.Provider != "" {
		var err error
		emailMgr, err = NewEmailManager(cfg.Email)
		if err != nil {
			log.Printf("Warning: email setup failed: %v", err)
		} else {
			log.Printf("Email: %s (%s) configured", cfg.Email.Provider, cfg.Email.SenderEmail)
		}
	}

	// Report agent started event
	go func() {
		if err := ReportDeviceEvent(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID, "started", fmt.Sprintf("yaver %s", version)); err != nil {
			log.Printf("[event] Failed to report start: %v", err)
		}
	}()

	// Start HTTP server (V1 — primary, also serves MCP)
	httpServer := NewHTTPServer(*httpPort, cfg.AuthToken, ownerUserID, cfg.DeviceID, cfg.ConvexSiteURL, hostname, taskMgr)
	httpServer.agentGraphMgr = NewAgentGraphManager(taskMgr)
	globalAgentGraphMgr = httpServer.agentGraphMgr

	// Start heartbeat loop (needs httpServer for authExpired flag)
	go heartbeatLoop(ctx, cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID, taskMgr, httpServer)

	// Warm the public package registry cache so /install/list is rich
	// from the first request. The cache refreshes itself every 6h via
	// PackageRegistry(), so we only need to kick it once here.
	go func() {
		warmupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_ = RefreshPackageRegistry(warmupCtx, cfg.ConvexSiteURL)
	}()

	// iOS install method (from flag or config, default "auto")
	iosMethod := *iosInstall
	if iosMethod == "" && cfg.IOSInstallMethod != "" {
		iosMethod = cfg.IOSInstallMethod
	}
	if iosMethod == "" {
		iosMethod = IOSInstallAuto
	}
	httpServer.iosInstallMethod = iosMethod
	resolvedIOSMethod, resolvedIOSReason := resolveIOSInstallMethodWithReason(iosMethod)
	log.Printf("iOS install method: %s (resolved: %s, reason: %s)", iosMethod, resolvedIOSMethod, resolvedIOSReason)

	// IP allowlist (from flag or config)
	allowIPsList := *allowIPs
	if allowIPsList == "" && len(cfg.AllowedIPs) > 0 {
		allowIPsList = strings.Join(cfg.AllowedIPs, ",")
	}
	if allowIPsList != "" {
		httpServer.allowedCIDRs = parseCIDRs(strings.Split(allowIPsList, ","))
	}
	// Guest IP allowlist — widens the gate for bearer-carrying
	// requests only. Typical use: --allow-ips="192.168.0.0/16"
	// (owner on LAN) + --allow-guest-ips="0.0.0.0/0,::/0" (guests
	// from anywhere, authenticated by token).
	allowGuestIPsList := *allowGuestIPs
	if allowGuestIPsList == "" && len(cfg.AllowedGuestIPs) > 0 {
		allowGuestIPsList = strings.Join(cfg.AllowedGuestIPs, ",")
	}
	if allowGuestIPsList != "" {
		httpServer.allowedGuestCIDRs = parseCIDRs(strings.Split(allowGuestIPsList, ","))
	}

	// TLS setup
	if !*noTLS {
		cert, fingerprint, err := EnsureTLSCert()
		if err != nil {
			log.Printf("Warning: TLS disabled — %v", err)
		} else {
			httpServer.tlsCert = cert
			httpServer.tlsFingerprint = fingerprint
			httpServer.tlsPort = *tlsPort
			log.Printf("TLS cert ready (fingerprint: %s...)", fingerprint[:16])
		}
	}

	httpServer.execMgr = NewExecManager(taskMgr.workDir, cfg.Sandbox)
	httpServer.scheduler = NewScheduler(taskMgr)
	// Wire the routine ("Verb-mode") dispatcher so scheduled routines
	// can invoke any registered ops verb on this or any peer machine.
	// Caller is "owner" because routine creation requires owner auth at
	// the MCP layer; firing them later is treated as an owner-initiated
	// internal call. Background context — the cron tick has no
	// inbound HTTP request to inherit cancellation from.
	httpServer.scheduler.SetOpsDispatcher(func(req OpsRequest) OpsResult {
		return dispatchOps(OpsContext{
			Ctx:    context.Background(),
			Server: httpServer,
			Caller: "owner",
		}, req)
	})
	// Wire the process-global TaskSupervisor to runServe's context so
	// every SupervisedGo() ticker gets cancelled cleanly on shutdown.
	// Must happen before any Start* call below so their Register hits
	// the real supervisor, not the fallback background-context one.
	sup := initSupervisor(ctx)

	// Beacon file — the external watchdog unit (systemd) reads this
	// to decide "is the agent alive". Supervisor refreshes it every
	// watchdog tick as long as no task is stalled.
	if dir, err := ConfigDir(); err == nil {
		sup.SetBeaconPath(dir + "/last-healthy")
	}

	// In-agent relay-password smoke. Replaces the standalone systemd
	// yaver-smoke-relay-password.timer — now a single-service design.
	// Disabled by default; the Hetzner bootstrap sets
	// YAVER_ENABLE_RELAY_SMOKE=1 so only the test box runs it.
	StartRelayPasswordSmoke()

	// P2P bus — distributed pub/sub for peer presence + live state.
	// Initialised without the user's Convex userId for now; the
	// relay-transport resolver looks up userId from the relay
	// password. Subscribers that need strict user scoping check
	// Publisher against the local device registry.
	b := InitBus(ctx, cfg.DeviceID, "")
	globalLeader = NewLeaderTracker(cfg.DeviceID)
	globalLeader.WireTo(b)

	// Connect every configured relay as a Tier-2 transport. The bus
	// dedupes cross-relay deliveries, so multiple subscriptions are
	// safe and let peer presence survive a single-relay flap.
	// Non-fatal if individual relays are unreachable.
	for _, relay := range cfg.RelayServers {
		if relay.HttpURL == "" {
			continue
		}
		rt := NewRelayBusTransport(relay.HttpURL, relay.Password, cfg.AuthToken, b)
		rt.Start(ctx)
		b.RegisterTransport(rt)
	}

	// Tier-1 LAN transport — UDP broadcast on :19838. Non-fatal on
	// startup error (network permission denied, port in use, etc).
	// YAVER_DISABLE_LAN_BUS=1 skips the listen entirely for hosts
	// that don't want stray UDP chatter.
	if os.Getenv("YAVER_DISABLE_LAN_BUS") != "1" {
		// Single-tenant mode — we don't have userID locally on the
		// agent (it's derived per-request from the bearer). Accept
		// all same-LAN Yaver traffic for now; tighten later when
		// we thread userID through config.
		lt := NewLANBusTransport(b, cfg.DeviceID, "")
		if err := lt.Start(ctx); err != nil {
			log.Printf("[bus-lan] skip: %v", err)
		} else {
			b.RegisterTransport(lt)
		}
	}

	// Periodic + event-driven heartbeat. StartPeerHeartbeat emits
	// one `online` event on boot, then `ping` every minute. The
	// shutdown handler (wired further down) publishes `offline`.
	busHostname, _ := os.Hostname()
	presence := PeerPresence{
		DeviceID: cfg.DeviceID,
		Hostname: busHostname,
		Platform: runtime.GOOS,
		Version:  version,
	}
	StartPeerHeartbeat(ctx, b, presence, 60*time.Second)

	httpServer.scheduler.Start(ctx)
	httpServer.aclMgr = aclMgr
	httpServer.emailMgr = emailMgr
	httpServer.analytics = NewAnalytics()
	httpServer.notifyMgr = NewNotificationManager(cfg.Notifications)
	SetGlobalNotifier(httpServer.notifyMgr)
	StartMetricsSampler(context.Background())
	StartConvexStateSync(context.Background())
	httpServer.buildMgr = NewBuildManager(httpServer.execMgr, taskMgr.workDir)
	httpServer.publishMgr = NewPublishManager(httpServer.execMgr, httpServer.buildMgr, taskMgr.workDir)
	httpServer.tunnelMgr = NewTunnelManager()
	httpServer.testMgr = NewTestManager(httpServer.execMgr, taskMgr.workDir)
	httpServer.qualityMgr = NewQualityManager(httpServer.execMgr, taskMgr.workDir)
	httpServer.qualityMgr.notifyMgr = httpServer.notifyMgr
	log.Printf("Quality gate manager ready")
	if hmMgr, err := NewHealthMonitor(); err != nil {
		log.Printf("Warning: health monitor unavailable: %v", err)
	} else {
		hmMgr.notifyMgr = httpServer.notifyMgr
		httpServer.healthMon = hmMgr
		log.Printf("Health monitor ready")
	}
	if fbMgr, err := NewFeedbackManager(); err != nil {
		log.Printf("Warning: feedback unavailable: %v", err)
	} else {
		httpServer.feedbackMgr = fbMgr
		log.Printf("Feedback manager ready (%d existing reports)", len(fbMgr.ListFeedback()))
	}
	if drMgr, err := NewDesignReferenceManager(); err != nil {
		log.Printf("Warning: design references unavailable: %v", err)
	} else {
		httpServer.designRefMgr = drMgr
		log.Printf("Design reference manager ready (%d existing references)", len(drMgr.List()))
	}
	if cfgDir, err := ConfigDir(); err == nil {
		httpServer.guestConfigMgr = NewGuestConfigManager(cfgDir)
		log.Printf("Guest config manager ready")
	}
	// Container isolation (optional — requires Docker + yaver-sandbox image)
	useContainerGuests := *containerizeGuests || cfg.ContainerizeGuests
	useContainerHost := *containerizeHost || cfg.ContainerizeHost
	// Resolve network mode: CLI flag > config > default "host"
	cNetwork := *containerNetwork
	if cNetwork == "" {
		cNetwork = cfg.ContainerNetwork
	}
	cReadOnly := *containerReadOnly || cfg.ContainerReadOnly
	if useContainerGuests || useContainerHost {
		cr := NewContainerRunner()
		if cr.IsAvailable() {
			httpServer.containerRunner = cr
			httpServer.containerizeGuests = useContainerGuests
			httpServer.containerizeHost = useContainerHost
			// Wire into task manager so tasks can use containers
			taskMgr.ContainerRunner = cr
			taskMgr.ContainerizeGuests = useContainerGuests
			taskMgr.ContainerizeHost = useContainerHost
			taskMgr.ContainerCPU = cfg.ContainerCPU
			taskMgr.ContainerMemory = cfg.ContainerMemory
			taskMgr.ContainerImage = cfg.ContainerImage
			taskMgr.ContainerNetwork = cNetwork
			taskMgr.ContainerReadOnly = cReadOnly
			taskMgr.ContainerMounts = cfg.ContainerMounts
			if cr.IsImageReady() {
				log.Printf("Container sandbox ready (guests=%v, host=%v, network=%s, readOnly=%v)",
					useContainerGuests, useContainerHost, cNetwork, cReadOnly)
			} else {
				log.Printf("Container sandbox enabled but image not built — will auto-build on first task")
			}
		} else {
			log.Printf("Warning: containerization requested but Docker not available — falling back to direct execution")
		}
	}
	if bbMgr, err := NewBlackBoxManager(); err != nil {
		log.Printf("Warning: blackbox unavailable: %v", err)
	} else {
		httpServer.blackboxMgr = bbMgr
		// Register so runner.go's pump can fire <<yaver-action:...>>
		// sentinels (e.g. "reload sfmg") at the paired phone without
		// threading the manager through every Task struct.
		SetActiveBlackboxMgr(bbMgr)
		log.Printf("Black box manager ready")
	}
	httpServer.devServerMgr = NewDevServerManager()
	httpServer.browserMgr = NewBrowserManager()
	httpServer.vibePreviewMgr = NewVibePreviewManager(httpServer.browserMgr)
	// Register as the global accessor so callers can find it without
	// threading a reference through every code path.
	SetActiveVibePreviewManager(httpServer.vibePreviewMgr)
	// Pick the summary backend from YAVER_VIBE_SUMMARIZER (default noop;
	// "claude" enables the CLI vision call when the binary is on PATH).
	SetVibePreviewSummarizer(ResolveDefaultSummarizer())
	// Use relay URL if relay is available (works from 4G/any network).
	// Fall back to local IP for direct/LAN connections.
	if len(relayServers) > 0 && cfg.DeviceID != "" {
		httpServer.devServerMgr.AgentURL = fmt.Sprintf("%s/d/%s",
			strings.TrimRight(relayServers[0].HttpURL, "/"), cfg.DeviceID)
		log.Printf("Dev server proxy URL: %s (relay — works from 4G)", httpServer.devServerMgr.AgentURL)
	} else {
		httpServer.devServerMgr.AgentURL = fmt.Sprintf("http://%s:%d", getLocalIP(), *httpPort)
		log.Printf("Dev server proxy URL: %s (direct — same WiFi only)", httpServer.devServerMgr.AgentURL)
	}
	if tlMgr, err := NewTodoListManager(); err != nil {
		log.Printf("Warning: todolist unavailable: %v", err)
	} else {
		httpServer.todolistMgr = tlMgr
		// Wire auto-consume: items are implemented immediately as they arrive
		tlMgr.SetAutoConsume(true, func(item *TodoItem) {
			httpServer.autoConsumeItem(item)
		})
		log.Printf("Todo list manager ready (%d existing items, auto-consume=on)", len(tlMgr.ListItems()))
		// Autopilot manager — agent-agnostic orchestrator, persists to ~/.yaver/autopilot.json
		// Enabled by default; disable with --no-autopilot
		httpServer.autopilot = NewAutopilotManager(taskMgr, tlMgr)
		if *noAutopilot {
			httpServer.autopilot.Disable()
		}
		log.Printf("Autopilot manager ready (enabled=%v)", httpServer.autopilot.IsEnabled())
	}
	// Vibing pre-warm disabled — was running 5 LLM calls × 3 projects on every startup.
	// Deep Shuffle is now on-demand only (user taps the dice button).
	// Quick actions (no LLM) are generated on first /vibing request.
	if *multiUser {
		muMgr, err := NewMultiUserManager(MultiUserConfig{
			TeamID:   *teamID,
			MaxUsers: *maxUsers,
		})
		if err != nil {
			log.Printf("Warning: multi-user mode unavailable: %v", err)
		} else {
			httpServer.multiUserMgr = muMgr
			log.Printf("Multi-user mode enabled (team=%q, maxUsers=%d, users=%d)", *teamID, *maxUsers, len(muMgr.ListUsers()))
		}
	}

	// Initialize vault (P2P encrypted key store). The early-boot path
	// above (right after mustLoadAuthConfig) already populated the
	// runtime store under whatever token was current before token
	// validation / refresh. Reuse it here so the HTTPServer ends up
	// pointing at the SAME unlocked instance. If the early load
	// failed (offline first boot, brand-new install, etc.), retry
	// here against whatever cfg now looks like — the rotations
	// completed during boot may have repopulated PreviousAuthToken
	// in a way that lets the fallback decrypt this time.
	if vs := currentRuntimeVaultStore(); vs != nil {
		httpServer.vaultStore = vs
		log.Printf("Vault attached (%d entries) from early-boot runtime store.", len(vs.List("*")))
	} else if vs, err := tryOpenAgentVault(cfg, *vaultPass); err != nil {
		log.Printf("Warning: vault unavailable: %v", err)
	} else {
		httpServer.vaultStore = vs
		setRuntimeVaultStore(vs)
		log.Printf("Vault unlocked late (%d entries) — runtime store now tracks rotations.", len(vs.List("*")))
	}
	globalEmailMgr = emailMgr // enable email notifications

	// Wire notification callbacks
	taskMgr.OnTaskDone = func(task *Task) {
		dur := 0
		if task.StartedAt != nil && task.FinishedAt != nil {
			dur = int(task.FinishedAt.Sub(*task.StartedAt).Seconds())
		}

		// Auto-retry: if task failed and has retries left, retry before notifying
		if task.Status == TaskStatusFailed && task.AutoRetry {
			if taskMgr.autoRetryTask(task) {
				log.Printf("[retry] Task %s auto-retrying — skipping notifications", task.ID)
				return
			}
		}

		httpServer.notifyMgr.NotifyTaskCompleted(task.ID, task.Title, string(task.Status), task.CostUSD, dur)

		// Auto hot-reload: if the task completed successfully and a dev server
		// is running, broadcast a reload command so the app on the device picks
		// up the agent's file changes without a manual tap.
		if task.Status == TaskStatusFinished && httpServer.devServerMgr != nil && httpServer.devServerMgr.IsRunning() {
			if err := httpServer.devServerMgr.Reload(); err != nil {
				log.Printf("[task %s] auto-reload dev server: %v", task.ID, err)
			}
			if httpServer.blackboxMgr != nil {
				httpServer.blackboxMgr.BroadcastCommand(BlackBoxCommand{
					Command: "reload",
					Data:    map[string]interface{}{"reason": "task " + task.ID + " completed"},
				})
			}
		}

		// Auto Hermes-bundle reload: when a vibing-or-feedback-source task
		// finishes successfully, recompile the native bundle and broadcast
		// `reload_bundle` so the loaded guest app on a paired phone swaps
		// to the fresh HBC. Closes the shake → AI fix → bundle reloaded
		// loop without a manual tap. No-op when no preview worker is
		// listening (CLI-driven `yaver vibing` with no phone in the loop).
		// See feedback_to_vibe.go.
		httpServer.autoReloadAfterFeedbackVibingTask(task)

		// Record guest usage
		if task.GuestUserID != "" && dur > 0 && httpServer.guestConfigMgr != nil {
			httpServer.guestConfigMgr.RecordUsage(task.GuestUserID, float64(dur))
		}

		// Chain advancement: start next task in chain
		if task.ChainID != "" {
			taskMgr.advanceChain(task)
		}

		// Autopilot: drive the next todo item
		if httpServer.autopilot != nil && httpServer.autopilot.IsEnabled() && task.Source == "todolist" {
			httpServer.autopilot.OnTaskDone(task)
		}

		// Video summary: if task asked for one, kick off the clip
		// recorder. Non-blocking; the recorder emits clip_ready over
		// the vibe-preview SSE channel when the MP4 is mux-ready.
		MaybeRecordTaskSummary(task)
	}
	// Defensive sweep — flip stuck "recording" entries to "stale" if
	// the recorder somehow died without finalizing.
	reapInactiveTaskClips(taskMgr)
	httpServer.execMgr.OnExecDone = func(command string, exitCode int) {
		status := "completed"
		if exitCode != 0 {
			status = "failed"
		}
		httpServer.notifyMgr.NotifyExecCompleted(command, status, exitCode)
	}

	// Morning summary: send a daily digest at 9am local time
	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
			if now.After(next) {
				next = next.Add(24 * time.Hour)
			}
			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				since := time.Now().Add(-24 * time.Hour)
				text := taskMgr.GenerateSummaryText(since)
				if text != "" && text != "No tasks in the last 24 hours." {
					httpServer.notifyMgr.NotifyMorningSummary(text)
					log.Printf("[summary] Morning summary sent: %d chars", len(text))
				}
			}
		}
	}()

	chatBot := NewChatBot(taskMgr, httpServer.execMgr, httpServer.notifyMgr, cfg.Notifications)
	chatBot.Start(ctx)
	httpServer.onShutdown = func() {
		log.Println("Shutdown requested via API — stopping agent")
		cancel() // cancel the main context, triggers graceful shutdown
	}
	go func() {
		err := httpServer.Start(ctx)
		// "address already in use" on the agent's HTTP port is almost
		// always a stale `yaver serve` from a previous install that
		// survived this restart. Try once to reclaim the port from
		// that yaver-on-yaver conflict before falling through to the
		// fatal exit — the user can't always SSH in to fix it (remote
		// primary reached only from their phone).
		// reclaimPortFromStaleYaver refuses to kill foreign processes,
		// so the worst case is "no holder is yaver, retry skipped".
		if err != nil && isAddrInUseErr(err) {
			log.Printf("HTTP server bind failed on :%d: %v", *httpPort, err)
			if reclaimPortFromStaleYaver(*httpPort) {
				log.Printf("[port-reclaim] reclaimed :%d from stale yaver — retrying bind", *httpPort)
				err = httpServer.Start(ctx)
			}
		}
		if err != nil {
			if isAddrInUseErr(err) {
				log.Printf("[port-conflict] Another process is bound to :%d and we couldn't reclaim it.", *httpPort)
				log.Printf("[port-conflict] Most likely an older yaver process or a foreign service is holding the port.")
				log.Printf("[port-conflict] Find it: ss -tnlp | grep :%d   (or: lsof -i :%d)", *httpPort, *httpPort)
				log.Printf("[port-conflict] Kill it: pkill -f 'yaver serve'  (then retry)")
				log.Printf("[port-conflict] If the dashboard is showing a stale agent version, this is why.")
			}
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start QUIC server (legacy, can be disabled)
	if !*noQUIC {
		quicServer := NewQUICServer(*quicPort, cfg.AuthToken, hostname, taskMgr)
		go func() {
			if err := quicServer.Start(ctx); err != nil {
				log.Printf("QUIC server error: %v", err)
			}
		}()
	}

	// Start LAN discovery beacon (UDP broadcast for same-network mobile discovery)
	go startBeacon(ctx, cfg.DeviceID, *httpPort, hostname, ownerUserID)

	// Disk health + SMART monitor — solo-dev headless hardware
	// guard. Every 10 minutes the scanner refreshes the
	// MachineHealth snapshot and fires notifications for fresh
	// crossings of the 85% / 95% disk thresholds or a SMART
	// "failing" transition. Local only, no vendor. Opt-out via
	// `disable_disk_health: true` in config.json.
	if cfg.DisableDiskHealth {
		log.Printf("[disk-health] disabled via config — skipping")
	} else {
		StartDiskHealthLoop()
	}

	// Peer heartbeat watcher — alerts when a registered peer
	// hasn't checked in for > 5 minutes. Opt-out via
	// `disable_heartbeat_watcher: true` in config.json.
	if cfg.DisableHeartbeatWatcher {
		log.Printf("[heartbeat] disabled via config — skipping")
	} else {
		StartHeartbeatWatcher(ctx)
	}

	// Job queue worker — drains ~/.yaver/jobs/queue/ every 2s
	// and runs registered handlers with retry/backoff/DLQ.
	registerBuiltinJobHandlers()
	StartJobQueue()

	// Start relay tunnels with hot-reload support
	// Initial relay tunnels are started, and config is polled for changes every 30s
	relayMgr := newRelayManager(ctx, cfg.DeviceID, cfg.AuthToken, fmt.Sprintf("127.0.0.1:%d", *httpPort), effectiveRelayPassword, cfg.ConvexSiteURL)
	if userSettings != nil && userSettings.RelayUrl != "" {
		relayMgr.lastSettingsRelay = userSettings.RelayUrl
	}
	// Share the relay expose manager with the HTTP server so /expose/relay/* endpoints work.
	httpServer.relayExposeMgr = relayMgr.relayExposeMgr
	relayMgr.applyRelayServers(relayServers, relayPasswords)
	if !*noRelay {
		go relayMgr.watchConfig(ctx)
		go relayMgr.healthCheckLoop(ctx)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// SIGHUP triggers config reload (relay servers); SIGINT/SIGTERM shut down
	for {
		sig := <-sigCh
		if sig == syscall.SIGHUP {
			log.Println("[CONFIG] Received SIGHUP — reloading relay config...")
			relayMgr.reloadNow()
			continue
		}
		log.Printf("Received signal %s, shutting down...", sig)
		break
	}
	if taskMgr.TmuxMgr != nil {
		taskMgr.TmuxMgr.Shutdown()
	}
	// Stop all running task containers on shutdown
	if taskMgr.ContainerRunner != nil {
		taskMgr.ContainerRunner.StopAllContainers()
	}
	taskMgr.Shutdown()
	if err := MarkOffline(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); err != nil {
		log.Printf("failed to mark offline: %v", err)
	}
	cancel()
	os.Remove(pidFilePath())

	time.Sleep(1 * time.Second)
	log.Println("Agent stopped.")
}

// ---------------------------------------------------------------------------
// logs — show agent log output
// ---------------------------------------------------------------------------

func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	follow := fs.Bool("f", false, "Follow log output (like tail -f)")
	lines := fs.Int("n", 50, "Number of lines to show")
	fs.Parse(args)

	logFile := logFilePath()
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		fmt.Println("No logs found. Start the agent with 'yaver serve'.")
		return
	}

	if *follow {
		// Use tail -f for following
		cmd := osexec.Command("tail", "-f", "-n", fmt.Sprintf("%d", *lines), logFile)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}()

		cmd.Run()
	} else {
		cmd := osexec.Command("tail", "-n", fmt.Sprintf("%d", *lines), logFile)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}
}

// ---------------------------------------------------------------------------
// stop — stop the running agent
// ---------------------------------------------------------------------------

func runStop() {
	pid, running := isAgentRunning()
	if !running {
		cfg, err := LoadConfig()
		if err == nil && cfg != nil && cfg.AuthToken != "" && probeLocalAgentHealthInfo(18080) != nil {
			runShutdown()
			return
		}
		fmt.Println("Yaver agent is not running.")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding process %d: %v\n", pid, err)
		os.Exit(1)
	}

	if err := terminateProcess(proc); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping agent: %v\n", err)
		os.Exit(1)
	}

	// Wait for process to exit
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if !isProcessAlive(pid) {
			break
		}
	}

	os.Remove(pidFilePath())
	fmt.Printf("Yaver agent stopped (was PID %d).\n", pid)

	// Stop system service so it doesn't auto-restart
	stopAutoStartService()

	// Kill any orphan runner processes that may have survived
	killOrphanRunners()
}

// killOrphanRunners kills any leftover runner processes that were forked by
// the agent (tracked in ~/.yaver/forked-pids.txt). Only kills yaver-forked
// processes, never user's own claude/codex sessions.
func killOrphanRunners() {
	pids := getForkedPIDs()
	for _, pid := range pids {
		if isProcessAlive(pid) {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
				fmt.Printf("  Killed orphan runner process (PID %d)\n", pid)
			}
		}
	}
	clearForkedPIDs()
}

// ---------------------------------------------------------------------------
// shutdown — gracefully stop the agent via its HTTP API (same as mobile)
// ---------------------------------------------------------------------------

func runShutdown() {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		_, running := isAgentRunning()
		if running {
			fmt.Println("Not signed in — using kill instead.")
			runStop()
			return
		}
		fmt.Println("Yaver agent is not running.")
		return
	}

	pid, running := isAgentRunning()
	if !running && probeLocalAgentHealthInfo(18080) == nil {
		fmt.Println("Yaver agent is not running.")
		return
	}

	// Call the agent's HTTP shutdown endpoint
	url := fmt.Sprintf("http://127.0.0.1:%d/agent/shutdown", 18080) // default port
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Could not reach agent API — falling back to kill (PID %d)\n", pid)
		runStop()
		return
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode == 200 {
		if running {
			fmt.Printf("Shutdown signal sent to agent (PID %d) — stopping gracefully.\n", pid)
		} else {
			fmt.Println("Shutdown signal sent to running agent — stopping gracefully.")
		}
		// Wait for process to exit
		for i := 0; i < 50 && running; i++ {
			time.Sleep(100 * time.Millisecond)
			if !isProcessAlive(pid) {
				break
			}
		}
		os.Remove(pidFilePath())
		fmt.Println("Agent stopped.")
	} else {
		fmt.Printf("Shutdown API returned %d — falling back to kill\n", resp.StatusCode)
		if running {
			runStop()
		}
	}
}

// ---------------------------------------------------------------------------
// config — dump current CLI configuration
// ---------------------------------------------------------------------------

// runAutoUpdate handles `yaver auto-update [enable|disable|status|check]`.
// Mirrors the shape of `yaver auto-start`. Persists `auto_update` in
// ~/.yaver/config.json. Without the toggle and the semver guard added
// alongside it, a stale "latest release" tag on the upstream repo
// could DOWNGRADE a running agent — which actually happened on
// 2026-04-20 (kivanccakmak/yaver-cli's latest pointed at v1.37.0
// while the agent was on v1.99.10).
func runAutoUpdate(args []string) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "status":
		fmt.Println("auto-update")
		fmt.Printf("  enabled: %v\n", cfg.AutoUpdate)
		fmt.Printf("  repo:    %s\n", updateRepo())
		fmt.Printf("  current: v%s\n", version)
		fmt.Println("")
		fmt.Println("Subcommands:")
		fmt.Println("  yaver auto-update enable   — opt in to auto-update on agent boot")
		fmt.Println("  yaver auto-update disable  — opt out (default)")
		fmt.Println("  yaver auto-update check    — run the update check once, ignoring config")
		fmt.Println("")
		fmt.Println("Override repo: YAVER_UPDATE_REPO=<owner>/<repo> yaver serve")
	case "enable", "on", "true":
		cfg.AutoUpdate = true
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("auto-update: enabled")
		fmt.Printf("Will check %s for newer releases on agent boot.\n", updateRepo())
	case "disable", "off", "false":
		cfg.AutoUpdate = false
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("auto-update: disabled")
		fmt.Println("Run `yaver update` manually when you want a new version.")
	case "check":
		// Force a single check + apply, ignoring config. Used to test
		// the new semver guard / repo without flipping persistent
		// settings.
		forced := *cfg
		forced.AutoUpdate = true
		checkAutoUpdate(&forced)
	default:
		fmt.Fprintf(os.Stderr, "Unknown auto-update subcommand: %q\n", sub)
		fmt.Fprintln(os.Stderr, "Try: yaver auto-update [status|enable|disable|check]")
		os.Exit(1)
	}
}

func runConfig(args []string) {
	// Handle "yaver config set bootstrap-secret <value>" and
	// "yaver config bootstrap-secret [value|clear]".
	if len(args) >= 1 && (args[0] == "bootstrap-secret" || (len(args) >= 2 && args[0] == "set" && args[1] == "bootstrap-secret")) {
		rest := args[1:]
		if args[0] == "set" {
			rest = args[2:]
		}
		runConfigBootstrapSecret(rest)
		return
	}
	// Handle "yaver config set <key> <value>"
	if len(args) >= 3 && args[0] == "set" {
		runConfigSet(args[1], args[2])
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	cfgPath, _ := ConfigPath()
	fmt.Printf("Config file: %s\n\n", cfgPath)

	token := cfg.AuthToken
	if len(token) > 8 {
		token = token[:4] + "..." + token[len(token)-4:]
	} else if token != "" {
		token = "***"
	} else {
		token = "(not set)"
	}

	// Show user info if token is valid
	if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		if info, err := ValidateTokenInfo(cfg.ConvexSiteURL, cfg.AuthToken); err == nil {
			fmt.Printf("user:            %s (%s)\n", info.Email, info.Provider)
			if info.FullName != "" && info.FullName != info.Email {
				fmt.Printf("name:            %s\n", info.FullName)
			}
		}
	}

	// Show current runner
	if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		runnerID := getCurrentRunner(client, cfg.ConvexSiteURL, cfg.AuthToken)
		if runnerID == "" {
			runnerID = "claude"
		}
		fmt.Printf("runner:          %s\n", runnerID)
	}

	fmt.Printf("auth_token:      %s\n", token)
	fmt.Printf("device_id:       %s\n", valueOrEmpty(cfg.DeviceID))
	fmt.Printf("convex_site_url: %s\n", valueOrEmpty(cfg.ConvexSiteURL))
	fmt.Printf("auto_start:      %v\n", cfg.AutoStart)
	fmt.Printf("auto_update:     %v\n", cfg.AutoUpdate)
	fmt.Printf("headless_keep_awake: %v\n", shouldEnableHeadlessKeepAwake(cfg))
	fmt.Printf("require_private_recovery: %v\n", cfg.RequirePrivateRecoveryTransport)
}

func runConfigSet(key, value string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	switch key {
	case "auto-start":
		enabled := value == "true" || value == "1" || value == "yes"
		cfg.AutoStart = enabled
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		if enabled {
			exePath, err := os.Executable()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
				os.Exit(1)
			}
			workDir, _ := os.Getwd()
			if err := installAutoStart(exePath, workDir); err != nil {
				fmt.Fprintf(os.Stderr, "Error installing auto-start: %v\n", err)
				os.Exit(1)
			}
			if isWSL() {
				fmt.Println("WSL detected.")
				fmt.Println("Installed the Yaver WSL startup helper.")
				fmt.Println("This is not a native systemd service; it uses your shell profile and can also write a Windows Startup wrapper when available.")
			}
		} else {
			removeAutoStart()
			fmt.Println("Auto-start disabled. Yaver will no longer start on login.")
		}

	case "auto-update":
		enabled := value == "true" || value == "1" || value == "yes"
		cfg.AutoUpdate = enabled
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		if enabled {
			fmt.Println("Auto-update enabled. Yaver will check for updates on startup.")
		} else {
			fmt.Println("Auto-update disabled.")
		}

	case "headless-keep-awake":
		enabled := value == "true" || value == "1" || value == "yes"
		cfg.HeadlessKeepAwake = &enabled
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		if enabled {
			switch {
			case isWSL():
				fmt.Println("Headless keep-awake preference saved, but WSL cannot block host sleep.")
				fmt.Println("Use Windows power settings and keep Tailscale running as a Windows service.")
			case runtime.GOOS == "linux" || runtime.GOOS == "darwin":
				fmt.Println("Headless keep-awake enabled. Yaver will block system sleep while `yaver serve` is running.")
			default:
				fmt.Printf("Headless keep-awake saved, but %s does not support it.\n", runtime.GOOS)
			}
		} else {
			fmt.Println("Headless keep-awake disabled.")
		}

	case "require-private-recovery":
		enabled := value == "true" || value == "1" || value == "yes"
		cfg.RequirePrivateRecoveryTransport = enabled
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		if enabled {
			fmt.Println("Private-only /auth/recover enabled.")
			fmt.Println("Direct public HTTP recovery is now blocked; use LAN/Tailscale/private relay/HTTPS Cloudflare Tunnel.")
		} else {
			fmt.Println("Public /auth/recover restored (default open mode).")
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown config key: %s\n", key)
		fmt.Fprintf(os.Stderr, "Supported keys: auto-start, auto-update, headless-keep-awake, require-private-recovery\n")
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// relay — manage custom relay servers
// ---------------------------------------------------------------------------

func runRelay(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage:")
		fmt.Println("  yaver relay add <url> [--password <pass>] [--label <name>]")
		fmt.Println("  yaver relay list")
		fmt.Println("  yaver relay remove <id-or-url>")
		fmt.Println("  yaver relay test [url]")
		fmt.Println("  yaver relay set-password <password>")
		fmt.Println("  yaver relay clear-password")
		os.Exit(0)
	}

	switch args[0] {
	case "add":
		runRelayAdd(args[1:])
	case "list", "ls":
		runRelayList()
	case "remove", "rm":
		runRelayRemove(args[1:])
	case "test":
		runRelayTest(args[1:])
	case "set-password":
		runRelaySetPassword(args[1:])
	case "clear-password":
		runRelayClearPassword()
	default:
		fmt.Fprintf(os.Stderr, "Unknown relay subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// signalRunningAgent sends SIGHUP to the running agent process to trigger config reload.
// Returns true if the agent was signaled, false if not running.
func signalRunningAgent() bool {
	pidPath := pidFilePath()
	if pidPath == "" {
		return false
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.SIGHUP); err != nil {
		return false
	}
	return true
}

func runRelayAdd(args []string) {
	// Reorder args: move flags before positional URL arg so Go's flag package
	// can parse them (Go stops parsing at first non-flag argument)
	var reordered []string
	var positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			reordered = append(reordered, args[i])
			// Consume the next arg as the flag value (e.g. --password VALUE)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.Contains(args[i], "=") {
				reordered = append(reordered, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	reordered = append(reordered, positional...)

	fs := flag.NewFlagSet("relay add", flag.ExitOnError)
	password := fs.String("password", "", "Relay server password")
	label := fs.String("label", "", "Human-readable label (e.g. 'My VPS')")
	fs.Parse(reordered)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver relay add <url> [--password <pass>] [--label <name>]")
		os.Exit(1)
	}

	rawURL := fs.Arg(0)
	// Normalize: strip trailing slash
	rawURL = strings.TrimRight(rawURL, "/")

	// Generate ID from URL: first 8 hex chars of a simple hash
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

	// Infer QUIC address from URL (same host, port 4433)
	host := rawURL
	// Remove scheme
	for _, prefix := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, prefix)
	}
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	// Remove path
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}
	quicAddr := host + ":4433"

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Check for duplicate URL
	for _, rs := range cfg.RelayServers {
		if rs.HttpURL == rawURL {
			fmt.Fprintf(os.Stderr, "Relay server already configured: %s (id: %s)\n", rawURL, rs.ID)
			os.Exit(1)
		}
	}

	relay := RelayServerConfig{
		ID:       id,
		QuicAddr: quicAddr,
		HttpURL:  rawURL,
		Password: *password,
		Label:    *label,
		Priority: len(cfg.RelayServers) + 1,
	}

	cfg.RelayServers = append(cfg.RelayServers, relay)
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	displayLabel := relay.Label
	if displayLabel == "" {
		displayLabel = relay.HttpURL
	}
	fmt.Printf("Added relay server: %s\n", displayLabel)
	fmt.Printf("  ID:       %s\n", relay.ID)
	fmt.Printf("  URL:      %s\n", relay.HttpURL)
	fmt.Printf("  QUIC:     %s\n", relay.QuicAddr)
	if relay.Password != "" {
		fmt.Printf("  Password: ****\n")
	}
	if signalRunningAgent() {
		fmt.Println("\nAgent notified — relay will connect within seconds.")
	} else {
		fmt.Println("\nAgent is not running. Start it with: yaver serve")
	}
}

func runRelayList() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.RelayServers) == 0 {
		fmt.Println("No custom relay servers configured.")
		fmt.Println("Relay servers from Convex platform config will be used.")
		fmt.Println()
		fmt.Println("Add one with: yaver relay add <url>")
		return
	}

	fmt.Printf("%-10s %-35s %-12s %-10s\n", "ID", "URL", "Password", "Label")
	fmt.Printf("%-10s %-35s %-12s %-10s\n", "------", "---", "--------", "-----")
	for _, rs := range cfg.RelayServers {
		pw := "(none)"
		if rs.Password != "" {
			pw = "****"
		}
		lbl := rs.Label
		if lbl == "" {
			lbl = "-"
		}
		fmt.Printf("%-10s %-35s %-12s %-10s\n", rs.ID, rs.HttpURL, pw, lbl)
	}
}

func runRelayRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver relay remove <id-or-url>")
		os.Exit(1)
	}
	target := args[0]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	found := false
	var remaining []RelayServerConfig
	for _, rs := range cfg.RelayServers {
		if rs.ID == target || rs.HttpURL == target {
			found = true
			fmt.Printf("Removed relay server: %s (%s)\n", rs.HttpURL, rs.ID)
		} else {
			remaining = append(remaining, rs)
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "Relay server not found: %s\n", target)
		os.Exit(1)
	}

	cfg.RelayServers = remaining
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	if signalRunningAgent() {
		fmt.Println("Agent notified — relay tunnel will be stopped.")
	}
}

func runRelayTest(args []string) {
	var urls []string

	if len(args) > 0 {
		urls = []string{strings.TrimRight(args[0], "/")}
	} else {
		// Test all configured relays
		cfg, err := LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		for _, rs := range cfg.RelayServers {
			urls = append(urls, rs.HttpURL)
		}
		if len(urls) == 0 {
			fmt.Println("No relay servers configured. Pass a URL: yaver relay test <url>")
			os.Exit(0)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, u := range urls {
		healthURL := u + "/health"
		start := time.Now()
		resp, err := client.Get(healthURL)
		rtt := time.Since(start)
		if err != nil {
			fmt.Printf("FAIL  %s  error: %v\n", u, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			fmt.Printf("OK    %s  %dms  %s\n", u, rtt.Milliseconds(), strings.TrimSpace(string(body)))
		} else {
			fmt.Printf("FAIL  %s  status: %d  %s\n", u, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

func runRelaySetPassword(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver relay set-password <password>")
		os.Exit(1)
	}
	password := args[0]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	cfg.RelayPassword = password
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Relay password saved.")
	if signalRunningAgent() {
		fmt.Println("Agent notified — new password will be used for relay connections.")
	}
}

func runRelayClearPassword() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if cfg.RelayPassword == "" {
		fmt.Println("No relay password was set.")
		return
	}

	cfg.RelayPassword = ""
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Relay password cleared.")
	if signalRunningAgent() {
		fmt.Println("Agent notified.")
	}
}

func runTunnel(args []string) {
	if len(args) == 0 {
		fmt.Print(`Yaver Tunnel — Cloudflare Tunnel configuration

Usage:
  yaver tunnel add <url> [--cf-client-id <id>] [--cf-client-secret <secret>] [--label <name>]
  yaver tunnel list
  yaver tunnel remove <id-or-url>
  yaver tunnel test [url]
  yaver tunnel setup

Cloudflare Tunnel exposes your agent's HTTP server via a public HTTPS URL.
This works through any firewall that allows HTTPS traffic.

Connection priority: LAN direct → Cloudflare Tunnel → Relay Server
`)
		os.Exit(0)
	}

	switch args[0] {
	case "add":
		runTunnelAdd(args[1:])
	case "list", "ls":
		// Prefer the new unified tunnel-forward listing when
		// the dev has any forwards registered; otherwise fall
		// back to the legacy Cloudflare Tunnel list.
		if forwards, _ := loadTunnelForwards(); len(forwards) > 0 {
			tunnelListCmd()
			return
		}
		runTunnelList()
	case "remove", "rm":
		runTunnelRemove(args[1:])
	case "test":
		runTunnelTest(args[1:])
	case "setup":
		runTunnelSetup()
	// New SSH-replacement forward/connect subcommands.
	case "forward":
		tunnelForwardCmd(args[1:])
	case "connect":
		tunnelConnectCmd(args[1:])
	case "cloudflare":
		tunnelCloudflareCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown tunnel subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func runTunnelAdd(args []string) {
	fs := flag.NewFlagSet("tunnel add", flag.ExitOnError)
	cfClientId := fs.String("cf-client-id", "", "CF Access Service Token Client ID")
	cfClientSecret := fs.String("cf-client-secret", "", "CF Access Service Token Client Secret")
	label := fs.String("label", "", "Human-readable label")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver tunnel add <url> [--cf-client-id <id>] [--cf-client-secret <secret>] [--label <name>]")
		os.Exit(1)
	}

	rawURL := strings.TrimRight(fs.Arg(0), "/")

	// Generate ID from URL hash
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
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Check duplicate
	for _, t := range cfg.CloudflareTunnels {
		if t.URL == rawURL {
			fmt.Fprintf(os.Stderr, "Tunnel already configured: %s (id: %s)\n", rawURL, t.ID)
			os.Exit(1)
		}
	}

	tunnel := CloudflareTunnelConfig{
		ID:                   id,
		URL:                  rawURL,
		CFAccessClientId:     *cfClientId,
		CFAccessClientSecret: *cfClientSecret,
		Label:                *label,
		Priority:             len(cfg.CloudflareTunnels) + 1,
	}

	cfg.CloudflareTunnels = append(cfg.CloudflareTunnels, tunnel)
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	displayLabel := tunnel.Label
	if displayLabel == "" {
		displayLabel = tunnel.URL
	}
	fmt.Printf("Added Cloudflare Tunnel: %s\n", displayLabel)
	fmt.Printf("  ID:  %s\n", tunnel.ID)
	fmt.Printf("  URL: %s\n", tunnel.URL)
	if tunnel.CFAccessClientId != "" {
		fmt.Printf("  CF Access: configured\n")
	}
	fmt.Println("\nThe mobile app will try this tunnel when connecting to your machine.")
}

func runTunnelList() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.CloudflareTunnels) == 0 {
		fmt.Println("No Cloudflare Tunnels configured.")
		fmt.Println()
		fmt.Println("Add one with: yaver tunnel add <url>")
		fmt.Println("Or run: yaver tunnel setup  for a setup guide")
		return
	}

	fmt.Printf("%-10s %-40s %-12s %-10s\n", "ID", "URL", "CF Access", "Label")
	fmt.Printf("%-10s %-40s %-12s %-10s\n", "------", "---", "---------", "-----")
	for _, t := range cfg.CloudflareTunnels {
		cfAccess := "(none)"
		if t.CFAccessClientId != "" {
			cfAccess = "configured"
		}
		lbl := t.Label
		if lbl == "" {
			lbl = "-"
		}
		fmt.Printf("%-10s %-40s %-12s %-10s\n", t.ID, t.URL, cfAccess, lbl)
	}
}

func runTunnelRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver tunnel remove <id-or-url>")
		os.Exit(1)
	}
	target := args[0]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	found := false
	var remaining []CloudflareTunnelConfig
	for _, t := range cfg.CloudflareTunnels {
		if t.ID == target || t.URL == target {
			found = true
			fmt.Printf("Removed Cloudflare Tunnel: %s (%s)\n", t.URL, t.ID)
		} else {
			remaining = append(remaining, t)
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "Tunnel not found: %s\n", target)
		os.Exit(1)
	}

	cfg.CloudflareTunnels = remaining
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
}

func runTunnelTest(args []string) {
	var tunnels []CloudflareTunnelConfig

	if len(args) > 0 {
		tunnels = []CloudflareTunnelConfig{{URL: strings.TrimRight(args[0], "/")}}
	} else {
		cfg, err := LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		tunnels = cfg.CloudflareTunnels
		if len(tunnels) == 0 {
			fmt.Println("No tunnels configured. Pass a URL: yaver tunnel test <url>")
			os.Exit(0)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, t := range tunnels {
		healthURL := t.URL + "/health"
		req, _ := http.NewRequest("GET", healthURL, nil)
		if t.CFAccessClientId != "" {
			req.Header.Set("CF-Access-Client-Id", t.CFAccessClientId)
			req.Header.Set("CF-Access-Client-Secret", t.CFAccessClientSecret)
		}
		start := time.Now()
		resp, err := client.Do(req)
		rtt := time.Since(start)
		if err != nil {
			fmt.Printf("FAIL  %s  error: %v\n", t.URL, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			fmt.Printf("OK    %s  %dms  %s\n", t.URL, rtt.Milliseconds(), strings.TrimSpace(string(body)))
		} else {
			fmt.Printf("FAIL  %s  status: %d  %s\n", t.URL, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

func runTunnelSetup() {
	fmt.Print(`Cloudflare Tunnel Setup Guide
═════════════════════════════

Cloudflare Tunnel creates a secure HTTPS connection from Cloudflare's edge
to your machine. No port forwarding, works through any firewall.

── Prerequisites ─────────────────────────────────────────────────

  1. A Cloudflare account (free tier works)
  2. A domain on Cloudflare (or use quick tunnels for testing)
  3. cloudflared CLI installed

── Install cloudflared ───────────────────────────────────────────

  macOS:   brew install cloudflared
  Linux:   See https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/
  Windows: winget install Cloudflare.cloudflared

── Option A: Quick Tunnel (testing) ──────────────────────────────

  # Start yaver agent first
  $ yaver serve

  # In another terminal, create a quick tunnel
  $ cloudflared tunnel --url http://localhost:18080

  # You'll see a URL like: https://abc123.trycloudflare.com
  # Add it to yaver:
  $ yaver tunnel add https://abc123.trycloudflare.com

  Note: Quick tunnel URLs change each time. Use named tunnels for
  a permanent setup.

── Option B: Named Tunnel (production) ───────────────────────────

  # 1. Login to Cloudflare
  $ cloudflared tunnel login

  # 2. Create a tunnel
  $ cloudflared tunnel create yaver

  # 3. Route DNS
  $ cloudflared tunnel route dns yaver tunnel.yourdomain.com

  # 4. Create config file (~/.cloudflared/config.yml):
  tunnel: <tunnel-id>
  credentials-file: ~/.cloudflared/tunnel-id.json
  ingress:
    - hostname: tunnel.yourdomain.com
      service: http://localhost:18080
    - service: http_status:404

  # 5. Run the tunnel
  $ cloudflared tunnel run yaver

  # 6. Register in yaver
  $ yaver tunnel add https://tunnel.yourdomain.com

── Option C: Named Tunnel + CF Access (extra security) ───────────

  If you want to restrict access with a service token:

  1. Create a CF Access application for tunnel.yourdomain.com
  2. Create a Service Token in Zero Trust → Access → Service Auth
  3. Create a policy allowing the service token

  $ yaver tunnel add https://tunnel.yourdomain.com \
      --cf-client-id <service-token-client-id> \
      --cf-client-secret <service-token-client-secret>

── Mobile App ────────────────────────────────────────────────────

  In the Yaver mobile app:
    Settings → Cloudflare Tunnel → + Add
    Enter the same tunnel URL (and CF Access credentials if used).

  The app will try: LAN direct → Cloudflare Tunnel → Relay Server

── Run on startup (systemd) ──────────────────────────────────────

  $ sudo cloudflared service install
  $ sudo systemctl enable cloudflared
  $ sudo systemctl start cloudflared

For more: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/
`)
}

func valueOrEmpty(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// updateRepo returns the GitHub `owner/repo` the auto-updater should
// query for new releases. Defaults to the canonical CLI distribution
// repo. Override with the YAVER_UPDATE_REPO env var so future moves
// (or staging deploys) don't require a binary rebuild — every running
// agent picks the new repo on its next update tick.
//
// Default was historically `kivanccakmak/yaver-cli` but its `latest`
// release pointer drifted to v1.37.0 while the actual current
// release pipeline targets `kivanccakmak/yaver.io`. Pointing here by
// default stops the silent downgrade. Operators who maintain a
// separate distribution repo can still set YAVER_UPDATE_REPO.
func updateRepo() string {
	if r := strings.TrimSpace(os.Getenv("YAVER_UPDATE_REPO")); r != "" {
		return r
	}
	return "kivanccakmak/yaver.io"
}

func updateRepoForLog() string { return updateRepo() }

// checkAutoUpdate checks for a newer release on GitHub and self-updates the binary.
// Returns silently if auto-update is disabled or if already up-to-date.
func checkAutoUpdate(cfg *Config) {
	if !cfg.AutoUpdate {
		return
	}

	// Tell subscribers we're alive immediately. Without this the
	// dashboard sat on "(starting…)" until the GitHub API call below
	// returned ~1-2s later — which on a slow link looked stuck. See
	// the "make update logs streaming better user feels stuck" bug.
	emitAgentUpdate("queued", "Update requested — preparing")
	emitAgentUpdate("fetch_release", "Asking GitHub for the latest release")

	log.Println("[auto-update] Checking for updates...")

	type ghRelease struct {
		TagName string `json:"tag_name"`
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo()))
	if err != nil {
		emitAgentUpdate("error", "GitHub release lookup failed: %v", err)
		return
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != 200 {
		log.Printf("[auto-update] GitHub API returned %d", resp.StatusCode)
		return
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		log.Printf("[auto-update] Failed to parse release: %v", err)
		return
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	if latestVersion == "" {
		log.Printf("[auto-update] Empty release tag from GitHub — skipping")
		return
	}

	// Proper semver comparison so a stale "latest release" pointer on
	// the upstream repo can never DOWNGRADE the running agent. Without
	// this check, GitHub returning v1.37.0 as "latest" while the agent
	// runs v1.99.10 would happily clobber the modern binary with the
	// old one (which is exactly what happened to kivanc's Mac on
	// 2026-04-20). semver.Compare returns +1 when a > b, 0 when equal,
	// -1 when a < b — only proceed if latest is strictly greater.
	currentSv := "v" + strings.TrimPrefix(version, "v")
	latestSv := "v" + latestVersion
	if !semver.IsValid(currentSv) || !semver.IsValid(latestSv) {
		log.Printf("[auto-update] Non-semver version (current=%s latest=%s) — skipping", currentSv, latestSv)
		return
	}
	cmp := semver.Compare(latestSv, currentSv)
	if cmp == 0 {
		log.Printf("[auto-update] Already up-to-date (v%s)", version)
		return
	}
	if cmp < 0 {
		log.Printf("[auto-update] Upstream %q is older than running v%s — refusing to downgrade. (Check that %s is the right release repo and its `latest` pointer is current.)",
			latestSv, version, updateRepoForLog())
		return
	}

	emitAgentUpdate("check", "New version available: v%s (current: v%s)", latestVersion, version)

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	// release-cli.yml ships macOS/Linux as `.tar.gz`, Windows as
	// `.exe`. Auto-update used to download a bare `yaver-darwin-arm64`
	// path that didn't exist in the canonical release — the only
	// repo that had it was `kivanccakmak/yaver-cli` whose latest
	// pointer drifted to v1.37.0. Match the real asset names now.
	var assetName string
	if goos == "windows" {
		assetName = fmt.Sprintf("yaver-windows-%s.exe", goarch)
	} else {
		assetName = fmt.Sprintf("yaver-%s-%s.tar.gz", goos, goarch)
	}
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", updateRepo(), latestVersion, assetName)

	emitAgentUpdate("download", "Downloading %s", assetName)
	dlResp, err := client.Get(downloadURL)
	if err != nil {
		emitAgentUpdate("error", "Download failed: %v", err)
		return
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		emitAgentUpdate("error", "Download returned HTTP %d for %s", dlResp.StatusCode, assetName)
		return
	}

	// Wrap the response body in a reader that emits download
	// progress every ~250ms (or every 5%) so the dashboard can
	// render a real progress bar instead of a static "downloading…"
	// label. ContentLength is "-1" when GitHub doesn't set it; in
	// that case we still emit periodic byte counts so the user can
	// see something is happening.
	totalBytes := dlResp.ContentLength
	dlBody := newAgentUpdateProgressReader(dlResp.Body, totalBytes, "download")
	defer dlBody.flush() // emit a final progress event with bytes_downloaded == totalBytes

	// Write to a temp file next to the current binary, then replace
	// in two stages: extract from tar.gz (macOS/Linux) or use the raw
	// .exe (Windows). Backup the running binary as `.previous` first
	// so a future bad update can be rolled back.
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("[auto-update] Cannot find executable path: %v", err)
		return
	}

	tmpPath := exePath + ".update"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		log.Printf("[auto-update] Cannot create temp file: %v", err)
		return
	}

	if goos == "windows" {
		if _, err := io.Copy(tmpFile, dlBody); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			emitAgentUpdate("error", "Failed to write update: %v", err)
			return
		}
		tmpFile.Close()
	} else {
		// tar.gz containing a single file named `yaver`. Stream the
		// download through gzip+tar — the wrapped progress reader
		// above keeps emitting download bytes as gzip pulls more data
		// off the wire. Once the gzip stream EOFs we move on to
		// the "extract" phase explicitly so the dashboard sees the
		// transition (download → extract → replace).
		gzr, err := gzip.NewReader(dlBody)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			emitAgentUpdate("error", "gzip open failed: %v", err)
			return
		}
		emitAgentUpdate("extract", "Extracting %s", assetName)
		defer gzr.Close()
		tr := tar.NewReader(gzr)
		extracted := false
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				tmpFile.Close()
				os.Remove(tmpPath)
				log.Printf("[auto-update] tar read failed: %v", err)
				return
			}
			// Take the first regular file — release tarball ships
			// exactly one entry named `yaver`.
			if hdr.Typeflag == tar.TypeReg {
				if _, err := io.Copy(tmpFile, tr); err != nil {
					tmpFile.Close()
					os.Remove(tmpPath)
					log.Printf("[auto-update] Failed to extract %s: %v", hdr.Name, err)
					return
				}
				extracted = true
				break
			}
		}
		tmpFile.Close()
		if !extracted {
			os.Remove(tmpPath)
			log.Printf("[auto-update] Tarball had no regular files — bad release asset")
			return
		}
	}

	// Backup the current binary so we have a rollback path. Best-
	// effort: a missing previous file shouldn't block the update.
	backupPath := exePath + ".previous"
	_ = os.Remove(backupPath)
	if err := copyFile(exePath, backupPath); err != nil {
		log.Printf("[auto-update] (warn) backup of running binary failed: %v", err)
	}

	emitAgentUpdate("replace", "Replacing running binary at %s", exePath)
	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		emitAgentUpdate("error", "Failed to replace binary: %v", err)
		return
	}

	// On macOS, re-adhoc-sign the freshly-placed binary. The release
	// tarballs are adhoc-signed by `go build` but the kernel rejects
	// a Mach-O whose on-disk bytes don't match the embedded signature
	// with "load code signature error 2" → SIGKILL on first exec.
	// Rebuilding the adhoc signature against the current bytes
	// guarantees the next exec (including the systemd/launchd restart
	// below) will not be killed by the kernel.
	if runtime.GOOS == "darwin" {
		if out, err := osexec.Command("codesign", "--force", "--sign", "-", exePath).CombinedOutput(); err != nil {
			log.Printf("[auto-update] (warn) codesign adhoc re-sign failed: %v — %s", err, strings.TrimSpace(string(out)))
		}
		// Also strip quarantine just in case the tarball round-trip
		// re-applied it. Best-effort; ignore errors.
		_ = osexec.Command("xattr", "-dr", "com.apple.quarantine", exePath).Run()
	}

	emitAgentUpdate("restart", "Updated to v%s — restarting in 1s for the new binary to take effect", latestVersion)
	// Brief pause so any in-flight SSE subscribers receive the
	// restart event before the process exits and their stream
	// closes. Without this the dashboard sees "stream closed"
	// without ever seeing why.
	time.Sleep(1 * time.Second)

	// Exit cleanly so the supervisor respawns us with the new binary:
	//   - systemd (Linux):   INVOCATION_ID env, Restart=on-failure triggers
	//     on any non-zero OR on clean exit if Restart=always. We exit 0 and
	//     rely on the unit we install (Restart=always + RestartSec=5).
	//   - launchd (macOS):   KeepAlive=true in our plist restarts on any
	//     exit (clean or otherwise). So a clean os.Exit(0) picks up the
	//     new binary within a few seconds — no reboot required. This
	//     closes the "auto-updated but old process still serving"
	//     window that bit users before Apr 2026.
	//   - Foreground (no supervisor): the user started us manually.
	//     Don't exit from under them — they'll see the "take effect
	//     on next restart" line and can CTRL-C when they like.
	if os.Getenv("INVOCATION_ID") != "" {
		log.Println("[auto-update] Running under systemd — exiting for automatic restart with new binary.")
		os.Exit(0)
	}
	if runtime.GOOS == "darwin" && isUnderLaunchd() {
		log.Println("[auto-update] Running under launchd — exiting for automatic restart with new binary.")
		os.Exit(0)
	}
	log.Println("[auto-update] New version will take effect on next restart.")
}

// isUnderLaunchd reports whether the current process was spawned by
// launchd (so KeepAlive=true will respawn us on clean exit). We
// check three cheap signals in order of reliability:
//  1. XPC_SERVICE_NAME — set by launchd for anything it launches.
//  2. LaunchDaemons set LAUNCHD_SOCKET / LAUNCH_DAEMON_SOCKET_NAME.
//  3. getppid() == 1 — classic launchd-owned parent; works for
//     user LaunchAgents that were re-parented to launchd.
//
// Any one of these being true is enough.
func isUnderLaunchd() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if os.Getenv("XPC_SERVICE_NAME") != "" {
		return true
	}
	if os.Getenv("LAUNCHD_SOCKET") != "" || os.Getenv("LAUNCH_DAEMON_SOCKET_NAME") != "" {
		return true
	}
	if os.Getppid() == 1 {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// set-runner — set which AI agent to use
// ---------------------------------------------------------------------------

func runSetRunner(args []string) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Fetch available runners from Convex
	runners, err := fetchRunnersFromBackend(client, cfg.ConvexSiteURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not fetch runners: %v\n", err)
		os.Exit(1)
	}

	// No args: list available runners and show current selection
	if len(args) == 0 {
		// Fetch current settings
		currentRunner := getCurrentRunner(client, cfg.ConvexSiteURL, cfg.AuthToken)
		fmt.Println("Available AI runners:")
		fmt.Println()
		for _, r := range runners {
			marker := "  "
			if r.RunnerID == currentRunner {
				marker = "* "
			}
			fmt.Printf("  %s%-12s %s\n", marker, r.RunnerID, r.Name)
			if r.Description != "" {
				fmt.Printf("    %s%s\n", strings.Repeat(" ", 12), r.Description)
			}
		}
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  yaver set-runner claude           Use Claude Code (default)")
		fmt.Println("  yaver set-runner codex            Use OpenAI Codex")
		fmt.Println("  yaver set-runner opencode         Use opencode (BYOK)")
		fmt.Printf("  yaver set-runner custom \"cmd\"      Use a custom command\n")
		fmt.Println()
		fmt.Println("Tip: You can also pick the AI agent from the Yaver mobile app when")
		fmt.Println("creating a task. Each task can use a different agent — this command")
		fmt.Println("sets the default for new tasks.")
		fmt.Println()
		if currentRunner != "" {
			fmt.Printf("Current runner: %s\n", currentRunner)
		}
		return
	}

	runnerID := args[0]

	// Validate runner ID
	if runnerID != "custom" {
		found := false
		for _, r := range runners {
			if r.RunnerID == runnerID {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Unknown runner: %s\n", runnerID)
			fmt.Fprintln(os.Stderr, "Run 'yaver set-runner' to see available runners.")
			os.Exit(1)
		}
	}

	// Build settings payload
	payload := map[string]string{"runnerId": runnerID}
	if runnerID == "custom" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Custom runner requires a command.")
			fmt.Fprintln(os.Stderr, "Example: yaver set-runner custom \"my-ai --auto {prompt}\"")
			os.Exit(1)
		}
		payload["customRunnerCommand"] = args[1]
	}

	payloadBytes, _ := json.Marshal(payload)
	req, err := newBearerRequest("POST", cfg.ConvexSiteURL+"/settings", cfg.AuthToken, bytes.NewReader(payloadBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not save settings: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Server returned %d\n", resp.StatusCode)
		os.Exit(1)
	}

	if runnerID == "custom" {
		fmt.Printf("Runner set to: custom (%s)\n", args[1])
	} else {
		// Find name
		name := runnerID
		for _, r := range runners {
			if r.RunnerID == runnerID {
				name = r.Name
				break
			}
		}
		fmt.Printf("Runner set to: %s\n", name)
	}
	fmt.Println("Restart the agent for changes to take effect: yaver restart")
}

type backendRunner struct {
	RunnerID    string `json:"runnerId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func fetchRunnersFromBackend(client *http.Client, convexSiteURL string) ([]backendRunner, error) {
	req, err := http.NewRequest("GET", convexSiteURL+"/runners", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runners endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Response is {"runners": [...]}
	var parsed struct {
		Runners []backendRunner `json:"runners"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Runners, nil
}

func getCurrentRunner(client *http.Client, convexSiteURL, token string) string {
	req, err := newBearerRequest("GET", convexSiteURL+"/settings", token, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var settings struct {
		RunnerID string `json:"runnerId"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return ""
	}
	return settings.RunnerID
}

// ---------------------------------------------------------------------------
// clear-logs — truncate the agent log file
// ---------------------------------------------------------------------------

func runClearLogs() {
	lp := logFilePath()
	if lp == "" {
		fmt.Fprintln(os.Stderr, "Could not determine log file path.")
		os.Exit(1)
	}
	if err := os.Truncate(lp, 0); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No log file to clear.")
			return
		}
		fmt.Fprintf(os.Stderr, "Error clearing logs: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Agent logs cleared.")
}

// ---------------------------------------------------------------------------
// restart — stop and re-start the agent
// ---------------------------------------------------------------------------

func runRestart(args []string) {
	if pid, running := isAgentRunning(); running {
		proc, err := os.FindProcess(pid)
		if err == nil {
			terminateProcess(proc)
			for i := 0; i < 30; i++ {
				time.Sleep(100 * time.Millisecond)
				if !isProcessAlive(pid) {
					break
				}
			}
		}
		os.Remove(pidFilePath())
		fmt.Printf("Stopped previous agent (PID %d).\n", pid)
	}
	runServe(args)
}

// ---------------------------------------------------------------------------
// status — show auth and agent status
// ---------------------------------------------------------------------------

func runStatus() {
	printWSL2RequirementWarning()

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		// Unauthenticated path: don't bail. The agent may be
		// running in bootstrap mode waiting for a phone to push
		// a token over the LAN beacon or the relay tunnel. Show
		// the user what's actually up so they know what to do
		// next instead of just "Run yaver auth".
		fmt.Printf("Yaver:    v%s\n", version)

		// If we're mid-device-code-flow (the human is about to tap
		// the URL on their phone, or hasn't yet), surface that up
		// front — this is the key bit an orchestrating AI agent
		// re-polls for between retries of `yaver auth`.
		if pend, _ := loadPendingAuth(); pend != nil &&
			pend.ExpiresAt > time.Now().UnixMilli() &&
			strings.TrimSpace(pend.DeviceCode) != "" {
			fmt.Println("Auth:     \033[33m●\033[0m waiting for sign-in on your phone")
			fmt.Printf("  URL:    %s\n", pend.URL)
			fmt.Printf("  Code:   %s\n", pend.UserCode)
			fmt.Printf("  Valid:  another %s\n",
				humanRoundDuration(time.Until(time.UnixMilli(pend.ExpiresAt))))
			fmt.Println("  Tap the URL on your phone, sign in (Apple / GitHub / Google / Microsoft),")
			fmt.Println("  then run `yaver auth` once more here to finalize.")
			fmt.Println()
		} else {
			fmt.Println("Auth:     \033[33m●\033[0m not signed in")
		}

		// Probe the local HTTP surface. If the agent is up in
		// bootstrap mode, /info answers without auth and reports
		// `mode: "bootstrap"`. If the full HTTPServer is running
		// it also answers /info but with a different shape — we
		// only claim "bootstrap" when the mode field says so.
		info := probeBootstrapInfo(18080)
		if info != nil && info.OK && info.Mode == "bootstrap" {
			fmt.Printf("Agent:    \033[32m●\033[0m running (bootstrap mode, port 18080)\n")
			if info.Hostname != "" {
				fmt.Printf("Host:     %s\n", info.Hostname)
			}
			if info.Version != "" {
				fmt.Printf("Binary:   v%s\n", info.Version)
			}
			fmt.Println("Mode:     bootstrap — waiting for a phone to pair")
		} else if pid, running := isAgentRunning(); running {
			fmt.Printf("Agent:    \033[33m●\033[0m running (PID %d, not responding to /info)\n", pid)
		} else {
			fmt.Printf("Agent:    \033[31m●\033[0m stopped\n")
		}

		// Auto-start state — the LaunchAgent / systemd unit
		// will keep us up across reboots even before auth.
		if isAutoStartInstalled() {
			fmt.Println("Auto-start: \033[32m●\033[0m installed (will run on login/boot)")
		} else {
			fmt.Println("Auto-start: \033[33m●\033[0m not installed (run 'yaver serve' to install)")
		}

		fmt.Println()
		fmt.Println("To sign in, either:")
		fmt.Println("  • Run 'yaver auth' here (opens browser for Apple/GitHub/Google/Microsoft sign-in), or")
		fmt.Println("  • Open the Yaver mobile app — this machine will be auto-paired:")
		fmt.Println("      - Same Wi-Fi: detected via LAN beacon, paired in ~5 seconds")
		fmt.Println("      - Any network: detected via relay, paired with encrypted token push")
		return
	}

	// Check agent first (local, instant). Prefix with a green dot
	// for running, red dot for stopped so terminals with color
	// show intent at a glance.
	liveHealth := probeLocalAgentHealthInfo(18080)
	liveInfo := probeAuthedLocalAgentInfo(18080, cfg.AuthToken)
	agentStatus := "\033[31m●\033[0m stopped"
	if liveHealth != nil {
		agentStatus = "\033[32m●\033[0m running (port 18080)"
		if pid, running := isAgentRunning(); running {
			agentStatus = fmt.Sprintf("\033[32m●\033[0m running (PID %d, port 18080)", pid)
		}
	} else if pid, running := isAgentRunning(); running {
		agentStatus = fmt.Sprintf("\033[33m●\033[0m pidfile says running (PID %d, port 18080 unreachable)", pid)
	}
	fmt.Printf("Yaver:    v%s\n", version)

	// Print local info immediately
	fmt.Printf("Agent:    %s\n", agentStatus)
	if liveInfo != nil && liveInfo.WorkDir != "" {
		fmt.Printf("Workdir:  %s\n", liveInfo.WorkDir)
	}
	if liveHealth != nil && liveHealth.Version != "" {
		fmt.Printf("Binary:   v%s\n", liveHealth.Version)
	}
	if cfg.DeviceID != "" {
		fmt.Printf("Device:   %s\n", cfg.DeviceID[:8]+"...")
	}
	fmt.Printf("Backend:  %s\n", cfg.ConvexSiteURL)

	// Git identity + GH/GitLab credential readiness. Independent of agent
	// state, so we surface it here whether the agent is running or not —
	// users hit "fatal: empty ident name" when the local config is missing
	// long before any task fails.
	renderGitStatusBlock(os.Stdout, collectGitStatusSummary(), "")

	// Validate token with a short timeout (3s) — don't block the user
	statusClient := &http.Client{Timeout: 3 * time.Second}
	req, reqErr := newBearerRequest("GET", cfg.ConvexSiteURL+"/auth/validate", cfg.AuthToken, nil)
	if reqErr != nil {
		fmt.Printf("Auth:     \033[33m●\033[0m token present (validation skipped)\n")
		return
	}
	resp, respErr := statusClient.Do(req)
	if respErr != nil {
		fmt.Printf("Auth:     \033[33m●\033[0m token present (could not reach server)\n")
		return
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Auth:     \033[31m●\033[0m session expired (agent still running)\n")
		fmt.Println()
		fmt.Println("Your session expired but the agent is still running locally.")
		fmt.Println("Run 'yaver auth' to refresh. Only 'yaver signout' will clear your credentials.")
		return
	}

	var result struct {
		User UserInfo `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Auth:     \033[32m●\033[0m valid\n")
		return
	}

	fmt.Printf("Auth:     \033[32m●\033[0m valid\n")
	fmt.Printf("User:     %s (%s)\n", result.User.Email, result.User.Provider)
	if result.User.FullName != "" && result.User.FullName != result.User.Email {
		fmt.Printf("Name:     %s\n", result.User.FullName)
	}

	// Show current runner
	runnerID := getCurrentRunner(statusClient, cfg.ConvexSiteURL, cfg.AuthToken)
	if runnerID == "" {
		runnerID = "claude"
	}
	runnerName := runnerID
	if runners, err := fetchRunnersFromBackend(statusClient, cfg.ConvexSiteURL); err == nil {
		for _, r := range runners {
			if r.RunnerID == runnerID {
				runnerName = r.Name
				break
			}
		}
	}
	fmt.Printf("Runner:   %s (%s)\n", runnerName, runnerID)

	// Check runner binary
	runnerCmd := runnerID
	if path, lookErr := osexec.LookPath(runnerCmd); lookErr != nil {
		fmt.Printf("  Status: not installed (%s not found in PATH)\n", runnerCmd)
	} else {
		fmt.Printf("  Binary: %s\n", path)
	}

	// Query the running agent's API for forked processes
	if pid, running := isAgentRunning(); running {
		agentURL := fmt.Sprintf("http://127.0.0.1:%d/agent/status", 18080)
		req, _ := http.NewRequest("GET", agentURL, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		statusResp, err := statusClient.Do(req)
		if err == nil {
			defer statusResp.Body.Close()
			if statusResp.StatusCode == 200 {
				var statusBody struct {
					Status AgentStatus `json:"status"`
				}
				if json.NewDecoder(statusResp.Body).Decode(&statusBody) == nil {
					procs := statusBody.Status.RunnerProcesses
					if len(procs) > 0 {
						for _, p := range procs {
							cmdPreview := p.Command
							if len(cmdPreview) > 60 {
								cmdPreview = cmdPreview[:60] + "..."
							}
							fmt.Printf("  Forked: PID %d (%s)\n", p.PID, cmdPreview)
						}
					} else {
						fmt.Printf("  Forked: idle\n")
					}

					// Show running tasks only
					runningCount := int(statusBody.Status.RunningTasks)
					if runningCount > 0 {
						fmt.Printf("  Tasks:  %d running\n", runningCount)
					}
				}
			}
		}
		_ = pid
	}

	// Relay server status
	fmt.Println()
	fmt.Println("Relay:")
	if cfg.RelayPassword != "" {
		fmt.Println("  Password: set (global)")
	}

	if len(cfg.RelayServers) == 0 {
		// Try to show relay info from user settings (per-user relay)
		if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
			if us, err := fetchUserSettingsForStatus(cfg.ConvexSiteURL, cfg.AuthToken); err == nil && us.RelayUrl != "" {
				pw := ""
				if us.RelayPassword != "" {
					pw = " [per-user password]"
				}
				fmt.Printf("  Server:   %s (from account settings)%s\n", us.RelayUrl, pw)
			} else if cfg.RelayPassword == "" {
				fmt.Println("  Password: not set")
				fmt.Println("  Servers:  none configured (will fetch from Convex on serve)")
			}
		} else {
			if cfg.RelayPassword == "" {
				fmt.Println("  Password: not set")
			}
			fmt.Println("  Servers:  none configured (will fetch from Convex on serve)")
		}
	} else {
		// Load cached health from agent's background health checks
		cachedHealth := make(map[string]*RelayHealthStatus)
		healthList := loadRelayHealth()
		for i := range healthList {
			cachedHealth[healthList[i].URL] = &healthList[i]
		}

		fmt.Println("  Servers:")
		for _, rs := range cfg.RelayServers {
			label := rs.ID
			if rs.Label != "" {
				label = rs.Label
			}
			pw := ""
			if rs.Password != "" {
				pw = " [password]"
			} else if cfg.RelayPassword != "" {
				pw = " [global pw]"
			}

			// Use cached health from agent's background checks (instant, no HTTP)
			if cached, ok := cachedHealth[rs.HttpURL]; ok && !cached.LastChecked.IsZero() {
				ago := time.Since(cached.LastChecked).Truncate(time.Second)
				if cached.OK {
					fmt.Printf("    %-10s %-30s OK (%dms, %d tunnel(s), v%s)%s\n",
						label, rs.HttpURL, cached.LatencyMs, cached.Tunnels, cached.Version, pw)
					fmt.Printf("              Last check: %s ago\n", ago)
				} else {
					fmt.Printf("    %-10s %-30s FAIL (%s)%s\n", label, rs.HttpURL, cached.Error, pw)
					fmt.Printf("              Last check: %s ago\n", ago)
				}
			} else {
				fmt.Printf("    %-10s %-30s (no health data yet)%s\n", label, rs.HttpURL, pw)
			}
		}
	}

	printStatusRemoteAccess(cfg)
	printStatusSharing(statusClient, cfg)

	// Voice / Speech status
	fmt.Println()
	fmt.Println("Voice:")
	if cfg.Speech != nil && cfg.Speech.Provider != "" {
		fmt.Printf("  STT:      %s\n", cfg.Speech.Provider)
		if cfg.Speech.APIKey != "" {
			fmt.Printf("  API Key:  set\n")
		} else if cfg.Speech.Provider == "whisper" || cfg.Speech.Provider == "on-device" {
			// Check if whisper-cpp is installed
			if _, err := osexec.LookPath("whisper-cpp"); err == nil {
				fmt.Printf("  Whisper:  installed\n")
			} else if _, err := osexec.LookPath("whisper"); err == nil {
				fmt.Printf("  Whisper:  installed\n")
			} else {
				fmt.Printf("  Whisper:  not found (install: brew install whisper-cpp)\n")
			}
		} else {
			fmt.Printf("  API Key:  not set\n")
		}
		if cfg.Speech.TTSEnabled {
			fmt.Printf("  TTS:      enabled\n")
		} else {
			fmt.Printf("  TTS:      disabled\n")
		}
	} else {
		fmt.Printf("  Not configured. Run: yaver config set speech.provider <whisper|openai|deepgram|assemblyai>\n")
	}

	printStatusMesh()
}

// ---------------------------------------------------------------------------
// devices — list registered devices
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tmux — manage tmux session adoption
// ---------------------------------------------------------------------------

func runTmux(args []string) {
	if len(args) == 0 {
		fmt.Print(`Usage:
  yaver tmux list                 List all tmux sessions (with agent detection)
  yaver tmux adopt <session>      Adopt a tmux session as a Yaver task
  yaver tmux detach <task-id>     Stop monitoring an adopted session (keeps running)
`)
		return
	}

	sub := args[0]
	switch sub {
	case "list":
		runTmuxList()
	case "adopt":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver tmux adopt <session-name>")
			os.Exit(1)
		}
		runTmuxAdopt(args[1])
	case "detach":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver tmux detach <task-id>")
			os.Exit(1)
		}
		runTmuxDetach(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown tmux subcommand: %s\n", sub)
		os.Exit(1)
	}
}

type statusSharedUsersResponse struct {
	Users    []statusSharedUser `json:"users"`
	TeamID   string             `json:"teamId"`
	MaxUsers int                `json:"maxUsers"`
}

type statusSharedUser struct {
	UserID       string `json:"userId"`
	Email        string `json:"email"`
	FullName     string `json:"fullName"`
	Provider     string `json:"provider"`
	WorkspaceDir string `json:"workspaceDir"`
	CreatedAt    string `json:"createdAt"`
	LastActiveAt string `json:"lastActiveAt"`
}

func printStatusSharing(client *http.Client, cfg *Config) {
	fmt.Println()
	fmt.Println("Sharing:")

	paired := ListPairedTokens()
	fmt.Printf("  Paired users: %d\n", len(paired))
	if len(paired) > 0 {
		for _, p := range paired {
			label := p.Label
			if label == "" {
				label = p.TokenHash[:8]
			}
			source := p.SourceHost
			if source == "" {
				source = "unknown"
			}
			lastUsed := p.LastUsedAt
			if lastUsed == "" {
				lastUsed = "never"
			}
			fmt.Printf("    %s  source=%s  last-used=%s\n", label, source, lastUsed)
		}
	}

	if shared, err := fetchLocalSharedUsers(client, cfg.AuthToken); err == nil {
		fmt.Printf("  Shared sessions: %d", len(shared.Users))
		if shared.TeamID != "" {
			fmt.Printf("  team=%s", shared.TeamID)
		}
		if shared.MaxUsers > 0 {
			fmt.Printf("  limit=%d", shared.MaxUsers)
		}
		fmt.Println()
		if len(shared.Users) > 0 {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "    EMAIL\tPROVIDER\tLAST ACTIVE\tWORKSPACE")
			for _, u := range shared.Users {
				email := u.Email
				if email == "" {
					email = shortStatusUserID(u.UserID)
				}
				provider := u.Provider
				if provider == "" {
					provider = "-"
				}
				fmt.Fprintf(w, "    %s\t%s\t%s\t%s\n",
					email,
					provider,
					statusTimeOrDash(u.LastActiveAt),
					u.WorkspaceDir,
				)
			}
			w.Flush()
		}
	} else {
		fmt.Println("  Shared sessions: unavailable (agent not in multi-user mode or not reachable)")
	}

	statusCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	var (
		guests      []GuestInfo
		guestErr    error
		configs     []GuestConfig
		cfgErr      error
		devices     []DeviceInfo
		devicesErr  error
		statusFetch sync.WaitGroup
	)
	statusFetch.Add(3)
	go func() {
		defer statusFetch.Done()
		guests, guestErr = fetchGuestListForStatus(statusCtx, cfg.ConvexSiteURL, cfg.AuthToken)
	}()
	go func() {
		defer statusFetch.Done()
		configs, cfgErr = fetchGuestConfigsForStatus(statusCtx, cfg.ConvexSiteURL, cfg.AuthToken)
	}()
	go func() {
		defer statusFetch.Done()
		devices, devicesErr = listDevicesForStatus(statusCtx, cfg.ConvexSiteURL, cfg.AuthToken)
	}()
	statusFetch.Wait()

	if guestErr != nil {
		fmt.Printf("  Guests: unavailable (%v)\n", guestErr)
		printStatusRunnableMachinesFromDevices(devices, devicesErr, cfg)
		return
	}
	configByEmail := map[string]*GuestConfig{}
	if cfgErr == nil {
		for i := range configs {
			c := configs[i]
			configByEmail[strings.ToLower(strings.TrimSpace(c.GuestEmail))] = &c
		}
	}

	activeGuests := 0
	for _, g := range guests {
		if strings.EqualFold(g.Status, "accepted") || strings.EqualFold(g.Status, "active") {
			activeGuests++
		}
	}
	fmt.Printf("  Guests: %d total, %d active\n", len(guests), activeGuests)
	if len(guests) == 0 {
		printStatusRunnableMachinesFromDevices(devices, devicesErr, cfg)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "    EMAIL\tSTATUS\tSINCE\tACCESS")
	for _, g := range guests {
		since := g.CreatedAt
		if g.AcceptedAt > 0 {
			since = g.AcceptedAt
		}
		cfg := configByEmail[strings.ToLower(strings.TrimSpace(g.Email))]
		fmt.Fprintf(w, "    %s\t%s\t%s\t%s\n",
			g.Email,
			g.Status,
			statusUnixMilliOrDash(since),
			summarizeGuestAccess(cfg),
		)
	}
	w.Flush()

	printStatusRunnableMachinesFromDevices(devices, devicesErr, cfg)
}

func printStatusRunnableMachines(cfg *Config) {
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	printStatusRunnableMachinesFromDevices(devices, err, cfg)
}

func printStatusRunnableMachinesFromDevices(devices []DeviceInfo, err error, cfg *Config) {
	if err != nil {
		fmt.Printf("  Runnable machines: unavailable (%v)\n", err)
		return
	}

	if len(devices) == 0 {
		fmt.Println("  Runnable machines: none")
		return
	}

	runnable := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		if strings.TrimSpace(d.DeviceID) == "" {
			continue
		}
		runnable = append(runnable, d)
	}
	if len(runnable) == 0 {
		fmt.Println("  Runnable machines: none")
		return
	}

	onlineCount := 0
	for _, d := range runnable {
		if d.IsOnline {
			onlineCount++
		}
	}

	sort.Slice(runnable, func(i, j int) bool {
		if runnable[i].IsOnline != runnable[j].IsOnline {
			return runnable[i].IsOnline
		}
		if runnable[i].IsGuest != runnable[j].IsGuest {
			return !runnable[i].IsGuest
		}
		return strings.ToLower(runnable[i].Name) < strings.ToLower(runnable[j].Name)
	})

	fmt.Printf("  Runnable machines: %d total, %d up\n", len(runnable), onlineCount)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "    NAME\tSTATUS\tACCESS\tSESSION\tADDRESS")
	for _, d := range runnable {
		status := "down"
		if d.IsOnline {
			status = "up"
		}
		fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n",
			statusDeviceLabel(d, cfg.DeviceID),
			status,
			deviceAccessLabel(d),
			deviceSessionBindingLabel(d),
			deviceAddressLabel(d),
		)
	}
	w.Flush()
}

func fetchGuestListForStatus(ctx context.Context, baseURL, token string) ([]GuestInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/guests/list", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create guest list request: %w", err)
	}
	req = req.WithContext(ctx)
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch guest list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("guest list failed (status %d)", resp.StatusCode)
	}
	var result struct {
		Guests []GuestInfo `json:"guests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode guest list: %w", err)
	}
	return result.Guests, nil
}

func fetchGuestConfigsForStatus(ctx context.Context, baseURL, token string) ([]GuestConfig, error) {
	req, err := newBearerRequest("GET", baseURL+"/guests/config", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create guest config request: %w", err)
	}
	req = req.WithContext(ctx)
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch guest config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("guest config failed (status %d)", resp.StatusCode)
	}
	var result struct {
		Configs []GuestConfig `json:"configs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode guest config: %w", err)
	}
	return result.Configs, nil
}

func listDevicesForStatus(ctx context.Context, baseURL, token string) ([]DeviceInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/devices/list", token, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list devices failed (status %d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		Devices []DeviceInfo `json:"devices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse devices: %w", err)
	}
	return result.Devices, nil
}

func fetchUserSettingsForStatus(baseURL, token string) (*UserSettings, error) {
	req, err := newBearerRequest("GET", baseURL+"/settings", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create settings request: %w", err)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch settings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("settings request failed (status %d)", resp.StatusCode)
	}
	var result struct {
		Settings UserSettings `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return &result.Settings, nil
}

func fetchLocalSharedUsers(client *http.Client, token string) (*statusSharedUsersResponse, error) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:18080/users", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// STREAMING DEBUG
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out statusSharedUsersResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func summarizeGuestAccess(cfg *GuestConfig) string {
	if cfg == nil {
		return "default: unlimited, all runners, all shared devices"
	}
	var parts []string
	mode := strings.TrimSpace(cfg.UsageMode)
	if mode == "" {
		mode = "always"
	}
	parts = append(parts, "mode="+mode)
	if cfg.DailyTokenLimit != nil && *cfg.DailyTokenLimit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%ds/day", *cfg.DailyTokenLimit))
	} else {
		parts = append(parts, "limit=unlimited")
	}
	if len(cfg.AllowedRunners) > 0 {
		parts = append(parts, "runners="+strings.Join(cfg.AllowedRunners, ","))
	} else {
		parts = append(parts, "runners=all")
	}
	if preset := guestResourcePreset(cfg); preset != "" {
		parts = append(parts, "preset="+preset)
	}
	if cfg.ShareAllDevices != nil && *cfg.ShareAllDevices {
		parts = append(parts, "devices=all")
	} else if len(cfg.DeviceIDs) > 0 {
		parts = append(parts, fmt.Sprintf("devices=%d", len(cfg.DeviceIDs)))
	}
	if cfg.ShareAllMachines != nil && *cfg.ShareAllMachines {
		parts = append(parts, "machines=all")
	} else if len(cfg.MachineIDs) > 0 {
		parts = append(parts, fmt.Sprintf("machines=%d", len(cfg.MachineIDs)))
	}
	if cfg.UseHostAPIKeys != nil {
		parts = append(parts, fmt.Sprintf("hostkeys=%t", *cfg.UseHostAPIKeys))
	}
	if cfg.AllowGuestProvidedAPIKeys != nil {
		parts = append(parts, fmt.Sprintf("guestkeys=%t", *cfg.AllowGuestProvidedAPIKeys))
	}
	if cfg.RequireIsolation != nil {
		parts = append(parts, fmt.Sprintf("isolation=%t", *cfg.RequireIsolation))
	}
	return strings.Join(parts, "; ")
}

func statusUnixMilliOrDash(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

func statusDateTimeUnixMilliOrDash(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

func statusAgoOrDash(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return humanRoundDuration(time.Since(time.UnixMilli(ms)))
}

func filterHostShareSessionsForDevice(sessions []HostShareSessionInfo, deviceID string) []HostShareSessionInfo {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	filtered := make([]HostShareSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if strings.TrimSpace(session.HostDeviceID) != deviceID {
			continue
		}
		filtered = append(filtered, session)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].LastActivityAt != filtered[j].LastActivityAt {
			return filtered[i].LastActivityAt > filtered[j].LastActivityAt
		}
		return filtered[i].StartedAt > filtered[j].StartedAt
	})
	return filtered
}

func printStatusRemoteAccess(cfg *Config) {
	fmt.Println()
	fmt.Println("Remote access:")
	if strings.TrimSpace(cfg.DeviceID) == "" {
		fmt.Println("  Host-share: local device id unknown")
		return
	}

	sessions, err := FetchHostShareSessions(cfg.ConvexSiteURL, cfg.AuthToken, "host")
	if err != nil {
		fmt.Printf("  Host-share: unavailable (%v)\n", err)
		return
	}

	deviceSessions := filterHostShareSessionsForDevice(sessions, cfg.DeviceID)
	if len(deviceSessions) == 0 {
		fmt.Println("  Host-share: no active remote sessions on this device")
		return
	}

	fmt.Printf("  Host-share: %d active remote session(s) on this device\n", len(deviceSessions))
	fmt.Printf("  Last activity: %s (%s ago)\n",
		statusDateTimeUnixMilliOrDash(deviceSessions[0].LastActivityAt),
		statusAgoOrDash(deviceSessions[0].LastActivityAt),
	)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "    GUEST\tSTATUS\tSTARTED\tLAST ACTIVE\tEXPIRES")
	for _, session := range deviceSessions {
		guest := strings.TrimSpace(session.GuestEmail)
		if guest == "" {
			guest = strings.TrimSpace(session.GuestName)
		}
		if guest == "" {
			guest = session.SessionID
		}
		status := strings.TrimSpace(session.Status)
		if status == "" {
			status = "active"
		}
		fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n",
			guest,
			status,
			statusDateTimeUnixMilliOrDash(session.StartedAt),
			statusDateTimeUnixMilliOrDash(session.LastActivityAt),
			statusDateTimeUnixMilliOrDash(session.ExpiresAt),
		)
	}
	w.Flush()
}

func statusTimeOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return t.Local().Format("2006-01-02 15:04")
}

func shortStatusUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if len(userID) <= 8 {
		return userID
	}
	return userID[:8] + "..."
}

func runTmuxList() {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		// Fall back to local listing without adoption info
		if !tmuxAvailable() {
			fmt.Println("tmux is not installed.")
			return
		}
		mgr := &TmuxManager{
			adopted:  make(map[string]string),
			pollStop: make(map[string]context.CancelFunc),
		}
		sessions, err := mgr.ListTmuxSessions()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		printTmuxSessions(sessions)
		return
	}

	// Try to get from running agent (has adoption info)
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := newBearerRequest("GET", "http://127.0.0.1:18080/tmux/sessions", cfg.AuthToken, nil)
	if req != nil {
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()

			// STREAMING DEBUG
			if resp.StatusCode == 200 {
				var body struct {
					Sessions []TmuxSession `json:"sessions"`
				}
				if json.NewDecoder(resp.Body).Decode(&body) == nil {
					printTmuxSessions(body.Sessions)
					return
				}
			}
		}
	}

	// Fallback to local
	if !tmuxAvailable() {
		fmt.Println("tmux is not installed.")
		return
	}
	mgr := &TmuxManager{
		adopted:  make(map[string]string),
		pollStop: make(map[string]context.CancelFunc),
	}
	sessions, _ := mgr.ListTmuxSessions()
	printTmuxSessions(sessions)
}

func printTmuxSessions(sessions []TmuxSession) {
	if len(sessions) == 0 {
		fmt.Println("No tmux sessions found.")
		return
	}
	for _, s := range sessions {
		agent := s.AgentType
		if agent == "" {
			agent = "shell"
		}
		relation := s.Relationship
		if relation == "" {
			relation = "unrelated"
		}

		fmt.Printf("  %-20s  %-12s  %-18s  %d window(s)", s.Name, agent, relation, s.Windows)
		if s.TaskID != "" {
			fmt.Printf("  task=%s", s.TaskID)
		}
		if s.Attached {
			fmt.Printf("  (attached)")
		}
		fmt.Println()
	}
}

func runTmuxAdopt(sessionName string) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}

	body := fmt.Sprintf(`{"session":%q}`, sessionName)
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := newBearerRequest("POST", "http://127.0.0.1:18080/tmux/adopt", cfg.AuthToken, strings.NewReader(body))
	if req == nil {
		fmt.Fprintln(os.Stderr, "Failed to create request")
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Agent not reachable: %v\nMake sure 'yaver serve' is running.\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != 200 {
		errMsg := "unknown error"
		if e, ok := result["error"].(string); ok {
			errMsg = e
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
		os.Exit(1)
	}

	taskID, _ := result["taskId"].(string)
	fmt.Printf("Adopted tmux session %q as task %s\n", sessionName, taskID)
}

func runTmuxDetach(taskID string) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}

	body := fmt.Sprintf(`{"taskId":%q}`, taskID)
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := newBearerRequest("POST", "http://127.0.0.1:18080/tmux/detach", cfg.AuthToken, strings.NewReader(body))
	if req == nil {
		fmt.Fprintln(os.Stderr, "Failed to create request")
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Agent not reachable: %v\nMake sure 'yaver serve' is running.\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != 200 {
		errMsg := "unknown error"
		if e, ok := result["error"].(string); ok {
			errMsg = e
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
		os.Exit(1)
	}

	fmt.Printf("Detached task %s — tmux session continues running.\n", taskID)
}

// ---------------------------------------------------------------------------
// doctor — system health check (like flutter doctor)
// ---------------------------------------------------------------------------

func runDoctor() {
	fmt.Println("Yaver Doctor")
	fmt.Printf("  Version: %s\n\n", version)
	printWSL2RequirementWarning()

	// Check for updates early (non-blocking, 3s timeout)
	latestCLI := fetchLatestCLIVersion()
	if latestCLI != "" && latestCLI != version && isNewerVersion(latestCLI, version) {
		fmt.Printf("  ⚠ Update available: %s → %s\n", version, latestCLI)
		fmt.Printf("    npm install -g yaver-cli@latest\n\n")
	}

	ok := 0
	warn := 0
	fail := 0

	check := func(name string) {
		fmt.Printf("  %-30s ", name)
	}
	pass := func(detail string) {
		fmt.Printf("✓ %s\n", detail)
		ok++
	}
	warning := func(detail string) {
		fmt.Printf("! %s\n", detail)
		warn++
	}
	failed := func(detail string) {
		fmt.Printf("✗ %s\n", detail)
		fail++
	}

	// 1. Config
	fmt.Println("── Configuration ──")
	cfg, err := LoadConfig()
	if err != nil {
		check("Config file")
		failed(fmt.Sprintf("Error: %v", err))
	} else {
		check("Config file")
		p, _ := ConfigPath()
		pass(p)
	}

	check("Version")
	if latestCLI != "" && latestCLI != version && isNewerVersion(latestCLI, version) {
		warning(fmt.Sprintf("%s (latest: %s)", version, latestCLI))
	} else if latestCLI != "" {
		pass(fmt.Sprintf("%s (up to date)", version))
	} else {
		pass(fmt.Sprintf("%s (could not check for updates)", version))
	}

	// 2. Auth
	fmt.Println("\n── Authentication ──")
	if cfg == nil || cfg.AuthToken == "" {
		check("Auth token")
		failed("Not signed in — run 'yaver auth'")
	} else {
		check("Auth token")
		pass("Present")

		check("Token validation")
		client := &http.Client{Timeout: 5 * time.Second}
		req, _ := newBearerRequest("GET", cfg.ConvexSiteURL+"/auth/validate", cfg.AuthToken, nil)
		if req != nil {
			resp, err := client.Do(req)
			if err != nil {
				failed(fmt.Sprintf("Network error: %v", err))
			} else {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					pass("Valid")
				} else if resp.StatusCode == 401 {
					failed("Expired — run 'yaver auth'")
				} else {
					warning(fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
		}

		check("Device ID")
		if cfg.DeviceID != "" {
			pass(cfg.DeviceID[:8] + "...")
		} else {
			failed("Not set — run 'yaver serve' to register")
		}

		check("Backend")
		if cfg.ConvexSiteURL != "" {
			pass(cfg.ConvexSiteURL)
		} else {
			failed("Not configured")
		}
	}

	// 3. Agent process
	fmt.Println("\n── Agent ──")
	agentPID, agentRunning := isAgentRunning()
	check("Agent process")
	if agentRunning {
		pass(fmt.Sprintf("Running (PID %d)", agentPID))
	} else {
		warning("Not running — start with 'yaver serve'")
	}

	check("Tmux")
	if tmuxAvailable() {
		insideTmux := os.Getenv("TMUX") != ""
		if insideTmux {
			tmuxSession := os.Getenv("TMUX")
			// Extract session info
			parts := strings.Split(tmuxSession, ",")
			if len(parts) >= 1 {
				pass(fmt.Sprintf("available, running inside tmux (%s)", parts[0]))
			} else {
				pass("available, running inside tmux")
			}
		} else {
			pass("available, not inside tmux")
		}
	} else {
		warning("not installed — session adoption requires tmux")
	}

	check("HTTP server")
	statusClient := &http.Client{Timeout: 3 * time.Second}
	resp, err := statusClient.Get("http://127.0.0.1:18080/health")
	if err != nil {
		warning("Not reachable on port 18080")
	} else {
		resp.Body.Close()
		pass("Listening on :18080")
	}

	// Query agent for forked processes (used to classify sessions below)
	yaverPIDs := map[int]string{} // PID -> description of forked processes
	var runningTasks, totalTasks int
	if cfg != nil && cfg.AuthToken != "" {
		req, _ := newBearerRequest("GET", "http://127.0.0.1:18080/agent/status", cfg.AuthToken, nil)
		if req != nil {
			if sResp, sErr := statusClient.Do(req); sErr == nil {
				var statusBody struct {
					Status struct {
						RunnerProcesses []struct {
							PID     int    `json:"pid"`
							Command string `json:"command"`
						} `json:"runnerProcesses"`
						RunningTasks int `json:"runningTasks"`
						TotalTasks   int `json:"totalTasks"`
					} `json:"status"`
				}
				if json.NewDecoder(sResp.Body).Decode(&statusBody) == nil {
					for _, p := range statusBody.Status.RunnerProcesses {
						yaverPIDs[p.PID] = p.Command
					}
					runningTasks = statusBody.Status.RunningTasks
					totalTasks = statusBody.Status.TotalTasks
				}
				sResp.Body.Close()
			}
		}
	}

	check("Tasks")
	if len(yaverPIDs) > 0 {
		pass(fmt.Sprintf("%d running, %d total", runningTasks, totalTasks))
	} else {
		pass(fmt.Sprintf("idle (%d total)", totalTasks))
	}

	fmt.Println("\n── Device Sessions ──")
	check("Session binding")
	if cfg != nil && cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
		if err != nil {
			warning(fmt.Sprintf("Could not inspect devices: %v", err))
		} else {
			legacyCount := 0
			for _, device := range devices {
				if device.IsGuest {
					continue
				}
				if device.SessionBinding != "dedicated" {
					legacyCount++
				}
			}
			if legacyCount == 0 {
				pass("All owned devices use dedicated device sessions")
			} else {
				warning(fmt.Sprintf("%d owned device(s) still on legacy shared sessions", legacyCount))
			}
			for _, device := range devices {
				if device.IsGuest {
					continue
				}
				check("  " + device.Name)
				switch device.SessionBinding {
				case "dedicated":
					pass("dedicated device session")
				default:
					warning("legacy shared session — restart/serve this machine once to rotate it")
				}
			}
		}
	} else {
		warning("Sign in first to inspect device session binding")
	}

	if runtime.GOOS == "darwin" {
		fmt.Println("\n── macOS Permissions ──")
		check("Permission onboarding")
		if cfg != nil && cfg.MacOSPermissionOnboardingDone {
			pass("Completed (rerun with `yaver permissions`)")
		} else {
			warning("Not completed — run `yaver permissions` once to front-load common macOS prompts")
		}
	}

	runDoctorMesh(check, pass, warning, failed)

	// 3b. Sessions — scan all agent processes and tmux sessions
	fmt.Println("\n── Sessions ──")
	agentBinaries := []string{"claude", "codex", "aider", "ollama", "goose", "amp", "opencode"}
	allSessions := findAllRunnerSessions(agentBinaries)

	// Also list tmux sessions if tmux is available
	var tmuxSessions []TmuxSession
	if tmuxAvailable() {
		// Try to get sessions from running agent (includes adoption info)
		if cfg != nil && cfg.AuthToken != "" {
			req, _ := newBearerRequest("GET", "http://127.0.0.1:18080/tmux/sessions", cfg.AuthToken, nil)
			if req != nil {
				if tResp, tErr := statusClient.Do(req); tErr == nil {
					var tmuxBody struct {
						Sessions []TmuxSession `json:"sessions"`
					}
					if json.NewDecoder(tResp.Body).Decode(&tmuxBody) == nil {
						tmuxSessions = tmuxBody.Sessions
					}
					tResp.Body.Close()
				}
			}
		}
		// Fallback: list locally (won't have adoption info)
		if tmuxSessions == nil {
			tmpTmuxMgr := &TmuxManager{
				adopted:  make(map[string]string),
				pollStop: make(map[string]context.CancelFunc),
			}
			tmuxSessions, _ = tmpTmuxMgr.ListTmuxSessions()
		}
	}

	hasAnySessions := len(allSessions) > 0 || len(tmuxSessions) > 0
	if !hasAnySessions {
		check("Agent sessions")
		pass("No agent sessions running")
	}

	// Show direct processes grouped by binary
	if len(allSessions) > 0 {
		grouped := map[string][]sessionProcess{}
		for _, s := range allSessions {
			grouped[s.BinaryName] = append(grouped[s.BinaryName], s)
		}
		for _, binaryName := range agentBinaries {
			sessions, exists := grouped[binaryName]
			if !exists {
				continue
			}
			for _, s := range sessions {
				relation := "independent"
				if _, forked := yaverPIDs[s.PID]; forked {
					relation = "forked"
				} else if agentRunning && isDescendantOf(s.PID, agentPID) {
					relation = "forked"
				}
				cmd := s.Command
				if len(cmd) > 50 {
					cmd = cmd[:50] + "..."
				}
				label := fmt.Sprintf("%s (PID %d)", binaryName, s.PID)
				check(label)
				switch relation {
				case "forked":
					if desc, ok := yaverPIDs[s.PID]; ok {
						pass(fmt.Sprintf("yaver — %s", desc))
					} else {
						pass("yaver (child process)")
					}
				default:
					warning(fmt.Sprintf("independent — %s", cmd))
				}
			}
		}
	}

	// Show tmux sessions
	if len(tmuxSessions) > 0 {
		for _, ts := range tmuxSessions {
			agent := ts.AgentType
			if agent == "" {
				agent = "shell"
			}
			label := fmt.Sprintf("tmux:%s", ts.Name)
			check(label)
			detail := fmt.Sprintf("%s, %d window(s)", agent, ts.Windows)
			if ts.Attached {
				detail += ", attached"
			}
			switch ts.Relationship {
			case "forked-by-yaver":
				pass(fmt.Sprintf("yaver — %s", detail))
			case "adopted":
				pass(fmt.Sprintf("adopted (task %s) — %s", ts.TaskID, detail))
			default:
				warning(fmt.Sprintf("independent — %s", detail))
			}
		}
	}

	// 4. AI Runners
	fmt.Println("\n── AI Runners ──")
	runners := []struct {
		id      string
		name    string
		cmd     string
		install string
	}{
		{"claude", "Claude Code", "claude", "npm install -g @anthropic-ai/claude-code"},
		{"codex", "OpenAI Codex", "codex", "npm install -g @openai/codex"},
		{"aider", "Aider", "aider", "pip install aider-chat"},
		{"ollama", "Ollama", "ollama", "brew install ollama (or https://ollama.com)"},
		{"goose", "Goose", "goose", "pip install goose-ai"},
		{"amp", "Amp", "amp", "npm install -g @anthropic/amp"},
		{"opencode", "OpenCode", "opencode", "npm install -g opencode-ai"},
	}

	for _, r := range runners {
		check(r.name + " (" + r.cmd + ")")
		path, err := osexec.LookPath(r.cmd)
		if err != nil {
			warning(fmt.Sprintf("Not installed — %s", r.install))
		} else {
			// Try to get version
			runnerCfg := GetRunnerConfig(r.id)
			out, verr := osexec.Command(r.cmd, "--version").CombinedOutput()
			if verr == nil {
				ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
				if len(ver) > 60 {
					ver = ver[:60]
				}
				level, detail := runnerDoctorDetail(runnerCfg, ".", path, ver)
				if level == "warn" {
					warning(detail)
				} else {
					pass(detail)
				}
			} else {
				level, detail := runnerDoctorDetail(runnerCfg, ".", path, "")
				if level == "warn" {
					warning(detail)
				} else {
					pass(detail)
				}
			}
		}
	}

	fmt.Println("\n── Machine Onboarding ──")
	for _, provider := range collectMachineOnboardingStatus().Providers {
		check(provider.Name)
		switch machineOnboardingDoctorLevel(provider) {
		case "pass":
			pass(machineOnboardingDoctorDetail(provider))
		case "warn":
			warning(machineOnboardingDoctorDetail(provider))
		default:
			failed(machineOnboardingDoctorDetail(provider))
		}
	}

	// 5. Relay servers
	fmt.Println("\n── Relay Servers ──")
	if cfg != nil && len(cfg.RelayServers) > 0 {
		relayClient := &http.Client{Timeout: 5 * time.Second}
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			check("Relay: " + label)
			start := time.Now()
			resp, err := relayClient.Get(rs.HttpURL + "/health")
			rtt := time.Since(start)
			if err != nil {
				failed("Unreachable")
			} else {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					pw := ""
					if rs.Password != "" {
						pw = ", password set"
					}
					pass(fmt.Sprintf("OK (%dms%s)", rtt.Milliseconds(), pw))
				} else {
					failed(fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
		}
	} else {
		check("Relay servers")
		warning("None configured — add with 'yaver relay add <url>'")
	}

	// 6. Network
	fmt.Println("\n── Network ──")
	check("Local IP")
	ip := getLocalIP()
	if ip != "" {
		pass(ip)
	} else {
		warning("Could not determine")
	}

	check("Internet connectivity")
	_, err = http.Get("https://yaver.io")
	if err != nil {
		failed("Cannot reach yaver.io")
	} else {
		pass("OK")
	}

	// 7. Voice / Speech
	fmt.Println("\n── Voice ──")
	if cfg != nil && cfg.Speech != nil && cfg.Speech.Provider != "" {
		check("Speech provider")
		pass(cfg.Speech.Provider)

		if cfg.Speech.Provider == "whisper" || cfg.Speech.Provider == "on-device" {
			check("Whisper binary")
			if p, err := osexec.LookPath("whisper-cpp"); err == nil {
				pass(p)
			} else if p, err := osexec.LookPath("whisper"); err == nil {
				pass(p)
			} else {
				warning("Not found — install: brew install whisper-cpp")
			}
		} else {
			check("API key")
			if cfg.Speech.APIKey != "" {
				pass("Set")
			} else {
				failed(fmt.Sprintf("Not set — run: yaver config set speech.api_key <key>"))
			}
		}

		check("TTS")
		if cfg.Speech.TTSEnabled {
			switch runtime.GOOS {
			case "darwin":
				if _, err := osexec.LookPath("say"); err == nil {
					pass("Enabled (macOS say)")
				} else {
					warning("Enabled but 'say' not found")
				}
			case "linux":
				if _, err := osexec.LookPath("espeak"); err == nil {
					pass("Enabled (espeak)")
				} else if _, err := osexec.LookPath("spd-say"); err == nil {
					pass("Enabled (spd-say)")
				} else {
					warning("Enabled but no TTS engine found (install espeak)")
				}
			default:
				warning("Enabled (no TTS engine available on this OS)")
			}
		} else {
			pass("Disabled")
		}

		check("Audio recording")
		if _, err := osexec.LookPath("rec"); err == nil {
			pass("sox/rec available")
		} else if _, err := osexec.LookPath("sox"); err == nil {
			pass("sox available")
		} else if _, err := osexec.LookPath("ffmpeg"); err == nil {
			pass("ffmpeg available")
		} else {
			warning("No recording tool — install sox (brew install sox)")
		}
	} else {
		check("Speech provider")
		warning("Not configured — run: yaver config set speech.provider <whisper|openai|deepgram|assemblyai>")
	}

	// 8. Hermes / Super-host
	fmt.Println("\n── Hermes Reload Stack ──")
	nodePath, nodeVersion := detectManagedOrSystemNode()
	check("Node.js runtime")
	if nodePath != "" {
		pass(fmt.Sprintf("%s (%s)", nodePath, nodeVersion))
	} else {
		failed("not installed — run `yaver install mobile`")
	}

	check("Embedded hermesc")
	hermesReady := false
	if summary, err := embeddedHermescSummary(); err == nil {
		pass(summary)
		hermesReady = true
	} else {
		failed(err.Error())
	}

	check("Hermes reload path")
	if nodePath != "" && hermesReady {
		pass("ready for React Native / Expo bundle reload into Yaver mobile from Linux, WSL, or macOS")
	} else {
		warning("install the mobile stack with `yaver install mobile` to enable Open in Yaver bundle reload")
	}

	check("SDK manifest BC version")
	manifestPath := ""
	manifestBC := 0
	// Check common locations for sdk-manifest.json
	for _, candidate := range []string{
		"mobile/sdk-manifest.json",
		"sdk-manifest.json",
	} {
		if data, err := os.ReadFile(candidate); err == nil {
			var m struct {
				Hermes struct {
					BytecodeVersion int `json:"bytecodeVersion"`
				} `json:"hermes"`
			}
			if json.Unmarshal(data, &m) == nil {
				manifestBC = m.Hermes.BytecodeVersion
				manifestPath = candidate
				break
			}
		}
	}
	if manifestBC > 0 {
		pass(fmt.Sprintf("BC%d (%s)", manifestBC, manifestPath))
	} else {
		warning("sdk-manifest.json not found in cwd")
	}

	fmt.Println("\n── Unity ──")
	check("Unity Editor")
	if unityPath, unityVer := detectUnityEditor(); unityPath != "" {
		if unityVer != "" && unityVer != "unknown" {
			pass(fmt.Sprintf("%s (%s)", unityPath, unityVer))
		} else {
			pass(unityPath)
		}
	} else {
		warning("not detected — Unity SDK feedback loop still works for source integration, but local editor/build tooling was not found")
	}

	check("Unity projects")
	unityCount := 0
	projectDetails := []string{}
	for _, p := range scanMobileProjects() {
		if p.Framework != "unity" {
			continue
		}
		unityCount++
		detail := p.Name
		if p.SDKVersion != "" {
			detail += " (" + p.SDKVersion + ")"
		}
		projectDetails = append(projectDetails, detail)
	}
	if unityCount == 0 {
		warning("no Unity projects detected in discovery roots")
	} else {
		pass(fmt.Sprintf("%d detected — %s", unityCount, strings.Join(projectDetails, ", ")))
	}

	check("Unity fast iteration path")
	if unityCount > 0 {
		pass("feedback SDK + relay/tailscale/vpn can support remote vibing, content refresh, scene reload, and redeploy workflows")
	} else {
		warning("detect a Unity project first, then run `yaver sdk add feedback --platform unity`")
	}

	// 8. Local CI integrations (yaver-test-sdk M4+)
	// What the dev needs on their own machine to run the embedded test
	// runner against the various targets — so they can stop paying for
	// BrowserStack/Sauce/Percy and use their own laptop instead.
	fmt.Println("\n── Local CI integrations (yaver-test-sdk) ──")

	// Chrome / Chromium for `target: web`
	check("Chrome / Chromium")
	if path, ver := detectChromeForCI(); path != "" {
		pass(fmt.Sprintf("%s (%s)", path, ver))
	} else {
		warning("not found — install Google Chrome or Chromium for `yaver test run` web target")
	}

	// Firefox (optional second browser)
	check("Firefox")
	if path, ver := detectBinaryWithVersion("firefox", "--version"); path != "" {
		pass(fmt.Sprintf("%s (%s)", path, ver))
	} else {
		warning("not installed (optional — only needed for cross-browser snapshots)")
	}

	// Xcode + simctl for iOS Simulator (M5)
	check("Xcode / simctl")
	if runtime.GOOS == "darwin" {
		if path, _ := detectBinaryWithVersion("xcrun", "--version"); path != "" {
			// `xcrun simctl help` exits 0 even when only the placeholder
			// CommandLineTools shim is installed; check for a real Xcode
			// developer dir to avoid false positives.
			out, _ := osexec.Command("xcode-select", "-p").Output()
			devPath := strings.TrimSpace(string(out))
			if devPath != "" && !strings.HasSuffix(devPath, "CommandLineTools") {
				pass(fmt.Sprintf("xcode-select -p: %s", devPath))
			} else {
				warning("only Command Line Tools installed — full Xcode required for iOS Simulator (M5)")
			}
		} else {
			warning("xcrun not found — install Xcode from the App Store for iOS Simulator (M5)")
		}
	} else {
		warning("not on macOS — iOS Simulator unavailable (use a Mac)")
	}

	// Android SDK (emulator + adb) for Android Emulator (M5)
	check("Android SDK (adb)")
	if path, ver := detectBinaryWithVersion("adb", "--version"); path != "" {
		pass(fmt.Sprintf("%s (%s)", path, firstLineRaw(ver)))
	} else {
		warning("adb not found — install Android Studio / Android SDK for Android Emulator (M5)")
	}
	check("Android emulator")
	if path, _ := osexec.LookPath("emulator"); path != "" {
		out, _ := osexec.Command(path, "-list-avds").Output()
		avds := strings.TrimSpace(string(out))
		if avds != "" {
			pass(fmt.Sprintf("%s — AVDs: %s", path, strings.ReplaceAll(avds, "\n", ", ")))
		} else {
			warning(fmt.Sprintf("%s present but no AVDs created (run `avdmanager create avd`)", path))
		}
	} else {
		warning("not found — install via `sdkmanager emulator`")
	}

	// Appium (optional — agent will eventually drive it directly, but
	// today the dev may already be using it)
	check("Appium")
	if path, ver := detectBinaryWithVersion("appium", "--version"); path != "" {
		pass(fmt.Sprintf("%s (%s)", path, ver))
	} else {
		warning("not installed (optional — yaver-test-sdk M5 will embed the WebDriver bridge)")
	}

	// Selenium server (almost always optional — chromedp speaks CDP
	// directly, so most users will never need this)
	check("Selenium server")
	if path, _ := detectBinaryWithVersion("selenium-server", "--version"); path != "" {
		pass(path)
	} else if path, _ := osexec.LookPath("selenium-side-runner"); path != "" {
		pass(path)
	} else {
		warning("not installed (optional — chromedp drives Chrome via CDP, no Selenium needed)")
	}

	// Node, npm, npx — only needed if the dev still wants to run
	// Playwright / Cypress as a fallback for things yaver-test-sdk
	// doesn't cover yet.
	check("Node.js")
	if path, ver := detectManagedOrSystemNode(); path != "" {
		pass(fmt.Sprintf("%s (%s)", path, ver))
	} else {
		warning("not installed (optional — only for legacy Playwright/Cypress fallback)")
	}

	// E2 — Headless-machine checks (Tailscale / cloudflared /
	// SMART / recovery / tunnels). These surface the solo-dev
	// "is the Mac mini upstairs actually reachable" story.
	fmt.Println("\n── Headless connectivity ──")

	check("Tailscale")
	if ts := DetectTailscale(); ts != nil && ts.Running && ts.Self != nil {
		pass(fmt.Sprintf("up (%s, addrs: %s)", ts.Self.HostName, strings.Join(ts.Self.Addrs, ", ")))
	} else {
		warning("not running (optional — alternative to relay)")
	}

	check("cloudflared")
	if path, err := osexec.LookPath("cloudflared"); err == nil {
		pass(path)
	} else {
		warning("not installed (run `yaver tunnel cloudflare wizard` to set up a public tunnel)")
	}

	check("ffmpeg (Loom screen recording)")
	if path, err := osexec.LookPath("ffmpeg"); err == nil {
		pass(path)
	} else {
		warning("not installed — brew install ffmpeg   (required by /loom/start)")
	}

	check("asciinema (terminal recording)")
	if path, err := osexec.LookPath("asciinema"); err == nil {
		pass(path)
	} else {
		warning("not installed (optional) — brew install asciinema")
	}

	check("gh CLI (newsletter compose-from-git)")
	if path, err := osexec.LookPath("gh"); err == nil {
		pass(path)
	} else {
		warning("not installed (optional) — https://cli.github.com/")
	}

	check("glab CLI")
	if path, err := osexec.LookPath("glab"); err == nil {
		pass(path)
	} else {
		warning("not installed (optional) — https://gitlab.com/gitlab-org/cli")
	}

	check("Cloudflare Tunnels")
	if cfg != nil && len(cfg.CloudflareTunnels) > 0 {
		for _, t := range cfg.CloudflareTunnels {
			label := t.Label
			if label == "" {
				label = t.ID
			}
			pass(fmt.Sprintf("%s → %s", label, t.URL))
		}
	} else {
		warning("none configured")
	}

	recoveryPosture := computeRecoveryTransportPosture(cfg)
	check("Remote recovery transport")
	if recoveryPosture.HasPrivateTransport {
		pass(recoveryPosture.Summary)
	} else {
		warning(recoveryPosture.Summary)
	}

	check("Public recovery exposure")
	if recoveryPosture.PublicDirectRecoveryClosed {
		pass("direct public HTTP recovery is disabled; mobile can use " + formatRecoveryTransports(recoveryPosture.MobileApprovedTransports) + ", web can use " + formatRecoveryTransports(recoveryPosture.WebApprovedTransports))
	} else {
		warning("direct public HTTP recovery is enabled (default). Set `yaver config set require-private-recovery true` or `yaver serve --recovery-policy=private` to restrict /auth/recover.")
	}

	check("Bootstrap secret (remote recovery)")
	if cfg != nil && cfg.BootstrapSecretHash != "" {
		pass("configured")
	} else {
		warning("not set — run `yaver init` or `yaver config bootstrap-secret <value>`")
	}

	check("Mobile auth recovery")
	if cfg != nil && cfg.BootstrapSecretHash != "" && recoveryPosture.PublicDirectRecoveryClosed && recoveryPosture.HasPrivateTransport {
		pass("/auth/recover ready over private paths only")
	} else if cfg != nil && cfg.BootstrapSecretHash != "" && recoveryPosture.PublicDirectRecoveryClosed {
		warning("/auth/recover is private-only, but no approved private recovery transport is present. Add Tailscale, a private relay, or an HTTPS Cloudflare Tunnel.")
	} else if cfg != nil && cfg.BootstrapSecretHash != "" {
		pass("/auth/recover ready on the main HTTP listener (default open mode)")
	} else {
		warning("bootstrap secret missing — mobile recovery will be limited. Guide: " + autoBootManualURL)
	}

	check("Headless power hardening")
	if power := detectHeadlessPowerStatus(); power == nil {
		warning("not supported on this OS — manual setup: " + autoBootManualURL)
	} else if power.ok {
		pass(power.summary)
	} else {
		warning(power.summary)
		for _, detail := range power.details {
			check("  hint")
			warning(detail)
		}
	}

	check("Auto-reboot how-to")
	pass(autoBootManualURL + " (firmware/BIOS auto power-on must be configured manually)")

	// SMART / disk health — run one fresh scan so doctor doesn't
	// print stale goroutine state when the dev invokes it right
	// after starting the agent.
	if !(cfg != nil && cfg.DisableDiskHealth) {
		fmt.Println("\n── Machine health ──")
		runDiskHealthScan()
		machineHealthMu.RLock()
		mh := machineHealth
		machineHealthMu.RUnlock()
		if len(mh.Filesystems) == 0 && len(mh.Drives) == 0 {
			check("SMART / disk")
			warning("unavailable on this platform")
		} else {
			for _, f := range mh.Filesystems {
				check("FS " + f.Mount)
				msg := fmt.Sprintf("%.0f%% used (%s free)", f.UsedPct, formatGB(f.FreeGB))
				switch {
				case f.UsedPct >= DiskCriticalPercent:
					failed(msg)
				case f.UsedPct >= DiskWarnPercent:
					warning(msg)
				default:
					pass(msg)
				}
			}
			for _, d := range mh.Drives {
				check("Drive " + d.Device)
				switch d.Health {
				case "passed":
					pass(fmt.Sprintf("SMART passed (%s)", d.Model))
				case "failing":
					failed(fmt.Sprintf("SMART failing (%s)", d.Model))
				default:
					warning(fmt.Sprintf("%s (%s)", d.Health, d.Model))
				}
			}
			for _, a := range mh.Alerts {
				check("Alert")
				warning(a)
			}
		}
	}

	// Rate limiter status
	if cfg != nil && cfg.RateLimit != nil && cfg.RateLimit.enabledOrDefault() {
		fmt.Println("\n── Rate limiter ──")
		check("Rate limit")
		pass(fmt.Sprintf("%d req/min, burst %d", cfg.RateLimit.RequestsPerMinute, cfg.RateLimit.BurstSize))
	}

	// Hardware status — battery + load
	hs := testkitHostStatus()
	check("Host status")
	if hs.OnBattery {
		warning(fmt.Sprintf("on battery (%d%%) — `yaver test schedule` will defer runs", hs.BatteryPct))
	} else {
		if hs.BatteryPct >= 0 {
			pass(fmt.Sprintf("AC power, %d%% battery, %d cores, load %.2f", hs.BatteryPct, hs.NumCPU, hs.LoadAvg1))
		} else {
			pass(fmt.Sprintf("AC power, %d cores, load %.2f", hs.NumCPU, hs.LoadAvg1))
		}
	}

	// Summary
	fmt.Println()
	fmt.Printf("Doctor summary: %d passed, %d warnings, %d failures\n", ok, warn, fail)
	if fail > 0 {
		fmt.Println("Run 'yaver auth' to fix authentication issues.")
	}
	if warn > 0 {
		fmt.Println("Install missing runners with their respective install commands above.")
	}
	if fail == 0 && warn == 0 {
		fmt.Println("Everything looks good!")
	}
}

func runDevices(args []string) {
	cfg := mustLoadAuthConfig()

	if len(args) > 0 {
		switch args[0] {
		case "remove", "rm", "delete":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: yaver devices remove <device-id>")
				os.Exit(1)
			}
			deviceID := strings.TrimSpace(args[1])
			if deviceID == "" {
				fmt.Fprintln(os.Stderr, "Device ID required")
				os.Exit(1)
			}
			if cfg.DeviceID != "" && deviceID == cfg.DeviceID {
				fmt.Fprintln(os.Stderr, "Refusing to remove the current device from itself. Run this from another device or use the mobile app.")
				os.Exit(1)
			}
			if err := RemoveDevice(cfg.ConvexSiteURL, cfg.AuthToken, deviceID); err != nil {
				fmt.Fprintf(os.Stderr, "Remove failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Removed device %s\n", deviceID)
			return
		default:
			fmt.Fprintln(os.Stderr, "Usage: yaver devices [remove <device-id>]")
			os.Exit(1)
		}
	}

	devices, err := listDevicesEnsuringAuth(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(devices) == 0 {
		fmt.Println("No devices registered.")
		fmt.Println("Run 'yaver serve' on your dev machine to register it.")
		return
	}

	mobileSectionDone := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		printMobileDevicesSection(&buf)
		mobileSectionDone <- buf.String()
	}()

	runnerByDevice := map[string]string{}
	var runnerMu sync.Mutex
	var runnerWG sync.WaitGroup
	for _, d := range devices {
		if !d.IsOnline {
			continue
		}
		d := d
		runnerWG.Add(1)
		go func() {
			defer runnerWG.Done()
			summary := summarizeDeviceRunners(cfg, d)
			runnerMu.Lock()
			runnerByDevice[d.DeviceID] = summary
			runnerMu.Unlock()
		}()
	}
	runnerWG.Wait()

	// Best-effort fetch of primary + secondary deviceId so the listing
	// can flag them. Failure here is silent — the user just sees the
	// list without ROLE annotations.
	var primaryID, secondaryID string
	if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		primaryID, _ = primaryGetCurrent(ctx, cfg.AuthToken, cfg.ConvexSiteURL)
		secondaryID, _ = secondaryGetCurrent(ctx, cfg.AuthToken, cfg.ConvexSiteURL)
		cancel()
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "ID\tROLE\tALIAS\tNAME\tPLATFORM\tSTATUS\tACCESS\tSESSION\tRUNNERS\tADDRESS")
	for _, d := range devices {
		status := "offline"
		if d.IsOnline {
			status = "online"
		}
		id := d.DeviceID
		if len(id) > 8 {
			id = id[:8]
		}
		alias := strings.TrimSpace(d.Alias)
		if alias == "" {
			alias = "-"
		}
		role := "-"
		switch d.DeviceID {
		case primaryID:
			role = "primary"
		case secondaryID:
			role = "secondary"
		}
		runners := runnerByDevice[d.DeviceID]
		if strings.TrimSpace(runners) == "" {
			runners = "-"
		}
		address := fmt.Sprintf("%s:%d", d.QuicHost, d.QuicPort)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			id, role, alias, d.Name, d.Platform, status, deviceAccessLabel(d), deviceSessionBindingLabel(d), runners, address)
	}
	w.Flush()

	if mobileSection := <-mobileSectionDone; strings.TrimSpace(mobileSection) != "" {
		fmt.Print(mobileSection)
	}
}

// printMobileDevicesSection appends a "Mobile devices" block to
// `yaver devices`: USB-cable-attached and WiFi-paired phones/tablets
// with their model, OS version, SoC, and RAM. Local-only data; nothing
// here flows to Convex (see CLAUDE.md privacy contract).
//
// Skipped silently when nothing is attached AND no tooling is missing —
// an empty header would just be noise.
func printMobileDevicesSection(out io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	wired := append([]wireDevice{}, listIOSWireDevices(ctx)...)
	wired = append(wired, listAndroidWireDevices(ctx)...)
	wirelessReport := buildWirelessDetectReport(ctx)

	type mobileRow struct {
		Transport string
		Status    string
		Platform  string
		UDID      string
		Info      *mobileDeviceInfo
		Bare      wireDevice
	}
	var rows []mobileRow

	for _, d := range wired {
		rows = append(rows, mobileRow{
			Transport: "usb",
			Status:    "attached",
			Platform:  d.Platform,
			UDID:      d.UDID,
			Bare:      d,
		})
	}
	wiredUDIDs := map[string]bool{}
	for _, d := range wired {
		wiredUDIDs[d.UDID] = true
	}
	for _, d := range wirelessReport.Devices {
		if wiredUDIDs[d.UDID] {
			continue
		}
		rows = append(rows, mobileRow{
			Transport: "wifi",
			Status:    d.Status,
			Platform:  d.Platform,
			UDID:      d.UDID,
			Bare:      d.wireDevice,
		})
	}

	if len(rows) == 0 {
		hint := wirelessReport.Hint
		if hint == "" {
			return
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Mobile devices: (none attached)")
		fmt.Fprintln(out, "  →", hint)
		return
	}

	pairedIdx := []int{}
	pairedDevs := []wireDevice{}
	for i, r := range rows {
		if r.Status == "visible-unpaired" {
			continue
		}
		pairedIdx = append(pairedIdx, i)
		pairedDevs = append(pairedDevs, r.Bare)
	}
	enrichWireDevices(ctx, pairedDevs, 4)
	for j, idx := range pairedIdx {
		rows[idx].Info = pairedDevs[j].Info
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Mobile devices (USB + WiFi):")
	mw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer mw.Flush()
	fmt.Fprintln(mw, "TRANSPORT\tSTATUS\tPLATFORM\tUDID/SERIAL\tDEVICE\tOS\tSOC\tRAM")
	for _, r := range rows {
		device := "(unknown)"
		osLabel := "-"
		soc := "-"
		ram := "-"
		if r.Info != nil {
			switch {
			case r.Info.DeviceName != "":
				device = r.Info.DeviceName
			case r.Info.MarketingName != "":
				device = r.Info.MarketingName
			case r.Info.ModelCode != "":
				device = r.Info.ModelCode
			}
			if r.Info.OS != "" {
				osLabel = r.Info.OS
				if r.Info.OSVersion != "" {
					osLabel = r.Info.OS + " " + r.Info.OSVersion
				}
			}
			if r.Info.SoC != "" {
				soc = r.Info.SoC
			}
			if r.Info.RAMBytes > 0 {
				ram = formatBytes(int64(r.Info.RAMBytes))
			}
		} else if r.Bare.Name != "" {
			device = r.Bare.Name
		}
		fmt.Fprintf(mw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Transport, r.Status, r.Platform, r.UDID, device, osLabel, soc, ram)
	}
	if wirelessReport.VisibleCount > 0 {
		mw.Flush()
		fmt.Fprintln(out)
		fmt.Fprintln(out, "→ visible-unpaired entries: run `yaver android setup` to pair.")
	}
}

// resolveDevice picks a device from `devices` by user-supplied target,
// matched (in order) against alias, deviceId (full or 8-char prefix),
// then case-insensitive name. Returns ambiguity errors so the caller
// can prompt the user to be more specific. Used by `yaver ssh` and
// `yaver alias` so a single resolution rule applies everywhere.
func normalizeDeviceHint(target string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "@") && strings.Count(target, "@") == 1 {
		return strings.TrimPrefix(target, "@")
	}
	return target
}

func resolveDevice(target string, devices []DeviceInfo) (*DeviceInfo, error) {
	target = normalizeDeviceHint(target)
	if target == "" {
		return nil, fmt.Errorf("device identifier required")
	}
	lower := strings.ToLower(target)

	var aliasHits, idHits, nameHits []DeviceInfo
	for _, d := range devices {
		if d.Alias != "" && strings.ToLower(d.Alias) == lower {
			aliasHits = append(aliasHits, d)
			continue
		}
		if d.DeviceID == target {
			idHits = append(idHits, d)
			continue
		}
		if len(target) >= 4 && strings.HasPrefix(d.DeviceID, target) {
			idHits = append(idHits, d)
			continue
		}
		if strings.EqualFold(d.Name, target) {
			nameHits = append(nameHits, d)
		}
	}

	for _, bucket := range [][]DeviceInfo{aliasHits, idHits, nameHits} {
		if len(bucket) == 1 {
			d := bucket[0]
			return &d, nil
		}
		if len(bucket) > 1 {
			labels := make([]string, 0, len(bucket))
			for _, d := range bucket {
				labels = append(labels, fmt.Sprintf("%s (%s)", d.DeviceID[:8], d.Name))
			}
			return nil, fmt.Errorf("%q matches multiple devices: %s — use the full deviceId", target, strings.Join(labels, ", "))
		}
	}

	return nil, fmt.Errorf("no device matches %q (try `yaver devices` to list)", target)
}

// runAlias implements `yaver alias` — set / clear / list per-user
// device aliases. Aliases are stored in Convex (POST /devices/alias)
// so the dashboard, mobile app, and `yaver ssh` all see the same
// names. Per-user uniqueness is enforced server-side: the same alias
// cannot point at two of the caller's devices.
//
//	yaver alias                       — list current aliases
//	yaver alias set <id|name> <alias> — assign or rename
//	yaver alias rm  <id|name|alias>   — clear
func runAlias(args []string) {
	cfg := mustLoadAuthConfig()

	if len(args) == 0 {
		devices, err := listDevicesEnsuringAuth(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		any := false
		for _, d := range devices {
			if d.IsGuest || strings.TrimSpace(d.Alias) == "" {
				continue
			}
			any = true
			fmt.Printf("%-16s  %-20s  %s\n", d.Alias, d.Name, d.DeviceID)
		}
		if !any {
			fmt.Println("No aliases set. Use: yaver alias set <device-id-or-name> <alias>")
		}
		return
	}

	switch strings.ToLower(args[0]) {
	case "set", "rename":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: yaver alias set <device-id|name|current-alias> <new-alias>")
			os.Exit(1)
		}
		target := args[1]
		newAlias := strings.TrimSpace(args[2])
		devices, err := listDevicesEnsuringAuth(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		dev, err := resolveDevice(target, devices)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if dev.IsGuest {
			fmt.Fprintln(os.Stderr, "Cannot set alias on a shared/guest device — only the host can.")
			os.Exit(1)
		}
		if err := SetDeviceAlias(cfg.ConvexSiteURL, cfg.AuthToken, dev.DeviceID, newAlias); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Alias %q → %s (%s)\n", newAlias, dev.Name, dev.DeviceID[:8])
	case "rm", "remove", "clear", "delete", "unset":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver alias rm <device-id|name|alias>")
			os.Exit(1)
		}
		target := args[1]
		devices, err := listDevicesEnsuringAuth(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		dev, err := resolveDevice(target, devices)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := SetDeviceAlias(cfg.ConvexSiteURL, cfg.AuthToken, dev.DeviceID, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Alias cleared for %s (%s)\n", dev.Name, dev.DeviceID[:8])
	case "list", "ls":
		runAlias(nil)
	default:
		fmt.Fprintln(os.Stderr, "Usage: yaver alias [set|rm|list] ...")
		os.Exit(1)
	}
}

// runSSHWrap implements `yaver ssh <target> [extra ssh args]`. It
// resolves `<target>` against the user's devices (alias > deviceId
// prefix > name) and execs the system `ssh` (OpenSSH) binary against
// an IP it learned from one of three sources, in priority order:
//
//  1. `tailscale ip <hostname-or-alias>` — when Tailscale is reachable
//     and knows about the host. This handles "yaver test ephemeral"
//     style boxes that join the tailnet but never register a public
//     endpoint with Convex.
//  2. The device row from Convex — publicEndpoints first (clean DNS
//     via Cloudflare Tunnel etc.), then localIps that look like
//     Tailscale (100.64.0.0/10), then the first non-loopback localIp,
//     then quicHost.
//  3. The literal target as the SSH host — last-resort so the user
//     can still type `yaver ssh user@host` for an unregistered box.
//
// Any extra args after the target are passed through to ssh untouched
// (so `yaver ssh prod-mac -L 5432:localhost:5432` works). Named
// runSSHWrap (not runSSH) because multiregion_orchestrate.go already
// owns runSSH for the sshpass-based provisioning helper.
func runSSHWrap(args []string) {
	// Bare `yaver ssh` (no target) and `yaver ssh primary` both resolve
	// to userSettings.primaryDeviceId (the same value `yaver primary`
	// surfaces). `yaver ssh secondary` resolves the secondary slot the
	// same way. Empty default lets `yaver ssh -L 5432:...` work too —
	// passthrough flags after the implicit primary target.
	if len(args) == 0 || strings.EqualFold(strings.TrimSpace(args[0]), "primary") || strings.EqualFold(strings.TrimSpace(args[0]), "secondary") {
		slot := "primary"
		if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "secondary") {
			slot = "secondary"
		}
		resolved, err := resolveSSHSlot(slot)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		// Replace the slot token (or prepend, if there was none) with
		// the resolved device handle so the rest of the function flows
		// the normal alias/deviceId/name path.
		if len(args) == 0 {
			args = []string{resolved}
		} else {
			args = append([]string{resolved}, args[1:]...)
		}
	}

	target := args[0]
	passthrough := args[1:]

	user := ""
	hostPart := target
	if at := strings.LastIndex(target, "@"); at > 0 {
		user = target[:at]
		hostPart = target[at+1:]
	}

	var resolvedDevice *DeviceInfo
	if cfg, err := LoadConfig(); err == nil && cfg != nil && cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		if devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); err == nil {
			if dev, err := resolveDevice(hostPart, devices); err == nil {
				resolvedDevice = dev
			}
		}
	}

	// If the input was an alias ("test") and resolved to a device, try
	// resolving SSH against the device's canonical name FIRST so users
	// who already have a Host entry for the registered name (e.g.
	// `Host yaver-test-ephemeral 157.180.114.179`) get a hit. Falling
	// straight back to `ssh test` would skip that.
	resolutionHints := []string{hostPart}
	if resolvedDevice != nil {
		if alias := strings.TrimSpace(resolvedDevice.Alias); alias != "" && !strings.EqualFold(alias, hostPart) {
			resolutionHints = append(resolutionHints, alias)
		}
		if name := strings.TrimSpace(resolvedDevice.Name); name != "" && !strings.EqualFold(name, hostPart) {
			resolutionHints = append(resolutionHints, name)
		}
	}
	host := ""
	for _, hint := range resolutionHints {
		if h := resolveSSHHost(hint); h != "" {
			host = h
			break
		}
	}
	if host == "" && resolvedDevice != nil && strings.TrimSpace(resolvedDevice.Name) != "" {
		// Last fallback before raw input: hand ssh the device Name. The
		// user's ~/.ssh/config probably aliases the registered name
		// (`Host yaver-test-ephemeral …`).
		host = resolvedDevice.Name
	}
	if host == "" {
		// Couldn't resolve via Tailscale or Convex — fall through to
		// whatever the user typed. ssh will give a sensible error if
		// it's not actually a hostname.
		host = hostPart
	}

	dest := host
	if user != "" {
		dest = user + "@" + host
	} else if resolvedDevice != nil {
		if inferred := inferSSHUser(host, *resolvedDevice); inferred != "" {
			dest = inferred + "@" + host
		}
	}

	sshPath, err := osexec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ssh not found in PATH — install OpenSSH and try again.")
		os.Exit(1)
	}

	exitCode, errSummary := runSSHCapturingAuthFailure(sshPath, dest, passthrough)
	if exitCode == 0 {
		return
	}
	// Auto-bootstrap path: the SSH child told us "Permission denied
	// (publickey)" against a device we have a Yaver-side handle on.
	// Push the local pubkey via the remote agent's same-Convex-user
	// trust channel, then retry once. If the bootstrap is skipped or
	// fails, fall through to the original ssh exit.
	if exitCode == 255 && errSummary == sshFailAuth && resolvedDevice != nil {
		fmt.Fprintln(os.Stderr, "→ no SSH access yet — bootstrapping via yaver auth (same-user verified)")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		boot, bootErr := sshBootstrapDevice(ctx, resolvedDevice.DeviceID)
		cancel()
		if bootErr != nil {
			fmt.Fprintf(os.Stderr, "  bootstrap failed: %v\n", bootErr)
			os.Exit(exitCode)
		}
		if boot == nil || boot.SkipReason != "" {
			reason := "unavailable"
			if boot != nil && boot.SkipReason != "" {
				reason = boot.SkipReason
			}
			fmt.Fprintf(os.Stderr, "  bootstrap skipped: %s\n", reason)
			os.Exit(exitCode)
		}
		switch {
		case boot.Pushed:
			fmt.Fprintf(os.Stderr, "  pushed pubkey to %s (%s)\n", boot.RemoteName, boot.Fingerprint)
		case boot.AlreadyPresent:
			fmt.Fprintf(os.Stderr, "  pubkey already on %s (%s) — retrying\n", boot.RemoteName, boot.Fingerprint)
		}
		// If the user typed a bare hostname (no `user@`) and the
		// bootstrap learned the agent's actual OS user, pivot the
		// retry to that user — the pubkey lives in THAT user's
		// authorized_keys, not necessarily root's. Caller-supplied
		// `user@host` is left alone (their explicit choice wins).
		if user == "" && strings.TrimSpace(boot.RemoteOSUser) != "" {
			pivoted := boot.RemoteOSUser + "@" + host
			if pivoted != dest {
				fmt.Fprintf(os.Stderr, "  ssh user → %s (agent runs as %s)\n", boot.RemoteOSUser, boot.RemoteOSUser)
				dest = pivoted
			}
		}
		fmt.Fprintf(os.Stderr, "→ ssh %s\n", dest)
		cmd := osexec.Command(sshPath, sshArgsWithSurvivability(dest, passthrough)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*osexec.ExitError); ok {
				printSSHResolutionDiagnostic(target, dest, resolvedDevice)
				os.Exit(ee.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "ssh: %v\n", err)
			printSSHResolutionDiagnostic(target, dest, resolvedDevice)
			os.Exit(1)
		}
		return
	}
	printSSHResolutionDiagnostic(target, dest, resolvedDevice)
	os.Exit(exitCode)
}

// printSSHResolutionDiagnostic explains what `yaver ssh` resolved to
// and what other endpoints the device row carries — so a timeout or
// refused connection doesn't leave the user guessing why we picked
// the IP we did. Printed only when ssh exited non-zero AND we have a
// device row to draw from. No-op when we ssh'd to a literal hostname
// the user typed (no Yaver context to share).
func printSSHResolutionDiagnostic(input, attempted string, dev *DeviceInfo) {
	if dev == nil {
		return
	}
	tsUp := localTailscaleUp()

	// Pull the user portion off `attempted` so we can compare it to
	// what the remote agent reports. ssh-tried-as can come from an
	// explicit user@host, the inferred /info.osUser, or just the
	// process's $USER fallback.
	triedUser, triedHost := splitDestUserHost(attempted)
	if triedUser == "" {
		triedUser = strings.TrimSpace(os.Getenv("USER"))
	}

	fmt.Fprintf(os.Stderr, "\nyaver ssh: %s did not connect.\n", attempted)
	fmt.Fprintf(os.Stderr, "  device:  %s", strings.TrimSpace(dev.Name))
	if alias := strings.TrimSpace(dev.Alias); alias != "" {
		fmt.Fprintf(os.Stderr, " (alias %s)", alias)
	}
	fmt.Fprintf(os.Stderr, " — %s\n", dev.DeviceID)
	if strings.TrimSpace(dev.Platform) != "" {
		fmt.Fprintf(os.Stderr, "  platform: %s\n", dev.Platform)
	}
	if len(dev.LocalIps) > 0 {
		fmt.Fprintf(os.Stderr, "  IPs:     %s\n", strings.Join(dev.LocalIps, ", "))
	}
	if len(dev.PublicEndpoints) > 0 {
		fmt.Fprintf(os.Stderr, "  public:  %s\n", strings.Join(dev.PublicEndpoints, ", "))
	}
	if dev.QuicHost != "" {
		fmt.Fprintf(os.Stderr, "  quic:    %s\n", dev.QuicHost)
	}
	fmt.Fprintf(os.Stderr, "  tailscale: %s\n", tailscaleStateLabel(tsUp))

	// "Connection closed by … port 22" almost always means we hit
	// sshd as a user the remote box doesn't accept. If the remote
	// agent reports a different osUser than what we just tried,
	// surface it inline — saves the user from having to run
	// `yaver primary status` to discover it.
	if osUser := remoteAgentOSUser(dev.DeviceID, 2*time.Second); osUser != "" && triedUser != "" && !strings.EqualFold(osUser, triedUser) {
		host := triedHost
		if host == "" {
			host = attempted
		}
		fmt.Fprintf(os.Stderr, "  ssh tried as: %s — agent reports osUser=%s\n", triedUser, osUser)
		fmt.Fprintf(os.Stderr, "  hint: try `ssh %s@%s` (or `yaver ssh %s@%s`)\n", osUser, host, osUser, host)
	}
	if !tsUp {
		// Most-common cause of a Yaver-resolved address timing out:
		// device row carries a 100.x address for an overlay that's
		// down on this host. Spell it out so the user doesn't have
		// to read the rest of the table to figure it out.
		hadCGNAT := false
		for _, ip := range dev.LocalIps {
			if isCGNATTailscaleIP(ip) {
				hadCGNAT = true
				break
			}
		}
		if hadCGNAT {
			fmt.Fprintln(os.Stderr, "  hint: tailscale is down on this host. Start it (`tailscale up`) or use a LAN/public IP.")
		}
	}
	fmt.Fprintln(os.Stderr, "  hint: `yaver devices` lists every endpoint, or `ssh <user>@<other-ip>` directly.")
}

// splitDestUserHost pulls the last user@host pair out of dest. Empty
// triedUser when no `@` is present.
func splitDestUserHost(dest string) (user, host string) {
	at := strings.LastIndex(dest, "@")
	if at <= 0 {
		return "", strings.TrimSpace(dest)
	}
	return strings.TrimSpace(dest[:at]), strings.TrimSpace(dest[at+1:])
}

func tailscaleStateLabel(up bool) string {
	if up {
		return "up (100.x interface present)"
	}
	return "down or not authenticated (no 100.x interface on this host)"
}

// sshArgsWithSurvivability prepends keepalive + ControlMaster flags so
// `yaver ssh` survives short network blips (ServerAlive*) and reuses
// one TCP socket across rapid back-to-back invocations (10-min persist).
// `%C` is the hashed-connection token, kept short to stay under the
// 104-char Unix-socket-path limit even with long aliases.
func sshArgsWithSurvivability(dest string, passthrough []string) []string {
	args := []string{
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=~/.ssh/cm-%C",
		"-o", "ControlPersist=10m",
		dest,
	}
	return append(args, passthrough...)
}

// sshFailAuth is the sentinel returned by runSSHCapturingAuthFailure
// when the OpenSSH client exited with "Permission denied (publickey)".
// Distinguishing it from other 255-code failures (DNS, refused, host
// key mismatch) lets the bootstrap branch fire only when adding a
// pubkey would actually help.
const sshFailAuth = "auth"

// runSSHCapturingAuthFailure spawns ssh, streams stdout/stderr to the
// user verbatim, and returns (exitCode, summary). summary is
// sshFailAuth when stderr matched the publickey-denied marker; empty
// otherwise. exitCode 0 means success — the caller is done.
func runSSHCapturingAuthFailure(sshPath, dest string, passthrough []string) (int, string) {
	fmt.Fprintf(os.Stderr, "→ ssh %s\n", dest)
	cmd := osexec.Command(sshPath, sshArgsWithSurvivability(dest, passthrough)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	// Tee stderr through a small ring buffer so we can detect the
	// publickey-denied line without losing the user-visible output.
	pr, pw, perr := osPipe()
	if perr != nil {
		// Fallback: no capture, behave like before.
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*osexec.ExitError); ok {
				return ee.ExitCode(), ""
			}
			fmt.Fprintf(os.Stderr, "ssh: %v\n", err)
			return 1, ""
		}
		return 0, ""
	}
	cmd.Stderr = pw
	tail := &lastLinesBuffer{max: 4096}
	doneRelay := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				_, _ = os.Stderr.Write(buf[:n])
				tail.append(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(doneRelay)
	}()
	runErr := cmd.Run()
	_ = pw.Close()
	<-doneRelay
	_ = pr.Close()

	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*osexec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			fmt.Fprintf(os.Stderr, "ssh: %v\n", runErr)
			exitCode = 1
		}
	}
	if exitCode != 0 {
		stderrTail := tail.String()
		if strings.Contains(stderrTail, "Permission denied (publickey") ||
			strings.Contains(stderrTail, "Permission denied (publickey,") {
			return exitCode, sshFailAuth
		}
	}
	return exitCode, ""
}

// lastLinesBuffer keeps the last N bytes of stderr so we can sniff
// for known failure patterns without buffering the whole stream.
type lastLinesBuffer struct {
	buf []byte
	max int
}

func (b *lastLinesBuffer) append(p []byte) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
}

func (b *lastLinesBuffer) String() string { return string(b.buf) }

// osPipe wraps os.Pipe so the function above can be unit-tested with
// a fake pipe. Production path is just os.Pipe().
func osPipe() (*os.File, *os.File, error) { return os.Pipe() }

// resolveSSHPrimary returns the best handle to pass into the ssh
// resolution pipeline for `yaver ssh primary` / bare `yaver ssh`.
//
// Priority:
//  1. Convex userSettings.primaryDeviceId — explicit user choice.
//  2. The single owner device, if exactly one is registered (the
//     "I just ran yaver auth on my only machine" case).
//
// The returned string is whatever the device list considers the most
// stable handle: alias if present, otherwise device name, otherwise
// the deviceId. resolveDevice in the caller then takes it from there.
func resolveSSHPrimary() (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	primaryID, _ := primaryGetCurrent(ctx, cfg.AuthToken, convex)
	devices, derr := listDevices(convex, cfg.AuthToken)
	if derr != nil {
		return "", fmt.Errorf("could not list devices: %w", derr)
	}
	pickHandle := func(d DeviceInfo) string {
		if a := strings.TrimSpace(d.Alias); a != "" {
			return a
		}
		if n := strings.TrimSpace(d.Name); n != "" {
			return n
		}
		return d.DeviceID
	}
	primaryID = strings.TrimSpace(primaryID)
	if primaryID != "" {
		for _, d := range devices {
			if d.DeviceID == primaryID {
				return pickHandle(d), nil
			}
		}
		return "", fmt.Errorf("primary device %s is set but not in the device list — run 'yaver primary clear' to reset", primaryID[:min(8, len(primaryID))])
	}
	// No explicit primary — fall back to "exactly one owner device" so
	// fresh single-device users don't have to set one before they can
	// `yaver ssh`.
	var owned []DeviceInfo
	for _, d := range devices {
		if !d.IsGuest {
			owned = append(owned, d)
		}
	}
	if len(owned) == 0 {
		return "", fmt.Errorf("no registered owner devices — run 'yaver serve' on a machine to register it")
	}
	if len(owned) > 1 {
		// More than one owned device + no primary chosen yet: if we have
		// a real terminal, run the picker right here so the user doesn't
		// hit a dead-end error. Non-TTY (scripts, CI) gets the original
		// error so automation surfaces it.
		if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Fprintln(os.Stderr, "→ no primary device set — picking one now")
			runPrimaryPick(context.Background())
			// runPrimaryPick already wrote primaryDeviceId to Convex on
			// success; re-resolve so the rest of the function sees it.
			primaryID, _ = primaryGetCurrent(ctx, cfg.AuthToken, convex)
			primaryID = strings.TrimSpace(primaryID)
			if primaryID != "" {
				for _, d := range devices {
					if d.DeviceID == primaryID {
						return pickHandle(d), nil
					}
				}
			}
		}
		return "", fmt.Errorf("no primary device set and you have %d devices — run 'yaver primary pick' first", len(owned))
	}
	return pickHandle(owned[0]), nil
}

// resolveSSHHost takes a literal target (alias, deviceId, name, or
// raw hostname) and returns the best IP/hostname for ssh. Returns ""
// if it has nothing better than the input.
func resolveSSHHost(target string) string {
	target = normalizeDeviceHint(target)
	if target == "" {
		return ""
	}

	var tsPath string
	if path, err := osexec.LookPath("tailscale"); err == nil {
		tsPath = path
	}
	// Detect locally-up Tailscale by interface inspection — works
	// regardless of whether the `tailscale` CLI is on PATH (Docker
	// hosts, headless Linux, etc.). When false, every Tailscale path
	// below is short-circuited so we don't hand back an unreachable
	// 100.x address that ssh will block on for 30 s.
	tsUp := localTailscaleUp()

	// 1. Convex device row first. We need it up front to compare the
	//    device's announced LAN IPs against our local subnet — that
	//    comparison is what unlocks the LAN-preferred path. When
	//    we're not signed in or Convex is down, dev stays nil and we
	//    fall through to the historical Tailscale-then-fail behavior.
	var dev *DeviceInfo
	if cfg, err := LoadConfig(); err == nil && cfg != nil && cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		if devices, derr := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); derr == nil {
			if d, rerr := resolveDevice(target, devices); rerr == nil {
				dev = d
			}
		}
	}

	// 2. LAN IP on our /24 wins over everything. Faster than the
	//    Tailscale overlay, doesn't depend on tailscaled, survives
	//    `tailscale down` / Wi-Fi roams.
	if dev != nil {
		if ip := pickReachableLanIP(dev.LocalIps); ip != "" {
			return ip
		}
	}

	// 3. Tailscale-by-hint (cheap, handles devices not in Convex).
	//    Gated on local Tailscale being up.
	if tsUp {
		if ip := lookupTailscaleIP(tsPath, target); ip != "" {
			return ip
		}
	}

	if dev == nil {
		return ""
	}

	// 4. Tailscale via device alias / canonical name (same gate).
	if tsUp {
		if ip := lookupTailscaleIP(tsPath, dev.Alias, dev.Name, strings.TrimSuffix(dev.Name, ".local")); ip != "" {
			return ip
		}
	}

	// 5. Public endpoints. Skip Yaver's HTTP relay hostnames —
	//    `<uuid>.yaver.io` / `<uuid>.dev.yaver.io` terminate HTTPS
	//    only, so handing ssh one of those just hangs. Strip the
	//    scheme AND the port: a public endpoint carries the agent's
	//    HTTP API port (e.g. `157.180.114.179:18080`), which is
	//    meaningless to ssh — OpenSSH has no `host:port` syntax and
	//    would treat the whole string as a literal hostname
	//    ("Could not resolve hostname 157.180.114.179:18080"). ssh
	//    connects on port 22 (or whatever ~/.ssh/config says), so we
	//    return only the bare host.
	for _, raw := range dev.PublicEndpoints {
		ep := strings.TrimPrefix(raw, "https://")
		ep = strings.TrimPrefix(ep, "http://")
		ep = strings.TrimSuffix(ep, "/")
		ep = bareHostNoPort(ep)
		if ep == "" || isYaverHTTPRelayHost(ep) {
			continue
		}
		return ep
	}

	// 6. Tailscale CGNAT addresses from the device row — only when
	//    our overlay is up. Without this gate, a host with Tailscale
	//    stopped would still get back a 100.x IP and waste 30 s on
	//    "Operation timed out". Surfaces as fast "no host" instead.
	if tsUp {
		for _, ip := range dev.LocalIps {
			if isCGNATTailscaleIP(ip) {
				return ip
			}
		}
	}

	// 7. Any other LAN IP from the device row, even off-subnet —
	//    better than nothing; ssh will produce a useful error if it's
	//    really unreachable. Tailscale CGNAT is filtered when local
	//    overlay is down (already handled above when up).
	for _, ip := range dev.LocalIps {
		if ip == "" || strings.HasPrefix(ip, "127.") || ip == "::1" {
			continue
		}
		if isLikelyDockerBridgeIP(ip) {
			continue
		}
		if !tsUp && isCGNATTailscaleIP(ip) {
			continue
		}
		return ip
	}

	if dev.QuicHost != "" && dev.QuicHost != "0.0.0.0" && !isLikelyDockerBridgeIP(dev.QuicHost) {
		if !tsUp && isCGNATTailscaleIP(dev.QuicHost) {
			return ""
		}
		return dev.QuicHost
	}
	return ""
}

func lookupTailscaleIP(tsPath string, names ...string) string {
	if strings.TrimSpace(tsPath) == "" {
		return ""
	}
	seen := map[string]struct{}{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := osexec.CommandContext(ctx, tsPath, "ip", name).Output()
		cancel()
		if err != nil {
			continue
		}
		if ip := firstPreferredTailscaleIP(string(out)); ip != "" {
			return ip
		}
	}
	return ""
}

func firstPreferredTailscaleIP(out string) string {
	var fallback string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ip := strings.TrimSpace(line)
		if ip == "" {
			continue
		}
		if strings.Contains(ip, ".") && strings.HasPrefix(ip, "100.") {
			return ip
		}
		if fallback == "" {
			fallback = ip
		}
	}
	return fallback
}

// bareHostNoPort strips a trailing :port (and surrounding IPv6 brackets)
// from an "endpoint" string so it can be handed to ssh as a hostname.
// Public endpoints carry the agent's HTTP API port (`host:18080`), which
// OpenSSH would otherwise treat as part of a literal hostname — there is
// no `host:port` ssh syntax. Bare hosts (no port) pass through unchanged;
// "[::1]:18080" → "::1"; "[::1]" → "::1"; "1.2.3.4" → "1.2.3.4".
func bareHostNoPort(ep string) string {
	ep = strings.TrimSpace(ep)
	if ep == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(ep); err == nil {
		return strings.TrimSpace(host)
	}
	// No port: net.SplitHostPort errors. Still unwrap a bracketed
	// bare IPv6 literal ("[::1]" → "::1").
	ep = strings.TrimPrefix(ep, "[")
	ep = strings.TrimSuffix(ep, "]")
	return strings.TrimSpace(ep)
}

// isYaverHTTPRelayHost reports whether host looks like one of Yaver's
// HTTP relay gateways (<uuid>.yaver.io or <uuid>.dev.yaver.io). Those
// endpoints terminate HTTPS only — SSH never works against them. Used
// by resolveSSHHost to skip them when picking an SSH target.
func isYaverHTTPRelayHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if !strings.HasSuffix(host, ".yaver.io") {
		return false
	}
	label := strings.TrimSuffix(host, ".yaver.io")
	label = strings.TrimSuffix(label, ".dev")
	if len(label) != 36 || strings.Count(label, "-") != 4 {
		return false
	}
	for _, r := range label {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

func isLikelyDockerBridgeIP(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// Common Docker bridge gateways are 172.17.0.1, 172.18.0.1, etc.
	// These are valid inside the container namespace but almost never a
	// useful SSH target from the caller's machine.
	return ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 && ip4[2] == 0 && ip4[3] == 1
}

func inferSSHUser(host string, dev DeviceInfo) string {
	if strings.TrimSpace(host) == "" {
		return ""
	}
	// Source of truth: the remote agent's /info.osUser. Reachable over
	// the same Convex-bearer-token channel ping uses, on every
	// platform. If the agent reports it, trust it — that's the user
	// whose authorized_keys the bootstrap path writes to and whose
	// home dir owns the session. The previous Linux-only gate caused
	// `yaver ssh <macos-box>` to ssh as the LOCAL user (e.g.
	// kivanccakmak) into a remote where that account doesn't exist
	// (e.g. pokayoke), producing "Connection closed by host port 22"
	// well before any auth-bootstrap path could help.
	if u := remoteAgentOSUser(dev.DeviceID, 3*time.Second); u != "" {
		return u
	}
	// Fallback heuristic only for Linux boxes — historical Yaver
	// targets where root-ssh / current-user-ssh probes are useful.
	// For other platforms without /info.osUser we'd rather return ""
	// (ssh uses the local user; the failure diagnostic explains how
	// to override with `ssh <user>@<host>`) than guess wrong.
	if !strings.EqualFold(strings.TrimSpace(dev.Platform), "linux") {
		return ""
	}
	if !strings.Contains(host, ".") {
		return ""
	}
	currentUser := strings.TrimSpace(os.Getenv("USER"))
	if currentUser != "" && probeSSHUser(host, currentUser) {
		return currentUser
	}
	if probeSSHUser(host, "root") {
		return "root"
	}
	return ""
}

// remoteAgentOSUser returns the OS user the remote agent reports it
// is running as, by hitting /info via the existing transport-fallback
// path. Empty string on any error so the caller can fall back to the
// legacy probe-based heuristic without leaking a misleading user.
func remoteAgentOSUser(deviceID string, timeout time.Duration) string {
	if strings.TrimSpace(deviceID) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, deviceID)
	if err != nil || report == nil || report.Info == nil {
		return ""
	}
	if v, ok := report.Info["osUser"].(string); ok {
		v = strings.TrimSpace(v)
		// Reject the numeric uid fallback — a literal "uid:0" is
		// meaningless to ssh and would just stall the dial.
		if v != "" && !strings.HasPrefix(v, "uid:") {
			return v
		}
	}
	return ""
}

func probeSSHUser(host, user string) bool {
	sshPath, err := osexec.LookPath("ssh")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, sshPath,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		user+"@"+host,
		"true",
	)
	return cmd.Run() == nil
}

// SetDeviceAlias calls POST /devices/alias to set or clear the
// per-user alias for one of the caller's Yaver devices. Convex stores
// the alias on the device row so `yaver devices`, `yaver ssh <alias>`,
// web, and mobile all resolve the same short name. Pass alias="" to
// clear it.
func SetDeviceAlias(baseURL, token, deviceId, alias string) error {
	payload, _ := json.Marshal(map[string]string{
		"deviceId": deviceId,
		"alias":    alias,
	})
	req, err := newBearerRequest("POST", baseURL+"/devices/alias", token, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set alias failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func deviceAccessLabel(d DeviceInfo) string {
	if !d.IsGuest {
		return "OWN"
	}
	label := "SHARED"
	if d.HostName != "" {
		label = "SHARED:" + d.HostName
	} else if d.HostEmail != "" {
		label = "SHARED:" + d.HostEmail
	}
	if len(label) > 22 {
		label = label[:21] + "…"
	}
	return label
}

func deviceSessionBindingLabel(d DeviceInfo) string {
	if d.IsGuest {
		return "-"
	}
	if d.SessionBinding != "" {
		return d.SessionBinding
	}
	return "legacy-shared"
}

func deviceAddressLabel(d DeviceInfo) string {
	host := strings.TrimSpace(d.QuicHost)
	if host == "" {
		return "-"
	}
	if d.QuicPort > 0 {
		return fmt.Sprintf("%s:%d", host, d.QuicPort)
	}
	return host
}

func statusDeviceLabel(d DeviceInfo, currentDeviceID string) string {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = strings.TrimSpace(d.DeviceID)
	}
	if name == "" {
		name = "unnamed-device"
	}
	if strings.TrimSpace(d.DeviceID) != "" && strings.TrimSpace(d.DeviceID) == strings.TrimSpace(currentDeviceID) {
		return name + " (this machine)"
	}
	return name
}

func summarizeDeviceRunners(cfg *Config, device DeviceInfo) string {
	deviceID := strings.TrimSpace(device.DeviceID)
	if deviceID == "" {
		return ""
	}

	var (
		rows []runnerAuthStatusRow
		err  error
	)
	if cfg.DeviceID != "" && deviceID == cfg.DeviceID {
		rows, err = collectRunnerAuthStatusRows()
	} else {
		rows, err = fetchRunnerAuthStatusRowsRemote(deviceID)
	}
	if err != nil {
		return "unreachable"
	}
	if len(rows) == 0 {
		return "-"
	}

	var parts []string
	for _, row := range rows {
		if !row.Installed {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", normalizeRunnerID(row.ID), summarizeRunnerAuthState(row)))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func summarizeRunnerAuthState(row runnerAuthStatusRow) string {
	if !row.AuthConfigured {
		return "non-auth"
	}
	source := strings.ToLower(strings.TrimSpace(row.AuthSource))
	switch {
	case strings.Contains(source, "glm") || strings.Contains(source, "zai"):
		return "glm"
	case strings.Contains(source, "anthropic"):
		return "anthropic"
	case strings.Contains(source, "openai"):
		return "openai"
	case strings.Contains(source, "local provider"):
		return "local"
	case strings.Contains(source, "auth.json"),
		strings.Contains(source, "login"),
		strings.Contains(source, "oauth"),
		strings.Contains(source, "keychain"):
		return "auth"
	default:
		return "auth"
	}
}

// ---------------------------------------------------------------------------
// uninstall — remove config, certs, stop agent service
// ---------------------------------------------------------------------------

func runUninstall() {
	yes := false
	target := ""
	for _, a := range os.Args[2:] {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--help", "-h":
			fmt.Println("yaver uninstall [<alias|deviceId>] — entirely remove Yaver.")
			fmt.Println()
			fmt.Println("Without arguments, uninstalls THIS machine. With a target,")
			fmt.Println("triggers /machine/remove on the named remote box and streams")
			fmt.Println("each step (Convex dereg, systemd stop, shell rc + ssh keys")
			fmt.Println("cleanup, ~/.yaver removal) until the remote agent exits.")
			fmt.Println()
			fmt.Println("Removes ~/.yaver (auth token, vault, logs, blobs), the systemd /")
			fmt.Println("launchd unit, the shell-rc PATH block from .bashrc/.zshrc/.profile,")
			fmt.Println("yaver-bootstrap entries from ~/.ssh/authorized_keys, and deletes")
			fmt.Println("the device from the public Yaver Convex backend (cascading to")
			fmt.Println("its sdkTokens, projects, services, and primary-device pointer).")
			fmt.Println()
			fmt.Println("Flags:")
			fmt.Println("  --yes, -y    skip the 'delete my machine' confirmation prompt")
			return
		default:
			if !strings.HasPrefix(a, "-") && target == "" {
				target = a
			}
		}
	}

	if target != "" {
		runRemoteUninstall(target, yes)
		return
	}

	if !yes {
		fmt.Println("This will entirely remove Yaver from this machine and delete this")
		fmt.Println("device's record on the public Yaver backend. Cannot be undone.")
		fmt.Println()
		fmt.Print("Type 'delete my machine' to confirm: ")
		var resp string
		_, _ = fmt.Scanln(&resp)
		if !machineRemovalPhraseValid(resp) {
			fmt.Println("Aborted.")
			os.Exit(1)
		}
	}

	fmt.Println("Uninstalling Yaver...")

	// Single canonical cleanup path — same code that runs when web /
	// mobile trigger remote uninstall via POST /machine/remove. The
	// progress callback prints each step as it happens so the user
	// has the same real-time visibility the streaming endpoint gives
	// to remote callers.
	progress := func(step, status, detail string, err error) {
		switch status {
		case "running":
			fmt.Printf("  [%s] %s\n", step, detail)
		case "ok":
			if detail != "" {
				fmt.Printf("    ✓ %s\n", detail)
			}
		case "skipped":
			fmt.Printf("    — %s (skipped)\n", detail)
		case "error":
			if err != nil {
				fmt.Fprintf(os.Stderr, "    ✗ %s: %v\n", step, err)
			} else {
				fmt.Fprintf(os.Stderr, "    ✗ %s: %s\n", step, detail)
			}
		}
	}
	if err := performPermanentMachineRemoval(progress); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: %v\n", err)
	}

	if removed, note := uninstallPackageWrapper(); note != "" {
		fmt.Println(note)
	} else if removed {
		fmt.Println("  Removed npm-installed yaver command.")
	}

	fmt.Println()
	fmt.Println("Yaver has been uninstalled.")
	fmt.Println()
	fmt.Println("If the binary itself remains (rare — npm uninstall already handles this):")
	fmt.Println("  npm uninstall -g yaver-cli")
	fmt.Printf("  rm %s   # if installed manually\n", os.Args[0])
}

func uninstallPackageWrapper() (bool, string) {
	npm, err := osexec.LookPath("npm")
	if err != nil {
		return false, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := osexec.CommandContext(ctx, npm, "uninstall", "-g", "yaver-cli")
	cmd.Env = augmentEnv(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return false, fmt.Sprintf("  Warning: could not remove npm wrapper automatically: %s", msg)
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// runner resolution — fetch user settings to determine which AI runner to use
// ---------------------------------------------------------------------------

// resolveRunner fetches user settings from the backend and returns the
// appropriate RunnerConfig. Falls back to defaultRunner on any error.
func resolveRunner(convexSiteURL, token, deviceID string) RunnerConfig {
	client := &http.Client{Timeout: 5 * time.Second}

	// Step 1: Fetch user settings
	req, err := newBearerRequest("GET", convexSiteURL+"/settings", token, nil)
	if err != nil {
		log.Printf("Runner: could not build settings request: %v — using default", err)
		return defaultRunner
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Runner: could not fetch settings: %v — using default", err)
		return defaultRunner
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != http.StatusOK {
		log.Printf("Runner: settings endpoint returned %d — using default", resp.StatusCode)
		return defaultRunner
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Runner: could not read settings response: %v — using default", err)
		return defaultRunner
	}

	// Convex returns { settings: { runnerId, primaryRunnerByDevice, ... } }.
	// Older code tried to parse the response as a flat object, so
	// userSettings.runnerId was always ""; every agent fell back to the
	// hard-coded "claude" default even after the user explicitly picked
	// codex through the web UI.
	type primaryRunnerEntry struct {
		DeviceID string `json:"deviceId"`
		RunnerID string `json:"runnerId"`
		Model    string `json:"model,omitempty"`
		Mode     string `json:"mode,omitempty"`
		Provider string `json:"provider,omitempty"`
	}
	var settingsEnv struct {
		Settings struct {
			RunnerID              string               `json:"runnerId"`
			CustomRunnerCommand   string               `json:"customRunnerCommand"`
			PrimaryRunnerByDevice []primaryRunnerEntry `json:"primaryRunnerByDevice"`
		} `json:"settings"`
		// Tolerate future/legacy flat shapes too — populated only if the
		// nested Settings.RunnerID is empty.
		RunnerID              string               `json:"runnerId"`
		CustomRunnerCommand   string               `json:"customRunnerCommand"`
		PrimaryRunnerByDevice []primaryRunnerEntry `json:"primaryRunnerByDevice"`
	}
	if err := json.Unmarshal(body, &settingsEnv); err != nil {
		log.Printf("Runner: could not parse settings: %v — using default", err)
		return defaultRunner
	}
	settings := struct {
		RunnerID              string
		CustomRunnerCommand   string
		PrimaryRunnerByDevice []primaryRunnerEntry
	}{
		RunnerID:              settingsEnv.Settings.RunnerID,
		CustomRunnerCommand:   settingsEnv.Settings.CustomRunnerCommand,
		PrimaryRunnerByDevice: settingsEnv.Settings.PrimaryRunnerByDevice,
	}
	if settings.RunnerID == "" && settingsEnv.RunnerID != "" {
		settings.RunnerID = settingsEnv.RunnerID
		settings.CustomRunnerCommand = settingsEnv.CustomRunnerCommand
	}
	if len(settings.PrimaryRunnerByDevice) == 0 && len(settingsEnv.PrimaryRunnerByDevice) > 0 {
		settings.PrimaryRunnerByDevice = settingsEnv.PrimaryRunnerByDevice
	}

	// Per-device preference wins over the global runnerId. The dashboard
	// writes this when the user picks a runner+model on a specific
	// device's tile (web/components/dashboard/DevicesView.tsx); the agent
	// must honor it so `yaver primary status`, /info, and any /tasks
	// POST without an explicit runner all spawn the runner the user
	// pinned for THIS box.
	deviceID = strings.TrimSpace(deviceID)
	var deviceRunnerID, deviceModel string
	if deviceID != "" {
		for _, e := range settings.PrimaryRunnerByDevice {
			if strings.TrimSpace(e.DeviceID) == deviceID {
				deviceRunnerID = strings.TrimSpace(e.RunnerID)
				deviceModel = strings.TrimSpace(e.Model)
				break
			}
		}
	}

	effectiveRunnerID := deviceRunnerID
	if effectiveRunnerID == "" {
		effectiveRunnerID = settings.RunnerID
	}
	if deviceRunnerID != "" {
		log.Printf("Runner: using per-device pref %q (model=%q) for device %s", deviceRunnerID, deviceModel, deviceID)
	}

	// No runner configured — use default but mark as auto-detected
	if effectiveRunnerID == "" {
		r := defaultRunner
		r.AutoDetected = true
		return r
	}

	// Custom runner: wrap in sh -c with {prompt} placeholder. Custom is
	// only valid as the global setting (it carries the literal command),
	// so we honor CustomRunnerCommand only when the global runnerId is
	// the one we're spawning — never from a per-device pref.
	if effectiveRunnerID == "custom" && deviceRunnerID == "" && settings.CustomRunnerCommand != "" {
		log.Printf("Runner: using custom command: %s", settings.CustomRunnerCommand)
		return RunnerConfig{
			RunnerID:        "custom",
			Name:            "Custom Runner",
			Command:         "sh",
			Args:            []string{"-c", settings.CustomRunnerCommand},
			OutputMode:      "raw",
			ResumeSupported: false,
		}
	}

	// Known runner ID — use builtinRunners (populated from Convex) or default
	if r, ok := builtinRunners[effectiveRunnerID]; ok {
		if deviceModel != "" {
			r.Model = deviceModel
		}
		return r
	}

	runner, err := fetchRunner(client, convexSiteURL, effectiveRunnerID)
	if err != nil {
		log.Printf("Runner: could not fetch runner %q: %v — using default", effectiveRunnerID, err)
		return defaultRunner
	}
	if deviceModel != "" {
		runner.Model = deviceModel
	}
	return runner
}

// backendRunnerFull mirrors the Convex aiRunners table (args/resumeArgs are JSON strings).
type backendRunnerFull struct {
	RunnerID        string `json:"runnerId"`
	Name            string `json:"name"`
	Command         string `json:"command"`
	Args            string `json:"args"` // JSON-encoded []string
	OutputMode      string `json:"outputMode"`
	ResumeSupported bool   `json:"resumeSupported"`
	ResumeArgs      string `json:"resumeArgs,omitempty"` // JSON-encoded []string
	ExitCommand     string `json:"exitCommand,omitempty"`
	Description     string `json:"description"`
}

// fetchRunner fetches the runner list from the backend and finds the one
// matching the given ID.
//
// For shipped runners (claude, codex, opencode) the local builtinRunners
// table is the source of truth for argv. Convex still carries metadata
// rows from older CLI releases — e.g. opencode used to spawn as
// `opencode {prompt}` before sst's CLI rename, and that single-element
// Args is still in the runners row. If we trust that here, startProcess
// later does `args[:2]` on a one-element slice and panics the entire
// HTTP server. Short-circuit to the builtin so version drift can't
// reach the spawn path.
func fetchRunner(client *http.Client, convexSiteURL, runnerID string) (RunnerConfig, error) {
	if normalized := normalizeRunnerID(runnerID); normalized != "" {
		if builtin, ok := builtinRunners[normalized]; ok {
			return builtin, nil
		}
	}
	req, err := http.NewRequest("GET", convexSiteURL+"/runners", nil)
	if err != nil {
		return RunnerConfig{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return RunnerConfig{}, err
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	if resp.StatusCode != http.StatusOK {
		return RunnerConfig{}, fmt.Errorf("runners endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return RunnerConfig{}, err
	}

	// Response is {"runners": [...]}
	var wrapped struct {
		Runners []backendRunnerFull `json:"runners"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return RunnerConfig{}, fmt.Errorf("parse runners: %w", err)
	}

	for _, r := range wrapped.Runners {
		if r.RunnerID == runnerID {
			rc := RunnerConfig{
				RunnerID:        r.RunnerID,
				Name:            r.Name,
				Command:         r.Command,
				OutputMode:      r.OutputMode,
				ResumeSupported: r.ResumeSupported,
				ExitCommand:     r.ExitCommand,
			}
			// Parse JSON-encoded args
			if r.Args != "" {
				_ = json.Unmarshal([]byte(r.Args), &rc.Args)
			}
			if r.ResumeArgs != "" {
				_ = json.Unmarshal([]byte(r.ResumeArgs), &rc.ResumeArgs)
			}
			return rc, nil
		}
	}
	return RunnerConfig{}, fmt.Errorf("runner %q not found", runnerID)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustLoadAuthConfig() *Config {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	if cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	if cfg.ConvexSiteURL == "" {
		fmt.Fprintln(os.Stderr, "No backend configured. Run 'yaver auth' first.")
		os.Exit(1)
	}
	return cfg
}

type DeviceInfo struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	// Alias is the caller's optional short name for this Yaver device,
	// stored in Convex and shared across CLI / web / mobile surfaces.
	Alias                     string   `json:"alias,omitempty"`
	Platform                  string   `json:"platform"`
	QuicHost                  string   `json:"quicHost"`
	LocalIps                  []string `json:"localIps,omitempty"`
	PublicEndpoints           []string `json:"publicEndpoints,omitempty"`
	QuicPort                  int      `json:"quicPort"`
	IsOnline                  bool     `json:"isOnline"`
	IsGuest                   bool     `json:"isGuest,omitempty"`
	HostName                  string   `json:"hostName,omitempty"`
	HostEmail                 string   `json:"hostEmail,omitempty"`
	AccessScope               string   `json:"accessScope,omitempty"`
	PriorityMode              string   `json:"priorityMode,omitempty"`
	UseHostAPIKeys            bool     `json:"useHostApiKeys,omitempty"`
	AllowGuestProvidedAPIKeys bool     `json:"allowGuestProvidedApiKeys,omitempty"`
	SessionBinding            string   `json:"sessionBinding,omitempty"`
}

func listDevices(baseURL, token string) ([]DeviceInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/devices/list", token, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// STREAMING DEBUG

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list devices failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Devices []DeviceInfo `json:"devices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse devices: %w", err)
	}
	return result.Devices, nil
}

// listDevicesEnsuringAuth wraps listDevices with automatic recovery
// when the saved bearer token is no longer accepted. It first tries
// the request as-is. If the call fails AND /auth/validate confirms
// the token is rejected, it attempts a silent refresh against the
// existing token, persisting any rotated token; if that does not
// restore access it falls through to the interactive browser OAuth
// flow (yaver auth) and retries once. cfg is updated in place so
// subsequent calls in the same process see the fresh credentials.
func listDevicesEnsuringAuth(cfg *Config) ([]DeviceInfo, error) {
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err == nil {
		return devices, nil
	}

	// If the token still validates the failure is unrelated to auth —
	// surface the original error so we don't paper over real backend
	// problems with a re-auth flow.
	if vErr := ValidateToken(cfg.ConvexSiteURL, cfg.AuthToken); vErr == nil {
		return nil, err
	}

	fmt.Fprintln(os.Stderr, "Session expired. Refreshing token...")
	refreshed := false
	if newToken, rErr := RefreshToken(cfg.ConvexSiteURL, cfg.AuthToken); rErr == nil {
		if newToken != "" {
			if pErr := persistRotatedAuthToken(cfg, newToken); pErr != nil {
				log.Printf("[auth] (warn) persist rotated token: %v", pErr)
			}
			cfg.AuthToken = newToken
		}
		if vErr := ValidateToken(cfg.ConvexSiteURL, cfg.AuthToken); vErr == nil {
			refreshed = true
		}
	}

	if !refreshed {
		fmt.Fprintln(os.Stderr, "Refresh did not restore access — opening browser to re-authenticate...")
		runAuth(nil)
		fresh, lErr := LoadConfig()
		if lErr != nil || fresh == nil || strings.TrimSpace(fresh.AuthToken) == "" {
			return nil, fmt.Errorf("re-authentication did not complete")
		}
		*cfg = *fresh
	}

	return listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
}

func summarizePeerStates(peers []*PeerState) (online, stale, offline int) {
	for _, peer := range peers {
		switch peer.State {
		case "online":
			online++
		case "stale":
			stale++
		case "offline":
			offline++
		}
	}
	return
}

func sortedPeerSnapshot() []*PeerState {
	peers := globalPeerWatcher().Snapshot()
	sort.Slice(peers, func(i, j int) bool {
		rank := func(state string) int {
			switch state {
			case "online":
				return 0
			case "stale":
				return 1
			case "offline":
				return 2
			default:
				return 3
			}
		}
		if rank(peers[i].State) != rank(peers[j].State) {
			return rank(peers[i].State) < rank(peers[j].State)
		}
		left := peers[i].Name
		if left == "" {
			left = peers[i].DeviceID
		}
		right := peers[j].Name
		if right == "" {
			right = peers[j].DeviceID
		}
		return strings.ToLower(left) < strings.ToLower(right)
	})
	return peers
}

func printStatusMesh() {
	fmt.Println()
	fmt.Println("Mesh:")
	ts := DetectTailscale()
	switch {
	case ts == nil:
		fmt.Println("  Tailscale: unavailable")
	case ts.Running && ts.Self != nil:
		addr := ts.Self.TailAddr
		if addr == "" && len(ts.Self.Addrs) > 0 {
			addr = ts.Self.Addrs[0]
		}
		fmt.Printf("  Tailscale: up (%s", ts.BackendState)
		if addr != "" {
			fmt.Printf(", %s", addr)
		}
		fmt.Println(")")
	case ts.BackendState != "":
		fmt.Printf("  Tailscale: %s\n", strings.ToLower(ts.BackendState))
	default:
		fmt.Println("  Tailscale: not installed or not running")
	}

	peers := sortedPeerSnapshot()
	online, stale, offline := summarizePeerStates(peers)
	if len(peers) == 0 {
		fmt.Println("  Peers:     none observed yet")
		return
	}
	fmt.Printf("  Peers:     %d online, %d stale, %d offline\n", online, stale, offline)
	limit := len(peers)
	if limit > 5 {
		limit = 5
	}
	for _, peer := range peers[:limit] {
		name := peer.Name
		if name == "" {
			name = peer.DeviceID
		}
		lastSeen := peer.LastSeen
		if lastSeen == "" {
			lastSeen = "never"
		}
		fmt.Printf("    %-20s %-7s %s\n", name, peer.State, lastSeen)
	}
	if len(peers) > limit {
		fmt.Printf("    ... and %d more\n", len(peers)-limit)
	}
}

func runDoctorMesh(check func(string), pass func(string), warning func(string), failed func(string)) {
	fmt.Println("\n── Mesh ──")
	check("Tailscale")
	ts := DetectTailscale()
	switch {
	case ts == nil:
		warning("Unavailable")
	case ts.Running && ts.Self != nil:
		addr := ts.Self.TailAddr
		if addr == "" && len(ts.Self.Addrs) > 0 {
			addr = ts.Self.Addrs[0]
		}
		if addr != "" {
			pass(fmt.Sprintf("Running (%s, %s)", ts.BackendState, addr))
		} else {
			pass(fmt.Sprintf("Running (%s)", ts.BackendState))
		}
	case ts.BackendState != "":
		warning(fmt.Sprintf("Installed but %s", strings.ToLower(ts.BackendState)))
	default:
		warning("Not installed or not running")
	}

	peers := sortedPeerSnapshot()
	check("Peer heartbeats")
	if len(peers) == 0 {
		warning("No peers observed yet")
		return
	}
	online, stale, offline := summarizePeerStates(peers)
	if offline > 0 {
		failed(fmt.Sprintf("%d online, %d stale, %d offline", online, stale, offline))
	} else if stale > 0 {
		warning(fmt.Sprintf("%d online, %d stale", online, stale))
	} else {
		pass(fmt.Sprintf("%d online", online))
	}
	for _, peer := range peers {
		check("  " + nonEmpty(peer.Name, peer.DeviceID))
		lastSeen := peer.LastSeen
		if lastSeen == "" {
			lastSeen = "never"
		}
		switch peer.State {
		case "online":
			pass("online, last seen " + lastSeen)
		case "stale":
			warning("stale, last seen " + lastSeen)
		default:
			failed("offline, last seen " + lastSeen)
		}
	}
}

// getLocalIP returns the preferred outbound LOCAL (RFC1918 / private)
// IPv4 address. Returns "" if the host has no private interface — e.g.
// a cloud VM where the only outbound route is via a public IP.
//
// We deliberately do NOT publish public IPs as `localIP` to Convex.
// Reason: iOS' App Transport Security blocks plain-HTTP requests to
// non-local addresses, so when the mobile app reads localIP from the
// device registry and tries `http://<public-ip>:18080`, ATS rejects
// the connection with NSURLError -1022. The mobile then bounces
// between failed direct attempts and the relay path, which looks like
// a network outage to the user. The relay is the intended path for
// off-LAN connections; the direct path is for same-Wi-Fi only.
func getLocalIP() string {
	// Probe the default outbound route. On a normal LAN host this
	// returns the host's RFC1918 address; on a cloud VM with only a
	// public NIC it returns the public address.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	ip := localAddr.IP
	if ip == nil {
		return ""
	}
	// Only advertise IPs the mobile can talk to over plain HTTP.
	// IsPrivate covers RFC1918 (10/8, 172.16/12, 192.168/16) plus
	// RFC4193 (IPv6 ULA). Loopback and link-local are also excluded.
	if !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
		// Public IP — fall through to scanning interfaces in case
		// there's a private NIC alongside (e.g. tailscale interface
		// that wasn't picked as the default route).
		if alt := firstPrivateIPv4(); alt != "" {
			return alt
		}
		return ""
	}
	return ip.String()
}

// firstPrivateIPv4 enumerates every UP non-loopback interface and
// returns the first RFC1918 IPv4 address it finds. Used as a fallback
// when getLocalIP's outbound-route probe lands on a public IP (cloud
// VMs, Hetzner, public-IP boxes, …) so the registered host is at
// least private and reachable from a teammate on the same LAN.
//
// Skips virtual / container bridge interfaces (docker0, br-*, virbr*,
// podman*, cni*, kube-bridge, weave, flannel, …). On a Hetzner cloud
// VM with Docker installed, these are the FIRST private interface
// `net.Interfaces()` returns — without filtering, the heartbeat
// reports `host=172.17.0.1` which:
//
//   - Is unreachable from any browser / mobile / SSH client.
//   - Trips the web's "172.16-31.x.y → likely WSL" heuristic (now
//     replaced by the hardwareProfile.isWsl bit, but legacy clients
//     still see the wrong label).
//   - Wedges the web shell modal at "connection error" because
//     terminalWsUrl falls through to the direct ws://172.17.0.1 path.
//
// Skipping container bridges does NOT skip Tailscale (100.x CGNAT) —
// that interface is intentionally reachable on the tailnet and the
// mobile client races it during direct-connect.
func firstPrivateIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isContainerBridgeInterfaceName(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil {
				continue
			}
			if !ip.IsPrivate() {
				continue
			}
			// Defense-in-depth: even an interface whose name slipped
			// past the prefix filter (custom Docker networks named
			// without the `br-` prefix, exotic CNI implementations)
			// still gets caught if its IP is on a known docker
			// bridge gateway (172.x.0.1).
			if isLikelyDockerBridgeIP(ip.String()) {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

// isContainerBridgeInterfaceName matches every common virtual /
// container-network interface so the LAN-IP picker skips them. Linux
// distros vary, so we go on prefixes (case-insensitive) rather than
// exact names. A real Wi-Fi / Ethernet / Tailscale / VPN interface
// never matches this.
func isContainerBridgeInterfaceName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	prefixes := []string{
		"docker",  // docker0, dockerN
		"br-",     // user-defined Docker networks
		"virbr",   // libvirt
		"podman",  // podmanN
		"cni",     // CNI plugins (Kubernetes, OpenShift, …)
		"flannel", // Flannel CNI
		"weave",   // Weave Net
		"calico",  // Calico CNI
		"cali",    // Calico interfaces (cali123abc)
		"vxlan",   // overlay networks
		"kube-",   // kube-bridge etc.
		"veth",    // virtual ethernet pair (container side)
	}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// getLocalIPs enumerates every reachable IPv4 address this host has —
// Wi-Fi LAN (192.168.x / 10.x / 172.16-31.x), Tailscale (100.x in CGNAT
// range), Ethernet, anything else on an UP, non-loopback interface.
// Mobile clients race all of these in parallel during connect so the
// session attaches via whichever path actually has a route from the
// phone (e.g. Tailscale when on cellular, plain Wi-Fi when same LAN).
// The preferred outbound IP is returned first; remaining unique
// addresses follow in interface-enumeration order. Loopback, link-local
// (169.254.x), and IPv6 are excluded — they are never useful remotely.
func getLocalIPs() []string {
	preferred := getLocalIP()
	seen := make(map[string]struct{})
	var ips []string
	if preferred != "" && preferred != "0.0.0.0" {
		seen[preferred] = struct{}{}
		ips = append(ips, preferred)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			// Skip public IPs. Mobile reads localIps[] and tries each
			// over plain HTTP for direct-LAN connect; iOS App Transport
			// Security blocks HTTP to public addresses with -1022, and
			// even if it didn't, no mobile on the user's home Wi-Fi can
			// reach the cloud VM's public IP via direct route. The
			// relay path (https://public.yaver.io/d/<id>) is the only
			// off-LAN path that works. Keep RFC1918 (and other private
			// ranges) — those are real LAN addresses for home agents.
			if !ip4.IsPrivate() {
				continue
			}
			s := ip4.String()
			if _, dup := seen[s]; dup {
				continue
			}
			// Skip Docker bridge gateway IPs (172.17.0.1, 172.18.0.1, ...).
			// Inside a Linux box with Docker, these are valid local
			// interfaces but mobile can't reach them — and worse, the
			// mobile UI heuristic flags 172.16-31.x.y as "WSL-like" and
			// shows the box as PUBLIC IP / direct-reachable. The mobile
			// then gets stuck CONNECTING because it tries that IP first
			// instead of falling through to the relay. Filtering at the
			// emit side keeps Convex's localIps record clean.
			if isLikelyDockerBridgeIP(s) {
				continue
			}
			seen[s] = struct{}{}
			ips = append(ips, s)
		}
	}
	return ips
}

// sameStringSet returns true when both slices contain the same elements
// regardless of order. Used to suppress noisy "LAN set changed" log lines
// when the heartbeat just re-enumerates the same interfaces.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]struct{}, len(a))
	for _, s := range a {
		m[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := m[s]; !ok {
			return false
		}
	}
	return true
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		execOpen("open", url)
	case "linux":
		if isWSL() {
			if _, err := osexec.LookPath("wslview"); err == nil {
				execOpen("wslview", url)
				return
			}
			if _, err := osexec.LookPath("explorer.exe"); err == nil {
				execOpen("explorer.exe", url)
				return
			}
			if _, err := osexec.LookPath("cmd.exe"); err == nil {
				execOpen("cmd.exe", "/c", "start", url)
				return
			}
		}
		execOpen("xdg-open", url)
	case "windows":
		execOpen("cmd", "/c", "start", url)
	}
}

func execOpen(name string, args ...string) {
	cmd := osexec.Command(name, args...)
	cmd.Start()
}

func heartbeatLoop(ctx context.Context, baseURL, token, deviceID string, taskMgr *TaskManager, httpServer *HTTPServer) {
	// 5 min instead of 30 s: the P2P bus (see bus.go) now carries
	// live peer presence between devices for free. Convex only
	// needs to know "does this device exist" for the dashboard's
	// cold-boot rendering — a 5-min cadence keeps lastHeartbeat
	// fresh enough for "is this machine online within the last
	// hour" style queries without burning 120 calls/hour/user.
	//
	// A SIGKILL'd agent now shows "possibly offline" to bus peers
	// within ~1 min (their keepalive timeout) and to the web
	// dashboard within ~5 min (next Convex heartbeat cutoff). That
	// ~5-min staleness is the conscious trade for ~90% lower
	// Convex call volume — the web dashboard can regain sub-minute
	// responsiveness by subscribing to /bus/events on the agent
	// directly (follow-up PR).
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	currentToken := func() string {
		if cfg, err := LoadConfig(); err == nil && cfg != nil && strings.TrimSpace(cfg.AuthToken) != "" {
			return cfg.AuthToken
		}
		return token
	}

	// Refresh token on startup — extends the expiry by 1 year and,
	// if the backend supports rotation, swaps the bearer token so a
	// leak only lives until the next daily refresh.
	refreshAndPersist := func(label string) bool {
		newToken, err := RefreshToken(baseURL, currentToken())
		if err != nil {
			log.Printf("[auth] %s token refresh failed: %v", label, err)
			return false
		}
		if newToken != "" {
			if cfg, cerr := LoadConfig(); cerr == nil && cfg != nil {
				if perr := persistRotatedAuthToken(cfg, newToken); perr != nil {
					log.Printf("[auth] (warn) %s — rotated but could not persist: %v", label, perr)
				} else {
					if taskMgr != nil {
						taskMgr.AuthToken = newToken
					}
					if httpServer != nil {
						httpServer.token = newToken
						// Flush token→user cache — old bearer entries
						// reference a revoked token and will make
						// auth() hand out the wrong routing decision
						// (owner vs guest vs host-share) until the
						// old entries age out.
						httpServer.tokenCache.Range(func(k, _ interface{}) bool {
							httpServer.tokenCache.Delete(k)
							return true
						})
					}
					log.Printf("[auth] %s refreshed + rotated (extended 1 year).", label)
				}
			}
		} else {
			log.Printf("[auth] %s refreshed (extended 1 year).", label)
		}
		return true
	}
	refreshAndPersist("startup")

	// Refresh token daily (extends to 1 year each time — prevents expiry even on long-running agents)
	refreshTicker := time.NewTicker(24 * time.Hour)
	defer refreshTicker.Stop()

	lastIP := getLocalIP()
	lastIPs := getLocalIPs()
	cfgAtStart, _ := LoadConfig()
	heartbeatPort := 0
	if httpServer != nil {
		heartbeatPort = httpServer.port
	}
	lastPublicEndpoints := publicEndpointsWithAutoIP(cfgAtStart, heartbeatPort)
	authExpiredLogged := false

	// Notify Convex that this device's auth has gone bad mid-session, so
	// web/mobile clients see needsAuth=true on the device row and can
	// surface a re-auth UI without waiting for a reboot's bootstrap dance.
	// Throttled to one notify per 5 minutes — we don't need to spam it.
	var lastAuthExpiredNotify time.Time
	notifyAuthExpiredOnce := func() {
		if time.Since(lastAuthExpiredNotify) < 5*time.Minute {
			return
		}
		lastAuthExpiredNotify = time.Now()
		port := 0
		if httpServer != nil {
			port = httpServer.port
		}
		cfgNow, _ := LoadConfig()
		if cfgNow != nil {
			go notifyConvexAuthExpired(cfgNow, port)
		}
	}
	clearAuthExpiredNotify := func() {
		lastAuthExpiredNotify = time.Time{}
	}

	// Send first heartbeat immediately (don't wait 2 min for ticker)
	runners := taskMgr.GetRunnerInfos()
	installedRunnerIDs := collectInstalledRunnerIDs()
	initialRecoveryPosture := computeRecoveryTransportPosture(cfgAtStart)
	if err := SendHeartbeat(baseURL, currentToken(), deviceID, runners, installedRunnerIDs, lastIP, lastIPs, lastPublicEndpoints, &initialRecoveryPosture); err != nil {
		if errors.Is(err, ErrAuthExpired) {
			log.Println("[auth] WARNING: Auth token expired! Run 'yaver auth' to re-authenticate.")
			authExpiredLogged = true
			if httpServer != nil {
				httpServer.authExpired.Store(true)
			}
			notifyAuthExpiredOnce()
		} else {
			log.Printf("initial heartbeat failed: %v", err)
		}
	} else {
		log.Println("Initial heartbeat sent.")
	}

	// Shared body for both the 30 s ticker and out-of-band kicks from
	// handlers that just changed reportable state (runner auth completing,
	// etc.). Kept as a closure so a kick and a tick are literally the same
	// code path — no risk of them diverging over time.
	sendOne := func() {
		currentIP := getLocalIP()
		currentIPs := getLocalIPs()
		cfgNow, _ := LoadConfig()
		currentPublicEndpoints := publicEndpointsWithAutoIP(cfgNow, heartbeatPort)
		runners := taskMgr.GetRunnerInfos()
		installedRunnerIDs := collectInstalledRunnerIDs()

		if currentIP != lastIP {
			log.Printf("[heartbeat] Local IP changed: %s → %s", lastIP, currentIP)
			lastIP = currentIP
		}
		if !sameStringSet(currentIPs, lastIPs) {
			log.Printf("[heartbeat] LAN/Tailscale set changed: %v → %v", lastIPs, currentIPs)
			lastIPs = currentIPs
		}
		if !sameStringSet(currentPublicEndpoints, lastPublicEndpoints) {
			log.Printf("[heartbeat] Public endpoints changed: %v → %v", lastPublicEndpoints, currentPublicEndpoints)
			lastPublicEndpoints = currentPublicEndpoints
		}

		currentRecoveryPosture := computeRecoveryTransportPosture(cfgNow)
		if err := SendHeartbeat(baseURL, currentToken(), deviceID, runners, installedRunnerIDs, currentIP, currentIPs, currentPublicEndpoints, &currentRecoveryPosture); err != nil {
			if errors.Is(err, ErrAuthExpired) {
				// Try to refresh token first — backend may rotate.
				if !refreshAndPersist("on-401") {
					if !authExpiredLogged {
						log.Println("[auth] WARNING: Auth token expired or revoked.")
						log.Println("[auth] This can happen if you signed out from all devices or your session expired.")
						log.Println("[auth] Run 'yaver auth' to re-authenticate. The agent will continue running but the device will appear offline.")
						authExpiredLogged = true
						if httpServer != nil {
							httpServer.authExpired.Store(true)
						}
					}
					notifyAuthExpiredOnce()
				} else {
					log.Println("[auth] Token refreshed after 401 — retrying heartbeat...")
					authExpiredLogged = false
					if httpServer != nil {
						httpServer.authExpired.Store(false)
					}
					clearAuthExpiredNotify()
					// Retry heartbeat
					retryRecoveryPosture := computeRecoveryTransportPosture(cfgNow)
					if retryErr := SendHeartbeat(baseURL, currentToken(), deviceID, runners, installedRunnerIDs, currentIP, currentIPs, currentPublicEndpoints, &retryRecoveryPosture); retryErr != nil {
						log.Printf("heartbeat retry failed: %v", retryErr)
					}
				}
			} else {
				log.Printf("heartbeat failed: %v", err)
			}
		} else {
			if authExpiredLogged {
				log.Println("[auth] Heartbeat succeeded — auth is working again.")
				authExpiredLogged = false
				if httpServer != nil {
					httpServer.authExpired.Store(false)
				}
				clearAuthExpiredNotify()
			}
		}
		// Claim + execute any rescue command queued in Convex. Always
		// runs after a successful heartbeat (which proves Convex is
		// reachable and our token is valid). Best-effort: errors are
		// logged but never break the heartbeat loop.
		go claimAndExecuteRescueCommandSingleFlight(baseURL, currentToken(), deviceID)
		// Same gate: a successful heartbeat proves Convex + token are
		// good, so this is also when we pull any queued publish job
		// for this farm node. Best-effort, single-flight per tick.
		go claimAndExecutePublishJobSingleFlight(baseURL, currentToken(), deviceID)
	}

	var kickChan <-chan struct{}
	if httpServer != nil {
		kickChan = httpServer.heartbeatKick
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-refreshTicker.C:
			if refreshAndPersist("daily") {
				authExpiredLogged = false
				if httpServer != nil {
					httpServer.authExpired.Store(false)
				}
				clearAuthExpiredNotify()
			}
		case <-ticker.C:
			sendOne()
		case <-kickChan:
			// Eager beat triggered by a handler that changed reportable
			// state (e.g. remote codex/claude sign-in just finished). The
			// 30 s ticker is left running — we don't try to reset it, the
			// next tick is still a useful freshness signal.
			sendOne()
		}
	}
}

// metricsLoop collects CPU/RAM every 60s and reports to Convex.
func metricsLoop(ctx context.Context, baseURL, token, deviceID string) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	currentToken := func() string {
		if cfg, err := LoadConfig(); err == nil && cfg != nil && strings.TrimSpace(cfg.AuthToken) != "" {
			return cfg.AuthToken
		}
		return token
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cpuPct, cpuErr := getCPUPercent()
			if cpuErr != nil {
				log.Printf("[metrics] CPU error: %v", cpuErr)
				cpuPct = 0
			}

			memUsed, memErr := getMemoryUsedMB()
			if memErr != nil {
				log.Printf("[metrics] Memory used error: %v", memErr)
				memUsed = 0
			}

			memTotal, totalErr := getSystemMemoryMB()
			if totalErr != nil {
				log.Printf("[metrics] Memory total error: %v", totalErr)
				memTotal = 0
			}

			log.Printf("[metrics] CPU=%.1f%% RAM=%dMB/%dMB", cpuPct, memUsed, memTotal)

			if err := ReportMetrics(baseURL, currentToken(), deviceID, cpuPct, float64(memUsed), float64(memTotal)); err != nil {
				log.Printf("[metrics] Report failed: %v", err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Relay tunnel — connects agent to public relay server for P2P connectivity
// ---------------------------------------------------------------------------

// relayRegisterMsg is sent by the agent on the first QUIC stream.
type relayRegisterMsg struct {
	Type     string `json:"type"`
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
	Password string `json:"password,omitempty"`
}

type relayRegisterResp struct {
	Type    string `json:"type"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	// AssignedURL is the relay-auto-provisioned <deviceId>.<expose-
	// domain> URL (e.g. https://abc1234.dev.yaver.io). Populated by
	// relays running v0.1.11+. We mirror it into config + publish it
	// as publicUrl on the next heartbeat so the dashboard can probe
	// the device on a clean HTTPS-direct origin instead of the
	// noisy /d/<id>/ path that triggers mixed-content blocks.
	AssignedURL string `json:"assignedUrl,omitempty"`
}

type relayTunnelRequest struct {
	ID         string            `json:"id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
	TargetPort int               `json:"targetPort,omitempty"`
}

type relayTunnelResponse struct {
	ID         string            `json:"id"`
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

// RelayHealthStatus holds the latest health check result for a relay server.
type RelayHealthStatus struct {
	URL         string    `json:"url"`
	OK          bool      `json:"ok"`
	LatencyMs   int64     `json:"latencyMs"`
	Tunnels     int       `json:"tunnels"`
	Version     string    `json:"version"`
	LastChecked time.Time `json:"lastChecked"`
	Error       string    `json:"error,omitempty"`
}

// relayHealthFile returns the path to the relay health cache file.
func relayHealthFile() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "relay-health.json")
}

// relayManager manages relay tunnel goroutines and hot-reloads config changes.
type relayManager struct {
	parentCtx         context.Context
	deviceID          string
	authToken         string
	agentAddr         string
	globalPassword    string
	convexSiteURL     string
	activeTunnels     map[string]context.CancelFunc // keyed by QuicAddr
	healthStatus      map[string]*RelayHealthStatus // keyed by httpUrl
	lastSettingsRelay string                        // last relayUrl from user settings (for change detection)
	relayExposeMgr    *RelayExposeManager
}

func newRelayManager(ctx context.Context, deviceID, authToken, agentAddr, globalPassword, convexSiteURL string) *relayManager {
	return &relayManager{
		parentCtx:      ctx,
		deviceID:       deviceID,
		authToken:      authToken,
		agentAddr:      agentAddr,
		globalPassword: globalPassword,
		convexSiteURL:  convexSiteURL,
		activeTunnels:  make(map[string]context.CancelFunc),
		healthStatus:   make(map[string]*RelayHealthStatus),
		relayExposeMgr: NewRelayExposeManager(),
	}
}

// applyRelayServers starts new tunnels and stops removed ones.
func (rm *relayManager) applyRelayServers(servers []RelayServerInfo, passwords map[string]string) {
	// Build desired set
	desired := make(map[string]string) // QuicAddr -> password
	for _, rs := range servers {
		pw := passwords[rs.QuicAddr]
		if pw == "" {
			pw = rm.globalPassword
		}
		desired[rs.QuicAddr] = pw
	}

	// Stop tunnels that are no longer in config
	for addr, cancelFn := range rm.activeTunnels {
		if _, ok := desired[addr]; !ok {
			log.Printf("[RELAY] Stopping tunnel to %s (removed from config)", addr)
			cancelFn()
			delete(rm.activeTunnels, addr)
		}
	}

	// Start new tunnels
	for addr, pw := range desired {
		if _, ok := rm.activeTunnels[addr]; ok {
			continue // already running
		}
		tunnelCtx, tunnelCancel := context.WithCancel(rm.parentCtx)
		rm.activeTunnels[addr] = tunnelCancel
		log.Printf("[RELAY] Starting tunnel to %s...", addr)
		go runRelayTunnel(tunnelCtx, addr, rm.agentAddr, rm.deviceID, rm.authToken, pw, rm.relayExposeMgr)
	}
}

// reloadNow triggers an immediate config reload (called on SIGHUP).
func (rm *relayManager) reloadNow() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("[RELAY] Config reload failed: %v", err)
		return
	}
	relayCfg := cfg.RelayServers
	if len(relayCfg) == 0 {
		relayCfg = cfg.CachedRelayServers
	}
	servers, passwords := relayInfosFromConfig(relayCfg)
	if cfg.RelayPassword != "" {
		rm.globalPassword = cfg.RelayPassword
	} else if cfg.CachedRelayPassword != "" {
		rm.globalPassword = cfg.CachedRelayPassword
	}
	rm.applyRelayServers(servers, passwords)
	log.Printf("[RELAY] Config reloaded: %d relay server(s)", len(servers))
}

// watchConfig polls config.json and Convex user settings every 30s for relay server changes.
func (rm *relayManager) watchConfig(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check config.json first (highest priority)
			cfg, err := LoadConfig()
			if err == nil && len(cfg.RelayServers) > 0 {
				var servers []RelayServerInfo
				passwords := make(map[string]string)
				for _, rs := range cfg.RelayServers {
					servers = append(servers, RelayServerInfo{
						ID:       rs.ID,
						QuicAddr: rs.QuicAddr,
						HttpURL:  rs.HttpURL,
						Region:   rs.Region,
						Priority: rs.Priority,
					})
					if rs.Password != "" {
						passwords[rs.QuicAddr] = rs.Password
					}
				}
				if cfg.RelayPassword != "" {
					rm.globalPassword = cfg.RelayPassword
				}
				rm.applyRelayServers(servers, passwords)
				continue
			}

			// No local config — check Convex user settings for relay changes
			if rm.convexSiteURL == "" {
				continue
			}
			settings, err := FetchUserSettings(rm.convexSiteURL, rm.authToken)
			if err != nil {
				continue
			}
			if settings.RelayUrl == "" {
				continue
			}
			// Only re-apply if the relay URL changed
			if settings.RelayUrl == rm.lastSettingsRelay {
				continue
			}
			rm.lastSettingsRelay = settings.RelayUrl
			log.Printf("[RELAY] User settings relay changed: %s", settings.RelayUrl)
			servers := []RelayServerInfo{{
				ID:       "user-settings",
				HttpURL:  settings.RelayUrl,
				Region:   "user",
				Priority: 1,
			}}
			passwords := make(map[string]string)
			if settings.RelayPassword != "" {
				rm.globalPassword = settings.RelayPassword
			}
			rm.applyRelayServers(servers, passwords)
		}
	}
}

// healthCheckLoop periodically pings each relay server's /health endpoint
// and persists the results to ~/.yaver/relay-health.json for `yaver status`.
func (rm *relayManager) healthCheckLoop(ctx context.Context) {
	client := &http.Client{Timeout: 10 * time.Second}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Run immediately on startup
	rm.checkRelayHealth(client)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.checkRelayHealth(client)
		}
	}
}

func (rm *relayManager) checkRelayHealth(client *http.Client) {
	cfg, err := LoadConfig()
	if err != nil || len(cfg.RelayServers) == 0 {
		return
	}

	for _, rs := range cfg.RelayServers {
		status := &RelayHealthStatus{
			URL:         rs.HttpURL,
			LastChecked: time.Now(),
		}

		start := time.Now()
		resp, err := client.Get(rs.HttpURL + "/health")
		status.LatencyMs = time.Since(start).Milliseconds()

		if err != nil {
			status.OK = false
			status.Error = err.Error()
		} else {
			defer resp.Body.Close()

			// STREAMING DEBUG
			if resp.StatusCode == 200 {
				status.OK = true
				var body struct {
					OK      bool   `json:"ok"`
					Tunnels int    `json:"tunnels"`
					Version string `json:"version"`
				}
				if json.NewDecoder(resp.Body).Decode(&body) == nil {
					status.Tunnels = body.Tunnels
					status.Version = body.Version
				}
			} else {
				status.OK = false
				status.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
		}

		rm.healthStatus[rs.HttpURL] = status
	}

	// Persist to file for `yaver status`
	rm.saveHealth()
}

func (rm *relayManager) saveHealth() {
	path := relayHealthFile()
	if path == "" {
		return
	}
	statuses := make([]RelayHealthStatus, 0, len(rm.healthStatus))
	for _, s := range rm.healthStatus {
		statuses = append(statuses, *s)
	}
	data, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0600)
}

// loadRelayHealth reads the cached relay health from disk (for `yaver status`).
func loadRelayHealth() []RelayHealthStatus {
	path := relayHealthFile()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var statuses []RelayHealthStatus
	if json.Unmarshal(data, &statuses) != nil {
		return nil
	}
	return statuses
}

// runRelayTunnel connects to ONE specific relay and reconnects to
// THAT relay with per-relay exponential backoff. The relayManager
// spawns one of these per configured relay so a dead relay hammers
// itself in isolation while the others keep serving.
//
// Failover semantics: if this relay is dead for > 60 s, we jitter the
// retry up to a minute so N agents on the same dead relay don't
// thundering-herd it when it comes back. Healthy reconnects (short
// blips < 30 s total) retry aggressively from 1 s so the user sees
// fast recovery on normal network flaps.
func runRelayTunnel(ctx context.Context, relayAddr, agentAddr, deviceID, token, password string, exposeMgr *RelayExposeManager) {
	backoff := time.Second
	unhealthySince := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[RELAY] Connecting to relay %s...", relayAddr)
		startedAt := time.Now()
		err := relayConnectAndServe(ctx, relayAddr, agentAddr, deviceID, token, password, exposeMgr)
		if err != nil {
			log.Printf("[RELAY %s] Connection lost after %s: %v", relayAddr, time.Since(startedAt).Round(time.Second), err)
			// One-shot recovery: if the rejection looks like a stale
			// relay password (Convex rotated the per-user password
			// server-side, our cached one is dead), refetch the
			// fresh password from /settings and retry immediately.
			// Without this, the agent loops forever on a stale
			// password until someone runs `yaver repair-relay`. See
			// the dynamic-IP / NAT audit — this is the "Convex
			// rotates password" case.
			if looksLikeStaleRelayPassword(err) {
				if fresh := refreshRelayPasswordFromConvex(ctx); fresh != "" && fresh != password {
					log.Printf("[RELAY %s] Refetched fresh relay password from Convex /settings; retrying", relayAddr)
					password = fresh
					backoff = time.Second // reset; we have new creds
					continue
				}
			}
		}

		if ctx.Err() != nil {
			return
		}

		// If we held the connection for ≥ 30 s, treat the flap as
		// healthy-transient: reset backoff + unhealthySince. Short
		// flaps aren't an outage.
		if time.Since(startedAt) >= 30*time.Second {
			backoff = time.Second
			unhealthySince = time.Time{}
		} else if unhealthySince.IsZero() {
			unhealthySince = time.Now()
		}

		// Past 60 s of continuous failure → add jitter so multiple
		// agents coming back don't hit the same dead relay in
		// lockstep. Up to ±50% of the backoff window.
		wait := backoff
		if !unhealthySince.IsZero() && time.Since(unhealthySince) > 60*time.Second {
			jitter := time.Duration(randInt63n(int64(backoff))) - backoff/2
			wait = backoff + jitter
			if wait < time.Second {
				wait = time.Second
			}
		}

		log.Printf("[RELAY %s] Reconnecting in %s...", relayAddr, wait.Round(100*time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
	}
}

// randInt63n is a test-stubbable wrapper around rand.Int63n. In normal
// operation the stock math/rand is fine — this isn't a security hot
// path, it's just a retry-jitter spreader.
var randInt63n = func(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return mathRand.Int63n(n)
}

// looksLikeStaleRelayPassword inspects a relay-connect error and
// decides whether re-fetching the per-user password from Convex
// /settings is worth a try. The relay returns errors like:
//
//	"registration rejected: invalid relay password"
//	"registration rejected: invalid password"
//
// We MUST be conservative — a false positive here would burn through
// reconnect budget. So we require the literal "password" + "invalid"
// or "rejected".
func looksLikeStaleRelayPassword(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "password") {
		return false
	}
	return strings.Contains(msg, "invalid") || strings.Contains(msg, "rejected") || strings.Contains(msg, "denied")
}

// refreshRelayPasswordFromConvex GETs /settings on the user's
// Convex deployment with the current auth token and returns the
// fresh per-user relay password (or "" if Convex is unreachable
// or the user doesn't have one). Used as a one-shot recovery in
// the relay-tunnel reconnect loop when the cached password
// suddenly stops working — most often because Convex rotated it
// server-side.
func refreshRelayPasswordFromConvex(ctx context.Context) string {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.ConvexSiteURL == "" || cfg.AuthToken == "" {
		return ""
	}
	settings, err := FetchUserSettings(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil || settings == nil {
		log.Printf("[RELAY] refresh-password: Convex /settings: %v", err)
		return ""
	}
	pw := strings.TrimSpace(settings.RelayPassword)
	if pw == "" {
		return ""
	}
	// Also persist into config.json so a restart picks up the
	// fresh password without another Convex round-trip.
	cfg.RelayPassword = pw
	if saveErr := SaveConfig(cfg); saveErr != nil {
		log.Printf("[RELAY] refresh-password: SaveConfig: %v", saveErr)
	}
	return pw
}

func relayConnectAndServe(ctx context.Context, relayAddr, agentAddr, deviceID, token, password string, exposeMgr *RelayExposeManager) error {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"yaver-relay"},
	}

	conn, err := quic.DialAddr(ctx, relayAddr, tlsCfg, &quic.Config{
		MaxIdleTimeout:  120 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	defer conn.CloseWithError(0, "shutdown")

	// Register
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open registration stream: %w", err)
	}

	regMsg := relayRegisterMsg{Type: "register", DeviceID: deviceID, Token: token, Password: password}
	data, _ := json.Marshal(regMsg)
	stream.Write(data)
	stream.Close()

	respData, err := io.ReadAll(io.LimitReader(stream, 1<<16))
	if err != nil {
		return fmt.Errorf("read registration response: %w", err)
	}

	var regResp relayRegisterResp
	if err := json.Unmarshal(respData, &regResp); err != nil {
		return fmt.Errorf("parse registration response: %w", err)
	}
	if !regResp.OK {
		return fmt.Errorf("registration rejected: %s", regResp.Message)
	}

	log.Printf("[RELAY] Registered with relay as device %s", deviceID[:8])

	// Cache the relay-assigned subdomain URL so heartbeat publishes
	// it as publicUrl. Fire-and-forget — heartbeat reads from the
	// same package-level variable on its next tick.
	if regResp.AssignedURL != "" {
		setAssignedRelayURL(regResp.AssignedURL)
		log.Printf("[RELAY] Assigned public URL %s — publishing as publicUrl on next heartbeat",
			regResp.AssignedURL)
	}

	// Re-register any active expose entries on the new connection
	if exposeMgr != nil {
		exposeMgr.SetConn(conn, deviceID)
	}

	// Handle incoming proxied requests.
	//
	// Timeout = 15 min so slow /dev/* handlers (Metro bundling, hermesc
	// compile, asset copy — combined 60-120s for a SFMG-sized RN app) can
	// finish before the agent's own per-handler caps (bundleBuildTimeout
	// 8m, hermesCompileTimeout 3m) fire. The previous 60s default cut the
	// connection right after Metro's "Done writing bundle output", before
	// hermesc could even start. The mobile then saw an HTML 504 page and
	// surfaced "JSON Parse error: Unexpected character: h" — symptom that
	// always means a relay/proxy timeout, never a real bundle problem.
	localClient := &http.Client{Timeout: 15 * time.Minute}

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept stream: %w", err)
		}
		go relayHandleProxiedRequest(stream, agentAddr, localClient)
	}
}

func relayHandleProxiedRequest(stream quic.Stream, agentAddr string, client *http.Client) {
	defer stream.Close()

	// Read the TunnelRequest JSON envelope WITHOUT waiting for EOF on
	// the stream. The previous `io.ReadAll(stream)` worked for normal
	// HTTP requests because the relay calls `stream.Close()` after
	// writing the request envelope (signaling end-of-write to the
	// agent), so the read returns once the FIN arrives. But for
	// WebSocket upgrades — Metro HMR, the dashboard's /ws/terminal,
	// and any future bidirectional channel — the relay deliberately
	// keeps the stream open for bidirectional proxying after writing
	// the envelope. ReadAll then blocks forever, the agent never
	// processes the request, the browser sees no 101 and the shell
	// modal renders "connection error / disconnected" with nothing
	// in any log to point at the cause.
	//
	// json.NewDecoder reads only as many bytes as the JSON value
	// needs and returns; any over-read bytes (the streaming-wire
	// post-envelope payload, if any) are buffered inside the decoder
	// and we drain them via decoder.Buffered() into the backend pipe
	// in the WS branch so the first WebSocket frame the browser sent
	// before the agent finished the handshake doesn't get dropped.
	// 64 MiB cap mirrors the relay's outbound limit.
	limited := io.LimitReader(stream, 64<<20)
	decoder := json.NewDecoder(limited)
	var req relayTunnelRequest
	if err := decoder.Decode(&req); err != nil {
		log.Printf("[RELAY] read/parse request: %v", err)
		return
	}
	envelopeOverflow := decoder.Buffered()

	// Build local HTTP request
	target := agentAddr
	if req.TargetPort > 0 {
		target = fmt.Sprintf("127.0.0.1:%d", req.TargetPort)
	}
	url := fmt.Sprintf("http://%s%s", target, req.Path)
	if req.Query != "" {
		url += "?" + req.Query
	}

	httpReq, err := http.NewRequest(req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		log.Printf("[RELAY] build request: %v", err)
		relaySendError(stream, req.ID, 500, "failed to build request")
		return
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Check if WebSocket upgrade (Metro HMR, debugger, /ws/terminal)
	isWebSocket := strings.EqualFold(req.Headers["Upgrade"], "websocket")
	if isWebSocket {
		// Open raw TCP to the local agent HTTP server and bidirectionally proxy
		backendConn, err := net.Dial("tcp", target)
		if err != nil {
			relaySendError(stream, req.ID, 502, fmt.Sprintf("agent unavailable: %v", err))
			return
		}
		defer backendConn.Close()

		// Forward the original HTTP upgrade request
		if err := httpReq.Write(backendConn); err != nil {
			relaySendError(stream, req.ID, 502, fmt.Sprintf("write upgrade: %v", err))
			return
		}

		// Bidirectional copy between QUIC stream and local TCP. The
		// JSON decoder may have over-read past the envelope (e.g.
		// the relay flushed the envelope + the first WS frame from
		// the browser in a single QUIC packet); drain that buffer
		// into the backend BEFORE handing the stream to io.Copy so
		// no client bytes are dropped on the floor.
		done := make(chan struct{}, 2)
		go func() {
			if envelopeOverflow != nil {
				io.Copy(backendConn, envelopeOverflow)
			}
			io.Copy(backendConn, stream)
			done <- struct{}{}
		}()
		go func() { io.Copy(stream, backendConn); done <- struct{}{} }()
		<-done
		return
	}

	// Check if SSE request — KEEP IN SYNC with relay/tunnel.go's
	// SSE detection. Anything that streams forever must be on this
	// list, otherwise the agent's tunnel-client buffers the response
	// body and the client times out with no response. The smoke for
	// /blackbox/command-stream surfaced this before /command-stream
	// was added.
	// Hybrid SSE detection — Accept header OR path suffix.
	// KEEP IN SYNC with relay/server.go and relay/tunnel.go.
	isSSE := req.Method == "GET" &&
		(strings.Contains(req.Headers["Accept"], "text/event-stream") ||
			strings.Contains(req.Path, "/output") ||
			strings.HasSuffix(req.Path, "/dev/events") ||
			strings.HasSuffix(req.Path, "/subscribe") ||
			strings.HasSuffix(req.Path, "/blackbox/command-stream") ||
			strings.HasSuffix(req.Path, "/blackbox/stream") ||
			strings.HasSuffix(req.Path, "/feedback/stream") ||
			strings.Contains(req.Path, "/streams/"))

	if isSSE {
		sseClient := &http.Client{Timeout: 10 * time.Minute}
		resp, err := sseClient.Do(httpReq)
		if err != nil {
			relaySendError(stream, req.ID, 502, fmt.Sprintf("agent error: %v", err))
			return
		}
		defer resp.Body.Close()

		// STREAMING DEBUG
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := stream.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}

	// Regular request
	resp, err := client.Do(httpReq)
	if err != nil {
		relaySendError(stream, req.ID, 502, fmt.Sprintf("agent error: %v", err))
		return
	}
	defer resp.Body.Close()

	// Streaming wire format. The legacy JSON envelope buffered the
	// entire response body inline, then writing 11 MB to a single QUIC
	// stream truncated under quic-go's default 6 MB stream window —
	// the relay then logged "unexpected end of JSON input" and the
	// iPhone got HTTP 502. The streaming format pushes 64 KB chunks
	// with their own length prefixes, never holding more than that in
	// flight at a time. See relay_stream_wire.go for the full spec.
	if err := writeStreamingResponse(stream, resp); err != nil {
		log.Printf("[RELAY] stream response failed: %v", err)
		return
	}
}

// ---------------------------------------------------------------------------
// mcp — Start MCP server (local stdio or network HTTP)
// ---------------------------------------------------------------------------

func runMCP(args []string) {
	// Check for subcommands first
	if len(args) > 0 {
		switch args[0] {
		case "deploy":
			mcpRemoteCmd("deploy", args[1:])
			return
		case "list":
			mcpRemoteCmd("list", args[1:])
			return
		case "remove":
			mcpRemoteCmd("remove", args[1:])
			return
		case "status":
			mcpRemoteCmd("status", args[1:])
			return
		case "setup":
			runMCPSetup(args[1:])
			return
		case "unregister":
			runMCPUnregister(args[1:])
			return
		}
	}

	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	mode := fs.String("mode", "stdio", "MCP transport: stdio (for Claude Desktop) or http (network)")
	httpPort := fs.Int("port", 18090, "HTTP port for network MCP mode")
	workDir := fs.String("work-dir", ".", "Working directory for tasks")
	fs.Parse(args)

	if *workDir == "." {
		wd, _ := os.Getwd()
		*workDir = wd
	}

	// Pin the AI session's cwd so MCP push/build tools default to the
	// directory the AI is actually working in, not whatever `os.Getwd()`
	// happens to be when each tool runs. Stdio MCP inherits $PWD from
	// the AI client at spawn time; this just records it once. HTTP MCP
	// callers can override per-call via YAVER_MCP_CWD env. See
	// mcp_session_cwd.go for the resolver and why it matters when a
	// user runs `claude` from inside their app repo (e.g. sfmg) rather
	// than the yaver.io checkout.
	SetMCPSessionCwd(*workDir)

	cfg, _ := LoadConfig()
	if cfg == nil {
		cfg = &Config{}
	}

	// Build a minimal task manager for MCP
	taskStore, _ := NewTaskStore()
	runner := defaultRunner
	if r, ok := builtinRunners["claude"]; ok {
		runner = r
	}
	taskMgr := NewTaskManager(*workDir, taskStore, runner)
	if cfg.Sandbox != nil {
		taskMgr.Sandbox = *cfg.Sandbox
	} else {
		taskMgr.Sandbox = DefaultSandboxConfig()
	}

	// Init ACL
	aclMgr := NewACLManager(cfg.ACLPeers)

	// Init email
	var emailMgr *EmailManager
	if cfg.Email != nil && cfg.Email.Provider != "" {
		emailMgr, _ = NewEmailManager(cfg.Email)
	}

	switch *mode {
	case "stdio":
		// The work-dir line doubles as the session-cwd indicator —
		// every push/build/dev MCP tool that doesn't get an explicit
		// path will default here. If a user complains "wireless_push
		// kept building the wrong project", this is the first place
		// to look.
		fmt.Fprintf(os.Stderr, "Yaver MCP server (stdio) v%s — session cwd: %s\n", version, *workDir)
		runMCPStdio(taskMgr, aclMgr, emailMgr)
	case "http":
		fmt.Printf("Yaver MCP server (HTTP) v%s on port %d — work dir: %s\n", version, *httpPort, *workDir)
		hostname, _ := os.Hostname()
		srv := NewHTTPServer(*httpPort, "", "", "", "", hostname, taskMgr)
		srv.devServerMgr = NewDevServerManager()
		srv.devServerMgr.AgentURL = fmt.Sprintf("http://127.0.0.1:%d", *httpPort)
		srv.agentGraphMgr = NewAgentGraphManager(taskMgr)
		globalAgentGraphMgr = srv.agentGraphMgr
		srv.aclMgr = aclMgr
		srv.emailMgr = emailMgr
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			cancel()
		}()

		if err := srv.Start(ctx); err != nil {
			log.Fatalf("MCP HTTP server error: %v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown MCP mode: %s (use 'stdio' or 'http')\n", *mode)
		os.Exit(1)
	}
}

// runMCPStdio runs MCP over stdin/stdout (JSON-RPC 2.0, one request per line).
func runMCPStdio(taskMgr *TaskManager, aclMgr *ACLManager, emailMgr *EmailManager) {
	hostname, _ := os.Hostname()
	srv := &HTTPServer{
		hostname: hostname,
		taskMgr:  taskMgr,
		aclMgr:   aclMgr,
		emailMgr: emailMgr,
	}
	srv.devServerMgr = NewDevServerManager()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32700, Message: "Parse error"}}
			data, _ := json.Marshal(resp)
			fmt.Println(string(data))
			continue
		}

		var resp mcpResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		// Notifications (method starts with "notifications/") have no id and must not receive a response.
		if strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		switch req.Method {
		case "initialize":
			resp.Result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "yaver", "version": version},
			}
		case "tools/list":
			// Reuse the same tool list from the HTTP handler
			resp.Result = srv.getMCPToolsList()
		case "tools/call":
			resp.Result = srv.handleMCPToolCall(req.Params)
		default:
			resp.Error = &mcpError{Code: -32601, Message: "Method not found: " + req.Method}
		}

		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
	}
}

// ---------------------------------------------------------------------------
// email — Email connector setup and management
// ---------------------------------------------------------------------------

func runEmail(args []string) {
	if len(args) == 0 {
		fmt.Print(`Yaver Email — inbox sync + transactional outbound mail

Usage:
  yaver email setup           Interactive email setup (inbox sync)
  yaver email test            Send a test email via the inbox provider
  yaver email sync            Sync emails from provider to local database
  yaver email status          Show email configuration status

Transactional outbound (SMTP relay, for "password reset" style mail):
  yaver email send --to <addr> --subject <s> [--body <t>] [--html <h>]
  yaver email config smtp --host <h> --port <p> --user <u> --pass <p> --from <addr>
  yaver email config show
  yaver email sent [N]
`)
		return
	}

	switch args[0] {
	case "setup":
		runEmailSetup()
	case "test":
		runEmailTest()
	case "sync":
		runEmailSync()
	case "status":
		runEmailStatus()
	// --- transactional outbound ---
	case "send":
		emailSendCmd(args[1:])
	case "config":
		emailConfigCmd(args[1:])
	case "sent":
		emailSentCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown email command: %s\n", args[0])
		os.Exit(1)
	}
}

func runEmailSetup() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	fmt.Println("Email Provider Setup")
	fmt.Println()
	fmt.Println("  1. Office 365 (Microsoft Graph API)")
	fmt.Println("  2. Gmail (Google API)")
	fmt.Print("\nSelect provider (1 or 2): ")

	var choice string
	fmt.Scanln(&choice)

	if cfg.Email == nil {
		cfg.Email = &EmailConfig{}
	}

	switch choice {
	case "1":
		cfg.Email.Provider = "office365"
		fmt.Print("Azure Tenant ID: ")
		fmt.Scanln(&cfg.Email.AzureTenantID)
		fmt.Print("Azure Client ID: ")
		fmt.Scanln(&cfg.Email.AzureClientID)
		fmt.Print("Azure Client Secret: ")
		fmt.Scanln(&cfg.Email.AzureClientSecret)
		fmt.Print("Sender Email: ")
		fmt.Scanln(&cfg.Email.SenderEmail)
	case "2":
		cfg.Email.Provider = "gmail"
		fmt.Print("Google Client ID: ")
		fmt.Scanln(&cfg.Email.GoogleClientID)
		fmt.Print("Google Client Secret: ")
		fmt.Scanln(&cfg.Email.GoogleClientSecret)
		fmt.Print("Google Refresh Token: ")
		fmt.Scanln(&cfg.Email.GoogleRefreshToken)
		fmt.Print("Sender Email: ")
		fmt.Scanln(&cfg.Email.SenderEmail)
	default:
		fmt.Fprintln(os.Stderr, "Invalid choice.")
		os.Exit(1)
	}

	if err := SaveConfig(cfg); err != nil {
		log.Fatalf("save config: %v", err)
	}
	fmt.Printf("\nEmail configured: %s (%s)\n", cfg.Email.Provider, cfg.Email.SenderEmail)
	fmt.Println("Run 'yaver email test' to verify.")
}

func runEmailTest() {
	cfg, err := LoadConfig()
	if err != nil || cfg.Email == nil || cfg.Email.Provider == "" {
		fmt.Fprintln(os.Stderr, "Email not configured. Run 'yaver email setup' first.")
		os.Exit(1)
	}
	mgr, err := NewEmailManager(cfg.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Email init failed: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	fmt.Printf("Sending test email to %s...\n", cfg.Email.SenderEmail)
	if err := mgr.SendEmail(cfg.Email.SenderEmail, "Yaver Email Test", "This is a test email from Yaver.io email connector.", ""); err != nil {
		fmt.Fprintf(os.Stderr, "Send failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Test email sent successfully!")
}

func runEmailSync() {
	cfg, err := LoadConfig()
	if err != nil || cfg.Email == nil || cfg.Email.Provider == "" {
		fmt.Fprintln(os.Stderr, "Email not configured. Run 'yaver email setup' first.")
		os.Exit(1)
	}
	mgr, err := NewEmailManager(cfg.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Email init failed: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	fmt.Println("Syncing emails...")
	count, err := mgr.SyncEmails()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Sync failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Synced %d emails to local database.\n", count)
}

func runEmailStatus() {
	cfg, err := LoadConfig()
	if err != nil || cfg.Email == nil || cfg.Email.Provider == "" {
		fmt.Println("Email: not configured")
		fmt.Println("Run 'yaver email setup' to configure.")
		return
	}
	fmt.Printf("Email Provider: %s\n", cfg.Email.Provider)
	fmt.Printf("Sender: %s\n", cfg.Email.SenderEmail)
}

// ---------------------------------------------------------------------------
// mcp subcommands — interact with a remote yaver-mcp server
// ---------------------------------------------------------------------------

func mcpRemoteCmd(subcmd string, args []string) {
	fs := flag.NewFlagSet("mcp "+subcmd, flag.ExitOnError)
	serverURL := fs.String("server", "http://localhost:18100", "MCP server URL")
	password := fs.String("password", "", "MCP server password (env: MCP_PASSWORD)")
	fs.Parse(args)

	pw := *password
	if pw == "" {
		pw = os.Getenv("MCP_PASSWORD")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	authHeader := ""
	if pw != "" {
		authHeader = "Bearer " + pw
	}

	switch subcmd {
	case "deploy":
		remaining := fs.Args()
		if len(remaining) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: yaver mcp deploy <plugin-dir> [--server URL] [--password PW]")
			os.Exit(1)
		}
		dir := remaining[0]

		// Read manifest
		manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: manifest.json not found in %s\n", dir)
			os.Exit(1)
		}
		var manifest struct {
			Name    string        `json:"name"`
			Version string        `json:"version"`
			Tools   []interface{} `json:"tools"`
		}
		json.Unmarshal(manifestData, &manifest)
		fmt.Printf("Deploying %s v%s (%d tools) to %s...\n", manifest.Name, manifest.Version, len(manifest.Tools), *serverURL)

		// Create tar.gz
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			if rel == "." {
				return nil
			}
			if info.IsDir() && (info.Name() == ".git" || info.Name() == "node_modules") {
				return filepath.SkipDir
			}
			hdr, _ := tar.FileInfoHeader(info, "")
			hdr.Name = rel
			tw.WriteHeader(hdr)
			if !info.IsDir() {
				f, _ := os.Open(path)
				io.Copy(tw, f)
				f.Close()
			}
			return nil
		})
		tw.Close()
		gw.Close()

		req, _ := http.NewRequest("POST", *serverURL+"/plugins/deploy", &buf)
		req.Header.Set("Content-Type", "application/gzip")
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		// STREAMING DEBUG
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if resp.StatusCode == 201 {
			fmt.Printf("Deployed %s (%v tools registered)\n", manifest.Name, result["tools"])
		} else {
			fmt.Fprintf(os.Stderr, "Deploy failed: %v\n", result["error"])
			os.Exit(1)
		}

	case "list":
		req, _ := http.NewRequest("GET", *serverURL+"/plugins", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		// STREAMING DEBUG
		var data struct {
			Plugins []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Tools   int    `json:"tools"`
				Healthy bool   `json:"healthy"`
			} `json:"plugins"`
		}
		json.NewDecoder(resp.Body).Decode(&data)
		if len(data.Plugins) == 0 {
			fmt.Println("No plugins deployed.")
			return
		}
		fmt.Printf("Deployed plugins (%d):\n\n", len(data.Plugins))
		fmt.Printf("  %-20s %-10s %-8s %s\n", "NAME", "VERSION", "TOOLS", "STATUS")
		for _, p := range data.Plugins {
			status := "healthy"
			if !p.Healthy {
				status = "unhealthy"
			}
			fmt.Printf("  %-20s %-10s %-8d %s\n", p.Name, p.Version, p.Tools, status)
		}

	case "remove":
		remaining := fs.Args()
		if len(remaining) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: yaver mcp remove <plugin-name>")
			os.Exit(1)
		}
		req, _ := http.NewRequest("DELETE", *serverURL+"/plugins?name="+remaining[0], nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			fmt.Printf("Plugin '%s' removed.\n", remaining[0])
		} else {
			fmt.Fprintf(os.Stderr, "Remove failed (HTTP %d)\n", resp.StatusCode)
		}

	case "status":
		req, _ := http.NewRequest("GET", *serverURL+"/health", nil)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println("MCP server is DOWN")
			os.Exit(1)
		}
		defer resp.Body.Close()

		// STREAMING DEBUG
		var data map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&data)
		fmt.Println("MCP server is UP")
		if v, ok := data["version"]; ok {
			fmt.Printf("  Version:  %v\n", v)
		}
		if u, ok := data["uptime"]; ok {
			fmt.Printf("  Uptime:   %v\n", u)
		}
		if p, ok := data["plugins"]; ok {
			fmt.Printf("  Plugins:  %v\n", p)
		}
	}
}

// ---------------------------------------------------------------------------
// acl — Agent Communication Layer
// ---------------------------------------------------------------------------

func runACL(args []string) {
	if len(args) == 0 {
		fmt.Print(`Yaver ACL — Agent Communication Layer

Connect to other MCP servers (local or remote) to extend Yaver's capabilities.

Usage:
  yaver acl add <name> <url> [--auth <token>]     Add HTTP MCP peer
  yaver acl add <name> --stdio "<command>"         Add stdio MCP peer
  yaver acl list                                    List connected peers
  yaver acl remove <id>                             Remove a peer
  yaver acl tools <id>                              List peer's available tools
  yaver acl health                                  Health check all peers

Examples:
  yaver acl add ollama http://localhost:11434/mcp
  yaver acl add filesystem --stdio "npx -y @modelcontextprotocol/server-filesystem /home"
  yaver acl add remote-db https://db.example.com/mcp --auth mytoken123
`)
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	switch args[0] {
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: yaver acl add <name> <url> [--auth <token>]")
			fmt.Fprintln(os.Stderr, "       yaver acl add <name> --stdio \"<command>\"")
			os.Exit(1)
		}
		name := args[1]
		peer := ACLPeerConfig{
			ID:   strings.ToLower(strings.ReplaceAll(name, " ", "-")),
			Name: name,
			Type: "http",
		}

		// Parse remaining args
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--stdio":
				peer.Type = "stdio"
				if i+1 < len(args) {
					i++
					peer.Command = args[i]
				}
			case "--auth":
				if i+1 < len(args) {
					i++
					peer.Auth = args[i]
				}
			default:
				if peer.URL == "" && peer.Type == "http" {
					peer.URL = args[i]
				}
			}
		}

		cfg.ACLPeers = append(cfg.ACLPeers, peer)
		if err := SaveConfig(cfg); err != nil {
			log.Fatalf("save config: %v", err)
		}
		fmt.Printf("Added MCP peer: %s (%s)\n", name, func() string {
			if peer.Type == "stdio" {
				return "stdio: " + peer.Command
			}
			return peer.URL
		}())

	case "list":
		if len(cfg.ACLPeers) == 0 {
			fmt.Println("No MCP peers configured.")
			fmt.Println("Use 'yaver acl add' to connect to an MCP server.")
			return
		}
		for _, p := range cfg.ACLPeers {
			target := p.URL
			if p.Type == "stdio" {
				target = "stdio: " + p.Command
			}
			fmt.Printf("  [%s] %s — %s\n", p.ID, p.Name, target)
		}

	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver acl remove <id>")
			os.Exit(1)
		}
		id := args[1]
		var remaining []ACLPeerConfig
		found := false
		for _, p := range cfg.ACLPeers {
			if p.ID == id {
				found = true
				continue
			}
			remaining = append(remaining, p)
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Peer not found: %s\n", id)
			os.Exit(1)
		}
		cfg.ACLPeers = remaining
		if err := SaveConfig(cfg); err != nil {
			log.Fatalf("save config: %v", err)
		}
		fmt.Printf("Removed peer: %s\n", id)

	case "tools":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver acl tools <peer-id>")
			os.Exit(1)
		}
		aclMgr := NewACLManager(cfg.ACLPeers)
		defer aclMgr.Shutdown()
		tools, err := aclMgr.ListPeerTools(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		for _, t := range tools {
			name, _ := t["name"].(string)
			desc, _ := t["description"].(string)
			fmt.Printf("  %s — %s\n", name, desc)
		}

	case "health":
		aclMgr := NewACLManager(cfg.ACLPeers)
		defer aclMgr.Shutdown()
		health := aclMgr.HealthCheck()
		for id, ok := range health {
			status := "healthy"
			if !ok {
				status = "unreachable"
			}
			fmt.Printf("  %s: %s\n", id, status)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown ACL command: %s\n", args[0])
		os.Exit(1)
	}
}

func relaySendError(stream quic.Stream, id string, code int, msg string) {
	resp := relayTunnelResponse{
		ID:         id,
		StatusCode: code,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       []byte(fmt.Sprintf(`{"ok":false,"error":"%s"}`, msg)),
	}
	data, _ := json.Marshal(resp)
	stream.Write(data)
}

// checkLatestVersion fetches the latest CLI version from Convex /config
// and prints an upgrade notice if a newer version is available.
func checkLatestVersion() {
	convexURL := defaultConvexSiteURL
	if cfg, err := LoadConfig(); err == nil && cfg.ConvexSiteURL != "" {
		convexURL = cfg.ConvexSiteURL
	}

	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", convexURL+"/config", nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// STREAMING DEBUG
	if resp.StatusCode != 200 {
		return
	}

	var result struct {
		CliVersion    string `json:"cliVersion"`
		MobileVersion string `json:"mobileVersion"`
		RelayVersion  string `json:"relayVersion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	if result.CliVersion != "" && result.CliVersion != version && isNewerVersion(result.CliVersion, version) {
		fmt.Printf("\nUpdate available: %s → %s\n", version, result.CliVersion)
		fmt.Println("  npm install -g yaver-cli@latest")
	}
}

// fetchLatestCLIVersion returns the latest CLI version from Convex platformConfig.
func fetchLatestCLIVersion() string {
	convexURL := defaultConvexSiteURL
	if cfg, err := LoadConfig(); err == nil && cfg.ConvexSiteURL != "" {
		convexURL = cfg.ConvexSiteURL
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", convexURL+"/config", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	defer resp.Body.Close()

	// STREAMING DEBUG
	var result struct {
		CliVersion string `json:"cliVersion"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.CliVersion
}

// isNewerVersion returns true if a is a higher semver than b (e.g. "1.40.0" > "1.39.0").
func isNewerVersion(a, b string) bool {
	parse := func(v string) (int, int, int) {
		v = strings.TrimPrefix(v, "v")
		parts := strings.Split(v, ".")
		major, minor, patch := 0, 0, 0
		if len(parts) >= 1 {
			fmt.Sscanf(parts[0], "%d", &major)
		}
		if len(parts) >= 2 {
			fmt.Sscanf(parts[1], "%d", &minor)
		}
		if len(parts) >= 3 {
			fmt.Sscanf(parts[2], "%d", &patch)
		}
		return major, minor, patch
	}
	a1, a2, a3 := parse(a)
	b1, b2, b3 := parse(b)
	if a1 != b1 {
		return a1 > b1
	}
	if a2 != b2 {
		return a2 > b2
	}
	return a3 > b3
}
