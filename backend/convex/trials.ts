import { internalAction, internalMutation, internalQuery } from "./_generated/server";
import { internal } from "./_generated/api";
import { v } from "convex/values";

/**
 * ─── Zero-friction trial ────────────────────────────────────────────────────
 *
 * WHY THIS EXISTS: an HN visitor registered for the app and then did nothing.
 * That is not a marketing failure — they were already convinced. It is an
 * ACTIVATION failure: six steps sit between "I want this" and "I see it
 * working" (install, auth, serve, have a project, have a Claude subscription,
 * keep a machine on), and none of them shows the product.
 *
 * A trial box collapses all six. Sign in, and ~90 s later an RN todo app is
 * rendering in the browser with an agent editing it.
 *
 * ─── Why this is safe when an open-ended trial box would not be ─────────────
 *
 * The earlier design decision was "trials get no VM", for good reasons. Those
 * reasons attach to a LONG-LIVED box with public ingress. This one is:
 *
 *   - 60-minute WALL-CLOCK TTL. Not idle-based: an idle timer is defeated by a
 *     keepalive, which converts a bounded cost into an unbounded one.
 *   - EPHEMERAL. No volume, no reserved egress IP, no snapshot. There are no
 *     satellites to outlive the server, so the only thing to reclaim is the
 *     server itself — which the R1 fix and the orphan sweep already cover.
 *   - NO INBOUND PORTS. Costs nothing, because Yaver's transport is already
 *     outbound-registered (the agent dials the relay). Nothing in the demo
 *     needs a listening port on the public internet.
 *   - ONE PER VERIFIED IDENTITY, enforced here, fail-closed.
 *
 * Residual risk, stated rather than wished away: one hour of 2 cores per
 * verified identity making outbound requests. Mining that is worth a fraction
 * of a cent; scraping from a datacenter IP is the real exposure, and
 * CLAUDE.md's rule is unambiguous about the consequence — a datacenter IP
 * hammering third parties gets the WHOLE provider account suspended. Hence the
 * kill switch below and egress policy on the box.
 *
 * Measured economics: €0.037 per 60-minute trial on cpx22. 1,000 trials/month
 * is €36.80. CAC lands between €0.37 and €1.84 against €26.7/mo recurring.
 * A free tier nobody activates is not cheaper — it is worth less.
 */

/** Wall-clock lifetime of a trial box. */
export const TRIAL_MINUTES = Number(process.env.YAVER_TRIAL_MINUTES) || 60;

/**
 * Global kill switch. First control to reach for if abuse appears — one env
 * flip stops every new trial without a deploy. Trials are a growth experiment;
 * a suspended provider account costs more than every trial combined.
 */
export function trialsEnabled(env: Record<string, string | undefined> = process.env): boolean {
  return String(env.YAVER_TRIALS_ENABLED ?? "").toLowerCase() === "true";
}

export type TrialEligibility = { eligible: boolean; reason: string };

/**
 * One trial per verified identity, ever.
 *
 * Deliberately checks EVER, not "currently" — otherwise a user cycles trials
 * indefinitely and it becomes a free compute faucet rather than a demo.
 *
 * ⚠️ Account-merge is the loophole to audit before launch: if two accounts can
 * be merged, a farmer could pool identities. See auth.mergeUserInto.
 */
export const checkEligibility = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }): Promise<TrialEligibility> => {
    if (!trialsEnabled()) {
      return { eligible: false, reason: "trials are disabled (YAVER_TRIALS_ENABLED)" };
    }
    const user = await ctx.db.get(userId);
    if (!user) return { eligible: false, reason: "unknown user" };
    // A trial costs real money, so identity must be verified. Fail closed on
    // anything unproven.
    if (!user.email) {
      return { eligible: false, reason: "unverified identity — trials require a verified account" };
    }
    const machines = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q: any) => q.eq("userId", userId))
      .collect();
    if (machines.some((m: any) => m.isTrial)) {
      return { eligible: false, reason: "this account has already used its trial" };
    }
    if (machines.some((m: any) => !m.isTrial && m.status !== "removed")) {
      // They already have a real workspace — a trial would be pointless and
      // would just cost us a box.
      return { eligible: false, reason: "account already has a workspace" };
    }
    return { eligible: true, reason: "eligible" };
  },
});

/** Trial rows whose wall-clock window has closed. Input to the reaper. */
export const listExpired = internalQuery({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const rows = await ctx.db.query("cloudMachines").collect();
    return rows
      .filter((m: any) => m.isTrial)
      .filter((m: any) => m.status !== "removed" && m.status !== "stopped")
      .filter((m: any) => typeof m.trialExpiresAt === "number" && m.trialExpiresAt <= now)
      .map((m: any) => ({
        machineId: m._id,
        expiredMinutesAgo: Math.round((now - m.trialExpiresAt) / 60_000),
      }));
  },
});

export const markTrial = internalMutation({
  args: { machineId: v.id("cloudMachines"), minutes: v.optional(v.number()) },
  handler: async (ctx, { machineId, minutes }) => {
    const mins = minutes && minutes > 0 ? minutes : TRIAL_MINUTES;
    await ctx.db.patch(machineId, {
      isTrial: true,
      trialExpiresAt: Date.now() + mins * 60_000,
      // Trials never park — they are deleted. Setting this explicitly stops the
      // idle sweep from "parking" a box that has no volume to come back to.
      autoParkEnabled: false,
      updatedAt: Date.now(),
    });
    return { ok: true, expiresAt: Date.now() + mins * 60_000 };
  },
});

/**
 * reapExpiredTrials — delete every trial past its wall clock.
 *
 * Runs LIVE (not dry-run) by default, unlike the wallet meter. A simulated
 * reaper is worse than none: it reports success while boxes keep billing, and
 * the whole cost model of the trial rests on the box actually going away.
 *
 * Schedule on the external cron timers alongside the others; every 5 minutes is
 * appropriate given a 60-minute window.
 */
export const reapExpiredTrials = internalAction({
  args: { dryRun: v.optional(v.boolean()) },
  handler: async (ctx, { dryRun }): Promise<{
    checked: number; reaped: number; failed: string[]; dryRun: boolean;
  }> => {
    // NOTE the inverted default vs meterTick: reaping must be live.
    const sim = dryRun === true;
    const expired = await ctx.runQuery(internal.trials.listExpired, {});
    const failed: string[] = [];
    let reaped = 0;
    for (const row of expired) {
      if (sim) { reaped++; continue; }
      try {
        await ctx.runAction(internal.cloudMachines.destroy, { machineId: row.machineId });
        reaped++;
      } catch (e) {
        // Say so loudly: an un-reaped trial bills indefinitely and is exactly
        // the failure this whole design is built to avoid.
        failed.push(`${row.machineId}: ${e instanceof Error ? e.message : String(e)}`);
      }
    }
    return { checked: expired.length, reaped, failed, dryRun: sim };
  },
});

/**
 * What the trial user gets. Deliberately identical to the paid default class —
 * a trial that runs on a faster box than the one they would buy is a
 * bait-and-switch discovered on day two.
 */
export const TRIAL_PROFILE = {
  machineType: "standard", // 2c/4GB, same as paid default
  // ONE sample project, not a picker. A menu at step zero is another decision
  // before seeing anything, and the point is to remove decisions. RN because
  // it is the flagship path (Hermes, feedback SDK, hot reload) and a todo app
  // because it is recognisable at a glance, so attention goes to Yaver rather
  // than to understanding the sample.
  sampleProject: "yaver-todo-rn",
  // Chrome + WebRTC: no emulator, no device pairing, no GPU. This is why the
  // trial fits on 2c/4GB at all.
  preview: "chrome-webrtc",
  feedbackSdk: "yaver-feedback-react-native",
  // No volume, no reserved IP, no snapshot — nothing that can outlive the box.
  ephemeral: true,
} as const;

/**
 * ─── Trial runtime bootstrap ────────────────────────────────────────────────
 *
 * What the box does on first boot so the user sees a running app in ~90s.
 *
 * The sample is BAKED INTO THE IMAGE, never cloned at boot. A `git clone`
 * during the most fragile 90 seconds of the funnel makes GitHub a hard
 * dependency of the demo — if it is slow or rate-limited, the trial opens on a
 * spinner, which demonstrates the opposite of the claim being made. Baking it
 * also removes the only outbound call the trial would otherwise need before the
 * user has done anything.
 */
export type TrialBootstrap = {
  sampleProject: string;
  /** Absolute path the sample is baked to inside the image. */
  workDir: string;
  /** Ordered steps the agent runs unattended. */
  steps: string[];
  /** Preview strategy — must match the workspace resolver's default. */
  preview: string;
  feedbackSdk: string;
  /** Suggested first prompt. A blank box is its own kind of friction. */
  starterPrompt: string;
};

export function trialBootstrap(): TrialBootstrap {
  return {
    sampleProject: TRIAL_PROFILE.sampleProject,
    // Fixed path inside the trial image. Safe to hardcode ONLY because this is
    // an image we build, not a user machine — the never-hardcode-a-path rule
    // is about other people's boxes.
    workDir: "/home/yaver/todo-rn",
    steps: [
      // Deps are pre-installed in the image for the same reason the repo is
      // baked: npm at boot is a third-party dependency in the critical path.
      "dev-server:start",   // Metro / Expo web target
      "chrome:headless",    // render the web target
      "webrtc:stream",      // stream it to the browser
      "feedback:verify",    // assert the SDK overlay is wired before we claim it
    ],
    preview: TRIAL_PROFILE.preview,
    feedbackSdk: TRIAL_PROFILE.feedbackSdk,
    starterPrompt: "Make completed todos strike through and turn grey.",
  };
}

/**
 * Egress policy for a trial box.
 *
 * Tighter than a paid workspace, because a trial is unattended, anonymous-ish,
 * and the one thing that could cost far more than every trial combined is a
 * datacenter IP abusing a third party — CLAUDE.md is explicit that this
 * suspends the WHOLE provider account, paying customers included.
 *
 * Blocks private ranges (no pivot to host/bridge/metadata) and SMTP (spam is
 * the cheapest abuse to attempt and the most damaging to IP reputation).
 */
export function trialEgressPolicy(): { blockedCidrs: string[]; blockedPorts: number[] } {
  return {
    blockedCidrs: [
      "10.0.0.0/8",
      "172.16.0.0/12",
      "192.168.0.0/16",
      "169.254.0.0/16", // cloud metadata
      "127.0.0.0/8",
    ],
    blockedPorts: [25, 465, 587], // SMTP
  };
}

/**
 * Funnel stages. Instrumenting these is not analytics polish — WITHOUT them a
 * failed trial is indistinguishable from an uninterested visitor, which is
 * exactly the blindness that produced this whole design (someone registered,
 * did nothing, and we could not tell why).
 */
export const TRIAL_FUNNEL_STAGES = [
  "requested",   // user asked for a trial
  "provisioned", // box exists
  "rendered",    // app visible in the browser — the first honest success signal
  "prompted",    // user gave the agent an instruction
  "edited",      // agent changed the code and the preview updated
  "converted",   // kept the workspace, or installed self-hosted
] as const;

export const recordFunnelStage = internalMutation({
  args: {
    userId: v.id("users"),
    stage: v.string(),
    machineId: v.optional(v.id("cloudMachines")),
  },
  handler: async (ctx, { userId, stage, machineId }) => {
    // platformConfig keyed rows — counters only, no prompts, no output, no
    // paths. Same privacy class as the existing activity summaries.
    const key = `trialFunnel:${stage}`;
    const existing = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q: any) => q.eq("key", key))
      .unique();
    const next = String(Number(existing?.value ?? "0") + 1);
    if (existing) await ctx.db.patch(existing._id, { value: next, updatedAt: Date.now() });
    else await ctx.db.insert("platformConfig", { key, value: next, updatedAt: Date.now() });
    void userId; void machineId;
    return { ok: true, stage, total: Number(next) };
  },
});

/** Funnel counts, so drop-off is visible rather than inferred. */
export const funnelSnapshot = internalQuery({
  args: {},
  handler: async (ctx) => {
    const out: Record<string, number> = {};
    for (const stage of TRIAL_FUNNEL_STAGES) {
      const row = await ctx.db
        .query("platformConfig")
        .withIndex("by_key", (q: any) => q.eq("key", `trialFunnel:${stage}`))
        .unique();
      out[stage] = Number(row?.value ?? "0");
    }
    return out;
  },
});
