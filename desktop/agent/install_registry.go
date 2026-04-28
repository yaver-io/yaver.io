package main

// install_registry.go — install tools that only exist in the public
// Convex package registry (not in the agent's built-in `integrations`
// map). Same streaming contract as the built-in installer
// (`/streams/install:<tool>`) but:
//
//   1. The command comes from ResolveInstallStep(registry, availablePMs).
//   2. It runs inside a PTY so `sudo` prompts are detectable.
//   3. When a `[sudo] password …` line is seen, the agent emits a
//      typed `sudo_prompt` event on the stream. The mobile/web Tools
//      UI subscribes to that stream for install progress anyway, so
//      it can show a secure password sheet the moment the prompt
//      arrives.
//   4. A separate endpoint — POST /install/sudo — takes {tool,
//      password} and writes the password to the in-flight PTY's
//      stdin. Password is never logged, never persisted.
//
// Anything not matching the sudo pattern is streamed verbatim as
// `{type:"line",text}` frames, exactly like the built-in installer.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// installSudoPromptPattern mirrors the stricter one used by the
// terminal WS handler — narrow enough to avoid firing the modal on
// every `echo "Password:"` but wide enough to catch apt-get / dnf /
// pacman / brew that wraps sudo.
var installSudoPromptPattern = regexp.MustCompile(`(?m)(\[sudo\]\s*[Pp]assword(?:\s+for\s+\S+)?\s*:|^[Ss]udo\s+password\s*:|^[Pp]assword\s*:\s*$)`)

// activeInstallPty tracks the one-and-only PTY per in-flight install.
// We deliberately keep this to one-per-tool so the `sudo-response`
// endpoint doesn't need request IDs: the mobile UI points at a tool
// name, the agent looks up the matching PTY, and writes the password
// to it. If a second install of the same tool kicks off, the older
// one is replaced (the goroutine that owned it will exit on PTY
// close).
type activeInstallPty struct {
	stdin io.Writer
	close func()
	start time.Time

	// suppressUntil is set when the agent just wrote a password to
	// the PTY. Lines read between "now" and this timestamp are
	// dropped from the log stream so a buggy wrapper that echoed
	// `read -p` instead of `read -s` cannot leak the secret. This is
	// belt-and-braces: OS sudo disables echo itself, but we never
	// want the agent to be the weak link.
	suppressMu    sync.Mutex
	suppressUntil time.Time
}

// armEchoSuppression closes the window during which install stdout
// is dropped. Called immediately after writing the password to the
// PTY. 1 second is more than enough for any echoed `read -p` line
// to flush through the pipe.
func (a *activeInstallPty) armEchoSuppression() {
	a.suppressMu.Lock()
	a.suppressUntil = time.Now().Add(1 * time.Second)
	a.suppressMu.Unlock()
}

func (a *activeInstallPty) suppressActive() bool {
	a.suppressMu.Lock()
	defer a.suppressMu.Unlock()
	return time.Now().Before(a.suppressUntil)
}

var (
	installPtyMu   sync.Mutex
	installPtys    = map[string]*activeInstallPty{}
	installPtyTTL  = 15 * time.Minute
)

func registerInstallPty(tool string, p *activeInstallPty) {
	installPtyMu.Lock()
	defer installPtyMu.Unlock()
	if prev, ok := installPtys[tool]; ok && prev.close != nil {
		prev.close() // supersede
	}
	installPtys[tool] = p
}

func unregisterInstallPty(tool string, p *activeInstallPty) {
	installPtyMu.Lock()
	defer installPtyMu.Unlock()
	if cur, ok := installPtys[tool]; ok && cur == p {
		delete(installPtys, tool)
	}
}

// getInstallPty returns the PTY for an in-flight install, if any.
// The caller must hold no lock while calling this; the returned
// pointer is safe to Write to (PTYs are single-writer-per-open but
// stdin writes are naturally serial).
func getInstallPty(tool string) *activeInstallPty {
	installPtyMu.Lock()
	defer installPtyMu.Unlock()
	p := installPtys[tool]
	if p == nil {
		return nil
	}
	// Safety valve — an install stuck in prompt state for >15m is
	// almost certainly a dead PTY from a crashed shell. Drop it.
	if time.Since(p.start) > installPtyTTL {
		delete(installPtys, tool)
		return nil
	}
	return p
}

// runRegistryInstall executes the chosen install command inside a
// PTY, streaming lines to `stream` and emitting a `sudo_prompt`
// event the moment the PTY asks for a password. Blocks until the
// command exits.
func runRegistryInstall(ctx context.Context, tool string, step *PackageRegistryStep, stream *LogStream) error {
	if step == nil {
		return fmt.Errorf("no install step matched — host is missing every required package manager for %s", tool)
	}
	if strings.TrimSpace(step.Command) == "" {
		return fmt.Errorf("registry entry for %s has an empty command", tool)
	}
	stream.Append(fmt.Sprintf("→ Using %s: %s", step.PackageManager, step.Command))

	// Use a login-style shell so PATH / rustup / nvm setups don't
	// ambush us. Bash is the safest default across mac + linux.
	cmd := exec.CommandContext(ctx, "bash", "-lc", step.Command)
	// TERM=dumb so apt / brew don't try to draw spinners — we want
	// flat text in the stream. Sudo still works fine without colour.
	cmd.Env = append(cmd.Environ(), "TERM=dumb", "DEBIAN_FRONTEND=noninteractive")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start pty: %w", err)
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	active := &activeInstallPty{
		stdin: ptmx,
		close: func() { _ = ptmx.Close() },
		start: time.Now(),
	}
	registerInstallPty(tool, active)
	defer unregisterInstallPty(tool, active)

	// Watch PTY output. Sudo prompts arrive without a trailing
	// newline, so we can't just use bufio.Scanner — we need to
	// inspect partial chunks too. Keep a 512-byte sliding tail for
	// the regex, and flush complete lines to the stream.
	br := bufio.NewReader(ptmx)
	tail := make([]byte, 0, 512)
	line := make([]byte, 0, 256)
	lastPromptAt := time.Time{}

	flushLine := func() {
		if len(line) == 0 {
			return
		}
		text := strings.TrimRight(string(line), "\r")
		// If the agent just submitted a password to stdin, drop the
		// next line from the stream. OS sudo doesn't echo the
		// password, but a poorly-written install wrapper using
		// `read -p` instead of `read -s` would — and the stream is
		// publicly subscribable by mobile + web + (down the line)
		// coding agents. Belt-and-braces.
		if active.suppressActive() {
			line = line[:0]
			return
		}
		stream.Append(text)
		line = line[:0]
	}

	for {
		b, err := br.ReadByte()
		if err != nil {
			flushLine()
			if err == io.EOF {
				break
			}
			return fmt.Errorf("pty read: %w", err)
		}
		if b == '\n' {
			flushLine()
			tail = tail[:0]
			continue
		}
		line = append(line, b)
		tail = append(tail, b)
		if len(tail) > 512 {
			tail = tail[len(tail)-512:]
		}
		// Only search on punctuation bytes that typical prompts end
		// with; keeps the regex cheap even though it's pre-compiled.
		if b != ':' && b != ' ' {
			continue
		}
		if loc := installSudoPromptPattern.FindIndex(tail); loc != nil {
			if time.Since(lastPromptAt) > 500*time.Millisecond {
				lastPromptAt = time.Now()
				prompt := string(tail[loc[0]:loc[1]])
				stream.AppendEvent(map[string]any{
					"type":   "sudo_prompt",
					"prompt": prompt,
					"tool":   tool,
					"hint":   "This install asked for the sudo password. Enter it once; it flows to stdin and is never persisted.",
				})
			}
			tail = tail[loc[1]:]
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("install exited: %w", err)
	}
	return nil
}

// respondToInstallSudo writes the password to the PTY stdin of the
// matching in-flight install. Returns an error if no install is
// waiting for a password, or if the PTY has already closed.
//
// Hard invariants worth calling out because getting this wrong leaks
// secrets into AI-agent prompt windows:
//
//   - The password flows mobile/web → this function → PTY stdin.
//   - It is NEVER written to `stream.Append` (the subscribable log
//     stream).
//   - armEchoSuppression() drops the next line of PTY output so a
//     buggy wrapper that uses `read -p` instead of `read -s` cannot
//     bleed the password into the stream either.
//   - No coding agent (claude-code / codex / opencode) ever consumes
//     `streams/install:*` — installs run outside AI context on
//     purpose. Do not change that contract.
func respondToInstallSudo(tool, password string) error {
	p := getInstallPty(tool)
	if p == nil {
		return fmt.Errorf("no install for %q is waiting for a sudo password", tool)
	}
	if p.stdin == nil {
		return fmt.Errorf("install for %q has no writable stdin", tool)
	}
	p.armEchoSuppression()
	if _, err := io.WriteString(p.stdin, password+"\n"); err != nil {
		return fmt.Errorf("stdin write: %w", err)
	}
	return nil
}

// cancelInstallSudo sends a ^C to the in-flight PTY so the user can
// abort a stuck prompt from the mobile UI.
func cancelInstallSudo(tool string) error {
	p := getInstallPty(tool)
	if p == nil {
		return fmt.Errorf("no install for %q is in progress", tool)
	}
	if _, err := p.stdin.Write([]byte{3}); err != nil {
		return fmt.Errorf("stdin write: %w", err)
	}
	return nil
}
