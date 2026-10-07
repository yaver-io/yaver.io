package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginBoundary(t *testing.T) {
	h := handler("http://localhost:8094")
	for _, origin := range []string{"", "https://attacker.example", "http://localhost:8095", "http://localhost:8094.attacker.example"} {
		r := httptest.NewRequest("POST", "http://localhost/invoke", strings.NewReader(`{"op":"closeAll"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("origin %q allowed: %d", origin, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/invoke", strings.NewReader(`{"op":"read","id":"missing"}`))
	r.Header.Set("Origin", "http://localhost:8094")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "SSH_DISCONNECTED") {
		t.Fatalf("approved origin: %d %s", w.Code, w.Body.String())
	}
}
