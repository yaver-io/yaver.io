package main

// screenlog_input_capture.go — the native input-capture orchestrator that
// turns the producer model into a built-in: while a screenlog session
// records frames, this captures the real keyboard/mouse stream and feeds
// it into the same events.jsonl companion file.
//
// Capture is platform-specific; the MANAGER here is shared + testable. A
// producer is any process that prints one JSON InputEvent per line to
// stdout; the manager scans those lines, redacts per policy, and ingests.
// This keeps the OS-specific code tiny and the pipeline uniform.
//
// Platforms:
//   - WSL → Windows host (THE top use case): the Linux/WSL agent can't
//     install a Windows hook itself, so it spawns `powershell.exe` running
//     a low-level WH_KEYBOARD_LL/WH_MOUSE_LL hook that streams JSON lines
//     back over the interop pipe — the same interop bridge screen capture
//     uses. No extra binary to ship.
//   - native Windows: an in-process SetWindowsHookEx (screenlog_input_windows.go,
//     build-tagged) registered via nativeStartInputCapture.
//   - Linux (X11): `xinput test-xi2 --root` producer when available.
//   - macOS: a CGEventTap helper (cgo) — documented; producer-pluggable.
//
// Only clicks, scroll, and keystrokes are captured — raw mouse MOVES are
// dropped (they flood at every pixel and carry little intent), matching
// "mouse clicks + keylogger". Everything stays local; gated by
// ScreenlogPolicy.AllowInputCapture + ScreenlogConfig.CaptureInput.

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// nativeStartInputCapture/nativeStopInputCapture are set by a build-tagged
// file on platforms with an in-process hook (Windows). nil elsewhere.
var (
	nativeStartInputCapture func(sessionID string, redact bool) error
	nativeStopInputCapture  func()
)

// testInputProducer, when set, overrides producer selection (tests inject
// a fake process that emits canned JSON lines).
var testInputProducer func() *exec.Cmd

type inputCapturer struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stop    chan struct{}
	native  bool
	running bool
}

var (
	inputCapMu sync.Mutex
	inputCap   *inputCapturer
)

// startInputCapture begins capturing input for a session. redact mirrors
// !cfg.AllowRawText. Non-fatal: if no producer is available it returns a
// reason and the session still records frames.
func startInputCapture(sessionID string, cfg ScreenlogConfig, redact bool) (string, bool) {
	inputCapMu.Lock()
	defer inputCapMu.Unlock()
	if inputCap != nil {
		return "input capture already running", false
	}

	// Native in-process hook (Windows) takes precedence.
	if nativeStartInputCapture != nil {
		if err := nativeStartInputCapture(sessionID, redact); err != nil {
			return err.Error(), false
		}
		inputCap = &inputCapturer{native: true, running: true}
		return "native", true
	}

	cmd, ok := inputProducerForCapture(cfg)
	if !ok {
		return "no input-capture producer on this platform (WSL needs powershell.exe; Linux needs xinput; macOS needs the CGEventTap helper)", false
	}
	ic := &inputCapturer{cmd: cmd, stop: make(chan struct{}), running: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err.Error(), false
	}
	if err := cmd.Start(); err != nil {
		return err.Error(), false
	}
	inputCap = ic
	go ic.pump(sessionID, redact, stdout)
	return "subprocess", true
}

func stopInputCapture() {
	inputCapMu.Lock()
	ic := inputCap
	inputCap = nil
	inputCapMu.Unlock()
	if ic == nil {
		return
	}
	if ic.native {
		if nativeStopInputCapture != nil {
			nativeStopInputCapture()
		}
		return
	}
	close(ic.stop)
	if ic.cmd != nil && ic.cmd.Process != nil {
		_ = ic.cmd.Process.Kill()
	}
}

// pump scans the producer's stdout (one JSON InputEvent per line), buffers,
// and flushes to the companion file on a ticker / at stop.
func (ic *inputCapturer) pump(sessionID string, redact bool, stdout interface{ Read([]byte) (int, error) }) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var buf []InputEvent
	var bufMu sync.Mutex

	flush := func() {
		bufMu.Lock()
		batch := buf
		buf = nil
		bufMu.Unlock()
		if len(batch) > 0 {
			_, _ = ingestInputEvents(sessionID, batch, redact)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var e InputEvent
			if json.Unmarshal(line, &e) != nil || !validInputTypes[e.Type] {
				continue
			}
			if e.T == 0 {
				e.T = time.Now().UnixMilli()
			}
			bufMu.Lock()
			buf = append(buf, e)
			n := len(buf)
			bufMu.Unlock()
			if n >= 50 {
				flush()
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ic.stop:
			flush()
			return
		case <-done:
			flush()
			return
		case <-ticker.C:
			flush()
		}
	}
}

// inputProducerForCapture selects the per-platform producer command.
func inputProducerForCapture(cfg ScreenlogConfig) (*exec.Cmd, bool) {
	if testInputProducer != nil {
		return testInputProducer(), true
	}
	if runtime.GOOS == "linux" && isWSLHost() && wslShouldUseHost(cfg) && lookPathOK("powershell.exe") {
		return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", powershellInputHookScript()), true
	}
	if runtime.GOOS == "linux" && lookPathOK("xinput") {
		// xinput streams raw events on stdout; a thin awk turns the
		// relevant ones into our JSON line protocol. Best-effort (X11 only).
		return exec.Command("sh", "-c", linuxXinputProducer()), true
	}
	return nil, false
}
