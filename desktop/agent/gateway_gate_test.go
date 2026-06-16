package main

// gateway_gate_test.go — tests for the resumable human-gate primitive (M-G3).
//
// No real device, no keychain. We use an in-memory gate store with a recording
// notifier double and assert: deliver→block→resolve, timeout (clean abort, never
// auto-approve), context-cancel abort, the yes/no answer interpretation, and the
// list/resolve HTTP surface.
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recordingNotifier captures the gates it was asked to deliver so a test can
// assert the user's phone was notified — without a real BlackBoxManager.
type recordingNotifier struct {
	mu    sync.Mutex
	gates []PendingGate
}

func (n *recordingNotifier) notifyGate(g *PendingGate) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gates = append(n.gates, *g)
	return nil
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.gates)
}

func TestGatewayGateDeliverBlockResolve(t *testing.T) {
	notifier := &recordingNotifier{}
	store := newGateStore(notifier)

	type result struct {
		res Resolution
		err error
	}
	done := make(chan result, 1)

	go func() {
		res, err := store.awaitHuman(context.Background(), GateRequest{
			ConnectorID: "example-app",
			Kind:        GateApprovePush,
			Prompt:      "approve the sign-in push",
			ViewRef:     "redroid:example-app",
			Timeout:     2 * time.Second,
		})
		done <- result{res, err}
	}()

	// The gate should become listable + delivered to the phone.
	var gateID string
	deadline := time.After(2 * time.Second)
	for {
		gates := store.List()
		if len(gates) == 1 {
			gateID = gates[0].ID
			if gates[0].Status != GatePending {
				t.Fatalf("gate status = %q, want pending", gates[0].Status)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("gate never appeared in the store")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if notifier.count() != 1 {
		t.Fatalf("notifier delivered %d gates, want 1 (user's phone must be notified)", notifier.count())
	}

	// Resolve as approved → the blocked awaitHuman unblocks.
	if err := store.Resolve(gateID, Resolution{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("awaitHuman err = %v", r.err)
		}
		if r.res.Status != GateResolved || !r.res.Approved {
			t.Fatalf("resolution = %+v, want resolved+approved", r.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitHuman did not return after Resolve")
	}

	// Gate is gone from the pending list after resolution.
	if got := len(store.List()); got != 0 {
		t.Fatalf("pending gates after resolve = %d, want 0", got)
	}
	// Resolving again fails (already removed).
	if err := store.Resolve(gateID, Resolution{Approved: true}); err == nil {
		t.Fatal("second Resolve should fail for an already-resolved gate")
	}
}

func TestGatewayGateTimeoutNeverAutoApproves(t *testing.T) {
	store := newGateStore(&recordingNotifier{})

	start := time.Now()
	res, err := store.awaitHuman(context.Background(), GateRequest{
		ConnectorID: "example-app",
		Kind:        GateApprovePush,
		Prompt:      "approve push",
		Timeout:     120 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("awaitHuman err = %v", err)
	}
	if res.Status != GateExpired {
		t.Fatalf("status = %q, want expired", res.Status)
	}
	// CRITICAL: a timeout must NEVER be an approval — a human factor is never
	// auto-satisfied.
	if res.Approved {
		t.Fatal("timeout returned Approved=true — a human factor was auto-satisfied (forbidden)")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("awaitHuman returned too early (%v) — should block until timeout", elapsed)
	}
	if got := len(store.List()); got != 0 {
		t.Fatalf("expired gate still listed (%d) — should be cleaned up", got)
	}
}

func TestGatewayGateContextCancelAborts(t *testing.T) {
	store := newGateStore(&recordingNotifier{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan Resolution, 1)
	go func() {
		res, _ := store.awaitHuman(ctx, GateRequest{
			ConnectorID: "x", Kind: GateSimpleConfirm, Prompt: "confirm", Timeout: 5 * time.Second,
		})
		done <- res
	}()

	// Wait for the gate to register, then cancel the flow.
	for len(store.List()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case res := <-done:
		if res.Status != GateAborted {
			t.Fatalf("status = %q, want aborted", res.Status)
		}
		if res.Approved {
			t.Fatal("cancelled gate returned Approved=true (forbidden)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitHuman did not abort on context cancel")
	}
}

func TestGatewayGateAnswerInterpretation(t *testing.T) {
	cases := map[string]bool{
		"yes": true, "y": true, "approve": true, "ok": true, "confirm": true,
		"true": true, "1": true, "allow": true, "APPROVE": true, " yes ": true,
		"no": false, "n": false, "deny": false, "": false, "maybe": false, "0": false,
	}
	for in, want := range cases {
		if got := gatewayAnswerApproves(in); got != want {
			t.Errorf("gatewayAnswerApproves(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGatewayGateEnterCodeResolution(t *testing.T) {
	store := newGateStore(&recordingNotifier{})
	done := make(chan Resolution, 1)
	go func() {
		res, _ := store.awaitHuman(context.Background(), GateRequest{
			ConnectorID: "example-app", Kind: GateEnterCode, Prompt: "enter sms code", Timeout: 2 * time.Second,
		})
		done <- res
	}()
	var id string
	for {
		g := store.List()
		if len(g) == 1 {
			id = g[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := store.Resolve(id, Resolution{Answer: "123456"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case res := <-done:
		if res.Status != GateResolved || res.Answer != "123456" {
			t.Fatalf("resolution = %+v, want resolved with answer 123456", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitHuman did not return after enter-code resolve")
	}
}

func TestGatewayGatePathParse(t *testing.T) {
	cases := map[string]string{
		"/gateway/gate/gate_123/resolve": "gate_123",
		"/gateway/gate/abc/resolve":      "abc",
		"/gateway/gate//resolve":         "",
		"/gateway/gate/a/b/resolve":      "",
		"/other/path":                    "",
	}
	for in, want := range cases {
		if got := gatewayGateIDFromPath(in); got != want {
			t.Errorf("gatewayGateIDFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}
