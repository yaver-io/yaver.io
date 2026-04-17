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
			"description": "List all phone-first mini-backend projects on this agent. Each project is its own local SQLite-backed Yaver project under ~/.yaver/phone-projects/<slug>/. Multiple projects can coexist; nothing is promoted off-machine until you explicitly export, push, or promote it.",
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
			"description": "Return a tgz of the project's portable manifest (schema + auth + seed + config + generated DDL). Base64-encoded so it round-trips through JSON. Supports include_data and containerize so MCP clients can export the exact same local-first backend bundle the mobile app and CLI use.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug":         map[string]interface{}{"type": "string"},
					"include_data": map[string]interface{}{"type": "boolean", "description": "Include local.db so runtime rows survive import/push"},
					"containerize": map[string]interface{}{"type": "boolean", "description": "Include Dockerfile/docker-compose/.env scaffold for own-cloud or Yaver Cloud deploy paths"},
				},
			},
		},
		{
			"name":        "phone_project_import",
			"description": "Import a previously exported phone project tgz (base64-encoded). This restores a local SQLite-backed phone sandbox on this agent without promoting it anywhere else.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"tarball_b64"},
				"properties": map[string]interface{}{
					"tarball_b64": map[string]interface{}{"type": "string", "description": "Base64-encoded .tgz produced by phone_project_export"},
					"slug":        map[string]interface{}{"type": "string", "description": "Optional slug override on import"},
					"on_conflict": map[string]interface{}{"type": "string", "description": "reject | rename | overwrite", "enum": []string{"reject", "rename", "overwrite"}},
					"skip_seed":   map[string]interface{}{"type": "boolean", "description": "Restore schema/auth but skip seed/live data application"},
				},
			},
		},
		{
			"name":        "phone_project_push",
			"description": "Export a local phone project and push it to another reachable Yaver agent via /phone/projects/receive. Use this for explicit promotion to your dev machine, your own cloud host, or Yaver Cloud. Local source project remains local unless you choose otherwise.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "target_base_url"},
				"properties": map[string]interface{}{
					"slug":              map[string]interface{}{"type": "string"},
					"target_base_url":   map[string]interface{}{"type": "string", "description": "Remote Yaver agent base URL, e.g. https://cloud.yaver.io or https://relay.yaver.io/d/<device>"},
					"target_auth_token": map[string]interface{}{"type": "string", "description": "Optional bearer token for the target. Defaults to local config auth token, then the current agent token if available."},
					"target_slug":       map[string]interface{}{"type": "string", "description": "Optional slug override on the target"},
					"on_conflict":       map[string]interface{}{"type": "string", "description": "reject | rename | overwrite", "enum": []string{"reject", "rename", "overwrite"}},
					"skip_seed":         map[string]interface{}{"type": "boolean"},
					"include_data":      map[string]interface{}{"type": "boolean", "description": "Include local.db so runtime rows survive promotion"},
					"containerize":      map[string]interface{}{"type": "boolean", "description": "Include Docker scaffold in the exported bundle before pushing"},
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
func dispatchPhoneMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
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
			Slug         string `json:"slug"`
			IncludeData  bool   `json:"include_data"`
			Containerize bool   `json:"containerize"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" {
			return true, mcpToolError("slug is required")
		}
		data, err := ExportPhoneProjectWithOptions(args.Slug, PhoneExportOptions{
			IncludeData:  args.IncludeData,
			Containerize: args.Containerize,
		})
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(map[string]interface{}{
			"slug":          args.Slug,
			"bytes":         len(data),
			"include_data":  args.IncludeData,
			"containerize":  args.Containerize,
			"tarball_b64":   base64.StdEncoding.EncodeToString(data),
			"bundle_format": "tgz",
		})

	case "phone_project_import":
		var args struct {
			TarballB64 string `json:"tarball_b64"`
			Slug       string `json:"slug"`
			OnConflict string `json:"on_conflict"`
			SkipSeed   bool   `json:"skip_seed"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.TarballB64 == "" {
			return true, mcpToolError("tarball_b64 is required")
		}
		data, err := base64.StdEncoding.DecodeString(args.TarballB64)
		if err != nil {
			return true, mcpToolError("invalid tarball_b64: " + err.Error())
		}
		if args.OnConflict == "" {
			args.OnConflict = "reject"
		}
		p, err := ImportPhoneProject(data, PhoneImportOptions{
			SlugOverride: args.Slug,
			OnConflict:   args.OnConflict,
			SkipSeed:     args.SkipSeed,
		})
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(map[string]interface{}{
			"slug":       p.Slug,
			"name":       p.Name,
			"dir":        p.Dir,
			"skip_seed":  args.SkipSeed,
			"conflict":   args.OnConflict,
			"stats":      p.Stats,
			"project":    p,
			"local_only": true,
		})

	case "phone_project_push":
		var args struct {
			Slug            string `json:"slug"`
			TargetBaseURL   string `json:"target_base_url"`
			TargetAuthToken string `json:"target_auth_token"`
			TargetSlug      string `json:"target_slug"`
			OnConflict      string `json:"on_conflict"`
			SkipSeed        bool   `json:"skip_seed"`
			IncludeData     bool   `json:"include_data"`
			Containerize    bool   `json:"containerize"`
		}
		_ = json.Unmarshal(arguments, &args)
		if args.Slug == "" || args.TargetBaseURL == "" {
			return true, mcpToolError("slug and target_base_url are required")
		}
		if args.OnConflict == "" {
			args.OnConflict = "reject"
		}
		data, err := ExportPhoneProjectWithOptions(args.Slug, PhoneExportOptions{
			IncludeData:  args.IncludeData,
			Containerize: args.Containerize,
		})
		if err != nil {
			return true, mcpToolError("export: " + err.Error())
		}
		token := strings.TrimSpace(args.TargetAuthToken)
		if token == "" {
			if cfg, err := LoadConfig(); err == nil && strings.TrimSpace(cfg.AuthToken) != "" {
				token = strings.TrimSpace(cfg.AuthToken)
			}
		}
		if token == "" && s != nil {
			token = strings.TrimSpace(s.token)
		}
		result, err := pushPhoneBundle(strings.TrimRight(args.TargetBaseURL, "/"), token, data, args.TargetSlug, args.OnConflict, args.SkipSeed)
		if err != nil {
			return true, mcpToolError("push: " + err.Error())
		}
		return true, mcpToolJSON(map[string]interface{}{
			"source_slug":     args.Slug,
			"target_slug":     result.Slug,
			"target_base_url": strings.TrimRight(args.TargetBaseURL, "/"),
			"browse_url":      result.BrowseUrl,
			"local_url":       result.LocalUrl,
			"include_data":    args.IncludeData,
			"containerize":    args.Containerize,
			"skip_seed":       args.SkipSeed,
			"on_conflict":     args.OnConflict,
			"pushed":          true,
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
