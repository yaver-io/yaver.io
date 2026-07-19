package main

// mcp_billing.go — buyer-side billing MCP tools. Let a Yaver user, from their
// terminal coding agent, check their plan and get payment / manage links for
// Yaver's own Relay Pro ($9) / Cloud Workspace ($29) plans. Thin wrappers over
// the authed Convex /billing/* endpoints (the daemon already holds the user's
// token).
//
// NOT the lemonsqueezy_* tools, which are SELLER-side (the user's OWN store).
// These buy *Yaver itself*.
//
// Mis-bill safety: the server-side checkout maps product→variant and REQUIRES
// a distinct LemonSqueezy variant for each paid product — if a variant isn't
// configured it returns a clean 503, which these tools surface verbatim (never
// a wrong-priced link). See docs/yaver-mcp-billing.md.

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

// billingRequest makes an authed call to a /billing/* endpoint and returns the
// status code + raw body. A nil bodyJSON sends no body (GET).
func billingRequest(method, path string, bodyJSON []byte) (int, []byte, error) {
	base, token := billingBaseURL()
	if token == "" {
		return 0, nil, fmt.Errorf("not signed in")
	}
	var r io.Reader
	if bodyJSON != nil {
		r = bytes.NewReader(bodyJSON)
	}
	req, err := newBearerRequest(method, base+path, token, r)
	if err != nil {
		return 0, nil, err
	}
	if bodyJSON != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

// --- yaver_billing_status ---------------------------------------------------

// mcpYaverBillingStatus reports whether the user is subscribed, which plan,
// included active-hours left this month, and whether managed inference is on.
// Internal wallet/meter fields are intentionally not exposed in this buyer UX.
func mcpYaverBillingStatus() interface{} {
	if _, token := billingBaseURL(); token == "" {
		return billingNotSignedIn()
	}
	code, body, err := billingRequest(http.MethodGet, "/billing/status", nil)
	if err != nil {
		return mcpToolError(fmt.Sprintf("billing status: %v", err))
	}
	if code != http.StatusOK {
		return mcpToolError(fmt.Sprintf("billing status failed (%d): %s", code, strings.TrimSpace(string(body))))
	}
	var s struct {
		Subscribed         bool     `json:"subscribed"`
		Tier               *string  `json:"tier"`
		SubscriptionStatus *string  `json:"subscriptionStatus"`
		CurrentPeriodEnd   *float64 `json:"currentPeriodEnd"`
		IncludedHoursLeft  float64  `json:"includedHoursLeft"`
		ManagedInference   bool     `json:"managedInference"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return mcpToolError(fmt.Sprintf("billing status decode: %v", err))
	}
	out := map[string]interface{}{
		"signed_in":           true,
		"subscribed":          s.Subscribed,
		"tier":                s.Tier,
		"subscription_status": s.SubscriptionStatus,
		"included_hours_left": s.IncludedHoursLeft,
		"managed_inference":   s.ManagedInference,
	}
	if s.Subscribed {
		tier := "your"
		if s.Tier != nil && *s.Tier != "" {
			tier = *s.Tier
		}
		out["next_action"] = fmt.Sprintf("You're on the %s plan. Use yaver_billing_manage to update payment, change plan, or cancel.", tier)
	} else {
		out["next_action"] = "No active Yaver plan. Use yaver_billing_checkout to subscribe — Relay Pro ($9/mo) or Cloud Workspace ($29/mo, BYO Claude/Codex/OpenCode)."
	}
	return mcpToolJSON(out)
}

// --- yaver_billing_checkout -------------------------------------------------

// mcpYaverBillingCheckout returns a LemonSqueezy payment link for the chosen
// product. Both paid products go through the server checkout, which sets the
// correct product + variant.
// billingPlanMap maps a user-facing plan name to its server productId, canonical
// short name, label, and price. ok=false for anything unrecognized. Pure (no
// I/O) so it's unit-testable.
func billingPlanMap(plan string) (planID, short, label, price string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "", "relay", "relay-pro", "managed-relay":
		return "relay-pro", "relay", "Relay Pro", "$9/mo", true
	case "workspace", "cloud-workspace", "yaver-cloud", "compute":
		return "cloud-workspace", "workspace", "Cloud Workspace", "$29/mo (BYO Claude/Codex/OpenCode)", true
	default:
		return "", "", "", "", false
	}
}

func mcpYaverBillingCheckout(plan string) interface{} {
	planID, short, label, price, ok := billingPlanMap(plan)
	if !ok {
		return mcpToolError(`plan must be "relay" ($9/mo Relay Pro) or "workspace" ($29/mo Cloud Workspace)`)
	}

	if _, token := billingBaseURL(); token == "" {
		return billingNotSignedIn()
	}
	reqBody, _ := json.Marshal(map[string]string{"region": "eu", "productId": planID})
	code, body, err := billingRequest(http.MethodPost, "/billing/checkout", reqBody)
	if err != nil {
		return mcpToolError(fmt.Sprintf("checkout: %v", err))
	}
	switch code {
	case http.StatusOK:
		var result struct {
			URL  string `json:"url"`
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(body, &result); err != nil || strings.TrimSpace(result.URL) == "" {
			return mcpToolError("checkout URL missing in response")
		}
		return mcpToolJSON(map[string]interface{}{
			"plan":        short,
			"price":       price,
			"url":         result.URL,
			"mode":        firstNonEmpty(result.Mode, "sandbox"),
			"next_action": fmt.Sprintf("Open this link to subscribe to %s (%s): %s  — pay with the SAME email you signed into Yaver with, or the subscription won't attach to your account.", label, price, result.URL),
		})
	case http.StatusForbidden:
		return mcpToolJSON(map[string]interface{}{
			"available":   false,
			"next_action": "Yaver Cloud plans aren't enabled on your account yet.",
		})
	case http.StatusServiceUnavailable:
		// Surface the server's message verbatim so the agent tells the human
		// accurately when a product variant is not configured.
		return mcpToolJSON(map[string]interface{}{
			"plan":        short,
			"available":   false,
			"manage_url":  yaverDashboardURL,
			"next_action": strings.TrimSpace(string(body)),
		})
	default:
		return mcpToolError(fmt.Sprintf("checkout failed (%d): %s", code, strings.TrimSpace(string(body))))
	}
}

// --- yaver_billing_manage ---------------------------------------------------

// mcpYaverBillingManage returns where to update payment, change plan, or cancel.
// Prefers the live LemonSqueezy customer-portal URL (/billing/portal); falls
// back to the Yaver dashboard billing tab.
func mcpYaverBillingManage() interface{} {
	if _, token := billingBaseURL(); token == "" {
		return billingNotSignedIn()
	}
	code, body, err := billingRequest(http.MethodGet, "/billing/portal", nil)
	if err == nil && code == http.StatusOK {
		var p struct {
			PortalURL        *string `json:"portalUrl"`
			UpdatePaymentURL *string `json:"updatePaymentUrl"`
		}
		if json.Unmarshal(body, &p) == nil && p.PortalURL != nil && *p.PortalURL != "" {
			return mcpToolJSON(map[string]interface{}{
				"portal_url":         *p.PortalURL,
				"update_payment_url": p.UpdatePaymentURL,
				"next_action":        "Manage your subscription (update payment, change plan, or cancel) here: " + *p.PortalURL,
			})
		}
	}
	// No active LS subscription (or LS not configured) — point at the dashboard.
	return mcpToolJSON(map[string]interface{}{
		"manage_url":  yaverDashboardURL,
		"next_action": "Manage billing at " + yaverDashboardURL + " (Billing). If you have an active subscription, your LemonSqueezy receipt emails also link to the customer portal to cancel.",
	})
}
