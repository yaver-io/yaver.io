package main

import "testing"

// Pure-function tests for the circuit design-slot routing. These deliberately
// avoid the vault/file paths so the suite never prompts the macOS keychain
// (see the no-keychain-prompt-spam rule).

func TestSanitizeDesignID(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"   ":                  "",
		"default":              "",
		"DEFAULT":              "",
		"panel-1":              "panel-1",
		"  Panel_1.rev ":       "panel_1.rev",
		"gridpilot/rev-c":      "gridpilotrev-c", // slash dropped (no path injection)
		"../../etc/passwd":     "etcpasswd",      // traversal stripped
		"a b c":                "abc",            // spaces dropped
		"--lead.trail__":       "lead.trail",     // trimmed of edge punctuation
		"UPPER":                "upper",
		"weird!@#$%^&*()chars": "weirdchars",
	}
	for in, want := range cases {
		if got := sanitizeDesignID(in); got != want {
			t.Errorf("sanitizeDesignID(%q) = %q, want %q", in, got, want)
		}
	}

	// length cap at 64.
	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	if got := sanitizeDesignID(long); len(got) != 64 {
		t.Errorf("sanitizeDesignID(len=100) len = %d, want 64", len(got))
	}
}

func TestCircuitSlotName(t *testing.T) {
	// default slot keeps the legacy vault name for back-compat.
	if got := circuitSlotName(""); got != circuitVaultConfigName {
		t.Errorf("default slot name = %q, want %q", got, circuitVaultConfigName)
	}
	// named slots are namespaced under the design prefix.
	if got := circuitSlotName("panel-1"); got != "circuit-design-panel-1" {
		t.Errorf("named slot = %q, want circuit-design-panel-1", got)
	}
	// two distinct designs must never collide on the default slot.
	a, b := circuitSlotName("talos-x"), circuitSlotName("ocpp-y")
	if a == b || a == circuitVaultConfigName || b == circuitVaultConfigName {
		t.Errorf("named slots collided: %q / %q (default=%q)", a, b, circuitVaultConfigName)
	}
}

func TestDesignLabelOut(t *testing.T) {
	if got := designLabelOut(""); got != "default" {
		t.Errorf("empty label = %q, want default", got)
	}
	if got := designLabelOut("default"); got != "default" {
		t.Errorf("'default' label = %q, want default", got)
	}
	if got := designLabelOut("  Rev-B "); got != "rev-b" {
		t.Errorf("label = %q, want rev-b", got)
	}
}

// TestCircuitConfigFilePathFor confirms named slots resolve to distinct files
// (tenant isolation within a per-product box) and the default keeps its path.
func TestCircuitConfigFilePathFor(t *testing.T) {
	def := circuitConfigFilePathFor("")
	if def != circuitConfigFilePath() {
		t.Errorf("default file path mismatch: %q vs %q", def, circuitConfigFilePath())
	}
	p1 := circuitConfigFilePathFor("panel-1")
	p2 := circuitConfigFilePathFor("panel-2")
	if p1 == p2 || p1 == def || p2 == def {
		t.Errorf("file paths collided: def=%q p1=%q p2=%q", def, p1, p2)
	}
}
