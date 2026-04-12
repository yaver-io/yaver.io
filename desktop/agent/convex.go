package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Default self-hosted Convex OSS admin key (matches upstream default).
const defaultConvexAdminKey = "0135d8598650f8f5cb0f30c34ec2e2bb62793bc28717c8eb6fb577996d50be5f4281b59181095065c5d0f86a2c31ddbe9b597ec62b47ded69782cd"

// ConvexAdminClient talks to a self-hosted Convex backend's HTTP API.
type ConvexAdminClient struct {
	URL       string
	AdminKey  string
	http      *http.Client
}

// NewConvexAdminClient resolves URL + admin key from the project's .env.local,
// falling back to env vars and then to localhost defaults.
func NewConvexAdminClient(projectDir string) *ConvexAdminClient {
	url, key := convexCredsFromDir(projectDir)
	if url == "" {
		if v := os.Getenv("CONVEX_SELF_HOSTED_URL"); v != "" {
			url = v
		} else {
			url = "http://127.0.0.1:3210"
		}
	}
	if key == "" {
		if v := os.Getenv("CONVEX_SELF_HOSTED_ADMIN_KEY"); v != "" {
			key = v
		} else {
			key = defaultConvexAdminKey
		}
	}
	return &ConvexAdminClient{
		URL:      strings.TrimRight(url, "/"),
		AdminKey: key,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func convexCredsFromDir(dir string) (url, key string) {
	if dir == "" {
		return
	}
	for _, name := range []string{".env.local", ".env"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			switch strings.TrimSpace(k) {
			case "CONVEX_SELF_HOSTED_URL":
				url = v
			case "CONVEX_SELF_HOSTED_ADMIN_KEY":
				key = v
			}
		}
		if url != "" || key != "" {
			return
		}
	}
	return
}

func (c *ConvexAdminClient) post(path string, body interface{}, admin bool) ([]byte, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, c.URL+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if admin {
		req.Header.Set("Authorization", "Convex "+c.AdminKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("convex %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// Health pings /version.
func (c *ConvexAdminClient) Health() error {
	req, err := http.NewRequest(http.MethodGet, c.URL+"/version", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("convex health: %d", resp.StatusCode)
	}
	return nil
}

// Query runs a query function via the public API.
func (c *ConvexAdminClient) Query(path string, args map[string]interface{}) ([]byte, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	return c.post("/api/query", map[string]interface{}{
		"path":   path,
		"args":   args,
		"format": "json",
	}, true)
}

// Mutation runs a mutation function.
func (c *ConvexAdminClient) Mutation(path string, args map[string]interface{}) ([]byte, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	return c.post("/api/mutation", map[string]interface{}{
		"path":   path,
		"args":   args,
		"format": "json",
	}, true)
}

// Action runs an action function.
func (c *ConvexAdminClient) Action(path string, args map[string]interface{}) ([]byte, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	return c.post("/api/action", map[string]interface{}{
		"path":   path,
		"args":   args,
		"format": "json",
	}, true)
}

// Export streams all data out via the admin streaming_export endpoint.
func (c *ConvexAdminClient) Export() ([]byte, error) {
	return c.post("/api/streaming_export", map[string]interface{}{}, true)
}

// ---------------------------------------------------------------------------
// MCP tool handlers
// ---------------------------------------------------------------------------

func mcpConvexLocalStatus(dir string) interface{} {
	c := NewConvexAdminClient(dir)
	out := map[string]interface{}{
		"url":     c.URL,
		"running": false,
	}
	if err := c.Health(); err != nil {
		out["error"] = err.Error()
		out["hint"] = "Run `yaver services add convex && yaver services start convex`"
		return out
	}
	out["running"] = true
	return out
}

func mcpConvexTables(dir string) interface{} {
	c := NewConvexAdminClient(dir)
	// Call a generic helper that users can install via yaver_admin.ts.
	// Fall back to streaming_export for table enumeration.
	data, err := c.Query("yaver_admin:listTables", nil)
	if err == nil {
		return map[string]interface{}{"tables": json.RawMessage(data)}
	}
	// Fallback: export → sniff table names
	exp, exErr := c.Export()
	if exErr != nil {
		return map[string]interface{}{
			"error": err.Error(),
			"hint":  "Install convex/yaver_admin.ts helper, or ensure the backend is running. streaming_export also failed: " + exErr.Error(),
		}
	}
	return map[string]interface{}{
		"raw":  string(exp),
		"hint": "yaver_admin:listTables helper not found; showing streaming_export output instead",
	}
}

func mcpConvexBrowse(dir, table, cursor string, limit int) interface{} {
	if limit <= 0 {
		limit = 50
	}
	c := NewConvexAdminClient(dir)
	args := map[string]interface{}{
		"tableName": table,
		"limit":     limit,
	}
	if cursor != "" {
		args["cursor"] = cursor
	}
	data, err := c.Query("yaver_admin:browseTable", args)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"table": table, "result": json.RawMessage(data)}
}

func mcpConvexAdminQuery(dir, path, argsJSON string) interface{} {
	c := NewConvexAdminClient(dir)
	args := parseJSONArgs(argsJSON)
	data, err := c.Query(path, args)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(data)}
	}
	return map[string]interface{}{"result": json.RawMessage(data)}
}

func mcpConvexAdminMutate(dir, path, argsJSON string) interface{} {
	c := NewConvexAdminClient(dir)
	args := parseJSONArgs(argsJSON)
	data, err := c.Mutation(path, args)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(data)}
	}
	return map[string]interface{}{"result": json.RawMessage(data)}
}

func mcpConvexAdminAction(dir, path, argsJSON string) interface{} {
	c := NewConvexAdminClient(dir)
	args := parseJSONArgs(argsJSON)
	data, err := c.Action(path, args)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(data)}
	}
	return map[string]interface{}{"result": json.RawMessage(data)}
}

func mcpConvexSchema(dir string) interface{} {
	path := filepath.Join(dir, "convex", "schema.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "path": path}
	}
	return map[string]interface{}{"path": path, "schema": string(data)}
}

func mcpConvexExport(dir string) interface{} {
	c := NewConvexAdminClient(dir)
	data, err := c.Export()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"bytes": len(data), "data": string(data)}
}

func parseJSONArgs(s string) map[string]interface{} {
	if strings.TrimSpace(s) == "" {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

// yaverAdminHelperSource is the TS file pushed to new Convex projects so
// listTables / browseTable MCP tools have something to call.
const yaverAdminHelperSource = `// Auto-generated by Yaver. Exposes safe admin helpers the Yaver dashboard
// + MCP tools call into. Safe to edit, but do not delete the exported names.
import { query, mutation } from "./_generated/server";
import { v } from "convex/values";

export const listTables = query({
  args: {},
  handler: async (ctx) => {
    const tables = await ctx.db.system.query("_tables" as any).collect();
    return tables.map((t: any) => ({ name: t.name, id: t._id }));
  },
});

export const browseTable = query({
  args: {
    tableName: v.string(),
    cursor: v.optional(v.string()),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, { tableName, cursor, limit }) => {
    return await ctx.db
      .query(tableName as any)
      .paginate({ cursor: cursor ?? null, numItems: limit ?? 50 });
  },
});

export const insertDocument = mutation({
  args: { tableName: v.string(), document: v.any() },
  handler: async (ctx, { tableName, document }) => {
    return await ctx.db.insert(tableName as any, document);
  },
});

export const patchDocument = mutation({
  args: { tableName: v.string(), id: v.string(), fields: v.any() },
  handler: async (ctx, { id, fields }) => {
    await ctx.db.patch(id as any, fields);
  },
});

export const deleteDocument = mutation({
  args: { tableName: v.string(), id: v.string() },
  handler: async (ctx, { id }) => {
    await ctx.db.delete(id as any);
  },
});

export const listScheduledJobs = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.system.query("_scheduled_functions" as any).collect();
  },
});

export const listStoredFiles = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.system.query("_storage" as any).collect();
  },
});
`

// InstallConvexHelper writes convex/yaver_admin.ts into the given project so
// the dashboard + MCP tools have typed helpers to call.
func InstallConvexHelper(dir string) error {
	if dir == "" {
		return fmt.Errorf("project directory required")
	}
	convexDir := filepath.Join(dir, "convex")
	if err := os.MkdirAll(convexDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(convexDir, "yaver_admin.ts"), []byte(yaverAdminHelperSource), 0644)
}

func mcpConvexInstallHelper(dir string) interface{} {
	if err := InstallConvexHelper(dir); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"ok":      true,
		"wrote":   filepath.Join(dir, "convex", "yaver_admin.ts"),
		"next":    "Run `npx convex dev` (or `npx convex deploy`) to push the helper functions.",
	}
}
