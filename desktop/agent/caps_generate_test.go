package main

import "testing"

func TestMergeManifestIntoAppConfigAddsMissing(t *testing.T) {
	cfg := map[string]interface{}{
		"expo": map[string]interface{}{
			"name": "X",
			"ios": map[string]interface{}{
				// User already customised the camera string — must be preserved.
				"infoPlist": map[string]interface{}{"NSCameraUsageDescription": "Custom camera reason."},
			},
			"android": map[string]interface{}{
				"permissions": []interface{}{"android.permission.INTERNET"},
			},
		},
	}
	plan := ManifestPlan{
		IOSPlistUsage: map[string]string{
			"NSCameraUsageDescription":            "default (should NOT overwrite)",
			"NSLocationWhenInUseUsageDescription": "We use location to show nearby results.",
		},
		AndroidPermissions: []string{"android.permission.CAMERA", "android.permission.INTERNET"},
	}

	updated, changes := mergeManifestIntoAppConfig(cfg, plan)

	ios := updated["expo"].(map[string]interface{})["ios"].(map[string]interface{})
	plist := ios["infoPlist"].(map[string]interface{})
	if plist["NSCameraUsageDescription"] != "Custom camera reason." {
		t.Error("existing custom usage string must be preserved, not overwritten")
	}
	if plist["NSLocationWhenInUseUsageDescription"] == nil {
		t.Error("missing usage string should be added")
	}

	perms := asStringSlice(updated["expo"].(map[string]interface{})["android"].(map[string]interface{})["permissions"])
	if !strSliceHas(perms, "android.permission.CAMERA") {
		t.Error("missing android permission should be added")
	}
	// INTERNET present once (union, no duplicate).
	count := 0
	for _, p := range perms {
		if p == "android.permission.INTERNET" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("INTERNET should appear once, got %d", count)
	}
	if len(changes) == 0 {
		t.Error("expected changes")
	}
}

func TestMergeManifestIdempotent(t *testing.T) {
	plan := ManifestPlan{
		IOSPlistUsage:      map[string]string{"NSCameraUsageDescription": "cam"},
		AndroidPermissions: []string{"android.permission.CAMERA"},
	}
	cfg := map[string]interface{}{}
	cfg, c1 := mergeManifestIntoAppConfig(cfg, plan)
	if len(c1) == 0 {
		t.Fatal("first merge should change something")
	}
	_, c2 := mergeManifestIntoAppConfig(cfg, plan)
	if len(c2) != 0 {
		t.Errorf("second merge should be a no-op, got %v", c2)
	}
}
