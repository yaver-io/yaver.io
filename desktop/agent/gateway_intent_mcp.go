package main

// gateway_intent_mcp.go — MCP exposure of the intent router.
//
// gateway_intent{utterance} routes a natural-language utterance and returns the
// decision. As a convenience it also takes the FIRST safe step for the user:
//   - code         → returns the decision; the caller dispatches a runner task.
//   - gateway_read → runs the read and returns the answer (reads are safe).
//   - gateway_act  → returns a DRY-RUN preview + act_id; NEVER auto-executes
//                    (an act always needs an explicit confirm).
//
// This is what a single voice box calls: it speaks the read answer, or speaks the
// act preview and asks the user to confirm, or hands a dev task to the runner.

import (
	"context"
	"time"
)

// mcpGatewayIntent routes an utterance and takes the first safe step.
func mcpGatewayIntent(utterance string) interface{} {
	if utterance == "" {
		return map[string]interface{}{"error": "utterance is required"}
	}
	deps, err := newGatewayDeps()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	decision, err := routeIntent(ctx, deps.registry, utterance, nil)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	switch decision.Engine {
	case IntentGatewayRead:
		res, err := deps.gatewayInvoke(ctx, decision.Connector, decision.Capability, decision.Params)
		if err != nil {
			return map[string]interface{}{"decision": decision, "error": err.Error()}
		}
		return map[string]interface{}{"decision": decision, "result": res}

	case IntentGatewayAct:
		conn, err := deps.registry.Get(decision.Connector)
		if err != nil {
			return map[string]interface{}{"decision": decision, "error": err.Error()}
		}
		cap, ok := conn.Capability(decision.Capability)
		if !ok {
			return map[string]interface{}{"decision": decision, "error": "capability not found"}
		}
		preview := buildActPreview(conn, cap, decision.Params, "")
		id := newActID()
		gatewayPendingActs.put(id, &pendingAct{conn: conn, cap: cap, params: decision.Params, preview: preview, created: time.Now()})
		return map[string]interface{}{
			"decision": decision,
			"act_id":   id,
			"preview":  preview,
			"note":     "This is an action. Confirm with gateway_act_confirm{act_id, answer:\"approve\"} — " + preview.ConfirmPrompt,
		}

	default: // IntentCode
		return map[string]interface{}{
			"decision": decision,
			"note":     "Routed to the coding agent — dispatch this as a runner task (e.g. via the voice/task pipeline).",
		}
	}
}
