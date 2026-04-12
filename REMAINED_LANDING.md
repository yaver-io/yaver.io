# REMAINED_LANDING.md

Items from the landing-page rewrite spec that were **not** implemented in Phase 1. The rewrite of `web/app/page.tsx` is complete (hero, demo, get-started, create-a-project, dashboard, device testing, deploy, any-agent, free-forever, MCP, FAQ, footer — 10 sections + FAQ + footer). Every spec item below is either a follow-up, content that needs assets, or something intentionally deferred.

## Phase 2 — assets to produce

1. **Demo videos — 2 of 3 tabs need recording.**
   - `Full Loop` tab currently reuses the existing `/demo.mp4`. OK for now.
   - `Bug Fix` tab → shows "Coming soon" placeholder. Needs `/demo-feedback.mp4`.
   - `Task Queue` tab → shows "Coming soon" placeholder. Needs `/demo-autotest.mp4` (or rename).
   - Per user's direction: skipped for this PR; record later.

2. **Real yaver.io web UI screenshots.** Section 5 (Your Dashboard) currently uses a side-by-side text comparison (`DashboardComparison` component). Spec calls for real dashboard screenshots + tunnel animation once the Convex/Supabase dashboard view is actually wired up in the app. Track with `project_convex_local.md`.

3. **Interactive ProjectWizardPreview.** Spec says static card "looks polished" — that's what's shipped. Future polish: animate green checkmarks in on scroll, clickable mock steps.

4. **A/B test hero copy variants.** Not set up. Would need `web/lib/ab.ts` + Convex events.

## Cut from current page, moved to /docs (or dropped)

- **Systemd / launchd "runs forever" section.** Spec said move to `/docs`. Not yet moved — the content is currently only in `CLAUDE.md`. Action: add a `/docs/systemd.md` or append to an existing docs page when someone next touches docs.
- **"All install methods" table (brew, apt, AUR, Docker, Nix, Scoop).** Kept only as a "All install methods" link pointing to `/download`. If `/download` doesn't list them, migrate the old table there.
- **Integrations section (large comparison table at end of old page).** Dropped from landing. If still wanted, hoist into `/integrations` page (already linked in footer of old version — footer now drops it).
- **"Built for solo founders" three-card section.** Dropped per spec ("weave sentiment into hero + throughout page"). Sentiment now lives in hero + FAQ tone.
- **Separate Flutter + Web Feedback SDK code blocks.** Dropped; the RN code block + "Available for: RN / Flutter / Web" badges replace them per spec.

## Structural TODOs (follow-ups, not blockers)

- `/integrations` route still exists (from old footer link) — verify it renders something sensible or delete it. New footer removed the link.
- `/pricing` route still exists under `web/app/pricing/` — spec says Yaver has no paid tier. Confirm this page either redirects to the "Free forever" section or is deleted. Footer no longer links to it.
- `sitemap.ts` should be checked against the new URL set (hero now anchors `#get-started` and `#faq` instead of `#features`).
- Open Graph image (`/og-image.png`) still reflects old positioning ("Use Any AI Agent from Anywhere"). Needs a new OG render matching "Your machine is your cloud."

## Not in spec but worth noting

- The old page had a Google Play SVG that was mis-rendered (rocket silhouette instead of the Play triangle). Replaced with a standard Play triangle glyph in the new page. Double-check at design review.
- New 4 helper components live at the bottom of `page.tsx`: `ProjectWizardPreview`, `DashboardComparison`, `EnvironmentStepper`, `PreDeployCheck`. If the page keeps growing, split them into `web/components/landing/`.

Original page backed up at `/tmp/page_full_backup.tsx` during the rewrite (local-only, not committed).
