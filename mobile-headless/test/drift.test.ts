// Drift detector — only the handful of QuicClient methods that
// mobile-headless commits to mirroring need to survive in the
// mobile lib. Everything else is app-internal and the harness
// doesn't care. Failing CI here is a signal that mobile broke an
// expectation the headless surrogate (and everyone who tests
// against it) relies on.

import { describe, it, expect } from "bun:test";
import * as fs from "node:fs";
import * as path from "node:path";

const REQUIRED_ON_QUIC_CLIENT = [
  // devices + agent
  "infraSummary",
  "getRunners",
  // install catalogue (new this cycle, easy to remove accidentally)
  "listInstallables",
  "installTool",
  "respondInstallSudo",
  "subscribeStream",
  // wizard — the mobile "new project" flow hinges on these
  "wizardStart",
  "wizardAnswer",
  "wizardGenerate",
  "wizardQuestions",
];

describe("mobile lib surface drift", () => {
  it("keeps the methods mobile-headless expects to exist", () => {
    const quicPath = path.resolve(__dirname, "../../mobile/src/lib/quic.ts");
    const src = fs.readFileSync(quicPath, "utf8");
    const missing = REQUIRED_ON_QUIC_CLIENT.filter(
      (name) => !new RegExp(`\\b(?:async\\s+)?${name}\\s*\\(`).test(src),
    );
    if (missing.length) {
      console.error(
        "Methods disappeared from mobile/src/lib/quic.ts — the headless\n" +
        "surrogate and any test that calls these will break:\n" +
        missing.map((m) => "  - " + m).join("\n"),
      );
    }
    expect(missing).toEqual([]);
  });
});
