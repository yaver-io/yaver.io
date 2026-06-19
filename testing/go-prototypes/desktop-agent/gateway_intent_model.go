package main

// gateway_intent_model.go — the model-backed intent classifier + a tiered
// (cheap-first, escalate-on-doubt) wrapper.
//
// keywordIntentClassifier (gateway_intent.go) is instant but literal. The model
// classifier handles paraphrase + slot-filling ("send mom fifty euros" →
// transit? bank? amount=50) by asking an LLM, but it costs a model call. So the
// production router runs the keyword classifier FIRST and only escalates to the
// model when the keyword result is ambiguous (it fell through to "code" but the
// utterance smells like a gateway request). A dev command ("fix the failing
// test") never pays for a model call.
//
// The model is reached through an injectable completion seam (intentCompleteFn)
// so this is unit-testable offline with a fake; production wires it to the user's
// configured runner via runMailDraftInline. On ANY failure (no runner, bad JSON,
// a hallucinated connector that isn't in the catalog) the classifier falls back
// to the keyword result — the model can only UPGRADE a routing decision to a
// catalog-valid one, never invent a connector.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// intentCompleteFn is a prompt→text completion. Injected so tests don't spawn a
// runner; production wires it to runMailDraftInline.
type intentCompleteFn func(ctx context.Context, prompt string) (string, error)

// modelIntentClassifier classifies via an LLM, validating the answer against the
// user's own capability catalog and falling back to keyword on any problem.
type modelIntentClassifier struct {
	complete intentCompleteFn
	fallback IntentClassifier
}

// newModelIntentClassifier wires the model classifier to a runner-backed
// completion (empty runnerID ⇒ the runMailDraftInline default).
func newModelIntentClassifier(runnerID string) *modelIntentClassifier {
	return &modelIntentClassifier{
		complete: func(ctx context.Context, prompt string) (string, error) {
			return runMailDraftInline(runnerID, prompt)
		},
		fallback: keywordIntentClassifier{},
	}
}

func (m *modelIntentClassifier) Classify(ctx context.Context, utterance string, reads, acts []MCPCapability) (IntentDecision, error) {
	fb := m.fallback
	if fb == nil {
		fb = keywordIntentClassifier{}
	}
	raw, err := m.complete(ctx, buildIntentPrompt(utterance, reads, acts))
	if err != nil {
		return fb.Classify(ctx, utterance, reads, acts)
	}
	d, ok := parseIntentJSON(raw)
	if !ok || !validateIntentDecision(&d, reads, acts) {
		return fb.Classify(ctx, utterance, reads, acts)
	}
	if d.Reason == "" {
		d.Reason = "model-classified"
	}
	return d, nil
}

// buildIntentPrompt renders the classification prompt: the catalog of the user's
// own read + act capabilities, the utterance, and a strict-JSON output contract.
func buildIntentPrompt(utterance string, reads, acts []MCPCapability) string {
	var b strings.Builder
	b.WriteString("You are an intent router for a personal automation gateway. ")
	b.WriteString("Decide how to handle the user's request. Output ONLY one JSON object, no prose.\n\n")
	b.WriteString("Engines:\n")
	b.WriteString("- \"code\": a software/dev/ops task for a coding agent (default when nothing below fits).\n")
	b.WriteString("- \"gateway_read\": read state from one of the user's apps below.\n")
	b.WriteString("- \"gateway_act\": perform an action on one of the user's apps below.\n\n")
	b.WriteString("READ capabilities (connector / capability — what it reads):\n")
	if len(reads) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, c := range reads {
		fmt.Fprintf(&b, "  - %s / %s — %s\n", c.Connector, c.Capability, intentCapDesc(c))
	}
	b.WriteString("ACT capabilities (connector / capability — what it does):\n")
	if len(acts) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, c := range acts {
		fmt.Fprintf(&b, "  - %s / %s — %s [risk: %s]\n", c.Connector, c.Capability, intentCapDesc(c), c.Risk)
	}
	b.WriteString("\nUser request: ")
	b.WriteString(strconvQuote(utterance))
	b.WriteString("\n\nOutput JSON shape:\n")
	b.WriteString(`{"engine":"code|gateway_read|gateway_act","connector":"<id or empty>","capability":"<id or empty>","params":{"<k>":"<v>"},"reason":"<short>"}`)
	b.WriteString("\nRules: connector+capability MUST be one of the pairs listed above for gateway_read/gateway_act; ")
	b.WriteString("use \"code\" with empty connector/capability if no app fits; never invent ids; ")
	b.WriteString("put any amount/quantity/recipient mentioned into params.\n")
	return b.String()
}

func intentCapDesc(c MCPCapability) string {
	d := strings.TrimSpace(c.Title)
	if d == "" {
		d = strings.TrimSpace(c.Description)
	}
	if d == "" {
		d = c.Capability
	}
	return d
}

// parseIntentJSON extracts the first JSON object from a model reply (which may
// wrap it in prose/markdown fences) and decodes it into a decision.
func parseIntentJSON(raw string) (IntentDecision, bool) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return IntentDecision{}, false
	}
	var wire struct {
		Engine     string            `json:"engine"`
		Connector  string            `json:"connector"`
		Capability string            `json:"capability"`
		Params     map[string]string `json:"params"`
		Reason     string            `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &wire); err != nil {
		return IntentDecision{}, false
	}
	eng := IntentEngine(strings.TrimSpace(strings.ToLower(wire.Engine)))
	switch eng {
	case IntentCode, IntentGatewayRead, IntentGatewayAct:
	default:
		return IntentDecision{}, false
	}
	return IntentDecision{
		Engine:     eng,
		Connector:  strings.TrimSpace(wire.Connector),
		Capability: strings.TrimSpace(wire.Capability),
		Params:     wire.Params,
		Confidence: 0.9,
		Reason:     strings.TrimSpace(wire.Reason),
	}, true
}

// validateIntentDecision rejects a decision that names a connector/capability not
// in the catalog — the model cannot route to something the user hasn't wired.
func validateIntentDecision(d *IntentDecision, reads, acts []MCPCapability) bool {
	switch d.Engine {
	case IntentCode:
		// code needs no connector; clear any stray ids the model emitted.
		d.Connector, d.Capability = "", ""
		return true
	case IntentGatewayRead:
		return capInCatalog(d.Connector, d.Capability, reads)
	case IntentGatewayAct:
		return capInCatalog(d.Connector, d.Capability, acts)
	}
	return false
}

func capInCatalog(connector, capability string, caps []MCPCapability) bool {
	for _, c := range caps {
		if c.Connector == connector && c.Capability == capability {
			return true
		}
	}
	return false
}

// ── tiered classifier: keyword first, model on doubt ─────────────────────────

// tieredIntentClassifier runs a fast classifier first and only escalates to a
// deep (model) classifier when the fast one is uncertain — it fell through to
// "code" yet the utterance carries a gateway cue (a read/act cue word or names a
// connector). A clear dev command never reaches the model.
type tieredIntentClassifier struct {
	fast IntentClassifier
	deep IntentClassifier
}

func newTieredIntentClassifier(deep IntentClassifier) tieredIntentClassifier {
	return tieredIntentClassifier{fast: keywordIntentClassifier{}, deep: deep}
}

func (t tieredIntentClassifier) Classify(ctx context.Context, utterance string, reads, acts []MCPCapability) (IntentDecision, error) {
	fast, err := t.fast.Classify(ctx, utterance, reads, acts)
	if err == nil && fast.Engine != IntentCode {
		return fast, nil // the cheap classifier already found a catalog match
	}
	if t.deep == nil || !gatewayCueLikely(utterance, reads, acts) {
		return fast, err
	}
	deep, derr := t.deep.Classify(ctx, utterance, reads, acts)
	if derr != nil {
		return fast, err
	}
	return deep, nil
}

// gatewayCueLikely reports whether an utterance plausibly targets a connector
// (so escalating to the model is worth the call). True if it carries a read/act
// cue word or mentions a registered connector id.
func gatewayCueLikely(utterance string, reads, acts []MCPCapability) bool {
	u := strings.ToLower(utterance)
	if intentContainsAny(u, actCueWords) || intentContainsAny(u, readCueWords) {
		return true
	}
	for _, c := range append(append([]MCPCapability{}, reads...), acts...) {
		if c.Connector != "" && strings.Contains(u, strings.ToLower(c.Connector)) {
			return true
		}
	}
	return false
}

// strconvQuote quotes a string for embedding in the prompt without pulling in
// strconv at call sites scattered across the file.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
