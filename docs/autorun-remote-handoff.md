# Remote autorun — handoff (2026-07-16)

Goal that triggered this: *"make autorun perfect for all compiles/deploys — go agent,
TestFlight, Android AAB, visionOS, tvOS, car, Cloudflare — user-intent aware, at MCP
level"*, dispatched to a remote machine with Claude so the laptop can be closed.

**It could not be dispatched.** Remote autorun does not exist yet. This file records
what is actually true, so the next session starts from evidence instead of re-deriving it.

Every claim below was verified on 2026-07-16 and carries a way to re-check it. Per the
repo rule: markdown drifts, code is the source of truth — re-run the checks before
trusting any line here.

## What shipped (safe, pushed, `main` in sync)

| Commit | What |
|---|---|
| `706c25351` | README redesign — hero + architecture SVGs, landing-mirrored copy |
| `13cc35eb5` | `fix(sdk)`: declare `projectName` + `bundleId` on `FeedbackConfig` — **main did not typecheck without it** |

`13cc35eb5` is worth knowing about: `cce1b102e` shipped the 11 files that *read*
`config?.projectName` / `config?.bundleId` (`YaverFeedback.ts:1043`,
`FeedbackModal.tsx:667`) but never `git add`-ed the `types.ts` that *declares* them.
Six `TS2339` errors on `main`. Re-check the class of bug with:

```bash
cd sdk/feedback/react-native && npx tsc --noEmit   # must exit 0
```

**CI did not catch this.** `test-suite.yml`'s RN SDK job (`:270-273`) installs and tests
the SDK but never typechecks it. That gap is still open — see below.

## The blocker chain

Four independent walls. Any one of them stops the handoff; all four are real.

### 1. Remote autorun is explicitly unimplemented

`desktop/agent/autorun_cmd.go:32,38-39`:

```go
machine := fs.String("machine", "", "remote machine (not available in this increment)")
if strings.TrimSpace(*machine) != "" {
    fmt.Fprintln(os.Stderr, "autorun: --machine is not available yet; refusing to run locally when remote persistence was requested")
```

The CLI refuses by design. The ops verb `autorun_start`
(`desktop/agent/autorun_ops.go:187`) *does* take `machine:`, and `autorun_stop_all`'s
description documents `machine:<deviceId|alias|primary>` — so the ops path is the
intended route, not the CLI.

### 2. This laptop's daemon is stale and unresponsive

- `ops_verbs` returns **492 verbs, zero `autorun_*`** — so `ops(verb: "autorun_start")`
  fails locally with `unknown_verb` before it can route anywhere.
- `curl -s localhost:18080/info` returns no `version` field.
- `yaver autorun --help` and `yaver ping linux-2` each hung past 120s and had to be killed.
- Binary on PATH is `1.99.306`, which *does* contain the autorun verbs (`c2b323bc0`).

So: binary ≠ running daemon. Restart is the fix, but see the warning in
[Next session](#next-session-suggested-order).

### 3. No working exec path to the one usable box

`magara` (`08182df8`, alias `linux-2`) is the **only reachable machine with Claude
authenticated** — `claude.ai · max`, `ready: true`, confirmed via
`runner_auth_status{device_id:"08182df8"}`. The `timeout` in the `RUNNERS` column of
`yaver devices` is slow enumeration, not a missing runner. Don't be fooled by it.

But there is no way to drive it:

| Path | Result |
|---|---|
| `exec_command{device_id:"08182df8"}` | returns `Status: <nil>`, no output — even for `echo HELLO` |
| `remote_exec{machine_id:"08182df8"}` | `machine "08182df8" not found` (only knows managed cloud) |
| `ops{machine:"08182df8", verb:"autorun_*"}` | `unknown_verb` (wall #2) |
| `ssh magara` | hostname does not resolve |

### 4. Even unblocked, the Linux box can't do most of the target list

`magara` is Linux. Of the requested matrix:

| Target | Runs on magara? |
|---|---|
| Go agent build/test | yes |
| Cloudflare | yes |
| Android AAB | yes, with the SDK installed |
| **TestFlight / iOS** | **no — needs macOS + Xcode** |
| **visionOS / tvOS** | **no — needs macOS + Xcode** |
| **CarPlay** | **no — needs macOS + Xcode** |

`CLAUDE.md` already pins TestFlight as local-only by design (GH runner keychains don't
carry the registered iPhone UDIDs). So the Apple half of the matrix needs a reachable Mac.

The only other Mac is `mac-mini` / `pokayoke` (`229aeb03`, primary) and it is **offline**:

```
$ yaver ping mac-mini
mac-mini unreachable
  cause:  every transport candidate failed
$ ssh pokayoke      # 192.168.111.25 — different LAN
ssh: connect to host 192.168.111.25 port 22: Operation timed out
```

Hetzner `yaver-test-ephemeral` and the Tailscale box both reject SSH
(`Permission denied (publickey)`).

**The real blocker is the mac-mini being offline.** Everything else is downstream of it.

## Still open

1. **`test-suite` is failing** — the red badge now sits at the top of the redesigned
   README. Last 4 completed runs: `failure`. All SDK jobs (feedback, web, flutter, rn,
   docker, integration) pass; the `test-suite` job itself is what fails. Not diagnosed —
   the log tail showed only artifact upload + `dorny/test-reporter`, so the real failure
   is earlier in the log. Either fix it or consciously accept the badge; don't remove it
   to make the page look green.
2. **Demo GIF leaks identity.** `demo-videos/yaver-hosting-demo.gif` shows absolute paths
   and private project names on the phone screen — `/Users/kivanccakmak/Workspace/botox/mobile`,
   `pokayoke_app`, `TusRehber`, `KlinikAI`, `SFMG`, `mobileref`. The repo is public and the
   GIF is now on a more prominent README. Pre-existing (it was in the old README too), not
   introduced by the redesign. Re-editable sources: `demo-videos/sources/*-lite.mp4`.
3. **CI never typechecks the RN SDK** — the gap that let `main` ship uncompilable.
   Cheapest fix: add `npx tsc --noEmit` to the RN SDK job in `test-suite.yml:270`.

## Next session — suggested order

Do these in order; each unblocks the next.

1. **Bring the mac-mini back.** Without a reachable Mac, the Apple half of the matrix is
   undeliverable no matter how good autorun gets. Start at `docs/mac-mini-remote-worker.md`
   and `scripts/setup-mac-mini-dev.sh` (both landed in `7b446ee9b`).
2. **Restart this laptop's daemon onto 1.99.306** so it learns the autorun verbs.
   ⚠️ Do this while sitting at the machine, not while walking away: a daemon restart drops
   into bootstrap/needs-auth. Recovery is `yaver auth send <passkey> <url>` — no browser
   needed, but it does need you.
3. **Verify the ops route works** before building on it:
   `ops{machine:"08182df8", verb:"autorun_status", payload:{}}` should stop returning
   `unknown_verb`. Note this also requires *magara's* agent to be ≥ 1.99.305 — unverified,
   because every CLI probe hung (wall #2).
4. **Then** design the target matrix + intent-awareness. Notice that steps 1-3 are all
   plumbing: the feature the request actually names can't start until the transport is
   honest.

## Related memory

- Remote autorun is *not* the same as `runner --machine` (shipped 1.99.274) — that wraps a
  TUI, it does not persist a loop.
- Runners must use subscription login, never API keys; autorun must use the interactive
  tmux TUI, never `-p` headless.
- Claude's TUI trust-folder prompt blocks tmux autorun in any directory it hasn't seen
  before, and `--dangerously-skip-permissions` does **not** skip it. Any new box needs the
  repo dir trusted once, by hand, or autorun hangs there forever.
