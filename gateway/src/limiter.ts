// UserMeter — per-user Durable Object for two jobs the spec flags as
// launch prerequisites:
//
//   1. Per-hour spend cap. A rolling 1h window of billed COGS cents; once
//      it exceeds MAX_CENTS_PER_HOUR the gateway 429s further requests.
//      Bounds a runaway agent loop even when the wallet is flush.
//
//   2. Sub-cent carry. Inference COGS for a single request is often a
//      fraction of a cent. If each request were ceil-rounded to whole
//      cents before metering, many tiny calls would systematically
//      over-charge. The DO accumulates fractional COGS and only emits
//      WHOLE cents downstream, carrying the remainder — so the user is
//      billed exactly (cents granularity), not per-call rounded up.
//
// One instance per userId (idFromName(userId)). State is a single record.
// Date.now() is valid inside a Worker/DO request context.
//
// Graceful: if the USER_METER binding is absent (not yet configured),
// index.ts falls back to per-request metering — the gateway still works,
// just without the cap/carry. So this is an enhancement, never a hard dep.

export interface MeterRecord {
  windowStartMs: number; // start of the current rolling hour
  cents: number;         // whole COGS cents billed this window
  carry: number;         // fractional COGS cents not yet emitted (0..1)
}

const WINDOW_MS = 60 * 60 * 1000;
const KEY = "m";

export class UserMeter implements DurableObject {
  private state: DurableObjectState;

  constructor(state: DurableObjectState) {
    this.state = state;
  }

  private async load(now: number): Promise<MeterRecord> {
    const rec = (await this.state.storage.get<MeterRecord>(KEY)) ?? {
      windowStartMs: now,
      cents: 0,
      carry: 0,
    };
    // Roll the window forward (carry survives — it's owed money, not a rate).
    if (now - rec.windowStartMs >= WINDOW_MS) {
      rec.windowStartMs = now;
      rec.cents = 0;
    }
    return rec;
  }

  async fetch(req: Request): Promise<Response> {
    const url = new URL(req.url);
    const now = Date.now();
    const rec = await this.load(now);

    if (url.pathname === "/check") {
      const { estCents = 0, capCentsPerHour = 0 } = (await req.json().catch(() => ({}))) as {
        estCents?: number;
        capCentsPerHour?: number;
      };
      const cap = capCentsPerHour > 0 ? capCentsPerHour : Infinity;
      const allow = rec.cents + Math.max(0, estCents) <= cap;
      await this.state.storage.put(KEY, rec); // persist any window roll
      return Response.json({
        allow,
        cents: rec.cents,
        remaining: cap === Infinity ? null : Math.max(0, cap - rec.cents),
      });
    }

    if (url.pathname === "/record") {
      const { rawCents = 0 } = (await req.json().catch(() => ({}))) as { rawCents?: number };
      rec.carry += Math.max(0, rawCents);
      const whole = Math.floor(rec.carry);
      rec.carry -= whole;
      rec.cents += whole;
      await this.state.storage.put(KEY, rec);
      // `bill` = whole COGS cents to forward to Convex (markup applied there).
      return Response.json({ bill: whole, cents: rec.cents, carry: rec.carry });
    }

    return new Response("not found", { status: 404 });
  }
}

// ── Client helpers (called from index.ts) ───────────────────────────

type MeterEnv = {
  USER_METER?: DurableObjectNamespace;
  MAX_CENTS_PER_HOUR?: string;
};

function stub(env: MeterEnv, userId: string) {
  const ns = env.USER_METER!;
  return ns.get(ns.idFromName(userId));
}

/** Ask the user's meter whether a request estimated at `estCents` fits under
 *  the rolling hourly cap. Returns true (allow) when no DO is bound. */
export async function meterCheck(
  env: MeterEnv,
  userId: string,
  estCents: number,
): Promise<{ allow: boolean; remaining: number | null }> {
  if (!env.USER_METER) return { allow: true, remaining: null };
  const cap = Number(env.MAX_CENTS_PER_HOUR) || 0;
  const res = await stub(env, userId).fetch("https://do/check", {
    method: "POST",
    body: JSON.stringify({ estCents, capCentsPerHour: cap }),
  });
  return (await res.json()) as { allow: boolean; remaining: number | null };
}

/** Record raw fractional COGS cents; returns the WHOLE cents to forward to
 *  Convex (carrying the remainder). With no DO bound, falls back to ceil so
 *  usage is never dropped (slightly pro-margin, the documented MVP behavior). */
export async function meterRecord(
  env: MeterEnv,
  userId: string,
  rawCents: number,
): Promise<number> {
  if (!env.USER_METER) return Math.ceil(Math.max(0, rawCents));
  const res = await stub(env, userId).fetch("https://do/record", {
    method: "POST",
    body: JSON.stringify({ rawCents }),
  });
  const { bill } = (await res.json()) as { bill: number };
  return bill;
}
