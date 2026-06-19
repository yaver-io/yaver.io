// testkitClient — drives project web tests over the Yaver mesh, mirroring
// qaClient's LAN-first/relay-fallback transport. Picking a target device =
// picking the remote PC the suite runs on. Supports chromedp, YAML Playwright,
// native Playwright projects, self-grow, dependency checks, profiles, and
// artifact fetches through desktop/agent/ops_testkit.go.

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
  tracePath?: string;
};

export type TKArtifactRef = {
  kind: string;
  path: string;
  name?: string;
  mimeType?: string;
  bytes?: number;
  feature?: string;
  step?: number;
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
  artifacts?: TKArtifactRef[];
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
  headed?: boolean;
  video?: boolean;
  trace?: boolean;
  profile?: string;
  storageState?: string;
  devCommand?: string;
  waitURL?: string;
  devTimeoutSec?: number;
  keepDevServer?: boolean;
};

export type TKNativeRunArgs = {
  dir?: string;
  config?: string;
  project?: string;
  grep?: string;
  workers?: number;
  headed?: boolean;
  trace?: string;
  reporter?: string;
  devCommand?: string;
  waitURL?: string;
  devTimeoutSec?: number;
  keepDevServer?: boolean;
  env?: Record<string, string>;
};

export type TKProfile = {
  name: string;
  path?: string;
  bytes?: number;
  modifiedAt?: string;
};

export type TKPlaywrightStatus = {
  ok?: boolean;
  ready?: boolean;
  dir?: string;
  nodePath?: string;
  nodeVersion?: string;
  playwrightPackage?: boolean;
  chromiumInstalled?: boolean;
  cacheDir?: string;
  fixes?: string[];
  error?: string;
};

export type TKQualityReport = {
  ok?: boolean;
  error?: string;
  jobId?: string;
  passed?: boolean;
  browserMode?: "skip" | "chromedp" | "playwright-yaml" | "playwright-native";
  preflight?: Record<string, unknown>;
  browserJobId?: string;
  qaJobId?: string;
  web?: TKReport;
  android?: {
    mode?: string;
    flows?: { name: string; goal?: string; steps?: number; bugs?: number }[];
    bugs?: { title: string; severity: string; oracle: string; detail?: string; outcome?: string; fixSummary?: string }[];
    caught?: number;
    fixed?: number;
    passed?: boolean;
  };
  summary?: string[];
};

export type TKTraceInspect = {
  ok?: boolean;
  error?: string;
  name?: string;
  path?: string;
  bytes?: number;
  entryCount?: number;
  shown?: number;
  entries?: { name: string; bytes?: number; compressed?: number }[];
  totalBytes?: number;
  traceFiles?: number;
  resources?: number;
  screenshots?: number;
  sourceFiles?: number;
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

  /** Check Playwright readiness on the selected remote PC. */
  playwrightStatus: (t: TKTarget, dir?: string) =>
    tkOps<TKPlaywrightStatus>(t, "playwright_status", { dir }, 15000),

  /** Repair/install Playwright runtime dependencies on the selected remote PC. */
  playwrightRepair: (t: TKTarget, include?: string[]) =>
    tkOps<TKJob>(t, "playwright_repair", { include }, 30000),

  /** Run Yaver YAML specs through Playwright instead of chromedp. */
  playwrightRun: (t: TKTarget, args: TKRunArgs) =>
    tkOps<TKJob>(t, "playwright_run", args as any, 30000),

  /** Run an app's native `npx playwright test` project. */
  playwrightNativeRun: (t: TKTarget, args: TKNativeRunArgs) =>
    tkOps<TKJob>(t, "playwright_native_run", args as any, 30000),

  /** List saved Playwright storage-state profiles on the selected PC. */
  playwrightProfiles: (t: TKTarget) =>
    tkOps<{ profiles?: TKProfile[]; error?: string }>(t, "playwright_profiles", {}, 15000),

  /** Start headed login/profile capture on the selected PC. */
  playwrightProfileAuth: (t: TKTarget, args: { dir?: string; url: string; successURL?: string; profile: string; timeoutSec?: number }) =>
    tkOps<TKJob>(t, "playwright_profile_auth", args as any, 30000),

  /** Save profile auth state immediately once login/2FA is complete. */
  playwrightProfileAuthFinish: (t: TKTarget, jobId?: string) =>
    tkOps<TKJob>(t, "playwright_profile_auth_finish", { jobId } as any, 15000),

  /** Cancel an in-progress headed profile-auth job. */
  playwrightProfileAuthCancel: (t: TKTarget, jobId?: string) =>
    tkOps<TKJob>(t, "playwright_profile_auth_cancel", { jobId } as any, 15000),

  /** Poll the run's live status (reuses studio_job_status). */
  jobStatus: (t: TKTarget, jobId?: string) =>
    tkOps<TKJob>(t, "studio_job_status", { jobId } as any, 15000),

  /** Fetch the feature-based highlight report once the job completes. */
  report: (t: TKTarget, jobId: string) => tkOps<TKReport>(t, "project_test_report", { jobId } as any, 15000),

  /** List normalized artifact refs for a completed Playwright run. */
  playwrightArtifacts: (t: TKTarget, jobId: string) =>
    tkOps<{ artifacts?: TKArtifactRef[]; error?: string }>(t, "playwright_artifacts", { jobId } as any, 15000),

  /** List recent local Playwright/testkit artifact directories. */
  playwrightRuns: (t: TKTarget, limit?: number) =>
    tkOps<{ runs?: any[]; error?: string }>(t, "playwright_runs", { limit } as any, 15000),

  /** Cleanup known Playwright/testkit artifact roots. Defaults to dry-run server-side. */
  playwrightGC: (t: TKTarget, args?: { olderThanHours?: number; dryRun?: boolean }) =>
    tkOps<{ deleted?: string[]; kept?: string[]; error?: string }>(t, "playwright_gc", args || {}, 30000),

  /** Run browser tests plus optional Redroid QA as one Talos certification job. */
  qualityRun: (t: TKTarget, args: Record<string, unknown>) =>
    tkOps<TKJob>(t, "talos_quality_run", args, 30000),

  /** Fetch the combined Talos quality report after qualityRun completes. */
  qualityReport: (t: TKTarget, jobId: string) =>
    tkOps<TKQualityReport>(t, "talos_quality_report", { jobId } as any, 15000),

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

  /** Fetch Playwright trace/video/screenshot/report artifacts through the scoped verb. */
  playwrightArtifact: (t: TKTarget, jobId: string, path: string) =>
    tkOps<{ name?: string; mimeType?: string; bytes?: number; base64?: string; error?: string }>(
      t,
      "playwright_artifact",
      { jobId, path },
      30000,
    ),

  /** Inspect a referenced Playwright trace.zip without downloading the whole zip. */
  playwrightTraceInspect: (t: TKTarget, jobId: string, path: string) =>
    tkOps<TKTraceInspect>(t, "playwright_trace_inspect", { jobId, path }, 15000),
};
