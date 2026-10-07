package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConnectivityStatusRequiresRunningTunnel(t *testing.T) {
	text := connectivityStatusText(map[string]interface{}{"enabled": true}, time.Now())
	if !strings.HasPrefix(text, "Disconnected.") {
		t.Fatal(text)
	}
}
func TestConnectivityStatusExpiresOldHandshake(t *testing.T) {
	now := time.Unix(2000, 0)
	for _, tc := range []struct {
		at   float64
		want string
	}{{0, "idle"}, {1000, "idle"}, {1990, "active"}, {3000, "idle"}} {
		text := connectivityStatusText(map[string]interface{}{"dataPlane": map[string]interface{}{"running": true, "selfIp": "test", "peers": []interface{}{map[string]interface{}{"LastHandshakeUnix": tc.at}}}}, now)
		if !strings.Contains(text, tc.want) {
			t.Fatal(text)
		}
	}
}
func TestMeshMutationsRejectGET(t *testing.T) {
	s := &HTTPServer{}
	for _, handler := range []http.HandlerFunc{s.handleMeshUp, s.handleMeshDown} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/mesh/up", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal(w.Code)
		}
	}
}
