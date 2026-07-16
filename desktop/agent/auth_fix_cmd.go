// auth_fix_cmd.go — `yaver auth fix <device>`: sign a box back in from here.
//
// The generic counterpart to `yaver primary auth`. That verb is welded to
// the primary slot (it reads userSettings.primaryDeviceId), so every OTHER
// box — a second Mac, a Pi, a friend's builder — had no Yaver-level way
// back and needed a hand-typed `yaver auth send <passkey> <url>`, which in
// turn needed the user to fetch a rotating passkey and know the box's URL.
//
// Ordering here is deliberate. HTTP first: it's the cheaper path and works
// without SSH access at all. SSH second: it's the path that survives the
// auth loss itself, because an unauthenticated agent's relay registration
// is rejected. Browser last: `yaver auth --headless` costs a human, so it's
// the fallback rather than the default — this machine's session is already
// proof of identity, and pushing it is strictly less work than making
// someone sign in again.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func runAuthFix(args []string) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver auth fix <alias|deviceId|name>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Signs a box that dropped to bootstrap/needs-auth back in by pushing")
		fmt.Fprintln(os.Stderr, "this machine's session to it. Tries HTTP (LAN/mesh/relay), then SSH,")
		fmt.Fprintln(os.Stderr, "then falls back to a browser sign-in on the box itself.")
		os.Exit(1)
	}
	hint := strings.TrimSpace(args[0])

	cfg, target, err := findOwnedDeviceForHint(hint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth fix: %v\n", err)
		os.Exit(1)
	}
	name := firstNonEmpty(strings.TrimSpace(target.Alias), strings.TrimSpace(target.Name), target.DeviceID)

	probe := probeOwnedDeviceReauth(cfg, target)
	fmt.Printf("%s: %s\n", name, describeDeviceReauthProbe(probe))
	if probe.State == "healthy" || probe.State == "ready-to-connect" {
		fmt.Println("Nothing to fix — already signed in.")
		return
	}

	// 1. Bootstrap box, HTTP still reaches it: drive its pair window.
	if probe.Reachable && probe.State == "bootstrap" {
		fmt.Printf("→ pushing this machine's session over %s…\n", firstNonEmpty(probe.Transport, "direct"))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		transport, err := recoverDeviceAuthViaPairWindow(ctx, cfg, target)
		cancel()
		if err == nil {
			fmt.Printf("  token accepted over %s\n", transport)
			if finishAuthFix(cfg, target, name) {
				return
			}
		} else {
			fmt.Printf("  pair-window push failed: %v\n", err)
		}
	}

	// 2. Auth-expired box: it still holds a device identity, so it can
	//    verify our host token and pull a fresh session itself.
	if probe.Reachable && probe.State == "yaver-auth-expired" {
		fmt.Printf("→ trying direct recovery over %s…\n", firstNonEmpty(probe.Transport, "direct"))
		_, status, raw, reqErr := doOwnedDeviceRecover(cfg, target, "direct", "")
		if reqErr == nil && status < 300 {
			if finishAuthFix(cfg, target, name) {
				return
			}
		} else if reqErr != nil {
			fmt.Printf("  direct recovery failed: %v\n", reqErr)
		} else {
			fmt.Printf("  direct recovery failed: %s\n", extractRemoteError(status, raw))
		}
	}

	// 3. SSH — the transport auth loss can't take away.
	fmt.Println("→ trying SSH recovery (pushing this machine's session)…")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	transport, sshErr := recoverDeviceAuthOverSSH(ctx, cfg, target)
	cancel()
	if sshErr == nil {
		fmt.Printf("  token pushed over %s\n", transport)
		if finishAuthFix(cfg, target, name) {
			return
		}
	} else {
		fmt.Printf("  SSH recovery failed: %v\n", sshErr)
	}

	// 4. Browser on the box. Costs a human, so it goes last.
	fmt.Println("→ falling back to a browser sign-in on the box itself…")
	if err := runRemoteHeadlessYaverAuthOverSSH(sshTargetHint(target)); err != nil {
		fmt.Fprintf(os.Stderr, "auth fix: could not reach %s over SSH either: %v\n", name, err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Last resort — from any machine signed in as you:")
		fmt.Fprintf(os.Stderr, "  PK=$(curl -s http://<ip>:18080/info | python3 -c \"import sys,json;print(json.load(sys.stdin)['bootstrapPasskey'])\")\n")
		fmt.Fprintln(os.Stderr, "  yaver auth send \"$PK\" http://<ip>:18080")
		os.Exit(1)
	}
	if !finishAuthFix(cfg, target, name) {
		os.Exit(1)
	}
}

// finishAuthFix waits for the box to actually report signed-in. A 200 from
// the recovery endpoint only means the token was ACCEPTED — the box still
// has to re-register with Convex and re-pin its relay tunnel before any
// other surface will see it as healthy. Claiming success at the 200 is how
// you get "it says it worked but the phone still can't see it".
func finishAuthFix(cfg *Config, target *DeviceInfo, name string) bool {
	fmt.Println("  waiting for the box to come back…")
	probe, err := waitForDeviceAuthHealthy(cfg, target, 90*time.Second)
	if err != nil {
		fmt.Printf("  not confirmed yet: %v\n", err)
		return false
	}
	fmt.Printf("✓ %s is signed in (%s)\n", name, describeDeviceReauthProbe(probe))
	return true
}
