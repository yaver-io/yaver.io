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
  os?: string | null;
};

// OpenCode default = the live-verified direct DeepSeek Chat model, kept in lockstep with the
// endpoint-local OpenCode configuration and web DEFAULT_MODEL_BY_RUNNER.
// Yaver never receives the provider credential.
export const HETZNER_OPENCODE_MODEL = "deepseek/deepseek-chat";

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
  const haystack = `${device.name || ""}`.toLowerCase();
  const isMac = ["darwin", "macos"].includes(String(device.os || "").trim().toLowerCase());
  if (!isMac) return false;
  return haystack.includes("kivanc") || haystack.includes("cakmak") || haystack.includes("macbook");
}

export function isHetznerLikeDevice(device: DeviceIdentityLike): boolean {
  const haystack = `${device.name || ""}`.toLowerCase();
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
  return normalized || "Selected agent";
}

/** Native command exposed by an adopted runner's interactive tmux seat.
 * This opens the catalogue reported by the signed-in Codex installation, so
 * a mobile build does not have to guess which subscription models exist. */
export function adoptedRunnerControlCommand(runnerId?: string | null): string | null {
  return normalizeTaskRunnerId(runnerId) === "codex" ? "/model" : null;
}

/** The one machine whose runner catalog/defaults the composer must use. */
export function resolveRunnerSelectionDeviceId(input: {
  taskTargetDeviceId?: string | null;
  runnerRoleDeviceId?: string | null;
  activeDeviceId?: string | null;
}): string {
  // The Tasks banner controls the visibly selected Remote Box. Older installs
  // can retain a split runner-role preference after focus changes; preferring
  // that hidden role made the header say Ubuntu while its runner button edited
  // a Mac. An explicit wizard target still wins, then the visible box, with
  // the legacy role only as a fallback when no box is selected.
  return input.taskTargetDeviceId || input.activeDeviceId || input.runnerRoleDeviceId || "";
}

/** True when a task-create response proves the machine launched a different
 * runner than the one the composer sent. Empty legacy response values remain
 * unknown rather than being guessed. */
export function runnerDispatchMismatch(
  requestedRunnerId?: string | null,
  actualRunnerId?: string | null,
): boolean {
  const requested = normalizeTaskRunnerId(requestedRunnerId);
  const actual = normalizeTaskRunnerId(actualRunnerId);
  return !!requested && !!actual && requested !== actual;
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
  if (runner === "opencode") {
    const [provider, modelName, ...extra] = model.split("/");
    return Boolean(provider && modelName && extra.length === 0);
  }
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
  return unique[0] || null;
}

export function preferredSeededRunnerForDevice(args: {
  device: DeviceIdentityLike;
  signedInEmail: string | null | undefined;
  readyRunnerIds: string[];
  installedRunnerIds: string[];
}): string | null {
  const ready = Array.from(new Set(args.readyRunnerIds.map(normalizeTaskRunnerId).filter(Boolean)));
  if (ready.length > 0) {
    return preferredDefaultRunnerForDevice(args.device, args.signedInEmail, ready);
  }
  const installed = Array.from(new Set(args.installedRunnerIds.map(normalizeTaskRunnerId).filter(Boolean)));
  return preferredDefaultRunnerForDevice(args.device, args.signedInEmail, installed);
}

export function preferredDefaultModelForRunner(
  runnerId: string | null | undefined,
  device: DeviceIdentityLike,
  signedInEmail: string | null | undefined,
): string | null {
  const normalized = normalizeTaskRunnerId(runnerId);
  if (!normalized) return null;
  if (isKivancAccount(signedInEmail)) {
    if (normalized === "claude" && isKivancMacBook(device)) return "claude-opus-4-8";
    if (normalized === "opencode" && !isKivancMacBook(device)) return HETZNER_OPENCODE_MODEL;
    if (normalized === "codex" && !isKivancMacBook(device)) return "gpt-5.6-sol";
  }
  if (normalized === "claude") return "claude-opus-4-8";
  if (normalized === "codex") return "gpt-5.6-sol";
  if (normalized === "opencode") return HETZNER_OPENCODE_MODEL;
  return null;
}

export function resolveRunnerForRemoteSend(args: {
  activeDeviceId?: string | null;
  /** Runner/render split: the box the task will actually run on. When set,
   *  per-device runner defaults key off THIS id — dispatching the render
   *  box's CLI to the runner box is the classic split mistake. */
  dispatchDeviceId?: string | null;
  primaryRunnerByDevice?: Record<string, string | undefined>;
  selectedRunner?: string | null;
  fallbackRunner?: string | null;
  userPickedRunner?: boolean;
}): string | undefined {
  if (args.selectedRunner === "custom") return "custom";
  const keyDeviceId = args.dispatchDeviceId || args.activeDeviceId;
  const explicitPrimary = keyDeviceId
    ? normalizeTaskRunnerId(args.primaryRunnerByDevice?.[keyDeviceId])
    : "";
  const picked = normalizeTaskRunnerId(args.selectedRunner);
  const fallback = normalizeTaskRunnerId(args.fallbackRunner);
  // The picker value is visible user-facing state, so it must be the wire
  // truth whenever it is non-empty. `userPickedRunner` used to make the same
  // visible "Codex" value mean two different things: a device-focus change
  // reset the hidden flag, then dispatch silently replaced Codex with that
  // machine's stored OpenCode primary. The task header was the first place the
  // user could learn the switch had been ignored.
  //
  // Per-device primary remains the default when the picker is genuinely
  // blank. Seeding the picker from that primary is a UI concern; dispatch must
  // never contradict what the composer currently displays.
  const resolved = picked || explicitPrimary || fallback;
  return resolved || undefined;
}

export function resolveModelForRemoteSend(args: {
  runnerId?: string | null;
  activeDevice?: DeviceIdentityLike | null;
  /** Runner/render split: box the task runs on — model defaults key here. */
  dispatchDeviceId?: string | null;
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
  const activeDeviceId = args.dispatchDeviceId || ((activeDevice as any).id ? String((activeDevice as any).id) : "");
  const primary = activeDeviceId ? args.primaryModelByDevice?.[activeDeviceId] || "" : "";
  const picked = args.selectedModel || "";
  const fallback = args.fallbackModel || "";
  const runnerRow = args.availableRunners?.find((r) => normalizeTaskRunnerId(r.id) === runner);
  const rowDefault = runnerRow?.models?.find((m) => m.isDefault)?.id || runnerRow?.models?.[0]?.id || "";
  const heuristic = preferredDefaultModelForRunner(runner, activeDevice, args.signedInEmail) || "";
  const candidates = args.userPickedModel
    ? [picked, primary, fallback, rowDefault, heuristic]
    : [primary, fallback, rowDefault, picked, heuristic];
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
