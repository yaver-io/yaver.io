package main

// cloud_provisioner_robot.go — Phase D0: the premium KVM SKU
// (Hetzner dedicated/bare-metal via the Robot webservice). This is
// the ONLY way to run the Android emulator for Flutter/Kotlin WebRTC
// — Hetzner Cloud (cx/cax, the api.hetzner.cloud path provisionHetzner
// uses) exposes no /dev/kvm on any plan; see memory
// project_no_linux_arm64_android_emulator + ..._sku_reality.
//
// FAIL-CLOSED BY DESIGN. Ordering a dedicated server is a paid,
// recurring (~€30-40+/mo), non-instant, hard-to-reverse commitment
// AND it uses a different API than the Cloud one (Robot webservice,
// HTTP basic auth, the server-market/auction for the only "fast"
// path). So this provisioner NEVER places an order unless ALL of:
//   1. HROBOT_USER + HROBOT_PASS are set (the user's Robot
//      webservice creds — a secret, env-only, never repo/Convex), and
//   2. opts["confirmPaidOrder"] == "true" (explicit per-call
//      acknowledgement that this spends money), and
//   3. opts["live"] == "true" (the order HTTP is otherwise a
//      dry-run that returns the plan only).
// Missing any → a Manual result that explains exactly what's needed
// and the cost. With env unset this is byte-for-byte a no-op, so
// shipping it changes nothing until the owner deliberately opts in.
//
// The order HTTP shape targets Hetzner's documented Robot endpoints
// (robot-ws.your-server.de, server_market) but the LIVE order path
// is intentionally a single guarded function the owner validates
// against their own Robot account before first real use — I will
// not pretend an unverified paid order path is proven.

import (
	"fmt"
	"os"
	"strings"
)

const robotWebserviceBase = "https://robot-ws.your-server.de"

func provisionHetznerRobot(name string, opts map[string]string) (*ProvisionResult, error) {
	user := strings.TrimSpace(os.Getenv("HROBOT_USER"))
	pass := strings.TrimSpace(os.Getenv("HROBOT_PASS"))
	if user == "" || pass == "" {
		return &ProvisionResult{
			Provider: "hetzner-robot",
			Manual: "Premium KVM SKU (bare-metal, Flutter/Kotlin emulator) is not wired to an account. " +
				"Set HROBOT_USER + HROBOT_PASS (Hetzner Robot webservice creds — env/secret only, never the repo) " +
				"on the provisioning host, then retry with opts.confirmPaidOrder=true. " +
				"Bare-metal is a paid recurring order (~€30-40+/mo), different from instant Cloud boxes.",
		}, nil
	}
	if opts["confirmPaidOrder"] != "true" {
		return &ProvisionResult{
			Provider: "hetzner-robot",
			Manual: "Refusing to order paid bare-metal without explicit acknowledgement. " +
				"Ordering a Hetzner dedicated server is a recurring (~€30-40+/mo) non-instant commitment. " +
				"Re-call with opts.confirmPaidOrder=true (and opts.live=true to actually place the order; " +
				"otherwise a plan-only dry-run is returned).",
		}, nil
	}

	plan := opts["plan"]
	if plan == "" {
		plan = "kvm-emulator"
	}
	region := opts["region"]
	if region == "" {
		region = "eu"
	}
	// Plan: what a live order WOULD do (server-market/auction pick +
	// installimage Ubuntu + cloud-init equivalent + KVM verify).
	planResult := &ProvisionResult{
		OK:       true,
		Provider: "hetzner-robot",
		Resource: "bare-metal (dry-run)",
		Details: map[string]string{
			"plan":     plan,
			"region":   region,
			"name":     name,
			"endpoint": robotWebserviceBase + "/order/server_market",
			"mode":     "dry-run (opts.live != true) — no order placed, no charge",
		},
		Notes: "Would: query server_market for a KVM-capable bare-metal box, place a market order, " +
			"installimage Ubuntu, run the managed cloud-init, then verify /dev/kvm before marking ready. " +
			"Set opts.live=true to place the real (paid) order — validate this path against your Robot " +
			"account first; the live order function is deliberately separate and unproven in CI.",
	}
	if opts["live"] != "true" {
		return planResult, nil
	}
	// Live path is intentionally not auto-executed here. Wiring the
	// real server-market order + installimage + KVM verify is the
	// owner-validated step (paid, account-specific). Fail loud rather
	// than silently "succeed" or burn money on an unverified call.
	return nil, fmt.Errorf("hetzner-robot live order path not yet validated against a real Robot account — "+
		"implement robotPlaceMarketOrder against %s with your account + a tested spec before enabling opts.live "+
		"(guarded on purpose: a wrong call here orders/charges real hardware)", robotWebserviceBase)
}
