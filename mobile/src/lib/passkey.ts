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
  try {
    assertion = await Passkey.get({ ...options, rpId: options.rpId || RP_ID });
  } catch (err: any) {
    if (isCancellation(err)) throw new PasskeyCancelled();
    throw new PasskeyError(err?.message || "Passkey sign-in failed.");
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
  | { ok: false; error: "EMAIL_EXISTS" | "INVALID_EMAIL"; hasPasskey?: boolean }
> {
  const startRes = await fetch(`${convexBaseUrl}/auth/passkey/signup/start`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: PASSKEY_BASE_URL },
    body: JSON.stringify({ email, fullName }),
  });
  if (!startRes.ok) throw new PasskeyError(await startRes.text());
  const startData = await startRes.json();
  if (startData?.ok === false) {
    return { ok: false, error: startData.error, hasPasskey: startData.hasPasskey };
  }

  let attestation;
  try {
    attestation = await Passkey.create({ ...startData.options, rp: { ...startData.options.rp, id: startData.options.rp?.id || RP_ID } });
  } catch (err: any) {
    if (isCancellation(err)) throw new PasskeyCancelled();
    throw new PasskeyError(err?.message || "Passkey sign-up failed.");
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
  try {
    attestation = await Passkey.create({ ...options, rp: { ...options.rp, id: options.rp?.id || RP_ID } });
  } catch (err: any) {
    if (isCancellation(err)) throw new PasskeyCancelled();
    throw new PasskeyError(err?.message || "Passkey enrollment failed.");
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

function isCancellation(err: any): boolean {
  if (!err) return false;
  const code = String(err?.code || "");
  const msg = String(err?.message || "").toLowerCase();
  return (
    code === "UserCancelled" ||
    code === "Cancelled" ||
    msg.includes("cancel") ||
    msg.includes("aborted") ||
    err?.name === "AbortError" ||
    err?.name === "NotAllowedError"
  );
}
