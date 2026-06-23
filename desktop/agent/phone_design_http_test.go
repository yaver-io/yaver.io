package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Verifies the relay route /phone/projects/design end-to-end at the HTTP layer:
// POST patches → GET reads them back from app.yaml → whole-design replace clears
// prior overrides. This is the server side of the web "Design studio" over relay.
func TestPhoneDesignHTTPRoundTrip(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Design HTTP", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	srv := &HTTPServer{}

	// POST patches: set quickadd margin + move quickadd to the end.
	post := func(body string) *PhoneDesign {
		t.Helper()
		req := httptest.NewRequest("POST", "/phone/projects/design", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.handlePhoneDesign(w, req)
		if w.Code != 200 {
			t.Fatalf("POST code %d: %s", w.Code, w.Body.String())
		}
		var d PhoneDesign
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return &d
	}

	d := post(`{"slug":"` + p.Slug + `","patches":[{"op":"set","nodeId":"quickadd","props":{"marginTop":24}},{"op":"move","nodeId":"quickadd","beforeId":""}]}`)
	if d.UI["quickadd"].MarginTop != 24 {
		t.Fatalf("marginTop not applied: %+v", d.UI)
	}
	if len(d.Layout) == 0 || d.Layout[len(d.Layout)-1] != "quickadd" {
		t.Fatalf("quickadd not moved to end: %v", d.Layout)
	}

	// GET must read the SAME design back (persisted to app.yaml on disk).
	req := httptest.NewRequest("GET", "/phone/projects/design?slug="+p.Slug, nil)
	w := httptest.NewRecorder()
	srv.handlePhoneDesign(w, req)
	if w.Code != 200 {
		t.Fatalf("GET code %d", w.Code)
	}
	var got PhoneDesign
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET decode: %v", err)
	}
	if got.UI["quickadd"].MarginTop != 24 {
		t.Fatalf("design did not persist across GET: %+v", got.UI)
	}

	// Whole-design replace must drop the prior quickadd override.
	d2 := post(`{"slug":"` + p.Slug + `","design":{"ui":{"title":{"hidden":true}}}}`)
	if !d2.UI["title"].Hidden {
		t.Fatalf("whole-design replace failed: %+v", d2.UI)
	}
	if _, ok := d2.UI["quickadd"]; ok {
		t.Fatalf("whole-design replace should have cleared the old quickadd override: %+v", d2.UI)
	}

	// Missing slug → 400.
	bad := httptest.NewRequest("POST", "/phone/projects/design", strings.NewReader(`{"patches":[]}`))
	bw := httptest.NewRecorder()
	srv.handlePhoneDesign(bw, bad)
	if bw.Code != 400 {
		t.Fatalf("expected 400 for missing slug, got %d", bw.Code)
	}
}
