/**
 * providerLogoSvg — inline SVG brand marks for the OAuth providers the
 * Feedback SDK supports. Matches the mobile LoginScreen's Ionicons set so
 * the auth step of both surfaces feels like the same product.
 *
 * Single-color monochrome glyphs (brand-tinted) at 18×18, no external
 * font/icon dependency. Safe to inline via `element.innerHTML`: each path
 * is hand-checked, no user input is interpolated in.
 */

export type LogoId =
  | 'apple'
  | 'google'
  | 'github'
  | 'gitlab'
  | 'microsoft'
  | 'email';

interface LogoTheme {
  color: string;
  /** SVG inner markup (already sized to a 24×24 viewBox). */
  body: string;
}

// Paths are simplified, single-color vector marks suitable for a small
// "Continue with X" row. Brand colors are pulled from each provider's
// published guidelines so no logo looks off-brand at a glance.
const LOGOS: Record<LogoId, LogoTheme> = {
  apple: {
    color: '#ffffff',
    body:
      '<path fill="currentColor" d="M16.4 12.4c0-2.3 1.9-3.4 2-3.5-1.1-1.6-2.7-1.8-3.3-1.8-1.4-.1-2.7.8-3.4.8-.7 0-1.8-.8-3-.8-1.5 0-3 .9-3.8 2.3-1.6 2.8-.4 6.9 1.2 9.2.8 1.1 1.7 2.3 2.9 2.3 1.2 0 1.6-.7 3-.7 1.4 0 1.8.7 3 .7 1.3 0 2.1-1.1 2.8-2.2.9-1.3 1.3-2.5 1.3-2.6-.1 0-2.7-1-2.7-3.7zM14.4 5.6c.6-.7 1-1.8.9-2.9-.9 0-2 .6-2.6 1.3-.6.6-1.1 1.7-.9 2.8 1 .1 2.1-.5 2.6-1.2z"/>',
  },
  google: {
    color: '#e0e0e0',
    body:
      '<path fill="#4285F4" d="M21.6 12.2c0-.8-.1-1.4-.2-2H12v3.8h5.4c-.1.9-.7 2.3-2.1 3.2l3.1 2.4c1.8-1.7 2.9-4.2 2.9-7.3z"/>' +
      '<path fill="#34A853" d="M12 21.5c2.7 0 5-.9 6.7-2.4l-3.2-2.5c-.9.6-2 1-3.5 1-2.7 0-5-1.8-5.8-4.3l-3.3 2.5c1.6 3.3 5.1 5.7 9.1 5.7z"/>' +
      '<path fill="#FBBC05" d="M6.2 13.3c-.2-.6-.3-1.3-.3-2s.1-1.3.3-2L2.9 6.8c-.7 1.4-1.1 3-1.1 4.6s.4 3.2 1.1 4.6l3.3-2.7z"/>' +
      '<path fill="#EA4335" d="M12 5c1.9 0 3.2.8 4 1.5l2.9-2.8C17 2.1 14.7 1.2 12 1.2c-4 0-7.5 2.3-9.1 5.6l3.3 2.5c.8-2.4 3.1-4.3 5.8-4.3z"/>',
  },
  github: {
    color: '#ffffff',
    body:
      '<path fill="currentColor" d="M12 2.2A10 10 0 0 0 8.8 21.7c.5.1.7-.2.7-.5v-1.7c-2.8.6-3.4-1.3-3.4-1.3-.4-1.2-1.1-1.5-1.1-1.5-.9-.6.1-.6.1-.6 1 .1 1.5 1 1.5 1 .9 1.5 2.4 1.1 3 .8.1-.7.4-1.1.6-1.4-2.2-.3-4.6-1.1-4.6-5 0-1.1.4-2 1-2.7-.1-.3-.4-1.3.1-2.7 0 0 .9-.3 2.8 1 .8-.2 1.7-.3 2.5-.3s1.7.1 2.5.3c1.9-1.3 2.8-1 2.8-1 .5 1.4.2 2.4.1 2.7.6.7 1 1.6 1 2.7 0 3.9-2.4 4.7-4.6 5 .4.3.7.9.7 1.9v2.7c0 .3.2.6.7.5A10 10 0 0 0 12 2.2z"/>',
  },
  gitlab: {
    color: '#FC6D26',
    body:
      '<path fill="currentColor" d="M22 13.4 20.7 9.2 18.2 1.6c-.1-.4-.7-.4-.8 0l-2.5 7.6H9.1L6.6 1.6c-.1-.4-.7-.4-.8 0L3.3 9.2 2 13.4c-.1.4 0 .8.4 1l9.6 7 9.6-7c.4-.2.5-.6.4-1z"/>',
  },
  microsoft: {
    color: '#e0e0e0',
    body:
      '<path fill="#F25022" d="M3 3h8.5v8.5H3z"/>' +
      '<path fill="#7FBA00" d="M12.5 3H21v8.5h-8.5z"/>' +
      '<path fill="#00A4EF" d="M3 12.5h8.5V21H3z"/>' +
      '<path fill="#FFB900" d="M12.5 12.5H21V21h-8.5z"/>',
  },
  email: {
    color: '#9ca3af',
    body:
      '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" d="M4 6h16v12H4z"/>' +
      '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" d="m4 7 8 6 8-6"/>',
  },
};

/**
 * Return a standalone `<svg>` element (as an HTML string) for the given
 * provider. Width/height default to 18; pass `size` to override. Callers
 * interpolate the returned string directly into `innerHTML` — nothing is
 * user-supplied, so this is safe.
 */
export function providerLogoSvg(id: LogoId, size = 18): string {
  const theme = LOGOS[id];
  if (!theme) return '';
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" ` +
    `width="${size}" height="${size}" aria-hidden="true" ` +
    `style="flex-shrink:0;color:${theme.color};">` +
    theme.body +
    `</svg>`
  );
}
