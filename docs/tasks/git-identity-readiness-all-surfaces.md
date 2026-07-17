# Task: git identity readiness — make the invisible failures visible, everywhere

Yaver's job is to lower dev opex. Today it happily reports "pushed" while the
push was silently waived, and reports a runner "not configured" when the runner
is authed and the box is broken. Every hour lost below was lost to a check that
could have existed.

Read `docs/architecture/AI_ARCH.md` before touching transport. **grep the code;
when a doc disagrees with code, the doc is the bug — fix it in the same change.**

## Where this came from (2026-07-17 — all verified live, do not re-litigate)

Five real failures, each of which cost real time because nothing surfaced them:

1. **SSH signing key registered as AUTH only, never as SIGNING.** They are two
   separate lists at github.com/settings/keys. `git log --format=%G?` showed
   **G (good)** locally — local G only means it verifies against the local
   allowed_signers file. GitHub said `verified=false reason=unknown_key` for
   EVERY commit from this machine, including other sessions'. Invisible until a
   ruleset enforced it.
2. **`remote: Bypassed rule violations for refs/heads/main` was printed on
   successful pushes and nobody read it.** As repo owner the signed-commits rule
   was waived. Move the repo into an org (no owner bypass) and every push fails
   `GH013`. **A "bypassed" line is a warning, not a success.**
3. **`yaver runner <box> status` reported `claude: ✗ not configured` while
   claude was authed** (`oauthAccount` present in `~/.claude.json`). The probe
   runs claude, claude died for an unrelated reason, probe concluded "logged
   out". A readiness check that cannot distinguish *broken* from *logged out*
   sends you hunting the wrong thing for hours.
4. **The tmux SERVER held a deleted worktree as its cwd**, so every runner it
   spawned died on startup — claude `ENOENT: Bun could not find a file`, codex
   `No such file or directory (os error 2)`, opencode `The current working
   directory was deleted`. Only opencode said it. See
   `project_tmux_server_deleted_cwd_kills_all_runners` memory.
5. **Autorun worktree paths contained a colon** (`<task>:<seat>`) — fixed in
   `e95ca8ee1`. And **claude's folder-trust dialog** blocks in any unseen dir and
   `--dangerously-skip-permissions` does NOT skip it — fixed in `577aa6046`.

## Scope

The forge seam landed in `6b2e97424` (`forge.go`, `forge_github.go`,
`forge_gitlab.go`, `forge_surface.go`) — **use it, do not fork it**. GitLab has
the same split (signing keys are separate from auth keys there too).

### P1 — Agent: a real git-identity readiness check
New check, surfaced through the existing `yaver_doctor` seam
(`mcp_tools.go:907`, `httpserver.go:7114`). It must answer, for the CURRENT repo
and remote:
- Is a signing key configured locally (`commit.gpgsign`, `gpg.format`,
  `user.signingkey`)? Does the referenced file exist?
- Is that key registered **as a signing key** on the forge? GitHub:
  `GET /user/ssh_signing_keys` (needs `admin:ssh_signing_key`; if the token
  lacks the scope, SAY SO rather than reporting "unknown"). GitLab: the
  equivalent under the forge seam.
- Does the remote's ref have rules requiring signatures/linear history/PRs?
  GitHub: `GET /repos/{o}/{r}/rules/branches/{branch}` — this needs NO admin
  scope and returns the rules that apply to *you*. That is the honest oracle.
- **Ground truth, not inference**: for the last N commits, ask the forge
  `GET /repos/{o}/{r}/commits/{sha}` → `.commit.verification.{verified,reason}`.
  `reason=unknown_key` is the exact fingerprint of failure #1.
- Fingerprint match: compare local key fingerprint to the registered signing
  keys, so "you registered a DIFFERENT key" is distinguishable from "you
  registered none".

Output must name the fix, not the symptom: *"your key is registered for
authentication but not signing — add the same key at
github.com/settings/ssh/new with Key type = Signing Key"*.

### P2 — Agent: stop reporting silent waivers as success
`git push` prints `remote: Bypassed rule violations` on a push that only
succeeded because of a privilege that may not survive an org move, a
permission change, or a different machine. Wherever Yaver wraps push
(`ops_git_verbs.go`, `git_push_creds.go`, forge seam, autorun's `--push`),
parse that line and surface it as a WARNING with what was waived. Same for
`GH013` — map it to the specific rule and the specific fix.

Law already on the books: **visible failure > silent retry**
(`feedback_visible_failure_over_silent_retry`). This is its git instance.

### P3 — Runner readiness must distinguish broken from logged-out
`CheckRunnerReady` / `yaver runner <box> status` currently collapses every
failure into "not configured". Split the states:
- `authed + working`
- `authed but the binary/env is broken` ← failures #3 and #4 both live here
- `not authed`
- `not installed`
Include the runner's actual stderr in the broken case. "claude: ✗ not
configured" while `~/.claude.json` holds a live `oauthAccount` is a lie the
tool told for hours.

Cheap high-value addition: if a runner dies instantly, check whether the tmux
SERVER's cwd still exists (`lsof -p <tmux pid>` → cwd) before blaming the
runner. A dead server cwd kills every runner identically and is invisible from
the runner's own error text.

### P4 — Surface it everywhere (the parity law)
Per CLAUDE.md's cross-surface rule, two families with DIFFERENT propagation:
- **RN-shared** (mobile, tablet, car, glass/AR-VR) consume the same
  `DeviceContext`/`AuthContext` — one wiring reaches all four. Verify, don't
  assume it isn't gated to one screen.
- **Native ports, each explicit**: web (`web/lib/`, `web/components/dashboard/`),
  tvOS (`tvos/YaverTV/`), watchOS (`watch/YaverWatch/`), Wear OS (`wear/`).
Not every surface needs the full report. A watch does not render a rules table.
The rule: **any surface that can trigger a push or a deploy must be able to
show why it would fail.** A surface that can only observe can link out.

Car is voice-only by Apple policy — a spoken "your commits aren't verified,
pushes to main will fail" is the right shape there, not a table.

### P5 — MCP awareness
The check must be reachable as an MCP verb so an agent can self-diagnose
instead of guessing (this session burned four wrong theories before finding
the real cause). Fold into `yaver_doctor`'s output and/or the forge seam's
verbs. It should be the FIRST thing a coding agent runs when a push fails.

## Hard constraints
- Never commit secrets/infra IPs/hostnames — repo is public.
- **Never `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits the real
  `~/.yaver` and signs the box out. Always `-run` scoped.
- Only `git commit -- <explicit paths>`. This checkout is shared; another
  session already destroyed a commit here with `amend`+`reset` over someone
  else's HEAD, and a `git add -A` has swept others' work before.
- Do NOT fork the forge seam (`forge*.go`) — extend it.
- Do NOT weaken a ruleset to make a push pass. The signed-commits rule is a real
  protection; registering the key is the fix, disabling the check is not.
- Read-only diagnosis by default. Never auto-register a key, never auto-bypass.
  Print the exact command/URL and let the human do it.

## Gate
`./scripts/gate-webrtc-vibe.sh` covers build/tsc; add scoped tests for the new
check. Suggested: a fake forge responding `unknown_key` must produce the
"registered for auth, not signing" message, not a generic failure.

## Findings
(Append what you PROVED and how. "should work" is not a finding.)
