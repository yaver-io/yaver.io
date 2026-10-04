export const MODEL_DEFAULTS_CONFIG_KEY = "runner_model_defaults";

export type RunnerModelDefault = {
  model: string;
  reasoningEffort?: string;
};

export type RunnerModelDefaults = Record<"claude" | "codex" | "opencode", RunnerModelDefault>;

// Product defaults, not account preferences. A user's task/device selection
// remains authoritative; these fill only an otherwise-empty selection and are
// the one-time recovery target after the provider rejects an explicit model.
export const YAVER_MODEL_DEFAULTS: RunnerModelDefaults = {
  claude: { model: "claude-opus-4-8" },
  codex: { model: "gpt-5.6-sol", reasoningEffort: "medium" },
  opencode: { model: "deepinfra/deepseek-ai/DeepSeek-V4.1-Flash" },
};

export function canonicalModelRunnerId(value: unknown): keyof RunnerModelDefaults | null {
  const id = String(value ?? "").trim().toLowerCase();
  if (id === "claude" || id === "claude-code") return "claude";
  if (id === "codex" || id === "opencode") return id;
  return null;
}

export function parseRunnerModelDefaults(raw: unknown): RunnerModelDefaults {
  let input: unknown = raw;
  if (typeof raw === "string") {
    try {
      input = JSON.parse(raw);
    } catch {
      input = {};
    }
  }
  const source = input && typeof input === "object" && !Array.isArray(input)
    ? input as Record<string, unknown>
    : {};
  const result = structuredClone(YAVER_MODEL_DEFAULTS);
  for (const [rawRunner, rawDefault] of Object.entries(source)) {
    const runner = canonicalModelRunnerId(rawRunner);
    if (!runner || !rawDefault || typeof rawDefault !== "object" || Array.isArray(rawDefault)) continue;
    const model = String((rawDefault as Record<string, unknown>).model ?? "").trim();
    if (!model) continue;
    result[runner] = { model };
    if (runner === "codex") {
      const effort = String((rawDefault as Record<string, unknown>).reasoningEffort ?? "").trim().toLowerCase();
      result.codex.reasoningEffort = ["none", "low", "medium", "high", "xhigh", "max", "ultra"].includes(effort)
        ? effort
        : "medium";
    }
  }
  return result;
}

type ModelRow = {
  modelId: string;
  runnerId: string;
  name?: string;
  description?: string;
  defaultReasoningEffort?: string;
  supportedReasoningEfforts?: string[];
  isDefault?: boolean;
  sortOrder?: number;
  [key: string]: unknown;
};

export function applyRunnerModelDefaults<T extends ModelRow>(
  rows: T[],
  defaults: RunnerModelDefaults,
): Array<T | ModelRow> {
  const seen = new Set<string>();
  const normalized: Array<T | ModelRow> = rows.map((row) => {
    const runner = canonicalModelRunnerId(row.runnerId);
    if (!runner) return row;
    const key = `${runner}\u0000${row.modelId}`;
    seen.add(key);
    return { ...row, isDefault: row.modelId === defaults[runner].model };
  });
  const backendRunnerId: Record<keyof RunnerModelDefaults, string> = {
    claude: "claude-code",
    codex: "codex",
    opencode: "opencode",
  };
  for (const runner of Object.keys(defaults) as Array<keyof RunnerModelDefaults>) {
    const model = defaults[runner].model;
    if (seen.has(`${runner}\u0000${model}`)) continue;
    normalized.push({
      modelId: model,
      runnerId: backendRunnerId[runner],
      name: model,
      description: "Yaver global default",
      isDefault: true,
      sortOrder: 0,
    });
  }
  return normalized;
}
