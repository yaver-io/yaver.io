"use client";

// ToolsView — dashboard tab that shows the connected machine's specs
// (RAM / CPU / disk / GPU) alongside an install catalogue so the user
// can one-click install ollama / aider / codex / claude-code / etc.
// onto their dev machine (or any paired peer) without touching a
// terminal. Progress streams live from /streams/install:<tool>.

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { agentClient, type CapabilitySnapshot, type IncidentEvent, type InfraSummary, type MachineOnboardingProviderStatus, type RunnerAuthStatusRow, type RunnerBrowserAuthSession } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type InstallEntry = { name: string; installed: boolean; description: string };

type Props = {
  /** Paired devices — used to populate the peer picker. Optional;
   *  when absent the view simply installs onto the currently-connected
   *  machine. */
  devices?: Device[];
};

const TOOL_META: Record<string, { emoji: string; tagline: string }> = {
  ollama: { emoji: "🦙", tagline: "Local LLM runtime — pulls models, serves them to aider + claude-code." },
  aider: { emoji: "🧑‍🔧", tagline: "Terminal pair-programmer. Powers the hybrid planner's implementer tier." },
  opencode: { emoji: "🪄", tagline: "Open-source coding agent, Claude-style UX." },
  "claude-code": { emoji: "🤖", tagline: "Anthropic's CLI agent — frontier-quality runner." },
  codex: { emoji: "🧠", tagline: "OpenAI Codex CLI — token-efficient daily driver." },
  hybrid: { emoji: "🪢", tagline: "Meta-install: aider + ollama + qwen2.5-coder:14b." },
  docker: { emoji: "🐳", tagline: "Containerise tasks — required for guest isolation + sandbox mode." },
  node: { emoji: "🟢", tagline: "Node.js — required for Expo / Vite / Next.js." },
  python: { emoji: "🐍", tagline: "Python 3 — ML tooling and some CLIs." },
  go: { emoji: "🐹", tagline: "Go toolchain — rebuild the agent / relay from source." },
  rust: { emoji: "🦀", tagline: "Rust toolchain — some runners + Hermes compiler." },
  git: { emoji: "🔀", tagline: "Version control — every scaffold depends on it." },
};

function fmtBytes(n?: number) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${u[i]}`;
}

function fmtUptime(s?: number) {
  if (!s) return "0m";
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}

export default function ToolsView({ devices = [] }: Props) {
  const [summary, setSummary] = useState<InfraSummary | null>(null);
  const [catalogue, setCatalogue] = useState<InstallEntry[]>([]);
  const [runnerAuthRows, setRunnerAuthRows] = useState<RunnerAuthStatusRow[]>([]);
  const [runnerCapabilitySnapshot, setRunnerCapabilitySnapshot] = useState<CapabilitySnapshot | null>(null);
  const [runnerIncidents, setRunnerIncidents] = useState<IncidentEvent[]>([]);
  const [onboardingRows, setOnboardingRows] = useState<MachineOnboardingProviderStatus[]>([]);
  const [target, setTarget] = useState<string | undefined>(undefined);
  const [installing, setInstalling] = useState<string | null>(null);
  const [savingRunnerAuth, setSavingRunnerAuth] = useState<string | null>(null);
  const [log, setLog] = useState<string[]>([]);
  const [result, setResult] = useState<{ tool: string; status: string } | null>(null);
  const [runnerAuthResult, setRunnerAuthResult] = useState<string | null>(null);
  const [onboardingResult, setOnboardingResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [authError, setAuthError] = useState<string | null>(null);
  const [browserAuthError, setBrowserAuthError] = useState<string | null>(null);
  const [onboardingError, setOnboardingError] = useState<string | null>(null);
  const [codexOpenAIKey, setCodexOpenAIKey] = useState("");
  const [claudeAnthropicKey, setClaudeAnthropicKey] = useState("");
  const [claudeAuthToken, setClaudeAuthToken] = useState("");
  const [claudeOAuthToken, setClaudeOAuthToken] = useState("");
  const [opencodeOpenAIKey, setOpencodeOpenAIKey] = useState("");
  const [opencodeAnthropicKey, setOpencodeAnthropicKey] = useState("");
  const [opencodeGLMKey, setOpencodeGLMKey] = useState("");
  const [opencodeZAIKey, setOpencodeZAIKey] = useState("");
  const [machineOpenAIKey, setMachineOpenAIKey] = useState("");
  const [machineGitHubToken, setMachineGitHubToken] = useState("");
  const [machineGitLabToken, setMachineGitLabToken] = useState("");
  const [machineGitLabHost, setMachineGitLabHost] = useState("gitlab.com");
  const [savingOnboarding, setSavingOnboarding] = useState(false);
  const [startingBrowserAuth, setStartingBrowserAuth] = useState<string | null>(null);
  const [browserAuthSession, setBrowserAuthSession] = useState<RunnerBrowserAuthSession | null>(null);
  const cancelStreamRef = useRef<(() => void) | null>(null);

  const peers = useMemo(
    () =>
      devices
        .filter((d) => d.online && d.deviceClass !== "edge-mobile")
        .map((d) => ({ id: d.id, name: d.name })),
    [devices],
  );

  const loadSummary = useCallback(async () => {
    try {
      setSummary(await agentClient.infraSummary(target));
    } catch {
      /* soft-fail */
    }
  }, [target]);

  const loadCatalogue = useCallback(async () => {
    try {
      setCatalogue(await agentClient.listInstallables(target));
    } catch {
      /* soft-fail */
    }
  }, [target]);

  const loadRunnerAuth = useCallback(async () => {
    try {
      const [rows, snapshot, incidents] = await Promise.all([
        agentClient.runnerAuthStatus(target),
        agentClient.capabilitySnapshot(),
        agentClient.incidents({ category: "runner_auth", limit: 8, device: target }),
      ]);
      setRunnerAuthRows(rows);
      setRunnerCapabilitySnapshot(snapshot);
      setRunnerIncidents(incidents);
    } catch {
      /* soft-fail */
    }
  }, [target]);

  const loadOnboarding = useCallback(async () => {
    try {
      setOnboardingRows(await agentClient.machineOnboardingStatus(target));
    } catch {
      /* soft-fail */
    }
  }, [target]);

  useEffect(() => {
    loadSummary();
    loadCatalogue();
    loadRunnerAuth();
    loadOnboarding();
    const i = setInterval(() => {
      loadSummary();
      loadCatalogue();
      loadRunnerAuth();
      loadOnboarding();
    }, 15_000);
    return () => {
      clearInterval(i);
      cancelStreamRef.current?.();
    };
  }, [loadSummary, loadCatalogue, loadRunnerAuth, loadOnboarding]);

  useEffect(() => {
    if (!browserAuthSession) return;
    if (browserAuthSession.status === "completed" || browserAuthSession.status === "failed" || browserAuthSession.status === "cancelled") {
      void loadRunnerAuth();
      return;
    }
    const id = window.setInterval(async () => {
      const res = await agentClient.runnerBrowserAuthStatus(browserAuthSession.id, target);
      if (res.ok && res.session) {
        setBrowserAuthSession(res.session);
        if (res.session.status === "completed" || res.session.status === "failed" || res.session.status === "cancelled") {
          void loadRunnerAuth();
        }
      }
    }, 2000);
    return () => window.clearInterval(id);
  }, [browserAuthSession, loadRunnerAuth, target]);

  async function runInstall(tool: string) {
    if (installing) return;
    setInstalling(tool);
    setLog([]);
    setResult(null);
    setError(null);
    const res = await agentClient.installTool(tool, target);
    if (!res.ok) {
      setError(res.error || "Install failed to start");
      setInstalling(null);
      return;
    }
    cancelStreamRef.current?.();
    cancelStreamRef.current = agentClient.streamLog(res.stream, (ev: any) => {
      if (ev.type === "line" && typeof ev.text === "string") {
        setLog((prev) => [...prev.slice(-299), ev.text]);
      } else if (ev.type === "result") {
        setResult({ tool, status: ev.status || "" });
        setInstalling(null);
        if (ev.status !== "ok" && ev.error) setError(ev.error);
        void loadCatalogue();
        void loadSummary();
        void loadRunnerAuth();
        void loadOnboarding();
      }
    });
  }

  async function saveRunnerAuth(runner: "claude" | "codex" | "opencode") {
    if (savingRunnerAuth) return;
    setSavingRunnerAuth(runner);
    setAuthError(null);
    setRunnerAuthResult(null);
    const res = await agentClient.runnerAuthSet(
      {
        runner,
        openaiApiKey: runner === "codex" ? codexOpenAIKey : opencodeOpenAIKey,
        anthropicApiKey: runner === "claude" ? claudeAnthropicKey : opencodeAnthropicKey,
        anthropicAuthToken: runner === "claude" ? claudeAuthToken : undefined,
        claudeCodeOauthToken: runner === "claude" ? claudeOAuthToken : undefined,
        glmApiKey: runner === "opencode" ? opencodeGLMKey : undefined,
        zaiApiKey: runner === "opencode" ? opencodeZAIKey : undefined,
      },
      target,
    );
    setSavingRunnerAuth(null);
    if (!res.ok) {
      setAuthError(res.error || "Runner auth update failed");
      return;
    }
    setRunnerAuthRows(res.runners);
    setRunnerAuthResult(`${labelForRunner(runner)} auth saved`);
  }

  async function startBrowserAuth(runner: "claude" | "codex") {
    if (startingBrowserAuth) return;
    setStartingBrowserAuth(runner);
    setBrowserAuthError(null);
    const res = await agentClient.runnerBrowserAuthStart({ runner }, target);
    setStartingBrowserAuth(null);
    if (!res.ok || !res.session) {
      setBrowserAuthError(res.error || "Could not start browser auth.");
      return;
    }
    setBrowserAuthSession(res.session);
  }

  async function cancelBrowserAuth() {
    if (!browserAuthSession) return;
    const res = await agentClient.runnerBrowserAuthCancel(browserAuthSession.id, target);
    if (res.ok && res.session) {
      setBrowserAuthSession(res.session);
    }
  }

  async function saveMachineOnboarding() {
    if (savingOnboarding) return;
    setSavingOnboarding(true);
    setOnboardingError(null);
    setOnboardingResult(null);
    const res = await agentClient.machineOnboardingApply(
      {
        openaiApiKey: machineOpenAIKey,
        githubToken: machineGitHubToken,
        gitlabToken: machineGitLabToken,
        gitlabHost: machineGitLabHost,
        applyClone: true,
        applyCiToken: true,
        notes: "Saved from Yaver web dashboard.",
      },
      target,
    );
    setSavingOnboarding(false);
    if (!res.ok) {
      setOnboardingError(res.error || "Machine onboarding update failed");
      return;
    }
    setOnboardingRows(res.providers);
    setOnboardingResult(res.applied.length > 0 ? `Applied ${res.applied.join(", ")}` : "Nothing changed");
  }

  const metrics = summary?.metrics;

  const sortedCatalogue = useMemo(
    () => [...catalogue].sort((a, b) => {
      // Missing tools first — the whole point of this view.
      if (a.installed !== b.installed) return a.installed ? 1 : -1;
      return a.name.localeCompare(b.name);
    }),
    [catalogue],
  );

  return (
    <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-surface-50">Tools &amp; Machine</h2>
        <p className="text-sm text-surface-400 mt-1">
          See what this dev machine is running on, then one-click install coding agents and local
          model runtimes without opening a terminal.
        </p>
      </div>

      {peers.length > 0 && (
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => setTarget(undefined)}
            className={`rounded-full px-3 py-1.5 text-xs font-semibold border ${
              !target
                ? "bg-indigo-500/15 text-indigo-300 border-indigo-500/40"
                : "bg-surface-900 text-surface-300 border-surface-800 hover:border-surface-700"
            }`}
          >
            This machine
          </button>
          {peers.map((p) => (
            <button
              key={p.id}
              onClick={() => setTarget(p.id)}
              className={`rounded-full px-3 py-1.5 text-xs font-semibold border ${
                target === p.id
                  ? "bg-indigo-500/15 text-indigo-300 border-indigo-500/40"
                  : "bg-surface-900 text-surface-300 border-surface-800 hover:border-surface-700"
              }`}
            >
              {p.name}
            </button>
          ))}
        </div>
      )}

      {summary && (
        <section className="rounded-2xl border border-surface-800 bg-surface-900/50 p-5">
          <div className="flex items-center gap-3 mb-4">
            <div
              className={`w-2.5 h-2.5 rounded-full ${
                summary.machine.isOnline ? "bg-emerald-400" : "bg-red-400"
              }`}
            />
            <h3 className="text-lg font-semibold text-surface-50">{summary.machine.name}</h3>
            <span className="text-xs text-surface-400">
              {summary.machine.platform}
              {summary.machine.arch ? ` · ${summary.machine.arch}` : ""}
            </span>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <Metric label="CPU" value={`${(metrics?.cpuPct || 0).toFixed(1)}%`} sub={`${metrics?.cores || 0} cores`} />
            <Metric
              label="RAM"
              value={`${(metrics?.ramPct || 0).toFixed(0)}%`}
              sub={`${fmtBytes(metrics?.ramUsed)} / ${fmtBytes(metrics?.ramTotal)}`}
            />
            <Metric
              label="Disk"
              value={`${(metrics?.diskPct || 0).toFixed(0)}%`}
              sub={`${fmtBytes(metrics?.diskUsed)} / ${fmtBytes(metrics?.diskTotal)}`}
            />
            <Metric label="Uptime" value={fmtUptime(metrics?.uptime)} sub={metrics?.hostname || summary.machine.deviceId} />
          </div>
        </section>
      )}

      <section>
        <h3 className="text-sm font-semibold text-surface-300 mb-3">Remote onboarding</h3>
        <div className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4 space-y-4">
          <p className="text-sm text-surface-400">
            Configure OpenAI, GitHub, and GitLab on the selected machine in one pass. GitHub and GitLab write both clone credentials and CI/deploy vault tokens.
          </p>
          <div className="grid gap-3 md:grid-cols-2">
            <SecretField label="OpenAI API key" value={machineOpenAIKey} onChange={setMachineOpenAIKey} placeholder="sk-..." />
            <SecretField label="GitHub token" value={machineGitHubToken} onChange={setMachineGitHubToken} placeholder="ghp_..." />
            <SecretField label="GitLab token" value={machineGitLabToken} onChange={setMachineGitLabToken} placeholder="glpat-..." />
            <SecretField label="GitLab host" value={machineGitLabHost} onChange={setMachineGitLabHost} placeholder="gitlab.com" secret={false} />
          </div>
          <div className="flex items-center gap-3">
            <button
              onClick={saveMachineOnboarding}
              disabled={savingOnboarding}
              className="rounded-xl bg-emerald-500 px-4 py-2 text-sm font-semibold text-slate-950 disabled:opacity-50"
            >
              {savingOnboarding ? "Applying..." : "Apply Onboarding"}
            </button>
            {onboardingResult && <span className="text-sm text-emerald-300">{onboardingResult}</span>}
            {onboardingError && <span className="text-sm text-rose-300">{onboardingError}</span>}
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            {onboardingRows.map((row) => (
              <div key={row.id} className="rounded-xl border border-surface-800 bg-surface-950/60 p-3">
                <div className="flex items-center justify-between gap-3">
                  <span className="font-semibold text-surface-50">{row.name}</span>
                  <span className={`text-xs font-semibold ${row.ready ? "text-emerald-300" : row.configured ? "text-amber-300" : "text-surface-500"}`}>
                    {row.ready ? "Ready" : row.configured ? "Partial" : "Missing"}
                  </span>
                </div>
                <p className="mt-2 text-xs text-surface-400">{row.detail || row.warning || "No status yet"}</p>
                {(row.host || row.username) && (
                  <p className="mt-2 text-[11px] text-surface-500">
                    {[row.host, row.username].filter(Boolean).join(" · ")}
                  </p>
                )}
              </div>
            ))}
          </div>
        </div>
      </section>

      <section>
        <h3 className="text-sm font-semibold text-surface-300 mb-3">Install catalogue</h3>
        {catalogue.length === 0 ? (
          <p className="text-sm text-surface-500">
            No install targets advertised. The connected agent may be below v1.98.0.
          </p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            {sortedCatalogue.map((entry) => {
              const meta = TOOL_META[entry.name] ?? { emoji: "⚙️", tagline: entry.description || "" };
              const isBusy = installing === entry.name;
              return (
                <div
                  key={entry.name}
                  className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4 flex flex-col gap-3"
                >
                  <div className="flex gap-3">
                    <div className="text-2xl leading-none">{meta.emoji}</div>
                    <div className="flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-surface-50">{entry.name}</span>
                        <span
                          className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold ${
                            entry.installed
                              ? "bg-emerald-500/15 text-emerald-300"
                              : "bg-surface-800 text-surface-400"
                          }`}
                        >
                          {entry.installed ? "INSTALLED" : "NOT INSTALLED"}
                        </span>
                      </div>
                      <p className="text-xs text-surface-400 mt-1 leading-relaxed">
                        {meta.tagline || entry.description}
                      </p>
                    </div>
                  </div>
                  <button
                    onClick={() => void runInstall(entry.name)}
                    disabled={!!installing}
                    className={`rounded-lg px-3 py-2 text-xs font-semibold transition ${
                      entry.installed
                        ? "border border-surface-700 text-surface-300 hover:border-surface-600"
                        : "bg-indigo-500 text-white hover:bg-indigo-400"
                    } ${installing && !isBusy ? "opacity-50 cursor-not-allowed" : ""}`}
                  >
                    {isBusy ? "Installing…" : entry.installed ? "Reinstall / update" : "Install"}
                  </button>
                </div>
              );
            })}
          </div>
        )}
      </section>

      <section>
        <div className="flex items-start justify-between gap-4 mb-3">
          <div>
            <h3 className="text-sm font-semibold text-surface-300">Runner auth</h3>
            <p className="text-xs text-surface-500 mt-1">
              Push API keys or auth tokens into the selected machine so Claude Code, Codex, and OpenCode are usable headlessly.
            </p>
          </div>
          <button
            onClick={() => void loadRunnerAuth()}
            className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-300 hover:border-surface-600"
          >
            Refresh
          </button>
        </div>
        <div className="grid gap-3">
          {runnerIncidents.length > 0 && (
            <div className="rounded-xl border border-rose-500/30 bg-rose-500/10 p-3">
              <div className="text-sm font-semibold text-rose-200">Runner blockers</div>
              <div className="mt-2 space-y-2">
                {runnerIncidents.slice(0, 3).map((incident) => (
                  <p key={incident.id} className="text-xs text-rose-100">
                    <span className="font-semibold">{incident.title}.</span> {incident.userMessage}
                  </p>
                ))}
              </div>
            </div>
          )}
          <RunnerAuthCard
            title="Codex"
            status={runnerAuthRows.find((row) => row.id === "codex")}
            capability={runnerCapabilitySnapshot?.targets?.["runner-codex"]}
            busy={savingRunnerAuth === "codex"}
            onSave={() => void saveRunnerAuth("codex")}
            secondaryAction={
              <button
                onClick={() => void startBrowserAuth("codex")}
                disabled={startingBrowserAuth !== null}
                className={`rounded-lg px-3 py-2 text-xs font-semibold ${
                  startingBrowserAuth === "codex"
                    ? "cursor-wait bg-surface-800 text-surface-400"
                    : "border border-surface-700 text-surface-300 hover:border-surface-600"
                }`}
              >
                {startingBrowserAuth === "codex" ? "Starting…" : "Browser sign-in"}
              </button>
            }
          >
            <div className="space-y-3">
              <p className="text-xs text-surface-500">
                Headless-friendly path: the remote box generates a device-auth link and one-time code, and you complete login from this browser.
              </p>
              <SecretInput label="OpenAI API key" value={codexOpenAIKey} onChange={setCodexOpenAIKey} placeholder="sk-..." />
            </div>
          </RunnerAuthCard>

          <RunnerAuthCard
            title="Claude Code"
            status={runnerAuthRows.find((row) => row.id === "claude")}
            capability={runnerCapabilitySnapshot?.targets?.["runner-claude"]}
            busy={savingRunnerAuth === "claude"}
            onSave={() => void saveRunnerAuth("claude")}
            secondaryAction={
              <button
                onClick={() => void startBrowserAuth("claude")}
                disabled={startingBrowserAuth !== null}
                className={`rounded-lg px-3 py-2 text-xs font-semibold ${
                  startingBrowserAuth === "claude"
                    ? "cursor-wait bg-surface-800 text-surface-400"
                    : "border border-surface-700 text-surface-300 hover:border-surface-600"
                }`}
              >
                {startingBrowserAuth === "claude" ? "Starting…" : "Browser sign-in"}
              </button>
            }
          >
            <div className="space-y-3">
              <p className="text-xs text-surface-500">
                Uses Claude Code&apos;s native browser login flow on the remote box and surfaces the generated auth URL here.
              </p>
              <div className="grid gap-3 md:grid-cols-3">
              <SecretInput label="Anthropic API key" value={claudeAnthropicKey} onChange={setClaudeAnthropicKey} placeholder="sk-ant-..." />
              <SecretInput label="Anthropic auth token" value={claudeAuthToken} onChange={setClaudeAuthToken} placeholder="oauth/session token" />
              <SecretInput label="Claude Code OAuth token" value={claudeOAuthToken} onChange={setClaudeOAuthToken} placeholder="oauth token" />
              </div>
            </div>
          </RunnerAuthCard>

          <RunnerAuthCard
            title="OpenCode"
            status={runnerAuthRows.find((row) => row.id === "opencode")}
            capability={runnerCapabilitySnapshot?.targets?.["runner-opencode"]}
            busy={savingRunnerAuth === "opencode"}
            onSave={() => void saveRunnerAuth("opencode")}
          >
            <div className="grid gap-3 md:grid-cols-2">
              <SecretInput label="OpenAI API key" value={opencodeOpenAIKey} onChange={setOpencodeOpenAIKey} placeholder="sk-..." />
              <SecretInput label="Anthropic API key" value={opencodeAnthropicKey} onChange={setOpencodeAnthropicKey} placeholder="sk-ant-..." />
              <SecretInput label="GLM API key" value={opencodeGLMKey} onChange={setOpencodeGLMKey} placeholder="zai_... or glm_..." />
              <SecretInput label="ZAI API key" value={opencodeZAIKey} onChange={setOpencodeZAIKey} placeholder="zai_..." />
            </div>
          </RunnerAuthCard>
        </div>
        {(runnerAuthResult || authError || browserAuthError) && (
          <div className={`mt-3 rounded-xl border px-4 py-3 text-sm ${
            authError || browserAuthError
              ? "border-red-500/30 bg-red-500/10 text-red-300"
              : "border-emerald-500/30 bg-emerald-500/10 text-emerald-300"
          }`}>
            {authError || browserAuthError || runnerAuthResult}
          </div>
        )}
      </section>

      {browserAuthSession && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
          <div className="w-full max-w-2xl rounded-2xl border border-surface-700 bg-surface-950 p-5 shadow-2xl">
            <div className="flex items-start justify-between gap-4">
              <div>
                <h3 className="text-lg font-semibold text-surface-50">
                  {browserAuthSession.runner === "codex" ? "Codex" : "Claude Code"} browser sign-in
                </h3>
                <p className="mt-1 text-sm text-surface-400">
                  The remote machine started a native auth flow. Open the generated link in a separate tab, finish login, and this dialog will update automatically.
                </p>
              </div>
              <button
                onClick={() => setBrowserAuthSession(null)}
                className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-300 hover:border-surface-600"
              >
                Close
              </button>
            </div>

            <div className="mt-4 grid gap-4">
              <div className="rounded-xl border border-surface-800 bg-surface-900/60 p-4">
                <div className="flex items-center gap-2">
                  <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold ${
                    browserAuthSession.status === "completed"
                      ? "bg-emerald-500/15 text-emerald-300"
                      : browserAuthSession.status === "failed"
                        ? "bg-red-500/15 text-red-300"
                        : browserAuthSession.status === "cancelled"
                          ? "bg-surface-800 text-surface-400"
                          : "bg-amber-500/15 text-amber-300"
                  }`}>
                    {browserAuthSession.status.toUpperCase()}
                  </span>
                  <span className="text-xs text-surface-500">{browserAuthSession.method}</span>
                </div>
                <p className="mt-2 text-sm text-surface-300">{browserAuthSession.detail || "Waiting for the remote CLI to emit the auth link..."}</p>
                {browserAuthSession.error ? (
                  <p className="mt-2 text-sm text-red-300">{browserAuthSession.error}</p>
                ) : null}
                {browserAuthSession.authConfigured ? (
                  <p className="mt-2 text-sm text-emerald-300">
                    Remote auth detected{browserAuthSession.authSource ? ` via ${browserAuthSession.authSource}` : ""}.
                  </p>
                ) : null}
              </div>

              {browserAuthSession.openUrl ? (
                <div className="rounded-xl border border-surface-800 bg-surface-900/60 p-4">
                  <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Auth link</div>
                  <div className="rounded-lg border border-surface-800 bg-surface-950 px-3 py-2 text-xs font-mono text-surface-200 break-all">
                    {browserAuthSession.openUrl}
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <button
                      onClick={() => window.open(browserAuthSession.openUrl, "_blank", "noopener,noreferrer")}
                      className="rounded-lg bg-indigo-500 px-3 py-2 text-xs font-semibold text-white hover:bg-indigo-400"
                    >
                      Open auth tab
                    </button>
                    <button
                      onClick={() => navigator.clipboard.writeText(browserAuthSession.openUrl || "")}
                      className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-300 hover:border-surface-600"
                    >
                      Copy link
                    </button>
                  </div>
                </div>
              ) : null}

              {browserAuthSession.code ? (
                <div className="rounded-xl border border-surface-800 bg-surface-900/60 p-4">
                  <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">One-time code</div>
                  <div className="rounded-lg border border-surface-800 bg-surface-950 px-3 py-2 text-lg font-semibold tracking-[0.2em] text-surface-100">
                    {browserAuthSession.code}
                  </div>
                  <button
                    onClick={() => navigator.clipboard.writeText(browserAuthSession.code || "")}
                    className="mt-3 rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-300 hover:border-surface-600"
                  >
                    Copy code
                  </button>
                </div>
              ) : null}

              <div className="flex flex-wrap gap-2">
                <button
                  onClick={() => void loadRunnerAuth()}
                  className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-300 hover:border-surface-600"
                >
                  Refresh runner status
                </button>
                {browserAuthSession.status !== "completed" && browserAuthSession.status !== "failed" && browserAuthSession.status !== "cancelled" ? (
                  <button
                    onClick={() => void cancelBrowserAuth()}
                    className="rounded-lg border border-red-500/30 px-3 py-2 text-xs font-semibold text-red-300 hover:bg-red-500/10"
                  >
                    Cancel remote auth
                  </button>
                ) : null}
              </div>
            </div>
          </div>
        </div>
      )}

      {(log.length > 0 || error) && (
        <section className="rounded-2xl border border-surface-800 bg-black p-4">
          <div className="text-[10px] font-bold text-surface-400 mb-2">
            {installing
              ? `INSTALLING · ${installing}`
              : result
                ? `LAST RUN · ${result.tool} · ${result.status}`
                : error
                  ? "ERROR"
                  : "LOG"}
          </div>
          {error && <div className="text-xs text-red-400 mb-2">{error}</div>}
          <div className="font-mono text-[11px] text-surface-200 leading-5 max-h-64 overflow-y-auto whitespace-pre-wrap">
            {log.slice(-300).map((line, i) => (
              <div key={i}>{line}</div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function SecretField({
  label,
  value,
  onChange,
  placeholder,
  secret = true,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  secret?: boolean;
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">{label}</span>
      <input
        type={secret ? "password" : "text"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="rounded-xl border border-surface-800 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none placeholder:text-surface-600"
      />
    </label>
  );
}

function labelForRunner(runner: "claude" | "codex" | "opencode") {
  if (runner === "claude") return "Claude Code";
  if (runner === "codex") return "Codex";
  return "OpenCode";
}

function runnerStatusTone(status?: RunnerAuthStatusRow) {
  if (!status?.installed) return "bg-surface-800 text-surface-400";
  if (status.ready) return "bg-emerald-500/15 text-emerald-300";
  return "bg-amber-500/15 text-amber-300";
}

function RunnerAuthCard({
  title,
  status,
  capability,
  busy,
  onSave,
  secondaryAction,
  children,
}: {
  title: string;
  status?: RunnerAuthStatusRow;
  capability?: CapabilitySnapshot["targets"][string];
  busy: boolean;
  onSave: () => void;
  secondaryAction?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <span className="font-semibold text-surface-50">{title}</span>
            <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold ${runnerStatusTone(status)}`}>
              {!status?.installed ? "NOT INSTALLED" : status.ready ? "READY" : "NEEDS AUTH"}
            </span>
          </div>
          <p className="mt-1 text-xs text-surface-400">
            {capability?.reason || status?.detail || "No status yet."}
          </p>
        </div>
        <button
          onClick={onSave}
          disabled={busy}
          className={`rounded-lg px-3 py-2 text-xs font-semibold ${
            busy
              ? "cursor-wait bg-surface-800 text-surface-400"
              : "bg-indigo-500 text-white hover:bg-indigo-400"
          }`}
        >
          {busy ? "Saving…" : "Save auth"}
        </button>
        {secondaryAction}
      </div>
      <div className="mt-4">{children}</div>
    </div>
  );
}

function SecretInput({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
}) {
  return (
    <label className="block">
      <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-surface-500">{label}</div>
      <input
        type="password"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className="w-full rounded-xl border border-surface-800 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none placeholder:text-surface-600 focus:border-indigo-500/60"
      />
    </label>
  );
}

function Metric({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-xl border border-surface-800 bg-surface-900/60 p-3">
      <div className="text-[10px] font-bold uppercase tracking-wider text-surface-500">{label}</div>
      <div className="text-2xl font-semibold text-surface-50 mt-1">{value}</div>
      {sub && <div className="text-[11px] text-surface-500 mt-1 truncate">{sub}</div>}
    </div>
  );
}
