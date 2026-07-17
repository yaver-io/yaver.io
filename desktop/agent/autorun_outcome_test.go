package main

import (
	"errors"
	"fmt"
	"testing"
)

// The work outcome and the landing outcome are two questions, and merging them
// told the user his working autorun had failed.
//
// autorun-1784283068279413000 on the mini: 3 iterations, gate passed, converged,
// commits real — recorded `status: failed` because the final push came back
// `! [rejected] main -> main (fetch first)`. executeAutorun returns ONE error, so
// the bookkeeping failure became the run's verdict.

func TestAutorunWorkSucceededOnlyForRealSuccess(t *testing.T) {
	for _, reason := range []string{autorunReasonConverged, autorunReasonDone} {
		if !autorunWorkSucceeded(reason) {
			t.Errorf("%q means the loop did its job", reason)
		}
	}
	// A gate failure or a scope violation is the WORK failing. A landing error on
	// top of one of these must never launder it into "completed".
	for _, reason := range []string{
		autorunReasonGate, autorunReasonRunner, autorunReasonScope,
		autorunReasonStopped, autorunReasonResources, autorunReasonMaxIters, "",
	} {
		if autorunWorkSucceeded(reason) {
			t.Errorf("%q is not a successful run; a landing error must not make it one", reason)
		}
	}
}

func TestAutorunLandingErrorIsDistinguishableAndUnwraps(t *testing.T) {
	// Verbatim from the mini failure.
	root := errors.New("push final commit: ! [rejected] main -> main (fetch first)")
	wrapped := asAutorunLandingError(root)

	var landing *autorunLandingError
	if !errors.As(wrapped, &landing) {
		t.Fatal("a landing failure must be recognisable, or the run keeps wearing the loss")
	}
	if !errors.Is(wrapped, root) {
		t.Fatal("wrapping must not hide the cause: errors.Is has to still reach it")
	}
	if wrapped.Error() != root.Error() {
		t.Fatalf("the message must survive verbatim for the operator; got %q", wrapped.Error())
	}

	// Nil in, nil out — call sites wrap unconditionally.
	if asAutorunLandingError(nil) != nil {
		t.Fatal("asAutorunLandingError(nil) must stay nil")
	}
}

// A WORK failure must never be mistaken for a landing failure, even when the
// landing also failed. That is the …80876139013000 case: the gate failed AND the
// push was rejected, and it is honestly a failure.
func TestAutorunWorkFailureIsNotLaunderedByALandingError(t *testing.T) {
	gate := errors.New("gate failed; changes were not committed")
	// executeAutorun reports this combined, with the WORK error primary and
	// deliberately NOT tagged as a landing error.
	combined := fmt.Errorf("%w (recording the final autorun commit also failed: %v)",
		gate, errors.New("push final commit: ! [rejected] main -> main (fetch first)"))

	var landing *autorunLandingError
	if errors.As(combined, &landing) {
		t.Fatal("a gate failure that also failed to push is a FAILED run; tagging it landing-only would hide a real regression")
	}
	if !errors.Is(combined, gate) {
		t.Fatal("the work failure must remain the primary cause")
	}
}

// The classifier's contract, as autorun_ops.go applies it.
func TestAutorunOutcomeClassification(t *testing.T) {
	pushRace := asAutorunLandingError(errors.New("push final commit: ! [rejected] main -> main (fetch first)"))

	cases := []struct {
		name   string
		reason string
		err    error
		want   string
	}{
		{"converged and landed", autorunReasonConverged, nil, "completed"},
		{"converged, push raced", autorunReasonConverged, pushRace, "completed"},
		{"done, push raced", autorunReasonDone, pushRace, "completed"},
		{"gate failed", autorunReasonGate, errors.New("gate failed"), "failed"},
		{"gate failed, push also raced", autorunReasonGate, pushRace, "failed"},
		{"runner failed", autorunReasonRunner, errors.New("runner failed"), "failed"},
	}

	for _, tc := range cases {
		got := "completed"
		if tc.err != nil {
			var landing *autorunLandingError
			switch {
			case errors.As(tc.err, &landing) && autorunWorkSucceeded(tc.reason):
				got = "completed"
			default:
				got = "failed"
			}
		}
		if got != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.name, got, tc.want)
		}
	}
}
