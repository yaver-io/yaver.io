package testkit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPassMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// No marker yet.
	if HasPassMarker(dir, "abc1234") {
		t.Error("HasPassMarker should be false on empty dir")
	}

	if err := WritePassMarker(dir, "abc1234", "main", "darwin", 5, 2*time.Second); err != nil {
		t.Fatalf("WritePassMarker: %v", err)
	}

	if !HasPassMarker(dir, "abc1234") {
		t.Error("HasPassMarker should be true after write")
	}

	markers, err := LatestPassMarkers(dir, 10)
	if err != nil {
		t.Fatalf("LatestPassMarkers: %v", err)
	}
	if len(markers) != 1 {
		t.Fatalf("len = %d, want 1", len(markers))
	}
	if markers[0].SHA != "abc1234" {
		t.Errorf("SHA = %q", markers[0].SHA)
	}
	if markers[0].Total != 5 {
		t.Errorf("Total = %d", markers[0].Total)
	}
}

func TestWritePassMarkerEmptySHA(t *testing.T) {
	dir := t.TempDir()
	// Empty SHA should silently no-op (project isn't a git repo).
	if err := WritePassMarker(dir, "", "", "linux", 1, time.Second); err != nil {
		t.Errorf("expected nil for empty sha, got %v", err)
	}
	if HasPassMarker(dir, "") {
		t.Error("HasPassMarker should be false for empty sha")
	}
}

func TestLatestPassMarkersOrder(t *testing.T) {
	dir := t.TempDir()
	for i, sha := range []string{"a", "b", "c"} {
		if err := WritePassMarker(dir, sha, "main", "darwin", i+1, time.Second); err != nil {
			t.Fatal(err)
		}
		// Tiny stagger so the file timestamps differ.
		time.Sleep(10 * time.Millisecond)
	}
	markers, err := LatestPassMarkers(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 3 {
		t.Fatalf("len = %d", len(markers))
	}
	// Newest first by PassedAt.
	if markers[0].SHA != "c" || markers[2].SHA != "a" {
		t.Errorf("ordering wrong: %v", []string{markers[0].SHA, markers[1].SHA, markers[2].SHA})
	}
}

func TestMarkersDir(t *testing.T) {
	got := MarkersDir("/tmp/foo")
	want := filepath.Join("/tmp/foo", ".yaver-test-results", "markers")
	if got != want {
		t.Errorf("MarkersDir = %q, want %q", got, want)
	}
}
