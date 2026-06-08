// sandboxRootfsManifest.ts — the single source of truth for the Android
// on-device sandbox rootfs download (RootfsInstaller.kt ← installRootfs).
//
// The Alpine arm64 rootfs (node · npm · git · ripgrep · bash · hermesc +
// claude/codex/opencode) is NOT bundled in the APK — it's downloaded + sha256-
// verified + extracted on first enable, so the APK stays small and the rootfs is
// versioned independently. This file pins the URL / sha256 / version that the
// enablement UI (app/local-box.tsx) passes to installRootfs().
//
// The tarball is produced by scripts/build-android-rootfs-alpine-arm64.sh and
// published as a GitHub Release asset by scripts/publish-android-rootfs.sh.
// Until that asset is live, ROOTFS_PUBLISHED stays false and the UI tells the
// user the rootfs isn't hosted yet instead of letting installRootfs hit a 404.
//
// When you publish a new rootfs:
//   1. build:    scripts/build-android-rootfs-alpine-arm64.sh --version <ver>
//   2. publish:  scripts/publish-android-rootfs.sh <ver>      (gh release upload)
//   3. update:   bump version/sha256/sizeBytes below + flip ROOTFS_PUBLISHED true

export interface RootfsManifest {
  /** Version stamp written to <rootfs>/.installed; installRootfs is a no-op when
   *  this matches the installed version (unless force). */
  version: string;
  /** Direct download URL of the .tar.gz release asset. */
  url: string;
  /** Lowercase hex sha256 of the tarball — RootfsInstaller rejects a mismatch. */
  sha256: string;
  /** Compressed size in bytes (for the download progress bar before headers). */
  sizeBytes: number;
}

/** Canonical rootfs the current app build expects. sha256 + sizeBytes are the
 *  locally-built+verified artifact (out/android-rootfs/, 2026-06-08). */
export const ROOTFS_MANIFEST: RootfsManifest = {
  version: "2026-06-08-1",
  url:
    "https://github.com/kivanccakmak/yaver-models/releases/download/" +
    "rootfs-2026-06-08-1/yaver-rootfs-alpine-arm64.tar.gz",
  sha256: "131aa5685838300afb789c82fc7f4f2eff324f8e8b352199b612167fd0ef2b57",
  sizeBytes: 39752910,
};

/** Flip to true once scripts/publish-android-rootfs.sh has uploaded the asset at
 *  ROOTFS_MANIFEST.url. Until then the enablement UI shows "not yet hosted" and
 *  disables the Install button — installRootfs would otherwise 404. */
export const ROOTFS_PUBLISHED = false;
