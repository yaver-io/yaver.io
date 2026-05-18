package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClassifyNativeDeployTargets(t *testing.T) {
	cases := map[string]nativeClass{
		"testflight": nativeIOS,
		"playstore":  nativeAndroid,
		"play":       nativeAndroid,
		"cloudflare": nativeNone,
		"convex":     nativeNone,
		"vercel":     nativeNone,
		"eas":        nativeNone,
	}
	for target, want := range cases {
		if got := classifyNative("deploy", target, "."); got != want {
			t.Errorf("deploy %q: got %q want %q", target, got, want)
		}
	}
}

func TestClassifyBuildCommand(t *testing.T) {
	cases := []struct {
		cmd, tool string
		want      nativeClass
	}{
		{"npx expo prebuild --platform ios && cd ios && xcodebuild", "expo-ios", nativeIOS},
		{"./gradlew bundleRelease", "expo-android", nativeAndroid},
		{"xcodebuild -configuration Release", "xcode", nativeIOS},
		{"./gradlew build", "gradle", nativeAndroid},
		{"flutter build ipa", "flutter", nativeIOS},
		{"flutter build apk", "flutter", nativeAndroid},
		{"flutter build appbundle", "flutter", nativeAndroid},
		{"go build ./...", "go", nativeNone},
		{"npm run build", "npm", nativeNone},
		{"cargo build --release", "cargo", nativeNone},
	}
	for _, c := range cases {
		if got := classifyBuildCommand(c.cmd, c.tool); got != c.want {
			t.Errorf("classifyBuildCommand(%q,%q): got %q want %q", c.cmd, c.tool, got, c.want)
		}
	}
}

func TestClassifyNativeBuildExplicitTarget(t *testing.T) {
	dir := t.TempDir()
	// Explicit target wins even with no manifest.
	if got := classifyNative("build", "ios", dir); got != nativeIOS {
		t.Errorf("target=ios: got %q want ios", got)
	}
	if got := classifyNative("build", "android", dir); got != nativeAndroid {
		t.Errorf("target=android: got %q want android", got)
	}
	if got := classifyNative("build", "appbundle", dir); got != nativeAndroid {
		t.Errorf("target=appbundle: got %q want android", got)
	}
}

func TestClassifyNativeBuildFromManifest(t *testing.T) {
	goDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := classifyNative("build", "", goDir); got != nativeNone {
		t.Errorf("go.mod project: got %q want none", got)
	}

	gradleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(gradleDir, "build.gradle"), []byte("// gradle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := classifyNative("build", "", gradleDir); got != nativeAndroid {
		t.Errorf("gradle project: got %q want android", got)
	}

	expoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(expoDir, "app.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := classifyNative("build", "ios", expoDir); got != nativeIOS {
		t.Errorf("expo + target ios: got %q want ios", got)
	}
}

func TestBuildPlatformClass(t *testing.T) {
	ios := []BuildPlatform{PlatformXcodeIPA, PlatformXcodeBuild, PlatformXcodeDeviceInstall, PlatformFlutterIPA, PlatformRNIOS, PlatformExpoIOS}
	for _, p := range ios {
		if buildPlatformClass(p) != nativeIOS {
			t.Errorf("%s: want nativeIOS", p)
		}
	}
	android := []BuildPlatform{PlatformGradleAPK, PlatformGradleAAB, PlatformFlutterAPK, PlatformFlutterAAB, PlatformRNAndroid, PlatformExpoAndroid}
	for _, p := range android {
		if buildPlatformClass(p) != nativeAndroid {
			t.Errorf("%s: want nativeAndroid", p)
		}
	}
	if buildPlatformClass(PlatformCustom) != nativeNone {
		t.Errorf("custom: want nativeNone")
	}
}

func TestHostOSGate(t *testing.T) {
	if err := hostOSGate(nativeNone); err != nil {
		t.Errorf("nativeNone should never gate: %v", err)
	}
	if err := hostOSGate(nativeAndroid); err != nil {
		t.Errorf("nativeAndroid is cross-platform, should not gate: %v", err)
	}
	err := hostOSGate(nativeIOS)
	if runtime.GOOS == "darwin" {
		if err != nil {
			t.Errorf("darwin host should pass the iOS host-OS gate: %v", err)
		}
	} else {
		if err == nil {
			t.Errorf("non-darwin host (%s) must fail the iOS host-OS gate", runtime.GOOS)
		}
	}
}

func TestRunBuildPreflightNoneAlwaysOK(t *testing.T) {
	pf := runBuildPreflight(context.Background(), nativeNone, false, nil)
	if !pf.OK || pf.Code != "" {
		t.Errorf("nativeNone preflight must be OK: %+v", pf)
	}
}

func TestRunBuildPreflightIOSWrongHost(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("wrong-host path only observable off darwin")
	}
	pf := runBuildPreflight(context.Background(), nativeIOS, false, nil)
	if pf.OK {
		t.Fatalf("iOS preflight on %s must fail", runtime.GOOS)
	}
	if pf.Code != "wrong_host_os" {
		t.Errorf("code: got %q want wrong_host_os", pf.Code)
	}
	if pf.RequiredOS != "darwin" {
		t.Errorf("requiredOS: got %q want darwin", pf.RequiredOS)
	}
}

func TestAndroidGateVerdict(t *testing.T) {
	// Both present → nothing missing.
	if m := androidGateVerdict(true, "Java 17", "/opt/android-sdk"); len(m) != 0 {
		t.Errorf("both present should yield no missing deps, got %+v", m)
	}
	// JDK too old + SDK absent → both reported, both auto-installable.
	m := androidGateVerdict(false, "Java 11", "")
	if len(m) != 2 {
		t.Fatalf("want 2 missing deps, got %d (%+v)", len(m), m)
	}
	for _, d := range m {
		if !d.Auto {
			t.Errorf("android dep %q must be auto-installable", d.Name)
		}
	}
	// Only SDK missing.
	m = androidGateVerdict(true, "Java 17", "")
	if len(m) != 1 || m[0].Name != "android-sdk" {
		t.Errorf("want only android-sdk missing, got %+v", m)
	}
}

// TestPreflightAgainstRealRepos exercises the gate on the actual talos
// and yaver.io mobile projects on this developer box. It asserts the
// invariants (classification is correct; OK iff no unmet deps) and logs
// the concrete verdict so a run is the "tested with talos and yaver"
// evidence. Skips cleanly on machines without those checkouts (CI).
func TestPreflightAgainstRealRepos(t *testing.T) {
	repos := map[string]string{
		"talos": "/Users/kivanccakmak/Workspace/talos/mobile",
		"yaver": "/Users/kivanccakmak/Workspace/yaver.io/mobile",
	}
	ran := 0
	for name, dir := range repos {
		if _, err := os.Stat(filepath.Join(dir, "app.json")); err != nil {
			t.Logf("%s: %s not present — skipping", name, dir)
			continue
		}
		ran++

		if got := classifyNative("build", "ios", dir); got != nativeIOS {
			t.Errorf("%s build target=ios: got %q want ios", name, got)
		}
		if got := classifyNative("build", "android", dir); got != nativeAndroid {
			t.Errorf("%s build target=android: got %q want android", name, got)
		}

		iosPF := runBuildPreflight(context.Background(), nativeIOS, false, nil)
		andPF := runBuildPreflight(context.Background(), nativeAndroid, false, nil)
		t.Logf("%s iOS  -> ok=%v code=%q missing=%v", name, iosPF.OK, iosPF.Code, iosPF.Missing)
		t.Logf("%s AAB  -> ok=%v code=%q missing=%v installable=%v", name, andPF.OK, andPF.Code, andPF.Missing, andPF.Installable)

		for _, pf := range []preflightResult{iosPF, andPF} {
			if pf.OK && (pf.Code != "" || len(pf.Missing) != 0) {
				t.Errorf("%s: OK verdict must have no code/missing: %+v", name, pf)
			}
			if !pf.OK && pf.Code == "" {
				t.Errorf("%s: failed verdict must carry a code: %+v", name, pf)
			}
		}
		if runtime.GOOS != "darwin" && iosPF.Code != "wrong_host_os" {
			t.Errorf("%s: iOS on %s must be wrong_host_os, got %q", name, runtime.GOOS, iosPF.Code)
		}
	}
	if ran == 0 {
		t.Skip("neither talos nor yaver mobile checkout present")
	}
}

func TestWireNativeClass(t *testing.T) {
	cases := []struct {
		stack, platform string
		want            nativeClass
	}{
		{"expo", "ios", nativeIOS},
		{"react-native", "android", nativeAndroid},
		{"flutter", "ios", nativeIOS},
		{"native-ios", "", nativeIOS},
		{"native-android", "", nativeAndroid},
		{"expo", "", nativeNone},
	}
	for _, c := range cases {
		if got := wireNativeClass(c.stack, c.platform); got != c.want {
			t.Errorf("wireNativeClass(%q,%q): got %q want %q", c.stack, c.platform, got, c.want)
		}
	}
}

func TestInstallAndroidSDKRuntimeRefusesUnapproved(t *testing.T) {
	err := installAndroidSDKRuntime(context.Background(), false, nil)
	if err == nil {
		t.Fatal("unapproved android sdk install must be refused")
	}
	if !strings.Contains(err.Error(), "explicit approval") {
		t.Errorf("refusal must mention approval, got: %v", err)
	}
}

func TestPreflightCLIError(t *testing.T) {
	if err := preflightCLIError(preflightResult{OK: true}); err != nil {
		t.Errorf("OK verdict must yield nil error, got %v", err)
	}
	pf := preflightResult{
		OK: false, Code: "deps_missing", Installable: true,
		Error:   "Android build needs JDK 17 + Android SDK.",
		Missing: []preflightDep{{Name: "jdk", Have: "Java 11", Need: "OpenJDK 17+", Fix: "yaver installs it"}},
	}
	msg := preflightCLIError(pf).Error()
	for _, want := range []string{"deps_missing", "jdk", "Java 11", "--install-deps"} {
		if !strings.Contains(msg, want) {
			t.Errorf("CLI error missing %q in:\n%s", want, msg)
		}
	}
}

func TestPreflightInitialShape(t *testing.T) {
	pf := preflightResult{
		OK: false, Code: "deps_missing", Class: nativeAndroid, HostOS: "linux",
		Missing:     []preflightDep{{Name: "jdk", Auto: true}},
		Installable: true,
	}
	m := preflightInitial(pf)
	if _, ok := m["preflight"]; !ok {
		t.Error("missing preflight block")
	}
	if _, ok := m["missing"]; !ok {
		t.Error("missing deps list")
	}
	if _, ok := m["hint"]; !ok {
		t.Error("deps_missing + installable should surface an installDeps hint")
	}
}
