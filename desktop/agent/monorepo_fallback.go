package main

// monorepo_fallback bridges the legacy single-framework detection paths into
// the new DetectMonorepo() so dirs like carrotbet/ and yaver.io/ — which look
// empty at the root because their apps live in subdirs — stop showing up as
// "?" in the dashboard and stop hitting "could not detect framework" when the
// user hits Start.

import (
	"fmt"
	"sort"
	"strings"
)

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
		return pickIdx(candidates[i].Framework) < pickIdx(candidates[j].Framework)
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
