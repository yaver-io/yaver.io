package main

// deploy_script_http.go — HTTP endpoints for the deploy-script generator
// and toolchain doctor. Owner-only (not in any guest allowlist — see
// httpserver.go::allowGuest). The endpoints are intentionally read-only:
// they return a script/report as JSON; actually running the script is
// a separate, larger concern that belongs in /deploy/run (not shipped
// yet — see DEPLOY_RUN_TODO notes).

import (
	"encoding/json"
	"net/http"
)

// handleDoctorBuild: GET /doctor/build?target=X&project=Y
//
// Returns a BuildDoctorReport (JSON). If target is empty, returns
// reports for every known target in an array.
func (s *HTTPServer) handleDoctorBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	target := r.URL.Query().Get("target")
	project := r.URL.Query().Get("project")

	targets := BuildTargetNames()
	if target != "" {
		if _, ok := buildTargets[target]; !ok {
			jsonReply(w, http.StatusBadRequest, map[string]interface{}{
				"error": "unknown target",
				"known": targets,
			})
			return
		}
		targets = []string{target}
	}
	var reports []BuildDoctorReport
	for _, t := range targets {
		rep, err := RunBuildDoctor(t, project, s.vaultStore)
		if err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		reports = append(reports, rep)
	}
	if target != "" && len(reports) == 1 {
		jsonReply(w, http.StatusOK, reports[0])
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"reports": reports})
}

// handleDeployTemplates: GET /deploy/templates
//
// Lists the (stack, target) pairs the generator supports + their
// required tools and secrets. Useful for UIs that want to show the user
// the supported combos.
func (s *HTTPServer) handleDeployTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	type templateInfo struct {
		Stack       string   `json:"stack"`
		Target      string   `json:"target"`
		Description string   `json:"description"`
		Tools       []string `json:"tools"`
		Secrets     []string `json:"secrets,omitempty"`
	}
	out := make([]templateInfo, 0, len(deployTemplates))
	for _, k := range DeployTemplateNames() {
		t := deployTemplates[k]
		info := templateInfo{
			Stack:       t.Stack,
			Target:      t.Target,
			Description: t.Description,
		}
		if bt, ok := buildTargets[t.Target]; ok {
			for _, tool := range bt.Tools {
				info.Tools = append(info.Tools, tool.Name)
			}
			info.Secrets = bt.Secrets
		}
		out = append(out, info)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"templates": out})
}

// handleDeployGenerate: POST /deploy/generate
//
// Body:
//
//	{ "app": "web", "target": "cloudflare", "stack": "nextjs", "path": "..." }
//
// stack + path are optional — if omitted the workspace manifest is used
// (if present in cwd's ancestry) to resolve both.
//
// Response: { "script": "...", "app": "...", "target": "...", ... }
func (s *HTTPServer) handleDeployGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		App    string `json:"app"`
		Target string `json:"target"`
		Stack  string `json:"stack"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if body.App == "" || body.Target == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "app and target are required"})
		return
	}
	if body.Stack == "" || body.Path == "" {
		if ref, err := resolveProjectRef(body.App, body.Path); err == nil {
			if body.Stack == "" {
				body.Stack = ref.Stack
			}
			if body.Path == "" {
				body.Path = ref.Path
			}
		}
	}
	if body.Stack == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "could not resolve stack — pass explicitly"})
		return
	}
	script, err := GenerateDeployScript(DeployScriptSpec{
		App:    body.App,
		Stack:  body.Stack,
		Target: body.Target,
		Path:   body.Path,
	})
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"app":    body.App,
		"target": body.Target,
		"stack":  body.Stack,
		"path":   body.Path,
		"script": script,
	})
}
