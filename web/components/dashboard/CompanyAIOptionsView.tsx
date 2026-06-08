"use client";

import { useEffect, useMemo, useState } from "react";
import { agentClient, type CompanyAIOptions, type TeamSummary } from "@/lib/agent-client";

const runnerChoices = ["opencode", "codex", "claude", "ollama"];
const workKindLabels: Record<string, string> = {
  appCode: "App code",
  erpFlow: "ERP flow",
  convex: "Convex",
  webUi: "Web UI",
  harnessCad: "Harness CAD",
  openScadCad: "OpenSCAD/CAD",
  robotTrial: "Robot trial",
  inspection: "Inspection",
};

function cloneOptions(options: CompanyAIOptions): CompanyAIOptions {
  return JSON.parse(JSON.stringify(options)) as CompanyAIOptions;
}

function boolPatch<T extends Record<string, boolean>>(obj: T, key: keyof T, value: boolean): T {
  return { ...obj, [key]: value };
}

export default function CompanyAIOptionsView() {
  const [teams, setTeams] = useState<TeamSummary[]>([]);
  const [teamId, setTeamId] = useState("");
  const [role, setRole] = useState("");
  const [canEdit, setCanEdit] = useState(false);
  const [options, setOptions] = useState<CompanyAIOptions | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      setMessage(null);
      try {
        const rows = await agentClient.listTeams();
        if (cancelled) return;
        setTeams(rows);
        const first = teamId || rows[0]?.teamId || "";
        setTeamId(first);
      } catch (err) {
        if (!cancelled) setMessage(err instanceof Error ? err.message : "Failed to load teams");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!teamId) {
      setOptions(null);
      return;
    }
    let cancelled = false;
    (async () => {
      setLoading(true);
      setMessage(null);
      try {
        const res = await agentClient.getCompanyAIOptions(teamId);
        if (cancelled) return;
        setOptions(res.options);
        setRole(res.role);
        setCanEdit(res.canEdit);
      } catch (err) {
        if (!cancelled) setMessage(err instanceof Error ? err.message : "Failed to load company AI options");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [teamId]);

  const selectedTeam = useMemo(() => teams.find((t) => t.teamId === teamId), [teams, teamId]);

  function update(fn: (next: CompanyAIOptions) => void) {
    setOptions((cur) => {
      if (!cur) return cur;
      const next = cloneOptions(cur);
      fn(next);
      return next;
    });
  }

  async function save() {
    if (!teamId || !options) return;
    setSaving(true);
    setMessage(null);
    try {
      const res = await agentClient.saveCompanyAIOptions(teamId, options);
      if (!res.ok) {
        setMessage(res.error || "Save failed");
        return;
      }
      setMessage("Saved company AI options.");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  if (loading && !options) {
    return <div className="p-6 text-sm text-surface-400">Loading company AI options...</div>;
  }

  if (!teams.length) {
    return (
      <div className="p-6">
        <h2 className="text-lg font-semibold text-surface-100">Company AI</h2>
        <p className="mt-2 text-sm text-surface-400">Create or join a team before configuring company AI runtimes.</p>
        {message ? <p className="mt-3 text-xs text-amber-700 dark:text-amber-300">{message}</p> : null}
      </div>
    );
  }

  if (!options) {
    return <div className="p-6 text-sm text-amber-700 dark:text-amber-300">{message || "No company AI options available."}</div>;
  }

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-surface-100">Company AI</h2>
          <p className="mt-1 text-sm text-surface-500">
            Configure the hidden Yaver runtime that Talos uses for company chat, MCP tools, runners, previews, and artifacts.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <select
            value={teamId}
            onChange={(e) => setTeamId(e.target.value)}
            className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 outline-none"
          >
            {teams.map((t) => (
              <option key={t.teamId} value={t.teamId}>{t.name || t.teamId}</option>
            ))}
          </select>
          <button
            onClick={save}
            disabled={!canEdit || saving}
            className="rounded-lg border border-cyan-400/40 bg-cyan-400/10 px-4 py-2 text-sm font-semibold text-cyan-800 dark:text-cyan-100 hover:bg-cyan-400/20 disabled:opacity-40"
          >
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
      </div>

      {message ? <div className="mb-4 rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-xs text-amber-700 dark:text-amber-200">{message}</div> : null}
      {!canEdit ? (
        <div className="mb-4 rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2 text-xs text-surface-400">
          You are {role || "a member"} of {selectedTeam?.name || teamId}. Only team admins can edit these options.
        </div>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <section className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-surface-100">Runtime</h3>
              <p className="mt-1 text-xs text-surface-500">Company-bound compute behind Talos Yaver mode.</p>
            </div>
            <label className="flex items-center gap-2 text-xs text-surface-300">
              <input
                type="checkbox"
                checked={options.enabled}
                disabled={!canEdit}
                onChange={(e) => update((next) => { next.enabled = e.target.checked; })}
              />
              Enabled
            </label>
          </div>
          <div className="mt-4 grid gap-3 sm:grid-cols-2">
            <label className="text-xs text-surface-500">
              Provider
              <select
                value={options.runtime.defaultProvider}
                disabled={!canEdit}
                onChange={(e) => update((next) => { next.runtime.defaultProvider = e.target.value as any; })}
                className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
              >
                {["hetzner", "aws", "gcp", "azure", "onprem", "byo-yaver-device"].map((p) => <option key={p} value={p}>{p}</option>)}
              </select>
            </label>
            <label className="text-xs text-surface-500">
              Mode
              <select
                value={options.runtime.mode}
                disabled={!canEdit}
                onChange={(e) => update((next) => { next.runtime.mode = e.target.value as any; })}
                className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
              >
                <option value="dedicated-compute">dedicated-compute</option>
                <option value="bring-your-own-yaver">bring-your-own-yaver</option>
                <option value="local-only">local-only</option>
              </select>
            </label>
            <label className="text-xs text-surface-500 sm:col-span-2">
              Default Yaver device id
              <input
                value={options.runtime.defaultDeviceId || ""}
                disabled={!canEdit}
                onChange={(e) => update((next) => { next.runtime.defaultDeviceId = e.target.value.trim() || undefined; })}
                placeholder="cloud-... or owned device id"
                className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
              />
            </label>
          </div>
        </section>

        <section className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
          <h3 className="text-sm font-semibold text-surface-100">Runners</h3>
          <p className="mt-1 text-xs text-surface-500">Allowed underlying coding/model tools on the runtime.</p>
          <div className="mt-4 flex flex-wrap gap-2">
            {runnerChoices.map((runner) => {
              const active = options.runners.allowedRunners.includes(runner);
              return (
                <button
                  key={runner}
                  disabled={!canEdit}
                  onClick={() => update((next) => {
                    const set = new Set(next.runners.allowedRunners);
                    if (set.has(runner)) set.delete(runner); else set.add(runner);
                    next.runners.allowedRunners = [...set];
                    if (!set.has(next.runners.defaultRunner)) next.runners.defaultRunner = [...set][0] || runner;
                  })}
                  className={`rounded-full border px-3 py-1 text-xs ${active ? "border-cyan-400/50 bg-cyan-400/10 text-cyan-800 dark:text-cyan-100" : "border-surface-700 text-surface-400"}`}
                >
                  {runner}
                </button>
              );
            })}
          </div>
          <div className="mt-4 grid gap-3 sm:grid-cols-2">
            <label className="text-xs text-surface-500">
              Default runner
              <select
                value={options.runners.defaultRunner}
                disabled={!canEdit}
                onChange={(e) => update((next) => { next.runners.defaultRunner = e.target.value; })}
                className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
              >
                {options.runners.allowedRunners.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </label>
            <label className="text-xs text-surface-500">
              Credential mode
              <select
                value={options.runners.credentialMode}
                disabled={!canEdit}
                onChange={(e) => update((next) => { next.runners.credentialMode = e.target.value as any; })}
                className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
              >
                <option value="user-auth-on-runtime">user-auth-on-runtime</option>
                <option value="company-api-key-on-runtime">company-api-key-on-runtime</option>
                <option value="local-model-on-runtime">local-model-on-runtime</option>
                <option value="external-onprem-endpoint">external-onprem-endpoint</option>
              </select>
            </label>
          </div>
          <label className="mt-4 flex items-center gap-2 text-xs text-surface-300">
            <input
              type="checkbox"
              checked={options.runners.allowUserOverride}
              disabled={!canEdit}
              onChange={(e) => update((next) => { next.runners.allowUserOverride = e.target.checked; })}
            />
            Allow users to choose among allowed runners
          </label>
        </section>

        <section className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
          <h3 className="text-sm font-semibold text-surface-100">Workflows</h3>
          <div className="mt-3 grid gap-2 sm:grid-cols-2">
            {Object.entries(options.workKinds).map(([key, value]) => (
              <label key={key} className="flex items-center gap-2 text-xs text-surface-300">
                <input
                  type="checkbox"
                  checked={value}
                  disabled={!canEdit}
                  onChange={(e) => update((next) => { next.workKinds = boolPatch(next.workKinds, key as keyof typeof next.workKinds, e.target.checked); })}
                />
                {workKindLabels[key] || key}
              </label>
            ))}
          </div>
        </section>

        <section className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
          <h3 className="text-sm font-semibold text-surface-100">Approvals</h3>
          <div className="mt-3 grid gap-2">
            {Object.entries(options.approvals).map(([key, value]) => (
              <label key={key} className="flex items-center gap-2 text-xs text-surface-300">
                <input
                  type="checkbox"
                  checked={value}
                  disabled={!canEdit || key === "requireApprovalForSecretsAccess"}
                  onChange={(e) => update((next) => { next.approvals = boolPatch(next.approvals, key as keyof typeof next.approvals, e.target.checked); })}
                />
                {key}
              </label>
            ))}
          </div>
        </section>

        <section className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
          <h3 className="text-sm font-semibold text-surface-100">MCP</h3>
          <label className="mt-3 block text-xs text-surface-500">
            Enabled servers
            <input
              value={options.mcp.enabledServers.join(", ")}
              disabled={!canEdit}
              onChange={(e) => update((next) => { next.mcp.enabledServers = e.target.value.split(",").map((s) => s.trim()).filter(Boolean); })}
              className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
            />
          </label>
          <label className="mt-3 block text-xs text-surface-500">
            Required servers
            <input
              value={options.mcp.requiredServers.join(", ")}
              disabled={!canEdit}
              onChange={(e) => update((next) => { next.mcp.requiredServers = e.target.value.split(",").map((s) => s.trim()).filter(Boolean); })}
              className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
            />
          </label>
        </section>

        <section className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
          <h3 className="text-sm font-semibold text-surface-100">Data Policy</h3>
          <div className="mt-3 grid gap-2">
            {(["allowCustomerDataInPrompts", "allowScreenshotsInPrompts", "allowTelemetryInPrompts", "redactPII"] as const).map((key) => (
              <label key={key} className="flex items-center gap-2 text-xs text-surface-300">
                <input
                  type="checkbox"
                  checked={options.dataPolicy[key]}
                  disabled={!canEdit}
                  onChange={(e) => update((next) => { next.dataPolicy[key] = e.target.checked; })}
                />
                {key}
              </label>
            ))}
          </div>
          <label className="mt-3 block text-xs text-surface-500">
            Retention days
            <input
              type="number"
              min={1}
              max={365}
              value={options.dataPolicy.retentionDays}
              disabled={!canEdit}
              onChange={(e) => update((next) => { next.dataPolicy.retentionDays = Math.max(1, Number(e.target.value || 1)); })}
              className="mt-1 w-32 rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-200"
            />
          </label>
        </section>
      </div>
    </div>
  );
}
