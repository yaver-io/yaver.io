package main

import (
	"testing"

	"github.com/yaver-io/agent/ghost"
)

func TestRDControlEnforce(t *testing.T) {
	cases := []struct {
		name    string
		pol     RemoteDesktopPolicy
		remote  bool
		allowed bool
	}{
		{"control off denies local", RemoteDesktopPolicy{ControlEnabled: false, AllowRemoteControl: true}, false, false},
		{"control off denies remote", RemoteDesktopPolicy{ControlEnabled: false, AllowRemoteControl: true}, true, false},
		{"control on local allowed", RemoteDesktopPolicy{ControlEnabled: true, AllowRemoteControl: false}, false, true},
		{"control on remote blocked when remote disallowed", RemoteDesktopPolicy{ControlEnabled: true, AllowRemoteControl: false}, true, false},
		{"control on remote allowed", RemoteDesktopPolicy{ControlEnabled: true, AllowRemoteControl: true}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := rdControlEnforce(c.pol, c.remote)
			if ok != c.allowed {
				t.Fatalf("got allowed=%v want %v (reason=%q)", ok, c.allowed, reason)
			}
			if !ok && reason == "" {
				t.Fatal("denied without a reason")
			}
		})
	}
}

func TestRDViewEnforce(t *testing.T) {
	if ok, _ := rdViewEnforce(RemoteDesktopPolicy{ViewEnabled: true}); !ok {
		t.Fatal("view should be allowed when enabled")
	}
	if ok, reason := rdViewEnforce(RemoteDesktopPolicy{ViewEnabled: false}); ok || reason == "" {
		t.Fatalf("view should be denied with a reason when disabled (ok=%v reason=%q)", ok, reason)
	}
}

func TestRDDefaultPolicy(t *testing.T) {
	p := defaultRemoteDesktopPolicy()
	if !p.ViewEnabled {
		t.Error("view should default ON")
	}
	if p.ControlEnabled {
		t.Error("control must default OFF — input injection is opt-in")
	}
	if !p.AllowRemoteControl {
		t.Error("remote control should default ON so web/mobile works once control is enabled")
	}
}

func TestRDScalePoint(t *testing.T) {
	disp := ghost.Display{Index: 0, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true}
	cases := []struct {
		nx, ny     float64
		wantX, wantY int
	}{
		{0, 0, 0, 0},
		{1, 1, 1920, 1080},
		{0.5, 0.5, 960, 540},
		{-0.5, 2, 0, 1080}, // clamped
	}
	for _, c := range cases {
		x, y := rdScalePoint(c.nx, c.ny, disp)
		if x != c.wantX || y != c.wantY {
			t.Errorf("scale(%v,%v): got (%d,%d) want (%d,%d)", c.nx, c.ny, x, y, c.wantX, c.wantY)
		}
	}
}

func TestRDScalePointOffsetDisplay(t *testing.T) {
	// Secondary display offset to the right; normalized center should land at
	// the display's own center in virtual-desktop space.
	disp := ghost.Display{Index: 1, X: 1920, Y: 0, Width: 1280, Height: 800}
	x, y := rdScalePoint(0.5, 0.5, disp)
	if x != 1920+640 || y != 400 {
		t.Errorf("offset scale: got (%d,%d) want (%d,%d)", x, y, 1920+640, 400)
	}
}

func TestRDButton(t *testing.T) {
	if rdButton("right") != ghost.ButtonRight {
		t.Error("right")
	}
	if rdButton("middle") != ghost.ButtonMiddle {
		t.Error("middle")
	}
	if rdButton("") != ghost.ButtonLeft || rdButton("left") != ghost.ButtonLeft {
		t.Error("left default")
	}
}
