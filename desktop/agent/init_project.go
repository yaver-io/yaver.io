package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InitProjectOpts is the full parameter set for the `init_project` MCP tool.
// All fields are optional — sensible defaults fire in.
type InitProjectOpts struct {
	Name     string `json:"name"`
	ParentDir string `json:"parentDir"`
	Stack    string `json:"stack"`    // nextjs | vite | remix | sveltekit | expo | hono
	DB       string `json:"db"`       // postgres | supabase | convex | sqlite | pocketbase | none
	Auth     string `json:"auth"`     // better-auth | supabase-auth | convex-auth | none
	Payments string `json:"payments"` // stripe | lemonsqueezy | none
	Template string `json:"template"` // saas-starter | landing | dashboard | api | mobile
	ORM      string `json:"orm"`      // drizzle | prisma | none (auto-picked)
	Services []string `json:"services"` // extra Docker services: redis, mailpit, minio
}

// InitProjectResult summarizes what was scaffolded.
type InitProjectResult struct {
	OK        bool     `json:"ok"`
	Directory string   `json:"directory"`
	Backend   string   `json:"backend"`
	Services  []string `json:"services"`
	NextSteps []string `json:"nextSteps"`
	Errors    []string `json:"errors,omitempty"`
}

// InitProject scaffolds a new project end-to-end. It assembles compose
// fragments into .yaver/services.yaml, writes .yaver/config.yaml, creates a
// minimal starter layout, and returns next-step hints. The heavy lifting
// (create-next-app, full code gen) still belongs in the wizard; this direct
// tool exists so the MCP/CLI flow doesn't require a multi-step Q&A.
func InitProject(opts InitProjectOpts) (*InitProjectResult, error) {
	if opts.Name == "" {
		return nil, fmt.Errorf("project name required")
	}
	parent := opts.ParentDir
	if parent == "" {
		parent, _ = os.Getwd()
	}
	dir := filepath.Join(parent, opts.Name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("target %s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// Apply defaults.
	if opts.Stack == "" {
		opts.Stack = "nextjs"
	}
	if opts.DB == "" {
		opts.DB = "postgres"
	}
	if opts.Auth == "" {
		opts.Auth = "better-auth"
	}
	if opts.Payments == "" {
		opts.Payments = "stripe"
	}
	if opts.Template == "" {
		opts.Template = "saas-starter"
	}
	if opts.ORM == "" {
		switch opts.DB {
		case "postgres", "sqlite":
			opts.ORM = "drizzle"
		default:
			opts.ORM = "none"
		}
	}

	backend := mapInitBackend(opts.DB)
	cfg := &YaverProjectConfig{
		Backend: backend,
		Stack:   opts.Stack,
		Auth:    opts.Auth,
		Env:     map[string]string{},
	}
	if err := SaveProjectConfig(dir, cfg); err != nil {
		return nil, err
	}

	// Populate services.yaml via presets for the chosen backend + extras.
	sm := NewServicesManager(dir)
	var addedServices []string
	for _, svc := range servicesForInit(backend, opts) {
		if _, err := sm.Add(svc, nil); err == nil {
			addedServices = append(addedServices, svc)
		}
	}

	// Write a minimal starter package.json + README so the dir is a proper project.
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(starterPackageJSON(opts)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte(starterReadme(opts, addedServices)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env.example"), []byte(starterEnv(opts)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(starterGitignore()), 0o644)

	// If the user picked Convex, also drop the admin helper.
	if backend == BackendConvex {
		_ = InstallConvexHelper(dir)
	}

	// Kick off framework scaffold via the official CLI when we can detect it.
	var scaffoldMsg string
	if opts.Stack == "nextjs" {
		// Create-next-app runs inside `parent`, not `dir`. We need the directory name.
		cmd := exec.Command("npx", "--yes", "create-next-app@latest", opts.Name,
			"--ts", "--tailwind", "--app", "--src-dir", "--import-alias", "@/*", "--no-git",
			"--use-npm",
		)
		cmd.Dir = parent
		cmd.Env = append(os.Environ(), "CI=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Run async — don't block the MCP call.
		if err := cmd.Start(); err == nil {
			scaffoldMsg = "create-next-app running in background (pid " + fmt.Sprint(cmd.Process.Pid) + ")"
		}
	}

	// git init (non-fatal).
	_, _ = exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput()

	next := []string{
		"cd " + dir,
		"yaver services start  # launches: " + strings.Join(addedServices, ", "),
	}
	if opts.Stack == "nextjs" {
		next = append(next, "npm install && npm run dev")
	}
	if backend == BackendConvex {
		next = append(next, "npx convex dev")
	}

	res := &InitProjectResult{
		OK: true, Directory: dir, Backend: string(backend),
		Services: addedServices, NextSteps: next,
	}
	if scaffoldMsg != "" {
		res.NextSteps = append([]string{scaffoldMsg}, res.NextSteps...)
	}
	return res, nil
}

func mapInitBackend(s string) BackendKind {
	switch s {
	case "postgres":
		return BackendPostgres
	case "supabase":
		return BackendSupabase
	case "convex":
		return BackendConvex
	case "sqlite":
		return BackendSQLite
	case "pocketbase":
		return BackendPocketBase
	case "appwrite":
		return BackendAppwrite
	}
	return ""
}

func servicesForInit(backend BackendKind, opts InitProjectOpts) []string {
	var out []string
	switch backend {
	case BackendPostgres:
		out = append(out, "postgres")
	case BackendConvex:
		out = append(out, "convex", "convex-dashboard")
	case BackendPocketBase:
		out = append(out, "pocketbase")
	case BackendSupabase, BackendSQLite, BackendAppwrite:
		// Supabase uses `supabase start`; SQLite is file-only; Appwrite has its own installer.
	}
	// Extras from opts.Services take precedence, de-dup.
	seen := map[string]bool{}
	for _, s := range out {
		seen[s] = true
	}
	for _, s := range opts.Services {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	// Common dev services based on template.
	switch opts.Template {
	case "saas-starter":
		for _, s := range []string{"redis", "mailpit"} {
			if !seen[s] {
				out = append(out, s)
				seen[s] = true
			}
		}
	}
	return out
}

// ---- starter file templates (minimal — heavy templates remain in wizard) ----

func starterPackageJSON(opts InitProjectOpts) string {
	return fmt.Sprintf(`{
  "name": %q,
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "echo 'run your framework dev server here'"
  },
  "yaver": {
    "stack": %q,
    "db": %q,
    "auth": %q,
    "payments": %q,
    "template": %q,
    "createdAt": %q
  }
}
`, opts.Name, opts.Stack, opts.DB, opts.Auth, opts.Payments, opts.Template, time.Now().Format(time.RFC3339))
}

func starterReadme(opts InitProjectOpts, services []string) string {
	return fmt.Sprintf(`# %s

Generated by Yaver.

- **Stack:** %s
- **Database:** %s (ORM: %s)
- **Auth:** %s
- **Payments:** %s
- **Template:** %s
- **Services:** %s

## Getting Started

%s

## Yaver

- Dashboard: open the Yaver app and connect to this machine
- Switch backends: `+"`yaver switch plan <target>`"+`
- Browse data: `+"`yaver data tables`"+`
`,
		opts.Name, opts.Stack, opts.DB, opts.ORM, opts.Auth, opts.Payments, opts.Template,
		strings.Join(services, ", "),
		"```bash\nyaver services start\n"+frameworkStartCommand(opts)+"\n```",
	)
}

func frameworkStartCommand(opts InitProjectOpts) string {
	switch opts.Stack {
	case "nextjs":
		return "npm install && npm run dev"
	case "vite":
		return "npm install && npm run dev"
	case "expo":
		return "npm install && npx expo start"
	}
	return "# add your framework's dev command"
}

func starterEnv(opts InitProjectOpts) string {
	var b strings.Builder
	b.WriteString("# Yaver-generated .env.example\n")
	switch opts.DB {
	case "postgres":
		b.WriteString("DATABASE_URL=postgresql://postgres:dev@localhost:5432/" + opts.Name + "\n")
	case "supabase":
		b.WriteString("NEXT_PUBLIC_SUPABASE_URL=http://localhost:54321\n")
		b.WriteString("NEXT_PUBLIC_SUPABASE_ANON_KEY=<from supabase start>\n")
	case "convex":
		b.WriteString("CONVEX_SELF_HOSTED_URL=http://127.0.0.1:3210\n")
		b.WriteString("CONVEX_SELF_HOSTED_ADMIN_KEY=" + defaultConvexAdminKey + "\n")
	case "sqlite":
		b.WriteString("DATABASE_URL=file:./local.db\n")
	case "pocketbase":
		b.WriteString("POCKETBASE_URL=http://127.0.0.1:8090\n")
	}
	if opts.Payments == "stripe" {
		b.WriteString("STRIPE_SECRET_KEY=\nSTRIPE_WEBHOOK_SECRET=\n")
	}
	return b.String()
}

func starterGitignore() string {
	return `node_modules/
.next/
.env
.env.local
*.db
.yaver/snapshots/
.yaver/switches/*.yaml
convex_local_backend.sqlite3
`
}

// ---- MCP / HTTP ----

func mcpInitProject(optsJSON string) interface{} {
	var opts InitProjectOpts
	for k, v := range parseJSONArgs(optsJSON) {
		// best-effort: support passing opts as a top-level map
		switch k {
		case "name":
			opts.Name = fmt.Sprintf("%v", v)
		case "parentDir":
			opts.ParentDir = fmt.Sprintf("%v", v)
		case "stack":
			opts.Stack = fmt.Sprintf("%v", v)
		case "db":
			opts.DB = fmt.Sprintf("%v", v)
		case "auth":
			opts.Auth = fmt.Sprintf("%v", v)
		case "payments":
			opts.Payments = fmt.Sprintf("%v", v)
		case "template":
			opts.Template = fmt.Sprintf("%v", v)
		case "orm":
			opts.ORM = fmt.Sprintf("%v", v)
		case "services":
			if arr, ok := v.([]interface{}); ok {
				for _, s := range arr {
					opts.Services = append(opts.Services, fmt.Sprintf("%v", s))
				}
			}
		}
	}
	res, err := InitProject(opts)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}
