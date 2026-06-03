//go:build !windows

package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// managed_units.go installs per-service OS units (systemd user units on Linux,
// launchd LaunchAgents on macOS) that are DECOUPLED from the agent's own unit.
//
// This is what makes a companion long-running service reboot-durable on its
// own: even if the Yaver agent is down, the OS supervisor keeps the service
// running and restarts it on boot. Companion HTTP crons do NOT use this — they
// ride the in-process scheduler which is re-armed when the agent's own unit
// restarts it. Only `services[].durable: true` workers get their own unit.
//
// The generic renderers here intentionally mirror the agent auto-start blocks
// in process_unix.go but stay self-contained so changes here never perturb the
// agent's own auto-start path.

// ManagedUnit describes a single OS-supervised process to install.
type ManagedUnit struct {
	// Name is the OS-level unit name, already namespaced by the caller, e.g.
	// "yaver-companion-eback-mailer". Must be [a-z0-9-] (sanitized by caller).
	Name string
	// ExecPath is the absolute path to the binary to run.
	ExecPath string
	// Args are passed verbatim after ExecPath.
	Args []string
	// WorkDir is the process working directory (absolute, on-device only).
	WorkDir string
	// Env is resolved on-device (vault + dotenv). NEVER synced to Convex — it
	// lives only inside the unit file on local disk.
	Env map[string]string
	// LogDir is where stdout/stderr are written (launchd) — systemd uses the
	// journal. Defaults to ~/.yaver/companion/logs when empty.
	LogDir string
}

// durableUnitsSupported reports whether this platform can install per-service
// OS units. WSL has no usable systemd --user across the fleet, so companion
// durable services fall back to the agent-child model there.
func durableUnitsSupported() bool {
	switch runtime.GOOS {
	case "darwin":
		return true
	case "linux":
		return !isWSL()
	default:
		return false
	}
}

// writeManagedUnit installs and enables a per-service OS unit. Returns a
// human-facing message. It is idempotent: re-installing overwrites the unit
// and reloads it.
func writeManagedUnit(u ManagedUnit) (string, error) {
	if strings.TrimSpace(u.Name) == "" || strings.TrimSpace(u.ExecPath) == "" {
		return "", fmt.Errorf("managed unit requires Name and ExecPath")
	}
	switch runtime.GOOS {
	case "darwin":
		return writeManagedLaunchAgent(u)
	case "linux":
		if isWSL() {
			return "", fmt.Errorf("durable OS units unsupported under WSL; run as agent-child service")
		}
		return writeManagedSystemdUserUnit(u)
	default:
		return "", fmt.Errorf("durable OS units unsupported on %s", runtime.GOOS)
	}
}

// removeManagedUnit stops, disables and deletes a previously installed unit.
// Safe to call when the unit does not exist.
func removeManagedUnit(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return removeManagedLaunchAgent(name)
	case "linux":
		if isWSL() {
			return nil
		}
		return removeManagedSystemdUserUnit(name)
	default:
		return nil
	}
}

// --- systemd (Linux) ---

func systemdUserUnitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// renderSystemdUserUnit produces a [Unit]/[Service]/[Install] block for a
// per-service companion worker. Pure (no side effects) so it is unit-testable.
// Env keys are sorted for deterministic output.
func renderSystemdUserUnit(name, exec string, args []string, workDir string, env map[string]string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString(fmt.Sprintf("Description=%s (Yaver companion service)\n", name))
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString(fmt.Sprintf("ExecStart=%s\n", systemdExecLine(exec, args)))
	if strings.TrimSpace(workDir) != "" {
		b.WriteString(fmt.Sprintf("WorkingDirectory=%s\n", workDir))
	}
	for _, k := range sortedKeys(env) {
		// systemd Environment= one quoted assignment per line; quotes and
		// backslashes in the value are escaped so secrets with =,/," survive.
		b.WriteString(fmt.Sprintf("Environment=\"%s=%s\"\n", k, systemdEscape(env[k])))
	}
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=5\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

func writeManagedSystemdUserUnit(u ManagedUnit) (string, error) {
	dir, err := systemdUserUnitDir()
	if err != nil {
		return "", err
	}
	unitPath := filepath.Join(dir, u.Name+".service")
	unit := renderSystemdUserUnit(u.Name, u.ExecPath, u.Args, u.WorkDir, u.Env)
	if err := os.WriteFile(unitPath, []byte(unit), 0600); err != nil {
		return "", fmt.Errorf("write unit: %w", err)
	}
	osexec.Command("systemctl", "--user", "daemon-reload").Run()
	osexec.Command("systemctl", "--user", "enable", u.Name).Run()
	if out, err := osexec.Command("systemctl", "--user", "restart", u.Name).CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemctl restart %s: %s: %w", u.Name, strings.TrimSpace(string(out)), err)
	}
	if user := os.Getenv("USER"); user != "" {
		osexec.Command("loginctl", "enable-linger", user).Run()
	}
	return fmt.Sprintf("installed systemd user unit %s", unitPath), nil
}

func removeManagedSystemdUserUnit(name string) error {
	dir, err := systemdUserUnitDir()
	if err != nil {
		return err
	}
	osexec.Command("systemctl", "--user", "stop", name).Run()
	osexec.Command("systemctl", "--user", "disable", name).Run()
	_ = os.Remove(filepath.Join(dir, name+".service"))
	osexec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func systemdExecLine(exec string, args []string) string {
	parts := []string{exec}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func systemdEscape(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "\"", "\\\"")
	return v
}

// --- launchd (macOS) ---

// managedLaunchdLabel maps a unit name to a reverse-DNS launchd label.
func managedLaunchdLabel(name string) string {
	return "io.yaver.companion." + name
}

func managedLaunchAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// buildManagedLaunchdPlist renders a LaunchAgent plist for an arbitrary
// command + args + env. Pure (no side effects). Env keys are sorted and XML
// values escaped.
func buildManagedLaunchdPlist(label, exec string, args []string, workDir string, env map[string]string, logDir string) string {
	var prog strings.Builder
	prog.WriteString("    <key>ProgramArguments</key>\n    <array>\n")
	prog.WriteString("        <string>" + xmlEscape(exec) + "</string>\n")
	for _, a := range args {
		prog.WriteString("        <string>" + xmlEscape(a) + "</string>\n")
	}
	prog.WriteString("    </array>\n")

	var envDict strings.Builder
	if len(env) > 0 {
		envDict.WriteString("    <key>EnvironmentVariables</key>\n    <dict>\n")
		for _, k := range sortedKeys(env) {
			envDict.WriteString("        <key>" + xmlEscape(k) + "</key>\n")
			envDict.WriteString("        <string>" + xmlEscape(env[k]) + "</string>\n")
		}
		envDict.WriteString("    </dict>\n")
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
%s    <key>WorkingDirectory</key>
    <string>%s</string>
%s    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, xmlEscape(label), prog.String(), xmlEscape(workDir), envDict.String(),
		xmlEscape(filepath.Join(logDir, label+".out.log")),
		xmlEscape(filepath.Join(logDir, label+".err.log")))
}

func writeManagedLaunchAgent(u ManagedUnit) (string, error) {
	dir, err := managedLaunchAgentDir()
	if err != nil {
		return "", err
	}
	logDir := u.LogDir
	if strings.TrimSpace(logDir) == "" {
		home, _ := os.UserHomeDir()
		logDir = filepath.Join(home, ".yaver", "companion", "logs")
	}
	_ = os.MkdirAll(logDir, 0700)

	label := managedLaunchdLabel(u.Name)
	plistPath := filepath.Join(dir, label+".plist")
	plist := buildManagedLaunchdPlist(label, u.ExecPath, u.Args, u.WorkDir, u.Env, logDir)
	if err := os.WriteFile(plistPath, []byte(plist), 0600); err != nil {
		return "", fmt.Errorf("write plist: %w", err)
	}
	osexec.Command("launchctl", "unload", plistPath).Run()
	if out, err := osexec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("launchctl load: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return fmt.Sprintf("installed LaunchAgent %s", plistPath), nil
}

func removeManagedLaunchAgent(name string) error {
	dir, err := managedLaunchAgentDir()
	if err != nil {
		return err
	}
	label := managedLaunchdLabel(name)
	plistPath := filepath.Join(dir, label+".plist")
	osexec.Command("launchctl", "unload", plistPath).Run()
	_ = os.Remove(plistPath)
	return nil
}

// --- shared helpers ---
// sortedKeys is defined in diagnose.go (generic over map value type).

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
