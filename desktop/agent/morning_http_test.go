package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runMorningRepo initializes a tiny git repo with two commits so we
// can test rollback against real SHAs.
func runMorningRepo(t *testing.T) (repo, base, head string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	run("git", "init", "-b", "main")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\n"), 0o644)
	run("git", "add", "f.txt")
	run("git", "commit", "-m", "base")
	base = GitHeadSHA(repo)
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\ntwo\n"), 0o644)
	run("git", "add", "-A")
	run("git", "commit", "-m", "add two")
	head = GitHeadSHA(repo)
	return
}

// primeMorningSummary writes a one-task summary into the server's
// morning store (creating the store eagerly) and returns runID/taskID.
func primeMorningSummary(t *testing.T, s *HTTPServer, workDir, base, head string) (string, string) {
	t.Helper()
	s.morningStore() // force init
	task := TaskHighlight{
		TaskID:     "t1",
		Title:      "Add two",
		Status:     TaskStatusHighlightShipped,
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC().Add(1 * time.Minute),
		BaseSHA:    base,
		HeadSHA:    head,
		CommitSHAs: GitCommitSHAsBetween(workDir, base, head),
		WorkDir:    workDir,
	}
	if _, err := s.morningStore().UpsertTask("run-a", "tproj", workDir, task); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return "run-a", "t1"
}

func TestMorningHTTPListAndGet(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	defer tm.Shutdown()
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()
	// Rebind morning store to an isolated tmp dir so this test doesn't
	// see runs from the user's real ~/.yaver.
	s := currentTestHTTPServer
	s.morningStoreRef = NewMorningStore(t.TempDir())
	s.recordingMgrRef = NewRecordingManager(t.TempDir())

	repo, base, head := runMorningRepo(t)
	primeMorningSummary(t, s, repo, base, head)

	// List
	code, body := doRequest(t, http.MethodGet, baseURL+"/morning/runs", "tok", "")
	if code != 200 {
		t.Fatalf("list status = %d: %+v", code, body)
	}
	runs, _ := body["runs"].([]interface{})
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	// Get single run
	code, body = doRequest(t, http.MethodGet, baseURL+"/morning/runs/run-a", "tok", "")
	if code != 200 {
		t.Fatalf("get status = %d: %+v", code, body)
	}
	run, _ := body["run"].(map[string]interface{})
	if run["runId"] != "run-a" {
		t.Fatalf("wrong run: %+v", run)
	}
}

func TestMorningHTTPRollbackRunsGitRevert(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	defer tm.Shutdown()
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()
	s := currentTestHTTPServer
	s.morningStoreRef = NewMorningStore(t.TempDir())

	repo, base, head := runMorningRepo(t)
	runID, taskID := primeMorningSummary(t, s, repo, base, head)

	// Rollback
	code, body := doRequest(t, http.MethodPost,
		fmt.Sprintf("%s/morning/runs/%s/tasks/%s/rollback", baseURL, runID, taskID),
		"tok", "")
	if code != 200 {
		t.Fatalf("rollback status = %d: %+v", code, body)
	}
	revertSHA, _ := body["revertSha"].(string)
	if revertSHA == "" || revertSHA == head {
		t.Fatalf("revertSha missing or equal to head: %q", revertSHA)
	}
	// Repo HEAD should now have a new revert commit, and f.txt should
	// be back to "one\n".
	content, _ := os.ReadFile(filepath.Join(repo, "f.txt"))
	if strings.TrimSpace(string(content)) != "one" {
		t.Fatalf("revert didn't restore file: %q", string(content))
	}
	// Second rollback of the same task must fail gracefully (409).
	code, _ = doRequest(t, http.MethodPost,
		fmt.Sprintf("%s/morning/runs/%s/tasks/%s/rollback", baseURL, runID, taskID),
		"tok", "")
	if code != http.StatusConflict {
		t.Fatalf("second rollback status = %d, want 409", code)
	}
}

func TestMorningHTTPRequiresAuth(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	defer tm.Shutdown()
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Auth middleware returns 401 or 403 depending on whether it
	// decides the caller is unknown vs. known-but-rejected. Both
	// block — we only care that the morning endpoint refuses access
	// to a caller without a valid bearer.
	code, _ := doRequest(t, http.MethodGet, baseURL+"/morning/runs", "wrong-token", "")
	if code == http.StatusOK {
		t.Fatalf("wrong-token got 200 — auth was bypassed")
	}
	if code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("expected 401 or 403, got %d", code)
	}
}

func TestRecordingHTTPServesByteRange(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	defer tm.Shutdown()
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()
	s := currentTestHTTPServer
	recDir := t.TempDir()
	s.recordingMgrRef = NewRecordingManager(recDir)

	// Plant a mock mp4 on disk.
	mp4Dir := filepath.Join(recDir, "run-a", "t1")
	if err := os.MkdirAll(mp4Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("MOCKMP4_abcdefghijklmnopqrstuvwxyz0123456789ABCDEF")
	if err := os.WriteFile(filepath.Join(mp4Dir, "video.mp4"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	// Full GET
	code, headers, full := doRequestRaw(t, http.MethodGet,
		baseURL+"/recordings/run-a/t1/video.mp4", "tok", "", nil)
	if code != 200 {
		t.Fatalf("full GET status = %d", code)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "video/mp4") {
		t.Fatalf("content-type = %q", ct)
	}
	if len(full) != len(payload) {
		t.Fatalf("body len = %d, want %d", len(full), len(payload))
	}
	if headers.Get("Accept-Ranges") != "bytes" {
		t.Errorf("expected Accept-Ranges: bytes")
	}

	// Range GET
	code, headers, partial := doRequestRaw(t, http.MethodGet,
		baseURL+"/recordings/run-a/t1/video.mp4", "tok", "",
		map[string]string{"Range": "bytes=0-9"})
	if code != http.StatusPartialContent {
		t.Fatalf("range GET status = %d, want 206", code)
	}
	if len(partial) != 10 {
		t.Fatalf("range body len = %d, want 10", len(partial))
	}
	if string(partial) != "MOCKMP4_ab" {
		t.Fatalf("range body = %q", string(partial))
	}
	if cr := headers.Get("Content-Range"); !strings.HasPrefix(cr, "bytes 0-9/") {
		t.Errorf("content-range = %q", cr)
	}
}

func TestRecordingHTTPRejectsPathEscape(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	defer tm.Shutdown()
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()
	s := currentTestHTTPServer
	s.recordingMgrRef = NewRecordingManager(t.TempDir())

	// /recordings/../../etc/... should bounce.
	code, _ := doRequest(t, http.MethodGet, baseURL+"/recordings/..%2F..%2Fetc/x/video.mp4", "tok", "")
	if code == 200 {
		t.Fatalf("path escape returned 200; expected non-200")
	}
}

// doRequestRaw is a thin variant of doRequest that exposes headers +
// raw body bytes so we can assert byte-range semantics.
func doRequestRaw(t *testing.T, method, url, token, body string, extraHeaders map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// currentTestHTTPServer is a hook so tests can reach into the server
// instance started by startTestServer. startTestServer sets it before
// returning.
var currentTestHTTPServer *HTTPServer

// we keep json imported for future use via indirection
var _ = json.RawMessage("")
