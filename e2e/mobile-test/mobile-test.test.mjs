import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { enqueue } from "./mobile-test.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const cli = join(here, "mobile-test.mjs");
const waitFor = async (probe, timeout = 10_000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = probe();
    if (value) return value;
    await new Promise((done) => setTimeout(done, 50));
  }
  throw new Error("condition timed out");
};

test("a restarted coordinator adopts the live step instead of duplicating it", async () => {
  const root = mkdtempSync(join(tmpdir(), "yaver-mobile-test-"));
  const fixture = join(root, "fixture");
  mkdirSync(fixture);
  const marker = join(fixture, "marker.txt");
  const worker = join(fixture, "worker.mjs");
  writeFileSync(worker, `import {appendFileSync} from 'node:fs'; setTimeout(() => appendFileSync(${JSON.stringify(marker)}, 'once\\n'), 900); setTimeout(() => {}, 1100);\n`);
  const manifest = join(fixture, "product.json");
  writeFileSync(manifest, JSON.stringify({
    product: "fixture", projectRoot: ".", scenarios: { interruption: { steps: [
      { id: "slow", command: [process.execPath, worker], timeoutMs: 10_000 },
    ] } },
  }));
  const job = enqueue({ root, manifest, scenario: "interruption" });
  const first = spawn(process.execPath, [cli, "daemon", "--once", "--root", root], { stdio: "ignore" });
  const jobFile = join(root, "jobs", `${job.id}.json`);
  const running = await waitFor(() => {
    const value = JSON.parse(readFileSync(jobFile));
    return value.runner?.pid ? value : null;
  });
  process.kill(first.pid, "SIGKILL");
  assert.equal(process.kill(running.runner.pid, 0), true);
  await new Promise((done) => setTimeout(done, 1_200));
  execFileSync(process.execPath, [cli, "daemon", "--once", "--root", root], { timeout: 10_000 });
  const completed = JSON.parse(readFileSync(jobFile));
  assert.equal(completed.status, "succeeded");
  assert.equal(readFileSync(marker, "utf8"), "once\n");
  assert.ok(completed.events.some((event) => event.type === "lease_recovered"));
});
test("missing live credentials fail NAMED with an executable configuration route", () => {
  const root = mkdtempSync(join(tmpdir(), "yaver-mobile-test-"));
  const fixture = join(root, "product.json");
  writeFileSync(fixture, JSON.stringify({ product: "fixture", projectRoot: ".", scenarios: { auth: {
    requiredEnv: ["YAVER_FIXTURE_SECRET"], steps: [{ id: "never", command: [process.execPath, "--version"] }],
  } } }));
  const job = enqueue({ root, manifest: fixture, scenario: "auth" });
  execFileSync(process.execPath, [cli, "daemon", "--once", "--root", root]);
  const completed = JSON.parse(readFileSync(join(root, "jobs", `${job.id}.json`)));
  assert.equal(completed.verdict, "NAMED");
  assert.equal(completed.reasonCode, "MISSING_ENV");
  assert.match(completed.remedy, /YAVER_FIXTURE_SECRET/);
});
