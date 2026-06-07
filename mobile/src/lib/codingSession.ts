// codingSession.ts — the ONE place that decides *how* a coding request runs,
// unifying the Mobile Sandbox, remote-dev tasks, and the Android on-device CLI
// behind a single pure policy. PURE + RN-free (tsx-tested).
//
// The mistake the rest of the app keeps making is collapsing two INDEPENDENT
// dimensions into one "backend" choice. Separate them:
//
//   ENGINE — who runs the agent brain (calls the LLM, drives the tool loop):
//     cli-on-device   the REAL claude/codex/opencode binary on THIS phone,
//                     inside the Android proot rootfs (sandbox_proot.go). Authed
//                     by the desktop credential mirror bound into the rootfs.
//     cli-on-box      the REAL CLI on a paired box over /ws/terminal. The box
//                     must carry its OWN runner auth (mirror/login) — this is the
//                     per-box "auth overhead" we want to avoid when we can.
//     hermes          the phone's Hermes-native agent loop (yaverAgentRunner +
//                     localAgent/orchestrator), model chosen by codingBackend.ts.
//                     Holds exactly ONE credential (the mirrored Max/Pro token or
//                     a local GGUF) and can drive a remote target itself.
//
//   TARGET — where files + shell commands actually execute:
//     phone           the phone-local sandbox tree (Mobile Sandbox) or, under
//                     proot, the rootfs project dir.
//     box:<id>        a remote box's filesystem + shell over the exec channel.
//
// The cells that matter:
//   (cli-on-device, phone)  Android: real Claude Code ON the phone, $0 on plan.
//   (cli-on-box,    box)    classic remote dev — box is authed.
//   (hermes,        phone)  Mobile Sandbox standalone, model via codingBackend.
//   (hermes,        box)    *** Hermes-only remote *** — the phone's single token
//                           drives edits+builds on an AUTH-FREE box. This is the
//                           "reduce claude/codex auth overhead" topology: you
//                           mirror the plan token ONCE to the phone instead of to
//                           every box.
//
// codingBackend.ts owns the MODEL/auth sub-decision; it only applies when the
// engine is `hermes`. The CLI engines carry their own auth and ignore it.

import {
  resolveBackend,
  type CodingBackendAvailability,
  type CodingBackendId,
  type CodingBackendPref,
} from "./codingBackend";

/** The coding-agent CLIs Yaver ships first-class support for. Mirrors the
 *  agent's `supportedRunnerIDs` (tasks.go) — keep in sync. */
export type RunnerId = "claude" | "codex" | "opencode";

export const SUPPORTED_RUNNERS: readonly RunnerId[] = ["claude", "codex", "opencode"] as const;

export type CodingEngine =
  | { kind: "cli-on-device"; runner: RunnerId }
  | { kind: "cli-on-box"; runner: RunnerId; deviceId: string }
  | { kind: "hermes"; backend: CodingBackendId };

export type ExecTarget = { kind: "phone" } | { kind: "box"; deviceId: string };

/** What the user is trying to do, which constrains the topology:
 *   - "sandbox" → edit phone-local files (the Mobile Sandbox). Never needs a box.
 *   - "project" → real dev work; prefers a box for a full toolchain (build/test),
 *     falls back to the phone when none is reachable. */
export type CodingIntent = "sandbox" | "project";

export interface CodingSession {
  engine: CodingEngine;
  target: ExecTarget;
  /** Short picker label, e.g. "Claude Code · this phone" / "Claude (plan) → box". */
  label: string;
  /** Why the policy chose this — surfaced in the UI for transparency. */
  reason: string;
  /** True when the chosen topology needs NO runner credentials on the box (the
   *  phone's Hermes engine drives it). The auth-overhead win. */
  boxAuthFree: boolean;
}

/** Live environment the policy resolves against. The RN layer fills this in. */
export interface CodingEnv {
  platform: "ios" | "android" | "web";
  online?: boolean;

  /** Android proot agent is up AND a runner CLI is installed in the rootfs. */
  onDeviceCliReady?: boolean;
  /** Which CLI the rootfs has (defaults to claude when ready but unspecified). */
  onDeviceRunner?: RunnerId;

  /** A box we're connected to / can attach to right now. */
  boxDeviceId?: string | null;
  /** That box already has a runner authed (mirror/login done). */
  boxRunnerReady?: boolean;
  /** Which runner the box has authed (defaults to claude). */
  boxRunner?: RunnerId;

  /** Model availability for the Hermes engine (from codingBackend.ts). */
  backend: CodingBackendAvailability;
}

/** User overrides. All optional; "auto" everywhere reproduces the default policy. */
export interface CodingPrefs {
  /** Force the model when the engine ends up being Hermes. */
  backend?: CodingBackendPref;
  /** For a remote target: prefer driving it from the phone (hermes, auth-free
   *  box) or running the real CLI on the box.
   *    "auto"   → hermes-remote when the phone has a usable backend, else cli-on-box.
   *    "hermes" → always hermes-remote (box stays auth-free), even if box is authed.
   *    "cli"    → always the box's own CLI (requires box auth). */
  remoteEngine?: "auto" | "hermes" | "cli";
  /** Force the on-device runner choice (claude/codex/opencode). */
  runner?: RunnerId;
  /** On a phone-local target, choose the engine:
   *    "auto"   → the real CLI in the proot rootfs when it's ready (richest,
   *               Android only), else the in-app Hermes loop.
   *    "cli"    → force the proot CLI (Android; falls back to Hermes if no rootfs).
   *    "hermes" → force the IN-APP Hermes loop even on Android. Lighter than
   *               proot — no rootfs, no ptrace, no backgrounding-survival risk —
   *               and still $0 on the mirrored plan. The universal path that
   *               works identically on iOS and Android. */
  onDeviceEngine?: "auto" | "cli" | "hermes";
}

/** Whether a phone-local session should run the real proot CLI (Android, rootfs
 *  ready, not overridden to Hermes) vs the in-app Hermes loop. Hermes is always
 *  available; the CLI is the Android power path. */
function preferOnDeviceCli(env: CodingEnv, prefs: CodingPrefs): boolean {
  if (prefs.onDeviceEngine === "hermes") return false;
  return env.platform === "android" && !!env.onDeviceCliReady;
}

function hermesBackend(env: CodingEnv, prefs: CodingPrefs): CodingBackendId | null {
  return resolveBackend(prefs.backend ?? "auto", env.backend).id;
}

/** Does the phone have ANY usable Hermes backend (local model OR a cloud token)?
 *  This gates whether we can offer an auth-free remote box. */
export function phoneCanDriveHermes(env: CodingEnv, prefs: CodingPrefs = {}): boolean {
  return hermesBackend(env, prefs) != null;
}

function labelForBackend(id: CodingBackendId): string {
  switch (id) {
    case "subscription":
      return "Claude (your plan)";
    case "local":
      return "On-device model";
    case "anthropic":
      return "Claude (BYO key)";
    case "openai":
      return "OpenAI (BYO key)";
    case "glm":
      return "GLM (BYO key)";
  }
}

/**
 * Resolve the coding-session topology. Deterministic; no I/O.
 *
 * Policy, in order:
 *
 *  SANDBOX (phone-local files):
 *    1. Android + on-device CLI ready → real CLI on the phone (richest, $0/plan).
 *    2. else → Hermes on the phone (model via codingBackend). null engine if the
 *       phone has no usable backend (UI prompts to mirror a plan / add a key /
 *       download a model).
 *
 *  PROJECT (real dev; prefer a box for the toolchain):
 *    1. A box is reachable:
 *       a. remoteEngine != "cli" AND the phone can drive Hermes → HERMES-REMOTE:
 *          (hermes, box). Box is auth-free. The default — one token on the phone
 *          beats mirroring creds to every box.
 *       b. else if the box has its OWN runner authed → (cli-on-box, box).
 *       c. else if the phone can drive Hermes → hermes-remote (only path left).
 *       d. else → engine=null on a box target (UI: "authorize a runner on the box
 *          or set up a phone backend").
 *    2. No box reachable:
 *       a. Android + on-device CLI ready → (cli-on-device, phone). Real CLI, but
 *          a phone-local project — build/native still wants a box.
 *       b. else → (hermes, phone) if a backend exists, else null. "Edits here,
 *          reaches for a machine to compile" (same remote-first brain as brain.ts).
 */
export function resolveCodingSession(
  intent: CodingIntent,
  env: CodingEnv,
  prefs: CodingPrefs = {},
): CodingSession {
  const boxReachable = env.online !== false && !!env.boxDeviceId;
  const onDeviceRunner: RunnerId = prefs.runner ?? env.onDeviceRunner ?? "claude";
  const boxRunner: RunnerId = prefs.runner ?? env.boxRunner ?? "claude";

  // ── SANDBOX ──────────────────────────────────────────────────────────
  if (intent === "sandbox") {
    if (preferOnDeviceCli(env, prefs)) {
      return {
        engine: { kind: "cli-on-device", runner: onDeviceRunner },
        target: { kind: "phone" },
        label: `${runnerLabel(onDeviceRunner)} · this phone`,
        reason: "Android sandbox runs the real CLI in the on-device rootfs (full agent, $0 on your plan)",
        boxAuthFree: true,
      };
    }
    const b = hermesBackend(env, prefs);
    return {
      engine: b ? { kind: "hermes", backend: b } : { kind: "hermes", backend: "glm" },
      target: { kind: "phone" },
      label: b ? `${labelForBackend(b)} · sandbox` : "Sandbox — no backend",
      reason: b
        ? "Hermes agent loop editing the phone-local sandbox"
        : "no on-device model, plan token, or API key — set one up to code the sandbox",
      boxAuthFree: true,
    };
  }

  // ── PROJECT ──────────────────────────────────────────────────────────
  if (boxReachable) {
    const deviceId = env.boxDeviceId!;
    const box: ExecTarget = { kind: "box", deviceId };
    const b = hermesBackend(env, prefs);

    // (a) Default: drive the box from the phone's single token — box auth-free.
    if (prefs.remoteEngine !== "cli" && b) {
      return {
        engine: { kind: "hermes", backend: b },
        target: box,
        label: `${labelForBackend(b)} → box`,
        reason:
          "Hermes-only remote: the phone's token drives edits + build/test on the box, so the box needs no claude/codex auth",
        boxAuthFree: true,
      };
    }
    // (b) Box runs its own authed CLI.
    if (env.boxRunnerReady) {
      return {
        engine: { kind: "cli-on-box", runner: boxRunner, deviceId },
        target: box,
        label: `${runnerLabel(boxRunner)} on box`,
        reason: "the box has an authorized runner; running the real CLI there",
        boxAuthFree: false,
      };
    }
    // (c) Phone backend is the only thing that can drive it.
    if (b) {
      return {
        engine: { kind: "hermes", backend: b },
        target: box,
        label: `${labelForBackend(b)} → box`,
        reason: "box has no runner authed; driving it from the phone's backend (auth-free box)",
        boxAuthFree: true,
      };
    }
    // (d) Nothing can drive it.
    return {
      engine: { kind: "hermes", backend: "glm" },
      target: box,
      label: "Box — no backend",
      reason: "authorize a runner on the box, or set up a phone backend to drive it auth-free",
      boxAuthFree: true,
    };
  }

  // No box reachable.
  if (preferOnDeviceCli(env, prefs)) {
    return {
      engine: { kind: "cli-on-device", runner: onDeviceRunner },
      target: { kind: "phone" },
      label: `${runnerLabel(onDeviceRunner)} · this phone`,
      reason: "no box reachable; running the real CLI on-device against a phone-local project",
      boxAuthFree: true,
    };
  }
  const b = hermesBackend(env, prefs);
  return {
    engine: b ? { kind: "hermes", backend: b } : { kind: "hermes", backend: "glm" },
    target: { kind: "phone" },
    label: b ? `${labelForBackend(b)} · this phone` : "No backend",
    reason: b
      ? "no box reachable; Hermes loop edits phone-local files and reaches for a machine to build"
      : "no box, no on-device model, no token — set up a backend or pair a box",
    boxAuthFree: true,
  };
}

function runnerLabel(r: RunnerId): string {
  switch (r) {
    case "claude":
      return "Claude Code";
    case "codex":
      return "Codex";
    case "opencode":
      return "opencode";
  }
}

/** True when the session runs the real CLI (on-device or on a box) rather than
 *  the Hermes loop — used by the UI to decide between an xterm view and the
 *  sandbox editor. */
export function isCliSession(s: CodingSession): boolean {
  return s.engine.kind === "cli-on-device" || s.engine.kind === "cli-on-box";
}

/** The wire endpoint the terminal/exec client should connect to:
 *   - phone target  → loopback agent ("127.0.0.1:18080" — the synthetic
 *     "This phone" device).
 *   - box target    → connectionManager.clientFor(deviceId).
 *  Returns null deviceId for the loopback case so the caller picks loopback. */
export function sessionEndpointDeviceId(s: CodingSession): string | null {
  return s.target.kind === "box" ? s.target.deviceId : null;
}
