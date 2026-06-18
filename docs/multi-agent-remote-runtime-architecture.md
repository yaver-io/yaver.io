# Multi-Agent Runtime & Self-Improving System Architecture

## Executive Summary

This system enables seamless use of multiple AI coding agents (OpenCode/GLM 4.7, Codex, Claude Code) from mobile devices, with Hetzner as remote runtime, layered deployment capabilities, and a self-improving Talos web UI.

## Core Components

### 1. Multi-Agent Runtime Orchestration Layer

**Purpose**: Enable seamless switching between different AI coding agents at runtime from mobile devices.

**Architecture**:
```
Mobile App → Agent Orchestration Service → Runtime Pool
                                          ├─ OpenCode/GLM 4.7
                                          ├─ Codex
                                          └─ Claude Code
```

**Key Features**:
- Agent capability detection and matching
- Runtime agent switching without session loss
- Performance-based agent routing
- Cost-aware agent selection
- Agent health monitoring and failover

**Implementation Plan**:

1. **Agent Registry Service** (`desktop/agent/agent_registry.go`)
```go
type AgentCapability struct {
    ID          string
    Name        string
    Models      []string
    Capabilities []string // ["code-generation", "analysis", "deployment"]
    CostPerToken float64
    Latency     time.Duration
    Available   bool
}

type AgentOrchestrator struct {
    registry      map[string]AgentCapability
    currentAgent  string
    performanceHistory map[string]PerformanceMetrics
}
```

2. **Agent Selection Strategy**
```go
func (ao *AgentOrchestrator) SelectAgent(task TaskRequirements) string {
    // Priority: capability match → cost → latency → availability
    candidates := ao.filterByCapability(task.RequiredCapabilities)
    candidates = ao.filterByCost(candidates, task.MaxCost)
    candidates = ao.filterByPerformance(candidates)
    return ao.selectBestCandidate(candidates)
}
```

3. **Mobile Integration** (`mobile/src/lib/agentOrchestrator.ts`)
```typescript
class AgentOrchestrator {
    async switchAgent(agentId: string): Promise<void>
    async getAgentCapabilities(): Promise<AgentCapability[]>
    async routeTask(task: Task): Promise<TaskResult>
}
```

### 2. Hetzner Remote Runtime Provisioning

**Purpose**: Automatically provision and manage Hetzner servers as remote runtimes with Yaver + Talos deployment.

**Architecture**:
```
Mobile/Console → Runtime Provisioning Service → Hetzner API
                                          ↓
                                    Server Bootstrap
                                          ├─ Yaver Install
                                          ├─ Talos Clone
                                          ├─ Agent Setup
                                          └─ Service Registration
```

**Key Features**:
- One-click Hetzner server provisioning
- Automatic Yaver + Talos deployment
- Runtime health monitoring
- Auto-scaling based on workload
- Cost optimization (server hibernation)

**Implementation Plan**:

1. **Hetzner Provisioning Service** (`desktop/agent/hetzner_provision.go`)
```go
type HetznerProvisioner struct {
    client       *hcloud.Client
    yaverInstaller YaverInstaller
    talosInstaller TalosInstaller
}

type RuntimeProvisionConfig struct {
    ServerType   string // "cax21" "cx22" etc.
    Region       string // "hel1" "fsn1" etc.
    Agents       []string // ["opencode", "codex", "claude"]
    Projects     []string // ["yaver", "talos"]
    AutoScale    bool
    MaxCost      float64
}
```

2. **Bootstrap Automation**
```bash
# Automated bootstrap script
yaver hetzner provision --server-type cax21 --region hel1 \
    --agents opencode,codex,claude \
    --projects yaver,talos \
    --auto-scale
```

3. **Health Monitoring**
```go
func (hp *HetznerProvisioner) MonitorRuntime(deviceID string) RuntimeHealth {
    health := RuntimeHealth{
        DeviceID: deviceID,
        Status:   "unknown",
        Metrics:  make(map[string]interface{}),
    }

    // Check Yaver agent status
    if yaverHealthy := hp.checkYaverHealth(deviceID); !yaverHealthy {
        health.Status = "degraded"
        health.Issues = append(health.Issues, "yaver_unhealthy")
    }

    // Check agent availability
    for _, agent := range hp.config.Agents {
        if agentHealthy := hp.checkAgentHealth(deviceID, agent); !agentHealthy {
            health.Status = "degraded"
            health.Issues = append(health.Issues, fmt.Sprintf("%s_unhealthy", agent))
        }
    }

    return health
}
```

### 3. Layered Deployment System

**Purpose**: Deploy to multiple platforms (Convex, Cloudflare, Google Play, TestFlight) with intelligent fallback to Mac mini for iOS builds.

**Architecture**:
```
Deployment Request → Deployment Router → Platform Layer
                                        ├─ Convex (direct)
                                        ├─ Cloudflare (direct)
                                        ├─ Google Play (direct)
                                        └─ TestFlight (Mac mini fallback)
```

**Key Features**:
- Platform capability detection
- Smart routing based on target platform
- Mac mini discovery and utilization
- Build result caching
- Deployment rollback support

**Implementation Plan**:

1. **Deployment Router** (`desktop/agent/deployment_router.go`)
```go
type DeploymentRouter struct {
    platforms map[string]PlatformHandler
    macMiniPool MacMiniPool
}

type DeploymentRequest struct {
    Project     string
    Platform    string // "convex" "cloudflare" "play-store" "testflight"
    Environment string // "production" "staging" "internal"
    Artifacts   []Artifact
}

func (dr *DeploymentRouter) RouteDeployment(req DeploymentRequest) DeploymentResult {
    handler := dr.platforms[req.Platform]
    if handler == nil {
        return DeploymentResult{Error: "unsupported_platform"}
    }

    // Special handling for iOS TestFlight
    if req.Platform == "testflight" && !dr.macMiniPool.Available() {
        return dr.waitForMacMini(req)
    }

    return handler.Deploy(req)
}
```

2. **Mac Mini Pool Manager** (`desktop/agent/macmini_pool.go`)
```go
type MacMiniPool struct {
    macMinis     map[string]MacMini
    buildQueue   chan BuildRequest
    results      map[string]BuildResult
}

type MacMini struct {
    DeviceID    string
    IPAddress   string
    Status      string // "idle" "building" "offline"
    Capabilities []string // ["xcodebuild" "testflight"]
    LastUsed    time.Time
}

func (mmp *MacMiniPool) QueueBuild(build BuildRequest) (string, error) {
    macMini := mmp.selectBestMacMini(build.Requirements)
    if macMini == nil {
        return "", errors.New("no available mac mini")
    }

    buildID := generateBuildID()
    mmp.buildQueue <- BuildRequest{
        ID:        buildID,
        MacMiniID: macMini.DeviceID,
        Request:   build,
    }

    return buildID, nil
}
```

3. **Cross-Platform Build System**
```bash
# Universal deployment command
yaver deploy --platform all --env production \
    --projects yaver,talos \
    --use-macmini-for-ios
```

### 4. Talos Self-Improvement Web UI

**Purpose**: Mobile-friendly admin interface for Talos with Yaver web SDK integration for prompt-based code updates.

**Architecture**:
```
Mobile/Web UI → Admin Access Control → Yaver Web SDK → Agent Runtime
                                          ↓
                                    Code Generation
                                          ↓
                                    Hermes Bundle Push
                                          ↓
                                    Hot Reload
```

**Key Features**:
- Mobile-responsive admin interface
- ACL-based access control
- Natural language code updates
- Real-time preview and testing
- Version control integration
- Rollback capabilities

**Implementation Plan**:

1. **Admin Access Control System** (`backend/convex/adminAccess.ts`)
```typescript
type AdminRole = "owner" | "admin" | "editor" | "viewer";

type AdminPermission = {
  resource: string;
  actions: string[];
};

type AdminUser = {
  userId: string;
  roles: AdminRole[];
  permissions: AdminPermission[];
  lastActive: number;
};

const ADMIN_ROLES: Record<AdminRole, AdminPermission[]> = {
  owner: [
    { resource: "*", actions: ["*"] }
  ],
  admin: [
    { resource: "code", actions: ["write", "deploy", "review"] },
    { resource: "config", actions: ["read", "write"] },
    { resource: "users", actions: ["read", "manage"] }
  ],
  editor: [
    { resource: "code", actions: ["write", "review"] },
    { resource: "config", actions: ["read"] }
  ],
  viewer: [
    { resource: "*", actions: ["read"] }
  ]
};
```

2. **Prompt-Based Code Update System** (`desktop/agent/prompt_updates.go`)
```go
type CodeUpdateRequest struct {
    Prompt          string
    TargetProject   string // "talos" "yaver"
    TargetFiles     []string
    UpdateType      string // "bugfix" "feature" "refactor"
    RequesterID     string
    ReviewRequired  bool
    AutoDeploy      bool
}

type PromptUpdateProcessor struct {
    agentOrchestrator *AgentOrchestrator
    codeValidator     CodeValidator
    deploymentRouter  *DeploymentRouter
}

func (pup *PromptUpdateProcessor) ProcessUpdate(req CodeUpdateRequest) UpdateResult {
    // 1. Validate permissions
    if !pup.checkPermissions(req.RequesterID, req) {
        return UpdateResult{Error: "insufficient_permissions"}
    }

    // 2. Generate code changes using selected agent
    codeChanges := pup.agentOrchestrator.GenerateCode(req)

    // 3. Validate changes
    if !pup.codeValidator.Validate(codeChanges) {
        return UpdateResult{Error: "validation_failed"}
    }

    // 4. Apply changes
    if err := pup.applyChanges(codeChanges); err != nil {
        return UpdateResult{Error: err.Error()}
    }

    // 5. Generate Hermes bundle if mobile project
    if req.TargetProject == "talos" {
        bundle := pup.generateHermesBundle(codeChanges)
        if err := pup.pushHermesBundle(bundle); err != nil {
            return UpdateResult{Error: err.Error()}
        }
    }

    // 6. Auto-deploy if requested
    if req.AutoDeploy {
        return pup.deploymentRouter.Deploy(req.TargetProject)
    }

    return UpdateResult{Success: true}
}
```

3. **Mobile-Friendly Admin UI** (`web/components/talos-admin/MobileAdminPanel.tsx`)
```typescript
const MobileAdminPanel = () => {
  const [activeTab, setActiveTab] = useState<'updates' | 'deploy' | 'monitor'>('updates');
  const [updatePrompt, setUpdatePrompt] = useState('');
  const [updateResult, setUpdateResult] = useState<UpdateResult | null>(null);

  const handleUpdateRequest = async () => {
    const result = await yaverSDK.processCodeUpdate({
      prompt: updatePrompt,
      targetProject: 'talos',
      updateType: 'bugfix',
      autoDeploy: false
    });
    setUpdateResult(result);
  };

  return (
    <View className="mobile-admin-panel">
      <Tabs value={activeTab} onChange={setActiveTab}>
        <Tab value="updates">Code Updates</Tab>
        <Tab value="deploy">Deployments</Tab>
        <Tab value="monitor">Monitoring</Tab>
      </Tabs>

      {activeTab === 'updates' && (
        <CodeUpdateInterface
          prompt={updatePrompt}
          onPromptChange={setUpdatePrompt}
          onSubmit={handleUpdateRequest}
          result={updateResult}
        />
      )}

      {activeTab === 'deploy' && (
        <DeploymentInterface />
      )}

      {activeTab === 'monitor' && (
        <MonitoringInterface />
      )}
    </View>
  );
};
```

### 5. Hermes Bundle Hot-Reload System

**Purpose**: Enable rapid mobile development iterations with instant code updates.

**Architecture**:
```
Code Change → Hermes Compiler → Bundle Push → Hot Reload
                                        ↓
                                    Mobile Preview
```

**Implementation Plan**:

1. **Hermes Bundle Manager** (`desktop/agent/hermes_manager.go`)
```go
type HermesBundleManager struct {
    compiler   HermesCompiler
    pusher     BundlePusher
    reloadChan chan ReloadRequest
}

type ReloadRequest struct {
    ProjectDir  string
    DeviceID    string
    BundleType  string // "full" "incremental"
    Force       bool
}

func (hbm *HermesBundleManager) HotReload(req ReloadRequest) error {
    // 1. Compile bundle
    bundle, err := hbm.compiler.Compile(req.ProjectDir, req.BundleType)
    if err != nil {
        return fmt.Errorf("compile failed: %w", err)
    }

    // 2. Push to device
    if err := hbm.pusher.Push(req.DeviceID, bundle); err != nil {
        return fmt.Errorf("push failed: %w", err)
    }

    // 3. Trigger reload
    return hbm.triggerReload(req.DeviceID)
}
```

## Integration Architecture

### End-to-End Flow

**1. User requests code update from mobile:**
```
User → Mobile Admin UI → Yaver Web SDK → Agent Runtime
```

**2. Agent processes update on Hetzner:**
```
Agent Runtime → Code Generation → Validation → Hermes Bundle
```

**3. Deployment pipeline:**
```
Hermes Bundle → Mobile Hot Reload → Preview
              ↓
         (if approved)
              ↓
         Deployment Router → Platform Deploy
```

### Multi-Agent Orchestration

**Agent Selection Flow:**
```go
func SelectAgentForTask(task Task) Agent {
    // 1. Check task requirements
    if task.RequiresCodeGeneration {
        return SelectBestCodeAgent()
    }
    if task.RequiresAnalysis {
        return SelectBestAnalysisAgent()
    }
    if task.RequiresDeployment {
        return SelectBestDeploymentAgent()
    }

    // 2. Consider user preferences
    if task.PreferredAgent != "" {
        return GetAgent(task.PreferredAgent)
    }

    // 3. Default to best performing agent
    return GetBestPerformingAgent()
}
```

## Security & Access Control

### Admin ACL Implementation

```typescript
const checkPermission = (user: AdminUser, resource: string, action: string): boolean => {
  for (const role of user.roles) {
    const permissions = ADMIN_ROLES[role];
    for (const perm of permissions) {
      if (perm.resource === '*' || perm.resource === resource) {
        if (perm.actions.includes('*') || perm.actions.includes(action)) {
          return true;
        }
      }
    }
  }
  return false;
};
```

### Runtime Security

1. **Secret Management**: All secrets stored in Yaver vault, never in code
2. **Agent Isolation**: Each agent runs in isolated environment
3. **Audit Logging**: All admin actions logged with user attribution
4. **Approval Workflows**: Critical changes require multi-admin approval
5. **Rollback Safety**: Automatic rollback on deployment failure

## Performance Optimization

### Runtime Selection Strategy

```go
func SelectOptimalRuntime(task Task) Runtime {
    candidates := []Runtime{}

    // 1. Filter by capability
    for _, runtime := range availableRuntimes {
        if runtime.HasCapability(task.RequiredCapabilities) {
            candidates = append(candidates, runtime)
        }
    }

    // 2. Sort by performance metrics
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].PerformanceScore() > candidates[j].PerformanceScore()
    })

    // 3. Load balancing
    return selectLeastLoaded(candidates)
}
```

### Cost Optimization

```go
func OptimizeDeploymentCost(deployment Deployment) OptimizedPlan {
    plan := OptimizedPlan{}

    // 1. Check for cached builds
    if cachedBuild := findCachedBuild(deployment); cachedBuild != nil {
        plan.UseCache = true
        plan.CachedBuild = cachedBuild
        return plan
    }

    // 2. Select cheapest capable runtime
    plan.Runtime = selectCheapestRuntime(deployment.Requirements)

    // 3. Batch deployments if possible
    if canBatchDeployment(deployment) {
        plan.BatchWith = findCompatibleDeployments(deployment)
    }

    return plan
}
```

## Monitoring & Observability

### Health Checks

```go
type SystemHealth struct {
    Runtimes    []RuntimeHealth
    Agents      []AgentHealth
    Deployments []DeploymentHealth
    Mobile      MobileHealth
}

func (sh *SystemHealth) OverallStatus() string {
    if sh.hasCriticalFailures() {
        return "critical"
    }
    if sh.hasWarnings() {
        return "degraded"
    }
    return "healthy"
}
```

### Performance Metrics

```go
type PerformanceMetrics struct {
    TaskLatency      map[string]time.Duration
    AgentPerformance map[string]AgentStats
    DeploymentTimes  map[string]DeploymentStats
    ErrorRates       map[string]float64
    CostTracking     map[string]CostMetrics
}
```

## Implementation Phases

### Phase 1: Core Infrastructure (Week 1-2)
- Agent orchestration layer
- Hetzner provisioning service
- Basic deployment routing

### Phase 2: Mobile Integration (Week 3-4)
- Mobile admin UI
- Yaver web SDK integration
- ACL system implementation

### Phase 3: Advanced Features (Week 5-6)
- Hermes hot-reload system
- Mac mini pool management
- Performance optimization

### Phase 4: Testing & Rollout (Week 7-8)
- Comprehensive testing
- Documentation
- Gradual rollout

## Success Criteria

- ✅ Seamless agent switching from mobile < 2 seconds
- ✅ Hetzner provisioning < 5 minutes
- ✅ Cross-platform deployment < 10 minutes
- ✅ Mobile code updates with live preview < 30 seconds
- ✅ 99.9% uptime for critical services
- ✅ < $50/month incremental infrastructure costs

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Agent API downtime | High | Multi-agent support with failover |
| Hetzner rate limits | Medium | Request queueing and retry logic |
| iOS build failures | High | Mac mini pool with multiple devices |
| Mobile network issues | Medium | Offline mode with sync |
| Security vulnerabilities | Critical | Comprehensive audit and testing |

This architecture provides a comprehensive solution for multi-agent AI development with remote runtime management, self-improving capabilities, and mobile-first user experience.