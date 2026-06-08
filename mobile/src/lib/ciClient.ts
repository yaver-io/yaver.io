// ciClient — configure a Yaver box as a GitHub/GitLab self-hosted CI runner over
// the YAVER MESH, addressed by device (LAN-first, relay fallback). Mirrors
// armClient's transport. Drives the ci_runner_* / ci_workflow_* ops verbs so the
// box runs the user's existing workflows (runs-on: [self-hosted, yaver]) for $0
// GitHub minutes. See docs/yaver-managed-cloud-ci-absorption.md.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const CI_DEVICE_KEY = "yaver.ci.deviceId";
const AGENT_PORT = 18080;

export type CITarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type CIRegistration = {
  key: string;
  provider: string;
  target: string;
  scope: string;
  labels: string[];
  isolation: string;
  where: string;
  maxConcurrent: number;
  live?: boolean;
};
export type CISavings = { runs: number; chargedCents: number; wouldHaveCostUpstreamCents: number; savedCents: number };
export type CIWorkflowTarget = { target: string; file: string; runsOn: string; secrets: string[]; description: string };
export type CIRegisterInput = {
  provider: "github" | "gitlab";
  target: string;
  scope?: "repo" | "org";
  isolation?: "container" | "host";
  where?: "self-hosted" | "operator-fleet" | "yaver-cloud";
  maxConcurrent?: number;
};

export async function getCIDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(CI_DEVICE_KEY)) || "";
}
export async function setCIDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(CI_DEVICE_KEY, id.trim());
}

async function lanAttempt(host: string, port: number, body: string, timeoutMs: number): Promise<any | null> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), Math.min(timeoutMs, 8000));
  try {
    const res = await fetch(`http://${host}:${port}/ops`, {
      method: "POST",
      headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
      body,
      signal: ctrl.signal,
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok || data?.error) return data;
    return null;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

async function ciOps<T = any>(target: CITarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a device first" } as unknown as T;
  const body = JSON.stringify({ verb, payload, machine: "local" });
  const port = target.port || AGENT_PORT;
  const hosts = [...(target.lanIps || []), ...(target.host ? [target.host] : [])].filter(Boolean);
  for (const h of hosts) {
    const data = await lanAttempt(h, port, body, timeoutMs);
    if (data) {
      if (data?.ok === false || (data?.error && data?.initial === undefined)) {
        return { ok: false, code: data?.code, error: data?.error } as unknown as T;
      }
      return ((data as any)?.initial ?? data) as T;
    }
  }
  const data = await quicClient.callOpsOnDevice(target.id, verb, payload, timeoutMs);
  if (data?.ok === false) return { ok: false, code: (data as any)?.code, error: data?.error } as unknown as T;
  return ((data as any)?.initial ?? data) as T;
}

export const ciClient = {
  status: (t: CITarget) => ciOps<{ registrations: CIRegistration[]; savings: CISavings }>(t, "ci_runner_status", {}, 20000),
  list: (t: CITarget) => ciOps<{ registrations: CIRegistration[]; count: number }>(t, "ci_runner_list", {}, 20000),
  register: (t: CITarget, input: CIRegisterInput) =>
    ciOps<{ key: string; labels: string[]; runsOn: string[]; forgeUrl: string; hint: string; ok?: boolean; error?: string }>(t, "ci_runner_register", input as any, 30000),
  remove: (t: CITarget, key: string) => ciOps<{ removed: string; ok?: boolean; error?: string }>(t, "ci_runner_remove", { key }, 20000),
  workflowTargets: (t: CITarget) => ciOps<{ targets: CIWorkflowTarget[] }>(t, "ci_workflow_targets", {}, 15000),
  scaffold: (t: CITarget, target: string, write: boolean) =>
    ciOps<{ path: string; content: string; secrets: string[]; written?: boolean; ok?: boolean; error?: string }>(t, "ci_workflow_scaffold", { target, write, workDir: "." }, 20000),
};
