// discoveryDiagnostics.ts — structured preflight for "why is project
// discovery stuck?" on the Hot Reload (Reload) tab.
//
// The agent's /projects/mobile scan has no server-side timeout
// (desktop/agent/mobile_projects.go), so a slow or permission-blocked
// home-directory walk on macOS (TCC / Full Disk Access) leaves the
// mobile UI spinning on "Discovering apps…" forever with no signal of
// WHICH layer failed. The old UX collapsed every cause into a single
// vague "the remote agent may be unreachable" callout.
//
// This module runs the checks the agent already supports and classifies
// the failure into the concrete layers a user can act on:
//
//   1. reachable     — can we even talk to the agent?            (/health, no auth)
//   2. agentSignedIn — is the agent itself signed in to Yaver?   (/auth/status, no auth)
//   3. authorized    — is THIS phone's session accepted?         (/info, auth)
//   4. runnerOAuth   — is an AI agent (Claude/Codex) authorized? (/runner-auth/status, auth)
//   5. filesystem    — does the project scan actually settle?    (/projects/mobile, auth)
//
// Each check yields pass / warn / fail / skip plus numbered, copy-pasteable
// remediation steps and an optional in-app action. The logic is pure and
// dependency-injected (fetch/now/sleep) so it's unit-testable headless —
// see discoveryDiagnostics.test.mts.

export type CheckId =
  | "reachable"
  | "agentSignedIn"
  | "authorized"
  | "runnerOAuth"
  | "filesystem";

export type CheckStatus = "pending" | "running" | "pass" | "warn" | "fail" | "skip";

export type DiagnosticActionKind = "openDevices" | "retryScan" | "reauth" | "runnerAuth";

export interface DiagnosticAction {
  kind: DiagnosticActionKind;
  label: string;
}

export interface DiagnosticCheck {
  id: CheckId;
  label: string;
  status: CheckStatus;
  /** One-line plain-language result. */
  detail?: string;
  /** Numbered fix-it steps, shown only on warn/fail. */
  remediation?: string[];
  /** Optional in-app affordance the panel renders as a button. */
  action?: DiagnosticAction;
}

export interface DiagnosticsReport {
  host: string;
  checks: DiagnosticCheck[];
  /** blocked = discovery cannot succeed; degraded = works but something's
   *  off (e.g. no runner OAuth, or scan found nothing); ok = all green. */
  overall: "ok" | "degraded" | "blocked";
  summary: string;
}

export interface DiagnosticsProbe {
  /** Base URL of the active device's agent (already device-pinned via
   *  connectionManager.clientFor — NOT the global proxy). */
  baseUrl: string;
  authHeaders: Record<string, string>;
  /** Human label for messages, e.g. "Mobiles-Mac-mini.local". */
  host?: string;
  // ── injectable for tests ──
  fetchImpl?: typeof fetch;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
  /** Total budget to wait for the scan to settle, ms. Default 14000. */
  scanBudgetMs?: number;
}

const DEFAULT_SCAN_BUDGET_MS = 14000;

function defaultSleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function timedFetch(
  fetchImpl: typeof fetch,
  url: string,
  init: RequestInit,
  ms: number,
): Promise<Response> {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), ms);
  try {
    return await fetchImpl(url, { ...init, signal: ctrl.signal });
  } finally {
    clearTimeout(t);
  }
}

function initialChecks(): DiagnosticCheck[] {
  return [
    { id: "reachable", label: "Device reachable", status: "pending" },
    { id: "agentSignedIn", label: "Agent signed in to Yaver", status: "pending" },
    { id: "authorized", label: "This phone authorized", status: "pending" },
    { id: "runnerOAuth", label: "AI agent sign-in (OAuth)", status: "pending" },
    { id: "filesystem", label: "Project scan & file access", status: "pending" },
  ];
}

/**
 * Run the discovery preflight against a single device's agent.
 *
 * `onUpdate` (optional) fires after every check transition with a fresh
 * snapshot so the UI can animate the checklist live. The returned report
 * is the final state.
 */
export async function runDiscoveryDiagnostics(
  probe: DiagnosticsProbe,
  onUpdate?: (checks: DiagnosticCheck[]) => void,
): Promise<DiagnosticsReport> {
  const fetchImpl = probe.fetchImpl ?? fetch;
  const now = probe.now ?? Date.now;
  const sleep = probe.sleep ?? defaultSleep;
  const scanBudgetMs = probe.scanBudgetMs ?? DEFAULT_SCAN_BUDGET_MS;
  const base = (probe.baseUrl || "").replace(/\/+$/, "");
  const host = probe.host?.trim() || "this device";

  const checks = initialChecks();
  const byId = (id: CheckId) => checks.find((c) => c.id === id)!;
  const emit = () => onUpdate?.(checks.map((c) => ({ ...c })));
  const set = (id: CheckId, patch: Partial<DiagnosticCheck>) => {
    Object.assign(byId(id), patch);
    emit();
  };
  const skipRest = (from: CheckId, detail: string) => {
    const order: CheckId[] = ["reachable", "agentSignedIn", "authorized", "runnerOAuth", "filesystem"];
    const start = order.indexOf(from);
    for (let i = start; i < order.length; i++) {
      const cur = byId(order[i]);
      if (cur.status === "pending") set(order[i], { status: "skip", detail });
    }
  };

  emit();

  // ── 1. reachable ───────────────────────────────────────────────────
  set("reachable", { status: "running" });
  let reachable = false;
  try {
    const r = await timedFetch(fetchImpl, `${base}/health`, {}, 6000);
    if (r.ok) {
      reachable = true;
      let info: any = {};
      try { info = await r.json(); } catch { /* health may be terse */ }
      const ver = info?.version ? ` · agent ${info.version}` : "";
      set("reachable", { status: "pass", detail: `Connected to ${info?.hostname || host}${ver}.` });
    } else {
      reachable = true; // we got an HTTP reply, so the transport works
      set("reachable", {
        status: "warn",
        detail: `Agent answered /health with HTTP ${r.status}. The transport works but the agent may be unhealthy.`,
        remediation: [
          `On ${host}, check the agent is healthy: \`yaver status\`.`,
          "If it's wedged, restart it: stop the agent, then `yaver serve`.",
        ],
        action: { kind: "openDevices", label: "Open Devices" },
      });
    }
  } catch {
    set("reachable", {
      status: "fail",
      detail: `Couldn't reach ${host}. The connection timed out or was refused — the request never reached the agent.`,
      remediation: [
        "Make sure your phone and the dev machine are on the same Wi-Fi — or that a relay is configured for remote/cellular access.",
        `On the dev machine, confirm the agent is running: \`yaver serve\` (verify with \`yaver status\`).`,
        "On cellular, Yaver uses the relay — check the relay server is up and the password matches.",
        "Tap Open Devices to re-pick or re-pair this device.",
      ],
      action: { kind: "openDevices", label: "Open Devices" },
    });
  }

  if (!reachable) {
    skipRest("agentSignedIn", "Skipped — device isn't reachable.");
    return finalize(host, checks);
  }

  // ── 2. agentSignedIn ───────────────────────────────────────────────
  // Public endpoint: does the agent itself hold a valid Yaver token? This
  // is the most fundamental auth cause — if the agent isn't signed in, the
  // phone's /info call will 401 too, but the fix is on the dev box.
  set("agentSignedIn", { status: "running" });
  try {
    const r = await timedFetch(fetchImpl, `${base}/auth/status`, {}, 6000);
    if (r.ok) {
      const data = await r.json().catch(() => ({} as any));
      if (data?.authenticated === true) {
        set("agentSignedIn", { status: "pass", detail: "The agent is signed in to your Yaver account." });
      } else {
        const reason =
          data?.reason === "revoked"
            ? "its token was revoked"
            : data?.reason === "no_token"
              ? "no token is stored"
              : "it isn't signed in";
        set("agentSignedIn", {
          status: "fail",
          detail: `The agent on ${host} isn't authenticated — ${reason}.`,
          remediation: [
            `Open a terminal on ${host} (or \`yaver ssh ${shortHost(host)}\`).`,
            "Run `yaver auth` and finish the browser sign-in — or `yaver auth --headless` over SSH.",
            "Sign in with the SAME Yaver account you use on this phone.",
            "Come back and tap Run again.",
          ],
          action: { kind: "reauth", label: "Account settings" },
        });
      }
    } else {
      // Older agents (< the build that shipped /auth/status) — don't fail,
      // just note we couldn't confirm and let /info be the real gate.
      set("agentSignedIn", { status: "skip", detail: `Agent didn't expose /auth/status (HTTP ${r.status}) — skipping this check.` });
    }
  } catch {
    set("agentSignedIn", { status: "skip", detail: "Couldn't read the agent's auth status — continuing." });
  }

  // ── 3. authorized (this phone's session) ───────────────────────────
  set("authorized", { status: "running" });
  let authorized = false;
  try {
    const r = await timedFetch(fetchImpl, `${base}/info`, { headers: probe.authHeaders }, 8000);
    if (r.ok) {
      authorized = true;
      set("authorized", { status: "pass", detail: "This phone's session is accepted by the agent." });
    } else if (r.status === 401 || r.status === 403) {
      set("authorized", {
        status: "fail",
        detail: `You're connected, but the agent rejected this phone's session (HTTP ${r.status}). Not authorized on ${host}.`,
        remediation: [
          "Make sure this phone is signed in to the same Yaver account as the dev machine.",
          "If you recently rotated your token, sign out and back in here (Settings → Account) so a fresh token is issued.",
          `Confirm this phone is paired: on ${host} run \`yaver devices\` and check it's listed.`,
          "Then tap Run again.",
        ],
        action: { kind: "reauth", label: "Account settings" },
      });
    } else {
      set("authorized", {
        status: "warn",
        detail: `/info returned HTTP ${r.status} — unexpected, but not an auth rejection.`,
        action: { kind: "retryScan", label: "Run again" },
      });
    }
  } catch {
    set("authorized", { status: "warn", detail: "Couldn't read /info (timed out). The agent may be busy." });
  }

  if (!authorized) {
    skipRest("runnerOAuth", "Skipped — this phone isn't authorized yet.");
    return finalize(host, checks);
  }

  // ── 4. runnerOAuth (AI agent sign-in) ──────────────────────────────
  // Not required for discovery, but the user specifically asked to surface
  // "Yaver-level OAuth control (ok/nok)" here. A warn never blocks.
  set("runnerOAuth", { status: "running" });
  try {
    const r = await timedFetch(fetchImpl, `${base}/runner-auth/status`, { headers: probe.authHeaders }, 8000);
    if (r.ok) {
      const data = await r.json().catch(() => ({} as any));
      const runners: any[] = Array.isArray(data?.runners) ? data.runners : [];
      const ready = runners.filter((x) => x?.authConfigured);
      if (ready.length > 0) {
        set("runnerOAuth", { status: "pass", detail: `Ready to run: ${ready.map((x) => x?.name || x?.id).join(", ")}.` });
      } else if (runners.length > 0) {
        set("runnerOAuth", {
          status: "warn",
          detail: `No AI agent is signed in on ${host}. Discovery still works, but you can't run a task until a runner is authorized.`,
          remediation: [
            `On ${host}, run \`yaver runner-auth setup\` and sign in to Claude Code or Codex (subscription OAuth — never an API key).`,
            "Or drive the browser flow from this phone: Settings → AI agents → Sign in.",
            "Optional for discovery — only required to actually run an agent.",
          ],
          action: { kind: "runnerAuth", label: "Sign in a runner" },
        });
      } else {
        set("runnerOAuth", { status: "skip", detail: "Agent reported no runners installed." });
      }
    } else {
      set("runnerOAuth", { status: "skip", detail: `runner-auth status unavailable (HTTP ${r.status}).` });
    }
  } catch {
    set("runnerOAuth", { status: "skip", detail: "Couldn't read runner-auth status — continuing." });
  }

  // ── 5. filesystem / scan settle ────────────────────────────────────
  // Kick a fresh scan, then poll until it settles or the budget runs out.
  // A scan that never settles is the classic macOS symptom: the agent
  // can't read the home dir (no Full Disk Access) or the walk is huge.
  set("filesystem", { status: "running" });
  try {
    await timedFetch(fetchImpl, `${base}/projects/mobile`, { method: "POST", headers: probe.authHeaders }, 6000).catch(() => {});
    const start = now();
    let settled = false;
    let lastProjects = 0;
    // Scan diagnostics the agent now returns (≥ the build that shipped the
    // scan-timeout fix). Older agents omit them → undefined → treated as 0/false.
    let permDenied = 0;
    let timedOut = false;
    let scanError = "";
    while (now() - start < scanBudgetMs) {
      await sleep(2000);
      let r: Response;
      try {
        r = await timedFetch(fetchImpl, `${base}/projects/mobile`, { headers: probe.authHeaders }, 6000);
      } catch {
        continue;
      }
      if (!r.ok) continue;
      const data = await r.json().catch(() => ({} as any));
      lastProjects = Array.isArray(data?.projects) ? data.projects.length : 0;
      permDenied = typeof data?.permDenied === "number" ? data.permDenied : permDenied;
      timedOut = !!data?.timedOut || timedOut;
      scanError = typeof data?.scanError === "string" ? data.scanError : scanError;
      const scanning = !!data?.scanning;
      if (!scanning || lastProjects > 0) {
        settled = true;
        break;
      }
    }
    // Permission-denied dirs are the macOS smoking gun — surface them even
    // when some projects were still found (partial scan).
    if (permDenied > 0) {
      const someFound = lastProjects > 0;
      set("filesystem", {
        status: someFound ? "warn" : "fail",
        detail: `The agent couldn't read ${permDenied} folder${permDenied === 1 ? "" : "s"} on ${host}${someFound ? ` (found ${lastProjects} project${lastProjects === 1 ? "" : "s"} anyway)` : ""}. On macOS this means the Yaver agent lacks Full Disk Access.`,
        remediation: [
          "macOS: System Settings → Privacy & Security → Full Disk Access → enable the Yaver agent (add it if it's not listed).",
          `Then restart the agent on ${host}: stop it, and \`yaver serve\`.`,
          "Tap Rescan — the protected folders (including your projects) will now be readable.",
        ],
        action: { kind: "retryScan", label: "Rescan" },
      });
    } else if (timedOut) {
      set("filesystem", {
        status: "fail",
        detail: `The project scan on ${host} hit its time limit before finishing${lastProjects > 0 ? ` (found ${lastProjects} so far)` : ""}. Your home directory may be very large or on a slow/Network volume.`,
        remediation: [
          "Keep projects under ~/Workspace (or ~/Projects/~/Code/~/src) so the scan doesn't crawl your whole home dir.",
          "Move large unrelated folders out of the scanned roots.",
          "Tap Rescan.",
        ],
        action: { kind: "retryScan", label: "Rescan" },
      });
    } else if (scanError) {
      set("filesystem", {
        status: "fail",
        detail: `The scan on ${host} errored: ${scanError}`,
        remediation: [`Restart the agent on ${host}: \`yaver serve\`.`, "Check `yaver status`.", "Then tap Rescan."],
        action: { kind: "retryScan", label: "Rescan" },
      });
    } else if (settled && lastProjects > 0) {
      set("filesystem", { status: "pass", detail: `Found ${lastProjects} project${lastProjects === 1 ? "" : "s"} on ${host}.` });
    } else if (settled) {
      set("filesystem", {
        status: "warn",
        detail: `The scan finished but found no mobile projects on ${host}.`,
        remediation: [
          "Yaver scans your home dir plus ~/Workspace, ~/Projects, ~/Code, ~/src, ~/work, ~/dev (up to 7 levels deep).",
          "Make sure your app lives under one of those and has a package.json (Expo/React Native), pubspec.yaml (Flutter), or Package.swift (native).",
          "A project must be a git repo OR sit directly under a known workspace root to be detected.",
          "Then tap Rescan.",
        ],
        action: { kind: "retryScan", label: "Rescan" },
      });
    } else {
      set("filesystem", {
        status: "fail",
        detail: `The project scan on ${host} didn't settle in ${Math.round(scanBudgetMs / 1000)}s — it's stuck or the agent can't read your files.`,
        remediation: [
          "macOS: grant the Yaver agent Full Disk Access (System Settings → Privacy & Security → Full Disk Access), then restart it. Without it, the home-directory walk silently stalls on protected folders.",
          `Restart the agent on ${host}: stop it, then \`yaver serve\`.`,
          "If your home directory is huge, keep projects under ~/Workspace so the scan stays fast.",
          "Then tap Rescan.",
        ],
        action: { kind: "retryScan", label: "Rescan" },
      });
    }
  } catch {
    set("filesystem", {
      status: "fail",
      detail: `Couldn't run the project scan on ${host}.`,
      remediation: [
        `Restart the agent on ${host}: \`yaver serve\`.`,
        "Check `yaver status` for errors.",
        "Then tap Rescan.",
      ],
      action: { kind: "retryScan", label: "Rescan" },
    });
  }

  return finalize(host, checks);
}

function finalize(host: string, checks: DiagnosticCheck[]): DiagnosticsReport {
  const has = (id: CheckId, status: CheckStatus) => checks.find((c) => c.id === id)?.status === status;
  const blocked =
    has("reachable", "fail") ||
    has("agentSignedIn", "fail") ||
    has("authorized", "fail") ||
    has("filesystem", "fail");
  const degraded =
    !blocked &&
    (has("filesystem", "warn") ||
      has("runnerOAuth", "warn") ||
      has("reachable", "warn") ||
      has("authorized", "warn"));

  let overall: DiagnosticsReport["overall"];
  let summary: string;
  if (blocked) {
    overall = "blocked";
    const firstFail = checks.find((c) => c.status === "fail");
    summary = firstFail
      ? `Discovery is blocked: ${firstFail.label.toLowerCase()} failed. Follow the steps below.`
      : "Discovery is blocked. Follow the steps below.";
  } else if (degraded) {
    overall = "degraded";
    summary = "Connection is fine, but something needs attention — see the highlighted check.";
  } else {
    overall = "ok";
    summary = `Everything checks out on ${host}. Discovery should work — pull to rescan.`;
  }
  return { host, checks, overall, summary };
}

/** Best-effort short alias for SSH hints — strips ".local"/domain suffix. */
function shortHost(host: string): string {
  const first = host.split(".")[0] || host;
  return first.toLowerCase().includes("mac") ? "primary" : first;
}
