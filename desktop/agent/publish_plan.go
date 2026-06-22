package main

// publish_plan.go — "just tell me what to run next." Given the publish
// readiness verdict, emit the EXACT ordered list of commands that take a
// normie from where they are to shippable. Pure (nextSteps) + unit-tested;
// the CLI prints the readiness banner followed by the numbered plan.

import (
	"encoding/json"
	"fmt"
)

// PlanStep is one remediation action.
type PlanStep struct {
	Title string `json:"title"`
	Cmd   string `json:"cmd,omitempty"`
}

// hasFailingCheck reports whether a named readiness check failed.
func hasFailingCheck(r PublishReadiness, name string) bool {
	for _, c := range r.Checks {
		if c.Name == name {
			return !c.OK
		}
	}
	return false
}

// nextSteps maps the readiness state → an ordered remediation plan. Order
// follows the real dependency chain: identity → permissions → signing/auth →
// assets → copy → push. Returns an empty slice when already shippable.
func nextSteps(r PublishReadiness, project string) []PlanStep {
	if project == "" {
		project = "<app>"
	}
	pf := " --project " + project
	var steps []PlanStep

	if hasFailingCheck(r, "listing-identity") {
		steps = append(steps, PlanStep{
			Title: "Set your app name + bundle/package id in app.json",
			Cmd:   "edit app.json: expo.name, expo.ios.bundleIdentifier, expo.android.package",
		})
	}
	if hasFailingCheck(r, "permissions") {
		steps = append(steps, PlanStep{
			Title: "Declare the permissions your code needs",
			Cmd:   "yaver caps generate --write",
		})
	}
	if hasFailingCheck(r, "store-auth-apple") {
		steps = append(steps, PlanStep{
			Title: "Connect App Store Connect + generate the iOS signing cert",
			Cmd:   "yaver stores apple-asc-key   then   yaver keys init" + pf + " --platform ios",
		})
	}
	if hasFailingCheck(r, "store-auth-google") {
		steps = append(steps, PlanStep{
			Title: "Create your Android keystore + connect Play",
			Cmd:   "yaver keys init" + pf + " --platform android   then   yaver stores google-service-account",
		})
	}
	if hasFailingCheck(r, "assets") {
		steps = append(steps, PlanStep{
			Title: "Capture store screenshots + feature graphic",
			Cmd:   "yaver assets capture --ios-sim <udid> --android-serial <serial>",
		})
	}
	// Copy + push always come last (best done once the above are green).
	steps = append(steps,
		PlanStep{Title: "Draft your listing copy (AI, grounded on your features)", Cmd: "yaver listing draft"},
		PlanStep{Title: "Push the listing to the stores", Cmd: "yaver listing push --store apple --live   (and --store google)"},
	)
	return steps
}

func runListingPlan(args []string) {
	path, project := ".", ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--json":
			jsonOut = true
		}
	}
	r := buildPublishReadiness(path, "")
	steps := nextSteps(r, project)
	if jsonOut {
		b, _ := json.MarshalIndent(map[string]interface{}{"ready": r.Ready, "blockers": r.Blockers, "steps": steps}, "", "  ")
		fmt.Println(string(b))
		return
	}
	if r.Ready {
		fmt.Println("✓ Ready to submit. Final steps:")
	} else {
		fmt.Printf("Your path to shipping (%d blocker%s):\n", len(r.Blockers), plural2(len(r.Blockers)))
	}
	fmt.Println()
	for i, s := range steps {
		fmt.Printf("  %d. %s\n", i+1, s.Title)
		if s.Cmd != "" {
			fmt.Printf("       %s\n", s.Cmd)
		}
	}
}

func plural2(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
