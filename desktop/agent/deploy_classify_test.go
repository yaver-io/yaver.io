package main

import (
	"testing"
)

func TestClassifyDeployOutput(t *testing.T) {
	cases := []struct {
		name      string
		tail      string
		exit      int
		timedOut  bool
		wantClass DeployErrorClass
		wantOK    bool
	}{
		{
			name:      "redundant binary is treated as success",
			tail:      "*** Error: Redundant Binary Upload. There already exists a binary ...",
			exit:      70,
			wantClass: DeployErrAlreadyUploaded,
			wantOK:    true,
		},
		{
			name:      "wrong passphrase trips vault_locked",
			tail:      "Error: wrong passphrase or corrupted vault\nexiting",
			exit:      1,
			wantClass: DeployErrVaultLocked,
		},
		{
			name:      "command not found → toolchain_missing",
			tail:      "bash: xcodebuild: command not found",
			exit:      127,
			wantClass: DeployErrToolchainMiss,
		},
		{
			name:      "401 Unauthorized → auth_error",
			tail:      "HTTP 401 Unauthorized",
			exit:      1,
			wantClass: DeployErrAuthError,
		},
		{
			name:      "code signing error → signing_error",
			tail:      "error: Code Signing error: No signing certificate ...",
			exit:      65,
			wantClass: DeployErrSignRing,
		},
		{
			name:      "network error",
			tail:      "dial tcp 1.2.3.4:443: connection refused",
			exit:      1,
			wantClass: DeployErrNetwork,
		},
		{
			name:      "disk full",
			tail:      "write /tmp/a: No space left on device",
			exit:      1,
			wantClass: DeployErrDiskFull,
		},
		{
			name:      "context deadline → timeout",
			tail:      "error: context deadline exceeded",
			exit:      -1,
			wantClass: DeployErrTimeout,
		},
		{
			name:      "timedOut arg wins over content",
			tail:      "",
			exit:      -1,
			timedOut:  true,
			wantClass: DeployErrTimeout,
		},
		{
			name:      "unknown non-zero → build_failed",
			tail:      "Undefined symbols for architecture arm64: _foo",
			exit:      1,
			wantClass: DeployErrBuildFailed,
		},
		{
			name:      "exit 0 with empty tail → unknown + ok",
			tail:      "",
			exit:      0,
			wantClass: DeployErrUnknown,
			wantOK:    true,
		},
		{
			name:      "preflight gate failure",
			tail:      "Preflight failed — re-run with:\n  yaver doctor build --target=cloudflare",
			exit:      1,
			wantClass: DeployErrPreflight,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cls, ok := ClassifyDeployOutput(tc.tail, tc.exit, tc.timedOut)
			if cls != tc.wantClass {
				t.Errorf("class: got %q, want %q", cls, tc.wantClass)
			}
			// treatAsOK should only be true for classes we specifically
			// designed to rewrite OK (currently: already_uploaded) or
			// for a clean exit-0 run. Every failure case should flip
			// treatAsOK to false.
			if tc.wantOK && !ok {
				t.Errorf("expected treatAsOK=true, got false")
			}
			if !tc.wantOK && ok {
				t.Errorf("expected treatAsOK=false, got true")
			}
		})
	}
}
