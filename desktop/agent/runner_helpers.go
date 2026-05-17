package main

// runner_helpers.go — small shared helpers used by survivors (autoideas,
// autoinit, ai_generator) that originally lived in the deleted autodev
// loop machinery.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shortSHA returns the first 8 chars of a git SHA, or the original
// string if it's already short.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// looksLikeGitRepo reports whether dir contains a .git entry (file
// or directory — both forms are valid git worktrees).
func looksLikeGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

const autodevRefillBatchSize = 5

// splitAgentSpec parses a "<agent>[:<model>]" spec. Recognises Claude
// model aliases (sonnet/opus/haiku) and normalises them to full IDs.
func splitAgentSpec(spec string) (agent, model string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	parts := strings.SplitN(spec, ":", 2)
	agent = strings.ToLower(parts[0])
	if len(parts) == 1 {
		return agent, ""
	}
	model = parts[1]
	if agent == "claude" || agent == "claude-code" {
		switch strings.ToLower(model) {
		case "sonnet":
			model = "claude-sonnet-4-6"
		case "opus":
			model = "claude-opus-4-6"
		case "haiku":
			model = "claude-haiku-4-5"
		}
	}
	return agent, model
}

// splitAutodevArgs splits positional args from flag args at the first
// flag-shaped argument (legacy name preserved for autoideas_cmd.go).
func splitAutodevArgs(args []string) (positional, flags []string) {
	seenFlag := false
	for _, a := range args {
		if !seenFlag && !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		} else {
			seenFlag = true
			flags = append(flags, a)
		}
	}
	return
}

// envOr returns the env var value or fallback when unset/empty.
func envOr(name, def string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	return v
}

// defaultStr returns v unless empty, in which case fallback.
func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// autodevHardenPrompt returns the curated focus prompt for a hardening
// area (security / memory / perf / quality / all). Used by autoideas
// when the user passes --harden.
func autodevHardenPrompt(area string) string {
	switch strings.ToLower(strings.TrimSpace(area)) {
	case "":
		return ""
	case "security", "sec", "secure":
		return "Audit and harden the security posture of this codebase. Focus on: input validation gaps (SQLi, XSS, command injection, path traversal); authentication and session handling; secrets / API keys / tokens being logged or persisted; insecure dependencies (npm audit / go vuln); CSRF / CORS / clickjacking; authorization checks on every protected route and Convex function; rate limiting and abuse protection; webhook signature verification; PII handling. Each kick: pick ONE concrete vulnerability or weakness, write the fix, add a regression test that fails before the fix and passes after, commit."
	case "memory", "mem":
		return "Reduce memory usage and eliminate leaks across this codebase. Focus on: long-lived caches without eviction; large objects retained by closures; React components that don't unsubscribe / clean up effects; FlatList / image lists without windowing; image assets larger than necessary; over-fetched API payloads; native module retain cycles; goroutine / context leaks on the Go side. Each kick: profile or read for ONE concrete leak/waste, fix it, add a measurement (test or before/after note in the commit), commit."
	case "perf", "performance", "speed":
		return "Improve runtime performance across this codebase. Focus on: cold-start time; bundle size; main-thread blocking; render passes (memoization, key churn, prop identity); list virtualization; image loading strategy; Convex / API query batching and cache hits; over-fetching; redundant rerenders; slow Go endpoints. Each kick: pick ONE concrete bottleneck, fix it, capture a before/after metric in the commit message (LCP, FPS, ms, bytes), commit."
	case "quality", "qa", "codequality", "code":
		return "Improve overall codebase quality without changing user-facing behaviour. Focus on: dead code / unused exports; duplicated logic that should be extracted; missing TypeScript types or `any` escapes; missing tests on critical paths; brittle tests using mocks where integration tests would catch real bugs; outdated comments; non-obvious code without a Why comment. Each kick: pick ONE concrete cleanup, do it surgically, add or improve a test if behaviour is now testable, commit."
	case "all", "everything", "codebase", "harden":
		return strings.Join([]string{
			"Run a broad hardening pass across security, memory, performance, and code quality.",
			"Each kick: pick ONE concrete improvement that fits whichever area is most neglected in this codebase right now (use `git log` and the file structure to judge). Prefer breadth over depth — many small wins across the four areas beat one deep refactor in only one area.",
			"For each kick: implement the fix, add a regression test or a measurable note in the commit message, commit.",
			"",
			"The four areas in detail:",
			"  - SECURITY: input validation, auth, secrets, deps, CSRF/CORS, rate limiting, PII handling, webhook signing.",
			"  - MEMORY:   leaks, retain cycles, unbounded caches, oversized assets, missing list virtualization, goroutine leaks.",
			"  - PERFORMANCE: cold-start, bundle size, render passes, query batching, slow endpoints, over-fetching.",
			"  - QUALITY:  dead code, duplication, missing types/tests, brittle mocks, missing Why comments.",
		}, "\n")
	default:
		return ""
	}
}

// extractRefillTitles finds the last JSON string-array in Claude's
// stdout. We scan from the end so any prose preamble is ignored.
func extractRefillTitles(out string) ([]string, error) {
	out = strings.ReplaceAll(out, "```json", "```")
	for {
		idx := strings.LastIndex(out, "[")
		if idx < 0 {
			return nil, fmt.Errorf("no JSON array in output")
		}
		end := strings.Index(out[idx:], "]")
		if end < 0 {
			out = out[:idx]
			continue
		}
		candidate := out[idx : idx+end+1]
		var arr []string
		if err := json.Unmarshal([]byte(candidate), &arr); err == nil && len(arr) > 0 {
			return arr, nil
		}
		out = out[:idx]
	}
}
