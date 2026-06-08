// src/lib/connections.ts — social graph client (the address book). Calls the
// Convex HTTP routes with the device's session token. Mirrors web/lib/connections.ts.

import { getConvexSiteUrl, getToken } from "./auth";

export interface Connection {
  peerUserId: string;
  fullName: string;
  email: string;
  nickname?: string;
  source?: string;
  createdAt: number;
  acceptedAt?: number;
}

export interface ConnectionsResponse {
  accepted: Connection[];
  incoming: Connection[];
  outgoing: Connection[];
  blocked: Connection[];
}

export interface SuggestedConnection {
  userId: string;
  fullName: string;
  email: string;
  source: string;
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

export async function listConnections(): Promise<ConnectionsResponse> {
  const res = await authedFetch("/connections/list");
  if (!res.ok) throw new Error(await errOf(res, "Failed to load connections"));
  return res.json();
}

export async function requestConnection(target: {
  peerUserId?: string;
  peerEmail?: string;
  nickname?: string;
  source?: string;
}): Promise<{ status: string }> {
  const res = await authedFetch("/connections/request", { method: "POST", body: JSON.stringify(target) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to send request"));
  return res.json();
}

export async function acceptConnection(peerUserId: string): Promise<void> {
  const res = await authedFetch("/connections/accept", { method: "POST", body: JSON.stringify({ peerUserId }) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to accept"));
}

export async function removeConnection(peerUserId: string): Promise<void> {
  const res = await authedFetch("/connections/remove", { method: "POST", body: JSON.stringify({ peerUserId }) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to remove"));
}

export async function blockConnection(peerUserId: string): Promise<void> {
  const res = await authedFetch("/connections/block", { method: "POST", body: JSON.stringify({ peerUserId }) });
  if (!res.ok) throw new Error(await errOf(res, "Failed to block"));
}

export async function suggestedConnections(): Promise<SuggestedConnection[]> {
  const res = await authedFetch("/connections/suggested");
  if (!res.ok) throw new Error(await errOf(res, "Failed to load suggestions"));
  const data = await res.json();
  return data.suggestions || [];
}
