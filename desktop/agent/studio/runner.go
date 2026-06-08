package studio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes a shell command on the HOST that holds the capture surface,
// and moves files onto that host. It is the seam that makes the Studio capture
// layer work identically on:
//
//   - a Yaver-managed-cloud farm box, where the agent runs ON the host →
//     LocalRunner (commands run in-process, files are already local), and
//   - an on-prem box the owner controls, reached over SSH/relay → SSHRunner
//     (commands run via `ssh host …`, files copied via `scp`).
//
// The redroid driver (redroid.go) builds plain shell strings (docker / android
// commands) and never cares which Runner carries them — same code, two homes.
type Runner interface {
	// Exec runs `cmd` through the host shell and returns combined output.
	Exec(ctx context.Context, cmd string) ([]byte, error)
	// PutFile places localPath onto the host at remotePath (e.g. an APK to
	// install). For LocalRunner this is a copy; for SSHRunner an scp.
	PutFile(ctx context.Context, localPath, remotePath string) error
	// GetFile retrieves remotePath from the host to localPath (e.g. a recorded
	// MP4 or screenshot). For LocalRunner a copy; for SSHRunner an scp back.
	GetFile(ctx context.Context, remotePath, localPath string) error
	// Label is a short human name for logs ("local", "ssh kivi@host").
	Label() string
}

// shellQuote single-quotes a value for safe interpolation into a shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// LocalRunner runs commands on this process's host. Use on a managed-cloud farm
// box where the agent itself runs next to Docker.
type LocalRunner struct{}

func (LocalRunner) Label() string { return "local" }

func (LocalRunner) Exec(ctx context.Context, cmd string) ([]byte, error) {
	var buf bytes.Buffer
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	return buf.Bytes(), err
}

func (LocalRunner) PutFile(ctx context.Context, localPath, remotePath string) error {
	return localCopy(localPath, remotePath)
}

func (LocalRunner) GetFile(ctx context.Context, remotePath, localPath string) error {
	return localCopy(remotePath, localPath)
}

func localCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Close()
}

// SSHRunner runs commands on a remote host over OpenSSH. Use for an on-prem box
// (resolved directly or via `yaver ssh` having set up the route). Opts are extra
// ssh flags (e.g. -o ConnectTimeout=8); Host is user@host.
type SSHRunner struct {
	Host string
	Opts []string
}

func (r SSHRunner) Label() string { return "ssh " + r.Host }

func (r SSHRunner) sshArgs(tail ...string) []string {
	args := append([]string{}, r.Opts...)
	args = append(args, r.Host)
	return append(args, tail...)
}

func (r SSHRunner) Exec(ctx context.Context, cmd string) ([]byte, error) {
	var buf bytes.Buffer
	// The remote shell receives cmd as a single argument and runs it.
	c := exec.CommandContext(ctx, "ssh", r.sshArgs(cmd)...)
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	return buf.Bytes(), err
}

func (r SSHRunner) PutFile(ctx context.Context, localPath, remotePath string) error {
	return r.scp(ctx, localPath, fmt.Sprintf("%s:%s", r.Host, remotePath))
}

func (r SSHRunner) GetFile(ctx context.Context, remotePath, localPath string) error {
	return r.scp(ctx, fmt.Sprintf("%s:%s", r.Host, remotePath), localPath)
}

func (r SSHRunner) scp(ctx context.Context, src, dst string) error {
	scpArgs := append([]string{}, r.Opts...)
	scpArgs = append(scpArgs, src, dst)
	var buf bytes.Buffer
	c := exec.CommandContext(ctx, "scp", scpArgs...)
	c.Stdout, c.Stderr = &buf, &buf
	if err := c.Run(); err != nil {
		return fmt.Errorf("scp %s -> %s: %v: %s", src, dst, err, buf.String())
	}
	return nil
}
