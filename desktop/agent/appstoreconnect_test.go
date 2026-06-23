package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testASCCreds returns creds with a freshly generated P-256 key so mintASCJWT
// can actually sign (the httptest server ignores the token's validity).
func testASCCreds(t *testing.T) *ascCreds {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return &ascCreds{KeyPEM: string(p), KeyID: "KID123", IssuerID: "ISS-456"}
}

func TestASCClientTesterLifecycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing bearer on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/apps":
			if got := r.URL.Query().Get("filter[bundleId]"); got != "com.acme.app" {
				t.Errorf("bundle filter = %q", got)
			}
			w.Write([]byte(`{"data":[{"id":"app-1","attributes":{"name":"Acme","bundleId":"com.acme.app","sku":"ACME"}}]}`))
		case r.Method == "GET" && r.URL.Path == "/betaGroups":
			w.Write([]byte(`{"data":[{"id":"grp-internal","attributes":{"name":"Internal","isInternal":true}},{"id":"grp-ext","attributes":{"name":"Beta","isInternal":false}}]}`))
		case r.Method == "POST" && r.URL.Path == "/betaTesters":
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			data := body["data"].(map[string]interface{})
			attrs := data["attributes"].(map[string]interface{})
			if attrs["email"] != "qa@acme.com" {
				t.Errorf("email = %v", attrs["email"])
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"data":{"id":"tester-9","attributes":{"email":"qa@acme.com","state":"INVITED"}}}`))
		case r.Method == "DELETE" && r.URL.Path == "/betaTesters/tester-9":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	old := ascAPIBase
	ascAPIBase = srv.URL
	defer func() { ascAPIBase = old }()

	cl := &ascClient{creds: testASCCreds(t), http: srv.Client()}

	app, err := cl.AppByBundleID("com.acme.app")
	if err != nil {
		t.Fatalf("AppByBundleID: %v", err)
	}
	if app.ID != "app-1" || app.Name != "Acme" {
		t.Fatalf("app = %+v", app)
	}

	gid, err := resolveAppleGroupID(cl, app.ID, "")
	if err != nil {
		t.Fatalf("resolveAppleGroupID: %v", err)
	}
	if gid != "grp-internal" {
		t.Fatalf("default group should be internal, got %q", gid)
	}

	tester, err := cl.InviteBetaTester(gid, "qa@acme.com", "QA", "Bot")
	if err != nil {
		t.Fatalf("InviteBetaTester: %v", err)
	}
	if tester.ID != "tester-9" || tester.State != "INVITED" {
		t.Fatalf("tester = %+v", tester)
	}

	if err := cl.RemoveBetaTester("tester-9"); err != nil {
		t.Fatalf("RemoveBetaTester: %v", err)
	}
}

func TestASCErrorDetail(t *testing.T) {
	body := []byte(`{"errors":[{"title":"Conflict","detail":"A beta tester with this email already exists."}]}`)
	if got := ascErrorDetail(body); got != "A beta tester with this email already exists." {
		t.Fatalf("detail = %q", got)
	}
	if got := ascErrorDetail([]byte("not json")); got != "not json" {
		t.Fatalf("fallback = %q", got)
	}
}
