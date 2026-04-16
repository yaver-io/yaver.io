package main

import (
	"strings"
	"testing"
)

func TestFormatTaskSliceContractRemoteRepoContractExplainsLocalExecution(t *testing.T) {
	contract := &TaskSliceContract{
		RunID:         "run-1",
		NodeID:        "dev",
		DeviceID:      "remote-1",
		DeviceName:    "ubuntu-4gb-hel1-1",
		SourceWorkDir: "/Users/me/project",
		IsolationMode: "remote-repo-contract",
	}

	got := formatTaskSliceContract(contract)
	for _, want := range []string{
		"You are already running on the assigned machine",
		"Do not use SSH",
		"Treat the current filesystem as the assigned machine's workspace",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected contract to contain %q, got:\n%s", want, got)
		}
	}
}
