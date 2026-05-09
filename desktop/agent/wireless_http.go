package main

// HTTP handlers for cable + WiFi phone discovery. Both endpoints surface
// the same data as `yaver wire detect` / `yaver wireless detect`. Output
// is read-only and per-agent: nothing here is persisted to Convex (the
// privacy contract forbids leaking serials, UDIDs, or LAN IPs into shared
// state). Each surface (web, mobile, MCP) calls this directly on the
// paired agent over the existing auth-protected HTTP path.

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

type wireDevicesResponse struct {
	Devices []wireDevice `json:"devices"`
	Count   int          `json:"count"`
	Hint    string       `json:"hint,omitempty"`
}

func (s *HTTPServer) handleWireDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	devs := append([]wireDevice{}, listIOSWireDevices(ctx)...)
	devs = append(devs, listAndroidWireDevices(ctx)...)
	writeWireDevicesJSON(w, devs, wireToolHint(false))
}

func (s *HTTPServer) handleWirelessDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	devs := listWirelessDevices(ctx)
	writeWireDevicesJSON(w, devs, wireToolHint(true))
}

func writeWireDevicesJSON(w http.ResponseWriter, devs []wireDevice, hint string) {
	if devs == nil {
		devs = []wireDevice{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wireDevicesResponse{
		Devices: devs,
		Count:   len(devs),
		Hint:    hint,
	})
}

// wireToolHint returns a short "what's missing" string for the UI to
// surface when zero devices are detected. Empty when everything is in
// place. The wireless flag picks between USB-tooling and WiFi-tooling
// preconditions.
func wireToolHint(wireless bool) string {
	missingXcrun := false
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("xcrun"); err != nil {
			missingXcrun = true
		}
	}
	missingAdb := false
	if _, err := exec.LookPath("adb"); err != nil {
		missingAdb = true
	}
	switch {
	case missingXcrun && missingAdb:
		return "neither xcrun nor adb is installed on this machine"
	case missingXcrun && runtime.GOOS == "darwin":
		return "xcrun missing — install Xcode command line tools to detect iOS devices"
	case missingAdb:
		return "adb missing — install android-platform-tools to detect Android devices"
	}
	if wireless && runtime.GOOS != "darwin" {
		return "iOS wireless detection requires macOS + Xcode; Android wireless still works"
	}
	return ""
}
