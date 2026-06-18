# Yaver — Legal Safety Notes

> Engineer's summary, not legal advice. Before commercializing, get a one-hour
> consult with a FOSS-savvy lawyer (Heather Meeker's firm, or similar).

Yaver is dual-licensed: **FSL-1.1-Apache-2.0** (core, Functional Source License with 2-year Apache-2.0 auto-transition) + **Apache-2.0** (client SDKs from day one). We
depend on Convex, Supabase, Expo, Metro, React Native, and many others, and our
landing page references Claude, Codex, Aider, Ollama, Apple, Google, Microsoft,
Cloudflare, Hetzner, and more. None of those projects can sue us *just because*
we're AGPL — their licenses explicitly allow it. But there are real legal risks
that are easy to trip over. Read this before adding a dep, writing landing copy,
or shipping a paid tier.

---

## 1. License compatibility — safe, but verify every new dep

| Upstream | License | AGPL downstream OK? |
|---|---|---|
| Convex client SDKs | Apache-2.0 | Yes |
| Supabase client/server | Apache-2.0 / MIT | Yes |
| Expo / Metro / React Native | MIT / BSD | Yes |
| quic-go | MIT | Yes |
| Ollama | MIT | Yes |
| Aider | Apache-2.0 | Yes |
| Hermes | MIT | Yes |

**Rule**: before adding a dep, check its `LICENSE` file. Permissive (MIT /
Apache-2.0 / BSD / ISC) is always fine downstream of AGPL. Copyleft (GPL / AGPL /
MPL / LGPL) needs a lawyer look — they can impose constraints on *our* licensing.

**Never add** anything under a "source-available" / Business Source License (BSL) /
SSPL / Commons Clause / Elastic License — those aren't FOSS and will block
commercial use. Examples to avoid: MongoDB (SSPL), Redis ≥ 7.4 (dual SSPL/RSAL),
CockroachDB (BSL), HashiCorp Terraform ≥ 1.6 (BSL), Sentry (FSL), Grafana
Enterprise (AGPL + commercial addons).

---

## 2. Trademark — the single biggest real risk

Software licenses cover *code*. They do NOT grant trademark rights. "Convex",
"Supabase", "Expo", "React Native", "Claude", "OpenAI", "GitHub", "Cloudflare",
"Hetzner", "Next.js", "Vercel" are all trademarks of their respective owners. The
US and EU give trademark owners broad power to stop *any* use that suggests
endorsement or could confuse consumers — even if the software is open source.

### What's OK (nominative fair use)

Plain-text, factual statements that describe compatibility. Examples:

- "Yaver works with Supabase, Convex, Neon, and Turso"
- "Deploy to Cloudflare Workers or Vercel"
- "Runs any terminal AI agent (Claude Code, Aider, Codex, Ollama)"
- "Sign in with Apple, Google, or Microsoft"

### What's NOT OK

- **Logos on the landing page** without permission, *except* where a brand
  explicitly publishes a Sign-In button spec (Apple, Google, Microsoft all do —
  follow their button guidelines exactly, don't restyle).
- **"Powered by X"** phrasing that implies a commercial partnership.
- **Composite marks**: "YaverConvex", "Yaver for Supabase", "YaverGPT".
- **Font / color / layout that imitates the other brand's site**.
- **Paid search ads on their trademarks** ("supabase alternative" is fine as
  a general keyword; bidding on `[Supabase]` as an exact-match kw is risky).
- **Screenshots that prominently show another product's UI** as if it's ours.
- **Testimonials or logos** ("used by Stripe", "endorsed by Vercel") that aren't
  documented in writing.

### Specific brands with known-strict trademark enforcement

- **Apple** — extremely strict. Use "Apple Sign-In" only in their approved button
  form. Never say "iOS" as a standalone name of something — use "for iPhone".
  Never use the Apple logo except the approved Sign-In glyph.
- **Google** — strict on logo usage and the word "Android" (must be "for Android",
  not "Android app"). Follow their [brand permissions](https://about.google/brand-resource-center/).
- **Microsoft** — strict on "Windows", "Azure", "Office 365" naming.
- **OpenAI** — very strict on "GPT". Don't ship a product named "…GPT". The
  word "AI" is fine; the mark "GPT" is enforced.
- **Expo** — specifically asks that you NOT call anything "Expo Go alternative"
  or use the Expo name as part of a feature name.

### Rule

Text-only mentions for factual compatibility are fine. Logos, slogans, and brand
aesthetics require written permission from that company. If in doubt, cut it.

---

## 3. Landing page copy — claims that can be litigated

Anything on the landing page that describes a competitor or makes a factual
claim is a liability if it's wrong or misleading. US Lanham Act §43(a) and EU
Unfair Commercial Practices Directive both create causes of action for false
advertising — your competitor can sue you directly, not just regulators.

### Comparison claims — be surgical

OK:
- "Yaver is P2P — task data never touches our servers." (only if actually true,
  verifiable in the architecture)
- "Open source under AGPL-3.0." (fact)
- "Self-hostable relay." (fact)

Risky:
- "Faster than Expo EAS" — needs a reproducible benchmark on the same hardware.
- "More secure than TestFlight" — security claims invite lawsuits + FTC scrutiny.
- "The only way to …" — 99% of "only" claims are false and discoverable.
- "Unlimited X" — if there's any rate limit at all, this is false advertising.
- Direct comparison tables with competitors — every row must be defensible with a
  citation. A single wrong cell can trigger a C&D.

### Privacy / security claims — the landmine

> "Your data never touches our servers."
> "End-to-end encrypted."
> "We can't see your code."

These are **statements of material fact**. If the architecture contradicts them
(even accidentally — e.g., a stack trace goes to Sentry, a crash report to
Crashlytics, a DNS lookup happens on our infra), that's a consumer-protection
violation in every US state and most EU countries. The CLAUDE.md privacy
contract is good; make sure the landing page copy matches it exactly and no
tighter.

**Rule**: every privacy/security claim on the landing page must have a line in
this repo showing it's true (test, architecture doc, code comment). If someone
asks "prove it", we can link to the code.

### Performance / price claims

- Any benchmark number needs a date, hardware spec, and a reproducible script.
- Any price ("$10/mo") needs a visible footnote about what's included + any caps.
- "Save $X" claims need a documented comparison basis (competitor's current
  list price, same quantity).

### Testimonials

- Must be from real customers who consented in writing to use their name/quote.
- Cannot imply endorsement by a company if only an individual gave it
  ("John Smith, Engineer at Stripe" is fine; "Stripe uses Yaver" is defamation
  risk if untrue).
- FTC requires disclosure of any compensation ("John got a free year for this
  quote" must be disclosed).

---

## 4. Terms of Service — the second biggest real risk

If a user connects Yaver to a *hosted* service (Convex Cloud, Supabase Cloud,
Neon, Turso, TestFlight, Play Store, GitHub, Cloudflare, OpenAI API, Anthropic
API), our code acts on their behalf. The host's ToS applies to whatever we do
through their API. Many hosted ToS prohibit:

- **Reselling / wrapping** their API as part of *our* paid offering.
- **Competing** with them using data/insights from their service.
- **Bulk scraping / rate-limit evasion / sharing API keys across tenants**.
- **Multi-tenancy** on a single-tenant account.
- **Automated account creation** on their signup flow.

Known landmines:

- **Apple Developer Program License Agreement**: strictly prohibits anything
  that looks like an "app store within an app" or "runtime for unreviewed code".
  Our push-to-device container MUST be marketed as a developer-only testing
  tool, never "an app store". Apple has rejected and pulled apps for this
  exact thing (see: Scratch Jr, AltStore pre-EU).
- **Google Play Developer Distribution Agreement**: similar restriction on
  side-loading JS bundles, plus ad-tech restrictions.
- **OpenAI / Anthropic API ToS**: forbid reselling tokens and, in some tiers,
  using output to train competing models.
- **GitHub API ToS**: rate limits + forbids using scraped data to build
  competing products.

**Our current paid offering is Managed Relay at $10/mo on our own infra** — no
third-party ToS at risk. As soon as we sell *anyone else's* hosted service
(cloud dev machines, managed Convex, managed Postgres), the upstream ToS must
be legally reviewed before launch.

---

## 5. App Store specific — Apple + Google can delist without notice

The Yaver mobile app is in TestFlight and will go to production. Both stores
have aggressive delisting for:

- **Guideline 2.5.2 (Apple)** — "apps that install or launch executable code".
  Our Hermes bundle push is borderline. Position as: *Yaver is a development
  tool for testing apps you're building*, never a general-purpose app loader.
- **Guideline 4.7 (Apple)** — "HTML5 games, bots, mini-apps". Borderline again.
  Keep the framing as developer tooling, not consumer entertainment.
- **Guideline 5.1.1 (Apple)** — privacy policy required, must be accurate, must
  be linked in App Store Connect *and* inside the app.
- **Play Policy: Deceptive Behavior** — any claim in the store listing must
  match what the app actually does.
- **Play Policy: User-Generated Content** — if any Yaver feature lets users
  share content with each other (even just "share invite link"), we need
  UGC moderation language in the listing and policy.

**Rule**: before submitting a major Yaver app update, re-read the relevant App
Review guidelines section. The rules change every 3-6 months.

---

## 6. Patents — low probability, high impact

Apache-2.0 includes an explicit patent grant from contributors. MIT / BSD / ISC
do not. In theory an upstream MIT project's contributor could sue for patent
infringement. In practice this is vanishingly rare in active FOSS, but it's why
enterprise customers sometimes prefer Apache-2.0 dependencies.

**Rule**: prefer Apache-2.0 upstream when equivalent MIT/Apache options exist.
Don't lose sleep over it, but note it if an enterprise customer asks.

Separately: the US is still a first-to-file patent system. If Yaver has a
genuinely novel idea (our specific P2P + relay + push-to-device architecture
might qualify), consider filing a provisional patent *before* a competitor does.
Cost is ~$1.5K and it buys 12 months.

---

## 7. AGPL obligations flow *downstream*, not upstream

AGPL-3.0 means: **anyone who runs Yaver as a network service must offer source
to their users**. That constrains *our customers*, not us.

- Single user running `yaver serve` on their laptop → no obligation (they *are*
  the user).
- Company running Yaver as a SaaS for their internal developers → must make
  source available to those developers. Pointing them at our GitHub satisfies
  this.
- Someone forks Yaver and runs a competing SaaS → **must open-source their
  fork**. This is the strategic point of AGPL — prevents a cloud vendor from
  taking our code and selling it without contributing back.

**Client SDKs are Apache-2.0** (Feedback SDK, push-to-device CLI). Third-party
apps that embed them do NOT inherit AGPL. This is intentional — we want adoption
by app developers who can't adopt AGPL.

---

## 8. Attribution — generate `THIRD_PARTY_LICENSES.md`

Apache-2.0 deps require preserving `NOTICE` files. MIT/BSD require reproducing
the copyright notice. We should ship a generated `THIRD_PARTY_LICENSES.md` with
every release.

**TODO**: add a script that runs `go-licenses` + `license-checker` (npm) +
`pip-licenses` + CocoaPods license generator, commits the consolidated list, and
updates it per release. CI should fail if a new dep with an unapproved license
slips in.

```bash
# Rough sketch
go-licenses report ./... > licenses-go.txt
cd web && npx license-checker --production --csv > ../licenses-web.csv
cd mobile && npx license-checker --production --csv > ../licenses-mobile.csv
# Consolidate into THIRD_PARTY_LICENSES.md
```

---

## 9. Privacy policy + ToS — we need real ones

The moment we have any hosted infra touching a human (Managed Relay, analytics,
email, OAuth), US state privacy laws (CCPA/CPRA, Texas TDPSA, Colorado CPA) and
EU GDPR apply. Minimum requirements:

- **Privacy policy** linked from landing + inside mobile app.
- **Terms of Service** linked from landing.
- **Cookie banner** on the landing page if any analytics/tracking cookies set
  (required for EU visitors; also a CCPA notice for California).
- **Data subject access + deletion** endpoints (we have `/auth/delete-account`
  — good).
- **Data processing agreement (DPA)** template for any enterprise customer.
- **Subprocessor list** (Convex, Cloudflare, Resend, Apple/Google/Microsoft for
  OAuth, etc.) published and kept current.

---

## 10. Domain + name defensive moves

- **Trademark search**: `yaver` in USPTO + EUIPO + WIPO. Cost ~$350 + a few
  hours. Do before YC.
- **Defensive domains**: `yaver.com`, `yaver.app`, `yaver.dev`, `getyaver.com`
  — squat the obvious ones to block typosquatting / phishing.
- **GitHub org**: `kivanccakmak/yaver.io` is fine; consider also grabbing
  `@yaver` on npm and PyPI (we have npm already).
- **Social handles**: register `yaver` on X, LinkedIn, YouTube, Product Hunt,
  Reddit even if unused — prevents impersonation.

---

## 11. Pre-commercialization checklist

Before accepting the first paid customer (Managed Relay or anything else):

- [ ] One-hour consult with FOSS + SaaS lawyer
- [ ] Trademark search on "Yaver" (USPTO + EUIPO)
- [ ] `THIRD_PARTY_LICENSES.md` generated and shipped
- [ ] Privacy policy + ToS drafted and linked
- [ ] Cookie banner / CCPA notice on landing for EU + CA visitors
- [ ] Every privacy/security claim on landing has a code-level citation
- [ ] No logos on landing we don't have written permission for
- [ ] No comparison tables with unverified rows
- [ ] `delete-account` endpoint works and is tested
- [ ] Subprocessor list published at `/subprocessors`
- [ ] For every hosted service we touch (Apple, Google, GitHub, Cloudflare,
      Convex, Supabase, OpenAI, Anthropic), re-read their developer ToS
- [ ] Entity formation done (LLC or corp) — personal liability shield before
      taking money
- [ ] Business insurance quote (E&O + general liability, ~$1-2K/year for a
      dev-tool SaaS)

---

## 12. What's explicitly NOT a legal risk

- ❌ "Can Convex sue us because we integrate with them?" — No. Using their
     Apache-2.0 client SDK is the point of publishing it.
- ❌ "Can Expo sue us because we ship an RN container?" — No. MIT license + we
     don't call it "Expo Go".
- ❌ "Can they force us to change license?" — No. Permissive upstream licenses
     don't attach conditions to our license choice.
- ❌ "Will AGPL scare away customers?" — Individual devs, maybe; but our client
     SDKs are Apache-2.0 specifically so downstream apps aren't affected.
     Enterprises with AGPL allergies can buy a commercial license from us
     separately (standard dual-licensing play).
- ❌ "Can OpenAI/Anthropic sue us because we run agents?" — No, as long as we
     don't resell API access. Running the user's own agent with the user's own
     API key is a client-side action.

---

## 13. Red flags — escalate to a lawyer immediately

- A cease-and-desist letter from any company.
- A DMCA takedown on any GitHub file.
- An App Store or Play Store rejection citing a specific trademark or policy
  section.
- A customer emailing "we can't use Yaver because of [legal reason]" —
  understand the reason, don't just agree or disagree.
- Any invitation from an "enterprise procurement" team asking for SOC 2, ISO
  27001, or a signed DPA. These are expensive projects; scope before committing.
- Any revenue moment ($1K/mo, $10K/mo, first international customer) — each
  triggers new tax + compliance obligations.

---

**Not legal advice.** This is an engineer's safety net, not a substitute for a
lawyer. For anything that affects revenue, brand, or a shipped commercial
offering, get a lawyer.
