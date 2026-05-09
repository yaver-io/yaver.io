package main

// git_provider_cli.go — single-source detection of `gh` and `glab`
// CLIs. Two callers care about this:
//
//   1. The agent itself, which wants to:
//      - Log availability + auth state at boot.
//      - Surface in /info so other surfaces (mobile, web) can show
//        "GitHub CLI ready" indicators.
//      - Inject a one-line hint into the runner-task preamble so
//        Claude/Codex don't waste a turn asking how to authenticate
//        when gh/glab are already authed and on PATH.
//
//   2. MCP tools that wrap these CLIs (gh_run, glab_run,
//      github_pr_create, gitlab_mr_create, …). They need to (a) bail
//      with a clear error when the CLI isn't installed and (b) bail
//      faster when the CLI is installed but not authenticated, since
//      `gh pr create` against an unauthed gh just hangs interactively.
//
// Detection is cheap (`exec.LookPath` + a single `--version` /
// `auth status` shell-out) and runs once at boot + cached. Refreshing
// is done explicitly via RefreshGitProviderCLIs() — wired into the
// install command so `yaver install glab` immediately surfaces the
// new CLI in /info without restarting serve.

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// GitProviderCLI captures one git-provider CLI's discoverability +
// auth posture. Both gh and glab populate this shape.
type GitProviderCLI struct {
	Name      string `json:"name"`        // "gh" or "glab"
	Available bool   `json:"available"`   // resolved on PATH
	Path      string `json:"path"`        // absolute, "" when missing
	Version   string `json:"version"`     // first line of --version, trimmed
	Authed    bool   `json:"authed"`      // `gh auth status` / `glab auth status` exit zero
	AuthUser  string `json:"authUser"`    // best-effort parse from auth status
	AuthHost  string `json:"authHost"`    // github.com / gitlab.com
	CheckedAt int64  `json:"checkedAt"`   // unix seconds
}

var (
	gitProviderCLIMu     sync.RWMutex
	gitProviderCLICache  = map[string]GitProviderCLI{}
	gitProviderCLIScanAt time.Time
)

// DetectGitProviderCLIs probes gh + glab and caches the result.
// Idempotent + safe to call concurrently. Cache is invalidated by
// RefreshGitProviderCLIs.
func DetectGitProviderCLIs() map[string]GitProviderCLI {
	gitProviderCLIMu.RLock()
	if !gitProviderCLIScanAt.IsZero() && time.Since(gitProviderCLIScanAt) < 10*time.Minute {
		out := make(map[string]GitProviderCLI, len(gitProviderCLICache))
		for k, v := range gitProviderCLICache {
			out[k] = v
		}
		gitProviderCLIMu.RUnlock()
		return out
	}
	gitProviderCLIMu.RUnlock()
	return RefreshGitProviderCLIs()
}

// RefreshGitProviderCLIs forces a fresh probe of gh + glab. Called
// from install_cmd after a successful `yaver install gh|glab` so the
// new state shows up in /info immediately.
func RefreshGitProviderCLIs() map[string]GitProviderCLI {
	gh := probeGitProviderCLI("gh", "github.com")
	glab := probeGitProviderCLI("glab", "gitlab.com")
	gitProviderCLIMu.Lock()
	gitProviderCLICache = map[string]GitProviderCLI{"gh": gh, "glab": glab}
	gitProviderCLIScanAt = time.Now()
	gitProviderCLIMu.Unlock()
	out := map[string]GitProviderCLI{"gh": gh, "glab": glab}
	return out
}

func probeGitProviderCLI(name, defaultHost string) GitProviderCLI {
	cli := GitProviderCLI{Name: name, AuthHost: defaultHost, CheckedAt: time.Now().Unix()}
	path, err := exec.LookPath(name)
	if err != nil {
		return cli
	}
	cli.Available = true
	cli.Path = path

	versionCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if v, err := exec.CommandContext(versionCtx, name, "--version").Output(); err == nil {
		first := strings.SplitN(strings.TrimSpace(string(v)), "\n", 2)[0]
		cli.Version = strings.TrimSpace(first)
	}
	cancel()

	authCtx, cancelAuth := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelAuth()
	authOut, authErr := exec.CommandContext(authCtx, name, "auth", "status").CombinedOutput()
	if authErr == nil {
		cli.Authed = true
	}
	// Parse "Logged in to github.com as <user>" / glab equivalent.
	// Both CLIs print a similar line; best-effort. Always set when
	// we can find it, even if Authed is false (helps debugging
	// "wrong account" cases).
	for _, line := range strings.Split(string(authOut), "\n") {
		l := strings.TrimSpace(line)
		if idx := strings.Index(l, "Logged in to "); idx >= 0 {
			rest := l[idx+len("Logged in to "):]
			fields := strings.Fields(rest)
			if len(fields) >= 1 {
				cli.AuthHost = strings.TrimRight(fields[0], ":")
			}
			if asIdx := strings.Index(rest, " as "); asIdx >= 0 {
				userPart := strings.TrimSpace(rest[asIdx+len(" as "):])
				userPart = strings.TrimSuffix(userPart, ".")
				if i := strings.Index(userPart, " "); i > 0 {
					userPart = userPart[:i]
				}
				cli.AuthUser = userPart
			}
			break
		}
	}
	return cli
}

// gitProviderCLIPreambleHint returns a one-line message to inject
// into the runner-task preamble when gh and/or glab are present and
// authed. Empty string when neither is usable, so the caller can
// skip injection cleanly.
func gitProviderCLIPreambleHint() string {
	clis := DetectGitProviderCLIs()
	parts := []string{}
	if c, ok := clis["gh"]; ok && c.Available && c.Authed {
		who := c.AuthUser
		if who == "" {
			who = "the configured user"
		}
		parts = append(parts, "GitHub CLI (`gh`) is installed and authenticated as "+who+" — use it directly for repo/PR/issue/workflow ops instead of asking for a token")
	} else if c, ok := clis["gh"]; ok && c.Available && !c.Authed {
		parts = append(parts, "GitHub CLI (`gh`) is installed but NOT authenticated — run `gh auth login` before using it")
	}
	if c, ok := clis["glab"]; ok && c.Available && c.Authed {
		who := c.AuthUser
		if who == "" {
			who = "the configured user"
		}
		parts = append(parts, "GitLab CLI (`glab`) is installed and authenticated as "+who+" — use it directly for MR/issue/CI ops")
	} else if c, ok := clis["glab"]; ok && c.Available && !c.Authed {
		parts = append(parts, "GitLab CLI (`glab`) is installed but NOT authenticated — run `glab auth login` before using it")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ". ") + "."
}
