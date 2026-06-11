/**
 * Bootstrap auto-pair E2E — the "headless Mac lost its token" scenario.
 *
 * Simulates:
 *   1. User's Mac previously authed (device registered in Convex with
 *      hardwareId + publicKey).
 *   2. Power glitch / reboot / token expiry → agent enters bootstrap mode.
 *   3. Agent posts to /devices/bootstrap (no auth token) → Convex marks
 *      device needsAuth=true + online=true, authed by matching
 *      (hardwareId, publicKey) against existing record.
 *   4. Mobile app sees the device with needsAuth=true in its list and
 *      can push an encrypted token to re-auth it remotely.
 *
 * This test verifies the Convex side of that flow. The mobile-side
 * auto-pair UI is tested by direct install + manual QA (no simulator
 * can do Apple Sign-In flow).
 */
import { test, expect } from "@playwright/test";
import { readFileSync } from "fs";
import { join } from "path";

const CONVEX =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

interface TestUser {
  token: string;
  userId: string;
  email: string;
}

function loadTestUser(): TestUser {
  const stateFile = join(__dirname, "..", ".playwright", "test-user.json");
  return JSON.parse(readFileSync(stateFile, "utf8"));
}

async function jpost(path: string, body: unknown, token?: string) {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(`${CONVEX}${path}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  return { status: res.status, body: await res.json().catch(() => ({})) };
}

async function jget(path: string, token: string) {
  const res = await fetch(`${CONVEX}${path}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  return { status: res.status, body: await res.json().catch(() => ({})) };
}

test.describe.configure({ mode: "serial" });

test.describe("Bootstrap auto-pair (headless Mac recovery)", () => {
  const HWID = "e2e-hw-" + Math.random().toString(36).slice(2, 10);
  const PUBKEY = "Xw3H/09zK5dO8o6aOhrZOEP5SrFdufkmNMY24NEI5z4=";
  let deviceId = "";

  test.beforeAll(async () => {
    const user = loadTestUser();
    // Register the "previously authed" Mac
    const reg = await jpost(
      "/devices/register",
      {
        deviceId: "e2e-mac-" + Date.now(),
        name: "E2E-Bootstrap-Mac",
        platform: "macos",
        publicKey: PUBKEY,
        quicHost: "192.168.111.8",
        quicPort: 18080,
        hardwareId: HWID,
      },
      user.token,
    );
    expect(reg.status).toBe(200);
    // Grab the Convex-assigned deviceId from /devices/list
    const list = await jget("/devices/list", user.token);
    expect(list.status).toBe(200);
    const dev = list.body.devices.find(
      (d: any) => d.name === "E2E-Bootstrap-Mac",
    );
    expect(dev).toBeTruthy();
    deviceId = dev.deviceId;
  });

  test("POST /devices/bootstrap with matching hwid+pubkey → ok", async () => {
    const res = await jpost("/devices/bootstrap", {
      deviceId,
      hardwareId: HWID,
      publicKey: PUBKEY,
      quicHost: "192.168.111.8",
      quicPort: 18080,
    });
    expect(res.status).toBe(200);
    expect(res.body.ok).toBe(true);
    expect(res.body.userId).toBeTruthy();
  });

  test("/devices/list shows needsAuth=true + online=true", async () => {
    const user = loadTestUser();
    const list = await jget("/devices/list", user.token);
    const dev = list.body.devices.find(
      (d: any) => d.name === "E2E-Bootstrap-Mac",
    );
    expect(dev).toBeTruthy();
    expect(dev.needsAuth).toBe(true);
    expect(dev.isOnline).toBe(true);
    expect(dev.quicHost).toBe("192.168.111.8");
  });

  test("POST /devices/bootstrap with wrong hardwareId → 400 Hardware ID mismatch", async () => {
    const res = await jpost("/devices/bootstrap", {
      deviceId,
      hardwareId: "WRONG_HWID",
      publicKey: PUBKEY,
    });
    expect(res.status).toBeGreaterThanOrEqual(400);
    expect(JSON.stringify(res.body)).toContain("Hardware ID mismatch");
  });

  test("POST /devices/bootstrap with wrong publicKey → 400 Public key mismatch", async () => {
    const res = await jpost("/devices/bootstrap", {
      deviceId,
      hardwareId: HWID,
      publicKey: "WRONG_PUBKEY_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
    });
    expect(res.status).toBeGreaterThanOrEqual(400);
    expect(JSON.stringify(res.body)).toContain("Public key mismatch");
  });

  test("POST /devices/bootstrap with missing fields → 400", async () => {
    const res = await jpost("/devices/bootstrap", { deviceId });
    expect(res.status).toBe(400);
  });

  test("POST /devices/bootstrap with unknown deviceId → 400 Device not found", async () => {
    const res = await jpost("/devices/bootstrap", {
      deviceId: "nonexistent-device-id-" + Date.now(),
      hardwareId: HWID,
      publicKey: PUBKEY,
    });
    expect(res.status).toBeGreaterThanOrEqual(400);
    expect(JSON.stringify(res.body)).toContain("Device not found");
  });
});
