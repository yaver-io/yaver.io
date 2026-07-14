package main

import (
	"net/http"
	"testing"
)

func TestNormalizeSurface(t *testing.T) {
	cases := map[string]ClientSurface{
		"tv":        SurfaceTV,
		"tvOS":      SurfaceTV,
		"appletv":   SurfaceTV,
		"androidtv": SurfaceTV,
		"watch":     SurfaceWatch,
		"wearos":    SurfaceWatch,
		"car":       SurfaceCar,
		"carplay":   SurfaceCar,
		"vision":    SurfaceVision,
		"visionos":  SurfaceVision,
		"vr":        SurfaceVision,
		"ar":        SurfaceVision,
		"phone":     SurfaceMobile,
		"ios":       SurfaceMobile,
		"tablet":    SurfaceTablet,
		"ipad":      SurfaceTablet,
		"web":       SurfaceWeb,
		"cli":       SurfaceCLI,
		"  TV  ":    SurfaceTV, // trims + lowercases
		"":          SurfaceUnknown,
		"nonsense":  SurfaceUnknown,
	}
	for in, want := range cases {
		if got := normalizeSurface(in); got != want {
			t.Errorf("normalizeSurface(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSurfaceFromHeaders(t *testing.T) {
	h := http.Header{}
	if s := surfaceFromHeaders(h); s != SurfaceUnknown {
		t.Errorf("empty header should be unknown, got %q", s)
	}
	h.Set(surfaceHeader, "tv")
	if s := surfaceFromHeaders(h); s != SurfaceTV {
		t.Errorf("X-Yaver-Surface: tv should parse to tv, got %q", s)
	}
	if s := surfaceFromHeaders(nil); s != SurfaceUnknown {
		t.Errorf("nil header should be unknown, got %q", s)
	}
}
