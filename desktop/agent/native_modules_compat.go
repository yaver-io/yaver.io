package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sdkManifestJSON is mobile/sdk-manifest.json embedded at compile time.
// Keep this file in sync with the canonical copy at mobile/sdk-manifest.json
// — TestSDKManifestInSync fails the build if they diverge.
//
//go:embed sdk-manifest.json
var sdkManifestJSON []byte

type sdkManifest struct {
	SdkVersion       string            `json:"sdkVersion"`
	Expo             string            `json:"expo"`
	ReactNative      string            `json:"reactNative"`
	React            string            `json:"react"`
	SupportedRNRange string            `json:"supportedRNRange"`
	NativeModules    map[string]string `json:"nativeModules"`
	ModuleSupport    map[string]struct {
		Version string `json:"version"`
	} `json:"moduleSupport"`
	Hermes struct {
		Version         string `json:"version"`
		BytecodeVersion int    `json:"bytecodeVersion"`
	} `json:"hermes"`
}

type RuntimeFingerprint struct {
	ExpoVersion        string `json:"expoVersion,omitempty"`
	ReactNativeVersion string `json:"reactNativeVersion,omitempty"`
	ReactVersion       string `json:"reactVersion,omitempty"`
	HermesBCVersion    int    `json:"hermesBCVersion,omitempty"`
}

type RuntimeFamily struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	SDKVersion       string `json:"sdkVersion,omitempty"`
	ExpoVersion      string `json:"expoVersion,omitempty"`
	ReactNative      string `json:"reactNativeVersion,omitempty"`
	React            string `json:"reactVersion,omitempty"`
	HermesVersion    string `json:"hermesVersion,omitempty"`
	HermesBCVersion  int    `json:"hermesBCVersion,omitempty"`
	SupportedRNRange string `json:"supportedRNRange,omitempty"`
}

type RuntimeFamilySelection struct {
	Guest         RuntimeFingerprint `json:"guest"`
	Selected      RuntimeFamily      `json:"selected"`
	ExactMatch    bool               `json:"exactMatch"`
	MatchKind     string             `json:"matchKind"`
	Reason        string             `json:"reason,omitempty"`
	Distance      int                `json:"distance"`
	Supported     []RuntimeFamily    `json:"supported"`
	SupportedHint string             `json:"supportedHint,omitempty"`
}

var (
	cachedManifest      *sdkManifest
	cachedManifestNames map[string]bool
)

// loadHostSDKManifest decodes the embedded sdk-manifest.json once.
func loadHostSDKManifest() (*sdkManifest, error) {
	if cachedManifest != nil {
		return cachedManifest, nil
	}
	var m sdkManifest
	if err := json.Unmarshal(sdkManifestJSON, &m); err != nil {
		return nil, fmt.Errorf("decode embedded sdk-manifest.json: %w", err)
	}
	names := make(map[string]bool, len(m.NativeModules))
	for k := range m.NativeModules {
		names[k] = true
	}
	cachedManifest = &m
	cachedManifestNames = names
	return cachedManifest, nil
}

// HostSupportedNativeModules returns the set of module names the Yaver mobile
// super-host registers, sourced from the embedded SDK manifest. Read-only.
func HostSupportedNativeModules() (map[string]bool, error) {
	if _, err := loadHostSDKManifest(); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(cachedManifestNames))
	for k, v := range cachedManifestNames {
		out[k] = v
	}
	return out, nil
}

// ExtractProjectNativeModules walks workDir/package.json and returns the
// dependency keys that LOOK like native (auto-linked) RN modules.
//
// We deliberately err on the side of inclusion. Pure-JS packages with
// names that match the heuristic (e.g. `react-native-svg-transformer`
// during dev) might surface as false positives, but it's safer to flag a
// few extras for the human to dismiss than to miss a real one and crash.
//
// Heuristic — a key is treated as native if any of:
//
//	react-native-…
//	@<scope>/react-native-…
//	@react-native-…
//	@react-native-community/…
//	expo-…
//	@expo/…  (excluding @expo/vector-icons-style packages — handled by sdk-manifest match)
//
// And the name is NOT in the explicit allow-list of pure-JS packages
// (jsOnlyPrefixes / jsOnlyExact) below.
func ExtractProjectNativeModules(workDir string) ([]string, error) {
	pkgPath := filepath.Join(workDir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgPath, err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}
	out, _, err := extractProjectNativeModulesFromDeps(pkg.Dependencies, workDir)
	return out, err
}

// jsOnlyExact is a deny-list of names that match the heuristic but are
// pure-JS in practice. Add cautiously — a wrong entry hides a real
// incompatibility.
var jsOnlyExact = map[string]bool{
	"@expo/metro-runtime":               true, // Metro dev-server shim, not a runtime TurboModule
	"@expo/vector-icons":                true,
	"@gorhom/bottom-sheet":              true, // JS wrapper over host-native deps
	"@react-native/babel-preset":        true,
	"@react-native/eslint-config":       true,
	"@react-native/metro-config":        true,
	"@react-native/typescript-config":   true,
	"@shopify/flash-list":               true, // JS list layer over host-native deps
	"expo":                              true, // umbrella shim — actual modules are expo-foo / @expo/foo
	"expo-build-properties":             true, // build-time only
	"expo-modules-autolinking":          true, // build-time only
	"expo-modules-core":                 true, // wired by the host runtime, not a guest dep
	"expo-router":                       true, // JS routing layer, not a host native runtime module
	"posthog-react-native":              true, // JS wrapper SDK
	"react-native":                      true, // the engine itself
	"react-native-modal":                true,
	"react-native-progress":             true,
	"react-native-qrcode-svg":           true,
	"react-native-reanimated-carousel":  true,
	"react-native-skeleton-placeholder": true,
	"react-native-svg-transformer":      true,
	"react-native-swipe-list-view":      true,
	"react-native-toast-message":        true,
	"react-native-url-polyfill":         true,
	"react-native-uuid":                 true,
	"react-native-web":                  true,
	"victory-native":                    true, // JS charting layer over react-native-svg
	"yaver-feedback-react-native":       true, // app-side SDK/plugin; not required for Open in Yaver host loads
}

func isLikelyNativeModule(workDir, name string) bool {
	if jsOnlyExact[name] {
		return false
	}
	packageDir := filepath.Join(workDir, "node_modules", filepath.FromSlash(name))
	if hasNativePackageMarkers(packageDir) {
		return true
	}
	if hasPackageDir(packageDir) {
		return false
	}
	if strings.Contains(name, "react-native") {
		return true
	}
	if strings.HasPrefix(name, "expo-") || strings.HasPrefix(name, "@expo/") {
		return true
	}
	return false
}

func extractProjectNativeModulesFromDeps(deps map[string]string, workDir string) ([]string, []string, error) {
	out := make([]string, 0, len(deps))
	ignored := make([]string, 0)
	for name := range deps {
		if jsOnlyExact[name] {
			if name == "yaver-feedback-react-native" {
				ignored = append(ignored, name)
			}
			continue
		}
		if isLikelyNativeModule(workDir, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	sort.Strings(ignored)
	return out, ignored, nil
}

func hasPackageDir(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func hasNativePackageMarkers(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".podspec") {
			return true
		}
		if entry.IsDir() && (name == "ios" || name == "android") {
			return true
		}
	}
	return false
}

// CompatReport summarises how a project's native deps line up with the
// host super-host's published manifest.
type CompatReport struct {
	ProjectModules       []string                `json:"projectModules"`                 // every dep we treated as native
	Matched              []string                `json:"matched"`                        // present in host manifest
	Incompatible         []string                `json:"incompatibleNativeModules"`      // missing — likely crash sites
	Ignored              []string                `json:"ignoredNativeModules"`           // intentionally ignored host-optional packages
	VersionMismatches    []NativeModuleMismatch  `json:"nativeModuleVersionMismatches"`  // present but at a likely-breaking version boundary
	ReactVersionMismatch *VersionMismatch        `json:"reactVersionMismatch,omitempty"` // project React vs host React
	ExpoVersionMismatch  *VersionMismatch        `json:"expoVersionMismatch,omitempty"`  // project Expo vs host Expo
	RNVersionMismatch    *VersionMismatch        `json:"reactNativeVersionMismatch,omitempty"`
	HostSDKVersion       string                  `json:"hostSdkVersion"`
	HostExpo             string                  `json:"hostExpoVersion"`
	HostReact            string                  `json:"hostReactVersion"`
	HostRN               string                  `json:"hostReactNative"`
	SupportedRNRange     string                  `json:"supportedRNRange"`
	GuestRuntime         RuntimeFingerprint      `json:"guestRuntime"`
	RuntimeFamily        *RuntimeFamilySelection `json:"runtimeFamilySelection,omitempty"`
}

type NativeModuleMismatch struct {
	Name           string `json:"name"`
	ProjectVersion string `json:"projectVersion"`
	HostVersion    string `json:"hostVersion"`
	Reason         string `json:"reason"`
}

type VersionMismatch struct {
	ProjectVersion string `json:"projectVersion"`
	HostVersion    string `json:"hostVersion"`
	Reason         string `json:"reason"`
}

// BuildNativeModuleCompatReport runs the diff. workDir is the third-party
// project root (where its package.json lives). Returns a fully-populated
// report — caller decides whether to warn or hard-fail on Incompatible.
func BuildNativeModuleCompatReport(workDir string) (*CompatReport, error) {
	return BuildNativeModuleCompatReportWithFamilies(workDir, nil)
}

func BuildNativeModuleCompatReportWithFamilies(workDir string, supportedFamilies []RuntimeFamily) (*CompatReport, error) {
	host, err := loadHostSDKManifest()
	if err != nil {
		return nil, err
	}
	hostNames, _ := HostSupportedNativeModules()
	pkgPath := filepath.Join(workDir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgPath, err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}
	projectMods, ignored, err := extractProjectNativeModulesFromDeps(pkg.Dependencies, workDir)
	if err != nil {
		return nil, err
	}
	matched := make([]string, 0, len(projectMods))
	missing := make([]string, 0)
	versionMismatches := make([]NativeModuleMismatch, 0)
	for _, m := range projectMods {
		if hostNames[m] {
			matched = append(matched, m)
			projectVersion := readProjectDependencyVersion(workDir, pkg.Dependencies, m)
			if mismatch := detectNativeModuleVersionMismatch(m, projectVersion, host); mismatch != nil {
				versionMismatches = append(versionMismatches, *mismatch)
			}
		} else {
			missing = append(missing, m)
		}
	}
	sort.Slice(versionMismatches, func(i, j int) bool {
		return versionMismatches[i].Name < versionMismatches[j].Name
	})
	var reactMismatch *VersionMismatch
	projectReact := readProjectDependencyVersion(workDir, pkg.Dependencies, "react")
	projectExpo := readProjectDependencyVersion(workDir, pkg.Dependencies, "expo")
	projectRN := readProjectDependencyVersion(workDir, pkg.Dependencies, "react-native")
	if mismatch := detectFrameworkVersionMismatch(projectReact, host.React); mismatch != nil {
		reactMismatch = mismatch
	}
	var expoMismatch *VersionMismatch
	if mismatch := detectFrameworkVersionMismatch(projectExpo, host.Expo); mismatch != nil {
		expoMismatch = mismatch
	}
	var rnMismatch *VersionMismatch
	if mismatch := detectFrameworkVersionMismatch(projectRN, host.ReactNative); mismatch != nil {
		rnMismatch = mismatch
	}
	guestRuntime := RuntimeFingerprint{
		ExpoVersion:        strings.TrimSpace(projectExpo),
		ReactNativeVersion: strings.TrimSpace(projectRN),
		ReactVersion:       strings.TrimSpace(projectReact),
	}
	runtimeFamilies := append([]RuntimeFamily(nil), supportedFamilies...)
	if len(runtimeFamilies) == 0 {
		var familyErr error
		runtimeFamilies, familyErr = HostRuntimeFamilies()
		if familyErr != nil {
			runtimeFamilies = nil
		}
	}
	var familySelection *RuntimeFamilySelection
	if len(runtimeFamilies) > 0 {
		sel := SelectRuntimeFamily(guestRuntime, runtimeFamilies)
		familySelection = &sel
	}
	return &CompatReport{
		ProjectModules:       projectMods,
		Matched:              matched,
		Incompatible:         missing,
		Ignored:              ignored,
		VersionMismatches:    versionMismatches,
		ReactVersionMismatch: reactMismatch,
		ExpoVersionMismatch:  expoMismatch,
		RNVersionMismatch:    rnMismatch,
		HostSDKVersion:       host.SdkVersion,
		HostExpo:             host.Expo,
		HostReact:            host.React,
		HostRN:               host.ReactNative,
		SupportedRNRange:     host.SupportedRNRange,
		GuestRuntime:         guestRuntime,
		RuntimeFamily:        familySelection,
	}, nil
}

func HostRuntimeFamilies() ([]RuntimeFamily, error) {
	host, err := loadHostSDKManifest()
	if err != nil {
		return nil, err
	}
	family := RuntimeFamily{
		ID:               runtimeFamilyID(host.Expo, host.ReactNative, host.React, host.Hermes.BytecodeVersion),
		Label:            runtimeFamilyLabel(host.Expo, host.ReactNative, host.React, host.Hermes.BytecodeVersion),
		SDKVersion:       strings.TrimSpace(host.SdkVersion),
		ExpoVersion:      strings.TrimSpace(host.Expo),
		ReactNative:      strings.TrimSpace(host.ReactNative),
		React:            strings.TrimSpace(host.React),
		HermesVersion:    strings.TrimSpace(host.Hermes.Version),
		HermesBCVersion:  host.Hermes.BytecodeVersion,
		SupportedRNRange: strings.TrimSpace(host.SupportedRNRange),
	}
	return []RuntimeFamily{family}, nil
}

func SelectRuntimeFamily(guest RuntimeFingerprint, supported []RuntimeFamily) RuntimeFamilySelection {
	selection := RuntimeFamilySelection{
		Guest:         guest,
		Supported:     append([]RuntimeFamily(nil), supported...),
		MatchKind:     "unknown",
		SupportedHint: runtimeFamilySupportedHint(supported),
	}
	if len(supported) == 0 {
		selection.Reason = "host did not advertise any runtime families"
		return selection
	}
	best := supported[0]
	bestDistance := runtimeFamilyDistance(guest, best)
	for _, family := range supported[1:] {
		distance := runtimeFamilyDistance(guest, family)
		if distance < bestDistance {
			best = family
			bestDistance = distance
		}
	}
	selection.Selected = best
	selection.Distance = bestDistance
	selection.ExactMatch = runtimeFamilyExactMatch(guest, best)
	if selection.ExactMatch {
		selection.MatchKind = "exact"
		selection.Reason = fmt.Sprintf(
			"guest Expo %s / RN %s / React %s matches host family %s",
			fallbackRuntimeValue(guest.ExpoVersion, "?"),
			fallbackRuntimeValue(guest.ReactNativeVersion, "?"),
			fallbackRuntimeValue(guest.ReactVersion, "?"),
			best.ID,
		)
		return selection
	}
	selection.MatchKind = "closest"
	selection.Reason = fmt.Sprintf(
		"guest Expo %s / RN %s / React %s is closest to host family %s",
		fallbackRuntimeValue(guest.ExpoVersion, "?"),
		fallbackRuntimeValue(guest.ReactNativeVersion, "?"),
		fallbackRuntimeValue(guest.ReactVersion, "?"),
		best.ID,
	)
	return selection
}

func runtimeFamilyID(expo, rn, react string, bc int) string {
	return fmt.Sprintf(
		"expo-%s-rn-%s-react-%s-bc-%d",
		runtimeFamilyVersionToken(expo),
		runtimeFamilyVersionToken(rn),
		runtimeFamilyVersionToken(react),
		bc,
	)
}

func runtimeFamilyLabel(expo, rn, react string, bc int) string {
	return fmt.Sprintf(
		"Expo %s / RN %s / React %s / BC%d",
		fallbackRuntimeValue(strings.TrimSpace(expo), "?"),
		fallbackRuntimeValue(strings.TrimSpace(rn), "?"),
		fallbackRuntimeValue(strings.TrimSpace(react), "?"),
		bc,
	)
}

func runtimeFamilyVersionToken(raw string) string {
	s := parseSemverish(raw)
	if s == nil {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "unknown"
		}
		raw = strings.NewReplacer(".", "-", "^", "", "~", "", " ", "", "/", "-").Replace(raw)
		return strings.ToLower(raw)
	}
	return fmt.Sprintf("%d-%d-%d", s.major, s.minor, s.patch)
}

func runtimeFamilyExactMatch(guest RuntimeFingerprint, family RuntimeFamily) bool {
	return runtimeVersionEquals(guest.ExpoVersion, family.ExpoVersion) &&
		runtimeVersionEquals(guest.ReactNativeVersion, family.ReactNative) &&
		runtimeVersionEquals(guest.ReactVersion, family.React)
}

func runtimeVersionEquals(a, b string) bool {
	sa := parseSemverish(a)
	sb := parseSemverish(b)
	if sa == nil || sb == nil {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return sa.major == sb.major && sa.minor == sb.minor && sa.patch == sb.patch
}

func runtimeFamilyDistance(guest RuntimeFingerprint, family RuntimeFamily) int {
	return weightedVersionDistance(guest.ReactNativeVersion, family.ReactNative, 700, 150, 30) +
		weightedVersionDistance(guest.ExpoVersion, family.ExpoVersion, 400, 90, 20) +
		weightedVersionDistance(guest.ReactVersion, family.React, 250, 70, 15)
}

func weightedVersionDistance(guest, host string, majorWeight, minorWeight, patchWeight int) int {
	ga := parseSemverish(guest)
	ha := parseSemverish(host)
	if ga == nil || ha == nil {
		if strings.TrimSpace(guest) == strings.TrimSpace(host) {
			return 0
		}
		if strings.TrimSpace(guest) == "" || strings.TrimSpace(host) == "" {
			return majorWeight * 2
		}
		return majorWeight
	}
	return intAbs(ga.major-ha.major)*majorWeight +
		intAbs(ga.minor-ha.minor)*minorWeight +
		intAbs(ga.patch-ha.patch)*patchWeight
}

func runtimeFamilySupportedHint(families []RuntimeFamily) string {
	if len(families) == 0 {
		return ""
	}
	parts := make([]string, 0, len(families))
	for _, family := range families {
		parts = append(parts, fmt.Sprintf("%s (%s)", family.ID, family.Label))
	}
	return strings.Join(parts, "; ")
}

func fallbackRuntimeValue(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	return raw
}

func intAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func readProjectDependencyVersion(workDir string, deps map[string]string, name string) string {
	if version, err := readInstalledPackageVersion(workDir, name); err == nil && strings.TrimSpace(version) != "" {
		return strings.TrimSpace(version)
	}
	return strings.TrimSpace(deps[name])
}

func detectNativeModuleVersionMismatch(name, projectVersion string, host *sdkManifest) *NativeModuleMismatch {
	hostVersion := strings.TrimSpace(host.NativeModules[name])
	if hostVersion == "" {
		if ms, ok := host.ModuleSupport[name]; ok {
			hostVersion = strings.TrimSpace(ms.Version)
		}
	}
	if hostVersion == "" {
		return nil
	}
	mismatch := detectVersionMismatch(projectVersion, hostVersion)
	if mismatch == nil {
		return nil
	}
	return &NativeModuleMismatch{
		Name:           name,
		ProjectVersion: mismatch.ProjectVersion,
		HostVersion:    mismatch.HostVersion,
		Reason:         mismatch.Reason,
	}
}

func detectVersionMismatch(projectVersion, hostVersion string) *VersionMismatch {
	project := parseSemverish(projectVersion)
	host := parseSemverish(hostVersion)
	if project == nil || host == nil {
		return nil
	}
	if project.major != host.major {
		return &VersionMismatch{
			ProjectVersion: project.original,
			HostVersion:    host.original,
			Reason:         "major version differs",
		}
	}
	if project.major == 0 && project.minor != host.minor {
		return &VersionMismatch{
			ProjectVersion: project.original,
			HostVersion:    host.original,
			Reason:         "0.x minor version differs",
		}
	}
	return nil
}

func detectFrameworkVersionMismatch(projectVersion, hostVersion string) *VersionMismatch {
	project := parseSemverish(projectVersion)
	host := parseSemverish(hostVersion)
	if project == nil || host == nil {
		projectVersion = strings.TrimSpace(projectVersion)
		hostVersion = strings.TrimSpace(hostVersion)
		if projectVersion == "" || hostVersion == "" || projectVersion == hostVersion {
			return nil
		}
		return &VersionMismatch{
			ProjectVersion: projectVersion,
			HostVersion:    hostVersion,
			Reason:         "exact runtime version differs",
		}
	}
	if project.major != host.major || project.minor != host.minor || project.patch != host.patch {
		return &VersionMismatch{
			ProjectVersion: project.original,
			HostVersion:    host.original,
			Reason:         "exact runtime version differs",
		}
	}
	return nil
}

type semverish struct {
	original string
	major    int
	minor    int
	patch    int
}

func parseSemverish(raw string) *semverish {
	clean := strings.TrimSpace(raw)
	clean = strings.TrimLeft(clean, "^~<>= v")
	if clean == "" {
		return nil
	}
	parts := strings.Split(clean, ".")
	if len(parts) < 2 {
		return nil
	}
	parsePart := func(s string) (int, bool) {
		digits := make([]rune, 0, len(s))
		for _, r := range s {
			if r >= '0' && r <= '9' {
				digits = append(digits, r)
				continue
			}
			break
		}
		if len(digits) == 0 {
			return 0, false
		}
		v, err := strconv.Atoi(string(digits))
		if err != nil {
			return 0, false
		}
		return v, true
	}
	major, ok := parsePart(parts[0])
	if !ok {
		return nil
	}
	minor, ok := parsePart(parts[1])
	if !ok {
		return nil
	}
	patch := 0
	if len(parts) > 2 {
		if p, ok := parsePart(parts[2]); ok {
			patch = p
		}
	}
	return &semverish{
		original: clean,
		major:    major,
		minor:    minor,
		patch:    patch,
	}
}
