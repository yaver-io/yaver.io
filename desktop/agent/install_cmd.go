package main

// `yaver install <tool>` — one-shot dependency installer for the
// integrations the local-CI runner needs. The point is "make
// yaver-test-sdk Just Work after `brew install yaver`" without forcing
// the user to learn the package layouts of every browser, every mobile
// SDK, and every test framework.
//
// Strict rules:
//
//   - We never download or compile binaries ourselves. Every install
//     shells out to the user's existing package manager (brew on
//     macOS, apt/dnf on Linux). The user can verify what we're doing
//     because the actual command is printed before it runs.
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
		description: "Appium — only needed if you want to drive existing Appium specs (yaver-test-sdk has its own bridges from M5)",
		macOS:       []string{"npm install -g appium"},
		linux: []linuxStep{
			{"npm", "npm install -g appium"},
		},
	},
	{
		name:        "node",
		description: "Node.js LTS — installs to ~/.yaver/runtimes/node, sudo-free, modern enough for Expo SDK 50+ (apt nodejs on Ubuntu 22.04 ships Node 12 which Expo rejects)",
		runFunc: func(ctx context.Context, progress func(string)) error {
			_, err := installNodeRuntime(ctx, progress)
			return err
		},
	},
	{
		name:        "mobile",
		description: "React Native / Expo dev stack on a fresh box: Node LTS into ~/.yaver/runtimes/node. Sudo-free. Meta-target.",
		runFunc: func(ctx context.Context, progress func(string)) error {
			if _, err := installNodeRuntime(ctx, progress); err != nil {
				return err
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
	// Special-case the agent-managed runtimes: they live under
	// ~/.yaver/runtimes/<tool>/bin and are not on the system PATH for
	// CLI users, so a plain LookPath would always say "—".
	switch name {
	case "node", "mobile":
		if v := nodeRuntimeExisting(runtimeNodeBinDir()); v != "" {
			return "✓"
		}
		if name == "node" {
			if _, err := exec.LookPath("node"); err == nil {
				return "✓"
			}
		}
		return "—"
	}

	probe := map[string][]string{
		"chrome":      {"google-chrome", "google-chrome-stable", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		"chromium":    {"chromium", "chromium-browser", "/Applications/Chromium.app/Contents/MacOS/Chromium"},
		"firefox":     {"firefox", "/Applications/Firefox.app/Contents/MacOS/firefox"},
		"android-sdk": {"adb"},
		"appium":      {"appium"},
		"ollama":      {"ollama"},
		"aider":       {"aider"},
		"opencode":    {"opencode"},
		"hybrid":      {"aider"}, // presence of aider is our cheapest proxy
		"tmux":        {"tmux"},
		"ffmpeg":      {"ffmpeg"},
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
