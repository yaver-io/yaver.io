package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformDeployPlanForTVTargets(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"deploy-tv.sh", "deploy-android-tv.sh", "deploy-tvos.sh", "deploy-wear-os.sh", "deploy-watchos.sh", "deploy-testflight.sh", "deploy-playstore.sh", "deploy-carplay.sh"} {
		if err := os.WriteFile(filepath.Join(scripts, name), []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := platformDeployPlanFor(root, "tv", true, []string{"--skip-tvos"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "tv" || plan.Script != "scripts/deploy-tv.sh" {
		t.Fatalf("unexpected tv plan: %+v", plan)
	}
	if len(plan.Args) != 2 || plan.Args[0] != "--skip-tvos" || plan.Args[1] != "--upload" {
		t.Fatalf("unexpected tv args: %+v", plan.Args)
	}

	plan, err = platformDeployPlanFor(root, "leanback", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "android-tv" || plan.Script != "scripts/deploy-android-tv.sh" || len(plan.Args) != 0 {
		t.Fatalf("unexpected android-tv alias plan: %+v", plan)
	}

	plan, err = platformDeployPlanFor(root, "appletv", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "tvos" || plan.Script != "scripts/deploy-tvos.sh" || len(plan.Args) != 1 || plan.Args[0] != "--upload" {
		t.Fatalf("unexpected tvos alias plan: %+v", plan)
	}

	plan, err = platformDeployPlanFor(root, "android-watch", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "wear-os" || plan.Script != "scripts/deploy-wear-os.sh" || len(plan.Args) != 1 || plan.Args[0] != "--upload" {
		t.Fatalf("unexpected wear-os alias plan: %+v", plan)
	}

	plan, err = platformDeployPlanFor(root, "apple-watch", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "watchos" || plan.Script != "scripts/deploy-watchos.sh" || len(plan.Args) != 0 {
		t.Fatalf("unexpected watchos alias plan: %+v", plan)
	}

	plan, err = platformDeployPlanFor(root, "testflight", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "ios" || plan.Script != "scripts/deploy-testflight.sh" || len(plan.Args) != 1 || plan.Args[0] != "--upload" {
		t.Fatalf("unexpected ios/testflight plan: %+v", plan)
	}

	plan, err = platformDeployPlanFor(root, "android-auto", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "android" || plan.Script != "scripts/deploy-playstore.sh" || len(plan.Args) != 1 || plan.Args[0] != "--upload" {
		t.Fatalf("unexpected android-auto plan: %+v", plan)
	}

	plan, err = platformDeployPlanForValidation(root, "carplay", false, nil, platformValidationConfig{Driver: "webdriver"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "carplay" || plan.Script != "scripts/deploy-carplay.sh" || plan.Validation == nil || plan.Validation.Driver != "selenium" || plan.Validation.Scope != "full" || plan.Validation.Viewport != "ipad11-landscape" {
		t.Fatalf("unexpected carplay selenium validation plan: %+v", plan)
	}
}

func TestMCPMobilePlatformDeployDryRunIncludesSeleniumValidation(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "deploy-playstore.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := mcpMobilePlatformDeploy(root, "android", true, true, 0, platformValidationConfig{
		Driver:   "selenium",
		Scope:    "screen:login",
		MaxFlows: 2,
	})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("dry run failed: %+v", res)
	}
	plan, ok := res["plan"].(platformDeployPlan)
	if !ok {
		t.Fatalf("plan has unexpected type: %#v", res["plan"])
	}
	if plan.Validation == nil || plan.Validation.Driver != "selenium" || plan.Validation.Scope != "screen:login" || plan.Validation.Viewport != "pixel7" || plan.Validation.MaxFlows != 2 {
		t.Fatalf("unexpected selenium validation plan: %+v", plan.Validation)
	}
}

func TestMobilePlatformMatrixAdvertisesSeleniumReleaseValidation(t *testing.T) {
	report := mobilePlatformMatrix(t.TempDir())
	want := map[string]bool{
		"android-mobile": false,
		"android-tv":     false,
		"android-auto":   false,
		"android-wear":   false,
		"ios-mobile":     false,
		"tvos":           false,
		"watchos":        false,
		"carplay":        false,
	}
	for _, surface := range report.Surfaces {
		if _, ok := want[surface.ID]; !ok {
			continue
		}
		for _, v := range surface.Validation {
			if v == "autotest-selenium" {
				want[surface.ID] = true
			}
		}
	}
	for id, ok := range want {
		if !ok {
			t.Fatalf("%s does not advertise autotest-selenium validation: %+v", id, report.Surfaces)
		}
	}
}
