package main

// provisioning_test.go — zero-touch (DPP-style) provisioning, agent side.
//
// Real HTTP servers on random ports, no mocks (per CLAUDE.md test
// conventions). The crypto interop with Convex is exercised by having the
// fake attest server verify the agent's Ed25519 signature exactly the way
// backend/convex/provisioning.ts does — so a divergence in message format,
// encoding, or key derivation fails here before it ships.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSeed(t *testing.T, convexURL string) *ProvisionSeed {
	t.Helper()
	seed, err := GenerateProvisionSeed("talos-edge-v1", "Talos Edge Node", "linux", convexURL)
	if err != nil {
		t.Fatalf("GenerateProvisionSeed: %v", err)
	}
	return seed
}

func TestProvisionSeedRoundTrip(t *testing.T) {
	seed := testSeed(t, "https://example.convex.site")
	path := filepath.Join(t.TempDir(), "yaver-provision.json")
	if err := writeProvisionSeed(path, seed); err != nil {
		t.Fatalf("writeProvisionSeed: %v", err)
	}
	got, err := readProvisionSeed(path)
	if err != nil {
		t.Fatalf("readProvisionSeed: %v", err)
	}
	if got.DeviceID != seed.DeviceID || got.Ed25519Seed != seed.Ed25519Seed || got.ClaimSecret != seed.ClaimSecret {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, seed)
	}
	// A seed missing required fields must be rejected.
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := writeProvisionSeed(bad, &ProvisionSeed{Version: 1, DeviceID: "x"}); err != nil {
		t.Fatalf("write bad seed: %v", err)
	}
	if _, err := readProvisionSeed(bad); err == nil {
		t.Fatal("expected readProvisionSeed to reject a seed with no key/secret")
	}
}

func TestProvisionQRRoundTrip(t *testing.T) {
	seed := testSeed(t, "https://example.convex.site")
	uri, err := seed.ProvisionQRURI()
	if err != nil {
		t.Fatalf("ProvisionQRURI: %v", err)
	}
	if !strings.HasPrefix(uri, "yaver://provision/v1?") {
		t.Fatalf("unexpected QR scheme: %s", uri)
	}
	claim, err := ParseProvisionQR(uri)
	if err != nil {
		t.Fatalf("ParseProvisionQR: %v", err)
	}
	if claim.DeviceID != seed.DeviceID {
		t.Errorf("deviceId: got %q want %q", claim.DeviceID, seed.DeviceID)
	}
	if claim.ClaimSecret != seed.ClaimSecret {
		t.Errorf("claimSecret: got %q want %q", claim.ClaimSecret, seed.ClaimSecret)
	}
	if claim.ProductID != "talos-edge-v1" || claim.Model != "Talos Edge Node" {
		t.Errorf("product/model not carried: %+v", claim)
	}
	// The QR's public key must equal the seed's derived public key.
	wantPub, _ := seed.PublicKeyBase64()
	if claim.PublicKeyB64 != wantPub {
		t.Errorf("public key mismatch:\n got %q\nwant %q", claim.PublicKeyB64, wantPub)
	}
	// Garbage in → error, not panic.
	if _, err := ParseProvisionQR("https://example.com/not-a-provision"); err == nil {
		t.Fatal("expected ParseProvisionQR to reject a non-provision URI")
	}
}

func TestClaimSecretHashMatchesSHA256(t *testing.T) {
	// Must match Convex sha256Hex(claimSecret): lowercase hex of UTF-8.
	secret := "abc-123_XYZ"
	sum := sha256.Sum256([]byte(secret))
	want := hex.EncodeToString(sum[:])
	if got := claimSecretHashHex(secret); got != want {
		t.Fatalf("claimSecretHashHex mismatch:\n got %s\nwant %s", got, want)
	}
}

// fakeAttestServer mimics /devices/provision-attest, verifying the agent's
// signature the SAME way provisioning.ts does. claimed toggles whether the
// owner has claimed yet (awaiting-claim vs active).
func fakeAttestServer(t *testing.T, seed *ProvisionSeed, claimed *bool, issuedToken string) *httptest.Server {
	t.Helper()
	wantPubB64, err := seed.PublicKeyBase64()
	if err != nil {
		t.Fatalf("derive pub: %v", err)
	}
	wantPub, _ := base64.StdEncoding.DecodeString(wantPubB64)
	wantSecretHash := claimSecretHashHex(seed.ClaimSecret)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/provision-attest" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			DeviceID    string `json:"deviceId"`
			ClaimSecret string `json:"claimSecret"`
			TimestampMs int64  `json:"timestampMs"`
			Signature   string `json:"signature"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		// Proof 1: claim secret hash.
		if claimSecretHashHex(body.ClaimSecret) != wantSecretHash {
			writeJSON(w, 401, map[string]string{"status": "bad-secret"})
			return
		}
		// Proof 2: fresh timestamp.
		skew := time.Now().UnixMilli() - body.TimestampMs
		if skew < 0 {
			skew = -skew
		}
		if skew > int64(10*time.Minute/time.Millisecond) {
			writeJSON(w, 401, map[string]string{"status": "stale"})
			return
		}
		// Proof 3: Ed25519 signature over the canonical message.
		sig, derr := base64.StdEncoding.DecodeString(body.Signature)
		if derr != nil {
			writeJSON(w, 401, map[string]string{"status": "bad-signature"})
			return
		}
		msg := []byte(fmt.Sprintf("provision-attest|%s|%d", body.DeviceID, body.TimestampMs))
		if !ed25519.Verify(wantPub, msg, sig) {
			writeJSON(w, 401, map[string]string{"status": "bad-signature"})
			return
		}
		if !*claimed {
			writeJSON(w, 202, map[string]string{"status": "awaiting-claim"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "active", "token": issuedToken})
	}))
}

func TestAttestProvisionSignatureInterop(t *testing.T) {
	claimed := false
	seed := testSeed(t, "")
	srv := fakeAttestServer(t, seed, &claimed, "tok-xyz")
	defer srv.Close()
	seed.ConvexSiteURL = srv.URL

	// Before the owner claims: awaiting-claim.
	res, err := attestProvision(t.Context(), seed)
	if err != nil {
		t.Fatalf("attestProvision (pre-claim): %v", err)
	}
	if res.Status != "awaiting-claim" {
		t.Fatalf("pre-claim status: got %q want awaiting-claim", res.Status)
	}

	// After claim: active + token. This proves the Go-produced signature
	// verifies under the same ed25519 the server (Convex) uses.
	claimed = true
	res, err = attestProvision(t.Context(), seed)
	if err != nil {
		t.Fatalf("attestProvision (post-claim): %v", err)
	}
	if res.Status != "active" || res.Token != "tok-xyz" {
		t.Fatalf("post-claim: got status=%q token=%q want active/tok-xyz", res.Status, res.Token)
	}
}

func TestAttestProvisionRejectsTamperedSecret(t *testing.T) {
	claimed := true
	seed := testSeed(t, "")
	srv := fakeAttestServer(t, seed, &claimed, "tok")
	defer srv.Close()
	seed.ConvexSiteURL = srv.URL

	// Corrupt the claim secret — server must reject (signature still valid,
	// but the secret proof fails), confirming both proofs are independent.
	seed.ClaimSecret = "totally-wrong-secret"
	res, err := attestProvision(t.Context(), seed)
	if err != nil {
		t.Fatalf("attestProvision: %v", err)
	}
	if res.Status != "bad-secret" {
		t.Fatalf("got %q want bad-secret", res.Status)
	}
}

// TestProvisionAttestLoopCompletesPairing is the agent-side e2e: a booting
// box with a seed, an active pairing session, and a server that flips to
// "claimed" mid-flight. The loop must complete the pairing session with
// the issued token — the same handoff manual pairing uses to save+reexec.
func TestProvisionAttestLoopCompletesPairing(t *testing.T) {
	claimed := false
	seed := testSeed(t, "")
	srv := fakeAttestServer(t, seed, &claimed, "session-token-42")
	defer srv.Close()
	seed.ConvexSiteURL = srv.URL

	session, err := StartPairingSession(2 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()

	go runProvisionAttestLoop(t.Context(), seed)

	// Simulate the buyer scanning + claiming shortly after boot.
	time.AfterFunc(150*time.Millisecond, func() { claimed = true })

	select {
	case <-session.done:
		if session.ReceivedToken != "session-token-42" {
			t.Fatalf("pairing completed with wrong token: %q", session.ReceivedToken)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("provision attest loop did not complete the pairing session in time")
	}
}

// TestProvisionedDeviceTableFields_AreNotConvexForbidden pins the
// provisionedDevices + deviceProducts tables (backend/convex/schema.ts) as
// privacy-clean: every persisted field must be a public key / hash / slug /
// id / status / timestamp and must NOT collide with the forbidden-secret
// list. In particular the raw claimSecret and the Ed25519 private seed must
// never be a stored field name — only claimSecretHash + the public key are.
func TestProvisionedDeviceTableFields_AreNotConvexForbidden(t *testing.T) {
	stored := []string{
		// provisionedDevices
		"deviceId", "publicKey", "claimSecretHash", "productId", "model",
		"ownerUserId", "status", "name", "platform",
		"mintedAt", "claimedAt", "activatedAt", "lastAttestAt",
		// deviceProducts
		"vendor", "defaultServices", "createdAt",
	}
	forbidden := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		forbidden[k] = true
	}
	for _, f := range stored {
		if forbidden[f] {
			t.Errorf("provisioned-device field %q is on the Convex forbidden-secret "+
				"list — provisioning rows must stay public-key + hash only", f)
		}
	}
	// Converse: the raw-secret + private-seed spellings MUST be forbidden so
	// nobody ever stores them.
	for _, secret := range []string{"claimSecret", "ed25519Seed", "privateKey"} {
		if !forbidden[secret] {
			t.Errorf("%q must be on the Convex forbidden list — the raw provisioning "+
				"secret / private signing key may never reach Convex", secret)
		}
	}
}
