export type YaverNativeSurface =
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

export type YaverNativeAppKind = "app" | "game";

export type YaverNativeAuthProvider = "yaver-oauth";

export const YAVER_NATIVE_AUTH_PROVIDER = "yaver-oauth" as const;

export const YAVER_NATIVE_APP_SCOPES = [
  "openid",
  "profile",
  "yaver.apps.run",
  "yaver.apps.events.write",
  "yaver.ai.invoke",
] as const;

export const YAVER_NATIVE_GAME_SCOPES = [
  ...YAVER_NATIVE_APP_SCOPES,
  "yaver.games.play",
  "yaver.games.save",
] as const;

export type YaverNativeScope =
  | (typeof YAVER_NATIVE_APP_SCOPES)[number]
  | (typeof YAVER_NATIVE_GAME_SCOPES)[number];

export type YaverNativeBootstrap = {
  schemaVersion: 1;
  appId?: string;
  gameId?: string;
  appKind: YaverNativeAppKind;
  yaverUserId?: string;
  yaverSessionToken?: string;
  scopes: string[];
  surface: YaverNativeSurface;
  entitlementSnapshot?: Record<string, boolean>;
  runnerSessionId?: string;
  sourceReviewId?: string;
  issuedAt?: string;
};

export type YaverNativeAuthAdapterConfig = {
  appId: string;
  appKind: YaverNativeAppKind;
  standaloneAuthAllowedOutsideYaver: boolean;
  yaverAuthRequiredInYaverBuild: true;
  externalProvidersOutsideYaver?: readonly string[];
};

export function requiredYaverNativeScopes(kind: YaverNativeAppKind): readonly string[] {
  return kind === "game" ? YAVER_NATIVE_GAME_SCOPES : YAVER_NATIVE_APP_SCOPES;
}

export function missingYaverNativeScopes(kind: YaverNativeAppKind, scopes: readonly string[]): string[] {
  const set = new Set(scopes);
  return requiredYaverNativeScopes(kind).filter((scope) => !set.has(scope));
}

export function isValidYaverNativeBootstrap(
  bootstrap: unknown,
  expected: { appId: string; appKind?: YaverNativeAppKind },
): bootstrap is YaverNativeBootstrap {
  if (!bootstrap || typeof bootstrap !== "object" || Array.isArray(bootstrap)) return false;
  const raw = bootstrap as Record<string, unknown>;
  const appKind = raw.appKind === "game" || raw.appKind === "app" ? raw.appKind : expected.appKind;
  const appId = raw.appId ?? raw.gameId;
  return (
    raw.schemaVersion === 1 &&
    appId === expected.appId &&
    (expected.appKind ? appKind === expected.appKind : true) &&
    typeof raw.surface === "string" &&
    Array.isArray(raw.scopes) &&
    missingYaverNativeScopes(appKind ?? "app", raw.scopes.filter((scope): scope is string => typeof scope === "string")).length === 0
  );
}

export function yaverNativeBearerHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
  };
}

export function yaverNativeAuthAdapterText(config: YaverNativeAuthAdapterConfig): string {
  const providers = config.externalProvidersOutsideYaver?.length
    ? config.externalProvidersOutsideYaver.join(", ")
    : "developer-owned providers";
  return [
    `Yaver-native auth adapter for ${config.appId}:`,
    `- In Yaver builds, ${YAVER_NATIVE_AUTH_PROVIDER} is mandatory and sits below any game/app-level account model.`,
    "- The app should treat the verified Yaver user as the account of record for saves, entitlements, devices, multiplayer identity, and event audits.",
    `- Outside Yaver, standalone auth can remain ${config.standaloneAuthAllowedOutsideYaver ? "enabled" : "disabled"} and may use ${providers}.`,
    "- The app backend should verify the Yaver bearer against Yaver /auth/validate or an approved Yaver token-introspection endpoint before mapping to local users.",
    "- Raw Yaver bearer tokens must not be stored in the app backend. Store only the linked Yaver user id, local user id, audit metadata, and non-secret session summaries.",
  ].join("\n");
}
