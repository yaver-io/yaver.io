package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StepExecutor runs a single step and returns its combined output.
type StepExecutor func(state *SwitchState, step *SwitchStep) (string, error)

func stepExecutors() map[string]StepExecutor {
	return map[string]StepExecutor{
		"snapshot":         execSnapshot,
		"provision":        execProvision,
		"migrate-data":     execMigrateData,
		"schema-translate": execSchemaTranslate,
		"migrate-auth":     execMigrateAuth,
		"update-env":       execUpdateEnv,
		"manual-oauth":     execManualOAuth,
		"emit-rewrite":     execEmitRewrite,
		"export-for-ai":    execExportForAI,
		"verify":           execVerify,
	}
}

// ---- snapshot ----

func execSnapshot(state *SwitchState, step *SwitchStep) (string, error) {
	branch := fmt.Sprintf("pre-switch/%s", state.ID)
	state.SnapshotBranch = branch

	out, err := runSwitchCmd(state.ProjectDir, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil {
		// Not a git repo — skip git snapshot but still dump data.
		out = "project is not a git repo; skipping branch snapshot"
	} else {
		if _, err := runSwitchCmd(state.ProjectDir, "git", "add", "-A"); err != nil {
			return out, err
		}
		// Allow empty commit so the branch exists even if there's nothing dirty.
		_, _ = runSwitchCmd(state.ProjectDir, "git", "commit", "-m", "yaver pre-switch snapshot "+state.ID, "--allow-empty")
		if o, err := runSwitchCmd(state.ProjectDir, "git", "branch", branch); err != nil {
			return out + "\n" + o, err
		}
	}

	// Data snapshot.
	snapDir := snapshotsDir(state.ProjectDir)
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return out, err
	}
	dataFile, dataErr := dumpBackendData(state.ProjectDir, state.FromBackend, snapDir, state.ID)
	if dataErr != nil {
		out += "\nwarning: data snapshot failed: " + dataErr.Error()
	} else {
		state.SnapshotData = dataFile
		out += "\ndata snapshot: " + dataFile
	}
	return out, nil
}

// dumpBackendData writes a backend-appropriate dump into snapDir and returns the path.
func dumpBackendData(projectDir string, backend BackendKind, snapDir, id string) (string, error) {
	switch backend {
	case BackendPostgres, BackendSupabase:
		return dumpPostgres(projectDir, snapDir, id)
	case BackendSQLite:
		return dumpSQLite(projectDir, snapDir, id)
	case BackendConvex:
		return dumpConvex(projectDir, snapDir, id)
	case BackendPocketBase, BackendAppwrite:
		return dumpViaAdapter(projectDir, snapDir, id)
	}
	return "", fmt.Errorf("no snapshotter for backend %q", backend)
}

func dumpPostgres(projectDir, snapDir, id string) (string, error) {
	dsn, _ := resolveDSN(projectDir, BackendPostgres)
	if dsn == "" {
		return "", fmt.Errorf("no DATABASE_URL / DSN found")
	}
	out := filepath.Join(snapDir, id+".sql")
	cmd := exec.Command("pg_dump", "--no-owner", "--no-privileges", dsn)
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pg_dump: %w", err)
	}
	return out, nil
}

func dumpSQLite(projectDir, snapDir, id string) (string, error) {
	dsn, _ := resolveDSN(projectDir, BackendSQLite)
	if dsn == "" {
		return "", fmt.Errorf("no SQLite file found")
	}
	out := filepath.Join(snapDir, id+".sqlite")
	srcBytes, err := os.ReadFile(dsn)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(out, srcBytes, 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func dumpConvex(projectDir, snapDir, id string) (string, error) {
	client := NewConvexAdminClient(projectDir)
	data, err := client.Export()
	if err != nil {
		return "", err
	}
	out := filepath.Join(snapDir, id+".convex.json")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func dumpViaAdapter(projectDir, snapDir, id string) (string, error) {
	adapter, err := NewBackendAdapter(projectDir)
	if err != nil {
		return "", err
	}
	tables, err := adapter.ListTables()
	if err != nil {
		return "", err
	}
	// Best-effort JSON dump: browse each table with large limit.
	out := filepath.Join(snapDir, id+".json")
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	f.WriteString("{\n")
	for i, t := range tables {
		res, err := adapter.Browse(t.Name, "", 10000)
		if err != nil {
			continue
		}
		if i > 0 {
			f.WriteString(",\n")
		}
		f.WriteString(fmt.Sprintf("  %q: ", t.Name))
		for j, row := range res.Rows {
			_ = j
			_ = row
		}
		// Write the rows array as a raw JSON dump
		writeJSONRows(f, res.Rows)
	}
	f.WriteString("\n}\n")
	return out, nil
}

func writeJSONRows(f *os.File, rows []map[string]interface{}) {
	if len(rows) == 0 {
		f.WriteString("[]")
		return
	}
	f.WriteString("[")
	for i, r := range rows {
		if i > 0 {
			f.WriteString(",")
		}
		b := argsToJSON(r)
		f.WriteString(b)
	}
	f.WriteString("]")
}

// ---- provision ----

func execProvision(state *SwitchState, step *SwitchStep) (string, error) {
	target, err := SwitchTargetByID(step.Args["target"])
	if err != nil {
		return "", err
	}
	// Try real provisioner first (uses stored cloud creds).
	if fn, ok := provisionerRegistry()[target.Host]; ok {
		name := filepath.Base(state.ProjectDir)
		res, err := fn(name, nil)
		if err != nil {
			return "", err
		}
		if res.Manual != "" {
			step.Status = StepManual
			return res.Manual, nil
		}
		// Persist connection details into step args so downstream steps can
		// pick up the new DSN/URL.
		if res.ConnectionString != "" {
			if step.Args == nil {
				step.Args = map[string]string{}
			}
			step.Args["connectionString"] = res.ConnectionString
			// Expose for the rest of the run via env.
			os.Setenv("PG_TARGET_DSN", res.ConnectionString)
		}
		b, _ := json.Marshal(res)
		return string(b), nil
	}
	switch target.Host {
	case HostLocalDocker:
		// Start matching local services
		sm := NewServicesManager(state.ProjectDir)
		if target.Backend == BackendConvex {
			_, _ = sm.Add("convex", nil)
			return sm.Start("convex")
		}
		if target.Backend == BackendPostgres {
			_, _ = sm.Add("postgres", nil)
			return sm.Start("postgres")
		}
		if target.Backend == BackendPocketBase {
			_, _ = sm.Add("pocketbase", nil)
			return sm.Start("pocketbase")
		}
		return "", fmt.Errorf("no local-docker provisioner for %s", target.Backend)
	case HostConvexCloud:
		details, err := seamlessConvexCloud(state.ProjectDir)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(details)
		return "convex cloud ready: " + string(b), nil
	case HostSupabaseCloud:
		details, err := seamlessSupabaseCloud(state.ProjectDir, filepath.Base(state.ProjectDir))
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(details)
		return "supabase cloud ready: " + string(b), nil
	case HostYaverCloud:
		return "Manual: provision the VPS via `yaver hetzner provision`", nil
	case HostVercel:
		return runSwitchCmd(state.ProjectDir, "npx", "vercel", "link")
	case HostCFWorkers:
		return runSwitchCmd(state.ProjectDir, "npx", "wrangler", "deploy", "--dry-run")
	}
	return "", fmt.Errorf("no provisioner for host %s", target.Host)
}

// ---- migrate-data ----

func execMigrateData(state *SwitchState, step *SwitchStep) (string, error) {
	target, err := SwitchTargetByID(step.Args["target"])
	if err != nil {
		return "", err
	}
	fromFam := backendFamily(state.FromBackend)

	if target.Family == FamilyConvex && state.FromBackend == BackendConvex {
		return convexStreamImport(state.ProjectDir, state.SnapshotData, target)
	}
	if fromFam == FamilyPostgres && target.Family == FamilyPostgres {
		return pgRestore(state.ProjectDir, state.SnapshotData, target)
	}
	if fromFam == FamilySQLite && target.Family == FamilySQLite {
		// Same family, usually just a config change — nothing to migrate beyond pointing at the new DSN.
		return "sqlite → sqlite: nothing to migrate; env update will handle it", nil
	}
	return fmt.Sprintf("No automatic migrator for %s → %s. The AI rewrite step handles cross-family data.", state.FromBackend, target.Backend), nil
}

func convexStreamImport(projectDir, dump string, target *SwitchTarget) (string, error) {
	if dump == "" {
		return "", fmt.Errorf("no data snapshot to import")
	}
	if target.Host == HostConvexCloud {
		return fmt.Sprintf("To import into Convex Cloud, run:\n  npx convex import --replace %s\nYaver cannot run this without your cloud deployment URL.", dump), nil
	}
	// local docker import → use streaming_import
	client := NewConvexAdminClient(projectDir)
	data, err := os.ReadFile(dump)
	if err != nil {
		return "", err
	}
	if _, err := client.post("/api/streaming_import", map[string]interface{}{"data": string(data)}, true); err != nil {
		return "", err
	}
	return "imported " + dump, nil
}

func pgRestore(projectDir, dump string, target *SwitchTarget) (string, error) {
	if dump == "" {
		return "", fmt.Errorf("no pg_dump snapshot")
	}
	// Target DSN resolution order:
	// 1. PG_TARGET_DSN env var (set by the provisioner step of this switch)
	// 2. DATABASE_URL from .env.local (set by seamless provisioners)
	// 3. Error with a helpful message
	targetDSN := os.Getenv("PG_TARGET_DSN")
	if targetDSN == "" {
		targetDSN = readEnvValue(projectDir, "DATABASE_URL")
	}
	if targetDSN == "" {
		return "", fmt.Errorf("no target DSN (ran seamless provision? PG_TARGET_DSN not set and no DATABASE_URL in .env.local)")
	}
	f, err := os.Open(dump)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cmd := exec.Command("psql", targetDSN)
	cmd.Stdin = f
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("psql restore: %w", err)
	}
	return string(out), nil
}

// ---- schema-translate (SQLite → Postgres, etc.) ----

func execSchemaTranslate(state *SwitchState, step *SwitchStep) (string, error) {
	// For Drizzle projects we can delegate to drizzle-kit.
	if _, err := os.Stat(filepath.Join(state.ProjectDir, "drizzle.config.ts")); err == nil {
		return runSwitchCmd(state.ProjectDir, "npx", "drizzle-kit", "push")
	}
	return "No drizzle.config.ts found. Translate your schema manually; this step is a no-op until Yaver can read your ORM layer.", nil
}

// ---- migrate-auth ----

func execMigrateAuth(state *SwitchState, step *SwitchStep) (string, error) {
	// Auth migration is almost always manual or AI-driven. Emit guidance only.
	return "Review your auth provider and reconfigure for the new backend. Better Auth keeps working if the underlying DB driver is the same.", nil
}

// ---- update-env ----

func execUpdateEnv(state *SwitchState, step *SwitchStep) (string, error) {
	target, err := SwitchTargetByID(step.Args["target"])
	if err != nil {
		return "", err
	}
	envPath := filepath.Join(state.ProjectDir, ".env.local")
	existing := ""
	if data, err := os.ReadFile(envPath); err == nil {
		existing = string(data)
	}

	var additions []string
	switch target.Host {
	case HostConvexCloud:
		additions = append(additions, "# Yaver switch: Convex Cloud", "# CONVEX_URL=<run `npx convex deploy` and copy the URL here>")
	case HostSupabaseCloud:
		additions = append(additions,
			"# Yaver switch: Supabase Cloud",
			"# NEXT_PUBLIC_SUPABASE_URL=https://<project>.supabase.co",
			"# NEXT_PUBLIC_SUPABASE_ANON_KEY=<from Supabase dashboard>",
			"# SUPABASE_SERVICE_ROLE_KEY=<from Supabase dashboard>",
		)
	case HostLocalDocker:
		if target.Backend == BackendConvex {
			additions = append(additions, "CONVEX_SELF_HOSTED_URL=http://127.0.0.1:3210")
		}
		if target.Backend == BackendPostgres {
			additions = append(additions, "DATABASE_URL=postgres://postgres:dev@localhost:5432/myapp?sslmode=disable")
		}
	}
	marker := "# === yaver switch " + state.ID + " ==="
	block := marker + "\n" + strings.Join(additions, "\n") + "\n"
	if strings.Contains(existing, marker) {
		return "env block already present", nil
	}
	newContent := existing
	if !strings.HasSuffix(newContent, "\n") && newContent != "" {
		newContent += "\n"
	}
	newContent += "\n" + block
	if err := os.WriteFile(envPath, []byte(newContent), 0o644); err != nil {
		return "", err
	}
	return "wrote " + envPath + " with placeholders for " + target.Label, nil
}

// ---- manual steps ----

func execManualOAuth(state *SwitchState, step *SwitchStep) (string, error) {
	return "Update OAuth redirect URIs at Google/Apple/Microsoft/GitHub consoles. Add the new host URL; keep the old one until rollback window expires.", nil
}

// ---- emit-rewrite (HARD) ----

func execEmitRewrite(state *SwitchState, step *SwitchStep) (string, error) {
	// Persist the rewrite prompt to a markdown file the AI agent can read.
	out := filepath.Join(state.ProjectDir, ".yaver", "switches", state.ID+"_rewrite.md")
	if err := os.WriteFile(out, []byte(state.RewritePrompt), 0o644); err != nil {
		return "", err
	}
	return "Rewrite prompt written to " + out + ". Run `yaver task create --from-file " + out + "` or pick it up via the dashboard.", nil
}

// ---- export-for-ai ----

func execExportForAI(state *SwitchState, step *SwitchStep) (string, error) {
	target, err := SwitchTargetByID(step.Args["target"])
	if err != nil {
		return "", err
	}
	// Re-use the snapshot. For cross-paradigm we typically want JSON.
	return fmt.Sprintf("Data export already at %s. AI rewrite should read this and produce %s-native inserts.", state.SnapshotData, target.Backend), nil
}

// ---- verify ----

func execVerify(state *SwitchState, step *SwitchStep) (string, error) {
	// Cheap smoke test: status on the target backend.
	target, err := SwitchTargetByID(step.Args["target"])
	if err != nil {
		return "", err
	}
	cfg := &YaverProjectConfig{Backend: target.Backend}
	if target.Backend == "" {
		return "pure deploy target — verify manually by hitting the app URL", nil
	}
	adapter, err := newBackendAdapter(state.ProjectDir, cfg)
	if err != nil {
		return "", err
	}
	st := adapter.Status()
	if !st.Running {
		return "", fmt.Errorf("target backend not reachable: %s", st.Error)
	}
	return fmt.Sprintf("target healthy (%s)", st.URL), nil
}

// ---- helpers ----

func runSwitchCmd(dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CI=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func resolveDSN(projectDir string, backend BackendKind) (string, string) {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		cfg = &YaverProjectConfig{Backend: backend}
	}
	driver, dsn, err := resolveSQLDSN(projectDir, cfg, backend)
	if err != nil {
		return "", ""
	}
	return dsn, driver
}

// buildRewritePrompt emits a Claude-Code-targeted instructions pack for HARD switches.
func buildRewritePrompt(projectDir string, from *YaverProjectConfig, to *SwitchTarget) string {
	fromFam := backendFamily(from.Backend)
	toFam := to.Family
	var sb strings.Builder
	sb.WriteString("# Yaver Switch: Paradigm Rewrite\n\n")
	sb.WriteString("Generated: " + time.Now().Format(time.RFC3339) + "\n\n")
	sb.WriteString("## Task\n")
	sb.WriteString(fmt.Sprintf("Rewrite this project's data layer to switch from **%s** (%s) to **%s** (%s).\n\n", from.Backend, fromFam, to.Backend, toFam))
	sb.WriteString("## What to change\n")
	switch {
	case fromFam == FamilyPostgres && toFam == FamilyConvex:
		sb.WriteString(`- Delete Drizzle schema files in src/db/ or db/
- Create convex/schema.ts using defineTable + v validators
- Rewrite db.select/.insert/.update/.delete calls to ctx.db.query/.insert/.patch/.delete
- SQL joins → Convex indexes + separate queries (Convex has no joins)
- Replace API route handlers that query the DB with Convex functions (convex/*.ts)
- Replace client-side useQuery(drizzle) hooks with useQuery(api.table.list)
- Update auth: Better Auth → Convex Auth (see https://labs.convex.dev/auth)
- Replace file upload code: filesystem → ctx.storage.store()
- Keep business logic identical — only change the data-access code
`)
	case fromFam == FamilyConvex && toFam == FamilyPostgres:
		sb.WriteString(`- Delete convex/ directory (keep backups in .yaver/snapshots/)
- Add Drizzle: npm i drizzle-orm drizzle-kit pg
- Create drizzle.config.ts and src/db/schema.ts from the old convex/schema.ts
- Rewrite ctx.db.query(...) → db.select().from(table)
- Rewrite ctx.db.insert(...) → db.insert(table).values(...)
- Replace Convex reactivity: useQuery(api.x) → useQuery(TanStack) + polling, or SWR
- Add API route handlers (src/app/api/... in Next.js, or src/routes in other frameworks) for each Convex function
- Replace Convex Auth with Better Auth
- Replace ctx.storage with S3/MinIO/local filesystem
- Ingest the data dump at .yaver/snapshots/ — each JSON table → INSERT rows
`)
	default:
		sb.WriteString(fmt.Sprintf("Cross-paradigm rewrite from %s to %s.\n", fromFam, toFam))
	}
	sb.WriteString("\n## Context\n")
	sb.WriteString("- Project dir: " + projectDir + "\n")
	sb.WriteString("- Snapshot branch: (will be created by the snapshot step)\n")
	sb.WriteString("- Data dump: .yaver/snapshots/<id>.json|.sql|.convex.json\n")
	sb.WriteString("- .yaver/config.yaml backend will be updated to: " + string(to.Backend) + "\n")
	sb.WriteString("\n## Deliverable\n")
	sb.WriteString("One PR against the main branch with all code changes. Business logic and UI must remain identical. Tests (if any) must pass.\n")
	return sb.String()
}
