package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBuildCommand(t *testing.T) {
	tests := []struct {
		platform BuildPlatform
		wantCmd  string
		wantPats int // expected number of artifact patterns
	}{
		{PlatformFlutterAPK, "flutter build apk", 2},
		{PlatformFlutterAAB, "flutter build appbundle", 1},
		{PlatformFlutterIPA, "flutter build ipa", 1},
		{PlatformRNAndroid, "cd android && ./gradlew assembleRelease", 1},
	}

	for _, tt := range tests {
		cmd, pats := resolveBuildCommand(tt.platform, "/tmp", nil)
		if !strings.Contains(cmd, tt.wantCmd) {
			t.Errorf("%s: expected command containing %q, got %q", tt.platform, tt.wantCmd, cmd)
		}
		if len(pats) != tt.wantPats {
			t.Errorf("%s: expected %d patterns, got %d", tt.platform, tt.wantPats, len(pats))
		}
	}
}

func TestResolveBuildCommandXcodeIPA(t *testing.T) {
	cmd, pats := resolveBuildCommand(PlatformXcodeIPA, "/tmp", nil)
	if len(pats) != 1 {
		t.Fatalf("xcode-ipa: expected 1 artifact pattern, got %d", len(pats))
	}
	if strings.Contains(cmd, `ls -1 *.xcworkspace`) || strings.Contains(cmd, `ls -1 *.xcodeproj`) {
		t.Fatalf("xcode-ipa: workspace/project detection should not shell out through ls because ls on a matched directory prints contents, got %q", cmd)
	}
	if !strings.Contains(cmd, `for d in *.xcworkspace; do if [ -e "$d" ]; then WS="$d"; break; fi; done;`) {
		t.Fatalf("xcode-ipa: workspace detection should capture the matched directory name directly, got %q", cmd)
	}
	if !strings.Contains(cmd, `xcodebuild $FLAG -scheme`) || !strings.Contains(cmd, `-archivePath build/App.xcarchive archive`) {
		t.Fatalf("xcode-ipa: archive action should come after xcodebuild options, got %q", cmd)
	}
	if strings.Contains(cmd, "xcodebuild archive $FLAG") {
		t.Fatalf("xcode-ipa: old invalid archive ordering still present in %q", cmd)
	}
	if !strings.Contains(cmd, `<key>uploadSymbols</key><false/>`) {
		t.Fatalf("xcode-ipa: export options should disable dSYM upload, got %q", cmd)
	}
	if !strings.Contains(cmd, `ENABLE_USER_SCRIPT_SANDBOXING=NO`) {
		t.Fatalf("xcode-ipa: archive command should disable user script sandboxing, got %q", cmd)
	}
	if !strings.Contains(cmd, `APP_STORE_KEY_PATH`) || !strings.Contains(cmd, `APP_STORE_KEY_ISSUER`) {
		t.Fatalf("xcode-ipa: auth env passthrough missing from %q", cmd)
	}
}

func TestResolveBuildCommandXcodeBuildAvoidsLsOnWorkspaceDirs(t *testing.T) {
	cmd, _ := resolveBuildCommand(PlatformXcodeBuild, "/tmp", []string{"App"})
	if strings.Contains(cmd, `ls -1 *.xcworkspace`) || strings.Contains(cmd, `ls -1 *.xcodeproj`) {
		t.Fatalf("xcode-build: workspace/project detection should not use ls on matched directories, got %q", cmd)
	}
}

func TestResolveBuildCommandGradle(t *testing.T) {
	for _, plat := range []BuildPlatform{PlatformGradleAPK, PlatformGradleAAB} {
		cmd, pats := resolveBuildCommand(plat, "/tmp", nil)
		if len(pats) == 0 {
			t.Fatalf("%s: expected at least one artifact pattern", plat)
		}
		if !strings.Contains(cmd, "JAVA_HOME=") {
			t.Fatalf("%s: command should set JAVA_HOME, got %q", plat, cmd)
		}
		if !strings.Contains(cmd, `GRADLE_OPTS="-Xmx`) {
			t.Fatalf("%s: command should inject GRADLE_OPTS heap defaults to survive expo prebuild --clean wiping gradle.properties, got %q", plat, cmd)
		}
		if !strings.Contains(cmd, "MaxMetaspaceSize") {
			t.Fatalf("%s: GRADLE_OPTS should include MaxMetaspaceSize, got %q", plat, cmd)
		}
	}

	// PlatformGradleAAB → bundleRelease task by default.
	cmdAAB, _ := resolveBuildCommand(PlatformGradleAAB, "/tmp", nil)
	if !strings.Contains(cmdAAB, "bundleRelease") {
		t.Fatalf("gradle-aab: expected bundleRelease task, got %q", cmdAAB)
	}

	// PlatformGradleAPK → assembleRelease task by default.
	cmdAPK, _ := resolveBuildCommand(PlatformGradleAPK, "/tmp", nil)
	if !strings.Contains(cmdAPK, "assembleRelease") {
		t.Fatalf("gradle-apk: expected assembleRelease task, got %q", cmdAPK)
	}

	// User-exported GRADLE_OPTS should win over the default.
	t.Setenv("GRADLE_OPTS", "-Xmx2g")
	cmdOverride, _ := resolveBuildCommand(PlatformGradleAAB, "/tmp", nil)
	if !strings.Contains(cmdOverride, `GRADLE_OPTS="-Xmx2g"`) {
		t.Fatalf("gradle-aab: user-exported GRADLE_OPTS should be honoured, got %q", cmdOverride)
	}
}

func TestDetectArtifact(t *testing.T) {
	tmpDir := t.TempDir()

	// Create fake artifact at known path
	artifactDir := filepath.Join(tmpDir, "build", "app", "outputs", "flutter-apk")
	os.MkdirAll(artifactDir, 0755)
	artifactPath := filepath.Join(artifactDir, "app-release.apk")
	os.WriteFile(artifactPath, []byte("fake-apk-content"), 0644)

	patterns := []string{
		"build/app/outputs/flutter-apk/app-release.apk",
		"build/app/outputs/flutter-apk/app-debug.apk",
	}

	found := detectArtifact(tmpDir, patterns)
	if found != artifactPath {
		t.Fatalf("expected %q, got %q", artifactPath, found)
	}
}

func TestDetectArtifactGlob(t *testing.T) {
	tmpDir := t.TempDir()

	// Create artifact matching glob
	artifactDir := filepath.Join(tmpDir, "app", "build", "outputs", "apk", "release")
	os.MkdirAll(artifactDir, 0755)
	artifactPath := filepath.Join(artifactDir, "app-release.apk")
	os.WriteFile(artifactPath, []byte("apk-data"), 0644)

	patterns := []string{"app/build/outputs/apk/release/*.apk"}
	found := detectArtifact(tmpDir, patterns)
	if found != artifactPath {
		t.Fatalf("expected %q, got %q", artifactPath, found)
	}
}

func TestDetectArtifactNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	found := detectArtifact(tmpDir, []string{"build/*.apk"})
	if found != "" {
		t.Fatalf("expected empty, got %q", found)
	}
}

func TestHashFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.bin")
	content := []byte("hello world hash test")
	os.WriteFile(path, content, 0644)

	hash, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}

	// Verify against stdlib
	h := sha256.Sum256(content)
	expected := fmt.Sprintf("%x", h[:])
	if hash != expected {
		t.Fatalf("expected %s, got %s", expected, hash)
	}
}

func TestRegisterArtifact(t *testing.T) {
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "app-release.apk")
	os.WriteFile(artifactPath, []byte("fake apk data for testing"), 0644)

	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)

	build, err := bm.RegisterArtifact(artifactPath, PlatformFlutterAPK)
	if err != nil {
		t.Fatalf("RegisterArtifact: %v", err)
	}

	if build.ArtifactName != "app-release.apk" {
		t.Fatalf("expected artifact name 'app-release.apk', got %q", build.ArtifactName)
	}
	if build.ArtifactSize != 25 {
		t.Fatalf("expected size 25, got %d", build.ArtifactSize)
	}
	if build.ArtifactHash == "" {
		t.Fatal("expected non-empty hash")
	}
	if build.Status != BuildStatusCompleted {
		t.Fatalf("expected status completed, got %s", build.Status)
	}
}

func TestBuildManagerList(t *testing.T) {
	tmpDir := t.TempDir()
	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)

	// Empty list
	if builds := bm.ListBuilds(); len(builds) != 0 {
		t.Fatalf("expected empty list, got %d", len(builds))
	}

	// Register two artifacts
	f1 := filepath.Join(tmpDir, "a.apk")
	f2 := filepath.Join(tmpDir, "b.ipa")
	os.WriteFile(f1, []byte("apk1"), 0644)
	os.WriteFile(f2, []byte("ipa2"), 0644)

	bm.RegisterArtifact(f1, PlatformFlutterAPK)
	bm.RegisterArtifact(f2, PlatformFlutterIPA)

	builds := bm.ListBuilds()
	if len(builds) != 2 {
		t.Fatalf("expected 2 builds, got %d", len(builds))
	}
}

func TestBuildHTTPList(t *testing.T) {
	tmpDir := t.TempDir()
	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)

	srv := &HTTPServer{
		token:       "test-token",
		ownerUserID: "user123",
		buildMgr:    bm,
	}

	req := httptest.NewRequest("GET", "/builds", nil)
	w := httptest.NewRecorder()
	srv.handleBuilds(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var builds []BuildSummary
	json.Unmarshal(w.Body.Bytes(), &builds)
	if len(builds) != 0 {
		t.Fatalf("expected empty list, got %d", len(builds))
	}
}

func TestBuildHTTPRegisterAndDownload(t *testing.T) {
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "test.apk")
	artifactContent := []byte("fake APK binary content for download test")
	os.WriteFile(artifactPath, artifactContent, 0644)

	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)

	srv := &HTTPServer{
		token:       "test-token",
		ownerUserID: "user123",
		buildMgr:    bm,
	}

	// Register artifact
	body := fmt.Sprintf(`{"artifactPath":"%s"}`, artifactPath)
	req := httptest.NewRequest("POST", "/builds/register", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleBuildRegister(w, req)

	if w.Code != 200 {
		t.Fatalf("register: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var build Build
	json.Unmarshal(w.Body.Bytes(), &build)
	if build.ArtifactName != "test.apk" {
		t.Fatalf("expected artifact name 'test.apk', got %q", build.ArtifactName)
	}

	// Download artifact
	req = httptest.NewRequest("GET", "/builds/"+build.ID+"/artifact", nil)
	w = httptest.NewRecorder()
	srv.handleBuildByID(w, req)

	if w.Code != 200 {
		t.Fatalf("download: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify content
	if w.Body.String() != string(artifactContent) {
		t.Fatalf("download content mismatch")
	}

	// Verify SHA256 header
	sha256Header := w.Header().Get("X-Content-SHA256")
	if sha256Header == "" {
		t.Fatal("expected X-Content-SHA256 header")
	}
	if sha256Header != build.ArtifactHash {
		t.Fatalf("SHA256 header %q != build hash %q", sha256Header, build.ArtifactHash)
	}
}

func TestBuildHTTPRangeDownload(t *testing.T) {
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "big.apk")
	content := make([]byte, 1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	os.WriteFile(artifactPath, content, 0644)

	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)
	bm.RegisterArtifact(artifactPath, PlatformFlutterAPK)

	builds := bm.ListBuilds()
	if len(builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(builds))
	}

	srv := &HTTPServer{
		token:       "test-token",
		ownerUserID: "user123",
		buildMgr:    bm,
	}

	// Request with Range header
	req := httptest.NewRequest("GET", "/builds/"+builds[0].ID+"/artifact", nil)
	req.Header.Set("Range", "bytes=0-99")
	w := httptest.NewRecorder()
	srv.handleBuildByID(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("range: expected 206, got %d", w.Code)
	}

	body := w.Body.Bytes()
	if len(body) != 100 {
		t.Fatalf("expected 100 bytes, got %d", len(body))
	}
}

func TestBuildHTTPGetNotFound(t *testing.T) {
	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: "/tmp"}
	bm := NewBuildManager(em, "/tmp")

	srv := &HTTPServer{buildMgr: bm}

	req := httptest.NewRequest("GET", "/builds/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.handleBuildByID(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestBuildHTTPNoArtifact(t *testing.T) {
	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: "/tmp"}
	bm := NewBuildManager(em, "/tmp")

	// Create a build without artifact
	bm.builds["test123"] = &Build{
		ID:       "test123",
		Platform: PlatformFlutterAPK,
		Status:   BuildStatusRunning,
	}

	srv := &HTTPServer{buildMgr: bm}

	req := httptest.NewRequest("GET", "/builds/test123/artifact", nil)
	w := httptest.NewRecorder()
	srv.handleBuildByID(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404 for no artifact, got %d", w.Code)
	}
}

func TestBuildHTTPNoBuildMgr(t *testing.T) {
	srv := &HTTPServer{buildMgr: nil}

	req := httptest.NewRequest("GET", "/builds", nil)
	w := httptest.NewRecorder()
	srv.handleBuilds(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503 when buildMgr nil, got %d", w.Code)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{52428800, "50.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.input)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGuessPlatformFromFile(t *testing.T) {
	tests := []struct {
		path string
		want BuildPlatform
	}{
		{"app-release.apk", PlatformFlutterAPK},
		{"app.aab", PlatformFlutterAAB},
		{"MyApp.ipa", PlatformFlutterIPA},
		{"unknown.zip", PlatformCustom},
	}
	for _, tt := range tests {
		got := guessPlatformFromFile(tt.path)
		if got != tt.want {
			t.Errorf("guessPlatformFromFile(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestRemarshal(t *testing.T) {
	src := map[string]interface{}{
		"id":       "abc123",
		"platform": "flutter-apk",
		"status":   "completed",
	}
	var dst Build
	if err := remarshal(src, &dst); err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if dst.ID != "abc123" {
		t.Fatalf("expected id 'abc123', got %q", dst.ID)
	}
	if dst.Platform != PlatformFlutterAPK {
		t.Fatalf("expected platform flutter-apk, got %q", dst.Platform)
	}
}

// Verify that downloading artifact returns content that matches SHA256 hash.
func TestBuildArtifactIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "integrity.apk")
	content := []byte("this is a test binary for integrity verification across P2P transfer")
	os.WriteFile(artifactPath, content, 0644)

	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)
	build, _ := bm.RegisterArtifact(artifactPath, PlatformFlutterAPK)

	srv := &HTTPServer{buildMgr: bm}

	// Download
	req := httptest.NewRequest("GET", "/builds/"+build.ID+"/artifact", nil)
	w := httptest.NewRecorder()
	srv.handleBuildByID(w, req)

	// Compute SHA256 of downloaded content
	h := sha256.New()
	io.Copy(h, w.Body)
	downloadHash := fmt.Sprintf("%x", h.Sum(nil))

	if downloadHash != build.ArtifactHash {
		t.Fatalf("integrity check failed: download hash %q != registered hash %q", downloadHash, build.ArtifactHash)
	}
}
