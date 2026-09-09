import assert from "node:assert/strict";
import test from "node:test";

import { addTerminalSSHProfile } from "./sshProfile.ts";

test("mobile terminal carries the safe persisted shell profile", () => {
  const params = addTerminalSSHProfile(new URLSearchParams({ token: "session" }), {
    shell: "zsh",
    tmux: true,
    tmuxSession: "work.main",
  });
  assert.equal(params.get("profile_shell"), "zsh");
  assert.equal(params.get("profile_tmux"), "work.main");
});

test("mobile terminal reduces injected profile values to fixed defaults", () => {
  const params = addTerminalSSHProfile(new URLSearchParams(), {
    shell: "zsh; touch /tmp/nope",
    tmux: true,
    tmuxSession: "work; touch /tmp/nope",
  });
  assert.equal(params.get("profile_shell"), "default");
  assert.equal(params.get("profile_tmux"), "yaver");
  assert.doesNotMatch(params.toString(), /touch|%2Ftmp/);
});
