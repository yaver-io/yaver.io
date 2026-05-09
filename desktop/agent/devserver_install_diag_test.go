package main

import (
	"errors"
	"strings"
	"testing"
)

// TestClassifyInstallFailure_KnownPatterns covers each branch of the
// classifier with real-world stderr fragments captured from the wild.
// The point isn't string-perfect parity — it's that someone reading the
// dashboard's error card can tell *what* to do without scrolling through
// 8 KB of npm chatter, and that classify() never silently regresses on a
// case we've already shipped a workaround for.
func TestClassifyInstallFailure_KnownPatterns(t *testing.T) {
	cases := []struct {
		name     string
		tail     string
		mustMention []string // each substring must appear in the cause
		shouldMatch bool
	}{
		{
			name: "EINTEGRITY (carrotbet stale lockfile case)",
			tail: `npm warn tarball tarball data for yaver-feedback-web@file:/root/yaver.io/sdk/feedback/web/yaver-feedback-web-0.2.2.tgz seems to be corrupted. Trying again.
npm error code EINTEGRITY
npm error sha512-X integrity checksum failed when using sha512: wanted sha512-X but got sha512-Y. (132220 bytes)`,
			mustMention: []string{"package-lock.json", "integrity"},
			shouldMatch: true,
		},
		{
			name: "ENOENT for yaver-feedback file:tarball",
			tail: `npm error code ENOENT
npm error syscall open
npm error path /root/yaver.io/sdk/feedback/web/yaver-feedback-web-0.2.2.tgz
npm error errno -2`,
			mustMention: []string{"yaver-feedback", "npm pack"},
			shouldMatch: true,
		},
		{
			name: "ENOENT generic file:tarball",
			tail: `npm error code ENOENT
npm error path /tmp/some-other-package.tgz`,
			mustMention: []string{"file:", "tarball"},
			shouldMatch: true,
		},
		{
			name: "ERESOLVE peer-dep conflict",
			tail: `npm ERR! code ERESOLVE
npm ERR! ERESOLVE could not resolve
npm ERR!
npm ERR! While resolving: app@1.0.0
npm ERR! Found: react@19.0.0`,
			mustMention: []string{"peer-dependency"},
			shouldMatch: true,
		},
		{
			name: "EACCES",
			tail: `npm ERR! code EACCES
npm ERR! syscall mkdir
npm ERR! errno -13
npm ERR! Error: EACCES: permission denied`,
			mustMention: []string{"permission"},
			shouldMatch: true,
		},
		{
			name: "Network ENOTFOUND",
			tail: `npm ERR! code ENOTFOUND
npm ERR! syscall getaddrinfo
npm ERR! errno ENOTFOUND`,
			mustMention: []string{"network"},
			shouldMatch: true,
		},
		{
			name:        "Unrecognized failure",
			tail:        `something completely unrelated`,
			mustMention: nil,
			shouldMatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyInstallFailure(tc.tail)
			if tc.shouldMatch && got == "" {
				t.Fatalf("expected classifier to match; got empty")
			}
			if !tc.shouldMatch && got != "" {
				t.Fatalf("expected classifier to NOT match; got %q", got)
			}
			lower := strings.ToLower(got)
			for _, want := range tc.mustMention {
				if !strings.Contains(lower, strings.ToLower(want)) {
					t.Fatalf("cause %q must mention %q", got, want)
				}
			}
		})
	}
}

// TestInstallFailureMessage_StructureAndTailTruncation makes sure the
// envelope stays parseable: it always starts with `<pm> install failed:`,
// includes the cause line when classify() matches, and trims the tail to
// the last ~30 lines so the dashboard error card doesn't render a 200-line
// scrollbox where the actionable bit is buried at the top.
func TestInstallFailureMessage_StructureAndTailTruncation(t *testing.T) {
	tailLines := make([]string, 100)
	for i := range tailLines {
		tailLines[i] = "noise line"
	}
	tailLines = append(tailLines, "npm error code EINTEGRITY")
	tailLines = append(tailLines, "npm error integrity checksum failed when using sha512")
	tail := strings.Join(tailLines, "\n")

	msg := installFailureMessage("npm", errors.New("exit status 254"), tail)

	if !strings.HasPrefix(msg, "npm install failed: exit status 254") {
		t.Fatalf("envelope must start with `<pm> install failed: <err>`; got %q", msg[:60])
	}
	if !strings.Contains(msg, "\ncause: ") {
		t.Fatal("classified failures must include `cause:` line")
	}
	if !strings.Contains(msg, "integrity") {
		t.Fatal("EINTEGRITY classifier must surface `integrity` in cause")
	}
	if !strings.Contains(msg, "\nlast lines:\n") {
		t.Fatal("tail section must be present")
	}
	// Exactly the last ~30 lines (we keep 30 incl. our two trailers).
	tailSection := msg[strings.Index(msg, "\nlast lines:\n")+len("\nlast lines:\n"):]
	gotLines := strings.Count(tailSection, "\n") + 1
	if gotLines > 31 || gotLines < 25 {
		t.Fatalf("expected ~30 lines in tail section, got %d", gotLines)
	}
}

// TestInstallDiag_TruncatesToMaxBytes guards the bounded-buffer
// invariant — a runaway noisy installer must not balloon agent memory.
func TestInstallDiag_TruncatesToMaxBytes(t *testing.T) {
	d := newInstallDiag()
	chunk := strings.Repeat("x", 1024)
	for i := 0; i < 100; i++ { // 100 KB total
		d.Write([]byte(chunk))
	}
	if got := len(d.Tail()); got > installDiagMaxBytes {
		t.Fatalf("diag tail %d bytes exceeds max %d", got, installDiagMaxBytes)
	}
	if !strings.Contains(d.Tail(), "x") {
		t.Fatal("tail must retain trailing content")
	}
}
