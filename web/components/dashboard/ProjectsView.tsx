"use client";

import { useState, useEffect, useMemo } from "react";
import { agentClient } from "@/lib/agent-client";
import EnvironmentSwitcher from "./EnvironmentSwitcher";
import ProjectDetailView from "./ProjectDetailView";
import RemoteRuntimeViewer from "./RemoteRuntimeViewer";
import { EmptyState, LiveDot, Button } from "@/components/ui";

interface Project {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  executionMode?: string;
  primarySurface?: string;
  tags?: string[];
}

interface PreviewTarget {
  id: string;
  name: string;
  deviceClass?: string;
  edgeProfile?: {
    supportsLocalInference: boolean;
    maxModelClass: "none" | "tiny" | "small" | "medium";
  };
}

const FRAMEWORK_ICONS: Record<string, string> = {
  expo: "\uD83D\uDCF1",
  "react-native": "\u269B",
  react: "\u269B",
  flutter: "\uD83D\uDC26",
  nextjs: "\u25B2",
  vite: "\u26A1",
};

const MOBILE_FRAMEWORKS = ["expo", "react-native", "flutter"];
const WEB_FRAMEWORKS = ["nextjs", "vite", "react"];

type Category = "all" | "mobile" | "web" | "other";

function getCategory(framework?: string): "mobile" | "web" | "other" {
  if (!framework) return "other";
  if (MOBILE_FRAMEWORKS.includes(framework)) return "mobile";
  if (WEB_FRAMEWORKS.includes(framework)) return "web";
  return "other";
}

function previewPlatformForProject(project: Project): "web" | undefined {
  const fw = (project.framework || "").toLowerCase();
  if (
    fw.includes("expo") ||
    fw.includes("react-native") ||
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react"
  ) {
    return "web";
  }
  return undefined;
}

export default function ProjectsView({
  onTaskCreated,
  mobileWorkers,
  selectedPreviewTarget,
  onSelectPreviewTarget,
  onRepairRelay,
  onReconnect,
}: {
  onTaskCreated?: (taskId: string) => void;
  mobileWorkers: PreviewTarget[];
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
  onRepairRelay?: () => Promise<{ repaired: boolean; reason: string }>;
  onReconnect?: () => Promise<void>;
}) {
  const [envProject, setEnvProject] = useState<string | null>(null);
  const [detailPath, setDetailPath] = useState<string | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [repairState, setRepairState] = useState<"idle" | "repairing" | "failed" | "repaired">("idle");
  const [repairMessage, setRepairMessage] = useState<string | null>(null);
  const [autoRepairedOnce, setAutoRepairedOnce] = useState(false);
  const [devStatus, setDevStatus] = useState<{
    running: boolean;
    serving?: boolean;
    servingLabel?: string;
    stopActionLabel?: string;
    framework?: string;
    workDir?: string;
    targetDeviceName?: string;
  } | null>(null);
  const [filter, setFilter] = useState<Category>("all");
  const [search, setSearch] = useState("");
  const [remoteCaps, setRemoteCaps] = useState<import("@/lib/agent-client").RemoteRuntimeCapabilities | null>(null);
  const [remoteProject, setRemoteProject] = useState<Project | null>(null);
  const [remoteSession, setRemoteSession] = useState<import("@/lib/agent-client").RemoteRuntimeSession | null>(null);
  const [remoteSessionNote, setRemoteSessionNote] = useState<string | null>(null);

  useEffect(() => {
    loadProjects();
    pollDevServer();
    const interval = setInterval(pollDevServer, 5000);
    return () => clearInterval(interval);
  }, []);

  async function loadProjects() {
    setLoading(true);
    setLoadError(null);
    try {
      const list = await agentClient.listProjects();
      setProjects(list);
    } catch (error) {
      setProjects([]);
      const message = error instanceof Error ? error.message : "Failed to load projects";
      setLoadError(message);
      if (!autoRepairedOnce && /invalid relay password/i.test(message) && onRepairRelay) {
        setAutoRepairedOnce(true);
        void repairRelayAndReload("auto");
      }
    }
    setLoading(false);
  }

  async function repairRelayAndReload(mode: "auto" | "manual") {
    if (!onRepairRelay) return;
    setRepairState("repairing");
    setRepairMessage(mode === "auto" ? "Detected invalid relay password — auto-repairing…" : "Repairing relay password…");
    try {
      const result = await onRepairRelay();
      if (result.repaired) {
        setRepairState("repaired");
        setRepairMessage(result.reason || "Relay repaired.");
        if (onReconnect) {
          try {
            await onReconnect();
          } catch {
            // next loadProjects still reports the real error if reconnect fails
          }
        }
        await loadProjects();
      } else {
        setRepairState("failed");
        setRepairMessage(result.reason || "Repair reported no change.");
      }
    } catch (error) {
      setRepairState("failed");
      setRepairMessage(error instanceof Error ? error.message : "Relay repair failed");
    }
  }

  async function pollDevServer() {
    try { setDevStatus(await agentClient.getDevServerStatus()); } catch {}
  }

  async function startProject(project: Project) {
    try {
      await agentClient.startDevServer({
        framework: project.framework || "",
        workDir: project.path,
        platform: previewPlatformForProject(project),
        targetDeviceId: selectedPreviewTarget?.id,
        targetDeviceName: selectedPreviewTarget?.name,
        targetDeviceClass: selectedPreviewTarget?.deviceClass,
      });
      await pollDevServer();
    } catch {}
  }

  async function gitSync(project: Project) {
    try {
      const task = await agentClient.sendTask(
        `Git Sync \u2014 ${project.name}`,
        `cd ${project.path} && Sync this repository with its remote. Pull the latest changes. If there are merge conflicts, resolve them intelligently. Show me a summary of what changed.`
      );
      onTaskCreated?.(task.id);
    } catch {}
  }

  async function stopDev() {
    await agentClient.stopDevServer();
    setDevStatus(null);
  }

  async function openRemoteRuntime(project: Project) {
    try {
      const caps = await agentClient.getRemoteRuntimeCapabilities(project.path, project.framework || "");
      setRemoteProject(project);
      setRemoteCaps(caps);
      setRemoteSession(null);
      setRemoteSessionNote(null);
    } catch (error) {
      setRemoteProject(project);
      setRemoteCaps(null);
      setRemoteSession(null);
      setRemoteSessionNote(error instanceof Error ? error.message : "Could not load remote runtime capabilities.");
    }
  }

  async function createRemoteRuntimeSession(targetId: string) {
    if (!remoteProject) return;
    try {
      const transportMode = agentClient.activeRelayUrl ? "relay-jpeg-poll" : "direct-webrtc";
      const session = await agentClient.startRemoteRuntimeSession(remoteProject.path, remoteProject.framework || "", targetId, transportMode);
      setRemoteSession(session);
      setRemoteSessionNote(session.note || `Remote runtime session ${session.id} created.`);
    } catch (error) {
      setRemoteSessionNote(error instanceof Error ? error.message : "Could not create remote runtime session.");
    }
  }

  async function triggerRemoteRuntimeFeedback() {
    if (!remoteSession) return;
    try {
      const result = await agentClient.sendRemoteRuntimeCommand(remoteSession.id, "launch-feedback", "web");
      setRemoteSession((prev) => prev ? {
        ...prev,
        status: "feedback-pending",
        lastCommand: "launch-feedback",
        note: result.note || prev.note,
      } : prev);
      setRemoteSessionNote(result.note || "Feedback launch requested.");
    } catch (error) {
      setRemoteSessionNote(error instanceof Error ? error.message : "Could not launch feedback.");
    }
  }

  // Shake from the web viewer — no phone needed. Sends the `shake` command so
  // the agent injects a hardware shake into the remote sim and the guest app's
  // own feedback SDK fires, streaming its overlay back over the video.
  async function shakeRemoteRuntime() {
    if (!remoteSession) return;
    try {
      const result = await agentClient.sendRemoteRuntimeCommand(remoteSession.id, "shake", "web-shake-button");
      setRemoteSession((prev) => prev ? { ...prev, status: "feedback-pending", lastCommand: "shake", note: result.note || prev.note } : prev);
      setRemoteSessionNote(result.note || (result.injected ? "Shake injected into the simulator." : "Shake sent."));
    } catch (error) {
      setRemoteSessionNote(error instanceof Error ? error.message : "Could not shake the simulator.");
    }
  }

  // Build+launch the RN guest app into the booted sim (dev mode, Fast Refresh).
  async function runGuestInSimulator() {
    if (!remoteSession) return;
    try {
      const result = await agentClient.sendRemoteRuntimeCommand(remoteSession.id, "run-guest", "web", remoteSession.workDir);
      setRemoteSession((prev) => prev ? { ...prev, status: result.status || "building", lastCommand: "run-guest", note: result.note || prev.note } : prev);
      setRemoteSessionNote(result.note || "Building the app into the simulator — Metro Fast Refresh once running.");
    } catch (error) {
      setRemoteSessionNote(error instanceof Error ? error.message : "Could not run the app.");
    }
  }

  async function closeRemoteRuntimeSession() {
    if (!remoteSession) return;
    try {
      await agentClient.closeRemoteRuntimeSession(remoteSession.id);
      setRemoteSession(null);
      setRemoteSessionNote("Remote runtime session closed.");
    } catch (error) {
      setRemoteSessionNote(error instanceof Error ? error.message : "Could not close remote runtime session.");
    }
  }

  // Compute category counts and filtered list
  const categories = useMemo(() => {
    const counts = { all: projects.length, mobile: 0, web: 0, other: 0 };
    for (const p of projects) {
      counts[getCategory(p.framework)]++;
    }
    return counts;
  }, [projects]);

  const filtered = useMemo(() => {
    let list = projects;
    if (filter !== "all") {
      list = list.filter(p => getCategory(p.framework) === filter);
    }
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter(p =>
        p.name.toLowerCase().includes(q) ||
        p.path.toLowerCase().includes(q) ||
        (p.framework || "").toLowerCase().includes(q) ||
        (p.tags ?? []).some(t => t.toLowerCase().includes(q))
      );
    }
    return list;
  }, [projects, filter, search]);

  if (loading) {
    return <div className="flex items-center justify-center py-12 text-surface-500 text-sm">Loading projects...</div>;
  }

  const filterChips: { id: Category; label: string; count: number }[] = [
    { id: "all", label: "All", count: categories.all },
    ...(categories.mobile > 0 ? [{ id: "mobile" as Category, label: "Mobile", count: categories.mobile }] : []),
    ...(categories.web > 0 ? [{ id: "web" as Category, label: "Web", count: categories.web }] : []),
    ...(categories.other > 0 ? [{ id: "other" as Category, label: "Other", count: categories.other }] : []),
  ];

  if (detailPath) {
    return <ProjectDetailView directory={detailPath} onClose={() => setDetailPath(null)} />;
  }

  return (
    <div className="space-y-3">
      {devStatus?.running && (
        <div className="rounded-lg border border-success/25 bg-success-soft/40 px-3 py-2 flex items-center justify-between">
          <div className="text-sm flex items-center gap-2">
            <LiveDot tone="success" size="xs" />
            <span className="text-success-softFg dark:text-success font-medium">{devStatus.servingLabel || "Serving preview"}</span>
            <span className="text-surface-400">{devStatus.framework} &middot; {devStatus.workDir?.split("/").pop()}</span>
            {devStatus.targetDeviceName ? (
              <span className="text-info dark:text-info-softFg">→ {devStatus.targetDeviceName}</span>
            ) : null}
          </div>
          <div className="flex gap-1.5">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void agentClient.reloadDevServer({ mode: (devStatus?.framework || "").match(/^(expo|react-native)$/i) ? "bundle" : "dev" })}
            >
              Refresh
            </Button>
            <Button variant="danger-ghost" size="sm" onClick={stopDev}>
              {devStatus.stopActionLabel || "Stop"}
            </Button>
          </div>
        </div>
      )}

      {/* Search + Filter */}
      {mobileWorkers.length > 0 && (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
          <div className="text-[11px] font-semibold uppercase tracking-widest text-surface-500">Preview Target</div>
          <div className="flex flex-wrap gap-2">
            <button
              onClick={() => onSelectPreviewTarget(null)}
              className={`px-2.5 py-1 text-[11px] rounded-md border ${
                !selectedPreviewTarget
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300"
                  : "border-surface-800 text-surface-500 hover:border-surface-700 hover:text-surface-300"
              }`}
            >
              Current device
            </button>
            {mobileWorkers.map((device) => (
              <button
                key={device.id}
                onClick={() => onSelectPreviewTarget(device.id)}
                className={`px-2.5 py-1 text-[11px] rounded-md border ${
                  selectedPreviewTarget?.id === device.id
                    ? "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300"
                    : "border-surface-800 text-surface-500 hover:border-surface-700 hover:text-surface-300"
                }`}
                title={device.edgeProfile ? `max ${device.edgeProfile.maxModelClass}` : "mobile worker"}
              >
                {device.name}
              </button>
            ))}
          </div>
          <div className="text-xs text-surface-500">
            Web UI preview prefers browser/webview flows first. Real-device worker targeting still applies when you need native validation.
          </div>
        </div>
      )}

      <div className="flex items-center gap-2 flex-wrap">
        <div className="flex gap-1">
          {filterChips.map(c => (
            <button
              key={c.id}
              onClick={() => setFilter(c.id)}
              className={`px-2.5 py-1 text-[11px] font-medium rounded-md border transition-colors ${
                filter === c.id
                  ? "bg-brand-soft border-brand/30 text-brand-softFg"
                  : "border-surface-800 text-surface-500 hover:text-surface-300 hover:border-surface-700"
              }`}
            >
              {c.label} ({c.count})
            </button>
          ))}
        </div>
        <input
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Search projects..."
          className="flex-1 min-w-[140px] rounded-md border border-surface-800 bg-surface-900 px-2.5 py-1 text-xs text-surface-200 placeholder-surface-600 outline-none focus:border-surface-600 focus:ring-1 focus:ring-brand/30"
        />
      </div>

      {projects.length === 0 ? (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40">
          <EmptyState
            icon={
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
              </svg>
            }
            title={loadError ? "Couldn't load projects" : "No projects yet"}
            description={loadError || "Projects on the remote machine will appear here once they're discovered."}
            action={
              <div className="flex gap-2 justify-center">
                <Button variant="secondary" size="sm" onClick={loadProjects}>Retry</Button>
                {onRepairRelay && loadError && /invalid relay password/i.test(loadError) ? (
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() => void repairRelayAndReload("manual")}
                  >
                    {repairState === "repairing" ? "Repairing…" : "Repair relay"}
                  </Button>
                ) : null}
              </div>
            }
          />
          {repairMessage ? (
            <div className={`pb-4 text-center text-xs ${
              repairState === "failed"
                ? "text-danger"
                : repairState === "repaired"
                  ? "text-success"
                  : "text-warning"
            }`}>
              {repairMessage}
            </div>
          ) : null}
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          compact
          title="No matches"
          description="Try a different filter or search term."
        />
      ) : (
        <div className="space-y-2">
          {filtered.map((p) => {
            const cat = getCategory(p.framework);
            const icon = FRAMEWORK_ICONS[p.framework || ""] || (cat === "mobile" ? "\uD83D\uDCF1" : cat === "web" ? "\uD83C\uDF10" : "\uD83D\uDCC1");
            return (
              <div key={p.path} onClick={(e) => { if ((e.target as HTMLElement).tagName !== "BUTTON") setDetailPath(p.path); }} className="group rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3 hover:border-brand/40 transition-colors cursor-pointer">
                <span className="text-lg">{icon}</span>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium truncate">{p.name}</div>
                  <div className="text-xs text-surface-500 flex items-center gap-1.5 flex-wrap mt-0.5">
                    {p.branch ? <span className="font-mono">{p.branch}</span> : null}
                    {p.framework ? <><span className="text-surface-700">·</span><span>{p.framework}</span></> : null}
                    {p.tags && p.tags.length > 0 ? (
                      <>
                        <span className="text-surface-700">·</span>
                        <span className="text-surface-600 truncate">
                          {p.tags.slice(0, 4).join(", ")}
                          {p.tags.length > 4 ? ` +${p.tags.length - 4}` : ""}
                        </span>
                      </>
                    ) : null}
                  </div>
                  {p.primarySurface ? (
                    <div className="mt-1 flex items-center gap-1.5">
                      <span className="text-[10px] uppercase tracking-wide text-surface-600">
                        Primary: {p.primarySurface}{p.executionMode ? ` · ${p.executionMode}` : ""}
                      </span>
                      {p.primarySurface.toLowerCase() === "none" || /unsupported/i.test(p.executionMode || "") ? (
                        <span className="rounded bg-warning-soft text-warning-softFg text-[9px] font-semibold uppercase tracking-wider px-1.5 py-px">
                          Unsupported
                        </span>
                      ) : null}
                    </div>
                  ) : null}
                </div>
                <button
                  onClick={(e) => { e.stopPropagation(); setEnvProject(p.path); }}
                  className="px-2 py-1 text-[10px] rounded-md text-surface-500 hover:text-brand hover:bg-surface-800/60 transition-colors"
                  title="Switch environment"
                >
                  Env
                </button>
                <button
                  onClick={(e) => { e.stopPropagation(); gitSync(p); }}
                  className="px-2.5 py-1 text-xs rounded-md text-surface-400 hover:text-surface-100 hover:bg-surface-800/60 transition-colors"
                >
                  Sync
                </button>
                {p.executionMode === "native-webrtc" ? (
                  <button
                    onClick={(e) => { e.stopPropagation(); void openRemoteRuntime(p); }}
                    className="px-3 py-1 text-xs rounded-md bg-warning-soft text-warning-softFg hover:bg-warning/15 transition-colors"
                  >
                    Remote Runtime
                  </button>
                ) : p.executionMode === "rn-hermes" ? (
                  <div className="flex items-center gap-2">
                    {/* Hermes is the default (fast bytecode reload into the phone
                        container, real device). */}
                    <button
                      onClick={(e) => { e.stopPropagation(); startProject(p); }}
                      className="px-3 py-1 text-xs font-medium rounded-md bg-brand text-brand-fg hover:bg-brand/90 active:scale-[0.97] transition-all"
                      title="Hermes push — real device, camera & sensors, slower reload"
                    >
                      ⚡ Hermes
                    </button>
                    {/* WebRTC alternative: run the app in a remote simulator,
                        streamed here. Metro Fast Refresh = instant iteration;
                        hardware is simulated. */}
                    <button
                      onClick={(e) => { e.stopPropagation(); void openRemoteRuntime(p); }}
                      className="px-3 py-1 text-xs rounded-md bg-warning-soft text-warning-softFg hover:bg-warning/15 transition-colors"
                      title="WebRTC — run in a remote simulator, instant Fast Refresh, simulated hardware"
                    >
                      Simulator (WebRTC)
                    </button>
                  </div>
                ) : (
                  <button
                    onClick={(e) => { e.stopPropagation(); startProject(p); }}
                    className="px-3 py-1 text-xs rounded-md bg-brand-soft text-brand-softFg hover:bg-brand/15 transition-colors"
                  >
                    Start
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}

      {envProject && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={() => setEnvProject(null)}>
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-4 max-w-lg w-full space-y-3" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold">Environment · <span className="font-mono text-surface-500">{envProject.split("/").pop()}</span></h3>
              <button onClick={() => setEnvProject(null)} className="text-xs text-surface-500">close</button>
            </div>
            <EnvironmentSwitcher directory={envProject} />
          </div>
        </div>
      )}

      {remoteProject && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={() => { setRemoteProject(null); setRemoteCaps(null); setRemoteSession(null); setRemoteSessionNote(null); }}>
          <div className="w-full max-w-xl rounded-xl border border-surface-700 bg-surface-950 p-4 space-y-4" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-surface-100">Native Remote Runtime</div>
                <div className="text-xs text-surface-500">{remoteProject.name} · {remoteProject.framework || "unknown"}</div>
              </div>
              <button onClick={() => { setRemoteProject(null); setRemoteCaps(null); setRemoteSession(null); setRemoteSessionNote(null); }} className="text-xs text-surface-500 hover:text-surface-300">close</button>
            </div>
            {remoteCaps ? (
              <div className="space-y-3">
                <div className="rounded-lg border border-surface-800 bg-surface-900/60 p-3 text-xs text-surface-300">
                  Primary surface: <span className="font-medium text-surface-100">{remoteCaps.primarySurface}</span>
                  <span className="text-surface-500"> · execution mode {remoteCaps.executionMode}</span>
                  {remoteCaps.supportedTransports?.length ? (
                    <span className="text-surface-500"> · transports {remoteCaps.supportedTransports.join(", ")}</span>
                  ) : null}
                  {remoteCaps.currentHostClass ? (
                    <span className="text-surface-500"> · host {remoteCaps.currentHostClass}</span>
                  ) : null}
                </div>
                {remoteCaps.feedbackSdkCompatible ? (
                  <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-3 text-xs text-emerald-800 dark:text-emerald-100">
                    Feedback SDK: {remoteCaps.feedbackSdkNote || "compatible"}
                    {remoteCaps.feedbackControlProtocol ? ` · protocol ${remoteCaps.feedbackControlProtocol}` : ""}
                  </div>
                ) : null}
                <div className="space-y-2">
                  {remoteCaps.targets.map((target) => (
                    <div key={target.id} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3">
                      <div className="flex items-center justify-between gap-3">
                        <div>
                          <div className="text-sm text-surface-100">{target.label}</div>
                          <div className="text-xs text-surface-500">
                            {target.requiredCli || "runtime tools"} · host {target.hostOs || "unknown"} · runtime class {target.runtimeHostClass || "generic"}
                          </div>
                        </div>
                        <button
                          disabled={!target.enabled}
                          onClick={() => void createRemoteRuntimeSession(target.id)}
                          className={`px-3 py-1 text-xs rounded-md ${target.enabled ? "bg-amber-500/10 text-amber-700 dark:text-amber-300 hover:bg-amber-500/20" : "bg-surface-800 text-surface-500 cursor-not-allowed"}`}
                        >
                          {target.enabled ? "Create Session" : "Unavailable"}
                        </button>
                      </div>
                      {target.reason ? <div className="mt-2 text-xs text-rose-700 dark:text-rose-300">{target.reason}</div> : null}
                    </div>
                  ))}
                </div>
                {remoteSession ? (
                  <div className="rounded-lg border border-sky-500/20 bg-sky-500/5 p-3 text-xs text-sky-800 dark:text-sky-100 space-y-2">
                    <div>
                      Session <span className="font-mono">{remoteSession.id}</span> · {remoteSession.status}
                      {remoteSession.lastCommand ? ` · ${remoteSession.lastCommand}` : ""}
                      {remoteSession.transportMode ? ` · ${remoteSession.transportMode}` : ""}
                    </div>
                    {remoteSession.note ? <div className="text-sky-700 dark:text-sky-200/80">{remoteSession.note}</div> : null}
                    <div className="flex flex-wrap items-center gap-2">
                      {/* Run the RN guest app into the sim (Metro Fast Refresh). */}
                      {remoteCaps?.feedbackSurface === "client-shake-remote-sim" ? (
                        <button onClick={() => void runGuestInSimulator()} className="px-3 py-1 text-xs rounded-md bg-brand-soft text-brand-softFg hover:bg-brand/15">
                          Run app in simulator
                        </button>
                      ) : null}
                      {/* Shake from the browser — injects into the remote sim. */}
                      <button onClick={() => void shakeRemoteRuntime()} className="px-3 py-1 text-xs rounded-md bg-amber-500/15 text-amber-700 dark:text-amber-200 hover:bg-amber-500/25" title="Inject a shake into the simulator to open the app's feedback">
                        🫨 Shake
                      </button>
                      <button onClick={() => void triggerRemoteRuntimeFeedback()} className="px-3 py-1 text-xs rounded-md bg-sky-500/15 text-sky-700 dark:text-sky-200 hover:bg-sky-500/25">
                        Trigger Feedback
                      </button>
                      <button onClick={() => void closeRemoteRuntimeSession()} className="px-3 py-1 text-xs rounded-md bg-rose-500/15 text-rose-700 dark:text-rose-200 hover:bg-rose-500/25">
                        Close Session
                      </button>
                    </div>
                    <RemoteRuntimeViewer session={remoteSession} onSessionChange={setRemoteSession} />
                  </div>
                ) : null}
              </div>
            ) : (
              <div className="rounded-lg border border-rose-500/20 bg-rose-500/5 p-3 text-sm text-rose-700 dark:text-rose-200">
                {remoteSessionNote || "Could not load remote runtime capabilities."}
              </div>
            )}
            {remoteSessionNote && remoteCaps ? (
              <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-xs text-amber-800 dark:text-amber-100">
                {remoteSessionNote}
              </div>
            ) : null}
          </div>
        </div>
      )}
    </div>
  );
}
