# Task: forge parity — GitHub/GitLab as a first-class resource on every surface

Goal: Yaver understands a **project's git resource** — Yaver-managed git, GitHub
(incl. Enterprise), and GitLab (incl. self-hosted) — and can drive the full set
of forge operations from **MCP and every surface**, not just from raw `gh`/`glab`
syntax the model has to know by heart. Inviting a collaborator is the canonical
example and must work end-to-end.

**Docs drift. grep the code; when a doc disagrees with the code, the doc is the
bug — fix it in the same change.** (`desktop/agent/CLAUDE.md` rule.)

## What already landed (commit 6b2e97424 — do NOT redo)

`desktop/agent/forge.go`, `forge_github.go`, `forge_gitlab.go`, `forge_test.go`.
Builds clean; 20 tests pass. This is the seam everything else hangs off:

- `Forge` interface + `ForgeKind` (github|gitlab), `ForgeHost`, `ForgeRepo`,
  `ForgeRole`, `ForgeMember`, `ForgeInvite`.
- **Hybrid transport** (`forgeTransport`): `gh api`/`glab api` when the CLI is on
  PATH and authed (both accept `--hostname/--method/--input -`, so one argv shape
  covers both), else direct REST with a token from the existing detect chain.
  Both send the same path + JSON body; a `Forge` impl never learns which it got.
- **GHE fixed**: `resolveForgeHost` puts the host in the URL (`/api/v3`), where
  before every GitHub call was hardcoded to `api.github.com`.
- `ListMembers` / `InviteMember` / `RemoveMember` implemented for both forges.

## Decided — do NOT re-litigate

1. **Hybrid transport, CLI-first.** Not CLI-only (headless boxes have no `gh` —
   the Mac mini itself does not), not REST-only (re-implements SSO/GHE/proxy).
2. **`gh_run`/`glab_run` keep the escape hatch, with a denylist.** Not an
   allowlist — that breaks the documented "any subcommand without a per-verb
   wrapper" intent at `mcp_platforms.go:415-428`.
3. **Neutral roles** read|triage|write|maintain|admin. `read` maps to GitLab
   **Reporter(20)**, not Guest(10) — Guest cannot read code on most tiers.
4. **"Forge", never "provider"** in new code. `provider` already means OAuth
   login identity, IaaS vendor, TTS engine, and LLM vendor in this repo.
5. **Honest states.** GitHub 204 = "added" (they already had access, no email
   goes out), not "invited". GitLab 201 + `{"status":"error"}` is a **failure**.
   GitLab 409 = already_member = success.

## Work remaining, in order

### 1. Wire the forge seam to MCP + ops (highest value — start here)

Neutral verbs, self-registering via `init()` + `registerOpsVerb` (copy the
pattern in `ops_git_surface.go`, do NOT add to a central switch):

- `git_members` — list who has access. Payload `{repo?, directory?, host?, kind?}`.
- `git_member_invite` — `{user, role?, repo?, directory?, host?, kind?}`.
- `git_member_remove` — `{user, repo?, directory?, ...}`.

Resolution precedence is already implemented in `resolveForgeRepo`: explicit
repo slug > directory > cwd. Use it; don't re-parse remotes.

Every result must report `via` (`"gh api"` | `"rest"`). When a call unexpectedly
403s, "which credential did this use" is the first question worth answering.

Then declare them in `mcp_tools.go` + dispatch in `httpserver.go` (the MCP tool
switch and the ops registry are **different surfaces** — a verb in one is not in
the other; that mismatch is bug #3 below).

### 2. `gh_run` / `glab_run` denylist

`mcpGhRun` (`mcp_platforms.go:430`) / `mcpGlabRun` (`:454`) validate exactly
three things: on PATH, authed, args non-empty. Then `osexec` with anything.

- Block credential-printing subcommands: `gh auth token`, `gh secret list`,
  `glab auth status --show-token`, and friends. **`gh auth token` through
  `gh_run` prints the user's PAT straight into tool output today.**
- Require an explicit confirm flag for destructive ones (`repo delete`,
  `gh api -X DELETE`).
- No shell is involved (`osexec.Command`, not `sh -c`), so this is about
  capability, not metacharacter injection. Don't add shell quoting theater.

### 3. Fix the advertised-but-absent verbs (honesty bugs, all confirmed)

- **`github_trending`** is declared (`mcp_tools.go:1733`) and dispatched
  (`httpserver.go:9217`) but returns `{"error":"feature_removed"}`
  (`mcp_dropped_stubs.go:48`). Either implement it or stop advertising it.
- **`git_prs` / `git_issues` / `git_ci_status`** exist only as ops verbs
  (`ops_git_surface.go:20-62`) but `mcp_onboarding_flows.go:197,204` advertises
  them as unlocked **MCP tools**. Pick one surface and make the claim true.
- **Three parallel `git status` implementations** exist (ops verbs at
  `ops_git_verbs.go:285`, the mux at `httpserver.go:1080-1093`, and
  `mcp_docker_ext.go`). Converge them.

### 4. Migrate the copy-pasted pairs onto the seam

~15 pairs in `git_provider.go`: `verifyGitHubToken`/`verifyGitLabToken` (`:373`/
`:398`), `listGitHubRepos`/`listGitLabRepos` (`:484`/`:535`), `addSSHKeyToGitHub`/
`addSSHKeyToGitLab` (`:644`/`:669`), `createRepoOnGitHub`/`createRepoOnGitLab`
(`:1617`/`:1650`). Move each behind `Forge`, one at a time, keeping tests green.
`mcpGhRun`/`mcpGlabRun` are byte-identical but for the binary name — collapse.

**Do not break the existing `/git/provider/*` HTTP contract** — web
(`agent-client.ts:7014-7100`) and mobile (`more.tsx:1370-1606`) both call it.

### 5. Extend the Forge interface

Missing entirely today (greps for `collaborator`, `/members`, `pr_merge`,
`/hooks`, `deploy_key`, `branch_protection` return zero forge hits): PR/MR
**merge**, reviews, forks, releases **create** (only `list` exists), webhooks,
deploy keys, protected branches. Add to `Forge`, implement per forge, expose as
neutral verbs.

### 6. Surface parity (see CLAUDE.md "Cross-surface parity")

RN surfaces (mobile/tablet/car/glass) share `DeviceContext` — one fix reaches
them. Native surfaces do **not** inherit and must be ported explicitly:

| Surface | Today | Needed |
|---|---|---|
| web | `GitView.tsx` (1364 lines): clone, branches, credentials | members/invite UI |
| mobile | deepest surface; on-device isomorphic-git | members/invite UI |
| tvOS | sign-in only, via `ops: git_connect` — a **different transport** than web/mobile | converge transport; read-only members |
| watch/Wear | **zero git** | read-only at most |
| desktop | 2 IPC bridges (`gitPull`/`gitStatus`) | wire the new verbs |

`cloneToPhone.ts:42` hard-rejects non-GitHub URLs while sitting on top of a
4-provider abstraction (`gitProviderAuth.ts:17`) — fix or document.

### 7. Convex: model repo identity

`userProjects` (`backend/convex/schema.ts:1977`) stores only `gitBranch` +
`lastCommit`. A cloned repo **loses the link back to its origin forge**. Add
host + kind + repo path.

**Privacy contract**: no absolute paths, no tokens in Convex — see
`convex_privacy_test.go` and add new fields to
`fieldsWeForbidInAnyConvexPayload` where relevant. A repo path like
`owner/repo` is fine; `/Users/<name>/...` is not.

## Gate

```
cd desktop/agent && go build ./... && go test -count=1 -run 'TestForge|TestGit|TestOps' .
```

**NEVER run a bare `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits
the real `~/.yaver` and **signs the machine out**. Always scope with `-run`.

## Definition of done

`git_member_invite` adds a real collaborator to a real repo on **both** GitHub
and GitLab, through **both** transports (CLI present and CLI absent), and the
result says which transport it used. Everything advertised in the tool list
exists. No new copy-pasted per-forge pair.
