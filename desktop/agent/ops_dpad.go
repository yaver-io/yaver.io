package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ops_dpad.go — target-neutral D-pad facade for constrained surfaces.
//
// TV, watch, car, and MCP clients should not need to learn whether the current
// target is pyatv, Android TV Remote v2, or the home-device router. This verb
// normalizes the small remote-control command shape and forwards to the existing
// source-of-truth ops verbs. It intentionally does not implement pointer-style
// remote desktop; D-pad surfaces need a different, smaller contract.

type dpadPayload struct {
	Target string `json:"target,omitempty"` // appletv | androidtv | home
	Key    string `json:"key,omitempty"`
	Repeat int    `json:"repeat,omitempty"`
	Device string `json:"device,omitempty"` // appletv/home logical device id
	Host   string `json:"host,omitempty"`   // androidtv host
	App    string `json:"app,omitempty"`    // optional app/deep-link for home routers
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "dpad_input",
		Description: "Send one normalized D-pad/remote key to a constrained target. Payload {target: appletv|androidtv|home, key, repeat?, device?, host?, app?}. Routes to existing appletv_remote_key, atv2_key, or home_key.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"target", "key"},
			"properties": map[string]interface{}{
				"target": map[string]interface{}{"type": "string", "enum": []string{"appletv", "androidtv", "home"}},
				"key":    map[string]interface{}{"type": "string", "description": "up/down/left/right/select/back/home/play_pause/next/previous/power/menu"},
				"repeat": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 10},
				"device": map[string]interface{}{"type": "string", "description": "Apple TV or home device identifier"},
				"host":   map[string]interface{}{"type": "string", "description": "Android TV host/IP"},
				"app":    map[string]interface{}{"type": "string", "description": "Optional app/deep-link for home routers"},
			},
			"additionalProperties": false,
		},
		Handler: dpadInputHandler,
	})
}

func dpadInputHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p dpadPayload
	if len(payload) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "payload is required"}
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	target := normalizeDpadTarget(p.Target)
	key, ok := normalizeDpadKey(p.Key)
	if target == "" || !ok {
		return OpsResult{OK: false, Code: "bad_payload", Error: "target must be appletv, androidtv, or home and key must be a supported D-pad key"}
	}
	repeat := p.Repeat
	if repeat <= 0 {
		repeat = 1
	}
	if repeat > 10 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "repeat must be 1..10"}
	}

	var last OpsResult
	for i := 0; i < repeat; i++ {
		last = dpadDispatchOnce(c, target, key, p)
		if !last.OK {
			return last
		}
	}
	initial := map[string]interface{}{
		"target": target,
		"key":    key,
		"repeat": repeat,
	}
	if last.Initial != nil {
		initial["last"] = last.Initial
	}
	return OpsResult{OK: true, Initial: initial}
}

func dpadDispatchOnce(c OpsContext, target, key string, p dpadPayload) OpsResult {
	switch target {
	case "appletv":
		body, _ := json.Marshal(map[string]interface{}{"device": strings.TrimSpace(p.Device), "key": key})
		return dispatchOps(c, OpsRequest{Machine: "local", Verb: "appletv_remote_key", Payload: body})
	case "androidtv":
		if strings.TrimSpace(p.Host) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "host is required for androidtv target"}
		}
		body, _ := json.Marshal(map[string]interface{}{"host": strings.TrimSpace(p.Host), "key": dpadKeyForAndroidTV(key)})
		return dispatchOps(c, OpsRequest{Machine: "local", Verb: "atv2_key", Payload: body})
	case "home":
		if strings.TrimSpace(p.Device) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "device is required for home target"}
		}
		body, _ := json.Marshal(map[string]interface{}{"device": strings.TrimSpace(p.Device), "key": key, "app": strings.TrimSpace(p.App)})
		return dispatchOps(c, OpsRequest{Machine: "local", Verb: "home_key", Payload: body})
	default:
		return OpsResult{OK: false, Code: "unsupported_target", Error: fmt.Sprintf("unsupported D-pad target %q", target)}
	}
}

func normalizeDpadTarget(target string) string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "appletv", "apple-tv", "apple_tv", "tv-apple":
		return "appletv"
	case "androidtv", "android-tv", "android_tv", "google-tv", "googletv", "tv-android":
		return "androidtv"
	case "home", "home-device", "home_device":
		return "home"
	default:
		return ""
	}
}

func normalizeDpadKey(key string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "up", "dpad_up":
		return "up", true
	case "down", "dpad_down":
		return "down", true
	case "left", "dpad_left":
		return "left", true
	case "right", "dpad_right":
		return "right", true
	case "ok", "enter", "center", "dpad_center", "select":
		return "select", true
	case "back", "menu":
		return "menu", true
	case "home":
		return "home", true
	case "play", "pause", "playpause", "play_pause":
		return "play_pause", true
	case "next", "previous", "prev", "stop", "power":
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "prev" {
			k = "previous"
		}
		return k, true
	case "volume_up", "vol_up":
		return "volume_up", true
	case "volume_down", "vol_down":
		return "volume_down", true
	default:
		return "", false
	}
}

func dpadKeyForAndroidTV(key string) string {
	switch key {
	case "volume_up":
		return "vol_up"
	case "volume_down":
		return "vol_down"
	default:
		return key
	}
}
