package main

// monorepo_start_auth.go — interactive auth flow for `yaver monorepo
// start` step 3. The matrix in monorepo_start_runners.go shows
// availability; this file walks the user through actually
// authenticating the runner they picked, on the host they picked,
// without leaving the wizard.
//
// The user's mental model is: "I'm developing here on my Mac, I
// want a remote box's claude/codex/opencode to do the work on this
// repo." For that, the remote runner needs valid credentials. The
// auth cases we handle:
//
//   1. Already authed + ready          → silent pass, return true
//   2. Installed, not signed in        → menu: paste credential /
//                                         "open browser myself" /
//                                         skip. Paste path uses
//                                         term.ReadPassword (silent),
//                                         calls applyRunnerAuthSetup
//                                         {Local,Remote}, re-probes.
//   3. Not installed                   → applyRunnerAuthSetup with
//                                         InstallIfMissing=true (which
//                                         install + auth in one shot
//                                         locally; remote runs the
//                                         same flow over peer-proxy).
//   4. Remote unreachable              → print SSH / `yaver serve`
//                                         hint, return user's choice
//                                         to continue or abort.
//   5. User wants host's API keys      → explain it's a host-side
//                                         flag (UseHostAPIKeys on the
//                                         device row), ask user to
//                                         set it from the host's
//                                         account, return.
//   6. Skip                            → return true (continue
//                                         without auth). Wizard still
//                                         creates the project; the
//                                         user signs in before vibing.
//
// Per CLAUDE.md feedback_runners_always_dangerous.md, supported
// runners are claude / codex / opencode; opencode wraps the long
// tail of providers via its own BYOK config so users who want a
// specific model still reach it through opencode, not a separate
// runner id.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// runMonorepoAuthInteractive is the entry point called from the
// wizard after the user picks (location, runner). Returns true to
// continue the wizard, false to abort. Never panics — user-visible
// errors degrade to a printed message + the skip path.
//
// The interactive flow is the only time during `yaver monorepo
// start` that we touch the runner-auth subsystem: the rest of the
// wizard is offline, fast, and idempotent. Auth is opt-in per
// invocation — the user can always answer "skip" and wire it later.
func runMonorepoAuthInteractive(r *bufio.Reader, loc *runnerLocation, runner string) bool {
	if loc == nil {
		return true
	}
	row := findRunnerRow(loc, runner)

	// Case 1 — already authed + ready. Silent pass.
	if row != nil && row.Installed && row.AuthConfigured && row.Ready {
		return true
	}

	// Case 4 — remote unreachable. We can't recover from inside the
	// wizard; user has to make `yaver serve` reachable on the remote
	// first. Print a one-liner with the next-step command and ask
	// whether to continue or abort.
	if row != nil && strings.EqualFold(strings.TrimSpace(row.Detail), "unreachable") {
		fmt.Printf("  ! %s on %s is unreachable. SSH in and run `yaver serve`, then come back.\n",
			runner, loc.Label)
		if loc.ID != "this" {
			fmt.Printf("    Or:  yaver ssh %s -- yaver serve --install-systemd\n", loc.ID)
		}
		return promptChoice(r, "Continue without authing", []string{"yes", "no"}, "yes") == "yes"
	}

	// Case 3 / 2 collapse — needs credentials, with or without an
	// install on the way. The credential menu is the same; the
	// applyRunnerAuthSetup* call below threads InstallIfMissing
	// through so the runner gets installed first if needed.
	state := authPromptState(row)
	fmt.Printf("\n  ! %s on %s — %s.\n", runner, loc.Label, state)
	if row == nil || !row.Installed {
		fmt.Println("    Yaver can install + authenticate it in one step.")
	}

	choice := promptAuthAction(r, runner, loc)
	switch choice {
	case authChoiceSkip:
		fmt.Printf("    Skipping — when you're ready: `yaver runner-auth setup %s%s`\n",
			runner, remoteTargetSuffix(loc))
		return true
	case authChoiceBrowserSelf:
		// User will run the runner's own login on the remote (or
		// locally) themselves. Print the canonical commands and
		// continue.
		fmt.Println()
		printSelfBrowserHints(runner, loc)
		fmt.Println("    Re-run `yaver monorepo start` after you've signed in.")
		return promptChoice(r, "Continue with the wizard now", []string{"yes", "no"}, "yes") == "yes"
	case authChoiceHostKeys:
		// Only meaningful when the chosen remote is a guest of the
		// caller's account — otherwise this flag isn't theirs to set.
		fmt.Println("    Host-API-key sharing is enabled per-device by the device's host.")
		fmt.Printf("    From the host account: yaver guests config <email> useHostKeys=true (target=%s)\n", loc.ID)
		fmt.Println("    The remote runner will then use host's vault keys instead of its own.")
		return promptChoice(r, "Continue without authing", []string{"yes", "no"}, "yes") == "yes"
	case authChoicePaste:
		// fall through — the most common path
	default:
		return true
	}

	req, ok := readRunnerCredentialRequest(r, runner)
	if !ok {
		fmt.Println("    No credential entered — skipping auth. You can run `yaver runner-auth setup` later.")
		return true
	}

	fmt.Printf("    Authing %s on %s…\n", runner, loc.Label)
	result, err := applyRunnerAuthSetupForLocation(loc, req)
	if err != nil {
		fmt.Printf("    ! Auth attempt failed: %v\n", err)
		fmt.Printf("    Try `yaver runner-auth setup %s%s` directly, or pick a different runner.\n",
			runner, remoteTargetSuffix(loc))
		return promptChoice(r, "Continue without authing", []string{"yes", "no"}, "yes") == "yes"
	}

	// Success. Update the row in-place so any subsequent matrix
	// re-print reflects the now-authed state, and surface the
	// outcome to the user.
	if loc != nil {
		for i := range loc.Rows {
			if loc.Rows[i].ID == runner {
				loc.Rows[i].Installed = result.Installed
				loc.Rows[i].Ready = result.Ready
				loc.Rows[i].AuthConfigured = result.AuthConfigured
				loc.Rows[i].AuthSource = result.AuthSource
				loc.Rows[i].Warning = result.Warning
				loc.Rows[i].Detail = result.Detail
			}
		}
	}
	if result.Ready && result.AuthConfigured {
		fmt.Printf("    ✓ %s on %s authenticated via %s.\n", runner, loc.Label,
			authSourceOrDefault(result.AuthSource, "vault"))
	} else {
		fmt.Printf("    ! %s on %s reports installed=%v auth=%v ready=%v — continuing anyway.\n",
			runner, loc.Label, result.Installed, result.AuthConfigured, result.Ready)
	}
	return true
}

type authChoice string

const (
	authChoicePaste       authChoice = "paste"
	authChoiceBrowserSelf authChoice = "browser"
	authChoiceHostKeys    authChoice = "host"
	authChoiceSkip        authChoice = "skip"
)

// promptAuthAction shows the per-runner auth menu. Returns the chosen
// authChoice (or authChoiceSkip on any unexpected input).
//
// We deliberately don't try to spawn the runner's OWN browser flow
// inline — that's a fragile thing to do from a stdin-driven wizard
// (terminal-mode collisions, the runner process owns stdin) and it
// would lock the user out of the wizard for the duration. Instead
// "browser" is a guidance path: print the exact command, return.
func promptAuthAction(r *bufio.Reader, runner string, loc *runnerLocation) authChoice {
	fmt.Println()
	fmt.Println("    How would you like to authenticate?")
	fmt.Println("      1. Paste an API key / token  (default — Yaver stores it in the vault)")
	fmt.Println("      2. Open the runner's browser sign-in myself, come back later")
	if loc != nil && loc.ID != "this" {
		fmt.Println("      3. Use host's API keys      (only if this remote is a guest of your account)")
	}
	fmt.Println("      4. Skip — I'll authenticate before vibing")

	choices := []string{"1", "2", "4"}
	if loc != nil && loc.ID != "this" {
		choices = []string{"1", "2", "3", "4"}
	}
	pick := promptChoice(r, "Choice", choices, "1")
	switch pick {
	case "1":
		return authChoicePaste
	case "2":
		return authChoiceBrowserSelf
	case "3":
		return authChoiceHostKeys
	case "4":
		return authChoiceSkip
	}
	return authChoiceSkip
}

// readRunnerCredentialRequest returns setup guidance for a missing runner.
// Claude/Codex are subscription-OAuth only in Yaver; OpenCode/GLM provider
// credentials are configured through their dedicated provider flows.
//
// Always silenced via term.ReadPassword so the secret doesn't
// echo and doesn't end up in the user's shell scrollback. Returns
// (req, false) when stdin doesn't yield a non-empty paste — caller
// treats that as "skip auth this round".
func readRunnerCredentialRequest(r *bufio.Reader, runner string) (runnerAuthSetupRequest, bool) {
	runner = strings.ToLower(strings.TrimSpace(runner))
	req := runnerAuthSetupRequest{Runner: runner}

	switch runner {
	case "claude":
		fmt.Println("Claude Code requires Claude plan OAuth — never API keys.")
		fmt.Println("Open Yaver mobile → Runner Auth → Claude Code, or run:")
		fmt.Println("    yaver runner-auth browser-start claude")
		fmt.Println("Then re-run `yaver monorepo start` once auth is in place.")
		return req, false
	case "codex":
		// API-key prompt removed 2026-05-27 per
		// feedback_no_api_keys_subscription_only. Direct the user to
		// ChatGPT Plus subscription OAuth via Yaver mobile.
		fmt.Println("Codex requires ChatGPT Plus subscription OAuth — never API keys.")
		fmt.Println("Open Yaver mobile → Runner Auth → Codex, or run:")
		fmt.Println("    yaver runner-auth browser-start codex")
		fmt.Println("Then re-run `yaver monorepo start` once auth is in place.")
		return req, false
	case "opencode":
		// OpenCode wraps Claude / Codex auth; their credentials cover it.
		fmt.Println("OpenCode reuses Claude / Codex subscription OAuth.")
		fmt.Println("Authorize Claude Code or Codex first via Yaver mobile, then re-run `yaver monorepo start`.")
		return req, false
	default:
		return req, false
	}
}

// readSecret prints a label and reads a line from stdin without
// echoing. Falls back to plain bufio read when stdin isn't a TTY
// (so test scripts and pipes still work). Empty input returns
// ("", false).
func readSecret(label string) (string, bool) {
	fmt.Printf("    > %s: ", label)

	// term.ReadPassword needs an actual TTY. For piped input (test
	// scripts) it returns an error — fall back to reading the line
	// in cleartext so the wizard still works in CI.
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		raw, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", false
		}
		s := strings.TrimSpace(string(raw))
		return s, s != ""
	}

	// Non-TTY fallback — used by smoke tests + CI.
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	return line, true
}

// applyRunnerAuthSetupForLocation routes between the local and
// remote setup paths based on the chosen location ID.
func applyRunnerAuthSetupForLocation(loc *runnerLocation, req runnerAuthSetupRequest) (runnerAuthSetupResult, error) {
	if loc == nil || loc.ID == "this" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return applyRunnerAuthSetupLocal(ctx, req)
	}
	return applyRunnerAuthSetupRemote(loc.ID, req)
}

// findRunnerRow returns the row for `runner` on `loc`, or nil if
// the location's matrix doesn't include it (shouldn't happen — every
// location has rows for every supported runner via filter+pad).
func findRunnerRow(loc *runnerLocation, runner string) *runnerAuthStatusRow {
	if loc == nil {
		return nil
	}
	for i := range loc.Rows {
		if loc.Rows[i].ID == runner {
			return &loc.Rows[i]
		}
	}
	return nil
}

// authPromptState renders a one-line human description of the
// runner's current state for the auth-prompt header.
func authPromptState(row *runnerAuthStatusRow) string {
	if row == nil {
		return "not detected"
	}
	if !row.Installed {
		return "not installed"
	}
	if row.AuthConfigured && row.Ready {
		return "already authenticated"
	}
	if row.AuthConfigured && !row.Ready {
		return "credentials present but runner reports not-ready"
	}
	return "installed but not signed in"
}

// printSelfBrowserHints prints the canonical command the user can
// run themselves to interactively sign in to a runner. We don't
// shell into these from the wizard — they own stdin.
func printSelfBrowserHints(runner string, loc *runnerLocation) {
	suffix := ""
	if loc != nil && loc.ID != "this" {
		suffix = fmt.Sprintf("yaver ssh %s -- ", loc.ID)
	}
	switch strings.ToLower(strings.TrimSpace(runner)) {
	case "claude":
		fmt.Printf("    Sign in:  %sclaude /login\n", suffix)
	case "codex":
		fmt.Printf("    Sign in:  %scodex login\n", suffix)
	case "opencode":
		fmt.Printf("    Sign in:  %sopencode auth login\n", suffix)
	default:
		fmt.Printf("    Sign in:  %s%s --login (consult the runner's docs)\n", suffix, runner)
	}
}

// remoteTargetSuffix is the bit appended to `yaver runner-auth setup`
// hints so the user can copy-paste them. Empty for the local
// machine; ` --target <id>` for remote.
func remoteTargetSuffix(loc *runnerLocation) string {
	if loc == nil || loc.ID == "this" {
		return ""
	}
	return " --target " + loc.ID
}

// authSourceOrDefault returns s if non-empty, otherwise dflt.
// Used to print "authenticated via vault" when the setup result
// doesn't surface a more specific source (e.g. a file path).
func authSourceOrDefault(s, dflt string) string {
	if strings.TrimSpace(s) == "" {
		return dflt
	}
	return s
}

// silenceCompilerWarnings — the syscall import is required on
// non-darwin platforms when we conditionally use it via int(os.Stdin.Fd()).
// Right now we only call term.IsTerminal, which doesn't need
// syscall, but referencing the package keeps gopls happy when
// future code adds platform-specific paths.
var _ = syscall.Stdin
