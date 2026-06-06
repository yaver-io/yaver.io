import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

const recoveryPosture = v.object({
  status: v.string(),
  mobileApprovedTransports: v.array(v.string()),
  webApprovedTransports: v.array(v.string()),
  hasPrivateTransport: v.boolean(),
  hasBrowserTransport: v.boolean(),
  publicDirectRecoveryClosed: v.boolean(),
  summary: v.string(),
});

const connectionPreference = v.object({
  kind: v.union(
    v.literal("direct-lan"),
    v.literal("tailscale"),
    v.literal("headscale"),
    v.literal("own-vpn"),
    v.literal("https-tunnel"),
    v.literal("free-relay"),
    v.literal("private-relay")
  ),
  active: v.boolean(),
  preferred: v.boolean(),
  source: v.union(
    v.literal("agent-detected"),
    v.literal("user-config"),
    v.literal("platform-config"),
    v.literal("relay-presence")
  ),
});

const hardwareProfile = v.object({
  os: v.optional(v.string()),
  osVersion: v.optional(v.string()),
  cpu: v.optional(v.string()),
  gpu: v.optional(v.string()),
  ramMb: v.optional(v.number()),
  vramMb: v.optional(v.number()),
  numCores: v.optional(v.number()),
  arch: v.optional(v.string()),
  iosSimulators: v.optional(v.array(v.string())),
  androidEmulators: v.optional(v.array(v.string())),
});

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
      v.literal("passkey"),
      // "oidc" is org-configured SSO via the generic OIDC handler in
      // http.ts (/auth/oidc/*). Distinct from individual provider
      // OAuth so the admin can enforce "OIDC only" and tell at a
      // glance which users came in via SSO.
      v.literal("oidc"),
    ),
    passwordHash: v.optional(v.string()),
    surveyCompleted: v.optional(v.boolean()),
    providerId: v.string(),
    avatarUrl: v.optional(v.string()),
    totpSecret: v.optional(v.string()),
    totpEnabled: v.optional(v.boolean()),
    totpRecoveryCodes: v.optional(v.string()),
    // emailVerified gates email-keyed auto-linking. OAuth-signup users
    // are verified-by-construction (the IdP attested the address).
    // Email + passkey signups start unverified and graduate to true
    // once the user clicks the verify-email link. Backfill is a
    // best-effort scan based on provider; see migrations.
    emailVerified: v.optional(v.boolean()),
    emailVerifiedAt: v.optional(v.number()),
    createdAt: v.number(),
    // Org-wide admin role. The first user is bootstrapped via the
    // env-var owner allowlist (ownerAllowlist.ts) which keeps
    // working as a fall-back; this field is the authoritative gate
    // for everyone promoted via the admin console afterwards.
    platformRole: v.optional(v.union(v.literal("admin"))),
  })
    .index("by_email", ["email"])
    .index("by_provider", ["provider", "providerId"])
    .index("by_platformRole", ["platformRole"]),

  authIdentities: defineTable({
    userId: v.id("users"),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
      v.literal("email"),
      v.literal("passkey"),
      v.literal("oidc"),
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

  // WebAuthn / passkey credentials. Strictly additive to the existing
  // password + OAuth flows: a user can have N passkeys attached to a
  // single users row alongside any other identity. Public-key only —
  // private material lives on the user's device / iCloud Keychain.
  // credentialId is base64url; counter monotonically increases on each
  // assertion to detect cloned authenticators (informational; iCloud-
  // synced passkeys legitimately keep counter=0).
  passkeys: defineTable({
    userId: v.id("users"),
    credentialId: v.string(),       // base64url; unique per credential
    publicKey: v.string(),          // base64url COSE public key
    counter: v.number(),
    transports: v.optional(v.array(v.string())), // "internal" | "hybrid" | ...
    deviceLabel: v.optional(v.string()),         // user-supplied nickname
    backedUp: v.optional(v.boolean()),           // synced via iCloud / Google
    createdAt: v.number(),
    lastUsedAt: v.optional(v.number()),
  })
    .index("by_credentialId", ["credentialId"])
    .index("by_userId", ["userId"]),

  // Short-lived WebAuthn challenges. Issued by registerStart / loginStart
  // and consumed by the Finish steps. Two flavours so a stolen-but-still-
  // valid challenge can't be cross-used between flows. Anonymous challenges
  // (login without a known userId) carry email==null and are matched by
  // challenge value alone — login completes against whichever user owns
  // the credential the browser produced.
  passkeyChallenges: defineTable({
    challenge: v.string(),         // base64url, ~32 random bytes
    purpose: v.union(v.literal("register"), v.literal("login"), v.literal("signup")),
    userId: v.optional(v.id("users")),  // null for anonymous login start
    expiresAt: v.number(),         // ~5 minutes from issuance
    createdAt: v.number(),
  })
    .index("by_challenge", ["challenge"])
    .index("by_expiresAt", ["expiresAt"]),

  // Email verification tokens — issued at email/passkey signup and on
  // explicit re-send from settings. Click the link → users.emailVerified
  // flips true → email-keyed auto-link unlocks. Tokens are single-use,
  // 24-hour TTL, scoped to a specific email + userId so a token leaked
  // from one inbox can't be redeemed against a different account.
  emailVerifications: defineTable({
    token: v.string(),
    userId: v.id("users"),
    email: v.string(),
    expiresAt: v.number(),
    createdAt: v.number(),
    consumedAt: v.optional(v.number()),
  })
    .index("by_token", ["token"])
    .index("by_userId", ["userId"])
    .index("by_expiresAt", ["expiresAt"]),

  sessions: defineTable({
    tokenHash: v.string(),
    userId: v.id("users"),
    deviceId: v.optional(v.string()),
    expiresAt: v.number(),
    createdAt: v.number(),
    // Rotation grace: when a token is rotated (X-Yaver-Rotate-Token),
    // the immediately-previous tokenHash stays valid until this time
    // (~2 min). Token rotation is otherwise instant-and-permanent, so
    // any client that rotates fire-and-forget (mobile has several
    // independent triggers) could strand an in-flight/concurrent
    // request on the now-dead token → blanket 401. This window lets
    // the new token propagate without the old one dying mid-flight.
    prevTokenHash: v.optional(v.string()),
    prevTokenValidUntil: v.optional(v.number()),
  })
    .index("by_tokenHash", ["tokenHash"])
    .index("by_userId", ["userId"])
    .index("by_deviceId", ["deviceId"])
    .index("by_prevTokenHash", ["prevTokenHash"])
    // Lets the daily cleanup cron prune long-expired sessions without
    // scanning the whole table (see cleanup.pruneExpiredSessions).
    .index("by_expiresAt", ["expiresAt"]),

  devices: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    name: v.string(),
    // User-set short alias used by `yaver ssh <alias>`, the dashboard,
    // and the mobile app. Per-user uniqueness is enforced in the
    // setDeviceAlias mutation. Lower-cased and trimmed before storage
    // so lookups don't have to re-normalize.
    alias: v.optional(v.string()),
    // Fleet labels for selector-based ops ("run on all gpu/arm64/edge
    // machines"). Auto-seeded at first registration from platform +
    // hardware (os, arch, gpu, docker, edge); thereafter user-owned via
    // setDeviceTags. Lower-cased, deduped. Powers Fleet.select({tags}) in
    // the yaver SDK and selectDevices below. Privacy-safe: static
    // capability/affinity labels, never a path or secret.
    tags: v.optional(v.array(v.string())),
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
    // Which app stores this device can build+publish to, e.g.
    // ["ios","android"] on macOS, ["android"] on Linux. A device that
    // advertises any of these IS a publish-farm node — there is no
    // separate farmNodes table for self-hosted machines (the device
    // row is the registration). Privacy-safe: a static capability
    // list, never a path or secret.
    publishCapabilities: v.optional(v.array(v.string())),
    publicKey: v.optional(v.string()),
    quicHost: v.string(),
    // Every reachable IPv4 address the agent has — preferred outbound
    // first, then any additional LAN/Tailscale/Ethernet/VPN address it
    // is bound to. Mobile clients race them in parallel during connect
    // so the session attaches via whichever path actually has a route
    // from the phone (Tailscale on cellular, Wi-Fi on same LAN, etc.).
    // Optional for backwards-compat with agents that haven't upgraded.
    localIps: v.optional(v.array(v.string())),
    // Public HTTPS origins that can reach this specific device, such as
    // Cloudflare Tunnel front doors or other reverse-proxy endpoints.
    // Optional and device-scoped so the transport resolver can treat them
    // as first-class runtime candidates instead of guessing from account
    // level tunnel settings.
    publicEndpoints: v.optional(v.array(v.string())),
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
    // Durable device capability inventory: which first-class coding CLIs
    // are installed on this machine. Intentionally narrow: presence only,
    // never auth state or credentials.
    installedRunnerIds: v.optional(v.array(v.string())),
    lastHeartbeat: v.number(),
    // Real-time tunnel state pushed by the relay server when an
    // agent's QUIC tunnel registers / deregisters. Optional because
    // only deployments with CONVEX_PRESENCE_URL + _SECRET wired on
    // the relay populate it. When present, clients show tunnel
    // connect/disconnect within ~2s end-to-end.
    lastTunnelEvent: v.optional(v.object({
      online:      v.boolean(),
      at:          v.number(),                     // epoch ms
      peerAddr:    v.optional(v.string()),         // relay-observed source
      connectedAt: v.optional(v.number()),         // epoch ms; matches the connect event
      durationSec: v.optional(v.number()),         // populated on disconnect
    })),
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
    hardwareProfile: v.optional(hardwareProfile),
    recoveryPosture: v.optional(recoveryPosture),
    // First-class connection policy/state for this machine. This is a
    // privacy-safe control-plane summary: transport kind + active/preferred,
    // never relay hostnames, VPN IPs, or secrets. Heartbeat seeds it from
    // current config/detection so clients do not have to infer whether
    // "100.x" means Tailscale, Headscale, or an arbitrary CGNAT address.
    connectionPreferences: v.optional(v.array(connectionPreference)),
    // Version string of the Go agent binary currently running on this
    // device (e.g. "1.99.36"). Reported on register and refreshed at
    // most once every 24 hours via heartbeat — the mutation side is
    // what enforces the cadence so older agents that send on every
    // heartbeat still don't generate unnecessary writes. Missing =
    // "no version info" in the dashboard.
    agentVersion: v.optional(v.string()),
    agentVersionReportedAt: v.optional(v.number()),
  })
    .index("by_userId", ["userId"])
    .index("by_deviceId", ["deviceId"])
    .index("by_hardwareId", ["hardwareId"]),

  // Pending device claims — bootstrap-mode boxes that registered a relay
  // tunnel but have no Convex row yet. Created when a fresh agent runs
  // `yaver serve` with no token AND no prior Convex device record:
  // /devices/bootstrap returns "Device not found", and the agent retries
  // against /devices/bootstrap-pending with its relay password. The
  // password's SHA-256 hash is the only per-user signal we have without
  // a session token — it lets the user's dashboard list "boxes that just
  // joined my relay but I haven't claimed yet" and convert them into
  // owned `devices` rows with one tap.
  //
  // Why a separate table instead of allowing devices.userId to be optional:
  //   - keeps every devices.userId-scoped query (the vast majority) free
  //     of "is this row actually mine" branching;
  //   - the row only lives until the user claims it OR the cron sweeps
  //     stale entries — short-lived state, not real device state;
  //   - mismatch between agent-supplied identity (deviceId/hardwareId/
  //     publicKey) and an existing devices row is a hard rejection
  //     instead of an accidental ownership flip.
  //
  // Lifecycle: created on bootstrap-pending; refreshed on every retry;
  // deleted on claimPendingDevice or by stale-claim sweep (>24h since
  // lastSeenAt with no claim).
  pendingDeviceClaims: defineTable({
    deviceId: v.string(),
    hardwareId: v.string(),
    publicKey: v.string(),
    name: v.optional(v.string()),
    platform: v.optional(v.string()),
    quicHost: v.optional(v.string()),
    quicPort: v.optional(v.number()),
    // SHA-256 hex of the relay password the agent registered with.
    // We never store the plaintext — the user's managedRelays.password
    // gets hashed for the same comparison.
    relayPasswordHash: v.string(),
    firstSeenAt: v.number(),
    lastSeenAt: v.number(),
    // Best-effort label populated when we can resolve the hash to a
    // managedRelay (helps the UI explain "this came in via your relay
    // 'home-mac'"). Optional: self-hosted shared-password setups won't
    // have it.
    relayLabel: v.optional(v.string()),
  })
    .index("by_relayPasswordHash", ["relayPasswordHash"])
    .index("by_deviceId", ["deviceId"])
    .index("by_hardwareId", ["hardwareId"])
    .index("by_lastSeenAt", ["lastSeenAt"]),

  // Rescue command queue — the *only* control channel that survives a
  // broken relay tunnel. The agent's heartbeat (independent network
  // path from the relay) polls here for pending commands and executes
  // them. Web UI / mobile / CLI write into this table when a device
  // is wedged and the normal /agent/* endpoints aren't reachable.
  //
  // Security: command is a strict enum (no arbitrary shell), only the
  // device's owner can queue (enforced in agentRescue.ts), 5-minute
  // TTL caps the replay window, single-claim semantics enforced via
  // status transition. Every queue/claim/result row is mirrored into
  // securityEvents for the audit trail.
  agentRescueCommands: defineTable({
    deviceId: v.string(),
    ownerUserId: v.id("users"),
    // Strict enum so a compromised UI cannot inject arbitrary shell.
    // Add new variants here AND in the agent's rescue.go dispatch.
    command: v.union(
      v.literal("restart"),
      v.literal("reinstall-latest"),
      v.literal("tunnel-reset"),
      v.literal("auth-reset"),
    ),
    // Per-command params. `version` is for `reinstall-latest`.
    // Empty for `restart`, `tunnel-reset`, `auth-reset`.
    params: v.optional(v.object({
      version: v.optional(v.string()),
    })),
    status: v.union(
      v.literal("pending"),
      v.literal("claimed"),
      v.literal("completed"),
      v.literal("failed"),
      v.literal("expired"),
    ),
    result: v.optional(v.string()),       // stdout tail or error
    createdAt: v.number(),
    claimedAt: v.optional(v.number()),
    completedAt: v.optional(v.number()),
    expiresAt: v.number(),                // createdAt + 5*60*1000
    sourceSurface: v.optional(v.string()), // "web" | "mobile" | "cli" — for audit
  })
    .index("by_device_status", ["deviceId", "status"])
    .index("by_owner", ["ownerUserId"]),

  // Publish-job queue — the async "tap Publish, close the app, come
  // back to a green check" channel. Same 3-tier shape as
  // agentRescueCommands (queue → claim → report) but the work is a
  // /deploy/ship run on a Mac-farm node the owner owns.
  //
  // Privacy contract (enforced by convex_privacy_test.go): this table
  // MUST NOT carry the project's absolute path, build logs, or
  // secrets. Only app NAME + targets + stack travel. The farm node
  // resolves the path itself, locally, from the app name — exactly
  // like /deploy/ship's resolveDeployStackPath fallback. Live build
  // output streams P2P (Phase 3), never through here. `result` is
  // per-target metadata only (ok / exitCode / errorClass / ms).
  publishJobs: defineTable({
    jobId: v.string(), // external id (agent-friendly, not the _id)
    deviceId: v.string(), // Mac-farm node that will run the build
    ownerUserId: v.id("users"),
    app: v.string(), // vault scope + label — NOT a path
    stack: v.string(), // e.g. "react-native-expo"
    // Canonical /deploy/ship target IDs, e.g. ["testflight","playstore"].
    targets: v.array(v.string()),
    status: v.union(
      v.literal("queued"),
      v.literal("claimed"),
      v.literal("running"),
      v.literal("done"),
      v.literal("failed"),
      v.literal("expired"),
    ),
    // Per-target outcome metadata — NO logs/stdout. Mirrors the shape
    // /deploy/ship's composite summary already emits.
    result: v.optional(
      v.array(
        v.object({
          target: v.string(),
          ok: v.boolean(),
          exitCode: v.number(),
          errorClass: v.optional(v.string()),
          durationMs: v.optional(v.number()),
        }),
      ),
    ),
    message: v.optional(v.string()), // short status line, not output
    createdAt: v.number(),
    claimedAt: v.optional(v.number()),
    // Refreshed by the worker while a long build runs so a 20-min
    // archive isn't reaped as a dead claim.
    lastProgressAt: v.optional(v.number()),
    finishedAt: v.optional(v.number()),
    // Claim TTL is short (a queued job must be picked up promptly);
    // once claimed, lastProgressAt + a longer running-grace governs
    // reaping instead.
    expiresAt: v.number(),
    sourceSurface: v.optional(v.string()), // "cli" | "mobile" | "web"
  })
    .index("by_device_status", ["deviceId", "status"])
    .index("by_owner", ["ownerUserId"])
    .index("by_jobId", ["jobId"]),

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
    speechApiKey: v.optional(v.string()),         // legacy only; never returned/updated by /settings
    ttsEnabled: v.optional(v.boolean()),          // read responses aloud
    ttsProvider: v.optional(v.string()),          // "device" | "openai" | "cartesia" preference only
    ttsTaskMode: v.optional(v.boolean()),         // run tasks in TTS mode: agent leads replies with a spoken-style summary (text only; no audio synthesized)
    verbosity: v.optional(v.number()),            // 0-10: response detail level (0=summary, 10=full detail)
    keyStorage: v.optional(v.string()),            // legacy preference; provider keys stay local/vault-only
    // When true, the mobile + (eventually) web tasks `+` button opens a
    // device + agent picker before the compose modal. Lets one task
    // route to a specific machine + runner instead of always using the
    // currently-connected device. Default: undefined → off. Stored on
    // the user record (not per-device) so the preference roams across
    // phones/web logins.
    multiTargetMode: v.optional(v.boolean()),
    // Preferred device for auto-connect when the user has more than one
    // machine registered. When set, mobile / desktop / web will attach
    // to this device on login if it's online, skipping the "pick one"
    // prompt. Cleared (undefined) = no preference → manual pick only
    // when N > 1. When N == 1 we always auto-connect regardless.
    // Value is devices.deviceId (uuid), not an Id<"devices">, so the
    // pref survives a device record being deleted and re-created.
    primaryDeviceId: v.optional(v.string()),
    // Optional second elevated device. Surfaced via `yaver secondary
    // {set,unset,status}`, `yaver ssh secondary`, the mobile + web
    // pickers, and the watchdog (gets the same tight 90s staleness
    // threshold as primary). Same validation as primary: must be one
    // of the caller's owned devices. Most users will leave this unset.
    secondaryDeviceId: v.optional(v.string()),
    // Per-device primary coding agent preference. The dashboard reads
    // this when it connects to a device and pre-selects the named
    // runner so the user doesn't have to pick "codex" every time on
    // the box that's signed into Codex but not Claude. Stored as an
    // array of {deviceId, runnerId} pairs (rather than a record/map)
    // so the schema works on every Convex version we currently
    // support. deviceId matches devices.deviceId (uuid), runnerId
    // matches the agent's runner.id ("claude" / "codex" / "aider" /
    // "ollama" / "aider-ollama" / "opencode" / "goose").
    //
    // Cleared (undefined) for the device → fall back to the previous
    // selection logic (agent's own default, then first installed).
    primaryRunnerByDevice: v.optional(
      v.array(
        v.object({
          deviceId: v.string(),
          runnerId: v.string(),
          // Optional model hint seeded into the runner at spawn time.
          // Examples:
          //   runnerId=claude → model="claude-opus-4-7" / "claude-sonnet-4-6" / "claude-haiku-4-5"
          //   runnerId=codex  → model="gpt-5-codex" / "gpt-5"
          //   runnerId=ollama → model="qwen2.5-coder:14b"
          // Empty/undefined = runner's own default (preserves legacy
          // rows without a model field).
          model: v.optional(v.string()),
          // Optional non-secret runner sub-selection. Today this is
          // primarily for OpenCode's `--agent <mode>` (build / plan /
          // custom agents). Other runners ignore it.
          mode: v.optional(v.string()),
          // Optional non-secret provider hint (e.g. "zai", "glm",
          // "ollama"). Secrets still stay on the machine in
          // opencode.json / env / vault; Convex only stores the
          // user's cross-surface preference.
          provider: v.optional(v.string()),
        }),
      ),
    ),
    // Per-subsystem managed: true|false toggle. true = use Yaver's
    // hosted infrastructure for that subsystem (managed relay,
    // managed analytics, managed storage, …). false = user hosts
    // their own (their Cloudflare, their Plausible, their VPS). The
    // endgame: one checkbox per feature, every Yaver surface honors
    // the same choice without the user juggling per-provider configs.
    //
    // Omitted fields mean "not yet chosen" — the feature keeps its
    // legacy behaviour until the user explicitly opts in/out. Any
    // new subsystem adopting the pattern adds an optional field
    // here; the dashboard/mobile/web Settings page enumerates the
    // schema and shows a toggle per subsystem automatically.
    managed: v.optional(v.object({
      relay:     v.optional(v.boolean()),  // today wired via relayUrl/platformConfig fallback; setting this to true forces the platform relay
      dns:       v.optional(v.boolean()),
      analytics: v.optional(v.boolean()),
      storage:   v.optional(v.boolean()),
      email:     v.optional(v.boolean()),
      ci:        v.optional(v.boolean()),
      voice:     v.optional(v.boolean()),
      llm:       v.optional(v.boolean()),
    })),
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
    // Underlying IaaS (provider-agnostic above the facade). Absent ⇒
    // "hetzner". See cloudMachines.provider for the rationale.
    provider: v.optional(v.string()),
    cloudResourceId: v.optional(v.string()),
    hetznerServerId: v.optional(v.string()),
    // Decommission policy + recovery pointer (see cloudMachines for the
    // privacy/security rationale). snapshotOnDelete defaults OFF.
    snapshotOnDelete: v.optional(v.boolean()),
    lastSnapshotId: v.optional(v.string()),
    lastSnapshotAt: v.optional(v.number()),
    serverIp: v.optional(v.string()),
    domain: v.optional(v.string()), // e.g. "abc123.relay.yaver.io"
    region: v.string(), // "eu" | "us" — datacenter region
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

  // Company-level AI/runtime policy for Talos-style tenants that use
  // Yaver as the remote execution plane. This is intentionally policy
  // and routing metadata only: no API keys, OAuth tokens, customer
  // prompts, output, logs, absolute paths, relay hosts, or secrets.
  // Secrets live on the tenant runtime, in a company vault, or in the
  // provider's own secret store. Convex stores only booleans/ids/labels
  // needed for UI gating and execution planning.
  companyAIOptions: defineTable({
    teamId: v.string(),
    enabled: v.boolean(),
    runtime: v.object({
      mode: v.union(
        v.literal("dedicated-compute"),
        v.literal("bring-your-own-yaver"),
        v.literal("local-only"),
      ),
      defaultProvider: v.union(
        v.literal("hetzner"),
        v.literal("aws"),
        v.literal("gcp"),
        v.literal("azure"),
        v.literal("onprem"),
        v.literal("byo-yaver-device"),
      ),
      defaultDeviceId: v.optional(v.string()),
      fallbackDeviceIds: v.optional(v.array(v.string())),
      region: v.optional(v.string()),
    }),
    convex: v.object({
      deploymentKind: v.union(
        v.literal("dedicated"),
        v.literal("shared-isolated"),
        v.literal("external"),
      ),
      deploymentName: v.optional(v.string()),
      siteUrl: v.optional(v.string()),
      envName: v.string(),
    }),
    runners: v.object({
      defaultRunner: v.string(),
      allowedRunners: v.array(v.string()),
      defaultModelByRunner: v.optional(v.array(v.object({
        runner: v.string(),
        model: v.string(),
      }))),
      allowUserOverride: v.boolean(),
      requireRunnerAuthPerUser: v.boolean(),
      credentialMode: v.union(
        v.literal("user-auth-on-runtime"),
        v.literal("company-api-key-on-runtime"),
        v.literal("local-model-on-runtime"),
        v.literal("external-onprem-endpoint"),
      ),
    }),
    opencode: v.optional(v.object({
      providers: v.array(v.object({
        id: v.string(),
        label: v.string(),
        baseUrl: v.optional(v.string()),
        models: v.array(v.string()),
        keyPolicy: v.union(
          v.literal("company-secret"),
          v.literal("user-secret"),
          v.literal("none"),
        ),
        keyConfigured: v.optional(v.boolean()),
      })),
      defaultAgent: v.optional(v.string()),
    })),
    mcp: v.object({
      enabledServers: v.array(v.string()),
      requiredServers: v.array(v.string()),
      toolPolicyByRole: v.optional(v.array(v.object({
        role: v.string(),
        allowedTools: v.array(v.string()),
      }))),
    }),
    workKinds: v.object({
      appCode: v.boolean(),
      erpFlow: v.boolean(),
      convex: v.boolean(),
      webUi: v.boolean(),
      harnessCad: v.boolean(),
      openScadCad: v.boolean(),
      robotTrial: v.boolean(),
      inspection: v.boolean(),
    }),
    approvals: v.object({
      requireApprovalForProductionWrites: v.boolean(),
      requireApprovalForDeploy: v.boolean(),
      requireApprovalForRobotMotion: v.boolean(),
      requireApprovalForSecretsAccess: v.boolean(),
    }),
    dataPolicy: v.object({
      allowCustomerDataInPrompts: v.boolean(),
      allowScreenshotsInPrompts: v.boolean(),
      allowTelemetryInPrompts: v.boolean(),
      redactPII: v.boolean(),
      retentionDays: v.number(),
    }),
    // Generic, app-contributed profile — the de-Talos-ified path. An app
    // (talos, carrotbet, …) registers its own work kinds + role caps +
    // provider catalog here instead of baking app vocabulary into Yaver's
    // fixed `workKinds` booleans. Optional for back-compat. Config only.
    appProfile: v.optional(v.object({
      app: v.string(),
      workKinds: v.array(v.object({
        key: v.string(),
        label: v.optional(v.string()),
        enabled: v.optional(v.boolean()),
        requiredTools: v.optional(v.array(v.string())),
        requiredMcp: v.optional(v.array(v.string())),
        approvals: v.optional(v.array(v.string())),
        promptHints: v.optional(v.array(v.string())),
        artifactKinds: v.optional(v.array(v.string())),
        allowedRoles: v.optional(v.array(v.string())),
      })),
      roles: v.optional(v.array(v.object({
        role: v.string(),
        allowedTools: v.optional(v.array(v.string())),
        allowedRunners: v.optional(v.array(v.string())),
        allowedProviders: v.optional(v.array(v.string())),
        allowedWorkKinds: v.optional(v.array(v.string())),
      }))),
      providers: v.optional(v.array(v.object({
        id: v.string(),
        label: v.string(),
        baseUrl: v.optional(v.string()),
        models: v.array(v.string()),
        keyPolicy: v.union(
          v.literal("company-secret"),
          v.literal("user-secret"),
          v.literal("none"),
        ),
        keyConfigured: v.optional(v.boolean()),
      }))),
    })),
    createdAt: v.number(),
    updatedAt: v.number(),
    updatedBy: v.optional(v.id("users")),
  }).index("by_teamId", ["teamId"]),

  // Cloud dev machines (provisioned on Hetzner, subscription required)
  cloudMachines: defineTable({
    userId: v.id("users"),
    teamId: v.optional(v.string()),   // if team-owned, all team members can access
    subscriptionId: v.optional(v.id("subscriptions")),
    machineType: v.string(),          // "cpu" | "gpu"
    // Provenance tag. "managed" = provisioned/adopted by Yaver (bought
    // from us, billed via LemonSqueezy or owner dev-adopt). Plain BYO
    // boxes are not cloudMachines rows at all, so they read as
    // "self-hosted" at the device layer. Optional for back-compat:
    // any existing cloudMachines row is Yaver-side ⇒ treat as managed.
    origin: v.optional(v.union(v.literal("managed"), v.literal("self-hosted"))),
    // Backend-hosting model. "byok" (default, absent ⇒ byok): the box
    // runs the user's deploys against THEIR own Convex/Cloudflare via
    // vault-stored keys. "hosted": the box additionally runs a
    // self-hosted Convex (Docker) so deploy targets the box itself —
    // no Convex Cloud account, no tokens. Privacy-safe: the tenant's
    // data lives in the Convex on their own dedicated box; central
    // Convex still sees only identity. Flag/URL only — never secrets.
    tier: v.optional(v.union(v.literal("byok"), v.literal("hosted"))),
    // Self-hosted Convex public API origin on this box (e.g.
    // https://<id>.cloud.yaver.io/_convex-api). Set once the hosted
    // backend is up. Plain URL — privacy-safe (no key, no path).
    hostedConvexUrl: v.optional(v.string()),
    // Phase 4 — hosted-tier teardown grace. A hosted box holds the
    // user's whole app + DB, so on subscription end we DON'T delete it
    // immediately: status goes "grace", deprovisionAt is the deadline,
    // scheduledDestroyId is the pending destroy job (cancelled if they
    // resubscribe). byok boxes are disposable → none of this applies.
    deprovisionAt: v.optional(v.number()),
    scheduledDestroyId: v.optional(v.id("_scheduled_functions")),
    status: v.string(),               // "provisioning" | "active" | "grace" | "stopping" | "stopped" | "paused" | "resuming" | "suspended" | "error"
    // First-class onboarding (project_managed_cloud_onboarding_gap).
    // Granular phase + 0-100 progress so web/mobile show a real
    // "setting up your box" bar, not a binary provisioning/active.
    // The box cloud-init POSTs ticks to /machine/phase (machine-token
    // authed); provision()/healthCheck set the server-side bookends.
    // Privacy-safe: label + percent + timestamp only.
    provisionPhase: v.optional(v.string()), // creating|booting|installing-docker|pulling-image|starting-agent|registering|authorizing-runners|ready|error
    provisionProgress: v.optional(v.number()), // 0-100
    provisionPhaseAt: v.optional(v.number()),
    // Last failure string the box itself reported via /machine/phase
    // (phase="error") — e.g. "agent-health-unreachable-300s". Short
    // curated label only: NEVER raw logs, paths, or secrets (the SSH
    // debug key, not Convex, is how real logs are read). Cleared the
    // moment the box ticks a healthy phase. project_managed_cloud_onboarding_gap.
    provisionError: v.optional(v.string()),
    // Has the user's runner OAuth (claude/codex/opencode subscription)
    // been pushed to this dedicated box? absent/false ⇒ device shows
    // "Unauthorized — Authorize runners" so the user triggers the
    // remote-OAuth push from web/mobile. Never an API key.
    runnersAuthorized: v.optional(v.boolean()),
    multiUser: v.optional(v.boolean()), // true for shared team machines
    // Underlying IaaS this resource lives on. The whole stack above this
    // record stays provider-agnostic ("cloud resource"); only Yaver's
    // facade layer (agent ops_cloud.go) knows the concrete API to call.
    // Absent ⇒ "hetzner" (every existing row predates multi-provider).
    // Future: "gcp" | "aws" | "digitalocean" | … — recorded now so the
    // schema doesn't need a migration when we add a second provider.
    provider: v.optional(v.string()),
    // Provider-native resource id. `hetznerServerId` kept for back-compat;
    // new code reads `cloudResourceId` (provider-agnostic) and falls back.
    cloudResourceId: v.optional(v.string()),
    hetznerServerId: v.optional(v.string()),
    // Decommission policy + recovery pointer. snapshotOnDelete defaults
    // OFF (a snapshot is a paid, lingering image). lastSnapshotId is an
    // opaque provider resource id (NOT contents — snapshot data never
    // touches Convex; privacy-safe, same class as hetznerServerId).
    // SECURITY: managed boxes share Yaver's platform token, so any read
    // of these MUST be scoped by this row's userId — one developer can
    // never see another's snapshot. BYO is isolated by the user's own
    // provider account.
    snapshotOnDelete: v.optional(v.boolean()),
    lastSnapshotId: v.optional(v.string()),
    lastSnapshotAt: v.optional(v.number()),
    serverIp: v.optional(v.string()),
    hostname: v.optional(v.string()),
    // The box's Yaver agent deviceId. For provisioned boxes this is
    // the deterministic `cloud-<machineIdPrefix>` written into the
    // box's config by cloud-init; for adopted boxes it's the existing
    // box's real deviceId supplied at adopt time. Stored so web/mobile
    // can target git/dev-loop/deploy ops at the exact owned device —
    // a credentials/exec op must never fuzzy-guess its target.
    deviceId: v.optional(v.string()),
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
    .index("by_team", ["teamId"])
    .index("by_deviceId", ["deviceId"]),

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
    // Access tier the host is granting:
    //   "full"          — classic teammate scope: /tasks, /vibing, /dev, /builds, /projects, /todolist,
    //                     plus the feedback/blackbox/voice/health/info safe set.
    //   "feedback-only" — hardened end-user scope: /feedback, /blackbox, /voice, /health, /info only.
    //                     Any task auto-triggered by this guest's feedback is force-containerized.
    //                     /info is redacted of project metadata; /projects returns 403.
    // Absent on legacy rows → treated as "full" at runtime (backward-compat). New invites
    // default to "feedback-only" (safer for Feedback-SDK-distributed end-users).
    scope: v.optional(v.union(v.literal("full"), v.literal("feedback-only"), v.literal("sdk-project"), v.literal("support"))),
    // Optional project narrowing at invite time — copied into guestAccess.allowedProjects
    // when the invitation is accepted. See guestAccess.allowedProjects for semantics.
    allowedProjects: v.optional(v.array(v.string())),
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
    // Access tier inherited from the accepted invitation. See guestInvitations.scope for semantics.
    // Absent on legacy rows → treated as "full" at runtime.
    scope: v.optional(v.union(v.literal("full"), v.literal("feedback-only"), v.literal("sdk-project"), v.literal("support"))),
    // Project narrowing — scopes the grant to a subset of the host's
    // projects/repos even within the allowed path list. Most useful with
    // scope=feedback-only when a dev wants to let end-users of Project
    // A file feedback without exposing feedback, workdirs, or fix-task
    // targets of Projects B/C. Matches by MobileProject.Name / project
    // slug. Empty / absent = all projects on this host (current behavior).
    //
    // Enforced in the agent's auth middleware + /feedback fix-task path:
    //   - /feedback (GET list): filter to reports whose inferred project is in the list
    //   - /feedback/{id}/fix: reject if the feedback's project is not in the list
    //   - /tasks: pin workDir to a project in the list; reject attempts to escape
    allowedProjects: v.optional(v.array(v.string())),
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
    // Optional auto-expiry — set by support-link redemption when the friend
    // chose "Allow for 24h" rather than "until I revoke". access.ts treats an
    // expired grant as inactive.
    expiresAt: v.optional(v.number()),
    // Provenance: set when this grant was created by a support-link redemption
    // (vs a normal host→guest invite), so the UI can label it "support".
    origin: v.optional(v.string()), // "support-link" | undefined
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

  // Support links (docs/mesh-support-link.md). A supporter mints a shareable
  // link (yaver.io/j/<code>); when a FRIEND redeems it on their machine, a
  // REVERSE grant is created (host=friend, guest=supporter) so the friend's box
  // joins the supporter's mesh and the supporter can ssh/exec/code into it. The
  // link only OFFERS scope; the friend's consent decides the actual grant.
  supportInvites: defineTable({
    inviterUserId: v.id("users"),
    code: v.string(),
    status: v.union(
      v.literal("pending"),
      v.literal("redeemed"),
      v.literal("revoked"),
      v.literal("expired")
    ),
    singleUse: v.boolean(),
    // The MAX the link offers; the friend opts up to these on the consent screen.
    offerTerminal: v.boolean(),
    offerDesktopControl: v.boolean(),
    defaultTtlHours: v.number(), // suggested session length shown on consent
    label: v.optional(v.string()),
    createdAt: v.number(),
    expiresAt: v.number(), // redeem window for the link itself
    // Populated on redemption:
    redeemedByUserId: v.optional(v.id("users")),
    redeemedDeviceId: v.optional(v.string()),
    redeemedAt: v.optional(v.number()),
    grantId: v.optional(v.id("infraAccessGrants")),
  })
    .index("by_code", ["code"])
    .index("by_inviter", ["inviterUserId"]),

  hostShareInvites: defineTable({
    hostUserId: v.id("users"),
    hostDeviceId: v.optional(v.string()),
    guestEmail: v.optional(v.string()),
    guestUserId: v.optional(v.id("users")),
    acceptedByGuestUserId: v.optional(v.id("users")),
    inviteCode: v.string(),
    status: v.union(
      v.literal("pending"),
      v.literal("accepted"),
      v.literal("revoked"),
      v.literal("expired"),
    ),
    label: v.optional(v.string()),
    inviteExpiresAt: v.number(),
    sessionTtlMinutes: v.number(),
    idleTimeoutMinutes: v.number(),
    toolingPreset: v.optional(v.string()),
    resourcePreset: v.optional(v.string()),
    allowInfra: v.boolean(),
    allowTerminal: v.boolean(),
    allowTunnel: v.boolean(),
    useHostAgentTools: v.boolean(),
    useHostInfra: v.boolean(),
    allowedRunners: v.optional(v.array(v.string())),
    allowedProjects: v.optional(v.array(v.string())),
    createdAt: v.number(),
    acceptedAt: v.optional(v.number()),
    revokedAt: v.optional(v.number()),
  })
    .index("by_hostUserId", ["hostUserId"])
    .index("by_guestUserId", ["guestUserId"])
    .index("by_inviteCode", ["inviteCode"]),

  hostShareSessions: defineTable({
    inviteId: v.id("hostShareInvites"),
    hostUserId: v.id("users"),
    hostDeviceId: v.optional(v.string()),
    guestUserId: v.id("users"),
    guestDeviceId: v.optional(v.string()),
    status: v.union(
      v.literal("active"),
      v.literal("ended"),
      v.literal("expired"),
      v.literal("revoked"),
    ),
    label: v.optional(v.string()),
    policy: v.object({
      toolingPreset: v.optional(v.string()),
      resourcePreset: v.optional(v.string()),
      allowInfra: v.boolean(),
      allowTerminal: v.boolean(),
      allowTunnel: v.boolean(),
      useHostAgentTools: v.boolean(),
      useHostInfra: v.boolean(),
      allowedRunners: v.array(v.string()),
      allowedProjects: v.array(v.string()),
    }),
    createdAt: v.number(),
    startedAt: v.number(),
    expiresAt: v.number(),
    idleTimeoutMinutes: v.number(),
    lastActivityAt: v.number(),
    endedAt: v.optional(v.number()),
    endedReason: v.optional(v.string()),
  })
    .index("by_invite", ["inviteId"])
    .index("by_host_status", ["hostUserId", "status"])
    .index("by_guest_status", ["guestUserId", "status"]),

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
    delegatedGuestUserId: v.optional(v.id("users")), // guest driving the host through Feedback SDK
    delegatedGuestScope: v.optional(v.string()), // currently "sdk-project"
    sourceSurface: v.optional(v.string()), // e.g. "feedback-sdk"
    targetDeviceId: v.optional(v.string()), // host device this token may hit
    allowedProjects: v.optional(v.array(v.string())), // repo/project allowlist for delegated guest use
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

  // User-defined one-tap shortcuts (mobile Shortcuts tab). Each shortcut
  // is an ordered chain of deterministic actions — connect to a device,
  // open a project, push a Hermes reload, start a dev server — run
  // client-side on the phone. Privacy contract: steps carry ONLY a
  // deviceId (uuid), a project slug, and flags/labels. They MUST NOT
  // carry absolute paths or task-prompt text (a "speak/type a task" step
  // collects its prompt at run time and never persists it) — same
  // reasoning as userProjects above. Enforced by convex_privacy_test.go.
  userShortcuts: defineTable({
    userId: v.id("users"),
    name: v.string(),
    icon: v.optional(v.string()),   // key into the app's inline-SVG icon set
    color: v.optional(v.string()),  // accent hex for the card
    order: v.number(),              // sort position in the grid
    steps: v.array(
      v.object({
        kind: v.string(),                     // select-device | open-project | hermes-reload | start-dev | create-task
        deviceId: v.optional(v.string()),     // uuid, matches devices.deviceId
        deviceName: v.optional(v.string()),   // display label only (resolved deviceId can roam)
        projectSlug: v.optional(v.string()),  // filesystem basename only — never a path
        mode: v.optional(v.string()),         // hermes-reload: "dev" | "bundle"
        framework: v.optional(v.string()),    // start-dev: expo | vite | nextjs | flutter | ...
        label: v.optional(v.string()),        // UI label ONLY — never a task prompt
      }),
    ),
    updatedAt: v.number(),
  }).index("by_user", ["userId", "order"]),

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

  // ── Managed-cloud prepaid wallet (metered stop/start) ──────────────
  // Bookkeeping counters only — Convex-allowed (same class as
  // runnerUsage/dailyTaskCounts; convex_privacy_test.go bans
  // secrets/output/paths, NOT balances). All money in integer cents to
  // avoid float drift. Owned by cloudLifecycle.ts; cloudMachines.ts
  // (parallel session) is NOT edited — read-only seam.
  prepaidCredits: defineTable({
    userId: v.id("users"),
    subscriptionId: v.optional(v.id("subscriptions")),
    balanceCents: v.number(),       // current spendable balance
    totalAddedCents: v.number(),    // lifetime topped up
    totalUsedCents: v.number(),     // lifetime metered out
    currency: v.string(),           // "usd" (display only; math is cents)
    lastTopupAt: v.optional(v.number()),
    lastMeteredAt: v.optional(v.number()),
    createdAt: v.number(),
    updatedAt: v.number(),
  }).index("by_user", ["userId"])
    .index("by_subscription", ["subscriptionId"]),

  // One row per metering tick (cron) or transition. Append-only ledger
  // so balance is auditable. `chargedCents` = 2x `hetznerCostCents`
  // (100% margin, both live and stopped/snapshot states).
  creditUsage: defineTable({
    userId: v.id("users"),
    machineId: v.optional(v.id("cloudMachines")),
    date: v.string(),                 // "YYYY-MM-DD" (UTC)
    state: v.string(),                // "live" | "stopped"
    seconds: v.number(),              // billable seconds in this tick
    hetznerCostCents: v.number(),     // raw provider cost (our COGS)
    chargedCents: v.number(),         // user-facing (2x markup)
    ratePerHourCents: v.number(),     // effective user rate this tick
    dryRun: v.boolean(),              // true = simulated, no real spend
    createdAt: v.number(),
  }).index("by_user_date", ["userId", "date"])
    .index("by_machine", ["machineId", "createdAt"]),

  // ── Org-admin singletons ──────────────────────────────────────────
  // Both keyed by the literal "org" so .first() always returns the
  // one-and-only row. A new row replaces; a deleted row reverts the
  // deployment to defaults (per-user managed config + 7-day retention
  // + no OIDC).

  orgPolicy: defineTable({
    singletonKey: v.literal("org"),
    enforceRelay: v.optional(v.boolean()),
    allowedRunners: v.optional(v.array(v.string())),
    allowedProviders: v.optional(v.array(v.string())),
    /** Idle window in minutes. 0 or missing = disabled. Enforced in
     *  authenticateRequest: sessions whose lastRefreshAt is older are
     *  refused as 401. */
    idleTimeoutMin: v.optional(v.number()),
    /** Replaces the hard-coded 7-day default in cleanup.ts. Floors
     *  at 1 day; missing = 7. */
    auditRetentionDays: v.optional(v.number()),
    /** When true, requireAdminRequest refuses admins without TOTP
     *  enrollment. Bootstrap allowlist admins are exempt during the
     *  first 24h after promotion so they can set up MFA. */
    requireMfaForAdmins: v.optional(v.boolean()),
    updatedAt: v.number(),
    updatedBy: v.id("users"),
  }).index("by_singleton", ["singletonKey"]),

  oidcConfig: defineTable({
    singletonKey: v.literal("org"),
    enabled: v.boolean(),
    issuerUrl: v.string(),
    clientId: v.string(),
    /** AES-GCM ciphertext of the client secret. Stored as
     *  base64(iv || ciphertext). Decryption key comes from env
     *  OIDC_SECRET_ENCRYPTION_KEY (32 bytes, base64). */
    clientSecretEnc: v.string(),
    /** Optional email-domain or tenant id; refuses sign-in from
     *  any other tenant. Empty = no restriction. */
    tenant: v.optional(v.string()),
    /** Discovered from .well-known/openid-configuration. Refreshed
     *  on every save + on every Test connection click. */
    authorizationEndpoint: v.optional(v.string()),
    tokenEndpoint: v.optional(v.string()),
    userinfoEndpoint: v.optional(v.string()),
    jwksUri: v.optional(v.string()),
    discoveredAt: v.optional(v.number()),
    updatedAt: v.number(),
    updatedBy: v.id("users"),
  }).index("by_singleton", ["singletonKey"]),

  /** Ephemeral state for the OIDC authorize-code flow. PKCE verifier
   *  + nonce + return-to URL keyed by the random `state` query param.
   *  Pruned by cleanup.ts after the 10-min TTL. */
  oidcAuthAttempts: defineTable({
    state: v.string(),
    codeVerifier: v.string(),
    nonce: v.string(),
    returnTo: v.optional(v.string()),
    expiresAt: v.number(),
    createdAt: v.number(),
  }).index("by_state", ["state"])
    .index("by_expiresAt", ["expiresAt"]),

  /** Companion-compute bookkeeping (desktop/agent/companion.go). Cross-device
   *  visibility for which box runs which serverless project's crons. Privacy
   *  contract: bookkeeping ONLY — slug + bound deviceId + cron names/schedules
   *  + last/next-run status. NEVER endpoint URLs, cron auth tokens, vault
   *  secrets, or absolute paths (those stay on the agent). The agent builds
   *  the payload through buildCompanionUpsertPayload, guarded by
   *  desktop/agent/convex_privacy_test.go. */
  companionProjects: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    slug: v.string(),
    enabled: v.boolean(),
    crons: v.array(v.object({
      name: v.string(),
      schedule: v.string(),
      lastOutcome: v.optional(v.string()),
      lastRunAt: v.optional(v.number()),
      nextRunAt: v.optional(v.number()),
    })),
    serviceCount: v.number(),
    updatedAt: v.number(),
  }).index("by_user", ["userId"])
    .index("by_device_slug", ["deviceId", "slug"]),

  // GPU-rental orchestration bookkeeping (gpuRentals.ts, written by the
  // agent's gpu_rental_sync.go). Cross-device visibility of which dispatcher
  // box rented which burst GPU / bound which serverless inference, for the
  // call-center and any app that bursts GPU. See docs/gpu-rental-orchestration.md.
  //
  // PRIVACY: bookkeeping ONLY. provider + opaque resource id + kind + gpu
  // class + the PUBLIC inference endpoint (host-only, no key) + model id + the
  // vault PROJECT NAME the app reads (never its values) + voiceSafe + status +
  // usage counters. NO api keys, vault values, prompts, call data, or absolute
  // paths — the agent strips them via buildGpuRentalUpsertPayload and
  // desktop/agent/convex_privacy_test.go pins it. Same class as
  // cloudMachines.hostedConvexUrl / cloudResourceId (public id/url, privacy-safe).
  gpuRentals: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),          // dispatcher box that owns this rental
    provider: v.string(),          // "salad" | "deepinfra"
    resourceId: v.string(),        // salad container-group id | "deepinfra:<model>"
    kind: v.string(),              // "gpu-group" | "serverless-binding"
    gpuClass: v.optional(v.string()),
    endpoint: v.optional(v.string()), // PUBLIC OpenAI-compatible base URL (no key)
    model: v.optional(v.string()),
    bindProject: v.optional(v.string()), // vault project NAME (not its values)
    voiceSafe: v.optional(v.boolean()),
    status: v.string(),            // "provisioning" | "running" | "draining" | "stopped"
    // Usage rollup — SUMMARY only, never per-call content.
    hoursUsed: v.optional(v.number()),
    tokensUsed: v.optional(v.number()),
    costCents: v.optional(v.number()),
    updatedAt: v.number(),
  }).index("by_user", ["userId"])
    .index("by_device_resource", ["deviceId", "resourceId"]),

  /** Yaver Mesh — optional WireGuard overlay control plane (desktop/agent
   *  mesh_cmd.go + desktop/agent/mesh/). STRICTLY OPT-IN: a device only gets
   *  a row here after the user runs `yaver mesh up`. Privacy contract:
   *  PUBLIC keys + endpoints + assigned mesh IP ONLY. The WireGuard PRIVATE
   *  key never leaves the device (it lives in the vault); `wgPrivateKey` is
   *  on the Convex forbidden-field list and pinned by
   *  desktop/agent/convex_privacy_test.go. `endpoints` are host:port UDP
   *  candidates the peer can be reached at — the same privacy class as the
   *  existing quicHost/publicEndpoints on the devices table. */
  meshNodes: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    // Base64 WireGuard public key (Curve25519). Never the private half.
    wgPublicKey: v.string(),
    // Stable overlay address assigned by joinMesh. Globally unique across
    // all meshNodes so devices shared between users never collide.
    meshIPv4: v.string(),
    meshIPv6: v.optional(v.string()),
    // host:port UDP candidates for WireGuard (LAN IPs, public endpoint,
    // relay-DERP pseudo-endpoint). Privacy-equivalent to devices.localIps.
    endpoints: v.array(v.string()),
    // Subnet-router CIDRs this node is willing to route (Phase 5).
    advertisedRoutes: v.optional(v.array(v.string())),
    isExitNode: v.optional(v.boolean()),
    online: v.boolean(),
    lastHandshake: v.optional(v.number()),
    updatedAt: v.number(),
    // DESIRED state set by the web/mobile console (Tailscale-style: the control
    // plane holds intent, the agent converges to it on its reconcile tick).
    // The agent reads these for its OWN node and applies them; they are NOT
    // touched by joinMesh (which only reports actual state).
    wantEnabled: v.optional(v.boolean()),     // false = console asked this node to leave
    wantExitNode: v.optional(v.boolean()),    // advertise as exit node
    wantUseExitNode: v.optional(v.string()),  // deviceId of exit node to route through ("" = none)
    wantRoutes: v.optional(v.array(v.string())), // subnet routes to advertise
    desiredAt: v.optional(v.number()),        // when intent last changed (agent dedupe)
  }).index("by_user", ["userId"])
    .index("by_device", ["deviceId"])
    .index("by_meshIPv4", ["meshIPv4"]),

  /** Mesh ACL rules (Phase 4). The tailnet owner authors who-can-reach-whom
   *  on which ports; the agent is the authoritative enforcer (compiled to a
   *  TUN packet filter), mirroring sdk/js/src/acl.ts "Convex composes, agent
   *  enforces". src/dst resolve by tag, deviceId, userId, or "*" (any). */
  meshAcls: defineTable({
    userId: v.id("users"),
    srcType: v.union(
      v.literal("tag"),
      v.literal("device"),
      v.literal("user"),
      v.literal("any")
    ),
    src: v.string(),
    dstType: v.union(
      v.literal("tag"),
      v.literal("device"),
      v.literal("user"),
      v.literal("any")
    ),
    dst: v.string(),
    // Port specs: "22", "80-90", or "*".
    ports: v.array(v.string()),
    action: v.union(v.literal("accept"), v.literal("drop")),
    updatedAt: v.number(),
  }).index("by_user", ["userId"]),

  /** Device tags for group ACLs (Phase 4) — lets a rule target many devices
   *  ("tag:prod") without per-pair grants. */
  meshTags: defineTable({
    userId: v.id("users"),
    deviceId: v.string(),
    tag: v.string(),
    updatedAt: v.number(),
  }).index("by_user", ["userId"])
    .index("by_device", ["deviceId"]),
});
