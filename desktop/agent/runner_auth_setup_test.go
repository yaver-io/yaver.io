package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyRunnerAuthSetupLocalCodexInstallOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub uses POSIX sh")
	}

	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	stubDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatalf("mkdir stub dir: %v", err)
	}
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}

	script := "#!/bin/sh\n" +
		"set -eu\n" +
		"case \"$1 ${2-} ${3-}\" in\n" +
		"  \"--version  \") echo \"codex test\" ;;\n" +
		"  \"login status \") echo not-logged-in >&2; exit 1 ;;\n" +
		"  \"login \"*) echo unexpected-codex-login-command >&2; exit 44 ;;\n" +
		"  \"mcp \"*) echo unexpected-mcp-modification >&2; exit 45 ;;\n" +
		"  *) exit 0 ;;\n" +
		"esac\n"
	codexPath := filepath.Join(stubDir, "codex")
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write codex stub: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("YAVER_VAULT_PASSPHRASE", "test-passphrase")

	installIfMissing := false
	result, err := applyRunnerAuthSetupLocal(context.Background(), runnerAuthSetupRequest{
		Runner:           "codex",
		InstallIfMissing: &installIfMissing,
	})
	if err != nil {
		t.Fatalf("applyRunnerAuthSetupLocal: %v", err)
	}
	if !result.OK || !result.Installed || result.Ready || result.AuthConfigured {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.LoginAttempt {
		t.Fatalf("expected no Codex login command attempt")
	}
	if len(result.MCPConfigured) != 0 || len(result.VaultKeys) != 0 {
		t.Fatalf("install-only setup must not modify MCP or credentials: %+v", result)
	}
	if !strings.Contains(result.Detail, "encrypted Yaver PTY") || !strings.Contains(result.Detail, "native codex sign-in") {
		t.Fatalf("expected native PTY guidance, got %q", result.Detail)
	}
}

func TestApplyRunnerAuthSetupRejectsCredentialMaterial(t *testing.T) {
	_, err := applyRunnerAuthSetupLocal(context.Background(), runnerAuthSetupRequest{
		Runner:       "opencode",
		OpenAIAPIKey: "must-not-enter-yaver",
	})
	if err == nil || !strings.Contains(err.Error(), "does not accept runner credentials") {
		t.Fatalf("expected credential refusal, got %v", err)
	}
}
