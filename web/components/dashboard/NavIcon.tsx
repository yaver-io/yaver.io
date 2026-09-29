"use client";

/**
 * Dashboard navigation icons.
 *
 * The nav used to mix colour emoji (💻 💬 📁) with geometric glyphs (▣ ⬇ ⬇).
 * That renders inconsistently across surfaces — colourful and heavy on macOS,
 * monochrome/boxy or missing (tofu) where a colour-emoji font is not installed,
 * and visually mismatched between the emoji and the geometric marks. A single
 * stroked SVG set is theme-aware (currentColor), crisp at 16px, identical on
 * web, the Electron desktop shell, and every mobile/tablet build.
 */
import type { ReactElement, SVGProps } from "react";

const PATHS: Record<string, ReactElement> = {
  devices: (
    <>
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M8 20h8M12 16v4" />
    </>
  ),
  chat: (
    <>
      <path d="M21 12a8 8 0 0 1-8 8H8l-5 3 1.5-4.5A8 8 0 1 1 21 12Z" />
    </>
  ),
  projects: (
    <>
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
    </>
  ),
  git: (
    <>
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="12" r="2.5" />
      <path d="M6 8.5v7M8.5 6H14a2 2 0 0 1 2 2v1.5" />
    </>
  ),
  runtime: (
    <>
      <path d="M13 2 4 14h7l-1 8 9-12h-7l1-8Z" />
    </>
  ),
  downloads: (
    <>
      <path d="M12 3v12m0 0 4-4m-4 4-4-4M4 19h16" />
    </>
  ),
};

export default function NavIcon({
  id,
  className = "h-4 w-4",
  ...rest
}: { id: string; className?: string } & SVGProps<SVGSVGElement>) {
  const glyph = PATHS[id];
  if (!glyph) return <span className={className} aria-hidden />;
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
      {...rest}
    >
      {glyph}
    </svg>
  );
}
