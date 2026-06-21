package main

// store_listing.go — ONE canonical store-listing data model derived from the
// app's code, projected onto BOTH Apple App Store Connect and Google Play.
//
// Apple and Google ask ~80% the same things in different shapes, so we model
// it once (StoreListing) and let projectors push each store (ASC API / Play
// Developer API) or, for Console-only forms, draft + route. The high-value,
// HARD-to-do-by-hand part is the PRIVACY declaration (Apple App Privacy +
// Google Data Safety): it must MATCH what the code actually does. We derive
// it from the capability graph (capabilities.go) + an SDK→data-collection
// catalogue, so the draft is truthful and auditable rather than hand-guessed.
//
// This module is deterministic + testable: identity from app config, privacy
// from code. The free-text marketing fields (description/keywords/what's-new)
// are left as drafts + a DerivationContext the AI layer grounds on — no LLM
// call in the core. Projectors (ASC/Play) + the redroid asset generator + the
// assisted UI build on this.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DataCollection is one declared data type the app collects — the shared
// shape behind Apple's App Privacy and Google's Data Safety questionnaires.
type DataCollection struct {
	Category        string   `json:"category"`        // canonical Yaver label
	AppleType       string   `json:"appleType"`       // Apple App Privacy data type
	GoogleType      string   `json:"googleType"`      // Google Data Safety category
	Purposes        []string `json:"purposes"`        // AppFunctionality | Analytics | Advertising | …
	LinkedToUser    bool     `json:"linkedToUser"`    // tied to identity?
	UsedForTracking bool     `json:"usedForTracking"` // cross-app tracking (Apple ATT / Play ad-id)
	Source          string   `json:"source"`          // what triggered it (capability/SDK)
}

// dataRule maps a code signal (a capability id OR an SDK dependency) to a
// declared collection. Keep truthful + conservative.
type dataRule struct {
	// Match if EITHER a detected capability id OR a present dependency.
	Capability string
	Dep        string
	Collection DataCollection
}

var dataRules = []dataRule{
	// ── From capabilities (already detected by capabilities.go) ──
	{Capability: "location-when-in-use", Collection: DataCollection{Category: "Location", AppleType: "Precise Location", GoogleType: "Location", Purposes: []string{"AppFunctionality"}, LinkedToUser: true, Source: "capability:location"}},
	{Capability: "location-always", Collection: DataCollection{Category: "Location", AppleType: "Precise Location", GoogleType: "Location", Purposes: []string{"AppFunctionality"}, LinkedToUser: true, Source: "capability:location-always"}},
	{Capability: "contacts", Collection: DataCollection{Category: "Contacts", AppleType: "Contacts", GoogleType: "Contacts", Purposes: []string{"AppFunctionality"}, LinkedToUser: true, Source: "capability:contacts"}},
	{Capability: "photos", Collection: DataCollection{Category: "Photos", AppleType: "Photos or Videos", GoogleType: "Photos and videos", Purposes: []string{"AppFunctionality"}, LinkedToUser: false, Source: "capability:photos"}},
	{Capability: "health", Collection: DataCollection{Category: "Health", AppleType: "Health", GoogleType: "Health and fitness", Purposes: []string{"AppFunctionality"}, LinkedToUser: true, Source: "capability:health"}},
	{Capability: "microphone", Collection: DataCollection{Category: "Audio", AppleType: "Audio Data", GoogleType: "Audio", Purposes: []string{"AppFunctionality"}, LinkedToUser: false, Source: "capability:microphone"}},
	{Capability: "tracking", Collection: DataCollection{Category: "Identifiers", AppleType: "Device ID", GoogleType: "Device or other IDs", Purposes: []string{"Advertising"}, LinkedToUser: true, UsedForTracking: true, Source: "capability:tracking"}},
	// ── From SDK dependencies ──
	{Dep: "@react-native-firebase/analytics", Collection: DataCollection{Category: "UsageData", AppleType: "Product Interaction", GoogleType: "App activity", Purposes: []string{"Analytics"}, LinkedToUser: true, Source: "sdk:firebase-analytics"}},
	{Dep: "posthog-react-native", Collection: DataCollection{Category: "UsageData", AppleType: "Product Interaction", GoogleType: "App activity", Purposes: []string{"Analytics"}, LinkedToUser: true, Source: "sdk:posthog"}},
	{Dep: "@amplitude/analytics-react-native", Collection: DataCollection{Category: "UsageData", AppleType: "Product Interaction", GoogleType: "App activity", Purposes: []string{"Analytics"}, LinkedToUser: true, Source: "sdk:amplitude"}},
	{Dep: "@segment/analytics-react-native", Collection: DataCollection{Category: "UsageData", AppleType: "Product Interaction", GoogleType: "App activity", Purposes: []string{"Analytics"}, LinkedToUser: true, Source: "sdk:segment"}},
	{Dep: "@sentry/react-native", Collection: DataCollection{Category: "Diagnostics", AppleType: "Crash Data", GoogleType: "App info and performance", Purposes: []string{"Analytics"}, LinkedToUser: false, Source: "sdk:sentry"}},
	{Dep: "@bugsnag/react-native", Collection: DataCollection{Category: "Diagnostics", AppleType: "Crash Data", GoogleType: "App info and performance", Purposes: []string{"Analytics"}, LinkedToUser: false, Source: "sdk:bugsnag"}},
	{Dep: "@react-native-firebase/crashlytics", Collection: DataCollection{Category: "Diagnostics", AppleType: "Crash Data", GoogleType: "App info and performance", Purposes: []string{"Analytics"}, LinkedToUser: false, Source: "sdk:crashlytics"}},
	{Dep: "react-native-google-mobile-ads", Collection: DataCollection{Category: "Identifiers", AppleType: "Device ID", GoogleType: "Device or other IDs", Purposes: []string{"Advertising"}, LinkedToUser: true, UsedForTracking: true, Source: "sdk:admob"}},
	{Dep: "react-native-fbsdk-next", Collection: DataCollection{Category: "Identifiers", AppleType: "Device ID", GoogleType: "Device or other IDs", Purposes: []string{"Advertising"}, LinkedToUser: true, UsedForTracking: true, Source: "sdk:facebook"}},
}

// ScreenshotSlot is one store-required screenshot size (filled later by the
// redroid/simulator asset generator).
type ScreenshotSlot struct {
	Platform    string `json:"platform"` // ios | android
	DeviceClass string `json:"deviceClass"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	MinCount    int    `json:"minCount"`
}

// requiredScreenshotSlots — the store-mandated sizes (portrait). Apple infers
// some sizes from the 6.7"; Google needs phone shots. Kept minimal + exact.
var requiredScreenshotSlots = []ScreenshotSlot{
	{Platform: "ios", DeviceClass: "iPhone 6.7\"", Width: 1290, Height: 2796, MinCount: 1},
	{Platform: "ios", DeviceClass: "iPhone 6.5\"", Width: 1242, Height: 2688, MinCount: 1},
	{Platform: "ios", DeviceClass: "iPad 12.9\"", Width: 2048, Height: 2732, MinCount: 0},
	{Platform: "android", DeviceClass: "Phone", Width: 1080, Height: 1920, MinCount: 2},
	{Platform: "android", DeviceClass: "Feature graphic", Width: 1024, Height: 500, MinCount: 1},
}

// DerivationContext is the ground-truth the AI layer uses to draft the
// free-text fields (so it never hallucinates capabilities the app lacks).
type DerivationContext struct {
	DetectedCapabilities []string `json:"detectedCapabilities"`
	SDKs                 []string `json:"sdks"`
	Notes                []string `json:"notes"`
}

// StoreListing is the canonical model both store projectors consume.
type StoreListing struct {
	AppName          string            `json:"appName"`
	Subtitle         string            `json:"subtitle"` // Apple subtitle / Google short description
	BundleID         string            `json:"bundleId"`
	PackageName      string            `json:"packageName"`
	Version          string            `json:"version"`
	PrimaryCategory  string            `json:"primaryCategory"`
	Description      string            `json:"description"` // DRAFT — AI refines
	Keywords         []string          `json:"keywords"`    // Apple (≤100 chars total)
	WhatsNew         string            `json:"whatsNew"`
	SupportURL       string            `json:"supportUrl,omitempty"`
	MarketingURL     string            `json:"marketingUrl,omitempty"`
	PrivacyPolicyURL string            `json:"privacyPolicyUrl,omitempty"`
	Privacy          []DataCollection  `json:"privacy"`
	Screenshots      []ScreenshotSlot  `json:"screenshots"`
	ConsoleForms     []string          `json:"consoleForms"` // human-only (Data Safety, ratings…)
	Derivation       DerivationContext `json:"derivation"`
}

// appConfig is the slice of app.json/app.config we read for identity.
type appConfig struct {
	Expo struct {
		Name    string `json:"name"`
		Slug    string `json:"slug"`
		Version string `json:"version"`
		IOS     struct {
			BundleIdentifier string `json:"bundleIdentifier"`
		} `json:"ios"`
		Android struct {
			Package string `json:"package"`
		} `json:"android"`
	} `json:"expo"`
}

func readAppConfig(projectDir string) appConfig {
	var cfg appConfig
	// app.json is the static form; app.config.js can't be read without
	// executing JS, so we fall back to app.json (the common case).
	if b, err := os.ReadFile(filepath.Join(projectDir, "app.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	return cfg
}

// derivePrivacy returns the truthful data-collection set from detected
// capabilities + present SDKs. Deduped by (category, source).
func derivePrivacy(detectedCaps map[string]bool, deps map[string]bool) []DataCollection {
	var out []DataCollection
	seen := map[string]bool{}
	for _, r := range dataRules {
		hit := (r.Capability != "" && detectedCaps[r.Capability]) || (r.Dep != "" && deps[r.Dep])
		if !hit {
			continue
		}
		key := r.Collection.Category + "|" + r.Collection.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r.Collection)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

// BuildStoreListing assembles the canonical listing from the project.
func BuildStoreListing(projectDir string) StoreListing {
	cfg := readAppConfig(projectDir)
	deps := scanProjectDeps(projectDir)
	plan := buildManifestPlan(projectDir)

	detected := map[string]bool{}
	var detectedList []string
	for _, f := range plan.Findings {
		if f.Detected {
			detected[f.ID] = true
			detectedList = append(detectedList, f.ID)
		}
	}
	sort.Strings(detectedList)

	name := cfg.Expo.Name
	if name == "" {
		name = cfg.Expo.Slug
	}

	listing := StoreListing{
		AppName:      name,
		BundleID:     cfg.Expo.IOS.BundleIdentifier,
		PackageName:  cfg.Expo.Android.Package,
		Version:      cfg.Expo.Version,
		Description:  draftDescription(name, detectedList),
		Privacy:      derivePrivacy(detected, deps),
		Screenshots:  append([]ScreenshotSlot(nil), requiredScreenshotSlots...),
		ConsoleForms: plan.ConsoleForms,
		Derivation: DerivationContext{
			DetectedCapabilities: detectedList,
			SDKs:                 sortedDepSubset(deps),
			Notes: []string{
				"Marketing copy is a DRAFT — refine with the AI grounded on the detected capabilities.",
				"Privacy entries are derived from code (capabilities + SDKs) and must match actual behaviour.",
			},
		},
	}
	return listing
}

// draftDescription is a deterministic placeholder the AI improves — never
// the final copy, just a non-empty starting point grounded in real features.
func draftDescription(name string, caps []string) string {
	if name == "" {
		name = "This app"
	}
	if len(caps) == 0 {
		return name + " — a mobile app built with Yaver."
	}
	return fmt.Sprintf("%s — a mobile app. Detected features to highlight: %s.", name, strings.Join(caps, ", "))
}

// sortedDepSubset returns only the dependencies that are privacy/capability
// relevant (avoids dumping the whole dep tree into the context).
func sortedDepSubset(deps map[string]bool) []string {
	relevant := map[string]bool{}
	for _, r := range dataRules {
		if r.Dep != "" && deps[r.Dep] {
			relevant[r.Dep] = true
		}
	}
	for _, c := range capabilityCatalogue {
		for _, sig := range c.Signals {
			if deps[sig] {
				relevant[sig] = true
			}
		}
	}
	var out []string
	for d := range relevant {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// ── CLI ──────────────────────────────────────────────────────────────

func runListing(args []string) {
	if len(args) > 0 && args[0] == "push" {
		runListingPush(args[1:])
		return
	}
	jsonOut := false
	path := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("Usage: yaver listing [--path DIR] [--json]")
			fmt.Println("  Derives a canonical store listing (identity + truthful privacy) from your code.")
			return
		}
	}
	listing := BuildStoreListing(path)
	if jsonOut {
		b, _ := json.MarshalIndent(listing, "", "  ")
		fmt.Println(string(b))
		return
	}
	printListing(listing)
}

func printListing(l StoreListing) {
	fmt.Printf("Store listing (derived from your code)\n\n")
	fmt.Printf("  App:       %s\n", dashIfEmpty(l.AppName))
	fmt.Printf("  iOS:       %s\n", dashIfEmpty(l.BundleID))
	fmt.Printf("  Android:   %s\n", dashIfEmpty(l.PackageName))
	fmt.Printf("  Version:   %s\n", dashIfEmpty(l.Version))
	fmt.Printf("\n  Description (DRAFT — AI refines):\n    %s\n", l.Description)
	if len(l.Privacy) > 0 {
		fmt.Println("\n  Privacy / Data Safety (derived from code — must match behaviour):")
		for _, d := range l.Privacy {
			track := ""
			if d.UsedForTracking {
				track = " · tracking"
			}
			fmt.Printf("    • %-12s Apple=%-22s Google=%-26s [%s%s]  (%s)\n",
				d.Category, d.AppleType, d.GoogleType, strings.Join(d.Purposes, ","), track, d.Source)
		}
	} else {
		fmt.Println("\n  Privacy: no data collection detected (declare 'No data collected').")
	}
	if len(l.ConsoleForms) > 0 {
		fmt.Println("\n  Console forms a human must submit (Yaver drafts + routes):")
		for _, f := range l.ConsoleForms {
			fmt.Printf("    ◆ %s\n", f)
		}
	}
	fmt.Println("\n  Screenshots needed (filled by the redroid/simulator asset generator):")
	for _, s := range l.Screenshots {
		if s.MinCount > 0 {
			fmt.Printf("    %s %s — %dx%d ×%d\n", s.Platform, s.DeviceClass, s.Width, s.Height, s.MinCount)
		}
	}
}

// ── Agent HTTP ───────────────────────────────────────────────────────

func (s *HTTPServer) handleListing(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	writeJSON(w, http.StatusOK, BuildStoreListing(path))
}
