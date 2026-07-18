package main

// remote_runtime_android_navigate.go — URL entry for Android targets.
//
// NOTE the filename, same trap as remote_runtime_android_pinch.go: a trailing
// _android would make Go treat this as a GOOS=android build constraint and drop
// the file from the macOS/Linux agent, leaving the symbol "undefined" with the
// source sitting right there.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// androidNavigateViaIntent delivers a URL as ACTION_VIEW, which is the same
// path a real link-tap takes on-device (deep links included).
//
// The URL is passed as a single argv element to exec.CommandContext — NOT
// interpolated into a shell string. adb forwards argv to the device shell,
// which does re-parse it, so an unquoted `;` or `&&` in a URL would otherwise
// run as a second command on the device. Go's exec does not spawn a shell
// locally, and the -e single-quoting below closes the remote half.
func androidNavigateViaIntent(ctx context.Context, deviceID, rawURL string) error {
	target, err := validateNavigateURL(rawURL)
	if err != nil {
		return err
	}

	// Single-quote for the DEVICE-side shell and escape any embedded quote.
	// validateNavigateURL already rejects non-http(s) schemes, so this is
	// defence in depth rather than the only guard.
	quoted := "'" + strings.ReplaceAll(target, "'", `'\''`) + "'"

	args := []string{}
	if deviceID != "" {
		args = append(args, "-s", deviceID)
	}
	args = append(args, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", quoted)

	out, err := exec.CommandContext(ctx, "adb", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb am start VIEW: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// `am start` exits 0 even when nothing can handle the intent; it reports the
	// failure on stdout instead. Surfacing it matters because the alternative is
	// a frame that never changes, which reads as a dead stream.
	if text := string(out); strings.Contains(text, "Error:") || strings.Contains(text, "does not exist") {
		return fmt.Errorf("adb am start VIEW reported: %s", strings.TrimSpace(text))
	}
	return nil
}
