# What's remaining for the $0/mo solo-dev task

Snapshot of everything still open for the "solo full-stack React Native
dev runs all their CI on their own hardware for zero monthly cost"
initiative, so this work can be picked up on any later day without
re-tracing the conversation.

Status at the time this was written:
- `testkit/` package contains ~22 Go files implementing the local
  runner, drivers, instrumentation, notifications, history, auto-fix
  log, and MCP integration.
- Mobile `app/(tabs)/runs.tsx` has 7 sub-tabs
  (Specs / Runs / Alerts / Auto-fixes / Devices / Flake / Setup) plus
  a screenshot + snapshot-diff viewer modal.
- HTTP endpoints under `/testkit/*` are all authed via the existing
  P2P transport; no Convex calls.
- GH Actions `ci` and `e2e` workflows green on `main`.
- The matrix in `docs/yaver_vs_saas_ci_comparison.md` is mostly green,
  except for the items below.

---

## True gaps — the dev would notice if missing

| # | Item | Why it matters | Effort |
|---|---|---|---|
| 1 | **iOS Simulator tap-by-selector via WDA (one-command install)** | Today `target: ios-sim` boots the sim, installs the app, launches it, and takes screenshots, but taps only work by coordinate. Selector taps need WebDriverAgent running on :8100. `testkit/driver_wda.go` already speaks WDA correctly (Click / SendKeys / Screenshot / findElement with predicate + class-chain + accessibility-id strategies). What's missing is a `yaver install wda` one-liner that downloads or builds a pre-signed WebDriverAgent.app + stands it up against the booted simulator so the dev never has to open Xcode. | ~1 day |
| 2 | **Mobile frame-sequence video player** | Backend captures CDP screencast PNGs on failure (`testkit/video.go`, per-step ring buffer → PNG sequence on disk). The mobile "Runs" tab's viewer modal only renders single stills. The dev can eyeball the frames on disk but can't scrub the failure on their phone. Build a small `FrameSequencePlayer` component that reads the manifest and steps through the PNGs with play/pause and a scrub bar. Reuses the existing `/testkit/artifact` endpoint. | ~1 day |
| 3 | **yaver-test-sdk user docs** | The feature set now rivals Playwright + Percy + axe-core + Applitools combined, but there's no single `docs/yaver-test-sdk.md` a new dev can read top-to-bottom. Everything is in commit messages and code comments. Write one canonical page with: intro, install, `yaver test init`, spec vocabulary reference, targets, capture config, macros, autofix log, dep installer, Hetzner deploy. Link it from README.md. | ~half day |
| 4 | **Safari driver smoke test on macOS** | `testkit/driver_safari.go` compiles and the transport path is proven by the Firefox W3C client it reuses, but there's no integration test that actually opens Safari. Safari needs `sudo safaridriver --enable` once on the host so this can't run in GH Actions — has to be a local opt-in test guarded by an env var (`YAVER_SAFARI_SMOKE=1`). | ~2 hours |
<!-- #5 landed: step-level `- include: macros/x.test.yaml` expands in
place. Implemented via `Step.Include` + `expandStepIncludes` in
testkit/spec.go. Test: `TestStepIncludeExpandsInPlace`. -->


---

## Nice-to-haves — solo dev could live without

| # | Item | Why it's low priority |
|---|---|---|
| 6 | Network throttling (slow 3G / offline) | chromedp can set it via CDP (`Network.emulateNetworkConditions`). Useful for PWA / RN-web work but most solo devs don't need it until shipping to production. Wire as a spec-level `network_profile:` field. |
| 7 | Retry-on-LLM-rate-limit for self-healing | Currently the self-heal call in `testkit/autonomous.go` is one-shot. If Mistral / Anthropic rate-limits, the step just fails through to the normal error path — which is actually fine most of the time. |
| 8 | Pinch-zoom + pan in the mobile screenshot modal | The three-tab (Baseline / Current / Diff) viewer works. Proper gesture-driven zoom would be nicer for dense UIs but isn't blocking. Use `react-native-zoom-reanimated` or equivalent. |
| 9 | HAR response body capture | We dump request metadata + timings but not response bodies. Adding them would balloon disk (~10x for API-heavy apps) and Chrome DevTools already lets the dev do it once manually via the "Preserve log" toggle. Only add if a user asks. |
| 10 | `yaver test report` HTML export | JSON + JUnit reporters already exist; a pretty HTML report is standalone marketing value, not a dev blocker. |
| 11 | Autonomous loop → testkit end-to-end integration | The `FixHandler` registry + `AutoFixLog` are wired. The parallel autodev thread needs to register its handler and write to the log. Runs on its own schedule. |

---

## Things explicitly kept out of scope

| Item | Why |
|---|---|
| Real iOS device tap-by-selector without WDA | There is no other supported path on iOS. If the dev wants taps on their own iPhone, WDA is the only real option. Item #1 above covers the full iOS tap story once it lands. |
| tcpdump integration into per-test teardown | The escape hatch command already exists (`yaver test debug --capture-packets`). Wiring it into per-test teardown is friction + root-prompt hell for zero real benefit — 99% of network bugs show up in the CDP network log we already capture. |
| Multi-tenant approval workflows | Explicitly rejected in a prior pass — solo dev = auto-fix log + Undo button, not pending approvals. Already implemented correctly in `testkit/autofix_log.go`. |
| Hosted "Yaver Cloud" runner | The entire wedge is "the dev's own machine." The Hetzner deploy script (`scripts/deploy-yaver-agent-hetzner.sh`) already covers the "always-on box I own" case for ~$5/mo to the dev, $0 to Yaver. |
| Replacing release CI (notarization, Play upload) | Runs rarely, cloud quota is free, trust model belongs on GH Actions. |
| Device cloud (3000+ phones) | Hardware game, not a software problem. Solo dev tests on their own devices. |
| Managed QA humans (QA Wolf model) | Services business, not a tool. |
| Team-wide test ownership / quarantine | Solo + pair max for now. Team CI is a separate roadmap. |
| Enterprise SSO / audit log | None of the solo-dev persona cares. |

---

## Ranked by what to ship next

### One-evening option
1. **#3 docs** (half day) — the dev needs to find all the features we've already built.
2. **#1 WDA install helper** (1 day) — kills the last iOS selector gap.

### One-week option
1. #3 docs
2. #1 WDA install helper
3. #2 mobile frame-sequence player
4. #4 local Safari smoke target (guarded by env var)

After that the feature matrix for the solo-RN-dev-with-own-hardware
persona is **fully green** against every paid SaaS row that isn't a
device cloud. The $0/mo target holds.

---

## Where to resume

Relevant files to re-read when picking this back up:

- `docs/roadmap_ci_solo_developer_lower_costs.md` — intention + constraints + milestone framing
- `docs/yaver_vs_saas_ci_comparison.md` — per-row competitor matrix (use this to verify nothing's slipped)
- `docs/autotest-spec.md` — the older autotest spec that seeded the early design
- `desktop/agent/testkit/` — the actual implementation
- `desktop/agent/test_cmd.go` — CLI surface (`yaver test init / run / record / history / flake / sync / schedule / debug`)
- `desktop/agent/testkit_http.go` + `testkit_mcp.go` — HTTP + MCP surfaces
- `mobile/app/(tabs)/runs.tsx` — mobile "Local CI" screen
- `mobile/src/lib/quic.ts` — `testkit*` client methods + TS types
- `scripts/run-gh-ci.sh` — dispatches CI workflows, dumps failures inline
- `scripts/deploy-yaver-agent-hetzner.sh` — own-server deploy

Most recent head that was fully green end-to-end: `b0cb38b1` —
"yaver-test-sdk: a11y audit, HAR export, step macros, tcpdump escape
hatch". Any later work should rebase on that or whatever `main` is
pointing at when you pick this back up.
