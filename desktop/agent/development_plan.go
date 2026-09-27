package main

// development_plan.go is the canonical X/Y/Z contract:
//
//   X — the client surface the user is holding
//   Y — the Yaver box that owns source, tools, builds, and capture
//   Z — the product platform declared by the project manifest
//
// None of the axes imply another. A tvOS client can drive a Windows box that
// builds Xbox, and a Windows client can drive a Mac that builds visionOS. The
// planner reports what is independently ready at each seam instead of turning
// any missing capability into an indefinite spinner.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

type DevelopmentClientContract struct {
	Surface       string   `json:"surface"`
	RenderModes   []string `json:"renderModes,omitempty"`
	Codecs        []string `json:"codecs,omitempty"`
	InputModes    []string `json:"inputModes,omitempty"`
	ChatEnabled   bool     `json:"chatEnabled"`
	RenderEnabled bool     `json:"renderEnabled"`
}

type DevelopmentPlanRequest struct {
	ProjectDir      string                    `json:"projectDir,omitempty"`
	App             string                    `json:"app,omitempty"`
	Target          string                    `json:"target"`
	RemoteBoxID     string                    `json:"remoteBoxId,omitempty"`
	Client          DevelopmentClientContract `json:"client"`
	SessionSettings *ClientSessionSettings    `json:"sessionSettings,omitempty"`
}

type DevelopmentPlanBlocker struct {
	Code     string `json:"code"`
	Layer    string `json:"layer"` // client|box|target|transport
	Message  string `json:"message"`
	Remedy   string `json:"remedy,omitempty"`
	Route    string `json:"route,omitempty"`
	Blocking bool   `json:"blocking"`
}

type DevelopmentPlan struct {
	ProjectDir    string                    `json:"projectDir"`
	App           string                    `json:"app,omitempty"`
	Client        DevelopmentClientContract `json:"client"`
	RemoteBox     *MachineInfo              `json:"remoteBox,omitempty"`
	TargetName    string                    `json:"targetName"`
	Target        ManifestDevelopmentTarget `json:"target"`
	CodeReady     bool                      `json:"codeReady"`
	BuildReady    bool                      `json:"buildReady"`
	RunReady      bool                      `json:"runReady"`
	RenderReady   bool                      `json:"renderReady"`
	FullLoopReady bool                      `json:"fullLoopReady"`
	StatusOnly    bool                      `json:"statusOnly"`
	RenderMode    string                    `json:"renderMode,omitempty"`
	Codec         string                    `json:"codec,omitempty"`
	Blockers      []DevelopmentPlanBlocker  `json:"blockers,omitempty"`
}

type developmentTargetDefaults struct {
	HostOS       []string
	Capabilities []string
	RenderModes  []string
	Codecs       []string
	Hardware     string
}

var developmentTargetCatalog = map[string]developmentTargetDefaults{
	"web":          {RenderModes: []string{"iframe", "webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}},
	"windows":      {HostOS: []string{"windows"}, Capabilities: []string{"windows-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}},
	"xbox":         {HostOS: []string{"windows"}, Capabilities: []string{"xbox-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}, Hardware: "xbox-hardware"},
	"playstation4": {Capabilities: []string{"playstation-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}, Hardware: "ps4-devkit"},
	"playstation5": {Capabilities: []string{"playstation-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}, Hardware: "ps5-devkit"},
	"ios":          {HostOS: []string{"darwin"}, Capabilities: []string{"ios-build"}, RenderModes: []string{"webrtc", "frames", "hermes"}, Codecs: []string{"h264", "jpeg"}},
	"ipados":       {HostOS: []string{"darwin"}, Capabilities: []string{"ios-build"}, RenderModes: []string{"webrtc", "frames", "hermes"}, Codecs: []string{"h264", "jpeg"}},
	"tvos":         {HostOS: []string{"darwin"}, Capabilities: []string{"ios-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}},
	"visionos":     {HostOS: []string{"darwin"}, Capabilities: []string{"ios-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}},
	"watchos":      {HostOS: []string{"darwin"}, Capabilities: []string{"ios-build"}, RenderModes: []string{"frames"}, Codecs: []string{"jpeg"}},
	"android":      {Capabilities: []string{"android-build"}, RenderModes: []string{"webrtc", "frames", "hermes"}, Codecs: []string{"h264", "jpeg"}},
	"android-tv":   {Capabilities: []string{"android-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}},
	"wear-os":      {Capabilities: []string{"android-build"}, RenderModes: []string{"frames"}, Codecs: []string{"jpeg"}},
	"android-xr":   {Capabilities: []string{"android-build"}, RenderModes: []string{"webrtc", "frames"}, Codecs: []string{"h264", "jpeg"}},
}

func normalizeDevelopmentPlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "win", "win32", "windows-store", "windows11":
		return "windows"
	case "ps4":
		return "playstation4"
	case "ps5", "playstation":
		return "playstation5"
	case "xbox-series", "xbox-series-x", "xbox-series-s":
		return "xbox"
	case "androidtv":
		return "android-tv"
	case "wear", "wearos":
		return "wear-os"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func mergeDevelopmentTarget(in ManifestDevelopmentTarget) (ManifestDevelopmentTarget, developmentTargetDefaults, bool) {
	in.Platform = normalizeDevelopmentPlatform(in.Platform)
	def, known := developmentTargetCatalog[in.Platform]
	if in.Builder == nil {
		in.Builder = &ManifestTargetBuilder{}
	}
	if len(in.Builder.HostOS) == 0 {
		in.Builder.HostOS = append([]string(nil), def.HostOS...)
	}
	in.Builder.Capabilities = uniqLowerStrings(append(append([]string(nil), def.Capabilities...), in.Builder.Capabilities...))
	if in.Runtime == nil {
		in.Runtime = &ManifestTargetRuntime{}
	}
	if len(in.Runtime.RenderModes) == 0 {
		in.Runtime.RenderModes = append([]string(nil), def.RenderModes...)
	}
	if len(in.Runtime.Codecs) == 0 {
		in.Runtime.Codecs = append([]string(nil), def.Codecs...)
	}
	if in.Runtime.Hardware == "" {
		in.Runtime.Hardware = def.Hardware
	}
	if def.Hardware != "" {
		in.Runtime.RequiresHardware = true
	}
	return in, def, known
}

func developmentConfigForProject(projectDir, app string) (*ManifestDevelopmentConfig, string, error) {
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	abs, err := filepath.Abs(projectDir)
	if err == nil {
		projectDir = abs
	}
	if manifest, err := LoadManifest(projectDir); err == nil && manifest.Development != nil {
		return manifest.Development, firstNonEmpty(strings.TrimSpace(app), manifest.Name), nil
	}
	root, workspace := loadNearestWorkspaceManifest(projectDir)
	if workspace == nil {
		return nil, "", fmt.Errorf("no development targets declared in .yaver/project.yaml or yaver.workspace.yaml")
	}
	if config, name := workspaceDevelopmentForDir(root, workspace, projectDir, app); config != nil {
		return config, name, nil
	}
	return nil, "", fmt.Errorf("workspace app %q has no development targets", firstNonEmpty(strings.TrimSpace(app), filepath.Base(projectDir)))
}

func workspaceDevelopmentForDir(root string, workspace *WorkspaceManifest, projectDir, app string) (*ManifestDevelopmentConfig, string) {
	if workspace == nil {
		return nil, ""
	}
	projectDir, _ = filepath.Abs(projectDir)
	wanted := strings.TrimSpace(app)
	for i := range workspace.Apps {
		candidate := &workspace.Apps[i]
		candidateDir, _ := filepath.Abs(filepath.Join(root, workspace.Workspace.Root, candidate.Path))
		if candidate.Development != nil && (candidate.Name == wanted || (wanted == "" && candidateDir == projectDir)) {
			return candidate.Development, candidate.Name
		}
	}
	return nil, ""
}

func appendDevelopmentBlocker(plan *DevelopmentPlan, blocker DevelopmentPlanBlocker) {
	plan.Blockers = append(plan.Blockers, blocker)
}

func stringIntersection(preferred, available []string) string {
	set := map[string]bool{}
	for _, value := range available {
		set[strings.ToLower(strings.TrimSpace(value))] = true
	}
	for _, value := range preferred {
		value = strings.ToLower(strings.TrimSpace(value))
		if set[value] {
			return value
		}
	}
	return ""
}

func machineOS(machine MachineInfo) string {
	if osName := strings.ToLower(strings.TrimSpace(machine.OS)); osName != "" {
		return osName
	}
	platform := strings.ToLower(machine.Platform)
	for _, candidate := range []string{"windows", "darwin", "linux"} {
		if strings.Contains(platform, candidate) {
			return candidate
		}
	}
	return ""
}

func machineByID(machines []MachineInfo, id string) *MachineInfo {
	id = strings.TrimSpace(id)
	for i := range machines {
		if machines[i].DeviceID == id || (id == "local" && machines[i].IsLocal) {
			copy := machines[i]
			return &copy
		}
	}
	return nil
}

func developmentClientForRequest(req DevelopmentPlanRequest) DevelopmentClientContract {
	client := req.Client
	if settings := req.SessionSettings; settings != nil {
		client.Surface = firstNonEmpty(client.Surface, settings.ClientSurface, settings.Surface)
		if len(client.RenderModes) == 0 {
			client.RenderModes = append([]string(nil), settings.RenderModes...)
		}
		if len(client.Codecs) == 0 {
			client.Codecs = append([]string(nil), settings.Codecs...)
		}
		if len(client.InputModes) == 0 {
			client.InputModes = append([]string(nil), settings.InputModes...)
		}
		client.ChatEnabled = client.ChatEnabled || settings.ChatEnabled
		client.RenderEnabled = client.RenderEnabled || settings.RenderEnabled
	}
	return client
}

func machineMatchesTargetHost(machine MachineInfo, target ManifestDevelopmentTarget) bool {
	if target.Builder == nil || len(target.Builder.HostOS) == 0 {
		return true
	}
	osName := machineOS(machine)
	return osName != "" && stringIntersection(target.Builder.HostOS, []string{osName}) != ""
}

func BuildDevelopmentPlan(ctx context.Context, req DevelopmentPlanRequest, machines []MachineInfo) (DevelopmentPlan, error) {
	req.Client = developmentClientForRequest(req)
	config, appName, err := developmentConfigForProject(req.ProjectDir, req.App)
	if err != nil {
		return DevelopmentPlan{}, err
	}
	targetName := strings.TrimSpace(req.Target)
	if targetName == "" {
		return DevelopmentPlan{}, fmt.Errorf("target is required")
	}
	target, ok := config.Targets[targetName]
	if !ok {
		// Permit selecting by platform when the manifest key is a friendly name.
		for name, candidate := range config.Targets {
			if normalizeDevelopmentPlatform(candidate.Platform) == normalizeDevelopmentPlatform(targetName) {
				targetName, target, ok = name, candidate, true
				break
			}
		}
	}
	if !ok {
		return DevelopmentPlan{}, fmt.Errorf("target %q is not declared by the project", req.Target)
	}
	target, _, known := mergeDevelopmentTarget(target)
	if target.Platform == "" {
		return DevelopmentPlan{}, fmt.Errorf("target %q must declare a platform", targetName)
	}
	plan := DevelopmentPlan{
		ProjectDir: req.ProjectDir, App: appName, Client: req.Client,
		TargetName: targetName, Target: target,
		CodeReady:  req.Client.ChatEnabled || len(req.Client.InputModes) > 0,
		BuildReady: true, RunReady: true, RenderReady: true,
	}
	if !known && (target.Builder == nil || len(target.Builder.Capabilities) == 0) {
		plan.BuildReady = false
		appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "TARGET_REQUIREMENTS_UNDECLARED", Layer: "target", Blocking: true, Message: "This custom target has no builder requirements.", Remedy: "Declare development.targets." + targetName + ".builder.capabilities in the project manifest."})
	}

	if strings.TrimSpace(req.RemoteBoxID) != "" {
		plan.RemoteBox = machineByID(machines, req.RemoteBoxID)
		if plan.RemoteBox == nil {
			plan.BuildReady, plan.RunReady = false, false
			appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "REMOTE_BOX_NOT_FOUND", Layer: "box", Blocking: true, Message: "The selected remote box is not in this account's machine inventory.", Remedy: "Pair or select another Yaver box.", Route: "/devices"})
		}
	} else {
		for i := range machines {
			if machines[i].IsOnline && machineMatchesTargetHost(machines[i], target) && projectRuntimeMachineSupports(machines[i], target.Builder.Capabilities) {
				candidate := machines[i]
				plan.RemoteBox = &candidate
				break
			}
		}
	}
	if plan.RemoteBox == nil && strings.TrimSpace(req.RemoteBoxID) == "" {
		plan.BuildReady, plan.RunReady = false, false
		appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "NO_ELIGIBLE_REMOTE_BOX", Layer: "box", Blocking: true, Message: "No online Yaver box satisfies this target's build requirements.", Remedy: "Pair a capable box or install the named toolchain on an existing box.", Route: "/devices"})
	}
	if box := plan.RemoteBox; box != nil {
		if !box.IsOnline {
			plan.BuildReady, plan.RunReady = false, false
			appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "REMOTE_BOX_OFFLINE", Layer: "box", Blocking: true, Message: "The selected remote box is offline.", Remedy: "Wake or recover the box, then retry.", Route: "/devices"})
		}
		if len(target.Builder.HostOS) > 0 {
			gotOS := machineOS(*box)
			if gotOS == "" || stringIntersection(target.Builder.HostOS, []string{gotOS}) == "" {
				plan.BuildReady, plan.RunReady = false, false
				appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "TARGET_HOST_OS_MISMATCH", Layer: "box", Blocking: true, Message: fmt.Sprintf("Target %s needs %s; selected box reports %s.", target.Platform, strings.Join(target.Builder.HostOS, "/"), firstNonEmpty(gotOS, "unknown OS")), Remedy: "Select a box with the required operating system.", Route: "/devices"})
			}
		}
		if !projectRuntimeMachineSupports(*box, target.Builder.Capabilities) {
			plan.BuildReady, plan.RunReady = false, false
			appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "TARGET_TOOLCHAIN_UNAVAILABLE", Layer: "box", Blocking: true, Message: "The selected box has not proven the target's required toolchain: " + strings.Join(target.Builder.Capabilities, ", ") + ".", Remedy: "Install/probe the toolchain or select another box.", Route: "/devices"})
		}
		if target.Runtime.RequiresHardware && (box.Capabilities == nil || !profileHasExact(box.Capabilities.Profile, target.Runtime.Hardware)) {
			plan.RunReady = false
			appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "TARGET_HARDWARE_UNAVAILABLE", Layer: "target", Blocking: true, Message: "The build box has not proven attached " + target.Runtime.Hardware + ".", Remedy: "Attach approved real hardware/devkit and add that capability to the box profile.", Route: "/devices"})
		}
	}

	if !plan.CodeReady {
		appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "CLIENT_CONTROL_UNAVAILABLE", Layer: "client", Blocking: true, Message: "This client declared neither chat nor an input capability.", Remedy: "Use a control-capable surface or update the client capability declaration."})
	}
	if !req.Client.RenderEnabled {
		plan.StatusOnly = true
		plan.RenderReady = true
	} else if len(req.Client.RenderModes) == 0 {
		plan.RenderReady = false
		appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "CLIENT_RENDER_CAPABILITIES_REQUIRED", Layer: "client", Blocking: true, Message: "The client requested rendering without declaring any render modes.", Remedy: "Declare the modes this exact client build can decode."})
	} else {
		plan.RenderMode = stringIntersection([]string{"webrtc", "iframe", "frames", "hermes"}, intersectStrings(req.Client.RenderModes, target.Runtime.RenderModes))
		if plan.RenderMode == "" {
			plan.RenderReady = false
			appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "NO_SHARED_RENDER_LANE", Layer: "transport", Blocking: true, Message: "The client and target have no render lane in common.", Remedy: "Use another surface or enable WebRTC/frames support in this client."})
		} else if plan.RenderMode == "webrtc" {
			plan.Codec = stringIntersection([]string{"h264", "vp9", "vp8", "av1", "jpeg"}, intersectStrings(req.Client.Codecs, target.Runtime.Codecs))
			if plan.Codec == "" {
				plan.RenderReady = false
				appendDevelopmentBlocker(&plan, DevelopmentPlanBlocker{Code: "NO_SHARED_MEDIA_CODEC", Layer: "transport", Blocking: true, Message: "WebRTC was selected but the client and target declared no common codec.", Remedy: "Enable H.264 on the surface or declare a supported fallback."})
			}
		}
	}
	plan.FullLoopReady = plan.CodeReady && plan.BuildReady && plan.RunReady && plan.RenderReady
	sort.SliceStable(plan.Blockers, func(i, j int) bool { return plan.Blockers[i].Layer < plan.Blockers[j].Layer })
	_ = ctx // reserved for bounded operation probes added by platform adapters
	return plan, nil
}

func intersectStrings(a, b []string) []string {
	set := map[string]bool{}
	for _, value := range b {
		set[strings.ToLower(strings.TrimSpace(value))] = true
	}
	out := []string{}
	for _, value := range a {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && set[value] {
			out = append(out, value)
		}
	}
	return out
}

func (s *HTTPServer) handleDevelopmentPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req DevelopmentPlanRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ProjectDir) == "" {
		req.ProjectDir = s.dirParam(r)
	}
	plan, err := BuildDevelopmentPlan(r.Context(), req, listAllMachines(r.Context()))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}
