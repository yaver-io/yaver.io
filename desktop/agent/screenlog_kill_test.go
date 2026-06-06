package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScreenlogKillDisarmsAndDisables verifies the panic button flips every
// switch (autostart off + master kill-switch off) but, WITHOUT --purge, leaves
// captured data on disk.
func TestScreenlogKillDisarmsAndDisables(t *testing.T) {
	withTempScreenlogDir(t)

	// Arm autostart and enable the policy, as a live "recording everything"
	// box would be.
	cfg := defaultScreenlogConfig()
	if err := setScreenlogAutostart(true, cfg, "dad-pc"); err != nil {
		t.Fatal(err)
	}
	if err := saveScreenlogPolicy(ScreenlogPolicy{Enabled: true, AllowRemoteControl: true}); err != nil {
		t.Fatal(err)
	}
	// Drop a fake captured session on disk.
	dir, err := screenlogSessionDir("slog-keepme")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001_d0_1.jpg"), []byte("frame"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := killScreenlog(false)

	if !res.AutostartCleared || !res.PolicyDisabled {
		t.Fatalf("kill did not report disarm+disable: %+v", res)
	}
	if a, _ := loadScreenlogAutostart(); a.Enabled {
		t.Error("autostart should be disarmed after kill")
	}
	if pol := loadScreenlogPolicy(); pol.Enabled {
		t.Error("policy master kill-switch should be OFF after kill")
	}
	// A future start must now be refused by the enforcement gate.
	if ok, _ := screenlogEnforce(loadScreenlogPolicy(), screenlogCaller{}); ok {
		t.Error("screenlogEnforce should deny after kill (policy disabled)")
	}
	// Without --purge the frame survives.
	if _, err := os.Stat(filepath.Join(dir, "000001_d0_1.jpg")); err != nil {
		t.Errorf("frame should survive a non-purge kill: %v", err)
	}
	if res.Purged {
		t.Error("non-purge kill must not report purged")
	}
}

// TestScreenlogKillPurge verifies --purge deletes session dirs but preserves
// the auditable control files (policy.json, autostart.json, audit.jsonl).
func TestScreenlogKillPurge(t *testing.T) {
	withTempScreenlogDir(t)
	_ = saveScreenlogPolicy(ScreenlogPolicy{Enabled: true})
	_ = setScreenlogAutostart(true, defaultScreenlogConfig(), "")

	for _, id := range []string{"slog-a", "slog-b"} {
		d, err := screenlogSessionDir(id)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(d, "f.jpg"), []byte("xxxx"), 0o600)
	}

	res := killScreenlog(true)
	if !res.Purged || res.PurgedSessions != 2 {
		t.Fatalf("expected 2 purged sessions, got %+v", res)
	}
	if res.PurgedBytes == 0 {
		t.Error("purged bytes should be > 0")
	}

	base, _ := screenlogDir()
	// Session dirs gone...
	for _, id := range []string{"slog-a", "slog-b"} {
		if _, err := os.Stat(filepath.Join(base, id)); !os.IsNotExist(err) {
			t.Errorf("session %s should be purged", id)
		}
	}
	// ...but control/audit files remain so the kill stays auditable.
	for _, f := range []string{"policy.json", "autostart.json", "audit.jsonl"} {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Errorf("%s should survive purge: %v", f, err)
		}
	}
	if pol := loadScreenlogPolicy(); pol.Enabled {
		t.Error("policy must stay disabled after purge")
	}
}

// TestScreenlogSessionDirRejectsTraversal locks in the path-traversal guard:
// a crafted session id from the HTTP path must never escape the root.
func TestScreenlogSessionDirRejectsTraversal(t *testing.T) {
	withTempScreenlogDir(t)
	for _, bad := range []string{"../evil", "a/b", `..\evil`, "..", "x/../../y", ""} {
		if _, err := screenlogSessionDir(bad); err == nil {
			t.Errorf("screenlogSessionDir(%q) should be rejected", bad)
		}
	}
	if _, err := screenlogSessionDir("slog-good1"); err != nil {
		t.Errorf("valid slug rejected: %v", err)
	}
}
