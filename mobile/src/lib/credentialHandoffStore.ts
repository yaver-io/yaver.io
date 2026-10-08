import nacl from "./secureNacl";
import util from "tweetnacl-util";

import { LOCAL_KEYS } from "./auth";
import { getSecret, setSecret } from "./secure-storage";
import {
  createCredentialHandoffRequest,
  openCredentialHandoff,
  type CredentialHandoffEnvelope,
  type CredentialHandoffRequest,
  type HandoffCredentialKind,
} from "./credentialHandoff";

const { decodeBase64, encodeBase64 } = util;
const IDENTITY_PREFIX = "yaver_credential_handoff_identity_v1_";
const CONSUMED_PREFIX = "yaver_credential_handoff_consumed_v1_";
const MAX_CONSUMED_IDS = 64;
const DEVICE_ID_KEY = "yaver_credential_handoff_device_id_v1";

const SECRET_SLOT: Record<HandoffCredentialKind, string> = {
  "deepseek-api-key": LOCAL_KEYS.deepseekApiKey,
  "openai-api-key": LOCAL_KEYS.openAiApiKey,
  "anthropic-api-key": LOCAL_KEYS.anthropicApiKey,
  "glm-api-key": LOCAL_KEYS.glmApiKey,
  "github-token": LOCAL_KEYS.githubToken,
  "gitlab-token": LOCAL_KEYS.gitlabToken,
  "bitbucket-token": LOCAL_KEYS.bitbucketToken,
  "hetzner-api-token": LOCAL_KEYS.hetznerToken,
};

function scopedKey(prefix: string, accountFingerprint: string): string {
  return `${prefix}${accountFingerprint}`;
}

async function loadOrCreateIdentity(accountFingerprint: string): Promise<nacl.BoxKeyPair> {
  const key = scopedKey(IDENTITY_PREFIX, accountFingerprint);
  const raw = await getSecret(key);
  if (raw) {
    try {
      const saved = JSON.parse(raw) as { publicKey: string; secretKey: string };
      const publicKey = decodeBase64(saved.publicKey);
      const secretKey = decodeBase64(saved.secretKey);
      if (publicKey.length === nacl.box.publicKeyLength && secretKey.length === nacl.box.secretKeyLength) {
        return { publicKey, secretKey };
      }
    } catch {
      // Replace corrupt identity below. No credential is accepted with it.
    }
  }
  const identity = nacl.box.keyPair();
  await setSecret(key, JSON.stringify({
    publicKey: encodeBase64(identity.publicKey),
    secretKey: encodeBase64(identity.secretKey),
  }));
  return identity;
}

export async function getCredentialHandoffPublicIdentity(accountFingerprint: string): Promise<{ publicKey: string }> {
  const identity = await loadOrCreateIdentity(accountFingerprint);
  return { publicKey: encodeBase64(identity.publicKey) };
}

export async function getCredentialHandoffDeviceId(): Promise<string> {
  const existing = (await getSecret(DEVICE_ID_KEY))?.trim();
  if (existing) return existing;
  const created = `client_${Array.from(nacl.randomBytes(12), (value) => value.toString(16).padStart(2, "0")).join("")}`;
  await setSecret(DEVICE_ID_KEY, created);
  return created;
}

async function loadConsumed(accountFingerprint: string): Promise<string[]> {
  const raw = await getSecret(scopedKey(CONSUMED_PREFIX, accountFingerprint));
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === "string").slice(-MAX_CONSUMED_IDS) : [];
  } catch {
    return [];
  }
}

export async function createLocalCredentialHandoffRequest(args: {
  deviceId: string;
  accountFingerprint: string;
  now?: number;
}): Promise<CredentialHandoffRequest> {
  const identity = await loadOrCreateIdentity(args.accountFingerprint);
  return createCredentialHandoffRequest({
    targetDeviceId: args.deviceId,
    targetPublicKey: identity.publicKey,
    accountFingerprint: args.accountFingerprint,
    now: args.now,
  });
}

/**
 * Authenticate, consume once, and write straight to platform secure storage.
 * Callers receive metadata only so a UI/log/event cannot accidentally retain
 * the plaintext provider key or Git token.
 */
export async function acceptCredentialHandoff(args: {
  envelope: CredentialHandoffEnvelope;
  deviceId: string;
  accountFingerprint: string;
  now?: number;
}): Promise<{ handoffId: string; kind: HandoffCredentialKind; expiresAt: number }> {
  const identity = await loadOrCreateIdentity(args.accountFingerprint);
  const consumed = await loadConsumed(args.accountFingerprint);
  const opened = openCredentialHandoff(args.envelope, {
    expectedDeviceId: args.deviceId,
    expectedAccountFingerprint: args.accountFingerprint,
    recipientSecretKey: identity.secretKey,
    consumedHandoffIds: new Set(consumed),
    now: args.now,
  });

  // Save the credential before consuming the id. If secure storage refuses the
  // write, retry remains possible and the UI must surface that concrete cause.
  if (opened.kind === "machine-access-bundle") throw new Error("Machine access profiles require the dedicated secure-storage path.");
  await setSecret(SECRET_SLOT[opened.kind], opened.value);
  await setSecret(
    scopedKey(CONSUMED_PREFIX, args.accountFingerprint),
    JSON.stringify([...consumed.filter((id) => id !== opened.handoffId), opened.handoffId].slice(-MAX_CONSUMED_IDS)),
  );
  return { handoffId: opened.handoffId, kind: opened.kind, expiresAt: opened.expiresAt };
}

/** Open an encrypted machine profile in memory for immediate Keychain/Keystore
 * persistence. It is never put in the generic credential slot or returned to UI. */
export async function acceptMachineAccessHandoff(args: {
  envelope: CredentialHandoffEnvelope;
  deviceId: string;
  accountFingerprint: string;
  now?: number;
}): Promise<string> {
  const identity = await loadOrCreateIdentity(args.accountFingerprint);
  const consumed = await loadConsumed(args.accountFingerprint);
  const opened = openCredentialHandoff(args.envelope, {
    expectedDeviceId: args.deviceId,
    expectedAccountFingerprint: args.accountFingerprint,
    recipientSecretKey: identity.secretKey,
    consumedHandoffIds: new Set(consumed),
    now: args.now,
  });
  if (opened.kind !== "machine-access-bundle") throw new Error("The endpoint returned the wrong machine access payload.");
  await setSecret(scopedKey(CONSUMED_PREFIX, args.accountFingerprint), JSON.stringify([...consumed, opened.handoffId].slice(-MAX_CONSUMED_IDS)));
  return opened.value;
}
