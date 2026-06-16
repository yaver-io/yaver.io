# Yaver Remote Test Runner — Self-Growing, Feature-Based, AI-Authored

> **Goal (user, 2026-06-16):** From the Yaver **mobile app + web UI Projects tab**, point at a
> **remote PC** and have the **Yaver runner** author, run, and **continuously grow** web + mobile
> tests for any project (including third-party). Record **video**, present pass/fail as
> **feature-based highlights** (football-highlights / YouTube-series style). Use **chromedp +
> playwright + redroid**. Tests **self-iterate during vibe-coding** — Yaver writes and expands the
> `yaver-tests/*.yaml` specs itself, no user prompting.

This is a **reuse-first** design. Most of the execution infra already exists; the work is wiring,
one auth gap, an ops verb, two UI surfaces, the AI test-author loop, and a highlights viewer.

---

## 0. What already exists (verified 2026-06-16) — DO NOT rebuild

| Capability | Where | Status |
|---|---|---|
| Web test driver (chromedp/CDP) | `desktop/agent/testkit/runner.go:runWebSpec`, `driver_chromecdp.go` | ✅ |
| Mobile test driver (redroid, no emulator/KVM) | `desktop/agent/testkit/driver_androidredroid.go`, `studio/redroid.go` | ✅ |
| iOS sim driver | `testkit/driver_iossim.go` | ✅ |
| **Video / screencast (web)** → FrameRing → flushed PNG seq | `testkit/runner.go:247` (`artifacts.video`/`ForceVideo`) + `StartScreencast` | ✅ |
| Mobile frame scrubber (`FrameSequencePlayer`) | mobile app | ✅ |
| Per-step screenshots, console/network/a11y/HAR capture | `testkit/runner.go`, `InstallInstrumentation` | ✅ |
| Spec format (`*.test.yaml`) + discovery + parallel `RunSuite` | `testkit/spec.go`, `testkit/scheduler.go` | ✅ |
| Agentic LLM-driven flows (`*.flow.yaml`) + oracle bank | `qa_jobs.go:runQAFlows`, `qa_brain.go`, `studio.DefaultOracles` | ✅ |
| **Remote PC execution** via `SSHRunner` (Exec/PutFile/GetFile) | `studio/runner.go:80` | ✅ |
| Async jobs + polling + artifacts | `studio_jobs.go`, `qa_jobs.go`, verb `studio_job_status` | ✅ |
| Ops verb registry + machine routing (`local`/`primary`/deviceId → `proxyToDeviceAs`) | `ops.go:139,175`, `mcp_remote_proxy.go:115` | ✅ |
| Mobile QA screen (device pick, run, poll, report card) | `mobile/app/qa.tsx`, `mobile/src/lib/qaClient.ts` | ✅ |
| Web QA panel + machine picker pattern | `web/components/dashboard/QAPanel.tsx`, `ProjectsView.tsx`, `lib/agent-client.ts:callOps` | ✅ |
| **Cookie/session auth for web specs** (`cookies:` block, CDP `SetCookie`, `${ENV}`) | `testkit/spec.go` + `runner.go:seedCookies` | ✅ **added 2026-06-16** |

**Gaps to build:** (1) testkit as an **ops verb** (today MCP/CLI only), (2) **project-aware** test
routing (resolve project → repo → `yaver-tests/` → remote host), (3) **Projects→Tests UI** on
mobile + web, (4) **highlights** report viewer over the frames, (5) the **AI test-author loop**
(self-growing specs), (6) optional **Playwright** driver (chromedp covers web today).

---

## 1. Test model — industry-standard, feature-based

Keep two existing artifacts; treat them as the BDD model:

- **`yaver-tests/*.test.yaml`** — deterministic **feature scenarios** (Gherkin-equivalent): `name`
  = Feature, `steps` = Given/When/Then (goto/click/fill/wait_for/assert.*). This is the
  "industry-standard data model": a Feature with Scenarios + Steps + Assertions, expressible 1:1
  from Gherkin `.feature` files (add a thin Gherkin→spec importer if a team brings `.feature`s).
- **`yaver-tests/flows/*.flow.yaml`** — **agentic** flows the LLM brain drives when there's no
  deterministic selector yet (exploratory coverage). Oracles assert quality.

Each Feature carries `tags:` (already on `A11yStep`; lift to spec level) so the highlights UI can
group "by feature" (Auth, Checkout, RFQ-Engine…). A per-project **coverage ledger**
(`yaver-tests/.coverage.json`) records which features/routes/components have specs, so growth is
monotonic and de-duplicated.

---

## 2. Self-growing, AI-authored specs (the key point)

The Yaver **runner** (claude/codex/opencode on the remote PC) is both **author** and **executor**.
During vibe-coding, after the runner ships a change it runs a **test-author step**:

```
vibe-code change ──► diff + route/component map ──► test-author step:
   1. read coverage ledger (.coverage.json) — what's already specced
   2. for each NEW/CHANGED route|component|feature with no spec:
        synthesize a *.test.yaml Feature (selectors from the just-written DOM/testids,
        assertions from the acceptance intent of the change)
   3. append/refresh specs, update ledger, never delete green coverage
   4. run the new + impacted specs on the remote PC; attach video
   5. if a spec is flaky/red on a correct app, self-heal selector (testkit_self_heal_selector)
```

Mechanics (reuse-first):
- The author step is a **runner sub-prompt** (a `playbook`) invoked by the agent loop, not new
  infra. Input = `git diff` + `project_context` + ledger; output = spec files written into
  `yaver-tests/`.
- Trigger points: (a) end of each vibe-code task, (b) the **ops verb** `project_test_grow` (manual
  "grow my tests"), (c) a schedule (`schedule_task`) for nightly coverage expansion.
- **Third-party projects:** identical — `project_context` infers stack/routes; the author step
  needs only the repo + a base URL (+ auth env). No per-project code. `data-testid` backfill is
  itself a suggested vibe-code change the runner can make to raise determinism.
- Guardrails: specs are **append/refresh-only** for green coverage; a red spec on a known-good app
  triggers self-heal, not deletion; the ledger caps runaway growth (one Feature per route/component
  until asked to deepen).

---

## 3. Execution + remote PC

```
Projects tab (mobile app / web dashboard)
  → pick PROJECT + pick REMOTE PC (runner) + suite (web|mobile|both|changed-only)
  → callOps("project_test_run", {project, host, suite, grow?:bool})
      ops.go dispatch → machine routing (local | deviceId via proxyToDeviceAs | ssh host)
      → NEW ops_testkit.go handler:
          resolve project → repo path → yaver-tests/ + base_url + auth env (.yaver/project.yaml `tests:`)
          if grow: run AI test-author step first (§2)
          DiscoverSpecs(root) → RunSuite(ctx, specs, {ForceVideo:true}, concurrency)
            web  → chromedp (SSHRunner-agnostic: runs where the agent runs; pick PC by routing the verb)
            mobile → RedroidSurface on SSHRunner{Host: pickedPC}  (reuse qa_jobs surface/runner seam)
          → studioJobs async job, artifacts dir (video frames + screenshots + report.json)
  → poll studio_job_status → render highlights (§4)
```

- **Remote PC selection** = the existing machine routing. "Run on magara" → the verb is dispatched
  to magara's agent (`machine: <deviceId>`), or web specs run under an `SSHRunner{Host:"kivi@10.0.0.45"}`
  added to the web path (mirror `qaConfigFromRequest`’s runner seam, which today only wires the
  redroid/mobile path).
- **chromedp vs playwright:** chromedp is the default and ships in-binary. Playwright is
  reserved (`RunnerJobPlaywright`, `package_ops.go:224`); add `testkit/driver_playwright.go`
  (playwright-go + node sidecar) and a `target: web-playwright` only where a team needs
  Playwright-specific features (codegen, trace viewer). Same spec format.

---

## 4. Highlights — "match highlights / YouTube series"

The frames + per-step results already exist; the highlights layer is presentation:

- Per Feature, stitch the screencast frames spanning its steps into a **clip** (mp4 via ffmpeg, or
  the existing FrameSequence for in-app scrub). Pass = green chip + a short "goal achieved" clip;
  fail = red chip + the failing step's clip auto-seeked to the assertion, with the console/network
  diff overlaid.
- **Report shape** (extend `qaReport`): `features:[{name, tags, status, durationMs, clipPath,
  failStep?, thumbnail}]`, plus a top **reel** (concatenated highlights, newest run first — the
  "series"). Counts: passed/failed/new-coverage.
- **Mobile:** new screen reuses `FrameSequencePlayer` + the `ReportCard` pattern (`qa.tsx`),
  grouped by feature with play buttons. **Web:** new `WebTestsPanel.tsx` reuses `QAPanel` report
  rendering + a `<video>`/frame carousel.

---

## 5. Phased build (exact files) — ALL SHIPPED 2026-06-16 (build/typecheck clean)

**P0 — Auth enabler ✅** `cookies:` on web specs. `testkit/spec.go` (`SpecCookie`, `expandEnv`),
`testkit/runner.go` (`seedCookies` via CDP). `go build ./testkit` clean.

**P1 — Project-aware ops verbs ✅** `desktop/agent/ops_testkit.go`: `project_test_specs`,
`project_test_run`, `project_test_report`, `project_test_grow`, `project_test_artifact`. Runs
`testkit.RunSuite` in a `studioJobs` job; env-injection (`${ENV}` secrets) serialized under
`testkitEnvMu`. Web runs where the agent runs → remote PC = `OpsRequest.machine` routing.

**P2 — Self-growing author loop ✅** `desktop/agent/testkit_grow.go`: `growTestPlan` scans Next/Expo
routes, diffs the `.coverage.json` ledger, returns the author plan + prompt for the runner. Monotonic,
never deletes green coverage.

**P3 — Mobile Projects→Tests ✅** `mobile/app/project-tests.tsx` + `mobile/src/lib/testkitClient.ts`
(mirror `qaClient.ts`) + `apps.tsx` action-sheet "🧪 Tests" entry → `/project-tests`. Remote-PC
picker = `useDevice()`. Plays per-Feature mp4 clips (expo-av) + screenshots (data-URI), deps banner.

**P4 — Web Projects→Tests ✅** `web/components/dashboard/WebTestsPanel.tsx` + quality-tab mount in
`web/app/dashboard/page.tsx`. Feature highlights + grow + "Install test tools" via `agentClient.callOps`.

**P5 — Highlights ✅** Per-Feature ffmpeg clip + combined reel in `ops_testkit.go`
(`stitchFramesToMP4`/`concatMP4`), served by `project_test_artifact` (base64), played in both UIs.

**P6 — Playwright driver ✅** `testkit/driver_playwright.go` + `target: web-playwright`: spec →
generated Node Playwright script → run → per-step `@@STEP` parse. Graceful if node/PW absent.

**P7 — Dependency bootstrap ✅ (so a user never fails on missing tooling)** `ops_testkit_deps.go`:
`testkit_deps_check` + `testkit_deps_install` (ffmpeg, chromium, node, playwright+chromium,
docker+redroid image) — one-shot, idempotent, brew/apt/dnf/pacman. Surfaced as a one-tap installer
banner in mobile + a button in web.

---

## 6. First proof (Talos as the pilot third-party-style project)

`talos` repo now has `.yaver/project.yaml` (`tests.web.base_url=https://talos.works`, cookie auth)
and `yaver-tests/`:
- `rfq-engine.test.yaml` — cookie-auth → QE list → asserts both ASELSAN packages + video. Passes
  against current prod.
- `rfq-detail-regression.test.yaml` — clicks `data-testid=qe-project-row-<id>` into Paket 2 detail
  and asserts it renders (guards the non-ASCII `getProjectFull` 500 fixed 2026-06-16). Green after
  the next web deploy ships the testid.

Install + run: `npm install -g yaver-cli` (brings the runner + chromium/ffmpeg/playwright via the
postinstall stacks), then `TALOS_SESSION_TOKEN=<token> yaver test run` (locally or on magara). This
is the seed the author loop (§2) grows from. Any still-missing tool is one tap away via the in-app
"Install test tools" button (`testkit_deps_install`).

---

## 7. Guardrails
- Session token / secrets via `${ENV}` only — never written into specs or the repo.
- Author loop is append/refresh-only for green coverage; ledger caps growth; self-heal over delete.
- Remote exec honors existing ops auth (owner/guest roles, user-bearer forwarding in `proxyToDeviceAs`).
- Read-only against third-party prod unless the project opts into write flows (ephemeral test accounts, as `qa_run` already does).
