package main

// ops_deploy.go — verb "deploy": push the current project to a hosting
// target. Same consolidating spirit as build/push — one verb, N
// provider branches, so agents don't have to learn per-provider
// tools. Streams provider output via the execMgr.

import (
	"encoding/json"
	"fmt"
	"strings"
)

type opsDeployPayload struct {
	// Target: cloud | cloudflare | vercel | fly | netlify | railway |
	// firebase | platform | testflight | playstore | convex | eas.
	// When ops is called with machine=auto, the dispatcher uses this
	// target plus workDir/project metadata to pick the best executor.
	Target  string `json:"target"`
	WorkDir string `json:"workDir,omitempty"`
	// Env: production / staging / preview / custom.
	Env string `json:"env,omitempty"`
	// Extra args appended to the provider CLI.
	Args []string `json:"args,omitempty"`
	// TimeoutSec: kill the deploy after this many seconds. 0 = none.
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "deploy",
		Description: "Deploy the project at workDir to a hosting target. target=cloud (Yaver cloud), cloudflare, vercel, fly, netlify, railway, firebase, platform (Yaver platform), convex, eas (Expo), testflight, playstore. Streams provider output.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"target"},
			"properties": map[string]interface{}{
				"target":     map[string]interface{}{"type": "string"},
				"workDir":    map[string]interface{}{"type": "string"},
				"env":        map[string]interface{}{"type": "string"},
				"args":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"timeoutSec": map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsDeployHandler,
		Streaming:  true,
		AllowGuest: true,
	})
}

func opsDeployHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsDeployPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Target == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "target is required"}
	}
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}
	if c.Server == nil || c.Server.execMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "exec manager not initialised"}
	}

	extra := strings.Join(p.Args, " ")
	envFlag := ""
	if p.Env != "" {
		envFlag = " --env=" + opsShellQuote(p.Env)
	}

	var cmd, tool string
	switch strings.ToLower(p.Target) {
	case "cloud", "yaver-cloud":
		// Points to the existing cloud_deploy tool; its full
		// implementation handles plan + provision + push.
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "call cloud_deploy MCP tool — handles plan + provision + push",
			"mcpTool": "cloud_deploy",
		}}
	case "cloudflare", "cf", "workers":
		cmd, tool = "npx wrangler deploy"+envFlag+" "+extra, "cloudflare"
	case "pages":
		cmd, tool = "npx wrangler pages deploy "+extra, "cloudflare-pages"
	case "vercel":
		prod := ""
		if p.Env == "production" || p.Env == "prod" {
			prod = " --prod"
		}
		cmd, tool = "npx vercel"+prod+" "+extra, "vercel"
	case "fly", "fly.io":
		cmd, tool = "flyctl deploy "+extra, "fly"
	case "netlify":
		prod := ""
		if p.Env == "production" || p.Env == "prod" {
			prod = " --prod"
		}
		cmd, tool = "npx netlify-cli deploy"+prod+" "+extra, "netlify"
	case "railway":
		cmd, tool = "railway up "+extra, "railway"
	case "firebase":
		cmd, tool = "firebase deploy "+extra, "firebase"
	case "convex":
		cmd, tool = "npx convex deploy "+extra, "convex"
	case "eas", "expo":
		cmd, tool = "eas submit "+extra, "eas"
	case "platform":
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "call platform_deploy MCP tool — Yaver-managed apps lifecycle",
			"mcpTool": "platform_deploy",
		}}
	case "testflight":
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "call mobile_project_build with {platform: \"ios\", track: \"testflight\"} — handles archive + export + App Store Connect upload",
			"mcpTool": "mobile_project_build",
		}}
	case "playstore", "play":
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "call mobile_project_build with {platform: \"android\", track: \"internal\"} — handles AAB + service-account upload",
			"mcpTool": "mobile_project_build",
		}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown target: " + p.Target}
	}

	sess, err := c.Server.execMgr.StartExec(strings.TrimSpace(cmd), workDir, "", nil, p.TimeoutSec)
	if err != nil {
		return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
	}
	return OpsResult{
		OK:       true,
		StreamID: sess.ID,
		Initial: map[string]interface{}{
			"sessionId": sess.ID,
			"tool":      tool,
			"command":   strings.TrimSpace(cmd),
			"workDir":   workDir,
			"env":       p.Env,
			"sseHint":   fmt.Sprintf("/exec/%s/stream for live output", sess.ID),
		},
	}
}
