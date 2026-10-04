import { LOCAL_KEYS } from "./auth";
import {
  credentialAccountFingerprint,
  sealCredentialForHandoff,
  type CredentialHandoffRequest,
} from "./credentialHandoff";
import {
  acceptCredentialHandoff,
  createLocalCredentialHandoffRequest,
  getCredentialHandoffDeviceId,
} from "./credentialHandoffStore";
import { registerCredentialHandoffDevice } from "./credentialHandoffDirectory";
import { createQuicClient } from "./quic";
import {
  runIsolatedCredentialP2P,
  type CredentialP2PEndpoint,
} from "./credentialP2P";
import { getSecret } from "./secure-storage";
import { Platform } from "react-native";

async function withCredentialP2P<T>(
  endpoint: CredentialP2PEndpoint,
  authToken: string,
  operation: (client: ReturnType<typeof createQuicClient>) => Promise<T>,
): Promise<T> {
  return runIsolatedCredentialP2P({
    endpoint,
    authToken,
    createClient: createQuicClient,
    operation: (client) => operation(client as ReturnType<typeof createQuicClient>),
  });
}

/**
 * Copy the phone-held token directly into the currently connected trusted
 * endpoint's local vault. The ops transport carries only the receiver's public
 * request and authenticated ciphertext. Inspectable relay ingress fails closed
 * at the receiver, so this succeeds only on direct LAN/private-overlay P2P.
 */
export async function shareLocalHetznerWithConnectedEndpoint(
  accountId: string,
  authToken: string,
  endpoint: CredentialP2PEndpoint,
): Promise<void> {
  const token = (await getSecret(LOCAL_KEYS.hetznerToken))?.trim();
  if (!token) throw new Error("Connect Hetzner on this phone first.");
  await withCredentialP2P(endpoint, authToken, async (client) => {
    const requested = await client.callOps("credential_handoff_request", {});
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
    const accepted = await client.callOps("credential_handoff_accept", envelope as unknown as Record<string, unknown>);
    if (!accepted.ok) throw new Error(accepted.error || "The endpoint rejected secure handoff.");
  });
}

/** Receive the endpoint's locally stored token as phone-targeted ciphertext. */
export async function receiveLocalHetznerFromConnectedEndpoint(
  accountId: string,
  authToken: string,
  endpoint: CredentialP2PEndpoint,
): Promise<void> {
  const accountFingerprint = credentialAccountFingerprint(accountId);
  const deviceId = await getCredentialHandoffDeviceId();
  const request = await createLocalCredentialHandoffRequest({ deviceId, accountFingerprint });
  // The connected endpoint fetches this exact (deviceId, publicKey) tuple from
  // Convex over its own authenticated TLS connection before encrypting. That
  // makes a relay-side public-key substitution fail closed while the Hetzner
  // token itself remains phone-targeted NaCl ciphertext end to end.
  await registerCredentialHandoffDevice({
    token: authToken,
    deviceId: request.targetDeviceId,
    publicKey: request.targetPublicKey,
    platform: Platform.OS,
  });
  await withCredentialP2P(endpoint, authToken, async (client) => {
    const offered = await client.callOps("credential_handoff_offer", request as unknown as Record<string, unknown>);
    if (!offered.ok || !offered.initial) {
      if (offered.code === "unknown_verb") {
        throw new Error("The selected device's Yaver agent is too old for secure Hetzner retrieval. Update Yaver on that device and try again.");
      }
      throw new Error(offered.error || "The selected endpoint could not provide a Hetzner token.");
    }
    const accepted = await acceptCredentialHandoff({
      envelope: offered.initial as any,
      deviceId: request.targetDeviceId,
      accountFingerprint,
    });
    if (accepted.kind !== "hetzner-api-token") {
      throw new Error("The selected endpoint returned the wrong credential kind.");
    }
  });
}
