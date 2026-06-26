package main

// publish_ship.go — the normie-facing façade over /deploy/ship.
//
// Decision (2026-05-19): the unified publish engine already exists, wired,
// as /deploy/ship (deploy_run.go) — remote --machine targeting, composite
// multi-target, vault-env injection, SSE, DeployRun history, the guest
// security envelope. publish.go's .yaver/publish.yaml system is a second,
// overlapping path. Rather than build a third engine, this is a thin
// friendly shim: it maps store-name words a non-developer would type
// onto the existing ship targets and delegates to shipToAgent().
//
//	yaver publish ios                       → ship --target testflight
//	yaver publish android                   → ship --target playstore
//	yaver publish both                      → ship --targets testflight,playstore
//	yaver publish both --machine <mac-node> → run it on a remote Mac you own
//
// "Make it a single thing": one verb, one or both stores, one command,
// runs on this Mac or a Mac-farm node. Everything underneath is the
// already-tested ship path — no logic is reimplemented here.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// publishStoreAliases maps the words a non-developer is likely to type to
// the canonical /deploy/ship target IDs. Keep this generous — the whole
// point of the façade is that the user does not need to know the word
// "testflight" or "playstore".
var publishStoreAliases = map[string][]string{
	// iOS / App Store
	"ios":        {"testflight"},
	"iphone":     {"testflight"},
	"apple":      {"testflight"},
	"appstore":   {"testflight"},
	"app-store":  {"testflight"},
	"testflight": {"testflight"},
	// Android / Play
	"android":    {"playstore"},
	"google":     {"playstore"},
	"play":       {"playstore"},
	"googleplay": {"playstore"},
	"playstore":  {"playstore"},
	"play-store": {"playstore"},
	// Both
	"both": {"testflight", "playstore"},
	"all":  {"testflight", "playstore"},
	// TV surfaces. These are handled by the platform deploy spine,
	// not /deploy/ship, unless queued to a farm node.
	"tv":         {"android-tv", "tvos"},
	"television": {"android-tv", "tvos"},
	"android-tv": {"android-tv"},
	"androidtv":  {"android-tv"},
	"google-tv":  {"android-tv"},
	"googletv":   {"android-tv"},
	"leanback":   {"android-tv"},
	"tvos":       {"tvos"},
	"apple-tv":   {"tvos"},
	"appletv":    {"tvos"},
	// Watch surfaces.
	"watch":         {"wear-os"},
	"wear":          {"wear-os"},
	"wear-os":       {"wear-os"},
	"wearos":        {"wear-os"},
	"android-wear":  {"wear-os"},
	"android-watch": {"wear-os"},
}

// isPublishStoreWord reports whether arg looks like a store selector (so
// `yaver publish` can tell the façade form apart from the existing
// init/config/run/list/status subcommands without breaking them).
func isPublishStoreWord(arg string) bool {
	_, ok := publishStoreAliases[strings.ToLower(strings.TrimSpace(arg))]
	return ok
}

// runPublishStoreFacade handles `yaver publish <store> [flags]`. It
// resolves normie-friendly defaults (stack = react-native-expo, app =
// project dir name, path = cwd) and hands off to shipToAgent — the exact
// same code path as `yaver deploy ship`, including --machine remote
// targeting at a Mac-farm node.
func runPublishStoreFacade(args []string) {
	store := strings.ToLower(strings.TrimSpace(args[0]))
	targets, ok := publishStoreAliases[store]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown store %q. Use: ios | android | both\n", args[0])
		os.Exit(1)
	}

	fs := flag.NewFlagSet("publish "+store, flag.ExitOnError)
	app := fs.String("app", "", "App/project name for vault scope (default: project directory name)")
	machine := fs.String("machine", "", "Publish on a remote Mac you own, by deviceId (default: this machine)")
	path := fs.String("path", "", "Project path (default: current directory)")
	stack := fs.String("stack", "react-native-expo", "Project stack")
	timeout := fs.Int("timeout", 0, "Timeout in seconds (0 = server default)")
	queue := fs.Bool("queue", false, "Enqueue on the Mac-farm node and return immediately (tap-and-walk-away). Requires --machine.")
	watch := fs.Bool("watch", false, "With --queue: poll until the job finishes instead of returning the job id.")
	fs.Parse(args[1:])

	// Resolve path → absolute, default to cwd.
	resolvedPath := strings.TrimSpace(*path)
	if resolvedPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			resolvedPath = cwd
		}
	}
	if abs, err := filepath.Abs(resolvedPath); err == nil {
		resolvedPath = abs
	}

	// App name: explicit flag wins; else the project directory's name.
	// shipToAgent uses this only for vault scope + the run label — the
	// server still resolves stack/path from what we pass.
	resolvedApp := strings.TrimSpace(*app)
	if resolvedApp == "" {
		resolvedApp = filepath.Base(resolvedPath)
	}
	if resolvedApp == "" || resolvedApp == "." || resolvedApp == string(filepath.Separator) {
		fmt.Fprintln(os.Stderr, "Could not infer an app name. Pass --app <name>.")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run 'yaver auth' first.")
		os.Exit(1)
	}

	// Async path: enqueue on the Mac-farm node and return. The build
	// runs there on its own time (its heartbeat loop claims the job);
	// the caller can close the laptop. This is the "tap Publish, walk
	// away" loop — the same Convex queue the mobile app will use.
	if *queue {
		deviceID := strings.TrimSpace(*machine)
		if deviceID == "" {
			fmt.Fprintln(os.Stderr, "--queue needs --machine <deviceId> (which Mac-farm node runs it). "+
				"Without a remote node, just run it directly (drop --queue).")
			os.Exit(1)
		}
		// iOS archive needs a Mac — fail before enqueuing to a non-Mac node.
		if targetsContain(targets, "testflight") {
			if err := preflightTestFlightMachine(deviceID); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(2)
			}
		}
		os.Exit(enqueuePublishJobCLI(deviceID, resolvedApp, *stack, targets, *watch))
	}

	where := "this machine"
	if m := strings.TrimSpace(*machine); m != "" {
		where = "remote device " + m
	}
	fmt.Fprintf(os.Stderr, "→ Publishing %s to %s on %s …\n",
		resolvedApp, strings.Join(targets, " + "), where)

	// Sync remote iOS: still needs a Mac on the other end.
	if targetsContain(targets, "testflight") && strings.TrimSpace(*machine) != "" {
		if err := preflightTestFlightMachine(strings.TrimSpace(*machine)); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(2)
		}
	}

	if hasPlatformPublishTarget(targets) {
		if strings.TrimSpace(*machine) != "" {
			fmt.Fprintln(os.Stderr, "Platform targets on a remote machine use --queue --machine <deviceId> so the farm node can run mobile_platform_deploy.")
			os.Exit(2)
		}
		for _, target := range targets {
			out := mcpMobilePlatformDeploy(resolvedPath, target, true, false, *timeout)
			if ok, _ := out["ok"].(bool); !ok {
				fmt.Fprintf(os.Stderr, "platform publish %s failed: %v\n", target, out["error"])
				os.Exit(1)
			}
		}
		os.Exit(0)
	}

	// Delegate to the existing, tested ship path. Single store → one
	// target (simple path); both → composite server-side fan-out. Either
	// way this is /deploy/ship, not a reimplementation.
	exit := shipToAgent(cfg, resolvedApp, targets, *stack, resolvedPath, *timeout, strings.TrimSpace(*machine))
	os.Exit(exit)
}

// machineLooksMac reports whether a MachineInfo can archive+upload iOS.
func machineLooksMac(m MachineInfo) bool {
	if m.Capabilities != nil && m.Capabilities.SupportsTestFlight {
		return true
	}
	h := strings.ToLower(m.Platform + " " + m.OS)
	return strings.Contains(h, "darwin") || strings.Contains(h, "mac")
}

// preflightTestFlightMachine refuses early when a normie queues an iOS
// publish to a device that can't archive+upload (anything but a Mac with
// Xcode). It's advisory: an unknown device is allowed through (we don't
// block on incomplete inventory), but a clearly-Linux box is rejected with
// the list of capable Macs so the fix is one copy-paste away.
func preflightTestFlightMachine(deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	machines := listAllMachines(context.Background())
	var capable []string
	var target *MachineInfo
	for i := range machines {
		m := machines[i]
		if machineLooksMac(m) {
			capable = append(capable, fmt.Sprintf("%s (%s)", m.DeviceID, m.Name))
		}
		if m.DeviceID == deviceID {
			mm := m
			target = &mm
		}
	}
	if target == nil {
		return nil // unknown device — don't block on incomplete inventory
	}
	if machineLooksMac(*target) {
		return nil
	}
	hint := "none registered — add a Mac (yaver auth on a macOS box) and try again"
	if len(capable) > 0 {
		hint = strings.Join(capable, ", ")
	}
	return fmt.Errorf("device %s (%s, %s) can't build for TestFlight — iOS archive+upload needs macOS + Xcode.\nTestFlight-capable Macs: %s",
		deviceID, target.Name, target.Platform, hint)
}

func targetsContain(targets []string, want string) bool {
	for _, t := range targets {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}

func hasPlatformPublishTarget(targets []string) bool {
	for _, target := range targets {
		if platformJobTargets[normalizePublishJobTarget(target)] {
			return true
		}
	}
	return false
}

// publishConvexBase returns (token, convexSiteURL) for talking to the
// /publish-jobs/* httpActions, mirroring managedLoadAuth's resolution
// (config first, defaultConvexSiteURL fallback).
func publishConvexBase() (string, string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	return cfg.AuthToken, strings.TrimRight(convex, "/"), nil
}

// enqueuePublishJobCLI POSTs to /publish-jobs/queue and, with watch,
// polls /publish-jobs/list until the job reaches a terminal state.
// Returns the process exit code.
func enqueuePublishJobCLI(deviceID, app, stack string, targets []string, watch bool) int {
	token, convex, err := publishConvexBase()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"deviceId":      deviceID,
		"app":           app,
		"stack":         stack,
		"targets":       targets,
		"sourceSurface": "cli",
	})
	req, _ := http.NewRequest("POST", convex+"/publish-jobs/queue", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "queue request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "queue failed: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(raw)))
		return 1
	}
	var q struct {
		OK      bool   `json:"ok"`
		JobID   string `json:"jobId"`
		Deduped bool   `json:"deduped"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(raw, &q)
	if !q.OK || q.JobID == "" {
		fmt.Fprintf(os.Stderr, "queue rejected: %s\n", strings.TrimSpace(string(raw)))
		return 1
	}
	dedupNote := ""
	if q.Deduped {
		dedupNote = " (already in flight — joined existing job)"
	}
	fmt.Printf("Queued %s → %s on %s%s\n  job: %s\n",
		app, strings.Join(targets, "+"), deviceID, dedupNote, q.JobID)
	if !watch {
		fmt.Println("  It runs on the farm node — you can close this. " +
			"Check later:\n    yaver publish " + targets[0] + " --queue --machine " + deviceID + " --watch")
		return 0
	}

	fmt.Println("  Watching (Ctrl-C to stop — the build keeps running on the node)…")
	lastStatus := ""
	for {
		time.Sleep(10 * time.Second)
		st, msg, done, ok := pollPublishJob(convex, token, deviceID, q.JobID)
		if st != "" && st != lastStatus {
			line := "  • " + st
			if msg != "" {
				line += " — " + msg
			}
			fmt.Println(line)
			lastStatus = st
		}
		if done {
			if ok {
				fmt.Println("  ✓ publish complete")
				return 0
			}
			fmt.Println("  ✗ publish failed (see node logs / `yaver deploy runs`)")
			return 1
		}
	}
}

// pollPublishJob fetches the job's current state from
// /publish-jobs/list. Returns (status, message, terminal, ok).
func pollPublishJob(convex, token, deviceID, jobID string) (string, string, bool, bool) {
	req, _ := http.NewRequest("GET", convex+"/publish-jobs/list?deviceId="+deviceID+"&limit=50", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", "", false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", false, false
	}
	var out struct {
		Jobs []struct {
			JobID   string `json:"jobId"`
			Status  string `json:"status"`
			Message string `json:"message"`
			Result  []struct {
				OK bool `json:"ok"`
			} `json:"result"`
		} `json:"jobs"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return "", "", false, false
	}
	for _, j := range out.Jobs {
		if j.JobID != jobID {
			continue
		}
		switch j.Status {
		case "done":
			allOK := len(j.Result) > 0
			for _, r := range j.Result {
				if !r.OK {
					allOK = false
				}
			}
			return j.Status, j.Message, true, allOK
		case "failed", "expired":
			return j.Status, j.Message, true, false
		default:
			return j.Status, j.Message, false, false
		}
	}
	return "", "", false, false
}
