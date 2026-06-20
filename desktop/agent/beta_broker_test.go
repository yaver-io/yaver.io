package main

import (
	"strings"
	"testing"
)

// recordingGitRunner captures every git invocation so tests can assert the
// command sequence AND the credential-isolation invariant.
type gitCall struct {
	dir     string
	env     []string
	args    []string
}
type recordingGitRunner struct {
	calls []gitCall
	head  string
}

func (r *recordingGitRunner) git(dir string, extraEnv []string, args ...string) (string, error) {
	r.calls = append(r.calls, gitCall{dir: dir, env: extraEnv, args: args})
	if len(args) > 0 && args[0] == "rev-parse" {
		return r.head + "\n", nil
	}
	return "", nil
}

func TestBetaBranchNameAlwaysBeta(t *testing.T) {
	cases := map[string]string{
		"abc123":            "beta/abc123/1700",
		"Dogukan Sahinbas":  "beta/dogukan-sahinbas/1700",
		"../../main":        "beta/main/1700",
		"":                  "beta/anon/1700",
		"UPPER_under":       "beta/upper-under/1700",
	}
	for in, want := range cases {
		if got := betaBranchName(in, 1700); got != want {
			t.Errorf("betaBranchName(%q) = %q, want %q", in, got, want)
		}
		if !strings.HasPrefix(betaBranchName(in, 1700), "beta/") {
			t.Errorf("branch for %q is not under beta/", in)
		}
	}
}

func TestBrokerCredentialIsolation(t *testing.T) {
	rec := &recordingGitRunner{head: "deadbeef"}
	cred := []string{"GIT_ASKPASS=/opt/owner-cred-helper"}
	b := &BetaPushBroker{
		UserID:    "tester1",
		Project:   "sfmg",
		TenantDir: "/srv/yaver/tenants/tester1/sfmg",
		MirrorDir: "/srv/yaver/broker/tester1/sfmg.mirror",
		CredEnv:   cred,
		Runner:    rec,
	}
	branch, sha, err := b.Push(1700)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "beta/tester1/1700" {
		t.Errorf("branch = %q", branch)
	}
	if sha != "deadbeef" {
		t.Errorf("sha = %q", sha)
	}

	// THE invariant: the credential env must NEVER appear on a command run
	// in the tenant dir, and must appear ONLY on the mirror push.
	pushSeen := false
	for _, c := range rec.calls {
		hasCred := false
		for _, e := range c.env {
			if strings.HasPrefix(e, "GIT_ASKPASS=/opt/owner-cred-helper") {
				hasCred = true
			}
		}
		if c.dir == b.TenantDir && hasCred {
			t.Errorf("LEAK: credential env on a tenant-dir command: %v", c.args)
		}
		if hasCred {
			if c.dir != b.MirrorDir {
				t.Errorf("credential env used outside the mirror dir: %s %v", c.dir, c.args)
			}
			if len(c.args) == 0 || c.args[0] != "push" {
				t.Errorf("credential env used on a non-push command: %v", c.args)
			}
			pushSeen = true
		}
	}
	if !pushSeen {
		t.Error("expected the credential env to be used on exactly the push")
	}

	// The tenant clone must never be asked to push (it has no creds).
	for _, c := range rec.calls {
		if c.dir == b.TenantDir && len(c.args) > 0 && c.args[0] == "push" {
			t.Errorf("tenant dir was asked to push: %v", c.args)
		}
	}
}

func TestBetaConfinePath(t *testing.T) {
	root := "/srv/yaver/tenants/t1"
	ok := []string{"sfmg/src/App.tsx", "a/b/c.ts", ".", "x"}
	for _, p := range ok {
		if _, err := betaConfinePath(root, p); err != nil {
			t.Errorf("expected OK for %q: %v", p, err)
		}
	}
	bad := []string{"../t2/secret", "../../etc/passwd", "/etc/passwd", "sfmg/../../t2/x"}
	for _, p := range bad {
		if _, err := betaConfinePath(root, p); err == nil {
			t.Errorf("expected ESCAPE rejection for %q", p)
		}
	}
}

func TestBetaTenantRunnerEnvNoSecrets(t *testing.T) {
	env := betaTenantRunnerEnv("https://gw.example.dev", "ygw_scoped_token", "/srv/yaver/tenants/t1/sfmg")
	joined := strings.Join(env, "\n")
	// gateway wiring present
	if !strings.Contains(joined, "OPENAI_BASE_URL=https://gw.example.dev/v1") {
		t.Error("missing gateway base url")
	}
	if !strings.Contains(joined, "OPENAI_API_KEY=ygw_scoped_token") {
		t.Error("missing scoped token")
	}
	// no host secrets — the env is an allowlist, so these must be absent
	for _, forbidden := range []string{"HCLOUD_TOKEN", "ZAI_API_KEY", "GLM", "VAULT", "ANTHROPIC", "AWS_", "GITHUB_TOKEN"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("tenant env leaked %q: %s", forbidden, joined)
		}
	}
	// tenant git must not be able to auth a push
	if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") || !strings.Contains(joined, "GIT_ASKPASS=/bin/true") {
		t.Error("tenant git is not push-disabled")
	}
}

func TestBetaTenantManagedGitIsLocalNoCreds(t *testing.T) {
	tenantDir := "/srv/yaver/tenants/t1"
	proj := tenantDir + "/sfmg"

	rec := &recordingGitRunner{head: "cafe"}
	bare, err := BetaEnsureTenantRepo(rec, proj, tenantDir, "sfmg")
	if err != nil {
		t.Fatal(err)
	}
	// Bare repo lives UNDER the tenant partition — never the owner's home.
	if !strings.HasPrefix(bare, tenantDir+"/") {
		t.Errorf("bare repo not under tenant partition: %q", bare)
	}
	if strings.Contains(bare, ".yaver") {
		t.Errorf("bare repo in owner home, not tenant partition: %q", bare)
	}
	// origin must be set to the local bare path.
	originSet := false
	for _, c := range rec.calls {
		if len(c.args) >= 4 && c.args[0] == "remote" && c.args[1] == "add" && c.args[3] == bare {
			originSet = true
		}
		if c.env != nil {
			t.Errorf("credential env on repo setup (must be local/no-creds): %v", c.args)
		}
	}
	if !originSet {
		t.Error("origin not pointed at the local bare repo")
	}

	// Checkpoint must push with ZERO credentials anywhere.
	rec2 := &recordingGitRunner{head: "f00d"}
	sha, err := BetaTenantCheckpoint(rec2, proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if sha != "f00d" {
		t.Errorf("sha = %q", sha)
	}
	pushed := false
	for _, c := range rec2.calls {
		if c.env != nil {
			t.Errorf("LEAK: credential env on a local-bare push command: %v", c.args)
		}
		if len(c.args) > 0 && c.args[0] == "push" {
			pushed = true
			// pushes to origin HEAD:main — local, no remote URL with a token
			if !betaContains(c.args, "origin") || !betaContains(c.args, "HEAD:main") {
				t.Errorf("unexpected push args: %v", c.args)
			}
		}
	}
	if !pushed {
		t.Error("expected a push to the local bare origin")
	}
}

func betaContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestBetaTenantPhoneRoot(t *testing.T) {
	r := betaTenantPhoneRoot("tester1")
	if !strings.HasPrefix(r, betaTenantRoot+"/") {
		t.Errorf("phone root not under tenant partition: %q", r)
	}
	if !strings.HasSuffix(r, "/phone-projects") {
		t.Errorf("phone root missing phone-projects suffix: %q", r)
	}
	if strings.Contains(r, ".yaver") {
		t.Errorf("phone root in shared owner home, not partition: %q", r)
	}
	// empty/garbage user → "" so the caller falls back to the shared root.
	if betaTenantPhoneRoot("") != "" {
		t.Error("empty user should yield empty (fallback to default root)")
	}
}

func TestBetaTenantUser(t *testing.T) {
	if u := betaTenantUser("Dogukan Sahinbas"); u != "yv-dogukan-saha" && !strings.HasPrefix(u, "yv-") {
		t.Errorf("unexpected tenant user %q", u)
	}
	if u := betaTenantUser("Dogukan Sahinbas"); len(u) > 15 {
		t.Errorf("tenant user too long: %q", u)
	}
}
