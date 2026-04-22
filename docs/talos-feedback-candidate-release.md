# Talos Self-Improving Feedback Flow

## Goal

Talos should be able to accept UI/UX feedback from real users through the Yaver Feedback SDK on both web and mobile without sending every AI-made change straight to `main` or directly to production.

The target behavior is:

1. User reports a UI issue from Talos web or mobile.
2. Yaver agent proposes and applies a fix in an isolated candidate lane.
3. The user sees the change quickly on a preview deployment or OTA-style candidate update.
4. The user can say:
   - "this is good, keep it"
   - "revert this"
   - "not like this; change it to X"
5. Only approved changes are promoted to the real production lane.

This is a safer version of "self-improving" for an ERP, especially when non-technical operators may trigger feedback frequently.

---

## Existing Yaver Primitives To Reuse

This repo already contains most of the low-level building blocks:

- Feedback SDK auth endpoint overrides for prod vs staging:
  - `sdk/feedback/web/src/auth.ts`
  - `sdk/feedback/react-native/src/auth.ts`
- Preview/rollback controls in the agent client:
  - `web/lib/agent-client.ts`
- Revert-oriented UI patterns:
  - `web/components/dashboard/MorningView.tsx`
  - `web/components/dashboard/SwitchView.tsx`
- SDK wording that already treats fixes as staged, not committed:
  - `sdk/feedback/react-native/src/FixReport.tsx`

The missing piece is the product contract tying a feedback report to a deployable candidate environment and an explicit approve/revert lifecycle.

---

## Required Model

Talos should have three lanes per surface:

### 1. Production

The official deploy. Stable. No feedback-driven direct writes.

### 2. Candidate

Auto-generated from feedback. User-facing, fast, revertable.

Examples:

- Web: preview URL or secondary domain like `candidate.talos.app`
- Mobile: candidate OTA/update channel or internal preview build
- Backend behavior: isolated env vars and isolated Convex deployment

### 3. Development

Developer-only worktree / branch / dev server lane where the agent iterates before publishing a candidate.

---

## The Core Rule

Feedback-generated changes must never go:

`feedback report -> patch -> main -> production`

They should go:

`feedback report -> patch -> candidate branch/deploy -> user review -> promote or revert`

---

## Proposed Entities

Add a Talos/Yaver concept of a **Feedback Change Set**.

Each change set should store:

- `id`
- `projectId`
- `surface` = `web | mobile | backend`
- `sourceReportId`
- `requesterUserId`
- `status` = `draft | building | candidate_live | approved | rejected | reverted | superseded`
- `baseCommitSha`
- `candidateCommitSha`
- `productionCommitSha?`
- `candidateUrl?`
- `candidateUpdateId?`
- `summary`
- `filesChanged[]`
- `diffStats`
- `createdAt`
- `approvedAt?`
- `revertedAt?`
- `supersedesChangeSetId?`

Also store a **Feedback Review Thread**:

- original request
- agent summary of what changed
- user response
- follow-up corrections
- revert reason

This thread is what powers "I am not satisfied; make it more compact / lighter / clearer."

---

## Environment Strategy

## Web

Talos web should have:

- `TALOS_WEB_PROD`
- `TALOS_WEB_CANDIDATE`

And separately:

- `YAVER_FEEDBACK_ENV=production|candidate`
- `YAVER_FEEDBACK_PROJECT_KEY`
- `YAVER_FEEDBACK_CONVEX_SITE_URL`
- `YAVER_FEEDBACK_WEB_BASE_URL`

The existing SDK endpoint override hooks already support this model.

Recommended behavior:

- Production users continue using production app data.
- Candidate preview uses either:
  - production-like read-only data with guarded writes, or
  - a staging database/Convex deployment seeded from production snapshots.

For ERP safety, default to isolated candidate data for any workflow that can mutate financial or operational records.

## Mobile

Talos mobile should have:

- `talos-mobile-production`
- `talos-mobile-candidate`

Candidate delivery options:

- Expo/EAS update channel if Talos is Expo-managed
- CodePush/OTA-style JS bundle lane if applicable
- Internal preview build if native changes are required

The feedback SDK in mobile should point at the same change-set API but receive candidate status and rollback state through the mobile UI.

## Convex / Backend

Do not use one shared environment for self-improving experiments.

Use:

- `convex-prod`
- `convex-candidate`

And keep the candidate deployment tied to the active change set or at least to the candidate lane for the project.

If a feedback change requires schema changes:

- schema migrates in candidate first
- user validates
- promotion runs a controlled migrate-to-prod flow

---

## User Experience

## Report Phase

From Talos web or mobile:

- user opens Yaver feedback
- reports issue in natural language
- marks scope if useful:
  - `this screen only`
  - `all dashboard tables`
  - `mobile only`
  - `web only`

Optional toggle:

- `Try auto-improve`

## Build Phase

The agent responds with:

- what it understood
- what files changed
- whether it touched web, mobile, backend
- whether a candidate deployment is being prepared

This should be visible from both web and mobile, not only inside the terminal.

## Candidate Live Phase

Once the candidate is ready:

- web user sees `Open improved preview`
- mobile user sees `Load improved version`
- both see a short machine-written summary:
  - "Reduced row height in invoices table"
  - "Moved primary action above the fold"
  - "Increased contrast in totals card"

## Review Actions

Every change set should expose:

- `Approve`
- `Revert`
- `Change Again`
- `View Diff`
- `What changed?`

`Change Again` should create a new change set that supersedes the prior one instead of editing history in place.

---

## Revert Model

Revert must exist in two forms.

### 1. Hidden fast revert

For owners/admins only.

This is the safety switch when an auto-improvement is clearly wrong.

Examples:

- web candidate immediately rolls back to previous candidate or production preview
- mobile candidate clears the downloaded candidate bundle and falls back
- backend candidate traffic is disabled

### 2. Explicit user revert

Shown in normal UI:

- "Undo this improvement"
- "This was worse"

This should:

- mark the change set as reverted
- store the user reason
- keep the diff for training/audit
- optionally trigger a follow-up request seeded with the revert reason

Do not delete history. In ERP software, auditability matters.

---

## Promotion Model

Approval should not mean "merge raw agent patch immediately."

Approval should trigger a controlled promotion job:

1. verify candidate health
2. run smoke tests
3. confirm deploy artifact exists
4. merge or cherry-pick approved commit(s)
5. deploy production
6. retain rollback pointer

For mobile:

- if the change is JS-only, promote the approved candidate bundle to the production channel
- if native code changed, keep approval as "ready for release" and require the normal store pipeline

---

## Recommended Technical Shape

## Branching

Per project, create:

- `main`
- `feedback/candidate`
- ephemeral branches such as `feedback/<report-id>`

Flow:

1. agent creates `feedback/<report-id>`
2. applies patch
3. deploys candidate
4. if user approves, squash/cherry-pick into `feedback/candidate`
5. production promotion merges from `feedback/candidate` to `main` or picks the approved commit

This avoids polluting `main` with half-correct iterations.

## Deploy Routing

Map each change set to a deploy record:

- web candidate URL
- mobile candidate update ID / bundle ID
- backend candidate env/deploy ID

This lets web/mobile dashboards show exactly what the user is reviewing.

## Convex Schema Additions

Add tables similar to:

- `feedbackProjects`
- `feedbackChangeSets`
- `feedbackReviews`
- `feedbackDeployments`

Minimum indexes:

- by `projectId`
- by `requesterUserId`
- by `status`
- by `sourceReportId`

---

## Permissions

Not every Talos user should be allowed to self-improve everything.

Recommended roles:

- `owner`
  - can enable/disable self-improving mode
  - can approve promotion
  - can use hidden revert
- `operator`
  - can submit feedback
  - can review candidate changes
  - cannot promote to production unless allowed
- `guest/tester`
  - can submit feedback only

Also add project-level policy:

- `selfImprovingMode: off | review_required | auto_candidate`

For ERP, `review_required` should be the default.

---

## What To Build First

Phase 1 is enough to make this real without overbuilding.

### Phase 1

- Create `feedbackChangeSets` in Convex
- Connect feedback report -> change set
- Deploy web fixes to a candidate URL instead of production
- Add review actions: approve, revert, change again
- Show change-set summary in web and mobile

### Phase 2

- Add mobile candidate update lane
- Add superseded iteration chain
- Add hidden owner-only revert
- Add per-project self-improving policy

### Phase 3

- Add automatic promotion gates
- Add analytics on reverted vs approved changes
- Feed review outcomes back into agent prompting

---

## Recommendation For Talos

For Talos specifically:

- start with **web-first candidate deploys**
- keep **mobile in review-required mode**
- isolate **Convex candidate** from production
- never allow feedback-triggered writes directly to `main`
- make **revert one tap** from both web and mobile

That gives the "instant improvement" feel your dad wants, without turning ERP production into an uncontrolled live-editing surface.

---

## Concrete Next Step In This Repo

The next implementation step in `yaver.io` should be:

1. add Convex schema + API for `feedbackChangeSets`
2. teach the feedback flow to create a change set before patching
3. connect existing preview/rollback infrastructure to that change set
4. expose review actions in the existing web/mobile dashboard surfaces

That is the minimum path from "feedback SDK" to "safe self-improving Talos."
