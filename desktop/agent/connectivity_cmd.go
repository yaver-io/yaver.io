package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
)

// Connectivity is deliberately independent of runners and Studio readiness.
func runConnectivityStatus(args []string) {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Println("Usage: yaver status [--json]")
			return
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
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	field := func(row map[string]interface{}, key string) string {
		v, _ := row[key].(string)
		v = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, v)
		return meshOrDash(strings.Join(strings.Fields(v), "-"))
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t-\n", field(dp, "selfIp"), field(res, "selfName"), "self", field(res, "selfOS"))
	peers, _ := dp["peers"].([]interface{})
	for _, raw := range peers {
		peer, _ := raw.(map[string]interface{})
		handshake, _ := peer["LastHandshakeUnix"].(float64)
		state := "idle"
		if age := now.Unix() - int64(handshake); handshake > 0 && age >= 0 && age <= 180 {
			state = "active"
			if path := field(peer, "Path"); path != "-" {
				state += "; " + path
				if path == "direct" {
					state += " " + field(peer, "Endpoint")
				}
			}
		}
		tx, _ := peer["TxBytes"].(float64)
		rx, _ := peer["RxBytes"].(float64)
		state += fmt.Sprintf(", tx %.0f rx %.0f", tx, rx)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", field(peer, "MeshIP"), field(peer, "Name"), field(peer, "Owner"), field(peer, "OS"), state)
	}
	_ = w.Flush()
	if reason, _ := dp["lastError"].(string); reason != "" {
		fmt.Fprintf(&buf, "\n# Health check:\n# %s\n", reason)
	}
	return buf.String()
}

func parseConnectivityCommand(command string, args []string) bool {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "Usage: yaver %s\n", command) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false
		}
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}
	return true
}
