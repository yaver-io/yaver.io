// Shared helper: the data directory a headless harness writes to.
//
// Precedence:
//   1. YMH_DATA_DIR env var (set by the harness when it wants a
//      specific isolated sandbox — e.g. a test case's tmpDir).
//   2. ~/.yaver/mobile-headless/default  (shared dev scratch).
//
// Every shim that writes to disk (async-storage, secure-store) goes
// through here so a test run is fully isolated by setting one env.

import * as os from "node:os";
import * as path from "node:path";

export function dataDir(): string {
  const env = process.env.YMH_DATA_DIR;
  if (env && env.trim()) return env;
  return path.join(os.homedir(), ".yaver", "mobile-headless", "default");
}
