package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// BackendKind identifies which backend a project uses.
type BackendKind string

const (
	BackendConvex   BackendKind = "convex"
	BackendSupabase BackendKind = "supabase"
	BackendPostgres BackendKind = "postgres"
	BackendSQLite   BackendKind = "sqlite"
	// BackendPocketBase + BackendAppwrite were removed 2026-04-28
	// per the lean-stack cut. Re-add only if the supported backend
	// matrix expands. See project_lean_stack_2026_04_28.md.
)

// TableInfo is a universal table/collection descriptor.
type TableInfo struct {
	Name     string `json:"name"`
	RowCount *int64 `json:"rowCount,omitempty"`
	Kind     string `json:"kind,omitempty"` // table, view, collection
}

// BrowseResult is a universal paginated read.
type BrowseResult struct {
	Rows       []map[string]interface{} `json:"rows"`
	NextCursor string                   `json:"nextCursor,omitempty"`
	Total      *int64                   `json:"total,omitempty"`
}

// BackendStatus is the universal health response.
type BackendStatus struct {
	Kind    BackendKind `json:"kind"`
	URL     string      `json:"url"`
	Running bool        `json:"running"`
	Error   string      `json:"error,omitempty"`
	Hint    string      `json:"hint,omitempty"`
	Version string      `json:"version,omitempty"`
}

// BackendAdapter is the universal interface every backend implements.
type BackendAdapter interface {
	Kind() BackendKind
	Status() BackendStatus
	ListTables() ([]TableInfo, error)
	Browse(table string, cursor string, limit int) (*BrowseResult, error)
	// Query runs an adapter-native query. For SQL backends this is SQL,
	// for Convex a function path, for PocketBase/Appwrite a REST path.
	Query(q string, args map[string]interface{}) (interface{}, error)
	Insert(table string, doc map[string]interface{}) (string, error)
	Update(table string, id string, fields map[string]interface{}) error
	Delete(table string, id string) error
}

// YaverProjectConfig is the persisted .yaver/config.yaml schema.
type YaverProjectConfig struct {
	Backend BackendKind       `yaml:"backend" json:"backend"`
	DB      string            `yaml:"db,omitempty" json:"db,omitempty"` // connection hint (url, file path)
	Auth    string            `yaml:"auth,omitempty" json:"auth,omitempty"`
	Stack   string            `yaml:"stack,omitempty" json:"stack,omitempty"`
	Cloud   []string          `yaml:"cloud,omitempty" json:"cloud,omitempty"` // aws, gcp, azure
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// LoadProjectConfig reads .yaver/config.yaml from the given project directory.
// If missing, it tries to infer the backend from files (package.json deps,
// pocketbase binary, .env vars, etc.) and returns a best-effort guess.
func LoadProjectConfig(dir string) (*YaverProjectConfig, error) {
	if dir == "" {
		return nil, fmt.Errorf("project directory required")
	}
	cfgPath := filepath.Join(dir, ".yaver", "config.yaml")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var cfg YaverProjectConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", cfgPath, err)
		}
		if cfg.Backend == "" {
			cfg.Backend = inferBackend(dir)
		}
		return &cfg, nil
	}
	return &YaverProjectConfig{Backend: inferBackend(dir)}, nil
}

// SaveProjectConfig writes .yaver/config.yaml.
func SaveProjectConfig(dir string, cfg *YaverProjectConfig) error {
	d := filepath.Join(dir, ".yaver")
	if err := os.MkdirAll(d, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "config.yaml"), data, 0644)
}

// inferBackend peeks at project files to guess the backend.
func inferBackend(dir string) BackendKind {
	// Convex: convex/ dir with _generated/
	if _, err := os.Stat(filepath.Join(dir, "convex", "_generated")); err == nil {
		return BackendConvex
	}
	if _, err := os.Stat(filepath.Join(dir, "convex.json")); err == nil {
		return BackendConvex
	}
	// Supabase: supabase/config.toml
	if _, err := os.Stat(filepath.Join(dir, "supabase", "config.toml")); err == nil {
		return BackendSupabase
	}
	// package.json deps
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		s := string(data)
		switch {
		case strings.Contains(s, "\"convex\""):
			return BackendConvex
		case strings.Contains(s, "@supabase/supabase-js"):
			return BackendSupabase
		case strings.Contains(s, "\"pg\"") || strings.Contains(s, "drizzle-orm"):
			return BackendPostgres
		case strings.Contains(s, "better-sqlite3"):
			return BackendSQLite
		}
	}
	return ""
}

// NewBackendAdapter builds the appropriate adapter for a project.
func NewBackendAdapter(dir string) (BackendAdapter, error) {
	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		return nil, err
	}
	return newBackendAdapter(dir, cfg)
}

func newBackendAdapter(dir string, cfg *YaverProjectConfig) (BackendAdapter, error) {
	switch cfg.Backend {
	case BackendConvex:
		return &convexAdapter{client: NewConvexAdminClient(dir), dir: dir}, nil
	case BackendSupabase:
		return newSQLAdapter(dir, cfg, BackendSupabase)
	case BackendPostgres:
		return newSQLAdapter(dir, cfg, BackendPostgres)
	case BackendSQLite:
		return newSQLAdapter(dir, cfg, BackendSQLite)
	case "":
		return nil, fmt.Errorf("could not detect backend for %s — add .yaver/config.yaml with `backend: <name>`", dir)
	default:
		return nil, fmt.Errorf("unsupported backend %q", cfg.Backend)
	}
}

// AllBackendKinds returns the list of known backend kinds (for UI pickers).
func AllBackendKinds() []BackendKind {
	return []BackendKind{
		BackendConvex, BackendSupabase, BackendPostgres, BackendSQLite,
	}
}
