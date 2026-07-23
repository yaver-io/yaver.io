import { CONVEX_URL } from "@/lib/constants";

export interface GuestInvitation {
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
  inviteId?: string;
  email: string;
  status: "pending" | "accepted" | "revoked" | "expired";
  fullName?: string;
  userId?: string;
  createdAt: number;
  expiresAt?: number;
  acceptedAt?: number;
  revokedAt?: number;
  grantedAt?: number;
  inviteCode?: string;
  invitedByUserId?: boolean;
  proposedDeviceIds?: string[];
  proposedDevices?: GuestMachineSummary[];
}

export interface GuestMachineSummary {
  deviceId: string;
  name: string;
  platform: string;
  lastHeartbeat?: number;
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

async function parseError(res: Response, fallback: string) {
  const data = await res.json().catch(() => ({}));
  return data?.error || fallback;
}

export async function fetchGuestHosts(token: string): Promise<GuestHostsResponse> {
  const res = await fetch(`${CONVEX_URL}/guests/hosts`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to fetch guest hosts"));
  return res.json();
}

export async function acceptGuestInvitation(
  token: string,
  hostUserId: string,
  approvedDeviceIds?: string[],
): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/guests/accept`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ hostUserId, approvedDeviceIds }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to accept invitation"));
}

export async function acceptGuestByCode(
  token: string,
  code: string,
  approvedDeviceIds?: string[],
): Promise<{ hostName: string; hostEmail: string }> {
  const res = await fetch(`${CONVEX_URL}/guests/accept-code`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ code, approvedDeviceIds }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Invalid invite code"));
  return res.json();
}

export async function inviteGuest(
  token: string,
  target:
    | string
    | {
        email?: string;
        userId?: string;
        deviceIds?: string[];
        scope?: "full" | "feedback-only" | "sdk-project";
        allowedProjects?: string[];
      },
): Promise<{ inviteCode: string; guestRegistered: boolean; guestUserId?: string; guestEmail?: string; scope?: string }> {
  const body =
    typeof target === "string"
      ? { email: target }
      : {
          email: target.email,
          userId: target.userId,
          deviceIds: target.deviceIds,
          scope: target.scope,
          allowedProjects: target.allowedProjects,
        };
  const res = await fetch(`${CONVEX_URL}/guests/invite`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to invite guest"));
  return res.json();
}

export async function findInviteByCode(token: string, code: string): Promise<InvitationPreview> {
  const res = await fetch(
    `${CONVEX_URL}/guests/find-by-code?code=${encodeURIComponent(code.toUpperCase().trim())}`,
    { headers: { Authorization: `Bearer ${token}` } },
  );
  if (!res.ok) throw new Error(await parseError(res, "Invite not found"));
  return res.json();
}

export async function lookupPublicUser(token: string, userId: string): Promise<PublicUserLookup | null> {
  const res = await fetch(
    `${CONVEX_URL}/users/lookup?userId=${encodeURIComponent(userId.trim())}`,
    { headers: { Authorization: `Bearer ${token}` } },
  );
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(await parseError(res, "Lookup failed"));
  return res.json();
}

export async function revokeGuest(
  token: string,
  target: string | { email?: string; userId?: string },
): Promise<void> {
  const body = typeof target === "string" ? { email: target } : target;
  const res = await fetch(`${CONVEX_URL}/guests/revoke`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to revoke guest"));
}

export async function deleteGuest(
  token: string,
  target: { inviteId?: string; email?: string; userId?: string },
): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/guests/delete`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(target),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to delete guest"));
}

/**
 * Guest-side counterpart to revokeGuest: drop MY OWN access to a host's
 * shared infra. The session token identifies the guest, so this can only
 * ever remove the caller's own access.
 *
 * Not a block — the host can invite again and the guest can accept again.
 */
export async function leaveSharedAccess(
  token: string,
  host: { hostUserId?: string; hostEmail?: string },
): Promise<{ ok: boolean; alreadyGone?: boolean; hostName?: string }> {
  const res = await fetch(`${CONVEX_URL}/guests/leave`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(host),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to remove your access"));
  return res.json();
}

export async function listGuests(token: string): Promise<GuestInfo[]> {
  const res = await fetch(`${CONVEX_URL}/guests/list`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to fetch guest list"));
  const data = await res.json();
  return data.guests || [];
}

export async function fetchGuestConversionSurface(token: string): Promise<GuestConversionSurface> {
  const res = await fetch(`${CONVEX_URL}/guests/conversion?role=guest`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to fetch guest conversion state"));
  return res.json();
}

export async function fetchHostConversionSummary(token: string): Promise<HostConversionSummary> {
  const res = await fetch(`${CONVEX_URL}/guests/conversion?role=host`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to fetch host conversion summary"));
  return res.json();
}
