"use client";

// GitMembersPanel — who can reach this repo, and inviting them.
//
// This is the surface half of the forge seam. It deliberately holds no
// GitHub-vs-GitLab logic: the agent's git_members / git_member_invite /
// git_member_remove verbs resolve the forge, choose CLI-vs-REST transport, and
// map the neutral role onto each forge's own vocabulary. The panel's whole job
// is to ask, and to report honestly what came back.
//
// Two honesty rules it exists to keep:
//   - "invited" and "added" are different outcomes. GitHub replies "added" when
//     the user already had access, and sends NO email. Saying "invited" there
//     would promise a message that never arrives.
//   - pending invitations are shown, not hidden. Without them, invite-then-list
//     looks like the invite silently failed.

import { useCallback, useEffect, useState } from "react";
import {
  agentClient,
  type ForgeInviteResult,
  type ForgeMember,
  type ForgeRole,
} from "@/lib/agent-client";

const ROLES: Array<{ id: ForgeRole; label: string; hint: string }> = [
  { id: "read", label: "Read", hint: "GitHub pull · GitLab Reporter" },
  { id: "triage", label: "Triage", hint: "GitHub triage · GitLab Reporter" },
  { id: "write", label: "Write", hint: "GitHub push · GitLab Developer" },
  { id: "maintain", label: "Maintain", hint: "GitHub maintain · GitLab Maintainer" },
  { id: "admin", label: "Admin", hint: "GitHub admin · GitLab Owner" },
];

type Props = {
  /** Repo directory on the agent. The agent reads its remote to find the
   *  forge, so the panel never has to know the host or the provider. */
  projectPath: string;
  projectName?: string;
};

export function GitMembersPanel({ projectPath, projectName }: Props) {
  const [members, setMembers] = useState<ForgeMember[]>([]);
  const [repo, setRepo] = useState("");
  const [via, setVia] = useState("");
  const [kind, setKind] = useState("");
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(false);

  const [inviteUser, setInviteUser] = useState("");
  const [inviteRole, setInviteRole] = useState<ForgeRole>("write");
  const [busy, setBusy] = useState("");
  const [result, setResult] = useState<ForgeInviteResult | null>(null);
  const [actionError, setActionError] = useState("");

  const load = useCallback(async () => {
    if (!projectPath) return;
    setLoading(true);
    setLoadError("");
    try {
      const res = await agentClient.gitMembers({ directory: projectPath });
      setMembers(Array.isArray(res?.members) ? res.members : []);
      setRepo(res?.repo || "");
      setVia(res?.via || "");
      setKind(res?.kind || "");
    } catch (err) {
      // Not every project is on a forge, and not every forge is reachable.
      // Say which, rather than rendering an empty list that looks like
      // "nobody has access".
      setLoadError(err instanceof Error ? err.message : String(err));
      setMembers([]);
    } finally {
      setLoading(false);
    }
  }, [projectPath]);

  useEffect(() => {
    void load();
  }, [load]);

  const invite = async () => {
    const user = inviteUser.trim();
    if (!user) return;
    setBusy("invite");
    setActionError("");
    setResult(null);
    try {
      const res = await agentClient.gitMemberInvite(user, inviteRole, { directory: projectPath });
      setResult(res);
      setInviteUser("");
      await load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  const remove = async (username: string) => {
    setBusy(`remove:${username}`);
    setActionError("");
    try {
      await agentClient.gitMemberRemove(username, { directory: projectPath });
      await load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="mt-4 rounded-md border border-surface-800 bg-surface-950/70 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">
            Who can reach this repo
          </div>
          <div className="mt-1 text-xs text-surface-500">
            {repo ? (
              <>
                {kind || "forge"} · <span className="text-surface-300">{repo}</span>
              </>
            ) : (
              <>Collaborators on {projectName || "this project"}&apos;s git remote.</>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {/* `via` answers "which credential did this use" — the first thing
              worth knowing when a call unexpectedly 403s. */}
          {via ? (
            <span
              className="rounded-full border border-surface-700 px-2 py-1 text-[10px] text-surface-400"
              title="Which transport the agent used: the gh/glab CLI (your own session) or direct REST with a stored token."
            >
              via {via}
            </span>
          ) : null}
          <button
            type="button"
            onClick={() => void load()}
            disabled={loading}
            className="rounded-md border border-surface-700 px-2 py-1 text-[11px] text-surface-300 hover:border-surface-500 disabled:opacity-50"
          >
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      </div>

      {loadError ? (
        <div className="mt-3 rounded-md border border-amber-900/60 bg-amber-950/30 p-2 text-[11px] text-amber-300">
          {loadError}
        </div>
      ) : null}

      {!loadError && members.length === 0 && !loading ? (
        <div className="mt-3 text-[11px] text-surface-500">No collaborators returned.</div>
      ) : null}

      {members.length > 0 ? (
        <ul className="mt-3 space-y-1">
          {members.map((m) => {
            const pending = m.state === "pending";
            return (
              <li
                key={`${m.username}:${m.state || "active"}`}
                className="flex items-center justify-between gap-2 rounded-md border border-surface-800 bg-surface-900/40 px-2 py-1.5"
              >
                <div className="flex min-w-0 items-center gap-2">
                  {m.avatarUrl ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img src={m.avatarUrl} alt="" className="h-5 w-5 rounded-full" />
                  ) : (
                    <span className="h-5 w-5 rounded-full bg-surface-800" />
                  )}
                  <span className="truncate text-xs text-surface-200">{m.username}</span>
                  {m.name ? <span className="truncate text-[11px] text-surface-500">{m.name}</span> : null}
                  {pending ? (
                    <span
                      className="rounded-full border border-amber-800 px-1.5 py-0.5 text-[10px] text-amber-400"
                      title="Invited, but they have not accepted yet."
                    >
                      pending
                    </span>
                  ) : null}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <span
                    className="rounded-full border border-surface-700 px-2 py-0.5 text-[10px] text-surface-400"
                    title={m.nativeRole ? `This forge calls it "${m.nativeRole}"` : undefined}
                  >
                    {m.role}
                    {m.nativeRole ? ` · ${m.nativeRole}` : ""}
                  </span>
                  <button
                    type="button"
                    onClick={() => void remove(m.username)}
                    disabled={busy === `remove:${m.username}`}
                    className="rounded-md border border-surface-700 px-2 py-0.5 text-[10px] text-surface-400 hover:border-red-800 hover:text-red-300 disabled:opacity-50"
                  >
                    {busy === `remove:${m.username}` ? "…" : "Remove"}
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <input
          value={inviteUser}
          onChange={(e) => setInviteUser(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void invite();
          }}
          placeholder="username (GitLab also accepts an email)"
          className="min-w-[200px] flex-1 rounded-md border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200 placeholder:text-surface-600"
        />
        <select
          value={inviteRole}
          onChange={(e) => setInviteRole(e.target.value as ForgeRole)}
          className="rounded-md border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
        >
          {ROLES.map((r) => (
            <option key={r.id} value={r.id} title={r.hint}>
              {r.label}
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={() => void invite()}
          disabled={busy === "invite" || !inviteUser.trim()}
          className="rounded-md border border-surface-600 px-3 py-1 text-xs text-surface-100 hover:border-surface-400 disabled:opacity-50"
        >
          {busy === "invite" ? "Inviting…" : "Invite"}
        </button>
      </div>

      <div className="mt-1 text-[10px] text-surface-600">
        {ROLES.find((r) => r.id === inviteRole)?.hint}
      </div>

      {actionError ? (
        <div className="mt-2 rounded-md border border-red-900/60 bg-red-950/30 p-2 text-[11px] text-red-300">
          {actionError}
        </div>
      ) : null}

      {result ? (
        <div className="mt-2 rounded-md border border-surface-800 bg-surface-900/40 p-2 text-[11px] text-surface-300">
          {/* The agent's own message already distinguishes invited / added /
              already_member. Prefer it over inventing a cheerier sentence. */}
          {result.invite?.message ||
            `${result.invite?.state || "done"}: ${result.user} on ${result.repo}`}
          {result.invite?.state === "added" ? (
            <div className="mt-1 text-surface-500">
              They already had access, so no invitation email was sent.
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
