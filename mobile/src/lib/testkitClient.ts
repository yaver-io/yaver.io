// testkitClient — drives the project test runner (chromedp web specs +
// self-grow) over the Yaver mesh, mirroring qaClient's LAN-first/relay-fallback
// transport. Picking a target device = picking the remote PC the suite runs on
// (web specs run wherever that agent runs). Pairs with the Go ops verbs in
// desktop/agent/ops_testkit.go: project_test_specs/run/report/grow.

import { quicClient } from "./quic";

const AGENT_PORT = 18080;

export type TKTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type TKFeature = {
  name: string;
  status: "pass" | "fail";
  target?: string;
  url?: string;
  durationMs?: number;
  steps?: number;
  error?: string;
  failStep?: number;
  screenshots?: string[];
  framesDir?: string;
  clipPath?: string;
  posterPath?: string;
};

export type TKReport = {
  ok?: boolean;
  error?: string;
  project?: string;
  total?: number;
  passed?: number;
  failed?: number;
  durationMs?: number;
  features?: TKFeature[];
  reelPath?: string;
  dir?: string;
};

export type TKJob = {
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

export type TKSpec = { name: string; target?: string; url?: string; steps?: number; path?: string };
export type TKGrowPlan = {
  ok?: boolean;
  error?: string;
  projectDir?: string;
  specsDir?: string;
  coveredCount?: number;
  uncovered?: { suggestedName: string; route: string; file: string; why: string }[];
  ledgerPath?: string;
  applied?: boolean;
  authorPrompt?: string;
  taskId?: string;
};

export type TKRunArgs = {
  project?: string;
  dir?: string; // repo root ON the remote PC (yaver-tests resolved under it)
  root?: string;
  only?: string;
  env?: Record<string, string>; // ${ENV} for spec cookies/secrets (e.g. TALOS_SESSION_TOKEN)
  concurrency?: number;
  headful?: boolean;
  video?: boolean;
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

async function tkOps<T = any>(
  target: TKTarget | undefined,
  verb: string,
  payload: Record<string, unknown>,
  timeoutMs = 60000,
): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a remote PC that has the project repo first" } as unknown as T;
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

export const testkitClient = {
  /** List the project's test Features (specs). */
  specs: (t: TKTarget, dir: string) =>
    tkOps<{ root?: string; features?: TKSpec[] }>(t, "project_test_specs", { dir }, 15000),

  /** Start an async web test run on the remote PC. Returns a job to poll. */
  run: (t: TKTarget, args: TKRunArgs) => tkOps<TKJob>(t, "project_test_run", args as any, 30000),

  /** Poll the run's live status (reuses studio_job_status). */
  jobStatus: (t: TKTarget, jobId?: string) =>
    tkOps<TKJob>(t, "studio_job_status", { jobId } as any, 15000),

  /** Fetch the feature-based highlight report once the job completes. */
  report: (t: TKTarget, jobId: string) => tkOps<TKReport>(t, "project_test_report", { jobId } as any, 15000),

  /** Self-grow: plan uncovered Features and (author:true) dispatch the runner to write them. */
  grow: (t: TKTarget, dir: string, opts?: { apply?: boolean; author?: boolean; runner?: string }) =>
    tkOps<TKGrowPlan>(t, "project_test_grow", { dir, apply: opts?.apply, author: opts?.author, runner: opts?.runner }, 30000),

  /** Check the runner's test dependencies (ffmpeg/chromium/node/playwright/docker/redroid). */
  depsCheck: (t: TKTarget) =>
    tkOps<{ os?: string; pkgManager?: string; ready?: boolean; deps?: { name: string; present: boolean; how?: string }[] }>(
      t,
      "testkit_deps_check",
      {},
      15000,
    ),

  /** Install every missing test dependency once (async job; poll then re-check). */
  depsInstall: (t: TKTarget) => tkOps<TKJob>(t, "testkit_deps_install", {}, 30000),

  /** Fetch a highlight clip / reel / screenshot (base64) to play/show in-app. */
  artifact: (t: TKTarget, jobId: string, path: string) =>
    tkOps<{ name?: string; mimeType?: string; bytes?: number; base64?: string; error?: string }>(
      t,
      "project_test_artifact",
      { jobId, path },
      30000,
    ),
};
