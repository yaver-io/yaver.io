package main

// forge.go — one provider-neutral seam over GitHub and GitLab.
//
// Why this exists: before it, every forge operation was written twice —
// verifyGitHubToken/verifyGitLabToken, listGitHubRepos/listGitLabRepos,
// addSSHKeyToGitHub/addSSHKeyToGitLab, mcpGhRun/mcpGlabRun (byte-identical
// but for the binary name). Adding an operation meant writing it twice and
// adding a third forge meant touching ~15 call sites. Everything below the
// Forge interface is written once.
//
// Transport is hybrid, and that choice is load-bearing:
//
//   - When `gh`/`glab` is on PATH and authenticated, we shell out to
//     `gh api` / `glab api`. Both accept --hostname/--method/--input -, so
//     one argv shape covers both. This inherits the user's existing auth,
//     their GitHub Enterprise / self-hosted GitLab config, their SSO
//     session, and their proxy settings — none of which we want to
//     reimplement.
//   - Otherwise we speak REST directly using a token from the existing
//     detect chain (gh auth token → env → git credential fill → Yaver's
//     stores). Headless boxes rarely have gh installed; they must still
//     be able to invite a collaborator.
//
// Both transports send the same path and the same JSON body, so a Forge
// implementation never knows which one it got. That is the whole point:
// per-provider code stays about API shape, not about auth or plumbing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Kinds + hosts
// ---------------------------------------------------------------------------

// ForgeKind is the provider family. Deliberately not named "provider": in
// this codebase `provider` already means OAuth login identity, IaaS vendor,
// TTS engine, and LLM vendor depending on the file. "Forge" is unambiguous.
type ForgeKind string

const (
	ForgeGitHub ForgeKind = "github"
	ForgeGitLab ForgeKind = "gitlab"
)

// ForgeHost is a resolved forge endpoint: which family, which API base.
//
// APIBase is the piece that was missing before — every GitHub call was
// hardcoded to api.github.com, so the Host field on GitProvider was a lie
// for GitHub Enterprise (it was only ever used as a map key). Here the host
// actually reaches the URL.
type ForgeHost struct {
	Host    string    `json:"host"`    // github.com, gitlab.com, ghe.acme.com, gitlab.acme.com
	Kind    ForgeKind `json:"kind"`    // github | gitlab
	APIBase string    `json:"apiBase"` // https://api.github.com, https://ghe.acme.com/api/v3, https://gitlab.acme.com/api/v4
	WebBase string    `json:"webBase"` // https://github.com, https://ghe.acme.com
}

// forgeCLIName maps a kind to the CLI that speaks it.
func (k ForgeKind) forgeCLIName() string {
	switch k {
	case ForgeGitHub:
		return "gh"
	case ForgeGitLab:
		return "glab"
	}
	return ""
}

// inferForgeKind guesses the family from a hostname. Explicit config wins;
// this is only the fallback for hosts we've never been told about.
//
// The substring checks are a heuristic and will not catch a GHE install at
// git.acme.com. That case is handled by the git-providers.json lookup in
// resolveForgeHost, which is why this returns ("", false) rather than
// defaulting to GitHub — a wrong guess here would send a GitLab token to a
// GitHub-shaped URL and produce a baffling 404.
func inferForgeKind(host string) (ForgeKind, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	switch h {
	case "github.com", "www.github.com":
		return ForgeGitHub, true
	case "gitlab.com", "www.gitlab.com":
		return ForgeGitLab, true
	}
	if strings.Contains(h, "github") {
		return ForgeGitHub, true
	}
	if strings.Contains(h, "gitlab") {
		return ForgeGitLab, true
	}
	return "", false
}

// resolveForgeHost turns a (host, kind) pair into a usable endpoint.
// Either may be empty: an empty host defaults to the kind's public host,
// an empty kind is inferred from the host or read from git-providers.json.
func resolveForgeHost(host string, kind ForgeKind) (ForgeHost, error) {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host = strings.TrimSuffix(host, "/")

	if host == "" {
		switch kind {
		case ForgeGitHub:
			host = "github.com"
		case ForgeGitLab:
			host = "gitlab.com"
		default:
			return ForgeHost{}, fmt.Errorf("host or kind is required (github|gitlab)")
		}
	}

	if kind == "" {
		if k, ok := inferForgeKind(host); ok {
			kind = k
		} else if p := findProvider(host); p != nil && p.Provider != "" {
			// Configured hosts are authoritative — this is how a GHE at
			// git.acme.com becomes resolvable.
			kind = ForgeKind(p.Provider)
		} else {
			return ForgeHost{}, fmt.Errorf("cannot tell whether %s is GitHub or GitLab — pass kind explicitly, or configure it once via git_provider_setup", host)
		}
	}

	fh := ForgeHost{Host: host, Kind: kind, WebBase: "https://" + host}

	switch kind {
	case ForgeGitHub:
		if host == "github.com" {
			fh.APIBase = "https://api.github.com"
		} else {
			// GitHub Enterprise Server mounts the v3 API under /api/v3.
			fh.APIBase = "https://" + host + "/api/v3"
		}
		// Respect the same env var gh honors, so a box already configured
		// for GHE needs no Yaver-specific setup.
		if v := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); v != "" && host != "github.com" {
			fh.APIBase = strings.TrimSuffix(v, "/")
		}
	case ForgeGitLab:
		fh.APIBase = "https://" + host + "/api/v4"
	default:
		return ForgeHost{}, fmt.Errorf("unsupported forge kind %q (want github or gitlab)", kind)
	}
	return fh, nil
}

// ---------------------------------------------------------------------------
// Repo identity
// ---------------------------------------------------------------------------

// ForgeRepo identifies one repository on one forge.
//
// Path is the full namespace path, which matters for GitLab: a project can
// live at group/subgroup/project, so "owner/name" is not sufficient there.
// GitHub is always exactly owner/name.
type ForgeRepo struct {
	Host ForgeHost `json:"host"`
	Path string    `json:"path"` // "owner/repo" or "group/subgroup/project"
}

// Owner is everything before the final path segment.
func (r ForgeRepo) Owner() string {
	if i := strings.LastIndex(r.Path, "/"); i > 0 {
		return r.Path[:i]
	}
	return ""
}

// Name is the final path segment.
func (r ForgeRepo) Name() string {
	if i := strings.LastIndex(r.Path, "/"); i >= 0 {
		return r.Path[i+1:]
	}
	return r.Path
}

// encodedPath is GitLab's URL-encoded project identifier (group%2Fproject).
func (r ForgeRepo) encodedPath() string { return url.PathEscape(r.Path) }

// parseForgeRepoURL extracts a ForgeRepo from any git remote URL, covering
// the three shapes a remote actually takes in the wild:
//
//	https://github.com/owner/repo.git
//	git@github.com:owner/repo.git
//	ssh://git@gitlab.acme.com:2222/group/sub/repo.git
func parseForgeRepoURL(raw string) (ForgeRepo, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ForgeRepo{}, fmt.Errorf("empty remote URL")
	}
	var host, path string

	switch {
	case strings.HasPrefix(s, "git@"), strings.Contains(s, "@") && !strings.Contains(s, "://"):
		// scp-like: git@host:path
		rest := s[strings.Index(s, "@")+1:]
		i := strings.Index(rest, ":")
		if i < 0 {
			return ForgeRepo{}, fmt.Errorf("cannot parse remote %q", raw)
		}
		host, path = rest[:i], rest[i+1:]
	default:
		u, err := url.Parse(s)
		if err != nil {
			return ForgeRepo{}, fmt.Errorf("cannot parse remote %q: %w", raw, err)
		}
		host, path = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" || !strings.Contains(path, "/") {
		return ForgeRepo{}, fmt.Errorf("remote %q is not a forge repo URL", raw)
	}

	fh, err := resolveForgeHost(host, "")
	if err != nil {
		return ForgeRepo{}, err
	}
	return ForgeRepo{Host: fh, Path: path}, nil
}

// forgeRepoFromDir reads `origin` (or the first remote) out of a working
// tree and resolves it. This is what lets every verb take a directory
// instead of making the caller retype owner/repo they already have on disk.
func forgeRepoFromDir(dir string) (ForgeRepo, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	cmd := osexec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		// No origin — fall back to whatever remote exists.
		lc := osexec.Command("git", "remote")
		lc.Dir = dir
		lout, lerr := lc.Output()
		if lerr != nil {
			return ForgeRepo{}, fmt.Errorf("no git remote in %s (not a repo, or no remote configured)", dir)
		}
		names := strings.Fields(string(lout))
		if len(names) == 0 {
			return ForgeRepo{}, fmt.Errorf("no git remote in %s (not a repo, or no remote configured)", dir)
		}
		c2 := osexec.Command("git", "remote", "get-url", names[0])
		c2.Dir = dir
		out, err = c2.Output()
		if err != nil {
			return ForgeRepo{}, fmt.Errorf("cannot read remote %s in %s: %w", names[0], dir, err)
		}
	}
	return parseForgeRepoURL(strings.TrimSpace(string(out)))
}

// resolveForgeRepo turns loose caller input into a repo. Precedence:
// explicit repo slug > directory > cwd. A slug without a host inherits the
// kind's default host.
func resolveForgeRepo(repoSlug, dir string, kind ForgeKind, host string) (ForgeRepo, error) {
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return forgeRepoFromDir(dir)
	}
	// A full URL is unambiguous — take it as-is.
	if strings.Contains(repoSlug, "://") || strings.HasPrefix(repoSlug, "git@") {
		return parseForgeRepoURL(repoSlug)
	}
	if !strings.Contains(repoSlug, "/") {
		return ForgeRepo{}, fmt.Errorf("repo %q must be owner/name (or a full clone URL)", repoSlug)
	}
	fh, err := resolveForgeHost(host, kind)
	if err != nil {
		return ForgeRepo{}, err
	}
	return ForgeRepo{Host: fh, Path: strings.Trim(repoSlug, "/")}, nil
}

// ---------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------

// ForgeRole is the provider-neutral permission level. GitHub and GitLab
// disagree on both names and granularity, so callers speak these five and
// each implementation maps them to its own vocabulary.
type ForgeRole string

const (
	RoleRead     ForgeRole = "read"
	RoleTriage   ForgeRole = "triage"
	RoleWrite    ForgeRole = "write"
	RoleMaintain ForgeRole = "maintain"
	RoleAdmin    ForgeRole = "admin"
)

// normalizeForgeRole accepts the neutral names plus each provider's native
// spelling, so someone who knows GitHub can say "push" and someone who
// knows GitLab can say "developer" and both land in the same place.
func normalizeForgeRole(s string) (ForgeRole, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "write", "push", "developer":
		return RoleWrite, nil // default: the role people actually mean
	case "read", "pull", "guest", "reporter":
		return RoleRead, nil
	case "triage":
		return RoleTriage, nil
	case "maintain", "maintainer":
		return RoleMaintain, nil
	case "admin", "owner":
		return RoleAdmin, nil
	}
	return "", fmt.Errorf("unknown role %q (want read|triage|write|maintain|admin)", s)
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// ForgeMember is one human with access to a repo.
type ForgeMember struct {
	Username   string    `json:"username"`
	Name       string    `json:"name,omitempty"`
	Role       ForgeRole `json:"role"`
	NativeRole string    `json:"nativeRole,omitempty"` // "push" / "developer" — what the forge calls it
	State      string    `json:"state,omitempty"`      // active | pending (invited, not yet accepted)
	AvatarURL  string    `json:"avatarUrl,omitempty"`
	ProfileURL string    `json:"profileUrl,omitempty"`
	ID         any       `json:"id,omitempty"`
}

// ForgeInvite is the result of inviting someone.
type ForgeInvite struct {
	Username string    `json:"username,omitempty"`
	Email    string    `json:"email,omitempty"`
	Role     ForgeRole `json:"role"`
	State    string    `json:"state"` // invited | added | already_member
	InviteID any       `json:"inviteId,omitempty"`
	URL      string    `json:"url,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

// forgeTransport is the hybrid seam. Both implementations take an API path
// relative to the forge's base and a JSON body, so Forge implementations
// are written once against this and never learn which transport they got.
type forgeTransport interface {
	// do issues a request. path is relative to the API base, e.g.
	// "repos/o/r/collaborators/u". out may be nil to discard the body.
	do(ctx context.Context, method, path string, body any, out any) error
	// name is surfaced in verb results so the user can tell whether an op
	// went through their CLI or our token — the first question worth
	// asking when a call unexpectedly 403s.
	name() string
}

// forgeCLITransport shells out to `gh api` / `glab api`.
type forgeCLITransport struct {
	bin  string // absolute path to gh/glab
	kind ForgeKind
	host string
}

func (t *forgeCLITransport) name() string { return t.bin + " api" }

func (t *forgeCLITransport) do(ctx context.Context, method, path string, body any, out any) error {
	args := []string{"api", "--hostname", t.host, "--method", strings.ToUpper(method)}

	var stdin io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body: %w", err)
		}
		// Both CLIs read a raw JSON body from stdin with --input -.
		args = append(args, "--input", "-")
		stdin = bytes.NewReader(raw)
	}
	args = append(args, path)

	cmd := osexec.CommandContext(ctx, t.bin, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return &forgeAPIError{Status: cliExitStatus(msg), Body: msg, Via: t.name()}
	}
	if out == nil {
		return nil
	}
	raw := bytes.TrimSpace(stdout.Bytes())
	if len(raw) == 0 {
		return nil // 204-equivalent
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse %s response: %w", t.name(), err)
	}
	return nil
}

// cliExitStatus digs the HTTP status out of gh/glab stderr. Both print
// "HTTP 404" / "(HTTP 403)" on failure. Best-effort: callers only use this
// to distinguish "already a member" (422) from real errors, so a miss
// degrades to a generic error rather than wrong behavior.
func cliExitStatus(stderr string) int {
	i := strings.Index(stderr, "HTTP ")
	if i < 0 {
		return 0
	}
	rest := stderr[i+len("HTTP "):]
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if n < 100 || n > 599 {
		return 0
	}
	return n
}

// forgeRESTTransport speaks the API directly with a detected token.
type forgeRESTTransport struct {
	apiBase string
	token   string
	kind    ForgeKind
}

func (t *forgeRESTTransport) name() string { return "rest" }

func (t *forgeRESTTransport) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), t.apiBase+"/"+strings.TrimPrefix(path, "/"), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.kind == ForgeGitHub {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &forgeAPIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw)), Via: "rest"}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// forgeAPIError carries the status so callers can branch on it (e.g. 422
// from GitHub means "already a collaborator", which is success, not failure).
type forgeAPIError struct {
	Status int
	Body   string
	Via    string
}

func (e *forgeAPIError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("forge API %d via %s: %s", e.Status, e.Via, truncateForgeBody(e.Body))
	}
	return fmt.Sprintf("forge API error via %s: %s", e.Via, truncateForgeBody(e.Body))
}

func truncateForgeBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

func forgeErrStatus(err error) int {
	var fe *forgeAPIError
	if errors.As(err, &fe) {
		return fe.Status
	}
	return 0
}

// ---------------------------------------------------------------------------
// Forge interface + construction
// ---------------------------------------------------------------------------

// Forge is the provider-neutral contract. Everything a surface can ask for
// is expressed once here.
type Forge interface {
	Kind() ForgeKind
	Host() ForgeHost
	// Via reports which transport backs this instance ("gh api" | "rest").
	Via() string

	ListMembers(ctx context.Context, repo ForgeRepo) ([]ForgeMember, error)
	InviteMember(ctx context.Context, repo ForgeRepo, userOrEmail string, role ForgeRole) (ForgeInvite, error)
	RemoveMember(ctx context.Context, repo ForgeRepo, username string) error
}

// forgeAuthUnavailableError explains, in one place, every way auth can be
// missing — and what to do about each. This is the error users will hit
// most, so it earns real prose instead of "unauthorized".
type forgeAuthUnavailableError struct {
	host ForgeHost
}

func (e *forgeAuthUnavailableError) Error() string {
	cli := e.host.Kind.forgeCLIName()
	return fmt.Sprintf(
		"no %s auth for %s — either run `%s auth login --hostname %s` (recommended: Yaver will use your CLI session), "+
			"set %s, or connect once via git_provider_oauth_start",
		e.host.Kind, e.host.Host, cli, e.host.Host, forgeTokenEnvHint(e.host.Kind))
}

func forgeTokenEnvHint(k ForgeKind) string {
	if k == ForgeGitLab {
		return "GITLAB_TOKEN"
	}
	return "GITHUB_TOKEN"
}

// newForge builds a Forge for a host, picking the transport.
//
// CLI first: an authed gh/glab already knows about GHE hosts, SSO, and
// proxies. We only fall back to REST when the CLI is absent or unauthed —
// which is the normal state on a headless agent box.
func newForge(fh ForgeHost) (Forge, error) {
	t, err := pickForgeTransport(fh)
	if err != nil {
		return nil, err
	}
	switch fh.Kind {
	case ForgeGitHub:
		return &githubForge{host: fh, t: t}, nil
	case ForgeGitLab:
		return &gitlabForge{host: fh, t: t}, nil
	}
	return nil, fmt.Errorf("unsupported forge kind %q", fh.Kind)
}

// pickForgeTransport implements the hybrid rule. Kept separate from
// newForge so tests can assert the choice without a live forge.
func pickForgeTransport(fh ForgeHost) (forgeTransport, error) {
	name := fh.Kind.forgeCLIName()
	if cli, ok := DetectGitProviderCLIs()[name]; ok && cli.Available && cli.Authed {
		return &forgeCLITransport{bin: cli.Path, kind: fh.Kind, host: fh.Host}, nil
	}
	token := detectForgeToken(fh)
	if token == "" {
		return nil, &forgeAuthUnavailableError{host: fh}
	}
	return &forgeRESTTransport{apiBase: fh.APIBase, token: token, kind: fh.Kind}, nil
}

// detectForgeToken reuses the existing per-provider detect chains rather
// than inventing a third token store.
func detectForgeToken(fh ForgeHost) string {
	switch fh.Kind {
	case ForgeGitHub:
		// detectGitHubToken's chain is github.com-shaped; for GHE prefer a
		// host-specific credential before falling back to it.
		if fh.Host != "github.com" {
			if cred := findCredentialForHost(fh.Host); cred != nil && cred.Token != "" {
				return cred.Token
			}
			if p := findProvider(fh.Host); p != nil && p.Token != "" {
				return p.Token
			}
			if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
				return v
			}
			return ""
		}
		return detectGitHubToken()
	case ForgeGitLab:
		return detectGitLabToken(fh.Host)
	}
	return ""
}

// forgeCallTimeout bounds any single forge call. Forge APIs are fast; a
// hang here would otherwise block a voice/watch surface indefinitely.
const forgeCallTimeout = 30 * time.Second

func forgeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), forgeCallTimeout)
}
