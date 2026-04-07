"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

interface Build { id: string; platform: string; status: string; startedAt?: number; artifactName?: string; }

export default function BuildsView({ onTaskCreated }: { onTaskCreated?: (taskId: string) => void }) {
  const [builds, setBuilds] = useState<Build[]>([]);
  const [loading, setLoading] = useState(true);
  const [projects, setProjects] = useState<{ name: string; path: string; framework?: string }[]>([]);

  useEffect(() => { loadBuilds(); loadProjects(); }, []);

  async function loadBuilds() {
    setLoading(true);
    try { setBuilds(await agentClient.listBuilds()); } catch {}
    setLoading(false);
  }

  async function loadProjects() {
    try { setProjects(await agentClient.listProjects()); } catch {}
  }

  async function deploy(target: "testflight" | "playstore" | "web") {
    let proj = projects[0];
    if (projects.length > 1) {
      const choice = prompt(`Select project:\n${projects.map((p, i) => `${i + 1}. ${p.name}`).join("\n")}\n\nEnter number:`);
      if (!choice) return;
      proj = projects[parseInt(choice) - 1];
      if (!proj) { alert("Invalid selection"); return; }
    }
    if (!proj) { alert("No projects found"); return; }

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

  function statusColor(s: string) {
    if (s === "completed") return "bg-emerald-500/10 text-emerald-400";
    if (s === "running") return "bg-amber-500/10 text-amber-400";
    if (s === "failed") return "bg-red-500/10 text-red-400";
    return "bg-surface-800 text-surface-400";
  }

  return (
    <div className="space-y-4">
      <div className="flex gap-2 flex-wrap">
        <button onClick={() => deploy("testflight")} className="px-3 py-2 text-sm rounded-lg border border-surface-700 bg-surface-900 hover:bg-surface-800 flex items-center gap-2">
          <span>&#x1F34E;</span> TestFlight
        </button>
        <button onClick={() => deploy("playstore")} className="px-3 py-2 text-sm rounded-lg border border-surface-700 bg-surface-900 hover:bg-surface-800 flex items-center gap-2">
          <span>&#x1F4E6;</span> Google Play
        </button>
        <button onClick={() => deploy("web")} className="px-3 py-2 text-sm rounded-lg border border-surface-700 bg-surface-900 hover:bg-surface-800 flex items-center gap-2">
          <span>&#x1F310;</span> Web Deploy
        </button>
      </div>

      <div className="text-xs text-surface-500 font-medium uppercase tracking-wider">Recent Builds</div>

      {loading ? (
        <div className="text-center py-8 text-surface-500 text-sm">Loading...</div>
      ) : builds.length === 0 ? (
        <div className="text-center py-8 text-surface-500 text-sm">No builds yet. Deploy to see build history.</div>
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
