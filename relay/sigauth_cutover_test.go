package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The password cutover is the moment the relay stops accepting the shared
// password. Getting it wrong locks the fleet out. These pin the signal it is
// decided from.

// A signature failure must be COUNTED, not silently swallowed by the password
// fallback. This is the bug: authViaPassword cannot distinguish "never migrated"
// from "migrated but failing", so /authmix could read 100% migrated while a
// chunk of the fleet was failing signature auth on every request.
func TestSigFailuresAreAttributable(t *testing.T) {
	s := NewRelayServer(0, 0, "pw", "", "")
	s.adminToken = "admintok"

	s.noteSigFail(sigFailBadSignature)
	s.noteSigFail(sigFailBadSignature)
	s.noteSigFail(sigFailUnresolved)

	rec := httptest.NewRecorder()
	s.handleAuthMix(rec, adminReq(s))

	if rec.Code != http.StatusOK {
		t.Fatalf("authmix status = %d, want 200 (admin gate)", rec.Code)
	}
	var out struct {
		SigFailures     uint64            `json:"sigFailures"`
		SigFailByReason map[string]uint64 `json:"sigFailByReason"`
		SafeToCutOver   bool              `json:"safeToCutOver"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if out.SigFailures != 3 {
		t.Errorf("sigFailures = %d, want 3", out.SigFailures)
	}
	if out.SigFailByReason["bad_signature"] != 2 {
		t.Errorf("bad_signature = %d, want 2", out.SigFailByReason["bad_signature"])
	}
	if out.SigFailByReason["unresolved_signer"] != 1 {
		t.Errorf("unresolved_signer = %d, want 1", out.SigFailByReason["unresolved_signer"])
	}
	if out.SafeToCutOver {
		t.Error("safeToCutOver = true while signatures are FAILING — this is the lockout bug")
	}
}

// The trap: 100% of auths report as signature-based, but signatures are also
// failing underneath. The old sigPercent==100 reading would have said "go".
func TestCutoverUnsafeWhenSigsFailEvenAtFullSigPercent(t *testing.T) {
	s := NewRelayServer(0, 0, "pw", "", "")
	s.adminToken = "admintok"
	s.authViaSig.Add(100) // every *successful* auth used a signature
	s.noteSigFail(sigFailBadSignature)

	rec := httptest.NewRecorder()
	s.handleAuthMix(rec, adminReq(s))

	var out struct {
		SigPercent    float64 `json:"sigPercent"`
		SafeToCutOver bool    `json:"safeToCutOver"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	if out.SigPercent != 100 {
		t.Fatalf("sigPercent = %v, want 100 (setup)", out.SigPercent)
	}
	if out.SafeToCutOver {
		t.Error("sigPercent==100 must NOT imply safe: failing signatures are masked by the password fallback")
	}
}

// Clean fleet: all signature, zero failures, zero password ⇒ safe.
func TestCutoverSafeOnlyWhenCleanlyMigrated(t *testing.T) {
	s := NewRelayServer(0, 0, "pw", "", "")
	s.adminToken = "admintok"
	s.authViaSig.Add(50)

	rec := httptest.NewRecorder()
	s.handleAuthMix(rec, adminReq(s))

	var out struct {
		SafeToCutOver bool `json:"safeToCutOver"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.SafeToCutOver {
		t.Error("all-signature, zero-failure, zero-password fleet should be safe to cut over")
	}
}

// A fleet that has never signed anything is not "safe" merely because nothing failed.
func TestCutoverUnsafeWithNoSignatureTrafficAtAll(t *testing.T) {
	s := NewRelayServer(0, 0, "pw", "", "")
	s.adminToken = "admintok"
	rec := httptest.NewRecorder()
	s.handleAuthMix(rec, adminReq(s))
	var out struct {
		SafeToCutOver bool `json:"safeToCutOver"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.SafeToCutOver {
		t.Error("zero signature traffic must not read as safe to cut over")
	}
}

// adminReq builds an admin-authenticated /authmix request. The endpoint is
// fail-closed: with no admin token configured it 401s, which is correct — the
// first draft of this test assumed the opposite and was wrong, not the code.
func adminReq(s *RelayServer) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/authmix", nil)
	r.Header.Set("Authorization", "Bearer "+s.adminToken)
	return r
}
