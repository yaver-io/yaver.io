package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// runCodeTerminal is yaver code's dedicated raw-mode interactive TUI.
//
// Scope is intentionally narrow: it only owns the `yaver code`
// local-interactive path. The mobile/web wrapping surfaces
// (runAttach + client.go remote sessions) are untouched and keep
// their line-buffered scanner contract. If stdin or stdout isn't a
// TTY we transparently fall back to runAttach so scripted callers
// still work.
func runCodeTerminal(runner, model string) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		attachArgs := make([]string, 0, 4)
		if r := strings.TrimSpace(runner); r != "" {
			attachArgs = append(attachArgs, "--runner", r)
		}
		if m := strings.TrimSpace(model); m != "" {
			attachArgs = append(attachArgs, "--model", m)
		}
		runAttach(attachArgs)
		return
	}

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	if _, running := isAgentRunning(); !running {
		fmt.Fprintln(os.Stderr, "Agent is not running. Run 'yaver serve' or 'yaver auth' first.")
		os.Exit(1)
	}

	baseURL := "http://127.0.0.1:18080"
	info, err := attachGetInfo(baseURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to agent: %v\n", err)
		os.Exit(1)
	}

	sess := &codeTerminalSession{
		baseURL: baseURL,
		token:   cfg.AuthToken,
		info:    info,
		opts: attachSessionOptions{
			Source:        terminalLocalTaskSource,
			DefaultRunner: strings.TrimSpace(runner),
			DefaultModel:  strings.TrimSpace(model),
		},
		knownTasks:    map[string]bool{},
		lastOutputLen: map[string]int{},
		firstDraw:     true,
	}
	if err := sess.run(); err != nil {
		fmt.Fprintf(os.Stderr, "code: %v\n", err)
		os.Exit(1)
	}
}

type codeTerminalSession struct {
	baseURL string
	token   string
	info    *attachInfo
	opts    attachSessionOptions

	// editor state — owned by main goroutine
	buf    []rune
	cursor int

	// palette state
	paletteActive   bool
	paletteOptions  []string
	paletteSelected int

	// poll bookkeeping
	knownTasks    map[string]bool
	lastOutputLen map[string]int
	activeTask    string

	// firstDraw is true until draw() has rendered the prompt block at
	// least once. Used so the first frame doesn't try to climb up to a
	// label line that doesn't exist yet, and so output that scrolled
	// the prompt off-screen (clearPromptArea) can flip back to "fresh".
	firstDraw bool

	// extraLinesBelow tracks how many trailing lines the prompt block
	// painted below the input cursor (e.g. footer/help). Used by
	// clearPromptArea / repaint helpers to climb back up correctly.
	extraLinesBelow int

	// guards stdout writes between the main loop and any future
	// fan-out — today only the main goroutine writes, so this is
	// just a defensive seatbelt.
	writeMu sync.Mutex
}

func (s *codeTerminalSession) run() error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Welcome banner. printAttachWelcome uses fmt.Println which emits
	// "\n" — that's still fine in raw mode because the terminal driver
	// turns line-feeds into CRLF when ONLCR is the default. We force
	// CR ourselves below for safety on terminals that don't.
	s.writeRaw(rawifyLines(captureStdout(func() { printAttachWelcome(s.info) })))

	if tasks, err := attachListTasks(s.baseURL, s.token); err == nil {
		for _, t := range tasks {
			s.knownTasks[t.ID] = true
			s.lastOutputLen[t.ID] = len(t.Output)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdinCh := make(chan byte, 256)
	go readStdinBytes(ctx, stdinCh)

	pollCh := make(chan []TaskInfo, 4)
	go pollTasks(ctx, s.baseURL, s.token, pollCh)

	s.draw()

	for {
		select {
		case b, ok := <-stdinCh:
			if !ok {
				s.clearPromptArea()
				return nil
			}
			done, exit, err := s.handleKey(b, stdinCh)
			if err != nil {
				return err
			}
			if exit {
				s.clearPromptArea()
				s.writeRaw("\r\nDetached from agent. Agent continues running in background.\r\n")
				return nil
			}
			if done {
				return nil
			}
		case tasks := <-pollCh:
			s.applyPoll(tasks)
		}
	}
}

// readStdinBytes is the only goroutine that ever calls os.Stdin.Read.
// Bytes are pushed onto stdinCh one at a time; the main loop owns
// escape-sequence parsing.
func readStdinBytes(ctx context.Context, ch chan<- byte) {
	defer close(ch)
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		select {
		case ch <- buf[0]:
		case <-ctx.Done():
			return
		}
	}
}

func pollTasks(ctx context.Context, baseURL, token string, ch chan<- []TaskInfo) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tasks, err := attachListTasks(baseURL, token)
			if err != nil {
				continue
			}
			select {
			case ch <- tasks:
			case <-ctx.Done():
				return
			}
		}
	}
}

// handleKey is called for every byte the user types. It also consumes
// extra bytes from stdinCh when parsing CSI escape sequences.
//
// Returns (done, exit, err): done=session ended without explicit
// detach message, exit=detach + print farewell.
func (s *codeTerminalSession) handleKey(b byte, stdinCh <-chan byte) (bool, bool, error) {
	switch b {
	case 0x03: // Ctrl+C
		return false, true, nil
	case 0x04: // Ctrl+D — also treat as detach when buffer empty
		if len(s.buf) == 0 {
			return false, true, nil
		}
	case 0x0C: // Ctrl+L — clear screen
		s.writeRaw("\033[2J\033[H")
		s.firstDraw = true
		s.draw()
		return false, false, nil
	case 0x15: // Ctrl+U — clear current input line
		s.buf = s.buf[:0]
		s.cursor = 0
		s.recomputePalette()
		s.draw()
		return false, false, nil
	case 0x17: // Ctrl+W — delete word backward
		s.deleteWordBack()
		s.recomputePalette()
		s.draw()
		return false, false, nil
	case '\r', '\n':
		return s.submit()
	case 0x7F, 0x08: // Backspace / DEL
		if s.cursor > 0 {
			s.buf = append(s.buf[:s.cursor-1], s.buf[s.cursor:]...)
			s.cursor--
		}
		s.recomputePalette()
		s.draw()
		return false, false, nil
	case 0x09: // Tab — accept palette selection into buffer
		if s.paletteActive && len(s.paletteOptions) > 0 {
			pick := s.paletteOptions[s.paletteSelected]
			s.buf = []rune(pick)
			s.cursor = len(s.buf)
			s.recomputePalette()
			s.draw()
		}
		return false, false, nil
	case 0x1B: // ESC — start of CSI or standalone cancel
		seq, ok := readEscapeSequence(stdinCh)
		if !ok {
			// lone ESC — cancel palette / clear buffer
			if s.paletteActive {
				s.buf = s.buf[:0]
				s.cursor = 0
				s.recomputePalette()
				s.draw()
			} else {
				s.buf = s.buf[:0]
				s.cursor = 0
				s.draw()
			}
			return false, false, nil
		}
		s.handleEscape(seq)
		return false, false, nil
	}

	if b >= 0x20 && b != 0x7F {
		r := rune(b)
		if s.cursor == len(s.buf) {
			s.buf = append(s.buf, r)
		} else {
			s.buf = append(s.buf[:s.cursor], append([]rune{r}, s.buf[s.cursor:]...)...)
		}
		s.cursor++
		s.recomputePalette()
		s.draw()
	}
	return false, false, nil
}

// readEscapeSequence reads the rest of a CSI/SS3 escape sequence after
// the leading 0x1B has already been consumed. Returns the trailing
// "[A" / "OA" / etc. Falls back to (nil,false) on a lone ESC.
func readEscapeSequence(stdinCh <-chan byte) ([]byte, bool) {
	select {
	case b := <-stdinCh:
		if b != '[' && b != 'O' {
			// unknown 2-byte ESC sequence; swallow it
			return []byte{b}, true
		}
		seq := []byte{b}
		// CSI: read until a final byte in 0x40-0x7E
		for {
			select {
			case c := <-stdinCh:
				seq = append(seq, c)
				if c >= 0x40 && c <= 0x7E {
					return seq, true
				}
				if len(seq) > 16 {
					return seq, true
				}
			case <-time.After(50 * time.Millisecond):
				return seq, true
			}
		}
	case <-time.After(50 * time.Millisecond):
		return nil, false
	}
}

func (s *codeTerminalSession) handleEscape(seq []byte) {
	if len(seq) < 2 {
		return
	}
	if seq[0] != '[' && seq[0] != 'O' {
		return
	}
	final := seq[len(seq)-1]
	switch final {
	case 'A': // up
		if s.paletteActive && len(s.paletteOptions) > 0 {
			if s.paletteSelected > 0 {
				s.paletteSelected--
			}
			s.draw()
		}
	case 'B': // down
		if s.paletteActive && len(s.paletteOptions) > 0 {
			if s.paletteSelected < len(s.paletteOptions)-1 {
				s.paletteSelected++
			}
			s.draw()
		}
	case 'C': // right
		if s.cursor < len(s.buf) {
			s.cursor++
			s.draw()
		}
	case 'D': // left
		if s.cursor > 0 {
			s.cursor--
			s.draw()
		}
	case 'H': // home
		s.cursor = 0
		s.draw()
	case 'F': // end
		s.cursor = len(s.buf)
		s.draw()
	}
}

func (s *codeTerminalSession) deleteWordBack() {
	if s.cursor == 0 {
		return
	}
	i := s.cursor
	for i > 0 && (s.buf[i-1] == ' ' || s.buf[i-1] == '\t') {
		i--
	}
	for i > 0 && s.buf[i-1] != ' ' && s.buf[i-1] != '\t' {
		i--
	}
	s.buf = append(s.buf[:i], s.buf[s.cursor:]...)
	s.cursor = i
}

func (s *codeTerminalSession) recomputePalette() {
	text := string(s.buf)
	if !strings.HasPrefix(strings.TrimLeft(text, " "), "/") {
		s.paletteActive = false
		s.paletteOptions = nil
		s.paletteSelected = 0
		return
	}
	attached := s.opts.Source == terminalRemoteTaskSource
	all := slashMenuOptions(attached)
	q := strings.ToLower(strings.TrimSpace(text))
	filtered := make([]string, 0, len(all))
	for _, opt := range all {
		if q == "/" || strings.Contains(strings.ToLower(opt), q) {
			filtered = append(filtered, opt)
		}
	}
	s.paletteOptions = filtered
	if s.paletteSelected >= len(filtered) {
		s.paletteSelected = 0
	}
	s.paletteActive = len(filtered) > 0
}

// submit handles Enter: if the palette is active, treat the selected
// option as the input; otherwise use the typed buffer. Empty submits
// just redraw the prompt.
func (s *codeTerminalSession) submit() (bool, bool, error) {
	var input string
	if s.paletteActive && len(s.paletteOptions) > 0 {
		input = s.paletteOptions[s.paletteSelected]
	} else {
		input = strings.TrimSpace(string(s.buf))
	}
	s.buf = s.buf[:0]
	s.cursor = 0
	s.paletteActive = false
	s.paletteOptions = nil
	s.paletteSelected = 0

	if input == "" {
		s.draw()
		return false, false, nil
	}

	// Newline so the submitted line is preserved above the next prompt.
	s.clearPromptArea()
	s.writeRaw(fmt.Sprintf("\033[1;35myaver>\033[0m %s\r\n", ansiEscape(input)))

	// First try the shared interactive control-plane handler (slash
	// commands like /attach, /set, /sessions, etc).
	if result, err := handleInteractiveCodeCommand(input, "", "", s.opts.DefaultRunner, s.opts.DefaultModel); result.Handled {
		if err != nil {
			s.writeRaw(fmt.Sprintf("Error: %v\r\n", err))
		} else if result.ShouldExit {
			return false, true, nil
		}
		// Refresh runner/model from any /set agent that just ran.
		if cfg, profile, loadErr := loadCodeConfig(); loadErr == nil && cfg != nil && profile != nil {
			s.opts.DefaultRunner = strings.TrimSpace(profile.Runner)
			s.opts.DefaultModel = strings.TrimSpace(profile.Model)
		}
		s.draw()
		return false, false, nil
	}

	// Local builtins (help, tasks, agent, set agent, clear, etc).
	if handled, exit := runAttachBuiltin(input, s.info, s.baseURL, s.token, &s.opts); handled {
		if exit {
			return false, true, nil
		}
		s.draw()
		return false, false, nil
	}

	// Fall through: it's a coding prompt — create a task.
	payload := buildTerminalPromptPayload(input)
	taskID, err := attachCreateTask(s.baseURL, s.token, payload, s.opts)
	if err != nil {
		s.writeRaw(fmt.Sprintf("\033[31mError: %v\033[0m\r\n", err))
		s.draw()
		return false, false, nil
	}
	s.knownTasks[taskID] = true
	s.lastOutputLen[taskID] = 0
	s.activeTask = taskID
	s.draw()
	return false, false, nil
}

func (s *codeTerminalSession) applyPoll(tasks []TaskInfo) {
	var out strings.Builder
	for _, t := range tasks {
		if !s.knownTasks[t.ID] {
			s.knownTasks[t.ID] = true
			s.lastOutputLen[t.ID] = 0
			out.WriteString(fmt.Sprintf("\r\n\033[1;33m[mobile] %s\033[0m\r\n\r\n", ansiEscape(t.Title)))
			s.activeTask = t.ID
		}
		prev := s.lastOutputLen[t.ID]
		if len(t.Output) > prev {
			out.WriteString(rawifyLines(t.Output[prev:]))
			s.lastOutputLen[t.ID] = len(t.Output)
		}
		if (t.Status == "completed" || t.Status == "failed" || t.Status == "stopped") && s.activeTask == t.ID {
			if t.ResultText != "" && len(t.Output) == 0 {
				out.WriteString(rawifyLines(t.ResultText))
				out.WriteString("\r\n")
			}
			switch t.Status {
			case "failed":
				out.WriteString("\r\n\033[31m✗ Task failed\033[0m\r\n")
			case "completed":
				if t.CostUSD > 0 {
					out.WriteString(fmt.Sprintf("\r\n\033[2m($%.4f)\033[0m\r\n", t.CostUSD))
				}
			}
			s.activeTask = ""
		}
	}
	if out.Len() == 0 {
		return
	}
	s.clearPromptArea()
	s.writeRaw(out.String())
	s.draw()
}

// draw renders a fresh prompt block (label + prompt + optional
// palette) at the current screen position. After the first frame,
// each redraw climbs back up to the label line and uses \033[J to
// wipe everything from there down — that handles palette grow/shrink
// in one step without per-frame size tracking. Cursor ends on the
// prompt line at the editor caret column.
func (s *codeTerminalSession) draw() {
	var b strings.Builder

	if !s.firstDraw {
		// Cursor was on the prompt line at the end of the previous
		// frame. Climb one line to the label and clear from there.
		b.WriteString("\033[1A\r\033[J")
	}

	label := renderTerminalPromptLabel(currentWorkDir(s.info), s.opts.DefaultRunner, s.opts.DefaultModel)
	fmt.Fprintf(&b, "\033[2m%s\033[0m\r\n\033[1;35myaver>\033[0m %s", label, string(s.buf))

	extra := 0
	if s.paletteActive && len(s.paletteOptions) > 0 {
		max := len(s.paletteOptions)
		if max > 8 {
			max = 8
		}
		b.WriteString("\r\n")
		extra++
		for i := 0; i < max; i++ {
			opt := s.paletteOptions[i]
			if i == s.paletteSelected {
				fmt.Fprintf(&b, "\033[1;36m> %s\033[0m", opt)
			} else {
				fmt.Fprintf(&b, "\033[2m  %s\033[0m", opt)
			}
			b.WriteString("\r\n")
			extra++
		}
		if len(s.paletteOptions) > max {
			fmt.Fprintf(&b, "\033[2m  … +%d more\033[0m\r\n", len(s.paletteOptions)-max)
			extra++
		}
		// Climb back from below the palette to the prompt line.
		fmt.Fprintf(&b, "\033[%dA", extra)
	}

	// Position cursor at the editor caret on the prompt line. The
	// prompt prefix "yaver> " is 7 visible cells.
	col := 7 + s.cursor + 1
	fmt.Fprintf(&b, "\r\033[%dC", col-1)

	s.extraLinesBelow = extra
	s.firstDraw = false
	s.writeRaw(b.String())
}

// clearPromptArea wipes the prompt block so the caller can scroll
// fresh content (e.g. task output) into the space the prompt was
// occupying. The next draw() will treat itself as a first draw.
func (s *codeTerminalSession) clearPromptArea() {
	if s.firstDraw {
		return
	}
	// Cursor on prompt line; climb to label line and clear down.
	s.writeRaw("\033[1A\r\033[J")
	s.extraLinesBelow = 0
	s.firstDraw = true
}

func (s *codeTerminalSession) writeRaw(text string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = os.Stdout.WriteString(text)
}

func currentWorkDir(info *attachInfo) string {
	if info != nil && strings.TrimSpace(info.WorkDir) != "" {
		return info.WorkDir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// rawifyLines turns "\n" into "\r\n" so output looks correct in raw
// mode regardless of the terminal's ONLCR setting.
func rawifyLines(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	// Preserve any pre-existing \r\n.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// ansiEscape strips embedded \x1b that could break our framing if the
// caller passed in already-coloured text. Today we only feed in user
// input strings; this is defensive.
func ansiEscape(s string) string {
	return strings.ReplaceAll(s, "\x1b", "")
}

// captureStdout lets us reuse helpers like printAttachWelcome that
// write directly to fmt — we capture their output into a string and
// emit it ourselves through the raw-mode writer.
func captureStdout(fn func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		fn()
		return ""
	}
	old := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	w.Close()
	out := <-done
	r.Close()
	return out
}
