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

	if err := InviteGuest(convexURL, cfg.AuthToken, email); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to invite: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Invitation sent to %s\n", email)
	fmt.Println("They need to sign into the Yaver app and accept the invitation within 2 days.")
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

func printGuestsUsage() {
	fmt.Println(`Usage: yaver guests <command>

Manage guest access to your machine. Guests can connect to your agent
from their Yaver mobile app to run tasks, but cannot access shell,
vault, sessions, or terminals.

Commands:
  invite <email>    Invite a guest (max 5, expires in 2 days if not accepted)
  list              List all guests and their status
  remove <email>    Revoke guest access

Examples:
  yaver guests invite cousin@gmail.com
  yaver guests list
  yaver guests remove cousin@gmail.com`)
}
