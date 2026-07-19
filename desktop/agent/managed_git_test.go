package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedGitCreateCheckpointAndBackup(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{
		Enabled:    true,
		Visibility: "private",
	})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	if !meta.Enabled || meta.RepoID != "demo" || meta.Visibility != "private" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if _, err := os.Stat(meta.BarePath); err != nil {
		t.Fatalf("bare repo missing: %v", err)
	}
	if strings.TrimSpace(meta.LastCommit) == "" {
		t.Fatalf("initial commit missing: %+v", meta)
	}

	if err := os.WriteFile(filepath.Join(workDir, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, err := ManagedGitCheckpoint(workDir, "yaver: add feature")
	if err != nil {
		t.Fatalf("ManagedGitCheckpoint: %v", err)
	}
	if commit == "" || commit == meta.LastCommit {
		t.Fatalf("checkpoint did not advance: before=%q after=%q", meta.LastCommit, commit)
	}

	backup, err := ManagedGitBackup(workDir)
	if err != nil {
		t.Fatalf("ManagedGitBackup: %v", err)
	}
	if backup.Path == "" || backup.SizeBytes <= 0 {
		t.Fatalf("bad backup: %+v", backup)
	}
	if _, err := os.Stat(backup.Path); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

// Mirror-on-push and sync must be safe no-ops when no mirror is connected (the
// common case) — and a checkpoint must still succeed.
func TestManagedGitSyncNoMirrorIsSafe(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	if got := ManagedGitMirrorSyncAll(workDir); got != nil {
		t.Fatalf("mirror-on-push with no mirror should be nil, got %+v", got)
	}
	res, err := ManagedGitMirrorPull(workDir)
	if err != nil {
		t.Fatalf("ManagedGitMirrorPull no-mirror: %v", err)
	}
	if res != "no-mirror" {
		t.Fatalf("expected no-mirror, got %q", res)
	}
	// checkpoint (which now calls mirror-on-push) must still succeed
	if err := os.WriteFile(filepath.Join(workDir, "x.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ManagedGitCheckpoint(workDir, "yaver: with no mirror"); err != nil {
		t.Fatalf("checkpoint with no mirror: %v", err)
	}
}

func TestNormalizeRelaySourceBranchStaysUnderYaver(t *testing.T) {
	if got := normalizeRelaySourceBranch("feature/login", "task-1"); got != "yaver/source/task-1" {
		t.Fatalf("feature branch normalized to %q", got)
	}
	if got := normalizeRelaySourceBranch("yaver/alice", "task-1"); got != "yaver/alice" {
		t.Fatalf("yaver branch normalized to %q", got)
	}
	if got := normalizeRelaySourceBranch("yaver/main", "task-1"); got != "yaver/source/task-1" {
		t.Fatalf("protected yaver/main normalized to %q", got)
	}
	if got := normalizeRelaySourceBranch("yaver/../prod.lock", "task-2"); got != "yaver/prod" {
		t.Fatalf("dangerous branch normalized to %q", got)
	}
}

func TestManagedGitRelayBranchRefspecScopesToYaver(t *testing.T) {
	refspec, err := managedGitRelayBranchRefspec("yaver/source/demo")
	if err != nil {
		t.Fatalf("managedGitRelayBranchRefspec: %v", err)
	}
	if refspec != "HEAD:refs/heads/yaver/source/demo" {
		t.Fatalf("refspec = %q", refspec)
	}
	bareRefspec, err := managedGitRelayBareBranchRefspec("yaver/source/demo")
	if err != nil {
		t.Fatalf("managedGitRelayBareBranchRefspec: %v", err)
	}
	if bareRefspec != "refs/heads/yaver/source/demo:refs/heads/yaver/source/demo" {
		t.Fatalf("bare refspec = %q", bareRefspec)
	}
	for _, branch := range []string{
		"main",
		"yaver/main",
		"yaver/master",
		"yaver/../prod",
		"yaver/source/demo:main",
		"yaver/source/demo lock",
	} {
		if _, err := managedGitRelayBranchRefspec(branch); err == nil {
			t.Fatalf("expected %q to be rejected", branch)
		}
	}
}

func TestManagedGitProviderBranchFromGitHubAppTokenMarksAppInstallation(t *testing.T) {
	branch := managedGitProviderBranchFromGitHubAppToken(&relaySourceGitHubAppToken{
		ProviderKind:      "github",
		ProviderHost:      "github.com",
		ProviderRepo:      "acme/app",
		ProviderBranch:    "yaver/source/demo",
		ProviderBranchURL: "https://github.com/acme/app/tree/yaver/source/demo",
		Token:             "ghs_secret",
	})
	if branch.ProviderKind != "github" || branch.ProviderHost != "github.com" || branch.ProviderRepo != "acme/app" {
		t.Fatalf("unexpected provider branch target: %+v", branch)
	}
	if branch.ProviderBranch != "yaver/source/demo" || branch.ProviderBranchURL == "" {
		t.Fatalf("unexpected provider branch: %+v", branch)
	}
	if branch.ProviderAuthMode != "app_installation" || branch.ProviderAuthStatus != "available" {
		t.Fatalf("unexpected provider auth labels: %+v", branch)
	}
}

func TestManagedGitPushRelaySourceBranchWithGitHubAppTokenRedactsTokenOnFailure(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	if _, err := ManagedGitPrepareRelaySourceBranch(workDir, "yaver/source/demo", "main"); err != nil {
		t.Fatalf("ManagedGitPrepareRelaySourceBranch: %v", err)
	}
	secret := "ghs_super_secret_token"
	_, err := ManagedGitPushRelaySourceBranchWithGitHubAppToken(workDir, &relaySourceGitHubAppToken{
		ProviderKind:   "github",
		ProviderHost:   "127.0.0.1:1",
		ProviderRepo:   "acme/app",
		ProviderBranch: "yaver/source/demo",
		Token:          secret,
	})
	if err == nil {
		t.Fatal("expected push failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("push error leaked token: %v", err)
	}
}

func TestManagedGitRelayProviderBranchFromMirrorMarksOwnerTokenFallback(t *testing.T) {
	branch := managedGitRelayProviderBranchFromMirror(ManagedGitMirrorMeta{
		Provider: "github",
		Host:     "github.com",
		FullName: "acme/app",
	}, "yaver/source/demo")
	if branch.ProviderKind != "github" || branch.ProviderHost != "github.com" || branch.ProviderRepo != "acme/app" {
		t.Fatalf("unexpected provider branch target: %+v", branch)
	}
	if branch.ProviderBranch != "yaver/source/demo" {
		t.Fatalf("provider branch = %q", branch.ProviderBranch)
	}
	if branch.ProviderBranchURL != "https://github.com/acme/app/tree/yaver/source/demo" {
		t.Fatalf("provider branch url = %q", branch.ProviderBranchURL)
	}
	if branch.ProviderAuthMode != "owner_local_token" || branch.ProviderAuthStatus != "owner_token_fallback" {
		t.Fatalf("unexpected provider auth labels: %+v", branch)
	}
}

func TestManagedGitPrepareRelaySourceBranchUsesTempClone(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	beforeBranch, _ := managedGitCmd(workDir, "branch", "--show-current")
	result, err := ManagedGitPrepareRelaySourceBranch(workDir, "feature/login", "main")
	if err != nil {
		t.Fatalf("ManagedGitPrepareRelaySourceBranch: %v", err)
	}
	if result.Branch != "yaver/source/demo" {
		t.Fatalf("branch = %q", result.Branch)
	}
	if result.Commit == "" || result.Commit != meta.LastCommit {
		t.Fatalf("commit = %q, want %q", result.Commit, meta.LastCommit)
	}
	if out, err := managedGitCmd("", "--git-dir", meta.BarePath, "show-ref", "--verify", "--quiet", "refs/heads/"+result.Branch); err != nil {
		t.Fatalf("branch was not pushed to bare repo: %s: %v", out, err)
	}
	afterBranch, _ := managedGitCmd(workDir, "branch", "--show-current")
	if strings.TrimSpace(afterBranch) != strings.TrimSpace(beforeBranch) {
		t.Fatalf("working branch changed: before=%q after=%q", beforeBranch, afterBranch)
	}
}

func TestManagedGitApplyRelaySourcePatchCommitsScopedFiles(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	beforeBranch, _ := managedGitCmd(workDir, "branch", "--show-current")
	result, err := ManagedGitApplyRelaySourcePatch(workDir, "yaver/source/demo", "main", "yaver: relay note", []ManagedGitRelaySourceFilePatch{
		{Path: "src/note.ts", Content: "export const note = 'relay';\n"},
	})
	if err != nil {
		t.Fatalf("ManagedGitApplyRelaySourcePatch: %v", err)
	}
	if result.Noop || result.Commit == "" || len(result.FilesChanged) != 1 || result.FilesChanged[0] != "src/note.ts" {
		t.Fatalf("unexpected result: %+v", result)
	}
	out, err := managedGitCmd("", "--git-dir", meta.BarePath, "show", "refs/heads/yaver/source/demo:src/note.ts")
	if err != nil {
		t.Fatalf("show patched file: %s: %v", out, err)
	}
	if out != "export const note = 'relay';\n" {
		t.Fatalf("patched content = %q", out)
	}
	afterBranch, _ := managedGitCmd(workDir, "branch", "--show-current")
	if strings.TrimSpace(afterBranch) != strings.TrimSpace(beforeBranch) {
		t.Fatalf("working branch changed: before=%q after=%q", beforeBranch, afterBranch)
	}
}

func TestManagedGitApplyRelaySourcePatchRejectsSecretsAndNativeDirs(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	cases := []ManagedGitRelaySourceFilePatch{
		{Path: ".env", Content: "SECRET=x\n"},
		{Path: "../outside.ts", Content: "x\n"},
		{Path: "android/app/build.gradle", Content: "x\n"},
		{Path: "package-lock.json", Content: "{}\n"},
	}
	for _, tc := range cases {
		if _, err := ManagedGitApplyRelaySourcePatch(workDir, "yaver/source/demo", "main", "", []ManagedGitRelaySourceFilePatch{tc}); err == nil {
			t.Fatalf("expected reject for %+v", tc)
		}
	}
}

func TestPlanManagedGitRelaySourcePatchAllowsExplicitSafeFiles(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	plan, err := PlanManagedGitRelaySourcePatch(workDir, "feature/login", "main", "Add login copy", "update the button text", []ManagedGitRelaySourceFilePatch{
		{Path: "src/login.tsx", Content: "export const label = 'Sign in';\n"},
	})
	if err != nil {
		t.Fatalf("PlanManagedGitRelaySourcePatch: %v", err)
	}
	if !plan.OK || !plan.RelayEligible || !plan.CanApply || plan.Mode != "apply_patch" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Branch != "yaver/source/demo" || plan.BaseBranch != "main" {
		t.Fatalf("unexpected branch/base: %+v", plan)
	}
	if len(plan.FilesPlanned) != 1 || plan.FilesPlanned[0] != "src/login.tsx" {
		t.Fatalf("unexpected files: %+v", plan.FilesPlanned)
	}
	if plan.CommitMessage != "yaver: Add login copy" {
		t.Fatalf("commit message = %q", plan.CommitMessage)
	}
}

func TestPlanManagedGitRelaySourcePatchWithoutFilesPreparesOnly(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	plan, err := PlanManagedGitRelaySourcePatch(workDir, "", "", "Make the header clearer", "small copy tweak", nil)
	if err != nil {
		t.Fatalf("PlanManagedGitRelaySourcePatch: %v", err)
	}
	if !plan.OK || !plan.RelayEligible || plan.CanApply || plan.Mode != "prepare_only" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Reasons) == 0 || !strings.Contains(strings.Join(plan.Reasons, " "), "explicit safe file patches") {
		t.Fatalf("missing explicit-patch reason: %+v", plan.Reasons)
	}
}

func TestPlanManagedGitRelaySourcePatchGeneratesFromYaverFileBlock(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	prompt := "Create this small docs file:\n```yaver-file docs/relay-note.md\n# Relay Note\n\nTiny safe note.\n```\n"
	plan, err := PlanManagedGitRelaySourcePatch(workDir, "", "", "Add relay note", prompt, nil)
	if err != nil {
		t.Fatalf("PlanManagedGitRelaySourcePatch: %v", err)
	}
	if !plan.OK || !plan.RelayEligible || !plan.CanApply || plan.Mode != "apply_patch" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.FilesPlanned) != 1 || plan.FilesPlanned[0] != "docs/relay-note.md" {
		t.Fatalf("unexpected files: %+v", plan.FilesPlanned)
	}
	if !strings.Contains(strings.Join(plan.Reasons, " "), "yaver-file") {
		t.Fatalf("missing yaver-file reason: %+v", plan.Reasons)
	}
}

func TestPlanManagedGitRelaySourcePatchRejectsUnsafeGeneratedBlock(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	prompt := "```yaver-file docs/relay-note.md\nAPI_KEY=sk-test\n```\n"
	plan, err := PlanManagedGitRelaySourcePatch(workDir, "", "", "Add relay note", prompt, nil)
	if err != nil {
		t.Fatalf("PlanManagedGitRelaySourcePatch: %v", err)
	}
	if !plan.OK || plan.RelayEligible || plan.CanApply || plan.Mode != "compute_required" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if !strings.Contains(strings.Join(plan.Reasons, " "), "api key") {
		t.Fatalf("missing secret rejection reason: %+v", plan.Reasons)
	}
}

func TestPlanManagedGitRelaySourcePatchRoutesHeavyPromptToCompute(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	plan, err := PlanManagedGitRelaySourcePatch(workDir, "yaver/source/demo", "main", "Build APK", "run gradle and build apk", []ManagedGitRelaySourceFilePatch{
		{Path: "src/readme.md", Content: "x\n"},
	})
	if err != nil {
		t.Fatalf("PlanManagedGitRelaySourcePatch: %v", err)
	}
	if !plan.OK || plan.RelayEligible || plan.CanApply || plan.Mode != "compute_required" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Reasons) == 0 || !strings.Contains(strings.Join(plan.Reasons, " "), "compute") {
		t.Fatalf("missing compute reason: %+v", plan.Reasons)
	}
}

func TestManagedGitRelaySourceWorkOnceAppliesGeneratedYaverFilePatch(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	payload := map[string]any{
		"workDir":     workDir,
		"intentId":    "intent-1",
		"localTaskId": "local-1",
		"title":       "Add relay docs note",
		"prompt":      "```yaver-file docs/relay-note.md\n# Relay Note\n\nTiny safe note.\n```\n",
	}
	raw, _ := json.Marshal(payload)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/managed-git/relay-source/work-once", bytes.NewReader(raw))
	(&HTTPServer{}).handleManagedGitRelaySourceWorkOnce(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out ManagedGitRelaySourceWorkResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !out.OK || out.Plan == nil || out.Plan.Mode != "apply_patch" || out.Apply == nil || out.Apply.Commit == "" {
		t.Fatalf("unexpected response: %+v", out)
	}
	contents, err := managedGitCmd("", "--git-dir", meta.BarePath, "show", "refs/heads/yaver/source/demo:docs/relay-note.md")
	if err != nil {
		t.Fatalf("show generated relay patch: %s: %v", contents, err)
	}
	if contents != "# Relay Note\n\nTiny safe note.\n" {
		t.Fatalf("contents = %q", contents)
	}
}

func TestManagedGitRelaySourceWorkOnceAppliesExplicitPatch(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	payload := map[string]any{
		"workDir":     workDir,
		"intentId":    "intent-1",
		"localTaskId": "local-1",
		"title":       "Add relay note",
		"prompt":      "small source-only update",
		"files": []map[string]string{
			{"path": "src/relay-note.ts", "content": "export const relayNote = true;\n"},
		},
	}
	raw, _ := json.Marshal(payload)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/managed-git/relay-source/work-once", bytes.NewReader(raw))
	(&HTTPServer{}).handleManagedGitRelaySourceWorkOnce(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out ManagedGitRelaySourceWorkResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !out.OK || out.Plan == nil || out.Plan.Mode != "apply_patch" || out.Apply == nil || out.Apply.Commit == "" {
		t.Fatalf("unexpected response: %+v", out)
	}
	contents, err := managedGitCmd("", "--git-dir", meta.BarePath, "show", "refs/heads/yaver/source/demo:src/relay-note.ts")
	if err != nil {
		t.Fatalf("show relay patch: %s: %v", contents, err)
	}
	if contents != "export const relayNote = true;\n" {
		t.Fatalf("contents = %q", contents)
	}
}

func TestManagedGitRelaySourceWorkOnceMetadataOnlyPreparesBranch(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	payload := map[string]any{
		"workDir":     workDir,
		"intentId":    "intent-2",
		"localTaskId": "local-2",
		"branch":      "yaver/source/queued",
	}
	raw, _ := json.Marshal(payload)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/managed-git/relay-source/work-once", bytes.NewReader(raw))
	(&HTTPServer{}).handleManagedGitRelaySourceWorkOnce(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out ManagedGitRelaySourceWorkResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !out.OK || out.Plan == nil || out.Plan.Mode != "prepare_only" || out.Prepare == nil || out.Apply != nil {
		t.Fatalf("unexpected response: %+v", out)
	}
	if out.Prepare.Branch != "yaver/source/queued" {
		t.Fatalf("branch = %q", out.Prepare.Branch)
	}
	if out.Prepare.Commit == "" {
		t.Fatalf("missing prepare commit: %+v", out.Prepare)
	}
	if out.Prepare.Commit != meta.LastCommit {
		t.Fatalf("prepare commit = %q, want %q", out.Prepare.Commit, meta.LastCommit)
	}
}

func TestCreateTodoPhoneProjectWithManagedGitLifecycle(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())

	project, err := CreatePhoneProject(PhoneCreateSpec{
		Slug:     "todo-demo",
		Name:     "Todo Demo",
		Template: "todos",
		ManagedGit: &ManagedGitCreateOptions{
			Enabled:    true,
			Visibility: "private",
		},
	})
	if err != nil {
		t.Fatalf("CreatePhoneProject: %v", err)
	}
	if project.ManagedGit == nil || !project.ManagedGit.Enabled {
		t.Fatalf("managed git missing: %+v", project)
	}
	if project.ManagedGit.DefaultBranch != "main" {
		t.Fatalf("default branch = %q, want main", project.ManagedGit.DefaultBranch)
	}

	note := filepath.Join(project.Dir, "src", "todo-note.txt")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("normie todo app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, err := ManagedGitCheckpoint(project.Dir, "yaver: add todo note")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if commit == "" {
		t.Fatal("empty checkpoint commit")
	}

	backup, err := ManagedGitBackup(project.Dir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if backup.SizeBytes <= 0 {
		t.Fatalf("empty backup: %+v", backup)
	}

	if err := os.WriteFile(note, []byte("broken local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err := ManagedGitRestoreBundle(project.Dir, backup.Path)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored == "" {
		t.Fatal("empty restored commit")
	}
	got, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "normie todo app\n" {
		t.Fatalf("restore content = %q", string(got))
	}

	ownedBackupRoot := filepath.Join(t.TempDir(), "owned-pc-backup")
	external, err := ManagedGitBackupToTarget(project.Dir, "local-folder", "", ownedBackupRoot)
	if err != nil {
		t.Fatalf("external backup: %v", err)
	}
	if external.Path == "" || external.SizeBytes <= 0 {
		t.Fatalf("bad external backup: %+v", external)
	}
	if _, err := os.Stat(filepath.Join(ownedBackupRoot, "YaverBackups", "todo-demo", "latest.bundle")); err != nil {
		t.Fatalf("latest external backup missing: %v", err)
	}
}
