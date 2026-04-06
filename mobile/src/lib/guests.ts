/**
 * Guest access API — invitation and acceptance flows via Convex.
 *
 * Host: invites guests via CLI/MCP/mobile → guest sees invitation → accepts → can connect
 * Guest: sees invitations in mobile app → accepts → host devices appear in device list
 */

import { CONVEX_SITE_URL } from "./constants";

// ── Types ────────────────────────────────────────────────────────────

export interface GuestInvitation {
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
