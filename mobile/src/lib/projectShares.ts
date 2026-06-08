// src/lib/projectShares.ts — shared-projects client ("ask him to join my project").
// Mirrors web/lib/projectShares.ts. Tokens stay on-device; this only carries
// ids / slugs / normalized repo URLs / roles.

import { getConvexSiteUrl, getToken } from "./auth";

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

async function authedFetch(path: string, init?: RequestInit): Promise<Response> {
  const token = await getToken();
  if (!token) throw new Error("Not signed in");
  return fetch(`${getConvexSiteUrl()}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...(init?.headers || {}),
    },
  });
}

async function errOf(res: Response, fallback: string): Promise<string> {
  const data = await res.json().catch(() => ({}));
  return data?.error || fallback;
}

export async function listProjectShares(): Promise<ProjectSharesResponse> {
  const res = await authedFetch("/project-shares/list");
  if (!res.ok) throw new Error(await errOf(res, "Failed to load shared projects"));
  return res.json();
}

export async function createProjectShare(input: {
  slug: string;
  repoUrl: string;
  defaultBranch?: string;
  hostKind: "owner-device" | "managed-cloud";
  hostDeviceId?: string;
  payer?: "owner" | "invitee";
}): Promise<{ shareId: string; shareCode: string }> {
  const res = await authedFetch("/project-shares/create", { method: "POST", body: JSON.stringify(input) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to create project"));
  return res.json();
}

export async function inviteToProject(input: {
  shareId: string;
  peerUserId?: string;
  peerEmail?: string;
  role?: "dev" | "normie" | "viewer";
}): Promise<{ membershipId: string; shareCode: string; role: string; branch: string }> {
  const res = await authedFetch("/project-shares/invite", { method: "POST", body: JSON.stringify(input) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to invite"));
  return res.json();
}

export async function acceptProjectShare(shareCode: string): Promise<any> {
  const res = await authedFetch("/project-shares/accept", {
    method: "POST",
    body: JSON.stringify({ shareCode: shareCode.toUpperCase().trim() }),
  });
  if (!res.ok) throw new Error(await errOf(res, "Failed to accept"));
  return res.json();
}

export async function revokeProjectMember(shareId: string, memberUserId: string): Promise<void> {
  const res = await authedFetch("/project-shares/revoke-member", {
    method: "POST",
    body: JSON.stringify({ shareId, memberUserId }),
  });
  if (!res.ok) throw new Error(await errOf(res, "Failed to remove member"));
}

export async function archiveProjectShare(shareId: string): Promise<void> {
  const res = await authedFetch("/project-shares/archive", { method: "POST", body: JSON.stringify({ shareId }) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to archive"));
}
