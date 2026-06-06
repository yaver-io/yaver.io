package main

// ops_machine_driver.go — the "Yaver for machines" driver verbs: connect a
// machine behind a uniform Driver, then VIEW (browse), WATCH (list/state/read),
// and CONTROL (write/recall/submit_job — gated) it over the mesh by deviceId.
// This is the generalization of the robot_* verbs to heterogeneous machines
// (Modbus crimpers/cut-strip lines today; OPC-UA/MQTT/S7/vision drivers next).
// See docs/yaver-for-machines-design.md.
//
// Security posture (same as ops_machine.go): opt-in via --machine, owner-only,
// reads/browse safe, writes require an explicit confirm + the driver's per-tag
// safe-range + read-back verify. Safety stays hardwired on the machine.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yaver-io/agent/machine"
	"github.com/yaver-io/agent/robot"
)

const machineDriverTimeout = 10 * time.Second

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_connect",
		Description: "Connect a machine behind a uniform Driver and register it under `id`. protocol=modbus_tcp (addr+unit+tags[]) or vision (camera+tags[] = HMI fields read by a VLM — wraps screen-only machines with no API). Returns the capability set.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id":         map[string]interface{}{"type": "string", "description": "stable machine id, e.g. yh8030h-01"},
			"protocol":   map[string]interface{}{"type": "string", "description": "modbus_tcp (default) | vision"},
			"name":       map[string]interface{}{"type": "string"},
			"kind":       map[string]interface{}{"type": "string", "description": "cut_strip|crimp|press|tester|… (free-form)"},
			"addr":       map[string]interface{}{"type": "string", "description": "modbus: host:port of the Modbus-TCP slave"},
			"unit":       map[string]interface{}{"type": "integer", "description": "modbus: unit/slave id (default 1)"},
			"statusTag":  map[string]interface{}{"type": "string", "description": "modbus: tag name whose nonzero value ⇒ running"},
			"programTag": map[string]interface{}{"type": "string", "description": "modbus: tag name written by machine_recall"},
			"tags":       map[string]interface{}{"type": "array", "description": "modbus: register map; vision: HMI field list [{name,unit2}]"},
			"schematic":  map[string]interface{}{"type": "object", "description": "modbus: a learned Schematic (sniff + machine_understand) used as the tag map"},
			"camera":     map[string]interface{}{"type": "string", "description": "vision: V4L2 device (default /dev/video0)"},
			"baseUrl":    map[string]interface{}{"type": "string", "description": "vision: OpenAI-compatible VLM base URL (default env/cloud)"},
			"apiKey":     map[string]interface{}{"type": "string", "description": "vision: VLM API key (default env)"},
			"model":      map[string]interface{}{"type": "string", "description": "vision: VLM model (default gpt-4o-mini / llama3.2-vision)"},
		}, "id"),
		Handler: machineConnectHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_list",
		Description: "List every wrapped machine on this device with its kind, capabilities, and live status (the view/watch list). Includes the robot cell if one is enabled here.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     machineListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_state",
		Description: "Live status snapshot of one wrapped machine (connected, run/idle/fault state, detail).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id": map[string]interface{}{"type": "string"},
		}, "id"),
		Handler: machineStateHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_browse",
		Description: "Enumerate a wrapped machine's addressable surface (its tag map / register map / HMI fields).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id": map[string]interface{}{"type": "string"},
		}, "id"),
		Handler: machineBrowseHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_read_tags",
		Description: "Read tags from a wrapped machine, scaled to engineering units. refs[] selects by name or address; omit refs to read the whole tag map (the current parameter set).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id":   map[string]interface{}{"type": "string"},
			"refs": map[string]interface{}{"type": "array", "description": "[{name}] or [{addr,func,unit}]; omit for all tags"},
		}, "id"),
		Handler: machineReadTagsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_write_tags",
		Description: "GATED write to a wrapped machine. Each write is value-in-engineering-units (driver applies inverse scale), bounds-checked against the tag's safe range, then read-back verified. Requires confirm=true (owner approval). Machine safety functions are never network-writable.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id":      map[string]interface{}{"type": "string"},
			"writes":  map[string]interface{}{"type": "array", "description": "[{ref:{name}, value}] — engineering units"},
			"confirm": map[string]interface{}{"type": "boolean", "description": "must be true; explicit owner approval for a live write"},
		}, "id", "writes", "confirm"),
		Handler: machineWriteTagsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_recall",
		Description: "GATED: recall a machine-stored program by number/name (CapProgram). Requires confirm=true.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id":      map[string]interface{}{"type": "string"},
			"program": map[string]interface{}{"type": "string"},
			"confirm": map[string]interface{}{"type": "boolean"},
		}, "id", "program", "confirm"),
		Handler: machineRecallHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_submit_job",
		Description: "GATED: download a job/recipe to a machine (CapJob) — sets params (engineering units) and optionally recalls a program. Requires confirm=true. Each param write is bounds-checked + read-back verified.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id":      map[string]interface{}{"type": "string"},
			"program": map[string]interface{}{"type": "string"},
			"params":  map[string]interface{}{"type": "object", "description": "{tagName: value} in engineering units"},
			"confirm": map[string]interface{}{"type": "boolean"},
		}, "id", "confirm"),
		Handler: machineSubmitJobHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_disconnect",
		Description: "Disconnect and unregister a wrapped machine (closes its connection).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"id": map[string]interface{}{"type": "string"},
		}, "id"),
		Handler: machineDisconnectHandler,
	})
}

// machineDriverFor resolves a registered driver, applying the opt-in gate and
// auto-registering the robot cell (if enabled here) so it shows on the wall.
func machineDriverFor(c OpsContext, id string) (machine.Driver, *OpsResult) {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return nil, deny
	}
	maybeRegisterRobotDriver(eng)
	d, ok := eng.GetDriver(id)
	if !ok {
		return nil, &OpsResult{OK: false, Code: "not_found", Error: "no wrapped machine with id " + id + " (call machine_connect first)"}
	}
	return d, nil
}

func machineConnectHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		ID         string        `json:"id"`
		Protocol   string        `json:"protocol"`
		Name       string        `json:"name"`
		Kind       string        `json:"kind"`
		Addr       string        `json:"addr"`
		Unit       int           `json:"unit"`
		StatusTag  string        `json:"statusTag"`
		ProgramTag string        `json:"programTag"`
		Tags       []machine.Tag `json:"tags"`
		// build the tag map from a learned Schematic (sniff + machine_understand)
		Schematic *machine.Schematic `json:"schematic"`
		// vision protocol
		Camera  string `json:"camera"`
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
		Model   string `json:"model"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	name := firstNonEmptyStr(p.Name, p.ID)
	// A learned Machine Operating Manual supplies the tag map directly; explicit
	// tags[] (if any) take precedence on name collisions.
	if p.Schematic != nil {
		learned := machine.TagsFromSchematic(*p.Schematic)
		have := map[string]bool{}
		for _, t := range p.Tags {
			have[t.Name] = true
		}
		for _, t := range learned {
			if !have[t.Name] {
				p.Tags = append(p.Tags, t)
			}
		}
	}
	var d machine.Driver
	switch p.Protocol {
	case "vision":
		d = newVisionDriver(visionDriverConfig{
			Name: name, Kind: firstNonEmptyStr(p.Kind, "vision"),
			Camera:  robot.NewGstCamera(p.Camera), // "" → /dev/video0
			Fields:  p.Tags,
			BaseURL: p.BaseURL, APIKey: p.APIKey, Model: p.Model,
		})
	case "", "modbus_tcp", "modbus":
		d = machine.NewModbusDriver(machine.ModbusConfig{
			Name: name, Kind: p.Kind, Addr: p.Addr, Unit: byte(p.Unit),
			Tags: p.Tags, StatusTag: p.StatusTag, ProgramTag: p.ProgramTag,
		})
	default:
		return OpsResult{OK: false, Code: "unsupported", Error: "protocol must be modbus_tcp or vision (more drivers coming)"}
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	if err := d.Connect(ctx); err != nil {
		return OpsResult{OK: false, Code: "connect_failed", Error: err.Error()}
	}
	eng.RegisterDriver(p.ID, d)
	return OpsResult{OK: true, Initial: map[string]any{
		"id": p.ID, "name": name, "kind": d.Kind(), "connected": true,
		"caps": d.Capabilities().List(), "tags": len(p.Tags),
	}}
}

func machineListHandler(c OpsContext, _ json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	maybeRegisterRobotDriver(eng)
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	statuses := eng.DriverStatuses(ctx, 4*time.Second)
	machines := make([]map[string]any, 0, len(statuses))
	for id, st := range statuses {
		machines = append(machines, map[string]any{"id": id, "status": st})
	}
	return OpsResult{OK: true, Initial: map[string]any{"machines": machines, "count": len(machines)}}
}

func machineStateHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	d, deny := machineDriverFor(c, p.ID)
	if deny != nil {
		return *deny
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	st, err := d.Status(ctx)
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: st}
}

func machineBrowseHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	d, deny := machineDriverFor(c, p.ID)
	if deny != nil {
		return *deny
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	tags, err := d.Browse(ctx)
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"id": p.ID, "tags": tags, "caps": d.Capabilities().List()}}
}

func machineReadTagsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID   string           `json:"id"`
		Refs []machine.TagRef `json:"refs"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	d, deny := machineDriverFor(c, p.ID)
	if deny != nil {
		return *deny
	}
	if !d.Capabilities().Has(machine.CapRead) {
		return OpsResult{OK: false, Code: "unsupported", Error: "machine has no read capability"}
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	samples, err := d.Read(ctx, p.Refs)
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"id": p.ID, "samples": samples}}
}

func machineWriteTagsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID      string             `json:"id"`
		Writes  []machine.TagWrite `json:"writes"`
		Confirm bool               `json:"confirm"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "needs_approval", Error: "live machine write requires confirm=true (owner approval)"}
	}
	if len(p.Writes) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "writes[] required"}
	}
	d, deny := machineDriverFor(c, p.ID)
	if deny != nil {
		return *deny
	}
	if !d.Capabilities().Has(machine.CapWrite) {
		return OpsResult{OK: false, Code: "unsupported", Error: "machine has no write capability"}
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	if err := d.Write(ctx, p.Writes); err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"id": p.ID, "wrote": len(p.Writes), "verified": true}}
}

func machineRecallHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID      string `json:"id"`
		Program string `json:"program"`
		Confirm bool   `json:"confirm"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "needs_approval", Error: "program recall requires confirm=true"}
	}
	d, deny := machineDriverFor(c, p.ID)
	if deny != nil {
		return *deny
	}
	if !d.Capabilities().Has(machine.CapProgram) {
		return OpsResult{OK: false, Code: "unsupported", Error: "machine has no program-recall capability"}
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	if err := d.Recall(ctx, p.Program); err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"id": p.ID, "recalled": p.Program}}
}

func machineSubmitJobHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID      string             `json:"id"`
		Program string             `json:"program"`
		Params  map[string]float64 `json:"params"`
		Confirm bool               `json:"confirm"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "needs_approval", Error: "job download requires confirm=true"}
	}
	d, deny := machineDriverFor(c, p.ID)
	if deny != nil {
		return *deny
	}
	if !d.Capabilities().Has(machine.CapJob) {
		return OpsResult{OK: false, Code: "unsupported", Error: "machine has no job-download capability"}
	}
	ctx, cancel := context.WithTimeout(c.Ctx, machineDriverTimeout)
	defer cancel()
	if err := d.SubmitJob(ctx, machine.Job{Program: p.Program, Params: p.Params}); err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"id": p.ID, "program": p.Program, "params": len(p.Params)}}
}

func machineDisconnectHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		ID string `json:"id"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if !eng.RemoveDriver(p.ID) {
		return OpsResult{OK: false, Code: "not_found", Error: "no wrapped machine with id " + p.ID}
	}
	return OpsResult{OK: true, Initial: map[string]any{"disconnected": p.ID}}
}
