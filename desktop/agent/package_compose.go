package main

// package_compose.go — "define a task loosely, get a package". The owner gives a
// natural-language goal; the composer emits a concrete, inspectable
// yaver.package.yaml (TaskPackage), which is then published and preflighted.
//
// The default composer is a dependency-light HEURISTIC (URL + intent + geo +
// schedule extraction) so it is deterministic and testable with no model. The
// `packageComposer` seam lets a richer LLM-backed composer (autopilot / the
// codingAgent stack) replace it later without changing the verb or the UI — the
// output is always a reviewable package, never an opaque prompt.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// packageComposer is the seam: goal + optional name -> a draft package.
var packageComposer = heuristicComposePackage

var (
	urlRe   = regexp.MustCompile(`https?://[^\s"'<>)]+`)
	everyRe = regexp.MustCompile(`every\s+(\d+)\s*(minutes?|mins?|hours?|hrs?|days?|m|h|d)\b`)
)

// countryHints maps words that appear in a goal to an ISO-3166 alpha-2 code.
var countryHints = map[string]string{
	"serbia": "RS", "serbian": "RS", "belgrade": "RS",
	"turkey": "TR", "turkish": "TR",
	"germany": "DE", "german": "DE",
	"united states": "US", " usa": "US", " us ": "US", "america": "US",
	"united kingdom": "GB", " uk ": "GB", "britain": "GB",
	"france": "FR", "french": "FR",
	"spain": "ES", "italy": "IT", "netherlands": "NL", "poland": "PL",
}

// actionVerbs signal an ACTING package (operate) rather than read-only collect.
var actionVerbs = []string{
	"submit", "place a bet", "place bet", "place an order", "place order",
	"check out", "checkout", "click", "buy ", "purchase", "fill in", "fill out",
	"sign in", "log in", "apply", "book ", "reserve", "send a", "post a",
}

func slugFromGoal(goal string) string {
	words := strings.Fields(strings.ToLower(goal))
	keep := []string{}
	for _, w := range words {
		w = strings.Trim(w, ".,;:\"'!?()")
		if len(w) < 3 || strings.HasPrefix(w, "http") {
			continue
		}
		keep = append(keep, w)
		if len(keep) >= 4 {
			break
		}
	}
	s := sanitizePackageName(strings.Join(keep, "-"))
	if s == "" {
		s = "task"
	}
	return s
}

func detectSchedule(low string) string {
	if m := everyRe.FindStringSubmatch(low); m != nil {
		n, unit := m[1], m[2]
		switch {
		case strings.HasPrefix(unit, "h"):
			return n + "h"
		case strings.HasPrefix(unit, "d"):
			return n + "d"
		default:
			return n + "m"
		}
	}
	switch {
	case strings.Contains(low, "every minute"):
		return "1m"
	case strings.Contains(low, "hourly"):
		return "1h"
	case strings.Contains(low, "daily"), strings.Contains(low, "every day"):
		return "24h"
	}
	return ""
}

func detectGeo(low string) string {
	for word, iso := range countryHints {
		if strings.Contains(low, word) {
			return iso
		}
	}
	return ""
}

// heuristicComposePackage builds a draft TaskPackage from a loose goal.
func heuristicComposePackage(goal, name string) (*TaskPackage, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, fmt.Errorf("goal required")
	}
	low := strings.ToLower(" " + goal + " ")

	// URLs -> sources.
	var sources []PackageSource
	for i, raw := range urlRe.FindAllString(goal, -1) {
		u := strings.TrimRight(raw, ".,;:!?)\"'")
		render := "auto"
		if strings.Contains(u, "/api/") || strings.HasSuffix(u, ".json") {
			render = "fetch"
		}
		sources = append(sources, PackageSource{ID: fmt.Sprintf("src%d", i+1), URL: u, Render: render})
	}

	// Intent -> kind + engines + runtimes.
	kind := "collect"
	acting := false
	for _, v := range actionVerbs {
		if strings.Contains(low, v) {
			kind = "operate"
			acting = true
			break
		}
	}
	engines := []string{"fetch", "webview"}
	runtimes := []string{"mobile", "agent"}
	if acting {
		engines = []string{"playwright"} // an action needs a real browser
		runtimes = []string{"agent", "docker"}
	}

	geo := detectGeo(low)
	residential := geo != "" || strings.Contains(low, "residential")
	schedule := detectSchedule(low)

	if strings.TrimSpace(name) == "" {
		name = slugFromGoal(goal)
	}

	domains := []string{}
	for _, s := range sources {
		if u := extractHost(s.URL); u != "" {
			domains = append(domains, u)
		}
	}
	consent := fmt.Sprintf("Run this on your device: %s", goal)
	if len(domains) > 0 {
		consent = fmt.Sprintf("Fetch public pages from %s using your connection. No accounts, no logins.", strings.Join(domains, ", "))
	}

	p := &TaskPackage{
		Metadata: PackageMeta{Name: name, Description: goal},
		Spec: PackageSpec{
			Runtimes: runtimes,
			Vantage:  PackageVantage{Residential: residential},
			Schedule: PackageSchedule{Every: schedule, Wakeable: true},
			Consent:  PackageConsent{Summary: consent, WillNot: []string{"login", "payment", "account_creation", "store_credentials"}},
			Task: PackageTask{
				Kind:    kind,
				Engines: engines,
				Sources: sources,
			},
		},
	}
	if geo != "" {
		p.Spec.Vantage.Geo = []string{geo}
	}
	if acting {
		p.Spec.Guard = PackageGuard{Tier: "acting", Confirm: "per-run", Sandbox: "required"}
	}
	if err := validatePackage(p); err != nil {
		return nil, err
	}
	return p, nil
}

func extractHost(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexAny(s, "/:?"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "package_compose",
		Description: "Define a task LOOSELY in natural language; Yaver writes a concrete Task Package " +
			"(yaver.package.yaml), publishes it as a draft, and preflights it. Returns the generated manifest + " +
			"the preflight verdict so you can review/edit before sharing. The composer infers sources (URLs), " +
			"kind (collect vs operate), vantage geo, schedule, and consent from the goal. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"goal": map[string]interface{}{"type": "string", "description": "Natural-language description of the task."},
			"name": map[string]interface{}{"type": "string", "description": "Optional package name; derived from the goal if omitted."},
		}, "goal"),
		Handler:    packageComposeHandler,
		AllowGuest: false,
	})
}

func packageComposeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Goal string `json:"goal"`
		Name string `json:"name"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(args.Goal) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "goal required"}
	}
	p, err := packageComposer(args.Goal, args.Name)
	if err != nil {
		return OpsResult{OK: false, Code: "compose_failed", Error: err.Error()}
	}
	stored := pkgStore.upsertPackage(*p)
	// Preflight the generated package so the owner sees its verdict immediately.
	check := checkPackage(c, stored)
	pkgStore.setCheck(stored.Metadata.Name, check)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"package": stored,
		"tier":    stored.effectiveTier(),
		"check":   check,
		"hint":    "review the generated package; edit + republish if needed, then allocate to a runner",
	}}
}
