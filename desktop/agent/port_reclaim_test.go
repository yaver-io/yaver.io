package main

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestIsYaverBinary(t *testing.T) {
	cases := []struct {
		exe  string
		want bool
	}{
		{"/usr/local/bin/yaver", true},
		{"/root/.local/bin/yaver", true},
		{"/Users/kivanc/.yaver/bin/1.99.188/darwin-arm64/yaver", true},
		{"yaver", true},
		{"yaver.exe", true},
		{"/usr/local/bin/yaver (deleted)", true},

		{"", false},
		{"/usr/bin/postgres", false},
		// A path component named "yaver" must not produce a false
		// positive — only the basename counts.
		{"/home/yaver/projects/sfmg/some-binary", false},
		{"/opt/yaver-cli/wrapper", false},
		{"/usr/local/bin/yaverctl", false},
		{"/usr/local/bin/not-yaver", false},
	}
	for _, tc := range cases {
		t.Run(tc.exe, func(t *testing.T) {
			if got := isYaverBinary(tc.exe); got != tc.want {
				t.Fatalf("isYaverBinary(%q) = %v, want %v", tc.exe, got, tc.want)
			}
		})
	}
}

func TestIsAddrInUseErr(t *testing.T) {
	if isAddrInUseErr(nil) {
		t.Fatal("nil error must not be classified as EADDRINUSE")
	}
	if !isAddrInUseErr(syscall.EADDRINUSE) {
		t.Fatal("bare syscall.EADDRINUSE must be classified as in-use")
	}
	wrapped := fmt.Errorf("listen tcp 0.0.0.0:18080: bind: %w", syscall.EADDRINUSE)
	if !isAddrInUseErr(wrapped) {
		t.Fatalf("wrapped EADDRINUSE must be classified as in-use; got false for %v", wrapped)
	}
	plain := errors.New("HTTP server error: listen tcp 0.0.0.0:18080: bind: address already in use")
	if !isAddrInUseErr(plain) {
		t.Fatalf("string-only 'address already in use' must be classified as in-use; got false")
	}
	other := errors.New("connection refused")
	if isAddrInUseErr(other) {
		t.Fatal("unrelated error must not be classified as in-use")
	}
}
