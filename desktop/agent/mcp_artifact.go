package main

// mcp_artifact.go — discoverable artifact-return tool.
//
// Thin adapter over the artifact_fetch ops verb (ops_artifact.go), in the
// robot_camera/circuit_plot image-first pattern: an image file is returned as a
// viewable MCP image block, anything else as a bounded base64 payload plus
// mime/size/sha256. Reuses the ops mesh path, so `machine` fetches from another
// owned box.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func artifactMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "artifact_fetch",
			"description": "Fetch a file's bytes from an allowed location (a discovered project root, " +
				"~/.yaver/artifacts, or YAVER_ARTIFACT_ROOTS) so a compiled PDF, a .dwg, a screenshot or a build " +
				"output can reach you. Images come back as a viewable image block; other files as base64 + " +
				"mime + size + sha256, bounded at 12 MB.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]interface{}{
					"path":     map[string]interface{}{"type": "string", "description": "Absolute path (or ~/…) inside an allowed root."},
					"machine":  map[string]interface{}{"type": "string", "description": "Owned device to read from (default: local)."},
					"maxBytes": map[string]interface{}{"type": "integer", "description": "Optional lower cap (hard max 12 MB)."},
				},
			},
		},
	}
}

func (s *HTTPServer) handleArtifactMCP(args json.RawMessage) interface{} {
	var a struct {
		Path     string `json:"path"`
		Machine  string `json:"machine"`
		MaxBytes int    `json:"maxBytes"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return mcpToolError("invalid artifact_fetch arguments: " + err.Error())
		}
	}
	if strings.TrimSpace(a.Path) == "" {
		return mcpToolError("artifact_fetch: `path` is required")
	}
	machine := strings.TrimSpace(a.Machine)
	if machine == "" {
		machine = "local"
	}
	payload := map[string]interface{}{"path": a.Path}
	if a.MaxBytes != 0 {
		payload["maxBytes"] = a.MaxBytes
	}
	raw, _ := json.Marshal(payload)
	octx := OpsContext{Ctx: context.Background(), Server: s, Caller: "owner"}
	out := dispatchOps(octx, OpsRequest{Machine: machine, Verb: "artifact_fetch", Payload: raw})
	if !out.OK {
		return mcpToolError("artifact_fetch (" + machine + "): " + opsErrText(out))
	}
	m, _ := out.Initial.(map[string]interface{})
	b64, _ := m["base64"].(string)
	mime, _ := m["mime"].(string)
	if b64 == "" {
		return mcpToolError("artifact_fetch (" + machine + "): no bytes returned")
	}
	summary := fmt.Sprintf("Fetched %v (%v bytes, %v, sha256 %v) from %s.",
		m["name"], m["size"], mime, m["sha256"], machine)
	content := []map[string]interface{}{{"type": "text", "text": summary}}
	if strings.HasPrefix(mime, "image/") {
		content = append(content, map[string]interface{}{"type": "image", "data": b64, "mimeType": mime})
	}
	return map[string]interface{}{"content": content}
}
