package main

// ops_appletv.go — ops verbs for Apple TV control + now-playing, and for the
// home capture-card video source. These are the mesh-routable surface: a phone
// (or any client) calls them via callOpsOnDevice with machine="<pi>", LAN-direct
// first, relay fallback — so the same call works at home and away with no
// call-site change. The CLI and the first-class MCP tools call the SAME engine
// methods (appletv.go / capture.go), never a parallel impl.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func atvSchema(props map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": props}
}

func init() {
	// ── Apple TV control ──────────────────────────────────────────────────────
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_scan",
		Description: "Discover Apple TVs on the LAN (pyatv). Returns identifier/address/name/services for each.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     atvScanHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_list",
		Description: "List paired Apple TVs (from the vault).",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     atvListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_pair_begin",
		Description: "Begin PIN pairing with an Apple TV. Payload {identifier, protocol?}. A PIN appears on the TV; pass it to appletv_pair_finish.",
		Schema: atvSchema(map[string]interface{}{
			"identifier": map[string]interface{}{"type": "string"},
			"protocol":   map[string]interface{}{"type": "string", "description": "MRP|AirPlay|Companion (default MRP)"},
		}),
		Handler: atvPairBeginHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_pair_finish",
		Description: "Finish PIN pairing and store credentials in the vault. Payload {session, pin, identifier, name, address}.",
		Schema: atvSchema(map[string]interface{}{
			"session":    map[string]interface{}{"type": "string"},
			"pin":        map[string]interface{}{"type": "integer"},
			"identifier": map[string]interface{}{"type": "string"},
			"name":       map[string]interface{}{"type": "string"},
			"address":    map[string]interface{}{"type": "string"},
		}),
		Handler: atvPairFinishHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_remote_key",
		Description: "Send a remote key. Payload {device?, key} — key in up/down/left/right/select/menu/home/play/pause/stop/next/previous/play_pause/volume_up/volume_down.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
			"key":    map[string]interface{}{"type": "string"},
		}),
		Handler: atvRemoteKeyHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_launch_app",
		Description: "Launch an app on the Apple TV. Payload {device?, bundle_id}.",
		Schema: atvSchema(map[string]interface{}{
			"device":    map[string]interface{}{"type": "string"},
			"bundle_id": map[string]interface{}{"type": "string"},
		}),
		Handler: atvLaunchHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_power",
		Description: "Power the Apple TV on/off. Payload {device?, state} — state on|off.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
			"state":  map[string]interface{}{"type": "string"},
		}),
		Handler: atvPowerHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_transport",
		Description: "Transport control. Payload {device?, action} — play|pause|stop|next|previous|play_pause.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
			"action": map[string]interface{}{"type": "string"},
		}),
		Handler: atvTransportHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_seek",
		Description: "Seek to a position. Payload {device?, seconds}.",
		Schema: atvSchema(map[string]interface{}{
			"device":  map[string]interface{}{"type": "string"},
			"seconds": map[string]interface{}{"type": "integer"},
		}),
		Handler: atvSeekHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "appletv_now_playing",
		Description: "Current now-playing metadata (title/artist/app/state/position/total + artwork data URL if available). Read-only.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
		}),
		Handler:    atvNowPlayingHandler,
		AllowGuest: true,
	})

	// ── Capture card (home A/V source) ────────────────────────────────────────
	registerOpsVerb(opsVerbSpec{
		Name:        "capture_devices",
		Description: "List capture-card video devices on this host (/dev/video* on Linux) and report whether ffmpeg is installed.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     captureDevicesHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "capture_start",
		Description: "Start streaming a capture card via ffmpeg→MJPEG. Payload {device?, fps?, width?, height?}. Watch via /capture/stream (MJPEG) or /capture/frame.jpg (poll). OWN NON-PROTECTED SOURCES ONLY — HDCP-protected input is reported, not streamed.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "/dev/video0 etc (Linux) or avfoundation index (macOS dev)"},
			"fps":    map[string]interface{}{"type": "integer", "description": "1-15 (default 6)"},
			"width":  map[string]interface{}{"type": "integer"},
			"height": map[string]interface{}{"type": "integer"},
		}),
		Handler:   captureStartHandler,
		Streaming: true,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "capture_stop",
		Description: "Stop the capture-card stream.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     captureStopHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "capture_status",
		Description: "Capture stream status (running, device, fps, hasFrame, hdcpBlocked, URLs).",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     captureStatusHandler,
		AllowGuest:  true,
	})
}

// ── Apple TV handlers ────────────────────────────────────────────────────────

func atvScanHandler(c OpsContext, _ json.RawMessage) OpsResult {
	out, err := appleTVEng.Scan(c.Ctx)
	if err != nil {
		return OpsResult{OK: false, Code: "appletv_unavailable", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}

func atvListHandler(c OpsContext, _ json.RawMessage) OpsResult {
	devs, err := appletvListDevices()
	if err != nil {
		return OpsResult{OK: false, Code: "vault_error", Error: err.Error()}
	}
	// Don't leak raw credentials over the mesh; summarize.
	summ := make([]map[string]interface{}, 0, len(devs))
	for _, d := range devs {
		protos := make([]string, 0, len(d.Credentials))
		for p := range d.Credentials {
			protos = append(protos, p)
		}
		summ = append(summ, map[string]interface{}{
			"identifier": d.Identifier, "name": d.Name, "address": d.Address,
			"default": d.Default, "protocols": protos,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"devices": summ}}
}

func atvPairBeginHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Identifier string `json:"identifier"`
		Protocol   string `json:"protocol"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || strings.TrimSpace(p.Identifier) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "identifier required"}
	}
	out, err := appleTVEng.call(c.Ctx, "/pair_begin", map[string]interface{}{
		"identifier": p.Identifier, "protocol": p.Protocol,
	})
	if err != nil {
		return OpsResult{OK: false, Code: "appletv_unavailable", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}

func atvPairFinishHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Session    string `json:"session"`
		PIN        *int   `json:"pin"`
		Identifier string `json:"identifier"`
		Name       string `json:"name"`
		Address    string `json:"address"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.Session == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "session required"}
	}
	req := map[string]interface{}{"session": p.Session}
	if p.PIN != nil {
		req["pin"] = *p.PIN
	}
	out, err := appleTVEng.call(c.Ctx, "/pair_finish", req)
	if err != nil {
		return OpsResult{OK: false, Code: "pair_failed", Error: err.Error()}
	}
	creds := map[string]string{}
	if raw, ok := out["credentials"].(map[string]interface{}); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				creds[k] = s
			}
		}
	}
	if len(creds) == 0 {
		return OpsResult{OK: false, Code: "pair_failed", Error: "no credentials returned"}
	}
	d := appletvDevice{Identifier: p.Identifier, Name: p.Name, Address: p.Address, Credentials: creds}
	if d.Identifier == "" {
		d.Identifier = p.Address
	}
	// First paired device becomes default.
	if existing, _ := appletvListDevices(); len(existing) == 0 {
		d.Default = true
	}
	if err := appletvSaveDevice(d); err != nil {
		return OpsResult{OK: false, Code: "vault_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"paired": d.Identifier, "name": d.Name}}
}

func atvDevicePayload(payload json.RawMessage) string {
	var p struct {
		Device string `json:"device"`
	}
	_ = json.Unmarshal(payload, &p)
	return p.Device
}

func atvRemoteKeyHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if !appletvValidKey(p.Key) {
		return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("unknown key %q", p.Key)}
	}
	out, err := appleTVEng.RemoteKey(c.Ctx, p.Device, strings.ToLower(p.Key))
	return atvResult(out, err)
}

func atvLaunchHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device   string `json:"device"`
		BundleID string `json:"bundle_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || strings.TrimSpace(p.BundleID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "bundle_id required"}
	}
	out, err := appleTVEng.LaunchApp(c.Ctx, p.Device, p.BundleID)
	return atvResult(out, err)
}

func atvPowerHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	state := strings.ToLower(strings.TrimSpace(p.State))
	if state != "on" && state != "off" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "state must be on|off"}
	}
	out, err := appleTVEng.Power(c.Ctx, p.Device, state)
	return atvResult(out, err)
}

func atvTransportHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || !appletvValidKey(p.Action) {
		return OpsResult{OK: false, Code: "bad_payload", Error: "valid action required"}
	}
	out, err := appleTVEng.Transport(c.Ctx, p.Device, strings.ToLower(p.Action))
	return atvResult(out, err)
}

func atvSeekHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device  string `json:"device"`
		Seconds int    `json:"seconds"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	out, err := appleTVEng.Seek(c.Ctx, p.Device, p.Seconds)
	return atvResult(out, err)
}

func atvNowPlayingHandler(c OpsContext, payload json.RawMessage) OpsResult {
	out, err := appleTVEng.NowPlaying(c.Ctx, atvDevicePayload(payload))
	return atvResult(out, err)
}

func atvResult(out map[string]interface{}, err error) OpsResult {
	if err != nil {
		return OpsResult{OK: false, Code: "appletv_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}

// ── Capture handlers ─────────────────────────────────────────────────────────

func captureDevicesHandler(c OpsContext, _ json.RawMessage) OpsResult {
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"devices": captureDevices(),
		"ffmpeg":  ffmpegPath() != "",
	}}
}

func captureStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		FPS    int    `json:"fps"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	_ = json.Unmarshal(payload, &p)
	if err := captureStream.start(p.Device, p.FPS, p.Width, p.Height); err != nil {
		return OpsResult{OK: false, Code: "capture_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, StreamID: "capture", Initial: captureStream.status()}
}

func captureStopHandler(c OpsContext, _ json.RawMessage) OpsResult {
	captureStream.stop()
	return OpsResult{OK: true, Initial: captureStream.status()}
}

func captureStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	return OpsResult{OK: true, Initial: captureStream.status()}
}
