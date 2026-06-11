import { randomUUID } from "crypto";
import { writeFileSync, mkdirSync, readFileSync } from "fs";
import { dirname, join } from "path";

/**
 * Create a throwaway "dummy test user" against the live Convex backend before
 * the test run. Credentials are handed to the tests via `process.env` and
 * also persisted to `.playwright/test-user.json` so the global teardown can
 * pick them up even in a separate process.
 *
 * The user email is randomized per run (`e2e-<uuid>@yaver.test`) so parallel
 * runs never collide. `global-teardown.ts` deletes the account afterwards via
 * `/auth/delete-account`.
 *
 * IMPORTANT: never hardcode real credentials in this file. It lives in the
 * open-source repo.
 */

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

export interface TestUser {
  email: string;
  password: string;
  fullName: string;
  token: string;
  userId: string;
}

const STATE_FILE = join(__dirname, ".playwright", "test-user.json");
const USERS_FILE = join(__dirname, ".playwright", "test-users.json");

async function createDummyUser(): Promise<TestUser> {
  const id = randomUUID();
  const email = `e2e-${id}@yaver.test`;
  const password = `pw-${randomUUID()}`;
  const fullName = `E2E Test ${id.slice(0, 8)}`;

  const res = await fetch(`${CONVEX_URL}/auth/signup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password, fullName }),
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(
      `[e2e setup] Failed to create dummy test user: ${res.status} ${text}`,
    );
  }

  const data = (await res.json()) as { token: string; userId: string };
  return { email, password, fullName, token: data.token, userId: data.userId };
}

export default async function globalSetup(): Promise<void> {
  if (process.env.E2E_SKIP_LIVE_AUTH === "1") {
    console.log("[e2e setup] Skipping live dummy-user creation.");
    return;
  }

  const user = await createDummyUser();

  process.env.E2E_USER_EMAIL = user.email;
  process.env.E2E_USER_PASSWORD = user.password;
  process.env.E2E_USER_FULL_NAME = user.fullName;
  process.env.E2E_USER_TOKEN = user.token;
  process.env.E2E_USER_ID = user.userId;

  mkdirSync(dirname(STATE_FILE), { recursive: true });
  writeFileSync(STATE_FILE, JSON.stringify(user, null, 2));
  writeFileSync(USERS_FILE, JSON.stringify([user], null, 2));

  console.log(`[e2e setup] Created dummy user ${user.email}`);
}

export function rememberTestUser(user: TestUser): void {
  mkdirSync(dirname(USERS_FILE), { recursive: true });
  let users: TestUser[] = [];
  try {
    users = JSON.parse(readFileSync(USERS_FILE, "utf8")) as TestUser[];
  } catch {
    users = [];
  }
  // Upsert by email: a test that re-mints a session (e.g. after logout
  // invalidates the original token) must replace the stale token so teardown
  // can still delete the account. Push when the email is new.
  const idx = users.findIndex((u) => u.email === user.email);
  if (idx === -1) {
    users.push(user);
  } else {
    users[idx] = { ...users[idx], ...user };
  }
  writeFileSync(USERS_FILE, JSON.stringify(users, null, 2));
}
