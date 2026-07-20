// startCoding.ts — the ONE "Start coding" brain. Today the app scatters coding
// across several historical surfaces. New app development now routes through
// Tasks and a selected remote box; older project-specific code routes remain
// only for compatibility. PURE + RN-free (tsx-tested).
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
  | "sandbox" // legacy phone-local code editor; hidden for new app development
  | "remote-task" // real CLI runner on a box (tasks.tsx)
  | "hermes-remote" // phone brain drives an auth-free box (code editor, target=box)
  | "phone-backend" // legacy Hermes SQLite app; hidden for new app development
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
 *  - all new app development requires a remote box: self-hosted Yaver mesh or
 *    Yaver Managed Cloud. The phone is the control/voice/preview surface, not
 *    the development sandbox.
 *  - resolve codingSession("project") and map the
 *    engine×target onto a surface:
 *      target box + engine hermes    → "hermes-remote" (editor, auth-free box)
 *      target box + engine cli-on-box→ "remote-task"   (tasks.tsx runner)
 *    A null engine (no backend at all) → "needs-setup".
 */
export function routeCoding(req: StartCodingRequest): CodingRoute {
  const boxReachable = req.env.online !== false && !!req.env.boxDeviceId;
  if (!boxReachable && !req.preferRemote) {
    return {
      surface: "needs-setup",
      screen: "tasks",
      label: "Choose a remote box",
      reason: "new app development runs on a self-hosted Yaver box or Yaver Managed Cloud; pick or wake a box to start",
    };
  }

  const session = resolveCodingSession("project", req.env, req.prefs);
  const codeScreen = req.slug ? `phone-project/code/${req.slug}` : "tasks";

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
      screen: "tasks",
      label: "Set up a coding backend",
      reason:
        "the selected box has no authorized runner; sign in a runner there or pick Yaver Managed Cloud",
    };
  }

  // Phone-local target. A hermes engine with no usable backend is the "set up
  // something" case. We do not start app development on the phone-local
  // sandbox anymore; the user must select/wake a real box first.
  return {
    surface: "needs-setup",
    session,
    deviceId: null,
    screen: "tasks",
    label: "Choose a remote box",
    reason: "new app development requires a self-hosted Yaver box or Yaver Managed Cloud",
  };
}

/** A one-line, user-facing explanation for the chosen route — for a confirmation
 *  chip in the unified "Start coding" entry. */
export function describeRoute(r: CodingRoute): string {
  switch (r.surface) {
    case "sandbox":
      return `Legacy local editor · ${r.label}`;
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
