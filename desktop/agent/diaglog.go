package main

// diaglog.go — the agent's on-disk diagnostic log.
//
// WHY THIS EXISTS
//
// `~/.yaver/agent.log` has been referenced by `yaver logs`, `yaver clean` and
// the backup manifest for a long time, and the agent never wrote a single byte
// to it. On a real box it was 0 bytes, dated the day it was created. So when a
// phone could not reach that box, there was no way to answer the first question
// worth asking — did the request ARRIVE? — from the box itself. Diagnosis was
// done entirely from phone screenshots, which show only one side of the wire.
//
// DESIGN CONSTRAINTS, all learned the hard way
//
//   - CAPPED FROM THE FIRST COMMIT. The box this was written for had 11 GB free
//     of 228 GB. Unbounded connection logging would have taken it down, and
//     "add rotation later" is how that happens. Size cap + generation cap + age
//     prune all ship here, not in a follow-up.
//   - TAGGED. Every line carries a subject ([connect], [hermes], [autorun], …)
//     so one noisy subsystem can be read or ignored without grep gymnastics.
//   - LEVELLED, defaulting to DEBUG. A diagnostic log that defaults to quiet
//     records nothing on the one run that mattered; the failures this exists for
//     are intermittent and un-reproducible on demand.
//   - PRIVACY-BOUND. Same contract as Convex: no prompts, no file contents, no
//     task output, no secrets. Peer ADDRESSES are allowed — they are the whole
//     point of a connection log, they stay on the user's own disk, and they are
//     never synced.
//
// Rotation is deliberately a plain size check on write rather than a timer: a
// timer does not run while the process is wedged, which is exactly when the log
// is growing fastest.

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type diagLevel int

const (
	diagDebug diagLevel = iota
	diagInfo
	diagWarn
	diagError
)

func (l diagLevel) String() string {
	switch l {
	case diagDebug:
		return "DEBUG"
	case diagInfo:
		return "INFO"
	case diagWarn:
		return "WARN"
	case diagError:
		return "ERROR"
	}
	return "?"
}

func parseDiagLevel(s string) (diagLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return diagDebug, true
	case "info":
		return diagInfo, true
	case "warn", "warning":
		return diagWarn, true
	case "error":
		return diagError, true
	}
	return diagDebug, false
}

const (
	// diagMaxBytes is the size at which the live file rotates. Small on
	// purpose: the machines this runs on are often disk-tight, and a log you
	// have to think twice about keeping is one that gets disabled.
	diagMaxBytes = 4 << 20 // 4 MiB
	// diagMaxGenerations is how many rotated files are kept (agent.log.1 …).
	// Total on-disk ceiling is therefore (generations+1) * maxBytes = 20 MiB.
	diagMaxGenerations = 4
	// diagMaxAge prunes rotated files even when the box is quiet, so a machine
	// that logs slowly does not carry months of history forever. "Weekly
	// cleanup" in practice: anything rotated out and older than this goes.
	diagMaxAge = 7 * 24 * time.Hour
)

type diagLogger struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	written int64
	min     diagLevel
	// disabled short-circuits every call after an unrecoverable open error, so
	// a read-only or full disk degrades to "no logging" instead of erroring on
	// every request. Logging must never be the thing that breaks the agent.
	disabled bool
}

var (
	diagOnce sync.Once
	diagInst *diagLogger
)

// diag returns the process-wide logger, opening the file on first use.
func diag() *diagLogger {
	diagOnce.Do(func() {
		lvl := diagDebug // default: debug, deliberately verbose
		if v := os.Getenv("YAVER_LOG_LEVEL"); v != "" {
			if parsed, ok := parseDiagLevel(v); ok {
				lvl = parsed
			}
		}
		dir, err := yaverDir()
		if err != nil {
			diagInst = &diagLogger{disabled: true}
			return
		}
		diagInst = &diagLogger{path: filepath.Join(dir, "agent.log"), min: lvl}
		diagInst.pruneAged()
	})
	return diagInst
}

// logf writes one tagged, levelled line. Never returns an error and never
// panics — a diagnostic facility that can take the process down is worse than
// no diagnostic facility.
func (d *diagLogger) logf(lvl diagLevel, tag, format string, args ...interface{}) {
	if d == nil || d.disabled || lvl < d.min {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil && !d.openLocked() {
		return
	}
	line := fmt.Sprintf("%s %-5s [%s] %s\n",
		time.Now().Format("2006-01-02T15:04:05.000Z07:00"), lvl, tag,
		fmt.Sprintf(format, args...))
	n, err := d.f.WriteString(line)
	if err != nil {
		// Most likely the disk filled. Stop rather than spin on every request.
		d.disabled = true
		_ = d.f.Close()
		d.f = nil
		return
	}
	d.written += int64(n)
	if d.written >= diagMaxBytes {
		d.rotateLocked()
	}
}

func (d *diagLogger) openLocked() bool {
	f, err := os.OpenFile(d.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		d.disabled = true
		return false
	}
	d.f = f
	if info, err := f.Stat(); err == nil {
		d.written = info.Size()
	}
	return true
}

// rotateLocked shifts agent.log -> .1 -> .2 … and drops the oldest.
func (d *diagLogger) rotateLocked() {
	if d.f != nil {
		_ = d.f.Close()
		d.f = nil
	}
	// Drop the oldest first so the shift below never overwrites a file we still
	// want, then cascade downwards.
	oldest := fmt.Sprintf("%s.%d", d.path, diagMaxGenerations)
	_ = os.Remove(oldest)
	for i := diagMaxGenerations - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", d.path, i), fmt.Sprintf("%s.%d", d.path, i+1))
	}
	_ = os.Rename(d.path, d.path+".1")
	d.written = 0
	d.openLocked()
}

// pruneAged removes rotated generations older than diagMaxAge. The LIVE file is
// never pruned by age — an idle box's current log is still the one you want when
// something finally goes wrong.
func (d *diagLogger) pruneAged() {
	if d.path == "" {
		return
	}
	cutoff := time.Now().Add(-diagMaxAge)
	matches, err := filepath.Glob(d.path + ".*")
	if err != nil {
		return
	}
	sort.Strings(matches)
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(m)
		}
	}
}

// Package-level helpers. Tag first, because the tag is what a reader scans for.
func logDebug(tag, format string, args ...interface{}) { diag().logf(diagDebug, tag, format, args...) }
func logInfo(tag, format string, args ...interface{})  { diag().logf(diagInfo, tag, format, args...) }
func logWarn(tag, format string, args ...interface{})  { diag().logf(diagWarn, tag, format, args...) }
func logError(tag, format string, args ...interface{}) { diag().logf(diagError, tag, format, args...) }

// Canonical subject tags. Constants rather than bare strings so a typo does not
// silently create a second, unsearchable subject.
const (
	tagConnect = "connect"
	tagAuth    = "auth"
	tagRelay   = "relay"
	tagMesh    = "mesh"
	tagHermes  = "hermes"
	tagAutorun = "autorun"
	tagDeploy  = "deploy"
	tagTask    = "task"
	tagRunner  = "runner"
	tagAgent   = "agent"
)

// withRequestLog records every HTTP request the agent actually received.
//
// This is the log line that was missing. When a phone reports "the machine
// accepted the connection but never answered", the box itself previously had no
// record either way — so there was no way to distinguish "the request never
// arrived" (network) from "it arrived and we were slow" (the real case, an 8 MB
// /tasks response). Status and byte count are both here because THAT pair is
// what identifies an oversized response: a 200 with a huge size, or the relay's
// 502 with none.
//
// Privacy: method, path, status, size, duration and the peer address. No
// bodies, no query values (a token can ride in ?token=), no headers. The peer
// address is deliberately kept — it is the point of a connection log, it never
// leaves this disk, and it is never synced to Convex.
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(started)

		// Path only — never r.URL.RequestURI(), which carries the query string
		// and therefore any ?token= a WebSocket client passed.
		lvl := diagDebug
		if rec.status >= 500 {
			lvl = diagError
		} else if rec.status >= 400 || elapsed > 5*time.Second {
			// A slow 200 is a warning in its own right: it is what an oversized
			// response looks like from here, just before a proxy gives up on it.
			lvl = diagWarn
		}
		diag().logf(lvl, tagConnect, "%s %s -> %d %dB in %s from %s",
			r.Method, r.URL.Path, rec.status, rec.bytes,
			elapsed.Round(time.Millisecond), peerAddr(r))
	})
}

// statusRecorder captures what was actually sent. WriteHeader may never be
// called (Go defaults to 200), hence the initialised status.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// Hijack keeps WebSocket and PTY upgrades working through this wrapper. Without
// it, wrapping the handler would break every streaming endpoint on the box —
// a logging change must never cost a feature.
func (rec *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter is not a Hijacker")
}

// Flush keeps streaming/SSE responses flushing promptly.
func (rec *statusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// peerAddr is the remote host without its ephemeral port.
func peerAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
