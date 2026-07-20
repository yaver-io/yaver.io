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

// TestGitOAuthClientID_CompiledDefaultEnablesZeroConfigBootstrap is the
// regression guard for zero-config OAuth on a freshly provisioned cloud box:
// with NO vault entry and NO env override, github/gitlab must still resolve a
// working client ID from the compiled default (byo=false = the shipped Yaver
// Device-Flow app). This is what makes "super-fast OAuth bootstrap" work on a
// brand-new managed box that has never been configured. If someone blanks the
// compiled defaults, this fails loudly rather than silently regressing boot.
func TestGitOAuthClientID_CompiledDefaultEnablesZeroConfigBootstrap(t *testing.T) {
	t.Setenv("YAVER_GITHUB_OAUTH_CLIENT_ID", "")
	t.Setenv("YAVER_GITLAB_OAUTH_CLIENT_ID", "")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("YAVER_VAULT_PASSPHRASE", "")

	for _, provider := range []string{"github", "gitlab"} {
		id, byo, err := gitOAuthClientID(provider)
		if err != nil {
			t.Fatalf("%s: expected compiled default (zero-config bootstrap), got error: %v", provider, err)
		}
		if strings.TrimSpace(id) == "" {
			t.Errorf("%s: expected non-empty compiled default client id", provider)
		}
		if byo {
			t.Errorf("%s: expected byo=false for the shipped default client id", provider)
		}
	}
}

// TestGitOAuthClientID_UnsupportedProviderErrors keeps the helpful-error path
// covered: an unknown provider (no compiled default) must return a clear
// error, not a cryptic crash.
func TestGitOAuthClientID_UnsupportedProviderErrors(t *testing.T) {
	_, _, err := gitOAuthClientID("bitbucket")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("error should name the unsupported provider, got: %s", err.Error())
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
