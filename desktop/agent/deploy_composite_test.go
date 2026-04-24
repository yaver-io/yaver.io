package main

// Tests for the server-side composite /deploy/ship path. The SSE
// end-to-end test is the important one: it proves per-target
// goroutines multiplex cleanly into a single stream without
// corrupted SSE framing, and that the final `composite` event
// summarises every target. Unit tests cover the helpers
// (normaliseTargetList + resolveDeployStackPath).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortSleep is 20 ms — tight enough to keep tests fast, loose
// enough that a goroutine scheduler has a chance to make progress.
func shortSleep() { time.Sleep(20 * time.Millisecond) }

func TestNormaliseTargetList(t *testing.T) {
	cases := []struct {
		name    string
		targets []string
		target  string
		want    []string
	}{
		{"empty both", nil, "", nil},
		{"single only", nil, "testflight", []string{"testflight"}},
		{"plural wins over singular", []string{"cloudflare"}, "testflight", []string{"cloudflare"}},
		{"dedupes + trims", []string{"  a", "a", "b ", ""}, "", []string{"a", "b"}},
		{"whitespace-only singular is ignored", nil, "   ", nil},
	}
	for _, c := range cases {
		got := normaliseTargetList(c.targets, c.target)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: idx %d got %q want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestResolveDeployStackPathExplicit(t *testing.T) {
	stack, path, err := resolveDeployStackPath("someapp", "nextjs", "/abs/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stack != "nextjs" || path != "/abs/path" {
		t.Errorf("expected passthrough, got stack=%q path=%q", stack, path)
	}
}

func TestResolveDeployStackPathMissing(t *testing.T) {
	_, _, err := resolveDeployStackPath("nonexistent", "", "")
	if err == nil {
		t.Fatal("expected error for unresolvable app")
	}
	if !strings.Contains(err.Error(), "could not resolve stack") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestDeployShipCompositeEndToEnd(t *testing.T) {
	// Register two disposable templates for (test-stack, t1) and
	// (test-stack, t2) whose bodies are simple echoes. The SSE
	// response should contain meta+line+exit events for BOTH, plus
	// a final composite event summarising them.
	for _, tgt := range []string{"t-fast", "t-slow"} {
		key := "test-stack:" + tgt
		orig := deployTemplates[key]
		deployTemplates[key] = deployTemplate{
			Stack:  "test-stack",
			Target: tgt,
			Body:   `cd "{{.Path}}" && echo "hello-from-{{.Target}}"` + "\n",
		}
		defer func(k string, orig deployTemplate) {
			if orig.Target == "" {
				delete(deployTemplates, k)
			} else {
				deployTemplates[k] = orig
			}
		}(key, orig)
		// Register a matching doctor target.
		origT := buildTargets[tgt]
		buildTargets[tgt] = buildTarget{
			Name:  tgt,
			Tools: []buildTool{{Name: "bash", VersionFlag: "--version", Required: true}},
		}
		defer func(k string, orig buildTarget) {
			if orig.Name == "" {
				delete(buildTargets, k)
			} else {
				buildTargets[k] = orig
			}
		}(tgt, origT)
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "/bin:/usr/bin")
	os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)

	// Workspace manifest so the handler can resolve stack + path.
	writeTestWorkspace(t, tmp, "compositeapp", "test-stack", "src")
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	_ = os.Chdir(tmp)

	vs, _ := NewVaultStoreWithDevice("p", "dev")
	srv := &HTTPServer{token: "t", vaultStore: vs}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleDeployShip))
	defer ts.Close()

	body := `{"app":"compositeapp","targets":["t-fast","t-slow"],"timeout_sec":30}`
	req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, raw)
	}

	events := readSSE(resp.Body)
	byType := map[string][]sseEvent{}
	for _, e := range events {
		byType[e.Event] = append(byType[e.Event], e)
	}
	if len(byType["meta"]) != 2 {
		t.Errorf("expected 2 meta events (one per target), got %d: %+v", len(byType["meta"]), byType["meta"])
	}
	// Every meta + line + exit event must carry a `target` field in
	// composite mode.
	for _, e := range events {
		if e.Event != "composite" && e.Event != "error" {
			if tgt, _ := e.Data["target"].(string); tgt == "" {
				t.Errorf("event %q missing `target` field: %+v", e.Event, e.Data)
			}
		}
	}
	exits := byType["exit"]
	if len(exits) != 2 {
		t.Fatalf("expected 2 exit events, got %d", len(exits))
	}
	for _, e := range exits {
		if code, _ := e.Data["code"].(float64); code != 0 {
			t.Errorf("expected exit code 0, got %v", code)
		}
	}
	composites := byType["composite"]
	if len(composites) != 1 {
		t.Fatalf("expected 1 composite event, got %d", len(composites))
	}
	comp := composites[0].Data
	summary, ok := comp["summary"].([]interface{})
	if !ok || len(summary) != 2 {
		t.Fatalf("composite.summary should have 2 entries, got %v", summary)
	}
	if allOK, _ := comp["all_ok"].(bool); !allOK {
		t.Errorf("expected all_ok=true in composite: %+v", comp)
	}
	// Sanity: content of each target's output landed under the right
	// target marker.
	seenHello := map[string]bool{}
	for _, e := range byType["line"] {
		text, _ := e.Data["text"].(string)
		tgt, _ := e.Data["target"].(string)
		if text == "hello-from-"+tgt {
			seenHello[tgt] = true
		}
	}
	if !seenHello["t-fast"] || !seenHello["t-slow"] {
		t.Errorf("expected hello lines for both targets, got %v", seenHello)
	}
}

func TestDeployShipRejectsEmptyBoth(t *testing.T) {
	srv := &HTTPServer{token: "t"}
	// Neither target nor targets present.
	body := `{"app":"x"}`
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when both target/targets missing, got %d", w.Code)
	}
}

func TestDeployWebhookPerTargetFilter(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)
	cfg := &Config{
		DeployWebhookURL: srv.URL,
		DeployWebhookOn:  "all",
		DeployWebhookOnByTarget: map[string]string{
			"testflight": "failure", // only fire on fail
			// cloudflare not listed → falls back to DeployWebhookOn ("all")
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// testflight success — per-target filter says "failure" only,
	// so this must NOT fire.
	FireDeployWebhook(DeployRun{Target: "testflight", OK: true})
	waitForHits(t, &hits, 0, 500)
	if hits != 0 {
		t.Errorf("expected 0 hits for testflight success, got %d", hits)
	}

	// testflight failure — per-target filter matches, fires.
	FireDeployWebhook(DeployRun{Target: "testflight", OK: false})
	waitForHits(t, &hits, 1, 1500)
	if hits != 1 {
		t.Errorf("expected 1 hit for testflight failure, got %d", hits)
	}

	// cloudflare success — falls through to global "all", fires.
	FireDeployWebhook(DeployRun{Target: "cloudflare", OK: true})
	waitForHits(t, &hits, 2, 1500)
	if hits != 2 {
		t.Errorf("expected 2 hits after cloudflare success, got %d", hits)
	}
}

// waitForHits polls *hits with a tiny sleep backoff until it reaches
// `want` or the timeout expires. Fire-and-forget webhooks need a
// little patience in tests.
func waitForHits(t *testing.T, hits *int, want, timeoutMs int) {
	t.Helper()
	deadline := timeoutMs
	for *hits < want && deadline > 0 {
		shortSleep()
		deadline -= 20
	}
}
