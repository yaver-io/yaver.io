"use client";

import { type ReactNode, useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

// WebTestsPanel — run a project's web test suite on the connected agent:
// chromedp YAML, Playwright YAML, or native Playwright project tests. The
// connected agent IS the remote PC — pick it in the dashboard device selector.

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
  tracePath?: string;
};
type ArtifactRef = {
  kind: string;
  path: string;
  name?: string;
  mimeType?: string;
  bytes?: number;
  feature?: string;
  step?: number;
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
  artifacts?: ArtifactRef[];
};
type Job = { id?: string; state?: string; phase?: string; log?: string[]; error?: string };
type RunMode = "chromedp" | "playwright-yaml" | "playwright-native";
type QualityReport = {
  passed?: boolean;
  browserJobId?: string;
  qaJobId?: string;
  preflight?: any;
  web?: Report;
  android?: { caught?: number; fixed?: number; passed?: boolean; bugs?: any[]; flows?: any[] };
  summary?: string[];
};
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
  const [mode, setMode] = useState<RunMode>("playwright-yaml");
  const [profile, setProfile] = useState("");
  const [devCommand, setDevCommand] = useState("");
  const [waitURL, setWaitURL] = useState("");
  const [trace, setTrace] = useState(true);
  const [nativeProject, setNativeProject] = useState("");
  const [nativeGrep, setNativeGrep] = useState("");
  const [status, setStatus] = useState<any | null>(null);
  const [profiles, setProfiles] = useState<any[]>([]);
  const [authURL, setAuthURL] = useState("");
  const [authSuccessURL, setAuthSuccessURL] = useState("");
  const [authJob, setAuthJob] = useState<Job | null>(null);
  const [qaPackage, setQAPackage] = useState("");
  const [qaAPK, setQAAPK] = useState("");
  const [qaBase, setQABase] = useState("");
  const [runRedroid, setRunRedroid] = useState(false);
  const [qualityReport, setQualityReport] = useState<QualityReport | null>(null);
  const [runs, setRuns] = useState<any[]>([]);
  const [gcResult, setGCResult] = useState<any | null>(null);
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

  const browserPayload = () => ({
    dir: dir || undefined,
    env: envFor(),
    video: true,
    trace,
    profile: profile.trim() || undefined,
    devCommand: devCommand.trim() || undefined,
    waitURL: waitURL.trim() || undefined,
  });

  const nativePayload = () => ({
    dir: dir || undefined,
    project: nativeProject.trim() || undefined,
    grep: nativeGrep.trim() || undefined,
    trace: trace ? "retain-on-failure" : "off",
    devCommand: devCommand.trim() || undefined,
    waitURL: waitURL.trim() || undefined,
    env: envFor(),
  });

  const run = async () => {
    setBusy(true); setMsg(null); setReport(null); setGrow(null); setQualityReport(null);
    try {
      const verb = mode === "chromedp" ? "project_test_run" : mode === "playwright-native" ? "playwright_native_run" : "playwright_run";
      const r = await agentClient.callOps(verb, mode === "playwright-native" ? nativePayload() : browserPayload());
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

  const checkPlaywright = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("playwright_status", { dir: dir || undefined });
      setStatus(r.initial || r);
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const repairPlaywright = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("playwright_repair", { include: ["node", "playwright", "ffmpeg"] });
      const j = (r.initial as Job) || null;
      if (!j?.id) { setMsg((r as any)?.error || "could not start repair"); setBusy(false); return; }
      setJob(j);
      if (poll.current) clearInterval(poll.current);
      poll.current = setInterval(async () => {
        const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
        const sj = (s.initial as Job) || null;
        setJob(sj);
        if (sj?.state === "completed" || sj?.state === "failed") {
          if (poll.current) clearInterval(poll.current);
          await checkPlaywright();
          setBusy(false);
        }
      }, 3000);
    } catch (e: any) { setMsg(String(e?.message || e)); setBusy(false); }
  };

  const loadProfiles = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("playwright_profiles", {});
      const p = ((r.initial as any)?.profiles || (r as any)?.profiles || []) as any[];
      setProfiles(p);
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const startProfileAuth = async () => {
    if (!authURL.trim()) { setMsg("profile auth URL is required"); return; }
    if (!profile.trim()) { setMsg("profile name is required"); return; }
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("playwright_profile_auth", {
        dir: dir || undefined,
        url: authURL.trim(),
        successURL: authSuccessURL.trim() || undefined,
        profile: profile.trim(),
        timeoutSec: 300,
      });
      const j = (r.initial as Job) || null;
      if (!j?.id) { setMsg((r as any)?.error || "could not start profile auth"); setBusy(false); return; }
      setAuthJob(j);
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const signalProfileAuth = async (signal: "finish" | "cancel") => {
    if (!authJob?.id) return;
    setBusy(true); setMsg(null);
    try {
      await agentClient.callOps(signal === "finish" ? "playwright_profile_auth_finish" : "playwright_profile_auth_cancel", { jobId: authJob.id });
      const s = await agentClient.callOps("studio_job_status", { jobId: authJob.id });
      setAuthJob((s.initial as Job) || authJob);
      if (signal === "finish") await loadProfiles();
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const loadRuns = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("playwright_runs", { limit: 20 });
      setRuns(((r.initial as any)?.runs || []) as any[]);
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const gcRuns = async (dryRun = true) => {
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("playwright_gc", { olderThanHours: 168, dryRun });
      setGCResult(r.initial || r);
      const rr = await agentClient.callOps("playwright_runs", { limit: 20 });
      setRuns(((rr.initial as any)?.runs || []) as any[]);
    } catch (e: any) { setMsg(String(e?.message || e)); }
    setBusy(false);
  };

  const runQuality = async () => {
    setBusy(true); setMsg(null); setReport(null); setGrow(null); setQualityReport(null);
    try {
      const r = await agentClient.callOps("talos_quality_run", {
        browserMode: mode,
        browser: browserPayload(),
        native: nativePayload(),
        runQA: runRedroid,
        qa: {
          package: qaPackage.trim() || undefined,
          apk: qaAPK.trim() || undefined,
          base: qaBase.trim() || undefined,
          mode: "catch",
        },
      });
      const j = (r.initial as Job) || null;
      if (!j?.id) { setMsg((r as any)?.error || "could not start quality run"); setBusy(false); return; }
      setJob(j);
      if (poll.current) clearInterval(poll.current);
      poll.current = setInterval(async () => {
        const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
        const sj = (s.initial as Job) || null;
        setJob(sj);
        if (sj?.state === "completed") {
          if (poll.current) clearInterval(poll.current);
          const rep = await agentClient.callOps("talos_quality_report", { jobId: j.id });
          const qr = (rep.initial as QualityReport) || null;
          setQualityReport(qr);
          if (qr?.web) setReport(qr.web);
          setBusy(false);
        } else if (sj?.state === "failed") {
          if (poll.current) clearInterval(poll.current);
          setMsg(sj?.error || "quality run failed"); setBusy(false);
        }
      }, 3000);
    } catch (e: any) { setMsg(String(e?.message || e)); setBusy(false); }
  };

  const preflightMissingDeps = () => {
    const deps = (qualityReport?.preflight?.deps || []) as any[];
    const missing = deps.filter((d) => d && d.present === false).map((d) => String(d.name || "")).filter(Boolean);
    if (qualityReport?.preflight?.playwright?.ok === false && !missing.includes("playwright")) missing.push("playwright");
    return missing;
  };

  const repairPreflightDeps = async () => {
    const include = preflightMissingDeps();
    if (include.length === 0) { setMsg("No missing preflight dependencies found."); return; }
    setBusy(true); setMsg(null);
    try {
      const r = await agentClient.callOps("testkit_deps_install", { include });
      const j = (r.initial as Job) || null;
      if (!j?.id) { setMsg((r as any)?.error || "could not start dependency repair"); setBusy(false); return; }
      setJob(j);
      if (poll.current) clearInterval(poll.current);
      poll.current = setInterval(async () => {
        const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
        const sj = (s.initial as Job) || null;
        setJob(sj);
        if (sj?.state === "completed" || sj?.state === "failed") {
          if (poll.current) clearInterval(poll.current);
          setBusy(false);
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
          Run <code>yaver-tests/*.test.yaml</code> or native Playwright tests on the connected
          machine. Each completed run returns pass/fail Features and scoped artifacts.
        </p>
      </div>

      <div className="flex flex-wrap gap-2">
        <ModeButton active={mode === "chromedp"} onClick={() => setMode("chromedp")}>YAML chromedp</ModeButton>
        <ModeButton active={mode === "playwright-yaml"} onClick={() => setMode("playwright-yaml")}>YAML Playwright</ModeButton>
        <ModeButton active={mode === "playwright-native"} onClick={() => setMode("playwright-native")}>Native Playwright</ModeButton>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="text-sm">
          <span className="text-neutral-400">Project dir (on the runner; empty = its cwd)</span>
          <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={dir} onChange={(e) => setDir(e.target.value)} placeholder="/Users/.../project" />
        </label>
        <label className="text-sm">
          <span className="text-neutral-400">Session token (optional, for authed pages)</span>
          <input type="password" className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={token} onChange={(e) => setToken(e.target.value)} placeholder="TALOS_SESSION_TOKEN…" />
        </label>
      </div>

      {mode !== "chromedp" && (
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="text-sm">
            <span className="text-neutral-400">Profile</span>
            <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={profile} onChange={(e) => setProfile(e.target.value)} placeholder="qa-admin" />
          </label>
          <label className="text-sm">
            <span className="text-neutral-400">Wait URL</span>
            <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={waitURL} onChange={(e) => setWaitURL(e.target.value)} placeholder="http://127.0.0.1:3000" />
          </label>
          <label className="text-sm sm:col-span-2">
            <span className="text-neutral-400">Dev command</span>
            <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={devCommand} onChange={(e) => setDevCommand(e.target.value)} placeholder="npm run dev" />
          </label>
          {mode === "playwright-native" && (
            <>
              <label className="text-sm">
                <span className="text-neutral-400">Native project</span>
                <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={nativeProject} onChange={(e) => setNativeProject(e.target.value)} placeholder="chromium" />
              </label>
              <label className="text-sm">
                <span className="text-neutral-400">Grep</span>
                <input className="mt-1 w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={nativeGrep} onChange={(e) => setNativeGrep(e.target.value)} placeholder="checkout" />
              </label>
            </>
          )}
          <label className="flex items-center gap-2 text-sm text-neutral-300">
            <input type="checkbox" checked={trace} onChange={(e) => setTrace(e.target.checked)} />
            Capture trace
          </label>
        </div>
      )}

      {mode !== "chromedp" && (
        <div className="rounded bg-neutral-950 border border-neutral-800 p-3 space-y-3">
          <div className="text-sm font-medium">Playwright Profile Auth</div>
          <div className="grid gap-3 sm:grid-cols-2">
            <input className="w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={authURL} onChange={(e) => setAuthURL(e.target.value)} placeholder="Login URL" />
            <input className="w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={authSuccessURL} onChange={(e) => setAuthSuccessURL(e.target.value)} placeholder="Success URL substring" />
          </div>
          <div className="flex flex-wrap gap-2">
            <button onClick={startProfileAuth} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-3 py-2 text-sm text-neutral-200 disabled:opacity-60">Start Auth</button>
            <button onClick={() => signalProfileAuth("finish")} disabled={busy || !authJob?.id} className="rounded border border-green-800 bg-green-950 px-3 py-2 text-sm text-green-300 disabled:opacity-60">Finish Auth</button>
            <button onClick={() => signalProfileAuth("cancel")} disabled={busy || !authJob?.id} className="rounded border border-red-800 bg-red-950 px-3 py-2 text-sm text-red-300 disabled:opacity-60">Cancel Auth</button>
          </div>
          {authJob && <div className="text-xs text-neutral-500">{authJob.id} · {authJob.phase || authJob.state}</div>}
        </div>
      )}

      <div className="rounded bg-neutral-950 border border-neutral-800 p-3 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div className="text-sm font-medium">Full Quality</div>
          <label className="flex items-center gap-2 text-xs text-neutral-300">
            <input type="checkbox" checked={runRedroid} onChange={(e) => setRunRedroid(e.target.checked)} />
            Include Redroid
          </label>
        </div>
        {runRedroid && (
          <div className="grid gap-3 sm:grid-cols-3">
            <input className="w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={qaPackage} onChange={(e) => setQAPackage(e.target.value)} placeholder="Android package" />
            <input className="w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={qaAPK} onChange={(e) => setQAAPK(e.target.value)} placeholder="APK path optional" />
            <input className="w-full rounded bg-neutral-900 border border-neutral-700 p-2 text-sm" value={qaBase} onChange={(e) => setQABase(e.target.value)} placeholder="Warm base optional" />
          </div>
        )}
        <button onClick={runQuality} disabled={busy || running} className="rounded bg-emerald-700 px-4 py-2 text-sm font-medium text-white disabled:opacity-60">
          {running ? "Running…" : "Run Full Quality"}
        </button>
      </div>

      <div className="flex flex-wrap gap-2">
        <button onClick={run} disabled={busy || running} className="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-60">
          {running ? "Running…" : mode === "playwright-native" ? "Run Native Playwright" : mode === "playwright-yaml" ? "Run Playwright YAML" : "Run Web Tests"}
        </button>
        <button onClick={doGrow} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
          🌱 Grow Tests
        </button>
        <button onClick={installDeps} disabled={busy || running} title="Install ffmpeg, chromium, node, playwright, redroid once" className="rounded border border-amber-700 bg-amber-950 px-4 py-2 text-sm font-medium text-amber-300 disabled:opacity-60">
          🔧 Install test tools
        </button>
        {mode !== "chromedp" && (
          <>
            <button onClick={checkPlaywright} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
              Check Playwright
            </button>
            <button onClick={repairPlaywright} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
              Repair Playwright
            </button>
            <button onClick={loadProfiles} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
              Load Profiles
            </button>
            <button onClick={loadRuns} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
              Runs
            </button>
            <button onClick={() => gcRuns(true)} disabled={busy || running} className="rounded border border-neutral-700 bg-neutral-900 px-4 py-2 text-sm font-medium text-neutral-200 disabled:opacity-60">
              GC Dry Run
            </button>
          </>
        )}
      </div>

      {mode !== "chromedp" && (status || profiles.length > 0) && (
        <div className="rounded bg-neutral-900 border border-neutral-800 p-3 text-xs text-neutral-300">
          {status && (
            <div>
              <span className={status.ready ? "text-green-400" : "text-amber-300"}>{status.ready ? "Ready" : "Needs repair"}</span>
              {status.nodeVersion ? <span className="ml-2 text-neutral-500">{status.nodeVersion}</span> : null}
              {status.fixes?.length ? <div className="mt-1 text-neutral-500">{status.fixes.join(" · ")}</div> : null}
            </div>
          )}
          {profiles.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-2">
              {profiles.map((p, i) => (
                <button key={p.name || i} onClick={() => setProfile(p.name)} className="rounded border border-neutral-700 px-2 py-1 text-neutral-200">
                  {p.name}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {msg && <div className="rounded bg-red-950 border border-red-800 p-3 text-sm text-red-300">{msg}</div>}

      {(runs.length > 0 || gcResult) && (
        <div className="rounded bg-neutral-900 border border-neutral-800 p-3 text-xs text-neutral-400">
          {gcResult ? <div className="mb-2">GC: {(gcResult.deleted || []).length ?? 0} deleted candidates</div> : null}
          {runs.slice(0, 10).map((r, i) => <div key={i}>{r.kind || r.source || "run"} · {r.name || r.path}</div>)}
        </div>
      )}

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
          {report.artifacts?.length ? <ArtifactList artifacts={report.artifacts} jobId={qualityReport?.browserJobId || job?.id} playwright={mode !== "chromedp"} /> : null}
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
              {f.tracePath ? <div className="text-xs text-neutral-500 mt-1">trace: {f.tracePath}</div> : null}
              <WebFeatureMedia feature={f} jobId={qualityReport?.browserJobId || job?.id} playwright={mode !== "chromedp"} />
            </div>
          ))}
        </div>
      )}

      {qualityReport && (
        <div className="rounded bg-neutral-900 border border-neutral-800 p-3 space-y-2">
          <div className={`text-sm font-bold ${qualityReport.passed ? "text-green-400" : "text-red-400"}`}>
            {qualityReport.passed ? "FULL QUALITY PASS" : "FULL QUALITY FOUND FAILURES"}
          </div>
          {qualityReport.preflight && (
            <div className={`text-xs ${qualityReport.preflight.ready ? "text-green-400" : "text-amber-300"}`}>
              Preflight: {qualityReport.preflight.ready ? "ready" : "needs attention"}
            </div>
          )}
          {qualityReport.preflight && !qualityReport.preflight.ready && (
            <button onClick={repairPreflightDeps} disabled={busy || running} className="rounded border border-amber-700 bg-amber-950 px-3 py-2 text-xs font-medium text-amber-300 disabled:opacity-60">
              Repair Missing Deps
            </button>
          )}
          {(qualityReport.summary || []).map((s, i) => <div key={i} className="text-xs text-neutral-400">{s}</div>)}
          {qualityReport.android && (
            <div className="text-xs text-neutral-300">
              Redroid: {qualityReport.android.caught ?? 0} caught · {qualityReport.android.fixed ?? 0} fixed · {(qualityReport.android.flows || []).length} flows
            </div>
          )}
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
function WebFeatureMedia({ feature, jobId, playwright }: { feature: Feature; jobId?: string; playwright?: boolean }) {
  const [poster, setPoster] = useState<string | null>(null);
  const [clip, setClip] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let alive = true;
    const thumb = feature.posterPath || (feature.screenshots && feature.screenshots[feature.screenshots.length - 1]);
    if (thumb && jobId) {
      agentClient.callOps(playwright ? "playwright_artifact" : "project_test_artifact", { jobId, path: thumb }).then((r: any) => {
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
      const r: any = await agentClient.callOps(playwright ? "playwright_artifact" : "project_test_artifact", { jobId, path: feature.clipPath });
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

function ArtifactList({ artifacts, jobId, playwright }: { artifacts: ArtifactRef[]; jobId?: string; playwright?: boolean }) {
  const [open, setOpen] = useState<{ uri: string; mime: string; name?: string } | null>(null);
  const [traceInfo, setTraceInfo] = useState<any | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const fetchArtifact = async (a: ArtifactRef) => {
    if (!jobId) return;
    setErr(null);
    try {
      const r: any = await agentClient.callOps(playwright ? "playwright_artifact" : "project_test_artifact", { jobId, path: a.path });
      const x = r?.initial;
      const mime = x?.mimeType || a.mimeType || "application/octet-stream";
      if (x?.base64) setOpen({ uri: `data:${mime};base64,${x.base64}`, mime, name: x?.name || a.name });
    } catch (e: any) { setErr(String(e?.message || e)); }
  };
  const inspectTrace = async (a: ArtifactRef) => {
    if (!jobId) return;
    setErr(null);
    setTraceInfo(null);
    try {
      const r: any = await agentClient.callOps("playwright_trace_inspect", { jobId, path: a.path });
      setTraceInfo(r.initial || r);
    } catch (e: any) { setErr(String(e?.message || e)); }
  };
  return (
    <div className="rounded bg-neutral-900 border border-neutral-800 p-3">
      <div className="text-xs font-medium text-neutral-300">Artifacts</div>
      <div className="mt-2 flex flex-wrap gap-2">
        {artifacts.slice(0, 40).map((a, i) => (
          <span key={`${a.path}-${i}`} className="inline-flex overflow-hidden rounded border border-neutral-700">
            <button onClick={() => fetchArtifact(a)} className="px-2 py-1 text-xs text-neutral-200">
              {a.kind}{a.name ? ` · ${a.name}` : ""}
            </button>
            {a.kind === "trace" && (
              <button onClick={() => inspectTrace(a)} className="border-l border-neutral-700 px-2 py-1 text-xs text-blue-300">
                Inspect
              </button>
            )}
          </span>
        ))}
      </div>
      {err ? <div className="mt-2 text-xs text-red-400">{err}</div> : null}
      {traceInfo ? (
        <div className="mt-3 rounded border border-neutral-800 bg-neutral-950 p-3 text-xs text-neutral-300">
          <div className="font-medium">{traceInfo.name} · {traceInfo.entryCount} entries · {traceInfo.resources} resources · {traceInfo.screenshots} screenshots</div>
          <div className="mt-2 max-h-52 overflow-auto text-neutral-500">
            {(traceInfo.entries || []).map((e: any, i: number) => (
              <div key={i}>{e.name} <span className="text-neutral-600">({e.bytes || 0} bytes)</span></div>
            ))}
          </div>
          {traceInfo.timeline?.length ? (
            <div className="mt-3">
              <div className="font-medium text-neutral-300">Timeline</div>
              <div className="mt-2 max-h-72 overflow-auto border-l border-neutral-800 pl-3">
                {traceInfo.timeline.slice(0, 120).map((e: any, i: number) => (
                  <div key={i} className="mb-2">
                    <div className={e.error ? "text-red-300" : "text-neutral-200"}>
                      {e.apiName || e.method || e.type || "event"}
                      {Number.isFinite(e.duration) && e.duration > 0 ? <span className="ml-2 text-neutral-600">{Math.round(e.duration)}ms</span> : null}
                    </div>
                    {e.params && (
                      <div className="text-neutral-500">
                        {Object.entries(e.params).map(([k, v]) => `${k}: ${v}`).join(" · ")}
                      </div>
                    )}
                    {e.error ? <div className="text-red-400">{e.error}</div> : null}
                  </div>
                ))}
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
      {open && (
        <div className="mt-3">
          {open.mime.startsWith("image/") ? (
            <img src={open.uri} alt="" className="max-h-96 w-full rounded object-contain bg-black" />
          ) : open.mime.startsWith("video/") ? (
            <video src={open.uri} controls className="max-h-96 w-full rounded bg-black" />
          ) : open.mime.includes("html") || open.mime.startsWith("text/") || open.mime.includes("json") ? (
            <iframe src={open.uri} className="h-72 w-full rounded border border-neutral-800 bg-white" />
          ) : (
            <a href={open.uri} download={open.name || "artifact"} className="text-xs text-blue-300 underline">Download {open.name || "artifact"}</a>
          )}
        </div>
      )}
    </div>
  );
}

function ModeButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button onClick={onClick} className={`rounded border px-3 py-2 text-sm ${active ? "border-blue-500 bg-blue-950 text-blue-100" : "border-neutral-700 bg-neutral-900 text-neutral-300"}`}>
      {children}
    </button>
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
