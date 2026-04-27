package main

// ops_reload.go — verb "reload": trigger hot-reload on the dev server
// the agent is currently hosting. Mode=dev pokes the DevServerManager
// (metro/vite/next/flutter) to reload connected clients. Mode=bundle
// rebuilds Hermes bytecode and pushes it to any connected phone via
// the existing /dev/reload-app path.
//
// Thin wrapper over existing /dev/reload + /dev/reload-app so an
// MCP caller in a different tool gets one verb for "refresh the
// running app" without remembering which endpoint handles which mode.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type opsReloadPayload struct {
	// Mode: "dev" | "bundle". Defaults to "dev".
	Mode string `json:"mode,omitempty"`
	// WorkDir: project root. Only used for bundle mode.
	WorkDir string `json:"workDir,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "reload",
		Description: "Hot-reload the in-flight dev server (metro/vite/next/flutter) or rebuild + push a fresh Hermes bundle to a physical phone. Mode=dev is a hot reload; mode=bundle is a full bundle swap.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"mode":    map[string]interface{}{"type": "string", "enum": []string{"dev", "bundle"}, "default": "dev"},
				"workDir": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsReloadHandler,
		Streaming:  false, // short operation
		AllowGuest: true,  // guests with dev-server scope already hit /dev/reload
	})
}

func opsReloadHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsReloadPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	mode := strings.ToLower(p.Mode)
	if mode == "" {
		mode = "dev"
	}
	if c.Server == nil || c.Server.devServerMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "dev server manager not initialised"}
	}

	switch mode {
	case "dev":
		status := c.Server.devServerMgr.Status()
		if status == nil || !status.Running {
			return OpsResult{OK: false, Code: "not_running", Error: "no dev server is currently running — start one with /dev/start or the devServer.start() mobile-headless call first"}
		}
		if err := c.Server.devServerMgr.Reload(); err != nil {
			return OpsResult{OK: false, Code: "reload_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"mode":      "dev",
			"framework": status.Framework,
			"reloaded":  true,
		}}
	case "bundle":
		workDir := p.WorkDir
		if workDir == "" {
			workDir = workDirFromEnv()
		}
		body, _ := json.Marshal(map[string]string{
			"mode":    "bundle",
			"workDir": workDir,
		})
		req, _ := http.NewRequest(http.MethodPost, "/dev/reload-app", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := newCapturingResponseWriter()
		c.Server.handleReloadApp(rec, req)
		if rec.Status() >= 300 {
			return OpsResult{
				OK:    false,
				Code:  "reload_failed",
				Error: fmt.Sprintf("reload-app returned HTTP %d: %s", rec.Status(), strings.TrimSpace(string(rec.Body()))),
			}
		}
		var initial map[string]interface{}
		if err := json.Unmarshal(rec.Body(), &initial); err != nil {
			initial = map[string]interface{}{
				"mode":    "bundle",
				"workDir": workDir,
				"raw":     strings.TrimSpace(string(rec.Body())),
			}
		}
		initial["mode"] = "bundle"
		initial["workDir"] = workDir
		return OpsResult{OK: true, Initial: initial}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "mode must be dev or bundle"}
	}
}
