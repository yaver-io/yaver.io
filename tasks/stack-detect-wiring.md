---
doer: codex
---

# Wire the canonical stack detector into deploy

## Context

`desktop/agent/stack_detect.go` landed in commit 41a2ce8db. It is the canonical
project-stack detector: `stackDetect(root) *StackDetection` returns
`{Framework, Backend, Hosting[], ORM, Services[], Tags[], Targets[], Evidence[], Packages[]}`
and is proven by 12 tests in `stack_detect_test.go` (all passing).

Providers live in ONE table — `stackProviders` in `stack_detect.go`. Supabase,
Convex, Firebase, Cloudflare, Vercel, Netlify, Fly, Railway, Docker, Prisma,
Drizzle. Each entry declares its marker `Files`, `Dirs`, `PkgDeps`, and its
`Actions[]` (each action has an `OpsTarget` or an `MCPTool`).

**Nothing reads the detector yet.** That is the whole job below.

Read `stack_detect.go` first, in full, before writing anything. Its doc comment
explains the two invariants you must not break:

1. **A dependency is not a deployment.** A repo with `@supabase/supabase-js` but
   no `supabase/config.toml` is a CLIENT of someone else's Supabase. It gets a
   chip; it must NEVER get a deploy button. `DetectedTarget.Weak == true` marks
   these, and `DeployableTargets()` already filters them out. Pushing a schema to
   a project the user does not own is not a recoverable mistake.
2. **Monorepo roots get chips, not buttons.** Tags roll up to the root; Targets
   do not, because an action needs the one directory it runs in.

## Tasks

### 0. A project has MANY stacks — fix the singular Framework (do this FIRST)

`StackDetection.Framework` is a single `string` and `detectCanonicalFramework`
returns on first match. That is wrong, and it is a regression against the legacy
`detectStack` (`repos_http.go:43`) which returned `Frameworks []string`.

Real cases it breaks:
  - **Solito**: Next.js AND Expo in one `package.json` — both are real, both have
    actions, and returning only `expo` silently loses the web half.
  - Any single directory that is genuinely two stacks at once.
  - A monorepo package that is both a web app and a mobile app.

**Do:**
- Add `Frameworks []string` — ALL detected frameworks, canonical spelling,
  stable-sorted via `dedupeSorted`.
- Keep `Framework string` as the PRIMARY (dominant) one, for display and for the
  legacy callers that expect one value. Do not remove it — `httpserver.go`,
  `devserver_kind.go` and the UIs read it. Document how primary is chosen; the
  existing precedence in `detectCanonicalFramework` (Expo before React Native,
  JS/Flutter before native) is the right order and is load-bearing — keep it and
  say so in a comment.
- `detectCanonicalFramework` must collect instead of early-returning. Keep the
  ordering contract: Expo still wins over React Native as PRIMARY (every Expo app
  also depends on react-native, so `Frameworks` may legitimately contain both, but
  `Framework` must be `expo`). A React Native repo has `ios/*.xcodeproj` and must
  still never report `swift` — see `classify.go:319`.
- `Tags` must include EVERY framework, not just the primary.

Tests to add: Solito (next + expo in one package.json) reports both in
`Frameworks` with `Framework == "expo"`; the existing single-framework tests keep
passing unchanged.

### 0b. Roles — "backend this, frontend that, mobile that"

A stack list is not legible on its own. Give each detection a ROLE so a UI, an
agent, and the user can say what each part IS.

Add `Role string` to `StackDetection`, canonical values:
`backend | frontend | mobile | cli | library | infra | unknown`
(mirror the vocabulary already in `RepoStack.Type` at `repos_http.go:43` —
`mobile|web|backend|monorepo|library|cli|unknown` — but use `frontend` for web and
keep ONE spelling).

Derive it from what is already detected, not a new scan:
  - `Backend != ""` and no web/mobile framework → `backend`
  - expo / react-native / flutter / swift / kotlin → `mobile`
  - nextjs / vite / react → `frontend`
  - go / rust / python with no web or backend marker → `cli` (or `backend` if it
    has a Dockerfile / fly.toml / railway config — a Go service IS a backend)
  - a package with no framework and no targets is already dropped as noise
  - the monorepo ROOT is `unknown` — it is not itself a project; its packages
    carry the roles.

Then add `Roles map[string]string` to the monorepo root ONLY: role → primary
stack, rolled up from `Packages[]` (e.g. `{"backend":"convex","frontend":"nextjs",
"mobile":"expo"}`). When two packages share a role (two frontends), prefer the
one whose RelPath is shortest, and note the collision in `Warnings[]` rather than
silently picking. This map is what gets seeded to Convex and shown to the user, so
it must be deterministic — stable-sort everything.

Tests: a monorepo with backend/web/mobile packages produces exactly that Roles
map; a two-frontend monorepo warns instead of silently dropping one.

### 0c. Fingerprint + cache — don't rediscover every poll, DO notice real changes

`/projects` recomputes detection per project per request, uncached, and mobile
polls every 2.5s while scanning. But a project's stack CHANGES over its life —
someone adds `supabase/config.toml`, drops Firebase, adds a mobile package — and
a stale answer is worse than a slow one.

`stack_detect.go`'s doc comment currently says there is deliberately no cache.
That was the right default for the first cut; replace it (and that comment) with
a fingerprinted cache. **A TTL-only cache is wrong on both ends** — it rescans
when nothing changed, and serves stale data right after a change. Do not use one.

**Do:**
- Add `Fingerprint string` to `StackDetection`: a deterministic hash over the
  inputs the detection actually depended on —
  - every `Evidence[].Signal` path that exists: its mtime + size
  - the mtime of each scanned directory (root + each package dir). A NEW marker
    file (`supabase/config.toml` appearing) does not change any existing file's
    mtime, but it DOES bump its directory's mtime. Without dir mtimes the cache
    would never notice a stack being ADDED — that is the main case.
  - the workspace globs (`package.json` "workspaces", `pnpm-workspace.yaml`) mtime
  Hash with `crypto/sha256`, hex, and stable-sort the inputs before hashing —
  the same tree must always produce the same fingerprint. Do NOT hash file
  CONTENTS (a 5MB lockfile per poll is worse than the scan you are avoiding).
- Add a cache: `map[string]*StackDetection` guarded by a `sync.RWMutex`, keyed by
  absolute dir. On lookup, recompute ONLY the fingerprint (stat-only, no parse,
  no recursive walk) and compare. Hit → return cached. Miss → full detect, store,
  return. Expose `stackDetectCached(root)` for the hot HTTP paths and keep
  `stackDetect(root)` pure + uncached for tests and for callers that must not lie.
- Add `DetectedAt time.Time` so a surface can show staleness honestly.
- Cap the cache (e.g. 256 entries, evict oldest) — a box that has scanned a
  thousand dirs must not hold them all forever.
- Add a `stackDetectInvalidate(root string)` used by `POST /projects/refresh`
  (`httpserver.go:3136`) so an explicit refresh always means what it says.

**Changed-detection signal.** The fingerprint IS the change signal others need:
when it differs from the cached one, the stack changed and downstream (Convex
seeding, in a later pass) must re-sync. Expose that plainly — e.g. have
`stackDetectCached` return `(det *StackDetection, changed bool)`, so a caller can
sync ONLY on a real change. **Convex writes are metered**; a 2.5s poll that writes
every tick is a cost bug, and this repo has been bitten by row-bloat before. Never
write on unchanged.

Tests to add (real files, `writeTree`, then mutate the tree):
  - same tree twice → identical fingerprint, cache hit, no re-detect
  - ADD `supabase/config.toml` to a detected project → fingerprint changes,
    detection now reports supabase, `changed == true` (this is the case a dir-mtime-
    less fingerprint would miss — it must fail if someone removes dir mtimes)
  - REMOVE `firebase.json` → fingerprint changes, firebase target disappears
  - edit `package.json` to add a dep → fingerprint changes
  - touch an unrelated file (`README.md`) → fingerprint UNCHANGED, no re-detect
  - `stackDetectInvalidate` forces a re-detect

### 1. Expose detection as an ops verb — `desktop/agent/ops_stack.go` (new)

Register a `stack_detect` verb following the exact pattern in `ops_deploy.go`:
an `init()` calling `registerOpsVerb(opsVerbSpec{...})`. Verbs self-register, so
no central file needs editing. See `ops.go:152`.

- Payload: `{workDir?: string}`. Default workDir to `"."`.
- Return the detection in `Initial` (it is fast + synchronous — no streamId).
- `AllowGuest: false`.
- **Do not return `StackDetection.Root` to the caller.** It is an absolute path
  and leaks the user's home-dir username. `RelPath` is the safe identifier. This
  matters because the result is bound for Convex and the UI surfaces; see the
  privacy contract in CLAUDE.md and `convex_privacy_test.go`.
- Surface `Warnings[]` in the response.

### 2. Default `ops_deploy`'s target from detection — `ops_deploy.go`

`ops_deploy.go:76` currently hard-fails with `"target is required"`. This is the
highest-leverage change in the batch: make `target` optional and infer it.

- When `target == ""`, run `stackDetect(workDir)` and consult `DeployableTargets()`.
- **Exactly one** deployable target → use it. Say so in the `Initial` payload
  (e.g. `"inferredTarget": "vercel", "inferredFrom": "vercel.json"`) so the caller
  can see it was inferred and what proved it. Never infer silently.
- **Zero** → keep failing, but with a better error than "target is required":
  say what was scanned and that nothing deployable was found.
- **Two or more** (a repo with BOTH `vercel.json` and `wrangler.toml` is real and
  already covered by `TestStackDetectNextWithVercelAndCloudflare`) → DO NOT guess.
  Return a typed error with code `"ambiguous_target"` listing the candidates. An
  ambiguous deploy that picks wrong ships to the wrong host.
- Explicit `target` always wins over inference. Never override the caller.
- Keep the guest hardening at `ops_deploy.go:94-107` intact.

### 3. Add the missing Supabase branch — `ops_deploy.go`

`ops_deploy.go:142-193` has branches for cloudflare/pages/vercel/fly/netlify/
railway/firebase/convex/eas/platform/testflight/playstore — but **no Supabase**,
even though `detectDeployTargets` has known about Supabase for ages. Add:

- `case "supabase-functions"` → `supabase functions deploy <name>` (name from
  `Args`; with no name, `supabase functions deploy` deploys all).
- `case "supabase-db"` → `supabase db push`.

These `OpsTarget` strings already exist in the `stackProviders` table — match them
exactly or the wiring silently won't connect.

Gate the branch on detection: if `stackDetect(workDir)` reports no strong (non-Weak)
supabase target, refuse with a clear error. Today `mcp_platforms.go:17-47` shells
out to `supabase` blindly and hands the user a raw CLI error; do not repeat that.

Rollback: Supabase has no native rollback. Follow the honest precedent already set
for convex/testflight/playstore at `ops_deploy.go:257-261` — return
`code: "no_rollback"` with a real explanation. Do not fake it.

**Reconcile the CLI drift while you are here.** `ops_deploy.go` uses `npx wrangler`
/ `npx vercel` / `npx netlify-cli`; `mcp_platforms.go` uses bare `wrangler` /
`vercel` / `netlify`. Pick one per provider, apply it consistently in `ops_deploy.go`,
and note the choice in the commit body. Do not edit `mcp_platforms.go` (out of scope).

### 4. Machine-aware deployability

A target is not just "detected here" — it must be RUNNABLE on the machine that
would execute it. `supabase functions deploy` needs the `supabase` CLI installed
and authed on THAT box; TestFlight is impossible off macOS.

`deploy_capabilities.go:216 ComputeDeployCapability(target, project, vs)` and
`doctor_build.go:51 buildTargets` (which already carry `Tools[]`, `Secrets[]`, and
`targetPlatformLock`) exist for exactly this. Wire detection into them:

- Add a `Runnable bool` + `Reason string` to the per-target result the verb returns
  (or a parallel struct — your call, but keep it in the same response).
- A target that is detected but not runnable here must say WHY in words a user can
  act on: `"supabase CLI not installed on this machine"`, not a bare false.
- Reuse the existing `runBuildPreflight` / `classifyNative` gate that `ops_deploy.go:115`
  already calls. Do not invent a second platform-lock table.

## The scope allowlist — read this before you edit

You may ONLY touch these paths. A single change outside them ABORTS the whole
run (not just the iteration) and rolls your work into a diagnostic git stash:

    desktop/agent/stack_detect*.go
    desktop/agent/ops_stack*.go
    desktop/agent/ops_deploy*.go
    desktop/agent/deploy_capabilities*.go
    desktop/agent/doctor_build*.go
    tasks/**
    docs/handoff/**

`mcp_platforms.go`, `classify.go`, `repos_http.go`, `devserver.go`, and the web/
mobile surfaces are all OUT of scope on purpose. If a task above seems to need a
file that is not on this list, **do not edit it** — write what you would have
changed and why into the progress file, and work around it. A note costs nothing;
a scope violation costs the entire run.

## Two rules learned the hard way — a previous attempt broke BOTH

A prior run of this exact task produced a test suite that **actually executed
`supabase db push`**. The log is not a joke:

    [exec] Started session e4c77c23…: supabase db push (pid=54107, timeout=5m0s)

It was harmless only by luck — that box had the Supabase CLI installed but no
credentials. On a box WITH credentials (the developer's laptop, any linked
project) `go test` would have pushed a schema to a live database. Do not
reproduce this.

### Rule A — separate RESOLUTION from EXECUTION, and never exec in a test

`opsDeployHandler` currently resolves a target AND calls
`c.Server.execMgr.StartExec(...)` in one function, so any test that reaches the
deploy branch really deploys. Refactor a **pure** seam:

```go
// resolveDeployCommand maps (target, payload) -> the command to run.
// Pure: no exec, no network, no filesystem writes. This is what tests exercise.
func resolveDeployCommand(p opsDeployPayload, det *StackDetection) (cmd, tool string, err error)
```

The handler becomes: validate → `resolveDeployCommand` → `StartExec`. **Tests call
`resolveDeployCommand` and assert on the returned command STRING. No test may
call `opsDeployHandler` with a live `execMgr`, ever.** If you think a handler test
is needed, inject a fake exec manager that records the command instead of running
it — but prefer testing the pure function.

This also fixes the ambiguity/inference tests: target inference is resolution, not
execution, so it is fully testable without touching a provider CLI.

### Rule B — machine capability must be INJECTABLE, not probed from the real PATH

The previous attempt's test asserted "supabase is not runnable without the CLI"
and FAILED, because the machine running the test HAD `/opt/homebrew/bin/supabase`.
A test whose result depends on what happens to be installed on the box is not a
test. It will pass on your machine and fail in CI, or vice versa.

Put the capability probe behind a seam — e.g. a `toolPresent func(string) bool`
field (defaulting to `DiscoverBinary`/`exec.LookPath`) that tests substitute, or
pass the resolved capability set in. Then test BOTH branches deterministically:
CLI present → runnable; CLI absent → not runnable, with the reason string. Do not
call `exec.LookPath` directly from the code under test.

Note this repo already has a binary-discovery helper (`DiscoverBinary`) that
handles the launchd-PATH problem where `/opt/homebrew/bin` is missing; prefer it
over raw `exec.LookPath` for the DEFAULT implementation.

## Hard rules

- **NEVER run `go test ./...` in `desktop/agent`.** `TestAuthLogout` executes
  `authLogout()` against the real `~/.yaver` and WILL sign this machine out of
  Yaver. This has happened before and cost hours. This checkout is isolated but
  `~/.yaver` is shared per-user, so a broad run signs the mini out. ALWAYS scope:
  `go test -count=1 -run 'TestStackDetect|TestOpsStack|TestOpsDeploy' .`
- **Never touch `~/.yaver`** for any reason.
- **Do not deploy anything.** No `wrangler deploy`, no `vercel`, no `convex deploy`,
  no `supabase db push` against a real project. You are wiring the code paths, not
  exercising them against live infrastructure. Tests must not make network calls.
- **Stay inside the scope allowlist.** Do not edit files outside it — the autorun
  will refuse to commit and the iteration is wasted.
- Tests: real fixtures on disk, no mocks, table-driven where it fits. Follow the
  style already in `stack_detect_test.go` (`writeTree` helper).
- Match surrounding code style. Comments explain constraints, not narration.
- `gofmt -w` every file you touch.

## Definition of done

- `go build ./...` clean in `desktop/agent`.
- `go test -count=1 -run 'TestStackDetect|TestOpsStack|TestOpsDeploy' .` passes,
  including NEW tests covering: single-target inference, the ambiguous
  two-target refusal, the zero-target error, explicit-target-wins, the Supabase
  functions/db branches, and a weak (dep-only) supabase target being refused.
- No behavior change when `target` is passed explicitly.
