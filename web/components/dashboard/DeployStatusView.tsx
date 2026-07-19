"use client";

import { useCallback, useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import { UICard, Badge, EmptyState } from "@/components/ui";

// DeployStatusView — the web half of the autorun deploy-status UI wiring
// (AUTORUN_STORE.md §8.5), mirroring the mobile Deploy Status screen. Reads the
// live board from the connected box's autorun store (GET /autoruns/deploy-status)
// and shows, per target: is it deploying? which build? which stage? uploads today
// vs the daily cap. Auto-refreshes every 5s so a live archive/upload advances on
// screen. Nothing here is Convex — it's pulled live from the box over the relay.

const TARGET_LABELS: Record<string, string> = {
  testflight: "TestFlight (iOS)",
  playstore: "Play Store (Android)",
  convex: "Convex (backend)",
  "cloudflare-web": "Cloudflare (web)",
};

const STAGES = ["archiving", "exporting", "uploading", "submitting"];

interface Row {
  target: string;
  deploying: boolean;
  holder?: string;
  build?: string;
  stage?: string;
  elapsedSecs?: number;
  uploadsToday: number;
  quota: number;
}

function elapsed(secs?: number): string {
  if (!secs || secs < 0) return "";
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  return m < 60 ? `${m}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function DeployStatusView() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await agentClient.getDeployStatus();
      setRows(Array.isArray(data?.targets) ? data.targets : []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 5000);
    return () => clearInterval(t);
  }, [load]);

  if (loading && rows.length === 0) {
    return <div className="text-sm text-neutral-500 p-4">Loading deploy status…</div>;
  }
  if (error && rows.length === 0) {
    return (
      <EmptyState
        title="Deploy status unavailable"
        description={`${error} — needs the box's yaver agent (≥ the autorun-store build) connected.`}
      />
    );
  }

  return (
    <div className="space-y-3">
      {rows.map((r) => {
        const stageIdx = r.stage ? STAGES.indexOf(r.stage) : -1;
        const nearCap = r.quota > 0 && r.uploadsToday >= r.quota - 3;
        return (
          <UICard key={r.target} className={r.deploying ? "border-indigo-500" : ""}>
            <div className="flex items-center justify-between">
              <span className="font-semibold">{TARGET_LABELS[r.target] || r.target}</span>
              {r.deploying ? (
                <Badge tone="info">● {r.stage || "deploying"}</Badge>
              ) : (
                <Badge tone="muted">idle</Badge>
              )}
            </div>

            {r.deploying && (
              <>
                <div className="mt-3 flex gap-1.5">
                  {STAGES.map((st, i) => (
                    <div
                      key={st}
                      className={`h-1 flex-1 rounded ${i <= stageIdx ? "bg-indigo-500" : "bg-neutral-700"}`}
                    />
                  ))}
                </div>
                <div className="mt-2 text-xs text-neutral-400 truncate">
                  build {r.build || "?"} · {elapsed(r.elapsedSecs)} · {r.holder}
                </div>
              </>
            )}

            <div className={`mt-2 text-xs ${nearCap ? "text-amber-500" : "text-neutral-400"}`}>
              {r.uploadsToday}/{r.quota > 0 ? r.quota : "∞"} uploads today
              {nearCap ? " · near daily cap" : ""}
            </div>
          </UICard>
        );
      })}
      <p className="text-center text-xs text-neutral-500">
        Live from the box&apos;s autorun store · one deploy per target at a time
      </p>
    </div>
  );
}
