package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
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
	PlatformFlutterAPK BuildPlatform = "flutter-apk"
	PlatformFlutterAAB BuildPlatform = "flutter-aab"
	PlatformFlutterIPA BuildPlatform = "flutter-ipa"
	PlatformGradleAPK  BuildPlatform = "gradle-apk"
	PlatformGradleAAB  BuildPlatform = "gradle-aab"
	PlatformXcodeIPA   BuildPlatform = "xcode-ipa"
	PlatformXcodeBuild BuildPlatform = "xcode-build"
	PlatformRNAndroid  BuildPlatform = "rn-android"
	PlatformRNIOS      BuildPlatform = "rn-ios"
	PlatformExpoAndroid BuildPlatform = "expo-android"
	PlatformExpoIOS     BuildPlatform = "expo-ios"
	PlatformXcodeDeviceInstall BuildPlatform = "xcode-device-install"
	PlatformCustom             BuildPlatform = "custom"
)

// Build represents a build job with optional artifact.
type Build struct {
	ID           string        `json:"id"`
	Platform     BuildPlatform `json:"platform"`
	Command      string        `json:"command"`
	WorkDir      string        `json:"workDir"`
	Status       BuildStatus   `json:"status"`
	ExecID       string        `json:"execId,omitempty"`
	ArtifactPath string        `json:"artifactPath,omitempty"`
	ArtifactName string        `json:"artifactName,omitempty"`
	ArtifactSize int64         `json:"artifactSize,omitempty"`
	ArtifactHash string        `json:"artifactHash,omitempty"` // SHA256
	StartedAt      string        `json:"startedAt"`
	FinishedAt     string        `json:"finishedAt,omitempty"`
	ExitCode       *int          `json:"exitCode,omitempty"`
	Error          string        `json:"error,omitempty"`
	InstallOnDevice bool         `json:"installOnDevice,omitempty"`
	InstallStatus   string       `json:"installStatus,omitempty"` // "", "installing", "installed", "install_failed"
	InstallError    string       `json:"installError,omitempty"`
	DeviceUDID      string       `json:"deviceUDID,omitempty"`
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
	extra := strings.Join(extraArgs, " ")
	if extra != "" {
		extra = " " + extra
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
		gradlew := "./gradlew"
		if _, err := os.Stat(filepath.Join(workDir, "gradlew")); err != nil {
			gradlew = "gradle"
		}
		task := "assembleRelease"
		if extra != "" {
			task = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("JAVA_HOME=%s %s %s", findJavaHome(), gradlew, task), []string{
			"app/build/outputs/apk/release/*.apk",
			"app/build/outputs/apk/debug/*.apk",
		}
	case PlatformGradleAAB:
		gradlew := "./gradlew"
		if _, err := os.Stat(filepath.Join(workDir, "gradlew")); err != nil {
			gradlew = "gradle"
		}
		return fmt.Sprintf("JAVA_HOME=%s %s bundleRelease", findJavaHome(), gradlew), []string{
			"app/build/outputs/bundle/release/*.aab",
		}
	case PlatformXcodeIPA:
		scheme := "App"
		if extra != "" {
			scheme = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("xcodebuild -scheme %s -archivePath build/App.xcarchive archive && xcodebuild -exportArchive -archivePath build/App.xcarchive -exportPath build/ipa", scheme), []string{
			"build/ipa/*.ipa",
		}
	case PlatformXcodeBuild:
		scheme := "App"
		if extra != "" {
			scheme = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("xcodebuild build -scheme %s -destination 'generic/platform=iOS' -quiet", scheme), nil
	case PlatformXcodeDeviceInstall:
		scheme := "App"
		if extra != "" {
			scheme = strings.TrimSpace(extra)
			extra = ""
		}
		// Build for device with derived data. detectIOSDevice + install happens post-build.
		// Detect workspace vs project
		wsFlag := fmt.Sprintf("-scheme %s", scheme)
		return fmt.Sprintf("xcodebuild build %s -destination 'generic/platform=iOS' -derivedDataPath build/DerivedData -configuration Release -allowProvisioningUpdates", wsFlag), []string{
			"build/DerivedData/Build/Products/Release-iphoneos/*.app",
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

		// Direct device install (xcode-device-install platform)
		if build.InstallOnDevice && build.ArtifactPath != "" {
			build.InstallStatus = "installing"
			log.Printf("[build] %s installing on device...", build.ID)
			bm.mu.Unlock() // unlock during install (can take time)

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			udid, installErr := installAppOnDevice(ctx, build.ArtifactPath)
			cancel()

			bm.mu.Lock() // re-lock
			build.DeviceUDID = udid
			if installErr != nil {
				build.InstallStatus = "install_failed"
				build.InstallError = installErr.Error()
				log.Printf("[build] %s install failed: %v", build.ID, installErr)
			} else {
				build.InstallStatus = "installed"
				log.Printf("[build] %s installed on device %s", build.ID, udid[:8])
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
