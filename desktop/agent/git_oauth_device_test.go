package main

// git_oauth_device_test.go — privacy + structural tests for the
// GitHub/GitLab Device Flow. The contract: tokens, device codes, user
// codes, verification URLs — none of it ever reaches Convex. Persistence
// is local-only (vault + ~/.yaver/git-credentials.json + provider
// metadata file). The OAuth state machine itself lives in agent RAM.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestGitOAuthSessionSerialization_HidesDeviceCode pins the serialized
// shape so DeviceCode never goes out over the wire even by accident.
// If anyone removes the json:"-" tag on DeviceCode, this test catches
// it before a remote-targeted /git/provider/oauth/status response can
// leak the device_code (which is a poll-token, not as sensitive as the
// access token but still owner-only and worth not-spraying).
func TestGitOAuthSessionSerialization_HidesDeviceCode(t *testing.T) {
	sess := gitOAuthSession{
		ID:              "test-session",
		Provider:        "github",
		Host:            "github.com",
		UserCode:        "ABCD-1234",
		VerificationURI: "https://github.com/login/device",
		DeviceCode:      "must-not-leak",
		Interval:        5,
		ExpiresAt:       time.Now().Add(15 * time.Minute),
		StartedAt:       time.Now(),
		State:           "pending",
	}
	b, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "must-not-leak") {
		t.Errorf("device_code leaked into serialized session: %s", b)
	}
	if strings.Contains(string(b), "device_code") {
		t.Errorf("device_code key appeared in serialized session: %s", b)
	}
}

// TestGetGitOAuthSession_ScrubsDeviceCode protects the public accessor
// in case a future refactor changes how sessions are stored. The
// returned copy must not retain the device_code regardless of the
// session's terminal state.
func TestGetGitOAuthSession_ScrubsDeviceCode(t *testing.T) {
	sessionID := "scrub-test-" + newGitOAuthSessionID()
	gitOAuthSessionsMu.Lock()
	gitOAuthSessions[sessionID] = &gitOAuthSession{
		ID:         sessionID,
		Provider:   "github",
		DeviceCode: "should-be-scrubbed",
		State:      "pending",
	}
	gitOAuthSessionsMu.Unlock()
	defer func() {
		gitOAuthSessionsMu.Lock()
		delete(gitOAuthSessions, sessionID)
		gitOAuthSessionsMu.Unlock()
	}()

	got, ok := getGitOAuthSession(sessionID)
	if !ok {
		t.Fatal("session not found")
	}
	if got.DeviceCode != "" {
		t.Errorf("DeviceCode not scrubbed: %q", got.DeviceCode)
	}
}

// TestPersistGitProviderTokenForOAuth_NoConvexCalls is the load-bearing
// privacy assertion: writing an OAuth-flow token to disk must NOT hit
// any Convex mutation. The persistence path goes:
//
//	tok → loadGitCredentials/saveGitCredentials (~/.yaver/git-credentials.json)
//	    → loadGitProviders/saveGitProviders     (~/.yaver/git-providers.json)
//
// All three are local-fs helpers; if anyone wires them through Convex
// in the future, this test trips. We use a temp HOME so the test
// doesn't touch the developer's real credential files.
func TestPersistGitProviderTokenForOAuth_NoConvexCalls(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	buf, teardown := installConvexRecorder(t)
	defer teardown()

	if err := persistGitProviderTokenForOAuth(
		"github",
		"github.com",
		"oauth-token-not-real",
		"test-user",
		"",
	); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if len(*buf) != 0 {
		paths := make([]string, 0, len(*buf))
		for _, m := range *buf {
			paths = append(paths, m.Path)
		}
		t.Errorf("expected zero Convex mutations from OAuth-flow persistence; got %d: %v",
			len(*buf), paths)
	}
}

// TestGitOAuthSession_SerializedFieldNames pins the public field names
// returned over /git/provider/oauth/status. If a future refactor adds
// a field with a forbidden name (token, secret, password, …) we want
// to catch it here instead of in production logs.
func TestGitOAuthSession_SerializedFieldNames(t *testing.T) {
	sess := gitOAuthSession{State: "pending"}
	b, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]interface{}
	if err := json.Unmarshal(b, &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	forbidden := []string{
		"token", "access_token", "secret", "password", "device_code",
		"client_secret", "refresh_token",
	}
	for _, f := range forbidden {
		if _, present := asMap[f]; present {
			t.Errorf("forbidden field %q surfaced in gitOAuthSession JSON: %s", f, b)
		}
	}
}

// TestGitOAuthClientID_BYOOverride confirms vault > env > compiled
// precedence. We don't actually run the vault path here (would need a
// passphrase + temp store) — env override is enough to prove the
// override mechanism exists and the function is reachable.
func TestGitOAuthClientID_EnvOverride(t *testing.T) {
	t.Setenv("YAVER_GITHUB_OAUTH_CLIENT_ID", "from-env-test")
	id, byo, err := gitOAuthClientID("github")
	if err != nil {
		t.Fatalf("client id: %v", err)
	}
	if id != "from-env-test" {
		t.Errorf("expected env override, got %q", id)
	}
	if !byo {
		t.Errorf("expected byo=true when env overrides default")
	}
}

// TestGitOAuthClientID_NoConfigErrorsHelpfully ensures a fresh box with
// no client ID configured returns a descriptive error pointing the user
// at the registration page + vault entry, not a cryptic crash.
func TestGitOAuthClientID_NoConfigErrorsHelpfully(t *testing.T) {
	t.Setenv("YAVER_GITHUB_OAUTH_CLIENT_ID", "")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("YAVER_VAULT_PASSPHRASE", "")

	_, _, err := gitOAuthClientID("github")
	if err == nil {
		t.Fatal("expected error when no client id configured")
	}
	msg := err.Error()
	for _, want := range []string{"github.com/settings/developers", "github-oauth-client-id", "YAVER_GITHUB_OAUTH_CLIENT_ID"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q hint: %s", want, msg)
		}
	}
}

// TestGitOAuthSession_StructFieldsAreFlat is a lightweight reflection
// check that no nested struct sneaks in carrying secrets. Keeps
// gitOAuthSession a flat record of metadata.
func TestGitOAuthSession_StructFieldsAreFlat(t *testing.T) {
	rt := reflect.TypeOf(gitOAuthSession{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		switch f.Type.Kind() {
		case reflect.Struct:
			// time.Time is fine — it's the only struct that should
			// appear and it serializes as a string.
			if f.Type.String() != "time.Time" {
				t.Errorf("unexpected nested struct field %s of type %s — keep gitOAuthSession flat",
					f.Name, f.Type.String())
			}
		case reflect.Map, reflect.Slice:
			t.Errorf("unexpected complex field %s of kind %s — keep gitOAuthSession flat",
				f.Name, f.Type.Kind())
		}
	}
}
