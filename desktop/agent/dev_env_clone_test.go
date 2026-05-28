package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevEnvironmentClonePlanRequiresExistingTargetDevice(t *testing.T) {
	s := NewHTTPServer(0, "tok", "user", "local-device", "", "host", NewTaskManager(t.TempDir(), nil, defaultRunner))
	plan := buildDevEnvironmentClonePlan(t.Context(), s, DevEnvironmentCloneRequest{
		Target: DevEnvironmentCloneTarget{Mode: "existing-device"},
		Repos:  []DevEnvironmentCloneRepo{{URL: "https://github.com/example/app.git"}},
	})
	if plan.OK {
		t.Fatal("expected plan to be blocked without targetDeviceId")
	}
	if len(plan.Steps) == 0 || plan.Steps[0].Status != "error" {
		t.Fatalf("expected first step error, got %#v", plan.Steps)
	}
}

func TestDevEnvironmentClonePlanSSHWithoutDeviceIsManualContinuation(t *testing.T) {
	s := NewHTTPServer(0, "tok", "user", "local-device", "", "host", NewTaskManager(t.TempDir(), nil, defaultRunner))
	plan := buildDevEnvironmentClonePlan(t.Context(), s, DevEnvironmentCloneRequest{
		Target: DevEnvironmentCloneTarget{Mode: "ssh", SSHHost: "example.test"},
		Repos:  []DevEnvironmentCloneRepo{{URL: "https://github.com/example/app.git"}},
	})
	if !plan.OK {
		t.Fatalf("expected ssh setup plan to be accepted, got %#v", plan)
	}
	if len(plan.ManualSteps) == 0 {
		t.Fatalf("expected manual continuation step, got %#v", plan.ManualSteps)
	}
	if plan.TargetMode != "ssh" {
		t.Fatalf("target mode = %q", plan.TargetMode)
	}
}

func TestDevEnvironmentClonePlanHTTP(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	body, _ := json.Marshal(DevEnvironmentCloneRequest{
		TargetDeviceID: "target-device",
		Repos:          []DevEnvironmentCloneRepo{{URL: "https://github.com/example/app.git"}},
		DryRun:         true,
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/dev-environments/clone/plan", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out DevEnvironmentClonePlan
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.TargetDeviceID != "target-device" || len(out.Repos) != 1 {
		t.Fatalf("unexpected plan: %#v", out)
	}
}

func TestCloneURLForDetectedRepo(t *testing.T) {
	if got := cloneURLForDetectedRepo(CIGitHub, "owner/repo"); got != "https://github.com/owner/repo.git" {
		t.Fatalf("github url = %q", got)
	}
	if got := cloneURLForDetectedRepo(CIGitLab, "group/repo"); got != "https://gitlab.com/group/repo.git" {
		t.Fatalf("gitlab url = %q", got)
	}
}

func TestDevConfigBundleAllowlistAndSecretFilter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite := func(rel, content string) {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(".vimrc", "set number\nPlugin 'VundleVim/Vundle.vim'\n")
	mustWrite(".config/i3/config", "bindsym $mod+Return exec alacritty\n")
	mustWrite(".config/opencode/token.json", `{"access_token":"secret"}`)

	configs := discoverDevConfigs()
	if len(configs) < 2 {
		t.Fatalf("expected detected configs, got %#v", configs)
	}
	bundle := buildDevConfigBundle(nil)
	paths := map[string]bool{}
	for _, item := range bundle.Items {
		paths[item.RelPath] = true
	}
	if !paths[".vimrc"] || !paths[".config/i3/config"] {
		t.Fatalf("expected vimrc and i3 config in bundle, got %#v", paths)
	}
	if paths[".config/opencode/token.json"] {
		t.Fatalf("secret-looking opencode token file must not be bundled")
	}
}

func TestApplyDevConfigBundleRejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []string{"../escape", "..", "/etc/passwd", "../../escape"}
	for _, rel := range cases {
		bundle := DevConfigBundle{
			Items: []DevConfigBundleItem{{
				Key:      "evil",
				Kind:     "test",
				RelPath:  rel,
				Mode:     0o600,
				Contents: base64.StdEncoding.EncodeToString([]byte("pwned")),
			}},
		}
		if _, err := applyDevConfigBundle(bundle, false); err == nil {
			t.Fatalf("expected error for unsafe rel path %q", rel)
		}
	}
}

func TestApplyDevConfigBundleBackupAndPerms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	existing := filepath.Join(home, ".vimrc")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle := DevConfigBundle{
		Items: []DevConfigBundleItem{{
			Key:      "vimrc",
			Kind:     "vim",
			RelPath:  ".vimrc",
			Mode:     0o644,
			Contents: base64.StdEncoding.EncodeToString([]byte("new")),
		}},
	}
	out, err := applyDevConfigBundle(bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "new" {
		t.Fatalf("file not overwritten: %q", got)
	}
	backedUp, _ := out["backedUp"].([]string)
	if len(backedUp) != 1 {
		t.Fatalf("expected 1 backup, got %#v", backedUp)
	}
	entries, _ := os.ReadDir(home)
	var foundBackup bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".vimrc.yaver-backup-") {
			foundBackup = true
		}
	}
	if !foundBackup {
		t.Fatalf("expected backup file in %s, got %v", home, entries)
	}
}

func TestDispatchDevEnvironmentCloneMCPPlan(t *testing.T) {
	s := NewHTTPServer(0, "tok", "user", "local-device", "", "host", NewTaskManager(t.TempDir(), nil, defaultRunner))
	args, _ := json.Marshal(DevEnvironmentCloneRequest{
		TargetDeviceID: "target-device",
		Repos:          []DevEnvironmentCloneRepo{{URL: "https://github.com/example/app.git"}},
		DryRun:         true,
	})
	handled, result := dispatchDevEnvironmentCloneMCP(s, "dev_environment_clone_plan", json.RawMessage(args))
	if !handled {
		t.Fatal("expected dev_environment_clone_plan to be handled")
	}
	if result == nil {
		t.Fatal("expected non-nil MCP result")
	}
	handled, _ = dispatchDevEnvironmentCloneMCP(s, "not_a_tool", nil)
	if handled {
		t.Fatal("expected unknown tool to be unhandled")
	}
}

func TestSanitizeRepoURLAndStatusScrub(t *testing.T) {
	if got := sanitizeRepoURL("https://alice:s3cret@github.com/o/r.git"); got != "https://github.com/o/r.git" {
		t.Fatalf("userinfo not stripped: %q", got)
	}
	if got := sanitizeRepoURL("https://github.com/o/r.git"); got != "https://github.com/o/r.git" {
		t.Fatalf("plain URL mutated: %q", got)
	}
	if got := scrubCredentialURLs("fatal: clone https://x:y@example.com/r.git failed"); strings.Contains(got, "x:y@") {
		t.Fatalf("creds not scrubbed: %q", got)
	}

	devEnvironmentCloneJobs.Lock()
	job := &DevEnvironmentCloneJob{
		ID:     "test-scrub-1",
		Status: "failed",
		Request: DevEnvironmentCloneRequest{
			Repos: []DevEnvironmentCloneRepo{{URL: "https://alice:s3cret@github.com/o/r.git"}},
		},
		Plan: DevEnvironmentClonePlan{
			Repos: []DevEnvironmentCloneRepo{{URL: "https://alice:s3cret@github.com/o/r.git"}},
		},
		Steps: []DevEnvironmentCloneStep{{
			ID:     "repo_clone",
			Title:  "Clone https://github.com/o/r.git",
			Detail: "git: clone https://alice:s3cret@github.com/o/r.git refused",
		}},
		Error: "https://alice:s3cret@github.com/o/r.git unreachable",
	}
	devEnvironmentCloneJobs.m[job.ID] = job
	devEnvironmentCloneJobs.Unlock()
	defer func() {
		devEnvironmentCloneJobs.Lock()
		delete(devEnvironmentCloneJobs.m, "test-scrub-1")
		devEnvironmentCloneJobs.Unlock()
	}()

	got, ok := getDevEnvironmentCloneJob("test-scrub-1")
	if !ok {
		t.Fatal("expected job")
	}
	if strings.Contains(got.Request.Repos[0].URL, "s3cret") {
		t.Fatalf("request URL leaked secret: %q", got.Request.Repos[0].URL)
	}
	if strings.Contains(got.Plan.Repos[0].URL, "s3cret") {
		t.Fatalf("plan URL leaked secret: %q", got.Plan.Repos[0].URL)
	}
	if strings.Contains(got.Steps[0].Detail, "s3cret") {
		t.Fatalf("step detail leaked secret: %q", got.Steps[0].Detail)
	}
	if strings.Contains(got.Error, "s3cret") {
		t.Fatalf("error leaked secret: %q", got.Error)
	}
}
