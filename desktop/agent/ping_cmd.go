package main

// `yaver ping <alias|deviceId|name>` and `yaver primary ping`.
//
// Modeled on `tailscale ping`: a one-shot reachability check that
// returns the transport that worked, latency, and a short summary
// of the remote agent's identity + auth state. Designed to answer
// the two questions that today require a five-step manual debug:
//
//   1. Is the box up at all? (HTTP /info or /health responds)
//   2. Is its Yaver auth valid, and is it the SAME user as me?
//
// Output is one or two short lines per attempt, similar to
// `tailscale ping ${node}`:
//
//     yaver-test-ephemeral [2859819c…] via relay (203 ms)
//       agent v1.99.127, lifecycle ready-to-connect, owner kivanc.cakmak@icloud.com (you)
//
// On failure, the line names the cause:
//
//     yaver-test-ephemeral [000ca94b…] unreachable
//       cause:    every transport candidate failed
//       hint:     run `yaver primary auth` to re-sign in on the primary device

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runReachPing is the reachability-style `yaver ping <hint>` (one-shot
// HTTP /info probe + auth check). The legacy QUIC-style `yaver ping`
// (-c N, --device <id>, --relay) lives in main.go::runPing and is
// reached when the caller passes a flag instead of a positional hint.
// runPingDispatch routes `yaver ping ...` to either the new
// reachability ping (positional hint, no legacy flags) or the
// legacy QUIC ping (--device / -c / --relay). Keeps both shapes
// callable from the single `yaver ping` verb.
func runPingDispatch(args []string) {
	hasLegacyFlag := false
	hasPositional := false
	for _, a := range args {
		switch {
		case a == "-c" || a == "--device" || a == "--relay" || a == "--relay-server" || strings.HasPrefix(a, "-c=") || strings.HasPrefix(a, "--device=") || strings.HasPrefix(a, "--relay="):
			hasLegacyFlag = true
		case !strings.HasPrefix(a, "-"):
			hasPositional = true
		}
	}
	if hasPositional && !hasLegacyFlag {
		runReachPing(args)
		return
	}
	runPing(args)
}

func runReachPing(args []string) {
	fs := flag.NewFlagSet("ping", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	timeout := fs.Duration("timeout", 8*time.Second, "overall timeout")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "yaver ping <alias|deviceId|name> — name a device, or use `yaver primary ping`.")
		os.Exit(2)
	}
	hint := fs.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := fetchRemoteAgentStatusByHint(ctx, hint)
	emitPingReport(report, err, *jsonOut, hint)
	if err != nil || report == nil {
		os.Exit(1)
	}
}

// runPrimaryPing is wired from `yaver primary ping`.
func runPrimaryPing(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("primary ping", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	timeout := fs.Duration("timeout", 8*time.Second, "overall timeout")
	_ = fs.Parse(args)

	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read userSettings: %v\n", err)
		os.Exit(1)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		fmt.Fprintln(os.Stderr, "No primary device set. Run `yaver primary set <deviceId>` first.")
		os.Exit(1)
	}
	probeCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := fetchRemoteAgentStatusByDeviceID(probeCtx, current)
	emitPingReport(report, err, *jsonOut, "primary")
	if err != nil || report == nil {
		os.Exit(1)
	}
}

type pingResult struct {
	Hint            string `json:"hint"`
	DeviceID        string `json:"deviceId,omitempty"`
	Name            string `json:"name,omitempty"`
	Reachable       bool   `json:"reachable"`
	Transport       string `json:"transport,omitempty"`
	BaseURL         string `json:"baseUrl,omitempty"`
	LatencyMS       int64  `json:"latencyMs,omitempty"`
	AgentVersion    string `json:"agentVersion,omitempty"`
	LifecycleState  string `json:"lifecycleState,omitempty"`
	NeedsAuth       bool   `json:"needsAuth"`
	OwnerEmail      string `json:"ownerEmail,omitempty"`
	OwnerIsCaller   bool   `json:"ownerIsCaller"`
	Cause           string `json:"cause,omitempty"`
	Hint2           string `json:"hint,omitempty"`
}

func emitPingReport(report *remoteAgentStatusReport, err error, asJSON bool, hint string) {
	res := pingResult{Hint: hint}
	if report != nil {
		res.DeviceID = report.DeviceID
		res.Name = report.Name
		res.Transport = report.Transport
		res.BaseURL = report.BaseURL
		res.AgentVersion = report.Version
		res.LifecycleState = report.LifecycleState
		res.NeedsAuth = report.NeedsAuth
	}
	if err == nil && report != nil && report.HTTPStatusInfo > 0 {
		res.Reachable = true
	}
	// Owner-equality and caller-email are best-effort. Looked up from
	// /info -> ownerUserID compared against the caller's userId.
	if report != nil && report.Info != nil {
		if email, ok := report.Info["ownerEmail"].(string); ok {
			res.OwnerEmail = email
		}
		if owner, ok := report.Info["ownerUserId"].(string); ok {
			if cfg, lerr := LoadConfig(); lerr == nil {
				if me := callerUserID(cfg); me != "" {
					res.OwnerIsCaller = (owner == me)
				}
			}
		}
	}
	if err != nil {
		var target *DeviceInfo
		if report != nil {
			target = &DeviceInfo{
				DeviceID: report.DeviceID,
				Name:     report.Name,
				IsOnline: report.IsOnline,
			}
		}
		res.Cause, res.Hint2 = classifyRemoteStatusError(err, target)
	}

	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(res)
		return
	}
	header := strings.TrimSpace(res.Name)
	if header == "" {
		header = res.Hint
	}
	if id := res.DeviceID; id != "" {
		header += fmt.Sprintf(" [%s…]", id[:min(8, len(id))])
	}
	if !res.Reachable {
		fmt.Println(header + " unreachable")
		if res.Cause != "" {
			fmt.Printf("  cause:  %s\n", res.Cause)
		}
		if res.Hint2 != "" {
			fmt.Printf("  hint:   %s\n", res.Hint2)
		}
		return
	}
	transport := res.Transport
	if transport == "" {
		transport = "direct"
	}
	fmt.Printf("%s via %s\n", header, transport)
	parts := []string{}
	if res.AgentVersion != "" {
		parts = append(parts, "agent "+res.AgentVersion)
	}
	if res.LifecycleState != "" {
		parts = append(parts, "lifecycle "+res.LifecycleState)
	}
	switch {
	case res.NeedsAuth:
		parts = append(parts, "needs auth — run `yaver primary auth`")
	case res.OwnerIsCaller && res.OwnerEmail != "":
		parts = append(parts, "owner "+res.OwnerEmail+" (you)")
	case res.OwnerEmail != "":
		parts = append(parts, "owner "+res.OwnerEmail+" (NOT you — different account)")
	}
	if len(parts) > 0 {
		fmt.Println("  " + strings.Join(parts, ", "))
	}
}

// callerUserID best-effort returns the caller's Convex userId by hitting
// /auth/me with the local token. Empty on any error so the OwnerIsCaller
// comparison silently downgrades to "unknown" instead of misreporting.
func callerUserID(cfg *Config) string {
	if cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return ""
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(convex, "/")+"/auth/me", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var body struct {
		UserID    string `json:"userId"`
		UserDocId string `json:"userDocId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	if body.UserID != "" {
		return body.UserID
	}
	return body.UserDocId
}
