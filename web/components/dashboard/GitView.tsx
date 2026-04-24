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
  const [projectActions, setProjectActions] = useState<ProjectAction[]>([]);

  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [repoBrowserHost, setRepoBrowserHost] = useState<string | null>(null);
  const [providerRepos, setProviderRepos] = useState<GitRemoteRepo[]>([]);
  const [providerSearch, setProviderSearch] = useState("");
  const [providerToken, setProviderToken] = useState("");
  const [manualProvider, setManualProvider] = useState<"github" | "gitlab" | null>(null);

  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);

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
      setProjectActions([]);
      return;
    }
    let cancelled = false;
    const refresh = async () => {
      try {
        const [status, branches, commits, actionsResult] = await Promise.all([
          agentClient.gitStatus(expandedProjectPath).catch(() => null),
          agentClient.gitBranches(expandedProjectPath).catch(() => []),
          agentClient.gitLog(expandedProjectPath, 5).catch(() => []),
          agentClient.getProjectActions(expandedProjectPath).catch(() => ({ actions: [] })),
        ]);
        if (cancelled) return;
        setGitStatus(status);
        setGitBranches(branches);
        setGitCommits(commits);
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
              const actions = isExpanded ? projectActions : [];
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
                        <StatPill label="ahead" value={status?.ahead || 0} />
                        <StatPill label="behind" value={status?.behind || 0} />
                        <StatPill
                          label="changes"
                          value={(status?.staged?.length || 0) + (status?.modified?.length || 0) + (status?.untracked?.length || 0)}
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

                          <div className="grid gap-3 sm:grid-cols-3">
                            <StatCard label="Remote Sync" value={`${status?.ahead || 0}↑ ${status?.behind || 0}↓`} sub="ahead / behind on current branch" />
                            <StatCard
                              label="Local State"
                              value={status?.clean ? "Clean" : String((status?.staged?.length || 0) + (status?.modified?.length || 0) + (status?.untracked?.length || 0))}
                              sub={status?.clean ? "no local changes" : "changed files on this machine"}
                            />
                            <StatCard label="Branch" value={status?.branch || project.branch || "none"} sub="active branch" />
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
                                className="w-full rounded-xl border border-red-500/30 px-3 py-2 text-xs font-semibold text-red-300 hover:bg-red-500/10 disabled:opacity-40"
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
              <div className="space-y-2 rounded-md border border-surface-800 bg-surface-950/70 p-3">
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
              <div className="rounded-md border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-400">
                No git provider linked yet. Auto-detect uses remote `gh` or `glab` auth.
              </div>
            ) : providers.map((provider) => (
              <div key={provider.host} className="rounded-md border border-surface-800 bg-surface-950/70 p-3">
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

function StatPill({ label, value }: { label: string; value: string | number }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-950 px-2.5 py-1 text-[10px] uppercase tracking-[0.14em] text-surface-400">
      {label}: {value}
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
