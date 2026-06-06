package main

// machine_edge_loop.go — the durable edge worker for the Talos machine case.
//
// One-off `machine_read` / `machine_sync` ops verbs are fine for interactive
// pokes over the relay, but a plant wants the Pi to CONTINUOUSLY observe its
// PLC and feed Talos as a historian — reboot-durable, no human in the loop. A
// relay round-trip per poll is the wrong shape; the loop must live ON the edge.
//
// This is that loop, wired to run as a companion durable service (systemd user
// unit with Restart=always + enable-linger, via desktop/agent/managed_units.go).
// `yaver machine companion ...` emits the ready-to-paste yaver.companion.yaml.
//
//	yaver machine edge-loop --device /dev/ttyUSB0 --baud 9600 \
//	    --start 0 --count 8 --interval 10s \
//	    --talos-url <url> --org-id <id> --org-secret <secret> \
//	    --device-id pi-edge-001 --machine-key wire-machine-1 [--understand]
//
// TCP PLC: pass --addr host:port instead of --device.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yaver-io/agent/machine"
)

type edgeLoopConfig struct {
	device, addr            string
	baud, unit, fc          int
	start, count            int
	interval                time.Duration
	talosURL, orgID, secret string
	deviceID, name          string
	machineID, machineKey   string
	understand              bool
	understandURL, model    string
	once                    bool
}

func runMachineEdgeLoop(args []string) {
	fs := flag.NewFlagSet("machine edge-loop", flag.ExitOnError)
	device := fs.String("device", "", "serial device for Modbus-RTU (e.g. /dev/ttyUSB0 or /dev/serial/by-id/...)")
	addr := fs.String("addr", "", "host:port for Modbus-TCP (use instead of --device)")
	baud := fs.Int("baud", 9600, "RTU baud")
	unit := fs.Int("unit", 1, "Modbus unit/slave id")
	fc := fs.Int("fc", 3, "function code: 3=holding, 4=input")
	start := fs.Int("start", 0, "first register")
	count := fs.Int("count", 8, "register count")
	interval := fs.Duration("interval", 10*time.Second, "poll/sync cadence")
	talosURL := fs.String("talos-url", "", "Talos machine-edge base URL (or env TALOS_MACHINE_URL/TALOS_CONVEX_SITE_URL)")
	orgID := fs.String("org-id", "", "Talos org id (or env TALOS_ORG_ID)")
	secret := fs.String("org-secret", "", "Talos org sync secret (or env TALOS_ORG_SECRET)")
	deviceID := fs.String("device-id", "", "edge device id reported to Talos")
	name := fs.String("name", "", "human name for the device")
	machineID := fs.String("machine-id", "", "Talos machine id (telemetry target)")
	machineKey := fs.String("machine-key", "", "schematic/machine key")
	understand := fs.Bool("understand", false, "run AI understand on the schematic once before streaming telemetry")
	understandURL := fs.String("understand-url", "", "inference base URL for --understand (or env YAVER_MACHINE_UNDERSTAND_URL)")
	model := fs.String("understand-model", "", "model for --understand")
	once := fs.Bool("once", false, "run a single cycle and exit (smoke test)")
	_ = fs.Parse(args)

	cfg := edgeLoopConfig{
		device: strings.TrimSpace(*device), addr: strings.TrimSpace(*addr),
		baud: *baud, unit: *unit, fc: *fc, start: *start, count: *count,
		interval: *interval,
		talosURL: firstNonEmptyStr(*talosURL, os.Getenv("TALOS_MACHINE_URL"), os.Getenv("TALOS_CONVEX_SITE_URL")),
		orgID:    firstNonEmptyStr(*orgID, os.Getenv("TALOS_ORG_ID")),
		secret:   firstNonEmptyStr(*secret, os.Getenv("TALOS_ORG_SECRET")),
		deviceID: strings.TrimSpace(*deviceID), name: strings.TrimSpace(*name),
		machineID: strings.TrimSpace(*machineID), machineKey: strings.TrimSpace(*machineKey),
		understand: *understand, understandURL: strings.TrimSpace(*understandURL), model: strings.TrimSpace(*model),
		once: *once,
	}
	if cfg.device == "" && cfg.addr == "" {
		fmt.Fprintln(os.Stderr, "machine edge-loop: need --device (RTU) or --addr (TCP)")
		os.Exit(2)
	}
	if cfg.deviceID == "" || cfg.talosURL == "" || cfg.orgID == "" || cfg.secret == "" {
		fmt.Fprintln(os.Stderr, "machine edge-loop: need --device-id + Talos --talos-url/--org-id/--org-secret (env fallbacks supported)")
		os.Exit(2)
	}
	cfg.talosURL = strings.TrimRight(cfg.talosURL, "/")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := edgeLoopRun(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "machine edge-loop: %v\n", err)
		os.Exit(1)
	}
}

// edgeLoopRun is the testable core: read → (optionally understand once) → sync,
// every interval, until the context is cancelled.
func edgeLoopRun(ctx context.Context, cfg edgeLoopConfig) error {
	eng, err := machine.New()
	if err != nil {
		return err
	}
	transport := "modbus_tcp"
	if cfg.device != "" {
		transport = "modbus_rtu"
	}
	fmt.Printf("[edge-loop] %s start=%d count=%d every %s → Talos %s (device %s)\n",
		transport, cfg.start, cfg.count, cfg.interval, cfg.talosURL, cfg.deviceID)

	var manualSent bool
	cycle := func() error {
		sch, vals, rerr := edgeReadOnce(eng, cfg)
		if rerr != nil {
			return rerr
		}
		// On the first cycle, optionally AI-label the schematic, then push it
		// as the machine "manual" (register map). Subsequent cycles stream
		// telemetry only.
		if !manualSent {
			if cfg.understand {
				if cfg.understandURL != "" {
					_ = os.Setenv("YAVER_MACHINE_UNDERSTAND_URL", cfg.understandURL)
				}
				if labelled, uerr := machineUnderstandLLM(ctx, cfg.understandURL, "", cfg.model, sch, nil); uerr == nil {
					sch = labelled
				} else {
					fmt.Printf("[edge-loop] understand skipped: %v\n", uerr)
				}
			}
			if err := edgeSyncManual(ctx, cfg, sch); err != nil {
				return err
			}
			manualSent = true
		}
		return edgeSyncTelemetry(ctx, cfg, sch, vals)
	}

	if cfg.once {
		return cycle()
	}
	t := time.NewTicker(cfg.interval)
	defer t.Stop()
	if err := cycle(); err != nil {
		fmt.Printf("[edge-loop] cycle error: %v\n", err)
	}
	for {
		select {
		case <-ctx.Done():
			fmt.Println("[edge-loop] shutting down")
			return nil
		case <-t.C:
			if err := cycle(); err != nil {
				fmt.Printf("[edge-loop] cycle error: %v\n", err)
			}
		}
	}
}

// edgeReadOnce reads the configured register window (RTU or TCP) and returns a
// one-shot schematic + the raw values for telemetry.
func edgeReadOnce(eng *machine.Engine, cfg edgeLoopConfig) (machine.Schematic, []uint16, error) {
	if cfg.device != "" {
		sch, err := eng.ScanRTU(cfg.device, cfg.baud, byte(cfg.unit), byte(cfg.fc), cfg.start, cfg.count, machineHTTPTimeout)
		if err != nil {
			return sch, nil, err
		}
		vals := make([]uint16, len(sch.Registers))
		for i := range sch.Registers {
			vals[i] = sch.Registers[i].Last
		}
		return sch, vals, nil
	}
	sch, err := eng.ScanTCP(cfg.addr, byte(cfg.unit), byte(cfg.fc), cfg.start, cfg.count, machineHTTPTimeout)
	if err != nil {
		return sch, nil, err
	}
	vals := make([]uint16, len(sch.Registers))
	for i := range sch.Registers {
		vals[i] = sch.Registers[i].Last
	}
	return sch, vals, nil
}

func edgeSyncManual(ctx context.Context, cfg edgeLoopConfig, sch machine.Schematic) error {
	man := map[string]any{
		"orgId": cfg.orgID, "deviceId": cfg.deviceID,
		"machineKey":  firstNonEmptyStr(cfg.machineKey, sch.MachineKey),
		"driver":      sch.Driver,
		"registers":   sch.Registers,
		"confidence":  sch.Confidence,
		"learnedFrom": sch.Source,
	}
	if _, err := machinePost(ctx, cfg.talosURL+"/machine-edge/manual", cfg.secret, man); err != nil {
		return fmt.Errorf("manual: %w", err)
	}
	// Heartbeat alongside the manual so Talos marks the device live.
	hb := map[string]any{
		"orgId": cfg.orgID, "deviceId": cfg.deviceID, "name": firstNonEmptyStr(cfg.name, cfg.deviceID),
		"machineKey": cfg.machineKey, "protocol": "modbus", "transport": "yaver",
		"capabilities": []string{"read", "sniff", "understand"},
	}
	if cfg.machineID != "" {
		hb["machineId"] = cfg.machineID
	}
	_, _ = machinePost(ctx, cfg.talosURL+"/machine-edge/heartbeat", cfg.secret, hb)
	return nil
}

func edgeSyncTelemetry(ctx context.Context, cfg edgeLoopConfig, sch machine.Schematic, vals []uint16) error {
	sample := map[string]any{"registers": registerValueMap(sch, vals)}
	tel := map[string]any{
		"orgId": cfg.orgID, "deviceId": cfg.deviceID, "machineId": cfg.machineID,
		"samples": []any{sample},
	}
	if _, err := machinePost(ctx, cfg.talosURL+"/machine-edge/telemetry", cfg.secret, tel); err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	return nil
}

// registerValueMap keys each value by its learned name when available, else by
// "addr<N>", so Talos telemetry is human-readable once a schematic is labelled.
func registerValueMap(sch machine.Schematic, vals []uint16) map[string]uint16 {
	out := map[string]uint16{}
	for i, v := range vals {
		key := fmt.Sprintf("addr%d", i)
		if i < len(sch.Registers) {
			if name := strings.TrimSpace(sch.Registers[i].Name); name != "" {
				key = name
			} else {
				key = fmt.Sprintf("addr%d", sch.Registers[i].Addr)
			}
		}
		out[key] = v
	}
	return out
}

// emitCompanionManifest prints a ready-to-paste yaver.companion.yaml that runs
// this edge-loop as a reboot-durable systemd user service.
func emitCompanionManifest(args []string) {
	fs := flag.NewFlagSet("machine companion", flag.ExitOnError)
	project := fs.String("project", "machine-edge", "companion project name")
	device := fs.String("device", "/dev/ttyUSB0", "serial device (RTU) — or use --addr")
	addr := fs.String("addr", "", "host:port (TCP)")
	baud := fs.Int("baud", 9600, "RTU baud")
	start := fs.Int("start", 0, "first register")
	count := fs.Int("count", 8, "register count")
	interval := fs.String("interval", "10s", "poll cadence")
	deviceID := fs.String("device-id", "pi-edge-001", "edge device id")
	machineKey := fs.String("machine-key", "wire-machine-1", "schematic key")
	_ = fs.Parse(args)

	conn := "--device " + *device + " --baud " + itoaInt(*baud)
	if strings.TrimSpace(*addr) != "" {
		conn = "--addr " + *addr
	}
	manifest := `# yaver.companion.yaml — durable Talos edge worker.
# Apply with:  yaver companion up   (or the companion_up MCP verb)
# It becomes a systemd user unit (Restart=always + enable-linger) that survives
# reboot AND agent downtime. Talos creds come from this box's vault, never Convex.
project: ` + *project + `
env_from:
  - vault: machine        # provides TALOS_MACHINE_URL / TALOS_ORG_ID / TALOS_ORG_SECRET
services:
  - name: edge-loop
    durable: true
    command: yaver
    args:
      - machine
      - edge-loop
      - ` + strings.Join(strings.Fields(conn), "\n      - ") + `
      - --start
      - "` + itoaInt(*start) + `"
      - --count
      - "` + itoaInt(*count) + `"
      - --interval
      - ` + *interval + `
      - --device-id
      - ` + *deviceID + `
      - --machine-key
      - ` + *machineKey + `
      - --understand
`
	fmt.Print(manifest)
}

func itoaInt(n int) string { return fmt.Sprintf("%d", n) }
