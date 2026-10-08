import { sha256Hex } from "./security";

export interface SessionIdentity {
  userId: string;
  email: string;
  fullName: string;
  avatarUrl?: string;
  expiresAt: number;
  deviceId?: string;
  plan: "free" | "relay-pro";
}

export interface DeviceAccess {
  deviceId: string;
  ownerUserId: string;
  role: "owner" | "member" | "guest";
  publicKey: string;
}

export async function sessionForToken(db: D1Database, token: string, now = Date.now()): Promise<SessionIdentity | null> {
  const hash = await sha256Hex(token);
  const row = await db.prepare(`
    SELECT s.user_id AS userId, s.device_id AS deviceId, s.expires_at AS expiresAt,
           u.email, u.full_name AS fullName, u.avatar_url AS avatarUrl,
           COALESCE(CASE WHEN sub.status = 'active' THEN sub.plan END, 'free') AS plan
      FROM sessions s
      JOIN users u ON u.id = s.user_id
 LEFT JOIN subscriptions sub ON sub.user_id = s.user_id
     WHERE s.token_hash = ?1 AND s.expires_at > ?2
  `).bind(hash, now).first<Record<string, string | number | null>>();
  if (!row) return null;
  return {
    userId: String(row.userId),
    email: String(row.email),
    fullName: String(row.fullName || ""),
    avatarUrl: row.avatarUrl ? String(row.avatarUrl) : undefined,
    expiresAt: Number(row.expiresAt),
    deviceId: row.deviceId ? String(row.deviceId) : undefined,
    plan: row.plan === "relay-pro" ? "relay-pro" : "free",
  };
}

export async function accessForDevice(
  db: D1Database,
  userId: string,
  deviceId: string,
  now = Date.now(),
): Promise<DeviceAccess | null> {
  const row = await db.prepare(`
    SELECT d.device_id AS deviceId, d.owner_user_id AS ownerUserId, d.public_key AS publicKey,
           CASE WHEN d.owner_user_id = ?1 THEN 'owner' ELSE a.role END AS role
      FROM devices d
 LEFT JOIN device_access a ON a.device_id = d.device_id AND a.user_id = ?1
     WHERE d.device_id = ?2
       AND (d.owner_user_id = ?1 OR (a.user_id = ?1 AND (a.expires_at IS NULL OR a.expires_at > ?3)))
  `).bind(userId, deviceId, now).first<Record<string, string>>();
  if (!row) return null;
  return {
    deviceId: row.deviceId,
    ownerUserId: row.ownerUserId,
    publicKey: row.publicKey,
    role: row.role as DeviceAccess["role"],
  };
}

export async function ownerDevice(db: D1Database, userId: string, deviceId: string): Promise<DeviceAccess | null> {
  const access = await accessForDevice(db, userId, deviceId);
  return access?.role === "owner" ? access : null;
}
