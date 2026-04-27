package main

// MCP tool: monorepo_detect — let AI agents (Claude Desktop, Cursor, Aider,
// Codex, Goose) classify the framework composition of a directory before
// kicking off any build. Pairs with `yaver iosNative / androidNative /
// flutter` so the agent can show "this monorepo has a Flutter app + a Swift
// iOS app + a Kotlin Android app — which one do you want to push?" without
// the user typing path guesses.

import (
	"encoding/json"
	"fmt"
)

func monorepoMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "monorepo_detect",
			"description": "Walk a directory and classify every project inside it by framework " +
				"(flutter | expo | react-native | next | vite | unity | iosNative | androidNative | swift-package | gradle-jvm). " +
				"Returns a Monorepo JSON: { root, gitBranch, gitRemote, projects[], frameworks[], isMonorepo, hasManifest }. " +
				"Use this BEFORE calling native_build to confirm which frameworks are present and pick the right --target. " +
				"Skips node_modules, build, .git, vendor, .pub-cache, .next, .turbo, etc. " +
				"`dir` defaults to the agent's current work directory when omitted.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Directory to classify (absolute path or relative to agent cwd). Defaults to cwd.",
					},
					"max_depth": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum recursion depth (1–12). Default 6.",
					},
				},
			},
		},
	}
}

func dispatchMonorepoMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	if name != "monorepo_detect" {
		return false, nil
	}
	var args struct {
		Dir      string `json:"dir"`
		MaxDepth int    `json:"max_depth"`
	}
	_ = json.Unmarshal(arguments, &args)

	dir := args.Dir
	if dir == "" {
		return true, mcpToolError("dir is required (no agent work directory configured)")
	}

	mr, err := DetectMonorepo(dir, DetectOpts{MaxDepth: args.MaxDepth})
	if err != nil {
		return true, mcpToolError(fmt.Sprintf("monorepo_detect: %v", err))
	}
	return true, mcpToolJSON(mr)
}
