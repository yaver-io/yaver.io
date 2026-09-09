import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

const root = join(import.meta.dirname, "..", "..");
const schema = readFileSync(join(root, "backend/convex/schema.ts"), "utf8");
const devices = readFileSync(join(root, "backend/convex/devices.ts"), "utf8");
const http = readFileSync(join(root, "backend/convex/http.ts"), "utf8");

test("SSH profiles are structured, owner-scoped, and exposed over one device route", () => {
  assert.match(schema, /sshProfile:\s*v\.optional\(v\.object\(/);
  assert.match(schema, /v\.literal\("zsh"\)/);
  assert.doesNotMatch(schema.match(/sshProfile:[\s\S]*?updatedAt: v\.number\(\)/)?.[0] || "", /command/i);
  assert.match(devices, /export const setDeviceSSHProfile = mutation/);
  assert.match(devices, /device\.userId !== session\.user\._id/);
  assert.match(devices, /SSH_PROFILE_SESSION/);
  assert.match(http, /path: "\/devices\/ssh-profile"/);
  assert.match(http, /api\.devices\.setDeviceSSHProfile/);
});
