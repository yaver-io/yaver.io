package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRecorder(t *testing.T, session string) *flightRecorder {
	t.Helper()
	return newFlightRecorder(filepath.Join(t.TempDir(), "flight-recorder.json"), session)
}

// The whole point of the black box: a session that never wrote `shutdown` did
// not stop gracefully, and the next boot must say so.
func TestFlightRecorderInfersUncleanStop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flight-recorder.json")

	dead := newFlightRecorder(path, "session-that-dies")
	if err := dead.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record boot: %v", err)
	}
	// No shutdown record — the box lost power here.

	next := newFlightRecorder(path, "session-after-reboot")
	ev := next.detectUncleanStop("macOS shutdown cause -128: power loss or hard reset")
	if ev == nil {
		t.Fatal("expected an unclean_stop verdict for a session that never wrote shutdown")
	}
	if ev.Kind != flightKindUncleanStop {
		t.Errorf("kind = %q, want %q", ev.Kind, flightKindUncleanStop)
	}
	// The verdict belongs to the session that died, not the one observing it —
	// otherwise the timeline blames the wrong run.
	if ev.Session != "session-that-dies" {
		t.Errorf("session = %q, want the dead session", ev.Session)
	}
	if !strings.Contains(ev.Detail, "power loss") {
		t.Errorf("detail = %q, want the OS cause carried through", ev.Detail)
	}
}

func TestFlightRecorderCleanShutdownIsNotUnclean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flight-recorder.json")

	prev := newFlightRecorder(path, "clean-session")
	if err := prev.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record boot: %v", err)
	}
	if err := prev.record(flightKindShutdown, "agent stopped on SIGTERM"); err != nil {
		t.Fatalf("record shutdown: %v", err)
	}

	next := newFlightRecorder(path, "next-session")
	if ev := next.detectUncleanStop(""); ev != nil {
		t.Fatalf("clean shutdown must not be reported as unclean, got %+v", ev)
	}
}

// A boot that re-runs detection (or a restarted probe) must not accuse itself.
func TestFlightRecorderDoesNotAccuseOwnSession(t *testing.T) {
	r := testRecorder(t, "same-session")
	if err := r.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if ev := r.detectUncleanStop(""); ev != nil {
		t.Fatalf("a recorder must not report its own live session as unclean, got %+v", ev)
	}
}

func TestFlightRecorderEmptyBufferHasNoVerdict(t *testing.T) {
	r := testRecorder(t, "first-ever-boot")
	if ev := r.detectUncleanStop("cause"); ev != nil {
		t.Fatalf("a first boot has no prior session to judge, got %+v", ev)
	}
}

// The write budget is a hard promise, not a hint.
func TestFlightRecorderCapsBuffer(t *testing.T) {
	r := testRecorder(t, "chatty")
	for i := 0; i < flightRecorderMaxEvents+25; i++ {
		if err := r.record(flightKindBoot, "agent started"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	events, err := r.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != flightRecorderMaxEvents {
		t.Errorf("buffer holds %d events, want the cap %d", len(events), flightRecorderMaxEvents)
	}
}

// Detail is a bounded cause, never a log dump — an OS log line must not become
// a data leak or an unbounded Convex row.
func TestFlightRecorderTruncatesDetail(t *testing.T) {
	r := testRecorder(t, "verbose")
	if err := r.record(flightKindSleep, strings.Repeat("x", flightDetailMaxLen*3)); err != nil {
		t.Fatalf("record: %v", err)
	}
	events, err := r.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := len(events[0].Detail); got > flightDetailMaxLen {
		t.Errorf("detail length %d exceeds cap %d", got, flightDetailMaxLen)
	}
}

// A corrupt buffer must never wedge the agent or block new history.
func TestFlightRecorderSurvivesCorruptBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flight-recorder.json")
	if err := os.WriteFile(path, []byte("{not json at all"), 0o600); err != nil {
		t.Fatalf("seed corrupt buffer: %v", err)
	}
	r := newFlightRecorder(path, "post-corruption")
	if err := r.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record after corruption: %v", err)
	}
	events, err := r.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 || events[0].Kind != flightKindBoot {
		t.Errorf("expected recording to resume after corruption, got %+v", events)
	}
}

// The buffer is the thing that has to survive a power cut, so it must be valid
// JSON on disk at all times.
func TestFlightRecorderWritesValidJSON(t *testing.T) {
	r := testRecorder(t, "s")
	if err := r.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record: %v", err)
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var events []FlightEvent
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("buffer on disk is not valid JSON: %v", err)
	}
}

// The write budget lives here: without a watermark the agent would re-ship its
// whole buffer on every heartbeat forever.
func TestFlightRecorderOnlySendsUnsyncedEvents(t *testing.T) {
	r := testRecorder(t, "s1")
	if err := r.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record: %v", err)
	}
	first := r.unsynced()
	if len(first) != 1 {
		t.Fatalf("expected the new event to be unsynced, got %d", len(first))
	}

	r.markSynced(first)
	if again := r.unsynced(); len(again) != 0 {
		t.Errorf("a synced event must never be re-sent; got %d events", len(again))
	}
}

func TestFlightRecorderSendsEventsRecordedAfterSync(t *testing.T) {
	r := testRecorder(t, "s1")
	if err := r.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record boot: %v", err)
	}
	r.markSynced(r.unsynced())

	// Timestamps are second-resolution RFC3339, so a later event needs a
	// distinct second for the strictly-after watermark to see it.
	newer := FlightEvent{Session: "s1", Kind: flightKindShutdown, Detail: "agent stopped on terminated", At: time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)}
	existing, err := r.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := r.write(append(existing, newer)); err != nil {
		t.Fatalf("write: %v", err)
	}

	pending := r.unsynced()
	if len(pending) != 1 || pending[0].Kind != flightKindShutdown {
		t.Fatalf("expected only the post-sync event, got %+v", pending)
	}
}

// A failed heartbeat must re-send rather than lose the record of why a box died.
func TestFlightRecorderResendsWhenSyncNeverConfirmed(t *testing.T) {
	r := testRecorder(t, "s1")
	if err := r.record(flightKindBoot, "agent started"); err != nil {
		t.Fatalf("record: %v", err)
	}
	_ = r.unsynced() // heartbeat attempted...
	// ...and failed, so markSynced is never called.
	if again := r.unsynced(); len(again) != 1 {
		t.Errorf("an unconfirmed batch must be re-sent, got %d events", len(again))
	}
}

func TestDescribeShutdownCause(t *testing.T) {
	for code, want := range map[string]string{
		"-128": "power loss",
		"5":    "clean OS shutdown",
		"-60":  "watchdog",
	} {
		if got := describeShutdownCause(code); !strings.Contains(got, want) {
			t.Errorf("describeShutdownCause(%q) = %q, want it to mention %q", code, got, want)
		}
	}
	if got := describeShutdownCause("9999"); strings.TrimSpace(got) == "" {
		t.Error("an unknown code must still describe itself rather than return empty")
	}
}

// Flight events ride the heartbeat HTTP route rather than convexSyncer.
// callMutation, so convex_privacy_test.go's sweep over recorded mutations does
// not see them. This runs the SAME two assertions against the real wire payload
// so the black box is held to the identical contract.
func TestFlightEventPayloadObeysConvexPrivacyContract(t *testing.T) {
	// A deliberately hostile detail: an OS probe on a real box could plausibly
	// echo a home directory back at us, and that must never reach Convex.
	payload := []flightEventPayload{
		{Session: "s1", Kind: flightKindBoot, Detail: "agent started", AtMs: 1700000000000},
		{Session: "s1", Kind: flightKindUncleanStop, Detail: "macOS shutdown cause -128: power loss or hard reset", AtMs: 1700000001000},
	}
	raw, err := json.Marshal(map[string]interface{}{"deviceId": "test-device", "flightEvents": payload})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var args map[string]interface{}
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec := recordedMutation{Path: "devices:heartbeat(flightEvents)", Args: args}
	assertNoForbiddenFields(t, rec)
	assertNoAbsolutePaths(t, rec)
}

// A path that leaked into `detail` must be caught by the contract, not shrugged
// off — this proves the fence above actually bites rather than passing
// vacuously.
func TestFlightPrivacyFenceCatchesAPathLeak(t *testing.T) {
	args := map[string]interface{}{
		"flightEvents": []interface{}{
			map[string]interface{}{"session": "s", "kind": "boot", "detail": "/Users/pokayoke/Workspace"},
		},
	}
	fake := &testing.T{}
	assertNoAbsolutePaths(fake, recordedMutation{Path: "devices:heartbeat", Args: args})
	if !fake.Failed() {
		t.Error("the privacy fence did not catch an absolute path in a flight event detail")
	}
}
