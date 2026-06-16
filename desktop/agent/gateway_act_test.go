package main

// gateway_act_test.go — tests for the ACT (write) path (M-G7). Same convention
// as gateway_test.go: a real httptest resource server, an in-memory CredStore,
// and a local audit ledger redirected into t.TempDir() so the suite never
// touches ~/.yaver or the macOS keychain.
//
// Run scoped: go test -run 'TestGatewayAct|TestGatewayIntent|TestRunnerPreflight' -count=1 -vet=off .

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// autoGateNotifier resolves every gate it is notified about with a fixed verdict
// — so awaitHuman doesn't block the test. Mirrors a user tapping approve/deny.
type autoGateNotifier struct {
	store   *gateStore
	approve bool
}

func (n *autoGateNotifier) notifyGate(g *PendingGate) error {
	go func() { _ = n.store.Resolve(g.ID, Resolution{Approved: n.approve, Answer: "approve"}) }()
	return nil
}

func newAutoGate(approve bool) *gateStore {
	gs := newGateStore(nil)
	gs.notifier = &autoGateNotifier{store: gs, approve: approve}
	return gs
}

// redirectAudit points the audit ledger at a temp file for the test.
func redirectAudit(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway-audit.jsonl")
	prev := gatewayAuditPathFn
	gatewayAuditPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { gatewayAuditPathFn = prev })
}

// fakeActAPI accepts a mutating request and records what it saw.
type fakeActAPI struct {
	srv        *httptest.Server
	gotMethod  atomic.Value
	gotIdem    atomic.Value
	gotBody    atomic.Value
	status     atomic.Int32 // 0 => 200
	hits       atomic.Int32
}

func newFakeActAPI() *fakeActAPI {
	f := &fakeActAPI{}
	f.gotMethod.Store("")
	f.gotIdem.Store("")
	f.gotBody.Store("")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.gotMethod.Store(r.Method)
		f.gotIdem.Store(r.Header.Get("Idempotency-Key"))
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
		}
		f.gotBody.Store(string(buf))
		if s := f.status.Load(); s != 0 {
			w.WriteHeader(int(s))
			w.Write([]byte(`{"error":"forced"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"orderId":"o123","status":"confirmed"}`))
	}))
	return f
}

func (f *fakeActAPI) close() { f.srv.Close() }

// buildActConnector returns an api ACT connector pointing at the fake act API.
func buildActConnector(apiSrv, risk string) Connector {
	return Connector{
		ID:      "shop",
		Engine:  "api",
		Surface: apiSrv,
		Auth: ConnectorAuth{
			Method:  "oauth_code",
			CredRef: "gateway/shop/oauth",
		},
		Capabilities: []Capability{{
			ID:    "place_order",
			Verb:  "add",
			Risk:  risk,
			Title: "Place order",
			Flow: CapabilityFlow{
				Type:   "api",
				Method: "POST",
				Path:   "/orders",
				Body:   `{"item":"{item}"}`,
			},
			AnswerSchema: map[string]string{"orderId": "orderId:string?"},
		}},
	}
}

func seedActCreds(t *testing.T, store *memCredStore, ref string) {
	t.Helper()
	if err := saveOAuthCreds(store, ref, &OAuthCreds{
		AccessToken: "act-token",
		ExpiryUnix:  time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("seed creds: %v", err)
	}
}

// TestGatewayActExecuteViaPhoneTap: a low-risk act with no inline confirm blocks
// on the human gate (auto-approved) and then executes; the audit ledger records
// it; the request carried the body + idempotency key.
func TestGatewayActExecuteViaPhoneTap(t *testing.T) {
	redirectAudit(t)
	deps, store, reg := newGatewayTestHarness(t)
	api := newFakeActAPI()
	defer api.close()

	conn := buildActConnector(api.srv.URL, "low")
	seedActCreds(t, store, conn.Auth.CredRef)
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store: %v", err)
	}
	cap, _ := conn.Capability("place_order")

	res, err := deps.gatewayActExecute(context.Background(), &conn, cap,
		map[string]string{"item": "coffee"}, actExecOptions{Gate: newAutoGate(true)})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if res.Outcome != "executed" {
		t.Fatalf("outcome = %q detail=%q", res.Outcome, res.Detail)
	}
	if res.Confirmed != "phone_tap" {
		t.Fatalf("confirmed = %q, want phone_tap", res.Confirmed)
	}
	if res.Answer["orderId"] != "o123" {
		t.Fatalf("answer = %+v", res.Answer)
	}
	if api.gotMethod.Load().(string) != "POST" {
		t.Fatalf("method = %v", api.gotMethod.Load())
	}
	if api.gotBody.Load().(string) != `{"item":"coffee"}` {
		t.Fatalf("body = %q", api.gotBody.Load())
	}
	if api.gotIdem.Load().(string) == "" {
		t.Fatalf("idempotency key missing")
	}

	// Audit ledger has exactly one executed entry for this connector.
	entries, err := listGatewayAudit(0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries: %v len=%d", err, len(entries))
	}
	if entries[0].Outcome != "executed" || entries[0].Connector != "shop" {
		t.Fatalf("audit entry: %+v", entries[0])
	}
}

// TestGatewayActVoiceConfirmsLowRisk: a low-risk act accepts an inline voice
// "yes" without a gate.
func TestGatewayActVoiceConfirmsLowRisk(t *testing.T) {
	redirectAudit(t)
	deps, store, reg := newGatewayTestHarness(t)
	api := newFakeActAPI()
	defer api.close()
	conn := buildActConnector(api.srv.URL, "low")
	seedActCreds(t, store, conn.Auth.CredRef)
	_ = reg.Store(conn)
	cap, _ := conn.Capability("place_order")

	res, err := deps.gatewayActExecute(context.Background(), &conn, cap,
		map[string]string{"item": "tea"}, actExecOptions{VoiceAnswer: "yes"})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if res.Outcome != "executed" || res.Confirmed != "voice" {
		t.Fatalf("outcome=%q confirmed=%q", res.Outcome, res.Confirmed)
	}
}

// TestGatewayActFinancialRefusesVoiceAlone: a financial act IGNORES an inline
// voice "yes" and requires the tapped gate — a denying gate ⇒ declined, proving
// voice-alone can't move money.
func TestGatewayActFinancialRefusesVoiceAlone(t *testing.T) {
	redirectAudit(t)
	deps, store, reg := newGatewayTestHarness(t)
	api := newFakeActAPI()
	defer api.close()
	conn := buildActConnector(api.srv.URL, "financial")
	seedActCreds(t, store, conn.Auth.CredRef)
	_ = reg.Store(conn)
	cap, _ := conn.Capability("place_order")

	// Voice says yes, but the phone gate DENIES → must be declined, no request.
	res, err := deps.gatewayActExecute(context.Background(), &conn, cap,
		map[string]string{"item": "gold"}, actExecOptions{VoiceAnswer: "yes", Gate: newAutoGate(false)})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if res.Outcome != "declined" {
		t.Fatalf("financial outcome = %q, want declined (voice must not bypass tap)", res.Outcome)
	}
	if api.hits.Load() != 0 {
		t.Fatalf("financial act hit the API %d times despite no tap", api.hits.Load())
	}
}

// TestGatewayActPolicyBlock: a funding act against a jurisdiction-blocked source
// is stopped before any network call.
func TestGatewayActPolicyBlock(t *testing.T) {
	redirectAudit(t)
	deps, store, reg := newGatewayTestHarness(t)

	conn := buildActConnector("https://betfair.com", "financial")
	conn.ID = "betfair"
	conn.Auth.CredRef = "gateway/betfair/oauth"
	conn.Capabilities[0].ID = "deposit"
	conn.Capabilities[0].Verb = "deposit"
	seedActCreds(t, store, conn.Auth.CredRef)
	_ = reg.Store(conn)
	cap, _ := conn.Capability("deposit")

	res, err := deps.gatewayActExecute(context.Background(), &conn, cap,
		map[string]string{"item": "x"}, actExecOptions{Gate: newAutoGate(true), Jurisdiction: "TR"})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if res.Outcome != "blocked_policy" {
		t.Fatalf("outcome = %q, want blocked_policy", res.Outcome)
	}
}

// TestGatewayActVelocityCap: with the cap set to 0, the first act is rate-blocked.
func TestGatewayActVelocityCap(t *testing.T) {
	redirectAudit(t)
	deps, store, reg := newGatewayTestHarness(t)
	api := newFakeActAPI()
	defer api.close()
	conn := buildActConnector(api.srv.URL, "low")
	seedActCreds(t, store, conn.Auth.CredRef)
	_ = reg.Store(conn)
	cap, _ := conn.Capability("place_order")

	prev := gatewayActMaxPerHour
	gatewayActMaxPerHour = 0
	defer func() { gatewayActMaxPerHour = prev }()

	res, err := deps.gatewayActExecute(context.Background(), &conn, cap,
		map[string]string{"item": "z"}, actExecOptions{Gate: newAutoGate(true)})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if res.Outcome != "blocked_rate" {
		t.Fatalf("outcome = %q, want blocked_rate", res.Outcome)
	}
	if api.hits.Load() != 0 {
		t.Fatalf("rate-blocked act still hit the API")
	}
}

// TestBuildActPreviewBothEngines: the dry-run preview is computed without any
// network and tiers risk correctly (financial ⇒ tapped key required).
func TestBuildActPreviewBothEngines(t *testing.T) {
	apiConn := buildActConnector("https://api.example", "financial")
	cap, _ := apiConn.Capability("place_order")
	p := buildActPreview(&apiConn, cap, map[string]string{"item": "coffee"}, "")
	if p.Method != "POST" || p.Endpoint != "https://api.example/orders" {
		t.Fatalf("api preview: %+v", p)
	}
	if p.BodyPreview != `{"item":"coffee"}` {
		t.Fatalf("body preview = %q", p.BodyPreview)
	}
	if !p.RequiresTapKey {
		t.Fatalf("financial act must require a tapped key")
	}

	redConn := Connector{
		ID: "carrier", Engine: "redroid", Surface: "com.carrier.app",
		Auth: ConnectorAuth{Method: "password_totp"},
		Capabilities: []Capability{{
			ID: "buy_ticket", Verb: "add", Risk: "low",
			Flow: CapabilityFlow{Type: "redroid", Steps: []FlowStep{
				{Action: "tap", Target: "buy"},
				{Action: "type", Target: "qty", Text: "{qty}"},
			}},
		}},
	}
	rcap, _ := redConn.Capability("buy_ticket")
	rp := buildActPreview(&redConn, rcap, map[string]string{"qty": "2"}, "")
	if len(rp.Steps) != 3 || rp.Steps[0] != "launch com.carrier.app" {
		t.Fatalf("redroid preview steps: %+v", rp.Steps)
	}
	if rp.RequiresTapKey {
		t.Fatalf("low-risk act should not require a tapped key")
	}
}
