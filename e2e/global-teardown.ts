import { readFileSync, unlinkSync } from "fs";
import { join } from "path";
import type { TestUser } from "./global-setup";

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

const STATE_FILE = join(__dirname, ".playwright", "test-user.json");
const USERS_FILE = join(__dirname, ".playwright", "test-users.json");

async function deleteUser(user: TestUser): Promise<void> {
  try {
    const res = await fetch(`${CONVEX_URL}/auth/delete-account`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${user.token}`,
      },
    });
    if (!res.ok) {
      console.warn(
        `[e2e teardown] delete-account for ${user.email} returned ${res.status}`,
      );
    } else {
      console.log(`[e2e teardown] Deleted dummy user ${user.email}`);
    }
  } catch (e) {
    console.warn(`[e2e teardown] delete-account failed: ${(e as Error).message}`);
  }
}

export default async function globalTeardown(): Promise<void> {
  if (process.env.E2E_SKIP_LIVE_AUTH === "1") {
    console.log("[e2e teardown] Live dummy-user teardown skipped.");
    return;
  }

  let users: TestUser[] = [];
  try {
    users = JSON.parse(readFileSync(USERS_FILE, "utf8")) as TestUser[];
  } catch {
    // Fall back to the legacy single-user state file below.
  }

  let user: TestUser | null = null;
  try {
    user = JSON.parse(readFileSync(STATE_FILE, "utf8")) as TestUser;
  } catch {
    user = null;
  }

  if (user && !users.some((u) => u.token === user!.token || u.email === user!.email)) {
    users.push(user);
  }
  if (users.length === 0) {
    console.log("[e2e teardown] No dummy user state file found, skipping.");
    return;
  }

  for (const u of users) {
    await deleteUser(u);
  }

  try {
    unlinkSync(STATE_FILE);
  } catch {
    // best-effort
  }
  try {
    unlinkSync(USERS_FILE);
  } catch {
    // best-effort
  }
}
