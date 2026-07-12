package main

// git_oauth_cmd.go — `yaver git oauth <provider> [--device <id>]` CLI.
// Drives a GitHub/GitLab Device Flow either on the local agent or on
// an owned remote box (via the existing peer-proxy), prints the user
// code + verification URL, polls until the session reaches a terminal
// state.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func runGitOAuth(args []string) {
	if len(args) > 0 && args[0] == "status" {
		runGitOAuthStatus(args[1:])
		return
	}
	fs := flag.NewFlagSet("git oauth", flag.ExitOnError)
	var (
		host     string
		device   string
		openBrwr bool
		outJSON  bool
		timeout  int
	)
	fs.StringVar(&host, "host", "", "Provider host (defaults to github.com or gitlab.com)")
	fs.StringVar(&device, "device", "", "Owned remote device id/alias to run the flow on: BYO Hetzner, Yaver-managed cloud, or another paired runtime (default: this machine)")
	fs.BoolVar(&openBrwr, "open", false, "Try to open the verification URL in the local browser")
	fs.BoolVar(&outJSON, "json", false, "Emit a JSON summary instead of waiting interactively")
	fs.IntVar(&timeout, "timeout", 600, "Seconds to wait for the user to approve before giving up")
	if len(args) == 0 {
		fmt.Println("usage: yaver git oauth <github|gitlab> [--device <id>] [--host <host>] [--open]")
		fmt.Println("       yaver git oauth status [--device <id>] <session_id>")
		os.Exit(2)
	}
	provider := strings.ToLower(strings.TrimSpace(args[0]))
	if provider != "github" && provider != "gitlab" {
		fmt.Fprintf(os.Stderr, "git oauth: unknown provider %q (want github|gitlab)\n", provider)
		os.Exit(2)
	}
	_ = fs.Parse(args[1:])

	target := resolveGitOAuthDeviceFlag(device)

	startResp, err := callGitOAuthStart(provider, host, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git oauth: start failed: %v\n", err)
		os.Exit(1)
	}

	whichBox := "this machine"
	if target != "" {
		whichBox = shortDeviceID(target)
	}
	fmt.Printf("Open this URL on any device:\n\n  %s\n\nAnd enter this code:\n\n  %s\n\n", startResp.VerificationURI, startResp.UserCode)
	fmt.Printf("(Approval will save clone/push credentials and a local CI/deploy vault token on %s. Polling every %ds, gives up in %ds…)\n\n", whichBox, startResp.Interval, timeout)

	if openBrwr && target == "" {
		_ = openInBrowser(startResp.VerificationURI)
	}

	if outJSON {
		fmt.Println(jsonOrEmpty(startResp))
		return
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	interval := startResp.Interval
	if interval <= 0 {
		interval = 5
	}
	for {
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "git oauth: client-side timeout reached. The agent will keep polling — re-check with: yaver git oauth status %s%s\n", deviceStatusFlag(target), startResp.SessionID)
			os.Exit(1)
		}
		time.Sleep(time.Duration(interval) * time.Second)

		st, err := callGitOAuthStatus(startResp.SessionID, target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "git oauth: status check failed: %v\n", err)
			continue
		}
		switch st.State {
		case "done":
			fmt.Printf("✓ %s linked as %s on %s (git clone/push + CI/deploy vault token ready)\n", st.Provider, st.Username, whichBox)
			return
		case "error":
			fmt.Fprintf(os.Stderr, "✗ %s\n", st.Error)
			os.Exit(1)
		case "expired":
			fmt.Fprintln(os.Stderr, "✗ device code expired before the user approved. Re-run to start a fresh code.")
			os.Exit(1)
		case "pending":
			// Tweak interval if the agent escalated due to slow_down.
			if st.Interval > interval {
				interval = st.Interval
			}
			continue
		case "unknown":
			fmt.Fprintln(os.Stderr, "✗ session expired from the agent's memory. Re-run to start a fresh code.")
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "✗ unexpected state %q\n", st.State)
			os.Exit(1)
		}
	}
}

func runGitOAuthStatus(args []string) {
	fs := flag.NewFlagSet("git oauth status", flag.ExitOnError)
	var (
		device  string
		outJSON bool
	)
	fs.StringVar(&device, "device", "", "Owned remote device id/alias that started the flow")
	fs.BoolVar(&outJSON, "json", false, "Emit JSON instead of text")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Println("usage: yaver git oauth status [--device <id>] <session_id>")
		os.Exit(2)
	}
	target := resolveGitOAuthDeviceFlag(device)
	st, err := callGitOAuthStatus(fs.Arg(0), target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git oauth status: %v\n", err)
		os.Exit(1)
	}
	if outJSON {
		fmt.Println(jsonOrEmpty(st))
		return
	}
	whichBox := "this machine"
	if target != "" {
		whichBox = shortDeviceID(target)
	}
	switch st.State {
	case "done":
		fmt.Printf("done: %s linked as %s on %s\n", st.Provider, st.Username, whichBox)
	case "pending":
		fmt.Printf("pending: waiting for browser approval on %s\n", whichBox)
	case "error":
		fmt.Fprintf(os.Stderr, "error: %s\n", st.Error)
		os.Exit(1)
	case "expired":
		fmt.Fprintln(os.Stderr, "expired: re-run yaver git oauth to start a fresh code")
		os.Exit(1)
	default:
		fmt.Printf("%s: %s\n", st.State, st.Error)
	}
}

func resolveGitOAuthDeviceFlag(device string) string {
	target := strings.TrimSpace(device)
	if target == "" {
		return ""
	}
	cfg := mustLoadAuthConfig()
	known, err := listDevicesEnsuringAuth(cfg)
	if err == nil {
		if d, rerr := resolveDevice(target, known); rerr == nil {
			return d.DeviceID
		}
	}
	return target
}

func deviceStatusFlag(target string) string {
	if strings.TrimSpace(target) == "" {
		return ""
	}
	return "--device " + shortDeviceID(target) + " "
}

type gitOAuthStartResponse struct {
	OK              bool   `json:"ok"`
	Error           string `json:"error,omitempty"`
	SessionID       string `json:"session_id"`
	Provider        string `json:"provider"`
	Host            string `json:"host"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresAt       int64  `json:"expires_at"`
	BYOClient       bool   `json:"byo_client"`
}

type gitOAuthStatusResponse struct {
	OK        bool   `json:"ok"`
	State     string `json:"state"`
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
	Host      string `json:"host"`
	Username  string `json:"username"`
	Error     string `json:"error"`
	Interval  int    `json:"interval"`
}

// callGitOAuthStart hits /git/provider/oauth/start either on the local
// agent (target=="") or via the peer-proxy on a remote agent. We use
// proxyToDevice for the remote case so the same auth/relay plumbing as
// every other peer call applies.
func callGitOAuthStart(provider, host, target string) (*gitOAuthStartResponse, error) {
	body, _ := json.Marshal(map[string]string{"provider": provider, "host": host})
	if target != "" {
		status, raw, err := proxyToDevice(context.Background(), "git_oauth_start", target, http.MethodPost, "/git/provider/oauth/start", body)
		if err != nil {
			return nil, err
		}
		if status/100 != 2 {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(raw)))
		}
		var r gitOAuthStartResponse
		if jerr := json.Unmarshal(raw, &r); jerr != nil {
			return nil, fmt.Errorf("decode response: %w", jerr)
		}
		if !r.OK && r.Error != "" {
			return nil, fmt.Errorf("%s", r.Error)
		}
		return &r, nil
	}
	// Local: drive the state machine in-process. No HTTP roundtrip
	// needed — keeps the CLI usable when the local agent isn't running
	// in serve mode.
	sess, err := startGitOAuthDevice(context.Background(), provider, host)
	if err != nil {
		return nil, err
	}
	return &gitOAuthStartResponse{
		OK:              true,
		SessionID:       sess.ID,
		Provider:        sess.Provider,
		Host:            sess.Host,
		UserCode:        sess.UserCode,
		VerificationURI: sess.VerificationURI,
		Interval:        sess.Interval,
		ExpiresAt:       sess.ExpiresAt.Unix(),
		BYOClient:       sess.BYOClient,
	}, nil
}

func callGitOAuthStatus(sessionID, target string) (*gitOAuthStatusResponse, error) {
	if target != "" {
		path := "/git/provider/oauth/status?session=" + url.QueryEscape(sessionID)
		status, raw, err := proxyToDevice(context.Background(), "git_oauth_status", target, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if status/100 != 2 {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(raw)))
		}
		var r gitOAuthStatusResponse
		if jerr := json.Unmarshal(raw, &r); jerr != nil {
			return nil, fmt.Errorf("decode response: %w", jerr)
		}
		return &r, nil
	}
	sess, ok := getGitOAuthSession(sessionID)
	if !ok {
		return &gitOAuthStatusResponse{OK: false, State: "unknown"}, nil
	}
	return &gitOAuthStatusResponse{
		OK:        true,
		State:     sess.State,
		SessionID: sess.ID,
		Provider:  sess.Provider,
		Host:      sess.Host,
		Username:  sess.Username,
		Error:     sess.Error,
		Interval:  sess.Interval,
	}, nil
}

func jsonOrEmpty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
