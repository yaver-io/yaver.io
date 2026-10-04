import { ENABLE_BOXLESS_UI } from "./launchFlags";

// One explicit execution-mode contract for the mobile app.
//
// `remote-preferred` is deliberately the default. `local-only` is entered only
// after the user selects “No remote box” and suppresses every automatic device
// connection until the user selects a real box again. `auto-fallback` remains
// readable for older installs, but the new picker never enables it implicitly.

export type MobileExecutionMode = "remote-preferred" | "local-only" | "auto-fallback";

export function normalizeMobileExecutionMode(value: unknown): MobileExecutionMode {
  return value === "local-only" || value === "auto-fallback" ? value : "remote-preferred";
}

export function allowsRemoteAutoConnect(mode: MobileExecutionMode): boolean {
  return mode !== "local-only";
}

export function isExplicitRemoteless(mode: MobileExecutionMode): boolean {
  return mode === "local-only";
}

/** Remoteless is an owner-preview capability for now. `isOwner` is computed by
 * the backend allowlist and carried in /auth/validate; no account identity is
 * embedded in the public mobile bundle. */
export function remotelessAccessAllowed(isOwner: boolean | null | undefined): boolean {
  return ENABLE_BOXLESS_UI && isOwner === true;
}

/** A connected box is not required for the owner dogfood account because the
 * task composer can fall through to the on-phone Remoteless lane. Everyone
 * else must have a real remote execution target. */
export function canComposeWithRemoteless(
  hasRemoteConnection: boolean,
  isOwner: boolean | null | undefined,
): boolean {
  return hasRemoteConnection || remotelessAccessAllowed(isOwner);
}

export const REMOTELESS_OWNER_ONLY_MESSAGE =
  "Remoteless is temporarily available only to the Yaver owner account.";

/** Fail closed when an older install persisted local-only before the preview
 * gate existed. */
export function executionModeForAccess(
  value: unknown,
  isOwner: boolean | null | undefined,
): MobileExecutionMode {
  const mode = normalizeMobileExecutionMode(value);
  return mode !== "remote-preferred" && !remotelessAccessAllowed(isOwner)
    ? "remote-preferred"
    : mode;
}
