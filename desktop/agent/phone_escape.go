package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Escape routes — curated "I'm on X, get me to Y" options.
//
// Positioning: the main use case of Yaver is the phone-first vibe coder
// working on a monorepo. Escape routes are NOT the headline feature —
// they're a trust signal. The existence of this surface, plus one-click
// migrations that actually work, reassures users ("no lock-in") so they'll
// commit to the Yaver-native continuum without worrying about being stuck.
//
// Routes include both escape-FROM-Yaver (where you'd expect) AND
// escape-INTO-Yaver (the inbound path that matches the YC pitch: "we'll
// pull you out of Convex/Supabase/Firebase").
//
// Implementation: each route is a thin wrapper around the existing
// SwitchEngine target set. We don't add new migration code here — we
// just map friendly (from, to) pairs to switch-target IDs + blurbs so
// the mobile UI can show a picker without exposing the 19-target menu.

// EscapeRoute is one curated migration path.
type EscapeRoute struct {
	ID          string           `json:"id"`                    // stable id: "convex-to-yaver-cloud"
	FromBackend BackendKind      `json:"fromBackend"`           // inferred from project dir
	FromLabel   string           `json:"fromLabel"`             // "Convex"
	ToTargetID  string           `json:"toTargetId"`            // SwitchEngine target id ("yaver-cloud")
	ToLabel     string           `json:"toLabel"`               // "Yaver Cloud"
	Label       string           `json:"label"`                 // "Convex → Yaver Cloud"
	Blurb       string           `json:"blurb"`                 // one-liner what happens
	Complexity  SwitchComplexity `json:"complexity,omitempty"`  // trivial/easy/medium/hard (computed)
	Highlight   bool             `json:"highlight,omitempty"`   // one of the top-surfaced routes
}

// escapeRouteCatalog is the single source of truth for the curated list.
// New rows: add here, no code changes elsewhere. Keep short — the mobile UI
// renders this as a list; over ~20 entries it gets noisy.
var escapeRouteCatalog = []EscapeRoute{
	// ---- Into Yaver (the pitch: "come home, no migration drama") ----
	{ID: "convex-to-yaver-cloud", FromBackend: BackendConvex, FromLabel: "Convex", ToTargetID: "yaver-cloud", ToLabel: "Yaver Cloud", Label: "Convex → Yaver Cloud", Blurb: "Leave Convex. Same data, same URLs, one manifest.", Highlight: true},
	{ID: "supabase-to-yaver-cloud", FromBackend: BackendSupabase, FromLabel: "Supabase", ToTargetID: "yaver-cloud", ToLabel: "Yaver Cloud", Label: "Supabase → Yaver Cloud", Blurb: "Pull your Supabase schema + data into Yaver's managed tier.", Highlight: true},
	{ID: "postgres-to-yaver-cloud", FromBackend: BackendPostgres, FromLabel: "Postgres", ToTargetID: "yaver-cloud", ToLabel: "Yaver Cloud", Label: "Postgres → Yaver Cloud", Blurb: "Dump + restore — no driver rewrite, just point your app at the new URL."},
	{ID: "pocketbase-to-yaver-cloud", FromBackend: BackendPocketBase, FromLabel: "PocketBase", ToTargetID: "yaver-cloud", ToLabel: "Yaver Cloud", Label: "PocketBase → Yaver Cloud", Blurb: "Collections + auth + files, all promoted to Yaver's managed tier."},
	{ID: "appwrite-to-yaver-cloud", FromBackend: BackendAppwrite, FromLabel: "Appwrite", ToTargetID: "yaver-cloud", ToLabel: "Yaver Cloud", Label: "Appwrite → Yaver Cloud", Blurb: "Appwrite collections + rules mapped onto the Yaver manifest."},
	{ID: "sqlite-to-yaver-cloud", FromBackend: BackendSQLite, FromLabel: "SQLite", ToTargetID: "yaver-cloud", ToLabel: "Yaver Cloud", Label: "SQLite → Yaver Cloud", Blurb: "Single-file local backend promoted to hosted. Trivial."},

	// ---- Escape OUT of Yaver (trust signal: "we're not holding you hostage") ----
	{ID: "yaver-to-convex-cloud", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "convex-cloud", ToLabel: "Convex", Label: "Yaver → Convex Cloud", Blurb: "Hard switch — paradigm shift from SQL to Convex reactivity. AI rewrite prompt included."},
	{ID: "yaver-to-supabase-cloud", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "supabase-cloud", ToLabel: "Supabase Cloud", Label: "Yaver → Supabase Cloud", Blurb: "Postgres-family move. Schema + data transferred; Supabase-specific features (RLS, Realtime) need a light touch."},
	{ID: "yaver-to-neon", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "postgres-neon", ToLabel: "Neon", Label: "Yaver → Neon", Blurb: "Serverless Postgres. Same schema, different connection string."},
	{ID: "yaver-to-turso", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "sqlite-turso", ToLabel: "Turso", Label: "Yaver → Turso (managed LibSQL)", Blurb: "SQLite stays SQLite, now distributed at the edge."},
	{ID: "yaver-to-d1", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "sqlite-d1", ToLabel: "Cloudflare D1", Label: "Yaver → Cloudflare D1", Blurb: "SQLite on the Cloudflare edge. Pair with the Workers port (handoff 2.5)."},

	// ---- Third-party to third-party (Yaver-as-transit) ----
	{ID: "convex-to-supabase-cloud", FromBackend: BackendConvex, FromLabel: "Convex", ToTargetID: "supabase-cloud", ToLabel: "Supabase Cloud", Label: "Convex → Supabase Cloud", Blurb: "Paradigm shift to Postgres. AI rewrite prompt for functions + auth."},
	{ID: "supabase-to-convex-cloud", FromBackend: BackendSupabase, FromLabel: "Supabase", ToTargetID: "convex-cloud", ToLabel: "Convex Cloud", Label: "Supabase → Convex Cloud", Blurb: "Flip to reactive Convex. AI rewrite prompt for RLS policies + queries."},
	{ID: "supabase-to-neon", FromBackend: BackendSupabase, FromLabel: "Supabase", ToTargetID: "postgres-neon", ToLabel: "Neon", Label: "Supabase → Neon", Blurb: "Keep Postgres, drop the Supabase-specific features. Schema + data transferred."},
	{ID: "neon-to-supabase-cloud", FromBackend: BackendPostgres, FromLabel: "Postgres", ToTargetID: "supabase-cloud", ToLabel: "Supabase Cloud", Label: "Postgres → Supabase Cloud", Blurb: "Add Supabase auth + storage to your existing Postgres schema."},
	{ID: "postgres-to-turso", FromBackend: BackendPostgres, FromLabel: "Postgres", ToTargetID: "sqlite-turso", ToLabel: "Turso", Label: "Postgres → Turso", Blurb: "Schema translated Postgres → SQLite; re-indexed. Some types (JSONB) simplified."},

	// ---- Self-host exits ----
	{ID: "yaver-to-hetzner", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "hetzner", ToLabel: "Your Hetzner VPS", Label: "Yaver → Hetzner (self-host)", Blurb: "Same yaver binary on your own VPS. No managed-cloud bill."},
	{ID: "yaver-to-render", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "render", ToLabel: "Render", Label: "Yaver → Render", Blurb: "Deploy the Yaver runtime to Render's managed tier."},
	{ID: "yaver-to-fly", FromBackend: BackendSQLite, FromLabel: "Yaver", ToTargetID: "fly", ToLabel: "Fly.io", Label: "Yaver → Fly.io", Blurb: "Yaver Docker image on Fly. Good for regional deploys."},
}

// computeRouteComplexity fills the Complexity field from the existing
// SwitchEngine assessor so the mobile UI can tag each row (trivial / easy /
// medium / hard). Runs in-process — no network.
func computeEscapeRouteComplexities(routes []EscapeRoute) []EscapeRoute {
	out := make([]EscapeRoute, 0, len(routes))
	for _, r := range routes {
		target, err := SwitchTargetByID(r.ToTargetID)
		if err != nil {
			// Catalog drift — surface the route but tag complexity unknown.
			out = append(out, r)
			continue
		}
		r.Complexity = AssessComplexity(r.FromBackend, *target)
		out = append(out, r)
	}
	return out
}

// EscapeRoutes returns the curated list with live complexities. Optional
// filters: fromBackend (e.g. "convex") narrows the list; yaver-as-source
// matches the synthetic SQLite fromBackend since Yaver projects are SQLite.
func EscapeRoutes(fromBackend, toTargetID string) []EscapeRoute {
	filtered := make([]EscapeRoute, 0, len(escapeRouteCatalog))
	for _, r := range escapeRouteCatalog {
		if fromBackend != "" && string(r.FromBackend) != fromBackend &&
			!(strings.EqualFold(fromBackend, "yaver") && r.FromLabel == "Yaver") {
			continue
		}
		if toTargetID != "" && r.ToTargetID != toTargetID {
			continue
		}
		filtered = append(filtered, r)
	}
	return computeEscapeRouteComplexities(filtered)
}

// ---- HTTP handlers ----

func (s *HTTPServer) registerEscapeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/escape/routes", s.auth(s.handleEscapeRoutes))
	mux.HandleFunc("/escape/plan", s.auth(s.handleEscapePlan))
}

func (s *HTTPServer) handleEscapeRoutes(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes": EscapeRoutes(from, to),
	})
}

// handleEscapePlan is a thin wrapper around /switch/plan that accepts a
// route id instead of requiring the caller to know switch-target IDs. The
// switch engine does the real work — we just translate (routeId →
// projectDir + targetId) for the mobile UI.
func (s *HTTPServer) handleEscapePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		RouteID    string `json:"routeId"`
		ProjectDir string `json:"projectDir"`
		DryRun     bool   `json:"dryRun"`
		Run        bool   `json:"run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.RouteID == "" || body.ProjectDir == "" {
		jsonError(w, http.StatusBadRequest, "routeId and projectDir required")
		return
	}
	route := findEscapeRoute(body.RouteID)
	if route == nil {
		jsonError(w, http.StatusNotFound, "unknown route: "+body.RouteID)
		return
	}

	// Sanity-check the project's detected backend matches the route's source
	// expectation. We warn but don't fail — the user may be escaping a
	// project whose detector hasn't been wired up yet.
	cfg, err := LoadProjectConfig(body.ProjectDir)
	warning := ""
	if err == nil && cfg.Backend != "" && cfg.Backend != route.FromBackend {
		// Yaver routes use SQLite as the FromBackend synthetic — still OK.
		if !(route.FromLabel == "Yaver" && cfg.Backend == BackendSQLite) {
			warning = "project backend is " + string(cfg.Backend) + " but route expects " + string(route.FromBackend) + " — proceed only if you're sure"
		}
	}

	engine := NewSwitchEngine()
	state, err := engine.Plan(body.ProjectDir, route.ToTargetID, body.DryRun)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := engine.Persist(state); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Run {
		if err := engine.Run(state); err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"route":   route,
				"state":   state,
				"warning": warning,
				"error":   err.Error(),
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"route":   route,
		"state":   state,
		"warning": warning,
	})
}

func findEscapeRoute(id string) *EscapeRoute {
	for i := range escapeRouteCatalog {
		if escapeRouteCatalog[i].ID == id {
			r := escapeRouteCatalog[i]
			if target, err := SwitchTargetByID(r.ToTargetID); err == nil {
				r.Complexity = AssessComplexity(r.FromBackend, *target)
			}
			return &r
		}
	}
	return nil
}
