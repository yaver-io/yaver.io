package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// testkit_grow.go — the self-growing test loop's planning half (P2). It scans a
// project's routes/screens, diffs them against the existing yaver-tests/ specs
// and a coverage ledger, and returns an *author plan*: the uncovered Features
// the Yaver runner should write as new *.test.yaml specs. The runner (Claude /
// Codex) is the author; this Go side keeps the plan deterministic and the ledger
// monotonic so coverage only grows. No spec is ever deleted here.

type growCandidate struct {
	SuggestedName string `json:"suggestedName"`
	Route         string `json:"route"`
	File          string `json:"file"`
	Why           string `json:"why"`
}

type ledgerEntry struct {
	Name      string `json:"name"`
	Route     string `json:"route,omitempty"`
	SpecPath  string `json:"specPath,omitempty"`
	Status    string `json:"status"` // "specced"
	UpdatedAt int64  `json:"updatedAt"`
}

type coverageLedger struct {
	Project   string        `json:"project,omitempty"`
	UpdatedAt int64         `json:"updatedAt"`
	Entries   []ledgerEntry `json:"entries"`
}

type growPlan struct {
	ProjectDir    string          `json:"projectDir"`
	SpecsDir      string          `json:"specsDir"`
	CoveredCount  int             `json:"coveredCount"`
	CoveredRoutes []string        `json:"coveredRoutes"`
	Uncovered     []growCandidate `json:"uncovered"`
	LedgerPath    string          `json:"ledgerPath"`
	Applied       bool            `json:"applied"`
	AuthorPrompt  string          `json:"authorPrompt"`
	TaskID        string          `json:"taskId,omitempty"` // set when author:true enqueued the runner
}

func growTestPlan(dir string, apply bool) (*growPlan, error) {
	projectDir := strings.TrimSpace(dir)
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		projectDir = cwd
	}
	projectDir, _ = filepath.Abs(projectDir)
	specsDir := filepath.Join(projectDir, "yaver-tests")

	// Existing coverage from specs (names + URLs).
	coveredRoutes := map[string]bool{}
	coveredNames := map[string]string{} // name -> specPath
	if specs, err := testkit.DiscoverSpecs(specsDir); err == nil {
		for _, sp := range specs {
			coveredNames[strings.ToLower(sp.Name)] = sp.Path
			if u := routePathOf(sp.URL); u != "" {
				coveredRoutes[u] = true
			}
		}
	}

	routes := discoverRoutes(projectDir)
	var uncovered []growCandidate
	for _, rt := range routes {
		if coveredRoutes[rt.route] {
			continue
		}
		// name-based fallback: if a spec already mentions this route segment, skip
		seg := lastSeg(rt.route)
		if seg != "" {
			skip := false
			for n := range coveredNames {
				if strings.Contains(n, seg) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		uncovered = append(uncovered, growCandidate{
			SuggestedName: "feature-" + slugRoute(rt.route),
			Route:         rt.route,
			File:          rt.file,
			Why:           "route has no test spec yet",
		})
		if len(uncovered) >= 50 {
			break
		}
	}

	plan := &growPlan{
		ProjectDir: projectDir, SpecsDir: specsDir,
		CoveredCount: len(coveredNames), Uncovered: uncovered,
		LedgerPath: filepath.Join(specsDir, ".coverage.json"),
	}
	for r := range coveredRoutes {
		plan.CoveredRoutes = append(plan.CoveredRoutes, r)
	}
	plan.AuthorPrompt = buildAuthorPrompt(plan)

	if apply {
		led := coverageLedger{UpdatedAt: time.Now().UnixMilli()}
		for name, sp := range coveredNames {
			led.Entries = append(led.Entries, ledgerEntry{
				Name: name, SpecPath: sp, Status: "specced", UpdatedAt: led.UpdatedAt,
			})
		}
		if err := os.MkdirAll(specsDir, 0o755); err == nil {
			if b, err := json.MarshalIndent(led, "", "  "); err == nil {
				if os.WriteFile(plan.LedgerPath, b, 0o644) == nil {
					plan.Applied = true
				}
			}
		}
	}
	return plan, nil
}

type routeHit struct {
	route string
	file  string
}

// discoverRoutes finds Next.js (app/pages router) + Expo-router screen files and
// derives their route path. Heuristic but stack-agnostic enough for a plan.
func discoverRoutes(projectDir string) []routeHit {
	skip := map[string]bool{
		"node_modules": true, ".git": true, ".next": true, "build": true,
		"dist": true, ".open-next": true, ".wrangler": true, "ios": true,
		"android": true, "__pycache__": true, ".yaver": true,
	}
	seen := map[string]routeHit{}
	_ = filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		ext := filepath.Ext(base)
		if ext != ".tsx" && ext != ".jsx" && ext != ".ts" && ext != ".js" {
			return nil
		}
		rel, _ := filepath.Rel(projectDir, path)
		rel = filepath.ToSlash(rel)
		stem := strings.TrimSuffix(base, ext)

		// Next.js app router: .../app/<segments>/page.tsx
		if (stem == "page") && (strings.Contains(rel, "/app/") || strings.HasPrefix(rel, "app/")) {
			rt := routeFromAppDir(rel, "page")
			if rt != "" {
				seen[rt] = routeHit{route: rt, file: rel}
			}
			return nil
		}
		// Next.js pages router: .../pages/<segments>.tsx (skip _app/_document/api)
		if strings.Contains(rel, "/pages/") || strings.HasPrefix(rel, "pages/") {
			if stem == "_app" || stem == "_document" || strings.Contains(rel, "/api/") {
				return nil
			}
			rt := routeFromPagesDir(rel)
			if rt != "" {
				seen[rt] = routeHit{route: rt, file: rel}
			}
			return nil
		}
		// Expo-router screens: app/<segments>/(index|name).tsx (no separate page file)
		if (strings.Contains(rel, "/app/") || strings.HasPrefix(rel, "app/")) && stem != "_layout" {
			rt := routeFromAppDir(rel, stem)
			if rt != "" {
				seen[rt] = routeHit{route: rt, file: rel}
			}
		}
		return nil
	})
	out := make([]routeHit, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	return out
}

func routeFromAppDir(rel, stem string) string {
	i := strings.LastIndex(rel, "app/")
	if i < 0 {
		return ""
	}
	sub := rel[i+len("app/"):]
	sub = strings.TrimSuffix(sub, filepath.Ext(sub)) // drop ext
	parts := strings.Split(sub, "/")
	var segs []string
	for _, p := range parts {
		if p == "" || p == stem || p == "page" || p == "index" {
			continue
		}
		if strings.HasPrefix(p, "(") && strings.HasSuffix(p, ")") {
			continue // route group
		}
		segs = append(segs, p)
	}
	return "/" + strings.Join(segs, "/")
}

func routeFromPagesDir(rel string) string {
	i := strings.LastIndex(rel, "pages/")
	if i < 0 {
		return ""
	}
	sub := rel[i+len("pages/"):]
	sub = strings.TrimSuffix(sub, filepath.Ext(sub))
	if sub == "index" {
		return "/"
	}
	sub = strings.TrimSuffix(sub, "/index")
	return "/" + sub
}

func routePathOf(u string) string {
	if u == "" {
		return ""
	}
	// strip scheme+host+query
	s := u
	if k := strings.Index(s, "://"); k >= 0 {
		s = s[k+3:]
		if sl := strings.Index(s, "/"); sl >= 0 {
			s = s[sl:]
		} else {
			s = "/"
		}
	}
	if q := strings.IndexAny(s, "?#"); q >= 0 {
		s = s[:q]
	}
	if s == "" {
		return "/"
	}
	return s
}

func lastSeg(route string) string {
	parts := strings.Split(strings.Trim(route, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(parts[len(parts)-1])
}

func slugRoute(route string) string {
	s := strings.Trim(route, "/")
	if s == "" {
		return "home"
	}
	r := strings.NewReplacer("/", "-", "[", "", "]", "", " ", "-")
	return strings.ToLower(r.Replace(s))
}

func buildAuthorPrompt(p *growPlan) string {
	var sb strings.Builder
	sb.WriteString("You are the Yaver test author. Write deterministic yaver-tests/*.test.yaml Feature specs ")
	sb.WriteString("for the uncovered routes below, using the project's real selectors/data-testids. ")
	sb.WriteString("One Feature per route. Reuse the existing specs' auth (cookies: block) and base url. ")
	sb.WriteString("Set artifacts.video: true. NEVER delete or weaken existing green specs. ")
	sb.WriteString("After writing, run project_test_run and self-heal any flaky selector.\n\n")
	sb.WriteString(fmt.Sprintf("Project: %s\nSpecs dir: %s\nAlready covered: %d feature(s)\n\nUncovered routes to author (%d):\n",
		p.ProjectDir, p.SpecsDir, p.CoveredCount, len(p.Uncovered)))
	for _, c := range p.Uncovered {
		sb.WriteString(fmt.Sprintf("  - %s  (route %s, from %s)\n", c.SuggestedName, c.Route, c.File))
	}
	return sb.String()
}
