package main

// shots_asc.go — App Store Connect backend for `yaver shots`. Thin Go
// wrappers that shell out to the three parameterized python scripts
// (upload screenshots, set metadata, submit for review). The scripts are
// embedded in the binary and materialized to ~/.yaver/shots-scripts on
// first use, so a shipped agent has no repo dependency.
//
// Canonical (human-runnable) copies live at scripts/screenshots/*.py and
// scripts/set-appstore-info.py; the shots_scripts/ copies here are the
// embedded mirror. Keep them in sync when changing the ASC flow.

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed shots_scripts/*.py
var shotsScriptsFS embed.FS

// shotsScriptsDir returns a directory holding the three ASC python
// scripts. Resolution: YAVER_SHOTS_SCRIPTS_DIR env (dev override) →
// materialized embedded copies under ~/.yaver/shots-scripts.
func shotsScriptsDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("YAVER_SHOTS_SCRIPTS_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver", "shots-scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := shotsScriptsFS.ReadDir("shots_scripts")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		data, err := shotsScriptsFS.ReadFile("shots_scripts/" + e.Name())
		if err != nil {
			return "", err
		}
		// Always overwrite so a binary upgrade refreshes the scripts.
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// shotsASCEnv builds the env for the python scripts: the current env plus
// the App Store Connect key triple. The triple is read from the process
// env (the deploy/ship vault-env path already exports these for mobile).
func shotsASCEnv() []string {
	return os.Environ()
}

// runShotsPython runs `python3 <scriptsDir>/<script> <args...>` streaming
// output to our stdout/stderr while also capturing it. Returns the exit
// code and the captured combined output (the submit script uses 0 for both
// "submitted" and "staged", 1 for hard failure).
func runShotsPython(script string, args ...string) (int, string, error) {
	dir, err := shotsScriptsDir()
	if err != nil {
		return -1, "", fmt.Errorf("resolve scripts dir: %w", err)
	}
	full := filepath.Join(dir, script)
	cmd := exec.Command("python3", append([]string{full}, args...)...)
	cmd.Env = shotsASCEnv()
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err = cmd.Run()
	if err == nil {
		return 0, buf.String(), nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), buf.String(), nil
	}
	return -1, buf.String(), err
}

// ascUploadScreenshots uploads the PNGs in dir for the given bundle id.
func ascUploadScreenshots(bundleID, dir, locale string) error {
	code, _, err := runShotsPython("upload-appstore.py",
		"--bundle-id", bundleID, "--dir", dir, "--locale", locale)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("upload-appstore.py exited %d", code)
	}
	return nil
}

// ascSetMetadata sets App Store metadata for the bundle. metaSource is an
// optional path to a metadata JSON file or a project dir holding
// .yaver/appstore.json; empty uses the script's built-in defaults.
func ascSetMetadata(bundleID, metaSource string) error {
	args := []string{"--bundle-id", bundleID}
	if metaSource != "" {
		args = append(args, "--meta-json", metaSource)
	}
	code, _, err := runShotsPython("set-appstore-info.py", args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("set-appstore-info.py exited %d", code)
	}
	return nil
}

// ascSubmitForReview attempts the submission. Returns (submitted, err):
// submitted=true means it went to review; submitted=false with err=nil
// means everything is staged and one manual tap remains (the expected
// outcome when Apple gates on compliance/pricing). err!=nil is a hard
// failure (auth, app not found, transport).
func ascSubmitForReview(bundleID, version string) (bool, error) {
	args := []string{"--bundle-id", bundleID}
	if version != "" {
		args = append(args, "--version", version)
	}
	code, out, err := runShotsPython("submit-appstore.py", args...)
	if err != nil {
		return false, err
	}
	if code != 0 {
		return false, fmt.Errorf("submit-appstore.py exited %d", code)
	}
	// Both "submitted" and "staged" exit 0; the script's printed marker
	// tells them apart. STAGED_MANUAL means one manual tap remains.
	submitted := strings.Contains(out, "SUBMITTED") && !strings.Contains(out, "STAGED_MANUAL")
	return submitted, nil
}
