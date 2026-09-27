import { NextResponse } from "next/server";
import {
  findNewestDesktopReleaseAsset,
  isDesktopReleaseSlug,
  type GitHubDesktopRelease,
} from "@/lib/desktopReleaseAssets";

const RELEASES_API =
  "https://api.github.com/repos/yaver-io/yaver.io/releases?per_page=100";
const RELEASES_PAGE = "https://github.com/yaver-io/yaver.io/releases";

export async function GET(
  _request: Request,
  context: { params: Promise<{ slug: string }> },
) {
  const { slug } = await context.params;
  if (!isDesktopReleaseSlug(slug)) {
    return NextResponse.json({ error: "Unknown desktop download" }, { status: 404 });
  }

  try {
    const response = await fetch(RELEASES_API, {
      headers: {
        Accept: "application/vnd.github+json",
        "User-Agent": "yaver.io-desktop-download",
      },
      next: { revalidate: 300 },
    });
    if (!response.ok) throw new Error(`GitHub returned HTTP ${response.status}`);
    const releases = (await response.json()) as GitHubDesktopRelease[];
    const asset = findNewestDesktopReleaseAsset(releases, slug);
    if (asset) {
      const redirect = NextResponse.redirect(asset, 307);
      redirect.headers.set("Cache-Control", "public, max-age=300, stale-while-revalidate=3600");
      return redirect;
    }
  } catch {
    // GitHub is the artifact host and the safe fallback. Never substitute an
    // unsigned mirror or a similarly-named asset when discovery is unavailable.
  }

  return NextResponse.redirect(RELEASES_PAGE, 307);
}
