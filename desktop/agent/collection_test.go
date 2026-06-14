package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func invokeVerb(t *testing.T, name string, payload interface{}) OpsResult {
	t.Helper()
	opsRegistryMu.RLock()
	spec, ok := opsRegistry[name]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatalf("verb %q not registered", name)
	}
	var raw json.RawMessage
	if payload != nil {
		b, _ := json.Marshal(payload)
		raw = b
	}
	return spec.Handler(OpsContext{Ctx: context.Background()}, raw)
}

func mustOK(t *testing.T, res OpsResult, label string) map[string]interface{} {
	t.Helper()
	if !res.OK {
		t.Fatalf("%s failed: code=%s err=%s", label, res.Code, res.Error)
	}
	m, _ := res.Initial.(map[string]interface{})
	return m
}

// TestCollectionPrivacyGuard proves an egress/client IP cannot be smuggled into
// a normalized observation row — it is vantage provenance, not data.
func TestCollectionPrivacyGuard(t *testing.T) {
	resetCollectionStoreForTest("")
	res := invokeVerb(t, "collection_observe", map[string]interface{}{
		"sourceId":  "s1",
		"vantageId": "v1",
		"fields":    map[string]interface{}{"price": 19.99, "egressIp": "203.0.113.10"},
	})
	if res.OK {
		t.Fatal("observation with an egressIp field must be refused")
	}
	if res.Code != "forbidden_field" {
		t.Fatalf("expected code forbidden_field, got %q (%s)", res.Code, res.Error)
	}

	// The same row without the IP is accepted.
	ok := invokeVerb(t, "collection_observe", map[string]interface{}{
		"sourceId":  "s1",
		"vantageId": "v1",
		"fields":    map[string]interface{}{"price": 19.99},
	})
	mustOK(t, ok, "clean observe")
}

// TestCollectionVantageAutoEgress proves collection_vantage_register fills a
// machine_native vantage from this runtime's egress (composing runtime_egress).
func TestCollectionVantageAutoEgress(t *testing.T) {
	resetCollectionStoreForTest("")
	resetEgressCache()

	autoPublicIPCache.mu.Lock()
	autoPublicIPCache.ip = "203.0.113.10"
	autoPublicIPCache.ts = time.Now()
	autoPublicIPCache.mu.Unlock()
	defer resetAutoPublicIPCache()

	orig := resolveEgressGeo
	resolveEgressGeo = func(ctx context.Context, ip string) (EgressIdentity, bool) {
		return EgressIdentity{Country: "US", Region: "us", ASN: "AS15169", StableKnown: true, Stable: true}, true
	}
	defer func() { resolveEgressGeo = orig }()

	m := mustOK(t, invokeVerb(t, "collection_vantage_register", map[string]interface{}{
		"runtimeId":    "local",
		"egressPolicy": "machine_native",
	}), "vantage register")

	v, _ := m["vantage"].(*CollectionVantage)
	if v == nil {
		t.Fatal("no vantage returned")
	}
	if v.EgressIP != "203.0.113.10" || v.EgressGeo != "us" || v.EgressCountry != "US" {
		t.Fatalf("egress not auto-filled: %+v", v)
	}
}

// TestCollectionSourceHealthPerVantage proves health is keyed by (source,vantage):
// a geo-block on one vantage does not poison the other.
func TestCollectionSourceHealthPerVantage(t *testing.T) {
	resetCollectionStoreForTest("")

	src := mustOK(t, invokeVerb(t, "collection_source_register", map[string]interface{}{"name": "Widget", "kind": "public_web"}), "src")
	srcRow, _ := src["source"].(*CollectionSource)
	sid := srcRow.SourceID

	// vantage US: ok. vantage TR: geo-blocked.
	mustOK(t, invokeVerb(t, "collection_run_record", map[string]interface{}{
		"sourceId": sid, "vantageId": "v_us", "status": "ok", "rowsExtracted": 1,
	}), "run us")
	mustOK(t, invokeVerb(t, "collection_run_record", map[string]interface{}{
		"sourceId": sid, "vantageId": "v_tr", "blockKind": "geo",
	}), "run tr")

	health := mustOK(t, invokeVerb(t, "collection_source_health", map[string]interface{}{"sourceId": sid}), "health")
	rows, _ := health["health"].([]map[string]interface{})
	if len(rows) != 2 {
		t.Fatalf("expected 2 health rows, got %d", len(rows))
	}
	states := map[string]string{}
	for _, r := range rows {
		states[r["vantageId"].(string)] = r["state"].(string)
		if r["vantageId"] == "v_tr" {
			if c, _ := r["geoBlockCount24h"].(int); c != 1 {
				t.Fatalf("expected geoBlockCount24h=1 for v_tr, got %v", r["geoBlockCount24h"])
			}
		}
	}
	if states["v_us"] != "healthy" {
		t.Fatalf("v_us should be healthy, got %q", states["v_us"])
	}
	if states["v_tr"] != "blocked_geo" {
		t.Fatalf("v_tr should be blocked_geo, got %q", states["v_tr"])
	}

	// block_list surfaces only the blocked vantage.
	bl := mustOK(t, invokeVerb(t, "block_list", map[string]interface{}{"sourceId": sid}), "block_list")
	if c, _ := bl["count"].(int); c != 1 {
		t.Fatalf("block_list count = %v, want 1", bl["count"])
	}
}

// TestCollectionMultiVantageCompare is the payoff: collect the same source from
// two vantages and diff, with a third vantage geo-blocked and surfaced as such.
func TestCollectionMultiVantageCompare(t *testing.T) {
	resetCollectionStoreForTest("")

	src := mustOK(t, invokeVerb(t, "collection_source_register", map[string]interface{}{"name": "Price", "kind": "public_web"}), "src")
	sid := src["source"].(*CollectionSource).SourceID

	// Register three vantages with explicit egress (eu, us, tr).
	for _, v := range []map[string]interface{}{
		{"vantageId": "v_eu", "runtimeId": "eu_box", "egressPolicy": "machine_native", "egressIp": "203.0.113.1", "egressGeo": "eu", "egressCountry": "DE"},
		{"vantageId": "v_us", "runtimeId": "us_box", "egressPolicy": "peer_egress", "egressIp": "198.51.100.2", "egressGeo": "us", "egressCountry": "US", "viaPeer": "us_box"},
		{"vantageId": "v_tr", "runtimeId": "tr_box", "egressPolicy": "machine_native", "egressIp": "192.0.2.3", "egressGeo": "ap", "egressCountry": "TR"},
	} {
		mustOK(t, invokeVerb(t, "collection_vantage_register", v), "vantage")
	}

	// EU and US see a price; TR is geo-blocked (record the block, store no row).
	mustOK(t, invokeVerb(t, "collection_observe", map[string]interface{}{
		"sourceId": sid, "vantageId": "v_eu", "dataset": "prices", "fields": map[string]interface{}{"price": 19.99, "currency": "EUR"},
	}), "obs eu")
	mustOK(t, invokeVerb(t, "collection_observe", map[string]interface{}{
		"sourceId": sid, "vantageId": "v_us", "dataset": "prices", "fields": map[string]interface{}{"price": 17.99, "currency": "USD"},
	}), "obs us")
	mustOK(t, invokeVerb(t, "collection_run_record", map[string]interface{}{
		"sourceId": sid, "vantageId": "v_tr", "blockKind": "geo", "collectorType": "browser_visible_dom",
	}), "run tr blocked")

	cmp := mustOK(t, invokeVerb(t, "collection_vantage_compare", map[string]interface{}{
		"sourceId": sid, "dataset": "prices",
	}), "compare")

	// Re-marshal for typed navigation.
	raw, _ := json.Marshal(cmp)
	var parsed struct {
		Fields   []string `json:"fields"`
		Vantages []struct {
			VantageID string                 `json:"vantageId"`
			EgressGeo string                 `json:"egressGeo"`
			State     string                 `json:"state"`
			Values    map[string]interface{} `json:"values"`
		} `json:"vantages"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse compare: %v", err)
	}

	if len(parsed.Vantages) != 3 {
		t.Fatalf("expected 3 vantages in compare, got %d", len(parsed.Vantages))
	}
	byID := map[string]struct {
		geo, state string
		price      interface{}
	}{}
	for _, v := range parsed.Vantages {
		byID[v.VantageID] = struct {
			geo, state string
			price      interface{}
		}{v.EgressGeo, v.State, v.Values["price"]}
	}
	if byID["v_eu"].price != 19.99 {
		t.Fatalf("v_eu price = %v, want 19.99", byID["v_eu"].price)
	}
	if byID["v_us"].price != 17.99 {
		t.Fatalf("v_us price = %v, want 17.99", byID["v_us"].price)
	}
	if byID["v_tr"].state != "blocked_geo" {
		t.Fatalf("v_tr should be surfaced as blocked_geo, got %q", byID["v_tr"].state)
	}
	if byID["v_tr"].price != nil {
		t.Fatalf("v_tr should have no price (blocked), got %v", byID["v_tr"].price)
	}
}

func TestCollectionVerbsRegistered(t *testing.T) {
	for _, name := range []string{
		"collection_source_register", "collection_vantage_register", "collection_run_record",
		"collection_observe", "collection_dataset_query", "collection_source_health",
		"collection_vantage_compare", "block_list",
	} {
		opsRegistryMu.RLock()
		spec, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Errorf("verb %q not registered", name)
			continue
		}
		if spec.AllowGuest {
			t.Errorf("verb %q must be owner-only", name)
		}
	}
}
