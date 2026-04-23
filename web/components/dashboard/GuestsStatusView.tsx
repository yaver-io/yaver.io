"use client";

import { useCallback, useEffect, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import { agentClient, type GuestConfigEntry } from "@/lib/agent-client";
import {
  acceptGuestByCode,
  acceptGuestInvitation,
  fetchGuestHosts,
  findInviteByCode,
  inviteGuest,
  listGuests,
  lookupPublicUser,
  revokeGuest,
  type ActiveHost,
  type GuestInfo,
  type GuestInvitation,
  type InvitationPreview,
  type PublicUserLookup,
} from "@/lib/guests";

type Scope = "full" | "feedback-only" | "sdk-project";

function StatusBadge({ status }: { status: string }) {
  const map: Record<string, { bg: string; fg: string }> = {
    pending: { bg: "bg-amber-500/10 border-amber-500/40", fg: "text-amber-300" },
    accepted: { bg: "bg-emerald-500/10 border-emerald-500/40", fg: "text-emerald-300" },
    revoked: { bg: "bg-red-500/10 border-red-500/40", fg: "text-red-300" },
    expired: { bg: "bg-surface-800 border-surface-700", fg: "text-surface-400" },
    active: { bg: "bg-emerald-500/10 border-emerald-500/40", fg: "text-emerald-300" },
  };
  const tone = map[status] ?? map.pending;
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${tone.bg} ${tone.fg}`}
    >
      {status}
    </span>
  );
}

function ScopeButton({
  active,
  onClick,
  label,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`border px-2 py-1 text-xs ${
        active
          ? "border-indigo-500 bg-indigo-500/15 text-indigo-200"
          : "border-surface-700 bg-surface-900 text-surface-400 hover:text-surface-200"
      }`}
    >
      {label}
    </button>
  );
}

export default function GuestsStatusView() {
  const { token, user } = useAuth();
  const { devices } = useDevices(token);
  const [guests, setGuests] = useState<GuestInfo[]>([]);
  const [hostsPending, setHostsPending] = useState<GuestInvitation[]>([]);
  const [hostsActive, setHostsActive] = useState<ActiveHost[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const [inviteKind, setInviteKind] = useState<"email" | "user-id">("email");
  const [inviteTarget, setInviteTarget] = useState("");
  const [inviteScope, setInviteScope] = useState<Scope>("feedback-only");
  const [inviteProjects, setInviteProjects] = useState<string[]>([]);
  const [inviteDeviceIds, setInviteDeviceIds] = useState<string[]>([]);
  const [inviteLookup, setInviteLookup] = useState<PublicUserLookup | null>(null);
  const [inviteLookupErr, setInviteLookupErr] = useState<string | null>(null);
  const [lastInvite, setLastInvite] = useState<{ code: string; target: string; scope: string } | null>(null);

  const [joinCode, setJoinCode] = useState("");
  const [joinPreview, setJoinPreview] = useState<InvitationPreview | null>(null);
  const [joinPreviewErr, setJoinPreviewErr] = useState<string | null>(null);
  const [joinApprovedDeviceIds, setJoinApprovedDeviceIds] = useState<string[]>([]);

  const [guestConfigs, setGuestConfigs] = useState<GuestConfigEntry[]>([]);
  const [projectOptions, setProjectOptions] = useState<string[]>([]);
  const [policyNote, setPolicyNote] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setErr(null);
    try {
      const [g, h] = await Promise.all([listGuests(token), fetchGuestHosts(token)]);
      setGuests(g);
      setHostsPending(h.pending || []);
      setHostsActive(h.active || []);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!token || inviteKind !== "user-id") {
      setInviteLookup(null);
      setInviteLookupErr(null);
      return;
    }
    const value = inviteTarget.trim();
    if (value.length < 3) {
      setInviteLookup(null);
      setInviteLookupErr(null);
      return;
    }
    let alive = true;
    const timer = window.setTimeout(async () => {
      try {
        const found = await lookupPublicUser(token, value);
        if (!alive) return;
        if (found) {
          setInviteLookup(found);
          setInviteLookupErr(null);
        } else {
          setInviteLookup(null);
          setInviteLookupErr("No Yaver user with that id");
        }
      } catch (e) {
        if (!alive) return;
        setInviteLookup(null);
        setInviteLookupErr(e instanceof Error ? e.message : String(e));
      }
    }, 350);
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [inviteKind, inviteTarget, token]);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const [configs, projects] = await Promise.all([
          agentClient.getGuestConfigs(),
          agentClient.listProjects(),
        ]);
        if (!alive) return;
        setGuestConfigs(configs);
        setProjectOptions(projects.map((p) => p.name).filter(Boolean));
        setPolicyNote(null);
      } catch {
        if (!alive) return;
        setGuestConfigs([]);
        setProjectOptions([]);
        setPolicyNote("Connect to one of your devices to edit runtime guest policy and project slices.");
      }
    })();
    return () => {
      alive = false;
    };
  }, []);

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      /* noop */
    }
  }

  const ownDevices = devices.filter((d) => !d.isGuest);
  const projectChoices = Array.from(new Set(projectOptions)).sort((a, b) => a.localeCompare(b));
  const configByEmail = new Map(guestConfigs.map((cfg) => [cfg.guestEmail, cfg]));

  function toggleProject(name: string) {
    setInviteProjects((prev) => (prev.includes(name) ? prev.filter((v) => v !== name) : [...prev, name]));
  }

  function toggleInviteDevice(id: string) {
    setInviteDeviceIds((prev) => (prev.includes(id) ? prev.filter((v) => v !== id) : [...prev, id]));
  }

  function toggleJoinDevice(id: string) {
    setJoinApprovedDeviceIds((prev) => (prev.includes(id) ? prev.filter((v) => v !== id) : [...prev, id]));
  }

  async function handleInvite() {
    if (!token || !inviteTarget.trim()) return;
    setBusy("invite");
    setErr(null);
    try {
      const result = await inviteGuest(token, {
        email: inviteKind === "email" ? inviteTarget.trim() : undefined,
        userId: inviteKind === "user-id" ? inviteTarget.trim() : undefined,
        deviceIds: inviteDeviceIds,
        scope: inviteScope,
        allowedProjects: inviteProjects,
      });
      setLastInvite({
        code: result.inviteCode,
        target: inviteKind === "email" ? inviteTarget.trim() : (inviteLookup?.email || inviteTarget.trim()),
        scope: result.scope || inviteScope,
      });
      setInviteTarget("");
      setInviteLookup(null);
      setInviteProjects([]);
      setInviteDeviceIds([]);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function handlePreviewJoin() {
    if (!token || !joinCode.trim()) return;
    setBusy("preview");
    setJoinPreviewErr(null);
    try {
      const preview = await findInviteByCode(token, joinCode.trim());
      setJoinPreview(preview);
      const defaults =
        preview.proposedDeviceIds && preview.proposedDeviceIds.length > 0
          ? preview.proposedDeviceIds
          : preview.hostDevices.map((d) => d.deviceId);
      setJoinApprovedDeviceIds(defaults);
    } catch (e) {
      setJoinPreview(null);
      setJoinPreviewErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function handleAcceptCode() {
    if (!token || !joinPreview) return;
    setBusy(`accept-code:${joinPreview.inviteCode}`);
    setErr(null);
    try {
      await acceptGuestByCode(token, joinPreview.inviteCode, joinApprovedDeviceIds);
      setJoinCode("");
      setJoinPreview(null);
      setJoinApprovedDeviceIds([]);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function handleAcceptPending(invite: GuestInvitation) {
    if (!token) return;
    const defaults = invite.proposedDeviceIds && invite.proposedDeviceIds.length > 0 ? invite.proposedDeviceIds : undefined;
    setBusy(`accept:${invite.hostUserId}`);
    setErr(null);
    try {
      await acceptGuestInvitation(token, invite.hostUserId, defaults);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function handleRevoke(guest: GuestInfo) {
    if (!token) return;
    setBusy(`revoke:${guest.email || guest.userId}`);
    setErr(null);
    try {
      await revokeGuest(token, guest.email ? { email: guest.email } : { userId: guest.userId });
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function handleScopeUpdate(email: string, scope: Scope, allowedProjects: string[]) {
    setBusy(`policy:${email}`);
    setErr(null);
    try {
      await agentClient.updateGuestConfig({
        email,
        scope,
        allowedProjects: scope === "sdk-project" ? allowedProjects : [],
      });
      const refreshed = await agentClient.getGuestConfigs();
      setGuestConfigs(refreshed);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-surface-100">Guest sharing</h2>
        <p className="text-sm text-surface-500">
          Invite by email or Yaver user ID, hand off the 6-character code, and slice access by machine, scope, and project when the agent is connected.
        </p>
      </div>

      <section className="grid gap-4 xl:grid-cols-[1.2fr_0.8fr]">
        <div className="space-y-4 rounded-lg border border-surface-800 bg-surface-900/40 p-4">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-surface-100">Invite a guest</h3>
              <p className="mt-1 text-xs text-surface-500">
                Human onboarding belongs here. Use Yaver Tokens for remote boxes and machine-to-machine auth.
              </p>
            </div>
            <StatusBadge status="pending" />
          </div>

          <div className="flex flex-wrap gap-2">
            <ScopeButton active={inviteKind === "email"} onClick={() => setInviteKind("email")} label="By Email" />
            <ScopeButton active={inviteKind === "user-id"} onClick={() => setInviteKind("user-id")} label="By User ID" />
          </div>

          <input
            value={inviteTarget}
            onChange={(e) => setInviteTarget(e.target.value)}
            placeholder={inviteKind === "email" ? "email@example.com" : "user id"}
            className="w-full border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none placeholder:text-surface-600"
          />

          {inviteKind === "user-id" && inviteLookup && (
            <div className="text-xs text-emerald-300">
              {inviteLookup.fullName} · {inviteLookup.email}
            </div>
          )}
          {inviteKind === "user-id" && inviteLookupErr && (
            <div className="text-xs text-red-300">{inviteLookupErr}</div>
          )}

          <div className="space-y-2">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Access scope</div>
            <div className="flex flex-wrap gap-2">
              <ScopeButton active={inviteScope === "feedback-only"} onClick={() => setInviteScope("feedback-only")} label="Feedback Only" />
              <ScopeButton active={inviteScope === "sdk-project"} onClick={() => setInviteScope("sdk-project")} label="SDK Project" />
              <ScopeButton active={inviteScope === "full"} onClick={() => setInviteScope("full")} label="Full" />
            </div>
          </div>

          {projectChoices.length > 0 && (
            <div className="space-y-2">
              <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Project slice</div>
              <div className="flex flex-wrap gap-2">
                {projectChoices.map((project) => (
                  <button
                    key={project}
                    type="button"
                    onClick={() => toggleProject(project)}
                    className={`border px-2 py-1 text-xs ${
                      inviteProjects.includes(project)
                        ? "border-indigo-500 bg-indigo-500/15 text-indigo-200"
                        : "border-surface-700 bg-surface-950 text-surface-400"
                    }`}
                  >
                    {project}
                  </button>
                ))}
              </div>
              <div className="text-xs text-surface-500">
                Leave this empty to keep the invite broad within the selected scope.
              </div>
            </div>
          )}

          {ownDevices.length > 0 && (
            <div className="space-y-2">
              <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Machine slice</div>
              <div className="grid gap-2 sm:grid-cols-2">
                {ownDevices.map((device) => (
                  <button
                    key={device.id}
                    type="button"
                    onClick={() => toggleInviteDevice(device.id)}
                    className={`border px-3 py-2 text-left text-xs ${
                      inviteDeviceIds.includes(device.id)
                        ? "border-indigo-500 bg-indigo-500/15 text-surface-100"
                        : "border-surface-700 bg-surface-950 text-surface-400"
                    }`}
                  >
                    <div className="font-semibold text-surface-200">{device.name}</div>
                    <div className="mt-1 font-mono">{device.id}</div>
                  </button>
                ))}
              </div>
              <div className="text-xs text-surface-500">
                Leave every machine unselected to propose all of your machines.
              </div>
            </div>
          )}

          <div className="flex items-center justify-between gap-3">
            <div className="text-xs text-surface-500">
              Invite codes expire in 2 days and work even if the guest signs in with a different OAuth email.
            </div>
            <button
              type="button"
              onClick={() => void handleInvite()}
              disabled={busy === "invite" || !inviteTarget.trim()}
              className="bg-indigo-600 px-4 py-2 text-sm font-semibold text-white disabled:opacity-40"
            >
              {busy === "invite" ? "Sending…" : "Send Invite"}
            </button>
          </div>
        </div>

        <div className="space-y-4">
          {user?.id ? (
            <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-4">
              <div className="text-[10px] uppercase tracking-wider text-surface-500 font-bold">Your user ID</div>
              <div className="mt-2 break-all font-mono text-sm text-surface-100">{user.id}</div>
              <div className="mt-2 text-xs text-surface-500">People can invite you without knowing your email.</div>
              <button
                onClick={() => copy(user.id)}
                className="mt-3 border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-300"
              >
                Copy User ID
              </button>
            </div>
          ) : null}

          {lastInvite && (
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4">
              <div className="text-[10px] uppercase tracking-wider text-emerald-300">Latest invite</div>
              <div className="mt-1 text-sm text-surface-200">{lastInvite.target}</div>
              <div className="mt-1 text-xs text-surface-500">scope: {lastInvite.scope}</div>
              <div className="mt-3 font-mono text-3xl font-semibold tracking-[0.3em] text-surface-50">{lastInvite.code}</div>
              <div className="mt-3 flex gap-2">
                <button onClick={() => copy(lastInvite.code)} className="border border-surface-700 bg-surface-950 px-3 py-2 text-xs text-surface-200">
                  Copy Code
                </button>
                <button
                  onClick={() => copy(`Your Yaver invite code: ${lastInvite.code}`)}
                  className="border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs text-indigo-300"
                >
                  Copy Message
                </button>
              </div>
            </div>
          )}
        </div>
      </section>

      <section className="rounded-lg border border-surface-800 bg-surface-900/40 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-surface-100">Join with invite code</h3>
            <p className="mt-1 text-xs text-surface-500">
              Use this when the host invited a different email than the one you signed in with.
            </p>
          </div>
          <div className="text-xs text-surface-500">Out-of-band code or pending invite</div>
        </div>
        <div className="mt-4 flex gap-2">
          <input
            value={joinCode}
            onChange={(e) => setJoinCode(e.target.value.toUpperCase())}
            placeholder="Invite code"
            className="flex-1 border border-surface-700 bg-surface-950 px-3 py-2 text-sm font-mono text-surface-100"
          />
          <button
            type="button"
            onClick={() => void handlePreviewJoin()}
            disabled={busy === "preview" || !joinCode.trim()}
            className="border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-300 disabled:opacity-40"
          >
            {busy === "preview" ? "Checking…" : "Preview"}
          </button>
        </div>
        {joinPreviewErr && <div className="mt-3 text-sm text-red-300">{joinPreviewErr}</div>}
        {joinPreview && (
          <div className="mt-4 space-y-3 border border-surface-800 bg-surface-950/60 p-3">
            <div>
              <div className="text-sm text-surface-100">{joinPreview.hostName}</div>
              <div className="text-xs text-surface-500">{joinPreview.hostEmail}</div>
            </div>
            <div className="grid gap-2 md:grid-cols-2">
              {joinPreview.hostDevices.map((device) => (
                <button
                  key={device.deviceId}
                  type="button"
                  onClick={() => toggleJoinDevice(device.deviceId)}
                  className={`border px-3 py-2 text-left text-xs ${
                    joinApprovedDeviceIds.includes(device.deviceId)
                      ? "border-indigo-500 bg-indigo-500/15 text-surface-100"
                      : "border-surface-700 bg-surface-950 text-surface-400"
                  }`}
                >
                  <div className="font-semibold text-surface-200">{device.name}</div>
                  <div className="mt-1 font-mono">{device.deviceId}</div>
                </button>
              ))}
            </div>
            <div className="flex items-center justify-between gap-3">
              <div className="text-xs text-surface-500">
                Expires {new Date(joinPreview.expiresAt).toLocaleString()}
              </div>
              <button
                type="button"
                onClick={() => void handleAcceptCode()}
                disabled={busy === `accept-code:${joinPreview.inviteCode}`}
                className="bg-emerald-600 px-4 py-2 text-sm font-semibold text-white disabled:opacity-40"
              >
                {busy === `accept-code:${joinPreview.inviteCode}` ? "Joining…" : "Accept Invite"}
              </button>
            </div>
          </div>
        )}
      </section>

      {err && <div className="rounded border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-200">{err}</div>}
      {loading && <div className="text-sm text-surface-500">Loading…</div>}

      {/* Guests I'm hosting */}
      <section className="space-y-2">
        <h3 className="text-xs uppercase tracking-wider text-surface-500 font-bold">
          People I share with
        </h3>
        {guests.length === 0 ? (
          <p className="text-sm text-surface-500">No guests yet.</p>
        ) : (
          <ul className="divide-y divide-surface-800 rounded-lg border border-surface-800 bg-surface-900/40">
            {guests.map((g, i) => (
              <li key={(g.email || g.userId || "guest") + String(i)} className="space-y-3 p-3">
                <div className="flex items-center gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-surface-100 truncate">
                        {g.fullName || g.email || `user ${g.userId ?? ""}`}
                      </span>
                      <StatusBadge status={g.status} />
                      {g.invitedByUserId && (
                        <span className="rounded bg-indigo-500/10 border border-indigo-500/40 px-2 py-0.5 text-[10px] font-semibold text-indigo-300">
                          BY USER ID
                        </span>
                      )}
                    </div>
                    <div className="mt-1 text-xs text-surface-500">
                      {g.email ? g.email + " · " : ""}
                      {g.acceptedAt
                        ? "joined " + new Date(g.acceptedAt).toLocaleDateString()
                        : g.createdAt
                          ? "invited " + new Date(g.createdAt).toLocaleDateString()
                          : ""}
                      {g.proposedDeviceIds && g.proposedDeviceIds.length > 0
                        ? ` · scoped to ${g.proposedDeviceIds.length} machine${g.proposedDeviceIds.length === 1 ? "" : "s"}`
                        : ""}
                    </div>
                  </div>
                  {g.status === "pending" && g.inviteCode && (
                    <div className="flex items-center gap-2">
                      <code className="rounded bg-surface-900 px-2 py-1 text-xs font-mono text-surface-200 border border-surface-700">
                        {g.inviteCode}
                      </code>
                      <button
                        onClick={() => copy(g.inviteCode!)}
                        className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-[11px] text-surface-300 hover:bg-surface-800"
                      >
                        Copy
                      </button>
                    </div>
                  )}
                  <button
                    type="button"
                    onClick={() => void handleRevoke(g)}
                    disabled={busy === `revoke:${g.email || g.userId}`}
                    className="rounded border border-red-500/30 bg-red-500/10 px-2 py-1 text-[11px] text-red-200 disabled:opacity-40"
                  >
                    Revoke
                  </button>
                </div>

                {g.status === "accepted" && g.email && (
                  <div className="border border-surface-800 bg-surface-950/60 p-3">
                    <div className="mb-2 flex items-center justify-between gap-3">
                      <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">
                        Runtime policy
                      </div>
                      {policyNote && <div className="text-[11px] text-surface-500">{policyNote}</div>}
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {(["feedback-only", "sdk-project", "full"] as Scope[]).map((scope) => {
                        const current = (configByEmail.get(g.email)?.scope || "full") as Scope;
                        return (
                          <button
                            key={scope}
                            type="button"
                            onClick={() =>
                              void handleScopeUpdate(
                                g.email,
                                scope,
                                configByEmail.get(g.email)?.allowedProjects || [],
                              )
                            }
                            disabled={!!policyNote || busy === `policy:${g.email}`}
                            className={`border px-2 py-1 text-xs disabled:opacity-40 ${
                              current === scope
                                ? "border-indigo-500 bg-indigo-500/15 text-indigo-200"
                                : "border-surface-700 bg-surface-950 text-surface-400"
                            }`}
                          >
                            {scope}
                          </button>
                        );
                      })}
                    </div>
                    {projectChoices.length > 0 && (
                      <div className="mt-3 space-y-2">
                        <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">
                          Allowed projects
                        </div>
                        <div className="flex flex-wrap gap-2">
                          {projectChoices.map((project) => {
                            const current = configByEmail.get(g.email)?.allowedProjects || [];
                            const selected = current.includes(project);
                            return (
                              <button
                                key={project}
                                type="button"
                                disabled={!!policyNote || busy === `policy:${g.email}`}
                                onClick={() => {
                                  const next = selected
                                    ? current.filter((p) => p !== project)
                                    : [...current, project];
                                  void handleScopeUpdate(
                                    g.email,
                                    ((configByEmail.get(g.email)?.scope || "sdk-project") as Scope),
                                    next,
                                  );
                                }}
                                className={`border px-2 py-1 text-xs disabled:opacity-40 ${
                                  selected
                                    ? "border-indigo-500 bg-indigo-500/15 text-indigo-200"
                                    : "border-surface-700 bg-surface-950 text-surface-400"
                                }`}
                              >
                                {project}
                              </button>
                            );
                          })}
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-2">
        <h3 className="text-xs uppercase tracking-wider text-surface-500 font-bold">
          Pending and active access for me
        </h3>
        {hostsPending.length === 0 && hostsActive.length === 0 ? (
          <p className="text-sm text-surface-500">Nobody has shared a machine with you yet.</p>
        ) : (
          <ul className="divide-y divide-surface-800 rounded-lg border border-surface-800 bg-surface-900/40">
            {hostsPending.map((h) => (
              <li key={h.inviteId || h.hostUserId} className="flex items-center gap-3 p-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm text-surface-100 truncate">
                      {h.hostName}
                      <span className="text-surface-500">{" · "}{h.hostEmail}</span>
                    </span>
                    <StatusBadge status="pending" />
                  </div>
                  <div className="mt-1 text-xs text-surface-500">
                    Invited {new Date(h.createdAt).toLocaleDateString()} · expires{" "}
                    {new Date(h.expiresAt).toLocaleDateString()}
                    {h.proposedDeviceIds && h.proposedDeviceIds.length > 0
                      ? ` · scope: ${h.proposedDeviceIds.length} machine${h.proposedDeviceIds.length === 1 ? "" : "s"}`
                      : ""}
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => void handleAcceptPending(h)}
                  disabled={busy === `accept:${h.hostUserId}`}
                  className="bg-emerald-600 px-3 py-2 text-xs font-semibold text-white disabled:opacity-40"
                >
                  {busy === `accept:${h.hostUserId}` ? "Accepting…" : "Accept"}
                </button>
              </li>
            ))}
            {hostsActive.map((h) => (
              <li key={h.hostUserId} className="flex items-center gap-3 p-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm text-surface-100 truncate">
                      {h.hostName}
                      <span className="text-surface-500">{" · "}{h.hostEmail}</span>
                    </span>
                    <StatusBadge status="active" />
                  </div>
                  <div className="mt-1 text-xs text-surface-500">
                    Since {new Date(h.grantedAt).toLocaleDateString()}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
