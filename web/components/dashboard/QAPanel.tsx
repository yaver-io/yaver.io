"use client";

import { useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

// QAPanel — the app-test agent on the web. Run the in-repo yaver-tests/flows
// corpus on a redroid surface (cold boot or a warm Yaver Base Image), watch the
// live log, and read the report card: bugs caught, and in fix mode, fixed.
// Everything goes through the connected agent's /ops verbs (qa_run /
// studio_job_status / qa_report) — the same backend the mobile app uses.

type Bug = {
  title: string;
  severity: "low" | "medium" | "high" | "critical";
  oracle: string;
  detail?: string;
  outcome?: "caught" | "fixed" | "attempted-unresolved";
  fixSummary?: string;
};
type FlowResult = { name: string; goal?: string; steps?: number; bugs?: number };
type Report = { mode?: string; flows?: FlowResult[]; bugs?: Bug[]; caught?: number; fixed?: number; passed?: boolean };
type Job = { id?: string; state?: string; phase?: string; log?: string[]; error?: string };

const SEV: Record<string, string> = { critical: "#ff5d5d", high: "#ff9f43", medium: "#ffd166", low: "#9aa7b4" };
const OUTCOME: Record<string, { label: string; color: string }> = {
  fixed: { label: "FIXED", color: "#2fbf71" },
  "attempted-unresolved": { label: "ATTEMPTED", color: "#ff9f43" },
  caught: { label: "CAUGHT", color: "#ff5d5d" },
};

export default function QAPanel() {
  const [pkg, setPkg] = useState("io.yaver.mobile");
  const [base, setBase] = useState("");
  const [mode, setMode] = useState<"catch" | "fix">("catch");
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const poll = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => () => { if (poll.current) clearInterval(poll.current); }, []);

  const run = async () => {
    setBusy(true);
    setMsg(null);
    setReport(null);
    try {
      const r = await agentClient.callOps("qa_run", { package: pkg, base: base || undefined, mode });
      const j = (r.initial as Job) || null;
      if (!j?.id) {
        setMsg((r as any)?.error || "could not start run");
        setBusy(false);
        return;
      }
      setJob(j);
      if (poll.current) clearInterval(poll.current);
      poll.current = setInterval(async () => {
        const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
        const sj = (s.initial as Job) || null;
        setJob(sj);
        if (sj?.state === "completed") {
          if (poll.current) clearInterval(poll.current);
          const rep = await agentClient.callOps("qa_report", { jobId: j.id });
          setReport((rep.initial as Report) || null);
          setBusy(false);
        } else if (sj?.state === "failed") {
          if (poll.current) clearInterval(poll.current);
          setMsg(sj?.error || "run failed");
          setBusy(false);
        }
      }, 3000);
    } catch (e: any) {
      setMsg(String(e?.message || e));
      setBusy(false);
    }
  };

  const running = job?.state === "running" || job?.state === "queued";

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">App-Test Agent</h2>
        <p className="text-sm text-neutral-400">
          Drive your app through the <code>yaver-tests/flows</code> corpus on a redroid surface. The agent
          explores toward each goal; the oracle bank watches for red boxes, crashes, ANRs and blank screens.
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <label className="text-sm">
          <span className="text-neutral-400">App package</span>
          <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={pkg} onChange={(e) => setPkg(e.target.value)} placeholder="io.yaver.mobile" />
        </label>
        <label className="text-sm">
          <span className="text-neutral-400">Warm base (optional)</span>
          <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={base} onChange={(e) => setBase(e.target.value)} placeholder="2026-06-09-1 (empty = cold boot)" />
        </label>
        <div className="text-sm">
          <span className="text-neutral-400">Mode</span>
          <div className="mt-1 flex gap-2">
            {(["catch", "fix"] as const).map((m) => (
              <button key={m} onClick={() => setMode(m)} className={`rounded px-3 py-2 text-sm border ${m === mode ? "bg-blue-600 border-blue-500 text-white" : "bg-neutral-900 border-neutral-700"}`}>
                {m === "catch" ? "Catch-only" : "Fix (draft)"}
              </button>
            ))}
          </div>
        </div>
      </div>

      <button onClick={run} disabled={busy || running} className="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-60">
        {running ? "Running…" : "Run app test"}
      </button>

      {msg && <div className="rounded bg-red-950 border border-red-800 p-3 text-sm text-red-300">{msg}</div>}

      {job && (
        <div>
          <div className="text-sm font-medium">
            {job.state === "completed" ? "✓ " : running ? "● " : ""}
            {job.phase || job.state}
          </div>
          <pre className="mt-1 max-h-48 overflow-auto rounded bg-neutral-900 border border-neutral-800 p-2 text-xs text-neutral-400">
            {(job.log || []).slice(-16).join("\n")}
          </pre>
        </div>
      )}

      {report && (
        <div className="space-y-3">
          <div className="grid grid-cols-3 gap-3">
            <Stat label="Caught" value={report.caught ?? 0} color="#ff9f43" />
            <Stat label="Fixed" value={report.fixed ?? 0} color="#2fbf71" />
            <Stat label="Flows" value={report.flows?.length ?? 0} color="#3b82f6" />
          </div>
          <div className={`text-sm font-bold ${report.passed ? "text-green-400" : "text-red-400"}`}>
            {report.passed ? "PASS — no unresolved bugs" : `${(report.bugs || []).filter((b) => b.outcome !== "fixed").length} unresolved bug(s)`}
          </div>
          {(report.bugs || []).map((b, i) => {
            const badge = OUTCOME[b.outcome || "caught"];
            return (
              <div key={i} className="rounded bg-neutral-900 p-3 border-l-2" style={{ borderLeftColor: SEV[b.severity] || "#888" }}>
                <div className="flex items-center justify-between">
                  <span className="font-medium">{b.title}</span>
                  <span className="text-xs font-bold" style={{ color: badge.color }}>{badge.label}</span>
                </div>
                <div className="text-xs text-neutral-500 mt-1">{b.oracle} · {b.severity}</div>
                {b.detail && <div className="text-xs text-neutral-400 mt-1">{b.detail}</div>}
                {b.fixSummary && <div className="text-xs text-green-400 mt-1">🔧 {b.fixSummary}</div>}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Stat({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="rounded bg-neutral-900 border border-neutral-800 p-3 text-center">
      <div className="text-2xl font-extrabold" style={{ color }}>{value}</div>
      <div className="text-xs text-neutral-400">{label}</div>
    </div>
  );
}
