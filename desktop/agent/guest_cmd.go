package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func runGuests(args []string) {
	if len(args) == 0 {
		printGuestsUsage()
		return
	}

	switch args[0] {
	case "invite":
		runGuestsInvite(args[1:])
	case "accept":
		runGuestsAccept(args[1:])
	case "list", "ls":
		runGuestsList()
	case "remove", "revoke", "rm":
		runGuestsRemove(args[1:])
	case "unshare-machine":
		runGuestsUnshareMachine(args[1:])
	case "hostkeys":
		runGuestsHostKeys(args[1:])
	case "config":
		runGuestsConfig(args[1:])
	case "usage":
		runGuestsUsage(args[1:])
	case "help", "--help", "-h":
		printGuestsUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown guests subcommand: %s\n", args[0])
		printGuestsUsage()
		os.Exit(1)
	}
}

func runGuestsInvite(args []string) {
	opts := InviteGuestOpts{}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--user-id" || a == "--userid":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--user-id requires a value")
				os.Exit(1)
			}
			opts.UserID = args[i+1]
			i++
		case strings.HasPrefix(a, "--user-id="):
			opts.UserID = strings.TrimPrefix(a, "--user-id=")
		case a == "--machines" || a == "--devices":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--machines requires a value")
				os.Exit(1)
			}
			opts.ProposedDeviceIDs = splitComma(args[i+1])
			i++
		case strings.HasPrefix(a, "--machines="):
			opts.ProposedDeviceIDs = splitComma(strings.TrimPrefix(a, "--machines="))
		case strings.HasPrefix(a, "--devices="):
			opts.ProposedDeviceIDs = splitComma(strings.TrimPrefix(a, "--devices="))
		case a == "--scope":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--scope requires a value (full|feedback-only)")
				os.Exit(1)
			}
			opts.Scope = args[i+1]
			i++
		case strings.HasPrefix(a, "--scope="):
			opts.Scope = strings.TrimPrefix(a, "--scope=")
		case a == "--full":
			// Shorthand for teammate invites. End-user invites just omit this flag.
			opts.Scope = GuestScopeFull
		case a == "--feedback-only":
			// Redundant with the default, but explicit is nice for scripts.
			opts.Scope = GuestScopeFeedbackOnly
		case a == "--project" || a == "--projects":
			// Repeatable project-scoping: --project SFMG --project talos
			// (or comma-separated via --projects=SFMG,talos).
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--project requires a value (project name or comma-separated list)")
				os.Exit(1)
			}
			opts.AllowedProjects = append(opts.AllowedProjects, splitComma(args[i+1])...)
			i++
		case strings.HasPrefix(a, "--project="):
			opts.AllowedProjects = append(opts.AllowedProjects, splitComma(strings.TrimPrefix(a, "--project="))...)
		case strings.HasPrefix(a, "--projects="):
			opts.AllowedProjects = append(opts.AllowedProjects, splitComma(strings.TrimPrefix(a, "--projects="))...)
		default:
			positional = append(positional, a)
		}
	}

	if opts.Scope != "" {
		switch opts.Scope {
		case GuestScopeFull, GuestScopeFeedbackOnly:
			// ok
		default:
			fmt.Fprintf(os.Stderr, "Invalid --scope %q. Must be 'full' or 'feedback-only'.\n", opts.Scope)
			os.Exit(1)
		}
	}

	if len(positional) > 0 && opts.Email == "" && opts.UserID == "" {
		// First positional is treated as email unless it looks like a userId (no @).
		v := strings.TrimSpace(positional[0])
		if strings.Contains(v, "@") {
			opts.Email = v
		} else {
			opts.UserID = v
		}
	}

	if opts.Email == "" && opts.UserID == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver guests invite <email | --user-id <id>> [--machines id1,id2]")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	result, err := InviteGuestWith(convexURL, cfg.AuthToken, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to invite: %v\n", err)
		os.Exit(1)
	}

	target := opts.Email
	if target == "" {
		target = "user " + opts.UserID
	}
	fmt.Printf("Invitation sent to %s\n", target)
	fmt.Printf("Invite code: %s\n", result.InviteCode)
	if len(opts.ProposedDeviceIDs) > 0 {
		fmt.Printf("Proposed machines: %s\n", strings.Join(opts.ProposedDeviceIDs, ", "))
	}
	scopeShown := result.Scope
	if scopeShown == "" {
		scopeShown = opts.Scope
	}
	if scopeShown == "" {
		scopeShown = GuestScopeFeedbackOnly // server default for new invites
	}
	if scopeShown == GuestScopeFeedbackOnly {
		fmt.Printf("Scope: %s  (feedback / blackbox / voice — no tasks, no vibing, no dev-server, /info redacted, tasks force-containerized)\n", scopeShown)
		fmt.Println("  For a teammate who should get full access, re-invite with: --scope=full")
	} else {
		fmt.Printf("Scope: %s  (classic teammate — tasks, vibing, dev server, builds, projects, plus feedback/voice)\n", scopeShown)
	}
	if len(opts.AllowedProjects) > 0 {
		fmt.Printf("Projects: %s  (this guest can only interact with these projects)\n", strings.Join(cleanProjectList(opts.AllowedProjects), ", "))
	} else {
		fmt.Println("Projects: all  (to narrow, re-invite with --projects=SFMG,talos)")
	}
	fmt.Println()
	if result.GuestRegistered {
		fmt.Println("This person is already registered on Yaver.")
		fmt.Println("They'll see the invitation in the Yaver app automatically.")
	} else {
		fmt.Println("This person is not yet registered on Yaver.")
		fmt.Println("Tell them to:")
		fmt.Println("  1. Download the Yaver app")
		fmt.Println("  2. Sign in with any method (Apple, Google, Microsoft, or email)")
		fmt.Println("  3. Enter the invite code above (or, if you invited an email, just sign in with it)")
	}
	fmt.Println()
	fmt.Println("The invitation expires in 2 days.")
}

// runGuestsAccept accepts a pending invitation as the signed-in user.
// Usage: yaver guests accept <code> [--machines id1,id2]
func runGuestsAccept(args []string) {
	var code string
	var approved []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--machines" || a == "--devices":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--machines requires a value")
				os.Exit(1)
			}
			approved = splitComma(args[i+1])
			i++
		case strings.HasPrefix(a, "--machines="):
			approved = splitComma(strings.TrimPrefix(a, "--machines="))
		case strings.HasPrefix(a, "--devices="):
			approved = splitComma(strings.TrimPrefix(a, "--devices="))
		case a == "--help" || a == "-h":
			fmt.Println("Usage: yaver guests accept <code> [--machines id1,id2]")
			return
		default:
			if code == "" {
				code = a
			}
		}
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver guests accept <code> [--machines id1,id2]")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	preview, err := FindInviteByCode(convexURL, cfg.AuthToken, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not load invitation: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("From: %s <%s>\n", preview.HostName, preview.HostEmail)
	if len(preview.HostDevices) == 0 {
		fmt.Println("Host has no registered devices yet — accepting gives you access to whatever they register later.")
	} else {
		fmt.Println("Host devices:")
		for _, d := range preview.HostDevices {
			marker := " "
			if d.Proposed {
				marker = "*"
			}
			fmt.Printf("  %s %s  (%s, %s)\n", marker, d.DeviceID, d.Name, d.Platform)
		}
		if len(preview.ProposedDeviceIDs) > 0 {
			fmt.Printf("Host proposed scope: %s (starred above)\n", strings.Join(preview.ProposedDeviceIDs, ", "))
		}
	}

	// If the caller didn't pass --machines, default to the host's proposal if present,
	// otherwise empty (= all).
	if len(approved) == 0 {
		approved = preview.ProposedDeviceIDs
	}
	if len(approved) > 0 {
		fmt.Printf("Accepting scope: %s\n", strings.Join(approved, ", "))
	} else {
		fmt.Println("Accepting scope: all host devices")
	}

	result, err := AcceptGuestByCode(convexURL, cfg.AuthToken, code, approved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to accept: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Joined %s's machine(s) via %s\n", result.HostName, result.HostEmail)
}

func runGuestsList() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	guests, err := FetchGuestList(convexURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list guests: %v\n", err)
		os.Exit(1)
	}

	if len(guests) == 0 {
		fmt.Println("No guests. Invite someone with: yaver guests invite <email>")
		return
	}

	fmt.Printf("%-30s  %-10s  %-20s  %s\n", "EMAIL", "STATUS", "NAME", "SINCE")
	fmt.Printf("%-30s  %-10s  %-20s  %s\n", "-----", "------", "----", "-----")
	for _, g := range guests {
		since := ""
		if g.AcceptedAt > 0 {
			since = time.UnixMilli(g.AcceptedAt).Format("2006-01-02")
		} else {
			since = time.UnixMilli(g.CreatedAt).Format("2006-01-02")
		}
		name := g.FullName
		if name == "" {
			name = "-"
		}
		fmt.Printf("%-30s  %-10s  %-20s  %s\n", g.Email, g.Status, name, since)
	}
}

func runGuestsRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver guests remove <email>")
		os.Exit(1)
	}
	email := args[0]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	if err := RevokeGuest(convexURL, cfg.AuthToken, email); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove guest: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Guest access revoked for %s\n", email)
}

func runGuestsUnshareMachine(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: yaver guests unshare-machine <email> <machine-id>")
		os.Exit(1)
	}
	email := args[0]
	machineID := strings.TrimSpace(args[1])
	if machineID == "" {
		fmt.Fprintln(os.Stderr, "Machine ID required")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	configs, err := FetchGuestConfigs(convexURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch guest configs: %v\n", err)
		os.Exit(1)
	}

	var existing *GuestConfig
	for i := range configs {
		if strings.EqualFold(configs[i].GuestEmail, email) {
			existing = &configs[i]
			break
		}
	}
	if existing == nil {
		fmt.Fprintf(os.Stderr, "No guest config found for %s\n", email)
		os.Exit(1)
	}

	var remaining []string
	if existing.ShareAllMachines != nil && *existing.ShareAllMachines {
		devices, err := listDevices(convexURL, cfg.AuthToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
			os.Exit(1)
		}
		for _, d := range devices {
			if d.IsGuest || d.DeviceID == "" || d.DeviceID == machineID {
				continue
			}
			remaining = append(remaining, d.DeviceID)
		}
	} else {
		for _, id := range existing.MachineIDs {
			if id != "" && id != machineID {
				remaining = append(remaining, id)
			}
		}
	}

	if err := UpdateGuestConfig(convexURL, cfg.AuthToken, map[string]interface{}{
		"email":            email,
		"shareAllMachines": false,
		"machineIds":       remaining,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update guest config: %v\n", err)
		os.Exit(1)
	}

	if len(remaining) == 0 {
		fmt.Printf("Removed all machine sharing for %s\n", email)
		return
	}
	fmt.Printf("Stopped sharing machine %s with %s\n", machineID, email)
}

func runGuestsHostKeys(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: yaver guests hostkeys <email> <on|off>")
		os.Exit(1)
	}
	email := args[0]
	value := parseBoolish(args[1])

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	if err := UpdateGuestConfig(convexURL, cfg.AuthToken, map[string]interface{}{
		"email":          email,
		"useHostApiKeys": value,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update guest config: %v\n", err)
		os.Exit(1)
	}

	if value {
		fmt.Printf("Host-managed keys enabled for %s\n", email)
	} else {
		fmt.Printf("Host-managed keys disabled for %s\n", email)
	}
}

func runGuestsConfig(args []string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	if len(args) == 0 {
		// Show all configs
		configs, err := FetchGuestConfigs(convexURL, cfg.AuthToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to fetch configs: %v\n", err)
			os.Exit(1)
		}
		if len(configs) == 0 {
			fmt.Println("No guest configs. Guests use default settings (unlimited access).")
			return
		}
		for _, c := range configs {
			fmt.Printf("%-30s  ", c.GuestEmail)
			fmt.Printf("scope=%-14s  ", guestScopeOrDefault(c.Scope))
			mode := c.UsageMode
			if mode == "" {
				mode = "always"
			}
			fmt.Printf("mode=%-10s  ", mode)
			if c.DailyTokenLimit != nil && *c.DailyTokenLimit > 0 {
				fmt.Printf("limit=%ds/day  ", *c.DailyTokenLimit)
			} else {
				fmt.Printf("limit=unlimited  ")
			}
			if len(c.AllowedRunners) > 0 {
				fmt.Printf("runners=%v", c.AllowedRunners)
			} else {
				fmt.Printf("runners=all")
			}
			if c.UseHostAPIKeys != nil {
				fmt.Printf("  host_keys=%v", *c.UseHostAPIKeys)
			}
			if preset := guestResourcePreset(&c); preset != "" {
				fmt.Printf("  preset=%s", preset)
			}
			if c.AllowGuestProvidedAPIKeys != nil {
				fmt.Printf("  guest_keys=%v", *c.AllowGuestProvidedAPIKeys)
			}
			if c.AllowTunnelForward != nil {
				fmt.Printf("  tunnels=%v", *c.AllowTunnelForward)
			}
			if c.PriorityMode != "" {
				fmt.Printf("  priority=%s", c.PriorityMode)
			}
			fmt.Println()
		}
		return
	}

	// Parse: yaver guests config <email> [key=value ...]
	email := args[0]
	if len(args) < 2 {
		// Show config for this email
		configs, err := FetchGuestConfigs(convexURL, cfg.AuthToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to fetch configs: %v\n", err)
			os.Exit(1)
		}
		for _, c := range configs {
			if c.GuestEmail == email {
				mode := c.UsageMode
				if mode == "" {
					mode = "always"
				}
				fmt.Printf("Guest: %s (%s)\n", c.GuestEmail, c.GuestName)
				fmt.Printf("  Scope:            %s\n", guestScopeOrDefault(c.Scope))
				if projs := cleanProjectList(c.AllowedProjects); len(projs) > 0 {
					fmt.Printf("  Projects:         %s\n", strings.Join(projs, ", "))
				} else {
					fmt.Printf("  Projects:         all\n")
				}
				fmt.Printf("  Usage mode:       %s\n", mode)
				if c.DailyTokenLimit != nil && *c.DailyTokenLimit > 0 {
					fmt.Printf("  Daily limit:      %d seconds\n", *c.DailyTokenLimit)
				} else {
					fmt.Printf("  Daily limit:      unlimited\n")
				}
				if len(c.AllowedRunners) > 0 {
					fmt.Printf("  Allowed runners:  %v\n", c.AllowedRunners)
				} else {
					fmt.Printf("  Allowed runners:  all\n")
				}
				if c.Schedule != nil {
					tz := c.Schedule.Timezone
					if tz == "" {
						tz = "local"
					}
					fmt.Printf("  Schedule:         %02d:00-%02d:00 %s\n", c.Schedule.StartHour, c.Schedule.EndHour, tz)
				}
				if c.UseHostAPIKeys != nil {
					fmt.Printf("  Use host keys:    %v\n", *c.UseHostAPIKeys)
				}
				if preset := guestResourcePreset(&c); preset != "" {
					fmt.Printf("  Resource preset:  %s\n", preset)
				}
				if c.AllowGuestProvidedAPIKeys != nil {
					fmt.Printf("  Guest keys:       %v\n", *c.AllowGuestProvidedAPIKeys)
				}
				if c.AllowDesktopControl != nil {
					fmt.Printf("  Desktop control:  %v\n", *c.AllowDesktopControl)
				}
				if c.AllowBrowserControl != nil {
					fmt.Printf("  Browser control:  %v\n", *c.AllowBrowserControl)
				}
				if c.AllowTunnelForward != nil {
					fmt.Printf("  Tunnel forward:   %v\n", *c.AllowTunnelForward)
				}
				if c.RequireIsolation != nil {
					fmt.Printf("  Docker isolation: %v\n", *c.RequireIsolation)
				}
				if c.CPULimitPercent != nil {
					fmt.Printf("  CPU cap:          %d%%\n", *c.CPULimitPercent)
				}
				if c.RAMLimitMB != nil {
					fmt.Printf("  RAM cap:          %d MB\n", *c.RAMLimitMB)
				}
				if c.PriorityMode != "" {
					fmt.Printf("  Priority mode:    %s\n", c.PriorityMode)
				}
				if c.ShareAllDevices != nil {
					fmt.Printf("  Share all devices:%v\n", *c.ShareAllDevices)
				}
				if len(c.DeviceIDs) > 0 {
					fmt.Printf("  Device scope:     %s\n", strings.Join(c.DeviceIDs, ", "))
				}
				if c.ShareAllMachines != nil {
					fmt.Printf("  Share all machines:%v\n", *c.ShareAllMachines)
				}
				return
			}
		}
		fmt.Printf("No config found for %s\n", email)
		return
	}

	// Set config: yaver guests config <email> limit=3600 mode=scheduled runners=claude,aider hostkeys=true
	payload := map[string]interface{}{"email": email}
	for _, kv := range args[1:] {
		parts := splitKV(kv)
		if parts == nil {
			fmt.Fprintf(os.Stderr, "Invalid key=value: %s\n", kv)
			os.Exit(1)
		}
		switch parts[0] {
		case "limit":
			var v int
			fmt.Sscanf(parts[1], "%d", &v)
			payload["dailyTokenLimit"] = v
		case "mode":
			payload["usageMode"] = parts[1]
		case "runners":
			runners := splitComma(parts[1])
			payload["allowedRunners"] = runners
		case "hostkeys":
			payload["useHostApiKeys"] = parseBoolish(parts[1])
		case "preset":
			payload["resourcePreset"] = parts[1]
		case "guestkeys":
			payload["allowGuestProvidedApiKeys"] = parseBoolish(parts[1])
		case "desktop":
			payload["allowDesktopControl"] = parseBoolish(parts[1])
		case "browser":
			payload["allowBrowserControl"] = parseBoolish(parts[1])
		case "tunnels":
			payload["allowTunnelForward"] = parseBoolish(parts[1])
		case "isolation", "docker":
			payload["requireIsolation"] = parseBoolish(parts[1])
		case "cpu":
			var v int
			fmt.Sscanf(parts[1], "%d", &v)
			payload["cpuLimitPercent"] = v
		case "rammb":
			var v int
			fmt.Sscanf(parts[1], "%d", &v)
			payload["ramLimitMb"] = v
		case "priority":
			payload["priorityMode"] = parts[1]
		case "devices":
			if parts[1] == "all" {
				payload["shareAllDevices"] = true
				payload["deviceIds"] = []string{}
			} else {
				payload["shareAllDevices"] = false
				payload["deviceIds"] = splitComma(parts[1])
			}
		case "machines":
			if parts[1] == "all" {
				payload["shareAllMachines"] = true
				payload["machineIds"] = []string{}
			} else {
				payload["shareAllMachines"] = false
				payload["machineIds"] = splitComma(parts[1])
			}
		default:
			fmt.Fprintf(os.Stderr, "Unknown config key: %s (use: limit, mode, runners, preset, hostkeys, guestkeys, desktop, browser, tunnels, isolation, cpu, rammb, priority, devices, machines)\n", parts[0])
			os.Exit(1)
		}
	}

	if err := UpdateGuestConfig(convexURL, cfg.AuthToken, payload); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config updated for %s\n", email)
}

func parseBoolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "allow", "enabled":
		return true
	default:
		return false
	}
}

func runGuestsUsage(args []string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not signed in. Run 'yaver auth' first.\n")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	date := ""
	if len(args) > 0 {
		date = args[0]
	}

	usage, err := FetchGuestUsage(convexURL, cfg.AuthToken, date)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch usage: %v\n", err)
		os.Exit(1)
	}

	if len(usage) == 0 {
		if date != "" {
			fmt.Printf("No usage for %s\n", date)
		} else {
			fmt.Println("No usage today.")
		}
		return
	}

	fmt.Printf("%-30s  %-20s  %s\n", "GUEST", "NAME", "SECONDS")
	fmt.Printf("%-30s  %-20s  %s\n", "-----", "----", "-------")
	for _, u := range usage {
		fmt.Printf("%-30s  %-20s  %.0f\n", u.GuestEmail, u.GuestName, u.SecondsUsed)
	}
}

func splitKV(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return nil
}

func printGuestsUsage() {
	fmt.Println(`Usage: yaver guests <command>

Manage guest access to your machine. Guests can connect to your agent
from their Yaver mobile app to run tasks, but cannot access shell,
vault, sessions, or terminals.

Commands:
  invite <email>                          Invite by email (host side, default scope=feedback-only)
  invite <email> --scope=full             Invite a teammate with classic full scope (tasks, vibing, dev, builds, projects)
  invite <email> --scope=feedback-only    Hardened end-user scope: feedback / blackbox / voice only, /info redacted, fix-tasks force-containerized
  invite --user-id <id> [--machines ids]  Invite by public user id
  invite <email|id> --machines <id1,id2>  Propose a limited machine scope
  accept <code> [--machines id1,id2]      Accept a pending invite (guest side)
  list                                    List all guests and their status
  remove <email>                          Revoke guest access
  unshare-machine <email> <machine-id>  Stop sharing one machine with a guest
  hostkeys <email> <on|off>  Toggle host-managed keys for a guest
  config            Show all guest configs
  config <email>    Show config for a specific guest
  config <email> key=value ...   Set config (limit, mode, runners)
  usage [date]      Show guest usage (today or specific date)

Config keys:
  limit=<seconds>          Daily task-seconds limit (0 = unlimited)
  mode=<always|idle-only|scheduled>   When guests can use the machine
  runners=<r1,r2,...>      Allowed runners (empty = all)
  hostkeys=<true|false>    Allow host-managed API keys for this guest
  guestkeys=<true|false>   Allow guest-provided API keys on shared infra
  isolation=<true|false>   Require Docker isolation for this guest's tasks
  cpu=<1-100>              CPU share cap percent for guest tasks
  rammb=<mb>               RAM cap in MB for guest tasks
  priority=<mode>          same-priority | spare-capacity | background
  devices=<all|id1,id2>    Shared device scope
  machines=<all|id1,id2>   Shared cloud machine scope

Examples:
  yaver guests invite cousin@gmail.com
  yaver guests config cousin@gmail.com limit=3600 mode=scheduled
  yaver guests config cousin@gmail.com runners=claude,aider
  yaver guests config cousin@gmail.com hostkeys=false isolation=true cpu=50 rammb=4096
  yaver guests unshare-machine cousin@gmail.com mac-mini-01
  yaver guests hostkeys cousin@gmail.com off
  yaver guests usage
  yaver guests usage 2026-04-06
  yaver guests list
  yaver guests remove cousin@gmail.com`)
}
