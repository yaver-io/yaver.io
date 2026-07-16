package main

// feedback_p4_test.go — P4 tests. Drives the pure runFeedbackCreate /
// runFeedbackSpeak against a live FeedbackManager (baseDir under
// t.TempDir()) so the report round-trips through the real persistence
// layer. Frame fetch is behind a var seam so we don't need a booted sim.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinFeedbackHome swaps HOME to a per-test temp dir so
// NewFeedbackManager writes into an isolated ~/.yaver/feedback tree.
func pinFeedbackHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	orig, _ := os.LookupEnv("HOME")
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
	// Sanity: the reports dir doesn't exist yet.
	if _, err := os.Stat(filepath.Join(tmp, ".yaver", "feedback")); err == nil {
		t.Fatal("temp feedback dir shouldn't exist yet")
	}
	return tmp
}

func TestFeedbackCreate_RequiresSurface(t *testing.T) {
	pinFeedbackHome(t)
	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}
	got := runFeedbackCreate(fm, feedbackCreateArgs{})
	if got["ok"] != false {
		t.Fatalf("empty surface should fail, got %+v", got)
	}
}

func TestFeedbackCreate_PersistsReport(t *testing.T) {
	pinFeedbackHome(t)
	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}
	got := runFeedbackCreate(fm, feedbackCreateArgs{
		Surface:    "watch",
		Transcript: "the crown scroll misses the target when I pass 30% velocity",
		AppName:    "Talos",
		Platform:   "ios",
		Model:      "Apple Watch Series 10",
	})
	if got["ok"] != true {
		t.Fatalf("create failed: %+v", got)
	}
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatal("id missing on success payload")
	}
	// Round-trip: list should return the report.
	summaries := fm.ListFeedback()
	if len(summaries) == 0 {
		t.Fatal("list returned no reports after create")
	}
	found := false
	for _, s := range summaries {
		if s.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("report %q missing from summaries %+v", id, summaries)
	}
}

func TestFeedbackCreate_AutoAttachesFrameFromSession(t *testing.T) {
	pinFeedbackHome(t)
	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}
	// Stub the frame fetch so we don't need a live agent.
	orig := feedbackFrameFetch
	feedbackFrameFetch = func(sessionID string) ([]byte, int, error) {
		if sessionID != "rr_test_frame" {
			t.Fatalf("frame fetched with wrong id %q", sessionID)
		}
		return []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}, 200, nil // JPEG-ish magic
	}
	defer func() { feedbackFrameFetch = orig }()

	got := runFeedbackCreate(fm, feedbackCreateArgs{
		Surface: "tv", Transcript: "menu scroll lag", ScreenshotSessionID: "rr_test_frame",
	})
	if got["ok"] != true {
		t.Fatalf("create failed: %+v", got)
	}
	if got["attachments"] != 1 {
		t.Fatalf("attachments = %v, want 1 (frame auto-attach)", got["attachments"])
	}
}

func TestFeedbackSpeak_SummarizesQueueThroughVoicePipe(t *testing.T) {
	pinFeedbackHome(t)
	fm, _ := NewFeedbackManager()
	bb, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	// Seed 2 reports.
	_ = runFeedbackCreate(fm, feedbackCreateArgs{Surface: "phone", Transcript: "login button too small"})
	_ = runFeedbackCreate(fm, feedbackCreateArgs{Surface: "watch", Transcript: "crown skips ahead"})

	sess := bb.GetOrCreateSession("tv-1", "tvos", "")
	ch := sess.SubscribeCommands()
	defer sess.UnsubscribeCommands(ch)

	got := runFeedbackSpeak(fm, bb, feedbackSpeakArgs{Device: "tv-1"})
	if got["ok"] != true {
		t.Fatalf("speak failed: %+v", got)
	}
	select {
	case cmd := <-ch:
		if cmd.Command != "voice_speak" {
			t.Fatalf("tv received %q, want voice_speak", cmd.Command)
		}
		text, _ := cmd.Data["text"].(string)
		if !strings.Contains(text, "feedback") {
			t.Fatalf("spoken text missing 'feedback': %q", text)
		}
	default:
		t.Fatal("voice_speak did not reach the client")
	}
}

func TestFeedbackSpeak_EmptyQueueIsQuiet(t *testing.T) {
	pinFeedbackHome(t)
	fm, _ := NewFeedbackManager()
	bb, _ := NewBlackBoxManager()
	got := runFeedbackSpeak(fm, bb, feedbackSpeakArgs{})
	if got["ok"] != true {
		t.Fatalf("empty queue should still return ok, got %+v", got)
	}
	if _, hasNote := got["note"]; !hasNote {
		t.Fatalf("expected a 'nothing to summarise' note, got %+v", got)
	}
}
