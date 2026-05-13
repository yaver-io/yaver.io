package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

func nativeBuildMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "native_build",
			"description": "Build or install a native iOS, Android, or Flutter app. Yaver discovers matching mobile projects under work_dir (including mobile/, app/, apps/*, packages/*). If more than one candidate matches, it returns candidates instead of guessing.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"platform"},
				"properties": map[string]interface{}{
					"platform":          map[string]interface{}{"type": "string", "enum": []string{"iosNative", "ios-native", "androidNative", "android-native", "flutter"}},
					"target":            map[string]interface{}{"type": "string", "description": "device | simulator | testflight | playstore | local | apk | aab | ipa"},
					"work_dir":          map[string]interface{}{"type": "string", "description": "Repo root or project directory to scan."},
					"project_path":      map[string]interface{}{"type": "string", "description": "Exact project path to use when work_dir is ambiguous."},
					"project_index":     map[string]interface{}{"type": "integer", "description": "1-based index into the discovered candidates."},
					"scheme":            map[string]interface{}{"type": "string"},
					"flavor":            map[string]interface{}{"type": "string"},
					"args":              map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"install_on_device": map[string]interface{}{"type": "boolean"},
				},
			},
		},
		{
			"name":        "build_ios",
			"description": "Discover an iOS-capable project in the repo, choose the correct builder for its stack (RN/Expo/Flutter/Swift), and start an IPA build.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"work_dir": map[string]interface{}{"type": "string"}, "project_path": map[string]interface{}{"type": "string"}, "project_index": map[string]interface{}{"type": "integer"}, "scheme": map[string]interface{}{"type": "string"}}},
		},
		{
			"name":        "build_android",
			"description": "Discover an Android-capable project in the repo, choose the correct builder for its stack (RN/Expo/Flutter/Kotlin), and start an AAB build.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"work_dir": map[string]interface{}{"type": "string"}, "project_path": map[string]interface{}{"type": "string"}, "project_index": map[string]interface{}{"type": "integer"}, "flavor": map[string]interface{}{"type": "string"}}},
		},
		{
			"name":        "push_ios",
			"description": "Discover an iOS-capable project, build an IPA with the correct stack-specific builder, wait for completion, then upload it to TestFlight.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"work_dir": map[string]interface{}{"type": "string"}, "project_path": map[string]interface{}{"type": "string"}, "project_index": map[string]interface{}{"type": "integer"}, "scheme": map[string]interface{}{"type": "string"}}},
		},
		{
			"name":        "push_android",
			"description": "Discover an Android-capable project, build an AAB with the correct stack-specific builder, wait for completion, then upload it to Google Play internal testing.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"work_dir": map[string]interface{}{"type": "string"}, "project_path": map[string]interface{}{"type": "string"}, "project_index": map[string]interface{}{"type": "integer"}, "flavor": map[string]interface{}{"type": "string"}}},
		},
	}
}

func nativeCandidatesPayload(hits []nativeProjectCandidate) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(hits))
	for i, hit := range hits {
		out = append(out, map[string]interface{}{
			"index": i + 1,
			"path":  hit.Path,
			"stack": hit.Stack,
		})
	}
	return out
}

func resolveMCPNativeProject(startDir, native, explicitPath string, projectIndex int) (nativeProjectCandidate, interface{}, error) {
	if explicitPath != "" {
		abs, err := filepath.Abs(explicitPath)
		if err != nil {
			return nativeProjectCandidate{}, nil, err
		}
		stack := detectMobileStack(abs)
		if stack == "" || !nativeProjectMatches(native, stack) {
			return nativeProjectCandidate{}, nil, fmt.Errorf("%s is not a supported %s project", abs, nativeLabel(native))
		}
		return nativeProjectCandidate{Path: abs, Stack: stack}, nil, nil
	}
	hits := discoverNativeProjectCandidates(startDir, native)
	if len(hits) == 0 {
		return nativeProjectCandidate{}, nil, fmt.Errorf("no %s mobile project detected under %s", nativeLabel(native), startDir)
	}
	if projectIndex > 0 {
		if projectIndex > len(hits) {
			return nativeProjectCandidate{}, nil, fmt.Errorf("project_index %d out of range (found %d candidates)", projectIndex, len(hits))
		}
		return hits[projectIndex-1], nil, nil
	}
	if len(hits) == 1 {
		return hits[0], nil, nil
	}
	return nativeProjectCandidate{}, mcpToolJSON(map[string]interface{}{
		"ok":              false,
		"needs_selection": true,
		"platform":        native,
		"workDir":         startDir,
		"candidates":      nativeCandidatesPayload(hits),
	}), nil
}

func startMCPNativeBuild(s *HTTPServer, native, target, startDir, explicitPath string, projectIndex int, extra []string, install bool) interface{} {
	candidate, selection, err := resolveMCPNativeProject(startDir, native, explicitPath, projectIndex)
	if selection != nil || err != nil {
		if err != nil {
			return mcpToolError(err.Error())
		}
		return selection
	}
	platform, err := resolveNativePlatform(native, target)
	if err != nil {
		return mcpToolError(err.Error())
	}
	build, err := s.buildMgr.StartBuild(platform, candidate.Path, extra, install)
	if err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolJSON(map[string]interface{}{
		"id":              build.ID,
		"platform":        build.Platform,
		"command":         build.Command,
		"workDir":         build.WorkDir,
		"status":          build.Status,
		"installOnDevice": build.InstallOnDevice,
		"native":          native,
		"target":          firstNonEmpty(target, "device"),
		"project":         candidate,
		"streamPath":      fmt.Sprintf("/builds/%s/stream", build.ID),
		"statusPath":      fmt.Sprintf("/builds/%s", build.ID),
	})
}

func startMCPNativeReleaseBuild(s *HTTPServer, native, startDir, explicitPath string, projectIndex int, extra []string) interface{} {
	candidate, selection, err := resolveMCPNativeProject(startDir, native, explicitPath, projectIndex)
	if selection != nil || err != nil {
		if err != nil {
			return mcpToolError(err.Error())
		}
		return selection
	}
	platform, err := releasePlatformForCandidate(native, candidate.Stack)
	if err != nil {
		return mcpToolError(err.Error())
	}
	build, err := s.buildMgr.StartBuild(platform, candidate.Path, extra, false)
	if err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolJSON(map[string]interface{}{
		"id":         build.ID,
		"platform":   build.Platform,
		"command":    build.Command,
		"workDir":    build.WorkDir,
		"status":     build.Status,
		"native":     native,
		"project":    candidate,
		"streamPath": fmt.Sprintf("/builds/%s/stream", build.ID),
		"statusPath": fmt.Sprintf("/builds/%s", build.ID),
	})
}

func waitForManagedBuild(s *HTTPServer, buildID string) (*Build, error) {
	for {
		build, ok := s.buildMgr.GetBuild(buildID)
		if !ok {
			return nil, fmt.Errorf("build not found: %s", buildID)
		}
		switch build.Status {
		case BuildStatusCompleted, BuildStatusFailed, BuildStatusCancelled:
			return build, nil
		}
		time.Sleep(2 * time.Second)
	}
}

func runMCPNativePush(s *HTTPServer, native, startDir, explicitPath string, projectIndex int, extra []string) interface{} {
	resp := startMCPNativeReleaseBuild(s, native, startDir, explicitPath, projectIndex, extra)
	payload, ok := resp.(map[string]interface{})
	if !ok {
		return resp
	}
	id, _ := payload["id"].(string)
	if id == "" {
		return resp
	}
	build, err := waitForManagedBuild(s, id)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if build.Status != BuildStatusCompleted || build.ArtifactPath == "" {
		return mcpToolJSON(map[string]interface{}{
			"ok":       false,
			"build_id": id,
			"status":   build.Status,
			"error":    firstNonEmpty(build.Error, "build did not produce an artifact"),
		})
	}
	switch native {
	case NativeIOS:
		if err := uploadToTestFlight(build.ArtifactPath); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "target": "testflight", "build_id": id, "artifactPath": build.ArtifactPath})
	case NativeAndroid:
		if err := uploadToPlayStore(build.ArtifactPath); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "target": "playstore-internal", "build_id": id, "artifactPath": build.ArtifactPath})
	default:
		return mcpToolError("unsupported native push target")
	}
}

func dispatchNativeBuildMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "native_build", "build_ios", "build_android", "push_ios", "push_android":
	default:
		return false, nil
	}
	if s == nil || s.buildMgr == nil {
		return true, mcpToolError("builds not available on this agent")
	}

	var args struct {
		Platform        string   `json:"platform"`
		Target          string   `json:"target"`
		WorkDir         string   `json:"work_dir"`
		ProjectPath     string   `json:"project_path"`
		ProjectIndex    int      `json:"project_index"`
		Scheme          string   `json:"scheme"`
		Flavor          string   `json:"flavor"`
		Args            []string `json:"args"`
		InstallOnDevice *bool    `json:"install_on_device"`
	}
	_ = json.Unmarshal(arguments, &args)

	// Defaulting chain: explicit args.WorkDir → buildMgr's pinned dir →
	// the MCP session cwd (AI's working directory) → "." (legacy
	// fallback for ad-hoc CLI invocations). The MCP-session step is the
	// reason `yaver mcp` started from `cd ../sfmg && claude` does the
	// right thing without the AI having to pass work_dir on every
	// build_*/push_* call. See mcp_session_cwd.go.
	startDir := firstNonEmpty(args.WorkDir, s.buildMgr.workDir, ResolveMCPCwd(), ".")
	switch name {
	case "build_ios":
		extra := append([]string{}, args.Args...)
		if args.Scheme != "" {
			extra = append([]string{args.Scheme}, extra...)
		}
		return true, startMCPNativeReleaseBuild(s, NativeIOS, startDir, args.ProjectPath, args.ProjectIndex, extra)
	case "build_android":
		extra := append([]string{}, args.Args...)
		if args.Flavor != "" {
			extra = append([]string{args.Flavor}, extra...)
		}
		return true, startMCPNativeReleaseBuild(s, NativeAndroid, startDir, args.ProjectPath, args.ProjectIndex, extra)
	case "push_ios":
		extra := append([]string{}, args.Args...)
		if args.Scheme != "" {
			extra = append([]string{args.Scheme}, extra...)
		}
		return true, runMCPNativePush(s, NativeIOS, startDir, args.ProjectPath, args.ProjectIndex, extra)
	case "push_android":
		extra := append([]string{}, args.Args...)
		if args.Flavor != "" {
			extra = append([]string{args.Flavor}, extra...)
		}
		return true, runMCPNativePush(s, NativeAndroid, startDir, args.ProjectPath, args.ProjectIndex, extra)
	}

	if !isNativeAlias(args.Platform) {
		return true, mcpToolError("platform must be one of: iosNative, androidNative, flutter")
	}
	extra := []string{}
	if args.Platform == NativeIOS && args.Scheme != "" {
		extra = append(extra, args.Scheme)
	}
	if (args.Platform == NativeAndroid || args.Platform == "android-native") && args.Flavor != "" {
		extra = append(extra, args.Flavor)
	}
	extra = append(extra, args.Args...)

	install := false
	switch args.Target {
	case "", "device", "simulator", "sim", "emulator", "emu":
		install = true
	}
	if args.InstallOnDevice != nil {
		install = *args.InstallOnDevice
	}
	return true, startMCPNativeBuild(s, args.Platform, args.Target, startDir, args.ProjectPath, args.ProjectIndex, extra, install)
}
