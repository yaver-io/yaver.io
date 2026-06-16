package main

// gateway_invoke.go — the capability dispatcher.
//
// gatewayInvoke is the read loop from docs §1.5, minus the NL intent router
// (this slice calls it directly with an explicit connector+capability):
//
//	manifest := registry.Get(connector)
//	session  := broker.Ensure(ctx, manifest)        // refresh if needed
//	raw      := engine.run(manifest, capability, …) // engine "api": authed GET
//	answer   := project(raw, capability.answerSchema)
//	return answer
//
// SLICE SCOPE: engine "api" only, GET only, READ-only. No write/ACT path.
//
// Answer projection here is DETERMINISTIC dotted-path mapping (response JSON →
// answerSchema keys), NOT LLM extraction. The hook where AI extraction plugs in
// later is clearly marked (see projectAnswer). This keeps the slice cheap,
// offline-testable, and free of model dependencies while preserving the exact
// answerSchema contract the AI extractor will satisfy later.
//
// Policy Guard (CLAUDE.md / docs §9): honest User-Agent; on a 401 the broker
// refreshes once (via Ensure) then we retry exactly once, then fail clean; on a
// 403/429/451 we return a structured {blocked:true} and STOP — no retry-spam, no
// identity rotation. No captcha/bot-detection logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// gatewayResult is the structured reply from gatewayInvoke.
type gatewayResult struct {
	Connector  string                 `json:"connector"`
	Capability string                 `json:"capability"`
	Verb       string                 `json:"verb"`
	Answer     map[string]interface{} `json:"answer,omitempty"`
	Blocked    bool                   `json:"blocked,omitempty"`
	StatusCode int                    `json:"status_code,omitempty"`
	Detail     string                 `json:"detail,omitempty"`
	Source     string                 `json:"source,omitempty"`
	// Signature is the final-screen ScreenSignature for the redroid engine
	// (docs §3) — empty for the api engine.
	Signature string `json:"signature,omitempty"`
	// NeedsHeal records flow steps whose observed signature drifted from the
	// expected one (redroid engine). Non-fatal here; the M-G6 curator consumes it
	// to rewrite a stale flow.
	NeedsHeal []string `json:"needs_heal,omitempty"`
}

// gatewayDeps bundles the collaborators gatewayInvoke needs. In production these
// are the vault-backed CredStore + the on-disk registry; tests inject in-memory
// versions + httptest servers.
type gatewayDeps struct {
	registry   *ConnectorRegistry
	broker     *broker
	httpClient *http.Client
}

// newGatewayDeps wires the production gateway: on-disk registry + vault-backed
// broker. Returns an error if the agent is not authenticated (vault closed).
func newGatewayDeps() (*gatewayDeps, error) {
	reg, err := NewConnectorRegistry()
	if err != nil {
		return nil, err
	}
	store, err := newVaultCredStore()
	if err != nil {
		return nil, err
	}
	return &gatewayDeps{
		registry:   reg,
		broker:     newBroker(store),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// gatewayInvoke runs a single read capability and returns its projected answer.
func (d *gatewayDeps) gatewayInvoke(ctx context.Context, connectorID, capabilityID string, params map[string]string) (*gatewayResult, error) {
	conn, err := d.registry.Get(connectorID)
	if err != nil {
		return nil, err
	}
	cap, ok := conn.Capability(capabilityID)
	if !ok {
		return nil, fmt.Errorf("gateway: connector %q has no capability %q", connectorID, capabilityID)
	}
	// Read-only guard: never execute a write in this slice, even if a manifest
	// somehow declared one (validation should have rejected it already).
	if !isReadVerb(cap.Verb) {
		return nil, fmt.Errorf("gateway: capability %q is not a read — write/ACT is out of scope for this slice", capabilityID)
	}

	// Acquire/refresh the session (may refresh an expired access token, or — for
	// a redroid connector — restore/relogin the trusted device).
	session, err := d.broker.Ensure(ctx, conn)
	if err != nil {
		return nil, err
	}

	// Engine dispatch. "api" runs an authed HTTP GET (below); "redroid" drives
	// an app on the device the broker just authenticated (gateway_redroid_invoke.go).
	switch conn.Engine {
	case "redroid":
		driver, ok := d.broker.deviceDriverFor(conn)
		if !ok || driver == nil {
			return nil, fmt.Errorf("gateway: connector %q uses the redroid engine but no device driver is available", connectorID)
		}
		return redroidInvoke(ctx, conn, cap, params, session, driver, d.broker.NeedsHuman(conn))
	case "api":
		// handled below
	default:
		return nil, fmt.Errorf("gateway: unsupported engine %q for connector %q", conn.Engine, connectorID)
	}

	if cap.Flow.Type != "api" {
		return nil, fmt.Errorf("gateway: connector %q engine \"api\" requires flow type \"api\" (got %q)", connectorID, cap.Flow.Type)
	}

	res := &gatewayResult{
		Connector:  connectorID,
		Capability: capabilityID,
		Verb:       cap.Verb,
		Source:     conn.Surface,
	}

	// Execute the authed GET. On a 401 we refresh once and retry once.
	raw, status, err := d.apiGet(ctx, conn, cap, params, session)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		// Token may have been revoked server-side between Ensure and the call,
		// or Ensure handed us a still-valid-by-clock-but-rejected token. Force
		// a refresh and retry exactly once. No further retries (Policy Guard).
		session2, rErr := d.broker.Refresh(ctx, conn)
		if rErr != nil {
			return nil, fmt.Errorf("gateway: %q returned 401 and re-auth failed: %w", connectorID, rErr)
		}
		raw, status, err = d.apiGet(ctx, conn, cap, params, session2)
		if err != nil {
			return nil, err
		}
	}

	// A block is a "no" — surface it structured, STOP, never retry/rotate.
	if status == http.StatusForbidden || status == http.StatusTooManyRequests || status == 451 {
		res.Blocked = true
		res.StatusCode = status
		res.Detail = fmt.Sprintf("connector %q returned a block (status %d). Backing off — not retrying or rotating identity. A block is a \"no\".", connectorID, status)
		return res, nil
	}
	if status == http.StatusUnauthorized {
		return nil, fmt.Errorf("gateway: connector %q still unauthorized after refresh (401) — re-consent required", connectorID)
	}
	if status < 200 || status >= 300 {
		res.StatusCode = status
		res.Detail = fmt.Sprintf("connector %q returned status %d: %s", connectorID, status, gatewayTruncate(string(raw), 512))
		return nil, fmt.Errorf("gateway: %s", res.Detail)
	}

	answer, err := projectAnswer(raw, cap.AnswerSchema)
	if err != nil {
		return nil, fmt.Errorf("gateway: project answer for %q/%q: %w", connectorID, capabilityID, err)
	}
	res.Answer = answer
	res.StatusCode = status
	return res, nil
}

// apiGet performs the capability's authed HTTP GET. The path may contain
// {param} placeholders substituted from params (a small set of built-ins like
// {now} are also supported). Returns (body, statusCode, transportErr).
func (d *gatewayDeps) apiGet(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, session Session) ([]byte, int, error) {
	path := substituteParams(cap.Flow.Path, params)
	endpoint := strings.TrimRight(conn.Surface, "/") + "/" + strings.TrimLeft(path, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", gatewayContactUA) // honest identity, never a browser spoof
	if session.Kind == SessionBearer && session.Token != "" {
		req.Header.Set("Authorization", "Bearer "+session.Token)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return body, resp.StatusCode, nil
}

// substituteParams replaces {key} placeholders in a path. {now} resolves to the
// current RFC3339 timestamp (a common API filter need); other keys come from
// params. Unknown placeholders are left intact so a misconfigured path surfaces
// rather than silently dropping a filter.
func substituteParams(path string, params map[string]string) string {
	out := strings.ReplaceAll(path, "{now}", time.Now().UTC().Format(time.RFC3339))
	for k, v := range params {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// projectAnswer maps a raw JSON response into the capability's answerSchema
// using DETERMINISTIC dotted-path projection.
//
// answerSchema is { outKey: spec } where spec is one of:
//
//	"string" / "datetime" / "number" / "bool" / "string?"  — type only; the
//	    source path defaults to outKey (looked up at the JSON root, descending
//	    through the first array element when needed)
//	"dotted.path:type"                                       — explicit source
//	    path (e.g. "items.0.title:string", "data.balance:number")
//
// A trailing "?" on the type marks the field optional (absent ⇒ omitted, no
// error). Non-optional fields that are missing produce an error so a broken
// extraction is loud, not silent.
//
// AI-EXTRACTION HOOK: when a response is messy/unstructured (HTML, free-form
// JSON the path can't address), a later slice routes raw → a models_* extractor
// that fills the same answerSchema. That extractor would be invoked HERE, behind
// the same answerSchema contract, as a fallback when deterministic projection
// can't satisfy a required field. This slice intentionally does NOT call a
// model — projection is pure + offline-testable.
func projectAnswer(raw []byte, schema map[string]string) (map[string]interface{}, error) {
	if len(schema) == 0 {
		// No schema: return the parsed body as-is (best effort) so the caller
		// still gets structured data.
		var any interface{}
		if err := json.Unmarshal(raw, &any); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return map[string]interface{}{"raw": any}, nil
	}

	var doc interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	out := map[string]interface{}{}
	for outKey, spec := range schema {
		path, optional := parseSchemaSpec(outKey, spec)
		val, found := lookupPath(doc, path)
		if !found {
			if optional {
				continue
			}
			return nil, fmt.Errorf("required field %q (path %q) not found in response", outKey, strings.Join(path, "."))
		}
		out[outKey] = val
	}
	return out, nil
}

// parseSchemaSpec splits an answerSchema value into (sourcePath, optional). The
// type itself is informational in this slice (we project the raw JSON value);
// the "?" optional marker is honored. Source path defaults to the outKey.
func parseSchemaSpec(outKey, spec string) (path []string, optional bool) {
	src := outKey
	typ := spec
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		// "dotted.path:type"
		src = spec[:i]
		typ = spec[i+1:]
	}
	optional = strings.HasSuffix(strings.TrimSpace(typ), "?")
	return splitPath(src), optional
}

// splitPath splits a dotted path into segments, trimming a trailing "?" that a
// caller may have left on the path side.
func splitPath(src string) []string {
	src = strings.TrimSuffix(strings.TrimSpace(src), "?")
	if src == "" {
		return nil
	}
	return strings.Split(src, ".")
}

// lookupPath walks a decoded JSON value along a dotted path. Numeric segments
// index into arrays; named segments index into objects. To make terse manifests
// work, when the current value is an array and the segment is NOT numeric, the
// walk descends into the first element (the common "list with one result"
// shape, e.g. calendar events with maxResults=1). Returns (value, found).
func lookupPath(doc interface{}, path []string) (interface{}, bool) {
	cur := doc
	for _, seg := range path {
		switch node := cur.(type) {
		case map[string]interface{}:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []interface{}:
			if idx, ok := arrayIndex(seg); ok {
				if idx < 0 || idx >= len(node) {
					return nil, false
				}
				cur = node[idx]
			} else {
				// Non-numeric segment against an array: descend into the first
				// element and re-apply this segment there.
				if len(node) == 0 {
					return nil, false
				}
				v, ok := lookupPath(node[0], []string{seg})
				if !ok {
					return nil, false
				}
				cur = v
			}
		default:
			return nil, false
		}
	}
	return cur, true
}

// arrayIndex parses a path segment as a non-negative array index.
func arrayIndex(seg string) (int, bool) {
	if seg == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func gatewayTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
