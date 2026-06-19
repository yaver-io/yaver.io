package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWizardGeneratesStoreSubmissionGates drives the wizard with the
// full permission + policy posture enabled and asserts that every
// file the stores actually look for shows up in the output tree.
// This is the regression guard for the 2026 submission gates documented
// in MOBILE_APP_PERMS_POLICY_FULLSTACK.md.
func TestWizardGeneratesStoreSubmissionGates(t *testing.T) {
	t.Setenv("YAVER_DISABLE_WIZARD_AUTOINIT", "1")

	tmpDir, err := os.MkdirTemp("", "wizard-policy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	sess, _ := StartWizard()
	answers := map[string]string{
		"app_name":                                "Demo",
		"slug":                                    "demo",
		"description":                             "demo app",
		"tagline":                                 "",
		"app_template":                            "saas-dashboard",
		"supported_languages":                     "English",
		"domain":                                  "demo.test",
		"primary_color":                           "#000000",
		"secondary_color":                         "#111111",
		"accent_color":                            "#222222",
		"surface_color":                           "#333333",
		"tone":                                    "dark",
		"include_web":                             "true",
		"include_mobile":                          "true",
		"include_backend":                         "true",
		"include_landing":                         "true",
		"web_framework":                           "nextjs",
		"web_host":                                "cloudflare",
		"backend":                                 "sqlite",
		"mobile_stack":                            "expo-rn",
		"mobile_nav_style":                        "bottom-tabs",
		"mobile_nav_count":                        "4",
		"mobile_nav_labels":                       "",
		"design_source":                           "prompt-only",
		"design_reference_url":                    "",
		"design_notes":                            "",
		"oauth_apple":                             "true",
		"oauth_google":                            "true",
		"oauth_microsoft":                         "false",
		"oauth_email":                             "true",
		"mobile_permission_camera":                "true",
		"mobile_permission_camera_usage":          "Scan QR codes.",
		"mobile_permission_photos":                "true",
		"mobile_permission_photos_usage":          "Attach screenshots.",
		"mobile_permission_photos_save":           "true",
		"mobile_permission_photos_save_usage":     "Save generated images.",
		"mobile_permission_microphone":            "false",
		"mobile_permission_microphone_usage":      "",
		"mobile_permission_location":              "true",
		"mobile_permission_location_usage":        "Show nearby devices.",
		"mobile_permission_location_always":       "false",
		"mobile_permission_location_always_usage": "",
		"mobile_permission_bluetooth":             "false",
		"mobile_permission_bluetooth_usage":       "",
		"mobile_permission_notifications":         "true",
		"mobile_permission_notifications_usage":   "Build alerts.",
		"mobile_permission_tracking":              "true",
		"mobile_permission_tracking_usage":        "Measure ad performance.",
		"mobile_account_deletion":                 "true",
		"mobile_data_collection":                  "tracking",
		"audience_children":                       "false",
		"payments":                                "stripe",
		"ios_bundle_id":                           "com.demo.app",
		"android_package":                         "com.demo.app",
		"apple_team_id":                           "",
		"play_service_account":                    "",
		"cloudflare_zone":                         "demo.test",
		"legal_entity_name":                       "Demo Inc",
		"legal_support_email":                     "hi@demo.test",
		"legal_jurisdiction":                      "Delaware, United States",
		"legal_privacy_notes":                     "",
		"git_provider":                            "none",
		"git_visibility":                          "",
		"git_org":                                 "",
		"git_repo_name":                           "",
		"confirm":                                 "true",
	}
	wizardMu.Lock()
	for k, v := range answers {
		sess.Answers[k] = v
	}
	sess.Done = true
	wizardMu.Unlock()

	res, err := GenerateProject(sess.ID, tmpDir)
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}

	expectedFiles := []string{
		"apps/mobile/app.json",
		"apps/mobile/ios/PrivacyInfo.xcprivacy",
		"apps/mobile/screens/DeleteAccount.tsx",
		"apps/web/app/account/delete/page.tsx",
		"apps/landing/account-delete.html",
		"legal/privacy.md",
		"legal/terms.md",
		"legal/app-review.md",
		"legal/play-data-safety.md",
		"legal/app-privacy-nutrition.md",
	}
	for _, rel := range expectedFiles {
		p := filepath.Join(res.Directory, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected generated file %q missing: %v", rel, err)
		}
	}

	readAll := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(res.Directory, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	appJSON := readAll("apps/mobile/app.json")
	for _, needle := range []string{
		"ITSAppUsesNonExemptEncryption",
		"NSCameraUsageDescription",
		"NSPhotoLibraryUsageDescription",
		"NSPhotoLibraryAddUsageDescription",
		"NSLocationWhenInUseUsageDescription",
		"NSUserTrackingUsageDescription",
		"privacyManifests",
		"NSPrivacyTracking",
		`"com.google.android.gms.permission.AD_ID"`,
	} {
		if !strings.Contains(appJSON, needle) {
			t.Errorf("apps/mobile/app.json missing %q", needle)
		}
	}

	manifest := readAll("apps/mobile/ios/PrivacyInfo.xcprivacy")
	for _, needle := range []string{
		"NSPrivacyTracking",
		"NSPrivacyCollectedDataTypes",
		"NSPrivacyAccessedAPITypes",
		"NSPrivacyCollectedDataTypeEmailAddress",
		"NSPrivacyCollectedDataTypeAdvertisingData",
	} {
		if !strings.Contains(manifest, needle) {
			t.Errorf("PrivacyInfo.xcprivacy missing %q", needle)
		}
	}

	privacy := readAll("legal/privacy.md")
	for _, needle := range []string{
		"Account deletion",
		"GDPR",
		"CCPA",
		"App Tracking Transparency",
	} {
		if !strings.Contains(privacy, needle) {
			t.Errorf("legal/privacy.md missing %q", needle)
		}
	}

	review := readAll("legal/app-review.md")
	for _, needle := range []string{
		"Privacy Manifest",
		"ITSAppUsesNonExemptEncryption",
		"Account deletion",
		"Data safety",
	} {
		if !strings.Contains(review, needle) {
			t.Errorf("legal/app-review.md missing %q", needle)
		}
	}

	playDS := readAll("legal/play-data-safety.md")
	for _, needle := range []string{
		"Data collection summary",
		"Account deletion",
		"/account/delete",
	} {
		if !strings.Contains(playDS, needle) {
			t.Errorf("legal/play-data-safety.md missing %q", needle)
		}
	}
}

// TestWizardOmitsGatesWhenMobileOff confirms we don't generate
// mobile-specific store gates for pure web / landing projects.
func TestWizardOmitsGatesWhenMobileOff(t *testing.T) {
	t.Setenv("YAVER_DISABLE_WIZARD_AUTOINIT", "1")

	tmpDir, err := os.MkdirTemp("", "wizard-no-mobile-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	sess, _ := StartWizard()
	wizardMu.Lock()
	for k, v := range map[string]string{
		"app_name":          "Web",
		"slug":              "web",
		"description":       "web only",
		"include_web":       "true",
		"include_mobile":    "false",
		"include_backend":   "false",
		"include_landing":   "false",
		"web_framework":     "nextjs",
		"web_host":          "cloudflare",
		"oauth_email":       "true",
		"legal_entity_name": "Web Inc",
		"git_provider":      "none",
		"confirm":           "true",
	} {
		sess.Answers[k] = v
	}
	sess.Done = true
	wizardMu.Unlock()

	res, err := GenerateProject(sess.ID, tmpDir)
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}

	for _, rel := range []string{
		"apps/mobile/ios/PrivacyInfo.xcprivacy",
		"apps/mobile/screens/DeleteAccount.tsx",
		"legal/play-data-safety.md",
		"legal/app-privacy-nutrition.md",
	} {
		if _, err := os.Stat(filepath.Join(res.Directory, rel)); err == nil {
			t.Errorf("unexpected mobile gate generated for web-only project: %s", rel)
		}
	}
}
