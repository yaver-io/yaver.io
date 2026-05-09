package main

// agent_question_fallback_test.go — corpus test for the prose-question
// detector. Precision is the priority: false positives push a useless
// modal in front of the user every few seconds. Add new TRUE cases
// freely, but every FALSE case is a guard against future regressions.

import "testing"

func TestMatchSoftQuestion(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantHit  bool
	}{
		// True positives: real prose questions Claude / Codex / OpenCode
		// have been observed emitting at the end of a turn.
		{"should_i_apply", "I've staged the change. Should I apply the migration to the prod database?", true},
		{"would_you_like_me", "Done with the refactor. Would you like me to also update the snapshot tests?", true},
		{"do_you_want_me", "I have two options here. Do you want me to use bun or npm for the new install?", true},
		{"which_framework", "Multiple frontend frameworks detected. Which framework do you prefer for the new app?", true},
		{"please_confirm", "This will delete 47 rows. Please confirm before I proceed.", true},
		{"please_let_me_know", "I can pick either approach. Please let me know which one to take.", true},
		{"midstream_should_i", "Started the build.\nShould I cancel the running deploy first?", true},

		// True negatives: text that LOOKS questiony but isn't actually
		// the agent stopping to ask. Each one has been observed firing
		// the regex during pattern-tuning and required guarding.
		{"quoted_doc_string", "// e.g. \"Should I do X?\" is a common question", false},
		{"git_commit_msg", "git commit -m 'fix: should_i flag for askFreely'", false},
		{"shell_var", "export SHOULD_I_RUN=1", false},
		{"midword_match", "shouldice_method.go updated", false},
		{"prose_no_question_mark", "I should figure out what to do next", false},
		{"long_runaway", "Should I do " + repeat("a", 300), false}, // capped at 160 chars to avoid runaway
		{"unrelated_q", "What is the meaning of life?", false},
	}

	for _, tc := range cases {
		got := matchSoftQuestion(tc.input)
		hit := got != ""
		if hit != tc.wantHit {
			t.Errorf("%s: input=%q got=%q (hit=%v), want hit=%v", tc.name, tc.input, got, hit, tc.wantHit)
		}
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
