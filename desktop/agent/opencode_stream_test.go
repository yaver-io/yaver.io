package main

import (
	"strings"
	"testing"
)

func TestOpencodeStreamFilter_StripsAnsiAndRewritesShellLines(t *testing.T) {
	f := &opencodeStreamFilter{}
	// Sample copied from a real opencode run against zai/glm-4.7. Mix
	// of TUI banner, ANSI-wrapped `$ <cmd>` markers, command output,
	// and plain prose. After filtering we expect:
	//   - all CSI / OSC escapes gone
	//   - `$ ls -la /tmp | head -10` rewritten as `\n**$ ls -la /tmp | head -10**\n`
	//   - command output untouched
	in := "\x1b[0m\n> build · glm-4.7\n\x1b[0m\n[0m$ [0mls -la /tmp | head -3\n" +
		"total 4\n" +
		"drwxr-xr-x 2 root root 4096 May  4 19:00 .\n" +
		"\x1b[0m$ \x1b[0mls -1 /tmp | head -1\n" +
		"foo\n" +
		"Here are the entries.\n"

	got := string(f.process([]byte(in)))

	if strings.Contains(got, "\x1b") {
		t.Fatalf("ANSI escape leaked into output:\n%q", got)
	}
	if strings.Contains(got, "[0m") {
		t.Fatalf("bare CSI leaked into output:\n%q", got)
	}
	if strings.Contains(got, "build · glm-4.7") {
		t.Fatalf("opencode banner leaked into output:\n%s", got)
	}
	if !strings.Contains(got, "**$ ls -la /tmp | head -3**") {
		t.Errorf("first shell line not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "**$ ls -1 /tmp | head -1**") {
		t.Errorf("second shell line not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "total 4") {
		t.Errorf("command output dropped:\n%s", got)
	}
	if !strings.Contains(got, "Here are the entries.") {
		t.Errorf("trailing prose dropped:\n%s", got)
	}
	// Lines that are NOT shell markers must keep their original spacing.
	if strings.Contains(got, "**$ total 4**") {
		t.Errorf("non-shell line incorrectly rewritten as shell marker:\n%s", got)
	}
}

func TestOpencodeStreamFilter_HandlesChunkBoundariesMidLine(t *testing.T) {
	// Same total content as the previous test, but split into chunks
	// that sever lines arbitrarily — including breaking a `$ ` marker
	// across the boundary AND breaking an ANSI escape across the
	// boundary. The filter must produce identical output to the
	// single-shot case for the streaming path to be correct.
	full := "\x1b[0m$ \x1b[0mls -la /tmp\ntotal 4\n\x1b[0m$ \x1b[0mecho ok\nok\n"
	expectedSubs := []string{"**$ ls -la /tmp**", "**$ echo ok**", "total 4", "ok"}

	for _, splitAt := range []int{1, 4, 9, 13, 17, 25, 32, 38, 44} {
		f := &opencodeStreamFilter{}
		var out strings.Builder
		out.Write(f.process([]byte(full[:splitAt])))
		out.Write(f.process([]byte(full[splitAt:])))
		out.Write(f.flush())
		got := out.String()
		if strings.Contains(got, "\x1b") {
			t.Errorf("split=%d: ANSI escape leaked: %q", splitAt, got)
		}
		for _, sub := range expectedSubs {
			if !strings.Contains(got, sub) {
				t.Errorf("split=%d: missing %q in:\n%s", splitAt, sub, got)
			}
		}
	}
}

func TestOpencodeStreamFilter_FlushPartialLineWithoutTrailingNewline(t *testing.T) {
	f := &opencodeStreamFilter{}
	// Process a chunk that ends mid-line, then flush — the partial
	// line must surface (sans ANSI). This matches the real-world case
	// where opencode's process exits without flushing a final \n.
	out := string(f.process([]byte("\x1b[0msome partial output without newline")))
	out += string(f.flush())
	if strings.Contains(out, "\x1b") {
		t.Fatalf("ANSI escape leaked on flush: %q", out)
	}
	if !strings.Contains(out, "some partial output without newline") {
		t.Fatalf("flushed partial line missing: %q", out)
	}
}

func TestOpencodeStreamFilter_OnlyRewritesLinesStartingWithDollarSpace(t *testing.T) {
	// Lines where `$ <token>` appears anywhere except at the start of
	// the line must NOT be rewritten as a shell marker — the heuristic
	// is "this whole line is opencode's tool-call sentinel", and prose
	// that happens to embed `$ foo` mid-sentence is not that.
	// A clean line that DOES start with `$ ` should match.
	f := &opencodeStreamFilter{}
	got := string(f.process([]byte(
		"the env var $HOME was empty\n" +
			"let me try: $ env\n" +
			"$ env\n")))
	if strings.Contains(got, "**$ HOME**") {
		t.Errorf("mid-line $HOME wrongly rewritten:\n%s", got)
	}
	// "let me try: $ env" — `$ env` is mid-line, must stay as-is.
	if strings.Contains(got, "**$ env**\n  ") || strings.Count(got, "**$ env**") != 1 {
		t.Errorf("mid-line `$ env` wrongly rewritten:\n%s", got)
	}
	if !strings.Contains(got, "**$ env**") {
		t.Errorf("standalone `$ env` line should be rewritten:\n%s", got)
	}
}
