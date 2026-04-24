package main

// ops_web_preview.go — verb "web-preview": start / reload / stop the
// agent's dev server for a named workspace app and return the iframe
// URL the Web Reload dashboard tab (or any other iframe surface) can
// embed. Owner-only by design — starting a dev server writes into
// the user's project tree.

import (
	"encoding/json"
	"fmt"
	"strings"
)

type opsWebPreviewPayload struct {
	// App: workspace manifest app name. Preferred over WorkDir.
	App string `json:"app,omitempty"`
	// WorkDir: absolute project path. Used when App is empty.
	WorkDir string `json:"workDir,omitempty"`
	// Action: "start" | "reload" | "stop" | "status". Defaults to "start".
	Action string `json:"action,omitempty"`
	// Root: workspace root override (for monorepo lookup).
	Root string `json:"root,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "web-preview",
		Description: "Start / reload / stop a web dev server (Next.js, Vite, Flutter Web) for a workspace app and return the iframe URL.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"app":     map[string]interface{}{"type": "string", "description": "workspace app name"},
				"workDir": map[string]interface{}{"type": "string", "description": "absolute project path (when app is empty)"},
				"action":  map[string]interface{}{"type": "string", "enum": []string{"start", "reload", "stop", "status"}, "default": "start"},
				"root":    map[string]interface{}{"type": "string", "description": "workspace root override"},
			},
			"additionalProperties": false,
		},
		Handler:    opsWebPreviewHandler,
		Streaming:  false,
		AllowGuest: false, // dev-server start modifies project state
	})
}

func opsWebPreviewHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsWebPreviewPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if c.Server == nil || c.Server.devServerMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "dev server manager not initialised"}
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if action == "" {
		action = "start"
	}

	switch action {
	case "status":
		st := c.Server.devServerMgr.Status()
		if st == nil {
			return OpsResult{OK: true, Initial: map[string]interface{}{"running": false}}
		}
		return OpsResult{OK: true, Initial: st}

	case "reload":
		st := c.Server.devServerMgr.Status()
		if st == nil || !st.Running {
			return OpsResult{OK: false, Code: "not_running", Error: "no dev server running"}
		}
		if st.Kind == DevServerKindMobile {
			return OpsResult{OK: false, Code: "wrong_surface", Error: fmt.Sprintf("active dev server is mobile (%s); use ops reload for mobile", st.Framework)}
		}
		if err := c.Server.devServerMgr.Reload(); err != nil {
			return OpsResult{OK: false, Code: "reload_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"reloaded":  true,
			"framework": st.Framework,
			"kind":      st.Kind,
		}}

	case "stop":
		if err := c.Server.devServerMgr.Stop(); err != nil {
			return OpsResult{OK: false, Code: "stop_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"running": false}}

	case "start":
		// Resolve app → workDir + framework via the workspace manifest.
		framework := ""
		workDir := p.WorkDir
		if strings.TrimSpace(p.App) != "" {
			root := strings.TrimSpace(p.Root)
			if root == "" {
				if c.Server.taskMgr != nil && c.Server.taskMgr.workDir != "" {
					root = c.Server.taskMgr.workDir
				}
			}
			m, _, err := loadWorkspaceManifestForHTTP(root)
			if err != nil {
				return OpsResult{OK: false, Code: "manifest_missing", Error: err.Error()}
			}
			var matched *WorkspaceApp
			for i := range m.Apps {
				if m.Apps[i].Name == p.App {
					matched = &m.Apps[i]
					break
				}
			}
			if matched == nil {
				return OpsResult{OK: false, Code: "unknown_app", Error: fmt.Sprintf("app %q not in workspace manifest", p.App)}
			}
			kind := StackToDevServerKind(matched.Stack)
			if kind == "" || kind == DevServerKindMobile {
				return OpsResult{OK: false, Code: "wrong_surface", Error: fmt.Sprintf("app %q is not a web surface (stack=%s)", matched.Name, matched.Stack)}
			}
			workDir = appAbsPath(root, m, matched)
			framework = StackToFramework(matched.Stack)
		}
		if workDir == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "app or workDir is required"}
		}
		if err := c.Server.devServerMgr.Start(framework, workDir, "web", 0, DevServerTarget{}); err != nil {
			return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
		}
		st := c.Server.devServerMgr.Status()
		iframeURL := ""
		if st != nil && st.Running {
			iframeURL = st.BundleURL
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"status":    st,
			"iframeUrl": iframeURL,
		}}

	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "action must be start, reload, stop, or status"}
	}
}
