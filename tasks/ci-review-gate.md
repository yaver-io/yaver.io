---
doer: codex
---

<!-- Single seat. claude is not authed on the mini; seats in front matter are
     binding, so naming an unauthed master fails the run at iteration 1. -->

# Optional code-review gate — off by default, and the per-project settings surface it needs

## Why

Owner's ask, verbatim: *"optional code review mandatings etc too but code review
thing should be off by default in yaver projects."*

Verified 2026-07-17: **there is nothing to turn off.** A repo-wide grep for
`code_review|codeReview|code review` across every `.go`/`.ts`/`.tsx` returns two
incidental hits — prose in `web/app/docs/contributing/page.tsx` and a comment in
`hermes_isolation_test.go`. Same for `requireReview|require_review|blockMerge`.
The only `needs_approval` codes are physical-machine write confirmations
(`ops_machine_driver.go:303,333,361`) and gateway dry-run confirms
(`ops_gateway.go:58`).

So this is net-new, not a default flip. Three adjacent things exist and are all
**false friends** — read this table before you start, it will save you an hour:

| Looks like review | Actually is |
|---|---|
| `app_review_check` (`mcp_tools.go:1823`) | **App Store / Play submission guidelines.** Nothing to do with code. |
| `managed_quality_run` (`quality.go:212`) | test/lint/typecheck/format fan-out. Returns `[]*QualityResult` — **and nothing consumes the verdict.** No gate. |
| `talos_quality_run` (`ops_quality.go:57`) | Computes `talosQualityReport{Passed bool}` — **also consumed by nothing.** |
| `github_pr_create` (`git_provider_cli.go`) | `{directory,title,body,base,head,draft}` — **no reviewers field, no approval polling, no merge.** |

The one real precedent for check-then-block in the whole repo is
`mobile_platform_deploy`'s `validate_driver` (`mcp_tools.go:2103`), which runs
Selenium/CDP autotest before upload. **Model the gate on that**, not on the
quality verbs — they are exactly the cautionary tale (a verdict computed and
thrown away).

## Part 1 — the flag, and why it defaults off for free

`DeployConfig` (`deploy_pipeline.go:41-48`) is `.yaver/deploy.yaml` **and** is
embedded as `ProjectManifest.Deploy` (`project_manifest.go:18-31`), so one field
reaches both surfaces:

```go
type DeployConfig struct {
	Branch        string `yaml:"branch,omitempty"`
	BuildCommand  string `yaml:"buildCommand,omitempty"`
	StartCommand  string `yaml:"startCommand,omitempty"`
	Healthcheck   string `yaml:"healthcheck,omitempty"`
	WebhookSecret string `yaml:"webhookSecret,omitempty"`
	AutoDeploy    bool   `yaml:"autoDeploy,omitempty"`
	RequireCodeReview bool `yaml:"requireCodeReview,omitempty"` // NEW — default false
}
```

`loadDeployConfig` (`deploy_pipeline.go:53-61`) returns
`DeployConfig{Branch: "main"}` on any read error and `yaml.Unmarshal`s into a
zero struct otherwise. **So a bool defaults to `false` for every existing project
with no migration and no backfill.** Off-by-default is free here — that is why
this is the right home. Do not add a `*bool` tri-state; do not add a default-on
path anywhere. `AutoDeploy` next to it is the precedent: a policy bool that is
off unless the user wrote it.

**The word "mandating" is the user's, and it means the user mandates it — never
us.** There is no Yaver-side default, no "recommended" nag, no org policy that
turns it on. A project with no `.yaver/deploy.yaml` has no gate, forever.

## Part 2 — what the gate actually does

`RunDeploy` (`deploy_pipeline.go:76`) already has the shape: fetch → checkout →
pull → build → **`.yaver/ci.yaml` gate** (`:133-150`) → migrations → swap →
healthcheck → auto-rollback (`:193`). The CI gate at `:133-150` is your template
— it already knows how to block a deploy and honour an `onFail: warn` escape.

Add the review check **immediately after the CI gate, before migrations.**
Rationale: never block on review a deploy that CI would have rejected anyway —
the user gets the cheaper, more actionable failure first. Migrations are the
first irreversible step, so the gate must precede them.

```
ops code_review_status { dir?, commit? } -> { required, satisfied, reason, reviewers[] }
```

`required: false` → `satisfied: true`. Always. The disabled path must be a
constant-time yes with no forge call, no network, no token read.

**When required, what satisfies it?** The forge is the source of truth — we do
not invent a review system:

- **GitHub**: the commit's PR has an `APPROVED` review and no `CHANGES_REQUESTED`.
  `gh api` — `gh pr list --search <sha>` → `gh api repos/{o}/{r}/pulls/{n}/reviews`.
- **GitLab**: the MR for the commit has an approval. `glab mr list` → approvals API.
- **No PR/MR found for this commit** → `satisfied: false`,
  `reason: "no_pull_request"`. **Not** an error, and **not** a pass. A commit that
  never went through a PR is precisely the thing the gate exists to catch.
- **Forge unreachable / not authed** → `satisfied: false`,
  `reason: "review_status_unknown"`. **Unknown is never a pass.** A gate that
  opens when it can't see is not a gate. This is the same law as the savings
  ledger's "unknown → zero": when we don't know, we do not claim the good outcome.
- **Self-approval does not count.** If the only approver is the commit's author,
  `satisfied: false`, `reason: "self_approved"`. Yaver's whole audience is solo
  founders (`user_target_audience`), so this WILL fire for a solo dev who turned
  the flag on — and the message must say so plainly: *"you approved your own PR;
  set requireCodeReview: false if you're working solo."* Don't silently pass it,
  don't lecture.

**Escape hatch, mirroring `.yaver/ci.yaml`'s `onFail: warn`:** `deploy_run` gains
`--force` semantics that record the override in the `DeployRecord`
(`deploy_pipeline.go:24`) — `ReviewOverride bool` + who + when. `deploy_all.go`
already has `Force bool` and a `GateStatus string // green|red|forced`
(`:64,:72`); **reuse `forced` — do not invent a second override vocabulary.** An
override that isn't recorded is just a bug with extra steps.

## Part 3 — the per-project settings surface (this is the "UI wirings" ask)

**There is no per-project settings screen in web or mobile.**
`ProjectDetailView.tsx` is 209 lines, no tabs, read-only stats + backend tables.
So `requireCodeReview` has nowhere to be toggled, and neither does `autoDeploy`,
`branch`, `buildCommand`, or anything else in `DeployConfig`. Every project
setting Yaver has is edit-the-YAML-by-hand today.

Build the surface once, generically, and the flag rides it:

- `ops project_config_get { dir }` / `ops project_config_set { dir, patch }` —
  read/write `.yaver/deploy.yaml` + the `ProjectManifest`. Both buses per
  `tasks/ci-one-bus.md`. Owner-only: it edits deploy policy.
- `ProjectDetailView.tsx` gains tabs — **Overview** (what it renders today,
  unchanged) and **Settings**. Settings renders `DeployConfig` fields, with
  `requireCodeReview` as an off-by-default switch and one line of copy stating
  what it gates and that the forge is the authority.
- Mobile parity: the same toggle. `mobile/app/` has no project-detail screen —
  if adding one is too large for this run, **say so in the progress file and ship
  web-only rather than half-wiring mobile.** Cross-surface parity is a house
  rule, but an honest gap beats a broken screen.

**Convex stays out of this.** `userProjects` (`schema.ts:1977-2005`) has **no
flags column** — and note `CLAUDE.md:223` claims the contract is "slug + deviceId
+ **flags** + branch", which is **wrong**: there is no flags field. Per the repo's
first rule, **the doc is the bug — fix that line in this change.** Do not add a
flags column to satisfy the doc. Deploy policy is project config; it lives in the
project's own `.yaver/`, reachable over `ops` + `machine` like everything else,
and it never touches `convex_privacy_test.go`'s forbidden-key list.

## Prior art — read before inventing

- **`deploy_pipeline.go:133-150`** — the `.yaver/ci.yaml` gate. Your template for
  block-with-an-escape-hatch. Read how `onFail: warn` is honoured.
- **`deploy_all.go:64,:72`** — `Force bool` + `GateStatus green|red|forced`. The
  override vocabulary already exists. Use it.
- **`mobile_platform_deploy` / `validate_driver`** (`mcp_tools.go:2103`) — the only
  check-then-block precedent in the repo. Match its shape.
- **`quality.go:212` `RunAllQualityChecks`** — the anti-pattern. It computes a
  verdict nobody reads. Do not add a fifth `QualityCheckType`; do not route the
  review gate through it.
- **`project_manifest.go:94-98` `ManifestPlacementPolicy`** — the closest existing
  per-project policy block (`prefer_owned`, `allow_managed_cloud`,
  `monthly_budget_usd`). Match its naming style.
- **`git_provider_cli.go`** — `mcpGitHubPRCreate` and the `gh`/`glab` preflight
  (install + auth check) you'll reuse for the reviews query.

## DO NOT BUILD. DO NOT RUN TESTS.

Owner's instruction: **do the coding, commit, push to main. That is all.**

No `go build`, no `go test`, no `tsc`, no gradle/xcodebuild — not even to check.
This box runs several autoruns at once and a Go build cache is what filled its
disk to 1.1 GB free before (`reclaimAutorunDisk` exists for that).

So **nothing verifies your edits.** Edit conservatively; if a change needs a
compiler to know whether it is right, write it under "Needs verification" in the
progress file instead of guessing.

**NEVER** run a bare `go test ./...` in `desktop/agent` — `TestAuthLogout` hits
the real `~/.yaver` and signs the owner out.

## Done means

- `requireCodeReview` exists in `.yaver/deploy.yaml`, is **false for every
  existing project with no migration**, and there is no code path anywhere that
  turns it on by default.
- When off: a constant-time pass. No forge call, no token read, no latency.
- When on: the **forge** decides. No PR → blocked. Unknown → blocked.
  Self-approval → blocked, with a message that names the flag to turn off.
- An override is possible and is **recorded** in the `DeployRecord` with who and
  when, reusing `forced`.
- The gate sits after the CI gate and before migrations.
- `ProjectDetailView` has a Settings tab that can edit `DeployConfig` at all —
  the first per-project settings surface Yaver has.
- `CLAUDE.md:223`'s false "flags" claim about `userProjects` is fixed.
- Nothing new reaches Convex.
