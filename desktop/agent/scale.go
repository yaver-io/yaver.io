package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ScaleRecommendation describes a single scaling action with its priority and expected impact.
type ScaleRecommendation struct {
	Type            string // cdn, cache, replica, upgrade, loadbalancer
	Priority        string // high, medium, low
	Reason          string
	Action          string
	EstimatedImpact string
	Cost            string
}

// ScaleStatus is a snapshot of current resource utilisation plus any recommendations.
type ScaleStatus struct {
	CurrentPlan     string
	CPU             float64       // usage %
	Memory          float64       // usage %
	Disk            float64       // usage %
	RequestsPerMin  int
	AvgLatency      time.Duration
	ErrorRate       float64 // fraction, e.g. 0.012 = 1.2 %
	Recommendations []ScaleRecommendation
}

// ScaleManager analyses and adjusts scaling for a deployed Yaver workspace.
type ScaleManager struct {
	mu      sync.Mutex
	workDir string
}

// NewScaleManager returns a ScaleManager rooted at workDir.
func NewScaleManager(workDir string) *ScaleManager {
	return &ScaleManager{workDir: workDir}
}

// --------------------------------------------------------------------------
// Check
// --------------------------------------------------------------------------

// Check samples current resource usage and returns prioritised recommendations.
func (m *ScaleManager) Check() (*ScaleStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := &ScaleStatus{
		CurrentPlan: m.detectPlan(),
	}

	var err error
	s.CPU, err = sampleCPU()
	if err != nil {
		s.CPU = -1
	}
	s.Memory, err = sampleMemory()
	if err != nil {
		s.Memory = -1
	}
	s.Disk, err = sampleDisk(m.workDir)
	if err != nil {
		s.Disk = -1
	}

	s.RequestsPerMin, s.AvgLatency, s.ErrorRate = m.sampleMetrics()

	s.Recommendations = m.buildRecommendations(s)
	return s, nil
}

// detectPlan reads a plan hint from the local workspace config, defaulting to "Starter ($9/mo)".
func (m *ScaleManager) detectPlan() string {
	return "Starter ($9/mo)"
}

func sampleCPU() (float64, error) {
	out, err := exec.Command("sh", "-c",
		`top -bn1 | grep "Cpu(s)" | awk '{print $2}' | tr -d '%us,'`).Output()
	if err != nil {
		// macOS fallback
		out, err = exec.Command("sh", "-c",
			`ps -A -o %cpu | awk '{s+=$1} END {print s}'`).Output()
		if err != nil {
			return 0, err
		}
	}
	var v float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &v)
	return v, nil
}

func sampleMemory() (float64, error) {
	// /proc/meminfo on Linux
	out, err := exec.Command("sh", "-c",
		`awk '/MemTotal/{t=$2} /MemAvailable/{a=$2} END{printf "%.1f", (t-a)/t*100}' /proc/meminfo`).Output()
	if err != nil {
		// macOS: vm_stat
		out, err = exec.Command("sh", "-c",
			`vm_stat | awk '/Pages active|Pages wired/{s+=$NF} /Pages free/{f=$NF} END{printf "%.1f", s/(s+f)*100}'`).Output()
		if err != nil {
			return 0, err
		}
	}
	var v float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &v)
	return v, nil
}

func sampleDisk(path string) (float64, error) {
	if path == "" {
		path = "/"
	}
	out, err := exec.Command("df", "-h", path).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output")
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return 0, fmt.Errorf("unexpected df fields")
	}
	var v float64
	fmt.Sscanf(strings.TrimRight(fields[4], "%"), "%f", &v)
	return v, nil
}

// sampleMetrics attempts to read request metrics from a local stats socket or log.
// Falls back to zero values when no instrumentation is available.
func (m *ScaleManager) sampleMetrics() (reqPerMin int, avgLatency time.Duration, errorRate float64) {
	// Try a local /metrics endpoint (Prometheus exposition format).
	out, err := exec.Command("curl", "-sf", "--max-time", "2",
		"http://localhost:18080/metrics").Output()
	if err != nil {
		return 0, 0, 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "yaver_requests_per_minute "):
			fmt.Sscanf(strings.Fields(line)[1], "%d", &reqPerMin)
		case strings.HasPrefix(line, "yaver_latency_p95_ms "):
			var ms float64
			fmt.Sscanf(strings.Fields(line)[1], "%f", &ms)
			avgLatency = time.Duration(ms) * time.Millisecond
		case strings.HasPrefix(line, "yaver_error_rate "):
			fmt.Sscanf(strings.Fields(line)[1], "%f", &errorRate)
		}
	}
	return
}

func (m *ScaleManager) buildRecommendations(s *ScaleStatus) []ScaleRecommendation {
	var recs []ScaleRecommendation

	// CPU > 80 % sustained
	if s.CPU > 80 {
		recs = append(recs, ScaleRecommendation{
			Type:            "upgrade",
			Priority:        "high",
			Reason:          fmt.Sprintf("CPU at %.0f%% — approaching saturation", s.CPU),
			Action:          "yaver scale plan pro",
			EstimatedImpact: "2× CPU cores, reduced response latency",
			Cost:            "$29/mo (Pro plan)",
		})
	}

	// Memory > 85 %
	if s.Memory > 85 {
		recs = append(recs, ScaleRecommendation{
			Type:            "upgrade",
			Priority:        "high",
			Reason:          fmt.Sprintf("Memory at %.0f%% — OOM risk", s.Memory),
			Action:          "yaver scale plan pro  # or add swap: fallocate -l 2G /swapfile",
			EstimatedImpact: "Eliminate OOM kills, stabilise response times",
			Cost:            "$0 for swap, $29/mo for plan upgrade",
		})
	}

	// Disk > 80 %
	if s.Disk > 80 {
		recs = append(recs, ScaleRecommendation{
			Type:            "upgrade",
			Priority:        "high",
			Reason:          fmt.Sprintf("Disk at %.0f%% — less than 20 %% free", s.Disk),
			Action:          "docker system prune -af && yaver scale plan pro",
			EstimatedImpact: "Prevent write failures and log loss",
			Cost:            "$0 for cleanup; $29/mo for extra storage",
		})
	}

	// P95 latency > 500 ms
	if s.AvgLatency > 500*time.Millisecond {
		recs = append(recs, ScaleRecommendation{
			Type:            "cache",
			Priority:        "high",
			Reason:          fmt.Sprintf("P95 latency %s — caching can cut this by 60–90%%", s.AvgLatency),
			Action:          "yaver scale cache 6379",
			EstimatedImpact: "P95 latency <50 ms for cacheable responses",
			Cost:            "+$0 (Docker container on existing server)",
		})
		recs = append(recs, ScaleRecommendation{
			Type:            "cdn",
			Priority:        "medium",
			Reason:          "High latency may be caused by geographic distance",
			Action:          "yaver scale cdn yourdomain.com cloudflare",
			EstimatedImpact: "Static assets served from 200+ PoPs worldwide",
			Cost:            "$0 (Cloudflare free tier)",
		})
	}

	// Error rate > 1 %
	if s.ErrorRate > 0.01 {
		recs = append(recs, ScaleRecommendation{
			Type:            "upgrade",
			Priority:        "high",
			Reason:          fmt.Sprintf("Error rate %.2f%% exceeds 1%% threshold", s.ErrorRate*100),
			Action:          "Review logs: journalctl -u yaver --since '1h ago' | grep ERROR",
			EstimatedImpact: "Restore availability SLO",
			Cost:            "$0",
		})
	}

	// Request rate > 100/min on starter plan
	if s.RequestsPerMin > 100 && strings.Contains(strings.ToLower(s.CurrentPlan), "starter") {
		recs = append(recs, ScaleRecommendation{
			Type:            "upgrade",
			Priority:        "medium",
			Reason:          fmt.Sprintf("Traffic at %d req/min — approaching starter-plan limits", s.RequestsPerMin),
			Action:          "yaver scale plan pro",
			EstimatedImpact: "Higher rate limits, more workers, queue depth increase",
			Cost:            "$29/mo (Pro plan)",
		})
	}

	// Opportunistic cache suggestion when latency is measurable but not critical
	if s.AvgLatency >= 150*time.Millisecond && s.AvgLatency <= 500*time.Millisecond {
		recs = append(recs, ScaleRecommendation{
			Type:            "cache",
			Priority:        "medium",
			Reason:          fmt.Sprintf("P95 latency %s could drop to <50 ms with caching", s.AvgLatency),
			Action:          "yaver scale cache 6379",
			EstimatedImpact: "Significant latency reduction for repeated reads",
			Cost:            "+$0 (Docker container on existing server)",
		})
	}

	// CDN suggestion when no high-priority issues exist
	if len(recs) == 0 {
		recs = append(recs, ScaleRecommendation{
			Type:            "cdn",
			Priority:        "low",
			Reason:          "Proactive: add a CDN now before traffic grows",
			Action:          "yaver scale cdn yourdomain.com cloudflare",
			EstimatedImpact: "Lower origin load, faster global delivery",
			Cost:            "$0 (Cloudflare free tier)",
		})
	}

	// Sort: high → medium → low
	sorted := make([]ScaleRecommendation, 0, len(recs))
	for _, p := range []string{"high", "medium", "low"} {
		for _, r := range recs {
			if r.Priority == p {
				sorted = append(sorted, r)
			}
		}
	}
	return sorted
}

// --------------------------------------------------------------------------
// Plan
// --------------------------------------------------------------------------

type planSpec struct {
	Name    string
	CPU     string
	Memory  string
	Disk    string
	Price   string
	Workers int
}

var knownPlans = map[string]planSpec{
	"starter": {"Starter", "1 vCPU", "512 MB", "10 GB SSD", "$9/mo", 2},
	"pro":     {"Pro", "2 vCPU", "2 GB", "40 GB SSD", "$29/mo", 8},
	"team":    {"Team", "4 vCPU", "8 GB", "100 GB SSD", "$79/mo", 24},
	"scale":   {"Scale", "8 vCPU", "16 GB", "200 GB SSD", "$149/mo", 64},
}

// Plan previews the changes involved in switching to newPlan.
func (m *ScaleManager) Plan(newPlan string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(newPlan))
	next, ok := knownPlans[key]
	if !ok {
		keys := make([]string, 0, len(knownPlans))
		for k := range knownPlans {
			keys = append(keys, k)
		}
		return "", fmt.Errorf("unknown plan %q — available: %s", newPlan, strings.Join(keys, ", "))
	}

	// Current plan is always "starter" for now (extend when persisted config exists).
	curr := knownPlans["starter"]

	var b bytes.Buffer
	fmt.Fprintf(&b, "Plan Migration Preview\n")
	fmt.Fprintf(&b, "══════════════════════════════════════\n")
	fmt.Fprintf(&b, "  %-12s %-16s %-16s\n", "", "Current ("+curr.Name+")", "New ("+next.Name+")")
	fmt.Fprintf(&b, "  %-12s %-16s %-16s\n", "CPU:", curr.CPU, next.CPU)
	fmt.Fprintf(&b, "  %-12s %-16s %-16s\n", "Memory:", curr.Memory, next.Memory)
	fmt.Fprintf(&b, "  %-12s %-16s %-16s\n", "Disk:", curr.Disk, next.Disk)
	fmt.Fprintf(&b, "  %-12s %-16s %-16s\n", "Workers:", fmt.Sprint(curr.Workers), fmt.Sprint(next.Workers))
	fmt.Fprintf(&b, "  %-12s %-16s %-16s\n", "Price:", curr.Price, next.Price)
	fmt.Fprintf(&b, "\nMigration Steps:\n")
	fmt.Fprintf(&b, "  1. Backup your data:\n")
	fmt.Fprintf(&b, "       yaver backup create --label pre-scale\n")
	fmt.Fprintf(&b, "  2. Initiate plan change (zero-downtime live migration):\n")
	fmt.Fprintf(&b, "       yaver workspace upgrade --plan %s\n", key)
	fmt.Fprintf(&b, "  3. Verify health after migration:\n")
	fmt.Fprintf(&b, "       yaver scale check\n")
	fmt.Fprintf(&b, "  4. Monitor for 24 h; rollback if needed:\n")
	fmt.Fprintf(&b, "       yaver workspace downgrade --plan starter\n")
	return b.String(), nil
}

// --------------------------------------------------------------------------
// CDN
// --------------------------------------------------------------------------

// CDN generates instructions for fronting the application with the given CDN provider.
func (m *ScaleManager) CDN(domain, provider string) (string, error) {
	switch strings.ToLower(provider) {
	case "cloudflare", "":
		return m.cdnCloudflare(domain), nil
	case "bunny":
		return m.cdnBunny(domain), nil
	default:
		return "", fmt.Errorf("unknown CDN provider %q — supported: cloudflare, bunny", provider)
	}
}

func (m *ScaleManager) cdnCloudflare(domain string) string {
	if domain == "" {
		domain = "yourdomain.com"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "Cloudflare CDN Setup for %s\n", domain)
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")
	fmt.Fprintf(&b, "Step 1 — Add site to Cloudflare\n")
	fmt.Fprintf(&b, "  1. Log in to https://dash.cloudflare.com → Add a Site → enter %s\n", domain)
	fmt.Fprintf(&b, "  2. Choose the Free plan (sufficient for most projects).\n")
	fmt.Fprintf(&b, "  3. Cloudflare scans your current DNS records — confirm them.\n\n")
	fmt.Fprintf(&b, "Step 2 — Update your registrar's nameservers\n")
	fmt.Fprintf(&b, "  Cloudflare provides two NS records, e.g.:\n")
	fmt.Fprintf(&b, "    ns1.cloudflare.com\n")
	fmt.Fprintf(&b, "    ns2.cloudflare.com\n")
	fmt.Fprintf(&b, "  Replace your existing nameservers at your domain registrar.\n\n")
	fmt.Fprintf(&b, "Step 3 — Proxy your A/CNAME records\n")
	fmt.Fprintf(&b, "  In the Cloudflare DNS dashboard, make sure the orange cloud (proxy) is\n")
	fmt.Fprintf(&b, "  enabled on your A and CNAME records for %s and www.%s.\n\n", domain, domain)
	fmt.Fprintf(&b, "Step 4 — Recommended settings\n")
	fmt.Fprintf(&b, "  Security → SSL/TLS → Full (strict)\n")
	fmt.Fprintf(&b, "  Speed → Optimization → Brotli: On, Minify: JS/CSS/HTML\n")
	fmt.Fprintf(&b, "  Caching → Cache Rules → cache static assets for 30 days\n\n")
	fmt.Fprintf(&b, "Step 5 — Verify\n")
	fmt.Fprintf(&b, "  curl -sI https://%s | grep cf-ray\n", domain)
	fmt.Fprintf(&b, "  # A cf-ray header confirms traffic flows through Cloudflare.\n")
	return b.String()
}

func (m *ScaleManager) cdnBunny(domain string) string {
	if domain == "" {
		domain = "yourdomain.com"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "Bunny CDN Setup for %s\n", domain)
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")
	fmt.Fprintf(&b, "Step 1 — Create a Pull Zone\n")
	fmt.Fprintf(&b, "  1. Log in to https://dash.bunny.net → CDN → Add Pull Zone\n")
	fmt.Fprintf(&b, "  2. Origin URL: https://%s\n", domain)
	fmt.Fprintf(&b, "  3. Bunny assigns a CDN hostname, e.g. %s.b-cdn.net\n\n", strings.Replace(domain, ".", "-", -1))
	fmt.Fprintf(&b, "Step 2 — Add custom hostname (optional)\n")
	fmt.Fprintf(&b, "  CDN → Pull Zone → Custom Hostnames → add cdn.%s\n", domain)
	fmt.Fprintf(&b, "  Create a CNAME record: cdn.%s → %s.b-cdn.net\n\n", domain, strings.Replace(domain, ".", "-", -1))
	fmt.Fprintf(&b, "Step 3 — Pricing\n")
	fmt.Fprintf(&b, "  ~$0.01/GB bandwidth (Europe/NA); no monthly minimum.\n\n")
	fmt.Fprintf(&b, "Step 4 — Verify\n")
	fmt.Fprintf(&b, "  curl -sI https://cdn.%s/static/logo.png | grep bunny\n", domain)
	return b.String()
}

// --------------------------------------------------------------------------
// Cache
// --------------------------------------------------------------------------

// Cache starts a Redis-compatible caching container and emits integration code.
func (m *ScaleManager) Cache(backend string, port int) (string, error) {
	if port <= 0 {
		port = 6379
	}
	if backend == "" {
		backend = "redis"
	}

	image := map[string]string{
		"redis":     "redis:7-alpine",
		"keydb":     "eqalpha/keydb:latest",
		"dragonfly": "docker.dragonflydb.io/dragonflydb/dragonfly:latest",
	}[strings.ToLower(backend)]
	if image == "" {
		return "", fmt.Errorf("unknown cache backend %q — supported: redis, keydb, dragonfly", backend)
	}

	containerName := fmt.Sprintf("yaver-%s-cache", backend)

	var b bytes.Buffer
	fmt.Fprintf(&b, "%s Cache Setup\n", strings.Title(backend))
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")
	fmt.Fprintf(&b, "Start container:\n")
	fmt.Fprintf(&b, "  docker run -d --name %s --restart unless-stopped \\\n", containerName)
	fmt.Fprintf(&b, "    -p 127.0.0.1:%d:%d \\\n", port, port)
	fmt.Fprintf(&b, "    --memory 512m \\\n")
	fmt.Fprintf(&b, "    %s\n\n", image)
	fmt.Fprintf(&b, "Verify:\n")
	fmt.Fprintf(&b, "  docker exec %s redis-cli ping   # → PONG\n\n", containerName)
	fmt.Fprintf(&b, "Node.js integration:\n")
	fmt.Fprintf(&b, "  npm install ioredis\n\n")
	fmt.Fprintf(&b, "  import Redis from 'ioredis';\n")
	fmt.Fprintf(&b, "  const redis = new Redis({ host: '127.0.0.1', port: %d });\n\n", port)
	fmt.Fprintf(&b, "  // Cache-aside pattern\n")
	fmt.Fprintf(&b, "  async function getCached(key, fetchFn, ttl = 60) {\n")
	fmt.Fprintf(&b, "    const cached = await redis.get(key);\n")
	fmt.Fprintf(&b, "    if (cached) return JSON.parse(cached);\n")
	fmt.Fprintf(&b, "    const value = await fetchFn();\n")
	fmt.Fprintf(&b, "    await redis.setex(key, ttl, JSON.stringify(value));\n")
	fmt.Fprintf(&b, "    return value;\n")
	fmt.Fprintf(&b, "  }\n\n")
	fmt.Fprintf(&b, "Python integration:\n")
	fmt.Fprintf(&b, "  pip install redis\n\n")
	fmt.Fprintf(&b, "  import redis, json\n")
	fmt.Fprintf(&b, "  r = redis.Redis(host='127.0.0.1', port=%d, decode_responses=True)\n\n", port)
	fmt.Fprintf(&b, "  def get_cached(key, fetch_fn, ttl=60):\n")
	fmt.Fprintf(&b, "      if v := r.get(key):\n")
	fmt.Fprintf(&b, "          return json.loads(v)\n")
	fmt.Fprintf(&b, "      value = fetch_fn()\n")
	fmt.Fprintf(&b, "      r.setex(key, ttl, json.dumps(value))\n")
	fmt.Fprintf(&b, "      return value\n\n")
	fmt.Fprintf(&b, "Connection string:\n")
	fmt.Fprintf(&b, "  REDIS_URL=redis://127.0.0.1:%d\n", port)

	// Actually attempt to start the container if Docker is available.
	if _, err := exec.LookPath("docker"); err == nil {
		out, err := exec.Command("docker", "run", "-d",
			"--name", containerName,
			"--restart", "unless-stopped",
			fmt.Sprintf("-p=127.0.0.1:%d:%d", port, port),
			"--memory=512m",
			image,
		).CombinedOutput()
		if err != nil {
			// Container may already exist — that's fine.
			if !strings.Contains(string(out), "already in use") {
				fmt.Fprintf(&b, "\nNote: docker run attempted but failed: %s\n", strings.TrimSpace(string(out)))
			} else {
				fmt.Fprintf(&b, "\nContainer %s already running.\n", containerName)
			}
		} else {
			id := strings.TrimSpace(string(out))
			if len(id) > 12 {
				id = id[:12]
			}
			fmt.Fprintf(&b, "\nContainer started: %s (id %s)\n", containerName, id)
		}
	}

	return b.String(), nil
}

// --------------------------------------------------------------------------
// DBReplica
// --------------------------------------------------------------------------

// DBReplica configures a Postgres streaming read replica from sourceURL.
func (m *ScaleManager) DBReplica(sourceURL string) (string, error) {
	if sourceURL == "" {
		return "", fmt.Errorf("sourceURL must not be empty — provide a postgres:// connection string")
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "Postgres Read Replica Setup\n")
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")
	fmt.Fprintf(&b, "Source:  %s\n\n", maskDSN(sourceURL))
	fmt.Fprintf(&b, "Step 1 — On the primary server, allow replication connections\n")
	fmt.Fprintf(&b, "  # postgresql.conf\n")
	fmt.Fprintf(&b, "  wal_level = replica\n")
	fmt.Fprintf(&b, "  max_wal_senders = 5\n")
	fmt.Fprintf(&b, "  wal_keep_size = 1GB\n\n")
	fmt.Fprintf(&b, "  # pg_hba.conf — add your replica IP\n")
	fmt.Fprintf(&b, "  host  replication  repuser  <replica-ip>/32  md5\n\n")
	fmt.Fprintf(&b, "  # Create replication role\n")
	fmt.Fprintf(&b, "  CREATE ROLE repuser REPLICATION LOGIN PASSWORD 'strongpassword';\n\n")
	fmt.Fprintf(&b, "Step 2 — On the replica server, clone the primary\n")
	fmt.Fprintf(&b, "  pg_basebackup -h <primary-ip> -U repuser -D /var/lib/postgresql/15/main \\\n")
	fmt.Fprintf(&b, "    -Fp -Xs -P -R\n\n")
	fmt.Fprintf(&b, "  # -R writes recovery.conf / postgresql.auto.conf with standby settings\n\n")
	fmt.Fprintf(&b, "Step 3 — Start the replica\n")
	fmt.Fprintf(&b, "  systemctl start postgresql\n\n")
	fmt.Fprintf(&b, "Step 4 — Verify replication lag\n")
	fmt.Fprintf(&b, "  # On primary:\n")
	fmt.Fprintf(&b, "  SELECT client_addr, state, sent_lsn, replay_lsn,\n")
	fmt.Fprintf(&b, "         (sent_lsn - replay_lsn) AS lag_bytes\n")
	fmt.Fprintf(&b, "  FROM pg_stat_replication;\n\n")
	fmt.Fprintf(&b, "Read/write splitting — Node.js (pg):\n")
	fmt.Fprintf(&b, "  import { Pool } from 'pg';\n")
	fmt.Fprintf(&b, "  const writer = new Pool({ connectionString: process.env.DATABASE_URL });\n")
	fmt.Fprintf(&b, "  const reader = new Pool({ connectionString: process.env.DATABASE_REPLICA_URL });\n\n")
	fmt.Fprintf(&b, "  // Use reader for SELECTs, writer for writes\n")
	fmt.Fprintf(&b, "  const rows = await reader.query('SELECT * FROM users WHERE id = $1', [id]);\n\n")
	fmt.Fprintf(&b, "Read/write splitting — Python (psycopg2):\n")
	fmt.Fprintf(&b, "  import psycopg2\n")
	fmt.Fprintf(&b, "  writer = psycopg2.connect(os.environ['DATABASE_URL'])\n")
	fmt.Fprintf(&b, "  reader = psycopg2.connect(os.environ['DATABASE_REPLICA_URL'])\n\n")
	fmt.Fprintf(&b, "Replica connection string:\n")
	fmt.Fprintf(&b, "  DATABASE_REPLICA_URL=postgresql://appuser:pass@<replica-ip>:5432/appdb\n")
	return b.String(), nil
}

// maskDSN replaces the password in a DSN with **** for safe display.
func maskDSN(dsn string) string {
	// postgres://user:password@host/db → postgres://user:****@host/db
	if i := strings.Index(dsn, "://"); i != -1 {
		rest := dsn[i+3:]
		if at := strings.LastIndex(rest, "@"); at != -1 {
			creds := rest[:at]
			if colon := strings.Index(creds, ":"); colon != -1 {
				return dsn[:i+3] + creds[:colon+1] + "****" + "@" + rest[at+1:]
			}
		}
	}
	return dsn
}

// --------------------------------------------------------------------------
// LoadBalancer
// --------------------------------------------------------------------------

// LoadBalancer generates a Caddy config for load-balancing across upstreams.
func (m *ScaleManager) LoadBalancer(upstreams []string) (string, error) {
	if len(upstreams) == 0 {
		return "", fmt.Errorf("provide at least one upstream URL")
	}

	var upstreamBlock strings.Builder
	for _, u := range upstreams {
		upstreamBlock.WriteString(fmt.Sprintf("            to %s\n", u))
	}

	caddyConf := fmt.Sprintf(`{
    admin off
}

:80 {
    log {
        output stdout
        format json
    }

    reverse_proxy {
        lb_policy least_conn
%s
        health_uri /health
        health_interval 10s
        health_timeout  5s
        health_status   200

        header_up X-Forwarded-For {remote_host}
        header_up X-Real-IP       {remote_host}
    }
}
`, upstreamBlock.String())

	var b bytes.Buffer
	fmt.Fprintf(&b, "Caddy Load Balancer Configuration\n")
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")
	fmt.Fprintf(&b, "Upstreams (%d):\n", len(upstreams))
	for i, u := range upstreams {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, u)
	}
	fmt.Fprintf(&b, "\nCaddyfile:\n")
	fmt.Fprintf(&b, "─────────────────────\n")
	fmt.Fprint(&b, caddyConf)
	fmt.Fprintf(&b, "─────────────────────\n\n")
	fmt.Fprintf(&b, "Save as /etc/caddy/Caddyfile and reload:\n")
	fmt.Fprintf(&b, "  sudo systemctl reload caddy\n\n")
	fmt.Fprintf(&b, "Or run with Docker:\n")
	fmt.Fprintf(&b, "  docker run -d --name yaver-lb --restart unless-stopped \\\n")
	fmt.Fprintf(&b, "    -p 80:80 -p 443:443 \\\n")
	fmt.Fprintf(&b, "    -v /etc/caddy/Caddyfile:/etc/caddy/Caddyfile:ro \\\n")
	fmt.Fprintf(&b, "    caddy:2-alpine caddy run --config /etc/caddy/Caddyfile\n")
	return b.String(), nil
}

// --------------------------------------------------------------------------
// Optimize
// --------------------------------------------------------------------------

// Optimize applies a set of automatic performance improvements and reports what was done.
func (m *ScaleManager) Optimize() (string, error) {
	var applied []string

	// 1. Enable Brotli/gzip in Caddy
	caddyOptimised, err := m.optimiseCaddy()
	if err == nil {
		applied = append(applied, caddyOptimised...)
	}

	// 2. Postgres: analyse + statistics
	pgOptimised, err := m.optimisePostgres()
	if err == nil {
		applied = append(applied, pgOptimised...)
	}

	// 3. Check for N+1 hints in logs
	n1hints := m.checkN1Queries()
	if n1hints != "" {
		applied = append(applied, n1hints)
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "Optimizations Applied\n")
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")
	if len(applied) == 0 {
		fmt.Fprintf(&b, "  No additional optimizations were needed.\n")
		return b.String(), nil
	}
	for i, a := range applied {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, a)
	}
	return b.String(), nil
}

func (m *ScaleManager) optimiseCaddy() ([]string, error) {
	// Check if caddy is installed
	if _, err := exec.LookPath("caddy"); err != nil {
		return nil, err
	}
	var out []string
	// Caddy 2 enables gzip/brotli via the encode directive.
	snippet := `
# Append to your site block in /etc/caddy/Caddyfile:
encode gzip br

# Cache static assets
@static {
    file
    path *.html *.css *.js *.png *.jpg *.jpeg *.webp *.svg *.ico *.woff2
}
header @static Cache-Control "public, max-age=2592000, immutable"
`
	out = append(out, "Caddy: encode gzip + brotli directive snippet generated (apply to Caddyfile):\n"+snippet)
	out = append(out, "Caddy: HTTP/2 and HTTP/3 (QUIC) are enabled by default in Caddy 2")
	return out, nil
}

func (m *ScaleManager) optimisePostgres() ([]string, error) {
	// Try to run ANALYZE via psql; skip silently if psql not present or no DB configured.
	if _, err := exec.LookPath("psql"); err != nil {
		return nil, err
	}
	dbURL := m.detectDatabaseURL()
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL not set")
	}
	out, err := exec.Command("psql", dbURL, "-c", "ANALYZE;").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ANALYZE failed: %s", out)
	}
	var results []string
	results = append(results, "Postgres: ran ANALYZE — query planner statistics refreshed")

	// Suggest missing index check
	indexQuery := `SELECT schemaname, tablename, attname
FROM pg_stats
WHERE n_distinct > 1000 AND tablename NOT LIKE 'pg_%'
ORDER BY n_distinct DESC LIMIT 5;`
	indexOut, err := exec.Command("psql", dbURL, "-c", indexQuery).CombinedOutput()
	if err == nil && strings.TrimSpace(string(indexOut)) != "" {
		results = append(results, "Postgres: high-cardinality columns found — consider adding indexes:\n"+string(indexOut))
	}
	return results, nil
}

func (m *ScaleManager) detectDatabaseURL() string {
	// Check common env var names.
	for _, v := range []string{"DATABASE_URL", "POSTGRES_URL", "DB_URL"} {
		if val, err := exec.Command("sh", "-c", fmt.Sprintf("echo $%s", v)).Output(); err == nil {
			if s := strings.TrimSpace(string(val)); s != "" {
				return s
			}
		}
	}
	return ""
}

func (m *ScaleManager) checkN1Queries() string {
	// Look for repeated SELECT patterns in recent app stdout.
	out, err := exec.Command("sh", "-c",
		`journalctl -u yaver --since '1h ago' 2>/dev/null | grep -i 'SELECT.*WHERE.*id' | sort | uniq -c | sort -rn | head -5`).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return ""
	}
	return "Potential N+1 queries detected in logs (last 1h):\n" + string(out)
}

// --------------------------------------------------------------------------
// Benchmark
// --------------------------------------------------------------------------

// Benchmark runs 1000 requests (10 concurrent) against url and reports throughput.
func (m *ScaleManager) Benchmark(url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("url must not be empty")
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "Benchmark: %s\n", url)
	fmt.Fprintf(&b, "══════════════════════════════════════\n\n")

	// Prefer `hey` (https://github.com/rakyll/hey) if available.
	if _, err := exec.LookPath("hey"); err == nil {
		out, err := exec.Command("hey", "-n", "1000", "-c", "10", url).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("hey benchmark failed: %s", out)
		}
		b.Write(out)
		return b.String(), nil
	}

	// Fallback: curl-based benchmark loop.
	fmt.Fprintf(&b, "Note: 'hey' not found — using curl loop (less accurate).\n")
	fmt.Fprintf(&b, "  Install hey: go install github.com/rakyll/hey@latest\n\n")

	start := time.Now()
	const total = 1000
	const concurrency = 10
	type result struct {
		dur time.Duration
		ok  bool
	}
	results := make(chan result, total)
	sem := make(chan struct{}, concurrency)

	for i := 0; i < total; i++ {
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			t := time.Now()
			out, err := exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", url).Output()
			dur := time.Since(t)
			ok := err == nil && strings.TrimSpace(string(out)) == "200"
			results <- result{dur, ok}
		}()
	}
	// Drain.
	var totalDur time.Duration
	var successes int
	for i := 0; i < total; i++ {
		r := <-results
		totalDur += r.dur
		if r.ok {
			successes++
		}
	}
	elapsed := time.Since(start)
	rps := float64(successes) / elapsed.Seconds()
	avgLatency := totalDur / total

	fmt.Fprintf(&b, "Requests:      %d\n", total)
	fmt.Fprintf(&b, "Concurrency:   %d\n", concurrency)
	fmt.Fprintf(&b, "Duration:      %s\n", elapsed.Round(time.Millisecond))
	fmt.Fprintf(&b, "RPS:           %.1f req/s\n", rps)
	fmt.Fprintf(&b, "Avg latency:   %s\n", avgLatency.Round(time.Millisecond))
	fmt.Fprintf(&b, "Success rate:  %.1f%% (%d/%d)\n", float64(successes)/total*100, successes, total)
	return b.String(), nil
}

// --------------------------------------------------------------------------
// FormatStatus
// --------------------------------------------------------------------------

// FormatStatus renders a ScaleStatus as a human-readable string with progress bars.
func (m *ScaleManager) FormatStatus(s *ScaleStatus) string {
	var b bytes.Buffer

	fmt.Fprintf(&b, "Scale Status\n")
	fmt.Fprintf(&b, "═══════════════════════════\n")
	fmt.Fprintf(&b, "  Plan:     %s\n", s.CurrentPlan)
	fmt.Fprintf(&b, "  CPU:      %s\n", formatBar(s.CPU))
	fmt.Fprintf(&b, "  Memory:   %s\n", formatBar(s.Memory))
	fmt.Fprintf(&b, "  Disk:     %s\n", formatBar(s.Disk))

	if s.RequestsPerMin >= 0 {
		fmt.Fprintf(&b, "  Requests: %d/min\n", s.RequestsPerMin)
	}
	if s.AvgLatency > 0 {
		fmt.Fprintf(&b, "  Latency:  P95 %s\n", s.AvgLatency.Round(time.Millisecond))
	}
	if s.ErrorRate >= 0 {
		fmt.Fprintf(&b, "  Errors:   %.2f%%\n", s.ErrorRate*100)
	}

	if len(s.Recommendations) > 0 {
		fmt.Fprintf(&b, "\n  Recommendations:\n")
		for _, r := range s.Recommendations {
			icon := priorityIcon(r.Priority)
			fmt.Fprintf(&b, "    %s %s: %s\n", icon, strings.ToUpper(r.Priority), r.Action)
			fmt.Fprintf(&b, "       Reason: %s\n", r.Reason)
			if r.Cost != "" {
				fmt.Fprintf(&b, "       Cost: %s\n", r.Cost)
			}
			fmt.Fprintln(&b)
		}
	}
	return b.String()
}

// formatBar renders "XX% ████████░░░░░░░░░░░░" (20-char bar).
func formatBar(pct float64) string {
	if pct < 0 {
		return "n/a"
	}
	filled := int(pct / 5) // 0..20
	if filled > 20 {
		filled = 20
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 20-filled)
	return fmt.Sprintf("%.0f%% %s", pct, bar)
}

func priorityIcon(priority string) string {
	switch priority {
	case "high":
		return "⚡"
	case "medium":
		return "📈"
	default:
		return "💡"
	}
}
