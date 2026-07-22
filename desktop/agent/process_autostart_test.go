package main

import "testing"

func TestEnsureAutoStartRespectsExplicitSkipEnv(t *testing.T) {
	t.Setenv("YAVER_SKIP_AUTO_START", "1")

	if got := ensureAutoStart("/definitely/missing/yaver", t.TempDir()); got != "" {
		t.Fatalf("ensureAutoStart() = %q, want skipped", got)
	}
}
