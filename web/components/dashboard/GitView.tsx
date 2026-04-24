"use client";

import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import {
  agentClient,
  type GitBranchRow,
  type GitCommitRow,
  type GitProviderStatusRow,
  type GitRemoteRepo,
  type GitStatusRow,
} from "@/lib/agent-client";

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
  onOpenSurface?: (surface: "preview" | "web-reload" | "builds", projectPath: string) => void;
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
  if (action.type === "build" || action.type === "deploy") return "builds";
  if (action.type === "dev-server") {
    if (isMobileFramework(action.framework)) return "preview";
    if (isWebFramework(action.framework)) return "web-reload";
  }
  return null;
}

function actionLabelForSurface(action: ProjectAction, surface: "preview" | "web-reload" | "builds") {
  if (surface === "preview") return "Hot Reload";
  if (surface === "web-reload") return "Web Reload";
  if (action.type === "build") return action.label || "Builds";
  if (action.type === "deploy") return "Builds";
  return "Builds";
}

export default function GitView({ onOpenSurface }: Props) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [expandedProjectPath, setExpandedProjectPath] = useState("");
  const [gitStatus, setGitStatus] = useState<GitStatusRow | null>(null);
  const [gitBranches, setGitBranches] = useState<GitBranchRow[]>([]);
  const [gitCommits, setGitCommits] = useState<GitCommitRow[]>([]);
  const [gitDiff, setGitDiff] = useState("");
  const [selectedDiffFile, setSelectedDiffFile] = useState("");
  const [projectActions, setProjectActions] = useState<ProjectAction[]>([]);

  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [repoBrowserHost, setRepoBrowserHost] = useState<string | null>(null);
  const [providerRepos, setProviderRepos] = useState<GitRemoteRepo[]>([]);
  const [providerSearch, setProviderSearch] = useState("");
  const [providerToken, setProviderToken] = useState("");
  const [manualProvider, setManualProvider] = useState<"github" | "gitlab" | null>(null);

  const [gitCommitMessage, setGitCommitMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);

  const expandedProject = useMemo(
    () => projects.find((project) => project.path === expandedProjectPath) || null,
    [projects, expandedProjectPath],
  );

  const changedFiles = useMemo(() => {
    const next = [
      ...(gitStatus?.staged?.map((item) => ({ path: item.path, kind: "staged" as const })) || []),
      ...(gitStatus?.modified?.map((item) => ({ path: item.path, kind: "modified" as const })) || []),
      ...(gitStatus?.untracked?.map((item) => ({ path: item.path, kind: "untracked" as const })) || []),
    ];
    return next.filter((item, index) => next.findIndex((candidate) => candidate.path === item.path) === index);
  }, [gitStatus]);

  const filteredProviderRepos = useMemo(() => {
    const needle = providerSearch.trim().toLowerCase();
    if (!needle) return providerRepos;
    return providerRepos.filter((repo) =>
      repo.name.toLowerCase().includes(needle) ||
      repo.fullName.toLowerCase().includes(needle) ||
      String(repo.description || "").toLowerCase().includes(needle),
    );
  }, [providerRepos, providerSearch]);

  useEffect(() => {
    let cancelled = false;
    const refresh = async () => {
      try {
        const [projectRows, gitProviders] = await Promise.all([
          agentClient.listProjects().catch(() => []),
          agentClient.gitProviderStatus().catch(() => []),
        ]);
        if (cancelled) return;
        setProjects(projectRows);
        setProviders(gitProviders);
        if (!expandedProjectPath && projectRows.length > 0) {
          setExpandedProjectPath(projectRows[0].path);
        }
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };
    void refresh();
    return () => {
      cancelled = true;
    };
  }, [expandedProjectPath, refreshNonce]);

  useEffect(() => {
    if (!expandedProjectPath) {
      setGitStatus(null);
      setGitBranches([]);
      setGitCommits([]);
      setGitDiff("");
      setProjectActions([]);
      return;
    }
    let cancelled = false;
    const refresh = async () => {
      try {
        const [status, branches, commits, actionsResult] = await Promise.all([
          agentClient.gitStatus(expandedProjectPath).catch(() => null),
          agentClient.gitBranches(expandedProjectPath).catch(() => []),
          agentClient.gitLog(expandedProjectPath, 12).catch(() => []),
          agentClient.getProjectActions(expandedProjectPath).catch(() => ({ actions: [] })),
        ]);
        if (cancelled) return;
        setGitStatus(status);
        setGitBranches(branches);
        setGitCommits(commits);
        setProjectActions(Array.isArray(actionsResult?.actions) ? actionsResult.actions : []);

        const nextFiles = [
          ...((status?.staged || []).map((item) => item.path)),
          ...((status?.modified || []).map((item) => item.path)),
          ...((status?.untracked || []).map((item) => item.path)),
        ];
        const firstFile = [...new Set(nextFiles)][0] || "";
        setSelectedDiffFile((current) => (current && nextFiles.includes(current) ? current : firstFile));
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };
    void refresh();
    return () => {
      cancelled = true;
    };
  }, [expandedProjectPath, refreshNonce]);

  useEffect(() => {
    if (!expandedProjectPath) {
      setGitDiff("");
      return;
    }
    let cancelled = false;
    const loadDiff = async () => {
      try {
        const result = await agentClient.gitDiff(expandedProjectPath, selectedDiffFile || undefined);
        if (!cancelled) setGitDiff(result.diff || "");
      } catch {
        if (!cancelled) setGitDiff("");
      }
    };
    void loadDiff();
    return () => {
      cancelled = true;
    };
  }, [expandedProjectPath, selectedDiffFile, refreshNonce]);

  async function autoDetectProviders() {
    setBusy("Detecting git providers from the remote machine…");
    const detected = await agentClient.gitProviderDetect();
    setProviders(await agentClient.gitProviderStatus());
    setBusy(detected.length > 0 ? `Detected ${detected.map((item) => item.provider).join(", ")}.` : "No git providers detected.");
  }

  async function saveProviderToken() {
    if (!manualProvider || !providerToken.trim()) return;
    setBusy(`Saving ${manualProvider} token to the remote machine vault…`);
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
    setBusy(`Cloning ${repo.fullName} on the remote machine…`);
    const result = await agentClient.cloneRepo(url);
    if (!result?.ok) {
      setBusy(result?.error || `Could not clone ${repo.fullName}.`);
      return;
    }
    const nextProjects = await agentClient.listProjects().catch(() => []);
    setProjects(nextProjects);
    if (result.path && nextProjects.some((project: Project) => project.path === result.path)) {
      setExpandedProjectPath(result.path);
    }
    setRefreshNonce((value) => value + 1);
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
    if (!expandedProject) return;
    try {
      if (action === "commit") {
        if (!gitCommitMessage.trim()) {
          setBusy("Enter a commit message first.");
          return;
        }
        setBusy(`Committing ${expandedProject.name}…`);
        const result = await agentClient.gitCommit(expandedProject.path, gitCommitMessage.trim());
        setGitCommitMessage("");
        setBusy(result.hash ? `Committed ${result.hash.trim()}.` : result.message || "Committed changes.");
      } else if (action === "pull") {
        setBusy(`Pulling ${expandedProject.name}…`);
        const result = await agentClient.gitPull(expandedProject.path);
        setBusy(result.message || "Pulled latest changes.");
      } else if (action === "push") {
        setBusy(`Pushing ${expandedProject.name}…`);
        const result = await agentClient.gitPush(expandedProject.path);
        setBusy(result.message || "Pushed branch.");
      } else if (action === "stash") {
        setBusy(`Stashing changes in ${expandedProject.name}…`);
        const result = await agentClient.gitStash(expandedProject.path);
        setBusy(result.message || "Stashed changes.");
      } else if (action === "stash-pop") {
        setBusy(`Restoring stash in ${expandedProject.name}…`);
        const result = await agentClient.gitStashPop(expandedProject.path);
        setBusy(result.message || "Restored stash.");
      } else if (action === "revert-head") {
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

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-3xl border border-surface-800 bg-surface-900/60 p-5">
        <div>
          <h2 className="text-xl font-semibold text-surface-100">Git</h2>
          <p className="mt-1 text-sm text-surface-400">
            See which repos live on this machine, open the right workflow for each one, and keep them in sync.
          </p>
        </div>
        <button
          onClick={() => void autoDetectProviders()}
          className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-200 hover:bg-indigo-500/15"
        >
          Detect Providers
        </button>
      </div>

      <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Repos On This Machine</div>
            <div className="mt-1 text-xs text-surface-500">Expand a repo for git state, launch surfaces, diffs, branches, and recent commits.</div>
          </div>
          <span className="text-xs text-surface-500">{projects.length}</span>
        </div>

        {projects.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-500">
            No project roots detected yet. Clone a repo from below or connect to a machine with existing repos.
          </div>
        ) : (
          <div className="space-y-3">
            {projects.map((project) => {
              const isExpanded = expandedProjectPath === project.path;
              const status = isExpanded ? gitStatus : null;
              const actions = isExpanded ? projectActions : [];
              return (
                <details
                  key={project.path}
                  open={isExpanded}
                  onToggle={(event) => {
                    const nextOpen = (event.currentTarget as HTMLDetailsElement).open;
                    setExpandedProjectPath(nextOpen ? project.path : "");
                  }}
                  className={`rounded-2xl border transition ${
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
                        <StatPill label="ahead" value={status?.ahead || 0} />
                        <StatPill label="behind" value={status?.behind || 0} />
                        <StatPill label="changes" value={status?.clean ? 0 : changedFiles.length} />
                      </div>
                    </div>
                  </summary>

                  {isExpanded ? (
                    <div className="border-t border-surface-800 px-4 pb-4 pt-4">
                      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.5fr),minmax(340px,1fr)]">
                        <div className="space-y-4">
                          <div className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4">
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
                                  const surface = preferredSurfaceForAction(action);
                                  const supported = action.supported !== false;
                                  const label = surface ? actionLabelForSurface(action, surface) : action.label;
                                  const disabled = !surface || !supported;
                                  return (
                                    <button
                                      key={`${action.label}:${action.target}:${index}`}
                                      type="button"
                                      disabled={disabled}
                                      title={!supported ? action.reason || "Not supported on this machine yet." : surface ? `Open ${label}` : "No dashboard surface for this action yet"}
                                      onClick={() => {
                                        if (!surface || !onOpenSurface) return;
                                        onOpenSurface(surface, project.path);
                                      }}
                                      className={`rounded-xl border px-3 py-2 text-xs font-semibold ${
                                        disabled
                                          ? "cursor-not-allowed border-surface-800 bg-surface-950 text-surface-600"
                                          : surface === "builds"
                                            ? "border-amber-500/30 bg-amber-500/10 text-amber-100 hover:bg-amber-500/15"
                                            : surface === "web-reload"
                                              ? "border-sky-500/30 bg-sky-500/10 text-sky-100 hover:bg-sky-500/15"
                                              : "border-emerald-500/30 bg-emerald-500/10 text-emerald-100 hover:bg-emerald-500/15"
                                      }`}
                                    >
                                      {label}
                                    </button>
                                  );
                                })
                              )}
                            </div>
                          </div>

                          <div className="grid gap-3 md:grid-cols-[minmax(0,1fr),220px]">
                            <div className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4">
                              <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Sync With Remote</div>
                              <div className="flex flex-wrap gap-2">
                                <PrimaryButton onClick={() => void runGitAction("pull")}>Pull</PrimaryButton>
                                <PrimaryButton onClick={() => void runGitAction("push")}>Push</PrimaryButton>
                                <PrimaryButton onClick={() => void runGitAction("stash")}>Stash</PrimaryButton>
                                <PrimaryButton onClick={() => void runGitAction("stash-pop")}>Pop Stash</PrimaryButton>
                              </div>
                              <div className="mt-3 flex gap-2">
                                <input
                                  value={gitCommitMessage}
                                  onChange={(event) => setGitCommitMessage(event.target.value)}
                                  placeholder="Commit message"
                                  className="flex-1 rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                                />
                                <button
                                  onClick={() => void runGitAction("commit")}
                                  disabled={!gitCommitMessage.trim()}
                                  className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                                >
                                  Commit
                                </button>
                              </div>
                              <div className="mt-3">
                                <button
                                  onClick={() => void runGitAction("revert-head")}
                                  disabled={gitCommits.length === 0}
                                  className="rounded-xl border border-red-500/30 px-3 py-2 text-xs font-semibold text-red-300 hover:bg-red-500/10 disabled:opacity-40"
                                >
                                  Revert Last
                                </button>
                              </div>
                            </div>

                            <div className="grid gap-3 sm:grid-cols-3 md:grid-cols-1">
                              <StatCard label="Remote Sync" value={`${status?.ahead || 0}↑ ${status?.behind || 0}↓`} sub="ahead / behind on current branch" />
                              <StatCard label="Local State" value={status?.clean ? "Clean" : String(changedFiles.length)} sub={status?.clean ? "no local changes" : "changed files on this machine"} />
                              <StatCard label="Branch" value={status?.branch || project.branch || "none"} sub="active branch" />
                            </div>
                          </div>

                          <details className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4" open>
                            <summary className="cursor-pointer list-none">
                              <div className="flex flex-wrap items-center justify-between gap-3">
                                <div>
                                  <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Local Changes</div>
                                  <div className="mt-1 text-sm text-surface-500">Inspect changed files and their diffs only when you need them.</div>
                                </div>
                                <div className="grid grid-cols-3 gap-2 text-xs text-surface-400">
                                  <div>staged: {status?.staged?.length || 0}</div>
                                  <div>modified: {status?.modified?.length || 0}</div>
                                  <div>untracked: {status?.untracked?.length || 0}</div>
                                </div>
                              </div>
                            </summary>

                            <div className="mt-4 grid gap-3 xl:grid-cols-[280px,minmax(0,1fr)]">
                              <div className="space-y-2">
                                {changedFiles.map((file) => (
                                  <button
                                    key={file.path}
                                    onClick={() => setSelectedDiffFile(file.path)}
                                    className={`w-full rounded-xl border px-3 py-2 text-left text-xs ${
                                      selectedDiffFile === file.path
                                        ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-100"
                                        : "border-surface-800 bg-surface-950/70 text-surface-300 hover:border-surface-700"
                                    }`}
                                  >
                                    <div className="flex items-center justify-between gap-3">
                                      <span className="block truncate">{file.path}</span>
                                      <span className="rounded-full border border-surface-700 px-2 py-0.5 text-[10px] uppercase tracking-[0.14em] text-surface-400">
                                        {file.kind}
                                      </span>
                                    </div>
                                  </button>
                                ))}
                                {changedFiles.length === 0 ? (
                                  <div className="rounded-xl border border-dashed border-surface-800 bg-surface-950/70 p-3 text-sm text-surface-500">
                                    Working tree is clean.
                                  </div>
                                ) : null}
                              </div>

                              <div className="rounded-2xl border border-surface-800 bg-[#08111a] p-3">
                                <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                                  Diff {selectedDiffFile ? `· ${selectedDiffFile}` : ""}
                                </div>
                                <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap rounded-xl border border-surface-800 bg-surface-950/80 p-3 font-mono text-[12px] leading-6 text-surface-200">
                                  {gitDiff || "No diff to show."}
                                </pre>
                              </div>
                            </div>
                          </details>
                        </div>

                        <div className="space-y-4">
                          <div className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4">
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
                                      ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-100"
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

                          <div className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4">
                            <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Recent Commits</div>
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

      <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-4">
        <details open>
          <summary className="cursor-pointer list-none">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Clone From Remotes</div>
                <div className="mt-1 text-xs text-surface-500">Link GitHub or GitLab when you want to browse remote repos and clone them onto this machine.</div>
              </div>
              <span className="rounded-full border border-surface-700 px-2 py-1 text-[10px] text-surface-400">
                {providers.length} linked
              </span>
            </div>
          </summary>

          <div className="mt-4 space-y-3">
            <div className="flex flex-wrap gap-2">
              <button
                onClick={() => setManualProvider("github")}
                className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
              >
                Add GitHub Token
              </button>
              <button
                onClick={() => setManualProvider("gitlab")}
                className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
              >
                Add GitLab Token
              </button>
            </div>

            {manualProvider ? (
              <div className="space-y-2 rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                <div className="text-[11px] leading-5 text-surface-500">
                  {manualProvider === "github"
                    ? "Use a GitHub token with repo access for private repositories."
                    : "Use a GitLab token with api scope for repo traversal and clone."}
                </div>
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
              <div className="rounded-2xl border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-400">
                No git provider linked yet. Auto-detect uses remote `gh` or `glab` auth.
              </div>
            ) : providers.map((provider) => (
              <div key={provider.host} className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="text-sm font-semibold text-surface-100">{provider.username}</div>
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
                      className="rounded-lg border border-red-500/30 px-2.5 py-1.5 text-[11px] text-red-300 hover:bg-red-500/10"
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
                              className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1.5 text-[11px] font-semibold text-emerald-100 hover:bg-emerald-500/15"
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
        <div className="rounded-2xl border border-surface-800 bg-surface-900/50 px-4 py-3 text-sm text-surface-300">
          {busy}
        </div>
      ) : null}
    </div>
  );
}

function StatCard({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
      <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">{label}</div>
      <div className="mt-2 text-2xl font-semibold text-surface-100">{value}</div>
      <div className="mt-1 text-xs text-surface-500">{sub}</div>
    </div>
  );
}

function StatPill({ label, value }: { label: string; value: string | number }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-950 px-2.5 py-1 text-[10px] uppercase tracking-[0.14em] text-surface-400">
      {label}: {value}
    </span>
  );
}

function PrimaryButton({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-semibold text-surface-100 hover:border-surface-500"
    >
      {children}
    </button>
  );
}

function MiniPill({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-900 px-2 py-1 text-[10px] text-surface-300">
      {children}
    </span>
  );
}
