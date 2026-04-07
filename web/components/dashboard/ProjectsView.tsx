"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

interface Project {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  tags?: string[];
}

const FRAMEWORK_ICONS: Record<string, string> = {
  expo: "\uD83D\uDCF1",
  "react-native": "\u269B",
  react: "\u269B",
  flutter: "\uD83D\uDC26",
  nextjs: "\u25B2",
  vite: "\u26A1",
};

export default function ProjectsView({ onTaskCreated }: { onTaskCreated?: (taskId: string) => void }) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [devStatus, setDevStatus] = useState<{ running: boolean; framework?: string; workDir?: string } | null>(null);

  useEffect(() => {
    loadProjects();
    pollDevServer();
    const interval = setInterval(pollDevServer, 5000);
    return () => clearInterval(interval);
  }, []);

  async function loadProjects() {
    setLoading(true);
    try {
      const list = await agentClient.listProjects();
      setProjects(list);
    } catch {}
    setLoading(false);
  }

  async function pollDevServer() {
    try { setDevStatus(await agentClient.getDevServerStatus()); } catch {}
  }

  async function startProject(project: Project) {
    try {
      const task = await agentClient.sendTask(
        `Start dev server for ${project.name}`,
        `Start the dev server for ${project.name} at ${project.path}. Use the appropriate framework (auto-detect). Make it available for preview.`
      );
      onTaskCreated?.(task.id);
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

  if (loading) {
    return <div className="flex items-center justify-center py-12 text-surface-500 text-sm">Loading projects...</div>;
  }

  return (
    <div className="space-y-4">
      {devStatus?.running && (
        <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-3 flex items-center justify-between">
          <div className="text-sm">
            <span className="text-emerald-400 font-medium">Dev server running</span>
            <span className="text-surface-400 ml-2">{devStatus.framework} &middot; {devStatus.workDir?.split("/").pop()}</span>
          </div>
          <div className="flex gap-2">
            <button onClick={() => agentClient.reloadDevServer()} className="px-3 py-1 text-xs rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Reload</button>
            <button onClick={stopDev} className="px-3 py-1 text-xs rounded-md bg-red-500/10 text-red-400 hover:bg-red-500/20">Stop</button>
          </div>
        </div>
      )}

      {projects.length === 0 ? (
        <div className="text-center py-12 text-surface-500 text-sm">No projects found on remote machine</div>
      ) : (
        <div className="space-y-2">
          {projects.map((p) => (
            <div key={p.path} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3 hover:border-surface-700 transition-colors">
              <span className="text-lg">{FRAMEWORK_ICONS[p.framework || ""] || "\uD83D\uDCC1"}</span>
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium truncate">{p.name}</div>
                <div className="text-xs text-surface-500">{p.branch || ""} &middot; {p.framework || "unknown"}</div>
              </div>
              <button onClick={() => gitSync(p)} className="px-3 py-1 text-xs rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Sync</button>
              <button onClick={() => startProject(p)} className="px-3 py-1 text-xs rounded-md bg-indigo-500/10 text-indigo-400 hover:bg-indigo-500/20">Start</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
