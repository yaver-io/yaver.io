package studio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const specialUseManifest = `<?xml version="1.0"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
  <uses-permission android:name="android.permission.INTERNET"/>
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE"/>
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE_SPECIAL_USE"/>
  <uses-permission android:name="android.permission.POST_NOTIFICATIONS"/>
  <application>
    <service android:name="io.example.sandbox.SandboxService"
        android:exported="false"
        android:foregroundServiceType="specialUse">
      <property android:name="android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE"
          android:value="on_device_coding_agent"/>
    </service>
  </application>
</manifest>`

func TestNormalizePermission(t *testing.T) {
	cases := map[string]string{
		"FOREGROUND_SERVICE_SPECIAL_USE":                  "android.permission.FOREGROUND_SERVICE_SPECIAL_USE",
		"android.permission.FOREGROUND_SERVICE_DATA_SYNC": "android.permission.FOREGROUND_SERVICE_DATA_SYNC",
		"  CAMERA  ": "android.permission.CAMERA",
	}
	for in, want := range cases {
		if got := NormalizePermission(in); got != want {
			t.Errorf("NormalizePermission(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFGSTypeForPermission(t *testing.T) {
	if got := FGSTypeForPermission("FOREGROUND_SERVICE_SPECIAL_USE"); got != "specialUse" {
		t.Errorf("specialUse mapping = %q", got)
	}
	if got := FGSTypeForPermission("android.permission.CAMERA"); got != "" {
		t.Errorf("non-FGS permission should map to empty, got %q", got)
	}
}

func TestAnalyzeSpecialUse(t *testing.T) {
	f, err := analyzeAndroidManifestReader(strings.NewReader(specialUseManifest), "FOREGROUND_SERVICE_SPECIAL_USE")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !f.Declared {
		t.Error("expected permission Declared=true")
	}
	if f.FGSType != "specialUse" {
		t.Errorf("FGSType = %q, want specialUse", f.FGSType)
	}
	if f.Service == nil {
		t.Fatal("expected a bound service")
	}
	if f.Service.Name != "io.example.sandbox.SandboxService" {
		t.Errorf("service name = %q", f.Service.Name)
	}
	if f.Service.Exported {
		t.Error("service should be exported=false")
	}
	if f.SpecialUseSubtype != "on_device_coding_agent" {
		t.Errorf("subtype = %q, want on_device_coding_agent", f.SpecialUseSubtype)
	}
	if len(f.AllFGSPermissions) != 2 { // FOREGROUND_SERVICE + FOREGROUND_SERVICE_SPECIAL_USE
		t.Errorf("AllFGSPermissions = %v, want 2", f.AllFGSPermissions)
	}
}

func TestAnalyzeMissingService(t *testing.T) {
	// Permission declared but no <service> with the matching type → reviewer risk.
	m := `<manifest xmlns:android="http://schemas.android.com/apk/res/android">
	  <uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC"/>
	  <application/></manifest>`
	f, err := analyzeAndroidManifestReader(strings.NewReader(m), "FOREGROUND_SERVICE_DATA_SYNC")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !f.Declared {
		t.Error("expected Declared=true")
	}
	if f.Service != nil {
		t.Error("expected no bound service")
	}
	j := GenerateJustification(f, "Test", "")
	foundWarn := false
	for _, w := range j.Warnings {
		if strings.Contains(w, "no <service> declares") {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Errorf("expected a missing-service warning, got %v", j.Warnings)
	}
}

func TestGenerateJustificationSpecialUse(t *testing.T) {
	f, _ := analyzeAndroidManifestReader(strings.NewReader(specialUseManifest), "FOREGROUND_SERVICE_SPECIAL_USE")
	j := GenerateJustification(f, "Yaver", "an on-device coding agent and a local Linux environment")
	if len(j.Warnings) != 0 {
		t.Errorf("well-formed manifest should produce no warnings, got %v", j.Warnings)
	}
	if j.TaskOther == "" || j.Description == "" {
		t.Fatal("expected non-empty prose")
	}
	for _, must := range []string{"cannot be paused", "not covered by any of the standard", "on_device_coding_agent", "coding agent and a local Linux"} {
		if !strings.Contains(j.Description, must) {
			t.Errorf("description missing %q\n---\n%s", must, j.Description)
		}
	}
	if len(j.ShotList) != 4 {
		t.Errorf("expected 4 shots, got %d", len(j.ShotList))
	}
	md := j.Markdown(f.Permission)
	if !strings.Contains(md, "Demo video shot-list") || !strings.Contains(md, "→ Other") {
		t.Errorf("markdown missing sections:\n%s", md)
	}
}

// TestAnalyzeRealYaverManifest runs against the repo's actual manifest when
// present (skipped elsewhere), proving end-to-end extraction on the real app.
func TestAnalyzeRealYaverManifest(t *testing.T) {
	path := filepath.Join("..", "..", "..", "mobile", "android", "app", "src", "main", "AndroidManifest.xml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("real manifest not present: %v", err)
	}
	f, err := AnalyzeAndroidManifest(path, "FOREGROUND_SERVICE_SPECIAL_USE")
	if err != nil {
		t.Fatalf("analyze real manifest: %v", err)
	}
	if !f.Declared {
		t.Error("real manifest should declare FGS_SPECIAL_USE")
	}
	if f.Service == nil || !strings.Contains(f.Service.Name, "SandboxService") {
		t.Errorf("expected SandboxService, got %+v", f.Service)
	}
	if f.SpecialUseSubtype != "on_device_coding_agent" {
		t.Errorf("subtype = %q", f.SpecialUseSubtype)
	}
	// Trigger discovery should find a caller in the repo (best-effort).
	repoRoot := filepath.Join("..", "..", "..")
	if hit := FindTrigger(repoRoot, f); hit == "" {
		t.Log("FindTrigger found no caller (advisory, not fatal)")
	} else {
		t.Logf("trigger located at %s", hit)
	}
}
