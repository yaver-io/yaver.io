package main

// mcp_billing.go — buyer-side billing MCP tools. Let a Yaver user, from their
// terminal coding agent, check their plan and get a payment link for Yaver's
// own Workspace ($9) / Agent ($19) plans. Thin wrappers over the authed Convex
// /billing/* endpoints (the daemon already holds the user's token).
//
// NOT to be confused with the lemonsqueezy_* tools, which are SELLER-side
// (managing the user's OWN store). These buy *Yaver itself*.
//
// Status note: the $19 Agent (hosted) initial-checkout tier wiring + a
// distinct LS variant are still being finalized server-side (see
// docs/yaver-mcp-billing.md). Until then yaver_billing_checkout offers the $9
// Workspace (byok) plan — which bills correctly today — and points Agent buyers
// at the dashboard rather than minting a mis-billed checkout.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const yaverDashboardURL = "https://yaver.io/dashboard"

// billingBaseURL returns the Convex HTTP-actions origin (.convex.site) the
// /billing/* endpoints live on, plus the user's auth token.
func billingBaseURL() (base, token string) {
	cfg, _ := LoadConfig()
	if cfg != nil {
		base = strings.TrimRight(strings.TrimSpace(cfg.ConvexSiteURL), "/")
		token = strings.TrimSpace(cfg.AuthToken)
	}
	if base == "" {
		base = defaultConvexSiteURL
	}
	return base, token
}

func billingNotSignedIn() interface{} {
	return mcpToolJSON(map[string]interface{}{
		"signed_in":   false,
		"next_action": "You're not signed in to Yaver yet. Run yaver_lazy_setup (or `yaver auth`) first, then check billing.",
	})
}

// --- yaver_billing_status ---------------------------------------------------

// mcpYaverBillingStatus reports the user's current plan fuel gauges: included
// active-hours left this month, prepaid wallet balance, and whether managed
// inference is on. Backed by the committed /billing/yaver-cloud/balance
// endpoint. This is the "am I already subscribed / what do I have" read.
func mcpYaverBillingStatus() interface{} {
	base, token := billingBaseURL()
	if token == "" {
		return billingNotSignedIn()
	}
	req, err := newBearerRequest(http.MethodGet, base+"/billing/yaver-cloud/balance", token, nil)
	if err != nil {
		return mcpToolError(fmt.Sprintf("billing status: %v", err))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return mcpToolError(fmt.Sprintf("billing status: %v", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		return mcpToolJSON(map[string]interface{}{
			"signed_in":   true,
			"available":   false,
			"next_action": "Yaver Cloud plans aren't enabled on your account yet (private preview).",
		})
	}
	if resp.StatusCode != http.StatusOK {
		return mcpToolError(fmt.Sprintf("billing status failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return mcpToolError(fmt.Sprintf("billing status decode: %v", err))
	}

	out := map[string]interface{}{"signed_in": true, "available": true}
	// Wallet (cents → dollars for the human-facing summary).
	if c, ok := raw["balanceCents"].(float64); ok {
		out["wallet_cents"] = c
		out["wallet_usd"] = c / 100.0
	}
	if lb, ok := raw["lowBalance"].(bool); ok {
		out["low_balance"] = lb
	}
	// Included active-hours this month + tier (allowance.plan ≈ byok/hosted/beta).
	if al, ok := raw["allowance"].(map[string]interface{}); ok {
		if plan, ok := al["plan"].(string); ok && plan != "" {
			out["tier"] = plan
		}
		if rem, ok := al["remainingSeconds"].(float64); ok {
			out["included_hours_left"] = rem / 3600.0
		}
	}
	subscribed := out["tier"] != nil && out["tier"] != ""
	out["subscribed"] = subscribed
	if subscribed {
		out["next_action"] = fmt.Sprintf("You're on the %v plan. Use yaver_billing_manage to change or cancel, or yaver_billing_status anytime.", out["tier"])
	} else {
		out["next_action"] = "No active Yaver plan. Use yaver_billing_checkout to subscribe (Workspace $9 / Agent $19)."
	}
	return mcpToolJSON(out)
}

// --- yaver_billing_checkout -------------------------------------------------

// mcpYaverBillingCheckout returns a payment link for the chosen plan. Workspace
// ($9 BYO) mints a real LemonSqueezy checkout via the committed endpoint. Agent
// ($19 included-model) is gated until the server-side hosted variant + tier
// wiring lands (docs/yaver-mcp-billing.md) so we never mint a mis-billed link.
func mcpYaverBillingCheckout(plan string) interface{} {
	plan = strings.ToLower(strings.TrimSpace(plan))
	if plan == "" {
		plan = "workspace"
	}
	if plan != "workspace" && plan != "agent" {
		return mcpToolError(`plan must be "workspace" ($9 BYO) or "agent" ($19 included model)`)
	}

	if plan == "agent" {
		// Don't mint a checkout that would silently grant byok entitlements —
		// surface the dashboard until the hosted variant is wired.
		return mcpToolJSON(map[string]interface{}{
			"plan":        "agent",
			"available":   false,
			"manage_url":  yaverDashboardURL,
			"next_action": "The Cloud Agent ($19, included model) plan is being finalized. For now, subscribe to Cloud Workspace ($9, bring your own Claude/Codex) with yaver_billing_checkout plan=\"workspace\", or open " + yaverDashboardURL + " to manage plans.",
		})
	}

	base, token := billingBaseURL()
	if token == "" {
		return billingNotSignedIn()
	}
	reqBody, _ := json.Marshal(map[string]string{"region": "eu", "planId": "cloud-workspace"})
	req, err := newBearerRequest(http.MethodPost, base+"/billing/yaver-cloud/checkout", token, bytes.NewReader(reqBody))
	if err != nil {
		return mcpToolError(fmt.Sprintf("checkout: %v", err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return mcpToolError(fmt.Sprintf("checkout: %v", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		return mcpToolJSON(map[string]interface{}{
			"available":   false,
			"next_action": "Yaver Cloud plans aren't enabled on your account yet (private preview).",
		})
	}
	if resp.StatusCode != http.StatusOK {
		return mcpToolError(fmt.Sprintf("checkout failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	var result struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &result); err != nil || strings.TrimSpace(result.URL) == "" {
		return mcpToolError("checkout URL missing in response")
	}
	return mcpToolJSON(map[string]interface{}{
		"plan":        "workspace",
		"price":       "$9/mo",
		"url":         result.URL,
		"mode":        firstNonEmpty(result.Mode, "sandbox"),
		"next_action": "Open this link to subscribe to Cloud Workspace ($9): " + result.URL + "  — pay with the same email you signed into Yaver with, or the subscription won't attach to your account.",
	})
}

// --- yaver_billing_manage ---------------------------------------------------

// mcpYaverBillingManage returns where to update payment, change plan, or cancel.
// LemonSqueezy's customer portal is the system of record (every receipt email
// links to it); the Yaver dashboard billing tab is the in-product entry.
func mcpYaverBillingManage() interface{} {
	return mcpToolJSON(map[string]interface{}{
		"manage_url":  yaverDashboardURL,
		"next_action": "Manage your subscription (update payment, change plan, or cancel) at " + yaverDashboardURL + " (Billing). Your LemonSqueezy receipt emails also link straight to the customer portal for cancellation.",
	})
}
