package main

import "testing"

// The DONE marker decides when a loop stops. It used to be a substring test over
// the progress file — and the progress file is where the doer's own transcript
// gets appended every iteration, so the runner was one stray sentence away from
// ending its own run. On 2026-07-17 it did exactly that: an ambient-slots loop
// wrote "I did not run the full project gate, so this is not marked `DONE`" and
// autorun stopped, one iteration into a six-part task, reporting success.
//
// Every negative case below is real prose a runner plausibly writes.
func TestAutorunMarksDone(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want bool
	}{
		// The bug, verbatim from docs/handoff/ambient-slots-progress.md line 46.
		{
			name: "a sentence denying completion is not completion",
			text: "Verification: `web: npx tsc --noEmit` passed. I did not run the full project gate, so this is not marked `DONE`.",
			want: false,
		},
		{
			name: "quoting the contract is not completion",
			text: "The task file says to say DONE, alone, only when the work is verified in the git log.",
			want: false,
		},
		{
			name: "the loop's own commit subject is not completion",
			text: "autorun: final autorun commit for ambient-slots (task marked DONE)",
			want: false,
		},
		{
			name: "finish-reason bookkeeping is not completion",
			text: "Finish reason: task marked DONE\nIterations run: 1",
			want: false,
		},
		{
			name: "planning to finish later is not completion",
			text: "Once section 6 lands and the gate is green I will report DONE.",
			want: false,
		},

		{
			name: "DONE alone on a line",
			text: "DONE",
			want: true,
		},
		{
			name: "DONE alone after a report",
			text: "Ported agentSlots to web/lib and deleted the hex maps.\nGate green.\n\nDONE\n",
			want: true,
		},
		{
			name: "markdown decoration around the marker still marks done",
			text: "**DONE**",
			want: true,
		},
		{
			name: "backticked marker still marks done",
			text: "- `DONE`",
			want: true,
		},
		{
			name: "empty text does not mark done",
			text: "",
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := autorunMarksDone(tc.text); got != tc.want {
				t.Fatalf("autorunMarksDone(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
