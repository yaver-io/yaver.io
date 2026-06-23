package main

// appstoreconnect.go — App Store Connect API v1 client for MANAGING TestFlight
// beta testers, beta groups and builds on behalf of a third-party developer.
//
// This layers on the auth primitives the Store Studio already ships:
//   - ascCreds{KeyPEM,KeyID,IssuerID} + resolveAppleASCCreds(project)  (store_push_live.go)
//   - mintASCJWT(...)                                                  (store_projectors.go)
// so dev B manages dev B's app with dev B's vault key — from any agent,
// including a managed-cloud box. testflight.go shells to xcrun for *upload*;
// this talks HTTP directly for the tester/build lifecycle the CLI can't reach.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ascAPIBase is overridable in tests so the client can be pointed at an
// httptest server (the repo convention: real HTTP servers, no mocks).
var ascAPIBase = "https://api.appstoreconnect.apple.com/v1"

// ascClient is a stateless ASC API caller bound to one project's credentials.
type ascClient struct {
	creds *ascCreds
	http  *http.Client
}

func newASCClient(project string) (*ascClient, error) {
	creds, err := resolveAppleASCCreds(project)
	if err != nil {
		return nil, err
	}
	return &ascClient{creds: creds, http: &http.Client{Timeout: 45 * time.Second}}, nil
}

func (a *ascClient) do(method, path string, body interface{}) ([]byte, int, error) {
	token, err := mintASCJWT(a.creds.KeyPEM, a.creds.KeyID, a.creds.IssuerID, time.Now().Unix())
	if err != nil {
		return nil, 0, err
	}
	var rdr io.Reader
	if body != nil {
		bb, _ := json.Marshal(body)
		rdr = bytes.NewReader(bb)
	}
	url := path
	if !strings.HasPrefix(path, "http") {
		url = ascAPIBase + path
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return out, resp.StatusCode, fmt.Errorf("app store connect %s %s: %d %s", method, path, resp.StatusCode, ascErrorDetail(out))
	}
	return out, resp.StatusCode, nil
}

// ascErrorDetail pulls the human-readable detail out of an ASC error envelope.
func ascErrorDetail(body []byte) string {
	var e struct {
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) == nil && len(e.Errors) > 0 {
		if e.Errors[0].Detail != "" {
			return e.Errors[0].Detail
		}
		return e.Errors[0].Title
	}
	s := string(body)
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

// --- typed results (trimmed to what the UI/agent needs) ---

type ASCApp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
	SKU      string `json:"sku"`
}

type ASCBetaGroup struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsInternal bool   `json:"isInternal"`
	PublicLink string `json:"publicLink,omitempty"`
}

type ASCBetaTester struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	State     string `json:"state,omitempty"`
}

type ASCBuild struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	UploadedDate    string `json:"uploadedDate,omitempty"`
	ProcessingState string `json:"processingState,omitempty"`
	Expired         bool   `json:"expired"`
}

// AppByBundleID resolves an app record from its bundle id.
func (a *ascClient) AppByBundleID(bundleID string) (*ASCApp, error) {
	out, _, err := a.do("GET", "/apps?filter[bundleId]="+urlQueryEscape(bundleID)+"&limit=1", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes ASCApp `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	if len(r.Data) == 0 {
		return nil, fmt.Errorf("no App Store Connect app for bundle id %q", bundleID)
	}
	app := r.Data[0].Attributes
	app.ID = r.Data[0].ID
	return &app, nil
}

func (a *ascClient) ListBetaGroups(appID string) ([]ASCBetaGroup, error) {
	out, _, err := a.do("GET", "/betaGroups?filter[app]="+appID+"&limit=200", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string       `json:"id"`
			Attributes ASCBetaGroup `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	groups := make([]ASCBetaGroup, 0, len(r.Data))
	for _, d := range r.Data {
		g := d.Attributes
		g.ID = d.ID
		groups = append(groups, g)
	}
	return groups, nil
}

func (a *ascClient) CreateBetaGroup(appID, name string, publicLinkEnabled bool) (*ASCBetaGroup, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "betaGroups",
			"attributes": map[string]interface{}{
				"name":              name,
				"publicLinkEnabled": publicLinkEnabled,
			},
			"relationships": map[string]interface{}{
				"app": map[string]interface{}{
					"data": map[string]string{"type": "apps", "id": appID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/betaGroups", body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string       `json:"id"`
			Attributes ASCBetaGroup `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	g := r.Data.Attributes
	g.ID = r.Data.ID
	return &g, nil
}

func (a *ascClient) ListBetaTesters(appID string) ([]ASCBetaTester, error) {
	out, _, err := a.do("GET", "/betaTesters?filter[apps]="+appID+"&limit=200", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string        `json:"id"`
			Attributes ASCBetaTester `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	testers := make([]ASCBetaTester, 0, len(r.Data))
	for _, d := range r.Data {
		t := d.Attributes
		t.ID = d.ID
		testers = append(testers, t)
	}
	return testers, nil
}

// InviteBetaTester creates a beta tester and adds them to a beta group. Apple
// emails the invite automatically. Returns the created tester.
func (a *ascClient) InviteBetaTester(groupID, email, first, last string) (*ASCBetaTester, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "betaTesters",
			"attributes": map[string]interface{}{
				"email":     email,
				"firstName": first,
				"lastName":  last,
			},
			"relationships": map[string]interface{}{
				"betaGroups": map[string]interface{}{
					"data": []map[string]string{{"type": "betaGroups", "id": groupID}},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/betaTesters", body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string        `json:"id"`
			Attributes ASCBetaTester `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	t := r.Data.Attributes
	t.ID = r.Data.ID
	return &t, nil
}

// RemoveBetaTester deletes a beta tester entirely (revokes access to all apps).
func (a *ascClient) RemoveBetaTester(testerID string) error {
	_, _, err := a.do("DELETE", "/betaTesters/"+testerID, nil)
	return err
}

func (a *ascClient) ListBuilds(appID string) ([]ASCBuild, error) {
	out, _, err := a.do("GET", "/builds?filter[app]="+appID+"&limit=25&sort=-uploadedDate", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string   `json:"id"`
			Attributes ASCBuild `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	builds := make([]ASCBuild, 0, len(r.Data))
	for _, d := range r.Data {
		b := d.Attributes
		b.ID = d.ID
		builds = append(builds, b)
	}
	return builds, nil
}

// AssignBuildToGroup makes a build available to a beta group (the TestFlight
// "add build to group" action — for an external group this starts the group
// seeing the build, after Beta App Review for its first build).
func (a *ascClient) AssignBuildToGroup(groupID, buildID string) error {
	body := map[string]interface{}{
		"data": []map[string]string{{"type": "builds", "id": buildID}},
	}
	_, _, err := a.do("POST", "/betaGroups/"+groupID+"/relationships/builds", body)
	return err
}
