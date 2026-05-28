package main

// launch_ssh.go — adopts an existing Linux box you can SSH to (NAS, VPS,
// homelab, on-prem). Unlike cloud provisioners this doesn't spin up a
// VM: it ssh's in, installs `yaver-cli` from npm (the canonical install
// path per CLAUDE.md), drops a pre-authorized pending-auth.json, and
// starts `yaver serve` as a background tmux session so the agent comes
// online without any human interaction on the target.
//
// Works on anything that:
//   - Lets you SSH in as a user with passwordless sudo
//   - Has apt (Debian/Ubuntu) — extending to other package managers is
//     a follow-up
//
// Doesn't create a real systemd unit yet; that's tracked as a follow-up.
// `yaver serve` in tmux is the simplest thing that's resilient enough
// for "adopt and forget" (the user can wire systemd later or use the
// pi-image-style firstboot service).

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func launchSSH(ctx context.Context, opts *launchOptions) error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return fmt.Errorf("ssh not found on PATH")
	}

	// Probe the target first so the user gets a fast failure (bad host,
	// no key, firewall) before we generate a device-code.
	if err := launchSSHProbe(ctx, opts.SSHTarget); err != nil {
		return fmt.Errorf("ssh probe %s failed: %w", opts.SSHTarget, err)
	}
	fmt.Printf("SSH reachable: %s\n", opts.SSHTarget)

	dc, err := requestLaunchDeviceCode(opts.SourceConfig)
	if err != nil {
		return fmt.Errorf("request device-code: %w", err)
	}
	if err := authorizeOwnDeviceCode(opts.SourceConfig, dc.UserCode); err != nil {
		return fmt.Errorf("authorize device-code: %w", err)
	}

	pending := map[string]any{
		"deviceCode": dc.DeviceCode,
		"userCode":   dc.UserCode,
		"url":        "(authorized via yaver launch ssh)",
		"convexUrl":  dc.ConvexURL,
		"expiresAt":  time.Now().Add(15 * time.Minute).UnixMilli(),
		"createdAt":  time.Now().UnixMilli(),
	}
	pendingJSON, err := json.Marshal(pending)
	if err != nil {
		return err
	}

	// One remote script does everything: ensure node + npm, install
	// yaver-cli, drop pending-auth, start serve in tmux. Idempotent —
	// re-running adopts again without breaking anything.
	remoteScript := fmt.Sprintf(`set -euo pipefail
SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo"; fi

echo "[yaver-launch-ssh] ensuring node + npm + tmux"
if ! command -v node >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  $SUDO apt-get update -qq
  curl -fsSL https://deb.nodesource.com/setup_22.x | $SUDO bash -
  $SUDO apt-get install -y -qq nodejs tmux curl
elif ! command -v tmux >/dev/null 2>&1; then
  $SUDO apt-get install -y -qq tmux
fi

if ! command -v yaver >/dev/null 2>&1; then
  echo "[yaver-launch-ssh] installing yaver-cli via npm"
  $SUDO npm install -g yaver-cli
fi

mkdir -p "$HOME/.yaver"
cat > "$HOME/.yaver/pending-auth.json" <<'PENDING_AUTH_EOF'
%s
PENDING_AUTH_EOF
chmod 0600 "$HOME/.yaver/pending-auth.json"

# Kill any prior yaver tmux session so re-running is idempotent.
tmux kill-session -t yaver 2>/dev/null || true

# Start yaver in a detached tmux session. The auth --background-wait
# step consumes the pending-auth file (already authorized by the
# launching device); yaver serve takes over once the token is written.
tmux new-session -d -s yaver \
  "yaver auth --headless --background-wait --convex-url '%s' && exec yaver serve --port 18080 --debug"
echo "[yaver-launch-ssh] yaver running in tmux session 'yaver' — attach with: tmux attach -t yaver"
`,
		string(pendingJSON),
		dc.ConvexURL,
	)

	fmt.Println("Installing yaver + dropping pre-authorized credentials on the box...")
	if out, err := launchSSHRun(ctx, opts.SSHTarget, remoteScript); err != nil {
		return fmt.Errorf("ssh bootstrap failed: %w\n%s", err, out)
	}
	fmt.Println("  Install complete.")

	fmt.Println("Waiting for the box to consume device-code...")
	if _, err := pollDeviceForOnline(ctx, dc); err != nil {
		return fmt.Errorf("box never came online: %w (debug: ssh %s 'tmux attach -t yaver')", err, opts.SSHTarget)
	}
	fmt.Println("  Box is online and authenticated as your user.")

	if !opts.NoMirror {
		host := launchSSHHostOnly(opts.SSHTarget)
		boxBase := fmt.Sprintf("http://%s:18080", host)
		fmt.Println("Mirroring runner credentials to the box:")
		mirrorRunnersToBox(ctx, opts, boxBase)
	}

	fmt.Println()
	fmt.Println("✓ Done.")
	fmt.Printf("  Target:   %s\n", opts.SSHTarget)
	fmt.Printf("  Attach:   ssh %s -t 'tmux attach -t yaver'\n", opts.SSHTarget)
	fmt.Printf("  Status:   yaver devices  (the box appears as a fresh device row)\n")
	return nil
}

// launchSSHProbe verifies the target accepts an SSH connection without
// blocking on keyboard-interactive prompts. Reuses the user's existing
// ~/.ssh/config (we don't override identity, port, etc.).
func launchSSHProbe(ctx context.Context, target string) error {
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"-o", "StrictHostKeyChecking=accept-new",
		target, "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// launchSSHRun feeds the script to a bash login shell on the target via stdin
// so we don't need an SCP step + a separate execute step.
func launchSSHRun(ctx context.Context, target, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		target, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// launchSSHHostOnly strips the user@ prefix so we can build http://host:18080
// for the mirror push. ssh-config aliases work too — they resolve to
// the same hostname at runtime when we mirror via TCP.
func launchSSHHostOnly(target string) string {
	if i := strings.Index(target, "@"); i >= 0 {
		return target[i+1:]
	}
	return target
}
