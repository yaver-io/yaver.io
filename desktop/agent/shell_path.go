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
		if err := ensureMarkedExportLine(target, exportLine); err != nil {
			return err
		}
		if progress != nil {
			progress("Updated shell PATH in " + target)
		}
	}

	return nil
}

func ensureMarkedExportLine(path, exportLine string) error {
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
		line := lines[i]
		if strings.TrimSpace(line) == yaverNodePathMarker {
			found = true
			out = append(out, yaverNodePathMarker, exportLine)
			if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "export PATH=") {
				skipNext = true
			}
			continue
		}
		out = append(out, line)
	}

	if !found {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, yaverNodePathMarker, exportLine)
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
