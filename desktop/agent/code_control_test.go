package main

import (
	"encoding/json"
	"net"
	"net/http"
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

// Cover the three local-info commands (/version, /about, /machine)
// across their plain/slash/backslash aliases. They must all map to a
// stable Kind so the client.go switch can render them consistently
// regardless of how the user typed it.
func TestParseTerminalCommandLocalInfo(t *testing.T) {
	for _, input := range []string{"version", "/version", "\\version", "--version", "-v"} {
		cmd, ok := parseTerminalCommand(input)
		if !ok || cmd.Kind != "version" {
			t.Fatalf("%q parsed as %+v ok=%v, want kind=version", input, cmd, ok)
		}
	}
	for _, input := range []string{"about", "/about", "\\about"} {
		cmd, ok := parseTerminalCommand(input)
		if !ok || cmd.Kind != "about" {
			t.Fatalf("%q parsed as %+v ok=%v, want kind=about", input, cmd, ok)
		}
	}
	for _, input := range []string{"machine", "/machine", "\\machine", "where", "/where", "host", "/host"} {
		cmd, ok := parseTerminalCommand(input)
		if !ok || cmd.Kind != "machine" {
			t.Fatalf("%q parsed as %+v ok=%v, want kind=machine", input, cmd, ok)
		}
	}
}

func TestParseTerminalCommandCloudPending(t *testing.T) {
	for _, input := range []string{"cloud", "/cloud", "cloud-pending", "/cloud-pending", "pending cloud", "/pending cloud"} {
		cmd, ok := parseTerminalCommand(input)
		if !ok || cmd.Kind != "cloud-pending" {
			t.Fatalf("%q parsed as %+v ok=%v, want kind=cloud-pending", input, cmd, ok)
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

func TestCodeSwitchRunnerIgnoresLocalDifferentUserError(t *testing.T) {
	restorePort := currentLocalAgentPort.Load()
	t.Cleanup(func() { currentLocalAgentPort.Store(restorePort) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var hit bool
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/agent/runner/switch" {
			t.Fatalf("path = %q, want /agent/runner/switch", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "token belongs to a different user"})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	currentLocalAgentPort.Store(int64(ln.Addr().(*net.TCPAddr).Port))

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".yaver"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := &Config{AuthToken: "owner-token"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if err := codeSwitchRunner("", "codex"); err != nil {
		t.Fatalf("codeSwitchRunner returned error: %v", err)
	}
	if !hit {
		t.Fatal("expected local agent switch request to be attempted")
	}
}

func TestCodeSwitchRunnerIgnoresLocalAgentUnavailableMessage(t *testing.T) {
	restorePort := currentLocalAgentPort.Load()
	t.Cleanup(func() { currentLocalAgentPort.Store(restorePort) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "agent not reachable"})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	currentLocalAgentPort.Store(int64(ln.Addr().(*net.TCPAddr).Port))

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".yaver"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := &Config{AuthToken: "owner-token"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if err := codeSwitchRunner("", "codex"); err != nil {
		t.Fatalf("codeSwitchRunner returned error: %v", err)
	}
}
