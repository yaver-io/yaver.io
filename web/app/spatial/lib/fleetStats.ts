// Pure summarizer: Task[] -> a "fleet / company at a glance" model for the
// spatial DataPane3D. Derived entirely from tasks already polled by
// useAgentBridge, so it needs no new agent endpoint.

import type { Task } from "../useAgentBridge";

export interface FleetRow {
  label: string;
  value: string;
  color?: string;
}

export interface FleetStats {
  rows: FleetRow[];
  spark: number[]; // 0..1 bar heights (status distribution)
  headline: string; // short status line, e.g. "3 running"
  tone: "ok" | "warn" | "busy" | "idle";
}

const STATUS_COLORS: Record<string, string> = {
  running: "#10b981",
  review: "#f59e0b",
  queued: "#60a5fa",
  completed: "#9ca3af",
  failed: "#ef4444",
  stopped: "#6b7280",
};

export function summarizeFleet(tasks: Task[]): FleetStats {
  const count = (s: string) => tasks.filter((t) => t.status === s).length;
  const running = count("running");
  const queued = count("queued");
  const review = count("review");
  const failed = count("failed");
  const completed = count("completed");

  const tokens = tasks.reduce((sum, t) => sum + (t.inputTokens ?? 0) + (t.outputTokens ?? 0), 0);

  const rows: FleetRow[] = [
    { label: "running", value: String(running), color: STATUS_COLORS.running },
    { label: "queued", value: String(queued), color: STATUS_COLORS.queued },
    { label: "review", value: String(review), color: STATUS_COLORS.review },
    { label: "failed", value: String(failed), color: failed ? STATUS_COLORS.failed : undefined },
    { label: "tokens", value: formatTokens(tokens) },
  ];

  // Sparkline = status distribution normalized against the busiest bucket.
  const dist = [running, queued, review, completed, failed];
  const max = Math.max(1, ...dist);
  const spark = dist.map((n) => n / max);

  const tone: FleetStats["tone"] = failed
    ? "warn"
    : running
      ? "busy"
      : queued || review
        ? "ok"
        : "idle";

  const headline =
    running > 0
      ? `${running} running`
      : queued > 0
        ? `${queued} queued`
        : failed > 0
          ? `${failed} failed`
          : "idle";

  return { rows, spark, headline, tone };
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}
