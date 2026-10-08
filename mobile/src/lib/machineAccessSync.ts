import { Platform } from "react-native";
import { credentialAccountFingerprint, type CredentialHandoffEnvelope } from "./credentialHandoff";
import { acceptMachineAccessHandoff, createLocalCredentialHandoffRequest, getCredentialHandoffDeviceId } from "./credentialHandoffStore";
import { registerCredentialHandoffDevice } from "./credentialHandoffDirectory";
import { runIsolatedCredentialP2P, type CredentialP2PEndpoint } from "./credentialP2P";
import { createQuicClient } from "./quic";
import { generateSSHIdentity, saveSyncedSSHHost } from "./plainSSH";
import { getSecret, setSecret } from "./secure-storage";

const ACCESS_PROFILES_KEY = "yaver.machine-access.profiles.v1";
const SSH_IDENTITY_KEY = "yaver.machine-access.ssh-identity.v1";

export interface MachineAccessProfile {
  version: 1;
  deviceId: string;
  name: string;
  user: string;
  port: number;
  hosts: string[];
  sshFingerprint: string;
}

export async function loadMachineAccessProfiles(): Promise<MachineAccessProfile[]> {
  if (Platform.OS === "web") return [];
  try { return JSON.parse((await getSecret(ACCESS_PROFILES_KEY)) || "[]"); } catch { return []; }
}

function validateBundle(raw: string, expectedDeviceId: string): MachineAccessProfile {
  const value = JSON.parse(raw) as MachineAccessProfile;
  value.hosts = Array.isArray(value.hosts) ? [...new Set(value.hosts.map(String).map((host) => host.trim()).filter(Boolean))] : [];
  if (value.version !== 1 || value.deviceId !== expectedDeviceId || !value.user?.trim() || !Number.isInteger(value.port) || value.port < 1 || value.port > 65535 || value.hosts.length === 0 || !value.sshFingerprint?.startsWith("SHA256:")) {
    throw new Error("The endpoint returned an invalid machine access profile.");
  }
  return value;
}

async function loadOrCreateSSHIdentity() {
  const existing = await getSecret(SSH_IDENTITY_KEY);
  if (existing) {
    try {
      const identity = JSON.parse(existing);
      if (identity.publicKey?.startsWith("ssh-ed25519 ") && identity.privateKey?.includes("PRIVATE KEY") && identity.fingerprint?.startsWith("SHA256:")) return identity;
    } catch { /* replace corrupt secure entry */ }
  }
  const identity = await generateSSHIdentity();
  await setSecret(SSH_IDENTITY_KEY, JSON.stringify(identity));
  return identity;
}

/** Enroll this phone's generated SSH public key and pull LAN/private-overlay
 * coordinates directly from the PC. The private key and the resulting profile
 * remain in platform secure storage; Convex sees only the existing public
 * handoff identity used to prevent key substitution. */
export async function syncMachineAccessToPhone(args: {
  accountId: string;
  authToken: string;
  endpoint: CredentialP2PEndpoint;
}): Promise<MachineAccessProfile> {
  const identity = await loadOrCreateSSHIdentity();
  const accountFingerprint = credentialAccountFingerprint(args.accountId);
  const deviceId = await getCredentialHandoffDeviceId();
  const request = await createLocalCredentialHandoffRequest({ deviceId, accountFingerprint });
  await registerCredentialHandoffDevice({ token: args.authToken, deviceId, publicKey: request.targetPublicKey, platform: Platform.OS });
  const client = createQuicClient();
  const profile = await runIsolatedCredentialP2P({
    endpoint: args.endpoint,
    authToken: args.authToken,
    createClient: () => client,
    operation: async (direct) => {
      const offered = await (direct as ReturnType<typeof createQuicClient>).callOps("machine_access_handoff_offer", {
        ...request,
        sshPublicKey: identity.publicKey,
        label: `yaver-${Platform.OS}`,
      });
      if (!offered.ok || !offered.initial) throw new Error(offered.error || "The endpoint could not synchronize machine access.");
      const raw = await acceptMachineAccessHandoff({ envelope: offered.initial as CredentialHandoffEnvelope, deviceId, accountFingerprint });
      return validateBundle(raw, args.endpoint.id);
    },
  });
  const existing = await loadMachineAccessProfiles();
  await saveSyncedSSHHost({ id: profile.deviceId, host: profile.hosts[0], port: profile.port, user: profile.user, fingerprint: profile.sshFingerprint }, { privateKey: identity.privateKey });
  await setSecret(ACCESS_PROFILES_KEY, JSON.stringify([...existing.filter((item) => item.deviceId !== profile.deviceId), profile]));
  return profile;
}
