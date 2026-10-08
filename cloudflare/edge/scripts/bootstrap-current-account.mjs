#!/usr/bin/env node

import { createHash } from "node:crypto";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

if (process.env.YAVER_EDGE_BOOTSTRAP_CURRENT_ACCOUNT !== "1") {
  console.error("Refusing bootstrap without YAVER_EDGE_BOOTSTRAP_CURRENT_ACCOUNT=1.");
  process.exit(2);
}

const edgeDir = resolve(import.meta.dirname, "..");
const configPath = join(homedir(), ".yaver", "config.json");
const config = JSON.parse(readFileSync(configPath, "utf8"));
const token = String(config.auth_token || "").trim();
const legacyOrigin = String(config.convex_site_url || "").trim().replace(/\/$/, "");
if (!token || !legacyOrigin) throw new Error("Current Yaver auth configuration is incomplete.");

const response = await fetch(`${legacyOrigin}/auth/validate`, {
  headers: { Authorization: `Bearer ${token}` },
  signal: AbortSignal.timeout(12_000),
});
if (!response.ok) throw new Error("Current Yaver session did not validate.");
const payload = await response.json();
const user = payload?.user || {};
if (!user.userId || !user.email) throw new Error("Validated Yaver identity is incomplete.");

const quote = (value) => `'${String(value).replaceAll("'", "''")}'`;
const now = Date.now();
const expiresAt = now + 365 * 86_400_000;
const tokenHash = createHash("sha256").update(token).digest("hex");
const sql = [
  `INSERT INTO users (id,email,full_name,avatar_url,created_at,updated_at) VALUES (${quote(user.userId)},${quote(String(user.email).toLowerCase())},${quote(user.fullName || "")},${user.avatarUrl ? quote(user.avatarUrl) : "NULL"},${now},${now}) ON CONFLICT(id) DO UPDATE SET email=excluded.email,full_name=excluded.full_name,avatar_url=excluded.avatar_url,updated_at=excluded.updated_at;`,
  `INSERT INTO sessions (token_hash,user_id,expires_at,created_at,refreshed_at) VALUES (${quote(tokenHash)},${quote(user.userId)},${expiresAt},${now},${now}) ON CONFLICT(token_hash) DO UPDATE SET user_id=excluded.user_id,expires_at=excluded.expires_at,refreshed_at=excluded.refreshed_at;`,
  "",
].join("\n");

const scratch = mkdtempSync(join(tmpdir(), "yaver-edge-bootstrap-"));
const sqlPath = join(scratch, "bootstrap.sql");
try {
  writeFileSync(sqlPath, sql, { mode: 0o600 });
  chmodSync(scratch, 0o700);
  const result = spawnSync(
    "npx",
    ["wrangler", "d1", "execute", "yaver-edge", "--remote", "--file", sqlPath],
    { cwd: edgeDir, stdio: "inherit" },
  );
  if (result.status !== 0) process.exit(result.status ?? 1);
  console.log("Current Yaver identity bootstrapped into Edge (raw bearer not uploaded or printed)." );
} finally {
  // This directory was created by this process with a fixed mkdtemp prefix and
  // contains only the generated SQL file. Never accept a caller-supplied path.
  rmSync(scratch, { recursive: true, force: false });
}
