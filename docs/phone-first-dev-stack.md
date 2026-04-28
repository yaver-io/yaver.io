# Phone-First Dev Stack

> **Vision (the user's framing).** A developer starts coding entirely
> on their phone. The phone is their dev environment: backend +
> frontend code + SQLite + AI runner sign-in, all on-device. The WIP
> code runs inside the Yaver mobile app the same way third-party
> apps do today. **That is Step 1.** **Step 2** is exporting the
> project off the phone — to the developer's own self-hosted PC, or
> to a cloud PC — and shipping it through the four canonical
> deploy targets: Convex, Cloudflare, TestFlight, Play Store. No
> intermediate "Yaver Cloud" host is required for the deploy itself;
> a hosted PC is required only for the iOS/Android compile step
> Apple and Google demand.
>
> This doc is the planning input. It catalogs what's already in the
> tree, calls out the hard physical constraints (no JIT on iOS, no
> hermesc on phone, store policies), and sequences the work so each
> slice ships value on its own.

## Step 1 — The Phone-Only Loop

The end-to-end flow we want a single phone to support:

```
┌────────────────────────── Yaver mobile app ────────────────────────┐
│                                                                    │
│  Editor      ──► AI assist  ──► File save                          │
│  (per-file   ──► (BYOK LLM)  ──► (project src/)                    │
│   text view)                                                       │
│                                  │                                 │
│                                  ▼                                 │
│  Live preview  ◄── JS runtime  ◄── Backend (phone-project SQLite)  │
│  (Hermes engine in Yaver app)      (already on-device)             │
│                                                                    │
└────────────────────────────────────────────────────────────────────┘
```

### What's already shipped (✅)

| Capability | File | Notes |
|---|---|---|
| Phone-side runner OAuth (Claude / Codex sign-in from device) | `mobile/src/components/RunnerAuthModal.tsx` | Browser flow + paste-back code, talks to `/runner-auth/browser/*` on agent. Today still needs an agent endpoint to drive it. |
| On-device backend sandbox | `mobile/src/lib/phoneSandboxLocal.ts` | Full SQLite schema + seed + CRUD via `expo-sqlite`. |
| Project manifest (schema/auth/seed/app) | `mobile/src/lib/phoneProjects.ts` | Modeled, persisted, edited from `mobile/app/phone-project/[slug].tsx`. |
| Bundle loader (load HBC into Yaver's RCT bridge) | `mobile/src/lib/bundleLoader.ts`, `mobile/src/components/DevPreview.tsx` | Today loads third-party Hermes bundles compiled by an agent. |
| Project store TS contract (Slice D, just landed) | `mobile/src/lib/projectStore.ts`, `projectStoreSandbox.ts` | `agentStore`, `phoneSandboxStore`, `pullFromAgent`. |
| Cost guardrails for export | `desktop/agent/phone_cost.go` + `mobile/app/phone-projects.tsx` confirm | 50 MB cap, pre-flight cost hint. |

### What's missing for Step 1 (❌)

| Gap | Why it blocks the vision | First-cut shape |
|---|---|---|
| **Code editor surface inside the mobile app** | Without it, there is nothing to edit on the phone. `DevPreview` has no input source. | A `mobile/app/phone-project/[slug]/code/` screen with a `<TextInput multiline>` per file. Syntax highlighting can come later. |
| **Source-tree storage on the phone** | `phone-projects/<slug>/` today holds schema/auth/seed/SQLite; no `src/` tree of JS/TS. | Add a `src/` AsyncStorage namespace per slug, mirrored to disk via `expo-file-system`. Files keyed by relative path. |
| **Phone-side AI runner** that can patch the source without a desktop agent | The user explicitly wants Step 1 to work with **only** a phone. Today every coding task has to ride through the agent. | Direct calls to Anthropic / OpenAI / Codex Cloud APIs from the mobile app, BYOK. Source files + a structured "edit plan" round-trip; the phone applies the patch locally. The agent stays optional. |
| **Phone-runs-the-code without re-bundling on a desktop** | `loadApp()` today loads HBC produced by `hermesc` on a developer machine. hermesc is a native binary that does **not** run on iOS / Android (no JIT in user space on iOS, no shipping binary on Play). | Two options. (a) **Source mode**: load uncompiled JS straight into the existing Hermes runtime — Hermes can interpret ES bytecode-free, slow but works for dev. (b) **Phone-to-self compile**: only viable on Android via a side-loaded NDK build of hermesc. (a) is the realistic Step-1 path. |
| **Live preview against the phone-project SQLite** | The dev's frontend code wants to hit `/data/<slug>/<table>` like a real backend. Today that endpoint is on the agent. | Mount a tiny on-device HTTP server on `localhost:<random>` inside the Yaver app that proxies into `phoneSandboxLocal.ts`. The live JS bundle hits `http://localhost:N/data/...` exactly as if the agent were there. |
| **Per-project token vault on the phone** | LLM API keys and OAuth tokens for Claude / Codex must live somewhere safe. | Reuse `expo-secure-store` (already a shim in mobile-headless) and key on `(slug, provider)`. Never written to AsyncStorage. |

### Hard physical constraints (and how we live with them)

- **iOS: no JIT in user-space apps.** Hermes is fine — it runs as a
  bytecode interpreter, not a JIT — but third-party JS engines that
  depend on JIT (V8, original JavaScriptCore JIT) cannot ship in
  App Store apps. Source-mode dev preview must use Hermes' interp
  path or be served via WebView (which gets JavaScriptCore-with-JIT
  inside the system WebView).
- **iOS: no executing arbitrary downloaded code that's not in your
  bundle.** Apple Guideline 4.7 carves out an exception for **code
  the user has authored** loaded via standard JS engines. That's
  exactly our case for Step 1 — the user is the developer, the code
  is the developer's own. Bundle loading via `loadApp()` falls under
  this carve-out today; source-mode loading does too.
- **Both iOS and Android: no `hermesc` on phone.** The Hermes
  compiler is a native CLI binary, distributed with React Native and
  the Yaver agent's `cli/hermesc/`. It does not have a phone build.
  Step 1 sidesteps this by running source-mode JS; HBC compilation
  happens on the agent in Step 2.
- **iOS: background limits.** Yaver mobile must be foreground for
  AI-driven edits to make progress. Long edits will pause when the
  app backgrounds. Acceptable for an interactive workflow.

## Step 2 — Export and Deploy

The user defined Step 2 narrowly: **export goes through the four
canonical deploy scripts**.

| Target | Script | Host requirement | What it ships |
|---|---|---|---|
| Convex | `cd backend && npx convex deploy --yes` | any Linux/macOS with Node | backend functions, schema, HTTP actions, real-time subs |
| Cloudflare Workers | `scripts/deploy-web.sh` (uses `wrangler` + `@opennextjs/cloudflare`) | any Linux/macOS with Node + Cloudflare API token | web frontend, Workers, D1 |
| TestFlight | `scripts/deploy-testflight.sh` | **macOS with Xcode** + App Store Connect API key | iOS native app for beta testers |
| Play Store | `scripts/deploy-playstore.sh` + `scripts/upload-playstore.py` | Linux/macOS with **Java 17** + keystore + Play service-account JSON | Android AAB to internal testing |

So the "hosted PC" the user mentioned is **whichever machine the
deploy script can run on**:

- Convex + Cloudflare deploys can run from any always-on Linux box
  (a `$5/mo` VPS or the developer's own desktop).
- TestFlight requires macOS — the developer's own Mac, or a hosted
  Mac (MacStadium, GitHub Actions `macos-latest`, EAS Build).
- Play Store requires Java 17 — the developer's own Linux/Mac, or a
  GitHub Actions `ubuntu-latest` runner.

The phone never runs these scripts itself. The phone says "deploy",
and a hosted PC executes the right script. The hosted PC's address
is the only thing the phone has to know.

### The export wire format

What the phone hands off to the hosted PC is the **same `Project`
shape from `desktop/agent/projectstore.go`** plus a new field:
`SourceFiles map[string][]byte` (relative path → contents). The
existing `ExportPhoneProjectWithOptions` tarball already carries
schema/auth/seed/app/oauth/Dockerfile; we extend it with `src/`.

```
my-project/
├── .yaver/
│   ├── project.yaml                 ← already exported
│   ├── schema.yaml                  ← already exported
│   ├── auth.yaml                    ← already exported
│   ├── seed.json                    ← already exported
│   └── app.yaml                     ← already exported
├── src/                             ← NEW — phone-authored source
│   ├── App.tsx
│   ├── screens/Home.tsx
│   └── ...
├── package.json                     ← NEW — generated from app spec
├── ios/                             ← prebuild output, regenerated on Mac
├── android/                         ← prebuild output, regenerated on Linux/Mac
└── README.md                        ← already exported
```

The phone exports `.yaver/` + `src/` + `package.json`. Native
`ios/` and `android/` are regenerated by `npx expo prebuild` on the
hosted PC right before the deploy script runs — that's where Apple
and Google's compilation requirements get satisfied.

### Step 2 control flow

```
┌─────────────────────────── Yaver mobile app ──────────────────────┐
│                                                                   │
│  Tap "Deploy" → choose target → choose host                       │
│                                                                   │
│  POST /phone/projects/receive   (existing)                        │
│       on the hosted PC                                            │
│       payload: .yaver/ + src/ + package.json                      │
│                                                                   │
└──────────────────────────────────────────┬────────────────────────┘
                                           │
                                           ▼
┌──────────────────────── Hosted PC (agent) ────────────────────────┐
│                                                                   │
│  ImportPhoneProject (existing) writes the tarball to disk.        │
│  NEW: project type detection on import.                           │
│        is there a src/ ?            → it's a full RN/Expo project │
│        is there only schema/auth/   → it's a backend-only project │
│                                                                   │
│  Run the chosen deploy script:                                    │
│    target=convex      → npx convex deploy --yes                   │
│    target=cloudflare  → ./scripts/deploy-web.sh                   │
│    target=testflight  → ./scripts/deploy-testflight.sh   (Mac)    │
│    target=playstore   → ./scripts/deploy-playstore.sh    (Linux)  │
│                                                                   │
│  Stream stdout back to the phone via the existing                 │
│  /dev/events SSE pipe.                                            │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

The agent already exposes `/dev/events` SSE (see `desktop/agent/
devserver_http.go` — same endpoint we just added timeouts to). The
deploy run reuses that channel; the mobile app already knows how to
read it (`mobile/src/lib/quic.ts::subscribeDevEvents`).

### What's already shipped for Step 2 (✅)

- `mobile/src/lib/phoneProjects.ts::pushPhoneProject` — phone → agent.
- `desktop/agent/phone_backend.go::ImportPhoneProject` — agent receive.
- `scripts/deploy-web.sh`, `scripts/deploy-testflight.sh`,
  `scripts/deploy-playstore.sh`, `scripts/upload-playstore.py` —
  the four canonical scripts (TestFlight currently has a known
  `react-native-udp` API mismatch that blocks archive — pre-existing,
  fixed separately).
- `desktop/agent/code_phone_control.go::runCodePhonePush` (just
  landed) — `yaver code phone push <slug> --to <target>` with
  `dev-hw` / `yaver-cloud` aliases.

### What's missing for Step 2 (❌)

| Gap | Why it matters | First-cut shape |
|---|---|---|
| **`src/` tree in the export tarball** | Today only schema/auth/seed/app travel; the phone-authored frontend stays on the phone. | Extend `ExportPhoneProjectWithOptions` to walk `<phone-project>/src/` and add each file with a stable path under `src/`. Symmetric extension on `ImportPhoneProject`. |
| **Generate `package.json` from project metadata on push** | The hosted PC needs `expo`, `react-native`, plus any deps the developer's source pulls in. The phone manifest doesn't know. | Build a minimal `package.json` from the project's `app` spec at export time. AI-runner can suggest extras; user reviews before push. |
| **Project type detection on the agent** | `ImportPhoneProject` today assumes a phone-project (backend only). When the bundle has `src/`, the agent should treat it as a full RN/Expo project. | Add a `ProjectKind` field to the bundle's manifest: `{phone-backend, expo-app, web-app}`. Switch on it during import. |
| **Deploy verb in the agent's HTTP API** | Today an agent can be told "import this", but not "import then deploy to TestFlight". Without it, the phone has to know how to invoke shell scripts on the host. | New `POST /phone/projects/deploy` handler that takes `{slug, target: convex|cloudflare|testflight|playstore, hostBaseUrl}` and runs the matching script in-process, streaming events. |
| **Mobile UI for the four deploy targets** | `phone-projects.tsx` today shows two options: `[Your Dev Machine]` and `[Yaver Cloud]`. The four-target flow has no UI. | Replace the two-button section with a target picker: Convex / Cloudflare / TestFlight / Play Store, each with a "host" sub-picker (Mac on Tailscale / Linux box / GH Actions). |
| **Convex deploy automation** | `npx convex deploy` exists but the phone-side flow doesn't wire to it. The auth (CONVEX_DEPLOY_KEY) needs a per-host config, not committed. | Add a `phone_deploy_convex.go` that resolves the key from the host's vault (`yaver vault add CONVEX_DEPLOY_KEY --project mobile`) and invokes `npx convex deploy` in the imported project's `backend/`. |
| **Hosted-PC selection / discovery** | Today the phone picks targets by URL. With four deploy targets each potentially needing a different host, the picker has to model the (target × host) matrix. | Extend `mobile/src/context/DeviceContext.tsx` to surface devices with capability flags (`canDeployTestflight`, `canDeployPlaystore`, etc.) sourced from `/info` on each agent. The flags come from probing for `xcodebuild` / Java 17 / wrangler / convex on agent startup. |
| **TestFlight's `react-native-udp` build break** | Pre-existing, blocks any iOS deploy. Caught today as exit-65 archive failure. | Patch `mobile/ios/Yaver/UdpSockets.m:122/131` so the type matches the framework's `(NSString *)group` signature. Separate fix from this plan. |

## What `yaver code` Already Knows (and Doesn't)

`yaver code phone {status,push}` (just landed in `b43a78e0`) covers
the agent → cloud push direction. For phone-first, the verbs to
add are:

- `yaver code phone deploy <slug> --target convex|cloudflare|testflight|playstore`
- `yaver code phone source pull <slug>` — pulls `src/` from a hosted
  agent into the phone sandbox (companion to the existing
  `pullFromAgent`)
- `yaver code phone source push <slug>` — pushes phone-edited `src/`
  to a hosted agent for compile + deploy

The slash-palette (`/phone status`) inside an interactive `code`
session is already wired; adding `/phone deploy ...` is a small
extension.

## The "Headless OAuth Without a Remote PC" Piece

The user specifically called this out. Two parts:

1. **Claude / Codex sign-in from the phone** — `RunnerAuthModal.tsx`
   already implements the full browser-flow + paste-back-code
   exchange. Today it talks to an agent's `/runner-auth/browser/*`
   endpoints to drive the OAuth handshake.

2. **Removing the agent dependency for OAuth** — to truly work
   without any PC, the OAuth handshake itself must happen on the
   phone. Anthropic and OpenAI both expose web sign-in flows with a
   redirect URI and a paste-back code. The phone can host the
   redirect via a custom URL scheme (`yaver://oauth/runner-auth`)
   and complete the exchange directly with the provider. The
   resulting OAuth tokens land in `expo-secure-store`, scoped per
   provider per project. No agent needed.

   That's the missing piece. Today the agent mediates; the phone
   needs to mediate too. Adding `mobile/src/lib/runnerAuthDirect.ts`
   that mirrors the agent's `runner_auth_browser_http.go` flow but
   runs entirely on-device closes this gap.

## Sequencing — Five Slices to Get to the Vision

Each slice ships value alone. Each is rebaseable on top of what we
already landed (`5c705af1`).

### Slice 1 — On-device source storage

Persist a `src/` tree per phone project. New `mobile/src/lib/
phoneSandboxSource.ts` with `listSourceFiles / readSourceFile /
writeSourceFile / deleteSourceFile`, backed by `expo-file-system`
under the existing project root. Headless tests in `mobile-
headless/` against an in-memory shim.

Effort: 1 day.

### Slice 2 — Code editor screen

`mobile/app/phone-project/[slug]/code.tsx`. File-tree on the left,
text editor on the right. `<TextInput multiline>` is enough to
ship — syntax highlighting can come later (`react-native-syntax-
highlighter` if we want, but it's optional).

Effort: 1-2 days.

### Slice 3 — Phone-side direct LLM client

`mobile/src/lib/llmClient.ts` with `editFiles({ files, prompt })`
that calls Anthropic's Messages API or OpenAI's Responses API
directly with a BYO key. Returns a structured edit plan; the phone
applies it via Slice 1's writes.

Effort: 2 days. Headless tests against a mock LLM endpoint.

### Slice 4 — Source-mode dev preview

Skip Hermes BC compilation for dev. Drop the source files into the
existing Hermes runtime via `loadApp()` after a small Metro-on-
phone bundling pass (Metro-lite — concat + AST-light import
resolution). Slow but works.

Alternative: WebView-based preview for web/Vite-class projects
where the source is already JS that a browser can run.

Effort: 3-5 days. The hardest slice. Probably ships the WebView
path first, defers Hermes-source-mode behind a feature flag.

### Slice 5 — Step 2 deploy plumbing

- Extend `ExportPhoneProjectWithOptions` to bundle `src/` +
  `package.json`.
- New `POST /phone/projects/deploy` on the agent.
- `yaver code phone deploy ... --target ...` CLI verb.
- Mobile UI: target picker + host picker.

Effort: 2-3 days. Reuses every existing piece (push, receive, run
script, stream events).

---

## Definition of Done

The phone-first vision is "perfect" when:

1. A developer with **only** an iPhone can:
   - sign into Claude / Codex from the phone
   - create a phone project (schema + auth + seed)
   - write `src/App.tsx` and a couple of screens via the editor + AI assist
   - tap "Run" and see the app live inside the Yaver mobile app,
     hitting the on-device SQLite sandbox as its backend
2. The same developer can:
   - tap "Deploy"
   - pick TestFlight (or Play Store, or Cloudflare, or Convex)
   - pick a host (their Mac on Tailscale, a Hetzner Linux box,
     or a GitHub Actions runner triggered via PAT)
   - watch the streaming log until the deploy script completes
   - launch the app from the App Store via TestFlight invitation
3. Every secret stays on-device (LLM keys, OAuth tokens) or in the
   target host's vault (TestFlight key, Play service-account JSON).
   Nothing committed to git, nothing in Convex.
4. The legacy `yaver phone push` and `yaver code phone push` paths
   keep working — phone-first is additive, not destructive.

When all four hold, a developer's phone is a full coding workstation
and the four canonical clouds become its build farm.
