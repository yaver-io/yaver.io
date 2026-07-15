// startCoding.ts — the ONE "Start coding" brain. Today the app scatters coding
// across three disjoint surfaces (the audit's #1 UX gap): tasks.tsx (remote
// runner), apps.tsx/phone-projects (Hermes SQLite backend), and sandbox-ai.tsx
// (phone-local code). A new user must guess which tab. This module collapses
// that into one decision: given what the user wants + the live environment,
// route to exactly one surface and explain why. PURE + RN-free (tsx-tested).
//
// It composes codingSession.ts (engine×target) for the CODE surfaces and adds
// the one axis codingSession doesn't model: "is this a data/backend app?" →
// the Hermes phone-backend (apps.tsx) instead of a code editor.

import {
  resolveCodingSession,
  sessionEndpointDeviceId,
  phoneCanDriveHermes,
  type CodingEnv,
  type CodingPrefs,
  type CodingSession,
} from "./codingSession";

/** Where the request lands. Each maps to a concrete screen. */
export type CodingSurface =
  | "sandbox" // phone-local code editor (sandbox-ai / phone-project/code)
  | "remote-task" // real CLI runner on a box (tasks.tsx)
  | "hermes-remote" // phone brain drives an auth-free box (code editor, target=box)
  | "phone-backend" // Hermes SQLite app (apps.tsx / phone-projects)
  | "needs-setup"; // nothing usable — prompt the user

/** What the user is making. "backend" = a data/CRUD/fullstack app that wants the
 *  Hermes phone-backend; "code" = a source project; "auto" = let policy decide
 *  (defaults to code — the create wizard asks the backend question explicitly). */
export type AppKind = "code" | "backend" | "auto";

export interface StartCodingRequest {
  /** Natural-language ask, carried through to whichever surface opens. */
  prompt?: string;
  /** Resume an existing project by slug (skips some inference). */
  slug?: string;
  appKind?: AppKind;
  env: CodingEnv;
  prefs?: CodingPrefs;
  /** Force the work onto a machine (real toolchain) even if a phone path exists
   *  — e.g. the user explicitly picked "run on my box". */
  preferRemote?: boolean;
}

export interface CodingRoute {
  surface: CodingSurface;
  /** The resolved engine×target for code surfaces (undefined for phone-backend). */
  session?: CodingSession;
  /** Box device id when the work runs on a box; null/undefined for phone-local. */
  deviceId?: string | null;
  /** The Expo Router screen to navigate to. */
  screen: string;
  label: string;
  reason: string;
}

/**
 * Route a coding request to a single surface. Deterministic; no I/O.
 *
 *  - appKind "backend" → the Hermes phone-backend (apps.tsx). The DB-app path.
 *  - otherwise resolve codingSession(intent) where intent is "project" when a box
 *    is reachable or the user preferRemote, else "sandbox", then map the
 *    engine×target onto a surface:
 *      target phone                  → "sandbox"      (local editor)
 *      target box + engine hermes    → "hermes-remote" (editor, auth-free box)
 *      target box + engine cli-on-box→ "remote-task"   (tasks.tsx runner)
 *    A null engine (no backend at all) → "needs-setup".
 */
export function routeCoding(req: StartCodingRequest): CodingRoute {
  if (req.appKind === "backend") {
    return {
      surface: "phone-backend",
      screen: req.slug ? `phone-project/${req.slug}` : "phone-projects",
      label: "Hermes app (data + backend)",
      reason: "a data/CRUD app — created on the on-device Hermes backend, promotable to Convex/Supabase later",
    };
  }

  const boxReachable = req.env.online !== false && !!req.env.boxDeviceId;
  const intent = boxReachable || req.preferRemote ? "project" : "sandbox";
  const session = resolveCodingSession(intent, req.env, req.prefs);
  const codeScreen = req.slug ? `phone-project/code/${req.slug}` : "sandbox-ai";

  // Box target.
  if (session.target.kind === "box") {
    if (session.engine.kind === "cli-on-box") {
      return {
        surface: "remote-task",
        session,
        deviceId: sessionEndpointDeviceId(session),
        screen: "tasks",
        label: session.label,
        reason: "the box runs the real CLI runner; opening the task screen targeted at it",
      };
    }
    // Hermes engine driving the box (auth-free remote) — but ONLY if the phone
    // actually has a compliant in-app backend. resolveCodingSession returns a
    // hermes *placeholder* for the "nothing usable" case; that's a setup prompt,
    // not a real hermes-remote (and never the subscription mimic).
    if (phoneCanDriveHermes(req.env, req.prefs)) {
      return {
        surface: "hermes-remote",
        session,
        deviceId: sessionEndpointDeviceId(session),
        screen: codeScreen,
        label: session.label,
        reason: session.reason,
      };
    }
    return {
      surface: "needs-setup",
      session,
      deviceId: sessionEndpointDeviceId(session),
      screen: "sandbox-ai",
      label: "Set up a coding backend",
      reason:
        "the box has no authorized runner and the phone has no compliant backend (GLM/BYO/on-device) to drive it — add one",
    };
  }

  // Phone-local target. A hermes engine with no usable backend is the "set up
  // something" case (resolveCodingSession returns a placeholder subscription
  // engine but the availability is empty).
  if (!phoneHasUsableEngine(req)) {
    return {
      surface: "needs-setup",
      session,
      screen: "sandbox-ai",
      label: "Set up a coding backend",
      reason: "no on-device model, no mirrored plan, and no box — add one to start coding",
    };
  }

  return {
    surface: "sandbox",
    session,
    deviceId: null,
    screen: codeScreen,
    label: session.label,
    reason: session.reason,
  };
}

/** True when the phone can actually run SOMETHING locally: the Android proot CLI,
 *  an on-device model, a mirrored plan, or a configured fallback backend. Mirrors the inputs
 *  codingSession uses, so the two never disagree. */
function phoneHasUsableEngine(req: StartCodingRequest): boolean {
  const { env } = req;
  if (env.platform === "android" && env.onDeviceCliReady && req.prefs?.onDeviceEngine !== "hermes") return true;
  const b = env.backend;
  return !!(b.localModelReady || b.claudeSubscription || b.anthropicKey || b.openaiKey || b.glmKey);
}

/** A one-line, user-facing explanation for the chosen route — for a confirmation
 *  chip in the unified "Start coding" entry. */
export function describeRoute(r: CodingRoute): string {
  switch (r.surface) {
    case "sandbox":
      return `Edit on this phone · ${r.label}`;
    case "hermes-remote":
      return `Drive your box from the phone (no box auth) · ${r.label}`;
    case "remote-task":
      return `Run on your box · ${r.label}`;
    case "phone-backend":
      return r.label;
    case "needs-setup":
      return r.reason;
  }
}
