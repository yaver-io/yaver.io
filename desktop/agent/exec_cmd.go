package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// runExec executes a command on a remote device and streams output.
func runExec(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	deviceID := fs.String("device", "", "Device ID or hostname prefix (auto-discovers if not set)")
	workDir := fs.String("work-dir", "", "Working directory on remote machine")
	timeout := fs.Int("timeout", 300, "Command timeout in seconds")
	useRelay := fs.Bool("relay", true, "Connect through relay server (default: true)")
	direct := fs.Bool("direct", false, "Connect directly (skip relay)")
	fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver exec [flags] <command...>")
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, `  yaver exec "ls -la"`)
		fmt.Fprintln(os.Stderr, `  yaver exec --device my-mac "git status"`)
		fmt.Fprintln(os.Stderr, `  yaver exec --work-dir /home/user/project "make build"`)
		os.Exit(1)
	}

	if *direct {
		*useRelay = false
	}

	command := strings.Join(fs.Args(), " ")
	cfg := mustLoadAuthConfig()

	// Discover device and resolve base URL
	baseURL := resolveDeviceURL(cfg, *deviceID, *useRelay)

	// Start exec
	execResp, err := execHTTP("POST", baseURL+"/exec", cfg.AuthToken, map[string]interface{}{
		"command": command,
		"workDir": *workDir,
		"timeout": *timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if execResp["ok"] != true {
		fmt.Fprintf(os.Stderr, "Error: %v\n", execResp["error"])
		os.Exit(1)
	}

	execID := execResp["execId"].(string)
	fmt.Fprintf(os.Stderr, "Exec %s started (pid %v)\n", execID[:8], execResp["pid"])

	// Handle Ctrl+C — send SIGINT to remote process
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			sigName := "SIGINT"
			if sig == syscall.SIGTERM {
				sigName = "SIGTERM"
			}
			execHTTP("POST", baseURL+"/exec/"+execID+"/signal", cfg.AuthToken, map[string]interface{}{
				"signal": sigName,
			})
		}
	}()

	// Poll output
	var lastStdoutLen, lastStderrLen int
	exitCode := 0
	for {
		select {
		case <-ctx.Done():
			os.Exit(130)
		default:
		}

		resp, err := execHTTP("GET", baseURL+"/exec/"+execID, cfg.AuthToken, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Poll error: %v\n", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		exec, ok := resp["exec"].(map[string]interface{})
		if !ok {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Print new stdout
		if stdout, ok := exec["stdout"].(string); ok && len(stdout) > lastStdoutLen {
			fmt.Print(stdout[lastStdoutLen:])
			lastStdoutLen = len(stdout)
		}
		// Print new stderr
		if stderr, ok := exec["stderr"].(string); ok && len(stderr) > lastStderrLen {
			fmt.Fprint(os.Stderr, stderr[lastStderrLen:])
			lastStderrLen = len(stderr)
		}

		status, _ := exec["status"].(string)
		if status == "completed" || status == "failed" || status == "killed" {
			if code, ok := exec["exitCode"].(float64); ok {
				exitCode = int(code)
			}
			break
		}

		time.Sleep(200 * time.Millisecond)
	}

	os.Exit(exitCode)
}

// resolveDeviceURL discovers a device and returns its HTTP base URL.
func resolveDeviceURL(cfg *Config, deviceHint string, useRelay bool) string {
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing devices: %v\n", err)
		os.Exit(1)
	}
	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "No devices found. Make sure your agent is running.")
		os.Exit(1)
	}

	var target *DeviceInfo
	for i := range devices {
		d := &devices[i]
		if deviceHint != "" {
			if strings.HasPrefix(d.DeviceID, deviceHint) || strings.EqualFold(d.Name, deviceHint) || strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(deviceHint)) {
				target = d
				break
			}
		} else if d.IsOnline {
			target = d
			break
		}
	}

	if target == nil {
		fmt.Fprintln(os.Stderr, "No matching online device. Your devices:")
		for _, d := range devices {
			status := "offline"
			if d.IsOnline {
				status = "online"
			}
			fmt.Fprintf(os.Stderr, "  %s  %-20s  %s\n", d.DeviceID[:8], d.Name, status)
		}
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Connected to %s (%s)\n", target.Name, target.DeviceID[:8])

	if useRelay {
		// Try relay servers
		relays, err := FetchRelayServers(cfg.ConvexSiteURL)
		if err == nil {
			for _, r := range relays {
				if r.HttpURL != "" {
					return strings.TrimRight(r.HttpURL, "/") + "/d/" + target.DeviceID
				}
			}
		}
		// Also try config relay servers
		for _, r := range cfg.RelayServers {
			if r.HttpURL != "" {
				return strings.TrimRight(r.HttpURL, "/") + "/d/" + target.DeviceID
			}
		}
		for _, r := range cfg.CachedRelayServers {
			if r.HttpURL != "" {
				return strings.TrimRight(r.HttpURL, "/") + "/d/" + target.DeviceID
			}
		}
	}

	// Direct connection (HTTP API defaults to port 18080)
	return fmt.Sprintf("http://%s:18080", target.QuicHost)
}

// execHTTP makes an HTTP request with auth and JSON body.
func execHTTP(method, url, token string, body map[string]interface{}) (map[string]interface{}, error) {
	var bodyReader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		bodyReader = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	relayPassword, err := relayPasswordForBase(url)
	if err != nil {
		return nil, err
	}
	if relayPassword != "" {
		req.Header.Set("X-Relay-Password", relayPassword)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}
