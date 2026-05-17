"use client";

// ManagedCloudPanel — owner/dev surface for the managed-cloud
// lifecycle (docs/managed-cloud-host-lifecycle.md). Lets the owner
// (allowlist-gated server-side, no LemonSqueezy) ADOPT an existing
// Hetzner box as a managed machine and DECOMMISSION it (snapshot +
// delete via the managed teardown path). Every managed row carries
// the `origin` provenance tag ("managed" = bought from / adopted by
// Yaver; plain BYO devices in the list above are "self-hosted").
//
// Non-owners just see an empty list / 403s — the gate is the server
// (isCloudPreviewUser), never the client.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient } from "@/lib/agent-client";

interface ManagedMachine {
  _id: string;
  machineType?: string;
  status?: string;
  origin?: "managed" | "self-hosted";
  hetznerServerId?: string;
  region?: string;
  serverIp?: string;
  errorMessage?: string;
  deviceId?: string;
}

// Per-machine actions (D3 git connect/push, D4 dev-loop, D5 deploy).
// Every action targets the box's EXPLICIT agent deviceId — never a
// guessed/fuzzy target (credentials + exec). Disabled until the box
// has registered (deviceId present). Ops verbs run on the connected
// agent and are routed P2P to the box; tokens never touch Convex.
function ManagedMachineActions({ deviceId }: { deviceId?: string }) {
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

export function ManagedCloudPanel({ token }: { token: string | null | undefined }) {
  const [machines, setMachines] = useState<ManagedMachine[]>([]);
  const [open, setOpen] = useState(false);
  const [adoptId, setAdoptId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
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
        // 403 = private preview (not owner-allowlisted); 500 =
        // LemonSqueezy env not configured. Surface verbatim.
        setError(data?.error || `checkout failed: ${res.status}`);
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
      const data = await res.json().catch(() => ({}));
      setMachines(Array.isArray(data?.machines) ? data.machines : []);
    } catch (e: any) {
      setError(e?.message || String(e));
    }
  }, [token]);

  // Provision is async (LemonSqueezy webhook → Hetzner → cloud-init →
  // agent heartbeat). Poll while the panel is open so a freshly
  // bought/adopted box flips provisioning → active without a manual
  // refresh. 8s is gentle; stops when the panel is closed.
  useEffect(() => {
    if (!open) return;
    void load();
    const iv = setInterval(() => void load(), 8000);
    return () => clearInterval(iv);
  }, [open, load]);

  const provisioning = machines.filter(
    (m) => m.status === "provisioning" || m.status === "stopping",
  ).length;
  const active = machines.filter((m) => m.status === "active").length;

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
        // deprovision schedules an async destroy action — poll so the
        // final row state (stopped, or error w/ the missing-token
        // message) surfaces without a manual refresh.
        if (path.includes("dev-deprovision")) {
          [2000, 5000, 9000].forEach((ms) => setTimeout(() => void load(), ms));
        }
      }
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!token) return null;

  return (
    <div className="mt-4 rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between text-left text-sm font-semibold text-slate-700 dark:text-surface-200"
      >
        <span>☁ Managed cloud — buy / adopt / decommission</span>
        <span className="text-xs opacity-60">{open ? "▾" : "▸"}</span>
      </button>

      {open ? (
        <div className="mt-3 space-y-3">
          <p className="text-xs text-slate-500 dark:text-surface-400">
            Boxes here are <b>managed</b> (provisioned/adopted by Yaver). Every
            other device in the list above is <b>self-hosted</b> (your own
            Hetzner / hardware). Adopt imitates a managed purchase for an
            existing box; Decommission snapshots then deletes it.
          </p>

          <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 p-3">
            <p className="mb-2 text-xs font-semibold text-emerald-700 dark:text-emerald-300">
              Buy a managed box
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
                  <option value="gpu">GPU — AI / Ollama workloads</option>
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
              placeholder="Existing Hetzner server id to adopt"
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

          {machines.length > 0 ? (
            <p className="text-[11px] text-slate-500 dark:text-surface-400">
              {active} active · {provisioning} provisioning
              {provisioning > 0 ? " — auto-refreshing every 8s, your box appears here when ready" : ""}
            </p>
          ) : null}

          <div className="space-y-2">
            {machines.length === 0 ? (
              <p className="text-xs text-slate-400">No managed machines.</p>
            ) : (
              machines.map((m) => (
                <div
                  key={m._id}
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
                      {m.machineType ?? "cpu"} · srv {m.hetznerServerId ?? "—"} · {m.region ?? "eu"} ·{" "}
                      <span className={m.status === "error" ? "font-semibold text-rose-600 dark:text-rose-400" : ""}>
                        {m.status ?? "?"}
                      </span>
                    </span>
                  </div>
                  <button
                    disabled={busy}
                    onClick={() => {
                      if (!window.confirm(`Decommission managed machine ${m._id}?\nSnapshots then deletes the Hetzner box.`)) return;
                      void post("/billing/yaver-cloud/dev-deprovision", { machineId: m._id });
                    }}
                    className="rounded-md border border-rose-400/50 px-2.5 py-1 text-[11px] font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-300"
                  >
                    Decommission
                  </button>
                 </div>
                  {m.errorMessage ? (
                    <p className="mt-1.5 text-[11px] text-rose-600 dark:text-rose-400">
                      {m.errorMessage}
                    </p>
                  ) : null}
                  <ManagedMachineActions deviceId={m.deviceId} />
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
