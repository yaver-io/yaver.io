/**
 * /spatial — minimal wrapper for headset / HUD / WebView preview surfaces.
 *
 * The root app/layout.tsx already owns <html> and <body> (Next.js app
 * router rule: only one layout per route tree has them). This layout
 * exists only so we can scope metadata + a transparent backdrop without
 * fighting the root layout's marketing chrome (the page itself uses
 * position:fixed; inset:0; z-index:9999 to overlay it).
 */

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Yaver — Spatial",
  description: "Hands-free Claude Code on smart glasses, VR, and AR headsets",
  // Quest Browser + Vision Pro Safari read this on first visit and offer
  // "Add to Home" → one-tap launch into immersive content. Separate from
  // the root /manifest.webmanifest so the install action lands on /spatial
  // instead of the marketing homepage.
  manifest: "/spatial-manifest.webmanifest",
};

export default function SpatialLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
