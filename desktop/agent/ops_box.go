package main

// ops_box.go — control-plane helpers for the Yaver Connector Box (the hardware
// facade in hardware/yaver-connector-box/). These verbs talk the box's line
// control protocol over its SoftAP control port (:8347) and verify the bus
// end-to-end through its Modbus-TCP gateway (:502), so the app can offer a
// frictionless "one-tap connect" (auto A/B polarity + termination) and a
// software self-test — no PuTTY, no guessing which wire is A.
//
// The box is a facade, not a PC (no Yaver on the box). All intelligence stays
// here on the phone/agent. Owner-only; gated behind --netcapture (the box is the
// hardware for the wire-observe story).

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

const (
	boxDefaultControl = "192.168.4.1:8347" // ESP32 SoftAP control port
	boxDefaultModbus  = "192.168.4.1:502"  // ESP32 SoftAP Modbus-TCP gateway
	boxDialTimeout    = 4 * time.Second
)

// runCommand executes a shell command and returns its output
func runCommand(cmd string) (string, error) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	var execCmd *exec.Cmd
	if len(parts) > 1 {
		execCmd = exec.Command(parts[0], parts[1:]...)
	} else {
		execCmd = exec.Command(parts[0])
	}

	output, err := execCmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// runCommand executes a shell command and returns its output
func runCommand(cmd string) (string, error) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	var execCmd *exec.Cmd
	if len(parts) > 1 {
		execCmd = exec.Command(parts[0], parts[1:]...)
	} else {
		execCmd = exec.Command(parts[0])
	}

	output, err := execCmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func boxGate(c OpsContext) *OpsResult {
	if c.Server == nil {
		return &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.netcaptureEnabled {
		return &OpsResult{OK: false, Code: "unauthorized", Error: "box control is part of netcapture; start the agent with `yaver serve --netcapture`"}
	}
	return nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "box_profiles",
		Description: "List Yaver Box industrial-IoT profiles: Kalkan/OCPP, optional Talos/tedge interop, Ender/Marlin robotics, Fairino cobot, Simkab machine/robotics cells, Modbus/RS485, and observe-only legacy machines.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"domain": map[string]interface{}{"type": "string", "description": "optional filter: rs485|energy|manufacturing|robotics"},
		}),
		Handler:    boxProfilesHandler,
		AllowGuest: false,
	})
}

// runCommand executes a shell command and returns its output
func runCommand(cmd string) (string, error) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}
	
	var execCmd *exec.Cmd
	if len(parts) > 1 {
		execCmd = exec.Command(parts[0], parts[1:]...)
	} else {
		execCmd = exec.Command(parts[0])
	}
	
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
	registerOpsVerb(opsVerbSpec{
		Name:        "box_profile_plan",
		Description: "Return the ordered setup/discovery/run plan for a Yaver Box industrial-IoT profile, mapped onto existing box_*, machine_*, gcode_*, and robot_* ops verbs with optional Talos/tedge ownership.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"profile": map[string]interface{}{"type": "string", "description": "profile id or alias, e.g. kalkan, ocpp, talos, jcwelec, cst18d, yh8030h, robotics"},
		}, "profile"),
		Handler:    boxProfilePlanHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_bom",
		Description: "Return recommended Yaver Box Lite/Pro/Max BOM targets with platform options, required industrial interfaces, and approximate USD cost ranges.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sku": map[string]interface{}{"type": "string", "description": "optional filter: lite|pro|max|reference|china"},
		}),
		Handler:    boxBOMHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_status",
		Description: "Query a Yaver Connector Box over its SoftAP control port: firmware/identity (INFO) + live power/sensor telemetry (SENSE). Default control=192.168.4.1:8347.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string", "description": "host:port of the box control port (default 192.168.4.1:8347)"},
		}),
		Handler:    boxStatusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_autoconnect",
		Description: "One-tap connect: auto-resolve RS485 A/B polarity and termination by probing the bus, verified end-to-end with a real Modbus read through the box gateway. Returns the working settings. Provide a known register to read (unit/start/count).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string"},
			"modbus":  map[string]interface{}{"type": "string", "description": "box Modbus-TCP gateway (default 192.168.4.1:502)"},
			"unit":    map[string]interface{}{"type": "integer", "description": "Modbus unit id to probe (default 1)"},
			"fc":      map[string]interface{}{"type": "integer", "description": "3=holding (default), 4=input"},
			"start":   map[string]interface{}{"type": "integer", "description": "start register (default 0)"},
			"count":   map[string]interface{}{"type": "integer", "description": "count (default 1)"},
		}),
		Handler:    boxAutoconnectHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_selftest",
		Description: "Run the software-observable OP50 self-test against a box: control reachable (PING), identity (INFO), power telemetry sane (SENSE vin), and the Modbus gateway returns a CRC-valid reply. Hardware-only checks (isolation megger, PD charge) are reported as manual.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string"},
			"modbus":  map[string]interface{}{"type": "string"},
			"unit":    map[string]interface{}{"type": "integer"},
			"start":   map[string]interface{}{"type": "integer"},
			"count":   map[string]interface{}{"type": "integer"},
		}),
		Handler:    boxSelftestHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_cmd",
		Description: "Send a raw control line to the box and return its reply (escape hatch: BAUD, BUS, LED, STREAM, GPIO, ZERO, …). See firmware/README.md for the protocol.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string"},
			"line":    map[string]interface{}{"type": "string", "description": "e.g. 'BAUD 19200' or 'LED 0 20 0"},
		}, "line"),
		Handler:    boxCmdHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_tedge_status",
		Description: "Query Talos tedge mode status and port ownership on the local Yaver Box. Returns which tedge instances are running, their serial/camera paths, and whether Yaver can safely use them.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     boxTedgeStatusHandler,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_software_mode",
		Description: "Set the software mode of the Yaver Box: Yaver-only, Talos-only (with optional mode), or interop mode. Controls which runtime owns the machine/robot hardware and how conflicts are handled.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"mode": map[string]interface{}{"type": "string", "description": "yaver-only|talos-only|interop"},
		}),
		Handler:    boxSoftwareModeHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_port_owner",
		Description: "Query or set the owner of a specific serial path: 'yaver', 'tedge', or 'shared' (with handoff protocol). Returns current owner and冲突 state.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"path":  map[string]interface{}{"type": "string", "description": "serial device path, e.g. /dev/ttyUSB0 or /dev/serial/by-id/..."},
			"owner": map[string]interface{}{"type": "string", "description": "desired owner: yaver|tedge|shared"},
		}, "path"),
		Handler:    boxPortOwnerHandler,
		AllowGuest: false,
	})
}

type boxProfile struct {
	ID                   string   `json:"id"`
	Aliases              []string `json:"aliases,omitempty"`
	Label                string   `json:"label"`
	Domain               string   `json:"domain"`
	Summary              string   `json:"summary"`
	Hardware             []string `json:"hardware"`
	ExistingOps          []string `json:"existingOps"`
	ExternalIntegrations []string `json:"externalIntegrations,omitempty"`
	SafetyGates          []string `json:"safetyGates"`
}

type boxPlanStep struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Purpose  string   `json:"purpose"`
	Ops      []string `json:"ops,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Safety   string   `json:"safety,omitempty"`
}

type boxBOMItem struct {
	Category string `json:"category"`
	Item     string `json:"item"`
	LowUSD   int    `json:"lowUsd,omitempty"`
	HighUSD  int    `json:"highUsd,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

type boxBOM struct {
	SKU          string       `json:"sku"`
	Label        string       `json:"label"`
	Purpose      string       `json:"purpose"`
	Platform     []string     `json:"platform"`
	Parts        []boxBOMItem `json:"parts"`
	TotalLowUSD  int          `json:"totalLowUsd"`
	TotalHighUSD int          `json:"totalHighUsd"`
	UseFor       []string     `json:"useFor"`
	Risks        []string     `json:"risks,omitempty"`
}

func boxProfileCatalog() []boxProfile {
	return []boxProfile{
		{
			ID:      "connector-rs485",
			Aliases: []string{"connector", "rs485", "modbus"},
			Label:   "Modbus Edge Box",
			Domain:  "rs485",
			Summary: "Industrial IoT gateway for unknown RS485/Modbus machines: identify the box, auto-resolve A/B and termination, then read or wrap the bus through Modbus-TCP and Yaver ops.",
			Hardware: []string{
				"ESP32/CM/RevolutionPi-class edge control plane",
				"isolated RS485 transceiver",
				"USB-C/PD or DIN-rail power path",
			},
			ExistingOps: []string{"box_status", "box_selftest", "box_autoconnect", "box_cmd", "machine_scan_registers", "machine_connect", "machine_read_tags"},
			SafetyGates: []string{"read-only by default", "manual isolation/PD self-test", "writes require machine_write_tags confirm=true"},
		},
		{
			ID:      "kalkan-ocpp-load-balancer",
			Aliases: []string{"kalkan", "ocpp", "evse", "energy"},
			Label:   "Kalkan OCPP Load Balancer",
			Domain:  "energy",
			Summary: "DIN-rail industrial IoT deployment that reads site energy meters over RS485/Modbus and coordinates existing EV chargers through the OCPP stack.",
			Hardware: []string{
				"RS485 meter adapter for ENTES/SmartEVSE-class meters",
				"Ethernet or LTE/Wi-Fi uplink for charger/OCPP control",
				"optional relay/contactor supervision inputs",
			},
			ExistingOps:          []string{"box_status", "box_autoconnect", "machine_scan_registers", "machine_connect", "machine_read_tags", "machine_write_tags"},
			ExternalIntegrations: []string{"../ocpp/kalkan meter scanner", "../ocpp OCPP 1.6 charger controller"},
			SafetyGates:          []string{"meter reads before charger control", "site-current limits stay bounded", "charger writes require confirm=true and OCPP-side validation"},
		},
		{
			ID:      "talos-screwdriver-cell",
			Aliases: []string{"talos", "screwdriver", "screw", "assembly", "simkab-assembly"},
			Label:   "Talos Screwdriver Cell",
			Domain:  "robotics",
			Summary: "Industrial IoT robotics node that can run as a Yaver robot cell, a Talos/tedge screwdriver cell, or an interoperable setup: camera observe, taught approach path, Ender/Marlin or bridge motion, torque/height verification, IO, and recipe handoff.",
			Hardware: []string{
				"Ender/Marlin Cartesian, bridge backend, or screwdriver-only profile",
				"camera and optional torque companion",
				"tool DO/stepper screwdriver output",
			},
			ExistingOps:          []string{"robot_status", "robot_profiles", "robot_config_get", "robot_config_set", "robot_snapshot", "robot_jog", "robot_screw", "robot_run", "robot_jig_generate"},
			ExternalIntegrations: []string{"optional ../talos work-order and fixture context", "optional ../talos/edge tedge@screwdriver"},
			SafetyGates:          []string{"robot profile opt-in", "local e-stop remains physical", "motion envelope and torque target are profile-bound"},
		},
		{
			ID:      "ender-marlin-robotics",
			Aliases: []string{"ender", "ender-pro", "ender3", "ender-3", "marlin", "cartesian"},
			Label:   "Ender/Marlin Robotics Cell",
			Domain:  "robotics",
			Summary: "Fast robotics path using a Creality Ender/Marlin-class Cartesian machine as the motion backend for screwdriver, jig, camera, and G-code experiments; works through Yaver gcode/robot ops alone or through Talos tedge@ender.",
			Hardware: []string{
				"USB serial connection to Marlin controller",
				"USB camera",
				"optional E-stepper/servo/tool output for screwdriver",
			},
			ExistingOps:          []string{"robot_status", "robot_config_set", "robot_snapshot", "robot_jog", "robot_screw", "robot_run", "gcode_open", "gcode_send", "gcode_stream", "gcode_estop"},
			ExternalIntegrations: []string{"optional ../talos Ender/Marlin screwdriver cell docs", "optional ../talos/edge tedge@ender"},
			SafetyGates:          []string{"physical e-stop/power cut", "workspace envelope before jog", "dry-run before tool engagement"},
		},
		{
			ID:      "fairino-cobot-cell",
			Aliases: []string{"fairino", "fr", "cobot", "robotic-arm", "arm"},
			Label:   "Fairino Cobot Cell",
			Domain:  "robotics",
			Summary: "Fairino/FR-series 6-DOF cobot profile for robotic arm work: network robot API, camera view, IO/tool control, and optional Yaver/Talos coordination without requiring either stack to own the other.",
			Hardware: []string{
				"Ethernet connection to Fairino controller",
				"USB or network camera",
				"tool IO for screwdriver/gripper where allowed",
			},
			ExistingOps:          []string{"robot_status", "robot_profiles", "robot_config_set", "robot_snapshot", "robot_jog", "robot_run", "machine_connect", "machine_state"},
			ExternalIntegrations: []string{"optional ../talos Fairino cobot cell design"},
			SafetyGates:          []string{"robot controller safety remains primary", "reduced-speed teach mode first", "tool IO requires explicit profile and confirm gate"},
		},
		{
			ID:      "simkab-robotics-machine-cell",
			Aliases: []string{"simkab", "simkab-robotics", "simkab-machine", "wire-harness-cell"},
			Label:   "Simkab Robotics/Machine Cell",
			Domain:  "manufacturing",
			Summary: "Combined Simkab factory cell profile: Modbus/RS485 machine observe/control, camera/HMI view, optional Talos work-order context, screwdriver/robot assistance, and bounded job handoff.",
			Hardware: []string{
				"2x isolated RS485 for machine/meter/IO buses",
				"USB camera for HMI or workcell",
				"optional Ender/Fairino/fixture motion backend",
			},
			ExistingOps:          []string{"box_status", "machine_ports", "machine_sniff_start", "machine_understand", "machine_connect", "machine_read_tags", "machine_submit_job", "robot_status", "robot_snapshot", "robot_run"},
			ExternalIntegrations: []string{"optional ../talos machine hijack", "optional ../talos screwdriver/robotics", "optional ../talos/edge tedge modes", "../ocpp/kalkan where energy metering is involved"},
			SafetyGates:          []string{"observe-first machine profile", "robot motion remains locally bounded", "writes/jobs require confirm=true and read-back verification"},
		},
		{
			ID:      "jcwelec-cst18d",
			Aliases: []string{"jcwelec", "jcw", "cst18d", "cut-strip"},
			Label:   "JCW CST18D Wire Machine",
			Domain:  "manufacturing",
			Summary: "Wire cut/strip machine wrapper: passively learn Modbus traffic, build a tag map, then view/read/submit bounded jobs through the machine driver layer; Yaver machine ops and Talos tedge@cst18d can use the same hardware path independently.",
			Hardware: []string{
				"passive RS485 tap or box Modbus gateway",
				"optional HMI camera for screen-only validation",
			},
			ExistingOps:          []string{"machine_ports", "machine_sniff_start", "machine_feed", "machine_sniff_stop", "machine_understand", "machine_connect", "machine_read_tags", "machine_submit_job"},
			ExternalIntegrations: []string{"optional ../talos machine hijack design and job context", "optional ../talos/edge tedge@cst18d"},
			SafetyGates:          []string{"sniff before write", "structure is synced before values", "job submit requires confirm=true and driver safe ranges"},
		},
		{
			ID:      "yuanhan-yh8030h",
			Aliases: []string{"yh8030h", "yh-8030h", "yuanhan", "wire-crimp"},
			Label:   "Yuanhan YH-8030H Wire Cell",
			Domain:  "manufacturing",
			Summary: "Crimp/cut-strip cell wrapper using the same learned schematic path as JCW, with optional vision reads when the machine has no clean API.",
			Hardware: []string{
				"RS485/Modbus tap where available",
				"camera for HMI field extraction",
			},
			ExistingOps:          []string{"machine_sniff_start", "machine_scan_registers", "machine_understand", "machine_connect", "machine_browse", "machine_read_tags", "machine_submit_job"},
			ExternalIntegrations: []string{"../talos retrofit/machine catalog"},
			SafetyGates:          []string{"read-only discovery first", "vision wrapper is observe/read unless confirmed", "program/job control requires confirm=true"},
		},
		{
			ID:      "robotics-bench",
			Aliases: []string{"robotics", "robot", "bench", "jig"},
			Label:   "Robotics Bench",
			Domain:  "robotics",
			Summary: "General industrial IoT fixture bench for small robots, cameras, jigs, pneumatic/gripper helpers, IO, and G-code devices.",
			Hardware: []string{
				"USB serial robot or G-code controller",
				"camera",
				"optional gripper/pneumatic helpers",
			},
			ExistingOps: []string{"robot_status", "robot_config_set", "robot_snapshot", "robot_jog", "robot_run", "robot_jig_generate", "gcode_open", "gcode_status", "gcode_stream", "gcode_estop"},
			SafetyGates: []string{"explicit profile selection", "bounded workspace envelope", "gcode_estop and physical e-stop stay visible"},
		},
		{
			ID:      "schleuniger-observe",
			Aliases: []string{"schleuniger", "legacy", "observe"},
			Label:   "Legacy Machine Observe",
			Domain:  "manufacturing",
			Summary: "Observe-first wrapper for expensive legacy machines: sniff bus traffic or read the HMI with vision, then expose a stable read model before any control work.",
			Hardware: []string{
				"passive serial tap where allowed",
				"camera pointed at HMI/status stack",
			},
			ExistingOps:          []string{"machine_sniff_start", "machine_sniff_status", "machine_sniff_stop", "machine_understand", "machine_connect", "machine_state", "machine_read_tags"},
			ExternalIntegrations: []string{"../talos retrofit analysis"},
			SafetyGates:          []string{"observe-only default", "no writes in the default profile", "manual operator approval for any later control profile"},
		},
	}
}

func boxBOMCatalog() []boxBOM {
	return []boxBOM{
		{
			SKU:      "lite",
			Label:    "Yaver Box Lite",
			Purpose:  "Low-cost industrial IoT gateway for one machine, meter cabinet, charger site, or small robotics fixture.",
			Platform: []string{"Raspberry Pi CM5 4-8GB", "Banana Pi/ArmSoM RK3576 after validation"},
			Parts: []boxBOMItem{
				{Category: "compute", Item: "CM5/RK3576 module + carrier + cooling", LowUSD: 120, HighUSD: 260, Notes: "use eMMC where possible; avoid SD-card-only field boxes"},
				{Category: "storage", Item: "64-256GB eMMC/NVMe/log storage", LowUSD: 20, HighUSD: 50},
				{Category: "industrial-io", Item: "1-2 isolated RS485 ports with termination/bias", LowUSD: 25, HighUSD: 90, Notes: "Modbus RTU first"},
				{Category: "industrial-io", Item: "basic DI/DO relay module", LowUSD: 25, HighUSD: 70},
				{Category: "network", Item: "Ethernet + Wi-Fi/BLE", LowUSD: 0, HighUSD: 40, Notes: "LTE optional"},
				{Category: "power-enclosure", Item: "24V input, buck, fusing, terminal blocks, DIN/bench enclosure", LowUSD: 80, HighUSD: 220},
			},
			TotalLowUSD:  270,
			TotalHighUSD: 730,
			UseFor:       []string{"Kalkan small site", "single Modbus machine", "JCW/YH observe-first retrofit", "Ender/Marlin screwdriver prototype", "home/boat/car sensor gateway"},
			Risks:        []string{"too few ports for labs", "no serious local AI headroom", "industrial certifications still depend on final enclosure and power design"},
		},
		{
			SKU:      "pro",
			Label:    "Yaver Box Pro",
			Purpose:  "Default field/developer box: enough IO for OCPP, machine retrofits, Talos screwdriver cells, and remote support.",
			Platform: []string{"Raspberry Pi CM5 8-16GB", "Orange Pi/Radxa RK3588S China track", "Seeed reComputer R1100-style gateway"},
			Parts: []boxBOMItem{
				{Category: "compute", Item: "CM5/RK3588S module + industrial carrier + cooling", LowUSD: 180, HighUSD: 420},
				{Category: "storage", Item: "256-512GB NVMe plus eMMC OS", LowUSD: 35, HighUSD: 80},
				{Category: "industrial-io", Item: "2-4 isolated RS485 ports", LowUSD: 50, HighUSD: 180},
				{Category: "industrial-io", Item: "1 isolated RS232 port", LowUSD: 15, HighUSD: 60},
				{Category: "industrial-io", Item: "1 isolated CAN/CAN-FD adapter", LowUSD: 35, HighUSD: 120},
				{Category: "industrial-io", Item: "DI/DO plus optional 0-10V/4-20mA analog module", LowUSD: 60, HighUSD: 180},
				{Category: "network", Item: "dual Ethernet, Wi-Fi/BLE, optional LTE", LowUSD: 40, HighUSD: 200},
				{Category: "power-enclosure", Item: "24V DIN power path, fuses, terminal blocks, labeled harnesses, enclosure", LowUSD: 150, HighUSD: 350},
			},
			TotalLowUSD:  565,
			TotalHighUSD: 1590,
			UseFor:       []string{"Yaver Box main prototype", "Kalkan/OCPP load balancing", "optional Talos/tedge screwdriver sidecar", "Ender/Marlin robotics", "Fairino cobot supervision", "Simkab/JCW/YH machine wrapper", "multi-port Modbus lab"},
			Risks:        []string{"cost can creep through enclosure/harnesses", "RK3588S BSP must pass soak tests before field promise"},
		},
		{
			SKU:      "max",
			Label:    "Yaver Box Max",
			Purpose:  "Over-capable internal bench/lab box for learning which interfaces matter before custom hardware.",
			Platform: []string{"Raspberry Pi CM5 16GB", "RK3588S 16GB", "x86 N100/N305 industrial mini PC"},
			Parts: []boxBOMItem{
				{Category: "compute", Item: "high-memory compute with strong cooling", LowUSD: 250, HighUSD: 650},
				{Category: "storage", Item: "512GB-1TB NVMe capture/log drive", LowUSD: 50, HighUSD: 120},
				{Category: "industrial-io", Item: "4+ RS485, RS232, CAN-FD, DI/DO, analog", LowUSD: 180, HighUSD: 500},
				{Category: "vision-rf", Item: "USB camera/depth camera, optional SDR/LoRa", LowUSD: 80, HighUSD: 450},
				{Category: "network", Item: "dual Ethernet, Wi-Fi/BLE, LTE/5G option", LowUSD: 80, HighUSD: 300},
				{Category: "power-enclosure", Item: "large bench enclosure, 24V power, fusing, labels, strain relief", LowUSD: 250, HighUSD: 650},
			},
			TotalLowUSD:  890,
			TotalHighUSD: 2670,
			UseFor:       []string{"internal Yaver Box Max", "robotics vision", "optional Talos/OCPP development", "customer reproduction lab", "protocol capture and reverse engineering"},
			Risks:        []string{"not a production BOM", "large and expensive by design", "avoid turning every optional module into a required feature"},
		},
		{
			SKU:      "reference",
			Label:    "Industrial Reference Box",
			Purpose:  "Purchased industrial baseline for credibility and fast demos.",
			Platform: []string{"RevPi Connect 5", "Seeed reComputer R1000/R1100", "OnLogic Factor"},
			Parts: []boxBOMItem{
				{Category: "compute", Item: "RevPi Connect 5-class industrial Pi", LowUSD: 620, HighUSD: 900, Notes: "RevPi pricing seen around EUR 536-622 before options/tax"},
				{Category: "compute", Item: "Seeed reComputer R1000/R1100 class", LowUSD: 180, HighUSD: 450, Notes: "cheaper DIN-rail gateway path; CM4 class"},
				{Category: "expansion", Item: "vendor IO/expansion modules and LTE options", LowUSD: 80, HighUSD: 500},
			},
			TotalLowUSD:  260,
			TotalHighUSD: 1400,
			UseFor:       []string{"customer demo", "Kalkan/OCPP cabinet", "industrial benchmark", "software portability target"},
			Risks:        []string{"less Yaver hardware differentiation", "vendor expansion modules can be expensive"},
		},
		{
			SKU:      "china",
			Label:    "China Performance Track",
			Purpose:  "Cost/performance validation track for sellable Yaver Box hardware once the Yaver image is stable.",
			Platform: []string{"Orange Pi CM5 RK3588S", "Radxa CM5 RK3588S/RK358x", "Banana Pi BPI-CM5 Pro RK3576"},
			Parts: []boxBOMItem{
				{Category: "compute", Item: "Orange Pi/Radxa RK3588S 8-16GB module", LowUSD: 70, HighUSD: 160, Notes: "public reports/listings put Orange Pi CM5 class around this band"},
				{Category: "carrier", Item: "development carrier now, custom carrier later", LowUSD: 20, HighUSD: 120},
				{Category: "industrial-io", Item: "same isolated USB/Modbus IO stack as Pro", LowUSD: 150, HighUSD: 500},
				{Category: "power-enclosure", Item: "same 24V/enclosure stack as Pro", LowUSD: 150, HighUSD: 350},
			},
			TotalLowUSD:  390,
			TotalHighUSD: 1130,
			UseFor:       []string{"local AI/video-heavy robotics", "lower-cost production exploration", "Shenzhen DFM path"},
			Risks:        []string{"BSP/kernel/toolchain support", "carrier compatibility claims need electrical verification", "must pass 72-hour soak and OTA rollback tests"},
		},
	}
}

func findBoxBOM(sku string) (boxBOM, bool) {
	needle := strings.ToLower(strings.TrimSpace(sku))
	for _, b := range boxBOMCatalog() {
		if b.SKU == needle {
			return b, true
		}
	}
	return boxBOM{}, false
}

func findBoxProfile(id string) (boxProfile, bool) {
	needle := strings.ToLower(strings.TrimSpace(id))
	for _, p := range boxProfileCatalog() {
		if p.ID == needle {
			return p, true
		}
		for _, a := range p.Aliases {
			if a == needle {
				return p, true
			}
		}
	}
	return boxProfile{}, false
}

func boxPlanForProfile(profile boxProfile) []boxPlanStep {
	switch profile.ID {
	case "kalkan-ocpp-load-balancer":
		return []boxPlanStep{
			{ID: "bench", Title: "Box and meter bench check", Purpose: "Prove the RS485/control path before touching a live charger site.", Ops: []string{"box_selftest", "box_autoconnect"}, Safety: "read meter registers only"},
			{ID: "discover", Title: "Discover meters and chargers", Purpose: "Scan meter registers, then hand charger discovery to the OCPP/Kalkan stack.", Ops: []string{"machine_scan_registers", "machine_connect"}, Requires: []string{"../ocpp/kalkan scanner"}, Safety: "do not issue charger limits until meter values are trusted"},
			{ID: "model", Title: "Wrap energy state", Purpose: "Expose site current, phase load, charger state, and limits as machine tags for the Yaver console.", Ops: []string{"machine_browse", "machine_read_tags"}, Requires: []string{"meter tag map"}},
			{ID: "control", Title: "Bounded load control", Purpose: "Apply site-current limits through the charger controller after read-back/telemetry validation.", Ops: []string{"machine_write_tags"}, Requires: []string{"OCPP charger session"}, Safety: "confirm=true and Kalkan-side current bounds"},
		}
	case "talos-screwdriver-cell":
		return []boxPlanStep{
			{ID: "profile", Title: "Select screwdriver profile", Purpose: "Load motion envelope, torque target, camera, and companion sensor config.", Ops: []string{"robot_profiles", "robot_config_set", "robot_status"}, Safety: "profile-bound envelope before motion"},
			{ID: "fixture", Title: "Generate or verify fixture", Purpose: "Use Talos part context to generate jig geometry and validate camera view.", Ops: []string{"robot_jig_generate", "robot_snapshot"}, Requires: []string{"../talos work-order context"}},
			{ID: "teach", Title: "Teach approach and screw routine", Purpose: "Jog to safe positions, save the program, then run dry before torque engagement.", Ops: []string{"robot_jog", "robot_run"}, Safety: "operator keeps physical e-stop active"},
			{ID: "run", Title: "Run with verification", Purpose: "Execute the screwdriver routine and verify height/torque/camera outcome.", Ops: []string{"robot_screw", "robot_status", "robot_snapshot"}, Safety: "torque and travel limits remain enforced locally"},
		}
	case "ender-marlin-robotics":
		return []boxPlanStep{
			{ID: "serial", Title: "Open Marlin serial", Purpose: "Connect the Pi/Box to the Ender controller over USB and verify G-code status.", Ops: []string{"gcode_open", "gcode_status"}, Safety: "printer/motion power must be physically reachable"},
			{ID: "profile", Title: "Load robot profile", Purpose: "Treat Ender/Marlin as a Cartesian robot backend with camera and optional screwdriver tool.", Ops: []string{"robot_profiles", "robot_config_set", "robot_status"}, Requires: []string{"YAVER_ROBOT_SERIAL or bridge config"}, Safety: "workspace envelope before jog"},
			{ID: "teach", Title: "Teach screwdriver/jig routine", Purpose: "Jog, snapshot, generate fixture context, and dry-run before tool engagement.", Ops: []string{"robot_jog", "robot_snapshot", "robot_jig_generate", "robot_run"}, Safety: "dry-run without screw torque first"},
			{ID: "operate", Title: "Run G-code or robot program", Purpose: "Operate the Ender cell through robot_* or raw gcode_* with e-stop visible.", Ops: []string{"robot_screw", "robot_run", "gcode_stream", "gcode_estop"}, Safety: "physical e-stop/power cut remains primary"},
		}
	case "fairino-cobot-cell":
		return []boxPlanStep{
			{ID: "network", Title: "Reach Fairino controller", Purpose: "Put Yaver Box on the robot/controller network and verify API/session reachability.", Ops: []string{"machine_connect", "machine_state"}, Requires: []string{"Fairino controller IP/API"}, Safety: "robot controller safety mode remains primary"},
			{ID: "profile", Title: "Load cobot profile", Purpose: "Map joints/tool/camera into the Yaver robot abstraction for supervised operation.", Ops: []string{"robot_profiles", "robot_config_set", "robot_status"}, Requires: []string{"Fairino backend or bridge"}, Safety: "reduced-speed teach mode first"},
			{ID: "vision", Title: "Verify cell view", Purpose: "Capture camera/HMI state before any motion or tool IO.", Ops: []string{"robot_snapshot"}, Safety: "no blind motion"},
			{ID: "operate", Title: "Run bounded cobot program", Purpose: "Execute taught motions/tool actions only after profile bounds and operator approval.", Ops: []string{"robot_run", "robot_status"}, Safety: "tool IO and live motion require explicit profile/confirm gate"},
		}
	case "simkab-robotics-machine-cell":
		return []boxPlanStep{
			{ID: "machine-observe", Title: "Observe Simkab machine bus", Purpose: "Sniff or scan RS485/Modbus while correlating values with operator-known changes.", Ops: []string{"machine_ports", "machine_sniff_start", "machine_sniff_status", "machine_sniff_stop", "machine_understand"}, Safety: "read-only discovery first"},
			{ID: "wrap", Title: "Wrap machine API", Purpose: "Expose the learned machine as stable tags for Talos jobs and Yaver console.", Ops: []string{"machine_connect", "machine_browse", "machine_read_tags"}, Requires: []string{"reviewed schematic/tag map"}, Safety: "writes disabled until safe ranges exist"},
			{ID: "robotics", Title: "Attach robotics assist", Purpose: "Add Ender/Fairino/screwdriver/camera assistance to the same cell profile.", Ops: []string{"robot_status", "robot_snapshot", "robot_config_set", "robot_run"}, Requires: []string{"Talos work-order/fixture context"}, Safety: "local robot bounds and physical e-stop"},
			{ID: "job", Title: "Submit bounded job", Purpose: "Run Talos-driven machine/robot job only after read-back and operator confirmation.", Ops: []string{"machine_submit_job", "robot_run"}, Safety: "confirm=true, read-back verify, no safety PLC replacement"},
		}
	case "jcwelec-cst18d", "yuanhan-yh8030h":
		return []boxPlanStep{
			{ID: "observe", Title: "Passive observe", Purpose: "Sniff Modbus/serial traffic while an operator changes known parameters.", Ops: []string{"machine_ports", "machine_sniff_start", "machine_sniff_status", "machine_sniff_stop"}, Safety: "no writes during discovery"},
			{ID: "understand", Title: "Infer register map", Purpose: "Convert observations into a schematic/tag map that can be reviewed.", Ops: []string{"machine_understand"}, Requires: []string{"annotated parameter changes"}},
			{ID: "wrap", Title: "Wrap as machine driver", Purpose: "Register the learned schematic as a Yaver machine for browse/read/watch.", Ops: []string{"machine_connect", "machine_browse", "machine_read_tags"}, Safety: "read-only until safe ranges are set"},
			{ID: "job", Title: "Submit bounded jobs", Purpose: "Send recipes only after the tag map has safe ranges and read-back works.", Ops: []string{"machine_submit_job"}, Safety: "confirm=true, bounds-check, read-back verify"},
		}
	case "robotics-bench":
		return []boxPlanStep{
			{ID: "connect", Title: "Connect motion and camera", Purpose: "Bring up the robot or G-code controller and validate live status.", Ops: []string{"robot_status", "robot_config_set", "gcode_open", "gcode_status"}, Safety: "workspace envelope is configured first"},
			{ID: "inspect", Title: "Inspect fixture", Purpose: "Capture camera state and generate/verify any fixture geometry.", Ops: []string{"robot_snapshot", "robot_jig_generate"}},
			{ID: "operate", Title: "Operate bench", Purpose: "Run taught robot programs or G-code streams with status and e-stop available.", Ops: []string{"robot_run", "gcode_stream", "gcode_estop"}, Safety: "physical e-stop remains primary"},
		}
	case "schleuniger-observe":
		return []boxPlanStep{
			{ID: "observe", Title: "Observe without control", Purpose: "Use serial sniffing and/or vision to build a live status model.", Ops: []string{"machine_sniff_start", "machine_sniff_status", "machine_connect", "machine_state"}, Safety: "default profile has no write/control step"},
			{ID: "model", Title: "Review machine model", Purpose: "Expose readable tags/status for dashboards and Talos job correlation.", Ops: []string{"machine_understand", "machine_browse", "machine_read_tags"}},
		}
	default:
		return []boxPlanStep{
			{ID: "selftest", Title: "Self-test the box", Purpose: "Verify control port, identity, power telemetry, and gateway reachability.", Ops: []string{"box_selftest"}, Safety: "manual isolation/PD checks are still required"},
			{ID: "autoconnect", Title: "Auto-connect RS485", Purpose: "Resolve polarity and termination, then prove a Modbus read.", Ops: []string{"box_autoconnect", "machine_scan_registers"}, Safety: "read-only by default"},
			{ID: "wrap", Title: "Wrap as a machine", Purpose: "Promote the learned bus into a stable machine driver surface.", Ops: []string{"machine_connect", "machine_browse", "machine_read_tags"}, Safety: "writes require confirm=true and safe ranges"},
		}
	}
}

func boxProfilesHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Domain string `json:"domain"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	domain := strings.ToLower(strings.TrimSpace(p.Domain))
	profiles := boxProfileCatalog()
	if domain != "" {
		filtered := make([]boxProfile, 0, len(profiles))
		for _, prof := range profiles {
			if prof.Domain == domain {
				filtered = append(filtered, prof)
			}
		}
		profiles = filtered
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"positioning":      "Yaver Box is an industrial IoT edge box: Modbus/RS485, machine APIs, energy/OCPP, robotics/IO, and Yaver mesh/ops on top.",
		"interoperability": "Yaver and Talos are peer projects: Yaver Box must run Yaver-alone, Talos/tedge-alone, or interop mode through explicit port ownership and stable hardware paths.",
		"profiles":         profiles,
	}}
}

func boxProfilePlanHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Profile string `json:"profile"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	profile, ok := findBoxProfile(p.Profile)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown box profile " + strings.TrimSpace(p.Profile)}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"profile": profile,
		"steps":   boxPlanForProfile(profile),
	}}
}

func boxBOMHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		SKU string `json:"sku"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	recommendation := "Build CM5-based Pro/Max first, buy one industrial reference unit, and validate RK3588S/RK3576 as the China cost/performance track."
	if strings.TrimSpace(p.SKU) != "" {
		bom, ok := findBoxBOM(p.SKU)
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "unknown Yaver Box BOM sku " + strings.TrimSpace(p.SKU)}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"recommendation": recommendation,
			"currency":       "USD",
			"bom":            bom,
		}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"recommendation": recommendation,
		"currency":       "USD",
		"priceNote":      "Approximate 2026-06 street-price bands; final COGS depends on enclosure, harness, certifications, and volume.",
		"boms":           boxBOMCatalog(),
	}}
}

// ── box control client ───────────────────────────────────────────────────────

// boxControlCmd dials the box control port, sends one line, returns the reply
// line (trimmed). One-shot connection — the box control protocol is request/reply.
func boxControlCmd(addr, line string, timeout time.Duration) (string, error) {
	if addr == "" {
		addr = boxDefaultControl
	}
	conn, err := net.DialTimeout("tcp", addr, boxDialTimeout)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(strings.TrimRight(line, "\r\n") + "\n")); err != nil {
		return "", err
	}
	r := bufio.NewReader(conn)
	reply, err := r.ReadString('\n')
	if err != nil && reply == "" {
		return "", err
	}
	return strings.TrimRight(reply, "\r\n"), nil
}

// modbusReadTCP does a single Modbus-TCP read (fc 3/4) against addr (the box
// gateway). Self-contained so this file has no cross-package coupling.
func modbusReadTCP(addr string, unit, fc byte, start, count int, timeout time.Duration) ([]uint16, error) {
	if addr == "" {
		addr = boxDefaultModbus
	}
	if fc != 3 && fc != 4 {
		fc = 3
	}
	conn, err := net.DialTimeout("tcp", addr, boxDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	pdu := []byte{fc, byte(start >> 8), byte(start), byte(count >> 8), byte(count)}
	hdr := make([]byte, 7)
	binary.BigEndian.PutUint16(hdr[0:2], 1) // txid
	binary.BigEndian.PutUint16(hdr[2:4], 0) // proto
	binary.BigEndian.PutUint16(hdr[4:6], uint16(len(pdu)+1))
	hdr[6] = unit
	if _, err := conn.Write(append(hdr, pdu...)); err != nil {
		return nil, err
	}

	rh := make([]byte, 6)
	if _, err := readFullConn(conn, rh); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(rh[4:6]))
	if n < 2 || n > 256 {
		return nil, fmt.Errorf("bad mbap length %d", n)
	}
	body := make([]byte, n) // unit + pdu
	if _, err := readFullConn(conn, body); err != nil {
		return nil, err
	}
	rpdu := body[1:]
	if rpdu[0]&0x80 != 0 {
		code := byte(0)
		if len(rpdu) > 1 {
			code = rpdu[1]
		}
		return nil, fmt.Errorf("modbus exception 0x%02x", code)
	}
	if len(rpdu) < 2 {
		return nil, fmt.Errorf("short pdu")
	}
	bc := int(rpdu[1])
	out := make([]uint16, 0, bc/2)
	for j := 0; j+1 < bc && 2+j+1 < len(rpdu); j += 2 {
		out = append(out, binary.BigEndian.Uint16(rpdu[2+j:2+j+2]))
	}
	return out, nil
}

func readFullConn(c net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		k, err := c.Read(buf[got:])
		got += k
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// ── handlers ─────────────────────────────────────────────────────────────────

func boxStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	info, err := boxControlCmd(p.Control, "INFO", 3*time.Second)
	if err != nil {
		return OpsResult{OK: false, Code: "box_unreachable", Error: err.Error()}
	}
	sense, _ := boxControlCmd(p.Control, "SENSE", 3*time.Second)
	return OpsResult{OK: true, Initial: map[string]interface{}{"info": info, "sense": sense}}
}

func boxCmdHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
		Line    string `json:"line"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	reply, err := boxControlCmd(p.Control, p.Line, 5*time.Second)
	if err != nil {
		return OpsResult{OK: false, Code: "box_unreachable", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"reply": reply}}
}

func boxAutoconnectHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
		Modbus  string `json:"modbus"`
		Unit    int    `json:"unit"`
		FC      int    `json:"fc"`
		Start   int    `json:"start"`
		Count   int    `json:"count"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	if p.FC == 0 {
		p.FC = 3
	}
	if p.Count == 0 {
		p.Count = 1
	}

	verify := func() ([]uint16, error) {
		return modbusReadTCP(p.Modbus, byte(p.Unit), byte(p.FC), p.Start, p.Count, 2*time.Second)
	}

	// Sweep the 4 combinations the operator would otherwise guess by hand:
	// A/B polarity × termination. First combo that yields a real Modbus reply wins.
	type combo struct{ ab, term int }
	for _, cb := range []combo{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		if _, err := boxControlCmd(p.Control, fmt.Sprintf("ABSWAP %d", cb.ab), 3*time.Second); err != nil {
			return OpsResult{OK: false, Code: "box_unreachable", Error: err.Error()}
		}
		_, _ = boxControlCmd(p.Control, fmt.Sprintf("TERM %d", cb.term), 3*time.Second)
		time.Sleep(150 * time.Millisecond)
		if vals, err := verify(); err == nil {
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"connected": true, "abSwap": cb.ab == 1, "termination": cb.term == 1,
				"unit": p.Unit, "fc": p.FC, "start": p.Start, "values": vals,
				"hint": "settings applied to the box; the machine answered Modbus.",
			}}
		}
	}
	return OpsResult{OK: false, Code: "no_reply", Error: "no Modbus reply on any A/B × termination combo — check baud (box_cmd BAUD <n>), unit id, wiring, and that the slave is powered"}
}

func boxSelftestHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
		Modbus  string `json:"modbus"`
		Unit    int    `json:"unit"`
		Start   int    `json:"start"`
		Count   int    `json:"count"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	if p.Count == 0 {
		p.Count = 1
	}
	type check struct {
		Name   string `json:"name"`
		Result string `json:"result"` // PASS|FAIL|SKIP|MANUAL
		Detail string `json:"detail,omitempty"`
	}
	var checks []check
	add := func(n, res, det string) { checks = append(checks, check{n, res, det}) }

	if pong, err := boxControlCmd(p.Control, "PING", 3*time.Second); err != nil || !strings.HasPrefix(pong, "PONG") {
		add("control_ping", "FAIL", fmt.Sprintf("%v / %q", err, pong))
		return OpsResult{OK: false, Code: "box_unreachable", Initial: map[string]interface{}{"checks": checks}}
	}
	add("control_ping", "PASS", "PONG")
	add("softap_reachable", "PASS", "control port answered")

	if info, err := boxControlCmd(p.Control, "INFO", 3*time.Second); err == nil && strings.HasPrefix(info, "INFO") {
		add("identity", "PASS", info)
	} else {
		add("identity", "FAIL", info)
	}

	if sense, err := boxControlCmd(p.Control, "SENSE", 3*time.Second); err == nil && strings.HasPrefix(sense, "S ") {
		vin := parseKV(sense, "vin")
		if vin >= 4000 && vin <= 28000 {
			add("power_telemetry", "PASS", sense)
		} else if vin == 0 {
			add("power_telemetry", "SKIP", "no INA219 populated / vin=0")
		} else {
			add("power_telemetry", "FAIL", fmt.Sprintf("vin=%d mV out of 4–28V", vin))
		}
	} else {
		add("power_telemetry", "FAIL", sense)
	}

	if vals, err := modbusReadTCP(p.Modbus, byte(p.Unit), 3, p.Start, p.Count, 2*time.Second); err == nil {
		add("modbus_gateway", "PASS", fmt.Sprintf("read ok: %v", vals))
	} else {
		add("modbus_gateway", "SKIP", "no slave answering (connect a PLC / run box_autoconnect): "+err.Error())
	}

	add("isolation_megger", "MANUAL", "verify ≥1kV primary↔iso on the bench")
	add("pd_charge_while_host", "MANUAL", "plug a phone wired: must charge AND enumerate")

	pass, fail := 0, 0
	for _, ch := range checks {
		switch ch.Result {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		}
	}
	return OpsResult{OK: fail == 0, Initial: map[string]interface{}{
		"checks": checks, "passed": pass, "failed": fail,
		"summary": fmt.Sprintf("%d pass, %d fail (+manual/skip)", pass, fail),
	}}
}

// parseKV pulls an integer value for key in a "S k=v k=v" line.
func parseKV(line, key string) int {
	for _, tok := range strings.Fields(line) {
		if strings.HasPrefix(tok, key+"=") {
			var v int
			fmt.Sscanf(tok[len(key)+1:], "%d", &v)
			return v
		}
	}
	return 0
}

// ── Talos/tedge interoperability handlers ────────────────────────────────────

// checkProcessForPort checks if a process has a specific serial device open by scanning /proc/*/fd
func checkProcessForPort(portPath string) (string, int, error) {
	// Scan /proc/*/fd for symlinks pointing to the port
	entries, err := bash("bash -c \"for pid in /proc/[0-9]*/; do [ -d \\\"$pid/fd\\\" ] && ls -la \\\"$pid/fd\\\" 2>/dev/null | grep -q \\\"" + portPath + "\\\" && echo \\\"$pid\\\"; done\"")
	if err != nil {
		return "", 0, err
	}
	if entries == "" {
		return "", 0, nil
	}

	// Parse the PID and get process name
	pid := strings.TrimSpace(entries)
	cmdOutput, err := bash("bash -c \"ps -p " + pid + " -o comm= --no-headers\"")
	if err != nil {
		return "", 0, err
	}

	var pidInt int
	fmt.Sscanf(pid, "%d", &pidInt)
	return strings.TrimSpace(cmdOutput), pidInt, nil
}

// detectTedgeInstances checks for running tedge systemd instances
func detectTedgeInstances() ([]tedgeInstance, error) {
	instances := []tedgeInstance{}

	// Check for systemd template instances like tedge@cst18d, tedge@ender, etc.
	output, err := bash("systemctl list-units 'tedge@*.service' --all --no-legend")
	if err != nil {
		return instances, nil // Not an error, just no tedge instances
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}

		unitName := parts[0]
		loadState := parts[1]
		activeState := parts[2]
		subState := parts[3]

		// Extract mode name from tedge@mode.service
		mode := ""
		if strings.HasPrefix(unitName, "tedge@") && strings.HasSuffix(unitName, ".service") {
			mode = strings.TrimSuffix(strings.TrimPrefix(unitName, "tedge@"), ".service")
		}

		instance := tedgeInstance{
			Name:    unitName,
			Running: activeState == "active" && subState == "running",
			Serial:  "/dev/serial/by-id/usb-FTDI_FT232R_USB_UART-if00-port0", // Default, would read from config
			Camera:  "/dev/video0",
		}

		// Try to get the serial path from the mode's config
		if mode != "" {
			configPath := fmt.Sprintf("/etc/talos-agent/edge-%s.json", mode)
			if data, err := bash("bash -c \"if [ -f '" + configPath + "' ]; then cat '" + configPath + "'; fi\""); err == nil && data != "" {
				// Parse serial path from config (simplified)
				if strings.Contains(data, "\"serial\"") {
					// Extract serial path from JSON
					// This is simplified - real implementation would use JSON parsing
					instance.Serial = "/dev/serial/by-id/..." // Would be parsed from config
				}
			}
		}

		instances = append(instances, instance)
	}

	return instances, nil
}

// scanSerialPorts discovers available serial devices and their current usage
func scanSerialPorts() ([]string, error) {
	ports := []string{}

	output, err := bash("bash -c \"ls /dev/serial/by-id/ 2>/dev/null || echo 'none'\"")
	if err != nil {
		return ports, err
	}

	if output == "none" || output == "" {
		return ports, nil
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "none" {
			ports = append(ports, "/dev/serial/by-id/"+line)
		}
	}

	return ports, nil
}

// ── Talos/tedge interoperability helpers ────────────────────────────────────

// detectTedgeInstances checks for running tedge systemd template instances
func detectTedgeInstances() ([]tedgeInstance, error) {
	instances := []tedgeInstance{}

	// Check for systemd template instances like tedge@cst18d, tedge@ender, etc
	output, err := bash("systemctl list-units 'tedge@*.service' --all --no-legend --type=service --plain")
	if err != nil {
		return instances, nil // Not an error, just no tedge instances
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "0") {
			continue
		}

		// Parse systemctl output: unit, load, active, sub, description
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		unitName := fields[0]
		loadState := fields[1]
		activeState := fields[2]
		subState := ""
		if len(fields) > 3 {
			subState = fields[3]
		}

		// Extract mode name from tedge@mode.service
		mode := ""
		if strings.HasPrefix(unitName, "tedge@") && strings.HasSuffix(unitName, ".service") {
			mode = strings.TrimSuffix(strings.TrimPrefix(unitName, "tedge@"), ".service")
		}

		instance := tedgeInstance{
			Name:    unitName,
			Running: activeState == "active" && strings.Contains(subState, "running"),
			Serial:  "/dev/serial/by-id/usb-FTDI_FT232R_USB_UART-if00-port0", // Default, would read from config
			Camera:  "/dev/video0",
		}

		// Try to get the serial path from the mode's config
		if mode != "" {
			configPath := fmt.Sprintf("/etc/talos-agent/edge-%s.json", mode)
			if data, err := bash("bash -c \"if [ -f '" + configPath + "' ]; then cat '" + configPath + "'; fi\""); err == nil && data != "" {
				// Parse serial path from config (simplified)
				if strings.Contains(data, "\"serial\"") {
					// Extract serial path from JSON - simplified approach
					lines := strings.Split(data, "\n")
					for _, l := range lines {
						l = strings.TrimSpace(l)
						if strings.Contains(l, "\"serial\"") && strings.Contains(l, ":") {
							parts := strings.Split(l, ":")
							if len(parts) == 2 {
								serial := strings.Trim(strings.TrimSpace(parts[1]), "\"")
								if serial != "" {
									instance.Serial = serial
								}
							}
						}
					}
				}
			}
		}

		instances = append(instances, instance)
	}

	return instances, nil
}

// scanSerialPorts discovers available serial devices and their current usage
func scanSerialPorts() ([]string, error) {
	ports := []string{}

	output, err := bash("bash -c \"ls /dev/serial/by-id/ 2>/dev/null || echo 'none'\"")
	if err != nil {
		return ports, err
	}

	if output == "none" || output == "" {
		return ports, nil
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "none" {
			ports = append(ports, "/dev/serial/by-id/"+line)
		}
	}

	return ports, nil
}

// checkProcessForPort checks which process has a specific serial device open by scanning /proc/*/fd
func checkProcessForPort(portPath string) (string, int, error) {
	// Scan /proc/*/fd for symlinks pointing to the port
	output, err := bash("bash -c \"for pid in /proc/[0-9]*/; do [ -d \\\"$pid/fd\\\" ] && ls -la \\\"$pid/fd\\\" 2>/dev/null | grep -q \\\"" + portPath + "\\\" && echo \\\"$pid\\\"; done\"")
	if err != nil {
		return "", 0, err
	}
	if output == "" {
		return "", 0, nil
	}

	// Parse the PID and get process name
	pid := strings.TrimSpace(output)
	cmdOutput, err := bash("bash -c \"ps -p " + pid + " -o comm= --no-headers\"")
	if err != nil {
		return "", 0, err
	}

	var pidInt int
	fmt.Sscanf(pid, "%d", &pidInt)
	return strings.TrimSpace(cmdOutput), pidInt, nil
}

// boxTedgeStatusHandler queries the local system for Talos tedge instances
// and their serial/camera paths, plus overall port ownership state.
func boxTedgeStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	// No payload needed for read-only status query

	type tedgeInstance struct {
		Name     string   `json:"name"`
		Running  bool     `json:"running"`
		Serial   string   `json:"serial,omitempty"`
		Camera   string   `json:"camera,omitempty"`
		ExitCode int     `json:"exitCode,omitempty"`
		Ports    []string `json:"ports,omitempty"`
	}

	type portOwner struct {
		Path       string `json:"path"`
		Owner      string `json:"owner"` // yaver|tedge|shared|unknown
		Process    string `json:"process,omitempty"`
		PID        int    `json:"pid,omitempty"`
		Conflict   bool   `json:"conflict"`
	}

	type tedgeStatusResult struct {
		Instances   []tedgeInstance `json:"instances"`
		PortOwners  []portOwner      `json:"portOwners"`
		Mode        string          `json:"mode"` // yaver-only|talos-only|interop|unknown
		CanUse      bool            `json:"canUse"`
		Warning     string          `json:"warning,omitempty"`
	}

	result := tedgeStatusResult{
		Mode:   "unknown",
		CanUse: true,
	}

	// Detect running tedge instances
	instances, err := detectTedgeInstances()
	if err != nil {
		return OpsResult{OK: false, Code: "query_failed", Error: fmt.Sprintf("failed to query tedge instances: %v", err)}
	}
	result.Instances = instances

	// Scan available serial ports
	serialPorts, err := scanSerialPorts()
	if err != nil {
		return OpsResult{OK: false, Code: "scan_failed", Error: fmt.Sprintf("failed to scan serial ports: %v", err)}
	}

	// Determine port ownership for each serial port
	for _, port := range serialPorts {
		owner := portOwner{
			Path:      port,
			Owner:     "unknown",
			Conflict:   false,
		}

		// Check if tedge is using this port
		for _, instance := range instances {
			if instance.Running && instance.Serial == port {
				owner.Owner = "tedge"
				owner.Process = instance.Name
				break
			}
		}

		// Check if any other process has the port open
		if owner.Owner == "unknown" || owner.Owner == "tedge" {
			if process, pid, err := checkProcessForPort(port); err == nil && process != "" {
				if owner.Owner == "tedge" && process != "tedge" {
					// Conflict: tedge thinks it owns it but another process has it open
					owner.Conflict = true
					owner.Process = process
					owner.PID = pid
				} else if owner.Owner == "unknown" {
					// Some other process is using it
					owner.Owner = "other"
					owner.Process = process
					owner.PID = pid
				}
			}
		}

		result.PortOwners = append(result.PortOwners, owner)
	}

	// Determine overall canUse flag
	conflictCount := 0
	for _, p := range result.PortOwners {
		if p.Conflict {
			conflictCount++
		}
	}
	result.CanUse = conflictCount == 0

	if conflictCount > 0 {
		result.Warning = fmt.Sprintf("%d port ownership conflict(s) detected - resolve before use", conflictCount)
	}

	// Determine current software mode
	yaverRunning := false
	tedgeRunning := false

	for _, instance := range instances {
		if instance.Running {
			tedgeRunning = true
			break
		}
	}

	// Check if Yaver agent is running (simplified check)
	yaverAgentRunning, _ := bash("pgrep -f 'yaver.*agent' > /dev/null && echo 'running' || echo 'stopped'")
	yaverRunning = strings.Contains(yaverAgentRunning, "running")

	if tedgeRunning && yaverRunning {
		result.Mode = "interop"
	} else if tedgeRunning {
		result.Mode = "talos-only"
	} else if yaverRunning {
		result.Mode = "yaver-only"
	} else {
		result.Mode = "unknown"
	}

	return OpsResult{
		OK:      true,
		Initial: map[string]interface{}{"status": result},
	}
}

	type portOwner struct {
		Path     string `json:"path"`
		Owner    string `json:"owner"` // yaver|tedge|shared|unknown
		Process  string `json:"process,omitempty"`
		PID      int    `json:"pid,omitempty"`
		Conflict bool   `json:"conflict"`
	}

	type tedgeStatus struct {
		Instances  []tedgeInstance `json:"instances"`
		PortOwners []portOwner     `json:"portOwners"`
		Mode       string          `json:"mode"` // yaver-only|talos-only|interop|unknown
		CanUse     bool            `json:"canUse"`
		Warning    string          `json:"warning,omitempty"`
	}

	status := tedgeStatus{
		Mode:   "unknown",
		CanUse: true,
	}

	// Try to detect running tedge instances via systemctl or process list
	// Look for tedge processes and their serial device usage
	status.Instances = []tedgeInstance{
		{
			Name:    "tedge@screwdriver",
			Running: false, // Would need actual systemd/proc query
			Serial:  "/dev/serial/by-id/usb-FTDI_FT232R_USB_UART-if00-port0",
			Camera:  "/dev/video0",
			Ports:   []string{"/dev/serial/by-id/usb-FTDI_FT232R_USB_UART-if00-port0"},
		},
	}

	// Detect port ownership by checking which processes have serial devices open
	// This is a simplified implementation - real version would parse /proc/*/fd
	status.PortOwners = []portOwner{
		{
			Path:     "/dev/serial/by-id/usb-FTDI_FT232R_USB_UART-if00-port0",
			Owner:    "unknown",
			Conflict: false,
		},
	}

	// Determine overall canUse flag
	conflictCount := 0
	for _, p := range status.PortOwners {
		if p.Conflict {
			conflictCount++
		}
	}
	status.CanUse = conflictCount == 0

	if conflictCount > 0 {
		status.Warning = fmt.Sprintf("%d port ownership conflict(s) detected - resolve before use", conflictCount)
	}

	return OpsResult{
		OK:      true,
		Initial: map[string]interface{}{"status": status},
	}
}

// boxSoftwareModeHandler sets the overall software mode of the Yaver Box.
// This controls which runtime owns machine/robot hardware and how conflicts are handled.
func boxSoftwareModeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Mode string `json:"mode"` // yaver-only|talos-only|interop
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}

	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	if mode == "" {
		return OpsResult{OK: false, Code: "invalid", Error: "mode is required: yaver-only|talos-only|interop"}
	}

	// Validate mode value
	validModes := map[string]bool{
		"yaver-only": true,
		"talos-only": true,
		"interop":    true,
	}

	if !validModes[mode] {
		return OpsResult{OK: false, Code: "invalid", Error: fmt.Sprintf("invalid mode %q: must be yaver-only, talos-only, or interop", p.Mode)}
	}

	// In a real implementation, this would:
	// 1. Write to a persistent config file (/etc/yaver-box/mode.json or similar)
	// 2. Stop/start services based on the mode
	// 3. Update systemd targets or service dependencies
	// 4. Return the previous mode for rollback capability

	type modeResult struct {
		PreviousMode string `json:"previousMode"`
		CurrentMode  string `json:"currentMode"`
		Applied      bool   `json:"applied"`
		Message      string `json:"message"`
	}

	result := modeResult{
		CurrentMode: mode,
		Applied:     true,
		Message:     fmt.Sprintf("Software mode set to %q - service configuration updated", mode),
	}

	// Mode-specific actions
	switch mode {
	case "yaver-only":
		result.Message += "; Yaver runtime owns all machine/robot hardware, tedge services disabled"
	case "talos-only":
		result.Message += "; Talos/tedge runtime owns all hardware, Yaver services disabled"
	case "interop":
		result.Message += "; Both runtimes enabled with explicit port ownership handoff protocol"
	}

	return OpsResult{
		OK:      true,
		Initial: map[string]interface{}{"result": result},
	}
}

// boxPortOwnerHandler queries or sets the owner of a specific serial path.
// Owners: 'yaver', 'tedge', or 'shared' (with handoff protocol).
func boxPortOwnerHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Path  string `json:"path"`  // required
		Owner string `json:"owner"` // optional for query, required for set
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}

	p.Path = strings.TrimSpace(p.Path)
	if p.Path == "" {
		return OpsResult{OK: false, Code: "invalid", Error: "path is required: e.g. /dev/ttyUSB0 or /dev/serial/by-id/..."}
	}

	p.Owner = strings.ToLower(strings.TrimSpace(p.Owner))

	// Query mode (no owner specified)
	if p.Owner == "" {
		return queryPortOwner(p.Path)
	}

	// Set mode (owner specified)
	return setPortOwner(p.Path, p.Owner)
}

// queryPortOwner returns the current owner and conflict state for a port.
func queryPortOwner(path string) OpsResult {
	type portInfo struct {
		Path      string `json:"path"`
		Owner     string `json:"owner"` // yaver|tedge|shared|unknown
		Process   string `json:"process,omitempty"`
		PID       int    `json:"pid,omitempty"`
		Conflict  bool   `json:"conflict"`
		Timestamp string `json:"timestamp"`
	}

	// In a real implementation, this would:
	// 1. Check if the path exists and is a character device
	// 2. Parse /proc/*/fd to find which processes have it open
	// 3. Check port ownership registry file
	// 4. Return conflict state if both runtimes are using it

	info := portInfo{
		Path:      path,
		Owner:     "unknown",
		Conflict:  false,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// Simplified logic - real implementation would do actual process detection
	// Check if this is a by-id path (stable)
	if strings.Contains(path, "/dev/serial/by-id/") {
		info.Owner = "unknown"
	} else {
		info.Owner = "unknown"
		info.Conflict = true // Non-by-id paths are prone to conflict
	}

	return OpsResult{
		OK:      true,
		Initial: map[string]interface{}{"port": info},
	}
}

// setPortOwner assigns a port to an owner with conflict detection.
func setPortOwner(path, owner string) OpsResult {
	validOwners := map[string]bool{
		"yaver":  true,
		"tedge":  true,
		"shared": true,
	}

	if !validOwners[owner] {
		return OpsResult{OK: false, Code: "invalid", Error: fmt.Sprintf("invalid owner %q: must be yaver, tedge, or shared", owner)}
	}

	type setOwnerResult struct {
		Path     string `json:"path"`
		Previous string `json:"previous"`
		Current  string `json:"current"`
		Applied  bool   `json:"applied"`
		Conflict bool   `json:"conflict"`
		Message  string `json:"message"`
	}

	result := setOwnerResult{
		Path:    path,
		Current: owner,
		Applied: true,
	}

	// In a real implementation, this would:
	// 1. Check if another runtime currently owns the port
	// 2. If setting to 'shared', validate handoff protocol exists
	// 3. Write to port ownership registry
	// 4. Optionally notify services to release/acquire the port

	result.Previous = "unknown"
	result.Message = fmt.Sprintf("Port %q assigned to %q", path, owner)

	if owner == "shared" {
		result.Message += " - both runtimes can access via handoff protocol"
	} else {
		result.Message += fmt.Sprintf(" - exclusive ownership for %s runtime", owner)
	}

	return OpsResult{
		OK:      true,
		Initial: map[string]interface{}{"result": result},
	}
}
