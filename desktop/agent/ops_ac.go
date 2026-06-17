package main

// ops_ac.go — WiFi AC ops verbs (docs/yaver-single-kumanda.md §5). Register an
// AC by its LOCAL credentials (Tuya devid+localkey / Gree host), then set state
// or read it — no vendor cloud. Creds live in the vault (ac.go). AC is stateful,
// so it's set_state, not logical keys.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ac_add",
		Description: "Register a WiFi AC. Payload {id, name?, kind, host, devid?, localkey?, version?}. kind: tuya (local) | gree. Tuya needs devid+localkey (from the Tuya app/IoT); stored in the vault, never Convex.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"id", "kind", "host"}, "properties": map[string]interface{}{
			"id":       map[string]interface{}{"type": "string"},
			"name":     map[string]interface{}{"type": "string"},
			"kind":     map[string]interface{}{"type": "string"},
			"host":     map[string]interface{}{"type": "string"},
			"devid":    map[string]interface{}{"type": "string"},
			"localkey": map[string]interface{}{"type": "string"},
			"version":  map[string]interface{}{"type": "string"},
		}},
		Handler: acAddHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ac_list",
		Description: "List registered ACs (no secrets — local key is never returned).",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler:     acListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ac_remove",
		Description: "Remove a registered AC. Payload {id}.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}},
		Handler:     acRemoveHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ac_set",
		Description: "Set AC state. Payload {id, power?, mode?, temp?, fan?, swing?}. e.g. {id:'bedroom', power:true, mode:'cool', temp:22, fan:'auto'}.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{
			"id":    map[string]interface{}{"type": "string"},
			"power": map[string]interface{}{"type": "boolean"},
			"mode":  map[string]interface{}{"type": "string"},
			"temp":  map[string]interface{}{"type": "integer"},
			"fan":   map[string]interface{}{"type": "string"},
			"swing": map[string]interface{}{"type": "boolean"},
		}},
		Handler: acSetHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ac_status",
		Description: "Read an AC's current state. Payload {id}.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}},
		Handler:     acStatusHandler,
	})
}

func acAddHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var d acDevice
	if err := json.Unmarshal(payload, &d); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	d.ID, d.Kind, d.Host = strings.TrimSpace(d.ID), strings.ToLower(strings.TrimSpace(d.Kind)), strings.TrimSpace(d.Host)
	if d.ID == "" || d.Kind == "" || d.Host == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id, kind and host are required"}
	}
	if err := acSaveDevice(d); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"ac": d.ID}}
}

func acListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	return OpsResult{OK: true, Initial: map[string]interface{}{"acs": acListDevices()}}
}

func acRemoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	vs, err := openVaultOptional()
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	if derr := vs.Delete(acVaultProject, strings.TrimSpace(p.ID)); derr != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: derr.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"removed": p.ID}}
}

func acSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID    string `json:"id"`
		Power *bool  `json:"power"`
		Mode  string `json:"mode"`
		Temp  *int   `json:"temp"`
		Fan   string `json:"fan"`
		Swing *bool  `json:"swing"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	dev, ok := acGetDevice(strings.TrimSpace(p.ID))
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "ac not found: " + p.ID}
	}
	state := map[string]interface{}{}
	if p.Power != nil {
		state["power"] = *p.Power
	}
	if p.Mode != "" {
		state["mode"] = p.Mode
	}
	if p.Temp != nil {
		state["temp"] = *p.Temp
	}
	if p.Fan != "" {
		state["fan"] = p.Fan
	}
	if p.Swing != nil {
		state["swing"] = *p.Swing
	}
	if len(state) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "no state fields set"}
	}
	out, err := acEng.Set(c.Ctx, dev, state)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}

func acStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	dev, ok := acGetDevice(strings.TrimSpace(p.ID))
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "ac not found: " + p.ID}
	}
	out, err := acEng.Status(c.Ctx, dev)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}
