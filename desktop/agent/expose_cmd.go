package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func runExposeCmd(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: yaver expose --port 3000 --subdomain myapp")
		fmt.Println("       yaver expose list")
		fmt.Println("       yaver expose stop [subdomain]")
		return
	}
	switch args[0] {
	case "list", "ls":
		runExposeList()
	case "stop":
		sub := ""
		if len(args) > 1 {
			sub = args[1]
		}
		runExposeStop(sub)
	default:
		runExposeStart(args)
	}
}

func runExposeStart(args []string) {
	fs := flag.NewFlagSet("expose", flag.ExitOnError)
	port := fs.Int("port", 0, "Local port to expose")
	subdomain := fs.String("subdomain", "", "Subdomain (e.g. myapp → myapp.yaver.io)")
	fs.Parse(args)
	if *port == 0 {
		fmt.Fprintln(os.Stderr, "Error: --port is required")
		os.Exit(1)
	}
	sub := *subdomain
	if sub == "" {
		hostname, _ := os.Hostname()
		hostname = strings.ToLower(strings.ReplaceAll(hostname, " ", "-"))
		hostname = strings.ReplaceAll(hostname, ".", "-")
		hostname = strings.ReplaceAll(hostname, "'", "")
		if len(hostname) > 20 {
			hostname = hostname[:20]
		}
		sub = fmt.Sprintf("%s-%d", hostname, *port)
	}
	body, _ := json.Marshal(map[string]interface{}{"port": *port, "subdomain": sub})
	resp, err := exposeAgentRequest("POST", "/expose/start", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		fmt.Fprintf(os.Stderr, "Error: %s\n", errResp["error"])
		os.Exit(1)
	}
	var result RelayExposeEntry
	json.NewDecoder(resp.Body).Decode(&result)
	fmt.Printf("Exposed: %s\n  Port: %d\n  Subdomain: %s\nPress Ctrl+C to stop.\n", result.PublicURL, result.Port, result.Subdomain)
	select {}
}

func runExposeList() {
	resp, err := exposeAgentRequest("GET", "/expose/list", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var entries []*RelayExposeEntry
	json.NewDecoder(resp.Body).Decode(&entries)
	if len(entries) == 0 {
		fmt.Println("No active expose entries.")
		return
	}
	for _, e := range entries {
		fmt.Printf("  %s → port %d (%s, %s ago)\n", e.PublicURL, e.Port, e.Subdomain, time.Since(e.CreatedAt).Round(time.Second))
	}
}

func runExposeStop(subdomain string) {
	body, _ := json.Marshal(map[string]string{"subdomain": subdomain})
	resp, err := exposeAgentRequest("POST", "/expose/stop", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	resp.Body.Close()
	if subdomain == "" {
		fmt.Println("All stopped.")
	} else {
		fmt.Printf("Stopped %s\n", subdomain)
	}
}

// exposeAgentRequest makes an authenticated HTTP request to the local agent and returns the raw response.
func exposeAgentRequest(method, path string, body []byte) (*http.Response, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return nil, fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	var req *http.Request
	if body != nil {
		req, err = http.NewRequest(method, "http://127.0.0.1:18080"+path, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest(method, "http://127.0.0.1:18080"+path, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent not reachable: %v", err)
	}
	return resp, nil
}
