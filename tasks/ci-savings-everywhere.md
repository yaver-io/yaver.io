---
doer: codex
---

<!-- Single seat. claude is not authed on the mini; seats in front matter are
     binding, so naming an unauthed master fails the run at iteration 1. -->

<!-- Depends on tasks/ci-one-bus.md Part 3 (CIRunView). If that has not landed,
     build the ledger anyway (Parts 1-2) and leave the merged view to it. -->

# The CI bill is the product — book the savings for every local run, not just one of three engines

## Why

The savings ledger exists, works, and is already on screen —
`CIRunnerView.tsx:176-182` and `mobile/app/ci.tsx:180-186` both render
"GitHub would have billed $X · you paid $Y · saved $Z". `ops_ci.go:23` sells it
in the verb description: *"GitHub bills $0 for the minutes."*

**But it only books Model 1.** `appendCISavingsLedger` (`ci_selfhosted_runner.go:350`)
is called from the ephemeral-runner supervisor and nowhere else. So:

| Engine | Runs on your hardware | Saves real money | Books savings |
|---|---|---|---|
| Model 1 — self-hosted runner | yes | yes | **yes** |
| Model 3 — `pipeline_run` (local workflow) | yes | **more** — GitHub never ran it at all | **no. zero.** |
| Model 2 — `.yaver/ci.yaml` | yes | yes | **no. zero.** |

This is backwards. `pipeline_run` interprets the workflow entirely on your box —
GitHub is never involved, so 100% of those minutes are saved. It books **nothing**.
Model 1 still round-trips through GitHub's orchestration and books everything.

The user's ask, verbatim: *"people pay pretty much to github gitlab ci etc i want
yaver to reduce those costs."* **The number is the pitch.** A "$0.00 saved"
counter next to a CI system that just saved you forty minutes of Actions billing
is the product failing to state its own case.

## Part 1 — one ledger, every engine

`appendCISavingsLedger` currently takes `CIRunResult` (`ci_selfhosted_runner.go:350`)
— Model 1's type. Widen it to a small engine-neutral fact so all three can book:

```go
// CISavingsEntry is what any local execution reports when it finishes.
type CISavingsEntry struct {
	RunID     string
	Engine    string // "self-hosted" | "native" | "local-workflow"
	Provider  string // "github" | "gitlab" | "" (native)
	RunnerOS  string // linux|macos|windows — picks the upstream rate
	Minutes   float64
	SavedCents   int
	ChargedCents int // >0 only for operator-fleet / yaver-cloud
	At        int64
	Machine   string
}
```

Keep the existing on-disk ledger file and **keep reading old rows** — entries
written before this change have no `Engine`. Default a missing/empty `Engine` to
`"self-hosted"` on read. Do not migrate the file, do not rewrite it, do not
break `CISavingsSummary` (`ci_selfhosted_runner.go:377`) — both UIs already
parse its exact shape (`{runs, chargedCents, wouldHaveCostUpstreamCents, savedCents}`).
**Add fields; never repurpose one.**

Then book from the other two:

- **Model 3** — at the end of `PipelineRunner.Run` / `RunGitLab` (`pipeline.go:579`/`:921`).
  Rate: `githubActionsCentsPerMin[runtime.GOOS]` (`ci_selfhosted_runner.go:282`),
  the same lookup Model 1 uses. `ChargedCents: 0` — it ran on hardware the user
  already owns.
- **Model 2** — at the end of `RunCI` (`ci_runner.go:83`).

### The honesty rules — read these twice

The savings number is a **claim about someone else's bill**. If it is inflated,
the whole cost-reduction pitch is worth nothing, and it is exactly the kind of
number a user will one day check against a real invoice.

1. **A run that did not really run does not save anything.** With
   `tasks/ci-one-bus.md` Part 2 landed, a `Status: "unsupported"` result books
   **zero**. A `Degraded: true` pass (ran with `allowUnsupported`) books **zero**
   — we cannot claim to have replaced a workflow we half-executed. Only a clean
   `passed`/`failed` books. **A failed run still saves** — GitHub bills for failed
   minutes too, and that is honest.
2. **Never bill-shame with a hypothetical.** The rate table is GitHub's *public
   list price for private repos*. A public-repo user pays **$0** for Linux
   minutes. Booking "saved $4.10" against a public repo is a lie.
   `CIRunnerRegistration.PrivateOnly` (`ci_selfhosted_runner.go:81`) already
   exists for Model 1. Model 3 has no registration to consult — so **detect the
   repo's visibility** (`gh repo view --json isPrivate`, cached; `glab` equivalent)
   and when it is public, or when visibility is **unknown**, book
   `SavedCents: 0` and set `RateBasis: "unknown"`. Unknown → zero. Never guess up.
3. **Include-minutes are not free minutes, but they are not list price either.**
   The free tier (2,000 min/mo Free, 3,000 Pro, …) means the first N minutes of a
   private repo genuinely cost $0. We do not know the user's plan or their
   month-to-date usage, and **we must not ask Convex or the forge to track it.**
   So: state the basis, don't model the plan. `RateBasis: "list-price"` and a
   surface string that says **"at GitHub's list price for private repos"** — not
   "you saved". Overclaiming here is the one thing that would make this feature
   an embarrassment.
4. `githubActionsCentsPerMin` is a **hardcoded price table for someone else's
   product** (`ci_selfhosted_runner.go:280-282`). It will go stale. Add a comment
   with the date it was last verified and the URL. If it is already stale by the
   time you read this, note it in the progress file — **do not silently update
   prices you did not verify.**

## Part 2 — savings for the thing that costs the most: deploys

CI minutes are the visible bill. But `CLAUDE.md`'s own deploy table is a longer
list of metered things — Cloudflare Workers requests, Convex function calls,
GitHub Actions minutes, TestFlight's hard ~15-20 uploads/app/day cap. And the
house rule is already written:

> **Deploys and cloud tools cost money — coalesce, never spray.** … Cost-awareness
> is a product requirement, not just a house rule — it is the whole "lower dev
> opex" wedge. Cloud tool usage and deploys should report what they cost
> (`remote_cost`, `switch_cost` are the existing seams).

`deploy_all.go` (`DefaultDeploySteps`, `:82-107`) runs every deploy **locally on
this Mac** — exactly per the "Local deploy first, CI second" rule. Each one is CI
minutes not spent. It books nothing.

**In scope:** book a savings entry from `deploy_all`'s local steps with
`Engine: "local-deploy"`, so `yaver ci savings` answers *"what did running my own
deploys save me this month"*. Rate: the same per-minute table, since the
counterfactual is `release-web.yml` / `release-cli.yml` on a GH runner.

**Out of scope, explicitly:** modelling Cloudflare/Convex/TestFlight quota
consumption. That is a different, larger feature (`remote_cost` / `switch_cost`
are the seams; they are not this task). If you find yourself reading Cloudflare's
pricing page, stop and write it in the progress file.

## Part 3 — surface it where the decision gets made

Both CI views already render the Model 1 ledger. Once Part 1 lands they render
**all three engines** with no UI change — that is the point of one ledger.

What to actually change:

**3a. `CIRunnerView.tsx` (281 lines) is `runner`-shaped, not `CI`-shaped.** It's
mounted at `web/app/dashboard/ci/page.tsx` (743 bytes — a thin wrapper). With
`ops ci_runs` from `tasks/ci-one-bus.md` Part 3, it should show **runs across all
three engines** with an engine badge, not just registrations. The savings block
moves from "this box's runner ledger" to "your CI bill, everywhere". Keep the
`dollars()` helper and the existing `Savings` type shape (`:26`).

**3b. Say the basis, not just the number.** Current string: *"GitHub would have
billed $X · you paid $Y · saved $Z"*. That sentence is stated as fact about
GitHub's invoice. Per honesty rule 3 it must read as a list-price estimate for
private-repo minutes, and a `RateBasis: "unknown"` run must not contribute to the
headline at all. Same string in `mobile/app/ci.tsx:180-186` — **both surfaces,
same change, same commit** (cross-surface parity is a house rule; web and mobile
here are separate code, not shared RN).

**3c. `ci_runner_status` returns the ledger already** (`ops_ci.go:128`, described
as *"registrations + live flag + local savings ledger"*). Add `ops ci_savings
{ machine?, since? }` as its own verb so a surface can ask for the number without
pulling runner registrations, and so `machine` lets you ask the Mac mini what
*it* saved. Both buses, per `tasks/ci-one-bus.md`.

**3d. Do NOT put the ledger in Convex.** It is per-run execution bookkeeping
about the user's own repos. `ops_ci.go:10` is explicit that registration is
HOST-LOCAL and never Convex; the ledger follows the same rule.
`convex_privacy_test.go` enumerates forbidden keys and scans for path leaks — a
ledger row carrying `ProjectDir` or a repo path would trip it, and rightly so.
The number is read live over `ops` + `machine`, like everything else.

## Prior art — read before inventing

- **`ci_selfhosted_runner.go:280-291`** — the rate table and
  `estimateUpstreamCents`-style lookup. Reuse; do not fork a second table.
- **`ci_selfhosted_runner.go:350` `appendCISavingsLedger`** + **`:377`
  `CISavingsSummary`** — the ledger's write path and the exact JSON both UIs
  parse. This is the compatibility contract.
- **`ci_selfhosted_runner.go:81` `CIRunnerRegistration.PrivateOnly`** — the
  existing precedent that public repos are a different pricing world.
- **`CIRunnerView.tsx:26,96,176-182`** and **`mobile/app/ci.tsx:17,61,180-186`** —
  both consumers. Any shape change breaks both; additive fields break neither.
- **`remote_cost` / `switch_cost`** — the existing "report what it cost" seams and
  the tone to match. Read one before writing a new cost string.
- **`docs/yaver-managed-cloud-ci-absorption.md`** — cited by `ops_ci.go:23`. Read
  it, and per the repo's first rule: **if it disagrees with the code, the doc is
  the bug — fix it in this change.**

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

- All three local engines book to **one** ledger; `pipeline_run` — the engine that
  saves the most — stops booking zero.
- Old ledger rows still parse; `CISavingsSummary`'s shape is unchanged; both UIs
  keep working without edits.
- **An `unsupported` or `Degraded` run books zero.** A failed run books normally.
- **A public repo books zero.** Unknown visibility books zero. Never guess up.
- The surfaces say *"at GitHub's list price for private repos"* — an estimate with
  a stated basis, not a claim about the user's actual invoice.
- `ops ci_savings --machine=<id>` answers "what did that box save me".
- Local deploys (`deploy_all`) book savings too; Cloudflare/Convex/TestFlight
  quota modelling is explicitly NOT attempted.
- Nothing about the ledger reaches Convex.
