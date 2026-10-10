#!/usr/bin/env node
import { spawn } from "node:child_process";
import { chmodSync, openSync, renameSync, writeFileSync } from "node:fs";

const spec = JSON.parse(process.argv[2] || "null");
if (!spec?.step?.command?.length || !spec.resultPath || !spec.logPath) {
  console.error("step-runner: invalid specification");
  process.exit(2);
}
const startedAt = new Date().toISOString();
const log = openSync(spec.logPath, "a", 0o600);
const child = spawn(spec.step.command[0], spec.step.command.slice(1), {
  cwd: spec.step.cwd,
  env: process.env,
  stdio: ["ignore", log, log],
});
let timedOut = false;
const timeout = setTimeout(() => {
  timedOut = true;
  try { child.kill("SIGTERM"); } catch { /* exited */ }
  setTimeout(() => { try { child.kill("SIGKILL"); } catch { /* exited */ } }, 5_000).unref();
}, spec.step.timeoutMs);
timeout.unref();

child.once("error", (error) => finish(127, null, error.message));
child.once("exit", (exitCode, signal) => finish(exitCode ?? 1, signal, null));

let finished = false;
function finish(exitCode, signal, error) {
  if (finished) return;
  finished = true;
  clearTimeout(timeout);
  const result = {
    step: spec.step.id, exitCode, signal, error, timedOut,
    startedAt, finishedAt: new Date().toISOString(), logPath: spec.logPath,
  };
  const temporary = `${spec.resultPath}.${process.pid}.part`;
  writeFileSync(temporary, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
  chmodSync(temporary, 0o600);
  renameSync(temporary, spec.resultPath);
  process.exitCode = exitCode;
}
