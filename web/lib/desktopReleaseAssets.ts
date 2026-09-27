export type DesktopReleaseSlug =
  | "macos-arm64"
  | "macos-x64"
  | "windows-x64"
  | "linux-appimage-x64"
  | "linux-appimage-arm64"
  | "linux-deb-x64"
  | "linux-deb-arm64"
  | "linux-rpm-x64"
  | "linux-rpm-arm64";

export type GitHubDesktopRelease = {
  tag_name: string;
  draft?: boolean;
  prerelease?: boolean;
  assets?: Array<{ name?: string; browser_download_url?: string }>;
};

const RELEASE_DOWNLOAD_PREFIX =
  "https://github.com/yaver-io/yaver.io/releases/download/";

export function desktopReleaseAssetNames(
  slug: DesktopReleaseSlug,
  version: string,
): readonly string[] {
  const prefix = `yaver-gui-${version}`;
  switch (slug) {
    case "macos-arm64":
      return [`${prefix}-mac-arm64.dmg`];
    case "macos-x64":
      return [`${prefix}-mac-x64.dmg`];
    case "windows-x64":
      return [`${prefix}-win-x64-setup.exe`, `${prefix}-win-setup.exe`];
    case "linux-appimage-x64":
      return [`${prefix}-linux-x86_64.AppImage`];
    case "linux-appimage-arm64":
      return [`${prefix}-linux-arm64.AppImage`];
    case "linux-deb-x64":
      return [`${prefix}-linux-amd64.deb`];
    case "linux-deb-arm64":
      return [`${prefix}-linux-arm64.deb`];
    case "linux-rpm-x64":
      return [`${prefix}-linux-x86_64.rpm`];
    case "linux-rpm-arm64":
      return [`${prefix}-linux-aarch64.rpm`];
  }
}

export function findNewestDesktopReleaseAsset(
  releases: readonly GitHubDesktopRelease[],
  slug: DesktopReleaseSlug,
): string | null {
  for (const release of releases) {
    const version = release.tag_name.match(/^gui\/v(.+)$/)?.[1];
    if (!version || release.draft || release.prerelease) continue;
    const names = new Set(desktopReleaseAssetNames(slug, version));
    for (const asset of release.assets || []) {
      if (!asset.name || !asset.browser_download_url || !names.has(asset.name)) continue;
      if (!asset.browser_download_url.startsWith(RELEASE_DOWNLOAD_PREFIX)) continue;
      return asset.browser_download_url;
    }
  }
  return null;
}

export function isDesktopReleaseSlug(value: string): value is DesktopReleaseSlug {
  return [
    "macos-arm64",
    "macos-x64",
    "windows-x64",
    "linux-appimage-x64",
    "linux-appimage-arm64",
    "linux-deb-x64",
    "linux-deb-arm64",
    "linux-rpm-x64",
    "linux-rpm-arm64",
  ].includes(value);
}
