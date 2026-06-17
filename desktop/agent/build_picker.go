package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// buildPickerAvailable reports whether an interactive picker can run — both
// stdin and stdout must be real TTYs and the user must not have opted out of
// color/interactivity. Non-interactive callers (pipes, CI, mobile/MCP) fall
// back to the deterministic dir-match path.
func buildPickerAvailable() bool {
	// A hard opt-out wins; NO_COLOR alone disables styling, not selection.
	if os.Getenv("YAVER_NONINTERACTIVE") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// pickBuildInteractive renders a selectable list of builds (newest first) and
// returns the index the user chose, or (-1, false) if they cancelled. preselect
// is the row highlighted on entry (e.g. the build that matches the current
// directory); pass -1 for the first row.
//
// Controls: ↑/k and ↓/j move, Enter selects, Esc / q / Ctrl-C cancel, and the
// number keys 1-9 jump straight to a row. It's a small hand-rolled menu rather
// than a heavy TUI dependency — the same spirit as the numbered prompts used
// elsewhere in the CLI, but with the arrow-key feel of a modern picker.
func pickBuildInteractive(builds []BuildSummary, preselect int) (int, bool) {
	if len(builds) == 0 {
		return -1, false
	}
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return -1, false
	}
	defer term.Restore(fd, oldState)

	cursor := 0
	if preselect >= 0 && preselect < len(builds) {
		cursor = preselect
	}

	c := buildUseColor()
	render := func(first bool) {
		if !first {
			// Move cursor up over the previously drawn block to repaint
			// in place (header + one line per build + footer).
			fmt.Printf("\033[%dA", len(builds)+2)
		}
		fmt.Print("\r\033[J") // clear from cursor down
		fmt.Printf("  %s\r\n", tcol(c, dimCode, "Select a build — ↑/↓ move · enter view · q cancel"))
		for i, b := range builds {
			marker := "  "
			row := buildPickerRow(b, c)
			if i == cursor {
				marker = tcol(c, cyanCode, "❯ ")
				row = tcol(c, cyanCode, stripANSI(buildPickerRow(b, false)))
			}
			fmt.Printf("%s%s\r\n", marker, row)
		}
		fmt.Printf("  %s\r\n", tcol(c, dimCode, fmt.Sprintf("%d of %d", cursor+1, len(builds))))
	}

	render(true)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return -1, false
		}
		switch {
		case buf[0] == 3 || buf[0] == 'q' || buf[0] == 27 && n == 1: // Ctrl-C, q, bare Esc
			return -1, false
		case buf[0] == '\r' || buf[0] == '\n':
			return cursor, true
		case buf[0] == 'k' || (n == 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A'): // up
			if cursor > 0 {
				cursor--
				render(false)
			}
		case buf[0] == 'j' || (n == 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B'): // down
			if cursor < len(builds)-1 {
				cursor++
				render(false)
			}
		case buf[0] >= '1' && buf[0] <= '9':
			idx := int(buf[0] - '1')
			if idx < len(builds) {
				cursor = idx
				render(false)
			}
		}
	}
}

// buildPickerRow renders one build as a single line: status glyph, short id,
// friendly platform, project dir, and age.
func buildPickerRow(b BuildSummary, c bool) string {
	glyph, _, color := humanBuildState(Build{Status: b.Status})
	proj := ""
	if b.WorkDir != "" {
		proj = filepath.Base(strings.TrimRight(b.WorkDir, string(filepath.Separator)))
	}
	age := ""
	switch b.Status {
	case BuildStatusRunning:
		if d, ok := sinceTime(b.StartedAt); ok {
			age = "running " + fmtBuildDur(d)
		}
	default:
		if d, ok := sinceTime(b.FinishedAt); ok {
			age = fmtBuildDur(d) + " ago"
		} else if d, ok := sinceTime(b.StartedAt); ok {
			age = fmtBuildDur(d) + " ago"
		}
	}
	line := fmt.Sprintf("%s %-8s  %-26s", tcol(c, color, glyph), b.ID, friendlyPlatform(b.Platform))
	if proj != "" {
		line += "  " + proj
	}
	if age != "" {
		line += "  " + tcol(c, dimCode, age)
	}
	return line
}
