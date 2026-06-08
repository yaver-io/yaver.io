"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import {
  listConnections,
  requestConnection,
  acceptConnection,
  removeConnection,
  blockConnection,
  suggestedConnections,
  type Connection,
  type ConnectionsResponse,
  type SuggestedConnection,
} from "@/lib/connections";
import {
  listProjectShares,
  createProjectShare,
  inviteToProject,
  acceptProjectShare,
  setProjectMemberRole,
  revokeProjectMember,
  archiveProjectShare,
  type OwnedProjectShare,
  type JoinedProjectShare,
  type ProjectRole,
} from "@/lib/projectShares";

// People & Shared Projects. The social graph (address book) + the
// invite-to-code wrapper. Friends here become one-tap targets when
// sharing a project, so a developer can pull a non-technical collaborator
// ("normie") into a repo hosted on their own machine OR a Yaver-managed box.

function friendlyError(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  if (/failed to fetch|load failed|networkerror/i.test(msg)) {
    return "Couldn't reach the server. Check your connection and try again.";
  }
  return msg;
}

const ROLE_BLURB: Record<ProjectRole, string> = {
  owner: "Full control.",
  dev: "Codes, pushes to a feature branch, opens PRs, can deploy.",
  normie: "Codes with AI on their own branch, opens PRs. No deploy, no main.",
  viewer: "Observes only — no code changes.",
};

// ── Small UI atoms (inline SVG icons, per web/ icon policy) ──────────

function Icon({ path, className = "h-4 w-4" }: { path: string; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d={path} />
    </svg>
  );
}
const I = {
  user: "M20 21a8 8 0 0 0-16 0 M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8",
  check: "M20 6 9 17l-5-5",
  x: "M18 6 6 18 M6 6l12 12",
  plus: "M12 5v14 M5 12h14",
  link: "M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1 M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1",
  folder: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
};

function Avatar({ name }: { name: string }) {
  const initials = (name || "?").split(/\s+/).map((p) => p[0]).slice(0, 2).join("").toUpperCase();
  return (
    <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-semibold text-surface-200">
      {initials || "?"}
    </div>
  );
}

function Banner({ msg }: { msg: { type: "ok" | "error"; text: string } | null }) {
  if (!msg) return null;
  return (
    <div className={`rounded-md px-3 py-2 text-xs ${msg.type === "ok" ? "bg-emerald-500/10 text-emerald-300" : "bg-red-500/10 text-red-300"}`}>
      {msg.text}
    </div>
  );
}

// ── People (connections) ────────────────────────────────────────────

function PeopleSection({ token }: { token: string }) {
  const [data, setData] = useState<ConnectionsResponse>({ accepted: [], incoming: [], outgoing: [], blocked: [] });
  const [suggested, setSuggested] = useState<SuggestedConnection[]>([]);
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [conns, sugg] = await Promise.all([listConnections(token), suggestedConnections(token).catch(() => [])]);
      setData(conns);
      setSuggested(sugg);
    } catch (e) {
      setMsg({ type: "error", text: friendlyError(e) });
    }
  }, [token]);

  useEffect(() => { void refresh(); }, [refresh]);

  const flash = (m: { type: "ok" | "error"; text: string }) => {
    setMsg(m);
    setTimeout(() => setMsg(null), 3500);
  };

  async function send() {
    const q = target.trim();
    if (!q) return;
    setBusy(true);
    try {
      const isEmail = q.includes("@");
      const res = await requestConnection(token, isEmail ? { peerEmail: q, source: "manual" } : { peerUserId: q, source: "manual" });
      flash({ type: "ok", text: res.status === "accepted" ? "Connected!" : "Request sent." });
      setTarget("");
      await refresh();
    } catch (e) {
      flash({ type: "error", text: friendlyError(e) });
    } finally {
      setBusy(false);
    }
  }

  async function act(fn: () => Promise<void>, okText: string) {
    setBusy(true);
    try {
      await fn();
      flash({ type: "ok", text: okText });
      await refresh();
    } catch (e) {
      flash({ type: "error", text: friendlyError(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-5">
      <div className="space-y-2">
        <label className="text-xs font-medium text-surface-400">Add someone by email or user id</label>
        <div className="flex gap-2">
          <input
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && send()}
            placeholder="friend@example.com"
            className="flex-1 rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 placeholder:text-surface-600 focus:border-surface-500 focus:outline-none"
          />
          <button onClick={send} disabled={busy || !target.trim()} className="flex items-center gap-1 rounded-md bg-surface-100 px-3 py-2 text-sm font-medium text-surface-900 disabled:opacity-40">
            <Icon path={I.plus} /> Connect
          </button>
        </div>
      </div>

      <Banner msg={msg} />

      {data.incoming.length > 0 && (
        <Group title={`Requests (${data.incoming.length})`}>
          {data.incoming.map((c) => (
            <Row key={c.peerUserId} c={c}>
              <button onClick={() => act(() => acceptConnection(token, c.peerUserId), "Connected!")} disabled={busy} className="rounded-md bg-emerald-500/15 p-1.5 text-emerald-300 hover:bg-emerald-500/25" title="Accept">
                <Icon path={I.check} />
              </button>
              <button onClick={() => act(() => removeConnection(token, c.peerUserId), "Declined.")} disabled={busy} className="rounded-md bg-surface-800 p-1.5 text-surface-400 hover:text-surface-200" title="Decline">
                <Icon path={I.x} />
              </button>
            </Row>
          ))}
        </Group>
      )}

      <Group title={`Connections (${data.accepted.length})`} empty={data.accepted.length === 0 ? "No connections yet. Add someone above." : undefined}>
        {data.accepted.map((c) => (
          <Row key={c.peerUserId} c={c}>
            <button onClick={() => act(() => removeConnection(token, c.peerUserId), "Removed.")} disabled={busy} className="rounded-md bg-surface-800 px-2 py-1 text-xs text-surface-400 hover:text-surface-200">Remove</button>
            <button onClick={() => act(() => blockConnection(token, c.peerUserId), "Blocked.")} disabled={busy} className="rounded-md bg-surface-800 px-2 py-1 text-xs text-surface-500 hover:text-red-300">Block</button>
          </Row>
        ))}
      </Group>

      {data.outgoing.length > 0 && (
        <Group title={`Pending (${data.outgoing.length})`}>
          {data.outgoing.map((c) => (
            <Row key={c.peerUserId} c={c}>
              <span className="text-xs text-surface-500">Sent</span>
              <button onClick={() => act(() => removeConnection(token, c.peerUserId), "Cancelled.")} disabled={busy} className="rounded-md bg-surface-800 px-2 py-1 text-xs text-surface-400 hover:text-surface-200">Cancel</button>
            </Row>
          ))}
        </Group>
      )}

      {suggested.length > 0 && (
        <Group title="Suggested — people you already collaborate with">
          {suggested.map((s) => (
            <div key={s.userId} className="flex items-center gap-3 rounded-md border border-surface-800 px-3 py-2">
              <Avatar name={s.fullName} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-surface-100">{s.fullName}</div>
                <div className="truncate text-xs text-surface-500">{s.email} · via {s.source}</div>
              </div>
              <button onClick={() => act(() => requestConnection(token, { peerUserId: s.userId, source: "suggested" }).then(() => {}), "Request sent.")} disabled={busy} className="flex items-center gap-1 rounded-md bg-surface-800 px-2 py-1 text-xs text-surface-200 hover:bg-surface-700">
                <Icon path={I.plus} className="h-3.5 w-3.5" /> Connect
              </button>
            </div>
          ))}
        </Group>
      )}
    </div>
  );
}

function Group({ title, children, empty }: { title: string; children?: React.ReactNode; empty?: string }) {
  return (
    <div className="space-y-2">
      <h4 className="text-xs font-semibold uppercase tracking-wide text-surface-500">{title}</h4>
      {empty ? <p className="text-sm text-surface-600">{empty}</p> : <div className="space-y-1.5">{children}</div>}
    </div>
  );
}

function Row({ c, children }: { c: Connection; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3 rounded-md border border-surface-800 px-3 py-2">
      <Avatar name={c.nickname || c.fullName} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm text-surface-100">{c.nickname || c.fullName}</div>
        <div className="truncate text-xs text-surface-500">{c.email}</div>
      </div>
      <div className="flex items-center gap-1.5">{children}</div>
    </div>
  );
}

// ── Shared Projects ─────────────────────────────────────────────────

function ProjectsSection({ token }: { token: string }) {
  const { devices } = useDevices(token);
  const [owned, setOwned] = useState<OwnedProjectShare[]>([]);
  const [joined, setJoined] = useState<JoinedProjectShare[]>([]);
  const [connections, setConnections] = useState<Connection[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [joinCode, setJoinCode] = useState("");

  // create form
  const [slug, setSlug] = useState("");
  const [repoUrl, setRepoUrl] = useState("");
  const [hostKind, setHostKind] = useState<"owner-device" | "managed-cloud">("owner-device");
  const [hostDeviceId, setHostDeviceId] = useState("");
  const [payer, setPayer] = useState<"owner" | "invitee">("owner");

  const refresh = useCallback(async () => {
    try {
      const [shares, conns] = await Promise.all([listProjectShares(token), listConnections(token).catch(() => null)]);
      setOwned(shares.owned);
      setJoined(shares.joined);
      if (conns) setConnections(conns.accepted);
    } catch (e) {
      setMsg({ type: "error", text: friendlyError(e) });
    }
  }, [token]);

  useEffect(() => { void refresh(); }, [refresh]);

  const onlineDevices = useMemo(() => devices.filter((d) => d.online), [devices]);

  const flash = (m: { type: "ok" | "error"; text: string }) => {
    setMsg(m);
    setTimeout(() => setMsg(null), 4000);
  };

  async function create() {
    if (!slug.trim() || !repoUrl.trim()) return;
    setBusy(true);
    try {
      await createProjectShare(token, {
        slug: slug.trim(),
        repoUrl: repoUrl.trim(),
        hostKind,
        hostDeviceId: hostKind === "owner-device" ? hostDeviceId || undefined : undefined,
        payer: hostKind === "managed-cloud" ? payer : undefined,
      });
      flash({ type: "ok", text: "Project created. Invite people below." });
      setShowCreate(false);
      setSlug(""); setRepoUrl(""); setHostDeviceId("");
      await refresh();
    } catch (e) {
      flash({ type: "error", text: friendlyError(e) });
    } finally {
      setBusy(false);
    }
  }

  async function join() {
    const code = joinCode.trim();
    if (!code) return;
    setBusy(true);
    try {
      const res = await acceptProjectShare(token, code);
      flash({ type: "ok", text: `Joined ${res.slug || "project"} as ${res.role || "member"}.` });
      setJoinCode("");
      await refresh();
    } catch (e) {
      flash({ type: "error", text: friendlyError(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <button onClick={() => setShowCreate((s) => !s)} className="flex items-center gap-1 rounded-md bg-surface-100 px-3 py-2 text-sm font-medium text-surface-900">
          <Icon path={I.plus} /> Share a project
        </button>
        <div className="flex flex-1 gap-2">
          <input
            value={joinCode}
            onChange={(e) => setJoinCode(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && join()}
            placeholder="Have a code? Join a project"
            className="flex-1 rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm uppercase text-surface-100 placeholder:normal-case placeholder:text-surface-600 focus:border-surface-500 focus:outline-none"
          />
          <button onClick={join} disabled={busy || !joinCode.trim()} className="rounded-md bg-surface-800 px-3 py-2 text-sm text-surface-200 disabled:opacity-40">Join</button>
        </div>
      </div>

      <Banner msg={msg} />

      {showCreate && (
        <div className="space-y-3 rounded-lg border border-surface-700 bg-surface-900/50 p-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Project name">
              <input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="acme-app" className="input" />
            </Field>
            <Field label="Repo URL">
              <input value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} placeholder="github.com/me/acme-app" className="input" />
            </Field>
          </div>
          <Field label="Where does collaborators' work run?">
            <div className="flex gap-2">
              <Choice active={hostKind === "owner-device"} onClick={() => setHostKind("owner-device")} label="My machine" />
              <Choice active={hostKind === "managed-cloud"} onClick={() => setHostKind("managed-cloud")} label="Yaver Cloud" />
            </div>
          </Field>
          {hostKind === "owner-device" ? (
            <Field label="Host device">
              <select value={hostDeviceId} onChange={(e) => setHostDeviceId(e.target.value)} className="input">
                <option value="">Any of my devices</option>
                {onlineDevices.map((d) => (
                  <option key={d.id} value={d.id}>{d.alias || d.name} ({d.platform})</option>
                ))}
              </select>
            </Field>
          ) : (
            <Field label="Who pays for the cloud box?">
              <div className="flex gap-2">
                <Choice active={payer === "owner"} onClick={() => setPayer("owner")} label="I pay" />
                <Choice active={payer === "invitee"} onClick={() => setPayer("invitee")} label="They pay" />
              </div>
            </Field>
          )}
          <div className="flex justify-end gap-2">
            <button onClick={() => setShowCreate(false)} className="rounded-md px-3 py-1.5 text-sm text-surface-400">Cancel</button>
            <button onClick={create} disabled={busy || !slug.trim() || !repoUrl.trim()} className="rounded-md bg-surface-100 px-3 py-1.5 text-sm font-medium text-surface-900 disabled:opacity-40">Create</button>
          </div>
        </div>
      )}

      {owned.length === 0 && joined.length === 0 && (
        <p className="text-sm text-surface-600">No shared projects yet. Share a repo to bring a collaborator in.</p>
      )}

      {owned.map((p) => (
        <OwnedProjectCard key={p.shareId} share={p} token={token} connections={connections} busy={busy} setBusy={setBusy} flash={flash} refresh={refresh} />
      ))}

      {joined.length > 0 && (
        <Group title="Shared with me">
          {joined.map((p) => (
            <div key={p.shareId} className="flex items-center gap-3 rounded-md border border-surface-800 px-3 py-2">
              <Icon path={I.folder} className="h-5 w-5 text-surface-500" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-surface-100">{p.slug} <span className="text-xs text-surface-500">· {p.role}</span></div>
                <div className="truncate text-xs text-surface-500">{p.repoUrl} · from {p.ownerName} · branch {p.branch || "—"}</div>
              </div>
            </div>
          ))}
        </Group>
      )}
    </div>
  );
}

function OwnedProjectCard({
  share, token, connections, busy, setBusy, flash, refresh,
}: {
  share: OwnedProjectShare;
  token: string;
  connections: Connection[];
  busy: boolean;
  setBusy: (b: boolean) => void;
  flash: (m: { type: "ok" | "error"; text: string }) => void;
  refresh: () => Promise<void>;
}) {
  const [invitee, setInvitee] = useState("");
  const [role, setRole] = useState<"dev" | "normie" | "viewer">("normie");

  async function invite() {
    const q = invitee.trim();
    if (!q) return;
    setBusy(true);
    try {
      const isEmail = q.includes("@");
      await inviteToProject(token, { shareId: share.shareId, ...(isEmail ? { peerEmail: q } : { peerUserId: q }), role });
      flash({ type: "ok", text: `Invited as ${role}.` });
      setInvitee("");
      await refresh();
    } catch (e) {
      flash({ type: "error", text: friendlyError(e) });
    } finally {
      setBusy(false);
    }
  }

  async function act(fn: () => Promise<void>, okText: string) {
    setBusy(true);
    try { await fn(); flash({ type: "ok", text: okText }); await refresh(); }
    catch (e) { flash({ type: "error", text: friendlyError(e) }); }
    finally { setBusy(false); }
  }

  const others = share.roster.filter((m) => m.role !== "owner");

  return (
    <div className="space-y-3 rounded-lg border border-surface-700 bg-surface-900/40 p-4">
      <div className="flex items-start gap-3">
        <Icon path={I.folder} className="mt-0.5 h-5 w-5 text-surface-400" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium text-surface-100">{share.slug}</div>
          <div className="truncate text-xs text-surface-500">{share.repoUrl}</div>
          <div className="mt-0.5 text-xs text-surface-600">
            {share.hostKind === "managed-cloud" ? `Yaver Cloud (${share.payer === "invitee" ? "they pay" : "you pay"})` : "Your machine"}
          </div>
        </div>
        <CodePill code={share.shareCode} />
      </div>

      <div className="flex flex-wrap items-center gap-2 border-t border-surface-800 pt-3">
        <input
          value={invitee}
          onChange={(e) => setInvitee(e.target.value)}
          list={`conns-${share.shareId}`}
          placeholder="Invite by email / user id"
          className="flex-1 rounded-md border border-surface-700 bg-surface-900 px-3 py-1.5 text-sm text-surface-100 placeholder:text-surface-600 focus:border-surface-500 focus:outline-none"
        />
        <datalist id={`conns-${share.shareId}`}>
          {connections.map((c) => <option key={c.peerUserId} value={c.email}>{c.fullName}</option>)}
        </datalist>
        <select value={role} onChange={(e) => setRole(e.target.value as any)} className="rounded-md border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm text-surface-200">
          <option value="normie">Normie</option>
          <option value="dev">Dev</option>
          <option value="viewer">Viewer</option>
        </select>
        <button onClick={invite} disabled={busy || !invitee.trim()} className="rounded-md bg-surface-100 px-3 py-1.5 text-sm font-medium text-surface-900 disabled:opacity-40">Invite</button>
      </div>
      <p className="text-xs text-surface-600">{ROLE_BLURB[role]}</p>

      {others.length > 0 && (
        <div className="space-y-1.5">
          {others.map((m) => (
            <div key={m.userId || m.email} className="flex items-center gap-3 rounded-md border border-surface-800 px-3 py-1.5">
              <Avatar name={m.fullName} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-surface-100">{m.fullName} <span className="text-xs text-surface-500">· {m.role}</span></div>
                <div className="truncate text-xs text-surface-500">{m.status === "invited" ? "Invited — not yet joined" : `branch ${m.branch || "—"}`}</div>
              </div>
              {m.userId && m.status !== "invited" && (
                <select
                  value={m.role}
                  onChange={(e) => act(() => setProjectMemberRole(token, share.shareId, m.userId, e.target.value as any), "Role updated.")}
                  disabled={busy}
                  className="rounded-md border border-surface-700 bg-surface-900 px-1.5 py-1 text-xs text-surface-300"
                >
                  <option value="normie">normie</option>
                  <option value="dev">dev</option>
                  <option value="viewer">viewer</option>
                </select>
              )}
              {m.userId && (
                <button onClick={() => act(() => revokeProjectMember(token, share.shareId, m.userId), "Removed.")} disabled={busy} className="rounded-md bg-surface-800 p-1.5 text-surface-500 hover:text-red-300" title="Remove">
                  <Icon path={I.x} />
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      <div className="flex justify-end">
        <button onClick={() => act(() => archiveProjectShare(token, share.shareId), "Project archived.")} disabled={busy} className="text-xs text-surface-600 hover:text-red-300">Archive project</button>
      </div>
    </div>
  );
}

function CodePill({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={async () => { try { await navigator.clipboard.writeText(code); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch {} }}
      className="flex items-center gap-1 rounded-md bg-surface-800 px-2 py-1 font-mono text-xs text-surface-200 hover:bg-surface-700"
      title="Copy join code"
    >
      <Icon path={I.link} className="h-3.5 w-3.5" /> {copied ? "Copied" : code}
    </button>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-surface-400">{label}</span>
      {children}
    </label>
  );
}

function Choice({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button onClick={onClick} className={`flex-1 rounded-md border px-3 py-1.5 text-sm ${active ? "border-surface-400 bg-surface-800 text-surface-100" : "border-surface-700 text-surface-400"}`}>
      {label}
    </button>
  );
}

// ── Top-level view ──────────────────────────────────────────────────

export default function CollabView() {
  const { token } = useAuth();
  const [section, setSection] = useState<"people" | "projects">("people");

  if (!token) {
    return <p className="text-sm text-surface-500">Sign in to manage people and shared projects.</p>;
  }

  return (
    <div className="space-y-5">
      <div>
        <h2 className="flex items-center gap-2 text-lg font-semibold text-surface-100">
          <Icon path={I.user} className="h-5 w-5" /> People & Projects
        </h2>
        <p className="mt-1 text-xs text-surface-500">
          Connect with people, then share a repo so they can code with you — on your machine or a Yaver Cloud box. Non-technical
          collaborators get an AI agent, their own branch, and a pull-request flow. No terminal, no tokens.
        </p>
      </div>

      <div className="flex gap-1 rounded-lg bg-surface-900 p-1 text-sm">
        <button onClick={() => setSection("people")} className={`flex-1 rounded-md px-3 py-1.5 ${section === "people" ? "bg-surface-800 text-surface-100" : "text-surface-400"}`}>People</button>
        <button onClick={() => setSection("projects")} className={`flex-1 rounded-md px-3 py-1.5 ${section === "projects" ? "bg-surface-800 text-surface-100" : "text-surface-400"}`}>Shared Projects</button>
      </div>

      {section === "people" ? <PeopleSection token={token} /> : <ProjectsSection token={token} />}
    </div>
  );
}
