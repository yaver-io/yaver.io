package main

// gateway_gate.go — the resumable human-gate primitive (M-G3).
//
// A *human gate* is the broker's escape hatch for an irreducible human factor:
// a captcha, a push-approval, an SMS/authenticator code, or a simple confirm.
// The machine NEVER satisfies a human factor itself (no captcha auto-solve, no
// 2FA bypass — see docs §19.1 and the Policy Guard in CLAUDE.md). Instead it:
//
//  1. writes a PendingGate (in-memory, mutex-guarded),
//  2. DELIVERS a notification to the user's OWN phone via the existing
//     device_broadcast_command path (reused, not reinvented),
//  3. BLOCKS the flow until the user resolves it or it times out.
//
// CRITICAL ASSUMPTION (docs §19): the user has ZERO physical access to the
// remote device. So an "interactive" gate (captcha / push-approval) references
// a live remote-view session the user drives from their phone:
//   - web    → /rd/stream + /rd/input  (remotedesktop_http.go)
//   - redroid→ droidFrame + droid input (droid_interactive.go)
// The user solves the challenge live in that window; the flow resumes when they
// resolve the gate. The machine relays input — it does not solve the challenge.
//
// On timeout the flow aborts CLEANLY and a finding is recorded — a human factor
// is never auto-satisfied.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GateKind classifies what the user must do to resolve a gate.
type GateKind string

const (
	// GateSimpleConfirm — the user just confirms ("yes, that was me"). The
	// answer is a yes/no.
	GateSimpleConfirm GateKind = "simple_confirm"
	// GateEnterCode — the user reads a code (SMS, authenticator app) and types
	// it back. The answer is the code string.
	GateEnterCode GateKind = "enter_code"
	// GateInteractive — the user solves a live challenge (captcha, device-trust
	// step) through a remote-view window. ViewRef points at the session.
	GateInteractive GateKind = "interactive"
	// GateApprovePush — the user approves a push prompt that landed on a device
	// (the redroid via remote-view, or their own phone). Answer is yes/no.
	GateApprovePush GateKind = "approve_push"
)

// GateStatus is the lifecycle of a PendingGate.
type GateStatus string

const (
	GatePending  GateStatus = "pending"
	GateResolved GateStatus = "resolved"
	GateExpired  GateStatus = "expired"
	GateAborted  GateStatus = "aborted"
)

// PendingGate is one outstanding human gate. It is generic/open — it carries no
// secret (codes the user types arrive only through the resolve call and are
// handed straight back to the waiting flow, never persisted here).
type PendingGate struct {
	ID            string     `json:"id"`
	ConnectorID   string     `json:"connectorId"`
	Kind          GateKind   `json:"kind"`
	Prompt        string     `json:"prompt"`
	ScreenshotRef string     `json:"screenshotRef,omitempty"`
	ViewRef       string     `json:"viewRef,omitempty"` // remote-view session for interactive kinds
	Options       []string   `json:"options,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	ExpiresAt     time.Time  `json:"expiresAt"`
	Status        GateStatus `json:"status"`
}

// Resolution is what a human supplied (or how the gate ended).
type Resolution struct {
	// Approved is the yes/no for confirm/push gates.
	Approved bool `json:"approved"`
	// Answer is the free-form answer for enter_code / option-pick gates.
	Answer string `json:"answer,omitempty"`
	// Status records how the gate finished (resolved/expired/aborted).
	Status GateStatus `json:"status"`
}

// GateRequest is the input to awaitHuman.
type GateRequest struct {
	ConnectorID   string
	Kind          GateKind
	Prompt        string
	ScreenshotRef string
	ViewRef       string
	Options       []string
	// Timeout caps how long the flow blocks. 0 ⇒ gateDefaultTimeout.
	Timeout time.Duration
}

// gateDefaultTimeout is how long awaitHuman blocks when GateRequest.Timeout is 0.
const gateDefaultTimeout = 3 * time.Minute

// gateNotifier delivers a gate notification to the user's own phone. The real
// impl reuses device_broadcast_command (BlackBoxManager); the test injects a
// double so the gate store is exercisable without a paired device.
type gateNotifier interface {
	notifyGate(g *PendingGate) error
}

// blackboxGateNotifier delivers gate notifications via the existing
// device_broadcast_command path (broadcast to the user's connected phones). It
// pushes a "gateway_human_gate" command the mobile app deep-links on — it does
// NOT reinvent a notification channel.
type blackboxGateNotifier struct {
	mgr *BlackBoxManager
}

func (n *blackboxGateNotifier) notifyGate(g *PendingGate) error {
	if n == nil || n.mgr == nil {
		// No paired phone to notify. The gate is still listable via
		// GET /gateway/gate so the user can resolve it from any surface; a
		// missing push is not fatal to the gate itself.
		return nil
	}
	res := runDeviceBroadcastCommand(n.mgr, deviceBroadcastCommandArgs{
		Command: "gateway_human_gate",
		Data: map[string]interface{}{
			"gateId":      g.ID,
			"connectorId": g.ConnectorID,
			"kind":        string(g.Kind),
			"prompt":      g.Prompt,
			"viewRef":     g.ViewRef,
			"options":     g.Options,
			"deepLink":    "yaver://gateway/gate/" + g.ID,
		},
	})
	if ok, _ := res["ok"].(bool); !ok {
		if msg, _ := res["error"].(string); msg != "" {
			return fmt.Errorf("notify gate: %s", msg)
		}
	}
	return nil
}

// gateWaiter pairs a pending gate with the channel its awaitHuman goroutine is
// blocked on.
type gateWaiter struct {
	gate *PendingGate
	ch   chan Resolution
}

// gateStore is the in-memory, mutex-guarded registry of pending human gates. It
// is the M-G3 primitive: awaitHuman writes + blocks, resolve unblocks.
type gateStore struct {
	mu       sync.Mutex
	waiters  map[string]*gateWaiter
	notifier gateNotifier
	seq      uint64
}

// newGateStore builds a gate store with the given notifier (nil ⇒ no push,
// gates still listable/resolvable).
func newGateStore(notifier gateNotifier) *gateStore {
	return &gateStore{
		waiters:  map[string]*gateWaiter{},
		notifier: notifier,
	}
}

// gatewayGates is the process-wide gate store. It is lazily bound to the running
// agent's BlackBoxManager via bindGatewayGateNotifier so notifications go to the
// user's phone; until then it still works (listable + resolvable), just without
// a push. Package-level so both the HTTP handlers (on HTTPServer) and the
// redroid AuthMethod handler share one store, mirroring ghostStream.
var gatewayGates = newGateStore(nil)

// bindGatewayGateNotifier points the process-wide gate store at the agent's
// BlackBoxManager so awaitHuman can push to the user's phone. Idempotent; safe
// to call from server startup.
func bindGatewayGateNotifier(mgr *BlackBoxManager) {
	gatewayGates.mu.Lock()
	gatewayGates.notifier = &blackboxGateNotifier{mgr: mgr}
	gatewayGates.mu.Unlock()
}

// nextID mints a unique gate id. Caller holds s.mu.
func (s *gateStore) nextID() string {
	s.seq++
	return fmt.Sprintf("gate_%d_%d", time.Now().UnixNano(), s.seq)
}

// awaitHuman writes a PendingGate, delivers it to the user's phone, and BLOCKS
// until the gate is resolved or the timeout fires. It NEVER auto-satisfies the
// human factor: a timeout returns a clean GateExpired resolution (the caller
// records a finding and aborts the flow), not an approval.
func (s *gateStore) awaitHuman(ctx context.Context, req GateRequest) (Resolution, error) {
	if req.Kind == "" {
		return Resolution{}, fmt.Errorf("gateway gate: kind is required")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = gateDefaultTimeout
	}
	now := time.Now()
	g := &PendingGate{
		Kind:          req.Kind,
		ConnectorID:   req.ConnectorID,
		Prompt:        req.Prompt,
		ScreenshotRef: req.ScreenshotRef,
		ViewRef:       req.ViewRef,
		Options:       req.Options,
		CreatedAt:     now,
		ExpiresAt:     now.Add(timeout),
		Status:        GatePending,
	}

	s.mu.Lock()
	g.ID = s.nextID()
	w := &gateWaiter{gate: g, ch: make(chan Resolution, 1)}
	s.waiters[g.ID] = w
	notifier := s.notifier
	s.mu.Unlock()

	// Deliver the notification to the user's own phone (best-effort — the gate
	// is listable regardless). A delivery failure is not fatal.
	if notifier != nil {
		_ = notifier.notifyGate(g)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-w.ch:
		// Resolved by the user (resolve() already removed the waiter + set
		// status on a copy returned to the lister).
		return res, nil
	case <-timer.C:
		s.finish(g.ID, GateExpired)
		return Resolution{Status: GateExpired}, nil
	case <-ctx.Done():
		// The flow was cancelled (e.g. request deadline). Abort cleanly; never
		// treat cancellation as approval.
		s.finish(g.ID, GateAborted)
		return Resolution{Status: GateAborted}, ctx.Err()
	}
}

// finish removes a waiter without delivering a resolution (timeout/abort path)
// and marks its gate's terminal status for any concurrent reader.
func (s *gateStore) finish(id string, status GateStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.waiters[id]; ok {
		w.gate.Status = status
		delete(s.waiters, id)
	}
}

// Resolve delivers a human's answer to the blocked awaitHuman goroutine. It is
// the target of POST /gateway/gate/<id>/resolve. Returns an error if the gate
// is unknown or already resolved.
func (s *gateStore) Resolve(id string, res Resolution) error {
	s.mu.Lock()
	w, ok := s.waiters[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("gate %q not found or already resolved/expired", id)
	}
	delete(s.waiters, id)
	w.gate.Status = GateResolved
	s.mu.Unlock()

	res.Status = GateResolved
	// Buffered channel (cap 1) ⇒ never blocks even if the waiter raced a timeout.
	w.ch <- res
	return nil
}

// List returns a snapshot of all currently pending gates (for GET /gateway/gate).
func (s *gateStore) List() []PendingGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PendingGate, 0, len(s.waiters))
	for _, w := range s.waiters {
		out = append(out, *w.gate)
	}
	return out
}

// ── HTTP routes ──────────────────────────────────────────────────────────────
//
// Registered next to the other gateway/remote-view routes in httpserver.go.

// handleGatewayGateList serves GET /gateway/gate — the pending-gate list.
func (s *HTTPServer) handleGatewayGateList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	gates := gatewayGates.List()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"gates": gates,
		"count": len(gates),
	})
}

// handleGatewayGateResolve serves POST /gateway/gate/{id}/resolve {answer}.
// The body's "answer" is "yes"/"no"/"approve"/"deny"/"true"/"false" for
// confirm/push gates, or the literal code/answer for enter_code/interactive.
func (s *HTTPServer) handleGatewayGateResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	id := gatewayGateIDFromPath(r.URL.Path)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gate id required in path /gateway/gate/{id}/resolve"})
		return
	}
	var body struct {
		Answer string `json:"answer"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	res := Resolution{
		Answer:   body.Answer,
		Approved: gatewayAnswerApproves(body.Answer),
	}
	if err := gatewayGates.Resolve(id, res); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "gateId": id})
}

// gatewayGateIDFromPath extracts {id} from /gateway/gate/{id}/resolve.
func gatewayGateIDFromPath(p string) string {
	const prefix = "/gateway/gate/"
	if !strings.HasPrefix(p, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(p, prefix)
	rest = strings.TrimSuffix(rest, "/resolve")
	rest = strings.Trim(rest, "/")
	if strings.Contains(rest, "/") {
		// e.g. ".../resolve/extra" — reject ambiguous paths.
		return ""
	}
	return rest
}

// gatewayAnswerApproves interprets a free-form answer as a yes/no for
// confirm/push gates. Anything not clearly affirmative is a "no" (fail-closed —
// a human factor is never approved by ambiguity).
func gatewayAnswerApproves(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "yes", "y", "approve", "approved", "ok", "confirm", "true", "1", "allow":
		return true
	default:
		return false
	}
}
