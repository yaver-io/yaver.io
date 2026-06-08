"use client";

import { useEffect, useState } from "react";
import { agentClient, type CapabilitySnapshot, type CapabilityTargetReadiness } from "@/lib/agent-client";
import { EmptyState } from "@/components/ui";

interface Build { id: string; platform: string; status: string; startedAt?: number; artifactName?: string; }
interface Project { name: string; path: string; framework?: string; }
interface PublishTarget { id: string; label?: string; kind: string; }
interface PublishConfigResponse {
  config?: {
    defaultTarget?: string;
    fallback?: { githubAllowed?: boolean };
    targets?: PublishTarget[];
  };
  exists: boolean;
  path: string;
}
interface PublishRun {
  id: string;
  targetId: string;
  status: string;
  provider: string;
  startedAt?: string;
  message?: string;
}
interface UnityRun {
  ok: boolean;
  status?: string;
  stage?: string;
  projectPath?: string;
  mode?: string;
  buildTarget?: string;
  executeMethod?: string;
  outputPath?: string;
  executablePath?: string;
  logPath?: string;
  resultsPath?: string;
  summary?: string;
  artifacts?: string[];
  nextAction?: string;
  command?: string[];
}

export default function BuildsView({
  onTaskCreated,
  preferredProjectPath,
}: {
  onTaskCreated?: (taskId: string) => void;
  preferredProjectPath?: string | null;
}) {
  const [builds, setBuilds] = useState<Build[]>([]);
  const [loading, setLoading] = useState(true);
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedPath, setSelectedPath] = useState("");
  const [publishConfig, setPublishConfig] = useState<PublishConfigResponse | null>(null);
  const [publishRuns, setPublishRuns] = useState<PublishRun[]>([]);
  const [unityRuns, setUnityRuns] = useState<UnityRun[]>([]);
  const [allowGitHubFallback, setAllowGitHubFallback] = useState(false);
  const [publishBusy, setPublishBusy] = useState<string | null>(null);
  const [capabilitySnapshot, setCapabilitySnapshot] = useState<CapabilitySnapshot | null>(null);
  const [publishMessage, setPublishMessage] = useState<string | null>(null);

  const noProjects = projects.length === 0;

  useEffect(() => { void loadBuilds(); void loadProjects(); void loadPublishes(); void loadUnityRuns(); void loadCapabilities(); }, []);
  useEffect(() => {
    if (!selectedPath) return;
    void loadPublishConfig(selectedPath);
  }, [selectedPath]);
  useEffect(() => {
    if (!preferredProjectPath) return;
    if (!projects.some((project) => project.path === preferredProjectPath)) return;
    setSelectedPath(preferredProjectPath);
  }, [preferredProjectPath, projects]);

  async function loadBuilds() {
    setLoading(true);
    try { setBuilds(await agentClient.listBuilds()); } catch {}
    setLoading(false);
  }

  async function loadProjects() {
    try {
      const out = await agentClient.listProjects();
      setProjects(out);
      if (!selectedPath && out[0]?.path) setSelectedPath(out[0].path);
    } catch {}
  }

  async function loadPublishConfig(dir: string) {
    try {
      const out = await agentClient.getPublishConfig(dir) as PublishConfigResponse;
      setPublishConfig(out);
      setAllowGitHubFallback(Boolean(out.config?.fallback?.githubAllowed));
    } catch {
      setPublishConfig(null);
    }
  }

  async function loadPublishes() {
    try {
      setPublishRuns(await agentClient.listPublishes() as PublishRun[]);
    } catch {}
  }

  async function loadUnityRuns() {
    try {
      setUnityRuns(await agentClient.listUnityRuns() as UnityRun[]);
    } catch {}
  }

  async function loadCapabilities() {
    try {
      setCapabilitySnapshot(await agentClient.capabilitySnapshot());
    } catch {
      setCapabilitySnapshot(null);
    }
  }

  async function deploy(target: "testflight" | "playstore" | "web") {
    let proj = projects.find((p) => p.path === selectedPath) ?? projects[0];
    if (!proj) return; // buttons are disabled in this state; nothing to do

    const prompts: Record<string, string> = {
      testflight: `cd ${proj.path} && Build ${proj.name} for iOS and deploy to TestFlight. Archive with xcodebuild, export, upload. Show build number when done.`,
      playstore: `cd ${proj.path} && Build ${proj.name} for Android (release AAB) and upload to Google Play internal testing. Bump versionCode, build, upload.`,
      web: `cd ${proj.path} && Deploy ${proj.name} to Vercel or configured hosting. Build and deploy. Show URL when done.`,
    };

    try {
      const task = await agentClient.sendTask(`Deploy ${proj.name} to ${target}`, prompts[target]);
      onTaskCreated?.(task.id);
    } catch {}
  }

  function targetReadiness(target: "testflight" | "playstore" | "web-preview"): CapabilityTargetReadiness | null {
    return capabilitySnapshot?.targets?.[target] ?? null;
  }

  function deployDisabledReason(target: "testflight" | "playstore" | "web") {
    if (noProjects) return "No projects detected on the connected machine yet.";
    if (target === "web") return "";
    const readiness = targetReadiness(target);
    if (!readiness) return "";
    if (readiness.enabled) return "";
    return readiness.reason || readiness.suggestedAction || "This target is not ready on the connected machine.";
  }

  function deployButtonClass(disabled: boolean, primary = false) {
    if (disabled) {
      return "px-3 py-2 text-sm rounded-lg border border-surface-800 bg-surface-950 text-surface-500 cursor-not-allowed opacity-70 flex items-center gap-2";
    }
    if (primary) {
      return "px-3 py-2 text-sm font-medium rounded-lg bg-brand text-brand-fg hover:bg-brand/90 active:scale-[0.97] transition-all flex items-center gap-2";
    }
    return "px-3 py-2 text-sm rounded-lg border border-surface-700 bg-surface-900 hover:bg-surface-800 text-surface-200 transition-colors flex items-center gap-2";
  }

  async function runPublish(targetId: string) {
    if (!selectedPath) return;
    setPublishBusy(targetId);
    setPublishMessage(null);
    try {
      await agentClient.startPublish(selectedPath, targetId, allowGitHubFallback);
      await loadPublishes();
      await loadBuilds();
    } catch (error) {
      const raw = error instanceof Error ? error.message : "";
      setPublishMessage(
        raw.trim() && raw.trim().length <= 160
          ? raw.trim()
          : "Publish failed. Check the target config and the agent logs, then try again.",
      );
    } finally {
      setPublishBusy(null);
    }
  }

  function statusColor(s: string) {
    if (s === "completed") return "bg-emerald-500/10 text-emerald-400";
    if (s === "running") return "bg-amber-500/10 text-amber-400";
    if (s === "failed") return "bg-red-500/10 text-red-400";
    if (s === "dispatched") return "bg-sky-500/10 text-sky-400";
    return "bg-surface-800 text-surface-400";
  }

  function unityTitle(run: UnityRun) {
    if (run.stage === "test") return `Tests${run.mode ? ` · ${run.mode}` : ""}`;
    if (run.stage === "build") return `Build${run.buildTarget ? ` · ${run.buildTarget}` : ""}`;
    if (run.stage === "relaunch") return "Relaunch";
    return run.stage || "Unity";
  }

  function unityPathHint(run: UnityRun) {
    return run.executablePath || run.outputPath || run.resultsPath || run.logPath || run.projectPath || "";
  }

  return (
    <div className="space-y-5">
      <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <div className="text-xs font-medium uppercase tracking-wider text-surface-500">Publish Targets</div>
            <div className="mt-1 text-sm text-surface-300">Local/self-hosted first. GitHub fallback only when enabled per project and requested here.</div>
          </div>
          <select
            value={selectedPath}
            onChange={(e) => setSelectedPath(e.target.value)}
            className="rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
          >
            {projects.map((p) => (
              <option key={p.path} value={p.path}>{p.name}</option>
            ))}
          </select>
        </div>

        <label className="mb-3 flex items-center gap-2 text-xs text-surface-400 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={allowGitHubFallback}
            onChange={(e) => setAllowGitHubFallback(e.target.checked)}
            className="h-3.5 w-3.5 rounded border-surface-700 bg-surface-900 text-brand focus:ring-1 focus:ring-brand/40 focus:ring-offset-0 cursor-pointer accent-brand"
          />
          Allow GitHub fallback for this run
        </label>

        {publishConfig?.config?.targets?.length ? (
          <div className="flex flex-wrap gap-2">
            {publishConfig.config.targets.map((target) => (
              <button
                key={target.id}
                onClick={() => void runPublish(target.id)}
                disabled={publishBusy === target.id}
                className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 hover:bg-surface-800 disabled:opacity-50"
              >
                {publishBusy === target.id ? "Running..." : target.label || target.id}
              </button>
            ))}
          </div>
        ) : (
          <div className="text-sm text-surface-500">No publish targets yet for this project. Run `yaver publish init` in the repo.</div>
        )}

        {publishMessage ? (
          <div className="mt-3 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300">
            {publishMessage}
          </div>
        ) : null}
      </div>

      <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-4">
        <div className="mb-3 text-xs font-medium uppercase tracking-wider text-surface-500">Recent Publishes</div>
        {publishRuns.length === 0 ? (
          <EmptyState
            compact
            icon={
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
                <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
                <line x1="12" y1="22.08" x2="12" y2="12" />
              </svg>
            }
            title="No publishes yet"
            description="Publish a project to see runs here."
          />
        ) : (
          <div className="space-y-2">
            {publishRuns.slice(0, 10).map((run) => (
              <div key={run.id} className="rounded-lg border border-surface-800 bg-surface-900/60 p-3">
                <div className="flex items-center gap-3">
                  <span className={`text-xs px-2 py-0.5 rounded-full ${statusColor(run.status)}`}>{run.status}</span>
                  <span className="text-sm font-mono text-surface-200">{run.targetId}</span>
                  <span className="text-xs text-surface-500">{run.provider}</span>
                  {run.startedAt && <span className="ml-auto text-xs text-surface-600">{new Date(run.startedAt).toLocaleTimeString()}</span>}
                </div>
                {run.message && <div className="mt-2 text-xs text-surface-500">{run.message}</div>}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-4">
        <div className="mb-3 text-xs font-medium uppercase tracking-wider text-surface-500">Recent Unity Runs</div>
        {unityRuns.length === 0 ? (
          <EmptyState
            compact
            icon={
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
              </svg>
            }
            title="No Unity runs"
            description="Tests, builds, and relaunches show up here."
          />
        ) : (
          <div className="space-y-2">
            {unityRuns.slice(0, 8).map((run, index) => {
              const pathHint = unityPathHint(run);
              return (
                <div key={`${run.projectPath || "unity"}-${run.stage || "run"}-${index}`} className="rounded-lg border border-surface-800 bg-surface-900/60 p-3">
                  <div className="flex items-center gap-3">
                    <span className={`text-xs px-2 py-0.5 rounded-full ${statusColor(run.status || (run.ok ? "completed" : "failed"))}`}>
                      {run.status || (run.ok ? "completed" : "failed")}
                    </span>
                    <span className="text-sm text-surface-200">{unityTitle(run)}</span>
                    {run.nextAction && <span className="text-xs text-surface-500">{run.nextAction}</span>}
                  </div>
                  {run.summary && <div className="mt-2 text-sm text-surface-300">{run.summary}</div>}
                  {pathHint && <div className="mt-1 truncate text-xs font-mono text-surface-500">{pathHint}</div>}
                </div>
              );
            })}
          </div>
        )}
      </div>

      <div className="flex gap-2 flex-wrap">
        <button
          onClick={() => deploy("testflight")}
          disabled={!!deployDisabledReason("testflight")}
          title={deployDisabledReason("testflight")}
          className={deployButtonClass(!!deployDisabledReason("testflight"), true)}
        >
          <span>&#x1F34E;</span> TestFlight Task
        </button>
        <button
          onClick={() => deploy("playstore")}
          disabled={!!deployDisabledReason("playstore")}
          title={deployDisabledReason("playstore")}
          className={deployButtonClass(!!deployDisabledReason("playstore"))}
        >
          <span>&#x1F4E6;</span> Play Task
        </button>
        <button
          onClick={() => deploy("web")}
          disabled={!!deployDisabledReason("web")}
          title={deployDisabledReason("web")}
          className={deployButtonClass(!!deployDisabledReason("web"))}
        >
          <span>&#x1F310;</span> Web Task
        </button>
      </div>

      {noProjects ? (
        <div className="text-xs text-surface-500">
          Deploy actions unlock once a project is detected on the connected machine.
        </div>
      ) : null}

      {(deployDisabledReason("testflight") || deployDisabledReason("playstore")) ? (
        <div className="rounded-xl border border-warning/30 bg-warning-soft/40 p-4 text-sm text-warning-softFg space-y-2">
          <div className="font-medium">Host readiness blockers</div>
          {deployDisabledReason("testflight") ? <div>TestFlight: {deployDisabledReason("testflight")}</div> : null}
          {deployDisabledReason("playstore") ? <div>Play Store: {deployDisabledReason("playstore")}</div> : null}
        </div>
      ) : null}

      <div className="text-xs text-surface-500 font-medium uppercase tracking-wider">Recent Builds</div>

      {loading ? (
        <div className="text-center py-8 text-surface-500 text-sm">Loading...</div>
      ) : builds.length === 0 ? (
        <EmptyState
          compact
          icon={
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
              <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
              <line x1="12" y1="22.08" x2="12" y2="12" />
            </svg>
          }
          title="No builds yet"
          description="Publish or deploy to see build history."
        />
      ) : (
        <div className="space-y-1">
          {builds.slice(0, 15).map((b) => (
            <div key={b.id} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3">
              <span className={`text-xs px-2 py-0.5 rounded-full ${statusColor(b.status)}`}>{b.status}</span>
              <span className="flex-1 text-sm font-mono">{b.platform}</span>
              {b.artifactName && <span className="text-xs text-surface-500">{b.artifactName}</span>}
              {b.startedAt && <span className="text-xs text-surface-600">{new Date(b.startedAt).toLocaleTimeString()}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
