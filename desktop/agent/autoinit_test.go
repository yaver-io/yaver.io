package main

// autoinit_test.go — pure-function tests for the autoinit machinery.
// Stays away from spawning Claude or hitting the daemon — those
// belong in a separate end-to-end workflow.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- autoinitMerge -----------------------------------------------------------

func TestAutoinitMergeFreshFile(t *testing.T) {
	got := autoinitMerge("", "## Generated body")
	if !strings.Contains(got, "# Project init") {
		t.Error("missing header")
	}
	if !strings.Contains(got, autoinitGenStart) || !strings.Contains(got, autoinitGenEnd) {
		t.Error("missing generated markers")
	}
	if !strings.Contains(got, autoinitHistoryStart) || !strings.Contains(got, autoinitHistoryEnd) {
		t.Error("missing history markers")
	}
	if !strings.Contains(got, "## Generated body") {
		t.Error("generated body missing")
	}
}

func TestAutoinitMergeReplacesGenerated(t *testing.T) {
	original := "# My header\n\n" +
		"User-written prose stays put.\n\n" +
		autoinitGenStart + "\nold body\n" + autoinitGenEnd + "\n\n" +
		"More user prose.\n\n" +
		autoinitHistoryStart + "\nhistory entries\n" + autoinitHistoryEnd
	out := autoinitMerge(original, "shiny new body")
	if !strings.Contains(out, "User-written prose stays put.") {
		t.Error("dropped user prose before generated section")
	}
	if !strings.Contains(out, "More user prose.") {
		t.Error("dropped user prose after generated section")
	}
	if !strings.Contains(out, "history entries") {
		t.Error("dropped existing history entries")
	}
	if strings.Contains(out, "old body") {
		t.Error("old generated body should have been replaced")
	}
	if !strings.Contains(out, "shiny new body") {
		t.Error("new generated body missing")
	}
}

func TestAutoinitMergeAppendsToManualFile(t *testing.T) {
	manual := "# Custom header\n\nHand-written notes."
	out := autoinitMerge(manual, "AI body")
	if !strings.Contains(out, "Custom header") || !strings.Contains(out, "Hand-written notes.") {
		t.Error("manual content not preserved")
	}
	if !strings.Contains(out, autoinitGenStart) || !strings.Contains(out, "AI body") {
		t.Error("generated section not appended")
	}
}

// --- autoinitAppendHistory ---------------------------------------------------

func TestAutoinitAppendHistoryInsertsAtTop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, autoinitFile)
	body := "# h\n\n" +
		autoinitGenStart + "\nbody\n" + autoinitGenEnd + "\n\n" +
		autoinitHistoryStart + "\nold entry\n" + autoinitHistoryEnd + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	autoinitAppendHistory(dir, "shiny new entry")
	got, _ := os.ReadFile(path)
	gotStr := string(got)
	if !strings.Contains(gotStr, "shiny new entry") {
		t.Error("new entry not appended")
	}
	if !strings.Contains(gotStr, "old entry") {
		t.Error("old entry lost")
	}
	// New entry should appear before old entry.
	newIdx := strings.Index(gotStr, "shiny new entry")
	oldIdx := strings.Index(gotStr, "old entry")
	if newIdx < 0 || oldIdx < 0 || newIdx >= oldIdx {
		t.Errorf("new entry should be above old entry; new=%d old=%d", newIdx, oldIdx)
	}
}

func TestAutoinitAppendHistoryNoFileNoop(t *testing.T) {
	dir := t.TempDir()
	autoinitAppendHistory(dir, "anything") // must not panic / create
	if _, err := os.Stat(filepath.Join(dir, autoinitFile)); !os.IsNotExist(err) {
		t.Error("append should NOT create the file when missing")
	}
}

func TestAutoinitAppendHistoryNoMarkersNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, autoinitFile)
	original := "# just markdown, no markers"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	autoinitAppendHistory(dir, "ignored")
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Error("file without history markers should not be modified")
	}
}

// --- autoinitContextBlock ----------------------------------------------------

func TestAutoinitContextBlockEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if got := autoinitContextBlock(dir); got != "" {
		t.Errorf("empty dir should return empty context, got %q", got)
	}
}

func TestAutoinitContextBlockPicksUpAllThree(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, autoinitFile), []byte("init body"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("conventions body"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "remained.md"), []byte("- [ ] item"), 0o644)
	got := autoinitContextBlock(dir)
	for _, want := range []string{
		"CACHED PROJECT CONTEXT",
		"init body",
		"conventions body",
		"- [ ] item",
		"END CACHED CONTEXT",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in context block:\n%s", want, got)
		}
	}
}

func TestAutoinitContextBlockTruncatesLargeFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 20*1024)
	_ = os.WriteFile(filepath.Join(dir, autoinitFile), []byte(big), 0o644)
	got := autoinitContextBlock(dir)
	if !strings.Contains(got, "(truncated)") {
		t.Error("oversize file should be truncated with marker")
	}
	// Should NOT contain the entire 20 KB blob.
	if strings.Count(got, "x") > 8*1024+200 {
		t.Errorf("file did not get capped at 8 KB; saw %d x's", strings.Count(got, "x"))
	}
}

// --- replaceBetween ----------------------------------------------------------

func TestReplaceBetween(t *testing.T) {
	in := "before <s>middle</e> after"
	got := replaceBetween(in, "<s>", "</e>", "<s>NEW</e>")
	if got != "before <s>NEW</e> after" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceBetweenMissingTagsReturnsInput(t *testing.T) {
	in := "no tags here"
	if got := replaceBetween(in, "<s>", "</e>", "X"); got != in {
		t.Errorf("missing tags should return input, got %q", got)
	}
}

// --- computeAutoInitStatus ---------------------------------------------------

func TestComputeAutoInitStatusMissing(t *testing.T) {
	dir := t.TempDir()
	st := computeAutoInitStatus(dir)
	if st.Done {
		t.Error("missing init.md should report Done=false")
	}
	if !strings.HasSuffix(st.Path, autoinitFile) {
		t.Errorf("path should end with %s, got %q", autoinitFile, st.Path)
	}
}

func TestComputeAutoInitStatusPresent(t *testing.T) {
	dir := t.TempDir()
	body := autoinitGenStart + "\nx\n" + autoinitGenEnd + "\n" +
		autoinitHistoryStart + "\n" + autoinitHistoryEnd
	_ = os.WriteFile(filepath.Join(dir, autoinitFile), []byte(body), 0o644)
	st := computeAutoInitStatus(dir)
	if !st.Done {
		t.Error("present init.md should report Done=true")
	}
	if !st.HasGenSec {
		t.Error("should detect generated section")
	}
	if !st.HasHistory {
		t.Error("should detect history section")
	}
	if st.Bytes == 0 {
		t.Error("bytes should be > 0")
	}
	if st.UpdatedAt == "" {
		t.Error("updated_at should be set")
	}
}
