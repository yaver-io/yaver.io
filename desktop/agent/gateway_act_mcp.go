package main

// gateway_act_mcp.go — MCP exposure of the ACT path (M-G7).
//
// Three tools:
//   - gateway_act{connector, capability, params, execute}
//       execute=false (DEFAULT): DRY-RUN. Returns the preview (method+endpoint or
//       step list), the Policy Guard verdict, and an act_id — NOTHING is mutated.
//       execute=true: run the full pipeline now. A low-risk act may be confirmed
//       inline via params["confirm"]="yes"; a high/financial act BLOCKS on a
//       tapped approval on the user's phone (the human gate) regardless.
//   - gateway_act_confirm{act_id, answer}
//       Completes a previously-previewed act. The separate confirm call IS the
//       second key (approve → execute; anything else → declined).
//   - gateway_audit{limit}
//       Lists recent acts from the LOCAL ledger (never Convex).
//
// This mirrors the gateway_connect / gateway_connect_finish two-step so a host AI
// (or the in-car voice surface) reviews before it commits.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// pendingAct holds a previewed-but-unconfirmed act between gateway_act{dry-run}
// and gateway_act_confirm. The params are held in memory only.
type pendingAct struct {
	conn    *Connector
	cap     *Capability
	params  map[string]string
	preview *ActPreview
	created time.Time
}

type pendingActStore struct {
	mu sync.Mutex
	m  map[string]*pendingAct
}

var gatewayPendingActs = &pendingActStore{m: map[string]*pendingAct{}}

func (s *pendingActStore) put(id string, p *pendingAct) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Evict stale entries (older than 15m) opportunistically.
	for k, v := range s.m {
		if time.Since(v.created) > 15*time.Minute {
			delete(s.m, k)
		}
	}
	s.m[id] = p
}

func (s *pendingActStore) take(id string) (*pendingAct, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[id]
	if ok {
		delete(s.m, id)
	}
	return p, ok
}

func newActID() string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return "act_" + base64.RawURLEncoding.EncodeToString(raw)
}

// mcpGatewayAct is the gateway_act entrypoint. With execute=false it previews and
// returns an act_id; with execute=true it runs the pipeline now.
func mcpGatewayAct(connector, capability string, params map[string]string, execute bool) interface{} {
	if connector == "" || capability == "" {
		return map[string]interface{}{"error": "connector and capability are required"}
	}
	deps, err := newGatewayDeps()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	conn, err := deps.registry.Get(connector)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	cap, ok := conn.Capability(capability)
	if !ok {
		return map[string]interface{}{"error": "connector " + connector + " has no capability " + capability}
	}
	if isReadVerb(cap.Verb) {
		return map[string]interface{}{"error": "capability " + capability + " is a read — use gateway_query, not gateway_act"}
	}

	jurisdiction := ""
	if params != nil {
		jurisdiction = params["__jurisdiction"]
	}

	if !execute {
		preview := buildActPreview(conn, cap, params, jurisdiction)
		id := newActID()
		gatewayPendingActs.put(id, &pendingAct{conn: conn, cap: cap, params: params, preview: preview, created: time.Now()})
		note := "DRY-RUN — nothing was changed. Review, then call gateway_act_confirm{act_id, answer:\"approve\"} to execute."
		if preview.RequiresTapKey {
			note = "DRY-RUN — nothing was changed. This is a " + cap.Risk + " act: it will require a TAPPED approval on your phone. Call gateway_act_confirm{act_id, answer:\"approve\"} to proceed (you'll still tap to confirm)."
		}
		return map[string]interface{}{"act_id": id, "preview": preview, "requires_tap_key": preview.RequiresTapKey, "note": note}
	}

	// execute=true — run now. Low risk may confirm inline via params["confirm"].
	voiceAnswer := ""
	if params != nil {
		voiceAnswer = params["confirm"]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res, err := deps.gatewayActExecute(ctx, conn, cap, params, actExecOptions{
		VoiceAnswer:  voiceAnswer,
		Gate:         gatewayGates,
		Jurisdiction: jurisdiction,
	})
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}

// mcpGatewayActConfirm completes a previewed act. The confirm call itself is the
// second key — approve → execute (pre-approved); anything else → declined.
func mcpGatewayActConfirm(actID, answer string) interface{} {
	if actID == "" {
		return map[string]interface{}{"error": "act_id is required"}
	}
	pa, ok := gatewayPendingActs.take(actID)
	if !ok {
		return map[string]interface{}{"error": "unknown or already-completed act_id"}
	}
	if !gatewayAnswerApproves(answer) {
		return map[string]interface{}{"connector": pa.conn.ID, "capability": pa.cap.ID, "outcome": "declined", "detail": "confirmation was not affirmative"}
	}
	deps, err := newGatewayDeps()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	jurisdiction := ""
	if pa.params != nil {
		jurisdiction = pa.params["__jurisdiction"]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res, err := deps.gatewayActExecute(ctx, pa.conn, pa.cap, pa.params, actExecOptions{
		PreApproved:  true, // the explicit confirm call is the second key
		Gate:         gatewayGates,
		Jurisdiction: jurisdiction,
	})
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}

// mcpGatewayAudit lists recent acts from the local ledger.
func mcpGatewayAudit(limit int) interface{} {
	if limit <= 0 {
		limit = 50
	}
	entries, err := listGatewayAudit(limit)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"count":   len(entries),
		"entries": entries,
		"note":    "Local audit ledger only — never synced to Convex. Records what Yaver did as you.",
	}
}
