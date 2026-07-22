package main

// `yaver install <tool>` — one-shot dependency installer for the
// integrations the local-CI runner needs. The point is "make
// yaver-test-sdk Just Work after `npm install -g yaver-cli`" without forcing
// the user to learn the package layouts of every browser, every mobile
// SDK, and every test framework.
//
// Strict rules:
//
//   - Most installs shell out to the user's existing package manager
//     (brew on macOS, apt/dnf on Linux). Agent-managed runtimes such
//     as Node are the exception: Yaver downloads them into
//     ~/.yaver/runtimes so headless boxes do not need sudo.
//   - We only support macOS and Linux. Windows users are out of scope
//     for the local-CI persona.
//   - Every "tool" is one or more package manager commands. We don't
//     hide them behind a custom installer.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

type installPlan struct {
	name        string
	description string
	// macOS commands run sequentially with `brew`
	macOS []string
	// Linux commands; the runner picks the first one whose
	// underlying tool exists (apt-get, dnf, pacman, etc).
	linux []linuxStep
	// runFunc, when non-nil, fully replaces the macOS/linux shell
	// recipes — used for sudo-free in-process installers (e.g. Node
	// runtime extracted to ~/.yaver/runtimes/node) so an HTTP-driven
	// install from the phone never has to prompt for a password.
	runFunc func(ctx context.Context, progress func(string)) error
}

type linuxStep struct {
	manager string // "apt-get" | "dnf" | "pacman"
	cmd     string
}

// integrations is the catalogue. Adding a new tool = adding an entry.
var integrations = []installPlan{
	{
		name:        "git",
		description: "Git — required for repo sync, worktrees, and nearly every Yaver coding loop",
		macOS:       []string{"brew install git"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y git"},
			{"dnf", "sudo dnf install -y git"},
			{"pacman", "sudo pacman -S --noconfirm git"},
		},
	},
	{
		name:        "gh",
		description: "GitHub CLI — useful for repo auth, PR flows, and headless developer setup",
		macOS:       []string{"brew install gh"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y gh"},
			{"dnf", "sudo dnf install -y gh"},
			{"pacman", "sudo pacman -S --noconfirm github-cli"},
		},
	},
	{
		name:        "glab",
		description: "GitLab CLI — same role as gh for GitLab repos (MR/issue/CI flows, token autodetect)",
		macOS:       []string{"brew install glab"},
		linux: []linuxStep{
			// Debian / Ubuntu: glab isn't in the default apt repos. Use
			// the upstream tarball for x86_64 + arm64 (covers Pi, ARM
			// cloud nodes, the yaver-test-ephemeral box).
			{"apt-get", "ARCH=$(uname -m); case \"$ARCH\" in x86_64) GLAB_ARCH=amd64 ;; aarch64|arm64) GLAB_ARCH=arm64 ;; *) GLAB_ARCH=amd64 ;; esac; curl -fsSL -o /tmp/glab.tar.gz \"https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/releases/permalink/latest/downloads/glab_${GLAB_ARCH}_linux.tar.gz\" && sudo tar -xzf /tmp/glab.tar.gz -C /usr/local/bin bin/glab --strip-components=1 && rm -f /tmp/glab.tar.gz"},
			{"dnf", "sudo dnf install -y glab"},
			{"pacman", "sudo pacman -S --noconfirm glab"},
		},
	},
	{
		name:        "uv",
		description: "uv — fast Python/environment manager for modern dev boxes",
		macOS:       []string{"brew install uv"},
		linux: []linuxStep{
			{"curl", "curl -LsSf https://astral.sh/uv/install.sh | sh"},
		},
	},
	{
		name:        "docker",
		description: "Docker engine / Docker Desktop for containerized dev, isolated job runners, and compose workflows",
		macOS:       []string{"brew install --cask docker"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get update && sudo apt-get install -y docker.io docker-compose-v2"},
			{"dnf", "sudo dnf install -y docker docker-compose-plugin"},
			{"pacman", "sudo pacman -S --noconfirm docker docker-compose"},
		},
	},
	{
		name:        "tailscale",
		description: "Tailscale — private remote access to the dev node without exposing SSH or ports publicly",
		macOS:       []string{"brew install --cask tailscale"},
		linux: []linuxStep{
			{"curl", "curl -fsSL https://tailscale.com/install.sh | sh"},
		},
	},
	{
		name:        "cloudflared",
		description: "Cloudflare Tunnel client — optional public tunnel for dashboards or webhooks",
		macOS:       []string{"brew install cloudflared"},
		linux: []linuxStep{
			{"curl", "curl -fsSL https://pkg.cloudflare.com/install.sh | sudo bash && sudo apt-get install -y cloudflared"},
			{"dnf", "sudo dnf install -y cloudflared"},
			{"pacman", "sudo pacman -S --noconfirm cloudflared"},
		},
	},
	{
		name:        "sqlite3",
		description: "SQLite CLI and libraries — local-first app storage, migrations, and quick backend prototypes",
		macOS:       []string{"brew install sqlite"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y sqlite3 libsqlite3-dev"},
			{"dnf", "sudo dnf install -y sqlite sqlite-devel"},
			{"pacman", "sudo pacman -S --noconfirm sqlite"},
		},
	},
	{
		name:        "rg",
		description: "ripgrep — fast recursive search used by coding agents, Vim/Neovim configs, and terminal workflows",
		macOS:       []string{"brew install ripgrep"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y ripgrep"},
			{"dnf", "sudo dnf install -y ripgrep"},
			{"pacman", "sudo pacman -S --noconfirm ripgrep"},
		},
	},
	{
		name:        "fd",
		description: "fd — fast file finder commonly used with fzf and editor integrations",
		macOS:       []string{"brew install fd"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y fd-find"},
			{"dnf", "sudo dnf install -y fd-find"},
			{"pacman", "sudo pacman -S --noconfirm fd"},
		},
	},
	{
		name:        "bat",
		description: "bat — syntax-highlighted cat replacement used by fzf previews and terminal workflows",
		macOS:       []string{"brew install bat"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y bat"},
			{"dnf", "sudo dnf install -y bat"},
			{"pacman", "sudo pacman -S --noconfirm bat"},
		},
	},
	{
		name:        "jq",
		description: "jq — JSON inspection and scripting utility used by deploy and terminal workflows",
		macOS:       []string{"brew install jq"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y jq"},
			{"dnf", "sudo dnf install -y jq"},
			{"pacman", "sudo pacman -S --noconfirm jq"},
		},
	},
	{
		name:        "fzf",
		description: "fzf — fuzzy finder for shell, Vim/Neovim, and tmux workflows",
		macOS:       []string{"brew install fzf"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y fzf"},
			{"dnf", "sudo dnf install -y fzf"},
			{"pacman", "sudo pacman -S --noconfirm fzf"},
		},
	},
	{
		name:        "vercel",
		description: "Vercel CLI — deploy web apps and edge backends from the Pi dev node",
		runFunc:     runVercelInstall,
	},
	{
		name:        "convex",
		description: "Convex CLI — managed backend workflows and local project setup from the Pi dev node",
		runFunc:     runConvexInstall,
	},
	{
		name:        "wrangler",
		description: "Cloudflare Wrangler CLI — Workers/Pages deploys and D1/R2/Queues workflows from a remote dev node",
		macOS:       []string{"npm install -g wrangler"},
		linux: []linuxStep{
			{"npm", "npm install -g wrangler"},
		},
	},
	{
		name:        "go",
		description: "Go toolchain — build/test Go services, CLIs, and Yaver backend helpers",
		macOS:       []string{"brew install go"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y golang-go"},
			{"dnf", "sudo dnf install -y golang"},
			{"pacman", "sudo pacman -S --noconfirm go"},
		},
	},
	{
		name:        "postgresql-client",
		description: "PostgreSQL client tools — psql, pg_dump, and migration utilities",
		macOS:       []string{"brew install libpq"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y postgresql-client"},
			{"dnf", "sudo dnf install -y postgresql"},
			{"pacman", "sudo pacman -S --noconfirm postgresql-libs postgresql"},
		},
	},
	{
		name:        "postgresql",
		description: "PostgreSQL server — local relational backend for promoted phone-born apps",
		macOS:       []string{"brew install postgresql@16"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y postgresql postgresql-contrib"},
			{"dnf", "sudo dnf install -y postgresql-server postgresql-contrib"},
			{"pacman", "sudo pacman -S --noconfirm postgresql"},
		},
	},
	{
		name:        "redis-tools",
		description: "Redis CLI tools — inspect and script caches, queues, and pub/sub flows",
		macOS:       []string{"brew install redis"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y redis-tools"},
			{"dnf", "sudo dnf install -y redis"},
			{"pacman", "sudo pacman -S --noconfirm redis"},
		},
	},
	{
		name:        "redis-server",
		description: "Redis server — local cache, queue, and event bus for app backends",
		macOS:       []string{"brew install redis"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y redis-server"},
			{"dnf", "sudo dnf install -y redis"},
			{"pacman", "sudo pacman -S --noconfirm redis"},
		},
	},
	{
		name:        "supabase",
		description: "Supabase local tooling — npx-backed CLI wrapper for Docker-based local stacks",
		runFunc:     runSupabaseInstall,
	},
	{
		name:        "mqtt-broker",
		description: "Mosquitto MQTT broker — lightweight pub/sub for devices, automations, and app events",
		macOS:       []string{"brew install mosquitto"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y mosquitto"},
			{"dnf", "sudo dnf install -y mosquitto"},
			{"pacman", "sudo pacman -S --noconfirm mosquitto"},
		},
	},
	{
		name:        "mqtt-clients",
		description: "Mosquitto MQTT client tools — publish/subscribe shells for local event debugging",
		macOS:       []string{"brew install mosquitto"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y mosquitto-clients"},
			{"dnf", "sudo dnf install -y mosquitto-clients"},
			{"pacman", "sudo pacman -S --noconfirm mosquitto"},
		},
	},
	{
		name:        "chrome",
		description: "Google Chrome — required for `yaver test run` web target and the chrome-webrtc preview",
		macOS:       []string{"brew install --cask google-chrome"},
		// `apt-get install google-chrome-stable` FAILS on a stock Debian/Ubuntu:
		// the package is not in their archives, it is in Google's own. Verified
		// on a clean Ubuntu 24.04 box (2026-07-22): the old one-liner returned
		// `E: Unable to locate package google-chrome-stable`.
		//
		// That mattered more than a bad hint. chrome-webrtc is the FALLBACK for
		// every browser-renderable stack when the viewer cannot reach the dev
		// server, so on a fresh Linux workspace the degraded path was
		// uninstallable by following our own instructions. Adding the repo is
		// the only supported way; the keyring form is required since apt-key
		// was removed.
		linux: []linuxStep{
			{"apt-get", "sudo install -d -m 0755 /etc/apt/keyrings && " +
				"curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | sudo gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg && " +
				"echo \"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main\" | sudo tee /etc/apt/sources.list.d/google-chrome.list >/dev/null && " +
				"sudo apt-get update && sudo apt-get install -y google-chrome-stable"},
			{"dnf", "sudo dnf install -y https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm"},
			{"pacman", "sudo pacman -S --noconfirm chromium"}, // google-chrome is AUR-only
		},
	},
	{
		name:        "chromium",
		description: "Chromium — open-source alternative to Chrome",
		macOS:       []string{"brew install --cask chromium"},
		// On Ubuntu, `chromium` has NO apt candidate and `chromium-browser` is a
		// SNAP STUB (2:1snap1-0ubuntu2) — verified on both jammy and noble. A
		// snap shim does not work headless in a container, so prefer real
		// Chrome above; this entry stays for distros that ship a genuine deb.
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y chromium || sudo snap install chromium"},
			{"dnf", "sudo dnf install -y chromium"},
			{"pacman", "sudo pacman -S --noconfirm chromium"},
		},
	},
	{
		name:        "firefox",
		description: "Firefox — optional second browser for cross-browser snapshots",
		macOS:       []string{"brew install --cask firefox"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y firefox"},
			{"dnf", "sudo dnf install -y firefox"},
		},
	},
	{
		name:        "android-sdk",
		description: "Android SDK platform-tools (adb) + emulator — required for `target: android-emu`",
		runFunc:     runAndroidSDKInstall,
	},
	{
		name:        "xcodegen",
		description: "XcodeGen — generates the Swift Todo fixture project and keeps native iOS sample apps reproducible",
		macOS:       []string{"brew install xcodegen"},
	},
	{
		name:        "cliclick",
		description: "cliclick — macOS UI automation helper for native remote-runtime control fallbacks outside Hermes",
		macOS:       []string{"brew install cliclick"},
	},
	{
		name:        "appium",
		description: "Appium — only needed if you want to drive existing Appium specs (yaver-test-sdk has its own bridges from M5). Used by vibe-preview's RN bug-hunter.",
		macOS:       []string{"npm install -g appium"},
		linux: []linuxStep{
			{"npm", "npm install -g appium"},
		},
	},
	{
		name:        "maestro",
		description: "Maestro — declarative mobile E2E flows. Drives demo-clip recordings in vibe-preview (Phase 7 exercise scripts) so MP4s capture interaction instead of an idle home screen.",
		macOS:       []string{"curl -Ls 'https://get.maestro.mobile.dev' | bash"},
		linux: []linuxStep{
			{"curl", "curl -Ls 'https://get.maestro.mobile.dev' | bash"},
		},
	},
	{
		name:        "node",
		description: "Node.js LTS for the Hermes reload stack — installs to ~/.yaver/runtimes/node, sudo-free, modern enough for Expo SDK 50+",
		runFunc: func(ctx context.Context, progress func(string)) error {
			_, err := installNodeRuntime(ctx, progress)
			return err
		},
	},
	{
		name:        "mobile",
		description: "Hermes bundle reload stack for React Native / Expo: Node LTS plus the embedded hermesc sanity check. Meta-target.",
		runFunc: func(ctx context.Context, progress func(string)) error {
			if _, err := installNodeRuntime(ctx, progress); err != nil {
				return err
			}
			summary, err := embeddedHermescSummary()
			if err != nil {
				return err
			}
			if progress != nil {
				progress("Embedded hermesc ready: " + summary)
				progress("Hermes reload stack ready for Open in Yaver on Linux, WSL, or macOS.")
			}
			return nil
		},
	},
	{
		name:        "remote-runtime",
		description: "Native remote-runtime stack for Swift/Kotlin projects: Android emulator tools everywhere, plus macOS host helpers for iOS Simulator and native sample projects. Meta-target.",
		runFunc:     runRemoteRuntimeInstall,
	},
	{
		// webrtc is the Phase-2 alias for the remote-runtime install
		// that ALSO runs `yaver doctor webrtc` at the end so the
		// user sees what's still missing in one shot. The npm
		// postinstall already runs remote-runtime; this entry exists
		// so a user who skipped that path (or deliberately turned it
		// off via YAVER_SKIP_POSTINSTALL_REMOTE_RUNTIME) has an
		// easy command to provision the WebRTC pipeline by name.
		name:        "webrtc",
		description: "WebRTC remote-runtime pipeline (alias for remote-runtime + doctor probe). Run after `npm install -g yaver-cli` if you opted out of the postinstall bootstrap.",
		runFunc:     runWebRTCInstall,
	},
	// Runner installs go through ensureRunnerInstalledStream so a
	// fresh box (Pi, ARM cloud, mac without brew) provisions a
	// sudo-free node runtime into ~/.yaver/runtimes/node before
	// `npm install -g`. Drives the web Devices view and mobile
	// CodingAgentsSection install buttons via /install/<runner> and
	// /peer/<id>/install/<runner>.
	{
		name:        "claude",
		description: "Claude Code — Anthropic's coding agent (subscription OAuth via Yaver). Installs @anthropic-ai/claude-code globally.",
		runFunc: func(ctx context.Context, progress func(string)) error {
			return ensureRunnerInstalledStream(ctx, "claude", progress)
		},
	},
	{
		name:        "codex",
		description: "OpenAI Codex — code-execution agent (ChatGPT Plus OAuth via Yaver). Installs @openai/codex globally.",
		runFunc: func(ctx context.Context, progress func(string)) error {
			return ensureRunnerInstalledStream(ctx, "codex", progress)
		},
	},
	{
		name:        "opencode",
		description: "opencode — yaver's third first-class runner. BYOK any provider (Anthropic / OpenAI / OpenRouter / Ollama / GLM / ZAI / …) via opencode.json.",
		runFunc: func(ctx context.Context, progress func(string)) error {
			return ensureRunnerInstalledStream(ctx, "opencode", progress)
		},
	},
	{
		name:        "pre-commit",
		description: "pre-commit — repo quality gates and fast local hook automation",
		macOS:       []string{"python3 -m pip install --user --upgrade pre-commit"},
		linux: []linuxStep{
			{"pip3", "pip3 install --user --upgrade pre-commit"},
			{"pipx", "pipx install pre-commit"},
		},
	},
	{
		name:        "pytest",
		description: "pytest — default Python unit test runner for TDD loops",
		macOS:       []string{"python3 -m pip install --user --upgrade pytest"},
		linux: []linuxStep{
			{"pip3", "pip3 install --user --upgrade pytest"},
			{"pipx", "pipx install pytest"},
		},
	},
	{
		name:        "ruff",
		description: "ruff — fast Python lint + format tool for low-cost repair loops",
		macOS:       []string{"python3 -m pip install --user --upgrade ruff"},
		linux: []linuxStep{
			{"pip3", "pip3 install --user --upgrade ruff"},
			{"pipx", "pipx install ruff"},
		},
	},
	{
		name:        "vitest",
		description: "Vitest — default TypeScript/JavaScript unit test runner for headless app TDD",
		macOS:       []string{"npm install -g vitest"},
		linux: []linuxStep{
			{"npm", "npm install -g vitest"},
		},
	},
	{
		name:        "eslint",
		description: "ESLint — JavaScript and TypeScript linting support for auto-fix loops",
		macOS:       []string{"npm install -g eslint"},
		linux: []linuxStep{
			{"npm", "npm install -g eslint"},
		},
	},
	{
		name:        "prettier",
		description: "Prettier — formatter for JavaScript/TypeScript/web repos",
		macOS:       []string{"npm install -g prettier"},
		linux: []linuxStep{
			{"npm", "npm install -g prettier"},
		},
	},
	{
		name:        "tdd",
		description: "Core TDD / quality stack: pre-commit, pytest, ruff, vitest, eslint, prettier. Meta-target.",
		runFunc:     runTDDInstall,
	},
	{
		name:        "backend-dev",
		description: "Local backend stack: sqlite, Vercel, Convex, PostgreSQL, Redis, Supabase, MQTT. Meta-target.",
		runFunc:     runBackendDevInstall,
	},
	{
		name:        "pi-dev-node",
		description: "Raspberry Pi / ARM64 headless dev-node profile: AI coding stack + TDD + local/cloud backend tooling. Meta-target.",
		runFunc:     runPiDevNodeInstall,
	},
	{
		name:        "vibe-preview",
		description: "Vibe Preview tool stack: chromium (frame capture), ffmpeg (clip poster), maestro (clip exercises), appium (RN bug-hunter), android-sdk (sim-android clips). macOS-only sim-ios capture relies on xcrun which ships with Xcode. Meta-target.",
		runFunc:     runVibePreviewInstall,
	},
	{
		name:        "tmux",
		description: "tmux — required for the agent's task runner (probably already installed)",
		macOS:       []string{"brew install tmux"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y tmux"},
			{"dnf", "sudo dnf install -y tmux"},
			{"pacman", "sudo pacman -S --noconfirm tmux"},
		},
	},
	{
		name:        "ffmpeg",
		description: "ffmpeg — required for the morning match-report screen recorder (run `yaver record`)",
		macOS:       []string{"brew install ffmpeg"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y ffmpeg"},
			{"dnf", "sudo dnf install -y ffmpeg"},
			{"pacman", "sudo pacman -S --noconfirm ffmpeg"},
		},
	},
}

func runInstall(args []string) {
	if len(args) == 0 {
		printInstallUsage()
		os.Exit(0)
	}
	if args[0] == "list" || args[0] == "--list" {
		listIntegrations()
		return
	}
	if args[0] == "all" {
		for _, plan := range integrations {
			runInstallOne(plan)
		}
		return
	}
	for _, target := range args {
		// WDA has its own handler — it's not a simple package
		// install but a build+install+launch sequence against the
		// booted iOS Simulator.
		if target == "wda" {
			runInstallWDA(args[1:])
			return
		}
		plan, ok := lookupIntegration(target)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown integration %q. Try `yaver install list`.\n", target)
			os.Exit(2)
		}
		runInstallOne(plan)
	}
}

func printInstallUsage() {
	fmt.Print(`Usage:
  yaver install list                  Show available integrations
  yaver install <name> [name…]        Install one or more integrations
  yaver install all                   Install everything (skip what's already there)

Available integrations:
`)
	listIntegrations()
}

func listIntegrations() {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tINSTALLED\tDESCRIPTION")
	for _, plan := range integrations {
		state := checkInstalled(plan.name)
		fmt.Fprintf(w, "%s\t%s\t%s\n", plan.name, state, plan.description)
	}
	// wda is handled by wda_cmd.go, not the generic installPlan
	// table — its "installed" probe is whether WDA answers on
	// :8100, not whether a binary is on PATH.
	wdaState := "—"
	if wdaIsLive() {
		wdaState = "✓"
	}
	fmt.Fprintf(w, "%s\t%s\t%s\n", "wda",
		wdaState,
		"WebDriverAgent for iOS Simulator tap-by-selector (`yaver install wda`)")
	w.Flush()
}

// wdaIsLive reports whether WebDriverAgent is currently answering
// on its default port. Cheap probe used by `yaver install list`.
func wdaIsLive() bool {
	return waitForWDAStatus(500*time.Millisecond) == nil
}

func checkInstalled(name string) string {
	if compositeInstallSatisfied(name) {
		return "✓"
	}

	// Special-case the agent-managed runtimes: they live under
	// ~/.yaver/runtimes/<tool>/bin and are not on the system PATH for
	// CLI users, so a plain LookPath would always say "—".
	switch name {
	case "node":
		if v := nodeRuntimeExisting(runtimeNodeBinDir()); v != "" {
			return "✓"
		}
		return "—"
	case "mobile":
		if v := nodeRuntimeExisting(runtimeNodeBinDir()); v != "" {
			// Mirror the capability snapshot's resolver: embedded
			// prebuilt → system-prewarmed (/usr/local/libexec/yaver).
			// embeddedHermescSummary() alone would mark a perfectly
			// reload-ready linux/arm64 box (no embedded binary, but a
			// prewarmed system hermesc) as not-installed — the same
			// false-negative the Remote Box picker's "Fix this machine"
			// flow must not hit.
			if _, _, err := resolveHermescForCapability(); err == nil {
				return "✓"
			}
		}
		return "—"
	case "android-sdk":
		if findAndroidToolPath("adb") != "" && findAndroidToolPath("emulator") != "" && findAndroidToolPath("sdkmanager") != "" {
			return "✓"
		}
		return "—"
	case "vercel", "convex", "supabase", "wrangler":
		if home, err := os.UserHomeDir(); err == nil {
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", name)); err == nil {
				return "✓"
			}
		}
		if DiscoverBinary(name) != "" {
			return "✓"
		}
		return "—"
	}

	probe := map[string][]string{
		"git":               {"git"},
		"gh":                {"gh"},
		"glab":              {"glab"},
		"uv":                {"uv"},
		"docker":            {"docker"},
		"tailscale":         {"tailscale"},
		"cloudflared":       {"cloudflared"},
		"wrangler":          {"wrangler"},
		"go":                {"go"},
		"rg":                {"rg"},
		"fd":                {"fd", "fdfind"},
		"bat":               {"bat", "batcat"},
		"jq":                {"jq"},
		"fzf":               {"fzf"},
		"sqlite3":           {"sqlite3"},
		"vercel":            {"vercel"},
		"convex":            {"convex"},
		"postgresql-client": {"psql"},
		"postgresql":        {"postgres"},
		"redis-tools":       {"redis-cli"},
		"redis-server":      {"redis-server"},
		"supabase":          {"supabase"},
		"mqtt-broker":       {"mosquitto"},
		"mqtt-clients":      {"mosquitto_pub", "mosquitto_sub"},
		"chrome":            {"google-chrome", "google-chrome-stable", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		"chromium":          {"chromium", "chromium-browser", "/Applications/Chromium.app/Contents/MacOS/Chromium"},
		"firefox":           {"firefox", "/Applications/Firefox.app/Contents/MacOS/firefox"},
		"android-sdk":       {"adb", "emulator"},
		"xcodegen":          {"xcodegen"},
		"cliclick":          {"cliclick"},
		"appium":            {"appium"},
		"maestro":           {"maestro"},
		"claude":            {"claude"},
		"codex":             {"codex"},
		"opencode":          {"opencode"},
		"pre-commit":        {"pre-commit"},
		"pytest":            {"pytest"},
		"ruff":              {"ruff"},
		"vitest":            {"vitest"},
		"eslint":            {"eslint"},
		"prettier":          {"prettier"},
		"tmux":              {"tmux"},
		"ffmpeg":            {"ffmpeg"},
	}
	candidates := probe[name]
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return "✓"
		}
		if c[0] == '/' {
			if _, err := os.Stat(c); err == nil {
				return "✓"
			}
		}
	}
	return "—"
}

func compositeInstallSatisfied(name string) bool {
	required := map[string][]string{
		"tdd":            {"pre-commit", "pytest", "ruff", "vitest", "eslint", "prettier"},
		"backend-dev":    {"sqlite3", "vercel", "convex", "postgresql-client", "postgresql", "redis-tools", "redis-server", "supabase", "mqtt-broker", "mqtt-clients"},
		"pi-dev-node":    {"git", "gh", "uv", "docker", "mobile", "tmux", "ffmpeg", "opencode", "tdd", "backend-dev"},
		"vibe-preview":   {"chromium", "ffmpeg", "maestro", "appium", "android-sdk"},
		"remote-runtime": {"android-sdk"},
	}
	targets, ok := required[name]
	if !ok {
		return false
	}
	for _, target := range targets {
		if checkInstalled(target) != "✓" {
			if name == "remote-runtime" && runtime.GOOS == "darwin" && (target == "xcodegen" || target == "cliclick") {
				continue
			}
			return false
		}
	}
	if name == "remote-runtime" && runtime.GOOS == "darwin" {
		return checkInstalled("xcodegen") == "✓" && checkInstalled("cliclick") == "✓" && checkInstalled("android-sdk") == "✓"
	}
	return true
}

// runtimeNodeBinDir is a tiny convenience wrapper so install_cmd.go
// stays decoupled from filepath spelling.
func runtimeNodeBinDir() string {
	for _, d := range runtimeBinDirs() {
		if strings.HasSuffix(d, "/node/bin") {
			return d
		}
	}
	return ""
}

func lookupIntegration(name string) (installPlan, bool) {
	for _, p := range integrations {
		if p.name == name {
			return p, true
		}
	}
	return installPlan{}, false
}

func runInstallOne(plan installPlan) {
	fmt.Printf("\n=> %s — %s\n", plan.name, plan.description)
	if checkInstalled(plan.name) == "✓" {
		fmt.Println("   already installed, skipping")
		return
	}
	if plan.runFunc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := plan.runFunc(ctx, func(line string) { fmt.Printf("   %s\n", line) }); err != nil {
			fmt.Fprintf(os.Stderr, "   error: %v\n", err)
		}
		return
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err != nil {
			fmt.Fprintln(os.Stderr, "   error: brew not found. Install Homebrew first: https://brew.sh")
			return
		}
		for _, c := range plan.macOS {
			runShellInteractive(c)
		}
	case "linux":
		ran := false
		for _, step := range plan.linux {
			if _, err := exec.LookPath(step.manager); err != nil {
				continue
			}
			runShellInteractive(step.cmd)
			ran = true
			break
		}
		if !ran {
			fmt.Fprintf(os.Stderr, "   error: no supported package manager found (tried: %v)\n", linuxManagers(plan))
		}
	default:
		fmt.Fprintf(os.Stderr, "   error: %s is not supported (yaver local CI is macOS + Linux only)\n", runtime.GOOS)
	}
}

// runInstallPlan is the non-interactive (HTTP-driven) cousin of
// runInstallOne. It streams every output line through `progress`
// instead of printing to a terminal, honors `runFunc` if present,
// and falls back to the shell recipes otherwise. Suitable for the
// /install/<tool> handler so a phone-only user can drive setup
// without ever opening a terminal.
func runInstallPlan(ctx context.Context, plan installPlan, progress func(string)) error {
	if plan.runFunc != nil {
		return plan.runFunc(ctx, progress)
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err != nil {
			return fmt.Errorf("brew not found — install Homebrew first: https://brew.sh")
		}
		for _, c := range plan.macOS {
			if err := runShellStreaming(ctx, c, progress); err != nil {
				return err
			}
		}
		return nil
	case "linux":
		for _, step := range plan.linux {
			if _, err := exec.LookPath(step.manager); err != nil {
				continue
			}
			return runShellStreaming(ctx, step.cmd, progress)
		}
		return fmt.Errorf("no supported package manager (tried: %v)", linuxManagers(plan))
	default:
		return fmt.Errorf("%s is not supported (macOS + Linux only)", runtime.GOOS)
	}
}

// runShellStreaming runs a shell command, streaming combined stdout
// and stderr lines through `progress`. Honors ctx for cancellation.
// Used by the HTTP install path so the phone sees live output.
func runShellStreaming(ctx context.Context, cmdline string, progress func(string)) error {
	progress("$ " + cmdline)
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	cmd.Env = augmentEnv(nil)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for s.Scan() {
			progress(s.Text())
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	return cmd.Wait()
}

func linuxManagers(plan installPlan) []string {
	out := make([]string, 0, len(plan.linux))
	for _, s := range plan.linux {
		out = append(out, s.manager)
	}
	return out
}

// runShellInteractive executes a shell command, streaming stdout/stderr
// to the user. We deliberately use `sh -c` so &&, |, etc all work in
// the recipe strings above.
func runShellInteractive(cmdline string) {
	fmt.Printf("   $ %s\n", cmdline)
	cmd := exec.Command("sh", "-c", cmdline)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "   error: %v\n", err)
	}
}

// integrationsHelpText is shown by `yaver doctor` when something is
// missing — points the user at `yaver install <name>` so they don't
// have to remember the recipe.
func integrationsHelpText(name string) string {
	plan, ok := lookupIntegration(name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("Run `yaver install %s` to install %s.", plan.name, plan.description))
}

func runPiDevNodeInstall(ctx context.Context, progress func(string)) error {
	planNames := []string{"git", "gh", "uv", "docker", "mobile", "tmux", "ffmpeg", "opencode", "tdd", "backend-dev"}
	for _, name := range planNames {
		plan, ok := metaInstallPlan(name)
		if !ok {
			return fmt.Errorf("missing install plan: %s", name)
		}
		if progress != nil {
			progress("==> " + plan.name + " — " + plan.description)
		}
		if checkInstalled(plan.name) == "✓" {
			if progress != nil {
				progress("already installed, skipping")
			}
			continue
		}
		if err := runInstallPlan(ctx, plan, progress); err != nil {
			return fmt.Errorf("%s: %w", plan.name, err)
		}
	}
	if progress != nil {
		progress("Pi dev-node base installed.")
		progress("Optional next steps: `yaver install tailscale` or `yaver install cloudflared`.")
		progress("Recommended hardware: Raspberry Pi 5, 16 GB RAM, 256 GB storage, active cooling, Ethernet.")
	}
	return nil
}

// runVibePreviewInstall installs every external tool the vibe-preview
// pipeline drives. Idempotent — already-installed entries are skipped
// — so this is safe to run on a fresh machine or as a "make sure I'm
// fully equipped" step on an existing one. macOS-only sim-ios capture
// uses xcrun (ships with Xcode); we don't try to install that.
func runVibePreviewInstall(ctx context.Context, progress func(string)) error {
	planNames := []string{"chromium", "ffmpeg", "maestro", "appium", "android-sdk"}
	for _, name := range planNames {
		plan, ok := metaInstallPlan(name)
		if !ok {
			return fmt.Errorf("missing install plan: %s", name)
		}
		if progress != nil {
			progress("==> " + plan.name + " — " + plan.description)
		}
		if checkInstalled(plan.name) == "✓" {
			if progress != nil {
				progress("already installed, skipping")
			}
			continue
		}
		if err := runInstallPlan(ctx, plan, progress); err != nil {
			// Tool installs are best-effort here — appium needs Node,
			// android-sdk install on Linux is the platform-tools subset
			// only — but we keep going so the user gets every available
			// piece even if one step needs manual intervention.
			if progress != nil {
				progress(fmt.Sprintf("warning: %s failed: %v (continuing)", plan.name, err))
			}
		}
	}
	if progress != nil {
		progress("Vibe Preview tool stack installed.")
		progress("Optional next steps:")
		progress("  - macOS users: install Xcode for sim-ios clip recording (xcrun simctl).")
		progress("  - YAVER_VIBE_SUMMARIZER=claude turns on the LLM diff summarizer (needs `claude` CLI).")
		progress("  - Set Think.AutoSummary.Enabled=true on a loop to start receiving summaries.")
	}
	return nil
}

func runAndroidSDKInstall(ctx context.Context, progress func(string)) error {
	// Reached only via an explicit `yaver install android-sdk` /
	// remote-runtime / vibe-preview command — that user action IS the
	// approval.
	return installAndroidSDKRuntime(ctx, true /* explicit yaver install */, progress)
}

// runWebRTCInstall provisions the WebRTC remote-runtime pipeline.
// In practice this means running the same `remote-runtime` plan (adb
// + xcodegen + cliclick where applicable), then printing the doctor
// report so the user sees the post-state immediately. No new
// per-platform packages — the `npm install -g yaver-cli` bootstrap
// already covers what the agent role needs thanks to the in-tree
// H.264 NAL extractor (h264_extract.go).
func runWebRTCInstall(ctx context.Context, progress func(string)) error {
	if err := runRemoteRuntimeInstall(ctx, progress); err != nil {
		return err
	}
	if progress != nil {
		progress("==> Probing WebRTC stack")
	}
	report := buildWebRTCDoctorReport(ctx)
	if progress != nil {
		// Inline a compact summary in the install stream — same as
		// the human-readable doctor printer, just one OK/missing
		// line per check, no headers.
		for _, c := range report.Checks {
			mark := "✓"
			if !c.OK {
				mark = "✗"
			}
			if c.Detail != "" {
				progress(fmt.Sprintf("%s %s — %s", mark, c.Name, c.Detail))
			} else {
				progress(fmt.Sprintf("%s %s", mark, c.Name))
			}
		}
	}
	return nil
}

func runRemoteRuntimeInstall(ctx context.Context, progress func(string)) error {
	// Phase 1 stack: kotlin/flutter via Android emulator + WebRTC capture.
	// Order matters — java first (sdkmanager + gradle dep), then the
	// SDK + system image (heaviest download), then Flutter (independent
	// of Android), then the webrtc-stack apt block (smallest, gives
	// quick feedback that the multi-step plan completed). On macOS
	// Apple's Xcode-shaped tooling layers on top.
	planNames := []string{"java", "android-sdk", "flutter", "webrtc-stack"}
	if runtime.GOOS == "darwin" {
		planNames = append(planNames, "xcodegen", "cliclick")
	}
	for _, name := range planNames {
		plan, ok := metaInstallPlan(name)
		if !ok {
			return fmt.Errorf("missing install plan: %s", name)
		}
		if progress != nil {
			progress("==> " + plan.name + " — " + plan.description)
		}
		if checkInstalled(plan.name) == "✓" {
			if progress != nil {
				progress("already installed, skipping")
			}
			continue
		}
		if err := runInstallPlan(ctx, plan, progress); err != nil {
			return fmt.Errorf("%s: %w", plan.name, err)
		}
	}
	if progress != nil {
		if runtime.GOOS == "darwin" {
			progress("Remote runtime host helpers ready. Xcode itself is still required for iOS Simulator sessions.")
		} else {
			progress("Remote runtime Android host tools ready. iOS Simulator sessions still require a separate macOS runtime node.")
		}
	}
	return nil
}

func runBackendDevInstall(ctx context.Context, progress func(string)) error {
	planNames := []string{"sqlite3", "vercel", "convex", "postgresql-client", "postgresql", "redis-tools", "redis-server", "supabase", "mqtt-broker", "mqtt-clients"}
	for _, name := range planNames {
		plan, ok := metaInstallPlan(name)
		if !ok {
			return fmt.Errorf("missing install plan: %s", name)
		}
		if progress != nil {
			progress("==> " + plan.name + " — " + plan.description)
		}
		if checkInstalled(plan.name) == "✓" {
			if progress != nil {
				progress("already installed, skipping")
			}
			continue
		}
		if err := runInstallPlan(ctx, plan, progress); err != nil {
			return fmt.Errorf("%s: %w", plan.name, err)
		}
	}
	if progress != nil {
		progress("Backend dev stack installed.")
		progress("Supabase uses Docker and a local npx-backed CLI wrapper; initialize a project with `supabase init` and start it with `supabase start`.")
	}
	return nil
}

func runTDDInstall(ctx context.Context, progress func(string)) error {
	planNames := []string{"pre-commit", "pytest", "ruff", "vitest", "eslint", "prettier"}
	for _, name := range planNames {
		plan, ok := metaInstallPlan(name)
		if !ok {
			return fmt.Errorf("missing install plan: %s", name)
		}
		if progress != nil {
			progress("==> " + plan.name + " — " + plan.description)
		}
		if checkInstalled(plan.name) == "✓" {
			if progress != nil {
				progress("already installed, skipping")
			}
			continue
		}
		if err := runInstallPlan(ctx, plan, progress); err != nil {
			return fmt.Errorf("%s: %w", plan.name, err)
		}
	}
	if progress != nil {
		progress("TDD stack installed.")
	}
	return nil
}

func metaInstallPlan(name string) (installPlan, bool) {
	switch name {
	case "git":
		return installPlan{
			name:        "git",
			description: "Git — required for repo sync, worktrees, and nearly every Yaver coding loop",
			macOS:       []string{"brew install git"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y git"},
				{"dnf", "sudo dnf install -y git"},
				{"pacman", "sudo pacman -S --noconfirm git"},
			},
		}, true
	case "gh":
		return installPlan{
			name:        "gh",
			description: "GitHub CLI — useful for repo auth, PR flows, and headless developer setup",
			macOS:       []string{"brew install gh"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y gh"},
				{"dnf", "sudo dnf install -y gh"},
				{"pacman", "sudo pacman -S --noconfirm github-cli"},
			},
		}, true
	case "uv":
		return installPlan{
			name:        "uv",
			description: "uv — fast Python/environment manager for modern dev boxes",
			macOS:       []string{"brew install uv"},
			linux: []linuxStep{
				{"curl", "curl -LsSf https://astral.sh/uv/install.sh | sh"},
			},
		}, true
	case "docker":
		return installPlan{
			name:        "docker",
			description: "Docker engine / Docker Desktop for containerized dev, isolated job runners, and compose workflows",
			macOS:       []string{"brew install --cask docker"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get update && sudo apt-get install -y docker.io docker-compose-v2"},
				{"dnf", "sudo dnf install -y docker docker-compose-plugin"},
				{"pacman", "sudo pacman -S --noconfirm docker docker-compose"},
			},
		}, true
	case "sqlite3":
		return installPlan{
			name:        "sqlite3",
			description: "SQLite CLI and libraries — local-first app storage, migrations, and quick backend prototypes",
			macOS:       []string{"brew install sqlite"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y sqlite3 libsqlite3-dev"},
				{"dnf", "sudo dnf install -y sqlite sqlite-devel"},
				{"pacman", "sudo pacman -S --noconfirm sqlite"},
			},
		}, true
	case "vercel":
		return installPlan{
			name:        "vercel",
			description: "Vercel CLI — deploy web apps and edge backends from the Pi dev node",
			runFunc:     runVercelInstall,
		}, true
	case "convex":
		return installPlan{
			name:        "convex",
			description: "Convex CLI — managed backend workflows and local project setup from the Pi dev node",
			runFunc:     runConvexInstall,
		}, true
	case "postgresql-client":
		return installPlan{
			name:        "postgresql-client",
			description: "PostgreSQL client tools — psql, pg_dump, and migration utilities",
			macOS:       []string{"brew install libpq"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y postgresql-client"},
				{"dnf", "sudo dnf install -y postgresql"},
				{"pacman", "sudo pacman -S --noconfirm postgresql-libs postgresql"},
			},
		}, true
	case "postgresql":
		return installPlan{
			name:        "postgresql",
			description: "PostgreSQL server — local relational backend for promoted phone-born apps",
			macOS:       []string{"brew install postgresql@16"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y postgresql postgresql-contrib"},
				{"dnf", "sudo dnf install -y postgresql-server postgresql-contrib"},
				{"pacman", "sudo pacman -S --noconfirm postgresql"},
			},
		}, true
	case "redis-tools":
		return installPlan{
			name:        "redis-tools",
			description: "Redis CLI tools — inspect and script caches, queues, and pub/sub flows",
			macOS:       []string{"brew install redis"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y redis-tools"},
				{"dnf", "sudo dnf install -y redis"},
				{"pacman", "sudo pacman -S --noconfirm redis"},
			},
		}, true
	case "redis-server":
		return installPlan{
			name:        "redis-server",
			description: "Redis server — local cache, queue, and event bus for app backends",
			macOS:       []string{"brew install redis"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y redis-server"},
				{"dnf", "sudo dnf install -y redis"},
				{"pacman", "sudo pacman -S --noconfirm redis"},
			},
		}, true
	case "supabase":
		return installPlan{
			name:        "supabase",
			description: "Supabase local tooling — npx-backed CLI wrapper for Docker-based local stacks",
			runFunc:     runSupabaseInstall,
		}, true
	case "mqtt-broker":
		return installPlan{
			name:        "mqtt-broker",
			description: "Mosquitto MQTT broker — lightweight pub/sub for devices, automations, and app events",
			macOS:       []string{"brew install mosquitto"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y mosquitto"},
				{"dnf", "sudo dnf install -y mosquitto"},
				{"pacman", "sudo pacman -S --noconfirm mosquitto"},
			},
		}, true
	case "mqtt-clients":
		return installPlan{
			name:        "mqtt-clients",
			description: "Mosquitto MQTT client tools — publish/subscribe shells for local event debugging",
			macOS:       []string{"brew install mosquitto"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y mosquitto-clients"},
				{"dnf", "sudo dnf install -y mosquitto-clients"},
				{"pacman", "sudo pacman -S --noconfirm mosquitto"},
			},
		}, true
	case "mobile":
		return installPlan{
			name:        "mobile",
			description: "Hermes bundle reload stack for React Native / Expo: Node LTS plus the embedded hermesc sanity check. Meta-target.",
			runFunc: func(ctx context.Context, progress func(string)) error {
				if _, err := installNodeRuntime(ctx, progress); err != nil {
					return err
				}
				// embedded prebuilt → system-prewarmed, matching the
				// capability snapshot's resolver so a box the picker
				// flags "prerequisites missing" and a box this install
				// verifies never disagree. On a platform with neither
				// (e.g. a stripped linux/arm64 box with no prewarm) the
				// error is surfaced to the phone so the user sees a real
				// failure instead of a silent half-fix.
				summary, source, err := resolveHermescForCapability()
				if err != nil {
					return err
				}
				if progress != nil {
					label := "Embedded hermesc"
					if source == "system" {
						label = "System hermesc"
					}
					progress(label + " ready: " + summary)
					progress("Hermes reload stack ready for Open in Yaver on Linux, WSL, or macOS.")
				}
				return nil
			},
		}, true
	case "tmux":
		return installPlan{
			name:        "tmux",
			description: "tmux — required for the agent's task runner (probably already installed)",
			macOS:       []string{"brew install tmux"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y tmux"},
				{"dnf", "sudo dnf install -y tmux"},
				{"pacman", "sudo pacman -S --noconfirm tmux"},
			},
		}, true
	case "ffmpeg":
		return installPlan{
			name:        "ffmpeg",
			description: "ffmpeg — required for the morning match-report screen recorder (run `yaver record`) and the vibe-preview clip poster extractor",
			macOS:       []string{"brew install ffmpeg"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y ffmpeg"},
				{"dnf", "sudo dnf install -y ffmpeg"},
				{"pacman", "sudo pacman -S --noconfirm ffmpeg"},
			},
		}, true
	case "chromium":
		return installPlan{
			name:        "chromium",
			description: "Chromium — headless browser used by chromedp for vibe-preview frame capture",
			macOS:       []string{"brew install --cask chromium"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y chromium-browser"},
				{"dnf", "sudo dnf install -y chromium"},
				{"pacman", "sudo pacman -S --noconfirm chromium"},
			},
		}, true
	case "java":
		return installPlan{
			name:        "java",
			description: "OpenJDK 17 JRE — required by Maestro (and useful for Android tooling)",
			macOS:       []string{"brew install openjdk@17"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y default-jre-headless"},
				{"dnf", "sudo dnf install -y java-17-openjdk-headless"},
				{"pacman", "sudo pacman -S --noconfirm jre17-openjdk-headless"},
			},
		}, true
	case "maestro":
		return installPlan{
			name:        "maestro",
			description: "Maestro — declarative mobile E2E flows; drives demo-clip recordings in vibe-preview (Phase 7). Requires Java.",
			macOS: []string{
				// Best-effort Java first; fail-soft so the curl step
				// always runs (the user may already have a different JDK).
				"brew install openjdk@17 2>/dev/null || true",
				"curl -Ls 'https://get.maestro.mobile.dev' | bash",
			},
			linux: []linuxStep{
				{"curl", "sudo apt-get install -y default-jre-headless 2>/dev/null || sudo dnf install -y java-17-openjdk-headless 2>/dev/null || true; curl -Ls 'https://get.maestro.mobile.dev' | bash"},
			},
		}, true
	case "appium":
		return installPlan{
			name:        "appium",
			description: "Appium — WebDriver automation for RN apps; drives the vibe-preview bug-hunter (Phase 15)",
			macOS:       []string{"npm install -g appium"},
			linux: []linuxStep{
				{"npm", "npm install -g appium"},
			},
		}, true
	case "android-sdk":
		return installPlan{
			name:        "android-sdk",
			description: "Managed Android SDK host tools — command-line tools, adb, emulator, and a default AVD for remote runtime / sim-android workflows",
			runFunc:     runAndroidSDKInstall,
		}, true
	case "flutter":
		return installPlan{
			name:        "flutter",
			description: "Flutter SDK (stable) — required to build/run Flutter projects + drive Flutter hot reload from the Yaver remote-runtime emulator stream",
			runFunc:     runFlutterInstall,
		}, true
	case "webrtc-stack":
		return installPlan{
			name:        "webrtc-stack",
			description: "WebRTC capture + encode dependencies for streaming an emulator/Xvfb framebuffer to phone or web: ffmpeg, GStreamer, Xvfb, dbus, qemu user-mode (TCG fallback)",
			macOS: []string{
				// macOS uses HVF for emulator and ScreenCaptureKit for capture; the
				// ffmpeg fallback path still benefits.
				"brew install ffmpeg gstreamer dbus",
			},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y ffmpeg gstreamer1.0-tools gstreamer1.0-plugins-good gstreamer1.0-plugins-bad gstreamer1.0-libav xvfb x11-utils dbus-x11 qemu-system-arm qemu-utils"},
				{"dnf", "sudo dnf install -y ffmpeg gstreamer1 gstreamer1-plugins-good gstreamer1-plugins-bad-free xorg-x11-server-Xvfb dbus-x11 qemu-system-aarch64"},
			},
		}, true
	case "remote-runtime":
		return installPlan{
			name:        "remote-runtime",
			description: "Meta-target: everything needed to host a phone-targeted WebRTC remote runtime — Java 17, Android SDK + ARM64 emulator, Flutter, ffmpeg/GStreamer capture stack. Run once per dev box.",
			runFunc:     runRemoteRuntimeInstall,
		}, true
	case "claude":
		return installPlan{
			name:        "claude",
			description: "Claude Code — Anthropic's coding agent (subscription OAuth via Yaver). Installs @anthropic-ai/claude-code globally.",
			runFunc: func(ctx context.Context, progress func(string)) error {
				return ensureRunnerInstalledStream(ctx, "claude", progress)
			},
		}, true
	case "codex":
		return installPlan{
			name:        "codex",
			description: "OpenAI Codex — code-execution agent (ChatGPT Plus OAuth via Yaver). Installs @openai/codex globally.",
			runFunc: func(ctx context.Context, progress func(string)) error {
				return ensureRunnerInstalledStream(ctx, "codex", progress)
			},
		}, true
	case "opencode":
		return installPlan{
			name:        "opencode",
			description: "opencode — yaver's third first-class runner. BYOK any provider (Anthropic / OpenAI / OpenRouter / Ollama / GLM / ZAI / …) via opencode.json.",
			runFunc: func(ctx context.Context, progress func(string)) error {
				return ensureRunnerInstalledStream(ctx, "opencode", progress)
			},
		}, true
	case "tdd":
		return installPlan{
			name:        "tdd",
			description: "Core TDD / quality stack: pre-commit, pytest, ruff, vitest, eslint, prettier. Meta-target.",
			runFunc:     runTDDInstall,
		}, true
	case "backend-dev":
		return installPlan{
			name:        "backend-dev",
			description: "Local backend stack: sqlite, Vercel, Convex, PostgreSQL, Redis, Supabase, MQTT. Meta-target.",
			runFunc:     runBackendDevInstall,
		}, true
	case "pre-commit":
		return installPlan{
			name:        "pre-commit",
			description: "pre-commit — repo quality gates and fast local hook automation",
			macOS:       []string{"python3 -m pip install --user --upgrade pre-commit"},
			linux: []linuxStep{
				{"pip3", "pip3 install --user --upgrade pre-commit"},
				{"pipx", "pipx install pre-commit"},
			},
		}, true
	case "pytest":
		return installPlan{
			name:        "pytest",
			description: "pytest — default Python unit test runner for TDD loops",
			macOS:       []string{"python3 -m pip install --user --upgrade pytest"},
			linux: []linuxStep{
				{"pip3", "pip3 install --user --upgrade pytest"},
				{"pipx", "pipx install pytest"},
			},
		}, true
	case "ruff":
		return installPlan{
			name:        "ruff",
			description: "ruff — fast Python lint + format tool for low-cost repair loops",
			macOS:       []string{"python3 -m pip install --user --upgrade ruff"},
			linux: []linuxStep{
				{"pip3", "pip3 install --user --upgrade ruff"},
				{"pipx", "pipx install ruff"},
			},
		}, true
	case "vitest":
		return installPlan{
			name:        "vitest",
			description: "Vitest — default TypeScript/JavaScript unit test runner for headless app TDD",
			macOS:       []string{"npm install -g vitest"},
			linux: []linuxStep{
				{"npm", "npm install -g vitest"},
			},
		}, true
	case "eslint":
		return installPlan{
			name:        "eslint",
			description: "ESLint — JavaScript and TypeScript linting support for auto-fix loops",
			macOS:       []string{"npm install -g eslint"},
			linux: []linuxStep{
				{"npm", "npm install -g eslint"},
			},
		}, true
	case "prettier":
		return installPlan{
			name:        "prettier",
			description: "Prettier — formatter for JavaScript/TypeScript/web repos",
			macOS:       []string{"npm install -g prettier"},
			linux: []linuxStep{
				{"npm", "npm install -g prettier"},
			},
		}, true
	default:
		return installPlan{}, false
	}
}

// installNodeGlobalPackageStream is the progress-streaming twin of
// installNodeGlobalPackage in runner_auth_setup.go. The
// terminal-driven CLI path (`yaver runner-auth setup`) is happy
// collecting CombinedOutput() in one chunk, but the web Devices view
// and mobile CodingAgentsSection install buttons drive
// /install/<runner> and need line-by-line progress through the
// install:<tool> log stream so the user sees something happen
// during the ~30 s npm install. Node-runtime auto-provision +
// sysctl tune + PATH augmentation match the non-streaming variant
// 1:1 so a fresh box (Pi, ARM cloud, mac without brew) Just Works.
func installNodeGlobalPackageStream(ctx context.Context, pkg string, progress func(string)) error {
	if runtime.GOOS == "linux" {
		ensureLinuxRunnerSandboxSupport()
	}
	nodeBin, err := installNodeRuntime(ctx, progress)
	if err != nil {
		return err
	}
	npmPath := filepath.Join(nodeBin, "npm")
	if runtime.GOOS == "windows" {
		npmPath += ".cmd"
	}
	if progress != nil {
		progress(fmt.Sprintf("$ %s install -g %s", npmPath, pkg))
	}
	cmd := exec.CommandContext(ctx, npmPath, "install", "-g", pkg)
	cmd.Env = append(os.Environ(), "PATH="+nodeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for s.Scan() {
			if progress != nil {
				progress(s.Text())
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("npm install -g %s: %v", pkg, err)
	}
	augmentAgentPATH()
	return nil
}

// ensureRunnerInstalledStream mirrors ensureRunnerInstalled but
// streams progress so the /install/<runner> SSE log carries live
// output. Pre-check via resolveRunnerBinary avoids a no-op npm
// reinstall on boxes where the runner is already present.
func ensureRunnerInstalledStream(ctx context.Context, runner string, progress func(string)) error {
	cmd := GetRunnerConfig(runner).Command
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("unsupported runner %q", runner)
	}
	if resolveRunnerBinary(cmd) != "" {
		if progress != nil {
			progress(runner + " already installed, skipping")
		}
		return nil
	}
	var pkg string
	switch normalizeRunnerAuthName(runner) {
	case "claude":
		pkg = "@anthropic-ai/claude-code"
	case "codex":
		pkg = "@openai/codex"
	case "opencode":
		pkg = "opencode-ai"
	default:
		return fmt.Errorf("runner %q does not have an auto-install recipe yet", runner)
	}
	return installNodeGlobalPackageStream(ctx, pkg, progress)
}

func runVercelInstall(ctx context.Context, progress func(string)) error {
	return installNodeBackedCLI(ctx, "vercel", "vercel", progress)
}

func runConvexInstall(ctx context.Context, progress func(string)) error {
	return installNodeBackedCLI(ctx, "convex", "convex", progress)
}

func runSupabaseInstall(ctx context.Context, progress func(string)) error {
	return installNodeBackedCLI(ctx, "supabase", "supabase", progress)
}

func installNodeBackedCLI(ctx context.Context, scriptName, pkgName string, progress func(string)) error {
	nodeBin, err := installNodeRuntime(ctx, progress)
	if err != nil {
		return err
	}
	if progress != nil {
		progress("Using Node runtime: " + nodeBin)
	}
	targetDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targetDir = filepath.Join(targetDir, ".local", "bin")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	targetPath := filepath.Join(targetDir, scriptName)
	script := fmt.Sprintf("#!/usr/bin/env sh\nset -eu\nPATH=\"%s:$PATH\"\nexec npx -y %s \"$@\"\n", filepath.Dir(nodeBin), pkgName)
	if err := os.WriteFile(targetPath, []byte(script), 0o755); err != nil {
		return err
	}
	if progress != nil {
		progress("Installed wrapper: " + targetPath)
	}
	return nil
}
