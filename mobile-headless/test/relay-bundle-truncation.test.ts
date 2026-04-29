// Reproduces the iPhone bundle-download failure end-to-end via the
// public relay. Surfaces the exact error iOS' YaverBundleLoader sees
// (HTTP 502 "tunnel response parse error") so we can iterate on the
// agent → relay → mobile path without flying through TestFlight.
//
// Opt-in via env vars — no fly time on a fresh `bun test` checkout:
//
//   YMH_RELAY_TRUNCATION_DEVICE=fc30be6e-...   # agent device id
//   YMH_RELAY_TRUNCATION_TOKEN=<owner-token>   # agent owner's session token
//   YMH_RELAY_TRUNCATION_CONVEX=https://perceptive-minnow-557...
//
// Without those it skips. CI never hits this; it's a manual debug tool.

import { describe, expect, it } from "bun:test";

const DEVICE = process.env.YMH_RELAY_TRUNCATION_DEVICE || "";
const TOKEN = process.env.YMH_RELAY_TRUNCATION_TOKEN || "";
const CONVEX = process.env.YMH_RELAY_TRUNCATION_CONVEX || "";
const RELAY = process.env.YMH_RELAY_TRUNCATION_RELAY || "https://public.yaver.io";

const maybe = DEVICE && TOKEN && CONVEX ? describe : describe.skip;

interface SettingsResponse {
  ok: boolean;
  settings?: { relayPassword?: string; relayUrl?: string };
}

async function fetchRelayPassword(): Promise<string> {
  const res = await fetch(`${CONVEX}/settings`, {
    headers: { Authorization: `Bearer ${TOKEN}` },
  });
  const data = (await res.json()) as SettingsResponse;
  return data?.settings?.relayPassword ?? "";
}

async function relayGet(path: string, relayPassword: string): Promise<{
  status: number;
  bytes: number;
  body: ArrayBuffer;
  headers: Headers;
  durationMs: number;
}> {
  const start = Date.now();
  const res = await fetch(`${RELAY}/d/${DEVICE}${path}`, {
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      "X-Relay-Password": relayPassword,
    },
  });
  const body = await res.arrayBuffer();
  return {
    status: res.status,
    bytes: body.byteLength,
    body,
    headers: res.headers,
    durationMs: Date.now() - start,
  };
}

async function relayPostJSON<T = unknown>(path: string, body: unknown, relayPassword: string): Promise<{
  status: number;
  body: T | string;
  durationMs: number;
}> {
  const start = Date.now();
  const res = await fetch(`${RELAY}/d/${DEVICE}${path}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      "X-Relay-Password": relayPassword,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  let parsed: T | string = text;
  try {
    parsed = JSON.parse(text) as T;
  } catch {
    /* keep as string */
  }
  return { status: res.status, body: parsed, durationMs: Date.now() - start };
}

maybe("relay bundle truncation — reproduces iPhone HTTP 502", () => {
  it("small responses through relay — sanity check", async () => {
    const pw = await fetchRelayPassword();
    expect(pw.length).toBeGreaterThan(0);

    // /info — small JSON, ~1KB. Should round-trip cleanly.
    const r = await relayGet("/info", pw);
    expect(r.status).toBe(200);
    expect(r.bytes).toBeGreaterThan(0);
    expect(r.bytes).toBeLessThan(64 * 1024);
  }, 30_000);

  it("triggers a Hermes build via relay (warm-up — small JSON response)", async () => {
    const pw = await fetchRelayPassword();
    const r = await relayPostJSON<{ status?: string; size?: number; bundleUrl?: string }>(
      "/dev/build-native",
      { platform: "ios" },
      pw,
    );
    if (r.status !== 200) {
      // If build-native needs an active dev server first, skip the rest
      // — main test focus is /dev/native-bundle truncation, not build.
      console.warn(`build-native returned ${r.status} — body:`, r.body);
      return;
    }
    expect(r.status).toBe(200);
    if (typeof r.body !== "string") {
      expect(r.body.status).toBe("ok");
      expect(r.body.bundleUrl).toBe("/dev/native-bundle");
      expect(r.body.size).toBeGreaterThan(1024 * 1024); // > 1MB
    }
  }, 240_000);

  it("8.5 MB bundle download through relay — REPRODUCES the truncation bug", async () => {
    const pw = await fetchRelayPassword();
    const r = await relayGet("/dev/native-bundle", pw);

    console.log(
      `[bundle-download] status=${r.status} bytes=${r.bytes} time=${r.durationMs}ms`,
    );

    // EXPECTED OUTCOME with the current buffered protocol:
    //   status = 502
    //   body = "tunnel response parse error\n" (28 bytes)
    // The 11 MB JSON envelope produced by the agent is truncated on
    // the agent → relay QUIC stream because:
    //   (a) agent's stream.Write(11MB) doesn't wait for full delivery, OR
    //   (b) quic-go default stream window is < 11 MB
    // Once the streaming wire protocol or windowed write fix lands,
    // this should flip to status 200 with the full 8,490,239-byte
    // HBC bundle.
    if (r.status === 502) {
      const txt = new TextDecoder().decode(new Uint8Array(r.body).slice(0, 64));
      console.log(`[bundle-download] BUG REPRODUCED — relay returned: ${JSON.stringify(txt)}`);
      // Document the expected failure for now so the test doesn't
      // green incorrectly while the bug still exists.
      expect(txt).toContain("tunnel response parse error");
    } else if (r.status === 200) {
      // Fix landed!
      expect(r.bytes).toBe(8_490_239);
      // HBC magic: 0x1F1903C1 at offset 4 (little-endian)
      const head = new Uint8Array(r.body).slice(0, 12);
      expect(head[4]).toBe(0xc1);
      expect(head[5]).toBe(0x03);
      expect(head[6]).toBe(0x19);
      expect(head[7]).toBe(0x1f);
    } else {
      throw new Error(`unexpected status=${r.status}`);
    }
  }, 120_000);
});
