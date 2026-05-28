package main

// project_kind.go — classify the agent's current working directory into
// one of four buckets the UI uses to decide which features to surface:
//
//   "mobile"   — Expo / React Native / Flutter / iOS-native / Android-native
//   "web"      — Next / Vite / Nuxt / SvelteKit / Astro / Remix (browser app)
//   "backend"  — Go / Rust / Python / Hono / Convex-only (no frontend marker)
//   "generic"  — none of the above (empty / non-project dir)
//
// Used by:
//   - glass-terminal.tsx vibe bar — mobile keeps Hermes reload / wire push;
//     web swaps in web_preview_reload / deploy / tsc / eslint chips.
//   - glass-workspace.tsx — picks the default 4-pane layout per kind.
//   - web /workspace route — same.
//
// Detection reuses the existing detectStack() in repos_http.go so the
// classification stays consistent across endpoints. We don't shell out
// to DetectMonorepo here on purpose — detectStack is one os.Stat per
// marker file, lifetime-safe to call from any HTTP handler.

import (
	"net/http"
	"strings"
)

// ProjectKind is one of "mobile" | "web" | "backend" | "generic".
type ProjectKind string

const (
	ProjectKindMobile  ProjectKind = "mobile"
	ProjectKindWeb     ProjectKind = "web"
	ProjectKindBackend ProjectKind = "backend"
	ProjectKindGeneric ProjectKind = "generic"
)

// ClassifyFrameworks maps a slice of framework markers (as returned by
// detectStack or monorepo_detect) into a ProjectKind. Mobile wins over
// web when both are present (an Expo app with a Next.js companion site
// is still mobile-first — the user came to yaver for the phone flow).
func ClassifyFrameworks(frameworks []string) ProjectKind {
	hasMobile, hasWeb, hasBackend := false, false, false
	for _, f := range frameworks {
		switch strings.ToLower(f) {
		case "expo", "react-native", "flutter", "iosnative", "androidnative",
			"swift-package", "gradle-jvm", "unity":
			hasMobile = true
		case "next", "next.js", "nextjs", "vite", "nuxt", "svelte",
			"sveltekit", "astro", "remix", "solid", "qwik", "hono-web":
			hasWeb = true
		case "go", "rust", "python", "hono", "node-cli", "convex", "django",
			"fastapi", "express", "nestjs", "fastify":
			hasBackend = true
		}
	}
	switch {
	case hasMobile:
		return ProjectKindMobile
	case hasWeb:
		return ProjectKindWeb
	case hasBackend:
		return ProjectKindBackend
	default:
		return ProjectKindGeneric
	}
}

// ProjectKindResult is the JSON shape /project/kind returns. workDir is
// the absolute path classification ran against — clients show it so the
// user knows which project the chips are bound to.
type ProjectKindResult struct {
	Kind        ProjectKind `json:"kind"`
	WorkDir     string      `json:"workDir"`
	Frameworks  []string    `json:"frameworks"`
	HasManifest bool        `json:"hasManifest"`
	Reason      string      `json:"reason,omitempty"`
}

// handleProjectKind — GET /project/kind[?dir=<absPath>]
//
// Resolves the work directory (caller-provided `dir` wins, then
// taskMgr.workDir), runs detectStack on it, classifies the result.
// Cheap: no shell-outs, no file reads beyond os.Stat on a handful
// of marker files.
func (s *HTTPServer) handleProjectKind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" && s.taskMgr != nil {
		dir = s.taskMgr.workDir
	}
	if dir == "" {
		jsonReply(w, http.StatusOK, ProjectKindResult{
			Kind:   ProjectKindGeneric,
			Reason: "no work directory configured",
		})
		return
	}
	stack := detectStack(dir)
	kind := ClassifyFrameworks(stack.Frameworks)
	// Workspace manifest sticky-pins the kind when present. A yaver
	// workspace manifest never appears in non-yaver projects, so it's
	// safe to treat as authoritative.
	hasManifest := false
	if mr, _, err := loadWorkspaceManifestForHTTP(dir); err == nil && mr != nil && len(mr.Apps) > 0 {
		hasManifest = true
		// If the manifest's apps disagree with the file-marker scan,
		// prefer the manifest — it's the user's explicit declaration.
		var manifestFwks []string
		for _, app := range mr.Apps {
			if fw := StackToFramework(app.Stack); fw != "" {
				manifestFwks = append(manifestFwks, fw)
			}
		}
		if len(manifestFwks) > 0 {
			kind = ClassifyFrameworks(manifestFwks)
			stack.Frameworks = manifestFwks
		}
	}
	jsonReply(w, http.StatusOK, ProjectKindResult{
		Kind:        kind,
		WorkDir:     dir,
		Frameworks:  stack.Frameworks,
		HasManifest: hasManifest,
	})
}
