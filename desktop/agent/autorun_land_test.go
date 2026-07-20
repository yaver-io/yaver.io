package main

import (
	"os"
	"strings"
	"testing"
)

// readSourceFile reads a file from this package. The invariants below are about
// which git verbs the landing path is allowed to use, and a behavioural test
// would need a real remote plus two racing pushes to observe them — so assert
// them where they are actually decided.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// sliceFunc returns one function's source, so an assertion about the landing
// path cannot be accidentally satisfied by an unrelated function elsewhere in
// the file.
func sliceFunc(t *testing.T, src, signature string) string {
	t.Helper()
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("function %q not found — it was renamed or removed; update this test deliberately", signature)
	}
	rest := src[i:]
	// Functions in this file end at the first column-0 closing brace.
	if j := strings.Index(rest, "\n}\n"); j > 0 {
		return rest[:j]
	}
	return rest
}

// The landing queue exists because runs that had already done their work were
// reported failed for losing a race on the bookkeeping push.
//
// autorunPushWasRejected is the hinge: it decides "someone landed first" (retry
// fixes it) from every other push failure (retry just repeats it). Getting this
// wrong in either direction is expensive — too narrow and a converged run still
// dies; too broad and we hammer a protected branch or a dead network four times.
//
// The strings below are verbatim git output from the 2026-07-17 mini failures,
// not invented samples.

func TestAutorunPushWasRejectedDetectsALostRace(t *testing.T) {
	// From autorun-1784283068279413000 and …80876139013000 — both converged, both
	// marked failed because of exactly this output.
	real := `To github.com:kivanccakmak/yaver.io.git
 ! [rejected]            main -> main (fetch first)
error: failed to push some refs to 'github.com:kivanccakmak/yaver.io.git'
hint: Updates were rejected because the remote contains work that you do not
hint: have locally. This is usually caused by another repository pushing to
hint: the same ref.`
	if !autorunPushWasRejected(real) {
		t.Fatal("the real push-race output must be retryable; this is the failure the whole queue exists for")
	}

	for _, out := range []string{
		" ! [rejected]        main -> main (non-fast-forward)",
		"hint: Updates were rejected because the tip of your current branch is behind\nfetch first",
		"! [REJECTED] MAIN -> MAIN (FETCH FIRST)", // case must not matter
	} {
		if !autorunPushWasRejected(out) {
			t.Errorf("must retry a lost race; missed: %q", out)
		}
	}
}

func TestAutorunPushWasRejectedIgnoresFailuresARetryCannotFix(t *testing.T) {
	// Auth, network, protected branches: retrying is pure noise, and on a
	// protected branch it is four rejected pushes in someone's audit log.
	for _, out := range []string{
		"",
		"Permission denied (publickey).\nfatal: Could not read from remote repository.",
		"fatal: unable to access 'https://github.com/...': Could not resolve host: github.com",
		"remote: error: GH006: Protected branch update failed for refs/heads/main.",
		"error: src refspec main does not match any",
	} {
		if autorunPushWasRejected(out) {
			t.Errorf("must NOT retry an unfixable push failure; wrongly retried: %q", out)
		}
	}
}

// The lock is only half the fix and must not be mistaken for the whole one: it
// serializes THIS box's autoruns, while the retry handles the laptop, another
// session, or CI. Guard the retry budget so a future edit can't quietly set it
// to 1 and resurrect the bug.
func TestAutorunLandRetryBudgetIsSaneAndBounded(t *testing.T) {
	if autorunLandAttempts < 2 {
		t.Fatalf("autorunLandAttempts=%d leaves no room to rebase and retry — a single lost race would fail the run again", autorunLandAttempts)
	}
	if autorunLandAttempts > 10 {
		t.Fatalf("autorunLandAttempts=%d would hammer the remote on a genuinely stuck branch", autorunLandAttempts)
	}
}

// A rejected push leaves our merge on local main while origin/main has moved, so
// the branches have genuinely diverged — --ff-only is RIGHT to refuse, which is
// why one lost race poisoned the clone and killed the NEXT run before it started
// ("Diverging branches can't be fast-forwarded, aborting"). The retry must
// therefore rebase, and must abort a stuck rebase rather than strand the clone.
func TestAutorunLandUsesRebaseNotFfOnlyAndCleansUp(t *testing.T) {
	src := readSourceFile(t, "autorun.go")
	fn := sliceFunc(t, src, "func autorunLandOntoMain(")

	// The remote is resolved at runtime (autorunRemoteOrOrigin), not hardcoded:
	// this repo's only remote is named `github`, so a literal "origin" here
	// would land nowhere. Assert the rebase and the dynamic remote, never the
	// remote's name — pinning the name is what made this test fail against
	// correct code.
	if !strings.Contains(fn, `"pull", "--rebase", landRemote, "main"`) {
		t.Error("the retry must rebase onto whatever landed; --ff-only cannot resolve a diverged main and re-creates the bug")
	}
	if !strings.Contains(fn, "landRemote := autorunRemoteOrOrigin(") {
		t.Error("the landing remote must be resolved, not hardcoded: this repo's remote is `github`, and `origin` does not exist here")
	}
	if strings.Contains(fn, `"pull", "--ff-only"`) {
		t.Error("--ff-only inside the landing retry: this is exactly what poisoned the clone for the next run")
	}
	if !strings.Contains(fn, `"rebase", "--abort"`) {
		t.Error("a rebase that stops mid-way must be aborted, or the clone is stranded in a rebasing state for every future run")
	}
	if !strings.Contains(fn, "autorunLandMu.Lock()") {
		t.Error("landing must take the queue lock; concurrent autoruns on one box are our own worst racer")
	}
}

// The final bookkeeping commit is pushed by autorunPushBranch, NOT by
// autorunLandOntoMain — and until 2026-07-20 it was a single unretried push.
// That is how the tasklist run on the mini converged, passed its gate, wrote its
// final note, and was still recorded failed:
//
//	push final commit: git push origin main: ! [rejected] main -> main (fetch first)
//
// The retry lived twenty lines away the whole time. This test exists so the two
// post-work pushes cannot drift apart again: whatever landing is hardened
// against, this path must be hardened against too.
func TestAutorunFinalCommitPushRetriesALostRace(t *testing.T) {
	src := readSourceFile(t, "autorun.go")
	fn := sliceFunc(t, src, "func autorunPushBranch(")

	if !strings.Contains(fn, "autorunLandAttempts") {
		t.Error("the final-commit push must retry a lost race; one unretried push is what recorded a converged run as failed")
	}
	// Retrying must be gated on the SAME hinge landing uses. Without it, a
	// protected-branch or auth failure becomes four rejected pushes.
	if !strings.Contains(fn, "autorunPushWasRejected(") {
		t.Error("retry must be gated on autorunPushWasRejected, or auth/network/protected-branch failures get hammered four times")
	}
	// Rebase, not --ff-only: our final commit is already on the local branch, so
	// once the remote moves the two have genuinely diverged.
	if !strings.Contains(fn, `"pull", "--rebase", pushRemote, name`) {
		t.Error("the retry must rebase onto whatever was pushed first; --ff-only cannot resolve a diverged branch")
	}
	if strings.Contains(fn, `"pull", "--ff-only"`) {
		t.Error("--ff-only in the final-commit retry re-creates the diverged-clone failure")
	}
	// Same remote-resolution rule as landing: this repo's remote is `github`.
	if !strings.Contains(fn, "pushRemote := autorunRemoteOrOrigin(") {
		t.Error("the push remote must be resolved, not hardcoded: `origin` does not exist in this repo")
	}
	if !strings.Contains(fn, `"rebase", "--abort"`) {
		t.Error("a rebase that stops mid-way must be aborted, or the clone is stranded for every future run")
	}
}
