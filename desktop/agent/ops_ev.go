package main

// ops_ev.go — EV charging operator helpers.
//
// These verbs intentionally do not start or stop a paid charging session. They
// expose provider metadata and launch a real provider app on an attached Android
// device so the owner can complete QR, SMS, payment, and final start/stop inside
// the provider UI. MCP callers reach these through the generic `ops` tool.

import (
	"encoding/json"
	"strings"
)

type evProviderInfo struct {
	ID                  string   `json:"id"`
	Label               string   `json:"label"`
	Domains             []string `json:"domains"`
	AndroidPackageHints []string `json:"androidPackageHints"`
	Notes               []string `json:"notes,omitempty"`
}

var evProviderCatalog = []evProviderInfo{
	{ID: "esarj", Label: "Esarj", Domains: []string{"esarj.com", "esarj.com.tr"}, AndroidPackageHints: []string{"com.esarj.mobile", "esarj"}, Notes: []string{"QR-first app, account/payment readiness gates, SMS may be required."}},
	{ID: "zes", Label: "ZES", Domains: []string{"zes.net", "zes.com.tr", "zorluenergy.com.tr"}, AndroidPackageHints: []string{"com.solidict.zorluenerji", "zes"}, Notes: []string{"Map home has quick QR start and AC/DC/HPC filters."}},
	{ID: "trugo", Label: "Trugo", Domains: []string{"trugo.com.tr"}, AndroidPackageHints: []string{"com.togg.trugoapp", "trugo"}, Notes: []string{"QR and station-code flow; supports AutoCharge-style provider features where the user has configured them."}},
	{ID: "enyakit", Label: "En Yakıt", Domains: []string{"enyakit.com.tr"}, AndroidPackageHints: []string{"com.ilerleyen.EnYakit", "enyakit"}, Notes: []string{"Phone-number/SMS login flow observed; treat OTP as user-present only."}},
	{ID: "voltrun", Label: "Voltrun", Domains: []string{"voltrun.com"}, AndroidPackageHints: []string{"com.voltrun", "voltrun"}, Notes: []string{"Public docs describe starting charge by scanning the unit QR in the Voltrun app."}},
	{ID: "sharz", Label: "Sharz", Domains: []string{"sharz.net"}, AndroidPackageHints: []string{"com.ipitex.sharz", "sharz"}, Notes: []string{"Provider app can view station availability and start charging through the app."}},
	{ID: "sarjtr", Label: "Şarj@TR", Domains: []string{"epdk.gov.tr"}, AndroidPackageHints: []string{"tr.gov.epdk.sarjetTR", "sarjet"}, Notes: []string{"Public EPDK station/socket availability app; discovery/status route, not a payment provider."}},
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ev_provider_catalog",
		Description: "List EV charging providers Yaver knows how to classify/launch in Turkey. Metadata only; does not start or stop charging.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"providers": evProviderCatalog,
				"policy":    "Yaver launches/supervises real provider apps. SMS, payment, start, and stop require user approval.",
			}}
		},
	})

	registerOpsVerb(opsVerbSpec{
		Name:        "ev_android_status",
		Description: "Report whether this machine has an attached Android device ready for supervised EV provider app control.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			serial := droidResolveDevice("")
			if serial == "" {
				return OpsResult{OK: true, Initial: map[string]interface{}{"device": nil, "ready": false}}
			}
			w, h := droidSize(serial)
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"device": serial,
				"ready":  true,
				"w":      w,
				"h":      h,
				"focus":  droidFocus(serial),
			}}
		},
	})

	registerOpsVerb(opsVerbSpec{
		Name:        "ev_android_launch",
		Description: "Launch a real EV provider app on an attached Android device for supervised user-present charging. Payload {provider?, package?, device?}. Does not start/stop charging.",
		Schema: atvSchema(map[string]interface{}{
			"provider": map[string]interface{}{"type": "string", "description": "Provider id, e.g. esarj|zes|trugo|enyakit|voltrun|sharz|sarjtr."},
			"package":  map[string]interface{}{"type": "string", "description": "Optional exact Android package or search hint. Overrides provider default."},
			"device":   map[string]interface{}{"type": "string", "description": "Optional adb serial."},
		}),
		Handler: evAndroidLaunchHandler,
	})
}

func evAndroidLaunchHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Provider string `json:"provider"`
		Package  string `json:"package"`
		Device   string `json:"device"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	hint := strings.TrimSpace(p.Package)
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	if hint == "" && provider != "" {
		for _, item := range evProviderCatalog {
			if item.ID == provider && len(item.AndroidPackageHints) > 0 {
				hint = item.AndroidPackageHints[0]
				break
			}
		}
	}
	if hint == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "provider or package is required"}
	}
	serial := droidResolveDevice(strings.TrimSpace(p.Device))
	if serial == "" {
		return OpsResult{OK: false, Code: "no_android_device", Error: "no Android device attached"}
	}
	pkg, err := droidLaunchPackage(serial, hint)
	if err != nil {
		return OpsResult{OK: false, Code: "launch_failed", Error: err.Error()}
	}
	w, h := droidSize(serial)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"provider":   provider,
		"package":    pkg,
		"device":     serial,
		"w":          w,
		"h":          h,
		"focus":      droidFocus(serial),
		"frame_path": "/droid/frame?device=" + serial,
		"input_path": "/droid/input",
		"policy":     "Provider app launched only. User must approve SMS, payment, start, and stop in the visible provider UI.",
	}}
}
