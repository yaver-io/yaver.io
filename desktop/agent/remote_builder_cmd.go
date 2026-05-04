package main

// remote_builder_cmd.go — `yaver builder {add,list,use,forget,
// ping,default}` CLI surface for managing paired remote-mac
// builders. See remote_builder.go for the on-disk model.

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func runBuilder(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printBuilderUsage()
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "add":
		runBuilderAdd(args[1:])
	case "list", "ls":
		runBuilderList(args[1:])
	case "use", "default":
		runBuilderUse(args[1:])
	case "forget", "remove", "rm":
		runBuilderForget(args[1:])
	case "ping":
		runBuilderPing(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "yaver builder: unknown subcommand %q\n\n", args[0])
		printBuilderUsage()
		os.Exit(2)
	}
}

func printBuilderUsage() {
	fmt.Println("usage: yaver builder <command>")
	fmt.Println()
	fmt.Println("Manage paired remote-mac builders for iOS / Swift sessions on")
	fmt.Println("non-darwin hosts. The current box dispatches build + sim runs")
	fmt.Println("to a paired Mac and streams the simulator UI back over WebRTC.")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  add [--token=...] [--platforms=ios,...] <alias> <url>   Pair a builder")
	fmt.Println("  list [--no-ping]                                        Show paired builders + reachability")
	fmt.Println("  use <alias>                                             Set the default builder")
	fmt.Println("  forget <alias>                                          Remove a builder")
	fmt.Println("  ping <alias>                                            Health-check a builder")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver builder add --token=xxx mac-rack-1 http://10.0.0.5:18080")
	fmt.Println("  yaver builder add --platforms=ios,macos mac-rack-1 http://10.0.0.5:18080")
	fmt.Println("  yaver builder list")
	fmt.Println("  yaver builder use mac-rack-1")
	fmt.Println("  yaver builder ping mac-rack-1")
	fmt.Println()
	fmt.Println("Tokens are read from --token=... or YAVER_BUILDER_TOKEN.")
	fmt.Println("Stored under ~/.yaver/builders.json (mode 0600).")
}

func runBuilderAdd(args []string) {
	fs := flag.NewFlagSet("builder add", flag.ExitOnError)
	tokenFlag := fs.String("token", "", "auth token for the builder (env: YAVER_BUILDER_TOKEN)")
	platformsFlag := fs.String("platforms", "ios", "comma-separated platforms the builder serves (e.g. ios,macos)")
	noteFlag := fs.String("note", "", "free-form description shown in `builder list`")
	fs.Parse(args)

	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver builder add [--token=...] [--platforms=...] <alias> <url>")
		fmt.Fprintln(os.Stderr, "  flags must appear BEFORE the alias + url positionals")
		os.Exit(2)
	}
	alias := fs.Arg(0)
	url := fs.Arg(1)

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("YAVER_BUILDER_TOKEN"))
	}

	reg, err := LoadBuilders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load builders: %v\n", err)
		os.Exit(1)
	}
	entry := BuilderEntry{
		Alias:     alias,
		URL:       url,
		Token:     token,
		Platforms: splitCSV(*platformsFlag),
		Note:      strings.TrimSpace(*noteFlag),
	}
	if err := reg.AddBuilder(entry); err != nil {
		fmt.Fprintf(os.Stderr, "add builder: %v\n", err)
		os.Exit(1)
	}
	if err := SaveBuilders(reg); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Paired %s → %s (platforms=%s)\n", alias, url, strings.Join(entry.Platforms, ","))
	if reg.Default == alias {
		fmt.Printf("  set as default builder\n")
	}
	fmt.Printf("  next: `yaver builder ping %s` to confirm reachability\n", alias)
}

func runBuilderList(args []string) {
	fs := flag.NewFlagSet("builder list", flag.ExitOnError)
	noPing := fs.Bool("no-ping", false, "skip the reachability probe")
	fs.Parse(args)

	reg, err := LoadBuilders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load builders: %v\n", err)
		os.Exit(1)
	}
	aliases := reg.SortedAliases()
	if len(aliases) == 0 {
		fmt.Println("No builders paired.")
		fmt.Println("Pair one with `yaver builder add <alias> <url>`.")
		return
	}

	client := &http.Client{Timeout: 3 * 1000 * 1000 * 1000} // 3s
	fmt.Println("ALIAS                URL                                          PLATFORMS       STATUS")
	for _, a := range aliases {
		entry := reg.Builders[a]
		mark := " "
		if reg.Default == a {
			mark = "*"
		}
		status := "?"
		if !*noPing {
			info, err := PingBuilder(client, entry)
			switch {
			case err != nil:
				status = "✗ " + truncateBuilderField(err.Error(), 40)
			case info != nil && info.IsBuilder:
				if len(info.Platforms) > 0 {
					status = "✓ " + strings.Join(info.Platforms, ",")
				} else {
					status = "✓"
				}
			default:
				status = "? not flagged as builder"
			}
		}
		fmt.Printf("%s %-20s %-44s %-15s %s\n",
			mark,
			truncateBuilderField(entry.Alias, 20),
			truncateBuilderField(entry.URL, 44),
			truncateBuilderField(strings.Join(entry.Platforms, ","), 15),
			status,
		)
	}
	fmt.Println()
	fmt.Println("(* = default; use `yaver builder use <alias>` to switch)")
}

func runBuilderUse(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver builder use <alias>")
		os.Exit(2)
	}
	reg, err := LoadBuilders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load builders: %v\n", err)
		os.Exit(1)
	}
	if err := reg.SetDefault(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := SaveBuilders(reg); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Default builder set to %s\n", args[0])
}

func runBuilderForget(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver builder forget <alias>")
		os.Exit(2)
	}
	reg, err := LoadBuilders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load builders: %v\n", err)
		os.Exit(1)
	}
	if !reg.Forget(args[0]) {
		fmt.Fprintf(os.Stderr, "no paired builder with alias %q\n", args[0])
		os.Exit(1)
	}
	if err := SaveBuilders(reg); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Forgot %s\n", args[0])
	if reg.Default != "" {
		fmt.Printf("  default is now %s\n", reg.Default)
	}
}

func runBuilderPing(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver builder ping <alias>")
		os.Exit(2)
	}
	reg, err := LoadBuilders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load builders: %v\n", err)
		os.Exit(1)
	}
	entry, ok := reg.Builders[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "no paired builder with alias %q\n", args[0])
		os.Exit(1)
	}
	info, err := PingBuilder(nil, entry)
	if err != nil {
		fmt.Printf("✗ %s — %v\n", entry.URL, err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s\n", entry.URL)
	if info.Hostname != "" {
		fmt.Printf("  hostname:  %s\n", info.Hostname)
	}
	if info.Version != "" {
		fmt.Printf("  version:   %s\n", info.Version)
	}
	fmt.Printf("  isBuilder: %v\n", info.IsBuilder)
	if len(info.Platforms) > 0 {
		fmt.Printf("  platforms: %s\n", strings.Join(info.Platforms, ","))
	}
	if info.Note != "" {
		fmt.Printf("  note:      %s\n", info.Note)
	}
}

// truncate clips s at n runes, adding an ellipsis when it had to
// trim. Used for `builder list` column formatting; we do it
// rune-by-rune so multi-byte aliases stay terminal-friendly.
func truncateBuilderField(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}
