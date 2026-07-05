// carSurfaceIntent.test.mts
// Run: npx tsx src/lib/carSurfaceIntent.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyCarSurfaceIntent,
  executeCarSurfaceIntent,
} from "./carSurfaceIntent.ts";

test("classifies join next Teams meeting as car meeting_join_next", () => {
  const intent = classifyCarSurfaceIntent("join my next Teams meeting");
  assert.equal(intent?.kind, "meeting_join_next");
  assert.equal(intent?.payload.provider, "teams");
  assert.equal(intent?.payload.open, true);
  assert.equal(intent?.payload.openMode, "browser");
  assert.equal(intent?.payload.surface, "car");
});

test("classifies next meeting lookup without unsupported surface payload", () => {
  const intent = classifyCarSurfaceIntent("when is my next meeting");
  assert.equal(intent?.kind, "meeting_next");
  assert.equal(intent?.payload.withinHours, 24);
  assert.equal("surface" in (intent?.payload || {}), false);
});

test("classifies incoming email check", () => {
  const intent = classifyCarSurfaceIntent("check my incoming Gmail");
  assert.equal(intent?.kind, "mail_unread");
  assert.equal(intent?.payload.provider, "gmail");
  assert.equal(intent?.payload.onlyPersonal, true);
});

test("classifies GitHub pull request check", () => {
  const intent = classifyCarSurfaceIntent("check my GitHub PRs");
  assert.equal(intent?.kind, "git_prs");
  assert.equal(intent?.payload.provider, "github");
  assert.equal(intent?.payload.state, "open");
});

test("classifies GitLab pipeline status", () => {
  const intent = classifyCarSurfaceIntent("what is the GitLab pipeline status");
  assert.equal(intent?.kind, "git_ci_status");
  assert.equal(intent?.payload.provider, "gitlab");
});

test("classifies YouTube live stream open", () => {
  const intent = classifyCarSurfaceIntent("open Hasan Arda Kasikci live stream");
  assert.equal(intent?.kind, "media_open");
  assert.equal(intent?.payload.provider, "auto");
  assert.equal(intent?.payload.query, "Hasan Arda Kasikci");
  assert.equal(intent?.payload.live, true);
  assert.equal(intent?.payload.open, true);
});

test("classifies Twitch search open", () => {
  const intent = classifyCarSurfaceIntent("open Twitch hasanabi");
  assert.equal(intent?.kind, "media_open");
  assert.equal(intent?.payload.provider, "twitch");
  assert.equal(intent?.payload.query, "hasanabi");
});

test("classifies Yandex traffic query", () => {
  const intent = classifyCarSurfaceIntent("check Yandex traffic on 15 July Bridge");
  assert.equal(intent?.kind, "maps_open");
  assert.equal(intent?.payload.provider, "yandex");
  assert.equal(intent?.payload.query, "15 July Bridge");
  assert.equal(intent?.payload.traffic, true);
});

test("execute calls ops and formats meeting response", async () => {
  const calls: Array<{ verb: string; payload: Record<string, unknown> }> = [];
  const result = await executeCarSurfaceIntent(
    "join my next zoom call",
    async (verb, payload) => {
      calls.push({ verb, payload });
      return { title: "Customer sync", opened: true };
    },
  );
  assert.equal(result.handled, true);
  assert.equal(calls[0].verb, "meeting_join_next");
  assert.match(result.spoken, /Opening Customer sync/);
});

test("execute calls ops and formats git response", async () => {
  const calls: Array<{ verb: string; payload: Record<string, unknown> }> = [];
  const result = await executeCarSurfaceIntent(
    "check my GitHub PRs",
    async (verb, payload) => {
      calls.push({ verb, payload });
      return { spoken: "GitHub has 2 pull requests." };
    },
  );
  assert.equal(result.handled, true);
  assert.equal(calls[0].verb, "git_prs");
  assert.match(result.spoken, /2 pull requests/);
});

test("execute calls ops and formats media response", async () => {
  const calls: Array<{ verb: string; payload: Record<string, unknown> }> = [];
  const result = await executeCarSurfaceIntent(
    "open YouTube lo-fi live stream",
    async (verb, payload) => {
      calls.push({ verb, payload });
      return { spoken: "Opening YouTube live results for lo-fi." };
    },
  );
  assert.equal(result.handled, true);
  assert.equal(calls[0].verb, "media_open");
  assert.match(result.spoken, /YouTube live/);
});

test("send email without address is handled as phone handoff", async () => {
  const result = await executeCarSurfaceIntent(
    "send an email that I am late",
    async () => {
      throw new Error("should not call ops");
    },
  );
  assert.equal(result.handled, true);
  assert.match(result.spoken, /specific email address/i);
});
