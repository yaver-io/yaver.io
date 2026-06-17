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
	for _, name := range []string{"deploy-tv.sh", "deploy-android-tv.sh", "deploy-tvos.sh"} {
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
}
