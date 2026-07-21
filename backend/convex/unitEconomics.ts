/**
 * ─── Unit-economics guard ───────────────────────────────────────────────────
 *
 * Owner directive, 2026-07-21: **"we won't have any business with 16% gross at
 * all."** That is a product rule, so it belongs in code where a change can trip
 * it — not in a doc nobody reads before adding a SKU.
 *
 * The number that provoked it: Relay Pro at $9/mo on a DEDICATED Hetzner box
 * (cax11, €6.99/mo, necessarily always-on) is **16% gross**. A relay cannot
 * scale to zero — it is useless when off — so that margin is structural, not a
 * tuning problem. One support ticket erases it.
 *
 * Everything here is pure and cheap so it can run as a PREFLIGHT: before a plan
 * ships, before a SKU becomes default, before a park mode is offered. Money
 * facts are measured (see docs/architecture/cloud-unit-economics.md), never
 * guessed — an unsourced cost number is worse than none, because it launders a
 * loss into a spreadsheet.
 */

/**
 * Minimum acceptable gross margin. Below this we do not ship the line.
 *
 * Owner directive 2026-07-21: **70% in general, not 23%.** Deliberately strict —
 * every configuration that has failed this floor so far failed because of an
 * ALWAYS-ON cost (a dedicated relay box, a dedicated standby box), and an
 * always-on cost cannot be quota'd away. Failing the check is the signal to
 * AMORTISE the always-on thing across tenants, not to lower the floor.
 */
export const MIN_GROSS_MARGIN = Number(process.env.YAVER_MIN_GROSS_MARGIN) || 0.7;

/** What we actually want, and what pricing should be designed toward. */
export const TARGET_GROSS_MARGIN = Number(process.env.YAVER_TARGET_GROSS_MARGIN) || 0.8;

/** Hetzner Volume, gross € per GB per month (measured 2026-07-21). */
export const VOLUME_EUR_PER_GB_MONTH = 0.044;

/** Reserved primary IPv4, gross € per month (measured 2026-07-21). */
export const EGRESS_IP_EUR_PER_MONTH = 1.2;

const HOURS_PER_MONTH = 730;

export type ParkMode = "deep" | "standby" | "reserved";

export type CostInputs = {
  /** Gross €/hour for the workspace's own server type. */
  hourlyEur: number;
  /** Expected ACTIVE hours per month. */
  activeHours: number;
  volumeGb?: number;
  hasEgressIp?: boolean;
  parkMode?: ParkMode;
  /**
   * Gross €/hour of the smallest server kept alive in "standby" park. Standby's
   * whole point is that this is much cheaper than the workspace's own type.
   */
  standbyHourlyEur?: number;
};

export type CostBreakdown = {
  computeEur: number;
  standbyEur: number;
  volumeEur: number;
  egressIpEur: number;
  totalEur: number;
};

/**
 * Monthly provider cost. Note what is NOT optional: the volume and the reserved
 * IP bill while the box is parked, so they are a FLOOR that no amount of
 * scale-to-zero removes. Forgetting that floor is how "parked costs nothing"
 * becomes a wrong business model.
 */
export function estimateMonthlyCostEur(input: CostInputs): CostBreakdown {
  const mode: ParkMode = input.parkMode ?? "deep";
  const activeHours = mode === "reserved"
    ? HOURS_PER_MONTH // never parks — billed continuously
    : Math.max(0, Math.min(input.activeHours, HOURS_PER_MONTH));

  const computeEur = input.hourlyEur * activeHours;

  // Standby keeps a small server alive for the hours the workspace is NOT
  // active. That is the price of "never unreachable".
  const standbyEur =
    mode === "standby" && input.standbyHourlyEur
      ? input.standbyHourlyEur * Math.max(0, HOURS_PER_MONTH - activeHours)
      : 0;

  const volumeEur = (input.volumeGb ?? 0) * VOLUME_EUR_PER_GB_MONTH;
  const egressIpEur = input.hasEgressIp ? EGRESS_IP_EUR_PER_MONTH : 0;

  return {
    computeEur,
    standbyEur,
    volumeEur,
    egressIpEur,
    totalEur: computeEur + standbyEur + volumeEur + egressIpEur,
  };
}

export type ViabilityVerdict = {
  viable: boolean;
  margin: number;
  cost: CostBreakdown;
  revenueEur: number;
  /** Plain-language reason, suitable for an operator alert. */
  reason: string;
};

/**
 * Would this configuration clear the margin floor?
 *
 * `revenueEur` is the MONTHLY revenue attributable to this workspace — i.e. the
 * plan price, converted, minus anything already spent serving the same user
 * elsewhere. Be conservative: overstating revenue here is how a losing line
 * gets shipped.
 */
export function assessViability(revenueEur: number, input: CostInputs): ViabilityVerdict {
  const cost = estimateMonthlyCostEur(input);
  if (revenueEur <= 0) {
    return {
      viable: false,
      margin: -Infinity,
      cost,
      revenueEur,
      reason: "no revenue attributed to this workspace — a free box that costs money is not a plan",
    };
  }
  const margin = (revenueEur - cost.totalEur) / revenueEur;
  if (margin < MIN_GROSS_MARGIN) {
    return {
      viable: false,
      margin,
      cost,
      revenueEur,
      reason:
        `gross margin ${(margin * 100).toFixed(0)}% is below the ${(MIN_GROSS_MARGIN * 100).toFixed(0)}% floor ` +
        `(revenue €${revenueEur.toFixed(2)} vs cost €${cost.totalEur.toFixed(2)}: ` +
        `compute €${cost.computeEur.toFixed(2)}, standby €${cost.standbyEur.toFixed(2)}, ` +
        `volume €${cost.volumeEur.toFixed(2)}, egress IP €${cost.egressIpEur.toFixed(2)})`,
    };
  }
  return {
    viable: true,
    margin,
    cost,
    revenueEur,
    reason: `gross margin ${(margin * 100).toFixed(0)}%`,
  };
}

/**
 * Included hours that still clear the TARGET margin for a given SKU.
 *
 * This is the lever that makes expensive SKUs safe: `cpx51` costs ~8x `cx33`,
 * so giving both the same allowance guarantees a loss on the heavy plan. Scale
 * the allowance by cost instead of hand-picking a number per plan.
 */
export function maxIncludedHoursForTarget(
  revenueEur: number,
  hourlyEur: number,
  fixedEur: number,
  targetMargin: number = TARGET_GROSS_MARGIN,
): number {
  if (hourlyEur <= 0) return HOURS_PER_MONTH;
  const budget = revenueEur * (1 - targetMargin) - fixedEur;
  if (budget <= 0) return 0;
  return Math.max(0, Math.min(HOURS_PER_MONTH, Math.floor(budget / hourlyEur)));
}

/**
 * Is a DEDICATED always-on box viable at this price?
 *
 * The Relay Pro question, generalised. Answers false for $9 + cax11 (16%),
 * which is exactly the configuration this module exists to prevent shipping.
 * The remedy is not a price rise — it is to SHARE the box: the relay is
 * pass-through, authorizes nothing, and free-vs-Pro is explicitly not a
 * security boundary, so multi-tenancy costs no security and multiplies margin.
 */
export function dedicatedAlwaysOnViable(revenueEur: number, monthlyBoxEur: number): ViabilityVerdict {
  return assessViability(revenueEur, {
    hourlyEur: monthlyBoxEur / HOURS_PER_MONTH,
    activeHours: HOURS_PER_MONTH,
    parkMode: "reserved",
  });
}

/**
 * Per-user cost of a SHARED always-on box — the fix for the above.
 * `tenantsPerBox` must be a number the relay can actually sustain at its QoS
 * floor, not an aspiration; oversubscribing converts a margin win into an
 * outage.
 */
export function sharedPerUserEur(monthlyBoxEur: number, tenantsPerBox: number): number {
  if (tenantsPerBox <= 0) return monthlyBoxEur;
  return monthlyBoxEur / tenantsPerBox;
}

/* ─── Allowance accounting in STANDARD-HOURS ─────────────────────────────────
 *
 * The plan is "$29 buys 120 standard-hours", not "$29 buys a machine". A bigger
 * box burns the same budget faster, which keeps margin roughly constant no
 * matter what the user picks — the allowance cannot be gamed into a loss by
 * upgrading.
 *
 * Everything below works in standard-hours and converts to WALL-CLOCK hours for
 * display, because those are different numbers and showing the wrong one is how
 * a user feels cheated: someone on the Heavy class with 60 standard-hours left
 * has ~17 real hours, and telling them "60 hours remaining" is a lie they will
 * discover at the worst moment.
 */

/** Included standard-hours per period. */
export const STANDARD_HOURS_INCLUDED = Number(process.env.YAVER_INCLUDED_HOURS) || 120;

/**
 * Burn multiplier for a class, DERIVED from real prices — never hardcoded, so
 * it self-corrects when provider pricing or availability shifts. A hardcoded
 * table silently becomes wrong the moment a SKU sells out and the substitute
 * costs 4x (which is exactly what happened on 2026-07-21).
 */
export function burnRateForHourly(hourlyEur: number, baselineHourlyEur: number): number {
  if (!(baselineHourlyEur > 0) || !(hourlyEur > 0)) return 1;
  // Round to 1 decimal: users can reason about "1.8x", not "1.8288x".
  return Math.max(0.1, Math.round((hourlyEur / baselineHourlyEur) * 10) / 10);
}

export type AllowanceView = {
  /** Budget units left this period. */
  standardHoursRemaining: number;
  /** REAL hours the user gets on their current class. This is what to display. */
  wallClockHoursRemaining: number;
  burnRate: number;
  periodEndsAt?: number;
  exhausted: boolean;
};

/**
 * What to show the user. `usedStandardHours` is what the meter accumulated —
 * already burn-adjusted at record time, so this function must NOT scale it
 * again (double-scaling is the classic bug here and it under-reports the
 * remaining balance, which reads as us stealing hours).
 */
export function allowanceView(args: {
  includedStandardHours?: number;
  usedStandardHours: number;
  hourlyEur: number;
  baselineHourlyEur: number;
  periodEndsAt?: number;
}): AllowanceView {
  const included = args.includedStandardHours ?? STANDARD_HOURS_INCLUDED;
  const remaining = Math.max(0, included - Math.max(0, args.usedStandardHours));
  const burn = burnRateForHourly(args.hourlyEur, args.baselineHourlyEur);
  return {
    standardHoursRemaining: Math.round(remaining * 10) / 10,
    wallClockHoursRemaining: Math.round((remaining / burn) * 10) / 10,
    burnRate: burn,
    periodEndsAt: args.periodEndsAt,
    exhausted: remaining <= 0,
  };
}

/**
 * Honest preview of an upgrade, for the confirmation dialog.
 *
 * An upgrade does NOT take hours away — the budget is unchanged — but it does
 * make the remaining budget cover fewer real hours. Showing only the new
 * machine's specs and not this number is how an upgrade turns into a support
 * ticket.
 */
export function previewClassChange(args: {
  standardHoursRemaining: number;
  currentHourlyEur: number;
  targetHourlyEur: number;
  baselineHourlyEur: number;
}): {
  fromBurn: number;
  toBurn: number;
  wallClockHoursBefore: number;
  wallClockHoursAfter: number;
  /** Negative when upgrading — real hours the user trades for more machine. */
  wallClockHoursDelta: number;
} {
  const fromBurn = burnRateForHourly(args.currentHourlyEur, args.baselineHourlyEur);
  const toBurn = burnRateForHourly(args.targetHourlyEur, args.baselineHourlyEur);
  const before = args.standardHoursRemaining / fromBurn;
  const after = args.standardHoursRemaining / toBurn;
  return {
    fromBurn,
    toBurn,
    wallClockHoursBefore: Math.round(before * 10) / 10,
    wallClockHoursAfter: Math.round(after * 10) / 10,
    wallClockHoursDelta: Math.round((after - before) * 10) / 10,
  };
}

/** Standard-hours consumed by `seconds` of wall-clock uptime on a class. */
export function standardHoursForUptime(
  seconds: number,
  hourlyEur: number,
  baselineHourlyEur: number,
): number {
  const hours = Math.max(0, seconds) / 3600;
  return hours * burnRateForHourly(hourlyEur, baselineHourlyEur);
}
