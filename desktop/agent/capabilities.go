package main

// capabilities.go — infer a mobile app's REQUIRED permissions/capabilities
// from its code (dependencies), then emit the exact iOS Info.plist keys +
// entitlements, Android manifest permissions, and the store-Console
// declarations each one triggers — with a good default usage string the
// AI can refine ("vibe"). This is the #1 cause of store rejection +
// launch crashes for normies, and it's DERIVABLE: which SDK you import
// determines what you must declare.
//
// Design mirrors setup_guide.go: a data-driven catalogue + a scanner +
// an aggregated plan, surfaced by CLI (`yaver caps`), the agent
// (GET /capabilities), and (next) the deploy doctor + the store-privacy
// form filler. Generation targets app.config / Expo config plugins — NOT
// raw Info.plist/AndroidManifest, which `expo prebuild --clean` overwrites.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// capabilitySpec is one device capability and everything declaring it
// requires across iOS + Android + the store consoles.
type capabilitySpec struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Dependency names (package.json) that imply this capability.
	Signals []string `json:"signals"`
	// iOS Info.plist usage-description keys → a sensible default string.
	// Apple REJECTS vague strings, so defaults explain the "why"; the AI
	// rewrites them in the app's own words.
	IOSPlistUsage map[string]string `json:"iosPlistUsage,omitempty"`
	// iOS entitlements (must also be enabled on the App ID — see `yaver keys`).
	IOSEntitlements []string `json:"iosEntitlements,omitempty"`
	// Android <uses-permission> values.
	AndroidPermissions []string `json:"androidPermissions,omitempty"`
	// Store-Console declarations only a human can submit (route + draft).
	ConsoleForms []string `json:"consoleForms,omitempty"`
	DocURL       string   `json:"docUrl,omitempty"`
	Notes        string   `json:"notes,omitempty"`
}

// capabilityCatalogue — the master map. Keep entries focused on the common
// Expo/React-Native SDKs a normie actually uses.
var capabilityCatalogue = []capabilitySpec{
	{
		ID: "camera", Title: "Camera",
		Signals:            []string{"expo-camera", "react-native-vision-camera"},
		IOSPlistUsage:      map[string]string{"NSCameraUsageDescription": "This app uses the camera to let you take photos and scan content."},
		AndroidPermissions: []string{"android.permission.CAMERA"},
		DocURL:             "https://developer.apple.com/documentation/bundleresources/information_property_list/nscamerausagedescription",
	},
	{
		ID: "microphone", Title: "Microphone",
		Signals:            []string{"expo-av", "@react-native-voice/voice", "react-native-audio-recorder-player"},
		IOSPlistUsage:      map[string]string{"NSMicrophoneUsageDescription": "This app uses the microphone to record audio."},
		AndroidPermissions: []string{"android.permission.RECORD_AUDIO"},
	},
	{
		ID: "location-when-in-use", Title: "Location (while using)",
		Signals:            []string{"expo-location", "@react-native-community/geolocation", "react-native-maps"},
		IOSPlistUsage:      map[string]string{"NSLocationWhenInUseUsageDescription": "This app uses your location to show nearby results."},
		AndroidPermissions: []string{"android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION"},
	},
	{
		ID: "location-always", Title: "Location (background)",
		Signals:            []string{"expo-location"},
		IOSPlistUsage:      map[string]string{"NSLocationAlwaysAndWhenInUseUsageDescription": "This app uses your location in the background to provide continuous updates."},
		AndroidPermissions: []string{"android.permission.ACCESS_BACKGROUND_LOCATION"},
		ConsoleForms:       []string{"Google Play: Background location declaration form", "Apple: explain background location in App Review notes"},
		Notes:              "Background location triggers heightened store review — only enable if truly needed.",
	},
	{
		ID: "photos", Title: "Photo library",
		Signals: []string{"expo-image-picker", "expo-media-library", "react-native-image-picker"},
		IOSPlistUsage: map[string]string{
			"NSPhotoLibraryUsageDescription":    "This app accesses your photo library so you can choose images.",
			"NSPhotoLibraryAddUsageDescription": "This app saves images to your photo library.",
		},
		AndroidPermissions: []string{"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO"},
	},
	{
		ID: "contacts", Title: "Contacts",
		Signals:            []string{"expo-contacts", "react-native-contacts"},
		IOSPlistUsage:      map[string]string{"NSContactsUsageDescription": "This app accesses your contacts to help you connect with people you know."},
		AndroidPermissions: []string{"android.permission.READ_CONTACTS"},
	},
	{
		ID: "calendar", Title: "Calendar",
		Signals:            []string{"expo-calendar"},
		IOSPlistUsage:      map[string]string{"NSCalendarsUsageDescription": "This app accesses your calendar to manage events."},
		AndroidPermissions: []string{"android.permission.READ_CALENDAR", "android.permission.WRITE_CALENDAR"},
	},
	{
		ID: "faceid", Title: "Face ID / biometrics",
		Signals:            []string{"expo-local-authentication", "react-native-biometrics"},
		IOSPlistUsage:      map[string]string{"NSFaceIDUsageDescription": "This app uses Face ID to securely authenticate you."},
		AndroidPermissions: []string{"android.permission.USE_BIOMETRIC"},
	},
	{
		ID: "bluetooth", Title: "Bluetooth",
		Signals:            []string{"react-native-ble-plx", "react-native-ble-manager"},
		IOSPlistUsage:      map[string]string{"NSBluetoothAlwaysUsageDescription": "This app uses Bluetooth to connect to nearby devices."},
		AndroidPermissions: []string{"android.permission.BLUETOOTH_SCAN", "android.permission.BLUETOOTH_CONNECT"},
	},
	{
		ID: "health", Title: "Health data",
		Signals: []string{"react-native-health", "@kingstinct/react-native-healthkit"},
		IOSPlistUsage: map[string]string{
			"NSHealthShareUsageDescription":  "This app reads your health data to show your activity.",
			"NSHealthUpdateUsageDescription": "This app writes health data you log.",
		},
		IOSEntitlements: []string{"com.apple.developer.healthkit"},
		ConsoleForms:    []string{"Apple: Health data must be declared in App Privacy"},
	},
	{
		ID: "push", Title: "Push notifications",
		Signals:            []string{"expo-notifications", "@react-native-firebase/messaging", "react-native-notifications"},
		IOSPlistUsage:      map[string]string{}, // no usage string; entitlement + capability
		IOSEntitlements:    []string{"aps-environment"},
		AndroidPermissions: []string{"android.permission.POST_NOTIFICATIONS"},
		Notes:              "POST_NOTIFICATIONS is required on Android 13+. iOS push needs the APNs key/cert (see `yaver keys`).",
	},
	{
		ID: "tracking", Title: "App tracking (IDFA)",
		Signals:       []string{"expo-tracking-transparency", "react-native-tracking-transparency", "react-native-idfa-aaid"},
		IOSPlistUsage: map[string]string{"NSUserTrackingUsageDescription": "This identifier will be used to deliver personalized ads to you."},
		ConsoleForms:  []string{"Apple: App Privacy 'Tracking' section", "Google Play: Data Safety 'Data shared' / advertising ID"},
		Notes:         "ATT prompt required before any cross-app tracking on iOS.",
	},
	{
		ID: "speech", Title: "Speech recognition",
		Signals:            []string{"@react-native-voice/voice", "expo-speech-recognition"},
		IOSPlistUsage:      map[string]string{"NSSpeechRecognitionUsageDescription": "This app uses speech recognition to transcribe what you say."},
		AndroidPermissions: []string{"android.permission.RECORD_AUDIO"},
	},
	{
		ID: "signin-apple", Title: "Sign in with Apple",
		Signals:         []string{"expo-apple-authentication", "@invertase/react-native-apple-authentication"},
		IOSEntitlements: []string{"com.apple.developer.applesignin"},
		ConsoleForms:    []string{"Apple: required (Guideline 4.8) if you offer any other social login"},
		DocURL:          "https://developer.apple.com/sign-in-with-apple/",
	},
	{
		ID: "signin-google", Title: "Google Sign-In",
		Signals:      []string{"@react-native-google-signin/google-signin"},
		ConsoleForms: []string{"Google Cloud: create OAuth client IDs (iOS bundle, Android package+SHA-1, Web)"},
		Notes:        "Adds an iOS URL scheme (reversed client id) + needs OAuth clients — see `yaver stores google-signin`.",
	},
	{
		ID: "iap", Title: "In-app purchases",
		Signals:      []string{"react-native-iap", "expo-in-app-purchases", "react-native-purchases"},
		ConsoleForms: []string{"Apple: sign the Paid Apps agreement + tax/banking", "Google Play: set up a Payments/merchant profile"},
		Notes:        "No runtime permission; Play Billing handles the rest. Products created via `yaver stores apple-iap`/`google-iap`.",
	},
	{
		ID: "deep-links", Title: "Deep links / universal links",
		Signals:         []string{"expo-linking", "expo-router", "@react-navigation/native"},
		IOSEntitlements: []string{"com.apple.developer.associated-domains"},
		Notes:           "iOS: CFBundleURLTypes (custom scheme) + Associated Domains (applinks:) for universal links/passkeys. Android: intent-filter with autoVerify + assetlinks.json.",
	},
}

func capabilityByID(id string) (*capabilitySpec, bool) {
	for i := range capabilityCatalogue {
		if capabilityCatalogue[i].ID == id {
			return &capabilityCatalogue[i], true
		}
	}
	return nil, false
}

// ── Scanner ──────────────────────────────────────────────────────────

// scanProjectDeps reads a project's package.json and returns the set of
// declared dependency names (deps + devDeps + peerDeps). Best-effort: an
// unreadable/parse-failed package.json returns an empty set, not an error.
func scanProjectDeps(projectDir string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		return out
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return out
	}
	for _, m := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.PeerDependencies, pkg.OptionalDependencies} {
		for name := range m {
			out[name] = true
		}
	}
	return out
}

// capabilityFinding is a catalogue entry plus whether it was detected and
// which signals matched.
type capabilityFinding struct {
	capabilitySpec
	Detected       bool     `json:"detected"`
	MatchedSignals []string `json:"matchedSignals,omitempty"`
}

// ManifestPlan aggregates everything the DETECTED capabilities require —
// the deployable truth the generator + doctor + privacy-form filler consume.
type ManifestPlan struct {
	Findings           []capabilityFinding `json:"capabilities"`
	IOSPlistUsage      map[string]string   `json:"iosPlistUsage"`
	IOSEntitlements    []string            `json:"iosEntitlements"`
	AndroidPermissions []string            `json:"androidPermissions"`
	ConsoleForms       []string            `json:"consoleForms"`
	// Plist usage keys that need a real, review-passing string (default
	// provided; the AI should rewrite in the app's own words).
	NeedsUsageStrings []string `json:"needsUsageStrings"`
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// buildManifestPlan scans a project and aggregates the required manifest.
func buildManifestPlan(projectDir string) ManifestPlan {
	deps := scanProjectDeps(projectDir)
	plan := ManifestPlan{IOSPlistUsage: map[string]string{}}
	var ent, perms, forms, needs []string

	for i := range capabilityCatalogue {
		spec := capabilityCatalogue[i]
		var matched []string
		for _, sig := range spec.Signals {
			if deps[sig] {
				matched = append(matched, sig)
			}
		}
		detected := len(matched) > 0
		plan.Findings = append(plan.Findings, capabilityFinding{
			capabilitySpec: spec, Detected: detected, MatchedSignals: matched,
		})
		if !detected {
			continue
		}
		for k, v := range spec.IOSPlistUsage {
			plan.IOSPlistUsage[k] = v
			needs = append(needs, k)
		}
		ent = append(ent, spec.IOSEntitlements...)
		perms = append(perms, spec.AndroidPermissions...)
		forms = append(forms, spec.ConsoleForms...)
	}
	plan.IOSEntitlements = dedupSorted(ent)
	plan.AndroidPermissions = dedupSorted(perms)
	plan.ConsoleForms = dedupSorted(forms)
	plan.NeedsUsageStrings = dedupSorted(needs)
	return plan
}

// ── CLI ──────────────────────────────────────────────────────────────

func runCaps(args []string) {
	jsonOut := false
	path := "."
	var sub string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("Usage: yaver caps [scan|list] [--path DIR] [--json]")
			fmt.Println("  scan   infer required permissions/capabilities from the project's deps (default)")
			fmt.Println("  list   show the full capability catalogue")
			return
		default:
			if sub == "" {
				sub = a
			}
		}
	}

	if sub == "list" {
		if jsonOut {
			b, _ := json.MarshalIndent(capabilityCatalogue, "", "  ")
			fmt.Println(string(b))
			return
		}
		fmt.Println("Capability catalogue (code signal → what it requires):")
		for _, c := range capabilityCatalogue {
			fmt.Printf("  %-22s %s\n      signals: %s\n", c.ID, c.Title, strings.Join(c.Signals, ", "))
		}
		return
	}

	plan := buildManifestPlan(path)
	if jsonOut {
		b, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(b))
		return
	}
	printManifestPlan(plan)
}

func printManifestPlan(plan ManifestPlan) {
	detected := 0
	for _, f := range plan.Findings {
		if f.Detected {
			detected++
		}
	}
	fmt.Printf("Detected %d capabilit%s from your dependencies:\n\n", detected, capPlural(detected, "y", "ies"))
	for _, f := range plan.Findings {
		if !f.Detected {
			continue
		}
		fmt.Printf("  • %s  (%s)\n", f.Title, strings.Join(f.MatchedSignals, ", "))
	}
	if detected == 0 {
		fmt.Println("  (none — no permission-bearing SDKs found in package.json)")
		return
	}
	if len(plan.IOSPlistUsage) > 0 {
		fmt.Println("\niOS Info.plist usage strings (rewrite in your app's own words):")
		for _, k := range plan.NeedsUsageStrings {
			fmt.Printf("  %s\n    → %s\n", k, plan.IOSPlistUsage[k])
		}
	}
	if len(plan.IOSEntitlements) > 0 {
		fmt.Printf("\niOS entitlements (also enable on the App ID): %s\n", strings.Join(plan.IOSEntitlements, ", "))
	}
	if len(plan.AndroidPermissions) > 0 {
		fmt.Println("\nAndroid permissions:")
		for _, p := range plan.AndroidPermissions {
			fmt.Printf("  %s\n", p)
		}
	}
	if len(plan.ConsoleForms) > 0 {
		fmt.Println("\nStore-console declarations (a human must submit — Yaver drafts + routes):")
		for _, c := range plan.ConsoleForms {
			fmt.Printf("  ◆ %s\n", c)
		}
	}
	fmt.Println("\nNext: generate config (app.config / Expo plugins) — keeps these regen-safe.")
}

func capPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ── Agent HTTP ───────────────────────────────────────────────────────

// handleCapabilities serves the manifest plan for the web/mobile permissions
// panel. ?path= overrides the scanned dir (default: server working dir).
func (s *HTTPServer) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	writeJSON(w, http.StatusOK, buildManifestPlan(path))
}
