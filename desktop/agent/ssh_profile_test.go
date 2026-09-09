package main

import (
	"strings"
	"testing"
)

func TestSSHProfileLaunchCommandIsStructuredAndPersistent(t *testing.T) {
	command := sshProfileLaunchCommand(&SSHProfile{Shell: "zsh", Tmux: true, TmuxSession: "work.main"})
	for _, want := range []string{"command -v zsh", "yaver install zsh", "command -v tmux", "new-session -A -s work.main", "yaver install tmux"} {
		if !strings.Contains(command, want) {
			t.Fatalf("profile launch command missing %q: %s", want, command)
		}
	}
}

func TestSSHProfileShellRemediesHaveInstallPlans(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "tmux"} {
		plan, ok := metaInstallPlan(shell)
		if !ok || plan.name != shell || (len(plan.macOS) == 0 && len(plan.linux) == 0) {
			t.Fatalf("profile remedy yaver install %s has no executable install plan: %+v", shell, plan)
		}
	}
}

func TestSSHProfileRejectsCommandInjectionThroughSessionName(t *testing.T) {
	p := normalizeSSHProfile(&SSHProfile{Shell: "not-a-shell; touch /tmp/nope", Tmux: true, TmuxSession: "x; touch /tmp/nope"})
	if p.Shell != "default" || p.TmuxSession != "yaver" {
		t.Fatalf("unsafe values were not reduced to fixed defaults: %+v", p)
	}
	command := sshProfileLaunchCommand(p)
	if strings.Contains(command, "touch") {
		t.Fatalf("untrusted command text leaked into launch command: %s", command)
	}
}

func TestSSHArgsWithProfileOnlyOverridesInteractiveSession(t *testing.T) {
	profile := &SSHProfile{Shell: "zsh", Tmux: true, TmuxSession: "yaver"}
	interactive := strings.Join(sshArgsWithProfile("user@example.test", nil, profile), " ")
	if !strings.Contains(interactive, " -t user@example.test ") || !strings.Contains(interactive, "tmux new-session") {
		t.Fatalf("interactive profile not added to SSH argv: %s", interactive)
	}
	command := strings.Join(sshArgsWithProfile("user@example.test", []string{"uname", "-a"}, profile), " ")
	if strings.Contains(command, "tmux new-session") || !strings.HasSuffix(command, "user@example.test uname -a") {
		t.Fatalf("explicit remote command must bypass interactive profile: %s", command)
	}
}

func TestTerminalWSURLCarriesSafeSSHProfile(t *testing.T) {
	got, err := terminalWSURLProfile("https://relay.example.test/d/device", "token", "", &SSHProfile{
		Shell: "zsh", Tmux: true, TmuxSession: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "profile_shell=zsh") || !strings.Contains(got, "profile_tmux=work") {
		t.Fatalf("relay terminal lost SSH profile: %s", got)
	}
}

func TestSSHProfileDeviceLabelUsesStableFallbackOrder(t *testing.T) {
	device := &DeviceInfo{Alias: " primary ", Name: "host", DeviceID: "device-id"}
	if got := sshProfileDeviceLabel(device); got != "primary" {
		t.Fatalf("alias should win: %q", got)
	}
	device.Alias = ""
	if got := sshProfileDeviceLabel(device); got != "host" {
		t.Fatalf("name should be the second choice: %q", got)
	}
	device.Name = ""
	if got := sshProfileDeviceLabel(device); got != "device-id" {
		t.Fatalf("device id should be the final stable identifier: %q", got)
	}
	if got := sshProfileDeviceLabel(nil); got != "device" {
		t.Fatalf("nil device should keep output readable: %q", got)
	}
}
