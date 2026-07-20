package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckRunnerBinaryCachesSuccessfulProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script probe test is Unix-only")
	}

	clearRunnerBinaryCheckCache()
	dir := t.TempDir()
	countFile := filepath.Join(dir, "count.txt")
	cmdPath := filepath.Join(dir, "fake-runner")
	script := "#!/bin/sh\n" +
		"printf '1\\n' >> " + shellQuoteForTest(countFile) + "\n" +
		"echo '1.2.3'\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake runner: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	if err := CheckRunnerBinary("fake-runner"); err != nil {
		t.Fatalf("first CheckRunnerBinary: %v", err)
	}
	if err := CheckRunnerBinary("fake-runner"); err != nil {
		t.Fatalf("second CheckRunnerBinary: %v", err)
	}

	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(string(data)), "1"); got != 1 {
		t.Fatalf("probe count = %d, want 1", got)
	}
}

func TestCheckRunnerBinaryDoesNotCacheMissingRunner(t *testing.T) {
	clearRunnerBinaryCheckCache()
	name := "definitely-missing-yaver-runner"
	if err := CheckRunnerBinary(name); err == nil {
		t.Fatalf("expected missing runner error")
	}
	if _, ok := cachedRunnerBinaryPath(name); ok {
		t.Fatalf("missing runner %q should not be cached", name)
	}
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
