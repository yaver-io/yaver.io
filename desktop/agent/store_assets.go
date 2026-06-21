package main

// store_assets.go — generate the store screenshot set from the RUNNING app on
// a simulator (iOS) / redroid-or-emulator (Android), at each store-mandated
// device size. Stores require EXACT pixel dims per device class, so we capture
// natively from a device booted as that class rather than scaling (scaling
// distorts and gets rejected). The capture plan is deterministic + testable;
// the capture itself shells out to `xcrun simctl io … screenshot` / `adb
// exec-out screencap` and is gated on those tools being present.
//
// Output fills StoreListing.screenshots → the projectors upload via the ASC
// `appScreenshotSets` / Play `edits.images` endpoints.

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// captureTarget is one screenshot to take: a slot + how to capture it.
type captureTarget struct {
	Platform    string `json:"platform"`    // ios | android
	DeviceClass string `json:"deviceClass"` // store device class
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	MinCount    int    `json:"minCount"`
	// Suggested device to boot for an EXACT-dim native capture.
	SuggestedDevice string `json:"suggestedDevice"`
	OutFile         string `json:"outFile"`
}

// iosSimulatorForClass maps a store device class → an iOS simulator whose
// native resolution matches the slot (so the capture is exact, no scaling).
var iosSimulatorForClass = map[string]string{
	"iPhone 6.7\"": "iPhone 16 Pro Max",
	"iPhone 6.5\"": "iPhone 11 Pro Max",
	"iPad 12.9\"":  "iPad Pro 13-inch (M4)",
}

// androidProfileForClass maps a class → a redroid/emulator window size.
// "Feature graphic" is intentionally absent — it's a COMPOSED marketing
// image, not a device capture, so suggestedDevice("") flags it to skip.
var androidProfileForClass = map[string]string{
	"Phone": "1080x1920",
}

func suggestedDevice(platform, class string) string {
	if platform == "ios" {
		return iosSimulatorForClass[class]
	}
	return androidProfileForClass[class]
}

// buildCapturePlan turns the listing's required slots into capture targets.
// Feature-graphic slots are flagged (composed, not captured from a device).
func buildCapturePlan(l StoreListing, outDir string) []captureTarget {
	var out []captureTarget
	for _, s := range l.Screenshots {
		if s.MinCount <= 0 {
			continue
		}
		safe := s.Platform + "-" + sanitizeFilePart(s.DeviceClass)
		out = append(out, captureTarget{
			Platform:        s.Platform,
			DeviceClass:     s.DeviceClass,
			Width:           s.Width,
			Height:          s.Height,
			MinCount:        s.MinCount,
			SuggestedDevice: suggestedDevice(s.Platform, s.DeviceClass),
			OutFile:         filepath.Join(outDir, safe+".png"),
		})
	}
	return out
}

func sanitizeFilePart(s string) string {
	repl := map[rune]rune{' ': '-', '"': 0, '(': 0, ')': 0, '.': 0, '/': '-'}
	var b []rune
	for _, r := range s {
		if v, ok := repl[r]; ok {
			if v != 0 {
				b = append(b, v)
			}
			continue
		}
		b = append(b, r)
	}
	return string(b)
}

// pngDims returns the width/height of PNG bytes without full decode.
func pngDims(data []byte) (int, int, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// validatePNGDims confirms captured bytes match the slot's required size —
// the store rejects off-spec dimensions, so we catch it before upload.
func validatePNGDims(data []byte, wantW, wantH int) error {
	w, h, err := pngDims(data)
	if err != nil {
		return fmt.Errorf("not a valid PNG: %w", err)
	}
	if w != wantW || h != wantH {
		return fmt.Errorf("size %dx%d ≠ required %dx%d", w, h, wantW, wantH)
	}
	return nil
}

// captureOne grabs one screenshot from a running device into t.OutFile and
// validates its dimensions. iosUDID/androidSerial select the booted device.
func captureOne(t captureTarget, iosUDID, androidSerial string) error {
	var data []byte
	var err error
	switch t.Platform {
	case "ios":
		if iosUDID == "" {
			return fmt.Errorf("no iOS simulator udid (boot %q, pass --ios-sim)", t.SuggestedDevice)
		}
		tmp := t.OutFile + ".tmp"
		if e := exec.Command("xcrun", "simctl", "io", iosUDID, "screenshot", tmp).Run(); e != nil {
			return fmt.Errorf("simctl screenshot: %w", e)
		}
		data, err = os.ReadFile(tmp)
		_ = os.Remove(tmp)
	case "android":
		if androidSerial == "" {
			return fmt.Errorf("no android serial (boot a %s emulator/redroid, pass --android-serial)", t.DeviceClass)
		}
		c := exec.Command("adb", "-s", androidSerial, "exec-out", "screencap", "-p")
		data, err = c.Output()
	default:
		return fmt.Errorf("unknown platform %q", t.Platform)
	}
	if err != nil {
		return err
	}
	if err := validatePNGDims(data, t.Width, t.Height); err != nil {
		return fmt.Errorf("%s %s: %w", t.Platform, t.DeviceClass, err)
	}
	return os.WriteFile(t.OutFile, data, 0644)
}

// ── CLI ──────────────────────────────────────────────────────────────

func runAssets(args []string) {
	sub := "plan"
	path, outDir := ".", "yaver-store-assets"
	iosUDID, androidSerial := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "plan", "capture":
			sub = args[i]
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "--out":
			if i+1 < len(args) {
				outDir = args[i+1]
				i++
			}
		case "--ios-sim":
			if i+1 < len(args) {
				iosUDID = args[i+1]
				i++
			}
		case "--android-serial":
			if i+1 < len(args) {
				androidSerial = args[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("Usage: yaver assets [plan|capture] [--path DIR] [--out DIR] [--ios-sim UDID] [--android-serial SERIAL]")
			fmt.Println("  plan     show the store-required screenshots + which device to boot for each")
			fmt.Println("  capture  capture each from the booted simulator/emulator at the exact size")
			return
		}
	}

	plan := buildCapturePlan(BuildStoreListing(path), outDir)
	if len(plan) == 0 {
		fmt.Println("No screenshot slots required.")
		return
	}
	if sub == "plan" {
		fmt.Println("Store screenshots needed (capture natively at the exact size — no scaling):")
		for _, t := range plan {
			dev := t.SuggestedDevice
			if dev == "" {
				dev = "(composed)"
			}
			fmt.Printf("  %-8s %-18s %dx%d ×%d  → boot: %s\n", t.Platform, t.DeviceClass, t.Width, t.Height, t.MinCount, dev)
		}
		fmt.Println("\nThen: yaver assets capture --ios-sim <udid> --android-serial <serial>")
		return
	}

	// capture
	if err := os.MkdirAll(outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", outDir, err)
		return
	}
	ok, fail := 0, 0
	for _, t := range plan {
		if t.SuggestedDevice == "" { // feature graphic etc. — composed elsewhere
			fmt.Printf("  ⤷ %s %s is composed, not captured — skipping\n", t.Platform, t.DeviceClass)
			continue
		}
		if err := captureOne(t, iosUDID, androidSerial); err != nil {
			fmt.Printf("  ✗ %s %s: %v\n", t.Platform, t.DeviceClass, err)
			fail++
			continue
		}
		fmt.Printf("  ✓ %s %s → %s\n", t.Platform, t.DeviceClass, t.OutFile)
		ok++
	}
	fmt.Printf("\nCaptured %d, failed %d. (%s)\n", ok, fail, time.Now().Format("15:04:05"))
}
