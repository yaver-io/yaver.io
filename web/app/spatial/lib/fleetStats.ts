// Pure summarizer: Task[] -> a "fleet / company at a glance" model for the
// spatial DataPane3D. Derived entirely from tasks already polled by
// useAgentBridge, so it needs no new agent endpoint.

import { agentSignalFromTaskStatus, agentStateHex } from "../../../lib/agentStatus";
import type { Task, TaskStatus } from "../useAgentBridge";

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

// Status colour now comes from lib/agentStatus.ts — the one vocabulary every
// surface reads. The map that lived here was the fourth copy in the product and
// the third meaning of `completed` (grey here, green on the mobile Tasks screen,
// blue in the mobile Home strip). agentStateHex resolves through globals.css, so
// it follows the theme instead of pinning a literal.
const statusHex = (status: TaskStatus): string => agentStateHex(agentSignalFromTaskStatus(status).state);

export function summarizeFleet(tasks: Task[]): FleetStats {
  const count = (s: string) => tasks.filter((t) => t.status === s).length;
  const running = count("running");
  const queued = count("queued");
  const review = count("review");
  const failed = count("failed");
  const completed = count("completed");

  const tokens = tasks.reduce((sum, t) => sum + (t.inputTokens ?? 0) + (t.outputTokens ?? 0), 0);

  const rows: FleetRow[] = [
    { label: "running", value: String(running), color: statusHex("running") },
    { label: "queued", value: String(queued), color: statusHex("queued") },
    { label: "review", value: String(review), color: statusHex("review") },
    { label: "failed", value: String(failed), color: failed ? statusHex("failed") : undefined },
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
