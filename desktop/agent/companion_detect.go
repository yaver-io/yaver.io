package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// companion_detect.go — read-only serverless-project scanner. It proposes a
// yaver.companion.yaml by recognising the always-on tail a serverless platform
// can't run itself: token-authed cron endpoints, missing reconcile sweeps,
// long-running workers. Everything it returns is a PROPOSAL — nothing is armed
// until the user confirms in the UI. It never writes the target repo.

type DetectedItem struct {
	Kind       string  `json:"kind"`   // "cron" | "service" | "note"
	Name       string  `json:"name"`
	Reason     string  `json:"reason"`
	Status     string  `json:"status"` // "detected" | "proposed-missing-endpoint" | "note"
	Endpoint   string  `json:"endpoint,omitempty"`
	Schedule   string  `json:"schedule,omitempty"`
	Confidence float64 `json:"confidence"`
}

// DetectCompanion scans repoDir and returns a proposed manifest + the reasoned
// item list backing it.
func DetectCompanion(repoDir string) (*CompanionManifest, []DetectedItem, error) {
	var items []DetectedItem
	m := &CompanionManifest{
		Version: 1,
		Project: sanitizeCompanionName(filepath.Base(repoDir)),
		repoDir: repoDir,
	}

	supabaseItems, supaCrons := detectSupabaseCrons(repoDir)
	items = append(items, supabaseItems...)
	if len(supaCrons) > 0 {
		m.Runtime = CompanionRuntime{Bind: "device", BaseURLFrom: "env:SUPABASE_FUNCTIONS_URL"}
		m.EnvFrom = []CompanionEnvSource{{Vault: m.Project}}
		m.Crons = append(m.Crons, supaCrons...)
	}

	// Subscription-expiry reconcile: webhook-only billing with no periodic
	// sweep is a silent correctness gap a companion cron should close.
	if recItem, recCron, ok := detectMissingSubscriptionReconcile(repoDir); ok {
		items = append(items, recItem)
		m.Crons = append(m.Crons, recCron)
		if m.Runtime.BaseURLFrom == "" {
			m.Runtime = CompanionRuntime{Bind: "device", BaseURLFrom: "env:SUPABASE_FUNCTIONS_URL"}
			m.EnvFrom = []CompanionEnvSource{{Vault: m.Project}}
		}
	}

	// Convex / Cloudflare already schedule their own crons — note, never
	// double-schedule.
	items = append(items, detectAlreadyScheduledNotes(repoDir)...)

	// Long-running workers declared in package.json.
	if svcItems, svcs := detectWorkerServices(repoDir); len(svcs) > 0 {
		items = append(items, svcItems...)
		m.Services = append(m.Services, svcs...)
	}

	return m, items, nil
}

var (
	reDirectRoute   = regexp.MustCompile(`path\s*===\s*"(/rest/[A-Za-z0-9_]+)"[\s\S]{0,160}?(handle[A-Za-z0-9_]+)\s*\(`)
	reCronHandlerFn = regexp.MustCompile(`function\s+(handle[A-Za-z0-9_]+)`)
)

// detectSupabaseCrons finds token-authed (CRON_AUTH_*) Edge Function endpoints
// that have no scheduler in code. These are the classic "ping me on a timer"
// endpoints (e-back's autoMailSenderDirect / dailySummaryMailDirect).
func detectSupabaseCrons(repoDir string) ([]DetectedItem, []CompanionCron) {
	fnDir := filepath.Join(repoDir, "supabase", "functions")
	if _, err := os.Stat(filepath.Join(repoDir, "supabase", "config.toml")); err != nil {
		return nil, nil
	}
	if _, err := os.Stat(fnDir); err != nil {
		return nil, nil
	}

	// 1. Collect handler names whose BODY checks a CRON_AUTH_* token — those
	//    are the cron-triggerable handlers (not every Direct endpoint).
	cronHandlers := map[string]bool{}
	_ = filepath.Walk(fnDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := string(data)
		if !strings.Contains(src, "CRON_AUTH") {
			return nil
		}
		// For each CRON_AUTH reference, attribute it to the nearest preceding
		// `function handleXxx`.
		for _, loc := range regexp.MustCompile(`CRON_AUTH`).FindAllStringIndex(src, -1) {
			prefix := src[:loc[0]]
			fns := reCronHandlerFn.FindAllStringSubmatch(prefix, -1)
			if len(fns) > 0 {
				cronHandlers[fns[len(fns)-1][1]] = true
			}
		}
		return nil
	})
	if len(cronHandlers) == 0 {
		return nil, nil
	}

	// 2. Map cron handlers to their routes via the function router (index.ts).
	indexSrc := ""
	if data, err := os.ReadFile(filepath.Join(fnDir, "rest", "index.ts")); err == nil {
		indexSrc = string(data)
	} else if data, err := os.ReadFile(filepath.Join(fnDir, "index.ts")); err == nil {
		indexSrc = string(data)
	}

	var items []DetectedItem
	var crons []CompanionCron
	seen := map[string]bool{}
	for _, match := range reDirectRoute.FindAllStringSubmatch(indexSrc, -1) {
		route, handler := match[1], match[2]
		if !cronHandlers[handler] || seen[route] {
			continue
		}
		seen[route] = true
		name := sanitizeCompanionName(strings.TrimPrefix(route, "/rest/"))
		schedule := suggestSchedule(name)
		items = append(items, DetectedItem{
			Kind:   "cron",
			Name:   name,
			Reason: "token-authed Supabase endpoint with no scheduler in code — must be pinged on a timer; a serverless platform won't fire it on its own",
			Status: "detected", Endpoint: route, Schedule: schedule, Confidence: 0.9,
		})
		crons = append(crons, CompanionCron{
			Name: name, Schedule: schedule, Idempotent: true, CompilesTo: "http_request",
			Request: CompanionRequest{
				Method: "POST",
				URL:    "${base_url}" + route + "?token=${CRON_AUTH_UUID}",
			},
		})
	}
	return items, crons
}

// detectMissingSubscriptionReconcile fires when billing state is mutated only
// by webhooks (next_payment_date present, no periodic sweep). A missed webhook
// then silently corrupts billing — a daily reconcile closes the gap.
func detectMissingSubscriptionReconcile(repoDir string) (DetectedItem, CompanionCron, bool) {
	hasNextPayment := repoContains(repoDir, "next_payment_date") || repoContains(repoDir, "nextPaymentDate")
	if !hasNextPayment {
		return DetectedItem{}, CompanionCron{}, false
	}
	// Already has a reconcile/sweep? Then nothing to propose.
	if repoContains(repoDir, "reconcile") || repoContains(repoDir, "subscriptionSweep") || repoContains(repoDir, "expireSubscriptions") {
		return DetectedItem{}, CompanionCron{}, false
	}
	item := DetectedItem{
		Kind:   "cron",
		Name:   "subscription-reconcile",
		Reason: "subscription status is webhook-only (next_payment_date present, no periodic sweep) — a missed webhook silently corrupts billing; propose a daily reconcile. The endpoint must be created.",
		Status: "proposed-missing-endpoint", Endpoint: "/rest/subscriptionReconcileDirect",
		Schedule: "0 3 * * *", Confidence: 0.6,
	}
	cron := CompanionCron{
		Name: "subscription-reconcile", Schedule: "0 3 * * *", Idempotent: true,
		CompilesTo: "http_request", Status: "proposed",
		Request: CompanionRequest{Method: "POST", URL: "${base_url}/rest/subscriptionReconcileDirect?token=${CRON_AUTH_UUID}"},
	}
	return item, cron, true
}

func detectAlreadyScheduledNotes(repoDir string) []DetectedItem {
	var items []DetectedItem
	if _, err := os.Stat(filepath.Join(repoDir, "convex", "crons.ts")); err == nil {
		items = append(items, DetectedItem{
			Kind: "note", Name: "convex-crons", Status: "note", Confidence: 1,
			Reason: "convex/crons.ts already schedules crons on the Convex backend — not duplicated as a companion cron.",
		})
	}
	if data, err := os.ReadFile(filepath.Join(repoDir, "wrangler.toml")); err == nil {
		if strings.Contains(string(data), "[triggers]") && strings.Contains(string(data), "crons") {
			items = append(items, DetectedItem{
				Kind: "note", Name: "cloudflare-cron-triggers", Status: "note", Confidence: 1,
				Reason: "wrangler.toml [triggers] crons are scheduled by Cloudflare — not duplicated as a companion cron.",
			})
		}
	}
	return items
}

var rePkgWorkerScript = regexp.MustCompile(`"(start|worker|server|queue|poller)"\s*:\s*"([^"]+)"`)

// detectWorkerServices flags package.json scripts that look like long-running
// workers (a poller, a queue consumer, a standalone server) rather than a
// serverless build. These need an always-on companion process.
func detectWorkerServices(repoDir string) ([]DetectedItem, []CompanionService) {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return nil, nil
	}
	var items []DetectedItem
	var svcs []CompanionService
	for _, m := range rePkgWorkerScript.FindAllStringSubmatch(string(data), -1) {
		script, cmd := m[1], m[2]
		// Skip obvious dev/serverless front-end servers.
		if strings.Contains(cmd, "next ") || strings.Contains(cmd, "vite") || strings.Contains(cmd, "expo") {
			continue
		}
		items = append(items, DetectedItem{
			Kind: "service", Name: script, Status: "detected", Confidence: 0.5,
			Reason: "package.json \"" + script + "\" looks like a long-running worker; needs an always-on companion process.",
		})
		parts := strings.Fields(cmd)
		svc := CompanionService{Name: script, Durable: true}
		if len(parts) > 0 {
			svc.Command = parts[0]
			svc.Args = parts[1:]
		}
		svcs = append(svcs, svc)
	}
	return items, svcs
}

// suggestSchedule offers a conservative default cadence by endpoint name.
func suggestSchedule(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "daily") || strings.Contains(n, "summary"):
		return "0 8 * * *"
	case strings.Contains(n, "mail") || strings.Contains(n, "sender") || strings.Contains(n, "digest"):
		return "0 9 * * *"
	case strings.Contains(n, "loop") || strings.Contains(n, "poll") || strings.Contains(n, "wake"):
		return "*/15 * * * *"
	default:
		return "0 * * * *"
	}
}

// repoContains greps the repo's source files (bounded) for a substring.
func repoContains(repoDir, needle string) bool {
	found := false
	skip := map[string]bool{"node_modules": true, ".git": true, "dist": true, "build": true, ".next": true}
	_ = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return nil
		}
		if info.IsDir() {
			if skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".ts" && ext != ".js" && ext != ".tsx" && ext != ".sql" {
			return nil
		}
		if info.Size() > 2<<20 { // skip files > 2MB
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), needle) {
			found = true
		}
		return nil
	})
	return found
}
