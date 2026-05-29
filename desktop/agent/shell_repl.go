package main

// shell_repl.go — `yaver` with no args (on a TTY) opens an interactive
// psql-style shell. Type any yaver command and it runs; search the command
// catalog; quit with \q. It's a thin wrapper: each line is dispatched by
// re-executing the yaver binary with those args, so EVERY command works
// identically to the non-interactive CLI — auth/signout(deauth), ping,
// status, devices, voice test/listen, relay, vault, etc. — with zero
// per-command wiring and no risk of a handler's os.Exit killing the loop.
//
// Meta commands (psql-flavored):
//   \?  \h  help        list the command catalog
//   \s <term>           search commands by name/description
//   \q  exit  quit      leave the shell (deauth via `signout`)
//   \! <cmd>            run a raw shell command
//   clear  \c           clear the screen

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"golang.org/x/term"
)

// maybeRunYaverShell launches the interactive shell when `yaver` is run
// with no args on an interactive terminal. Returns true if it handled the
// session (caller should exit), false to fall through to printUsage (e.g.
// piped/non-TTY, or YAVER_NO_SHELL set to avoid sub-command recursion).
func maybeRunYaverShell() bool {
	if os.Getenv("YAVER_NO_SHELL") != "" {
		return false
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	runYaverShell()
	return true
}

// replCommand is one entry in the searchable catalog.
type replCommand struct {
	name string
	desc string
}

// replCatalog is the searchable command list shown by help / \s. Kept in
// sync with the top-level dispatch in main.go. Adding a command here only
// affects discovery + search — execution always re-runs the real binary,
// so an omission just means "not listed", never "can't run".
var replCatalog = []replCommand{
	{"auth", "Sign in and start the agent"},
	{"auth status", "Show who you are signed in as"},
	{"signout", "Sign out and clear credentials (deauth)"},
	{"connect", "Connect to your dev machine"},
	{"ping", "Ping a device (direct or via relay)"},
	{"status", "Show agent + session status"},
	{"devices", "List your registered devices"},
	{"doctor", "Audit what's configured vs missing"},
	{"diagnose", "Deep diagnostics"},
	{"version", "Print agent version"},
	{"serve", "Start the agent manually"},
	{"restart", "Restart the agent"},
	{"stop", "Stop the running agent"},
	{"logs", "Show agent logs"},
	{"attach", "Interactive terminal — tasks + prompts"},
	{"code", "Terminal-first coding UX"},
	{"voice status", "Show voice readiness + provider state"},
	{"voice test", "Flux-style live transcription test (speak → text)"},
	{"voice listen", "Live mic transcription → stdout"},
	{"voice deps", "Check/install local voice deps (ffmpeg, whisper, model)"},
	{"voice deps --install", "Install whatever local voice deps are missing"},
	{"voice setup", "Set up a voice provider"},
	{"relay add", "Add a relay server"},
	{"relay list", "List configured relay servers"},
	{"relay test", "Test relay connectivity"},
	{"tunnel", "Manage Cloudflare tunnels"},
	{"mcp", "MCP server / setup"},
	{"tmux", "List/adopt tmux sessions"},
	{"push", "Push-to-device (RN/Expo)"},
	{"deploy", "Deploy backend/frontend/mobile"},
	{"cloud", "Yaver managed cloud (buy/create/status/ssh)"},
	{"install", "Provision tool stacks"},
	{"2fa", "Two-factor auth setup/challenge"},
	{"set-runner", "Set the default AI runner"},
	{"primary", "Primary device controls"},
	{"guests", "Guest session controls"},
}

// runYaverShell is the interactive loop. Returns when the user quits.
func runYaverShell() {
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "yaver"
	}

	out := os.Stdout
	fmt.Fprintln(out, "yaver interactive shell — type a command, `\\?` for help, `\\q` to quit.")
	fmt.Fprintln(out, "(every line runs the real `yaver <cmd>`: auth, signout, ping, status, devices, voice test, …)")
	fmt.Fprintln(out)

	reader := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(out, "yaver> ")
		if !reader.Scan() {
			fmt.Fprintln(out) // newline after EOF (Ctrl-D)
			return
		}
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}

		switch {
		case line == "\\q" || line == "exit" || line == "quit" || line == "\\quit":
			return
		case line == "\\?" || line == "\\h" || line == "help" || line == "?":
			printReplHelp(out)
			continue
		case line == "clear" || line == "\\c":
			fmt.Fprint(out, "\033[2J\033[H")
			continue
		case strings.HasPrefix(line, "\\s"):
			searchReplCommands(out, strings.TrimSpace(strings.TrimPrefix(line, "\\s")))
			continue
		case strings.HasPrefix(line, "\\!"):
			raw := strings.TrimSpace(strings.TrimPrefix(line, "\\!"))
			if raw != "" {
				runRawShellLine(raw)
			}
			continue
		}

		// Anything else: run as a yaver command. Strip an optional leading
		// "yaver" so both `status` and `yaver status` work.
		args := strings.Fields(line)
		if len(args) > 0 && args[0] == "yaver" {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		execYaverCommand(self, args)
	}
}

// execYaverCommand re-runs the yaver binary with the given args, wiring
// stdio straight through so interactive sub-commands (auth browser flow,
// voice test mic loop, attach) behave exactly as they do standalone.
func execYaverCommand(self string, args []string) {
	cmd := exec.Command(self, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Mark the child so it never recurses into the shell.
	cmd.Env = append(os.Environ(), "YAVER_NO_SHELL=1")
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "(exit %d)\n", ee.ExitCode())
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}

func runRawShellLine(raw string) {
	cmd := exec.Command("/bin/sh", "-c", raw)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func printReplHelp(out *os.File) {
	fmt.Fprintln(out, "Commands (type any of these, or `yaver <cmd>`):")
	rows := append([]replCommand(nil), replCatalog...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, c := range rows {
		fmt.Fprintf(out, "  %-22s %s\n", c.name, c.desc)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Meta:")
	fmt.Fprintln(out, "  \\s <term>              search commands")
	fmt.Fprintln(out, "  \\! <cmd>               run a raw shell command")
	fmt.Fprintln(out, "  clear  \\c              clear the screen")
	fmt.Fprintln(out, "  \\?  help               this help")
	fmt.Fprintln(out, "  \\q  exit  quit         leave the shell")
}

func searchReplCommands(out *os.File, termStr string) {
	if termStr == "" {
		printReplHelp(out)
		return
	}
	t := strings.ToLower(termStr)
	var hits []replCommand
	for _, c := range replCatalog {
		if strings.Contains(strings.ToLower(c.name), t) || strings.Contains(strings.ToLower(c.desc), t) {
			hits = append(hits, c)
		}
	}
	if len(hits) == 0 {
		fmt.Fprintf(out, "no commands match %q\n", termStr)
		return
	}
	for _, c := range hits {
		fmt.Fprintf(out, "  %-22s %s\n", c.name, c.desc)
	}
}
