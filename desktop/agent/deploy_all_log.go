package main

// deploy_all_log.go — persistent logs for `yaver deploy all` (both the
// bespoke yaver.io path and the generic detection-driven path).
//
// `deploy all` shells out to the canonical scripts/*.sh and streams their
// output to the terminal. That's great while you're watching, but the moment
// a stage fails you've usually lost scrollback — the talos Play Store upload
// that died with "Version code 337 has already been used" scrolled off after
// 700 Gradle lines. This tees every byte of orchestration + stage output to
// ~/.yaver/deploy-logs/<repo>/<timestamp>.log so there's always a record to
// grep after the fact, regardless of which surface failed.
//
// This is intentionally separate from `yaver deploy logs <run-id>`, which
// reads the agent's HTTP /deploy/ship run store (guest-project deploys with
// vault creds). `deploy all` is a local CLI shell-out and never touches that
// store, so it gets its own on-disk log here.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// deployAllLogger tees terminal output to a per-run log file. out/errW are
// MultiWriters (terminal + file) so existing fmt.Fprint* call sites keep
// printing to the screen while the file captures everything. When the file
// can't be opened it degrades to plain stdout/stderr — logging must never
// block a deploy.
type deployAllLogger struct {
	out  io.Writer
	errW io.Writer
	file *os.File
	path string
}

// newDeployAllLogger opens ~/.yaver/deploy-logs/<repo>/<timestamp>.log and
// returns a tee-ing logger. repoName is filepath.Base(repoRoot). Best-effort:
// any failure falls back to bare stdout/stderr with an empty path.
func newDeployAllLogger(repoName string) *deployAllLogger {
	fallback := &deployAllLogger{out: os.Stdout, errW: os.Stderr}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return fallback
	}
	dir := filepath.Join(home, ".yaver", "deploy-logs", sanitizeLogSegment(repoName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fallback
	}
	name := time.Now().Format("2006-01-02_150405") + ".log"
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return fallback
	}
	return &deployAllLogger{
		out:  io.MultiWriter(os.Stdout, f),
		errW: io.MultiWriter(os.Stderr, f),
		file: f,
		path: f.Name(),
	}
}

func (l *deployAllLogger) printf(format string, a ...any) { fmt.Fprintf(l.out, format, a...) }
func (l *deployAllLogger) println(a ...any)               { fmt.Fprintln(l.out, a...) }

// close flushes and closes the underlying file (no-op in fallback mode).
func (l *deployAllLogger) close() {
	if l.file != nil {
		_ = l.file.Close()
	}
}

// sanitizeLogSegment keeps the repo name safe as a single path segment.
func sanitizeLogSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "repo"
	}
	repl := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}
	return strings.Map(repl, s)
}
