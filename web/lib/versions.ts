/**
 * Web-side version constants, kept in sync by scripts/sync-versions.sh.
 *
 * versions.json (repo root) is the single source of truth; this file is the
 * web-app copy because the web tsconfig cannot import JSON from outside the
 * web/ project root. sync-versions.sh rewrites GUI_VERSION here whenever
 * versions.json's `gui` key changes.
 *
 * Desktop GUI release artifacts are named deterministically by
 * electron/package.json's artifactName pattern. URLs pin the component tag;
 * the repository's generic "latest" release can be a CLI/mobile release and
 * must never decide which desktop bytes a user downloads.
 */
export const GUI_VERSION = "0.1.16";
export const GUI_WINDOWS_VERSION = "0.1.2";
export const WINDOWS_STORE_URL = "https://apps.microsoft.com/detail/9NCMRQ0SXCS9";
// Public pages use stable same-origin routes that resolve only an asset which
// actually exists in a published GitHub GUI release. This prevents a source
// version bump from turning every landing-page button into a 404 before the
// signed artifacts finish building. GitHub remains the artifact host.
export const GUI_DOWNLOADS = {
  macArm64: "/download/desktop/macos-arm64",
  macX64: "/download/desktop/macos-x64",
  winX64: "/download/desktop/windows-x64",
  linuxX64: "/download/desktop/linux-appimage-x64",
  linuxArm64: "/download/desktop/linux-appimage-arm64",
  debX64: "/download/desktop/linux-deb-x64",
  debArm64: "/download/desktop/linux-deb-arm64",
  rpmX64: "/download/desktop/linux-rpm-x64",
  rpmArm64: "/download/desktop/linux-rpm-arm64",
} as const;
