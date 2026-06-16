package main

// appletv_cmd.go — `yaver appletv …` CLI. Thin front door over the appletv.go
// engine + capture.go streamer (same code the ops verbs call). Hand-rolled
// subcommand switch, matching vault_cmd.go (this repo has no Cobra).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func printAppleTVUsage() {
	fmt.Println(`yaver appletv — control an Apple TV from this box (pyatv over LAN)

  yaver appletv scan                     discover Apple TVs on the LAN
  yaver appletv pair <identifier>        PIN-pair and store credentials in the vault
  yaver appletv list                     list paired Apple TVs
  yaver appletv key <name>               up|down|left|right|select|menu|home|play|pause|...
  yaver appletv app <bundle-id>          launch an app
  yaver appletv now-playing              print current metadata (JSON)
  yaver appletv power <on|off>
  yaver appletv transport <play|pause|stop|next|previous|play_pause>
  yaver appletv seek <seconds>

  yaver appletv capture devices          list capture-card devices + ffmpeg status
  yaver appletv capture start [dev] [fps] start the capture-card MJPEG stream
  yaver appletv capture stop
  yaver appletv capture status

Use --device <identifier|name> on control commands to target a non-default TV.
Capture streams YOUR OWN non-protected sources only (HDCP input is reported, not streamed).`)
}

func runAppleTV(args []string) {
	if len(args) == 0 {
		printAppleTVUsage()
		os.Exit(0)
	}
	// Pull an optional --device <ref> out of args.
	device := ""
	rest := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--device" && i+1 < len(args) {
			device = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	args = rest
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	switch args[0] {
	case "scan":
		out, err := appleTVEng.Scan(ctx)
		atvPrint(out, err)
	case "pair":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver appletv pair <identifier>")
			os.Exit(1)
		}
		runAppleTVPair(ctx, args[1])
	case "list":
		devs, err := appletvListDevices()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(devs) == 0 {
			fmt.Println("no paired Apple TVs — run `yaver appletv scan` then `yaver appletv pair <identifier>`")
			return
		}
		for _, d := range devs {
			def := ""
			if d.Default {
				def = " (default)"
			}
			fmt.Printf("%-40s %s  [%s]%s\n", d.Identifier, d.Name, d.Address, def)
		}
	case "key":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver appletv key <name>")
			os.Exit(1)
		}
		out, err := appleTVEng.RemoteKey(ctx, device, args[1])
		atvPrint(out, err)
	case "app":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver appletv app <bundle-id>")
			os.Exit(1)
		}
		out, err := appleTVEng.LaunchApp(ctx, device, args[1])
		atvPrint(out, err)
	case "now-playing":
		out, err := appleTVEng.NowPlaying(ctx, device)
		atvPrint(out, err)
	case "power":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver appletv power <on|off>")
			os.Exit(1)
		}
		out, err := appleTVEng.Power(ctx, device, args[1])
		atvPrint(out, err)
	case "transport":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver appletv transport <play|pause|stop|next|previous>")
			os.Exit(1)
		}
		out, err := appleTVEng.Transport(ctx, device, args[1])
		atvPrint(out, err)
	case "seek":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver appletv seek <seconds>")
			os.Exit(1)
		}
		secs, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "seconds must be an integer")
			os.Exit(1)
		}
		out, err := appleTVEng.Seek(ctx, device, secs)
		atvPrint(out, err)
	case "capture":
		runAppleTVCapture(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown appletv subcommand: %s\n", args[0])
		printAppleTVUsage()
		os.Exit(1)
	}
}

func runAppleTVPair(ctx context.Context, identifier string) {
	// Scan to learn name + address for the identifier.
	scan, err := appleTVEng.Scan(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan failed:", err)
		os.Exit(1)
	}
	var name, address string
	if devs, ok := scan["devices"].([]interface{}); ok {
		for _, dv := range devs {
			if m, ok := dv.(map[string]interface{}); ok {
				if fmt.Sprintf("%v", m["identifier"]) == identifier {
					name, _ = m["name"].(string)
					address, _ = m["address"].(string)
				}
			}
		}
	}
	if address == "" {
		fmt.Fprintf(os.Stderr, "identifier %q not found on the LAN; run `yaver appletv scan`\n", identifier)
		os.Exit(1)
	}
	begin, err := appleTVEng.call(ctx, "/pair_begin", map[string]interface{}{"identifier": identifier})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pair begin failed:", err)
		os.Exit(1)
	}
	session, _ := begin["session"].(string)
	fmt.Print("Enter the PIN shown on the Apple TV: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	pin, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		fmt.Fprintln(os.Stderr, "PIN must be a number")
		os.Exit(1)
	}
	fin, err := appleTVEng.call(ctx, "/pair_finish", map[string]interface{}{"session": session, "pin": pin})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pairing failed:", err)
		os.Exit(1)
	}
	creds := map[string]string{}
	if raw, ok := fin["credentials"].(map[string]interface{}); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				creds[k] = s
			}
		}
	}
	d := appletvDevice{Identifier: identifier, Name: name, Address: address, Credentials: creds}
	if existing, _ := appletvListDevices(); len(existing) == 0 {
		d.Default = true
	}
	if err := appletvSaveDevice(d); err != nil {
		fmt.Fprintln(os.Stderr, "save to vault failed:", err)
		os.Exit(1)
	}
	fmt.Printf("paired %s (%s) — credentials stored in vault\n", name, identifier)
}

func runAppleTVCapture(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: yaver appletv capture <devices|start|stop|status> [device] [fps]")
		return
	}
	switch args[0] {
	case "devices":
		b, _ := json.MarshalIndent(map[string]interface{}{
			"devices": captureDevices(), "ffmpeg": ffmpegPath() != "",
		}, "", "  ")
		fmt.Println(string(b))
	case "start":
		dev := ""
		fps := 0
		if len(args) > 1 {
			dev = args[1]
		}
		if len(args) > 2 {
			fps, _ = strconv.Atoi(args[2])
		}
		if err := captureStream.start(dev, fps, 0, 0, 0); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(captureStream.status(), "", "  ")
		fmt.Println(string(b))
	case "stop":
		captureStream.stop()
		fmt.Println("capture stopped")
	case "status":
		b, _ := json.MarshalIndent(captureStream.status(), "", "  ")
		fmt.Println(string(b))
	default:
		fmt.Fprintf(os.Stderr, "unknown capture subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func atvPrint(out map[string]interface{}, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
