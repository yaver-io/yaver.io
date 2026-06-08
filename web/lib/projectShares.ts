import { CONVEX_URL } from "@/lib/constants";

// Client for shared projects ("ask him to join my project"). Mirrors lib/guests.ts.

export type ProjectRole = "owner" | "dev" | "normie" | "viewer";

export interface ProjectMember {
  userId: string;
  fullName: string;
  email: string;
  role: ProjectRole;
  branch?: string;
  status: "invited" | "active" | "revoked";
  invitedAt: number;
  acceptedAt?: number;
}

export interface OwnedProjectShare {
  shareId: string;
  slug: string;
  repoUrl: string;
  defaultBranch?: string;
  hostKind: "owner-device" | "managed-cloud";
  hostDeviceId?: string;
  payer?: "owner" | "invitee";
  shareCode: string;
  createdAt: number;
  roster: ProjectMember[];
}

export interface JoinedProjectShare {
  shareId: string;
  slug: string;
  repoUrl: string;
  defaultBranch?: string;
  hostKind: "owner-device" | "managed-cloud";
  hostDeviceId?: string;
  role: ProjectRole;
  branch?: string;
  status: string;
  ownerName: string;
  ownerUserId: string;
}

export interface ProjectSharesResponse {
  owned: OwnedProjectShare[];
  joined: JoinedProjectShare[];
}

export interface ProjectPreview {
  shareId: string;
  slug: string;
  repoUrl: string;
  defaultBranch?: string;
  hostKind: "owner-device" | "managed-cloud";
  ownerName: string;
  ownerUserId: string;
  myRole?: ProjectRole;
  myStatus?: string;
}

async function parseError(res: Response, fallback: string) {
  const data = await res.json().catch(() => ({}));
  return data?.error || fallback;
}

function authHeaders(token: string, json = false) {
  const h: Record<string, string> = { Authorization: `Bearer ${token}` };
  if (json) h["Content-Type"] = "application/json";
  return h;
}

export async function listProjectShares(token: string): Promise<ProjectSharesResponse> {
  const res = await fetch(`${CONVEX_URL}/project-shares/list`, { headers: authHeaders(token) });
  if (!res.ok) throw new Error(await parseError(res, "Failed to load shared projects"));
  return res.json();
}

export async function createProjectShare(
  token: string,
  input: {
    slug: string;
    repoUrl: string;
    defaultBranch?: string;
    hostKind: "owner-device" | "managed-cloud";
    hostDeviceId?: string;
    hostMachineId?: string;
    payer?: "owner" | "invitee";
  },
): Promise<{ shareId: string; shareCode: string }> {
  const res = await fetch(`${CONVEX_URL}/project-shares/create`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify(input),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to create project"));
  return res.json();
}

export async function inviteToProject(
  token: string,
  input: { shareId: string; peerUserId?: string; peerEmail?: string; role?: "dev" | "normie" | "viewer" },
): Promise<{ membershipId: string; shareCode: string; role: string; branch: string }> {
  const res = await fetch(`${CONVEX_URL}/project-shares/invite`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify(input),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to invite"));
  return res.json();
}

export async function acceptProjectShare(token: string, shareCode: string): Promise<any> {
  const res = await fetch(`${CONVEX_URL}/project-shares/accept`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ shareCode: shareCode.toUpperCase().trim() }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to accept"));
  return res.json();
}

export async function findProjectByCode(token: string, code: string): Promise<ProjectPreview | null> {
  const res = await fetch(`${CONVEX_URL}/project-shares/find-by-code?code=${encodeURIComponent(code.toUpperCase().trim())}`, {
    headers: authHeaders(token),
  });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(await parseError(res, "Project not found"));
  return res.json();
}

export async function setProjectMemberRole(
  token: string,
  shareId: string,
  memberUserId: string,
  role: "dev" | "normie" | "viewer",
): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/project-shares/set-role`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ shareId, memberUserId, role }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to set role"));
}

export async function revokeProjectMember(token: string, shareId: string, memberUserId: string): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/project-shares/revoke-member`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ shareId, memberUserId }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to remove member"));
}

export async function archiveProjectShare(token: string, shareId: string): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/project-shares/archive`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ shareId }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to archive"));
}
