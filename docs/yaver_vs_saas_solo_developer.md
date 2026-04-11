# Yaver vs the Solo-Dev SaaS Stack — Broad Audit (2026)

A wider companion to
[`docs/yaver_vs_saas_ci_comparison.md`](./yaver_vs_saas_ci_comparison.md).
That doc focuses only on CI / test SaaS. This one asks the bigger
question: **across the whole paid-SaaS stack a solo developer
typically signs up for, what does Yaver already cover, what's
missing, and what should stay out of scope on purpose?**

Pricing is collected from 2026 vendor pages and third-party
aggregators (links at the bottom). Every dollar figure is
"typical solo-dev plan" — the cheapest tier that isn't a
hobby trap (5k events / month, 1 user, no retention).

> **Framing:** Yaver is a local-first developer runtime and
> remote-pair-programming tool. Some rows are out of scope
> because they're app-runtime concerns (the user's own users'
> auth, end-user push, database hosting). Those cells stay
> "out of scope" by design — Yaver is where the dev writes
> code, not a full backend-as-a-service.
>
> A few cells that initially look "app-runtime" are actually
> already half-built because Yaver's core architecture
> (self-hostable QUIC relay, authenticated on-device HTTP
> server, Hermes bytecode validation, bundle push pipeline)
> overlaps with what OTA / error ingest / remote-config
> vendors charge for. Those rows are called out as 🟡 rather
> than ❌.

---

## Legend

- **✅ have** — already shipped
- **🟡 partial** — some of it is shipped, rest is a known gap
- **⏳ roadmap** — not shipped but planned
- **❌ skip** — intentionally out of scope; rationale per row

---

## 1. Dev-runtime / inner loop

Tools the dev uses while writing, testing, and debugging code.
**This is Yaver's home territory.**

| Category | Typical SaaS (2026) | $/mo | Yaver | Notes |
|---|---|---:|:---:|---|
| **AI coding agent** | Cursor Pro, Claude Code Pro, Copilot Pro, Windsurf Pro | $10–20 | ✅ | Runs Claude Code / Codex / Aider / Ollama over the existing P2P transport; mobile Auto Dev tab can kick loops, set prompts, pick ideas. Dev's own API key / subscription. |
| **AI coding agent — autonomous loops** | Devin, Factory, Tabnine Agent | $20–500 | ✅ | `yaver loop` modes (`auto-fix`, `develop`, `auto-test`, `ideas`) run claude/codex/aider/ollama inside a per-loop git worktree with budget, session-limit, and release-train gates. |
| **Remote dev environment** | GitHub Codespaces, Coder, DevPod | $0.18+/hr | ✅ | `yaver serve` turns the dev's own laptop / Hetzner box / Mac mini into the remote env; mobile + web dashboards connect over P2P or relay. No metered compute. |
| **Dev server hot reload from phone** | Expo Go, Vercel dev tunnels | free / $20 | ✅ | `yaver dev start` proxies Expo / Flutter / Vite / Next.js through the P2P channel; phone banner auto-appears, hot reload via SSE. |
| **Push-to-device (Expo Go-like container)** | Expo Go (can't run your native modules) | free | ✅ | `yaver-cli push` compiles Hermes bytecode and POSTs to the on-device HTTP server. First-party TurboModules match; no cloud. |
| **Test runner (browser)** | Playwright Cloud, BrowserStack Automate | $30–129 | ✅ | yaver-test-sdk: chromedp-backed, spec-driven, network throttling, visual diff, HAR, axe, video, macro includes. |
| **Test runner (iOS sim)** | Waldo, Sauce RDC, Maestro Cloud | $50–500 | ✅ | testkit/driver_iossim.go + `yaver install wda` booting WebDriverAgent on the simulator for selector taps. |
| **Test runner (Android emu)** | BrowserStack App Automate, LambdaTest | $50–200 | ✅ | testkit/driver_androidemu.go + UIAutomator2 bridge; specs stay identical across targets. |
| **Visual regression** | Percy, Chromatic, Applitools | $149–300 | ✅ | Pure-Go perceptual diff with per-spec baselines in the repo. Three-pane pinch-zoom viewer on mobile. |
| **AI self-healing selectors** | Applitools, Reflect, Octomind | $200+ | ✅ | testkit `SelfHealSelector` + `testkit_fixhandler.go` registers a claude-backed FixHandler with retry-on-rate-limit. |
| **Failure screencast playback on mobile** | LambdaTest, Waldo | bundled | ✅ | `FrameSequencePlayer` scrubs through per-step PNG frames captured via CDP screencast. |
| **HTML test report export** | Playwright report, Allure | free OSS | ✅ | `yaver test report` renders `.history.jsonl` as a single-file HTML with no JS / no external assets. |
| **Voice-driven coding** | PersonaPlex, OpenAI Realtime | free / usage | ✅ | `yaver voice setup` — pluggable provider interface (PersonaPlex, OpenAI, Whisper). Always-on mic from mobile + Feedback SDK. |
| **Session transfer between machines** | tmate + screen-sharing hacks | free | ✅ | `yaver session transfer` for Claude / Codex / Aider / Goose / Amp / OpenCode. |
| **Network throttling for PWA work** | Chrome DevTools, Playwright `--slow-3g` | bundled | ✅ | `spec.network_profile` = online / fast-3g / slow-3g / 2g / offline via CDP. |

**Verdict:** Every row a solo dev would actually sign up for in
the dev-runtime category is covered. The stack the doc is
comparing against would cost ~$300–$600/mo if the dev bought
each category separately.

---

## 2. App-runtime / production observability

Tools that watch the dev's shipped app after it's in users'
hands. **This is where Yaver has real gaps.**

| Category | Typical SaaS (2026) | $/mo | Yaver | Gap |
|---|---|---:|:---:|---|
| **Error monitoring** | Sentry Team, Rollbar Startup, BetterStack, PostHog | $25–80 | 🟡 | Feedback SDK captures errors (`wrapErrorHandler`, `attachError`, BlackBox) and streams them to the agent over P2P. Ring buffer per device, SSE subscribe endpoint, `/blackbox/context` for fix prompts. **Missing:** multi-device aggregation, stack trace symbolication against uploaded source maps, release tagging, alerting rules. |
| **Crash reporting (mobile, symbolicated)** | Sentry, Crashlytics (free), Bugsnag | $0–50 | ❌ | Firebase Crashlytics is free and ecosystem-native. Don't reinvent. |
| **Session replay (pixel / DOM)** | FullStory, LogRocket, PostHog | $0–295 | 🟡 | BlackBox streams events (logs, navigation, lifecycle, network, render) but not pixels. A full pixel replayer is a big build — see decision matrix below. |
| **Product analytics** | PostHog, Mixpanel, Amplitude | $0–200 | ❌ | Yaver has internal `analytics.go` tracking *task runs*, not end-user events. Shipping a second event-tracking product is outside the dev-runtime mission. |
| **Feature flags** | LaunchDarkly ($10/seat), Statsig (free), ConfigCat ($110), Flagsmith OSS (free self-host), PostHog | $0–40+ | ❌ | Flag evaluation has to live in the user's app runtime, not the dev's agent. The OSS options (Flagsmith, Unleash) are already free and self-hostable. Yaver's `acl.go` is about agent-side tool ACLs, not app feature flags. |
| **A/B testing / experimentation** | Statsig, PostHog, GrowthBook | $0–150 | ❌ | Same reason as feature flags — app-runtime concern, and PostHog / Statsig already cover it for free. |
| **Uptime monitoring** | BetterStack, UptimeRobot, Pingdom | $0–20 | 🟡 | `healthmon.go` monitors the dev's own machines (for the Yaver agent daemon). **Missing:** monitoring the dev's shipped app endpoints. Could add a `yaver monitor add <url>` that runs a cron job through the existing scheduler. |
| **Log aggregation (prod)** | BetterStack Logs, Papertrail, Datadog | $20–80 | 🟡 | BlackBox streams logs from one device at a time. No cross-device search or alert rules. |
| **Push notifications (prod users)** | OneSignal, Expo Push, Firebase Cloud Messaging | $0–99 | ❌ | End-user push. Out of scope — Firebase + Expo Push already free. |
| **OTA updates (prod)** | Expo EAS Update ($99/mo 50k MAU), CodePush (dead), Stallion | $30–100 | 🟡 | **Most of the plumbing already exists.** `cli/src/bundler.js` + `cli/hermesc/` compile Hermes bytecode; `bundlecheck.go` validates the HBC version on ingest; the mobile app already runs a `YaverHTTPServer` on port 8347 and `YaverBundleValidator` guards bridge reloads; the P2P relay already forwards authenticated HTTPS to any `/releases/*` path we expose. **Missing:** a `yaver release publish` pipeline that stores versioned bundles on the agent, a `/releases/latest?channel=<lane>` pull endpoint, and a small end-user-side `expo-updates` shim that polls through the relay. See §6 build item R1. |
| **Transactional email** | Resend, Postmark, SendGrid | $15–50 | ❌ | Not dev-runtime. |

**Verdict:** Three real gaps worth considering (error dashboard,
session replay, uptime monitor). The rest is cleanly out of
scope — Yaver shouldn't become a vertical competitor to
PostHog / Firebase / Expo just because the dev uses both.

---

## 3. Ops / infra / deploy

| Category | Typical SaaS (2026) | $/mo | Yaver | Notes |
|---|---|---:|:---:|---|
| **Mobile CI builds** | EAS Build (usage), Codemagic, Bitrise | $30–300 | 🟡 | `yaver build ios/android` shells to `xcodebuild` / Gradle on the dev's machine; TestFlight + Play Store upload scripts in `scripts/deploy-*.sh`. Works for one dev on one Mac; doesn't scale to parallel builds or pay-as-you-go queues. Fine for the target persona. |
| **GH Actions overflow / fallback** | GitHub Actions minutes | free 2k | ✅ | `scripts/run-gh-ci.sh` triggers remote workflows and dumps failing logs inline. |
| **Cron / scheduled jobs** | Temporal, Inngest, Trigger.dev | $0–75 | ✅ | `/schedules` + `yaver.ScheduledTask`; used by Auto Dev loops and `yaver test schedule`. Local, no metered runs. |
| **Secret / credential vault** | Doppler, 1Password, AWS Secrets Manager | $0–20 | ✅ | Local P2P-synced vault (`vault.enc`); SDK tokens; mobile keychain sync on connect. |
| **Self-hostable relay / tunnel** | ngrok ($8), Tailscale (free), Cloudflare Tunnel (free) | $0–20 | ✅ | `relay/` Go QUIC relay + password auth; works alongside Tailscale or ngrok if the dev prefers. |
| **Multi-user team access** | GitHub Teams, Notion, shared Slack | $4–15/seat | ✅ | `--multi-user` mode + guest invitations + per-guest config (daily limits, allowed runners, project access). |
| **Deploy to Cloudflare / Vercel** | Vercel, Cloudflare Workers | $0–20 | ✅ | `scripts/deploy-vercel.sh` → `wrangler deploy` for the yaver.io landing page. Same path works for user apps. |
| **Database hosting** | Neon, Supabase, Turso, PlanetScale | $0–25 | ❌ | App-runtime concern. |
| **End-user auth / BaaS** | Clerk, Auth0, Supabase Auth, WorkOS | $25–500 | ❌ | End-user auth is the dev's app's concern; yaver's Convex auth is only for the dev themselves. |

---

## 4. Mobile-specific SaaS (React Native persona)

The roadmap persona is a solo React Native dev. This row is
where the dollar bleed is biggest.

| Category | SaaS | $/mo | Yaver | Notes |
|---|---|---:|:---:|---|
| **Hot reload + device container** | Expo Go (free, but can't use 3rd-party native modules) | free | ✅ | `yaver-cli push` runs the dev's existing RN project in the native yaver.io container with full TurboModule support. |
| **TestFlight upload automation** | Fastlane, EAS Submit | $0–30 | ✅ | `scripts/deploy-testflight.sh` does the whole archive + upload loop with one `.env`. |
| **Play Store upload automation** | Fastlane, EAS Submit | $0–30 | ✅ | `scripts/deploy-playstore.sh` + Python helper for edit transactions. |
| **Over-the-air JS updates (prod)** | EAS Update, Stallion, Appcircle | $30–100 | 🟡 | Yaver already ships Hermes compilation, BC validation, and the on-device HTTP server. One `yaver release publish` + a pull endpoint over the existing relay turns that into a self-hosted OTA lane. See §2 note and §6 R1. |
| **Real device testing (dev's own phone)** | Waldo, BrowserStack | $50–200 | ✅ | WDA install helper + `target: device` + yaver-test-sdk specs. |
| **Device farm (3000+ phones)** | BrowserStack, Sauce Labs, LambdaTest | $129+ | ❌ | Hardware problem. Solo dev owns 1–2 phones. |
| **Crash reporting (symbolicated)** | Sentry, Crashlytics | free–$26 | ❌ | Firebase Crashlytics is already free for mobile. |

---

## 5. Where the money actually goes (one solo dev, 2026)

A representative stack a solo React Native dev might pay for
today, with Yaver's current coverage:

| Tool | Category | $/mo | Yaver kills it? |
|---|---|---:|:---:|
| Cursor / Claude Code Pro | AI coding | $20 | ✅ (yaver runs either via dev's own key, mobile kick) |
| GitHub Copilot Pro (if also subscribed) | AI inline | $10 | ✅ (redundant; keep if the dev likes inline ghost text) |
| Sentry Team | Error tracking | $26 | 🟡 (BlackBox covers the capture side; no dashboard) |
| PostHog (free tier) | Analytics + replay | $0 | ❌ (and that's fine — free tier is enough for most solos) |
| Expo EAS Build | Mobile CI | $30 | 🟡 (replaceable by local Mac builds; some devs want cloud anyway) |
| Expo EAS Update | OTA | $99 | 🟡 (see R1 — Hermes + relay already shipped) |
| BrowserStack / Waldo | Real-device testing | $129+ | ✅ (yaver-test-sdk + own phone) |
| Percy / Chromatic | Visual diff | $149 | ✅ (bundled in testkit) |
| Vercel Pro | Hosting | $20 | — (orthogonal — deploy script uses it) |
| GitHub Teams | Code hosting | $4 | — (orthogonal) |
| **Total** |  | **~$487/mo** |  |

**Yaver replaces ~$300–$400/mo of this stack today.** The rest
is either intentionally-out-of-scope (hosting, OTA, code host,
free tiers of analytics) or the "dashboard for errors" gap
below.

---

## 6. Concrete build vs. skip decisions

For each real gap in section 2, the decision:

### Build

1. **E1 — Error dashboard on the mobile Auto Dev / Runs tab.**
   BlackBox already streams errors to the agent; what's missing:
   - Aggregation across multiple SDK sessions (today it's one
     ring per device),
   - A source-map upload endpoint + stack-trace symbolication,
     so native-looking stack traces resolve to TS line numbers,
   - Simple rules: "notify my phone if error rate > X/min",
   - A "Errors" sub-tab on the mobile app.

   **Why:** closes the $26/mo Sentry gap, keeps 100% of the
   capture on the dev's machine, and the wire already exists.
   Rough scope: two files (`errors.go` + `errors_http.go`) and
   one mobile tab.

2. **U1 — Uptime monitoring for the dev's own apps.** `yaver
   monitor add <url>` → cron through `/schedules` → record
   response / status / latency → mobile alert on three
   consecutive failures. Reuses the notifications channel.
   ~0.5 day.

3. **A1 — BlackBox `track()` ingest channel.** SDK calls
   `yaver.track("purchase_completed", { amount: 9.99 })`.
   Events land in the existing ring buffer + a new
   `/analytics/events` endpoint for CSV export or a webhook
   into PostHog. **Explicitly no dashboard** — dashboards are
   PostHog's job. ~0.5 day.

4. **R1 — Self-hosted OTA via the existing Hermes + P2P stack.**
   `yaver release publish --channel production` compiles a
   fresh Hermes bundle, stores it under
   `~/.yaver/releases/<channel>/<semver>.jsbundle` on the
   agent, and updates a tiny `/releases/latest?channel=<lane>`
   endpoint. The end-user-side piece is a small native module
   that:
   - On cold start, polls the dev's relay at `GET
     /d/<deviceId>/releases/latest?channel=...`,
   - Validates the returned `hbcVersion` against the embedded
     container manifest (reuses `YaverBundleValidator`),
   - Downloads the bundle into the same safe-reload slot the
     dev-push path already uses,
   - Falls back to the last known-good bundle on any error.

   Rollouts = percentage bucketing on deviceID (pure local
   math, no server), rollback = `yaver release rollback
   <channel> <semver>`. **This is the biggest single unique
   differentiator in the whole matrix** — nobody else ships
   "no vendor, no store, no server, just your own relay"
   OTA. Rough scope: 1 day for the publish side, 2 days for
   the end-user native module + expo-updates-shim.

### Skip

5. **Session replay pixel recording.** The right answer is
   "use PostHog free tier." Building a pixel recorder means
   maintaining a JS shim that touches every component tree, a
   storage backend, and a web viewer. Not worth it vs.
   PostHog's 5k-session free tier.

6. **Feature flags / A/B tests.** Flagsmith is OSS and
   self-hostable in one Docker command. Pointing the dev at
   Flagsmith is more honest than building yaver-flags.

7. **End-user push, transactional email, auth, database
   hosting.** All app-runtime, not dev-runtime. Yaver's thesis
   is "the dev's machine is the dashboard," not "Yaver is the
   backend-as-a-service."

---

## 7. Proposed roadmap additions (prioritized)

Ordered by $/mo killed per dev-day of effort.

1. **R1 — Self-hosted OTA over the existing Hermes + relay
   stack.** ~3 days. Kills $30–$100/mo of EAS Update **and**
   is the single biggest "no one else can ship this" story in
   the matrix because it depends on Yaver already having a
   self-hostable relay, on-device HTTP server, and BC-version
   validation.

2. **E1 — `/errors/*` endpoint + mobile Errors tab.** ~1 day.
   Kills $26/mo of Sentry. Reuses BlackBox.

3. **E2 — source-map upload + symbolication.** ~0.5 day.
   Turns native-looking traces into `src/foo.tsx:42` on the
   phone during commute.

4. **U1 — `yaver monitor add <url>`.** ~0.5 day. Kills
   $0–$20/mo of BetterStack / UptimeRobot.

5. **A1 — BlackBox `track()` ingest channel (no dashboard).**
   ~0.5 day. Adds product-event capture without trying to
   replace PostHog; bridges via CSV / webhook.

After those five items, the $487/mo reference stack drops to
**~$30/mo** (optional mobile cloud CI). Every other row is
either shipped, out-of-scope-by-design, or covered by a free
SaaS tier the dev was going to use anyway.

---

## Sources

- [Sentry Pricing 2026](https://sentry.io/pricing/) — free developer (5k events, 1 user), Team $26/mo, Business $80/mo
- [BetterStack Pricing](https://betterstack.com/pricing) — error tracking free 100k/mo, Team plans below Sentry at scale
- [Rollbar Pricing](https://rollbar.com/pricing) — free 5k events, Startup $25/mo
- [PostHog Analytics Alternatives](https://posthog.com/blog/best-session-replay-tools) — 1M analytics events + 5k replays free, usage-based above
- [LogRocket Pricing](https://logrocket.com/pricing) — Team $69/mo 10k sessions, Professional $295/mo
- [LaunchDarkly Alternatives](https://posthog.com/blog/best-launchdarkly-alternatives) — LD $10/seat, Statsig free to 2M events, ConfigCat $110, Flagsmith OSS free
- [Cursor vs Claude Code vs Copilot 2026](https://www.shareuhack.com/en/posts/cursor-vs-claude-code-vs-windsurf-2026) — $10–200/mo tiers across AI coding agents
- [Expo EAS Pricing](https://expo.dev/pricing) — EAS Build usage-based, EAS Update $99/mo 50k MAU
- [What to do without CodePush](https://expo.dev/blog/what-to-do-without-codepush) — CodePush sunset + EAS Update migration
- [Gitpod Alternatives 2026](https://www.morphllm.com/comparisons/gitpod-alternative) — Gitpod Cloud shut down Oct 2025; Codespaces / Coder / DevPod are the options left
- [GitHub Codespaces Pricing](https://github.com/pricing/calculator) — 120 free core-hours/mo on personal accounts
- [Complete Tech Stack for Solo SaaS Development 2026](https://solodevstack.com/blog/complete-tech-stack-saas-solo-2025) — reference stack and cost breakdown
- [Solo Founder Tech Stack: 30 Essential Tools 2026](https://www.opc.community/blog/solo-founder-tools-2026) — broader category catalogue
