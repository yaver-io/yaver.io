package main

// machine_lifecycle_cmd.go — `yaver machine create|up|down|status|list|rm`.
// The single-owner production "own remote machine" surface. Reads box state
// from Convex byoMachines (GET /byo/machines) and drives lifecycle through
// the local daemon's machine_* ops verbs (machine_lifecycle.go), which hold
// the vault Hetzner token. Default posture is scale-to-zero: `down` snapshots
// then deletes the box so it stops billing; `up` recreates it from the
// snapshot in ~minutes. Works the same whether you're at the CLI or (later)
// the phone — both drive the same byoMachines state.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

type byoMachineRow struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	ServerID        string `json:"serverId"`
	DeviceID        string `json:"deviceId"`
	Name            string `json:"name"`
	Region          string `json:"region"`
	Plan            string `json:"plan"`
	ServerIP        string `json:"serverIp"`
	SnapshotImageID string `json:"snapshotImageId"`
	State           string `json:"state"`
	UpdatedAt       int64  `json:"updatedAt"`
}

// fetchByoMachines reads the caller's BYO boxes from Convex.
func fetchByoMachines(cfg *Config) ([]byoMachineRow, error) {
	req, err := newBearerRequest("GET", cfg.ConvexSiteURL+"/byo/machines", cfg.AuthToken, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("byo/machines failed (status %d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		Machines []byoMachineRow `json:"machines"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse byo machines: %w", err)
	}
	return result.Machines, nil
}

// latestByName returns the most-recently-updated row whose name matches
// (case-insensitive). optState, when set, filters to that lifecycle state.
func latestByName(rows []byoMachineRow, name, optState string) *byoMachineRow {
	name = strings.TrimSpace(strings.ToLower(name))
	var best *byoMachineRow
	for i := range rows {
		r := &rows[i]
		if strings.ToLower(r.Name) != name {
			continue
		}
		if optState != "" && r.State != optState {
			continue
		}
		if best == nil || r.UpdatedAt > best.UpdatedAt {
			best = r
		}
	}
	return best
}

// callMachineOp POSTs a machine_* verb to the local daemon /ops with
// confirm=true baked in, and returns the parsed { ok, code, error, initial }.
func callMachineOp(verb string, payload map[string]interface{}) (map[string]interface{}, error) {
	token, err := opsLoadToken()
	if err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["confirm"] = true
	reqBody, _ := json.Marshal(map[string]interface{}{
		"machine": "local",
		"verb":    verb,
		"payload": payload,
	})
	body, status := opsLocalRequest(context.Background(), "POST", "/ops", token, reqBody)
	var parsed map[string]interface{}
	_ = json.Unmarshal(body, &parsed)
	if status >= 500 || parsed == nil {
		return parsed, fmt.Errorf("machine op %s: HTTP %d: %s", verb, status, string(body))
	}
	if ok, _ := parsed["ok"].(bool); !ok {
		msg, _ := parsed["error"].(string)
		code, _ := parsed["code"].(string)
		return parsed, fmt.Errorf("%s failed (%s): %s", verb, code, msg)
	}
	return parsed, nil
}

func machineInitial(res map[string]interface{}) map[string]interface{} {
	if res == nil {
		return nil
	}
	if init, ok := res["initial"].(map[string]interface{}); ok {
		return init
	}
	return nil
}

func loadConfigOrExit() *Config {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "not signed in — run `yaver auth` first")
		os.Exit(1)
	}
	return cfg
}

func runMachineCreateCmd(args []string) {
	name, flags := parseMachinePosFlags(args)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver machine create <name> [--plan starter|pro|scale] [--region eu|us]")
		os.Exit(1)
	}
	payload := map[string]interface{}{"name": name}
	if p := flags["plan"]; p != "" {
		payload["plan"] = p
	}
	if r := flags["region"]; r != "" {
		payload["region"] = r
	}
	fmt.Printf("Creating box %q on your Hetzner account…\n", name)
	res, err := callMachineOp("machine_create", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine create: %v\n", err)
		os.Exit(1)
	}
	init := machineInitial(res)
	fmt.Printf("✓ created: server %v  ip %v  (%v/%v)\n", init["created"], init["ip"], init["plan"], init["region"])
	fmt.Printf("  The box is provisioning + will register as a device shortly.\n")
	fmt.Printf("  Once it appears in `yaver devices`, run:\n")
	fmt.Printf("    yaver alias set %s <deviceId>   &&   yaver primary set %s\n", name, name)
	fmt.Printf("  Then `yaver codex --machine=%s` connects (and auto-wakes it when stopped).\n", name)
}

func runMachineDownCmd(args []string) {
	name, _ := parseMachinePosFlags(args)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver machine down <name>")
		os.Exit(1)
	}
	cfg := loadConfigOrExit()
	rows, err := fetchByoMachines(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine down: %v\n", err)
		os.Exit(1)
	}
	row := latestByName(rows, name, "active")
	if row == nil {
		fmt.Fprintf(os.Stderr, "no ACTIVE box named %q found (already stopped?). `yaver machine list` to check.\n", name)
		os.Exit(1)
	}
	fmt.Printf("Scaling %q (server %s) to zero — snapshot then delete…\n", name, row.ServerID)
	res, err := callMachineOp("machine_down", map[string]interface{}{"serverId": row.ServerID, "name": name})
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine down: %v\n", err)
		os.Exit(1)
	}
	init := machineInitial(res)
	fmt.Printf("✓ stopped. snapshot %v kept — `yaver machine up %s` recreates it.\n", init["snapshotImageId"], name)
}

func runMachineUpCmd(args []string) {
	name, flags := parseMachinePosFlags(args)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver machine up <name>")
		os.Exit(1)
	}
	cfg := loadConfigOrExit()
	rows, err := fetchByoMachines(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine up: %v\n", err)
		os.Exit(1)
	}
	if active := latestByName(rows, name, "active"); active != nil {
		fmt.Printf("%q is already up (server %s, ip %s).\n", name, active.ServerID, active.ServerIP)
		return
	}
	row := latestByName(rows, name, "stopped")
	if row == nil || strings.TrimSpace(row.SnapshotImageID) == "" {
		fmt.Fprintf(os.Stderr, "no stopped box named %q with a snapshot to restore. `yaver machine list`.\n", name)
		os.Exit(1)
	}
	payload := map[string]interface{}{"snapshotImageId": row.SnapshotImageID, "name": name}
	if p := firstNonEmpty(flags["plan"], row.Plan); p != "" {
		payload["plan"] = p
	}
	if r := firstNonEmpty(flags["region"], row.Region); r != "" {
		payload["region"] = r
	}
	fmt.Printf("Recreating %q from snapshot %s (~minutes)…\n", name, row.SnapshotImageID)
	res, err := callMachineOp("machine_up", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine up: %v\n", err)
		os.Exit(1)
	}
	init := machineInitial(res)
	fmt.Printf("✓ up: server %v  ip %v. It re-registers over the relay automatically.\n", init["started"], init["ip"])
}

func runMachineRmCmd(args []string) {
	name, flags := parseMachinePosFlags(args)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver machine rm <name> [--purge]")
		os.Exit(1)
	}
	cfg := loadConfigOrExit()
	rows, err := fetchByoMachines(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine rm: %v\n", err)
		os.Exit(1)
	}
	row := latestByName(rows, name, "active")
	if row == nil {
		fmt.Fprintf(os.Stderr, "no active box named %q to remove (already stopped/deleted?). `yaver machine list`.\n", name)
		os.Exit(1)
	}
	payload := map[string]interface{}{"serverId": row.ServerID, "name": name}
	if flags["purge"] == "true" {
		payload["purge"] = true
	}
	res, err := callMachineOp("machine_rm", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine rm: %v\n", err)
		os.Exit(1)
	}
	init := machineInitial(res)
	if init["snapshotPurged"] == true {
		fmt.Printf("✓ removed %q (server %v) + snapshot purged.\n", name, row.ServerID)
	} else {
		fmt.Printf("✓ removed %q (server %v). Final snapshot %v retained (use --purge to delete it).\n", name, row.ServerID, init["snapshotImageId"])
	}
}

func runMachineListCmd() {
	cfg := loadConfigOrExit()
	rows, err := fetchByoMachines(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine list: %v\n", err)
		os.Exit(1)
	}
	// Collapse to the latest row per name.
	latest := map[string]byoMachineRow{}
	for _, r := range rows {
		key := strings.ToLower(r.Name)
		if cur, ok := latest[key]; !ok || r.UpdatedAt > cur.UpdatedAt {
			latest[key] = r
		}
	}
	if len(latest) == 0 {
		fmt.Println("No BYO machines yet. Create one: yaver machine create <name>")
		return
	}
	names := make([]string, 0, len(latest))
	for k := range latest {
		names = append(names, k)
	}
	sort.Strings(names)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tSERVER\tIP\tSNAPSHOT")
	for _, k := range names {
		r := latest[k]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.Name, machineStateLabel(r.State), dashIfEmpty(r.ServerID), dashIfEmpty(r.ServerIP), dashIfEmpty(r.SnapshotImageID))
	}
	w.Flush()
}

func runMachineStatusCmd(args []string) {
	name, _ := parseMachinePosFlags(args)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver machine status <name>")
		os.Exit(1)
	}
	cfg := loadConfigOrExit()
	rows, err := fetchByoMachines(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine status: %v\n", err)
		os.Exit(1)
	}
	row := latestByName(rows, name, "")
	if row == nil {
		fmt.Fprintf(os.Stderr, "no machine named %q.\n", name)
		os.Exit(1)
	}
	fmt.Printf("machine   %s\n", row.Name)
	fmt.Printf("state     %s\n", machineStateLabel(row.State))
	fmt.Printf("server    %s\n", dashIfEmpty(row.ServerID))
	fmt.Printf("ip        %s\n", dashIfEmpty(row.ServerIP))
	fmt.Printf("device    %s\n", dashIfEmpty(row.DeviceID))
	fmt.Printf("plan/rgn  %s / %s\n", dashIfEmpty(row.Plan), dashIfEmpty(row.Region))
	fmt.Printf("snapshot  %s\n", dashIfEmpty(row.SnapshotImageID))
	switch row.State {
	case "stopped":
		fmt.Printf("→ `yaver machine up %s` recreates it (or `yaver codex --machine=%s` auto-wakes).\n", name, name)
	case "active":
		fmt.Printf("→ `yaver machine down %s` scales it to zero (snapshot + delete).\n", name)
	}
}

func machineStateLabel(s string) string {
	switch s {
	case "active":
		return "up"
	case "stopped":
		return "stopped (snapshot)"
	case "deleted":
		return "deleted"
	default:
		return firstNonEmpty(s, "?")
	}
}

// parseMachinePosFlags splits `<name> [--k v | --k=v | --flag]` into the
// first positional (name) and a flag map ("--purge" → "true").
func parseMachinePosFlags(args []string) (name string, flags map[string]string) {
	flags = map[string]string{}
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			key := strings.TrimPrefix(a, "--")
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				flags[key[:eq]] = key[eq+1:]
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				flags[key] = args[i+1]
				i++
			} else {
				flags[key] = "true"
			}
		default:
			if name == "" {
				name = a
			}
		}
		i++
	}
	return name, flags
}
