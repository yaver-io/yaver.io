import { readFileSync, unlinkSync } from "fs";
import { join } from "path";
import type { TestUser } from "./global-setup";

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  "https://shocking-echidna-394.eu-west-1.convex.site";

const STATE_FILE = join(__dirname, ".playwright", "test-user.json");

export default async function globalTeardown(): Promise<void> {
  if (process.env.E2E_SKIP_LIVE_AUTH === "1") {
    console.log("[e2e teardown] Live dummy-user teardown skipped.");
    return;
  }

  let user: TestUser;
  try {
    user = JSON.parse(readFileSync(STATE_FILE, "utf8")) as TestUser;
  } catch {
    console.log("[e2e teardown] No dummy user state file found, skipping.");
    return;
  }

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

  try {
    unlinkSync(STATE_FILE);
  } catch {
    // best-effort
  }
}
