package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSSHFleetDeviceIDRoundTrip(t *testing.T) {
	id := sshFleetDeviceID(" Build-Pi ")
	if id != "ssh:build-pi" {
		t.Fatalf("device id = %q", id)
	}
	name, ok := sshFleetTargetName(id)
	if !ok || name != "build-pi" {
		t.Fatalf("round trip = %q, %v", name, ok)
	}
	if _, ok := sshFleetTargetName("registered-device"); ok {
		t.Fatal("registered device was misclassified as an SSH worker")
	}
}

func TestSSHFleetWorkerParticipatesWithoutConvexMembership(t *testing.T) {
	prefs := &agentFleetPreferences{
		ControllerDeviceID: "master",
		WorkerDeviceIDs:    map[string]bool{},
		Opportunistic:      true,
	}
	machine := MachineInfo{DeviceID: "ssh:pi", FleetWorker: true, ConnectionKind: "ssh", IsOnline: true}
	if !fleetMachineEligible(prefs, AgentGraphCreateRequest{}, nil, machine) {
		t.Fatal("configured SSH worker was excluded because it had no Convex device row")
	}
	prefs.Opportunistic = false
	if fleetMachineEligible(prefs, AgentGraphCreateRequest{}, nil, machine) {
		t.Fatal("automatic worker disable must also apply to SSH workers")
	}
	if !fleetMachineEligible(prefs, AgentGraphCreateRequest{AllowedDevices: []string{"ssh:pi"}}, nil, machine) {
		t.Fatal("an explicit SSH worker pin must survive automatic worker disable")
	}
}

func TestSSHFleetWorkerMustPassOperationalProbeForAutomaticPlacement(t *testing.T) {
	machine := MachineInfo{DeviceID: "ssh:pi", FleetWorker: true, ConnectionKind: "ssh", IsOnline: false}
	if fleetMachineEligible(nil, AgentGraphCreateRequest{}, nil, machine) {
		t.Fatal("an unreachable SSH worker must not be selected automatically")
	}
	if !fleetMachineEligible(nil, AgentGraphCreateRequest{AllowedDevices: []string{"ssh:pi"}}, nil, machine) {
		t.Fatal("an explicit unreachable worker must remain selectable so execution reports its named remedy")
	}
}

func TestSSHFleetWorkerPreferenceFeedsPlacement(t *testing.T) {
	machine := MachineInfo{
		DeviceID:        "ssh:pi",
		PreferredRunner: "opencode",
		PreferredModel:  "deepseek/deepseek-flash",
		Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{
			{ID: "opencode", Ready: true},
		}},
	}
	runner := chooseCandidateRunnerWithState(AgentGraphCreateRequest{}, AgentGraphNodeSpec{}, machine, &meshPlannerState{})
	if runner != "opencode" {
		t.Fatalf("runner = %q", runner)
	}
	if model := choosePlacementModelForMachine(AgentGraphNodeSpec{}, runner, machine, nil); model != "deepseek/deepseek-flash" {
		t.Fatalf("model = %q", model)
	}
}

func TestProbeSSHFleetWorkerUsesOperationAndNamesRemedy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh executable is a POSIX shell script")
	}
	dir := t.TempDir()
	sshPath := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\nprintf '%s' '{\"deviceId\":\"remote\",\"name\":\"host\",\"platform\":\"linux\",\"isOnline\":true,\"capabilities\":{\"hardware\":{},\"runners\":[{\"id\":\"opencode\",\"ready\":true}]}}'\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	row := probeSSHFleetWorker(context.Background(), SSHTarget{
		Name: "pi", Host: "pi.local", FleetWorker: true, WorkDir: "/srv/repo", Runner: "opencode",
	})
	if !row.IsOnline || row.DeviceID != "ssh:pi" || row.ConnectionKind != "ssh" || row.YaverAuthRequired {
		t.Fatalf("unexpected SSH worker receipt: %+v", row)
	}

	badScript := "#!/bin/sh\necho 'connection refused' >&2\nexit 1\n"
	if err := os.WriteFile(sshPath, []byte(badScript), 0o700); err != nil {
		t.Fatal(err)
	}
	row = probeSSHFleetWorker(context.Background(), SSHTarget{Name: "broken-pi", Host: "broken.local", FleetWorker: true})
	if row.IsOnline || !strings.Contains(row.StatusReason, "connection refused") || !strings.Contains(row.Remedy, "sign-in is not required") {
		t.Fatalf("failure did not carry a named route: %+v", row)
	}
}

func TestSSHFleetPatchMaterializesIntoControllerWorktree(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Yaver Test", "GIT_AUTHOR_EMAIL=test@yaver.invalid", "GIT_COMMITTER_NAME=Yaver Test", "GIT_COMMITTER_EMAIL=test@yaver.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "base")
	base := runGit("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("worker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch, err := gitPatchFromBase(repo, base, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	runGit("reset", "--hard", base)
	if err := os.Remove(filepath.Join(repo, "new.txt")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	worktree, err := materializeSSHFleetPatch(context.Background(), repo, "run-patch-test", "fleet-main", base, patch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(worktree, "base.txt"))
	if err != nil || string(got) != "worker\n" {
		t.Fatalf("tracked patch = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(worktree, "new.txt"))
	if err != nil || string(got) != "new\n" {
		t.Fatalf("new-file patch = %q, %v", got, err)
	}
}
