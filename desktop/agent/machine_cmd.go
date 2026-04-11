package main

// machine_cmd.go — `yaver machine ...` CLI surface for the
// headless-hardware ops story. Right now this is a thin
// façade over diskhealth.go (SMART + disk space), but it's
// the landing spot for future remote power, sleep scheduling,
// temperature monitoring, and the "your Mac mini hasn't
// checked in for 10 minutes" heartbeat alert.

import (
	"fmt"
	"os"
)

func runMachine(args []string) {
	if len(args) == 0 {
		printMachineUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "health":
		runMachineHealthCmd()
	case "scan":
		runDiskHealthScan()
		runMachineHealthCmd()
	case "help", "--help", "-h":
		printMachineUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown machine subcommand: %s\n\n", args[0])
		printMachineUsage()
		os.Exit(1)
	}
}

func printMachineUsage() {
	fmt.Print(`Yaver machine — headless hardware management.

Usage:
  yaver machine health            Print the latest disk + SMART snapshot
  yaver machine scan              Force a fresh scan now

When the agent is running as a daemon, it scans every 10
minutes and fires a notification whenever a filesystem crosses
85% / 95% or a drive reports a SMART failure. No vendor account
— state lives under ~/.yaver/ and alerts go through the
existing push channel.
`)
}
