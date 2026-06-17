package main

// gateway_phone_inventory.go — the phone→clone INVENTORY BRIDGE.
//
// The user's own phone (the Yaver mobile app) enumerates its launchable apps
// (mobile YaverAppInventoryModule.kt → appSync.ts::listPhoneApps) and PUSHES that
// list up to the agent here. The agent persists it LOCAL-FIRST (never Convex) and
// can derive a clone node's desired app-set from it (gateway_appsync.go's
// NodeAppSet), so "mirror the apps on my phone onto the redroid / second-hand
// clone" is ONE step instead of hand-listing every package.
//
// SEPARATION OF CONCERNS (mirrors gateway_appsync.go's provisioning vs auth split):
//   - inventory   = "what's on the user's REAL phone" (THIS file — the source,
//                   reported by the phone, keyed by the phone's device id).
//   - desired set = "what we WANT on the clone" (NodeAppSet, gateway_appsync.go).
//   - sync        = install the desired set onto the clone (syncApps).
//   - login       = authorize each app on the clone (gateway_redroid.go) — SEPARATE.
//
// PLATFORM REALITY: only Android phones populate an inventory — iOS cannot
// enumerate installed apps (no public API), so an iPhone user curates the clone's
// app-set by hand. Nothing here assumes a platform; an empty report is valid.
//
// PRIVACY (CLAUDE.md): the inventory is package ids + labels — no secrets. It
// still stays LOCAL (~/.yaver/nodes/<deviceID>/inventory.json), never Convex,
// because the *set of apps a person runs* is itself personal.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── reported-inventory types ─────────────────────────────────────────────────

// PhoneApp is one app the phone reported. System marks an OS/pre-installed
// package (never mirrored onto a clone — it can't be Play-installed).
type PhoneApp struct {
	PackageID string `json:"packageId"`
	Label     string `json:"label,omitempty"`
	System    bool   `json:"system,omitempty"`
}

// PhoneInventory is the snapshot of apps a single phone last reported. Persisted
// at ~/.yaver/nodes/<deviceID>/inventory.json — never Convex.
type PhoneInventory struct {
	DeviceID   string     `json:"deviceId"`
	CapturedAt int64      `json:"capturedAt"`
	Apps       []PhoneApp `json:"apps"`
}

// phoneAppWire tolerates BOTH the mobile native shape (packageName, from
// YaverAppInventoryModule.kt / PhoneInstalledApp) and our own packageId, so the
// phone can post its list verbatim without a client-side rename.
type phoneAppWire struct {
	PackageID   string `json:"packageId"`
	PackageName string `json:"packageName"`
	Label       string `json:"label"`
	System      bool   `json:"system"`
}

// toPhoneApp normalizes a wire app, preferring packageId then packageName. ok is
// false when neither names a package (the entry is dropped).
func (w phoneAppWire) toPhoneApp() (PhoneApp, bool) {
	pkg := strings.TrimSpace(w.PackageID)
	if pkg == "" {
		pkg = strings.TrimSpace(w.PackageName)
	}
	if pkg == "" {
		return PhoneApp{}, false
	}
	return PhoneApp{PackageID: pkg, Label: strings.TrimSpace(w.Label), System: w.System}, true
}

// normalizePhoneApps maps + dedups a wire list into stored PhoneApps.
func normalizePhoneApps(raw []phoneAppWire) []PhoneApp {
	seen := map[string]bool{}
	out := make([]PhoneApp, 0, len(raw))
	for _, w := range raw {
		a, ok := w.toPhoneApp()
		if !ok || seen[a.PackageID] {
			continue
		}
		seen[a.PackageID] = true
		out = append(out, a)
	}
	return out
}

// ── persistence (LOCAL-FIRST, never Convex) ──────────────────────────────────

// gatewayPhoneInventoryPath returns ~/.yaver/nodes/<deviceID>/inventory.json,
// validating the id so it can never escape the nodes directory (same fs-safe
// check the desired-set path uses).
func gatewayPhoneInventoryPath(deviceID string) (string, error) {
	if err := validateConnectorID(deviceID); err != nil {
		return "", fmt.Errorf("device id: %w", err)
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nodes", deviceID, "inventory.json"), nil
}

// savePhoneInventory persists a phone's reported inventory locally (atomic write).
func savePhoneInventory(inv PhoneInventory) error {
	if strings.TrimSpace(inv.DeviceID) == "" {
		return fmt.Errorf("deviceId is required")
	}
	path, err := gatewayPhoneInventoryPath(inv.DeviceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create node dir: %w", err)
	}
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal phone inventory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write phone inventory tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename phone inventory: %w", err)
	}
	return nil
}

// loadPhoneInventory loads a phone's reported inventory. A missing file is not an
// error — it returns an empty inventory (the phone hasn't reported yet).
func loadPhoneInventory(deviceID string) (PhoneInventory, error) {
	path, err := gatewayPhoneInventoryPath(deviceID)
	if err != nil {
		return PhoneInventory{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PhoneInventory{DeviceID: deviceID}, nil
		}
		return PhoneInventory{}, fmt.Errorf("read phone inventory: %w", err)
	}
	var inv PhoneInventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return PhoneInventory{}, fmt.Errorf("parse phone inventory: %w", err)
	}
	if inv.DeviceID == "" {
		inv.DeviceID = deviceID
	}
	return inv, nil
}

// ── derivation: phone inventory → clone desired-set ──────────────────────────

// deriveCloneAppSet turns a reported phone inventory into a clone's desired
// app-set. System apps are NEVER mirrored (they ship with the OS / aren't
// Play-installable). When connectorOnly is true, only apps that back a redroid/
// device connector are kept (mirror just what the gateway can actually drive);
// otherwise every non-system app is mirrored. Order is preserved; ids deduped.
func deriveCloneAppSet(inv PhoneInventory, connectorOnly bool, connectorPkgs map[string]bool) []AppSpec {
	seen := map[string]bool{}
	out := make([]AppSpec, 0, len(inv.Apps))
	for _, a := range inv.Apps {
		pkg := strings.TrimSpace(a.PackageID)
		if pkg == "" || a.System || seen[pkg] {
			continue
		}
		if connectorOnly && !connectorPkgs[pkg] {
			continue
		}
		seen[pkg] = true
		out = append(out, AppSpec{PackageID: pkg})
	}
	return out
}

// connectorPackageSet returns the Android package ids that back a device/redroid
// connector. For those engines Connector.Surface IS the package id (the auth
// handler launches c.Surface), so this is the set the gateway can actually drive.
func connectorPackageSet(reg *ConnectorRegistry) (map[string]bool, error) {
	conns, err := reg.List()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, c := range conns {
		switch strings.ToLower(strings.TrimSpace(c.Engine)) {
		case "redroid", "device":
			if s := strings.TrimSpace(c.Surface); s != "" {
				set[s] = true
			}
		}
	}
	return set, nil
}

// ── MCP entrypoints ──────────────────────────────────────────────────────────

// mcpGatewayPhoneInventoryReport stores the app list a phone reports. This is the
// PUSH side the mobile app calls (via callMcpDirect) so the agent learns what's
// on the user's real phone. Provisioning-only data: no secrets, never Convex.
func mcpGatewayPhoneInventoryReport(deviceID string, apps []phoneAppWire) interface{} {
	if strings.TrimSpace(deviceID) == "" {
		return map[string]interface{}{"error": "device is required (the reporting phone's id)"}
	}
	// Opt-in gate: sharing the app list is off until the user grants it.
	if !consentAllows(consentShareAppInventory) {
		return consentNotEnabled(consentShareAppInventory)
	}
	inv := PhoneInventory{
		DeviceID:   strings.TrimSpace(deviceID),
		CapturedAt: time.Now().Unix(),
		Apps:       normalizePhoneApps(apps),
	}
	if err := savePhoneInventory(inv); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"ok":         true,
		"device":     inv.DeviceID,
		"count":      len(inv.Apps),
		"capturedAt": inv.CapturedAt,
		"note":       "Stored locally (never Convex). Mirror onto a clone with gateway_clone_from_phone.",
	}
}

// mcpGatewayPhoneInventory reads back the inventory a phone last reported.
func mcpGatewayPhoneInventory(deviceID string) interface{} {
	if strings.TrimSpace(deviceID) == "" {
		return map[string]interface{}{"error": "device is required"}
	}
	inv, err := loadPhoneInventory(deviceID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"device":     deviceID,
		"capturedAt": inv.CapturedAt,
		"count":      len(inv.Apps),
		"apps":       inv.Apps,
		"note":       "Apps reported by the user's own phone (local only). Use gateway_clone_from_phone to mirror them onto a clone node.",
	}
}

// mcpGatewayCloneFromPhone derives a clone node's desired app-set from a phone's
// reported inventory, persists it as the clone's desired set, and (when sync)
// installs them all. This is the "mirror my phone onto the clone" entry point.
//
//	phone         — the device id of the phone that reported (source inventory).
//	clone         — the clone node id (redroid/phone adb serial) to provision.
//	connectorOnly — mirror only apps that back a gateway connector (vs every app).
//	mode          — install path passed to syncApps ("play_ui" / "device_owner").
//	sync          — install now; false just writes the desired set.
func mcpGatewayCloneFromPhone(phone, clone string, connectorOnly bool, mode string, sync bool) interface{} {
	if strings.TrimSpace(phone) == "" || strings.TrimSpace(clone) == "" {
		return map[string]interface{}{"error": "phone and clone are required"}
	}
	inv, err := loadPhoneInventory(phone)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if len(inv.Apps) == 0 {
		return map[string]interface{}{"error": "phone " + phone + " has not reported any apps yet — open the Yaver app on that phone to report its app list"}
	}

	var connectorPkgs map[string]bool
	if connectorOnly {
		reg, rerr := NewConnectorRegistry()
		if rerr != nil {
			return map[string]interface{}{"error": rerr.Error()}
		}
		connectorPkgs, rerr = connectorPackageSet(reg)
		if rerr != nil {
			return map[string]interface{}{"error": rerr.Error()}
		}
	}

	apps := deriveCloneAppSet(inv, connectorOnly, connectorPkgs)
	if len(apps) == 0 {
		return map[string]interface{}{
			"phone": phone, "clone": clone, "mirrored": 0,
			"note": "No mirrorable apps (all system, or none back a connector when connectorOnly=true).",
		}
	}

	// Persist as the clone's desired set (reuses gateway_appsync.go). Sync then
	// reconciles the stored set so we never write it twice.
	if err := saveNodeAppSet(NodeAppSet{NodeID: clone, Apps: apps}); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	resp := map[string]interface{}{
		"phone":    phone,
		"clone":    clone,
		"mirrored": len(apps),
		"apps":     apps,
	}
	if !sync {
		resp["note"] = "Desired set written to the clone (local only). Pass sync:true (or call gateway_node_sync) to install them. install ≠ logged in."
		return resp
	}
	resp["sync"] = mcpGatewayNodeSync(clone, nil, mode)
	resp["note"] = "Mirrored + synced. install ≠ logged in — authorize each app via the gateway login broker separately."
	return resp
}

// (deriveCloneAppSet with nil apps passed to mcpGatewayNodeSync reconciles the
// stored desired set we just saved — see gateway_appsync.go.)

// ── HTTP route (alternative to the MCP report tool) ──────────────────────────

// handleGatewayPhoneInventory serves the phone-inventory bridge:
//
//	POST /gateway/phone-inventory  {deviceId, apps:[{packageId|packageName,label,system}]}
//	GET  /gateway/phone-inventory?device=<id>
//
// POST persists what the phone reports; GET reads it back. The phone may also
// supply its id via the X-Device-ID header (the same header the blackbox stream
// uses) when it omits deviceId in the body.
func (s *HTTPServer) handleGatewayPhoneInventory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		deviceID := strings.TrimSpace(r.URL.Query().Get("device"))
		if deviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device query param required"})
			return
		}
		inv, err := loadPhoneInventory(deviceID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"device": deviceID, "capturedAt": inv.CapturedAt, "count": len(inv.Apps), "apps": inv.Apps,
		})

	case http.MethodPost:
		var body struct {
			DeviceID string         `json:"deviceId"`
			Apps     []phoneAppWire `json:"apps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		deviceID := strings.TrimSpace(body.DeviceID)
		if deviceID == "" {
			deviceID = strings.TrimSpace(r.Header.Get("X-Device-ID"))
		}
		res := mcpGatewayPhoneInventoryReport(deviceID, body.Apps)
		if m, ok := res.(map[string]interface{}); ok {
			if _, bad := m["error"]; bad {
				writeJSON(w, http.StatusBadRequest, m)
				return
			}
		}
		writeJSON(w, http.StatusOK, res)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST only"})
	}
}
