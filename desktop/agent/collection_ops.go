package main

// collection_ops.go — generic, domain-agnostic MCP ops verbs for user-directed
// data collection. These are owned by Yaver (the collection runtime); domain
// adapters (Yaver Bet, ecommerce, QA, …) consume the resulting datasets but do
// not own the runtime. Nothing here is betting-specific.
//
// All state is local (collection_store.go); the privacy contract forbids
// collected data in Convex. Owner-only.

import (
	"context"
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "collection_source_register",
		Description: "Register or update a collection source (a page/API/app/manual feed). Returns its sourceId. Domain-agnostic.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sourceId":    map[string]interface{}{"type": "string", "description": "Existing id to update; omit to create."},
			"name":        map[string]interface{}{"type": "string"},
			"kind":        map[string]interface{}{"type": "string", "description": "public_web | official_api | app | manual"},
			"baseUrl":     map[string]interface{}{"type": "string"},
			"accessState": map[string]interface{}{"type": "string", "description": "public_allowed | official_api | manual_required | blocked_geo | ..."},
		}, "name"),
		Handler:    collectionSourceRegisterHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "collection_vantage_register",
		Description: "Register a vantage = (runtime + egress identity) that observations are collected from. " +
			"If egress fields are omitted and policy is machine_native, they are auto-filled from THIS runtime's " +
			"egress (runtime_egress). Returns its vantageId.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"vantageId":     map[string]interface{}{"type": "string", "description": "Existing id to update; omit to create."},
			"runtimeId":     map[string]interface{}{"type": "string"},
			"egressPolicy":  map[string]interface{}{"type": "string", "description": "machine_native | peer_egress | user_proxy"},
			"egressIp":      map[string]interface{}{"type": "string"},
			"egressGeo":     map[string]interface{}{"type": "string", "description": "coarse region eu|us|..."},
			"egressCountry": map[string]interface{}{"type": "string", "description": "ISO-2"},
			"egressAsn":     map[string]interface{}{"type": "string"},
			"viaPeer":       map[string]interface{}{"type": "string", "description": "device id when egress routes via a peer"},
		}),
		Handler:    collectionVantageRegisterHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "collection_run_record",
		Description: "Record a collection run against a (source, vantage) and update per-(source,vantage) health. " +
			"Set blockKind=geo|ip|rate_limit (or status=blocked_geo|blocked_ip|rate_limited) when a vantage was " +
			"blocked — this is recorded as a per-vantage block, never routed around.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sourceId":      map[string]interface{}{"type": "string"},
			"vantageId":     map[string]interface{}{"type": "string"},
			"collectorType": map[string]interface{}{"type": "string", "description": "browser_visible_dom | http_fetch | redroid | manual | ..."},
			"status":        map[string]interface{}{"type": "string", "description": "ok | no_data | blocked_geo | blocked_ip | rate_limited | parse_error"},
			"rowsExtracted": map[string]interface{}{"type": "integer"},
			"egressIpUsed":  map[string]interface{}{"type": "string"},
			"egressGeoUsed": map[string]interface{}{"type": "string"},
			"blockKind":     map[string]interface{}{"type": "string", "description": "geo | ip | rate_limit"},
			"errorCode":     map[string]interface{}{"type": "string"},
			"errorMessage":  map[string]interface{}{"type": "string"},
		}, "sourceId", "vantageId"),
		Handler:    collectionRunRecordHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "collection_observe",
		Description: "Store a normalized observation row tagged with its (source, vantage). Fields holds domain data " +
			"ONLY — an egress/client IP in fields is refused (it is vantage provenance, not data).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sourceId":  map[string]interface{}{"type": "string"},
			"vantageId": map[string]interface{}{"type": "string"},
			"runId":     map[string]interface{}{"type": "string"},
			"dataset":   map[string]interface{}{"type": "string"},
			"fields":    map[string]interface{}{"type": "object", "description": "normalized row (no egress/client IP)"},
		}, "sourceId", "vantageId", "fields"),
		Handler:    collectionObserveHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "collection_dataset_query",
		Description: "Query stored observations by dataset/source/vantage. Domain adapters read normalized rows through this.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"dataset":   map[string]interface{}{"type": "string"},
			"sourceId":  map[string]interface{}{"type": "string"},
			"vantageId": map[string]interface{}{"type": "string"},
			"limit":     map[string]interface{}{"type": "integer", "description": "max rows (default 100)"},
		}),
		Handler:    collectionDatasetQueryHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "collection_source_health",
		Description: "Per-(source, vantage) health: state + 24h geo/ip/rate block counts. A source can be healthy from one vantage and blocked_geo from another.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sourceId": map[string]interface{}{"type": "string", "description": "filter to one source (optional)"},
		}),
		Handler:    collectionSourceHealthHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "collection_vantage_compare",
		Description: "Cross-vantage diff for a source: latest observation per vantage with each vantage's egress/geo and " +
			"health state, so you can see where a source differs by region/IP (and which vantages are blocked).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sourceId": map[string]interface{}{"type": "string"},
			"dataset":  map[string]interface{}{"type": "string"},
		}, "sourceId"),
		Handler:    collectionVantageCompareHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "block_list",
		Description: "List vantages currently blocked (geo/ip/rate) across sources, with the egress/geo that saw the block. Blocks are surfaced as findings, never routed around.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sourceId": map[string]interface{}{"type": "string", "description": "filter to one source (optional)"},
		}),
		Handler:    blockListHandler,
		AllowGuest: false,
	})
}

func collectionSourceRegisterHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var src CollectionSource
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &src); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(src.Name) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
	}
	out := collStore.upsertSource(src)
	return OpsResult{OK: true, Initial: map[string]interface{}{"source": out}}
}

func collectionVantageRegisterHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var v CollectionVantage
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &v); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if v.EgressPolicy == "" {
		v.EgressPolicy = "machine_native"
	}
	// Auto-fill machine-native egress from this runtime's identity when not given.
	if v.EgressPolicy == "machine_native" && v.EgressIP == "" {
		ctx := c.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := detectEgressIdentity(ctx, mustLoadConfigBestEffort(), false)
		v.EgressIP = id.IP
		v.EgressGeo = id.Region
		v.EgressCountry = id.Country
		v.EgressASN = id.ASN
	}
	out := collStore.upsertVantage(v)
	return OpsResult{OK: true, Initial: map[string]interface{}{"vantage": out}}
}

func collectionRunRecordHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var run CollectionRun
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &run); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if run.SourceID == "" || run.VantageID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "sourceId and vantageId required"}
	}
	if run.Status == "" {
		run.Status = "ok"
	}
	out := collStore.recordRun(run)
	return OpsResult{OK: true, Initial: map[string]interface{}{"run": out}}
}

func collectionObserveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var obs CollectionObservation
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &obs); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if obs.SourceID == "" || obs.VantageID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "sourceId and vantageId required"}
	}
	if len(obs.Fields) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "fields required"}
	}
	out, err := collStore.addObservation(obs)
	if err != nil {
		return OpsResult{OK: false, Code: "forbidden_field", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"observation": out}}
}

func collectionDatasetQueryHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Dataset   string `json:"dataset"`
		SourceID  string `json:"sourceId"`
		VantageID string `json:"vantageId"`
		Limit     int    `json:"limit"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if args.Limit <= 0 {
		args.Limit = 100
	}
	rows := collStore.queryObservations(args.Dataset, args.SourceID, args.VantageID, args.Limit)
	return OpsResult{OK: true, Initial: map[string]interface{}{"observations": rows, "count": len(rows)}}
}

func collectionSourceHealthHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		SourceID string `json:"sourceId"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &args)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"health": collStore.healthRows(args.SourceID)}}
}

func collectionVantageCompareHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		SourceID string `json:"sourceId"`
		Dataset  string `json:"dataset"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(args.SourceID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "sourceId required"}
	}
	return OpsResult{OK: true, Initial: collStore.vantageCompare(args.SourceID, args.Dataset)}
}

func blockListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		SourceID string `json:"sourceId"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &args)
	}
	rows := collStore.healthRows(args.SourceID)
	blocked := make([]map[string]interface{}, 0)
	for _, h := range rows {
		state, _ := h["state"].(string)
		if strings.HasPrefix(state, "blocked_") || state == "rate_limited" {
			blocked = append(blocked, h)
		}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"blocked": blocked, "count": len(blocked)}}
}
