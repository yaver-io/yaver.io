// SVG badge embeddable on any site — proof that a project ships via Yaver.
// Free, open, and a lightweight viral surface.
//
//   <a href="https://yaver.io"><img src="https://yaver.io/badge" alt="Built with Yaver" /></a>
//
// Dark and light variants via ?theme=light.

import { NextResponse } from "next/server";

export const runtime = "edge";

export function GET(req: Request) {
  const theme = new URL(req.url).searchParams.get("theme") === "light" ? "light" : "dark";
  const bg = theme === "light" ? "#ffffff" : "#0b0d10";
  const fg = theme === "light" ? "#0b0d10" : "#e5e7eb";
  const accent = "#818cf8";
  const border = theme === "light" ? "#e5e7eb" : "#1f2937";

  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="148" height="28" viewBox="0 0 148 28" role="img" aria-label="Built with Yaver">
  <rect x="0.5" y="0.5" width="147" height="27" rx="4" fill="${bg}" stroke="${border}"/>
  <g fill="${fg}" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="11">
    <text x="10" y="18">Built with</text>
    <text x="74" y="18" fill="${accent}" font-weight="700">Yaver</text>
    <text x="113" y="18" fill="${fg}" font-size="10" opacity="0.6">→ yaver.io</text>
  </g>
</svg>`;

  return new NextResponse(svg, {
    headers: {
      "Content-Type": "image/svg+xml",
      "Cache-Control": "public, max-age=3600, s-maxage=86400",
    },
  });
}
