package main

// slot_cmd.go — slot-aware helpers shared by `yaver primary` and
// `yaver secondary`. The two CLIs differ only in which userSettings
// field they read (`primaryDeviceId` vs `secondaryDeviceId`) and the
// label they print to the user; everything else (transport probe,
// status fetch, ping report, SSH-headless auth) is identical.
//
// runElevatedSlot* funcs are the implementations. The primary side
// keeps its dedicated runPrimaryStatus / runPrimaryPing / runPrimaryAuth
// for backward compatibility — the new secondary subcommand routes
// straight through these helpers.

import (
	"context"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"
)

// slotResolveDeviceID returns the device ID stored at the named slot
// (either "primary" or "secondary"), and a friendly user-facing label
// for the slot. Empty deviceID means the user hasn't set this slot.
func slotResolveDeviceID(ctx context.Context, slot string) (deviceID, slotLabel, token, convex string, err error) {
	t, c, err := primaryLoadAuth()
	if err != nil {
		return "", slot, "", "", err
	}
	switch strings.ToLower(slot) {
	case "secondary":
		id, err := secondaryGetCurrent(ctx, t, c)
		if err != nil {
			return "", "secondary", t, c, fmt.Errorf("read userSettings: %w", err)
		}
		return strings.TrimSpace(id), "secondary", t, c, nil
	default:
		id, err := primaryGetCurrent(ctx, t, c)
		if err != nil {
			return "", "primary", t, c, fmt.Errorf("read userSettings: %w", err)
		}
		return strings.TrimSpace(id), "primary", t, c, nil
	}
}

// runElevatedSlotStatus is the slot-agnostic implementation behind
// `yaver primary status` and `yaver secondary status`. Different from
// runPrimaryStatus in that it doesn't auto-fall-back to the only-owned
// device when the slot is unset — that fallback only makes sense for
// primary.
func runElevatedSlotStatus(ctx context.Context, slot string, asJSON bool) {
	deviceID, slotLabel, _, _, err := slotResolveDeviceID(ctx, slot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s status: %v\n", slotLabel, err)
		os.Exit(1)
	}
	if deviceID == "" {
		fmt.Fprintf(os.Stderr, "No %s device set. Run `yaver %s set <deviceId>` first.\n", slotLabel, slotLabel)
		os.Exit(1)
	}
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, deviceID)
	if err != nil {
		renderRemoteAgentStatusError(ctx, deviceID, err, asJSON)
		os.Exit(1)
	}
	renderRemoteAgentStatus(report, asJSON)
}

// runElevatedSlotPing implements the `yaver <slot> ping` reachability
// + ownership probe.
func runElevatedSlotPing(ctx context.Context, slot string, args []string) {
	fs := flag.NewFlagSet(slot+" ping", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	timeout := fs.Duration("timeout", 8*time.Second, "overall timeout")
	_ = fs.Parse(args)

	deviceID, slotLabel, _, _, err := slotResolveDeviceID(ctx, slot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s ping: %v\n", slotLabel, err)
		os.Exit(1)
	}
	if deviceID == "" {
		fmt.Fprintf(os.Stderr, "No %s device set. Run `yaver %s set <deviceId>` first.\n", slotLabel, slotLabel)
		os.Exit(1)
	}
	probeCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := fetchRemoteAgentPingByDeviceID(probeCtx, deviceID)
	emitPingReport(report, err, *jsonOut, slotLabel)
	if err != nil || report == nil {
		os.Exit(1)
	}
}

// runElevatedSlotAuth runs `yaver auth --headless` on the slot's
// device via `yaver ssh <handle>`. Slim version of runPrimaryAuth's
// happy-path: read slot → resolve to ssh handle → exec ssh.
func runElevatedSlotAuth(ctx context.Context, slot string, args []string) {
	deviceID, slotLabel, token, convex, err := slotResolveDeviceID(ctx, slot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auth: %v\n", slotLabel, err)
		os.Exit(1)
	}
	if deviceID == "" {
		fmt.Fprintf(os.Stderr, "No %s device set. Run `yaver %s set <deviceId>` first.\n", slotLabel, slotLabel)
		os.Exit(1)
	}
	devices, err := listDevices(convex, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auth: list devices: %v\n", slotLabel, err)
		os.Exit(1)
	}
	var target *DeviceInfo
	for i := range devices {
		if devices[i].DeviceID == deviceID {
			target = &devices[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "%s device %q is no longer registered — clear with `yaver %s clear`.\n", slotLabel, deviceID[:min(8, len(deviceID))], slotLabel)
		os.Exit(1)
	}
	hint := strings.TrimSpace(target.Alias)
	if hint == "" {
		hint = strings.TrimSpace(target.Name)
	}
	if hint == "" {
		hint = target.DeviceID
	}
	yaverPath := findYaverBinary()
	cmd := osexec.Command(yaverPath, "ssh", hint, "--", "yaver", "auth", "--headless")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s auth: remote ssh failed: %v\n", slotLabel, err)
		os.Exit(1)
	}
	_ = args
}

// resolveSSHSlot returns an ssh-able handle (alias > name > deviceID)
// for the named slot. Used by runSSHWrap so `yaver ssh primary` and
// `yaver ssh secondary` resolve through the same lookup path.
func resolveSSHSlot(slot string) (string, error) {
	handle, _, err := resolveSSHSlotDevice(slot)
	return handle, err
}

// resolveSSHSlotDevice is the operation form used by `yaver ssh`: it returns
// the already-fetched device row together with the friendly handle. The old
// handle-only flow discarded that row, then fetched the same device list two
// more times during route selection. On a remote/loaded backend those advisory
// round trips cost seconds before any transport was even tried.
func resolveSSHSlotDevice(slot string) (string, *DeviceInfo, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", nil, fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// Slot settings and device inventory are independent reads. Fetching them
	// serially made `yaver ssh primary` pay two full backend latencies before it
	// could probe a transport; run them under the same bounded wall-clock.
	type slotResult struct {
		id  string
		err error
	}
	type devicesResult struct {
		devices []DeviceInfo
		err     error
	}
	slotCh := make(chan slotResult, 1)
	devicesCh := make(chan devicesResult, 1)
	go func() {
		var id string
		var getErr error
		if strings.EqualFold(slot, "secondary") {
			id, getErr = secondaryGetCurrent(ctx, cfg.AuthToken, convex)
		} else {
			id, getErr = primaryGetCurrent(ctx, cfg.AuthToken, convex)
		}
		slotCh <- slotResult{id: id, err: getErr}
	}()
	go func() {
		devices, listErr := listDevices(convex, cfg.AuthToken)
		devicesCh <- devicesResult{devices: devices, err: listErr}
	}()
	slotRead := <-slotCh
	devicesRead := <-devicesCh
	if slotRead.err != nil {
		return "", nil, fmt.Errorf("could not read %s device: %w", strings.ToLower(slot), slotRead.err)
	}
	slotID := slotRead.id
	slotID = strings.TrimSpace(slotID)
	if slotID == "" {
		return "", nil, fmt.Errorf("no %s device set — run `yaver %s set <deviceId>` first", strings.ToLower(slot), strings.ToLower(slot))
	}
	if devicesRead.err != nil {
		return "", nil, fmt.Errorf("could not list devices: %w", devicesRead.err)
	}
	devices := devicesRead.devices
	for i := range devices {
		d := &devices[i]
		if d.DeviceID == slotID {
			if a := strings.TrimSpace(d.Alias); a != "" {
				return a, d, nil
			}
			if n := strings.TrimSpace(d.Name); n != "" {
				return n, d, nil
			}
			return d.DeviceID, d, nil
		}
	}
	return "", nil, fmt.Errorf("%s device %s is set but not in the device list — run 'yaver %s clear' to reset", strings.ToLower(slot), slotID[:min(8, len(slotID))], strings.ToLower(slot))
}
