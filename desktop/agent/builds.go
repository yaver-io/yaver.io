package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// BuildStatus represents the state of a build.
type BuildStatus string

const (
	BuildStatusRunning   BuildStatus = "running"
	BuildStatusCompleted BuildStatus = "completed"
	BuildStatusFailed    BuildStatus = "failed"
	BuildStatusCancelled BuildStatus = "cancelled"
)

// BuildPlatform identifies the build target.
type BuildPlatform string

const (
	PlatformFlutterAPK         BuildPlatform = "flutter-apk"
	PlatformFlutterAAB         BuildPlatform = "flutter-aab"
	PlatformFlutterIPA         BuildPlatform = "flutter-ipa"
	PlatformGradleAPK          BuildPlatform = "gradle-apk"
	PlatformGradleAAB          BuildPlatform = "gradle-aab"
	PlatformXcodeIPA           BuildPlatform = "xcode-ipa"
	PlatformXcodeBuild         BuildPlatform = "xcode-build"
	PlatformRNAndroid          BuildPlatform = "rn-android"
	PlatformRNIOS              BuildPlatform = "rn-ios"
	PlatformExpoAndroid        BuildPlatform = "expo-android"
	PlatformExpoIOS            BuildPlatform = "expo-ios"
	PlatformXcodeDeviceInstall BuildPlatform = "xcode-device-install"
	PlatformHermesBundlePush   BuildPlatform = "hermes-bundle-push"
	PlatformCustom             BuildPlatform = "custom"
)

// Build represents a build job with optional artifact.
type Build struct {
	ID              string        `json:"id"`
	Platform        BuildPlatform `json:"platform"`
	Command         string        `json:"command"`
	WorkDir         string        `json:"workDir"`
	Status          BuildStatus   `json:"status"`
	ExecID          string        `json:"execId,omitempty"`
	ArtifactPath    string        `json:"artifactPath,omitempty"`
	ArtifactName    string        `json:"artifactName,omitempty"`
	ArtifactSize    int64         `json:"artifactSize,omitempty"`
	ArtifactHash    string        `json:"artifactHash,omitempty"` // SHA256
	StartedAt       string        `json:"startedAt"`
	FinishedAt      string        `json:"finishedAt,omitempty"`
	ExitCode        *int          `json:"exitCode,omitempty"`
	Error           string        `json:"error,omitempty"`
	InstallOnDevice bool          `json:"installOnDevice,omitempty"`
	InstallStatus   string        `json:"installStatus,omitempty"` // "", "installing", "installed", "install_failed"
	InstallError    string        `json:"installError,omitempty"`
	DeviceUDID      string        `json:"deviceUDID,omitempty"`
}

// BuildSummary is returned by list (no large fields).
type BuildSummary struct {
	ID           string        `json:"id"`
	Platform     BuildPlatform `json:"platform"`
	Status       BuildStatus   `json:"status"`
	ArtifactName string        `json:"artifactName,omitempty"`
	ArtifactSize int64         `json:"artifactSize,omitempty"`
	StartedAt    string        `json:"startedAt"`
	FinishedAt   string        `json:"finishedAt,omitempty"`
}

// BuildManager manages build jobs and artifact tracking.
type BuildManager struct {
	mu      sync.RWMutex
	builds  map[string]*Build
	execMgr *ExecManager
	workDir string
}

// NewBuildManager creates a new build manager.
func NewBuildManager(execMgr *ExecManager, workDir string) *BuildManager {
	return &BuildManager{
		builds:  make(map[string]*Build),
		execMgr: execMgr,
		workDir: workDir,
	}
}

// resolveBuildCommand returns the shell command and expected artifact patterns for a platform.
func resolveBuildCommand(platform BuildPlatform, workDir string, extraArgs []string) (command string, artifactPatterns []string) {
	// Native build platforms (gradle-device-install, flutter-device-install)
	// live in native_build.go; check there first so they don't need to be
	// duplicated in the big switch below.
	if cmd, patterns, ok := resolveNativeBuildCommand(platform, workDir, extraArgs); ok {
		return cmd, patterns
	}

	extra := strings.Join(extraArgs, " ")
	if extra != "" {
		extra = " " + extra
	}

	gradleRoot := func(root string) string {
		if _, err := os.Stat(filepath.Join(root, "gradlew")); err == nil {
			return root
		}
		if _, err := os.Stat(filepath.Join(root, "android", "gradlew")); err == nil {
			return filepath.Join(root, "android")
		}
		return root
	}
	iosRoot := func(root string) string {
		if hasXcodeproj(filepath.Join(root, "ios")) {
			return filepath.Join(root, "ios")
		}
		return root
	}
	patternIn := func(base, dir, tail string) string {
		if rel, err := filepath.Rel(base, dir); err == nil && rel != "." {
			return filepath.ToSlash(filepath.Join(rel, tail))
		}
		return filepath.ToSlash(tail)
	}

	switch platform {
	case PlatformFlutterAPK:
		return "flutter build apk" + extra, []string{
			"build/app/outputs/flutter-apk/app-release.apk",
			"build/app/outputs/flutter-apk/app-debug.apk",
		}
	case PlatformFlutterAAB:
		return "flutter build appbundle" + extra, []string{
			"build/app/outputs/bundle/release/app-release.aab",
		}
	case PlatformFlutterIPA:
		return "flutter build ipa" + extra, []string{
			"build/ios/ipa/*.ipa",
		}
	case PlatformGradleAPK:
		projectDir := gradleRoot(workDir)
		gradlew := "./gradlew"
		if _, err := os.Stat(filepath.Join(projectDir, "gradlew")); err != nil {
			gradlew = "gradle"
		}
		task := "assembleRelease"
		if extra != "" {
			task = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("cd %s && JAVA_HOME=%s %s %s", shellQuote(projectDir), findJavaHome(), gradlew, task), []string{
			patternIn(workDir, projectDir, "app/build/outputs/apk/release/*.apk"),
			patternIn(workDir, projectDir, "app/build/outputs/apk/debug/*.apk"),
		}
	case PlatformGradleAAB:
		projectDir := gradleRoot(workDir)
		gradlew := "./gradlew"
		if _, err := os.Stat(filepath.Join(projectDir, "gradlew")); err != nil {
			gradlew = "gradle"
		}
		task := "bundleRelease"
		if strings.TrimSpace(extra) != "" {
			task = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("cd %s && JAVA_HOME=%s %s %s", shellQuote(projectDir), findJavaHome(), gradlew, task), []string{
			patternIn(workDir, projectDir, "app/build/outputs/bundle/release/*.aab"),
		}
	case PlatformXcodeIPA:
		projectDir := iosRoot(workDir)
		schemeOverride := ""
		if extra != "" {
			schemeOverride = strings.TrimSpace(extra)
			extra = ""
		}
		script := `set -e; ` +
			`WS=$(ls -1 *.xcworkspace 2>/dev/null | head -1 || true); ` +
			`PROJ=$(ls -1 *.xcodeproj 2>/dev/null | head -1 || true); ` +
			`if [ -n "$WS" ]; then FLAG="-workspace $WS"; SCHEME=$(basename "$WS" .xcworkspace); ` +
			`elif [ -n "$PROJ" ]; then FLAG="-project $PROJ"; SCHEME=$(basename "$PROJ" .xcodeproj); ` +
			`else echo "no .xcworkspace or .xcodeproj found" >&2; exit 1; fi; `
		if schemeOverride != "" {
			script += fmt.Sprintf("SCHEME=%q; ", schemeOverride)
		}
		script += `mkdir -p build/ipa; ` +
			`cat > build/ExportOptions.plist <<'EOF'` + "\n" +
			`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n" +
			`<plist version="1.0"><dict>` + "\n" +
			`  <key>destination</key><string>export</string>` + "\n" +
			`  <key>method</key><string>app-store-connect</string>` + "\n" +
			`  <key>signingStyle</key><string>automatic</string>` + "\n" +
			`</dict></plist>` + "\n" +
			`EOF` + "\n" +
			`xcodebuild archive $FLAG -scheme "$SCHEME" -configuration Release -archivePath build/App.xcarchive -allowProvisioningUpdates && ` +
			`xcodebuild -exportArchive -archivePath build/App.xcarchive -exportPath build/ipa -exportOptionsPlist build/ExportOptions.plist -allowProvisioningUpdates`
		return fmt.Sprintf("cd %s && %s", shellQuote(projectDir), script), []string{
			patternIn(workDir, projectDir, "build/ipa/*.ipa"),
		}
	case PlatformXcodeBuild:
		scheme := "App"
		if extra != "" {
			scheme = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("xcodebuild build -scheme %s -destination 'generic/platform=iOS' -quiet", scheme), nil
	case PlatformXcodeDeviceInstall:
		// For Expo/RN projects, `npx expo run:ios --device --configuration Release`
		// is the right command — it handles JS bundling, native prebuild, xcodebuild,
		// and device install in one step. Raw `xcodebuild build` alone produces a
		// .app with no embedded JS bundle → "no script url provided" crash.
		//
		// For bare Xcode projects (no package.json with expo), fall back to
		// xcodebuild + manual devicectl install.
		pkgData, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
		isExpo := strings.Contains(string(pkgData), `"expo"`)
		isRN := strings.Contains(string(pkgData), `"react-native"`)

		if isExpo {
			// `expo run:ios --device` bundles JS, builds with Xcode, and installs
			// on the connected device — exactly what a developer does manually.
			// --no-install: we handle install + launch ourselves via devicectl
			// so we can track the artifact and report status to the phone.
			cmd := "npx expo run:ios --configuration Release --no-install"
			if extra != "" {
				cmd += " " + strings.TrimSpace(extra)
			}
			return cmd, []string{
				"ios/build/Build/Products/Release-iphoneos/*.app",
				"ios/build/DerivedData/Build/Products/Release-iphoneos/*.app",
			}
		} else if isRN {
			// Bare React Native (no Expo): bundle JS first, then xcodebuild.
			iosSub := "ios"
			if _, err := os.Stat(filepath.Join(workDir, "ios")); err != nil {
				iosSub = "."
			}
			bundleCmd := fmt.Sprintf(
				"npx react-native bundle --platform ios --dev false --entry-file index.js "+
					"--bundle-output %s/main.jsbundle --assets-dest %s && ",
				iosSub, iosSub)
			script := `set -e; ` +
				`WS=$(ls -1 *.xcworkspace 2>/dev/null | head -1 || true); ` +
				`PROJ=$(ls -1 *.xcodeproj 2>/dev/null | head -1 || true); ` +
				`if [ -n "$WS" ]; then FLAG="-workspace $WS"; SCHEME=$(basename "$WS" .xcworkspace); ` +
				`elif [ -n "$PROJ" ]; then FLAG="-project $PROJ"; SCHEME=$(basename "$PROJ" .xcodeproj); ` +
				`else echo "no .xcworkspace or .xcodeproj found" >&2; exit 1; fi; ` +
				`xcodebuild build $FLAG -scheme "$SCHEME" ` +
				`-destination 'generic/platform=iOS' ` +
				`-derivedDataPath build/DerivedData ` +
				`-configuration Release ` +
				`-allowProvisioningUpdates`
			return fmt.Sprintf("%scd %s && %s", bundleCmd, iosSub, script), []string{
				"ios/build/DerivedData/Build/Products/Release-iphoneos/*.app",
				"build/DerivedData/Build/Products/Release-iphoneos/*.app",
			}
		}

		// Bare Xcode project (no RN/Expo): raw xcodebuild
		iosSub := "ios"
		if _, err := os.Stat(filepath.Join(workDir, "ios")); err != nil {
			iosSub = "."
		}
		extraEsc := strings.TrimSpace(extra)
		schemeOverride := ""
		if extraEsc != "" {
			schemeOverride = extraEsc
			extra = ""
		}
		script := `set -e; ` +
			`WS=$(ls -1 *.xcworkspace 2>/dev/null | head -1 || true); ` +
			`PROJ=$(ls -1 *.xcodeproj 2>/dev/null | head -1 || true); ` +
			`if [ -n "$WS" ]; then FLAG="-workspace $WS"; SCHEME=$(basename "$WS" .xcworkspace); ` +
			`elif [ -n "$PROJ" ]; then FLAG="-project $PROJ"; SCHEME=$(basename "$PROJ" .xcodeproj); ` +
			`else echo "no .xcworkspace or .xcodeproj found" >&2; exit 1; fi; `
		if schemeOverride != "" {
			script += fmt.Sprintf("SCHEME=%q; ", schemeOverride)
		}
		script += `xcodebuild build $FLAG -scheme "$SCHEME" ` +
			`-destination 'generic/platform=iOS' ` +
			`-derivedDataPath build/DerivedData ` +
			`-configuration Release ` +
			`-allowProvisioningUpdates`
		return fmt.Sprintf("cd %s && %s", iosSub, script), []string{
			"ios/build/DerivedData/Build/Products/Release-iphoneos/*.app",
			"build/DerivedData/Build/Products/Release-iphoneos/*.app",
		}
	case PlatformHermesBundlePush:
		// JS bundle for Hermes push — detect Expo vs bare RN, bundle to .yaver-build/
		// hermesc compilation + serving happens as a post-build step in monitorBuild.
		buildDir := filepath.Join(workDir, ".yaver-build")
		bundlePath := filepath.Join(buildDir, "main.jsbundle")
		assetsDir := filepath.Join(buildDir, "assets")
		// Detect project type from package.json
		pkgData, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
		isExpo := strings.Contains(string(pkgData), `"expo"`)
		var cmd string
		if isExpo {
			cmd = fmt.Sprintf("mkdir -p %s && npx expo export:embed --platform ios --bundle-output %s --assets-dest %s --dev false --minify true --reset-cache",
				buildDir, bundlePath, assetsDir)
		} else {
			entryFile := "index.js"
			var pkg struct {
				Main string `json:"main"`
			}
			json.Unmarshal(pkgData, &pkg)
			if pkg.Main != "" {
				entryFile = pkg.Main
			}
			cmd = fmt.Sprintf("mkdir -p %s && npx react-native bundle --platform ios --entry-file %s --bundle-output %s --assets-dest %s --dev false --minify true --reset-cache",
				buildDir, entryFile, bundlePath, assetsDir)
		}
		return cmd + extra, []string{
			".yaver-build/main.jsbundle",
		}
	case PlatformRNAndroid:
		return "cd android && ./gradlew assembleRelease" + extra, []string{
			"android/app/build/outputs/apk/release/*.apk",
		}
	case PlatformRNIOS:
		return "cd ios && xcodebuild -workspace *.xcworkspace -scheme App -configuration Release -archivePath build/App.xcarchive archive" + extra, []string{
			"ios/build/*.ipa",
		}
	case PlatformExpoAndroid:
		return "npx expo run:android --variant release" + extra, []string{
			"android/app/build/outputs/apk/release/*.apk",
		}
	case PlatformExpoIOS:
		return "npx expo run:ios --configuration Release" + extra, []string{
			"ios/build/Build/Products/Release-iphoneos/*.app",
		}
	case PlatformCustom:
		return strings.TrimSpace(extra), nil
	default:
		return "", nil
	}
}

// StartBuild starts a new build for the given platform.
func (bm *BuildManager) StartBuild(platform BuildPlatform, workDir string, extraArgs []string, installOnDevice ...bool) (*Build, error) {
	if workDir == "" {
		workDir = bm.workDir
	}

	command, artifactPatterns := resolveBuildCommand(platform, workDir, extraArgs)
	if command == "" {
		return nil, fmt.Errorf("unknown build platform: %s", platform)
	}

	// Start via ExecManager (1 hour timeout for builds)
	session, err := bm.execMgr.StartExec(command, workDir, "", nil, 3600)
	if err != nil {
		return nil, fmt.Errorf("start build: %w", err)
	}

	wantInstall := len(installOnDevice) > 0 && installOnDevice[0]
	build := &Build{
		ID:              uuid.New().String()[:8],
		Platform:        platform,
		Command:         command,
		WorkDir:         workDir,
		Status:          BuildStatusRunning,
		ExecID:          session.ID,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
		InstallOnDevice: wantInstall,
	}

	bm.mu.Lock()
	bm.builds[build.ID] = build
	bm.mu.Unlock()

	// Monitor build completion in background
	go bm.monitorBuild(build, session, artifactPatterns)

	return build, nil
}

// monitorBuild waits for exec to finish, then detects artifacts.
func (bm *BuildManager) monitorBuild(build *Build, session *ExecSession, patterns []string) {
	// Wait for exec to complete
	<-session.doneCh

	session.mu.RLock()
	exitCode := session.ExitCode
	status := session.Status
	session.mu.RUnlock()

	bm.mu.Lock()
	defer bm.mu.Unlock()

	build.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	build.ExitCode = exitCode

	if status == ExecStatusCompleted && (exitCode == nil || *exitCode == 0) {
		build.Status = BuildStatusCompleted
		// Try to detect artifact
		if artifact := detectArtifact(build.WorkDir, patterns); artifact != "" {
			if err := populateArtifactInfo(build, artifact); err != nil {
				log.Printf("[build] artifact info error: %v", err)
			}
		}

		// Post-build: native xcode install or hermes bundle push
		if build.InstallOnDevice && build.ArtifactPath != "" {
			if build.Platform == PlatformHermesBundlePush {
				// Hermes bytecode compilation + serve via /dev/native-bundle
				build.InstallStatus = "compiling_hermes"
				log.Printf("[build] %s compiling Hermes bytecode...", build.ID)
				bm.mu.Unlock()

				hermesErr := compileHermesBundle(build.ArtifactPath)

				bm.mu.Lock()
				if hermesErr != nil {
					// Not fatal — plain JS bundle still works, just slower
					log.Printf("[build] %s hermesc failed (using plain JS): %v", build.ID, hermesErr)
				}
				build.InstallStatus = "bundle_ready"
				log.Printf("[build] %s Hermes bundle ready at %s", build.ID, build.ArtifactPath)
			} else {
				// Native device install (iOS .app via xcrun devicectl, Android .apk via adb,
				// Flutter falls through to whichever artifact extension was produced).
				build.InstallStatus = "installing"
				log.Printf("[build] %s installing on device (platform=%s)...", build.ID, build.Platform)
				bm.mu.Unlock()

				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				deviceID, installErr := dispatchDeviceInstall(ctx, build.Platform, build.ArtifactPath)
				cancel()

				bm.mu.Lock()
				build.DeviceUDID = deviceID
				if installErr != nil {
					build.InstallStatus = "install_failed"
					build.InstallError = installErr.Error()
					log.Printf("[build] %s install failed: %v", build.ID, installErr)
				} else {
					build.InstallStatus = "installed"
					tag := deviceID
					if len(tag) > 8 {
						tag = tag[:8]
					}
					log.Printf("[build] %s installed on device %s", build.ID, tag)

					// Best-effort auto-launch so the user doesn't have to tap the icon.
					bm.mu.Unlock()
					launchCtx, launchCancel := context.WithTimeout(context.Background(), 30*time.Second)
					launchErr := dispatchAutoLaunch(launchCtx, build.Platform, deviceID, build.ArtifactPath)
					launchCancel()
					bm.mu.Lock()
					if launchErr != nil {
						log.Printf("[build] %s launch failed (install still succeeded): %v", build.ID, launchErr)
					}
				}
			}
		}
	} else {
		build.Status = BuildStatusFailed
		if exitCode != nil {
			build.Error = fmt.Sprintf("exit code %d", *exitCode)
		}
	}

	log.Printf("[build] %s finished: status=%s artifact=%s install=%s", build.ID, build.Status, build.ArtifactName, build.InstallStatus)
}

// detectArtifact searches for build artifacts matching the given glob patterns.
func detectArtifact(workDir string, patterns []string) string {
	for _, pattern := range patterns {
		fullPattern := filepath.Join(workDir, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			continue
		}
		if len(matches) > 0 {
			// Return the newest match
			sort.Slice(matches, func(i, j int) bool {
				fi, _ := os.Stat(matches[i])
				fj, _ := os.Stat(matches[j])
				if fi == nil || fj == nil {
					return false
				}
				return fi.ModTime().After(fj.ModTime())
			})
			return matches[0]
		}
	}
	return ""
}

// populateArtifactInfo fills in artifact path, name, size, and SHA256 hash.
func populateArtifactInfo(build *Build, artifactPath string) error {
	fi, err := os.Stat(artifactPath)
	if err != nil {
		return fmt.Errorf("stat artifact: %w", err)
	}

	hash, err := hashFile(artifactPath)
	if err != nil {
		return fmt.Errorf("hash artifact: %w", err)
	}

	build.ArtifactPath = artifactPath
	build.ArtifactName = fi.Name()
	build.ArtifactSize = fi.Size()
	build.ArtifactHash = hash
	return nil
}

// hashFile computes SHA256 of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// RegisterArtifact manually registers a pre-built artifact (for yaver deploy --file).
func (bm *BuildManager) RegisterArtifact(artifactPath string, platform BuildPlatform) (*Build, error) {
	fi, err := os.Stat(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}

	hash, err := hashFile(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("hash file: %w", err)
	}

	build := &Build{
		ID:           uuid.New().String()[:8],
		Platform:     platform,
		Command:      "(manual)",
		WorkDir:      filepath.Dir(artifactPath),
		Status:       BuildStatusCompleted,
		ArtifactPath: artifactPath,
		ArtifactName: fi.Name(),
		ArtifactSize: fi.Size(),
		ArtifactHash: hash,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		FinishedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	bm.mu.Lock()
	bm.builds[build.ID] = build
	bm.mu.Unlock()

	return build, nil
}

// GetBuild returns a build by ID.
func (bm *BuildManager) GetBuild(id string) (*Build, bool) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	b, ok := bm.builds[id]
	return b, ok
}

// ListBuilds returns summaries of all builds, newest first.
func (bm *BuildManager) ListBuilds() []BuildSummary {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	result := make([]BuildSummary, 0, len(bm.builds))
	for _, b := range bm.builds {
		result = append(result, BuildSummary{
			ID:           b.ID,
			Platform:     b.Platform,
			Status:       b.Status,
			ArtifactName: b.ArtifactName,
			ArtifactSize: b.ArtifactSize,
			StartedAt:    b.StartedAt,
			FinishedAt:   b.FinishedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt > result[j].StartedAt
	})
	return result
}

// CancelBuild kills a running build.
func (bm *BuildManager) CancelBuild(id string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	build, ok := bm.builds[id]
	if !ok {
		return fmt.Errorf("build %q not found", id)
	}
	if build.Status != BuildStatusRunning {
		return fmt.Errorf("build %q is not running (status: %s)", id, build.Status)
	}

	if build.ExecID != "" {
		bm.execMgr.KillExec(build.ExecID)
	}
	build.Status = BuildStatusCancelled
	build.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// WatchDir polls a directory for new artifacts matching patterns.
// Returns a channel that emits the Build when a new artifact is detected.
// Call the returned cancel func to stop watching.
func (bm *BuildManager) WatchDir(dir string, patterns []string, platform BuildPlatform) (chan *Build, func()) {
	ch := make(chan *Build, 8)
	done := make(chan struct{})
	seen := make(map[string]time.Time)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				for _, pattern := range patterns {
					matches, _ := filepath.Glob(filepath.Join(dir, pattern))
					for _, m := range matches {
						fi, err := os.Stat(m)
						if err != nil {
							continue
						}
						prevMod, known := seen[m]
						if !known || fi.ModTime().After(prevMod) {
							seen[m] = fi.ModTime()
							if known {
								// File was updated — register as artifact
								build, err := bm.RegisterArtifact(m, platform)
								if err == nil {
									log.Printf("[build] Watched artifact: %s (%d bytes)", build.ArtifactName, build.ArtifactSize)
									ch <- build
								}
							}
						}
					}
				}
			}
		}
	}()

	cancel := func() { close(done) }
	return ch, cancel
}

// compileHermesBundle compiles a JS bundle to Hermes bytecode in-place.
// Uses the embedded hermesc (matching Yaver app's exact Hermes version).
// Falls back to project-local hermesc if embedded is unavailable.
// Returns nil if hermesc is not available (plain JS bundle still works).
//
// As of the secondary-reload optimisation work, this function consults
// the HBC content cache (hbc_cache.go) BEFORE running hermesc. Cache
// hit → skip hermesc and write the cached bytecode in place. Cache
// miss → run hermesc as today and store the result for next time.
//
// The cache is purely OPTIMISATIONAL — any cache failure (lookup,
// validation, write, panic) silently falls through to the original
// hermesc path. See docs/hermes-secondary-reload-optimization.md §13.
func compileHermesBundle(bundlePath string) error {
	return compileHermesBundleWithOpts(bundlePath, HBCCacheCompileOpts{
		OptLevel: "O", // current behaviour: -O always
	})
}

// compileHermesBundleWithOpts is the cache-aware variant. Today's only
// caller of compileHermesBundle uses default opts; future callers (the
// dev-iteration path with -O0) will pass opts explicitly.
//
// Named return `err` is referenced from the deferred cache-store hook
// so we only persist after a successful compile.
func compileHermesBundleWithOpts(bundlePath string, opts HBCCacheCompileOpts) (err error) {
	hermescPath, err := resolveHermesc(filepath.Dir(filepath.Dir(bundlePath)))
	if err != nil {
		return fmt.Errorf("hermesc not available: %w", err)
	}

	// Layer 0 — content-hash cache hit. The wrapper is failure-tolerant:
	// if anything goes sideways inside the cache path, hit==false and
	// we fall through to the existing hermesc invocation untouched.
	hit, key := tryServeFromHBCCache(bundlePath, hermescPath, opts)
	if hit {
		return nil
	}
	// Miss — key still carries the inputs we'd want to file the post-
	// compile output under. Defer the store; runs only when the
	// outer function returns nil (compile succeeded).
	defer func() {
		if err == nil {
			storeHBCCacheAfterCompile(bundlePath, key, opts)
		}
	}()

	tmpPath := bundlePath + ".tmp"
	if rerr := os.Rename(bundlePath, tmpPath); rerr != nil {
		return fmt.Errorf("rename for hermesc: %w", rerr)
	}

	hermescOpt := opts.OptLevel
	if hermescOpt == "" {
		hermescOpt = "O"
	}
	args := []string{"-emit-binary", "-out", bundlePath, "-" + hermescOpt, tmpPath}
	if opts.EmitSourceMap {
		args = append(args, "-output-source-map")
	}
	cmd := exec.Command(hermescPath, args...)
	out, cerr := cmd.CombinedOutput()
	if cerr != nil {
		// Restore original JS bundle so the caller doesn't end up with
		// an empty bundlePath when hermesc fails.
		os.Rename(tmpPath, bundlePath)
		return fmt.Errorf("hermesc failed: %v\n%s", cerr, string(out))
	}

	os.Remove(tmpPath)
	log.Printf("[build] hermesc compile complete: %s", bundlePath)
	return nil
}

// findHermescInProject looks for hermesc in the project's react-native installation.
func findHermescInProject(workDir string) string {
	return findProjectHermesc(workDir)
}
