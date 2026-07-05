// HN-LAUNCH-HIDE-PAID: single source of truth for temporarily hiding paid /
// managed-cloud / managed-relay / billing / metered surfaces across the web
// app (landing + logged-in dashboard) for the HN launch, so the product reads
// as free + open-source + self-hosted. Flip to `false` to restore every gated
// surface at once. Grep the token `HN-LAUNCH-HIDE-PAID` to find them all
// (web here, mobile in mobile/src/lib/launchFlags.ts).
export const HIDE_PAID_UI = true;
