package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These guard the defect that made a healthy Mac mini report itself unreachable
// from the phone on 2026-07-20.
//
// POST /tasks hung forever. The goroutine dump was unambiguous:
//
//	createTask -> taskPlacementRequestFromTaskBody -> taskPlacementStackLabel
//	  -> DetectMonorepo -> walkAndClassify -> classifyDir -> hasXcodeProj
//	  -> os.ReadDir   [blocked]
//
// workDir defaulted to "." — the agent's CWD — which was the user's HOME
// directory. So every task creation recursively classified the entire home
// tree. `find ~ -maxdepth 4 -type d` on that box did not finish in 120s.
//
// The user saw "Timed out after 30s — the machine accepted the connection but
// never answered" while the box was idle with three ready runners. Advisory
// placement metadata sat in the critical path of the operation it annotates.

// TestUnspecifiedWorkDirIsNotScanned pins the exact default that broke.
// "" and "." must never license a scan of whatever directory the daemon
// happens to be running in.
func TestUnspecifiedWorkDirIsNotScanned(t *testing.T) {
	for _, dir := range []string{"", ".", "   "} {
		if isScannableProjectDir(dir) {
			t.Errorf("isScannableProjectDir(%q) = true — an unspecified workDir "+
				"means \"unknown\", and the agent CWD is not a safe stand-in. "+
				"This is the default that scanned a home directory and hung "+
				"POST /tasks forever", dir)
		}
	}
}

// TestHomeDirectoryIsNotScannable — a home directory is not a repo, and is
// unbounded to walk. Resolved via os.UserHomeDir so this holds for ANY user on
// ANY box; Yaver is not single-user and must never assume a path layout.
func TestHomeDirectoryIsNotScannable(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this platform")
	}
	if isScannableProjectDir(home) {
		t.Fatal("the user home directory must never be recursively classified — " +
			"that is the walk that never finished")
	}
	if isScannableProjectDir(string(filepath.Separator)) {
		t.Error("filesystem root must never be recursively classified")
	}
}

// TestRealProjectDirIsStillScanned — the guard must not break the feature it
// protects. A directory with a project marker is still scanned.
func TestRealProjectDirIsStillScanned(t *testing.T) {
	dir := t.TempDir()
	if isScannableProjectDir(dir) {
		t.Fatal("a directory with no project marker should not be scanned")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isScannableProjectDir(dir) {
		t.Error("a directory containing go.mod IS a project root and must still " +
			"be scanned — the bound must not disable detection")
	}
}

// TestWalkIsBoundedByWallClock is the invariant that actually matters. Depth
// alone is not a bound: breadth at depth 4 under a home directory is
// effectively unbounded, which is why the original maxDepth did not save us.
// Only wall-clock bounds wall-clock.
func TestWalkIsBoundedByWallClock(t *testing.T) {
	root := t.TempDir()
	// A wide, shallow tree — the shape that defeats a depth limit.
	for i := 0; i < 60; i++ {
		for j := 0; j < 60; j++ {
			_ = os.MkdirAll(filepath.Join(root, dirName(i), dirName(j)), 0o755)
		}
	}

	start := time.Now()
	// Already-expired deadline: the walk must give up immediately rather than
	// enumerate the tree.
	got := walkAndClassifyUntil(root, root, 0, 4, map[string]bool{}, time.Now().Add(-time.Second))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("walk took %v with an expired deadline — it is not bounded, so a "+
			"large directory can still park POST /tasks forever", elapsed)
	}
	if got != nil {
		t.Errorf("expected no results from an expired-deadline walk, got %d", len(got))
	}
}

func dirName(i int) string {
	return string(rune('a'+i/26)) + string(rune('a'+i%26))
}
