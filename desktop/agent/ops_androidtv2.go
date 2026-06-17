package main

// ops_androidtv2.go — Android TV Remote v2 verbs (Mi Box / Google TV). Pair once
// (TV shows a code), then send keys / launch apps over the official remote
// protocol — no ADB, no dev mode. A home device of kind `androidtv` routes its
// logical keys through atv2Eng (see ops_home.go).

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "atv2_pair_begin",
		Description: "Start pairing with an Android TV / Mi Box. Payload {host}. A code appears on the TV — pass it to atv2_pair_finish.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}}},
		Handler:     atv2PairBeginHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "atv2_pair_finish",
		Description: "Finish pairing and register the TV. Payload {host, code, id, name?}. Persists the cert; the TV stays paired across reboots.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"host", "code", "id"}, "properties": map[string]interface{}{
			"host": map[string]interface{}{"type": "string"},
			"code": map[string]interface{}{"type": "string"},
			"id":   map[string]interface{}{"type": "string"},
			"name": map[string]interface{}{"type": "string"},
		}},
		Handler: atv2PairFinishHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "atv2_list",
		Description: "List paired Android TVs.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler:     atv2ListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "atv2_key",
		Description: "Send a logical key to a paired Android TV. Payload {host, key}.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"host", "key"}, "properties": map[string]interface{}{
			"host": map[string]interface{}{"type": "string"},
			"key":  map[string]interface{}{"type": "string"},
		}},
		Handler: atv2KeyHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "atv2_launch",
		Description: "Launch an app on a paired Android TV. Payload {host, app} (app = deep link or app id).",
		Schema: map[string]interface{}{"type": "object", "required": []string{"host", "app"}, "properties": map[string]interface{}{
			"host": map[string]interface{}{"type": "string"},
			"app":  map[string]interface{}{"type": "string"},
		}},
		Handler: atv2LaunchHandler,
	})
}

func atv2PairBeginHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if err := atv2Eng.PairBegin(c.Ctx, strings.TrimSpace(p.Host)); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"pairing": true, "note": "enter the code shown on the TV via atv2_pair_finish"}}
}

func atv2PairFinishHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Host string `json:"host"`
		Code string `json:"code"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	p.Host, p.ID = strings.TrimSpace(p.Host), strings.TrimSpace(p.ID)
	if p.Host == "" || p.ID == "" || strings.TrimSpace(p.Code) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "host, code and id are required"}
	}
	if err := atv2Eng.PairFinish(c.Ctx, p.Host, strings.TrimSpace(p.Code)); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	if err := atv2SaveDevice(atv2Device{ID: p.ID, Name: p.Name, Host: p.Host}); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"paired": p.ID, "host": p.Host}}
}

func atv2ListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	return OpsResult{OK: true, Initial: map[string]interface{}{"devices": atv2ListDevices()}}
}

func atv2KeyHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Host string `json:"host"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	name, ok := atv2KeyName(strings.ToLower(strings.TrimSpace(p.Key)))
	if !ok {
		return OpsResult{OK: false, Code: "bad_payload", Error: "unsupported key " + p.Key}
	}
	if err := atv2Eng.Key(c.Ctx, strings.TrimSpace(p.Host), name); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sent": name}}
}

func atv2LaunchHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Host string `json:"host"`
		App  string `json:"app"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if err := atv2Eng.Launch(c.Ctx, strings.TrimSpace(p.Host), strings.TrimSpace(p.App)); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"launched": p.App}}
}
