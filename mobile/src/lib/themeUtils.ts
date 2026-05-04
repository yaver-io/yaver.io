export function withAlpha(hex: string, alpha: string): string {
  if (!hex.startsWith("#")) return hex;
  if (hex.length === 7) return `${hex}${alpha}`;
  return hex;
}
