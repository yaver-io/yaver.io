package main

// screenlog_autostart.go — opt-in, reboot-durable "keep recording" intent.
//
// The recording loop itself is already fully LOCAL: once started it only
// writes frames to ~/.yaver/screenlog/ and never needs auth, the relay, or
// the internet. The missing piece for a set-and-forget black box (e.g.
// monitoring a family member's PC) is RESUMING it automatically after a
// reboot/crash. This adds a tiny persisted marker:
//
//   ~/.yaver/screenlog/autostart.json  = { enabled, title, config }
//
// When the agent starts, resumeScreenlogIfEnabled() reads it and — if the
// owner opted in AND the master kill-switch is on — starts screenlog with
// the saved config. Crucially this runs INDEPENDENTLY of auth: even if the
// box is signed out or offline, if the last state was "record", it records.
//
// Set via `yaver screenlog start --persist` (or `yaver screenlog autostart
// on`); cleared by `yaver screenlog stop` (unless --keep-autostart) or
// `yaver screenlog autostart off`.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type screenlogAutostart struct {
	Enabled   bool            `json:"enabled"`
	Title     string          `json:"title,omitempty"`
	Config    ScreenlogConfig `json:"config"`
	UpdatedAt int64           `json:"updatedAt"`
}

func screenlogAutostartPath() (string, error) {
	base, err := screenlogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "autostart.json"), nil
}

func loadScreenlogAutostart() (screenlogAutostart, bool) {
	var a screenlogAutostart
	p, err := screenlogAutostartPath()
	if err != nil {
		return a, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return a, false
	}
	if json.Unmarshal(data, &a) != nil {
		return a, false
	}
	return a, true
}

func saveScreenlogAutostart(a screenlogAutostart) error {
	p, err := screenlogAutostartPath()
	if err != nil {
		return err
	}
	a.UpdatedAt = time.Now().UnixMilli()
	data, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// setScreenlogAutostart records (or clears) the opt-in intent + config.
func setScreenlogAutostart(enabled bool, cfg ScreenlogConfig, title string) error {
	return saveScreenlogAutostart(screenlogAutostart{Enabled: enabled, Title: title, Config: cfg})
}

// resumeScreenlogIfEnabled is invoked from `yaver serve` startup. It auto-
// starts screenlog when the persisted intent says so — with NO auth /
// internet dependency, so a rebooted-and-offline box keeps logging. Honors
// only the local master kill-switch (ScreenlogPolicy.Enabled).
func resumeScreenlogIfEnabled() {
	a, ok := loadScreenlogAutostart()
	if !ok || !a.Enabled {
		return
	}
	if pol := loadScreenlogPolicy(); !pol.Enabled {
		fmt.Fprintln(os.Stderr, "[screenlog] autostart skipped — recording disabled by policy kill-switch")
		return
	}
	// Let the agent settle (and, on WSL, the Windows session come up) before
	// the first capture, then start. Local only — never blocks on auth.
	time.Sleep(8 * time.Second)
	screenlogMu.Lock()
	already := screenlogActive != nil
	screenlogMu.Unlock()
	if already {
		return
	}
	if _, err := startScreenlog(a.Config, a.Title); err != nil {
		fmt.Fprintf(os.Stderr, "[screenlog] autostart failed: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stderr, "[screenlog] auto-resumed recording (persisted intent, auth-independent)")
}
