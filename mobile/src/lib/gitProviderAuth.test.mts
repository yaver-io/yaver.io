// gitProviderAuth.test.mts — multi-provider git auth shapes + url normalization.
// Run: npx tsx src/lib/gitProviderAuth.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  detectGitProvider,
  makeGitOnAuth,
  makeOnAuthForUrl,
  normalizeGitUrl,
  repoLabelFromUrl,
} from "./gitProviderAuth.ts";

test("detectGitProvider recognises hosts incl self-hosted gitlab/gitea", () => {
  assert.equal(detectGitProvider("https://github.com/a/b.git"), "github");
  assert.equal(detectGitProvider("git@github.com:a/b.git"), "github");
  assert.equal(detectGitProvider("https://gitlab.com/a/b.git"), "gitlab");
  assert.equal(detectGitProvider("https://gitlab.mycorp.io/a/b.git"), "gitlab");
  assert.equal(detectGitProvider("https://gitea.example.com/a/b.git"), "gitlab");
  assert.equal(detectGitProvider("https://bitbucket.org/a/b.git"), "bitbucket");
  assert.equal(detectGitProvider("https://git.example.com/a/b.git"), "generic");
});

test("makeGitOnAuth uses the right PAT pairing per provider", () => {
  assert.deepEqual(makeGitOnAuth("github", "ghp_x")("u"), { username: "ghp_x", password: "x-oauth-basic" });
  assert.deepEqual(makeGitOnAuth("gitlab", "glpat_x")("u"), { username: "oauth2", password: "glpat_x" });
  assert.deepEqual(makeGitOnAuth("bitbucket", "bb_x")("u"), { username: "x-token-auth", password: "bb_x" });
  assert.deepEqual(makeGitOnAuth("generic", "tok")("u"), { username: "tok", password: "tok" });
});

test("explicit username overrides the provider default (self-hosted basic auth)", () => {
  assert.deepEqual(makeGitOnAuth("gitlab", "pw", "deploy-user")("u"), {
    username: "deploy-user",
    password: "pw",
  });
});

test("makeOnAuthForUrl picks the provider from the URL passed at call time", () => {
  const onAuth = makeOnAuthForUrl("tok");
  assert.deepEqual(onAuth("https://gitlab.com/a/b.git"), { username: "oauth2", password: "tok" });
  assert.deepEqual(onAuth("https://github.com/a/b.git"), { username: "tok", password: "x-oauth-basic" });
});

test("normalizeGitUrl handles owner/repo, ssh, and https forms", () => {
  assert.equal(normalizeGitUrl("octocat/Hello-World"), "https://github.com/octocat/Hello-World.git");
  assert.equal(normalizeGitUrl("git@gitlab.com:grp/app.git"), "https://gitlab.com/grp/app.git");
  assert.equal(normalizeGitUrl("https://gitlab.com/grp/app"), "https://gitlab.com/grp/app.git");
  assert.equal(normalizeGitUrl("https://gitlab.com/grp/app.git"), "https://gitlab.com/grp/app.git");
  assert.throws(() => normalizeGitUrl("not a url"), /not a full git url|not a GitHub repo/);
});

test("repoLabelFromUrl extracts owner/repo for display", () => {
  assert.equal(repoLabelFromUrl("https://github.com/octocat/Hello-World.git"), "octocat/Hello-World");
  assert.equal(repoLabelFromUrl("git@gitlab.com:grp/app.git"), "grp/app");
});
