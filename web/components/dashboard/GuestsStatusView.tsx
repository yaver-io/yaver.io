"use client";

import { useCallback, useEffect, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import { AgentClient, agentClient, type GuestConfigEntry } from "@/lib/agent-client";
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
  type GuestMachineSummary,
  type InvitationPreview,
  type PublicUserLookup,
} from "@/lib/guests";

type Scope = "full" | "feedback-only" | "sdk-project";

// guests.ts already surfaces clean, server-provided `data.error` strings via
// parseError. The one gap is a transport failure (fetch rejects before any
// response) which throws a raw "Failed to fetch" / "Load failed" TypeError —
// meaningless to a user. Map those to a friendly sentence; pass clean
// server messages through untouched.
function friendlyGuestError(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e ?? "");
  if (!msg || /failed to fetch|load failed|networkerror|network request failed/i.test(msg)) {
    return "Couldn't reach the server — check your connection.";
  }
  return msg;
}

function StatusBadge({ status }: { status: string }) {
  // Pending = informational ("waiting on guest"), not a warning. Accepted/
  // active = success. Revoked = danger. Expired = muted.
  const map: Record<string, { bg: string; fg: string }> = {
    pending: { bg: "bg-info-soft/60 border-info/30", fg: "text-info-softFg" },
    accepted: { bg: "bg-success-soft/60 border-success/30", fg: "text-success-softFg" },
    revoked: { bg: "bg-danger-soft/60 border-danger/30", fg: "text-danger-softFg" },
    expired: { bg: "bg-surface-800 border-surface-700", fg: "text-surface-400" },
    active: { bg: "bg-success-soft/60 border-success/30", fg: "text-success-softFg" },
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
      className={`rounded-md border px-2.5 py-1 text-xs font-medium transition-colors ${
        active
          ? "border-brand/40 bg-brand-soft text-brand-softFg"
          : "border-surface-700 bg-surface-900 text-surface-400 hover:text-surface-200 hover:border-surface-600"
      }`}
    >
      {label}
    </button>
  );
}

function formatPlatform(platform?: string) {
  const value = String(platform || "").trim();
  if (!value) return "Unknown OS";
  return value;
}

function formatLastSeen(lastSeen?: string | number) {
  if (!lastSeen) return null;
  const ms =
    typeof lastSeen === "number"
      ? lastSeen
      : Number.isNaN(Date.parse(lastSeen))
        ? 0
        : Date.parse(lastSeen);
  if (!ms) return null;
  return new Date(ms).toLocaleString();
}

function machineStatusLine(opts: {
  platform?: string;
  online?: boolean;
  lastSeen?: string | number;
  host?: string;
  deviceClass?: string;
}) {
  const bits = [formatPlatform(opts.platform)];
  if (opts.deviceClass) bits.push(opts.deviceClass);
  if (opts.online) bits.push("online");
  else {
    const seen = formatLastSeen(opts.lastSeen);
    if (seen) bits.push(`seen ${seen}`);
  }
  if (opts.host) bits.push(opts.host);
  return bits.join(" · ");
}

function machineScopeLabel(ids?: string[], machines?: GuestMachineSummary[]) {
  const names = (machines || [])
    .map((machine) => String(machine.name || machine.deviceId || "").trim())
    .filter(Boolean);
  if (names.length > 0) {
    const visible = names.slice(0, 3).join(", ");
    const extra = names.length > 3 ? ` +${names.length - 3} more` : "";
    return `${visible}${extra}`;
  }
  const count = ids?.length || 0;
  if (count > 0) return `${count} machine${count === 1 ? "" : "s"}`;
  return "";
}

function tunnelUrlsForDevice(device: { publicEndpoints?: string[]; tunnelUrl?: string }) {
  return Array.from(
    new Set(
      [
        ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
        ...(device.tunnelUrl ? [device.tunnelUrl] : []),
      ]
        .map((value) => String(value || "").trim())
        .filter(Boolean),
    ),
  );
}

async function fetchProjectsFromDevice(device: {
  id: string;
  name: string;
  host: string;
  port: number;
  publicEndpoints?: string[];
  tunnelUrl?: string;
}, token: string): Promise<string[]> {
  const client = new AgentClient();
  client.setRelayServers(
    agentClient.configuredRelayServers.map((relay) => ({ ...relay })),
  );
  try {
    await client.connect(device.host, device.port, token, device.id, {
      tunnelUrls: tunnelUrlsForDevice(device),
    });
    const [projects, workspaceApps] = await Promise.all([
      client.listProjects().catch(() => []),
      client.getWorkspaceApps().catch(() => []),
    ]);
    return Array.from(
      new Set(
        [
          ...projects.map((project) => String(project?.name || "").trim()),
          ...workspaceApps.map((app) => String(app?.name || "").trim()),
        ].filter(Boolean),
      ),
    ).sort((a, b) => a.localeCompare(b));
  } finally {
    client.disconnect();
  }
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
  const [inviteProjectChoices, setInviteProjectChoices] = useState<string[]>([]);
  const [inviteProjectsLoading, setInviteProjectsLoading] = useState(false);
  const [inviteProjectsError, setInviteProjectsError] = useState<string | null>(null);
  const [inviteProjectsSource, setInviteProjectsSource] = useState<string | null>(null);
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
      setErr(friendlyGuestError(e));
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
  const inviteSelectedDevices = ownDevices.filter((d) => inviteDeviceIds.includes(d.id));
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

  async function loadInviteProjects() {
    if (!token || inviteSelectedDevices.length === 0) return;
    setInviteProjectsLoading(true);
    setInviteProjectsError(null);
    setInviteProjectChoices([]);
    setInviteProjects([]);
    setInviteProjectsSource(null);
    try {
      const settled = await Promise.allSettled(
        inviteSelectedDevices.map((device) => fetchProjectsFromDevice(device, token)),
      );
      const merged = new Set<string>();
      let successCount = 0;
      let failureCount = 0;
      for (const result of settled) {
        if (result.status === "fulfilled") {
          successCount += 1;
          for (const project of result.value) merged.add(project);
        } else {
          failureCount += 1;
        }
      }
      const choices = [...merged].sort((a, b) => a.localeCompare(b));
      setInviteProjectChoices(choices);
      setInviteProjectsSource(inviteSelectedDevices.map((device) => device.name).join(", "));
      if (choices.length === 0) {
        setInviteProjectsError("No projects were detected on the selected machine(s).");
      } else if (failureCount > 0 && successCount > 0) {
        setInviteProjectsError("Loaded projects from some selected machines, but at least one machine did not respond.");
      } else if (failureCount > 0) {
        setInviteProjectsError("Could not load projects from the selected machine(s).");
      }
    } catch (e) {
      setInviteProjectsError(e instanceof Error ? e.message : String(e));
    } finally {
      setInviteProjectsLoading(false);
    }
  }

  useEffect(() => {
    setInviteProjects([]);
    setInviteProjectChoices([]);
    setInviteProjectsError(null);
    setInviteProjectsSource(null);
}, [inviteDeviceIds.join("|")]);

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
      setInviteProjectChoices([]);
      setInviteProjectsError(null);
      setInviteProjectsSource(null);
      await load();
    } catch (e) {
      setErr(friendlyGuestError(e));
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
      setErr(friendlyGuestError(e));
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
      setErr(friendlyGuestError(e));
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
      setErr(friendlyGuestError(e));
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
      setErr(friendlyGuestError(e));
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

      <section className="space-y-4">
        {user?.id ? (
          <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-4">
            {user.email ? (
              <>
                <div className="text-[10px] font-bold uppercase tracking-wider text-surface-500">Your email</div>
                <div className="mt-1 text-sm font-medium text-surface-100">{user.email}</div>
                <div className="mt-3 text-[10px] font-bold uppercase tracking-wider text-surface-500">Your user ID</div>
              </>
            ) : (
              <div className="text-[10px] font-bold uppercase tracking-wider text-surface-500">Your user ID</div>
            )}
            <div className="mt-1 break-all font-mono text-xs text-surface-300">{user.id}</div>
            <div className="mt-2 text-xs text-surface-500">
              {user.email
                ? "Share either your email or user ID. The user ID lets people invite you without knowing your email."
                : "People can invite you without knowing your email."}
            </div>
            <div className="mt-3 flex flex-wrap gap-2">
              {user.email ? (
                <button
                  onClick={() => copy(user.email)}
                  className="border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-700 dark:text-indigo-300"
                >
                  Copy email
                </button>
              ) : null}
              <button
                onClick={() => copy(user.id)}
                className="border border-surface-700 bg-surface-800/50 px-3 py-2 text-xs font-semibold text-surface-300"
              >
                Copy user ID
              </button>
            </div>
          </div>
        ) : null}

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
            <div className="text-xs text-emerald-700 dark:text-emerald-300">
              {inviteLookup.fullName} · {inviteLookup.email}
            </div>
          )}
          {inviteKind === "user-id" && inviteLookupErr && (
            <div className="text-xs text-red-700 dark:text-red-300">{inviteLookupErr}</div>
          )}

          <div className="space-y-2">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Access scope</div>
            <div className="flex flex-wrap gap-2">
              <ScopeButton active={inviteScope === "feedback-only"} onClick={() => setInviteScope("feedback-only")} label="Feedback Only" />
              <ScopeButton active={inviteScope === "sdk-project"} onClick={() => setInviteScope("sdk-project")} label="SDK Project" />
              <ScopeButton active={inviteScope === "full"} onClick={() => setInviteScope("full")} label="Full" />
            </div>
          </div>

          {ownDevices.length > 0 && (
            <div className="space-y-2">
              <div>
                <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Machine slice</div>
                <div className="mt-1 text-xs text-surface-500">
                  Pick which machines to offer. Leave all unselected to offer every machine. Load projects below only if you want to narrow access further.
                </div>
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                {ownDevices.map((device) => {
                  const selected = inviteDeviceIds.includes(device.id);
                  return (
                  <button
                    key={device.id}
                    type="button"
                    onClick={() => toggleInviteDevice(device.id)}
                    className={`relative rounded-md border px-3 py-2 text-left text-xs transition-colors ${
                      selected
                        ? "border-brand/40 bg-brand-soft text-surface-100"
                        : device.online
                          ? "border-success/25 bg-surface-950 text-surface-300 hover:border-success/40"
                          : "border-surface-700 bg-surface-950 text-surface-400 hover:border-surface-600"
                    }`}
                  >
                    {device.online && !selected ? (
                      <span aria-hidden className="absolute left-0 top-0 h-full w-0.5 rounded-l-md bg-success/60" />
                    ) : null}
                    <div className="flex items-center justify-between gap-2">
                      <div className="font-semibold text-surface-200">{device.name}</div>
                      <span
                        className={`inline-flex h-2.5 w-2.5 rounded-full ${
                          device.online ? "bg-success animate-live-pulse" : "bg-surface-600"
                        }`}
                      />
                    </div>
                    <div className="mt-1 text-surface-400">
                      {machineStatusLine({
                        platform: device.platform,
                        online: device.online,
                        lastSeen: device.lastSeen,
                        host: device.host,
                        deviceClass: device.deviceClass,
                      })}
                    </div>
                  </button>
                  );
                })}
              </div>
            </div>
          )}

          {inviteSelectedDevices.length > 0 && (
            <div className="space-y-2">
              <div>
                <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Project slice</div>
                <div className="mt-1 text-xs text-surface-500">
                  Optional. Load a machine&apos;s repo list if this invite should only see specific projects.
                </div>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-surface-800 bg-surface-950/60 p-3">
                <button
                  type="button"
                  onClick={() => void loadInviteProjects()}
                  disabled={inviteProjectsLoading}
                  className="border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-700 dark:text-indigo-300 disabled:opacity-40"
                >
                  {inviteProjectsLoading
                    ? "Loading projects…"
                    : `Load repos from selected machine${inviteSelectedDevices.length === 1 ? "" : "s"}`}
                </button>
                {inviteProjectsSource ? (
                  <div className="rounded-full border border-surface-700 px-2.5 py-1 text-xs text-surface-400">
                    Source: {inviteProjectsSource}
                  </div>
                ) : null}
              </div>
              {inviteProjectsError ? (
                <div className={`text-xs ${inviteProjectChoices.length > 0 ? "text-amber-700 dark:text-amber-300" : "text-red-700 dark:text-red-300"}`}>
                  {inviteProjectsError}
                </div>
              ) : (
                <div className="text-xs text-surface-500">
                  Selected repos are saved into the invite and enforced for this guest.
                </div>
              )}
              {inviteProjectChoices.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {inviteProjectChoices.map((project) => (
                    <button
                      key={project}
                      type="button"
                      onClick={() => toggleProject(project)}
                      className={`border px-2 py-1 text-xs ${
                        inviteProjects.includes(project)
                          ? "border-indigo-500 bg-indigo-500/15 text-indigo-700 dark:text-indigo-200"
                          : "border-surface-700 bg-surface-950 text-surface-400"
                      }`}
                    >
                      {project}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          )}

          <div className="space-y-3">
            <div className="text-xs text-surface-500">
              Invite codes expire in 2 days and work even if the guest signs in with a different OAuth email.
            </div>
            <button
              type="button"
              onClick={() => void handleInvite()}
              disabled={busy === "invite" || !inviteTarget.trim()}
              className="w-full whitespace-nowrap rounded-md bg-brand text-brand-fg hover:bg-brand/90 active:scale-[0.99] px-4 py-1.5 text-sm font-semibold transition-all disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {busy === "invite" ? "Sending…" : "Send Invite"}
            </button>
          </div>
        </div>

        {lastInvite && (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4">
            <div className="text-[10px] uppercase tracking-wider text-emerald-700 dark:text-emerald-300">Latest invite</div>
            <div className="mt-1 text-sm text-surface-200">{lastInvite.target}</div>
            <div className="mt-1 text-xs text-surface-500">scope: {lastInvite.scope}</div>
            <div className="mt-3 font-mono text-3xl font-semibold tracking-[0.3em] text-surface-50">{lastInvite.code}</div>
            <div className="mt-3 flex gap-2">
              <button onClick={() => copy(lastInvite.code)} className="border border-surface-700 bg-surface-950 px-3 py-2 text-xs text-surface-200">
                Copy Code
              </button>
              <button
                onClick={() => copy(`Your Yaver invite code: ${lastInvite.code}`)}
                className="border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs text-indigo-700 dark:text-indigo-300"
              >
                Copy Message
              </button>
            </div>
          </div>
        )}
      </section>

      <section className="rounded-lg border border-surface-800 bg-surface-900/40 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-surface-100">Join with invite code</h3>
            <p className="mt-1 text-xs text-surface-500">
              Use this when the host invited a different email than the one you signed in with.
            </p>
          </div>
          <div className="text-[11px] text-surface-600">Out-of-band code or pending invite</div>
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
            className="border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-700 dark:text-indigo-300 disabled:opacity-40"
          >
            {busy === "preview" ? "Checking…" : "Preview"}
          </button>
        </div>
        {joinPreviewErr && <div className="mt-3 text-sm text-red-700 dark:text-red-300">{joinPreviewErr}</div>}
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
                  <div className="flex items-center justify-between gap-2">
                    <div className="font-semibold text-surface-200">{device.name}</div>
                    <span
                      className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] font-semibold ${
                        device.proposed
                          ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-700 dark:text-indigo-300"
                          : "border-surface-700 bg-surface-900 text-surface-500"
                      }`}
                    >
                      {device.proposed ? "proposed" : "optional"}
                    </span>
                  </div>
                  <div className="mt-1 text-surface-400">
                    {formatPlatform(device.platform)}
                  </div>
                  {formatLastSeen(device.lastHeartbeat) ? (
                    <div className="mt-1 text-surface-500">
                      seen {formatLastSeen(device.lastHeartbeat)}
                    </div>
                  ) : null}
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

      {err && <div className="rounded border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-200">{err}</div>}
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
                        <span className="rounded bg-indigo-500/10 border border-indigo-500/40 px-2 py-0.5 text-[10px] font-semibold text-indigo-700 dark:text-indigo-300">
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
                      {machineScopeLabel(g.proposedDeviceIds, g.proposedDevices)
                        ? ` · scoped to ${machineScopeLabel(g.proposedDeviceIds, g.proposedDevices)}`
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
                    className="rounded border border-red-500/30 bg-red-500/10 px-2 py-1 text-[11px] text-red-700 dark:text-red-200 disabled:opacity-40"
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
                                ? "border-indigo-500 bg-indigo-500/15 text-indigo-700 dark:text-indigo-200"
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
                                    ? "border-indigo-500 bg-indigo-500/15 text-indigo-700 dark:text-indigo-200"
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
                      ? ` · scope: ${machineScopeLabel(h.proposedDeviceIds, h.proposedDevices)}`
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
                    {machineScopeLabel(undefined, h.devices)
                      ? ` · scope: ${machineScopeLabel(undefined, h.devices)}`
                      : ""}
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
