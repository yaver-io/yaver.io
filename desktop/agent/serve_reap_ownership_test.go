package main

import (
	"os"
	"strings"
	"testing"
)

// A second service definition can briefly launch `yaver serve` while the real
// primary is healthy. It must discover and reuse that owner before touching the
// shared dev-child registry; otherwise its different agentBootID makes the
// orphan reaper SIGTERM the real agent's live Expo/Metro child.
func TestServeChecksPrimaryOwnershipBeforeReapingDevChildren(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	runServe := string(src)
	if start := strings.Index(runServe, "func runServe("); start >= 0 {
		runServe = runServe[start:]
	}
	probe := strings.Index(runServe, "probeLocalAgentHealthInfo(*httpPort)")
	reap := strings.Index(runServe, "ReapOrphanedDevChildren()")
	if probe < 0 || reap < 0 {
		t.Fatalf("runServe ownership guard missing: probe=%d reap=%d", probe, reap)
	}
	if probe > reap {
		t.Fatalf("runServe reaps shared dev children before checking for a healthy primary; a duplicate service can kill the live Expo process")
	}
}
