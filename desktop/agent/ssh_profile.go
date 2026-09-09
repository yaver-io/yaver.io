package main

// ssh_profile.go — safe, Convex-backed interactive shell preferences.
//
// The profile is deliberately structured. Yaver stores a shell choice and a
// tmux session name, never an arbitrary command to execute on login. This gives
// users a persistent "make this box feel like my machine" setup without making
// a stolen web/Convex session a deferred remote-code-execution channel.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type SSHProfile struct {
	Shell       string `json:"shell"`
	Tmux        bool   `json:"tmux"`
	TmuxSession string `json:"tmuxSession,omitempty"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
}

var sshProfileSessionPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,48}$`)

func normalizeSSHProfile(profile *SSHProfile) *SSHProfile {
	if profile == nil {
		return nil
	}
	shell := strings.ToLower(strings.TrimSpace(profile.Shell))
	switch shell {
	case "default", "bash", "zsh", "fish":
	default:
		shell = "default"
	}
	session := strings.TrimSpace(profile.TmuxSession)
	if session == "" {
		session = "yaver"
	}
	if !sshProfileSessionPattern.MatchString(session) {
		session = "yaver"
	}
	return &SSHProfile{Shell: shell, Tmux: profile.Tmux, TmuxSession: session, UpdatedAt: profile.UpdatedAt}
}

// sshProfileLaunchCommand returns the fixed command family used by both
// OpenSSH and /ws/terminal. User-controlled values are enum/syntax validated
// before interpolation. Missing tools degrade to the machine's login shell and
// print the exact streamed installer route instead of failing the session.
func sshProfileLaunchCommand(profile *SSHProfile) string {
	p := normalizeSSHProfile(profile)
	if p == nil || (p.Shell == "default" && !p.Tmux) {
		return ""
	}
	wanted := p.Shell
	if wanted == "default" {
		wanted = ""
	}
	parts := []string{"yaver_shell=${SHELL:-/bin/sh}"}
	if wanted != "" {
		parts = append(parts,
			"if command -v "+wanted+" >/dev/null 2>&1; then yaver_shell=$(command -v "+wanted+"); else printf '\\nYaver: "+wanted+" is not installed; continuing with %s. Install: yaver install "+wanted+"\\n' \"$yaver_shell\" >&2; fi",
		)
	}
	if p.Tmux {
		parts = append(parts,
			"if command -v tmux >/dev/null 2>&1; then exec tmux new-session -A -s "+p.TmuxSession+" \"$yaver_shell -l\"; else printf '\\nYaver: tmux is not installed; continuing without persistence. Install: yaver install tmux\\n' >&2; fi",
		)
	}
	parts = append(parts, "exec \"$yaver_shell\" -l")
	return strings.Join(parts, "; ")
}

func sshArgsWithProfile(dest string, passthrough []string, profile *SSHProfile) []string {
	args := sshArgsWithSurvivability(dest, nil)
	if launch := sshProfileLaunchCommand(profile); launch != "" && len(passthrough) == 0 {
		// Force a PTY because tmux and an interactive login shell both need one.
		args = append(args[:len(args)-1], "-t", dest, launch)
		return args
	}
	return append(args, passthrough...)
}

func resolveSSHProfileDevice(hint string) (*DeviceInfo, error) {
	if strings.EqualFold(hint, "primary") || strings.EqualFold(hint, "secondary") {
		_, device, err := resolveSSHSlotDevice(strings.ToLower(hint))
		return device, err
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return nil, fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("could not list devices: %w", err)
	}
	return resolveDevice(hint, devices)
}

func sshProfileDeviceLabel(device *DeviceInfo) string {
	if device == nil {
		return "device"
	}
	if alias := strings.TrimSpace(device.Alias); alias != "" {
		return alias
	}
	if name := strings.TrimSpace(device.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(device.DeviceID); id != "" {
		return id
	}
	return "device"
}

func saveSSHProfile(deviceID string, profile SSHProfile) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	p := normalizeSSHProfile(&profile)
	body, _ := json.Marshal(map[string]any{
		"deviceId":    deviceID,
		"shell":       p.Shell,
		"tmux":        p.Tmux,
		"tmuxSession": p.TmuxSession,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.ConvexSiteURL, "/")+"/devices/ssh-profile", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var result struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		if result.Error == "" {
			result.Error = resp.Status
		}
		return fmt.Errorf("save SSH profile: %s", result.Error)
	}
	return nil
}

func runSSHProfileSubcommand(args []string) {
	if len(args) < 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(`yaver ssh profile <primary|secondary|alias|device> [options]

With no options, prints the device's current interactive profile.

Options:
  --shell default|bash|zsh|fish  Preferred login shell
  --tmux [session]               Attach/create a persistent tmux session
  --no-tmux                      Open the login shell without tmux

Tool installation is explicit and streamed:
  yaver install zsh
  yaver install tmux`)
		return
	}
	dev, err := resolveSSHProfileDevice(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	p := normalizeSSHProfile(dev.SSHProfile)
	if p == nil {
		p = &SSHProfile{Shell: "default", TmuxSession: "yaver"}
	}
	if len(args) == 1 {
		fmt.Printf("%s: shell=%s tmux=%t", sshProfileDeviceLabel(dev), p.Shell, p.Tmux)
		if p.Tmux {
			fmt.Printf(" session=%s", p.TmuxSession)
		}
		fmt.Println()
		return
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--shell":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--shell needs default, bash, zsh, or fish")
				return
			}
			i++
			p.Shell = args[i]
		case "--tmux":
			p.Tmux = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				p.TmuxSession = args[i]
			}
		case "--no-tmux":
			p.Tmux = false
		default:
			fmt.Fprintf(os.Stderr, "unknown profile option %q\n", args[i])
			return
		}
	}
	p = normalizeSSHProfile(p)
	if err := saveSSHProfile(dev.DeviceID, *p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Printf("✓ %s SSH profile: shell=%s tmux=%t", sshProfileDeviceLabel(dev), p.Shell, p.Tmux)
	if p.Tmux {
		fmt.Printf(" session=%s", p.TmuxSession)
	}
	fmt.Println()
}
