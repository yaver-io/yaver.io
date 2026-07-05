# Yaver Games Runtime

This document defines the first SFMG-in-Yaver integration and the generic
runtime boundary for Yaver-first strategy games. It is a starting contract; grep
the code before relying on it for implementation details.

The developer-platform lifecycle is defined in
`docs/architecture/YAVER_DEVELOPER_PLATFORM.md`: mobile sandbox first, provider-
neutral Git/source, private deploys, optional reviewed catalog publication, and
full exit rights.

## Decision

SFMG ships inside Yaver first. It is a free first-party Yaver game at launch,
not a separately released SFMG app and not a public third-party marketplace.

The Yaver build of SFMG must use Yaver OAuth/session identity. It must not show
a standalone SFMG login, and it must not own mobile payments directly.

## Why

- One first-party flagship is safer for App Store and Play Store review than a
  public game marketplace.
- Yaver can prove the platform with its own game before asking external
  developers to trust the runtime.
- SFMG already has manager/owner/club simulation depth, so it is the right first
  game to force the platform contract to handle real state.
- Keeping SFMG free at launch avoids early IAP/Play Billing complexity while
  still designing for future entitlements.

## Required SFMG Adapter

The SFMG adapter in `../sfmg` should target this boundary:

```ts
type YaverSFMGBootstrap = {
  yaverUserId: string;
  yaverSessionToken: string;
  scopes: string[];
  surface: "web" | "ios" | "android" | "tvos" | "android-tv" | "remote-runner";
  entitlementSnapshot: Record<string, boolean>;
};
```

Rules:

1. Yaver OAuth is required.
2. Yaver user ID is the account of record.
3. SFMG save IDs, club ownership, owner mode, leagues, multiplayer rooms, AI
   history, and moderation events must be keyed to Yaver identity.
4. The Yaver build must not offer standalone SFMG email/password login.
5. The launch version is free. Future products are Yaver entitlements, not
   direct SFMG checkout links.

## Generic Game Contract

Every Yaver-first strategy game should publish a manifest with:

- metadata: ID, title, owner, status, surfaces, age/content tags
- auth contract: required Yaver OAuth scopes and guest mode
- monetization contract: free/subscription/packs and platform billing owner
- command contract: state authority, input model, event log, reducer
- AI contract: parser, advisor, NPCs, narrator, moderation, test bots
- review contract: screenshots, reviewer access, catalog metadata

Production state uses this flow:

```text
input/voice/text
-> AI intent parser when needed
-> strict command JSON
-> validation
-> deterministic reducer
-> event log
-> snapshot
-> render on web/mobile/TV
```

The LLM never owns canonical state. It can propose commands, summarize, narrate,
simulate test users, and generate scenario drafts, but the reducer decides what
is legal.

## Remote Runner

Yaver Remote Runner is the developer/test execution surface:

- launch SFMG or another game on web/mobile/TV
- create test users and entitlement snapshots
- simulate multiplayer rooms
- run AI players/test bots
- record event logs and deterministic replays
- test voice/controller/touch command paths

This is not a generic cloud-gaming farm. For the first version, it is a
strategy-game runtime and QA harness.

## Developer Lifecycle

For SFMG and later third-party games, the basic path is:

```text
Yaver mobile app
-> sandbox/import repo
-> develop with Yaver app/MCP/runner
-> deploy private preview
-> optional Yaver catalog review
-> optional Yaver catalog publish
```

Source can live in Yaver Git, GitHub, GitLab, self-hosted Git, a local folder,
or a signed package artifact. Private deploy is not publication. A developer can
leave Yaver and keep shipping externally.

For SFMG specifically, Kivanc and Serhat are named owners. They can use Yaver
to develop the closed-source SFMG repo, allocate temporary managed Hetzner
workers, configure OpenCode/GLM on those target machines, and run/deploy private
previews without committing to catalog publication.

## MCP / Agent Contract

Yaver exposes a hosted MCP guidance tool named
`yaver_strategy_game_native_guide` and a manifest scanner named
`yaver_game_manifest_audit`. Local Yaver MCP servers and coding agents should
use the same product boundary:

- Yaver is primarily for strategy, simulation, tactics, management, and
  command/state-driven games.
- Game source can live in GitHub, GitLab, self-hosted Git, local folders, or
  signed bundles.
- Developers can use Yaver to develop, test, run privately, self-host, or do
  whatever their own project/license allows without sharing source with Yaver.
- Source/package sharing is only a condition for official in-Yaver catalog
  release/distribution.
- Yaver catalog publication requires private source/package review access.
- Mobile, tablet, TV, browser, and remote runner are first-class runtime
  surfaces.
- Watch and car surfaces are companion/briefing/approval surfaces, not full
  dense gameplay surfaces.

Yaver-native / Yaver-first games must satisfy these scanner constraints before
official in-Yaver release:

- `auth.provider = "yaver-oauth"`
- `auth.requiredInYaverBuild = true`
- required scopes include `openid`, `profile`, `yaver.games.play`,
  `yaver.games.save`, `yaver.games.events.write`, and `yaver.ai.invoke`
- `billing.billingOwner = "yaver"`
- `billing.directDeveloperPaymentsInYaverApp = false`
- `source.codeCopyIntoYaverRepo = false`
- source/package sharing is scoped to official Yaver catalog release only

## Billing Boundary

Yaver owns billing in the Yaver app:

- iOS/tvOS: Apple IAP
- Android/Google TV: Play Billing
- Web: Yaver web billing

Developer games call Yaver entitlement APIs. They do not insert their own
checkout links inside Yaver mobile/TV apps.

SFMG launch state:

```text
billing = free
future = subscription-included | scenario-pack | premium-unlock
```

## Beyond Games

The same runtime can support non-game products when the domain is command/state
driven:

- training simulations
- operations rooms
- education labs
- crisis rehearsal
- business simulators
- AI workflow copilots
- industrial or IoT control-room drills

The reusable primitive is not "game"; it is:

```text
intent -> command -> validation -> reducer -> event log -> replayable state
```

Games are the first product because they make this pattern visible, fun, and
monetizable. The infrastructure should stay generic enough to become a broader
interactive runtime later.

## Current Code Anchor

The generic app manifest now lives in `web/lib/yaver-apps.ts`. The game
compatibility wrapper lives in `web/lib/yaver-games.ts`.

Current web surfaces:

- `/apps` — generic Yaver Apps catalog/runtime contract.
- `/games` — games-first view, currently SFMG plus Carrotbet as a developer-owned
  Yaver-native import target.

Carrotbet is intentionally modeled as `externalRelease: "allowed"` plus
`yaverCatalogRelease: "optional-reviewed"`: it can keep shipping its own app,
while Yaver can still sell managed cloud, inference, feedback, MCP, relay,
TV/watch/car/XR surfaces, and optional catalog distribution.
