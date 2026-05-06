//go:build windows

package main

import "os"

// workDirOwnerLabel is a no-op on Windows — the codex bwrap sandbox is
// Linux-only, so this helper is never actually consulted on Windows
// builds. Defined to keep the signature the same across platforms.
func workDirOwnerLabel(_ os.FileInfo) string {
	return ""
}

// codexBwrapWillFail is always false on Windows — codex's bwrap path
// only runs on Linux, so this preflight has nothing to catch here.
func codexBwrapWillFail(_ os.FileInfo) bool {
	return false
}
