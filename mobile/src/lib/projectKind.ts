// projectKind — classify the currently-attached agent's working dir
// into one of four buckets the UI uses to pick which features to surface:
//
//   "mobile"   — Expo / React Native / Flutter / native iOS / native Android
//   "web"      — Next / Vite / Nuxt / SvelteKit / Astro / Remix
//   "backend"  — Go / Rust / Python / Hono / Convex-only
//   "generic"  — none of the above
//
// Consumers:
//   - glass-terminal.tsx vibe bar — mobile shows Hermes reload + wire push;
//     web shows web_preview_reload + deploy + tsc/eslint; backend shows
//     logs + db + deploy. The mobile flow ([feedback_always_deploy_yaver],
//     mobile_hermes_reload chips) is preserved EXACTLY for kind=mobile.
//   - glass-workspace.tsx — picks the default 4-pane layout per kind.
//
// Cache: in-memory + per quicClient.baseUrl. Invalidates when the active
// device changes (DeviceContext drives this). 5-minute TTL otherwise.

import { quicClient } from "./quic";

export type ProjectKind = "mobile" | "web" | "backend" | "generic";

export interface ProjectKindResult {
  kind: ProjectKind;
  workDir: string;
  frameworks: string[];
  hasManifest: boolean;
  reason?: string;
  /** Filled by the client — agent base URL the result came from. */
  source: string;
  /** Epoch ms when fetched. */
  fetchedAt: number;
  /**
   * True when this result is the silent fallback (404, fetch error,
   * no agent). UIs that show kind-conditional features should render
   * a visible warning so the user knows the agent is older than the
   * /project/kind endpoint, not that this is a "generic" project.
   */
  degraded?: boolean;
}

const CACHE_TTL_MS = 5 * 60 * 1000;
const cache = new Map<string, ProjectKindResult>();

/**
 * Fetch (or return cached) project kind for the currently-attached
 * agent. Falls back to {kind:"generic"} on any error — never throws,
 * so the vibe bar can always render something.
 */
export async function fetchProjectKind(opts: {
  /** Override the work directory the agent classifies. Optional. */
  dir?: string;
  /** Skip cache and re-fetch. */
  force?: boolean;
  signal?: AbortSignal;
} = {}): Promise<ProjectKindResult> {
  const base = quicClient.baseUrl;
  if (!base) {
    return makeFallback("no agent selected", "");
  }
  const cacheKey = `${base}|${opts.dir ?? ""}`;
  const cached = cache.get(cacheKey);
  if (!opts.force && cached && Date.now() - cached.fetchedAt < CACHE_TTL_MS) {
    return cached;
  }
  try {
    const url = new URL("/project/kind", base);
    if (opts.dir) url.searchParams.set("dir", opts.dir);
    const res = await fetch(url.toString(), {
      headers: { Accept: "application/json", ...quicClient.getAuthHeaders() },
      signal: opts.signal,
    });
    if (!res.ok) {
      // 404 → agent doesn't have the endpoint yet (older agent on the
      // remote box). Degrade gracefully but mark the result as degraded
      // so UIs render a visible indicator instead of silently rendering
      // a generic vibe-bar.
      if (res.status === 404) {
        console.warn(`projectKind: agent at ${base} missing /project/kind — needs upgrade`);
      }
      return makeFallback(`HTTP ${res.status}`, base);
    }
    const json = await res.json() as Partial<ProjectKindResult>;
    const result: ProjectKindResult = {
      kind: (json.kind as ProjectKind) ?? "generic",
      workDir: json.workDir ?? "",
      frameworks: Array.isArray(json.frameworks) ? json.frameworks : [],
      hasManifest: !!json.hasManifest,
      reason: typeof json.reason === "string" ? json.reason : undefined,
      source: base,
      fetchedAt: Date.now(),
    };
    cache.set(cacheKey, result);
    return result;
  } catch (e: unknown) {
    if (e instanceof Error && e.name === "AbortError") throw e;
    return makeFallback(
      e instanceof Error ? e.message : "fetch failed",
      base,
    );
  }
}

/**
 * Force-clear the cache. DeviceContext should call this on device
 * switch so the new agent's kind isn't masked by the previous one.
 */
export function invalidateProjectKindCache(): void {
  cache.clear();
}

function makeFallback(reason: string, source: string): ProjectKindResult {
  return {
    kind: "generic",
    workDir: "",
    frameworks: [],
    hasManifest: false,
    reason,
    source,
    fetchedAt: Date.now(),
    degraded: true,
  };
}

/**
 * Stable boolean check for "this kind is a mobile-app dev workflow".
 * Used to keep the Hermes-reload chips visible without leaking the
 * concept of "mobile" into every consumer.
 */
export function isMobileProjectKind(kind: ProjectKind): boolean {
  return kind === "mobile";
}
