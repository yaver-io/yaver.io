package main

// screenlog_cmd.go — `yaver screenlog` CLI. A thin client over the local
// daemon's /screenlog/* HTTP surface (the capture loop must live in the
// long-running agent, not the CLI process). Local-only by construction —
// the daemon never ships frames anywhere.
//
//   yaver screenlog drivers                 # can this host capture?
//   yaver screenlog start [flags]           # begin recording
//   yaver screenlog status                  # live counters
//   yaver screenlog stop                    # finish
//   yaver screenlog list                    # past sessions
//   yaver screenlog open [<id>]             # print the local viewer URL

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// screenlogPull downloads a session's tar.gz export from the local agent
// to dst. For a REMOTE machine, attach first (`yaver code --attach`) or run
// it where that agent is reachable — the export URL is the contract.
func screenlogPull(id, dst string) error {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	req, err := http.NewRequest("GET", localAgentBaseURL()+"/screenlog/"+id+"/export", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// runScreenlogLocal runs the capture loop IN THIS PROCESS — no `yaver
// serve` daemon required. This is the "works without the agent" path: a
// thin standalone recorder writing to the same ~/.yaver/screenlog/ files
// that every other surface reads. Runs until Ctrl-C, then finalizes.
func runScreenlogLocal(title string, cfgMap map[string]interface{}) {
	var cfg ScreenlogConfig
	raw, _ := json.Marshal(cfgMap)
	_ = json.Unmarshal(raw, &cfg)
	sess, err := startScreenlog(cfg, title)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("recording locally → %s  (Ctrl-C to stop)\n", sess.ID)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	out, err := stopScreenlog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stop error:", err)
		os.Exit(1)
	}
	fmt.Printf("stopped %s — %d frames in ~/.yaver/screenlog/%s\n", out.ID, len(out.Frames), out.ID)
}

func runScreenlog(args []string) {
	if len(args) == 0 {
		screenlogUsage()
		return
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "drivers", "doctor":
		res, err := localAgentRequest("GET", "/screenlog/drivers", nil)
		printScreenlogResult(res, err)
	case "install-deps":
		msg, err := installScreenlogDeps(true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(msg)
	case "start":
		fs := flag.NewFlagSet("screenlog start", flag.ExitOnError)
		title := fs.String("title", "", "session title")
		interval := fs.Int("interval", 2, "seconds between captures")
		format := fs.String("format", "jpg", "frame format: png|jpg")
		maxWidth := fs.Int("max-width", 1920, "downscale cap in px (0 = full res)")
		displays := fs.String("displays", "all", "all|primary")
		maxDisk := fs.Int("max-disk-mb", 4096, "disk-budget ring buffer in MB")
		retention := fs.Int("retention-days", 7, "prune sessions older than N days")
		dedup := fs.Bool("dedup", true, "perceptual de-dup of unchanged frames")
		tagWindow := fs.Bool("tag-window", true, "tag frames with active app/window")
		wslTarget := fs.String("wsl-target", "auto", "WSL only: auto|host|wslg")
		ephemeral := fs.Bool("ephemeral", false, "temporary screenshots: keep only the trace, discard images")
		captureInput := fs.Bool("capture-input", false, "also record keystroke/mouse events (needs allow-input)")
		allowRawText := fs.Bool("allow-raw-text", false, "store typed characters verbatim (default: redact)")
		local := fs.Bool("local", false, "run the capture loop in THIS process (no agent/daemon needed)")
		_ = fs.Parse(rest)

		cfg := map[string]interface{}{
			"intervalSec":     *interval,
			"format":          *format,
			"maxWidth":        *maxWidth,
			"displays":        *displays,
			"maxDiskMB":       *maxDisk,
			"retentionDays":   *retention,
			"dedup":           *dedup,
			"tagWindow":       *tagWindow,
			"wslTarget":       *wslTarget,
			"ephemeralFrames": *ephemeral,
			"captureInput":    *captureInput,
			"allowRawText":    *allowRawText,
		}
		if *local {
			runScreenlogLocal(*title, cfg)
			return
		}
		res, err := localAgentRequest("POST", "/screenlog/start", map[string]interface{}{
			"title": *title, "config": cfg,
		})
		printScreenlogResult(res, err)
	case "stop":
		res, err := localAgentRequest("POST", "/screenlog/stop", nil)
		printScreenlogResult(res, err)
	case "status":
		res, err := localAgentRequest("GET", "/screenlog/status", nil)
		printScreenlogResult(res, err)
	case "list":
		res, err := localAgentRequest("GET", "/screenlog/list", nil)
		printScreenlogResult(res, err)
	case "analyze":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: yaver screenlog analyze <id>")
			os.Exit(1)
		}
		res, err := localAgentRequest("GET", "/screenlog/analyze?id="+rest[0], nil)
		printScreenlogResult(res, err)
	case "emulate":
		fs := flag.NewFlagSet("screenlog emulate", flag.ExitOnError)
		scale := fs.Int("scale-seconds", 1, "seconds per scenario-minute (1 = fast headless demo)")
		input := fs.Bool("input", true, "also emit synthetic keystroke/mouse events")
		_ = fs.Parse(rest)
		cfg := defaultScreenlogConfig()
		cfg.CaptureInput = *input
		sess, err := runScreenlogEmulation("emulated session", defaultEmulationScenario(*scale), cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		rep, _, _ := analyzeScreenlogSession(sess.ID, 0, 0)
		top := ""
		if rep != nil && len(rep.ByCategory) > 0 {
			top = rep.ByCategory[0].Name
		}
		fmt.Printf("emulated %s — %d frames, top activity: %s\n", sess.ID, len(sess.Frames), top)
		if rep != nil {
			for i, cstat := range rep.ByCategory {
				if i >= 6 {
					break
				}
				fmt.Printf("  %-10s %5ds  %.1f%%\n", cstat.Name, cstat.Seconds, cstat.Percent)
			}
		}
		fmt.Printf("view: yaver screenlog open %s  ·  pull: yaver screenlog pull %s\n", sess.ID, sess.ID)
	case "policy":
		res, err := localAgentRequest("GET", "/screenlog/policy", nil)
		printScreenlogResult(res, err)
	case "audit":
		res, err := localAgentRequest("GET", "/screenlog/audit", nil)
		printScreenlogResult(res, err)
	case "enable", "disable":
		on := sub == "enable"
		res, err := localAgentRequest("POST", "/screenlog/policy", map[string]interface{}{"enabled": on})
		printScreenlogResult(res, err)
	case "allow-remote", "deny-remote":
		on := sub == "allow-remote"
		res, err := localAgentRequest("POST", "/screenlog/policy", map[string]interface{}{"allowRemoteControl": on})
		printScreenlogResult(res, err)
	case "allow-input", "deny-input":
		on := sub == "allow-input"
		res, err := localAgentRequest("POST", "/screenlog/policy", map[string]interface{}{"allowInputCapture": on})
		printScreenlogResult(res, err)
	case "events":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: yaver screenlog events <id>")
			os.Exit(1)
		}
		res, err := localAgentRequest("GET", "/screenlog/"+rest[0]+"/events", nil)
		printScreenlogResult(res, err)
	case "pull":
		fs := flag.NewFlagSet("screenlog pull", flag.ExitOnError)
		out := fs.String("out", "", "output .tar.gz path (default <id>.tar.gz)")
		_ = fs.Parse(rest)
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "usage: yaver screenlog pull <id> [--out file.tar.gz]")
			os.Exit(1)
		}
		id := fs.Arg(0)
		dst := *out
		if dst == "" {
			dst = id + ".tar.gz"
		}
		if err := screenlogPull(id, dst); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("pulled %s → %s\n", id, dst)
	case "allow-peer", "revoke-peer":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: yaver screenlog "+sub+" <peerId>")
			os.Exit(1)
		}
		key := "allowPeer"
		if sub == "revoke-peer" {
			key = "revokePeer"
		}
		res, err := localAgentRequest("POST", "/screenlog/policy", map[string]interface{}{key: rest[0]})
		printScreenlogResult(res, err)
	case "open":
		id := ""
		if len(rest) > 0 {
			id = rest[0]
		}
		if id == "" {
			res, err := localAgentRequest("GET", "/screenlog/status", nil)
			if err == nil {
				if st, ok := res["status"].(map[string]interface{}); ok {
					if v, ok := st["id"].(string); ok {
						id = v
					}
				}
			}
		}
		if id == "" {
			fmt.Fprintln(os.Stderr, "no session id (pass one, or start a recording first)")
			os.Exit(1)
		}
		fmt.Printf("%s/screenlog/%s\n", localAgentBaseURL(), id)
	default:
		screenlogUsage()
	}
}

func printScreenlogResult(res map[string]interface{}, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	prettyPrintJSONObject(res)
}

func screenlogUsage() {
	fmt.Println(`yaver screenlog — local-only screen-frame black box (Windows/macOS/Linux/WSL)

  yaver screenlog drivers                 check whether capture works here
  yaver screenlog install-deps            install the OS screen-capture dependency (Linux: scrot)
  yaver screenlog start [flags]           begin a local recording
  yaver screenlog status                  live counters
  yaver screenlog stop                    finish
  yaver screenlog list                    past sessions
  yaver screenlog analyze <id>            time-by-app report ("what did it spend time on")
  yaver screenlog open [<id>]             print the local viewer URL

  yaver screenlog start --local           record in THIS process (no daemon)
  yaver screenlog events <id>             keystroke/mouse companion stream + stats
  yaver screenlog pull <id> [--out f]     download a session (frames+events) as .tar.gz
  yaver screenlog emulate [--scale-seconds N]  headless demo session (no display/hardware)

permissions (on the RECORDED machine):
  yaver screenlog policy                  show consent policy
  yaver screenlog enable | disable        master kill-switch
  yaver screenlog allow-remote|deny-remote   gate non-local start/stop
  yaver screenlog allow-input|deny-input     gate keystroke/mouse capture
  yaver screenlog allow-peer|revoke-peer <id>   grant/remove a mesh peer
  yaver screenlog audit                   who started recording, when

start flags:
  --title, --interval, --format png|jpg, --max-width, --displays all|primary,
  --max-disk-mb, --retention-days, --dedup, --tag-window, --wsl-target auto|host|wslg,
  --ephemeral (keep only the trace, discard images), --capture-input, --allow-raw-text,
  --local (run in this process, no daemon)

Frames live under ~/.yaver/screenlog/ and never leave this machine.`)
}
