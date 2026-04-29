package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	ReactNative      string            `json:"reactNative"`
	React            string            `json:"react"`
	SupportedRNRange string            `json:"supportedRNRange"`
	NativeModules    map[string]string `json:"nativeModules"`
	Hermes           struct {
		Version         string `json:"version"`
		BytecodeVersion int    `json:"bytecodeVersion"`
	} `json:"hermes"`
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
	out := make([]string, 0, len(pkg.Dependencies))
	for name := range pkg.Dependencies {
		if isLikelyNativeModule(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// jsOnlyExact is a deny-list of names that match the heuristic but are
// pure-JS in practice. Add cautiously — a wrong entry hides a real
// incompatibility.
var jsOnlyExact = map[string]bool{
	"react-native":                    true, // the engine itself
	"react-native-web":                true,
	"react-native-svg-transformer":    true,
	"react-native-url-polyfill":       true,
	"react-native-uuid":               true,
	"yaver-feedback-react-native":     true, // app-side SDK/plugin; not required for Open in Yaver host loads
	"@react-native/babel-preset":      true,
	"@react-native/eslint-config":     true,
	"@react-native/typescript-config": true,
	"@react-native/metro-config":      true,
	"expo":                            true, // umbrella shim — actual modules are expo-foo / @expo/foo
	"expo-modules-core":               true, // wired by the host runtime, not a guest dep
	"expo-modules-autolinking":        true, // build-time only
	"@expo/metro-runtime":             true, // Metro dev-server shim, not a runtime TurboModule
}

func isLikelyNativeModule(name string) bool {
	if jsOnlyExact[name] {
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

// CompatReport summarises how a project's native deps line up with the
// host super-host's published manifest.
type CompatReport struct {
	ProjectModules   []string `json:"projectModules"`            // every dep we treated as native
	Matched          []string `json:"matched"`                   // present in host manifest
	Incompatible     []string `json:"incompatibleNativeModules"` // missing — likely crash sites
	HostSDKVersion   string   `json:"hostSdkVersion"`
	HostRN           string   `json:"hostReactNative"`
	SupportedRNRange string   `json:"supportedRNRange"`
}

// BuildNativeModuleCompatReport runs the diff. workDir is the third-party
// project root (where its package.json lives). Returns a fully-populated
// report — caller decides whether to warn or hard-fail on Incompatible.
func BuildNativeModuleCompatReport(workDir string) (*CompatReport, error) {
	host, err := loadHostSDKManifest()
	if err != nil {
		return nil, err
	}
	hostNames, _ := HostSupportedNativeModules()
	projectMods, err := ExtractProjectNativeModules(workDir)
	if err != nil {
		return nil, err
	}
	matched := make([]string, 0, len(projectMods))
	missing := make([]string, 0)
	for _, m := range projectMods {
		if hostNames[m] {
			matched = append(matched, m)
		} else {
			missing = append(missing, m)
		}
	}
	return &CompatReport{
		ProjectModules:   projectMods,
		Matched:          matched,
		Incompatible:     missing,
		HostSDKVersion:   host.SdkVersion,
		HostRN:           host.ReactNative,
		SupportedRNRange: host.SupportedRNRange,
	}, nil
}
