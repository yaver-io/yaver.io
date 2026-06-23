package main

// playpublish_api.go — Google Play Android Publisher v3 client for MANAGING
// testing tracks, testers and release rollout on behalf of a third-party dev.
//
// Reuses the Store Studio auth primitives:
//   - googleSA + resolveGoogleSA(project)        (store_push_live.go)
//   - getGoogleAccessToken(sa, nowUnix)          (store_push_apply.go)
// scripts/upload-playstore.py uploads the AAB; this drives the lifecycle after.
//
// Honest scope (Play API reality): the API reads/updates a track's releases and
// the *Google Groups* bound to a testing track, and rolls a draft out to
// testers. It does NOT manage the per-email internal tester list (Console-only).
// So individual-email management is surfaced as guidance; Google-Group binding
// (which IS API-manageable) is the automatable path. See ops_store.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// playAPIBase is overridable in tests to point at an httptest server.
var playAPIBase = "https://androidpublisher.googleapis.com/androidpublisher/v3"

// playClient is bound to one project's service account + Play package.
type playClient struct {
	pkg   string
	token string
	http  *http.Client
}

func newPlayClient(project, pkg string) (*playClient, error) {
	if pkg == "" {
		return nil, fmt.Errorf("packageName required")
	}
	sa, err := resolveGoogleSA(project)
	if err != nil {
		return nil, err
	}
	tok, err := getGoogleAccessToken(sa, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	return &playClient{pkg: pkg, token: tok, http: &http.Client{Timeout: 45 * time.Second}}, nil
}

func (p *playClient) do(method, path string, body interface{}) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		bb, _ := json.Marshal(body)
		rdr = bytes.NewReader(bb)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = playAPIBase + path
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("play %s %s: %d %s", method, path, resp.StatusCode, httpSnippet(out))
	}
	return out, nil
}

// --- edits ---

func (p *playClient) editInsert() (string, error) {
	out, err := p.do("POST", "/applications/"+p.pkg+"/edits", map[string]interface{}{})
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.ID == "" {
		return "", fmt.Errorf("play edit insert: no id")
	}
	return r.ID, nil
}

func (p *playClient) editCommit(editID string) error {
	_, err := p.do("POST", "/applications/"+p.pkg+"/edits/"+editID+":commit", nil)
	return err
}

func (p *playClient) editDelete(editID string) {
	_, _ = p.do("DELETE", "/applications/"+p.pkg+"/edits/"+editID, nil)
}

// --- typed results ---

type PlayRelease struct {
	Name         string   `json:"name,omitempty"`
	VersionCodes []string `json:"versionCodes,omitempty"`
	Status       string   `json:"status,omitempty"` // draft | inProgress | halted | completed
	UserFraction float64  `json:"userFraction,omitempty"`
}

type PlayTrack struct {
	Track    string        `json:"track"`
	Releases []PlayRelease `json:"releases,omitempty"`
}

type PlayTesters struct {
	Track        string   `json:"track,omitempty"`
	GoogleGroups []string `json:"googleGroups,omitempty"`
}

// GetTrack reads a testing track's current releases (read-only edit).
func (p *playClient) GetTrack(track string) (*PlayTrack, error) {
	editID, err := p.editInsert()
	if err != nil {
		return nil, err
	}
	defer p.editDelete(editID)
	out, err := p.do("GET", "/applications/"+p.pkg+"/edits/"+editID+"/tracks/"+track, nil)
	if err != nil {
		return nil, err
	}
	var t PlayTrack
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTesters reads the Google Groups bound to a track for auto-enrollment.
func (p *playClient) GetTesters(track string) (*PlayTesters, error) {
	editID, err := p.editInsert()
	if err != nil {
		return nil, err
	}
	defer p.editDelete(editID)
	out, err := p.do("GET", "/applications/"+p.pkg+"/edits/"+editID+"/testers/"+track, nil)
	if err != nil {
		return nil, err
	}
	var t PlayTesters
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, err
	}
	t.Track = track
	return &t, nil
}

// SetTesters replaces the Google Groups bound to a track (commits the edit).
func (p *playClient) SetTesters(track string, groups []string) (*PlayTesters, error) {
	editID, err := p.editInsert()
	if err != nil {
		return nil, err
	}
	out, err := p.do("PUT", "/applications/"+p.pkg+"/edits/"+editID+"/testers/"+track,
		map[string]interface{}{"googleGroups": groups})
	if err != nil {
		p.editDelete(editID)
		return nil, err
	}
	if err := p.editCommit(editID); err != nil {
		return nil, err
	}
	var t PlayTesters
	_ = json.Unmarshal(out, &t)
	t.Track = track
	t.GoogleGroups = groups
	return &t, nil
}

// PromoteRelease flips the newest release on a track to a new status (e.g.
// draft → completed for an internal track, which delivers it to testers).
// userFraction (0..1) only applies to inProgress staged rollouts.
func (p *playClient) PromoteRelease(track, status string, userFraction float64) (*PlayTrack, error) {
	editID, err := p.editInsert()
	if err != nil {
		return nil, err
	}
	out, err := p.do("GET", "/applications/"+p.pkg+"/edits/"+editID+"/tracks/"+track, nil)
	if err != nil {
		p.editDelete(editID)
		return nil, err
	}
	var t PlayTrack
	if err := json.Unmarshal(out, &t); err != nil {
		p.editDelete(editID)
		return nil, err
	}
	if len(t.Releases) == 0 {
		p.editDelete(editID)
		return nil, fmt.Errorf("no releases on %q track to promote", track)
	}
	t.Releases[0].Status = status
	if status == "inProgress" && userFraction > 0 {
		t.Releases[0].UserFraction = userFraction
	} else {
		t.Releases[0].UserFraction = 0
	}
	upd, err := p.do("PUT", "/applications/"+p.pkg+"/edits/"+editID+"/tracks/"+track, t)
	if err != nil {
		p.editDelete(editID)
		return nil, err
	}
	if err := p.editCommit(editID); err != nil {
		return nil, err
	}
	var res PlayTrack
	_ = json.Unmarshal(upd, &res)
	if res.Track == "" {
		res = t
	}
	return &res, nil
}
