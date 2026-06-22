package main

// store_push_apply.go — the LIVE listing-write choreography (`--apply --yes`).
//
// ⚠️ UNVERIFIED against a live store account in this build. The pure logic
// (attribute/body builders, editable-version selection) is unit-tested; the
// multi-step HTTP round-trip needs a real test account to exercise. Safety:
//   - writes ONLY the editable DRAFT version's text metadata,
//   - NEVER calls submit-for-review,
//   - requires both --apply and --yes,
//   - logs every field written.
//
// Apple: resolve app by bundle id → editable appStoreVersion → its
// localization → PATCH text. Google: edits.insert → listings.update → commit.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ── pure builders (unit-tested) ──────────────────────────────────────

// appleLocalizationAttributes maps the listing → the editable text fields of
// an appStoreVersionLocalization (only non-empty fields are sent).
func appleLocalizationAttributes(l StoreListing) map[string]interface{} {
	a := map[string]interface{}{}
	if l.Description != "" {
		a["description"] = l.Description
	}
	if len(l.Keywords) > 0 {
		a["keywords"] = strings.Join(l.Keywords, ",")
	}
	if l.WhatsNew != "" {
		a["whatsNew"] = l.WhatsNew
	}
	if l.MarketingURL != "" {
		a["marketingUrl"] = l.MarketingURL
	}
	if l.SupportURL != "" {
		a["supportUrl"] = l.SupportURL
	}
	return a
}

// googleListingBody maps the listing → a Play edits.listings resource.
func googleListingBody(l StoreListing, language string) map[string]interface{} {
	b := map[string]interface{}{"language": language}
	if l.AppName != "" {
		b["title"] = l.AppName
	}
	if l.Subtitle != "" {
		b["shortDescription"] = l.Subtitle
	}
	if l.Description != "" {
		b["fullDescription"] = l.Description
	}
	return b
}

// editableAppleVersionStates — states whose metadata can still be edited.
var editableAppleVersionStates = map[string]bool{
	"PREPARE_FOR_SUBMISSION": true,
	"DEVELOPER_REJECTED":     true,
	"REJECTED":               true,
	"METADATA_REJECTED":      true,
	"INVALID_BINARY":         true,
}

type ascVersion struct {
	ID    string
	State string
}

// pickEditableVersion returns the id of the first version in an editable state.
func pickEditableVersion(versions []ascVersion) (string, error) {
	for _, v := range versions {
		if editableAppleVersionStates[v.State] {
			return v.ID, nil
		}
	}
	return "", fmt.Errorf("no editable appStoreVersion (need PREPARE_FOR_SUBMISSION); create a new version first")
}

// ── Apple HTTP choreography (gated) ──────────────────────────────────

func ascGET(token, path string) ([]byte, int, error) {
	req, _ := http.NewRequest("GET", "https://api.appstoreconnect.apple.com"+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b, resp.StatusCode, nil
}

func ascPATCH(token, path string, body map[string]interface{}) (int, string, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("PATCH", "https://api.appstoreconnect.apple.com"+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(b), nil
}

func ascDataIDs(body []byte) []ascVersion {
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				AppStoreState string `json:"appStoreState"`
				Locale        string `json:"locale"`
			} `json:"attributes"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &out)
	vs := make([]ascVersion, 0, len(out.Data))
	for _, d := range out.Data {
		vs = append(vs, ascVersion{ID: d.ID, State: d.Attributes.AppStoreState})
	}
	return vs
}

// applyAppleListing performs the live PATCH. nowUnix for the JWT.
func applyAppleListing(c *ascCreds, l StoreListing, locale string, nowUnix int64) error {
	attrs := appleLocalizationAttributes(l)
	if len(attrs) == 0 {
		return fmt.Errorf("nothing to write (empty listing copy — run `yaver listing draft` first)")
	}
	tok, err := mintASCJWT(c.KeyPEM, c.KeyID, c.IssuerID, nowUnix)
	if err != nil {
		return err
	}
	if l.BundleID == "" {
		return fmt.Errorf("listing has no bundleId")
	}
	// 1) app by bundle id
	b, code, err := ascGET(tok, "/v1/apps?filter[bundleId]="+url.QueryEscape(l.BundleID)+"&limit=1")
	if err != nil || code != 200 {
		return fmt.Errorf("resolve app (HTTP %d): %v %s", code, err, httpSnippet(b))
	}
	apps := ascDataIDs(b)
	if len(apps) == 0 {
		return fmt.Errorf("no app found for bundle id %s on this account", l.BundleID)
	}
	appID := apps[0].ID
	// 2) editable version
	b, code, err = ascGET(tok, "/v1/apps/"+appID+"/appStoreVersions?limit=10")
	if err != nil || code != 200 {
		return fmt.Errorf("list versions (HTTP %d): %v %s", code, err, httpSnippet(b))
	}
	verID, err := pickEditableVersion(ascDataIDs(b))
	if err != nil {
		return err
	}
	// 3) localization for the locale
	b, code, err = ascGET(tok, "/v1/appStoreVersions/"+verID+"/appStoreVersionLocalizations?limit=50")
	if err != nil || code != 200 {
		return fmt.Errorf("list localizations (HTTP %d): %v %s", code, err, httpSnippet(b))
	}
	locID := pickLocalization(b, locale)
	if locID == "" {
		return fmt.Errorf("no %s localization on the editable version", locale)
	}
	// 4) PATCH the text
	patch := map[string]interface{}{"data": map[string]interface{}{
		"type": "appStoreVersionLocalizations", "id": locID, "attributes": attrs,
	}}
	code, resp, err := ascPATCH(tok, "/v1/appStoreVersionLocalizations/"+locID, patch)
	if err != nil || code/100 != 2 {
		return fmt.Errorf("PATCH localization (HTTP %d): %v %s", code, err, httpSnippet([]byte(resp)))
	}
	for k := range attrs {
		fmt.Printf("    ✓ wrote %s\n", k)
	}
	return nil
}

func pickLocalization(body []byte, locale string) string {
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Locale string `json:"locale"`
			} `json:"attributes"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &out)
	var first string
	for _, d := range out.Data {
		if first == "" {
			first = d.ID
		}
		if strings.EqualFold(d.Attributes.Locale, locale) {
			return d.ID
		}
	}
	return first // fall back to the first available locale
}

// ── Google HTTP choreography (gated) ─────────────────────────────────

func getGoogleAccessToken(sa *googleSA, nowUnix int64) (string, error) {
	grant, err := buildGoogleJWTGrant(sa.ClientEmail, sa.PrivateKey,
		"https://www.googleapis.com/auth/androidpublisher", sa.TokenURI, nowUnix)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", grant)
	resp, err := http.PostForm(sa.TokenURI, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token exchange HTTP %d: %s", resp.StatusCode, httpSnippet(b))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(b, &out)
	if out.AccessToken == "" {
		return "", fmt.Errorf("no access_token")
	}
	return out.AccessToken, nil
}

func applyGoogleListing(sa *googleSA, l StoreListing, language string, nowUnix int64) error {
	if l.PackageName == "" {
		return fmt.Errorf("listing has no packageName")
	}
	tok, err := getGoogleAccessToken(sa, nowUnix)
	if err != nil {
		return err
	}
	base := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/" + url.PathEscape(l.PackageName)
	gv := func(method, path string, body interface{}) (int, []byte, error) {
		var rdr io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rdr = bytes.NewReader(raw)
		}
		req, _ := http.NewRequest(method, base+path, rdr)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, b, nil
	}
	// 1) insert edit
	code, b, err := gv("POST", "/edits", nil)
	if err != nil || code/100 != 2 {
		return fmt.Errorf("edits.insert (HTTP %d): %v %s", code, err, httpSnippet(b))
	}
	var edit struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &edit)
	if edit.ID == "" {
		return fmt.Errorf("no edit id returned")
	}
	// 2) update listing
	code, b, err = gv("PUT", "/edits/"+edit.ID+"/listings/"+url.PathEscape(language), googleListingBody(l, language))
	if err != nil || code/100 != 2 {
		return fmt.Errorf("listings.update (HTTP %d): %v %s", code, err, httpSnippet(b))
	}
	// 3) commit
	code, b, err = gv("POST", "/edits/"+edit.ID+":commit", nil)
	if err != nil || code/100 != 2 {
		return fmt.Errorf("edits.commit (HTTP %d): %v %s", code, err, httpSnippet(b))
	}
	fmt.Printf("    ✓ committed listing (%s): title/shortDescription/fullDescription\n", language)
	return nil
}

func httpSnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
