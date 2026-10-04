package main

// Real-device inventory is intentionally split from the machine inventory.
// The agent host owns the ADB connection; clients receive an opaque id and
// public metadata, never the raw USB serial or wireless-debugging address.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var realDeviceCache struct {
	sync.RWMutex
	devices []RegisteredRealDevice
}
var realDeviceRefreshRunning atomic.Bool

type RegisteredRealDevice struct {
	ID           string   `json:"id"`
	HostDeviceID string   `json:"hostDeviceId,omitempty"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform"`
	Kind         string   `json:"kind"`
	OSVersion    string   `json:"osVersion,omitempty"`
	Transport    string   `json:"transport"`
	Online       bool     `json:"online"`
	Capabilities []string `json:"capabilities"`
	Capture      string   `json:"capture"`
	LastSeen     int64    `json:"lastSeen"`
	serial       string
}

func realDeviceIDFor(serial string) string {
	// HardwareID survives an agent token/device-row repair, so the registered
	// tablet does not duplicate merely because its host was re-paired.
	sum := sha256.Sum256([]byte("yaver-real-device-v1\x00" + HardwareID() + "\x00android\x00" + strings.TrimSpace(serial)))
	return "rd_" + hex.EncodeToString(sum[:12])
}

func adbValue(ctx context.Context, serial string, args ...string) string {
	adbPath, err := resolveAndroidTool("adb")
	if err != nil {
		return ""
	}
	all := append([]string{"-s", serial}, args...)
	out, err := exec.CommandContext(ctx, adbPath, all...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func registeredRealDevices(ctx context.Context) []RegisteredRealDevice {
	now := time.Now().UnixMilli()
	hostID := localDeviceID()
	_, scrcpyErr := exec.LookPath("scrcpy")
	devices := attachedAndroidDevices(ctx)
	out := make([]RegisteredRealDevice, 0, len(devices))
	for _, device := range devices {
		serial := strings.TrimSpace(device.UDID)
		if serial == "" {
			continue
		}
		model := adbValue(ctx, serial, "shell", "getprop", "ro.product.model")
		if model == "" {
			model = strings.TrimSpace(device.Name)
		}
		if model == "" {
			model = "Android device"
		}
		kind := "phone"
		if dims := probeAndroidDims(ctx, serial); dims.Width > 0 && dims.Height > 0 {
			short, long := dims.Width, dims.Height
			if short > long {
				short, long = long, short
			}
			// Android's tablet breakpoint is 600dp. Pixel width alone labels
			// modern 1080px phones as tablets, so account for measured density.
			shortDP := 0
			if dims.Scale > 0 {
				shortDP = short * 160 / dims.Scale
			}
			characteristics := strings.ToLower(adbValue(ctx, serial, "shell", "getprop", "ro.build.characteristics"))
			if shortDP >= 600 || strings.Contains(characteristics, "tablet") || (dims.Scale == 0 && float64(long)/float64(short) < 1.55) {
				kind = "tablet"
			}
		}
		transport := "usb"
		if strings.Contains(serial, ":") || strings.Contains(serial, "_adb-tls-") {
			transport = "wifi"
		}
		capture := "webrtc-adb"
		capabilities := []string{"video", "touch", "keyboard", "install", "launch"}
		if scrcpyErr == nil {
			// Installation is reported separately from the active backend. The
			// current stream remains Yaver WebRTC/ADB until the scrcpy server
			// protocol adapter itself passes the operation probe.
			capabilities = append(capabilities, "scrcpy-installed")
		}
		out = append(out, RegisteredRealDevice{
			ID: realDeviceIDFor(serial), HostDeviceID: hostID, Name: model,
			Platform: "android", Kind: kind,
			OSVersion: adbValue(ctx, serial, "shell", "getprop", "ro.build.version.release"),
			Transport: transport, Online: true, Capabilities: capabilities,
			Capture: capture, LastSeen: now, serial: serial,
		})
	}
	realDeviceCache.Lock()
	realDeviceCache.devices = make([]RegisteredRealDevice, len(out))
	copy(realDeviceCache.devices, out)
	realDeviceCache.Unlock()
	return out
}

// Heartbeats must never wait for adb/property probes. Return the last measured
// inventory and refresh it in a bounded background probe for the next beat.
func realDevicesForHeartbeat() []RegisteredRealDevice {
	realDeviceCache.RLock()
	devices := make([]RegisteredRealDevice, len(realDeviceCache.devices))
	copy(devices, realDeviceCache.devices)
	realDeviceCache.RUnlock()
	if realDeviceRefreshRunning.CompareAndSwap(false, true) {
		go func() {
			defer realDeviceRefreshRunning.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			_ = registeredRealDevices(ctx)
		}()
	}
	for i := range devices {
		devices[i].serial = ""
	}
	return devices
}

func resolveRegisteredRealDevice(ctx context.Context, id string) (string, error) {
	wanted := strings.TrimSpace(id)
	for _, device := range registeredRealDevices(ctx) {
		if device.ID == wanted {
			return device.serial, nil
		}
	}
	return "", fmt.Errorf("registered real device %q is not currently reachable from this host; reconnect USB/Wireless Debugging and accept the authorization prompt", wanted)
}

func publicRegisteredRealDevices(ctx context.Context) []RegisteredRealDevice {
	devices := registeredRealDevices(ctx)
	for i := range devices {
		devices[i].serial = ""
	}
	return devices
}

func realDeviceProbe(ctx context.Context) map[string]any {
	result := map[string]any{"devices": publicRegisteredRealDevices(ctx)}
	if adbPath, err := resolveAndroidTool("adb"); err == nil {
		result["adb"] = map[string]any{"available": true, "pathResolved": adbPath != ""}
	} else {
		result["adb"] = map[string]any{"available": false, "reason": "adb is not installed; run `yaver install remote-runtime`"}
	}
	if path, err := exec.LookPath("scrcpy"); err == nil {
		version := "installed"
		if out, runErr := exec.CommandContext(ctx, path, "--version").CombinedOutput(); runErr == nil {
			if line := strings.TrimSpace(string(out)); line != "" {
				version = strings.Split(line, "\n")[0]
			}
		}
		result["scrcpy"] = map[string]any{"available": true, "version": version}
	} else {
		result["scrcpy"] = map[string]any{"available": false, "reason": "scrcpy is not installed; adb H.264 capture remains available"}
	}
	checks := make([]map[string]any, 0)
	adbPath, _ := resolveAndroidTool("adb")
	for _, device := range registeredRealDevices(ctx) {
		frameCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		out, err := exec.CommandContext(frameCtx, adbPath, "-s", device.serial, "exec-out", "screencap", "-p").Output()
		cancel()
		checks = append(checks, map[string]any{
			"id": device.ID, "name": device.Name,
			"captureOk":    err == nil && len(out) > 8,
			"captureBytes": len(out),
		})
	}
	result["checks"] = checks
	return result
}

func (s *HTTPServer) handleRealDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	jsonReply(w, http.StatusOK, map[string]any{"devices": publicRegisteredRealDevices(ctx)})
}
