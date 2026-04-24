package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyRunnerAuthSetupLocalCodex(t *testing.T) {
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

	loginKeyPath := filepath.Join(home, "codex-login-key.txt")
	mcpArgsPath := filepath.Join(home, "codex-mcp-args.txt")
	script := "#!/bin/sh\n" +
		"set -eu\n" +
		"case \"$1 ${2-} ${3-}\" in\n" +
		"  \"--version  \") echo \"codex test\" ;;\n" +
		"  \"login --with-api-key \") cat > \"" + loginKeyPath + "\"; mkdir -p \"" + codexHome + "\"; printf '{\"token\":\"ok\"}' > \"" + filepath.Join(codexHome, "auth.json") + "\" ;;\n" +
		"  \"mcp get yaver\") exit 1 ;;\n" +
		"  \"mcp add yaver\") printf '%s' \"$*\" > \"" + mcpArgsPath + "\" ;;\n" +
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

	setupMCP := true
	installIfMissing := false
	codexLogin := true
	result, err := applyRunnerAuthSetupLocal(context.Background(), runnerAuthSetupRequest{
		Runner:           "codex",
		OpenAIAPIKey:     "sk-test-codex",
		InstallIfMissing: &installIfMissing,
		CodexLogin:       &codexLogin,
		SetupMCP:         &setupMCP,
	})
	if err != nil {
		t.Fatalf("applyRunnerAuthSetupLocal: %v", err)
	}
	if !result.OK || !result.Ready || !result.AuthConfigured {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.LoginAttempt {
		t.Fatalf("expected codex login to run")
	}
	if len(result.MCPConfigured) != 1 || result.MCPConfigured[0] != "codex" {
		t.Fatalf("expected codex MCP config, got %+v", result.MCPConfigured)
	}

	loginKey, err := os.ReadFile(loginKeyPath)
	if err != nil {
		t.Fatalf("read login key: %v", err)
	}
	if strings.TrimSpace(string(loginKey)) != "sk-test-codex" {
		t.Fatalf("unexpected login key: %q", string(loginKey))
	}

	if _, err := os.Stat(filepath.Join(codexHome, "auth.json")); err != nil {
		t.Fatalf("expected auth.json to exist: %v", err)
	}

	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	entry, err := vs.Get("", "OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("vault get: %v", err)
	}
	if strings.TrimSpace(entry.Value) != "sk-test-codex" {
		t.Fatalf("unexpected vault value: %q", entry.Value)
	}

	mcpArgs, err := os.ReadFile(mcpArgsPath)
	if err != nil {
		t.Fatalf("read MCP args: %v", err)
	}
	if !strings.Contains(string(mcpArgs), "mcp add yaver") {
		t.Fatalf("unexpected MCP args: %q", string(mcpArgs))
	}
}
