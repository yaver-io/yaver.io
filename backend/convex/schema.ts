import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

export default defineSchema({
  users: defineTable({
    userId: v.string(),
    email: v.string(),
    fullName: v.string(),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
      v.literal("email"),
    ),
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

  authIdentities: defineTable({
    userId: v.id("users"),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
      v.literal("email"),
    ),
    providerId: v.string(),
    email: v.optional(v.string()),
    createdAt: v.number(),
    lastUsedAt: v.number(),
  })
    .index("by_provider", ["provider", "providerId"])
    .index("by_userId", ["userId"]),

  oauthLinkIntents: defineTable({
    token: v.string(),
    userId: v.id("users"),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
    ),
    client: v.optional(v.string()),
    returnTo: v.optional(v.string()),
    expiresAt: v.number(),
    createdAt: v.number(),
  })
    .index("by_token", ["token"])
    .index("by_userId", ["userId"]),

  // accountMergeIntents let a signed-in user request that another of their
  // accounts be merged INTO the current one. The target (currently signed
  // in) creates an intent and gets a short-lived token. Someone signed
  // into the SOURCE account then exchanges that token + their own session
  // to complete the merge — merging is irreversible so we require the
  // source user to actively consent by signing in on the approval URL.
  accountMergeIntents: defineTable({
    token: v.string(),           // short random token carried in URL
    targetUserId: v.id("users"), // account that stays; receives source's data
    targetEmail: v.string(),     // cached for approval-page UX
    status: v.union(v.literal("pending"), v.literal("completed"), v.literal("cancelled")),
    client: v.optional(v.string()),
    expiresAt: v.number(),
    createdAt: v.number(),
    completedAt: v.optional(v.number()),
  })
    .index("by_token", ["token"])
    .index("by_targetUserId", ["targetUserId"]),

  passwordResets: defineTable({
    tokenHash: v.string(),          // SHA-256 of the reset token
    email: v.string(),              // email this reset is for
    userId: v.id("users"),
    expiresAt: v.number(),          // 1 hour TTL
    usedAt: v.optional(v.number()), // set when token is consumed
    createdAt: v.number(),
  })
    .index("by_tokenHash", ["tokenHash"])
    .index("by_email", ["email"]),

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
    deviceId: v.optional(v.string()),
    expiresAt: v.number(),
    createdAt: v.number(),
  })
    .index("by_tokenHash", ["tokenHash"])
    .index("by_userId", ["userId"])
    .index("by_deviceId", ["deviceId"]),

  devices: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    name: v.string(),
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux"),
      v.literal("android"),
      v.literal("ios")
    ),
    deviceClass: v.optional(
      v.union(
        v.literal("desktop"),
        v.literal("edge-mobile"),
        v.literal("server")
      )
    ),
    edgeProfile: v.optional(v.object({
      supportsLocalInference: v.boolean(),
      maxModelClass: v.union(
        v.literal("none"),
        v.literal("tiny"),
        v.literal("small"),
        v.literal("medium")
      ),
      preferredTasks: v.array(v.union(
        v.literal("speech"),
        v.literal("ocr"),
        v.literal("vision"),
        v.literal("embedding"),
        v.literal("rerank"),
        v.literal("automation"),
        v.literal("small-llm")
      )),
      memoryMb: v.optional(v.number()),
      batteryPct: v.optional(v.number()),
      isCharging: v.optional(v.boolean()),
      thermalState: v.optional(
        v.union(
          v.literal("nominal"),
          v.literal("warm"),
          v.literal("hot")
        )
      ),
    })),
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
    // Bootstrap state: true when agent is running without a valid token.
    // Clients show a "NEEDS AUTH" badge and can auto-pair via relay.
    needsAuth: v.optional(v.boolean()),
    // hardwareId is a stable per-machine fingerprint reported by
    // the agent on registration and every heartbeat. Used by the
    // remote-OAuth-trigger flow: when an agent loses its token
    // and re-enters bootstrap mode, the original host can call
    // /auth/recover with their Convex token and we look up the
    // device by hardwareId to confirm they own it.
    hardwareId: v.optional(v.string()),
  })
    .index("by_userId", ["userId"])
    .index("by_deviceId", ["deviceId"])
    .index("by_hardwareId", ["hardwareId"]),

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

  // Public install catalogue — the Go agent fetches this on startup
  // and merges it with its built-in list. Lets new tools / new OS
  // install commands ship without a CLI release. Every field is
  // intentionally NON-SENSITIVE: tool name, one-line description,
  // and an array of `(packageManager, command)` pairs. No URLs to
  // private infra, no credentials, no customer data.
  //
  // `kind` groups the tool so UIs can section them: "ai-runner" for
  // claude-code / codex / aider, "model-runtime" for ollama + lm
  // studio, "language" for node / python / go / rust, "devtool"
  // for ripgrep / fd / bat / jq / gh / docker / sqlite, "system"
  // for things that only make sense once (tailscale, cloudflared).
  packageRegistry: defineTable({
    name: v.string(),                 // tool slug, e.g. "ollama"
    kind: v.string(),                 // category hint (see comment)
    description: v.string(),          // one-line summary for UIs
    tags: v.optional(v.array(v.string())),
    // Install steps as a flat array so an agent can pick the first
    // one whose `packageManager` it has on PATH. `platform` is
    // optional — when set ("darwin", "linux", "windows") the agent
    // must also match GOOS. `packageManager` of "" means "run the
    // command verbatim" (for one-line curl installers etc.).
    installs: v.array(v.object({
      platform: v.optional(v.string()),
      packageManager: v.string(),
      command: v.string(),
    })),
    // `checkCommand` is executed to decide whether the tool is
    // already installed. Empty = fall back to `which <name>`.
    checkCommand: v.optional(v.string()),
    // `docUrl` points at an authoritative page for users who want
    // to read more. Optional and always public.
    docUrl: v.optional(v.string()),
    sortOrder: v.number(),
    updatedAt: v.number(),
  })
    .index("by_name", ["name"])
    .index("by_kind", ["kind"]),

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
    machineName: v.optional(v.string()),
    platform: v.optional(v.string()),
    arch: v.optional(v.string()),
    shell: v.optional(v.string()),
    environment: v.optional(v.string()),
    runtimeVersion: v.optional(v.string()),
    preferredProvider: v.optional(v.string()),
    isWsl: v.optional(v.boolean()),
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
    // Hash of a long-lived machine-auth token generated at provisioning
    // time. The plaintext is placed on the box in /etc/yaver/machine.json
    // (root-owned, 0600) so the TLS reconciler systemd timer can call
    // /machine/pending-tls on Convex. We never store the plaintext.
    machineTokenHash: v.optional(v.string()),
  }).index("by_user", ["userId"])
    .index("by_team", ["teamId"]),

  // User-bound custom domains. Independent of the auto-generated
  // <shortId>.cloud.yaver.io / <shortId>.relay.yaver.io hostnames, so a user
  // can bring their own myapp.com from Namecheap / Porkbun / Route53 and
  // point it at either a cloud machine or a managed relay.
  //
  // `targetType` + `targetId` identify where the domain should route to
  // (cloud_machine → cloudMachines._id, managed_relay → managedRelays._id,
  //  custom_server → raw IP in `targetIp`). Verification flow:
  //   1. User enters domain + chooses target.
  //   2. Convex returns a TXT-record challenge (verificationToken) and an
  //      A/CNAME record spec for the target (serverIp / autoDomain).
  //   3. User adds both records at their registrar / DNS host.
  //   4. verify() polls DNS, flips `status` to "verified" when both appear.
  //   5. Once verified, the target's nginx/Caddy config is updated to
  //      accept the custom hostname and certbot issues a TLS cert.
  userDomains: defineTable({
    userId: v.id("users"),
    domain: v.string(),              // "myapp.com" or "api.myapp.com"
    targetType: v.union(
      v.literal("cloud_machine"),
      v.literal("managed_relay"),
      v.literal("custom_server"),
    ),
    targetId: v.optional(v.string()),  // Convex id of target (as string)
    targetIp: v.optional(v.string()),  // IPv4 of the current target — kept
                                       // here so the UI can print DNS
                                       // instructions without re-joining.
    autoDomain: v.optional(v.string()), // "<shortId>.cloud.yaver.io" for
                                        // CNAME-based setups.
    dnsProvider: v.optional(v.string()), // "cloudflare" | "manual" | ...
    verificationToken: v.string(),     // the user adds this as a TXT record
                                       // to prove ownership.
    status: v.union(
      v.literal("pending"),            // waiting for DNS records
      v.literal("verified"),           // records observed, TLS being issued
      v.literal("active"),             // TLS cert issued, domain live
      v.literal("error"),
    ),
    errorMessage: v.optional(v.string()),
    createdAt: v.number(),
    updatedAt: v.number(),
    verifiedAt: v.optional(v.number()),
  })
    .index("by_user", ["userId"])
    .index("by_domain", ["domain"])
    .index("by_target", ["targetType", "targetId"]),

  // Guest access — let other users connect to your agent (peer-to-peer sharing)
  guestInvitations: defineTable({
    hostUserId: v.id("users"),       // who is sharing their machine
    guestEmail: v.string(),          // invited user's email (hint — code also works). Empty string when invited by userId.
    inviteCode: v.string(),          // 6-char code for acceptance (works even if emails differ)
    status: v.union(v.literal("pending"), v.literal("accepted"), v.literal("revoked")),
    guestUserId: v.optional(v.id("users")),  // set when accepted, OR pre-set when host invited by userId
    invitedByUserId: v.optional(v.boolean()), // true if the host typed a userId (not an email)
    // Host's proposed device scope at invite time. Guest sees this and can trim it on accept.
    // Absent / empty = propose all host devices.
    proposedDeviceIds: v.optional(v.array(v.string())),
    createdAt: v.number(),
    expiresAt: v.number(),           // pending invitations expire after 2 days
    acceptedAt: v.optional(v.number()),
    revokedAt: v.optional(v.number()),
  })
    .index("by_hostUserId", ["hostUserId"])
    .index("by_guestEmail", ["guestEmail"])
    .index("by_host_guest", ["hostUserId", "guestEmail"])
    .index("by_host_guestUser", ["hostUserId", "guestUserId"])
    .index("by_guestUserId", ["guestUserId"])
    .index("by_inviteCode", ["inviteCode"]),

  guestAccess: defineTable({
    hostUserId: v.id("users"),       // machine owner
    guestUserId: v.id("users"),      // guest who has access
    grantedAt: v.number(),
    revokedAt: v.optional(v.number()),  // null = active, set = revoked
    // Guest config — set by host to control guest access
    dailyTokenLimit: v.optional(v.number()),    // max task-seconds per day (0 or absent = unlimited)
    allowedRunners: v.optional(v.array(v.string())), // runner IDs guest can use (empty/absent = all)
    usageMode: v.optional(v.string()),          // "idle-only" (default), "always", "scheduled"
    schedule: v.optional(v.object({
      startHour: v.number(),
      endHour: v.number(),
      timezone: v.optional(v.string()),
    })),
  })
    .index("by_hostUserId", ["hostUserId"])
    .index("by_guestUserId", ["guestUserId"])
    .index("by_host_guest", ["hostUserId", "guestUserId"]),

  // Explicit infra grants — hosts can share selected devices/machines with
  // another user without giving them blanket access to the whole account.
  infraAccessGrants: defineTable({
    hostUserId: v.id("users"),
    guestUserId: v.id("users"),
    status: v.union(v.literal("active"), v.literal("revoked")),
    resourcePreset: v.optional(v.string()),
    shareAllDevices: v.optional(v.boolean()),
    shareAllMachines: v.optional(v.boolean()),
    useHostApiKeys: v.optional(v.boolean()),
    allowGuestProvidedApiKeys: v.optional(v.boolean()),
    allowDesktopControl: v.optional(v.boolean()),
    allowBrowserControl: v.optional(v.boolean()),
    allowTunnelForward: v.optional(v.boolean()),
    requireIsolation: v.optional(v.boolean()),
    cpuLimitPercent: v.optional(v.number()),
    ramLimitMb: v.optional(v.number()),
    priorityMode: v.optional(v.string()), // "same-priority" | "spare-capacity" | "background"
    allowedRunners: v.optional(v.array(v.string())),
    usageMode: v.optional(v.string()),
    schedule: v.optional(v.object({
      startHour: v.number(),
      endHour: v.number(),
      timezone: v.optional(v.string()),
    })),
    grantedAt: v.number(),
    updatedAt: v.number(),
    revokedAt: v.optional(v.number()),
  })
    .index("by_hostUserId", ["hostUserId"])
    .index("by_guestUserId", ["guestUserId"])
    .index("by_host_guest", ["hostUserId", "guestUserId"]),

  infraAccessGrantDevices: defineTable({
    grantId: v.id("infraAccessGrants"),
    hostUserId: v.id("users"),
    guestUserId: v.id("users"),
    deviceId: v.string(),
    createdAt: v.number(),
  })
    .index("by_grant", ["grantId"])
    .index("by_guestUserId", ["guestUserId"])
    .index("by_hostUserId", ["hostUserId"])
    .index("by_device_guest", ["deviceId", "guestUserId"]),

  infraAccessGrantMachines: defineTable({
    grantId: v.id("infraAccessGrants"),
    hostUserId: v.id("users"),
    guestUserId: v.id("users"),
    machineId: v.id("cloudMachines"),
    createdAt: v.number(),
  })
    .index("by_grant", ["grantId"])
    .index("by_guestUserId", ["guestUserId"])
    .index("by_hostUserId", ["hostUserId"])
    .index("by_machine_guest", ["machineId", "guestUserId"]),

  // Guest usage tracking — daily task-seconds consumed per guest
  guestUsage: defineTable({
    hostUserId: v.id("users"),
    guestUserId: v.id("users"),
    date: v.string(),              // "2026-04-06"
    secondsUsed: v.number(),
  })
    .index("by_host_guest_date", ["hostUserId", "guestUserId", "date"])
    .index("by_hostUserId_date", ["hostUserId", "date"]),

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

  // Projects — synced from each agent via /projects/sync. Source of truth for
  // the dashboard overview grid, recent activity feed, and cross-machine
  // project discovery.
  userProjects: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),         // Yaver device id where this project lives
    slug: v.string(),              // filesystem basename
    // path deliberately omitted — absolute paths contain the user's
    // home-dir username; privacy contract keeps them on the agent.
    // The field remains optional for back-compat with rows written
    // before the cutoff; new rows are never given one.
    path: v.optional(v.string()),
    name: v.string(),
    stack: v.optional(v.string()), // nextjs, vite, expo, hono, etc.
    backend: v.optional(v.string()),
    auth: v.optional(v.string()),
    activeEnv: v.optional(v.string()),
    localPort: v.optional(v.number()),
    tunnelUrl: v.optional(v.string()),
    gitBranch: v.optional(v.string()),
    lastCommit: v.optional(v.string()),
    status: v.union(
      v.literal("running"),
      v.literal("stopped"),
      v.literal("error"),
      v.literal("creating"),
    ),
    updatedAt: v.number(),
  }).index("by_user", ["userId", "updatedAt"])
    .index("by_device", ["deviceId"])
    .index("by_user_slug", ["userId", "slug"]),

  // Services running on each device — synced from /services/status.
  userServices: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    projectSlug: v.optional(v.string()),
    name: v.string(),
    image: v.optional(v.string()),
    port: v.number(),
    status: v.string(),
    cpuPercent: v.optional(v.number()),
    ramMB: v.optional(v.number()),
    updatedAt: v.number(),
  }).index("by_user", ["userId", "updatedAt"])
    .index("by_device", ["deviceId"]),

  // Deployment records from /deploy/list fanned out into Convex.
  userDeployments: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    projectSlug: v.string(),
    deployId: v.string(),
    target: v.optional(v.string()),     // vercel, cloudflare, fly, vps
    environment: v.optional(v.string()),
    status: v.string(),                 // success, failed, rolled-back
    commit: v.optional(v.string()),
    message: v.optional(v.string()),
    duration: v.optional(v.string()),
    startedAt: v.number(),
    finishedAt: v.optional(v.number()),
  }).index("by_user", ["userId", "startedAt"])
    .index("by_project", ["userId", "projectSlug"]),

  // Agent audit log mirrored into Convex for cross-machine activity feed.
  userActivity: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    action: v.string(),
    target: v.optional(v.string()),
    outcome: v.string(),
    error: v.optional(v.string()),
    timestamp: v.number(),
  }).index("by_user", ["userId", "timestamp"]),
});
