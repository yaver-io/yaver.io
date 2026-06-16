"use client";

import { useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

// WebTestsPanel — run a project's web test suite (yaver-tests/*.test.yaml) via
// the connected agent's chromedp runner and read a feature-based highlight
// report (pass/fail per Feature + clip/reel paths). "Grow" returns the
// self-author plan of uncovered Features for the Yaver runner. The connected
// agent IS the remote PC — pick the machine in the dashboard's device selector.
// Backed by ops verbs project_test_run / project_test_report / project_test_grow.

type Feature = {
  name: string;
  status: "pass" | "fail";
  target?: string;
  url?: string;
  durationMs?: number;
  steps?: number;
  error?: string;
  failStep?: number;
  screenshots?: string[];
  clipPath?: string;
  posterPath?: string;
};
type Report = {
  project?: string;
  total?: number;
  passed?: number;
  failed?: number;
  durationMs?: number;
  features?: Feature[];
  reelPath?: string;
  dir?: string;
};
type Job = { id?: string; state?: string; phase?: string; log?: string[]; error?: string };
type GrowPlan = {
  coveredCount?: number;
  uncovered?: { suggestedName: string; route: string; file: string }[];
  applied?: boolean;
  authorPrompt?: string;
  taskId?: string;
};

export default function WebTestsPanel({ initialDir = "" }: { initialDir?: string }) {
  const [dir, setDir] = useState(initialDir);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [grow, setGrow] = useState<GrowPlan | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const poll = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => () => { if (poll.current) clearInterval(poll.current); }, []);

  const envFor = () => {
    const e: Record<string, string> = {};
    if (token.trim()) e.TALOS_SESSION_TOKEN = token.trim();
    return e;
  };

  const run = async () => {
    setBusy(true); setMsg(null); setReport(null); setGrow(null);
    try {
      const r = await agentClient.callOps("project_test_run", { dir: dir || undefined, env: envFor(), video: true });
      const j = (r.initial as Job) || null;
      if (!j?.id) { setMsg((r as any)?.error || "could not start run"); setBusy(false); return; }
      setJob(j);
      if (poll.current) clearInterval(poll.current);
      poll.current = setInterval(async () => {
        const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
        const sj = (s.initial as Job) || null;
        setJob(sj);
        if (sj?.state === "completed") {
          if (poll.current) clearInterval(poll.current);
          const rep = await agentClient.callOps("project_test_report", { jobId: j.id });
          setReport((rep.initial as Report) || null);
          setBusy(false);
        } else if (sj?.state === "failed") {
          if (poll.current) clearInterval(poll.current);
          setMsg(sj?.error || "run failed"); setBusy(false);
        }
      }, 3000);
    } catch (e: any) { setMsg(String(e?.message || e)); setBusy(false); }
  };

  const doGrow = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("project_test_grow", { dir: dir || undefined, apply: true, author: true });
      if ((r as any)?.error) setMsg((r as any).error);
      else setGrow((r.initial as GrowPlan) || null);
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const installDeps = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("testkit_deps_install", {});
      const j = (r.initial as Job) || null;
      if (!j?.id) { setMsg((r as any)?.error || "could not start install"); setBusy(false); return; }
      setJob(j);
      if (poll.current) clearInterval(poll.current);
      poll.current = setInterval(async () => {
        const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
        const sj = (s.initial as Job) || null;
        setJob(sj);
        if (sj?.state === "completed" || sj?.state === "failed") { if (poll.current) clearInterval(poll.current); setBusy(false); }
      }, 3000);
    } catch (e: any) { setMsg(String(e?.message || e)); setBusy(false); }
  };

  const running = job?.state === "running" || job?.state === "queued";
  const ok = (report?.failed ?? 0) === 0;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">Web Tests — Highlights</h2>
        <p className="text-sm text-neutral-400">
          Run <code>yaver-tests/*.test.yaml</code> on the connected machine via chromedp, recording
          video. Each Feature comes back as a pass/fail highlight. <b>Grow</b> proposes new Features
          the Yaver runner authors automatically.
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="text-sm">
          <span className="text-neutral-400">Project dir (on the runner; empty = its cwd)</span>
          <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={dir} onChange={(e) => setDir(e.target.value)} placeholder="/Users/…/talos" />
        </label>
        <label className="text-sm">
          <span className="text-neutral-400">Session token (optional, for authed pages)</span>
          <input type="password" className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={token} onChange={(e) => setToken(e.target.value)} placeholder="TALOS_SESSION_TOKEN…" />
        </label>
      </div>

      <div className="flex gap-2">
        <button onClick={run} disabled={busy || running} className="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-60">
          {running ? "Running…" : "Run Web Tests"}
        </button>
        <button onClick={doGrow} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
          🌱 Grow Tests
        </button>
        <button onClick={installDeps} disabled={busy || running} title="Install ffmpeg, chromium, node, playwright, redroid once" className="rounded border border-amber-700 bg-amber-950 px-4 py-2 text-sm font-medium text-amber-300 disabled:opacity-60">
          🔧 Install test tools
        </button>
      </div>

      {msg && <div className="rounded bg-red-950 border border-red-800 p-3 text-sm text-red-300">{msg}</div>}

      {job && (
        <div>
          <div className="text-sm font-medium">{job.state === "completed" ? "✓ " : running ? "● " : ""}{job.phase || job.state}</div>
          <pre className="mt-1 max-h-48 overflow-auto rounded bg-neutral-900 border border-neutral-800 p-2 text-xs text-neutral-400">{(job.log || []).slice(-16).join("\n")}</pre>
        </div>
      )}

      {report && (
        <div className="space-y-3">
          <div className="grid grid-cols-3 gap-3">
            <Stat label="Passed" value={report.passed ?? 0} color="#2fbf71" />
            <Stat label="Failed" value={report.failed ?? 0} color="#ff5d5d" />
            <Stat label="Features" value={report.total ?? (report.features?.length ?? 0)} color="#3b82f6" />
          </div>
          <div className={`rounded p-2 text-sm font-medium ${ok ? "text-green-400" : "text-red-400"}`}>
            {ok ? "PASS — all Features green" : `${report.failed} Feature(s) failing`}
            {report.reelPath ? <span className="ml-2 text-neutral-500">🎬 reel: {report.reelPath}</span> : null}
          </div>
          {(report.features || []).map((f, i) => (
            <div key={i} className="rounded bg-neutral-900 p-3 border-l-2" style={{ borderColor: f.status === "pass" ? "#2fbf71" : "#ff5d5d" }}>
              <div className="flex justify-between">
                <span className="font-medium">{f.name}</span>
                <span className="text-xs font-bold" style={{ color: f.status === "pass" ? "#2fbf71" : "#ff5d5d" }}>{f.status.toUpperCase()}</span>
              </div>
              <div className="text-xs text-neutral-500 mt-1">
                {f.target}{f.url ? " · " + f.url : ""} · {Math.round((f.durationMs ?? 0) / 100) / 10}s · {f.steps ?? 0} steps{f.screenshots?.length ? ` · ${f.screenshots.length} shots` : ""}
              </div>
              {f.error && <div className="text-xs text-orange-400 mt-1">step {f.failStep}: {f.error}</div>}
              <WebFeatureMedia feature={f} jobId={job?.id} />
            </div>
          ))}
        </div>
      )}

      {grow && (
        <div className="rounded bg-neutral-900 border border-neutral-800 p-3 space-y-1">
          <div className="font-medium">🌱 Self-grow plan</div>
          <div className="text-xs text-neutral-400">{grow.coveredCount ?? 0} covered · {(grow.uncovered?.length ?? 0)} uncovered route(s){grow.applied ? " · ledger updated" : ""}</div>
          {grow.taskId && <div className="text-xs text-green-400">🤖 runner authoring specs (task {grow.taskId})</div>}
          {(grow.uncovered || []).slice(0, 30).map((u, i) => (
            <div key={i} className="text-xs text-neutral-200">• {u.suggestedName} <span className="text-neutral-500">({u.route})</span></div>
          ))}
          <div className="text-xs text-neutral-500 mt-1">The Yaver runner authors these as new specs — no hand-written YAML.</div>
        </div>
      )}
    </div>
  );
}

// WebFeatureMedia shows a Feature's short success/fail evidence: a tiny poster
// thumbnail (auto) and a tap-to-play highlight clip, both fetched base64 via
// project_test_artifact. Kept lazy/cheap for weak links.
function WebFeatureMedia({ feature, jobId }: { feature: Feature; jobId?: string }) {
  const [poster, setPoster] = useState<string | null>(null);
  const [clip, setClip] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let alive = true;
    const thumb = feature.posterPath || (feature.screenshots && feature.screenshots[feature.screenshots.length - 1]);
    if (thumb && jobId) {
      agentClient.callOps("project_test_artifact", { jobId, path: thumb }).then((r: any) => {
        const a = r?.initial;
        if (alive && a?.base64) setPoster(`data:${a.mimeType || "image/jpeg"};base64,${a.base64}`);
      }).catch(() => {});
    }
    return () => { alive = false; };
  }, [jobId, feature?.name]);

  const playClip = async () => {
    if (!feature.clipPath || !jobId) return;
    setLoading(true);
    try {
      const r: any = await agentClient.callOps("project_test_artifact", { jobId, path: feature.clipPath });
      const a = r?.initial;
      if (a?.base64) setClip(`data:${a.mimeType || "video/mp4"};base64,${a.base64}`);
    } catch { /* keep poster */ }
    setLoading(false);
  };

  if (clip) return <video src={clip} controls autoPlay loop className="mt-2 w-full max-h-64 rounded bg-black" />;
  return (
    <div className="mt-2">
      {poster && <img src={poster} alt="" className="w-full max-h-48 object-cover rounded" />}
      {feature.clipPath && (
        <button onClick={playClip} disabled={loading} className="mt-1 rounded border border-neutral-700 bg-neutral-900 px-3 py-1 text-xs text-neutral-200 disabled:opacity-60">
          {loading ? "Loading…" : "▶ Play highlight"}
        </button>
      )}
    </div>
  );
}

function Stat({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="rounded bg-neutral-900 border border-neutral-800 p-3 text-center">
      <div className="text-xl font-extrabold" style={{ color }}>{value}</div>
      <div className="text-xs text-neutral-500">{label}</div>
    </div>
  );
}
