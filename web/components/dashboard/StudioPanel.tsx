"use client";

import { useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

// StudioPanel — Store Studio on the web. An agentic, status-aware panel for
// app-compliance assets: generate the Play Console permission-justification
// prose (offline) and RECORD the demo video on a redroid surface (managed-cloud
// or on-prem), with live job status the user watches. Everything goes through
// the connected agent's /ops verbs (studio_permission_prose / studio_job_start /
// studio_job_status) — same backend the mobile app uses.

const COMMON_PERMS = [
  "FOREGROUND_SERVICE_SPECIAL_USE",
  "FOREGROUND_SERVICE_DATA_SYNC",
  "FOREGROUND_SERVICE_LOCATION",
  "FOREGROUND_SERVICE_MEDIA_PLAYBACK",
];

type Prose = {
  taskOther?: string;
  description?: string;
  shotList?: string[];
  warnings?: string[];
  service?: string;
  fgsType?: string;
  subtype?: string;
  trigger?: string;
};

type Job = {
  id?: string;
  state?: string;
  phase?: string;
  log?: string[];
  error?: string;
  durationSec?: number;
  artifacts?: { mp4?: string; captionedMp4?: string; justification?: string; captionCount?: number; dir?: string };
};

export default function StudioPanel() {
  const [permission, setPermission] = useState(COMMON_PERMS[0]);
  const [path, setPath] = useState("");
  const [app, setApp] = useState("");
  const [what, setWhat] = useState("");
  const [busy, setBusy] = useState(false);
  const [prose, setProse] = useState<Prose | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  // record mode
  const [apk, setApk] = useState("");
  const [hostWorkDir, setHostWorkDir] = useState("");
  const [sshHost, setSshHost] = useState("");
  const [startAction, setStartAction] = useState("");
  const [job, setJob] = useState<Job | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, []);

  const genProse = async () => {
    if (!agentClient.isConnected) {
      setMsg("Connect to an agent that has your app's repo first.");
      return;
    }
    setBusy(true);
    setMsg(null);
    setProse(null);
    try {
      const r = await agentClient.callOps("studio_permission_prose", {
        permission,
        path: path.trim() || undefined,
        app: app.trim() || undefined,
        what: what.trim() || undefined,
      });
      setProse((r.initial as Prose) || null);
    } catch (e: any) {
      setMsg(e?.message || "Failed");
    } finally {
      setBusy(false);
    }
  };

  const startRecord = async () => {
    if (!apk.trim() || !hostWorkDir.trim()) {
      setMsg("Recording needs the APK path and a host work dir on the device.");
      return;
    }
    setMsg(null);
    setJob(null);
    if (pollRef.current) clearInterval(pollRef.current);
    try {
      const r = await agentClient.callOps("studio_job_start", {
        permission,
        apk: apk.trim(),
        hostWorkDir: hostWorkDir.trim(),
        path: path.trim() || undefined,
        startAction: startAction.trim() || undefined,
        sshHost: sshHost.trim() || undefined,
        app: app.trim() || undefined,
        what: what.trim() || undefined,
      });
      const j = (r.initial as Job) || null;
      setJob(j);
      if (j?.id) {
        pollRef.current = setInterval(async () => {
          try {
            const s = await agentClient.callOps("studio_job_status", { jobId: j.id });
            const sj = (s.initial as Job) || null;
            if (sj) setJob(sj);
            if (sj?.state === "completed" || sj?.state === "failed") {
              if (pollRef.current) clearInterval(pollRef.current);
            }
          } catch {
            /* keep polling */
          }
        }, 3000);
      }
    } catch (e: any) {
      setMsg(e?.message || "Failed to start");
    }
  };

  const recording = !!job && job.state !== "completed" && job.state !== "failed";
  const inp = "w-full rounded-md border border-neutral-700 bg-neutral-900 px-3 py-2 text-sm text-neutral-100";
  const lbl = "mt-3 text-xs text-neutral-400";

  return (
    <div className="rounded-xl border border-neutral-800 bg-neutral-950 p-5">
      <h3 className="text-lg font-semibold text-neutral-100">🎬 Store Studio</h3>
      <p className="mt-1 text-sm text-neutral-400">
        App Store / Play assets for your app. Generate a permission-justification (prose + shot-list) and record the demo video on a redroid surface — managed-cloud or your own box.
      </p>

      <div className={lbl}>Permission</div>
      <div className="mt-1 flex flex-wrap gap-2">
        {COMMON_PERMS.map((p) => (
          <button
            key={p}
            onClick={() => setPermission(p)}
            className={`rounded-md border px-2 py-1 text-xs ${p === permission ? "border-blue-500 bg-blue-600 text-white" : "border-neutral-700 bg-neutral-900 text-neutral-200"}`}
          >
            {p.replace("FOREGROUND_SERVICE_", "FGS_")}
          </button>
        ))}
      </div>
      <input className={`${inp} mt-2`} value={permission} onChange={(e) => setPermission(e.target.value)} />

      <div className={lbl}>Project path on the device (optional)</div>
      <input className={inp} value={path} onChange={(e) => setPath(e.target.value)} placeholder="/home/you/myapp" />
      <div className={lbl}>App name / what the service does (optional)</div>
      <div className="flex gap-2">
        <input className={inp} value={app} onChange={(e) => setApp(e.target.value)} placeholder="My App" />
        <input className={inp} value={what} onChange={(e) => setWhat(e.target.value)} placeholder="an on-device sync engine the user starts" />
      </div>

      <button
        onClick={genProse}
        disabled={busy}
        className="mt-4 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
      >
        {busy ? "Generating…" : "Generate justification"}
      </button>

      {msg && <div className="mt-3 rounded-md bg-red-950 p-3 text-sm text-red-300">{msg}</div>}

      {prose && (
        <div className="mt-4 space-y-2 text-sm">
          {prose.service && (
            <div className="text-xs text-neutral-500">
              service: {prose.service} · type: {prose.fgsType}{prose.subtype ? ` · ${prose.subtype}` : ""}{prose.trigger ? ` · trigger: ${prose.trigger}` : ""}
            </div>
          )}
          {prose.warnings && prose.warnings.length > 0 && (
            <div className="rounded-md bg-amber-950 p-3 text-amber-300">
              {prose.warnings.map((w, i) => <div key={i}>⚠ {w}</div>)}
            </div>
          )}
          <div className={lbl}>&quot;What tasks&quot; → Other</div>
          <p className="text-neutral-100">{prose.taskOther}</p>
          <div className={lbl}>Describe your app&apos;s use</div>
          <p className="whitespace-pre-wrap text-neutral-100">{prose.description}</p>
          <div className={lbl}>Demo video shot-list</div>
          {(prose.shotList || []).map((s, i) => <div key={i} className="text-neutral-200">• {s}</div>)}
        </div>
      )}

      <div className="mt-6 border-t border-neutral-800 pt-4">
        <h4 className="text-sm font-semibold text-neutral-100">Record the demo video</h4>
        <p className="text-xs text-neutral-500">Leave SSH host empty for a Yaver-managed-cloud box (agent runs there); set it for an on-prem box.</p>
        <div className={lbl}>APK path (built for the surface arch)</div>
        <input className={inp} value={apk} onChange={(e) => setApk(e.target.value)} placeholder="/home/you/app-x86_64.apk" />
        <div className={lbl}>Host work dir (redroid /data mount)</div>
        <input className={inp} value={hostWorkDir} onChange={(e) => setHostWorkDir(e.target.value)} placeholder="/home/you/redroid-data" />
        <div className="flex gap-2">
          <div className="flex-1">
            <div className={lbl}>SSH host (on-prem only)</div>
            <input className={inp} value={sshHost} onChange={(e) => setSshHost(e.target.value)} placeholder="user@10.0.0.45" />
          </div>
          <div className="flex-1">
            <div className={lbl}>FGS start action</div>
            <input className={inp} value={startAction} onChange={(e) => setStartAction(e.target.value)} placeholder="io.yaver.mobile.sandbox.START" />
          </div>
        </div>
        <button
          onClick={startRecord}
          disabled={recording}
          className="mt-4 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
        >
          {recording ? "Recording…" : "Record demo video"}
        </button>

        {job && (
          <div className="mt-4 rounded-lg border border-neutral-800 bg-neutral-900 p-3">
            <div className="flex items-center gap-2 text-sm font-medium text-neutral-100">
              {recording && <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />}
              <span>
                {job.state === "completed" ? "✓ Done" : job.state === "failed" ? "✗ Failed" : `${job.phase || job.state || "starting"}…`}
              </span>
              {typeof job.durationSec === "number" && <span className="text-xs text-neutral-500">{job.durationSec}s</span>}
            </div>
            {job.error && <div className="mt-1 text-xs text-red-400">{job.error}</div>}
            <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap text-[11px] text-neutral-500">
              {(job.log || []).slice(-12).join("\n")}
            </pre>
            {job.state === "completed" && job.artifacts && (
              <div className="mt-2 text-xs text-blue-400">
                {job.artifacts.captionedMp4 || job.artifacts.mp4}
                <span className="text-neutral-500"> · saved on the device{job.artifacts.captionCount ? ` · ${job.artifacts.captionCount} captions` : ""}</span>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
