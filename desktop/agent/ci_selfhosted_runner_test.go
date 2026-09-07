package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestGithubActionsUpstreamCents(t *testing.T) {
	cases := []struct {
		os   string
		min  float64
		want int
	}{
		{"linux", 100, 80},   // 100 * 0.8
		{"macos", 25, 200},   // 25 * 8.0
		{"windows", 10, 16},  // 10 * 1.6
		{"unknown", 100, 80}, // falls back to linux
		{"linux", 0.1, 1},    // ceil(0.08) = 1
		{"MacOS", 1, 8},      // case-insensitive
	}
	for _, c := range cases {
		if got := githubActionsUpstreamCents(c.os, c.min); got != c.want {
			t.Errorf("upstream(%s,%v)=%d want %d", c.os, c.min, got, c.want)
		}
	}
}

func TestCICogsCentsPerMin(t *testing.T) {
	if v := ciCogsCentsPerMin(CIWhereOwn, "linux"); v != 0 {
		t.Errorf("own hardware must be free, got %v", v)
	}
	if v := ciCogsCentsPerMin(CIWhereOperator, "macos"); v != 0 {
		t.Errorf("operator fleet must be free, got %v", v)
	}
	if v := ciCogsCentsPerMin(CIWhereCloud, "linux"); v <= 0 {
		t.Errorf("cloud linux must cost > 0, got %v", v)
	}
	if mac, lin := ciCogsCentsPerMin(CIWhereCloud, "macos"), ciCogsCentsPerMin(CIWhereCloud, "linux"); mac <= lin {
		t.Errorf("cloud mac (%v) must cost more than cloud linux (%v)", mac, lin)
	}
}

func TestGithubRunnerDownloadURL(t *testing.T) {
	cases := []struct {
		goos, goarch string
		wantSub      string
		wantErr      bool
	}{
		{"linux", "amd64", "actions-runner-linux-x64-2.321.0.tar.gz", false},
		{"linux", "arm64", "actions-runner-linux-arm64-2.321.0.tar.gz", false},
		{"darwin", "arm64", "actions-runner-osx-arm64-2.321.0.tar.gz", false},
		{"windows", "amd64", "actions-runner-win-x64-2.321.0.zip", false},
		{"plan9", "amd64", "", true},
		{"linux", "mips", "", true},
	}
	for _, c := range cases {
		got, err := githubRunnerDownloadURL("2.321.0", c.goos, c.goarch)
		if c.wantErr {
			if err == nil {
				t.Errorf("expected error for %s/%s", c.goos, c.goarch)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s/%s: unexpected error %v", c.goos, c.goarch, err)
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("%s/%s url %q missing %q", c.goos, c.goarch, got, c.wantSub)
		}
		if !strings.HasPrefix(got, "https://github.com/actions/runner/releases/download/v2.321.0/") {
			t.Errorf("unexpected url base: %q", got)
		}
	}
}

func TestGithubRegistrationTokenURL(t *testing.T) {
	if got := githubRegistrationTokenURL("", "repo", "owner/repo"); got != "https://api.github.com/repos/owner/repo/actions/runners/registration-token" {
		t.Errorf("repo url wrong: %s", got)
	}
	if got := githubRegistrationTokenURL("", "org", "acme"); got != "https://api.github.com/orgs/acme/actions/runners/registration-token" {
		t.Errorf("org url wrong: %s", got)
	}
	if got := githubRegistrationTokenURL("github.example.com", "repo", "o/r"); !strings.HasPrefix(got, "https://github.example.com/api/v3/repos/o/r/") {
		t.Errorf("GHES url wrong: %s", got)
	}
}

func TestRunnerLabelsDedup(t *testing.T) {
	r := CIRunnerRegistration{Provider: CIGitHub, Target: "o/r", Labels: []string{"yaver", "gpu", "gpu", ""}}
	labels := r.runnerLabels()
	seen := map[string]int{}
	for _, l := range labels {
		seen[l]++
	}
	if seen["self-hosted"] != 1 || seen["yaver"] != 1 {
		t.Errorf("missing/duplicate base labels: %v", labels)
	}
	if seen["gpu"] != 1 {
		t.Errorf("duplicate gpu label not deduped: %v", labels)
	}
	for _, l := range labels {
		if l == "" {
			t.Errorf("empty label leaked: %v", labels)
		}
	}
}

func TestForgeURL(t *testing.T) {
	if got := (CIRunnerRegistration{Provider: CIGitHub, Target: "o/r"}).forgeURL(); got != "https://github.com/o/r" {
		t.Errorf("github forgeURL: %s", got)
	}
	if got := (CIRunnerRegistration{Provider: CIGitHub, Host: "ghe.example.com", Target: "o/r"}).forgeURL(); got != "https://ghe.example.com/o/r" {
		t.Errorf("GHES forgeURL: %s", got)
	}
	if got := (CIRunnerRegistration{Provider: CIGitLab, Target: "123"}).forgeURL(); got != "https://gitlab.com" {
		t.Errorf("gitlab forgeURL: %s", got)
	}
}

func TestCIRegistrationStoreInMemory(t *testing.T) {
	s := &CIRegistrationStore{regs: map[string]*CIRunnerRegistration{}} // path "" → no disk
	stored, err := s.Add(CIRunnerRegistration{Provider: CIGitHub, Target: "o/r"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if stored.Isolation != CIIsolationContainer {
		t.Errorf("default isolation should be container, got %s", stored.Isolation)
	}
	if !stored.PrivateOnly {
		t.Errorf("default must be private-only")
	}
	if stored.MaxConcurrent != 1 {
		t.Errorf("default maxConcurrent should be 1, got %d", stored.MaxConcurrent)
	}
	if stored.key() != "github:o/r" {
		t.Errorf("key wrong: %s", stored.key())
	}
	if len(s.List()) != 1 {
		t.Errorf("expected 1 registration")
	}
	if _, ok := s.Get("github:o/r"); !ok {
		t.Errorf("get miss")
	}
	if err := s.Remove("github:o/r"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if len(s.List()) != 0 {
		t.Errorf("expected 0 after remove")
	}
	if err := s.Remove("nope:x"); err == nil {
		t.Errorf("remove of missing key should error")
	}

	// Validation.
	if _, err := s.Add(CIRunnerRegistration{Target: "o/r"}); err == nil {
		t.Errorf("missing provider should error")
	}
	if _, err := s.Add(CIRunnerRegistration{Provider: CIGitHub}); err == nil {
		t.Errorf("missing target should error")
	}
	if _, err := s.Add(CIRunnerRegistration{Provider: CIGitLab, Target: "group/project", Scope: "org"}); err == nil {
		t.Errorf("gitlab group/org runner should be refused until group safety is implemented")
	}
	if _, err := s.Add(CIRunnerRegistration{Provider: CIGitHub, Target: "o/r", MaxConcurrent: 2}); err == nil {
		t.Errorf("maxConcurrent > 1 must not be accepted while the supervisor is single-worker")
	}
	if _, err := s.Add(CIRunnerRegistration{Provider: CIGitHub, Target: "o/r", Isolation: CIIsolationHost, Where: CIWhereOperator}); err == nil {
		t.Errorf("operator-fleet host execution must be refused")
	}
}

func TestCIRegistrationStoreDoesNotReportUnpersistedRegistration(t *testing.T) {
	store := &CIRegistrationStore{
		regs: map[string]*CIRunnerRegistration{},
		path: filepath.Join(t.TempDir(), "missing-parent", "ci-registrations.json"),
	}
	if _, err := store.Add(CIRunnerRegistration{Provider: CIGitHub, Target: "owner/repo"}); err == nil {
		t.Fatal("registration should fail when its durable record cannot be written")
	}
	if len(store.List()) != 0 {
		t.Fatal("failed persistence left a memory-only registration behind")
	}
}

func TestRequirePrivateCIProject(t *testing.T) {
	private := ciForgeProject{FullName: "group/private", Visibility: "private"}
	if err := requirePrivateCIProject(CIGitLab, private); err != nil {
		t.Fatalf("private project rejected: %v", err)
	}
	for _, visibility := range []string{"public", "internal", ""} {
		project := ciForgeProject{FullName: "group/not-private", Visibility: visibility}
		if err := requirePrivateCIProject(CIGitLab, project); err == nil {
			t.Errorf("visibility %q should be refused", visibility)
		}
	}
}

func TestValidateCIRunnerDiskCarriesRecoveryRoute(t *testing.T) {
	fs := diskGuardFS{Path: "/runner-volume", FreeBytes: ciRunnerMinFreeBytes - 1}
	err := validateCIRunnerDisk(fs)
	if err == nil {
		t.Fatal("low runner volume must fail before a forge record is created")
	}
	var failure *ciPreflightFailure
	if !errors.As(err, &failure) {
		t.Fatalf("low-disk error is not structured: %T", err)
	}
	if failure.Code != "ci_runner_insufficient_disk" {
		t.Fatalf("failure code=%q", failure.Code)
	}
	fix, _ := failure.Initial["fix"].(map[string]interface{})
	if fix["opsVerb"] != "storage_scan" {
		t.Fatalf("low-disk failure has no invocable storage route: %#v", failure.Initial)
	}
	if err := validateCIRunnerDisk(diskGuardFS{FreeBytes: ciRunnerMinFreeBytes}); err != nil {
		t.Fatalf("exact disk floor rejected: %v", err)
	}
}

func TestGitLabProtectedRunnerLeaseLifecycle(t *testing.T) {
	var created, deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v4/projects/"):
			if !strings.Contains(r.RequestURI, "group%2Fproject") {
				t.Errorf("project path was not URL encoded: %s", r.RequestURI)
			}
			if r.Header.Get("PRIVATE-TOKEN") != "access-token" {
				t.Errorf("project probe missing private token")
			}
			_, _ = w.Write([]byte(`{"id":42,"path_with_namespace":"group/project","visibility":"private","web_url":"https://gitlab.example/group/project"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/user/runners":
			if r.Header.Get("PRIVATE-TOKEN") != "access-token" {
				t.Errorf("runner creation missing private token")
			}
			_ = r.ParseForm()
			want := map[string]string{
				"runner_type":  "project_type",
				"project_id":   "42",
				"paused":       "false",
				"locked":       "true",
				"run_untagged": "false",
				"access_level": "ref_protected",
			}
			for key, value := range want {
				if got := r.Form.Get(key); got != value {
					t.Errorf("runner create %s=%q, want %q", key, got, value)
				}
			}
			if tags := r.Form.Get("tag_list"); !strings.Contains(tags, "self-hosted") || !strings.Contains(tags, "yaver") {
				t.Errorf("runner tags missing mandatory selectors: %q", tags)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":99,"token":"glrt-one-use"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v4/runners":
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			if form.Get("token") != "glrt-one-use" {
				t.Errorf("cleanup token mismatch")
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	project, err := fetchGitLabProjectAt(context.Background(), srv.URL+"/api/v4", "access-token", "group/project")
	if err != nil {
		t.Fatalf("project probe: %v", err)
	}
	lease, err := createGitLabRunnerLeaseAt(context.Background(), srv.URL+"/api/v4", CIRunnerRegistration{
		Provider: CIGitLab,
		Target:   "group/project",
		Labels:   []string{"ios"},
	}, project, "access-token")
	if err != nil {
		t.Fatalf("create lease: %v", err)
	}
	if lease.Token != "glrt-one-use" || !created {
		t.Fatalf("runner lease not created correctly")
	}
	if err := lease.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup lease: %v", err)
	}
	if !deleted {
		t.Fatalf("runner record was not deleted")
	}
}

func TestGitHubRunnerCleanupDeletesOnlyExactName(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("cleanup request missing bearer token")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/project/actions/runners":
			_, _ = w.Write([]byte(`{"total_count":2,"runners":[{"id":11,"name":"yaver-someone-else"},{"id":22,"name":"yaver-exact"}]}`))
		case r.Method == http.MethodDelete:
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := deleteGitHubRunnerByNameAt(context.Background(), srv.URL+"/repos/owner/project", "access-token", "yaver-exact"); err != nil {
		t.Fatalf("cleanup exact runner: %v", err)
	}
	if deletedPath != "/repos/owner/project/actions/runners/22" {
		t.Fatalf("deleted %q, want only exact runner id 22", deletedPath)
	}
}

func TestCISupervisorStopCancelsWaitingLoop(t *testing.T) {
	reg := CIRunnerRegistration{Provider: CIGitHub, Target: "owner/repo", MaxConcurrent: 1, PrivateOnly: true}
	limiter := newRunnerLimiter()
	key := "ci:" + reg.key()
	if !limiter.tryAcquire(key, 1) {
		t.Fatal("failed to occupy test limiter")
	}
	defer limiter.release(key)

	sv := NewCISupervisor(reg, NewRunnerStore(1), limiter, nil, nil)
	go sv.Run(context.Background())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sv.Status().State != "waiting_for_slot" {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sv.Status().State; got != "waiting_for_slot" {
		t.Fatalf("supervisor state=%q, want waiting_for_slot", got)
	}
	done := make(chan struct{})
	go func() {
		sv.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the waiting supervisor")
	}
	if got := sv.Status().State; got != "stopped" {
		t.Fatalf("supervisor state after Stop=%q", got)
	}
}

func TestFetchRegistrationToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"bad creds"}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"RTREGTOKEN","expires_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	tok, err := fetchRegistrationToken(context.Background(), srv.URL, "Authorization", "Bearer tok")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if tok != "RTREGTOKEN" {
		t.Errorf("token = %q", tok)
	}

	if _, err := fetchRegistrationToken(context.Background(), srv.URL, "Authorization", "Bearer wrong"); err == nil {
		t.Errorf("expected error on 401")
	}
}

func TestGhRunnerConfigArgs(t *testing.T) {
	args := ghRunnerConfigArgs("https://github.com/o/r", "tok", "yaver-abc", "self-hosted,yaver", "/work")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--url https://github.com/o/r", "--token tok", "--ephemeral", "--unattended", "--labels self-hosted,yaver", "--work /work"} {
		if !strings.Contains(joined, want) {
			t.Errorf("config args missing %q: %v", want, args)
		}
	}
}

func TestIsNumeric(t *testing.T) {
	for _, ok := range []string{"1", "123", " 42 "} {
		if !isNumeric(ok) {
			t.Errorf("%q should be numeric", ok)
		}
	}
	for _, bad := range []string{"", "o/r", "12a", "gitlab.com"} {
		if isNumeric(bad) {
			t.Errorf("%q should not be numeric", bad)
		}
	}
}

func TestCIShellJoin(t *testing.T) {
	got := ciShellJoin([]string{"a", "b c", "it's"})
	if got != `'a' 'b c' 'it'\''s'` {
		t.Errorf("shellJoin = %q", got)
	}
}

func TestScaffoldCIWorkflow(t *testing.T) {
	// Preview (no write) for every catalog target.
	for _, target := range ciWorkflowTargets() {
		rel, content, _, err := scaffoldCIWorkflow(target, "", false, false)
		if err != nil {
			t.Fatalf("preview %s: %v", target, err)
		}
		if !strings.Contains(rel, ".github/workflows/") {
			t.Errorf("%s path wrong: %s", target, rel)
		}
		if !strings.Contains(content, "self-hosted, yaver") {
			t.Errorf("%s yaml missing self-hosted runs-on:\n%s", target, content)
		}
	}

	// GitLab is a first-class scaffold, not a GitHub file with a different
	// extension. Each fragment is valid YAML and carries tags that match the
	// protected project runner registration.
	for _, target := range ciWorkflowTargets() {
		rel, content, _, err := scaffoldCIWorkflowFor(CIGitLab, target, "", false, false)
		if err != nil {
			t.Fatalf("gitlab preview %s: %v", target, err)
		}
		if !strings.HasPrefix(filepath.ToSlash(rel), ".gitlab/") {
			t.Errorf("gitlab %s path wrong: %s", target, rel)
		}
		if !strings.Contains(content, "tags: [self-hosted, yaver") {
			t.Errorf("gitlab %s missing runner tags:\n%s", target, content)
		}
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
			t.Errorf("gitlab %s invalid YAML: %v", target, err)
		}
	}
	_, gitlabTF, _, _ := scaffoldCIWorkflowFor(CIGitLab, "testflight", "", false, false)
	if !strings.Contains(gitlabTF, `yaver publish ios --path "$CI_PROJECT_DIR"`) || !strings.Contains(gitlabTF, "when: manual") {
		t.Errorf("gitlab TestFlight must use the canonical Yaver publish facade behind a manual gate:\n%s", gitlabTF)
	}

	// TestFlight pins os:darwin + the ASC secrets.
	_, tf, secrets, _ := scaffoldCIWorkflow("testflight", "", false, false)
	if !strings.Contains(tf, "os:darwin") {
		t.Errorf("testflight must target os:darwin")
	}
	hasKeyID := false
	for _, s := range secrets {
		if s == "APP_STORE_CONNECT_KEY_ID" {
			hasKeyID = true
		}
	}
	if !hasKeyID {
		t.Errorf("testflight secrets missing APP_STORE_CONNECT_KEY_ID: %v", secrets)
	}

	if _, _, _, err := scaffoldCIWorkflow("bogus", "", false, false); err == nil {
		t.Errorf("unknown target should error")
	}

	// Write + no-clobber + overwrite.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel, _, _, err := scaffoldCIWorkflow("npm", dir, true, false)
	if err != nil {
		t.Fatalf("write npm: %v", err)
	}
	if !fileExistsCI(dir + "/" + rel) {
		t.Errorf("workflow not written")
	}
	if _, _, _, err := scaffoldCIWorkflow("npm", dir, true, false); err == nil {
		t.Errorf("second write without overwrite should refuse")
	}
	if _, _, _, err := scaffoldCIWorkflow("npm", dir, true, true); err != nil {
		t.Errorf("overwrite should succeed: %v", err)
	}
	if _, _, _, err := scaffoldCIWorkflow("npm", ".", true, false); err == nil {
		t.Error("relative workDir must never fall back to the agent daemon's CWD")
	}
}

func TestGitlabRunnerDownloadURL(t *testing.T) {
	cases := []struct {
		goos, goarch, wantSub string
		wantErr               bool
	}{
		{"linux", "amd64", "gitlab-runner-linux-amd64", false},
		{"darwin", "arm64", "gitlab-runner-darwin-arm64", false},
		{"windows", "amd64", "gitlab-runner-windows-amd64.exe", false},
		{"plan9", "amd64", "", true},
		{"linux", "mips", "", true},
	}
	for _, c := range cases {
		got, err := gitlabRunnerDownloadURL("latest", c.goos, c.goarch)
		if c.wantErr {
			if err == nil {
				t.Errorf("expected error for %s/%s", c.goos, c.goarch)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s/%s: %v", c.goos, c.goarch, err)
			continue
		}
		if !strings.HasSuffix(got, c.wantSub) {
			t.Errorf("%s/%s url %q missing suffix %q", c.goos, c.goarch, got, c.wantSub)
		}
		if !strings.HasPrefix(got, "https://gitlab-runner-downloads.s3.amazonaws.com/latest/binaries/") {
			t.Errorf("unexpected base: %q", got)
		}
	}
}

func TestGitlabRunnerRunArgs(t *testing.T) {
	shell := strings.Join(gitlabRunnerRunArgs("https://gitlab.com", "tok", "shell", "alpine:latest"), " ")
	for _, want := range []string{"run-single", "--url https://gitlab.com", "--token tok", "--executor shell", "--max-builds 1"} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell args missing %q: %s", want, shell)
		}
	}
	if strings.Contains(shell, "--docker-image") {
		t.Errorf("shell executor must not pass --docker-image: %s", shell)
	}
	if strings.Contains(shell, "--wait-timeout") {
		t.Errorf("one-shot runner must wait for a real job instead of timing out into a false run: %s", shell)
	}
	docker := strings.Join(gitlabRunnerRunArgs("https://gitlab.com", "tok", "docker", "alpine:latest"), " ")
	if !strings.Contains(docker, "--docker-image alpine:latest") {
		t.Errorf("docker executor must pass --docker-image: %s", docker)
	}
}

func TestFetchGitLabRunnerChecksum(t *testing.T) {
	asset := "gitlab-runner-darwin-arm64"
	hash := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hash + "  binaries/" + asset + "\n"))
	}))
	defer srv.Close()
	got, err := fetchGitLabRunnerChecksum(context.Background(), srv.URL, asset)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	if got != hash {
		t.Fatalf("checksum=%q want %q", got, hash)
	}
	if _, err := fetchGitLabRunnerChecksum(context.Background(), srv.URL, "missing"); err == nil {
		t.Fatal("missing checksum entry should fail closed")
	}
}

func TestGitlabExecutorFor(t *testing.T) {
	if gitlabExecutorFor(CIIsolationContainer) != "docker" {
		t.Errorf("container → docker")
	}
	if gitlabExecutorFor(CIIsolationHost) != "shell" {
		t.Errorf("host → shell")
	}
}

func TestCIDockerHardeningArgs(t *testing.T) {
	args := strings.Join(ciDockerHardeningArgs(), " ")
	for _, want := range []string{"--rm", "--cap-drop ALL", "--security-opt no-new-privileges", "--pids-limit", "--memory"} {
		if !strings.Contains(args, want) {
			t.Errorf("hardening args missing %q: %s", want, args)
		}
	}
	if strings.Contains(args, "--network") {
		t.Errorf("no jail network should be set by default: %s", args)
	}
	// When the operator-fleet jail network is configured, containers join it.
	t.Setenv("YAVER_CI_JAIL_NETWORK", "yaver-ci-jail")
	if !strings.Contains(strings.Join(ciDockerHardeningArgs(), " "), "--network yaver-ci-jail") {
		t.Errorf("jail network not applied when YAVER_CI_JAIL_NETWORK set")
	}
}

func TestCIMeterUnit(t *testing.T) {
	if ciMeterUnit("macos") != "mac-min" {
		t.Errorf("macos unit wrong")
	}
	if ciMeterUnit("linux") != "cpu-min" {
		t.Errorf("linux unit wrong")
	}
}
