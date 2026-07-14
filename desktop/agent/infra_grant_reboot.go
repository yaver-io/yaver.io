package main

// Opt-in reboot permission.
//
// Rebooting a host needs root, and the agent runs as an ordinary user under
// launchd/systemd. So "Reboot host" was an offer we could not honour: the button
// was enabled on every macOS/Linux box (the capability was a claim about the OS,
// not about us) and simply failed when tapped.
//
// The honest fix has two halves. capabilities.hostReboot now VERIFIES the
// permission (canRebootHost), so a box without it says so. This file is the
// other half: letting the owner GRANT it, deliberately, from wherever they are —
// the phone, the dashboard, the CLI — by supplying their sudo password once.
//
// Rules this flow lives by:
//
//   - Opt-in, never automatic. Yaver does not escalate its own privileges as a
//     side effect of some other action. The user asks for this, explicitly.
//   - The password is used once and never stored: it is piped to `sudo -S` on
//     stdin, held in a local variable for the length of one exec, and zeroed. It
//     is never written to the config, the vault, a log line, or Convex.
//   - The grant is MINIMAL. Not blanket NOPASSWD-ALL — just the two reboot
//     binaries. A stolen agent token gets you a reboot, not a root shell.
//   - The rule is validated with `visudo -c` before it is installed. A malformed
//     sudoers file can lock the user out of sudo entirely on their own machine;
//     that is not an acceptable failure mode for a convenience feature.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// rebootSudoersPath is the drop-in we own. Named so it is obvious who wrote it and
// safe to delete by hand.
const rebootSudoersPath = "/etc/sudoers.d/yaver-reboot"

// sudoersRule is the least privilege that makes `host_reboot` work: the reboot
// binaries, nothing else. Kept as a function so the username is never templated
// from anything but the OS's own idea of who we are.
func sudoersRule(username string) string {
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("%s ALL=(root) NOPASSWD: /sbin/shutdown, /sbin/reboot\n", username)
	default: // linux
		return fmt.Sprintf("%s ALL=(root) NOPASSWD: /sbin/shutdown, /sbin/reboot, /bin/systemctl reboot, /usr/bin/systemctl reboot\n", username)
	}
}

// grantRebootPermission installs the sudoers drop-in using a sudo password the
// owner supplied for this one call. Returns the resulting capability so the
// caller can render the new state without waiting for a heartbeat.
func grantRebootPermission(password string) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return fmt.Errorf("host reboot is not supported on %s", runtime.GOOS)
	}
	if os.Geteuid() == 0 {
		return nil // already root — nothing to grant
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("a sudo password is required to grant reboot permission")
	}

	rule := sudoersRule(currentUsername())

	// Write the candidate rule to a private temp file and let visudo check it
	// BEFORE it goes anywhere near /etc/sudoers.d. An invalid file there can
	// break sudo for every user on the machine.
	tmp, err := os.CreateTemp("", "yaver-reboot-*.sudoers")
	if err != nil {
		return fmt.Errorf("could not stage the sudoers rule: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(rule); err != nil {
		tmp.Close()
		return fmt.Errorf("could not stage the sudoers rule: %w", err)
	}
	tmp.Close()
	if err := os.Chmod(tmpPath, 0o440); err != nil {
		return fmt.Errorf("could not stage the sudoers rule: %w", err)
	}

	if out, err := exec.Command("visudo", "-c", "-f", tmpPath).CombinedOutput(); err != nil {
		return fmt.Errorf("refusing to install a sudoers rule that does not validate: %s",
			strings.TrimSpace(string(out)))
	}

	// Install it with the owner's password. `sudo -S` reads the password from
	// stdin; `-k` forces a fresh prompt so we never silently ride an existing
	// sudo timestamp (which would let a caller with no password succeed).
	install := exec.Command("sudo", "-S", "-k", "install", "-m", "0440", "-o", "root", "-g",
		sudoersGroup(), tmpPath, rebootSudoersPath)
	install.Stdin = strings.NewReader(password + "\n")
	out, err := install.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "incorrect password") || strings.Contains(msg, "Sorry, try again") {
			return fmt.Errorf("that sudo password was not accepted")
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("could not install the sudoers rule: %s", msg)
	}

	// Prove it actually worked rather than trusting the exit code.
	if !canRebootHost() {
		return fmt.Errorf("the sudoers rule was installed but reboot is still not permitted — check %s", rebootSudoersPath)
	}
	return nil
}

// sudoersGroup is the group root-owned system files use on this OS.
func sudoersGroup() string {
	if runtime.GOOS == "darwin" {
		return "wheel"
	}
	return "root"
}

// revokeRebootPermission removes the drop-in we installed. Granting a privilege
// you cannot take back is not a real opt-in.
func revokeRebootPermission(password string) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return fmt.Errorf("host reboot is not supported on %s", runtime.GOOS)
	}
	if _, err := os.Stat(rebootSudoersPath); os.IsNotExist(err) {
		return nil // nothing granted by us
	}
	rm := exec.Command("sudo", "-S", "-k", "rm", "-f", rebootSudoersPath)
	rm.Stdin = strings.NewReader(password + "\n")
	if out, err := rm.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("could not remove %s: %s", rebootSudoersPath, msg)
	}
	return nil
}

// rebootGrantHint is what a surface shows when it cannot (or will not) take a
// password: the exact command, so the user can do it themselves on the box.
func rebootGrantHint() string {
	return fmt.Sprintf("echo '%s' | sudo tee %s >/dev/null && sudo chmod 0440 %s",
		strings.TrimSpace(sudoersRule(currentUsername())), rebootSudoersPath, rebootSudoersPath)
}

// handleInfraRebootGrant — POST /infra/reboot-grant
//
//	{"password":"…"}            → install the rule
//	{"password":"…","revoke":true} → remove it
//
// Owner-only (the route is mounted behind s.auth). Never logs the body.
func (s *HTTPServer) handleInfraRebootGrant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Password string `json:"password"`
		Revoke   bool   `json:"revoke"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	// Hold the password for exactly as long as the exec needs it.
	password := req.Password
	req.Password = ""
	defer func() { password = "" }()

	var err error
	if req.Revoke {
		err = revokeRebootPermission(password)
	} else {
		err = grantRebootPermission(password)
	}
	if err != nil {
		// The error text is user-facing guidance ("that sudo password was not
		// accepted"), never the password itself.
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                true,
		"canReboot":         canRebootHost(),
		"rebootSudoersPath": rebootSudoersPath,
	})
}

// rebootGrantState is what surfaces render: whether reboot works, and if not,
// exactly what granting it would take.
type rebootGrantState struct {
	CanReboot bool   `json:"canReboot"`
	NeedsSudo bool   `json:"needsSudo"`
	AgentUser string `json:"agentUser"`
	GrantHint string `json:"grantHint,omitempty"`
	Granted   bool   `json:"granted"` // true when OUR drop-in is what permits it
	CheckedAt int64  `json:"checkedAt"`
}

func currentRebootGrantState() rebootGrantState {
	can := canRebootHost()
	st := rebootGrantState{
		CanReboot: can,
		NeedsSudo: !can && os.Geteuid() != 0,
		AgentUser: currentUsername(),
		CheckedAt: time.Now().UnixMilli(),
	}
	if !can {
		st.GrantHint = rebootGrantHint()
	}
	if _, err := os.Stat(filepath.Clean(rebootSudoersPath)); err == nil {
		st.Granted = true
	}
	return st
}
