package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEscapeRoutes_IncludesBothDirections(t *testing.T) {
	all := EscapeRoutes("", "")
	if len(all) < 10 {
		t.Fatalf("expected a substantial curated list, got %d", len(all))
	}

	var intoYaver, outOfYaver bool
	for _, r := range all {
		if r.ToTargetID == "yaver-cloud" {
			intoYaver = true
		}
		if r.FromLabel == "Yaver" {
			outOfYaver = true
		}
	}
	if !intoYaver {
		t.Error("catalog missing inbound routes (X → Yaver Cloud)")
	}
	if !outOfYaver {
		t.Error("catalog missing outbound escape routes (Yaver → X)")
	}
}

func TestEscapeRoutes_FilterFrom(t *testing.T) {
	convex := EscapeRoutes("convex", "")
	if len(convex) == 0 {
		t.Fatal("expected Convex-origin routes")
	}
	for _, r := range convex {
		if r.FromBackend != BackendConvex {
			t.Errorf("filter leaked non-convex route: %+v", r)
		}
	}
}

func TestEscapeRoutes_FilterYaverAsSource(t *testing.T) {
	// "yaver" is a friendly alias for SQLite-as-source (Yaver projects are
	// SQLite-backed). Both label forms should match.
	routes := EscapeRoutes("yaver", "")
	if len(routes) == 0 {
		t.Fatal("expected outbound Yaver routes")
	}
	for _, r := range routes {
		if r.FromLabel != "Yaver" {
			t.Errorf("yaver filter returned non-Yaver route: %+v", r)
		}
	}
}

func TestEscapeRoutes_FilterTo(t *testing.T) {
	toCloud := EscapeRoutes("", "yaver-cloud")
	if len(toCloud) == 0 {
		t.Fatal("expected routes targeting yaver-cloud")
	}
	for _, r := range toCloud {
		if r.ToTargetID != "yaver-cloud" {
			t.Errorf("to filter leaked: %+v", r)
		}
	}
}

func TestEscapeRoutes_ComplexitiesComputed(t *testing.T) {
	all := EscapeRoutes("", "")
	var assessed int
	for _, r := range all {
		if r.Complexity != "" {
			assessed++
		}
	}
	if assessed != len(all) {
		t.Errorf("expected all %d routes to have computed complexity, got %d", len(all), assessed)
	}
}

func TestEscapeRoutes_CatalogTargetIDsAreValid(t *testing.T) {
	// Guard against catalog drift — every curated target must exist in the
	// SwitchEngine target list.
	for _, r := range escapeRouteCatalog {
		if _, err := SwitchTargetByID(r.ToTargetID); err != nil {
			t.Errorf("catalog route %q points at unknown switch target %q", r.ID, r.ToTargetID)
		}
	}
}

func TestEscapeRoutes_HighlightedCoversHeadlineCases(t *testing.T) {
	// The pitch case: Convex/Supabase → Yaver Cloud should both be
	// highlighted so the mobile UI can bubble them up.
	var convexInbound, supabaseInbound bool
	for _, r := range escapeRouteCatalog {
		if r.Highlight && r.ToTargetID == "yaver-cloud" {
			switch r.FromBackend {
			case BackendConvex:
				convexInbound = true
			case BackendSupabase:
				supabaseInbound = true
			}
		}
	}
	if !convexInbound || !supabaseInbound {
		t.Errorf("expected Convex→Yaver + Supabase→Yaver highlighted (got convex=%v, supabase=%v)", convexInbound, supabaseInbound)
	}
}

func TestHandleEscapeRoutes_ReturnsJSON(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/escape/routes?from=supabase", nil)
	w := httptest.NewRecorder()
	srv.handleEscapeRoutes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Routes []EscapeRoute `json:"routes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v — body=%s", err, w.Body.String())
	}
	if len(out.Routes) == 0 {
		t.Error("expected Supabase-origin routes")
	}
}

func TestHandleEscapePlan_RequiresFields(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/escape/plan", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleEscapePlan(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body, got %d", w.Code)
	}
}

func TestHandleEscapePlan_UnknownRoute(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/escape/plan",
		strings.NewReader(`{"routeId":"not-a-route","projectDir":"/tmp"}`))
	w := httptest.NewRecorder()
	srv.handleEscapePlan(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown route, got %d", w.Code)
	}
}

func TestHandleEscapePlan_GETRejected(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/escape/plan", nil)
	w := httptest.NewRecorder()
	srv.handleEscapePlan(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestFindEscapeRoute_AttachesComplexity(t *testing.T) {
	r := findEscapeRoute("convex-to-yaver-cloud")
	if r == nil {
		t.Fatal("expected to find convex-to-yaver-cloud")
	}
	if r.Complexity == "" {
		t.Error("expected complexity populated on findEscapeRoute result")
	}
}
