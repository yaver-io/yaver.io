package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// opencodeStreamFilter rewrites opencode's raw stdout chunk-by-chunk so
// the stream surfaced to mobile + web matches what claude / codex
// produce. Two concerns:
//
//  1. **Live ANSI scrubbing.** opencode's TUI prints ANSI colour /
//     style escapes (`\x1b[0m`, bare-CSI like `[91m`, …). The
//     persisted `task.ResultText` is scrubbed via stripPromptEcho ->
//     stripANSI on completion, but every live SSE chunk reaching
//     mobile / web during the run shipped the codes verbatim and
//     rendered as literal text in chat bubbles ("[0m> build · …").
//
//  2. **Tool-call rendering parity.** opencode emits shell tool
//     invocations as plain `$ <cmd>` lines. claude (via
//     readStreamJSON) writes those as `\n**$ <cmd>**\n` markdown
//     sentinels — and the mobile + web chat-bubble renderers
//     special-case that marker to render a bold "shell" pill, the
//     same visual treatment as claude's bash tool_use cards. Without
//     this, opencode's commands show as flat prose alongside their
//     output, which reads "less first-class" than the same task run
//     under claude or codex even though the underlying behaviour is
//     identical.
//
// Filtering is line-buffered so the `$ <cmd>` detector only fires on
// a complete line — a partial chunk that ends mid-line stays in
// `leftover` until the next read finishes the line. This matters for
// 8 KB read boundaries: opencode happily writes a `$ ls -la /tmp` and
// the bash output behind it in the same flush, but the surrounding
// chunk size is unrelated to opencode's line boundaries.
type opencodeStreamFilter struct {
	// leftover holds the trailing bytes of the most recent process()
	// call that did not yet end with a newline. ANSI is intentionally
	// kept on the leftover so a CSI sequence split across chunks is
	// stripped only when the line completes (avoids a partial match
	// that could leak `\x1b` into the output).
	leftover []byte

	// task + cmdSeq drive structured command_* events (command cards).
	// opencode's raw stream gives us the command line but no captured
	// output or exit code (that needs the opencode server event stream
	// — the real P3). So we emit command_start + an immediate
	// command_end with exitKnown=false: the card renders the command
	// with a neutral "done" badge and "(no output captured — see
	// transcript)", while the inline `**$ cmd**` pill still carries the
	// narrative + its output below it. task may be nil in unit tests.
	task   *Task
	cmdSeq int
}

// opencodeShellLineRE matches a line whose only "real" content is the
// `$ <command>` form opencode prints when invoking a shell tool. Lines
// like `command output: $ var` (which contain `$ ` but aren't a tool
// call) won't match because we anchor on optional leading whitespace
// followed immediately by `$`. The captured command preserves any
// internal whitespace; outer whitespace is trimmed.
var opencodeShellLineRE = regexp.MustCompile(`^[ \t]*\$[ \t]+(\S.*?\S|\S)[ \t]*$`)

// OpenCode prints a lightweight banner line near the top of each run:
//   > build · glm-4.7
// That is transport metadata, not assistant output. Once mobile/web
// started deriving "what is it doing?" from the raw stream, this
// banner falsely forced trivial commands like `ls` into a
// "compiling…" phase because the word "build" was present. The runner
// + model already live in task metadata and the header chip, so we
// drop the banner from the transcript entirely.
var opencodeBannerLineRE = regexp.MustCompile(`^[ \t]*>[ \t]+[A-Za-z0-9._-]+[ \t]+·[ \t]+[A-Za-z0-9_./:-]+[ \t]*$`)

// process consumes a raw chunk of bytes from opencode's stdout (or
// stderr) and returns the transformed text ready to push onto
// task.outputCh + task.Output. Idempotent w.r.t. ANSI: callers may
// double-strip without ill effect because stripANSI on already-clean
// text is a no-op.
func (f *opencodeStreamFilter) process(chunk []byte) []byte {
	if len(chunk) == 0 {
		return nil
	}
	f.leftover = append(f.leftover, chunk...)
	var out bytes.Buffer
	for {
		i := bytes.IndexByte(f.leftover, '\n')
		if i < 0 {
			break
		}
		line := append([]byte(nil), f.leftover[:i]...)
		f.leftover = f.leftover[i+1:]
		f.writeLine(&out, line, true)
	}
	return out.Bytes()
}

// flush returns whatever partial line remains in the leftover buffer,
// e.g. when the underlying process closes stdout without a trailing
// newline. Safe to call multiple times — subsequent calls return nil.
func (f *opencodeStreamFilter) flush() []byte {
	if len(f.leftover) == 0 {
		return nil
	}
	line := f.leftover
	f.leftover = nil
	var out bytes.Buffer
	f.writeLine(&out, line, false)
	return out.Bytes()
}

func (f *opencodeStreamFilter) writeLine(out *bytes.Buffer, line []byte, hasNewline bool) {
	clean := stripANSI(string(line))
	// Trim trailing CR (opencode runs on Linux but the filter is
	// transport-agnostic — be friendly to a Windows future where the
	// CLI might write CRLF).
	clean = strings.TrimRight(clean, "\r")
	if opencodeBannerLineRE.MatchString(clean) {
		return
	}
	if m := opencodeShellLineRE.FindStringSubmatch(clean); m != nil {
		// Mirror readStreamJSON's claude-side format exactly so the
		// mobile + web chat-bubble renderer that already grep's for
		// `**$ ` doesn't need a second matcher to special-case
		// opencode. Leading newline gives the marker its own block
		// when it lands mid-paragraph; trailing newline keeps any
		// shell output on its own following line.
		out.WriteString("\n**$ ")
		out.WriteString(m[1])
		out.WriteString("**\n")
		// Structured command card (P2P only — never Convex). No output
		// or exit available from opencode's raw stream, so close it
		// immediately with exitKnown=false (neutral "done" badge).
		if f.task != nil {
			f.cmdSeq++
			id := fmt.Sprintf("%s-oc%d", f.task.ID, f.cmdSeq)
			emitCommandStart(f.task, id, m[1], nil, "", "opencode")
			emitCommandEnd(f.task, id, 0, false, 0, false)
		}
		return
	}
	out.WriteString(clean)
	if hasNewline {
		out.WriteByte('\n')
	}
}
