package main

// build_preflight.go — platform + dependency gate shared by `ops build`,
// `ops deploy` and the BuildManager. It answers two questions BEFORE a
// build command is ever handed to the exec manager:
//
//  1. Is this build even possible on this host OS? An iOS / IPA build
//     needs macOS. On Linux/WSL `xcodebuild` isn't "missing", it's
//     impossible — fail fast with wrong_host_os instead of a cryptic
//     "xcodebuild: command not found" 200 lines into the build log.
//  2. Are the toolchain deps present? iOS → a real Xcode (not the CLT
//     stub). Android APK/AAB → JDK 17 + Android SDK. Web / backend /
//     library builds get no native gate.
//
// Missing deps are surfaced as deps_missing with a structured plan.
// JDK + Android SDK are auto-installable WITH the caller's approval
// (re-invoke with installDeps:true). Xcode is never auto-installed
// (Mac App Store only) — we only emit manual steps. This is the
// "infra-aware, dependency-aware, approval-gated" contract: nothing
// gets downloaded or installed unless the caller explicitly opted in.

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// nativeClass is the host-sensitive category a build/deploy resolves to.
type nativeClass string

const (
	nativeNone    nativeClass = ""        // web / backend / lib — no host gate
	nativeIOS     nativeClass = "ios"     // needs macOS + real Xcode
	nativeAndroid nativeClass = "android" // needs JDK 17 + Android SDK
)

// preflightDep is one unmet requirement.
type preflightDep struct {
	Name string `json:"name"`
	Have string `json:"have,omitempty"`
	Need string `json:"need"`
	// Auto: yaver can install this with the caller's approval
	// (installDeps:true). Xcode is Auto=false — Mac App Store only.
	Auto bool   `json:"auto"`
	Fix  string `json:"fix"`
}

// preflightResult is the gate verdict. OK=true means "go ahead and run
// the build". Otherwise Code is one of: wrong_host_os, deps_missing,
// deps_install_failed.
type preflightResult struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`

	Class      nativeClass `json:"class,omitempty"`
	RequiredOS string      `json:"requiredOS,omitempty"`
	HostOS     string      `json:"hostOS"`

	Missing     []preflightDep `json:"missing,omitempty"`
	ManualSteps []string       `json:"manualSteps,omitempty"`
	// Installable: every Missing dep has Auto=true, so re-invoking with
	// installDeps:true can fully resolve the gate.
	Installable bool `json:"installable,omitempty"`
	// Installed: what installDeps:true actually installed this run.
	Installed []string `json:"installed,omitempty"`
}

// classifyNative maps (verb, target, workDir) to a nativeClass.
//
// For deploy the target name is authoritative (testflight → iOS,
// playstore → Android). For build we resolve the command the verb is
// actually going to run via detectBuildCommand so the gate matches
// reality (an Expo monorepo with target=ios runs xcodebuild; a Flutter
// repo with no target runs `flutter build apk` → Android).
func classifyNative(verb, target, workDir string) nativeClass {
	tgt := strings.ToLower(strings.TrimSpace(target))

	if strings.EqualFold(verb, "deploy") {
		switch tgt {
		case "testflight":
			return nativeIOS
		case "playstore", "play":
			return nativeAndroid
		default:
			// cloudflare / convex / vercel / fly / eas (cloud submit) /
			// etc. need no host-sensitive native toolchain.
			return nativeNone
		}
	}

	// build verb. An explicit target wins when unambiguous.
	switch {
	case strings.Contains(tgt, "ios") || strings.Contains(tgt, "ipa"):
		return nativeIOS
	case strings.Contains(tgt, "android") || strings.Contains(tgt, "aab") ||
		strings.Contains(tgt, "apk") || strings.Contains(tgt, "appbundle"):
		return nativeAndroid
	}

	// Otherwise classify by the command detectBuildCommand will run.
	cmd, tool := detectBuildCommand(workDir, target)
	return classifyBuildCommand(cmd, tool)
}

// classifyBuildCommand inspects a resolved build command + tool label
// and decides whether it is a host-sensitive native build. Split out so
// it is unit-testable without a real project on disk.
func classifyBuildCommand(cmd, tool string) nativeClass {
	c := strings.ToLower(cmd)
	switch tool {
	case "expo-ios", "xcode":
		return nativeIOS
	case "expo-android", "gradle":
		return nativeAndroid
	}
	switch {
	case strings.Contains(c, "xcodebuild"),
		strings.Contains(c, "flutter build ipa"),
		strings.Contains(c, "flutter build ios"):
		return nativeIOS
	case strings.Contains(c, "gradlew"), strings.Contains(c, "gradle "),
		strings.Contains(c, "flutter build apk"),
		strings.Contains(c, "flutter build appbundle"),
		strings.Contains(c, "flutter build android"):
		return nativeAndroid
	}
	return nativeNone
}

// wireNativeClass maps a `yaver wire push` (stack, platform) pair to a
// nativeClass. Every wire stack ultimately produces a native iOS or
// Android binary (Expo/RN/Flutter prebuild then xcodebuild/gradle), so
// the gate is purely the resolved device platform.
func wireNativeClass(stack, platform string) nativeClass {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "ios":
		return nativeIOS
	case "android":
		return nativeAndroid
	}
	// Platform not yet resolved — fall back to the stack hint.
	switch strings.ToLower(strings.TrimSpace(stack)) {
	case "native-ios":
		return nativeIOS
	case "native-android":
		return nativeAndroid
	}
	return nativeNone
}

// buildPlatformClass maps a BuildManager BuildPlatform constant to a
// nativeClass — used by StartBuild's host-OS hard gate.
func buildPlatformClass(p BuildPlatform) nativeClass {
	switch p {
	case PlatformXcodeIPA, PlatformXcodeBuild, PlatformXcodeDeviceInstall,
		PlatformFlutterIPA, PlatformRNIOS, PlatformExpoIOS:
		return nativeIOS
	case PlatformGradleAPK, PlatformGradleAAB,
		PlatformFlutterAPK, PlatformFlutterAAB,
		PlatformRNAndroid, PlatformExpoAndroid:
		return nativeAndroid
	}
	return nativeNone
}

// hostOSGate is the cheap, dependency-free half: refuse an iOS build on
// a non-macOS host before anything is executed. Returns nil when the
// host can in principle perform this class of build.
func hostOSGate(cls nativeClass) error {
	if cls == nativeIOS && runtime.GOOS != "darwin" {
		return fmt.Errorf("iOS builds require macOS — this host is %s; "+
			"run it on a Mac (yaver code --attach <mac>) or pick a darwin device", runtime.GOOS)
	}
	return nil
}

// androidJDKOK reports whether a usable JDK 17+ is reachable, plus a
// human-readable "have" string for the deps_missing payload.
func androidJDKOK(ctx context.Context) (bool, string) {
	javaPath, err := exec.LookPath("java")
	if err != nil {
		if jh := findJavaHome(); strings.TrimSpace(jh) != "" {
			javaPath = filepath.Join(jh, "bin", "java")
		} else {
			return false, "not found"
		}
	}
	major, raw, err := javaMajorVersion(ctx, javaPath)
	if err != nil {
		if raw != "" {
			return false, raw
		}
		return false, "unparseable java -version"
	}
	return major >= 17, fmt.Sprintf("Java %d", major)
}

// androidGateVerdict is the pure decision: given JDK status and the
// detected Android SDK root, which deps are unmet? Split out so the
// matrix is unit-testable without a real toolchain.
func androidGateVerdict(jdkOK bool, jdkHave, sdkRoot string) []preflightDep {
	var missing []preflightDep
	if !jdkOK {
		missing = append(missing, preflightDep{
			Name: "jdk",
			Have: jdkHave,
			Need: "OpenJDK 17+ (Gradle for RN/Expo requires Java 17)",
			Auto: true,
			Fix:  "yaver installs OpenJDK 17 (re-invoke with installDeps:true)",
		})
	}
	if strings.TrimSpace(sdkRoot) == "" {
		missing = append(missing, preflightDep{
			Name: "android-sdk",
			Need: "Android SDK (platform-tools + build platform)",
			Auto: true,
			Fix:  "yaver downloads Android command-line tools + SDK (re-invoke with installDeps:true)",
		})
	}
	return missing
}

// runBuildPreflight is the gate. progress (optional) receives install
// log lines when installDeps actually runs.
func runBuildPreflight(ctx context.Context, cls nativeClass, installDeps bool, progress func(string)) preflightResult {
	if ctx == nil {
		ctx = context.Background()
	}
	r := preflightResult{OK: true, Class: cls, HostOS: runtime.GOOS}

	switch cls {
	case nativeNone:
		return r

	case nativeIOS:
		r.RequiredOS = "darwin"
		if runtime.GOOS != "darwin" {
			r.OK = false
			r.Code = "wrong_host_os"
			r.Error = fmt.Sprintf("iOS builds require macOS — this host is %s. "+
				"Run on a Mac or attach a darwin device.", runtime.GOOS)
			return r
		}
		_, ok, reason := xcodebuildIsRealXcode(ctx)
		if !ok {
			r.OK = false
			r.Code = "deps_missing"
			r.Error = "Xcode is required for iOS builds and is not active on this Mac."
			r.Missing = []preflightDep{{
				Name: "xcode",
				Need: "full Xcode (not just Command Line Tools)",
				Auto: false,
				Fix:  "Install Xcode from the Mac App Store",
			}}
			r.ManualSteps = []string{
				"Xcode cannot be installed non-interactively — install it from the Mac App Store.",
				strings.TrimSpace(reason),
				"Then: sudo xcode-select -s /Applications/Xcode.app/Contents/Developer && sudo xcodebuild -license accept",
			}
			r.Installable = false
		}
		return r

	case nativeAndroid:
		// Android builds are cross-platform; the gate is purely about
		// the toolchain, not the host OS.
		jdkOK, jdkHave := androidJDKOK(ctx)
		missing := androidGateVerdict(jdkOK, jdkHave, detectedAndroidSDKRoot())
		if len(missing) == 0 {
			return r
		}
		r.Missing = missing
		r.Installable = true // every android dep is Auto=true
		if !installDeps {
			r.OK = false
			r.Code = "deps_missing"
			r.Error = "Android build needs JDK 17 + Android SDK. " +
				"Re-invoke with installDeps:true to download & install them with your approval."
			return r
		}
		// Approved. installAndroidSDKRuntime installs OpenJDK 17 first
		// when java is missing, then the command-line tools + SDK
		// packages — one call covers both missing deps.
		if err := installAndroidSDKRuntime(ctx, true /* installDeps approval */, progress); err != nil {
			r.OK = false
			r.Code = "deps_install_failed"
			r.Error = "android dependency install failed: " + err.Error()
			return r
		}
		// Re-verify so we never green-light a build that will still
		// fail on a half-installed SDK.
		if jOK, _ := androidJDKOK(ctx); !jOK {
			r.OK = false
			r.Code = "deps_install_failed"
			r.Error = "JDK 17 still not usable after install"
			return r
		}
		if detectedAndroidSDKRoot() == "" {
			r.OK = false
			r.Code = "deps_install_failed"
			r.Error = "Android SDK still not detected after install"
			return r
		}
		for _, m := range missing {
			r.Installed = append(r.Installed, m.Name)
		}
		return r
	}
	return r
}

// preflightCLIError formats a failed preflight as a human, multi-line
// error for CLI surfaces (yaver wire push). Returns nil when pf.OK.
func preflightCLIError(pf preflightResult) error {
	if pf.OK {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", pf.Code, pf.Error)
	for _, m := range pf.Missing {
		fmt.Fprintf(&b, "\n  - %s: need %s", m.Name, m.Need)
		if m.Have != "" {
			fmt.Fprintf(&b, " (have: %s)", m.Have)
		}
		if m.Fix != "" {
			fmt.Fprintf(&b, "\n    fix: %s", m.Fix)
		}
	}
	for _, s := range pf.ManualSteps {
		if strings.TrimSpace(s) != "" {
			fmt.Fprintf(&b, "\n  · %s", s)
		}
	}
	if pf.Installable && pf.Code == "deps_missing" {
		b.WriteString("\n  → re-run with --install-deps to download & install these with your approval")
	}
	return fmt.Errorf("%s", b.String())
}

// preflightInitial renders a preflight verdict into the map shape the
// ops verbs put in OpsResult.Initial so callers (and the model) get the
// structured plan, not just an error string.
func preflightInitial(pf preflightResult) map[string]interface{} {
	m := map[string]interface{}{
		"preflight": map[string]interface{}{
			"class":       string(pf.Class),
			"hostOS":      pf.HostOS,
			"requiredOS":  pf.RequiredOS,
			"code":        pf.Code,
			"installable": pf.Installable,
		},
	}
	if len(pf.Missing) > 0 {
		m["missing"] = pf.Missing
	}
	if len(pf.ManualSteps) > 0 {
		m["manualSteps"] = pf.ManualSteps
	}
	if pf.Installable && pf.Code == "deps_missing" {
		m["hint"] = "re-invoke this verb with installDeps:true to install the missing dependencies with your approval"
	}
	return m
}
