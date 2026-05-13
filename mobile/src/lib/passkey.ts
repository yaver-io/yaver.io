// passkey.ts — mobile-side WebAuthn flow.
//
// Three calls round-trip with the same Convex actions the web client
// hits (backend/convex/passkeys.ts):
//   passkeySignup     → /auth/passkey/signup/start + /finish (new user)
//   passkeySignin     → /auth/passkey/login/start  + /finish (any user
//                       who already enrolled a passkey, regardless of
//                       how they originally signed up)
//   passkeyEnroll     → /auth/passkey/register/start + /finish (signed-
//                       in OAuth/email/passkey user adds a new passkey
//                       to their existing account)
//
// All three use react-native-passkey for the native ASAuthorization /
// CredentialManager dance, then POST the structured result back to
// Convex for verification + session minting.
//
// Trust gate: the server enforces every check (origin allowlist,
// challenge match, RP ID match, attestation/assertion verification,
// counter handling). The mobile side is purely a UI for the platform
// keychain — failure modes are surfaced as typed errors so the auth
// screen can render the right message ("cancelled", "wrong account",
// "no passkey enrolled", network error).

import { Passkey } from "react-native-passkey";

const RP_ID = "yaver.io";
const PASSKEY_BASE_URL = "https://yaver.io";

export type PasskeyAuthResult = {
  token: string;
  userId: string;
  userDocId: string;
  email: string | null;
};

export class PasskeyCancelled extends Error {
  constructor() {
    super("Passkey prompt cancelled.");
    this.name = "PasskeyCancelled";
  }
}

/**
 * Thrown when the platform dismissed the passkey sheet without showing
 * it — usually because no matching credential exists for rpId="yaver.io"
 * in the platform keychain. iOS folds this into the same error code as
 * "user cancelled" (ASAuthorizationError.canceled = 1001); we distinguish
 * by elapsed time inside `passkeySignin` (a fast dismiss with no UI is
 * NoCredential; a slow one is genuine user cancel).
 */
export class PasskeyNoCredential extends Error {
  constructor() {
    super("No passkey registered for this account on this device.");
    this.name = "PasskeyNoCredential";
  }
}

/**
 * Thrown when the platform reports a configuration error — entitlements
 * mismatch, AASA file unreachable, or assetlinks.json missing. Surfaces
 * to the UI as a developer-actionable error, NOT a silent revert.
 */
export class PasskeyMisconfigured extends Error {
  constructor(message: string) {
    super(message || "Passkey support is misconfigured on this device.");
    this.name = "PasskeyMisconfigured";
  }
}

export class PasskeyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PasskeyError";
  }
}

/**
 * Whether the device claims to support passkeys at all. iOS 16+ +
 * Android 14+ generally return true. Older devices fall back to other
 * auth flows transparently.
 */
export function isPasskeySupported(): boolean {
  try {
    return typeof (Passkey as any).isSupported === "function" ? (Passkey as any).isSupported() : true;
  } catch {
    return false;
  }
}

/**
 * Sign-in with an existing passkey. The platform shows whichever
 * credential is associated with rpId="yaver.io" — discoverable
 * credentials make this a username-less flow.
 */
export async function passkeySignin(convexBaseUrl: string): Promise<PasskeyAuthResult> {
  const startRes = await fetch(`${convexBaseUrl}/auth/passkey/login/start`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: PASSKEY_BASE_URL },
    body: "{}",
  });
  if (!startRes.ok) throw new PasskeyError(await startRes.text());
  const { options } = await startRes.json();

  let assertion;
  const sheetStartedAt = Date.now();
  try {
    assertion = await Passkey.get({ ...options, rpId: options.rpId || RP_ID });
  } catch (err: any) {
    throw classifyPasskeyError(err, sheetStartedAt, "sign-in");
  }

  const finishRes = await fetch(`${convexBaseUrl}/auth/passkey/login/finish`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: PASSKEY_BASE_URL },
    body: JSON.stringify({ response: assertion }),
  });
  if (!finishRes.ok) throw new PasskeyError(await finishRes.text());
  return (await finishRes.json()) as PasskeyAuthResult;
}

/**
 * Brand-new account with a passkey as the only credential.
 * Server returns EMAIL_EXISTS when the email is already in use; the
 * caller surfaces a clear "use sign-in" hint instead of failing
 * silently after the platform Touch ID / Face ID prompt.
 */
export async function passkeySignup(
  convexBaseUrl: string,
  email: string,
  fullName: string,
): Promise<
  | { ok: true; result: PasskeyAuthResult }
  | {
      ok: false;
      error: "EMAIL_EXISTS" | "INVALID_EMAIL";
      hasPasskey?: boolean;
      providers?: string[];
    }
> {
  const startRes = await fetch(`${convexBaseUrl}/auth/passkey/signup/start`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: PASSKEY_BASE_URL },
    body: JSON.stringify({ email, fullName }),
  });
  if (!startRes.ok) throw new PasskeyError(await startRes.text());
  const startData = await startRes.json();
  if (startData?.ok === false) {
    return {
      ok: false,
      error: startData.error,
      hasPasskey: startData.hasPasskey,
      providers: Array.isArray(startData.providers) ? startData.providers : undefined,
    };
  }

  let attestation;
  const sheetStartedAt = Date.now();
  try {
    attestation = await Passkey.create({ ...startData.options, rp: { ...startData.options.rp, id: startData.options.rp?.id || RP_ID } });
  } catch (err: any) {
    throw classifyPasskeyError(err, sheetStartedAt, "sign-up");
  }

  const finishRes = await fetch(`${convexBaseUrl}/auth/passkey/signup/finish`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: PASSKEY_BASE_URL },
    body: JSON.stringify({ email, fullName, response: attestation }),
  });
  if (!finishRes.ok) throw new PasskeyError(await finishRes.text());
  const result = (await finishRes.json()) as PasskeyAuthResult;
  return { ok: true, result };
}

/**
 * Add a passkey to the currently signed-in user's account. Used by
 * the post-OAuth-login enrollment screen so an Apple/Google/email
 * user can upgrade to passkey-first sign-in next time.
 */
export async function passkeyEnroll(
  convexBaseUrl: string,
  authToken: string,
  deviceLabel?: string,
): Promise<{ ok: true; credentialId: string }> {
  const startRes = await fetch(`${convexBaseUrl}/auth/passkey/register/start`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Origin: PASSKEY_BASE_URL,
      Authorization: `Bearer ${authToken}`,
    },
    body: JSON.stringify({ deviceLabel }),
  });
  if (!startRes.ok) throw new PasskeyError(await startRes.text());
  const { options } = await startRes.json();

  let attestation;
  const sheetStartedAt = Date.now();
  try {
    attestation = await Passkey.create({ ...options, rp: { ...options.rp, id: options.rp?.id || RP_ID } });
  } catch (err: any) {
    throw classifyPasskeyError(err, sheetStartedAt, "enrollment");
  }

  const finishRes = await fetch(`${convexBaseUrl}/auth/passkey/register/finish`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Origin: PASSKEY_BASE_URL,
      Authorization: `Bearer ${authToken}`,
    },
    body: JSON.stringify({ response: attestation, deviceLabel }),
  });
  if (!finishRes.ok) throw new PasskeyError(await finishRes.text());
  return (await finishRes.json()) as { ok: true; credentialId: string };
}

// iOS folds "no credential found" and "user cancelled" into the same
// error code (ASAuthorizationError.canceled = 1001 → react-native-passkey
// "UserCancelled"). They are distinguishable in practice by elapsed time:
//
//   - No credentials: iOS dismisses immediately, no sheet renders.
//     Elapsed time ≪ 500 ms.
//   - User cancel:    sheet animates in, user reads + taps cancel.
//     Elapsed time ≥ 1500 ms in any plausible scenario.
//
// 800 ms is comfortably between the two regimes. The exact threshold is
// not load-bearing — any value in [500, 1200] would work.
const NO_CREDENTIAL_CANCEL_THRESHOLD_MS = 800;

function classifyPasskeyError(err: any, sheetStartedAt: number, op: string): Error {
  if (!err) return new PasskeyError(`Passkey ${op} failed.`);

  const code = String(err?.code || "");
  const msg = String(err?.message || "");
  const lower = msg.toLowerCase();
  const elapsed = Date.now() - sheetStartedAt;

  // Explicit no-credential signals from Android / future iOS versions.
  if (code === "NoCredentials" || lower.includes("no credential") || lower.includes("no passkeys")) {
    return new PasskeyNoCredential();
  }

  // Configuration errors — AASA / assetlinks / entitlements mismatch.
  if (code === "BadConfiguration" || code === "NotConfiguredError" || code === "NotSupported") {
    return new PasskeyMisconfigured(msg);
  }

  const looksLikeCancel =
    code === "UserCancelled" ||
    code === "Cancelled" ||
    lower.includes("cancel") ||
    lower.includes("aborted") ||
    err?.name === "AbortError" ||
    err?.name === "NotAllowedError";

  if (looksLikeCancel) {
    // Fast dismiss with no user interaction → there were no credentials
    // for rpId="yaver.io" to show. Slow dismiss → genuine cancel.
    if (elapsed < NO_CREDENTIAL_CANCEL_THRESHOLD_MS) {
      return new PasskeyNoCredential();
    }
    return new PasskeyCancelled();
  }

  return new PasskeyError(msg || `Passkey ${op} failed.`);
}

/**
 * Anonymous preflight: does this email have a passkey registered? Used
 * by the login screen so we can route the user to OAuth instead of
 * firing an iOS sheet that will silently dismiss with no UI.
 *
 * Returns null when the network call fails — caller should fall back to
 * trying the platform sheet anyway rather than block sign-in on a network
 * blip.
 */
export async function passkeyHasCredential(
  convexBaseUrl: string,
  email: string,
): Promise<{ hasPasskey: boolean; emailRegistered: boolean } | null> {
  const trimmed = email.trim().toLowerCase();
  if (!trimmed || !trimmed.includes("@")) return null;
  try {
    const res = await fetch(
      `${convexBaseUrl}/auth/passkey/check?email=${encodeURIComponent(trimmed)}`,
    );
    if (!res.ok) return null;
    return (await res.json()) as { hasPasskey: boolean; emailRegistered: boolean };
  } catch {
    return null;
  }
}

/**
 * Signed-in user's enrolled passkey count. Powers the post-OAuth
 * "Enable passkey for next time?" prompt on the login screen and the
 * passkey settings card.
 */
export async function passkeyListCount(
  convexBaseUrl: string,
  authToken: string,
): Promise<number | null> {
  try {
    const res = await fetch(`${convexBaseUrl}/auth/passkey/list`, {
      headers: { Authorization: `Bearer ${authToken}` },
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { passkeys?: unknown[] };
    return Array.isArray(data?.passkeys) ? data.passkeys.length : 0;
  } catch {
    return null;
  }
}
