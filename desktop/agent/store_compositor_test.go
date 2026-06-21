package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateFeatureGraphicDims(t *testing.T) {
	img := GenerateFeatureGraphic("Receipt Scanner", "Snap & track expenses", 1024, 500)
	if img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 500 {
		t.Errorf("feature graphic dims = %v, want 1024x500", img.Bounds())
	}
	// Defaults when zero.
	d := GenerateFeatureGraphic("", "", 0, 0)
	if d.Bounds().Dx() != 1024 || d.Bounds().Dy() != 500 {
		t.Errorf("default dims = %v", d.Bounds())
	}
}

func TestComposeMarketingScreenshotDims(t *testing.T) {
	shot := image.NewRGBA(image.Rect(0, 0, 300, 650))
	out := ComposeMarketingScreenshot(shot, "Track every receipt", "seed", 1290, 2796)
	if out.Bounds().Dx() != 1290 || out.Bounds().Dy() != 2796 {
		t.Errorf("composed dims = %v, want 1290x2796 (stores need exact size)", out.Bounds())
	}
	// Nil shot must not panic (caption-only canvas).
	out2 := ComposeMarketingScreenshot(nil, "x", "s", 100, 200)
	if out2.Bounds().Dx() != 100 || out2.Bounds().Dy() != 200 {
		t.Error("nil-shot compose should still produce the canvas")
	}
}

func TestBrandColorDeterministic(t *testing.T) {
	a := brandColor("Receipt Scanner")
	b := brandColor("Receipt Scanner")
	if a != b {
		t.Error("brand color must be stable for the same app name")
	}
	if brandColor("Other App") == a {
		t.Error("different names should (almost always) differ")
	}
	if a.A != 255 {
		t.Errorf("alpha should be opaque, got %d", a.A)
	}
}

func TestWritePNGRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fg.png")
	if err := writePNG(GenerateFeatureGraphic("App", "tag", 1024, 500), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePNGDims(data, 1024, 500); err != nil {
		t.Errorf("written feature graphic has wrong dims: %v", err)
	}
}
