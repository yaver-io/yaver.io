package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: the auto-reconciler must NOT mistake the npm-shipped
// `bin/yaver` Node entry-point for a stale Go binary. Pre-fix, it would
// have happily overwritten the 105-byte launcher with the 41 MB Go agent
// and bricked every npm install on the next boot. Mark these recognised
// shapes IsNPMWrapper=true so apply skips them.
func TestLooksLikeNPMWrapperScript_recognised(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"env-node":          "#!/usr/bin/env node\n\nrequire('../src/index').runUnified(process.argv.slice(2));\n",
		"env-dash-s-node":   "#!/usr/bin/env -S node --no-warnings\n\nconsole.log('hi');\n",
		"absolute-bin-node": "#!/usr/bin/node\nconsole.log('legacy shebang');\n",
		"local-bin-node":    "#!/usr/local/bin/node\nconsole.log('homebrew');\n",
	}
	for name, body := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if !looksLikeNPMWrapperScript(path, info.Size()) {
			t.Errorf("%s: expected wrapper detection, got false (size=%d, body=%q)", name, info.Size(), body)
		}
	}
}

// Files that must NOT be flagged as the wrapper. The 41 MB compiled Go
// agent is the canonical case — overwriting the wrapper with its bytes
// is the exact failure mode this guard exists to prevent. A small but
// non-Node-shebang script (e.g. a bash wrapper an operator wrote by
// hand) is also out of scope: we only protect the npm-shipped shape.
func TestLooksLikeNPMWrapperScript_rejected(t *testing.T) {
	dir := t.TempDir()

	// 1. Compiled Go binary — large, no shebang. Use 8 KB of
	// non-shebang bytes so the 4 KB cap rejects it on size alone.
	bigPath := filepath.Join(dir, "yaver-go-binary")
	bigBody := make([]byte, 8192)
	bigBody[0] = 0x7f
	bigBody[1] = 'E'
	bigBody[2] = 'L'
	bigBody[3] = 'F'
	if err := os.WriteFile(bigPath, bigBody, 0o755); err != nil {
		t.Fatalf("write big: %v", err)
	}
	info, _ := os.Stat(bigPath)
	if looksLikeNPMWrapperScript(bigPath, info.Size()) {
		t.Errorf("compiled Go binary (size=%d) was misdetected as wrapper", info.Size())
	}

	// 2. Tiny shell script with bash shebang — small enough to pass the
	// size cap, but not the npm wrapper. Explicit reject.
	shPath := filepath.Join(dir, "yaver-bash-wrapper")
	if err := os.WriteFile(shPath, []byte("#!/bin/bash\nexec ~/.yaver/bin/current/yaver \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write sh: %v", err)
	}
	info, _ = os.Stat(shPath)
	if looksLikeNPMWrapperScript(shPath, info.Size()) {
		t.Errorf("bash wrapper script was misdetected as npm-node wrapper")
	}

	// 3. Empty file — should be rejected (no header, no shebang).
	emptyPath := filepath.Join(dir, "empty")
	if err := os.WriteFile(emptyPath, nil, 0o755); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	info, _ = os.Stat(emptyPath)
	if looksLikeNPMWrapperScript(emptyPath, info.Size()) {
		t.Errorf("empty file was misdetected as wrapper")
	}
}

// hasReconcilableDrift gates the startup auto-apply. It must be false
// when the only "different" install is the npm wrapper (would brick
// the launcher), a managed package-manager binary (apt would revert),
// or unwritable (we'd just log an apply error). And true the moment a
// plain stale copy at /usr/local/bin or ~/.local/bin shows up.
func TestHasReconcilableDrift_gates(t *testing.T) {
	canonical := YaverInstall{Path: "/home/u/.yaver/bin/1.99.168/linux-arm64/yaver", Version: "1.99.168", SameAsRunning: true, IsRunningBinary: true, Writable: true}

	tests := []struct {
		name     string
		installs []YaverInstall
		want     bool
	}{
		{
			name:     "only canonical present",
			installs: []YaverInstall{canonical},
			want:     false,
		},
		{
			name: "wrapper sibling — must skip",
			installs: []YaverInstall{
				canonical,
				{Path: "/home/u/.local/bin/yaver", SHA256: "wrapper-hash", Writable: true, IsNPMWrapper: true},
			},
			want: false,
		},
		{
			name: "managed sibling — must skip",
			installs: []YaverInstall{
				canonical,
				{Path: "/usr/bin/yaver", SHA256: "apt-hash", Writable: true, IsManaged: true, Manager: "apt"},
			},
			want: false,
		},
		{
			name: "unwritable sibling — must skip",
			installs: []YaverInstall{
				canonical,
				{Path: "/usr/local/bin/yaver", SHA256: "stale-hash", Writable: false},
			},
			want: false,
		},
		{
			name: "plain stale copy — must reconcile",
			installs: []YaverInstall{
				canonical,
				{Path: "/home/u/.local/bin/legacy-yaver", SHA256: "stale-hash", Writable: true},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &SelfHealReport{Canonical: canonical, Installs: tt.installs}
			if got := hasReconcilableDrift(rep); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
