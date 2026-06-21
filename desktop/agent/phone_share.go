package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PhoneShare is a short join code for a phone-sandbox project so a friend can
// pull + Hermes-load it against the host's Yaver Serverless Lite data API. It
// is the friends-preview half of the normie loop: "I built it, my friends try
// it", before TestFlight or app-store distribution.
//
// P2P only: the record lives on this agent under
// ~/.yaver/phone-projects/_shares/, never in Convex (privacy contract —
// app/project data flows device↔device, not through central Convex).
type PhoneShare struct {
	Code string `json:"code"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Runtime and dataUrl tell the friend's Hermes-loaded copy which backend
	// adapter to use. Keep hostedConvexUrl only as a legacy optional field for
	// older clients that have not yet moved to Yaver Serverless Lite.
	Runtime         string `json:"runtime,omitempty"`
	DataURL         string `json:"dataUrl,omitempty"`
	HostedConvexURL string `json:"hostedConvexUrl,omitempty"`
	// Relative path the friend fetches the bundle from (the .zip twin,
	// data included so the preview is populated).
	BundleURL string `json:"bundleUrl"`
	// DataToken is a scoped READ-ONLY pp_ token bound to this project, minted
	// at share time. The friend uses it as the Bearer against DataURL so they
	// can read the app's live data WITHOUT the owner's session and WITHOUT
	// write access. This is what makes "friends open & use the app" work for
	// accounts other than the owner's.
	DataToken string `json:"dataToken,omitempty"`
	// Schema + App let a friend's client render the actual app (screens + table
	// columns) without any owner-scoped fetch — everything needed to RUN the
	// app against DataURL is in the join payload.
	Schema    *PhoneSchema  `json:"schema,omitempty"`
	App       *PhoneAppSpec `json:"app,omitempty"`
	CreatedAt string        `json:"createdAt"`
	ExpiresAt string        `json:"expiresAt"`
}

// ErrPhoneShareNotFound is returned for an unknown or expired code.
var ErrPhoneShareNotFound = errors.New("share code not found or expired")

func phoneSharesDir() (string, error) {
	root, err := PhoneProjectsRoot()
	if err != nil {
		return "", err
	}
	d := filepath.Join(root, "_shares")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

func normalizeShareCode(c string) string {
	return strings.ToUpper(strings.TrimSpace(c))
}

func phoneShareBundleURL(slug string) string {
	// .zip twin + live data so a friend's preview isn't an empty shell.
	return fmt.Sprintf("/phone/projects/export?slug=%s&format=zip&includeData=1", slug)
}

// CreatePhoneShare mints a join code for an existing project. Default TTL 24h.
// The share is placement-neutral: the friend uses the host agent's origin plus
// dataUrl, whether the host is a laptop, self-hosted box, or Yaver managed
// cloud.
func CreatePhoneShare(slug string, ttl time.Duration) (*PhoneShare, error) {
	p, err := LoadPhoneProject(slug)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	dir, err := phoneSharesDir()
	if err != nil {
		return nil, err
	}
	// Mint a scoped read-only data token so the friend can read /data without
	// the owner's session and without write access. Best-effort: if minting
	// fails the share still works for owner-side previews, just without a
	// friend data credential.
	var dataToken string
	if mint, mErr := MintPhoneProjectTokenScoped(p.Slug, "friend-preview (read-only)", true); mErr == nil {
		dataToken = mint.Raw
	}
	now := time.Now()
	sh := &PhoneShare{
		Code:      generatePairCode(), // shared alphabet (no 0/O/1/I)
		Slug:      p.Slug,
		Name:      p.Name,
		Runtime:   "yaver-serverless-lite",
		DataURL:   "/data/" + p.Slug,
		BundleURL: phoneShareBundleURL(p.Slug),
		DataToken: dataToken,
		Schema:    p.Schema,
		App:       p.App,
		CreatedAt: now.UTC().Format(time.RFC3339),
		ExpiresAt: now.Add(ttl).UTC().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(sh, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, sh.Code+".json"), b, 0o600); err != nil {
		return nil, err
	}
	return sh, nil
}

// ResolvePhoneShare looks up a code, dropping it if expired.
func ResolvePhoneShare(code string) (*PhoneShare, error) {
	code = normalizeShareCode(code)
	if code == "" {
		return nil, ErrPhoneShareNotFound
	}
	dir, err := phoneSharesDir()
	if err != nil {
		return nil, err
	}
	f := filepath.Join(dir, code+".json")
	b, err := os.ReadFile(f)
	if err != nil {
		return nil, ErrPhoneShareNotFound
	}
	var sh PhoneShare
	if json.Unmarshal(b, &sh) != nil {
		return nil, ErrPhoneShareNotFound
	}
	if exp, e := time.Parse(time.RFC3339, sh.ExpiresAt); e == nil && time.Now().After(exp) {
		_ = os.Remove(f) // self-clean expired codes
		return nil, ErrPhoneShareNotFound
	}
	return &sh, nil
}
