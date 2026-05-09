package main

// uninstall_cleanup.go — extra cleanup that lives outside ~/.yaver and
// outside the systemd/launchd unit files. Called by both `yaver
// uninstall` (local CLI) and `performPermanentMachineRemoval` (remote
// trigger via /machine/remove from web/mobile) so the two paths
// produce identical end-state.
//
// Each helper is best-effort and idempotent: missing files are not
// errors, partial successes report counts, calling twice on a clean
// system is a no-op. Failures are logged via the returned error
// string but never abort the surrounding uninstall.

import (
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// stripShellRcYaverPath finds the `# yaver-cli PATH` block that
// cli/src/postinstall.js appends to ~/.bashrc / ~/.zshrc / ~/.profile
// and removes it. The block is exactly:
//
//   <blank line>
//   # yaver-cli PATH (added by yaver-cli postinstall)
//   case ":$PATH:" in *":<binDir>:"*) ;; *) export PATH="<binDir>:$PATH" ;; esac
//
// We strip from the marker line back through the leading blank, plus
// the case-statement line that immediately follows. Any other content
// in the rc file is left untouched.
//
// Returns (rcFilesPatched, errs). errs is non-nil when at least one
// file could not be read/written (caller surfaces; uninstall
// continues).
func stripShellRcYaverPath() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	const marker = "# yaver-cli PATH"
	candidates := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".profile"),
	}
	patched := 0
	var errs []string
	for _, rc := range candidates {
		data, err := os.ReadFile(rc)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, rc+": "+err.Error())
			}
			continue
		}
		text := string(data)
		if !strings.Contains(text, marker) {
			continue
		}
		lines := strings.Split(text, "\n")
		out := make([]string, 0, len(lines))
		i := 0
		for i < len(lines) {
			ln := lines[i]
			if strings.HasPrefix(strings.TrimSpace(ln), marker) {
				// Drop the trailing blank line we appended above the
				// marker (postinstall.js wrote `\n# yaver-cli PATH\n…`,
				// so the previous line is often empty).
				if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
					out = out[:len(out)-1]
				}
				// Skip the marker + the case-statement line that follows.
				skip := 1
				if i+1 < len(lines) && strings.Contains(lines[i+1], "export PATH=") {
					skip = 2
				}
				i += skip
				continue
			}
			out = append(out, ln)
			i++
		}
		newText := strings.Join(out, "\n")
		if newText == text {
			continue
		}
		if err := os.WriteFile(rc, []byte(newText), 0o644); err != nil {
			errs = append(errs, rc+": "+err.Error())
			continue
		}
		patched++
	}
	if len(errs) > 0 {
		return patched, errors.New(strings.Join(errs, "; "))
	}
	return patched, nil
}

// stripYaverBootstrapAuthorizedKeys removes lines from the agent's
// ~/.ssh/authorized_keys whose comment field starts with
// `yaver-bootstrap`. Those are the entries that the
// /auth/ssh/authorized-keys endpoint added (label format defined in
// ssh_bootstrap.go: "yaver-bootstrap from <hostname> @ <ts>").
// Pre-existing authorized_keys entries (manual ssh-copy-id, GitHub
// CI keys, teammate keys) are left intact.
//
// Returns (linesRemoved, err). Missing authorized_keys = 0 + nil.
func stripYaverBootstrapAuthorizedKeys() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	akPath := filepath.Join(home, ".ssh", "authorized_keys")
	data, err := os.ReadFile(akPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	removed := 0
	for _, ln := range lines {
		fields := strings.Fields(strings.TrimSpace(ln))
		// authorized_keys line: "<type> <blob> [comment...]". The
		// comment is everything from field 2 to end, joined by space.
		if len(fields) >= 3 {
			comment := strings.Join(fields[2:], " ")
			if strings.HasPrefix(comment, "yaver-bootstrap") {
				removed++
				continue
			}
		}
		out = append(out, ln)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := os.WriteFile(akPath, []byte(strings.Join(out, "\n")), 0o600); err != nil {
		return removed, err
	}
	_ = os.Chmod(akPath, 0o600)
	return removed, nil
}

// disableLingerForCurrentUser unwinds the `loginctl enable-linger`
// call that --install-systemd ran on install (1.99.161+). Without
// this, an uninstalled-then-reinstalled non-root user would inherit
// the previous linger flag — harmless but tidier to clean up.
//
// Best-effort. Failures are silent because `loginctl disable-linger`
// requires root to disable for OTHER users, but a user can always
// disable it for themselves; either way, the next install will
// re-enable.
func disableLingerForCurrentUser() {
	if runtime.GOOS != "linux" {
		return
	}
	user := strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		if u, err := osuser.Current(); err == nil {
			user = strings.TrimSpace(u.Username)
		}
	}
	if user == "" {
		return
	}
	_ = osexec.Command("loginctl", "disable-linger", user).Run()
}

// uninstallExtraCleanup is the shared post-uninstall step run by both
// `yaver uninstall` and `performPermanentMachineRemoval`. Returns a
// human-readable summary line per cleanup that ran, plus an error
// only when something genuinely failed (missing files are not
// errors).
func uninstallExtraCleanup() []string {
	var report []string
	if patched, err := stripShellRcYaverPath(); patched > 0 || err != nil {
		if err != nil {
			report = append(report, fmt.Sprintf("shell rc cleanup: %v", err))
		} else {
			report = append(report, fmt.Sprintf("shell rc PATH block removed from %d file(s)", patched))
		}
	}
	if removed, err := stripYaverBootstrapAuthorizedKeys(); removed > 0 || err != nil {
		if err != nil {
			report = append(report, fmt.Sprintf("authorized_keys cleanup: %v", err))
		} else {
			report = append(report, fmt.Sprintf("removed %d yaver-bootstrap pubkey line(s) from ~/.ssh/authorized_keys", removed))
		}
	}
	disableLingerForCurrentUser()
	return report
}
