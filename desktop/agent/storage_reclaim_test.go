package main

// storage_reclaim_test.go — the guard is the whole feature. A reclaim that
// frees 40 GB is worth nothing if it can also eat a repo, so the tests that
// matter here are the refusals.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReclaimPathAllowed_RefusesDangerousPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	cases := []struct {
		name string
		path string
		want string // substring of the expected refusal
	}{
		{"empty", "", "empty path"},
		{"relative", "Library/Caches", "must be absolute"},
		{"filesystem root", string(filepath.Separator), "filesystem root"},
		{"home itself", home, "home directory"},
		{"outside home", filepath.Dir(home), "outside the home directory"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := reclaimPathAllowed(tc.path)
			if err == nil {
				t.Fatalf("expected refusal for %q, got nil", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

// A cache dir has no .git. Source does. If the catalog ever proposes a path
// that turns out to be a repo, the guard must win over the catalog.
func TestReclaimPathAllowed_RefusesGitRepo(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	repo, err := os.MkdirTemp(home, "yaver-reclaim-test-repo-")
	if err != nil {
		t.Skipf("cannot create temp dir under home: %v", err)
	}
	defer os.RemoveAll(repo)

	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = reclaimPathAllowed(repo)
	if err == nil {
		t.Fatal("expected refusal for a git repository, got nil")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("expected git-repo refusal, got %q", err.Error())
	}
}

// A symlinked cache dir must be judged by where it POINTS, not where it sits —
// otherwise a symlink to / walks past every check.
func TestReclaimPathAllowed_ResolvesSymlinks(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir, err := os.MkdirTemp(home, "yaver-reclaim-test-link-")
	if err != nil {
		t.Skipf("cannot create temp dir under home: %v", err)
	}
	defer os.RemoveAll(dir)

	link := filepath.Join(dir, "cache")
	if err := os.Symlink(string(filepath.Separator), link); err != nil {
		t.Skipf("cannot symlink: %v", err)
	}
	if err := reclaimPathAllowed(link); err == nil {
		t.Fatal("expected refusal for a symlink pointing at /, got nil")
	}
}

func TestReclaimPathAllowed_AllowsRealCacheDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir, err := os.MkdirTemp(home, "yaver-reclaim-test-cache-")
	if err != nil {
		t.Skipf("cannot create temp dir under home: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := reclaimPathAllowed(dir); err != nil {
		t.Fatalf("expected a plain dir under home to be allowed, got %v", err)
	}
}

// IDs are the only thing a caller can name. An ID the catalog doesn't know
// must be refused rather than falling through to a path.
func TestPerformStorageReclaim_UnknownIDIsRefused(t *testing.T) {
	res := performStorageReclaim([]string{"deadbeefdeadbeef"}, true)
	if len(res.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(res.Outcomes))
	}
	if res.Outcomes[0].OK {
		t.Fatal("expected unknown ID to be refused")
	}
	if res.FreedBytes != 0 {
		t.Fatalf("expected 0 bytes freed, got %d", res.FreedBytes)
	}
}

func TestTargetID_StableAndPathDerived(t *testing.T) {
	a := targetID("xcode_derived_data", "/Users/x/Library/Developer/Xcode/DerivedData/App-abc")
	b := targetID("xcode_derived_data", "/Users/x/Library/Developer/Xcode/DerivedData/App-abc")
	c := targetID("xcode_derived_data", "/Users/x/Library/Developer/Xcode/DerivedData/App-xyz")
	if a != b {
		t.Fatal("expected the same path to produce a stable ID")
	}
	if a == c {
		t.Fatal("expected different paths to produce different IDs")
	}
}

func TestParseHumanBytes(t *testing.T) {
	cases := map[string]int64{
		"0B":      0,
		"512B":    512,
		"1KB":     1 << 10,
		"1.5GB":   int64(1.5 * float64(int64(1)<<30)),
		"2TB":     2 << 40,
		"":        0,
		"garbage": 0,
	}
	for in, want := range cases {
		if got := parseHumanBytes(in); got != want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", in, got, want)
		}
	}
}

// macOS reports ~15 APFS synthetic volumes that all echo the same container's
// free space. Rendering them is 15 rows of noise saying the same thing once,
// so the collapse must survive.
func TestUserVisibleFilesystems_CollapsesAPFSSiblings(t *testing.T) {
	all := []DiskSpaceEntry{
		{Mount: "/", TotalGB: 460.4, FreeGB: 17.7, UsedPct: 96},
		{Mount: "/System/Volumes/VM", TotalGB: 460.4, FreeGB: 17.7, UsedPct: 96},
		{Mount: "/System/Volumes/Preboot", TotalGB: 460.4, FreeGB: 17.7, UsedPct: 96},
		{Mount: "/System/Volumes/Data", TotalGB: 460.4, FreeGB: 17.7, UsedPct: 96},
		{Mount: "/Library/Developer/CoreSimulator/Volumes/iOS_22G86", TotalGB: 19.3, FreeGB: 0.5},
		// A genuine second disk must NOT be collapsed away.
		{Mount: "/mnt/data", TotalGB: 2000, FreeGB: 900, UsedPct: 55},
	}
	got := userVisibleFilesystems(all)

	mounts := make([]string, 0, len(got))
	for _, fs := range got {
		mounts = append(mounts, fs.Mount)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 filesystems (root + the real second disk), got %d: %v", len(got), mounts)
	}
	if got[0].Mount != "/" {
		t.Errorf("expected / to survive, got %q", got[0].Mount)
	}
	if got[1].Mount != "/mnt/data" {
		t.Errorf("expected the real second disk to survive, got %q", got[1].Mount)
	}
}

// Windows roots are "C:\", not "/". A guard that only compares against the
// separator would never fire there — which is the kind of guard that silently
// doesn't guard.
func TestIsFilesystemRoot(t *testing.T) {
	if !isFilesystemRoot(string(filepath.Separator)) {
		t.Error("expected the separator to be a filesystem root")
	}
	if isFilesystemRoot(filepath.Join("home", "kivanc")) {
		t.Error("did not expect a normal path to be a filesystem root")
	}
	// Only meaningful on Windows, where VolumeName is non-empty; on unix
	// this documents that a colon path is not special.
	if filepath.VolumeName(`C:\`) != "" && !isFilesystemRoot(`C:\`) {
		t.Error(`expected C:\ to be a filesystem root on Windows`)
	}
}

// Xcode encodes the project name in the DerivedData dir name — that
// attribution is what makes the phone UI readable, so it must not silently
// regress.
func TestXcodeDerivedDataTargets_AttributesProject(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Yaver-abcdef123456", "sfmg-999", "ModuleCache.noindex"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	targets := xcodeDerivedDataTargets(root)
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}
	got := map[string]string{}
	for _, tg := range targets {
		got[filepath.Base(tg.Path)] = tg.Project
	}
	if got["Yaver-abcdef123456"] != "Yaver" {
		t.Errorf("expected project Yaver, got %q", got["Yaver-abcdef123456"])
	}
	if got["sfmg-999"] != "sfmg" {
		t.Errorf("expected project sfmg, got %q", got["sfmg-999"])
	}
	if got["ModuleCache.noindex"] != "" {
		t.Errorf("expected the shared module cache to have no project, got %q", got["ModuleCache.noindex"])
	}
}
