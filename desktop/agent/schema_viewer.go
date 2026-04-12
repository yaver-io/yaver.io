package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SchemaTable is a universal description of a table/collection schema.
type SchemaTable struct {
	Name    string         `json:"name"`
	Columns []SchemaColumn `json:"columns"`
}

type SchemaColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"notNull,omitempty"`
	PrimaryKey bool   `json:"primaryKey,omitempty"`
	ForeignKey string `json:"foreignKey,omitempty"` // "tableName.col"
}

// BuildSchema returns table+column info across backends (best-effort).
func BuildSchema(projectDir string) (*struct {
	Backend string         `json:"backend"`
	Tables  []SchemaTable  `json:"tables"`
	Mermaid string         `json:"mermaid"`
	Source  string         `json:"source"`
}, error) {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}
	out := struct {
		Backend string         `json:"backend"`
		Tables  []SchemaTable  `json:"tables"`
		Mermaid string         `json:"mermaid"`
		Source  string         `json:"source"`
	}{Backend: string(cfg.Backend)}

	switch cfg.Backend {
	case BackendPostgres, BackendSupabase, BackendSQLite:
		tables, err := sqlSchema(projectDir, cfg, cfg.Backend)
		if err != nil {
			return nil, err
		}
		out.Tables = tables
		out.Source = "information_schema"
	case BackendConvex:
		tables, src, err := parseConvexSchema(projectDir)
		if err != nil {
			return nil, err
		}
		out.Tables = tables
		out.Source = src
	case BackendPocketBase, BackendAppwrite:
		adapter, err := NewBackendAdapter(projectDir)
		if err != nil {
			return nil, err
		}
		list, err := adapter.ListTables()
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			out.Tables = append(out.Tables, SchemaTable{Name: t.Name})
		}
		out.Source = "adapter.ListTables"
	}
	out.Mermaid = renderMermaidERD(out.Tables)
	return &out, nil
}

func sqlSchema(projectDir string, cfg *YaverProjectConfig, kind BackendKind) ([]SchemaTable, error) {
	adapter, err := newSQLAdapter(projectDir, cfg, kind)
	if err != nil {
		return nil, err
	}
	if err := adapter.open(); err != nil {
		return nil, err
	}
	tables, err := adapter.ListTables()
	if err != nil {
		return nil, err
	}
	var out []SchemaTable
	for _, t := range tables {
		st := SchemaTable{Name: t.Name}
		rows, err := adapter.db.Query(columnQuery(adapter.driver, t.Name))
		if err != nil {
			out = append(out, st)
			continue
		}
		for rows.Next() {
			var c SchemaColumn
			var notNull, pk int
			if adapter.driver == "postgres" {
				var nullable string
				_ = rows.Scan(&c.Name, &c.Type, &nullable)
				c.NotNull = nullable == "NO"
			} else {
				_ = rows.Scan(&c.Name, &c.Type, &notNull, &pk)
				c.NotNull = notNull != 0
				c.PrimaryKey = pk != 0
			}
			st.Columns = append(st.Columns, c)
		}
		rows.Close()
		out = append(out, st)
	}
	return out, nil
}

func columnQuery(driver, table string) string {
	if driver == "postgres" {
		return fmt.Sprintf(
			`SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = '%s' ORDER BY ordinal_position`,
			strings.ReplaceAll(table, "'", ""),
		)
	}
	return fmt.Sprintf(`PRAGMA table_info(%q)`, table)
}

func parseConvexSchema(projectDir string) ([]SchemaTable, string, error) {
	path := filepath.Join(projectDir, "convex", "schema.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	// Very light-touch parse: find `defineTable({ ... })` blocks.
	var out []SchemaTable
	src := string(data)
	for {
		idx := strings.Index(src, "defineTable({")
		if idx < 0 {
			break
		}
		// Table name is typically the property before defineTable: `users: defineTable({`
		nameEnd := strings.LastIndex(src[:idx], ":")
		nameStart := strings.LastIndexAny(src[:nameEnd], "\n \t,{")
		name := strings.TrimSpace(src[nameStart+1 : nameEnd])
		body := src[idx+len("defineTable({"):]
		end := strings.Index(body, "})")
		if end < 0 {
			break
		}
		cols := parseConvexFields(body[:end])
		out = append(out, SchemaTable{Name: name, Columns: cols})
		src = body[end+2:]
	}
	return out, path, nil
}

func parseConvexFields(body string) []SchemaColumn {
	var cols []SchemaColumn
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		typ := strings.TrimSpace(line[colon+1:])
		cols = append(cols, SchemaColumn{Name: name, Type: typ})
	}
	return cols
}

// renderMermaidERD renders a Mermaid `erDiagram` string the UI can render.
func renderMermaidERD(tables []SchemaTable) string {
	var sb strings.Builder
	sb.WriteString("erDiagram\n")
	for _, t := range tables {
		sb.WriteString(fmt.Sprintf("    %s {\n", safeID(t.Name)))
		for _, c := range t.Columns {
			attrs := ""
			if c.PrimaryKey {
				attrs = " PK"
			}
			typ := strings.ReplaceAll(c.Type, " ", "_")
			if typ == "" {
				typ = "any"
			}
			sb.WriteString(fmt.Sprintf("        %s %s%s\n", typ, safeID(c.Name), attrs))
		}
		sb.WriteString("    }\n")
	}
	// Relationships from ForeignKey strings.
	for _, t := range tables {
		for _, c := range t.Columns {
			if c.ForeignKey == "" {
				continue
			}
			parts := strings.SplitN(c.ForeignKey, ".", 2)
			if len(parts) != 2 {
				continue
			}
			sb.WriteString(fmt.Sprintf("    %s ||--o{ %s : %s\n", safeID(parts[0]), safeID(t.Name), c.Name))
		}
	}
	return sb.String()
}

func safeID(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
			out = append(out, ch)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 || (out[0] >= '0' && out[0] <= '9') {
		out = append([]byte{'t'}, out...)
	}
	return string(out)
}

// ---- MCP / HTTP ----

func mcpSchemaView(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	res, err := BuildSchema(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}

func (s *HTTPServer) handleSchemaView(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpSchemaView(s.dirParam(r)))
}
