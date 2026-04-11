# Yaver vs Paid CI/Test SaaS — Feature Matrix for the Solo Developer

A side-by-side of what the major paid CI/test SaaS vendors charge for
versus what's already in the Yaver agent today, what's queued in the
roadmap, and what's intentionally out of scope. The comparison
deliberately favors a single solo / pair-of-developers workflow on
their own hardware — that's the persona we're optimizing for.

> Yaver's architecture choices that drive every "yes" cell:
>
> - **Local-first.** Specs, baselines, history all live on disk in
>   the user's repo (`yaver-tests/`). The agent is a single Go binary.
> - **P2P.** The mobile app talks to the agent over the same authed
>   QUIC/HTTP transport that the rest of Yaver uses; the dev's phone
>   is the dashboard. No SaaS web UI to host.
> - **No central server.** Convex is only used by Yaver itself for
>   sign-in / device discovery; the test runner never touches it.
> - **The dev's own hardware is the runner.** No metered cloud minutes,
>   no rented device cloud, no per-snapshot fee.

---

## Feature matrix

Legend: **✅ Yaver today** · **⏳ Yaver roadmap** · **❌ out of scope**.

| Capability | BrowserStack | Sauce Labs | LambdaTest | Chromatic / Percy | Applitools | Waldo | Maestro Cloud | QA Wolf | Reflect / Octomind | **Yaver** | Yaver state |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Run real browser tests (Chrome) | ✅ | ✅ | ✅ | ✅ | ✅ | — | — | ✅ | ✅ | ✅ | **today** — embedded chromedp, no Selenium/Playwright install |
| Run on Firefox / WebKit | ✅ | ✅ | ✅ | ✅ | ✅ | — | — | ✅ | ✅ | ⏳ | M5 — same chromedp/CDP shape, just spawn a different binary |
| Run on iOS Simulator | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ | ✅ | ✅ | ⏳ | M5 — `simctl` lifecycle wrapper already in `testkit/driver_iossim.go`, missing Appium WD bridge |
| Run on Android Emulator | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ | ✅ | ✅ | ⏳ | M5 — `emulator`+`adb` wrapper in `testkit/driver_androidemu.go`, missing UIAutomator2 bridge |
| Run on real iOS device | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ | ✅ | — | ⏳ | M5 — WebDriverAgent + `idevice*`; the dev's own iPhone over USB |
| Run on real Android device | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ | ✅ | — | ⏳ | M5 — UIAutomator2 + `adb` over USB |
| Massive cloud device farm (3000+ phones) | ✅ | ✅ | ✅ | — | — | — | — | — | — | ❌ | **out of scope** — the whole point is the dev's *own* devices |
| Parallel test sharding | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **today** — `--concurrency N`, fan-out across worker goroutines |
| Visual regression / pixel diff | ✅ Percy | Sauce Visual | ✅ | ✅ | ✅ Visual AI | — | — | ✅ | ✅ | ✅ | **today** — pure-Go perceptual diff, baselines in `<spec dir>/snapshots/` |
| AI-powered visual inspection ("is this UI broken?") | — | — | — | — | ✅ | — | — | ✅ | ✅ | ✅ | **today** — `inspect:` step uses dev's own Mistral/OpenAI/Anthropic/Ollama key |
| AI self-healing selectors | — | — | — | — | ✅ | — | — | — | ✅ | ⏳ | M6 — call MCP with the failing selector + DOM snapshot |
| Flake detection / retry tagging | ✅ | ✅ | ✅ | — | — | — | — | ✅ | ✅ | ✅ | **today** — `--retries N` + `Flaky` flag in result + `flake` history report |
| Test history / build analytics | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **today** — `<spec dir>/.history.jsonl`, `yaver test history`, mobile "Runs" tab |
| Per-test screenshots / videos | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **today** — screenshots on failure (configurable always/failure/never); video M6 |
| Trace / time-travel debugger | ✅ | ✅ | ✅ | — | — | — | — | — | ✅ | ⏳ | M6 — chromedp can produce CDP traces; needs viewer in mobile app |
| Recorder ("record once, replay anywhere") | — | — | ✅ | — | — | ✅ | — | — | ✅ | ⏳ | M5 — capture chromedp/CDP events, emit YAML |
| Test definition format | proprietary | proprietary | proprietary | Storybook | proprietary | YAML | YAML | proprietary | no-code | ✅ YAML | **today** — `*.test.yaml` is plain data, AI-friendly, diff-friendly in PRs |
| Build agent runs on user's own machine | — | — | — | — | — | — | — | — | — | ✅ | **today** — entire point of Yaver |
| No proprietary cloud account required | partial | partial | partial | partial | partial | — | partial | — | — | ✅ | **today** — sign in with Apple/Google/Microsoft only for peer discovery |
| Test artifacts stored locally | — | — | — | — | — | — | — | — | — | ✅ | **today** — files in repo, never on someone else's bucket |
| Test artifacts visible from phone over P2P | — | — | — | — | — | — | — | — | — | ✅ | **today** — mobile "Runs" tab pulls artifacts via existing transport |
| Sync results to GitHub Actions on demand | partial | partial | partial | ✅ | partial | partial | ✅ | ✅ | ✅ | ⏳ | M3 — `--junit` already lands the file; needs an `upload-artifact` helper |
| Cron / nightly scheduled runs | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⏳ | M3 — agent has cron infra (`/schedules`), needs a `yaver test schedule` subcommand |
| Hardware-aware (skip on battery / busy CPU) | — | — | — | — | — | — | — | — | — | ✅ | **today** — `--ac-power-only` + `--max-load`, reads `pmset` (macOS) and `/sys/class/power_supply` (linux) |
| Notify on failure only | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⏳ | M3 — push notification via existing mobile push channel |
| Headful mode for debugging | partial | partial | partial | — | — | — | — | partial | partial | ✅ | **today** — `--headful` flag, browser opens visibly |
| Watch mode (re-run on file save) | — | — | — | — | — | — | — | — | — | ✅ | **today** — `--watch`, 500ms poll, vibe-coding loop |
| Managed-QA humans writing your tests | — | — | — | — | — | — | — | ✅ | partial | ❌ | **out of scope** — that's a services business, not a tool |
| Crowd-testing marketplace | — | — | — | — | — | — | — | partial | — | ❌ | **out of scope** — see TestProject / Rainforest, both died |
| Sub-team / multi-tenant SaaS | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | **out of scope today** — solo + pair only; team CI is a separate later roadmap |
| Apple notarization / Play upload | — | — | — | — | — | — | — | — | — | ❌ | **out of scope** — keep on GH Actions, runs on tag push, free quota covers it |

---

## Where Yaver wins (already)

These cells are where the architecture makes Yaver a strict
improvement over every paid SaaS in the table — because they only
become possible when the runner lives on the dev's own machine.

1. **Privacy.** Screenshots, traces, baselines, history, and the
   dev's own AI provider keys never leave the laptop. Every paid
   competitor in the table sends at minimum a screenshot to a
   third-party bucket; most send the whole DOM. For solo devs
   working on pre-launch products under NDA, "my screenshots
   never leave my hardware" is a real differentiator.

2. **Cost.** $0/mo for the dev. The only metered service in the
   workflow is the LLM provider, and that's the dev's choice and
   their own key — Mistral, Anthropic, OpenAI, or local Ollama
   for free. A typical solo dev paying for BrowserStack ($129/mo)
   + Percy ($199/mo) saves ~$3,900/yr.

3. **Speed.** No queue. No artifact upload + download. No round
   trip through someone else's CI cluster. A 30-second test suite
   actually takes 30 seconds, not "30 seconds + 4 minutes of queue
   + boot + cache restore."

4. **AI loop.** The same MCP server that already drives Claude
   Code in `yaver serve` exposes the test runner. The dev can
   paste a failing run into Claude / Cursor / Aider and get a
   patch in the same conversation, then re-run via the same
   process — no third-party CI integration to wire up.

5. **Hardware-aware.** Defers runs while on battery; skips when
   the laptop is already pegged. None of the paid SaaS even has
   this concept because the runner isn't on your machine.

6. **One vocabulary.** A `*.test.yaml` describes a flow against
   web, ios-sim, android-emu, or a real device using the same
   `goto / click / fill / wait_for / assert.*` actions. No need
   to learn Playwright, Appium, XCUITest, and Espresso to cover
   all targets.

---

## What's missing (and the explicit plan)

The table above has one cluster of ⏳ rows that all flow from M5
("real-device drivers") and M6 ("AI integration + recorder").
Those are the next two milestones in
`docs/roadmap_ci_solo_developer_lower_costs.md`. After M6, the
only ❌ rows left in the matrix are the ones we deliberately
won't ship: cloud device farms, managed-QA services, and
multi-tenant team SaaS.

### Concrete deltas vs each competitor

- **vs BrowserStack / Sauce Labs / LambdaTest** — we'll never
  match their device cloud headcount, but the dev only needs *their
  own* iPhone and one Android emulator. We close every other gap
  in M5/M6.
- **vs Chromatic / Percy** — visual diffing is already in. The
  thing we still owe is a "review baselines from your phone" UI
  with PR-comment integration; M6.
- **vs Applitools** — we already have the LLM-driven inspection
  primitive; the missing piece is "self-healing selectors,"
  which is one MCP tool away (M6).
- **vs Waldo / Reflect / Octomind** — we owe the recorder
  (`yaver test --record`) for users who don't want to write
  YAML by hand; M5.
- **vs Maestro Cloud** — Maestro's OSS CLI is the closest
  spiritual sibling to yaver-test-sdk. The thing the user pays
  Maestro Cloud for is "execute on real iOS devices" which we
  cover with the dev's own hardware in M5.
- **vs QA Wolf** — explicitly out of scope. Yaver is a tool;
  QA Wolf sells humans.

---

## Cost comparison (one solo developer, ~50 commits/week)

| Stack | $/mo | Cancelled by |
|---|---|---|
| BrowserStack App Automate + Percy | ~$330 | M5 (mobile drivers) + already-shipped visual diff |
| Sauce Labs RDC + Sauce Visual | ~$200+ | same |
| Chromatic + GitHub Actions overage | ~$220 | already-shipped visual diff + JUnit emitter |
| Applitools Eyes + GitHub Actions | ~$300+ | already-shipped LLM inspection step |
| Waldo / Reflect (no-code) | ~$225 | M5 recorder mode |
| Maestro Cloud (1 iOS + 1 Android) | $500 | M5 mobile drivers |
| QA Wolf (managed) | ~$4,000 | not addressed; out of scope |
| **Yaver local CI** | **$0** | the laptop the dev already owns |

Even if the dev keeps a $5/mo Hetzner box as their relay so the
phone can reach the laptop from anywhere, that's $60/yr against
the ~$3,960/yr they were paying for BrowserStack + Percy alone.

---

## What this means for the roadmap

The table makes it obvious where the next slice of work pays back
the most:

1. **M5 — mobile drivers.** Single biggest unblock; closes every
   "Run on iOS / Android" cell in one shot. The two driver files
   already exist (`testkit/driver_iossim.go`,
   `testkit/driver_androidemu.go`), missing piece is the
   WebDriverAgent / UIAutomator2 bridge so existing Appium specs
   work unchanged.
2. **M6 — recorder + self-healing selectors + cloud sync.** Each
   one closes a feature row a paid SaaS sells today.
3. **Defer team / multi-tenant CI** until at least M7. Solo +
   pair is the focus.

The non-goal stays: **don't compete on device cloud headcount.**
That's a hardware game we can't win and don't need to. The dev
only ever wants to test on devices they actually ship for, and
they own at least one.
