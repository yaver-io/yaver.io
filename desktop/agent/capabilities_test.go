package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapabilityCatalogueWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range capabilityCatalogue {
		if c.ID == "" || c.Title == "" {
			t.Errorf("capability %q missing id/title", c.ID)
		}
		if seen[c.ID] {
			t.Errorf("duplicate capability id %q", c.ID)
		}
		seen[c.ID] = true
		if len(c.Signals) == 0 {
			t.Errorf("capability %q has no detection signals", c.ID)
		}
		for k := range c.IOSPlistUsage {
			if !strings.HasPrefix(k, "NS") {
				t.Errorf("%q: plist usage key %q should start with NS", c.ID, k)
			}
		}
		for _, p := range c.AndroidPermissions {
			if !strings.HasPrefix(p, "android.permission.") {
				t.Errorf("%q: android perm %q malformed", c.ID, p)
			}
		}
	}
	// A capability must declare SOMETHING actionable (else it's noise).
	for _, c := range capabilityCatalogue {
		if len(c.IOSPlistUsage) == 0 && len(c.IOSEntitlements) == 0 &&
			len(c.AndroidPermissions) == 0 && len(c.ConsoleForms) == 0 {
			t.Errorf("capability %q declares no requirements", c.ID)
		}
	}
}

func writePkgJSON(t *testing.T, deps ...string) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString(`{"name":"x","dependencies":{`)
	for i, d := range deps {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + d + `":"*"`)
	}
	b.WriteString(`}}`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildManifestPlanDetectsFromDeps(t *testing.T) {
	dir := writePkgJSON(t, "expo-camera", "expo-location", "@react-native-google-signin/google-signin", "react")
	plan := buildManifestPlan(dir)

	detected := map[string]bool{}
	for _, f := range plan.Findings {
		if f.Detected {
			detected[f.ID] = true
		}
	}
	for _, want := range []string{"camera", "location-when-in-use", "signin-google"} {
		if !detected[want] {
			t.Errorf("expected to detect %q", want)
		}
	}
	if _, ok := plan.IOSPlistUsage["NSCameraUsageDescription"]; !ok {
		t.Error("camera should add NSCameraUsageDescription")
	}
	if !strSliceHas(plan.AndroidPermissions, "android.permission.ACCESS_FINE_LOCATION") {
		t.Error("location should add ACCESS_FINE_LOCATION")
	}
	if !strSliceHas(plan.NeedsUsageStrings, "NSCameraUsageDescription") {
		t.Error("camera usage string should be flagged as needing a real value")
	}
	// signin-google routes to a console form (OAuth clients).
	if len(plan.ConsoleForms) == 0 {
		t.Error("signin-google should contribute a console form")
	}
}

func TestBuildManifestPlanEmptyProject(t *testing.T) {
	dir := t.TempDir() // no package.json
	plan := buildManifestPlan(dir)
	for _, f := range plan.Findings {
		if f.Detected {
			t.Errorf("no deps ⇒ nothing detected, but %q was", f.ID)
		}
	}
	if len(plan.IOSPlistUsage) != 0 || len(plan.AndroidPermissions) != 0 {
		t.Error("empty project should yield an empty plan")
	}
}

func TestCapabilityByID(t *testing.T) {
	if _, ok := capabilityByID("camera"); !ok {
		t.Error("camera should resolve")
	}
	if _, ok := capabilityByID("nope"); ok {
		t.Error("unknown id should not resolve")
	}
}
