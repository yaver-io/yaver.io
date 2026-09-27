import assert from "node:assert/strict";
import test from "node:test";
import {
  desktopReleaseAssetNames,
  findNewestDesktopReleaseAsset,
  isDesktopReleaseSlug,
} from "./desktopReleaseAssets.ts";

test("desktop release resolver chooses the newest published asset that really exists", () => {
  const releases = [
    {
      tag_name: "v1.99.469",
      assets: [{ name: "yaver-darwin-arm64.tar.gz", browser_download_url: "https://github.com/yaver-io/yaver.io/releases/download/v1.99.469/yaver-darwin-arm64.tar.gz" }],
    },
    {
      tag_name: "gui/v0.1.11",
      draft: true,
      assets: [{ name: "yaver-gui-0.1.11-mac-arm64.dmg", browser_download_url: "https://github.com/yaver-io/yaver.io/releases/download/gui/v0.1.11/yaver-gui-0.1.11-mac-arm64.dmg" }],
    },
    {
      tag_name: "gui/v0.1.10",
      assets: [{ name: "yaver-gui-0.1.10-mac-arm64.dmg", browser_download_url: "https://github.com/yaver-io/yaver.io/releases/download/gui/v0.1.10/yaver-gui-0.1.10-mac-arm64.dmg" }],
    },
  ];

  assert.equal(
    findNewestDesktopReleaseAsset(releases, "macos-arm64"),
    "https://github.com/yaver-io/yaver.io/releases/download/gui/v0.1.10/yaver-gui-0.1.10-mac-arm64.dmg",
  );
});

test("desktop release resolver rejects lookalike URLs outside the canonical GitHub repo", () => {
  assert.equal(findNewestDesktopReleaseAsset([{
    tag_name: "gui/v9.9.9",
    assets: [{
      name: "yaver-gui-9.9.9-win-x64-setup.exe",
      browser_download_url: "https://example.invalid/yaver-gui-9.9.9-win-x64-setup.exe",
    }],
  }], "windows-x64"), null);
});

test("all public stable download slugs have deterministic release filenames", () => {
  assert.deepEqual(desktopReleaseAssetNames("windows-x64", "0.1.11"), [
    "yaver-gui-0.1.11-win-x64-setup.exe",
    "yaver-gui-0.1.11-win-setup.exe",
  ]);
  assert.equal(isDesktopReleaseSlug("linux-deb-arm64"), true);
  assert.equal(isDesktopReleaseSlug("../../private"), false);
});
