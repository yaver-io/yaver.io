package main

import (
	"fmt"
	"net/http"
)

// Cost guardrails for the phone-first deploy path. Two concerns:
//
// 1. Byte budget. Phone-originated bundles should be tiny — without
//    IncludeData a project is ~1.5 KB, with IncludeData it's whatever the
//    user's SQLite file is. We cap both export AND receive to avoid a
//    runaway upload pinning Hetzner traffic bills. Default 50 MB; Caddy's
//    128 MB request_body cap stays the outer ceiling.
// 2. Cost transparency. Mobile asks for a cost hint before opening the
//    deploy confirm so the user sees "free" / "$0 on CF free tier" /
//    "~$0.001/MB on Yaver Cloud over quota" up-front. No metering yet —
//    that's a follow-up — just advisory text.

// PhoneDeployBudget is the hard byte-cap on export + receive. Used by both
// ends so the mobile client can show "X MB / 50 MB used" and refuse before
// hitting the wire when the bundle is too big.
const PhoneDeployBudgetBytes int64 = 50 * 1024 * 1024 // 50 MB

// PhoneDeployCostHint is a human-readable advisory for one deploy target.
// Sent to the mobile UI so the user sees what a tap will cost BEFORE they
// commit. Never metered from Convex — pure local advisory.
type PhoneDeployCostHint struct {
	TargetKind string `json:"targetKind"` // "this-device" / "dev-hw" / "yaver-cloud" / "cloudflare-workers" / "custom"
	Label      string `json:"label"`      // human name
	Free       string `json:"free"`       // "free" / "free up to 500 MB D1" / "included in $19/mo plan"
	Overage    string `json:"overage"`    // "$0.045/GB egress after 10 GB" / "n/a"
	Budget     string `json:"budget"`     // "50 MB bundle cap" etc.
	Advice     string `json:"advice"`     // one-line user-facing reassurance
}

// PhoneDeployCostHints returns the advisory list for every deploy target
// the mobile UI surfaces. Edit this when pricing changes — the mobile
// fetches it instead of hardcoding strings so updates don't need a
// TestFlight push.
func PhoneDeployCostHints() []PhoneDeployCostHint {
	return []PhoneDeployCostHint{
		{
			TargetKind: "this-device",
			Label:      "This device",
			Free:       "free — stays on your own agent",
			Overage:    "n/a",
			Budget:     "no upload happens",
			Advice:     "Nothing leaves your Mac. Zero cost.",
		},
		{
			TargetKind: "dev-hw",
			Label:      "Your Dev Machine (another Yaver agent you own)",
			Free:       "free — your own hardware",
			Overage:    "whatever your ISP / host charges",
			Budget:     fmt.Sprintf("%d MB bundle cap (configurable)", PhoneDeployBudgetBytes/(1024*1024)),
			Advice:     "Goes over the relay you're already paying for (or free Yaver relay). No per-deploy fee.",
		},
		{
			TargetKind: "yaver-cloud",
			Label:      "Yaver Cloud",
			Free:       "included in your plan (CPU $49/mo or GPU $449/mo)",
			Overage:    "n/a — plans are flat, no metering",
			Budget:     fmt.Sprintf("%d MB bundle cap (configurable)", PhoneDeployBudgetBytes/(1024*1024)),
			Advice:     "Flat subscription. Deploy as often as you want under the bundle cap.",
		},
		{
			TargetKind: "cloudflare-workers",
			Label:      "Cloudflare Workers",
			Free:       "paid plan from $5/mo; free tier has tighter script/storage limits",
			Overage:    "paid: $5/mo includes 10M req, then $0.30/M",
			Budget:     "Workers script ≤ 10 MB on paid plan; D1/R2 billed by Cloudflare",
			Advice:     "Good fit for small web deploys. Check script size and storage before shipping large assets or data.",
		},
		{
			TargetKind: "custom",
			Label:      "Custom base URL",
			Free:       "depends on your host",
			Overage:    "depends on your host",
			Budget:     fmt.Sprintf("%d MB bundle cap enforced by Yaver", PhoneDeployBudgetBytes/(1024*1024)),
			Advice:     "You chose the host, you own the bill.",
		},
	}
}

// EnforcePhoneDeployBudget returns an error if the bundle exceeds the cap.
// Called from ExportPhoneProjectWithOptions (producer side) and from
// handlePhoneReceive (consumer side) so both ends refuse oversize payloads
// cleanly. Caller can override the default via opts.MaxBundleBytes > 0.
func EnforcePhoneDeployBudget(actual int64, overrideCap int64) error {
	cap := PhoneDeployBudgetBytes
	if overrideCap > 0 {
		cap = overrideCap
	}
	if actual > cap {
		return fmt.Errorf(
			"bundle is %.1f MB but cap is %.1f MB — enable --include-data only when you need runtime rows, otherwise the portable manifest is tiny. Override with opts.MaxBundleBytes.",
			float64(actual)/(1024*1024),
			float64(cap)/(1024*1024),
		)
	}
	return nil
}

// handlePhoneCostHint serves /phone/projects/cost-hint.
func (s *HTTPServer) handlePhoneCostHint(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hints":           PhoneDeployCostHints(),
		"bundleCapBytes":  PhoneDeployBudgetBytes,
		"bundleCapMB":     PhoneDeployBudgetBytes / (1024 * 1024),
	})
}
