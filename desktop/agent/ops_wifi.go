package main

import (
	"encoding/json"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_capabilities",
		Description: "Detect Linux Wi-Fi AP/repeater capabilities from iw/nl80211.",
		Handler:     opsWiFiCapabilities,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_status",
		Description: "Return current Yaver-managed Wi-Fi hotspot/repeater status.",
		Handler:     opsWiFiStatus,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_start",
		Description: "Start a Yaver-managed Wi-Fi access point or AP+STA repeater. Payload is WiFiHotspotConfig. In APSTA mode, upstreamSsid/upstreamPass are optional when the Linux box uses NetworkManager with a saved PSK readable by root; otherwise returns code credentials_required so MCP/mobile can ask the user for them.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"ssid", "password"},
			"properties": map[string]interface{}{
				"ssid":         map[string]interface{}{"type": "string", "description": "SSID to broadcast, e.g. kivanc."},
				"password":     map[string]interface{}{"type": "string", "description": "WPA2 password for the broadcast network; 8-63 chars."},
				"mode":         map[string]interface{}{"type": "string", "enum": []string{"ap", "apsta"}, "description": "ap for hotspot only, apsta for repeater. Default ap."},
				"interface":    map[string]interface{}{"type": "string", "description": "Wi-Fi STA interface, e.g. wlan0. Optional if auto-detected."},
				"apInterface":  map[string]interface{}{"type": "string", "description": "Optional AP virtual interface, e.g. wlan0ap."},
				"upstreamIf":   map[string]interface{}{"type": "string", "description": "Optional NAT/uplink interface. Defaults to interface in APSTA."},
				"upstreamSsid": map[string]interface{}{"type": "string", "description": "Existing Wi-Fi SSID to join in APSTA mode. Optional with NetworkManager auto-detect."},
				"upstreamPass": map[string]interface{}{"type": "string", "description": "Existing Wi-Fi password for APSTA mode. If credentials_required is returned, ask the user for this."},
				"channel":      map[string]interface{}{"type": "integer", "description": "Wi-Fi channel. Default 6."},
				"frequency":    map[string]interface{}{"type": "string", "enum": []string{"2.4GHz", "5GHz"}},
				"countryCode":  map[string]interface{}{"type": "string", "description": "ISO country code, e.g. US."},
				"enableDhcp":   map[string]interface{}{"type": "boolean", "description": "Run dnsmasq DHCP. Default true."},
				"enableNat":    map[string]interface{}{"type": "boolean", "description": "Enable NAT masquerade. Default true."},
			},
		},
		Handler:    opsWiFiStart,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_stop",
		Description: "Stop the Yaver-managed Wi-Fi access point/repeater and clean up Yaver-owned NAT/processes.",
		Handler:     opsWiFiStop,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_mesh_capabilities",
		Description: "Detect Linux Wi-Fi mesh substrate support: 802.11s mesh point, wpa_supplicant, BATMAN-adv.",
		Handler:     opsWiFiMeshCapabilities,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_mesh_status",
		Description: "Return current Yaver-managed Wi-Fi mesh status.",
		Handler:     opsWiFiMeshStatus,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_mesh_start",
		Description: "Start a Yaver-managed Linux 802.11s Wi-Fi mesh. Payload is WiFiMeshConfig. backend can be 80211s or batman.",
		Handler:     opsWiFiMeshStart,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_mesh_stop",
		Description: "Stop the Yaver-managed Wi-Fi mesh and clean up Yaver-owned wpa_supplicant/BATMAN state.",
		Handler:     opsWiFiMeshStop,
		AllowGuest:  false,
	})

	// Client management ops (added for APSTA and self-hosted remote dev)
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_list_clients",
		Description: "List all clients currently connected to the Wi-Fi hotspot.",
		Handler:     opsWiFiListClients,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_kick_client",
		Description: "Kick a specific client from the Wi-Fi hotspot by MAC address. Payload: {\"mac\":\"xx:xx:xx:xx:xx\"}.",
		Handler:     opsWiFiKickClient,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_ban_client",
		Description: "Ban a client from the Wi-Fi hotspot. Payload: {\"mac\":\"xx:xx:xx:xx:xx\",\"durationHours\":int}. Duration of 0 means permanent ban. Kick client if currently connected.",
		Handler:     opsWiFiBanClient,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_unban_client",
		Description: "Unban a previously banned client by MAC address. Payload: {\"mac\":\"xx:xx:xx:xx:xx\"}.",
		Handler:     opsWiFiUnbanClient,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_get_banned",
		Description: "List all currently banned clients with their MAC addresses and ban expiry times.",
		Handler:     opsWiFiGetBannedClients,
		AllowGuest:  false,
	})

	// APSTA configuration ops (added for self-hosted remote dev)
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_set_apsta_config",
		Description: "Set and persist the APSTA hotspot configuration. Payload is WiFiHotspotConfig JSON. On macOS, custom SSID/password are not supported.",
		Handler:     opsWiFiSetAPSTAConfig,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "wifi_get_apsta_config",
		Description: "Return the currently saved APSTA hotspot configuration, or error if none exists.",
		Handler:     opsWiFiGetAPSTAConfig,
		AllowGuest:  false,
	})
}

func opsWiFiCapabilities(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}
	caps, err := c.Server.wifiManager().DetectHardwareCapabilities()
	if err != nil {
		return OpsResult{OK: false, Code: "wifi_capabilities_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"capabilities": caps}}
}

func opsWiFiStatus(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}
	status, err := c.Server.wifiManager().GetStatus()
	if err != nil {
		return OpsResult{OK: false, Code: "wifi_status_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"status": status}}
}

func opsWiFiStart(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}
	var cfg WiFiHotspotConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if err := c.Server.wifiManager().StartHotspot(&cfg); err != nil {
		if isWiFiUpstreamCredentialsRequired(err) {
			return OpsResult{
				OK:    false,
				Code:  "credentials_required",
				Error: err.Error(),
				Initial: map[string]interface{}{
					"missing": missingAPSTAUpstreamFields(&cfg),
					"ask":     "Ask the user for upstreamSsid and upstreamPass, then retry wifi_start with those fields.",
				},
			}
		}
		return OpsResult{OK: false, Code: "wifi_start_failed", Error: err.Error()}
	}
	status, _ := c.Server.wifiManager().GetStatus()
	return OpsResult{OK: true, Initial: map[string]interface{}{"status": status}}
}

func opsWiFiStop(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}
	if err := c.Server.wifiManager().StopHotspot(); err != nil {
		return OpsResult{OK: false, Code: "wifi_stop_failed", Error: err.Error()}
	}
	status, _ := c.Server.wifiManager().GetStatus()
	return OpsResult{OK: true, Initial: map[string]interface{}{"status": status}}
}

func opsWiFiMeshCapabilities(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi mesh ops require an agent server"}
	}
	caps, err := c.Server.wifiMeshManager().DetectCapabilities()
	if err != nil {
		return OpsResult{OK: false, Code: "wifi_mesh_capabilities_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"capabilities": caps}}
}

func opsWiFiMeshStatus(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi mesh ops require an agent server"}
	}
	status, err := c.Server.wifiMeshManager().Status()
	if err != nil {
		return OpsResult{OK: false, Code: "wifi_mesh_status_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"status": status}}
}

func opsWiFiMeshStart(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi mesh ops require an agent server"}
	}
	var cfg WiFiMeshConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if err := c.Server.wifiMeshManager().Start(&cfg); err != nil {
		return OpsResult{OK: false, Code: "wifi_mesh_start_failed", Error: err.Error()}
	}
	status, _ := c.Server.wifiMeshManager().Status()
	return OpsResult{OK: true, Initial: map[string]interface{}{"status": status}}
}

func opsWiFiMeshStop(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi mesh ops require an agent server"}
	}
	if err := c.Server.wifiMeshManager().Stop(); err != nil {
		return OpsResult{OK: false, Code: "wifi_mesh_stop_failed", Error: err.Error()}
	}
	status, _ := c.Server.wifiMeshManager().Status()
	return OpsResult{OK: true, Initial: map[string]interface{}{"status": status}}
}

// Client Management and APSTA Configuration Ops
// Added for APSTA self-hosted remote dev with kick/ban features

func opsWiFiListClients(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	clients := c.Server.wifiManager().ListClients()
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"clients": clients,
		"count":   len(clients),
	}}
}

func opsWiFiKickClient(c OpsContext, payload json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	// Parse MAC from payload
	type KickRequest struct {
		MAC string `json:"mac"`
	}
	var req KickRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return OpsResult{OK: false, Code: "invalid_payload", Error: "invalid payload: must be {\"mac\":\"xx:xx:xx:xx:xx:xx\"}"}
	}

	if req.MAC == "" {
		return OpsResult{OK: false, Code: "missing_field", Error: "mac address required"}
	}

	if err := c.Server.wifiManager().KickClient(req.MAC); err != nil {
		return OpsResult{OK: false, Code: "kick_failed", Error: err.Error()}
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"kicked": true,
		"mac":    req.MAC,
	}}
}

func opsWiFiBanClient(c OpsContext, payload json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	// Parse MAC and duration from payload
	type BanRequest struct {
		MAC           string `json:"mac"`
		DurationHours int    `json:"durationHours"` // 0 = permanent
	}
	var req BanRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return OpsResult{OK: false, Code: "invalid_payload", Error: "invalid payload: must be {\"mac\":\"xx:xx:xx:xx:xx:xx\",\"durationHours\":int}"}
	}

	if req.MAC == "" {
		return OpsResult{OK: false, Code: "missing_field", Error: "mac address required"}
	}

	if err := c.Server.wifiManager().BanClient(req.MAC, req.DurationHours); err != nil {
		return OpsResult{OK: false, Code: "ban_failed", Error: err.Error()}
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"banned":         true,
		"mac":            req.MAC,
		"duration_hours": req.DurationHours,
	}}
}

func opsWiFiUnbanClient(c OpsContext, payload json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	// Parse MAC from payload
	type UnbanRequest struct {
		MAC string `json:"mac"`
	}
	var req UnbanRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return OpsResult{OK: false, Code: "invalid_payload", Error: "invalid payload: must be {\"mac\":\"xx:xx:xx:xx:xx:xx\"}"}
	}

	if req.MAC == "" {
		return OpsResult{OK: false, Code: "missing_field", Error: "mac address required"}
	}

	if err := c.Server.wifiManager().UnbanClient(req.MAC); err != nil {
		return OpsResult{OK: false, Code: "unban_failed", Error: err.Error()}
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"unbanned": true,
		"mac":      req.MAC,
	}}
}

func opsWiFiGetBannedClients(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	banned := c.Server.wifiManager().GetBannedClients()

	// Convert to slice for JSON serialization
	bannedList := []map[string]interface{}{}
	for mac, expiry := range banned {
		entry := map[string]interface{}{
			"mac": mac,
		}
		if expiry.IsZero() {
			entry["expiry"] = "permanent"
		} else {
			entry["expiry"] = expiry.Format(time.RFC3339)
		}
		bannedList = append(bannedList, entry)
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"banned_clients": bannedList,
		"count":          len(bannedList),
	}}
}

func opsWiFiSetAPSTAConfig(c OpsContext, payload json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	var cfg WiFiHotspotConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return OpsResult{OK: false, Code: "invalid_payload", Error: "invalid payload: must be valid WiFiHotspotConfig JSON"}
	}

	if err := c.Server.wifiManager().SetAPSTAConfig(&cfg); err != nil {
		return OpsResult{OK: false, Code: "config_save_failed", Error: err.Error()}
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"saved":  true,
		"config": cfg,
	}}
}

func opsWiFiGetAPSTAConfig(c OpsContext, _ json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_unavailable", Error: "wifi ops require an agent server"}
	}

	if c.Server.wifiManager() == nil {
		return OpsResult{OK: false, Code: "wifi_manager_unavailable", Error: "wifi hotspot manager not initialized"}
	}

	cfg, err := c.Server.wifiManager().GetAPSTAConfig()
	if err != nil {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"error": "no config found",
		}}
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"config": cfg,
	}}
}
