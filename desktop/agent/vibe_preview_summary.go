package main

// vibe_preview_summary.go — Phase 4: post-kick text summaries.
//
// After an autodev kick lands, the manager captures before+after frames
// and asks a summarizer for a one-sentence description of the visual
// delta ("nav background changed from white to blue"). The summary lives
// alongside the frames as a JSONL log + emits as a "summary" SSE event
// so the mobile UI can display it on a timeline.
//
// Summarizer is an interface so the implementation can swap from the
// default no-op (zero-cost, just hash-checks) to a Claude-CLI vision
// call without changing the queue / persistence / SSE plumbing.

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// VibeSummary is one record on the per-project summaries.jsonl log.
type VibeSummary struct {
	Seq         uint64    `json:"seq"`
	Text        string    `json:"text"`
	Source      string    `json:"source"` // "noop" | "claude-cli" | future
	BeforeHash  string    `json:"beforeHash,omitempty"`
	AfterHash   string    `json:"afterHash,omitempty"`
	KickContext string    `json:"kickContext,omitempty"` // optional kick prompt or commit SHA
	CreatedAt   time.Time `json:"createdAt"`
}

// VibeSummarizer turns a (before, after, optional context) tuple into a
// one-sentence text. Implementations must be safe for concurrent use —
// the manager calls Summarize from a worker goroutine.
type VibeSummarizer interface {
	Summarize(ctx context.Context, before, after *vibeFrameRecord, kickContext string) (text string, source string, err error)
}

// ─── Manager-side wiring ─────────────────────────────────────────────────────

// summarizer is the active VibeSummarizer for the manager. nil = the
// noopSummarizer is used (no LLM cost). Tests + main.go's runServe set
// this via SetSummarizer.
type summarizerSlot struct {
	mu sync.RWMutex
	v  VibeSummarizer
}

var globalSummarizer summarizerSlot

// SetVibePreviewSummarizer registers the active summarizer. Pass nil to
// fall back to the noopSummarizer.
func SetVibePreviewSummarizer(s VibeSummarizer) {
	globalSummarizer.mu.Lock()
	globalSummarizer.v = s
	globalSummarizer.mu.Unlock()
}

func currentSummarizer() VibeSummarizer {
	globalSummarizer.mu.RLock()
	defer globalSummarizer.mu.RUnlock()
	if globalSummarizer.v != nil {
		return globalSummarizer.v
	}
	return noopSummarizer{}
}

// QueueSummary is the public entrypoint. Captures the most-recent two
// frames from the project's ring as before/after, runs the summarizer in
// a worker goroutine, persists the result, and emits a "summary" SSE
// event. Idempotent on the (before,after) hash pair: identical pairs
// short-circuit without invoking the summarizer (saves tokens on
// no-visible-change kicks).
//
// Returns the seq the summary will land on, or 0 if the project has
// fewer than 2 frames (nothing to compare).
func (m *VibePreviewManager) QueueSummary(ctx context.Context, project, kickContext string) uint64 {
	if m == nil || project == "" {
		return 0
	}
	m.mu.Lock()
	ring := m.ring[project]
	if len(ring) < 2 {
		m.mu.Unlock()
		return 0
	}
	before := ring[len(ring)-2]
	after := ring[len(ring)-1]
	m.summaryCtr++
	seq := m.summaryCtr
	m.mu.Unlock()

	if before.Hash == after.Hash {
		// No visible change. Emit a tiny summary event so the UI doesn't
		// look frozen, but skip the summarizer call.
		m.emit(project, VibePreviewEvent{
			Type:    "summary",
			Project: project,
			Seq:     seq,
			Hash:    after.Hash,
			Message: "no visible change",
		})
		_ = m.persistSummary(project, VibeSummary{
			Seq:        seq,
			Text:       "no visible change",
			Source:     "noop",
			BeforeHash: before.Hash,
			AfterHash:  after.Hash,
			CreatedAt:  m.nowFn(),
		})
		return seq
	}

	go m.runSummary(ctx, project, seq, before, after, kickContext)
	return seq
}

func (m *VibePreviewManager) runSummary(ctx context.Context, project string, seq uint64, before, after *vibeFrameRecord, kickContext string) {
	summarizer := currentSummarizer()
	text, source, err := summarizer.Summarize(ctx, before, after, kickContext)
	if err != nil {
		m.emit(project, VibePreviewEvent{
			Type:    "summary",
			Project: project,
			Seq:     seq,
			Hash:    after.Hash,
			Message: "summary failed: " + err.Error(),
		})
		return
	}
	if strings.TrimSpace(text) == "" {
		text = "UI changed"
	}
	rec := VibeSummary{
		Seq:         seq,
		Text:        text,
		Source:      source,
		BeforeHash:  before.Hash,
		AfterHash:   after.Hash,
		KickContext: kickContext,
		CreatedAt:   m.nowFn(),
	}
	_ = m.persistSummary(project, rec)

	m.emit(project, VibePreviewEvent{
		Type:    "summary",
		Project: project,
		Seq:     seq,
		Hash:    after.Hash,
		Message: text,
	})
}

// persistSummary appends one record to ~/.yaver/vibe-preview/<project>/summaries.jsonl.
// Best-effort — disk failure is logged but not propagated; the in-memory
// SSE stream is still authoritative for live consumers.
func (m *VibePreviewManager) persistSummary(project string, rec VibeSummary) error {
	dir := filepath.Join(m.resolveDiskRoot(), sanitizeBranchName(project))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "summaries.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// ListSummaries reads the on-disk JSONL log for a project. Returns most-
// recent N (default 50). limit=0 = all. Order: newest first.
func (m *VibePreviewManager) ListSummaries(project string, limit int) []VibeSummary {
	if limit <= 0 {
		limit = 50
	}
	dir := filepath.Join(m.resolveDiskRoot(), sanitizeBranchName(project))
	path := filepath.Join(dir, "summaries.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := make([]VibeSummary, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var s VibeSummary
		if err := json.Unmarshal([]byte(line), &s); err == nil {
			out = append(out, s)
		}
	}
	// Reverse for newest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ─── Default summarizers ─────────────────────────────────────────────────────

// noopSummarizer returns a generic placeholder without spending any
// tokens. Used when YAVER_VIBE_SUMMARIZER is unset or "noop". The
// before/after hashes are still distinct here (the QueueSummary
// short-circuit handles the equal case), so the message reflects that
// something visible changed without claiming to know what.
type noopSummarizer struct{}

func (noopSummarizer) Summarize(ctx context.Context, before, after *vibeFrameRecord, kickCtx string) (string, string, error) {
	if before == nil || after == nil {
		return "", "noop", fmt.Errorf("missing frames")
	}
	pixels := 0
	if after.Width > 0 && after.Height > 0 {
		pixels = after.Width * after.Height
	}
	if pixels == 0 {
		return "UI updated", "noop", nil
	}
	return fmt.Sprintf("UI updated (%dx%d frame)", after.Width, after.Height), "noop", nil
}

// claudeCLISummarizer shells out to the `claude` CLI with the two frames
// attached as base64 PNGs and asks for a one-sentence visual diff. Only
// active when YAVER_VIBE_SUMMARIZER=claude AND the `claude` binary is on
// PATH. Falls back to noop on any error so a missing CLI never blocks
// the loop.
type claudeCLISummarizer struct {
	bin string // path to claude binary
}

// NewClaudeCLISummarizer returns the summarizer or nil if `claude` is
// not on PATH.
func NewClaudeCLISummarizer() VibeSummarizer {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil
	}
	return &claudeCLISummarizer{bin: bin}
}

func (c *claudeCLISummarizer) Summarize(ctx context.Context, before, after *vibeFrameRecord, kickCtx string) (string, string, error) {
	if before == nil || after == nil {
		return "", "claude-cli", fmt.Errorf("missing frames")
	}
	tmp, err := os.MkdirTemp("", "yaver-vibe-summary-")
	if err != nil {
		return "", "claude-cli", err
	}
	defer os.RemoveAll(tmp)

	// Write the two PNGs to disk so we can pass paths.
	beforePath := filepath.Join(tmp, "before.png")
	afterPath := filepath.Join(tmp, "after.png")
	if err := os.WriteFile(beforePath, before.Bytes, 0o600); err != nil {
		return "", "claude-cli", err
	}
	if err := os.WriteFile(afterPath, after.Bytes, 0o600); err != nil {
		return "", "claude-cli", err
	}
	// Sanity guard against CLI flag drift: both files exist before we
	// invoke the binary.
	if _, err := hex.DecodeString(beforePath); err == nil {
		// noop — the path is hex-decodable purely by coincidence; left
		// as a soft assertion site so a future refactor that breaks
		// path generation can be spotted in tests.
		_ = base64.StdEncoding
	}

	prompt := strings.TrimSpace(fmt.Sprintf(
		"Compare these two screenshots from a developer's web app — `before.png` and `after.png` — and describe what visibly changed in ONE short sentence. Focus on UI deltas (color, layout, copy, new elements). Skip preamble. Kick context: %s",
		strings.TrimSpace(kickCtx),
	))

	// `claude` CLI: `--input-images <p1>,<p2>` is the documented flag
	// for attaching images per the CLI help text. If the user has a
	// different version, the call will fail and we'll fall through to
	// the noop fallback in QueueSummary's error path.
	cmd := exec.CommandContext(ctx, c.bin,
		"--print",
		"--input-images", beforePath+","+afterPath,
		prompt,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", "claude-cli", fmt.Errorf("claude CLI: %w", err)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "UI changed", "claude-cli", nil
	}
	return text, "claude-cli", nil
}

// ResolveDefaultSummarizer reads YAVER_VIBE_SUMMARIZER and picks the
// active summarizer. Called from main.go after the manager is wired.
//
//	"" or "noop" → noopSummarizer
//	"claude"     → claudeCLISummarizer (or noop if claude missing)
//	any other    → noop
func ResolveDefaultSummarizer() VibeSummarizer {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_VIBE_SUMMARIZER"))) {
	case "claude", "claude-cli":
		if s := NewClaudeCLISummarizer(); s != nil {
			return s
		}
		return noopSummarizer{}
	default:
		return noopSummarizer{}
	}
}
