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
	if ok, _ := rdViewEnforce(RemoteDesktopPolicy{ViewEnabled: true, ViewConsentSet: true}); !ok {
		t.Fatal("view should be allowed when enabled")
	}
	if ok, reason := rdViewEnforce(RemoteDesktopPolicy{ViewEnabled: false, ViewConsentSet: true}); ok || reason == "" {
		t.Fatalf("view should be denied with a reason when disabled (ok=%v reason=%q)", ok, reason)
	}
	if ok, reason := rdViewEnforce(RemoteDesktopPolicy{ViewEnabled: true}); ok || reason == "" {
		t.Fatalf("view without local consent should be denied (ok=%v reason=%q)", ok, reason)
	}
}

func TestRDDefaultPolicy(t *testing.T) {
	p := defaultRemoteDesktopPolicy()
	if p.ViewEnabled || p.ViewConsentSet {
		t.Error("view and consent must default OFF")
	}
	if p.ControlEnabled {
		t.Error("control must default OFF — input injection is opt-in")
	}
	if p.ControlConsentSet {
		t.Error("control consent must default OFF — a remote caller must not be able to self-grant")
	}
	if !p.AllowRemoteControl {
		t.Error("remote control should default ON so web/mobile works once control is enabled")
	}
}

// TestRDControlPolicyUpdateEnforce is the guard for the 2026-09-29 self-grant
// hole: before it, handleRemoteDesktopPolicy wrote ControlEnabled with no
// locality check, so an authenticated phone could enable control and
// immediately drive the box. This proves the operation, not the inventory.
func TestRDControlPolicyUpdateEnforce(t *testing.T) {
	on := true
	off := false
	unset := RemoteDesktopPolicy{}
	if ok, _ := rdControlPolicyUpdateEnforce(unset, false, &on); !ok {
		t.Fatal("a local caller must be able to establish first control consent")
	}
	if ok, reason := rdControlPolicyUpdateEnforce(unset, true, &on); ok || reason == "" {
		t.Fatalf("a remote caller must NOT be able to self-grant control (ok=%v reason=%q)", ok, reason)
	}
	if ok, _ := rdControlPolicyUpdateEnforce(unset, true, &off); !ok {
		t.Fatal("a remote caller must be able to fail closed")
	}
	consented := RemoteDesktopPolicy{ControlConsentSet: true}
	if ok, _ := rdControlPolicyUpdateEnforce(consented, true, &on); !ok {
		t.Fatal("remote toggle should work after local control consent is recorded")
	}
	// A nil request (caller only touched other fields) must never be blocked.
	if ok, _ := rdControlPolicyUpdateEnforce(unset, true, nil); !ok {
		t.Fatal("a request that does not mention control must pass through")
	}
}

func TestRDViewPolicyUpdateEnforce(t *testing.T) {
	on := true
	off := false
	unset := RemoteDesktopPolicy{}
	if ok, _ := rdViewPolicyUpdateEnforce(unset, false, &on); !ok {
		t.Fatal("a local caller must be able to establish first consent")
	}
	if ok, reason := rdViewPolicyUpdateEnforce(unset, true, &on); ok || reason == "" {
		t.Fatalf("a remote caller must not establish first consent (ok=%v reason=%q)", ok, reason)
	}
	if ok, _ := rdViewPolicyUpdateEnforce(unset, true, &off); !ok {
		t.Fatal("a remote caller must be able to fail closed")
	}
	consented := RemoteDesktopPolicy{ViewConsentSet: true}
	if ok, _ := rdViewPolicyUpdateEnforce(consented, true, &on); !ok {
		t.Fatal("remote toggle should work after local consent is recorded")
	}
}

func TestRDScalePoint(t *testing.T) {
	disp := ghost.Display{Index: 0, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true}
	cases := []struct {
		nx, ny       float64
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
