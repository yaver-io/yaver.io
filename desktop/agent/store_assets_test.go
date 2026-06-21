package main

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildCapturePlan(t *testing.T) {
	l := StoreListing{Screenshots: append([]ScreenshotSlot(nil), requiredScreenshotSlots...)}
	plan := buildCapturePlan(l, "/out")

	// iPad slot has MinCount 0 → excluded; phone+iphone slots included.
	classes := map[string]captureTarget{}
	for _, tg := range plan {
		classes[tg.DeviceClass] = tg
	}
	if _, ok := classes["iPad 12.9\""]; ok {
		t.Error("MinCount 0 slots must be excluded from the plan")
	}
	ip, ok := classes["iPhone 6.7\""]
	if !ok {
		t.Fatal("iPhone 6.7\" should be in the plan")
	}
	if ip.SuggestedDevice == "" {
		t.Error("iPhone 6.7\" should map to a simulator for native-size capture")
	}
	if ip.Width != 1290 || ip.Height != 2796 {
		t.Errorf("iPhone 6.7\" dims wrong: %dx%d", ip.Width, ip.Height)
	}
	if !strings.HasSuffix(ip.OutFile, ".png") || !strings.HasPrefix(ip.OutFile, "/out/") {
		t.Errorf("out file path wrong: %s", ip.OutFile)
	}
	// Feature graphic is composed → no suggested device (skipped at capture).
	if fg, ok := classes["Feature graphic"]; ok && fg.SuggestedDevice != "" {
		t.Error("feature graphic must have no capture device (it's composed)")
	}
}

func TestValidatePNGDims(t *testing.T) {
	good := makePNG(t, 1290, 2796)
	if err := validatePNGDims(good, 1290, 2796); err != nil {
		t.Errorf("exact dims should pass: %v", err)
	}
	if err := validatePNGDims(good, 1080, 1920); err == nil {
		t.Error("wrong dims must be rejected (stores reject off-spec sizes)")
	}
	if err := validatePNGDims([]byte("not a png"), 10, 10); err == nil {
		t.Error("non-PNG must error")
	}
}

func TestSanitizeFilePart(t *testing.T) {
	got := sanitizeFilePart("iPhone 6.7\"")
	if strings.ContainsAny(got, " \"().") {
		t.Errorf("sanitized name still has unsafe chars: %q", got)
	}
	if got == "" {
		t.Error("sanitized name should be non-empty")
	}
}
