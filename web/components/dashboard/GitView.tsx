"use client";

import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import {
  agentClient,
  type GitBranchRow,
  type GitCommitRow,
  type ManagedGitProjectMeta,
  type GitProviderStatusRow,
  type GitRemoteRepo,
  type GitStatusRow,
} from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type Project = {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  tags?: string[];
};

type ProjectAction = {
  label: string;
  target: string;
  type: string;
  framework?: string;
  platform?: string;
  command?: string;
  icon?: string;
  supported?: boolean;
  reason?: string;
};

type Props = {
  onOpenSurface?: (surface: "chat" | "preview" | "web-reload" | "builds", projectPath: string) => void;
  /** Optional: open the chat tab for `projectPath` with the supplied
   *  prompt pre-filled in the composer. Used by the "Pull (Rebase)"
   *  action so the user gets a one-click rebase via the Vibing flow
   *  without having to type the prompt themselves. */
  onVibePrompt?: (projectPath: string, prompt: string) => void;
  /** Paired devices — used to populate the "Configure on" picker so the
   *  user can push GitHub/GitLab creds onto any owned online machine
   *  (remote runner, managed cloud, …) without first reconnecting to
   *  it as primary. When absent the view targets only the connected
   *  agent. */
  devices?: Device[];
};

const MOBILE_FRAMEWORKS = ["expo", "react-native", "flutter", "swift", "kotlin"];
const WEB_FRAMEWORKS = ["nextjs", "vite", "react", "astro", "remix"];

function normalizeFramework(value?: string) {
  return String(value || "").trim().toLowerCase();
}

function isMobileFramework(framework?: string) {
  const normalized = normalizeFramework(framework);
  return MOBILE_FRAMEWORKS.some((item) => normalized.includes(item));
}

function isWebFramework(framework?: string) {
  const normalized = normalizeFramework(framework);
  return WEB_FRAMEWORKS.some((item) => normalized.includes(item));
}

function preferredSurfaceForAction(action: ProjectAction): "preview" | "web-reload" | "builds" | null {
  if (action.type === "vibing") return null;
  if (action.type === "build" || action.type === "deploy") return "builds";
  if (action.type === "dev-server") {
    if (isMobileFramework(action.framework)) return "preview";
    if (isWebFramework(action.framework)) return "web-reload";
  }
  return null;
}

function actionLabelForSurface(action: ProjectAction, surface: "preview" | "web-reload" | "builds") {
  if (surface === "preview" || surface === "web-reload") return "Webview";
  if (action.type === "build") return action.label || "Builds";
  if (action.type === "deploy") return "Builds";
  return "Builds";
}

function providerTokenHelp(provider: "github" | "gitlab") {
  if (provider === "github") {
    return {
      href: "https://github.com/settings/personal-access-tokens/new",
      label: "How to create a GitHub token",
      hint: "Use a GitHub fine-grained token with repository access for private repositories.",
    };
  }
  return {
    href: "https://gitlab.com/-/user_settings/personal_access_tokens",
    label: "How to create a GitLab token",
    hint: "Use a GitLab token with api scope for repo traversal and clone.",
  };
}

function ProviderIcon({ provider, className = "h-4 w-4" }: { provider: "github" | "gitlab"; className?: string }) {
  if (provider === "gitlab") {
    return (
      <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
        <path d="M12 22.4 16.4 8.9h-8.8L12 22.4Z" />
        <path d="M12 22.4 7.6 8.9H1.8L12 22.4Z" />
        <path d="M1.8 8.9.5 13a.9.9 0 0 0 .33 1.01L12 22.4 1.8 8.9Z" />
        <path d="M1.8 8.9h5.8L5.1 1.2a.45.45 0 0 0-.86 0L1.8 8.9Z" />
        <path d="M12 22.4 16.4 8.9h5.8L12 22.4Z" />
        <path d="M22.2 8.9 23.5 13a.9.9 0 0 1-.33 1.01L12 22.4 22.2 8.9Z" />
        <path d="M22.2 8.9h-5.8l2.5-7.7a.45.45 0 0 1 .86 0l2.5 7.7Z" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
      <path d="M12 .75a11.25 11.25 0 0 0-3.56 21.92c.56.1.76-.24.76-.54v-2.07c-3.1.67-3.76-1.31-3.76-1.31-.5-1.29-1.24-1.63-1.24-1.63-1.02-.69.08-.67.08-.67 1.12.08 1.72 1.16 1.72 1.16 1 .17 1.96 1.42 1.96 1.42.89 1.52 2.33 1.08 2.9.82.09-.72.35-1.08.63-1.33-2.47-.28-5.07-1.23-5.07-5.5 0-1.22.43-2.22 1.15-3-.12-.28-.5-1.42.11-2.96 0 0 .93-.3 3.06 1.14a10.7 10.7 0 0 1 5.58 0c2.13-1.44 3.06-1.14 3.06-1.14.61 1.54.23 2.68.11 2.96.72.78 1.15 1.78 1.15 3 0 4.28-2.61 5.22-5.1 5.49.4.35.75 1.04.75 2.1v3.11c0 .3.2.65.77.54A11.25 11.25 0 0 0 12 .75Z" />
    </svg>
  );
}

export default function GitView({ onOpenSurface, onVibePrompt, devices = [] }: Props) {
  const [projects, setProjects] = useState<Project[]>([]);
  // Target = which machine receives git creds and clones. null/undefined
  // means "this machine" (the connected agent). Anything else is an
  // owned peer the agent forwards to via /peer/<id>/. The picker only
  // affects provider state + repo browse + clone; project listing /
  // status / branches still query the connected agent because they
  // operate on the local filesystem the dashboard already shows.
  const [targetDeviceId, setTargetDeviceId] = useState<string | undefined>(undefined);
  const peerTargets = useMemo(
    () =>
      devices
        .filter((d) => d.online && d.deviceClass !== "edge-mobile")
        .map((d) => ({ id: d.id, name: d.name })),
    [devices],
  );
  const targetOptions = useMemo(
    () => [{ id: undefined as string | undefined, name: "This machine" }, ...peerTargets],
    [peerTargets],
  );
  const targetLabel =
    targetOptions.find((option) => option.id === targetDeviceId)?.name || "This machine";
  const [expandedProjectPath, setExpandedProjectPath] = useState("");
  const [gitStatus, setGitStatus] = useState<GitStatusRow | null>(null);
  const [gitBranches, setGitBranches] = useState<GitBranchRow[]>([]);
  const [gitCommits, setGitCommits] = useState<GitCommitRow[]>([]);
  const [managedGit, setManagedGit] = useState<ManagedGitProjectMeta | null>(null);
  const [projectActions, setProjectActions] = useState<ProjectAction[]>([]);

  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [repoBrowserHost, setRepoBrowserHost] = useState<string | null>(null);
  const [providerRepos, setProviderRepos] = useState<GitRemoteRepo[]>([]);
  const [providerSearch, setProviderSearch] = useState("");
  const [providerToken, setProviderToken] = useState("");
  const [manualProvider, setManualProvider] = useState<"github" | "gitlab" | null>(null);
  const [linkedGitProviders, setLinkedGitProviders] = useState<string[]>([]);
  const [linkingGitProvider, setLinkingGitProvider] = useState<"github" | "gitlab" | null>(null);
  const [gitLinkError, setGitLinkError] = useState<string | null>(null);
  const [dropboxFlow, setDropboxFlow] = useState<{ sessionId: string; authUrl: string } | null>(null);
  const [dropboxCode, setDropboxCode] = useState("");

  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);

  // Device Flow session — when non-null, an OAuth approval is in flight
  // on `targetDeviceId`. UI shows the user_code + verification URL and
  // polls until the agent signals done|error|expired. Switching the
  // target chip aborts the local poll loop (the remote agent keeps its
  // own session for 30 minutes either way).
  type DeviceFlowSession = {
    sessionId: string;
    provider: "github" | "gitlab";
    host: string;
    userCode: string;
    verificationUri: string;
    interval: number;
    expiresAt: number;
    state: "pending" | "done" | "error" | "expired" | "unknown";
    username?: string;
    error?: string;
    byoClient?: boolean;
  };
  const [deviceFlow, setDeviceFlow] = useState<DeviceFlowSession | null>(null);
  const [deviceFlowStarting, setDeviceFlowStarting] = useState<"github" | "gitlab" | null>(null);

  const expandedProject = useMemo(
    () => projects.find((project) => project.path === expandedProjectPath) || null,
    [projects, expandedProjectPath],
  );

  const filteredProviderRepos = useMemo(() => {
    const needle = providerSearch.trim().toLowerCase();
    if (!needle) return providerRepos;
    return providerRepos.filter((repo) =>
      repo.name.toLowerCase().includes(needle) ||
      repo.fullName.toLowerCase().includes(needle) ||
      String(repo.description || "").toLowerCase().includes(needle),
    );
  }, [providerRepos, providerSearch]);

  async function refreshLinkedGitProviders() {
    if (typeof window === "undefined") return;
    const token =
      localStorage.getItem("yaver_auth_token") ||
      document.cookie
        .split(";")
        .find((c) => c.trim().startsWith("yaver_auth_token="))
        ?.split("=")[1] ||
      null;
    if (!token) {
      setLinkedGitProviders([]);
      return;
    }
    try {
      const res = await fetch(`${CONVEX_URL}/auth/providers`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) return;
      const data = await res.json().catch(() => ({}));
      const providers = Array.isArray(data?.identities)
        ? data.identities
            .map((identity: { provider?: string }) => String(identity?.provider || "").toLowerCase())
            .filter((provider: string) => provider === "github" || provider === "gitlab")
        : [];
      setLinkedGitProviders(Array.from(new Set(providers)));
    } catch {
      // soft-fail
    }
  }

  useEffect(() => {
    let cancelled = false;
    const refresh = async () => {
      try {
        const [projectRows, gitProviders] = await Promise.all([
          agentClient.listProjects().catch(() => []),
          agentClient.gitProviderStatus(targetDeviceId).catch(() => []),
        ]);
        if (cancelled) return;
        setProjects(projectRows);
        setProviders(gitProviders);
        void refreshLinkedGitProviders();
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };
    void refresh();
    return () => {
      cancelled = true;
    };
  }, [expandedProjectPath, refreshNonce, targetDeviceId]);

  useEffect(() => {
    if (!expandedProjectPath) {
      setGitStatus(null);
      setGitBranches([]);
      setGitCommits([]);
      setManagedGit(null);
      setProjectActions([]);
      return;
    }
    let cancelled = false;
    const refresh = async () => {
      try {
        const [status, branches, commits, actionsResult, managed] = await Promise.all([
          agentClient.gitStatus(expandedProjectPath).catch(() => null),
          agentClient.gitBranches(expandedProjectPath).catch(() => []),
          agentClient.gitLog(expandedProjectPath, 5).catch(() => []),
          agentClient.getProjectActions(expandedProjectPath).catch(() => ({ actions: [] })),
          agentClient.managedGitStatus({ workDir: expandedProjectPath }).catch(() => null),
        ]);
        if (cancelled) return;
        setGitStatus(status);
        setGitBranches(branches);
        setGitCommits(commits);
        setManagedGit(managed);
        setProjectActions(Array.isArray(actionsResult?.actions) ? actionsResult.actions : []);
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };
    void refresh();
    return () => {
      cancelled = true;
    };
  }, [expandedProjectPath, refreshNonce]);

  async function autoDetectProviders() {
    setBusy(`Detecting git providers on ${targetLabel}…`);
    const detected = await agentClient.gitProviderDetect(targetDeviceId);
    setProviders(await agentClient.gitProviderStatus(targetDeviceId));
    setBusy(detected.length > 0 ? `Detected ${detected.map((item) => item.provider).join(", ")} on ${targetLabel}.` : `No git providers detected on ${targetLabel}.`);
  }

  async function saveProviderToken() {
    if (!manualProvider || !providerToken.trim()) return;
    setBusy(`Saving ${manualProvider} token to ${targetLabel}'s vault…`);
    const result = await agentClient.gitProviderSetup(
      { provider: manualProvider, token: providerToken.trim() },
      targetDeviceId,
    );
    if (!result.ok) {
      setBusy(result.error || "Could not save provider token.");
      return;
    }
    setProviderToken("");
    setManualProvider(null);
    setProviders(await agentClient.gitProviderStatus(targetDeviceId));
    setBusy(`Connected ${result.provider || manualProvider} as ${result.username || "user"} on ${targetLabel}.`);
  }

  async function startGitAccountLink(provider: "github" | "gitlab") {
    if (typeof window === "undefined") return;
    const token =
      localStorage.getItem("yaver_auth_token") ||
      document.cookie
        .split(";")
        .find((c) => c.trim().startsWith("yaver_auth_token="))
        ?.split("=")[1] ||
      null;
    if (!token) {
      setGitLinkError("Not signed in. Refresh the dashboard and sign in again.");
      return;
    }
    setGitLinkError(null);
    setLinkingGitProvider(provider);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/oauth-link/start`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          provider,
          client: "web",
          returnTo: "/dashboard",
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.token) {
        throw new Error(data?.error || `Failed to start ${provider} link`);
      }
      window.location.href = `/api/auth/oauth/${provider}?client=web&intent=link&linkToken=${encodeURIComponent(data.token)}&return=${encodeURIComponent("/dashboard")}`;
    } catch (error) {
      setGitLinkError(error instanceof Error ? error.message : `Failed to start ${provider} link`);
      setLinkingGitProvider(null);
    }
  }

  async function toggleProviderRepos(host: string) {
    if (repoBrowserHost === host) {
      setRepoBrowserHost(null);
      setProviderRepos([]);
      return;
    }
    setRepoBrowserHost(host);
    setBusy(`Loading repos from ${host} on ${targetLabel}…`);
    const repos = await agentClient.gitProviderRepos(host, targetDeviceId);
    setProviderRepos(repos);
    setBusy(`Loaded ${repos.length} repos from ${host}.`);
  }

  async function cloneRemoteRepo(repo: GitRemoteRepo) {
    const url = repo.sshUrl || repo.cloneUrl;
    if (!url) return;
    setBusy(`Cloning ${repo.fullName} on ${targetLabel}…`);
    const result = await agentClient.cloneRepo(url, targetDeviceId);
    if (!result?.ok) {
      setBusy(result?.error || `Could not clone ${repo.fullName}.`);
      return;
    }
    // Project listing always reflects the connected agent, not the
    // remote target — listProjects has no peer-target plumbing yet,
    // and cloning to a peer doesn't show up in this dashboard's
    // project list anyway. Still nudge a refresh so a local clone
    // appears immediately.
    const nextProjects = await agentClient.listProjects().catch(() => []);
    setProjects(nextProjects);
    if (result.path && nextProjects.some((project: Project) => project.path === result.path)) {
      setExpandedProjectPath(result.path);
    }
    setRefreshNonce((value) => value + 1);
    setBusy(`Cloned ${repo.fullName}${result.path ? ` → ${result.path}` : ""} on ${targetLabel}.`);
  }

  async function removeProvider(host: string) {
    await agentClient.gitProviderRemove(host, targetDeviceId);
    setProviders(await agentClient.gitProviderStatus(targetDeviceId));
    if (repoBrowserHost === host) {
      setRepoBrowserHost(null);
      setProviderRepos([]);
    }
    setBusy(`Removed ${host} from ${targetLabel}.`);
  }

  async function startDeviceFlow(provider: "github" | "gitlab") {
    setDeviceFlowStarting(provider);
    setBusy(`Starting ${provider} Device Flow on ${targetLabel}…`);
    try {
      const start = await agentClient.gitOAuthStart({ provider }, targetDeviceId);
      if (!start.ok || !start.session_id || !start.user_code || !start.verification_uri) {
        setBusy(start.error || `Could not start ${provider} Device Flow.`);
        return;
      }
      setDeviceFlow({
        sessionId: start.session_id,
        provider,
        host: start.host || (provider === "github" ? "github.com" : "gitlab.com"),
        userCode: start.user_code,
        verificationUri: start.verification_uri,
        interval: start.interval || 5,
        expiresAt: start.expires_at || 0,
        state: "pending",
        byoClient: !!start.byo_client,
      });
      setBusy(`Waiting for approval at ${start.verification_uri}…`);
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    } finally {
      setDeviceFlowStarting(null);
    }
  }

  // Poll the active Device Flow session at the agent-prescribed
  // interval. Stops when state moves out of 'pending' or when the
  // user closes the card. Restarts cleanly when the user starts a new
  // session or switches targets — the dependency on targetDeviceId
  // ensures we don't accidentally poll yesterday's session against
  // today's machine.
  useEffect(() => {
    if (!deviceFlow || deviceFlow.state !== "pending") return;
    let cancelled = false;
    const intervalMs = Math.max(2, deviceFlow.interval) * 1000;
    const timer = setInterval(async () => {
      if (cancelled) return;
      try {
        const status = await agentClient.gitOAuthStatus(deviceFlow.sessionId, targetDeviceId);
        if (cancelled) return;
        if (!status.state || status.state === "pending") return;
        setDeviceFlow((prev) =>
          prev && prev.sessionId === deviceFlow.sessionId
            ? {
                ...prev,
                state: status.state || "unknown",
                username: status.username,
                error: status.error,
              }
            : prev,
        );
        if (status.state === "done") {
          setBusy(`Connected ${deviceFlow.provider} as ${status.username || "user"} on ${targetLabel}.`);
          setProviders(await agentClient.gitProviderStatus(targetDeviceId));
        } else if (status.state === "error" || status.state === "expired" || status.state === "unknown") {
          setBusy(status.error || `Device Flow ended (${status.state}).`);
        }
      } catch {
        // soft-fail: keep polling
      }
    }, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [deviceFlow?.sessionId, deviceFlow?.state, deviceFlow?.interval, targetDeviceId, targetLabel]);

  async function runGitAction(action: "revert-head") {
    if (!expandedProject) return;
    try {
      if (action === "revert-head") {
        const head = gitCommits[0]?.hash;
        if (!head) {
          setBusy("No commit available to revert.");
          return;
        }
        setBusy(`Reverting ${gitCommits[0]?.shortHash || "HEAD"}…`);
        const result = await agentClient.gitRevert(expandedProject.path, head);
        setBusy(result.message || `Reverted ${gitCommits[0]?.shortHash || "HEAD"}.`);
      }
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    } finally {
      setRefreshNonce((value) => value + 1);
    }
  }

  async function checkoutBranch(branch: string) {
    if (!expandedProject || !branch) return;
    try {
      setBusy(`Checking out ${branch}…`);
      const result = await agentClient.gitCheckout(expandedProject.path, branch);
      setBusy(result.message || `Checked out ${branch}.`);
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    } finally {
      setRefreshNonce((value) => value + 1);
    }
  }

  async function refreshManagedGit() {
    if (!expandedProjectPath) return;
    const managed = await agentClient.managedGitStatus({ workDir: expandedProjectPath }).catch(() => null);
    setManagedGit(managed);
  }

  async function enableManagedGitForProject(): Promise<ManagedGitProjectMeta | null> {
    if (!expandedProject) return null;
    try {
      setBusy(`Turning on Yaver Git for ${expandedProject.name}…`);
      const meta = await agentClient.managedGitEnable({
        workDir: expandedProject.path,
        name: expandedProject.name,
        visibility: "private",
      });
      setManagedGit(meta);
      setBusy("Yaver Git is on. Dropbox, GitHub, GitLab, and local backups can all be added together.");
      return meta;
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
      return null;
    }
  }

  async function runManagedGitBackup(targetKind?: "dropbox") {
    if (!expandedProject) return;
    try {
      if (!managedGit?.enabled) {
        const meta = await enableManagedGitForProject();
        if (!meta?.enabled) return;
      }
      if (targetKind === "dropbox") {
        const status = await agentClient.managedGitDropboxStatus();
        if (!status.connected) {
          const start = await agentClient.managedGitDropboxOAuthStart();
          setDropboxFlow({ sessionId: start.sessionId, authUrl: start.authUrl });
          setDropboxCode("");
          if (typeof window !== "undefined") window.open(start.authUrl, "_blank", "noopener,noreferrer");
          setBusy("Dropbox approval opened. Paste the returned code here to finish from the web UI.");
          return;
        }
        await agentClient.managedGitBackupCopy({ workDir: expandedProject.path, targetKind: "dropbox" });
        setBusy("Copied a recoverable Yaver Git bundle to Dropbox.");
      } else {
        await agentClient.managedGitBackupRun({ workDir: expandedProject.path });
        setBusy("Created a local recoverable Yaver Git bundle.");
      }
      await refreshManagedGit();
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    }
  }

  async function finishDropboxSync() {
    if (!expandedProject || !dropboxFlow || !dropboxCode.trim()) return;
    try {
      setBusy("Connecting Dropbox and syncing Yaver Git…");
      await agentClient.managedGitDropboxOAuthSubmit({
        sessionId: dropboxFlow.sessionId,
        code: dropboxCode.trim(),
      });
      await agentClient.managedGitBackupCopy({ workDir: expandedProject.path, targetKind: "dropbox" });
      setDropboxFlow(null);
      setDropboxCode("");
      await refreshManagedGit();
      setBusy("Dropbox is connected and has a recoverable git bundle. Other mirrors remain available.");
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    }
  }

  async function mirrorManagedGit(provider: "github" | "gitlab") {
    if (!expandedProject) return;
    try {
      if (!managedGit?.enabled) {
        const meta = await enableManagedGitForProject();
        if (!meta?.enabled) return;
      }
      const repoName = (managedGit?.repoId || expandedProject.name || "yaver-project")
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-+|-+$/g, "");
      const result = await agentClient.managedGitMirrorConnect({
        workDir: expandedProject.path,
        provider,
        repoName,
        visibility: "private",
        description: `${expandedProject.name} managed by Yaver Git`,
      });
      await refreshManagedGit();
      setBusy(`Mirrored Yaver Git to ${result.mirror?.fullName || provider}.`);
    } catch (error) {
      const msg = error instanceof Error ? error.message : String(error);
      setBusy(msg.includes("token") ? `Connect ${provider} under Clone From Remotes, then try the mirror again.` : msg);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-surface-800 bg-surface-900/60 px-4 py-3">
        <div>
          <h2 className="text-base font-semibold text-surface-100">Git</h2>
          <p className="mt-0.5 text-xs text-surface-400">
            See which repos live on this machine, open the right workflow for each one, and keep them in sync.
          </p>
        </div>
        <button
          onClick={() => void autoDetectProviders()}
          className="rounded-md border border-surface-700 bg-surface-800/60 px-2.5 py-1.5 text-xs font-semibold text-surface-200 hover:border-surface-600 hover:bg-surface-800"
        >
          Detect Providers
        </button>
      </div>

      <section className="rounded-lg border border-surface-800 bg-surface-900/50 p-3">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Repos On This Machine</div>
            <div className="mt-1 text-xs text-surface-500">Expand a repo for git state, launch surfaces, diffs, branches, and recent commits.</div>
          </div>
          <span className="text-xs text-surface-500">{projects.length}</span>
        </div>

        {projects.length === 0 ? (
          <div className="rounded-md border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-500">
            No project roots detected yet. Clone a repo from below or connect to a machine with existing repos.
          </div>
        ) : (
          <div className="space-y-3">
            {projects.map((project) => {
              const isExpanded = expandedProjectPath === project.path;
              const status = isExpanded ? gitStatus : null;
              const actions = isExpanded
                ? [
                    {
                      label: "Start Vibing",
                      target: ".",
                      type: "vibing",
                      supported: true,
                    } satisfies ProjectAction,
                    // Pull/Rebase action — drives the Vibing flow with
                    // a pre-canned prompt so the user gets one-click
                    // git pull --rebase + conflict resolution by the
                    // chosen primary agent. Hidden when no vibe-prompt
                    // handler is wired (older dashboard mounts).
                    ...(onVibePrompt
                      ? [{
                          label: "Pull (Rebase)",
                          target: ".",
                          type: "vibe-prompt-rebase",
                          supported: true,
                        } satisfies ProjectAction]
                      : []),
                    ...projectActions,
                  ]
                : [];
              return (
                <details
                  key={project.path}
                  open={isExpanded}
                  onToggle={(event) => {
                    const nextOpen = (event.currentTarget as HTMLDetailsElement).open;
                    setExpandedProjectPath(nextOpen ? project.path : "");
                  }}
                  className={`rounded-md border transition ${
                    isExpanded ? "border-indigo-500/30 bg-surface-950/80" : "border-surface-800 bg-surface-950/60"
                  }`}
                >
                  <summary className="cursor-pointer list-none p-4">
                    <div className="flex flex-wrap items-start justify-between gap-4">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <div className="text-base font-semibold text-surface-100">{project.name}</div>
                          <span className="text-[11px] text-surface-600">{isExpanded ? "Hide details" : "Show details"}</span>
                        </div>
                        <div className="mt-1 truncate text-[11px] text-surface-500">{project.path}</div>
                        <div className="mt-2 flex flex-wrap gap-1.5">
                          {project.branch ? <MiniPill>{project.branch}</MiniPill> : null}
                          {project.framework ? <MiniPill>{project.framework}</MiniPill> : null}
                          {status ? <MiniPill>{status.clean ? "clean" : "dirty"}</MiniPill> : null}
                        </div>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        <StatPill label="ahead" value={status?.ahead || 0} tone="success" />
                        <StatPill label="behind" value={status?.behind || 0} tone="warning" />
                        <StatPill
                          label="changes"
                          value={(status?.staged?.length || 0) + (status?.modified?.length || 0) + (status?.untracked?.length || 0)}
                          tone="info"
                        />
                      </div>
                    </div>
                  </summary>

                  {isExpanded ? (
                    <div className="border-t border-surface-800 px-4 pb-4 pt-4">
                      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.25fr),minmax(340px,0.9fr)]">
                        <div className="space-y-4">
                          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-4">
                            <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
                              <div>
                                <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Open Surface</div>
                                <div className="mt-1 text-xs text-surface-500">Only workflows the agent detects for this repo are shown here.</div>
                              </div>
                            </div>
                            <div className="flex flex-wrap gap-2">
                              {actions.length === 0 ? (
                                <div className="text-sm text-surface-500">No project-specific actions detected for this repo yet.</div>
                              ) : (
                                actions.map((action, index) => {
                                  const isVibing = action.type === "vibing";
                                  const isVibePrompt = String(action.type || "").startsWith("vibe-prompt");
                                  const surface = preferredSurfaceForAction(action);
                                  const supported = action.supported !== false;
                                  const label = isVibing
                                    ? "Start Vibing"
                                    : isVibePrompt
                                      ? action.label
                                      : surface
                                        ? actionLabelForSurface(action, surface)
                                        : action.label;
                                  const disabled = !supported || (!isVibing && !isVibePrompt && !surface);
                                  return (
                                    <button
                                      key={`${action.label}:${action.target}:${index}`}
                                      type="button"
                                      disabled={disabled}
                                      title={
                                        !supported
                                          ? action.reason || "Not supported on this machine yet."
                                          : isVibing
                                            ? `Open ${label}`
                                            : isVibePrompt
                                              ? "git pull --rebase via the Vibing flow on the device's primary coding agent. Conflicts get resolved by the agent."
                                              : surface
                                                ? `Open ${label}`
                                                : "This action isn't available from the dashboard."
                                      }
                                      onClick={() => {
                                        if (isVibePrompt && onVibePrompt) {
                                          // Hand off to the chat tab with a pre-canned
                                          // rebase prompt. The agent picks up the workdir
                                          // from preferredProjectPath and the runner
                                          // from the device's primary, so this is a
                                          // single-click "fast-forward me to origin
                                          // with conflict help".
                                          onVibePrompt(project.path, [
                                            "Run `git pull --rebase origin " + (project.branch || "main") + "` in this repo.",
                                            "If there are merge conflicts, resolve them in the smallest, most conservative way that keeps both intents — never delete code without telling me which file and which lines.",
                                            "When done, run `git log --oneline -5` and `git status` and report the result.",
                                          ].join("\n"));
                                          return;
                                        }
                                        if (!onOpenSurface) return;
                                        if (isVibing) {
                                          onOpenSurface("chat", project.path);
                                          return;
                                        }
                                        if (!surface) return;
                                        onOpenSurface(surface, project.path);
                                      }}
                                      className={`rounded-xl border px-3 py-2 text-xs font-semibold ${
                                        disabled
                                          ? "cursor-not-allowed border-surface-800 bg-surface-950 text-surface-600"
                                          : isVibing
                                            ? "border-fuchsia-500/30 bg-fuchsia-500/10 text-fuchsia-800 dark:text-fuchsia-100 hover:bg-fuchsia-500/15"
                                          : isVibePrompt
                                            ? "border-violet-500/30 bg-violet-500/10 text-violet-800 dark:text-violet-100 hover:bg-violet-500/15"
                                          : surface === "builds"
                                            ? "border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-100 hover:bg-amber-500/15"
                                            : surface === "web-reload"
                                              ? "border-sky-500/30 bg-sky-500/10 text-sky-800 dark:text-sky-100 hover:bg-sky-500/15"
                                              : "border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-100 hover:bg-emerald-500/15"
                                      }`}
                                    >
                                      {label}
                                    </button>
                                  );
                                })
                              )}
                            </div>
                          </div>

                          <div className="grid gap-3 sm:grid-cols-3">
                            <StatCard label="Remote Sync" value={`${status?.ahead || 0}↑ ${status?.behind || 0}↓`} sub="ahead / behind on current branch" />
                            <StatCard
                              label="Local State"
                              value={status?.clean ? "Clean" : String((status?.staged?.length || 0) + (status?.modified?.length || 0) + (status?.untracked?.length || 0))}
                              sub={status?.clean ? "no local changes" : "changed files on this machine"}
                            />
                            <StatCard label="Branch" value={status?.branch || project.branch || "none"} sub="active branch" />
                          </div>

                          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-4">
                            <div className="flex flex-wrap items-start justify-between gap-3">
                              <div className="min-w-0">
                                <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Yaver Git Sync</div>
                                <div className="mt-1 text-sm font-semibold text-surface-100">
                                  {managedGit?.enabled ? "Managed repo is on" : "Turn on zero-config repo storage"}
                                </div>
                                <div className="mt-1 max-w-2xl text-xs leading-5 text-surface-500">
                                  Yaver Git keeps this project on a managed main branch. Dropbox, GitHub, GitLab, local folders, and self-hosted boxes are extra copies you can enable together for recovery or later migration.
                                </div>
                              </div>
                              <MiniPill>{managedGit?.enabled ? managedGit.visibility : "not enabled"}</MiniPill>
                            </div>

                            {managedGit?.enabled ? (
                              <div className="mt-3 flex flex-wrap gap-1.5">
                                <MiniPill>{managedGit.defaultBranch || "main"}</MiniPill>
                                {managedGit.lastCommit ? <MiniPill>{managedGit.lastCommit.slice(0, 7)}</MiniPill> : null}
                                {managedGit.lastBackup ? <MiniPill>local backup</MiniPill> : null}
                                {(managedGit.externalBackups ?? []).map((backup, index) => (
                                  <MiniPill key={`${backup.targetKind}:${backup.path}:${index}`}>
                                    {managedBackupLabel(backup.targetKind)}
                                  </MiniPill>
                                ))}
                                {(managedGit.mirrors ?? []).map((mirror) => (
                                  <MiniPill key={`${mirror.provider}:${mirror.fullName}`}>
                                    {mirror.provider}: {mirror.fullName}
                                  </MiniPill>
                                ))}
                              </div>
                            ) : null}

                            {dropboxFlow ? (
                              <div className="mt-3 rounded-md border border-amber-500/30 bg-amber-500/10 p-3">
                                <div className="text-xs font-semibold text-amber-800 dark:text-amber-100">Finish Dropbox from this browser</div>
                                <div className="mt-1 text-[11px] leading-5 text-surface-400">
                                  Approve Yaver in Dropbox, then paste the returned code. This uploads a git bundle and does not disable other mirrors.
                                </div>
                                <div className="mt-3 flex flex-wrap gap-2">
                                  <a
                                    href={dropboxFlow.authUrl}
                                    target="_blank"
                                    rel="noreferrer"
                                    className="rounded-xl border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                                  >
                                    Open Dropbox
                                  </a>
                                  <input
                                    value={dropboxCode}
                                    onChange={(event) => setDropboxCode(event.target.value)}
                                    placeholder="Dropbox code"
                                    className="min-w-56 flex-1 rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                                  />
                                  <button
                                    type="button"
                                    onClick={() => void finishDropboxSync()}
                                    disabled={!dropboxCode.trim()}
                                    className="rounded-xl border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-800 hover:bg-emerald-500/15 disabled:opacity-40 dark:text-emerald-100"
                                  >
                                    Connect & sync
                                  </button>
                                </div>
                              </div>
                            ) : null}

                            <div className="mt-3 flex flex-wrap gap-2">
                              {!managedGit?.enabled ? (
                                <button
                                  type="button"
                                  onClick={() => void enableManagedGitForProject()}
                                  className="rounded-xl border border-brand/40 bg-brand-soft/40 px-3 py-2 text-xs font-semibold text-brand-softFg hover:border-brand/60 hover:bg-brand-soft"
                                >
                                  Turn on Yaver Git
                                </button>
                              ) : null}
                              <button
                                type="button"
                                onClick={() => void runManagedGitBackup()}
                                className="rounded-xl border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                              >
                                Local backup
                              </button>
                              <button
                                type="button"
                                onClick={() => void runManagedGitBackup("dropbox")}
                                className="rounded-xl border border-sky-500/40 bg-sky-500/10 px-3 py-2 text-xs font-semibold text-sky-800 hover:bg-sky-500/15 dark:text-sky-100"
                              >
                                Dropbox
                              </button>
                              <button
                                type="button"
                                onClick={() => void mirrorManagedGit("github")}
                                className="rounded-xl border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                              >
                                Mirror GitHub
                              </button>
                              <button
                                type="button"
                                onClick={() => void mirrorManagedGit("gitlab")}
                                className="rounded-xl border border-orange-500/40 bg-orange-500/10 px-3 py-2 text-xs font-semibold text-orange-800 hover:bg-orange-500/15 dark:text-orange-100"
                              >
                                Mirror GitLab
                              </button>
                            </div>
                          </div>
                        </div>

                        <div className="space-y-4">
                          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-4">
                            <div className="mb-3 flex items-center justify-between gap-3">
                              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Branches</div>
                              <MiniPill>{gitBranches.length} branches</MiniPill>
                            </div>
                            <div className="space-y-2">
                              {gitBranches.slice(0, 24).map((branch) => (
                                <button
                                  key={branch.name}
                                  onClick={() => void checkoutBranch(branch.name)}
                                  className={`flex w-full items-center justify-between rounded-xl border px-3 py-2 text-left ${
                                    branch.current
                                      ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-100"
                                      : "border-surface-800 bg-surface-950/70 text-surface-300 hover:border-surface-700"
                                  }`}
                                >
                                  <span className="truncate text-xs font-medium">{branch.name}</span>
                                  <span className="text-[10px] uppercase tracking-[0.16em]">{branch.current ? "current" : "checkout"}</span>
                                </button>
                              ))}
                              {gitBranches.length === 0 ? (
                                <div className="rounded-xl border border-dashed border-surface-800 bg-surface-950/70 p-3 text-sm text-surface-500">
                                  No branch data yet.
                                </div>
                              ) : null}
                            </div>
                          </div>

                          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-4">
                            <div className="mb-3 flex items-center justify-between gap-3">
                              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Recent Commits</div>
                              <MiniPill>last 5</MiniPill>
                            </div>
                            <div className="space-y-2">
                              {gitCommits.map((commit) => (
                                <div key={commit.hash} className="rounded-xl border border-surface-800 bg-surface-950/70 p-3">
                                  <div className="flex flex-wrap items-start justify-between gap-3">
                                    <div>
                                      <div className="text-sm font-semibold text-surface-100">{commit.message}</div>
                                      <div className="mt-1 text-[11px] text-surface-500">
                                        {commit.shortHash} · {commit.author} · {new Date(commit.date).toLocaleString()}
                                      </div>
                                    </div>
                                    <MiniPill>{commit.filesChanged} files</MiniPill>
                                  </div>
                                </div>
                              ))}
                              <button
                                onClick={() => void runGitAction("revert-head")}
                                disabled={gitCommits.length === 0}
                                className="w-full rounded-xl border border-red-500/30 px-3 py-2 text-xs font-semibold text-red-700 dark:text-red-300 hover:bg-red-500/10 disabled:opacity-40"
                              >
                                Revert Last Commit
                              </button>
                              {gitCommits.length === 0 ? (
                                <div className="rounded-xl border border-dashed border-surface-800 bg-surface-950/70 p-3 text-sm text-surface-500">
                                  No commit history available for this project.
                                </div>
                              ) : null}
                            </div>
                          </div>
                        </div>
                      </div>
                    </div>
                  ) : null}
                </details>
              );
            })}
          </div>
        )}
      </section>

      <section className="rounded-lg border border-surface-800 bg-surface-900/50 p-4">
        <details open>
          <summary className="cursor-pointer list-none">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Clone From Remotes</div>
                <div className="mt-1 text-xs text-surface-500">
                  Link GitHub or GitLab when you want to browse remote repos and clone them onto {targetLabel}.
                </div>
              </div>
              <span className="rounded-full border border-surface-700 px-2 py-1 text-[10px] text-surface-400">
                {providers.length} linked on {targetLabel}
              </span>
            </div>
          </summary>

          {peerTargets.length > 0 ? (
            <div className="mt-4 rounded-md border border-surface-800 bg-surface-950/70 p-3">
              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Configure on</div>
              <div className="mt-2 flex flex-wrap gap-2">
                {targetOptions.map((option) => {
                  const active = (option.id || undefined) === (targetDeviceId || undefined);
                  return (
                    <button
                      key={option.id || "__local__"}
                      type="button"
                      onClick={() => {
                        if ((option.id || undefined) === (targetDeviceId || undefined)) return;
                        setTargetDeviceId(option.id);
                        // Clear stale provider/repo state so we never
                        // show another machine's data on the chip
                        // switch.
                        setProviders([]);
                        setProviderRepos([]);
                        setRepoBrowserHost(null);
                        setManualProvider(null);
                        setProviderToken("");
                      }}
                      className={`rounded-xl border px-3 py-1.5 text-xs font-semibold transition-colors ${
                        active
                          ? "border-brand bg-brand text-white"
                          : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-600 hover:text-surface-100"
                      }`}
                    >
                      {option.name}
                    </button>
                  );
                })}
              </div>
              <div className="mt-2 text-[11px] text-surface-500">
                Tokens stored here go to <span className="text-surface-300">{targetLabel}</span>'s vault and never reach Yaver servers. Remote calls travel over QUIC/relay.
              </div>
            </div>
          ) : null}

          <div className="mt-4 space-y-3">
            <div className="rounded-md border border-surface-800 bg-surface-950/70 p-4">
              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Step 1 · Connect provider to Yaver</div>
              <div className="mt-2 text-sm text-surface-400">
                Connect GitHub or GitLab to this Yaver account for sign-in and recovery. Then add a machine token below when you want this box to browse and clone private repos.
              </div>
              <div className="mt-3 flex flex-wrap gap-2">
                {(["github", "gitlab"] as const).map((provider) => {
                  const linked = linkedGitProviders.includes(provider);
                  return (
                    <button
                      key={provider}
                      onClick={() => void startGitAccountLink(provider)}
                      disabled={linked || linkingGitProvider !== null}
                      className={`inline-flex items-center gap-2 rounded-xl px-3 py-2 text-xs font-semibold disabled:opacity-60 transition-colors ${
                        linked
                          ? "bg-success-soft text-success-softFg cursor-default"
                          : "border border-surface-700 bg-surface-950 text-surface-200 hover:border-brand/50 hover:text-brand-softFg"
                      }`}
                    >
                      <ProviderIcon provider={provider} className={`h-4 w-4 ${linked ? "" : provider === "gitlab" ? "text-warning" : "text-surface-300"}`} />
                      {linked ? (
                        <span className="inline-flex items-center gap-1">
                          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" strokeLinejoin="round" className="h-3 w-3" aria-hidden>
                            <polyline points="20 6 9 17 4 12" />
                          </svg>
                          {provider === "github" ? "GitHub" : "GitLab"} linked
                        </span>
                      ) : linkingGitProvider === provider ? "Opening…" : `Connect ${provider === "github" ? "GitHub" : "GitLab"}`}
                    </button>
                  );
                })}
              </div>
              {gitLinkError ? <div className="mt-2 text-xs text-danger">{gitLinkError}</div> : null}
            </div>

            <div className="rounded-md border border-surface-800 bg-surface-950/70 p-4">
              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Step 2 · Populate this machine</div>
              <div className="mt-2 text-sm text-surface-400">
                Approve a Device Flow login to push an OAuth token onto {targetLabel}, or paste a Personal Access Token if you prefer.
              </div>
            </div>

            <div className="flex flex-wrap gap-2">
              {(["github", "gitlab"] as const).map((p) => (
                <button
                  key={`oauth-${p}`}
                  onClick={() => void startDeviceFlow(p)}
                  disabled={deviceFlowStarting !== null || (deviceFlow?.state === "pending" && deviceFlow?.provider === p)}
                  className="inline-flex items-center gap-2 rounded-xl border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-800 dark:text-emerald-100 hover:border-emerald-500/60 hover:bg-emerald-500/15 disabled:opacity-50"
                >
                  <ProviderIcon provider={p} className={`h-4 w-4 ${p === "gitlab" ? "text-warning" : ""}`} />
                  {deviceFlowStarting === p
                    ? "Starting…"
                    : deviceFlow?.state === "pending" && deviceFlow?.provider === p
                      ? `Waiting for ${p === "github" ? "GitHub" : "GitLab"} approval…`
                      : `Sign in with ${p === "github" ? "GitHub" : "GitLab"}`}
                </button>
              ))}
            </div>

            {deviceFlow ? (
              <div
                className={`rounded-md border p-4 ${
                  deviceFlow.state === "done"
                    ? "border-emerald-500/40 bg-emerald-500/10"
                    : deviceFlow.state === "error" || deviceFlow.state === "expired"
                      ? "border-rose-500/40 bg-rose-500/10"
                      : "border-amber-500/40 bg-amber-500/10"
                }`}
              >
                <div className="flex items-center justify-between gap-3">
                  <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-300">
                    {deviceFlow.provider === "github" ? "GitHub" : "GitLab"} Device Flow · {deviceFlow.state}
                    {deviceFlow.byoClient ? " · BYO client" : ""}
                  </div>
                  <button
                    type="button"
                    onClick={() => setDeviceFlow(null)}
                    className="text-[11px] font-medium text-surface-300 hover:text-surface-100"
                  >
                    Close
                  </button>
                </div>
                {deviceFlow.state === "pending" ? (
                  <div className="mt-3 grid gap-3 md:grid-cols-[1fr,auto]">
                    <div>
                      <div className="text-xs text-surface-300">Open this URL in any browser:</div>
                      <a
                        href={deviceFlow.verificationUri}
                        target="_blank"
                        rel="noreferrer"
                        className="mt-1 break-all text-sm font-medium text-info hover:text-info-softFg"
                      >
                        {deviceFlow.verificationUri}
                      </a>
                      <div className="mt-3 text-xs text-surface-300">And enter this code:</div>
                      <div className="mt-1 flex items-center gap-2">
                        <code className="rounded-md border border-surface-700 bg-surface-950 px-3 py-1.5 text-lg font-bold tracking-[0.2em] text-surface-100">
                          {deviceFlow.userCode}
                        </code>
                        <button
                          type="button"
                          onClick={() => {
                            if (typeof navigator !== "undefined" && navigator.clipboard) {
                              void navigator.clipboard.writeText(deviceFlow.userCode);
                            }
                          }}
                          className="rounded-md border border-surface-700 px-2 py-1 text-[11px] text-surface-300 hover:bg-surface-800"
                        >
                          Copy
                        </button>
                      </div>
                      <div className="mt-3 text-[11px] text-surface-500">
                        Token will land on {targetLabel}'s vault. Polling every {deviceFlow.interval}s.
                      </div>
                    </div>
                  </div>
                ) : deviceFlow.state === "done" ? (
                  <div className="mt-2 text-sm text-emerald-800 dark:text-emerald-100">
                    ✓ Linked {deviceFlow.provider} as <span className="font-semibold">{deviceFlow.username}</span> on {targetLabel}.
                  </div>
                ) : (
                  <div className="mt-2 text-sm text-rose-800 dark:text-rose-100">
                    {deviceFlow.error || `Device Flow ended (${deviceFlow.state}). Start again to retry.`}
                  </div>
                )}
              </div>
            ) : null}

            <div className="flex flex-wrap gap-2">
              {(["github", "gitlab"] as const).map((p) => {
                const hasToken = providers.some((x) => x.provider === p);
                return (
                  <button
                    key={p}
                    onClick={() => setManualProvider(p)}
                    className={`inline-flex items-center gap-2 rounded-xl px-3 py-2 text-xs font-semibold transition-colors ${
                      hasToken
                        ? "text-surface-400 hover:text-surface-100 hover:bg-surface-800/50"
                        : "border border-brand/40 bg-brand-soft/40 text-brand-softFg hover:border-brand/60 hover:bg-brand-soft"
                    }`}
                  >
                    <ProviderIcon provider={p} className={`h-4 w-4 ${p === "gitlab" ? "text-warning" : ""}`} />
                    {hasToken ? `Manage ${p === "github" ? "GitHub" : "GitLab"} token` : `Or paste ${p === "github" ? "GitHub" : "GitLab"} PAT`}
                  </button>
                );
              })}
            </div>

            {manualProvider ? (
              <div className="space-y-2 rounded-md border border-surface-800 bg-surface-950/70 p-3">
                <div className="text-[11px] leading-5 text-surface-500">
                  {providerTokenHelp(manualProvider).hint}
                </div>
                <a
                  href={providerTokenHelp(manualProvider).href}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-2 text-[11px] font-medium text-info hover:text-info-softFg"
                >
                  <ProviderIcon provider={manualProvider} className="h-3.5 w-3.5" />
                  {providerTokenHelp(manualProvider).label}
                </a>
                <div className="flex gap-2">
                  <input
                    type="password"
                    value={providerToken}
                    onChange={(event) => setProviderToken(event.target.value)}
                    placeholder={`${manualProvider} token`}
                    className="flex-1 rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                  />
                  <button
                    onClick={() => void saveProviderToken()}
                    disabled={!providerToken.trim()}
                    className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Save
                  </button>
                </div>
              </div>
            ) : null}

            {providers.length === 0 ? (
              <div className="rounded-md border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-400">
                No git provider linked yet. Auto-detect uses remote `gh` or `glab` auth.
              </div>
            ) : providers.map((provider) => (
              <div key={provider.host} className="rounded-md border border-surface-800 bg-surface-950/70 p-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <ProviderIcon
                        provider={(provider.provider === "gitlab" ? "gitlab" : "github")}
                        className={`h-4 w-4 ${provider.provider === "gitlab" ? "text-warning" : "text-surface-300"}`}
                      />
                      <div className="text-sm font-semibold text-surface-100">{provider.username}</div>
                    </div>
                    <div className="mt-1 text-[11px] text-surface-500">
                      {provider.provider} · {provider.host}{provider.hasSsh ? " · SSH" : " · HTTPS"}
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <button
                      onClick={() => void toggleProviderRepos(provider.host)}
                      className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600"
                    >
                      {repoBrowserHost === provider.host ? "Hide Repos" : "Browse"}
                    </button>
                    <button
                      onClick={() => {
                        setProviderToken("");
                        setManualProvider(provider.provider as "github" | "gitlab");
                      }}
                      className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600"
                    >
                      Update
                    </button>
                    <button
                      onClick={() => void removeProvider(provider.host)}
                      className="rounded-lg border border-red-500/30 px-2.5 py-1.5 text-[11px] text-red-700 dark:text-red-300 hover:bg-red-500/10"
                    >
                      Remove
                    </button>
                  </div>
                </div>

                {repoBrowserHost === provider.host ? (
                  <div className="mt-3 border-t border-surface-800 pt-3">
                    <input
                      value={providerSearch}
                      onChange={(event) => setProviderSearch(event.target.value)}
                      placeholder="Search repos"
                      className="w-full rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                    />
                    <div className="mt-3 max-h-80 space-y-2 overflow-auto">
                      {filteredProviderRepos.map((repo) => (
                        <div key={repo.fullName} className="rounded-xl border border-surface-800 bg-surface-900/60 p-3">
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0">
                              <div className="truncate text-sm font-semibold text-surface-100">{repo.fullName}</div>
                              <div className="mt-1 flex flex-wrap gap-1.5">
                                {repo.private ? <MiniPill>private</MiniPill> : null}
                                {repo.language ? <MiniPill>{repo.language}</MiniPill> : null}
                              </div>
                              {repo.description ? <div className="mt-2 text-[11px] leading-5 text-surface-500">{repo.description}</div> : null}
                            </div>
                            <button
                              onClick={() => void cloneRemoteRepo(repo)}
                              className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1.5 text-[11px] font-semibold text-emerald-800 dark:text-emerald-100 hover:bg-emerald-500/15"
                            >
                              Clone
                            </button>
                          </div>
                        </div>
                      ))}
                      {filteredProviderRepos.length === 0 ? (
                        <div className="rounded-xl border border-surface-800 bg-surface-900/60 p-3 text-sm text-surface-500">
                          No repos found for this provider.
                        </div>
                      ) : null}
                    </div>
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        </details>
      </section>

      {busy ? (
        <div className="rounded-md border border-surface-800 bg-surface-900/50 px-4 py-3 text-sm text-surface-300">
          {busy}
        </div>
      ) : null}
    </div>
  );
}

function StatCard({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="rounded-md border border-surface-800 bg-surface-950/70 p-3">
      <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">{label}</div>
      <div className="mt-2 text-2xl font-semibold text-surface-100">{value}</div>
      <div className="mt-1 text-xs text-surface-500">{sub}</div>
    </div>
  );
}

function StatPill({
  label,
  value,
  tone,
}: {
  label: string;
  value: string | number;
  tone?: "success" | "warning" | "info";
}) {
  // Numeric zeroes stay neutral; non-zero gets the semantic tone so the eye
  // catches divergence without reading the number.
  const numeric = typeof value === "number" ? value : Number.parseInt(String(value), 10);
  const isZero = !Number.isFinite(numeric) || numeric === 0;
  const cls = isZero || !tone
    ? "border-surface-700 bg-surface-950 text-surface-400"
    : tone === "success"
      ? "border-success/30 bg-success-soft text-success-softFg"
      : tone === "warning"
        ? "border-warning/30 bg-warning-soft text-warning-softFg"
        : "border-info/30 bg-info-soft text-info-softFg";
  return (
    <span className={`rounded-full border px-2.5 py-1 text-[10px] uppercase tracking-[0.14em] ${cls}`}>
      {label}: {value}
    </span>
  );
}

function managedBackupLabel(kind?: string) {
  switch (kind) {
    case "dropbox":
      return "Dropbox";
    case "shared-storage":
      return "Own storage";
    case "local-folder":
      return "Local folder";
    default:
      return kind || "Backup";
  }
}

function MiniPill({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-900 px-2 py-1 text-[10px] text-surface-300">
      {children}
    </span>
  );
}
