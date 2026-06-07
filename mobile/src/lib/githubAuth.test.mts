// githubAuth.test.mts — pure GitHub auth helpers. Run: npx tsx src/lib/githubAuth.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { makeGitHubOnAuth, gitHubNetOptions, parseRepoSlug, normalizeRepoUrl, looksLikeGitHubToken } from "./githubAuth.ts";

test("makeGitHubOnAuth returns token-as-username basic auth", () => {
  const onAuth = makeGitHubOnAuth("  ghp_abc123  ");
  assert.deepEqual(onAuth("https://github.com/x/y.git"), { username: "ghp_abc123", password: "x-oauth-basic" });
});

test("gitHubNetOptions bundles http + onAuth", () => {
  const http = {} as any;
  const net = gitHubNetOptions("tok", http, "https://cors.example");
  assert.equal(net.http, http);
  assert.equal(net.corsProxy, "https://cors.example");
  assert.deepEqual(net.onAuth!("u"), { username: "tok", password: "x-oauth-basic" });
});

test("parseRepoSlug accepts owner/repo, .git, and full URLs", () => {
  assert.deepEqual(parseRepoSlug("kivanccakmak/yaver.io"), { owner: "kivanccakmak", repo: "yaver.io" });
  assert.deepEqual(parseRepoSlug("owner/repo.git"), { owner: "owner", repo: "repo" });
  assert.deepEqual(parseRepoSlug("https://github.com/owner/repo"), { owner: "owner", repo: "repo" });
  assert.deepEqual(parseRepoSlug("https://github.com/owner/repo.git"), { owner: "owner", repo: "repo" });
  assert.deepEqual(parseRepoSlug("git@github.com:owner/repo.git"), { owner: "owner", repo: "repo" });
});

test("parseRepoSlug rejects junk", () => {
  assert.equal(parseRepoSlug(""), null);
  assert.equal(parseRepoSlug("just-a-name"), null);
  assert.equal(parseRepoSlug("https://gitlab.com/owner/repo"), null);
  assert.equal(parseRepoSlug("a/b/c"), null);
});

test("normalizeRepoUrl canonicalizes; throws on junk", () => {
  assert.equal(normalizeRepoUrl("owner/repo"), "https://github.com/owner/repo.git");
  assert.equal(normalizeRepoUrl("https://github.com/o/r"), "https://github.com/o/r.git");
  assert.throws(() => normalizeRepoUrl("nope"));
});

test("looksLikeGitHubToken recognizes common shapes", () => {
  assert.ok(looksLikeGitHubToken("ghp_" + "a".repeat(36)));
  assert.ok(looksLikeGitHubToken("github_pat_" + "A".repeat(30)));
  assert.ok(looksLikeGitHubToken("a".repeat(40))); // 40-hex classic
  assert.ok(!looksLikeGitHubToken("hello"));
  assert.ok(!looksLikeGitHubToken(""));
});
