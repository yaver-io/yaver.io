"use client";

import { useState, useEffect, useMemo } from "react";
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

const MOBILE_FRAMEWORKS = ["expo", "react-native", "flutter"];
const WEB_FRAMEWORKS = ["nextjs", "vite", "react"];

type Category = "all" | "mobile" | "web" | "other";

function getCategory(framework?: string): "mobile" | "web" | "other" {
  if (!framework) return "other";
  if (MOBILE_FRAMEWORKS.includes(framework)) return "mobile";
  if (WEB_FRAMEWORKS.includes(framework)) return "web";
  return "other";
}

export default function ProjectsView({ onTaskCreated }: { onTaskCreated?: (taskId: string) => void }) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [devStatus, setDevStatus] = useState<{ running: boolean; framework?: string; workDir?: string } | null>(null);
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

  return (
    <div className="space-y-3">
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

      {/* Search + Filter */}
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
        <div className="text-center py-12 text-surface-500 text-sm">No projects found on remote machine</div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-8 text-surface-500 text-sm">No projects match filter</div>
      ) : (
        <div className="space-y-2">
          {filtered.map((p) => {
            const cat = getCategory(p.framework);
            const icon = FRAMEWORK_ICONS[p.framework || ""] || (cat === "mobile" ? "\uD83D\uDCF1" : cat === "web" ? "\uD83C\uDF10" : "\uD83D\uDCC1");
            return (
              <div key={p.path} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3 hover:border-surface-700 transition-colors">
                <span className="text-lg">{icon}</span>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium truncate">{p.name}</div>
                  <div className="text-xs text-surface-500">
                    {p.branch && <span>{p.branch} &middot; </span>}
                    {p.framework || "unknown"}
                    {p.tags && p.tags.length > 0 && <span className="ml-1 text-surface-600">&middot; {p.tags.join(", ")}</span>}
                  </div>
                </div>
                <button onClick={() => gitSync(p)} className="px-3 py-1 text-xs rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Sync</button>
                <button onClick={() => startProject(p)} className="px-3 py-1 text-xs rounded-md bg-indigo-500/10 text-indigo-400 hover:bg-indigo-500/20">Start</button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
