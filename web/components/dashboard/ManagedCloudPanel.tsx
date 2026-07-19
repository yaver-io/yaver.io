"use client";

// ManagedCloudPanel — web-only surface for Cloud Workspace resources:
// subscribe via LemonSqueezy, or owner/dev ADOPT an existing cloud machine
// (allowlist-gated server-side). Every managed row carries the
// `origin` provenance tag ("managed" = bought from/adopted by Yaver;
// plain BYO devices in the list above are "self-hosted").
//
// Removal is intentionally per-workspace. Pause preserves state and deletes
// active compute; Delete decommissions the managed resource.
//
// Non-owners just see an empty list / 403s — the gate is the server
// (isCloudPreviewUser), never the client.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient } from "@/lib/agent-client";
import WakeProgress from "@/components/dashboard/WakeProgress";
import { listRecentWakeRuns, type CloudWakeRun } from "@/lib/task-placement";

interface ManagedMachine {
  id: string;            // /subscription returns the machine id as `id` (NOT _id)
  machineType?: string;
  status?: string;
  origin?: "managed" | "self-hosted";
  hetznerServerId?: string;
  region?: string;
  serverIp?: string;
  hostname?: string;
  errorMessage?: string;
  deviceId?: string;
  // First-class onboarding (project_managed_cloud_onboarding_gap):
  // drives the "setting up your box" progress bar + Authorize state.
  provisionPhase?: string | null;
  provisionProgress?: number | null;
  // Timers + provider state behind the wake ladder. Without provisionPhaseAt a
  // surface can only time the whole wake, so it cannot tell "booting for 20s"
  // from "booting for 9 minutes" — the difference between a normal wake and a
  // stuck one.
  provisionPhaseAt?: number | null;
  lastWokeAt?: number | null;
  providerStatus?: string | null;
  providerStatusAt?: number | null;
  runnersAuthorized?: boolean;
}

// Web-only checkout is allowed here. Mobile surfaces may control existing
// machines, but must not initiate purchases.
const HIDE_PAID_UI = false;

type PaidProductId = "relay-pro" | "cloud-workspace";

const CLOUD_PLANS: Array<{
  id: PaidProductId;
  name: string;
  price: string;
  label: string;
  detail: string;
  bullets: string[];
}> = [
  {
    id: "relay-pro",
    name: "Relay Pro",
    price: "$9",
    label: "/mo",
    detail: "Private managed relay for users who keep coding on their own machine.",
    bullets: ["Private relay", "Higher shared limits", "Custom managed endpoint"],
  },
  {
    id: "cloud-workspace",
    name: "Cloud Workspace",
    price: "$29",
    label: "/mo",
    detail: "Saved cloud machine for full-stack projects, with Relay Pro included.",
    bullets: ["Saved workspace", "Relay Pro included", "Auto-sleep when idle"],
  },
];

function wakeRunLabel(run: CloudWakeRun): string {
  const kind =
    run.kind === "provision"
      ? "Provisioning"
      : run.kind === "park"
        ? "Parking"
        : "Waking";
  const phase = run.phase ? run.phase.replace(/-/g, " ") : null;
  return phase ? `${kind} · ${phase}` : kind;
}

function wakeRunTone(status: CloudWakeRun["status"]): string {
  switch (status) {
    case "succeeded":
      return "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300";
    case "failed":
    case "cancelled":
      return "bg-rose-500/15 text-rose-700 dark:text-rose-300";
    case "retrying":
    case "blocked":
      return "bg-amber-500/15 text-amber-700 dark:text-amber-300";
    default:
      return "bg-sky-500/15 text-sky-700 dark:text-sky-300";
  }
}

function timeAgo(ts?: number | null): string {
  if (!ts) return "";
  const seconds = Math.max(0, Math.round((Date.now() - ts) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

// Per-machine actions (D3 git connect/push, D4 dev-loop, D5 deploy).
// Every action targets the box's EXPLICIT agent deviceId — never a
// guessed/fuzzy target (credentials + exec). Disabled until the box
// has registered (deviceId present). Ops verbs run on the connected
// agent and are routed P2P to the box; tokens never touch Convex.
// Managed boxes aren't auto-connected to the dashboard's agentClient
// (the panel row has no "Open Workspace"), so callOps would throw
// "AgentClient is not connected". Connect to the box itself — its
// public serverIp:18080 (direct, no cert wait) with https://hostname
// as a tunnel fallback — using the user's session token + the box's
// explicit deviceId, before any ops. Idempotent: skips if already
// connected to this box. This is what makes GitHub/GitLab/Codex/
// Claude auth actually run on the managed box.
async function ensureBoxConnected(
  deviceId?: string,
  serverIp?: string,
  hostname?: string,
  token?: string | null,
): Promise<void> {
  if (!deviceId) throw new Error("workspace agent not registered yet");
  if (!token) throw new Error("not signed in");
  if (agentClient.isConnected && agentClient.connectedDeviceId === deviceId) return;
  const host = serverIp || hostname;
  if (!host) throw new Error("workspace has no address yet (still provisioning)");
  const tunnelUrls = [
    hostname ? `https://${hostname}` : "",
    serverIp ? `http://${serverIp}:18080` : "",
  ].filter(Boolean);
  await agentClient.connect(host, 18080, token, deviceId, { tunnelUrls });
}

function ManagedMachineActions({
  deviceId,
  serverIp,
  hostname,
  token,
}: {
  deviceId?: string;
  serverIp?: string;
  hostname?: string;
  token?: string | null;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const [out, setOut] = useState<string | null>(null);
  const [gitSession, setGitSession] = useState<{ id: string; uri: string; code: string } | null>(null);

  if (!deviceId) {
    return (
      <p className="mt-1 text-[10px] text-slate-400">
        Actions appear once the workspace has registered its agent (deviceId pending).
      </p>
    );
  }

  const run = async (label: string, verb: string, payload: Record<string, unknown>) => {
    setBusy(label);
    setOut(null);
    try {
      await ensureBoxConnected(deviceId, serverIp, hostname, token);
      const r = await agentClient.callOps(verb, { ...payload, deviceId });
      if (r.ok === false || (r as any)?.error) {
        setOut(`✗ ${(r as any)?.error || "failed"}`);
      } else {
        setOut(`✓ ${label}`);
      }
      return r;
    } catch (e: any) {
      setOut(`✗ ${e?.message || String(e)}`);
      return null;
    } finally {
      setBusy(null);
    }
  };

  const connectGit = async (provider: "github" | "gitlab") => {
    const r = await run(`connect ${provider}`, "git_connect", { provider });
    const init = (r as any)?.initial;
    if (init?.user_code && init?.verification_uri) {
      setGitSession({ id: init.sessionId, uri: init.verification_uri, code: init.user_code });
      window.open(init.verification_uri, "_blank", "noopener");
    }
  };
  const checkGit = async () => {
    if (!gitSession) return;
    setBusy("check");
    try {
      await ensureBoxConnected(deviceId, serverIp, hostname, token);
      const r = await agentClient.callOps("git_connect_status", { sessionId: gitSession.id, deviceId });
      const st = (r as any)?.initial?.state;
      setOut(st === "done" ? `✓ git connected (${(r as any)?.initial?.username ?? "ok"})` : `state: ${st ?? "?"}`);
      if (st === "done") setGitSession(null);
    } finally {
      setBusy(null);
    }
  };

  const Btn = ({ id, onClick, children }: { id: string; onClick: () => void; children: any }) => (
    <button
      disabled={busy !== null}
      onClick={onClick}
      className="rounded border border-slate-300 px-2 py-0.5 text-[10px] font-medium disabled:opacity-50 dark:border-surface-700"
    >
      {busy === id ? "…" : children}
    </button>
  );

  return (
    <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
      <Btn id="connect github" onClick={() => void connectGit("github")}>Connect GitHub</Btn>
      <Btn id="connect gitlab" onClick={() => void connectGit("gitlab")}>Connect GitLab</Btn>
      {gitSession ? (
        <Btn id="check" onClick={() => void checkGit()}>
          ✓ I authorized {gitSession.code}
        </Btn>
      ) : null}
      <Btn id="push git" onClick={() => void run("push git", "git_push", {})}>Push git creds</Btn>
      <Btn id="reload" onClick={() => void run("reload", "reload", {})}>Reload</Btn>
      <Btn id="web preview" onClick={() => void run("web preview", "web-preview", {})}>Web preview</Btn>
      <Btn id="deploy" onClick={() => void run("deploy", "deploy", {})}>Deploy</Btn>
      {out ? (
        <span className={`text-[10px] ${out.startsWith("✗") ? "text-rose-500" : "text-slate-500 dark:text-surface-400"}`}>
          {out}
        </span>
      ) : null}
    </div>
  );
}

// RunnerAuthCTA — #9: one-click runner OAuth for a managed box.
// Drives the runner_auth ops verb (browser device-code flow) exactly
// like git_connect: start → open URL → poll → on done flip Convex
// runnersAuthorized so the UI clears Unauthorized. Subscription
// OAuth, never API keys; tokens land on the box P2P, never Convex.
function RunnerAuthCTA({
  deviceId,
  machineId,
  serverIp,
  hostname,
  token,
  onAuthorized,
}: {
  deviceId?: string;
  machineId: string;
  serverIp?: string;
  hostname?: string;
  token: string | null | undefined;
  onAuthorized: () => void;
}) {
  const [sess, setSess] = useState<{ id: string; uri: string; code: string; runner: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [authCode, setAuthCode] = useState("");

  if (!deviceId) {
    return (
      <span className="text-[11px] text-slate-400">
        Authorize unlocks once the workspace has registered its agent.
      </span>
    );
  }

  const start = async (runner: string) => {
    setBusy(true);
    setMsg(null);
    try {
      await ensureBoxConnected(deviceId, serverIp, hostname, token);
      const r = await agentClient.callOps("runner_auth", { op: "browser_start", runner, deviceId });
      const init = (r as any)?.initial ?? {};
      const uri = init.verification_uri || init.verificationUri;
      const code = init.user_code || init.userCode;
      const id = init.sessionId || init.session_id;
      if ((r as any)?.ok === false || !uri || !id) {
        setMsg(`✗ ${(r as any)?.error || "could not start auth"}`);
        return;
      }
      setSess({ id, uri, code: code || "", runner });
      setAuthCode("");
      window.open(uri, "_blank", "noopener");
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const submitCode = async () => {
    if (!sess || !authCode.trim()) return;
    setBusy(true);
    setMsg("verifying on the remote workspace...");
    try {
      await ensureBoxConnected(deviceId, serverIp, hostname, token);
      const r = await agentClient.callOps("runner_auth", {
        op: "submit_code",
        sessionId: sess.id,
        code: authCode.trim(),
        deviceId,
      });
      if ((r as any)?.ok === false) {
        setMsg(`✗ ${(r as any)?.error || "could not submit auth code"}`);
        return;
      }
      setAuthCode("");
      await check();
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const check = async () => {
    if (!sess) return;
    setBusy(true);
    try {
      await ensureBoxConnected(deviceId, serverIp, hostname, token);
      const r = await agentClient.callOps("runner_auth", { op: "browser_status", sessionId: sess.id, deviceId });
      const st = (r as any)?.initial?.state ?? (r as any)?.initial?.status;
      if (st === "done" || st === "authorized") {
        setMsg(`✓ ${sess.runner} authorized`);
        setSess(null);
        try {
          await fetch(`${CONVEX_URL}/billing/yaver-cloud/runners-authorized`, {
            method: "POST",
            headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
            body: JSON.stringify({ machineId }),
          });
        } catch {
          /* non-fatal — reconcile self-corrects */
        }
        onAuthorized();
      } else {
        setMsg(`state: ${st ?? "pending"}`);
      }
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <span className="flex flex-wrap items-center gap-1.5">
      {!sess ? (
        <>
          <button
            disabled={busy}
            onClick={() => void start("claude")}
            className="rounded border border-amber-500/50 px-2 py-0.5 font-semibold text-amber-700 disabled:opacity-50 dark:text-amber-300"
          >
            {busy ? "…" : "Authorize Claude"}
          </button>
          <button
            disabled={busy}
            onClick={() => void start("codex")}
            className="rounded border border-slate-300 px-2 py-0.5 font-semibold disabled:opacity-50 dark:border-surface-700"
          >
            Codex
          </button>
        </>
      ) : (
        <>
          {sess.runner === "claude" ? (
            <>
              <input
                value={authCode}
                onChange={(e) => setAuthCode(e.target.value)}
                onPaste={(e) => {
                  const pasted = e.clipboardData.getData("text") || "";
                  const cleaned = pasted.trim();
                  if (cleaned !== pasted) {
                    e.preventDefault();
                    setAuthCode(cleaned);
                  }
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && authCode.trim()) {
                    e.preventDefault();
                    void submitCode();
                  }
                }}
                placeholder="Claude auth code"
                spellCheck={false}
                autoComplete="off"
                autoCorrect="off"
                autoCapitalize="off"
                className="w-40 rounded border border-slate-300 bg-white px-2 py-0.5 font-mono text-[11px] text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-100"
              />
              <button
                disabled={busy || !authCode.trim()}
                onClick={() => void submitCode()}
                className="rounded border border-emerald-500/50 px-2 py-0.5 font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
              >
                Submit Claude code
              </button>
            </>
          ) : null}
          <button
            disabled={busy}
            onClick={() => void check()}
            className="rounded border border-emerald-500/50 px-2 py-0.5 font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
          >
            Check {sess.runner} {sess.code ? `(${sess.code})` : ""}
          </button>
        </>
      )}
      {msg ? (
        <span className={`text-[10px] ${msg.startsWith("✗") ? "text-rose-500" : "text-slate-500 dark:text-surface-400"}`}>
          {msg}
        </span>
      ) : null}
    </span>
  );
}

// Slim one-line summary for the Devices index. The full buy/manage
// flow now lives on its own Cloud page (so it doesn't pollute the
// Devices list and has room to grow into the paid surface) — this just
// shows machine count + allowance and links there.
export function ManagedCloudSummary({
  token,
  onOpen,
}: {
  token: string | null | undefined;
  onOpen: () => void;
}) {
  const [access, setAccess] = useState<boolean | null>(null);
  const [count, setCount] = useState(0);
  const [active, setActive] = useState(0);
  const [allowanceLabel, setAllowanceLabel] = useState<string | null>(null);

  useEffect(() => {
    if (!token) return;
    let alive = true;
    void (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/subscription`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        const data = await res.json().catch(() => ({}));
        if (!alive) return;
        setAccess(data?.cloudAccess === true || data?.cloudPreviewOwner === true);
        const ms = Array.isArray(data?.machines) ? data.machines : [];
        const live = ms.filter((m: ManagedMachine) => m.status !== "stopped");
        setCount(live.length);
        setActive(live.filter((m: ManagedMachine) => m.status === "active").length);
      } catch {
        /* non-fatal */
      }
      try {
        const b = await fetch(`${CONVEX_URL}/billing/yaver-cloud/balance`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (b.ok && alive) {
          const j = await b.json().catch(() => ({}));
          const remaining = j?.allowance?.remainingStandardCredits;
          const included = j?.allowance?.includedStandardCredits;
          setAllowanceLabel(
            typeof remaining === "number" && typeof included === "number"
              ? `${remaining.toFixed(1)} standard credits left of ${included}`
              : null,
          );
        }
      } catch {
        /* non-fatal */
      }
    })();
    return () => {
      alive = false;
    };
  }, [token]);

  if (!token || access !== true) return null;

  return (
    <button
      onClick={onOpen}
      className="flex w-full items-center justify-between gap-3 rounded-xl border border-slate-300 bg-white/60 px-4 py-3 text-left text-sm font-semibold text-slate-700 transition-colors hover:border-sky-500/50 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)] dark:text-surface-200"
    >
      <span className="flex flex-wrap items-center gap-2">
        <span>Cloud Workspace</span>
        {count > 0 ? (
          <span className="rounded-full bg-sky-500/15 px-2 py-0.5 text-[11px] font-semibold text-sky-600 dark:text-sky-300">
            {count} workspace{count === 1 ? "" : "s"}
            {active > 0 ? ` · ${active} active` : ""}
          </span>
        ) : (
          <span className="text-xs font-normal text-slate-400">subscribe for a saved workspace</span>
        )}
      </span>
      <span className="flex items-center gap-2 text-xs font-normal text-slate-500 dark:text-surface-400">
        {allowanceLabel ? <span>{allowanceLabel}</span> : null}
        <span className="opacity-60">→</span>
      </span>
    </button>
  );
}

export function ManagedCloudPanel({
  token,
  standalone,
}: {
  token: string | null | undefined;
  standalone?: boolean;
}) {
  const [machines, setMachines] = useState<ManagedMachine[]>([]);
  const [open, setOpen] = useState(standalone ?? false);
  // Owner-only private preview. Server (/subscription cloudPreviewOwner
  // = isCloudPreviewUser allowlist) is the source of truth — never a
  // hardcoded name. null = unknown (render nothing, don't flash the
  // panel to non-owners); false = hide entirely; true = show. This is
  // cosmetic — every buy/provision route is independently 403'd
  // server-side, so hiding is UX, not the security boundary.
  const [owner, setOwner] = useState<boolean | null>(null);
  // access = owner allowlist OR the YAVER_CLOUD_PUBLIC launch flag. The
  // panel renders when EITHER is true; still cosmetic (routes 403 too).
  const [access, setAccess] = useState<boolean | null>(null);
  const [allowance, setAllowance] = useState<{
    includedStandardCredits?: number;
    usedStandardCredits?: number;
    remainingStandardCredits?: number;
  } | null>(null);
  const [wakeRuns, setWakeRuns] = useState<CloudWakeRun[]>([]);
  const [adoptId, setAdoptId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [selectedPlan, setSelectedPlan] = useState<PaidProductId>("cloud-workspace");
  const [region, setRegion] = useState("eu");
  const [showAdopt, setShowAdopt] = useState(false);

  async function buy() {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/checkout`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ productId: selectedPlan, region }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.url) {
        // 403 = not owner-allowlisted → map to a user-meaningful message
        // rather than a bare "checkout failed: 403". Other errors get a
        // clean fallback (don't leak LemonSqueezy/env internals).
        if (res.status === 403) {
          setError("Please sign in on the web to subscribe.");
        } else {
          setError(
            typeof data?.error === "string" && data.error.length <= 120 && !/[<{]/.test(data.error)
              ? data.error
              : "Couldn't start checkout. Please try again.",
          );
        }
        return;
      }
      window.location.href = data.url; // → LemonSqueezy
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  }

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/subscription`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        // A non-OK response means we couldn't load — distinct from a
        // genuinely empty machine list (which is res.ok with []).
        setLoadError(true);
        return;
      }
      const data = await res.json().catch(() => ({}));
      setOwner(data?.cloudPreviewOwner === true);
      setAccess(data?.cloudAccess === true || data?.cloudPreviewOwner === true);
      setMachines(Array.isArray(data?.machines) ? data.machines : []);
      setLoadError(false);
      void listRecentWakeRuns(token, { limit: 6 })
        .then(setWakeRuns)
        .catch(() => {
          /* non-fatal */
        });
      // Allowance is best-effort; if it fails, the subscription/resource rows
      // still render.
      void (async () => {
        try {
          const bRes = await fetch(`${CONVEX_URL}/billing/yaver-cloud/balance`, { headers: { Authorization: `Bearer ${token}` } });
          if (bRes.ok) {
            const b = await bRes.json().catch(() => ({}));
            setAllowance(b?.allowance ?? null);
          }
        } catch {
          /* non-fatal */
        }
      })();
    } catch {
      setLoadError(true);
    }
  }, [token]);

  // Provision is async (LemonSqueezy webhook → provider → cloud-init →
  // agent heartbeat). Poll while the panel is open so a freshly
  // bought/adopted box flips provisioning → active without a manual
  // refresh. 8s is gentle; stops when the panel is closed.
  useEffect(() => {
    if (!open) return;
    void load();
    const iv = setInterval(() => void load(), 8000);
    return () => clearInterval(iv);
  }, [open, load]);

  // Auto-expand the moment the user has ANY managed box, so a
  // provisioning/active/unauthorized box is never hidden in a
  // collapsed panel (project_managed_cloud_onboarding_gap — users
  // repeatedly "can't see" their bought box otherwise).
  useEffect(() => {
    if (!token) return;
    let alive = true;
    (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/subscription`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        const data = await res.json().catch(() => ({}));
        if (alive) {
          setOwner(data?.cloudPreviewOwner === true);
          setAccess(data?.cloudAccess === true || data?.cloudPreviewOwner === true);
        }
        const ms = Array.isArray(data?.machines) ? data.machines : [];
        if (alive && ms.length > 0) {
          setMachines(ms);
          setOpen(true);
        }
      } catch {
        /* non-fatal — panel still opens manually */
      }
    })();
    return () => {
      alive = false;
    };
  }, [token]);

  const provisioning = machines.filter(
    (m) =>
      m.status === "provisioning" ||
      m.status === "stopping" ||
      m.status === "resuming",
  ).length;
  const active = machines.filter((m) => m.status === "active").length;
  // Paused = compute deleted to cut cost; resumable from volume/base image or
  // legacy snapshot.
  // suspended = auto-paused after allowance/cost guardrail breach — same UX.
  const paused = machines.filter(
    (m) => m.status === "paused" || m.status === "suspended",
  ).length;
  // A removed/decommissioned box is "stopped" — don't show dead boxes
  // at all (they clutter the panel and confuse "is this still mine").
  // Only live/in-flight machines are listed.
  const liveMachines = machines.filter((m) => m.status !== "stopped");

  async function post(path: string, body: Record<string, unknown>) {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await fetch(`${CONVEX_URL}${path}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(data?.error || `${path} failed: ${res.status}`);
      } else {
        setNote(data?.note || data?.mode || "ok");
        await load();
      }
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!token) return null;
  const cloudCount = liveMachines.length;
  const allowanceText =
    typeof allowance?.remainingStandardCredits === "number" &&
    typeof allowance?.includedStandardCredits === "number"
      ? `${allowance.remainingStandardCredits.toFixed(1)} standard credits left of ${allowance.includedStandardCredits}`
      : null;

  return (
    <div className="mt-4 rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between gap-3 text-left text-sm font-semibold text-slate-700 dark:text-surface-200"
      >
        <span className="flex flex-wrap items-center gap-2">
          <span>Cloud Workspace</span>
          {cloudCount > 0 ? (
            <span className="rounded-full bg-sky-500/15 px-2 py-0.5 text-[11px] font-semibold text-sky-600 dark:text-sky-300">
              {cloudCount} machine{cloudCount === 1 ? "" : "s"}
              {active > 0 ? ` · ${active} active` : ""}
              {paused > 0 ? ` · ${paused} paused` : ""}
              {provisioning > 0 ? ` · ${provisioning} starting` : ""}
            </span>
          ) : (
            <span className="text-xs font-normal text-slate-400">subscribe for a saved workspace</span>
          )}
        </span>
        <span className="flex items-center gap-2 text-xs font-normal text-slate-500 dark:text-surface-400">
          {allowanceText ? <span>{allowanceText}</span> : null}
          <span className="opacity-60">{open ? "▾" : "▸"}</span>
        </span>
      </button>

      {open ? (
        <div className="mt-3 space-y-3">
          <p className="text-xs text-slate-500 dark:text-surface-400">
            Web-only subscription for a saved Cloud Workspace. Mobile can
            control an existing workspace, but checkout stays out of the App
            Store / Play Store app.
          </p>

          {/* HN-LAUNCH-HIDE-PAID: plan cards + web checkout CTA. */}
          {!HIDE_PAID_UI && (<>
          <div className="grid gap-2 md:grid-cols-2">
            {CLOUD_PLANS.map((plan) => {
              const activePlan = selectedPlan === plan.id;
              return (
                <button
                  key={plan.id}
                  onClick={() => setSelectedPlan(plan.id)}
                  className={`rounded-lg border p-3 text-left transition-colors ${
                    activePlan
                      ? "border-emerald-500/60 bg-emerald-500/10"
                      : "border-slate-300 bg-white/40 hover:border-sky-500/50 dark:border-surface-700 dark:bg-[rgba(12,12,16,0.5)]"
                  }`}
                >
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <p className="text-sm font-bold text-slate-800 dark:text-surface-100">{plan.name}</p>
                      <p className="mt-0.5 text-[11px] text-slate-500 dark:text-surface-400">{plan.detail}</p>
                    </div>
                    <div className="text-right">
                      <p className="text-lg font-bold text-slate-900 dark:text-surface-50">{plan.price}</p>
                      <p className="text-[10px] text-slate-400">{plan.label}</p>
                    </div>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {plan.bullets.map((item) => (
                      <span
                        key={item}
                        className="rounded-full bg-slate-500/10 px-2 py-0.5 text-[10px] font-medium text-slate-500 dark:text-surface-300"
                      >
                        {item}
                      </span>
                    ))}
                  </div>
                </button>
              );
            })}
          </div>

          <div className="rounded-md border border-sky-500/30 bg-sky-500/5 p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm font-bold text-slate-800 dark:text-surface-100">
                {allowanceText ?? "Workspace allowance"}
              </span>
              <span className="text-[11px] text-slate-500 dark:text-surface-400">
                Cloud Workspace includes monthly use; heavy/build work uses it faster.
              </span>
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <span className="text-[11px] text-slate-500 dark:text-surface-400">
                Free includes limited shared relay usage. Upgrade for a private relay or saved cloud workspace. If allowance is exhausted, Cloud Workspace compute pauses until the next period or billing settings are updated.
              </span>
              {selectedPlan === "cloud-workspace" ? <span className="flex gap-1">
                {["eu", "us"].map((r) => (
                  <button
                    key={r}
                    onClick={() => setRegion(r)}
                    className={`rounded border px-2 py-0.5 text-[11px] font-semibold uppercase ${
                      region === r
                        ? "border-emerald-500/60 bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
                        : "border-slate-300 text-slate-500 dark:border-surface-700 dark:text-surface-400"
                    }`}
                  >
                    {r}
                  </button>
                ))}
              </span> : null}
              <button
                disabled={busy}
                onClick={() => void buy()}
                className="rounded-md border border-emerald-500/50 bg-emerald-500/10 px-3 py-1 text-xs font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
              >
                {busy ? "…" : "Subscribe on web"}
              </button>
            </div>
            <p className="mt-1.5 text-[10px] text-slate-400">
              Free is not a product to buy: it is the limited shared public relay.
              Cloud Workspace provisions after LemonSqueezy confirms payment.
            </p>
          </div>
          </>)}
          {/* End HN-LAUNCH-HIDE-PAID buy block. */}

          {/* Adopt is an owner/dev path — tucked behind a toggle so it
              doesn't clutter the index for everyone else. */}
          {owner ? (
            showAdopt ? (
              <div className="flex flex-wrap items-center gap-2">
                <input
                  value={adoptId}
                  onChange={(e) => setAdoptId(e.target.value)}
                  placeholder="Existing cloud resource id to adopt"
                  className="flex-1 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-xs dark:border-surface-700 dark:bg-[rgba(12,12,16,0.9)]"
                />
                <button
                  disabled={busy || adoptId.trim() === ""}
                  onClick={() => post("/billing/yaver-cloud/dev-adopt", { hetznerServerId: adoptId.trim() })}
                  className="rounded-md border border-slate-300 px-3 py-1.5 text-xs font-semibold disabled:opacity-50 dark:border-surface-700"
                >
                  {busy ? "…" : "Adopt"}
                </button>
              </div>
            ) : (
              <button
                onClick={() => setShowAdopt(true)}
                className="text-[11px] font-medium text-slate-500 underline-offset-2 hover:underline dark:text-surface-400"
              >
                Adopt an existing cloud workspace →
              </button>
            )
          ) : null}

          <div className="space-y-2">
            {loadError ? (
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="text-slate-500 dark:text-surface-400">
                  Couldn&apos;t load your managed machines.
                </span>
                <button
                  onClick={() => void load()}
                  className="rounded border border-slate-300 px-2 py-0.5 text-[11px] font-medium dark:border-surface-700"
                >
                  Retry
                </button>
              </div>
            ) : liveMachines.length === 0 ? (
              <p className="text-xs text-slate-400">No managed machines.</p>
            ) : (
              liveMachines.map((m) => (
                <div
                  key={m.id}
                  className="rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800"
                >
                 <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span
                      className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${
                        (m.origin ?? "managed") === "managed"
                          ? "bg-sky-500/15 text-sky-600 dark:text-sky-300"
                          : "bg-slate-500/15 text-slate-500 dark:text-surface-300"
                      }`}
                    >
                      {m.origin ?? "managed"}
                    </span>
                    <span className="font-mono opacity-80">
                      {m.machineType ?? "standard"} · {m.region ?? "eu"} ·{" "}
                      <span className={m.status === "error" ? "font-semibold text-rose-600 dark:text-rose-400" : ""}>
                        {m.status ?? "?"}
                      </span>
                    </span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    {m.status === "active" ? (
                      <button
                        disabled={busy}
                        onClick={() => {
                          if (
                            !window.confirm(
                              "Pause this workspace? It preserves state, deletes active compute, " +
                                "and stops compute spend. Resume recreates it when you need it.",
                            )
                          )
                            return;
                          void post("/billing/yaver-cloud/stop", { machineId: m.id });
                        }}
                        className="rounded border border-amber-400/50 px-2 py-0.5 text-[10px] font-semibold text-amber-600 disabled:opacity-50 dark:text-amber-400"
                        title="Preserve state and stop active compute spend"
                      >
                        ⏸ Pause
                      </button>
                    ) : null}
                    <button
                      disabled={busy}
                      onClick={() => {
                        if (
                          !window.confirm(
                            "Delete this Cloud Workspace? " +
                              "This decommissions the managed cloud resource and stops its billing. This cannot be undone - use Pause instead if you just want to save cost temporarily.",
                        )
                        )
                          return;
                        void post("/billing/yaver-cloud/dev-deprovision", { machineId: m.id });
                      }}
                      className="rounded border border-rose-400/50 px-2 py-0.5 text-[10px] font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-400"
                    >
                      Delete workspace
                    </button>
                  </div>
                 </div>
                  {(() => {
                    // Paused / suspended → calm parked state + Resume.
                    if (m.status === "paused" || m.status === "suspended") {
                      return (
                        <div className="mt-1.5 flex flex-wrap items-center gap-2 text-[11px]">
                          <span className="rounded bg-slate-500/15 px-1.5 py-0.5 font-semibold text-slate-600 dark:text-surface-300">
                            ⏸ {m.status === "suspended" ? "Suspended" : "Paused"}
                          </span>
                          <span className="text-slate-500 dark:text-surface-400">
                            State kept · active compute stopped
                          </span>
                          <button
                            disabled={busy}
                            onClick={() => void post("/billing/yaver-cloud/start", { machineId: m.id })}
                            className="rounded border border-emerald-500/50 px-2 py-0.5 font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
                          >
                            ▶ Resume
                          </button>
                        </div>
                      );
                    }
                    // A wake gets the real ladder: which step, how long it has
                    // been on it, what the provider sees, and — when the box is
                    // merely blocked on sign-in — that fact instead of a bar.
                    // The old branch was a static ETA for work that depends on
                    // provider state, with no bar and no way to tell progress
                    // from a hang.
                    if (m.status === "resuming" || m.status === "stopping" || m.status === "grace") {
                      return <WakeProgress machine={m} deviceReachable={false} />;
                    }
                    const phase = m.provisionPhase ?? null;
                    const pct =
                      typeof m.provisionProgress === "number"
                        ? m.provisionProgress
                        : m.status === "active"
                          ? 90
                          : 10;
                    const initializing =
                      m.status === "provisioning" ||
                      (!!phase &&
                        phase !== "ready" &&
                        m.status !== "error" &&
                        m.status !== "stopped" &&
                        m.status !== "active");
                    const LABEL: Record<string, string> = {
                      creating: "Reserving your workspace…",
                      booting: "Booting & installing Docker…",
                      "installing-docker": "Installing Docker…",
                      "pulling-image": "Pulling the Yaver image…",
                      "starting-agent": "Starting the Yaver agent…",
                      registering: "Registering your device…",
                      "authorizing-runners": "Almost there — finishing setup…",
                      ready: "Ready",
                      error: "Setup failed",
                      // Wake-only steps. Absent here, they fell through the
                      // `?? phase` fallback below and printed the raw
                      // control-plane slug at the user.
                      "checking-snapshot": "Finding legacy snapshot…",
                      "preparing-volume": "Preparing saved workspace state…",
                      "restoring-snapshot": "Starting workspace…",
                    };
                    // Not progress: the box is up and nothing will change until
                    // the user signs it in. This slug was missing from LABEL, so
                    // it rendered as "Setting up your box — awaiting-yaver-auth"
                    // above a bar creeping toward a flip that could never come.
                    if (phase === "awaiting-yaver-auth") {
                      return <WakeProgress machine={m} deviceReachable={false} />;
                    }
                    if (initializing) {
                      return (
                        <div className="mt-1.5">
                          <div className="mb-1 text-[11px] text-slate-500 dark:text-surface-400">
                            Setting up your workspace —{" "}
                            {/* Never print a raw slug: an unmapped phase is our
                                bug, and the user cannot act on "pulling-image"
                                spelled with a hyphen. */}
                            {phase ? LABEL[phase] ?? "working on it…" : "initializing…"}
                          </div>
                          <div className="h-1.5 w-full overflow-hidden rounded bg-slate-200 dark:bg-surface-800">
                            <div
                              className="h-full rounded bg-sky-500 transition-all duration-700"
                              style={{
                                width: `${Math.max(5, Math.min(100, pct))}%`,
                              }}
                            />
                          </div>
                        </div>
                      );
                    }
                    if (m.status === "active" && !m.runnersAuthorized) {
                      return (
                        <div className="mt-1.5 flex flex-wrap items-center gap-2 text-[11px]">
                          <span className="rounded bg-amber-500/15 px-1.5 py-0.5 font-semibold text-amber-700 dark:text-amber-300">
                            ⚠ Unauthorized
                          </span>
                          <span className="text-slate-500 dark:text-surface-400">
                            Box is up — sign your coding agents in:
                          </span>
                          <RunnerAuthCTA
                            deviceId={m.deviceId}
                            machineId={m.id}
                            serverIp={m.serverIp}
                            hostname={m.hostname}
                            token={token}
                            onAuthorized={() => void load()}
                          />
                        </div>
                      );
                    }
                    return null;
                  })()}
                  {m.errorMessage ? (
                    <p className="mt-1.5 text-[11px] text-rose-600 dark:text-rose-400">
                      {m.errorMessage}
                    </p>
                  ) : null}
                  <ManagedMachineActions
                    deviceId={m.deviceId}
                    serverIp={m.serverIp}
                    hostname={m.hostname}
                    token={token}
                  />
                </div>
              ))
            )}
          </div>

          {wakeRuns.length > 0 ? (
            <div className="rounded-md border border-slate-200 px-3 py-2 dark:border-surface-800">
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <p className="text-xs font-bold text-slate-700 dark:text-surface-200">
                  Recent workspace activity
                </p>
                <button
                  disabled={busy}
                  onClick={() => void load()}
                  className="rounded border border-slate-300 px-2 py-0.5 text-[10px] font-medium text-slate-500 disabled:opacity-50 dark:border-surface-700 dark:text-surface-400"
                >
                  Refresh
                </button>
              </div>
              <div className="space-y-1">
                {wakeRuns.map((run) => {
                  const pct =
                    typeof run.progress === "number"
                      ? Math.max(0, Math.min(100, Math.round(run.progress)))
                      : null;
                  return (
                    <div
                      key={run.id}
                      className="flex flex-wrap items-center gap-2 text-[11px] text-slate-500 dark:text-surface-400"
                    >
                      <span className={`rounded-full px-2 py-0.5 font-semibold ${wakeRunTone(run.status)}`}>
                        {run.status}
                      </span>
                      <span className="min-w-0 flex-1 truncate">
                        {wakeRunLabel(run)}
                        {run.machineType ? ` · ${run.machineType}` : ""}
                      </span>
                      {pct !== null ? (
                        <span className="font-mono tabular-nums">{pct}%</span>
                      ) : null}
                      <span className="font-mono tabular-nums">{timeAgo(run.updatedAt || run.startedAt)}</span>
                    </div>
                  );
                })}
              </div>
              <p className="mt-1.5 text-[10px] text-slate-400">
                This tracks provisioning, wake and park steps only. Prompts, repo paths and output stay off the ledger.
              </p>
            </div>
          ) : null}

          {note ? <p className="text-xs text-emerald-600 dark:text-emerald-400">✓ {note}</p> : null}
          {error ? <p className="text-xs text-rose-600 dark:text-rose-400">{error}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
