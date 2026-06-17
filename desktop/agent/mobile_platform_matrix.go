package main

import (
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
	Notes           []string `json:"notes,omitempty"`
	Limitations     []string `json:"limitations,omitempty"`
}

type mobilePlatformMatrixReport struct {
	DevicePlatform string                  `json:"device_platform"`
	DeviceArch     string                  `json:"device_arch"`
	Surfaces       []mobilePlatformSurface `json:"surfaces"`
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
				Notes: []string{"Builds the shared Android release AAB used by phone, TV, and Android Auto-capable listings."},
			}),
			surface(mobilePlatformSurface{
				ID: "android-tv", Label: "Android TV / Google TV", Family: "android", Surface: "tv",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "android-tv", Script: "scripts/deploy-android-tv.sh", QueueTargets: []string{"android-tv"},
				Notes: []string{"Verifies Leanback launcher metadata, builds the shared Play AAB, and can upload to Play internal testing."},
			}),
			surface(mobilePlatformSurface{
				ID: "android-auto", Label: "Android Auto", Family: "android", Surface: "car",
				Status: "bundled", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "playstore", Script: "scripts/deploy-playstore.sh", QueueTargets: []string{"playstore"},
				Notes:       []string{"Ships through the Android mobile Play artifact; car-specific review depends on the app's Android Auto categories and manifest."},
				Limitations: []string{"No separate Android Auto deploy script is needed until we add car-only manifest validation."},
			}),
			surface(mobilePlatformSurface{
				ID: "android-wear", Label: "Wear OS", Family: "android", Surface: "watch",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "supported", RequiredHost: "any",
				StoreTarget: "playstore", DeployTarget: "wear-os", Script: "scripts/deploy-wear-os.sh", QueueTargets: []string{"wear-os"},
				Notes: []string{"Builds the standalone Wear OS AAB as a watch-only io.yaver.mobile artifact and uploads it to the existing Play internal track."},
			}),
			surface(mobilePlatformSurface{
				ID: "ios-mobile", Label: "iPhone / iPad", Family: "apple", Surface: "mobile",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "needs-macos", RequiredHost: "macos",
				StoreTarget: "testflight", DeployTarget: "testflight", Script: "scripts/deploy-testflight.sh", QueueTargets: []string{"testflight"},
				Notes: []string{"Archives locally on a signed-in Mac and uploads to TestFlight when Apple credentials are available."},
			}),
			surface(mobilePlatformSurface{
				ID: "tvos", Label: "Apple TV", Family: "apple", Surface: "tv",
				Status: "ready", BuildSupported: true, SubmitSupported: true, ManagedCloud: "needs-macos", RequiredHost: "macos",
				StoreTarget: "app-store-connect", DeployTarget: "tvos", Script: "scripts/deploy-tvos.sh", QueueTargets: []string{"tvos"},
				Notes: []string{"Builds the standalone tvOS app; upload requires the Apple TV bundle and App Store Connect signing path to be configured."},
			}),
			surface(mobilePlatformSurface{
				ID: "watchos", Label: "Apple Watch", Family: "apple", Surface: "watch",
				Status: "build-only", BuildSupported: true, SubmitSupported: false, ManagedCloud: "planned", RequiredHost: "macos",
				Script:      "scripts/deploy-watchos.sh",
				Notes:       []string{"Builds a signed standalone watchOS archive; upload should ride a companion iOS archive or an Apple-accepted standalone watch submission flow."},
				Limitations: []string{"Not queued from mobile/web yet."},
			}),
			surface(mobilePlatformSurface{
				ID: "carplay", Label: "Apple CarPlay", Family: "apple", Surface: "car",
				Status: "blocked", BuildSupported: false, SubmitSupported: false, ManagedCloud: "needs-entitlement", RequiredHost: "macos",
				Notes:       []string{"CarPlay is not a separate App Store upload; it needs Apple entitlement approval and native CarPlay scene/template work."},
				Limitations: []string{"Do not expose as a one-click deploy until entitlement and app UX are approved."},
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

func (s *HTTPServer) handleMobilePlatformMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	jsonReply(w, http.StatusOK, mobilePlatformMatrix(r.URL.Query().Get("directory")))
}
