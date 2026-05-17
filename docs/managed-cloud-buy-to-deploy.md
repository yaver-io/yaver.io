# Managed Cloud — Buy → Git → Code → Deploy (audit + plan)

> **Status: design doc, NOT yet implemented (2026-05-17). Code is the
> source of truth — re-grep; this drifts.** Goal: from web UI, mobile
> app, AND MCP, a user buys a Yaver managed Hetzner box (LemonSqueezy),
> connects GitHub/GitLab, then gets WebRTC remote-runtime / Hermes
> hot-reload / WebView preview, and deploys. Builds on
> `docs/managed-cloud-host-lifecycle.md` (gate, owner bypass, tag,
> recycle) and `docs/physical-device-remote-runtime.md`.

## 0. What to sell (platform reality — decided)

The dev loop a box can offer is bounded by hard platform facts found
this session:

- **React Native / Hermes** renders on the **user's own phone** (Yaver
  container); the box only Metro-builds + serves the Hermes bundle.
  Works on anything — incl. the default KVM box.
- **Web** (Next / Vite / **Flutter-web**) = dev server + WebView
  preview — works on anything.
- **Flutter / Kotlin native** WebRTC remote-runtime needs a real
  **Android emulator** ⇒ **KVM** ⇒ Hetzner **Robot bare-metal x86**
  (Hetzner **Cloud** can't: no linux-arm64 emulator binary + no
  `/dev/kvm` on any Cloud plan, TCG unusable). The decision below
  makes this KVM box the **single default** because it is a strict
  superset (also does RN/Hermes + web + deploy) — one SKU, not a
  premium add-on.
- **iOS** (Simulator or `ios-device`/WDA) is **macOS-only**. UIKit/
  SwiftUI/iOS-Simulator do not exist on Linux; Swift-on-Linux is
  server-side only; GTK/Darling cannot host an iOS app. ⇒ **Mac SKU
  (Mac mini / Mac cloud), later — NOT Hetzner.** Do not build toward
  iOS-on-Linux; it is impossible.

**SKU plan (decided 2026-05-17): single default = emulator-capable
KVM bare-metal.** A KVM box is a **superset** — it runs the Android
emulator (Flutter/Kotlin WebRTC) **and** Metro/Hermes (RN) **and**
web **and** deploy. One SKU, no tier confusion, covers every
non-iOS case. GPU tier unchanged. iOS = Mac SKU, deferred.

**Honest implementation cost of that decision (Phase D0, blocks D1):**
`cloudMachines.provision` today calls the **Hetzner Cloud API**
(`api.hetzner.cloud/v1`, cx/cax types) — Hetzner Cloud exposes
**no `/dev/kvm` on any plan** (shared or dedicated vCPU). KVM ⇒
Hetzner **Robot / dedicated bare-metal**, which is a **different API**
(hrobot / Robot webservice), **not instant** to provision, and
**~€30–40+/mo vs €5–15** Cloud. So "make the emulator box the
default" is a new provisioner workstream (Robot bring-up + slower
ready-poll + price), not a config flip. The cheaper "RN/Hermes-only
Cloud box" remains a possible *downsell* later, but the default is
the KVM box per owner decision.

## 1. Audit — backend is wired, the front door is missing

Pipeline is architecturally sound and the seams are clean:
LemonSqueezy webhook → `subscriptions.upsertFromWebhook` →
`scheduler.runAfter(0, cloudMachines.provision)` → Hetzner + cloud-init
(installs git/node/go/rust/docker/expo + yaver agent) → agent
heartbeat → box reachable → ops verbs for the dev loop → ops deploy.

Stage × surface (EXISTS / PARTIAL / STUB / MISSING):

| Stage | Web | Mobile | MCP/Ops |
|---|---|---|---|
| Purchase (LemonSqueezy) | MISSING | MISSING | MISSING |
| Provision + "ready" status | PARTIAL (ManagedCloudPanel, owner-only, no poll) | MISSING | EXISTS (`ops provision`) |
| Git connect (GH/GitLab) | PARTIAL (GitView) | STUB (`gitproviders.tsx`) | EXISTS (`git_push_creds`, `git_oauth_*`) |
| WebRTC remote-runtime | STUB | EXISTS (`remote-runtime.tsx`) | PARTIAL (HTTP routes, no MCP tool) |
| Hermes hot-reload | PARTIAL (`WebviewView`) | EXISTS (`hotreload.tsx`) | EXISTS (`ops reload`) |
| WebView preview | PARTIAL | PARTIAL | EXISTS (`ops web-preview`) |
| Deploy | EXISTS (`DeployCapabilitiesView`) | PARTIAL | EXISTS (`code_deploy`, `ops deploy`) |

Key backend anchors: `backend/convex/http.ts:3071` checkout,
`:2907` LS webhook; `cloudMachines.ts` provision + cloud-init;
`subscriptions.ts` gate + owner bypass (`canProvisionManaged`);
`ops_cloud.go` provision/scale/destroy/recycle; `ops_deploy.go`;
`git_push_creds_cmd.go`; `remote_runtime*.go`; `ops_reload.go`;
`ops_web_preview.go`.

## 2. The three gaps blocking buy→code→deploy

1. **No purchase front door** anywhere (web/mobile/MCP). Backend
   checkout route exists; nothing surfaces "buy / pick machine type /
   pay".
2. **No real-time "box ready" feedback.** Provision is async; only a
   manual-refresh owner panel + `GET /subscription`. No polling, no
   mobile managed view, no notification.
3. **Git connect is CLI/MCP-only.** `yaver git push-creds <device>`
   works but there's no one-click "Connect GitHub to this box" in
   web/mobile after purchase.

## 3. Phased plan

### Phase D1 — Purchase front door (web + mobile + MCP)
- Web: a `BuyManagedCloud` flow — machine-type picker (CPU /
  premium-KVM (Flutter/Kotlin) / GPU; iOS shown "coming soon, Mac"),
  region, → `POST /billing/yaver-cloud/checkout` → redirect to
  LemonSqueezy. Owner sees the dev-adopt/owner-bypass path too.
- Mobile: a Cloud screen mirroring it (checkout opens the LS URL in a
  web view / external browser; subscription state from `/subscription`).
- MCP/ops: `cloud_checkout` verb returning the checkout URL (so an
  agent can hand the user a pay link).
- Single default SKU = the KVM bare-metal box (does Android emulator
  + RN/Hermes + web + deploy). No tier picker beyond GPU; iOS shown
  "coming soon (Mac)". Depends on Phase D0 (Robot provisioner).

### Phase D2 — Provision status + ready handoff
- `GET /subscription` already returns machines+status. Add light
  polling to ManagedCloudPanel + a mobile managed-machines view; toast/
  push when status → active. Reuse the `origin` tag + honest-failure
  states already shipped.
- "Box ready" card → one-tap actions: Connect Git, Open Code,
  Start preview.

### Phase D3 — One-click Git connect (web + mobile)
- Wrap the existing `git_oauth_start`/`git_push_creds` MCP tools in a
  "Connect GitHub/GitLab" button on the managed-machine card (web +
  mobile). Device-flow code shown inline; on success creds are pushed
  to the box (`/machine/onboarding/apply`). Optional repo picker →
  set `repoUrl` (cloud-init already clones it).

### Phase D4 — Dev loop entrypoints parity
- WebRTC remote-runtime: add a web panel + MCP tool wrapper
  (`remote_runtime_start/stop`) over the existing
  `/remote-runtime/*` routes; works against the managed box like any
  owned device. (Flutter/Kotlin require the premium KVM SKU; RN uses
  Hermes, not this.)
- Hermes hot-reload + WebView preview: surface `ops reload` /
  `ops web-preview` as buttons on the managed-machine card in web +
  mobile (mobile `hotreload.tsx` already rich — point it at the
  managed deviceId).

### Phase D5 — Deploy cases from the managed box
- `ops deploy` / `code_deploy` already cover cloud/cloudflare/vercel/
  fly/netlify/railway/firebase/convex/eas/testflight/playstore with
  `machine=auto` routing. Surface a "Deploy" action on the managed-
  machine card (web + mobile) → pick target → stream logs (`ops logs`).
  TestFlight/Play remain Mac/CI-bound per CLAUDE.md.

## 4. Cleanest happy path (today, MCP/CLI — what to productize)

`POST /billing/yaver-cloud/checkout` → pay → webhook provisions →
poll `GET /subscription` until `status:active` → `yaver git
push-creds <deviceId>` → `ops <deviceId> reload` / remote-runtime /
`ops web-preview` → `ops deploy <target>`. Phases D1–D5 wrap each
hop in a web/mobile/MCP button. Owner can skip LemonSqueezy via the
allowlist + `dev-adopt` (already shipped).

## 5. Deploy cases (explicit)

| Target | Path | Surface |
|---|---|---|
| Convex / Cloudflare / Vercel / Supabase / CF Workers | `ops deploy` / `code_deploy`, `deploy_script_gen.go`, switch engine | EXISTS (ops/MCP); add web/mobile button |
| EAS / Play | `ops deploy target=eas/playstore`, `release-mobile.yml` | EXISTS; CI for Play |
| TestFlight | local-only (Mac), CLAUDE.md | EXISTS; never CI |
| npm (yaver-cli) | tag `cli/v*` (see `project_cli_deploy_must_use_tag`) | EXISTS |

## 6. Anchors (re-grep before use)

`backend/convex/http.ts` 3071 checkout / 2907 webhook /
3106+ dev-activate/adopt/deprovision; `cloudMachines.ts` provision +
cloud-init + adoptExisting; `subscriptions.ts` canProvisionManaged;
`ownerAllowlist.ts`; `ops_cloud.go`, `ops_deploy.go`,
`ops_reload.go`, `ops_web_preview.go`, `ops_machine_auto.go`;
`git_push_creds_cmd.go`, `mcp_tools.go` (git_oauth_*, code_deploy);
`remote_runtime*.go`; web `components/dashboard/{ManagedCloudPanel,
DevicesView,DeployCapabilitiesView,WebviewView}.tsx`,
`lib/subscription.ts`; mobile `app/(tabs)/{hotreload,gitproviders,
devices}.tsx`, `app/remote-runtime.tsx`.
