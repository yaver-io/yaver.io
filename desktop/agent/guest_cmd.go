package main

import (
	"fmt"
	"os"
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
	case "list", "ls":
		runGuestsList()
	case "remove", "revoke", "rm":
		runGuestsRemove(args[1:])
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
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver guests invite <email>")
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

	result, err := InviteGuest(convexURL, cfg.AuthToken, email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to invite: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Invitation sent to %s\n", email)
	fmt.Printf("Invite code: %s\n", result.InviteCode)
	fmt.Println()
	if result.GuestRegistered {
		fmt.Println("This email is already registered on Yaver.")
		fmt.Println("They'll see the invitation in the Yaver app automatically.")
	} else {
		fmt.Println("This email is not yet registered on Yaver.")
		fmt.Println("Tell them to:")
		fmt.Println("  1. Download the Yaver app")
		fmt.Println("  2. Sign in with any method (Apple, Google, Microsoft, or email)")
		fmt.Println("  3. Enter the invite code above (or sign in with the invited email)")
	}
	fmt.Println()
	fmt.Println("The invitation expires in 2 days.")
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
				return
			}
		}
		fmt.Printf("No config found for %s\n", email)
		return
	}

	// Set config: yaver guests config <email> limit=3600 mode=scheduled runners=claude,aider
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
		default:
			fmt.Fprintf(os.Stderr, "Unknown config key: %s (use: limit, mode, runners)\n", parts[0])
			os.Exit(1)
		}
	}

	if err := UpdateGuestConfig(convexURL, cfg.AuthToken, payload); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config updated for %s\n", email)
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
  invite <email>    Invite a guest (max 5, expires in 2 days if not accepted)
  list              List all guests and their status
  remove <email>    Revoke guest access
  config            Show all guest configs
  config <email>    Show config for a specific guest
  config <email> key=value ...   Set config (limit, mode, runners)
  usage [date]      Show guest usage (today or specific date)

Config keys:
  limit=<seconds>          Daily task-seconds limit (0 = unlimited)
  mode=<always|idle-only|scheduled>   When guests can use the machine
  runners=<r1,r2,...>      Allowed runners (empty = all)

Examples:
  yaver guests invite cousin@gmail.com
  yaver guests config cousin@gmail.com limit=3600 mode=scheduled
  yaver guests config cousin@gmail.com runners=claude,aider
  yaver guests usage
  yaver guests usage 2026-04-06
  yaver guests list
  yaver guests remove cousin@gmail.com`)
}
