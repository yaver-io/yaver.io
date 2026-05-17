package main

// wipe_cmd.go — `yaver wipe`. Selective scrub of local Yaver state.
//
// `yaver purge` (main.go::runPurge) already nukes ~/.yaver wholesale
// — including the auth token — and forces the user back through the
// OAuth flow. That's the right tool for "handing this Mac to someone
// else" but way too aggressive for day-to-day housekeeping.
//
// `yaver wipe` gives finer knobs:
//
//   yaver wipe vault      — forget every secret
//   yaver wipe apikeys    — drop the SDK-token registry (Convex
//                           rows stay until `yaver apikey disable`)
//   yaver wipe tasks      — drop task history + outputs
//   yaver wipe blobs      — drop the local blob store
//   yaver wipe sessions   — drop transferred AI sessions
//   yaver wipe caches     — drop dev-server + build caches
//   yaver wipe all        — everything except config.json (auth
//                           token stays — you're still signed in)
//   yaver wipe --including-auth   — equivalent to `yaver purge`
//
// Always prompts for confirmation unless --yes is passed. Never
// touches directories Yaver didn't create (we gate on a fixed
// allowlist below).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// wipeTargets maps the CLI name → relative paths under ~/.yaver to
// remove. A slash suffix means the whole subtree; no suffix means
// the single file. Every entry must be inside configDirName.
var wipeTargets = map[string][]string{
	"vault":    {"vault.enc", "vault.enc.bak"},
	"apikeys":  {"apikeys/"},
	"tasks":    {"tasks/", "agent-graphs.json"},
	"blobs":    {"blobs/"},
	"sessions": {"sessions/"},
	"caches": {
		"blackbox/",
		"builds/",
		"clips/",
		"errors/",
		"feedback/",
		"forked-pids.txt",
		"graphs/",
		"jobs/",
		"agent.log",
	},
}

func runWipe(args []string) {
	// Parse flags and positional list first so `yaver wipe all` works.
	yes := false
	includingAuth := false
	var kinds []string
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--including-auth":
			includingAuth = true
		case "--help", "-h":
			printWipeUsage()
			return
		case "help":
			printWipeUsage()
			return
		default:
			kinds = append(kinds, a)
		}
	}
	if len(kinds) == 0 && !includingAuth {
		printWipeUsage()
		os.Exit(1)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wipe: cannot resolve home dir: %v\n", err)
		os.Exit(1)
	}
	base := filepath.Join(home, configDirName)
	if _, err := os.Stat(base); os.IsNotExist(err) {
		fmt.Println("Nothing to wipe — ~/.yaver does not exist.")
		return
	}

	// Resolve the list of files to remove. `all` is a macro for every
	// known target; we deliberately keep it narrower than
	// `yaver purge` (config.json stays unless --including-auth).
	var victims []string
	seen := map[string]bool{}
	add := func(rels []string) {
		for _, rel := range rels {
			abs := filepath.Join(base, rel)
			if seen[abs] {
				continue
			}
			seen[abs] = true
			victims = append(victims, abs)
		}
	}
	for _, k := range kinds {
		if k == "all" {
			for _, rels := range wipeTargets {
				add(rels)
			}
			continue
		}
		rels, ok := wipeTargets[k]
		if !ok {
			fmt.Fprintf(os.Stderr, "wipe: unknown target %q\n", k)
			printWipeUsage()
			os.Exit(1)
		}
		add(rels)
	}
	if includingAuth {
		// Wipe everything in ~/.yaver that isn't special-cased below.
		// Gate at the top-level directory boundary: never follow
		// symlinks out, never touch anything outside base.
		add([]string{"config.json", "config.json.bak", "device.key", "cert.pem"})
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			add([]string{e.Name()})
		}
	}

	// Filter down to the ones that actually exist on disk so the
	// confirmation prompt is honest and empty runs don't prompt.
	existing := victims[:0]
	for _, v := range victims {
		if _, err := os.Stat(v); err == nil || !os.IsNotExist(err) {
			existing = append(existing, v)
		}
	}
	if len(existing) == 0 {
		fmt.Println("Nothing to wipe — targets don't exist on disk.")
		return
	}
	sort.Strings(existing)

	fmt.Println("Will remove:")
	for _, v := range existing {
		rel, _ := filepath.Rel(home, v)
		fmt.Printf("  ~/%s\n", rel)
	}
	fmt.Println()
	if includingAuth {
		fmt.Println("  --including-auth: you will be signed out. Run `yaver auth` to sign back in.")
	} else {
		fmt.Println("  Your sign-in (~/.yaver/config.json) stays intact.")
	}
	fmt.Println()

	if !yes {
		fmt.Print("Proceed? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Aborted.")
			return
		}
	}

	// Tell Convex about the wipe BEFORE touching disk so mobile / web
	// see the correct status immediately instead of waiting for the
	// 90 s heartbeat-staleness window. Best-effort — never aborts the
	// local wipe on network failure (heartbeat freshness is the
	// safety net).
	notifyConvexOfWipe(includingAuth)

	// Defensive: refuse to ever delete anything outside ~/.yaver.
	for _, v := range existing {
		if !strings.HasPrefix(v+string(os.PathSeparator), base+string(os.PathSeparator)) &&
			v != base {
			fmt.Fprintf(os.Stderr, "wipe: refusing to touch path outside ~/.yaver: %s\n", v)
			os.Exit(1)
		}
		if err := os.RemoveAll(v); err != nil {
			fmt.Fprintf(os.Stderr, "wipe: %s: %v\n", v, err)
			continue
		}
	}

	fmt.Printf("Wiped %d path(s).\n", len(existing))
	if includingAuth {
		fmt.Println("Run `yaver auth` to sign in again.")
	}
}

// notifyConvexOfWipe pings the backend so mobile / web stop showing
// this device as live the moment a `yaver wipe` runs. Two modes:
//
//   - includingAuth=false → MarkOffline. The device record stays
//     (so a re-`yaver serve` brings it back without re-pairing); we
//     only flip the isOnline flag.
//   - includingAuth=true → RemoveDeviceShutdown. The device record
//     is deleted entirely, so the user sees it disappear from their
//     device list. They'll need to re-auth + re-register to come back.
//
// Also stops any running yaver agent first so its in-memory
// heartbeat loop doesn't immediately re-register the device after
// we just told Convex it's gone.
//
// Best-effort everywhere: failures are logged, never fatal. Mobile
// / web still see correct state via the 90 s heartbeat-freshness
// gate even if every call here drops.
func notifyConvexOfWipe(includingAuth bool) {
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.AuthToken == "" || cfg.DeviceID == "" || cfg.ConvexSiteURL == "" {
		return
	}

	// Stop any running agent first. Otherwise its 30 s heartbeat
	// loop will re-create the device record we just deleted, or flip
	// isOnline back to true a few seconds after we marked it offline.
	if pid, running := isAgentRunning(); running {
		if proc, err := os.FindProcess(pid); err == nil {
			terminateProcess(proc)
			// Give it a moment to flush its own MarkOffline before
			// we call ours — best-effort, non-blocking.
			time.Sleep(500 * time.Millisecond)
		}
	}

	if includingAuth {
		if err := RemoveDeviceShutdown(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); err != nil {
			fmt.Fprintf(os.Stderr, "wipe: device removal notification failed (mobile/web will catch up via heartbeat staleness): %v\n", err)
		}
		return
	}
	if err := MarkOffline(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); err != nil {
		fmt.Fprintf(os.Stderr, "wipe: offline notification failed (mobile/web will catch up via heartbeat staleness): %v\n", err)
	}
}

func printWipeUsage() {
	keys := make([]string, 0, len(wipeTargets))
	for k := range wipeTargets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println(`yaver wipe — selective scrub of local Yaver state.

Usage:
  yaver wipe <target> [<target> …] [--yes] [--including-auth]

Targets:`)
	for _, k := range keys {
		paths := strings.Join(wipeTargets[k], ", ")
		fmt.Printf("  %-9s  ~/.yaver/%s\n", k, paths)
	}
	fmt.Println(`  all        every target above (auth stays)

Flags:
  --yes                  skip the confirmation prompt
  --including-auth       also drop config.json / device.key (same as
                         yaver purge — you'll be signed out)

Examples:
  yaver wipe vault
  yaver wipe apikeys tasks --yes
  yaver wipe all
  yaver wipe --including-auth --yes`)
}
