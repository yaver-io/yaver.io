package main

// ops_home.go — the universal-remote ROUTER (docs/yaver-single-kumanda.md §4).
// One logical key set; each key routes to the right LIVE transport per device:
//   apple_tv → appleTVEng (pyatv)      mibox → droidKey (ADB input keyevent)
// Future kinds (satellite_ir, ac, camera, switch) slot in here as new cases.
//
// These are home_* ops verbs — a deliberately separate namespace from the
// coding-agent surface, so the remote/appliance/camera features never pollute
// the main Yaver dev UI. The mobile/web "Home" section calls these over the
// mesh (callOpsOnDevice, machine="<hub>"), LAN-direct first, relay fallback.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "home_device_add",
		Description: "Register a controllable device. Payload {id, name, kind, address?}. kind: apple_tv | mibox (more to come). address = Apple TV identifier or Mi Box adb serial/\"ip:port\".",
		Schema: map[string]interface{}{"type": "object", "required": []string{"id", "kind"}, "properties": map[string]interface{}{
			"id":      map[string]interface{}{"type": "string"},
			"name":    map[string]interface{}{"type": "string"},
			"kind":    map[string]interface{}{"type": "string"},
			"address": map[string]interface{}{"type": "string"},
		}},
		Handler: homeDeviceAddHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "home_device_list",
		Description: "List registered home devices.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler:     homeDeviceListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "home_device_remove",
		Description: "Remove a registered device. Payload {id}.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}},
		Handler:     homeDeviceRemoveHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "home_key",
		Description: "Send a logical key to a device. Payload {device, key, app?}. key: up/down/left/right/ok/back/home/menu/play/pause/play_pause/stop/next/previous/vol_up/vol_down/mute/power/power_on/power_off/channel_up/channel_down/0-9/launch. For launch pass {app}.",
		Schema: map[string]interface{}{"type": "object", "required": []string{"device", "key"}, "properties": map[string]interface{}{
			"device": map[string]interface{}{"type": "string"},
			"key":    map[string]interface{}{"type": "string"},
			"app":    map[string]interface{}{"type": "string"},
		}},
		Handler: homeKeyHandler,
	})
}

func homeDeviceAddHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p homeDevice
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	p.ID, p.Kind = strings.TrimSpace(p.ID), strings.TrimSpace(strings.ToLower(p.Kind))
	if p.ID == "" || p.Kind == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id and kind are required"}
	}
	homeStoreMu.Lock()
	defer homeStoreMu.Unlock()
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	replaced := false
	for i := range s.Devices {
		if strings.EqualFold(s.Devices[i].ID, p.ID) {
			s.Devices[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		s.Devices = append(s.Devices, p)
	}
	if err := saveHomeStore(s); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"device": p, "updated": replaced}}
}

func homeDeviceListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"devices": s.Devices}}
}

func homeDeviceRemoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	homeStoreMu.Lock()
	defer homeStoreMu.Unlock()
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	kept := s.Devices[:0]
	removed := false
	for _, d := range s.Devices {
		if strings.EqualFold(d.ID, p.ID) {
			removed = true
			continue
		}
		kept = append(kept, d)
	}
	s.Devices = kept
	if err := saveHomeStore(s); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"removed": removed}}
}

func homeKeyHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Device string `json:"device"`
		Key    string `json:"key"`
		App    string `json:"app"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	out, err := sendHomeKey(c, p.Device, p.Key, p.App)
	if err != nil {
		code := "remote_error"
		if strings.HasPrefix(err.Error(), "unsupported") || strings.HasPrefix(err.Error(), "unknown") {
			code = "bad_payload"
		} else if strings.Contains(err.Error(), "not found") {
			code = "not_found"
		}
		return OpsResult{OK: false, Code: code, Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: out}
}

// sendHomeKey is the actual router, shared by home_key and the activity
// executor. It resolves the device and dispatches the logical key to the
// device's live transport.
func sendHomeKey(c OpsContext, deviceID, key, app string) (map[string]interface{}, error) {
	key = strings.TrimSpace(strings.ToLower(key))
	s, err := loadHomeStore()
	if err != nil {
		return nil, err
	}
	dev, ok := s.device(deviceID)
	if !ok {
		return nil, fmt.Errorf("device %q not found", deviceID)
	}

	switch dev.Kind {
	case "apple_tv":
		switch key {
		case "launch":
			if app == "" {
				return nil, fmt.Errorf("unknown app for launch")
			}
			return appleTVEng.LaunchApp(c.Ctx, dev.Address, app)
		case "power", "power_on":
			return appleTVEng.Power(c.Ctx, dev.Address, "on")
		case "power_off":
			return appleTVEng.Power(c.Ctx, dev.Address, "off")
		default:
			name, ok := atvLogicalKey(key)
			if !ok {
				return nil, fmt.Errorf("unsupported key %q for apple_tv", key)
			}
			return appleTVEng.RemoteKey(c.Ctx, dev.Address, name)
		}

	case "mibox":
		serial := droidResolveDevice(dev.Address)
		if serial == "" {
			return nil, fmt.Errorf("mibox %q not reachable (no adb device); pair with `adb connect <ip>:5555`", deviceID)
		}
		if key == "launch" {
			if app == "" {
				return nil, fmt.Errorf("unknown app for launch")
			}
			msg, lerr := droidLaunchPackage(serial, app)
			if lerr != nil {
				return nil, lerr
			}
			return map[string]interface{}{"launched": msg, "device": serial}, nil
		}
		code, ok := miboxKeycode(key)
		if !ok {
			return nil, fmt.Errorf("unsupported key %q for mibox", key)
		}
		if kerr := droidKey(serial, code); kerr != nil {
			return nil, kerr
		}
		return map[string]interface{}{"sent": key, "keycode": code, "device": serial}, nil

	case "androidtv":
		// Android TV / Mi Box via the official remote v2 protocol (no ADB).
		// dev.Address is the TV's host/IP; pair once with atv2_pair_*.
		if dev.Address == "" {
			return nil, fmt.Errorf("androidtv device %q has no host (set address to the TV IP)", deviceID)
		}
		if key == "launch" {
			if app == "" {
				return nil, fmt.Errorf("unknown app for launch")
			}
			if lerr := atv2Eng.Launch(c.Ctx, dev.Address, app); lerr != nil {
				return nil, lerr
			}
			return map[string]interface{}{"launched": app, "via": "androidtv"}, nil
		}
		name, ok := atv2KeyName(key)
		if !ok {
			return nil, fmt.Errorf("unsupported key %q for androidtv", key)
		}
		if kerr := atv2Eng.Key(c.Ctx, dev.Address, name); kerr != nil {
			return nil, kerr
		}
		return map[string]interface{}{"sent": key, "via": "androidtv"}, nil

	case "ir":
		// IR device: the blaster lives at dev.Address; the logical key maps to
		// a learned code in the vault (ir.go). The phone can't blast IR — the
		// hub box drives the Broadlink-class blaster on the LAN.
		if dev.Address == "" {
			return nil, fmt.Errorf("ir device %q has no blaster host (set address to the Broadlink IP)", deviceID)
		}
		code, ok := irGetCode(deviceID, key)
		if !ok {
			return nil, fmt.Errorf("no learned code for %s/%s — run ir_learn first", deviceID, key)
		}
		if berr := irEng.Blast(c.Ctx, dev.Address, code); berr != nil {
			return nil, berr
		}
		return map[string]interface{}{"sent": key, "via": "ir"}, nil

	case "switch":
		// Open/close/toggle device (garage, gate, blinds, lock, relay) reached
		// over plain HTTP — dev.Address is a URL template with "{cmd}" (e.g. a
		// Shelly relay: http://192.168.1.50/relay/0?turn={cmd}). open→on,
		// close→off, toggle→toggle, stop→stop.
		cmd, ok := switchCommand(key)
		if !ok {
			return nil, fmt.Errorf("unsupported key %q for switch (use open/close/toggle/stop)", key)
		}
		return sendSwitchHTTP(c, dev.Address, cmd)

	default:
		return nil, fmt.Errorf("unsupported device kind %q (remote_not_implemented)", dev.Kind)
	}
}

// switchCommand maps an open/close logical key to the {cmd} token substituted
// into a switch device's URL template.
func switchCommand(key string) (string, bool) {
	switch key {
	case "open", "on":
		return "on", true
	case "close", "off":
		return "off", true
	case "toggle":
		return "toggle", true
	case "stop":
		return "stop", true
	}
	return "", false
}

// sendSwitchHTTP fires the switch's HTTP endpoint (GET), substituting {cmd}.
func sendSwitchHTTP(c OpsContext, urlTemplate, cmd string) (map[string]interface{}, error) {
	url := strings.ReplaceAll(urlTemplate, "{cmd}", cmd)
	req, err := http.NewRequestWithContext(c.Ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("bad switch url: %w", err)
	}
	resp, err := switchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("switch returned HTTP %d", resp.StatusCode)
	}
	return map[string]interface{}{"sent": cmd, "via": "switch", "status": resp.StatusCode}, nil
}

var switchHTTPClient = &http.Client{Timeout: 10 * time.Second}

// atvLogicalKey maps a canonical logical key to the name appleTVEng.RemoteKey
// accepts. Power is handled separately; mute/channel/digits aren't meaningful
// on Apple TV.
func atvLogicalKey(key string) (string, bool) {
	m := map[string]string{
		"up": "up", "down": "down", "left": "left", "right": "right",
		"ok": "select", "select": "select",
		"menu": "menu", "back": "menu", "home": "home",
		"play": "play", "pause": "pause", "play_pause": "play_pause",
		"stop": "stop", "next": "next", "previous": "previous",
		"vol_up": "volume_up", "vol_down": "volume_down",
	}
	v, ok := m[key]
	return v, ok
}

// miboxKeycode maps a canonical logical key to the Android KEYCODE_* integer
// sent via `adb shell input keyevent`.
func miboxKeycode(key string) (int, bool) {
	m := map[string]int{
		"up": 19, "down": 20, "left": 21, "right": 22, "ok": 23, "select": 23,
		"back": 4, "home": 3, "menu": 82,
		"play": 126, "pause": 127, "play_pause": 85, "stop": 86,
		"next": 87, "previous": 88,
		"vol_up": 24, "vol_down": 25, "mute": 164, "power": 26,
		"channel_up": 166, "channel_down": 167,
		"0": 7, "1": 8, "2": 9, "3": 10, "4": 11, "5": 12, "6": 13, "7": 14, "8": 15, "9": 16,
	}
	v, ok := m[key]
	return v, ok
}
