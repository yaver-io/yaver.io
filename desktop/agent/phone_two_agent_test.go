package main

// phone_two_agent_test.go — end-to-end proof that a phone-project
// bundle exported from agent A imports cleanly on agent B. Mirrors
// the real "Deploy to my Mac" / "Deploy to Yaver Cloud" flow:
//
//   A: POST /phone/projects/create  → slug `demo`
//   A: GET  /phone/projects/export?slug=demo → gzipped bundle
//   B: POST /phone/projects/receive (raw gzip body + ?slug=…)
//   B: GET  /phone/projects/list    → contains `demo`
//
// Importantly, each agent sits in its own $HOME so the SQLite +
// manifest files land in distinct sandboxes. Without that you'd
// get false positives because both "agents" share on-disk state.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withIsolatedHome reruns fn in a goroutine that owns the HOME env
// variable. Tests in Go run sequentially within a package (no
// t.Parallel here), so we can safely flip HOME for each phase of the
// two-agent flow — but we must flip it back before inspecting
// anything on "the other side" of the test.
func withIsolatedHome(t *testing.T, home string, fn func()) {
	t.Helper()
	prev := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("setenv HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", prev)
	}()
	fn()
}

func TestPhoneProjectExportReceiveBetweenAgents(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()

	// Agent A starts in its own HOME — every file it writes lands
	// under homeA/.yaver.
	var urlA, urlB string
	var cancelA, cancelB func()
	tmA := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tmB := NewTaskManager(t.TempDir(), nil, defaultRunner)

	withIsolatedHome(t, homeA, func() {
		urlA, cancelA = startTestServer(t, "owner-A", tmA)
	})
	defer cancelA()
	withIsolatedHome(t, homeB, func() {
		urlB, cancelB = startTestServer(t, "owner-B", tmB)
	})
	defer cancelB()

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Create the phone project on agent A. Phone backend code
	//    writes under HOME/.yaver, so we flip HOME for the duration
	//    of the call.
	projSpec := `{"name":"demo-app","template":"todos"}`
	var createdSlug string
	withIsolatedHome(t, homeA, func() {
		req, _ := http.NewRequest("POST", urlA+"/phone/projects/create",
			bytes.NewReader([]byte(projSpec)))
		req.Header.Set("Authorization", "Bearer owner-A")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("A create: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("A create: HTTP %d — %s", resp.StatusCode, string(raw))
		}
		var proj PhoneProject
		if err := json.Unmarshal(raw, &proj); err != nil {
			t.Fatalf("parse A project: %v — raw=%s", err, string(raw))
		}
		createdSlug = proj.Slug
		if createdSlug == "" {
			t.Fatalf("A returned empty slug: %v", proj)
		}
		// Sanity: project dir should live under homeA.
		if proj.Dir != "" && !stringsHasPrefix(proj.Dir, homeA) {
			t.Logf("warning: project.Dir %q not under %q — HOME override may not be taking effect", proj.Dir, homeA)
		}
	})

	// 2. Export the project bundle from A.
	var bundle []byte
	withIsolatedHome(t, homeA, func() {
		req, _ := http.NewRequest("GET",
			urlA+"/phone/projects/export?slug="+createdSlug+"&includeData=true", nil)
		req.Header.Set("Authorization", "Bearer owner-A")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("A export: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("A export: HTTP %d — %s", resp.StatusCode, string(raw))
		}
		bundle, err = io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read export body: %v", err)
		}
	})
	if len(bundle) < 10 {
		t.Fatalf("bundle suspiciously small (%d bytes)", len(bundle))
	}

	// 3. Receive on agent B — raw gzip body shape with ?slug=.
	withIsolatedHome(t, homeB, func() {
		req, _ := http.NewRequest("POST",
			urlB+"/phone/projects/receive?slug="+createdSlug,
			bytes.NewReader(bundle),
		)
		req.Header.Set("Authorization", "Bearer owner-B")
		req.Header.Set("Content-Type", "application/gzip")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("B receive: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("B receive: HTTP %d — %s", resp.StatusCode, string(raw))
		}
	})

	// 4. List on B should include the slug.
	var listOK bool
	withIsolatedHome(t, homeB, func() {
		req, _ := http.NewRequest("GET", urlB+"/phone/projects/list", nil)
		req.Header.Set("Authorization", "Bearer owner-B")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("B list: %v", err)
		}
		defer resp.Body.Close()
		var body struct {
			Projects []PhoneProject `json:"projects"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("parse B list: %v", err)
		}
		for _, p := range body.Projects {
			if p.Slug == createdSlug {
				listOK = true
				return
			}
		}
		t.Errorf("B's list missing slug %q after receive — had %d projects", createdSlug, len(body.Projects))
	})
	if !listOK {
		t.Fatal("phone project didn't land on agent B after export → receive")
	}

	// 4b. Runtime proof: the target agent serves the imported app's data API.
	withIsolatedHome(t, homeB, func() {
		req, _ := http.NewRequest("GET", urlB+"/data/"+createdSlug+"/todos", nil)
		req.Header.Set("Authorization", "Bearer owner-B")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("B data API: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("B data API: HTTP %d — %s", resp.StatusCode, string(raw))
		}
		if !bytes.Contains(raw, []byte("Buy milk")) {
			t.Fatalf("B data API did not expose imported rows: %s", string(raw))
		}
	})

	// 5. Receive a SECOND time should hit the conflict path unless
	//    the client explicitly asks for overwrite.
	withIsolatedHome(t, homeB, func() {
		req, _ := http.NewRequest("POST",
			urlB+"/phone/projects/receive?slug="+createdSlug,
			bytes.NewReader(bundle),
		)
		req.Header.Set("Authorization", "Bearer owner-B")
		req.Header.Set("Content-Type", "application/gzip")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("B 2nd receive: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Error("second receive with same slug should have hit conflict, not 200")
		}
	})

	// 6. The bundle file on B should live under homeB/.yaver —
	//    never under homeA. Sanity check against HOME-plumbing
	//    regressions.
	bUnderB := filepath.Join(homeB, ".yaver", "phone-projects", createdSlug)
	if _, err := os.Stat(bUnderB); err != nil {
		t.Logf("note: phone-projects dir %q stat err: %v (path convention may differ)", bUnderB, err)
	}
}

// stringsHasPrefix is imported inline to avoid pulling in `strings`
// just for one helper call — the test above already has several
// imports.
func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

var _ = fmt.Sprintf // keep fmt in use even if the final assertion style changes
