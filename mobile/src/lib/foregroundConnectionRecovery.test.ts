/**
 * `npx tsx src/lib/foregroundConnectionRecovery.test.ts`
 *
 * Regression for the native lifecycle failure where the app retained
 * `connected` across suspension after iOS had discarded the real relay path.
 */
import assert from "node:assert/strict";
import test from "node:test";
import {
  recoverStaleConnectionOnForeground,
  type ForegroundRecoveryClient,
} from "./foregroundConnectionRecovery.ts";

class FakeClient implements ForegroundRecoveryClient {
  connectionState: ForegroundRecoveryClient["connectionState"] = "connected";
  probes = 0;
  reconnects = 0;
  private verdict: Promise<boolean>;

  constructor(verdict: boolean | Promise<boolean>) {
    this.verdict = Promise.resolve(verdict);
  }

  async verifyStillConnected(): Promise<boolean> {
    this.probes++;
    return this.verdict;
  }

  fullReconnect(): void {
    this.reconnects++;
    this.connectionState = "connecting";
  }
}

test("a stale connected flag starts the full transport ladder on foreground", async () => {
  const client = new FakeClient(false);
  const recovered = await recoverStaleConnectionOnForeground({ client, isCurrent: () => true });

  assert.equal(recovered, true);
  assert.equal(client.probes, 1);
  assert.equal(client.reconnects, 1);
});

test("a live connection stays uninterrupted", async () => {
  const client = new FakeClient(true);
  const recovered = await recoverStaleConnectionOnForeground({ client, isCurrent: () => true });

  assert.equal(recovered, false);
  assert.equal(client.probes, 1);
  assert.equal(client.reconnects, 0);
  assert.equal(client.connectionState, "connected");
});

test("a rejected health probe also takes the reconnect route", async () => {
  const client = new FakeClient(Promise.reject(new Error("native socket closed")));

  assert.equal(
    await recoverStaleConnectionOnForeground({ client, isCurrent: () => true }),
    true,
  );
  assert.equal(client.reconnects, 1);
});

test("a resume probe cannot reconnect after the app backgrounds again", async () => {
  let settle!: (value: boolean) => void;
  const verdict = new Promise<boolean>((resolve) => { settle = resolve; });
  const client = new FakeClient(verdict);
  let current = true;

  const recovery = recoverStaleConnectionOnForeground({ client, isCurrent: () => current });
  current = false;
  settle(false);

  assert.equal(await recovery, false);
  assert.equal(client.reconnects, 0);
});

test("a competing reconnect supersedes the resume probe", async () => {
  let settle!: (value: boolean) => void;
  const verdict = new Promise<boolean>((resolve) => { settle = resolve; });
  const client = new FakeClient(verdict);

  const recovery = recoverStaleConnectionOnForeground({ client, isCurrent: () => true });
  client.connectionState = "connecting";
  settle(false);

  assert.equal(await recovery, false);
  assert.equal(client.reconnects, 0);
});

test("negative control: the old flag-only resume policy leaves a dead path trusted", () => {
  const client = new FakeClient(false);

  // This is the removed policy: `if (connectionState === "connected") return`.
  if (client.connectionState !== "connected") client.fullReconnect();

  assert.equal(client.probes, 0);
  assert.equal(client.reconnects, 0);
  assert.equal(client.connectionState, "connected");
});
