package main

// gateway_redroid_invoke.go — the redroid engine path for gatewayInvoke (M-G5b).
//
// Where the "api" engine runs a single authed HTTP GET, the redroid engine
// DRIVES AN APP on the trusted device the broker already authenticated (golden
// snapshot restore, or a fresh login + 2FA — see gateway_redroid.go). It is the
// read loop from docs §1.5 specialized to the mobile/redroid surface
// (docs/yaver-self-improving-mcp-mobile-apps.md §3-§5):
//
//	observe()  → read the Screen (accessibility nodes + frame ref) and fingerprint it
//	run(flow)  → execute the capability's ordered Steps, RE-OBSERVE + VERIFY after each
//	extract()  → project the answerSchema out of the final Screen (deterministic
//	             label→value matcher; AI/vision extraction plugs in behind the same seam)
//
// SLICE SCOPE: READ-only. Steps are tap/type/wait only; no write/ACT verb runs
// here (gatewayInvoke already rejected a non-read verb). The Flow + extractor are
// shaped so the M-G6 curator (self-heal, dynamic MCP registration, vision
// extraction) slots in without changing this contract.
//
// Policy Guard (CLAUDE.md / docs §19): a challenge screen (captcha / "verify
// it's you" / unusual-activity wall) is NEVER auto-solved. It routes to the
// existing human gate (awaitHuman, gateway_gate.go) so the account owner solves
// it live via remote-view. An anti-automation / block signal → back off + record
// a structured {blocked:true} and STOP — never evade, never rotate identity.
//
// DEVICE INTERACTION IS BEHIND deviceDriver (gateway_redroid.go), injected by the
// caller (gatewayInvoke gets it from the broker's password_totp handler; tests
// inject fakeDeviceDriver). So this whole path is unit-testable WITHOUT a real
// redroid, the keychain, or the network.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Screen is the lightweight on-device screen model (docs §3): the accessibility
// nodes, a robust signature, and the app identity. The frame (pixels) is kept as
// a reference only — the deterministic extractor works off nodes; the frame is
// what a later vision extractor / a human-gate screenshot would consume.
type Screen struct {
	Nodes      []uiNode `json:"nodes"`
	Signature  string   `json:"signature"`
	FrameRef   string   `json:"frameRef,omitempty"`
	AppPkg     string   `json:"appPkg,omitempty"`
	AppVersion string   `json:"appVersion,omitempty"`
}

// redroidInvoke runs one read capability against an app on the device and returns
// its projected answer. session is the broker-issued device session (carries the
// instance id); driver is the SAME device the broker authenticated. mayNeedHuman
// hints whether this connector's auth could pause for a human (forwarded from the
// broker); the challenge-screen gate fires regardless when a wall is detected.
//
// reg, when non-nil, is the connector registry the self-heal path (M-G7) writes a
// rewritten flow back to. It is OPTIONAL: when nil (or when nothing heals) the
// flow is run unchanged and no manifest is written — so a caller that doesn't want
// auto-rewrites simply passes nil. healClock supplies the timestamp hint for a
// heal event (so the heal path never calls time.Now non-deterministically); nil ⇒
// a default wall-clock hint.
//
// deviceInvoke is the canonical name (M-G5c): the SAME path drives a redroid
// container OR a real paired phone — the engine string differs (source +
// persistence), the read loop does not. redroidInvoke is kept as an alias so
// existing callers/tests don't break.
func deviceInvoke(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, session Session, driver deviceDriver, mayNeedHuman bool, reg *ConnectorRegistry, healClock func() int64) (*gatewayResult, error) {
	if driver == nil {
		return nil, fmt.Errorf("gateway: device invoke for %q has no device driver", conn.ID)
	}

	res := &gatewayResult{
		Connector:  conn.ID,
		Capability: cap.ID,
		Verb:       cap.Verb,
		Source:     "redroid:" + conn.ID,
	}

	// Launch the app (default to the connector surface = package id).
	pkg := strings.TrimSpace(cap.Flow.LaunchPkg)
	if pkg == "" {
		pkg = strings.TrimSpace(conn.Surface)
	}
	if pkg != "" {
		if err := driver.Launch(pkg); err != nil {
			return nil, fmt.Errorf("gateway: redroid %q launch %q: %w", conn.ID, pkg, err)
		}
	}

	// Observe the entry screen, then run the flow, re-observing + verifying after
	// each step. A challenge wall at ANY observation routes to a human gate.
	screen, err := observeScreen(driver, pkg)
	if err != nil {
		return nil, fmt.Errorf("gateway: redroid %q observe: %w", conn.ID, err)
	}
	// HIGHEST precedence: a device-attestation / Play Integrity / SafetyNet wall.
	// An emulated or uncertified device can NEVER satisfy attestation, so there is
	// nothing to retry, self-heal, or gate — STOP immediately with a structured
	// result that steers to the official API or a real-device engine.
	if integrity, reason := detectIntegrityBlock(*screen); integrity {
		return integrityBlockedResult(res, conn, reason), nil
	}
	if blocked := detectBlockSignal(screen); blocked != "" {
		// A block is a "no" — surface it structured, STOP, never retry/evade.
		res.Blocked = true
		res.Detail = fmt.Sprintf("connector %q hit an anti-automation block (%s). Backing off — not retrying or evading. A block is a \"no\".", conn.ID, blocked)
		return res, nil
	}
	if challenge := detectChallengeScreen(screen); challenge != "" {
		if err := gateChallenge(ctx, conn, screen, challenge); err != nil {
			return nil, err
		}
		// After the human solved it live, re-observe before continuing.
		if screen, err = observeScreen(driver, pkg); err != nil {
			return nil, fmt.Errorf("gateway: redroid %q re-observe after challenge: %w", conn.ID, err)
		}
	}

	// runSteps is a mutable copy of the flow's steps; the self-heal path rewrites
	// entries here when a selector is re-located, and the rewritten set is
	// persisted (versioned) at the end if anything healed.
	runSteps := make([]FlowStep, len(cap.Flow.Steps))
	copy(runSteps, cap.Flow.Steps)
	anyHealed := false

	for i := range runSteps {
		step := runSteps[i]

		// SELF-HEAL (M-G7) before acting: if this step's selector target can't be
		// found on the CURRENT screen — or its expected signature drifted — try to
		// re-locate the element on the screen we're actually looking at and rewrite
		// the step. This is the bounded auto-improvement (docs §6 step 2 / §8):
		// READ-flow selector heals are auto-allowed. We NEVER heal across a
		// challenge/block (those still route to the gate / stop, above and below).
		needsRelocate := step.Target != "" && !targetPresent(*screen, step.Target)
		signatureDrift := step.ExpectSignature != "" && screen.Signature != step.ExpectSignature
		if needsRelocate || signatureDrift {
			if healed, didHeal := healFlowStep(*screen, step); didHeal {
				oldTarget := step.Target
				strategy := strategyForTargets(*screen, oldTarget, healed.Target)
				step = healed
				runSteps[i] = healed
				anyHealed = true
				res.NeedsHeal = append(res.NeedsHeal, fmt.Sprintf("step %d re-located %q -> %q (%s)", i, oldTarget, healed.Target, strategy))
				gatewayHealLog.append(healEvent{
					Connector:  conn.ID,
					Capability: cap.ID,
					StepIndex:  i,
					OldTarget:  oldTarget,
					NewTarget:  healed.Target,
					Strategy:   strategy,
					AtUnixHint: healNowHint(healClock),
				})
			} else if needsRelocate {
				// Couldn't re-locate the target at all — keep the existing
				// best-effort NeedsHeal record and run the step as-authored (the
				// driver may still resolve it; the extractor is the real oracle).
				res.NeedsHeal = append(res.NeedsHeal, fmt.Sprintf("step %d target %q not found and could not be re-located", i, step.Target))
			}
		}

		if err := runFlowStep(driver, step, params); err != nil {
			return nil, fmt.Errorf("gateway: redroid %q step %d (%s): %w", conn.ID, i, step.Action, err)
		}
		// Re-observe + verify (docs §4): trust the OBSERVED screen, not the tap.
		next, err := observeScreen(driver, pkg)
		if err != nil {
			return nil, fmt.Errorf("gateway: redroid %q observe after step %d: %w", conn.ID, i, err)
		}
		// HIGHEST precedence (same as the entry screen): an integrity/attestation
		// wall mid-flow STOPS immediately — never retry/heal/gate/fabricate.
		if integrity, reason := detectIntegrityBlock(*next); integrity {
			return integrityBlockedResult(res, conn, reason), nil
		}
		if blocked := detectBlockSignal(next); blocked != "" {
			res.Blocked = true
			res.Detail = fmt.Sprintf("connector %q hit an anti-automation block (%s) mid-flow. Backing off — not retrying or evading.", conn.ID, blocked)
			return res, nil
		}
		if challenge := detectChallengeScreen(next); challenge != "" {
			if err := gateChallenge(ctx, conn, next, challenge); err != nil {
				return nil, err
			}
			if next, err = observeScreen(driver, pkg); err != nil {
				return nil, fmt.Errorf("gateway: redroid %q re-observe after challenge: %w", conn.ID, err)
			}
		}
		// Verify advance: if the step declared an expected signature and the
		// observed one differs AFTER acting, the flow is "stale". Refresh the
		// step's ExpectSignature to the observed one (a versioned selector/sig
		// update — auto-allowed by docs §8) and record the heal. We never fail
		// hard on a signature drift — the extractor below is the real oracle.
		if step.ExpectSignature != "" && next.Signature != step.ExpectSignature {
			res.NeedsHeal = append(res.NeedsHeal, fmt.Sprintf("step %d expected signature %q, observed %q", i, step.ExpectSignature, next.Signature))
			runSteps[i].ExpectSignature = next.Signature
			anyHealed = true
			gatewayHealLog.append(healEvent{
				Connector:  conn.ID,
				Capability: cap.ID,
				StepIndex:  i,
				OldTarget:  step.Target,
				NewTarget:  step.Target, // selector unchanged; signature refreshed
				Strategy:   "signature",
				AtUnixHint: healNowHint(healClock),
			})
		}
		screen = next
	}

	// Persist any rewritten flow back to the manifest (versioned + bounded). This
	// is local-first: it writes ~/.yaver/connectors/<id>.json via the registry and
	// never touches Convex. When reg is nil (caller opted out) or nothing healed,
	// it's a no-op. A persist failure is non-fatal to the READ — the answer below
	// is still returned — it just isn't durably rewritten this time.
	if anyHealed && reg != nil {
		if _, perr := persistHealedFlow(reg, conn.ID, cap.ID, runSteps); perr != nil {
			res.NeedsHeal = append(res.NeedsHeal, "persist healed flow failed: "+perr.Error())
		}
	}

	// Extract the answerSchema from the final screen (deterministic matcher;
	// vision/AI extraction plugs in behind screenExtractor).
	answer := defaultExtractor.Extract(screen, cap.AnswerSchema)
	res.Answer = answer
	res.Signature = screen.Signature
	return res, nil
}

// redroidInvoke is the original name for the device-invoke path, kept as a thin
// alias so existing callers/tests that name it keep compiling. New code calls
// deviceInvoke (which serves both the "redroid" and "device" engines).
func redroidInvoke(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, session Session, driver deviceDriver, mayNeedHuman bool, reg *ConnectorRegistry, healClock func() int64) (*gatewayResult, error) {
	return deviceInvoke(ctx, conn, cap, params, session, driver, mayNeedHuman, reg, healClock)
}

// observeScreen reads the device's accessibility nodes + a frame reference and
// computes the ScreenSignature (docs §3). The frame bytes themselves are not
// retained (only a short ref/hash) — the deterministic extractor reads nodes.
func observeScreen(driver deviceDriver, pkg string) (*Screen, error) {
	nodes, err := driver.UiTexts()
	if err != nil {
		return nil, err
	}
	s := &Screen{Nodes: nodes, AppPkg: pkg}
	// Frame is best-effort: a missing frame must not fail a read (the extractor
	// is node-based). We keep only a short content hash as a reference so a later
	// vision extractor / a gate screenshot can be attached without holding pixels.
	if frame, fErr := driver.Frame(); fErr == nil && len(frame) > 0 {
		sum := sha256.Sum256(frame)
		s.FrameRef = "sha256:" + hex.EncodeToString(sum[:8])
	}
	s.Signature = ScreenSignature(s)
	return s, nil
}

// ScreenSignature is a robust, DETERMINISTIC fingerprint of a screen (docs §3):
// the set of salient resource-ids plus a "text shape" hash (the static label
// structure, with volatile tokens — digits, times — normalized out). The goal is
// that the SAME logical screen produces the SAME signature across trivial changes
// (a different balance, a rotating code, a timestamp), while a DIFFERENT screen
// (different controls/labels) produces a different one. Flows key off this so a
// minor UI shift doesn't break replay, but a big shift (signature miss) triggers
// self-heal. Kept simple + deterministic on purpose — no vision embedding yet
// (that augments, not replaces, this in M-G6).
func ScreenSignature(s *Screen) string {
	if s == nil {
		return ""
	}
	// 1. Salient resource-ids: stable structural anchors, order-independent.
	ids := map[string]struct{}{}
	for _, n := range s.Nodes {
		if id := strings.TrimSpace(n.ResourceID); id != "" {
			ids[id] = struct{}{}
		}
	}
	idList := make([]string, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	sort.Strings(idList)

	// 2. Text shape: the set of NORMALIZED static labels (volatile tokens masked)
	// so the same screen with different live values fingerprints identically.
	shapes := map[string]struct{}{}
	for _, n := range s.Nodes {
		for _, raw := range []string{n.Text, n.ContentDesc} {
			norm := normalizeLabel(raw)
			if norm != "" {
				shapes[norm] = struct{}{}
			}
		}
	}
	shapeList := make([]string, 0, len(shapes))
	for sh := range shapes {
		shapeList = append(shapeList, sh)
	}
	sort.Strings(shapeList)

	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(s.AppPkg)))
	h.Write([]byte("\x00ids\x00"))
	h.Write([]byte(strings.Join(idList, "\x01")))
	h.Write([]byte("\x00shape\x00"))
	h.Write([]byte(strings.Join(shapeList, "\x01")))
	return "sig:" + hex.EncodeToString(h.Sum(nil)[:12])
}

// normalizeLabel collapses a label into its stable "shape": lowercased, with
// runs of digits → "#" and surrounding whitespace squeezed, so "Balance: $42.10"
// and "Balance: $7.00" share a shape but "Balance" and "Settings" do not. Pure
// punctuation / empty strings drop out (they carry no structure).
func normalizeLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevDigit := false
	prevSpace := false
	hasLetter := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			if !prevDigit {
				b.WriteByte('#')
			}
			prevDigit = true
			prevSpace = false
		case r == ' ' || r == '\t' || r == '\n':
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			prevSpace = true
			prevDigit = false
		default:
			if r >= 'a' && r <= 'z' {
				hasLetter = true
			}
			b.WriteRune(r)
			prevDigit = false
			prevSpace = false
		}
	}
	out := strings.TrimSpace(b.String())
	// A label with no letters (pure "#"/punctuation) carries no structural value.
	if !hasLetter {
		return ""
	}
	return out
}

// runFlowStep executes one UI step. tap/type/wait only (read-only slice). type
// substitutes {param} placeholders from params (same convention as the api path).
func runFlowStep(driver deviceDriver, step FlowStep, params map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(step.Action)) {
	case "tap", "click":
		return driver.Tap(step.Target)
	case "type", "input":
		return driver.Type(substituteParams(step.Text, params))
	case "wait", "":
		// A wait re-observes only (handled by the caller's re-observe). No-op here
		// so a flow can insert an explicit settle point without an action.
		return nil
	default:
		return fmt.Errorf("unsupported step action %q (read-only slice: tap|type|wait)", step.Action)
	}
}

// ── extraction seam ───────────────────────────────────────────────────────────
//
// screenExtractor projects an answerSchema out of an observed Screen. The default
// impl is a DETERMINISTIC label→value matcher over the node text/contentDesc. An
// AI/vision extractor (reading the frame for values the accessibility tree omits)
// implements the SAME interface and slots in behind this seam in M-G6 — the
// answerSchema contract it satisfies is identical, so nothing downstream changes.

type screenExtractor interface {
	// Extract returns outKey→value for the schema. It is best-effort: a field it
	// cannot find is OMITTED rather than erroring (a redroid read of a live app
	// is fuzzier than a typed JSON API — partial answers are useful, and the
	// curator/AI extractor fills the gaps later). When the schema is empty it
	// returns the raw visible node texts so the caller still gets the screen.
	Extract(s *Screen, schema map[string]string) map[string]interface{}
}

// deterministicExtractor matches answerSchema fields against on-screen nodes by
// label, with no model dependency — pure, offline, deterministic.
type deterministicExtractor struct{}

// defaultExtractor is the process default. Swappable (a var, not a const) so a
// later slice can install an AI/vision extractor without touching redroidInvoke.
var defaultExtractor screenExtractor = deterministicExtractor{}

// Extract implements screenExtractor.
//
// answerSchema is { outKey: spec } reusing the api-path spec grammar, where spec
// is "type" or "label:type". For redroid the source is a SCREEN LABEL, not a JSON
// path:
//
//   - "label:type" → find the node whose text/contentDesc matches "label"
//     (case-insensitive substring) and return the value that follows it on the
//     SAME node (e.g. "Balance: $42.10" → "$42.10") or, failing that, the text of
//     the NEXT node (the common "label above value" layout).
//   - "type" only  → use outKey as the label (so {"balance":"string"} matches a
//     "Balance" label).
//   - trailing "?" → optional (already implied; all fields are best-effort here).
//
// When NOTHING matches and the schema is empty, the raw node texts are returned
// under "screen" so the caller still gets something to reason about (and a later
// AI extractor has the raw material).
func (deterministicExtractor) Extract(s *Screen, schema map[string]string) map[string]interface{} {
	out := map[string]interface{}{}
	if s == nil {
		return out
	}
	if len(schema) == 0 {
		texts := make([]string, 0, len(s.Nodes))
		for _, n := range s.Nodes {
			if t := strings.TrimSpace(n.Text); t != "" {
				texts = append(texts, t)
			}
		}
		out["screen"] = texts
		return out
	}
	for outKey, spec := range schema {
		label := redroidLabelForSchema(outKey, spec)
		if val, ok := matchLabelValue(s.Nodes, label); ok {
			out[outKey] = val
		}
		// Unmatched fields are omitted (best-effort) — the AI/vision extractor
		// behind this seam fills them later without changing the contract.
	}
	return out
}

// redroidLabelForSchema derives the on-screen LABEL to look for from an
// answerSchema entry. It reuses the api-path "label:type" split (the left side is
// the source) and falls back to the outKey when only a type is given.
func redroidLabelForSchema(outKey, spec string) string {
	src := outKey
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		src = spec[:i]
	}
	src = strings.TrimSuffix(strings.TrimSpace(src), "?")
	if src == "" {
		return outKey
	}
	return src
}

// matchLabelValue finds the value associated with a label among the nodes:
//  1. a node whose text contains "<label>:" → the remainder after the colon, or
//     the trailing value on the same node;
//  2. else a node whose text/contentDesc EQUALS the label → the next non-empty
//     node's text (label-above-value layout);
//  3. else "" / not found.
func matchLabelValue(nodes []uiNode, label string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(label))
	if want == "" {
		return "", false
	}
	// Pass 1: "Label: value" on the same node.
	for _, n := range nodes {
		t := strings.TrimSpace(n.Text)
		low := strings.ToLower(t)
		if idx := strings.Index(low, want+":"); idx >= 0 {
			rest := strings.TrimSpace(t[idx+len(want)+1:])
			if rest != "" {
				return rest, true
			}
		}
	}
	// Pass 2: exact label node → next non-empty node's text.
	for i, n := range nodes {
		if strings.EqualFold(strings.TrimSpace(n.Text), label) ||
			strings.EqualFold(strings.TrimSpace(n.ContentDesc), label) {
			for j := i + 1; j < len(nodes); j++ {
				if v := strings.TrimSpace(nodes[j].Text); v != "" {
					return v, true
				}
			}
		}
	}
	// Pass 3: contentDesc carries "Label, value" (a11y descriptions often do).
	for _, n := range nodes {
		d := strings.TrimSpace(n.ContentDesc)
		low := strings.ToLower(d)
		if idx := strings.Index(low, want); idx == 0 && len(d) > len(label) {
			rest := strings.TrimSpace(strings.TrimLeft(d[len(label):], ":,- "))
			if rest != "" {
				return rest, true
			}
		}
	}
	return "", false
}

// ── Policy Guard: challenge + block detection ─────────────────────────────────

// detectChallengeScreen returns a non-empty reason when the screen looks like a
// human-verification challenge (captcha / "verify it's you" / device-trust). The
// machine NEVER solves these — it routes to a live human gate. Detection is
// conservative (keyword-based) on purpose: a false positive just asks the user;
// it never auto-solves.
func detectChallengeScreen(s *Screen) string {
	if s == nil {
		return ""
	}
	for _, n := range s.Nodes {
		blob := strings.ToLower(n.Text + " " + n.ContentDesc + " " + n.ResourceID)
		for _, kw := range []string{
			"captcha", "recaptcha", "hcaptcha", "i'm not a robot", "im not a robot",
			"verify it's you", "verify its you", "are you human", "select all images",
			"prove you're human", "prove you are human", "security check",
			"unusual activity", "confirm it's you",
		} {
			if strings.Contains(blob, kw) {
				return kw
			}
		}
	}
	return ""
}

// detectBlockSignal returns a non-empty reason when the screen indicates the app
// has BLOCKED automation (rate-limit / account-locked / access-denied). A block
// is a "no": the caller surfaces it structured and STOPS — no retry, no evasion.
func detectBlockSignal(s *Screen) string {
	if s == nil {
		return ""
	}
	for _, n := range s.Nodes {
		blob := strings.ToLower(n.Text + " " + n.ContentDesc)
		for _, kw := range []string{
			"too many requests", "rate limit", "rate-limited", "temporarily blocked",
			"access denied", "account locked", "account has been locked",
			"automated access", "unusual traffic", "try again later",
		} {
			if strings.Contains(blob, kw) {
				return kw
			}
		}
	}
	return ""
}

// detectIntegrityBlock returns (true, reason) when the screen indicates a Play
// Integrity / SafetyNet / device-attestation FAILURE — the app has decided this
// device isn't a genuine, certified, untampered device and won't let the user in.
//
// This is DISTINCT from the other two signals and outranks both:
//   - a challenge (detectChallengeScreen) is a HUMAN-solvable puzzle (captcha,
//     "verify it's you") → route to the live human gate;
//   - a generic block (detectBlockSignal) is a rate-limit / lockout that backs
//     off and could succeed later from the same device;
//   - an integrity block is a HARD device-class rejection: an emulated /
//     uncertified / rooted device can NEVER pass attestation no matter what the
//     user does here. Retrying, self-healing the flow, or asking a human to solve
//     it are all futile (and the gate would just bounce). The honest move is to
//     STOP and steer the user to the official API or a genuine certified device.
//
// Detection is conservative keyword-matching on the login-blocking copy these
// failures show. We match attestation-specific phrasing ("Play Integrity",
// "SafetyNet", "device isn't secure", "rooted device", "device not supported",
// the login-blocking "update Google Play services to continue") rather than
// generic words, so it doesn't over-trigger on an ordinary challenge or block.
func detectIntegrityBlock(s Screen) (bool, string) {
	for _, n := range s.Nodes {
		blob := strings.ToLower(n.Text + " " + n.ContentDesc + " " + n.ResourceID)
		for _, kw := range []string{
			"play integrity", "safetynet", "device attestation", "attestation failed",
			"device not supported", "unsupported device",
			"this device isn't secure", "device isn't secure", "device is not secure",
			"rooted device", "device is rooted", "your device is rooted",
			"can't verify it's you", "cant verify it's you", "can't verify its you",
			"couldn't verify your device", "could not verify your device",
			"update google play services to continue",
		} {
			if strings.Contains(blob, kw) {
				return true, kw
			}
		}
	}
	return false, ""
}

// integrityBlockedResult populates a gatewayResult for an integrity/attestation
// block: Blocked AND IntegrityBlocked, no answer, and a Detail that is honest
// about WHY (a genuine certified device is required) and what to do instead
// (official API / real-device engine). A block is a "no", not a puzzle.
func integrityBlockedResult(res *gatewayResult, conn *Connector, reason string) *gatewayResult {
	res.Blocked = true
	res.IntegrityBlocked = true
	res.Answer = nil
	res.Detail = fmt.Sprintf("connector %q hit a device-integrity/attestation block (%s). "+
		"This app requires a genuine, certified device and will not run on an emulated or "+
		"uncertified one — switch to the service's official API or a real-device engine. "+
		"Not retrying, self-healing, or routing to a human gate: a block is a \"no\", not a puzzle.",
		conn.ID, reason)
	return res
}

// gateChallenge suspends on the interactive human gate (gateway_gate.go) so the
// account owner solves the challenge live via remote-view. We attach a frame ref
// and a redroid view ref. On timeout/decline → clean abort + recorded finding;
// the machine NEVER auto-solves.
func gateChallenge(ctx context.Context, conn *Connector, s *Screen, reason string) error {
	frameRef := ""
	if s != nil {
		frameRef = s.FrameRef
	}
	res, err := gatewayGates.awaitHuman(ctx, GateRequest{
		ConnectorID:   conn.ID,
		Kind:          GateInteractive,
		Prompt:        fmt.Sprintf("%q shows a verification challenge (%s). Solve it in the remote-view window — Yaver does not auto-solve challenges.", conn.ID, reason),
		ScreenshotRef: frameRef,
		ViewRef:       "redroid:" + conn.ID,
	})
	if err != nil {
		return fmt.Errorf("gateway: redroid %q challenge gate: %w", conn.ID, err)
	}
	if res.Status != GateResolved {
		return fmt.Errorf("gateway: redroid %q challenge not resolved (status %q) — aborting read; a challenge is never auto-solved", conn.ID, res.Status)
	}
	return nil
}
