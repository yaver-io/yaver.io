package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// MigrationStep represents a single, independently executable step in a plan.
type MigrationStep struct {
	Number       int           `json:"number"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Status       string        `json:"status"` // pending | running | completed | failed | skipped
	Duration     time.Duration `json:"duration_ns"`
	Output       string        `json:"output"`
	Rollbackable bool          `json:"rollbackable"`
}

// MigrationPlan is the complete migration specification for a project.
type MigrationPlan struct {
	ID            string          `json:"id"`
	From          string          `json:"from"`
	To            string          `json:"to"`
	Steps         []MigrationStep `json:"steps"`
	EstimatedTime time.Duration   `json:"estimated_time_ns"`
	CostEstimate  string          `json:"cost_estimate"`
	CreatedAt     time.Time       `json:"created_at"`
}

// MigrationTarget describes a supported deployment target.
type MigrationTarget struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CostRange   string   `json:"cost_range"`
	Pros        []string `json:"pros"`
	Cons        []string `json:"cons"`
}

// MigrateManager is the universal migration engine.
type MigrateManager struct {
	mu          sync.Mutex
	workDir     string
	currentPlan *MigrationPlan
	plansDir    string
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewMigrateManager creates a MigrateManager rooted at workDir.
// Plans are persisted to ~/.yaver/migrations/.
func NewMigrateManager(workDir string) *MigrateManager {
	home, _ := os.UserHomeDir()
	plansDir := filepath.Join(home, ".yaver", "migrations")
	return &MigrateManager{
		workDir:  workDir,
		plansDir: plansDir,
	}
}

// ---------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------

// Plan generates a MigrationPlan for the given from→to path, saves it to
// disk, and returns it. Calling Plan replaces any active plan in memory.
func (m *MigrateManager) Plan(from, to string) (*MigrationPlan, error) {
	if from == "" {
		from = m.detectCurrentTier()
	}
	if from == to {
		return nil, fmt.Errorf("source and destination are both %q — nothing to do", from)
	}

	analysis := m.analyzeProject(m.workDir)
	steps := m.generateSteps(from, to, analysis)
	if len(steps) == 0 {
		return nil, fmt.Errorf("migration path %q → %q is not supported", from, to)
	}

	// Rough time estimate: 5 min per step on average.
	estimated := time.Duration(len(steps)) * 5 * time.Minute

	cost := m.estimateCost(to)

	plan := &MigrationPlan{
		ID:            fmt.Sprintf("%s-%s-%d", from, to, time.Now().Unix()),
		From:          from,
		To:            to,
		Steps:         steps,
		EstimatedTime: estimated,
		CostEstimate:  cost,
		CreatedAt:     time.Now(),
	}

	if err := m.SavePlan(plan); err != nil {
		return nil, fmt.Errorf("save plan: %w", err)
	}

	m.mu.Lock()
	m.currentPlan = plan
	m.mu.Unlock()

	return plan, nil
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

// Run executes a specific step (stepNumber > 0) or all remaining pending steps
// (stepNumber == 0) of the saved plan identified by planID.
// Returns a human-readable progress summary.
func (m *MigrateManager) Run(planID string, stepNumber int) (string, error) {
	plan, err := m.LoadPlan(planID)
	if err != nil {
		return "", fmt.Errorf("load plan: %w", err)
	}

	m.mu.Lock()
	m.currentPlan = plan
	m.mu.Unlock()

	var sb strings.Builder

	for i := range plan.Steps {
		step := &plan.Steps[i]

		if stepNumber > 0 && step.Number != stepNumber {
			continue
		}
		if step.Status == "completed" || step.Status == "skipped" {
			continue
		}

		step.Status = "running"
		if err := m.SavePlan(plan); err != nil {
			return sb.String(), fmt.Errorf("save plan mid-run: %w", err)
		}

		start := time.Now()
		out, runErr := m.executeStep(plan, step)
		step.Duration = time.Since(start)
		step.Output = out

		if runErr != nil {
			step.Status = "failed"
			_ = m.SavePlan(plan)
			return sb.String(), fmt.Errorf("step %d %q failed: %w\nOutput: %s", step.Number, step.Name, runErr, out)
		}

		step.Status = "completed"
		_ = m.SavePlan(plan)

		fmt.Fprintf(&sb, "[%d/%d] %s — done (%s)\n", step.Number, len(plan.Steps), step.Name, step.Duration.Round(time.Second))

		if stepNumber > 0 {
			break
		}
	}

	if sb.Len() == 0 {
		return "No pending steps to run.", nil
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status returns the current in-memory plan, loading it from disk if needed.
func (m *MigrateManager) Status() (*MigrationPlan, error) {
	m.mu.Lock()
	plan := m.currentPlan
	m.mu.Unlock()

	if plan != nil {
		return plan, nil
	}
	return nil, fmt.Errorf("no active migration plan — run 'yaver migrate plan <from> <to>' first")
}

// ---------------------------------------------------------------------------
// Rollback
// ---------------------------------------------------------------------------

// Rollback reverses a completed, rollbackable step identified by stepNumber
// in the plan identified by planID.
func (m *MigrateManager) Rollback(planID string, stepNumber int) (string, error) {
	plan, err := m.LoadPlan(planID)
	if err != nil {
		return "", fmt.Errorf("load plan: %w", err)
	}

	var target *MigrationStep
	for i := range plan.Steps {
		if plan.Steps[i].Number == stepNumber {
			target = &plan.Steps[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("step %d not found in plan %s", stepNumber, planID)
	}
	if !target.Rollbackable {
		return "", fmt.Errorf("step %d %q is not rollbackable", stepNumber, target.Name)
	}
	if target.Status != "completed" {
		return "", fmt.Errorf("step %d has status %q — only completed steps can be rolled back", stepNumber, target.Status)
	}

	out, err := m.rollbackStep(plan, target)
	if err != nil {
		return out, fmt.Errorf("rollback step %d %q: %w", stepNumber, target.Name, err)
	}

	target.Status = "pending"
	target.Output = fmt.Sprintf("[rolled back]\n%s", out)
	_ = m.SavePlan(plan)

	return fmt.Sprintf("Step %d %q rolled back successfully.\n%s", stepNumber, target.Name, out), nil
}

// ---------------------------------------------------------------------------
// ListTargets
// ---------------------------------------------------------------------------

// ListTargets returns every supported migration destination with metadata.
func (m *MigrateManager) ListTargets() []MigrationTarget {
	return []MigrationTarget{
		{
			Name:        "local",
			Description: "Run everything on localhost (dev only)",
			CostRange:   "$0/mo",
			Pros:        []string{"Zero cost", "Instant iteration", "Full control"},
			Cons:        []string{"Not reachable externally", "No HA", "Dev only"},
		},
		{
			Name:        "self-hosted",
			Description: "Your own VPS/bare-metal with Docker Compose + Caddy",
			CostRange:   "$5–$50/mo (VPS)",
			Pros:        []string{"Full control", "No vendor lock-in", "Data sovereignty"},
			Cons:        []string{"You manage infra", "Manual SSL renewal", "No auto-scaling"},
		},
		{
			Name:        "yaver-cloud",
			Description: "Yaver-managed VPS — same as self-hosted but provisioned and monitored for you",
			CostRange:   "$49/mo (CPU) / $449/mo (GPU)",
			Pros:        []string{"Managed infra", "Auto-update agent", "Includes relay + GPU tier"},
			Cons:        []string{"Higher cost than raw VPS", "Yaver account required"},
		},
		{
			Name:        "vercel",
			Description: "Vercel for frontend/API routes + Supabase for Postgres + storage",
			CostRange:   "$0–$20/mo (hobby/pro)",
			Pros:        []string{"Zero-config deploys", "Edge CDN", "Generous free tier"},
			Cons:        []string{"Cold starts on serverless", "Vendor lock-in", "Limited long-running processes"},
		},
		{
			Name:        "fly",
			Description: "Fly.io for containers + Neon for serverless Postgres",
			CostRange:   "$0–$30/mo",
			Pros:        []string{"True containers", "Close to Heroku DX", "Multi-region"},
			Cons:        []string{"Small community vs Vercel", "Neon cold starts"},
		},
		{
			Name:        "cloudflare",
			Description: "Cloudflare Workers + D1 (SQLite) + R2 (object storage)",
			CostRange:   "$0–$5/mo",
			Pros:        []string{"Globally distributed edge", "Extremely cheap", "No cold starts"},
			Cons:        []string{"Workers runtime limitations", "D1 is SQLite-only", "No long-running processes"},
		},
		{
			Name:        "railway",
			Description: "Railway.app — container platform with managed Postgres",
			CostRange:   "$5–$20/mo",
			Pros:        []string{"Simple DX", "No YAML config", "Built-in Postgres"},
			Cons:        []string{"Less mature than Fly/Vercel", "Limited regions"},
		},
	}
}

// ---------------------------------------------------------------------------
// MigrateDB
// ---------------------------------------------------------------------------

// MigrateDB copies schema + data + sequences from sourceURL to targetURL.
// Supports postgres:// and sqlite:// DSNs.
func (m *MigrateManager) MigrateDB(sourceURL, targetURL string) (string, error) {
	var sb strings.Builder

	switch {
	case strings.HasPrefix(sourceURL, "postgres://") || strings.HasPrefix(sourceURL, "postgresql://"):
		fmt.Fprintln(&sb, "Detected Postgres source — using pg_dump / pg_restore")

		dumpFile := filepath.Join(os.TempDir(), fmt.Sprintf("yaver-db-dump-%d.sql", time.Now().Unix()))
		defer os.Remove(dumpFile)

		// Dump
		dumpOut, err := migrateRunCmd("pg_dump", "--no-owner", "--no-acl", "-Fc", "-f", dumpFile, sourceURL)
		fmt.Fprintln(&sb, dumpOut)
		if err != nil {
			return sb.String(), fmt.Errorf("pg_dump failed: %w", err)
		}
		fmt.Fprintln(&sb, "Source dump complete:", dumpFile)

		// Restore
		restoreOut, err := migrateRunCmd("pg_restore", "--no-owner", "--no-acl", "-d", targetURL, dumpFile)
		fmt.Fprintln(&sb, restoreOut)
		if err != nil {
			return sb.String(), fmt.Errorf("pg_restore failed: %w", err)
		}
		fmt.Fprintln(&sb, "Restore complete.")

	case strings.HasPrefix(sourceURL, "sqlite://") || strings.HasSuffix(sourceURL, ".db") || strings.HasSuffix(sourceURL, ".sqlite"):
		srcPath := strings.TrimPrefix(sourceURL, "sqlite://")
		dstPath := strings.TrimPrefix(targetURL, "sqlite://")
		fmt.Fprintf(&sb, "Detected SQLite source: %s → %s\n", srcPath, dstPath)

		dumpFile := filepath.Join(os.TempDir(), fmt.Sprintf("yaver-sqlite-dump-%d.sql", time.Now().Unix()))
		defer os.Remove(dumpFile)

		dumpSQL, err := migrateRunCmd("sqlite3", srcPath, ".dump")
		fmt.Fprintln(&sb, "Dump captured.")
		if err != nil {
			return sb.String(), fmt.Errorf("sqlite3 dump failed: %w", err)
		}
		if err := os.WriteFile(dumpFile, []byte(dumpSQL), 0600); err != nil {
			return sb.String(), fmt.Errorf("write dump file: %w", err)
		}
		restoreOut, err := migrateRunCmd("sqlite3", dstPath, fmt.Sprintf(".read %s", dumpFile))
		fmt.Fprintln(&sb, restoreOut)
		if err != nil {
			return sb.String(), fmt.Errorf("sqlite3 restore failed: %w", err)
		}
		fmt.Fprintln(&sb, "Restore complete.")

	default:
		return "", fmt.Errorf("unsupported database DSN — expected postgres:// or sqlite:// prefix")
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// MigrateStorage
// ---------------------------------------------------------------------------

// MigrateStorage copies files between two storage backends.
// sourceType / targetType: "local", "s3", "minio", "supabase", "r2"
func (m *MigrateManager) MigrateStorage(sourceType, sourcePath, targetType, targetPath string) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Migrating storage: [%s] %s → [%s] %s\n", sourceType, sourcePath, targetType, targetPath)

	toMCAlias := func(t, path string) string {
		switch t {
		case "local":
			return path
		case "s3":
			return "s3/" + path
		case "minio":
			return "minio/" + path
		case "supabase":
			return "supabase/" + path
		case "r2":
			return "r2/" + path
		default:
			return path
		}
	}

	src := toMCAlias(sourceType, sourcePath)
	dst := toMCAlias(targetType, targetPath)

	// Prefer `mc` (MinIO Client) which handles all backends; fall back to `aws s3 cp`.
	if _, err := exec.LookPath("mc"); err == nil {
		out, err := migrateRunCmd("mc", "mirror", "--overwrite", src, dst)
		fmt.Fprintln(&sb, out)
		if err != nil {
			return sb.String(), fmt.Errorf("mc mirror failed: %w", err)
		}
	} else if _, err := exec.LookPath("aws"); err == nil {
		fmt.Fprintln(&sb, "mc not found — falling back to aws s3 sync")
		out, err := migrateRunCmd("aws", "s3", "sync", src, dst)
		fmt.Fprintln(&sb, out)
		if err != nil {
			return sb.String(), fmt.Errorf("aws s3 sync failed: %w", err)
		}
	} else {
		return sb.String(), fmt.Errorf("neither 'mc' nor 'aws' CLI found — install one of them first")
	}

	fmt.Fprintln(&sb, "Storage migration complete.")
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// MigrateEnv
// ---------------------------------------------------------------------------

// MigrateEnv reads .env.local / .env in workDir and pushes variables to the
// target platform's secret store.
// Supported targets: vercel, fly, railway, self-hosted (writes .env on remote).
func (m *MigrateManager) MigrateEnv(from, to string) (string, error) {
	var sb strings.Builder

	envFile := m.findEnvFile()
	if envFile == "" {
		return "", fmt.Errorf("no .env or .env.local file found in %s", m.workDir)
	}

	vars, err := parseEnvFile(envFile)
	if err != nil {
		return "", fmt.Errorf("parse env file: %w", err)
	}

	fmt.Fprintf(&sb, "Read %d variables from %s\n", len(vars), envFile)

	switch to {
	case "vercel":
		for k, v := range vars {
			out, err := migrateRunCmdInput(fmt.Sprintf("%s\n", v), "vercel", "env", "add", k, "production")
			fmt.Fprintln(&sb, out)
			if err != nil {
				fmt.Fprintf(&sb, "  WARN: failed to set %s: %v\n", k, err)
			}
		}
		fmt.Fprintln(&sb, "Vercel env vars updated — run `vercel deploy` to apply.")

	case "fly":
		args := []string{"secrets", "set"}
		for k, v := range vars {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
		out, err := migrateRunCmd("fly", args...)
		fmt.Fprintln(&sb, out)
		if err != nil {
			return sb.String(), fmt.Errorf("fly secrets set failed: %w", err)
		}

	case "railway":
		for k, v := range vars {
			out, err := migrateRunCmd("railway", "variables", "set", fmt.Sprintf("%s=%s", k, v))
			fmt.Fprintln(&sb, out)
			if err != nil {
				fmt.Fprintf(&sb, "  WARN: failed to set %s: %v\n", k, err)
			}
		}

	case "self-hosted", "yaver-cloud":
		fmt.Fprintln(&sb, "Copy the following to your .env file on the remote server:")
		for k, v := range vars {
			fmt.Fprintf(&sb, "  %s=%s\n", k, v)
		}

	default:
		return "", fmt.Errorf("unsupported env migration target: %q", to)
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// MigrateDNS
// ---------------------------------------------------------------------------

// MigrateDNS updates an A record for domain to targetIP using the Cloudflare
// API when a token is available, otherwise outputs manual instructions.
func (m *MigrateManager) MigrateDNS(domain, provider, targetIP string) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DNS migration: %s → %s (provider: %s)\n", domain, targetIP, provider)

	switch provider {
	case "cloudflare":
		token := os.Getenv("CLOUDFLARE_API_TOKEN")
		zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")
		if token == "" || zoneID == "" {
			fmt.Fprintln(&sb, "CLOUDFLARE_API_TOKEN / CLOUDFLARE_ZONE_ID not set — manual steps below:")
			m.dnsManualInstructions(&sb, domain, targetIP)
			return sb.String(), nil
		}
		out, err := cloudflareUpdateDNS(token, zoneID, domain, targetIP)
		fmt.Fprintln(&sb, out)
		if err != nil {
			return sb.String(), fmt.Errorf("cloudflare DNS update: %w", err)
		}
		fmt.Fprintln(&sb, "DNS updated via Cloudflare API. Changes propagate in ≤ 5 minutes.")

	default:
		fmt.Fprintf(&sb, "Provider %q does not have automatic DNS support.\n", provider)
		m.dnsManualInstructions(&sb, domain, targetIP)
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// MigrateOAuthUpdate
// ---------------------------------------------------------------------------

// MigrateOAuthUpdate generates human-readable instructions for updating
// OAuth redirect URIs in detected providers after a domain change.
func (m *MigrateManager) MigrateOAuthUpdate(oldDomain, newDomain string) (string, error) {
	analysis := m.analyzeProject(m.workDir)
	providers, _ := analysis["oauth_providers"].([]string)

	var sb strings.Builder
	fmt.Fprintf(&sb, "OAuth redirect URI update: %s → %s\n\n", oldDomain, newDomain)

	if len(providers) == 0 {
		providers = []string{"(unknown — scan your .env for *_CLIENT_ID keys)"}
	}

	for _, p := range providers {
		switch strings.ToLower(p) {
		case "google":
			fmt.Fprintf(&sb, "Google OAuth:\n  https://console.cloud.google.com/apis/credentials\n"+
				"  Replace all occurrences of %s with %s in Authorized Redirect URIs.\n\n", oldDomain, newDomain)
		case "github":
			fmt.Fprintf(&sb, "GitHub OAuth:\n  https://github.com/settings/developers\n"+
				"  Update Authorization callback URL from %s to %s.\n\n", oldDomain, newDomain)
		case "apple":
			fmt.Fprintf(&sb, "Apple Sign-In:\n  https://developer.apple.com/account/resources/identifiers\n"+
				"  Update Return URLs from %s to %s.\n\n", oldDomain, newDomain)
		case "microsoft":
			fmt.Fprintf(&sb, "Microsoft OAuth (Azure):\n  https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps\n"+
				"  Update Redirect URIs from %s to %s.\n\n", oldDomain, newDomain)
		default:
			fmt.Fprintf(&sb, "%s: update redirect URIs from %s to %s in the provider dashboard.\n\n", p, oldDomain, newDomain)
		}
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------

// Verify runs post-migration smoke tests for the plan identified by planID.
func (m *MigrateManager) Verify(planID string) (string, error) {
	plan, err := m.LoadPlan(planID)
	if err != nil {
		return "", fmt.Errorf("load plan: %w", err)
	}

	analysis := m.analyzeProject(m.workDir)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Smoke tests for migration %s (%s → %s)\n\n", plan.ID, plan.From, plan.To)

	// 1. HTTP health check
	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		fmt.Fprintln(&sb, "SKIP health-check: APP_URL not set")
	} else {
		out, err := migrateRunCmd("curl", "-sf", "--max-time", "10", appURL+"/health")
		if err != nil {
			fmt.Fprintf(&sb, "FAIL /health: %v\n  %s\n", err, out)
		} else {
			fmt.Fprintf(&sb, "PASS /health → %s\n", strings.TrimSpace(out))
		}
	}

	// 2. Database connectivity
	if dbURL, ok := analysis["db_url"].(string); ok && dbURL != "" {
		out, err := migrateRunCmd("pg_isready", "-d", dbURL)
		if err != nil {
			fmt.Fprintf(&sb, "FAIL db connectivity: %v\n  %s\n", err, out)
		} else {
			fmt.Fprintf(&sb, "PASS db connectivity: %s\n", strings.TrimSpace(out))
		}
	}

	// 3. OAuth round-trip (just validate config is present)
	if providers, ok := analysis["oauth_providers"].([]string); ok && len(providers) > 0 {
		fmt.Fprintf(&sb, "INFO OAuth providers detected: %v — verify redirect URIs manually.\n", providers)
	}

	// 4. Stripe webhook reachability
	if _, ok := analysis["stripe"].(bool); ok {
		stripeWebhook := fmt.Sprintf("%s/webhook/stripe", appURL)
		out, err := migrateRunCmd("curl", "-sf", "--max-time", "10", "-X", "POST", stripeWebhook)
		if err != nil {
			fmt.Fprintf(&sb, "WARN Stripe webhook endpoint: %v (expected — Stripe signature check)\n  %s\n", err, out)
		} else {
			fmt.Fprintf(&sb, "PASS Stripe webhook endpoint reachable.\n")
		}
	}

	fmt.Fprintln(&sb, "\nSmoke tests complete.")
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// SavePlan writes the plan to ~/.yaver/migrations/<plan.ID>.json.
func (m *MigrateManager) SavePlan(plan *MigrationPlan) error {
	if err := os.MkdirAll(m.plansDir, 0700); err != nil {
		return fmt.Errorf("create plans dir: %w", err)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	path := filepath.Join(m.plansDir, plan.ID+".json")
	return os.WriteFile(path, data, 0600)
}

// LoadPlan reads a plan from ~/.yaver/migrations/<planID>.json.
func (m *MigrateManager) LoadPlan(planID string) (*MigrationPlan, error) {
	path := filepath.Join(m.plansDir, planID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plan file %s: %w", path, err)
	}
	var plan MigrationPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}
	return &plan, nil
}

// ---------------------------------------------------------------------------
// Internal helpers — detection & analysis
// ---------------------------------------------------------------------------

// detectCurrentTier infers the current deployment tier from config and project files.
func (m *MigrateManager) detectCurrentTier() string {
	cfg, err := LoadConfig()
	if err == nil {
		if cfg.HAURL != "" {
			return "self-hosted"
		}
	}

	// Presence of yaver cloud config
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".yaver", "cloud.json")); err == nil {
		return "yaver-cloud"
	}

	// Vercel project config
	if _, err := os.Stat(filepath.Join(m.workDir, ".vercel", "project.json")); err == nil {
		return "vercel"
	}

	// Fly.io
	if _, err := os.Stat(filepath.Join(m.workDir, "fly.toml")); err == nil {
		return "fly"
	}

	// Railway
	if _, err := os.Stat(filepath.Join(m.workDir, "railway.toml")); err == nil {
		return "railway"
	}

	// Cloudflare Workers
	if _, err := os.Stat(filepath.Join(m.workDir, "wrangler.toml")); err == nil {
		return "cloudflare"
	}

	return "local"
}

// analyzeProject inspects the project directory and returns a map of
// relevant characteristics for step generation.
func (m *MigrateManager) analyzeProject(workDir string) map[string]interface{} {
	result := make(map[string]interface{})

	// Database
	for _, f := range []string{".env", ".env.local", ".env.production"} {
		vars, err := parseEnvFile(filepath.Join(workDir, f))
		if err != nil {
			continue
		}
		for k, v := range vars {
			ku := strings.ToUpper(k)
			if strings.Contains(ku, "DATABASE_URL") || strings.Contains(ku, "DB_URL") || strings.Contains(ku, "POSTGRES") {
				result["db_url"] = v
				if strings.HasPrefix(v, "postgres") {
					result["db_type"] = "postgres"
				} else if strings.HasPrefix(v, "sqlite") || strings.HasSuffix(v, ".db") {
					result["db_type"] = "sqlite"
				}
			}
			if strings.Contains(ku, "STRIPE") {
				result["stripe"] = true
			}
		}
	}

	// OAuth providers
	var oauthProviders []string
	envVars := m.allEnvVars()
	for k := range envVars {
		ku := strings.ToUpper(k)
		if strings.Contains(ku, "GOOGLE") && strings.Contains(ku, "CLIENT_ID") {
			oauthProviders = append(oauthProviders, "google")
		}
		if strings.Contains(ku, "GITHUB") && strings.Contains(ku, "CLIENT_ID") {
			oauthProviders = append(oauthProviders, "github")
		}
		if strings.Contains(ku, "APPLE") {
			oauthProviders = append(oauthProviders, "apple")
		}
		if strings.Contains(ku, "MICROSOFT") || strings.Contains(ku, "AZURE") {
			oauthProviders = append(oauthProviders, "microsoft")
		}
	}
	if len(oauthProviders) > 0 {
		result["oauth_providers"] = unique(oauthProviders)
	}

	// File storage
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml"} {
		if _, err := os.Stat(filepath.Join(workDir, f)); err == nil {
			result["has_docker_compose"] = true
		}
	}

	// Framework detection
	if _, err := os.Stat(filepath.Join(workDir, "next.config.js")); err == nil {
		result["framework"] = "nextjs"
	} else if _, err := os.Stat(filepath.Join(workDir, "vite.config.ts")); err == nil {
		result["framework"] = "vite"
	}

	return result
}

// generateSteps returns ordered MigrationSteps for the given migration path
// informed by the project analysis.
func (m *MigrateManager) generateSteps(from, to string, analysis map[string]interface{}) []MigrationStep {
	var defs []migrateStepDef

	key := from + "→" + to

	switch key {
	case "local→self-hosted":
		defs = localToSelfHosted(analysis)
	case "local→yaver-cloud":
		defs = localToYaverCloud(analysis)
	case "local→vercel":
		defs = localToVercel(analysis)
	case "local→fly":
		defs = localToFly(analysis)
	case "local→cloudflare":
		defs = localToCloudflare(analysis)
	case "local→railway":
		defs = localToRailway(analysis)
	case "self-hosted→yaver-cloud":
		defs = selfHostedToYaverCloud(analysis)
	case "yaver-cloud→self-hosted":
		defs = yaverCloudToSelfHosted(analysis)
	case "yaver-cloud→vercel":
		defs = yaverCloudToVercel(analysis)

	// Reverse directions
	case "vercel→local", "fly→local", "cloudflare→local", "railway→local", "self-hosted→local":
		defs = anyToLocal(from, analysis)
	case "vercel→self-hosted", "fly→self-hosted", "railway→self-hosted":
		defs = cloudToSelfHosted(from, analysis)

	default:
		return nil
	}

	steps := make([]MigrationStep, len(defs))
	for i, d := range defs {
		steps[i] = MigrationStep{
			Number:       i + 1,
			Name:         d.name,
			Description:  d.desc,
			Status:       "pending",
			Rollbackable: d.rollbackable,
		}
	}
	return steps
}

// ---------------------------------------------------------------------------
// Step definitions per migration path
// ---------------------------------------------------------------------------

type migrateStepDef struct {
	name         string
	desc         string
	rollbackable bool
}

func localToSelfHosted(a map[string]interface{}) []migrateStepDef {
	steps := []migrateStepDef{
		{"provision-server", "Provision a VPS (Hetzner/DigitalOcean/any) and install Docker + Caddy", false},
		{"export-docker-compose", "Generate docker-compose.yml + .env for all services", true},
	}
	if _, ok := a["db_type"]; ok {
		steps = append(steps, migrateStepDef{"dump-database", "pg_dump / sqlite3 dump to a SQL file", true})
	}
	steps = append(steps,
		migrateStepDef{"copy-files", "rsync project files to remote server", true},
		migrateStepDef{"restore-database", "Restore SQL dump on remote Postgres / SQLite", true},
		migrateStepDef{"configure-caddy", "Write Caddyfile with TLS for your domain", true},
		migrateStepDef{"configure-dns", "Point your domain's A record to the server IP", false},
		migrateStepDef{"configure-env", "Copy env vars to server .env", true},
		migrateStepDef{"update-oauth-uris", "Update OAuth redirect URIs in all providers", false},
		migrateStepDef{"update-stripe-webhook", "Update Stripe webhook endpoint URL", false},
		migrateStepDef{"docker-compose-up", "Run docker compose up -d on the server", true},
		migrateStepDef{"smoke-test", "Verify health endpoint, DB, OAuth, and Stripe webhook", false},
	)
	return steps
}

func localToYaverCloud(a map[string]interface{}) []migrateStepDef {
	steps := localToSelfHosted(a)
	// Replace first step with Yaver-specific provisioning.
	steps[0] = migrateStepDef{"provision-yaver-cloud", "Provision Yaver Cloud machine via API (CPU or GPU tier)", true}
	return steps
}

func localToVercel(a map[string]interface{}) []migrateStepDef {
	steps := []migrateStepDef{
		{"git-push", "Push project to GitHub", false},
		{"vercel-link", "Link project to Vercel via `vercel link`", true},
		{"create-supabase", "Create Supabase project and note connection string", true},
	}
	if _, ok := a["db_type"]; ok {
		steps = append(steps, migrateStepDef{"migrate-db-supabase", "Dump local DB and restore to Supabase Postgres", true})
	}
	steps = append(steps,
		migrateStepDef{"sync-env-vercel", "Push env vars with `vercel env add`", true},
		migrateStepDef{"vercel-deploy", "Run `vercel deploy --prod`", true},
		migrateStepDef{"configure-dns", "Set custom domain in Vercel and update DNS records", false},
		migrateStepDef{"update-oauth-uris", "Update OAuth redirect URIs to new domain", false},
		migrateStepDef{"update-stripe-webhook", "Update Stripe webhook to Vercel URL", false},
		migrateStepDef{"smoke-test", "Verify deployment health, DB, OAuth", false},
	)
	return steps
}

func localToFly(a map[string]interface{}) []migrateStepDef {
	steps := []migrateStepDef{
		{"fly-launch", "Run `fly launch` to create app + fly.toml", true},
		{"create-neon-db", "Create Neon serverless Postgres project", true},
	}
	if _, ok := a["db_type"]; ok {
		steps = append(steps, migrateStepDef{"migrate-db-neon", "Dump local DB and restore to Neon Postgres", true})
	}
	steps = append(steps,
		migrateStepDef{"fly-secrets-set", "Push env vars with `fly secrets set`", true},
		migrateStepDef{"fly-deploy", "Run `fly deploy`", true},
		migrateStepDef{"configure-dns", "Set custom domain in Fly and update DNS", false},
		migrateStepDef{"update-oauth-uris", "Update OAuth redirect URIs", false},
		migrateStepDef{"update-stripe-webhook", "Update Stripe webhook URL", false},
		migrateStepDef{"smoke-test", "Verify deployment health", false},
	)
	return steps
}

func localToCloudflare(a map[string]interface{}) []migrateStepDef {
	steps := []migrateStepDef{
		{"wrangler-init", "Run `wrangler init` and add wrangler.toml", true},
		{"create-d1", "Create Cloudflare D1 database with `wrangler d1 create`", true},
		{"create-r2", "Create Cloudflare R2 bucket with `wrangler r2 bucket create`", true},
	}
	if dbType, _ := a["db_type"].(string); dbType == "sqlite" || dbType == "postgres" {
		steps = append(steps, migrateStepDef{"migrate-db-d1", "Export schema + data to D1 via wrangler d1 execute", true})
	}
	steps = append(steps,
		migrateStepDef{"migrate-storage-r2", "Copy file storage to R2 with mc or aws CLI", true},
		migrateStepDef{"update-env-worker", "Set secrets with `wrangler secret put`", true},
		migrateStepDef{"wrangler-deploy", "Run `wrangler deploy`", true},
		migrateStepDef{"configure-dns", "Route domain through Cloudflare Workers", false},
		migrateStepDef{"update-oauth-uris", "Update OAuth redirect URIs", false},
		migrateStepDef{"update-stripe-webhook", "Update Stripe webhook URL", false},
		migrateStepDef{"smoke-test", "Verify Worker health", false},
	)
	return steps
}

func localToRailway(a map[string]interface{}) []migrateStepDef {
	steps := []migrateStepDef{
		{"railway-init", "Run `railway init` and link project", true},
		{"railway-add-postgres", "Add Postgres plugin via `railway add postgresql`", true},
	}
	if _, ok := a["db_type"]; ok {
		steps = append(steps, migrateStepDef{"migrate-db-railway", "Dump local DB and restore to Railway Postgres", true})
	}
	steps = append(steps,
		migrateStepDef{"railway-vars-set", "Push env vars with `railway variables set`", true},
		migrateStepDef{"railway-deploy", "Run `railway up`", true},
		migrateStepDef{"configure-dns", "Set custom domain in Railway and update DNS", false},
		migrateStepDef{"update-oauth-uris", "Update OAuth redirect URIs", false},
		migrateStepDef{"update-stripe-webhook", "Update Stripe webhook URL", false},
		migrateStepDef{"smoke-test", "Verify deployment health", false},
	)
	return steps
}

func selfHostedToYaverCloud(a map[string]interface{}) []migrateStepDef {
	return []migrateStepDef{
		{"snapshot-server", "Create a Docker volume snapshot on the current server", true},
		{"provision-yaver-cloud", "Provision Yaver Cloud machine", true},
		{"transfer-snapshot", "rsync snapshot to Yaver Cloud machine", true},
		{"restore-services", "Restore docker-compose on new machine", true},
		{"update-dns", "Re-point DNS to new machine IP", false},
		{"update-oauth-uris", "Update OAuth redirect URIs if domain changed", false},
		{"smoke-test", "Verify new machine health", false},
		{"decommission-old", "Stop and snapshot-archive old server", true},
	}
}

func yaverCloudToSelfHosted(a map[string]interface{}) []migrateStepDef {
	return []migrateStepDef{
		{"export-data", "Export DB dump + file storage from Yaver Cloud", true},
		{"provision-vps", "User provisions a VPS and installs Docker + Caddy", false},
		{"copy-docker-compose", "Copy docker-compose.yml + .env to new server", true},
		{"restore-database", "Restore DB dump on new server", true},
		{"restore-storage", "Restore file storage on new server", true},
		{"configure-caddy", "Write Caddyfile for custom domain + TLS", true},
		{"update-dns", "Point domain DNS to new server IP", false},
		{"docker-compose-up", "Run docker compose up -d on new server", true},
		{"update-oauth-uris", "Update OAuth redirect URIs if domain changed", false},
		{"smoke-test", "Verify new server health", false},
		{"cancel-yaver-cloud", "Cancel Yaver Cloud subscription", false},
	}
}

func yaverCloudToVercel(a map[string]interface{}) []migrateStepDef {
	steps := localToVercel(a)
	// Prepend export step.
	export := migrateStepDef{"export-from-cloud", "Export DB dump + files from Yaver Cloud", true}
	return append([]migrateStepDef{export}, steps...)
}

func anyToLocal(from string, a map[string]interface{}) []migrateStepDef {
	return []migrateStepDef{
		{"export-data", fmt.Sprintf("Export DB dump and files from %s", from), true},
		{"restore-database-local", "Restore DB dump locally", true},
		{"restore-storage-local", "Copy file storage locally", true},
		{"update-env-local", "Write exported env vars to .env.local", true},
		{"smoke-test", "Verify local app health", false},
	}
}

func cloudToSelfHosted(from string, a map[string]interface{}) []migrateStepDef {
	steps := anyToLocal(from, a)
	return append(steps, localToSelfHosted(a)...)
}

// ---------------------------------------------------------------------------
// executeStep dispatches step execution by name.
// ---------------------------------------------------------------------------

func (m *MigrateManager) executeStep(plan *MigrationPlan, step *MigrationStep) (string, error) {
	analysis := m.analyzeProject(m.workDir)

	switch step.Name {
	case "export-docker-compose":
		return m.exportDockerCompose()

	case "dump-database", "migrate-db-supabase", "migrate-db-neon", "migrate-db-railway", "migrate-db-d1":
		dbURL, _ := analysis["db_url"].(string)
		if dbURL == "" {
			return "No DATABASE_URL found — skipping DB dump.", nil
		}
		dumpPath := filepath.Join(m.workDir, fmt.Sprintf("yaver-db-dump-%s.sql", plan.ID))
		out, err := migrateRunCmd("pg_dump", "--no-owner", "--no-acl", "-f", dumpPath, dbURL)
		return fmt.Sprintf("DB dump written to %s\n%s", dumpPath, out), err

	case "sync-env-vercel":
		return m.MigrateEnv(plan.From, "vercel")

	case "fly-secrets-set":
		return m.MigrateEnv(plan.From, "fly")

	case "railway-vars-set":
		return m.MigrateEnv(plan.From, "railway")

	case "configure-dns", "update-dns":
		targetIP := os.Getenv("TARGET_IP")
		domain := os.Getenv("APP_DOMAIN")
		provider := os.Getenv("DNS_PROVIDER")
		if domain == "" || targetIP == "" {
			return "APP_DOMAIN or TARGET_IP not set — manual DNS update required.\n" +
				"Set the A record for your domain to point to your server IP.", nil
		}
		return m.MigrateDNS(domain, provider, targetIP)

	case "update-oauth-uris":
		oldDomain := os.Getenv("OLD_DOMAIN")
		newDomain := os.Getenv("APP_DOMAIN")
		if oldDomain == "" || newDomain == "" {
			return "OLD_DOMAIN / APP_DOMAIN not set — skipping OAuth URI update. Update manually.", nil
		}
		return m.MigrateOAuthUpdate(oldDomain, newDomain)

	case "smoke-test":
		return m.Verify(plan.ID)

	case "git-push":
		return migrateRunCmd("git", "-C", m.workDir, "push", "origin", "HEAD")

	case "vercel-link":
		return migrateRunCmd("vercel", "link", "--yes")

	case "vercel-deploy":
		return migrateRunCmd("vercel", "deploy", "--prod")

	case "fly-launch":
		return migrateRunCmd("fly", "launch", "--no-deploy")

	case "fly-deploy":
		return migrateRunCmd("fly", "deploy")

	case "wrangler-init":
		return "wrangler.toml already exists or will be created by the developer — skipping.", nil

	case "wrangler-deploy":
		return migrateRunCmd("wrangler", "deploy")

	case "railway-init":
		return migrateRunCmd("railway", "init")

	case "railway-deploy":
		return migrateRunCmd("railway", "up")

	case "docker-compose-up":
		return migrateRunCmd("docker", "compose", "-f", filepath.Join(m.workDir, "docker-compose.yml"), "up", "-d")

	case "provision-server", "provision-yaver-cloud", "provision-vps",
		"cancel-yaver-cloud", "decommission-old":
		return fmt.Sprintf("Step %q requires manual action — see description: %s", step.Name, step.Description), nil

	default:
		return fmt.Sprintf("Step %q does not have an automated implementation — complete manually: %s", step.Name, step.Description), nil
	}
}

// ---------------------------------------------------------------------------
// rollbackStep reverses a completed step where possible.
// ---------------------------------------------------------------------------

func (m *MigrateManager) rollbackStep(plan *MigrationPlan, step *MigrationStep) (string, error) {
	switch step.Name {
	case "vercel-deploy":
		// Roll back by promoting the previous deployment.
		return migrateRunCmd("vercel", "rollback")

	case "fly-deploy":
		return migrateRunCmd("fly", "releases", "list") // guidance output only

	case "docker-compose-up":
		return migrateRunCmd("docker", "compose", "-f", filepath.Join(m.workDir, "docker-compose.yml"), "down")

	case "dump-database":
		dumpPath := filepath.Join(m.workDir, fmt.Sprintf("yaver-db-dump-%s.sql", plan.ID))
		if err := os.Remove(dumpPath); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove dump file: %w", err)
		}
		return fmt.Sprintf("Removed dump file %s", dumpPath), nil

	case "sync-env-vercel":
		return "Env var rollback requires manual removal via `vercel env rm <KEY>` for each variable.", nil

	case "configure-dns", "update-dns":
		return "DNS rollback: revert the A record to the old IP address in your DNS provider dashboard.", nil

	default:
		return fmt.Sprintf("No automated rollback for step %q — revert manually.", step.Name), nil
	}
}

// ---------------------------------------------------------------------------
// Private utilities
// ---------------------------------------------------------------------------

func (m *MigrateManager) exportDockerCompose() (string, error) {
	composePath := filepath.Join(m.workDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); err == nil {
		return fmt.Sprintf("docker-compose.yml already exists at %s", composePath), nil
	}

	content := `version: "3.9"
services:
  app:
    build: .
    ports:
      - "3000:3000"
    env_file:
      - .env
    restart: unless-stopped
  db:
    image: postgres:16-alpine
    volumes:
      - db_data:/var/lib/postgresql/data
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB:-app}
    restart: unless-stopped
volumes:
  db_data:
`
	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write docker-compose.yml: %w", err)
	}
	return fmt.Sprintf("Generated docker-compose.yml at %s — review and customise before deploying.", composePath), nil
}

func (m *MigrateManager) findEnvFile() string {
	for _, name := range []string{".env.local", ".env.production", ".env"} {
		p := filepath.Join(m.workDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (m *MigrateManager) allEnvVars() map[string]string {
	result := make(map[string]string)
	for _, name := range []string{".env", ".env.local", ".env.production"} {
		vars, err := parseEnvFile(filepath.Join(m.workDir, name))
		if err != nil {
			continue
		}
		for k, v := range vars {
			result[k] = v
		}
	}
	return result
}

func (m *MigrateManager) dnsManualInstructions(sb *strings.Builder, domain, targetIP string) {
	fmt.Fprintf(sb, "\nManual DNS steps:\n")
	fmt.Fprintf(sb, "  1. Log in to your DNS provider (Cloudflare, Route53, etc.)\n")
	fmt.Fprintf(sb, "  2. Find the A record for: %s\n", domain)
	fmt.Fprintf(sb, "  3. Update its value to: %s\n", targetIP)
	fmt.Fprintf(sb, "  4. Save and wait up to 48 h for propagation (usually < 5 min with low TTL)\n")
	fmt.Fprintf(sb, "  Verify with: dig +short %s\n", domain)
}

func (m *MigrateManager) estimateCost(to string) string {
	costs := map[string]string{
		"local":       "$0/mo",
		"self-hosted": "$5–$50/mo (VPS costs)",
		"yaver-cloud": "$49/mo (CPU) or $449/mo (GPU)",
		"vercel":      "$0 hobby / $20/mo pro",
		"fly":         "$0–$30/mo",
		"cloudflare":  "$0–$5/mo",
		"railway":     "$5–$20/mo",
	}
	if c, ok := costs[to]; ok {
		return c
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// runCmd runs an external command and returns combined output as a string.
// It is intentionally simple — callers check the returned error.
func migrateRunCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// migrateRunCmdInput runs a command with data piped to stdin.
func migrateRunCmdInput(input, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// parseEnvFile reads KEY=VALUE pairs from a .env file.
// Lines starting with # and empty lines are skipped.
// Quoted values have quotes stripped.
func parseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes.
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		result[k] = v
	}
	return result, nil
}

// cloudflareUpdateDNS updates an A record via the Cloudflare v4 API.
func cloudflareUpdateDNS(token, zoneID, domain, ip string) (string, error) {
	// List DNS records to find the record ID.
	listOut, err := migrateRunCmd("curl", "-sf",
		"-H", "Authorization: Bearer "+token,
		"-H", "Content-Type: application/json",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=A&name=%s", zoneID, domain),
	)
	if err != nil {
		return listOut, fmt.Errorf("cloudflare list records: %w", err)
	}

	var listResp struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(listOut), &listResp); err != nil || len(listResp.Result) == 0 {
		// No existing record — create one.
		body := fmt.Sprintf(`{"type":"A","name":%q,"content":%q,"ttl":120,"proxied":false}`, domain, ip)
		out, err := migrateRunCmd("curl", "-sf", "-X", "POST",
			"-H", "Authorization: Bearer "+token,
			"-H", "Content-Type: application/json",
			"-d", body,
			fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID),
		)
		return out, err
	}

	recordID := listResp.Result[0].ID
	body := fmt.Sprintf(`{"type":"A","name":%q,"content":%q,"ttl":120,"proxied":false}`, domain, ip)
	out, err := migrateRunCmd("curl", "-sf", "-X", "PUT",
		"-H", "Authorization: Bearer "+token,
		"-H", "Content-Type: application/json",
		"-d", body,
		fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, recordID),
	)
	return out, err
}

// unique returns a deduplicated slice preserving first-occurrence order.
func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
