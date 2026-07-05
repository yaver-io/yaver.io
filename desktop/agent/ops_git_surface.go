package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ops_git_surface.go — provider-neutral GitHub/GitLab read verbs for
// constrained surfaces. The heavy lifting already exists in the MCP wrappers
// around gh/glab; these verbs give car/watch/TV/mobile a stable, small contract.

type gitSurfacePayload struct {
	Provider  string `json:"provider,omitempty"`  // auto | github | gitlab
	Directory string `json:"directory,omitempty"` // repo path; empty = agent cwd
	State     string `json:"state,omitempty"`     // open/opened/closed/merged/all
	Limit     int    `json:"limit,omitempty"`     // advisory; underlying tools cap themselves
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_prs",
		Description: "Driving/watch-safe pull/merge request summary for GitHub or GitLab. Payload {provider?: auto|github|gitlab, directory?, state?}. Read-only.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":  map[string]interface{}{"type": "string"},
				"directory": map[string]interface{}{"type": "string"},
				"state":     map[string]interface{}{"type": "string"},
				"limit":     map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 50},
			},
			"additionalProperties": false,
		},
		Handler: gitPRsOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "git_issues",
		Description: "Driving/watch-safe issue summary for GitHub or GitLab. Payload {provider?: auto|github|gitlab, directory?, state?}. Read-only.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":  map[string]interface{}{"type": "string"},
				"directory": map[string]interface{}{"type": "string"},
				"state":     map[string]interface{}{"type": "string"},
				"limit":     map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 50},
			},
			"additionalProperties": false,
		},
		Handler: gitIssuesOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "git_ci_status",
		Description: "Driving/watch-safe CI status summary for GitHub Actions or GitLab CI. Payload {provider?: auto|github|gitlab, directory?}. Read-only.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":  map[string]interface{}{"type": "string"},
				"directory": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: gitCIStatusOpsHandler,
	})
}

func gitPRsOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	p, provider, err := parseGitSurfacePayload(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	state := strings.TrimSpace(p.State)
	var raw interface{}
	switch provider {
	case "github":
		raw = mcpGitHubPRs(p.Directory, state)
	case "gitlab":
		if state == "open" {
			state = "opened"
		}
		raw = mcpGitLabMRs(p.Directory, state)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "provider must be github or gitlab"}
	}
	if err := mcpMapError(raw); err != "" {
		return OpsResult{OK: false, Code: "git_error", Error: err, Initial: raw}
	}
	count := gitSurfaceCount(raw, "pull_requests", "merge_requests")
	label := "pull requests"
	if provider == "gitlab" {
		label = "merge requests"
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"provider": provider,
		"kind":     "prs",
		"count":    count,
		"raw":      raw,
		"spoken":   fmt.Sprintf("%s has %d %s.", gitProviderDisplay(provider), count, label),
	}}
}

func gitIssuesOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	p, provider, err := parseGitSurfacePayload(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	state := strings.TrimSpace(p.State)
	var raw interface{}
	switch provider {
	case "github":
		raw = mcpGitHubIssues(p.Directory, state)
	case "gitlab":
		if state == "open" {
			state = "opened"
		}
		raw = mcpGitLabIssues(p.Directory, state)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "provider must be github or gitlab"}
	}
	if err := mcpMapError(raw); err != "" {
		return OpsResult{OK: false, Code: "git_error", Error: err, Initial: raw}
	}
	count := gitSurfaceCount(raw, "issues")
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"provider": provider,
		"kind":     "issues",
		"count":    count,
		"raw":      raw,
		"spoken":   fmt.Sprintf("%s has %d issues.", gitProviderDisplay(provider), count),
	}}
}

func gitCIStatusOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	p, provider, err := parseGitSurfacePayload(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	var raw interface{}
	switch provider {
	case "github":
		raw = mcpGitHubCIStatus(p.Directory)
	case "gitlab":
		raw = mcpGitLabCI(p.Directory)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "provider must be github or gitlab"}
	}
	if err := mcpMapError(raw); err != "" {
		return OpsResult{OK: false, Code: "git_error", Error: err, Initial: raw}
	}
	count := gitSurfaceCount(raw, "runs", "pipelines")
	spoken := fmt.Sprintf("%s CI status returned %d recent entries.", gitProviderDisplay(provider), count)
	if count == 0 {
		spoken = fmt.Sprintf("%s CI status is available, but no recent entries were returned.", gitProviderDisplay(provider))
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"provider": provider,
		"kind":     "ci",
		"count":    count,
		"raw":      raw,
		"spoken":   spoken,
	}}
}

func parseGitSurfacePayload(payload json.RawMessage) (gitSurfacePayload, string, error) {
	var p gitSurfacePayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return p, "", err
		}
	}
	provider := normalizeGitSurfaceProvider(p.Provider)
	if provider == "auto" {
		provider = inferGitProviderFromDir(p.Directory)
	}
	if provider == "auto" || provider == "" {
		provider = "github"
	}
	return p, provider, nil
}

func normalizeGitSurfaceProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "auto":
		return "auto"
	case "gh", "github.com":
		return "github"
	case "gl", "glab", "gitlab.com":
		return "gitlab"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func inferGitProviderFromDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "auto"
	}
	if out, err := runCmdDir(dir, "git", "remote", "get-url", "origin"); err == nil {
		remote := strings.ToLower(strings.TrimSpace(out))
		if strings.Contains(remote, "github.com") {
			return "github"
		}
		if strings.Contains(remote, "gitlab") {
			return "gitlab"
		}
	}
	return "auto"
}

func gitProviderDisplay(provider string) string {
	if provider == "gitlab" {
		return "GitLab"
	}
	return "GitHub"
}

func gitSurfaceCount(raw interface{}, keys ...string) int {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return 0
	}
	for _, key := range keys {
		if arr, ok := m[key].([]interface{}); ok {
			return len(arr)
		}
	}
	if output, ok := m["output"].(string); ok {
		lines := 0
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) != "" {
				lines++
			}
		}
		return lines
	}
	return 0
}

func mcpMapError(raw interface{}) string {
	if m, ok := raw.(map[string]interface{}); ok {
		if err, has := m["error"]; has && err != nil && strings.TrimSpace(fmt.Sprint(err)) != "" {
			return strings.TrimSpace(fmt.Sprint(err))
		}
	}
	return ""
}
