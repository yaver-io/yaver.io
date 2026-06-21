package main

// store_projectors.go — project the canonical StoreListing onto each store.
//
// Apple App Store Connect is almost fully API-driven (listing text, privacy,
// age rating, screenshots). Google Play's listing text + images are API, but
// Data Safety + content rating (IARC) are Console-only forms. So a projector
// returns a PushPlan: the actions Yaver can PUSH via API, plus the ones a
// human must submit in the Console (we draft the value + route to the page).
//
// Live push is gated on the user's creds in the vault (ASC .p8 / Play service
// account) — fail-closed like the meter/HCLOUD path: with no creds it's a
// dry-run that shows exactly what WOULD happen. The pure parts (field mapping
// + ASC ES256 JWT minting) are deterministic + unit-tested.

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
)

// PushAction is one field Yaver will set on a store (or route to Console).
type PushAction struct {
	Field        string `json:"field"`
	Automatable  bool   `json:"automatable"`            // true ⇒ API push; false ⇒ Console-only
	Endpoint     string `json:"endpoint,omitempty"`     // ASC/Play API resource (automatable)
	ConsoleRoute string `json:"consoleRoute,omitempty"` // page to open (Console-only)
	Value        string `json:"value"`                  // value pushed, or text to paste
}

// PushPlan is the full per-store projection — the dry-run output.
type PushPlan struct {
	Store            string       `json:"store"`
	Actions          []PushAction `json:"actions"`
	AutomatableCount int          `json:"automatableCount"`
	ConsoleCount     int          `json:"consoleCount"`
}

func (p *PushPlan) add(a PushAction) {
	p.Actions = append(p.Actions, a)
	if a.Automatable {
		p.AutomatableCount++
	} else {
		p.ConsoleCount++
	}
}

func privacySummary(l StoreListing) string {
	if len(l.Privacy) == 0 {
		return "No data collected"
	}
	var cats []string
	for _, d := range l.Privacy {
		cats = append(cats, d.Category)
	}
	return strings.Join(cats, ", ")
}

// buildApplePushPlan maps StoreListing → App Store Connect API actions.
// Nearly everything is API-automatable on Apple.
func buildApplePushPlan(l StoreListing) PushPlan {
	p := PushPlan{Store: "apple"}
	p.add(PushAction{Field: "name", Automatable: true, Endpoint: "appInfoLocalizations", Value: l.AppName})
	p.add(PushAction{Field: "subtitle", Automatable: true, Endpoint: "appStoreVersionLocalizations", Value: l.Subtitle})
	p.add(PushAction{Field: "description", Automatable: true, Endpoint: "appStoreVersionLocalizations", Value: l.Description})
	p.add(PushAction{Field: "keywords", Automatable: true, Endpoint: "appStoreVersionLocalizations", Value: strings.Join(l.Keywords, ",")})
	p.add(PushAction{Field: "whatsNew", Automatable: true, Endpoint: "appStoreVersionLocalizations", Value: l.WhatsNew})
	if l.PrivacyPolicyURL != "" {
		p.add(PushAction{Field: "privacyPolicyUrl", Automatable: true, Endpoint: "appInfoLocalizations", Value: l.PrivacyPolicyURL})
	}
	p.add(PushAction{Field: "appPrivacy", Automatable: true, Endpoint: "appDataUsages", Value: privacySummary(l)})
	p.add(PushAction{Field: "ageRating", Automatable: true, Endpoint: "ageRatingDeclaration", Value: "from questionnaire"})
	p.add(PushAction{Field: "screenshots", Automatable: true, Endpoint: "appScreenshotSets", Value: fmt.Sprintf("%d device classes", countIOSSlots(l))})
	return p
}

// buildGooglePushPlan maps StoreListing → Play Developer API actions, with
// Data Safety + content rating flagged as Console-only.
func buildGooglePushPlan(l StoreListing) PushPlan {
	p := PushPlan{Store: "google"}
	p.add(PushAction{Field: "title", Automatable: true, Endpoint: "edits.listings", Value: l.AppName})
	p.add(PushAction{Field: "shortDescription", Automatable: true, Endpoint: "edits.listings", Value: l.Subtitle})
	p.add(PushAction{Field: "fullDescription", Automatable: true, Endpoint: "edits.listings", Value: l.Description})
	p.add(PushAction{Field: "recentChanges", Automatable: true, Endpoint: "edits.listings", Value: l.WhatsNew})
	p.add(PushAction{Field: "screenshots", Automatable: true, Endpoint: "edits.images", Value: fmt.Sprintf("%d android slots", countAndroidSlots(l))})
	// Console-only on Google:
	p.add(PushAction{
		Field: "dataSafety", Automatable: false,
		ConsoleRoute: "https://play.google.com/console (App content → Data safety)",
		Value:        privacySummary(l),
	})
	p.add(PushAction{
		Field: "contentRating", Automatable: false,
		ConsoleRoute: "https://play.google.com/console (App content → Content rating, IARC)",
		Value:        "complete the IARC questionnaire",
	})
	return p
}

func countIOSSlots(l StoreListing) int {
	n := 0
	for _, s := range l.Screenshots {
		if s.Platform == "ios" && s.MinCount > 0 {
			n++
		}
	}
	return n
}
func countAndroidSlots(l StoreListing) int {
	n := 0
	for _, s := range l.Screenshots {
		if s.Platform == "android" && s.MinCount > 0 {
			n++
		}
	}
	return n
}

// buildPushPlan dispatches by store.
func buildPushPlan(store string, l StoreListing) (PushPlan, error) {
	switch store {
	case "apple", "ios":
		return buildApplePushPlan(l), nil
	case "google", "android", "play":
		return buildGooglePushPlan(l), nil
	default:
		return PushPlan{}, fmt.Errorf("unknown store %q (use apple|google)", store)
	}
}

// ── CLI ──────────────────────────────────────────────────────────────

func runListingPush(args []string) {
	store := ""
	path := "."
	project := ""
	jsonOut := false
	live := false
	apply := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--store":
			if i+1 < len(args) {
				store = args[i+1]
				i++
			}
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--json":
			jsonOut = true
		case "--live":
			live = true
		case "--apply":
			apply = true
		case "-h", "--help":
			fmt.Println("Usage: yaver listing push --store apple|google [--path DIR] [--project P] [--live] [--json]")
			fmt.Println("  default   dry-run: which fields Yaver pushes via API vs Console-only (routed)")
			fmt.Println("  --live    verify your store creds (vault) + connectivity, report readiness")
			fmt.Println("  --apply   (guarded) attempt live writes — not enabled until test-verified")
			return
		}
	}
	if store == "" {
		fmt.Fprintln(os.Stderr, "Error: --store apple|google is required")
		return
	}
	if live || apply {
		runListingPushLive(store, project, path, apply)
		return
	}
	plan, err := buildPushPlan(store, BuildStoreListing(path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	if jsonOut {
		b, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("Push plan for %s — DRY RUN (no store creds applied)\n", plan.Store)
	fmt.Printf("  %d field(s) Yaver pushes via API, %d need the Console:\n\n", plan.AutomatableCount, plan.ConsoleCount)
	for _, a := range plan.Actions {
		if a.Automatable {
			fmt.Printf("  ✓ %-18s → %s\n", a.Field, a.Endpoint)
		} else {
			fmt.Printf("  ◆ %-18s → Console: %s\n", a.Field, a.ConsoleRoute)
		}
	}
	fmt.Println("\n  Live push: add your store creds to the vault, then re-run without --dry-run (coming).")
}

// ── App Store Connect ES256 JWT (pure Go, testable) ──────────────────
//
// ASC auth = a short-lived ES256 JWT signed with the .p8 EC private key.
// header {alg:ES256, kid:<keyId>, typ:JWT}; claims {iss:<issuerId>, iat, exp
// (≤20 min), aud:"appstoreconnect-v1"}. nowUnix is passed in so the result is
// deterministic for tests (no hidden clock).

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// mintASCJWT builds + signs an App Store Connect API token. keyP8PEM is the
// PKCS#8 PEM contents of the AuthKey_*.p8.
func mintASCJWT(keyP8PEM, keyID, issuerID string, nowUnix int64) (string, error) {
	block, _ := pem.Decode([]byte(keyP8PEM))
	if block == nil {
		return "", fmt.Errorf("ASC key: not valid PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("ASC key: parse PKCS#8: %w", err)
	}
	ecKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("ASC key: not an EC key (got %T)", parsed)
	}

	header := map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"}
	claims := map[string]interface{}{
		"iss": issuerID,
		"iat": nowUnix,
		"exp": nowUnix + 1200, // 20 min
		"aud": "appstoreconnect-v1",
	}
	hJSON, _ := json.Marshal(header)
	cJSON, _ := json.Marshal(claims)
	signingInput := b64url(hJSON) + "." + b64url(cJSON)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, ecKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("ASC key: sign: %w", err)
	}
	// JOSE ES256 signature = R||S, each left-padded to 32 bytes (P-256).
	sig := make([]byte, 64)
	r.FillBytes(sig[0:32])
	s.FillBytes(sig[32:64])
	return signingInput + "." + b64url(sig), nil
}

// verifyASCJWTForTest re-checks a token's signature against the public key —
// used only by tests (production never verifies its own token).
func verifyASCJWTForTest(token string, pub *ecdsa.PublicKey) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[0:32])
	s := new(big.Int).SetBytes(sig[32:64])
	return ecdsa.Verify(pub, digest[:], r, s)
}
