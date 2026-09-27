package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDevelopmentManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yaver", "project.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func developmentClient(surface string) DevelopmentClientContract {
	return DevelopmentClientContract{
		Surface: surface, ChatEnabled: true, RenderEnabled: true,
		RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"},
		InputModes: []string{"controller", "text"},
	}
}

func TestDevelopmentPlanSeparatesClientBoxAndTarget(t *testing.T) {
	dir := t.TempDir()
	writeDevelopmentManifest(t, dir, `name: sfmg
development:
  targets:
    xbox:
      platform: xbox
`)
	machines := []MachineInfo{{
		DeviceID: "win-11", Name: "Windows builder", OS: "windows", IsOnline: true,
		Capabilities: &MachineCapabilities{
			SupportsXboxBuild: true,
			Profile:           &MachineProfile{Tags: []string{"xbox-hardware"}},
		},
	}}
	plan, err := BuildDevelopmentPlan(context.Background(), DevelopmentPlanRequest{
		ProjectDir: dir, Target: "xbox", RemoteBoxID: "win-11", Client: developmentClient("tvos"),
	}, machines)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.FullLoopReady {
		t.Fatalf("tvOS -> Windows -> Xbox should be ready, blockers=%+v", plan.Blockers)
	}
	if plan.Client.Surface != "tvos" || plan.RemoteBox.OS != "windows" || plan.Target.Platform != "xbox" {
		t.Fatalf("axes collapsed: %+v", plan)
	}
	if plan.RenderMode != "webrtc" || plan.Codec != "h264" {
		t.Fatalf("negotiated render=%q codec=%q", plan.RenderMode, plan.Codec)
	}
}

func TestDevelopmentPlanRefusesMacForXbox(t *testing.T) {
	dir := t.TempDir()
	writeDevelopmentManifest(t, dir, `name: sfmg
development:
  targets:
    xbox: { platform: xbox }
`)
	plan, err := BuildDevelopmentPlan(context.Background(), DevelopmentPlanRequest{
		ProjectDir: dir, Target: "xbox", RemoteBoxID: "mac", Client: developmentClient("mobile"),
	}, []MachineInfo{{DeviceID: "mac", OS: "darwin", IsOnline: true, Capabilities: &MachineCapabilities{}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BuildReady || plan.FullLoopReady {
		t.Fatal("a Mac must not report an Xbox build loop ready")
	}
	joined := ""
	for _, blocker := range plan.Blockers {
		joined += blocker.Code + " "
	}
	if !strings.Contains(joined, "TARGET_HOST_OS_MISMATCH") || !strings.Contains(joined, "TARGET_TOOLCHAIN_UNAVAILABLE") {
		t.Fatalf("missing named host/toolchain blockers: %+v", plan.Blockers)
	}
}

func TestDevelopmentPlanPlayStationNeedsExplicitSDKAndDevkit(t *testing.T) {
	dir := t.TempDir()
	writeDevelopmentManifest(t, dir, `name: talos
development:
  targets:
    ps5: { platform: playstation5 }
`)
	plan, err := BuildDevelopmentPlan(context.Background(), DevelopmentPlanRequest{
		ProjectDir: dir, Target: "ps5", RemoteBoxID: "win", Client: developmentClient("windows"),
	}, []MachineInfo{{DeviceID: "win", OS: "windows", IsOnline: true, Capabilities: &MachineCapabilities{}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BuildReady || plan.RunReady {
		t.Fatal("ordinary Windows inventory must never imply PlayStation SDK/devkit access")
	}
}

func TestRestrictedCapabilitiesRequireExactProfileTokens(t *testing.T) {
	profile := &MachineProfile{Tags: []string{"no-playstation-sdk", "not-ps5-devkit", "no-xbox-sdk"}}
	if profileHasExact(profile, "playstation-sdk") || profileHasExact(profile, "ps5-devkit") || profileHasExact(profile, "xbox-sdk") {
		t.Fatal("negative profile labels must not prove restricted SDK or devkit access")
	}
}

func TestDevelopmentPlanAllowsStatusOnlyCompanion(t *testing.T) {
	dir := t.TempDir()
	writeDevelopmentManifest(t, dir, `name: yaver
development:
  targets:
    web: { platform: web }
`)
	client := DevelopmentClientContract{Surface: "watchos", ChatEnabled: true, RenderEnabled: false, InputModes: []string{"voice"}}
	plan, err := BuildDevelopmentPlan(context.Background(), DevelopmentPlanRequest{
		ProjectDir: dir, Target: "web", RemoteBoxID: "linux", Client: client,
	}, []MachineInfo{{DeviceID: "linux", OS: "linux", IsOnline: true, Capabilities: &MachineCapabilities{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.StatusOnly || !plan.FullLoopReady {
		t.Fatalf("a status/chat-only watch should participate without pixels: %+v", plan)
	}
}

func TestDevelopmentPlanConsumesNormalClientSessionSettings(t *testing.T) {
	dir := t.TempDir()
	writeDevelopmentManifest(t, dir, `name: yaver
development:
  targets:
    web: { platform: web }
`)
	plan, err := BuildDevelopmentPlan(context.Background(), DevelopmentPlanRequest{
		ProjectDir: dir, Target: "web", RemoteBoxID: "linux",
		SessionSettings: &ClientSessionSettings{
			ClientSurface: "vision-pro", ChatEnabled: true, RenderEnabled: true,
			RenderModes: []string{"webrtc"}, Codecs: []string{"h264"}, InputModes: []string{"gaze", "voice"},
		},
	}, []MachineInfo{{DeviceID: "linux", OS: "linux", IsOnline: true, Capabilities: &MachineCapabilities{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.FullLoopReady || plan.Client.Surface != "vision-pro" || plan.RenderMode != "webrtc" {
		t.Fatalf("normal session settings were not used as the client contract: %+v", plan)
	}
}

func TestDevelopmentPlanAutoSelectionHonorsTargetHostOS(t *testing.T) {
	dir := t.TempDir()
	writeDevelopmentManifest(t, dir, `name: sfmg
development:
  targets:
    windows: { platform: windows }
`)
	machines := []MachineInfo{
		{DeviceID: "mac", OS: "darwin", IsOnline: true, Capabilities: &MachineCapabilities{SupportsWindowsBuild: true}},
		{DeviceID: "win", OS: "windows", IsOnline: true, Capabilities: &MachineCapabilities{SupportsWindowsBuild: true}},
	}
	plan, err := BuildDevelopmentPlan(context.Background(), DevelopmentPlanRequest{
		ProjectDir: dir, Target: "windows", Client: developmentClient("web"),
	}, machines)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RemoteBox == nil || plan.RemoteBox.DeviceID != "win" {
		t.Fatalf("auto selection chose the wrong host: %+v", plan.RemoteBox)
	}
}

func TestWorkspaceAppCanDeclareDevelopmentTargets(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "sfmg")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
workspace: { root: . }
apps:
  - name: sfmg
    path: ./sfmg
    stack: react-native-expo
    development:
      targets:
        xbox: { platform: xbox }
`
	if err := os.WriteFile(filepath.Join(root, WorkspaceManifestPath), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	config, app, err := developmentConfigForProject(appDir, "sfmg")
	if err != nil {
		t.Fatal(err)
	}
	if app != "sfmg" || config.Targets["xbox"].Platform != "xbox" {
		t.Fatalf("workspace development contract did not resolve: app=%q config=%+v", app, config)
	}
	summary := buildProjectRuntimeSummaryFromManifest(context.Background(), nil, appDir, &ProjectManifest{Name: "sfmg"})
	if _, ok := summary.DevelopmentTargets["xbox"]; !ok {
		t.Fatalf("runtime summary hid the workspace development target: %+v", summary.DevelopmentTargets)
	}
}
