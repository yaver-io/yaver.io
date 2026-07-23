import type { ProviderId } from "./cloudProviders/types";

export type RuntimeProbeSnapshot = {
  provider: ProviderId;
  providerCreateOk?: boolean;
  providerStatus?: "creating" | "running" | "stopped" | "deleted" | "error" | "unknown";
  agentHeartbeatOk?: boolean;
  relayDataPathOk?: boolean;
  sshReachable?: boolean;
  lastProviderError?: string;
  lastAgentError?: string;
  lastRelayError?: string;
  lastSshError?: string;
};

export type RuntimeTroubleshootingVerdict = {
  ok: boolean;
  plane: "provider" | "agent" | "relay" | "ssh" | "ready";
  code:
    | "provider_create_failed"
    | "provider_not_running"
    | "agent_not_online"
    | "relay_data_path_down"
    | "ssh_unreachable"
    | "ready";
  summary: string;
  nextProbe: string;
};

export function classifyRuntimeTroubleshooting(snapshot: RuntimeProbeSnapshot): RuntimeTroubleshootingVerdict {
  if (snapshot.providerCreateOk === false || snapshot.providerStatus === "error") {
    return {
      ok: false,
      plane: "provider",
      code: "provider_create_failed",
      summary: snapshot.lastProviderError || `${snapshot.provider} could not create or update the VM`,
      nextProbe: "provider API create/status logs",
    };
  }
  if (snapshot.providerStatus && !["running", "unknown"].includes(snapshot.providerStatus)) {
    return {
      ok: false,
      plane: "provider",
      code: "provider_not_running",
      summary: `${snapshot.provider} reports machine state ${snapshot.providerStatus}`,
      nextProbe: "provider machine status",
    };
  }
  if (snapshot.agentHeartbeatOk === false) {
    return {
      ok: false,
      plane: "agent",
      code: "agent_not_online",
      summary: snapshot.lastAgentError || "provider VM exists but the Yaver agent has not heartbeated",
      nextProbe: "cloud-init logs and Yaver agent service status",
    };
  }
  if (snapshot.relayDataPathOk === false) {
    return {
      ok: false,
      plane: "relay",
      code: "relay_data_path_down",
      summary: snapshot.lastRelayError || "Yaver agent is online but relay signaling/data path is not usable",
      nextProbe: "relay registration, relay password, relay SPKI pin, and relay presence samples",
    };
  }
  if (snapshot.sshReachable === false) {
    return {
      ok: false,
      plane: "ssh",
      code: "ssh_unreachable",
      summary: snapshot.lastSshError || "workspace is controllable through Yaver but direct SSH is not reachable",
      nextProbe: "provider firewall, public endpoint, and SSH key configuration",
    };
  }
  return {
    ok: true,
    plane: "ready",
    code: "ready",
    summary: "provider VM, Yaver agent, relay data path, and SSH access are ready",
    nextProbe: "none",
  };
}
