package main

import "testing"

func TestParseYaverActionSentinel(t *testing.T) {
	cases := []struct {
		in       string
		wantOK   bool
		wantVerb string
		wantArgs string
	}{
		// Happy paths.
		{"<<yaver-action: reload sfmg>>", true, "reload", "sfmg"},
		{"<<yaver-action: reload carrotbet>>", true, "reload", "carrotbet"},
		{"<<yaver-action: open sfmg>>", true, "open", "sfmg"},
		{"<<yaver-action: serve todo-kt>>", true, "serve", "todo-kt"},
		// Whitespace tolerance — runners often indent stream output.
		{"   <<yaver-action: reload sfmg>>", true, "reload", "sfmg"},
		{"<<yaver-action:   reload   sfmg   >>", true, "reload", "sfmg"},
		{"<<yaver-action: reload sfmg>>   ", true, "reload", "sfmg"},
		// Verb-only sentinel (no args). Dispatcher rejects empty slug
		// downstream, so we still want the parse to succeed and let it
		// log a useful warning.
		{"<<yaver-action: reload>>", true, "reload", ""},
		// Case-insensitive verb (LLM might capitalize).
		{"<<yaver-action: RELOAD sfmg>>", true, "reload", "sfmg"},
		// Multi-word slug — preserved verbatim, dispatcher decides
		// whether to interpret. Useful if we add `serve <port>` later.
		{"<<yaver-action: reload sfmg ios>>", true, "reload", "sfmg ios"},

		// Misses — must NOT false-fire.
		{"this is not a sentinel", false, "", ""},
		{"<yaver-action: reload sfmg>", false, "", ""},
		{"<<yaver: reload sfmg>>", false, "", ""},
		{"prefix <<yaver-action: reload sfmg>>", false, "", ""},
		{"<<yaver-action: reload sfmg>> trailing", false, "", ""},
		{"", false, "", ""},
		// Verb starting with digit / unknown shape — regex rejects.
		{"<<yaver-action: 9reload sfmg>>", false, "", ""},
	}
	for _, c := range cases {
		gotVerb, gotArgs, gotOK := ParseYaverActionSentinel(c.in)
		if gotOK != c.wantOK || gotVerb != c.wantVerb || gotArgs != c.wantArgs {
			t.Errorf("ParseYaverActionSentinel(%q) = (%q, %q, %v); want (%q, %q, %v)",
				c.in, gotVerb, gotArgs, gotOK, c.wantVerb, c.wantArgs, c.wantOK)
		}
	}
}

func TestDispatchYaverAction_NoManager(t *testing.T) {
	// Reset the global to nil and confirm dispatch is a silent no-op
	// rather than panicking. Important because pump goroutines fire
	// dispatches off the hot streaming path; a panic would crash the
	// agent for what should be a best-effort signal.
	prev := activeBlackboxMgr.Load()
	activeBlackboxMgr.Store(nil)
	defer activeBlackboxMgr.Store(prev)

	// Should not panic. No assertion needed — the test passes if the
	// call returns.
	DispatchYaverAction("reload", "sfmg", "test-task-id")
	DispatchYaverAction("unknown-verb", "ignored", "test-task-id")
	DispatchYaverAction("reload", "", "test-task-id")
}
