import { NextRequest, NextResponse } from "next/server";
import {
  DOWNLOAD_SLUGS,
  VERIFIED_DOWNLOAD_SLUGS,
  fetchDownloadFallbacks,
  fetchDownloads,
  findDownload,
} from "@/lib/downloads";

export async function GET(
  _request: NextRequest,
  context: { params: Promise<{ slug: string }> }
) {
  const { slug } = await context.params;
  const target = DOWNLOAD_SLUGS[slug as keyof typeof DOWNLOAD_SLUGS];

  if (!target) {
    return NextResponse.redirect("https://yaver.io/download", { status: 307 });
  }

  if (!VERIFIED_DOWNLOAD_SLUGS.has(slug as keyof typeof DOWNLOAD_SLUGS)) {
    return NextResponse.redirect("https://yaver.io/download", { status: 307 });
  }

  try {
    const [downloads, fallbacks] = await Promise.all([
      fetchDownloads(),
      fetchDownloadFallbacks(),
    ]);
    const match = findDownload(downloads, target);
    if (match?.url) {
      return NextResponse.redirect(match.url, { status: 307 });
    }

    const fallback = fallbacks[slug as keyof typeof fallbacks];
    if (fallback?.href) {
      return NextResponse.redirect(fallback.href, { status: 307 });
    }
  } catch {
    // Fall back to GitHub when storage lookup is unavailable.
  }

  return NextResponse.redirect("https://yaver.io/download", { status: 307 });
}
