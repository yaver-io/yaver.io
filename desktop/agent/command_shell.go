package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type commandShellSpec struct {
	Executable string
	Prefix     []string
}

// executableCommandSpecFor keeps native executables direct but routes Windows
// batch shims through cmd.exe. npm installs coding runners as .cmd files;
// CreateProcessW cannot execute those files itself, so finding the shim without
// this wrapper is a false-green "runner installed" state.
func executableCommandSpecFor(goos, binary string, args []string, lookPath func(string) (string, error), getenv func(string) string) (commandShellSpec, error) {
	if goos != "windows" {
		return commandShellSpec{Executable: binary, Prefix: append([]string(nil), args...)}, nil
	}
	ext := strings.ToLower(filepath.Ext(binary))
	if ext != ".cmd" && ext != ".bat" {
		return commandShellSpec{Executable: binary, Prefix: append([]string(nil), args...)}, nil
	}
	shell, err := commandShellSpecFor(goos, "cmd", lookPath, getenv)
	if err != nil {
		return commandShellSpec{}, err
	}
	line := buildWindowsCommandLine(append([]string{binary}, args...))
	shell.Prefix = append(shell.Prefix, line)
	return shell, nil
}

// commandShellSpecFor resolves the shell contract separately from command
// execution so Windows behavior is testable on every CI host. Native Windows
// prefers modern PowerShell, keeps cmd.exe as the compatibility fallback, and
// supports explicit WSL interop without making WSL a hidden dependency.
func commandShellSpecFor(goos, requested string, lookPath func(string) (string, error), getenv func(string) string) (commandShellSpec, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	requested = strings.TrimSpace(requested)
	if goos != "windows" {
		if requested == "" {
			requested = preferredUnixShell()
		}
		return commandShellSpec{Executable: requested, Prefix: []string{"-c"}}, nil
	}

	resolve := func(candidates ...string) string {
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if filepath.IsAbs(candidate) || strings.ContainsAny(candidate, `/\\`) {
				return candidate
			}
			if path, err := lookPath(candidate); err == nil && path != "" {
				return path
			}
		}
		return ""
	}
	powerShell := func(executable string) commandShellSpec {
		return commandShellSpec{
			Executable: executable,
			Prefix:     []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"},
		}
	}

	if requested == "" {
		if path := resolve("pwsh.exe", "pwsh", "powershell.exe", "powershell"); path != "" {
			return powerShell(path), nil
		}
		if path := resolve(getenv("ComSpec"), "cmd.exe", "cmd"); path != "" {
			return commandShellSpec{Executable: path, Prefix: []string{"/D", "/S", "/C"}}, nil
		}
		return commandShellSpec{}, fmt.Errorf("no Windows command shell found; install PowerShell 7 or restore cmd.exe")
	}

	base := strings.ToLower(strings.TrimSuffix(filepath.Base(requested), filepath.Ext(requested)))
	switch base {
	case "pwsh", "powershell":
		path := resolve(requested)
		if path == "" {
			return commandShellSpec{}, fmt.Errorf("requested PowerShell %q was not found; install it or omit shell to use the available Windows shell", requested)
		}
		return powerShell(path), nil
	case "cmd":
		path := resolve(requested, getenv("ComSpec"))
		if path == "" {
			return commandShellSpec{}, fmt.Errorf("requested cmd.exe was not found")
		}
		return commandShellSpec{Executable: path, Prefix: []string{"/D", "/S", "/C"}}, nil
	case "wsl":
		path := resolve(requested)
		if path == "" {
			return commandShellSpec{}, fmt.Errorf("WSL is not installed or wsl.exe is not on PATH; run `wsl --install`, restart Windows, or choose PowerShell")
		}
		return commandShellSpec{Executable: path, Prefix: []string{"--exec", "bash", "-lc"}}, nil
	case "bash", "sh", "zsh":
		path := resolve(requested)
		if path == "" {
			return commandShellSpec{}, fmt.Errorf("requested shell %q was not found", requested)
		}
		return commandShellSpec{Executable: path, Prefix: []string{"-lc"}}, nil
	default:
		return commandShellSpec{}, fmt.Errorf("unsupported Windows shell %q; use pwsh, powershell, cmd, wsl, bash, sh, or zsh", requested)
	}
}

func newCommandShellContext(ctx context.Context, goos, requested, command string) (*exec.Cmd, error) {
	spec, err := commandShellSpecFor(goos, requested, exec.LookPath, os.Getenv)
	if err != nil {
		return nil, err
	}
	args := append(append([]string(nil), spec.Prefix...), command)
	return exec.CommandContext(ctx, spec.Executable, args...), nil
}

func newExecutableCommandContext(ctx context.Context, goos, binary string, args ...string) (*exec.Cmd, error) {
	spec, err := executableCommandSpecFor(goos, binary, args, exec.LookPath, os.Getenv)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, spec.Executable, spec.Prefix...), nil
}

func newExecutableCommand(goos, binary string, args ...string) (*exec.Cmd, error) {
	return newExecutableCommandContext(context.Background(), goos, binary, args...)
}

// newRuntimeCommandContext combines Yaver's private-runtime lookup with the
// Windows batch-shim wrapper. Keeping this as one constructor prevents the
// common false green where npm.cmd/npx.cmd is discovered successfully and is
// then handed directly to CreateProcessW, which cannot execute batch files.
func newRuntimeCommandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	return newExecutableCommandContext(ctx, runtime.GOOS, resolveSpawnPath(name), args...)
}

func newRuntimeCommand(name string, args ...string) (*exec.Cmd, error) {
	return newRuntimeCommandContext(context.Background(), name, args...)
}

// newRunnerCommandContext is the single spawn boundary for first-class coding
// runners. CheckRunnerReady records the exact executable it exercised; use that
// path here instead of asking CreateProcess/exec.LookPath a second, weaker
// question. This matters on launchd/systemd hosts (the runner can live outside
// the daemon PATH) and on Windows where npm exposes codex/claude/opencode as
// .cmd shims that must be invoked through cmd.exe.
func newRunnerCommandContext(ctx context.Context, command string, args ...string) (*exec.Cmd, error) {
	resolved := strings.TrimSpace(command)
	if path, ok := cachedRunnerBinaryPath(resolved); ok {
		resolved = path
	} else if path := resolveRunnerBinary(resolved); path != "" {
		resolved = path
	}
	return newExecutableCommandContext(ctx, runtime.GOOS, resolved, args...)
}
