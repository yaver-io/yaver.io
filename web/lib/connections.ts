import { CONVEX_URL } from "@/lib/constants";

// Client for the social graph (the address book). Mirrors lib/guests.ts.

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

export interface ConnectionSearchResult {
  userId?: string;
  fullName?: string;
  email?: string;
  connectionStatus?: "none" | "pending" | "accepted" | "blocked";
  direction?: "incoming" | "outgoing";
  self?: boolean;
}

export interface SuggestedConnection {
  userId: string;
  fullName: string;
  email: string;
  source: string;
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

export async function listConnections(token: string): Promise<ConnectionsResponse> {
  const res = await fetch(`${CONVEX_URL}/connections/list`, { headers: authHeaders(token) });
  if (!res.ok) throw new Error(await parseError(res, "Failed to load connections"));
  return res.json();
}

export async function requestConnection(
  token: string,
  target: { peerUserId?: string; peerEmail?: string; nickname?: string; source?: string },
): Promise<{ status: string }> {
  const res = await fetch(`${CONVEX_URL}/connections/request`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify(target),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to send request"));
  return res.json();
}

export async function acceptConnection(token: string, peerUserId: string): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/connections/accept`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ peerUserId }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to accept"));
}

export async function removeConnection(token: string, peerUserId: string): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/connections/remove`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ peerUserId }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to remove"));
}

export async function blockConnection(token: string, peerUserId: string): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/connections/block`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ peerUserId }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to block"));
}

export async function setConnectionNickname(token: string, peerUserId: string, nickname: string): Promise<void> {
  const res = await fetch(`${CONVEX_URL}/connections/nickname`, {
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ peerUserId, nickname }),
  });
  if (!res.ok) throw new Error(await parseError(res, "Failed to set nickname"));
}

export async function searchConnection(token: string, query: string): Promise<ConnectionSearchResult | null> {
  const res = await fetch(`${CONVEX_URL}/connections/search?query=${encodeURIComponent(query.trim())}`, {
    headers: authHeaders(token),
  });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(await parseError(res, "Search failed"));
  return res.json();
}

export async function suggestedConnections(token: string): Promise<SuggestedConnection[]> {
  const res = await fetch(`${CONVEX_URL}/connections/suggested`, { headers: authHeaders(token) });
  if (!res.ok) throw new Error(await parseError(res, "Failed to load suggestions"));
  const data = await res.json();
  return data.suggestions || [];
}
