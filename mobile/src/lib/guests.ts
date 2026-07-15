/**
 * Guest access API — invitation and acceptance flows via Convex.
 *
 * Host: invites guests via CLI/MCP/mobile → guest sees invitation → accepts → can connect
 * Guest: sees invitations in mobile app → accepts → host devices appear in device list
 */

import { getConvexSiteUrl } from "./auth";

// ── Types ────────────────────────────────────────────────────────────

export interface GuestInvitation {
  /** Convex row id — present on records fetched from the backend,
   *  absent on invitations constructed client-side. */
  _id?: string;
  inviteId?: string;
  inviteCode?: string;
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  hostUserIdString?: string;
  createdAt: number;
  expiresAt: number;
  invitedByUserId?: boolean;
  /** Device ids the host pre-scoped; empty / undefined means "all host devices". */
  proposedDeviceIds?: string[];
  proposedDevices?: GuestMachineSummary[];
}

export interface InvitationHostDevice {
  deviceId: string;
  name: string;
  platform: string;
  lastHeartbeat?: number;
  proposed: boolean;
}

export interface InvitationPreview {
  inviteCode: string;
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  hostUserIdString?: string;
  proposedDeviceIds?: string[];
  hostDevices: InvitationHostDevice[];
  invitedByUserId?: boolean;
  expiresAt: number;
  createdAt: number;
}

export interface PublicUserLookup {
  userId: string;
  fullName: string;
  email: string;
}

export interface ActiveHost {
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  grantedAt: number;
  devices?: GuestMachineSummary[];
}

export interface GuestHostsResponse {
  pending: GuestInvitation[];
  active: ActiveHost[];
}

export interface GuestInfo {
  email: string;
  status: "pending" | "accepted" | "revoked" | "expired";
  fullName?: string;
  /** Public user id (stable across providers). Present when the guest is already
   *  a Yaver user (invited by email that matched an existing account, or invited
   *  directly by user id). */
  userId?: string;
  createdAt: number;
  expiresAt?: number;
  acceptedAt?: number;
  revokedAt?: number;
  /** Set when status === "accepted". Epoch ms of when the host granted access. */
  grantedAt?: number;
  /** Set when status === "pending". 6-char uppercase alphanumeric. */
  inviteCode?: string;
  /** True when the host targeted this invite by userId instead of email. */
  invitedByUserId?: boolean;
  /** Host-proposed device scope (empty/undefined means "all host devices"). */
  proposedDeviceIds?: string[];
  proposedDevices?: GuestMachineSummary[];
}

export interface GuestMachineSummary {
  deviceId: string;
  name: string;
  platform: string;
  lastHeartbeat?: number;
}

export interface GuestConfigEntry {
  guestUserId: string;
  guestEmail: string;
  guestName: string;
  scope?: "full" | "feedback-only" | "sdk-project";
  dailyTokenLimit?: number;
  allowedRunners?: string[];
  usageMode?: string; // "always" | "idle-only" | "scheduled"
  schedule?: {
    startHour: number;
    endHour: number;
    timezone?: string;
  };
  shareAllDevices?: boolean;
  deviceIds?: string[];
  shareAllMachines?: boolean;
  machineIds?: string[];
  resourcePreset?: string;
  useHostApiKeys?: boolean;
  allowGuestProvidedApiKeys?: boolean;
  allowDesktopControl?: boolean;
  allowBrowserControl?: boolean;
  allowTunnelForward?: boolean;
  requireIsolation?: boolean;
  cpuLimitPercent?: number;
  ramLimitMb?: number;
  priorityMode?: string;
  allowedProjects?: string[];
  allowedSharedStorage?: string[];
}

export interface GuestUsageEntry {
  guestEmail: string;
  guestName: string;
  date: string;
  secondsUsed: number;
}

export interface GuestConversionSource {
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  sourceScope: "full" | "feedback-only" | "sdk-project" | "support";
  sourceProjects: string[];
  firstAcceptedAt: number;
  lastGuestActivityAt?: number;
  guestActivityCount: number;
  conversionState: "guest-active" | "service-enabled" | "paid-usage";
  firstManagedService?: string;
  enabledServices: string[];
}

export interface GuestRecommendedService {
  service: string;
  label: string;
  reason: string;
}

export interface GuestConversionSurface {
  sources: GuestConversionSource[];
  hasGuestOrigin: boolean;
  enabledServices: Record<string, boolean>;
  recommendedServices: GuestRecommendedService[];
}

export interface HostGuestConversion {
  guestUserId: string;
  guestEmail: string;
  guestName: string;
  sourceScope: "full" | "feedback-only" | "sdk-project" | "support";
  sourceProjects: string[];
  firstAcceptedAt: number;
  lastGuestActivityAt?: number;
  guestActivityCount: number;
  conversionState: "guest-active" | "service-enabled" | "paid-usage";
  firstManagedServiceAt?: number;
  firstManagedService?: string;
  enabledServices: string[];
  convertedAt?: number;
}

export interface HostConversionSummary {
  guests: HostGuestConversion[];
  totals: {
    invited: number;
    serviceEnabled: number;
    paidUsage: number;
  };
}

export interface GuestConfigUpdate {
  email: string;
  scope?: "full" | "feedback-only" | "sdk-project";
  dailyTokenLimit?: number;
  allowedRunners?: string[];
  usageMode?: string;
  schedule?: {
    startHour: number;
    endHour: number;
    timezone?: string;
  };
  shareAllDevices?: boolean;
  deviceIds?: string[];
  shareAllMachines?: boolean;
  machineIds?: string[];
  resourcePreset?: string;
  useHostApiKeys?: boolean;
  allowGuestProvidedApiKeys?: boolean;
  allowDesktopControl?: boolean;
  allowBrowserControl?: boolean;
  allowTunnelForward?: boolean;
  requireIsolation?: boolean;
  cpuLimitPercent?: number;
  ramLimitMb?: number;
  priorityMode?: string;
  allowedProjects?: string[];
  allowedSharedStorage?: string[];
}

// ── API ──────────────────────────────────────────────────────────────

/**
 * Fetch pending invitations and active host access for the current user (guest perspective).
 */
export async function fetchGuestHosts(token: string): Promise<GuestHostsResponse> {
  const res = await fetch(`${getConvexSiteUrl()}/guests/hosts`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error("Failed to fetch guest hosts");
  return res.json();
}

/**
 * Accept a pending guest invitation. Optional approvedDeviceIds narrows scope.
 */
export async function acceptGuestInvitation(
  token: string,
  hostUserId: string,
  approvedDeviceIds?: string[]
): Promise<void> {
  const res = await fetch(`${getConvexSiteUrl()}/guests/accept`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ hostUserId, approvedDeviceIds }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to accept invitation");
  }
}

/**
 * Accept a guest invitation using a 6-character invite code.
 * Works regardless of the guest's email — the code is the proof.
 * Use this when the guest signed up with a different OAuth email than the one invited.
 * Optional approvedDeviceIds narrows scope.
 */
export async function acceptGuestByCode(
  token: string,
  code: string,
  approvedDeviceIds?: string[]
): Promise<{ hostName: string; hostEmail: string }> {
  const res = await fetch(`${getConvexSiteUrl()}/guests/accept-code`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ code, approvedDeviceIds }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Invalid invite code");
  }
  return res.json();
}

/**
 * Invite a guest. Either email or userId must be supplied.
 * Optional proposedDeviceIds pre-scopes the invitation to a subset of host
 * devices (the guest can trim further on accept).
 */
export async function inviteGuest(
  token: string,
  target:
    | {
        email?: string;
        userId?: string;
        deviceIds?: string[];
        scope?: "full" | "feedback-only" | "sdk-project";
        allowedProjects?: string[];
        // Opt a tester (scope="sdk-project") into the AI-improve surface.
        // Ignored by the server for any other scope.
        canVibe?: boolean;
      }
    | string
): Promise<{ inviteCode: string; guestRegistered: boolean; guestUserId?: string; guestEmail?: string }> {
  const body: Record<string, unknown> =
    typeof target === "string"
      ? { email: target }
      : {
          email: target.email,
          userId: target.userId,
          deviceIds: target.deviceIds,
          scope: target.scope,
          allowedProjects: target.allowedProjects,
          canVibe: target.canVibe,
        };
  const res = await fetch(`${getConvexSiteUrl()}/guests/invite`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to invite guest");
  }
  return res.json();
}

/**
 * Preview an invitation by code before accepting — returns host + device scope.
 */
export async function findInviteByCode(
  token: string,
  code: string
): Promise<InvitationPreview> {
  const res = await fetch(
    `${getConvexSiteUrl()}/guests/find-by-code?code=${encodeURIComponent(code.toUpperCase().trim())}`,
    { headers: { Authorization: `Bearer ${token}` } }
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Invite not found");
  }
  return res.json();
}

/**
 * Resolve a public user id (what the user sees in their Settings tab) to a
 * basic profile so the host UI can confirm the target before inviting.
 */
export async function lookupPublicUser(
  token: string,
  userId: string
): Promise<PublicUserLookup | null> {
  const res = await fetch(
    `${getConvexSiteUrl()}/users/lookup?userId=${encodeURIComponent(userId.trim())}`,
    { headers: { Authorization: `Bearer ${token}` } }
  );
  if (res.status === 404) return null;
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Lookup failed");
  }
  return res.json();
}

/**
 * Revoke guest access (host perspective). Accepts email or public userId.
 * Pass a string (treated as email, for back-compat) or an object.
 */
export async function revokeGuest(
  token: string,
  target: string | { email?: string; userId?: string }
): Promise<void> {
  const body: Record<string, unknown> =
    typeof target === "string" ? { email: target } : { email: target.email, userId: target.userId };
  const res = await fetch(`${getConvexSiteUrl()}/guests/revoke`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to revoke guest");
  }
}

/**
 * List all guests (host perspective).
 */
export async function listGuests(token: string): Promise<GuestInfo[]> {
  const res = await fetch(`${getConvexSiteUrl()}/guests/list`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error("Failed to fetch guest list");
  const data = await res.json();
  return data.guests || [];
}

/**
 * Guest-facing funnel state: which developer/shared runtime introduced
 * this account, and which self-owned Yaver capability to offer next.
 */
export async function fetchGuestConversionSurface(token: string): Promise<GuestConversionSurface> {
  const res = await fetch(`${getConvexSiteUrl()}/guests/conversion?role=guest`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to fetch guest conversion state");
  }
  return res.json();
}

/**
 * Host-facing referral/conversion summary for invited guests.
 */
export async function fetchHostConversionSummary(token: string): Promise<HostConversionSummary> {
  const res = await fetch(`${getConvexSiteUrl()}/guests/conversion?role=host`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to fetch host conversion summary");
  }
  return res.json();
}

// ── Guest Config API (via agent P2P) ────────────────────────────────

/**
 * Fetch guest configs from the agent (includes Convex config + local project access).
 */
export async function fetchGuestConfigs(
  agentUrl: string,
  token: string,
  email?: string
): Promise<GuestConfigEntry[]> {
  const url = email
    ? `${agentUrl}/guests/config?email=${encodeURIComponent(email)}`
    : `${agentUrl}/guests/config`;
  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error("Failed to fetch guest configs");
  const data = await res.json();
  return data.configs || [];
}

/**
 * Update guest config via the agent (Convex fields + local project access).
 */
export async function updateGuestConfig(
  agentUrl: string,
  token: string,
  config: GuestConfigUpdate
): Promise<void> {
  const res = await fetch(`${agentUrl}/guests/config`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(config),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to update guest config");
  }
}

/**
 * Fetch guest usage stats from the agent.
 */
export async function fetchGuestUsage(
  agentUrl: string,
  token: string,
  date?: string
): Promise<GuestUsageEntry[]> {
  const url = date
    ? `${agentUrl}/guests/usage?date=${encodeURIComponent(date)}`
    : `${agentUrl}/guests/usage`;
  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error("Failed to fetch guest usage");
  const data = await res.json();
  return data.usage || [];
}
