package main

import (
	"encoding/json"
	"fmt"
	osexec "os/exec"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Apple App Store Connect — uses Xcode CLI tools + App Store Connect API
// ---------------------------------------------------------------------------

func mcpAppStoreStatus(bundleID string) interface{} {
	// Use xcrun altool or app-store-connect CLI
	out, err := runCmd("xcrun", "altool", "--list-apps", "--output-format", "json",
		"--apiKey", os.Getenv("APP_STORE_API_KEY_ID"),
		"--apiIssuer", os.Getenv("APP_STORE_API_ISSUER"))
	if err != nil {
		// Fallback: try fastlane
		out, err = runCmd("fastlane", "deliver", "download_metadata", "--skip_screenshots")
		if err != nil {
			return map[string]interface{}{
				"error": "Requires App Store Connect API key or fastlane. Set APP_STORE_API_KEY_ID and APP_STORE_API_ISSUER env vars.",
				"setup": "https://developer.apple.com/documentation/appstoreconnectapi/creating_api_keys_for_app_store_connect_api",
			}
		}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return result
}

func mcpAppStoreTestFlight(bundleID string) interface{} {
	// List TestFlight builds
	out, err := runCmd("xcrun", "altool", "--list-builds", "--bundle-id", bundleID,
		"--output-format", "json",
		"--apiKey", os.Getenv("APP_STORE_API_KEY_ID"),
		"--apiIssuer", os.Getenv("APP_STORE_API_ISSUER"))
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("xcrun altool: %s — %s", err, out)}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return result
}

func mcpXcodeBuild(dir, scheme, destination string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if scheme == "" {
		// Auto-detect scheme from .xcodeproj
		schemes, _ := runCmd("xcodebuild", "-list", "-json")
		return map[string]interface{}{"schemes": schemes, "note": "Specify a scheme to build"}
	}
	if destination == "" {
		destination = "generic/platform=iOS"
	}
	cmd := osexec.Command("xcodebuild", "build", "-scheme", scheme, "-destination", destination, "-quiet")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "scheme": scheme, "output": string(out)}
}

func mcpXcodeTest(dir, scheme, destination string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if destination == "" {
		destination = "platform=iOS Simulator,name=iPhone 16"
	}
	args := []string{"test", "-scheme", scheme, "-destination", destination, "-quiet", "-resultBundlePath", "/tmp/yaver-test-results"}
	cmd := osexec.Command("xcodebuild", args...)
	cmd.Dir = dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	result := map[string]interface{}{
		"output":   string(out),
		"duration": duration.String(),
		"passed":   err == nil,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func mcpSimulators() interface{} {
	out, err := runCmd("xcrun", "simctl", "list", "devices", "--json")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return result
}

func mcpSimulatorBoot(deviceName string) interface{} {
	out, err := runCmd("xcrun", "simctl", "boot", deviceName)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("%s — %s", err, out)}
	}
	return map[string]interface{}{"ok": true, "device": deviceName}
}

func mcpSimulatorScreenshot(deviceID string) interface{} {
	if deviceID == "" {
		deviceID = "booted"
	}
	path := fmt.Sprintf("/tmp/sim-screenshot-%d.png", time.Now().Unix())
	_, err := runCmd("xcrun", "simctl", "io", deviceID, "screenshot", path)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"path": path, "device": deviceID}
}

// ---------------------------------------------------------------------------
// Google Play Console — uses bundletool + google-play CLI
// ---------------------------------------------------------------------------

func mcpPlayStoreStatus(packageName string) interface{} {
	// Try using Google Play Developer API via gcloud or dedicated CLI
	// Most devs use fastlane supply
	out, err := runCmd("fastlane", "supply", "init", "--json_key", findGooglePlayKey(), "--package_name", packageName)
	if err != nil {
		return map[string]interface{}{
			"error": "Requires fastlane + Google Play service account key.",
			"setup": "1. Create service account in Google Cloud Console\n2. Grant access in Google Play Console\n3. Set GOOGLE_PLAY_JSON_KEY env var or place key at keys/google-play-service-account.json",
			"package": packageName,
		}
	}
	return map[string]interface{}{"output": out}
}

func mcpPlayStoreTrack(packageName, track string) interface{} {
	if track == "" {
		track = "production"
	}
	keyPath := findGooglePlayKey()
	out, err := runCmd("fastlane", "supply", "download_metadata",
		"--json_key", keyPath,
		"--package_name", packageName,
		"--track", track)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"track": track, "output": out}
}

func mcpGradleBuild(dir, task string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if task == "" {
		task = "assembleDebug"
	}
	gradlew := filepath.Join(dir, "gradlew")
	if _, err := os.Stat(gradlew); err != nil {
		gradlew = "gradle"
	}
	cmd := osexec.Command(gradlew, task)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("JAVA_HOME=%s", findJavaHome()))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	result := map[string]interface{}{
		"output":   string(out),
		"duration": duration.String(),
		"task":     task,
		"passed":   err == nil,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func mcpGradleTest(dir string) interface{} {
	return mcpGradleBuild(dir, "testDebugUnitTest")
}

func mcpAndroidLint(dir string) interface{} {
	return mcpGradleBuild(dir, "lintDebug")
}

func mcpEmulators() interface{} {
	out, err := runCmd("emulator", "-list-avds")
	if err != nil {
		out, err = runCmd(filepath.Join(os.Getenv("ANDROID_HOME"), "emulator", "emulator"), "-list-avds")
		if err != nil {
			return map[string]interface{}{"error": "Android emulator not found. Set ANDROID_HOME."}
		}
	}
	avds := strings.Split(strings.TrimSpace(out), "\n")
	return map[string]interface{}{"emulators": avds, "count": len(avds)}
}

func findGooglePlayKey() string {
	paths := []string{
		os.Getenv("GOOGLE_PLAY_JSON_KEY"),
		"keys/google-play-service-account.json",
		"../keys/google-play-service-account.json",
		filepath.Join(os.Getenv("HOME"), ".config/gcloud/google-play-key.json"),
	}
	for _, p := range paths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return "google-play-service-account.json"
}

func findJavaHome() string {
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		return jh
	}
	out, err := runCmd("/usr/libexec/java_home", "-v", "17")
	if err == nil {
		return strings.TrimSpace(out)
	}
	return "/usr/lib/jvm/java-17"
}

// ---------------------------------------------------------------------------
// Firebase — wraps firebase CLI
// ---------------------------------------------------------------------------

func mcpFirebaseProjects() interface{} {
	out, err := runCmd("firebase", "projects:list", "--json")
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("firebase CLI: %s (install: npm install -g firebase-tools)", out)}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return result
}

func mcpFirebaseDeploy(dir, only string) interface{} {
	args := []string{"deploy"}
	if only != "" {
		args = append(args, "--only", only)
	}
	args = append(args, "--json")
	cmd := osexec.Command("firebase", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

func mcpFirebaseCrashlytics(projectID string) interface{} {
	// Firebase Crashlytics via gcloud
	out, err := runCmd("gcloud", "firebase", "crashlytics", "issues", "list",
		"--project", projectID, "--format", "json")
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("gcloud: %s", out), "note": "Requires: gcloud auth login && gcloud config set project <id>"}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return result
}

// ---------------------------------------------------------------------------
// React Native / Expo
// ---------------------------------------------------------------------------

func mcpExpoStatus(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cmd := osexec.Command("npx", "expo", "config", "--type", "public")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": string(out)}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

func mcpExpoBuild(dir, platform string) interface{} {
	if platform == "" {
		platform = "ios"
	}
	cmd := osexec.Command("eas", "build", "--platform", platform, "--non-interactive", "--json")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

func mcpEASSubmit(dir, platform string) interface{} {
	if platform == "" {
		platform = "ios"
	}
	cmd := osexec.Command("eas", "submit", "--platform", platform, "--non-interactive", "--json")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

// ---------------------------------------------------------------------------
// Flutter
// ---------------------------------------------------------------------------

func mcpFlutterDoctor() interface{} {
	out, err := runCmd("flutter", "doctor", "-v")
	wd, _ := os.Getwd()
	runnerChecks := []map[string]interface{}{}
	for _, r := range []struct {
		ID   string
		Name string
		Cmd  string
	}{
		{ID: "claude", Name: "Claude Code", Cmd: "claude"},
		{ID: "codex", Name: "OpenAI Codex", Cmd: "codex"},
		{ID: "opencode", Name: "OpenCode", Cmd: "opencode"},
	} {
		path, lookErr := osexec.LookPath(r.Cmd)
		entry := map[string]interface{}{
			"id":        r.ID,
			"name":      r.Name,
			"installed": lookErr == nil,
		}
		if lookErr != nil {
			entry["status"] = "missing"
			entry["detail"] = "Not installed"
			runnerChecks = append(runnerChecks, entry)
			continue
		}
		cfg := GetRunnerConfig(r.ID)
		version := ""
		if verOut, verErr := osexec.Command(r.Cmd, "--version").CombinedOutput(); verErr == nil {
			version = strings.TrimSpace(strings.Split(string(verOut), "\n")[0])
			if len(version) > 60 {
				version = version[:60]
			}
		}
		level, detail := runnerDoctorDetail(cfg, wd, path, version)
		entry["status"] = level
		entry["detail"] = detail
		entry["path"] = path
		status := DetectRunnerRuntimeStatus(cfg, wd)
		entry["ready"] = status.Ready
		entry["authConfigured"] = status.AuthConfigured
		if status.AuthSource != "" {
			entry["authSource"] = status.AuthSource
		}
		if status.Warning != "" {
			entry["warning"] = status.Warning
		}
		if status.Error != "" {
			entry["error"] = status.Error
		}
		runnerChecks = append(runnerChecks, entry)
	}
	result := map[string]interface{}{
		"output":        out,
		"runner_status": runnerChecks,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func mcpFlutterBuild(dir, platform string) interface{} {
	if platform == "" {
		platform = "apk"
	}
	args := []string{"build", platform}
	cmd := osexec.Command("flutter", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	result := map[string]interface{}{
		"output":   string(out),
		"duration": duration.String(),
		"platform": platform,
		"passed":   err == nil,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func mcpFlutterTest(dir string) interface{} {
	cmd := osexec.Command("flutter", "test", "--reporter", "compact")
	if dir != "" {
		cmd.Dir = dir
	}
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	result := map[string]interface{}{
		"output":   string(out),
		"duration": duration.String(),
		"passed":   err == nil,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

// ---------------------------------------------------------------------------
// CocoaPods
// ---------------------------------------------------------------------------

func mcpPodInstall(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cmd := osexec.Command("pod", "install")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

func mcpPodOutdated(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cmd := osexec.Command("pod", "outdated")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return map[string]interface{}{"outdated": string(out)}
}

// ---------------------------------------------------------------------------
// App review guidelines quick check
// ---------------------------------------------------------------------------

func mcpAppReviewCheck(platform string) interface{} {
	guidelines := map[string]interface{}{
		"ios": map[string]interface{}{
			"common_rejections": []string{
				"Crashes or bugs during review",
				"Broken links or placeholder content",
				"Missing privacy policy URL",
				"Requesting unnecessary permissions",
				"In-app purchase issues (must use Apple IAP for digital goods)",
				"Incomplete metadata (screenshots, description)",
				"Login required but no demo account provided",
				"Guideline 4.3 — Spam/duplicate app",
				"Guideline 2.1 — App completeness",
				"Guideline 5.1.1 — Data collection without purpose string",
			},
			"checklist": []string{
				"Privacy policy URL set in App Store Connect",
				"NSPrivacyAccessedAPITypes declared in PrivacyInfo.xcprivacy",
				"All permission usage descriptions filled (NSCameraUsageDescription, etc.)",
				"Demo account credentials provided in review notes",
				"No placeholder content, all links working",
				"App tested on latest iOS version",
				"Screenshots match current UI",
				"No private API usage",
			},
			"guidelines_url": "https://developer.apple.com/app-store/review/guidelines/",
		},
		"android": map[string]interface{}{
			"common_rejections": []string{
				"Policy violation: data safety form incomplete",
				"Missing privacy policy",
				"Deceptive behavior or misleading metadata",
				"Target API level too low (must target latest -1)",
				"Missing content rating questionnaire",
				"Permissions not justified in Data Safety section",
				"Background location without approved use case",
				"Crashes on review devices",
			},
			"checklist": []string{
				"Data Safety form completed in Play Console",
				"Privacy policy URL set",
				"Content rating questionnaire filled",
				"Target SDK is current (Android 14 / API 34+)",
				"All permissions justified in manifest and Data Safety",
				"AAB format (not APK) for Play Store",
				"No cleartext HTTP traffic (usesCleartextTraffic=false)",
				"ProGuard/R8 rules don't break functionality",
			},
			"guidelines_url": "https://play.google.com/about/developer-content-policy/",
		},
	}

	if platform == "" {
		return guidelines
	}
	if g, ok := guidelines[strings.ToLower(platform)]; ok {
		return g
	}
	return map[string]interface{}{"error": "platform must be: ios or android"}
}
