package main

// remotedesktop.go — "Remote Desktop" for your own machines, driven from the
// web dashboard and the mobile app. Unlike the GUI ghost (ops_ghost.go), which
// is the unattended Talos/blackbox use case gated behind the static
// `yaver serve --ghost` startup flag, Remote Desktop is the OWNER-driving-their-
// own-box case: you're signed into the same account, you open the device's
// screen from web/mobile, and you click/type on it.
//
// It reuses the cross-OS capture + input engine (desktop/agent/ghost) and the
// live MJPEG streamer (ghost_stream.go), but it is gated by a RUNTIME consent
// policy stored on the recorded machine — no agent restart required:
//
//   - View (MJPEG screen stream): allowed for the owner by default. The owner
//     can disable it (master kill-switch).
//   - Control (mouse/keyboard injection): OFF by default. Injecting input is
//     more sensitive than watching, so it must be explicitly enabled — from the
//     box itself, or remotely once the owner has flipped it on. Every remote
//     control session is audited and (optionally) notified on the desktop.
//
// Same-account auth still clears s.auth before any of this runs; this layer is
// the extra, owner-controlled gate on top of identity. Policy lives at
// ~/.yaver/remotedesktop/policy.json, LOCAL ONLY (never synced to Convex).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yaver-io/agent/ghost"
)

// RemoteDesktopPolicy is the owner's local control surface for Remote Desktop.
type RemoteDesktopPolicy struct {
	// ViewEnabled is the master switch for the screen stream. Default true —
	// your machines, your call — but the owner can kill it.
	ViewEnabled bool `json:"viewEnabled"`
	// ControlEnabled gates mouse/keyboard injection. Default FALSE: watching
	// is one thing, driving the box is another, so control is opt-in.
	ControlEnabled bool `json:"controlEnabled"`
	// AllowRemoteControl decides whether a NON-loopback caller (a phone over
	// the relay, the web dashboard) may inject input, vs control being
	// restricted to a process on the box itself. Default true so the feature
	// works from web/mobile once ControlEnabled is on.
	AllowRemoteControl bool `json:"allowRemoteControl"`
	// NotifyOnControl posts a desktop toast + push when a remote caller starts
	// driving the machine, so the person at the keyboard always knows.
	NotifyOnControl bool  `json:"notifyOnControl"`
	UpdatedAt       int64 `json:"updatedAt"`
}

func defaultRemoteDesktopPolicy() RemoteDesktopPolicy {
	return RemoteDesktopPolicy{
		ViewEnabled:        true,
		ControlEnabled:     false,
		AllowRemoteControl: true,
		NotifyOnControl:    true,
	}
}

var (
	remoteDesktopMu      sync.Mutex
	remoteDesktopBaseDir string
)

func remoteDesktopDir() (string, error) {
	remoteDesktopMu.Lock()
	defer remoteDesktopMu.Unlock()
	if remoteDesktopBaseDir != "" {
		return remoteDesktopBaseDir, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "remotedesktop")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	remoteDesktopBaseDir = p
	return p, nil
}

func remoteDesktopPolicyPath() (string, error) {
	base, err := remoteDesktopDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "policy.json"), nil
}

func loadRemoteDesktopPolicy() RemoteDesktopPolicy {
	p, err := remoteDesktopPolicyPath()
	if err != nil {
		return defaultRemoteDesktopPolicy()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return defaultRemoteDesktopPolicy()
	}
	pol := defaultRemoteDesktopPolicy()
	if err := json.Unmarshal(data, &pol); err != nil {
		return defaultRemoteDesktopPolicy()
	}
	return pol
}

func saveRemoteDesktopPolicy(pol RemoteDesktopPolicy) error {
	p, err := remoteDesktopPolicyPath()
	if err != nil {
		return err
	}
	pol.UpdatedAt = time.Now().UnixMilli()
	data, _ := json.MarshalIndent(pol, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// rdControlEnforce decides whether an input-injection request is allowed.
// Pure — unit-tested. `remote` is true for a non-loopback caller.
func rdControlEnforce(pol RemoteDesktopPolicy, remote bool) (bool, string) {
	if !pol.ControlEnabled {
		return false, "remote control is disabled on this machine (owner must enable Control in Remote Desktop settings)"
	}
	if remote && !pol.AllowRemoteControl {
		return false, "remote control from other devices is disabled on this machine (only a process on the box itself may drive it)"
	}
	return true, ""
}

// rdViewEnforce decides whether the screen stream may be served.
func rdViewEnforce(pol RemoteDesktopPolicy) (bool, string) {
	if !pol.ViewEnabled {
		return false, "screen view is disabled on this machine (owner turned Remote Desktop off)"
	}
	return true, ""
}

// rdScalePoint maps a normalized point (nx, ny in [0,1], fraction of the
// displayed frame) onto absolute screen coordinates for the given display.
// The frame is captured in pixels (which can be 2× on retina) but ghost.Input
// works in the display's logical coordinate space, so normalizing on the client
// and de-normalizing here against the display bounds is resolution-independent.
func rdScalePoint(nx, ny float64, disp ghost.Display) (int, int) {
	if nx < 0 {
		nx = 0
	}
	if nx > 1 {
		nx = 1
	}
	if ny < 0 {
		ny = 0
	}
	if ny > 1 {
		ny = 1
	}
	x := disp.X + int(nx*float64(disp.Width)+0.5)
	y := disp.Y + int(ny*float64(disp.Height)+0.5)
	return x, y
}

// --- audit trail (local) ---------------------------------------------------

type rdAuditEntry struct {
	At     int64  `json:"at"`
	Action string `json:"action"` // "control" | "policy" | "deny" | "view"
	Remote bool   `json:"remote"`
	Note   string `json:"note,omitempty"`
}

func appendRemoteDesktopAudit(e rdAuditEntry) {
	base, err := remoteDesktopDir()
	if err != nil {
		return
	}
	e.At = time.Now().UnixMilli()
	f, err := os.OpenFile(filepath.Join(base, "audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(e)
	f.Write(append(data, '\n'))
}
