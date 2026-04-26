package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectRemoteFromURL(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		wantOK   bool
		wantProv string
		wantHost string
		wantRepo string
	}{
		{
			name: "github https",
			url:  "https://github.com/owner/repo.git",
			wantOK: true, wantProv: "github", wantHost: "github.com", wantRepo: "owner/repo",
		},
		{
			name: "github ssh",
			url:  "git@github.com:owner/repo.git",
			wantOK: true, wantProv: "github", wantHost: "github.com", wantRepo: "owner/repo",
		},
		{
			name: "gitlab.com https",
			url:  "https://gitlab.com/group/project.git",
			wantOK: true, wantProv: "gitlab", wantHost: "gitlab.com", wantRepo: "group/project",
		},
		{
			name: "self-hosted gitlab by hostname keyword",
			url:  "https://gitlab.example.com/team/app.git",
			wantOK: true, wantProv: "gitlab", wantHost: "gitlab.example.com", wantRepo: "team/app",
		},
		{
			name: "garbage",
			url:  "not a url",
			wantOK: false,
		},
		{
			name: "bitbucket — unsupported by registry mapper",
			url:  "https://bitbucket.org/owner/repo.git",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := projectRemoteFromURL(tc.url)
			if (got.Provider != "") != tc.wantOK {
				t.Fatalf("got provider=%q wantOK=%v", got.Provider, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.Provider != tc.wantProv {
				t.Errorf("provider=%q want=%q", got.Provider, tc.wantProv)
			}
			if got.Host != tc.wantHost {
				t.Errorf("host=%q want=%q", got.Host, tc.wantHost)
			}
			if got.Repo != tc.wantRepo {
				t.Errorf("repo=%q want=%q", got.Repo, tc.wantRepo)
			}
			if got.RemoteURL != strings.TrimSpace(tc.url) {
				t.Errorf("remoteUrl roundtrip lost: %q", got.RemoteURL)
			}
		})
	}
}

func TestUpsertAndFindAndDeleteProjectRemote(t *testing.T) {
	func() {
		_ = withTempHome(t)
		// First write
		if err := upsertProjectRemote(ProjectRemote{
			Name:      "carrotbet",
			RemoteURL: "https://github.com/kivanccakmak/carrotbet.git",
			Provider:  "github",
			Host:      "github.com",
			Repo:      "kivanccakmak/carrotbet",
			SetAt:     "2026-04-26T00:00:00Z",
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		// Lookup is case-insensitive
		got := findProjectRemote("CarrotBet")
		if got == nil {
			t.Fatal("findProjectRemote returned nil after upsert")
		}
		if got.Repo != "kivanccakmak/carrotbet" {
			t.Errorf("repo=%q", got.Repo)
		}

		// Replace existing (host changes)
		if err := upsertProjectRemote(ProjectRemote{
			Name:      "carrotbet",
			RemoteURL: "https://gitlab.com/foo/carrotbet.git",
			Provider:  "gitlab",
			Host:      "gitlab.com",
			Repo:      "foo/carrotbet",
			SetAt:     "2026-04-26T01:00:00Z",
		}); err != nil {
			t.Fatalf("upsert (replace): %v", err)
		}

		entries, err := loadProjectRemotes()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries=%d want=1 (upsert should replace, not duplicate)", len(entries))
		}
		if entries[0].Provider != "gitlab" || entries[0].Repo != "foo/carrotbet" {
			t.Errorf("replace lost data: %+v", entries[0])
		}

		// Delete
		if err := deleteProjectRemote("carrotbet"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if findProjectRemote("carrotbet") != nil {
			t.Error("entry still present after delete")
		}
	}()
}

func TestHandleProjectRemoteSetRejectsBadURL(t *testing.T) {
	func() {
		_ = withTempHome(t)
		s := &HTTPServer{}
		body := strings.NewReader(`{"projectName":"carrotbet","remoteUrl":"https://example.com/just/a/site"}`)
		req := httptest.NewRequest("POST", "/vibing/project/remote", body)
		w := httptest.NewRecorder()
		s.handleProjectRemote(w, req)

		if w.Code != 400 {
			t.Fatalf("status=%d body=%s want=400", w.Code, w.Body.String())
		}
	}()
}

func TestHandleProjectRemoteSetAndGetRoundtrip(t *testing.T) {
	func() {
		_ = withTempHome(t)
		s := &HTTPServer{}

		// POST
		body := strings.NewReader(`{"projectName":"carrotbet","remoteUrl":"https://github.com/kivanccakmak/carrotbet.git"}`)
		req := httptest.NewRequest("POST", "/vibing/project/remote", body)
		w := httptest.NewRecorder()
		s.handleProjectRemote(w, req)
		if w.Code != 200 {
			t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
		}

		// GET single
		req = httptest.NewRequest("GET", "/vibing/project/remote?projectName=carrotbet", nil)
		w = httptest.NewRecorder()
		s.handleProjectRemote(w, req)
		if w.Code != 200 {
			t.Fatalf("GET single status=%d body=%s", w.Code, w.Body.String())
		}
		var got struct {
			Found   bool          `json:"found"`
			Project ProjectRemote `json:"project"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !got.Found {
			t.Fatal("found=false after POST")
		}
		if got.Project.Provider != "github" {
			t.Errorf("provider=%q", got.Project.Provider)
		}

		// GET list
		req = httptest.NewRequest("GET", "/vibing/project/remote", nil)
		w = httptest.NewRecorder()
		s.handleProjectRemote(w, req)
		if w.Code != 200 {
			t.Fatalf("GET list status=%d body=%s", w.Code, w.Body.String())
		}
		var list struct {
			Projects []ProjectRemote `json:"projects"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(list.Projects) != 1 {
			t.Fatalf("list len=%d want=1", len(list.Projects))
		}

		// DELETE
		req = httptest.NewRequest("DELETE", "/vibing/project/remote?projectName=carrotbet", nil)
		w = httptest.NewRecorder()
		s.handleProjectRemote(w, req)
		if w.Code != 200 {
			t.Fatalf("DELETE status=%d body=%s", w.Code, w.Body.String())
		}

		// GET single after delete → found=false
		req = httptest.NewRequest("GET", "/vibing/project/remote?projectName=carrotbet", nil)
		w = httptest.NewRecorder()
		s.handleProjectRemote(w, req)
		var after struct {
			Found bool `json:"found"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &after)
		if after.Found {
			t.Error("entry still found after delete")
		}
	}()
}
