package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func runSdkToken(args []string) {
	if len(args) == 0 {
		printSdkTokenUsage()
		return
	}

	switch args[0] {
	case "create":
		runSdkTokenCreate(args[1:])
	case "help", "--help", "-h":
		printSdkTokenUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown sdk-token subcommand: %s\n", args[0])
		printSdkTokenUsage()
		os.Exit(1)
	}
}

func runSdkTokenCreate(args []string) {
	fs := flag.NewFlagSet("sdk-token create", flag.ExitOnError)
	label := fs.String("label", "", "Human-readable label (e.g. 'AcmeStore dev')")
	scopes := fs.String("scopes", "", "Comma-separated scopes (default: feedback,blackbox,voice,builds)")
	allowedIPs := fs.String("allowed-ips", "", "Comma-separated CIDRs (e.g. 192.168.1.0/24)")
	expires := fs.String("expires", "", "Token lifetime (e.g. 24h, 7d, 30d). Default: 365d")
	fs.Parse(args)

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	if cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}

	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	opts := SdkTokenCreateOpts{Label: *label}

	if *scopes != "" {
		opts.Scopes = strings.Split(*scopes, ",")
	}
	if *allowedIPs != "" {
		opts.AllowedCIDRs = strings.Split(*allowedIPs, ",")
	}
	if *expires != "" {
		d, err := parseDuration(*expires)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid --expires value: %v\n", err)
			os.Exit(1)
		}
		opts.ExpiresInMs = d.Milliseconds()
	}

	sdkToken, err := CreateSdkToken(convexURL, cfg.AuthToken, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating SDK token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(sdkToken)
}

// parseDuration parses durations like "24h", "7d", "30d".
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(s, "%d", &days); err != nil {
			return 0, fmt.Errorf("invalid day count: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func printSdkTokenUsage() {
	fmt.Print(`Manage SDK tokens for the Yaver Feedback SDK.

SDK tokens are independent from your CLI session — CLI reauth does not
invalidate them. Tokens are scoped to specific endpoints and can be
restricted to certain IP ranges.

Usage:
  yaver sdk-token create [flags]    Create a new SDK token

Flags:
  --label "name"           Human-readable label
  --scopes "a,b,c"         Allowed scopes (default: feedback,blackbox,voice,builds)
  --allowed-ips "cidr,..."  Restrict to these IP ranges (e.g. 192.168.1.0/24)
  --expires "duration"     Token lifetime: 24h, 7d, 30d (default: 365d)

Examples:
  yaver sdk-token create --label "AcmeStore dev"
  yaver sdk-token create --label "CI" --expires 24h --allowed-ips 10.0.0.0/8
  yaver sdk-token create --scopes feedback,blackbox --allowed-ips 192.168.1.0/24

`)
}
