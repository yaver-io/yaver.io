package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DeployPreview is the pre-flight summary shown to the user before they
// commit to `/deploy/run`. It answers: "what's going to happen?" — git state,
// CI gate presence, DB migrations, healthcheck target, active env, any
// obvious footguns.
type DeployPreview struct {
	ProjectDir    string          `json:"projectDir"`
	Branch        string          `json:"branch,omitempty"`
	Ahead         int             `json:"ahead"`
	Behind        int             `json:"behind"`
	Dirty         bool            `json:"dirty"`
	DirtyFiles    []string        `json:"dirtyFiles,omitempty"`
	LastCommit    string          `json:"lastCommit,omitempty"`
	LastMessage   string          `json:"lastMessage,omitempty"`
	CIConfigured  bool            `json:"ciConfigured"`
	CISteps       int             `json:"ciSteps"`
	CIOnFail      string          `json:"ciOnFail,omitempty"`
	Migrator      string          `json:"migrator,omitempty"`
	MigratorCmd   string          `json:"migratorCmd,omitempty"`
	Healthcheck   string          `json:"healthcheck,omitempty"`
	HealthInferred bool           `json:"healthInferred"`
	ActiveEnv     string          `json:"activeEnv"`
	DeployConfig  DeployConfig    `json:"deployConfig"`
	Warnings      []string        `json:"warnings,omitempty"`
	LastDeploy    *DeployRecord   `json:"lastDeploy,omitempty"`
}

// BuildDeployPreview inspects the project and returns a DeployPreview.
func BuildDeployPreview(projectDir string) *DeployPreview {
	p := &DeployPreview{
		ProjectDir:   projectDir,
		DeployConfig: loadDeployConfig(projectDir),
		ActiveEnv:    ActiveEnv(projectDir),
	}

	// Git state.
	if out, err := runIn(projectDir, "git", "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		p.Branch = strings.TrimSpace(out)
	}
	if p.Branch == "" {
		p.Warnings = append(p.Warnings, "not a git repo — deploy will skip git pull")
	} else {
		if out, err := runIn(projectDir, "git", "log", "-1", "--pretty=%h %s"); err == nil {
			line := strings.TrimSpace(out)
			if sp := strings.IndexByte(line, ' '); sp > 0 {
				p.LastCommit = line[:sp]
				p.LastMessage = line[sp+1:]
			}
		}
		// ahead/behind vs origin
		if out, err := runIn(projectDir, "git", "rev-list", "--left-right", "--count",
			fmt.Sprintf("HEAD...origin/%s", p.Branch)); err == nil {
			parts := strings.Fields(strings.TrimSpace(out))
			if len(parts) == 2 {
				fmt.Sscanf(parts[0], "%d", &p.Ahead)
				fmt.Sscanf(parts[1], "%d", &p.Behind)
			}
		}
		// dirty tree
		if out, err := runIn(projectDir, "git", "status", "--porcelain"); err == nil {
			lines := strings.Split(strings.TrimSpace(out), "\n")
			for _, l := range lines {
				if l == "" {
					continue
				}
				p.DirtyFiles = append(p.DirtyFiles, strings.TrimSpace(l))
			}
			p.Dirty = len(p.DirtyFiles) > 0
		}
	}
	if p.Dirty {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d uncommitted change(s) — they will NOT deploy", len(p.DirtyFiles)))
	}
	if p.Behind > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("local branch is %d commits behind origin/%s", p.Behind, p.Branch))
	}

	// CI gate.
	if pipe, _ := LoadCIPipeline(projectDir); pipe != nil {
		p.CIConfigured = true
		p.CISteps = len(pipe.Steps)
		p.CIOnFail = pipe.OnFail
	}

	// DB migrations.
	if migrator, cmd := detectMigrator(projectDir); migrator != "" {
		p.Migrator = migrator
		p.MigratorCmd = cmd
	}

	// Healthcheck.
	if p.DeployConfig.Healthcheck != "" {
		p.Healthcheck = p.DeployConfig.Healthcheck
	} else if hc := autoInferDeployHealthcheck(projectDir); hc != "" {
		p.Healthcheck = hc
		p.HealthInferred = true
	} else {
		p.Warnings = append(p.Warnings, "no healthcheck configured — deploy won't verify readiness")
	}

	// Webhook configured but secret missing — soft warning.
	if p.DeployConfig.AutoDeploy && p.DeployConfig.WebhookSecret == "" {
		p.Warnings = append(p.Warnings, "auto-deploy on; webhook secret NOT set (any POST to /deploy/webhook triggers a deploy)")
	}

	// Active env hint (people sometimes forget they're on prod).
	if p.ActiveEnv == "production" {
		p.Warnings = append(p.Warnings, "active environment is PRODUCTION — .env.local reflects prod")
	}

	// Last deploy for context.
	hist := listDeploys(projectDir)
	if len(hist) > 0 {
		p.LastDeploy = hist[0]
	}

	// Does the project dir actually exist + have services.yaml?
	if _, err := os.Stat(filepath.Join(projectDir, ".yaver", "services.yaml")); err != nil {
		p.Warnings = append(p.Warnings, "no .yaver/services.yaml — deploy has no services to restart")
	}
	return p
}

func (s *HTTPServer) handleDeployPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, BuildDeployPreview(s.dirParam(r)))
}

// fmtlike import shim; keep this file independent.
var _ = json.Marshal
