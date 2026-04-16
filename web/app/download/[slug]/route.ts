import { NextRequest, NextResponse } from "next/server";
import { DOWNLOAD_SLUGS, fetchDownloads, findDownload } from "@/lib/downloads";

const FALLBACK_RELEASES_URL = "https://github.com/kivanccakmak/yaver.io/releases/latest";

export async function GET(
  _request: NextRequest,
  context: { params: Promise<{ slug: string }> }
) {
  const { slug } = await context.params;
  const target = DOWNLOAD_SLUGS[slug as keyof typeof DOWNLOAD_SLUGS];

  if (!target) {
    return NextResponse.redirect(FALLBACK_RELEASES_URL, { status: 307 });
  }

  try {
    const downloads = await fetchDownloads();
    const match = findDownload(downloads, target);
    if (match?.url) {
      return NextResponse.redirect(match.url, { status: 307 });
    }
  } catch {
    // Fall back to GitHub releases when storage lookup is unavailable.
  }

  return NextResponse.redirect(FALLBACK_RELEASES_URL, { status: 307 });
}
