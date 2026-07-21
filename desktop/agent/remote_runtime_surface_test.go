package main

import "testing"

// Yaver's pragmatic defaults: RN/Flutter → browser (fast), Kotlin → emulator,
// Swift → simulator. User-overridable, but these are what Yaver picks unasked.
func TestDefaultStreamingSurface(t *testing.T) {
	cases := map[string]StreamingSurface{
		"expo": SurfaceBrowser, "react-native": SurfaceBrowser, "flutter": SurfaceBrowser,
		"kotlin": SurfaceEmulator, "swift": SurfaceSimulator, "nextjs": SurfaceBrowser,
	}
	for fw, want := range cases {
		if got := defaultStreamingSurface(fw); got != want {
			t.Errorf("defaultStreamingSurface(%q) = %q, want %q", fw, got, want)
		}
	}
}

// Override is honored only when valid for the framework; else falls back to default.
func TestResolveStreamingSurface(t *testing.T) {
	// RN can be forced to emulator or simulator.
	if got := resolveStreamingSurface("react-native", "emulator"); got != SurfaceEmulator {
		t.Errorf("RN override emulator = %q", got)
	}
	if got := resolveStreamingSurface("react-native", "simulator"); got != SurfaceSimulator {
		t.Errorf("RN override simulator = %q", got)
	}
	// Kotlin CANNOT be browser (no web build) — invalid override falls back to emulator default.
	if got := resolveStreamingSurface("kotlin", "browser"); got != SurfaceEmulator {
		t.Errorf("kotlin browser override must fall back to emulator, got %q", got)
	}
	// Empty override → default.
	if got := resolveStreamingSurface("flutter", ""); got != SurfaceBrowser {
		t.Errorf("flutter empty override = %q, want browser", got)
	}
}

func TestStreamingSurfaceOptionsDefaultFirst(t *testing.T) {
	opts := streamingSurfaceOptions("react-native")
	if len(opts) == 0 || opts[0] != SurfaceBrowser {
		t.Errorf("RN options must lead with browser (the default), got %v", opts)
	}
	if got := streamingSurfaceOptions("swift"); len(got) != 1 || got[0] != SurfaceSimulator {
		t.Errorf("swift options = %v, want [simulator]", got)
	}
}

// The browser streaming surface builds each framework's WEB dev server with HMR —
// the fast RN/Flutter default. Contract pinned so the surface can't silently
// build the wrong thing.
func TestWebDevServerCommand(t *testing.T) {
	cases := map[string]string{
		"expo":         "npx expo start --web --port 8090",
		"react-native": "npx expo start --web --port 8090",
		"flutter":      "flutter run -d web-server --web-port 8090 --web-hostname 127.0.0.1",
		"nextjs":       "npx next dev -p 8090",
	}
	for fw, want := range cases {
		cmd, err := webDevServerCommand(fw, 8090)
		if err != nil {
			t.Errorf("%s: %v", fw, err)
			continue
		}
		if got := joinStr(cmd); got != want {
			t.Errorf("webDevServerCommand(%q) = %q, want %q", fw, got, want)
		}
	}
	if _, err := webDevServerCommand("kotlin", 8090); err == nil {
		t.Error("kotlin has no web dev server — must error, not build the wrong thing")
	}
}

func joinStr(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}
