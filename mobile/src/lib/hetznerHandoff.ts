import { LOCAL_KEYS } from "./auth";
import {
  credentialAccountFingerprint,
  sealCredentialForHandoff,
  type CredentialHandoffRequest,
} from "./credentialHandoff";
import { quicClient } from "./quic";
import { getSecret } from "./secure-storage";

/**
 * Copy the phone-held token directly into the currently connected trusted
 * endpoint's local vault. The ops transport carries only the receiver's public
 * request and authenticated ciphertext. Inspectable relay ingress fails closed
 * at the receiver, so this succeeds only on direct LAN/TLS or Yaver Mesh.
 */
export async function shareLocalHetznerWithConnectedEndpoint(accountId: string): Promise<void> {
  const token = (await getSecret(LOCAL_KEYS.hetznerToken))?.trim();
  if (!token) throw new Error("Connect Hetzner on this phone first.");
  if (!quicClient.isConnected) throw new Error("Connect a trusted endpoint on the same network or Yaver Mesh first.");
  if (quicClient.connectionMode !== "direct") {
    throw new Error("Secure handoff is disabled on inspectable relay/tunnel routes. Connect over the same LAN, Tailscale, or Yaver Mesh.");
  }

  const requested = await quicClient.callOps("credential_handoff_request", {});
  if (!requested.ok || !requested.initial) throw new Error(requested.error || "The endpoint could not start secure handoff.");
  const request = requested.initial as CredentialHandoffRequest;
  const fingerprint = credentialAccountFingerprint(accountId);
  if (request.accountFingerprint !== fingerprint) throw new Error("The endpoint belongs to a different signed-in account.");

  const envelope = sealCredentialForHandoff({
    request,
    expectedAccountFingerprint: fingerprint,
    kind: "hetzner-api-token",
    value: token,
  });
  const accepted = await quicClient.callOps("credential_handoff_accept", envelope as unknown as Record<string, unknown>);
  if (!accepted.ok) throw new Error(accepted.error || "The endpoint rejected secure handoff.");
}
