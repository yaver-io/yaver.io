# Personal Assistant Automation Infrastructure — Deep Audit & Implementation Plan

## Executive Summary

**Status**: Partial foundation exists, needs unified automation surface

**Current State**:
- ✅ Browser automation: chromedp driver (mature, production-ready)
- ✅ Alternative: Playwright driver (Node sidecar)
- ✅ Mobile automation: redroid driver + real device ADB support
- ✅ Intent routing: model-backed gateway intent classifier
- ✅ Connector framework: dynamic MCP tools for app connectors
- ❌ Missing: Unified automation abstraction across web + mobile
- ❌ Missing: Session management + warm-keeping for personal assistant use
- ❌ Missing: Health monitoring + self-healing
- ❌ Missing: Security hardening + audit logging
- ❌ Missing: Latency optimization + async task patterns

**Verdict**: Foundation is solid, but needs 2-3 months of focused engineering to reach production-ready personal assistant automation infrastructure.

---

## Current Architecture Analysis

### Browser Automation Stack

**Location**: `desktop/agent/testkit/driver_chromecdp.go`

**Capabilities**:
```go
type WebDriver interface {
    Launch(ctx context.Context) error
    Navigate(ctx context.Context, url string) error
    Snapshot(ctx context.Context) (Snapshot, error)
    Click(ctx context.Context, selector string) error
    Fill(ctx context.Context, selector, value string) error
    Screenshot(ctx context.Context) ([]byte, error)
    Console() []ConsoleMsg
    Network() []NetEvent
    Close()
}
```

**Strengths**:
- Zero dependency: built on existing chromedp usage
- Full CDP event capture: console + network
- Device emulation: viewport, DPR, mobile size
- Stable selectors: testID > id > aria-label > structural path
- Headless + headful modes (for WebRTC streaming)

**Limitations for Personal Assistant**:
- No session persistence (always fresh launch)
- No warm-keeping (always cold boot ~2-3s)
- No cross-tab management
- No cookie/auth state management across runs
- No retry/fallback logic
- No health monitoring

**Alternative: Playwright** (`desktop/agent/testkit/driver_playwright.go`):
- Node.js sidecar, self-contained script generation
- Same spec format, transparent execution
- Better selector engine + trace viewer support
- Extra dependency (Node + playwright npm package)

---

### Mobile Automation Stack

**Location**: `desktop/agent/studio/redroid.go`

**Capabilities**:
```go
type Driver interface {
    Launch(ctx context.Context, app App) error
    ForceStop(ctx context.Context, app App) error
    StartForegroundService(ctx context.Context, component, action string) error
    StopService(ctx context.Context, component string) error
    Tap(ctx context.Context, x, y int) error
    Type(ctx context.Context, text string) error
    Key(ctx context.Context, key string) error
    Back(ctx context.Context) error
    Home(ctx context.Context) error
    ExpandNotifications(ctx context.Context) error
    CollapseNotifications(ctx context.Context) error
    NotificationText(ctx context.Context) (string, error)
    ViewTree(ctx context.Context) (string, error)
    Logcat(ctx context.Context, lines int) (string, error)
    Screenshot(ctx context.Context) ([]byte, error)
    RecordStart(ctx context.Context, maxSec int) error
    RecordStop(ctx context.Context) ([]byte, error)
}
```

**Strengths**:
- Full Android input coverage: tap, type, keys, swipe, nav
- UIAutomator view hierarchy dump for element discovery
- Screen recording + screenshots
- Logcat capture for debugging
- Foreground service support (for long-running operations)
- Notification management

**Limitations for Personal Assistant**:
- No element-by-element abstraction (only coordinates/text search)
- No visual self-healing (relies on XML dump + coordinates)
- No app state persistence across runs
- No session management
- No retry/fallback logic
- No health monitoring

---

### Intent Routing Infrastructure

**Location**: `desktop/agent/gateway_intent_model.go`

**Capabilities**:
```go
type IntentEngine string
const (
    IntentCode         IntentEngine = "code"
    IntentGatewayRead  IntentEngine = "gateway_read"
    IntentGatewayAct   IntentEngine = "gateway_act"
)

type IntentDecision struct {
    Engine     IntentEngine
    Connector  string
    Capability string
    Params     map[string]string
    Confidence float64
    Reason     string
}
```

**Strengths**:
- Model-backed intent classification with fallback
- Tiered routing: keyword (fast) → model (deep) only when needed
- Validates against user's connector catalog
- Never invents connectors (security hardening)
- Cost-optimized: clear dev commands never pay for model calls

**Limitations for Personal Assistant**:
- No automation engine binding (classification only)
- No connector execution framework
- No session/context management across calls
- No retry/fallback logic
- No audit logging

---

## Critical Gaps

### 1. **Unified Automation Interface**

**Problem**: No single abstraction that personal assistant code can use to switch between web (chromedp/playwright) and mobile (redroid/real device) drivers.

**Impact**: Each connector needs to know whether its target is web or mobile, creating tight coupling.

**Solution Needed**:
```go
type AutomationSurface interface {
    // Lifecycle
    EnsureReady(ctx context.Context) error
    WarmKeep(ctx context.Context) error
    Close() error

    // Universal actions
    Navigate(ctx context.Context, target string) error // URL or app package
    Element(ctx context.Context, selector string) (Element, error)
    Action(ctx context.Context, selector string, action ActionType, payload any) error
    Screenshot(ctx context.Context) ([]byte, error)

    // State capture
    CaptureState(ctx context.Context) (SurfaceState, error)
    CaptureNetwork(ctx context.Context) ([]NetworkEvent, error)
    CaptureConsole(ctx context.Context) ([]ConsoleMsg, error)

    // Mobile-specific
    TapAt(ctx context.Context, x, y int) error
    Swipe(ctx context.Context, x1, y1, x2, y2 int, durationMs int) error
    Back(ctx context.Context) error
    Home(ctx context.Context) error

    // Diagnostics
    HealthCheck(ctx context.Context) HealthReport
    Metrics() SurfaceMetrics
}
```

---

### 2. **Session Management + Warm-Keeping**

**Problem**: Personal assistant needs persistent sessions (cookies, auth state) + warm instances to reduce latency.

**Impact**: Cold boots add 2-5s latency per task → poor UX for "check balance" style queries.

**Solution Needed**:
```go
type SessionManager struct {
    // Session pool
    webSessions    map[string]*WebSession
    mobileSessions map[string]*MobileSession

    // Warm-keep
    warmWebPool    *WebSessionPool
    warmMobilePool *MobileSessionPool

    // Security
    encryptionKey  []byte
    auditLog       *AuditLogger
}

type SessionConfig struct {
    // App + auth scope
    TargetID      string  // "misli.com" or "com.garanti.ceza"
    AuthMethod    string  // "oauth", "password", "totp"
    Permissions   []string

    // Lifecycle
    KeepWarm      bool
    MaxIdleTime   time.Duration
    MaxReuseCount int

    // Security
    NetworkJail   string  // "strict", "permissive"
    AuditActions  bool
}
```

---

### 3. **Health Monitoring + Self-Healing**

**Problem**: UI automation is fragile (app updates, session expiry, captchas). Need monitoring + automatic recovery.

**Impact**: <95% reliability → trust death for personal assistant (per audit doc).

**Solution Needed**:
```go
type HealthMonitor struct {
    // Node health
    WebNodes    *WebHealthPool
    MobileNodes *MobileHealthPool

    // App-specific health
    AppHealth   map[string]*AppHealthTracker

    // Self-healing
    Healers     map[string]Healer  // "session-expiry", "ui-changed", "captcha"
}

type HealthReport struct {
    Overall    HealthStatus
    Checks     map[string]HealthCheck
    Suggested  []HealingAction
}

type Healer interface {
    CanHeal(ctx context.Context, issue HealthIssue) bool
    Heal(ctx context.Context, issue HealthIssue) error
}
```

---

### 4. **Security Hardening + Audit Logging**

**Problem**: Personal assistant holds ALL user credentials. Security breach = catastrophe (per audit doc).

**Impact**: Hardening is existential. Audit logging is both security + compliance requirement.

**Solution Needed**:
```go
type SecurityLayer struct {
    // Network isolation
    NetworkJails map[string]*NetworkJail

    // Credential management
    Vault        *CredentialVault
    Permissions  *PermissionManager

    // Audit
    AuditLog     *AuditLogger
}

type NetworkJail struct {
    AllowedHosts    map[string]bool
    BlockedPatterns []string
    RateLimiter     *RateLimiter
}

type AuditLogger struct {
    // Per-action audit
    Actions     []AuditEntry

    // Session audit
    Sessions    []SessionAudit

    // Security events
    Events      []SecurityEvent
}
```

---

### 5. **Latency Optimization + Async Task Patterns**

**Problem**: "Alexa ~1s; Yaver UI-drive = 10-30s/task" is existential (per audit doc).

**Impact**: If not addressed, personal assistant loses to Alexa on the primary metric.

**Solution Needed**:
```go
type TaskEngine struct {
    // Task queue
    Queue       *TaskQueue

    // Latency optimization
    Cache       *TaskCache
    Prefetcher  *Prefetcher

    // Async patterns
    Notifier    *TaskNotifier
    CallbackURL string
}

type TaskConfig struct {
    // Async handling
    AsyncMode   AsyncMode  // "sync", "async", "prefetch"
    CallbackURL string
    NotifyOn    string  // "success", "failure", "both"

    // Latency targets
    TargetLatency time.Duration
    Timeout      time.Duration
    RetryPolicy  RetryPolicy
}
```

---

## Implementation Plan

### Phase 1: Core Abstraction (Weeks 1-2)

**Goal**: Create unified `AutomationSurface` interface + implementations for web/mobile.

**Deliverables**:
1. `automation/surface.go` — unified interface
2. `automation/surface_web.go` — chromedp/playwright wrapper
3. `automation/surface_mobile.go` — redroid/real device wrapper
4. `automation/surface_pool.go` — session pool management
5. `automation/factory.go` — surface factory based on target type

**Key Design Decisions**:
- Prefer chromedp for web (zero deps), Playwright as opt-in
- Prefer real device for mobile, redroid as fallback
- Surface abstraction hides driver differences from connectors
- Session pool handles warm-keep + reuse

---

### Phase 2: Session Management (Weeks 2-3)

**Goal**: Persistent sessions + warm-keep + auth state management.

**Deliverables**:
1. `session/manager.go` — unified session manager
2. `session/web.go` — web session (cookies + localstorage)
3. `session/mobile.go` — mobile session (app state + auth)
4. `session/warm.go` — warm-keep logic
5. `session/crypto.go` — session encryption

**Key Design Decisions**:
- Sessions encrypted at rest
- Auth state persisted + refreshed (OAuth tokens, session cookies)
- Warm pool with LRU eviction + max reuse limits
- Session scoping by target (per-app isolation)

---

### Phase 3: Health Monitoring (Weeks 3-4)

**Goal**: Node health checks + app health tracking + self-healing.

**Deliverables**:
1. `health/monitor.go` — unified health monitor
2. `health/web.go` — web node health
3. `health/mobile.go` — mobile node health
4. `health/app.go` — app-specific health
5. `health/healers.go` — healing strategies

**Key Design Decisions**:
- Health checks every 30s (tunable)
- App health tracking (version, UI stability, reliability score)
- Healing strategies: session refresh, surface restart, connector update
- Health API for dashboard monitoring

---

### Phase 4: Security Hardening (Weeks 4-5)

**Goal**: Network jails + credential vault + audit logging.

**Deliverables**:
1. `security/network.go` — network jails
2. `security/vault.go` — credential encryption
3. `security/permissions.go` — per-app permission model
4. `security/audit.go` — audit logging
5. `security/compliance.go` — compliance reports

**Key Design Decisions**:
- Network jails per app (allowlist + blocklist)
- Vault encrypts credentials at rest, decrypts in memory only
- Permissions: read/write/admin tiers per connector
- Audit logs: tamper-evident append-only store
- Compliance: GDPR + SOX exportable reports

---

### Phase 5: Task Engine + Latency (Weeks 5-6)

**Goal**: Task queue + async patterns + latency optimization.

**Deliverables**:
1. `task/engine.go` — unified task engine
2. `task/queue.go` — persistent task queue
3. `task/cache.go` — task result caching
4. `task/async.go` — async + notification
5. `task/metrics.go` — latency tracking

**Key Design Decisions**:
- Task queue: SQLite-backed (same infra as phone projects)
- Async modes: sync (wait), async (callback), prefetch (background)
- Latency targets: <5s for cached reads, <15s for fresh reads
- Notification: push + webhook + SSE
- Metrics: per-target latency distribution + trends

---

### Phase 6: Connector Framework Integration (Weeks 6-7)

**Goal**: Wire new automation infrastructure into existing connector system.

**Deliverables**:
1. Updated MCP tools using `AutomationSurface`
2. Connector health monitoring
3. Connector session management
4. Connector audit logging
5. Migration guide for existing connectors

**Key Design Decisions**:
- Connectors use `AutomationSurface` abstraction (no driver coupling)
- Each connector declares its surface requirements (web/mobile/both)
- Health + audit hooks in connector lifecycle
- Backward compatible with existing connector interface

---

### Phase 7: Production Hardening (Weeks 7-8)

**Goal**: Monitoring + alerts + scaling + disaster recovery.

**Deliverables**:
1. Production monitoring stack (Prometheus + Grafana)
2. Alerting: PagerDuty + Slack + email
3. Auto-scaling for warm pool
4. Disaster recovery procedures
5. Load testing benchmarks

**Key Design Decisions**:
- Metrics: surface health, task latency, connector reliability
- Alerts: critical (surface down), warning (latency spike)
- Auto-scaling: warm pool size based on demand
- DR: backups, failover regions, manual override

---

## Recommended Stack

| Component | Recommendation |
|-----------|---------------|
| **Web automation** | chromedp (primary), Playwright (opt-in) |
| **Mobile automation** | Real device (primary), redroid (fallback) |
| **Session storage** | SQLite (same as phone projects) |
| **Credential vault** | NaCl encryption (libsodium-go) |
| **Audit logging** | Append-only SQLite + daily rotation |
| **Health monitoring** | Prometheus + custom probes |
| **Task queue** | SQLite (persistent) + in-memory cache |
| **Notifications** | SSE (web) + APNs/FCM (mobile) |
| **Metrics** | OpenTelemetry + Prometheus |
| **Alerting** | PagerDuty + Slack |

---

## Success Metrics

**Reliability**:
- >95% task success rate (self-healed)
- <5% manual intervention rate
- <1m MTTR for surface failures

**Latency**:
- <5s P50 for cached reads
- <15s P50 for fresh reads
- <30s P95 for fresh reads

**Security**:
- Zero credential leaks in logs
- 100% audit trail for sensitive actions
- 100% network jail compliance

**Performance**:
- <1s P50 surface acquisition (warm pool)
- <50ms P50 task dispatch
- <100ms P50 task queue op

---

## Risks + Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| App UI changes break selectors | High | Visual self-healing + ML selector fallback |
| Session expires mid-task | High | Session refresh hooks + graceful retry |
| Latency spikes with cold boots | High | Warm pool + prefetch + async fallback |
| Credential vault breach | Critical | Encryption + audit + MFA rotation |
| Resource exhaustion | Medium | Pool limits + LRU + auto-scaling |

---

## Timeline

| Phase | Duration | Start | End | Owner |
|-------|----------|-------|-----|-------|
| Core abstraction | 2 weeks | Week 1 | Week 2 | TBA |
| Session management | 1 week | Week 2 | Week 3 | TBA |
| Health monitoring | 1 week | Week 3 | Week 4 | TBA |
| Security hardening | 1 week | Week 4 | Week 5 | TBA |
| Task engine | 1 week | Week 5 | Week 6 | TBA |
| Connector integration | 1 week | Week 6 | Week 7 | TBA |
| Production hardening | 1 week | Week 7 | Week 8 | TBA |

**Total**: 8 weeks focused engineering → production-ready personal assistant automation infrastructure.