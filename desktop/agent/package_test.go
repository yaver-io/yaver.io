package main

// package_test.go — real-server tests for Yaver Task Packages. No mocks; the
// MCP-over-MCP test stands up an actual JSON-RPC-over-HTTP MCP server (the shape
// a yaver-bet MCP exposes) and proves a package can call it and capture the
// result. Egress is stubbed via pkgDetectEgress so tests stay hermetic.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func stubEgress(t *testing.T) {
	t.Helper()
	prev := pkgDetectEgress
	pkgDetectEgress = func(ctx context.Context, cfg *Config, refresh bool) EgressIdentity {
		return EgressIdentity{IP: "203.0.113.7", Country: "RS", Region: "eu", ASN: "AS00", Source: "test"}
	}
	t.Cleanup(func() { pkgDetectEgress = prev })
}

func ownerCtx() OpsContext { return OpsContext{Caller: "owner", ActorUserID: "u_test"} }

func runVerb(t *testing.T, h VerbHandler, payload interface{}) OpsResult {
	t.Helper()
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		raw = b
	}
	return h(ownerCtx(), raw)
}

func TestPackagePublishListGet(t *testing.T) {
	resetPackageStoreForTest("")

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "price-watch", "description": "watch a price"},
		"spec": map[string]interface{}{
			"task": map[string]interface{}{
				"kind":    "collect",
				"sources": []map[string]interface{}{{"id": "p", "url": "http://example.test"}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}

	r := runVerb(t, packageListHandler, nil)
	if !r.OK {
		t.Fatalf("list: %s", r.Error)
	}
	got := r.Initial.(map[string]interface{})
	if got["count"].(int) != 1 {
		t.Fatalf("want 1 package, got %v", got["count"])
	}

	g := runVerb(t, packageGetHandler, map[string]interface{}{"name": "price-watch"})
	if !g.OK {
		t.Fatalf("get: %s", g.Error)
	}
	if tier := g.Initial.(map[string]interface{})["tier"].(string); tier != "read_only" {
		t.Fatalf("collect package should be read_only, got %q", tier)
	}
}

func TestPackagePublishRejectsEmptyTask(t *testing.T) {
	resetPackageStoreForTest("")
	bad := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "empty"},
		"spec":     map[string]interface{}{"task": map[string]interface{}{"kind": "collect"}},
	}
	if r := runVerb(t, packagePublishHandler, bad); r.OK {
		t.Fatalf("expected validation failure for a task with no sources/steps/mcp/goal")
	}
}

// TestPackageRunMCPOverMCP is the headline: a package uses an existing MCP
// server (yaver-bet style) to do work, and the result is captured into the run.
func TestPackageRunMCPOverMCP(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	var gotMethod, gotTool string
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMethod, gotTool = req.Method, req.Params.Name
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]interface{}{"tool": req.Params.Name, "fair_odds": 2.1},
		})
	}))
	defer mcp.Close()

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "odds"},
		"spec": map[string]interface{}{
			"task": map[string]interface{}{
				"kind":    "collect",
				"engines": []string{"mcp"},
				"mcp": []map[string]interface{}{{
					"name": "yaver-bet", "url": mcp.URL,
					"tool":      "live_play_recommend",
					"arguments": map[string]interface{}{"match": "X"},
					"as":        "rec",
				}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}

	r := runVerb(t, packageRunHandler, map[string]interface{}{"name": "odds"})
	if !r.OK {
		t.Fatalf("run: %s", r.Error)
	}
	res := r.Initial.(map[string]interface{})["run"].(PackageRunResult)
	if res.Status != "ok" {
		t.Fatalf("run status = %q, notes=%v", res.Status, res.Notes)
	}
	if gotMethod != "tools/call" || gotTool != "live_play_recommend" {
		t.Fatalf("MCP not called correctly: method=%q tool=%q", gotMethod, gotTool)
	}
	rec, ok := res.Fields["rec"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected captured mcp result under 'rec', got %#v", res.Fields["rec"])
	}
	if rec["fair_odds"].(float64) != 2.1 {
		t.Fatalf("captured fair_odds = %v, want 2.1", rec["fair_odds"])
	}
	if len(res.MCPCalls) != 1 || res.MCPCalls[0]["ok"] != true {
		t.Fatalf("expected 1 ok mcp call, got %#v", res.MCPCalls)
	}
	if res.ObservationID == "" {
		t.Fatalf("expected the run to store a vantage-tagged observation")
	}
}

// TestPackageRunLocalVerbMCP proves MCP-over-MCP can also compose a LOCAL Yaver
// ops verb (not just a remote server).
func TestPackageRunLocalVerbMCP(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "compose-local"},
		"spec": map[string]interface{}{
			"task": map[string]interface{}{
				"kind":    "collect",
				"engines": []string{"mcp"},
				"mcp":     []map[string]interface{}{{"name": "local-list", "verb": "package_list", "as": "pkgs"}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}
	r := runVerb(t, packageRunHandler, map[string]interface{}{"name": "compose-local"})
	if !r.OK {
		t.Fatalf("run: %s", r.Error)
	}
	res := r.Initial.(map[string]interface{})["run"].(PackageRunResult)
	if res.Status != "ok" {
		t.Fatalf("status=%q notes=%v", res.Status, res.Notes)
	}
	if _, ok := res.Fields["pkgs"]; !ok {
		t.Fatalf("expected local verb result under 'pkgs', got %#v", res.Fields)
	}
}

func TestPackageRunFetchAndExtract(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"price":"19.99","stock":"in_stock"}}`))
	}))
	defer srv.Close()

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "fetcher"},
		"spec": map[string]interface{}{
			"task": map[string]interface{}{
				"kind":    "collect",
				"engines": []string{"fetch"},
				"sources": []map[string]interface{}{{
					"id": "sku", "url": srv.URL, "render": "fetch",
					"extract": map[string]interface{}{
						"price":        map[string]interface{}{"jsonPath": "data.price", "as": "number"},
						"availability": map[string]interface{}{"jsonPath": "data.stock", "as": "text"},
					},
				}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}
	r := runVerb(t, packageRunHandler, map[string]interface{}{"name": "fetcher"})
	if !r.OK {
		t.Fatalf("run: %s", r.Error)
	}
	res := r.Initial.(map[string]interface{})["run"].(PackageRunResult)
	if res.Status != "ok" || res.SourcesOk != 1 {
		t.Fatalf("status=%q sourcesOk=%d notes=%v", res.Status, res.SourcesOk, res.Notes)
	}
	if res.Fields["price"].(float64) != 19.99 {
		t.Fatalf("price = %v (want 19.99 coerced to number)", res.Fields["price"])
	}
	if res.Fields["availability"] != "in_stock" {
		t.Fatalf("availability = %v", res.Fields["availability"])
	}
}

func TestPackageActingTierRequiresConfirm(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "do-thing"},
		"spec": map[string]interface{}{
			"task": map[string]interface{}{
				"kind":  "operate",
				"steps": []map[string]interface{}{{"run": "echo hi"}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}
	// without confirm -> gated
	r := runVerb(t, packageRunHandler, map[string]interface{}{"name": "do-thing"})
	res := r.Initial.(map[string]interface{})["run"].(PackageRunResult)
	if res.Status != "needs_confirmation" {
		t.Fatalf("acting package must gate without confirm, got %q", res.Status)
	}
}

func TestPackageAllocateMobileRejectsBrowserEngine(t *testing.T) {
	resetPackageStoreForTest("")
	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "pw"},
		"spec": map[string]interface{}{
			"task": map[string]interface{}{
				"kind": "collect", "engines": []string{"playwright"},
				"steps": []map[string]interface{}{{"run": "node x.js"}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}
	// force=true skips the preflight gate so this test exercises engine eligibility.
	r := runVerb(t, packageAllocateHandler, map[string]interface{}{
		"packageName": "pw", "device": "dev1", "target": "mobile", "force": true,
	})
	if r.OK {
		t.Fatalf("playwright package must not allocate to mobile target")
	}
	if r2 := runVerb(t, packageAllocateHandler, map[string]interface{}{
		"packageName": "pw", "device": "box1", "target": "agent", "force": true,
	}); !r2.OK {
		t.Fatalf("playwright package should allocate to agent: %s", r2.Error)
	}
}

func TestPackageCheckGatesSharing(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"v":42}`))
	}))
	defer srv.Close()

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "ok-pkg"},
		"spec": map[string]interface{}{
			"consent": map[string]interface{}{"summary": "fetch a public number"},
			"task": map[string]interface{}{
				"kind": "collect", "engines": []string{"fetch"},
				"sources": []map[string]interface{}{{
					"id": "n", "url": srv.URL, "render": "fetch",
					"extract": map[string]interface{}{"v": map[string]interface{}{"jsonPath": "v", "as": "number"}},
				}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}

	// share before preflight -> blocked
	pre := runVerb(t, packageAllocateHandler, map[string]interface{}{"packageName": "ok-pkg", "device": "d1"})
	if pre.OK || pre.Code != "check_required" {
		t.Fatalf("allocate before check should be check_required, got ok=%v code=%q", pre.OK, pre.Code)
	}

	// preflight -> pass
	ch := runVerb(t, packageCheckHandler, map[string]interface{}{"name": "ok-pkg"})
	if !ch.OK {
		t.Fatalf("check: %s", ch.Error)
	}
	res := ch.Initial.(map[string]interface{})["check"].(*PackageCheckResult)
	if res.Status != "pass" {
		t.Fatalf("clean fetch package should pass preflight, got %q (%+v)", res.Status, res.Findings)
	}

	// share after a passing preflight -> allowed
	post := runVerb(t, packageAllocateHandler, map[string]interface{}{"packageName": "ok-pkg", "device": "d1"})
	if !post.OK {
		t.Fatalf("allocate after passing check should succeed: %s", post.Error)
	}
}

func TestPackageCheckFailsOnBadMCP(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	manifest := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "broken"},
		"spec": map[string]interface{}{
			"consent": map[string]interface{}{"summary": "x"},
			"task": map[string]interface{}{
				"kind": "collect", "engines": []string{"mcp"},
				"mcp": []map[string]interface{}{{"name": "dead", "url": "http://127.0.0.1:1/mcp", "tool": "t"}},
			},
		},
	}
	if r := runVerb(t, packagePublishHandler, manifest); !r.OK {
		t.Fatalf("publish: %s", r.Error)
	}
	ch := runVerb(t, packageCheckHandler, map[string]interface{}{"name": "broken"})
	res := ch.Initial.(map[string]interface{})["check"].(*PackageCheckResult)
	if res.Status != "fail" {
		t.Fatalf("unreachable MCP binding should FAIL preflight, got %q", res.Status)
	}
	// share blocked unless forced
	blocked := runVerb(t, packageAllocateHandler, map[string]interface{}{"packageName": "broken", "device": "d1"})
	if blocked.OK || blocked.Code != "check_failed" {
		t.Fatalf("allocate after failed check should be check_failed, got ok=%v code=%q", blocked.OK, blocked.Code)
	}
	forced := runVerb(t, packageAllocateHandler, map[string]interface{}{"packageName": "broken", "device": "d1", "force": true})
	if !forced.OK {
		t.Fatalf("force allocate should override the gate: %s", forced.Error)
	}
}

func TestHeuristicComposeCollect(t *testing.T) {
	p, err := heuristicComposePackage(
		"watch the public odds at https://odds.example.com/live from Serbia every 10 minutes and save them", "")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if p.Spec.Task.Kind != "collect" {
		t.Fatalf("kind = %q, want collect", p.Spec.Task.Kind)
	}
	if len(p.Spec.Vantage.Geo) != 1 || p.Spec.Vantage.Geo[0] != "RS" {
		t.Fatalf("geo = %v, want [RS]", p.Spec.Vantage.Geo)
	}
	if !p.Spec.Vantage.Residential {
		t.Fatalf("expected residential vantage")
	}
	if p.Spec.Schedule.Every != "10m" {
		t.Fatalf("schedule = %q, want 10m", p.Spec.Schedule.Every)
	}
	if len(p.Spec.Task.Sources) != 1 || p.Spec.Task.Sources[0].URL != "https://odds.example.com/live" {
		t.Fatalf("sources = %+v", p.Spec.Task.Sources)
	}
	if p.Spec.Consent.Summary == "" {
		t.Fatalf("expected a generated consent summary")
	}
}

func TestHeuristicComposeOperateIsActing(t *testing.T) {
	p, err := heuristicComposePackage("submit the form at https://x.example.com/apply", "apply-bot")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if p.Spec.Task.Kind != "operate" {
		t.Fatalf("kind = %q, want operate", p.Spec.Task.Kind)
	}
	if p.effectiveTier() != "acting" {
		t.Fatalf("operate package should be acting tier, got %q", p.effectiveTier())
	}
	hasAgent := false
	for _, r := range p.Spec.Runtimes {
		if r == "agent" {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatalf("acting package should run on agent/docker, got runtimes %v", p.Spec.Runtimes)
	}
}

func TestPackageComposeVerbPublishesAndChecks(t *testing.T) {
	resetPackageStoreForTest("")
	resetCollectionStoreForTest("")
	stubEgress(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	goal := "collect the public page at " + srv.URL + " from Serbia every 10 minutes"
	r := runVerb(t, packageComposeHandler, map[string]interface{}{"goal": goal})
	if !r.OK {
		t.Fatalf("compose verb: %s", r.Error)
	}
	out := r.Initial.(map[string]interface{})
	pkg := out["package"].(*TaskPackage)
	if pkg.Metadata.Name == "" {
		t.Fatalf("composed package has no name")
	}
	chk := out["check"].(*PackageCheckResult)
	if chk.Status == "fail" {
		t.Fatalf("composed package should not FAIL preflight against a live url: %+v", chk.Findings)
	}
	// it must be published (listable)
	lst := runVerb(t, packageListHandler, nil).Initial.(map[string]interface{})
	if lst["count"].(int) != 1 {
		t.Fatalf("composed package not published; list count=%v", lst["count"])
	}
}
