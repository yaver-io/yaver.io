package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTVProject fabricates a .xcodeproj whose pbxproj declares tvOS, the same
// signal Xcode uses and one a third-party project sets without knowing Yaver
// exists.
func writeTVProject(t *testing.T, dir, name string, tvOS bool) string {
	t.Helper()
	proj := filepath.Join(dir, name+".xcodeproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "OTHER = 1;\n"
	if tvOS {
		body = "TVOS_DEPLOYMENT_TARGET = 17.0;\nSDKROOT = appletvos;\n"
	}
	if err := os.WriteFile(filepath.Join(proj, "project.pbxproj"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

// Apple has moved the tvOS marker between fields across Xcode releases, so the
// detector must not pin one. Getting this wrong means a paired Apple TV is
// silently invisible — exactly the bug wire_cmd.go has today.
func TestIsAppleTVDevice(t *testing.T) {
	yes := [][4]string{
		{"tvOS", "", "", ""},
		{"", "appletv", "", ""},
		{"", "", "Apple TV", ""},
		{"", "", "", "AppleTV14,1"},
	}
	for _, c := range yes {
		if !isAppleTVDevice(c[0], c[1], c[2], c[3]) {
			t.Errorf("should be recognised as an Apple TV: %v", c)
		}
	}
	no := [][4]string{
		{"iOS", "iphone", "iPhone", "iPhone14,7"},
		{"iOS", "ipad", "iPad", "iPad13,1"},
		{"xrOS", "realitydevice", "Apple Vision Pro", "RealityDevice14,1"},
		{"", "", "", ""},
	}
	for _, c := range no {
		if isAppleTVDevice(c[0], c[1], c[2], c[3]) {
			t.Errorf("must NOT be treated as an Apple TV: %v", c)
		}
	}
}

// A third-party tvOS app: the project sits right where the user is standing.
func TestDetectTVProjectThirdParty(t *testing.T) {
	root := t.TempDir()
	writeTVProject(t, root, "CoolTVApp", true)

	p, err := detectTVProject(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if filepath.Base(p.Path) != "CoolTVApp.xcodeproj" {
		t.Errorf("found %q, want CoolTVApp.xcodeproj", p.Path)
	}
	if p.Scheme != "CoolTVApp" {
		t.Errorf("scheme = %q, want CoolTVApp", p.Scheme)
	}
	if p.IsWorkspace {
		t.Error("a bare .xcodeproj must not be built with -workspace")
	}
}

// A non-tvOS project must not be mistaken for one — otherwise `yaver tv push`
// in an iOS repo would build the wrong target and install nothing useful.
func TestDetectTVProjectIgnoresNonTVOS(t *testing.T) {
	root := t.TempDir()
	writeTVProject(t, root, "PhoneOnly", false)
	if _, err := detectTVProject(root); err == nil {
		t.Fatal("an iOS-only project must not satisfy tvOS detection")
	}
}

// Yaver's own dogfood layout: a monorepo root with the TV app in ./tvos.
// Standing at the repo root means "this repo's TV app", not "nothing here" —
// the same reason `yaver wire push` descends into ./mobile.
func TestDetectTVProjectDescendsIntoConventionalSubdir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "tvos")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTVProject(t, sub, "YaverTV", true)

	p, err := detectTVProject(root)
	if err != nil {
		t.Fatalf("detect from repo root: %v", err)
	}
	if filepath.Base(p.Path) != "YaverTV.xcodeproj" {
		t.Errorf("found %q, want tvos/YaverTV.xcodeproj", p.Path)
	}
	if filepath.Base(p.Root) != "tvos" {
		t.Errorf("root = %q, want the tvos dir (xcodebuild runs there)", p.Root)
	}
}

// Walking UP matters too: a user deep in Sources/ still means their project.
func TestDetectTVProjectWalksUp(t *testing.T) {
	root := t.TempDir()
	writeTVProject(t, root, "DeepApp", true)
	deep := filepath.Join(root, "Sources", "Feature")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := detectTVProject(deep)
	if err != nil {
		t.Fatalf("detect from subdir: %v", err)
	}
	if filepath.Base(p.Path) != "DeepApp.xcodeproj" {
		t.Errorf("found %q, want DeepApp.xcodeproj", p.Path)
	}
}

// CocoaPods/SPM projects only build through the workspace.
func TestDetectTVProjectPrefersWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTVProject(t, root, "Pods", true)
	ws := filepath.Join(root, "Pods.xcworkspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := detectTVProject(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !p.IsWorkspace || p.flag() != "-workspace" {
		t.Errorf("a workspace must win over the bare project, got %q (%s)", p.Path, p.flag())
	}
}

// The unpaired case is the one thing that cannot be automated, so its message
// has to carry the manual steps rather than a bare "not found".
func TestTVNoDeviceHelpIsActionable(t *testing.T) {
	h := tvNoDeviceHelp()
	for _, want := range []string{"Remote App and Devices", "Devices and Simulators", "Pair", "TestFlight"} {
		if !strings.Contains(h, want) {
			t.Errorf("unpaired help is missing %q:\n%s", want, h)
		}
	}
}

func TestFindTVAppSkipsSimulatorBuilds(t *testing.T) {
	derived := t.TempDir()
	sim := filepath.Join(derived, "Build", "Products", "Debug-appletvsimulator")
	dev := filepath.Join(derived, "Build", "Products", "Debug-appletvos")
	for _, d := range []string{filepath.Join(sim, "App.app"), filepath.Join(dev, "App.app")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := findTVAppInDerivedData(derived)
	if strings.Contains(got, "simulator") {
		t.Errorf("picked the simulator build: %s", got)
	}
	if !strings.Contains(got, "appletvos") {
		t.Errorf("expected the device build, got %s", got)
	}
}
