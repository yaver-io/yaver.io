#!/usr/bin/env node

import { createHash, randomBytes, randomUUID } from "node:crypto";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { execFileSync, spawnSync } from "node:child_process";

if (process.env.YAVER_EDGE_DEVICE_LIMIT_E2E !== "1") {
  console.error("Refusing remote mutation without YAVER_EDGE_DEVICE_LIMIT_E2E=1.");
  process.exit(2);
}

const edgeDir = resolve(import.meta.dirname, "..");
const suffix = randomUUID().replaceAll("-", "").slice(0, 16);
const userId = `e2e-device-limit-${suffix}`;
const token = randomBytes(32).toString("hex");
const tokenHash = createHash("sha256").update(token).digest("hex");
const publicKey = `e2e-public-key-${suffix}`;
const deviceIds = [1, 2, 3].map((index) => `e2e-limit-${suffix}-${index}`);
const scratch = mkdtempSync(join(tmpdir(), "yaver-edge-limit-e2e-"));
chmodSync(scratch, 0o700);

const quote = (value) => `'${String(value).replaceAll("'", "''")}'`;

function d1(sql, filename) {
  const sqlPath = join(scratch, filename);
  writeFileSync(sqlPath, `${sql.trim()}\n`, { mode: 0o600 });
  const result = spawnSync(
    "npx",
    ["wrangler", "d1", "execute", "yaver-edge", "--remote", "--file", sqlPath],
    { cwd: edgeDir, encoding: "utf8" },
  );
  if (result.status !== 0) {
    throw new Error(`D1 ${filename} failed: ${String(result.stderr || result.stdout).trim()}`);
  }
}

const edgeIp = execFileSync("dig", ["@1.1.1.1", "+short", "edge.yaver.io", "A"], {
  encoding: "utf8",
}).trim().split(/\s+/)[0];
if (!edgeIp) throw new Error("Cloudflare authoritative DNS returned no edge IPv4 address.");

function request(path, { method = "GET", body } = {}) {
  const args = [
    "--silent", "--show-error", "--max-time", "15",
    "--resolve", `edge.yaver.io:443:${edgeIp}`,
    "--request", method,
    "--header", `Authorization: Bearer ${token}`,
    "--header", "Content-Type: application/json",
    ...(body === undefined ? [] : ["--data", JSON.stringify(body)]),
    "--write-out", "\n%{http_code}",
    `https://edge.yaver.io${path}`,
  ];
  const output = execFileSync("curl", args, { encoding: "utf8" });
  const splitAt = output.lastIndexOf("\n");
  const status = Number(output.slice(splitAt + 1));
  const payload = JSON.parse(output.slice(0, splitAt));
  return { status, payload };
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

let seeded = false;
let verified = false;
try {
  const now = Date.now();
  d1(`
    INSERT INTO users (id, email, full_name, created_at, updated_at)
    VALUES (${quote(userId)}, ${quote(`${userId}@invalid.yaver.test`)}, 'Device limit E2E', ${now}, ${now});
    INSERT INTO sessions (token_hash, user_id, expires_at, created_at, refreshed_at)
    VALUES (${quote(tokenHash)}, ${quote(userId)}, ${now + 3_600_000}, ${now}, ${now});
  `, "seed.sql");
  seeded = true;

  const register = (deviceId) => request("/devices/register", {
    method: "POST",
    body: { deviceId, publicKey, name: deviceId, platform: "e2e" },
  });
  assert(register(deviceIds[0]).status === 200, "first free device registration failed");
  assert(register(deviceIds[1]).status === 200, "second free device registration failed");

  const denied = register(deviceIds[2]);
  assert(denied.status === 403, `third free device returned ${denied.status}, expected 403`);
  assert(denied.payload.code === "free_device_limit_reached", "third device lacked the stable reason code");
  assert(denied.payload.maxOwnedDevices === 2, "third device lacked the effective limit");
  assert(denied.payload.remedy?.url === "https://yaver.io/dashboard?tab=billing", "third device lacked the billing route");

  assert(register(deviceIds[0]).status === 200, "existing device could not reconnect at the cap");
  const listed = request("/devices/list");
  const exactOwnedIds = (listed.payload.devices || [])
    .filter((device) => Number(device.isOwner) === 1)
    .map((device) => String(device.deviceId))
    .sort();
  assert(JSON.stringify(exactOwnedIds) === JSON.stringify(deviceIds.slice(0, 2).sort()),
    `unexpected cleanup candidates: ${JSON.stringify(exactOwnedIds)}`);
  verified = true;
} finally {
  if (seeded) {
    d1(`
      DELETE FROM device_access WHERE device_id IN (
        SELECT device_id FROM devices WHERE owner_user_id = ${quote(userId)}
      );
      DELETE FROM devices WHERE owner_user_id = ${quote(userId)};
      DELETE FROM sessions WHERE user_id = ${quote(userId)};
      DELETE FROM users WHERE id = ${quote(userId)};
    `, "cleanup.sql");
    const afterCleanup = request("/auth/validate");
    assert(afterCleanup.status === 401, "synthetic session still authenticated after cleanup");
  }
  rmSync(scratch, { recursive: true, force: false });
}

assert(verified, "device limit verification did not complete");
console.log("Production device-limit E2E passed: 2 accepted, 3rd denied, reconnect accepted, exact test identity removed.");
