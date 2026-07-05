package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type mobilePlatformSurface struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Family          string   `json:"family"`
	Surface         string   `json:"surface"`
	Status          string   `json:"status"`
	BuildSupported  bool     `json:"build_supported"`
	SubmitSupported bool     `json:"submit_supported"`
	ManagedCloud    string   `json:"managed_cloud"`
	RequiredHost    string   `json:"required_host"`
	StoreTarget     string   `json:"store_target,omitempty"`
	DeployTarget    string   `json:"deploy_target,omitempty"`
	Script          string   `json:"script,omitempty"`
	ScriptPresent   bool     `json:"script_present"`
	QueueTargets    []string `json:"queue_targets,omitempty"`
	Validation      []string `json:"validation,omitempty"`
	Notes           []string `json:"notes,omitempty"`
	Limitations     []string `json:"limitations,omitempty"`
	PortalActions   []string `json:"portal_actions,omitempty"`
	NextSteps       []string `json:"next_steps,omitempty"`
}

type mobilePlatformMatrixReport struct {
	DevicePlatform string                  `json:"device_platform"`
	DeviceArch     string                  `json:"device_arch"`
	Surfaces       []mobilePlatformSurface `json:"surfaces"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "mobile_platform_matrix",
		Description: "Read-only platform surface matrix for phone, TV, watch, and car deployability. Used by lean-back clients to show what the selected runtime can build or submit.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"directory": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMobilePlatformMatrixHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "mobile_platform_deploy_plan",
		Description: "Dry-run-only platform deploy planner for phone, TV, watch, and car targets. Returns the script/validation plan without building, uploading, or submitting.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"target"},
			"properties": map[string]interface{}{
				"directory": map[string]interface{}{"type": "string"},
				"target":    map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMobilePlatformDeployPlanHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func mobilePlatformMatrix(root string) mobilePlatformMatrixReport {
	root = normalizePlatformRoot(root)
	surface := func(s mobilePlatformSurface) mobilePlatformSurface {
		if s.Script != "" && root != "" {
			_, err := os.Stat(filepath.Join(root, s.Script))
			s.ScriptPresent = err == nil
		}
		return s
	}
	return mobilePlatformMatrixReport{
		DevicePlatform: runtime.GOOS,
		DeviceArch:     runtime.GOARCH,
		Surfaces: []mobilePlatformSurface{
			surface(mobilePlatformSurface{
				ID: "android-mobile", Label: "Android phone", Family: "android", Surface: "mobile",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "playstore", Script: "scripts/deploy-playstore.sh", QueueTargets: []string{"playstore"},
				Validation: []string{"autotest-cdp", "autotest-selenium", "android-device", "android-emulator"},
				Notes:      []string{"Builds the shared Android release AAB used by phone, TV, and Android Auto-capable listings."},
				NextSteps:  []string{"Keep Play signing credentials available locally or on the selected build machine."},
			}),
			surface(mobilePlatformSurface{
				ID: "android-tv", Label: "Android TV / Google TV", Family: "android", Surface: "tv",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "android-tv", Script: "scripts/deploy-android-tv.sh", QueueTargets: []string{"android-tv"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "android-tv-emulator", "android-device"},
				Notes:         []string{"Verifies Leanback launcher metadata, builds the shared Play AAB, and can upload to Play internal testing."},
				PortalActions: []string{"In Play Console, ensure the Android TV form factor is enabled for the Yaver app listing."},
				NextSteps:     []string{"Run mobile_platform_deploy target=android-tv upload=false before the first store upload."},
			}),
			surface(mobilePlatformSurface{
				ID: "android-auto", Label: "Android Auto", Family: "android", Surface: "car",
				Status: "bundled", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "playstore", Script: "scripts/deploy-playstore.sh", QueueTargets: []string{"playstore"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "android-device", "android-emulator"},
				Notes:         []string{"Ships through the Android mobile Play artifact; car-specific review depends on the app's Android Auto categories and manifest."},
				Limitations:   []string{"No separate Android Auto deploy script is needed until we add car-only manifest validation."},
				PortalActions: []string{"In Play Console, complete any Android Auto/car quality review declarations for the messaging notification surface."},
				NextSteps:     []string{"Verify the Android Auto MessagingStyle path in Desktop Head Unit before marking the car surface generally available."},
			}),
			surface(mobilePlatformSurface{
				ID: "android-wear", Label: "Wear OS", Family: "android", Surface: "watch",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "wear-os", Script: "scripts/deploy-wear-os.sh", QueueTargets: []string{"wear-os"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "wear-emulator", "android-device"},
				Notes:         []string{"Builds the standalone Wear OS AAB as a watch-only io.yaver.mobile artifact and uploads it to the existing Play internal track."},
				PortalActions: []string{"In Play Console, enable the Wear OS form factor and confirm the watch-only artifact is accepted under the existing package."},
				NextSteps:     []string{"Pair-test the Wear Data Layer bridge with the Android app before promoting beyond internal testing."},
			}),
			surface(mobilePlatformSurface{
				ID: "ios-mobile", Label: "iPhone / iPad", Family: "apple", Surface: "mobile",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "needs-macos", RequiredHost: "macos",
				StoreTarget: "testflight", DeployTarget: "testflight", Script: "scripts/deploy-testflight.sh", QueueTargets: []string{"testflight"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "ios-simulator", "ios-device-webdriveragent"},
				Notes:         []string{"Archives locally on a signed-in Mac and uploads to TestFlight when Apple credentials are available."},
				PortalActions: []string{"Keep the io.yaver.mobile App ID, App Store Connect app record, signing certificate, and TestFlight groups current."},
			}),
			surface(mobilePlatformSurface{
				ID: "tvos", Label: "Apple TV", Family: "apple", Surface: "tv",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "needs-macos", RequiredHost: "macos",
				StoreTarget: "app-store-connect", DeployTarget: "tvos", Script: "scripts/deploy-tvos.sh", QueueTargets: []string{"tvos"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "tvos-simulator"},
				Notes:         []string{"Builds the standalone tvOS app; upload requires the Apple TV bundle and App Store Connect signing path to be configured."},
				PortalActions: []string{"In App Store Connect, add the tvOS platform for the existing Yaver app record and ensure the bundle ID matches the tvOS Xcode target.", "Create or let Xcode manage a tvOS App Store provisioning profile for the target."},
				NextSteps:     []string{"Run mobile_platform_deploy target=tvos upload=false on a Mac before creating TestFlight metadata."},
			}),
			surface(mobilePlatformSurface{
				ID: "watchos", Label: "Apple Watch", Family: "apple", Surface: "watch",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "needs-macos", RequiredHost: "macos",
				StoreTarget: "app-store-connect", DeployTarget: "watchos", Script: "scripts/deploy-watchos.sh", QueueTargets: []string{"watchos"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "ios-simulator", "ios-device-webdriveragent"},
				Notes:         []string{"Builds, archives, and uploads the standalone watchOS app using bundle io.yaver.mobile.watch."},
				Limitations:   []string{"Real upload requires an App Store Connect watchOS app record/profile that accepts the standalone watch archive."},
				PortalActions: []string{"In Certificates, Identifiers & Profiles, make sure the watch App ID exists as io.yaver.mobile.watch under the same team.", "Regenerate/download development profiles after the physical Apple Watch appears as a paired Xcode destination."},
				NextSteps:     []string{"Run mobile_platform_deploy target=watchos upload=false for build verification, then upload=true when the App Store Connect watch record/profile is ready."},
			}),
			surface(mobilePlatformSurface{
				ID: "carplay", Label: "Apple CarPlay", Family: "apple", Surface: "car",
				Status: "ready-gated", BuildSupported: true, SubmitSupported: true, ManagedCloud: "needs-entitlement", RequiredHost: "macos",
				StoreTarget: "testflight", DeployTarget: "carplay", Script: "scripts/deploy-carplay.sh", QueueTargets: []string{"carplay"},
				Validation:    []string{"autotest-cdp", "autotest-selenium", "ios-simulator", "ios-device-webdriveragent"},
				Notes:         []string{"CarPlay ships inside the shared iOS/TestFlight artifact after preflight confirms the entitlement, scene manifest, and native scene delegate are wired."},
				Limitations:   []string{"Upload still requires Apple's granted CarPlay entitlement in the provisioning profile; the script fails fast before upload if local project wiring is missing."},
				PortalActions: []string{"Confirm Apple granted com.apple.developer.carplay-voice-based-conversation, then regenerate the iOS App Store provisioning profile with that entitlement."},
				NextSteps:     []string{"Run mobile_platform_deploy target=carplay upload=false for unsigned simulator build/preflight, then upload=true to submit the shared iOS artifact."},
			}),
		},
	}
}

func normalizePlatformRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		if r, _, err := findDeployRepoRoot(); err == nil {
			root = r
		}
	}
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if root != "" && !filepath.IsAbs(root) {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	return root
}

func mcpMobilePlatformMatrix(directory string) map[string]interface{} {
	report := mobilePlatformMatrix(directory)
	return map[string]interface{}{"ok": true, "matrix": report}
}

func opsMobilePlatformMatrixHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Dir string `json:"directory"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	return OpsResult{OK: true, Initial: mcpMobilePlatformMatrix(p.Dir)}
}

func opsMobilePlatformDeployPlanHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Dir    string `json:"directory"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Target) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "target is required"}
	}
	out := mcpMobilePlatformDeploy(p.Dir, p.Target, false, true, 0, platformValidationConfig{})
	if errText, _ := out["error"].(string); errText != "" {
		return OpsResult{OK: false, Code: "platform_deploy_plan_failed", Error: errText, Initial: out}
	}
	return OpsResult{OK: true, Initial: out}
}

func (s *HTTPServer) handleMobilePlatformMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	jsonReply(w, http.StatusOK, mobilePlatformMatrix(r.URL.Query().Get("directory")))
}
