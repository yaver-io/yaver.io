package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Connectivity is deliberately independent of runners and Studio readiness.
func runConnectivityStatus(args []string) {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--details":
			runStatus()
			return
		default:
			fmt.Fprintln(os.Stderr, "Usage: yaver status [--json]")
			os.Exit(1)
		}
	}
	res, err := localAgentRequest("GET", "/mesh/status", nil)
	if err != nil {
		if jsonOutput {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"running": false, "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "Yaver status unavailable: %v\n", err)
		}
		os.Exit(1)
	}
	if jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(res)
		return
	}
	fmt.Print(connectivityStatusText(res, time.Now()))
}

func connectivityStatusText(res map[string]interface{}, now time.Time) string {
	dp, _ := res["dataPlane"].(map[string]interface{})
	running, _ := dp["running"].(bool)
	if !running {
		if warning, _ := res["autoEnableWarning"].(string); warning != "" {
			return "Disconnected. " + warning + "\n"
		}
		if reason, _ := dp["lastError"].(string); reason != "" {
			return "Disconnected. " + reason + "\n"
		}
		return "Disconnected. Run `yaver up` to connect. Existing SSH and Tailscale connections are independent.\n"
	}
	ip, _ := dp["selfIp"].(string)
	text := fmt.Sprintf("%s  self  connected\n", ip)
	if reason, _ := dp["lastError"].(string); reason != "" {
		text += "Status: " + reason + "\n"
	}
	peers, _ := dp["peers"].([]interface{})
	for _, raw := range peers {
		peer, _ := raw.(map[string]interface{})
		endpoint, _ := peer["Endpoint"].(string)
		handshake, _ := peer["LastHandshakeUnix"].(float64)
		state := "idle; no recent handshake"
		if age := now.Unix() - int64(handshake); handshake > 0 && age >= 0 && age <= 180 {
			state = "active"
		}
		text += fmt.Sprintf("%s  peer  %s\n", meshOrDash(endpoint), state)
	}
	return text
}
