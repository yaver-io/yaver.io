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

func TestConnectivityHelpDoesNotConnect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runMeshUp([]string{"--help"})
	runMeshDown([]string{"--help"})
	runConnectivityStatus([]string{"--help"})
}

func TestConnectivityStatusUsesOverlayAddressAndMeasuredPath(t *testing.T) {
	now := time.Unix(2000, 0)
	text := connectivityStatusText(map[string]interface{}{
		"selfName": "local", "selfOS": "linux",
		"dataPlane": map[string]interface{}{"running": true, "selfIp": "100.96.0.1", "peers": []interface{}{
			map[string]interface{}{"MeshIP": "100.96.0.2", "Name": "remote", "Owner": "owner", "OS": "linux", "Path": "relay", "Endpoint": "127.0.0.1:1234", "LastHandshakeUnix": float64(1999), "TxBytes": float64(10), "RxBytes": float64(20)},
		}},
	}, now)
	rows := strings.Split(strings.TrimSpace(text), "\n")
	fields := strings.Fields(rows[1])
	if fields[0] != "100.96.0.2" || fields[1] != "remote" || fields[2] != "owner" || fields[3] != "linux" {
		t.Fatal(text)
	}
	if !strings.Contains(text, "active; relay, tx 10 rx 20") || strings.Contains(text, "127.0.0.1") {
		t.Fatal(text)
	}
}
