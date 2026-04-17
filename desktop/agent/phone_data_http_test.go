package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// End-to-end on the in-memory SQLite the todos template creates. Exercises
// the token gate + all CRUD verbs + CORS preflight.

func setupDataProjectWithToken(t *testing.T, name string) (slug, rawToken string) {
	t.Helper()
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: name, Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mint, err := MintPhoneProjectToken(p.Slug, "test-client")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return p.Slug, mint.Raw
}

func TestPhoneData_RejectsMissingToken(t *testing.T) {
	setupPhoneTestHome(t)
	slug, _ := setupDataProjectWithToken(t, "data-noauth")
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/data/"+slug+"/todos", nil)
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPhoneData_RejectsInvalidToken(t *testing.T) {
	setupPhoneTestHome(t)
	slug, _ := setupDataProjectWithToken(t, "data-badauth")
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/data/"+slug+"/todos?api_key=pp_fake_xxx", nil)
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestPhoneData_CrossProjectTokenForbidden(t *testing.T) {
	setupPhoneTestHome(t)
	_, tokA := setupDataProjectWithToken(t, "project-a")
	_, _ = setupDataProjectWithToken(t, "project-b")
	srv := &HTTPServer{}
	// Use A's token to access B's data — must 403.
	req := httptest.NewRequest(http.MethodGet, "/data/project-b/todos", nil)
	req.Header.Set("Authorization", "Bearer "+tokA)
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cross-project access, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPhoneData_ListAndInsertAndGet(t *testing.T) {
	setupPhoneTestHome(t)
	slug, tok := setupDataProjectWithToken(t, "data-crud")
	srv := &HTTPServer{}

	// List — should return the 3 seeded todos.
	req := httptest.NewRequest(http.MethodGet, "/data/"+slug+"/todos", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Rows []map[string]interface{} `json:"rows"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listed)
	if len(listed.Rows) < 1 {
		t.Fatalf("expected seeded todos, got %d rows", len(listed.Rows))
	}

	// Insert — POST with Bearer.
	row := map[string]interface{}{
		"id": "data-api-row", "title": "from api", "done": false, "owner_id": "alice",
	}
	body, _ := json.Marshal(row)
	req = httptest.NewRequest(http.MethodPost, "/data/"+slug+"/todos", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("insert: %d %s", w.Code, w.Body.String())
	}

	// Get one — GET /data/<slug>/todos/<id>.
	req = httptest.NewRequest(http.MethodGet, "/data/"+slug+"/todos/data-api-row", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get one: %d %s", w.Code, w.Body.String())
	}
	var one map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &one)
	if one["title"] != "from api" {
		t.Errorf("get-one payload mismatch: %+v", one)
	}

	// PATCH update.
	patch := map[string]interface{}{"title": "updated"}
	pb, _ := json.Marshal(patch)
	req = httptest.NewRequest(http.MethodPatch, "/data/"+slug+"/todos/data-api-row",
		strings.NewReader(string(pb)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("patch: %d %s", w.Code, w.Body.String())
	}

	// DELETE.
	req = httptest.NewRequest(http.MethodDelete, "/data/"+slug+"/todos/data-api-row", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("delete: %d %s", w.Code, w.Body.String())
	}

	// Confirm it's gone — get-one should 404.
	req = httptest.NewRequest(http.MethodGet, "/data/"+slug+"/todos/data-api-row", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", w.Code)
	}
}

func TestPhoneData_CORSPreflight(t *testing.T) {
	setupPhoneTestHome(t)
	slug, _ := setupDataProjectWithToken(t, "cors-pre")
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodOptions, "/data/"+slug+"/todos", nil)
	req.Header.Set("Origin", "https://myapp.com")
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status: %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://myapp.com" {
		t.Errorf("CORS origin not echoed: %q", got)
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "DELETE") {
		t.Errorf("CORS methods missing DELETE: %q", w.Header().Get("Access-Control-Allow-Methods"))
	}
}

func TestPhoneData_QueryParamAuthFallback(t *testing.T) {
	setupPhoneTestHome(t)
	slug, tok := setupDataProjectWithToken(t, "qparam-auth")
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet,
		"/data/"+slug+"/todos?api_key="+tok, nil)
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("query-param auth should work, got %d %s", w.Code, w.Body.String())
	}
}

func TestPhoneData_BadPath(t *testing.T) {
	setupPhoneTestHome(t)
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/data/onlyslug", nil)
	w := httptest.NewRecorder()
	srv.phoneDataRouter(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestExtractPhoneProjectToken_PriorityOrder(t *testing.T) {
	// Bearer wins over X-API-Key wins over ?api_key=.
	r := httptest.NewRequest(http.MethodGet, "/?api_key=pp_q_1", nil)
	r.Header.Set("Authorization", "Bearer pp_h_1")
	r.Header.Set("X-API-Key", "pp_x_1")
	if got := extractPhoneProjectToken(r); got != "pp_h_1" {
		t.Errorf("bearer should win: %q", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/?api_key=pp_q_1", nil)
	r.Header.Set("X-API-Key", "pp_x_1")
	if got := extractPhoneProjectToken(r); got != "pp_x_1" {
		t.Errorf("x-api-key should win over query: %q", got)
	}
	// Non-pp_ prefix is ignored.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer some-other-token")
	if got := extractPhoneProjectToken(r); got != "" {
		t.Errorf("non-pp_ bearer should be ignored: %q", got)
	}
}
