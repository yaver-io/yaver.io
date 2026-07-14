package main

// `yaver reboot [--machine=<alias|deviceId>] [--yes]`
//
// Reboot is the one machine action you could do from the phone and the web
// dashboard but not from the terminal. It routes through the `infra_power` ops
// verb, so a remote reboot uses the same transport (and the same owner-only
// authorization) as every other remote verb — no SSH hop required.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func runReboot(args []string) {
	machine := "local"
	assumeYes := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--machine="):
			machine = strings.TrimPrefix(a, "--machine=")
		case a == "--yes" || a == "-y":
			assumeYes = true
		case a == "--help" || a == "-h":
			fmt.Println("usage: yaver reboot [--machine=<alias|deviceId>] [--yes]")
			fmt.Println()
			fmt.Println("Reboot a machine. Without --machine, reboots THIS machine.")
			fmt.Println("Needs root or passwordless sudo on the target — `yaver ops infra_summary`")
			fmt.Println("reports capabilities.hostReboot=false when it doesn't have it.")
			return
		}
	}

	target := machine
	if target == "local" {
		host, _ := os.Hostname()
		target = fmt.Sprintf("this machine (%s)", host)
	}

	// A reboot kills every task, build and runner on the box. Never do that off
	// a bare command with no second look, the way `yaver restart` (agent-only)
	// can afford to.
	if !assumeYes {
		fmt.Printf("Reboot %s? Every running task, build and runner on it dies. [y/N] ", target)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Println("Cancelled.")
			return
		}
	}

	token, err := opsLoadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	req := OpsRequest{
		Verb:    "infra_power",
		Machine: machine,
		Payload: json.RawMessage(`{"action":"host_reboot","confirm":true}`),
	}
	buf, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, status := opsLocalRequest(context.Background(), "POST", "/ops", token, buf)
	if status >= 500 {
		fmt.Fprintf(os.Stderr, "HTTP %d\n%s\n", status, string(body))
		os.Exit(2)
	}
	var res OpsResult
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected response: %s\n", string(body))
		os.Exit(2)
	}
	if !res.OK {
		fmt.Fprintf(os.Stderr, "Reboot failed: %s\n", res.Error)
		os.Exit(1)
	}
	fmt.Printf("Rebooting %s — it will drop off the network for a minute.\n", target)
}
