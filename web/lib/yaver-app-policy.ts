import {
  YAVER_NATIVE_AUTH_PROVIDER,
  YAVER_NATIVE_APP_SCOPES,
  YAVER_NATIVE_GAME_SCOPES,
} from "./yaver-native-auth";

export type YaverAppPolicySeverity = "error" | "warning";

export type YaverAppPolicyFinding = {
  severity: YaverAppPolicySeverity;
  code: string;
  message: string;
};

export type YaverAppPolicyAudit = {
  ok: boolean;
  findings: YaverAppPolicyFinding[];
};

const KNOWN_SURFACES = new Set([
  "web",
  "ios",
  "android",
  "tablet",
  "tvos",
  "android-tv",
  "watch",
  "car",
  "visionos",
  "xr",
  "remote-runner",
  "mcp",
]);

function readObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function readStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function readNested(root: Record<string, unknown>, preferred: string, fallback: string): Record<string, unknown> {
  return readObject(root[preferred] ?? root[fallback]);
}

export function auditYaverAppManifest(manifest: unknown): YaverAppPolicyAudit {
  const findings: YaverAppPolicyFinding[] = [];
  const root = readObject(manifest);
  const runtime = readObject(root.runtime);
  const auth = readObject(root.auth);
  const billing = readObject(root.billing ?? root.monetization);
  const publishPolicy = readObject(root.publishPolicy);
  const source = readObject(root.source);
  const mcp = readObject(root.mcp);

  const kind = String(root.kind ?? runtime.kind ?? "");
  const owner = String(root.owner ?? "");
  const runtimeKind = String(runtime.kind ?? "");
  const positioning = String(runtime.platformPositioning ?? readNested(root, "platformPositioning", "platform").primaryCategory ?? "");
  const isGame = kind === "game" || runtimeKind === "yaver-strategy-game" || positioning === "strategy-games";
  const isYaverNative =
    owner === "yaver" ||
    runtimeKind.startsWith("yaver-") ||
    positioning === "app-runtime" ||
    positioning === "strategy-games" ||
    positioning === "strategy-games-first";

  if (!isYaverNative) {
    findings.push({
      severity: "warning",
      code: "not_yaver_native",
      message: "Manifest is not marked as a Yaver-native app; catalog constraints may not apply yet.",
    });
  }

  if (isYaverNative && auth.provider !== YAVER_NATIVE_AUTH_PROVIDER) {
    findings.push({
      severity: "error",
      code: "yaver_oauth_required",
      message: `Yaver-native catalog apps must use auth.provider = ${YAVER_NATIVE_AUTH_PROVIDER}.`,
    });
  }

  if (isYaverNative && auth.requiredInYaverBuild !== true && auth.mode !== "required") {
    findings.push({
      severity: "error",
      code: "yaver_auth_must_be_required",
      message: "Yaver-native catalog apps must require Yaver auth in the Yaver build.",
    });
  }

  const scopes = readStringArray(auth.requiredScopes);
  const requiredScopes = isGame ? YAVER_NATIVE_GAME_SCOPES : YAVER_NATIVE_APP_SCOPES;
  for (const scope of requiredScopes) {
    if (!scopes.includes(scope)) {
      findings.push({
        severity: "error",
        code: "missing_required_scope",
        message: `Yaver-native manifest is missing required OAuth scope: ${scope}.`,
      });
    }
  }

  const billingOwner = billing.billingOwner ?? billing.webBillingOwner;
  if (isYaverNative && billingOwner !== "yaver") {
    findings.push({
      severity: "error",
      code: "billing_owner_required",
      message: "Official Yaver catalog builds must use Yaver-owned billing and entitlements.",
    });
  }

  const directPayments =
    billing.directDeveloperPaymentsInYaverApp ??
    billing.developerDirectPayments;
  if (isYaverNative && directPayments !== false && directPayments !== "forbidden-in-yaver-app") {
    findings.push({
      severity: "error",
      code: "direct_payments_forbidden",
      message: "Direct developer payments inside Yaver catalog apps must be disabled.",
    });
  }

  if (source.codeCopyIntoYaverRepo === true) {
    findings.push({
      severity: "error",
      code: "source_copy_forbidden",
      message: "Yaver app integration must not depend on copying app source into the Yaver platform repo.",
    });
  }

  if (publishPolicy.externalRelease !== "allowed") {
    findings.push({
      severity: "warning",
      code: "external_release_policy_missing",
      message: "Manifest should explicitly allow developers to release their own app outside Yaver.",
    });
  }

  if (publishPolicy.yaverCatalogRelease !== "optional-reviewed" && publishPolicy.publishingOptional !== true) {
    findings.push({
      severity: "warning",
      code: "catalog_optional_missing",
      message: "Manifest should say Yaver catalog publishing is optional and reviewed.",
    });
  }

  const sourceSharingScope = String(publishPolicy.sourceSharingScope ?? "");
  if (
    sourceSharingScope !== "official-yaver-catalog-release-only" &&
    sourceSharingScope !== "review-package-only" &&
    sourceSharingScope !== "none"
  ) {
    findings.push({
      severity: "warning",
      code: "source_sharing_scope_missing",
      message: "Manifest should limit source/package sharing to review or official Yaver catalog release.",
    });
  }

  const surfaces = readStringArray(root.surfaces);
  for (const surface of surfaces) {
    if (!KNOWN_SURFACES.has(surface)) {
      findings.push({
        severity: "warning",
        code: "unknown_surface",
        message: `Manifest declares unknown surface: ${surface}.`,
      });
    }
  }

  if ((surfaces.includes("watch") || surfaces.includes("car")) && mcp.approvalPolicy !== "surface-aware" && mcp.approvalPolicy !== "phone-required-for-risky-actions") {
    findings.push({
      severity: "warning",
      code: "surface_approval_policy_missing",
      message: "Watch and car surfaces should declare a surface-aware or phone-required approval policy.",
    });
  }

  return {
    ok: findings.every((finding) => finding.severity !== "error"),
    findings,
  };
}

export function formatYaverAppPolicyAudit(audit: YaverAppPolicyAudit): string {
  if (audit.findings.length === 0) {
    return "Yaver app manifest policy audit passed with no findings.";
  }
  return [
    audit.ok ? "Yaver app manifest policy audit passed with warnings." : "Yaver app manifest policy audit failed.",
    "",
    ...audit.findings.map((finding) => `- ${finding.severity.toUpperCase()} ${finding.code}: ${finding.message}`),
  ].join("\n");
}
