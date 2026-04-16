package main

// ui_cmd.go — `yaver ui`. Opens the right control surface for this
// machine in the default browser.
//
// Default behavior (no flags): if a local agent is answering on
// http://127.0.0.1:18080/health, open the embedded console at
// /app/. Otherwise open the hosted dashboard at yaver.io/dashboard.
//
// Flags:
//   --local           force the embedded console
//   --hosted          force yaver.io/dashboard
//   --device <id>     pre-select a device in the hosted dashboard
//   --code <INVITE>   append a support-session code from `yaver support start`

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
	"time"
)

func runUI(args []string) {
	mode := "auto"
	device := ""
	code := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--local":
			mode = "local"
		case "--hosted":
			mode = "hosted"
		case "--device":
			if i+1 < len(args) {
				device = strings.TrimSpace(args[i+1])
				i++
			}
		case "--code":
			if i+1 < len(args) {
				code = strings.ToUpper(strings.TrimSpace(args[i+1]))
				i++
			}
		case "-h", "--help":
			printUIUsage()
			return
		}
	}

	// Fill device id from local config for --hosted if the caller
	// didn't supply one — otherwise the dashboard lands on "pick a
	// machine" and the user has to re-select every time.
	if device == "" {
		if cfg, err := LoadConfig(); err == nil && cfg != nil {
			device = cfg.DeviceID
		}
	}

	target := ""
	switch mode {
	case "local":
		target = buildLocalConsoleURL(code)
	case "hosted":
		target = buildHostedDashboardURL(device, code)
	default:
		if probeLocalAgentHealth() {
			target = buildLocalConsoleURL(code)
		} else {
			target = buildHostedDashboardURL(device, code)
		}
	}

	fmt.Printf("Opening %s\n", target)
	if err := openInBrowser(target); err != nil {
		fmt.Fprintf(os.Stderr, "open browser: %v\n", err)
		fmt.Fprintf(os.Stderr, "(try manually: %s)\n", target)
		os.Exit(1)
	}
}

func printUIUsage() {
	fmt.Println(`usage: yaver ui [--local|--hosted] [--device <id>] [--code <invite>]

Opens the Yaver control surface in your default browser.

  (default)       auto: local embedded console if this machine's agent
                  is responding, otherwise yaver.io/dashboard.
  --local         force http://127.0.0.1:18080/app/
  --hosted        force https://yaver.io/dashboard
  --device <id>   pre-select a device in the hosted dashboard
  --code <INVITE> include a support-session code (from 'yaver support start')`)
}

func probeLocalAgentHealth() bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:18080/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func buildLocalConsoleURL(code string) string {
	u := "http://127.0.0.1:18080/app/"
	if code != "" {
		u += "?support=" + url.QueryEscape(code)
	}
	return u
}

func buildHostedDashboardURL(device, code string) string {
	u := "https://yaver.io/dashboard"
	q := url.Values{}
	if device != "" {
		q.Set("device", device)
	}
	if code != "" {
		q.Set("support", code)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func openInBrowser(u string) error {
	var cmd *osexec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = osexec.Command("open", u)
	case "windows":
		cmd = osexec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		// Linux / BSD / WSL: prefer xdg-open, fall back to wslview
		// so `yaver ui` works inside WSL (a first-class host per
		// teamviewer.md).
		if _, err := osexec.LookPath("xdg-open"); err == nil {
			cmd = osexec.Command("xdg-open", u)
		} else if _, err := osexec.LookPath("wslview"); err == nil {
			cmd = osexec.Command("wslview", u)
		} else {
			fmt.Printf("(no browser opener found — open this URL manually: %s)\n", u)
			return nil
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
