package main

import (
	"strings"
	"testing"
)

// The Convex aiRunners table ships its Claude row under runnerId
// "claude-code" (backend/convex/aiRunners.ts), while builtinRunners is keyed
// "claude" (tasks.go:146). LoadRunnersFromBackend's skip-guard asks
// IsSupportedRunner(r.RunnerID) — which NORMALIZES, so "claude-code" -> "claude"
// -> true — but then looks the row up with the RAW id, which misses, so the
// `continue` never fires and the backend row is injected as a SECOND runner.
//
// The shadow row is not inert. GetRunnerConfig normalizes (tasks.go:237) so it
// can't be SELECTED through that path, but the runner LIST is built by ranging
// the map ("Any remaining runners from Convex", httpserver.go:3607) and dedups
// on the RAW id (seenIDs[r.RunnerID], :3561). "claude" and "claude-code" are
// different keys, so every runner picker — web, mobile, and the list_runners
// MCP verb (:5977) — shows "Claude Code" twice whenever /config is reachable.
//
// The root cause is that this codebase normalizes for BEHAVIOUR but not for
// IDENTITY: two lines below the raw-id dedup, the same row is asked
// normalizeRunnerID(r.RunnerID) == "claude" (:3588). The boundary fix is to
// normalize on ingestion, so an alias can never enter the map as a key.
func TestLoadRunnersFromBackend_ClaudeCodeAliasDoesNotOverrideBuiltin(t *testing.T) {
	restore := snapshotBuiltinRunners()
	defer restore()

	before := builtinRunners["claude"]

	LoadRunnersFromBackend([]backendRunnerFull{{
		RunnerID:   "claude-code", // the alias Convex actually ships
		Name:       "Claude Code",
		Command:    "claude",
		Args:       `["-p","{prompt}","--output-format","stream-json"]`,
		OutputMode: "stream-json",
	}})

	if _, ok := builtinRunners["claude-code"]; ok {
		t.Error("backend alias \"claude-code\" registered a second runner alongside \"claude\" — " +
			"the alias must collapse onto the builtin, not shadow it")
	}

	// Argv for a shipped runner must come from the binary that ships it — the
	// backend rows are snapshots from older CLI releases (see the opencode
	// rename note at tasks.go:320).
	got := builtinRunners["claude"]
	if strings.Join(got.Args, " ") != strings.Join(before.Args, " ") {
		t.Errorf("builtin claude argv was overwritten by the backend row.\n got: %v\nwant: %v",
			got.Args, before.Args)
	}
}

// A runner the agent does NOT ship first-class is the extension point: the
// backend is how an operator adds one without a new binary. Those rows must
// still load — the fix above must not turn the alias guard into a whitelist
// that drops everything unfamiliar.
func TestLoadRunnersFromBackend_UnshippedRunnerStillLoads(t *testing.T) {
	restore := snapshotBuiltinRunners()
	defer restore()

	LoadRunnersFromBackend([]backendRunnerFull{{
		RunnerID:   "aider",
		Name:       "Aider",
		Command:    "aider",
		Args:       `["--yes","--message","{prompt}"]`,
		OutputMode: "raw",
	}})

	got, ok := builtinRunners["aider"]
	if !ok {
		t.Fatal("backend-defined runner \"aider\" did not load — the backend is how a " +
			"non-shipped runner gets added; this must keep working")
	}
	if got.Command != "aider" {
		t.Errorf("aider command = %q, want %q", got.Command, "aider")
	}
}

// The "custom" row is a template, not a runner.
func TestLoadRunnersFromBackend_SkipsCustomTemplate(t *testing.T) {
	restore := snapshotBuiltinRunners()
	defer restore()

	LoadRunnersFromBackend([]backendRunnerFull{{
		RunnerID: "custom", Name: "Custom", Command: "echo",
	}})

	if _, ok := builtinRunners["custom"]; ok {
		t.Error("\"custom\" is a template row and must never register as a runner")
	}
}

// snapshotBuiltinRunners lets each case mutate the package-level registry and
// put it back — these tests would otherwise leak into every later test in the
// package via builtinRunners.
func snapshotBuiltinRunners() func() {
	saved := make(map[string]RunnerConfig, len(builtinRunners))
	for k, v := range builtinRunners {
		saved[k] = v
	}
	return func() {
		builtinRunners = saved
	}
}
