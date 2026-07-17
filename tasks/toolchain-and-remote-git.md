# One tool-resolution truth, and git verbs that reach another machine

## Why this exists

Two holes, both found by a real incident on 2026-07-17, both at the Go-agent +
MCP level.

**1. Two lists of "where tools live", and they disagree.** `augmentAgentPATH`
(`desktop/agent/main.go:52`, called first thing in `main()` at `:434`) repairs the
daemon's minimal launchd/systemd `$PATH`. `commonInstallPrefixes`
(`desktop/agent/binary_discovery.go:49`) does the same job for `DiscoverBinary`.
They were written separately and cover different directories, so a tool is
findable by one and invisible to the other:

| Prefix | augmentAgentPATH | commonInstallPrefixes |
|---|---|---|
| `~/.local/bin`, `~/.npm-global/bin`, `/opt/homebrew/bin`, `/usr/local/bin` | yes | yes |
| `~/.cargo/bin`, `~/.bun/bin`, `~/.deno/bin`, `~/go/bin` | **no** | yes |
| `~/.yaver/bin`, `~/.yaver/runtimes/bin` | **no** | yes |
| `/opt/homebrew/sbin`, `/usr/local/sbin` | **no** | yes |
| `/snap/bin`, flatpak exports (linux) | **no** | yes |
| `~/Library/Python/*/bin` | yes | **no** |

`~/go/bin` is where `go install` puts every Go tool. `~/.cargo/bin` is every
`cargo install`. Neither is on the daemon's `$PATH`, so any code that shells out
by bare name misses them — while `/infra/summary` cheerfully reports them as
installed, because that path uses the other list.

**2. Nothing git-shaped can run on another machine.** The `ops` framework already
solved multi-machine: `OpsContext` carries `Machine` (`desktop/agent/ops.go:37-40`,
`local` | `auto` | deviceId), so **every ops verb is machine-routable for free**
and is exposed through the `ops` MCP grand-tool. The git tools never used it —
they are plain MCP tools taking `directory` and nothing else:

- `git_stash` (`mcp_tools.go:2180`), `git_log_advanced` (`:2182`), `git_branches`
  (`:2183`), `git_reflog` (`:2186`), `git_blame_file` (`:2181`) — local only.
- `diff` (`mcp_tools.go:1579`) is **not git**. It is "Compare two files and show
  differences". There is no `git diff` verb at all.
- `git_rebase`, `git_commit`, `git_merge`, `git_add`, `git_cherry_pick`,
  `git_pull`, `git_checkout` — **zero hits in the whole tree.** MCP cannot commit
  or rebase, locally or remotely.

So an agent driving a second box (the normal case here — laptop + Mac mini) can
read that box's logs but cannot see its diff, stash its work, or land a commit.

## Ground rules

- **Ops verbs, not new MCP tools.** Register in the ops registry
  (`registerOpsVerb`, see `desktop/agent/autorun_ops.go:217` for the shape). That
  buys `--machine` routing, the `ops` MCP grand-tool, and `yaver ops <verb>` in
  one move. Do NOT add a `machine` property to individual MCP tool schemas.
- **One source of truth for prefixes.** Do not extend both lists. Make
  `augmentAgentPATH` consume `commonInstallPrefixes()`; that function is the
  canonical list and already has the manager-guessing logic hanging off it.
- **Append, never reorder.** `augmentAgentPATH` prepends its candidates today and
  dedupes against the existing `$PATH` (`main.go:78-109`). Whatever you change,
  a directory ALREADY on the user's `$PATH` must keep its existing precedence —
  we are adding fallbacks, not overriding the user's choice of which node wins.
- **Never `git add -A` / `git commit -a` in a verb.** Both boxes here run
  parallel sessions in one checkout; a bare commit swept nine files into the
  wrong commit today. Verbs stage explicit paths only.
- Keep each iteration to one increment the gate can verify.
- **Stay inside these files — anything else is a scope violation that kills the
  run.** Prefix work: `desktop/agent/binary_discovery.go` + its tests in
  `desktop/agent/binary_discovery_test.go`, and `desktop/agent/main.go`. Git
  verbs: `desktop/agent/ops_git.go` + its tests in
  `desktop/agent/ops_git_test.go`. Do not create test files under other names.

## Work

### 1. Make `augmentAgentPATH` consume `commonInstallPrefixes()`

`main.go:66-76` hardcodes its own candidate list. Replace it with
`commonInstallPrefixes()` plus the one thing that list lacks —
`~/Library/Python/*/bin` (`main.go:74`), which should MOVE into
`commonInstallPrefixes` so both callers get it.

Keep the existing behaviour at `main.go:87` (only add directories that exist) and
the dedupe at `:96-108`. The result: `go install`/`cargo install`/snap tools stop
being invisible to the daemon, and `DiscoverBinary` and `$PATH` can no longer
disagree about what is installed.

Verify: a binary reachable only via `~/go/bin` is found by `exec.LookPath` inside
the agent after startup. A table test over `commonInstallPrefixes()` asserting
both callers see one list is enough — do not shell out.

### 2. Cover the platform CLIs actually in use

`aws`, `gcloud`, `supabase`, `firebase`, `convex`. Most arrive via npm/pipx/brew
and are covered once §1 lands, with one real exception: the Google Cloud SDK
installs to `~/google-cloud-sdk/bin` and appears in **neither** list. Add it to
`commonInstallPrefixes`. Add `gcloud`, `aws`, `supabase`, `firebase` to
`knownProbeBinaries` (`binary_discovery.go:197`) so `/infra/summary` reports them.

Note `convex` is normally project-local (`node_modules/.bin`), not global — do not
add a home prefix for it; that is what `npx` is for.

### 3. `git_ops` read verbs

New file `desktop/agent/ops_git.go`. Register:

- `git_status` — porcelain v1, parsed into `{path, index, worktree, untracked}`.
- `git_diff` — the real one. `{ref?, staged?, paths?[], stat?}`. Default: unstaged
  worktree diff. `stat: true` returns `--stat` only, because a full diff of a big
  change is enormous and the caller usually wants the shape first.
- `git_log` — thin, `{limit?, paths?[]}`. Do not duplicate `git_log_advanced`;
  if it is redundant, say so in the handoff rather than deleting it here.

All take an optional `dir` (default: the agent's project dir) and inherit
`machine` from the ops layer. Resolve git via `DiscoverBinary("git")` and exec by
absolute path — same reason as `tmuxCmdName` (`tmux.go`).

### 4. `git_ops` write verbs, with a safety contract

- `git_stash_ops` — `{action: list|push|pop|apply|drop, message?, paths?[]}`.
  `push` MUST accept explicit paths and MUST NOT be able to stash the whole tree
  without them: a bare stash on a shared checkout swallows a sibling's live work,
  which is exactly what went wrong today.
- `git_commit` — `{message, paths[]}`. `paths` is **required and non-empty**.
  Stage with `git add -- <paths>`, commit with `-S` (this repo signs; see
  `autorun_cmd.go:230`). Return the new SHA. Reject an empty `paths` with a real
  error naming why, not a silent all-stage.
- `git_rebase` — `{onto, upstream?, abort?, continue?}`. Interactive rebase is
  impossible over this transport, so `-i` must be rejected with a clear error.
  Always report the conflicted paths on failure; a rebase that stops mid-way and
  says only "exit 1" is useless to a remote caller.
- `git_merge` — `{ref, abort?}`, `--no-edit`, never `--squash` by default.

Every write verb returns the resulting HEAD sha and the worktree state after it,
so the caller never has to guess whether it landed.

### 5. Push is deliberately out of §4

Do not add `git_push`. `git_push_creds` already exists (`mcp_tools.go:2069`) and
pushing is the one action whose blast radius leaves the machine. Land §1-§4
first; pushing gets its own decision.

## Out of scope

- Do not touch `desktop/agent/autorun*.go`, `tmux.go`, `runner_*.go` — another
  loop owns those.
- Do not touch `mobile/`, `web/`, `backend/convex/`.
- Do not add dependencies.
- Do not rewrite `binary_discovery.go`'s caching (`discoveryCache`, 60s). It is
  correct. If a verb installs something, call `clearDiscoveryCacheFor(name)`.

## Definition of done

Say DONE, alone, only when:

- `augmentAgentPATH` and `DiscoverBinary` demonstrably share one prefix list, and
  a test pins that `~/go/bin` and `~/.cargo/bin` are in it.
- `gcloud`/`aws`/`supabase`/`firebase` are probeable.
- `git_status`, `git_diff`, `git_log`, `git_stash_ops`, `git_commit`,
  `git_rebase`, `git_merge` are registered ops verbs, each with a test.
- `git_commit` with empty `paths` is proven to fail rather than stage everything.
- The gate passes and it is all in the git log.
</content>
</invoke>
