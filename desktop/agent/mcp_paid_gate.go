package main

// HN-LAUNCH-HIDE-PAID: temporarily stop exposing/supporting Yaver's OWN paid
// (managed / metered / billed) MCP tools for the HN launch, so a fresh user's
// coding agent sees only the free + self-hosted surface. Flip
// hidePaidMCPAtLaunch to false to restore them all at once.
//
// Scope is deliberately narrow: ONLY the tools that are purely "buy/manage a
// Yaver-billed plan." The cloud_*/relay/remote_* provisioning tools are shared
// BYO (bring-your-own-token, self-hosted) paths and are NOT gated here — the
// managed side of those is already fail-closed server-side (owner allowlist +
// LemonSqueezy env), so hiding them would only break the free BYO story.
const hidePaidMCPAtLaunch = true

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
		Reason: "tool \"" + toolName + "\" is not available right now — Yaver's paid plans are turned off during the free launch",
	}
}
