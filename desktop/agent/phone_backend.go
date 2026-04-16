package main

import (
	"archive/tar"
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

// PhoneProject is the full metadata of a mini-backend project.
type PhoneProject struct {
	Slug      string       `yaml:"slug" json:"slug"`
	Name      string       `yaml:"name" json:"name"`
	Template  string       `yaml:"template,omitempty" json:"template,omitempty"`
	Dir       string       `yaml:"dir" json:"dir"`
	CreatedAt string       `yaml:"createdAt" json:"createdAt"`
	UpdatedAt string       `yaml:"updatedAt" json:"updatedAt"`
	Schema    *PhoneSchema `yaml:"-" json:"schema,omitempty"`
	Auth      *PhoneAuth   `yaml:"-" json:"auth,omitempty"`
	Seed      PhoneSeed    `yaml:"-" json:"seed,omitempty"`
	Stats     *PhoneStats  `yaml:"-" json:"stats,omitempty"`
}

// PhoneStats are live counts from the SQLite file.
type PhoneStats struct {
	TableCount int            `json:"tableCount"`
	RowCount   int64          `json:"rowCount"`
	PerTable   map[string]int64 `json:"perTable"`
	DBBytes    int64          `json:"dbBytes"`
}

// PhoneCreateSpec is the payload for creating a new project.
type PhoneCreateSpec struct {
	Slug     string       `json:"slug"`
	Name     string       `json:"name"`
	Template string       `json:"template,omitempty"`
	Schema   *PhoneSchema `json:"schema,omitempty"`
	Auth     *PhoneAuth   `json:"auth,omitempty"`
	Seed     PhoneSeed    `json:"seed,omitempty"`
}

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

func schemaPath(dir string) string  { return filepath.Join(dir, "schema.yaml") }
func authPath(dir string) string    { return filepath.Join(dir, "auth.yaml") }
func seedPath(dir string) string    { return filepath.Join(dir, "seed.json") }
func dbFilePath(dir string) string  { return filepath.Join(dir, "local.db") }

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

// ExportPhoneProject returns a tgz bundle of the project (sans local.db binary —
// schema + seed are the portable representation). The bundle is a drop-in for
// `yaver apply` on any machine.
func ExportPhoneProject(slug string) ([]byte, error) {
	p, err := LoadPhoneProject(slug)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	add := func(name string, content []byte, mode int64) error {
		hdr := &tar.Header{
			Name:    filepath.Join(p.Slug, name),
			Size:    int64(len(content)),
			Mode:    mode,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(content)
		return err
	}

	if b, err := os.ReadFile(filepath.Join(p.Dir, ".yaver", "config.yaml")); err == nil {
		_ = add(".yaver/config.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, ".yaver", "project.yaml")); err == nil {
		_ = add(".yaver/project.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(schemaPath(p.Dir)); err == nil {
		_ = add("schema.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(authPath(p.Dir)); err == nil {
		_ = add("auth.yaml", b, 0o644)
	}
	if b, err := os.ReadFile(seedPath(p.Dir)); err == nil {
		_ = add("seed.json", b, 0o644)
	}
	// Generate CREATE TABLE statements so non-Yaver environments can import.
	if ddl, err := generateSchemaSQL(p.Schema, "sqlite"); err == nil && ddl != "" {
		_ = add("schema.sql", []byte(ddl), 0o644)
	}
	if ddl, err := generateSchemaSQL(p.Schema, "postgres"); err == nil && ddl != "" {
		_ = add("schema.postgres.sql", []byte(ddl), 0o644)
	}
	_ = add("README.md", []byte(phoneReadme(p)), 0o644)

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func phoneReadme(p *PhoneProject) string {
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
	b.WriteString("- `schema.sql` / `schema.postgres.sql` — generated DDL for non-Yaver imports\n\n")
	b.WriteString("## Promoting to a real backend\n\n")
	b.WriteString("```\nyaver switch plan --to supabase-cloud\nyaver switch plan --to convex-cloud\nyaver switch plan --to postgres-neon\n```\n\n")
	b.WriteString("Or open the project in the Yaver desktop app and pick a target from the switch engine UI.\n")
	return b.String()
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
func ImportPhoneProject(tgz []byte, opts PhoneImportOptions) (*PhoneProject, error) {
	if len(tgz) == 0 {
		return nil, fmt.Errorf("empty bundle")
	}
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	// Files we care about, keyed by base path (stripped of the top-level dir).
	parts := map[string][]byte{}
	var topDir string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		// Reject absolute / traversal paths — belt-and-suspenders against a
		// hostile bundle.
		if strings.HasPrefix(hdr.Name, "/") || strings.Contains(hdr.Name, "..") {
			return nil, fmt.Errorf("unsafe tar entry: %s", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// First directory component is treated as the slug root.
		idx := strings.IndexByte(hdr.Name, '/')
		if idx <= 0 {
			// Bundle without a top-level dir; treat filename as-is.
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			parts[hdr.Name] = b
			continue
		}
		if topDir == "" {
			topDir = hdr.Name[:idx]
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		parts[hdr.Name[idx+1:]] = b
	}
	if topDir == "" {
		return nil, fmt.Errorf("bundle missing top-level directory")
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
	})
	if err != nil {
		return nil, err
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
