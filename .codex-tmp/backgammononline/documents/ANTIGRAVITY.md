# Backgammon Platform — Antigravity Stack Blueprint

Here is your full `.md` architecture document. You can save this as `ANTIGRAVITY.md` in the root of your project.

---

```markdown
# Backgammon Platform — Antigravity Stack Blueprint

> **"Antigravity for coding"** — a stack so well-chosen that complexity
> disappears. Less friction, more flight.

---

## Table of Contents

1. [Vision](#vision)
2. [What is the Antigravity Stack?](#what-is-the-antigravity-stack)
3. [Tech Stack Breakdown](#tech-stack-breakdown)
4. [Architecture Overview](#architecture-overview)
5. [Phase 1 — Playable Backgammon (MVP)](#phase-1--playable-backgammon-mvp)
6. [Phase 2 — Multiplayer & Real-Time](#phase-2--multiplayer--real-time)
7. [Phase 3 — Tournaments & Real Money](#phase-3--tournaments--real-money)
8. [Project Structure](#project-structure)
9. [Data Models (Convex Schema)](#data-models-convex-schema)
10. [Deployment Pipeline](#deployment-pipeline)
11. [Environment Variables](#environment-variables)
12. [Development Roadmap](#development-roadmap)
13. [Cost Estimate](#cost-estimate)

---

## Vision

A browser-based backgammon platform that starts as a **polished single/local
multiplayer game** and scales into a **real-money tournament platform** — with
global low-latency infrastructure, reactive real-time state, and seamless
payment flows.

The architecture is chosen so that **every phase adds on top of the previous
one** without rewrites. You never pay for infrastructure you haven't needed yet.

---

## What is the Antigravity Stack?

"Antigravity" means choosing tools that eliminate the gravitational drag of
traditional web development:

| Traditional Drag                  | Antigravity Solution          |
|-----------------------------------|-------------------------------|
| Managing WebSocket servers        | Convex (reactive DB + WS)     |
| Configuring CDN / edge rules      | Cloudflare Pages + Workers    |
| Building payment + subscriptions  | Lemon Squeezy                 |
| Wiring frontend state             | React + Convex hooks          |
| Ops overhead (servers, DevOps)    | JAMstack — no servers to own  |

The result: **two developers can ship a production-grade, real-money tournament
platform** that would traditionally require a backend team, a DevOps engineer,
and a payments specialist.

---

## Tech Stack Breakdown

### React (Frontend Framework)

- **Role**: UI layer, game board rendering, state-driven component tree.
- **Why**: Component model maps perfectly to backgammon's discrete game objects
  (board, checkers, dice, pip labels).
- **Libs**:
  - `react` + `react-dom` — core
  - `react-router-dom` — client-side routing
  - `framer-motion` — checker animations, dice rolls
  - `tailwindcss` — utility styling
  - `convex/react` — reactive hooks to Convex backend

---

### Convex (Backend-as-a-Service + Real-Time Database)

- **Role**: The "brain" of the platform. Handles all game state, user records,
  matchmaking, and tournament brackets.
- **Why**: Convex is a **reactive, transactional database** with built-in
  WebSocket subscriptions. When Player A moves a checker, Player B's board
  updates **automatically** — zero polling, zero manual WebSocket code.
- **Key Convex concepts used**:
  - `query` — read game/user/tournament state (auto-subscribes in React)
  - `mutation` — move checker, roll dice, double cube
  - `action` — call external APIs (Lemon Squeezy webhooks, ELO recalculation)
  - `scheduler` — rematch timers, tournament round advancement
- **Auth**: Convex Auth (with Clerk or built-in) for user identity

```
Client (React)
     |
     |  useQuery / useMutation (WebSocket, auto-reactive)
     v
 Convex Functions (TypeScript, server-side)
     |
     v
 Convex Database (transactional, consistent)
```

---

### Cloudflare (Edge Hosting + Security)

- **Role**: Hosts the frontend (Pages), handles DDoS protection, geo-routing,
  and will run edge middleware for auth token validation.
- **Why**:
  - **Cloudflare Pages** — deploy the React app globally in seconds.
    Deploys on every `git push` to `main`.
  - **Cloudflare Workers** — optional edge functions for webhook proxying
    (Lemon Squeezy → Convex), rate-limiting moves, IP banning cheaters.
  - **Cloudflare R2** — store avatar images, game replay files cheaply.
  - 300+ PoPs worldwide = <50ms load times for players globally.
- **DNS + DDoS**: Automatic. Tournament launch days won't go down.

---

### Lemon Squeezy (Payments & Subscriptions)

- **Role**: All real-money flows — tournament entry fees, prize payouts,
  premium memberships.
- **Why**:
  - Lemon Squeezy is a **Merchant of Record** — they handle tax compliance
    (VAT, sales tax) in every country. You never touch tax law.
  - Built-in **checkout overlays**, **customer portals**, and **webhooks**.
  - Supports **one-time payments** (tournament entry) and **subscriptions**
    (premium tier).
- **Integration**:
  - A **Cloudflare Worker** receives Lemon Squeezy webhooks.
  - The Worker calls a **Convex action** to mark a player's entry as paid
    and register them in the tournament bracket.

```
Player pays entry fee
        |
        v
Lemon Squeezy Checkout (hosted, PCI compliant)
        |
        v (webhook: order_created)
Cloudflare Worker (validates signature)
        |
        v
Convex Action (registerTournamentEntry)
        |
        v
Convex DB updated — player is in the bracket
```

---

### JAMstack (Architecture Philosophy)

- **Role**: The overarching deployment philosophy tying everything together.
- **JAM** = **J**avaScript + **A**PIs + **M**arkup
  - **JavaScript**: React app, all interactivity in the client.
  - **APIs**: Convex (game logic/DB), Lemon Squeezy (payments), Cloudflare
    Workers (edge logic).
  - **Markup**: Pre-built static HTML shell deployed to Cloudflare Pages.
- **Why JAMstack for a game platform?**
  - No servers to manage or scale.
  - Static assets on a global CDN = instant loads.
  - Each service (DB, payments, auth) is best-in-class and independent.
  - Each "layer" can be swapped or upgraded without touching the others.

---

## Architecture Overview

```
+-----------------------------------------------------+
|               CLOUDFLARE PAGES                      |
|   React App (static build, globally distributed)    |
|   - Game Board UI                                   |
|   - Tournament Lobby                                |
|   - User Profile / Leaderboard                      |
+-------------------+---------------------------------+
                    |
          WebSocket / HTTPS
                    |
+-------------------v---------------------------------+
|                   CONVEX                            |
|   - Game State (boards, turns, dice)                |
|   - User Profiles (ELO, history)                   |
|   - Matchmaking Queue                               |
|   - Tournament Brackets                             |
|   - Scheduled Functions (timers, round advancement) |
+-------------------+---------------------------------+
                    |
         Webhook / HTTP Actions
                    |
+-------------------v---------------------------------+
|           CLOUDFLARE WORKERS                        |
|   - Lemon Squeezy webhook handler                   |
|   - Auth token edge validation                      |
|   - Rate limiting / move validation proxy           |
+-------------------+---------------------------------+
                    |
+-------------------v---------------------------------+
|              LEMON SQUEEZY                          |
|   - Tournament entry payments                       |
|   - Premium subscription billing                   |
|   - Tax compliance (MoR)                            |
|   - Customer portal                                 |
+-----------------------------------------------------+
```

---

## Phase 1 — Playable Backgammon (MVP)

**Goal**: A fully functional backgammon game playable in the browser.

### Features

- [ ] Backgammon board rendered in React (24 points, bar, home boards)
- [ ] Local 2-player mode (pass-and-play on same browser)
- [ ] vs. AI mode (basic pip-count heuristic AI, runs client-side)
- [ ] Dice roll animation (seeded random, verifiable server-side later)
- [ ] Legal move highlighting
- [ ] Checker hit → bar mechanic
- [ ] Bearing off mechanic
- [ ] Doubling cube (UI only in MVP)
- [ ] Win detection + score display
- [ ] Responsive design (desktop + tablet)
- [ ] No login required for local play

### Stack used in Phase 1

| Layer    | Used?  | Notes                                  |
|----------|--------|----------------------------------------|
| React    | Yes    | Full game UI                           |
| Convex   | No     | Not needed yet (local state only)      |
| CF Pages | Yes    | Hosting                                |
| CF Workers | No   | Not needed yet                         |
| Lemon Squeezy | No | Not needed yet                    |

### Key files to build first

```
src/
  game/
    backgammon.ts       # Pure game logic (no React, fully testable)
    types.ts            # GameState, Move, Checker, Point, Dice types
    legalMoves.ts       # Move validation engine
    ai.ts               # Simple heuristic AI
  components/
    Board.tsx           # The 24-point board layout
    Point.tsx           # A single triangular point
    Checker.tsx         # Animated checker piece
    Dice.tsx            # Dice roll display + animation
    DoublingCube.tsx    # Cube display
    GameControls.tsx    # Roll, undo, resign buttons
  App.tsx
```

---

## Phase 2 — Multiplayer & Real-Time

**Goal**: Players can create and join online games against strangers or friends.

### Features

- [ ] User registration and login (Convex Auth + Clerk)
- [ ] Public matchmaking queue
- [ ] Private game links (share to invite a friend)
- [ ] Real-time board sync via Convex subscriptions
- [ ] Server-side dice rolling (provably fair, stored in Convex)
- [ ] Move validation on the server (prevent cheating)
- [ ] Reconnection handling (game state persists in Convex)
- [ ] In-game chat
- [ ] ELO rating system (updated via Convex mutation on game end)
- [ ] Public leaderboard
- [ ] Game history / replay viewer (stored in Convex + R2)

### Stack used in Phase 2

| Layer    | Used?  | Notes                                  |
|----------|--------|----------------------------------------|
| React    | Yes    | + Convex hooks for live state          |
| Convex   | YES    | Core: game state, users, matchmaking   |
| CF Pages | Yes    | Hosting                                |
| CF Workers | Optional | Auth edge middleware              |
| Lemon Squeezy | No | Not needed yet                    |

### Convex functions to write

```typescript
// queries (reactive, auto-subscribe in React)
getGame(gameId)          // live board state
getMatchmakingQueue()    // who is waiting
getLeaderboard()         // top ELO players

// mutations (called from client)
joinMatchmakingQueue(userId)
createPrivateGame(userId)
joinGame(gameId, userId)
rollDice(gameId, userId)          // server-side, provably fair
makeMove(gameId, userId, move)    // validated server-side
resign(gameId, userId)
sendChatMessage(gameId, userId, text)

// actions (async, can call external services)
calculateElo(winnerId, loserId)
saveGameReplay(gameId)             // write to R2
```

---

## Phase 3 — Tournaments & Real Money

**Goal**: Run bracketed tournaments with entry fees and prize pools.

### Features

- [ ] Tournament creation (admin panel)
- [ ] Tournament types: Single Elimination, Double Elimination, Swiss
- [ ] Entry fee payment via Lemon Squeezy checkout
- [ ] Prize pool calculation (entry fees - platform cut)
- [ ] Automated bracket advancement (Convex scheduler)
- [ ] Match result verification before advancing
- [ ] Prize payout flow (Lemon Squeezy payout API or manual)
- [ ] Premium membership tier (monthly subscription via Lemon Squeezy)
  - Benefits: reduced rake, priority matchmaking, exclusive tournaments
- [ ] Anti-cheat: move timing analysis, IP fingerprinting via Cloudflare
- [ ] Responsible gambling features: deposit limits, self-exclusion
- [ ] Legal compliance checklist (varies by jurisdiction)

### Stack used in Phase 3

| Layer    | Used?  | Notes                                          |
|----------|--------|------------------------------------------------|
| React    | Yes    | Tournament lobby, bracket viewer               |
| Convex   | Yes    | Bracket state, payment status, scheduling      |
| CF Pages | Yes    | Hosting                                        |
| CF Workers | YES  | Webhook handler, rate limiting, anti-cheat     |
| Lemon Squeezy | YES | Entry fees, subscriptions, payouts       |

### Payment Flow Detail

```
1. Player clicks "Enter Tournament ($10)"
2. React calls Convex mutation: reserveTournamentSpot(tournamentId, userId)
   - Creates a PENDING entry in Convex DB
3. React redirects to Lemon Squeezy hosted checkout
   - Custom metadata: { tournamentId, userId, convexEntryId }
4. Player pays (credit card, PayPal, etc.)
5. Lemon Squeezy fires webhook: order_created
6. Cloudflare Worker receives webhook:
   - Validates HMAC signature (Lemon Squeezy secret)
   - Calls Convex HTTP action: confirmTournamentEntry(entryId)
7. Convex marks entry as CONFIRMED
8. Convex useQuery in React auto-updates — player sees "Registered" badge
9. When tournament fills → Convex scheduler fires → bracket generated
```

---

## Project Structure

```
backgammon-platform/
|
+-- apps/
|   +-- web/                        # React app (Vite + Tailwind)
|   |   +-- src/
|   |   |   +-- game/               # Pure game logic (no React)
|   |   |   +-- components/         # UI components
|   |   |   +-- pages/              # Route-level pages
|   |   |   +-- hooks/              # Custom React hooks
|   |   |   +-- lib/                # Utilities
|   |   +-- index.html
|   |   +-- vite.config.ts
|   |
|   +-- workers/                    # Cloudflare Workers
|       +-- webhook-handler/        # Lemon Squeezy webhook receiver
|       +-- auth-middleware/        # Edge auth validation
|
+-- convex/                         # Convex backend (lives at repo root)
|   +-- schema.ts                   # Database schema
|   +-- games.ts                    # Game mutations/queries
|   +-- users.ts                    # User mutations/queries
|   +-- tournaments.ts              # Tournament logic
|   +-- matchmaking.ts              # Queue logic
|   +-- _generated/                 # Auto-generated by Convex CLI
|
+-- packages/
|   +-- game-engine/                # Shared backgammon logic (npm package)
|   +-- types/                      # Shared TypeScript types
|
+-- ANTIGRAVITY.md                  # This file
+-- package.json
+-- turbo.json                      # Turborepo (monorepo task runner)
```

---

## Data Models (Convex Schema)

```typescript
// convex/schema.ts

import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

export default defineSchema({

  users: defineTable({
    clerkId: v.string(),
    username: v.string(),
    email: v.string(),
    elo: v.number(),                  // default: 1200
    gamesPlayed: v.number(),
    gamesWon: v.number(),
    premiumUntil: v.optional(v.number()),  // timestamp
    isBanned: v.boolean(),
  }).index("by_clerkId", ["clerkId"])
    .index("by_elo", ["elo"]),

  games: defineTable({
    playerOneId: v.id("users"),
    playerTwoId: v.optional(v.id("users")),   // null if vs AI
    status: v.union(
      v.literal("waiting"),
      v.literal("active"),
      v.literal("completed"),
      v.literal("abandoned")
    ),
    boardState: v.string(),           // JSON-serialised GameState
    currentTurn: v.id("users"),
    dice: v.array(v.number()),
    doublingCubeValue: v.number(),    // 1,2,4,8,16,32,64
    doublingCubeOwner: v.optional(v.id("users")),
    winnerId: v.optional(v.id("users")),
    winType: v.optional(v.union(
      v.literal("normal"),
      v.literal("gammon"),
      v.literal("backgammon")
    )),
    tournamentId: v.optional(v.id("tournaments")),
    createdAt: v.number(),
    updatedAt: v.number(),
  }).index("by_status", ["status"])
    .index("by_tournament", ["tournamentId"]),

  tournaments: defineTable({
    name: v.string(),
    format: v.union(
      v.literal("single_elimination"),
      v.literal("double_elimination"),
      v.literal("swiss")
    ),
    status: v.union(
      v.literal("registration"),
      v.literal("active"),
      v.literal("completed")
    ),
    entryFeeUsd: v.number(),
    maxPlayers: v.number(),
    prizePoolUsd: v.number(),
    platformCutPercent: v.number(),   // e.g. 10
    startTime: v.number(),
    bracket: v.string(),              // JSON bracket structure
    lemonSqueezyVariantId: v.string(),
  }),

  tournamentEntries: defineTable({
    tournamentId: v.id("tournaments"),
    userId: v.id("users"),
    status: v.union(
      v.literal("pending"),           // awaiting payment
      v.literal("confirmed"),         // paid
      v.literal("eliminated"),
      v.literal("winner")
    ),
    lemonSqueezyOrderId: v.optional(v.string()),
    seed: v.optional(v.number()),     // bracket seeding position
  }).index("by_tournament", ["tournamentId"])
    .index("by_user", ["userId"]),

  chatMessages: defineTable({
    gameId: v.id("games"),
    userId: v.id("users"),
    text: v.string(),
    createdAt: v.number(),
  }).index("by_game", ["gameId"]),

});
```

---

## Deployment Pipeline

```
Developer pushes to GitHub
        |
        +-------> Cloudflare Pages CI
        |         - npm run build (React app)
        |         - Deploy to global CDN
        |         - Preview URL per PR
        |
        +-------> Convex CI (auto via npx convex deploy)
        |         - Type-checks convex/ functions
        |         - Deploys backend functions + schema migrations
        |
        +-------> Cloudflare Workers CI (Wrangler)
                  - Deploys webhook handler Worker
                  - Deploys auth middleware Worker
```

### Branch Strategy

| Branch    | Environment | Notes                              |
|-----------|-------------|-------------------------------------|
| `main`    | Production  | Auto-deploys on merge               |
| `staging` | Staging     | For testing before merge            |
| `feat/*`  | Preview     | Cloudflare Pages preview URL per PR |

---

## Environment Variables

```bash
# apps/web/.env.local

VITE_CONVEX_URL=https://your-project.convex.cloud
VITE_CLERK_PUBLISHABLE_KEY=pk_live_...
VITE_LEMON_SQUEEZY_STORE_ID=12345

# convex/.env (set via Convex dashboard)

CLERK_SECRET_KEY=sk_live_...
LEMON_SQUEEZY_API_KEY=eyJ...

# workers/webhook-handler/.env (set via Wrangler secrets)

LEMON_SQUEEZY_WEBHOOK_SECRET=whsec_...
CONVEX_DEPLOY_KEY=prod:...
```

---

## Development Roadmap

### Sprint 1 (Week 1-2) — Game Engine
- [ ] Write pure TypeScript backgammon engine (`packages/game-engine`)
  - Board representation, move generation, hit/bar/bear-off logic
  - 100% unit tested
- [ ] Simple heuristic AI (pip count + blocking strategy)

### Sprint 2 (Week 3-4) — React UI
- [ ] Build board, checker, dice, and control components
- [ ] Wire local 2-player game
- [ ] Add animations (framer-motion)
- [ ] Deploy to Cloudflare Pages

### Sprint 3 (Week 5-6) — Convex Integration
- [ ] Set up Convex project + schema
- [ ] User auth with Clerk
- [ ] Online matchmaking + real-time game sync
- [ ] Server-side dice + move validation

### Sprint 4 (Week 7-8) — Polish & ELO
- [ ] ELO rating system
- [ ] Leaderboard
- [ ] Game history
- [ ] Chat

### Sprint 5 (Week 9-10) — Tournaments (no money yet)
- [ ] Tournament creation (admin)
- [ ] Bracket logic in Convex
- [ ] Automated round advancement via Convex scheduler
- [ ] Free-entry practice tournaments

### Sprint 6 (Week 11-12) — Real Money
- [ ] Lemon Squeezy integration
- [ ] Cloudflare Worker webhook handler
- [ ] Paid tournament entry flow
- [ ] Premium subscription

### Sprint 7 (Week 13+) — Trust & Scale
- [ ] Anti-cheat monitoring
- [ ] Responsible gambling features
- [ ] Legal review per target markets
- [ ] Load testing

---

## Cost Estimate

> Costs at scale (~10,000 monthly active users, ~1,000 daily games)

| Service             | Plan                    | Est. Monthly Cost |
|---------------------|-------------------------|-------------------|
| Cloudflare Pages    | Free tier               | $0                |
| Cloudflare Workers  | Paid ($5/mo base)       | ~$5               |
| Cloudflare R2       | ~50GB storage           | ~$1               |
| Convex              | Pro (~1M function calls) | ~$25              |
| Clerk (Auth)        | Pro (10k MAU)           | ~$25              |
| Lemon Squeezy       | 5% + fees (per transaction) | Variable       |
| **Total (infra)**   |                         | **~$56/mo**       |

At 1,000 paid tournament entries/month at $10 each:
- Revenue: $10,000
- Lemon Squeezy fees: ~$550
- Platform rake (10%): +$1,000
- Infra cost: ~$56
- **Net margin**: very healthy

---

## Key Decisions Log

| Decision | Chosen | Alternatives | Reason |
|----------|--------|--------------|--------|
| Real-time DB | Convex | Supabase, Firebase | TypeScript-native, auto-reactive |
| Hosting | Cloudflare Pages | Vercel, Netlify | Edge network + Workers ecosystem |
| Payments | Lemon Squeezy | Stripe, Paddle | MoR (tax handled), simpler API |
| Auth | Clerk | Supabase Auth, Auth0 | Convex native integration |
| Frontend | React + Vite | Next.js, SvelteKit | JAMstack SPA fits real-time game |
| Monorepo | Turborepo | Nx, pnpm workspaces | Simple, fast |

---

## Useful Links

- [Convex Docs](https://docs.convex.dev)
- [Convex + Clerk Guide](https://docs.convex.dev/auth/clerk)
- [Cloudflare Pages Docs](https://developers.cloudflare.com/pages)
- [Cloudflare Workers Docs](https://developers.cloudflare.com/workers)
- [Lemon Squeezy Docs](https://docs.lemonsqueezy.com)
- [Lemon Squeezy Webhooks](https://docs.lemonsqueezy.com/help/webhooks)
- [Framer Motion](https://www.framer.com/motion)
- [Turborepo](https://turbo.build/repo)

---

*This document is a living blueprint. Update it as architectural decisions evolve.*
```

---

## How to use this file

1. **Save it** as `ANTIGRAVITY.md` in your monorepo root.
2. **Start with Sprint 1** — the game engine in pure TypeScript, no UI, fully testable. This is the hardest part of backgammon — get it right first.
3. **The Convex schema** above is your database contract. Share it with any backend collaborator and they'll know exactly what to build.
4. **Phase the tech** — you don't need Convex, Lemon Squeezy, or Workers on day one. The beauty of this stack is that each layer plugs in cleanly when you're ready.

The "antigravity" philosophy here is: **the stack absorbs the hard problems** (real-time sync → Convex, global CDN → Cloudflare, tax compliance → Lemon Squeezy) so you focus entirely on the game logic and user experience.