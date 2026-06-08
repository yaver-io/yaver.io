// qaClient — drives the agentic app-test agent over the YAVER MESH (LAN-first,
// relay fallback), mirroring studioClient's transport. Runs the in-repo flow
// corpus on a redroid surface and returns the bug report ("K caught / J fixed").
// Catch-only or fix mode; the surface is a cold redroid or a warm Yaver Base
// Image (base). Works against any device holding the app's repo — owner box or
// managed-cloud farm.

import { quicClient } from "./quic";

const AGENT_PORT = 18080;

export type QATarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type QABug = {
  title: string;
  severity: "low" | "medium" | "high" | "critical";
  oracle: string;
  detail?: string;
  step?: number;
  outcome?: "caught" | "fixed" | "attempted-unresolved";
  fixSummary?: string;
};

export type QAFlowResult = {
  name: string;
  goal?: string;
  steps?: number;
  bugs?: number;
  expectations?: { expectation: string; pass: boolean; reason?: string; severity?: string }[];
};

export type QAReport = {
  ok?: boolean;
  error?: string;
  mode?: string;
  flows?: QAFlowResult[];
  bugs?: QABug[];
  caught?: number;
  fixed?: number;
  passed?: boolean;
};

export type QAJob = {
  ok?: boolean;
  error?: string;
  id?: string;
  kind?: string;
  state?: "queued" | "running" | "completed" | "failed";
  phase?: string;
  log?: string[];
  durationSec?: number;
  dir?: string;
};

export type QARunArgs = {
  package: string;
  apk?: string;
  flowsDir?: string;
  mode?: "catch" | "fix";
  base?: string; // a Yaver Base Image version (warm restore) instead of cold boot
  sshHost?: string;
  hostWorkDir?: string;
  container?: string;
};

async function lanAttempt(host: string, port: number, body: string, timeoutMs: number): Promise<any | null> {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), timeoutMs);
    const res = await fetch(`http://${host}:${port}/ops`, {
      method: "POST",
      headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
      body,
      signal: ctrl.signal,
    });
    clearTimeout(t);
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

async function qaOps<T = any>(
  target: QATarget | undefined,
  verb: string,
  payload: Record<string, unknown>,
  timeoutMs = 60000,
): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a device that has your app's repo first" } as unknown as T;
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

export const qaClient = {
  /** Start an async QA run over yaver-tests/flows. Returns a job to poll. */
  run: (t: QATarget, args: QARunArgs) => qaOps<QAJob>(t, "qa_run", args as any, 30000),

  /** Poll a QA job's live status (reuses studio_job_status). */
  jobStatus: (t: QATarget, jobId?: string) =>
    qaOps<QAJob>(t, "studio_job_status", { jobId } as any, 15000),

  /** Fetch the structured report card once the job completes. */
  report: (t: QATarget, jobId: string) => qaOps<QAReport>(t, "qa_report", { jobId } as any, 15000),

  /** List Yaver Base Image snapshots on the device (for the base picker). */
  bases: (t: QATarget) => qaOps<{ bases?: { version: string; arch: string; yaverBaked?: boolean }[] }>(t, "qa_base_list", {}, 15000),
};
