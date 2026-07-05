import { auditYaverAppManifest, formatYaverAppPolicyAudit, type YaverAppPolicyAudit } from "./yaver-app-policy";

export type YaverGamePolicySeverity = "error" | "warning";
export type YaverGamePolicyFinding = YaverAppPolicyAudit["findings"][number];
export type YaverGamePolicyAudit = YaverAppPolicyAudit;

export function formatYaverGamePolicyAudit(audit: YaverGamePolicyAudit): string {
  return formatYaverAppPolicyAudit(audit).replace("Yaver app", "Yaver game");
}

export function auditYaverGameManifest(manifest: unknown): YaverGamePolicyAudit {
  return auditYaverAppManifest({ ...(manifest as object), kind: "game" });
}
