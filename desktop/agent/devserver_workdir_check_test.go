package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDevServerStart_RejectsMissingWorkDir — when /dev/start is invoked
// with a workDir that doesn't exist on this machine, the agent must
// short-circuit with 404 and a clear "workDir not found" message
// rather than accept the request, kick off an operation that fails
// minutes later inside `npm install` with
// `chdir /root/<...>: no such file or directory`, and leave the
// mobile UI staring at a stuck "Start failed / Stop" card.
//
// The trigger from the field: user switches Remote Box from a Linux
// host (paths like /root/...) to a Mac mini (paths like /Users/...).
// The mobile UI carries the previous box's workDir into the next
// /dev/start, the Mac happily accepts it, and the failure surfaces
// 30+ seconds later in a confusing form.
func TestDevServerStart_RejectsMissingWorkDir(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	// Path is shaped like the field bug — Linux-style absolute path
	// invented client-side, never created on the test box.
	body := `{"workDir":"/root/nope-this-path-cannot-exist-on-the-test-host","framework":"expo","platform":"ios"}`
	req := httptest.NewRequest(http.MethodPost, "/dev/start", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+fx.hostToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.server.auth(fx.server.handleDevServerStart)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "workDir not found") {
		t.Fatalf("body should mention 'workDir not found', got: %s", rec.Body.String())
	}
}

// TestDevServerStart_AcceptsExistingWorkDir — sanity check the inverse:
// an existing workDir must NOT be rejected by the new check. Without
// this, the new guard could over-fire on every legitimate /dev/start
// and break Hot Reload entirely.
func TestDevServerStart_AcceptsExistingWorkDir(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	body := `{"workDir":"` + fx.sfmgDir + `","framework":"expo","platform":"ios"}`
	req := httptest.NewRequest(http.MethodPost, "/dev/start", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+fx.hostToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.server.auth(fx.server.handleDevServerStart)(rec, req)

	// The handler may still 4xx for unrelated reasons (no expo deps in
	// the empty fixture dir, etc.), but it must NOT be the 404
	// "workDir not found" we'd expect from the missing-path branch.
	if rec.Code == http.StatusNotFound &&
		strings.Contains(rec.Body.String(), "workDir not found") {
		t.Fatalf("existing workDir was rejected as missing: %s", rec.Body.String())
	}
}
