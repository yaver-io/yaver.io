import test from "node:test";
import assert from "node:assert/strict";

import { buildWatchPrompt, classifyWatchPrompt } from "./watchPrompt.ts";

test("walking ideas default to idea capture, not implementation", () => {
  assert.equal(classifyWatchPrompt("idea for sfmg owner mode sponsor board"), "idea-capture");
  const plan = buildWatchPrompt("idea for sfmg owner mode sponsor board");
  assert.equal(plan.mode, "idea-capture");
  assert.match(plan.prompt, /Do not edit code/i);
  assert.match(plan.prompt, /acceptance criteria/i);
});

test("explicit build request becomes implementation", () => {
  const plan = buildWatchPrompt("add this to sfmg owner mode");
  assert.equal(plan.mode, "implementation");
  assert.match(plan.prompt, /permission to work/i);
});

test("confirmed operational commands become implementation", () => {
  const plan = buildWatchPrompt("deploy to production");
  assert.equal(plan.mode, "implementation");
  assert.match(plan.prompt, /permission to work/i);
});

test("browser automation prompt is consent-safe", () => {
  const plan = buildWatchPrompt("open browser and check the pricing page");
  assert.equal(plan.mode, "browser-automation");
  assert.match(plan.prompt, /Stop for login, payment, CAPTCHA, consent/i);
});

test("remote runtime question stays summary-first", () => {
  const plan = buildWatchPrompt("what is the status of my remote runtime session");
  assert.equal(plan.mode, "remote-runtime-question");
  assert.match(plan.prompt, /one-sentence watch summary/i);
});
