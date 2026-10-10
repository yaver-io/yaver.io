#!/usr/bin/env node
import { spawn } from "node:child_process";
import {
  chmodSync, existsSync, mkdirSync, openSync, readFileSync, readdirSync,
  renameSync, rmSync, statSync, writeFileSync,
} from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DEFAULT_ROOT = join(homedir(), ".yaver", "mobile-test");
const LEASE_MS = 15_000;

const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
const nowIso = () => new Date().toISOString();
const alive = (pid) => {
  if (!Number.isInteger(pid) || pid < 1) return false;
  try { process.kill(pid, 0); return true; } catch { return false; }
};

function statePaths(root = process.env.YAVER_MOBILE_TEST_ROOT || DEFAULT_ROOT) {
  return {
    root,
    jobs: join(root, "jobs"),
    artifacts: join(root, "artifacts"),
    locks: join(root, "locks"),
    daemonLock: join(root, "daemon.lock"),
  };
}

function ensureState(root) {
  const paths = statePaths(root);
  for (const directory of [paths.root, paths.jobs, paths.artifacts, paths.locks]) {
    mkdirSync(directory, { recursive: true, mode: 0o700 });
    chmodSync(directory, 0o700);
  }
  return paths;
}

function atomicJson(path, value) {
  const temporary = `${path}.${process.pid}.${Date.now()}.part`;
  writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
  chmodSync(temporary, 0o600);
  renameSync(temporary, path);
}

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

async function withJobLock(paths, id, action) {
  const lock = join(paths.locks, `${id}.lock`);
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      mkdirSync(lock, { mode: 0o700 });
      try { return await action(); } finally { rmSync(lock, { recursive: true, force: true }); }
    } catch (error) {
      if (error?.code !== "EEXIST") throw error;
      const age = Date.now() - statSync(lock).mtimeMs;
      if (age > 30_000) { rmSync(lock, { recursive: true, force: true }); continue; }
      await sleep(25);
    }
  }
  throw new Error(`job lock timed out: ${id}`);
}

function jobPath(paths, id) { return join(paths.jobs, `${id}.json`); }
function loadJob(paths, id) { return readJson(jobPath(paths, id)); }

async function updateJob(paths, id, mutate) {
  return withJobLock(paths, id, async () => {
    const path = jobPath(paths, id);
    const job = readJson(path);
    const updated = await mutate(job) || job;
    updated.updatedAt = nowIso();
    atomicJson(path, updated);
    return updated;
  });
}

function manifestPath(input) {
  if (input) return resolve(input);
  return join(HERE, "products", "yaver.json");
}

function loadScenario(input, scenarioName) {
  const path = manifestPath(input);
  const manifest = readJson(path);
  const scenario = manifest.scenarios?.[scenarioName];
  if (!scenario) throw new Error(`unknown scenario ${scenarioName} in ${path}`);
  if (!Array.isArray(scenario.steps) || scenario.steps.length === 0) {
    throw new Error(`scenario ${scenarioName} has no steps`);
  }
  const base = resolve(dirname(path), manifest.projectRoot || "../../..");
  const ids = new Set();
  const steps = scenario.steps.map((step, index) => {
    if (!step.id || ids.has(step.id)) throw new Error(`step ${index} needs a unique id`);
    ids.add(step.id);
    if (!Array.isArray(step.command) || step.command.length === 0 || step.command.some((part) => typeof part !== "string")) {
      throw new Error(`step ${step.id} command must be a non-empty argv array`);
    }
    return {
      id: step.id,
      command: step.command,
      cwd: resolve(base, step.cwd || "."),
      timeoutMs: Number(step.timeoutMs || 10 * 60_000),
      requiredEnv: [...new Set([...(scenario.requiredEnv || []), ...(step.requiredEnv || [])])],
    };
  });
  return { manifestPath: path, product: manifest.product, scenarioName, description: scenario.description || "", steps };
}

export function enqueue({ root, manifest, scenario }) {
  const paths = ensureState(root);
  const selected = loadScenario(manifest, scenario);
  const id = `${Date.now()}-${process.pid}-${Math.random().toString(16).slice(2, 10)}`;
  const artifactDir = join(paths.artifacts, id);
  mkdirSync(artifactDir, { recursive: true, mode: 0o700 });
  const job = {
    id, product: selected.product, scenario: selected.scenarioName,
    description: selected.description, manifestPath: selected.manifestPath,
    status: "queued", verdict: null, reasonCode: null, remedy: null,
    steps: selected.steps, nextStep: 0, attempts: 0,
    lease: null, runner: null, cancelRequested: false,
    artifactDir, createdAt: nowIso(), updatedAt: nowIso(), events: [],
  };
  atomicJson(jobPath(paths, id), job);
  return job;
}

function jobFiles(paths) {
  return readdirSync(paths.jobs).filter((name) => name.endsWith(".json")).sort();
}

async function claim(paths, owner) {
  for (const file of jobFiles(paths)) {
    const id = basename(file, ".json");
    const candidate = loadJob(paths, id);
    const stale = candidate.status === "running" && (
      !alive(candidate.lease?.pid) || Date.parse(candidate.lease?.expiresAt || 0) <= Date.now()
    );
    if (candidate.status !== "queued" && !stale) continue;
    const claimed = await updateJob(paths, id, (job) => {
      const stillStale = job.status === "running" && (
        !alive(job.lease?.pid) || Date.parse(job.lease?.expiresAt || 0) <= Date.now()
      );
      if (job.status !== "queued" && !stillStale) return job;
      job.status = "running";
      job.attempts += 1;
      job.lease = { owner, pid: process.pid, heartbeatAt: nowIso(), expiresAt: new Date(Date.now() + LEASE_MS).toISOString() };
      job.events.push({ at: nowIso(), type: stillStale ? "lease_recovered" : "started", owner });
      return job;
    });
    if (claimed.lease?.owner === owner && claimed.status === "running") return claimed;
  }
  return null;
}

async function heartbeat(paths, id, owner) {
  await updateJob(paths, id, (job) => {
    if (job.lease?.owner === owner && job.status === "running") {
      job.lease.heartbeatAt = nowIso();
      job.lease.expiresAt = new Date(Date.now() + LEASE_MS).toISOString();
    }
    return job;
  });
}

function missingEnvironment(step) {
  return step.requiredEnv.filter((name) => !/^[A-Z][A-Z0-9_]*$/.test(name) || !process.env[name]);
}

async function consumeResult(paths, id, owner, resultPath) {
  const result = readJson(resultPath);
  return updateJob(paths, id, (job) => {
    if (job.lease?.owner !== owner || job.status !== "running") return job;
    const step = job.steps[job.nextStep];
    job.events.push({ at: nowIso(), type: "step_finished", step: step.id, exitCode: result.exitCode, signal: result.signal });
    job.runner = null;
    if (result.exitCode === 0) {
      job.nextStep += 1;
      if (job.nextStep >= job.steps.length) {
        job.status = "succeeded";
        job.verdict = "PASS";
        job.lease = null;
      }
    } else {
      job.status = "failed";
      job.verdict = "SILENT";
      job.reasonCode = result.timedOut ? "STEP_TIMEOUT" : "STEP_FAILED";
      job.remedy = `Inspect ${result.logPath}`;
      job.lease = null;
    }
    return job;
  });
}

async function runClaimed(paths, initial, owner) {
  let job = initial;
  while (job.status === "running") {
    if (job.cancelRequested) {
      if (job.runner?.pid && alive(job.runner.pid)) {
        try { process.kill(-job.runner.pid, "SIGTERM"); } catch { /* already exited */ }
      }
      job = await updateJob(paths, job.id, (current) => ({ ...current, status: "cancelled", verdict: "NAMED", reasonCode: "CANCELLED", lease: null, runner: null }));
      break;
    }
    if (job.nextStep >= job.steps.length) break;
    const step = job.steps[job.nextStep];
    const missing = missingEnvironment(step);
    if (missing.length) {
      job = await updateJob(paths, job.id, (current) => ({
        ...current, status: "failed", verdict: "NAMED", reasonCode: "MISSING_ENV",
        remedy: `Set ${missing.join(", ")} in the coordinator service environment`, lease: null,
      }));
      break;
    }
    const resultPath = join(job.artifactDir, `${String(job.nextStep).padStart(2, "0")}-${step.id}.result.json`);
    const logPath = join(job.artifactDir, `${String(job.nextStep).padStart(2, "0")}-${step.id}.log`);
    if (existsSync(resultPath)) {
      job = await consumeResult(paths, job.id, owner, resultPath);
      continue;
    }
    if (!job.runner?.pid || !alive(job.runner.pid)) {
      const runner = spawn(process.execPath, [join(HERE, "step-runner.mjs"), JSON.stringify({ step, resultPath, logPath })], {
        cwd: step.cwd, detached: true, stdio: "ignore", env: process.env,
      });
      runner.unref();
      job = await updateJob(paths, job.id, (current) => {
        current.runner = { pid: runner.pid, step: step.id, resultPath, logPath, startedAt: nowIso() };
        current.events.push({ at: nowIso(), type: "step_started", step: step.id, pid: runner.pid });
        return current;
      });
    }
    await sleep(500);
    await heartbeat(paths, job.id, owner);
    job = loadJob(paths, job.id);
  }
  atomicJson(join(job.artifactDir, "manifest.json"), job);
  return job;
}

function acquireDaemon(paths, owner) {
  try {
    mkdirSync(paths.daemonLock, { mode: 0o700 });
  } catch (error) {
    if (error?.code !== "EEXIST") throw error;
    let prior = null;
    try { prior = readJson(join(paths.daemonLock, "owner.json")); } catch { /* incomplete stale lock */ }
    if (alive(prior?.pid)) throw new Error(`mobile-test coordinator already running as pid ${prior.pid}`);
    rmSync(paths.daemonLock, { recursive: true, force: true });
    mkdirSync(paths.daemonLock, { mode: 0o700 });
  }
  atomicJson(join(paths.daemonLock, "owner.json"), { owner, pid: process.pid, startedAt: nowIso() });
  return () => rmSync(paths.daemonLock, { recursive: true, force: true });
}

export async function daemon({ root, once = false } = {}) {
  const paths = ensureState(root);
  const owner = `${process.pid}-${Math.random().toString(16).slice(2)}`;
  const release = acquireDaemon(paths, owner);
  const stop = () => { release(); process.exit(0); };
  process.once("SIGTERM", stop);
  process.once("SIGINT", stop);
  try {
    do {
      const job = await claim(paths, owner);
      if (job) await runClaimed(paths, job, owner);
      if (once) return job;
      await sleep(job ? 100 : 1_000);
    } while (true);
  } finally {
    process.removeListener("SIGTERM", stop);
    process.removeListener("SIGINT", stop);
    release();
  }
}

function option(args, name) {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
}

function printJob(job) {
  const step = job.steps[job.nextStep]?.id || "complete";
  console.log(`${job.id}  ${job.status}  ${job.product}/${job.scenario}  step=${step}  attempts=${job.attempts}`);
  if (job.reasonCode) console.log(`${job.verdict} ${job.reasonCode}: ${job.remedy || "no remedy recorded"}`);
}

async function main(args = process.argv.slice(2)) {
  const command = args[0];
  const root = option(args, "--root");
  const paths = ensureState(root);
  if (command === "enqueue") {
    const job = enqueue({ root, manifest: option(args, "--manifest"), scenario: option(args, "--scenario") || "contract-smoke" });
    printJob(job);
  } else if (command === "daemon") {
    await daemon({ root, once: args.includes("--once") });
  } else if (command === "status") {
    const id = args[1]?.startsWith("--") ? null : args[1];
    const jobs = id ? [loadJob(paths, id)] : jobFiles(paths).map((file) => readJson(join(paths.jobs, file)));
    if (args.includes("--json")) console.log(JSON.stringify(jobs, null, 2)); else jobs.forEach(printJob);
  } else if (command === "follow") {
    const id = args[1];
    if (!id) throw new Error("follow requires a job id");
    let previous = "";
    while (true) {
      const job = loadJob(paths, id);
      const line = `${job.status}:${job.nextStep}:${job.updatedAt}`;
      if (line !== previous) printJob(job);
      previous = line;
      if (["succeeded", "failed", "cancelled"].includes(job.status)) break;
      await sleep(1_000);
    }
  } else if (command === "cancel") {
    const id = args[1];
    if (!id) throw new Error("cancel requires a job id");
    printJob(await updateJob(paths, id, (job) => ({ ...job, cancelRequested: true })));
  } else if (command === "artifacts") {
    const job = loadJob(paths, args[1]);
    console.log(job.artifactDir);
  } else if (command === "doctor") {
    console.log(`state=${paths.root}`);
    console.log(`mode=${(statSync(paths.root).mode & 0o777).toString(8)}`);
    console.log(`node=${process.version}`);
    loadScenario(option(args, "--manifest"), option(args, "--scenario") || "contract-smoke");
    console.log("mobile-test coordinator ready");
  } else {
    console.log("usage: mobile-test.mjs enqueue|daemon|status|follow|cancel|artifacts|doctor [options]");
    process.exitCode = command ? 2 : 0;
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] || "").href) {
  main().catch((error) => { console.error(`mobile-test: ${error.message}`); process.exitCode = 1; });
}
