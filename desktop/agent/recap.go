package main

// recap.go — Recap: a short narrated video of what an autorun run actually did.
//
// The problem it solves: an autorun loop runs for hours unattended and lands
// N commits. Nobody reads N commits. The evidence of what happened is already
// on the box — screenlog keeps a de-duplicated, timestamped, app-tagged frame
// sequence (screenlog.go), and the run itself records iterations/commits/heals
// (autorunRunSummary). A recap cuts those into ~75 seconds you can watch.
//
// WHERE THE BYTES LIVE — and why they can never live anywhere else.
// A recap is task output rendered to pixels. CLAUDE.md's privacy contract
// forbids task output, file contents, and absolute paths in Convex, and
// convex_privacy_test.go enforces it (including `summaryText` — so the
// narration script is as confidential as the frames). So the artifact stays
// on the box that produced it and surfaces pull it over the existing authed
// HTTP route, exactly like vibe-preview clips do. Convex may hold at most a
// counters-only pointer; see TestRecapConvexPayload_isCounterOnly.
//
//	~/.yaver/recaps/<recapID>/
//	    recap.json      — RecapRecord (metadata + cues)
//	    recap.mp4       — H.264 video, optional AAC narration track
//	    poster.jpg      — first frame, a few KB, shows instantly
//	    subtitles.vtt   — WebVTT sidecar
//
// WHY A SIDECAR VTT rather than studio/compositor.go's CaptionMP4, which
// already burns timed text into pixels with ffmpeg drawtext: burned captions
// cannot be toggled off, cannot be muted independently of the narrator, and
// cannot be translated. The script is the source of truth here; the audio
// track and the subtitle track are both renders OF it. That inversion is what
// makes "mute the guy and read subtitles instead" a volume control rather
// than a re-encode.
//
// Multiple recaps per run is the point, not an accident: one autorun can have
// a `nightly` cut and a `failure` cut, addressed by (AutorunID, Tag).

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Recap tags. A run may carry several recaps; the tag says which cut this is.
// Free-form tags are allowed (validated by recapValidTag) but these are the
// ones the agent generates itself.
const (
	RecapTagNightly = "nightly" // the whole run, start to finish
	RecapTagFailure = "failure" // only the parts that broke — the 90s worth watching
	RecapTagUIDiff  = "ui-diff" // before/after of the app surface
	RecapTagManual  = "manual"  // operator asked for it
)

// Recap build/serve status.
const (
	RecapStatusBuilding = "building"
	RecapStatusReady    = "ready"
	RecapStatusFailed   = "failed"
)

// RecapCue is one line of narration pinned to a span of the FINISHED video's
// timeline (not wall-clock — see recapTimeline, which maps between them).
// One cue renders three ways: a VTT subtitle, a TTS utterance placed at
// StartSec, and a caption a 3D surface can draw as geometry.
type RecapCue struct {
	Text     string  `json:"text"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
}

// RecapRecord is the persisted metadata for one recap. Written to
// recap.json inside the recap dir so listings survive a daemon restart —
// autorunSessions itself is in-memory only, so if we didn't persist here a
// restart would orphan the video with no way to know what it was.
type RecapRecord struct {
	ID        string `json:"id"`                  // r_<hex>
	AutorunID string `json:"autorunId,omitempty"` // the join key: which run this recaps
	Slot      string `json:"slot,omitempty"`      // task:seat — stable across runs
	Task      string `json:"task,omitempty"`      // task NAME, never the path (privacy)
	Tag       string `json:"tag"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`

	CreatedAt   int64   `json:"createdAt"` // unix ms
	DurationSec float64 `json:"durationSec"`
	SizeBytes   int64   `json:"sizeBytes"`
	Frames      int     `json:"frames"`

	SourceSession string `json:"sourceSession,omitempty"` // screenlog session id
	Display       int    `json:"display"`

	HasAudio     bool       `json:"hasAudio"`
	HasSubtitles bool       `json:"hasSubtitles"`
	Voice        string     `json:"voice,omitempty"` // TTS provider that narrated
	Cues         []RecapCue `json:"cues,omitempty"`

	// --- evidence, kept deliberately apart from the narration ---
	//
	// FinishReason is a CLAIM. Per 3a32a4fc3, autorunReasonDone means "a line
	// in the progress file said DONE" — a runner that wrote "this is NOT done"
	// once ended a run as complete. `converged` and `maxIters` are equally
	// ambiguous about whether the work is actually finished. Landed and
	// Complete are computed from evidence, never from FinishReason. The recap
	// narrates the claim and the evidence separately; it must never say
	// "shipped" off the claim alone.
	FinishReason        string `json:"finishReason,omitempty"`
	Iterations          int    `json:"iterations"`
	Commits             int    `json:"commits"`
	FinalCommit         string `json:"finalCommit,omitempty"`
	Landed              bool   `json:"landed"`
	Complete            string `json:"complete,omitempty"` // complete | incomplete | unknown
	PriorityCount       int    `json:"priorityCount,omitempty"`
	EvidencedPriorities int    `json:"evidencedPriorities,omitempty"`
	Heals               int    `json:"heals"`
}

// --- storage ---------------------------------------------------------------

var (
	recapMu      sync.Mutex
	recapBaseDir string // overridable in tests
)

func recapsDir() (string, error) {
	recapMu.Lock()
	defer recapMu.Unlock()
	if recapBaseDir != "" {
		return recapBaseDir, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "recaps")
	// 0700: a recap is a picture of the user's screen.
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

// recapValidID gates every id that reaches the filesystem. IDs are r_<hex> by
// construction; anything with a path component in it is an attack, not a typo.
func recapValidID(id string) bool {
	// len > 3, not just non-empty: a bare "r_" passes a hex loop over an empty
	// string trivially, and would reach filepath.Join as an empty component.
	if len(id) < 3 || len(id) > 64 {
		return false
	}
	if !strings.HasPrefix(id, "r_") {
		return false
	}
	for _, c := range id[2:] {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			return false
		}
	}
	return true
}

// recapValidTag keeps tags safe for a filename-adjacent identifier and for a
// Convex pointer. Free text is rejected: a tag like "fix /Users/bob/api" would
// leak a home-dir username the moment anything synced it.
func recapValidTag(tag string) bool {
	if tag == "" || len(tag) > 32 {
		return false
	}
	for _, c := range tag {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

func recapDir(id string) (string, error) {
	if !recapValidID(id) {
		return "", fmt.Errorf("invalid recap id")
	}
	base, err := recapsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, id), nil
}

func newRecapID() string {
	return "r_" + randomHex(10)
}

// Artifact paths inside a recap dir.
func recapVideoPath(dir string) string     { return filepath.Join(dir, "recap.mp4") }
func recapPosterPath(dir string) string    { return filepath.Join(dir, "poster.jpg") }
func recapSubtitlesPath(dir string) string { return filepath.Join(dir, "subtitles.vtt") }
func recapJSONPath(dir string) string      { return filepath.Join(dir, "recap.json") }

func saveRecap(rec *RecapRecord) error {
	dir, err := recapDir(rec.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename: a reader listing recaps while one is being written
	// must never parse a half-file.
	tmp := recapJSONPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, recapJSONPath(dir))
}

func loadRecap(id string) (*RecapRecord, error) {
	dir, err := recapDir(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(recapJSONPath(dir))
	if err != nil {
		return nil, fmt.Errorf("recap not found")
	}
	var rec RecapRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("recap metadata unreadable: %w", err)
	}
	return &rec, nil
}

// RecapFilter narrows a listing. Zero value lists everything, newest first.
type RecapFilter struct {
	AutorunID string
	Slot      string
	Tag       string
	Limit     int
}

// listRecaps scans the recaps dir. Deliberately a disk scan rather than an
// in-memory index: a recap can be built by a task agent's separate process
// (the same cross-process problem findClipOnDisk solves for clips), so the
// filesystem is the only source both processes agree on.
func listRecaps(f RecapFilter) ([]*RecapRecord, error) {
	base, err := recapsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*RecapRecord, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !recapValidID(e.Name()) {
			continue
		}
		rec, err := loadRecap(e.Name())
		if err != nil {
			continue // half-written or corrupt — skip, don't fail the listing
		}
		if f.AutorunID != "" && rec.AutorunID != f.AutorunID {
			continue
		}
		if f.Slot != "" && rec.Slot != f.Slot {
			continue
		}
		if f.Tag != "" && rec.Tag != f.Tag {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func deleteRecap(id string) error {
	dir, err := recapDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("recap not found")
	}
	return os.RemoveAll(dir)
}

// --- retention -------------------------------------------------------------
//
// Recaps are generated unattended, so they MUST be bounded. ~/.yaver/clips/
// is the cautionary tale: it has no cap, no retention, and no pruning
// anywhere in the tree, and it grows until the disk does. Autorun already
// fights for disk (autorunDiskFloorGB reclaims caches at 3 GB free) — an
// unbounded recap dir would be actively hostile to the loop that feeds it.
//
// Three independent bounds, matching screenlog's shape (count + bytes + age)
// so no single misconfiguration can defeat all of them.

const (
	recapDefaultMaxCount = 40
	recapDefaultMaxMB    = 2048
	recapDefaultMaxDays  = 30
)

// pruneRecaps enforces count/bytes/age. Oldest go first. Never touches a
// recap still building — a half-encoded file has no size yet and would
// otherwise look like a cheap eviction.
func pruneRecaps(maxCount int, maxMB int64, maxDays int) (removed int, err error) {
	if maxCount <= 0 {
		maxCount = recapDefaultMaxCount
	}
	if maxMB <= 0 {
		maxMB = recapDefaultMaxMB
	}
	if maxDays <= 0 {
		maxDays = recapDefaultMaxDays
	}
	all, err := listRecaps(RecapFilter{})
	if err != nil {
		return 0, err
	}
	// Newest first from listRecaps; walk oldest-first for eviction.
	var kept []*RecapRecord
	cutoff := time.Now().Add(-time.Duration(maxDays) * 24 * time.Hour).UnixMilli()
	for _, r := range all {
		if r.Status == RecapStatusBuilding {
			continue // in flight — not ours to evict
		}
		if r.CreatedAt < cutoff {
			if deleteRecap(r.ID) == nil {
				removed++
			}
			continue
		}
		kept = append(kept, r)
	}
	// kept is newest-first. Evict from the tail until under both caps.
	var total int64
	limit := maxMB * 1024 * 1024
	for i, r := range kept {
		total += r.SizeBytes
		overCount := i >= maxCount
		overBytes := total > limit
		if overCount || overBytes {
			if deleteRecap(r.ID) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func pruneRecapsBestEffort() {
	cfg := loadRecapConfig()
	if n, err := pruneRecaps(cfg.MaxCount, cfg.MaxMB, cfg.MaxDays); err == nil && n > 0 {
		log.Printf("[recap] pruned %d old recap(s)", n)
	}
}

// --- config ----------------------------------------------------------------

// RecapConfig controls automatic recap generation. Persisted at
// ~/.yaver/recap.json.
//
// AutoOnAutorun defaults to FALSE. Encoding a recap costs CPU on a box that
// is often mid-build, disk on a loop that already reclaims caches to stay
// above its floor, and — if narration is on — real inference tokens. CLAUDE.md
// makes cost-awareness a product requirement, not a house rule, so this is
// opt-in and says what it costs when you turn it on.
type RecapConfig struct {
	AutoOnAutorun bool    `json:"autoOnAutorun"`
	TargetSec     float64 `json:"targetSec,omitempty"`
	MaxWidth      int     `json:"maxWidth,omitempty"`
	Narrate       bool    `json:"narrate,omitempty"`
	Voice         string  `json:"voice,omitempty"`  // TTS provider; "" = configured default
	Runner        string  `json:"runner,omitempty"` // script polish runner; "" = default
	MaxCount      int     `json:"maxCount,omitempty"`
	MaxMB         int64   `json:"maxMB,omitempty"`
	MaxDays       int     `json:"maxDays,omitempty"`
	// FailureCut also emits a `failure` tagged recap when a run ends badly —
	// gate failed, runner failed, scope violation, or heals occurred.
	FailureCut bool `json:"failureCut,omitempty"`
}

func defaultRecapConfig() RecapConfig {
	return RecapConfig{
		AutoOnAutorun: false,
		TargetSec:     75,
		MaxWidth:      960,
		Narrate:       false,
		MaxCount:      recapDefaultMaxCount,
		MaxMB:         recapDefaultMaxMB,
		MaxDays:       recapDefaultMaxDays,
		FailureCut:    true,
	}
}

func (c *RecapConfig) normalize() {
	d := defaultRecapConfig()
	if c.TargetSec <= 0 || c.TargetSec > 600 {
		c.TargetSec = d.TargetSec
	}
	if c.MaxWidth <= 0 || c.MaxWidth > 3840 {
		c.MaxWidth = d.MaxWidth
	}
	if c.MaxCount <= 0 {
		c.MaxCount = d.MaxCount
	}
	if c.MaxMB <= 0 {
		c.MaxMB = d.MaxMB
	}
	if c.MaxDays <= 0 {
		c.MaxDays = d.MaxDays
	}
}

func recapConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "recap.json"), nil
}

func loadRecapConfig() RecapConfig {
	cfg := defaultRecapConfig()
	p, err := recapConfigPath()
	if err != nil {
		return cfg
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(b, &cfg)
	cfg.normalize()
	return cfg
}

func saveRecapConfig(cfg RecapConfig) error {
	cfg.normalize()
	p, err := recapConfigPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}
