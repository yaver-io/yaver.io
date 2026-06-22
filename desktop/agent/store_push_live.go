package main

// store_push_live.go — the AUTH + connectivity layer for live store push.
//
// SAFETY FIRST: pushing to a real, public store listing is destructive (a bad
// write overwrites the user's live description). So `--live` does only a
// READ-ONLY verify — resolve creds from the vault, acquire auth, confirm the
// store is reachable, and report how many fields are READY. The actual writes
// are guarded behind an explicit `--apply` (and must be exercised against a
// real test account before being trusted). We never blind-write a listing.
//
// Apple auth = the ES256 JWT (store_projectors.go). Google auth = a service
// account → OAuth2 access token via an RS256 JWT grant (built + signed here;
// the grant builder is pure + unit-tested, the token exchange is network).

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ── Apple ASC creds (from vault) ─────────────────────────────────────

type ascCreds struct {
	KeyPEM   string
	KeyID    string
	IssuerID string
}

func resolveAppleASCCreds(project string) (*ascCreds, error) {
	vs, err := openVaultOptional()
	if err != nil || vs == nil {
		return nil, fmt.Errorf("vault unavailable (run `yaver auth`)")
	}
	get := func(name string) string {
		if e, err := vs.Get(project, name); err == nil {
			return e.Value
		}
		if e, err := vs.Get("", name); err == nil { // fall back to global
			return e.Value
		}
		return ""
	}
	keyPath := get("APP_STORE_KEY_PATH")
	keyID := get("APP_STORE_KEY_ID")
	issuer := get("APP_STORE_KEY_ISSUER")
	if keyPath == "" || keyID == "" || issuer == "" {
		return nil, fmt.Errorf("missing ASC creds in vault (APP_STORE_KEY_PATH/_ID/_ISSUER) — see `yaver stores apple-asc-key`")
	}
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read ASC key %s: %w", keyPath, err)
	}
	return &ascCreds{KeyPEM: string(pem), KeyID: keyID, IssuerID: issuer}, nil
}

// verifyAppleAuth mints a JWT and does a read-only GET /v1/apps to confirm the
// creds actually work. Returns the count of apps visible.
func verifyAppleAuth(c *ascCreds, nowUnix int64) (int, error) {
	tok, err := mintASCJWT(c.KeyPEM, c.KeyID, c.IssuerID, nowUnix)
	if err != nil {
		return 0, err
	}
	req, _ := http.NewRequest("GET", "https://api.appstoreconnect.apple.com/v1/apps?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("ASC auth failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(body, &out)
	return len(out.Data), nil
}

// ── Google service account → access token ────────────────────────────

type googleSA struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

func resolveGoogleSA(project string) (*googleSA, error) {
	vs, err := openVaultOptional()
	if err != nil || vs == nil {
		return nil, fmt.Errorf("vault unavailable (run `yaver auth`)")
	}
	path := ""
	if e, err := vs.Get(project, "PLAY_STORE_KEY_FILE"); err == nil {
		path = e.Value
	} else if e, err := vs.Get("", "PLAY_STORE_KEY_FILE"); err == nil {
		path = e.Value
	}
	if path == "" {
		return nil, fmt.Errorf("missing PLAY_STORE_KEY_FILE in vault — see `yaver stores google-service-account`")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service account %s: %w", path, err)
	}
	var sa googleSA
	if err := json.Unmarshal(b, &sa); err != nil {
		return nil, fmt.Errorf("parse service account JSON: %w", err)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("service account JSON missing client_email/private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &sa, nil
}

// buildGoogleJWTGrant builds + signs the RS256 assertion a service account
// exchanges for an access token. Pure + deterministic (nowUnix injected) →
// unit-tested. scope = the Android Publisher API.
func buildGoogleJWTGrant(clientEmail, privateKeyPEM, scope, aud string, nowUnix int64) (string, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("service account key: not valid PEM")
	}
	var rsaKey *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if rsaKey, ok = k.(*rsa.PrivateKey); !ok {
			return "", fmt.Errorf("service account key: not RSA")
		}
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		rsaKey = k
	} else {
		return "", fmt.Errorf("service account key: parse failed")
	}

	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]interface{}{
		"iss":   clientEmail,
		"scope": scope,
		"aud":   aud,
		"iat":   nowUnix,
		"exp":   nowUnix + 3600,
	}
	hJSON, _ := json.Marshal(header)
	cJSON, _ := json.Marshal(claims)
	signingInput := b64url(hJSON) + "." + b64url(cJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign grant: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}

// verifyGoogleAuth exchanges the grant for an access token (network) — proof
// the service account works for the Android Publisher API.
func verifyGoogleAuth(sa *googleSA, nowUnix int64) error {
	grant, err := buildGoogleJWTGrant(sa.ClientEmail, sa.PrivateKey,
		"https://www.googleapis.com/auth/androidpublisher", sa.TokenURI, nowUnix)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", grant)
	resp, err := http.PostForm(sa.TokenURI, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != 200 {
		return fmt.Errorf("Google token exchange failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(body, &out)
	if out.AccessToken == "" {
		return fmt.Errorf("Google token exchange returned no access_token")
	}
	return nil
}

// runListingPushLive is the `--live` path: verify creds + connectivity, report
// readiness, and (with --apply --yes) write the editable draft listing.
func runListingPushLive(store, project, path string, apply, confirmed bool) {
	now := time.Now().Unix()
	listing := BuildStoreListing(path)
	plan, err := buildPushPlan(store, listing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	write := apply && confirmed
	switch store {
	case "apple", "ios":
		c, err := resolveAppleASCCreds(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			return
		}
		n, err := verifyAppleAuth(c, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			return
		}
		fmt.Printf("✓ App Store Connect authenticated (key %s, %d app(s) visible).\n", c.KeyID, n)
		if write {
			fmt.Println("  Writing the editable draft listing (en-US)…")
			if err := applyAppleListing(c, listing, "en-US", now); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ write failed: %v\n", err)
				return
			}
		}
	case "google", "android", "play":
		sa, err := resolveGoogleSA(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			return
		}
		if err := verifyGoogleAuth(sa, now); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			return
		}
		fmt.Printf("✓ Google Play authenticated (%s).\n", sa.ClientEmail)
		if write {
			fmt.Println("  Writing the draft listing (en-US)…")
			if err := applyGoogleListing(sa, listing, "en-US", now); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ write failed: %v\n", err)
				return
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown store %q\n", store)
		return
	}
	fmt.Printf("  %d field(s) pushable via API, %d via the Console.\n", plan.AutomatableCount, plan.ConsoleCount)
	switch {
	case write:
		fmt.Println("  ✓ Draft listing updated. Review in the store console, then submit for review yourself.")
		fmt.Println("  ⚠ Live-write path is UNVERIFIED against a real account in this build — check the result.")
	case apply && !confirmed:
		fmt.Println("  --apply needs --yes to actually write. (It writes only the editable DRAFT, never submits.)")
	default:
		fmt.Println("  Verified auth only. Add --apply --yes to write the draft listing.")
	}
}
