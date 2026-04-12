package main

import (
	"encoding/json"
	"net/http"
)

// CatalogEntry is a one-click installable service (like a GCP Marketplace item).
type CatalogEntry struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Tags        []string       `json:"tags"`
	Image       string         `json:"image,omitempty"`
	DefaultPort int            `json:"defaultPort,omitempty"`
	Fields      []CatalogField `json:"fields"`
	EnvTemplate string         `json:"envTemplate,omitempty"`
	Dashboard   string         `json:"dashboard,omitempty"` // studio id from studioTargets
	Memory      string         `json:"memory,omitempty"`
	Notes       string         `json:"notes,omitempty"`
	ServicePreset string       `json:"servicePreset,omitempty"` // maps to services.go presets()
}

type CatalogField struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Default string `json:"default,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
	Generate string `json:"generate,omitempty"` // random_32, random_16
}

// catalogEntries is the built-in service marketplace. Expands the existing
// services.go presets with user-facing metadata + a "dashboard" pointer so the
// console can wire the "Open Studio" button after install.
func catalogEntries() []CatalogEntry {
	return []CatalogEntry{
		// Databases
		{ID: "postgres", Name: "PostgreSQL", Category: "databases", Description: "Relational database with ACID guarantees", Tags: []string{"sql", "relational"}, Image: "postgres:16", DefaultPort: 5432, Memory: "512m", Dashboard: "drizzle", ServicePreset: "postgres", Fields: []CatalogField{
			{Key: "POSTGRES_USER", Label: "Username", Default: "postgres"},
			{Key: "POSTGRES_PASSWORD", Label: "Password", Secret: true, Generate: "random_32"},
			{Key: "POSTGRES_DB", Label: "Database", Default: "{project}"},
		}, EnvTemplate: "DATABASE_URL=postgresql://{POSTGRES_USER}:{POSTGRES_PASSWORD}@localhost:{port}/{POSTGRES_DB}"},
		{ID: "redis", Name: "Redis (Valkey)", Category: "databases", Description: "In-memory key/value cache", Tags: []string{"cache", "kv"}, Image: "valkey/valkey:8-alpine", DefaultPort: 6379, Memory: "256m", ServicePreset: "redis", Fields: []CatalogField{}, EnvTemplate: "REDIS_URL=redis://localhost:{port}"},
		{ID: "mysql", Name: "MySQL", Category: "databases", Description: "Classic relational database", Tags: []string{"sql"}, Image: "mysql:8", DefaultPort: 3306, Memory: "1g", Fields: []CatalogField{
			{Key: "MYSQL_ROOT_PASSWORD", Secret: true, Generate: "random_32"},
			{Key: "MYSQL_DATABASE", Default: "{project}"},
		}, EnvTemplate: "DATABASE_URL=mysql://root:{MYSQL_ROOT_PASSWORD}@localhost:{port}/{MYSQL_DATABASE}"},
		{ID: "mongo", Name: "MongoDB", Category: "databases", Description: "Document database", Tags: []string{"nosql", "document"}, Image: "mongo:7", DefaultPort: 27017, Memory: "1g", Fields: []CatalogField{}, EnvTemplate: "MONGODB_URI=mongodb://localhost:{port}"},

		// Backend platforms (map to existing Yaver backend kinds)
		{ID: "convex-local", Name: "Convex (local)", Category: "platforms", Description: "Reactive TypeScript backend with realtime queries", Tags: []string{"baas", "reactive"}, Image: "ghcr.io/get-convex/convex-backend:latest", DefaultPort: 3210, Memory: "1g", Dashboard: "convex", ServicePreset: "convex", Fields: []CatalogField{}, EnvTemplate: "CONVEX_SELF_HOSTED_URL=http://127.0.0.1:3210"},
		{ID: "pocketbase", Name: "PocketBase", Category: "platforms", Description: "SQLite BaaS in a single binary", Tags: []string{"baas", "sqlite"}, Image: "ghcr.io/muchobien/pocketbase:latest", DefaultPort: 8090, Memory: "128m", Dashboard: "pocketbase", ServicePreset: "pocketbase", Fields: []CatalogField{}, EnvTemplate: "POCKETBASE_URL=http://127.0.0.1:{port}"},
		{ID: "supabase-local", Name: "Supabase (local)", Category: "platforms", Description: "Full Postgres + Auth + Storage + Realtime stack", Tags: []string{"baas", "postgres"}, Memory: "4g", Dashboard: "supabase", Notes: "Launched via `supabase start` (CLI-driven, not plain Docker)"},

		// Dev tools
		{ID: "mailpit", Name: "Mailpit", Category: "devtools", Description: "SMTP server + web UI for email testing", Tags: []string{"smtp", "email"}, DefaultPort: 8025, Dashboard: "mailpit", ServicePreset: "mailpit", Fields: []CatalogField{}, EnvTemplate: "SMTP_HOST=localhost\nSMTP_PORT=1025"},
		{ID: "minio", Name: "MinIO", Category: "devtools", Description: "S3-compatible object storage", Tags: []string{"s3", "storage"}, Image: "minio/minio:latest", DefaultPort: 9000, Memory: "256m", Dashboard: "minio", ServicePreset: "minio", Fields: []CatalogField{
			{Key: "MINIO_ROOT_USER", Default: "minioadmin"},
			{Key: "MINIO_ROOT_PASSWORD", Secret: true, Generate: "random_32"},
		}, EnvTemplate: "S3_ENDPOINT=http://localhost:{port}\nS3_ACCESS_KEY={MINIO_ROOT_USER}\nS3_SECRET_KEY={MINIO_ROOT_PASSWORD}"},

		// Cloud emulators (already presets, surfaced here for the UI)
		{ID: "dynamodb-local", Name: "DynamoDB Local", Category: "cloud-emulators", Description: "Amazon DynamoDB local emulator", Tags: []string{"aws", "nosql"}, Image: "amazon/dynamodb-local:latest", DefaultPort: 8000, ServicePreset: "dynamodb-local"},
		{ID: "elasticmq", Name: "ElasticMQ (SQS)", Category: "cloud-emulators", Description: "SQS-compatible local queue", Tags: []string{"aws", "queue"}, Image: "softwaremill/elasticmq-native:latest", DefaultPort: 9324, ServicePreset: "elasticmq"},
		{ID: "azurite", Name: "Azurite", Category: "cloud-emulators", Description: "Azure Blob/Queue/Table emulator", Tags: []string{"azure"}, Image: "mcr.microsoft.com/azure-storage/azurite:latest", DefaultPort: 10000, ServicePreset: "azurite"},

		// AI
		{ID: "ollama", Name: "Ollama", Category: "ai", Description: "Local LLM runtime", Tags: []string{"llm", "inference"}, Image: "ollama/ollama:latest", DefaultPort: 11434, Memory: "4g", Fields: []CatalogField{}, EnvTemplate: "OLLAMA_URL=http://localhost:{port}"},
		{ID: "chromadb", Name: "ChromaDB", Category: "ai", Description: "Vector database for embeddings", Tags: []string{"vector", "embeddings"}, Image: "chromadb/chroma:latest", DefaultPort: 8000, Memory: "512m"},
		{ID: "qdrant", Name: "Qdrant", Category: "ai", Description: "High-performance vector search engine", Tags: []string{"vector"}, Image: "qdrant/qdrant:latest", DefaultPort: 6333, Memory: "512m"},
	}
}

// mcpCatalogList returns the catalog grouped by category.
func mcpCatalogList() interface{} {
	groups := map[string][]CatalogEntry{}
	for _, e := range catalogEntries() {
		groups[e.Category] = append(groups[e.Category], e)
	}
	return map[string]interface{}{"categories": groups, "entries": catalogEntries()}
}

// CatalogInstall installs a catalog entry into the given project. Uses the
// servicePreset to add the service via ServicesManager when available,
// otherwise falls back to a raw add with the entry's image/port.
func CatalogInstall(projectDir, entryID string, fieldValues map[string]string) (interface{}, error) {
	var entry *CatalogEntry
	for _, e := range catalogEntries() {
		if e.ID == entryID {
			c := e
			entry = &c
			break
		}
	}
	if entry == nil {
		return nil, errUnknownCatalogEntry(entryID)
	}
	sm := NewServicesManager(projectDir)
	name := entry.ServicePreset
	if name == "" {
		name = entry.ID
	}
	// If there's a preset, the manager already knows how to wire it.
	if _, ok := presets()[name]; ok {
		msg, err := sm.Add(name, nil)
		if err != nil {
			return nil, err
		}
		// Start immediately — vibe coders expect "click install → running".
		start, _ := sm.Start(name)
		return map[string]interface{}{"added": msg, "started": start, "entry": entry}, nil
	}
	// No preset: build a minimal DevServiceConfig from catalog metadata.
	if entry.Image == "" {
		return nil, errUnsupportedInstall(entryID)
	}
	cfg := &DevServiceConfig{
		Image: entry.Image,
		Port:  entry.DefaultPort,
	}
	if len(fieldValues) > 0 {
		cfg.Env = map[string]string{}
		for k, v := range fieldValues {
			cfg.Env[k] = v
		}
	}
	if _, err := sm.Add(name, cfg); err != nil {
		return nil, err
	}
	start, _ := sm.Start(name)
	return map[string]interface{}{"added": name, "started": start, "entry": entry}, nil
}

func errUnknownCatalogEntry(id string) error       { return jsonError2("unknown catalog entry " + id) }
func errUnsupportedInstall(id string) error         { return jsonError2("no installer for " + id + " (add image in catalog entry or services.go preset)") }

type catalogError struct{ msg string }

func (e *catalogError) Error() string { return e.msg }
func jsonError2(s string) error       { return &catalogError{msg: s} }

// ---- HTTP ----

func (s *HTTPServer) handleCatalogList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpCatalogList())
}

func (s *HTTPServer) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		ID     string            `json:"id"`
		Fields map[string]string `json:"fields"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := CatalogInstall(s.dirParam(r), b.ID, b.Fields)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
