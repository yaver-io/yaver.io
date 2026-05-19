package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Phone-first mini-backend: a constrained, portable backend hosted inside the
// Yaver mobile app's agent. Each project is a regular Yaver project stored at
// ~/.yaver/phone-projects/<slug>/ with a SQLite backend, so the existing
// /backend/*, /switch/*, and /manifest/* routes work unchanged.
//
// The portability contract (MOBILE_WORKER.md §331-356): every project is
// representable as schema + tables + queries + mutations + auth rules +
// storage rules + seed + env. The switch engine handles promotion to any
// of the 19 SwitchTarget destinations (Convex/Supabase/Postgres/etc.).

// PhoneColumn describes one column in a portable-subset schema.
type PhoneColumn struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"` // text|int|bool|real|timestamp|json
	Primary  bool   `yaml:"primary,omitempty" json:"primary,omitempty"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Unique   bool   `yaml:"unique,omitempty" json:"unique,omitempty"`
	Default  string `yaml:"default,omitempty" json:"default,omitempty"` // uuid|now|<literal>
}

// PhoneIndex is a simple multi-column index.
type PhoneIndex struct {
	Columns []string `yaml:"columns" json:"columns"`
	Unique  bool     `yaml:"unique,omitempty" json:"unique,omitempty"`
}

// PhoneTable is the portable table/collection shape.
type PhoneTable struct {
	Name    string        `yaml:"name" json:"name"`
	Columns []PhoneColumn `yaml:"columns" json:"columns"`
	Indexes []PhoneIndex  `yaml:"indexes,omitempty" json:"indexes,omitempty"`
}

// PhoneRelation ties two tables (column-level FK).
type PhoneRelation struct {
	From     string `yaml:"from" json:"from"` // "todos.owner_id"
	To       string `yaml:"to" json:"to"`     // "users.id"
	OnDelete string `yaml:"onDelete,omitempty" json:"onDelete,omitempty"`
}

// PhoneSchema is the declarative whole-schema doc.
type PhoneSchema struct {
	Tables    []PhoneTable    `yaml:"tables" json:"tables"`
	Relations []PhoneRelation `yaml:"relations,omitempty" json:"relations,omitempty"`
}

// PhonePersona is a mock-auth user for the mini-backend.
type PhonePersona struct {
	ID    string `yaml:"id" json:"id"`
	Email string `yaml:"email" json:"email"`
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`
	Role  string `yaml:"role,omitempty" json:"role,omitempty"`
}

// PhoneAuth holds the personas list.
type PhoneAuth struct {
	Personas []PhonePersona `yaml:"personas" json:"personas"`
}

// PhoneSeed is rows keyed by table name.
type PhoneSeed map[string][]map[string]interface{}

// PhoneScreenAction is a minimal portable UI intent for the scaffolded app.
type PhoneScreenAction struct {
	Label       string `yaml:"label" json:"label"`
	Kind        string `yaml:"kind" json:"kind"` // list|create|update|delete|navigate|filter|auth
	Target      string `yaml:"target,omitempty" json:"target,omitempty"`
	Table       string `yaml:"table,omitempty" json:"table,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// PhoneScreenSpec describes one mobile screen in the generated app plan.
type PhoneScreenSpec struct {
	ID         string              `yaml:"id" json:"id"`
	Title      string              `yaml:"title" json:"title"`
	Kind       string              `yaml:"kind" json:"kind"` // list|detail|form|auth|settings
	Table      string              `yaml:"table,omitempty" json:"table,omitempty"`
	EmptyState string              `yaml:"emptyState,omitempty" json:"emptyState,omitempty"`
	Actions    []PhoneScreenAction `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// PhoneAppSpec is a portable app-layer outline that can move with the backend.
type PhoneAppSpec struct {
	Summary       string            `yaml:"summary,omitempty" json:"summary,omitempty"`
	PrimaryEntity string            `yaml:"primaryEntity,omitempty" json:"primaryEntity,omitempty"`
	Screens       []PhoneScreenSpec `yaml:"screens,omitempty" json:"screens,omitempty"`
}

// PhoneProject is the full metadata of a mini-backend project.
type PhoneProject struct {
	Slug      string        `yaml:"slug" json:"slug"`
	Name      string        `yaml:"name" json:"name"`
	Template  string        `yaml:"template,omitempty" json:"template,omitempty"`
	Dir       string        `yaml:"dir" json:"dir"`
	CreatedAt string        `yaml:"createdAt" json:"createdAt"`
	UpdatedAt string        `yaml:"updatedAt" json:"updatedAt"`
	Schema    *PhoneSchema  `yaml:"-" json:"schema,omitempty"`
	Auth      *PhoneAuth    `yaml:"-" json:"auth,omitempty"`
	Seed      PhoneSeed     `yaml:"-" json:"seed,omitempty"`
	App       *PhoneAppSpec `yaml:"-" json:"app,omitempty"`
	Stats     *PhoneStats   `yaml:"-" json:"stats,omitempty"`
}

// PhoneStats are live counts from the SQLite file.
type PhoneStats struct {
	TableCount int              `json:"tableCount"`
	RowCount   int64            `json:"rowCount"`
	PerTable   map[string]int64 `json:"perTable"`
	DBBytes    int64            `json:"dbBytes"`
}

// PhoneCreateSpec is the payload for creating a new project.
type PhoneCreateSpec struct {
	Slug          string        `json:"slug"`
	Name          string        `json:"name"`
	Template      string        `json:"template,omitempty"`
	Schema        *PhoneSchema  `json:"schema,omitempty"`
	Auth          *PhoneAuth    `json:"auth,omitempty"`
	Seed          PhoneSeed     `json:"seed,omitempty"`
	App           *PhoneAppSpec `json:"app,omitempty"`
	Prompt        string        `json:"prompt,omitempty"`
	Runner        string        `json:"runner,omitempty"`
	ImportURL     string        `json:"importUrl,omitempty"`
	ImportContent string        `json:"importContent,omitempty"`
	ImportTitle   string        `json:"importTitle,omitempty"`
}

var runPhonePromptGenerator = RunAIGenerator

var slugRE = regexp.MustCompile(`[^a-z0-9-]+`)

// Slugify produces a safe directory name. Returns "" for empty / all-invalid
// input so callers can decide whether to fall through to a different source.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// PhoneProjectsRoot returns ~/.yaver/phone-projects/.
func PhoneProjectsRoot() (string, error) {
	cfg, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "phone-projects")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// PhoneProjectDir returns the absolute path for a project's slug.
func PhoneProjectDir(slug string) (string, error) {
	root, err := PhoneProjectsRoot()
	if err != nil {
		return "", err
	}
	slug = Slugify(slug)
	if slug == "" {
		return "", fmt.Errorf("empty slug")
	}
	return filepath.Join(root, slug), nil
}

// CreatePhoneProject materialises a new mini-backend on disk. Safe to re-run:
// if the slug exists it returns ErrPhoneProjectExists.
var ErrPhoneProjectExists = fmt.Errorf("phone project already exists")

func CreatePhoneProject(spec PhoneCreateSpec) (*PhoneProject, error) {
	if strings.TrimSpace(spec.Prompt) == "" && (strings.TrimSpace(spec.ImportURL) != "" || strings.TrimSpace(spec.ImportContent) != "") {
		plan, err := AnalyzeConversationImport(ConversationImportRequest{
			URL:     spec.ImportURL,
			Content: spec.ImportContent,
			Title:   spec.ImportTitle,
			Runner:  spec.Runner,
			WorkDir: ".",
		})
		if err != nil {
			return nil, fmt.Errorf("analyze imported conversation: %w", err)
		}
		spec.Prompt = strings.TrimSpace(plan.GeneratedPrompt)
		if strings.TrimSpace(spec.Name) == "" {
			spec.Name = strings.TrimSpace(plan.SuggestedName)
		}
		if strings.TrimSpace(spec.Template) == "" {
			spec.Template = "prompt"
		}
	}
	if strings.TrimSpace(spec.Prompt) != "" && spec.Schema == nil && spec.Auth == nil && spec.Seed == nil {
		gen, err := generatePhoneProjectFromPrompt(spec)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(spec.Name) == "" {
			spec.Name = gen.Name
		}
		if strings.TrimSpace(spec.Template) == "" {
			spec.Template = "prompt"
		}
		spec.Schema = gen.Schema
		spec.Auth = gen.Auth
		spec.Seed = gen.Seed
		spec.App = gen.App
	}
	slug := Slugify(spec.Slug)
	if slug == "" {
		slug = Slugify(spec.Name)
	}
	if slug == "" {
		return nil, fmt.Errorf("slug or name required")
	}
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err == nil {
		return nil, ErrPhoneProjectExists
	}
	if err := os.MkdirAll(filepath.Join(dir, ".yaver"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0o700); err != nil {
		return nil, err
	}

	// Persist YaverProjectConfig so the existing BackendAdapter stack picks it up.
	cfg := &YaverProjectConfig{
		Backend: BackendSQLite,
		DB:      filepath.Join(dir, "local.db"),
		Auth:    "phone-personas",
		Stack:   "expo",
	}
	if err := SaveProjectConfig(dir, cfg); err != nil {
		return nil, err
	}

	name := spec.Name
	if name == "" {
		name = slug
	}
	proj := &PhoneProject{
		Slug:      slug,
		Name:      name,
		Template:  spec.Template,
		Dir:       dir,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := savePhoneMeta(proj); err != nil {
		return nil, err
	}

	// Apply template if schema/auth/seed not already supplied.
	if spec.Schema == nil {
		spec.Schema = templateSchema(spec.Template)
	}
	if spec.Auth == nil {
		spec.Auth = templateAuth(spec.Template)
	}
	if spec.Seed == nil {
		spec.Seed = templateSeed(spec.Template)
	}
	if spec.App == nil {
		spec.App = templateApp(spec.Template)
	}

	if spec.Schema != nil {
		if err := ApplyPhoneSchema(slug, spec.Schema); err != nil {
			return nil, fmt.Errorf("apply schema: %w", err)
		}
	}
	if spec.Auth != nil {
		if err := ApplyPhoneAuth(slug, spec.Auth); err != nil {
			return nil, fmt.Errorf("apply auth: %w", err)
		}
	}
	if spec.Seed != nil {
		if err := ApplyPhoneSeed(slug, spec.Seed); err != nil {
			return nil, fmt.Errorf("apply seed: %w", err)
		}
	}
	if spec.App != nil {
		if err := savePhoneApp(dir, spec.App); err != nil {
			return nil, fmt.Errorf("apply app: %w", err)
		}
	}

	// Also drop a minimal ProjectManifest so /manifest/* works.
	_ = SaveManifest(dir, &ProjectManifest{
		Name:    name,
		Backend: BackendSQLite,
		Stack:   "expo",
		Auth:    "phone-personas",
		Env:     map[string]string{"YAVER_PHONE_PROJECT": slug},
	})

	return LoadPhoneProject(slug)
}

type generatedPhoneProjectSpec struct {
	Name   string        `json:"name"`
	Schema *PhoneSchema  `json:"schema"`
	Auth   *PhoneAuth    `json:"auth"`
	Seed   PhoneSeed     `json:"seed"`
	App    *PhoneAppSpec `json:"app"`
}

func generatePhoneProjectFromPrompt(spec PhoneCreateSpec) (*generatedPhoneProjectSpec, error) {
	wd, _ := os.Getwd()
	body, err := runPhonePromptGenerator(AIGeneratorSpec{
		Runner:  spec.Runner,
		WorkDir: wd,
		Prompt: fmt.Sprintf(`You are generating a phone-first Yaver mini-backend project.

User project name: %s
User prompt: %s

Return ONLY one JSON object, no markdown fences, no commentary.

Shape:
{
  "name": "short project name",
  "schema": {
    "tables": [
      {
        "name": "table_name",
        "columns": [
          {"name":"id","type":"text","primary":true,"default":"uuid"},
          {"name":"title","type":"text","required":true}
        ],
        "indexes": [{"columns":["owner_id"]}]
      }
    ],
    "relations": [
      {"from":"todos.owner_id","to":"users.id","onDelete":"cascade"}
    ]
  },
  "auth": {
    "personas": [
      {"id":"owner","email":"owner@example.com","name":"Owner","role":"owner"}
    ]
  },
  "seed": {
    "todos": [
      {"id":"welcome","title":"Welcome","done":false,"owner_id":"owner"}
    ]
  },
  "app": {
    "summary": "short description of the mobile UX",
    "primaryEntity": "todos",
    "screens": [
      {
        "id": "home",
        "title": "Inbox",
        "kind": "list",
        "table": "todos",
        "emptyState": "No tasks yet.",
        "actions": [
          {"label":"Add task","kind":"create","table":"todos"},
          {"label":"View task","kind":"navigate","target":"todo_detail"}
        ]
      }
    ]
  }
}

Rules:
- Use only supported column types: text, int, bool, real, timestamp, json, uuid.
- Prefer 1-3 tables, phone-first, SQLite-friendly.
- If auth is useful, include a users table and personas. If not useful, return an empty personas array.
- Seed should include a few realistic starter rows when helpful.
- Include a compact app plan with 1-4 screens that a phone user could ship first.
- Keep names ASCII and snake_case.
- Do not include fields outside this shape.
`, strings.TrimSpace(spec.Name), strings.TrimSpace(spec.Prompt)),
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("generate phone project from prompt: %w", err)
	}
	payload := extractJSONObject(body)
	var out generatedPhoneProjectSpec
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return nil, fmt.Errorf("parse generated phone project JSON: %w", err)
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = strings.TrimSpace(spec.Name)
	}
	if out.Schema == nil {
		return nil, fmt.Errorf("generated phone project missing schema")
	}
	return &out, nil
}

func extractJSONObject(s string) string {
	body := strings.TrimSpace(s)
	body = strings.TrimPrefix(body, "```json")
	body = strings.TrimPrefix(body, "```")
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)
	start := strings.IndexByte(body, '{')
	end := strings.LastIndexByte(body, '}')
	if start >= 0 && end > start {
		return body[start : end+1]
	}
	return body
}

func phoneMetaPath(dir string) string {
	return filepath.Join(dir, ".yaver", "phone.yaml")
}

func savePhoneMeta(p *PhoneProject) error {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(phoneMetaPath(p.Dir), data, 0o600)
}

func loadPhoneMeta(dir string) (*PhoneProject, error) {
	data, err := os.ReadFile(phoneMetaPath(dir))
	if err != nil {
		return nil, err
	}
	var p PhoneProject
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	p.Dir = dir
	return &p, nil
}

// LoadPhoneProject reads everything for one slug.
func LoadPhoneProject(slug string) (*PhoneProject, error) {
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return nil, err
	}
	meta, err := loadPhoneMeta(dir)
	if err != nil {
		return nil, fmt.Errorf("load meta: %w", err)
	}
	meta.Schema, _ = loadPhoneSchema(dir)
	meta.Auth, _ = loadPhoneAuth(dir)
	meta.Seed, _ = loadPhoneSeed(dir)
	meta.App, _ = loadPhoneApp(dir)
	meta.Stats, _ = computePhoneStats(dir)
	return meta, nil
}

// ListPhoneProjects returns a summary of all mini-backend projects.
func ListPhoneProjects() ([]*PhoneProject, error) {
	root, err := PhoneProjectsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []*PhoneProject
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		p, err := loadPhoneMeta(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		p.Stats, _ = computePhoneStats(p.Dir)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// DeletePhoneProject rm -rf's the project directory. Returns nil if missing.
func DeletePhoneProject(slug string) error {
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}

// ---- Schema + auth + seed persistence ----

func schemaPath(dir string) string { return filepath.Join(dir, "schema.yaml") }
func authPath(dir string) string   { return filepath.Join(dir, "auth.yaml") }
func seedPath(dir string) string   { return filepath.Join(dir, "seed.json") }
func appPath(dir string) string    { return filepath.Join(dir, "app.yaml") }
func dbFilePath(dir string) string { return filepath.Join(dir, "local.db") }

func loadPhoneSchema(dir string) (*PhoneSchema, error) {
	data, err := os.ReadFile(schemaPath(dir))
	if err != nil {
		return nil, err
	}
	var s PhoneSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func savePhoneSchema(dir string, s *PhoneSchema) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(schemaPath(dir), data, 0o644)
}

func loadPhoneAuth(dir string) (*PhoneAuth, error) {
	data, err := os.ReadFile(authPath(dir))
	if err != nil {
		return nil, err
	}
	var a PhoneAuth
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func savePhoneAuth(dir string, a *PhoneAuth) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	return os.WriteFile(authPath(dir), data, 0o644)
}

func loadPhoneSeed(dir string) (PhoneSeed, error) {
	data, err := os.ReadFile(seedPath(dir))
	if err != nil {
		return nil, err
	}
	var s PhoneSeed
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return s, nil
}

func savePhoneSeed(dir string, s PhoneSeed) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(seedPath(dir), data, 0o644)
}

func loadPhoneApp(dir string) (*PhoneAppSpec, error) {
	data, err := os.ReadFile(appPath(dir))
	if err != nil {
		return nil, err
	}
	var a PhoneAppSpec
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func savePhoneApp(dir string, a *PhoneAppSpec) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	return os.WriteFile(appPath(dir), data, 0o644)
}

// ---- Schema → SQLite DDL ----

var allowedColumnTypes = map[string]string{
	"text":      "TEXT",
	"string":    "TEXT",
	"int":       "INTEGER",
	"integer":   "INTEGER",
	"bool":      "INTEGER", // SQLite has no native bool
	"boolean":   "INTEGER",
	"real":      "REAL",
	"float":     "REAL",
	"timestamp": "TEXT", // RFC3339 strings — portable to Postgres/Convex
	"json":      "TEXT",
	"uuid":      "TEXT",
}

func sqliteColumnDDL(c PhoneColumn) (string, error) {
	sqlType, ok := allowedColumnTypes[strings.ToLower(c.Type)]
	if !ok {
		return "", fmt.Errorf("unsupported column type %q (allowed: text,int,bool,real,timestamp,json,uuid)", c.Type)
	}
	parts := []string{fmt.Sprintf(`"%s"`, c.Name), sqlType}
	if c.Primary {
		parts = append(parts, "PRIMARY KEY")
	}
	if c.Required && !c.Primary {
		parts = append(parts, "NOT NULL")
	}
	if c.Unique && !c.Primary {
		parts = append(parts, "UNIQUE")
	}
	if c.Default != "" {
		switch strings.ToLower(c.Default) {
		case "uuid":
			parts = append(parts, "DEFAULT (lower(hex(randomblob(16))))")
		case "now":
			parts = append(parts, "DEFAULT CURRENT_TIMESTAMP")
		default:
			parts = append(parts, "DEFAULT "+sqliteLiteral(c.Default))
		}
	}
	return strings.Join(parts, " "), nil
}

func sqliteLiteral(v string) string {
	if v == "" {
		return "''"
	}
	// already a numeric literal? pass through.
	if _, err := fmtAtoi(v); err == nil {
		return v
	}
	if v == "true" {
		return "1"
	}
	if v == "false" {
		return "0"
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func fmtAtoi(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// ApplyPhoneSchema creates / alters SQLite tables to match the declared schema.
// Only additive: new tables + new columns on existing tables. Never drops.
func ApplyPhoneSchema(slug string, schema *PhoneSchema) error {
	if schema == nil {
		return fmt.Errorf("nil schema")
	}
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dbFilePath(dir))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}

	existingTables := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		existingTables[n] = true
	}
	_ = rows.Close()

	for _, t := range schema.Tables {
		if t.Name == "" || len(t.Columns) == 0 {
			return fmt.Errorf("table %q missing columns", t.Name)
		}
		if !existingTables[t.Name] {
			var cols []string
			for _, c := range t.Columns {
				ddl, err := sqliteColumnDDL(c)
				if err != nil {
					return fmt.Errorf("table %s: %w", t.Name, err)
				}
				cols = append(cols, ddl)
			}
			stmt := fmt.Sprintf(`CREATE TABLE "%s" (%s)`, t.Name, strings.Join(cols, ", "))
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("create %s: %w", t.Name, err)
			}
		} else {
			// Additive: add new columns (SQLite ALTER TABLE ADD COLUMN).
			colRows, err := db.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, t.Name))
			if err != nil {
				return err
			}
			existingCols := map[string]bool{}
			for colRows.Next() {
				var cid int
				var cname, ctype string
				var notnull, pk int
				var dflt sql.NullString
				if err := colRows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
					_ = colRows.Close()
					return err
				}
				existingCols[cname] = true
			}
			_ = colRows.Close()
			for _, c := range t.Columns {
				if existingCols[c.Name] {
					continue
				}
				ddl, err := sqliteColumnDDL(c)
				if err != nil {
					return fmt.Errorf("table %s col %s: %w", t.Name, c.Name, err)
				}
				stmt := fmt.Sprintf(`ALTER TABLE "%s" ADD COLUMN %s`, t.Name, ddl)
				if _, err := db.Exec(stmt); err != nil {
					return fmt.Errorf("add column %s.%s: %w", t.Name, c.Name, err)
				}
			}
		}
		// Indexes.
		for i, idx := range t.Indexes {
			if len(idx.Columns) == 0 {
				continue
			}
			unique := ""
			if idx.Unique {
				unique = "UNIQUE "
			}
			name := fmt.Sprintf("idx_%s_%d", t.Name, i)
			quoted := make([]string, len(idx.Columns))
			for j, c := range idx.Columns {
				quoted[j] = fmt.Sprintf(`"%s"`, c)
			}
			stmt := fmt.Sprintf(`CREATE %sINDEX IF NOT EXISTS "%s" ON "%s" (%s)`,
				unique, name, t.Name, strings.Join(quoted, ","))
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("index %s: %w", name, err)
			}
		}
	}
	return savePhoneSchema(dir, schema)
}

// ApplyPhoneAuth writes auth.yaml and mirrors personas into a `users` table if
// one is declared in the schema. Mirroring is best-effort — failures don't
// abort the auth update since the declarative file is still the source of
// truth for promotion.
func ApplyPhoneAuth(slug string, auth *PhoneAuth) error {
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return err
	}
	if err := savePhoneAuth(dir, auth); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dbFilePath(dir))
	if err != nil {
		return nil // schema may not be applied yet — fine.
	}
	defer db.Close()
	var has string
	_ = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&has)
	if has == "" {
		return nil
	}
	for _, p := range auth.Personas {
		_, _ = db.Exec(`INSERT OR IGNORE INTO "users" (id,email,name) VALUES (?,?,?)`,
			p.ID, p.Email, p.Name)
	}
	return nil
}

// ApplyPhoneSeed writes seed.json and inserts rows using INSERT OR REPLACE.
// Seed rows must match existing columns; unknown columns are dropped with a
// warning in the returned error chain but other rows still apply.
func ApplyPhoneSeed(slug string, seed PhoneSeed) error {
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return err
	}
	if err := savePhoneSeed(dir, seed); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dbFilePath(dir))
	if err != nil {
		return err
	}
	defer db.Close()
	for table, rows := range seed {
		// Collect column names in existing table so we know what's safe to write.
		colRows, err := db.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, table))
		if err != nil {
			return fmt.Errorf("seed %s: %w", table, err)
		}
		okCols := map[string]bool{}
		for colRows.Next() {
			var cid int
			var cname, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := colRows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
				_ = colRows.Close()
				return err
			}
			okCols[cname] = true
		}
		_ = colRows.Close()
		if len(okCols) == 0 {
			return fmt.Errorf("seed: table %q does not exist — apply schema first", table)
		}
		for _, row := range rows {
			cols := make([]string, 0, len(row))
			placeholders := make([]string, 0, len(row))
			vals := make([]interface{}, 0, len(row))
			for k, v := range row {
				if !okCols[k] {
					continue
				}
				cols = append(cols, fmt.Sprintf(`"%s"`, k))
				placeholders = append(placeholders, "?")
				vals = append(vals, normalizeSeedValue(v))
			}
			if len(cols) == 0 {
				continue
			}
			stmt := fmt.Sprintf(`INSERT OR REPLACE INTO "%s" (%s) VALUES (%s)`,
				table, strings.Join(cols, ","), strings.Join(placeholders, ","))
			if _, err := db.Exec(stmt, vals...); err != nil {
				return fmt.Errorf("seed %s: %w", table, err)
			}
		}
	}
	return nil
}

func normalizeSeedValue(v interface{}) interface{} {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case map[string]interface{}, []interface{}:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return v
	}
}

// ---- Stats / browse helpers ----

func computePhoneStats(dir string) (*PhoneStats, error) {
	dbPath := dbFilePath(dir)
	info, err := os.Stat(dbPath)
	if err != nil {
		return &PhoneStats{PerTable: map[string]int64{}}, nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	stats := &PhoneStats{PerTable: map[string]int64{}, DBBytes: info.Size()}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, n)
	}
	_ = rows.Close()
	stats.TableCount = len(tables)
	for _, t := range tables {
		var n int64
		if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, t)).Scan(&n); err == nil {
			stats.PerTable[t] = n
			stats.RowCount += n
		}
	}
	return stats, nil
}

// ErrPhoneProjectNotFound is returned when a slug doesn't resolve to an on-disk
// phone project. HTTP handlers translate this to 404 so a missing project never
// surfaces as a confusing SQLite "unable to open database file" error.
var ErrPhoneProjectNotFound = fmt.Errorf("phone project not found")

// PhoneAdapter returns a BackendAdapter over the project's local SQLite file,
// so /backend/* endpoints can operate on a phone project via ?directory=<dir>.
// Returns ErrPhoneProjectNotFound if the slug has never been created on this
// agent — previously the missing dir leaked through as a SQLite CANTOPEN error.
func PhoneAdapter(slug string) (BackendAdapter, error) {
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(phoneMetaPath(dir)); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrPhoneProjectNotFound, slug)
		}
		return nil, err
	}
	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		return nil, err
	}
	return newSQLAdapter(dir, cfg, BackendSQLite)
}

// ---- Export ----

type PhoneExportOptions struct {
	// IncludeData bundles the live SQLite file (`local.db`) so runtime rows
	// survive promotion. Default false keeps the portable-manifest behavior.
	IncludeData bool
	// MaxBundleBytes overrides the default PhoneDeployBudgetBytes cap. Zero
	// or negative means "use the default". Keeps runaway uploads from
	// silently pushing 100 MB of SQLite over a paid data plan — see
	// phone_cost.go for the guardrail rationale.
	MaxBundleBytes int64
	// Containerize includes Dockerfile + compose scaffold so the exported
	// Yaver-lite backend can run on remote hardware or Yaver Cloud via Docker.
	Containerize bool
}

// ExportPhoneProject returns the portable-manifest bundle for a project.
func ExportPhoneProject(slug string) ([]byte, error) {
	return ExportPhoneProjectWithOptions(slug, PhoneExportOptions{})
}

// ExportPhoneProjectWithOptions returns a tgz bundle of the project. By
// default only the portable manifest ships; IncludeData also bundles local.db.
// phoneExportFile is one entry in the portable bundle. The tar and zip
// writers share one list so the two archive formats can never drift.
type phoneExportFile struct {
	Name    string
	Content []byte
	Mode    int64
}

// collectPhoneExportFiles is the single source of truth for what an
// exported sandbox contains — consumed by both the .tgz and .zip
// writers so they stay identical.
func collectPhoneExportFiles(p *PhoneProject, opts PhoneExportOptions) []phoneExportFile {
	files := []phoneExportFile{}
	add := func(name string, content []byte, mode int64) {
		files = append(files, phoneExportFile{Name: name, Content: content, Mode: mode})
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, ".yaver", "config.yaml")); err == nil {
		add(".yaver/config.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, ".yaver", "project.yaml")); err == nil {
		add(".yaver/project.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(schemaPath(p.Dir)); err == nil {
		add("schema.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(authPath(p.Dir)); err == nil {
		add("auth.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(seedPath(p.Dir)); err == nil {
		add("seed.json", b, 0o644)
	}
	if b, err := os.ReadFile(appPath(p.Dir)); err == nil {
		add("app.yaml", b, 0o644)
	}
	if opts.IncludeData {
		if b, err := os.ReadFile(dbFilePath(p.Dir)); err == nil {
			add("local.db", b, 0o600)
		}
	}
	// OAuth provider config (client IDs + secrets). 0600 keeps it
	// secret-grade after extraction. See phone_oauth.go.
	if b, err := os.ReadFile(phoneOAuthPath(p.Dir)); err == nil {
		add("oauth-providers.yaml", b, 0o600)
	}
	if ddl, err := generateSchemaSQL(p.Schema, "sqlite"); err == nil && ddl != "" {
		add("schema.sql", []byte(ddl), 0o644)
	}
	if ddl, err := generateSchemaSQL(p.Schema, "postgres"); err == nil && ddl != "" {
		add("schema.postgres.sql", []byte(ddl), 0o644)
	}
	if opts.Containerize {
		add("Dockerfile", []byte(phoneContainerDockerfile()), 0o644)
		add("docker-compose.yml", []byte(phoneContainerCompose()), 0o644)
		add(".env.example", []byte(phoneContainerEnvExample(p)), 0o644)
		add(".dockerignore", []byte(phoneDockerIgnore()), 0o644)
	}
	add(".gitignore", []byte(phoneGitIgnore()), 0o644)
	add("README.md", []byte(phoneReadme(p, opts)), 0o644)
	// AGENTS.md — machine-oriented handoff. Drop this bundle into
	// claude-code / codex / opencode (or onto a Yaver Cloud box) and
	// the agent picks the project up with full context, zero ramp-up.
	add("AGENTS.md", []byte(phoneAgentsDoc(p, opts)), 0o644)
	return files
}

func ExportPhoneProjectWithOptions(slug string, opts PhoneExportOptions) ([]byte, error) {
	p, err := LoadPhoneProject(slug)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range collectPhoneExportFiles(p, opts) {
		hdr := &tar.Header{
			Name:    filepath.Join(p.Slug, f.Name),
			Size:    int64(len(f.Content)),
			Mode:    f.Mode,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	// Producer-side budget guard — consumer (handlePhoneReceive)
	// enforces again so a crafted bundle can't bypass it.
	if err := EnforcePhoneDeployBudget(int64(buf.Len()), opts.MaxBundleBytes); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportPhoneProjectZip is the .zip twin of ExportPhoneProjectWithOptions
// — byte-identical file set, broader compatibility (most coding-agent
// intake flows and OS unzip want .zip, not .tgz). Same budget guard.
func ExportPhoneProjectZip(slug string, opts PhoneExportOptions) ([]byte, error) {
	p, err := LoadPhoneProject(slug)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range collectPhoneExportFiles(p, opts) {
		hdr := &zip.FileHeader{
			Name:     filepath.ToSlash(filepath.Join(p.Slug, f.Name)),
			Method:   zip.Deflate,
			Modified: time.Now(),
		}
		hdr.SetMode(os.FileMode(f.Mode))
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := EnforcePhoneDeployBudget(int64(buf.Len()), opts.MaxBundleBytes); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func phoneReadme(p *PhoneProject, opts PhoneExportOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Name)
	fmt.Fprintf(&b, "Yaver phone-first mini-backend project (slug: `%s`).\n\n", p.Slug)
	fmt.Fprintf(&b, "Created: %s  \nLast updated: %s\n\n", p.CreatedAt, p.UpdatedAt)
	b.WriteString("## Files\n\n")
	b.WriteString("- `.yaver/config.yaml` — backend config (SQLite)\n")
	b.WriteString("- `.yaver/project.yaml` — declarative manifest (see `yaver apply`)\n")
	b.WriteString("- `schema.yaml` — portable schema DSL\n")
	b.WriteString("- `auth.yaml` — persona list\n")
	b.WriteString("- `seed.json` — fixture rows\n")
	b.WriteString("- `app.yaml` — portable phone UI plan\n")
	b.WriteString("- `schema.sql` / `schema.postgres.sql` — generated DDL for non-Yaver imports\n\n")
	if opts.IncludeData {
		b.WriteString("- `local.db` — live SQLite runtime rows for zero-loss promotion\n\n")
	}
	if opts.Containerize {
		b.WriteString("- `Dockerfile` / `docker-compose.yml` — containerized Yaver-lite backend export\n")
		b.WriteString("- `.env.example` / `.dockerignore` — container deploy helpers\n\n")
	}
	b.WriteString("- `.gitignore` — short-path git repo hygiene for exported sandboxes\n\n")
	b.WriteString("## Promoting to a real backend\n\n")
	b.WriteString("```\nyaver switch plan --to supabase-cloud\nyaver switch plan --to convex-cloud\nyaver switch plan --to postgres-neon\n```\n\n")
	b.WriteString("Or open the project in the Yaver desktop app and pick a target from the switch engine UI.\n")
	b.WriteString("\n## Git + SQLite short path\n\n")
	b.WriteString("```bash\ngit init\ngit add .\ngit commit -m \"Import Yaver mobile sandbox\"\n```\n\n")
	b.WriteString("If `local.db` is included, this is enough to move the same SQLite-backed sandbox onto your laptop or server.\n")
	if opts.Containerize {
		b.WriteString("\n## Containerized short path\n\n")
		b.WriteString("```bash\ndocker compose up -d --build\n```\n\n")
		b.WriteString("This runs the exported Yaver-lite backend in a container on your remote hardware or a Hetzner VM.\n")
	}
	return b.String()
}

// phoneAgentsDoc is the AI-coding-agent handoff. Unlike README.md
// (human, "how to move this somewhere"), this tells an agent exactly
// what the project IS and how to continue it — including the Yaver
// Cloud / hosted-box backend story so an agent that resumes on a
// provisioned box needs zero extra configuration.
func phoneAgentsDoc(p *PhoneProject, opts PhoneExportOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# AGENTS.md — %s\n\n", p.Name)
	b.WriteString("You are a coding agent picking up an app built in the Yaver mobile sandbox. ")
	b.WriteString("This bundle is the complete, runnable source of truth. Read it, then continue building.\n\n")

	if p.App != nil && strings.TrimSpace(p.App.Summary) != "" {
		fmt.Fprintf(&b, "## What it is\n\n%s\n\n", strings.TrimSpace(p.App.Summary))
	}

	b.WriteString("## Stack\n\n")
	b.WriteString("- Frontend: React Native (Expo). Runs inside the Yaver container via Hermes push, or standalone.\n")
	b.WriteString("- Backend: a portable mini-backend (SQLite now). Schema/auth/seed are declarative — see the files below.\n\n")

	if p.Schema != nil && len(p.Schema.Tables) > 0 {
		b.WriteString("## Data model\n\n")
		for _, t := range p.Schema.Tables {
			cols := make([]string, 0, len(t.Columns))
			for _, c := range t.Columns {
				cols = append(cols, c.Name)
			}
			fmt.Fprintf(&b, "- **%s**(%s)\n", t.Name, strings.Join(cols, ", "))
		}
		b.WriteString("\nFull definition in `schema.yaml`; ready-to-run DDL in `schema.sql` / `schema.postgres.sql`.\n\n")
	}
	if p.App != nil && len(p.App.Screens) > 0 {
		b.WriteString("## Screens to build / extend\n\n")
		for _, s := range p.App.Screens {
			fmt.Fprintf(&b, "- **%s** (%s) — table `%s`\n", s.Title, s.Kind, s.Table)
		}
		b.WriteString("\nFull UI plan in `app.yaml`.\n\n")
	}

	b.WriteString("## Backend / runtime\n\n")
	b.WriteString("- **Local / dev:** the Yaver agent serves the SQLite mini-backend; `local.db` (if present) holds real runtime rows.\n")
	b.WriteString("- **Yaver Cloud / hosted box:** the box runs its own self-hosted Convex. The app reads its backend URL from the `EXPO_PUBLIC_CONVEX_URL` env var, which Yaver bakes into the bundle automatically on a hosted box — you do **not** hardcode a URL or manage a deploy key.\n")
	b.WriteString("- **Deploy the backend (hosted, zero config):** `yaver deploy --target=selfhosted` (resolves the on-box admin key itself).\n")
	b.WriteString("- **Deploy the backend (bring-your-own Convex Cloud):** set `CONVEX_DEPLOY_KEY`, then `yaver deploy --target=convex`.\n\n")

	b.WriteString("## Files\n\n")
	b.WriteString("- `schema.yaml` / `schema.sql` — data model (source of truth + DDL)\n")
	b.WriteString("- `app.yaml` — screen/UI plan\n")
	b.WriteString("- `auth.yaml` — auth personas\n")
	b.WriteString("- `seed.json` — fixture rows\n")
	if opts.IncludeData {
		b.WriteString("- `local.db` — live SQLite state (zero-loss continuation)\n")
	}
	b.WriteString("- `.yaver/project.yaml` — declarative manifest (`yaver apply`)\n")
	if opts.Containerize {
		b.WriteString("- `Dockerfile` / `docker-compose.yml` — containerized backend\n")
	}
	b.WriteString("\n## How to continue (suggested)\n\n")
	b.WriteString("1. `yaver apply` (or import `schema.sql`) to materialise the backend.\n")
	b.WriteString("2. Implement/extend the screens listed above against the data model.\n")
	b.WriteString("3. Keep the schema in `schema.yaml` authoritative — regenerate DDL, don't hand-edit `schema.sql`.\n")
	b.WriteString("4. On a Yaver Cloud box, the backend is already wired (`EXPO_PUBLIC_CONVEX_URL`). Just build/Hermes-push to preview.\n")
	return b.String()
}

func phoneContainerDockerfile() string {
	return `FROM golang:1.26-bookworm AS builder
RUN go install github.com/yaver-io/agent@latest

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/bin/agent /usr/local/bin/yaver
WORKDIR /workspace
EXPOSE 18080
CMD ["yaver","serve","--debug","--no-quic","--no-relay","--no-tls","--port","18080","--work-dir","/workspace","--containerize-host"]
`
}

func phoneContainerCompose() string {
	return `services:
  yaver-lite:
    build: .
    image: yaver-phone-backend:latest
    restart: unless-stopped
    ports:
      - "18080:18080"
    environment:
      - YAVER_NO_BOOTSTRAP=1
    volumes:
      - ./:/workspace
`
}

func phoneContainerEnvExample(p *PhoneProject) string {
	return fmt.Sprintf("YAVER_PHONE_PROJECT=%s\nPORT=18080\n", p.Slug)
}

func phoneGitIgnore() string {
	return `.DS_Store
.env
*.log
storage/
tmp/
`
}

func phoneDockerIgnore() string {
	return `.git
.gitignore
.env
*.log
storage/
tmp/
`
}

func generateSchemaSQL(s *PhoneSchema, dialect string) (string, error) {
	if s == nil {
		return "", nil
	}
	var b strings.Builder
	for _, t := range s.Tables {
		fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS %q (\n", t.Name)
		var cols []string
		for _, c := range t.Columns {
			typ := dialectType(c.Type, dialect)
			var parts []string
			parts = append(parts, fmt.Sprintf("  %q %s", c.Name, typ))
			if c.Primary {
				parts = append(parts, "PRIMARY KEY")
			}
			if c.Required && !c.Primary {
				parts = append(parts, "NOT NULL")
			}
			if c.Unique && !c.Primary {
				parts = append(parts, "UNIQUE")
			}
			if c.Default != "" {
				switch strings.ToLower(c.Default) {
				case "uuid":
					if dialect == "postgres" {
						parts = append(parts, "DEFAULT gen_random_uuid()::text")
					} else {
						parts = append(parts, "DEFAULT (lower(hex(randomblob(16))))")
					}
				case "now":
					parts = append(parts, "DEFAULT CURRENT_TIMESTAMP")
				default:
					parts = append(parts, "DEFAULT "+sqliteLiteral(c.Default))
				}
			}
			cols = append(cols, strings.Join(parts, " "))
		}
		b.WriteString(strings.Join(cols, ",\n"))
		b.WriteString("\n);\n\n")
	}
	return b.String(), nil
}

func dialectType(t, dialect string) string {
	t = strings.ToLower(t)
	switch dialect {
	case "postgres":
		switch t {
		case "text", "string", "uuid":
			return "TEXT"
		case "int", "integer":
			return "INTEGER"
		case "bool", "boolean":
			return "BOOLEAN"
		case "real", "float":
			return "DOUBLE PRECISION"
		case "timestamp":
			return "TIMESTAMPTZ"
		case "json":
			return "JSONB"
		}
		return "TEXT"
	default:
		return allowedColumnTypes[t]
	}
}

// ---- Templates ----

func templateSchema(name string) *PhoneSchema {
	switch name {
	case "todos":
		return &PhoneSchema{
			Tables: []PhoneTable{
				{
					Name: "users",
					Columns: []PhoneColumn{
						{Name: "id", Type: "text", Primary: true},
						{Name: "email", Type: "text", Required: true, Unique: true},
						{Name: "name", Type: "text"},
					},
				},
				{
					Name: "todos",
					Columns: []PhoneColumn{
						{Name: "id", Type: "text", Primary: true, Default: "uuid"},
						{Name: "title", Type: "text", Required: true},
						{Name: "done", Type: "bool", Default: "false"},
						{Name: "owner_id", Type: "text"},
						{Name: "created_at", Type: "timestamp", Default: "now"},
					},
					Indexes: []PhoneIndex{{Columns: []string{"owner_id"}}, {Columns: []string{"done"}}},
				},
			},
			Relations: []PhoneRelation{
				{From: "todos.owner_id", To: "users.id", OnDelete: "cascade"},
			},
		}
	case "notes":
		return &PhoneSchema{
			Tables: []PhoneTable{
				{
					Name: "users",
					Columns: []PhoneColumn{
						{Name: "id", Type: "text", Primary: true},
						{Name: "email", Type: "text", Required: true, Unique: true},
						{Name: "name", Type: "text"},
					},
				},
				{
					Name: "notes",
					Columns: []PhoneColumn{
						{Name: "id", Type: "text", Primary: true, Default: "uuid"},
						{Name: "title", Type: "text", Required: true},
						{Name: "body", Type: "text"},
						{Name: "owner_id", Type: "text"},
						{Name: "created_at", Type: "timestamp", Default: "now"},
						{Name: "updated_at", Type: "timestamp", Default: "now"},
					},
					Indexes: []PhoneIndex{{Columns: []string{"owner_id"}}},
				},
			},
		}
	case "crud", "":
		return &PhoneSchema{
			Tables: []PhoneTable{
				{
					Name: "users",
					Columns: []PhoneColumn{
						{Name: "id", Type: "text", Primary: true},
						{Name: "email", Type: "text", Required: true, Unique: true},
						{Name: "name", Type: "text"},
					},
				},
				{
					Name: "items",
					Columns: []PhoneColumn{
						{Name: "id", Type: "text", Primary: true, Default: "uuid"},
						{Name: "name", Type: "text", Required: true},
						{Name: "description", Type: "text"},
						{Name: "owner_id", Type: "text"},
						{Name: "created_at", Type: "timestamp", Default: "now"},
					},
				},
			},
		}
	case "blank":
		return &PhoneSchema{Tables: []PhoneTable{}}
	}
	return nil
}

func templateAuth(name string) *PhoneAuth {
	if name == "blank" {
		return &PhoneAuth{Personas: []PhonePersona{}}
	}
	return &PhoneAuth{Personas: []PhonePersona{
		{ID: "alice", Email: "alice@example.com", Name: "Alice"},
		{ID: "bob", Email: "bob@example.com", Name: "Bob"},
	}}
}

func templateSeed(name string) PhoneSeed {
	switch name {
	case "todos":
		return PhoneSeed{
			"todos": []map[string]interface{}{
				{"id": "t1", "title": "Buy milk", "done": false, "owner_id": "alice"},
				{"id": "t2", "title": "Learn Yaver", "done": true, "owner_id": "alice"},
				{"id": "t3", "title": "Ship mini-backend", "done": false, "owner_id": "bob"},
			},
		}
	case "notes":
		return PhoneSeed{
			"notes": []map[string]interface{}{
				{"id": "n1", "title": "Welcome", "body": "This is a starter note.", "owner_id": "alice"},
			},
		}
	case "crud":
		return PhoneSeed{
			"items": []map[string]interface{}{
				{"id": "i1", "name": "Example", "description": "Edit or delete this row.", "owner_id": "alice"},
			},
		}
	}
	return nil
}

func templateApp(name string) *PhoneAppSpec {
	switch name {
	case "todos":
		return &PhoneAppSpec{
			Summary:       "Simple shared todo list with a quick capture flow.",
			PrimaryEntity: "todos",
			Screens: []PhoneScreenSpec{
				{
					ID:         "todo_list",
					Title:      "Todos",
					Kind:       "list",
					Table:      "todos",
					EmptyState: "No tasks yet. Add one from your phone.",
					Actions: []PhoneScreenAction{
						{Label: "Add task", Kind: "create", Table: "todos"},
						{Label: "Toggle done", Kind: "update", Table: "todos"},
					},
				},
			},
		}
	case "notes":
		return &PhoneAppSpec{
			Summary:       "Lightweight notes app with a notes list and editor.",
			PrimaryEntity: "notes",
			Screens: []PhoneScreenSpec{
				{
					ID:         "notes_list",
					Title:      "Notes",
					Kind:       "list",
					Table:      "notes",
					EmptyState: "Start with a quick note.",
					Actions: []PhoneScreenAction{
						{Label: "New note", Kind: "create", Table: "notes"},
						{Label: "Open note", Kind: "navigate", Target: "note_detail"},
					},
				},
			},
		}
	case "crud", "":
		return &PhoneAppSpec{
			Summary:       "Generic CRUD app with a collection list and editor.",
			PrimaryEntity: "items",
			Screens: []PhoneScreenSpec{
				{
					ID:         "items_list",
					Title:      "Items",
					Kind:       "list",
					Table:      "items",
					EmptyState: "Create the first item.",
					Actions: []PhoneScreenAction{
						{Label: "Create item", Kind: "create", Table: "items"},
						{Label: "View item", Kind: "navigate", Target: "item_detail"},
					},
				},
			},
		}
	case "blank":
		return &PhoneAppSpec{
			Summary: "Blank app. Define screens after shaping the schema.",
		}
	}
	return nil
}

// ListPhoneTemplates is used by the mobile/web wizards.
func ListPhoneTemplates() []map[string]string {
	return []map[string]string{
		{"id": "blank", "label": "Blank", "description": "Empty project — define your own schema."},
		{"id": "crud", "label": "Generic CRUD", "description": "users + items table with a few personas."},
		{"id": "todos", "label": "Todos", "description": "users + todos with seeded tasks."},
		{"id": "notes", "label": "Notes", "description": "users + notes with a starter entry."},
	}
}

// ---- Import (inverse of Export) ----

// PhoneImportOptions controls ImportPhoneProject.
type PhoneImportOptions struct {
	// SlugOverride replaces the slug found inside the bundle. Useful when the
	// source slug already exists locally.
	SlugOverride string
	// OnConflict = "reject" (default), "rename" (append -2, -3, …), "overwrite".
	OnConflict string
	// SkipSeed, when true, instantiates the schema + auth but ignores seed.json.
	// Useful for staging moves where the source data is already live elsewhere.
	SkipSeed bool
}

// ImportPhoneProject takes a tarball produced by ExportPhoneProject (or any
// compatible bundle containing schema.yaml + auth.yaml + seed.json) and
// materialises it as a local phone project under ~/.yaver/phone-projects/.
//
// The target yaver serve agent calls this from POST /phone/projects/receive.
// The CLI calls it from `yaver phone push`.
//
// The returned project is fully loaded (schema/auth/seed/stats populated).
// bundleFormat returns "gzip", "zip", or "" by magic bytes. The phone
// can hand the agent either: .tgz (legacy/push) or .zip (the
// coding-agent / OS-friendly twin). Both materialise identically.
func bundleFormat(data []byte) string {
	if len(data) >= 4 && data[0] == 0x50 && data[1] == 0x4b && data[2] == 0x03 && data[3] == 0x04 {
		return "zip"
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return "gzip"
	}
	return ""
}

// addPart applies the shared traversal-safety + top-level-dir rule
// used by both archive readers, so .tgz and .zip import identically.
func addPart(parts map[string][]byte, topDir *string, name string, content []byte) error {
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("unsafe bundle entry: %s", name)
	}
	idx := strings.IndexByte(name, '/')
	if idx <= 0 {
		parts[name] = content
		return nil
	}
	if *topDir == "" {
		*topDir = name[:idx]
	}
	parts[name[idx+1:]] = content
	return nil
}

// decodeBundleParts sniffs the archive format and returns the file set
// (keyed by path under the top-level dir) + that top dir. Format-
// agnostic so receive accepts .tgz AND .zip from the phone.
func decodeBundleParts(data []byte) (map[string][]byte, string, error) {
	parts := map[string][]byte{}
	var topDir string
	switch bundleFormat(data) {
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, "", fmt.Errorf("unzip: %w", err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, "", fmt.Errorf("zip entry %s: %w", f.Name, err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, "", err
			}
			if err := addPart(parts, &topDir, f.Name, b); err != nil {
				return nil, "", err
			}
		}
	case "gzip":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, "", fmt.Errorf("gunzip: %w", err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, "", fmt.Errorf("tar: %w", err)
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, "", err
			}
			if err := addPart(parts, &topDir, hdr.Name, b); err != nil {
				return nil, "", err
			}
		}
	default:
		return nil, "", fmt.Errorf("unrecognised bundle format (expected .tgz or .zip)")
	}
	if topDir == "" {
		return nil, "", fmt.Errorf("bundle missing top-level directory")
	}
	return parts, topDir, nil
}

func ImportPhoneProject(tgz []byte, opts PhoneImportOptions) (*PhoneProject, error) {
	if len(tgz) == 0 {
		return nil, fmt.Errorf("empty bundle")
	}
	parts, topDir, err := decodeBundleParts(tgz)
	if err != nil {
		return nil, err
	}

	// Resolve target slug.
	slug := Slugify(opts.SlugOverride)
	if slug == "" {
		slug = Slugify(topDir)
	}
	if slug == "" {
		return nil, fmt.Errorf("could not resolve slug from bundle or override")
	}

	// Honour conflict policy.
	if exists, _ := phoneProjectExists(slug); exists {
		switch opts.OnConflict {
		case "overwrite":
			if err := DeletePhoneProject(slug); err != nil {
				return nil, fmt.Errorf("overwrite: %w", err)
			}
		case "rename":
			slug = uniquePhoneSlug(slug)
		default: // "reject" or ""
			return nil, fmt.Errorf("%w: %s", ErrPhoneProjectExists, slug)
		}
	}

	// Decode schema + auth + seed if present.
	var schema *PhoneSchema
	if b, ok := parts["schema.yaml"]; ok {
		var s PhoneSchema
		if err := yaml.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("schema.yaml: %w", err)
		}
		schema = &s
	}
	var auth *PhoneAuth
	if b, ok := parts["auth.yaml"]; ok {
		var a PhoneAuth
		if err := yaml.Unmarshal(b, &a); err != nil {
			return nil, fmt.Errorf("auth.yaml: %w", err)
		}
		auth = &a
	}
	var seed PhoneSeed
	if !opts.SkipSeed {
		if b, ok := parts["seed.json"]; ok {
			if err := json.Unmarshal(b, &seed); err != nil {
				return nil, fmt.Errorf("seed.json: %w", err)
			}
		}
	}
	var app *PhoneAppSpec
	if b, ok := parts["app.yaml"]; ok {
		var a PhoneAppSpec
		if err := yaml.Unmarshal(b, &a); err != nil {
			return nil, fmt.Errorf("app.yaml: %w", err)
		}
		app = &a
	}
	var liveDB []byte
	if !opts.SkipSeed {
		liveDB = parts["local.db"]
	}
	oauthProviders := parts["oauth-providers.yaml"]

	// Recover the project display name from project.yaml if it's in the bundle.
	name := slug
	if b, ok := parts[".yaver/project.yaml"]; ok {
		var m struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(b, &m); err == nil && m.Name != "" {
			name = m.Name
		}
	}

	p, err := CreatePhoneProject(PhoneCreateSpec{
		Slug:   slug,
		Name:   name,
		Schema: schema,
		Auth:   auth,
		Seed:   seed,
		App:    app,
	})
	if err != nil {
		return nil, err
	}
	if len(liveDB) > 0 {
		if err := os.WriteFile(dbFilePath(p.Dir), liveDB, 0o600); err != nil {
			return nil, fmt.Errorf("restore local.db: %w", err)
		}
	}
	if len(oauthProviders) > 0 {
		if err := os.WriteFile(phoneOAuthPath(p.Dir), oauthProviders, 0o600); err != nil {
			return nil, fmt.Errorf("restore oauth-providers.yaml: %w", err)
		}
	}
	for name, content := range parts {
		switch name {
		case "schema.yaml", "auth.yaml", "seed.json", "app.yaml", "local.db",
			"oauth-providers.yaml", ".yaver/config.yaml", ".yaver/project.yaml":
			continue
		}
		clean := filepath.Clean(name)
		if clean == "." || clean == "" || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("unsafe extra file: %s", name)
		}
		dst := filepath.Join(p.Dir, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir extra file dir: %w", err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return nil, fmt.Errorf("restore extra file %s: %w", name, err)
		}
	}
	return p, nil
}

func phoneProjectExists(slug string) (bool, error) {
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(dir)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func uniquePhoneSlug(base string) string {
	for n := 2; n < 1000; n++ {
		cand := fmt.Sprintf("%s-%d", base, n)
		if exists, _ := phoneProjectExists(cand); !exists {
			return cand
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

// ---- Helpers ----

// makeTarForTest is a testing helper.
func readTarNames(data []byte) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, hdr.Name)
	}
	return out, nil
}
