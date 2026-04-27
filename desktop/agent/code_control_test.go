package main

import "testing"

func TestParseTerminalCommandExitAliases(t *testing.T) {
	for _, input := range []string{"exit", "/exit", "\\exit", "quit", "\\quit", "detach"} {
		cmd, ok := parseTerminalCommand(input)
		if !ok {
			t.Fatalf("%q was not parsed", input)
		}
		if cmd.Kind != "detach" {
			t.Fatalf("%q parsed as %q, want detach", input, cmd.Kind)
		}
	}
}

func TestParseTerminalCommandAgentQueries(t *testing.T) {
	cmd, ok := parseTerminalCommand("get agent")
	if !ok || cmd.Kind != "agent" {
		t.Fatalf("get agent parsed as %+v ok=%v", cmd, ok)
	}

	cmd, ok = parseTerminalCommand("set agent opencode openai/gpt-5.4")
	if !ok {
		t.Fatal("set agent command not parsed")
	}
	if cmd.Kind != "set-agent" {
		t.Fatalf("kind=%q, want set-agent", cmd.Kind)
	}
	if cmd.Runner != "opencode" {
		t.Fatalf("runner=%q, want opencode", cmd.Runner)
	}
	if cmd.Model != "openai/gpt-5.4" {
		t.Fatalf("model=%q, want openai/gpt-5.4", cmd.Model)
	}
}

func TestCodeAttachedDevice(t *testing.T) {
	if got := codeAttachedDevice(&CodeCLIConfig{WorkMode: codeWorkModeLocal, AttachedDeviceID: "abc"}); got != "" {
		t.Fatalf("local mode returned attached device %q", got)
	}
	if got := codeAttachedDevice(&CodeCLIConfig{WorkMode: codeWorkModeAttached, AttachedDeviceID: "abc"}); got != "abc" {
		t.Fatalf("attached mode returned %q, want abc", got)
	}
}

func TestMatchCodeProject(t *testing.T) {
	projects := []codeProjectRow{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	got, err := matchCodeProject(projects, "bet")
	if err != nil {
		t.Fatalf("matchCodeProject() error = %v", err)
	}
	if got.Path != "/tmp/beta" {
		t.Fatalf("matched %q, want /tmp/beta", got.Path)
	}
}
