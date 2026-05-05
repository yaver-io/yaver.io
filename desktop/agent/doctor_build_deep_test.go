package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestIsPathSecret pins the suffix list so future renames have to
// consciously update both the helper and any deploy-template secrets
// that rely on path validity (APP_STORE_KEY_PATH, PLAY_STORE_KEY_FILE).
func TestIsPathSecret(t *testing.T) {
	cases := map[string]bool{
		"APP_STORE_KEY_PATH":   true,
		"PLAY_STORE_KEY_FILE":  true,
		"ANDROID_KEY_PATH":     true,
		"FOO_KEYSTORE":         true,
		"APPLE_TEAM_ID":        false,
		"APP_STORE_KEY_ID":     false,
		"APP_STORE_KEY_ISSUER": false,
		"":                     false,
	}
	for name, want := range cases {
		got := isPathSecret(name)
		if got != want {
			t.Errorf("isPathSecret(%q) = %v; want %v", name, got, want)
		}
	}
}

// TestCheckPathSecret_Existing verifies the happy path resolves a real
// file. We use the test binary's own location (always present during
// `go test`) so the test doesn't depend on system state.
func TestCheckPathSecret_Existing(t *testing.T) {
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "AuthKey_FAKE.p8")
	if err := os.WriteFile(keyPath, []byte("fake key"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resolved, err := checkPathSecret(keyPath)
	if err != nil {
		t.Fatalf("checkPathSecret: %v", err)
	}
	if resolved != keyPath {
		t.Errorf("resolved = %q; want %q", resolved, keyPath)
	}
}

// TestCheckPathSecret_Missing confirms the deep probe catches the
// "vault has the env var, but the file got deleted" case — the wedge
// the user explicitly called out.
func TestCheckPathSecret_Missing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "AuthKey_MISSING.p8")
	_, err := checkPathSecret(missing)
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error = %q; want substring 'file not found'", err.Error())
	}
}

// TestCheckPathSecret_Directory rejects a path that points at a folder
// instead of a key file — easy mistake when the user runs `yaver vault
// add APP_STORE_KEY_PATH` and tabs to the parent dir by accident.
func TestCheckPathSecret_Directory(t *testing.T) {
	dir := t.TempDir()
	_, err := checkPathSecret(dir)
	if err == nil {
		t.Fatalf("expected error for directory, got nil")
	}
	if !strings.Contains(err.Error(), "got a directory") {
		t.Errorf("error = %q; want 'got a directory'", err.Error())
	}
}

// TestCheckPathSecret_Tilde expands ~ to the real home dir so the deep
// check matches what the deploy script sees.
func TestCheckPathSecret_Tilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this runner")
	}
	if _, err := os.Stat(home); err != nil {
		t.Skip("home dir does not exist")
	}
	// ~ on its own resolves to a directory — checkPathSecret rejects
	// directories. That's fine; we use ~/<known-existing-file> via a
	// temp file we drop into HOME instead, then clean up.
	tmpName := ".yaver-doctor-test-" + filepath.Base(t.TempDir())
	abs := filepath.Join(home, tmpName)
	if err := os.WriteFile(abs, []byte("x"), 0600); err != nil {
		t.Skipf("cannot write to home: %v", err)
	}
	defer os.Remove(abs)

	resolved, err := checkPathSecret("~/" + tmpName)
	if err != nil {
		t.Fatalf("checkPathSecret: %v", err)
	}
	if resolved != abs {
		t.Errorf("resolved = %q; want %q", resolved, abs)
	}
}

// TestJavaMajorVersion_Parse covers each output flavour we've seen
// in the wild: Oracle JDK, OpenJDK, and the legacy 1.8.0 scheme.
// The actual `java -version` invocation is exercised by an
// integration probe in TestRunBuildDoctor_DeepProbes when java is
// installed — here we just verify the regex.
func TestJavaMajorVersion_Parse(t *testing.T) {
	// Regex is exercised inside javaMajorVersion via a fake binary
	// that prints a known string. We can't call the parser directly
	// since it shells out, so build a tiny shell stub.
	if runtime.GOOS == "windows" {
		t.Skip("shell stub trick is unix-only")
	}
	cases := map[string]int{
		`openjdk version "17.0.8" 2023-07-18`: 17,
		`java version "1.8.0_281"`:            8,
		`openjdk version "21" 2023-09-19`:     21,
		`openjdk version "11.0.20" 2023-07-18`: 11,
	}
	for body, want := range cases {
		dir := t.TempDir()
		stub := filepath.Join(dir, "java")
		// Stub writes the version to STDERR (matches real `java -version`)
		// and exits 0. Real `java -version` exits 0 too for most JDKs.
		script := "#!/bin/sh\necho '" + strings.ReplaceAll(body, "'", `'"'"'`) + "' 1>&2\n"
		if err := os.WriteFile(stub, []byte(script), 0755); err != nil {
			t.Fatalf("stub: %v", err)
		}
		got, _, err := javaMajorVersion(context.Background(), stub)
		if err != nil {
			t.Errorf("body=%q err=%v", body, err)
			continue
		}
		if got != want {
			t.Errorf("body=%q got=%d want=%d", body, got, want)
		}
	}
}

// TestRunBuildDoctor_DeepFiltering_PathSecretInvalid confirms that a
// vault-found secret pointing at a missing file flips report.OK to
// false AND surfaces a sanitised PathError.
func TestRunBuildDoctor_DeepFiltering_PathSecretInvalid(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	vs, err := NewVaultStoreWithDevice("p", "test-dev")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	// Set every testflight secret. Path-shaped one points at a path
	// that does NOT exist — the deep check should catch it.
	missing := filepath.Join(tmp, "missing-key.p8")
	if err := vs.Set(VaultEntry{Name: "APP_STORE_KEY_PATH", Value: missing}); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	for _, n := range []string{"APP_STORE_KEY_ID", "APP_STORE_KEY_ISSUER", "APPLE_TEAM_ID"} {
		if err := vs.Set(VaultEntry{Name: n, Value: "x"}); err != nil {
			t.Fatalf("vault set %s: %v", n, err)
		}
	}

	rep, err := RunBuildDoctor("testflight", "", vs)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	// Find the APP_STORE_KEY_PATH secret in the report.
	var got *BuildSecretResult
	for i := range rep.Secrets {
		if rep.Secrets[i].Name == "APP_STORE_KEY_PATH" {
			got = &rep.Secrets[i]
			break
		}
	}
	if got == nil {
		t.Fatal("APP_STORE_KEY_PATH not in report.Secrets")
	}
	if !got.Found {
		t.Errorf("Found = false; want true (env var resolved from vault)")
	}
	if got.PathValid == nil {
		t.Fatal("PathValid is nil; deep check did not run for path secret")
	}
	if *got.PathValid {
		t.Errorf("PathValid = true; want false for missing file")
	}
	if !strings.Contains(got.PathError, "file not found") {
		t.Errorf("PathError = %q; want substring 'file not found'", got.PathError)
	}

	// On Linux, the platforms gate already sets OK=false (xcodebuild
	// is darwin-only). On Mac CI, the path check is what flips OK to
	// false. Either way, the report must NOT be OK.
	if rep.OK {
		t.Errorf("report.OK = true; want false (missing key file should block)")
	}
}

// TestRunBuildDoctor_DeepFiltering_PathSecretValid is the inverse —
// when the file exists, PathValid=true and the report stays "deep-OK"
// for that secret (other gates may still fail, e.g. xcodebuild stub on
// non-darwin).
func TestRunBuildDoctor_DeepFiltering_PathSecretValid(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	vs, err := NewVaultStoreWithDevice("p", "test-dev")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	keyPath := filepath.Join(tmp, "AuthKey_REAL.p8")
	if err := os.WriteFile(keyPath, []byte("fake"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "APP_STORE_KEY_PATH", Value: keyPath}); err != nil {
		t.Fatalf("vault set: %v", err)
	}

	rep, err := RunBuildDoctor("testflight", "", vs)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, s := range rep.Secrets {
		if s.Name != "APP_STORE_KEY_PATH" {
			continue
		}
		if s.PathValid == nil || !*s.PathValid {
			t.Errorf("PathValid = %v; want true for existing file (PathError=%q)", s.PathValid, s.PathError)
		}
	}
}

// TestSanitisePathInError replaces the user's home dir with `~` so
// cross-device responses don't leak the macOS short username via
// embedded paths in error strings.
func TestSanitisePathInError(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	in := "file not found at " + home + "/keys/AuthKey_FAKE.p8"
	got := sanitisePathInError(in)
	want := "file not found at ~/keys/AuthKey_FAKE.p8"
	if got != want {
		t.Errorf("sanitisePathInError\n  got:  %q\n  want: %q", got, want)
	}
}

// TestBuildDoctorReport_RoundTrip confirms the extended struct is
// JSON-serialisable both ways. The mobile clients decode this verbatim
// — a stray `omitempty` removal would break their decoders silently.
func TestBuildDoctorReport_RoundTrip(t *testing.T) {
	valid := true
	rep := BuildDoctorReport{
		Target: "testflight",
		OK:     true,
		Tools: []BuildToolResult{
			{Name: "xcodebuild", Required: true, Found: true, DeepValid: &valid, VersionMajor: 0},
		},
		Secrets: []BuildSecretResult{
			{Name: "APP_STORE_KEY_PATH", Found: true, PathValid: &valid},
		},
		ProjectStatus: &BuildProjectStatus{Name: "myapp", Found: true, Stack: "react-native-expo"},
	}
	round, err := jsonForRoundTripDeep(rep)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if round.Target != "testflight" {
		t.Errorf("Target lost: %+v", round)
	}
	if round.ProjectStatus == nil || round.ProjectStatus.Name != "myapp" {
		t.Errorf("ProjectStatus lost: %+v", round.ProjectStatus)
	}
	if len(round.Tools) != 1 || round.Tools[0].DeepValid == nil || !*round.Tools[0].DeepValid {
		t.Errorf("Tool DeepValid lost: %+v", round.Tools)
	}

	// Also verify omitempty fields are dropped when unset — keeps the
	// wire payload tight (mobile decode time matters).
	plain := BuildToolResult{Name: "node", Required: true, Found: true}
	b, _ := json.Marshal(plain)
	if strings.Contains(string(b), "deepValid") {
		t.Errorf("deepValid leaked into payload when nil: %s", string(b))
	}
	if strings.Contains(string(b), "deepError") {
		t.Errorf("deepError leaked into payload when empty: %s", string(b))
	}
	if strings.Contains(string(b), "versionMajor") {
		t.Errorf("versionMajor leaked into payload when 0: %s", string(b))
	}
}

// TestFirstBlockerFromReport_Deep covers the new priority ordering —
// project missing beats every tool gate, deep-tool-fail beats secret
// gates, path-secret-invalid is last.
func TestFirstBlockerFromReport_Deep(t *testing.T) {
	falseRef := false

	// Project missing wins over everything else.
	rep1 := BuildDoctorReport{
		OK:            false,
		ProjectStatus: &BuildProjectStatus{Name: "sfmg", Found: false, Reason: "no workspace entry on this machine"},
		Tools: []BuildToolResult{
			{Name: "xcodebuild", Required: true, Skipped: true, SkipReason: "only on darwin"},
		},
	}
	if got := firstBlockerFromReport(rep1); !strings.Contains(got, "sfmg") {
		t.Errorf("project-missing should win: got %q", got)
	}

	// DeepValid=false beats missing-secret.
	rep2 := BuildDoctorReport{
		OK: false,
		Tools: []BuildToolResult{
			{Name: "java", Required: true, Found: true, DeepValid: &falseRef, DeepError: "Java 17+ required (found 11)"},
		},
		Secrets: []BuildSecretResult{
			{Name: "ANDROID_KEYSTORE_PASSWORD", Found: false},
		},
	}
	got := firstBlockerFromReport(rep2)
	if !strings.Contains(got, "Java 17") {
		t.Errorf("deep-tool blocker should beat missing-secret: got %q", got)
	}

	// Missing secret beats path-secret-invalid (because if a different
	// secret is missing entirely, fixing the path is wasted effort).
	rep3 := BuildDoctorReport{
		OK: false,
		Secrets: []BuildSecretResult{
			{Name: "APPLE_TEAM_ID", Found: false},
			{Name: "APP_STORE_KEY_PATH", Found: true, PathValid: &falseRef, PathError: "file not found at ~/foo.p8"},
		},
	}
	got3 := firstBlockerFromReport(rep3)
	if !strings.Contains(got3, "APPLE_TEAM_ID") {
		t.Errorf("missing-secret should beat path-invalid: got %q", got3)
	}

	// Path-secret-invalid surfaces when no other gate fires.
	rep4 := BuildDoctorReport{
		OK: false,
		Secrets: []BuildSecretResult{
			{Name: "APP_STORE_KEY_PATH", Found: true, PathValid: &falseRef, PathError: "file not found at ~/foo.p8"},
		},
	}
	got4 := firstBlockerFromReport(rep4)
	if !strings.Contains(got4, "APP_STORE_KEY_PATH") || !strings.Contains(got4, "file not found") {
		t.Errorf("path-invalid should surface: got %q", got4)
	}
}
