export type YaverAppKind =
  | "game"
  | "simulation"
  | "workflow"
  | "assistant-connector"
  | "education"
  | "ops-room"
  | "devtool";

export type YaverAppCategory =
  | "sports-management"
  | "strategy"
  | "tactics"
  | "simulation"
  | "developer-tool"
  | "operations"
  | "personal-assistant"
  | "remote-runtime"
  | "feedback"
  | "testing"
  | "education"
  | "health"
  | "family-care"
  | "reminders";

export type YaverAppSurface =
  | "web"
  | "ios"
  | "android"
  | "tablet"
  | "tvos"
  | "android-tv"
  | "watch"
  | "car"
  | "visionos"
  | "xr"
  | "remote-runner"
  | "mcp";

export type YaverAppAuthMode = "required" | "optional" | "guest";

export type YaverAppBillingMode =
  | "free"
  | "subscription-included"
  | "premium-unlock"
  | "scenario-pack"
  | "usage"
  | "developer-byo";

export type YaverAppRuntimeMode =
  | "first-party"
  | "invited-developer"
  | "developer-owned"
  | "internal-tool";

export type YaverAppAiCapability =
  | "intent-parser"
  | "advisor"
  | "npc-dialogue"
  | "narration"
  | "scenario-generation"
  | "test-bots"
  | "moderation"
  | "workflow-planning"
  | "summarization"
  | "feedback-triage"
  | "release-assistant"
  | "result-analysis"
  | "reminder-planning"
  | "document-extraction";

export type YaverAppCommandModel =
  | "intent-to-command"
  | "direct-command"
  | "mixed"
  | "tool-workflow"
  | "stream-control";

export type YaverAppStateAuthority =
  | "server-authoritative"
  | "developer-hosted"
  | "local-preview"
  | "tool-authoritative";

export type YaverDeveloperLifecycleStage =
  | "mobile-sandbox"
  | "private-repo"
  | "private-deploy"
  | "catalog-review"
  | "yaver-catalog-publish"
  | "external-exit";

export type YaverSourceProvider =
  | "yaver-git"
  | "github"
  | "gitlab"
  | "self-hosted-git"
  | "local-folder"
  | "signed-package";

export type YaverDeploymentTarget =
  | "phone-sandbox"
  | "self-hosted-yaver"
  | "yaver-cloud-hetzner"
  | "developer-vps"
  | "web-preview"
  | "native-preview"
  | "catalog-release";

export interface YaverDeveloperWorkspaceContract {
  readonly lifecycle: readonly YaverDeveloperLifecycleStage[];
  readonly sourceProviders: readonly YaverSourceProvider[];
  readonly deploymentTargets: readonly YaverDeploymentTarget[];
  readonly ownerAccess: "named-owners" | "developer-team" | "single-developer";
  readonly namedOwners?: readonly string[];
  readonly cloudAllocation: {
    readonly mode: "optional" | "required-for-builds";
    readonly provider: "hetzner" | "developer-owned";
    readonly scaleToZero: boolean;
    readonly notes: string;
  };
  readonly codingRunners: {
    readonly supported: readonly ("claude" | "codex" | "opencode" | "glm" | "custom-tmux")[];
    readonly opencodeGlm: "byok" | "yaver-managed-credits" | "both";
    readonly credentialStorage: "local-device-or-target-machine";
  };
  readonly exitRights: {
    readonly exportCode: true;
    readonly externalReleaseAllowed: true;
    readonly removeFromYaverAllowed: true;
    readonly notes: string;
  };
}

export interface YaverAppCommandContract {
  readonly stateAuthority: YaverAppStateAuthority;
  readonly inputModel: YaverAppCommandModel;
  readonly eventLog: "required" | "optional";
  readonly reducer: "deterministic" | "tool-mediated";
}

export interface YaverAppAuthContract {
  readonly mode: YaverAppAuthMode;
  readonly provider: "yaver-oauth";
  readonly requiredScopes: readonly string[];
  readonly guestAccess: "disabled" | "private-test-only" | "public";
  readonly notes: string;
}

export interface YaverAppMonetizationContract {
  readonly launchBilling: YaverAppBillingMode;
  readonly futureBilling: readonly YaverAppBillingMode[];
  readonly mobileBillingOwner: "yaver" | "developer-outside-yaver";
  readonly webBillingOwner: "yaver" | "developer-outside-yaver";
  readonly developerDirectPayments: "forbidden-in-yaver-app" | "allowed-outside-yaver";
  readonly revenueShare: {
    readonly catalog: "applies" | "not-applicable";
    readonly defaultDeveloperShareBps: number;
    readonly yaverHostedCosts: "deduct-before-split" | "not-applicable";
  };
}

export interface YaverAppMcpContract {
  readonly toolPacks: readonly string[];
  readonly requiredPermissions: readonly string[];
  readonly approvalPolicy: "surface-aware" | "phone-required-for-risky-actions";
}

export interface YaverAppPublishPolicy {
  readonly externalRelease: "allowed";
  readonly yaverCatalogRelease: "optional-reviewed";
  readonly sourceSharingScope: "official-yaver-catalog-release-only" | "review-package-only" | "none";
  readonly directDeveloperPaymentsOutsideYaver: "allowed";
}

export interface YaverAppModule {
  readonly id: string;
  readonly title: string;
  readonly kind: "game-mode" | "connector" | "surface" | "tool";
  readonly status: "planned" | "prototype" | "playable" | "needs-hardening";
  readonly multiplayer: "none" | "local" | "online-planned" | "online";
  readonly tvOptimized: "planned" | "ready" | "not-applicable";
  readonly notes: string;
}

export interface YaverAppManifest {
  readonly id: string;
  readonly slug: string;
  readonly title: string;
  readonly subtitle: string;
  readonly status: "planned" | "prototype" | "internal" | "public-free" | "paid";
  readonly kind: YaverAppKind;
  readonly owner: "yaver" | "developer";
  readonly runtimeMode: YaverAppRuntimeMode;
  readonly repo?: string;
  readonly categories: readonly YaverAppCategory[];
  readonly surfaces: readonly YaverAppSurface[];
  readonly ai: readonly YaverAppAiCapability[];
  readonly auth: YaverAppAuthContract;
  readonly monetization: YaverAppMonetizationContract;
  readonly mcp: YaverAppMcpContract;
  readonly commandContract: YaverAppCommandContract;
  readonly developerWorkspace: YaverDeveloperWorkspaceContract;
  readonly publishPolicy: YaverAppPublishPolicy;
  readonly modules?: readonly YaverAppModule[];
  readonly launchPlan: readonly string[];
  readonly platformExtensions: readonly string[];
  readonly platformPositioning: {
    readonly primaryCategory: "strategy-games" | "app-runtime" | "developer-tools";
    readonly nativeCompatibility: readonly YaverAppSurface[];
    readonly companionOnlySurfaces: readonly YaverAppSurface[];
    readonly mcpGuidanceTool: "yaver_app_runtime_guide" | "yaver_strategy_game_native_guide";
  };
}

export const REQUIRED_YAVER_APP_SCOPES = [
  "openid",
  "profile",
  "yaver.apps.run",
  "yaver.apps.events.write",
  "yaver.ai.invoke",
] as const;

export const REQUIRED_YAVER_GAME_SCOPES = [
  ...REQUIRED_YAVER_APP_SCOPES,
  "yaver.games.play",
  "yaver.games.save",
] as const;

const catalogRevenueShare = {
  catalog: "applies",
  defaultDeveloperShareBps: 8000,
  yaverHostedCosts: "deduct-before-split",
} as const;

const defaultDeveloperWorkspace = {
  lifecycle: [
    "mobile-sandbox",
    "private-repo",
    "private-deploy",
    "catalog-review",
    "yaver-catalog-publish",
    "external-exit",
  ],
  sourceProviders: ["yaver-git", "github", "gitlab", "self-hosted-git", "local-folder", "signed-package"],
  deploymentTargets: [
    "phone-sandbox",
    "self-hosted-yaver",
    "yaver-cloud-hetzner",
    "developer-vps",
    "web-preview",
    "native-preview",
    "catalog-release",
  ],
  ownerAccess: "developer-team",
  cloudAllocation: {
    mode: "optional",
    provider: "hetzner",
    scaleToZero: true,
    notes:
      "Yaver Cloud is optional managed compute for builds, previews, and runners. Hetzner machines must snapshot and delete when idle; stopped boxes still bill.",
  },
  codingRunners: {
    supported: ["claude", "codex", "opencode", "glm", "custom-tmux"],
    opencodeGlm: "both",
    credentialStorage: "local-device-or-target-machine",
  },
  exitRights: {
    exportCode: true,
    externalReleaseAllowed: true,
    removeFromYaverAllowed: true,
    notes:
      "Using Yaver to develop or deploy privately must not trap the developer. Source remains in the developer's chosen Git provider or exportable package; Yaver catalog publication is optional.",
  },
} satisfies YaverDeveloperWorkspaceContract;

export const SFMG_YAVER_APP: YaverAppManifest = {
  id: "game_sfmg",
  slug: "sfmg",
  title: "SFMG",
  subtitle: "First-party football strategy, manager, and owner simulation inside Yaver.",
  status: "planned",
  kind: "game",
  owner: "yaver",
  runtimeMode: "first-party",
  repo: "../sfmg",
  categories: ["sports-management", "strategy", "simulation"],
  surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "remote-runner", "mcp"],
  ai: ["intent-parser", "advisor", "narration", "scenario-generation", "test-bots", "moderation"],
  auth: {
    mode: "required",
    provider: "yaver-oauth",
    requiredScopes: REQUIRED_YAVER_GAME_SCOPES,
    guestAccess: "private-test-only",
    notes:
      "The Yaver-integrated SFMG build must not expose a standalone SFMG login. Yaver session identity is the account of record.",
  },
  monetization: {
    launchBilling: "free",
    futureBilling: ["subscription-included", "scenario-pack", "premium-unlock"],
    mobileBillingOwner: "yaver",
    webBillingOwner: "yaver",
    developerDirectPayments: "forbidden-in-yaver-app",
    revenueShare: catalogRevenueShare,
  },
  mcp: {
    toolPacks: ["yaver.app.runtime", "yaver.app.identity", "yaver.app.events", "yaver.app.ai", "yaver.app.surfaces"],
    requiredPermissions: ["apps.run", "apps.events.write", "ai.invoke"],
    approvalPolicy: "surface-aware",
  },
  commandContract: {
    stateAuthority: "server-authoritative",
    inputModel: "mixed",
    eventLog: "required",
    reducer: "deterministic",
  },
  developerWorkspace: {
    ...defaultDeveloperWorkspace,
    ownerAccess: "named-owners",
    namedOwners: ["kivanc", "serhat"],
    cloudAllocation: {
      ...defaultDeveloperWorkspace.cloudAllocation,
      notes:
        "Kivanc and Serhat can allocate temporary Yaver Cloud/Hetzner development boxes for SFMG, clone the closed SFMG repo there, configure OpenCode/GLM on that target, and tear the box down when idle.",
    },
    exitRights: {
      ...defaultDeveloperWorkspace.exitRights,
      notes:
        "SFMG can keep living as a closed-source repo outside Yaver. Yaver development, private deploys, and runner testing do not force catalog publication.",
    },
  },
  publishPolicy: {
    externalRelease: "allowed",
    yaverCatalogRelease: "optional-reviewed",
    sourceSharingScope: "official-yaver-catalog-release-only",
    directDeveloperPaymentsOutsideYaver: "allowed",
  },
  launchPlan: [
    "Create a Yaver-auth-only SFMG adapter.",
    "Map SFMG player, club, owner, save, and league records to Yaver user identity.",
    "Run manager and owner modes as free first-party games inside the Yaver catalog.",
    "Add AI command parsing for tactical, owner, and assistant-coach actions.",
    "Use remote-runner sessions for internal TV/mobile/browser testing before public release.",
  ],
  platformExtensions: [
    "The same manifest shape can describe non-game apps such as simulations, training labs, operations rooms, education modules, and AI workflow tools.",
    "The reusable boundary is intent -> command -> validation -> reducer -> event log -> render, not football-specific code.",
    "Developers can use Yaver for private development and self-hosted/private runs without sharing source; source/package sharing is required only for official in-Yaver catalog release.",
  ],
  platformPositioning: {
    primaryCategory: "strategy-games",
    nativeCompatibility: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    mcpGuidanceTool: "yaver_strategy_game_native_guide",
  },
};

export const YAVER_FEEDBACK_APP: YaverAppManifest = {
  id: "app_feedback_loop",
  slug: "feedback-loop",
  title: "Feedback Loop",
  subtitle: "In-app feedback, screenshots, triage, and agent-routed fixes for apps built with Yaver.",
  status: "prototype",
  kind: "devtool",
  owner: "yaver",
  runtimeMode: "first-party",
  categories: ["developer-tool", "feedback", "testing"],
  surfaces: ["web", "ios", "android", "tablet", "remote-runner", "mcp"],
  ai: ["feedback-triage", "summarization", "release-assistant", "test-bots"],
  auth: {
    mode: "required",
    provider: "yaver-oauth",
    requiredScopes: REQUIRED_YAVER_APP_SCOPES,
    guestAccess: "private-test-only",
    notes: "Feedback reporters can be guest-scoped, but developer triage and agent routing require a Yaver session.",
  },
  monetization: {
    launchBilling: "subscription-included",
    futureBilling: ["usage", "premium-unlock"],
    mobileBillingOwner: "yaver",
    webBillingOwner: "yaver",
    developerDirectPayments: "forbidden-in-yaver-app",
    revenueShare: {
      catalog: "not-applicable",
      defaultDeveloperShareBps: 0,
      yaverHostedCosts: "not-applicable",
    },
  },
  mcp: {
    toolPacks: ["yaver.app.feedback", "yaver.app.runtime", "yaver.app.ai", "yaver.app.release"],
    requiredPermissions: ["feedback.read", "tasks.create", "ai.invoke"],
    approvalPolicy: "phone-required-for-risky-actions",
  },
  commandContract: {
    stateAuthority: "tool-authoritative",
    inputModel: "tool-workflow",
    eventLog: "required",
    reducer: "tool-mediated",
  },
  developerWorkspace: defaultDeveloperWorkspace,
  publishPolicy: {
    externalRelease: "allowed",
    yaverCatalogRelease: "optional-reviewed",
    sourceSharingScope: "none",
    directDeveloperPaymentsOutsideYaver: "allowed",
  },
  launchPlan: [
    "Treat feedback capture as a first-party Yaver app, not only an SDK page.",
    "Route screenshots, device metadata, and user notes into Yaver task packages.",
    "Let MCP agents triage feedback, reproduce issues, and propose fixes on managed cloud or self-hosted runners.",
    "Expose app-safe summaries on phone and web; keep watch/car to short status and approvals.",
  ],
  platformExtensions: [
    "External apps can keep their own distribution while paying Yaver for feedback triage, cloud runners, and inference.",
    "Catalog apps can use the same feedback loop for review-gated releases and paid support workflows.",
  ],
  platformPositioning: {
    primaryCategory: "developer-tools",
    nativeCompatibility: ["web", "ios", "android", "tablet", "remote-runner", "mcp"],
    companionOnlySurfaces: [],
    mcpGuidanceTool: "yaver_app_runtime_guide",
  },
};

export const CARROTBET_YAVER_APP: YaverAppManifest = {
  id: "game_carrotbet",
  slug: "carrotbet",
  title: "Carrotbet",
  subtitle: "Developer-owned multi-game platform brought into Yaver with OAuth, managed cloud, feedback, MCP, TV, and multiplayer rails.",
  status: "prototype",
  kind: "game",
  owner: "developer",
  runtimeMode: "developer-owned",
  repo: "../carrotbet",
  categories: ["strategy", "tactics", "simulation", "developer-tool"],
  surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "visionos", "xr", "remote-runner", "mcp"],
  ai: ["intent-parser", "advisor", "narration", "scenario-generation", "test-bots", "moderation", "feedback-triage"],
  auth: {
    mode: "required",
    provider: "yaver-oauth",
    requiredScopes: REQUIRED_YAVER_GAME_SCOPES,
    guestAccess: "private-test-only",
    notes:
      "Carrotbet can keep its own public app and accounts outside Yaver. The Yaver-native build uses Yaver OAuth as the account of record for catalog saves, multiplayer identity, entitlements, and cross-device surfaces.",
  },
  monetization: {
    launchBilling: "free",
    futureBilling: ["subscription-included", "scenario-pack", "premium-unlock", "usage"],
    mobileBillingOwner: "yaver",
    webBillingOwner: "yaver",
    developerDirectPayments: "forbidden-in-yaver-app",
    revenueShare: catalogRevenueShare,
  },
  mcp: {
    toolPacks: [
      "yaver.app.runtime",
      "yaver.app.identity",
      "yaver.app.events",
      "yaver.app.ai",
      "yaver.app.feedback",
      "yaver.app.surfaces",
      "yaver.tv_control",
      "yaver.watch_approval",
    ],
    requiredPermissions: ["apps.run", "apps.events.write", "ai.invoke", "feedback.read", "runtime.stream"],
    approvalPolicy: "surface-aware",
  },
  commandContract: {
    stateAuthority: "developer-hosted",
    inputModel: "mixed",
    eventLog: "required",
    reducer: "deterministic",
  },
  developerWorkspace: defaultDeveloperWorkspace,
  publishPolicy: {
    externalRelease: "allowed",
    yaverCatalogRelease: "optional-reviewed",
    sourceSharingScope: "official-yaver-catalog-release-only",
    directDeveloperPaymentsOutsideYaver: "allowed",
  },
  modules: [
    {
      id: "carrotbet_backgammon",
      title: "Backgammon",
      kind: "game-mode",
      status: "playable",
      multiplayer: "online",
      tvOptimized: "planned",
      notes: "Primary Yaver import target because it already has web/mobile play, AI/local modes, friend rooms, and multiplayer hardening notes.",
    },
    {
      id: "carrotbet_battleship",
      title: "Battleship",
      kind: "game-mode",
      status: "prototype",
      multiplayer: "online-planned",
      tvOptimized: "planned",
      notes: "Good TV/D-pad candidate: turn-based, readable from distance, and already has web/mobile engine work.",
    },
    {
      id: "carrotbet_air_hockey",
      title: "Air Hockey",
      kind: "game-mode",
      status: "prototype",
      multiplayer: "online-planned",
      tvOptimized: "planned",
      notes: "Use for latency testing and living-room multiplayer after Yaver stream/input timing is measured.",
    },
    {
      id: "carrotbet_arcade_pack",
      title: "Arcade Pack",
      kind: "game-mode",
      status: "prototype",
      multiplayer: "local",
      tvOptimized: "planned",
      notes: "Includes sky defense, jet shooter, drone shield, carrot catch, tower stack, asteroid belt, and similar quick-session games.",
    },
  ],
  launchPlan: [
    "Add a Yaver-native OAuth adapter for Carrotbet catalog builds while preserving Carrotbet's independent external release path.",
    "Expose Carrotbet game modules through a Yaver manifest instead of copying the app into the Yaver repo.",
    "Use Yaver feedback and managed cloud runners for bug capture, replay, AI triage, and release testing.",
    "Prioritize backgammon as the first multiplayer import, then Battleship and selected arcade modes.",
    "Create TV-optimized layouts with D-pad navigation, large board state, room codes, and couch multiplayer flows.",
    "Gate purchases, scenario packs, and premium catalog entitlements through Yaver billing only inside the Yaver build.",
  ],
  platformExtensions: [
    "Carrotbet remains free to ship its own web/mobile app outside Yaver with its own branding and payments.",
    "Yaver monetizes the optional catalog build, managed cloud testing, inference, relay, feedback, MCP, and multi-surface runtime.",
    "Turn-based games should use server-validated commands and event logs before being promoted as Yaver multiplayer catalog titles.",
    "TV optimization should favor D-pad, room codes, readable boards, and spectator/couch flows rather than pointer-heavy desktop UI.",
  ],
  platformPositioning: {
    primaryCategory: "strategy-games",
    nativeCompatibility: ["web", "ios", "android", "tablet", "tvos", "android-tv", "visionos", "xr", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    mcpGuidanceTool: "yaver_strategy_game_native_guide",
  },
};

export const PERSONAL_RUNTIME_APP: YaverAppManifest = {
  id: "app_personal_runtime",
  slug: "personal-runtime",
  title: "Personal Runtime",
  subtitle: "A cross-device assistant surface for user-owned apps, browsers, redroid, and MCP connectors.",
  status: "planned",
  kind: "assistant-connector",
  owner: "yaver",
  runtimeMode: "first-party",
  categories: ["personal-assistant", "operations", "remote-runtime"],
  surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "visionos", "xr", "remote-runner", "mcp"],
  ai: ["intent-parser", "workflow-planning", "summarization", "advisor", "moderation"],
  auth: {
    mode: "required",
    provider: "yaver-oauth",
    requiredScopes: REQUIRED_YAVER_APP_SCOPES,
    guestAccess: "disabled",
    notes: "User-owned connectors and app sessions stay bound to the user's Yaver identity and local vault.",
  },
  monetization: {
    launchBilling: "subscription-included",
    futureBilling: ["usage", "premium-unlock"],
    mobileBillingOwner: "yaver",
    webBillingOwner: "yaver",
    developerDirectPayments: "forbidden-in-yaver-app",
    revenueShare: {
      catalog: "not-applicable",
      defaultDeveloperShareBps: 0,
      yaverHostedCosts: "not-applicable",
    },
  },
  mcp: {
    toolPacks: ["yaver.personal_assistant", "yaver.browser_runtime", "yaver.android_clone", "yaver.watch_approval", "yaver.tv_control"],
    requiredPermissions: ["gateway.query", "gateway.plan", "approval.request", "runtime.stream"],
    approvalPolicy: "phone-required-for-risky-actions",
  },
  commandContract: {
    stateAuthority: "tool-authoritative",
    inputModel: "tool-workflow",
    eventLog: "required",
    reducer: "tool-mediated",
  },
  developerWorkspace: {
    ...defaultDeveloperWorkspace,
    lifecycle: ["mobile-sandbox", "private-repo", "private-deploy", "external-exit"],
    deploymentTargets: ["phone-sandbox", "self-hosted-yaver", "yaver-cloud-hetzner", "developer-vps", "web-preview"],
  },
  publishPolicy: {
    externalRelease: "allowed",
    yaverCatalogRelease: "optional-reviewed",
    sourceSharingScope: "none",
    directDeveloperPaymentsOutsideYaver: "allowed",
  },
  launchPlan: [
    "Unify personal assistant connectors, browser automation, redroid, and device surfaces as one Yaver app runtime.",
    "Use phone for setup and risky approvals, watch for glanceable approvals, car for voice summaries, and TV/XR for wallboard views.",
    "Sell managed cloud, inference, and relay capacity as the paid runtime behind the app.",
  ],
  platformExtensions: [
    "Third-party developers can build connector packs and publish outside Yaver while paying for Yaver cloud/inference.",
    "Official catalog connector packs use Yaver billing and revenue-share terms.",
  ],
  platformPositioning: {
    primaryCategory: "app-runtime",
    nativeCompatibility: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "visionos", "xr", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    mcpGuidanceTool: "yaver_app_runtime_guide",
  },
};

export const PERSONAL_HEALTH_AGENT_APP: YaverAppManifest = {
  id: "app_personal_health_agent",
  slug: "personal-health-agent",
  title: "Personal Health Agent",
  subtitle: "User-owned health portal automation, result tracking, reminders, and optional AI summaries with local-first privacy.",
  status: "planned",
  kind: "assistant-connector",
  owner: "yaver",
  runtimeMode: "first-party",
  categories: ["personal-assistant", "health", "family-care", "reminders"],
  surfaces: ["web", "ios", "android", "tablet", "watch", "car", "remote-runner", "mcp"],
  ai: ["intent-parser", "summarization", "result-analysis", "reminder-planning", "document-extraction"],
  auth: {
    mode: "required",
    provider: "yaver-oauth",
    requiredScopes: REQUIRED_YAVER_APP_SCOPES,
    guestAccess: "disabled",
    notes:
      "Yaver identity owns the automation consent and notification routing. Health portal login remains the user's own account and is bound interactively into the local vault or explicitly approved managed runtime.",
  },
  monetization: {
    launchBilling: "subscription-included",
    futureBilling: ["usage", "premium-unlock", "developer-byo"],
    mobileBillingOwner: "yaver",
    webBillingOwner: "yaver",
    developerDirectPayments: "forbidden-in-yaver-app",
    revenueShare: {
      catalog: "not-applicable",
      defaultDeveloperShareBps: 0,
      yaverHostedCosts: "not-applicable",
    },
  },
  mcp: {
    toolPacks: [
      "yaver.personal_assistant",
      "yaver.browser_runtime",
      "yaver.android_clone",
      "yaver.health_connectors",
      "yaver.watch_approval",
    ],
    requiredPermissions: ["gateway.query", "gateway.plan", "health.read", "reminder.create", "ai.invoke"],
    approvalPolicy: "phone-required-for-risky-actions",
  },
  commandContract: {
    stateAuthority: "tool-authoritative",
    inputModel: "tool-workflow",
    eventLog: "required",
    reducer: "tool-mediated",
  },
  developerWorkspace: {
    ...defaultDeveloperWorkspace,
    lifecycle: ["mobile-sandbox", "private-repo", "private-deploy", "external-exit"],
    deploymentTargets: ["phone-sandbox", "self-hosted-yaver", "yaver-cloud-hetzner", "developer-vps"],
    cloudAllocation: {
      ...defaultDeveloperWorkspace.cloudAllocation,
      notes:
        "Health connectors should default to local/self-hosted execution. Yaver Cloud is opt-in for scheduled checks, must scale to zero, and must keep health artifacts in the user's runtime/vault rather than Convex.",
    },
  },
  publishPolicy: {
    externalRelease: "allowed",
    yaverCatalogRelease: "optional-reviewed",
    sourceSharingScope: "none",
    directDeveloperPaymentsOutsideYaver: "allowed",
  },
  modules: [
    {
      id: "health_enabiz",
      title: "e-Nabız connector",
      kind: "connector",
      status: "planned",
      multiplayer: "none",
      tvOptimized: "not-applicable",
      notes:
        "Read-only first connector for lab results, prescriptions, appointments, reports, and health timeline reminders. OAuth, e-Devlet, 2FA, CAPTCHA, or block states require user handoff; Yaver must not bypass them.",
    },
    {
      id: "health_result_watch",
      title: "Result watcher",
      kind: "tool",
      status: "planned",
      multiplayer: "none",
      tvOptimized: "not-applicable",
      notes:
        "Scheduled human-cadence checks for new results. Compares reference ranges and prior values deterministically, then optionally asks a model for a plain-language summary.",
    },
    {
      id: "health_care_reminders",
      title: "Care reminders",
      kind: "tool",
      status: "planned",
      multiplayer: "none",
      tvOptimized: "not-applicable",
      notes:
        "Creates reminders for doctor follow-ups, prescription pickup, lab repeats, and user-authored questions to ask a clinician. No autonomous medical treatment changes.",
    },
  ],
  launchPlan: [
    "Create a generic sensitive-read connector schema for health portals before writing e-Nabız-specific flows.",
    "Build e-Nabız as read-only first: login handoff, result list, result detail, prescription list, appointment list, and report download metadata.",
    "Store portal sessions, downloaded reports, extracted values, and detailed audit only in the local vault/runtime store; Convex receives coordination metadata only.",
    "Add a scheduler policy for human-cadence checks, jittered wakeups, pause/resume, and visible run history.",
    "Implement OAuth/2FA/CAPTCHA/block handoff: stop automation, notify the user, open a visible browser/redroid session, and resume only after user action.",
    "Offer inference modes: none, local/BYOK, or Yaver managed inference for summaries and reminder planning.",
  ],
  platformExtensions: [
    "Yaver is not the clinician and must not diagnose, prescribe, or recommend starting/stopping medication.",
    "The agent can say a result is outside the lab reference range, changed from a prior value, or ready to discuss with a doctor.",
    "Family/caregiver workflows require explicit delegated access and should never silently share health data.",
    "Managed cloud and managed inference are paid add-ons, not requirements; local-only mode must remain viable for sensitive health workflows.",
  ],
  platformPositioning: {
    primaryCategory: "app-runtime",
    nativeCompatibility: ["web", "ios", "android", "tablet", "watch", "car", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    mcpGuidanceTool: "yaver_app_runtime_guide",
  },
};

export const YAVER_CATALOG_APPS = [
  SFMG_YAVER_APP,
  CARROTBET_YAVER_APP,
  YAVER_FEEDBACK_APP,
  PERSONAL_RUNTIME_APP,
  PERSONAL_HEALTH_AGENT_APP,
] as const;

export const YAVER_FIRST_PARTY_APPS = YAVER_CATALOG_APPS.filter((app) => app.owner === "yaver");
export const YAVER_FIRST_PARTY_GAMES = YAVER_CATALOG_APPS.filter((app) => app.kind === "game");

export function getYaverAppBySlug(slug: string): YaverAppManifest | undefined {
  return YAVER_CATALOG_APPS.find((app) => app.slug === slug);
}
