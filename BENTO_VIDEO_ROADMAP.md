# Bento Video Shoot — Implementation Roadmap

Status as of today: gaps 1–4 from the capability audit are landed (commits `27e6ba2c`, `c9c2d1f3`). Gap 5 needs wiring, plus the Bento app itself has to exist. This doc is the pick-up list.

## What's already shipped for the videos

| Gap | What | Where |
|---|---|---|
| 1 | Unified project screen — `[💬 Chat]` + `[🗄️ Dashboard]` + `[🚀 Deploy]` + `[📸 Snapshot]` + `[🗄️ Data]` | `mobile/app/(tabs)/project.tsx` |
| 1 | Project-scoped chat (tasks screen now reads `?dir=…` and forwards `workDir`) | `mobile/app/(tabs)/tasks.tsx`, `mobile/src/lib/quic.ts` |
| 2 | `GenerateProject` auto-boots the local backend (`ServicesManager.Start()`) | `desktop/agent/project_wizard.go` |
| 3 | Real Convex / Supabase / Drizzle / PocketBase / Mailpit dashboard in a WebView tunnelled via `/proxy/{id}/*` | `mobile/app/dashboard-view.tsx` |
| 4 | Auto hot-reload on task completion (`devServerMgr.Reload()` + `blackboxMgr.BroadcastCommand("reload")`) | `desktop/agent/main.go` (`OnTaskDone` handler) |

## What Video 3 still needs

Video 3 ("Auto Test") shows an agent autonomously driving Bento running inside Yaver, detecting 2 crashes, patching both, hot-reloading, and producing a report. The testkit package already has most of what's required — what's missing is the glue + the target app.

### Building blocks that already exist
- `desktop/agent/testkit/driver_wda.go` — WebDriverAgent client (iOS automation)
- `desktop/agent/testkit/driver_device.go` — physical-device driver (uses simctl/ideviceinstaller/adb)
- `desktop/agent/testkit/driver_androidemu.go` + `android_uiautomator.go` — Android driver
- `desktop/agent/testkit/autonomous.go` — autofix dispatch (spec-fail → LLM patch → rerun)
- `desktop/agent/testkit/selfheal.go` — stale-selector rescue
- `desktop/agent/testkit/spec.go` — YAML spec format (steps, selectors)
- `desktop/agent/blackbox.go` — already streams crashes & navigation from a running RN bundle

### Missing wiring — the "Video 3 MVP" task list

1. **Accessibility labels on the Yaver shell** — the host chrome (tab bar, floating button, debug console) needs stable `testID` / `accessibilityLabel` props so WDA can find them. Spread across `mobile/app/(tabs)/_layout.tsx` and `mobile/src/components/FeedbackOverlay.tsx`. ~4 hours.

2. **`yaver autotest bento`** command — a thin wrapper in `desktop/agent/autodev_cmd.go` (or a new `bento_autotest_cmd.go`) that:
   1. Calls `yaver-cli push` to load the guest bundle.
   2. Launches Yaver on the target device via the device driver.
   3. Waits for BlackBox session to register.
   4. Executes a scenario YAML (list of screen visits).
   5. On crash: creates a `Task` with the BlackBox context (auto hot-reload on completion already wired in gap 4).
   6. Waits for reload event on BlackBox, resumes scenario.
   7. Emits a JSON report via the existing reporter in `testkit/reporter.go`.
   ~1–2 days.

3. **Scenario spec for Bento** — a YAML that walks every tab and recipe. Lives in the Bento repo, not here.
   ~2 hours once Bento exists.

4. **Mobile UI to show the fix report** — a screen that reads `/autotest/runs/:id` and renders the diff + `[Accept All] [Review Diff] [Revert All]` buttons. Can be a new stack route `mobile/app/autotest-report.tsx`. ~half a day.

5. **HTTP endpoint** — `POST /autotest/run`, `GET /autotest/runs`, `GET /autotest/runs/:id`. Thin wrapper over the existing `testkit` runner. ~2 hours.

**Total for a scripted-scenario MVP**: ~3–4 days of focused work. This is the version we shoot.

### The fully-autonomous version (post-video)

The audit estimated 6–8 weeks for a true "agent plans the next tap from the accessibility tree and observed state." That's still accurate for the *planner*. The pieces:

- A BlackBox event type that streams the current React Navigation stack + focused screen's accessibility tree, debounced.
- An LLM loop that takes `{lastAction, newTree, goalList}` and picks the next tap. Same interface as `testkit/autonomous.go` but for navigation instead of patches.
- Coverage tracking so the planner knows which screens are unvisited.
- A budget (max 100 taps per run).

Nothing blocks the video if we ship the scripted MVP first.

## Building Bento itself

Separate repo (suggested `github.com/kivanccakmak/bento-demo`, or a `demos/bento/` subdir kept outside the published yaver.io source tree so we don't bloat the open-source repo).

- Expo + Convex + NativeWind stack
- 8 seed recipes, 3 intentional bugs (null price, null step duration, null imageUrl) — spec has the exact seed data
- Total build time: ~1 week for one person, less if we accept a rough first pass

Must exist before any video records, because all three storyboards use it.

## Shoot order

1. Build Bento app (~1 week).
2. Wire scripted-scenario MVP for Video 3 (~3–4 days).
3. Accessibility labels on Yaver host chrome (~half day).
4. Record Video 1 (Full Loop) — all needed infra shipped.
5. Record Video 2 (Push & Fix) — all needed infra shipped.
6. Record Video 3 (Auto Test) — MVP version.
7. Post-shoot: upgrade Video 3 to fully-autonomous planner if we want to re-record v2 later.

## Open questions before shooting

- **Where does Bento live?** Separate repo keeps `yaver.io/` clean. Check with owner.
- **Template choice in the wizard for Bento** — `saas-complete` is closest but assumes a web surface. Need a "mobile-first" preset in `desktop/agent/template.go` or rename `saas-complete` → pick "indie-hacker" + add Expo. Probably just add a fifth template: `mobile-expo-convex`.
- **Test device** — which iPhone are we recording on? WDA setup has to be done once on that specific device before shoot day.
- **Convex local port collisions** — if the recording machine already has another Convex project running on 6791, the demo will fight for the port. Either stop other services before shoot or give the wizard a `convex.port` answer.
