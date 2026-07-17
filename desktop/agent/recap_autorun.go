package main

// recap_autorun.go — join a finished autorun run to its recap.
//
// The autorun loop is signal-silent end to end: no channel, no callback, no
// publish. The ONLY place in the codebase that knows a run just ended is the
// completion goroutine in autorunSessionManager.start (autorun_ops.go), which
// sets s.Status and holds the fully-populated summary. That's where this
// attaches — following the OnExecDone precedent (main.go) for the same shape.
//
// This is also the join the whole feature needed: screenlog records the WHOLE
// SCREEN with no notion of runs, and autorun records runs with no notion of
// pixels. Nothing connected them. A run's [StartedAt, FinishedAt] window over
// a screenlog session is that connection — which is why the window is the
// first thing this computes and the reason recaps are addressed by autorun id.

import (
	"log"
	"strings"
)

// onAutorunFinished is called from the autorun completion goroutine once the
// session's terminal state is set. It must not block that goroutine — it holds
// the manager's write lock.
//
// Everything here is best-effort by design. A recap is a nice-to-have on top
// of a run that has already done its real work; no recap failure may ever
// change the run's recorded outcome.
func onAutorunFinished(s *autorunSession) {
	if s == nil {
		return
	}
	cfg := loadRecapConfig()
	if !cfg.AutoOnAutorun {
		return // opt-in: encoding costs CPU, disk, and (with narration) tokens
	}

	// A run with no wall-clock window can't be located in the frame stream.
	// FinishedAt is set by the caller immediately before this runs.
	sinceMs := s.StartedAt.UnixMilli()
	untilMs := s.FinishedAt.UnixMilli()
	if sinceMs <= 0 || untilMs <= sinceMs {
		log.Printf("[recap] autorun %s has no usable time window; skipping recap", s.ID)
		return
	}

	// Pick the session that was actually recording during the run — not the
	// newest one. A run that finished an hour ago may well be followed by a
	// fresh screenlog session containing none of its frames.
	sessID, err := screenlogSessionForWindow(sinceMs, untilMs)
	if err != nil {
		// The common cause is simply that screenlog wasn't running. Say so;
		// silence here looks like a broken feature.
		log.Printf("[recap] no frames for autorun %s (%v) — start screenlog to get recaps", s.ID, err)
		return
	}

	base := RecapBuildOpts{
		SessionID:    sessID,
		AutorunID:    s.ID,
		Slot:         s.Slot,
		Task:         autorunTaskName(s.Task), // NAME, never the path
		SinceMs:      sinceMs,
		UntilMs:      untilMs,
		TargetSec:    cfg.TargetSec,
		MaxWidth:     cfg.MaxWidth,
		Narrate:      cfg.Narrate,
		Voice:        cfg.Voice,
		Runner:       cfg.Runner,
		WorkDir:      s.WorkDir,
		TaskPath:     s.Task,
		ProgressPath: s.ProgressPath,
		FinishReason: s.Summary.FinishReason,
		Iterations:   s.Summary.Iterations,
		Commits:      s.Summary.Commits,
		FinalCommit:  s.Summary.FinalCommit,
		Heals:        len(s.Summary.Heals),
	}
	ev := deriveRecapCompletion(s.Task, s.ProgressPath, recapLanded(s.Summary))
	base.Landed = ev.Landed
	base.Complete = ev.Complete
	base.PriorityCount = ev.PriorityCount
	base.EvidencedPriorities = ev.EvidencedPriorities

	nightly := base
	nightly.Tag = RecapTagNightly
	buildRecapAsync(nightly)

	// The failure cut is the one a busy person actually watches: 90 seconds of
	// only the part that broke, instead of eight hours of everything. Emitted
	// as a SECOND recap against the same run — which is exactly why recaps are
	// keyed by (autorunId, tag) rather than one-per-run.
	if cfg.FailureCut && autorunRunLooksBad(s) {
		fail := base
		fail.Tag = RecapTagFailure
		fail.TargetSec = 45 // shorter: it exists to be watched, not admired
		// Narrow to the tail of the run, where the failure is. Heals and gate
		// failures cluster at the end; the first six hours are not the story.
		if w := untilMs - sinceMs; w > recapFailureTailMs {
			fail.SinceMs = untilMs - recapFailureTailMs
		}
		buildRecapAsync(fail)
	}
}

// recapFailureTailMs bounds how far back a failure cut reaches. Ten minutes
// is long enough to include the gate run that failed and the iteration that
// caused it, short enough to stay watchable.
const recapFailureTailMs int64 = 10 * 60 * 1000

// autorunRunLooksBad reports whether a run merits a failure cut.
//
// Note what counts as bad here, and what does not. autorunReasonDone with
// zero commits IS bad — per 3a32a4fc3 that combination is the exact signature
// of a runner asserting completion without landing anything, which is the
// single most important thing a recap can show you. Conversely a run that
// self-healed but still landed commits is not a failure; it's a run that
// worked, and the heal is a line in the narration, not its own video.
func autorunRunLooksBad(s *autorunSession) bool {
	if s.Status == "failed" || s.Error != "" {
		return true
	}
	switch s.Summary.FinishReason {
	case autorunReasonGate, autorunReasonRunner, autorunReasonScope, autorunReasonResources:
		return true
	}
	// Claimed done, nothing to show for it.
	if s.Summary.FinishReason == autorunReasonDone && s.Summary.Commits == 0 {
		return true
	}
	if s.Summary.FinishReason == autorunReasonDone {
		ev := deriveRecapCompletion(s.Task, s.ProgressPath, recapLanded(s.Summary))
		if ev.PriorityCount > 0 && ev.Complete != recapCompleteComplete {
			return true
		}
	}
	// Converged without landing anything: it stopped changing things rather
	// than finishing them.
	if s.Summary.FinishReason == autorunReasonConverged && s.Summary.Commits == 0 {
		return true
	}
	return false
}

// recapTagsForRun is a small helper for surfaces that want to know, without
// listing the disk, which cuts a run should have.
func recapTagsForRun(s *autorunSession) []string {
	tags := []string{RecapTagNightly}
	cfg := loadRecapConfig()
	if cfg.FailureCut && autorunRunLooksBad(s) {
		tags = append(tags, RecapTagFailure)
	}
	return tags
}

// recapSlotLabel renders a slot for display without leaking a path. Slots are
// "<taskPath>:<seat>" internally (autorunSlotKey), and the task path is an
// absolute filesystem path — which must never reach a UI payload that could
// be synced.
func recapSlotLabel(slot string) string {
	i := strings.LastIndex(slot, ":")
	if i < 0 {
		return autorunTaskName(slot)
	}
	return autorunTaskName(slot[:i]) + ":" + slot[i+1:]
}
