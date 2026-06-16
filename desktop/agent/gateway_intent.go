package main

// gateway_intent.go — the intent router (the keystone of "one interface").
//
// A single voice/text utterance can mean two very different things:
//
//	"fix the failing test in the auth package"  → a DEV task for a coding agent
//	"how much is on my transit card"            → a gateway READ
//	"top up my transit card by 100"             → a gateway ACT
//
// routeIntent decides which engine should handle an utterance and, for the
// gateway engines, WHICH connector + capability + params. It is the missing brain
// that lets the car/phone/watch surface dispatch "anything" without the user
// naming a connector.
//
// The classifier is an interface so a model-backed classifier can drop in behind
// the same contract. The default keywordIntentClassifier is DETERMINISTIC and
// offline (matches the utterance against the user's own registered connectors +
// capabilities) — useful as a real first cut and fully unit-testable. Ambiguity
// always falls back to the coding agent (the safe default: it asks the user
// rather than acting), and an ACT is NEVER auto-executed by routing — it returns
// a dry-run the caller must confirm.

import (
	"context"
	"sort"
	"strings"
)

// IntentEngine is the target an utterance routes to.
type IntentEngine string

const (
	IntentCode        IntentEngine = "code"         // a coding/ops task → the runner
	IntentGatewayRead IntentEngine = "gateway_read" // read state from a connector
	IntentGatewayAct  IntentEngine = "gateway_act"  // act on a connector (needs confirm)
)

// IntentDecision is the routing result.
type IntentDecision struct {
	Engine     IntentEngine      `json:"engine"`
	Connector  string            `json:"connector,omitempty"`
	Capability string            `json:"capability,omitempty"`
	Params     map[string]string `json:"params,omitempty"`
	Confidence float64           `json:"confidence"`
	Reason     string            `json:"reason,omitempty"`
}

// IntentClassifier maps an utterance + the user's available capabilities onto a
// decision. reads/acts are the connector capability catalogs.
type IntentClassifier interface {
	Classify(ctx context.Context, utterance string, reads, acts []MCPCapability) (IntentDecision, error)
}

// routeIntent loads the user's connector catalog and classifies the utterance.
// classifier nil ⇒ the default keyword classifier.
func routeIntent(ctx context.Context, reg *ConnectorRegistry, utterance string, classifier IntentClassifier) (IntentDecision, error) {
	if classifier == nil {
		classifier = keywordIntentClassifier{}
	}
	reads, err := reg.CapabilitiesForMCP()
	if err != nil {
		return IntentDecision{}, err
	}
	acts, err := reg.ActCapabilitiesForMCP()
	if err != nil {
		return IntentDecision{}, err
	}
	return classifier.Classify(ctx, utterance, reads, acts)
}

// keywordIntentClassifier is a deterministic, dependency-free classifier. It
// scores each capability by how strongly the utterance overlaps the connector
// id, the capability id/title, and (for acts) act verbs; the best match above a
// threshold wins. No match ⇒ route to the coding agent.
type keywordIntentClassifier struct{}

// actCueWords bias an utterance toward an ACT when no capability dominates.
var actCueWords = []string{
	"buy", "purchase", "pay", "order", "book", "top up", "topup", "start", "stop",
	"charge", "deposit", "withdraw", "send", "transfer", "cancel", "renew", "schedule",
}

// readCueWords bias toward a READ.
var readCueWords = []string{
	"how much", "what is", "what's", "balance", "status", "is my", "do i have",
	"when is", "show me", "check", "price", "are there", "how many",
}

func (keywordIntentClassifier) Classify(ctx context.Context, utterance string, reads, acts []MCPCapability) (IntentDecision, error) {
	u := strings.ToLower(strings.TrimSpace(utterance))
	if u == "" {
		return IntentDecision{Engine: IntentCode, Confidence: 0, Reason: "empty utterance"}, nil
	}
	tokens := intentTokenize(u)

	bestRead, readScore := bestCapMatch(u, tokens, reads)
	bestAct, actScore := bestCapMatch(u, tokens, acts)

	// Cue-word nudge: a clear act/read cue tips an otherwise-even contest.
	if intentContainsAny(u, actCueWords) {
		actScore += 1.0
	}
	if intentContainsAny(u, readCueWords) {
		readScore += 1.0
	}

	const threshold = 2.0
	switch {
	case actScore >= threshold && actScore >= readScore:
		return IntentDecision{
			Engine: IntentGatewayAct, Connector: bestAct.Connector, Capability: bestAct.Capability,
			Params: intentParams(u, tokens), Confidence: scoreToConfidence(actScore),
			Reason: "matched act capability " + bestAct.Connector + "/" + bestAct.Capability,
		}, nil
	case readScore >= threshold:
		return IntentDecision{
			Engine: IntentGatewayRead, Connector: bestRead.Connector, Capability: bestRead.Capability,
			Params: intentParams(u, tokens), Confidence: scoreToConfidence(readScore),
			Reason: "matched read capability " + bestRead.Connector + "/" + bestRead.Capability,
		}, nil
	default:
		return IntentDecision{Engine: IntentCode, Confidence: 0.3, Reason: "no strong connector match — routing to the coding agent"}, nil
	}
}

// bestCapMatch returns the highest-scoring capability and its score.
func bestCapMatch(u string, tokens []string, caps []MCPCapability) (MCPCapability, float64) {
	var best MCPCapability
	var bestScore float64
	for _, c := range caps {
		s := capMatchScore(u, tokens, c)
		if s > bestScore {
			bestScore = s
			best = c
		}
	}
	return best, bestScore
}

// capMatchScore scores how well an utterance matches a capability. A connector-id
// hit is worth the most (it names the app); capability/title token overlaps add
// up. Verb hits help acts.
func capMatchScore(u string, tokens []string, c MCPCapability) float64 {
	var score float64
	connTok := strings.ToLower(c.Connector)
	if connTok != "" && strings.Contains(u, connTok) {
		score += 2.0 // the utterance names the app
	}
	capTokens := intentTokenize(c.Capability + " " + c.Title + " " + c.Description)
	overlap := tokenOverlap(tokens, capTokens)
	score += float64(overlap)
	if v := strings.ToLower(strings.TrimSpace(c.Verb)); v != "" && v != "get" && strings.Contains(u, v) {
		score += 1.0
	}
	return score
}

// intentParams extracts trivially-recognizable params from an utterance: a bare
// integer becomes {"amount": n} (top-ups, quantities). Deliberately minimal —
// richer slot-filling is the model classifier's job; this keeps the default
// offline + predictable.
func intentParams(u string, tokens []string) map[string]string {
	for _, t := range tokens {
		if isAllDigits(t) {
			return map[string]string{"amount": t}
		}
	}
	return nil
}

// ── small deterministic text helpers (no deps) ───────────────────────────────

func intentTokenize(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, f := range fields {
		if len(f) < 3 || intentStopword(f) {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func tokenOverlap(a, b []string) int {
	set := map[string]struct{}{}
	for _, t := range a {
		set[t] = struct{}{}
	}
	n := 0
	for _, t := range b {
		if _, ok := set[t]; ok {
			n++
		}
	}
	return n
}

func intentStopword(w string) bool {
	switch w {
	case "the", "and", "for", "with", "that", "this", "from", "you", "your", "are",
		"was", "will", "can", "please", "would", "could", "have", "has", "how", "what",
		"whats", "much", "many", "when", "where", "which", "into", "out", "now", "yes", "okay":
		return true
	}
	return false
}

func intentContainsAny(u string, words []string) bool {
	for _, w := range words {
		if strings.Contains(u, w) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func scoreToConfidence(score float64) float64 {
	c := score / 6.0
	if c > 0.99 {
		return 0.99
	}
	return c
}
