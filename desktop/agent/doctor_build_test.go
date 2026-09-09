package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// doctor_build_test.go — pins the deploy env-file secret resolution added
// 2026-08-12 (headless TestFlight audit). The doctor used to check ONLY
// vault + parent env for deploy secrets, so on a machine where
// ~/.appstoreconnect/yaver.env pre-seeds APP_STORE_KEY_* (the documented
// friction-free path) it reported them MISS — a false red that sent
// operators hunting for a missing secret on a deploy that would have
// worked. deployEnvFileValue() and RunBuildDoctor must read those files.

// testHomeWithDeployEnvFiles builds a fake HOME containing the two
// gitignored deploy env files the scripts source, with a couple of
// representative entries (quoted $HOME path, plain value). Returns the
// home dir; the caller should t.Setenv("HOME", home) and t.Cleanup.
func testHomeWithDeployEnvFiles(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	ascDir := filepath.Join(home, ".appstoreconnect")
	if err := os.MkdirAll(ascDir, 0700); err != nil {
		t.Fatalf("mkdir asc: %v", err)
	}
	asc := "export APP_STORE_KEY_PATH=\"$HOME/.appstoreconnect/private_keys/AuthKey_77Z6B543D5.p8\"\n" +
		"export APP_STORE_KEY_ID=77Z6B543D5\n" +
		"export APPLE_TEAM_ID=5SJZ4KA39A\n"
	if err := os.WriteFile(filepath.Join(ascDir, "yaver.env"), []byte(asc), 0600); err != nil {
		t.Fatalf("write asc env: %v", err)
	}
	secretsDir := filepath.Join(home, ".yaver")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		t.Fatalf("mkdir .yaver: %v", err)
	}
	secrets := "export YAVER_LOGIN_PASSWORD=\"hunter2\"\n"
	if err := os.WriteFile(filepath.Join(secretsDir, "local-secrets.env"), []byte(secrets), 0600); err != nil {
		t.Fatalf("write local-secrets: %v", err)
	}
	return home
}

func TestDeployEnvFileValue_ReadsBothFiles(t *testing.T) {
	home := testHomeWithDeployEnvFiles(t)
	t.Setenv("HOME", home)

	cases := map[string]string{
		// Quoted $HOME path from ~/.appstoreconnect/yaver.env.
		"APP_STORE_KEY_PATH": "$HOME/.appstoreconnect/private_keys/AuthKey_77Z6B543D5.p8",
		"APP_STORE_KEY_ID":   "77Z6B543D5",
		"APPLE_TEAM_ID":      "5SJZ4KA39A",
		// From ~/.yaver/local-secrets.env.
		"YAVER_LOGIN_PASSWORD": "hunter2",
		// Unknown key → empty.
		"NOT_A_REAL_SECRET": "",
	}
	for name, want := range cases {
		if got := deployEnvFileValue(name); got != want {
			t.Errorf("deployEnvFileValue(%q) = %q; want %q", name, got, want)
		}
	}
}

func TestDeployEnvFileValue_PrefersAppstoreconnect(t *testing.T) {
	// Same key in both files: ~/.appstoreconnect/yaver.env is checked
	// first (mirrors deploy-testflight.sh sourcing order).
	home := testHomeWithDeployEnvFiles(t)
	if err := os.WriteFile(
		filepath.Join(home, ".yaver", "local-secrets.env"),
		[]byte("export APPLE_TEAM_ID=WRONG\n"), 0600); err != nil {
		t.Fatalf("overwrite local-secrets: %v", err)
	}
	t.Setenv("HOME", home)
	if got := deployEnvFileValue("APPLE_TEAM_ID"); got != "5SJZ4KA39A" {
		t.Errorf("expected asc env to win, got %q", got)
	}
}

func TestDeployEnvFileValue_MissingHome(t *testing.T) {
	// HOME pointing nowhere must not panic and must return "".
	t.Setenv("HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	if got := deployEnvFileValue("APP_STORE_KEY_PATH"); got != "" {
		t.Errorf("expected empty for missing HOME, got %q", got)
	}
}

func TestResolveSecretValue_ConsultsDeployEnvFiles(t *testing.T) {
	// The path-existence probe calls resolveSecretValue to re-resolve;
	// before 2026-08-12 it stopped at env and returned "" for a secret
	// that lives only in the deploy env files — the p8 then looked
	// missing. It must resolve from the env files too.
	home := testHomeWithDeployEnvFiles(t)
	t.Setenv("HOME", home)
	value, source := resolveSecretValue("APP_STORE_KEY_PATH", "", nil)
	if value == "" {
		t.Fatal("resolveSecretValue returned empty for a deploy-env-file secret")
	}
	if source != "deploy env file" {
		t.Errorf("source = %q; want %q", source, "deploy env file")
	}
	if !filepath.IsAbs(os.ExpandEnv(value)) {
		t.Errorf("expected an expandable path, got %q", value)
	}
}

func TestRunBuildDoctor_FindsSecretsInDeployEnvFiles(t *testing.T) {
	// Full doctor run against the testflight target with a fake HOME:
	// the ASC secrets must resolve as Found with source "deploy env file"
	// (vault nil, parent env empty) — the exact false red we killed.
	home := testHomeWithDeployEnvFiles(t)
	t.Setenv("HOME", home)
	for _, name := range []string{"APP_STORE_KEY_PATH", "APP_STORE_KEY_ID", "APP_STORE_KEY_ISSUER", "APPLE_TEAM_ID"} {
		t.Setenv(name, "")
	}

	report, err := RunBuildDoctor("testflight", "", nil)
	if err != nil {
		t.Fatalf("RunBuildDoctor: %v", err)
	}
	byName := map[string]BuildSecretResult{}
	for _, s := range report.Secrets {
		byName[s.Name] = s
	}
	for _, name := range []string{"APP_STORE_KEY_PATH", "APP_STORE_KEY_ID", "APPLE_TEAM_ID"} {
		res, ok := byName[name]
		if !ok {
			t.Errorf("secret %q missing from report", name)
			continue
		}
		if !res.Found {
			t.Errorf("%s: expected Found via deploy env file, got %+v", name, res)
		}
		if res.Source != "deploy env file" {
			t.Errorf("%s: source = %q; want %q", name, res.Source, "deploy env file")
		}
	}
	// APP_STORE_KEY_ISSUER is genuinely absent from the fake files —
	// it must stay Found=false, proving the resolver doesn't hallucinate.
	if res, ok := byName["APP_STORE_KEY_ISSUER"]; ok && res.Found {
		t.Errorf("APP_STORE_KEY_ISSUER: expected not found, got %+v", res)
	}
}

func TestProbeToolBoundsInheritedOutputPipe(t *testing.T) {
	binDir := t.TempDir()
	toolName := "yaver-hanging-version-probe"
	toolPath := filepath.Join(binDir, toolName)
	// The direct tool exits immediately, but its background child inherits the
	// captured output descriptors. Plain CommandContext+CombinedOutput waits for
	// that child and turns an advisory doctor check into a multi-minute block.
	script := "#!/bin/sh\n(sleep 30) &\nprintf 'probe 1.0\\n'\n"
	if err := os.WriteFile(toolPath, []byte(script), 0700); err != nil {
		t.Fatalf("write hanging probe: %v", err)
	}
	t.Setenv("PATH", binDir)

	started := time.Now()
	got := probeTool(buildTool{Name: toolName, VersionFlag: "--version", Required: true})
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("probe took %v; inherited output pipe was not bounded", elapsed)
	}
	if !got.Found || got.Version != "probe 1.0" {
		t.Fatalf("probe result = %+v", got)
	}
}
