/**
 * taskStreamWithRecovery.test.ts — `npx tsx lib/taskStreamWithRecovery.test.ts`.
 *
 * The connectivity+vibing pass gave `streamTaskOutput` an `onEnd` callback and
 * a `?since=` resume, and gave `taskStreamRecovery.ts` the policy. VibeCodingView
 * wired both by hand. FOUR other web call sites — PreviewPane, RuntimeLabView,
 * WebReloadView (x2) and app/dashboard/page.tsx — passed no `onEnd` at all, so
 * they kept the original defect exactly: a severed relay tunnel froze the
 * transcript on its last frame, under a spinner, over a task that was still
 * running fine on the box.
 *
 * These tests pin the wrapper's behaviour AND that every call site uses it,
 * because a helper nobody calls fixes nothing.
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { streamTaskOutputWithRecovery, type TaskStreamHealth } from "./taskStreamWithRecovery";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB = join(HERE, "..");

/** A fake client that lets a test end a stream on demand. */
function fakeClient() {
  const calls: Array<{ since: number; end: (info: { sawDone: boolean; cancelled: boolean; error?: string }) => void; chunk: (s: string) => void }> = [];
  return {
    calls,
    streamTaskOutput(
      _taskId: string,
      onChunk: (chunk: string) => void,
      _onEvent?: (e: Record<string, unknown>) => void,
      opts?: { since?: number; onEnd?: (info: { sawDone: boolean; cancelled: boolean; error?: string }) => void },
    ) {
      calls.push({ since: Number(opts?.since || 0), end: opts?.onEnd || (() => {}), chunk: onChunk });
      return () => {};
    },
  };
}

test("an interrupted stream is named, not swallowed", () => {
  const client = fakeClient();
  const seen: TaskStreamHealth[] = [];
  streamTaskOutputWithRecovery(client, "t1", () => {}, undefined, { onHealth: (h) => seen.push(h) });

  // A clean EOF with no `done` frame — exactly what a dropped relay tunnel
  // looks like, and exactly the case the old code treated as "fine".
  client.calls[0].end({ sawDone: false, cancelled: false });

  const named = seen.filter((h) => h && h.kind === "reattaching");
  assert.equal(named.length, 1, "a dropped stream must produce a visible 'reattaching' state");
  assert.match(String(named[0]!.message), /task has not reported a failure/, "the stream drop must not be presented as a task failure");
});

test("a finished stream says nothing", () => {
  const client = fakeClient();
  const seen: TaskStreamHealth[] = [];
  streamTaskOutputWithRecovery(client, "t1", () => {}, undefined, { onHealth: (h) => seen.push(h) });
  client.calls[0].end({ sawDone: true, cancelled: false });
  assert.ok(!seen.some((h) => h !== null), "a task that really finished must not raise a recovery banner");
});

test("a local teardown says nothing", () => {
  const client = fakeClient();
  const seen: TaskStreamHealth[] = [];
  const stop = streamTaskOutputWithRecovery(client, "t1", () => {}, undefined, { onHealth: (h) => seen.push(h) });
  client.calls[0].end({ sawDone: false, cancelled: true });
  stop();
  assert.ok(!seen.some((h) => h && h.kind !== "reattaching" && h.kind !== "lost") || !seen.some((h) => h !== null),
    "switching tasks must not accuse the transport");
});

test("the reattach resumes from bytes received, never from zero", async () => {
  const client = fakeClient();
  streamTaskOutputWithRecovery(client, "t1", () => {}, undefined, {});
  client.calls[0].chunk("hello world");   // 11 bytes
  client.calls[0].end({ sawDone: false, cancelled: false });
  // First rung is 1000 ms; wait past it.
  await new Promise((r) => setTimeout(r, 1200));
  assert.equal(client.calls.length, 2, "the ladder must actually resubscribe");
  assert.equal(client.calls[1].since, 11, "resuming from 0 replays a transcript the user already read");
});

test("a chunk clears the banner and resets the ladder", () => {
  const client = fakeClient();
  const seen: TaskStreamHealth[] = [];
  streamTaskOutputWithRecovery(client, "t1", () => {}, undefined, { onHealth: (h) => seen.push(h) });
  client.calls[0].end({ sawDone: false, cancelled: false });
  assert.ok(seen.some((h) => h && h.kind === "reattaching"));
  client.calls[0].chunk("alive again");
  assert.equal(seen[seen.length - 1], null, "a live chunk must clear the banner — a stale warning is its own lie");
});

test("give-up hands over a Reattach route, not just a sentence", () => {
  const client = fakeClient();
  let last: TaskStreamHealth = null;
  streamTaskOutputWithRecovery(client, "t1", () => {}, undefined, { onHealth: (h) => (last = h) });
  // Exhaust the ladder without waiting on timers: each end() bumps `attempt`.
  for (let i = 0; i < 8; i += 1) client.calls[0].end({ sawDone: false, cancelled: false });
  const health = last as TaskStreamHealth;
  assert.ok(health && health.kind === "lost", "the ladder must eventually stop and say so");
  assert.equal(typeof health!.reattach, "function", "give-up without a button is a dead end with a sentence");
});

/**
 * THE WIRING GUARD. The wrapper existing is not the deliverable; the call
 * sites using it is. Every web `streamTaskOutput` consumer must either go
 * through the wrapper or pass its own `onEnd` — a bare call is the freeze.
 */
test("no web surface subscribes to task output without an end handler", () => {
  const sites = [
    "components/dashboard/PreviewPane.tsx",
    "components/dashboard/RuntimeLabView.tsx",
    "components/dashboard/WebReloadView.tsx",
    "app/dashboard/page.tsx",
    "components/dashboard/VibeCodingView.tsx",
  ];
  for (const rel of sites) {
    const src = readFileSync(join(WEB, rel), "utf8");
    const bare = src.split("agentClient.streamTaskOutput(").length - 1;
    const wrapped = src.split("streamTaskOutputWithRecovery(").length - 1;
    // COUNT the onEnd handlers, do not merely detect one. This assertion used
    // to be `bare === 0 || /onEnd:\s*\(/.test(src)` — a FILE-level test — and
    // VibeCodingView.tsx passed it while carrying two bare call sites and one
    // onEnd: the hand-wired transcript stream satisfied the regex and the graph
    // node's tail rode along unguarded, freezing on its last line with nothing
    // said. A per-file boolean cannot express a per-call-site obligation.
    const guarded = src.split(/onEnd:\s*\(/).length - 1;
    assert.ok(
      bare <= guarded,
      `${rel} has ${bare} direct agentClient.streamTaskOutput call(s) but only ${guarded} onEnd handler(s) — ` +
        `at least one severed stream freezes this surface on its last frame (wrapped=${wrapped}). ` +
        "Use streamTaskOutputWithRecovery, or give every call site its own onEnd.",
    );
  }
});

test("mobile's hand-wired stream call sites each carry since + onEnd", () => {
  // Mobile has no streamTaskOutputWithRecovery wrapper — every screen wires the
  // ladder itself, so the obligation is per call site and nothing was checking
  // it at all. dogfood.tsx streamed a whole session tail with neither `since`
  // nor `onEnd` for exactly that reason.
  const repoRoot = join(WEB, "..");
  const sites = ["mobile/app/(tabs)/tasks.tsx", "mobile/app/(tabs)/dogfood.tsx"];
  for (const rel of sites) {
    const src = readFileSync(join(repoRoot, rel), "utf8");
    const bare = src.split("quicClient.streamTaskOutput(").length - 1;
    if (bare === 0) continue;
    const guarded = src.split(/onEnd:\s*\(/).length - 1;
    assert.ok(
      bare <= guarded,
      `${rel} has ${bare} streamTaskOutput call(s) but only ${guarded} onEnd handler(s) — ` +
        "a dropped stream leaves that view frozen on its last line with nothing said.",
    );
  }
});

/** The shared notice must be the only place the banner is drawn, or five
 *  hand-rolled copies drift the way the relay-auth matchers already have. */
test("the recovery banner is rendered from one component", () => {
  for (const rel of [
    "components/dashboard/PreviewPane.tsx",
    "components/dashboard/RuntimeLabView.tsx",
    "components/dashboard/WebReloadView.tsx",
    "app/dashboard/page.tsx",
  ]) {
    const src = readFileSync(join(WEB, rel), "utf8");
    assert.match(src, /<StreamHealthNotice/, `${rel} wires the ladder but renders nothing — a reattach the user cannot see is a different silence`);
  }
});

/** The raw opencode console lane (audit 2026-08-12 §2): mobile renders RAW
 *  runner stdout; web chat had no consumer — the transport accepted rawSince
 *  but dropped raw/re raw_replay bytes into the event bus. Three guards:
 *  1. agent-client dispatches raw frames to a dedicated onRaw (never onLine —
 *     groomed vs raw would double-render with different text).
 *  2. the recovery wrapper threads onRaw through, and consumes raw frames
 *     there rather than double-passing them to onEvent.
 *  3. the chat subscribes with rawSince + onRaw and renders a console panel.
 *  Deleting any one of the three reopens the "raw bytes arrive, nothing shows"
 *  freeze this lane exists to prevent.
 */
test("the raw opencode console lane is dispatched and consumed on web", () => {
  const client = readFileSync(join(WEB, "lib/agent-client.ts"), "utf8");
  assert.ok(
    /onRaw\?: \(event: \{ type: "raw" \| "raw_replay"/.test(client) || client.includes("onRaw?"),
    "agent-client does not expose an onRaw callback — raw frames cannot reach a consumer",
  );
  assert.ok(
    client.includes('event?.type === "raw"') && client.includes("opts?.onRaw"),
    "agent-client does not route raw/raw_replay frames to onRaw — they fall into the event bus and vanish",
  );

  const recovery = readFileSync(join(WEB, "lib/taskStreamWithRecovery.ts"), "utf8");
  assert.ok(
    recovery.includes("onRaw?:") && recovery.includes("options?.onRaw"),
    "streamTaskOutputWithRecovery does not thread onRaw through to the transport",
  );

  const chat = readFileSync(join(WEB, "app/dashboard/page.tsx"), "utf8");
  assert.ok(
    chat.includes("onRaw:") && chat.includes("rawSince") && chat.includes("rawOutput"),
    "web chat does not subscribe with rawSince+onRaw into a raw buffer",
  );
  assert.ok(
    chat.includes("Runner details") && chat.includes("AnsiConsoleText text={summarizeRawConsole(rawOutput"),
    "web chat has no foldable raw console panel rendering the raw bytes",
  );
});
