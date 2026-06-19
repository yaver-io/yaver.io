export type RunnerModelLike = {
  id: string;
  isDefault?: boolean;
};

export type RunnerLike = {
  id: string;
  models?: RunnerModelLike[];
};

export type DeviceIdentityLike = {
  name?: string | null;
  hostName?: string | null;
  os?: string | null;
};

export const HETZNER_OPENCODE_MODEL = "zai-coding-plan/glm-4.7";
export const HETZNER_GLM_MODEL = "glm-4.7";

export function isKivancAccount(email: string | null | undefined): boolean {
  const normalized = String(email || "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.EXPO_PUBLIC_YAVER_OWNER_EMAIL ||
    process.env.EXPO_PUBLIC_YAVER_CLOUD_PREVIEW_EMAILS ||
    "";
  const allowed = raw
    .split(",")
    .map((item: string) => item.trim().toLowerCase())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(normalized);
}

export function isKivancMacBook(device: DeviceIdentityLike): boolean {
  const haystack = `${device.name || ""} ${device.hostName || ""}`.toLowerCase();
  const isMac = ["darwin", "macos"].includes(String(device.os || "").trim().toLowerCase());
  if (!isMac) return false;
  return haystack.includes("kivanc") || haystack.includes("cakmak") || haystack.includes("macbook");
}

export function isHetznerLikeDevice(device: DeviceIdentityLike): boolean {
  const haystack = `${device.name || ""} ${device.hostName || ""}`.toLowerCase();
  const os = String(device.os || "").trim().toLowerCase();
  return os === "linux" && (
    haystack.includes("hetzner") ||
    haystack.includes("cloud") ||
    haystack.includes("remote") ||
    haystack.includes("yaver-")
  );
}

export function normalizeTaskRunnerId(runnerId?: string | null): string {
  const normalized = String(runnerId || "").trim().toLowerCase();
  if (normalized === "claude-code") return "claude";
  return normalized;
}

export function displayRunnerLabel(runnerId?: string | null): string {
  const normalized = String(runnerId || "").trim().toLowerCase();
  if (normalized === "claude" || normalized === "claude-code") return "Claude Code";
  if (normalized === "codex") return "Codex";
  if (normalized === "opencode") return "OpenCode";
  if (normalized === "glm") return "GLM (z.ai)";
  return normalized || "Selected agent";
}

export function isModelCompatibleWithRunnerId(
  modelId: string | null | undefined,
  runnerId: string | null | undefined,
): boolean {
  const model = String(modelId || "").trim().toLowerCase();
  const runner = normalizeTaskRunnerId(runnerId);
  if (!model || !runner) return false;
  if (runner === "claude") return model.startsWith("claude-");
  if (runner === "codex") return model.startsWith("gpt-") || model.startsWith("o") || model.includes("codex");
  return true;
}

export function preferredDefaultRunnerForDevice(
  device: DeviceIdentityLike,
  signedInEmail: string | null | undefined,
  availableRunnerIds: string[],
): string | null {
  if (availableRunnerIds.length === 0) return null;
  const unique = Array.from(new Set(availableRunnerIds.map(normalizeTaskRunnerId).filter(Boolean)));
  if (isHetznerLikeDevice(device) && unique.includes("opencode")) return "opencode";
  if (isKivancAccount(signedInEmail)) {
    if (isKivancMacBook(device) && unique.includes("claude")) return "claude";
    if (!isKivancMacBook(device) && unique.includes("opencode")) return "opencode";
    if (!isKivancMacBook(device) && unique.includes("codex")) return "codex";
  }
  if (unique.includes("claude")) return "claude";
  if (unique.includes("codex")) return "codex";
  if (unique.includes("opencode")) return "opencode";
  if (unique.includes("glm")) return "glm";
  return unique[0] || null;
}

export function preferredDefaultModelForRunner(
  runnerId: string | null | undefined,
  device: DeviceIdentityLike,
  signedInEmail: string | null | undefined,
): string | null {
  const normalized = normalizeTaskRunnerId(runnerId);
  if (!normalized) return null;
  if (isKivancAccount(signedInEmail)) {
    if (normalized === "claude" && isKivancMacBook(device)) return "claude-opus-4-7";
    if (normalized === "opencode" && !isKivancMacBook(device)) return HETZNER_OPENCODE_MODEL;
    if (normalized === "glm" && !isKivancMacBook(device)) return HETZNER_GLM_MODEL;
    if (normalized === "codex" && !isKivancMacBook(device)) return "gpt-5.3-codex";
  }
  if (normalized === "claude") return "claude-opus-4-7";
  if (normalized === "codex") return "gpt-5.3-codex";
  if (normalized === "opencode") return HETZNER_OPENCODE_MODEL;
  if (normalized === "glm") return HETZNER_GLM_MODEL;
  return null;
}

export function resolveRunnerForRemoteSend(args: {
  activeDeviceId?: string | null;
  primaryRunnerByDevice?: Record<string, string | undefined>;
  selectedRunner?: string | null;
  fallbackRunner?: string | null;
  userPickedRunner?: boolean;
}): string | undefined {
  if (args.selectedRunner === "custom") return "custom";
  const explicitPrimary = args.activeDeviceId
    ? normalizeTaskRunnerId(args.primaryRunnerByDevice?.[args.activeDeviceId])
    : "";
  const picked = normalizeTaskRunnerId(args.selectedRunner);
  const fallback = normalizeTaskRunnerId(args.fallbackRunner);
  const resolved = !args.userPickedRunner && explicitPrimary
    ? explicitPrimary
    : picked || explicitPrimary || fallback;
  return resolved || undefined;
}

export function resolveModelForRemoteSend(args: {
  runnerId?: string | null;
  activeDevice?: DeviceIdentityLike | null;
  primaryModelByDevice?: Record<string, string | undefined>;
  selectedModel?: string | null;
  fallbackModel?: string | null;
  availableRunners?: RunnerLike[];
  signedInEmail?: string | null;
  userPickedModel?: boolean;
}): string | undefined {
  const runner = normalizeTaskRunnerId(args.runnerId);
  if (!runner || runner === "custom") return undefined;
  const activeDevice = args.activeDevice ?? {};
  const activeDeviceId = (activeDevice as any).id ? String((activeDevice as any).id) : "";
  const primary = activeDeviceId ? args.primaryModelByDevice?.[activeDeviceId] || "" : "";
  const picked = args.selectedModel || "";
  const fallback = args.fallbackModel || "";
  const runnerRow = args.availableRunners?.find((r) => normalizeTaskRunnerId(r.id) === runner);
  const rowDefault = runnerRow?.models?.find((m) => m.isDefault)?.id || runnerRow?.models?.[0]?.id || "";
  const heuristic = preferredDefaultModelForRunner(runner, activeDevice, args.signedInEmail) || "";
  const candidates = args.userPickedModel
    ? [picked, primary, fallback, rowDefault, heuristic]
    : [primary, fallback, picked, rowDefault, heuristic];
  return candidates.find((model) => isModelCompatibleWithRunnerId(model, runner)) || undefined;
}

export function isTransportDeviceLabel(label: string | null | undefined): boolean {
  const value = String(label || "").trim();
  if (!value) return false;
  if (/^\d{1,3}(?:\.\d{1,3}){3}$/.test(value)) return true;
  if (/^[0-9a-f:]+$/i.test(value) && value.includes(":")) return true;
  if (/^https?:\/\//i.test(value)) return true;
  return false;
}

export function normalizeProjectChipName(name: string | null | undefined): string {
  const value = String(name || "").trim();
  const lower = value.toLowerCase();
  if (!value || lower === "root" || value === "/root" || value === "~") return "";
  return value;
}
