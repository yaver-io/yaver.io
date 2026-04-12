package main

// backend_pb.go — PocketBase backend manager for Yaver workspaces.
//
// PocketBase is a single Go binary that bundles SQLite, auth, file storage,
// and realtime subscriptions — a self-hosted Supabase alternative.
//
// Three run modes are supported:
//
//   standalone — download the PocketBase binary from GitHub releases and
//                run it as a child process. Best for local dev machines.
//   docker     — pull and run ghcr.io/pocketbase/pocketbase in a Docker
//                container. Best for cloud / CI environments.
//   embedded   — placeholder for future embedding inside the relay binary.
//
// Admin credentials are created on first startup via the PocketBase Admin API.
// A JWT admin token is obtained and cached for the lifetime of the manager so
// that subsequent API calls do not re-authenticate every time.
//
// HTTP surface (registered in httpserver.go):
//
//   POST   /pb/start                — start PocketBase (mode, port)
//   POST   /pb/stop                 — stop PocketBase
//   GET    /pb/status               — health + stats
//   GET    /pb/collections          — list collections
//   POST   /pb/collections          — create collection
//   GET    /pb/records/{collection} — query records
//   POST   /pb/records/{collection} — create record
//   POST   /pb/users                — user management (list/create/delete)
//   POST   /pb/migrate              — run migrations
//   POST   /pb/backup               — backup pb_data
//   POST   /pb/restore              — restore from backup
//   GET    /pb/admin-url            — admin UI URL
//   GET    /pb/setup                — SDK setup snippet
//   POST   /pb/upgrade              — migrate to Postgres

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────────────────

// PBField describes a single field inside a PocketBase collection schema.
type PBField struct {
	Name     string                 `json:"name"`
	// Type is one of: text, number, bool, email, url, date, file, relation,
	// json, select.
	Type     string                 `json:"type"`
	Required bool                   `json:"required"`
	Unique   bool                   `json:"unique"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

// PBCollection mirrors the PocketBase collection resource.
type PBCollection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	// Type is one of: base, auth, view.
	Type        string    `json:"type"`
	Schema      []PBField `json:"schema"`
	Created     string    `json:"created"`
	Updated     string    `json:"updated"`
	RecordCount int       `json:"recordCount"`
}

// PBRecord is a single PocketBase record with its raw data payload.
type PBRecord struct {
	ID             string                 `json:"id"`
	CollectionName string                 `json:"collectionName"`
	Data           map[string]interface{} `json:"data"`
	Created        string                 `json:"created"`
	Updated        string                 `json:"updated"`
}

// PBUser represents a PocketBase user record from the _pb_users_auth
// collection.
type PBUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Verified bool   `json:"verified"`
	Created  string `json:"created"`
}

// PBStatus is returned by Status() and describes the runtime state of the
// PocketBase instance managed by this manager.
type PBStatus struct {
	Running     bool   `json:"running"`
	// Mode is one of: embedded, standalone, docker.
	Mode        string `json:"mode"`
	Port        int    `json:"port"`
	URL         string `json:"url"`
	AdminURL    string `json:"adminUrl"`
	DataDir     string `json:"dataDir"`
	Collections int    `json:"collections"`
	Users       int    `json:"users"`
	StorageUsed string `json:"storageUsed"`
}

// PocketBaseManager manages a PocketBase process or Docker container.
// All exported methods are safe for concurrent use.
type PocketBaseManager struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	mode          string
	port          int
	dataDir       string
	adminEmail    string
	adminPassword string

	// cached admin JWT so we don't re-authenticate on every API call.
	adminToken    string
	tokenExpiry   time.Time
}

// ──────────────────────────────────────────────────────────────────────────
// Constructor
// ──────────────────────────────────────────────────────────────────────────

// NewPocketBaseManager returns a manager with sensible defaults:
//   port    = 8090
//   dataDir = ~/.yaver/pb_data
func NewPocketBaseManager() *PocketBaseManager {
	dataDir := filepath.Join(os.TempDir(), "yaver_pb_data") // fallback
	if home, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(home, ".yaver", "pb_data")
	}
	return &PocketBaseManager{
		port:          8090,
		dataDir:       dataDir,
		adminEmail:    "admin@yaver.local",
		adminPassword: "yaver_admin_" + randomHex(8),
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Public API
// ──────────────────────────────────────────────────────────────────────────

// Start launches PocketBase in the requested mode.
//
// mode must be "standalone", "docker", or "embedded".
// port overrides the default (8090) when non-zero.
//
// On first startup the admin account is created automatically.
// Returns the app URL and admin URL as a human-readable summary.
func (m *PocketBaseManager) Start(mode string, port int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if port > 0 {
		m.port = port
	}
	m.mode = mode

	switch mode {
	case "standalone":
		return m.startStandalone()
	case "docker":
		return m.startDocker()
	case "embedded":
		return "", fmt.Errorf("embedded mode is not yet implemented")
	default:
		return "", fmt.Errorf("unknown mode %q: want standalone|docker|embedded", mode)
	}
}

// Stop shuts down the PocketBase process or Docker container.
func (m *PocketBaseManager) Stop() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.mode {
	case "docker":
		return m.stopDocker()
	default:
		return m.stopStandalone()
	}
}

// Status returns a snapshot of the current PocketBase runtime state including
// collection count, user count, and approximate data-directory size.
func (m *PocketBaseManager) Status() (*PBStatus, error) {
	running := m.isRunning()
	status := &PBStatus{
		Running:  running,
		Mode:     m.mode,
		Port:     m.port,
		URL:      fmt.Sprintf("http://localhost:%d", m.port),
		AdminURL: fmt.Sprintf("http://localhost:%d/_/", m.port),
		DataDir:  m.dataDir,
	}

	if !running {
		return status, nil
	}

	// Collection count.
	cols, err := m.Collections()
	if err == nil {
		status.Collections = len(cols)
	}

	// User count.
	users, err := m.Users("list", "", "")
	if err == nil {
		if list, ok := users.([]PBUser); ok {
			status.Users = len(list)
		}
	}

	// Storage — uses package-level dirSize (returns int64) and humanBytes.
	status.StorageUsed = humanBytes(dirSize(m.dataDir))

	return status, nil
}

// Collections lists all collections via GET /api/collections.
func (m *PocketBaseManager) Collections() ([]PBCollection, error) {
	raw, err := m.pbAPI("GET", "/api/collections?perPage=500", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Schema  []struct {
				Name     string                 `json:"name"`
				Type     string                 `json:"type"`
				Required bool                   `json:"required"`
				Unique   bool                   `json:"unique"`
				Options  map[string]interface{} `json:"options"`
			} `json:"schema"`
			Created string `json:"created"`
			Updated string `json:"updated"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse collections response: %w", err)
	}

	cols := make([]PBCollection, 0, len(resp.Items))
	for _, item := range resp.Items {
		col := PBCollection{
			ID:      item.ID,
			Name:    item.Name,
			Type:    item.Type,
			Created: item.Created,
			Updated: item.Updated,
		}
		for _, f := range item.Schema {
			col.Schema = append(col.Schema, PBField{
				Name:     f.Name,
				Type:     f.Type,
				Required: f.Required,
				Unique:   f.Unique,
				Options:  f.Options,
			})
		}
		cols = append(cols, col)
	}
	return cols, nil
}

// CreateCollection creates a new PocketBase collection.
// collType must be "base", "auth", or "view".
func (m *PocketBaseManager) CreateCollection(name, collType string, schema []PBField) (string, error) {
	if name == "" {
		return "", fmt.Errorf("collection name is required")
	}
	if collType == "" {
		collType = "base"
	}

	// Map PBField to PocketBase wire format.
	type pbSchemaField struct {
		Name     string                 `json:"name"`
		Type     string                 `json:"type"`
		Required bool                   `json:"required"`
		Unique   bool                   `json:"unique"`
		Options  map[string]interface{} `json:"options,omitempty"`
	}
	fields := make([]pbSchemaField, 0, len(schema))
	for _, f := range schema {
		fields = append(fields, pbSchemaField{
			Name:     f.Name,
			Type:     f.Type,
			Required: f.Required,
			Unique:   f.Unique,
			Options:  f.Options,
		})
	}

	body := map[string]interface{}{
		"name":   name,
		"type":   collType,
		"schema": fields,
	}

	raw, err := m.pbAPI("POST", "/api/collections", body)
	if err != nil {
		return "", err
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return "", fmt.Errorf("parse create-collection response: %w", err)
	}
	return fmt.Sprintf("created collection %q (id=%s)", name, created.ID), nil
}

// Records queries records from a collection.
//
//   collection — collection name or ID
//   filter     — PocketBase filter string, e.g. `status = "active"`
//   sort       — sort string, e.g. `-created,name`
//   limit      — max records to return (0 = default 30)
func (m *PocketBaseManager) Records(collection, filter, sort string, limit int) ([]PBRecord, error) {
	if collection == "" {
		return nil, fmt.Errorf("collection is required")
	}
	if limit <= 0 {
		limit = 30
	}

	path := fmt.Sprintf("/api/collections/%s/records?perPage=%d", collection, limit)
	if filter != "" {
		path += "&filter=" + urlEncode(filter)
	}
	if sort != "" {
		path += "&sort=" + urlEncode(sort)
	}

	raw, err := m.pbAPI("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse records response: %w", err)
	}

	records := make([]PBRecord, 0, len(resp.Items))
	for _, item := range resp.Items {
		rec := PBRecord{
			CollectionName: collection,
			Data:           item,
		}
		if id, _ := item["id"].(string); id != "" {
			rec.ID = id
		}
		if c, _ := item["created"].(string); c != "" {
			rec.Created = c
		}
		if u, _ := item["updated"].(string); u != "" {
			rec.Updated = u
		}
		records = append(records, rec)
	}
	return records, nil
}

// CreateRecord creates a new record in the given collection.
// Returns a human-readable summary including the new record ID.
func (m *PocketBaseManager) CreateRecord(collection string, data map[string]interface{}) (string, error) {
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}

	raw, err := m.pbAPI("POST", fmt.Sprintf("/api/collections/%s/records", collection), data)
	if err != nil {
		return "", err
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return "", fmt.Errorf("parse create-record response: %w", err)
	}
	return fmt.Sprintf("created record %s in %q", created.ID, collection), nil
}

// Users manages users in the built-in _pb_users_auth collection.
//
//   action  — "list", "create", or "delete"
//   email   — required for "create" and "delete"
//   password — required for "create"
//
// Returns []PBUser for "list", a PBUser for "create", and a string for
// "delete".
func (m *PocketBaseManager) Users(action, email, password string) (interface{}, error) {
	switch action {
	case "list":
		return m.listUsers()
	case "create":
		if email == "" || password == "" {
			return nil, fmt.Errorf("email and password are required for create")
		}
		return m.createUser(email, password)
	case "delete":
		if email == "" {
			return nil, fmt.Errorf("email is required for delete")
		}
		return m.deleteUser(email)
	default:
		return nil, fmt.Errorf("unknown action %q: want list|create|delete", action)
	}
}

// Migrate runs PocketBase migrations (`pocketbase migrate`).
func (m *PocketBaseManager) Migrate() (string, error) {
	bin, err := m.pbBinaryPath()
	if err != nil {
		return "", fmt.Errorf("locate pocketbase binary: %w", err)
	}

	out, err := exec.Command(bin, "migrate", "--dir", m.dataDir).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("migrate: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Backup creates a compressed .tar.gz archive of the pb_data directory.
// output is the destination file path; if empty a timestamped file is created
// in ~/.yaver/backups/.
func (m *PocketBaseManager) Backup(output string) (*DBBackupInfo, error) {
	if output == "" {
		backupDir := filepath.Join(m.dataDir, "..", "backups")
		if err := os.MkdirAll(backupDir, 0700); err != nil {
			return nil, fmt.Errorf("create backup dir: %w", err)
		}
		output = filepath.Join(backupDir, fmt.Sprintf("pb_backup_%s.tar.gz",
			time.Now().Format("20060102_150405")))
	}

	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	f, err := os.Create(output)
	if err != nil {
		return nil, fmt.Errorf("create backup file: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	if err := pbAddDirToTar(tw, m.dataDir, filepath.Base(m.dataDir)); err != nil {
		return nil, fmt.Errorf("archive pb_data: %w", err)
	}

	// Flush writers before stat.
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}

	info, err := os.Stat(output)
	if err != nil {
		return nil, fmt.Errorf("stat backup file: %w", err)
	}

	// Count SQLite files as a proxy for "tables".
	tables := 0
	_ = filepath.Walk(m.dataDir, func(p string, fi os.FileInfo, _ error) error {
		if strings.HasSuffix(p, ".db") {
			tables++
		}
		return nil
	})

	return &DBBackupInfo{
		Path:      output,
		Size:      info.Size(),
		CreatedAt: time.Now(),
		Database:  m.dataDir,
		Tables:    tables,
	}, nil
}

// Restore extracts a backup archive created by Backup() into the data
// directory, replacing its current contents.
func (m *PocketBaseManager) Restore(backupPath string) (string, error) {
	if backupPath == "" {
		return "", fmt.Errorf("backupPath is required")
	}
	if _, err := os.Stat(backupPath); err != nil {
		return "", fmt.Errorf("backup file not found: %w", err)
	}

	// Stop PocketBase if running to avoid lock conflicts.
	wasRunning := m.isRunning()
	if wasRunning {
		if _, err := m.Stop(); err != nil {
			return "", fmt.Errorf("stop pocketbase before restore: %w", err)
		}
	}

	// Remove existing data dir and recreate.
	if err := os.RemoveAll(m.dataDir); err != nil {
		return "", fmt.Errorf("remove existing data dir: %w", err)
	}
	if err := os.MkdirAll(m.dataDir, 0700); err != nil {
		return "", fmt.Errorf("recreate data dir: %w", err)
	}

	if err := pbExtractTarGz(backupPath, filepath.Dir(m.dataDir)); err != nil {
		return "", fmt.Errorf("extract backup: %w", err)
	}

	msg := fmt.Sprintf("restored from %s", backupPath)
	if wasRunning {
		if _, err := m.Start(m.mode, m.port); err != nil {
			return msg + " (warning: could not restart pocketbase: " + err.Error() + ")", nil
		}
		msg += " and restarted pocketbase"
	}
	return msg, nil
}

// AdminURL returns the PocketBase admin UI URL.
func (m *PocketBaseManager) AdminURL() (string, error) {
	return fmt.Sprintf("http://localhost:%d/_/", m.port), nil
}

// Setup returns a ready-to-use client SDK code snippet for the given
// framework.
//
//   framework — "javascript", "dart", or "react"
func (m *PocketBaseManager) Setup(framework string) (string, error) {
	appURL := fmt.Sprintf("http://localhost:%d", m.port)
	switch framework {
	case "javascript":
		return fmt.Sprintf(`// PocketBase JavaScript SDK
// npm install pocketbase

import PocketBase from 'pocketbase';

const pb = new PocketBase('%s');

// Authenticate as a regular user
const authData = await pb.collection('users').authWithPassword(
  'user@example.com',
  'yourpassword',
);

// List records
const records = await pb.collection('posts').getFullList({
  sort: '-created',
  filter: 'status = "active"',
});

// Create a record
const record = await pb.collection('posts').create({
  title: 'Hello PocketBase',
  status: 'active',
});

// Real-time subscription
pb.collection('posts').subscribe('*', (e) => {
  console.log(e.action, e.record);
});
`, appURL), nil

	case "dart":
		return fmt.Sprintf(`// PocketBase Dart SDK
// pubspec.yaml: pocketbase: ^0.18.0

import 'package:pocketbase/pocketbase.dart';

final pb = PocketBase('%s');

// Authenticate
final authData = await pb.collection('users').authWithPassword(
  'user@example.com',
  'yourpassword',
);

// List records
final records = await pb.collection('posts').getFullList(
  sort: '-created',
  filter: 'status = "active"',
);

// Create a record
final record = await pb.collection('posts').create(body: {
  'title': 'Hello PocketBase',
  'status': 'active',
});

// Real-time subscription
pb.collection('posts').subscribe('*', (e) {
  print('action: \${e.action}, record: \${e.record}');
});
`, appURL), nil

	case "react":
		return fmt.Sprintf(`// PocketBase React hooks
// npm install pocketbase

import { useEffect, useState } from 'react';
import PocketBase from 'pocketbase';

const pb = new PocketBase('%s');

// Auth hook
export function useAuth() {
  const [user, setUser] = useState(pb.authStore.model);

  useEffect(() => {
    return pb.authStore.onChange((token, model) => {
      setUser(model);
    });
  }, []);

  const login = async (email, password) => {
    await pb.collection('users').authWithPassword(email, password);
  };

  const logout = () => pb.authStore.clear();

  return { user, login, logout };
}

// Records hook with real-time updates
export function useRecords(collection, options = {}) {
  const [records, setRecords] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    pb.collection(collection).getFullList(options).then((r) => {
      setRecords(r);
      setLoading(false);
    });

    const unsub = pb.collection(collection).subscribe('*', (e) => {
      setRecords((prev) => {
        if (e.action === 'create') return [e.record, ...prev];
        if (e.action === 'update') return prev.map((r) => r.id === e.record.id ? e.record : r);
        if (e.action === 'delete') return prev.filter((r) => r.id !== e.record.id);
        return prev;
      });
    });

    return () => pb.collection(collection).unsubscribe('*');
  }, [collection]);

  return { records, loading };
}
`, appURL), nil

	default:
		return "", fmt.Errorf("unknown framework %q: want javascript|dart|react", framework)
	}
}

// Upgrade generates a migration guide and schema scaffolding for moving from
// PocketBase (SQLite) to a full Postgres setup (Drizzle or Prisma).
//
// targetDB must be "drizzle" or "prisma".
func (m *PocketBaseManager) Upgrade(targetDB string) (string, error) {
	cols, err := m.Collections()
	if err != nil {
		return "", fmt.Errorf("list collections: %w", err)
	}

	switch targetDB {
	case "drizzle":
		return m.generateDrizzleSchema(cols), nil
	case "prisma":
		return m.generatePrismaSchema(cols), nil
	default:
		return "", fmt.Errorf("unknown targetDB %q: want drizzle|prisma", targetDB)
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────────────────────────────

// pbAPI makes an authenticated HTTP call to the PocketBase API.
// body may be nil for GET/DELETE requests.
func (m *PocketBaseManager) pbAPI(method, path string, body interface{}) ([]byte, error) {
	token, err := m.getAdminToken()
	if err != nil {
		return nil, fmt.Errorf("admin auth: %w", err)
	}

	base := fmt.Sprintf("http://localhost:%d", m.port)
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, base+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pb API %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read pb API response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pb API %s %s returned %d: %s", method, path, resp.StatusCode, raw)
	}
	return raw, nil
}

// ensureAdmin creates the first superuser/admin account when PocketBase has
// just started with no existing admin. It is idempotent — if an admin already
// exists the request is ignored.
func (m *PocketBaseManager) ensureAdmin() error {
	type adminPayload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	data, err := json.Marshal(adminPayload{Email: m.adminEmail, Password: m.adminPassword})
	if err != nil {
		return err
	}

	base := fmt.Sprintf("http://localhost:%d", m.port)
	resp, err := http.Post(base+"/api/admins",
		"application/json", bytes.NewReader(data)) //nolint:noctx
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	defer resp.Body.Close()

	// 200 = created, 400 = already exists (both are acceptable).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create admin returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// getAdminToken returns a valid JWT admin token, re-authenticating when the
// cached token is missing or within 30 seconds of expiry.
func (m *PocketBaseManager) getAdminToken() (string, error) {
	m.mu.Lock()
	if m.adminToken != "" && time.Now().Before(m.tokenExpiry.Add(-30*time.Second)) {
		token := m.adminToken
		m.mu.Unlock()
		return token, nil
	}
	m.mu.Unlock()

	type authReq struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	type authResp struct {
		Token string `json:"token"`
	}

	payload, _ := json.Marshal(authReq{
		Identity: m.adminEmail,
		Password: m.adminPassword,
	})

	base := fmt.Sprintf("http://localhost:%d", m.port)
	resp, err := http.Post(base+"/api/admins/auth-with-password",
		"application/json", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("admin auth request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("admin auth returned %d: %s", resp.StatusCode, body)
	}

	var ar authResp
	if err := json.Unmarshal(body, &ar); err != nil {
		return "", fmt.Errorf("parse admin auth response: %w", err)
	}

	m.mu.Lock()
	m.adminToken = ar.Token
	// PocketBase admin tokens are valid for 1 day by default.
	m.tokenExpiry = time.Now().Add(24 * time.Hour)
	m.mu.Unlock()

	return ar.Token, nil
}

// downloadPocketBase downloads the PocketBase binary for the current
// OS/architecture from GitHub releases and returns the path to the binary.
//
// The binary is cached at ~/.yaver/pocketbase (or ~/.yaver/pocketbase.exe on
// Windows) and is only re-downloaded if the file does not already exist.
func (m *PocketBaseManager) downloadPocketBase() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	yaverBinDir := filepath.Join(home, ".yaver")
	if err := os.MkdirAll(yaverBinDir, 0700); err != nil {
		return "", fmt.Errorf("create .yaver dir: %w", err)
	}

	binName := "pocketbase"
	if runtime.GOOS == "windows" {
		binName = "pocketbase.exe"
	}
	binPath := filepath.Join(yaverBinDir, binName)

	if _, err := os.Stat(binPath); err == nil {
		// Already downloaded.
		return binPath, nil
	}

	osName := runtime.GOOS
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", arch)
	}

	// Latest stable release as of the current knowledge cutoff.
	const pbVersion = "0.23.4"
	zipName := fmt.Sprintf("pocketbase_%s_%s_%s.zip", pbVersion, osName, arch)
	downloadURL := fmt.Sprintf(
		"https://github.com/pocketbase/pocketbase/releases/download/v%s/%s",
		pbVersion, zipName,
	)

	fmt.Printf("[pb] downloading PocketBase %s for %s/%s...\n", pbVersion, osName, arch)

	resp, err := http.Get(downloadURL) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("download pocketbase: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download pocketbase: HTTP %d from %s", resp.StatusCode, downloadURL)
	}

	// Write zip to a temp file, then unzip the binary.
	tmp, err := os.CreateTemp("", "pocketbase-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write zip: %w", err)
	}
	tmp.Close()

	if err := unzipBinary(tmp.Name(), binName, binPath); err != nil {
		return "", fmt.Errorf("unzip pocketbase binary: %w", err)
	}

	if err := os.Chmod(binPath, 0755); err != nil {
		return "", fmt.Errorf("chmod pocketbase: %w", err)
	}

	fmt.Printf("[pb] pocketbase binary saved to %s\n", binPath)
	return binPath, nil
}

// isRunning returns true if PocketBase is responding on its health endpoint.
// For docker mode it additionally checks that the container is running.
func (m *PocketBaseManager) isRunning() bool {
	url := fmt.Sprintf("http://localhost:%d/api/health", m.port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ──────────────────────────────────────────────────────────────────────────
// Start / Stop helpers
// ──────────────────────────────────────────────────────────────────────────

func (m *PocketBaseManager) startStandalone() (string, error) {
	if m.isRunning() {
		return m.runningMessage(), nil
	}

	bin, err := m.downloadPocketBase()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(m.dataDir, 0700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", m.port)
	cmd := exec.Command(bin, "serve", "--http", addr, "--dir", m.dataDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start pocketbase: %w", err)
	}
	m.cmd = cmd

	// Wait for health endpoint.
	if err := m.waitForHealth(15 * time.Second); err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("pocketbase did not start in time: %w", err)
	}

	// Create admin on first run (ignore "already exists" errors).
	_ = m.ensureAdmin()

	return m.runningMessage(), nil
}

func (m *PocketBaseManager) stopStandalone() (string, error) {
	if m.cmd == nil || m.cmd.Process == nil {
		return "pocketbase is not running", nil
	}
	if err := m.cmd.Process.Kill(); err != nil {
		return "", fmt.Errorf("kill pocketbase: %w", err)
	}
	m.cmd = nil
	return fmt.Sprintf("pocketbase stopped (was listening on port %d)", m.port), nil
}

func (m *PocketBaseManager) startDocker() (string, error) {
	if m.isRunning() {
		return m.runningMessage(), nil
	}

	// Remove any stale stopped container.
	_ = exec.Command("docker", "rm", "-f", "yaver-pocketbase").Run()

	args := []string{
		"run", "-d",
		"--name", "yaver-pocketbase",
		"-p", fmt.Sprintf("%d:8090", m.port),
		"-v", "yaver-pb-data:/pb_data",
		"ghcr.io/pocketbase/pocketbase",
	}
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker run pocketbase: %w\n%s", err, out)
	}

	if err := m.waitForHealth(30 * time.Second); err != nil {
		return "", fmt.Errorf("pocketbase docker container did not become healthy: %w", err)
	}

	_ = m.ensureAdmin()
	return m.runningMessage(), nil
}

func (m *PocketBaseManager) stopDocker() (string, error) {
	out, err := exec.Command("docker", "rm", "-f", "yaver-pocketbase").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker rm pocketbase: %w\n%s", err, out)
	}
	return "yaver-pocketbase docker container removed", nil
}

// ──────────────────────────────────────────────────────────────────────────
// User management helpers
// ──────────────────────────────────────────────────────────────────────────

func (m *PocketBaseManager) listUsers() ([]PBUser, error) {
	raw, err := m.pbAPI("GET", "/api/collections/users/records?perPage=500", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Items []struct {
			ID       string `json:"id"`
			Email    string `json:"email"`
			Username string `json:"username"`
			Verified bool   `json:"verified"`
			Created  string `json:"created"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse users response: %w", err)
	}

	users := make([]PBUser, 0, len(resp.Items))
	for _, u := range resp.Items {
		users = append(users, PBUser{
			ID:       u.ID,
			Email:    u.Email,
			Username: u.Username,
			Verified: u.Verified,
			Created:  u.Created,
		})
	}
	return users, nil
}

func (m *PocketBaseManager) createUser(email, password string) (PBUser, error) {
	body := map[string]interface{}{
		"email":           email,
		"password":        password,
		"passwordConfirm": password,
	}
	raw, err := m.pbAPI("POST", "/api/collections/users/records", body)
	if err != nil {
		return PBUser{}, err
	}

	var u struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Username string `json:"username"`
		Verified bool   `json:"verified"`
		Created  string `json:"created"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return PBUser{}, fmt.Errorf("parse create-user response: %w", err)
	}
	return PBUser{
		ID:       u.ID,
		Email:    u.Email,
		Username: u.Username,
		Verified: u.Verified,
		Created:  u.Created,
	}, nil
}

func (m *PocketBaseManager) deleteUser(email string) (string, error) {
	// Find the user ID by email first.
	users, err := m.listUsers()
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if strings.EqualFold(u.Email, email) {
			_, err := m.pbAPI("DELETE", "/api/collections/users/records/"+u.ID, nil)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("deleted user %s (%s)", u.ID, email), nil
		}
	}
	return "", fmt.Errorf("user %q not found", email)
}

// ──────────────────────────────────────────────────────────────────────────
// Schema upgrade helpers
// ──────────────────────────────────────────────────────────────────────────

func (m *PocketBaseManager) generateDrizzleSchema(cols []PBCollection) string {
	var sb strings.Builder
	sb.WriteString("// Drizzle ORM schema generated from PocketBase collections\n")
	sb.WriteString("// npm install drizzle-orm @libsql/client\n\n")
	sb.WriteString("import { sqliteTable, text, integer, real } from 'drizzle-orm/sqlite-core';\n\n")

	for _, col := range cols {
		sb.WriteString(fmt.Sprintf("export const %s = sqliteTable('%s', {\n", col.Name, col.Name))
		sb.WriteString("  id:      text('id').primaryKey(),\n")
		sb.WriteString("  created: text('created').notNull(),\n")
		sb.WriteString("  updated: text('updated').notNull(),\n")
		for _, f := range col.Schema {
			drizzleType := pbFieldToDrizzleType(f.Type)
			nullable := ""
			if !f.Required {
				nullable = ""
			}
			sb.WriteString(fmt.Sprintf("  %-20s %s('%s')%s,\n",
				f.Name+":", drizzleType, f.Name, nullable))
		}
		sb.WriteString("});\n\n")
	}

	sb.WriteString(`// Migration steps:
// 1. npm install drizzle-orm drizzle-kit @libsql/client pg
// 2. Point DATABASE_URL to your Postgres instance
// 3. npx drizzle-kit generate
// 4. npx drizzle-kit migrate
// 5. Export data from PocketBase: GET /api/collections/{name}/records
// 6. Import via drizzle insert statements
`)
	return sb.String()
}

func (m *PocketBaseManager) generatePrismaSchema(cols []PBCollection) string {
	var sb strings.Builder
	sb.WriteString("// Prisma schema generated from PocketBase collections\n")
	sb.WriteString("// npm install prisma @prisma/client\n\n")
	sb.WriteString(`datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}

generator client {
  provider = "prisma-client-js"
}

`)

	for _, col := range cols {
		// Capitalise first letter for Prisma model name.
		modelName := strings.ToUpper(col.Name[:1]) + col.Name[1:]
		sb.WriteString(fmt.Sprintf("model %s {\n", modelName))
		sb.WriteString("  id      String   @id @default(cuid())\n")
		sb.WriteString("  created DateTime @default(now())\n")
		sb.WriteString("  updated DateTime @updatedAt\n")
		for _, f := range col.Schema {
			prismaType := pbFieldToPrismaType(f.Type)
			optional := "?"
			if f.Required {
				optional = ""
			}
			sb.WriteString(fmt.Sprintf("  %-20s %s%s\n", f.Name, prismaType, optional))
		}
		sb.WriteString(fmt.Sprintf("  @@map(\"%s\")\n", col.Name))
		sb.WriteString("}\n\n")
	}

	sb.WriteString(`// Migration steps:
// 1. Set DATABASE_URL in .env
// 2. npx prisma migrate dev --name init
// 3. Export data from PocketBase: GET /api/collections/{name}/records
// 4. Seed via prisma.$executeRaw or prisma.{model}.createMany
`)
	return sb.String()
}

// ──────────────────────────────────────────────────────────────────────────
// Utilities
// ──────────────────────────────────────────────────────────────────────────

// pbBinaryPath returns the path to the pocketbase binary, downloading it if
// necessary.
func (m *PocketBaseManager) pbBinaryPath() (string, error) {
	// Check PATH first.
	if path, err := exec.LookPath("pocketbase"); err == nil {
		return path, nil
	}
	return m.downloadPocketBase()
}

func (m *PocketBaseManager) runningMessage() string {
	return fmt.Sprintf(
		"PocketBase running at http://localhost:%d  (admin: http://localhost:%d/_/)",
		m.port, m.port,
	)
}

// waitForHealth polls the PocketBase health endpoint until it responds 200 or
// timeout is reached.
func (m *PocketBaseManager) waitForHealth(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	url := fmt.Sprintf("http://localhost:%d/api/health", m.port)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("health check timed out after %s", timeout)
}

// pbAddDirToTar recursively writes all files under srcDir into tw, rooted at
// prefix.
func pbAddDirToTar(tw *tar.Writer, srcDir, prefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.Join(prefix, rel)

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// pbExtractTarGz extracts a .tar.gz archive into destDir.
func pbExtractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)
		// Guard against path traversal.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)) {
			return fmt.Errorf("illegal path in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

// unzipBinary extracts a single named file from a ZIP archive.
func unzipBinary(zipPath, binaryName, destPath string) error {
	// Use the system unzip command to avoid importing archive/zip.
	out, err := exec.Command("unzip", "-o", zipPath, binaryName, "-d",
		filepath.Dir(destPath)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("unzip: %w\n%s", err, out)
	}
	extracted := filepath.Join(filepath.Dir(destPath), binaryName)
	if extracted != destPath {
		return os.Rename(extracted, destPath)
	}
	return nil
}

// urlEncode is a minimal percent-encoder for query parameter values.
func urlEncode(s string) string {
	var sb strings.Builder
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			sb.WriteRune(c)
		default:
			sb.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return sb.String()
}

// pbFieldToDrizzleType maps a PocketBase field type to a Drizzle column
// function name.
func pbFieldToDrizzleType(pbType string) string {
	switch pbType {
	case "number":
		return "real"
	case "bool":
		return "integer" // 0/1
	case "date":
		return "text"
	case "json":
		return "text"
	default:
		// text, email, url, file, relation, select → all text
		return "text"
	}
}

// pbFieldToPrismaType maps a PocketBase field type to a Prisma scalar type.
func pbFieldToPrismaType(pbType string) string {
	switch pbType {
	case "number":
		return "Float"
	case "bool":
		return "Boolean"
	case "date":
		return "DateTime"
	case "json":
		return "Json"
	default:
		return "String"
	}
}

// randomHex is provided by analytics_selfhost.go (package-level).
