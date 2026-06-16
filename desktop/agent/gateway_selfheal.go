package main

// gateway_selfheal.go — M-G7: the SELF-HEAL loop for redroid read flows.
//
// This is the bounded auto-improvement from the architecture (docs §6 step 2 +
// docs §8 "curator"): when a flow step's target can't be found on the current
// screen — or its observed ScreenSignature drifts because the app's UI changed —
// re-locate the element on the CURRENT screen via multi-strategy matching, retry,
// and on success REWRITE the flow (versioned + logged) so the connector survives
// app UI changes.
//
// BOUNDED-IMPROVEMENT GUARD (docs §8 — the safety contract):
//
//	auto-applied (low blast radius): selector heals, READ-flow selector refinements,
//	screen-signature updates.
//	requires human confirm (NEVER touched here): new ACT/financial capabilities,
//	auth changes, policy / spend-cap changes.
//
// So healing here ONLY rewrites a step's selector Target + its ExpectSignature on
// a READ capability. It must NOT add/modify capabilities, verbs, auth, the
// connector engine, or anything ACT. `assertHealIsBounded` enforces this in code
// (and a test asserts it), so a self-rewriting system can never silently grant
// itself a write/transfer/auth capability via a "heal".
//
// A challenge wall is STILL never auto-solved (it routes to the human gate in
// gateway_redroid_invoke.go) and a block is STILL a "no" — self-heal only kicks
// in for an ordinary "selector not found / signature drifted" condition, never to
// route around a challenge or a block.
//
// The heal log + the rewritten manifests are LOCAL-FIRST: the heal log is an
// in-process ring (never Convex), and persistHealedFlow writes the manifest back
// through ConnectorRegistry.Store (which is ~/.yaver/connectors/<id>.json on local
// disk). Privacy: a heal event records only selector strings (resource-ids / text
// labels), never credentials, never a screenshot, never an absolute path.
//
// Determinism: nothing here calls time.Now in a way a test can't control. The
// timestamp on a heal event is PASSED IN (atUnixHint) — production passes a real
// clock, tests pass a fixed counter — so the whole path is deterministic offline.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// healNowHint resolves the timestamp hint for a heal event. Production passes nil
// and gets a wall-clock unix second; a test passes a deterministic counter so the
// heal path stays reproducible offline. Centralized so the single time.Now call
// site is here, behind the injected-clock seam — not scattered through the invoke
// loop.
func healNowHint(clock func() int64) int64 {
	if clock != nil {
		return clock()
	}
	return time.Now().Unix()
}

// ── multi-strategy locator ────────────────────────────────────────────────────

// visionLocate is the SEAM where an AI/vision locator plugs in later (docs §6
// step 2: "vision-LLM re-locates the target by intent"). It has the SAME
// signature as the deterministic strategies so it can be swapped in behind
// locateTarget without changing any caller. The default is a NO-OP (returns
// ok=false) — the deterministic strategies do all the work in this slice.
//
// It is a var, not a const, so a later slice installs a real vision locator
// (reusing testkit_self_heal_selector / the host vision-LLM) without touching
// this file's callers. When it lands it must obey the SAME bounded-improvement
// guard: it may only return a selector for the SAME logical element, never widen
// scope to a new capability/verb/auth.
var visionLocate = func(screen Screen, target string) (matchedTarget string, node *uiNode, ok bool) {
	return "", nil, false
}

// locateTarget re-locates `target` on `screen` using multiple strategies in
// RESILIENCE ORDER (most precise / most stable first, fuzziest last):
//
//  1. exact ResourceID      — the most stable structural anchor.
//  2. exact ContentDesc     — accessibility description, stable across themes.
//  3. exact Text            — the visible label, exact match.
//  4. normalized Text contains — case-insensitive, whitespace-normalized substring
//     (survives a trailing-glyph / casing / spacing change).
//  5. visionLocate (seam)   — AI/vision intent match (default no-op).
//
// It returns the strategy-appropriate target string to PERSIST: for a
// ResourceID/ContentDesc/Text hit it returns that exact node attribute (so the
// rewritten Flow re-anchors on the strongest signal the new screen offers); for a
// normalized-contains hit it returns the node's exact Text (promoting the fuzzy
// match to an exact one for next time).
func locateTarget(screen Screen, target string) (matchedTarget string, node *uiNode, ok bool) {
	want := strings.TrimSpace(target)
	if want == "" {
		return "", nil, false
	}

	// 1. exact ResourceID.
	for i := range screen.Nodes {
		if strings.TrimSpace(screen.Nodes[i].ResourceID) == want {
			return screen.Nodes[i].ResourceID, &screen.Nodes[i], true
		}
	}
	// 2. exact ContentDesc.
	for i := range screen.Nodes {
		if strings.TrimSpace(screen.Nodes[i].ContentDesc) == want {
			return screen.Nodes[i].ContentDesc, &screen.Nodes[i], true
		}
	}
	// 3. exact Text.
	for i := range screen.Nodes {
		if strings.TrimSpace(screen.Nodes[i].Text) == want {
			return screen.Nodes[i].Text, &screen.Nodes[i], true
		}
	}
	// 4. normalized / case-insensitive Text contains. Persist the node's exact
	// Text so the heal upgrades a fuzzy match into a precise one.
	wantNorm := strings.ToLower(want)
	for i := range screen.Nodes {
		t := strings.TrimSpace(screen.Nodes[i].Text)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		if strings.Contains(low, wantNorm) || strings.Contains(wantNorm, low) {
			return t, &screen.Nodes[i], true
		}
	}
	// 5. vision seam (default no-op).
	if mt, n, vok := visionLocate(screen, target); vok {
		return mt, n, true
	}
	return "", nil, false
}

// targetPresent reports whether `target` is DIRECTLY resolvable on `screen` by
// any of the exact strategies (resource-id / contentDesc / exact text). This is
// the "no heal needed" fast path: if the original selector still hits, we don't
// rewrite anything.
func targetPresent(screen Screen, target string) bool {
	want := strings.TrimSpace(target)
	if want == "" {
		// An empty target (e.g. a "wait" step) is trivially "present" — nothing to
		// locate, nothing to heal.
		return true
	}
	for i := range screen.Nodes {
		n := &screen.Nodes[i]
		if strings.TrimSpace(n.ResourceID) == want ||
			strings.TrimSpace(n.ContentDesc) == want ||
			strings.TrimSpace(n.Text) == want {
			return true
		}
	}
	return false
}

// ── step healing ──────────────────────────────────────────────────────────────

// healFlowStep attempts to repair one FlowStep against the freshly-observed
// `screen`. If the step's Target is already directly present it returns the step
// unchanged with healed=false (nothing to do). Otherwise it re-locates via
// locateTarget; on a hit it returns an updated FlowStep with the new Target AND a
// refreshed ExpectSignature (the signature of the screen the heal was observed
// against), and healed=true. On a miss it returns the original step + healed=false
// (the caller keeps its existing best-effort NeedsHeal behavior).
//
// It only ever changes Target + ExpectSignature: Action / Text are preserved
// verbatim (healing a SELECTOR never changes what the step DOES).
func healFlowStep(screen Screen, step FlowStep) (FlowStep, bool) {
	// A type/wait step with no selector target (it types literal text or settles)
	// has nothing to locate — never "heal" it.
	if strings.TrimSpace(step.Target) == "" {
		return step, false
	}
	if targetPresent(screen, step.Target) {
		return step, false
	}
	matched, _, ok := locateTarget(screen, step.Target)
	if !ok || strings.TrimSpace(matched) == "" {
		return step, false
	}
	healed := step // copy — Action/Text preserved
	healed.Target = matched
	healed.ExpectSignature = ScreenSignature(&screen)
	return healed, true
}

// ── heal log (local-first, in-process) ────────────────────────────────────────

// healEvent is one structured self-heal record. It captures ONLY selector strings
// + indices — never a credential, a screenshot, or an absolute path — so it is
// safe to keep in process and (if ever surfaced) safe to show. It is LOCAL-FIRST:
// it never goes to Convex.
type healEvent struct {
	Connector  string `json:"connector"`
	Capability string `json:"capability"`
	StepIndex  int    `json:"stepIndex"`
	OldTarget  string `json:"oldTarget"`
	NewTarget  string `json:"newTarget"`
	Strategy   string `json:"strategy"`
	// AtUnixHint is the caller-supplied timestamp (a real unix time in production,
	// a deterministic counter in tests). We never call time.Now here so the path
	// stays deterministic under test.
	AtUnixHint int64 `json:"atUnixHint"`
}

// healLog is a small, bounded, in-process ring of heal events. Local-first by
// construction: it lives only in agent memory and is never synced to Convex. The
// curator (M-G6, future) can read it to score reliability; for now it's the audit
// trail the bounded-improvement contract requires ("all changes ... audited").
type healLog struct {
	mu     sync.Mutex
	events []healEvent
	max    int
}

// gatewayHealLog is the process-wide heal log. Tests use their own instance via
// newHealLog so they never depend on global state.
var gatewayHealLog = newHealLog(256)

func newHealLog(max int) *healLog {
	if max <= 0 {
		max = 256
	}
	return &healLog{max: max}
}

// append records a heal event, dropping the oldest when the ring is full.
func (l *healLog) append(ev healEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
	if len(l.events) > l.max {
		l.events = l.events[len(l.events)-l.max:]
	}
}

// snapshot returns a copy of the current events (for tests / a future curator).
func (l *healLog) snapshot() []healEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]healEvent, len(l.events))
	copy(out, l.events)
	return out
}

// strategyForTargets infers which locate strategy produced the new target by
// comparing it to the nodes — purely for the audit label. Best-effort; defaults
// to "relocate".
func strategyForTargets(screen Screen, oldTarget, newTarget string) string {
	nt := strings.TrimSpace(newTarget)
	for i := range screen.Nodes {
		n := &screen.Nodes[i]
		switch nt {
		case strings.TrimSpace(n.ResourceID):
			return "resourceId"
		case strings.TrimSpace(n.ContentDesc):
			return "contentDesc"
		case strings.TrimSpace(n.Text):
			// Distinguish an exact-text re-anchor from a normalized-contains promote.
			if strings.EqualFold(strings.TrimSpace(oldTarget), nt) {
				return "text"
			}
			return "textContains"
		}
	}
	return "relocate"
}

// ── persistence (bounded, versioned, local-first) ─────────────────────────────

// persistHealedFlow writes the healed flow steps back into the connector manifest
// for `capabilityID` and bumps that capability's CapabilityFlow.Version, then
// returns the saved connector. It ONLY rewrites the Steps of the matching READ
// capability — assertHealIsBounded guards that the operation touches nothing but
// selectors (no verb/auth/engine/capability-set change).
//
// It reads the connector fresh from the registry, mutates in place, asserts the
// bounded-improvement invariant against the pre-image, then Stores. The heal log
// entries are appended by the CALLER (which knows the per-step old/new targets);
// this function is the manifest-rewrite half.
func persistHealedFlow(reg *ConnectorRegistry, connectorID, capabilityID string, updatedSteps []FlowStep) (*Connector, error) {
	if reg == nil {
		return nil, fmt.Errorf("gateway selfheal: nil registry")
	}
	before, err := reg.Get(connectorID)
	if err != nil {
		return nil, fmt.Errorf("gateway selfheal: load connector %q: %w", connectorID, err)
	}
	// Deep-ish copy of the pre-image for the bounded-improvement assertion (we
	// only need to compare scalar/auth fields + capability shape, so a shallow
	// struct copy of the Connector plus its capability headers suffices).
	pre := *before

	after := *before
	after.Capabilities = make([]Capability, len(before.Capabilities))
	copy(after.Capabilities, before.Capabilities)

	found := false
	for i := range after.Capabilities {
		if after.Capabilities[i].ID != capabilityID {
			continue
		}
		found = true
		cap := after.Capabilities[i] // copy
		newSteps := make([]FlowStep, len(updatedSteps))
		copy(newSteps, updatedSteps)
		cap.Flow.Steps = newSteps
		cap.Flow.Version = cap.Flow.Version + 1
		after.Capabilities[i] = cap
	}
	if !found {
		return nil, fmt.Errorf("gateway selfheal: capability %q not found on connector %q", capabilityID, connectorID)
	}

	// BOUNDED-IMPROVEMENT GUARD: refuse to persist if the heal would have changed
	// anything beyond selector Target/ExpectSignature/Version of a read capability.
	if err := assertHealIsBounded(&pre, &after, capabilityID); err != nil {
		return nil, err
	}

	// Store() fully re-validates the rewritten manifest (engine, flow shape, act
	// risk tiers, no inline secrets). assertHealIsBounded already guaranteed the
	// heal touched nothing but a read capability's selectors, so the manifest is
	// still valid — Store is belt-and-suspenders on top of that.
	if err := reg.Store(after); err != nil {
		return nil, fmt.Errorf("gateway selfheal: persist healed connector %q: %w", connectorID, err)
	}
	return &after, nil
}

// assertHealIsBounded is the in-code enforcement of the docs §8 safety contract.
// It compares the connector BEFORE and AFTER a heal and fails if the heal touched
// anything outside the allowed envelope:
//
//	ALLOWED to change: for the healed capability only — each step's Target and
//	ExpectSignature, and that capability's Flow.Version.
//	FORBIDDEN to change: the set of capabilities, any verb/risk, any auth field,
//	the engine/surface, any OTHER capability, and a step's Action/Text (healing a
//	selector never changes what a step DOES or grants a new action).
//
// If this ever fires, a "heal" was about to mutate a verb/auth/ACT surface — the
// exact thing a self-rewriting system must never do silently. We refuse loudly.
func assertHealIsBounded(before, after *Connector, capabilityID string) error {
	if before == nil || after == nil {
		return fmt.Errorf("gateway selfheal: nil connector in bounded-improvement check")
	}
	// Engine / surface / id are immutable under a heal.
	if before.ID != after.ID || before.Engine != after.Engine || before.Surface != after.Surface {
		return fmt.Errorf("gateway selfheal: heal must not change connector identity/engine/surface")
	}
	// Auth is NEVER touched by a selector heal. ConnectorAuth contains a slice
	// (Scopes) so it isn't directly comparable — compare field-by-field.
	if !connectorAuthEqual(before.Auth, after.Auth) {
		return fmt.Errorf("gateway selfheal: heal must not change auth (auth changes require human confirm)")
	}
	// The capability SET must be identical (no add/remove of capabilities).
	if len(before.Capabilities) != len(after.Capabilities) {
		return fmt.Errorf("gateway selfheal: heal must not add or remove capabilities")
	}
	for i := range before.Capabilities {
		b := before.Capabilities[i]
		a := after.Capabilities[i]
		if b.ID != a.ID {
			return fmt.Errorf("gateway selfheal: heal must not reorder/rename capabilities")
		}
		// Verb / risk / engine-shape of EVERY capability is immutable. A heal may
		// never promote a read into an act, or change risk.
		if b.Verb != a.Verb || b.Risk != a.Risk {
			return fmt.Errorf("gateway selfheal: heal must not change a capability's verb/risk (got %q/%q -> %q/%q)", b.Verb, b.Risk, a.Verb, a.Risk)
		}
		// Only a read capability may be healed at all.
		if a.ID == capabilityID && !isReadVerb(a.Verb) {
			return fmt.Errorf("gateway selfheal: refusing to heal non-read capability %q (verb %q) — only READ flows self-heal", a.ID, a.Verb)
		}
		// The flow's type/launch/api fields are immutable; only Steps + Version may
		// move, and Steps only in Target/ExpectSignature.
		if b.Flow.Type != a.Flow.Type || b.Flow.Method != a.Flow.Method ||
			b.Flow.Path != a.Flow.Path || b.Flow.LaunchPkg != a.Flow.LaunchPkg {
			return fmt.Errorf("gateway selfheal: heal must not change flow engine fields of capability %q", a.ID)
		}
		// A capability OTHER than the one being healed must be byte-identical in its
		// steps (no collateral rewrites).
		if a.ID != capabilityID {
			if !flowStepsSelectorEqual(b.Flow.Steps, a.Flow.Steps, true) {
				return fmt.Errorf("gateway selfheal: heal must not modify capability %q (only %q is being healed)", a.ID, capabilityID)
			}
			if b.Flow.Version != a.Flow.Version {
				return fmt.Errorf("gateway selfheal: heal must not bump version of untouched capability %q", a.ID)
			}
			continue
		}
		// The healed capability: every step's Action + Text must be unchanged; only
		// Target / ExpectSignature may differ. The step COUNT must be unchanged.
		if !flowStepsSelectorEqual(b.Flow.Steps, a.Flow.Steps, false) {
			return fmt.Errorf("gateway selfheal: heal changed a step's Action/Text on capability %q — only Target/ExpectSignature may change", a.ID)
		}
		// Version may only go up (and only by the heal).
		if a.Flow.Version < b.Flow.Version {
			return fmt.Errorf("gateway selfheal: heal must not lower the flow version of capability %q", a.ID)
		}
	}
	return nil
}

// connectorAuthEqual reports whether two ConnectorAuth values are identical. It
// exists because ConnectorAuth holds a slice (Scopes) and so cannot use ==. Used
// only by the bounded-improvement guard to assert a heal never mutated auth.
func connectorAuthEqual(a, b ConnectorAuth) bool {
	if a.Method != b.Method || a.AuthURL != b.AuthURL || a.TokenURL != b.TokenURL ||
		a.ClientID != b.ClientID || a.CredRef != b.CredRef ||
		a.Mechanism != b.Mechanism || a.LoginRef != b.LoginRef ||
		a.TotpRef != b.TotpRef || a.DeviceRef != b.DeviceRef {
		return false
	}
	if len(a.Scopes) != len(b.Scopes) {
		return false
	}
	for i := range a.Scopes {
		if a.Scopes[i] != b.Scopes[i] {
			return false
		}
	}
	return true
}

// flowStepsSelectorEqual compares two step lists. When requireSelectorsEqual is
// true it requires FULL equality (used for capabilities that must be untouched).
// When false it allows Target/ExpectSignature to differ but requires Action + Text
// to match (used for the healed capability — selector-only changes).
func flowStepsSelectorEqual(before, after []FlowStep, requireSelectorsEqual bool) bool {
	if len(before) != len(after) {
		return false
	}
	for i := range before {
		b, a := before[i], after[i]
		if b.Action != a.Action || b.Text != a.Text {
			return false
		}
		if requireSelectorsEqual {
			if b.Target != a.Target || b.ExpectSignature != a.ExpectSignature {
				return false
			}
		}
	}
	return true
}
