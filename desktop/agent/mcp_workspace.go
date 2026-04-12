package main

// getWorkspaceMCPTools returns MCP tool definitions for all workspace features.
func getWorkspaceMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		// --- Services (Docker stack) ---
		{
			"name":        "services_start",
			"description": "Start all or specific local development services (Postgres, Redis, MinIO, etc.) from .yaver/services.yaml.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"names": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Service names to start (empty = all)"},
				},
			},
		},
		{
			"name":        "services_stop",
			"description": "Stop all or specific local development services.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"names": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Service names to stop (empty = all)"},
				},
			},
		},
		{
			"name":        "services_status",
			"description": "Show status of all configured local services (running, port, health, memory).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "services_logs",
			"description": "Tail logs from a service.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name":  map[string]interface{}{"type": "string", "description": "Service name"},
					"lines": map[string]interface{}{"type": "integer", "description": "Number of lines (default: 50)"},
				},
			},
		},
		{
			"name":        "services_add",
			"description": "Add a service to the local stack. Presets: postgres, redis, minio, mailpit, umami, posthog, logto, meili, typesense.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name":  map[string]interface{}{"type": "string", "description": "Service name (use preset name for defaults)"},
					"image": map[string]interface{}{"type": "string", "description": "Docker image (optional if using preset)"},
					"port":  map[string]interface{}{"type": "integer", "description": "Port (optional if using preset)"},
				},
			},
		},
		{
			"name":        "services_remove",
			"description": "Remove a service from the local stack.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Service name"},
				},
			},
		},
		// --- Proxy ---
		{
			"name":        "proxy_start",
			"description": "Start local reverse proxy (Caddy) with HTTPS for local development.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "proxy_stop",
			"description": "Stop the local reverse proxy.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "proxy_add",
			"description": "Add a reverse proxy route (e.g. myapp.local → localhost:3000).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain", "target"},
				"properties": map[string]interface{}{
					"domain": map[string]interface{}{"type": "string", "description": "Local domain (e.g. myapp.local)"},
					"target": map[string]interface{}{"type": "string", "description": "Target (e.g. localhost:3000)"},
					"tls":    map[string]interface{}{"type": "boolean", "description": "Enable HTTPS (default: true)"},
				},
			},
		},
		{
			"name":        "proxy_remove",
			"description": "Remove a reverse proxy route.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain"},
				"properties": map[string]interface{}{
					"domain": map[string]interface{}{"type": "string", "description": "Domain to remove"},
				},
			},
		},
		{
			"name":        "proxy_list",
			"description": "List all reverse proxy routes.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "proxy_status",
			"description": "Show reverse proxy status (running, routes, Caddy PID).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- DNS ---
		{
			"name":        "dns_add",
			"description": "Add a local DNS entry to /etc/hosts.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"hostname"},
				"properties": map[string]interface{}{
					"hostname": map[string]interface{}{"type": "string", "description": "Hostname (e.g. myapp.local)"},
					"ip":       map[string]interface{}{"type": "string", "description": "IP address (default: 127.0.0.1)"},
				},
			},
		},
		{
			"name":        "dns_remove",
			"description": "Remove a yaver-managed DNS entry from /etc/hosts.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"hostname"},
				"properties": map[string]interface{}{
					"hostname": map[string]interface{}{"type": "string", "description": "Hostname to remove"},
				},
			},
		},
		{
			"name":        "dns_list",
			"description": "List all yaver-managed DNS entries.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "dns_flush",
			"description": "Remove all yaver-managed DNS entries and flush DNS cache.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- Storage (MinIO) ---
		{
			"name":        "storage_start",
			"description": "Start local S3-compatible storage (MinIO).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"port": map[string]interface{}{"type": "integer", "description": "API port (default: 9000)"},
				},
			},
		},
		{
			"name":        "storage_stop",
			"description": "Stop local MinIO storage.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "storage_status",
			"description": "Show MinIO status (running, endpoint, buckets, total size).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "storage_bucket_create",
			"description": "Create an S3 bucket.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Bucket name"},
				},
			},
		},
		{
			"name":        "storage_bucket_list",
			"description": "List all S3 buckets.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "storage_upload",
			"description": "Upload a file to S3 storage.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"bucket", "key", "file"},
				"properties": map[string]interface{}{
					"bucket": map[string]interface{}{"type": "string"},
					"key":    map[string]interface{}{"type": "string", "description": "Object key (path)"},
					"file":   map[string]interface{}{"type": "string", "description": "Local file path"},
				},
			},
		},
		{
			"name":        "storage_list",
			"description": "List objects in an S3 bucket.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"bucket"},
				"properties": map[string]interface{}{
					"bucket": map[string]interface{}{"type": "string"},
					"prefix": map[string]interface{}{"type": "string", "description": "Key prefix filter"},
				},
			},
		},
		{
			"name":        "storage_presign",
			"description": "Generate a presigned URL for an S3 object.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"bucket", "key"},
				"properties": map[string]interface{}{
					"bucket": map[string]interface{}{"type": "string"},
					"key":    map[string]interface{}{"type": "string"},
					"expiry": map[string]interface{}{"type": "string", "description": "Expiry duration (e.g. 1h, 24h)"},
				},
			},
		},
		{
			"name":        "storage_config",
			"description": "Get S3-compatible config for app integration (endpoint, access key, secret key).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- Mock Server ---
		{
			"name":        "mock_start",
			"description": "Start an API mock server.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"port": map[string]interface{}{"type": "integer", "description": "Port (default: 9999)"},
				},
			},
		},
		{
			"name":        "mock_stop",
			"description": "Stop the mock server.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "mock_add",
			"description": "Add a mock route.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"method", "path", "status", "body"},
				"properties": map[string]interface{}{
					"method":  map[string]interface{}{"type": "string", "description": "HTTP method"},
					"path":    map[string]interface{}{"type": "string", "description": "URL path"},
					"status":  map[string]interface{}{"type": "integer", "description": "Response status code"},
					"body":    map[string]interface{}{"type": "string", "description": "Response body (JSON)"},
					"headers": map[string]interface{}{"type": "object", "description": "Response headers"},
				},
			},
		},
		{
			"name":        "mock_list",
			"description": "List all mock routes.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "mock_reset",
			"description": "Clear all mock routes and recordings.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "mock_preset",
			"description": "Load a mock preset (stripe, openai, twilio, github, supabase-auth).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Preset name", "enum": []string{"stripe", "openai", "twilio", "github", "supabase-auth"}},
				},
			},
		},
		{
			"name":        "mock_record",
			"description": "Start recording mode — capture real API responses as mock routes.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "mock_openapi",
			"description": "Load mock routes from an OpenAPI spec file.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"spec_path"},
				"properties": map[string]interface{}{
					"spec_path": map[string]interface{}{"type": "string", "description": "Path to OpenAPI YAML/JSON file"},
				},
			},
		},
		// --- Pre-deployment Checks ---
		{
			"name":        "check_run",
			"description": "Run all pre-deployment checks (typecheck, lint, format, tests, build, bundle size, security audit, env vars, git clean).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"fix":  map[string]interface{}{"type": "boolean", "description": "Auto-fix lint/format issues"},
					"skip": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Check names to skip"},
				},
			},
		},
		{
			"name":        "check_single",
			"description": "Run a single pre-deployment check.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Check name: git-clean, typecheck, lint, format, tests, build, bundle-size, security-audit, env-vars, deps"},
					"fix":  map[string]interface{}{"type": "boolean", "description": "Auto-fix if possible"},
				},
			},
		},
		// --- Performance Testing ---
		{
			"name":        "perf_lighthouse",
			"description": "Run a Lighthouse audit on a URL. Returns performance, accessibility, best practices, SEO scores and Core Web Vitals.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"url"},
				"properties": map[string]interface{}{
					"url":    map[string]interface{}{"type": "string", "description": "URL to audit"},
					"device": map[string]interface{}{"type": "string", "description": "Device: mobile or desktop (default: mobile)", "enum": []string{"mobile", "desktop"}},
				},
			},
		},
		{
			"name":        "perf_loadtest",
			"description": "Run a load test on a URL. Returns RPS, latency percentiles, error rate.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"url"},
				"properties": map[string]interface{}{
					"url":         map[string]interface{}{"type": "string"},
					"requests":    map[string]interface{}{"type": "integer", "description": "Total requests (default: 1000)"},
					"concurrency": map[string]interface{}{"type": "integer", "description": "Concurrent requests (default: 10)"},
					"duration":    map[string]interface{}{"type": "string", "description": "Duration (e.g. 30s)"},
				},
			},
		},
		{
			"name":        "perf_compare",
			"description": "Compare current Lighthouse scores against the last saved result.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"url"},
				"properties": map[string]interface{}{
					"url": map[string]interface{}{"type": "string"},
				},
			},
		},
		// --- Database Lifecycle ---
		{
			"name":        "db_migrate",
			"description": "Run pending database migrations. Auto-detects ORM (Drizzle, Prisma, Goose, Alembic).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{"type": "string", "description": "Target: local or production (default: local)"},
				},
			},
		},
		{
			"name":        "db_generate",
			"description": "Generate a new migration from schema changes.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Migration name"},
				},
			},
		},
		{
			"name":        "db_push",
			"description": "Push schema directly to database (dev only, skips migration files).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "db_seed",
			"description": "Run database seed file.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "db_reset",
			"description": "Drop all tables, re-migrate, and re-seed. Requires force=true.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"force": map[string]interface{}{"type": "boolean", "description": "Confirm destructive reset"},
				},
			},
		},
		{
			"name":        "db_studio",
			"description": "Open database GUI (Drizzle Studio or Prisma Studio).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"port": map[string]interface{}{"type": "integer", "description": "Port for studio UI"},
				},
			},
		},
		{
			"name":        "db_backup",
			"description": "Backup database (pg_dump / SQLite copy / mysqldump).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"db_url": map[string]interface{}{"type": "string", "description": "Database URL (auto-detected from .env if omitted)"},
				},
			},
		},
		{
			"name":        "db_restore",
			"description": "Restore database from a backup file.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"backup_path"},
				"properties": map[string]interface{}{
					"backup_path": map[string]interface{}{"type": "string"},
					"db_url":      map[string]interface{}{"type": "string", "description": "Target database URL"},
				},
			},
		},
		{
			"name":        "db_status",
			"description": "Show database migration status (pending/applied migrations, ORM engine).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- Preview Environments ---
		{
			"name":        "preview_create",
			"description": "Create a branch preview environment (git worktree + build + serve).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"branch"},
				"properties": map[string]interface{}{
					"branch": map[string]interface{}{"type": "string"},
					"port":   map[string]interface{}{"type": "integer", "description": "Port (auto-assigned if omitted)"},
					"expose": map[string]interface{}{"type": "boolean", "description": "Create public tunnel URL"},
				},
			},
		},
		{
			"name":        "preview_list",
			"description": "List all active preview environments.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "preview_stop",
			"description": "Stop a preview environment.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"branch"},
				"properties": map[string]interface{}{
					"branch": map[string]interface{}{"type": "string", "description": "Branch name or preview ID"},
				},
			},
		},
		{
			"name":        "preview_stop_all",
			"description": "Stop all preview environments.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- OAuth Wizard ---
		{
			"name":        "auth_oauth_setup",
			"description": "Get step-by-step OAuth provider setup guide (Google, Apple, Microsoft, GitHub).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"provider"},
				"properties": map[string]interface{}{
					"provider": map[string]interface{}{"type": "string", "description": "OAuth provider", "enum": []string{"google", "apple", "microsoft", "github"}},
				},
			},
		},
		{
			"name":        "auth_oauth_save",
			"description": "Save OAuth credentials to .env.local.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"provider", "credentials"},
				"properties": map[string]interface{}{
					"provider":    map[string]interface{}{"type": "string"},
					"credentials": map[string]interface{}{"type": "object", "description": "Key-value pairs (e.g. GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET)"},
				},
			},
		},
		{
			"name":        "auth_oauth_test",
			"description": "Test an OAuth flow end-to-end.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"provider"},
				"properties": map[string]interface{}{
					"provider": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "auth_oauth_list",
			"description": "List all OAuth providers with their configuration status.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "auth_oauth_migrate",
			"description": "Generate instructions for updating OAuth redirect URIs when changing domains.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"old_domain", "new_domain"},
				"properties": map[string]interface{}{
					"old_domain": map[string]interface{}{"type": "string"},
					"new_domain": map[string]interface{}{"type": "string"},
				},
			},
		},
		// --- Cloud Deploy ---
		{
			"name":        "cloud_deploy",
			"description": "Deploy app to Yaver Cloud (managed Hetzner VPS). Provisions server, deploys Docker containers, sets up SSL/DNS/backups.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"plan"},
				"properties": map[string]interface{}{
					"plan":   map[string]interface{}{"type": "string", "description": "Plan: starter ($9/mo), pro ($19/mo), scale ($29/mo)", "enum": []string{"starter", "pro", "scale"}},
					"region": map[string]interface{}{"type": "string", "description": "Region: eu, us, asia (default: closest)"},
					"name":   map[string]interface{}{"type": "string", "description": "App name"},
					"domain": map[string]interface{}{"type": "string", "description": "Custom domain"},
				},
			},
		},
		{
			"name":        "cloud_status",
			"description": "Show Yaver Cloud deployment status (containers, CPU, memory, SSL, backups).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "cloud_logs",
			"description": "Get logs from a cloud-deployed app.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app":   map[string]interface{}{"type": "string", "description": "App name"},
					"lines": map[string]interface{}{"type": "integer", "description": "Number of lines (default: 100)"},
				},
			},
		},
		{
			"name":        "cloud_redeploy",
			"description": "Rebuild and redeploy from latest code.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "cloud_scale",
			"description": "Change Yaver Cloud plan tier.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"plan"},
				"properties": map[string]interface{}{
					"plan": map[string]interface{}{"type": "string", "enum": []string{"starter", "pro", "scale"}},
				},
			},
		},
		{
			"name":        "cloud_backup",
			"description": "Trigger a manual cloud backup.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "cloud_destroy",
			"description": "Tear down Yaver Cloud deployment (exports data first).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"confirm": map[string]interface{}{"type": "boolean", "description": "Must be true to proceed"},
				},
			},
		},
		{
			"name":        "cloud_plans",
			"description": "List available Yaver Cloud plans with specs and pricing.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- Migration ---
		{
			"name":        "migrate_plan",
			"description": "Generate a migration plan between tiers (local, self-hosted, yaver-cloud, vercel, fly, cloudflare, railway).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"to"},
				"properties": map[string]interface{}{
					"from": map[string]interface{}{"type": "string", "description": "Source tier (auto-detected if omitted)"},
					"to":   map[string]interface{}{"type": "string", "description": "Target tier"},
				},
			},
		},
		{
			"name":        "migrate_run",
			"description": "Execute a migration plan (all steps or a specific step).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"plan_id"},
				"properties": map[string]interface{}{
					"plan_id": map[string]interface{}{"type": "string"},
					"step":    map[string]interface{}{"type": "integer", "description": "Specific step number (0 = all remaining)"},
				},
			},
		},
		{
			"name":        "migrate_status",
			"description": "Show current migration plan status.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "migrate_rollback",
			"description": "Rollback a migration step.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"plan_id", "step"},
				"properties": map[string]interface{}{
					"plan_id": map[string]interface{}{"type": "string"},
					"step":    map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "migrate_targets",
			"description": "List all supported migration targets with pros, cons, and costs.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "migrate_verify",
			"description": "Run smoke tests after migration to verify everything works.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"plan_id"},
				"properties": map[string]interface{}{
					"plan_id": map[string]interface{}{"type": "string"},
				},
			},
		},
		// --- Remote Machine ---
		{
			"name":        "remote_setup",
			"description": "First-time setup of a remote dev machine (installs Docker, Node.js, Git, Yaver agent).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"host"},
				"properties": map[string]interface{}{
					"host": map[string]interface{}{"type": "string", "description": "IP or hostname"},
					"user": map[string]interface{}{"type": "string", "description": "SSH user (default: root)"},
				},
			},
		},
		{
			"name":        "remote_status",
			"description": "Dashboard of all remote machines (online/offline, CPU, memory, disk, containers).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "remote_provision",
			"description": "Spin up a new VPS from phone. Supports Hetzner and DigitalOcean.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"provider", "size"},
				"properties": map[string]interface{}{
					"provider": map[string]interface{}{"type": "string", "enum": []string{"hetzner", "digitalocean"}},
					"size":     map[string]interface{}{"type": "string", "enum": []string{"small", "medium", "large"}},
					"region":   map[string]interface{}{"type": "string", "description": "Region (default: closest)"},
				},
			},
		},
		{
			"name":        "remote_destroy",
			"description": "Destroy a remote VPS.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"machine_id"},
				"properties": map[string]interface{}{
					"machine_id": map[string]interface{}{"type": "string"},
					"confirm":    map[string]interface{}{"type": "boolean", "description": "Must be true"},
				},
			},
		},
		{
			"name":        "remote_cost",
			"description": "Show monthly cost across all remote machines.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "remote_exec",
			"description": "Execute a command on a remote machine.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"machine_id", "command"},
				"properties": map[string]interface{}{
					"machine_id": map[string]interface{}{"type": "string"},
					"command":    map[string]interface{}{"type": "string"},
				},
			},
		},
		// --- Scale ---
		{
			"name":        "scale_check",
			"description": "Analyze resource usage and generate scaling recommendations.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "scale_plan",
			"description": "Preview what upgrading/downgrading a plan would change.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"plan"},
				"properties": map[string]interface{}{
					"plan": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "scale_cdn",
			"description": "Add CDN in front of your app (Cloudflare or Bunny).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain"},
				"properties": map[string]interface{}{
					"domain":   map[string]interface{}{"type": "string"},
					"provider": map[string]interface{}{"type": "string", "description": "CDN provider (default: cloudflare)"},
				},
			},
		},
		{
			"name":        "scale_cache",
			"description": "Add Redis caching layer.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"backend": map[string]interface{}{"type": "string", "description": "Cache backend: redis, keydb, dragonfly (default: redis)"},
					"port":    map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "scale_optimize",
			"description": "Run automatic performance optimizations (compression, caching, DB tuning).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- PocketBase Backend ---
		{
			"name":        "backend_start",
			"description": "Start PocketBase backend (built-in DB + auth + storage + realtime).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mode": map[string]interface{}{"type": "string", "description": "Mode: standalone, docker (default: standalone)", "enum": []string{"standalone", "docker", "embedded"}},
					"port": map[string]interface{}{"type": "integer", "description": "Port (default: 8090)"},
				},
			},
		},
		{
			"name":        "backend_stop",
			"description": "Stop PocketBase backend.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "backend_status",
			"description": "Show PocketBase status (running, collections, users, storage).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "backend_collections",
			"description": "List all PocketBase collections (tables).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "backend_collection_create",
			"description": "Create a PocketBase collection.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name":   map[string]interface{}{"type": "string"},
					"type":   map[string]interface{}{"type": "string", "description": "Collection type: base, auth, view (default: base)"},
					"schema": map[string]interface{}{"type": "array", "description": "Array of field definitions"},
				},
			},
		},
		{
			"name":        "backend_records",
			"description": "Query PocketBase records.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"collection"},
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string"},
					"filter":     map[string]interface{}{"type": "string"},
					"sort":       map[string]interface{}{"type": "string"},
					"limit":      map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "backend_users",
			"description": "Manage PocketBase auth users.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"action"},
				"properties": map[string]interface{}{
					"action":   map[string]interface{}{"type": "string", "enum": []string{"list", "create", "delete"}},
					"email":    map[string]interface{}{"type": "string"},
					"password": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "backend_backup",
			"description": "Backup PocketBase data.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"output": map[string]interface{}{"type": "string", "description": "Output path"},
				},
			},
		},
		{
			"name":        "backend_setup",
			"description": "Get PocketBase SDK integration code for your framework.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"framework": map[string]interface{}{"type": "string", "description": "Framework: javascript, dart, react"},
				},
			},
		},
		// --- Platform (Self-hosted PaaS) ---
		{
			"name":        "platform_init",
			"description": "Initialize Yaver Platform on current machine or remote (self-hosted PaaS like Vercel).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mode":   map[string]interface{}{"type": "string", "description": "Mode: local, relay, vps", "enum": []string{"local", "relay", "vps"}},
					"domain": map[string]interface{}{"type": "string", "description": "Custom domain"},
				},
			},
		},
		{
			"name":        "platform_deploy",
			"description": "Deploy an app to Yaver Platform.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"directory"},
				"properties": map[string]interface{}{
					"directory": map[string]interface{}{"type": "string", "description": "Project directory"},
					"name":      map[string]interface{}{"type": "string", "description": "App name"},
					"domain":    map[string]interface{}{"type": "string", "description": "Custom domain or subdomain"},
				},
			},
		},
		{
			"name":        "platform_redeploy",
			"description": "Rebuild and redeploy an app.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"app"},
				"properties": map[string]interface{}{
					"app": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "platform_apps",
			"description": "List all deployed apps with domains, status, and resource usage.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "platform_logs",
			"description": "Tail logs from a deployed app.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"app"},
				"properties": map[string]interface{}{
					"app":   map[string]interface{}{"type": "string"},
					"lines": map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "platform_remove",
			"description": "Remove a deployed app.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"app"},
				"properties": map[string]interface{}{
					"app": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "platform_status",
			"description": "Show platform health (running apps, SSL status, memory, CPU, uptime).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "platform_preview",
			"description": "Create a preview deploy from a branch.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"branch", "app"},
				"properties": map[string]interface{}{
					"branch": map[string]interface{}{"type": "string"},
					"app":    map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "platform_webhook",
			"description": "Set up GitHub/GitLab push-to-deploy webhook.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"repo", "app"},
				"properties": map[string]interface{}{
					"repo":   map[string]interface{}{"type": "string", "description": "Repository URL"},
					"branch": map[string]interface{}{"type": "string", "description": "Branch to watch (default: main)"},
					"app":    map[string]interface{}{"type": "string"},
				},
			},
		},
		// --- Domain/DNS/HTTPS ---
		{
			"name":        "domain_setup",
			"description": "Full domain setup wizard — detects IP type, guides DNS, configures SSL.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain"},
				"properties": map[string]interface{}{
					"domain":   map[string]interface{}{"type": "string"},
					"provider": map[string]interface{}{"type": "string", "description": "DNS provider: cloudflare, manual (default: manual)"},
				},
			},
		},
		{
			"name":        "domain_add",
			"description": "Add domain routing (map domain to an app or port).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain", "target"},
				"properties": map[string]interface{}{
					"domain": map[string]interface{}{"type": "string"},
					"target": map[string]interface{}{"type": "string", "description": "App name or port"},
					"path":   map[string]interface{}{"type": "string", "description": "Path prefix (default: /)"},
				},
			},
		},
		{
			"name":        "domain_list",
			"description": "List all domains with SSL status and routing.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "domain_ssl_status",
			"description": "Show SSL certificate status for all domains.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "domain_dns_check",
			"description": "Verify DNS records are correct for a domain.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain"},
				"properties": map[string]interface{}{
					"domain": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "domain_detect_ip",
			"description": "Detect if machine has a public IP (static_public, dynamic_public, or private_nat).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "domain_ddns_start",
			"description": "Start dynamic DNS updater (auto-update Cloudflare DNS when IP changes).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"domain"},
				"properties": map[string]interface{}{
					"domain":   map[string]interface{}{"type": "string"},
					"provider": map[string]interface{}{"type": "string", "description": "DNS provider (default: cloudflare)"},
					"interval": map[string]interface{}{"type": "string", "description": "Check interval (default: 5m)"},
				},
			},
		},
		// --- Site Generator (WordPress killer) ---
		{
			"name":        "site_create",
			"description": "Create a new static site (landing page, blog, docs, changelog).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name":      map[string]interface{}{"type": "string"},
					"type":      map[string]interface{}{"type": "string", "description": "Site type: landing, blog, docs, changelog (default: landing)"},
					"framework": map[string]interface{}{"type": "string", "description": "Framework: astro, next-static, 11ty (default: astro)"},
				},
			},
		},
		{
			"name":        "site_generate",
			"description": "AI-generate a complete landing page with hero, features, pricing, CTA.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"product_name", "tagline"},
				"properties": map[string]interface{}{
					"product_name": map[string]interface{}{"type": "string"},
					"tagline":      map[string]interface{}{"type": "string"},
					"features":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Feature descriptions"},
					"cta":          map[string]interface{}{"type": "string", "description": "Call-to-action text"},
					"style":        map[string]interface{}{"type": "string", "description": "Style: minimal, bold, corporate, playful", "enum": []string{"minimal", "bold", "corporate", "playful"}},
				},
			},
		},
		{
			"name":        "site_build",
			"description": "Build the static site.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "site_serve",
			"description": "Serve the site locally for preview.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"port": map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "site_deploy",
			"description": "Deploy the site to Yaver Platform.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "site_pages",
			"description": "List all pages in the site.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "site_page_add",
			"description": "Add a page to the site.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"slug", "title"},
				"properties": map[string]interface{}{
					"slug":    map[string]interface{}{"type": "string"},
					"title":   map[string]interface{}{"type": "string"},
					"content": map[string]interface{}{"type": "string", "description": "Markdown content"},
				},
			},
		},
		{
			"name":        "site_blog_post",
			"description": "Create a blog post.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"title"},
				"properties": map[string]interface{}{
					"title":   map[string]interface{}{"type": "string"},
					"content": map[string]interface{}{"type": "string"},
					"tags":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"draft":   map[string]interface{}{"type": "boolean"},
				},
			},
		},
		{
			"name":        "site_blog_list",
			"description": "List all blog posts.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// --- Form Handler ---
		{
			"name":        "form_create",
			"description": "Create a form endpoint (contact, newsletter, lead capture).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name", "fields"},
				"properties": map[string]interface{}{
					"name":         map[string]interface{}{"type": "string"},
					"fields":       map[string]interface{}{"type": "array", "description": "Field definitions [{name, type, required, label}]"},
					"notify_email": map[string]interface{}{"type": "string"},
					"redirect_url": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "form_list",
			"description": "List all forms with submission counts.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "form_submissions",
			"description": "List submissions for a form.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"form"},
				"properties": map[string]interface{}{
					"form": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "form_export",
			"description": "Export form submissions as CSV.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"form"},
				"properties": map[string]interface{}{
					"form": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "form_component",
			"description": "Generate a frontend form component (React, Astro, or HTML).",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"form"},
				"properties": map[string]interface{}{
					"form":      map[string]interface{}{"type": "string"},
					"framework": map[string]interface{}{"type": "string", "description": "react, astro, or html"},
				},
			},
		},
		// --- SEO ---
		{
			"name":        "seo_audit",
			"description": "Run a full SEO audit on the site (meta tags, headings, images, sitemap, schema, speed).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "seo_fix",
			"description": "Auto-fix SEO issues.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"fix": map[string]interface{}{"type": "string", "description": "What to fix: all, meta, images, sitemap, schema, robots (default: all)"},
				},
			},
		},
		{
			"name":        "seo_report",
			"description": "Generate an SEO score report.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "seo_sitemap",
			"description": "Generate or regenerate sitemap.xml.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "seo_schema",
			"description": "Add structured data (JSON-LD) to a page.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"page", "type"},
				"properties": map[string]interface{}{
					"page": map[string]interface{}{"type": "string", "description": "Page slug"},
					"type": map[string]interface{}{"type": "string", "description": "Schema type: article, product, faq, organization, local-business, breadcrumb"},
				},
			},
		},
		// --- CMS ---
		{
			"name":        "cms_start",
			"description": "Start a headless CMS (Keystatic, Tina, Decap, or PocketBase).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"engine": map[string]interface{}{"type": "string", "description": "CMS engine: keystatic, tina, decap, pocketbase (default: keystatic)"},
					"port":   map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "cms_stop",
			"description": "Stop the CMS.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "cms_status",
			"description": "Show CMS status and admin URL.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "cms_content",
			"description": "List content entries in a collection.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"collection"},
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string"},
				},
			},
		},
		// --- Templates ---
		{
			"name":        "template_list",
			"description": "List available full-stack SaaS templates (saas-complete, indie-hacker, api-first, content-site).",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "template_use",
			"description": "Apply a full-stack template to create a new project.",
			"inputSchema": map[string]interface{}{
				"type": "object", "required": []string{"name"},
				"properties": map[string]interface{}{
					"name":         map[string]interface{}{"type": "string", "description": "Template name", "enum": []string{"saas-complete", "indie-hacker", "api-first", "content-site"}},
					"project_name": map[string]interface{}{"type": "string", "description": "Project name"},
				},
			},
		},
	}
}
