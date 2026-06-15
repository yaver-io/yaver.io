"use client";

// PackagesView — owner console for Yaver Task Packages (docs/yaver-task-packages.md).
// Lists published packages, runs one once (showing the result incl. MCP-over-MCP
// calls), allocates a package to a runner device, and publishes a manifest.
// Drives the agent's package_* ops verbs via agentClient.callOps.

import { useCallback, useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type PackageRow = {
  name: string;
  kind: string;
  tier: string;
  version: number;
  engines?: string[];
  runtimes?: string[];
  vantage?: { geo?: string[]; residential?: boolean };
};

type RunResult = {
  package: string;
  status: string;
  fields?: Record<string, unknown>;
  sourcesOk?: number;
  sourcesBlocked?: number;
  mcpCalls?: Array<Record<string, unknown>>;
  notes?: string[];
  observationId?: string;
  country?: string;
};

const card = "rounded-xl border border-white/10 bg-white/[0.03] p-4";
const btn =
  "rounded-xl border border-white/10 bg-white/[0.06] px-3 py-1.5 text-sm hover:bg-white/[0.12] disabled:opacity-40";

function statusTone(s: string) {
  if (s === "ok") return "text-emerald-300";
  if (s === "needs_confirmation") return "text-amber-300";
  if (s.startsWith("blocked")) return "text-orange-300";
  return "text-red-300";
}

const SAMPLE = `{
  "metadata": { "name": "price-watch", "description": "watch a public price" },
  "spec": {
    "task": {
      "kind": "collect",
      "engines": ["fetch"],
      "sources": [{
        "id": "sku",
        "url": "https://api.example.com/p/123",
        "render": "fetch",
        "extract": { "price": { "jsonPath": "data.price", "as": "number" } }
      }]
    },
    "vantage": { "geo": ["RS"], "residential": true },
    "consent": { "summary": "fetch a public price page hourly", "willNot": ["login"] }
  }
}`;

export default function PackagesView() {
  const [pkgs, setPkgs] = useState<PackageRow[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<any>(null);
  const [run, setRun] = useState<RunResult | null>(null);
  const [check, setCheck] = useState<any>(null);
  const [forceShare, setForceShare] = useState(false);
  const [manifest, setManifest] = useState(SAMPLE);
  const [allocDevice, setAllocDevice] = useState("");
  const [allocTarget, setAllocTarget] = useState("mobile");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const res = await agentClient.callOps("package_list", {});
      setPkgs(res?.initial?.packages ?? []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const openPackage = useCallback(async (name: string) => {
    setSelected(name);
    setRun(null);
    setCheck(null);
    setForceShare(false);
    try {
      const res = await agentClient.callOps("package_get", { name });
      setDetail(res?.initial ?? null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function publish() {
    setBusy(true);
    setErr(null);
    try {
      const parsed = JSON.parse(manifest);
      const res = await agentClient.callOps("package_publish", parsed);
      if (res?.ok === false) throw new Error(res?.error || "publish failed");
      await load();
      await openPackage(res?.initial?.package?.metadata?.name);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function runOnce(confirm: boolean) {
    if (!selected) return;
    setBusy(true);
    setErr(null);
    try {
      const res = await agentClient.callOps("package_run", { name: selected, confirm });
      setRun(res?.initial?.run ?? null);
      await openPackage(selected);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function runCheck() {
    if (!selected) return;
    setBusy(true);
    setErr(null);
    try {
      const res = await agentClient.callOps("package_check", { name: selected });
      setCheck(res?.initial?.check ?? null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function allocate() {
    if (!selected || !allocDevice.trim()) return;
    setBusy(true);
    setErr(null);
    try {
      const res = await agentClient.callOps("package_allocate", {
        packageName: selected,
        device: allocDevice.trim(),
        target: allocTarget,
        force: forceShare,
      });
      if (res?.ok === false) {
        if (res?.code === "check_required" || res?.code === "check_failed") {
          throw new Error(`${res.error} — run the preflight check above first.`);
        }
        throw new Error(res?.error || "allocate failed");
      }
      setAllocDevice("");
      await openPackage(selected);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const shareReady = forceShare || (check && check.status !== "fail");

  return (
    <div className="flex flex-col gap-4 text-surface-100">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Task Packages</h2>
        <button className={btn} onClick={() => void load()}>
          Refresh
        </button>
      </div>
      {err && <div className="rounded-lg bg-red-950/40 px-3 py-2 text-sm text-red-300">{err}</div>}

      <div className="grid gap-4 md:grid-cols-[260px_1fr]">
        {/* left: package list + publish */}
        <div className="flex flex-col gap-3">
          <div className={card}>
            <div className="mb-2 text-xs uppercase tracking-wide text-white/50">Published</div>
            {pkgs.length === 0 && <div className="text-sm text-white/40">No packages yet.</div>}
            <div className="flex flex-col gap-1">
              {pkgs.map((p) => (
                <button
                  key={p.name}
                  onClick={() => void openPackage(p.name)}
                  className={`flex items-center justify-between rounded-lg px-2 py-1.5 text-left text-sm hover:bg-white/[0.06] ${
                    selected === p.name ? "bg-white/[0.08]" : ""
                  }`}
                >
                  <span>{p.name}</span>
                  <span className="text-xs text-white/40">
                    {p.kind}
                    {p.tier === "acting" ? " · acting" : ""}
                  </span>
                </button>
              ))}
            </div>
          </div>

          <div className={card}>
            <div className="mb-2 text-xs uppercase tracking-wide text-white/50">Publish manifest</div>
            <textarea
              value={manifest}
              onChange={(e) => setManifest(e.target.value)}
              spellCheck={false}
              className="h-48 w-full resize-y rounded-lg border border-white/10 bg-black/40 p-2 font-mono text-xs"
            />
            <button className={`${btn} mt-2`} disabled={busy} onClick={() => void publish()}>
              Publish
            </button>
          </div>
        </div>

        {/* right: detail + run + allocate */}
        <div className="flex flex-col gap-3">
          {!selected && <div className={`${card} text-sm text-white/40`}>Select a package.</div>}

          {selected && detail && (
            <>
              <div className={card}>
                <div className="flex items-center justify-between">
                  <div className="text-base font-medium">{selected}</div>
                  <div className="text-xs text-white/50">
                    {detail?.package?.spec?.task?.kind} · tier {detail?.tier}
                  </div>
                </div>
                <div className="mt-1 text-sm text-white/60">
                  {detail?.package?.metadata?.description || "—"}
                </div>
                <div className="mt-2 flex flex-wrap gap-2 text-xs text-white/50">
                  {(detail?.package?.spec?.task?.engines ?? []).map((e: string) => (
                    <span key={e} className="rounded-full bg-white/[0.06] px-2 py-0.5">
                      {e}
                    </span>
                  ))}
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button className={btn} disabled={busy} onClick={() => void runOnce(false)}>
                    Run once
                  </button>
                  {detail?.tier === "acting" && (
                    <button
                      className={`${btn} border-amber-500/30`}
                      disabled={busy}
                      onClick={() => void runOnce(true)}
                    >
                      Run (confirm acting)
                    </button>
                  )}
                  <button className={`${btn} border-sky-500/30`} disabled={busy} onClick={() => void runCheck()}>
                    Preflight check
                  </button>
                </div>
              </div>

              {check && (
                <div className={card}>
                  <div className="mb-1 flex items-center justify-between">
                    <span className="text-xs uppercase tracking-wide text-white/50">Preflight</span>
                    <span
                      className={`text-sm font-semibold ${
                        check.status === "pass"
                          ? "text-emerald-300"
                          : check.status === "warn"
                          ? "text-amber-300"
                          : "text-red-300"
                      }`}
                    >
                      {check.status === "pass" ? "✓ pass" : check.status === "warn" ? "⚠ warn" : "✕ fail"}
                    </span>
                  </div>
                  <div className="flex flex-col gap-1">
                    {(check.findings ?? []).map((f: any, i: number) => (
                      <div
                        key={i}
                        className={`text-xs ${
                          f.level === "fail"
                            ? "text-red-300"
                            : f.level === "warn"
                            ? "text-amber-300/90"
                            : "text-white/50"
                        }`}
                      >
                        {f.level === "fail" ? "✕" : f.level === "warn" ? "⚠" : "·"} {f.message}
                      </div>
                    ))}
                  </div>
                  <div className="mt-2 text-xs text-white/40">
                    {check.status === "fail"
                      ? "Sharing is blocked until this passes (or check Force below)."
                      : "Ready to share."}
                  </div>
                </div>
              )}

              {run && (
                <div className={card}>
                  <div className="mb-1 text-xs uppercase tracking-wide text-white/50">Last run</div>
                  <div className={`text-sm font-medium ${statusTone(run.status)}`}>
                    {run.status}
                    {run.country ? ` · ${run.country}` : ""}
                    {run.observationId ? " · stored" : ""}
                  </div>
                  {run.fields && Object.keys(run.fields).length > 0 && (
                    <pre className="mt-2 overflow-x-auto rounded-lg bg-black/40 p-2 text-xs">
                      {JSON.stringify(run.fields, null, 2)}
                    </pre>
                  )}
                  {run.mcpCalls && run.mcpCalls.length > 0 && (
                    <div className="mt-2 text-xs text-white/60">
                      MCP-over-MCP: {run.mcpCalls.map((m) => String(m.name)).join(", ")}
                    </div>
                  )}
                  {run.notes?.map((n, i) => (
                    <div key={i} className="mt-1 text-xs text-amber-300/80">
                      {n}
                    </div>
                  ))}
                </div>
              )}

              <div className={card}>
                <div className="mb-2 text-xs uppercase tracking-wide text-white/50">
                  Allocate to a runner
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <input
                    value={allocDevice}
                    onChange={(e) => setAllocDevice(e.target.value)}
                    placeholder="runner device id"
                    className="flex-1 rounded-lg border border-white/10 bg-black/40 px-2 py-1.5 text-sm"
                  />
                  <select
                    value={allocTarget}
                    onChange={(e) => setAllocTarget(e.target.value)}
                    className="rounded-lg border border-white/10 bg-black/40 px-2 py-1.5 text-sm"
                  >
                    <option value="mobile">mobile</option>
                    <option value="agent">agent</option>
                    <option value="docker">docker</option>
                    <option value="worker">worker</option>
                  </select>
                  <button
                    className={btn}
                    disabled={busy || !shareReady}
                    onClick={() => void allocate()}
                    title={shareReady ? "" : "Run the preflight check first"}
                  >
                    Allocate
                  </button>
                </div>
                <label className="mt-2 flex items-center gap-2 text-xs text-white/50">
                  <input
                    type="checkbox"
                    checked={forceShare}
                    onChange={(e) => setForceShare(e.target.checked)}
                  />
                  Force share without a passing preflight
                </label>
                {!shareReady && (
                  <div className="mt-1 text-xs text-amber-300/70">
                    Run a preflight check before sharing to a runner.
                  </div>
                )}
                <div className="mt-3 flex flex-col gap-1">
                  {(detail?.allocations ?? []).map((a: any) => (
                    <div
                      key={a.allocationId}
                      className="flex items-center justify-between rounded-lg bg-white/[0.04] px-2 py-1.5 text-sm"
                    >
                      <span>
                        {a.runnerDeviceId} · {a.target}
                      </span>
                      <span className="text-xs text-white/50">{a.status}</span>
                    </div>
                  ))}
                  {(detail?.allocations ?? []).length === 0 && (
                    <div className="text-sm text-white/40">No runners yet.</div>
                  )}
                </div>
              </div>

              {(detail?.recentRuns ?? []).length > 0 && (
                <div className={card}>
                  <div className="mb-2 text-xs uppercase tracking-wide text-white/50">
                    Recent runs
                  </div>
                  <div className="flex flex-col gap-1">
                    {(detail.recentRuns ?? []).slice(0, 10).map((r: any) => (
                      <div key={r.runId} className="flex justify-between text-xs text-white/60">
                        <span className={statusTone(r.status)}>{r.status}</span>
                        <span>
                          {r.rowsExtracted} fields {r.country ? `· ${r.country}` : ""}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
