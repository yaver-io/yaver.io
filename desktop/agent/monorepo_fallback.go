package main

// monorepo_fallback bridges the legacy single-framework detection paths into
// the new DetectMonorepo() so dirs like carrotbet/ and yaver.io/ — which look
// empty at the root because their apps live in subdirs — stop showing up as
// "?" in the dashboard and stop hitting "could not detect framework" when the
// user hits Start.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// preferredMonorepoAppNames is the set of conventional sub-app directory
// names that win tiebreakers in the monorepo fallback picker. These names
// are what most teams use for the "main" web/dashboard app — picking them
// over generic / demo apps means dashboard's Webview tab opens the project
// the user actually wanted instead of an arbitrary same-framework sibling
// (e.g. yaver.io has both web/ and demo/web/todo-web — both Next.js — and
// the picker should prefer web/).
var preferredMonorepoAppNames = map[string]bool{
	"web":       true,
	"app":       true,
	"apps":      true,
	"frontend":  true,
	"dashboard": true,
	"site":      true,
	"www":       true,
	"client":    true,
	"ui":        true,
}

// deprioritizedMonorepoSegments is the set of path segments that deduct from
// a candidate's score during monorepo fallback. These are dirs where teams
// typically park examples / playgrounds / tests — we never want them to be
// picked over a real top-level app of the same framework.
var deprioritizedMonorepoSegments = map[string]bool{
	"demo":     true,
	"demos":    true,
	"example":  true,
	"examples": true,
	"sample":   true,
	"samples":  true,
	"sandbox":  true,
	"test":     true,
	"tests":    true,
	"e2e":      true,
	"fixture":  true,
	"fixtures": true,
}

func hasDeprioritizedSegment(rel string) bool {
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if deprioritizedMonorepoSegments[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

// monorepoSummaryForDir returns a "monorepo" framework label plus the distinct
// frameworks found inside dir, when dir is a monorepo. Returns ("", nil) for
// standalone projects so callers can fall through to their normal handling.
func monorepoSummaryForDir(dir string) (framework string, subFrameworks []string) {
	mr, err := DetectMonorepo(dir, DetectOpts{MaxDepth: 4})
	if err != nil || mr == nil || !mr.IsMonorepo {
		return "", nil
	}
	return "monorepo", mr.Frameworks
}

// monorepoFallbackDevServer attempts to pick a runnable sub-project when the
// caller asked to start a dev server in a directory that has no marker file
// at its root but is actually a monorepo. Returns the best (DevServer, workDir)
// pair, or (nil, "", err) with a friendly message listing the candidates.
//
// Preference order matches what most developers expect: dev servers that
// produce a usable preview the fastest first (Vite > Next > Expo > RN > Flutter),
// then anything else.
func monorepoFallbackDevServer(dir string) (DevServer, string, error) {
	mr, err := DetectMonorepo(dir, DetectOpts{MaxDepth: 4})
	if err != nil {
		return nil, "", fmt.Errorf("could not detect framework in %s", dir)
	}
	if mr == nil || len(mr.Projects) == 0 {
		return nil, "", fmt.Errorf("could not detect framework in %s", dir)
	}

	preference := []string{"vite", "next", "expo", "react-native", "flutter"}
	pickIdx := func(fw string) int {
		for i, p := range preference {
			if p == fw {
				return i
			}
		}
		return len(preference)
	}
	candidates := append([]DetectedProject(nil), mr.Projects...)
	sort.SliceStable(candidates, func(i, j int) bool {
		// 1. Framework preference: Vite/Next get the user to a web preview
		//    fastest; Expo/RN/Flutter need a device.
		pi := pickIdx(candidates[i].Framework)
		pj := pickIdx(candidates[j].Framework)
		if pi != pj {
			return pi < pj
		}
		// 2. Conventional app name: when a monorepo has multiple apps in
		//    the same framework, the one named `web` / `app` / `frontend`
		//    is overwhelmingly the "main" app the user wants — anything
		//    else (e.g. yaver.io has demo/web/todo-web) is a sibling we
		//    should avoid auto-picking.
		ni := preferredMonorepoAppNames[strings.ToLower(filepath.Base(candidates[i].RelPath))]
		nj := preferredMonorepoAppNames[strings.ToLower(filepath.Base(candidates[j].RelPath))]
		if ni != nj {
			return ni
		}
		// 3. Demote anything under `demo/` / `examples/` / `tests/` etc.
		//    These dirs are intentionally not the user's main app.
		di := hasDeprioritizedSegment(candidates[i].RelPath)
		dj := hasDeprioritizedSegment(candidates[j].RelPath)
		if di != dj {
			return !di
		}
		// 4. Prefer shallower paths — top-level `web/` beats nested
		//    `apps/web/` only when (2) and (3) tie, which is rarely the
		//    case but matters for monorepos where every app lives under
		//    apps/ except a top-level one that's actually a tool.
		depthI := strings.Count(candidates[i].RelPath, string(filepath.Separator))
		depthJ := strings.Count(candidates[j].RelPath, string(filepath.Separator))
		if depthI != depthJ {
			return depthI < depthJ
		}
		// 5. Fall back to alphabetical so the picker is deterministic.
		return candidates[i].RelPath < candidates[j].RelPath
	})

	for _, p := range candidates {
		if ds := detectDevServer(p.Path); ds != nil {
			return ds, p.Path, nil
		}
	}

	// No runnable dev server, but real apps exist — give the user the catalogue.
	names := make([]string, 0, len(mr.Projects))
	for _, p := range mr.Projects {
		names = append(names, fmt.Sprintf("%s (%s)", p.RelPath, p.Framework))
	}
	return nil, "", fmt.Errorf(
		"%s is a monorepo with %d app(s): %s — pass workDir to a specific app",
		dir, len(mr.Projects), strings.Join(names, ", "),
	)
}
