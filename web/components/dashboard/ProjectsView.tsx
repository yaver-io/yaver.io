"use client";

import { useState, useEffect, useMemo } from "react";
import { agentClient } from "@/lib/agent-client";
import EnvironmentSwitcher from "./EnvironmentSwitcher";
import ProjectDetailView from "./ProjectDetailView";

interface Project {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
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
    framework?: string;
    workDir?: string;
    targetDeviceName?: string;
  } | null>(null);
  const [filter, setFilter] = useState<Category>("all");
  const [search, setSearch] = useState("");

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
        <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-3 flex items-center justify-between">
          <div className="text-sm">
            <span className="text-emerald-400 font-medium">Dev server running</span>
            <span className="text-surface-400 ml-2">{devStatus.framework} &middot; {devStatus.workDir?.split("/").pop()}</span>
            {devStatus.targetDeviceName ? (
              <span className="text-sky-300 ml-2">→ {devStatus.targetDeviceName}</span>
            ) : null}
          </div>
          <div className="flex gap-2">
            <button onClick={() => void agentClient.reloadDevServer()} className="px-3 py-1 text-xs rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Refresh Preview</button>
            <button onClick={stopDev} className="px-3 py-1 text-xs rounded-md bg-red-500/10 text-red-400 hover:bg-red-500/20">Stop</button>
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
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
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
                    ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
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
                  ? "bg-indigo-500/10 border-indigo-500/30 text-indigo-400"
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
          className="flex-1 min-w-[140px] rounded-md border border-surface-800 bg-surface-900 px-2.5 py-1 text-xs text-surface-200 placeholder-surface-600 outline-none focus:border-surface-600"
        />
      </div>

      {projects.length === 0 ? (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 px-4 py-10 text-center">
          <div className="text-sm text-surface-400">
            {loadError || "No projects found on remote machine"}
          </div>
          {repairMessage ? (
            <div className={`mt-3 text-xs ${
              repairState === "failed"
                ? "text-red-300"
                : repairState === "repaired"
                  ? "text-emerald-300"
                  : "text-amber-300"
            }`}>
              {repairMessage}
            </div>
          ) : null}
          <button
            onClick={loadProjects}
            className="mt-3 rounded-md border border-surface-700 px-3 py-1.5 text-xs text-surface-300 hover:border-surface-600 hover:text-surface-100"
          >
            Retry
          </button>
          {onRepairRelay && loadError && /invalid relay password/i.test(loadError) ? (
            <button
              onClick={() => void repairRelayAndReload("manual")}
              className="mt-3 ml-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-xs text-amber-200 hover:bg-amber-500/20"
            >
              {repairState === "repairing" ? "Repairing…" : "Repair relay"}
            </button>
          ) : null}
        </div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-8 text-surface-500 text-sm">No projects match filter</div>
      ) : (
        <div className="space-y-2">
          {filtered.map((p) => {
            const cat = getCategory(p.framework);
            const icon = FRAMEWORK_ICONS[p.framework || ""] || (cat === "mobile" ? "\uD83D\uDCF1" : cat === "web" ? "\uD83C\uDF10" : "\uD83D\uDCC1");
            return (
              <div key={p.path} onClick={(e) => { if ((e.target as HTMLElement).tagName !== "BUTTON") setDetailPath(p.path); }} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3 hover:border-indigo-500/40 transition-colors cursor-pointer">
                <span className="text-lg">{icon}</span>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium truncate">{p.name}</div>
                  <div className="text-xs text-surface-500">
                    {p.branch && <span>{p.branch} &middot; </span>}
                    {p.framework || "unknown"}
                    {p.tags && p.tags.length > 0 && <span className="ml-1 text-surface-600">&middot; {p.tags.join(", ")}</span>}
                  </div>
                </div>
                <button onClick={() => setEnvProject(p.path)} className="px-2 py-1 text-[10px] rounded-md bg-surface-800 text-surface-400 hover:text-indigo-400" title="Switch environment">Env</button>
                <button onClick={() => gitSync(p)} className="px-3 py-1 text-xs rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Sync</button>
                <button onClick={() => startProject(p)} className="px-3 py-1 text-xs rounded-md bg-indigo-500/10 text-indigo-400 hover:bg-indigo-500/20">Start</button>
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
    </div>
  );
}
