package main

// package_runtime.go — execute ONE run of a Task Package on this runtime.
//
// The headline capability is MCP-over-MCP: a package's task can call another MCP
// server (e.g. the owner's already-built yaver-bet MCP, remote on Hetzner) OR a
// local Yaver ops verb, and capture the result. This is what makes a package
// "use my yaver-bet MCP to do something" rather than just static code.
//
// What the Go core run does today: declarative `fetch` sources (HTTP+JSON
// extraction) and `mcp` bindings (http remote / local verb). webview / playwright
// / redroid engines are device/host targets and run their collector there; the
// Go core notes them but does not drive a browser/emulator from here.
//
// Safety: ACTING-tier packages (operate/agent, or guard.tier=acting) refuse to
// run unless the caller passes confirm=true — a friend's device never silently
// takes irreversible actions.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var pkgHTTPClient = &http.Client{Timeout: 15 * time.Second}

// pkgDetectEgress is a seam so tests can supply a fixed vantage without a
// network probe (mirrors resolveEgressGeo in egress.go).
var pkgDetectEgress = detectEgressIdentity

// PackageRunResult is the inline result of a single run.
type PackageRunResult struct {
	Package        string                   `json:"package"`
	Status         string                   `json:"status"` // ok | blocked_geo | error | needs_confirmation
	Fields         map[string]interface{}   `json:"fields"`
	SourcesOk      int                      `json:"sourcesOk"`
	SourcesBlocked int                      `json:"sourcesBlocked"`
	MCPCalls       []map[string]interface{} `json:"mcpCalls,omitempty"`
	Notes          []string                 `json:"notes,omitempty"`
	ObservationID  string                   `json:"observationId,omitempty"`
	Country        string                   `json:"country,omitempty"`
}

// runPackageOnce executes the package's task body once and returns the result.
// confirm gates the ACTING tier. The OpsContext lets MCP bindings reach local
// verbs via the same dispatcher.
func runPackageOnce(c OpsContext, p *TaskPackage, confirm bool) PackageRunResult {
	res := PackageRunResult{Package: p.Metadata.Name, Fields: map[string]interface{}{}}

	if p.effectiveTier() == "acting" && !confirm {
		res.Status = "needs_confirmation"
		res.Notes = append(res.Notes,
			"ACTING-tier package (operate/agent/write) — pass confirm=true to run; it can take actions on this device")
		return res
	}

	// Detect this runtime's vantage (egress identity) for provenance.
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	egress := pkgDetectEgress(ctx, mustLoadConfigBestEffort(), false)
	res.Country = egress.Country

	// 1. Declarative fetch sources (HTTP + JSON extraction).
	for _, src := range p.Spec.Task.Sources {
		render := src.Render
		if render == "" {
			render = "auto"
		}
		if render == "webview" {
			res.Notes = append(res.Notes,
				fmt.Sprintf("source %q render=webview is a mobile-target concern; Go core skipped it", src.ID))
			continue
		}
		fields, status, note := fetchSource(ctx, src)
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
		switch status {
		case "ok":
			res.SourcesOk++
			for k, v := range fields {
				res.Fields[k] = v
			}
		case "blocked_geo", "blocked_login", "blocked_challenge":
			res.SourcesBlocked++
			if res.Status == "" || res.Status == "ok" {
				res.Status = status
			}
		default:
			if res.Status == "" {
				res.Status = "error"
			}
		}
	}

	// 2. MCP-over-MCP bindings: use another MCP server / local verb to do work.
	for _, b := range p.Spec.Task.MCP {
		out, err := runPackageMCP(c, b)
		call := map[string]interface{}{"name": b.Name, "transport": b.transport()}
		if err != nil {
			call["error"] = err.Error()
			res.Notes = append(res.Notes, fmt.Sprintf("mcp %q: %v", b.Name, err))
			if res.Status == "" {
				res.Status = "error"
			}
		} else {
			call["ok"] = true
			key := b.As
			if key == "" {
				key = b.Name
			}
			res.Fields[key] = out
		}
		res.MCPCalls = append(res.MCPCalls, call)
	}

	if res.Status == "" {
		res.Status = "ok"
	}

	// 3. Persist to the local collection store (vantage-tagged), reusing the
	//    multi-vantage data model. Source = the package; vantage = this runtime.
	if len(res.Fields) > 0 && res.Status == "ok" {
		src := collStore.upsertSource(CollectionSource{
			Name:        p.Metadata.Name,
			Kind:        "package",
			AccessState: "public_allowed",
		})
		van := collStore.upsertVantage(CollectionVantage{
			RuntimeID:     egressRuntimeID(),
			EgressPolicy:  "machine_native",
			EgressIP:      egress.IP,
			EgressGeo:     egress.Region,
			EgressCountry: egress.Country,
			EgressASN:     egress.ASN,
		})
		dataset := p.Spec.Output.Dataset
		if dataset == "" {
			dataset = p.Spec.Task.Dataset
		}
		if dataset == "" {
			dataset = p.Metadata.Name
		}
		run := collStore.recordRun(CollectionRun{
			SourceID: src.SourceID, VantageID: van.VantageID,
			CollectorType: "package", Status: "ok", RowsExtracted: 1,
			EgressIPUsed: egress.IP, EgressGeoUsed: egress.Region,
		})
		if obs, err := collStore.addObservation(CollectionObservation{
			SourceID: src.SourceID, VantageID: van.VantageID, RunID: run.RunID,
			Dataset: dataset, Fields: res.Fields,
		}); err == nil {
			res.ObservationID = obs.ObservationID
		} else {
			res.Notes = append(res.Notes, "observation rejected: "+err.Error())
		}
	}

	// 4. Local audit row.
	pkgStore.recordRun(PackageRun{
		PackageName: p.Metadata.Name, Status: res.Status,
		RowsExtracted: len(res.Fields), SourcesOk: res.SourcesOk,
		SourcesBlocked: res.SourcesBlocked, Country: res.Country,
		Summary: map[string]interface{}{"mcpCalls": len(res.MCPCalls)},
	})
	return res
}

// runPackageMCP performs one MCP-over-MCP call: remote http MCP server, or a
// local Yaver ops verb (so a package can compose existing Yaver capability).
func runPackageMCP(c OpsContext, b PackageMCPBinding) (interface{}, error) {
	switch b.transport() {
	case "http":
		srv := ExternalMCPServer{Name: b.Name, URL: b.URL, AuthToken: b.AuthToken, Enabled: true}
		raw, err := callExternalMCP(srv, "tools/call", map[string]interface{}{
			"name": b.Tool, "arguments": b.Arguments,
		})
		if err != nil {
			return nil, err
		}
		var out interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("bad mcp response: %w", err)
		}
		return out, nil
	case "local":
		payload, _ := json.Marshal(b.Arguments)
		out := dispatchOps(c, OpsRequest{Machine: "local", Verb: b.Verb, Payload: payload})
		if !out.OK {
			return nil, fmt.Errorf("local verb %q: %s", b.Verb, out.Error)
		}
		return out.Initial, nil
	default:
		return nil, fmt.Errorf("binding %q needs a url (http) or verb (local)", b.Name)
	}
}

// fetchSource does an HTTP GET and extracts jsonPath fields. Returns
// (fields, status, note). It detects obvious challenge/login/geo blocks and
// stops (never bypasses).
func fetchSource(ctx context.Context, src PackageSource) (map[string]interface{}, string, string) {
	if strings.TrimSpace(src.URL) == "" {
		return nil, "error", fmt.Sprintf("source %q has no url", src.ID)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, "error", fmt.Sprintf("source %q: %v", src.ID, err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := pkgHTTPClient.Do(req)
	if err != nil {
		return nil, "error", fmt.Sprintf("source %q: %v", src.ID, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	switch resp.StatusCode {
	case http.StatusForbidden:
		return nil, "blocked_ip", fmt.Sprintf("source %q: 403 (likely datacenter/IP block — try a residential vantage)", src.ID)
	case 451:
		return nil, "blocked_geo", fmt.Sprintf("source %q: 451 (geo-blocked)", src.ID)
	}
	low := strings.ToLower(string(body))
	if strings.Contains(low, "captcha") || strings.Contains(low, "are you a robot") ||
		strings.Contains(low, "cf-challenge") || strings.Contains(low, "just a moment") {
		return nil, "blocked_challenge", fmt.Sprintf("source %q: challenge detected — stopped (not bypassed)", src.ID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "error", fmt.Sprintf("source %q: HTTP %d", src.ID, resp.StatusCode)
	}

	fields := map[string]interface{}{}
	if len(src.Extract) == 0 {
		// no extraction declared: record a minimal liveness field
		fields[src.ID+"_len"] = len(body)
		return fields, "ok", ""
	}
	var doc interface{}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, "parse_error", fmt.Sprintf("source %q: response is not JSON; selector/webview extraction is a mobile-target job", src.ID)
	}
	for name, ex := range src.Extract {
		val := resolveJSONPath(doc, ex.JSONPath)
		if val == nil {
			continue
		}
		fields[name] = coerce(val, ex.As)
	}
	if len(fields) == 0 {
		return nil, "no_data", fmt.Sprintf("source %q: no fields matched", src.ID)
	}
	return fields, "ok", ""
}

// resolveJSONPath walks a simple dot/bracket path like "data.items[0].price".
func resolveJSONPath(doc interface{}, path string) interface{} {
	path = strings.TrimSpace(path)
	if path == "" {
		return doc
	}
	cur := doc
	for _, raw := range strings.Split(path, ".") {
		key := raw
		var idx = -1
		if i := strings.Index(raw, "["); i >= 0 && strings.HasSuffix(raw, "]") {
			key = raw[:i]
			if n, err := strconv.Atoi(raw[i+1 : len(raw)-1]); err == nil {
				idx = n
			}
		}
		if key != "" {
			m, ok := cur.(map[string]interface{})
			if !ok {
				return nil
			}
			cur, ok = m[key]
			if !ok {
				return nil
			}
		}
		if idx >= 0 {
			arr, ok := cur.([]interface{})
			if !ok || idx >= len(arr) {
				return nil
			}
			cur = arr[idx]
		}
	}
	return cur
}

func coerce(v interface{}, as string) interface{} {
	if as != "number" {
		return v
	}
	switch t := v.(type) {
	case float64:
		return t
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
	}
	return v
}

// egressRuntimeID returns a stable-ish id for this runtime as a vantage runtime.
func egressRuntimeID() string {
	cfg, _ := LoadConfig()
	if cfg != nil && cfg.DeviceID != "" {
		return cfg.DeviceID
	}
	return "local"
}
