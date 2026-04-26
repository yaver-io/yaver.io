package main

// `yaver install <tool>` — one-shot dependency installer for the
// integrations the local-CI runner needs. The point is "make
// yaver-test-sdk Just Work after `brew install yaver`" without forcing
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
		description: "Google Chrome — required for `yaver test run` web target",
		macOS:       []string{"brew install --cask google-chrome"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get update && sudo apt-get install -y google-chrome-stable"},
			{"dnf", "sudo dnf install -y google-chrome-stable"},
			{"pacman", "sudo pacman -S --noconfirm google-chrome"},
		},
	},
	{
		name:        "chromium",
		description: "Chromium — open-source alternative to Chrome",
		macOS:       []string{"brew install --cask chromium"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y chromium-browser"},
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
		macOS:       []string{"brew install --cask android-platform-tools", "brew install --cask android-commandlinetools"},
		linux: []linuxStep{
			{"apt-get", "sudo apt-get install -y android-tools-adb"},
		},
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
		name:        "ollama",
		description: "Ollama — local LLM provider for $0 visual inspection (alternative to Mistral/OpenAI/Anthropic)",
		macOS:       []string{"brew install ollama"},
		linux: []linuxStep{
			{"curl", "curl -fsSL https://ollama.com/install.sh | sh"},
		},
	},
	{
		name:        "aider",
		description: "Aider — file-editing AI CLI; pairs with Ollama/Qwen for the hybrid mode implementer",
		macOS:       []string{"python3 -m pip install --user --upgrade aider-chat"},
		linux: []linuxStep{
			{"pip3", "pip3 install --user --upgrade aider-chat"},
			{"pipx", "pipx install aider-chat"},
		},
	},
	{
		name:        "opencode",
		description: "OpenCode — alternative terminal AI coding agent; usable as a hybrid planner or implementer",
		macOS:       []string{"brew install opencode"},
		linux: []linuxStep{
			{"npm", "npm install -g opencode-ai"},
			{"curl", "curl -fsSL https://opencode.ai/install | bash"},
		},
	},
	{
		name:        "hybrid",
		description: "Everything needed for `yaver hybrid` (aider + ollama + qwen2.5-coder:14b pulled). Meta-target.",
		macOS: []string{
			"brew install ollama",
			"python3 -m pip install --user --upgrade aider-chat",
			// Model pull is heavy (~9 GB); run it last so an early
			// failure doesn't leave a half-downloaded blob behind.
			"ollama serve >/dev/null 2>&1 & sleep 2; ollama pull qwen2.5-coder:14b",
		},
		linux: []linuxStep{
			{"curl", "curl -fsSL https://ollama.com/install.sh | sh && pip3 install --user --upgrade aider-chat && (ollama serve >/dev/null 2>&1 & sleep 2; ollama pull qwen2.5-coder:14b)"},
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
			if _, err := embeddedHermescSummary(); err == nil {
				return "✓"
			}
		}
		return "—"
	case "vercel", "convex", "supabase":
		if home, err := os.UserHomeDir(); err == nil {
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", name)); err == nil {
				return "✓"
			}
		}
		return "—"
	}

	probe := map[string][]string{
		"git":               {"git"},
		"gh":                {"gh"},
		"uv":                {"uv"},
		"docker":            {"docker"},
		"tailscale":         {"tailscale"},
		"cloudflared":       {"cloudflared"},
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
		"android-sdk":       {"adb"},
		"appium":            {"appium"},
		"maestro":           {"maestro"},
		"ollama":            {"ollama"},
		"aider":             {"aider"},
		"opencode":          {"opencode"},
		"hybrid":            {"aider"}, // presence of aider is our cheapest proxy
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
		"tdd":          {"pre-commit", "pytest", "ruff", "vitest", "eslint", "prettier"},
		"backend-dev":  {"sqlite3", "vercel", "convex", "postgresql-client", "postgresql", "redis-tools", "redis-server", "supabase", "mqtt-broker", "mqtt-clients"},
		"pi-dev-node":  {"git", "gh", "uv", "docker", "mobile", "tmux", "ffmpeg", "ollama", "aider", "opencode", "tdd", "backend-dev"},
		"vibe-preview": {"chromium", "ffmpeg", "maestro", "appium", "android-sdk"},
	}
	targets, ok := required[name]
	if !ok {
		return false
	}
	for _, target := range targets {
		if checkInstalled(target) != "✓" {
			return false
		}
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
	planNames := []string{"git", "gh", "uv", "docker", "mobile", "tmux", "ffmpeg", "ollama", "aider", "opencode", "tdd", "backend-dev"}
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
		progress("Optional next steps: `yaver install tailscale`, `yaver install cloudflared`, or `yaver install hybrid` if you want qwen2.5-coder:14b pulled immediately.")
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
	case "maestro":
		return installPlan{
			name:        "maestro",
			description: "Maestro — declarative mobile E2E flows; drives demo-clip recordings in vibe-preview (Phase 7)",
			macOS:       []string{"curl -Ls 'https://get.maestro.mobile.dev' | bash"},
			linux: []linuxStep{
				{"curl", "curl -Ls 'https://get.maestro.mobile.dev' | bash"},
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
			description: "Android SDK platform-tools (adb) — needed for `target: android-emu` and vibe-preview sim-android clip recording",
			macOS:       []string{"brew install --cask android-platform-tools"},
			linux: []linuxStep{
				{"apt-get", "sudo apt-get install -y android-tools-adb"},
				{"dnf", "sudo dnf install -y android-tools"},
				{"pacman", "sudo pacman -S --noconfirm android-tools"},
			},
		}, true
	case "ollama":
		return installPlan{
			name:        "ollama",
			description: "Ollama — local LLM provider for $0 visual inspection (alternative to Mistral/OpenAI/Anthropic)",
			macOS:       []string{"brew install ollama"},
			linux: []linuxStep{
				{"curl", "curl -fsSL https://ollama.com/install.sh | sh"},
			},
		}, true
	case "aider":
		return installPlan{
			name:        "aider",
			description: "Aider — file-editing AI CLI; pairs with Ollama/Qwen for the hybrid mode implementer",
			macOS:       []string{"python3 -m pip install --user --upgrade aider-chat"},
			linux: []linuxStep{
				{"pip3", "pip3 install --user --upgrade aider-chat"},
				{"pipx", "pipx install aider-chat"},
			},
		}, true
	case "opencode":
		return installPlan{
			name:        "opencode",
			description: "OpenCode — alternative terminal AI coding agent; usable as a hybrid planner or implementer",
			macOS:       []string{"brew install opencode"},
			linux: []linuxStep{
				{"npm", "npm install -g opencode-ai"},
				{"curl", "curl -fsSL https://opencode.ai/install | bash"},
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
