"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { agentClient, type AutoDevLoop } from "@/lib/agent-client";
import TerminalView from "./TerminalView";

type Tab = "overview" | "autodev" | "agent" | "machines" | "containers" | "terminal" | "catalog" | "images" | "multiregion";

export default function ConsoleView() {
  const [tab, setTab] = useState<Tab>("overview");
  return (
    <div className="space-y-4">
      <div className="flex gap-1 border-b border-surface-800">
        {(["overview", "autodev", "agent", "machines", "containers", "terminal", "catalog", "images", "multiregion"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}>
            {t}
          </button>
        ))}
      </div>
      {tab === "overview" && <Overview />}
      {tab === "autodev" && <AutodevWorkbench />}
      {tab === "agent" && <AgentOrchestrator />}
      {tab === "machines" && <Machines />}
      {tab === "containers" && <Containers />}
      {tab === "terminal" && <TerminalView />}
      {tab === "catalog" && <Catalog />}
      {tab === "images" && <Images />}
      {tab === "multiregion" && <MultiRegion />}
    </div>
  );
}

function AutodevWorkbench() {
  const [loops, setLoops] = useState<AutoDevLoop[]>([]);
  const [items, setItems] = useState<Array<{ line: number; checked: boolean; title: string }>>([]);
  const [workDir, setWorkDir] = useState("");
  const [project, setProject] = useState("");
  const [prompt, setPrompt] = useState("");
  const [runner, setRunner] = useState("");
  const [runners, setRunners] = useState<any[]>([]);
  const [busy, setBusy] = useState("");
  const [picked, setPicked] = useState<number[]>([]);
  const [streamEvents, setStreamEvents] = useState<Array<{ id: string; type?: string; text?: string; runner?: string; tool?: string; detail?: string; status?: string }>>([]);
  const [connected, setConnected] = useState(agentClient.connectionState === "connected");

  const activeLoop = useMemo(
    () =>
      loops.find((loop) => loop.status === "running") ??
      loops.find((loop) => loop.status === "needs_human") ??
      loops[0],
    [loops],
  );

  async function refresh() {
    if (agentClient.connectionState !== "connected") return;
    const [loopRows, runnerRows] = await Promise.all([
      agentClient.autodevLoops().catch(() => []),
      agentClient.getRunners().catch(() => []),
    ]);
    setLoops(loopRows);
    setRunners((runnerRows || []).filter((row: any) => row.installed));
    if (!runner && runnerRows?.length) {
      const pref = runnerRows.find((row: any) => row.isDefault) || runnerRows[0];
      if (pref) setRunner(pref.id);
    }
    if (workDir.trim()) {
      const ideas = await agentClient.autoideasFile(workDir, "ideas.md").catch(() => null);
      if (ideas) setItems(ideas.items || []);
    }
  }

  useEffect(() => {
    const off = agentClient.on("connectionState", (state) => {
      setConnected(state === "connected");
    });
    return off;
  }, []);

  useEffect(() => {
    if (!connected) return;
    refresh();
    const id = setInterval(refresh, 5000);
    return () => clearInterval(id);
  }, [connected, workDir]);

  useEffect(() => {
    if (!activeLoop || !connected) {
      setStreamEvents([]);
      return;
    }
    const abort = agentClient.streamLog(`autodev:${activeLoop.name}`, (ev) => {
      setStreamEvents((prev) => [
        ...prev.slice(-199),
        {
          id: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
          type: ev?.type,
          text: ev?.text,
          runner: ev?.runner,
          tool: ev?.tool,
          detail: ev?.detail,
          status: ev?.status,
        },
      ]);
    });
    return abort;
  }, [activeLoop?.name, connected]);

  const openCount = items.filter((item) => !item.checked).length;

  async function startLoop() {
    if (!workDir.trim()) {
      setBusy("Work dir is required.");
      return;
    }
    setBusy("Starting autodev…");
    const res = await agentClient.autodevStart({
      project: project || undefined,
      workDir,
      prompt: prompt || undefined,
      runner: runner || undefined,
      deploy: "auto",
      hours: "8",
      load: "lite",
    });
    if (!res.ok) {
      setBusy(res.error || "Could not start autodev.");
      return;
    }
    setBusy(`Started ${res.loopName}.`);
    setPrompt("");
    refresh();
  }

  async function generateIdeas() {
    if (!workDir.trim()) {
      setBusy("Set a work dir first.");
      return;
    }
    setBusy("Generating more ideas…");
    const res = await agentClient.autoideasStart({
      work_dir: workDir,
      project: project || undefined,
      output: "ideas.md",
      max_batches: 1,
      tick: 1,
    });
    if (!res.ok) {
      setBusy(res.error || "Could not generate ideas.");
      return;
    }
    setBusy("Idea generation started.");
    setTimeout(() => refresh(), 1500);
  }

  async function implementSelected() {
    if (!workDir.trim() || picked.length === 0) return;
    setBusy(`Starting implementation for ${picked.length} idea(s)…`);
    const res = await agentClient.autoideasSelect({
      work_dir: workDir,
      project: project || undefined,
      output: "ideas.md",
      lines: picked,
      engine: runner || undefined,
    });
    if (!res.ok) {
      setBusy(res.error || "Could not start implementation.");
      return;
    }
    setBusy(`Started ${res.loop_name}.`);
    setPicked([]);
    refresh();
  }

  return (
    <div className="space-y-4" data-testid="autodev-workbench">
      {!connected && (
        <div className="rounded-2xl border border-amber-500/20 bg-amber-500/10 p-4 text-sm text-amber-200">
          Connect to a device first. This view drives the live autodev session on that machine.
        </div>
      )}

      <div className="grid gap-4 xl:grid-cols-[1.1fr,0.9fr]">
        <div className="space-y-4">
          <div className="rounded-2xl border border-surface-800 bg-surface-900/60 p-5">
            <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Machine status</div>
            <div className="mt-2 text-2xl font-semibold text-surface-100" data-testid="autodev-live-title">
              {activeLoop ? `${activeLoop.name} is ${humanLoopStatus(activeLoop.status)}` : "No active autodev loop"}
            </div>
            <div className="mt-2 max-w-2xl text-sm text-surface-400">
              {activeLoop?.lastSummary || "Start a loop or promote a generated idea into implementation. The machine transcript shows up here instead of raw event JSON."}
            </div>
            <div className="mt-4 flex flex-wrap gap-2 text-xs">
              <MetricPill label="working" value={String(loops.filter((loop) => loop.status === "running" || loop.status === "paused").length)} />
              <MetricPill label="total loops" value={String(loops.length)} />
              <MetricPill label="runner" value={activeLoop?.runner || runner || "auto"} />
              <MetricPill label="ideas open" value={String(openCount)} />
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900/60 p-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-surface-100">Live transcript</div>
                <div className="text-xs text-surface-500">The machine talking to Yaver while it edits and tests.</div>
              </div>
              {activeLoop && (
                <button
                  onClick={async () => {
                    await agentClient.autodevStop(activeLoop.name);
                    refresh();
                  }}
                  className="rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs font-semibold text-red-300 hover:bg-red-500/20"
                >
                  Stop active loop
                </button>
              )}
            </div>
            <div className="mt-4 h-[360px] overflow-auto rounded-xl border border-surface-800 bg-surface-950/80 p-3">
              {streamEvents.length === 0 ? (
                <div className="text-sm text-surface-500">No transcript yet.</div>
              ) : (
                <div className="space-y-2">
                  {streamEvents.map((ev) => (
                    <div key={ev.id} className={eventBubbleClass(ev.type)}>
                      <div className="text-[10px] uppercase tracking-wide text-surface-500">
                        {ev.type === "yaver_say" ? "yaver" : ev.runner || ev.type || "event"}
                      </div>
                      <div className="mt-1 text-sm text-surface-200">
                        {ev.text || [ev.tool, ev.detail, ev.status].filter(Boolean).join(" · ")}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900/60 p-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-surface-100">All loops</div>
                <div className="text-xs text-surface-500">Quick status across every active autodev lane on the machine.</div>
              </div>
              <button onClick={refresh} className="rounded-lg border border-surface-700 px-3 py-2 text-xs text-surface-300 hover:bg-surface-800">
                Refresh
              </button>
            </div>
            <div className="mt-3 space-y-2">
              {loops.length === 0 ? (
                <div className="text-sm text-surface-500">No loops yet.</div>
              ) : (
                loops.map((loop) => (
                  <div key={loop.id} className={`rounded-xl border p-3 ${activeLoop?.id === loop.id ? "border-indigo-500/50 bg-indigo-500/10" : "border-surface-800 bg-surface-950/50"}`}>
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <div className="text-sm font-semibold text-surface-100">{loop.name}</div>
                        <div className="text-xs text-surface-500">
                          {loop.mode} · {humanLoopStatus(loop.status)} · iter {loop.iterationCount} · {loop.runner || "auto"}
                        </div>
                      </div>
                      <button
                        onClick={async () => {
                          await agentClient.autodevStop(loop.name);
                          refresh();
                        }}
                        className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-xs text-surface-300 hover:bg-surface-800"
                      >
                        Stop
                      </button>
                    </div>
                    {loop.lastSummary && <div className="mt-2 text-sm text-surface-400">{loop.lastSummary}</div>}
                  </div>
                ))
              )}
            </div>
          </div>
        </div>

        <div className="space-y-4">
          <div className="rounded-2xl border border-surface-800 bg-surface-900/60 p-4">
            <div className="text-sm font-semibold text-surface-100">Start a loop</div>
            <div className="mt-1 text-xs text-surface-500">This should feel like telling the machine what to do, not editing daemon internals.</div>
            <div className="mt-4 space-y-3">
              <input
                value={project}
                onChange={(e) => setProject(e.target.value)}
                placeholder="project name"
                className="w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
              />
              <input
                value={workDir}
                onChange={(e) => setWorkDir(e.target.value)}
                placeholder="/abs/path/to/project"
                className="w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm font-mono text-surface-100"
              />
              <textarea
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="Ship onboarding, fix the rough edges, and keep tests green."
                className="min-h-28 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
              />
              <select
                value={runner}
                onChange={(e) => setRunner(e.target.value)}
                className="w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
              >
                <option value="">auto runner</option>
                {runners.map((row: any) => (
                  <option key={row.id} value={row.id}>{row.name}</option>
                ))}
              </select>
              <button
                onClick={startLoop}
                disabled={!connected}
                className="w-full rounded-xl bg-indigo-500 px-4 py-3 text-sm font-semibold text-white hover:bg-indigo-400 disabled:cursor-not-allowed disabled:opacity-50"
              >
                Start machine session
              </button>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900/60 p-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-surface-100">Auto-generated ideas</div>
                <div className="text-xs text-surface-500">Select one or many and immediately turn them into implementation.</div>
              </div>
              <button
                onClick={generateIdeas}
                disabled={!connected}
                className="rounded-lg border border-surface-700 px-3 py-2 text-xs text-surface-300 hover:bg-surface-800 disabled:opacity-50"
              >
                Generate more
              </button>
            </div>
            <div className="mt-3 flex flex-wrap gap-2 text-xs">
              <MetricPill label="open" value={String(openCount)} />
              <MetricPill label="selected" value={String(picked.length)} />
              <MetricPill label="done" value={String(items.filter((item) => item.checked).length)} />
            </div>
            <div className="mt-4 space-y-2" data-testid="autoideas-list">
              {items.length === 0 ? (
                <div className="text-sm text-surface-500">No ideas loaded yet.</div>
              ) : (
                items.map((item) => {
                  const selected = picked.includes(item.line);
                  return (
                    <button
                      key={item.line}
                      type="button"
                      data-testid={`autoidea-card-${item.line}`}
                      disabled={item.checked}
                      onClick={() => {
                        if (item.checked) return;
                        setPicked((prev) =>
                          prev.includes(item.line)
                            ? prev.filter((line) => line !== item.line)
                            : [...prev, item.line],
                        );
                      }}
                      className={`w-full rounded-xl border p-3 text-left ${item.checked ? "border-surface-800 bg-surface-950/40 opacity-60" : selected ? "border-indigo-500/50 bg-indigo-500/10" : "border-surface-800 bg-surface-950/50 hover:bg-surface-900"}`}
                    >
                      <div className="flex items-start justify-between gap-3">
                        <div className="text-sm font-medium text-surface-100">{item.title}</div>
                        <div className="rounded-full border border-surface-700 px-2 py-1 text-[10px] uppercase tracking-wide text-surface-400">
                          {item.checked ? "done" : selected ? "selected" : "ready"}
                        </div>
                      </div>
                    </button>
                  );
                })
              )}
            </div>
            <button
              onClick={implementSelected}
              disabled={!connected || picked.length === 0}
              data-testid="implement-selected-btn"
              className="mt-4 w-full rounded-xl bg-emerald-500 px-4 py-3 text-sm font-semibold text-white hover:bg-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Implement selected ({picked.length})
            </button>
          </div>

          {busy && (
            <div className="rounded-xl border border-surface-800 bg-surface-900/60 px-4 py-3 text-sm text-surface-300">
              {busy}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function MetricPill({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-full border border-surface-700 bg-surface-950/70 px-3 py-2">
      <div className="text-[10px] uppercase tracking-wide text-surface-500">{label}</div>
      <div className="text-sm font-semibold text-surface-100">{value}</div>
    </div>
  );
}

function humanLoopStatus(status: AutoDevLoop["status"]): string {
  return status.replaceAll("_", " ");
}

function eventBubbleClass(type?: string): string {
  if (type === "yaver_say") return "rounded-2xl border border-cyan-500/30 bg-cyan-500/10 p-3";
  if (type === "runner_action") return "rounded-2xl border border-surface-800 bg-surface-900/80 p-3";
  if (type === "runner_result") return "rounded-2xl border border-emerald-500/20 bg-emerald-500/10 p-3";
  return "rounded-2xl border border-surface-800 bg-surface-950/80 p-3";
}

function AgentOrchestrator() {
  const [runs, setRuns] = useState<any[]>([]);
  const [runners, setRunners] = useState<any[]>([]);
  const [machines, setMachines] = useState<any[]>([]);
  const [name, setName] = useState("");
  const [workDir, setWorkDir] = useState("");
  const [prompt, setPrompt] = useState("");
  const [runner, setRunner] = useState("");
  const [selectedDevices, setSelectedDevices] = useState<string[]>([]);
  const [template, setTemplate] = useState<"full" | "ship">("full");
  const [maxParallel, setMaxParallel] = useState("2");
  const [starting, setStarting] = useState(false);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 4000);
    return () => clearInterval(id);
  }, []);

  async function refresh() {
    const [graphRuns, availableRunners, inventory] = await Promise.all([
      agentClient.agentGraphs(),
      agentClient.getRunners(),
      agentClient.consoleMachines(),
    ]);
    setRuns(graphRuns || []);
    setRunners((availableRunners || []).filter((r: any) => r.installed));
    setMachines((inventory.machines || []).filter((m: any) => m.isOnline));
  }

  async function start() {
    if (!workDir.trim() || !prompt.trim()) {
      alert("work dir and goal are required");
      return;
    }
    setStarting(true);
    const res = await agentClient.createAgentGraph({
      name: name || undefined,
      workDir,
      prompt,
      runner: runner || undefined,
      template,
      maxParallel: Math.max(1, parseInt(maxParallel || "2", 10) || 2),
      preferredDevice: selectedDevices.length === 1 ? selectedDevices[0] : undefined,
      allowedDevices: selectedDevices,
    });
    setStarting(false);
    if (!res.ok) {
      alert(res.error || "could not create graph");
      return;
    }
    setPrompt("");
    refresh();
  }

  return (
    <div className="space-y-4">
      <div className="bg-surface-900/50 border border-surface-800 rounded-xl p-4 space-y-3">
        <div className="text-xs text-surface-500">
          Mesh orchestration can use several machines at once. Pick a pool or leave it on auto and Yaver will spread nodes across capable hosts while serializing Claude/Codex usage when policy says so.
        </div>
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="graph name"
          className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
        <input value={workDir} onChange={(e) => setWorkDir(e.target.value)} placeholder="/abs/path/to/project"
          className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Ship onboarding, keep iOS and Android release paths green, and stay budget-aware."
          className="w-full min-h-28 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
        <div className="grid md:grid-cols-4 gap-2">
          <select value={template} onChange={(e) => setTemplate(e.target.value as "full" | "ship")}
            className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
            <option value="full">full</option>
            <option value="ship">ship</option>
          </select>
          <input value={maxParallel} onChange={(e) => setMaxParallel(e.target.value)} placeholder="max parallel"
            className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
          <select value={runner} onChange={(e) => setRunner(e.target.value)}
            className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
            <option value="">auto runner</option>
            {runners.map((r: any) => <option key={r.id} value={r.id}>{r.name}</option>)}
          </select>
          <div className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
            <div className="text-[11px] uppercase text-surface-500 mb-2">Machine Pool</div>
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => setSelectedDevices([])}
                className={`px-2 py-1 rounded-full border text-xs ${selectedDevices.length === 0 ? "border-indigo-400 bg-indigo-500/20 text-indigo-200" : "border-surface-700 text-surface-300"}`}>
                Auto
              </button>
              {machines.map((m: any) => (
                <button
                  key={m.deviceId}
                  type="button"
                  onClick={() => setSelectedDevices((current) => current.includes(m.deviceId) ? current.filter((id) => id !== m.deviceId) : [...current, m.deviceId])}
                  className={`px-2 py-1 rounded-full border text-xs ${selectedDevices.includes(m.deviceId) ? "border-indigo-400 bg-indigo-500/20 text-indigo-200" : "border-surface-700 text-surface-300"}`}>
                  {m.name}
                </button>
              ))}
            </div>
          </div>
        </div>
        <button onClick={start} disabled={starting}
          className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
          {starting ? "Starting…" : "Start agent graph"}
        </button>
      </div>

      <div className="space-y-3">
        {runs.map((run: any) => (
          <div key={run.id} className="bg-surface-900/50 border border-surface-800 rounded-xl p-4 space-y-3">
            <div className="flex items-start gap-3">
              <div className="flex-1">
                <div className="text-sm font-semibold text-surface-200">{run.name}</div>
                <div className="text-xs text-surface-500">{run.status} · {run.nodes.length} nodes · parallel {run.maxParallel}</div>
              </div>
              {(run.status === "running" || run.status === "queued") && (
                <button onClick={async () => { await agentClient.stopAgentGraph(run.id); refresh(); }}
                  className="px-3 py-1.5 text-xs rounded bg-surface-800 text-surface-200 hover:bg-surface-700">
                  Stop
                </button>
              )}
            </div>
            {run.summary && <div className="text-xs text-surface-500">{run.summary}</div>}
            <div className="space-y-2">
              {run.nodes.map((node: any) => (
                <div key={node.spec.id} className="border border-surface-800 rounded-lg p-3 bg-surface-950/50">
                  <div className="text-sm text-surface-200">{node.spec.title} <span className="text-surface-500">({node.spec.kind})</span></div>
                  <div className="text-xs text-surface-500">
                    {node.status}
                    {node.placement ? ` · ${node.placement.deviceName || node.placement.deviceId}${node.placement.runner ? ` · ${node.placement.runner}` : ""}` : ""}
                  </div>
                  {node.summary && <div className="text-xs text-surface-400 mt-1">{node.summary}</div>}
                  {node.error && <div className="text-xs text-red-400 mt-1">{node.error}</div>}
                  {node.placement?.reason && <div className="text-[11px] text-surface-600 mt-1">{node.placement.reason}</div>}
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function Overview() {
  const [m, setM] = useState<any>(null);
  const [hist, setHist] = useState<number[]>([]);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let cancelled = false;
    void (async () => {
      try {
        const url = await agentClient.metricsWsUrl();
        if (cancelled) return;
        ws = new WebSocket(url);
        ws.onmessage = (e) => {
          const sample = JSON.parse(e.data);
          setM(sample);
          setHist((h) => [...h.slice(-59), sample.cpuPct || 0]);
        };
      } catch {}
    })();
    return () => { cancelled = true; ws?.close(); };
  }, []);

  if (!m) return <div className="text-sm text-surface-500">Connecting to metrics stream…</div>;

  return (
    <div className="space-y-4">
      <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-2">
        <MetricCard label="CPU" value={`${m.cpuPct?.toFixed(1) ?? 0}%`} sub={`${m.cores} cores`} sparkline={hist} />
        <MetricCard label="RAM" value={`${m.ramPct?.toFixed(0) ?? 0}%`} sub={`${fmtBytes(m.ramUsed)} / ${fmtBytes(m.ramTotal)}`} />
        <MetricCard label="Disk" value={`${m.diskPct?.toFixed(0) ?? 0}%`} sub={`${fmtBytes(m.diskUsed)} / ${fmtBytes(m.diskTotal)}`} />
        <MetricCard label="Network" value={`↓ ${fmtBps(m.netRxBps)}`} sub={`↑ ${fmtBps(m.netTxBps)}`} />
      </div>
      <div className="text-xs text-surface-500">
        Host: <span className="font-mono text-surface-300">{m.hostname}</span> · {m.os} · uptime {fmtUptime(m.uptime)}
      </div>
    </div>
  );
}

function MetricCard({ label, value, sub, sparkline }: { label: string; value: string; sub: string; sparkline?: number[] }) {
  return (
    <div className="bg-surface-900/50 border border-surface-800 rounded-lg p-3">
      <div className="text-xs uppercase text-surface-500 font-semibold">{label}</div>
      <div className="text-2xl font-bold text-surface-200 mt-1">{value}</div>
      <div className="text-xs text-surface-500">{sub}</div>
      {sparkline && sparkline.length > 1 && (
        <svg viewBox="0 0 100 20" className="mt-2 w-full h-6">
          <polyline
            points={sparkline.map((v, i) => `${(i / (sparkline.length - 1)) * 100},${20 - (v / 100) * 20}`).join(" ")}
            fill="none" stroke="#818cf8" strokeWidth="0.5" />
        </svg>
      )}
    </div>
  );
}

function Containers() {
  const [list, setList] = useState<any[]>([]);
  const [all, setAll] = useState(false);
  const [error, setError] = useState("");
  const [selectedLogId, setSelectedLogId] = useState<string | null>(null);

  useEffect(() => { refresh(); }, [all]);
  async function refresh() {
    const r = await agentClient.consoleContainers(all);
    setList(r.containers || []);
    setError(r.error || "");
  }
  async function act(id: string, action: string) {
    const r = await agentClient.consoleContainerAction(id, action);
    if (r.error) alert(r.error);
    refresh();
  }
  async function prune() {
    if (!confirm("Prune unused images, containers, and volumes?")) return;
    const r = await agentClient.consolePrune();
    alert(JSON.stringify(r, null, 2));
    refresh();
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <label className="text-xs text-surface-400 flex items-center gap-1">
          <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
          Include stopped
        </label>
        <button onClick={refresh} className="px-3 py-1.5 text-xs rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
        <button onClick={prune} className="px-3 py-1.5 text-xs rounded bg-amber-500/20 text-amber-300 hover:bg-amber-500/30">Prune unused</button>
      </div>
      {error && <div className="text-xs text-red-400 p-2 rounded bg-red-900/20 border border-red-500/30">{error}</div>}
      <div className="overflow-auto border border-surface-800 rounded-lg">
        <table className="w-full text-xs">
          <thead className="bg-surface-900">
            <tr className="text-surface-500 uppercase">
              <th className="text-left p-2">Name</th>
              <th className="text-left p-2">Image</th>
              <th className="text-left p-2">State</th>
              <th className="text-left p-2">Ports</th>
              <th className="text-left p-2">Project</th>
              <th className="text-right p-2">Actions</th>
            </tr>
          </thead>
          <tbody>
            {list.map((c) => (
              <tr key={c.id} className="border-t border-surface-800">
                <td className="p-2 font-mono">{c.name}</td>
                <td className="p-2 font-mono text-surface-400">{c.image}</td>
                <td className="p-2">
                  <span className={`px-1.5 py-0.5 rounded text-[10px] ${c.state === "running" ? "bg-emerald-500/20 text-emerald-300" : "bg-surface-800 text-surface-400"}`}>{c.state}</span>
                </td>
                <td className="p-2 text-surface-400">
                  {(c.ports || []).filter((p: any) => p.public).map((p: any) => `${p.public}→${p.private}`).join(", ") || "—"}
                </td>
                <td className="p-2 text-surface-500">{c.project || "—"}</td>
                <td className="p-2 text-right space-x-1">
                  {c.state === "running" ? (
                    <>
                      <button onClick={() => act(c.id, "restart")} className="text-indigo-400 hover:text-indigo-300">↻</button>
                      <button onClick={() => act(c.id, "stop")} className="text-red-400 hover:text-red-300">⏹</button>
                    </>
                  ) : (
                    <button onClick={() => act(c.id, "start")} className="text-emerald-400 hover:text-emerald-300">▶</button>
                  )}
                  <button onClick={() => setSelectedLogId(c.id)} className="text-surface-400 hover:text-surface-200">logs</button>
                  <button onClick={() => confirm(`Remove ${c.name}?`) && act(c.id, "remove")} className="text-red-400 hover:text-red-300">✕</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {selectedLogId && <LogPanel id={selectedLogId} onClose={() => setSelectedLogId(null)} />}
    </div>
  );
}

function LogPanel({ id, onClose }: { id: string; onClose: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    let ws: WebSocket | null = null;
    let cancelled = false;
    void (async () => {
      try {
        const url = await agentClient.containerLogsWsUrl(id);
        if (cancelled) return;
        ws = new WebSocket(url);
        ws.binaryType = "arraybuffer";
        ws.onmessage = (e) => {
          const text = typeof e.data === "string" ? e.data : new TextDecoder().decode(e.data);
          setLines((ls) => [...ls.slice(-999), ...text.split("\n").filter(Boolean)]);
        };
      } catch {}
    })();
    return () => { cancelled = true; ws?.close(); };
  }, [id]);
  useEffect(() => { ref.current?.scrollTo({ top: ref.current.scrollHeight }); }, [lines]);
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
      <div className="bg-surface-950 border border-surface-700 rounded-xl w-full max-w-3xl max-h-[80vh] flex flex-col">
        <div className="flex items-center gap-2 p-3 border-b border-surface-800">
          <span className="text-sm font-mono flex-1">logs: {id}</span>
          <button onClick={onClose} className="text-xs text-surface-500 hover:text-surface-300">Close</button>
        </div>
        <pre ref={ref} className="flex-1 overflow-auto p-3 text-[10px] font-mono text-surface-300">
          {lines.join("\n")}
        </pre>
      </div>
    </div>
  );
}

function Catalog() {
  const [entries, setEntries] = useState<any[]>([]);
  const [categories, setCategories] = useState<Record<string, any[]>>({});
  const [active, setActive] = useState<any>(null);
  const [fieldValues, setFieldValues] = useState<Record<string, string>>({});
  const [directory, setDirectory] = useState("");
  const [installing, setInstalling] = useState(false);

  useEffect(() => { (async () => {
    const r = await agentClient.consoleCatalog();
    setEntries(r.entries || []);
    setCategories(r.categories || {});
  })(); }, []);

  async function install() {
    if (!active) return;
    setInstalling(true);
    const r = await agentClient.consoleCatalogInstall(active.id, fieldValues, directory || undefined);
    setInstalling(false);
    alert(r.error ? `Error: ${r.error}` : JSON.stringify(r, null, 2));
    if (!r.error) setActive(null);
  }

  return (
    <div className="space-y-4">
      <input value={directory} onChange={(e) => setDirectory(e.target.value)}
        placeholder="project directory (defaults to agent cwd)"
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />

      {Object.entries(categories).map(([cat, list]) => (
        <div key={cat}>
          <h3 className="text-xs uppercase text-indigo-400 font-semibold mb-2">{cat.replace("-", " ")}</h3>
          <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {list.map((e: any) => (
              <button key={e.id} onClick={() => { setActive(e); setFieldValues(Object.fromEntries((e.fields || []).map((f: any) => [f.key, f.default || ""]))); }}
                className="text-left bg-surface-900/50 border border-surface-800 hover:border-indigo-500 rounded-lg p-3 transition">
                <div className="text-sm font-semibold text-surface-200">{e.name}</div>
                <div className="text-xs text-surface-500 line-clamp-2">{e.description}</div>
                <div className="text-[10px] text-surface-600 mt-1 font-mono">{e.image || e.notes}</div>
              </button>
            ))}
          </div>
        </div>
      ))}

      {active && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 max-w-md w-full space-y-3">
            <h3 className="text-sm font-semibold">Install {active.name}</h3>
            <div className="text-xs text-surface-500">{active.description}</div>
            {(active.fields || []).map((f: any) => (
              <div key={f.key}>
                <label className="text-xs text-surface-400">{f.label || f.key}</label>
                <input
                  type={f.secret ? "password" : "text"}
                  value={fieldValues[f.key] || ""}
                  onChange={(e) => setFieldValues({ ...fieldValues, [f.key]: e.target.value })}
                  placeholder={f.default}
                  className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200"
                />
              </div>
            ))}
            <div className="flex gap-2 pt-2">
              <button onClick={install} disabled={installing} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
                {installing ? "Installing…" : "Install & Start"}
              </button>
              <button onClick={() => setActive(null)} className="px-4 py-2 text-sm rounded-lg bg-surface-800 text-surface-200">Cancel</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Images() {
  const [list, setList] = useState<any[]>([]);
  useEffect(() => { (async () => {
    const r = await agentClient.consoleImages();
    setList(r.images || []);
  })(); }, []);
  return (
    <div className="space-y-1">
      {list.map((i) => (
        <div key={i.id} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
          <span className="font-mono text-surface-200 flex-1 truncate">{(i.repoTags || ["(untagged)"]).join(", ")}</span>
          <span className="text-surface-500 font-mono">{fmtBytes(i.size)}</span>
          <span className="text-surface-600 font-mono">{i.id.slice(7, 19)}</span>
        </div>
      ))}
    </div>
  );
}

function MultiRegion() {
  const [name, setName] = useState("");
  const [regions, setRegions] = useState("nbg1,fsn1");
  const [domain, setDomain] = useState("");
  const [gitRepo, setGitRepo] = useState("");
  const [directory, setDirectory] = useState("");
  const [result, setResult] = useState<any>(null);
  const [running, setRunning] = useState(false);

  async function deploy() {
    if (!name || !regions) { alert("name + regions required"); return; }
    const regionList = regions.split(",").map((r) => r.trim()).filter(Boolean);
    if (regionList.length < 2) { alert("need at least 2 regions"); return; }
    if (!confirm(`Provision ${regionList.length} Hetzner VPSes in ${regionList.join(", ")} and bootstrap each? This creates real billable servers.`)) return;
    setRunning(true);
    const r = await agentClient.multiRegionOrchestrate(name, regionList, domain, gitRepo, directory || undefined);
    setRunning(false);
    setResult(r);
  }

  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">
        Provisions N Hetzner VPSes across the chosen regions via the connected Hetzner account,
        SSHes in, installs Docker + Yaver agent, rsyncs your project (or git clones), starts services,
        and writes a Caddy round-robin config on the first healthy server.
      </div>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="deployment name (e.g. myapp-ha)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm" />
      <input value={regions} onChange={(e) => setRegions(e.target.value)} placeholder="regions (comma-separated, e.g. nbg1,fsn1,hel1)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="domain (e.g. myapp.com — Caddy writes round-robin config)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <input value={gitRepo} onChange={(e) => setGitRepo(e.target.value)} placeholder="git clone URL (optional — rsync current project if blank)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <input value={directory} onChange={(e) => setDirectory(e.target.value)} placeholder="project directory (defaults to agent cwd)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <button onClick={deploy} disabled={running} className="px-4 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
        {running ? "Provisioning + bootstrapping…" : "🌍 Deploy multi-region"}
      </button>

      {result?.error && <div className="text-xs text-red-400 p-2 rounded bg-red-900/20 border border-red-500/30">{result.error}</div>}
      {result?.provision?.servers && (
        <div className="space-y-2">
          <h3 className="text-xs uppercase text-surface-500 font-semibold mt-4">Servers</h3>
          {result.provision.servers.map((srv: any, i: number) => (
            <div key={i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
              <div className="flex items-center gap-2">
                <span className="font-mono text-indigo-300">{srv.resource}/{srv.id}</span>
                <span className="text-xs text-surface-500 flex-1">{srv.details?.ipv4}</span>
                <span className="text-xs text-emerald-400">{srv.details?.status || "ok"}</span>
              </div>
              {srv.notes && <div className="text-xs text-surface-500 mt-1">{srv.notes}</div>}
            </div>
          ))}
        </div>
      )}
      {result?.orchestrate?.servers && (
        <div className="space-y-2">
          <h3 className="text-xs uppercase text-surface-500 font-semibold mt-4">Bootstrap</h3>
          {result.orchestrate.servers.map((os: any, i: number) => (
            <div key={i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
              <div className="flex items-center gap-2">
                <span className={`px-1.5 py-0.5 rounded text-[10px] uppercase ${os.status === "ready" ? "bg-emerald-500/20 text-emerald-300" : os.status === "failed" ? "bg-red-500/20 text-red-300" : "bg-amber-500/20 text-amber-300"}`}>{os.status}</span>
                <span className="font-mono flex-1">{os.ip} · {os.region} · {os.role}</span>
              </div>
              <ul className="text-[10px] text-surface-500 mt-2 space-y-0.5">
                {(os.steps || []).map((step: string, j: number) => <li key={j}>· {step}</li>)}
              </ul>
              {os.error && <div className="text-xs text-red-400 mt-1">{os.error}</div>}
            </div>
          ))}
        </div>
      )}
      {result?.orchestrate?.caddyConfig && (
        <div>
          <h3 className="text-xs uppercase text-surface-500 font-semibold mt-4">Caddy round-robin</h3>
          <pre className="bg-surface-900/50 border border-surface-800 rounded p-2 text-[10px] font-mono overflow-auto">{result.orchestrate.caddyConfig}</pre>
        </div>
      )}
    </div>
  );
}

function Machines() {
  const [list, setList] = useState<any[]>([]);
  const [error, setError] = useState("");

  useEffect(() => { refresh(); const i = setInterval(refresh, 10000); return () => clearInterval(i); }, []);

  async function refresh() {
    try {
      const r = await agentClient.consoleMachines();
      setList(r.machines || []);
    } catch (e: any) { setError(e.message); }
  }

  const providerIcon = (p: string) => {
    switch (p) {
      case "hetzner": return "🖥️";
      case "aws": return "☁️";
      case "gcp": return "🌩️";
      case "local-mac": return "🍎";
      case "yaver-cloud": return "⚡";
      default: return "💻";
    }
  };
  const providerColor = (p: string) => {
    switch (p) {
      case "hetzner": return "bg-red-500/20 text-red-300";
      case "aws": return "bg-amber-500/20 text-amber-300";
      case "gcp": return "bg-blue-500/20 text-blue-300";
      case "local-mac":
      case "local": return "bg-emerald-500/20 text-emerald-300";
      case "yaver-cloud": return "bg-indigo-500/20 text-indigo-300";
      default: return "bg-surface-800 text-surface-400";
    }
  };

  return (
    <div className="space-y-3">
      {error && <div className="text-xs text-red-400">{error}</div>}
      <div className="text-xs text-surface-500">
        Hybrid view: own hardware + cloud VPSes managed through one UI. Agent mode can now pin a graph to one machine or leave placement on auto for per-node mesh scheduling.
      </div>
      <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {list.map((m) => (
          <div key={m.deviceId} className={`bg-surface-900/50 border rounded-lg p-3 space-y-2 ${m.isLocal ? "border-indigo-500/40" : "border-surface-800"}`}>
            <div className="flex items-center gap-2">
              <span className="text-xl">{providerIcon(m.provider)}</span>
              <div className="flex-1 min-w-0">
                <div className="text-sm font-semibold text-surface-200 truncate">{m.name}</div>
                <div className="text-[10px] font-mono text-surface-500 truncate">{m.platform}</div>
              </div>
              <span className={`w-2 h-2 rounded-full ${m.isOnline ? "bg-emerald-400" : "bg-red-400"}`} />
            </div>
            <div className="flex flex-wrap gap-1 text-[10px]">
              <span className={`px-1.5 py-0.5 rounded uppercase ${providerColor(m.provider || "unknown")}`}>{m.provider || "unknown"}</span>
              {m.isLocal && <span className="px-1.5 py-0.5 rounded bg-indigo-500/20 text-indigo-300">this machine</span>}
              {m.arch && <span className="px-1.5 py-0.5 rounded bg-surface-800 text-surface-400">{m.arch}</span>}
              {m.cost && <span className="px-1.5 py-0.5 rounded bg-surface-800 text-surface-400">{m.cost}</span>}
            </div>
            {m.capabilities?.runners?.length > 0 && (
              <div className="flex flex-wrap gap-1 text-[10px]">
                {m.capabilities.runners.filter((r: any) => r.ready).slice(0, 4).map((r: any) => (
                  <span key={r.id} className="px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-300">
                    {r.id}
                  </span>
                ))}
                {m.capabilities.supportsTestFlight && <span className="px-1.5 py-0.5 rounded bg-indigo-500/10 text-indigo-300">testflight</span>}
                {m.capabilities.supportsAndroid && <span className="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-300">android</span>}
                {m.capabilities.supportsLocalLlm && <span className="px-1.5 py-0.5 rounded bg-surface-800 text-surface-300">local-llm</span>}
                {m.capabilities.maxTaskSlots ? <span className="px-1.5 py-0.5 rounded bg-surface-800 text-surface-300">slots {m.capabilities.maxTaskSlots}</span> : null}
              </div>
            )}
            {m.capabilities?.profile?.summary && (
              <div className="text-[10px] text-surface-500">{m.capabilities.profile.summary}</div>
            )}
            {m.uptime > 0 && (
              <div className="text-[10px] text-surface-500">uptime: {Math.floor(m.uptime / 86400)}d {Math.floor((m.uptime % 86400) / 3600)}h</div>
            )}
            {m.quicHost && (
              <div className="text-[10px] text-surface-600 font-mono truncate">{m.quicHost}:{m.quicPort}</div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function fmtBytes(n: number | undefined): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(1)} ${units[i]}`;
}

function fmtBps(n: number | undefined): string {
  if (!n) return "0 B/s";
  return fmtBytes(n) + "/s";
}

function fmtUptime(secs: number | undefined): string {
  if (!secs) return "—";
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  return `${d}d ${h}h`;
}
