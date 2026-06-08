package studio

import (
	"context"
	"strings"
	"testing"
)

func TestCaptureScreenshots(t *testing.T) {
	f := &fakeRunner{
		getData: []byte("\x89PNGSHOT"),
		respond: func(cmd string) string {
			switch {
			case strings.Contains(cmd, "lsmod"):
				return "1"
			case strings.Contains(cmd, "getprop sys.boot_completed"):
				return "1"
			case strings.Contains(cmd, "pm install"):
				return "Success"
			}
			return ""
		},
	}
	surface := newSurface(f)
	spec := ScreenshotSpec{
		App:          App{Package: "io.example", Activity: ".MainActivity"},
		ArtifactPath: "/local/app.apk",
		SettleSec:    0,
		Scenes: []ScreenshotScene{
			{Name: "home"},
			{Name: "settings", Steps: []Step{{Run: func(ctx context.Context, d Driver) error { return d.Key(ctx, "TAB") }}}},
		},
	}
	shots, err := CaptureScreenshots(context.Background(), surface, spec)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(shots) != 2 {
		t.Fatalf("expected 2 shots, got %d", len(shots))
	}
	if shots[0].Name != "home" || shots[1].Name != "settings" {
		t.Errorf("names = %q,%q", shots[0].Name, shots[1].Name)
	}
	if string(shots[0].PNG) != "\x89PNGSHOT" {
		t.Errorf("png bytes = %q", shots[0].PNG)
	}
	if !f.saw("am start -n io.example/.MainActivity") {
		t.Error("did not launch the app")
	}
	if !f.saw("screencap -p") {
		t.Error("did not screencap")
	}
}
