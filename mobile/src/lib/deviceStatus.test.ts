/**
 * deviceStatus.test.ts — `npx tsx src/lib/deviceStatus.test.ts`.
 * No RN, no jest — pure harness for the runner-fetch classifier.
 */
import { classifyRunnerFetchOutcome } from "./deviceStatusRunnerProbe";

let failures = 0;

function check(name: string, cond: boolean) {
  if (cond) {
    console.log(`ok   ${name}`);
  } else {
    failures++;
    console.error(`FAIL ${name}`);
  }
}

console.log("classifyRunnerFetchOutcome");
check("aborted -> timed-out", classifyRunnerFetchOutcome({ aborted: true }) === "timed-out");
check("500 -> http-error", classifyRunnerFetchOutcome({ status: 500 }) === "http-error");
check("200 -> ok", classifyRunnerFetchOutcome({ status: 200 }) === "ok");
check(
  "network error -> network-error",
  classifyRunnerFetchOutcome({ networkError: new TypeError("Network request failed") }) === "network-error",
);
check("empty input defaults to network-error", classifyRunnerFetchOutcome({}) === "network-error");

process.exit(failures);
