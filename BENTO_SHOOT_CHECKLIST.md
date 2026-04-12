# Bento Video Shoot — Readiness Checklist

The landing-page placeholders are gone until every gate below passes. This
file tracks *what is actually verified working* vs *what still blocks
recording*. No dummy footage, no faking it in post.

---

## ✅ Verified working (run these commands to re-verify)

### Bento itself compiles and bundles

```bash
cd demos/bento/apps/mobile
npm install --legacy-peer-deps
npx tsc --noEmit               # 0 errors — Bento typechecks
npx expo export --platform ios --output-dir /tmp/bento-export
# Expected: 3.76 MB .hbc at /tmp/bento-export/_expo/static/js/ios/entry-*.hbc
```

### Yaver scaffold produces Bento bit-for-bit

```bash
cd desktop/agent && go build -o /tmp/yaver .
mkdir -p /tmp/bento-reproduce
/tmp/yaver new --quick demos/bento/.yaver-wizard-answers.json /tmp/bento-reproduce
# Expected: {"ok":true,"directory":"/tmp/bento-reproduce/bento","files":[19 paths]}
diff -q <(ls /tmp/bento-reproduce/bento) <(ls demos/bento)   # identical skeleton
```

### Agent wiring for Videos 1 & 2 (source-level)

- Gap 2 (auto-start services): `desktop/agent/project_wizard.go:519-526` calls
  `ServicesManager.Start()` after generation and surfaces the result in the
  response (`servicesStarted`, `servicesLog`, `servicesError`).
- Gap 4 (auto hot-reload on task completion): `desktop/agent/main.go:1796-1807`
  calls `devServerMgr.Reload()` + broadcasts a `BlackBoxCommand{"reload"}` the
  moment any task finishes `completed` while a dev server is running.
- Gap 1 (project-scoped Chat): `mobile/app/(tabs)/project.tsx:124` links to
  `/(tabs)/tasks?dir=...`, and `tasks.tsx:328-333` forwards that as `workDir`
  through `sendTask`.
- Gap 3 (real dashboard tunnel): `mobile/app/dashboard-view.tsx` renders a
  `WebView` pointed at `${baseUrl}/proxy/${kind}/` with auth headers injected,
  which the agent reverse-proxies via `desktop/agent/studio_proxy.go:71`.

### 3 intentional Bento bugs in place (and survive tsc)

```bash
grep -n "INTENTIONAL" demos/bento/apps/mobile/app/recipe/*.tsx \
  demos/bento/apps/mobile/app/components/*.tsx
# Expected: 3 markers
#   app/recipe/[id].tsx — imageUrl cast, no fallback (Video 1 fixes)
#   app/components/CookTimer.tsx — step.duration cast, missing ?? (auto-test fixes)
#   app/components/GroceryTotal.tsx — i.price! assertion, crashes on null (shake-to-report fixes)
```

---

## ❌ Hard blockers for recording day

These must be green on the shoot machine before anyone hits record.

### 1. Docker daemon must be running

```bash
docker info >/dev/null && echo OK
# Today on this box: NOT OK. Video 1 needs convex-backend + convex-dashboard
# containers, which the wizard wires into .yaver/services.yaml and then tries
# to `docker compose up -d` during GenerateProject.
```

**If this is red on shoot day → Video 1 fails at "tap [Create Project]"** —
`servicesError: "Cannot connect to the Docker daemon"` will show in the mobile
UI instead of the "✅ Convex ready" line.

### 2. Yaver serve authed + mobile paired

```bash
yaver status
# Today: "Auth: ● not signed in"  "Mode: bootstrap"
```

Needed: run `yaver auth` on the shoot Mac (browser OAuth) AND sign the
mobile app into the same account AND let the mobile see this Mac in its
device list. Without this, the mobile app cannot reach the agent at all, so
**no video shoots.**

### 3. A runner must be configured

```bash
yaver set-runner claude    # or codex / aider / ollama
jq '.runner, .runner_config' ~/.yaver/config.json   # should be non-null
```

Without a runner, every `POST /tasks` returns the task in `queued` forever
and the chat in Video 1 produces no output.

### 4. Bento's Feedback SDK integration (T4.2) is not done

Video 2 ("shake to report") requires `<FloatingButton />` + `BlackBox.start()`
inside Bento's `app/_layout.tsx`. Not yet added. Until it is, there is no
floating Yaver button to shake into.

**Est: ~30 min of vibe-coding. Prompt is queued as T4.2 in `BENTO_BUILD_QUEUE.md`.**

### 5. Yaver host-chrome accessibility labels (for Video 3 only)

Video 3's scripted-MVP autotest uses WDA to tap the Yaver host UI. The tab
bar and FeedbackOverlay need stable `testID`s. Unassigned today.

**Est: ~4 hours.**

### 6. Video 3 scripted-MVP runner itself

The `yaver autotest bento <scenario.yaml>` command described in
`BENTO_VIDEO_ROADMAP.md` is not yet written. Testkit drivers exist, glue
does not.

**Est: 3–4 days.** See roadmap for file-level task list.

---

## 🟡 Soft blockers (video is shootable, polish later)

- **T3.5 (Favorites + Profile tabs)** — still stubs. Video 1/2/3 don't actually
  enter these tabs so not blocking; leaving as stubs is fine on camera.
- **Better Auth (T4.1)** — the Convex auth tables exist but the Bento sign-in
  screen isn't wired. Videos open the app with the splash → tabs, no auth
  gate. Not blocking unless a storyboard adds "log in" as a step.
- **Bento needs to be PUSHED to a physical phone** via `yaver-cli push`
  before Video 2 can record. Works end-to-end today for properly configured
  RN projects, but not yet dry-run-tested against `demos/bento/`. ~15 min to
  run through once.

---

## Recommended shoot-day run-of-show verification (60 minutes)

1. `docker start` (or boot Docker Desktop) — gate 1.
2. `yaver auth` + sign the mobile app in — gate 2.
3. `yaver set-runner claude` — gate 3.
4. On the mobile app: `[+ New Project]` → name "bento-shoot" → Convex
   local → Create. Watch for `servicesStarted: true` in the response.
5. Tap `[Database]` → real Convex dashboard should load in the webview.
6. Tap `[Chat]` → type "Add a recipes table with title and cookTime" →
   watch task stream. On completion, auto hot-reload fires (gap 4).
7. `cd demos/bento/apps/mobile && npx yaver-cli push` — bundle lands on
   a paired physical phone.
8. Shake phone on the Grocery tab → see the SDK overlay (blocked until
   T4.2 ships).

If steps 1–7 all succeed with zero manual patching of Yaver's code, we
can shoot Videos 1 & 2 the same day. Video 3 stays blocked until the
autotest MVP + accessibility labels land.

---

## What's already committed for the shoot (by commit hash)

| SHA | What |
|---|---|
| `27e6ba2c` | Agent: auto-start local backend + auto hot-reload on task done |
| `c9c2d1f3` | Mobile: project-scoped Chat + dashboard WebView route |
| `a393be6b` | demos/bento: Yaver-scaffolded skeleton + build queue + pipeline |
| `1f4db430` | Bento Tier 1: recipes/favorites/grocery schema + seeds with 3 bugs |
| `538a0036` | Bento Tier 2 + T3.1–T3.4: tabs, NativeWind, 4 screens |
| (this) | Bento mobile builds cleanly; lockfile committed; shoot checklist |
