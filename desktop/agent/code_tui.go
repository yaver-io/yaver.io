package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// code_tui.go — the "lean" render loop for `yaver code --mesh`.
//
// No external TUI framework. Raw ANSI. Animated braille spinner, color
// chips for queued/running/completed/failed, and a single rolling tail
// panel showing the most recent output from whichever node last spoke.
//
// If stdout is not a TTY or the caller passed --plain, streamCodeGraph
// still prints the line-based fallback. This file stays lean and
// additive; it never replaces the plain path.

// ANSI constants ansiReset/ansiDim/ansiBold/ansiCyan come from stream_cmd.go.
const (
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"

	ansiHideCursor  = "\x1b[?25l"
	ansiShowCursor  = "\x1b[?25h"
	ansiClearScreen = "\x1b[2J\x1b[H"
	ansiClearLine   = "\x1b[2K"
	ansiCursorHome  = "\x1b[H"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// streamCodeGraphTUI is the TTY variant of streamCodeGraph. It polls the
// agent graph and paints a full frame every ~200ms. Returns when the
// graph is completed/failed/stopped.
func streamCodeGraphTUI(runID string) error {
	fmt.Print(ansiHideCursor)
	defer fmt.Print(ansiShowCursor)
	defer fmt.Print("\n")

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	tail := newTailBuffer(8)
	offsets := map[string]int{}
	started := time.Now()
	frame := 0

	for {
		run, err := fetchCodeGraph(runID)
		if err != nil {
			fmt.Print(ansiShowCursor)
			return err
		}
		for _, node := range run.Nodes {
			if node.TaskID == "" {
				continue
			}
			if err := collectNodeDelta(node, offsets, tail); err != nil {
				return err
			}
		}
		renderCodeGraphFrame(run, tail, frame, time.Since(started))
		frame++

		switch run.Status {
		case AgentGraphCompleted, AgentGraphFailed, AgentGraphStopped:
			fmt.Println()
			if strings.TrimSpace(run.Summary) != "" {
				fmt.Printf("%s%s%s\n", ansiDim, run.Summary, ansiReset)
			}
			return nil
		}
		<-tick.C
	}
}

func collectNodeDelta(node *AgentGraphNodeState, offsets map[string]int, tail *tailBuffer) error {
	resp, err := localAgentRequest("GET", "/tasks/"+node.TaskID, nil)
	if err != nil {
		return err
	}
	taskMap, ok := resp["task"]
	if !ok {
		return nil
	}
	data, _ := json.Marshal(taskMap)
	var task TaskInfo
	if err := json.Unmarshal(data, &task); err != nil {
		return err
	}
	prev := offsets[task.ID]
	if len(task.Output) <= prev {
		return nil
	}
	label := graphNodeLabel(node)
	for _, line := range splitLinesClean(task.Output[prev:]) {
		tail.Push(label, line)
	}
	offsets[task.ID] = len(task.Output)
	return nil
}

func renderCodeGraphFrame(run *AgentGraphRun, tail *tailBuffer, frame int, elapsed time.Duration) {
	// Clear and home — rewrite the full frame so layout stays stable
	// across terminal resizes and wrapping.
	fmt.Print(ansiClearScreen)

	spin := spinnerFrames[frame%len(spinnerFrames)]

	header := fmt.Sprintf("%s%syaver code%s · %s · %s   %s%s%s",
		ansiBold, ansiCyan, ansiReset,
		run.Name, run.ID,
		ansiDim, humanElapsed(elapsed), ansiReset,
	)
	fmt.Println(header)
	fmt.Println()

	for _, node := range run.Nodes {
		fmt.Println(formatNodeLine(node, spin))
	}

	fmt.Println()
	renderTail(tail)
}

func formatNodeLine(node *AgentGraphNodeState, spin string) string {
	status := string(node.Status)
	label := node.Spec.Title
	if label == "" {
		label = node.Spec.ID
	}
	id := node.Spec.ID

	var statusChip, glyph string
	switch node.Status {
	case AgentNodeRunning:
		glyph = ansiCyan + spin + ansiReset
		statusChip = ansiCyan + "[running]" + ansiReset
	case AgentNodeCompleted:
		glyph = ansiGreen + "✓" + ansiReset
		statusChip = ansiGreen + "[done]   " + ansiReset
	case AgentNodeFailed:
		glyph = ansiRed + "✗" + ansiReset
		statusChip = ansiRed + "[failed] " + ansiReset
	case AgentNodeStopped:
		glyph = ansiDim + "·" + ansiReset
		statusChip = ansiDim + "[stopped]" + ansiReset
	default:
		glyph = ansiDim + "·" + ansiReset
		statusChip = ansiDim + "[queued] " + ansiReset
	}

	placement := ""
	if node.Placement != nil {
		host := node.Placement.DeviceNameOrID()
		runner := node.Placement.Runner
		if host != "" || runner != "" {
			parts := []string{}
			if host != "" {
				parts = append(parts, host)
			}
			if runner != "" {
				parts = append(parts, runner)
			}
			placement = ansiDim + strings.Join(parts, " / ") + ansiReset
		}
	}

	_ = status
	idCol := fmt.Sprintf("%-14s", clip(id, 14))
	return fmt.Sprintf("  %s  %s  %s  %s", glyph, statusChip, idCol, placement)
}

func renderTail(tail *tailBuffer) {
	if tail == nil {
		return
	}
	lines := tail.Lines()
	if len(lines) == 0 {
		fmt.Printf("%s── waiting for output ──%s\n", ansiDim, ansiReset)
		return
	}
	lastLabel := tail.LastLabel()
	fmt.Printf("%s── tail: %s ──%s\n", ansiDim, lastLabel, ansiReset)
	for _, l := range lines {
		fmt.Printf("  %s\n", clip(l, 116))
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func humanElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) - mins*60
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

func splitLinesClean(delta string) []string {
	normalized := strings.ReplaceAll(delta, "\r\n", "\n")
	out := []string{}
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// tailBuffer keeps the most recent N log lines across all nodes.
type tailBuffer struct {
	mu        sync.Mutex
	capacity  int
	lines     []string
	lastLabel string
}

func newTailBuffer(cap int) *tailBuffer {
	if cap <= 0 {
		cap = 6
	}
	return &tailBuffer{capacity: cap}
}

func (b *tailBuffer) Push(label, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastLabel = label
	b.lines = append(b.lines, line)
	if len(b.lines) > b.capacity {
		b.lines = b.lines[len(b.lines)-b.capacity:]
	}
}

func (b *tailBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

func (b *tailBuffer) LastLabel() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastLabel == "" {
		return "(none)"
	}
	return b.lastLabel
}
