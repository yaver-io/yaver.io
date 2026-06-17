package main

// ops_ir.go — IR ops verbs (docs/yaver-single-kumanda.md §6). Discover a
// Broadlink learner/blaster, LEARN a code off the user's real remote (the phone
// can't — no IR receiver), BLAST a stored code, and LIST what a device knows.
// Codes are vault-local (ir.go). An `ir` home device routes its logical keys
// here via the home_key router (ops_home.go).

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ir_scan",
		Description: "Discover Broadlink IR/RF learners-blasters on the LAN. Returns {devices:[{host,mac,type,name}]}.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler:     irScanHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ir_learn",
		Description: "Learn one IR code off the real remote and store it. Payload {device, key, host}. device = a registered home device id (kind ir); key = logical key (power/ok/ch_up/…); host = blaster IP. Point the remote at the blaster and press the button.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"device", "key", "host"}, "properties": map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
			"key":    map[string]interface{}{"type": "string"},
			"host":   map[string]interface{}{"type": "string"},
		}},
		Handler: irLearnHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ir_blast",
		Description: "Blast a previously-learned code. Payload {device, key, host}. Looks up the stored code for device/key and sends it through the blaster at host.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"device", "key", "host"}, "properties": map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
			"key":    map[string]interface{}{"type": "string"},
			"host":   map[string]interface{}{"type": "string"},
		}},
		Handler: irBlastHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ir_list",
		Description: "List the logical keys learned for a device. Payload {device}.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"device"}, "properties": map[string]interface{}{"device": map[string]interface{}{"type": "string"}}},
		Handler:     irListHandler,
	})
}

func irScanHandler(c OpsContext, payload json.RawMessage) OpsResult {
	out, err := irEng.Scan(c.Ctx)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}

func irLearnHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		Key    string `json:"key"`
		Host   string `json:"host"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	p.Device, p.Key, p.Host = strings.TrimSpace(p.Device), strings.ToLower(strings.TrimSpace(p.Key)), strings.TrimSpace(p.Host)
	if p.Device == "" || p.Key == "" || p.Host == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "device, key and host are required"}
	}
	code, err := irEng.Learn(c.Ctx, p.Host)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	if err := irSaveCode(p.Device, p.Key, code); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"device": p.Device, "key": p.Key, "learned": true}}
}

func irBlastHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		Key    string `json:"key"`
		Host   string `json:"host"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	p.Device, p.Key, p.Host = strings.TrimSpace(p.Device), strings.ToLower(strings.TrimSpace(p.Key)), strings.TrimSpace(p.Host)
	code, ok := irGetCode(p.Device, p.Key)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "no learned code for " + p.Device + "/" + p.Key}
	}
	if err := irEng.Blast(c.Ctx, p.Host, code); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sent": p.Key}}
}

func irListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"device": p.Device, "keys": irListKeys(strings.TrimSpace(p.Device))}}
}
