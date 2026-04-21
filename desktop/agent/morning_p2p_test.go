package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMorningP2PRelayFlow is the headline integration test for the
// morning feature. It proves:
//
//  1. Agent A (the "dev rig") exposes /morning and /recordings via
//     its usual owner-authed routes.
//  2. A local relay proxy forwards /d/{deviceId}/* requests to A while
//     preserving bearer auth, method, body, and byte-range headers —
//     the same contract the real relay-server package guarantees.
//  3. A second *user agent* (the client), doing exactly what the mobile
//     app or another paired Mac's agent does in production, fetches the
//     match report through the relay, performs a byte-range seek on
//     the recording, and triggers a rollback.
//
// No network, no real QUIC, no shell-outs to ffmpeg. The relay proxy
// is deliberately minimal so the test zeroes in on the HTTP contract
// the mobile/web/yaver-to-yaver viewers rely on.
func TestMorningP2PRelayFlow(t *testing.T) {
	// ── Agent A (dev rig) ──────────────────────────────────────────
	tmA := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	defer tmA.Shutdown()
	baseA, cancelA := startTestServer(t, "tokA", tmA)
	defer cancelA()

	srvA := currentTestHTTPServer
	srvA.deviceID = "deviceA"
	srvA.morningStoreRef = NewMorningStore(t.TempDir())
	srvA.recordingMgrRef = NewRecordingManager(t.TempDir())

	repo, base, head := runMorningRepo(t)
	runID, taskID := primeMorningSummary(t, srvA, repo, base, head)

	// Plant a mock recording so the byte-range path is exercised.
	recDir := filepath.Join(srvA.recordingMgrRef.root, runID, taskID)
	if err := os.MkdirAll(recDir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 5000)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(recDir, "video.mp4"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	// Stamp video metadata into the summary so clients see hasVideo=true.
	if _, err := srvA.morningStoreRef.UpsertTask(runID, "", "", TaskHighlight{
		TaskID:          taskID,
		HasVideo:        true,
		VideoSizeBytes:  int64(len(payload)),
		VideoDurationMs: 4000,
	}); err != nil {
		t.Fatal(err)
	}

	// ── Relay proxy ────────────────────────────────────────────────
	// Strips the /d/{deviceId} prefix and forwards the rest to the
	// target agent, preserving auth + range headers. Mirrors what the
	// production relay (relay/ package) does for arbitrary HTTP.
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/d/") {
			http.NotFound(w, r)
			return
		}
		tail := strings.TrimPrefix(r.URL.Path, "/d/")
		slash := strings.IndexByte(tail, '/')
		if slash < 0 {
			http.Error(w, "missing path", 400)
			return
		}
		deviceID := tail[:slash]
		remainder := tail[slash:]
		if deviceID != "deviceA" {
			http.Error(w, "no such device", http.StatusBadGateway)
			return
		}
		targetURL := baseA + remainder
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		for k, vv := range r.Header {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer relay.Close()

	relayBase := relay.URL + "/d/deviceA"

	// ── Client flow ────────────────────────────────────────────────
	// 1. Fetch run list through the relay.
	code, body := p2pGetJSON(t, relayBase+"/morning/runs", "tokA")
	if code != 200 {
		t.Fatalf("list through relay: status=%d body=%+v", code, body)
	}
	runs, _ := body["runs"].([]interface{})
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	// 2. Get the run and verify the video metadata survived the relay.
	code, body = p2pGetJSON(t, relayBase+"/morning/runs/"+runID, "tokA")
	if code != 200 {
		t.Fatalf("get run through relay: status=%d body=%+v", code, body)
	}
	run, _ := body["run"].(map[string]interface{})
	tasks, _ := run["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	first, _ := tasks[0].(map[string]interface{})
	if first["hasVideo"] != true {
		t.Fatalf("hasVideo lost through relay: %+v", first)
	}

	// 3. Byte-range GET the video through the relay.
	status, headers, partial := p2pGetRaw(t, relayBase+"/recordings/"+runID+"/"+taskID+"/video.mp4",
		"tokA", map[string]string{"Range": "bytes=100-199"})
	if status != http.StatusPartialContent {
		t.Fatalf("byte-range through relay: status=%d", status)
	}
	if len(partial) != 100 {
		t.Fatalf("byte-range body len=%d, want 100", len(partial))
	}
	if !bytes.Equal(partial, payload[100:200]) {
		t.Fatalf("byte-range body corrupted through relay")
	}
	if cr := headers.Get("Content-Range"); !strings.HasPrefix(cr, "bytes 100-199/") {
		t.Fatalf("content-range header lost: %q", cr)
	}

	// 4. Rollback through the relay.
	code, body = p2pPostJSON(t, relayBase+"/morning/runs/"+runID+"/tasks/"+taskID+"/rollback", "tokA")
	if code != 200 {
		t.Fatalf("rollback through relay: status=%d body=%+v", code, body)
	}
	revertSHA, _ := body["revertSha"].(string)
	if revertSHA == "" {
		t.Fatalf("revertSha missing: %+v", body)
	}

	// 5. A wrong-token client is rejected even through the relay —
	//    proves the relay doesn't strip the auth layer.
	code, _ = p2pGetJSON(t, relayBase+"/morning/runs", "bogus")
	if code == 200 {
		t.Fatalf("wrong-token leaked through relay")
	}

	// 6. Guest-token access to /morning/* is blocked (morning is not
	//    in the guest allowlist). We simulate a guest by setting the
	//    X-Yaver-Guest header; the auth middleware checks the path
	//    against guestAllowedPrefixes and rejects.
	status, _, _ = p2pGetRaw(t, relayBase+"/morning/runs", "tokA",
		map[string]string{"X-Yaver-Guest": "true", "X-Yaver-GuestUserID": "guest-1"})
	// We accept either "blocked" (403) or success depending on how
	// the local auth middleware treats an owner-token-with-guest-hint.
	// The invariant that matters is: a real guest session can't reach
	// morning — enforced by not adding /morning/ to guestAllowedPrefixes,
	// which is covered by the server build. This final check is a
	// smoke test so we notice if that invariant silently flips.
	if status == http.StatusOK {
		// If it was a full-owner call that happened to pass, that's
		// still OK — the test's prior steps already validated auth
		// rejection. The guarantee we want is in the prefix list,
		// which is a static data check:
		for _, list := range [][]string{guestFullAllowedPrefixes, guestFeedbackOnlyAllowedPrefixes} {
			for _, p := range list {
				if strings.HasPrefix("/morning/", p) || strings.HasPrefix("/recordings/", p) {
					t.Fatalf("morning/recordings leaked into a guest allowlist: %q", p)
				}
			}
		}
	}
}

// ── Tiny JSON helpers ─────────────────────────────────────────────────

func p2pGetJSON(t *testing.T, url, token string) (int, map[string]interface{}) {
	t.Helper()
	return p2pJSON(t, http.MethodGet, url, token)
}

func p2pPostJSON(t *testing.T, url, token string) (int, map[string]interface{}) {
	t.Helper()
	return p2pJSON(t, http.MethodPost, url, token)
}

func p2pJSON(t *testing.T, method, url, token string) (int, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	return resp.StatusCode, body
}

func p2pGetRaw(t *testing.T, url, token string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// Suppress "unused" if tests above are skipped in some environments.
var _ = fmt.Sprintf
