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

	mcpArgsPath := filepath.Join(home, "codex-mcp-args.txt")
	script := "#!/bin/sh\n" +
		"set -eu\n" +
		"case \"$1 ${2-} ${3-}\" in\n" +
		"  \"--version  \") echo \"codex test\" ;;\n" +
		"  \"login status \") echo not-logged-in >&2; exit 1 ;;\n" +
		"  \"login --with-api-key \") echo unexpected-api-key-login >&2; exit 44 ;;\n" +
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
	allowInstallOnly := true
	result, err := applyRunnerAuthSetupLocal(context.Background(), runnerAuthSetupRequest{
		Runner:           "codex",
		InstallIfMissing: &installIfMissing,
		CodexLogin:       &codexLogin,
		SetupMCP:         &setupMCP,
		AllowInstallOnly: &allowInstallOnly,
	})
	if err != nil {
		t.Fatalf("applyRunnerAuthSetupLocal: %v", err)
	}
	if !result.OK || !result.Installed || result.Ready || result.AuthConfigured {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.LoginAttempt {
		t.Fatalf("expected no API-key login attempt")
	}
	if len(result.MCPConfigured) != 1 || result.MCPConfigured[0] != "codex" {
		t.Fatalf("expected codex MCP config, got %+v", result.MCPConfigured)
	}
	if !strings.Contains(result.Detail, "ChatGPT Plus/Pro plan OAuth") {
		t.Fatalf("expected plan OAuth detail, got %q", result.Detail)
	}

	mcpArgs, err := os.ReadFile(mcpArgsPath)
	if err != nil {
		t.Fatalf("read MCP args: %v", err)
	}
	if !strings.Contains(string(mcpArgs), "mcp add yaver") {
		t.Fatalf("unexpected MCP args: %q", string(mcpArgs))
	}
}
