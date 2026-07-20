package main

// ops_relay_pro.go — customer-facing MCP surface for "Relay Pro", the managed
// per-user relay. The backend engine already exists (backend/convex:
// managedRelays + provisionRelay + the LemonSqueezy webhook that snapshots a
// CAX11 box behind <shortId>.relay.yaver.io). What was missing was the
// acquisition + inspection path a UI can actually call:
//
//   - relay_pro_status   → GET  /subscription, surface the `relay` block
//                          (status/domain/region/ports) + whether THIS agent is
//                          already wired to a managed relay.
//   - relay_pro_checkout → POST /billing/checkout {productId:"relay-pro"},
//                          two-phase cost guard (accept_cost), returns the pay
//                          URL; the box auto-provisions on the webhook.
//
// Deprovision is deliberately NOT a verb here: teardown is driven by the
// subscription-cancelled webhook (snapshot-then-delete) so a stray MCP call can
// never strand billing state — cancel the subscription to release the box.
//
// ISOLATED FILE (prefer-new-files): own init(), reuses newBearerRequest + the
// same Convex routes the web `/billing/checkout` + `/subscription` use, so
// web / mobile / MCP all hit ONE backend contract. Core logic is factored into
// doRelayPro* so the closed-loop test drives them against a fake Convex.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func relayProConfig() (*Config, *OpsResult) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, &OpsResult{OK: false, Code: "not_authed", Error: "agent not authed (missing convex site url / token) — run `yaver auth`"}
	}
	return cfg, nil
}

// doRelayProStatus reads GET /subscription and projects the managed-relay view.
// Factored from the handler so it is testable against a fake Convex.
func doRelayProStatus(cfg *Config, client *http.Client) OpsResult {
	req, err := newBearerRequest("GET", cfg.ConvexSiteURL+"/subscription", cfg.AuthToken, nil)
	if err != nil {
		return OpsResult{OK: false, Code: "request_error", Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return OpsResult{OK: false, Code: "convex_unreachable", Error: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OpsResult{OK: false, Code: "status_failed", Error: fmt.Sprintf("subscription HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	var out struct {
		Relay *struct {
			Status   string `json:"status"`
			Domain   string `json:"domain"`
			Region   string `json:"region"`
			QuicPort int    `json:"quicPort"`
			HTTPPort int    `json:"httpPort"`
		} `json:"relay"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return OpsResult{OK: false, Code: "status_failed", Error: "subscription returned bad json"}
	}
	if out.Relay == nil {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"active": false,
			"hint":   "no managed relay — call relay_pro_checkout to buy Relay Pro (a dedicated per-user relay at <id>.relay.yaver.io)",
		}}
	}
	// Is this agent already pointed at the managed relay? Compare the box's
	// domain against the locally-configured relay servers.
	wired := false
	if out.Relay.Domain != "" {
		for _, rs := range cfg.RelayServers {
			if strings.Contains(rs.QuicAddr, out.Relay.Domain) || strings.Contains(rs.HttpURL, out.Relay.Domain) {
				wired = true
				break
			}
		}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"active":         out.Relay.Status == "active",
		"status":         out.Relay.Status,
		"domain":         out.Relay.Domain,
		"region":         out.Relay.Region,
		"quicPort":       out.Relay.QuicPort,
		"httpPort":       out.Relay.HTTPPort,
		"agentWiredToIt": wired,
		"hint":           relayProStatusHint(out.Relay.Status, wired),
	}}
}

func relayProStatusHint(status string, wired bool) string {
	switch status {
	case "active":
		if wired {
			return "Relay Pro is active and this agent is using it."
		}
		return "Relay Pro is active but this agent isn't pointed at it yet — POST /settings/repair-relay to pull creds."
	case "provisioning":
		return "Relay Pro box is still provisioning (~1-2 min) — check again shortly."
	case "error":
		return "Relay Pro provisioning errored — check the subscription state / contact support."
	default:
		return "Relay Pro is " + status + "."
	}
}

// doRelayProCheckout proxies POST /billing/checkout for the relay-pro product.
func doRelayProCheckout(cfg *Config, region string, client *http.Client) OpsResult {
	if region == "" {
		region = "eu"
	}
	body, _ := json.Marshal(map[string]string{"productId": "relay-pro", "region": region})
	req, err := newBearerRequest("POST", cfg.ConvexSiteURL+"/billing/checkout", cfg.AuthToken, bytes.NewReader(body))
	if err != nil {
		return OpsResult{OK: false, Code: "request_error", Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return OpsResult{OK: false, Code: "convex_unreachable", Error: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OpsResult{OK: false, Code: "checkout_failed", Error: fmt.Sprintf("checkout HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	var out struct {
		URL       string `json:"url"`
		ProductID string `json:"productId"`
		Mode      string `json:"mode"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.URL == "" {
		return OpsResult{OK: false, Code: "checkout_failed", Error: "checkout returned no url"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"checkoutUrl": out.URL,
		"productId":   out.ProductID,
		"mode":        out.Mode,
		"hint":        "open checkoutUrl in a browser to pay; the relay box auto-provisions on the LemonSqueezy webhook, then relay_pro_status shows it active",
	}}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "relay_pro_status",
		Description: "Show the user's managed Relay Pro: status (provisioning|active|error), its <id>.relay.yaver.io domain, region, ports, and whether THIS agent is already wired to it. Read-only.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsRelayProStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "relay_pro_checkout",
		Description: "Buy Relay Pro — a dedicated per-user managed relay (own box at <id>.relay.yaver.io, direct-first/relay-fallback). Two-phase cost guard: without accept_cost=true this returns a PLAN only (no checkout). With accept_cost=true it returns a pay URL; the relay box auto-provisions on the payment webhook. region=eu|us (default eu).",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"accept_cost"},
			"properties": map[string]interface{}{
				"region":      map[string]interface{}{"type": "string", "description": "eu|us (default eu)"},
				"accept_cost": map[string]interface{}{"type": "boolean", "description": "Must be true to actually create the checkout — this is a recurring paid subscription."},
			},
			"additionalProperties": false,
		},
		Handler:    opsRelayProCheckoutHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsRelayProStatusHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	cfg, fail := relayProConfig()
	if fail != nil {
		return *fail
	}
	return doRelayProStatus(cfg, &http.Client{Timeout: 20 * time.Second})
}

func opsRelayProCheckoutHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Region     string `json:"region"`
		AcceptCost bool   `json:"accept_cost"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if !p.AcceptCost {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"dryRun":  true,
			"plan":    "would create a Relay Pro checkout (recurring paid subscription for a dedicated managed relay)",
			"why":     "accept_cost=true not set",
			"product": "relay-pro",
		}}
	}
	cfg, fail := relayProConfig()
	if fail != nil {
		return *fail
	}
	return doRelayProCheckout(cfg, strings.TrimSpace(p.Region), &http.Client{Timeout: 20 * time.Second})
}
