package main

import (
	"strings"
	"testing"
)

func TestResolveNativePlatform_Table(t *testing.T) {
	cases := []struct {
		native, target string
		want           BuildPlatform
		wantErr        bool
	}{
		// iosNative
		{"iosNative", "device", PlatformXcodeDeviceInstall, false},
		{"iosNative", "", PlatformXcodeDeviceInstall, false}, // empty target → device
		{"iosNative", "simulator", PlatformXcodeBuild, false},
		{"iosNative", "sim", PlatformXcodeBuild, false},
		{"iosNative", "testflight", PlatformXcodeIPA, false},
		{"iosNative", "ipa", PlatformXcodeIPA, false},
		{"iosNative", "local", PlatformXcodeIPA, false},
		{"ios-native", "device", PlatformXcodeDeviceInstall, false},
		{"ios", "device", PlatformXcodeDeviceInstall, false},

		// androidNative
		{"androidNative", "device", PlatformGradleDeviceInstall, false},
		{"androidNative", "emulator", PlatformGradleDeviceInstall, false},
		{"androidNative", "playstore", PlatformGradleAAB, false},
		{"androidNative", "aab", PlatformGradleAAB, false},
		{"androidNative", "apk", PlatformGradleAPK, false},
		{"android-native", "device", PlatformGradleDeviceInstall, false},
		{"android", "device", PlatformGradleDeviceInstall, false},

		// flutter
		{"flutter", "device", PlatformFlutterDeviceInstall, false},
		{"flutter", "ios", PlatformFlutterIPA, false},
		{"flutter", "ipa", PlatformFlutterIPA, false},
		{"flutter", "testflight", PlatformFlutterIPA, false},
		{"flutter", "playstore", PlatformFlutterAAB, false},
		{"flutter", "aab", PlatformFlutterAAB, false},
		{"flutter", "apk", PlatformFlutterAPK, false},

		// case insensitivity on target
		{"iosNative", "DEVICE", PlatformXcodeDeviceInstall, false},
		{"androidNative", "PlayStore", PlatformGradleAAB, false},

		// errors
		{"iosNative", "bogus", "", true},
		{"androidNative", "bogus", "", true},
		{"flutter", "bogus", "", true},
		{"unknown", "device", "", true},
	}

	for _, tc := range cases {
		got, err := resolveNativePlatform(tc.native, tc.target)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveNativePlatform(%q, %q) want error, got %q", tc.native, tc.target, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveNativePlatform(%q, %q) unexpected error: %v", tc.native, tc.target, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveNativePlatform(%q, %q) = %q, want %q", tc.native, tc.target, got, tc.want)
		}
	}
}

func TestIsNativeAlias(t *testing.T) {
	for _, p := range []string{"iosNative", "ios-native", "androidNative", "android-native", "flutter"} {
		if !isNativeAlias(p) {
			t.Errorf("isNativeAlias(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"flutter-apk", "xcode-ipa", "gradle-aab", "rn-ios", "expo-android", "", "ios"} {
		if isNativeAlias(p) {
			t.Errorf("isNativeAlias(%q) = true, want false", p)
		}
	}
}

func TestIsIOSBoundPlatform(t *testing.T) {
	iosBound := []string{
		string(PlatformXcodeIPA),
		string(PlatformXcodeBuild),
		string(PlatformXcodeDeviceInstall),
		string(PlatformRNIOS),
		string(PlatformExpoIOS),
		string(PlatformHermesBundlePush),
		string(PlatformFlutterIPA),
		"ios", "iosNative", "ios-native",
	}
	for _, p := range iosBound {
		if !isIOSBoundPlatform(p) {
			t.Errorf("isIOSBoundPlatform(%q) = false, want true", p)
		}
	}
	notIOS := []string{
		string(PlatformGradleAPK),
		string(PlatformGradleAAB),
		string(PlatformGradleDeviceInstall),
		string(PlatformFlutterAPK),
		string(PlatformFlutterAAB),
		string(PlatformFlutterDeviceInstall),
		string(PlatformRNAndroid),
		string(PlatformExpoAndroid),
		"androidNative", "android-native", "flutter", "",
	}
	for _, p := range notIOS {
		if isIOSBoundPlatform(p) {
			t.Errorf("isIOSBoundPlatform(%q) = true, want false", p)
		}
	}
}

func TestResolveNativeBuildCommand_Gradle(t *testing.T) {
	// No gradlew on disk → falls back to system gradle.
	cmd, patterns, ok := resolveNativeBuildCommand(PlatformGradleDeviceInstall, "/nonexistent", nil)
	if !ok {
		t.Fatal("resolveNativeBuildCommand(gradle-device-install) ok=false, want true")
	}
	if !strings.Contains(cmd, "assembleDebug") {
		t.Errorf("gradle-device-install command should include assembleDebug, got %q", cmd)
	}
	if !strings.Contains(cmd, "JAVA_HOME=") {
		t.Errorf("gradle-device-install command should set JAVA_HOME, got %q", cmd)
	}
	if len(patterns) == 0 {
		t.Error("gradle-device-install should return artifact patterns")
	}
	hasDebugAPK := false
	for _, p := range patterns {
		if strings.Contains(p, "apk/debug") {
			hasDebugAPK = true
		}
	}
	if !hasDebugAPK {
		t.Errorf("gradle-device-install patterns missing apk/debug glob: %v", patterns)
	}
}

func TestResolveNativeBuildCommand_Flutter(t *testing.T) {
	cmd, patterns, ok := resolveNativeBuildCommand(PlatformFlutterDeviceInstall, "/tmp", nil)
	if !ok {
		t.Fatal("resolveNativeBuildCommand(flutter-device-install) ok=false")
	}
	if !strings.Contains(cmd, "flutter build apk --debug") {
		t.Errorf("flutter-device-install command should be flutter build apk --debug, got %q", cmd)
	}
	if len(patterns) == 0 {
		t.Error("flutter-device-install should return artifact patterns")
	}
}

func TestResolveNativeBuildCommand_NotMine(t *testing.T) {
	// Platforms native_build.go doesn't own should return ok=false so the big
	// switch in resolveBuildCommand handles them.
	for _, p := range []BuildPlatform{
		PlatformFlutterAPK, PlatformGradleAPK, PlatformXcodeIPA,
		PlatformXcodeDeviceInstall, PlatformRNIOS, PlatformHermesBundlePush,
	} {
		_, _, ok := resolveNativeBuildCommand(p, "/tmp", nil)
		if ok {
			t.Errorf("resolveNativeBuildCommand(%q) returned ok=true; should be handled by main switch", p)
		}
	}
}

func TestNativeAliasConstants(t *testing.T) {
	// Anchor the public string values so other surfaces (mobile, web, MCP) can rely on them.
	if NativeIOS != "iosNative" {
		t.Errorf("NativeIOS drifted: got %q want iosNative", NativeIOS)
	}
	if NativeAndroid != "androidNative" {
		t.Errorf("NativeAndroid drifted: got %q want androidNative", NativeAndroid)
	}
	if NativeFlutter != "flutter" {
		t.Errorf("NativeFlutter drifted: got %q want flutter", NativeFlutter)
	}
}
