# X/Y/Z Platform Monetization Roadmap

Status: product direction and staged roadmap, audited 2026-09-27. Runtime and
manifest truth remains in code (`development_plan.go`, `project_manifest.go`,
`ClientSessionSettings`) rather than this document.

## Thesis

The customer usually already owns X (phone, tablet, TV, headset, browser), a
computer, and a paid coding agent such as ChatGPT, Codex or Claude Code. Do not
charge them again for generic tokens. Monetize what remains painfully fragmented:

1. Private, dependable access from X to the user's own Y box.
2. A guided path from source code to a runnable, signed and publishable Z.
3. Only later, rentable Y capacity when the user lacks the required OS/toolchain.

The first product is **Private Relay**. The durable product is **the shortest
honest path from an idea to every store the user is actually eligible to ship
to**.

## Current product audit

- The public pricing code already has the right wedge: self-hosted runtime is
  free, Relay Pro is USD 9/month, users bring their own coding-agent accounts,
  and Relay Pro does not claim to include compute (`web/app/pricing/page.tsx`).
- Subscription/webhook/provisioning machinery exists, but the last recorded
  production readiness audit found Relay Pro checkout configuration blocked.
  That is historical evidence, not current truth; the checkout must be probed
  again before any launch claim (`docs/relay-pro-100-user-readiness-2026-08-10.md`).
- Cloud Workspace subscription and placement plumbing exists, while the public
  release intentionally does not sell hosted compute. Keep it latent/previewed
  until Relay economics and target demand are measured.
- X/Y/Z build/run/render planning now exists in the agent and manifests. The
  largest remaining product gap is a structured account/sign/listing/submit
  state machine plus its Launch Map consumer on every surface.

## The product in one sentence

“Build from anywhere, on the right machine, for the platform you chose—and
always see the one next thing preventing launch.”

Yaver must never market “publish to Xbox/PlayStation” when it merely has a
Windows box. Console partner approval, restricted SDK access, certification and
approved hardware remain platform-holder gates.

## Customer journey

Treat readiness as a ladder, not one green badge:

1. **Idea ready** — project exists and target Z is selected.
2. **Control ready** — X can chat/control through a private route.
3. **Code ready** — coding runner is authenticated; repository is writable.
4. **Build ready** — a Y box proves the right OS and toolchain operation.
5. **Run ready** — simulator, emulator, browser or approved real hardware works.
6. **Render ready** — X and Z negotiate WebRTC/frames/iframe/Hermes plus codec.
7. **Account ready** — the user's platform account, contracts and identity are complete.
8. **Sign ready** — user-owned signing identity is available on the authorized Y box.
9. **Listing ready** — privacy, ratings, screenshots, metadata and policies pass preflight.
10. **Submit ready** — artifact and store record pass a dry-run validation.
11. **Submitted/review/released** — explicit human action; never implied by a build.

The default screen shows only the current milestone and one primary action. A
detail sheet may show the full ladder. Advisory diagnostics never push the next
action below the fold.

## What Yaver can guide, and what it must not impersonate

| Target | Yaver can automate | Human/platform gate |
|---|---|---|
| Web/PWA | build, preview, domain/TLS checks, deploy preflight | registrar/payment consent |
| Google Play | account checklist, identity state, signing, bundle validation, listing preflight | account creation, agreement, payment, final submission |
| Microsoft Store | Partner Center checklist, package/signing tests, listing preflight | account verification, agreements, final submission |
| Apple platforms | enrollment checklist, Xcode/signing probes, TestFlight/App Store preflight | Apple enrollment, 2FA, agreements, final submission |
| Xbox | public readiness guide, application checklist, approved GDK/devkit detection, build/capture after access | NDA, concept/program approval, restricted resources, certification |
| PlayStation | partner application checklist, approved SDK/devkit detection, build/capture after access | partner approval, agreements, restricted resources, certification |

Current official gates support this ordering:

- Apple enrollment is identity-verified; organization enrollment requires
  organization evidence such as a D-U-N-S number, and the Apple Developer
  Program is currently USD 99/year:
  https://developer.apple.com/programs/enroll/
- Google Play currently charges a USD 25 one-time registration fee and requires
  account verification; organization accounts require a D-U-N-S number:
  https://support.google.com/googleplay/android-developer/answer/6112435 and
  https://support.google.com/googleplay/android-developer/answer/13634885
- Microsoft Store enrollment starts in Partner Center and distinguishes
  individual from company accounts:
  https://developer.microsoft.com/en-us/microsoft-store/register
- ID@Xbox requires a developer relationship/NDA and approval before restricted
  SDK/devkit access; Microsoft describes SDK access and dev kits as following
  title approval:
  https://developer.microsoft.com/en-us/games/ and
  https://developer.microsoft.com/en-us/games/articles/2025/04/helping-indie-developers-succeed-on-xbox/
- PlayStation directs developers and publishers through PlayStation Partners:
  https://partners.playstation.net/

Yaver opens the official enrollment page in its authenticated Chromium lane,
explains each field, preserves progress, and resumes after the user completes
login, payment, 2FA, identity checks or agreements. It never asks for or stores
the user's platform password. API keys/certificates are connected only through
the vault after the account exists.

## Monetization ladder

### 0. Free: prove the loop

- Local/LAN/self-hosted development on the user's own machines.
- Project/target manifest, readiness plan and store-account checklist.
- Bring-your-own coding agent and bring-your-own machines.
- Enough preview time to experience the product, not enough hosted relay usage
  to create an uncapped bandwidth liability.

Goal: activation, trust and a truthful readiness report.

### 1. Private Relay: first paid product

- Private reachability when LAN/Tailscale/direct paths are unavailable.
- Multiple client surfaces, wake/recovery, connection history and route health.
- Direct WebRTC where possible; relay signaling/control and TURN/media fallback
  only when required.
- One owner security model across every box; no cross-tenant reachability.

The current public product contract is **Relay Pro at USD 9/month**. Keep that
simple Solo anchor while testing a later Studio tier for team roles, audit
history and higher fair-use limits.
- Meter exceptional TURN/video egress or reduce quality before allowing relay
  media to destroy gross margin. Never meter chat/control bytes theatrically.

The paid event should be “I need my box while away from home,” not “I clicked
build.”

### 2. Launch Guide: sell completion, not documentation

- A target-specific interactive checklist with verified state.
- Official account enrollment handoff/resume.
- Signing and certificate doctor.
- Store metadata, screenshot and policy linting.
- Certification/preflight packs and a final human submission gate.

Pricing hypotheses to test:

- Included readiness/checklists in Relay to strengthen retention.
- A **Launch Pass** per product/target (roughly USD/EUR 49–149) for intensive
  preflight, artifact history and submission assistance.
- Studio subscription includes a number of active release tracks.

Do not take a margin on platform enrollment fees. Show them as third-party costs
paid directly to the platform holder. Trust is worth more than a small markup.

### 3. Bring-your-own Y orchestration

- Route Android/Linux work to an existing PC/server.
- Route Apple work to the user's Mac.
- Route Windows Store work to the user's Windows machine.
- Detect SDK/account/hardware gaps and offer deterministic installation where
  licensing permits.

This is mostly software margin and should precede owned cloud compute. It also
produces demand data: which OS, target and duration users genuinely need.

### 4. Yaver Cloud Builders

Cloud Workspace plumbing already exists behind guarded subscription/preview
paths. Keep managed compute out of the first-product message, and broaden it
only after Relay and Launch Guide establish repeat demand:

- Ephemeral Linux/Windows builders, then carefully scoped Mac capacity.
- Per-minute or prepaid build credits with spending caps and sleep-by-default.
- Source encrypted in transit; isolated tenants; short-lived credentials;
  artifacts and logs have explicit retention controls.
- Cloud box is a Y candidate in the same planner, not a separate product flow.

Suggested model: subscription includes orchestration and a small credit; compute
is usage-based with a transparent margin. Do not promise flat “unlimited Mac” or
video streaming before real utilization and licensing economics are measured.

Console cloud capacity is not part of the generic builder pool. Devkits and
restricted SDKs require platform-holder authorization and a compliant program.
Until Yaver has those agreements, the supported model is customer-owned,
approved hardware connected as Y.

## UX: Launch Map

Each project gets one compact card:

```
SFMG → Xbox
Ready now: code on Windows Box · preview on Apple TV
Next: Apply to ID@Xbox
[Continue application]
```

After approval:

```
SFMG → Xbox
Build box ready · GDK connected · devkit not detected
[Connect devkit]
```

The same state is rendered on phone, browser, TV, watch, XR and CLI. Watches and
cars are control/status surfaces; they do not pretend to be full editors. A TV
or headset can be a first-class WebRTC render surface. Every state change comes
from the agent contract, never from surface-specific prose matching.

The manifest declares durable intent; it must not contain credentials:

```yaml
development:
  targets:
    xbox:
      platform: xbox
    ios:
      platform: ios
```

The next contract extension should add a code-owned distribution catalog and a
project selection of release channels. Account and signing readiness are runtime
evidence, not booleans committed to YAML.

## Roadmap

### Phase A — now: truthful X/Y/Z foundation

- Ship target declarations for Yaver, SFMG and Talos.
- Ship `development_plan` over HTTP/MCP and client capability declarations.
- Keep console capabilities fail-closed.
- Complete parity tests for every client implementation.
- Make existing `develop_for(machine=...)` execute the entire loop on Y.

Exit: no combination of X/Y/Z silently falls back to localhost or spins forever.

### Phase B — Private Relay GA

- Package plans, quotas, route health, direct-vs-relayed evidence and billing.
- Measure signaling bytes, control bytes, TURN/video egress, session duration and
  recovery rate separately.
- Make relay failure carry an in-place repair action on every surface.

Exit: paid users can reliably reach their own machines away from home, with
known unit economics and no weaker authorization path.

### Phase C — Platform Readiness + Launch Map

- Add account/program/signing/listing/submission readiness as separate states.
- Build guided enrollment handoffs for Google Play, Microsoft Store and Apple
  first; Xbox and PlayStation application guidance remains public/readiness-only
  until approval.
- Add deterministic certificate, bundle/package and listing doctors.
- Add artifact provenance and human-confirmed submit gates.

Exit: a first-time user always knows the next legal/technical step and never has
to translate a store rejection into a terminal command.

### Phase D — BYO multi-box automation

- One-click toolchain install/probe where redistribution/licensing allows it.
- Build queues, caches, artifact transfer and remote signing without exporting
  private keys.
- Real-device farms are customer-owned first.

Exit: Android, Apple and Windows targets can move among eligible user boxes
without changing the client experience.

### Phase E — Cloud builders

- Start with Linux/Windows demand demonstrated by Phase D telemetry.
- Add Mac only after legal/licensing, capacity, queue and gross-margin tests.
- Add regionality, budgets, retention and team controls.

Exit: users lacking Y can buy a temporary eligible Y without learning cloud infrastructure.

### Phase F — approved console programs

- Pursue middleware/tool-provider relationships with Microsoft and Sony.
- Support restricted adapters in private packages/environments, never in the
  public repository.
- Offer console build/test capacity only within platform-holder terms.

Exit: Yaver may truthfully claim an approved console workflow; before that it
claims guidance plus customer-owned approved hardware only.

## Metrics that matter

- Time from target selection to first successful render.
- Time from “account missing” to verified account connection.
- Percentage of blockers resolved from the offered in-place action.
- Build/run/render success by X/Y/Z tuple.
- Relay paid conversion after an off-LAN need, not after arbitrary UI exposure.
- Relay gross margin split by control versus TURN/video traffic.
- Time from first artifact to store-ready and store acceptance.
- Cloud-builder attach rate by OS/target and contribution margin per build hour.
- Support minutes per launched target.

Avoid vanity counts such as declared targets, paired boxes or generated code.
The commercial outcome is a user who reaches a real platform milestone with
less uncertainty.

## Product boundaries

- Do not resell or silently consume the user's ChatGPT/Claude subscription.
- Do not automate acceptance of legal terms, identity verification, payment,
  2FA or final publication.
- Do not upload signing keys or restricted SDKs to a generic shared builder.
- Do not call relay-terminated HTTP “end-to-end encrypted.”
- Do not claim a simulator is real hardware or that a PC toolchain proves
  console eligibility.
- Do not make cloud compute the default when the user's own eligible box works.
