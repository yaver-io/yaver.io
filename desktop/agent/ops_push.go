package main

// ops_push.go — verb "push": the one-handler-to-rule-them-all for
// "move bits somewhere". Today this is a thin dispatcher that picks
// between:
//
//   target=phone   → push Hermes bundle to the user's physical phone
//                    via phone_project_push / the bundle loader
//   target=docker  → docker push <image>
//   target=prisma  → prisma db push
//   target=drizzle → drizzle-kit push
//   target=git     → git push (optionally to a specific remote/branch)
//
// Each path hands back a streamId when the underlying tool streams.
// All of these have dedicated MCP tools today; the verb consolidates
// the UX so an agent that just says "push this RN app to my phone"
// doesn't have to pick the right one of six tools first.

import (
	"encoding/json"
	"fmt"
	"strings"
)

type opsPushPayload struct {
	// Target: phone | docker | prisma | drizzle | git
	Target string `json:"target"`
	// WorkDir: project root. Defaults to agent CWD.
	WorkDir string `json:"workDir,omitempty"`
	// Image: for docker pushes. Full tag (repo:tag).
	Image string `json:"image,omitempty"`
	// Remote / Branch: for git pushes. Default: origin / current.
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
	// DeviceId: optional phone id; defaults to the first online phone
	// the agent knows about.
	DeviceId string `json:"deviceId,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "push",
		Description: "Push bits to a runtime. target=phone (Hermes bundle), docker (image push), prisma/drizzle (schema push), git (commit push). One verb, one payload discriminator — no need to remember which domain tool to call.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"target"},
			"properties": map[string]interface{}{
				"target":   map[string]interface{}{"type": "string", "enum": []string{"phone", "docker", "prisma", "drizzle", "git"}},
				"workDir":  map[string]interface{}{"type": "string"},
				"image":    map[string]interface{}{"type": "string"},
				"remote":   map[string]interface{}{"type": "string"},
				"branch":   map[string]interface{}{"type": "string"},
				"deviceId": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsPushHandler,
		Streaming:  true,
		AllowGuest: false,
	})
}

func opsPushHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsPushPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Target == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "target is required"}
	}
	if c.Server == nil || c.Server.execMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "exec manager not initialised"}
	}
	workDir := p.WorkDir
	if workDir == "" {
		// Fall back to the AI session's pinned cwd before bare ".",
		// so an agent that runs `ops.push target=docker` from a
		// session rooted in the user's app repo pushes from there.
		// See mcp_session_cwd.go.
		if cwd := ResolveMCPCwd(); cwd != "" {
			workDir = cwd
		} else {
			workDir = "."
		}
	}

	var cmd string
	var subTool string
	switch strings.ToLower(p.Target) {
	case "phone":
		// Today the canonical path is the phone_project_push MCP tool.
		// Surface a pointer so agents either call it directly or pipe
		// through ops.push again (idempotent). The full reimplement-
		// everything route is a follow-up — doing it through the
		// existing MCP tool keeps the bundle-handling code in one
		// place while we grow the verb.
		return OpsResult{
			OK:       true,
			StreamID: "", // no subprocess; agent calls the domain MCP tool
			Initial: map[string]interface{}{
				"hint":    "call the phone_project_push MCP tool with { workDir, deviceId? } — full Hermes bundle + safe-reload pipeline lives there",
				"mcpTool": "phone_project_push",
				"workDir": workDir,
			},
		}
	case "docker":
		if p.Image == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "image required for target=docker (use repo:tag)"}
		}
		cmd = fmt.Sprintf("docker push %s", opsShellQuote(p.Image))
		subTool = "docker"
	case "prisma":
		cmd = "npx prisma db push"
		subTool = "prisma"
	case "drizzle":
		cmd = "npx drizzle-kit push"
		subTool = "drizzle"
	case "git":
		remote := p.Remote
		if remote == "" {
			remote = "origin"
		}
		branch := p.Branch
		if branch == "" {
			branch = "HEAD"
		}
		cmd = fmt.Sprintf("git push %s %s", opsShellQuote(remote), opsShellQuote(branch))
		subTool = "git"
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown target: " + p.Target}
	}

	sess, err := c.Server.execMgr.StartExec(cmd, workDir, "", nil, 0)
	if err != nil {
		return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
	}
	return OpsResult{
		OK:       true,
		StreamID: sess.ID,
		Initial: map[string]interface{}{
			"sessionId": sess.ID,
			"tool":      subTool,
			"command":   cmd,
			"workDir":   workDir,
		},
	}
}

// opsShellQuote wraps a value in single quotes so exec-manager's `sh -c`
// treats it as a single token. Scoped per-file to avoid colliding with
// the existing shellQuote (env.go).
func opsShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
