package main

// ops_droid_discovery.go — read-only Android/Redroid discovery verbs for the
// grand-MCP operator loop. These verbs inventory apps and visible UI state; they
// never tap, type, launch, submit, or read app-private storage.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "droid_packages",
		Description: "Read-only: list installed Android package ids on the attached device/Redroid. Payload {device?, filter?, limit?}. Does not launch apps or read app data.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "Optional adb serial."},
			"filter": map[string]interface{}{"type": "string", "description": "Optional case-insensitive package substring filter."},
			"limit":  map[string]interface{}{"type": "integer", "description": "Max packages to return, default 250, max 1000."},
		}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var p struct {
				Device string `json:"device"`
				Filter string `json:"filter"`
				Limit  int    `json:"limit"`
			}
			_ = json.Unmarshal(payload, &p)
			serial := droidResolveDevice(strings.TrimSpace(p.Device))
			if serial == "" {
				return OpsResult{OK: false, Code: "no_android_device", Error: "no Android device attached"}
			}
			pkgs, err := droidInstalledPackages(serial, p.Filter, p.Limit)
			if err != nil {
				return OpsResult{OK: false, Code: "adb_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"device":   serial,
				"packages": pkgs,
				"count":    len(pkgs),
				"policy":   "read-only package inventory; no app launch or private data read",
			}}
		},
	})

	registerOpsVerb(opsVerbSpec{
		Name:        "droid_ui_elements",
		Description: "Read-only: dump the current Android UI hierarchy as structured visible/actionable nodes. Payload {device?, limit?}. Use before any input; passwords are flagged and values should not be stored.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "Optional adb serial."},
			"limit":  map[string]interface{}{"type": "integer", "description": "Max UI nodes to return, default 120, max 250."},
		}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var p struct {
				Device string `json:"device"`
				Limit  int    `json:"limit"`
			}
			_ = json.Unmarshal(payload, &p)
			serial := droidResolveDevice(strings.TrimSpace(p.Device))
			if serial == "" {
				return OpsResult{OK: false, Code: "no_android_device", Error: "no Android device attached"}
			}
			nodes, err := droidUIElements(serial, p.Limit)
			if err != nil {
				return OpsResult{OK: false, Code: "adb_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"device": serial,
				"focus":  droidFocus(serial),
				"nodes":  nodes,
				"count":  len(nodes),
				"policy": "read-only UI discovery; input/submit requires a separate user-approved action",
			}}
		},
	})

	registerOpsVerb(opsVerbSpec{
		Name:        "droid_operator_snapshot",
		Description: "Read-only: combined Android operator snapshot for agent planning: status, focus, optional package matches, and current visible UI nodes. Payload {device?, package_filter?, package_limit?, ui_limit?}.",
		Schema: atvSchema(map[string]interface{}{
			"device":         map[string]interface{}{"type": "string", "description": "Optional adb serial."},
			"package_filter": map[string]interface{}{"type": "string", "description": "Optional package substring filter, e.g. trugo or esarj."},
			"package_limit":  map[string]interface{}{"type": "integer", "description": "Max packages, default 50."},
			"ui_limit":       map[string]interface{}{"type": "integer", "description": "Max UI nodes, default 80."},
		}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var p struct {
				Device        string `json:"device"`
				PackageFilter string `json:"package_filter"`
				PackageLimit  int    `json:"package_limit"`
				UILimit       int    `json:"ui_limit"`
			}
			_ = json.Unmarshal(payload, &p)
			serial := droidResolveDevice(strings.TrimSpace(p.Device))
			if serial == "" {
				return OpsResult{OK: false, Code: "no_android_device", Error: "no Android device attached"}
			}
			if p.PackageLimit <= 0 {
				p.PackageLimit = 50
			}
			if p.UILimit <= 0 {
				p.UILimit = 80
			}
			pkgs, pkgErr := droidInstalledPackages(serial, p.PackageFilter, p.PackageLimit)
			nodes, uiErr := droidUIElements(serial, p.UILimit)
			w, h := droidSize(serial)
			initial := map[string]interface{}{
				"device":   serial,
				"w":        w,
				"h":        h,
				"focus":    droidFocus(serial),
				"packages": pkgs,
				"nodes":    nodes,
				"policy":   "read-only planning snapshot. Launch/input/submit/payment/start/stop are separate gated actions.",
			}
			if pkgErr != nil {
				initial["packages_error"] = pkgErr.Error()
			}
			if uiErr != nil {
				initial["ui_error"] = uiErr.Error()
			}
			return OpsResult{OK: pkgErr == nil || uiErr == nil, Initial: initial}
		},
	})
}
