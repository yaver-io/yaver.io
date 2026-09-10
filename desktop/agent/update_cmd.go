package main

// `yaver update` — manual self-update from the latest GitHub release.
//
// Wraps the existing checkAutoUpdate machinery (which only runs when
// the user has opted in via cfg.AutoUpdate). Runs unconditionally
// when invoked from the CLI so the dev can pull the latest binary
// without waiting for the next agent boot.
//
// --device <alias|id> routes to a remote agent via the same
// proxyToDeviceJSON path runner-auth-setup uses; the agent on the
// far end runs its existing /agent/update POST handler. This closes
// the cross-device gap the web Update modal hit pre-1.99.222 (web
// refused to update non-connected peers; CLI now sidesteps that for
// scripted / terminal-driven fleet upgrades).

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runManualUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	device := fs.String("device", "", "remote device id or alias to update (default: local box)")
	fs.Parse(args)

	if strings.TrimSpace(*device) != "" {
		runRemoteUpdate(strings.TrimSpace(*device))
		return
	}

	if handled, err := runManualUpdateViaNPM(); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "npm upgrade failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Force the update path regardless of the operator's auto-update
	// setting. forcedAutoUpdateConfig copies, so nothing is persisted:
	// this run only.
	loaded, _ := LoadConfig()
	cfg := forcedAutoUpdateConfig(loaded)

	fmt.Println("yaver update")
	fmt.Println("============")
	fmt.Println()

	checkAutoUpdate(cfg)

	fmt.Println()
	fmt.Println("Tip: enable auto-updates so the agent does this on every boot:")
	fmt.Println("  yaver auto-update enable")

	// `checkAutoUpdate` returns silently when there's nothing to do;
	// the user sees the [auto-update] log lines either way.
	os.Exit(0)
}

// runRemoteUpdate triggers /agent/update on a remote peer. proxy-
// ToDeviceJSON resolves the deviceHint (alias OR raw id) via the
// same Convex device list the runner-auth-setup --target path uses,
// so `yaver update --device simkab-vostro-3888` Just Works against
// any owned box. Output mirrors the local update header so terminal
// users see the same banner whether they're upgrading themselves or
// a peer.
func runRemoteUpdate(deviceHint string) {
	fmt.Printf("yaver update --device %s\n", deviceHint)
	fmt.Println(strings.Repeat("=", 24+len(deviceHint)))
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	out, err := proxyToDeviceJSON(ctx, "agent-update", deviceHint, http.MethodPost, "/agent/update", map[string]any{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote update: %v\n", err)
		os.Exit(1)
	}

	if msg, _ := out["message"].(string); msg != "" {
		fmt.Println(msg)
	}
	if cur, _ := out["currentVersion"].(string); cur != "" {
		if lv, _ := out["latestVersion"].(string); lv != "" {
			fmt.Printf("currentVersion=%s latestVersion=%s\n", cur, lv)
		}
	}
	if started, ok := out["started"].(bool); ok && !started {
		// Agent thinks it's already on latest. checkAutoUpdate-style
		// silent no-op would be confusing through SSH — print why.
		fmt.Println("No update needed — agent reports it is already on the latest version.")
		os.Exit(0)
	}
	fmt.Println()
	fmt.Printf("Update started on %s. Watch progress with `yaver primary status` or the web Devices view.\n", deviceHint)
	os.Exit(0)
}

func runManualUpdateViaNPM() (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_INSTALL_SOURCE")), "npm") {
		return false, nil
	}

	pkg := strings.TrimSpace(os.Getenv("YAVER_NPM_PACKAGE"))
	if pkg == "" {
		pkg = "yaver-cli"
	}
	npm, err := osexec.LookPath("npm")
	if err != nil {
		return true, fmt.Errorf("npm not found in PATH")
	}

	fmt.Println("yaver update")
	fmt.Println("============")
	fmt.Println()
	fmt.Printf("Detected npm-managed install. Running: npm install -g %s@latest\n", pkg)
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := runNPMUpgrade(ctx, npm, pkg); err != nil {
		return true, err
	}

	fmt.Println()
	fmt.Println("npm upgrade complete.")
	fmt.Println("Tip: `yaver --version` prints the active agent version after the wrapper refreshes.")
	return true, nil
}

// runNPMUpgrade repairs the one safe, deterministic npm failure we have seen
// in production: an interrupted global upgrade can leave npm's hidden rename
// destination (for example .yaver-cli-AbCd1234) behind. The next upgrade then
// fails ENOTEMPTY before postinstall can run. We only remove the exact `dest`
// npm reported, and only when it is a direct child of this npm's global root
// with the expected package-specific staging prefix; every other failure is
// returned untouched.
func runNPMUpgrade(ctx context.Context, npm, pkg string) error {
	run := func() (string, error) {
		var stderr bytes.Buffer
		cmd := osexec.CommandContext(ctx, npm, "install", "-g", pkg+"@latest")
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
		cmd.Stdin = os.Stdin
		cmd.Env = os.Environ()
		return stderr.String(), cmd.Run()
	}

	stderr, err := run()
	if err == nil {
		return nil
	}
	rootOut, rootErr := osexec.CommandContext(ctx, npm, "root", "-g").Output()
	if rootErr != nil {
		return err
	}
	stale := npmStaleRenameDestination(stderr, strings.TrimSpace(string(rootOut)), pkg)
	if stale == "" {
		return err
	}
	if info, statErr := os.Lstat(stale); statErr != nil || !info.IsDir() {
		return err
	}

	fmt.Fprintf(os.Stderr, "[yaver] Removing stale npm upgrade staging directory %s and retrying once.\n", filepath.Base(stale))
	if removeErr := os.RemoveAll(stale); removeErr != nil {
		return fmt.Errorf("remove stale npm staging directory: %w (original upgrade error: %v)", removeErr, err)
	}
	_, retryErr := run()
	return retryErr
}

func npmStaleRenameDestination(stderr, globalRoot, pkg string) string {
	if !strings.Contains(stderr, "ENOTEMPTY") || !strings.Contains(stderr, "rename") {
		return ""
	}
	pkgBase := pkg
	if slash := strings.LastIndex(pkgBase, "/"); slash >= 0 {
		pkgBase = pkgBase[slash+1:]
	}
	wantPrefix := "." + pkgBase + "-"
	cleanRoot, err := filepath.Abs(globalRoot)
	if err != nil || strings.TrimSpace(globalRoot) == "" {
		return ""
	}
	for _, line := range strings.Split(stderr, "\n") {
		idx := strings.Index(line, " dest ")
		if idx < 0 {
			continue
		}
		candidate := strings.Trim(strings.TrimSpace(line[idx+len(" dest "):]), "'\"")
		candidate, err = filepath.Abs(candidate)
		if err == nil && filepath.Dir(candidate) == cleanRoot && strings.HasPrefix(filepath.Base(candidate), wantPrefix) {
			return candidate
		}
	}
	return ""
}
