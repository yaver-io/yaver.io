---
master: claude
doer: codex
---

# Runner wiring: opencode and glm, in the Go agent and over MCP

## Why this exists

Every finding below is from a real failure on the Mac mini on 2026-07-17, read
out of `autorun_status`. None is hypothetical. Runners are the product's engine;
when their wiring lies, a loop burns hours and blames the wrong thing — that is
already the documented history of this box (`7a5c652d7`, `9d1eed683`).

**The law: report a runner's readiness as the truth, or refuse. Never as a
guess.** A runner that is "found but not working" while printing its own version
number is the product lying about its own engine.

## Ground rules

- Read `desktop/agent/env_profile.go:282`, `autorun.go:319`,
  `agent_mesh.go:578` and `agent_runner_resume.go:25` **first**. They all state
  the same fact and it is the key to this whole file.
- Do not add an API-key path for Claude. Subscription login only — that law
  stands. z.ai/GLM is a different service where a key is the legitimate auth;
  keep the two straight.
- Prove each fix against the real failure text quoted below, not against a
  mock.

## P0 — `glm` is not a binary, and the product must stop implying it is

`glm` is **the `claude` binary pointed at z.ai's Anthropic-compatible
endpoint**. `env_profile.go:282` maps `glm|zai|z.ai → claude`; nothing anywhere
does `LookPath("glm")`, correctly, because no such binary exists on any machine,
ever.

The resolver is right and the *surfaces* are wrong. A human — or an agent —
naturally probes `command -v glm`, gets nothing, and concludes glm is
unavailable. That happened in this very session and sent a task file to the
wrong seat.

- A runner's availability is a **triple**: (binary, endpoint, credential). Model
  it as one. `glm` = binary `claude` + endpoint z.ai + a z.ai key. Availability
  reporting must say *which leg* is missing.
- **"glm not installed" must be an unsayable sentence.** The only truthful
  variants are "claude binary missing" or "z.ai credential missing".
- This matters operationally right now: the mini's `claude` is **not signed in**,
  so the claude master seat is dead — but **glm rides the same binary with z.ai
  auth and does not need the Claude subscription at all**. glm is the available
  master on that box, and nothing surfaces that. `yaver runner <box> status`
  should say so instead of just "claude ✗ not configured".

## P1 — glm ran headless and died; it must ride the TUI like claude

Real failure, mini, `toolchain-and-remote-git:opencode`:

```
master glm failed to plan iteration 1: exit status 1:
{"type":"system","subtype":"init","cwd":"/Users/pokayoke/Workspace/yaver-autorun-toolchain",
 "session_id":"43266e44-…"
```

That payload is **claude's `--output-format stream-json` init frame**. So glm was
invoked headless. The standing law is: **never `-p`/headless — it fakes an "OAuth
expired" failure; the interactive tmux TUI works** (proven 2026-07-16, cost 6h;
fixed at root in 1.99.306).

`autorunRunsClaudeBinary` (`autorun.go:319`) exists precisely to say "glm is the
claude binary, so binary-level rules apply to both". Something on the **master**
seat path is not consulting it — `--tmux` is documented as "forced on for claude",
and glm must be forced on identically.

- Find every place that decides TUI-vs-headless and route glm through
  `autorunRunsClaudeBinary`, not through a string compare against `"claude"`.
- Add a test: a master seat of `glm` must never be spawned with `-p` or
  `--output-format`.

## P2 — opencode is "found but not working" while printing its version

Real failure, mini:

```
runner opencode is not ready: opencode found but not working:
  signal: killed (output: 1.14.41)
```

Read that carefully: the probe **captured `1.14.41`** — a perfectly good version
string — and still declared the runner broken, because the process was `killed`
(a probe timeout). opencode printed its version and did not exit; the probe waited,
timed out, killed it, then judged it by the signal instead of by the answer it had
already received.

- If the probe got a valid version, the runner **is** installed and working.
  Judge on the answer, not on how the process ended.
- Kill the process by all means — a probe must not hang — but a timeout after a
  successful answer is a probe bug, not a runner fault.
- The message is also a lie worth deleting: "found but not working" with the
  working output right there in the parentheses.
- opencode is the mini's **default runner**, so this one bug idles the box's
  default path.

## P3 — autorun cannot survive a diverging branch

Real failure, mini:

```
git pull --ff-only: exit status 128:
  hint: Diverging branches can't be fast-forwarded, you need to either: …
```

The loop does `git pull --ff-only` and simply dies when `main` has moved. On a box
running several loops plus a human pushing, divergence is the **normal** case, not
the exception.

- A loop must **resolve** divergence, not fail on it. Rebase or merge, and when
  there is a conflict, hand it to the runner — resolving a merge conflict is
  exactly what a coding agent is for. Failing the run and stranding the work in a
  stash is the behaviour that built this box's stash graveyard.
- Never leave work stranded. See `tasks/deploy-orchestration.md` P0: landing is
  part of converging.
- Related real failure from the same box, worth fixing together:
  `gate failed; … (recording the final autorun commit also failed: push final
  commit: exit status 1)` — a failed push must not silently swallow the record of
  what happened.

## P4 — `resolveRunnerBinary` uses raw `LookPath` first

`runner_resolve.go:57` calls `osexec.LookPath(name)` before trying
`runnerCandidatePaths`. This is the disease `9d1eed683` named: the agent runs from
launchd/systemd with a minimal `$PATH`, so `LookPath` fails for a binary one
directory away — that is exactly how tmux was "not installed" while installed, and
it killed this box's autorun for hours.

`binary_discovery.go` exists for this and has **~8 consumers, all reporting/install
paths**, while **~38 raw `exec.LookPath` calls** for node/npm/npx/go/flutter/brew/
docker still bypass it. Yaver *reports* tools with the smart resolver and *runs*
them with dumb PATH lookup.

- Route `resolveRunnerBinary` through `DiscoverBinary`.
- **Absolute path matters as much as finding it** — argv[0] of `"opencode"`
  re-inherits the same broken PATH. Return and exec the absolute path.
- `DiscoverBinary` is memoised 60s; clear `discoveryCache` in tests.
- Do not attempt all ~38 sites here. Do the runner ones and leave the rest.

## P5 — opencode leaks a 4.5 MB `/tmp` `.so` per run

Known and proven: opencode drops a ~4.5 MB shared object into `/tmp` on every run
and never removes it. It filled a Hetzner box to 100% disk. It looks like malware
and is not — do not re-litigate that, just handle it.

- Autorun's `disk_reclaim` heal should know this specific leak and sweep it.
- Reclaim on the runner's own lifecycle, not only when the disk is already full —
  a heal that fires at 100% has already broken the build it was meant to save.

## Out of scope

Do not touch `desktop/agent/autorun_ops.go`'s slot key or sort (correct — read,
don't change). Do not change the deploy layer (`tasks/deploy-orchestration.md`
owns it). Do not add dependencies. Do not add an API-key path for Claude.

## Definition of done

Say the completion marker, alone on its own line, only when P0–P5 are complete and
verified in the git log and the gate passes — in particular when `yaver runner
<box> status` can no longer say a runner is "not working" while quoting its
working output.
