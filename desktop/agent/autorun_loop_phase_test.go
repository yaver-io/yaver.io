package main

import (
	"strings"
	"testing"
)

// The policy tests in autorun_leases_test.go prove the phase table is right.
// This proves the LOOP actually performs it — the difference between an overlap
// that is allowed and one that happens.
//
// Asserted against the loop's source because the alternative is standing up a
// runner, a worktree and a gate to observe two lease calls. The invariant is
// about which calls surround the gate, and that is decided here.
func TestLoopHandsBackTheSeatAroundTheGate(t *testing.T) {
	src := readSourceFile(t, "autorun_cmd.go")
	fn := sliceFunc(t, src, "func autorunLoop(")

	gate := strings.Index(fn, `autorunExec(ctx, "sh", []string{"-lc", opts.Gate}`)
	if gate < 0 {
		t.Fatal("gate invocation not found — it was renamed; update this test deliberately")
	}
	before, after := fn[:gate], fn[gate:]

	// The seat must be released BEFORE the compiler runs. This is the whole
	// point: while this loop waits on a build, its runner belongs to a sibling.
	if !strings.Contains(before, "autorunLeases.Release(opts.SessionID, seatLease(runner.RunnerID))") {
		t.Error("the seat is not handed back before the gate — a build would keep holding the runner it is not using, and unrelated work would queue behind a compiler")
	}
	if !strings.Contains(before, `autorunPhaseLeases("build"`) {
		t.Error("the build phase leases are not taken before the gate")
	}

	// ...and the toolchain must be released after it, whatever the verdict.
	if !strings.Contains(after, "autorunLeases.Release(opts.SessionID, buildLease(t))") {
		t.Error("the build target is not released after the gate — a failed gate would strand the toolchain until TTL and block every sibling")
	}
	if !strings.Contains(after, `autorunPhaseLeases("edit"`) {
		t.Error("the seat is not re-taken after the gate, so the next iteration would edit without holding its own runner")
	}
}

// The release must come before the gate's error branches return, or a failed
// gate leaks the toolchain. That ordering is easy to get wrong and invisible
// until a build fails at 3am.
func TestLoopReleasesBuildLeaseBeforeGateErrorReturns(t *testing.T) {
	src := readSourceFile(t, "autorun_cmd.go")
	fn := sliceFunc(t, src, "func autorunLoop(")

	release := strings.Index(fn, "autorunLeases.Release(opts.SessionID, buildLease(t))")
	if release < 0 {
		t.Fatal("build lease release not found")
	}
	gateFailReturn := strings.Index(fn, "return autorunReasonGate,")
	if gateFailReturn < 0 {
		t.Fatal("gate failure return not found")
	}
	if release > gateFailReturn {
		t.Error("the build lease is released only after the gate-failure return — a failed gate would hold the toolchain until TTL")
	}
}

// Lease trouble must never fail a gate. The claims are a fleet optimisation;
// the gate is the correctness oracle, and losing verified work to a contended
// lease would be strictly worse than a slower fleet.
func TestLoopDoesNotFailTheGateOnLeaseContention(t *testing.T) {
	src := readSourceFile(t, "autorun_cmd.go")
	fn := sliceFunc(t, src, "func autorunLoop(")

	i := strings.Index(fn, `autorunPhaseLeases("build"`)
	if i < 0 {
		t.Fatal("build phase acquire not found")
	}
	end := i + 700
	if end > len(fn) {
		end = len(fn)
	}
	window := fn[i:end]
	if strings.Contains(window, "return autorunReason") {
		t.Error("a contended build lease returns from the loop — throughput must never cost correctness")
	}
	if !strings.Contains(window, "gating anyway") {
		t.Error("contention should be recorded and proceed, so the progress file explains the slower run")
	}
}
