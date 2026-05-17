package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Phase A (BYO self-hosted Hetzner). These exercise the agent-side
// provisioner whose token is the USER's own vault-backed Hetzner
// account (provisionHetzner / mcpCloudDestroy) — distinct from the
// managed-cloud Convex path (Yaver's platform token + LemonSqueezy
// gate). Real httptest fake Hetzner API, no mocks, no 100s boot wait.

type fakeHetzner struct {
	mu       sync.Mutex
	created  bool
	snapshot bool
	deleted  bool
	srv      *httptest.Server
}

func newFakeHetzner(t *testing.T) *fakeHetzner {
	t.Helper()
	f := &fakeHetzner{}
	mux := http.NewServeMux()
	mux.HandleFunc("/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("create: missing bearer token")
		}
		f.mu.Lock()
		f.created = true
		f.mu.Unlock()
		w.Write([]byte(`{"server":{"id":4242,"public_net":{"ipv4":{"ip":"203.0.113.7"}}}}`))
	})
	mux.HandleFunc("/servers/4242/actions/create_image", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.snapshot = true
		f.mu.Unlock()
		w.Write([]byte(`{"image":{"id":1}}`))
	})
	mux.HandleFunc("/servers/4242", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			f.mu.Lock()
			f.deleted = true
			f.mu.Unlock()
			w.WriteHeader(204)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func withFakeHetzner(t *testing.T, f *fakeHetzner) {
	t.Helper()
	origBase, origSkip := hetznerAPIBase, hetznerSkipReadyWait
	hetznerAPIBase = f.srv.URL
	hetznerSkipReadyWait = true
	t.Cleanup(func() { hetznerAPIBase = origBase; hetznerSkipReadyWait = origSkip })
}

func TestHetznerProvisionerRegistered(t *testing.T) {
	if _, ok := provisionerRegistry()[HostHetzner]; !ok {
		t.Fatal("HostHetzner must be in provisionerRegistry (Phase A re-enable)")
	}
}

func TestHetznerCreateReturnsIDAndIP(t *testing.T) {
	f := newFakeHetzner(t)
	withFakeHetzner(t, f)
	m, err := NewCloudDeployManager(".")
	if err != nil {
		t.Fatal(err)
	}
	ip, id, err := m.hetznerCreateServer("tok-byo", "box1", "starter", "eu")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ip != "203.0.113.7" || id != "4242" {
		t.Fatalf("create returned ip=%q id=%q, want 203.0.113.7 / 4242", ip, id)
	}
}

func TestCloudDestroyRequiresConfirm(t *testing.T) {
	res := mcpCloudDestroy(string(HostHetzner), "4242", `{}`)
	m, _ := res.(map[string]interface{})
	if m == nil || !strings.Contains(errStr(m), "confirm=true") {
		t.Fatalf("destroy without confirm must error on confirm=true; got %v", res)
	}
}

func TestCloudDestroyWrongHostRejected(t *testing.T) {
	res := mcpCloudDestroy("vercel", "x", `{"confirm":"true"}`)
	if m, _ := res.(map[string]interface{}); m == nil || m["error"] == nil {
		t.Fatalf("cloud_destroy must reject non-hetzner host; got %v", res)
	}
}

// helper: stringify the error map value for substring checks
func errStr(m map[string]interface{}) string {
	if e, ok := m["error"].(string); ok {
		return e
	}
	return ""
}
