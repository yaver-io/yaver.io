package main

// secondary_cmd.go — `yaver secondary` CLI subcommand.
//
// Mirrors `yaver primary` but writes userSettings.secondaryDeviceId.
// The secondary slot is a second elevated device the user can name —
// surfaced everywhere primary is: mobile auto-connect fallback,
// `yaver ssh secondary`, the watchdog's tight 90s staleness threshold,
// and the web/mobile pickers.
//
// Shape:
//
//   yaver secondary               # print current secondary + device list
//   yaver secondary show          # alias for bare invocation
//   yaver secondary set <devId>   # mark a device secondary (partial match OK)
//   yaver secondary clear         # unset the preference
//   yaver secondary status        # detailed status against the secondary
//   yaver secondary ping          # one-shot reachability + auth check
//   yaver secondary auth          # SSH-based headless re-auth
//
// Most users will never set this. It exists for the dictate-from-phone
// scenario where you want a fallback box (typically your home Mac mini
// or a second cloud agent) automatically chosen when the primary is
// asleep. The same `setByToken` mutation handles both slots.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func runSecondary(args []string) {
	ctx := context.Background()
	if len(args) == 0 {
		runSecondaryShow(ctx)
		return
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		runSecondaryStatus(ctx, primaryHasFlag(args[1:], "--json"))
		return
	case "ping":
		runSecondaryPing(ctx, args[1:])
		return
	case "auth":
		runSecondaryAuth(ctx, args[1:])
		return
	}
	switch args[0] {
	case "show", "get", "list", "ls":
		runSecondaryShow(ctx)
	case "set":
		runSecondarySet(ctx, args[1:])
	case "pick", "choose", "select":
		runSecondaryPick(ctx)
	case "clear", "unset", "remove":
		runSecondaryClear(ctx)
	case "help", "-h", "--help":
		secondaryUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver secondary %s\n\n", args[0])
		secondaryUsage()
		os.Exit(1)
	}
}

func secondaryUsage() {
	fmt.Println(`yaver secondary — pick a fallback device to auto-connect when primary is offline.

Usage:
  yaver secondary                  Show current secondary + all devices
  yaver secondary set <devId>      Mark a device as secondary (partial match)
  yaver secondary set self         Mark THIS machine as secondary
  yaver secondary pick             Interactive picker
  yaver secondary clear            Unset the secondary preference
  yaver secondary status [--json]  Probe the secondary's agent + auth state
  yaver secondary ping             One-shot reachability check
  yaver secondary auth             SSH in and run 'yaver auth --headless'

The secondary slot has the same access semantics as primary (must be
one of your owned devices, never a guest device). It receives the
same tight 90s staleness threshold from the watchdog.`)
}

func runSecondaryShow(ctx context.Context) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := secondaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read settings: %v\n", err)
		os.Exit(1)
	}
	primaryID, _ := primaryGetCurrent(ctx, token, convex)
	devices, err := primaryListDevices(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
		os.Exit(1)
	}
	if current == "" {
		fmt.Println("Secondary device: (none set)")
	} else {
		name := ""
		for _, d := range devices {
			if d.DeviceID == current {
				name = d.Name
				break
			}
		}
		if name == "" {
			fmt.Printf("Secondary device: %s (record missing — run 'yaver secondary clear' to reset)\n", current)
		} else {
			fmt.Printf("Secondary device: %s (%s)\n", name, current[:min(8, len(current))])
		}
	}
	if len(devices) == 0 {
		fmt.Println("\nNo devices registered yet. Run 'yaver serve' on a machine to register it.")
		return
	}
	fmt.Println("\nAll registered devices:")
	for _, d := range devices {
		marker := "  "
		if d.DeviceID == primaryID {
			marker = "★ " // primary
		}
		if d.DeviceID == current {
			marker = "☆ " // secondary (always wins over primary marker if same id, but UI rejects that)
		}
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

func runSecondarySet(ctx context.Context, args []string) {
	target := ""
	if len(args) > 0 {
		target = strings.TrimSpace(args[0])
	}
	if target == "" || strings.EqualFold(target, "self") || strings.EqualFold(target, "me") || strings.EqualFold(target, "local") || target == "." {
		cfg, _ := LoadConfig()
		if cfg == nil || strings.TrimSpace(cfg.DeviceID) == "" {
			fmt.Fprintln(os.Stderr, "This machine has no registered deviceId yet.")
			fmt.Fprintln(os.Stderr, "Run `yaver auth` and then `yaver serve` once so the agent registers,")
			fmt.Fprintln(os.Stderr, "then re-run `yaver secondary set` to claim secondary on this machine.")
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
		fmt.Fprintf(os.Stderr, "No device matching %q. Run 'yaver secondary' to see the list.\n", target)
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
		fmt.Fprintln(os.Stderr, "Cannot mark a shared (guest) device as secondary — the host can revoke it at any time.")
		os.Exit(1)
	}
	// Reject when secondary == primary. Both slots pointing at the
	// same device makes the watchdog and auto-connect surfaces trip
	// over themselves; the user almost certainly meant to pick a
	// different box. Surface the conflict clearly.
	primaryID, _ := primaryGetCurrent(ctx, token, convex)
	if strings.TrimSpace(primaryID) != "" && primaryID == chosen.DeviceID {
		fmt.Fprintf(os.Stderr, "%s is already your primary — pick a different device for secondary, or clear primary first.\n", chosen.Name)
		os.Exit(1)
	}
	if err := secondarySaveRaw(ctx, token, convex, chosen.DeviceID, false); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set secondary: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Secondary device set to %s (%s).\n", chosen.Name, chosen.DeviceID[:min(8, len(chosen.DeviceID))])
}

func runSecondaryClear(ctx context.Context) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := secondarySaveRaw(ctx, token, convex, "", true); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to clear secondary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Secondary device cleared.")
}

// runSecondaryPick — interactive picker, mirrors runPrimaryPick behaviour
// but writes the secondary slot.
func runSecondaryPick(ctx context.Context) {
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
		fmt.Fprintln(os.Stderr, "No owned devices available.")
		os.Exit(1)
	}
	fmt.Println("Select a device to mark as secondary:")
	for i, d := range owned {
		marker := "  "
		fmt.Printf("  %s%2d) %s — %s — %s\n", marker, i+1, d.DeviceID[:min(8, len(d.DeviceID))], d.Name, d.Platform)
	}
	fmt.Print("Number (or 'q' to cancel): ")
	var input string
	fmt.Scanln(&input)
	input = strings.TrimSpace(input)
	if input == "" || input == "q" || input == "Q" {
		fmt.Println("Cancelled.")
		return
	}
	idx, err := parseSecondaryIndex(input, len(owned))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid selection: %v\n", err)
		os.Exit(1)
	}
	chosen := owned[idx]
	primaryID, _ := primaryGetCurrent(ctx, token, convex)
	if strings.TrimSpace(primaryID) != "" && primaryID == chosen.DeviceID {
		fmt.Fprintf(os.Stderr, "%s is already your primary — pick a different device.\n", chosen.Name)
		os.Exit(1)
	}
	if err := secondarySaveRaw(ctx, token, convex, chosen.DeviceID, false); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set secondary: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Secondary device set to %s.\n", chosen.Name)
}

func parseSecondaryIndex(s string, max int) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	if n < 1 || n > max {
		return 0, fmt.Errorf("out of range: %d (1..%d)", n, max)
	}
	return n - 1, nil
}

// runSecondaryStatus / Ping / Auth delegate to the existing primary
// helpers via a slot indirection — the work itself (probe + ssh + …)
// is shared. We just point the resolver at the secondary slot.
func runSecondaryStatus(ctx context.Context, asJSON bool) {
	runElevatedSlotStatus(ctx, "secondary", asJSON)
}

func runSecondaryPing(ctx context.Context, args []string) {
	runElevatedSlotPing(ctx, "secondary", args)
}

func runSecondaryAuth(ctx context.Context, args []string) {
	runElevatedSlotAuth(ctx, "secondary", args)
}
