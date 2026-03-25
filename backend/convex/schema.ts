import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

export default defineSchema({
  users: defineTable({
    userId: v.string(),
    email: v.string(),
    fullName: v.string(),
    provider: v.union(v.literal("google"), v.literal("microsoft"), v.literal("apple"), v.literal("email")),
    passwordHash: v.optional(v.string()),
    surveyCompleted: v.optional(v.boolean()),
    providerId: v.string(),
    avatarUrl: v.optional(v.string()),
    totpSecret: v.optional(v.string()),
    totpEnabled: v.optional(v.boolean()),
    totpRecoveryCodes: v.optional(v.string()),
    createdAt: v.number(),
  })
    .index("by_email", ["email"])
    .index("by_provider", ["provider", "providerId"]),

  pendingAuth: defineTable({
    pendingToken: v.string(),
    userId: v.id("users"),
    attempts: v.number(),
    expiresAt: v.number(),
    createdAt: v.number(),
  })
    .index("by_pendingToken", ["pendingToken"]),

  sessions: defineTable({
    tokenHash: v.string(),
    userId: v.id("users"),
    expiresAt: v.number(),
    createdAt: v.number(),
  })
    .index("by_tokenHash", ["tokenHash"])
    .index("by_userId", ["userId"]),

  devices: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    name: v.string(),
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux")
    ),
    publicKey: v.optional(v.string()),
    quicHost: v.string(),
    quicPort: v.number(),
    isOnline: v.boolean(),
    runnerDown: v.optional(v.boolean()),  // true when runner crashed and all retries exhausted
    runners: v.optional(v.array(v.object({
      taskId: v.string(),
      runnerId: v.string(),
      model: v.optional(v.string()),
      pid: v.number(),
      status: v.string(),
      title: v.string(),
    }))),
    lastHeartbeat: v.number(),
    createdAt: v.number(),
  })
    .index("by_userId", ["userId"])
    .index("by_deviceId", ["deviceId"]),

  downloads: defineTable({
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux"),
      v.literal("android"),
      v.literal("ios")
    ),
    arch: v.string(),
    format: v.string(),
    version: v.string(),
    filename: v.string(),
    storageId: v.id("_storage"),
    size: v.number(),
    createdAt: v.number(),
  })
    .index("by_platform", ["platform"])
    .index("by_platform_arch_format", ["platform", "arch", "format"]),

  developerSurveys: defineTable({
    userId: v.id("users"),
    isDeveloper: v.boolean(),
    languages: v.optional(v.array(v.string())),
    experienceLevel: v.optional(v.string()),
    role: v.optional(v.string()),
    companySize: v.optional(v.string()),
    useCase: v.optional(v.string()),
    completedAt: v.number(),
  }).index("by_userId", ["userId"]),

  platformConfig: defineTable({
    key: v.string(),
    value: v.string(),
    updatedAt: v.number(),
  }).index("by_key", ["key"]),

  authLogs: defineTable({
    level: v.union(v.literal("info"), v.literal("error"), v.literal("warn")),
    provider: v.string(),
    step: v.string(),
    message: v.string(),
    details: v.optional(v.string()),
    createdAt: v.number(),
  }).index("by_createdAt", ["createdAt"]),

  userSettings: defineTable({
    userId: v.id("users"),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
    // Speech-to-text settings
    speechProvider: v.optional(v.string()),      // "on-device" | "openai" | "deepgram" | "assemblyai"
    speechApiKey: v.optional(v.string()),         // API key for cloud providers
    ttsEnabled: v.optional(v.boolean()),          // read responses aloud
    verbosity: v.optional(v.number()),            // 0-10: response detail level (0=summary, 10=full detail)
    keyStorage: v.optional(v.string()),            // "local" | "cloud" — where API keys are stored
  }).index("by_userId", ["userId"]),

  aiRunners: defineTable({
    runnerId: v.string(),
    name: v.string(),
    command: v.string(),
    args: v.string(), // JSON array as string
    outputMode: v.union(v.literal("stream-json"), v.literal("raw")),
    resumeSupported: v.boolean(),
    resumeArgs: v.optional(v.string()), // JSON array as string
    exitCommand: v.optional(v.string()), // e.g. "/exit", "/quit"
    description: v.string(),
    isDefault: v.optional(v.boolean()),
    sortOrder: v.number(),
  }).index("by_runnerId", ["runnerId"]),

  // Available AI models per runner (managed by us, not by users)
  aiModels: defineTable({
    modelId: v.string(),        // e.g. "sonnet", "opus", "haiku", "o3-mini"
    runnerId: v.string(),       // which runner this model belongs to
    name: v.string(),           // display name, e.g. "Claude Sonnet"
    description: v.optional(v.string()),
    isDefault: v.optional(v.boolean()), // default model for this runner
    sortOrder: v.number(),
  })
    .index("by_runnerId", ["runnerId"])
    .index("by_modelId", ["modelId", "runnerId"]),

  // Per-minute CPU/RAM metrics from desktop agents (last 1 hour kept)
  deviceMetrics: defineTable({
    deviceId: v.string(),       // matches devices.deviceId
    timestamp: v.number(),      // epoch ms
    cpuPercent: v.number(),     // 0-100
    memoryUsedMb: v.number(),
    memoryTotalMb: v.number(),
  })
    .index("by_deviceId", ["deviceId", "timestamp"]),

  // Device lifecycle events (crashes, restarts, OOM, etc.)
  deviceEvents: defineTable({
    deviceId: v.string(),
    event: v.union(
      v.literal("crash"),
      v.literal("restart"),
      v.literal("oom"),
      v.literal("started"),
      v.literal("stopped"),
    ),
    details: v.optional(v.string()),
    timestamp: v.number(),
  })
    .index("by_deviceId", ["deviceId", "timestamp"]),

  // Runner usage tracking — how long each AI agent ran per task
  runnerUsage: defineTable({
    userId: v.string(),           // owner of the device
    deviceId: v.string(),         // which device ran it
    taskId: v.string(),           // task identifier
    runner: v.string(),           // "claude", "codex", "aider", etc.
    model: v.optional(v.string()), // "sonnet", "opus", etc.
    durationSec: v.number(),      // how many seconds the runner ran
    startedAt: v.number(),        // epoch ms when task started
    finishedAt: v.number(),       // epoch ms when task finished
    source: v.optional(v.string()), // "mobile", "cli", "mcp"
  })
    .index("by_userId", ["userId", "startedAt"])
    .index("by_deviceId", ["deviceId", "startedAt"]),

  // Daily task counts per user — simple counter for analytics dashboard
  dailyTaskCounts: defineTable({
    userId: v.string(),         // matches users.userId
    date: v.string(),           // "YYYY-MM-DD"
    taskCount: v.number(),
  })
    .index("by_userId_date", ["userId", "date"]),

  developerLogs: defineTable({
    userId: v.optional(v.string()),
    email: v.optional(v.string()),
    source: v.union(v.literal("agent"), v.literal("mobile"), v.literal("web"), v.literal("relay")),
    level: v.union(v.literal("info"), v.literal("error"), v.literal("warn"), v.literal("debug")),
    tag: v.string(),
    message: v.string(),
    data: v.optional(v.string()), // JSON blob
    createdAt: v.number(),
  })
    .index("by_createdAt", ["createdAt"])
    .index("by_email", ["email", "createdAt"]),

  deviceCodes: defineTable({
    userCode: v.string(),
    deviceCode: v.string(),
    status: v.union(v.literal("pending"), v.literal("authorized"), v.literal("expired")),
    pendingToken: v.optional(v.string()),
    expiresAt: v.number(),
    createdAt: v.number(),
  })
    .index("by_userCode", ["userCode"])
    .index("by_deviceCode", ["deviceCode"]),

  // Managed relay subscriptions (LemonSqueezy payments)
  subscriptions: defineTable({
    userId: v.id("users"),
    plan: v.string(), // "relay-monthly" | "relay-yearly"
    status: v.string(), // "active" | "past_due" | "cancelled" | "expired"
    lemonSqueezyId: v.string(), // LemonSqueezy subscription ID
    lemonSqueezyCustomerId: v.string(),
    currentPeriodEnd: v.number(), // Unix timestamp
    cancelledAt: v.optional(v.number()),
    createdAt: v.number(),
    updatedAt: v.number(),
  }).index("by_user", ["userId"])
    .index("by_lemon_id", ["lemonSqueezyId"])
    .index("by_status", ["status"]),

  // Managed relay servers (provisioned on Hetzner)
  managedRelays: defineTable({
    userId: v.id("users"),
    subscriptionId: v.id("subscriptions"),
    status: v.string(), // "provisioning" | "active" | "stopping" | "stopped" | "error"
    hetznerServerId: v.optional(v.string()),
    serverIp: v.optional(v.string()),
    domain: v.optional(v.string()), // e.g. "abc123.relay.yaver.io"
    region: v.string(), // "eu" | "us" — Hetzner datacenter
    password: v.string(), // relay password (auto-generated)
    quicPort: v.number(),
    httpPort: v.number(),
    createdAt: v.number(),
    updatedAt: v.number(),
    lastHealthCheck: v.optional(v.number()),
    errorMessage: v.optional(v.string()),
  }).index("by_user", ["userId"])
    .index("by_subscription", ["subscriptionId"])
    .index("by_status", ["status"]),

  // Teams (shared machines, centralized billing)
  teams: defineTable({
    teamId: v.string(),             // short unique ID (e.g. "team_abc123")
    name: v.string(),               // "Acme Engineering"
    ownerId: v.id("users"),         // admin/billing owner
    plan: v.string(),               // "cpu" | "gpu" | "custom"
    maxMembers: v.number(),         // seat limit
    subscriptionId: v.optional(v.id("subscriptions")),
    createdAt: v.number(),
    updatedAt: v.number(),
  }).index("by_teamId", ["teamId"])
    .index("by_owner", ["ownerId"]),

  // Team membership (who has access to which team's machines)
  teamMembers: defineTable({
    teamId: v.string(),
    userId: v.id("users"),
    role: v.string(),               // "admin" | "member"
    invitedBy: v.optional(v.id("users")),
    joinedAt: v.number(),
  }).index("by_team", ["teamId"])
    .index("by_user", ["userId"])
    .index("by_team_user", ["teamId", "userId"]),

  // Cloud dev machines (provisioned on Hetzner, subscription required)
  cloudMachines: defineTable({
    userId: v.id("users"),
    teamId: v.optional(v.string()),   // if team-owned, all team members can access
    subscriptionId: v.optional(v.id("subscriptions")),
    machineType: v.string(),          // "cpu" | "gpu"
    status: v.string(),               // "provisioning" | "active" | "stopping" | "stopped" | "error"
    multiUser: v.optional(v.boolean()), // true for shared team machines
    hetznerServerId: v.optional(v.string()),
    serverIp: v.optional(v.string()),
    hostname: v.optional(v.string()),
    region: v.string(),               // "eu" | "us"
    tools: v.array(v.string()),       // ["nodejs", "python", "go", "docker", ...]
    repoUrl: v.optional(v.string()),  // cloned on provisioning
    sshPublicKey: v.optional(v.string()),
    specs: v.optional(v.object({
      vcpu: v.number(),
      ramGb: v.number(),
      diskGb: v.number(),
      arch: v.string(),               // "arm64" | "amd64"
      gpu: v.optional(v.string()),    // "rtx4000" | null
      vram: v.optional(v.number()),   // GB
    })),
    createdAt: v.number(),
    updatedAt: v.number(),
    lastHealthCheck: v.optional(v.number()),
    errorMessage: v.optional(v.string()),
  }).index("by_user", ["userId"])
    .index("by_team", ["teamId"]),

  // SDK tokens — long-lived tokens for Feedback SDK (independent from CLI session tokens)
  sdkTokens: defineTable({
    tokenHash: v.string(),        // SHA-256 of the raw token
    userId: v.id("users"),        // owner — must match CLI user
    label: v.optional(v.string()), // human-readable label (e.g. "AcmeStore dev build")
    scopes: v.optional(v.array(v.string())), // allowed scopes: "feedback","blackbox","voice","builds"
    allowedCIDRs: v.optional(v.array(v.string())), // IP binding: "192.168.1.0/24"
    replacedBy: v.optional(v.string()),  // tokenHash of replacement (rotation)
    replacedAt: v.optional(v.number()),  // when replaced (5min grace period)
    expiresAt: v.number(),        // 1 year from creation (or custom)
    createdAt: v.number(),
  })
    .index("by_tokenHash", ["tokenHash"])
    .index("by_userId", ["userId"]),

  // Security events — new device IP alerts, token usage anomalies
  securityEvents: defineTable({
    userId: v.id("users"),
    eventType: v.string(),        // "new_ip", "token_rotated", "token_revoked"
    details: v.string(),          // JSON blob with event-specific data
    read: v.boolean(),
    createdAt: v.number(),
  })
    .index("by_userId", ["userId", "createdAt"]),

  mobileStreamLogs: defineTable({
    userId: v.optional(v.string()),
    platform: v.string(),
    appVersion: v.string(),
    buildNumber: v.string(),
    level: v.union(v.literal("info"), v.literal("error"), v.literal("warn")),
    step: v.string(),
    message: v.string(),
    details: v.optional(v.string()),
    createdAt: v.number(),
  }).index("by_createdAt", ["createdAt"])
    .index("by_userId", ["userId", "createdAt"]),
});
