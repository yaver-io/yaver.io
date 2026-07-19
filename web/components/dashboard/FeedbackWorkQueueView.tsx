"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { agentClient, type FeedbackWorkAgentConfig } from "@/lib/agent-client";
import {
  listFeedbackWorkItems,
  listRelaySourceIntents,
  queueFeedbackWorkItemRelaySource,
  routeFeedbackWorkItem,
  updateFeedbackWorkItemStatus,
  type FeedbackWorkItem,
  type FeedbackWorkStatus,
  type FeedbackWorkTarget,
  type RelaySourceIntent,
  type RelaySourceIntentStatus,
} from "@/lib/task-placement";

const FILTERS: Array<{ id: FeedbackWorkStatus | "all"; label: string }> = [
  { id: "all", label: "All" },
  { id: "queued", label: "Queued" },
  { id: "claimed", label: "Claimed" },
  { id: "task_created", label: "Tasks" },
  { id: "issue_draft_created", label: "Issue Drafts" },
  { id: "branch_created", label: "Branches" },
  { id: "blocked", label: "Blocked" },
];

function statusTone(status: FeedbackWorkStatus): string {
  switch (status) {
    case "queued":
      return "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-200";
    case "claimed":
      return "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200";
    case "task_created":
    case "issue_draft_created":
    case "issue_created":
    case "branch_created":
      return "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200";
    case "blocked":
      return "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-200";
    default:
      return "border-surface-700 bg-surface-900 text-surface-400";
  }
}

function relaySourceStatusTone(status: RelaySourceIntentStatus): string {
  switch (status) {
    case "queued":
      return "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-200";
    case "claimed":
      return "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200";
    case "committed":
    case "handoff_ready":
      return "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200";
    case "blocked":
    case "failed":
      return "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-200";
    default:
      return "border-surface-700 bg-surface-900 text-surface-400";
  }
}

function targetLabel(target: FeedbackWorkTarget): string {
  switch (target) {
    case "task":
      return "Task";
    case "issue":
      return "Issue";
    case "branch":
      return "Branch";
    default:
      return "Triage";
  }
}

function formatAge(ts?: number | null): string {
  if (!ts) return "";
  const seconds = Math.max(0, Math.round((Date.now() - ts) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

function shortText(value: string, max = 280): string {
  const text = String(value || "").trim().replace(/\s+/g, " ");
  if (text.length <= max) return text;
  return `${text.slice(0, max - 1).trim()}...`;
}

function providerAuthLabel(intent: RelaySourceIntent): string {
  const mode = String(intent.providerAuthMode || "").trim();
  const status = String(intent.providerAuthStatus || "").trim();
  if (mode === "app_installation" && status === "available") return "GitHub App";
  if (mode === "owner_local_token" || status === "owner_token_fallback") return "Owner token fallback";
  if (status === "unsupported") return "Provider app token unsupported";
  if (status === "required") return "Provider app token required";
  if (mode || status) return [mode, status].filter(Boolean).join(" / ").replace(/_/g, " ");
  return "Local branch only";
}

function providerAuthTone(intent: RelaySourceIntent): string {
  const mode = String(intent.providerAuthMode || "").trim();
  const status = String(intent.providerAuthStatus || "").trim();
  if (mode === "app_installation" && status === "available") {
    return "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200";
  }
  if (mode === "owner_local_token" || status === "owner_token_fallback") {
    return "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200";
  }
  if (status === "unsupported") {
    return "border-surface-700 bg-surface-900 text-surface-400";
  }
  return "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-200";
}

export default function FeedbackWorkQueueView({
  token,
  agentConnected,
}: {
  token: string | null | undefined;
  agentConnected?: boolean;
}) {
  const [items, setItems] = useState<FeedbackWorkItem[]>([]);
  const [filter, setFilter] = useState<FeedbackWorkStatus | "all">("queued");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [workerConfig, setWorkerConfig] = useState<FeedbackWorkAgentConfig | null>(null);
  const [workerConfigError, setWorkerConfigError] = useState<string | null>(null);
  const [workerConfigBusy, setWorkerConfigBusy] = useState(false);
  const [relaySourceIntents, setRelaySourceIntents] = useState<RelaySourceIntent[]>([]);

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setError(null);
    try {
      const rows = await listFeedbackWorkItems(token, {
        limit: 80,
        status: filter === "all" ? undefined : filter,
      });
      setItems(rows);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [filter, token]);

  const loadRelaySourceIntents = useCallback(async () => {
    if (!token) return;
    try {
      setRelaySourceIntents(await listRelaySourceIntents(token, { limit: 12, includeTerminal: true, scope: "owned" }));
    } catch {
      setRelaySourceIntents([]);
    }
  }, [token]);

  useEffect(() => {
    void load();
    void loadRelaySourceIntents();
    const id = setInterval(() => void load(), 30000);
    const relayId = setInterval(() => void loadRelaySourceIntents(), 30000);
    return () => {
      clearInterval(id);
      clearInterval(relayId);
    };
  }, [load, loadRelaySourceIntents]);

  const loadWorkerConfig = useCallback(async () => {
    if (!agentConnected) {
      setWorkerConfig(null);
      setWorkerConfigError(null);
      return;
    }
    try {
      setWorkerConfigError(null);
      setWorkerConfig(await agentClient.getFeedbackWorkConfig());
    } catch (err) {
      setWorkerConfig(null);
      setWorkerConfigError(err instanceof Error ? err.message : String(err));
    }
  }, [agentConnected]);

  useEffect(() => {
    void loadWorkerConfig();
  }, [loadWorkerConfig]);

  const counts = useMemo(() => {
    const next = new Map<string, number>();
    for (const item of items) next.set(item.status, (next.get(item.status) || 0) + 1);
    return next;
  }, [items]);

  async function runAction(item: FeedbackWorkItem, label: string, fn: () => Promise<unknown>) {
    if (!token) return;
    setBusy(`${item.id}:${label}`);
    setMessage(null);
    setError(null);
    try {
      await fn();
      setMessage(`${label} requested for "${item.title}".`);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  async function setStatus(item: FeedbackWorkItem, status: FeedbackWorkStatus, reason: string) {
    await runAction(item, status, () =>
      updateFeedbackWorkItemStatus(token!, {
        itemId: item.id,
        status,
        reason,
        workerId: "web-dashboard",
      }),
    );
  }

  async function routeItem(item: FeedbackWorkItem, target: FeedbackWorkTarget, reason: string) {
    await runAction(item, targetLabel(target), () =>
      routeFeedbackWorkItem(token!, {
        itemId: item.id,
        target,
        reason,
        workerId: "web-dashboard",
      }),
    );
  }

  async function updateWorkerConfig(patch: Partial<FeedbackWorkAgentConfig>) {
    if (!agentConnected) return;
    setWorkerConfigBusy(true);
    setWorkerConfigError(null);
    try {
      setWorkerConfig(await agentClient.updateFeedbackWorkConfig(patch));
    } catch (err) {
      setWorkerConfigError(err instanceof Error ? err.message : String(err));
    } finally {
      setWorkerConfigBusy(false);
    }
  }

  if (!token) {
    return <div className="p-6 text-sm text-surface-500">Sign in to review feedback work.</div>;
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-surface-100">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">Feedback Work</h2>
          <p className="mt-1 text-xs leading-5 text-surface-500">
            Review guest feedback and route it to owner-machine tasks, private issue drafts, or branch-scoped relay work.
          </p>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          disabled={loading}
          className="rounded-md border border-surface-700 bg-surface-900 px-3 py-1.5 text-xs font-semibold text-surface-200 disabled:opacity-50"
        >
          {loading ? "Refreshing..." : "Refresh"}
        </button>
      </header>

      <section className="rounded border border-surface-800 bg-surface-950/40 p-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-surface-100">Owner-machine worker</h3>
            <p className="mt-1 text-xs leading-5 text-surface-500">
              The local agent claims queued feedback and turns it into tasks, private issue drafts, or provider issues when explicitly enabled.
            </p>
          </div>
          {agentConnected ? (
            <button
              type="button"
              onClick={() => void loadWorkerConfig()}
              disabled={workerConfigBusy}
              className="rounded-md border border-surface-700 bg-surface-900 px-2.5 py-1 text-xs font-semibold text-surface-300 disabled:opacity-50"
            >
              Sync
            </button>
          ) : null}
        </div>
        {!agentConnected ? (
          <p className="mt-3 text-xs text-amber-700 dark:text-amber-300">
            Connect a Yaver machine to manage the local feedback worker. Queue review still works from the web.
          </p>
        ) : workerConfigError ? (
          <p className="mt-3 text-xs text-rose-700 dark:text-rose-300">{workerConfigError}</p>
        ) : workerConfig ? (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <button
              type="button"
              disabled={workerConfigBusy}
              onClick={() => void updateWorkerConfig({ enabled: !workerConfig.enabled })}
              className={`rounded-md border px-2.5 py-1 text-xs font-semibold disabled:opacity-50 ${
                workerConfig.enabled
                  ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
                  : "border-surface-700 bg-surface-900 text-surface-400"
              }`}
            >
              {workerConfig.enabled ? "Worker On" : "Worker Off"}
            </button>
            <span
              className={`rounded-md border px-2.5 py-1 text-xs font-semibold ${
                workerConfig.running
                  ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
                  : workerConfig.enabled
                    ? "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200"
                    : "border-surface-700 bg-surface-900 text-surface-400"
              }`}
            >
              {workerConfig.running ? "Running" : workerConfig.enabled ? "Saved, not running" : "Stopped"}
            </span>
            <button
              type="button"
              disabled={workerConfigBusy}
              onClick={() => void updateWorkerConfig({ createProviderIssues: !workerConfig.createProviderIssues })}
              className={`rounded-md border px-2.5 py-1 text-xs font-semibold disabled:opacity-50 ${
                workerConfig.createProviderIssues
                  ? "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200"
                  : "border-surface-700 bg-surface-900 text-surface-400"
              }`}
              title="When on, issue feedback can create GitHub/GitLab issues using provider auth stored on this machine."
            >
              {workerConfig.createProviderIssues ? "Provider Issues On" : "Provider Issues Off"}
            </button>
            {workerConfig.projectSlug ? (
              <span className="rounded bg-surface-900 px-2 py-1 font-mono text-[11px] text-surface-500">
                {workerConfig.projectSlug}
              </span>
            ) : null}
            {!workerConfig.running && workerConfig.runtimeReason ? (
              <span className="text-xs text-surface-500">
                {workerConfig.runtimeReason}
              </span>
            ) : null}
            {workerConfig.createProviderIssues ? (
              <span className="text-xs text-amber-700 dark:text-amber-300">
                Uses this machine's local GitHub/GitLab provider auth.
              </span>
            ) : null}
          </div>
        ) : (
          <p className="mt-3 text-xs text-surface-500">Loading worker config...</p>
        )}
      </section>

      <div className="flex flex-wrap gap-2">
        {FILTERS.map((option) => (
          <button
            key={option.id}
            type="button"
            onClick={() => setFilter(option.id)}
            className={`rounded-md border px-2.5 py-1 text-xs font-medium ${
              filter === option.id
                ? "border-brand/40 bg-brand-soft text-brand-softFg"
                : "border-surface-700 bg-surface-900 text-surface-400 hover:border-surface-600 hover:text-surface-200"
            }`}
          >
            {option.label}
            {option.id !== "all" && counts.get(option.id) ? (
              <span className="ml-1 text-[10px] opacity-70">{counts.get(option.id)}</span>
            ) : null}
          </button>
        ))}
      </div>

      {error ? (
        <div className="rounded border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-700 dark:text-rose-200">
          {error}
        </div>
      ) : null}
      {message ? (
        <div className="rounded border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-200">
          {message}
        </div>
      ) : null}

      <div className="space-y-3">
        {items.map((item) => {
          const keyPrefix = `${item.id}:`;
          const isBusy = busy?.startsWith(keyPrefix);
          const canRoute = !["task_created", "issue_draft_created", "issue_created", "branch_created", "cancelled", "rejected", "expired"].includes(item.status);
          return (
            <article key={item.id} className="rounded border border-surface-800 bg-surface-950/40 p-3">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="truncate text-sm font-semibold text-surface-100">{item.title}</h3>
                    <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${statusTone(item.status)}`}>
                      {item.status.replace(/_/g, " ")}
                    </span>
                    <span className="rounded-full border border-surface-700 bg-surface-900 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-surface-400">
                      {targetLabel(item.target)}
                    </span>
                  </div>
                  <p className="mt-1 text-[11px] text-surface-500">
                    {item.projectSlug} · {item.kind} · {item.priority} · {formatAge(item.createdAt)}
                    {item.component ? ` · ${item.component}` : ""}
                    {item.platform ? ` · ${item.platform}` : ""}
                  </p>
                </div>
              </div>

              <p className="mt-3 text-sm leading-6 text-surface-300">{shortText(item.body)}</p>

              <div className="mt-3 flex flex-wrap gap-2">
                {item.taskId ? <span className="rounded bg-surface-900 px-2 py-1 font-mono text-[11px] text-surface-400">task {item.taskId}</span> : null}
                {item.issueUrl ? (
                  <a href={item.issueUrl} target="_blank" rel="noreferrer" className="rounded bg-surface-900 px-2 py-1 text-[11px] text-indigo-700 underline dark:text-indigo-300">
                    issue
                  </a>
                ) : null}
                {item.branch ? <span className="rounded bg-surface-900 px-2 py-1 font-mono text-[11px] text-surface-400">{item.branch}</span> : null}
                {item.reason ? <span className="rounded bg-surface-900 px-2 py-1 text-[11px] text-surface-500">{item.reason}</span> : null}
              </div>

              <div className="mt-3 flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  disabled={isBusy || !canRoute}
                  onClick={() => void runAction(item, "Branch work", () => queueFeedbackWorkItemRelaySource(token, { itemId: item.id, workerId: "web-dashboard" }))}
                  className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1 text-xs font-semibold text-emerald-700 disabled:opacity-40 dark:text-emerald-200"
                >
                  Branch
                </button>
                <button
                  type="button"
                  disabled={isBusy || !canRoute}
                  onClick={() => void routeItem(item, "issue", "issue draft requested from web dashboard")}
                  className="rounded-md border border-sky-500/30 bg-sky-500/10 px-2.5 py-1 text-xs font-semibold text-sky-700 disabled:opacity-40 dark:text-sky-200"
                >
                  Issue Draft
                </button>
                <button
                  type="button"
                  disabled={isBusy || !canRoute}
                  onClick={() => void routeItem(item, "task", "owner-machine task requested from web dashboard")}
                  className="rounded-md border border-indigo-500/30 bg-indigo-500/10 px-2.5 py-1 text-xs font-semibold text-indigo-700 disabled:opacity-40 dark:text-indigo-200"
                >
                  Task
                </button>
                <button
                  type="button"
                  disabled={isBusy || !canRoute}
                  onClick={() => void setStatus(item, "rejected", "rejected from web dashboard")}
                  className="rounded-md border border-surface-700 bg-surface-900 px-2.5 py-1 text-xs font-semibold text-surface-400 disabled:opacity-40"
                >
                  Reject
                </button>
                {isBusy ? <span className="text-xs text-surface-500">Working...</span> : null}
              </div>
            </article>
          );
        })}
      </div>

      {!loading && items.length === 0 ? (
        <div className="rounded border border-surface-800 bg-surface-950/40 p-5 text-sm text-surface-500">
          No feedback work items match this filter.
        </div>
      ) : null}

      <section className="rounded border border-surface-800 bg-surface-950/40 p-3">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-surface-100">Relay branch handoffs</h3>
            <p className="mt-1 text-xs leading-5 text-surface-500">
              Provider branch state for owner-scoped relay-source work. Tokens and file paths are never shown here.
            </p>
          </div>
          <button
            type="button"
            onClick={() => void loadRelaySourceIntents()}
            className="rounded-md border border-surface-700 bg-surface-900 px-2.5 py-1 text-xs font-semibold text-surface-300"
          >
            Sync
          </button>
        </div>
        {relaySourceIntents.length > 0 ? (
          <div className="space-y-2">
            {relaySourceIntents.map((intent) => (
              <div key={intent.id} className="rounded border border-surface-800 bg-surface-900/60 p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs text-surface-200">{intent.projectSlug}</span>
                  <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${relaySourceStatusTone(intent.status)}`}>
                    {intent.status.replace(/_/g, " ")}
                  </span>
                  <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${providerAuthTone(intent)}`}>
                    {providerAuthLabel(intent)}
                  </span>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
                  <span className="rounded bg-surface-950 px-2 py-1 font-mono text-surface-400">{intent.branch}</span>
                  {intent.providerKind ? (
                    <span className="rounded bg-surface-950 px-2 py-1 text-surface-500">
                      {intent.providerKind}
                      {intent.providerRepo ? ` · ${intent.providerRepo}` : ""}
                    </span>
                  ) : null}
                  {intent.providerBranchUrl ? (
                    <a
                      href={intent.providerBranchUrl}
                      target="_blank"
                      rel="noreferrer"
                      className="rounded bg-surface-950 px-2 py-1 text-indigo-700 underline underline-offset-2 dark:text-indigo-300"
                    >
                      provider branch
                    </a>
                  ) : null}
                  {intent.reason ? <span className="text-surface-500">{shortText(intent.reason, 120)}</span> : null}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-surface-500">No relay-source branch handoffs yet.</p>
        )}
      </section>
    </div>
  );
}
