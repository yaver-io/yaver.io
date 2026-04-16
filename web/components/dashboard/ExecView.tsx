"use client";

// ExecView — UI over /exec on the agent. Runs arbitrary shell
// commands inside the ExecManager (desktop/agent/exec.go). Output is
// streamed back to this view by polling /exec/{id}; nothing is sent
// to Convex.

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClient, type ExecSnapshot } from "@/lib/agent-client";

function statusColor(s: ExecSnapshot["status"]): string {
  if (s === "running") return "bg-amber-900/40 text-amber-200";
  if (s === "completed") return "bg-emerald-900/40 text-emerald-200";
  return "bg-red-900/40 text-red-200";
}

export default function ExecView() {
  const [execs, setExecs] = useState<ExecSnapshot[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selected, setSelected] = useState<ExecSnapshot | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [command, setCommand] = useState("");
  const [workDir, setWorkDir] = useState("");
  const [starting, setStarting] = useState(false);
  const outputRef = useRef<HTMLPreElement>(null);

  const loadList = useCallback(async () => {
    try {
      const list = await agentClient.listExecs();
      list.sort((a, b) => b.startedAt.localeCompare(a.startedAt));
      setExecs(list);
      if (!selectedId && list.length > 0) setSelectedId(list[0].id);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, [selectedId]);

  useEffect(() => {
    void loadList();
    const iv = window.setInterval(loadList, 4_000);
    return () => window.clearInterval(iv);
  }, [loadList]);

  useEffect(() => {
    if (!selectedId) {
      setSelected(null);
      return;
    }
    let cancelled = false;
    const poll = async () => {
      try {
        const snap = await agentClient.getExec(selectedId);
        if (!cancelled) setSelected(snap);
      } catch {
        // transient, keep polling
      }
    };
    void poll();
    const iv = window.setInterval(poll, 500);
    return () => {
      cancelled = true;
      window.clearInterval(iv);
    };
  }, [selectedId]);

  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [selected?.stdout, selected?.stderr]);

  async function run() {
    if (!command.trim() || starting) return;
    setStarting(true);
    try {
      const res = await agentClient.startExec({
        command: command.trim(),
        workDir: workDir.trim() || undefined,
      });
      setCommand("");
      setSelectedId(res.execId);
      await loadList();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setStarting(false);
    }
  }

  async function kill(id: string) {
    try {
      await agentClient.killExec(id);
      await loadList();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="flex h-full flex-col gap-3 overflow-hidden p-4 text-surface-100">
      <header>
        <h2 className="text-lg font-semibold">Exec</h2>
        <p className="text-xs text-surface-400">
          Run shell commands on your own machine through the agent. Output streams here over P2P — nothing is sent to Convex.
        </p>
      </header>

      {err && (
        <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200" role="alert">
          {err}
        </div>
      )}

      <section className="rounded border border-surface-700 bg-surface-950/30 p-3">
        <div className="flex flex-col gap-2 md:flex-row">
          <input
            className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-sm"
            placeholder="command (e.g. git status)"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !starting) void run();
            }}
          />
          <input
            className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-sm md:w-72"
            placeholder="workDir (optional)"
            value={workDir}
            onChange={(e) => setWorkDir(e.target.value)}
          />
          <button
            type="button"
            className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-semibold disabled:opacity-40"
            disabled={!command.trim() || starting}
            onClick={() => void run()}
          >
            {starting ? "Starting…" : "Run"}
          </button>
        </div>
      </section>

      <div className="grid min-h-0 flex-1 gap-3 md:grid-cols-[minmax(0,280px)_minmax(0,1fr)]">
        <ul className="min-h-0 overflow-auto rounded border border-surface-700 text-sm">
          {execs.length === 0 && <li className="px-3 py-2 text-surface-500">No execs.</li>}
          {execs.map((e) => (
            <li key={e.id}>
              <button
                type="button"
                className={`flex w-full flex-col items-start gap-1 px-3 py-2 text-left hover:bg-surface-800 ${
                  selectedId === e.id ? "bg-surface-800" : ""
                }`}
                onClick={() => setSelectedId(e.id)}
              >
                <div className="flex w-full items-center gap-2">
                  <span className={`rounded px-1.5 py-0.5 text-[10px] ${statusColor(e.status)}`}>{e.status}</span>
                  {typeof e.exitCode === "number" && (
                    <span className="text-[10px] text-surface-500">exit {e.exitCode}</span>
                  )}
                  <span className="ml-auto text-[10px] text-surface-500">{e.startedAt.slice(11, 19)}</span>
                </div>
                <span className="truncate font-mono text-xs text-surface-300">{e.command}</span>
              </button>
            </li>
          ))}
        </ul>
        <div className="flex min-h-0 flex-col gap-2">
          {selected ? (
            <>
              <div className="flex items-center gap-2 text-xs">
                <code className="truncate font-mono text-surface-300">{selected.command}</code>
                <span className={`rounded px-1.5 py-0.5 text-[10px] ${statusColor(selected.status)}`}>{selected.status}</span>
                {selected.status === "running" && (
                  <button
                    type="button"
                    className="ml-auto rounded bg-red-900/40 px-2 py-0.5 text-red-200 hover:bg-red-900/70"
                    onClick={() => void kill(selected.id)}
                  >
                    Kill
                  </button>
                )}
              </div>
              <pre
                ref={outputRef}
                className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap rounded border border-surface-700 bg-black/40 p-3 font-mono text-xs"
              >
                {selected.stdout || ""}
                {selected.stderr ? `\n${selected.stderr}` : ""}
                {selected.status !== "running" && typeof selected.exitCode === "number"
                  ? `\n[exit ${selected.exitCode}]`
                  : ""}
              </pre>
            </>
          ) : (
            <div className="flex min-h-0 flex-1 items-center justify-center rounded border border-surface-700 bg-surface-950/30 text-sm text-surface-500">
              Select or start an exec to see output.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
