/**
 * Guest access API — invitation and acceptance flows via Convex.
 *
 * Host: invites guests via CLI/MCP/mobile → guest sees invitation → accepts → can connect
 * Guest: sees invitations in mobile app → accepts → host devices appear in device list
 */

import { CONVEX_SITE_URL } from "./constants";

// ── Types ────────────────────────────────────────────────────────────

export interface GuestInvitation {
  /** Convex row id — present on records fetched from the backend,
   *  absent on invitations constructed client-side. */
  _id?: string;
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  createdAt: number;
  expiresAt: number;
}

export interface ActiveHost {
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  grantedAt: number;
}

export interface GuestHostsResponse {
  pending: GuestInvitation[];
  active: ActiveHost[];
}

export interface GuestInfo {
  email: string;
  status: "pending" | "accepted" | "revoked" | "expired";
  fullName?: string;
  createdAt: number;
  expiresAt?: number;
  acceptedAt?: number;
  revokedAt?: number;
  /** Set when status === "accepted". Epoch ms of when the host granted access. */
  grantedAt?: number;
  /** Set when status === "pending". 6-char uppercase alphanumeric. */
  inviteCode?: string;
}

export interface GuestConfigEntry {
  guestUserId: string;
  guestEmail: string;
  guestName: string;
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

export interface GuestConfigUpdate {
  email: string;
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
  const res = await fetch(`${CONVEX_SITE_URL}/guests/hosts`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error("Failed to fetch guest hosts");
  return res.json();
}

/**
 * Accept a pending guest invitation.
 */
export async function acceptGuestInvitation(
  token: string,
  hostUserId: string
): Promise<void> {
  const res = await fetch(`${CONVEX_SITE_URL}/guests/accept`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ hostUserId }),
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
 */
export async function acceptGuestByCode(
  token: string,
  code: string
): Promise<{ hostName: string; hostEmail: string }> {
  const res = await fetch(`${CONVEX_SITE_URL}/guests/accept-code`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ code }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Invalid invite code");
  }
  return res.json();
}

/**
 * Invite a guest by email (host perspective — can be called from mobile too).
 * Returns the invite code and whether the email is already registered.
 */
export async function inviteGuest(
  token: string,
  email: string
): Promise<{ inviteCode: string; guestRegistered: boolean }> {
  const res = await fetch(`${CONVEX_SITE_URL}/guests/invite`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ email }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "Failed to invite guest");
  }
  return res.json();
}

/**
 * Revoke guest access (host perspective).
 */
export async function revokeGuest(
  token: string,
  email: string
): Promise<void> {
  const res = await fetch(`${CONVEX_SITE_URL}/guests/revoke`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ email }),
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
  const res = await fetch(`${CONVEX_SITE_URL}/guests/list`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error("Failed to fetch guest list");
  const data = await res.json();
  return data.guests || [];
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
