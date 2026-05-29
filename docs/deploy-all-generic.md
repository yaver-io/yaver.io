# Generic, monorepo-aware `yaver deploy all` — design

Status: **proposal / design-first** (no code yet beyond the yaver.io-only
version-bump preflight already shipped in `deploy_version_bump.go`).
Owner thread: yaver.io + CLI.
Date: 2026-05-30.

## Problem

`yaver deploy all` ships the **whole Yaver stack in one command** —
TestFlight → Play → Convex → Cloudflare → npm. It is hard-locked to the
yaver.io repo:

- `findYaverRepoRoot()` (`deploy_all_cmd.go`) aborts unless
  `scripts/deploy-web.sh` exists at the repo root — *"doesn't look like
  yaver.io"*.
- Every stage shells out to yaver.io's own `scripts/deploy-*.sh`.
- The version bump assumes yaver.io's `versions.json` schema (keys
  `cli/mobile/backend/web/...`) and `scripts/sync-versions.sh`.
- The npm stage assumes a `cli/` package + `cli/v*` tag + `release-cli.yml`.

Kıvanç runs the same `tmux`/multi-repo workflow across **carrotbet, talos,
sfmg, …**. He wants one `yaver deploy all` that works in any of them, that
**greps the repo to understand its stack** (React/Vite, Next, Supabase,
Convex, Cloudflare, mobile iOS/Android, Flutter, …), **finds every version
site and always increments it**, and is **optimized for monorepos** (many
apps, each with its own stack + version).

### What the repos actually look like (today)

| Repo | `versions.json` | `yaver.workspace.yaml` | `.yaver/` | own `scripts/deploy-*.sh` | layout |
|---|---|---|---|---|---|
| yaver.io | ✅ (7 keys) | ✅ | — | testflight, playstore, web, … | `backend/ web/ mobile/ cli/ relay/` |
| carrotbet | ❌ | ✅ | config/project/services.yaml | cloudflare, convex, testflight, full | root `convex/`, `mobile/`, `apps/web`, `packages/*` |
| talos | ❌ | ❌ | — | cloudflare, playstore, testflight, migration | `cli/ web/ mobile/` |

Three different shapes. None of the non-yaver.io repos has `versions.json`.
Two of three already carry their own `scripts/deploy-*.sh`. So the generic
command must **discover**, not assume.

## Decisions (locked with the user)

1. **`deploy all` itself is deterministic — no LLM at deploy time.** It only
   ever does `+patch` (or `--bump minor|major`) arithmetic on whatever
   version it finds. An LLM never picks a version number.
2. **The "understanding" (LLM) lives in a separate setup-time command, not in
   `deploy all`.** That command is the existing **`yaver autoinit`** (and the
   deterministic **`yaver workspace scaffold`**). `deploy all` *consumes*
   their output; it does not call a model.
3. **Auto-detect + cache.** `deploy all` resolves a **deploy plan** by
   deterministic detection and writes it to **`.yaver/deploy-plan.json`** for
   review/reuse. `--refresh` re-detects; editing the file overrides.
4. **Optimized for monorepos.** The plan is a list of apps×targets ordered by
   `yaver.workspace.yaml` `depends`.

## Architecture

```
                 setup time (occasional, may use LLM)
  ┌──────────────────────────────────────────────────────────┐
  │ yaver workspace scaffold   → yaver.workspace.yaml (deterministic) │
  │ yaver autoinit <app>       → init.md (LLM, interactive TUI)       │
  │ yaver deploy collect       → vault creds (kolay/easy Apple etc.)  │
  └──────────────────────────────────────────────────────────┘
                              │  (artifacts on disk + vault)
                              ▼
                 deploy time (every ship, NO LLM, deterministic)
  ┌──────────────────────────────────────────────────────────┐
  │ yaver deploy all                                                  │
  │   1. resolvePlan()  → .yaver/deploy-plan.json  (detect or cached) │
  │   2. bumpVersions() → +patch every version site in the plan       │
  │   3. preflight      → vault/toolchain check per target            │
  │   4. execute        → repo's own scripts/ OR built-in templates   │
  │                       in `depends` order                          │
  └──────────────────────────────────────────────────────────┘
```

The LLM is strictly a **setup-time understanding layer** (`autoinit`), which
already exists, runs interactively (never `-p`/headless — see
`ai_generator.go` + `feedback_no_headless_p_mode`), and writes `init.md`. It
can be taught to also emit/enrich `.yaver/deploy-plan.json`, but `deploy all`
must run correctly with zero LLM involvement from the deterministic detector
alone.

## 1. Detection — the deterministic "grep layer"

A new `deploy_detect.go` produces a `DeployPlan` from the repo. Inputs, in
priority order:

1. **`yaver.workspace.yaml`** if present — authoritative app list
   (`name/path/stack/provider/depends/scripts/env`). carrotbet + yaver.io
   have this. Reuses `workspace.go` parsing.
2. **`yaver workspace scaffold` detection** for repos without a manifest
   (talos): one-level scan + `detectWorkspaceApp()` markers
   (`app.json`→expo, `pubspec.yaml`→flutter, `next.config.*`→nextjs,
   `convex.json`/`convex/`→convex, `Cargo.toml`→rust, `go.mod`→go,
   `package.json`→node).
3. **Content greps** to map a stack → deploy targets and to confirm tooling:

   | Signal (grep / file) | Inference |
   |---|---|
   | `dependencies.next` / `next.config.*` | nextjs web |
   | `dependencies.vite` + `react` | react-vite web |
   | `@supabase/supabase-js`, `supabase/config.toml` | supabase backend |
   | `convex/` dir, `convex.json`, `dependencies.convex` | convex backend |
   | `wrangler.toml`, `@cloudflare/*`, `scripts.deploy ~= wrangler` | cloudflare target |
   | `app.json`+`ios/` / `dependencies.expo` | react-native-expo → testflight |
   | `app.json`+`android/` | react-native-expo → playstore |
   | `pubspec.yaml` | flutter → testflight/playstore |
   | `cli/package.json` + `bin` | npm publish target |
   | `README.md` "Deploy"/"Hosting" section | hints only (logged, never authoritative) |
   | `scripts/deploy-<target>.sh` | **use the repo's own script for that target** |

   README is parsed for *hints only* and surfaced in the plan as
   `notes[]`; it never silently drives a deploy.

The detector emits **what it is sure about** and lists **ambiguities** as
`needsReview[]` rather than guessing. `deploy all` refuses to ship a target
in `needsReview` unless `--accept-detected` is passed or the user edits the
plan. (No silent caps — per `feedback_visible_failure_over_silent_retry`.)

## 2. Version-site discovery + deterministic bump

Generalize the yaver.io-only `bumpMonorepoVersions` into a config-driven
bumper. Each plan app carries a `version` block listing its sites:

- **yaver.io model**: a repo-level `versions.json` + a sync step. Keep
  `scripts/sync-versions.sh` as the propagation engine when `versions.json`
  exists (already fixed to include `mobile/package.json`).
- **No-`versions.json` model** (carrotbet/talos): per-app sites discovered
  directly:
  - mobile: `app.json` `expo.version`, `ios/**/Info.plist`
    `CFBundleShortVersionString`, `*.xcodeproj/project.pbxproj`
    `MARKETING_VERSION` (×N), `android/app/build.gradle` `versionName`,
    `package.json` `version`.
  - web/node: `package.json` `version`.
  - flutter: `pubspec.yaml` `version`.
  - rust: `Cargo.toml` `[package].version`.
  - build numbers (`CFBundleVersion`/`versionCode`) stay owned by the deploy
    scripts (monotonic, remote-max aware) — unchanged.

Bump rule: read current → `+patch` (or `--bump`) → write **all** sites for
that app atomically; abort if sites disagree before the bump (drift guard —
exactly the `mobile/package.json` 1.18.126 drift we just fixed). The
resolved site list is cached in the plan so the second run doesn't re-grep.

## 3. Execution

For each target, prefer in this order:
1. The repo's **own** `scripts/deploy-<target>.sh` (carrotbet/talos/yaver.io
   all have these) — run via the existing `runScript` streamer.
2. A workspace app `scripts.deploy` command.
3. A **built-in template** from `deploy_script_gen.go` (the 5 vault-aware
   `(stack,target)` templates) when the repo has none.

Order by `yaver.workspace.yaml` `depends` (backend before web/mobile). Same
fail-loud ordering rationale as today (most-flaky/most-expensive first).
`--dry-run`, `--skip-<target>`, `--continue-on-error` carry over.

## 4. Credentials — "kolay" (easy) collect

Reuse the existing `DeployTokenCatalogue()` (`deploy_tokens.go`): each target
already declares its required vault keys + `GenerateURL` deep links +
`CanVerify`. Add a setup-time **`yaver deploy collect`** that, for the
targets in the plan, walks the catalogue: opens the dashboard deep link,
prompts (masked — per `project_vault_prompt_echo_bug`), stores into vault,
and verifies where possible. This is where "easy Apple collect" lives —
App Store Connect needs 4 values (key `.p8`, key id, issuer, team id); the
flow links straight to `appstoreconnect.apple.com/access/api` and stores them
under project `mobile`. Deploy-time only *reads* vault; it never collects.

## `.yaver/deploy-plan.json` schema (cache)

```jsonc
{
  "schemaVersion": 1,
  "repo": "carrotbet",
  "generatedBy": "detect",        // "detect" | "autoinit" | "hand-edited"
  "bump": "patch",
  "apps": [
    {
      "name": "convex", "path": "./convex", "stack": "convex",
      "target": "convex", "script": "scripts/deploy-convex.sh",
      "depends": [],
      "version": { "sites": [] }   // convex has no marketing version
    },
    {
      "name": "web", "path": "./apps/web", "stack": "react-vite",
      "target": "cloudflare", "script": "scripts/deploy-cloudflare.sh",
      "depends": ["convex"],
      "version": { "sites": [
        { "file": "apps/web/package.json", "kind": "json", "key": "version" }
      ]}
    },
    {
      "name": "mobile", "path": "./mobile", "stack": "react-native-expo",
      "target": ["testflight"], "script": "scripts/deploy-testflight.sh",
      "depends": ["convex"],
      "version": { "sites": [
        { "file": "mobile/app.json",        "kind": "json",  "key": "expo.version" },
        { "file": "mobile/ios/**/Info.plist","kind": "plist", "key": "CFBundleShortVersionString" },
        { "file": "mobile/ios/*.xcodeproj/project.pbxproj", "kind": "pbxproj", "key": "MARKETING_VERSION" }
      ]}
    }
  ],
  "needsReview": [],
  "notes": ["README mentions Fly.io — not wired; ignored."]
}
```

## Phasing

- **P0 (already shipped, yaver.io-only):** deterministic `versions.json` bump
  preflight + `sync-versions.sh` `mobile/package.json` fix.
- **P1 — unlock + detect:** drop the `deploy-web.sh` gate; add `deploy_detect.go`
  + `.yaver/deploy-plan.json`; run each repo's own `scripts/deploy-*.sh`.
  Generic per-app version-site bump (no `versions.json` required). Target:
  carrotbet + talos ship via `yaver deploy all`.
- **P2 — collect + templates:** `yaver deploy collect` (vault, easy Apple);
  built-in template fallback for repos with no scripts.
- **P3 — autoinit enrichment:** teach `yaver autoinit` to write/refresh
  `.yaver/deploy-plan.json` (the LLM understanding layer), still
  deterministic at deploy time.

## Non-goals / guardrails

- No LLM at deploy time. No headless runner anywhere (`-p` forbidden).
- No secrets in the plan file or any tracked file — vault only.
- Build-number monotonicity stays in the deploy scripts (remote-max aware).
- yaver.io's existing behavior is preserved: its plan resolves to the same
  `versions.json` + `scripts/*.sh` it uses today.
- Per-repo ownership respected: this changes the **CLI tool**, not the
  contents of carrotbet/talos (other threads own those repos).
```
