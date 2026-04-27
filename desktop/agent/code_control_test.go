package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestParseInteractiveCodeArgsSlash(t *testing.T) {
	args, ok := parseInteractiveCodeArgs("/get agent")
	if !ok {
		t.Fatal("slash command was not parsed")
	}
	if len(args) != 2 || args[0] != "get" || args[1] != "agent" {
		t.Fatalf("unexpected args: %#v", args)
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

func TestBuildTerminalPromptPayloadDetectsAttachments(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "screen shot.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(dir, "clip.mov")
	if err := os.WriteFile(videoPath, []byte("mov"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := `please inspect "` + imgPath + `" ` + videoPath
	payload := buildTerminalPromptPayload(input)

	if len(payload.Attachments) != 2 {
		t.Fatalf("attachments=%d, want 2", len(payload.Attachments))
	}
	if len(payload.Images) != 1 {
		t.Fatalf("images=%d, want 1", len(payload.Images))
	}
	if !strings.Contains(payload.Prompt, "[Attached local files]") {
		t.Fatalf("prompt missing attachment block: %q", payload.Prompt)
	}
	if !strings.Contains(payload.UserEcho, "screen shot.png") {
		t.Fatalf("echo missing image filename: %q", payload.UserEcho)
	}
}
