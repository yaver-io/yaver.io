// Tests for the UserMeter Durable Object carry + cap logic, plus the
// no-DO fallback. Run: cd gateway && npx tsx --test src/limiter.test.ts
//
// We drive the DO directly with a tiny in-memory storage fake (the parts
// of DurableObjectState the class actually uses: storage.get/put).

import test from "node:test";
import assert from "node:assert/strict";
import { UserMeter, meterRecord } from "./limiter";

function fakeState() {
  const m = new Map<string, unknown>();
  return {
    storage: {
      async get<T>(k: string): Promise<T | undefined> {
        return m.get(k) as T | undefined;
      },
      async put(k: string, v: unknown): Promise<void> {
        m.set(k, v);
      },
    },
  } as any;
}

function meter(): UserMeter {
  return new UserMeter(fakeState());
}

async function post(do_: UserMeter, path: string, body: unknown): Promise<any> {
  const res = await do_.fetch(
    new Request(`https://do${path}`, { method: "POST", body: JSON.stringify(body) }),
  );
  return res.json();
}

test("record carries sub-cent COGS and emits only whole cents", async () => {
  const d = meter();
  let r = await post(d, "/record", { rawCents: 0.3 });
  assert.equal(r.bill, 0); // 0.3 carried, nothing billed
  r = await post(d, "/record", { rawCents: 0.3 });
  assert.equal(r.bill, 0); // 0.6 carried
  r = await post(d, "/record", { rawCents: 0.5 });
  assert.equal(r.bill, 1); // 1.1 → bill 1, carry 0.1
  assert.ok(Math.abs(r.carry - 0.1) < 1e-9);
});

test("record sums whole + fractional correctly across calls", async () => {
  const d = meter();
  const r1 = await post(d, "/record", { rawCents: 2.7 });
  assert.equal(r1.bill, 2); // carry 0.7
  const r2 = await post(d, "/record", { rawCents: 0.4 });
  assert.equal(r2.bill, 1); // 0.7+0.4=1.1 → bill 1, carry 0.1
  assert.equal(r2.cents, 3); // window total billed
});

test("check allows under the cap and denies over it", async () => {
  const d = meter();
  // accumulate 4 whole cents
  await post(d, "/record", { rawCents: 4 });
  let r = await post(d, "/check", { estCents: 1, capCentsPerHour: 5 });
  assert.equal(r.allow, true); // 4 + 1 <= 5
  r = await post(d, "/check", { estCents: 2, capCentsPerHour: 5 });
  assert.equal(r.allow, false); // 4 + 2 > 5
  assert.equal(r.remaining, 1);
});

test("check with no cap (0) always allows and reports null remaining", async () => {
  const d = meter();
  await post(d, "/record", { rawCents: 9999 });
  const r = await post(d, "/check", { estCents: 1_000_000, capCentsPerHour: 0 });
  assert.equal(r.allow, true);
  assert.equal(r.remaining, null);
});

test("negative/garbage rawCents is clamped to 0 (never credits)", async () => {
  const d = meter();
  const r = await post(d, "/record", { rawCents: -5 });
  assert.equal(r.bill, 0);
  assert.equal(r.cents, 0);
});

test("unknown DO path 404s", async () => {
  const d = meter();
  const res = await d.fetch(new Request("https://do/nope", { method: "POST", body: "{}" }));
  assert.equal(res.status, 404);
});

test("meterRecord falls back to ceil when no DO binding is present", async () => {
  assert.equal(await meterRecord({}, "u1", 0.3), 1); // ceil(0.3)
  assert.equal(await meterRecord({}, "u1", 2.1), 3); // ceil(2.1)
  assert.equal(await meterRecord({}, "u1", 0), 0);
  assert.equal(await meterRecord({}, "u1", -4), 0); // clamped
});
