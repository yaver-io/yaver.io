"use client";

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

export default function GitView() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedProjectPath, setSelectedProjectPath] = useState("");
  const [gitStatus, setGitStatus] = useState<GitStatusRow | null>(null);
  const [gitBranches, setGitBranches] = useState<GitBranchRow[]>([]);
  const [gitCommits, setGitCommits] = useState<GitCommitRow[]>([]);
  const [gitDiff, setGitDiff] = useState("");
  const [selectedDiffFile, setSelectedDiffFile] = useState("");

  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [repoBrowserHost, setRepoBrowserHost] = useState<string | null>(null);
  const [providerRepos, setProviderRepos] = useState<GitRemoteRepo[]>([]);
  const [providerSearch, setProviderSearch] = useState("");
  const [providerToken, setProviderToken] = useState("");
  const [manualProvider, setManualProvider] = useState<"github" | "gitlab" | null>(null);

  const [gitCommitMessage, setGitCommitMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);

  const selectedProject = useMemo(
    () => projects.find((project) => project.path === selectedProjectPath) || null,
    [projects, selectedProjectPath],
  );

  const changedFiles = useMemo(() => {
    const next = [
      ...(gitStatus?.staged?.map((item) => item.path) || []),
      ...(gitStatus?.modified?.map((item) => item.path) || []),
      ...(gitStatus?.untracked?.map((item) => item.path) || []),
    ];
    return [...new Set(next)];
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
        if (!selectedProjectPath && projectRows.length > 0) {
          setSelectedProjectPath(projectRows[0].path);
        }
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };
    void refresh();
    return () => {
      cancelled = true;
    };
  }, [selectedProjectPath, refreshNonce]);

  useEffect(() => {
    if (!selectedProjectPath) {
      setGitStatus(null);
      setGitBranches([]);
      setGitCommits([]);
      setGitDiff("");
      return;
    }
    let cancelled = false;
    const refresh = async () => {
      try {
        const [status, branches, commits] = await Promise.all([
          agentClient.gitStatus(selectedProjectPath).catch(() => null),
          agentClient.gitBranches(selectedProjectPath).catch(() => []),
          agentClient.gitLog(selectedProjectPath, 12).catch(() => []),
        ]);
        if (cancelled) return;
        setGitStatus(status);
        setGitBranches(branches);
        setGitCommits(commits);

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
  }, [selectedProjectPath, refreshNonce]);

  useEffect(() => {
    if (!selectedProjectPath) {
      setGitDiff("");
      return;
    }
    let cancelled = false;
    const loadDiff = async () => {
      try {
        const result = await agentClient.gitDiff(selectedProjectPath, selectedDiffFile || undefined);
        if (!cancelled) setGitDiff(result.diff || "");
      } catch {
        if (!cancelled) setGitDiff("");
      }
    };
    void loadDiff();
    return () => {
      cancelled = true;
    };
  }, [selectedProjectPath, selectedDiffFile, refreshNonce]);

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
      setSelectedProjectPath(result.path);
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

  async function checkoutBranch(branch: string) {
    if (!selectedProject || !branch) return;
    try {
      setBusy(`Checking out ${branch}…`);
      const result = await agentClient.gitCheckout(selectedProject.path, branch);
      setBusy(result.message || `Checked out ${branch}.`);
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    } finally {
      setRefreshNonce((value) => value + 1);
    }
  }

  return (
    <div className="space-y-5">
      <header className="rounded-3xl border border-surface-800 bg-surface-900/60 p-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2 className="text-xl font-semibold text-surface-100">Git Control</h2>
            <p className="mt-1 max-w-3xl text-sm leading-6 text-surface-400">
              Traverse repos, clone onto the remote machine, and operate project Git state from the control plane. Provider tokens stay in the Yaver vault on that machine, not in Convex.
            </p>
          </div>
          <button
            onClick={() => void autoDetectProviders()}
            className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-200 hover:bg-indigo-500/15"
          >
            Detect from Dev Machine
          </button>
        </div>
        <div className="mt-4 grid gap-3 md:grid-cols-3">
          <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
            <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Vault</div>
            <div className="mt-2 text-sm text-surface-300">GitHub and GitLab tokens are stored only on the remote machine.</div>
          </div>
          <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
            <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Clone</div>
            <div className="mt-2 text-sm text-surface-300">Browsing and cloning run on the remote machine, so project roots appear immediately in Yaver.</div>
          </div>
          <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
            <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Project Scope</div>
            <div className="mt-2 text-sm text-surface-300">Every branch, diff, commit, push, and checkout action is tied to the selected project path.</div>
          </div>
        </div>
      </header>

      <div className="grid gap-5 xl:grid-cols-[360px,minmax(0,1fr)]">
        <aside className="space-y-5">
          <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-4">
            <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Providers</div>
            <div className="flex flex-wrap gap-2">
              <button
                onClick={() => setManualProvider("github")}
                className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
              >
                GitHub Token
              </button>
              <button
                onClick={() => setManualProvider("gitlab")}
                className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
              >
                GitLab Token
              </button>
            </div>
            {manualProvider ? (
              <div className="mt-3 space-y-2">
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
            <div className="mt-4 space-y-3">
              {providers.length === 0 ? (
                <div className="rounded-2xl border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-400">
                  No git provider linked yet. Auto-detect uses remote `gh` / `glab` auth, or you can add a token manually and keep it in the machine vault.
                </div>
              ) : providers.map((provider) => (
                <div key={provider.host} className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div>
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
                        {repoBrowserHost === provider.host ? "Hide" : "Repos"}
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
                        placeholder="search repos"
                        className="w-full rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                      />
                      <div className="mt-3 max-h-96 space-y-2 overflow-auto">
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
          </section>

          <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-4">
            <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Projects on Remote Machine</div>
            <div className="space-y-2">
              {projects.map((project) => (
                <button
                  key={project.path}
                  onClick={() => setSelectedProjectPath(project.path)}
                  className={`w-full rounded-2xl border p-3 text-left ${
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
              {projects.length === 0 ? (
                <div className="rounded-2xl border border-dashed border-surface-800 bg-surface-950/70 p-4 text-sm text-surface-500">
                  No project roots detected yet. Clone a repo from above or connect to a machine with existing repos.
                </div>
              ) : null}
            </div>
          </section>
        </aside>

        <section className="space-y-5">
          <div className="rounded-3xl border border-surface-800 bg-surface-900/50 p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div className="text-lg font-semibold text-surface-100">{selectedProject?.name || "Select a project"}</div>
                <div className="mt-1 text-sm text-surface-500">{selectedProject?.path || "Choose a repo on the remote machine to inspect and operate."}</div>
              </div>
              {selectedProject ? (
                <div className="flex flex-wrap gap-2">
                  <MiniPill>{gitStatus?.branch || selectedProject.branch || "no branch"}</MiniPill>
                  <MiniPill>{gitStatus?.ahead || 0} ahead</MiniPill>
                  <MiniPill>{gitStatus?.behind || 0} behind</MiniPill>
                  <MiniPill>{gitStatus?.clean ? "clean" : "dirty"}</MiniPill>
                </div>
              ) : null}
            </div>

            {selectedProject ? (
              <>
                <div className="mt-4 grid gap-3 lg:grid-cols-[minmax(0,1fr),260px]">
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                    <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Git Actions</div>
                    <div className="flex flex-wrap gap-2">
                      <button onClick={() => void runGitAction("pull")} className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600">Pull</button>
                      <button onClick={() => void runGitAction("push")} className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600">Push</button>
                      <button onClick={() => void runGitAction("stash")} className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600">Stash</button>
                      <button onClick={() => void runGitAction("stash-pop")} className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600">Pop Stash</button>
                      <button onClick={() => void runGitAction("revert-head")} disabled={gitCommits.length === 0} className="rounded-lg border border-red-500/30 px-3 py-2 text-xs font-semibold text-red-300 hover:bg-red-500/10 disabled:opacity-40">Revert Last</button>
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
                        disabled={!gitCommitMessage.trim()}
                        className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                      >
                        Commit All
                      </button>
                    </div>
                  </div>

                  <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                    <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Branches</div>
                    <div className="space-y-2">
                      {gitBranches.slice(0, 24).map((branch) => (
                        <button
                          key={branch.name}
                          onClick={() => void checkoutBranch(branch.name)}
                          className={`flex w-full items-center justify-between rounded-xl border px-3 py-2 text-left ${
                            branch.current
                              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-100"
                              : "border-surface-800 bg-surface-900/60 text-surface-300 hover:border-surface-700"
                          }`}
                        >
                          <span className="truncate text-xs font-medium">{branch.name}</span>
                          <span className="text-[10px] uppercase tracking-[0.16em]">{branch.current ? "current" : "checkout"}</span>
                        </button>
                      ))}
                    </div>
                  </div>
                </div>

                <div className="mt-4 grid gap-3 xl:grid-cols-[320px,minmax(0,1fr)]">
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                    <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Working Tree</div>
                    <div className="grid grid-cols-3 gap-2 text-xs text-surface-400">
                      <div>staged: {gitStatus?.staged?.length || 0}</div>
                      <div>modified: {gitStatus?.modified?.length || 0}</div>
                      <div>untracked: {gitStatus?.untracked?.length || 0}</div>
                    </div>
                    <div className="mt-3 space-y-2">
                      {changedFiles.map((file) => (
                        <button
                          key={file}
                          onClick={() => setSelectedDiffFile(file)}
                          className={`w-full rounded-xl border px-3 py-2 text-left text-xs ${
                            selectedDiffFile === file
                              ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-100"
                              : "border-surface-800 bg-surface-900/60 text-surface-300 hover:border-surface-700"
                          }`}
                        >
                          <span className="block truncate">{file}</span>
                        </button>
                      ))}
                      {changedFiles.length === 0 ? (
                        <div className="rounded-xl border border-dashed border-surface-800 bg-surface-900/50 p-3 text-sm text-surface-500">
                          Working tree is clean.
                        </div>
                      ) : null}
                    </div>
                  </div>

                  <div className="rounded-2xl border border-surface-800 bg-[#08111a] p-3">
                    <div className="mb-2 flex items-center justify-between gap-3">
                      <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                        Diff {selectedDiffFile ? `· ${selectedDiffFile}` : ""}
                      </div>
                    </div>
                    <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap rounded-xl border border-surface-800 bg-surface-950/80 p-3 font-mono text-[12px] leading-6 text-surface-200">
                      {gitDiff || "No diff to show."}
                    </pre>
                  </div>
                </div>

                <div className="mt-4 rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                  <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">Recent Commits</div>
                  <div className="space-y-2">
                    {gitCommits.map((commit) => (
                      <div key={commit.hash} className="rounded-xl border border-surface-800 bg-surface-900/60 p-3">
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
                      <div className="rounded-xl border border-dashed border-surface-800 bg-surface-900/50 p-3 text-sm text-surface-500">
                        No commit history available for this project.
                      </div>
                    ) : null}
                  </div>
                </div>
              </>
            ) : null}
          </div>

          {busy ? (
            <div className="rounded-2xl border border-surface-800 bg-surface-900/50 px-4 py-3 text-sm text-surface-300">
              {busy}
            </div>
          ) : null}
        </section>
      </div>
    </div>
  );
}

function MiniPill({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-900 px-2 py-1 text-[10px] text-surface-300">
      {children}
    </span>
  );
}
