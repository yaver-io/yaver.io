package main

// beta_broker.go — the TWO-REPO push broker for the invisible beta share.
//
// A beta tenant's opencode runs ARBITRARY code in their partition, so the
// owner's git credential must never be reachable by it. The broker keeps
// two repos:
//
//   TenantDir  — the tenant's working clone. NO credentialed remote, NO
//                credential helper. An in-sandbox `git push` simply fails.
//   MirrorDir  — owner-only (0700, OUTSIDE any tenant partition). Its
//                "origin" carries the owner's git creds. The tenant has no
//                filesystem access here.
//
// On "commit & push": commit in the tenant clone (no creds) → fetch those
// commits into the mirror over a LOCAL path (no creds) → push from the
// mirror to a per-tenant branch `beta/<id>/<ts>`, NEVER main. The owner's
// credential env is applied to EXACTLY ONE call (the mirror push) and never
// to any command run in the tenant dir — an invariant the tests pin.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// betaGitRunner executes a git command in dir with extra env. Real impl
// shells out; tests inject a recorder to assert the command sequence and
// the credential-isolation invariant.
type betaGitRunner interface {
	git(dir string, extraEnv []string, args ...string) (string, error)
}

type execBetaGitRunner struct{}

func (execBetaGitRunner) git(dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// BetaPushBroker pushes a tenant's work to a beta branch using the owner's
// credentials, without ever exposing them to the tenant.
type BetaPushBroker struct {
	UserID    string // beta user id (→ branch namespace)
	Project   string // "sfmg" | "carrotbet" | …
	TenantDir string // tenant working clone — NO creds, untrusted
	MirrorDir string // broker mirror — credentialed origin, owner-only, OUTSIDE tenant partition
	CredEnv   []string // owner git credential env (e.g. GIT_ASKPASS=…); applied ONLY to the mirror push
	Runner    betaGitRunner
}

// gitTenant runs a git command in the tenant clone — NEVER with creds.
func (b *BetaPushBroker) gitTenant(args ...string) (string, error) {
	return b.Runner.git(b.TenantDir, nil, args...)
}

// gitMirror runs a git command in the owner-only mirror; pass withCred=true
// ONLY for the network push.
func (b *BetaPushBroker) gitMirror(withCred bool, args ...string) (string, error) {
	var env []string
	if withCred {
		env = b.CredEnv
	}
	return b.Runner.git(b.MirrorDir, env, args...)
}

// Push commits the tenant working tree and pushes it to beta/<id>/<ts>.
// Returns the branch and commit sha. Refuses any non-beta branch as a
// belt-and-braces guard against ever touching main.
func (b *BetaPushBroker) Push(ts int64) (branch, sha string, err error) {
	branch = betaBranchName(b.UserID, ts)
	if !strings.HasPrefix(branch, "beta/") {
		return "", "", fmt.Errorf("beta broker: refusing non-beta branch %q", branch)
	}

	// 1. Stage + commit in the tenant clone — NO creds. An empty commit
	//    (nothing changed) is tolerated, not an error.
	if _, err = b.gitTenant("add", "-A"); err != nil {
		return "", "", fmt.Errorf("tenant add: %w", err)
	}
	if out, cerr := b.gitTenant(
		"-c", "user.name=Yaver Beta", "-c", "user.email=beta@yaver.io",
		"commit", "-m", "beta: changes from "+b.UserID,
	); cerr != nil && !strings.Contains(out, "nothing to commit") {
		return "", "", fmt.Errorf("tenant commit: %w (%s)", cerr, out)
	}
	rawSha, err := b.gitTenant("rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("tenant rev-parse: %w", err)
	}
	sha = strings.TrimSpace(rawSha)

	// 2. Pull those commits into the mirror over a LOCAL path (no creds),
	//    then push to the beta branch (creds confined to THIS one call).
	if _, err = b.gitMirror(false, "fetch", b.TenantDir, "HEAD"); err != nil {
		return "", "", fmt.Errorf("mirror fetch: %w", err)
	}
	if _, err = b.gitMirror(true, "push", "origin", "FETCH_HEAD:refs/heads/"+branch); err != nil {
		return "", "", fmt.Errorf("mirror push: %w", err)
	}
	return branch, sha, nil
}

// betaBranchName builds the per-tenant push target. ALWAYS under beta/,
// NEVER main. ts (caller-supplied; no time call here) disambiguates pushes.
func betaBranchName(userID string, ts int64) string {
	return fmt.Sprintf("beta/%s/%d", betaSanitizeRef(userID), ts)
}

// betaSanitizeRef makes a git-ref-safe slug: lowercase, [a-z0-9-], other
// runs collapse to '-', trimmed. Empty → "anon".
func betaSanitizeRef(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "anon"
	}
	return out
}
