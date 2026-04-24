"use client";

import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { agentClient, type ConnectionState, type GitCommitRow, type GitProviderStatusRow, type GitRemoteRepo, type GitStatusRow, type Runner, type Task } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import PreviewPane from "./PreviewPane";

type SectionKey = "projects" | "runner" | "actions" | "repo" | "secrets" | "providers" | "sessions";

type SectionState = { visible: boolean; open: boolean };

const SECTION_ORDER: SectionKey[] = ["projects", "runner", "actions", "repo", "secrets", "providers", "sessions"];

const SECTION_LABELS: Record<SectionKey, string> = {
  projects: "Projects",
  runner: "Coding Agent",
  actions: "Actions",
  repo: "Repo",
  secrets: "Project Secrets",
  providers: "Git Providers",
  sessions: "Sessions",
};

const SECTION_DEFAULTS: Record<SectionKey, SectionState> = {
  projects: { visible: true, open: true },
  runner: { visible: true, open: true },
  actions: { visible: true, open: true },
  repo: { visible: true, open: false },
  secrets: { visible: false, open: false },
  providers: { visible: false, open: false },
  sessions: { visible: true, open: true },
};

const SECTION_STORAGE_KEY = "yaver_vibe_sections_v1";

function loadSectionState(): Record<SectionKey, SectionState> {
  if (typeof window === "undefined") return { ...SECTION_DEFAULTS };
  try {
    const raw = window.localStorage.getItem(SECTION_STORAGE_KEY);
    if (!raw) return { ...SECTION_DEFAULTS };
    const parsed = JSON.parse(raw) as Partial<Record<SectionKey, SectionState>>;
    const next = { ...SECTION_DEFAULTS };
    for (const key of SECTION_ORDER) {
      if (parsed[key]) next[key] = { ...next[key], ...parsed[key] };
    }
    return next;
  } catch {
    return { ...SECTION_DEFAULTS };
  }
}

type Project = {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  tags?: string[];
};

type PreviewTarget = {
  id: string;
  name: string;
  deviceClass?: string;
  edgeProfile?: {
    supportsLocalInference: boolean;
    maxModelClass: "none" | "tiny" | "small" | "medium";
  };
};

type DeployPreviewSummary = {
  warnings?: string[];
  lastDeploy?: {
    target?: string;
    status?: string;
    startedAt?: string;
    finishedAt?: string;
  };
};

export default function VibeCodingView({
  devices,
  connectedDevice,
  connState,
  onSelectDevice,
  mobileWorkers,
  selectedPreviewTarget,
  onSelectPreviewTarget,
}: {
  devices: Device[];
  connectedDevice: Device | null;
  connState: ConnectionState;
  onSelectDevice: (device: Device) => Promise<void> | void;
  mobileWorkers: PreviewTarget[];
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
}) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [selectedProjectPath, setSelectedProjectPath] = useState("");
  const [selectedRunner, setSelectedRunner] = useState("");
  const [taskList, setTaskList] = useState<Task[]>([]);
  const [activeTaskId, setActiveTaskId] = useState("");
  const [composer, setComposer] = useState("");
  const [draftTitle, setDraftTitle] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);
  const [deployTargets, setDeployTargets] = useState<Array<{ id: string; name: string }>>([]);
  const [deployPreviewSummary, setDeployPreviewSummary] = useState<DeployPreviewSummary | null>(null);
  const [gitStatus, setGitStatus] = useState<GitStatusRow | null>(null);
  const [gitCommits, setGitCommits] = useState<GitCommitRow[]>([]);
  const [gitCommitMessage, setGitCommitMessage] = useState("");
  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [repoBrowserHost, setRepoBrowserHost] = useState<string | null>(null);
  const [providerRepos, setProviderRepos] = useState<GitRemoteRepo[]>([]);
  const [providerSearch, setProviderSearch] = useState("");
  const [providerToken, setProviderToken] = useState("");
  const [manualProvider, setManualProvider] = useState<"github" | "gitlab" | null>(null);
  const [projectSecrets, setProjectSecrets] = useState<Record<string, string>>({});
  const [savingSecretKey, setSavingSecretKey] = useState<string | null>(null);
  const [devStatus, setDevStatus] = useState<{
    running: boolean;
    framework?: string;
    workDir?: string;
    targetDeviceName?: string;
  } | null>(null);
  const [streamedOutput, setStreamedOutput] = useState("");
  const [sections, setSections] = useState<Record<SectionKey, SectionState>>(() => loadSectionState());
  const [customizeOpen, setCustomizeOpen] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(SECTION_STORAGE_KEY, JSON.stringify(sections));
    } catch {}
  }, [sections]);

  const toggleSectionOpen = (key: SectionKey) =>
    setSections((prev) => ({ ...prev, [key]: { ...prev[key], open: !prev[key].open } }));

  const toggleSectionVisible = (key: SectionKey) =>
    setSections((prev) => ({ ...prev, [key]: { ...prev[key], visible: !prev[key].visible } }));

  const resetSections = () => setSections({ ...SECTION_DEFAULTS });

  const connected = connState === "connected" && !!connectedDevice;
  const visibleDevices = useMemo(() => devices, [devices]);
  const selectedProject = useMemo(
    () => projects.find((project) => project.path === selectedProjectPath) || null,
    [projects, selectedProjectPath],
  );
  const activeTask = useMemo(
    () => taskList.find((task) => task.id === activeTaskId) || taskList[0] || null,
    [taskList, activeTaskId],
  );

  useEffect(() => {
    if (!connected) {
      setProjects([]);
      setRunners([]);
      setTaskList([]);
      setActiveTaskId("");
      setDeployTargets([]);
      return;
    }

    let cancelled = false;
    const refresh = async () => {
      try {
        const [projectRows, runnerRows, tasks, preview, currentDevStatus, git, commits, gitProviders] = await Promise.all([
          agentClient.listProjects().catch(() => []),
          agentClient.getRunners().catch(() => []),
          agentClient.listTasks(12).catch(() => []),
          selectedProjectPath ? agentClient.deployPreview(selectedProjectPath).catch(() => null) : Promise.resolve(null),
          agentClient.getDevServerStatus().catch(() => null),
          selectedProjectPath ? agentClient.gitStatus(selectedProjectPath).catch(() => null) : Promise.resolve(null),
          selectedProjectPath ? agentClient.gitLog(selectedProjectPath, 8).catch(() => []) : Promise.resolve([]),
          agentClient.gitProviderStatus().catch(() => []),
        ]);
        if (cancelled) return;
        setProjects(projectRows);
        setRunners((runnerRows || []).filter((runner) => runner.installed));
        setTaskList(tasks || []);
        setDevStatus(currentDevStatus);
        setGitStatus(git ?? null);
        setGitCommits(commits || []);
        setProviders(gitProviders || []);
        const targets = Array.isArray(preview?.targets) ? preview.targets : [];
        setDeployTargets(targets.map((target: any) => ({ id: String(target.id), name: String(target.name) })));
        setDeployPreviewSummary(
          preview
            ? {
                warnings: Array.isArray(preview.warnings) ? preview.warnings : [],
                lastDeploy: preview.lastDeploy,
              }
            : null,
        );

        if (!selectedProjectPath && projectRows.length > 0) {
          setSelectedProjectPath(projectRows[0].path);
        }
        if (!selectedRunner) {
          const preferred =
            runnerRows.find((runner) => runner.active) ||
            runnerRows.find((runner) => runner.id === "codex") ||
            runnerRows.find((runner) => runner.id === "claude") ||
            runnerRows[0];
          if (preferred) setSelectedRunner(preferred.id);
        }
        if (!activeTaskId && tasks.length > 0) {
          setActiveTaskId(tasks[0].id);
        }
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };

    void refresh();
    const interval = setInterval(refresh, 4000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [connected, connectedDevice?.id, selectedProjectPath, selectedRunner, activeTaskId, refreshNonce]);

  useEffect(() => {
    if (!activeTask) {
      setStreamedOutput("");
      return;
    }
    setStreamedOutput(activeTask.output.join("\n"));
    const stop = agentClient.streamTaskOutput(activeTask.id, (chunk) => {
      setStreamedOutput((prev) => prev + extractOutputText(chunk));
    });
    return stop;
  }, [activeTask?.id]);

  useEffect(() => {
    if (!selectedProject) {
      setProjectSecrets({});
      return;
    }
    let cancelled = false;
    const loadSecrets = async () => {
      const next: Record<string, string> = {};
      for (const secret of PROJECT_SECRET_TEMPLATES) {
        try {
          const entry = await agentClient.vaultGet(projectVaultEntryName(selectedProject.path, secret.id));
          next[secret.id] = entry.value;
        } catch {
          next[secret.id] = "";
        }
      }
      if (!cancelled) setProjectSecrets(next);
    };
    void loadSecrets();
    return () => {
      cancelled = true;
    };
  }, [selectedProject?.path]);

  async function startChatTask() {
    if (!selectedProject || !composer.trim()) {
      setBusy("Pick a project and enter a prompt.");
      return;
    }
    setBusy("Starting coding task…");
    const title = draftTitle.trim() || summarizeTitle(composer, selectedProject.name);
    const task = await agentClient.createTask({
      title,
      description: buildVibeTaskPrompt({
        project: selectedProject,
        prompt: composer.trim(),
        gitStatus,
        deployTargets,
      }),
      runner: selectedRunner || undefined,
      projectName: selectedProject.name,
      workDir: selectedProject.path,
    });
    setComposer("");
    setDraftTitle("");
    setActiveTaskId(task.id);
    setBusy(`Started ${task.title}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function continueChatTask() {
    if (!activeTask || !composer.trim()) return;
    setBusy(`Continuing ${activeTask.title}…`);
    await agentClient.continueTask(
      activeTask.id,
      buildVibeContinuationPrompt({
        project: selectedProject,
        prompt: composer.trim(),
        gitStatus,
        deployTargets,
      }),
    );
    setComposer("");
    setBusy(`Continued ${activeTask.title}.`);
  }

  async function startPreview() {
    if (!selectedProject) return;
    await agentClient.startDevServer({
      framework: selectedProject.framework || "",
      workDir: selectedProject.path,
      targetDeviceId: selectedPreviewTarget?.id,
      targetDeviceName: selectedPreviewTarget?.name,
      targetDeviceClass: selectedPreviewTarget?.deviceClass,
    });
    setBusy(`Started preview for ${selectedProject.name}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function deploy(targetId?: string) {
    if (!selectedProject) return;
    setBusy(`Deploying ${selectedProject.name}…`);
    const result = await agentClient.deployRun(selectedProject.path);
    const deployName = result?.target || targetId || "deploy";
    setBusy(`Triggered ${deployName}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function autoDetectProviders() {
    setBusy("Detecting git providers from the machine…");
    const detected = await agentClient.gitProviderDetect();
    setProviders(await agentClient.gitProviderStatus());
    setBusy(detected.length > 0 ? `Detected ${detected.map((item) => item.provider).join(", ")}.` : "No git providers detected.");
  }

  async function saveProviderToken() {
    if (!manualProvider || !providerToken.trim()) return;
    setBusy(`Saving ${manualProvider} token to machine vault…`);
    const result = await agentClient.gitProviderSetup({ provider: manualProvider, token: providerToken.trim() });
    if (!result.ok) {
      setBusy(result.error || "Could not save provider token.");
      return;
    }
    setProviderToken("");
    setManualProvider(null);
    setProviders(await agentClient.gitProviderStatus());
    setBusy(`Connected ${result.provider || manualProvider} as ${result.username || "user"}.`);
  }

  async function toggleProviderRepos(host: string) {
    if (repoBrowserHost === host) {
      setRepoBrowserHost(null);
      setProviderRepos([]);
      return;
    }
    setRepoBrowserHost(host);
    setBusy(`Loading repos from ${host}…`);
    const repos = await agentClient.gitProviderRepos(host);
    setProviderRepos(repos);
    setBusy(`Loaded ${repos.length} repos from ${host}.`);
  }

  async function cloneRemoteRepo(repo: GitRemoteRepo) {
    const url = repo.sshUrl || repo.cloneUrl;
    if (!url) return;
    setBusy(`Cloning ${repo.fullName}…`);
    const result = await agentClient.cloneRepo(url);
    if (!result?.ok) {
      setBusy(result?.error || `Could not clone ${repo.fullName}.`);
      return;
    }
    const nextProjects = await agentClient.listProjects().catch(() => []);
    if (nextProjects.length > 0) {
      setProjects(nextProjects);
      if (result.path && nextProjects.some((project: Project) => project.path === result.path)) {
        setSelectedProjectPath(result.path);
      }
    }
    setBusy(`Cloned ${repo.fullName}${result.path ? ` → ${result.path}` : ""}.`);
  }

  async function removeProvider(host: string) {
    await agentClient.gitProviderRemove(host);
    setProviders(await agentClient.gitProviderStatus());
    if (repoBrowserHost === host) {
      setRepoBrowserHost(null);
      setProviderRepos([]);
    }
    setBusy(`Removed ${host}.`);
  }

  async function runGitAction(action: "pull" | "push" | "stash" | "stash-pop" | "commit" | "revert-head") {
    if (!selectedProject) return;
    try {
      if (action === "commit") {
        if (!gitCommitMessage.trim()) {
          setBusy("Enter a commit message first.");
          return;
        }
        setBusy(`Committing ${selectedProject.name}…`);
        const result = await agentClient.gitCommit(selectedProject.path, gitCommitMessage.trim());
        setGitCommitMessage("");
        setBusy(result.hash ? `Committed ${result.hash.trim()}.` : result.message || "Committed changes.");
      } else if (action === "pull") {
        setBusy(`Pulling ${selectedProject.name}…`);
        const result = await agentClient.gitPull(selectedProject.path);
        setBusy(result.message || "Pulled latest changes.");
      } else if (action === "push") {
        setBusy(`Pushing ${selectedProject.name}…`);
        const result = await agentClient.gitPush(selectedProject.path);
        setBusy(result.message || "Pushed branch.");
      } else if (action === "stash") {
        setBusy(`Stashing changes in ${selectedProject.name}…`);
        const result = await agentClient.gitStash(selectedProject.path);
        setBusy(result.message || "Stashed changes.");
      } else if (action === "stash-pop") {
        setBusy(`Restoring stash in ${selectedProject.name}…`);
        const result = await agentClient.gitStashPop(selectedProject.path);
        setBusy(result.message || "Restored stash.");
      } else if (action === "revert-head") {
        const head = gitCommits[0]?.hash;
        if (!head) {
          setBusy("No commit available to revert.");
          return;
        }
        setBusy(`Reverting ${gitCommits[0]?.shortHash || "HEAD"}…`);
        const result = await agentClient.gitRevert(selectedProject.path, head);
        setBusy(result.message || `Reverted ${gitCommits[0]?.shortHash || "HEAD"}.`);
      }
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    } finally {
      setRefreshNonce((value) => value + 1);
    }
  }

  async function launchWorkflowShortcut(mode: "sync" | "resolve" | "ship") {
    if (!selectedProject) return;
    const nextPrompt =
      mode === "sync"
        ? "Sync this project with origin. Prefer rebase over merge, resolve any conflicts carefully, keep my local work intact, run a quick sanity check, then summarize the final branch state."
        : mode === "resolve"
          ? "Inspect the current git state, resolve any merge or rebase conflicts completely, finish the interrupted git operation cleanly, run a sanity check, commit if needed, and tell me exactly what changed."
          : "Finalize this project for shipping: verify the tree, commit any intended changes with a clear message, push the current branch, deploy through the detected path, and report the resulting URL or machine-hosted address.";
    setComposer(nextPrompt);
    setDraftTitle(
      mode === "sync"
        ? `${selectedProject.name} — sync branch`
        : mode === "resolve"
          ? `${selectedProject.name} — resolve conflicts`
          : `${selectedProject.name} — push and deploy`,
    );
    setBusy(
      mode === "sync"
        ? "Prepared a sync/rebase workflow prompt."
        : mode === "resolve"
          ? "Prepared a conflict-resolution workflow prompt."
          : "Prepared a commit/push/deploy workflow prompt.",
    );
  }

  async function saveProjectSecret(secretId: string, value: string) {
    if (!selectedProject) return;
    setSavingSecretKey(secretId);
    try {
      const meta = PROJECT_SECRET_TEMPLATES.find((item) => item.id === secretId);
      if (!meta) return;
      await agentClient.vaultSet({
        name: projectVaultEntryName(selectedProject.path, secretId),
        category: "api-key",
        value,
        notes: `project=${selectedProject.path}; key=${meta.label}; source=web-vibe`,
      });
      setBusy(`Saved ${meta.label} for ${selectedProject.name} to machine vault.`);
    } finally {
      setSavingSecretKey(null);
    }
  }

  const filteredProviderRepos = useMemo(() => {
    const needle = providerSearch.trim().toLowerCase();
    if (!needle) return providerRepos;
    return providerRepos.filter((repo) =>
      repo.name.toLowerCase().includes(needle) ||
      repo.fullName.toLowerCase().includes(needle) ||
      String(repo.description || "").toLowerCase().includes(needle),
    );
  }, [providerRepos, providerSearch]);

  return (
    <div className="flex h-full min-h-0 overflow-hidden bg-surface-950 text-surface-100">
      <div className="flex min-w-0 flex-1 flex-col border-r border-surface-800">
        <div className="border-b border-surface-800 bg-surface-900/70 px-4 py-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Vibing</span>
            <StatusPill>{connectedDevice?.name || "no machine"}</StatusPill>
            {selectedProject ? <StatusPill>{selectedProject.name}</StatusPill> : null}
            {selectedRunner ? <StatusPill>{selectedRunner}</StatusPill> : null}
            {devStatus?.running ? <StatusPill>preview live</StatusPill> : null}
            <div className="relative ml-auto">
              <button
                onClick={() => setCustomizeOpen((v) => !v)}
                className="rounded-full border border-surface-700 bg-surface-950 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-300 hover:border-surface-600"
              >
                Sections
              </button>
              {customizeOpen ? (
                <div className="absolute right-0 top-full z-20 mt-2 w-56 rounded-xl border border-surface-700 bg-surface-900 p-2 shadow-xl">
                  <div className="mb-1 px-1 text-[10px] uppercase tracking-[0.16em] text-surface-500">Show sections</div>
                  {SECTION_ORDER.map((key) => (
                    <label
                      key={key}
                      className="flex cursor-pointer items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-[11px] text-surface-200 hover:bg-surface-950"
                    >
                      <span>{SECTION_LABELS[key]}</span>
                      <input
                        type="checkbox"
                        checked={sections[key].visible}
                        onChange={() => toggleSectionVisible(key)}
                        className="accent-indigo-500"
                      />
                    </label>
                  ))}
                  <button
                    onClick={() => {
                      resetSections();
                      setCustomizeOpen(false);
                    }}
                    className="mt-1 w-full rounded-lg border border-surface-700 px-2 py-1 text-[10px] text-surface-400 hover:border-surface-600"
                  >
                    Reset to defaults
                  </button>
                </div>
              ) : null}
            </div>
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            {visibleDevices.map((device) => (
              <button
                key={device.id}
                onClick={() => void onSelectDevice(device)}
                className={`rounded-full border px-3 py-2 text-xs font-semibold ${
                  connectedDevice?.id === device.id
                    ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-100"
                    : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                }`}
              >
                {device.name}
              </button>
            ))}
          </div>
        </div>

        <div className="grid min-h-0 flex-1 xl:grid-cols-[280px,minmax(0,1fr)]">
          <aside className="flex min-h-0 flex-col gap-2 overflow-auto border-r border-surface-800 bg-surface-900/40 p-3">
            <FoldableSection
              sectionKey="projects"
              label={SECTION_LABELS.projects}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex min-h-0 flex-col gap-2 overflow-auto">
                {projects.map((project) => (
                  <button
                    key={project.path}
                    onClick={() => setSelectedProjectPath(project.path)}
                    className={`rounded-2xl border p-3 text-left ${
                      selectedProjectPath === project.path
                        ? "border-indigo-500/40 bg-indigo-500/10"
                        : "border-surface-800 bg-surface-950/70 hover:border-surface-700"
                    }`}
                  >
                    <div className="text-sm font-semibold text-surface-100">{project.name}</div>
                    <div className="mt-1 truncate text-[11px] text-surface-500">{project.path}</div>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {project.branch ? <MiniPill>{project.branch}</MiniPill> : null}
                      {project.framework ? <MiniPill>{project.framework}</MiniPill> : null}
                    </div>
                  </button>
                ))}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="runner"
              label={SECTION_LABELS.runner}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex flex-wrap gap-2">
                {runners.map((runner) => (
                  <button
                    key={runner.id}
                    onClick={() => setSelectedRunner(runner.id)}
                    className={`rounded-full border px-3 py-2 text-xs font-semibold ${
                      selectedRunner === runner.id
                        ? "border-sky-500/40 bg-sky-500/10 text-sky-100"
                        : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                    }`}
                  >
                    {runner.name}
                  </button>
                ))}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="actions"
              label={SECTION_LABELS.actions}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex flex-wrap gap-2">
                <button
                  onClick={() => void startPreview()}
                  disabled={!selectedProject}
                  className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                >
                  Start Preview
                </button>
                <button
                  onClick={() => void agentClient.reloadDevServer()}
                  disabled={!devStatus?.running}
                  className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                >
                  Hermes Reload
                </button>
                {deployTargets.map((target) => (
                  <button
                    key={target.id}
                    onClick={() => void deploy(target.id)}
                    disabled={!selectedProject}
                    className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-100 hover:bg-emerald-500/15 disabled:opacity-40"
                  >
                    Deploy {target.name}
                  </button>
                ))}
              </div>
              {deployPreviewSummary?.warnings && deployPreviewSummary.warnings.length > 0 ? (
                <div className="mt-3 rounded-2xl border border-amber-500/20 bg-amber-500/10 p-3 text-[11px] leading-5 text-amber-100">
                  {deployPreviewSummary.warnings.slice(0, 3).map((warning) => (
                    <div key={warning}>{warning}</div>
                  ))}
                </div>
              ) : null}
              {deployPreviewSummary?.lastDeploy ? (
                <div className="mt-3 text-[11px] text-surface-500">
                  Last deploy: {deployPreviewSummary.lastDeploy.target || "target"} · {deployPreviewSummary.lastDeploy.status || "unknown"}
                </div>
              ) : null}
            </FoldableSection>

            <FoldableSection
              sectionKey="repo"
              label={SECTION_LABELS.repo}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3 text-xs text-surface-300">
                <div className="flex flex-wrap gap-2">
                  <MiniPill>{gitStatus?.branch || selectedProject?.branch || "no branch"}</MiniPill>
                  <MiniPill>{gitStatus?.ahead || 0} ahead</MiniPill>
                  <MiniPill>{gitStatus?.behind || 0} behind</MiniPill>
                  <MiniPill>{gitStatus?.clean ? "clean" : "dirty"}</MiniPill>
                </div>
                <div className="mt-3 space-y-1 text-surface-400">
                  <div>staged: {gitStatus?.staged?.length || 0}</div>
                  <div>modified: {gitStatus?.modified?.length || 0}</div>
                  <div>untracked: {gitStatus?.untracked?.length || 0}</div>
                </div>
                <div className="mt-4 flex flex-wrap gap-2">
                  <button
                    onClick={() => void runGitAction("pull")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Pull
                  </button>
                  <button
                    onClick={() => void runGitAction("push")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Push
                  </button>
                  <button
                    onClick={() => void runGitAction("stash")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Stash
                  </button>
                  <button
                    onClick={() => void runGitAction("stash-pop")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Pop Stash
                  </button>
                  <button
                    onClick={() => void runGitAction("revert-head")}
                    disabled={!selectedProject || gitCommits.length === 0}
                    className="rounded-lg border border-red-500/30 px-2.5 py-1.5 text-[11px] text-red-300 hover:bg-red-500/10 disabled:opacity-40"
                  >
                    Revert Last
                  </button>
                </div>
                <div className="mt-3 flex gap-2">
                  <input
                    value={gitCommitMessage}
                    onChange={(event) => setGitCommitMessage(event.target.value)}
                    placeholder="commit message"
                    className="flex-1 rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                  />
                  <button
                    onClick={() => void runGitAction("commit")}
                    disabled={!selectedProject || !gitCommitMessage.trim()}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Commit All
                  </button>
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button
                    onClick={() => void launchWorkflowShortcut("sync")}
                    disabled={!selectedProject}
                    className="rounded-full border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[11px] font-semibold text-amber-100 hover:bg-amber-500/15 disabled:opacity-40"
                  >
                    Sync/Rebase Prompt
                  </button>
                  <button
                    onClick={() => void launchWorkflowShortcut("resolve")}
                    disabled={!selectedProject}
                    className="rounded-full border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[11px] font-semibold text-amber-100 hover:bg-amber-500/15 disabled:opacity-40"
                  >
                    Resolve Conflicts Prompt
                  </button>
                  <button
                    onClick={() => void launchWorkflowShortcut("ship")}
                    disabled={!selectedProject}
                    className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-[11px] font-semibold text-emerald-100 hover:bg-emerald-500/15 disabled:opacity-40"
                  >
                    Commit/Push/Deploy Prompt
                  </button>
                </div>
                {gitCommits.length > 0 ? (
                  <div className="mt-4 border-t border-surface-800 pt-3">
                    <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">Recent commits</div>
                    <div className="space-y-2">
                      {gitCommits.slice(0, 5).map((commit) => (
                        <div key={commit.hash} className="rounded-xl border border-surface-800 bg-surface-900/60 p-2">
                          <div className="text-[11px] font-semibold text-surface-200">{commit.message}</div>
                          <div className="mt-1 text-[10px] text-surface-500">
                            {commit.shortHash} · {commit.author} · {new Date(commit.date).toLocaleDateString()}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="secrets"
              label={SECTION_LABELS.secrets}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                <div className="mb-3 text-[11px] leading-5 text-surface-500">
                  Stored in the selected machine vault, namespaced by project path. Nothing here goes to Convex.
                </div>
                <div className="space-y-3">
                  {PROJECT_SECRET_TEMPLATES.map((secret) => (
                    <div key={secret.id}>
                      <label className="mb-1 block text-[11px] font-semibold uppercase tracking-[0.14em] text-surface-500">{secret.label}</label>
                      <div className="mb-1 text-[10px] text-surface-600">{secret.hint}</div>
                      <div className="flex gap-2">
                        <input
                          type={secret.multiline ? "text" : "password"}
                          value={projectSecrets[secret.id] || ""}
                          onChange={(event) => setProjectSecrets((prev) => ({ ...prev, [secret.id]: event.target.value }))}
                          placeholder={secret.placeholder}
                          className="flex-1 rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                        />
                        <button
                          onClick={() => void saveProjectSecret(secret.id, projectSecrets[secret.id] || "")}
                          disabled={!selectedProject || !projectSecrets[secret.id] || savingSecretKey === secret.id}
                          className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                        >
                          {savingSecretKey === secret.id ? "Saving…" : "Save"}
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
                {selectedProject ? (
                  <div className="mt-3 rounded-xl border border-surface-800 bg-surface-900/60 p-2 text-[10px] text-surface-500">
                    Namespace: <span className="font-mono text-surface-400">{projectVaultEntryName(selectedProject.path, "…")}</span>
                  </div>
                ) : null}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="providers"
              label={SECTION_LABELS.providers}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                <div className="flex flex-wrap gap-2">
                  <button
                    onClick={() => void autoDetectProviders()}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                  >
                    Detect
                  </button>
                  <button
                    onClick={() => setManualProvider("github")}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                  >
                    GitHub Token
                  </button>
                  <button
                    onClick={() => setManualProvider("gitlab")}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                  >
                    GitLab Token
                  </button>
                </div>
                {manualProvider ? (
                  <div className="mt-3 flex gap-2">
                    <input
                      type="password"
                      value={providerToken}
                      onChange={(event) => setProviderToken(event.target.value)}
                      placeholder={`${manualProvider} token`}
                      className="flex-1 rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                    />
                    <button
                      onClick={() => void saveProviderToken()}
                      disabled={!providerToken.trim()}
                      className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                    >
                      Save
                    </button>
                  </div>
                ) : null}
                <div className="mt-3 space-y-2">
                  {providers.length === 0 ? (
                    <div className="text-xs text-surface-500">No git providers connected on this machine.</div>
                  ) : providers.map((provider) => (
                    <div key={provider.host} className="rounded-xl border border-surface-800 bg-surface-900/60 p-3">
                      <div className="flex items-center justify-between gap-2">
                        <div>
                          <div className="text-sm font-semibold text-surface-100">{provider.username}</div>
                          <div className="text-[11px] text-surface-500">{provider.provider} · {provider.host}{provider.hasSsh ? " · SSH" : ""}</div>
                        </div>
                        <div className="flex gap-2">
                          <button
                            onClick={() => void toggleProviderRepos(provider.host)}
                            className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600"
                          >
                            {repoBrowserHost === provider.host ? "Hide" : "Repos"}
                          </button>
                          <button
                            onClick={() => void removeProvider(provider.host)}
                            className="rounded-lg border border-red-500/30 px-2.5 py-1.5 text-[11px] text-red-300 hover:bg-red-500/10"
                          >
                            Remove
                          </button>
                        </div>
                      </div>
                      {repoBrowserHost === provider.host ? (
                        <div className="mt-3">
                          <input
                            value={providerSearch}
                            onChange={(event) => setProviderSearch(event.target.value)}
                            placeholder="search repos"
                            className="w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                          />
                          <div className="mt-2 max-h-64 space-y-2 overflow-auto">
                            {filteredProviderRepos.map((repo) => (
                              <div key={repo.fullName} className="rounded-xl border border-surface-800 bg-surface-950/70 p-3">
                                <div className="flex items-start justify-between gap-3">
                                  <div className="min-w-0">
                                    <div className="truncate text-sm font-semibold text-surface-100">{repo.fullName}</div>
                                    <div className="mt-1 flex flex-wrap gap-1.5">
                                      {repo.private ? <MiniPill>private</MiniPill> : null}
                                      {repo.language ? <MiniPill>{repo.language}</MiniPill> : null}
                                    </div>
                                    {repo.description ? <div className="mt-1 text-[11px] text-surface-500">{repo.description}</div> : null}
                                  </div>
                                  <button
                                    onClick={() => void cloneRemoteRepo(repo)}
                                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600"
                                  >
                                    Clone
                                  </button>
                                </div>
                              </div>
                            ))}
                          </div>
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="sessions"
              label={SECTION_LABELS.sessions}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex min-h-0 flex-col gap-2 overflow-auto">
                {taskList.map((task) => (
                  <button
                    key={task.id}
                    onClick={() => setActiveTaskId(task.id)}
                    className={`rounded-2xl border p-3 text-left ${
                      activeTask?.id === task.id
                        ? "border-amber-500/40 bg-amber-500/10"
                        : "border-surface-800 bg-surface-950/70 hover:border-surface-700"
                    }`}
                  >
                    <div className="text-sm font-semibold text-surface-100">{task.title}</div>
                    <div className="mt-1 text-[11px] text-surface-500">
                      {task.status} · {new Date(task.updatedAt).toLocaleTimeString()}
                    </div>
                  </button>
                ))}
              </div>
            </FoldableSection>
          </aside>

          <div className="flex min-h-0 flex-col bg-[#08111a]">
            <div className="border-b border-surface-800 px-4 py-3">
              <div className="text-sm font-semibold text-surface-100">
                {activeTask?.title || "New coding session"}
              </div>
              <div className="text-xs text-surface-500">
                {activeTask
                  ? `${activeTask.status} · ${selectedProject?.path || ""}`
                  : "Project-scoped remote coding through the connected machine"}
              </div>
            </div>

            <div className="min-h-0 flex-1 overflow-auto px-4 py-4 font-mono text-[13px] leading-6 text-surface-200">
              {streamedOutput.trim() ? (
                <pre className="whitespace-pre-wrap">{streamedOutput}</pre>
              ) : activeTask?.output?.length ? (
                <pre className="whitespace-pre-wrap">{activeTask.output.join("\n")}</pre>
              ) : (
                <div className="flex h-full min-h-[240px] items-center justify-center text-sm text-surface-500">
                  Start a task on the left, or continue the selected session here.
                </div>
              )}
            </div>

            <div className="border-t border-surface-800 bg-surface-900/70 p-4">
              <input
                value={draftTitle}
                onChange={(event) => setDraftTitle(event.target.value)}
                placeholder="optional title"
                className="mb-3 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
              />
              <textarea
                value={composer}
                onChange={(event) => setComposer(event.target.value)}
                placeholder="Fix the mobile login screen, keep the RN preview running, and tell me the local URL or tunnel once it is reachable."
                className="min-h-28 w-full rounded-2xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-100 outline-none focus:border-indigo-500"
              />
              <div className="mt-3 flex items-center justify-between gap-3">
                <div className="text-xs text-surface-500">
                  Task is scoped to {selectedProject?.name || "the selected project"} and runs on {connectedDevice?.name || "the connected machine"}.
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => void continueChatTask()}
                    disabled={!activeTask || !composer.trim()}
                    className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Continue
                  </button>
                  <button
                    onClick={() => void startChatTask()}
                    disabled={!selectedProject || !composer.trim()}
                    className="rounded-xl bg-indigo-500 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-400 disabled:opacity-40"
                  >
                    Start Task
                  </button>
                </div>
              </div>
              {busy ? <div className="mt-3 text-xs text-surface-400">{busy}</div> : null}
            </div>
          </div>
        </div>
      </div>

      <div className="w-[42vw] min-w-[360px] max-w-[720px]">
        <PreviewPane
          selectedPreviewTarget={selectedPreviewTarget}
          onSelectPreviewTarget={onSelectPreviewTarget}
          mobileWorkers={mobileWorkers}
        />
      </div>
    </div>
  );
}

const PROJECT_SECRET_TEMPLATES = [
  { id: "cloudflare_api_token", label: "Cloudflare Token", placeholder: "Cloudflare API token", hint: "Used for Workers, DNS, and deploy flows.", multiline: false },
  { id: "cloudflare_account_id", label: "Cloudflare Account ID", placeholder: "Cloudflare account ID", hint: "Project-level Cloudflare account target.", multiline: false },
  { id: "convex_deploy_key", label: "Convex Deploy Key", placeholder: "Convex deploy key", hint: "For `npx convex deploy` and backend promotion.", multiline: false },
  { id: "convex_url", label: "Convex URL", placeholder: "https://...convex.cloud", hint: "Project runtime endpoint if you keep several Convex apps.", multiline: false },
  { id: "vercel_token", label: "Vercel Token", placeholder: "Vercel token", hint: "For Vercel CLI deploys tied to this repo.", multiline: false },
  { id: "supabase_access_token", label: "Supabase Token", placeholder: "Supabase access token", hint: "For project-linked Supabase deploy or db flows.", multiline: false },
  { id: "expo_token", label: "Expo Token", placeholder: "Expo access token", hint: "For EAS / Expo publish from this mobile project.", multiline: false },
  { id: "sentry_auth_token", label: "Sentry Token", placeholder: "Sentry auth token", hint: "Upload source maps or releases for this project only.", multiline: false },
] as const;

function projectVaultEntryName(projectPath: string, secretId: string): string {
  const normalized = projectPath.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  const suffix = hashProjectPath(projectPath);
  return `project:${normalized}:${suffix}:${secretId}`;
}

function hashProjectPath(input: string): string {
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

function summarizeTitle(prompt: string, projectName: string): string {
  const clean = prompt.replace(/\s+/g, " ").trim();
  const base = clean.slice(0, 64);
  return base ? `${projectName} — ${base}` : `${projectName} task`;
}

function buildVibeTaskPrompt({
  project,
  prompt,
  gitStatus,
  deployTargets,
}: {
  project: Project;
  prompt: string;
  gitStatus: GitStatusRow | null;
  deployTargets: Array<{ id: string; name: string }>;
}): string {
  const lines = [
    `You are working for a solo developer in project ${project.name}.`,
    `Project path: ${project.path}`,
    project.framework ? `Framework: ${project.framework}` : "",
    gitStatus?.branch ? `Current branch: ${gitStatus.branch}` : "",
    gitStatus ? `Git state: ${gitStatus.clean ? "clean" : "dirty"}, ahead=${gitStatus.ahead || 0}, behind=${gitStatus.behind || 0}, staged=${gitStatus.staged?.length || 0}, modified=${gitStatus.modified?.length || 0}, untracked=${gitStatus.untracked?.length || 0}` : "",
    deployTargets.length > 0 ? `Known deploy targets: ${deployTargets.map((target) => target.name).join(", ")}` : "Known deploy targets: none detected yet",
    "",
    "Execution contract:",
    "1. Stay inside the selected project unless the task explicitly requires adjacent workspace files.",
    "2. Sync with git before making risky changes. If the branch is behind or diverged, fetch and rebase intelligently instead of creating an unnecessary merge commit.",
    "3. If conflicts happen, resolve them carefully, explain the resolution, and continue instead of stopping to ask.",
    "4. After implementing, run the relevant checks, keep the working tree coherent, then commit the work with a clear message.",
    "5. Push the branch unless the prompt explicitly says not to.",
    "6. If there is a detected deploy path and the change is meant to ship, deploy it and report the result.",
    "7. If you start or reload a dev/mobile preview, surface the reachable preview/LAN/device URL clearly.",
    "8. Do not stop at analysis; carry the work through to a real state change when feasible.",
  "",
  "User request:",
  prompt,
  ].filter(Boolean);
  return lines.join("\n");
}

function buildVibeContinuationPrompt({
  project,
  prompt,
  gitStatus,
  deployTargets,
}: {
  project: Project | null;
  prompt: string;
  gitStatus: GitStatusRow | null;
  deployTargets: Array<{ id: string; name: string }>;
}): string {
  const lines = [
    project ? `Continue in project ${project.name} (${project.path}).` : "",
    gitStatus?.branch ? `Current branch: ${gitStatus.branch}` : "",
    deployTargets.length > 0 ? `Deploy targets still available: ${deployTargets.map((target) => target.name).join(", ")}` : "",
    "Keep the solo-developer flow: sync/rebase if needed, resolve conflicts, finish the git operation cleanly, commit, push, and deploy when the request implies shipping.",
    "",
    prompt,
  ].filter(Boolean);
  return lines.join("\n");
}

function extractOutputText(chunk: string): string {
  try {
    const parsed = JSON.parse(chunk);
    if (parsed?.text) return String(parsed.text);
  } catch {}
  return chunk;
}

function FoldableSection({
  sectionKey,
  label,
  sections,
  onToggle,
  children,
}: {
  sectionKey: SectionKey;
  label: string;
  sections: Record<SectionKey, SectionState>;
  onToggle: (key: SectionKey) => void;
  children: ReactNode;
}) {
  const state = sections[sectionKey];
  if (!state.visible) return null;
  return (
    <section className="rounded-xl border border-surface-800 bg-surface-950/40">
      <button
        onClick={() => onToggle(sectionKey)}
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-400 hover:text-surface-200"
      >
        <span>{label}</span>
        <span className="text-surface-500">{state.open ? "−" : "+"}</span>
      </button>
      {state.open ? <div className="border-t border-surface-800 px-3 py-3">{children}</div> : null}
    </section>
  );
}

function StatusPill({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-950 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-300">
      {children}
    </span>
  );
}

function MiniPill({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-900 px-2 py-1 text-[10px] text-surface-300">
      {children}
    </span>
  );
}
