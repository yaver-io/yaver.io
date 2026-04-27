"use client";

import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { agentClient, type ConnectionState, type GitCommitRow, type GitProviderStatusRow, type GitRemoteRepo, type GitStatusRow, type MachineInfo, type Runner, type Task } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import { useAuth } from "@/lib/use-auth";
import PreviewPane from "./PreviewPane";
import { preferredDefaultModelForRunner, preferredDefaultRunnerForDevice, usePrimaryRunnerByDevice } from "./DevicesView";

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

type DeployActionKind = "testflight" | "play-internal" | "eas";

function previewPlatformForFramework(framework?: string): "web" | undefined {
  const fw = (framework || "").toLowerCase();
  if (
    fw.includes("expo") ||
    fw.includes("react-native") ||
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react"
  ) {
    return "web";
  }
  return undefined;
}

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
  const { token, user } = useAuth();
  const { primaryRunnerByDevice, primaryModelByDevice } = usePrimaryRunnerByDevice(token);
  const [projects, setProjects] = useState<Project[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [selectedProjectPath, setSelectedProjectPath] = useState("");
  const [selectedRunner, setSelectedRunner] = useState("");
  const [selectedModel, setSelectedModel] = useState("");
  // OpenCode-specific: which agent (build / plan / custom) drives the
  // task. Maps to `--agent <mode>` on `opencode run`. Empty = the
  // user's defaultAgent in opencode.json. Other runners ignore it.
  const [selectedMode, setSelectedMode] = useState("");
  const [taskList, setTaskList] = useState<Task[]>([]);
  const [activeTaskId, setActiveTaskId] = useState("");
  const [composer, setComposer] = useState("");
  const [draftTitle, setDraftTitle] = useState("");
  // Video summary toggle — when on, the agent records a short MP4 demo
  // after the task completes (vibe-preview pipeline). Surfaces as a
  // "▶ Watch demo" button on the task row when ready.
  const [videoSummaryEnabled, setVideoSummaryEnabled] = useState(false);
  // Currently-playing clip id; opens a fullscreen <video> overlay.
  const [activeClipId, setActiveClipId] = useState<string | null>(null);
  // OpenCode agents discovered via /runner/opencode/config — used to
  // populate the agent dropdown below. Includes built-ins (build,
  // plan) plus any custom agents the user has defined under
  // `agent.<name>` in their opencode.json. Re-fetched whenever the
  // connected device changes so a phone driving a Mac mini sees that
  // mac mini's local agents.
  const [opencodeAgents, setOpencodeAgents] = useState<Array<{ name: string; model?: string; isBuiltin?: boolean }>>([]);

  useEffect(() => {
    // `connected` is declared further down; recompute it locally so this
    // effect can run before its top-level binding without TS hoisting
    // issues. Same expression as the const at line 216.
    const isConnected = connState === "connected" && !!connectedDevice;
    if (selectedRunner !== "opencode" || !isConnected) {
      setOpencodeAgents([]);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const cfg = await agentClient.openCodeConfig(connectedDevice?.id);
        if (cancelled) return;
        setOpencodeAgents((cfg?.agents || []) as any);
      } catch {
        // Silently fall back to hardcoded build/plan; the dropdown
        // below tolerates an empty agents list and shows the stock
        // entries inline.
      }
    })();
    return () => { cancelled = true; };
  }, [selectedRunner, connState, connectedDevice?.id]);
  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);
  const [deployTargets, setDeployTargets] = useState<Array<{ id: string; name: string }>>([]);
  const [deployPreviewSummary, setDeployPreviewSummary] = useState<DeployPreviewSummary | null>(null);
  const [gitStatus, setGitStatus] = useState<GitStatusRow | null>(null);
  const [gitCommits, setGitCommits] = useState<GitCommitRow[]>([]);
  const [gitCommitMessage, setGitCommitMessage] = useState("");
  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [machines, setMachines] = useState<MachineInfo[]>([]);
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
  const [runnerAuthBusy, setRunnerAuthBusy] = useState(false);
  const [runnerAuthError, setRunnerAuthError] = useState<string | null>(null);
  const [runnerAuthSessionId, setRunnerAuthSessionId] = useState<string | null>(null);
  const [runnerAuthStatus, setRunnerAuthStatus] = useState<{
    runner: "claude" | "codex";
    status: "starting" | "awaiting_browser" | "completed" | "failed" | "cancelled";
    openUrl?: string;
    code?: string;
    detail?: string;
    error?: string;
  } | null>(null);

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
  const connectedMachine = useMemo(() => {
    if (!connectedDevice) return null;
    const normalize = (value?: string | null) =>
      String(value || "").trim().toLowerCase().replace(/\.local$/i, "");
    return (
      machines.find((machine) => machine.deviceId === connectedDevice.id) ||
      machines.find((machine) => normalize(machine.name) === normalize(connectedDevice.name)) ||
      null
    );
  }, [connectedDevice, machines]);
  const activeTask = useMemo(
    () => taskList.find((task) => task.id === activeTaskId) || taskList[0] || null,
    [taskList, activeTaskId],
  );
  const selectedRunnerRow = useMemo(
    () => runners.find((runner) => runner.id === selectedRunner) || null,
    [runners, selectedRunner],
  );
  const availableModels = selectedRunnerRow?.models || [];
  const conversationTurns = useMemo(
    () => (activeTask?.turns || []).filter((turn) => String(turn.content || "").trim()),
    [activeTask?.turns],
  );
  const liveOutput = streamedOutput.trim() || activeTask?.output?.join("\n").trim() || "";
  const lastAssistantTurn = useMemo(() => {
    for (let i = conversationTurns.length - 1; i >= 0; i -= 1) {
      if (conversationTurns[i].role === "assistant") return conversationTurns[i].content.trim();
    }
    return "";
  }, [conversationTurns]);
  const showLiveOutput = !!liveOutput && liveOutput !== lastAssistantTurn;

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
        const [projectRows, runnerRows, tasks, preview, currentDevStatus, git, commits, gitProviders, machineInventory] = await Promise.all([
          agentClient.listProjects().catch(() => []),
          agentClient.getRunners().catch(() => []),
          agentClient.listTasks(12).catch(() => []),
          selectedProjectPath ? agentClient.deployPreview(selectedProjectPath).catch(() => null) : Promise.resolve(null),
          agentClient.getDevServerStatus().catch(() => null),
          selectedProjectPath ? agentClient.gitStatus(selectedProjectPath).catch(() => null) : Promise.resolve(null),
          selectedProjectPath ? agentClient.gitLog(selectedProjectPath, 8).catch(() => []) : Promise.resolve([]),
          agentClient.gitProviderStatus().catch(() => []),
          agentClient.consoleMachines().catch(() => ({ machines: [] })),
        ]);
        if (cancelled) return;
        setProjects(projectRows);
        setRunners((runnerRows || []).filter((runner) => runner.installed));
        setTaskList(tasks || []);
        setDevStatus(currentDevStatus);
        setGitStatus(git ?? null);
        setGitCommits(commits || []);
        setProviders(gitProviders || []);
        setMachines(machineInventory?.machines || []);
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
        const installed = (runnerRows || []).filter((runner) => runner.installed);
        const ready = installed.filter((runner) => runner.ready);
        const explicitRunner = connectedDevice ? primaryRunnerByDevice[connectedDevice.id] : "";
        if (explicitRunner && installed.some((runner) => runner.id === explicitRunner) && selectedRunner !== explicitRunner) {
          setSelectedRunner(explicitRunner);
        } else if (!selectedRunner) {
          const seededRunner = connectedDevice
            ? preferredDefaultRunnerForDevice(
                connectedDevice,
                user?.email,
                ready.map((runner) => runner.id).concat(installed.map((runner) => runner.id)),
              )
            : null;
          const preferred =
            ready.find((runner) => runner.id === seededRunner) ||
            installed.find((runner) => runner.id === seededRunner) ||
            ready.find((runner) => runner.active) ||
            ready.find((runner) => runner.id === "claude") ||
            ready.find((runner) => runner.id === "opencode") ||
            ready.find((runner) => runner.id === "codex") ||
            installed.find((runner) => runner.active) ||
            installed.find((runner) => runner.id === "claude") ||
            installed.find((runner) => runner.id === "opencode") ||
            installed.find((runner) => runner.id === "codex") ||
            installed[0];
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
  }, [activeTaskId, connected, connectedDevice, primaryRunnerByDevice, refreshNonce, selectedProjectPath, selectedRunner, user?.email]);

  useEffect(() => {
    if (!selectedRunnerRow || availableModels.length === 0) {
      setSelectedModel("");
      return;
    }
    const explicitModel = connectedDevice ? primaryModelByDevice[connectedDevice.id] : "";
    if (explicitModel && availableModels.some((model) => model.id === explicitModel) && selectedModel !== explicitModel) {
      setSelectedModel(explicitModel);
      return;
    }
    if (selectedModel && availableModels.some((model) => model.id === selectedModel)) {
      return;
    }
    const seededModel = connectedDevice
      ? preferredDefaultModelForRunner(selectedRunnerRow.id, connectedDevice, user?.email)
      : null;
    const preferred =
      (explicitModel && availableModels.find((model) => model.id === explicitModel)) ||
      (seededModel && availableModels.find((model) => model.id === seededModel)) ||
      availableModels.find((model) => model.isDefault) ||
      availableModels[0];
    setSelectedModel(preferred?.id || "");
  }, [availableModels, connectedDevice, primaryModelByDevice, selectedModel, selectedRunnerRow, user?.email]);

  useEffect(() => {
    if (!runnerAuthSessionId) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const session = await agentClient.getRunnerBrowserAuthStatus(runnerAuthSessionId);
        if (cancelled) return;
        setRunnerAuthStatus({
          runner: session.runner,
          status: session.status,
          openUrl: session.openUrl,
          code: session.code,
          detail: session.detail,
          error: session.error,
        });
        if (session.status === "completed") {
          setRunnerAuthBusy(false);
          setRunnerAuthError(null);
          setBusy(`${session.runner === "claude" ? "Claude Code" : "Codex"} is ready on this machine.`);
          setRefreshNonce((value) => value + 1);
          setTimeout(() => {
            setRunnerAuthSessionId(null);
            setRunnerAuthStatus(null);
          }, 1500);
        } else if (session.status === "failed" || session.status === "cancelled") {
          setRunnerAuthBusy(false);
          setRunnerAuthError(session.error || session.detail || "Sign-in did not complete.");
        }
      } catch (error) {
        if (!cancelled) {
          setRunnerAuthBusy(false);
          setRunnerAuthError(error instanceof Error ? error.message : String(error));
        }
      }
    };
    void poll();
    const interval = setInterval(() => void poll(), 1500);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [runnerAuthSessionId]);

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
    if (selectedRunnerRow && selectedRunnerRow.ready === false) {
      setBusy(selectedRunnerRow.error || selectedRunnerRow.warning || `${selectedRunnerRow.name} is installed but not ready on this machine.`);
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
        machine: connectedMachine,
      }),
      userPrompt: composer.trim(),
      runner: selectedRunner || undefined,
      model: selectedModel || undefined,
      mode: selectedRunner === "opencode" && selectedMode ? selectedMode : undefined,
      projectName: selectedProject.name,
      workDir: selectedProject.path,
      videoEnabled: videoSummaryEnabled,
    });
    setComposer("");
    setDraftTitle("");
    setActiveTaskId(task.id);
    setBusy(`Started ${task.title}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function continueChatTask() {
    if (!activeTask || !composer.trim()) return;
    if (selectedRunnerRow && selectedRunnerRow.ready === false) {
      setBusy(selectedRunnerRow.error || selectedRunnerRow.warning || `${selectedRunnerRow.name} is installed but not ready on this machine.`);
      return;
    }
    setBusy(`Continuing ${activeTask.title}…`);
    await agentClient.continueTask(
      activeTask.id,
      buildVibeContinuationPrompt({
        project: selectedProject,
        prompt: composer.trim(),
        gitStatus,
        deployTargets,
        machine: connectedMachine,
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
      platform: previewPlatformForFramework(selectedProject.framework),
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

  async function launchDeployTask(kind: DeployActionKind) {
    if (!selectedProject) {
      setBusy("Pick a project first.");
      return;
    }
    const plan = buildDeployTaskPlan({
      kind,
      project: selectedProject,
      machine: connectedMachine,
      deployTargets,
      gitStatus,
    });
    setBusy(plan.startingLabel);
    const task = await agentClient.createTask({
      title: plan.title,
      description: plan.prompt,
      userPrompt: plan.userPrompt,
      runner: selectedRunner || undefined,
      model: selectedModel || undefined,
      projectName: selectedProject.name,
      workDir: selectedProject.path,
      videoEnabled: videoSummaryEnabled,
    });
    setActiveTaskId(task.id);
    setBusy(plan.startedLabel);
    setRefreshNonce((value) => value + 1);
  }

  function prepareDeployPrompt(kind: DeployActionKind) {
    if (!selectedProject) return;
    const plan = buildDeployTaskPlan({
      kind,
      project: selectedProject,
      machine: connectedMachine,
      deployTargets,
      gitStatus,
    });
    setDraftTitle(plan.title);
    setComposer(plan.userPrompt);
    setBusy(plan.preparedLabel);
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

  async function startSelectedRunnerSignIn() {
    if (!selectedRunnerRow) return;
    if (selectedRunnerRow.id !== "claude" && selectedRunnerRow.id !== "codex") {
      setRunnerAuthError(`${selectedRunnerRow.name} does not expose browser sign-in here.`);
      return;
    }
    setRunnerAuthBusy(true);
    setRunnerAuthError(null);
    setRunnerAuthStatus({
      runner: selectedRunnerRow.id,
      status: "starting",
    });
    try {
      const session = await agentClient.startRunnerBrowserAuth(selectedRunnerRow.id);
      setRunnerAuthSessionId(session.id);
      setRunnerAuthStatus({
        runner: session.runner,
        status: session.status,
        openUrl: session.openUrl,
        code: session.code,
        detail: session.detail,
        error: session.error,
      });
      if (session.openUrl && typeof window !== "undefined") {
        window.open(session.openUrl, "_blank", "noopener,noreferrer");
      }
    } catch (error) {
      setRunnerAuthBusy(false);
      setRunnerAuthError(error instanceof Error ? error.message : String(error));
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

  const machineSummary = useMemo(() => {
    if (!connectedMachine) return null;
    const caps = connectedMachine.capabilities;
    return {
      platform: connectedMachine.platform || connectedMachine.os || "machine",
      supportsIos: !!caps?.supportsIos,
      supportsAndroid: !!caps?.supportsAndroid,
      supportsTestFlight: !!caps?.supportsTestFlight,
      supportsPlayStore: !!caps?.supportsPlayStore,
      supportsDocker: !!caps?.supportsDocker,
      runnerNames: (caps?.runners || [])
        .filter((runner) => runner.ready)
        .map((runner) => runner.name),
      currentWorkDir: connectedMachine.currentWorkDir || "",
    };
  }, [connectedMachine]);

  const deployQuickActions = useMemo(
    () => buildDeployQuickActions(selectedProject, connectedMachine, deployTargets),
    [selectedProject, connectedMachine, deployTargets],
  );

  // Resolve the clip URL once when the user hits "▶ Watch demo".
  // We can't put the auth token in <video src> directly (browser
  // ignores headers), so we fetch the MP4 as a blob and render the
  // object URL. Same blob-shim the standalone VibePreviewView uses.
  const [activeClipBlobUrl, setActiveClipBlobUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!activeClipId) {
      if (activeClipBlobUrl) URL.revokeObjectURL(activeClipBlobUrl);
      setActiveClipBlobUrl(null);
      return;
    }
    const req = agentClient.vibeClipRequest(activeClipId);
    if (!req) return;
    let cancelled = false;
    let url: string | null = null;
    void (async () => {
      try {
        const res = await fetch(req.url, { headers: req.headers });
        if (!res.ok) return;
        const blob = await res.blob();
        url = URL.createObjectURL(blob);
        if (!cancelled) setActiveClipBlobUrl(url);
      } catch { /* ignore */ }
    })();
    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [activeClipId]);

  return (
    <div className="flex h-full min-h-0 overflow-hidden bg-surface-950 text-surface-100">
      {/* Video summary overlay — fullscreen player triggered from the
          task header chip. Closes on ✕ or video end. */}
      {activeClipId && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/95"
          onClick={() => setActiveClipId(null)}
        >
          <button
            onClick={() => setActiveClipId(null)}
            className="absolute right-6 top-6 rounded-full bg-white/10 px-4 py-2 text-white hover:bg-white/20"
          >
            ✕ Close
          </button>
          {activeClipBlobUrl ? (
            <video
              src={activeClipBlobUrl}
              controls
              autoPlay
              className="max-w-full max-h-full"
              onClick={(e) => e.stopPropagation()}
              onEnded={() => setActiveClipId(null)}
            />
          ) : (
            <span className="text-zinc-400 text-sm">Loading clip…</span>
          )}
        </div>
      )}
      <div className="w-[46vw] min-w-[380px] max-w-[760px] border-r border-surface-800">
        <PreviewPane
          selectedPreviewTarget={selectedPreviewTarget}
          onSelectPreviewTarget={onSelectPreviewTarget}
          mobileWorkers={mobileWorkers}
        />
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="border-b border-surface-800 bg-surface-900/70 px-4 py-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Vibing</span>
            <StatusPill>{connectedDevice?.name || "no machine"}</StatusPill>
            {selectedProject ? <StatusPill>{selectedProject.name}</StatusPill> : null}
            {selectedRunner ? <StatusPill>{selectedRunner}</StatusPill> : null}
            {selectedModel ? <StatusPill>{selectedModel}</StatusPill> : null}
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

        <div className="grid min-h-0 flex-1 xl:grid-cols-[320px,minmax(0,1fr)]">
          <aside className="flex min-h-0 flex-col gap-2 overflow-auto border-r border-surface-800 bg-surface-900/40 p-3">
            <section className="rounded-xl border border-surface-800 bg-surface-950/40 px-3 py-3">
              <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-400">Remote Machine</div>
              <div className="mt-2 text-sm font-semibold text-surface-100">{connectedMachine?.name || connectedDevice?.name || "No machine"}</div>
              <div className="mt-1 text-[11px] text-surface-500">
                {machineSummary?.platform || "Connect to a machine to see toolchain readiness."}
              </div>
              <div className="mt-3 flex flex-wrap gap-1.5">
                <MiniPill>{machineSummary?.supportsIos ? "iOS toolchain" : "no iOS toolchain"}</MiniPill>
                <MiniPill>{machineSummary?.supportsAndroid ? "Android toolchain" : "no Android toolchain"}</MiniPill>
                <MiniPill>{machineSummary?.supportsTestFlight ? "TestFlight ready" : "TestFlight gated"}</MiniPill>
                <MiniPill>{machineSummary?.supportsPlayStore ? "Play deploy ready" : "Play deploy gated"}</MiniPill>
              </div>
              {machineSummary?.runnerNames?.length ? (
                <div className="mt-3 text-[11px] text-surface-500">
                  Ready runners: {machineSummary.runnerNames.join(", ")}
                </div>
              ) : null}
              {machineSummary?.currentWorkDir ? (
                <div className="mt-2 truncate text-[10px] font-mono text-surface-600">
                  cwd: {machineSummary.currentWorkDir}
                </div>
              ) : null}
            </section>

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
              <label className="block text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                Agent
              </label>
              <select
                value={selectedRunner}
                onChange={(event) => setSelectedRunner(event.target.value)}
                className="mt-2 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
              >
                {runners.map((runner) => (
                  <option key={runner.id} value={runner.id}>
                    {runner.name}{runner.ready === false ? " (sign-in needed)" : ""}
                  </option>
                ))}
              </select>
              <div className="mt-3 flex flex-wrap gap-2">
                {runners.map((runner) => (
                  <button
                    key={runner.id}
                    onClick={() => setSelectedRunner(runner.id)}
                    className={`rounded-full border px-3 py-2 text-xs font-semibold ${
                      selectedRunner === runner.id
                        ? "border-sky-500/40 bg-sky-500/10 text-sky-100"
                        : runner.ready === false
                          ? "border-amber-500/20 bg-amber-500/5 text-amber-100 hover:border-amber-500/40"
                          : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                    }`}
                    title={runner.error || runner.warning || runner.name}
                  >
                    {runner.name}
                    {runner.ready === false ? " (blocked)" : ""}
                  </button>
                ))}
              </div>
              {availableModels.length > 0 ? (
                <>
                  <label className="mt-4 block text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                    Model
                  </label>
                  <select
                    value={selectedModel}
                    onChange={(event) => setSelectedModel(event.target.value)}
                    className="mt-2 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                  >
                    {availableModels.map((model) => (
                      <option key={model.id} value={model.id}>
                        {model.name}
                      </option>
                    ))}
                  </select>
                </>
              ) : null}
              {/* OpenCode-only: build vs plan agent picker. Maps to
                  `--agent <mode>` on `opencode run`. The user's
                  opencode.json defines what each agent does (different
                  models, system prompts, allowed tools); Yaver just
                  forwards the choice. Empty value = "default agent",
                  which means whatever defaultAgent is set in their
                  opencode.json. Custom agents the user defined show
                  up here automatically because the agent's
                  /runner/opencode/config endpoint surfaces them. */}
              {selectedRunner === "opencode" ? (
                <>
                  <label className="mt-4 block text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                    OpenCode Agent
                  </label>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {/* Default + every agent from opencode.json. The
                        agents[] array surfaces both stock (build, plan)
                        and custom (review, research, etc.) entries.
                        Falls back to hardcoded build/plan when the
                        config isn't reachable so the picker is never
                        empty. */}
                    {([{ name: "", isBuiltin: true } as { name: string; model?: string; isBuiltin?: boolean }]
                      .concat(
                        opencodeAgents.length > 0
                          ? opencodeAgents
                          : [
                              { name: "build", isBuiltin: true },
                              { name: "plan", isBuiltin: true },
                            ],
                      )
                    ).map((agent) => {
                      const id = agent.name;
                      const label = id === "" ? "Default" : id.charAt(0).toUpperCase() + id.slice(1);
                      const tooltip = id === ""
                        ? "Use defaultAgent from opencode.json"
                        : agent.model
                          ? `Run with --agent ${id} (${agent.model})`
                          : `Run with --agent ${id}`;
                      return (
                        <button
                          key={id || "default"}
                          onClick={() => setSelectedMode(id)}
                          className={`rounded-full border px-3 py-1.5 text-xs font-semibold ${
                            selectedMode === id
                              ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-100"
                              : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                          } ${!agent.isBuiltin && id !== "" ? "italic" : ""}`}
                          title={tooltip}
                        >
                          {label}
                          {agent.model ? <span className="ml-1.5 text-[10px] text-surface-500">({agent.model.split("/").pop()})</span> : null}
                        </button>
                      );
                    })}
                  </div>
                  <p className="mt-1 text-[10px] text-surface-500">
                    Build for code edits · Plan for architecture / dry runs · italic = custom agents from your <span className="font-mono">opencode.json</span>.
                  </p>
                </>
              ) : null}
              {selectedRunnerRow?.ready === false ? (
                <div className="mt-3 rounded-2xl border border-amber-500/20 bg-amber-500/10 p-3 text-[11px] leading-5 text-amber-100">
                  {selectedRunnerRow.error || selectedRunnerRow.warning || `${selectedRunnerRow.name} is installed but not ready on this machine.`}
                </div>
              ) : null}
              {selectedRunnerRow && (selectedRunnerRow.id === "claude" || selectedRunnerRow.id === "codex") && selectedRunnerRow.ready === false ? (
                <div className="mt-3 rounded-2xl border border-sky-500/20 bg-sky-500/10 p-3 text-[11px] text-sky-100">
                  <div className="font-semibold">
                    {selectedRunnerRow.id === "claude" ? "Claude Code" : "OpenAI Codex"} sign-in is available from here.
                  </div>
                  <div className="mt-1 leading-5 text-sky-100/80">
                    Start the browser auth flow on the host, finish it in your browser, then this runner will become selectable for vibe coding.
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <button
                      onClick={() => void startSelectedRunnerSignIn()}
                      disabled={runnerAuthBusy}
                      className="rounded-xl border border-sky-400/30 bg-sky-400/10 px-3 py-2 text-xs font-semibold text-sky-100 hover:bg-sky-400/15 disabled:opacity-40"
                    >
                      {runnerAuthBusy ? "Opening sign-in…" : `Sign in to ${selectedRunnerRow.name}`}
                    </button>
                    {runnerAuthStatus?.openUrl ? (
                      <a
                        href={runnerAuthStatus.openUrl}
                        target="_blank"
                        rel="noreferrer"
                        className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                      >
                        Open auth page
                      </a>
                    ) : null}
                  </div>
                  {runnerAuthStatus ? (
                    <div className="mt-3 rounded-xl border border-surface-800 bg-surface-950/60 p-3 text-[10px] text-surface-300">
                      <div className="font-semibold uppercase tracking-[0.14em] text-surface-500">Sign-in status</div>
                      <div className="mt-2">{runnerAuthStatus.status.replaceAll("_", " ")}</div>
                      {runnerAuthStatus.code ? <div className="mt-1 font-mono text-surface-200">Code: {runnerAuthStatus.code}</div> : null}
                      {runnerAuthStatus.detail ? <div className="mt-1 text-surface-400">{runnerAuthStatus.detail}</div> : null}
                    </div>
                  ) : null}
                  {runnerAuthError ? (
                    <div className="mt-2 text-[10px] text-rose-200">{runnerAuthError}</div>
                  ) : null}
                </div>
              ) : null}
              {availableModels.length > 0 ? (
                <div className="mt-3 flex flex-wrap gap-2">
                  {availableModels.map((model) => (
                    <button
                      key={model.id}
                      onClick={() => setSelectedModel(model.id)}
                      className={`rounded-full border px-3 py-1.5 text-[11px] font-semibold ${
                        selectedModel === model.id
                          ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-100"
                          : "border-surface-700 bg-surface-950 text-surface-400 hover:border-surface-600"
                      }`}
                      title={model.description || model.name}
                    >
                      {model.name}
                    </button>
                  ))}
                </div>
              ) : null}
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
                  onClick={() => void agentClient.reloadDevServer({ mode: (devStatus?.framework || "").match(/^(expo|react-native)$/i) ? "bundle" : "dev" })}
                  disabled={!devStatus?.running}
                  className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                >
                  Refresh Preview
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
              <div className="mt-3 grid gap-2">
                {deployQuickActions.map((action) => (
                  <div key={action.kind} className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <div className="text-sm font-semibold text-surface-100">{action.label}</div>
                        <div className="mt-1 text-[11px] leading-5 text-surface-500">{action.description}</div>
                      </div>
                      <span
                        className={`rounded-full px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] ${
                          action.enabled
                            ? "border border-emerald-500/30 bg-emerald-500/10 text-emerald-100"
                            : "border border-amber-500/20 bg-amber-500/10 text-amber-100"
                        }`}
                      >
                        {action.enabled ? "ready" : "check host"}
                      </span>
                    </div>
                    <div className="mt-3 text-[11px] text-surface-500">{action.readiness}</div>
                    <div className="mt-3 flex flex-wrap gap-2">
                      <button
                        onClick={() => void launchDeployTask(action.kind)}
                        disabled={!selectedProject}
                        className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-100 hover:bg-emerald-500/15 disabled:opacity-40"
                      >
                        Run with agent
                      </button>
                      <button
                        onClick={() => prepareDeployPrompt(action.kind)}
                        disabled={!selectedProject}
                        className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                      >
                        Draft prompt
                      </button>
                    </div>
                  </div>
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
              <div className="flex items-center gap-2">
                <div className="text-sm font-semibold text-surface-100 flex-1">
                  {activeTask?.title || "New coding session"}
                </div>
                {/* Video summary chip — same shape as the mobile chip,
                    just rendered as a button next to the title. Opens
                    a fullscreen <video> when clicked. */}
                {activeTask?.videoStatus === "ready" && activeTask?.videoClipId ? (
                  <button
                    onClick={() => setActiveClipId(activeTask.videoClipId!)}
                    className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-semibold text-emerald-300 hover:bg-emerald-500/20"
                  >
                    ▶ Watch demo
                  </button>
                ) : activeTask?.videoStatus === "recording" || activeTask?.videoStatus === "queued" ? (
                  <span className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-semibold text-amber-300">
                    🎬 {activeTask.videoStatus}…
                  </span>
                ) : null}
              </div>
              <div className="text-xs text-surface-500">
                {activeTask
                  ? `${activeTask.status} · ${selectedProject?.path || ""}`
                  : "Project-scoped remote coding through the connected machine"}
              </div>
            </div>

            <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
              {conversationTurns.length > 0 || showLiveOutput ? (
                <div className="space-y-4">
                  {conversationTurns.map((turn, index) => (
                    <div
                      key={`${turn.role}:${turn.timestamp}:${index}`}
                      className={`max-w-[88%] rounded-2xl border px-4 py-3 ${
                        turn.role === "user"
                          ? "ml-auto border-indigo-500/30 bg-indigo-500/10 text-surface-100"
                          : "border-surface-800 bg-surface-900/70 text-surface-200"
                      }`}
                    >
                      <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                        {turn.role === "user" ? "You" : "Agent"}
                      </div>
                      <pre className="whitespace-pre-wrap font-mono text-[13px] leading-6">{turn.content}</pre>
                    </div>
                  ))}
                  {showLiveOutput ? (
                    <div className="max-w-[92%] rounded-2xl border border-amber-500/20 bg-[#14110a] px-4 py-3 text-surface-200">
                      <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-amber-300">
                        {activeTask?.status === "running" ? "Live output" : "Agent output"}
                      </div>
                      <pre className="whitespace-pre-wrap font-mono text-[13px] leading-6">{liveOutput}</pre>
                    </div>
                  ) : null}
                </div>
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
              <label className="mt-3 flex cursor-pointer items-center gap-2 text-xs text-surface-400 select-none">
                <input
                  type="checkbox"
                  className="h-3.5 w-3.5 rounded border-surface-600 bg-surface-950 text-indigo-500 focus:ring-1 focus:ring-indigo-500"
                  checked={videoSummaryEnabled}
                  onChange={(e) => setVideoSummaryEnabled(e.target.checked)}
                />
                🎬 Record demo video when this task finishes
              </label>
              <div className="mt-3 flex items-center justify-between gap-3">
                <div className="text-xs text-surface-500">
                  Task is scoped to {selectedProject?.name || "the selected project"} and runs on {connectedMachine?.name || connectedDevice?.name || "the connected machine"}.
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => void agentClient.reloadDevServer({ mode: (devStatus?.framework || "").match(/^(expo|react-native)$/i) ? "bundle" : "dev" })}
                    disabled={!devStatus?.running}
                    className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Refresh Preview
                  </button>
                  <button
                    onClick={() => void continueChatTask()}
                    disabled={!activeTask || !composer.trim() || selectedRunnerRow?.ready === false}
                    className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Continue
                  </button>
                  <button
                    onClick={() => void startChatTask()}
                    disabled={!selectedProject || !composer.trim() || selectedRunnerRow?.ready === false}
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
  machine,
}: {
  project: Project;
  prompt: string;
  gitStatus: GitStatusRow | null;
  deployTargets: Array<{ id: string; name: string }>;
  machine: MachineInfo | null;
}): string {
  const machineLines = describeMachineForPrompt(machine);
  const lines = [
    `You are working for a solo developer in project ${project.name}.`,
    `Project path: ${project.path}`,
    project.framework ? `Framework: ${project.framework}` : "",
    gitStatus?.branch ? `Current branch: ${gitStatus.branch}` : "",
    gitStatus ? `Git state: ${gitStatus.clean ? "clean" : "dirty"}, ahead=${gitStatus.ahead || 0}, behind=${gitStatus.behind || 0}, staged=${gitStatus.staged?.length || 0}, modified=${gitStatus.modified?.length || 0}, untracked=${gitStatus.untracked?.length || 0}` : "",
    deployTargets.length > 0 ? `Known deploy targets: ${deployTargets.map((target) => target.name).join(", ")}` : "Known deploy targets: none detected yet",
    ...machineLines,
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
  machine,
}: {
  project: Project | null;
  prompt: string;
  gitStatus: GitStatusRow | null;
  deployTargets: Array<{ id: string; name: string }>;
  machine: MachineInfo | null;
}): string {
  const lines = [
    project ? `Continue in project ${project.name} (${project.path}).` : "",
    gitStatus?.branch ? `Current branch: ${gitStatus.branch}` : "",
    deployTargets.length > 0 ? `Deploy targets still available: ${deployTargets.map((target) => target.name).join(", ")}` : "",
    ...describeMachineForPrompt(machine),
    "Keep the solo-developer flow: sync/rebase if needed, resolve conflicts, finish the git operation cleanly, commit, push, and deploy when the request implies shipping.",
    "",
    prompt,
  ].filter(Boolean);
  return lines.join("\n");
}

function describeMachineForPrompt(machine: MachineInfo | null): string[] {
  if (!machine) return [];
  const caps = machine.capabilities;
  const readyRunners = (caps?.runners || []).filter((runner) => runner.ready).map((runner) => runner.name);
  return [
    `Execution machine: ${machine.name} (${machine.platform || machine.os || "unknown platform"})`,
    caps
      ? `Machine capabilities: iOS=${caps.supportsIos ? "yes" : "no"}, Android=${caps.supportsAndroid ? "yes" : "no"}, TestFlight=${caps.supportsTestFlight ? "yes" : "no"}, PlayStore=${caps.supportsPlayStore ? "yes" : "no"}, Docker=${caps.supportsDocker ? "yes" : "no"}`
      : "",
    readyRunners.length > 0 ? `Ready coding runners on this machine: ${readyRunners.join(", ")}` : "",
    machine.currentWorkDir ? `Machine current work dir: ${machine.currentWorkDir}` : "",
    "Before doing platform-specific release work, verify the host actually has the required signing, CLI auth, team configuration, and build tooling. If something is missing, fix it when feasible or report the exact blocker clearly.",
  ].filter(Boolean);
}

function buildDeployQuickActions(
  project: Project | null,
  machine: MachineInfo | null,
  deployTargets: Array<{ id: string; name: string }>,
): Array<{ kind: DeployActionKind; label: string; description: string; readiness: string; enabled: boolean }> {
  const caps = machine?.capabilities;
  const framework = (project?.framework || "").toLowerCase();
  const projectLooksMobile = framework.includes("expo") || framework.includes("react-native") || framework.includes("flutter") || !project?.framework;
  const hasEasTarget = deployTargets.some((target) => /eas|expo/i.test(target.name) || /eas|expo/i.test(target.id));

  return [
    {
      kind: "testflight",
      label: "TestFlight",
      description: "Build, sign, upload, and verify the iOS lane on a machine that can actually handle Apple tooling.",
      readiness: caps?.supportsTestFlight
        ? "Connected machine advertises TestFlight-capable iOS tooling."
        : "Needs a macOS host with Xcode, Apple credentials, signing assets, and team configuration.",
      enabled: !!caps?.supportsTestFlight && projectLooksMobile,
    },
    {
      kind: "play-internal",
      label: "Google Play Internal",
      description: "Ship an Android build to the internal testing track and confirm the upload state.",
      readiness: caps?.supportsPlayStore
        ? "Connected machine advertises Android/Play deployment capability."
        : "Needs Android build tooling, signing config, and Play Console credentials on the host.",
      enabled: !!caps?.supportsPlayStore && projectLooksMobile,
    },
    {
      kind: "eas",
      label: "EAS Deploy",
      description: "Use Expo/EAS from the current machine for updates or builds, depending on what the project supports.",
      readiness: hasEasTarget
        ? "Project already exposes an EAS/Expo deploy target."
        : "Agent will inspect `eas.json`, Expo auth, channels, and build profiles before choosing update vs build.",
      enabled: (framework.includes("expo") || framework.includes("react-native")) && projectLooksMobile,
    },
  ];
}

function buildDeployTaskPlan({
  kind,
  project,
  machine,
  deployTargets,
  gitStatus,
}: {
  kind: DeployActionKind;
  project: Project;
  machine: MachineInfo | null;
  deployTargets: Array<{ id: string; name: string }>;
  gitStatus: GitStatusRow | null;
}) {
  const targetLabel =
    kind === "testflight" ? "TestFlight" : kind === "play-internal" ? "Google Play Internal" : "EAS";
  const hostLine = machine
    ? `Run this on ${machine.name} (${machine.platform || machine.os || "unknown platform"}).`
    : "Run this on the currently connected machine.";
  const capabilityLine = machine?.capabilities
    ? `Host capabilities: iOS=${machine.capabilities.supportsIos ? "yes" : "no"}, Android=${machine.capabilities.supportsAndroid ? "yes" : "no"}, TestFlight=${machine.capabilities.supportsTestFlight ? "yes" : "no"}, PlayStore=${machine.capabilities.supportsPlayStore ? "yes" : "no"}.`
    : "";
  const deployLine = deployTargets.length > 0
    ? `Detected deploy targets: ${deployTargets.map((target) => target.name).join(", ")}.`
    : "No deploy target was auto-detected yet, so inspect the repo and toolchain before you decide the release path.";
  const branchLine = gitStatus?.branch ? `Current branch: ${gitStatus.branch}.` : "";
  const dirtyLine = gitStatus
    ? `Git state: ${gitStatus.clean ? "clean" : "dirty"}, ahead=${gitStatus.ahead || 0}, behind=${gitStatus.behind || 0}.`
    : "";
  const platformRequest =
    kind === "testflight"
      ? "Prepare and ship this project to TestFlight. Verify Xcode, Apple team selection, signing certificates/profiles, bundle identifiers, and any App Store Connect credentials before building. If the current host cannot do it, explain exactly why."
      : kind === "play-internal"
        ? "Prepare and ship this project to Google Play internal testing. Verify Gradle/Android tooling, keystore/signing config, Play Console service credentials, package id, and release track configuration before building. If the current host cannot do it, explain exactly why."
        : "Prepare and ship this project through Expo/EAS. Decide whether this should be an EAS Update, EAS Build, or EAS Submit flow after inspecting the repo. Verify `eas.json`, channels/profiles, Expo auth, Apple/Google credentials, and any required project secrets before doing the release.";

  const prompt = [
    `You are shipping project ${project.name} at ${project.path}.`,
    project.framework ? `Framework: ${project.framework}.` : "",
    hostLine,
    capabilityLine,
    branchLine,
    dirtyLine,
    deployLine,
    "",
    platformRequest,
    "Execution contract:",
    "1. Inspect the repository and the host first. Do not guess whether the machine can sign or submit builds.",
    "2. If the branch is behind/diverged, fetch and rebase cleanly before release work unless that would be unsafe.",
    "3. Check all release prerequisites on the host: CLI availability, auth state, signing/team settings, env vars, and project config.",
    "4. If a prerequisite is missing but fixable from this machine, fix it and continue.",
    "5. If a prerequisite cannot be fixed here, stop before a broken release and report the exact missing requirement.",
    "6. If release succeeds, include the artifact/build id/submission state and the next place the user should check.",
    "7. Trigger preview reload only when it helps validate the shipped change locally first.",
  ].filter(Boolean).join("\n");

  return {
    title: `${project.name} — deploy ${targetLabel}`,
    userPrompt: `Ship this project to ${targetLabel}. Check whether the connected machine is actually capable first, then handle the missing setup or complete the release.`,
    prompt,
    startingLabel: `Starting ${targetLabel} workflow…`,
    startedLabel: `Started ${targetLabel} workflow.`,
    preparedLabel: `Prepared ${targetLabel} deploy prompt.`,
  };
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
