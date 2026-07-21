package main

// ops_webrtc_doctor.go — verb "webrtc_doctor": probe (and optionally heal) the
// toolchain WebRTC RN/native simulator streaming needs. Every check here is a
// real incident from the 2026-07-21 bring-up, encoded where the agent looks so
// nobody rediscovers it:
//
//   - watchman missing → Metro slow-polls file changes; Fast Refresh drags.
//   - idb missing → iOS-sim finger taps silently never work (no simctl tap verb).
//   - flutter missing → Flutter guest apps (e-mobile) can't build/stream.
//   - scrcpy missing → no fast Android/redroid stream.
//   - adb missing → no Android control at all.
//   - `simctl io screenshot` slow → a DEGRADED CoreSimulator (measured 17s/frame
//     on the mini, fresh sim too); streaming is impossible until the box is
//     rebooted. This is the "probe the real capability, not the proxy" case: the
//     tool is present and answers, but the OPERATION is broken — only timing it
//     reveals the truth.
//
// action=check reports; action=heal installs the unambiguous, idempotent ones
// (brew/pip) and never guesses at destructive fixes (a reboot is recommended,
// never performed).

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type opsWebRTCDoctorPayload struct {
	Action string `json:"action,omitempty"` // check | heal. default check.
	// SkipSimctlTiming avoids the ~1–18s screenshot probe when the caller only
	// wants a fast tool inventory.
	SkipSimctlTiming bool `json:"skipSimctlTiming,omitempty"`
}

type depCheck struct {
	Name     string `json:"name"`
	Present  bool   `json:"present"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"`      // the install command
	Healable bool   `json:"healable,omitempty"` // heal can install it unattended
	Critical bool   `json:"critical,omitempty"` // blocks streaming if absent/broken
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "webrtc_doctor",
		Description: "Probe (and optionally heal) the simulator-streaming toolchain: watchman, idb, flutter, scrcpy, adb, xcodebuild, and a live degraded-simctl check. action=heal installs the safe ones; a broken CoreSimulator is reported with a reboot remedy, never rebooted automatically.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":           map[string]interface{}{"type": "string", "enum": []string{"check", "heal"}, "default": "check"},
				"skipSimctlTiming": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsWebRTCDoctorHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsWebRTCDoctorHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsWebRTCDoctorPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	checks := probeWebRTCDeps(ctx, p.SkipSimctlTiming)

	if strings.TrimSpace(strings.ToLower(p.Action)) == "heal" {
		healed := []string{}
		for i := range checks {
			if !checks[i].Present && checks[i].Healable {
				if err := healDep(ctx, checks[i].Name); err == nil {
					checks[i].Present = true
					checks[i].Detail = "installed by webrtc_doctor heal"
					healed = append(healed, checks[i].Name)
				} else {
					checks[i].Detail = "heal failed: " + err.Error()
				}
			}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"action": "heal", "healed": healed, "checks": checks,
			"streamingReady": streamingReady(checks),
		}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"action": "check", "checks": checks,
		"streamingReady":     streamingReady(checks),
		"remediationSummary": remediationSummary(checks),
	}}
}

// probeWebRTCDeps runs the whole inventory. Pure enough to reason about; the
// timing probe is the one that catches a degraded box.
func probeWebRTCDeps(ctx context.Context, skipSimctlTiming bool) []depCheck {
	onPath := func(bin string) bool { _, err := exec.LookPath(bin); return err == nil }
	checks := []depCheck{}

	// watchman — Metro Fast Refresh speed (missing = slow polling).
	checks = append(checks, depCheck{
		Name: "watchman", Present: onPath("watchman"), Healable: true, Critical: false,
		Fix:    "brew install watchman",
		Detail: "Metro file-watch for Fast Refresh; without it Metro slow-polls and edits lag",
	})
	// idb — iOS simulator finger taps / gestures (no simctl tap verb exists).
	checks = append(checks, depCheck{
		Name: "idb", Present: onPath("idb"), Healable: true, Critical: false,
		Fix:    "brew tap facebook/fb && brew install idb-companion && pip3 install fb-idb",
		Detail: "iOS-sim tap/swipe injection; without it interactive taps never reach the guest app",
	})
	if runtime.GOOS == "darwin" {
		checks = append(checks, depCheck{
			Name: "flutter", Present: onPath("flutter"), Healable: true, Critical: false,
			Fix:    "brew install --cask flutter",
			Detail: "builds Flutter guest apps for the sim/emulator (e.g. e-mobile)",
		})
		checks = append(checks, depCheck{
			Name: "xcodebuild", Present: onPath("xcodebuild"), Healable: false, Critical: true,
			Fix:    "install Xcode from the App Store",
			Detail: "builds iOS guest apps for the simulator",
		})
	}
	// scrcpy + adb — Android/redroid streaming + control.
	checks = append(checks, depCheck{
		Name: "scrcpy", Present: onPath("scrcpy"), Healable: true, Critical: false,
		Fix:    "brew install scrcpy",
		Detail: "fast H.264 Android/redroid mirror + control",
	})
	checks = append(checks, depCheck{
		Name: "adb", Present: onPath("adb"), Healable: false, Critical: false,
		Fix:    "brew install --cask android-platform-tools",
		Detail: "Android emulator/redroid install + input",
	})

	// The real-capability probe: is simctl screenshot actually usable, or is the
	// CoreSimulator degraded? Only timing it tells the truth.
	if runtime.GOOS == "darwin" && !skipSimctlTiming {
		checks = append(checks, probeSimctlHealth(ctx))
	}
	return checks
}

// probeSimctlHealth times a screenshot against the first booted sim (or reports
// none booted). A healthy sim screenshots in well under a second; the mini was
// measured at ~17s — a degraded CoreSimulator that needs a reboot.
func probeSimctlHealth(ctx context.Context) depCheck {
	c := depCheck{Name: "simctl-capture", Present: false, Critical: true, Healable: false,
		Fix: "reboot the box to reset CoreSimulator (a degraded simctl io can't be fixed by restarting the service)"}
	tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	booted, err := exec.CommandContext(tctx, "xcrun", "simctl", "list", "devices", "booted").Output()
	if err != nil {
		c.Detail = "could not list booted sims: " + err.Error()
		return c
	}
	udid := firstBootedUDID(string(booted))
	if udid == "" {
		c.Detail = "no booted simulator to probe (boot one, then re-run)"
		c.Critical = false
		return c
	}
	start := time.Now()
	shot := exec.CommandContext(tctx, "xcrun", "simctl", "io", udid, "screenshot", "/tmp/.yaver-simhealth.png")
	err = shot.Run()
	elapsed := time.Since(start)
	if err != nil {
		c.Detail = fmt.Sprintf("screenshot failed after %.1fs: %v", elapsed.Seconds(), err)
		return c
	}
	// Threshold: a healthy sim is sub-second; anything over ~3s is degraded and
	// makes real-time streaming impossible.
	if elapsed > 3*time.Second {
		c.Detail = fmt.Sprintf("simctl screenshot took %.1fs — CoreSimulator is DEGRADED (healthy is <1s). Streaming is impossible until the box is rebooted.", elapsed.Seconds())
		return c
	}
	c.Present = true
	c.Detail = fmt.Sprintf("simctl screenshot healthy (%.2fs)", elapsed.Seconds())
	return c
}

func firstBootedUDID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "("); i >= 0 {
			if j := strings.Index(line[i+1:], ")"); j >= 0 {
				id := strings.TrimSpace(line[i+1 : i+1+j])
				if len(id) >= 32 && strings.Contains(id, "-") {
					return id
				}
			}
		}
	}
	return ""
}

func healDep(ctx context.Context, name string) error {
	switch name {
	case "watchman":
		return runHeal(ctx, "brew", "install", "watchman")
	case "scrcpy":
		return runHeal(ctx, "brew", "install", "scrcpy")
	case "flutter":
		return runHeal(ctx, "brew", "install", "--cask", "flutter")
	case "idb":
		// idb is two-step; do the companion (the tap/swipe engine). The python
		// client (fb-idb) is left to the operator since pip envs vary.
		return runHeal(ctx, "brew", "install", "idb-companion")
	}
	return fmt.Errorf("no unattended heal for %q", name)
}

func runHeal(ctx context.Context, name string, args ...string) error {
	hctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(hctx, name, args...).CombinedOutput(); err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), tail)
	}
	return nil
}

// streamingReady is true only when the CRITICAL deps are satisfied — including a
// healthy simctl. A missing watchman/idb degrades UX but doesn't block a stream.
func streamingReady(checks []depCheck) bool {
	for _, c := range checks {
		if c.Critical && !c.Present {
			return false
		}
	}
	return true
}

func remediationSummary(checks []depCheck) []string {
	out := []string{}
	for _, c := range checks {
		if !c.Present {
			sev := "warn"
			if c.Critical {
				sev = "BLOCKER"
			}
			out = append(out, fmt.Sprintf("[%s] %s — %s (fix: %s)", sev, c.Name, c.Detail, c.Fix))
		}
	}
	return out
}
