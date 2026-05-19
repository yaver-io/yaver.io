package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// sqlAdapter covers Postgres, Supabase (Postgres), and SQLite projects.
type sqlAdapter struct {
	kind   BackendKind
	driver string // "postgres" or "sqlite"
	dsn    string
	db     *sql.DB
}

func newSQLAdapter(dir string, cfg *YaverProjectConfig, kind BackendKind) (*sqlAdapter, error) {
	driver, dsn, err := resolveSQLDSN(dir, cfg, kind)
	if err != nil {
		return nil, err
	}
	return &sqlAdapter{kind: kind, driver: driver, dsn: dsn}, nil
}

func resolveSQLDSN(dir string, cfg *YaverProjectConfig, kind BackendKind) (driver, dsn string, err error) {
	if cfg.DB != "" {
		return sqlDriverFor(cfg.DB), cfg.DB, nil
	}
	// Check env vars
	for _, key := range []string{"DATABASE_URL", "POSTGRES_URL", "SUPABASE_DB_URL"} {
		if v := os.Getenv(key); v != "" {
			return sqlDriverFor(v), v, nil
		}
	}
	// Read project .env files
	for _, name := range []string{".env.local", ".env"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || strings.HasPrefix(k, "#") {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if k == "DATABASE_URL" || k == "POSTGRES_URL" || k == "SUPABASE_DB_URL" {
				return sqlDriverFor(v), v, nil
			}
		}
	}
	// Defaults
	switch kind {
	case BackendSupabase:
		return "postgres", "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable", nil
	case BackendPostgres:
		return "postgres", "postgres://postgres:dev@localhost:5432/myapp?sslmode=disable", nil
	case BackendSQLite:
		return "sqlite", filepath.Join(dir, "local.db"), nil
	}
	return "", "", fmt.Errorf("no DSN for %s; set DATABASE_URL or add `db:` to .yaver/config.yaml", kind)
}

func sqlDriverFor(dsn string) string {
	if strings.HasPrefix(dsn, "file:") || strings.HasSuffix(dsn, ".db") || strings.HasSuffix(dsn, ".sqlite") || strings.HasSuffix(dsn, ".sqlite3") {
		return "sqlite"
	}
	return "postgres"
}

func (a *sqlAdapter) open() error {
	if a.db != nil {
		return nil
	}
	db, err := sql.Open(a.driver, a.dsn)
	if err != nil {
		return err
	}
	a.db = db
	return nil
}

func (a *sqlAdapter) Kind() BackendKind { return a.kind }

func (a *sqlAdapter) Status() BackendStatus {
	st := BackendStatus{Kind: a.kind, URL: a.dsn}
	if err := a.open(); err != nil {
		st.Error = err.Error()
		return st
	}
	if err := a.db.Ping(); err != nil {
		st.Error = err.Error()
		st.Hint = hintForKind(a.kind)
		return st
	}
	st.Running = true
	var ver string
	_ = a.db.QueryRow(a.versionQuery()).Scan(&ver)
	st.Version = ver
	return st
}

func hintForKind(k BackendKind) string {
	switch k {
	case BackendPostgres:
		return "Run `yaver services add postgres && yaver services start postgres`"
	case BackendSupabase:
		return "Run `supabase start` in your project directory"
	case BackendSQLite:
		return "Check that DATABASE_URL points to a valid file path"
	}
	return ""
}

func (a *sqlAdapter) versionQuery() string {
	if a.driver == "postgres" {
		return "SHOW server_version"
	}
	return "SELECT sqlite_version()"
}

func (a *sqlAdapter) ListTables() ([]TableInfo, error) {
	if err := a.open(); err != nil {
		return nil, err
	}
	var q string
	if a.driver == "postgres" {
		q = `SELECT table_name FROM information_schema.tables
		     WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		     ORDER BY table_name`
	} else {
		q = `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	}
	rows, err := a.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, TableInfo{Name: name, Kind: "table"})
	}
	// Row counts (best-effort — skip on error so the list still works)
	for i := range out {
		var n int64
		if err := a.db.QueryRow("SELECT COUNT(*) FROM " + quoteIdent(a.driver, out[i].Name)).Scan(&n); err == nil {
			out[i].RowCount = &n
		}
	}
	return out, nil
}

func (a *sqlAdapter) Browse(table, cursor string, limit int) (*BrowseResult, error) {
	if err := a.open(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	offset := 0
	if cursor != "" {
		fmt.Sscanf(cursor, "%d", &offset)
	}
	q := fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", quoteIdent(a.driver, table), limit, offset)
	rows, err := a.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, limit)
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			row[c] = normalizeSQLValue(vals[i])
		}
		out = append(out, row)
	}
	next := ""
	if len(out) == limit {
		next = fmt.Sprintf("%d", offset+limit)
	}
	return &BrowseResult{Rows: out, NextCursor: next}, nil
}

// normalizeSQLValue converts []byte to string so JSON marshal is human-friendly.
func normalizeSQLValue(v interface{}) interface{} {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func (a *sqlAdapter) Query(q string, args map[string]interface{}) (interface{}, error) {
	if err := a.open(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(strings.ToLower(q))
	isSelect := strings.HasPrefix(trimmed, "select") || strings.HasPrefix(trimmed, "with") ||
		strings.HasPrefix(trimmed, "show") || strings.HasPrefix(trimmed, "explain") ||
		strings.HasPrefix(trimmed, "pragma")

	// Bind named args so parameterized queries actually work. Before
	// this, args was accepted by the API but silently dropped — every
	// `:name` placeholder failed ("missing named argument"), pushing
	// callers toward string interpolation (injection). modernc sqlite
	// and lib/pq both accept database/sql named parameters.
	named := make([]interface{}, 0, len(args))
	for k, v := range args {
		named = append(named, sql.Named(k, v))
	}

	if !isSelect {
		res, err := a.db.Exec(q, named...)
		if err != nil {
			return nil, err
		}
		affected, _ := res.RowsAffected()
		return map[string]interface{}{"rowsAffected": affected}, nil
	}

	rows, err := a.db.Query(q, named...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []map[string]interface{}{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			row[c] = normalizeSQLValue(vals[i])
		}
		out = append(out, row)
	}
	return out, nil
}

func (a *sqlAdapter) Insert(table string, doc map[string]interface{}) (string, error) {
	if err := a.open(); err != nil {
		return "", err
	}
	cols := make([]string, 0, len(doc))
	vals := make([]interface{}, 0, len(doc))
	placeholders := make([]string, 0, len(doc))
	i := 1
	for k, v := range doc {
		cols = append(cols, quoteIdent(a.driver, k))
		vals = append(vals, v)
		placeholders = append(placeholders, sqlPlaceholder(a.driver, i))
		i++
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(a.driver, table),
		strings.Join(cols, ","),
		strings.Join(placeholders, ","),
	)
	if a.driver == "postgres" {
		q += " RETURNING id"
		var id interface{}
		if err := a.db.QueryRow(q, vals...).Scan(&id); err != nil {
			return "", err
		}
		b, _ := json.Marshal(id)
		return strings.Trim(string(b), `"`), nil
	}
	res, err := a.db.Exec(q, vals...)
	if err != nil {
		return "", err
	}
	rowid, _ := res.LastInsertId()
	// LastInsertId is the integer rowid — NOT the real primary key when
	// the PK is a TEXT column with a DEFAULT (e.g. uuid). Callers use
	// the returned id to update/delete the row, so it must be the real
	// PK. If the caller supplied the PK, return that; else resolve it
	// from the just-inserted rowid.
	if pk := a.sqlitePrimaryKey(table); pk != "" {
		if v, ok := doc[pk]; ok && v != nil && fmt.Sprintf("%v", v) != "" {
			return fmt.Sprintf("%v", v), nil
		}
		var idv interface{}
		sel := fmt.Sprintf("SELECT %s FROM %s WHERE rowid = ?",
			quoteIdent(a.driver, pk), quoteIdent(a.driver, table))
		if e := a.db.QueryRow(sel, rowid).Scan(&idv); e == nil && idv != nil {
			return fmt.Sprintf("%v", normalizeSQLValue(idv)), nil
		}
	}
	return fmt.Sprintf("%d", rowid), nil
}

// sqlitePrimaryKey returns the single-column primary key name for a
// SQLite table via PRAGMA table_info, or "" if none/composite/non-sqlite.
func (a *sqlAdapter) sqlitePrimaryKey(table string) string {
	if a.driver != "sqlite" {
		return ""
	}
	rows, err := a.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(a.driver, table)))
	if err != nil {
		return ""
	}
	defer rows.Close()
	pkCol := ""
	count := 0
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return ""
		}
		if pk == 1 {
			pkCol = name
			count++
		}
	}
	if count != 1 {
		return "" // no PK or composite — fall back to rowid
	}
	return pkCol
}

func (a *sqlAdapter) Update(table, id string, fields map[string]interface{}) error {
	if err := a.open(); err != nil {
		return err
	}
	sets := make([]string, 0, len(fields))
	vals := make([]interface{}, 0, len(fields)+1)
	i := 1
	for k, v := range fields {
		sets = append(sets, fmt.Sprintf("%s = %s", quoteIdent(a.driver, k), sqlPlaceholder(a.driver, i)))
		vals = append(vals, v)
		i++
	}
	vals = append(vals, id)
	q := fmt.Sprintf("UPDATE %s SET %s WHERE id = %s",
		quoteIdent(a.driver, table),
		strings.Join(sets, ","),
		sqlPlaceholder(a.driver, i),
	)
	_, err := a.db.Exec(q, vals...)
	return err
}

func (a *sqlAdapter) Delete(table, id string) error {
	if err := a.open(); err != nil {
		return err
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE id = %s",
		quoteIdent(a.driver, table),
		sqlPlaceholder(a.driver, 1),
	)
	_, err := a.db.Exec(q, id)
	return err
}

func quoteIdent(driver, name string) string {
	// Basic identifier quoting; refuses embedded quotes to avoid injection.
	safe := strings.ReplaceAll(name, `"`, "")
	return `"` + safe + `"`
}

func sqlPlaceholder(driver string, i int) string {
	if driver == "postgres" {
		return fmt.Sprintf("$%d", i)
	}
	return "?"
}
