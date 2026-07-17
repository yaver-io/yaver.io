package main

// autorun_remote.go — which remote does this checkout actually push to?
//
// Autorun hardcoded "origin" in every fetch and push. That is not this repo's
// convention: CLAUDE.md says "Only one remote here — `github` (HTTPS).
// `branch.main.remote=github`, so plain `git push` works." A clone configured
// the way the repo documents therefore killed autorun instantly, at iteration
// 0, before any runner ran:
//
//	autorun: git fetch origin: exit status 128:
//	fatal: 'origin' does not appear to be a git repository
//
// That is a loop dying because the checkout followed the project's own rules.
// Resolving the name costs one `git config` read, and it is the difference
// between a self-healing loop and one that needs a human to `git remote add
// origin` before it will start.
//
// This is deliberately NOT a fallback to "origin" when resolution fails: a
// wrong remote name produces a confusing "does not appear to be a git
// repository" instead of a sentence naming the real problem. Say what's wrong.

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// autorunRemoteFor reports the remote name a checkout should fetch and push.
//
// Precedence, most-specific first:
//  1. The current branch's configured remote (`branch.<name>.remote`). This is
//     what plain `git push` uses, so honoring it means autorun pushes exactly
//     where the human would.
//  2. `origin`, when it exists — still the common case elsewhere.
//  3. The only remote, when there is exactly one. A clone with one remote has
//     no ambiguity to resolve, whatever it is called.
//
// Anything else is genuinely ambiguous (several remotes, none configured for
// this branch) and returns an error naming them rather than guessing: pushing
// a run's work to the wrong remote is worse than not starting.
func autorunRemoteFor(ctx context.Context, workDir string) (string, error) {
	remotes := autorunRemoteNames(ctx, workDir)
	if len(remotes) == 0 {
		return "", fmt.Errorf("no git remote configured in %s — autorun needs one to fetch and push", workDir)
	}
	has := func(name string) bool {
		for _, r := range remotes {
			if r == name {
				return true
			}
		}
		return false
	}

	if branch := autorunCurrentBranch(ctx, workDir); branch != "" {
		cfg := autorunExec(ctx, "git", []string{"config", "--get", "branch." + branch + ".remote"}, workDir)
		if cfg.Err == nil {
			if name := strings.TrimSpace(cfg.Output); name != "" && has(name) {
				return name, nil
			}
		}
	}
	if has("origin") {
		return "origin", nil
	}
	if len(remotes) == 1 {
		return remotes[0], nil
	}
	return "", fmt.Errorf(
		"cannot tell which remote to use in %s: %s are all configured and the current branch names none of them — set `git config branch.<branch>.remote <name>`",
		workDir, strings.Join(remotes, ", "))
}

// autorunRemoteNames lists configured remotes, sorted for a stable message.
func autorunRemoteNames(ctx context.Context, workDir string) []string {
	res := autorunExec(ctx, "git", []string{"remote"}, workDir)
	if res.Err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(res.Output, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// autorunCurrentBranch returns the checked-out branch, or "" when detached.
func autorunCurrentBranch(ctx context.Context, workDir string) string {
	res := autorunExec(ctx, "git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, workDir)
	if res.Err != nil {
		return ""
	}
	name := strings.TrimSpace(res.Output)
	if name == "" || name == "HEAD" { // detached
		return ""
	}
	return name
}

// autorunRemoteOrOrigin is the forgiving form for paths that cannot fail the
// run outright (best-effort syncs). It keeps the old hardcoded behavior as the
// last resort so a resolution miss degrades to today's behavior rather than to
// a crash — but real callers should prefer autorunRemoteFor and report the
// error.
func autorunRemoteOrOrigin(ctx context.Context, workDir string) string {
	if name, err := autorunRemoteFor(ctx, workDir); err == nil {
		return name
	}
	return "origin"
}
