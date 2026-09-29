package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// remoteless_test.go — the hosted-model "remoteless" lane (interim backend:
// opencode binary + DeepSeek BYOK key; later an in-process Go loop). The
// stable contract is the runner id; the credential-detection logic is pure
// and hermetically tested here. See docs/architecture/REMOTELESS_AI.md.

func TestGetRunnerConfigRemoteless(t *testing.T) {
	rc := GetRunnerConfig("remoteless")
	if rc.RunnerID != "remoteless" {
		t.Fatalf("expected runner id remoteless, got %q", rc.RunnerID)
	}
	if rc.Command != "opencode" {
		t.Fatalf("interim backend must use the opencode binary, got %q", rc.Command)
	}
	if rc.Model != "deepseek/deepseek-flash" {
		t.Fatalf("expected default model deepseek/deepseek-flash, got %q", rc.Model)
	}
	if !IsSupportedRunner("remoteless") {
		t.Fatalf("remoteless must be in supportedRunnerIDs")
	}
}

func TestRemotelessRunnerIsOwnerPreviewOnly(t *testing.T) {
	if err := validateRemotelessRunnerAccess(false, "remoteless"); err == nil || !strings.Contains(err.Error(), "owner account") {
		t.Fatalf("non-owner remoteless request must fail closed with a named cause, got %v", err)
	}
	if err := validateRemotelessRunnerAccess(true, "remoteless"); err != nil {
		t.Fatalf("owner remoteless request rejected: %v", err)
	}
	if err := validateRemotelessRunnerAccess(false, "opencode"); err != nil {
		t.Fatalf("ordinary runners must remain available: %v", err)
	}
}

func TestRunnerModelCompatibleRemoteless(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"deepseek/deepseek-flash", true},
		{"deepseek/deepseek-chat", true},
		{"gpt-5.4", false}, // cross-runner stale-model footgun must be caught
		{"", true},         // empty = runner default
		{"claude-opus-4-7", false},
	}
	for _, c := range cases {
		if got := runnerModelCompatible("remoteless", c.model); got != c.want {
			t.Errorf("runnerModelCompatible(remoteless, %q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestRemotelessCredentialSourceEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek-test")
	if src := remotelessCredentialSource(t.TempDir()); src != "DEEPSEEK_API_KEY" {
		t.Fatalf("expected env DEEPSEEK_API_KEY source, got %q", src)
	}
}

func TestRemotelessCredentialSourceVault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "DEEPSEEK_API_KEY", Category: "api-key", Value: "vault-deepseek-key"}); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	setRuntimeVaultStore(vs)
	defer setRuntimeVaultStore(nil)

	if src := remotelessCredentialSource(t.TempDir()); src != "vault:DEEPSEEK_API_KEY" {
		t.Fatalf("expected vault:DEEPSEEK_API_KEY source, got %q", src)
	}
}

func TestRemotelessCredentialSourceOpenCodeConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	workDir := t.TempDir()
	cfg := `{
  "provider": {
    "deepseek": {
      "options": {
        "baseURL": "https://api.deepseek.com",
        "apiKey": "sk-deepseek-cfg"
      }
    }
  },
  "model": "deepseek/deepseek-flash"
}`
	if err := os.WriteFile(filepath.Join(workDir, "opencode.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write opencode.json: %v", err)
	}

	src := remotelessCredentialSource(workDir)
	if src == "" {
		t.Fatalf("expected an opencode.json provider.deepseek credential source")
	}
	// The opencode.json path resolves via openCodeConfigPaths; assert it is
	// the file we wrote (relative is fine, it must point at this workDir).
	want := filepath.Join(workDir, "opencode.json")
	if src != want {
		t.Fatalf("expected source %q, got %q", want, src)
	}
}

func TestRemotelessCredentialSourceNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	// No vault store installed by default in tests; empty workDir → no config.
	if src := remotelessCredentialSource(t.TempDir()); src != "" {
		t.Fatalf("expected no credential source, got %q", src)
	}
}

func TestAppendRemotelessFallbackList(t *testing.T) {
	base := []string{"claude", "codex", "opencode"}

	if got := appendRemotelessFallbackList(false, base); !reflect.DeepEqual(got, base) {
		t.Fatalf("lane unusable → list must be untouched, got %v", got)
	}

	got := appendRemotelessFallbackList(true, base)
	want := []string{"claude", "codex", "opencode", "remoteless"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lane usable → remoteless must remain last, got %v want %v", got, want)
	}

	// Dedup when the caller already listed remoteless.
	dup := appendRemotelessFallbackList(true, []string{"remoteless", "opencode", "remoteless"})
	if want := []string{"opencode", "remoteless"}; !reflect.DeepEqual(dup, want) {
		t.Fatalf("dedup failed: got %v want %v", dup, want)
	}
}

func TestDetectRunnerRuntimeStatusRemoteless(t *testing.T) {
	// The binary gate is environment-dependent, so assert only the parts that
	// are deterministic: the lane exists and goes through DetectRunnerRuntimeStatus
	// without panicking, and readiness requires a DeepSeek credential.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	rc := GetRunnerConfig("remoteless")
	status := DetectRunnerRuntimeStatus(rc, t.TempDir())
	if status.AuthConfigured {
		t.Fatalf("no DeepSeek credential → AuthConfigured must be false")
	}
	// With no credential the lane must never report Ready (that would let a
	// picker advertise it in green and then fail the task).
	if status.Ready {
		t.Fatalf("no DeepSeek credential → Ready must be false")
	}
}
