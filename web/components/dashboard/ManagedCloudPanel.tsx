"use client";

// ManagedCloudPanel — owner/dev surface for ADDING managed-cloud
// boxes (docs/managed-cloud-host-lifecycle.md): buy one via
// LemonSqueezy, or ADOPT an existing cloud box as a managed machine
// (allowlist-gated server-side). Every managed row carries the
// `origin` provenance tag ("managed" = bought from / adopted by
// Yaver; plain BYO devices in the list above are "self-hosted"), and
// each device card now shows that same Self-hosted / Yaver Cloud
// label inline.
//
// Removal is intentionally NOT here — the per-device "♻ Recycle box"
// action on each card owns teardown (snapshot + delete, dry-run
// first). Keeping decommission in one place avoids two divergent
// destroy paths.
//
// Non-owners just see an empty list / 403s — the gate is the server
// (isCloudPreviewUser), never the client.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient } from "@/lib/agent-client";

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
  runnersAuthorized?: boolean;
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
  if (!deviceId) throw new Error("box agent not registered yet");
  if (!token) throw new Error("not signed in");
  if (agentClient.isConnected && agentClient.connectedDeviceId === deviceId) return;
  const host = serverIp || hostname;
  if (!host) throw new Error("box has no address yet (still provisioning)");
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
        Actions appear once the box has registered its agent (deviceId pending).
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

  if (!deviceId) {
    return (
      <span className="text-[11px] text-slate-400">
        Authorize unlocks once the box has registered its agent.
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
      window.open(uri, "_blank", "noopener");
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
        <button
          disabled={busy}
          onClick={() => void check()}
          className="rounded border border-emerald-500/50 px-2 py-0.5 font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
        >
          ✓ I authorized {sess.runner} {sess.code ? `(${sess.code})` : ""}
        </button>
      )}
      {msg ? (
        <span className={`text-[10px] ${msg.startsWith("✗") ? "text-rose-500" : "text-slate-500 dark:text-surface-400"}`}>
          {msg}
        </span>
      ) : null}
    </span>
  );
}

export function ManagedCloudPanel({ token }: { token: string | null | undefined }) {
  const [machines, setMachines] = useState<ManagedMachine[]>([]);
  const [open, setOpen] = useState(false);
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
  const [balanceCents, setBalanceCents] = useState<number | null>(null);
  const [hourlyCents, setHourlyCents] = useState<number | null>(null);
  const [lowBalance, setLowBalance] = useState(false);
  const [packs, setPacks] = useState<Array<{ id: string; cents: number; label: string }>>([]);
  const [adoptId, setAdoptId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [machineType, setMachineType] = useState("cpu");
  const [region, setRegion] = useState("eu");

  async function buy() {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/yaver-cloud/checkout`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ machineType, region }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.url) {
        // 403 = not owner-allowlisted → map to a user-meaningful message
        // rather than a bare "checkout failed: 403". Other errors get a
        // clean fallback (don't leak LemonSqueezy/env internals).
        if (res.status === 403) {
          setError("Managed cloud is in private preview.");
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
      // Wallet + catalog (best-effort; null balance just hides the row).
      void (async () => {
        try {
          const [bRes, pRes] = await Promise.all([
            fetch(`${CONVEX_URL}/billing/yaver-cloud/balance`, { headers: { Authorization: `Bearer ${token}` } }),
            fetch(`${CONVEX_URL}/billing/credits/packs`, { headers: { Authorization: `Bearer ${token}` } }),
          ]);
          if (bRes.ok) {
            const b = await bRes.json().catch(() => ({}));
            setBalanceCents(typeof b?.balanceCents === "number" ? b.balanceCents : null);
            setHourlyCents(typeof b?.estimatedHourlyCents === "number" ? b.estimatedHourlyCents : null);
            setLowBalance(b?.lowBalance === true);
          }
          if (pRes.ok) {
            const p = await pRes.json().catch(() => ({}));
            if (Array.isArray(p?.packs)) setPacks(p.packs);
          }
        } catch {
          /* non-fatal */
        }
      })();
    } catch {
      setLoadError(true);
    }
  }, [token]);

  // Add credit (OpenAI-style): create a one-time pack checkout and send
  // the browser to LemonSqueezy. Webhook credits the wallet on payment.
  async function addCredit(packId: string) {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/credits/checkout`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ packId }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.url) {
        setError(
          res.status === 503
            ? "Credit packs aren't configured yet (owner: set the LemonSqueezy pack variant ids)."
            : typeof data?.error === "string" && data.error.length <= 140 && !/[<{]/.test(data.error)
              ? data.error
              : "Couldn't start top-up. Please try again.",
        );
        return;
      }
      window.location.href = data.url; // → LemonSqueezy
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  }

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
  // Paused = snapshotted + server deleted to cut cost; resumable.
  // suspended = auto-paused (prepaid floor breach) — same UX.
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
  // Private preview / launch-gated: render NOTHING for non-access users
  // (and while access is still unknown, so the panel never flashes).
  // Server independently 403s every action regardless.
  if (access !== true) return null;

  const money = (cents: number | null) =>
    typeof cents === "number" ? `$${(cents / 100).toFixed(2)}` : "—";

  return (
    <div className="mt-4 rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between text-left text-sm font-semibold text-slate-700 dark:text-surface-200"
      >
        <span>☁ Managed cloud — buy / adopt</span>
        <span className="text-xs opacity-60">{open ? "▾" : "▸"}</span>
      </button>

      {open ? (
        <div className="mt-3 space-y-3">
          <p className="text-xs text-slate-500 dark:text-surface-400">
            Boxes here are <b>managed</b> (provisioned/adopted by Yaver) — each
            device card shows a <b>Yaver Cloud</b> badge. Every other device is{" "}
            <b>self-hosted</b> (your own cloud box or hardware). Adopt imitates a
            managed purchase for an existing box. To remove a box, use the{" "}
            <b>♻ Delete box</b> button on its row below — it snapshots, then
            decommissions the cloud resource and stops billing.
          </p>

          {/* Prepaid wallet — OpenAI-style credit. Top up on the web
              (no app-store billing); spend it on compute. */}
          <div className="rounded-md border border-sky-500/30 bg-sky-500/5 p-3">
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <span className="text-sm font-bold text-slate-800 dark:text-surface-100">
                {money(balanceCents)}
              </span>
              {typeof hourlyCents === "number" ? (
                <span className="text-[11px] text-slate-500 dark:text-surface-400">
                  ~{money(hourlyCents)}/hr running
                </span>
              ) : null}
              {lowBalance ? (
                <span className="text-[11px] font-semibold text-amber-600 dark:text-amber-400">
                  Low balance
                </span>
              ) : null}
            </div>
            <p className="mb-1.5 text-[11px] text-slate-500 dark:text-surface-400">Add credit</p>
            <div className="flex flex-wrap gap-2">
              {(packs.length
                ? packs
                : [
                    { id: "p10", cents: 1000, label: "$10" },
                    { id: "p25", cents: 2500, label: "$25" },
                    { id: "p50", cents: 5000, label: "$50" },
                    { id: "p100", cents: 10000, label: "$100" },
                  ]
              ).map((p) => (
                <button
                  key={p.id}
                  disabled={busy}
                  onClick={() => void addCredit(p.id)}
                  className="rounded-md border border-sky-500/50 bg-sky-500/10 px-3 py-1.5 text-xs font-semibold text-sky-700 disabled:opacity-50 dark:text-sky-300"
                >
                  + {p.label}
                </button>
              ))}
              <button
                disabled={busy}
                onClick={() => post("/billing/yaver-cloud/provision", { machineType, region })}
                className="rounded-md border border-emerald-500/50 bg-emerald-500/10 px-3 py-1.5 text-xs font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
              >
                {busy ? "…" : "Spin up (prepaid)"}
              </button>
            </div>
            <p className="mt-1.5 text-[10px] text-slate-400">
              Opens a secure checkout; credit is added when payment clears. Spin
              up bills from your balance — no subscription.
            </p>
          </div>

          {/* Subscription buy + adopt are owner/dev paths (server still
              owner-gates them); prepaid is the public front door above. */}
          {owner ? (
          <>
          <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 p-3">
            <p className="mb-2 text-xs font-semibold text-emerald-700 dark:text-emerald-300">
              Buy a managed box (subscription)
            </p>
            <div className="flex flex-wrap items-end gap-2">
              <label className="text-[11px] text-slate-500 dark:text-surface-400">
                Machine
                <select
                  value={machineType}
                  onChange={(e) => setMachineType(e.target.value)}
                  className="mt-1 block rounded-md border border-slate-300 bg-white px-2 py-1.5 text-xs dark:border-surface-700 dark:bg-[rgba(12,12,16,0.9)]"
                >
                  <option value="cpu">CPU — React Native/Hermes + web + deploy (default)</option>
                  <option value="gpu" disabled>GPU — AI / Ollama — coming soon</option>
                  <option value="kvm" disabled>Flutter/Kotlin emulator — coming soon</option>
                  <option value="ios" disabled>iOS (Mac) — coming soon</option>
                </select>
              </label>
              <label className="text-[11px] text-slate-500 dark:text-surface-400">
                Region
                <select
                  value={region}
                  onChange={(e) => setRegion(e.target.value)}
                  className="mt-1 block rounded-md border border-slate-300 bg-white px-2 py-1.5 text-xs dark:border-surface-700 dark:bg-[rgba(12,12,16,0.9)]"
                >
                  <option value="eu">eu</option>
                  <option value="us">us</option>
                </select>
              </label>
              <button
                disabled={busy}
                onClick={() => void buy()}
                className="rounded-md border border-emerald-500/50 bg-emerald-500/10 px-3 py-1.5 text-xs font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
              >
                {busy ? "…" : "Buy → checkout"}
              </button>
            </div>
            <p className="mt-1.5 text-[10px] text-slate-400">
              Opens LemonSqueezy; the box auto-provisions on payment. Private
              preview — non-owner accounts get a 403 until launch.
            </p>
          </div>

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
              {busy ? "…" : "Adopt as managed"}
            </button>
          </div>
          </>
          ) : null}

          {machines.length > 0 ? (
            <p className="text-[11px] text-slate-500 dark:text-surface-400">
              {active} active · {paused} paused · {provisioning} provisioning
              {provisioning > 0 ? " — auto-refreshing every 8s, your box appears here when ready" : ""}
            </p>
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
                      {m.machineType ?? "cpu"} · resource {m.hetznerServerId ?? "—"} · {m.region ?? "eu"} ·{" "}
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
                              "Pause this box? It snapshots the disk, then deletes the cloud " +
                                "server so it stops billing — paused costs only ~€0.50/mo (snapshot " +
                                "storage) vs ~€30/mo running. Resume recreates it from the snapshot " +
                                "in ~2-3 min (the box gets a new IP).",
                            )
                          )
                            return;
                          void post("/billing/yaver-cloud/stop", { machineId: m.id });
                        }}
                        className="rounded border border-amber-400/50 px-2 py-0.5 text-[10px] font-semibold text-amber-600 disabled:opacity-50 dark:text-amber-400"
                        title="Snapshot + delete the server to stop billing — resumable"
                      >
                        ⏸ Pause
                      </button>
                    ) : null}
                    <button
                      disabled={busy}
                      onClick={() => {
                        if (
                          !window.confirm(
                            `Delete this managed box (resource ${m.hetznerServerId ?? "—"})? ` +
                              `It snapshots first, then decommissions the cloud resource and stops billing. This cannot be undone — use Pause instead if you just want to save cost temporarily.`,
                        )
                        )
                          return;
                        void post("/billing/yaver-cloud/dev-deprovision", { machineId: m.id });
                      }}
                      className="rounded border border-rose-400/50 px-2 py-0.5 text-[10px] font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-400"
                    >
                      ♻ Delete box
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
                            Snapshot kept · ~€0.50/mo while paused (vs ~€30/mo running)
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
                    if (m.status === "resuming") {
                      return (
                        <div className="mt-1.5 text-[11px] text-sky-600 dark:text-sky-300">
                          Resuming from snapshot — recreating the server (~2-3 min)…
                        </div>
                      );
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
                      creating: "Reserving your box…",
                      booting: "Booting & installing Docker…",
                      "installing-docker": "Installing Docker…",
                      "pulling-image": "Pulling the Yaver image…",
                      "starting-agent": "Starting the Yaver agent…",
                      registering: "Registering your device…",
                      "authorizing-runners": "Almost there — finishing setup…",
                      ready: "Ready",
                      error: "Setup failed",
                    };
                    if (initializing) {
                      return (
                        <div className="mt-1.5">
                          <div className="mb-1 text-[11px] text-slate-500 dark:text-surface-400">
                            Setting up your box —{" "}
                            {phase ? LABEL[phase] ?? phase : "initializing…"}
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

          {note ? <p className="text-xs text-emerald-600 dark:text-emerald-400">✓ {note}</p> : null}
          {error ? <p className="text-xs text-rose-600 dark:text-rose-400">{error}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
