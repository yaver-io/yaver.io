package main

import (
	"context"
	"encoding/json"
)

func devEnvironmentCloneMCPTools() []map[string]interface{} {
	props := map[string]interface{}{
		"sourceDeviceId": map[string]interface{}{"type": "string", "description": "Optional source Yaver device id; empty means this machine."},
		"targetDeviceId": map[string]interface{}{"type": "string", "description": "Existing target Yaver device id/name/alias."},
		"target": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"mode":                  map[string]interface{}{"type": "string", "enum": []string{"existing-device", "ssh", "managed-cloud"}},
				"deviceId":              map[string]interface{}{"type": "string"},
				"sshHost":               map[string]interface{}{"type": "string"},
				"sshUser":               map[string]interface{}{"type": "string"},
				"managedCloudMachineId": map[string]interface{}{"type": "string"},
			},
		},
		"repos": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type":     "object",
				"required": []string{"url"},
				"properties": map[string]interface{}{
					"url":            map[string]interface{}{"type": "string"},
					"branch":         map[string]interface{}{"type": "string"},
					"dir":            map[string]interface{}{"type": "string"},
					"autoInit":       map[string]interface{}{"type": "boolean"},
					"autoInitRunner": map[string]interface{}{"type": "string"},
				},
			},
		},
		"includeDiscoveredRepos": map[string]interface{}{"type": "boolean", "description": "Infer repos from local discovered projects when possible."},
		"installMissing":         map[string]interface{}{"type": "boolean", "description": "Install missing supported tools on target."},
		"includeGitCredentials":  map[string]interface{}{"type": "boolean", "description": "P2P transfer git clone credentials to target. Never Convex."},
		"skipConfigs":            map[string]interface{}{"type": "boolean", "description": "Skip allowlisted developer config clone (.vimrc, nvim, tmux, shell rc, i3, terminal, runner configs)."},
		"configKeys":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional subset of config keys to clone."},
		"syncKinds":              map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"configureCode":          map[string]interface{}{"type": "boolean", "description": "Set the first cloned repo as yaver code repo on target."},
		"runnerIds":              map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"verify":                 map[string]interface{}{"type": "boolean"},
		"dryRun":                 map[string]interface{}{"type": "boolean"},
	}
	return []map[string]interface{}{
		{
			"name":        "dev_environment_clone_plan",
			"description": "Plan cloning a coding-focused Yaver dev environment to an existing Yaver device, SSH host, or managed-cloud target. No side effects.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": props},
		},
		{
			"name":        "dev_environment_clone_start",
			"description": "Start cloning a coding-focused Yaver dev environment. Reuses toolchain sync, repo clone, runner auth verification, and yaver code config.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": props},
		},
		{
			"name":        "dev_environment_clone_status",
			"description": "Read a dev environment clone job by id.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
}

func dispatchDevEnvironmentCloneMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "dev_environment_clone_plan":
		var req DevEnvironmentCloneRequest
		if err := json.Unmarshal(arguments, &req); err != nil {
			return true, mcpToolError("bad args: " + err.Error())
		}
		if len(req.Repos) == 0 && !req.IncludeDiscoveredRepos {
			req.Repos = defaultDevEnvCloneReposFromCwd()
		}
		return true, mcpToolJSON(buildDevEnvironmentClonePlan(context.Background(), s, req))
	case "dev_environment_clone_start":
		var req DevEnvironmentCloneRequest
		if err := json.Unmarshal(arguments, &req); err != nil {
			return true, mcpToolError("bad args: " + err.Error())
		}
		if len(req.Repos) == 0 && !req.IncludeDiscoveredRepos {
			req.Repos = defaultDevEnvCloneReposFromCwd()
		}
		plan := buildDevEnvironmentClonePlan(context.Background(), s, req)
		if !plan.OK {
			return true, mcpToolJSON(plan)
		}
		job := startDevEnvironmentCloneJob(s, req, plan)
		return true, mcpToolJSON(map[string]any{"ok": true, "jobId": job.ID, "status": job.Status, "plan": plan})
	case "dev_environment_clone_status":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return true, mcpToolError("bad args: " + err.Error())
		}
		job, ok := getDevEnvironmentCloneJob(args.ID)
		if !ok {
			return true, mcpToolError("dev environment clone job not found: " + args.ID)
		}
		return true, mcpToolJSON(map[string]any{"ok": true, "job": job})
	default:
		return false, nil
	}
}
