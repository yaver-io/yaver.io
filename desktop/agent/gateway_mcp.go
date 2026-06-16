package main

// gateway_mcp.go — MCP exposure of the Personal Agent Gateway.
//
// Slice 1 surfaces a single dispatcher tool, gateway_query{connector,
// capability, params}, which runs one READ capability against one connector and
// returns its structured answer (projected to the capability's answerSchema).
// This is "your apps as tools": the host AI (Claude Code, the in-car voice
// surface) calls gateway_query to read state from any credentialed service you
// have wired up — no per-app integration in the AI.
//
// The dynamic per-capability tool registration (gw_<connector>_<capability>) is
// the Slice-1b stretch (docs §1.6) and is intentionally NOT built here.
//
// READ-ONLY: gateway_query can only invoke read capabilities. There is no
// write/ACT verb in this slice.

import (
	"context"
	"time"
)

// mcpGatewayQuery is the gateway_query MCP tool entrypoint. It resolves the
// production gateway (on-disk registry + vault-backed broker), runs the
// capability, and returns a structured result. Credential acquisition/refresh
// happens inside gatewayInvoke via the broker.
func mcpGatewayQuery(connector, capability string, params map[string]string) interface{} {
	if connector == "" || capability == "" {
		return map[string]interface{}{"error": "connector and capability are required"}
	}
	deps, err := newGatewayDeps()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := deps.gatewayInvoke(ctx, connector, capability, params)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}

// mcpGatewayCapabilities lists the read capabilities available across all wired
// connectors — useful for a host AI to discover what gateway_query can call.
// (Not registered as its own MCP tool in this minimal slice, but available for
// the dispatch layer / future per-capability registration.)
func mcpGatewayCapabilities() interface{} {
	reg, err := NewConnectorRegistry()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	caps, err := reg.CapabilitiesForMCP()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"count":        len(caps),
		"capabilities": caps,
		"note":         "Read-only. Call one via gateway_query{connector, capability, params}.",
	}
}
