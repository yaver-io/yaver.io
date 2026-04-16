package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"
)

// autodev_morning_hook.go — thin bridge between the autodev kick loop
// and the morning match-report store / recording manager.
//
// Cardinal rule: these hooks MUST NEVER fail an autodev kick. If the
// morning store is unwritable, if no video driver is present, if the
// repo isn't git — all are logged and swallowed. Autodev's job is to
// ship code; the match report is a reporting layer on top, not a
// precondition.

// morningHook carries the state a single autodev run needs to maintain
// a rolling MorningSummary and, optionally, a product-demo recording
// per kick.
type morningHook struct {
	plan     autodevPlan
	store    *MorningStore
	recMgr   *RecordingManager
	runID    string
	workDir  string
	recording bool // did the current kick start a recording we need to stop?
}

// newMorningHookFromPlan returns a hook configured from the plan, or
// nil if morning summaries are disabled. Never returns an error — we
// degrade silently when the on-disk store can't be initialized.
func newMorningHookFromPlan(p autodevPlan) *morningHook {
	if !p.MorningSummary {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	// Use the loop name as the run id so multiple sessions of the
	// same autodev loop collapse into the same match report over
	// successive nights; the UI shows newest first.
	return &morningHook{
		plan:    p,
		store:   DefaultMorningStore(),
		recMgr:  DefaultRecordingManager(),
		runID:   p.LoopName,
		workDir: wd,
	}
}

// beforeKick is called right before the runner is spawned. It captures
// the base git SHA and, if video is enabled and a product-demo driver
// is ready, starts recording. All errors are logged, not returned.
func (h *morningHook) beforeKick(iter int, title string) (baseSHA string) {
	if h == nil {
		return ""
	}
	baseSHA = GitHeadSHA(h.workDir)
	if !h.plan.MorningVideo {
		return baseSHA
	}
	if !h.recMgr.HasAnyAppDemoDriver() {
		// No booted sim / attached emu. Fine — skip video this kick.
		// The match-report UI renders the card without a video layer.
		return baseSHA
	}
	// Auto-pick (ios-sim > android-emu). We never force target here
	// because the user's intent is "record the product" and we let
	// the resolver decide which surface is live.
	_, err := h.recMgr.Start(context.Background(), h.runID, h.taskIDFor(iter), "")
	if err != nil {
		log.Printf("[morning] record start failed (kick #%d): %v — continuing without video", iter, err)
		return baseSHA
	}
	h.recording = true
	return baseSHA
}

// afterKick is called once the runner returns. Collects git stats,
// stops recording if we started one, and upserts a TaskHighlight. A
// no-op kick (before == after) still gets a "skipped" card so the
// user can see autodev was alive and didn't ship anything.
func (h *morningHook) afterKick(iter int, title, baseSHA string, kickStart time.Time) {
	if h == nil {
		return
	}
	headSHA := GitHeadSHA(h.workDir)
	status := TaskStatusHighlightShipped
	filesChanged, added, removed, shas := 0, 0, 0, []string(nil)
	if baseSHA == "" || headSHA == baseSHA {
		status = TaskStatusHighlightSkipped
	} else {
		shas = GitCommitSHAsBetween(h.workDir, baseSHA, headSHA)
		filesChanged, added, removed = GitDiffStats(h.workDir, baseSHA, headSHA)
	}

	taskID := h.taskIDFor(iter)
	highlight := TaskHighlight{
		TaskID:       taskID,
		RunnerID:     h.plan.Runner,
		Title:        firstNonEmptyMorning(title, fmt.Sprintf("%s kick #%d", h.plan.Kind, iter)),
		Status:       status,
		StartedAt:    kickStart.UTC(),
		FinishedAt:   time.Now().UTC(),
		BaseSHA:      baseSHA,
		HeadSHA:      headSHA,
		CommitSHAs:   shas,
		WorkDir:      h.workDir,
		FilesChanged: filesChanged,
		LinesAdded:   added,
		LinesRemoved: removed,
	}

	if h.recording {
		h.recording = false
		result, err := h.recMgr.Stop(h.runID, taskID)
		if err != nil {
			log.Printf("[morning] record stop failed (kick #%d): %v — card will show no video", iter, err)
		} else {
			highlight.HasVideo = true
			highlight.VideoDurationMs = result.DurationMs
			highlight.VideoSizeBytes = result.SizeBytes
		}
	}

	if _, err := h.store.UpsertTask(h.runID, h.plan.Project, h.workDir, highlight); err != nil {
		log.Printf("[morning] upsert failed (kick #%d): %v — autodev continues", iter, err)
	}
}

// finalize stamps FinishedAt on the summary at end-of-run. Cheap; safe
// to call repeatedly.
func (h *morningHook) finalize(note string) {
	if h == nil {
		return
	}
	if _, err := h.store.Finalize(h.runID, note); err != nil {
		log.Printf("[morning] finalize failed: %v", err)
	}
}

func (h *morningHook) taskIDFor(iter int) string {
	return fmt.Sprintf("kick-%d", iter)
}

func firstNonEmptyMorning(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
