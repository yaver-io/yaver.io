package main

// MCP tool: native_build — single entrypoint for AI agents (Claude Desktop,
// Cursor, Aider, Codex, Goose) to trigger a native iOS Swift, native Android
// Kotlin, or Flutter build/install/upload from a headless dev machine. Pairs
// with the `yaver iosNative` / `yaver androidNative` / `yaver flutter` CLI
// verbs and the matching POST /builds aliases — same orchestration, three
// different surfaces.

import (
	"encoding/json"
	"fmt"
)

// nativeBuildMCPTools returns the schema for `native_build`. Appended into the
// master tool list by mcpBuildToolList (mcp_tools.go).
func nativeBuildMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "native_build",
			"description": "Build & deploy a native iOS Swift, native Android Kotlin, or Flutter app from this dev machine. " +
				"`platform` picks the toolchain (iosNative | androidNative | flutter); `target` picks where the artifact " +
				"goes (device | simulator | testflight | playstore | local). On `device`/`simulator` the build is auto-installed " +
				"on the connected iPhone (xcrun devicectl) or Android device/emulator (adb install -r). React Native + Hermes paths " +
				"are NOT used — for RN see mobile_project_build / dev_start. Returns the Build object with id, status, command, " +
				"and (once finished) artifactPath. Stream live logs from /streams/exec:<execId> or poll /builds/<id>.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"platform"},
				"properties": map[string]interface{}{
					"platform": map[string]interface{}{
						"type":        "string",
						"description": "iosNative | androidNative | flutter",
						"enum":        []string{"iosNative", "ios-native", "androidNative", "android-native", "flutter"},
					},
					"target": map[string]interface{}{
						"type":        "string",
						"description": "device (default) | simulator | testflight | playstore | local | apk | aab | ipa",
					},
					"work_dir": map[string]interface{}{
						"type":        "string",
						"description": "Project directory (defaults to the agent's current work dir)",
					},
					"scheme": map[string]interface{}{
						"type":        "string",
						"description": "Xcode scheme override (iosNative only)",
					},
					"flavor": map[string]interface{}{
						"type":        "string",
						"description": "Gradle task / product flavor override (androidNative only)",
					},
					"args": map[string]interface{}{
						"type":        "array",
						"description": "Optional extra args appended to the build command",
						"items":       map[string]interface{}{"type": "string"},
					},
					"install_on_device": map[string]interface{}{
						"type":        "boolean",
						"description": "Force the on-device install branch (default: true when target=device|simulator)",
					},
				},
			},
		},
	}
}

// dispatchNativeBuildMCP handles the `native_build` tool call. Returns
// (handled, result). If handled=false, the outer dispatcher continues.
func dispatchNativeBuildMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	if name != "native_build" {
		return false, nil
	}
	if s == nil || s.buildMgr == nil {
		return true, mcpToolError("builds not available on this agent")
	}

	var args struct {
		Platform        string   `json:"platform"`
		Target          string   `json:"target"`
		WorkDir         string   `json:"work_dir"`
		Scheme          string   `json:"scheme"`
		Flavor          string   `json:"flavor"`
		Args            []string `json:"args"`
		InstallOnDevice *bool    `json:"install_on_device"`
	}
	_ = json.Unmarshal(arguments, &args)

	if !isNativeAlias(args.Platform) {
		return true, mcpToolError("platform must be one of: iosNative, androidNative, flutter")
	}
	platform, err := resolveNativePlatform(args.Platform, args.Target)
	if err != nil {
		return true, mcpToolError(err.Error())
	}

	// Compose extra args mirroring native_build.go:runNativeBuild.
	extra := []string{}
	if args.Platform == NativeIOS && args.Scheme != "" {
		extra = append(extra, args.Scheme)
	}
	if (args.Platform == NativeAndroid || args.Platform == "android-native") && args.Flavor != "" {
		extra = append(extra, args.Flavor)
	}
	extra = append(extra, args.Args...)

	// Default install-on-device for device/simulator targets.
	install := false
	switch args.Target {
	case "", "device", "simulator", "sim", "emulator", "emu":
		install = true
	}
	if args.InstallOnDevice != nil {
		install = *args.InstallOnDevice
	}

	build, err := s.buildMgr.StartBuild(platform, args.WorkDir, extra, install)
	if err != nil {
		return true, mcpToolError(err.Error())
	}

	return true, mcpToolJSON(map[string]interface{}{
		"id":              build.ID,
		"platform":        build.Platform,
		"command":         build.Command,
		"workDir":         build.WorkDir,
		"status":          build.Status,
		"installOnDevice": build.InstallOnDevice,
		"native":          args.Platform,
		"target":          firstNonEmpty(args.Target, "device"),
		"streamPath":      fmt.Sprintf("/builds/%s/stream", build.ID),
		"statusPath":      fmt.Sprintf("/builds/%s", build.ID),
	})
}

