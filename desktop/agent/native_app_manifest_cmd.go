package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func runNativeAppManifest(args []string) {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		nativeAppManifestUsage()
		return
	}
	switch args[0] {
	case "audit":
		runNativeAppManifestAudit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver app-manifest %s\n\n", args[0])
		nativeAppManifestUsage()
		os.Exit(1)
	}
}

func nativeAppManifestUsage() {
	fmt.Print(`yaver app-manifest — Yaver-native app/game manifest tools.

Usage:
  yaver app-manifest audit [--json] [path]
  yaver game-manifest audit [--json] [path]

Path defaults to the current directory. Yaver scans yaver.app.yaml,
yaver.game.yaml, yaver.app.yml, yaver.game.yml, yaver.app.json, and
yaver.game.json in that order.
`)
}

func runNativeAppManifestAudit(args []string) {
	dir := "."
	jsonOut := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.TrimSpace(arg) != "":
			dir = arg
		}
	}
	m, err := LoadYaverNativeAppManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest audit failed: %v\n", err)
		os.Exit(1)
	}
	audit := AuditYaverNativeAppManifest(m)
	if jsonOut {
		data, _ := json.MarshalIndent(audit, "", "  ")
		fmt.Println(string(data))
	} else {
		if m != nil {
			fmt.Printf("Manifest: %s\n", m.path)
		}
		if len(audit.Findings) == 0 {
			fmt.Println("Yaver-native manifest audit passed with no findings.")
		} else {
			if audit.OK {
				fmt.Println("Yaver-native manifest audit passed with warnings.")
			} else {
				fmt.Println("Yaver-native manifest audit failed.")
			}
			for _, finding := range audit.Findings {
				fmt.Printf("- %s %s: %s\n", strings.ToUpper(finding.Severity), finding.Code, finding.Message)
			}
		}
	}
	if !audit.OK {
		os.Exit(1)
	}
}
