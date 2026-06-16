package main

import "testing"

func TestPlanStreamPath(t *testing.T) {
	cases := []struct {
		name        string
		deviceClass string
		w           int
		latency     string
		public      bool
		wantPath    string
	}{
		{"public→rtmp", "phone", 1280, "normal", true, "rtmp"},
		{"tv→webrtc", "tv", 1920, "normal", false, "webrtc"},
		{"projector→webrtc", "projector", 1920, "normal", false, "webrtc"},
		{"low-latency→webrtc", "web", 1280, "low", false, "webrtc"},
		{"glass→mjpeg", "glass", 480, "normal", false, "mjpeg"},
		{"tiny→mjpeg", "web", 360, "normal", false, "mjpeg"},
		{"default phone→mjpeg", "phone", 1280, "normal", false, "mjpeg"},
	}
	for _, tc := range cases {
		got := planStreamPath("capture", tc.deviceClass, "", tc.w, 0, tc.latency, tc.public)
		if got.Path != tc.wantPath {
			t.Errorf("%s: path=%q want %q", tc.name, got.Path, tc.wantPath)
		}
		if got.Endpoint == "" || got.Reason == "" {
			t.Errorf("%s: missing endpoint/reason", tc.name)
		}
	}
}

func TestPlanStreamPathProfileClamped(t *testing.T) {
	// A small web sink should get a downscaled profile (clamped to render width).
	got := planStreamPath("capture", "web", "", 400, 300, "normal", false)
	if got.Profile.MaxWidth != 400 {
		t.Errorf("expected profile clamped to 400px, got %d", got.Profile.MaxWidth)
	}
}
