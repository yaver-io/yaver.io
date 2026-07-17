package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempRecapDir points recap storage at a throwaway dir. Recaps live under
// ~/.yaver by default and tests must never touch a real one.
func withTempRecapDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	recapMu.Lock()
	prev := recapBaseDir
	recapBaseDir = dir
	recapMu.Unlock()
	t.Cleanup(func() {
		recapMu.Lock()
		recapBaseDir = prev
		recapMu.Unlock()
	})
	return dir
}

func frameAt(idx int, display int, capturedAt, activeTo int64) ScreenlogFrame {
	return ScreenlogFrame{
		Idx:        idx,
		Display:    display,
		CapturedAt: capturedAt,
		ActiveToMs: activeTo,
		File:       fmt.Sprintf("%06d_d%d_%d.jpg", idx, display, capturedAt),
	}
}

// --- pacing ----------------------------------------------------------------

// The whole point of paceFrames: a screen that sat for an hour must read as
// longer than one that flashed for two seconds, WITHOUT eating the recap.
// Linear time would give it 1800x the screen time; uniform time would give it
// 1x and erase the difference. Both are lies.
func TestPaceFrames_compressesDwellWithoutErasingIt(t *testing.T) {
	base := int64(1_000_000)
	frames := []ScreenlogFrame{
		frameAt(0, 0, base, base+2_000),           // 2s dwell
		frameAt(1, 0, base+2_000, base+3_602_000), // 1h dwell
	}
	// Target 4s for 2 frames: a realistic per-frame share. With a 60s target
	// both frames would exceed recapMaxFrameSec and clamp flat — correct
	// behaviour (2 frames cannot honestly fill a minute), but it tells us
	// nothing about the weighting, which is what this test is about.
	durs, total := paceFrames(frames, base+3_602_000, 4)
	if len(durs) != 2 {
		t.Fatalf("want 2 durations, got %d", len(durs))
	}
	if durs[1] <= durs[0] {
		t.Errorf("the hour-long screen must get more time than the 2s one: got %.2f vs %.2f", durs[1], durs[0])
	}
	ratio := durs[1] / durs[0]
	if ratio > 20 {
		t.Errorf("dwell ratio %.1fx is too close to linear (1800x) — log compression is not working", ratio)
	}
	if total <= 0 {
		t.Errorf("total duration must be positive, got %.2f", total)
	}
}

func TestPaceFrames_clampsBothTails(t *testing.T) {
	base := int64(1_000_000)
	var frames []ScreenlogFrame
	// 200 frames of 1s each: the per-frame share of a 60s target is 0.3s,
	// which is fine, but with 2000 frames it would fall under the floor.
	for i := 0; i < 2000; i++ {
		at := base + int64(i)*1000
		frames = append(frames, frameAt(i, 0, at, at+1000))
	}
	durs, _ := paceFrames(frames, base+2_000_000, 60)
	for i, d := range durs {
		if d < recapMinFrameSec-1e-9 {
			t.Fatalf("frame %d under floor: %.4f < %.4f", i, d, recapMinFrameSec)
		}
		if d > recapMaxFrameSec+1e-9 {
			t.Fatalf("frame %d over ceiling: %.4f > %.4f", i, d, recapMaxFrameSec)
		}
	}
}

// A frame whose interval was never closed (the last one for a display —
// screenlog closes it at StoppedAt) must not produce a negative or zero
// duration.
func TestPaceFrames_handlesUnclosedFinalFrame(t *testing.T) {
	base := int64(1_000_000)
	frames := []ScreenlogFrame{
		frameAt(0, 0, base, base+5_000),
		{Idx: 1, Display: 0, CapturedAt: base + 5_000, ActiveToMs: 0, File: "x.jpg"}, // unclosed
	}
	durs, total := paceFrames(frames, base+30_000, 60)
	for i, d := range durs {
		if d <= 0 {
			t.Fatalf("frame %d got non-positive duration %.4f", i, d)
		}
	}
	if total <= 0 {
		t.Fatalf("total must be positive, got %.4f", total)
	}
}

func TestPaceFrames_emptyInput(t *testing.T) {
	durs, total := paceFrames(nil, 0, 60)
	if durs != nil || total != 0 {
		t.Fatalf("empty input must yield no durations, got %v / %.2f", durs, total)
	}
}

// --- frame selection -------------------------------------------------------

// Trap 1 from recap_build.go: every display's frames share ONE directory.
// Selecting without filtering splices two monitors into one video.
func TestSelectRecapFrames_neverMixesDisplays(t *testing.T) {
	base := int64(1_000_000)
	sess := &ScreenlogSession{Frames: []ScreenlogFrame{
		frameAt(0, 0, base, base+1000),
		frameAt(1, 1, base+100, base+1100), // other monitor, interleaved
		frameAt(2, 0, base+1000, base+2000),
		frameAt(3, 1, base+1100, base+2100),
	}}
	got := selectRecapFrames(sess, 0, 0, 0)
	if len(got) != 2 {
		t.Fatalf("want 2 frames for display 0, got %d", len(got))
	}
	for _, f := range got {
		if f.Display != 0 {
			t.Errorf("display %d leaked into a display-0 recap", f.Display)
		}
	}
}

func TestSelectRecapFrames_windowScopesToTheRun(t *testing.T) {
	base := int64(1_000_000)
	sess := &ScreenlogSession{Frames: []ScreenlogFrame{
		frameAt(0, 0, base, base+1000),          // before
		frameAt(1, 0, base+5_000, base+6_000),   // inside
		frameAt(2, 0, base+50_000, base+51_000), // after
	}}
	got := selectRecapFrames(sess, 0, base+2_000, base+10_000)
	if len(got) != 1 || got[0].Idx != 1 {
		t.Fatalf("window must select exactly the in-run frame, got %d frames", len(got))
	}
}

// EphemeralFrames mode keeps the activity trace but discards the images.
// Those frames have no File and must not reach ffmpeg.
func TestSelectRecapFrames_skipsFramesWithNoImage(t *testing.T) {
	base := int64(1_000_000)
	sess := &ScreenlogSession{Frames: []ScreenlogFrame{
		{Idx: 0, Display: 0, CapturedAt: base, File: ""},
		frameAt(1, 0, base+1000, base+2000),
	}}
	got := selectRecapFrames(sess, 0, 0, 0)
	if len(got) != 1 || got[0].Idx != 1 {
		t.Fatalf("ephemeral (image-less) frames must be skipped, got %d", len(got))
	}
}

// --- concat list -----------------------------------------------------------

// The concat demuxer ignores the FINAL entry's duration, so the last file must
// be repeated or the video ends a frame early. This is the documented ffmpeg
// workaround and it is easy to "clean up" by mistake.
func TestWriteConcatList_repeatsFinalFrame(t *testing.T) {
	dir := t.TempDir()
	frames := []ScreenlogFrame{
		frameAt(0, 0, 1000, 2000),
		frameAt(1, 0, 2000, 3000),
	}
	list := filepath.Join(dir, "frames.txt")
	if err := writeConcatList(list, "/frames", frames, []float64{0.5, 1.25}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(list)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if got := strings.Count(s, frames[1].File); got != 2 {
		t.Errorf("final frame must appear twice (concat demuxer quirk), appeared %d times:\n%s", got, s)
	}
	if !strings.Contains(s, "duration 0.500") || !strings.Contains(s, "duration 1.250") {
		t.Errorf("per-frame durations missing — pacing would be discarded:\n%s", s)
	}
}

// A home directory can contain a quote. The frame names can't, but the dir can.
func TestFfmpegConcatEscape_quotesInPath(t *testing.T) {
	got := ffmpegConcatEscape("/Users/o'brien/.yaver/screenlog/s1/000001_d0_1.jpg")
	if strings.Contains(got, "/o'brien/") {
		t.Errorf("bare quote survived escaping — the concat list would break: %s", got)
	}
	if !strings.Contains(got, `'\''`) {
		t.Errorf("want shell-style quote escape, got %s", got)
	}
}

// --- ids and tags ----------------------------------------------------------

// recapValidID is the ONLY gate between a URL path and the filesystem.
func TestRecapValidID_rejectsTraversal(t *testing.T) {
	bad := []string{
		"", "r_", "..", "../../etc/passwd", "r_../../etc/passwd",
		"r_ABCDEF", // uppercase isn't produced by randomHex
		"r_zzzz",   // not hex
		"c_abc123", // a clip id, not a recap id
		"r_abc/def",
		"r_abc.def",
		strings.Repeat("r_a", 40),
	}
	for _, id := range bad {
		if recapValidID(id) {
			t.Errorf("recapValidID(%q) = true, want false", id)
		}
	}
	if !recapValidID("r_0123456789abcdef") {
		t.Error("a well-formed r_<hex> id must be accepted")
	}
	if !recapValidID(newRecapID()) {
		t.Error("newRecapID must produce an id that passes its own validator")
	}
}

// Tags are user-authored and are the one recap field a UI would plausibly sync.
// Free text there would leak a path or a repo name the moment it did.
func TestRecapValidTag_rejectsFreeText(t *testing.T) {
	bad := []string{"", "Nightly", "fix /Users/bob/api", "tag with spaces", "../x", strings.Repeat("a", 33)}
	for _, tag := range bad {
		if recapValidTag(tag) {
			t.Errorf("recapValidTag(%q) = true, want false", tag)
		}
	}
	for _, tag := range []string{RecapTagNightly, RecapTagFailure, RecapTagUIDiff, RecapTagManual, "my-custom-cut"} {
		if !recapValidTag(tag) {
			t.Errorf("recapValidTag(%q) = false, want true", tag)
		}
	}
}

func TestRecapDir_rejectsInvalidID(t *testing.T) {
	withTempRecapDir(t)
	if _, err := recapDir("../escape"); err == nil {
		t.Fatal("recapDir must reject a traversal id")
	}
}

// --- storage ---------------------------------------------------------------

func TestRecapStorage_roundTripAndFilter(t *testing.T) {
	withTempRecapDir(t)

	mk := func(autorun, tag string, createdAt int64) *RecapRecord {
		r := &RecapRecord{
			ID: newRecapID(), AutorunID: autorun, Tag: tag, Slot: "task:doer",
			Status: RecapStatusReady, CreatedAt: createdAt, SizeBytes: 1000,
		}
		if err := saveRecap(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a1 := mk("autorun-1", RecapTagNightly, 1000)
	mk("autorun-1", RecapTagFailure, 2000)
	mk("autorun-2", RecapTagNightly, 3000)

	got, err := loadRecap(a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AutorunID != "autorun-1" || got.Tag != RecapTagNightly {
		t.Errorf("round-trip lost fields: %+v", got)
	}

	// The core of the data model: one run, many tagged cuts.
	byRun, err := listRecaps(RecapFilter{AutorunID: "autorun-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byRun) != 2 {
		t.Fatalf("autorun-1 must have 2 recaps, got %d", len(byRun))
	}
	if byRun[0].CreatedAt < byRun[1].CreatedAt {
		t.Error("listings must be newest-first")
	}

	byTag, err := listRecaps(RecapFilter{AutorunID: "autorun-1", Tag: RecapTagFailure})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTag) != 1 || byTag[0].Tag != RecapTagFailure {
		t.Fatalf("tag filter failed: %+v", byTag)
	}

	if err := deleteRecap(a1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecap(a1.ID); err == nil {
		t.Error("deleted recap must not load")
	}
}

// A half-written or corrupt recap must not break the whole listing.
func TestListRecaps_survivesCorruptEntry(t *testing.T) {
	dir := withTempRecapDir(t)
	good := &RecapRecord{ID: newRecapID(), Tag: RecapTagManual, Status: RecapStatusReady, CreatedAt: 1}
	if err := saveRecap(good); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "r_deadbeef01")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "recap.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := listRecaps(RecapFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != good.ID {
		t.Fatalf("corrupt entry must be skipped, not fatal: got %d recaps", len(got))
	}
}

// --- retention -------------------------------------------------------------
//
// ~/.yaver/clips has no cap, no retention, and no pruning anywhere in the
// tree — it grows until the disk does. Recaps are generated unattended by the
// autorun hook, so they must be bounded or they'd be strictly worse.

func TestPruneRecaps_enforcesCountCap(t *testing.T) {
	withTempRecapDir(t)
	// Timestamps must be realistic unix-ms: CreatedAt is also what the age cap
	// reads, and a nominal `1000` is 1970, which every age cap deletes.
	now := time.Now().UnixMilli()
	for i := 0; i < 10; i++ {
		r := &RecapRecord{ID: newRecapID(), Tag: RecapTagManual, Status: RecapStatusReady,
			CreatedAt: now + int64(i), SizeBytes: 10}
		if err := saveRecap(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pruneRecaps(4, 1000, 30); err != nil {
		t.Fatal(err)
	}
	got, _ := listRecaps(RecapFilter{})
	if len(got) != 4 {
		t.Fatalf("count cap of 4 not enforced: %d remain", len(got))
	}
	// The survivors must be the NEWEST four.
	for _, r := range got {
		if r.CreatedAt < now+6 {
			t.Errorf("pruning evicted a newer recap (createdAt=%d) before an older one", r.CreatedAt)
		}
	}
}

func TestPruneRecaps_enforcesAgeAndBytes(t *testing.T) {
	withTempRecapDir(t)
	old := &RecapRecord{ID: newRecapID(), Tag: RecapTagManual, Status: RecapStatusReady,
		CreatedAt: time.Now().Add(-60 * 24 * time.Hour).UnixMilli(), SizeBytes: 10}
	fresh := &RecapRecord{ID: newRecapID(), Tag: RecapTagManual, Status: RecapStatusReady,
		CreatedAt: time.Now().UnixMilli(), SizeBytes: 10}
	for _, r := range []*RecapRecord{old, fresh} {
		if err := saveRecap(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pruneRecaps(100, 1000, 30); err != nil {
		t.Fatal(err)
	}
	got, _ := listRecaps(RecapFilter{})
	if len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("age cap must evict the 60-day-old recap: %d remain", len(got))
	}

	// Bytes cap: two 2 MB recaps with a 3 MB budget → one survives.
	withTempRecapDir(t)
	now := time.Now().UnixMilli()
	for i := 0; i < 2; i++ {
		r := &RecapRecord{ID: newRecapID(), Tag: RecapTagManual, Status: RecapStatusReady,
			CreatedAt: now + int64(i), SizeBytes: 2 * 1024 * 1024}
		if err := saveRecap(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pruneRecaps(100, 3, 30); err != nil {
		t.Fatal(err)
	}
	got, _ = listRecaps(RecapFilter{})
	if len(got) != 1 {
		t.Fatalf("byte cap of 3MB against 2x2MB must leave 1, got %d", len(got))
	}
}

// A build in flight has no size yet and would look like a free eviction.
func TestPruneRecaps_neverEvictsABuildInFlight(t *testing.T) {
	withTempRecapDir(t)
	building := &RecapRecord{ID: newRecapID(), Tag: RecapTagManual, Status: RecapStatusBuilding,
		CreatedAt: time.Now().Add(-90 * 24 * time.Hour).UnixMilli()}
	if err := saveRecap(building); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneRecaps(1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecap(building.ID); err != nil {
		t.Fatal("a recap still building must never be pruned, however old it looks")
	}
}

// --- timeline --------------------------------------------------------------

func TestRecapTimeline_mapsWallClockToVideoTime(t *testing.T) {
	tl := &recapTimeline{
		wallMs:   []int64{1000, 2000, 3000},
		videoSec: []float64{0, 1, 2},
		totalSec: 3,
	}
	cases := []struct {
		wall int64
		want float64
	}{
		{500, 0},  // before the first frame
		{1000, 0}, // exactly the first
		{2500, 1}, // inside frame 2's span
		{9999, 3}, // past the end
	}
	for _, c := range cases {
		if got := tl.videoSecAt(c.wall); got != c.want {
			t.Errorf("videoSecAt(%d) = %.2f, want %.2f", c.wall, got, c.want)
		}
	}
	empty := &recapTimeline{}
	if got := empty.videoSecAt(123); got != 0 {
		t.Errorf("empty timeline must map to 0, got %.2f", got)
	}
}

// --- cue timing ------------------------------------------------------------

func TestTimeRecapCues_keepsCuesReadableAndInsideTheVideo(t *testing.T) {
	tl := &recapTimeline{
		wallMs:   []int64{0, 1000, 2000, 3000},
		videoSec: []float64{0, 10, 20, 30},
		totalSec: 40,
	}
	beats := []recapBeat{
		{Text: "opener"},
		{Text: "middle", WallMs: 1000},
		{Text: "later", WallMs: 1050}, // almost on top of the previous one
		{Text: "closer"},
	}
	cues := timeRecapCues(beats, nil, tl)
	if len(cues) == 0 {
		t.Fatal("want cues")
	}
	var prevEnd float64
	for i, c := range cues {
		if c.EndSec > tl.totalSec+1e-9 {
			t.Errorf("cue %d ends at %.2f, past the video's %.2f", i, c.EndSec, tl.totalSec)
		}
		if c.StartSec < prevEnd-1e-9 {
			t.Errorf("cue %d overlaps the previous one (%.2f < %.2f)", i, c.StartSec, prevEnd)
		}
		if c.EndSec-c.StartSec < 0.4 {
			t.Errorf("cue %d is %.2fs — too brief to read", i, c.EndSec-c.StartSec)
		}
		prevEnd = c.EndSec
	}
	if cues[0].StartSec != 0 {
		t.Errorf("the opener must start at 0, got %.2f", cues[0].StartSec)
	}
}

func TestTimeRecapCues_zeroLengthVideo(t *testing.T) {
	if got := timeRecapCues([]recapBeat{{Text: "x"}}, nil, &recapTimeline{}); got != nil {
		t.Fatalf("a zero-length video can hold no cues, got %v", got)
	}
}

// Regression: an episode anchored near the end of the video used to displace
// the closer entirely. The closer states the outcome — it is the one cue that
// must never be dropped, however crowded the tail gets.
func TestTimeRecapCues_lateEpisodeNeverDisplacesTheCloser(t *testing.T) {
	tl := &recapTimeline{
		wallMs:   []int64{0, 1000, 2000},
		videoSec: []float64{0, 2.0, 4.5},
		totalSec: 4.545,
	}
	beats := []recapBeat{
		{Text: "opener"},
		{Text: "an episode right at the end", WallMs: 2000}, // maps to 4.5 of 4.545
		{Text: "the gate failed, so nothing was kept"},
	}
	cues := timeRecapCues(beats, nil, tl)
	if len(cues) == 0 {
		t.Fatal("want cues")
	}
	last := cues[len(cues)-1]
	if last.Text != "the gate failed, so nothing was kept" {
		t.Fatalf("the closer must be the final cue, got %q", last.Text)
	}
	if last.EndSec > tl.totalSec+1e-9 {
		t.Errorf("closer ends past the video: %.3f > %.3f", last.EndSec, tl.totalSec)
	}
	if last.EndSec-last.StartSec < 1.0 {
		t.Errorf("closer is only %.2fs — too brief to read the outcome", last.EndSec-last.StartSec)
	}
	var prevEnd float64
	for i, c := range cues {
		if c.StartSec < prevEnd-1e-9 {
			t.Errorf("cue %d overlaps its predecessor (%.3f < %.3f)", i, c.StartSec, prevEnd)
		}
		prevEnd = c.EndSec
	}
}

// A single beat should fill the video rather than sit in a reserved tail.
func TestTimeRecapCues_singleBeatFillsTheVideo(t *testing.T) {
	cues := timeRecapCues([]recapBeat{{Text: "only"}}, nil, &recapTimeline{totalSec: 10})
	if len(cues) != 1 || cues[0].StartSec != 0 || cues[0].EndSec != 10 {
		t.Fatalf("want one cue spanning the video, got %+v", cues)
	}
}

// On a very short video the outcome outranks the tour.
func TestTimeRecapCues_shortVideoKeepsTheCloser(t *testing.T) {
	tl := &recapTimeline{wallMs: []int64{0}, videoSec: []float64{0}, totalSec: 1.0}
	cues := timeRecapCues([]recapBeat{{Text: "opener"}, {Text: "outcome"}}, nil, tl)
	if len(cues) == 0 {
		t.Fatal("want at least the closer")
	}
	if cues[len(cues)-1].Text != "outcome" {
		t.Errorf("the closer must survive a short video, got %+v", cues)
	}
}

// --- the honesty rule ------------------------------------------------------
//
// This is the most important test in the file.
//
// Per 3a32a4fc3: autorunReasonDone means "a line in the progress file said
// DONE", not that the work is finished. A runner once wrote "I did not run the
// full project gate, so this is NOT marked DONE" and the substring match ended
// the run as complete. A recap that narrates that as success is worse than no
// recap — it launders an unverified claim into a confident-sounding video.

func TestRecapCloser_neverClaimsSuccessFromAnUnverifiedDoneClaim(t *testing.T) {
	got := recapCloser(nil, RecapBuildOpts{
		FinishReason: autorunReasonDone,
		Commits:      0,
		Landed:       false,
	})
	low := strings.ToLower(got)
	for _, banned := range []string{"shipped", "successful", "completed successfully"} {
		if strings.Contains(low, banned) {
			t.Errorf("closer claimed %q off a DONE line with zero commits: %q", banned, got)
		}
	}
	if !strings.Contains(low, "said it was done") {
		t.Errorf("closer must mark the runner's claim AS a claim, got %q", got)
	}
	if !strings.Contains(low, "nothing was committed") {
		t.Errorf("closer must state the missing evidence, got %q", got)
	}
}

func TestRecapCloser_reportsRealOutcomes(t *testing.T) {
	cases := []struct {
		name string
		opts RecapBuildOpts
		want string
	}{
		{"commits landed", RecapBuildOpts{Commits: 3, Landed: true}, "landed 3 commits"},
		{"gate failed", RecapBuildOpts{FinishReason: autorunReasonGate}, "gate failed"},
		{"scope violation", RecapBuildOpts{FinishReason: autorunReasonScope}, "scope violation"},
		{"runner failed", RecapBuildOpts{FinishReason: autorunReasonRunner}, "runner failed"},
		{"stopped", RecapBuildOpts{FinishReason: autorunReasonStopped}, "you stopped this run"},
		{"resources", RecapBuildOpts{FinishReason: autorunReasonResources}, "resources"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.ToLower(recapCloser(nil, c.opts))
			if !strings.Contains(got, c.want) {
				t.Errorf("want %q in closer, got %q", c.want, got)
			}
		})
	}
}

func TestRecapCloser_mentionsHeals(t *testing.T) {
	got := strings.ToLower(recapCloser(nil, RecapBuildOpts{Commits: 1, Landed: true, Heals: 2}))
	if !strings.Contains(got, "self-healed 2 times") {
		t.Errorf("heals must be narrated — healing invisibly is how a loop looks fine for six hours: %q", got)
	}
}

func TestRecapCloser_callsOutRemainingPriorities(t *testing.T) {
	got := strings.ToLower(recapCloser(nil, RecapBuildOpts{
		Commits:             2,
		Landed:              true,
		FinishReason:        autorunReasonDone,
		Complete:            recapCompleteIncomplete,
		PriorityCount:       9,
		EvidencedPriorities: 1,
	}))
	if !strings.Contains(got, "only evidences 1") || !strings.Contains(got, "8 remain") {
		t.Fatalf("closer must say what landed and what remains, got %q", got)
	}
	if !strings.Contains(got, "said it was done") {
		t.Fatalf("closer must keep the done claim framed as a claim, got %q", got)
	}
}

// The prompt must carry the honesty rule to the model. Without it a summariser
// will happily turn "a line said DONE" into "successfully completed".
func TestBuildRecapPrompt_carriesTheHonestyRule(t *testing.T) {
	p := buildRecapPrompt(
		[]recapBeat{{Text: "a"}, {Text: "b"}},
		nil, nil,
		RecapBuildOpts{FinishReason: autorunReasonDone, Commits: 0},
	)
	for _, want := range []string{"HONESTY RULES", "claim, not a fact", "Do not invent"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q — the model would launder the claim", want)
		}
	}
	if !strings.Contains(p, "EXACTLY 2 lines") {
		t.Error("prompt must pin the line count; a different count breaks the timeline mapping")
	}
}

func TestRecapLanded_requiresEvidenceNotAClaim(t *testing.T) {
	if recapLanded(autorunRunSummary{FinishReason: autorunReasonDone, Commits: 0}) {
		t.Error("a DONE claim with no commits is not landed")
	}
	if recapLanded(autorunRunSummary{Commits: 2, FinalCommit: ""}) {
		t.Error("commits without a final commit is not landed")
	}
	if !recapLanded(autorunRunSummary{Commits: 2, FinalCommit: "abc123"}) {
		t.Error("commits + a final commit is the evidence that counts")
	}
}

// --- script polish parsing -------------------------------------------------

func TestParseRecapScriptJSON_toleratesWrapping(t *testing.T) {
	for _, in := range []string{
		`["a","b"]`,
		"```json\n[\"a\",\"b\"]\n```",
		"Sure! Here you go:\n[\"a\",\"b\"]\nHope that helps.",
	} {
		got, err := parseRecapScriptJSON(in)
		if err != nil {
			t.Fatalf("parse(%q): %v", in, err)
		}
		if len(got) != 2 || got[0] != "a" {
			t.Errorf("parse(%q) = %v", in, got)
		}
	}
	if _, err := parseRecapScriptJSON("not json at all"); err == nil {
		t.Error("want an error on a non-array reply")
	}
}

// --- WebVTT ----------------------------------------------------------------

func TestVTTTimestamp(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "00:00:00.000"},
		{1.5, "00:00:01.500"},
		{61.25, "00:01:01.250"},
		{3661.001, "01:01:01.001"},
		{-5, "00:00:00.000"},
	}
	for _, c := range cases {
		if got := vttTimestamp(c.in); got != c.want {
			t.Errorf("vttTimestamp(%.3f) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestWriteVTT_wellFormed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.vtt")
	cues := []RecapCue{
		{Text: "first line", StartSec: 0, EndSec: 2},
		{Text: "second\nline with a newline", StartSec: 2, EndSec: 4},
	}
	if err := writeVTT(path, cues); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.HasPrefix(s, "WEBVTT\n\n") {
		t.Errorf("VTT must begin with the WEBVTT magic:\n%s", s)
	}
	if !strings.Contains(s, "00:00:00.000 --> 00:00:02.000") {
		t.Errorf("missing cue timing:\n%s", s)
	}
	// A newline inside cue text would break the block structure and could
	// forge a cue.
	if strings.Contains(s, "second\nline") {
		t.Errorf("newlines inside cue text must be flattened:\n%s", s)
	}
}

// --- WAV -------------------------------------------------------------------

func TestWriteWAV_headerIsCorrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.wav")
	pcm := make([]byte, 8000) // 4000 samples
	if err := writeWAV(path, pcm, 16000); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 44+len(pcm) {
		t.Fatalf("want a 44-byte canonical header + payload, got %d bytes total", len(b))
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[36:40]) != "data" {
		t.Errorf("malformed RIFF/WAVE/data chunk ids: %q %q %q", b[0:4], b[8:12], b[36:40])
	}
	le32 := func(o int) uint32 {
		return uint32(b[o]) | uint32(b[o+1])<<8 | uint32(b[o+2])<<16 | uint32(b[o+3])<<24
	}
	le16 := func(o int) uint16 { return uint16(b[o]) | uint16(b[o+1])<<8 }
	if got := le32(4); got != uint32(36+len(pcm)) {
		t.Errorf("ChunkSize = %d, want %d", got, 36+len(pcm))
	}
	if got := le16(22); got != 1 {
		t.Errorf("NumChannels = %d, want 1 (mono)", got)
	}
	if got := le32(24); got != 16000 {
		t.Errorf("SampleRate = %d, want 16000", got)
	}
	if got := le32(28); got != 32000 { // 16000 * 1 * 16/8
		t.Errorf("ByteRate = %d, want 32000", got)
	}
	if got := le32(40); got != uint32(len(pcm)) {
		t.Errorf("data size = %d, want %d", got, len(pcm))
	}
}

func TestPcmDurationSec(t *testing.T) {
	// 16000 samples of 16-bit mono at 16kHz = 1 second.
	if got := pcmDurationSec(make([]byte, 32000), 16000); got != 1.0 {
		t.Errorf("pcmDurationSec = %.3f, want 1.0", got)
	}
	if got := pcmDurationSec(make([]byte, 100), 0); got != 0 {
		t.Errorf("a zero sample rate must not divide by zero, got %.3f", got)
	}
}

// --- autorun join ----------------------------------------------------------

func TestAutorunRunLooksBad(t *testing.T) {
	writeEvidenceTask := func(t *testing.T, priorities int, progress string) (string, string) {
		t.Helper()
		dir := t.TempDir()
		taskPath := filepath.Join(dir, "task.md")
		progressPath := filepath.Join(dir, "progress.md")
		var body strings.Builder
		body.WriteString("# Task\n\n")
		for i := 0; i < priorities; i++ {
			body.WriteString(fmt.Sprintf("## P%d — priority\n\n", i))
		}
		if err := os.WriteFile(taskPath, []byte(body.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(progressPath, []byte(progress), 0o600); err != nil {
			t.Fatal(err)
		}
		return taskPath, progressPath
	}

	incompleteTask, incompleteProgress := writeEvidenceTask(t, 9, `# Yaver autorun progress
## 2026-07-17T10:00:00Z

DOER REPORT (iteration 1, runner "codex"):

Implemented the first P0 increment.
`)
	completeTask, completeProgress := writeEvidenceTask(t, 2, `# Yaver autorun progress
## 2026-07-17T10:00:00Z

DOER REPORT (iteration 1, runner "codex"):

Implemented P0.

## 2026-07-17T10:05:00Z

DOER REPORT (iteration 2, runner "codex"):

Implemented P1.
`)

	cases := []struct {
		name string
		sess autorunSession
		want bool
	}{
		{"gate failed", autorunSession{Summary: autorunRunSummary{FinishReason: autorunReasonGate}}, true},
		{"runner failed", autorunSession{Summary: autorunRunSummary{FinishReason: autorunReasonRunner}}, true},
		{"scope violation", autorunSession{Summary: autorunRunSummary{FinishReason: autorunReasonScope}}, true},
		{"out of resources", autorunSession{Summary: autorunRunSummary{FinishReason: autorunReasonResources}}, true},
		{"status failed", autorunSession{Status: "failed"}, true},
		// The 3a32a4fc3 signature: claimed done, landed nothing.
		{"claimed done, no commits", autorunSession{Summary: autorunRunSummary{FinishReason: autorunReasonDone, Commits: 0}}, true},
		{"claimed done, priorities not all evidenced", autorunSession{
			Task:         incompleteTask,
			ProgressPath: incompleteProgress,
			Summary:      autorunRunSummary{FinishReason: autorunReasonDone, Commits: 2, FinalCommit: "a1"},
		}, true},
		{"converged with nothing", autorunSession{Summary: autorunRunSummary{FinishReason: autorunReasonConverged, Commits: 0}}, true},
		// Healthy runs — a heal that still landed work is not a failure.
		{"done with priorities evidenced", autorunSession{
			Status:       "completed",
			Task:         completeTask,
			ProgressPath: completeProgress,
			Summary:      autorunRunSummary{FinishReason: autorunReasonDone, Commits: 4, FinalCommit: "a1"},
		}, false},
		{"healed but landed", autorunSession{Status: "completed", Summary: autorunRunSummary{
			FinishReason: autorunReasonDone, Commits: 2, FinalCommit: "a1",
			Heals: []autorunHealEvent{{Kind: autorunHealDiskReclaim}},
		}}, false},
		{"converged with commits", autorunSession{Status: "completed", Summary: autorunRunSummary{FinishReason: autorunReasonConverged, Commits: 1, FinalCommit: "b2"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := autorunRunLooksBad(&c.sess); got != c.want {
				t.Errorf("autorunRunLooksBad = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRecapCompletion_ed9311d1aTwoCommitsAgainstNinePriorityTaskIsNotComplete(t *testing.T) {
	dir := t.TempDir()
	taskPath := filepath.Join(dir, "deploy-orchestration.md")
	progressPath := filepath.Join(dir, "deploy-orchestration-progress.md")
	task := "# Task\n\n"
	for i := 0; i < 9; i++ {
		task += fmt.Sprintf("## P%d — priority\n\n", i)
	}
	progress := `# Yaver autorun progress
## 2026-07-17T09:52:44Z

DOER REPORT (iteration 1, runner "codex"):

Implemented the first P0 increment.

## 2026-07-17T10:36:19Z

DOER REPORT (iteration 2, runner "codex"):

Implemented the next P0 slice.
`
	if err := os.WriteFile(taskPath, []byte(task), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(progressPath, []byte(progress), 0o600); err != nil {
		t.Fatal(err)
	}
	got := deriveRecapCompletion(taskPath, progressPath, true)
	if got.Complete != recapCompleteIncomplete {
		t.Fatalf("completion = %q, want %q", got.Complete, recapCompleteIncomplete)
	}
	if got.EvidencedPriorities != 1 || got.PriorityCount != 9 {
		t.Fatalf("evidence = %+v, want 1 of 9", got)
	}
}

func TestRecapBuild_ownerOnly(t *testing.T) {
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "support"}, OpsRequest{
		Machine: "local",
		Verb:    "recap_build",
		Payload: json.RawMessage(`{}`),
	})
	if res.OK {
		t.Fatalf("support caller should not be allowed to build recaps: %+v", res)
	}
	if res.Code != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized (%+v)", res.Code, res)
	}
}

// A slot is "<absolute task path>:<seat>". The label must never carry the path
// — it would leak the user's home-dir username into any UI payload.
func TestRecapSlotLabel_dropsThePath(t *testing.T) {
	got := recapSlotLabel("/Users/somebody/Workspace/yaver.io/docs/tasks/my-task.md:doer")
	if strings.Contains(got, "/Users/") || strings.Contains(got, "somebody") {
		t.Errorf("slot label leaked a path: %q", got)
	}
	if got != "my-task:doer" {
		t.Errorf("recapSlotLabel = %q, want %q", got, "my-task:doer")
	}
}

// The hook must be a no-op unless explicitly enabled. Encoding costs CPU on a
// box that is often mid-build, disk on a loop that already reclaims caches to
// stay above its floor, and (with narration) real inference tokens.
func TestRecapConfig_autoIsOffByDefault(t *testing.T) {
	if defaultRecapConfig().AutoOnAutorun {
		t.Error("AutoOnAutorun must default to false — a recap spends CPU, disk, and tokens")
	}
	if !defaultRecapConfig().FailureCut {
		t.Error("FailureCut should default on: once recaps are enabled, the failure cut is the one worth watching")
	}
}

func TestRecapConfig_normalizeClampsNonsense(t *testing.T) {
	c := RecapConfig{TargetSec: -5, MaxWidth: 99999, MaxCount: -1, MaxMB: 0, MaxDays: -3}
	c.normalize()
	d := defaultRecapConfig()
	if c.TargetSec != d.TargetSec || c.MaxWidth != d.MaxWidth {
		t.Errorf("normalize failed to clamp: %+v", c)
	}
	if c.MaxCount <= 0 || c.MaxMB <= 0 || c.MaxDays <= 0 {
		t.Errorf("retention bounds must never normalize to unbounded: %+v", c)
	}
}

// --- privacy ---------------------------------------------------------------

// TestRecapConvexPayload_isCounterOnly mirrors TestVibePreviewClipPayload_
// isCounterOnly. A recap is task output rendered to pixels, and its script is
// task output rendered to prose — CLAUDE.md forbids both in Convex, and the
// existing clip guard already bans `summaryText` by name for exactly this
// reason. Convex may hold a pointer and counters; resolution goes to the box.
//
// This test is written AHEAD of any syncer, deliberately — the same way the
// vibe-preview guards were. If someone later wires a recap syncer and reaches
// for the obvious fields, this fails first.
func TestRecapConvexPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	convexMutationRecorder("agentSync:recordRecap", map[string]interface{}{
		"deviceId":    "test-device",
		"recapId":     "r_abc123",
		"autorunId":   "autorun-1",
		"tag":         RecapTagNightly,
		"durationSec": 74.5,
		"sizeBytes":   1843200,
		"frames":      210,
		"commits":     3,
		"landed":      true,
		"complete":    recapCompleteUnknown,
		"createdAt":   1714000000,
	})

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	assertNoAbsolutePaths(t, rec)
	assertNoUsernameLeak(t, rec, "kivanccakmak-private-dir")

	// Belt and braces: the recap-specific temptations. `title` and `cues` are
	// prose about the user's work; `videoPath` is a path; `script` is the
	// narration. None may ever ride along.
	for _, forbidden := range []string{"videoPath", "posterBytes", "videoBlob", "script", "cues", "subtitles", "narration", "workDir", "task"} {
		if _, ok := rec.Args[forbidden]; ok {
			t.Errorf("forbidden field %q must not be in a recap Convex payload", forbidden)
		}
	}
}

// The record we persist on disk is allowed to hold prose, but never a path.
// Task is a NAME by construction (autorunTaskName) — if that ever regresses,
// the record becomes a path leak waiting for a syncer.
func TestRecapRecord_taskIsANameNotAPath(t *testing.T) {
	name := autorunTaskName("/Users/somebody/Workspace/yaver.io/docs/tasks/nightly.md")
	if strings.ContainsAny(name, `/\`) {
		t.Errorf("autorunTaskName returned something path-shaped: %q", name)
	}
	if name != "nightly" {
		t.Errorf("autorunTaskName = %q, want %q", name, "nightly")
	}
}
