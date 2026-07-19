package main

// Paid-plan MCP gate. Older launch builds hid Yaver's OWN paid-plan buyer
// tools so a fresh user's coding agent saw only the free + self-hosted surface.
// The current product model sells Relay Pro + Cloud Workspace from web-billed
// checkout links, so the gate is off by default.
//
// Scope is deliberately narrow: ONLY the tools that are purely "buy/manage a
// Yaver-billed plan." The cloud_*/relay/remote_* provisioning tools are shared
// BYO (bring-your-own-token, self-hosted) paths and are NOT gated here — the
// managed side of those is already fail-closed server-side (owner allowlist +
// LemonSqueezy env), so hiding them would only break the free BYO story.
const hidePaidMCPAtLaunch = false

// paidMCPToolsHiddenAtLaunch are the buyer-side "purchase/manage a Yaver plan"
// tools (mcp_billing.go). Managed cloud/relay themselves ride the shared
// cloud_*/relay verbs and stay reachable for BYO.
var paidMCPToolsHiddenAtLaunch = map[string]bool{
	"yaver_billing_status":   true,
	"yaver_billing_checkout": true,
	"yaver_billing_manage":   true,
}

// mcpToolIsPaidHiddenAtLaunch reports whether a tool is a launch-hidden paid tool.
func mcpToolIsPaidHiddenAtLaunch(toolName string) bool {
	return hidePaidMCPAtLaunch && paidMCPToolsHiddenAtLaunch[toolName]
}

// filterPaidToolsAtLaunch drops the launch-hidden paid tools from the tools/list
// response so they never reach the model. Mirrors filterOwnerOnlyTools.
func filterPaidToolsAtLaunch(tools []map[string]interface{}) []map[string]interface{} {
	if !hidePaidMCPAtLaunch {
		return tools
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name, _ := t["name"].(string)
		if paidMCPToolsHiddenAtLaunch[name] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// mcpToolDeniedAsPaidAtLaunch denies a tools/call for a launch-hidden paid tool
// (defense-in-depth alongside the list filter). Mirrors mcpToolDeniedByOwnerGate.
func mcpToolDeniedAsPaidAtLaunch(toolName string) *AccessDeniedReason {
	if !mcpToolIsPaidHiddenAtLaunch(toolName) {
		return nil
	}
	return &AccessDeniedReason{
		Denied: true,
		Reason: "tool \"" + toolName + "\" is not available in this build — use the Yaver web dashboard Billing tab",
	}
}
