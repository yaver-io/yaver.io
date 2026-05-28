/**
 * Connection-failure classifier.
 *
 * The dashboard talks to agents over several transports (relay, Cloudflare
 * tunnel, direct LAN, `{deviceId}.yaver.io` subdomain) and each fails in its
 * own way. Without classification, every fetch failure renders the same
 * generic "unavailable" — which is the headline complaint in the failure-state
 * audit: cards lie ("Ready to Connect" while underlying probes 502), errors
 * are swallowed, and the user has no actionable next step.
 *
 * This module is the single place that maps a `Response`, thrown `Error`, or
 * `ConnectAttemptDiagnostic` to a `ClassifiedFailure { reason, label, detail,
 * suggestedAction }`. Callers render the label inline on the card and use the
 * reason to pick a recovery affordance.
 */

import type { ConnectAttemptDiagnostic } from "@/lib/agent-client";

export type ConnectionFailureReason =
  | "mixed-content"
  | "cors-blocked"
  | "relay-stale"
  | "tunnel-stale"
  | "subdomain-unrouted"
  | "auth-expired"
  | "unauthorized"
  | "forbidden"
  | "not-found"
  | "timeout"
  | "browser-offline"
  | "network"
  | "device-offline"
  | "unknown";

export interface ClassifiedFailure {
  reason: ConnectionFailureReason;
  label: string;
  detail: string;
  suggestedAction?: string;
  status?: number;
  raw?: string;
}

export interface ClassifyFetchErrorInput {
  error?: unknown;
  response?: { status?: number; url?: string } | null;
  path?: "relay" | "tunnel" | "direct" | "subdomain";
  url?: string;
  authExpired?: boolean;
}

const SUBDOMAIN_RE = /^https?:\/\/[0-9a-f-]{36}\.yaver\.io/i;

function isSubdomainUrl(url?: string): boolean {
  return !!url && SUBDOMAIN_RE.test(url);
}

function pathLabel(path?: ClassifyFetchErrorInput["path"]): string {
  switch (path) {
    case "relay": return "relay";
    case "tunnel": return "Cloudflare tunnel";
    case "direct": return "direct LAN";
    case "subdomain": return "device subdomain";
    default: return "agent";
  }
}

export function classifyFetchError(input: ClassifyFetchErrorInput): ClassifiedFailure {
  const { error, response, path, authExpired } = input;
  const url = input.url ?? response?.url;
  const status = response?.status;
  const errMsg = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  const raw = errMsg || (status ? `HTTP ${status}` : undefined);

  if (authExpired) {
    return {
      reason: "auth-expired",
      label: "Agent auth expired",
      detail: "The agent reached us, but its Convex session is stale. The box is up; it just needs to re-auth.",
      suggestedAction: "Run `yaver auth` on the agent (or use Rescue / Reauth here).",
      status,
      raw,
    };
  }

  if (typeof navigator !== "undefined" && navigator.onLine === false) {
    return {
      reason: "browser-offline",
      label: "Browser offline",
      detail: "Your browser reports no network connectivity.",
      suggestedAction: "Check your Wi-Fi / cellular and retry.",
      raw,
    };
  }

  if (/blocked: browser refuses http:\/\/ from https:\/\//i.test(errMsg)) {
    return {
      reason: "mixed-content",
      label: "Browser blocked direct LAN",
      detail: "yaver.io is served over HTTPS, but the agent's direct LAN address is HTTP. Browsers refuse mixed-content fetches.",
      suggestedAction: "Connect via relay or the local desktop app instead.",
      raw,
    };
  }

  if (error instanceof Error && (error.name === "AbortError" || /timeout|timed out/i.test(error.message))) {
    return {
      reason: "timeout",
      label: `${pathLabel(path)} timed out`,
      detail: "The request didn't complete before the timeout.",
      suggestedAction: "Retry. If it persists, the agent or relay may be wedged.",
      raw,
    };
  }

  if (status === 401) {
    return {
      reason: "unauthorized",
      label: "Unauthorized",
      detail: "The agent (or relay) refused our token. Most often: relay password missing or token rotated.",
      suggestedAction: "Sign in again or refresh the dashboard.",
      status,
      raw,
    };
  }

  if (status === 403) {
    return {
      reason: "forbidden",
      label: "Forbidden",
      detail: "Reached the agent, but it refused this request for the current identity.",
      suggestedAction: "Check guest scopes or device ownership.",
      status,
      raw,
    };
  }

  if (status === 404) {
    if (path === "subdomain" || isSubdomainUrl(url)) {
      return {
        reason: "subdomain-unrouted",
        label: "Stale device subdomain",
        detail: "The `{deviceId}.yaver.io` subdomain isn't wired through to the relay. The agent has a path-style URL (`public.yaver.io/d/...`) it should publish instead.",
        suggestedAction: "Bump the agent (it'll republish a path-style URL on next heartbeat) or hit the device via the relay path directly.",
        status,
        raw,
      };
    }
    return {
      reason: "not-found",
      label: "Route not found",
      detail: "The agent answered 404 — feature may not be implemented at this agent version.",
      suggestedAction: "Update the agent to a newer version.",
      status,
      raw,
    };
  }

  if (status === 502 || status === 503 || status === 504) {
    if (path === "relay") {
      return {
        reason: "relay-stale",
        label: "Relay tunnel down",
        detail: "The relay returned 502 — your agent's QUIC tunnel to the relay isn't established. The agent may be alive (Convex heartbeats can still flow) but it isn't reachable via relay right now.",
        suggestedAction: "Restart the agent (`yaver serve`) or wait for it to re-establish the tunnel.",
        status,
        raw,
      };
    }
    if (path === "tunnel") {
      return {
        reason: "tunnel-stale",
        label: "Cloudflare tunnel down",
        detail: "The agent's Cloudflare tunnel URL returned 502. The tunnel is dead or restarting.",
        suggestedAction: "Restart the agent's tunnel or fall back to relay.",
        status,
        raw,
      };
    }
    return {
      reason: "relay-stale",
      label: `${pathLabel(path)} unreachable`,
      detail: `Got HTTP ${status} from the ${pathLabel(path)} path. The upstream service is down.`,
      suggestedAction: "Retry or try a different transport.",
      status,
      raw,
    };
  }

  // Browser CORS preflight failures usually surface as TypeError "Failed to
  // fetch" with no status code. Distinguish by whether the URL is the
  // known-broken subdomain pattern (which 404's without CORS headers).
  if (error instanceof TypeError || /failed to fetch|network/i.test(errMsg)) {
    if (path === "subdomain" || isSubdomainUrl(url)) {
      return {
        reason: "cors-blocked",
        label: "CORS preflight blocked",
        detail: "The `{deviceId}.yaver.io` subdomain returned without CORS headers (likely a 404 from un-wired wildcard DNS). Browser blocked the request.",
        suggestedAction: "This URL is stale — the dashboard should be using `public.yaver.io/d/...`. Refresh the page to repull from Convex.",
        raw,
      };
    }
    return {
      reason: "network",
      label: `${pathLabel(path)} unreachable`,
      detail: errMsg || "fetch failed with no further detail. Most likely a DNS, TLS, or CORS issue.",
      suggestedAction: "Retry. If it persists, the agent may be offline.",
      raw,
    };
  }

  if (typeof status === "number" && status >= 400) {
    return {
      reason: "unknown",
      label: `HTTP ${status}`,
      detail: `Got an unexpected HTTP ${status} from the ${pathLabel(path)} path.`,
      status,
      raw,
    };
  }

  return {
    reason: "unknown",
    label: "Unreachable",
    detail: errMsg || "Unknown failure.",
    raw,
  };
}

export function classifyDiagnostic(diag: ConnectAttemptDiagnostic): ClassifiedFailure {
  return classifyFetchError({
    error: diag.error,
    response: { status: diag.status },
    path: diag.path,
    authExpired: diag.authExpired,
  });
}

/**
 * Given a list of attempts (one per transport tried), return the most
 * informative failure to surface. Priority:
 *   1. auth-expired (most actionable — user just needs to reauth)
 *   2. unauthorized / forbidden
 *   3. relay-stale / tunnel-stale (agent unreachable via that transport)
 *   4. mixed-content / cors-blocked / subdomain-unrouted (browser/infra issue)
 *   5. timeout / network / unknown
 */
export function summarizeFailures(diags: ConnectAttemptDiagnostic[]): ClassifiedFailure | null {
  const failed = diags.filter((d) => !d.ok);
  if (failed.length === 0) return null;

  const classified = failed.map(classifyDiagnostic);
  const priority: ConnectionFailureReason[] = [
    "auth-expired",
    "unauthorized",
    "forbidden",
    "relay-stale",
    "tunnel-stale",
    "subdomain-unrouted",
    "mixed-content",
    "cors-blocked",
    "timeout",
    "network",
    "browser-offline",
    "not-found",
    "device-offline",
    "unknown",
  ];

  for (const r of priority) {
    const hit = classified.find((c) => c.reason === r);
    if (hit) return hit;
  }
  return classified[0] ?? null;
}
