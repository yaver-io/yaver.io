package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Verifies the beta pool controller PAUSES (poweroff, never delete) and RESUMES
// (poweron) the persistent beta box via the Hetzner API — the user's directive:
// "relay will down it if nobody uses (not delete, power off)".
func TestBetaPoolPowerOffOnIdle(t *testing.T) {
	var mu sync.Mutex
	status := "running"
	var actions []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/servers"):
			if r.URL.Query().Get("name") != "yaver-beta-cloud" {
				t.Errorf("queried wrong box: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"servers":[{"id":42,"status":"` + status + `","public_net":{"ipv4":{"ip":"1.2.3.4"}}}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/actions/poweroff"):
			actions = append(actions, "poweroff")
			status = "off"
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"action":{"status":"running"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/actions/poweron"):
			actions = append(actions, "poweron")
			status = "running"
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"action":{"status":"running"}}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := &betaPoolController{
		hcloudToken:  "test-token",
		betaBoxName:  "yaver-beta-cloud",
		hcloudAPIURL: srv.URL,
		httpc:        &http.Client{Timeout: 5 * time.Second},
	}

	// reap = power off (NOT delete): no DELETE endpoint exists; if reap tried to
	// delete, the stub would 400.
	if err := c.reap(""); err != nil {
		t.Fatalf("reap (power off) failed: %v", err)
	}
	mu.Lock()
	if len(actions) != 1 || actions[0] != "poweroff" {
		t.Fatalf("expected exactly one poweroff, got %v", actions)
	}
	if status != "off" {
		t.Fatalf("box should be off after reap, got %s", status)
	}
	mu.Unlock()

	// idempotent: a second reap on an already-off box issues no further action.
	if err := c.reap(""); err != nil {
		t.Fatalf("second reap failed: %v", err)
	}
	mu.Lock()
	if len(actions) != 1 {
		t.Fatalf("reap on already-off box must be a no-op, got %v", actions)
	}
	mu.Unlock()

	// provision = power on (resume).
	ip, err := c.provision()
	if err != nil {
		t.Fatalf("provision (power on) failed: %v", err)
	}
	if ip != "1.2.3.4" {
		t.Errorf("expected box ip, got %q", ip)
	}
	mu.Lock()
	if actions[len(actions)-1] != "poweron" {
		t.Fatalf("expected poweron, got %v", actions)
	}
	mu.Unlock()
}
