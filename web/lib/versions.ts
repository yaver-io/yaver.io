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
export const GUI_VERSION = "0.1.11";
export const GUI_WINDOWS_VERSION = "0.1.2";
export const GUI_BASE_URL =
  `https://github.com/yaver-io/yaver.io/releases/download/gui/v${GUI_VERSION}`;
export const GUI_WINDOWS_BASE_URL =
  `https://github.com/yaver-io/yaver.io/releases/download/gui/v${GUI_WINDOWS_VERSION}`;
export const GUI_DOWNLOADS = {
  macArm64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-mac-arm64.dmg`,
  macX64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-mac-x64.dmg`,
  winX64: `${GUI_WINDOWS_BASE_URL}/yaver-gui-${GUI_WINDOWS_VERSION}-win-setup.exe`,
  linuxX64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-linux-x86_64.AppImage`,
  linuxArm64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-linux-arm64.AppImage`,
  debX64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-linux-amd64.deb`,
  debArm64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-linux-arm64.deb`,
  rpmX64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-linux-x86_64.rpm`,
  rpmArm64: `${GUI_BASE_URL}/yaver-gui-${GUI_VERSION}-linux-aarch64.rpm`,
} as const;
