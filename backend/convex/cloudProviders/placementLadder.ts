import type { ProviderId } from "./types";

/**
 * ─── Placement ladder ───────────────────────────────────────────────────────
 *
 * "Never disappoint the user because one provider is out of capacity" — done
 * safely. See docs/architecture/cloud-multiprovider-placement-architecture.md.
 *
 * MEASURED 2026-07-21: every SKU Yaver uses was sold out in all three EU
 * Hetzner datacenters while a dozen other types were orderable. Capacity is not
 * a hypothetical edge case; it is the normal operating condition. Park is
 * delete-not-stop, so a wake must ORDER A NEW SERVER — meaning a capacity gap
 * does not merely delay a purchase, it makes an existing workspace unwakeable.
 *
 * Two rules make the difference between a ladder that helps and one that hurts:
 *
 *  1. **Classify before retrying.** Retrying an unretryable failure across
 *     every candidate turns one clear error into N confusing ones and wastes
 *     the user's time. The `cx32` bug (a SKU that never existed) was exactly
 *     this class — a ladder without classification would have marched it
 *     through every datacenter and reported "no capacity anywhere".
 *  2. **Reclaim before advancing.** Every attempt that creates a resource must
 *     release it before the next attempt, or the ladder multiplies the orphan
 *     bug by the number of rungs.
 */

export type ProviderFailureClass =
  | "capacity"      // advance the ladder — someone else has room
  | "quota"         // advance, and ALERT: our limit, not the market's
  | "bad_request"   // STOP: identical failure everywhere
  | "auth"          // STOP and alert: retrying spreads a credential failure
  | "transient"     // retry the SAME candidate, bounded
  | "unknown";      // treat as transient once, then stop

/**
 * Classify a provider error from its message/body.
 *
 * Ordered most-specific first. `bad_request` and `auth` are checked BEFORE
 * capacity because a message can contain both words and stopping is the safe
 * default — advancing on a permanent error is the expensive mistake.
 */
export function classifyProviderError(raw: unknown): ProviderFailureClass {
  const text = (raw instanceof Error ? raw.message : String(raw ?? "")).toLowerCase();
  if (!text) return "unknown";

  // Credentials — never retry, never spread.
  if (/\b(401|403)\b|unauthor|forbidden|invalid[_ ]?client|signature.*match|expired.*token|accessdenied/.test(text)) {
    return "auth";
  }
  // Our own limit. Advancing may help, but an operator must know.
  if (/quota|limit exceeded|limitexceeded|too many|maxnumber|exceeded.*limit/.test(text)) {
    return "quota";
  }
  // Permanently wrong request — identical everywhere. THE cx32 CLASS.
  if (
    /invalid.*(server[_ ]?type|instance[_ ]?type|machine[_ ]?type|sku|image|ami|parameter|argument)/.test(text) ||
    /server type not found|not found.*type|unsupported.*type|no such image|image.*not found|invalidparametervalue|malformed/.test(text) ||
    /\b400\b|\b404\b|\b422\b/.test(text)
  ) {
    return "bad_request";
  }
  // Capacity — the one class the ladder exists for.
  if (
    /resource_unavailable|resource unavailable|no available|not available|capacity|placement|sold ?out/.test(text) ||
    /insufficientinstancecapacity|insufficientcapacity|zone_resource_pool_exhausted|skunotavailable|allocationfailed/.test(text)
  ) {
    return "capacity";
  }
  if (/\b(500|502|503|504)\b|timeout|timed out|temporar|try again|econnreset|network/.test(text)) {
    return "transient";
  }
  return "unknown";
}

/** Should the ladder move to the next candidate for this class? */
export function shouldAdvance(cls: ProviderFailureClass): boolean {
  return cls === "capacity" || cls === "quota";
}

/** Should we stop entirely, because no candidate can succeed? */
export function shouldAbort(cls: ProviderFailureClass): boolean {
  return cls === "bad_request" || cls === "auth";
}

export type PlacementCandidate = {
  provider: ProviderId;
  /** Datacenter / zone / region the create must target. */
  scope: string;
  sku: string;
  reasons: string[];
};

export type LadderAttemptResult<T> = {
  ok: boolean;
  value?: T;
  candidate?: PlacementCandidate;
  /** One line per rung: what was tried and why it failed. Operator gold. */
  trail: string[];
  failureClass?: ProviderFailureClass;
  /** Present when a resource was created but could NOT be reclaimed. */
  strandedResourceIds: string[];
};

export type LadderAttemptOptions<T> = {
  candidates: PlacementCandidate[];
  /**
   * Attempt one rung. If it creates a provider resource it MUST return the id
   * via `onCreated` before doing anything else that can throw — that is what
   * lets the ladder reclaim it (the R1 orphan window, per rung).
   */
  attempt: (candidate: PlacementCandidate, onCreated: (id: string) => void) => Promise<T>;
  /** Release a resource created by a failed attempt. */
  reclaim?: (candidate: PlacementCandidate, cloudResourceId: string) => Promise<void>;
  /** Bounded retries for `transient` on the SAME rung. */
  transientRetries?: number;
};

/**
 * Walk the ladder until something succeeds or nothing can.
 *
 * The invariant that matters: **a rung never leaves a resource behind.** If an
 * attempt created something and then failed, it is reclaimed before advancing;
 * if reclamation also fails, the id is surfaced in `strandedResourceIds` so the
 * caller can shout about it rather than lose it.
 */
export async function attemptAcrossLadder<T>(
  opts: LadderAttemptOptions<T>,
): Promise<LadderAttemptResult<T>> {
  const trail: string[] = [];
  const stranded: string[] = [];
  const maxTransient = opts.transientRetries ?? 1;
  let lastClass: ProviderFailureClass | undefined;

  for (const candidate of opts.candidates) {
    const label = `${candidate.provider}/${candidate.scope}/${candidate.sku}`;
    let transientLeft = maxTransient;

    for (;;) {
      let createdId: string | undefined;
      try {
        const value = await opts.attempt(candidate, (id) => { createdId = id; });
        trail.push(`${label}: ok`);
        return { ok: true, value, candidate, trail, strandedResourceIds: stranded };
      } catch (e) {
        const cls = classifyProviderError(e);
        lastClass = cls;
        const msg = e instanceof Error ? e.message : String(e);
        trail.push(`${label}: ${cls} — ${msg.slice(0, 200)}`);

        // Reclaim anything this rung created BEFORE we move on. Skipping this
        // is how a ladder turns one orphan into one-per-rung.
        if (createdId) {
          if (opts.reclaim) {
            try {
              await opts.reclaim(candidate, createdId);
              trail.push(`${label}: reclaimed ${createdId}`);
            } catch (re) {
              stranded.push(createdId);
              trail.push(
                `${label}: FAILED to reclaim ${createdId} (${re instanceof Error ? re.message : String(re)}) — STILL BILLING`,
              );
            }
          } else {
            stranded.push(createdId);
            trail.push(`${label}: no reclaim handler for ${createdId} — STILL BILLING`);
          }
        }

        if (shouldAbort(cls)) {
          // Permanent: every other rung would fail identically. Reporting the
          // real cause beats reporting "no capacity anywhere".
          return { ok: false, candidate, trail, failureClass: cls, strandedResourceIds: stranded };
        }
        if (cls === "transient" && transientLeft > 0) {
          transientLeft--;
          await new Promise((r) => setTimeout(r, 1500 + Math.floor(1000 * ((transientLeft + 1) % 3))));
          continue; // same rung
        }
        break; // advance
      }
    }
  }

  return { ok: false, trail, failureClass: lastClass, strandedResourceIds: stranded };
}

/**
 * Human-facing explanation. The distinction that matters to the user is
 * "the market is full" (wait, or pick another region) versus "we have nothing
 * eligible" — the second is OUR failure and must never be dressed up as the
 * first.
 */
export function explainLadderFailure(result: LadderAttemptResult<unknown>): string {
  // An empty trail means no rung was ever attempted — i.e. we had nothing
  // eligible to try. That is OUR failure, and must never be reported as though
  // the market were full.
  if (result.trail.length === 0) return "No eligible provider is configured for this workspace.";
  switch (result.failureClass) {
    case "capacity":
      return "Every available location is out of capacity for this size right now. Try a different region or a different size — your data is safe and untouched.";
    case "quota":
      return "This placement hit an account limit on our side. It has been reported to the operator.";
    case "auth":
      return "A provider credential is invalid or expired. This is a platform problem, not yours — it has been reported.";
    case "bad_request":
      return "This workspace is configured with something the provider rejected (an unknown machine size or image). It has been reported.";
    default:
      return "Placement failed for an unexpected reason. Your data is safe; please retry shortly.";
  }
}
