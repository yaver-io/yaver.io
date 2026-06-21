package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProject(t *testing.T, appJSON string, deps ...string) string {
	t.Helper()
	dir := t.TempDir()
	if appJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(appJSON), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if len(deps) > 0 {
		dir = writePkgJSONInto(t, dir, deps...)
	}
	return dir
}

// writePkgJSONInto writes package.json into an EXISTING dir (so app.json +
// package.json coexist).
func writePkgJSONInto(t *testing.T, dir string, deps ...string) string {
	t.Helper()
	var b []byte
	b = append(b, []byte(`{"name":"x","dependencies":{`)...)
	for i, d := range deps {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, []byte(`"`+d+`":"*"`)...)
	}
	b = append(b, []byte(`}}`)...)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildStoreListingIdentity(t *testing.T) {
	appJSON := `{"expo":{"name":"Receipt Scanner","version":"1.2.3","ios":{"bundleIdentifier":"com.acme.receipts"},"android":{"package":"com.acme.receipts"}}}`
	dir := writeProject(t, appJSON, "expo-camera")
	l := BuildStoreListing(dir)

	if l.AppName != "Receipt Scanner" {
		t.Errorf("appName = %q", l.AppName)
	}
	if l.BundleID != "com.acme.receipts" || l.PackageName != "com.acme.receipts" {
		t.Errorf("ids wrong: %q / %q", l.BundleID, l.PackageName)
	}
	if l.Version != "1.2.3" {
		t.Errorf("version = %q", l.Version)
	}
	if l.Description == "" {
		t.Error("description draft should be non-empty")
	}
	// Camera is a capability but collects no PRIVACY data type by itself,
	// so it should appear in derivation, not necessarily in Privacy.
	if !strSliceHas(l.Derivation.DetectedCapabilities, "camera") {
		t.Error("camera should be in the derivation context")
	}
}

func TestDerivePrivacyTruthful(t *testing.T) {
	// Location capability + an analytics SDK + an ads SDK ⇒ three truthful
	// data-collection declarations, one flagged as tracking.
	appJSON := `{"expo":{"name":"X"}}`
	dir := writeProject(t, appJSON, "expo-location", "posthog-react-native", "react-native-google-mobile-ads")
	l := BuildStoreListing(dir)

	cats := map[string]DataCollection{}
	for _, d := range l.Privacy {
		cats[d.Category] = d
	}
	if _, ok := cats["Location"]; !ok {
		t.Error("expo-location should declare Location")
	}
	if _, ok := cats["UsageData"]; !ok {
		t.Error("posthog should declare UsageData/Analytics")
	}
	ids, ok := cats["Identifiers"]
	if !ok {
		t.Fatal("admob should declare Identifiers")
	}
	if !ids.UsedForTracking {
		t.Error("ad SDK identifiers must be flagged UsedForTracking")
	}
	if len(ids.Purposes) == 0 || ids.Purposes[0] != "Advertising" {
		t.Errorf("ad identifiers purpose should be Advertising, got %v", ids.Purposes)
	}
}

func TestStoreListingNoData(t *testing.T) {
	dir := writeProject(t, `{"expo":{"name":"Plain"}}`, "react", "expo")
	l := BuildStoreListing(dir)
	if len(l.Privacy) != 0 {
		t.Errorf("a plain app should collect no data, got %d entries", len(l.Privacy))
	}
	// Screenshot slots are always present (the asset generator fills them).
	if len(l.Screenshots) == 0 {
		t.Error("required screenshot slots should always be listed")
	}
}
