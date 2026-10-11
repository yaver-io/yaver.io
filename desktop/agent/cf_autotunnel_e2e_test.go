package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Live test: opt-in (talks to Cloudflare). YAVER_E2E_TUNNEL=1 go test -run AutoTunnelLive
func TestAutoTunnelLive(t *testing.T) {
	if getenvTest("YAVER_E2E_TUNNEL") != "1" {
		t.Skip("set YAVER_E2E_TUNNEL=1")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "yaver-ok") }))
	defer srv.Close()
	port := srv.Listener.Addr().(interface{ String() string }).String()
	var p int
	for i := len(port) - 1; i >= 0 && port[i] != ':'; i-- {
	}
	_, ps, _ := strings.Cut(port, ":")
	for _, c := range ps {
		p = p*10 + int(c-'0')
	}
	tailscaleRunning = func() bool { return false }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runAutoTunnel(ctx, &Config{}, p, false)
	deadline := time.Now().Add(150 * time.Second)
	for autoTunnelURL() == "" {
		if time.Now().After(deadline) {
			t.Fatalf("tunnel never came up: %+v", autoTunnelStatus())
		}
		time.Sleep(time.Second)
	}
	u := autoTunnelURL()
	t.Logf("tunnel up: %s", u)
	if !strings.Contains(strings.Join(configuredPublicEndpoints(&Config{}), ","), u) {
		t.Fatal("URL not in published endpoints")
	}
	var body string
	for i := 0; i < 20; i++ { // DNS for a fresh tunnel can lag a few seconds
		resp, err := http.Get(u)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			if body == "yaver-ok" {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if body != "yaver-ok" {
		t.Fatalf("round trip failed, body=%q", body)
	}
}

func getenvTest(k string) string { return os.Getenv(k) }
