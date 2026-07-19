# Autorun task — remote-box-first, end to end, proven by an edtech app

Run this on the Mac mini, in your own dedicated clone, on your own branch.

Read `docs/handoff/remote-box-first-app-dev-2026-07-19.md` **in full first**.
It records what already landed and what was deliberately left. Do not revert
any of it, and do not delete the hidden legacy sandbox code — it is kept on
purpose. Per CLAUDE.md: when that doc and the code disagree, the doc is the
bug — fix it in the same change.

## The acceptance driver

The owner is about to build a real product on Yaver, and that product is the
test of whether "remote-box-first" actually works:

> An **education app that uses AI to instruct doctors preparing for TUS**
> (Turkish medical specialty exam) and **students preparing for LGS** (high
> school entrance exam). Built entirely through Yaver.

Concretely that means, on a remote box, from a phone or a voice surface:

- **Yaver Git** for the repo, **Yaver Serverless** for the backend, Yaver
  monorepo layout — the greenfield defaults, no external SaaS required.
- **STT and TTS on every surface that speaks**: backend, mobile, web. A user
  asks a question out loud and hears the answer. This is the app's primary
  interaction, not a convenience.
- **Feedback SDK** wired into the app from the first build, so the owner can
  report a bug from inside the running app and have it land as a task.
- **Hermes / WebRTC preview** as the iteration loop.

Do not build the edtech app itself. **Make Yaver able to build it without a
single manual step**, and prove it by walking the path.

## Non-negotiable constraints

1. **NEVER disrupt Tailscale on this Mac mini.** It is the only remote access
   path to this box. Nothing may remove, shadow or race a route in
   `100.64.0.0/10`. Do not run `yaver mesh up`, `POST /mesh/up`, or anything
   calling `ConfigureNetwork` here. Test mesh logic with unit tests only.
   Losing the box is far worse than an untested branch.
2. **`git commit -- <paths>` ALWAYS.** Never `-a`, never `add -A`. Many
   sessions share this machine; a bare commit sweeps a sibling's files.
3. **Sequential only.** Never two builds at once on this box — parallel Xcode
   archive + Gradle exhaust its RAM and SSD.
4. **Deploy at most once per target, at the very end**, via
   `scripts/mini-deploy.sh`. Never deploy to check something.
5. Do not touch `~/Workspace/yaver.io` (a sibling session's dirty tree) or
   `~/Workspace/yaver-deploy-runner` (the deploy clone).

## Phase 1 — walk the path before changing anything

Actually attempt, from this box, the flow a new user would take:

1. Create a task that starts a greenfield app on a remote box.
2. Let it pick Yaver Git + Yaver Serverless + monorepo defaults.
3. Get a preview onto a device via Hermes/WebRTC.
4. Trigger the Feedback SDK from inside the previewed app.

Write down **every point where it stopped, asked a question it should have
answered itself, or required a manual step**. That list is the real task; the
phases below are the expected shape of it, not a substitute for walking it.

## Phase 2 — STT/TTS parity on every surface

`mobile/src/lib/voice/` already holds a surface-agnostic `VoiceConversationCore`
plus a `useHandsFreeVoice` seam, and car is wired in `app/car-voice-coding.tsx`.

- Confirm — by reading the code, not the docs — which surfaces actually reach
  that core: phone, tablet, web, watch, Wear, tvOS, car, glass/AR-VR.
- RN surfaces share `DeviceContext`/`AuthContext`, so a fix there reaches
  phone/tablet/car/glass for free. **Verify it isn't gated to one screen.**
- Native surfaces (tvOS Swift, watchOS Swift, Wear Kotlin, web) inherit
  nothing — port explicitly and say so in the commit.
- Where a surface genuinely cannot do STT/TTS, say so in the handoff table.
  An honest "not supported here" beats a silent gap.

## Phase 3 — deploy stage + failure, visible on every surface

Today a deploy's progress and its FAILURE are near-invisible outside web and
mobile, and the vocabulary is fragmented — `DeployCapability.Target` (14
targets), `CapabilitySnapshot.Targets` (7 keys), `deployCapabilityRequirements`
tags and `publishCapabilities` (10 platform strings) do not map to each other.

- Reconcile those vocabularies into one list, or state plainly why they must
  stay separate.
- Every deploy target the product supports must be representable: convex,
  firebase, google cloud functions, cloudflare, vercel, netlify, supabase,
  testflight, play internal, npm, and the wear/tv/car/AR-VR channels.
- Show **stage** while running and **failure with its reason** when it fails,
  on web, mobile, tvOS, watch, Wear, car, glass. A failure that only the CLI
  can see does not exist for someone holding a phone.
- `GET /fleet/deploy-options` (`desktop/agent/fleet_deploy_options.go`) already
  proxies `/doctor/build` to every online peer and **has no caller in `web/` or
  `mobile/`**. Wire it rather than rebuilding it.
- `GET /autoruns/deploy-status` reports a lease board whose `--outcome`
  defaults to `success` (`autorun_store_cmd.go`). Do not present self-reported
  success as verified truth. Deploy capability on the device row
  (`deployCapabilities`, agent 1.99.320+) is probed and safe to trust; render
  its age.

## Phase 4 — close the follow-ups the handoff names

Work the "What Is Still Missing / Follow-Ups" list, in its order. Items 2
(hidden-route cleanup) and 6 (`phone_sandbox` legacy lane) are the ones most
likely to surface a legacy path to a real user.

## Gate

`cd desktop/agent && go build ./...` plus the scoped tests you touched, and
`cd web && npm run build` — **the real build, not `tsc --noEmit`**, which
false-greens (that is how `variant="info"` shipped a broken main on
2026-07-19). Never `go test ./...` unscoped: it hits the real `~/.yaver` and
signs this box out.

## Deploy

Only after everything converges, once, from the deploy clone:

```bash
cd ~/Workspace/yaver-deploy-runner && ./scripts/mini-deploy.sh
```

Cloudflare will report BLOCKED until a `CLOUDFLARE_API_TOKEN` exists on this
box — that is expected and is not yours to fix. Deploy the targets that are
green and say plainly which were skipped.

## Done means

A handoff doc at `docs/handoff/` that answers, per surface (web, mobile,
tablet, tvOS, watchOS, Wear, car, glass/AR-VR), three columns: **STT/TTS**,
**deploy stage**, **deploy failure** — each cell one of shipped / not
applicable / still missing, with a file:line for every "shipped". A cell you
did not verify is "still missing", not "shipped".
