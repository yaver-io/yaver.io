/**
 * App-only surface detection (web).
 *
 * The product rule (AGENTS.md + docs/handoff/electron-gui-2026-08-12.md):
 * the desktop GUI and the web app are "sign-in → app only". Marketing chrome
 * (header nav: Pricing / FAQ / Docs / Developers / Download / Blog, search,
 * footer) must never render on the login page or on the app surfaces — the
 * page IS the product there.
 *
 * The prefix set mirrors electron/src/navigation-policy.js
 * (ALLOWED_PATH_PREFIXES) so the shell and the web app agree on what counts
 * as the app. Keep the two lists in sync when one changes.
 */
export const APP_SURFACE_PREFIXES = [
  "/auth", // login / signup / device-code / totp / callbacks
  "/api", // route handlers (never rendered as a page, but harmless)
  "/dashboard", // the app itself
  "/d/", // agent proxy (relay-backed device pages)
  "/survey", // post-signup onboarding
  "/pair",
  "/device",
  "/account",
  "/delete-account",
  "/add-device",
  "/admin",
  "/workspace",
  "/studio",
  "/render",
  "/dev",
  "/shortcut",
  "/spatial",
  "/j",
];

/** True when `pathname` is an app-only surface (no marketing chrome). */
export function isAppSurfacePath(pathname: string): boolean {
  return APP_SURFACE_PREFIXES.some((prefix) => {
    if (pathname === prefix) return true;
    // "/d/" already ends in a slash — appending another would require "/d//".
    if (prefix.endsWith("/")) return pathname.startsWith(prefix);
    return pathname.startsWith(prefix + "/");
  });
}
