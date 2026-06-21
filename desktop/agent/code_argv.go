package main

// code_argv.go — bare-word + `yaver <argv>` dispatcher for the
// `yaver code` interactive prompt.
//
// What it does:
//   - First token is checked against the allowlist of safe-in-TUI
//     yaver subcommands.
//   - Optional leading "yaver" is stripped, so both
//     `guests list` and `yaver guests list` work.
//   - Matched lines run as a subprocess of yaver itself, output
//     captured into the TUI scrollback. A coding prompt is anything
//     that doesn't match.
//
// Why an allowlist not a denylist:
//   - Some yaver subcommands take over stdin / spawn daemons / would
//     recursively re-enter `yaver code`. Easier to enumerate the safe
//     read-mostly + quick-action set than to chase every long-running
//     command.
//   - The list is also what we surface in the slash menu so the user
//     can discover what bare-word verbs are available — keeping the
//     two lists in sync matters more than completeness.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// safeArgvSubcommands is the set of yaver subcommands that are safe
// to invoke from inside `yaver code`'s interactive prompt. Each one:
//   - returns within ~30 s under normal use
//   - does not require interactive stdin
//   - does not spawn a long-running foreground daemon
//   - does not recursively re-enter the TUI
//
// Add new entries when a new short-running subcommand becomes useful
// to drive from the TUI. Do *not* add `serve`, `auth`, `attach`,
// `connect`, `code`, `tmux`, `exec`, `support connect`, or anything
// that pipes raw stdin to the user.
var safeArgvSubcommands = map[string]string{
	"machines":   "list / inspect cloud machines",
	"machine":    "alias for machines",
	"guests":     "manage guest invitations + scopes",
	"projects":   "list projects this agent has discovered",
	"devices":    "list registered devices for this account",
	"discover":   "scan the local network for yaver agents",
	"vault":      "project-scoped secrets (list/get/env/projects/sync)",
	"deploy":     "deploy ship / templates / runs / diagnose",
	"stores":     "store-onboarding concierge (account / keys / TestFlight / IAP / sign-in)",
	"caps":       "infer iOS/Android permissions + Info.plist/entitlements from your code",
	"listing":    "derive a store listing (identity + truthful privacy/data-safety) from code",
	"assets":     "capture store screenshots from a simulator/redroid at exact sizes",
	"workspace":  "yaver.workspace.yaml apps / status / scaffold",
	"ops":        "verb-based grand MCP API (info / status / verbs / ...)",
	"status":     "agent status",
	"version":    "yaver version",
	"feedback":   "feedback reports + visual capture sessions",
	"relay":      "relay server config + health",
	"tunnel":     "tunnel list / status",
	"flags":      "feature flags",
	"env":        "env profile",
	"logs":       "agent logs (tail)",
	"managed":    "managed-subsystem toggles (relay/dns/analytics/...)",
	"phone":      "phone-first mini-backend projects",
	"diagnose":   "diagnostic check across subsystems",
	"doctor":     "build + toolchain doctor",
	"morning":    "morning-summary recordings",
	"primary":    "primary-device pin",
	"sandbox":    "container sandbox status",
	"sdk":        "feedback SDK utilities (status, etc.)",
	"sdk-token":  "SDK token list/create/rotate",
	"sessions":   "transferable agent sessions",
	"session":    "alias for sessions",
	"host-share": "host-share borrowed-workspace status",
	"backup":     "convex backups",
	"changelog":  "release changelog",
	"flutter":    "flutter helpers",
	"expo":       "expo helpers",
	"build":      "build current project",
	"test":       "run tests",
	"info":       "agent info snapshot",
	"health":     "agent health probe",
}

// SafeArgvSubcommandList returns the verbs above sorted alphabetically.
// Used by the slash-menu rebuild + `?` discovery banner.
func SafeArgvSubcommandList() []string {
	out := make([]string, 0, len(safeArgvSubcommands))
	for k := range safeArgvSubcommands {
		out = append(out, k)
	}
	// Stable order — sort.Strings would pull in the sort package
	// just for a one-shot menu render. Insertion sort is fine for ~40
	// items.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// MaybeRunYaverArgv inspects an interactive-prompt input and, if its
// first token is a known safe yaver subcommand, runs `yaver <line>`
// as a subprocess and returns its captured combined output. The
// boolean signals whether the line was treated as a yaver command;
// when false the caller should fall through to the existing slash /
// runner / prompt handling.
//
// The literal leading word "yaver" is stripped so both forms work:
//
//	guests invite foo@bar.com
//	yaver guests invite foo@bar.com
func MaybeRunYaverArgv(line string) (output string, handled bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false, nil
	}
	// Slash commands are owned by handleInteractiveCodeCommand; bail.
	if strings.HasPrefix(line, "/") {
		return "", false, nil
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false, nil
	}
	if fields[0] == "yaver" {
		fields = fields[1:]
		if len(fields) == 0 {
			return "", false, nil
		}
	}
	verb := strings.ToLower(fields[0])
	if _, ok := safeArgvSubcommands[verb]; !ok {
		return "", false, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", true, fmt.Errorf("locate yaver binary: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, fields...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = nil
	runErr := cmd.Run()
	out := buf.String()
	if runErr != nil {
		// Surface the captured output even on failure so the user
		// can see why. The error itself goes back to the caller.
		return out, true, runErr
	}
	return out, true, nil
}
