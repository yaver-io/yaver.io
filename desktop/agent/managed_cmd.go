package main

// managed_cmd.go — `yaver managed` CLI subcommand.
//
// Usage:
//   yaver managed                      # list every subsystem + current value
//   yaver managed get                  # same as bare invocation
//   yaver managed set relay true       # use Yaver-managed relay
//   yaver managed set dns false        # self-hosted DNS (user's Cloudflare/etc.)
//   yaver managed set analytics null   # clear the preference (revert to default)
//
// The CLI is a thin wrapper around fetchManagedSettings /
// setManagedSubsystem so the CLI and MCP call sites stay in lockstep.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// managedLoadAuth is the local auth helper so we don't tangle with
// the ops/primary CLIs. Returns (token, convex site URL, err).
func managedLoadAuth() (string, string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex := cfg.ConvexSiteURL
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	return cfg.AuthToken, convex, nil
}

func runManaged(args []string) {
	if len(args) == 0 {
		runManagedGet()
		return
	}
	switch args[0] {
	case "get", "list", "ls":
		runManagedGet()
	case "set":
		runManagedSet(args[1:])
	case "help", "-h", "--help":
		managedUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver managed %s\n\n", args[0])
		managedUsage()
		os.Exit(1)
	}
}

func managedUsage() {
	fmt.Print(`yaver managed — per-subsystem Yaver-managed vs self-hosted toggle.

Usage:
  yaver managed                      Show every subsystem + current value
  yaver managed get                  Same as bare invocation
  yaver managed set <subsystem> <true|false|null>
                                     true  = use Yaver-hosted infra
                                     false = user-hosted (your own CF/VPS/etc.)
                                     null  = clear; revert to subsystem default

Subsystems: relay, dns, analytics, storage, email, ci, voice, llm.

Setting a value here updates Convex userSettings — mobile, web, CLI,
and MCP surfaces all honour the same choice on next read.
`)
}

func runManagedGet() {
	token, convex, err := managedLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, err := fetchManagedSettings(context.Background(), convex, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	// Pretty-print the result. fetchManagedSettings returns a blob
	// shaped {managed, subsystems, hint}. Render it as a small table
	// so the CLI output is immediately readable.
	var parsed struct {
		Managed    map[string]interface{} `json:"managed"`
		Subsystems []string               `json:"subsystems"`
		Hint       string                 `json:"hint"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Println("Per-subsystem managed toggle:")
	for _, sub := range parsed.Subsystems {
		val, ok := parsed.Managed[sub]
		label := "(unset)"
		if ok {
			switch v := val.(type) {
			case bool:
				if v {
					label = "managed (Yaver-hosted)"
				} else {
					label = "self-hosted"
				}
			default:
				label = fmt.Sprintf("%v", val)
			}
		}
		fmt.Printf("  %-10s  %s\n", sub, label)
	}
	if parsed.Hint != "" {
		fmt.Println()
		fmt.Println(parsed.Hint)
	}
}

func runManagedSet(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: yaver managed set <subsystem> <true|false|null>")
		os.Exit(1)
	}
	subsystem := args[0]
	raw := strings.ToLower(strings.TrimSpace(args[1]))
	var valueJSON json.RawMessage
	switch raw {
	case "true", "yes", "on", "managed":
		valueJSON = json.RawMessage("true")
	case "false", "no", "off", "self", "self-hosted", "selfhosted":
		valueJSON = json.RawMessage("false")
	case "null", "clear", "unset", "":
		valueJSON = json.RawMessage("null")
	default:
		fmt.Fprintf(os.Stderr, "Error: value must be true, false, or null (got %q)\n", args[1])
		os.Exit(1)
	}
	token, convex, err := managedLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := setManagedSubsystem(context.Background(), convex, token, subsystem, valueJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("managed.%s = %s\n", subsystem, string(valueJSON))
}
