#!/usr/bin/env node
/**
 * Headless end-to-end test of the relay credential path that mobile AND web
 * both depend on. No mocks: it talks to the real Convex backend and the real
 * relay, exactly as a client does.
 *
 * Why this exists — the 2026-07-13 outage:
 *   /config deliberately strips the relay password (it is per-user now, not a
 *   shared secret). The mobile client's getUserSettings() swallowed every
 *   failure and returned {}, which is indistinguishable from "this account has
 *   no settings". Any transient /settings failure therefore dropped the client
 *   into a fallback that installed the PASSWORD-LESS /config relays — and then
 *   persisted them. Every relay request 401'd forever, the phone could reach no
 *   machine at all, and the retry loop escalated into the relay's invalid-auth
 *   rate limiter. Every piece looked healthy in isolation; only the composed
 *   path was broken. That is exactly what this test covers.
 *
 * Usage:
 *   YAVER_TOKEN=<session token> node scripts/test-relay-path.mjs
 *   YAVER_TOKEN=... YAVER_DEVICE_ID=<deviceId> node scripts/test-relay-path.mjs
 *
 * A device id is optional: without one the test skips the live-tunnel probe and
 * still checks every credential-resolution invariant.
 */

const CONVEX =
  process.env.YAVER_CONVEX_SITE ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";
const TOKEN = process.env.YAVER_TOKEN;
const DEVICE_ID = process.env.YAVER_DEVICE_ID || "";

if (!TOKEN) {
  console.error("YAVER_TOKEN is required (a Yaver session token). Refusing to guess.");
  process.exit(2);
}

let failures = 0;
const ok = (name, detail = "") => console.log(`  \x1b[32m✓\x1b[0m ${name}${detail ? ` — ${detail}` : ""}`);
const bad = (name, detail = "") => {
  failures++;
  console.log(`  \x1b[31m✗\x1b[0m ${name}${detail ? ` — ${detail}` : ""}`);
};
const check = (cond, name, detail) => (cond ? ok(name, detail) : bad(name, detail));

const timeout = (ms) => AbortSignal.timeout(ms);

// ── 1. /config — the public platform config every client fetches first ───────
console.log("\n1. GET /config (platform relay list)");
const cfgRes = await fetch(`${CONVEX}/config`, { signal: timeout(15000) });
check(cfgRes.ok, "/config reachable", `HTTP ${cfgRes.status}`);
const cfg = await cfgRes.json();
const platformRelays = cfg.relayServers || [];
check(platformRelays.length > 0, "platform relay list is non-empty", `${platformRelays.length} relay(s)`);

const relay = platformRelays[0];
check(!!relay?.httpUrl, "platform relay exposes an httpUrl", relay?.httpUrl);

// The load-bearing invariant. If a password ever reappears here it is a shared
// secret handed to every caller of a PUBLIC, unauthenticated endpoint.
check(
  !relay?.password,
  "/config does NOT leak a relay password (per-user secret stays out of a public endpoint)",
  relay?.password ? "LEAKED!" : "absent, as intended",
);

// ── 2. /settings — where a client's real, per-user relay credential lives ────
console.log("\n2. GET /settings (per-user relay credential)");
const setRes = await fetch(`${CONVEX}/settings`, {
  headers: { Authorization: `Bearer ${TOKEN}` },
  signal: timeout(15000),
});
check(setRes.ok, "/settings reachable with a live session", `HTTP ${setRes.status}`);
const settings = (await setRes.json()).settings || {};
check(!!settings.relayUrl, "settings carry relayUrl", settings.relayUrl);
check(!!settings.relayPassword, "settings carry a per-user relayPassword", settings.relayPassword ? "<set>" : "MISSING");

// ── 3. Credential resolution — the step the mobile bug got wrong ─────────────
// Mirrors DeviceContext.resolveRelayServers: the account password must end up
// attached to the relay the client will actually dial.
console.log("\n3. resolve relay servers (mirror of DeviceContext.resolveRelayServers)");
const norm = (u) => (u || "").trim().replace(/\/+$/, "");
const matched = platformRelays.filter((r) => norm(r.httpUrl) === norm(settings.relayUrl));
check(matched.length > 0, "settings.relayUrl matches a platform relay", `${norm(settings.relayUrl)}`);

const resolved = (matched.length > 0 ? matched : platformRelays).map((r) => ({
  ...r,
  password: r.password || settings.relayPassword,
}));
check(
  resolved.every((r) => !!r.password),
  "every resolved relay carries a password",
  "a password-less relay is a guaranteed 401 loop",
);

const relayHttp = norm(resolved[0].httpUrl);
const relayPw = resolved[0].password;

// ── 4. Live relay auth — positive and negative ───────────────────────────────
console.log("\n4. relay authentication (live)");
const health = await fetch(`${relayHttp}/health`, { signal: timeout(10000) })
  .then((r) => r.json())
  .catch(() => null);
check(health?.ok === true, "relay is healthy", health ? `v${health.version}` : "unreachable");

if (DEVICE_ID) {
  // POSITIVE: the resolved credential must authenticate. Reaching the agent —
  // even when the AGENT then demands its own bearer token — proves the relay
  // accepted us and forwarded. A relay-level rejection says "invalid relay
  // password" instead.
  const good = await fetch(`${relayHttp}/d/${DEVICE_ID}/info`, {
    headers: { "X-Relay-Password": relayPw },
    signal: timeout(15000),
  });
  const goodBody = await good.text();
  const relayRejected = goodBody.includes("invalid relay password");
  const noTunnel = goodBody.includes("not connected to relay");

  if (noTunnel) {
    console.log(`  \x1b[33m~\x1b[0m device has no tunnel (agent offline) — relay auth still passed`);
  } else {
    check(!relayRejected, "resolved credential authenticates to the relay", relayRejected ? goodBody.slice(0, 60) : "relay forwarded to the agent");
  }

  // NEGATIVE: this is precisely what the old mobile fallback sent. It MUST be
  // rejected — and the client must therefore never take that path.
  const bare = await fetch(`${relayHttp}/d/${DEVICE_ID}/info`, { signal: timeout(15000) });
  const bareBody = await bare.text();
  check(
    bare.status === 401 || bareBody.includes("invalid relay password"),
    "a password-less request is rejected (the old fallback was fatal)",
    `HTTP ${bare.status}`,
  );
} else {
  console.log("  \x1b[33m~\x1b[0m YAVER_DEVICE_ID not set — skipped the live tunnel probe");
}

// ── 5. The composed invariant the outage violated ────────────────────────────
// A client that only has /config (i.e. one whose /settings read failed) holds
// NO usable relay credential. So "fall back to /config on error" is not a
// degraded mode — it is a guaranteed, self-inflicted outage. Assert that the
// only credential source is /settings.
console.log("\n5. composed invariant");
const configOnlyHasCredential = platformRelays.some((r) => !!r.password);
check(
  !configOnlyHasCredential,
  "a /config-only client has NO relay credential",
  "=> clients MUST NOT fall back to /config when /settings fails",
);

console.log(
  failures === 0
    ? `\n\x1b[32mPASS\x1b[0m — relay credential path is sound\n`
    : `\n\x1b[31mFAIL\x1b[0m — ${failures} check(s) failed\n`,
);
process.exit(failures === 0 ? 0 : 1);
