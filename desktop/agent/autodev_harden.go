package main

// autodev_harden.go — curated focus prompts for "harden the codebase"
// modes. Used when the user runs `yaver autodev --harden <area>` (or
// the equivalent HTTP / MCP field) without — or alongside — their
// own --prompt. The point is letting a tired user say "harden the
// security overnight" and get a meaningful run, no prompt-engineering.

import "strings"

// autodevHardenPrompt returns the prompt body for a hardening area.
// Empty input or unknown values return "". Plural / synonym aliases
// are accepted so the user doesn't have to remember the exact noun.
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
