package main

// db_lifecycle.go — Database lifecycle manager for Yaver workspaces.
//
// Handles migrations, schema generation, seeding, backups, and database GUI
// (studio) across common ORMs and databases:
//
//   ORMs:     Drizzle, Prisma, Goose, Alembic
//   Databases: PostgreSQL, SQLite, MySQL
//
// All operations are run inside the project's working directory so that
// framework config files (drizzle.config.ts, prisma/schema.prisma, etc.)
// and .env files are resolved correctly.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DBMigrationStatus summarises the current migration state of a project
// database without running any mutations.
type DBMigrationStatus struct {
	Pending       int       `json:"pending"`
	Applied       int       `json:"applied"`
	LastMigration string    `json:"lastMigration"`
	LastAppliedAt time.Time `json:"lastAppliedAt"`
	// Engine is the detected ORM: "drizzle", "prisma", "goose", "alembic", "unknown".
	Engine string `json:"engine"`
}

// DBBackupInfo describes a single database backup snapshot.
type DBBackupInfo struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	Database  string    `json:"database"`
	Tables    int       `json:"tables"`
}

// DBLifecycleManager coordinates database operations for a single project
// workspace. All methods are safe for concurrent use.
type DBLifecycleManager struct {
	mu        sync.Mutex
	workDir   string
	backupDir string // defaults to ~/.yaver/backups/
}

// NewDBLifecycleManager creates a manager for the given project directory.
// The backup directory defaults to ~/.yaver/backups/.
func NewDBLifecycleManager(workDir string) *DBLifecycleManager {
	backupDir := ""
	if cfgDir, err := ConfigDir(); err == nil {
		backupDir = filepath.Join(cfgDir, "backups")
	}
	return &DBLifecycleManager{
		workDir:   workDir,
		backupDir: backupDir,
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Public API
// ──────────────────────────────────────────────────────────────────────────

// Migrate runs all pending migrations against the target environment.
// target should be "local" or "production". The appropriate ORM command is
// auto-detected from the project working directory.
//
// Returns a human-readable summary of what was applied, e.g.:
//
//	"drizzle: 3 migrations applied"
func (m *DBLifecycleManager) Migrate(target string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	orm := detectORM(m.workDir)
	switch orm {
	case "drizzle":
		out, err := m.runInWorkDir("npx", "drizzle-kit", "migrate")
		if err != nil {
			return "", fmt.Errorf("drizzle migrate: %w\n%s", err, out)
		}
		return fmt.Sprintf("drizzle: migrations applied\n%s", strings.TrimSpace(out)), nil

	case "prisma":
		args := []string{"prisma", "migrate", "deploy"}
		if target == "local" {
			// `migrate deploy` works for both but some teams use `migrate dev` locally.
			// We prefer deploy for safety across both targets.
		}
		out, err := m.runInWorkDir("npx", args...)
		if err != nil {
			return "", fmt.Errorf("prisma migrate deploy: %w\n%s", err, out)
		}
		return fmt.Sprintf("prisma: migrations deployed\n%s", strings.TrimSpace(out)), nil

	case "goose":
		dbURL := getDBURL(m.workDir)
		db, driver := parseGooseDriver(dbURL)
		out, err := m.runInWorkDir("goose", "-dir", "migrations", driver, db, "up")
		if err != nil {
			return "", fmt.Errorf("goose up: %w\n%s", err, out)
		}
		return fmt.Sprintf("goose: migrations applied\n%s", strings.TrimSpace(out)), nil

	case "alembic":
		out, err := m.runInWorkDir("alembic", "upgrade", "head")
		if err != nil {
			return "", fmt.Errorf("alembic upgrade head: %w\n%s", err, out)
		}
		return fmt.Sprintf("alembic: upgraded to head\n%s", strings.TrimSpace(out)), nil

	default:
		return "", fmt.Errorf("no supported ORM detected in %s (looked for drizzle.config.ts, prisma/schema.prisma, goose annotations, alembic.ini)", m.workDir)
	}
}

// Generate creates a new migration file from the current schema state.
// name is used as the migration label (e.g. "add_users_table").
//
// Returns the path of the generated migration file.
func (m *DBLifecycleManager) Generate(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "" {
		return "", fmt.Errorf("migration name must not be empty")
	}

	orm := detectORM(m.workDir)
	switch orm {
	case "drizzle":
		out, err := m.runInWorkDir("npx", "drizzle-kit", "generate")
		if err != nil {
			return "", fmt.Errorf("drizzle-kit generate: %w\n%s", err, out)
		}
		// Drizzle prints the generated file path to stdout.
		generated := extractFilePath(out)
		if generated == "" {
			generated = filepath.Join(m.workDir, "drizzle", name+".sql")
		}
		return generated, nil

	case "prisma":
		out, err := m.runInWorkDir("npx", "prisma", "migrate", "dev", "--name", name)
		if err != nil {
			return "", fmt.Errorf("prisma migrate dev: %w\n%s", err, out)
		}
		generated := extractFilePath(out)
		if generated == "" {
			generated = filepath.Join(m.workDir, "prisma", "migrations", name)
		}
		return generated, nil

	case "goose":
		// Goose does not generate from schema — create a blank timestamped SQL file.
		migDir := filepath.Join(m.workDir, "migrations")
		if err := os.MkdirAll(migDir, 0755); err != nil {
			return "", fmt.Errorf("create migrations dir: %w", err)
		}
		stamp := time.Now().UTC().Format("20060102150405")
		safeName := strings.ReplaceAll(name, " ", "_")
		filename := fmt.Sprintf("%s_%s.sql", stamp, safeName)
		filePath := filepath.Join(migDir, filename)
		content := fmt.Sprintf("-- +goose Up\n-- SQL in section 'Up' is executed when this migration is applied.\n\n-- +goose Down\n-- SQL section 'Down' is executed when this migration is rolled back.\n")
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("write migration file: %w", err)
		}
		return filePath, nil

	case "alembic":
		out, err := m.runInWorkDir("alembic", "revision", "--autogenerate", "-m", name)
		if err != nil {
			return "", fmt.Errorf("alembic revision: %w\n%s", err, out)
		}
		generated := extractFilePath(out)
		if generated == "" {
			generated = filepath.Join(m.workDir, "alembic", "versions", name+".py")
		}
		return generated, nil

	default:
		return "", fmt.Errorf("no supported ORM detected in %s", m.workDir)
	}
}

// Push applies the schema directly to the database, bypassing migration files.
// This is intended for development environments only. The method returns a
// warning message alongside the output to make the side-effect explicit.
func (m *DBLifecycleManager) Push() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	const warning = "WARNING: push skips migration files and cannot be safely applied to production databases.\n"

	orm := detectORM(m.workDir)
	switch orm {
	case "drizzle":
		out, err := m.runInWorkDir("npx", "drizzle-kit", "push")
		if err != nil {
			return "", fmt.Errorf("drizzle-kit push: %w\n%s", err, out)
		}
		return warning + strings.TrimSpace(out), nil

	case "prisma":
		out, err := m.runInWorkDir("npx", "prisma", "db", "push")
		if err != nil {
			return "", fmt.Errorf("prisma db push: %w\n%s", err, out)
		}
		return warning + strings.TrimSpace(out), nil

	default:
		return "", fmt.Errorf("push is only supported for Drizzle and Prisma (detected ORM: %s)", orm)
	}
}

// Seed runs the project's seed script. The following seed files are searched
// in order:
//
//  1. seed.ts / seed.js (root)
//  2. prisma/seed.ts
//  3. db/seed.go
//
// Each seed file is executed with the appropriate runner (tsx/node/go run).
func (m *DBLifecycleManager) Seed() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	type candidate struct {
		rel     string
		runner  string
		runArgs []string
	}
	candidates := []candidate{
		{"seed.ts", "npx", []string{"tsx", "seed.ts"}},
		{"seed.js", "node", []string{"seed.js"}},
		{"prisma/seed.ts", "npx", []string{"tsx", "prisma/seed.ts"}},
		{"prisma/seed.js", "node", []string{"prisma/seed.js"}},
		{"db/seed.go", "go", []string{"run", "db/seed.go"}},
	}

	for _, c := range candidates {
		fullPath := filepath.Join(m.workDir, c.rel)
		if _, err := os.Stat(fullPath); err == nil {
			args := append([]string{}, c.runArgs...)
			out, err := m.runInWorkDir(c.runner, args...)
			if err != nil {
				return "", fmt.Errorf("seed (%s): %w\n%s", c.rel, err, out)
			}
			return fmt.Sprintf("seeded via %s %s\n%s", c.runner, c.rel, strings.TrimSpace(out)), nil
		}
	}

	return "", fmt.Errorf("no seed file found — tried: seed.ts, seed.js, prisma/seed.ts, prisma/seed.js, db/seed.go")
}

// Reset drops all tables and re-runs migrations + seed. This is a
// destructive operation and requires the caller to pass force=true.
// Returns an error with an explanatory message when force is false.
func (m *DBLifecycleManager) Reset(force bool) (string, error) {
	if !force {
		return "", fmt.Errorf("Reset is destructive: it drops all tables and re-seeds the database. Call Reset(true) to confirm")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	orm := detectORM(m.workDir)
	var summary strings.Builder

	switch orm {
	case "drizzle":
		// drizzle-kit drop removes all schema objects, then re-apply migrations and seed.
		out, err := m.runInWorkDir("npx", "drizzle-kit", "drop")
		if err != nil {
			return "", fmt.Errorf("drizzle-kit drop: %w\n%s", err, out)
		}
		summary.WriteString("dropped schema\n")

		out, err = m.runInWorkDir("npx", "drizzle-kit", "migrate")
		if err != nil {
			return "", fmt.Errorf("drizzle-kit migrate (post-drop): %w\n%s", err, out)
		}
		summary.WriteString("migrations applied\n")

	case "prisma":
		// prisma migrate reset drops, re-applies all migrations, and seeds automatically.
		out, err := m.runInWorkDir("npx", "prisma", "migrate", "reset", "--force", "--skip-seed")
		if err != nil {
			return "", fmt.Errorf("prisma migrate reset: %w\n%s", err, out)
		}
		summary.WriteString("prisma: reset complete\n")
		summary.WriteString(strings.TrimSpace(out))
		summary.WriteString("\n")

	case "goose":
		dbURL := getDBURL(m.workDir)
		db, driver := parseGooseDriver(dbURL)
		out, err := m.runInWorkDir("goose", "-dir", "migrations", driver, db, "reset")
		if err != nil {
			return "", fmt.Errorf("goose reset: %w\n%s", err, out)
		}
		summary.WriteString("goose: all migrations rolled back\n")
		out, err = m.runInWorkDir("goose", "-dir", "migrations", driver, db, "up")
		if err != nil {
			return "", fmt.Errorf("goose up (post-reset): %w\n%s", err, out)
		}
		summary.WriteString("goose: migrations re-applied\n")

	case "alembic":
		out, err := m.runInWorkDir("alembic", "downgrade", "base")
		if err != nil {
			return "", fmt.Errorf("alembic downgrade base: %w\n%s", err, out)
		}
		summary.WriteString("alembic: downgraded to base\n")
		out, err = m.runInWorkDir("alembic", "upgrade", "head")
		if err != nil {
			return "", fmt.Errorf("alembic upgrade head (post-reset): %w\n%s", err, out)
		}
		summary.WriteString("alembic: upgraded to head\n")

	default:
		return "", fmt.Errorf("no supported ORM detected in %s", m.workDir)
	}

	// Attempt seed regardless of ORM (best-effort; ignore missing seed file).
	if seedOut, err := m.seedLocked(); err == nil {
		summary.WriteString("seeded: " + seedOut + "\n")
	}

	return strings.TrimSpace(summary.String()), nil
}

// Studio launches the ORM's built-in database GUI on the given port (0 = use
// the ORM default). Returns the URL to open in a browser.
func (m *DBLifecycleManager) Studio(port int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	orm := detectORM(m.workDir)
	switch orm {
	case "drizzle":
		if port == 0 {
			port = 4983
		}
		go func() {
			_, _ = m.runInWorkDir("npx", "drizzle-kit", "studio", "--port", strconv.Itoa(port))
		}()
		return fmt.Sprintf("http://localhost:%d", port), nil

	case "prisma":
		if port == 0 {
			port = 5555
		}
		go func() {
			_, _ = m.runInWorkDir("npx", "prisma", "studio", "--port", strconv.Itoa(port))
		}()
		return fmt.Sprintf("http://localhost:%d", port), nil

	default:
		return "", fmt.Errorf("studio is only supported for Drizzle (port 4983) and Prisma (port 5555); detected ORM: %s", orm)
	}
}

// Backup creates a snapshot of the database indicated by dbURL. The backup is
// written to the manager's backupDir with a timestamp-based filename.
//
// Supported schemes: postgres / postgresql, sqlite / sqlite3, mysql.
func (m *DBLifecycleManager) Backup(dbURL string) (*DBBackupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.backupDir, 0700); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	dbType := dbTypeFromURL(dbURL)
	stamp := time.Now().UTC().Format("20060102-150405")
	backupFile := filepath.Join(m.backupDir, fmt.Sprintf("db-%s-%s.dump", dbType, stamp))

	switch dbType {
	case "postgres":
		cmd := exec.Command("pg_dump", "--format=custom", "--file="+backupFile, dbURL)
		cmd.Env = os.Environ()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("pg_dump: %w — %s", err, stderr.String())
		}

	case "sqlite":
		srcPath := sqlitePathFromURL(dbURL)
		if srcPath == "" {
			return nil, fmt.Errorf("cannot determine SQLite file path from URL %q", dbURL)
		}
		backupFile = filepath.Join(m.backupDir, fmt.Sprintf("db-sqlite-%s.db", stamp))
		if err := dbCopyFile(srcPath, backupFile); err != nil {
			return nil, fmt.Errorf("sqlite backup copy: %w", err)
		}

	case "mysql":
		// Parse mysql://user:pass@host:port/dbname
		u, err := url.Parse(dbURL)
		if err != nil {
			return nil, fmt.Errorf("parse mysql URL: %w", err)
		}
		args := []string{}
		if u.User != nil {
			args = append(args, "-u"+u.User.Username())
			if pw, ok := u.User.Password(); ok {
				args = append(args, "-p"+pw)
			}
		}
		if u.Host != "" {
			host := u.Hostname()
			port := u.Port()
			args = append(args, "-h"+host)
			if port != "" {
				args = append(args, "-P"+port)
			}
		}
		dbName := strings.TrimPrefix(u.Path, "/")
		args = append(args, dbName, "--result-file="+backupFile)
		cmd := exec.Command("mysqldump", args...)
		cmd.Env = os.Environ()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("mysqldump: %w — %s", err, stderr.String())
		}

	default:
		return nil, fmt.Errorf("unsupported database type %q — supported: postgres, sqlite, mysql", dbType)
	}

	info, err := os.Stat(backupFile)
	if err != nil {
		return nil, fmt.Errorf("stat backup file: %w", err)
	}

	return &DBBackupInfo{
		Path:      backupFile,
		Size:      info.Size(),
		CreatedAt: time.Now().UTC(),
		Database:  dbType,
	}, nil
}

// Restore restores a database from a backup file produced by Backup.
// dbURL is the target database connection string.
func (m *DBLifecycleManager) Restore(backupPath, dbURL string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(backupPath); err != nil {
		return "", fmt.Errorf("backup file not found: %w", err)
	}

	dbType := dbTypeFromURL(dbURL)
	switch dbType {
	case "postgres":
		// Prefer pg_restore for custom-format dumps; fall back to psql for plain SQL.
		cmd := exec.Command("pg_restore", "--clean", "--if-exists", "--dbname="+dbURL, backupPath)
		cmd.Env = os.Environ()
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			// pg_restore exits non-zero on warnings too; check whether output contains "error"
			output := buf.String()
			if isFatalPgOutput(output) {
				return "", fmt.Errorf("pg_restore: %w\n%s", err, output)
			}
		}
		return fmt.Sprintf("postgres: restored from %s", backupPath), nil

	case "sqlite":
		destPath := sqlitePathFromURL(dbURL)
		if destPath == "" {
			return "", fmt.Errorf("cannot determine SQLite file path from URL %q", dbURL)
		}
		if err := dbCopyFile(backupPath, destPath); err != nil {
			return "", fmt.Errorf("sqlite restore copy: %w", err)
		}
		return fmt.Sprintf("sqlite: restored from %s to %s", backupPath, destPath), nil

	case "mysql":
		u, err := url.Parse(dbURL)
		if err != nil {
			return "", fmt.Errorf("parse mysql URL: %w", err)
		}
		args := []string{}
		if u.User != nil {
			args = append(args, "-u"+u.User.Username())
			if pw, ok := u.User.Password(); ok {
				args = append(args, "-p"+pw)
			}
		}
		if u.Host != "" {
			host := u.Hostname()
			port := u.Port()
			args = append(args, "-h"+host)
			if port != "" {
				args = append(args, "-P"+port)
			}
		}
		dbName := strings.TrimPrefix(u.Path, "/")
		args = append(args, dbName)
		// mysql < dump.sql
		f, err := os.Open(backupPath)
		if err != nil {
			return "", fmt.Errorf("open backup file: %w", err)
		}
		defer f.Close()
		cmd := exec.Command("mysql", args...)
		cmd.Stdin = f
		cmd.Env = os.Environ()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("mysql restore: %w — %s", err, stderr.String())
		}
		return fmt.Sprintf("mysql: restored from %s", backupPath), nil

	default:
		return "", fmt.Errorf("unsupported database type %q — supported: postgres, sqlite, mysql", dbType)
	}
}

// ListBackups returns all backup snapshots in the manager's backupDir, sorted
// newest-first.
func (m *DBLifecycleManager) ListBackups() ([]DBBackupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.backupDir, 0700); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var backups []DBBackupInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "db-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(m.backupDir, name)
		// Infer database type from filename: db-<type>-<stamp>.<ext>
		dbType := inferDBTypeFromFilename(name)
		backups = append(backups, DBBackupInfo{
			Path:      fullPath,
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC(),
			Database:  dbType,
		})
	}

	// Newest-first.
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})

	return backups, nil
}

// Status inspects the project's migration state without applying any changes.
func (m *DBLifecycleManager) Status() (*DBMigrationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	orm := detectORM(m.workDir)
	status := &DBMigrationStatus{Engine: orm}

	switch orm {
	case "drizzle":
		out, err := m.runInWorkDir("npx", "drizzle-kit", "status")
		if err != nil {
			// drizzle-kit status exits non-zero when pending migrations exist.
			_ = out
		}
		status.Pending, status.Applied, status.LastMigration = parseDrizzleStatus(out)

	case "prisma":
		out, err := m.runInWorkDir("npx", "prisma", "migrate", "status")
		if err != nil {
			_ = out
		}
		status.Pending, status.Applied, status.LastMigration = parsePrismaStatus(out)

	case "goose":
		dbURL := getDBURL(m.workDir)
		db, driver := parseGooseDriver(dbURL)
		out, err := m.runInWorkDir("goose", "-dir", "migrations", driver, db, "status")
		if err != nil {
			_ = out
		}
		status.Pending, status.Applied, status.LastMigration, status.LastAppliedAt = parseGooseStatus(out)

	case "alembic":
		out, err := m.runInWorkDir("alembic", "current")
		if err != nil {
			_ = out
		}
		status.LastMigration = parseAlembicCurrent(out)
		headOut, _ := m.runInWorkDir("alembic", "heads")
		status.Pending = countAlembicPending(out, headOut)

	default:
		return status, fmt.Errorf("no supported ORM detected in %s", m.workDir)
	}

	return status, nil
}

// ──────────────────────────────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────────────────────────────

// detectORM inspects the working directory for ORM configuration files.
// Returns "drizzle", "prisma", "goose", "alembic", or "unknown".
func detectORM(workDir string) string {
	checks := []struct {
		orm   string
		paths []string
	}{
		{"drizzle", []string{"drizzle.config.ts", "drizzle.config.js", "drizzle.config.mts"}},
		{"prisma", []string{filepath.Join("prisma", "schema.prisma")}},
		{"alembic", []string{"alembic.ini"}},
	}
	for _, c := range checks {
		for _, p := range c.paths {
			if dbFileExists(filepath.Join(workDir, p)) {
				return c.orm
			}
		}
	}
	// Goose detection: look for .sql files in migrations/ containing a goose annotation.
	if hasGooseAnnotations(workDir) {
		return "goose"
	}
	return "unknown"
}

// detectDatabase infers the database engine from project config files and .env.
// Returns "postgres", "sqlite", "mysql", or "unknown".
func detectDatabase(workDir string) string {
	url := getDBURL(workDir)
	if url != "" {
		return dbTypeFromURL(url)
	}
	// Fall back to inspecting Prisma schema for the provider field.
	schemaPath := filepath.Join(workDir, "prisma", "schema.prisma")
	if data, err := os.ReadFile(schemaPath); err == nil {
		content := strings.ToLower(string(data))
		switch {
		case strings.Contains(content, `provider = "postgresql"`), strings.Contains(content, `provider = "postgres"`):
			return "postgres"
		case strings.Contains(content, `provider = "sqlite"`):
			return "sqlite"
		case strings.Contains(content, `provider = "mysql"`):
			return "mysql"
		}
	}
	return "unknown"
}

// getDBURL reads DATABASE_URL from .env.local or .env in workDir.
// Returns an empty string if not found.
func getDBURL(workDir string) string {
	for _, envFile := range []string{".env.local", ".env", ".env.development"} {
		if val := readEnvVar(filepath.Join(workDir, envFile), "DATABASE_URL"); val != "" {
			return val
		}
	}
	// Also check the process environment so container-injected vars work.
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return ""
}

// runInWorkDir executes a command inside m.workDir and captures its combined
// stdout+stderr output. Returns the output and any non-zero exit error.
func (m *DBLifecycleManager) runInWorkDir(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = m.workDir
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// seedLocked is the internal seed implementation that assumes the caller holds
// m.mu. Used by Reset to avoid a double-lock.
func (m *DBLifecycleManager) seedLocked() (string, error) {
	type candidate struct {
		rel     string
		runner  string
		runArgs []string
	}
	candidates := []candidate{
		{"seed.ts", "npx", []string{"tsx", "seed.ts"}},
		{"seed.js", "node", []string{"seed.js"}},
		{"prisma/seed.ts", "npx", []string{"tsx", "prisma/seed.ts"}},
		{"prisma/seed.js", "node", []string{"prisma/seed.js"}},
		{"db/seed.go", "go", []string{"run", "db/seed.go"}},
	}
	for _, c := range candidates {
		if dbFileExists(filepath.Join(m.workDir, c.rel)) {
			args := append([]string{}, c.runArgs...)
			out, err := m.runInWorkDir(c.runner, args...)
			if err != nil {
				return "", fmt.Errorf("seed (%s): %w\n%s", c.rel, err, out)
			}
			return strings.TrimSpace(out), nil
		}
	}
	return "", fmt.Errorf("no seed file found")
}

// ──────────────────────────────────────────────────────────────────────────
// Parsing helpers
// ──────────────────────────────────────────────────────────────────────────

// parseDrizzleStatus extracts pending/applied counts and the last migration
// name from drizzle-kit status output.
func parseDrizzleStatus(out string) (pending, applied int, last string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "pending"):
			fmt.Sscanf(line, "%*s %d", &pending)
		case strings.HasPrefix(lower, "applied"):
			fmt.Sscanf(line, "%*s %d", &applied)
		case strings.Contains(lower, "migration"):
			// "Last migration: 0001_initial.sql"
			if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
				last = strings.TrimSpace(parts[1])
			}
		}
	}
	return
}

// parsePrismaStatus parses the output of `prisma migrate status`.
func parsePrismaStatus(out string) (pending, applied int, last string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "migration not yet applied") || strings.Contains(line, "not applied") {
			pending++
		}
		if strings.Contains(line, "Applied") || strings.HasPrefix(line, "[√]") || strings.HasPrefix(line, "[✓]") {
			applied++
			// Capture the last applied migration name (usually the line content).
			name := strings.TrimPrefix(line, "[√]")
			name = strings.TrimPrefix(name, "[✓]")
			name = strings.TrimSpace(name)
			if name != "" && !strings.EqualFold(name, "applied") {
				last = name
			}
		}
	}
	return
}

// parseGooseStatus parses `goose status` tabular output.
func parseGooseStatus(out string) (pending, applied int, last string, lastAt time.Time) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Applied") || strings.HasPrefix(line, "Pending") {
			continue
		}
		if strings.Contains(line, "Pending") {
			pending++
		} else if strings.Contains(line, "Applied") {
			applied++
			// Goose format: "  2024/01/01 12:00:00  00001_init.sql       Applied"
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				last = fields[len(fields)-2]
				// Try to parse the timestamp from the first two fields.
				ts := fields[0] + " " + fields[1]
				if t, err := time.Parse("2006/01/02 15:04:05", ts); err == nil {
					lastAt = t
				}
			}
		}
	}
	return
}

// parseAlembicCurrent extracts the current revision from `alembic current`.
func parseAlembicCurrent(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "INFO") {
			continue
		}
		// First non-INFO line is the current revision.
		return line
	}
	return ""
}

// countAlembicPending estimates how many migrations are pending by comparing
// current revision output with heads output. A very rough heuristic — Alembic
// does not expose a structured pending count without running upgrade --sql.
func countAlembicPending(currentOut, headsOut string) int {
	current := parseAlembicCurrent(currentOut)
	head := parseAlembicCurrent(headsOut)
	if current == "" || head == "" {
		return 0
	}
	if strings.Contains(current, head) || strings.Contains(head, current) {
		return 0
	}
	// We can't easily count without revision graph traversal; signal ≥1 pending.
	return 1
}

// extractFilePath scans command output for a file path (starts with "/", "./",
// or a known directory prefix). Returns the first match.
func extractFilePath(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"/", "./", "migrations/", "prisma/migrations/", "drizzle/", "alembic/versions/"} {
			if strings.HasPrefix(line, prefix) && !strings.Contains(line, " ") {
				return line
			}
		}
	}
	return ""
}

// parseGooseDriver extracts the Goose driver name and DSN from a DATABASE_URL.
// Returns ("", "") on parse failure.
func parseGooseDriver(dbURL string) (dsn, driver string) {
	if dbURL == "" {
		return "sqlite3:./dev.db", "sqlite3"
	}
	switch dbTypeFromURL(dbURL) {
	case "postgres":
		return dbURL, "postgres"
	case "sqlite":
		path := sqlitePathFromURL(dbURL)
		if path == "" {
			path = "dev.db"
		}
		return path, "sqlite3"
	case "mysql":
		// Goose expects user:pass@tcp(host:port)/dbname
		u, err := url.Parse(dbURL)
		if err != nil {
			return dbURL, "mysql"
		}
		host := u.Host
		if !strings.Contains(host, ":") {
			host += ":3306"
		}
		dsn = fmt.Sprintf("%s@tcp(%s)%s", u.User.String(), host, u.Path)
		return dsn, "mysql"
	default:
		return dbURL, "postgres"
	}
}

// dbTypeFromURL infers the database engine from a connection URL scheme.
func dbTypeFromURL(dbURL string) string {
	lower := strings.ToLower(dbURL)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return "postgres"
	case strings.HasPrefix(lower, "sqlite://"), strings.HasPrefix(lower, "sqlite3://"),
		strings.HasSuffix(lower, ".db"), strings.HasSuffix(lower, ".sqlite"),
		strings.HasSuffix(lower, ".sqlite3"):
		return "sqlite"
	case strings.HasPrefix(lower, "mysql://"):
		return "mysql"
	default:
		return "unknown"
	}
}

// sqlitePathFromURL extracts the file path from a sqlite:// or sqlite3:// URL,
// or from a bare file path ending in .db/.sqlite.
func sqlitePathFromURL(dbURL string) string {
	for _, prefix := range []string{"sqlite3://", "sqlite://"} {
		if strings.HasPrefix(strings.ToLower(dbURL), prefix) {
			return dbURL[len(prefix):]
		}
	}
	// Bare file path.
	lower := strings.ToLower(dbURL)
	if strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".sqlite3") {
		return dbURL
	}
	return ""
}

// inferDBTypeFromFilename guesses the database type from a backup filename.
// Filename convention: db-<type>-<stamp>.<ext>
func inferDBTypeFromFilename(name string) string {
	// Strip "db-" prefix
	name = strings.TrimPrefix(name, "db-")
	for _, t := range []string{"postgres", "sqlite", "mysql"} {
		if strings.HasPrefix(name, t) {
			return t
		}
	}
	return "unknown"
}

// hasGooseAnnotations returns true if any .sql file in migrations/ contains
// a "-- +goose Up" or "-- +goose Down" annotation.
func hasGooseAnnotations(workDir string) bool {
	migDir := filepath.Join(workDir, "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		f, err := os.Open(filepath.Join(migDir, e.Name()))
		if err != nil {
			continue
		}
		found := containsGooseAnnotation(f)
		f.Close()
		if found {
			return true
		}
	}
	return false
}

// containsGooseAnnotation scans r for a "+goose" annotation marker.
func containsGooseAnnotation(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "+goose") {
			return true
		}
	}
	return false
}

// isFatalPgOutput returns true if pg_restore output contains a hard error
// (as opposed to non-fatal warnings that cause a non-zero exit code).
func isFatalPgOutput(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "pg_restore: error:") {
			return true
		}
	}
	return false
}

// readEnvVar parses a .env file and returns the value for the given key.
// Returns "" if the file does not exist or the key is not present.
func readEnvVar(filePath, key string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == key {
			val := strings.TrimSpace(parts[1])
			// Strip surrounding quotes.
			if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
				val = val[1 : len(val)-1]
			}
			return val
		}
	}
	return ""
}

// dbFileExists reports whether the named file exists and is accessible.
func dbFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dbCopyFile copies src to dst, creating dst if it does not exist and
// truncating it if it does.
func dbCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
