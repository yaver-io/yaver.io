package main

// primary_cmd.go — `yaver primary` CLI subcommand.
//
// Convex stores a per-user `userSettings.primaryDeviceId` that mobile,
// web, and (eventually) the desktop app use as the auto-connect target
// when the user has more than one machine registered. This CLI gives
// the user a terminal-side knob for the same preference so they can
// script it or set it without opening the phone.
//
// Shape:
//
//   yaver primary               # print current primary + device list
//   yaver primary show          # alias for bare invocation
//   yaver primary set <devId>   # mark a device primary (partial match OK)
//   yaver primary clear         # unset the preference
//
// All commands read ~/.yaver/config.json for the auth token. Partial
// match on `set` accepts any unique prefix of deviceId OR the exact
// device name — same ergonomics as `yaver guests remove <email>`.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

func runPrimary(args []string) {
	ctx := context.Background()
	if len(args) == 0 {
		runPrimaryShow(ctx)
		return
	}
	// Reserved verbs come first so a future runner whose name collides
	// with a verb (e.g. "auth") never silently re-routes to the runner
	// quick flow.
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		runPrimaryStatus(ctx, primaryHasFlag(args[1:], "--json"))
		return
	case "auth":
		runPrimaryAuth(ctx, args[1:])
		return
	case "ping":
		runPrimaryPing(ctx, args[1:])
		return
	case "projects":
		runPrimaryProjects(ctx, args[1:], false)
		return
	case "mobiles":
		runPrimaryProjects(ctx, args[1:], true)
		return
	case "signout", "logout":
		runPrimarySignout(ctx, args[1:])
		return
	case "stop":
		runPrimaryStop(ctx, args[1:])
		return
	}
	if runner := normalizePrimaryRunnerQuickArg(args[0]); runner != "" {
		runPrimaryRunnerQuickFlow(ctx, runner, args[1:])
		return
	}
	switch args[0] {
	case "show", "get", "list", "ls":
		runPrimaryShow(ctx)
	case "set":
		runPrimarySet(ctx, args[1:])
	case "pick", "choose", "select":
		runPrimaryPick(ctx)
	case "clear", "unset", "remove":
		runPrimaryClear(ctx)
	case "help", "-h", "--help":
		primaryUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver primary %s\n\n", args[0])
		primaryUsage()
		os.Exit(1)
	}
}

func primaryHasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// runPrimaryAuth implements `yaver primary auth` (Yaver-level headless
// auth on the primary device) and `yaver primary auth <runner>` (the
// runner-auth setup flow on the primary device — same path as
// `yaver primary <runner>`, just spelled out).
func runPrimaryAuth(ctx context.Context, args []string) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read userSettings: %v\n", err)
		os.Exit(1)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		fmt.Fprintln(os.Stderr, "No primary device set. Run `yaver primary set <deviceId>` first.")
		os.Exit(1)
	}
	if len(args) == 0 {
		// Pure Yaver-level headless auth. Reuses the same SSH-piped
		// `yaver auth --headless` path as the runner quick flow's
		// reauth recovery branch.
		if err := runRemoteHeadlessYaverAuthOverSSH(current); err != nil {
			fmt.Fprintf(os.Stderr, "primary auth: %v\n", err)
			os.Exit(1)
		}
		// Wait for the remote daemon's lifecycle to actually flip out of
		// "yaver-auth-expired" before returning. SSH `yaver auth` writes
		// a fresh token to disk in a separate process; the running
		// daemon may still hold the old token in memory until the
		// /auth/reload-from-disk nudge propagates or the next 5-min
		// heartbeat tick runs. Without this wait, an immediate
		// `yaver primary status` races the heartbeat and reports
		// "expired (needs reauth)" — the exact symptom we just chased.
		// Bound matches runRunnerAuthQuickFlow so the two surfaces
		// behave the same.
		localCfg, err := LoadConfig()
		if err == nil && localCfg != nil && strings.TrimSpace(localCfg.AuthToken) != "" {
			if devices, derr := listDevices(localCfg.ConvexSiteURL, localCfg.AuthToken); derr == nil {
				var target *DeviceInfo
				for i := range devices {
					if devices[i].DeviceID == current {
						target = &devices[i]
						break
					}
				}
				if target != nil {
					probe, werr := waitForRemoteYaverAuth(localCfg, target, 2*time.Minute)
					if werr != nil {
						fmt.Fprintf(os.Stderr, "primary auth: %v\n", werr)
						os.Exit(1)
					}
					fmt.Printf("Remote yaver: %s\n", describeDeviceReauthProbe(probe))
				}
			}
		}
		return
	}
	runner := normalizeRunnerAuthName(args[0])
	if runner != "claude" && runner != "codex" {
		fmt.Fprintf(os.Stderr, "primary auth: unsupported runner %q. Use claude / claude-code / codex.\n", args[0])
		os.Exit(1)
	}
	runRunnerQuickFlow(current, runner, args[1:])
}

// resolvePrimaryDeviceForRemote loads the caller's Convex creds, looks up the
// primaryDeviceId, and returns the device row. Used by primary signout / stop /
// auth — every remote-target verb needs the same prelude.
func resolvePrimaryDeviceForRemote(ctx context.Context) (*Config, string, *DeviceInfo, error) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		return nil, "", nil, err
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		return nil, "", nil, fmt.Errorf("read userSettings: %w", err)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return nil, "", nil, fmt.Errorf("no primary device set — run `yaver primary set <deviceId>` first")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil, "", nil, fmt.Errorf("load config: %w", err)
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, "", nil, fmt.Errorf("list devices: %w", err)
	}
	for i := range devices {
		if devices[i].DeviceID == current {
			return cfg, current, &devices[i], nil
		}
	}
	return nil, "", nil, fmt.Errorf("primary device %q is no longer in your registered devices — run `yaver primary clear` to reset", current)
}

func primaryConfirm(prompt string, args []string) bool {
	if primaryHasFlag(args, "-y") || primaryHasFlag(args, "--yes") {
		return true
	}
	fmt.Print(prompt + " [y/N]: ")
	var resp string
	fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}

// runPrimarySignout SSH-runs `yaver signout` on the primary device. Clears the
// remote box's auth token + marks it offline in Convex but leaves the agent
// process running (sits in yaver-auth-expired state, ready to be re-auth'd
// via `yaver primary auth`). Confirms before acting.
func runPrimarySignout(ctx context.Context, args []string) {
	_, _, target, err := resolvePrimaryDeviceForRemote(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary signout: %v\n", err)
		os.Exit(1)
	}
	label := strings.TrimSpace(target.Alias)
	if label == "" {
		label = target.Name
	}
	if !primaryConfirm(fmt.Sprintf("Sign out primary device %q (%s)?", label, target.DeviceID[:8]), args) {
		fmt.Println("Aborted.")
		return
	}
	hint := strings.TrimSpace(target.Alias)
	if hint == "" {
		hint = target.DeviceID
	}
	yaverPath := findYaverBinary()
	cmd := osexec.Command(yaverPath, "ssh", hint, "--", "yaver", "signout")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "primary signout: remote ssh failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Primary %s signed out. Re-auth with `yaver primary auth`.\n", label)
}

// runPrimaryStop SSH-runs `yaver stop` on the primary device. Stops the agent
// process AND disables auto-start so it doesn't immediately resurrect. Confirms
// before acting because this takes the box offline until someone restarts it.
func runPrimaryStop(ctx context.Context, args []string) {
	_, _, target, err := resolvePrimaryDeviceForRemote(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary stop: %v\n", err)
		os.Exit(1)
	}
	label := strings.TrimSpace(target.Alias)
	if label == "" {
		label = target.Name
	}
	if !primaryConfirm(fmt.Sprintf("Stop the agent on primary device %q (%s)? This takes it offline until restarted.", label, target.DeviceID[:8]), args) {
		fmt.Println("Aborted.")
		return
	}
	hint := strings.TrimSpace(target.Alias)
	if hint == "" {
		hint = target.DeviceID
	}
	yaverPath := findYaverBinary()
	cmd := osexec.Command(yaverPath, "ssh", hint, "--", "yaver", "stop")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "primary stop: remote ssh failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Primary %s stopped. Restart from the box with `yaver serve` (or via systemd/launchd if installed).\n", label)
}

// runPrimaryProjects implements both `yaver primary projects` (every
// project on the box) and `yaver primary mobiles` (mobile-capable filter).
//
// The agent already serves /projects (general scanner — flags monorepos,
// branches, tags, subframeworks) and /projects/mobile (Expo / RN / Flutter
// / Swift / Kotlin only — used by the mobile Hot Reload tab). This wires
// the same surfaces to the CLI so we can confirm discovery on a remote
// primary box without opening the phone — handy for shooting a marketing
// demo or auditing a fresh ARM64 / Pi after pairing.
//
// Both endpoints are auth'd by the Yaver session token alone — no
// coding-agent dependency. Boxes with neither claude nor codex installed
// still surface projects identically.
func runPrimaryProjects(ctx context.Context, args []string, mobileOnly bool) {
	asJSON := false
	for _, a := range args {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "--json":
			asJSON = true
		case "":
			// noop
		default:
			verb := "projects"
			if mobileOnly {
				verb = "mobiles"
			}
			fmt.Fprintf(os.Stderr, "primary %s: unknown argument %q\n", verb, a)
			os.Exit(1)
		}
	}

	verb := "projects"
	if mobileOnly {
		verb = "mobiles"
	}

	cfg, _, target, err := resolvePrimaryDeviceForRemote(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary %s: %v\n", verb, err)
		os.Exit(1)
	}
	if !target.IsOnline {
		fmt.Fprintf(os.Stderr, "primary %s: %s is offline (no recent heartbeat). Try `yaver primary status`.\n", verb, target.Name)
		os.Exit(1)
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary %s: %v\n", verb, err)
		os.Exit(1)
	}
	if len(candidates) == 0 {
		fmt.Fprintf(os.Stderr, "primary %s: %s has no reachable transport candidates.\n", verb, target.Name)
		os.Exit(1)
	}

	path := "/projects"
	if mobileOnly {
		path = "/projects/mobile"
	}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// /projects/mobile may scan synchronously on cold cache (10-min TTL,
	// fresh boxes have nothing). Give a generous slice so a first-time
	// scan doesn't time out.
	//
	// We iterate candidates manually instead of calling doRemoteAgentRequest
	// because some boxes publish a `<deviceID>.yaver.io` PublicEndpoint that
	// resolves to a stale CF/Vercel wildcard returning 404 for everything
	// except / and /info-cached. doRemoteAgentRequest returns immediately
	// on 4xx, so the relay candidate (which actually proxies to the agent)
	// is never tried. Treat 404 + 502/503 as "wrong host, try next" so we
	// fall through to the relay /d/{deviceID} URL on its own.
	raw, status, chosenURL, err := primaryFetchWithFallthrough(reqCtx, candidates, cfg.AuthToken, path, 20*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary %s: %v\n", verb, err)
		os.Exit(1)
	}
	if status < 200 || status >= 300 {
		fmt.Fprintf(os.Stderr, "primary %s: %s returned HTTP %d: %s\n", verb, chosenURL, status, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}

	if asJSON {
		os.Stdout.Write(raw)
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return
	}

	// Both endpoints carry partially overlapping shapes. Decode into a
	// permissive map so a missing field on one endpoint doesn't blank
	// the column for the other (e.g. /projects has `tags` + `isMonorepo`
	// but no `size`; /projects/mobile has `size` + `monorepoRoot` but no
	// `tags`).
	var resp struct {
		Projects  []map[string]interface{} `json:"projects"`
		ScannedAt string                   `json:"scannedAt"`
		Scanning  bool                     `json:"scanning"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "primary %s: parse response: %v\n", verb, err)
		os.Exit(1)
	}

	label := strings.TrimSpace(target.Alias)
	if label == "" {
		label = target.Name
	}
	scope := "all"
	if mobileOnly {
		scope = "mobile-capable"
	}
	fmt.Printf("%s — %d %s project(s)", label, len(resp.Projects), scope)
	if resp.Scanning {
		fmt.Print(" (scan in progress)")
	}
	if strings.TrimSpace(resp.ScannedAt) != "" {
		fmt.Printf(" — scanned %s", resp.ScannedAt)
	}
	fmt.Println()
	if len(resp.Projects) == 0 {
		fmt.Println("  (none — try `yaver ssh primary` then check ~/Workspace, ~/Projects, ~/src on the box)")
		return
	}

	rows := make([][]string, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		framework := stringField(p, "framework")
		if framework == "" {
			framework = "?"
		}
		// Decorate framework with the monorepo flag from /projects, or
		// monorepoApp/monorepoRoot from /projects/mobile so the output
		// makes the layout obvious without forcing --json.
		if b, _ := p["isMonorepo"].(bool); b {
			framework = framework + " (monorepo)"
		} else if app := stringField(p, "monorepoApp"); app != "" {
			framework = framework + " (mono:" + app + ")"
		}

		var caps []string
		if mobile, _ := p["mobileCapable"].(bool); mobile {
			caps = append(caps, "mobile")
		}
		if web, _ := p["webCapable"].(bool); web {
			caps = append(caps, "web")
		}
		if surface := stringField(p, "primarySurface"); surface != "" && surface != "none" {
			caps = append(caps, "surface="+surface)
		}
		if subs, ok := p["subframeworks"].([]interface{}); ok && len(subs) > 0 {
			subList := make([]string, 0, len(subs))
			for _, s := range subs {
				if str, ok := s.(string); ok && str != "" {
					subList = append(subList, str)
				}
			}
			if len(subList) > 0 {
				caps = append(caps, "subs="+strings.Join(subList, "+"))
			}
		}
		if tags, ok := p["tags"].([]interface{}); ok && len(tags) > 0 {
			tagList := make([]string, 0, len(tags))
			for _, t := range tags {
				if str, ok := t.(string); ok && str != "" {
					tagList = append(tagList, str)
				}
			}
			if len(tagList) > 0 {
				caps = append(caps, "tags="+strings.Join(tagList, ","))
			}
		}
		capsStr := strings.Join(caps, " ")
		if capsStr == "" {
			capsStr = "-"
		}

		branch := stringField(p, "branch")
		if branch == "" {
			branch = "-"
		}
		path := stringField(p, "path")

		rows = append(rows, []string{framework, branch, capsStr, path})
	}

	colW := []int{len("FRAMEWORK"), len("BRANCH"), len("FLAGS"), len("PATH")}
	for _, r := range rows {
		for i, cell := range r {
			if l := len(cell); l > colW[i] {
				colW[i] = l
			}
		}
	}
	// Cap PATH so a long workspace prefix doesn't swallow the terminal.
	if colW[3] > 80 {
		colW[3] = 80
	}
	headers := []string{"FRAMEWORK", "BRANCH", "FLAGS", "PATH"}
	for i, h := range headers {
		if i > 0 {
			fmt.Print("  ")
		} else {
			fmt.Print("  ")
		}
		if i < len(headers)-1 {
			fmt.Printf("%-*s", colW[i], h)
		} else {
			fmt.Print(h)
		}
	}
	fmt.Println()
	for _, r := range rows {
		for i, cell := range r {
			if i > 0 {
				fmt.Print("  ")
			} else {
				fmt.Print("  ")
			}
			if i < len(r)-1 {
				fmt.Printf("%-*s", colW[i], cell)
			} else {
				fmt.Print(cell)
			}
		}
		fmt.Println()
	}
}

// primaryFetchWithFallthrough iterates remote candidates and returns the
// first 2xx response. On 4xx (except 401/403 which are real auth errors)
// and 5xx it advances to the next candidate. doRemoteAgentRequest can't
// do this because it bails on any 4xx — we want the more permissive
// behavior here so a stale `<deviceID>.yaver.io` CF route doesn't shadow
// the working relay route on the same device.
func primaryFetchWithFallthrough(ctx context.Context, candidates []RemoteAgentCandidate, token, path string, perCallTimeout time.Duration) ([]byte, int, string, error) {
	if len(candidates) == 0 {
		return nil, 0, "", fmt.Errorf("no transport candidates available")
	}
	client := remoteHTTPClient(perCallTimeout)
	orderRemoteAgentCandidates(candidates)
	var errs []string
	var lastRaw []byte
	var lastStatus int
	var lastURL string
	for _, candidate := range candidates {
		url := strings.TrimRight(candidate.BaseURL, "/") + path
		lastURL = url
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", candidate.BaseURL, err))
			continue
		}
		if strings.TrimSpace(token) != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range candidate.Headers {
			if strings.TrimSpace(v) != "" {
				req.Header.Set(k, v)
			}
		}
		req.Header.Set("X-Yaver-Proxied-By", localDeviceID())
		req.Header.Set("X-Yaver-Proxied-Tool", "primary-projects")

		resp, err := client.Do(req)
		if err != nil {
			recordRemoteAgentFailure(candidate.DeviceID, candidate.BaseURL, time.Now())
			errs = append(errs, fmt.Sprintf("%s: %v", candidate.BaseURL, err))
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			recordRemoteAgentFailure(candidate.DeviceID, candidate.BaseURL, time.Now())
			errs = append(errs, fmt.Sprintf("%s: read response: %v", candidate.BaseURL, readErr))
			continue
		}
		lastRaw, lastStatus = raw, resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			recordRemoteAgentSuccess(candidate.DeviceID, candidate.BaseURL, time.Now())
			return raw, resp.StatusCode, url, nil
		}
		// 401/403 are real auth signals from the agent itself — surface
		// those immediately so the caller can hint at re-auth.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return raw, resp.StatusCode, url, nil
		}
		// Everything else (404, 5xx, gateway errors) likely means we hit
		// a wrong host or a relay tunnel that's down. Try next candidate.
		recordRemoteAgentFailure(candidate.DeviceID, candidate.BaseURL, time.Now())
		errs = append(errs, fmt.Sprintf("%s: HTTP %d", candidate.BaseURL, resp.StatusCode))
	}
	if lastStatus != 0 {
		return lastRaw, lastStatus, lastURL, fmt.Errorf("no candidate returned 2xx (last %d): %s", lastStatus, strings.Join(errs, " | "))
	}
	return nil, 0, lastURL, fmt.Errorf("all candidates failed: %s", strings.Join(errs, " | "))
}

func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func primaryUsage() {
	fmt.Print(`yaver primary — manage + inspect the auto-connect preferred device

Usage:
  yaver primary                   Show current primary + all devices
  yaver primary status [--json]   Live status of the primary device
                                  (agent version, lifecycle, runners,
                                  dev-server) over the existing direct/
                                  relay transport stack
  yaver primary auth              Run remote 'yaver auth --headless' on
                                  the primary device (Yaver-level auth)
  yaver primary auth <claude|claude-code|codex>
                                  Run the runner sanity/auth flow on the
                                  primary device for the named coding agent
  yaver primary signout [-y]      Sign the primary device out (clears its
                                  Yaver auth token + marks it offline; the
                                  agent stays running but enters
                                  yaver-auth-expired state). Prompts for
                                  confirmation unless -y is given.
  yaver primary stop [-y]         Stop the agent process on the primary
                                  device + disable its auto-start service.
                                  Prompts for confirmation unless -y.
  yaver primary projects [--json] List ALL projects discovered on the primary
                                  device by the agent's filesystem scanner
                                  (mobile + web + native).
  yaver primary mobiles [--json]  List ONLY mobile-capable projects (Expo /
                                  React Native / Flutter / Swift / Kotlin).
                                  Same scanner; filtered surface. Discovery
                                  runs without any coding agent installed.
  yaver primary <claude|claude-code|codex>
                                  Same as 'auth <runner>' — kept as a
                                  shortcut so existing scripts still work
  yaver primary set [deviceId|name|alias|self]
                                  Mark a device as primary. With NO arg (or
                                  'self' / 'me' / 'local' / '.') marks THIS
                                  machine as primary. Otherwise resolves a
                                  partial deviceId / name / alias prefix.
  yaver primary clear             Unset the preference (multi-device users
                                  will have to pick manually again)

Single-device users auto-connect regardless of this setting.
`)
}

func normalizePrimaryRunnerQuickArg(arg string) string {
	runner := normalizeRunnerAuthName(arg)
	switch runner {
	case "claude", "codex":
		return runner
	default:
		return ""
	}
}

type primaryDevice struct {
	DeviceID        string   `json:"deviceId"`
	Name            string   `json:"name"`
	Platform        string   `json:"platform"`
	QuicHost        string   `json:"quicHost"`
	LocalIps        []string `json:"localIps,omitempty"`
	PublicEndpoints []string `json:"publicEndpoints,omitempty"`
	IsOnline        bool     `json:"isOnline"`
	IsGuest         bool     `json:"isGuest"`
	LastHeartbeat   int64    `json:"lastHeartbeat"`
}

func primaryListDevices(ctx context.Context, token, convexURL string) ([]primaryDevice, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", convexURL+"/devices/list", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("devices/list failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Devices []primaryDevice `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Devices, nil
}

func primaryGetCurrent(ctx context.Context, token, convexURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", convexURL+"/settings", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("settings: %d", resp.StatusCode)
	}
	var parsed struct {
		Settings struct {
			PrimaryDeviceID string `json:"primaryDeviceId"`
		} `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	return parsed.Settings.PrimaryDeviceID, nil
}

// primarySaveRaw posts the primaryDeviceId field to /settings. Pass an empty
// string + clear=true to null-out the preference. Convex's setByToken treats
// null as "clear" and undefined as "leave untouched"; the explicit `clear`
// flag controls which one we send.
func primarySaveRaw(ctx context.Context, token, convexURL, deviceID string, clear bool) error {
	payload := map[string]interface{}{}
	if clear {
		payload["primaryDeviceId"] = nil
	} else {
		payload["primaryDeviceId"] = deviceID
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", convexURL+"/settings", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/settings POST failed: %d %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return nil
}

func primaryLoadAuth() (token, convex string, err error) {
	cfg, loadErr := LoadConfig()
	if loadErr != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex = cfg.ConvexSiteURL
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	return cfg.AuthToken, convex, nil
}

func runPrimaryShow(ctx context.Context) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read settings: %v\n", err)
		os.Exit(1)
	}
	devices, err := primaryListDevices(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
		os.Exit(1)
	}
	if current == "" {
		fmt.Println("Primary device: (none set)")
	} else {
		name := ""
		for _, d := range devices {
			if d.DeviceID == current {
				name = d.Name
				break
			}
		}
		if name == "" {
			fmt.Printf("Primary device: %s (record missing — run 'yaver primary clear' to reset)\n", current)
		} else {
			fmt.Printf("Primary device: %s (%s)\n", name, current[:min(8, len(current))])
		}
	}
	if len(devices) == 0 {
		fmt.Println("\nNo devices registered yet. Run 'yaver serve' on a machine to register it.")
		return
	}
	fmt.Println("\nAll registered devices:")
	for _, d := range devices {
		marker := "  "
		if d.DeviceID == current {
			marker = "★ "
		}
		// IsOnline = had a heartbeat in the last ~5 min. Without one,
		// the box may still be reachable over SSH; "bootstrap" reflects
		// that better than "offline" (which we reserve for genuinely
		// unreachable, no-internet boxes).
		status := "bootstrap"
		if d.IsOnline {
			status = "online"
		}
		shared := ""
		if d.IsGuest {
			shared = " (shared)"
		}
		fmt.Printf("%s%s — %s — %s%s — %s\n", marker, d.DeviceID[:min(8, len(d.DeviceID))], d.Name, status, shared, d.Platform)
	}
}

func runPrimarySet(ctx context.Context, args []string) {
	target := ""
	if len(args) > 0 {
		target = strings.TrimSpace(args[0])
	}
	// No arg, or explicit "self" / "me" / "local" / "." → mark THIS
	// machine as primary. Most natural after `yaver auth` on a fresh
	// machine: register, then claim primary in one step. Reads
	// device_id from ~/.yaver/config.json (populated when the agent
	// completes its first registration round-trip).
	if target == "" || strings.EqualFold(target, "self") || strings.EqualFold(target, "me") || strings.EqualFold(target, "local") || target == "." {
		cfg, _ := LoadConfig()
		if cfg == nil || strings.TrimSpace(cfg.DeviceID) == "" {
			fmt.Fprintln(os.Stderr, "This machine has no registered deviceId yet.")
			fmt.Fprintln(os.Stderr, "Run `yaver auth` and then `yaver serve` once so the agent registers,")
			fmt.Fprintln(os.Stderr, "then re-run `yaver primary set` to claim primary on this machine.")
			os.Exit(1)
		}
		target = cfg.DeviceID
	}
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	devices, err := primaryListDevices(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
		os.Exit(1)
	}
	// Resolve the target: exact deviceId, then unique prefix, then exact name.
	var matches []primaryDevice
	for _, d := range devices {
		if d.DeviceID == target || strings.EqualFold(d.Name, target) {
			matches = []primaryDevice{d}
			break
		}
		if strings.HasPrefix(d.DeviceID, target) {
			matches = append(matches, d)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "No device matching %q. Run 'yaver primary' to see the list.\n", target)
		os.Exit(1)
	}
	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "%q matches multiple devices — use a longer prefix or the full deviceId:\n", target)
		for _, d := range matches {
			fmt.Fprintf(os.Stderr, "  %s — %s\n", d.DeviceID, d.Name)
		}
		os.Exit(1)
	}
	chosen := matches[0]
	if chosen.IsGuest {
		fmt.Fprintln(os.Stderr, "Cannot mark a shared (guest) device as primary — the host can revoke it at any time.")
		os.Exit(1)
	}
	if err := primarySaveRaw(ctx, token, convex, chosen.DeviceID, false); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set primary: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Primary device set to %s (%s).\n", chosen.Name, chosen.DeviceID[:min(8, len(chosen.DeviceID))])
}

// runPrimaryPick is the interactive companion to `yaver primary set`.
// Lists owned devices in a numbered prompt, reads a selection from
// stdin, then writes userSettings.primaryDeviceId. Refuses to run
// without a TTY so script callers don't get stuck on an unanswered
// prompt — those should pass an explicit deviceId to `set`.
func runPrimaryPick(ctx context.Context) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "yaver primary pick: stdin is not a TTY — pass `<deviceId>` to `yaver primary set` instead.")
		os.Exit(1)
	}
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	devices, err := primaryListDevices(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
		os.Exit(1)
	}
	owned := make([]primaryDevice, 0, len(devices))
	for _, d := range devices {
		if !d.IsGuest {
			owned = append(owned, d)
		}
	}
	if len(owned) == 0 {
		fmt.Fprintln(os.Stderr, "No registered owner devices — run `yaver serve` on a machine to register it.")
		os.Exit(1)
	}
	current, _ := primaryGetCurrent(ctx, token, convex)
	current = strings.TrimSpace(current)
	fmt.Println("Pick a primary device:")
	for i, d := range owned {
		marker := "  "
		if d.DeviceID == current {
			marker = "★ "
		}
		status := "bootstrap"
		if d.IsOnline {
			status = "online"
		}
		fmt.Printf("  %s%d) %s — %s — %s — %s\n", marker, i+1, d.Name, status, d.Platform, d.DeviceID[:min(8, len(d.DeviceID))])
	}
	fmt.Print("\nNumber (or q to abort): ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "\naborted")
		os.Exit(1)
	}
	choice := strings.TrimSpace(line)
	if choice == "" || strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") {
		fmt.Println("Aborted.")
		return
	}
	idx, err := strconv.Atoi(choice)
	if err != nil || idx < 1 || idx > len(owned) {
		fmt.Fprintf(os.Stderr, "invalid selection %q — expected 1..%d\n", choice, len(owned))
		os.Exit(1)
	}
	chosen := owned[idx-1]
	if err := primarySaveRaw(ctx, token, convex, chosen.DeviceID, false); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set primary: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Primary device set to %s (%s).\n", chosen.Name, chosen.DeviceID[:min(8, len(chosen.DeviceID))])
}

func runPrimaryClear(ctx context.Context) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := primarySaveRaw(ctx, token, convex, "", true); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to clear primary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Primary device cleared. Multi-device users will be asked to pick on next login.")
}

func runPrimaryRunnerQuickFlow(ctx context.Context, runner string, extra []string) {
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "primary: unexpected extra arguments after %s: %s\n", runner, strings.Join(extra, " "))
		os.Exit(1)
	}
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read settings: %v\n", err)
		os.Exit(1)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		fmt.Fprintln(os.Stderr, "No primary device set. Run `yaver primary set <deviceId>` first.")
		os.Exit(1)
	}
	runRunnerQuickFlow(current, runner, nil)
}
