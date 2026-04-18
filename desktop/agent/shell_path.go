package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const yaverNodePathMarker = "# yaver-node-path"

// ensureUserShellPathSetup makes the user-facing shells aware of the
// sudo-free Node/npm locations Yaver manages on fresh Linux/WSL/macOS
// boxes. We write the same export to the common startup files so zsh
// on WSL does not get left behind when the machine defaults away from
// bash.
func ensureUserShellPathSetup(progress func(string)) error {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	exportLine := `export PATH="$HOME/.local/node-current/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH"`
	targets := []string{
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
	}

	for _, target := range targets {
		if err := ensureMarkedLine(target, yaverNodePathMarker, exportLine); err != nil {
			return err
		}
		if progress != nil {
			progress("Updated shell PATH in " + target)
		}
	}

	return nil
}

func ensureMarkedExportLine(path, exportLine string) error {
	return ensureMarkedLine(path, yaverNodePathMarker, exportLine)
}

func ensureMarkedLine(path, marker, value string) error {
	var text string
	if data, err := os.ReadFile(path); err == nil {
		text = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	lines := strings.Split(text, "\n")
	var out []string
	skipNext := false
	found := false

	for i := 0; i < len(lines); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		currentLine := lines[i]
		if strings.TrimSpace(currentLine) == marker {
			found = true
			out = append(out, marker, value)
			if i+1 < len(lines) {
				skipNext = true
			}
			continue
		}
		out = append(out, currentLine)
	}

	if !found {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, marker, value)
	}

	contents := strings.Join(out, "\n")
	if !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}

func removeMarkedLine(path, marker string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	skipNext := false
	for i := 0; i < len(lines); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.TrimSpace(lines[i]) == marker {
			if i+1 < len(lines) {
				skipNext = true
			}
			continue
		}
		out = append(out, lines[i])
	}

	contents := strings.Join(out, "\n")
	if !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}

// configureNpmUserPrefix points global npm installs at ~/.local so the
// resulting bin dir is already on PATH on most developer machines. The
// extra ~/.npm-global entry stays in PATH for backwards compatibility.
func configureNpmUserPrefix(nodeBinDir string, progress func(string)) error {
	if strings.TrimSpace(nodeBinDir) == "" {
		return nil
	}
	npmBin := filepath.Join(nodeBinDir, "npm")
	if _, err := os.Stat(npmBin); err != nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return fmt.Errorf("resolve home dir for npm prefix: %w", err)
	}
	targetPrefix := filepath.Join(home, ".local")
	if err := os.MkdirAll(filepath.Join(targetPrefix, "bin"), 0o755); err != nil {
		return fmt.Errorf("create npm prefix bin dir: %w", err)
	}

	cmd := exec.Command(npmBin, "config", "set", "prefix", targetPrefix)
	cmd.Env = augmentEnv(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("configure npm prefix: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if progress != nil {
		progress("Configured npm global prefix: " + targetPrefix)
	}
	return nil
}
