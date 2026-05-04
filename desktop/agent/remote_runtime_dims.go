package main

// remote_runtime_dims.go — probe a booted simulator/emulator for its
// current screen dimensions and rotation, then surface them to the
// web viewer as a `dims` event so it can scale pointer coordinates.
//
// The probes are intentionally robust-by-default: every platform path
// returns a sensible iPhone-15-shape fallback if anything fails so a
// session never hangs on a parse error. The viewer can correct itself
// on the next rotation event.

import (
	"context"
	"encoding/binary"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DeviceDims is the device's logical screen resolution + rotation as
// reported by the platform. The viewer uses these to scale pointer
// coordinates so a 4K monitor and a laptop produce identical taps for
// the same UI control. Width/Height are device pixels; Scale is the
// @x DPI factor (informational, not load-bearing).
type DeviceDims struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Scale    int    `json:"scale,omitempty"`
	Rotation string `json:"rotation,omitempty"` // "portrait" | "landscape"
}

// fallbackDims is the iPhone 15 portrait shape — a sensible default
// when probing fails so the viewer can render *something* until the
// real dims arrive on a rotation event.
var fallbackDims = DeviceDims{Width: 393, Height: 852, Scale: 3, Rotation: "portrait"}

// ProbeDeviceDims dispatches to the right per-platform probe based on
// the remote-runtime targetID. Unknown targets get the fallback so
// the rest of the pipeline can keep moving.
func ProbeDeviceDims(ctx context.Context, targetID, deviceID string) DeviceDims {
	if strings.TrimSpace(deviceID) == "" {
		return fallbackDims
	}
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	switch targetID {
	case "ios-simulator":
		return probeIOSDims(probeCtx, deviceID)
	case "android-emulator":
		return probeAndroidDims(probeCtx, deviceID)
	}
	return fallbackDims
}

// probeAndroidDims reads `adb shell wm size` (and `wm density` for
// the scale factor). The "Override size:" line — if present — wins
// over "Physical size:" because that's what the user actually sees.
// Returns fallback dims rather than blocking session start on parse
// failures.
func probeAndroidDims(ctx context.Context, deviceID string) DeviceDims {
	args := []string{}
	if strings.TrimSpace(deviceID) != "" {
		args = append(args, "-s", deviceID)
	}
	args = append(args, "shell", "wm", "size")
	out, err := exec.CommandContext(ctx, "adb", args...).Output()
	if err != nil {
		return fallbackDims
	}
	dims := parseAndroidWMSize(string(out))
	if dims.Width == 0 || dims.Height == 0 {
		return fallbackDims
	}
	dims.Scale = probeAndroidDensity(ctx, deviceID)
	return dims
}

func parseAndroidWMSize(text string) DeviceDims {
	var w, h int
	var override bool
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(line, "Override size:"):
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Override size:"))
			override = true
		case strings.HasPrefix(line, "Physical size:") && !override:
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Physical size:"))
		default:
			continue
		}
		parts := strings.SplitN(rest, "x", 2)
		if len(parts) != 2 {
			continue
		}
		pw, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		ph, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil && pw > 0 && ph > 0 {
			w, h = pw, ph
			if override {
				break
			}
		}
	}
	if w == 0 || h == 0 {
		return DeviceDims{}
	}
	rot := "portrait"
	if w > h {
		rot = "landscape"
	}
	return DeviceDims{Width: w, Height: h, Rotation: rot}
}

func probeAndroidDensity(ctx context.Context, deviceID string) int {
	args := []string{}
	if strings.TrimSpace(deviceID) != "" {
		args = append(args, "-s", deviceID)
	}
	args = append(args, "shell", "wm", "density")
	out, err := exec.CommandContext(ctx, "adb", args...).Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// "Physical density: 440" or "Override density: 320"
		var rest string
		if strings.HasPrefix(line, "Override density:") {
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Override density:"))
		} else if strings.HasPrefix(line, "Physical density:") {
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Physical density:"))
		} else {
			continue
		}
		dpi, err := strconv.Atoi(rest)
		if err != nil || dpi <= 0 {
			continue
		}
		// Approximate scale buckets — Android maps 160 dpi → @1, 320
		// → @2, 480 → @3, 640 → @4. Round to the nearest 80 dpi step.
		scale := (dpi + 79) / 160
		if scale < 1 {
			scale = 1
		}
		return scale
	}
	return 0
}

// probeIOSDims grabs a PNG screenshot and reads its IHDR header to
// discover the device's pixel dimensions. Robust across every
// device-type-identifier without needing to embed a static map.
// Cheap (one screenshot, ~50 ms on Apple Silicon).
func probeIOSDims(ctx context.Context, deviceID string) DeviceDims {
	cmd := exec.CommandContext(ctx, "xcrun", "simctl", "io", deviceID, "screenshot", "--type=png", "-")
	raw, err := cmd.Output()
	if err != nil {
		return fallbackDims
	}
	dims := parsePNGDims(raw)
	if dims.Width == 0 || dims.Height == 0 {
		return fallbackDims
	}
	// iOS Simulators always report @3 on iPhone Pro variants, @2 on
	// SE / iPad. We don't differentiate here — the viewer only needs
	// width/height for pointer scaling. Leave Scale=0 (informational).
	return dims
}

// parsePNGDims extracts width/height from a PNG IHDR chunk, which
// sits at offset 16 (after the 8-byte signature + 4-byte length +
// "IHDR" type). Both numbers are big-endian uint32. Sets Rotation
// from the aspect ratio.
func parsePNGDims(raw []byte) DeviceDims {
	if len(raw) < 24 {
		return DeviceDims{}
	}
	// PNG signature: 89 50 4E 47 0D 0A 1A 0A
	if !(raw[0] == 0x89 && raw[1] == 0x50 && raw[2] == 0x4E && raw[3] == 0x47) {
		return DeviceDims{}
	}
	if string(raw[12:16]) != "IHDR" {
		return DeviceDims{}
	}
	w := int(binary.BigEndian.Uint32(raw[16:20]))
	h := int(binary.BigEndian.Uint32(raw[20:24]))
	if w <= 0 || h <= 0 {
		return DeviceDims{}
	}
	rot := "portrait"
	if w > h {
		rot = "landscape"
	}
	return DeviceDims{Width: w, Height: h, Rotation: rot}
}
