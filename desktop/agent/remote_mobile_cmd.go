package main

// `yaver remote detect` and `yaver insert` — counterpart to `yaver wire`
// for phones that paired via QUIC relay (e.g. yaver-test-ephemeral).
//
//   yaver remote detect              list mobile sessions currently paired with
//                                    this agent (works the same on macOS, Linux,
//                                    or any host running `yaver serve`).
//
//   yaver insert <app> [deviceId]    send an "open_app" control message to the
//                                    phone. The mobile app receives it on its
//                                    /blackbox/command-stream subscription and
//                                    auto-navigates to the Hot Reload tab,
//                                    finds <app>, and triggers "Open in Yaver"
//                                    — same UI flow as a manual tap.
//                                    With no deviceId we broadcast to every
//                                    paired mobile (single-phone shortcut).

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func runRemote(args []string) {
	if len(args) == 0 {
		remoteUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "detect", "list", "ls", "phones":
		runRemoteDetect(rest)
	case "insert", "push":
		runRemoteInsert(rest)
	case "-h", "--help", "help":
		remoteUsage()
	default:
		fmt.Fprintf(os.Stderr, "yaver remote: unknown subcommand %q\n\n", sub)
		remoteUsage()
		os.Exit(2)
	}
}

func remoteUsage() {
	fmt.Println("yaver remote — paired-phone discovery + remote app insertion")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  detect                          list mobile devices currently paired with this agent")
	fmt.Println("  insert <app> [deviceId]         send open_app command to the phone (no deviceId = broadcast)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver remote detect")
	fmt.Println("  yaver insert sfmg                                 # one phone paired — broadcast")
	fmt.Println("  yaver insert sfmg 8C4A...                         # specific deviceId")
	fmt.Println()
	fmt.Println("Tip: when run on a remote box (yaver-test-ephemeral, GPU rig, ...) the CLI")
	fmt.Println("targets the local daemon there. The mobile must already be paired to that")
	fmt.Println("agent (via Yaver app's device list) for it to show up in `remote detect`.")
}

func runRemoteDetect(args []string) {
	fs := flag.NewFlagSet("remote detect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)

	resp, err := localAgentRequest("GET", "/mobile/sessions", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver remote detect: %v\n", err)
		os.Exit(1)
	}
	rawSessions, _ := resp["sessions"].([]interface{})

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(rawSessions)
		return
	}

	if len(rawSessions) == 0 {
		fmt.Println("No mobile devices paired with this agent yet.")
		fmt.Println()
		fmt.Println("  Pair from the Yaver mobile app: open the Devices tab, sign in with the")
		fmt.Println("  same account this agent is signed into, then tap this device.")
		return
	}

	fmt.Printf("%-44s  %-10s  %-22s  %s\n", "DEVICE ID", "PLATFORM", "APP", "EVENTS")
	fmt.Printf("%-44s  %-10s  %-22s  %s\n", "---------", "--------", "---", "------")
	for _, raw := range rawSessions {
		s, _ := raw.(map[string]interface{})
		if s == nil {
			continue
		}
		deviceID, _ := s["deviceId"].(string)
		platform, _ := s["platform"].(string)
		app, _ := s["appName"].(string)
		events := 0
		switch v := s["eventCount"].(type) {
		case float64:
			events = int(v)
		case int:
			events = v
		}
		if app == "" {
			app = "(yaver mobile)"
		}
		fmt.Printf("%-44s  %-10s  %-22s  %d\n", deviceID, platform, app, events)
	}
}

func runRemoteInsert(args []string) {
	fs := flag.NewFlagSet("remote insert", flag.ExitOnError)
	device := fs.String("device", "", "specific deviceId (alternative to second positional arg)")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "yaver insert: need an app name. e.g. `yaver insert sfmg`")
		os.Exit(2)
	}
	app := rest[0]
	deviceID := *device
	if len(rest) > 1 && deviceID == "" {
		deviceID = rest[1]
	}

	body := map[string]interface{}{"app": app}
	if deviceID != "" {
		body["deviceId"] = deviceID
	}
	resp, err := localAgentRequest("POST", "/mobile/insert", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver insert: %v\n", err)
		os.Exit(1)
	}

	target, _ := resp["target"].(string)
	switch target {
	case "broadcast":
		sentTo := 0
		switch v := resp["sentTo"].(type) {
		case float64:
			sentTo = int(v)
		case int:
			sentTo = v
		}
		fmt.Printf("✓ open_app(%s) broadcast to %d paired phone(s)\n", app, sentTo)
	case "device":
		dst, _ := resp["deviceId"].(string)
		fmt.Printf("✓ open_app(%s) sent to %s\n", app, dst)
	default:
		raw, _ := json.Marshal(resp)
		fmt.Println(string(raw))
	}
}
