# Yaver Mesh — Mobile (Tailscale-grade UI) + Node-Role Conventions

**Status:** Deep design / analysis. Design-only — no code changed.
**Date:** 2026-06-07.
**Scope:** Redesign the mobile Mesh surface (`mobile/app/(tabs)/network.tsx`) into a
Tailscale-mobile-app-class experience, and formalize a node-role taxonomy
(exit node, gateway/subnet-router node, this-device, shared, ephemeral, tagged).

> Per CLAUDE.md: **code is the source of truth, docs drift.** Every file/field
> reference below was read on `main` at the time of writing. Re-grep before acting.

---

## 0. TL;DR

Yaver Mesh is **not a greenfield feature** — it is ~95% built and tracked on
`main`:

- **Control plane (Convex):** `backend/convex/mesh.ts` (488 lines) + `meshNodes`,
  `meshAcls`, `meshTags` tables in `schema.ts`. IP allocation, join/leave,
  endpoints heartbeat, desired-state (`wantEnabled`/`wantExitNode`/
  `wantUseExitNode`/`wantRoutes`), ACLs, tags — all real, token-hash + `ctx.auth`
  split for web vs agent.
- **Data plane (Go):** `desktop/agent/mesh/` — userspace WireGuard device, peer
  reconciler (20s), MagicDNS (`.mesh`), port-level ACL packet filter, STUN,
  per-platform `netconfig_*` (forwarding + NAT for exit nodes / subnet routers).
- **Relay-as-DERP fallback:** `relay/mesh.go` + `desktop/agent/mesh/derp.go` +
  `mesh_derp_transport.go` — symmetric-NAT bridge over the existing relay QUIC.
- **Address space:** `100.96.0.0/12`, deliberately disjoint from Tailscale's
  `100.64.0.0/10` so both can run at once (and a node on both can *bridge* a
  Tailnet — `TAILSCALE_BRIDGE_CIDR` in `network.tsx`).
- **Web console:** `web/components/dashboard/NetworkView.tsx` — full peer list +
  ACL editor + tagger + exit-node toggles.
- **Privacy:** WireGuard **private** key never leaves the device (agent vault /
  iOS Keychain / Android Keystore). `desktop/agent/convex_privacy_test.go` is a
  tripwire forbidding `wgPrivateKey`/`wg_private_key`/`meshPrivateKey` in any
  Convex payload.

**What is actually missing / weak — the real subject of this request:**

1. **Mobile UX.** `mobile/app/(tabs)/network.tsx` (589 lines) works but is a
   *flat utilitarian scroll*: a "This phone" card, a "Support a friend" block,
   a `MESH NODES` list with inline pills (`exit node`, `via: …`, a raw routes
   `TextInput`, `bridge Tailnet`), then a raw ACL editor. It is an admin form,
   not a Tailscale-style client. No node-detail screen, no exit-node picker, no
   copyable IP/DNS, no last-seen / version / connection-path, no search, no
   empty-state onboarding.
2. **On-device tunnel (Phase 7) is scaffolded but NOT activated.** The phone is
   a *console only* — it does not get its own `100.96` IP. The native pieces
   exist and are tracked (`mobile/native-mesh/{ios,android}`,
   `mobile/plugins/withMeshTunnel.js`, `src/lib/yaverMesh.ts`) but the Expo
   plugin is deliberately **not** in `app.json` (activation = Apple entitlement
   + new Xcode target + native rebuild on the Mac). Until then, the JS no-ops
   (`{ supported: false }`) and the UI shows a "coming in a native update" hint.
3. **No formal node-role taxonomy** surfaced to the user. Roles exist as raw
   booleans/strings (`isExitNode`, `wantExitNode`, `wantUseExitNode`,
   `advertisedRoutes`/`wantRoutes`) but there is no UI vocabulary — no "exit
   node" / "gateway node" / "this device" badges-and-meaning model like
   Tailscale's.

This doc designs (1) and (3) end-to-end, and treats (2) as the dependency that
turns the redesign from "remote admin console" into "real Tailscale-like
client." (2)'s activation steps already live in `docs/mesh-mobile-tunnel.md`;
this doc references rather than repeats them.

---

## 1. Tailscale mobile app — UX teardown (what we are matching)

The Tailscale iOS/Android app is the reference. Its structure, distilled:

| Element | Tailscale behavior | Why it matters |
|---|---|---|
| **Top connect toggle** | One giant VPN on/off switch; status line ("Connected", your IP). | The 90% action. Everything else is secondary. |
| **This device header** | Your own machine pinned at top: name, `100.x` IP, MagicDNS name, "you". | Orientation — "where am I on the mesh." |
| **Machine list** | Rows grouped (My devices / Tagged / Shared). Each: online dot, name, owner, OS glyph, IP, role badges (exit node, subnet router). | Scannable topology. |
| **Search / filter** | Filter machines by name. | Fleets get big. |
| **Node detail screen** | Tap a row → addresses (v4/v6 + MagicDNS, each copyable), last seen, client version, OS, "connected directly" vs "relayed (DERP)", advertised routes, "Use as exit node", expiry. | The depth layer. |
| **Exit node picker** | Dedicated screen: **None** / list of available exit nodes (with location) / "Allow local network access" / "Run this device as an exit node". | First-class concept, not a pill. |
| **Tailnet / account** | Tailnet switcher, account, settings (MagicDNS, key expiry…). | Multi-context. |
| **Admin actions** | ACLs/policy are **web-only** in Tailscale; the app stays consumer-clean. | Deliberate scope split. |

**Key takeaways for Yaver:**

- **Lead with one action** ("Connect"), demote administration.
- **Node detail is a real screen**, not inline pills.
- **Exit node is a first-class picker**, not a toggle in a row.
- **Roles are badges with meaning**, surfaced consistently.
- Tailscale hides ACL editing in the app. Yaver's solo-founder audience (see
  `user_target_audience.md`) actually benefits from *keeping* lightweight ACL
  editing reachable — but **behind a deliberate "Access rules" entry**, not
  dumped on the main screen. (Decision D3 below.)

---

## 2. Node-role taxonomy (the "exit node / gate node conventions")

Tailscale has a precise vocabulary. Yaver should adopt an equivalent one and
render it consistently everywhere (mobile rows, node detail, web console, CLI
`yaver mesh status`). Proposed canonical roles + how each maps to **fields that
already exist** in `meshNodes` / `/mesh/peers`:

### 2.1 Roles (what a node *is*)

| Role | Meaning | Backing fields (today) | Badge |
|---|---|---|---|
| **This device** | The node you're looking from. | derived: `deviceId === self` | `you` (pinned, no badge) |
| **Exit node** | Routes a peer's *full internet* traffic (`0.0.0.0/0`, `::/0`). | `isExitNode` (effective) / `wantExitNode` (desired) | `Exit node` (amber) |
| **Gateway node** *(= subnet router)* | Advertises one or more LAN CIDRs so peers reach a network behind it (e.g. `10.0.0.0/24`, a Tailnet via `100.64.0.0/10`). | `advertisedRoutes` (effective) / `wantRoutes` (desired), minus `0.0.0.0/0` | `Gateway · N routes` (cyan) |
| **Peer** | Plain mesh member, reachable by its `/32`. | default | none |
| **Mobile node** | A phone/tablet carrying traffic (Phase 7). | `deviceId` prefix `phone-ios-`/`phone-android-` (see `meshDeviceIdFromPubKey`) | OS glyph |

> **Naming decision (D1):** the user said "gate node." Tailscale calls this a
> **subnet router**. Recommendation: surface it as **"Gateway"** in the UI
> (clearer to non-network folks) with subtitle "advertises N subnet routes," and
> keep `subnet route` in tooltips/docs so Tailscale users aren't lost. A node can
> be **both** exit node and gateway.

### 2.2 State (what a node's connection *is*)

| State | Meaning | Backing fields | Indicator |
|---|---|---|---|
| **Online / offline** | Recent handshake. | `online`, `lastHandshake` | green/grey dot |
| **Direct vs relayed** | P2P UDP vs relay-DERP bridge. | *NOT yet in payload* — see Gap G2 | "Direct" / "Relayed" subtitle |
| **Owner / shared** | Yours vs shared-in via infra grant. | `accessScope: owner\|shared\|peer` | `shared` (violet) |
| **Using exit node** | This node sends its traffic via another. | `wantUseExitNode` (deviceId) | "via <name>" chip |
| **Tagged** | Carries ACL tags. | `meshTags` (per device) | `tag:server` chips |
| **Pending / desired≠effective** | Console set `want*` but agent hasn't converged. | compare `want*` vs effective | "applying…" |

### 2.3 The exit-node / gateway distinction (depth — this is the user's focus)

Both are "this node forwards traffic for others," differing only in **scope of
`AllowedIPs`**:

- **Exit node** → advertises the default route `0.0.0.0/0` (+ `::/0`). A peer
  that *selects* it (`wantUseExitNode = <exitNodeId>`) sends **all** its
  internet traffic through it. Use: egress through a fixed IP, geo, hostile
  Wi-Fi.
- **Gateway / subnet router** → advertises **specific** CIDRs
  (`10.0.0.0/24`, a printer VLAN, a Tailnet's `100.64.0.0/10`). Peers
  automatically route *only those prefixes* through it; no per-peer selection
  needed — longest-prefix-match wins (the comment at `network.tsx:60-62`
  explains exactly this for the Tailnet bridge).

The data-plane already implements both (`netconfig_*.go` does IP forwarding +
NAT masquerade; `buildWgQuickConfig` in `yaverMesh.ts` puts
`advertisedRoutes` into a peer's `AllowedIPs`). **The gap is purely UI
vocabulary + an exit-node *picker*.**

**Two control axes per node (must not be conflated in UI):**
1. *Provider* axis — "**This node serves as** an exit node / gateway."
   (`wantExitNode`, `wantRoutes`.)
2. *Consumer* axis — "**This node routes through** exit node X."
   (`wantUseExitNode`.)

Today both are crammed into one row in `network.tsx`. The redesign splits them:
provider settings live on the **node detail** screen; the consumer choice for
*this phone* lives in a dedicated **Exit node picker**.

---

## 3. Proposed mobile information architecture

Replace the single flat `network.tsx` scroll with a small hub-and-detail tree
(all under the existing hidden-tab pattern from `_layout.tsx`; the Robot Cell
commit `f211f1f3` is the exact wiring precedent — `Tabs.Screen name=… href:null
headerLeft:backToMore` + a More card).

```
More ▸ "Yaver Mesh"  (new card; replaces/ër renames current "Mesh" card)
└── mesh/index           Mesh home (the Tailscale-style main screen)
    ├── mesh/node/[id]    Node detail (addresses, role toggles, routes, last seen)
    ├── mesh/exit-node    Exit-node picker for THIS phone
    ├── mesh/access       Access rules (ACLs) + tags  — "advanced"
    └── mesh/share        Support / sharing (the existing support-link flow, lifted out)
```

> Expo-router note: nested folders under `app/(tabs)/` need each leaf registered
> as a hidden `Tabs.Screen`. Simpler alternative that matches the current repo
> style: keep flat route files `mesh.tsx`, `mesh-node.tsx`, `mesh-exit.tsx`,
> `mesh-access.tsx`, passing `deviceId` via `router.navigate({ pathname, params })`.
> Decision D4.

### 3.1 Mesh home (`mesh/index`) — the main screen

Top-to-bottom, mirroring Tailscale:

1. **Connect hero (this phone).**
   - Big rounded toggle: **Connect to mesh** / **Disconnect** (reuse
     `toggleTunnel` → `meshTunnelUp/Down` from `yaverMesh.ts`).
   - When connected: status line "Connected · `100.96.x.y` · via Direct/Relay".
   - When the native extension is absent (`!isMeshTunnelSupported()`): render a
     calm **"Manage-only" state** — "This phone manages the mesh. On-device
     tunneling arrives in a native update." (Today's behavior, but visually a
     first-class hero, not a footnote.) This keeps the screen honest and shippable
     **now**, lighting up automatically when Phase 7 lands.
   - **Exit-node row** under the hero: "Exit node: **None ›**" → opens
     `mesh/exit-node`. (Only meaningful when connected; show disabled hint
     otherwise.)

2. **This device card** (when tunnel active): name, `100.96` IP + MagicDNS
   `<alias>.mesh` (copyable), role badges if this phone advertises anything.

3. **Machine list**, grouped + searchable:
   - Sections: **My devices**, **Tagged**, **Shared with me**.
   - Row = online dot · name · OS glyph · `100.96` IP (mono) · role badges
     (`Exit node`, `Gateway · N`) · `›`.
   - Tap → `mesh/node/[id]`.
   - Search field appears when > ~8 nodes.

4. **Footer chips**: "Access rules ›" (→ `mesh/access`), "Sharing ›"
   (→ `mesh/share`). Demoted, not gone.

### 3.2 Node detail (`mesh/node/[id]`)

The depth layer Yaver currently lacks entirely:

- **Header**: name (editable alias → existing `devices.alias`), online state,
  OS, "owner/shared."
- **Addresses**: `100.96` IPv4, IPv6 (if present), MagicDNS `<alias>.mesh` — each
  with a copy button (`expo-clipboard`, already used).
- **Connection**: Direct vs Relayed, last handshake (relative time), client
  version (Gap G2 — needs payload fields).
- **This node serves as** (owner-only, provider axis):
  - **Exit node** switch (`wantExitNode`).
  - **Gateway** — routes editor: list of CIDR chips + add/remove (replaces the
    raw comma `TextInput`), plus a one-tap **"Bridge my Tailnet"** toggle
    (`100.64.0.0/10`, the existing `TAILSCALE_BRIDGE_CIDR` logic).
  - Show **desired vs effective** ("applying…" until the agent converges).
- **Tags** (owner-only): chips from `meshTags`, add/remove (drives ACL `tag:` src/dst).
- **Danger**: remove node / revoke share (where applicable).

### 3.3 Exit-node picker (`mesh/exit-node`) — first-class, Tailscale-style

A dedicated screen for **this phone's** consumer choice (`wantUseExitNode`):

```
○ None  (direct internet)
─────────────────────────
◉ home-server    100.96.0.4   · online
○ hetzner-edge   100.96.2.9   · online
○ office-gw      100.96.1.7   · offline (disabled)
─────────────────────────
[ ] Allow local network access while using exit node
```

- Single-select radio; writes `wantUseExitNode` for this phone via
  `/mesh/node/config`.
- Lists only nodes where `isExitNode || wantExitNode`.
- "Allow local network access" = keep LAN routes out of the default-route capture
  (a wg `AllowedIPs` refinement; agent/native concern, surfaced as a checkbox).

### 3.4 Access rules (`mesh/access`) — advanced, kept but demoted

Lift the existing ACL editor (`network.tsx:486-579`) here, plus tag management.
Add a plain-English header and rule **templates** ("Lock down to SSH only,"
"Isolate guests") so a solo founder isn't authoring matchers by hand. This is
where Yaver deliberately diverges from Tailscale (which is web-only) — but it's
behind a tap, not on the main screen.

### 3.5 Sharing (`mesh/share`)

The current "Support a friend" block + `supporting` / `supportedBy` lists
(`network.tsx:283-355`) move here verbatim. Links to the support-link flow
(`docs/mesh-support-link.md`).

---

## 4. Visual / component plan (matches repo conventions)

- **Design system:** reuse `useColors()` (`ThemeContext`), `YaverGlass` /
  `YaverSheet`, `AppScreenHeader` (every More-opened screen uses it), token
  spacing/typography (`src/theme/tokens.ts`).
- **Icons:** project rule = **no icon library** (`feedback_no_lucide_use_inline_svg`).
  Use inline SVG components or the existing Unicode-glyph convention from
  `more.tsx`. Need a small set: online dot, exit-node (↗ / up-arrow-in-circle),
  gateway (⇄ / subnet), copy, search, chevron, OS glyphs (apple/android/linux/win).
  Build a tiny `MeshIcons.tsx` of inline SVGs.
- **Reusable components to add:**
  - `MeshNodeRow` — dot + name + OS + IP + role badges + chevron.
  - `RoleBadge` — typed (`exit` amber, `gateway` cyan, `shared` violet, `tag` neutral).
  - `ConnectHero` — the big toggle + status.
  - `CidrChips` — add/remove CIDR editor (replaces raw `TextInput`).
  - `CopyableAddress` — mono text + copy affordance.
- **Color semantics** (keep existing palette): online `#34d399`, exit `#fcd34d`,
  gateway/tailnet `#22d3ee`, shared `#c4b5fd`, error `#ef4444`.

---

## 5. Data-model & API gaps

The redesign needs slightly richer node data than `/mesh/peers` returns today.

| Gap | Need | Where |
|---|---|---|
| **G1 — MagicDNS name in payload** | Return `<alias>.mesh` per peer so detail can show/copy it. DNS responder already knows aliases (`mesh/dns.go`); expose in `listMeshPeers`/`/mesh/peers`. | `backend/convex/mesh.ts`, agent dns |
| **G2 — Connection path + telemetry** | `connectionType: "direct"\|"relay"`, `lastHandshake`, `clientVersion`, `os`, `platform`. `lastHandshake` already on `meshNodes`; add `os`/`platform`/`version` to join payload; derive direct/relay agent-side. **Privacy:** all non-sensitive (no paths/secrets) — but add each new field to `fieldsWeForbidInAnyConvexPayload` review + a `convex_privacy_test.go` assertion that they're the *only* additions. | schema + `mesh.ts` + privacy test |
| **G3 — Effective vs desired** | Payload already has both `isExitNode`/`advertisedRoutes` (effective) and `wantExitNode`/`wantRoutes` (desired) → expose both to render "applying…". Mostly already present in `MeshPeer` type. | none / minor |
| **G4 — Phone as first-class node** | The phone joins via `joinMeshWeb` (token-hash) with a derived id (`meshDeviceIdFromPubKey`). Ensure it appears in its **own** `/mesh/peers` so "This device" renders. Confirm `joinMeshWeb` exists (doc says "we add a thin `mesh:joinMeshWeb`" — **verify it's actually implemented**, not just planned). | `mesh.ts` (verify) |
| **G5 — "Allow LAN access" flag** | Persist a per-node `wantExitLanAccess` bool so the exit-node picker checkbox round-trips. | schema + `mesh.ts` + agent/native |

> **Privacy reminder (CLAUDE.md):** every new Convex-bound field must pass
> `desktop/agent/convex_privacy_test.go`. None of G1–G5 introduce paths, tokens,
> stdout, or LAN customer IPs — but the test enumerates *allowed* shapes, so each
> addition needs a corresponding test update. The WireGuard private key stays out
> (already enforced).

---

## 6. The on-device tunnel (Phase 7) — the dependency that makes this "real"

The redesign **ships useful without Phase 7** (manage-only hero), but only
*becomes* a Tailscale-like client once the phone carries traffic. The activation
is hardware/Apple-bound and already documented:

- iOS: NetworkExtension entitlement (Apple portal) + `YaverMeshTunnel`
  packet-tunnel-provider target via `mobile/plugins/withMeshTunnel.js`
  (the one `TODO(activation)` PBX piece) + `WireGuardKit`.
- Android: `com.wireguard.android:tunnel` GoBackend + `VpnService` +
  foreground notification + `BIND_VPN_SERVICE`.
- Both reuse the **existing** control plane (`/mesh/join`, `/mesh/peers`) and the
  relay-DERP fallback (`relay/mesh.go`) for CGNAT. Private key in Keychain/Keystore.

See `docs/mesh-mobile-tunnel.md` §"Activation checklist." This is a Mac + real
device task; it cannot be compiled/verified from CI. The UI work in §3 is the
right thing to do **first** so that the moment the extension lands, the hero
toggle is already wired (`yaverMesh.ts` lights up automatically).

---

## 7. Phased build plan

**Phase A — UI redesign on existing data (ships today, no native, no backend).**
Pure mobile. Split `network.tsx` into the IA in §3 using only fields already in
`/mesh/peers`. Deliver: ConnectHero (manage-only state), grouped+searchable node
list, node-detail screen, exit-node picker, role badges, CIDR-chip editor,
access-rules + sharing moved behind taps. *Highest value / lowest risk.*

**Phase B — payload enrichment (small backend).** G1–G3, G5: MagicDNS name,
`connectionType`, `os`/`version`, `wantExitLanAccess`; update privacy test.
Verify `joinMeshWeb` (G4) actually exists.

**Phase C — node-role polish + web/CLI parity.** Apply the same taxonomy
vocabulary to `web/components/dashboard/NetworkView.tsx` and `yaver mesh status`
so "Exit node / Gateway" mean the same thing everywhere.

**Phase D — Phase 7 activation (Mac + device, Apple-gated).** Per
`docs/mesh-mobile-tunnel.md`. Turns the manage-only hero into a live tunnel.

Phases A–C are doable from this environment; D is not.

---

## 8. Decisions for the user (open questions)

- **D1 — "Gateway" vs "Subnet router" naming.** Recommend **Gateway** in UI
  (subtitle "advertises N subnet routes"), keep "subnet route" in tooltips/docs.
- **D2 — Rename the More card** "Mesh" → "Yaver Mesh" (with subtitle "WireGuard
  overlay · exit nodes · access")? Or keep "Mesh."
- **D3 — Keep ACL editing in the app** (behind `mesh/access`) or go
  Tailscale-style web-only? Recommend **keep, demoted** (solo-founder audience).
- **D4 — Route structure:** nested `mesh/...` folder vs flat
  `mesh.tsx`/`mesh-node.tsx`/… Recommend **flat files** (matches current repo).
- **D5 — Build it now?** This doc is analysis-only. Phase A is a self-contained
  mobile change I can implement next on request (then `yaver wireless push` per
  `feedback_mobile_only_wire_push`).

---

## 9. File map (for whoever implements)

**Read first:** `mobile/app/(tabs)/network.tsx` (current screen),
`mobile/src/lib/yaverMesh.ts` (tunnel shim), `backend/convex/mesh.ts` +
`schema.ts` (control plane), `docs/mesh-mobile-tunnel.md` (Phase 7),
`web/components/dashboard/NetworkView.tsx` (parity reference).

**New (Phase A):**
- `mobile/app/(tabs)/mesh.tsx` (home) — replaces `network.tsx` role.
- `mobile/app/(tabs)/mesh-node.tsx`, `mesh-exit.tsx`, `mesh-access.tsx`,
  `mesh-share.tsx`.
- `mobile/src/components/mesh/{MeshNodeRow,RoleBadge,ConnectHero,CidrChips,CopyableAddress,MeshIcons}.tsx`.
- Wire in `mobile/app/(tabs)/_layout.tsx` (hidden `Tabs.Screen` per route) +
  `mobile/app/(tabs)/more.tsx` (the card + `handleMesh`).

**Touch (Phase B/C):** `backend/convex/mesh.ts`, `backend/convex/schema.ts`,
`desktop/agent/convex_privacy_test.go`, `desktop/agent/mesh_cmd.go`
(status vocabulary), `web/components/dashboard/NetworkView.tsx`.

**Don't touch without the Mac + Apple steps:** `mobile/native-mesh/*`,
`mobile/plugins/withMeshTunnel.js`, `app.json` plugins (Phase D only).
