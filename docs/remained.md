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
<!-- #1 landed: `yaver install wda` in wda_cmd.go locates the WDA
xcodeproj inside a global `npm install -g appium-xcuitest-driver`,
runs xcodebuild build-for-testing against the booted simulator,
launches WebDriverAgentRunner in the background, and polls
http://127.0.0.1:8100/status until it answers. `yaver install list`
now shows a wda row with live status. -->

<!-- #2 landed: FrameSequencePlayer component in runs.tsx reads a
dir via the new /testkit/frames endpoint and scrubs through the
PNGs at the manifest's fps. Backend screencast wiring
(StartScreencast / FlushFrames) is still scaffolding — video.go
exists but isn't called from runner.go yet. -->

<!-- #3 landed: docs/yaver-test-sdk.md is the canonical page; linked
from README.md next to the auto-detect testing bullet. -->

<!-- #4 landed: testkit/driver_safari_smoke_test.go — guarded by
YAVER_SAFARI_SMOKE=1, runs against an httptest server, checks the
PNG signature on the screenshot. -->

<!-- #5 landed: step-level `- include: macros/x.test.yaml` expands in
place. Implemented via `Step.Include` + `expandStepIncludes` in
testkit/spec.go. Test: `TestStepIncludeExpandsInPlace`. -->


---

## Nice-to-haves — solo dev could live without

| # | Item | Why it's low priority |
|---|---|---|
<!-- #6 landed: Spec.NetworkProfile supports
online|fast-3g|slow-3g|2g|offline via chromedp
Network.EmulateNetworkConditionsByRule in runWebSpec. -->

<!-- #7 landed: runInteractiveFixClaude retries up to 3 times with
exponential backoff on errors that match a rate-limit / 429 / 503 /
timeout signature. -->

<!-- #8 landed: ZoomableImage component in runs.tsx implements
pinch-to-zoom + drag-to-pan directly with PanResponder + Animated,
no new deps. Used by both the failure screenshot modal and the
three-pane snapshot diff modal. -->

<!-- #9 landed: opt-in via `capture: {network_bodies: true}`. The
instrumentation queues request IDs during the run and
FinalizeInstrumentation drains them via Network.getResponseBody;
HARContent picks up Text + Encoding fields when bodies are captured. -->

<!-- #10 landed: `yaver test report [path] [-o out.html]` renders
the latest runs from .history.jsonl as a single-file HTML report
with no JS or external assets. -->

<!-- #11 landed: testkit_fixhandler.go registers a claude-backed
FixHandler in runTestSDK. runner.go now calls
AttemptAutonomousFix as a last-resort self-heal when
SelectorReplaceFromSelfHeal doesn't rescue the failing step. -->


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

### What's left after this session

- Nice-to-haves #6–10 in the table above (network throttling,
  retry-on-LLM-rate-limit, pinch-zoom, HAR response bodies,
  HTML report).
- #11 autonomous loop → testkit end-to-end integration. The
  `FixHandler` seam and `AutoFixLog` are still waiting for a
  proper handler — the loop-mode Auto Test path uses its own
  shortcut (synthetic `HeuristicReport` → `phaseThink`), not
  the `testkit.FixHandler` seam.
- Everything "explicitly kept out of scope" above stays out of
  scope.

After that the feature matrix for the solo-RN-dev-with-own-hardware
persona is **fully green** against every paid SaaS row that isn't a
device cloud. The $0/mo target holds.

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
