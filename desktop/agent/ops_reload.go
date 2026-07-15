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

// forwardGuestIdentity copies the server-stamped guest headers from the
// authenticated /ops request onto a request we forge to call a dev handler
// in-process.
//
// Both dev handlers below authorize guests by HEADER, not by argument:
// requireGuestAccessToActiveDevServer checks the active project against the
// guest's shared set, and (bundle mode) isolatedGuestDevMutationBlocked
// refuses a guest configured to require isolation. Both read
// X-Yaver-GuestUserID and read "" as "the owner is calling" — so a forged
// request carrying no headers walks through both gates as an owner.
//
// This is defence in depth, not a plugged hole: today no guest reaches this
// handler, because dispatchOps authorizes first (authorizeGuestOpsExecution
// allows a deploy-scope guest only info/status/deploy, and no other guest
// scope has /ops on its path allow-list at all). But `reload` is declared
// AllowGuest:true — "guests with dev-server scope already hit /dev/reload" —
// so the verb INTENDS to be guest-reachable, and the only thing standing in
// the way is a separate allow-list in a different file. The day `reload` is
// added there, the project gate must already hold. It holds now.
//
// These values are safe to carry inward because they are not caller-supplied:
// the auth middleware strips every inbound X-Yaver-Guest* header and re-stamps
// them from server-resolved state (stripGuestRequestHeaders, httpserver.go).
func forwardGuestIdentity(dst *http.Request, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for _, name := range []string{
		"X-Yaver-Guest",
		"X-Yaver-GuestUserID",
		"X-Yaver-GuestScope",
		"X-Yaver-GuestAllowedProjects",
	} {
		if v := src.Get(name); v != "" {
			dst.Header.Set(name, v)
		}
	}
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
		req, _ := http.NewRequest(http.MethodPost, "/dev/reload", nil)
		forwardGuestIdentity(req, c.RequestHeaders)
		rec := newCapturingResponseWriter()
		c.Server.handleDevServerReload(rec, req)
		if rec.Status() >= 300 {
			return OpsResult{
				OK:    false,
				Code:  "reload_failed",
				Error: fmt.Sprintf("reload returned HTTP %d: %s", rec.Status(), strings.TrimSpace(string(rec.Body()))),
			}
		}
		var initial map[string]interface{}
		if err := json.Unmarshal(rec.Body(), &initial); err != nil {
			initial = map[string]interface{}{
				"mode": "dev",
				"raw":  strings.TrimSpace(string(rec.Body())),
			}
		}
		initial["mode"] = "dev"
		return OpsResult{OK: true, Initial: initial}
	case "bundle":
		workDir := p.WorkDir
		if workDir == "" {
			workDir = workDirFromEnv()
		}
		body, _ := json.Marshal(map[string]string{
			"mode":        "bundle",
			"projectPath": workDir,
		})
		req, _ := http.NewRequest(http.MethodPost, "/dev/reload-app", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		forwardGuestIdentity(req, c.RequestHeaders)
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
