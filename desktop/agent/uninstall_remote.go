package main

// uninstall_remote.go — `yaver uninstall <alias|deviceId>` (and the
// sugar verb `yaver primary uninstall`). Triggers the same
// /machine/remove flow the web + mobile UIs use, then streams the
// agent's progress events back to the CLI until the remote process
// exits and the connection drops.
//
// Trust gate: identical to `yaver ssh primary` — the call rides
// `doRemoteAgentRequest` (direct → relay fallback) with the local
// Convex bearer token; the remote agent's s.auth() middleware
// rejects callers that don't own the device.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runRemoteUninstall handles `yaver uninstall <target>` (target is
// alias / deviceId / device name) and `yaver primary uninstall`.
// It POSTs /machine/remove, then opens an SSE subscription to the
// returned stream and prints each event as it arrives. Returns when
// the SSE connection drops (the remote agent has exited).
func runRemoteUninstall(target string, yes bool) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in — run 'yaver auth' first.")
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}

	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not list devices: %v\n", err)
		os.Exit(1)
	}
	dev, err := resolveDevice(target, devices)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	label := strings.TrimSpace(dev.Alias)
	if label == "" {
		label = dev.Name
	}

	if !yes {
		fmt.Printf("This will entirely remove Yaver from %q (%s) and delete its\n", label, dev.DeviceID[:min(8, len(dev.DeviceID))])
		fmt.Println("record on the public Yaver backend. Cannot be undone.")
		fmt.Println()
		fmt.Print("Type 'delete my machine' to confirm: ")
		var resp string
		_, _ = fmt.Scanln(&resp)
		if !machineRemovalPhraseValid(resp) {
			fmt.Println("Aborted.")
			os.Exit(1)
		}
	}

	candidates, err := buildRemoteAgentCandidates(cfg, dev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Transport candidates: %v\n", err)
		os.Exit(1)
	}
	if len(candidates) == 0 {
		fmt.Fprintf(os.Stderr, "No reachable transport candidates for %q. Try `yaver ping %s` to debug.\n", label, label)
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"confirm": true,
		"phrase":  machineRemovalPhrase,
	})
	postCtx, postCancel := context.WithTimeout(context.Background(), 15*time.Second)
	chosen, status, raw, err := doRemoteAgentRequest(postCtx, candidates, cfg.AuthToken, http.MethodPost, "/machine/remove", body, 12*time.Second)
	postCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST /machine/remove: %v\n", err)
		os.Exit(1)
	}
	if status < 200 || status >= 300 {
		fmt.Fprintf(os.Stderr, "POST /machine/remove returned HTTP %d: %s\n", status, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Stream string `json:"stream"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Could not parse /machine/remove response: %v\n%s\n", err, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}
	if !resp.OK || resp.Stream == "" {
		fmt.Fprintf(os.Stderr, "Agent did not return a stream name: %s\n", strings.TrimSpace(string(raw)))
		os.Exit(1)
	}

	fmt.Printf("Removing Yaver from %s (%s)…\n", label, dev.DeviceID[:min(8, len(dev.DeviceID))])
	streamRemoteMachineRemoveProgress(chosen.BaseURL, cfg.AuthToken, resp.Stream)
	fmt.Println()
	fmt.Printf("%s has been uninstalled remotely. The device record is gone from the Yaver backend.\n", label)
}

// streamRemoteMachineRemoveProgress opens GET /streams/<name> as SSE
// and renders each event line. Exits when the connection drops (the
// remote agent has exited as part of uninstall) or when a
// machine_remove_result event arrives.
func streamRemoteMachineRemoveProgress(baseURL, token, streamName string) {
	url := strings.TrimRight(baseURL, "/") + "/streams/" + streamName
	// Long-lived: the stream stays open until the agent exits. No
	// hard timeout — the connection drops naturally when the remote
	// process dies. A liveness ceiling of 10 minutes guards against
	// the rare hang.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (stream: build request: %v)\n", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (stream: connect: %v)\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "  (stream: HTTP %d)\n", resp.StatusCode)
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		// Try structured event first; fall back to raw line.
		var evt map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &evt); err == nil && evt["type"] != nil {
			renderRemoteMachineRemoveEvent(evt)
			if t, _ := evt["type"].(string); t == "machine_remove_result" {
				return
			}
			continue
		}
		// Plain text line — agent's first "Starting machine removal…" banner.
		if strings.TrimSpace(payload) != "" {
			fmt.Printf("  %s\n", payload)
		}
	}
	// Scanner exited (connection dropped) — that's the expected end
	// state when the remote agent process exits as part of uninstall.
}

func renderRemoteMachineRemoveEvent(evt map[string]interface{}) {
	t, _ := evt["type"].(string)
	step, _ := evt["step"].(string)
	status, _ := evt["status"].(string)
	detail, _ := evt["detail"].(string)
	errStr, _ := evt["error"].(string)
	switch t {
	case "machine_remove_step":
		switch status {
		case "running":
			fmt.Printf("  [%s] %s\n", step, detail)
		case "ok":
			if detail != "" {
				fmt.Printf("    ✓ %s\n", detail)
			}
		case "skipped":
			fmt.Printf("    — %s (skipped)\n", detail)
		case "error":
			if errStr != "" {
				fmt.Fprintf(os.Stderr, "    ✗ %s: %s\n", step, errStr)
			} else if detail != "" {
				fmt.Fprintf(os.Stderr, "    ✗ %s: %s\n", step, detail)
			}
		}
	case "machine_remove_result":
		if status == "error" && errStr != "" {
			fmt.Fprintf(os.Stderr, "  ✗ result: %s\n", errStr)
		} else if detail != "" {
			fmt.Printf("  ✓ %s\n", detail)
		}
	}
}
