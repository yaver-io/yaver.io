# Spike + Plan — First-class Local Voice Helper + Device Nicknames (2026-06-01)

Owner thread: yaver.io audits/mobile. Branch suggestion: `feat/local-voice-helper`
(keep separate from `feat/device-pairing-onboarding` which holds Phase-1 pairing work).

> **Why this exists / strategic frame.** Prod data (2026-05) shows the activation
> funnel dies at "signed in → never paired → never ran the agent." The local voice
> helper is a **fully-offline, BYO-nothing first-class onboarding + troubleshooting
> guide** that works *before* a device is even paired, and the device nicknames make
> the voice router safe to act ("my hetzner box" resolves deterministically instead
> of whisper-tiny guessing). This is the maximal expression of the wedge
> ("open / BYOK-cheap / no-lock-in vs Expo's locked cloud") and the productivity
> surface the glasses/voice direction needs. See memories:
> [[project_local_voice_helper]] · [[project_device_nicknames]] ·
> [[project_normie_fullstack_phone_audit]] · [[project_voice_glasses_revival_2026_05_27]].

> **Scope decision (locked by kivanc):** local LLM is a **voice command-router /
> guide ONLY** — onboarding, troubleshooting (not-connected/reconnect/re-auth),
> device + runner control. **NO on-device coding** (that stays the paired-machine /
> cloud story). Target device floor: **iPhone 14 (6GB, A15)**.

---

## A. What already exists (verified) — build on it, don't duplicate

### Local speech I/O (mobile) — DONE and working, reuse verbatim
- **On-device STT:** `whisper.rn` v0.5.5 + bundled **`mobile/assets/models/ggml-whisper-tiny.bin` (31MB)**. Real-time streaming w/ live partials, manages mic itself. API: `mobile/src/lib/speech.ts:89 initWhisper()`, `:128 startRealtimeTranscribe(onPartial)→{stop()}`.
- **On-device TTS:** `expo-speech` → native iOS AVSpeech. `speech.ts:521 speakText(text,{provider:"device"})`. Free, offline.
- **Push-to-talk loop already wired:** `mobile/app/glass-terminal.tsx:233-290` (mic→partials→stop→submit→speak).
- **Keyword fast-path precedent:** saying "reload" bypasses the LLM and fires a direct reload (`glass-terminal.tsx:263`). **Our feature is the generalized version of this.**
- Config is SecureStore-local only, never Convex (privacy). `SPEECH_PROVIDERS` / `TTS_PROVIDERS` lists in `speech.ts:372/420`; "on-device" + "device" are the local picks.

### The action surface (Go agent) — DONE, phone-reachable
- **`GET /ops/verbs`** returns ~39 verbs *with JSON-Schema payloads* — a ready-made, machine-readable tool catalog. `desktop/agent/ops_http.go` (handleOpsVerbs). Dispatch via **`POST /ops {machine,verb,payload}`**; safe dry-run via **`POST /ops/plan`**.
- Verbs relevant to the helper: `status`, `info`, `reload`, `runner`, `runner_auth`, `run`, `build`, `deploy`, `test`, `logs`, `voice`, `secrets`, `files`. (Destructive ones to gate: `cloud_destroy`, `destroy`, `recycle`, `provision`, `scale`.)
- Machine routing already accepts `machine="primary"|"local"|<deviceId>|<alias>` and resolves via `resolveDeviceRef` (see nicknames below). Reachable from phone over HTTP(18080)/QUIC-relay/MCP. **No verb is CLI-only.**

### Mobile action layer — DONE, reuse as the helper's tool impls
From `DeviceContext.tsx` / `quic.ts` / `yaverMcpDirect.ts` / `yaverAgentTools.ts`:
- `recoverDeviceAuth(device)` — reconnect / fix expired auth (direct→device-code).
- `setPrimaryRunnerForDevice(deviceId, runnerId, model?, mode?, provider?)` — switch coding agent on a box.
- `setPrimaryDevice(id)` / `setSecondaryDevice(id)`.
- `selectDevice`, `disconnect`, `refreshDevices`, `claimPendingDevice(id)`.
- `callMcpDirect(tool,args)` + helpers (`callMobileHermesReload`, `callDeviceBroadcastCommand`).
- **`yaverAgentTools` registry** already has `device.list / resolve / audit / next_step` — the model's `resolve("my mac")→deviceId` step is *already written*; verify it covers alias map.
- Rich **read-only grounding state** in `DeviceContext` (synchronous, no RPC): `connectionStatus` (`disconnected|connecting|connected|error`), `lastError`, `agentAuthExpired`, per-device `needsAuth`/`peerState`/`online`/`lastSeen`, `unreachableDeviceIds`, `manualAuthRequiredDeviceIds`, `primaryDeviceId`/`secondaryDeviceId`, `connectedDeviceIds`.

### Device alias / nickname system — ALREADY COMPLETE on all 3 surfaces (CORRECTED)
**The earlier "web is the gap" claim was WRONG.** Deeper audit confirms the alias
system is end-to-end done. It is ONE concept: a per-device field `devices.alias`.
- **Convex:** `devices.alias: v.optional(v.string())` (schema.ts:234). Mutation
  `setDeviceAlias` (devices.ts ~1323) — validates `^[a-z0-9._-]{1,48}$`, per-user
  uniqueness, lowercases. Route **`POST /devices/alias`** (http.ts:2282; `alias:""`
  clears). Listed in `listMyDevices`. NOTE: device display `name` is set once at
  registration from hostname; there is **no separate `/devices/rename` route** (the
  alias IS the rename mechanism — verified `http_rename=0`).
- **Go CLI:** `yaver alias set/list/clear` → `runAlias` (main.go:7154); resolver is
  `main.go::resolveDevice` (~7102, alias→deviceId→name priority). NOTE: there is NO
  `device_resolve.go` file — it's in main.go.
- **Mobile:** `DeviceContext.setDeviceAlias(device, alias)` (:619) wired to
  `AliasRow` inline editor in `DeviceDetailsModal.tsx` (~156). Display: `@alias` else
  id[:8].
- **Web:** ✅ HAS IT — `DeviceAliasChip` inline editor in `web/app/dashboard/page.tsx`
  (~338-430) + `setDeviceAlias()` in `web/lib/use-devices.ts` (~415). Click-to-edit,
  uniqueness error shown, owner-only.
- **THE ACTUAL GAPS (only two):**
  1. **Auto-naming:** registration sets `name = os.Hostname()` only (main.go:2436;
     Convex registerDevice devices.ts:341). No smart label. Signals available:
     `platform`, `hardwareProfile` (incl. IsWSL), and the SEPARATE `cloudMachines`
     table (provider="hetzner", region, cloudResourceId, deviceId link). Note cloud
     metadata lives in `cloudMachines`, joined to `devices` via `cloudMachines.deviceId`
     — no reverse field on the device row yet.
  2. **Auto-seeded alias slug:** nothing auto-creates a memorable alias at first
     registration. This is the de-risker for voice resolution.

> ⚠️ Line numbers from the audit drift — **re-grep before editing.** The Bash tool in
> this session intermittently corrupts output; verify each edit with a fresh read.

---

## B. Build plan

### APPLE POLICY (resolved) — on-the-fly model download is ALLOWED
Guideline 2.5.2 forbids downloading *executable code* that changes app behavior. GGUF
weights are **data**, consumed by the `llama.rn` engine which is **compiled into the
binary at submission**. So: ship the engine, **download the weights at runtime**
(Wi-Fi-gated), activate per measured RAM. Same pattern as the bundled whisper model,
except downloaded to keep the App Store binary small. What you must NOT do: download a
new native inference engine / native code post-install. Runtime RAM/chip gating is a
pure local capability check — no Apple concern.

### TWO LOCAL-LLM TIERS, ONE ENGINE (llama.rn) — FINAL: router BUNDLED, coder ON-THE-FLY
**DECISION (FINAL 2026-06-01): bundle the router IF it's <1GB; download the coder.**
The router 1B Q4 lands ~0.7–1GB → bundle it so onboarding is instant + works fully
offline on first launch (same precedent as the bundled 31MB whisper-tiny). The coder 3B
is the big, opt-in, high-RAM one → download on-the-fly to keep the binary in check.
| Tier | Ships how | Device floor | Model | Job |
|---|---|---|---|---|
| Router/guide | **BUNDLED** (must stay <1GB; pick the smallest competent 1B Q4) | all (runs on iPhone 14 6GB/A15) | 1B Q4 (Llama-3.2-1B / Qwen2.5-1.5B) | voice onboarding-concierge, troubleshooting, device/runner control |
| Coder (opt-in) | **DOWNLOADED on-the-fly** (GitHub Releases) | 8GB+ Pro-class (15 Pro / 16 / 17) | 3B Q4 (Qwen2.5-Coder-3B or similar) | Mobile Sandbox full-stack codegen, monorepo-aware |
| Fallback | — | very low RAM / model load fails | none | keyword fast-paths + scripted guidance |
- **Router BUNDLED** → instant, offline, zero-wait onboarding from first launch. HARD
  CONSTRAINT: the chosen router GGUF must stay **<1GB**; if a candidate exceeds that,
  drop to a smaller quant / smaller model rather than bloating the binary. Allowed: no
  Wi-Fi install-size cap (only the irrelevant 200MB cellular-auto-download cap).
- **Coder DOWNLOADED** on-the-fly (it's big, opt-in, 8GB+ only) — keeps binary lean,
  Apple-compliant (weights=data, engine compiled in).
- Runtime picks the highest tier the device can survive (jetsam-safe); coder download
  gated on Wi-Fi + RAM/chip check; lazy-load + warm on use; unload on memory pressure.
- iPhone 14: router runs great (bundled); Coder tier unavailable → suggest pairing a
  machine for full-power codegen.

### MODEL HOSTING — GitHub Releases on kivanccakmak (for the DOWNLOADED coder tier)
The bundled router needs no hosting. The **coder tier (and any future downloaded models)**
is hosted as **GitHub Releases assets** under `kivanccakmak/yaver-models`, NOT in-repo
(Releases only, repo stays lean). Rationale: up to **2GB/file**, free CDN-backed
bandwidth, ~100% uptime — vs HuggingFace flakiness. App fetches a **manifest** (model id
→ release asset URL + sha256 + min-RAM tier), verifies sha256, resumable download, caches
to app-support. Gives the "%100 download success" the user wanted. NOTE: ML-weight DATA
hosting only — unrelated to CLAUDE.md "npm only" agent-binary rule (that's the yaver-cli
binary, not ML weights).

### TRACK 1 — Device nicknames everywhere + smart auto-labels (do FIRST; de-risks the voice router)
> CORRECTION: alias EDITING already exists on web+mobile+CLI. Track 1 is now ONLY
> (a) smart auto-label + (b) auto-seeded alias slug + (c) surface/suggest in UI.
> Drop the "build web alias UI" item — it exists (`DeviceAliasChip`).

**1a. Smart auto-label (pure function, run at registration).** Greenfield, trivial,
high-leverage. A pure labeler from existing signals:
```
provider=="hetzner"            -> "Hetzner box" (+region: "Hetzner box (hel1)")
provider set (other)           -> "<Provider> box"
platform=="macos"              -> hostname has "macbook"->"MacBook", "mini"->"Mac mini", else "Mac"
platform=="linux" (no provider)-> "Linux box"  (hardwareProfile raspberry -> "Raspberry Pi")
platform=="windows"            -> "Windows PC"
```
- Best home: **Convex `registerDevice`** (covers all surfaces at the source) — set
  `name` to the smart label *only if the agent didn't send a user-chosen name* and
  the current name is still a raw hostname. Privacy: label uses platform/provider/
  region only — NO absolute paths/usernames (respect `convex_privacy_test.go`).
- **Auto-seed an alias** at first registration to de-risk voice: derive a slug from
  the label (`"hetzner"`, `"mac"`, `"linux"`, `"pi"`, `"windows"`) and set
  `aliases[slug]=deviceId` IF that slug is free; on collision append `-2`, `-3`.
  This means "my hetzner box" / "switch the linux box" resolve deterministically
  on day one with zero user action.

**1b. ~~Web rename/alias UI~~ — ALREADY EXISTS (`DeviceAliasChip`). Skip.** Optionally
add an "accept suggested nickname" affordance there too, mirroring mobile 1c.

**1c. Mobile — surface it better.** `setDeviceAlias`/rename already exist in
`DeviceDetailsModal`; make the alias field discoverable (it's buried). Add an
"auto-suggested nickname" chip the user can one-tap accept.

**1d. CLI — already complete.** Optionally add `yaver alias suggest` that prints the
smart-label/slug suggestions. Low priority.

**1e. Client-side resolver for voice.** The voice router needs `resolve("my hetzner
box")→deviceId`. Prefer reusing `yaverAgentTools.device.resolve()` if it consults
the alias map; otherwise add a small mobile resolver mirroring the Go 6-step order
(alias map exact → name substring → fuzzy), fed by the live device list + a fetched
`GET /devices/aliases`. Fuzzy-match against REAL device/alias strings — never trust
the raw transcript.

### TRACK 2 — Local voice helper (the router model)

**2a. Runtime + model.** Add **`llama.rn`** (GGUF, RN New Arch, Metal — same family as
whisper.rn, so no new native toolchain). Model: **Qwen2.5-1.5B-Instruct Q4_K_M**
(native tool-calling) with **Llama-3.2-1B-Q4** as the lighter fallback. iPhone 14
budget: 1B Q4 ≈ 0.7–1GB RAM (jetsam ceiling ~3GB) @ ~15–25 tok/s Metal — safe.
- **Router BUNDLED** (<1GB) → instant/offline first-run, no download. **Coder DOWNLOADED**
  on-the-fly from **GitHub Releases** (kivanccakmak/yaver-models) via a manifest (id→asset
  URL + sha256 + min-RAM tier); verify sha256; resumable; cache to app-support dir; gate
  behind Wi-Fi + device-RAM/chip check. Lazy-load + warm on voice-tab open; unload under
  memory pressure.

**2b. Tool spec + hard constraints.** At connect, fetch `GET /ops/verbs` + merge the
`DeviceContext` action list → a static tool catalog. **Constrain decoding with a
llama.cpp GBNF / JSON-schema grammar** so the model can ONLY emit a valid
`{action, deviceRef, args}` — it literally cannot hallucinate a non-existent verb.

**2c. Safety tiers (the core risk control).**
- **Auto-exec (read-only / safe):** `status`, `info`, `device.audit`, `recoverDeviceAuth`,
  `setPrimaryRunnerForDevice`, `setPrimaryDevice`, `selectDevice`, `refreshDevices`,
  `reload`. These just run.
- **Voice-confirm (mutating/irreversible):** anything touching `cloud_destroy`,
  `destroy`, `removeDevice`, `recycle`, `provision`, `scale`, `run` (arbitrary shell),
  `deploy`. Model proposes → TTS reads back the resolved device + action → user says
  "yes" → execute. Default-deny on low ASR confidence or ambiguous device resolve.
- Never act on a device the user didn't clearly name when >1 device exists — ask.

**2d. Wire the loop.** Reuse existing: `startRealtimeTranscribe` → router model
(grammar-constrained) → resolve deviceRef against live list (Track 1e) → execute
safe / confirm mutating → `speakText` the result. Keep the existing keyword
fast-paths ("reload") as zero-latency shortcuts; model only runs on no-keyword-match.

**2e. Onboarding mode (first-class, THE focus) — triggers at initial mobile signup.**
The helper's concierge mode fires straight from **first-run / post-survey**
(`onboarding-pair.tsx`), not just later. When it detects **zero paired devices** it
proactively *guides by voice*: "Run `npm install -g yaver-cli && yaver auth` on your
computer — say 'done' when it's running and I'll check." Then polls device list /
beacon and confirms by voice ("Found your Mac — connecting"). Works before any
cloud/agent exists (1B model is local). This is the local helper as **onboarding
concierge for pairing the first remote dev box**, wired into Phase-1 onboarding-pair.
The 1B router model is enough for this — it's guidance + the pair/recover safe verbs.

### TRACK 3 — Mobile Sandbox prompt-engineering + monorepo (uses the Coder tier)
The Mobile Sandbox (phone-only, no machine) gets a **first-class in-app prompt layer**
so the on-device coder model produces grounded output, not blind text:
- **System prompt + context builder in-app:** assemble the model's context from the
  sandbox project — file tree, open file, and **monorepo awareness** (the Tasks
  empty-state already says the sandbox "expects a monorepo workspace"; honor
  `yaver.workspace.yaml` conventions — see [[project_switch_engine]]/workspace manifest
  in CLAUDE.md). Feed package/workspace layout so edits target the right package.
- **Prompt templates** for the common sandbox actions (scaffold, edit file, explain,
  fix) with the workspace context injected — clearly authored/visible in-app, not a
  hidden string.
- Runs on the **Coder tier (3B)** when the device qualifies; on a router-only device
  the sandbox coding assistant is unavailable (degrade gracefully, suggest pairing a
  machine for full-power codegen). Output constrained/validated before applying to the
  local SQLite-backed project.
- This is the on-device-coding path we earlier deferred — now scoped to the **Sandbox
  only**, opt-in, high-RAM only, monorepo-grounded.

**3b. Voice-driven FULLY-OFFLINE full-stack authoring loop (the headline sandbox UX).**
Close the loop end-to-end on-device: **speak → local whisper STT (`startRealtimeTranscribe`)
→ 3B Coder LLM (monorepo/workspace-grounded prompt from 3a) → write code into the local
SQLite-backed sandbox project → local TTS (`speakText`) narrates what changed.** No cloud,
no paired machine, no API key — full-stack vibe-coding a monorepo app entirely on the
phone. Reuses the exact same local STT/TTS the router uses (Track 2). Honest scope: this
is the **Coder tier (8GB+)**; on iPhone 14 the voice + guidance is instant/bundled but
full-stack codegen needs the high-RAM download (else: pair a machine). Apply-with-preview
before writing files; keep edits reversible in the sandbox.

---

## C. Suggested sequencing (smallest safe → biggest)
1. **Track 1a** smart auto-label + auto-seeded alias (Convex `registerDevice`, pure fn + tests). Highest leverage, lowest risk, useful even without voice. (Web/mobile/CLI alias editing already done — skip old 1b.)
2. **Track 1e** client resolver (verify mobile `yaverAgentTools.resolveTarget` at `yaverAgentTools.ts:128` consults the alias field first).
3. **Track 2a** add llama.rn + 1B model, on-the-fly download (Wi-Fi-gated) + RAM/chip capability tiering.
4. **Track 2b–2d** voice router behind a feature flag, safe-tier verbs only, GBNF-constrained, nickname-resolved.
5. **Track 2e** onboarding-concierge at signup, wired to `onboarding-pair.tsx` (THE focus).
6. **Track 3** Coder tier (3B) + Mobile Sandbox prompt/monorepo layer (opt-in, 8GB+ only) — last/biggest.

## D. Constraints (CLAUDE.md + memory)
- Never commit/push without explicit permission; verify `git branch` first.
- Convex privacy: labels/aliases use platform/provider/region only — no paths,
  usernames, IPs (`convex_privacy_test.go`). Aliases are arguably fine in Convex
  (they're slugs, already stored), but keep raw hostnames out if they leak usernames.
- No new icon libs in web (inline SVG). Mobile-only changes ship via
  `yaver wireless push`; TestFlight only on explicit ask.
- Ship tests/fixtures alongside (pure auto-label fn is easy to unit-test).
- Typecheck before "done": `cd mobile && npx tsc --noEmit`; `cd backend && npx convex` typegen; `cd desktop/agent && go build ./...`.
- Bash tool corruption observed this session — verify every edit with a fresh read.

## D2. Reuse the EXISTING edgeProfile capability infra (don't reinvent RAM checks)
The `devices` schema ALREADY has `edgeProfile { supportsLocalInference, maxModelClass:
none|tiny|small|medium, preferredTasks[], memoryMb, batteryPct, isCharging, thermalState }`
(schema.ts:249, also in registerDevice args). This is purpose-built for local-LLM
tiering — the phone self-reports capability, and the model-tier picker reads
`maxModelClass`/`memoryMb` instead of us inventing a RAM probe. Wire the voice-helper
tier selection (router vs coder vs fallback) off edgeProfile. The phone registers itself
as a device too (platform "ios", deviceClass "edge-mobile"), so it can carry its own
edgeProfile.

## TRACK 1a — DONE (2026-06-01)
- `backend/convex/deviceLabels.ts` — pure helpers: `smartDeviceLabel`, `smartAliasSlug`,
  `uniqueAliasSlug`, `isRawHostname` (+ `LabelSignals`). Privacy-safe (platform/provider/
  region/hostname-keywords only; raw hostname inspected not stored).
- `backend/convex/devices.ts::registerDevice` (insert branch only) — on first registration:
  replaces a RAW hostname with a smart label, auto-seeds a unique alias slug. Reads cloud
  provider/region by joining `cloudMachines` on `by_deviceId`. Best-effort (never blocks
  registration). Existing-device patch branch intentionally untouched (don't clobber a
  name/alias the user already set).
- `backend/convex/deviceLabels.test.mts` — 7 tests, all pass (`npx tsx convex/deviceLabels.test.mts`).
  Matches repo convention (node:test, like cloudMachines.test.mts; run via tsx).
- Typecheck: `cd backend && npx tsc --noEmit` → 0 errors. NOT deployed (needs `npx convex
  deploy` to take effect; existing rows unaffected — only NEW registrations get labels).

## E. Status at write time
- Audit complete + mostly verified (alias backend/CLI/mobile exist; web missing;
  auto-label greenfield; local STT/TTS + ops/verbs catalog all confirmed present).
- No code written for this spike yet. Phase-1 pairing work sits uncommitted on
  `feat/device-pairing-onboarding`.
- Next concrete step: Track 1a (Convex auto-label + auto-alias pure fn + unit test).
