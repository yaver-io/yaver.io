package main

// recap_build.go — turn a screenlog frame sequence into a watchable MP4.
//
// The raw material is already good: screenlog keeps a de-duplicated,
// timestamped, app-tagged JPEG per distinct screen, with ActiveToMs closing
// each frame's interval ("this screen was up from 12:01 to 12:53").
// Three things stand between that and a video, and each is a trap:
//
//  1. MULTI-DISPLAY INTERLEAVE. Frames for every display live in ONE dir
//     (000123_d0_….jpg, 000124_d1_….jpg). ops_testkit.go's stitchFramesToMP4
//     globs `*.png` — pointed at a screenlog dir it would both miss the .jpgs
//     and splice two monitors into one video. We select an explicit display
//     and build the input list from index.json, never from a glob.
//
//  2. VARIABLE TIMING. Because duplicates are dropped, consecutive frames can
//     be 2 seconds or 50 minutes apart. A fixed -framerate would render an
//     8-hour night as uniform motion — every idle screen and every burst of
//     work equally weighted, which is a lie. See paceFrames.
//
//  3. STREAMABILITY. Surfaces seek (tvOS AVPlayer, <video>, VideoTexture) and
//     pull over relay with Range requests. Without -movflags +faststart the
//     moov atom lands at the end of the file and playback stalls until the
//     whole thing is downloaded.

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RecapBuildOpts drives one recap build.
type RecapBuildOpts struct {
	SessionID string // screenlog session; "" = most recent
	AutorunID string // join key — which run this recaps
	Slot      string // task:seat
	Task      string // task NAME (never a path)
	Tag       string // nightly | failure | ui-diff | manual | <custom>
	Title     string
	Display   int     // which monitor (see trap 1)
	TargetSec float64 // aim for this runtime; 0 = 75s
	MaxWidth  int     // downscale cap; 0 = 960

	// Window. Frames outside [SinceMs, UntilMs] are excluded — this is what
	// scopes a whole-screen recorder down to "what happened during run X".
	// Zero on either side means unbounded on that side.
	SinceMs int64
	UntilMs int64

	// Narration. Off by default; costs inference tokens + TTS.
	Narrate bool
	Voice   string
	Runner  string

	// Evidence carried from the run (see RecapRecord.FinishReason).
	FinishReason string
	Iterations   int
	Commits      int
	FinalCommit  string
	Verified     bool
	Heals        int
	// WorkDir is the repo the run touched; used to collect git activity for
	// the script. Never stored in the record.
	WorkDir string
}

const (
	// Per-frame screen-time bounds. A frame is never a subliminal flash and
	// never a stall you'd reach for the remote to skip.
	recapMinFrameSec      = 0.15
	recapMaxFrameSec      = 4.0
	recapDefaultTargetSec = 75.0
	recapDefaultMaxWidth  = 960
	// Encoding an 8-hour night can be genuinely slow on a busy box.
	recapEncodeTimeout = 10 * time.Minute
)

// recapTimeline maps wall-clock ms onto the finished video's compressed
// seconds. Cues are authored against wall-clock events (an episode started at
// 03:14) but must be rendered against video time (that moment is 41.2s in).
type recapTimeline struct {
	wallMs   []int64   // frame capture time
	videoSec []float64 // cumulative video offset at that frame
	totalSec float64
}

// videoSecAt maps a wall-clock instant to a video offset, interpolating
// within the frame that was on screen at that moment.
func (t *recapTimeline) videoSecAt(wallMs int64) float64 {
	if len(t.wallMs) == 0 {
		return 0
	}
	if wallMs <= t.wallMs[0] {
		return 0
	}
	for i := 0; i < len(t.wallMs); i++ {
		if t.wallMs[i] > wallMs {
			return t.videoSec[i-1]
		}
	}
	return t.totalSec
}

// paceFrames assigns each frame its screen time.
//
// The honest-pacing problem: frame A was on screen 2 seconds, frame B for 50
// minutes. Linear time gives B 1500x the screen time and the entire recap is
// one idle window. Uniform time says they were equally significant, which is
// also false — it erases the difference between "glanced at" and "sat on for
// an hour".
//
// log1p keeps the ordering truthful while compressing the dynamic range: B
// still reads as longer than A, by roughly 8x rather than 1500x. Clamping
// then bounds both tails. The result is a time-lapse whose pacing you can
// actually trust to mean something.
//
// Returns per-frame durations in seconds and the resulting total (which drifts
// from targetSec once clamping binds — we report the truth rather than
// re-normalising into a lie).
func paceFrames(frames []ScreenlogFrame, sessionEndMs int64, targetSec float64) ([]float64, float64) {
	if len(frames) == 0 {
		return nil, 0
	}
	if targetSec <= 0 {
		targetSec = recapDefaultTargetSec
	}
	weights := make([]float64, len(frames))
	var sum float64
	for i, f := range frames {
		end := f.ActiveToMs
		if end <= f.CapturedAt {
			// Last frame of a display never gets its interval closed by a
			// successor; screenlog closes it at StoppedAt. Fall back to the
			// next frame's capture, then to the session end.
			if i+1 < len(frames) {
				end = frames[i+1].CapturedAt
			} else {
				end = sessionEndMs
			}
		}
		activeSec := float64(end-f.CapturedAt) / 1000.0
		if activeSec < 0 {
			activeSec = 0
		}
		w := math.Log1p(activeSec)
		if w <= 0 {
			w = math.Log1p(1) // a frame with no measurable dwell still gets a floor
		}
		weights[i] = w
		sum += w
	}
	durs := make([]float64, len(frames))
	var total float64
	for i, w := range weights {
		d := targetSec * (w / sum)
		if d < recapMinFrameSec {
			d = recapMinFrameSec
		}
		if d > recapMaxFrameSec {
			d = recapMaxFrameSec
		}
		durs[i] = d
		total += d
	}
	return durs, total
}

// selectRecapFrames filters a session's frames to one display and window,
// preserving capture order.
func selectRecapFrames(sess *ScreenlogSession, display int, sinceMs, untilMs int64) []ScreenlogFrame {
	out := make([]ScreenlogFrame, 0, len(sess.Frames))
	for _, f := range sess.Frames {
		if f.Display != display {
			continue // trap 1: never mix monitors
		}
		if f.File == "" {
			continue // EphemeralFrames mode kept the trace but dropped the image
		}
		if sinceMs > 0 && f.CapturedAt < sinceMs {
			continue
		}
		if untilMs > 0 && f.CapturedAt > untilMs {
			continue
		}
		out = append(out, f)
	}
	return out
}

// recapDisplays reports which displays a session actually has frames for, so
// a caller can pick rather than guess.
func recapDisplays(sess *ScreenlogSession) []int {
	seen := map[int]bool{}
	var out []int
	for _, f := range sess.Frames {
		if !seen[f.Display] {
			seen[f.Display] = true
			out = append(out, f.Display)
		}
	}
	return out
}

// ffmpegConcatEscape quotes a path for the concat demuxer's `file '...'`
// directive. The demuxer takes single-quoted paths; a literal quote is
// escaped by closing, emitting an escaped quote, and reopening. Frame names
// are safe by construction (%06d_d%d_%d.jpg) but the SESSION DIR sits under
// $HOME and a home directory may contain anything.
func ffmpegConcatEscape(p string) string {
	return strings.ReplaceAll(p, "'", `'\''`)
}

// writeConcatList emits an ffmpeg concat-demuxer script with per-frame
// durations.
//
// The trailing repeat is not a typo: the concat demuxer ignores the final
// entry's `duration`, so without repeating the last file its frame flashes
// for one tick and the video ends early. This is a long-standing ffmpeg
// quirk and the documented workaround.
func writeConcatList(path string, dir string, frames []ScreenlogFrame, durs []float64) error {
	var b strings.Builder
	for i, f := range frames {
		abs := ffmpegConcatEscape(filepath.Join(dir, f.File))
		fmt.Fprintf(&b, "file '%s'\n", abs)
		fmt.Fprintf(&b, "duration %.3f\n", durs[i])
	}
	if len(frames) > 0 {
		last := ffmpegConcatEscape(filepath.Join(dir, frames[len(frames)-1].File))
		fmt.Fprintf(&b, "file '%s'\n", last)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// encodeRecapVideo runs the concat list through x264.
func encodeRecapVideo(ctx context.Context, listPath, out string, maxWidth int) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		// Soft dependency, deliberately: screenlog itself is ffmpeg-free, and
		// every other encode path in the tree LookPaths and degrades. A box
		// without ffmpeg loses recaps, not screen recording.
		return fmt.Errorf("ffmpeg not found — install it to build recaps (brew install ffmpeg)")
	}
	if maxWidth <= 0 {
		maxWidth = recapDefaultMaxWidth
	}
	ctx, cancel := context.WithTimeout(ctx, recapEncodeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-f", "concat", "-safe", "0", "-i", listPath,
		// vfr honours the per-frame durations from the concat list; without it
		// ffmpeg resamples to a constant rate and the pacing work is discarded.
		"-fps_mode", "vfr",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", maxWidth),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "30",
		"-pix_fmt", "yuv420p",
		// -2 keeps height even for yuv420p; faststart moves the moov atom to
		// the front so Range/seek works before the file is fully pulled.
		"-movflags", "+faststart",
		out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg encode: %v: %s", err, tailStr(string(b), 400))
	}
	return nil
}

// makeRecapPoster grabs the first frame as a small JPEG so a listing can show
// something instantly, before the MP4 is pulled.
func makeRecapPoster(ctx context.Context, video, out string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", video,
		"-frames:v", "1", "-vf", "scale=360:-2", "-q:v", "6", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg poster: %v: %s", err, tailStr(string(b), 200))
	}
	return nil
}

// tailStr keeps ffmpeg's error output readable — it prints its whole banner
// before the actual failure.
func tailStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// BuildRecap assembles a recap end to end and returns its persisted record.
//
// The record is saved as `building` first so a listing during a long encode
// shows the work in flight rather than nothing — and so a crash mid-encode
// leaves visible evidence instead of an orphan directory.
func BuildRecap(ctx context.Context, opts RecapBuildOpts) (*RecapRecord, error) {
	if opts.Tag == "" {
		opts.Tag = RecapTagManual
	}
	if !recapValidTag(opts.Tag) {
		return nil, fmt.Errorf("invalid tag %q: use lowercase letters, digits, and dashes", opts.Tag)
	}
	if opts.TargetSec <= 0 {
		opts.TargetSec = recapDefaultTargetSec
	}
	if opts.MaxWidth <= 0 {
		opts.MaxWidth = recapDefaultMaxWidth
	}

	sessID := opts.SessionID
	if sessID == "" {
		latest, err := latestScreenlogSessionID()
		if err != nil {
			return nil, err
		}
		sessID = latest
	}
	sess, err := loadScreenlogSession(sessID)
	if err != nil {
		return nil, fmt.Errorf("load screenlog session: %w", err)
	}
	sessDir, err := screenlogSessionDir(sessID)
	if err != nil {
		return nil, err
	}

	frames := selectRecapFrames(sess, opts.Display, opts.SinceMs, opts.UntilMs)
	if len(frames) == 0 {
		// Be specific: "no frames" is usually a display mismatch or a window
		// that missed the run, and both are fixable by the caller.
		return nil, fmt.Errorf("no frames for display %d in window (session %s has displays %v, %d frames total)",
			opts.Display, sessID, recapDisplays(sess), len(sess.Frames))
	}

	sessEnd := sess.StoppedAt
	if sessEnd == 0 {
		sessEnd = time.Now().UnixMilli()
	}
	durs, total := paceFrames(frames, sessEnd, opts.TargetSec)

	// Build the wall-clock → video-time map before encoding; the script stage
	// needs it to place cues.
	tl := &recapTimeline{totalSec: total}
	var acc float64
	for i, f := range frames {
		tl.wallMs = append(tl.wallMs, f.CapturedAt)
		tl.videoSec = append(tl.videoSec, acc)
		acc += durs[i]
	}

	rec := &RecapRecord{
		ID:            newRecapID(),
		AutorunID:     opts.AutorunID,
		Slot:          opts.Slot,
		Task:          opts.Task,
		Tag:           opts.Tag,
		Title:         opts.Title,
		Status:        RecapStatusBuilding,
		CreatedAt:     time.Now().UnixMilli(),
		DurationSec:   total,
		Frames:        len(frames),
		SourceSession: sessID,
		Display:       opts.Display,
		FinishReason:  opts.FinishReason,
		Iterations:    opts.Iterations,
		Commits:       opts.Commits,
		FinalCommit:   opts.FinalCommit,
		Verified:      opts.Verified,
		Heals:         opts.Heals,
	}
	if rec.Title == "" {
		rec.Title = recapDefaultTitle(opts)
	}
	if err := saveRecap(rec); err != nil {
		return nil, err
	}
	dir, err := recapDir(rec.ID)
	if err != nil {
		return nil, err
	}

	fail := func(err error) (*RecapRecord, error) {
		rec.Status = RecapStatusFailed
		rec.Error = err.Error()
		_ = saveRecap(rec)
		return rec, err
	}

	listPath := filepath.Join(dir, "frames.txt")
	if err := writeConcatList(listPath, sessDir, frames, durs); err != nil {
		return fail(fmt.Errorf("write concat list: %w", err))
	}
	defer os.Remove(listPath)

	video := recapVideoPath(dir)
	if err := encodeRecapVideo(ctx, listPath, video, opts.MaxWidth); err != nil {
		return fail(err)
	}

	// Poster is best-effort: a recap with no thumbnail is still watchable.
	if err := makeRecapPoster(ctx, video, recapPosterPath(dir)); err != nil {
		log.Printf("[recap] poster failed for %s: %v", rec.ID, err)
	}

	// Script → cues → VTT. Best-effort as well: a silent, subtitle-less recap
	// is still the thing you wanted to watch.
	cues, err := BuildRecapCues(ctx, sess, tl, opts)
	if err != nil {
		log.Printf("[recap] script failed for %s: %v", rec.ID, err)
	} else if len(cues) > 0 {
		rec.Cues = cues
		if err := writeVTT(recapSubtitlesPath(dir), cues); err != nil {
			log.Printf("[recap] vtt failed for %s: %v", rec.ID, err)
		} else {
			rec.HasSubtitles = true
		}
		if opts.Narrate {
			voice, err := NarrateRecap(ctx, dir, video, cues, opts.Voice)
			if err != nil {
				// Narration is the most failure-prone stage (needs a TTS key,
				// a network, and ffmpeg). Losing the voice must never lose the
				// video — the subtitles still carry the whole script.
				log.Printf("[recap] narration failed for %s: %v", rec.ID, err)
			} else {
				rec.HasAudio = true
				rec.Voice = voice
			}
		}
	}

	if st, err := os.Stat(video); err == nil {
		rec.SizeBytes = st.Size()
	}
	rec.Status = RecapStatusReady
	if err := saveRecap(rec); err != nil {
		return rec, err
	}

	cfg := loadRecapConfig()
	if n, err := pruneRecaps(cfg.MaxCount, cfg.MaxMB, cfg.MaxDays); err == nil && n > 0 {
		log.Printf("[recap] pruned %d old recap(s)", n)
	}
	return rec, nil
}

func recapDefaultTitle(opts RecapBuildOpts) string {
	name := opts.Task
	if name == "" {
		name = "session"
	}
	switch opts.Tag {
	case RecapTagFailure:
		return fmt.Sprintf("%s — what broke", name)
	case RecapTagUIDiff:
		return fmt.Sprintf("%s — before and after", name)
	case RecapTagNightly:
		return fmt.Sprintf("%s — overnight", name)
	}
	return name
}
