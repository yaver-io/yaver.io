package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
}

func TestCIMeterUnit(t *testing.T) {
	if ciMeterUnit("macos") != "mac-min" {
		t.Errorf("macos unit wrong")
	}
	if ciMeterUnit("linux") != "cpu-min" {
		t.Errorf("linux unit wrong")
	}
}
