package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// phoneProjectMCPTools returns the MCP tool schemas for the phone-first mini
// backend. Appended into the master tool list by mcpBuildToolList.
func phoneProjectMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "phone_project_list",
			"description": "List all phone-first mini-backend projects on this agent. Each project is a SQLite-backed Yaver project stored at ~/.yaver/phone-projects/<slug>/.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "phone_project_templates",
			"description": "List the built-in phone-project templates (blank, crud, todos, notes). Use one of these when calling phone_project_create.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "phone_project_create",
			"description": "Create a new phone-first mini-backend project. Writes schema.yaml, auth.yaml, seed.json, and local.db under ~/.yaver/phone-projects/<slug>/. Returns the created project summary.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]interface{}{
					"name":     map[string]interface{}{"type": "string", "description": "Human-readable project name"},
					"slug":     map[string]interface{}{"type": "string", "description": "Optional directory slug (derived from name if omitted)"},
					"template": map[string]interface{}{"type": "string", "description": "One of: blank, crud, todos, notes"},
				},
			},
		},
		{
			"name":        "phone_project_get",
			"description": "Get the full metadata for one phone project — schema, auth, seed, live table/row stats.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "phone_project_delete",
			"description": "Delete a phone project (removes the SQLite file and manifest).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "phone_project_schema",
			"description": "Apply a declarative schema update to a phone project. Additive only — adds new tables / columns / indexes. Use the PhoneSchema shape: {tables:[{name,columns:[{name,type,primary,required,unique,default}], indexes:[{columns,unique}]}]}. Column types: text|int|bool|real|timestamp|json|uuid. Defaults: uuid|now|<literal>.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "schema"},
				"properties": map[string]interface{}{
					"slug":   map[string]interface{}{"type": "string"},
					"schema": map[string]interface{}{"type": "object"},
				},
			},
		},
		{
			"name":        "phone_project_seed",
			"description": "Write rows to a phone project's SQLite file from a seed map {tableName:[{column:value}]}. Uses INSERT OR REPLACE.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "seed"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string"},
					"seed": map[string]interface{}{"type": "object"},
				},
			},
		},
		{
			"name":        "phone_project_export",
			"description": "Return a tgz of the project's portable manifest (schema + auth + seed + config + generated DDL). Base64-encoded so it round-trips through JSON.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "phone_project_promote",
			"description": "Plan (and optionally run) a switch-engine migration from a phone project to any of the 19 switch targets (sqlite-local, sqlite-turso, postgres-local, postgres-neon, supabase-cloud, convex-cloud, etc.). Same 7-day rollback window as a regular switch.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "target"},
				"properties": map[string]interface{}{
					"slug":    map[string]interface{}{"type": "string"},
					"target":  map[string]interface{}{"type": "string"},
					"run":     map[string]interface{}{"type": "boolean"},
					"dry_run": map[string]interface{}{"type": "boolean"},
				},
			},
		},
	}
}

// dispatchPhoneMCP handles phone_project_* MCP tool calls. Returns (handled,
// result). If handled=false, the outer dispatcher should continue.
func dispatchPhoneMCP(name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "phone_project_list":
		projs, err := ListPhoneProjects()
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		if len(projs) == 0 {
			return true, mcpToolResult("No phone projects. Use phone_project_create to start one.")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%d phone project(s):\n\n", len(projs)))
		for _, p := range projs {
			sb.WriteString(fmt.Sprintf("- %s [%s]", p.Name, p.Slug))
			if p.Template != "" {
				sb.WriteString(" · " + p.Template)
			}
			if p.Stats != nil {
				sb.WriteString(fmt.Sprintf(" · %d tables, %d rows", p.Stats.TableCount, p.Stats.RowCount))
			}
			sb.WriteString("\n")
		}
		return true, mcpToolResult(sb.String())

	case "phone_project_templates":
		return true, mcpToolJSON(ListPhoneTemplates())

	case "phone_project_create":
		var args PhoneCreateSpec
		_ = json.Unmarshal(arguments, &args)
		if args.Name == "" {
			return true, mcpToolError("name is required")
		}
		p, err := CreatePhoneProject(args)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolResult(fmt.Sprintf(
			"Created phone project %q (slug: %s, template: %s).\nDir: %s\nNext: phone_project_schema to customise or phone_project_promote to migrate to a real backend.",
			p.Name, p.Slug, p.Template, p.Dir,
		))

	case "phone_project_get":
		var args struct {
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" {
			return true, mcpToolError("slug is required")
		}
		p, err := LoadPhoneProject(args.Slug)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(p)

	case "phone_project_delete":
		var args struct {
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" {
			return true, mcpToolError("slug is required")
		}
		if err := DeletePhoneProject(args.Slug); err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolResult(fmt.Sprintf("Deleted phone project %s.", args.Slug))

	case "phone_project_schema":
		var args struct {
			Slug   string       `json:"slug"`
			Schema *PhoneSchema `json:"schema"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" || args.Schema == nil {
			return true, mcpToolError("slug and schema are required")
		}
		if err := ApplyPhoneSchema(args.Slug, args.Schema); err != nil {
			return true, mcpToolError(err.Error())
		}
		p, _ := LoadPhoneProject(args.Slug)
		tableCount := len(args.Schema.Tables)
		if p != nil && p.Stats != nil {
			tableCount = p.Stats.TableCount
		}
		return true, mcpToolResult(fmt.Sprintf("Schema applied to %s — %d table(s) now in place.", args.Slug, tableCount))

	case "phone_project_seed":
		var args struct {
			Slug string    `json:"slug"`
			Seed PhoneSeed `json:"seed"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" || args.Seed == nil {
			return true, mcpToolError("slug and seed are required")
		}
		if err := ApplyPhoneSeed(args.Slug, args.Seed); err != nil {
			return true, mcpToolError(err.Error())
		}
		count := 0
		for _, rows := range args.Seed {
			count += len(rows)
		}
		return true, mcpToolResult(fmt.Sprintf("Seeded %d row(s) into %s.", count, args.Slug))

	case "phone_project_export":
		var args struct {
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" {
			return true, mcpToolError("slug is required")
		}
		data, err := ExportPhoneProject(args.Slug)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(map[string]interface{}{
			"slug":        args.Slug,
			"bytes":       len(data),
			"tarball_b64": base64.StdEncoding.EncodeToString(data),
		})

	case "phone_project_promote":
		var args struct {
			Slug   string `json:"slug"`
			Target string `json:"target"`
			Run    bool   `json:"run"`
			DryRun bool   `json:"dry_run"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" || args.Target == "" {
			return true, mcpToolError("slug and target are required")
		}
		dir, err := PhoneProjectDir(args.Slug)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		engine := NewSwitchEngine()
		state, err := engine.Plan(dir, args.Target, args.DryRun)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		if err := engine.Persist(state); err != nil {
			return true, mcpToolError(err.Error())
		}
		if args.Run {
			if err := engine.Run(state); err != nil {
				return true, mcpToolJSON(map[string]interface{}{"state": state, "error": err.Error()})
			}
		}
		return true, mcpToolJSON(map[string]interface{}{"state": state})
	}
	return false, nil
}
