package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBumpSemver(t *testing.T) {
	cases := []struct {
		in, kind, want string
		wantErr        bool
	}{
		{"1.99.152", "patch", "1.99.153", false},
		{"1.99.152", "minor", "1.100.0", false},
		{"1.99.152", "major", "2.0.0", false},
		{"1.99.152", "", "1.99.153", false}, // empty == patch
		{"v1.2.3", "patch", "", true},        // leading v rejected
		{"1.2", "patch", "", true},           // not X.Y.Z
		{"1.2.3-beta", "patch", "", true},    // pre-release not supported
		{"1.99.152", "weird", "", true},      // unknown bump kind
	}
	for _, c := range cases {
		got, err := bumpSemver(c.in, c.kind)
		if c.wantErr {
			if err == nil {
				t.Errorf("bumpSemver(%q,%q): want error, got %q", c.in, c.kind, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("bumpSemver(%q,%q): unexpected error %v", c.in, c.kind, err)
			continue
		}
		if got != c.want {
			t.Errorf("bumpSemver(%q,%q) = %q, want %q", c.in, c.kind, got, c.want)
		}
	}
}

func TestUpdateJSONField_PreservesShape(t *testing.T) {
	// versions.json has multiple top-level keys; updating "cli" must
	// not reorder or rewrite anything else.
	dir := t.TempDir()
	path := filepath.Join(dir, "versions.json")
	original := `{
  "cli": "1.99.152",
  "mobile": "1.18.86",
  "relay": "0.1.17",
  "web": "1.1.118"
}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := updateJSONField(path, "cli", "1.99.153"); err != nil {
		t.Fatalf("updateJSONField: %v", err)
	}

	got, _ := os.ReadFile(path)
	want := strings.Replace(original, `"cli": "1.99.152"`, `"cli": "1.99.153"`, 1)
	if string(got) != want {
		t.Errorf("file rewrite changed shape:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestUpdateJSONField_MissingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	os.WriteFile(path, []byte(`{"foo":"bar"}`), 0o644)
	if err := updateJSONField(path, "missing", "1.0.0"); err == nil {
		t.Error("expected error when key not found")
	}
}

func TestUpdatePackageLockVersion_BothFields(t *testing.T) {
	// Real package-lock.json has version twice at the top — the
	// top-level field and the implicit `""` package entry. Both must
	// be updated, but dependency entries below must not be touched.
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	original := `{
  "name": "yaver-cli",
  "version": "1.99.152",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "yaver-cli",
      "version": "1.99.152",
      "dependencies": {
        "minimist": "^1.2.8"
      }
    },
    "node_modules/minimist": {
      "version": "1.2.8",
      "license": "MIT"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := updatePackageLockVersion(path, "1.99.153"); err != nil {
		t.Fatalf("updatePackageLockVersion: %v", err)
	}

	got, _ := os.ReadFile(path)
	gotStr := string(got)

	// Both top-level + "" entry must be 1.99.153.
	if strings.Count(gotStr, `"version": "1.99.153"`) != 2 {
		t.Errorf("expected 2 updated occurrences, got:\n%s", gotStr)
	}
	// minimist version is a dependency, must remain untouched.
	if !strings.Contains(gotStr, `"version": "1.2.8"`) {
		t.Error("dependency version 1.2.8 was rewritten — bug")
	}
}

func TestHasVersionKey(t *testing.T) {
	dir := t.TempDir()
	withVer := filepath.Join(dir, "with.json")
	os.WriteFile(withVer, []byte(`{"version": "1.0.0"}`), 0o644)
	withoutVer := filepath.Join(dir, "without.json")
	os.WriteFile(withoutVer, []byte(`{"name": "x"}`), 0o644)

	if !hasVersionKey(withVer) {
		t.Error("hasVersionKey: false negative on file with version field")
	}
	if hasVersionKey(withoutVer) {
		t.Error("hasVersionKey: false positive on file without version field")
	}
}
