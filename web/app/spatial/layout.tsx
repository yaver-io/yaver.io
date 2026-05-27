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
};

export default function SpatialLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
